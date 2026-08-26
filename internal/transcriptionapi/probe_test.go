package transcriptionapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadFixtureCatalogValidatesTheCheckedInWebMCorpus(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return the probe test path")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..", "testdata", "transcription-fixtures")
	fixtures, err := LoadFixtureCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != DefaultFixtureCount {
		t.Fatalf("fixture count = %d, want %d", len(fixtures), DefaultFixtureCount)
	}
	for _, fixture := range fixtures {
		if fixture.MIMEType != "audio/webm" || filepath.Ext(fixture.FileName) != ".webm" || fixture.Bytes <= 0 {
			t.Fatalf("invalid fixture metadata: %+v", fixture)
		}
	}
}

func TestFixtureSelectorChoosesFromTheLeastRecentlyUsedWeightedPool(t *testing.T) {
	now := time.Date(2026, 8, 20, 20, 0, 0, 0, time.UTC)
	fixtures := make([]ProbeFixture, 8)
	lastUsed := make(map[string]time.Time, len(fixtures))
	for index := range fixtures {
		id := "fixture-" + string(rune('1'+index))
		fixtures[index] = ProbeFixture{ID: id}
		lastUsed[id] = now.Add(-time.Duration(8-index) * time.Hour)
	}
	selector := newFixtureSelector(fixtures, lastUsed, 7)
	selected, ok := selector.Choose(now)
	if !ok {
		t.Fatal("Choose returned no fixture")
	}
	if selected.ID != "fixture-1" && selected.ID != "fixture-2" {
		t.Fatalf("selected %q outside the oldest quarter", selected.ID)
	}
	if !selector.Snapshot()[selected.ID].Equal(now) {
		t.Fatalf("selected fixture timestamp was not persisted in selector")
	}
}

func TestProbeHealthRequiresARecentSuccessfulProbe(t *testing.T) {
	maxAge := 10 * time.Minute
	health := NewProbeHealth(maxAge)
	provider := ProviderCapability{Provider: ProviderChatGPT, File: true, Ready: true}
	now := time.Date(2026, 8, 20, 20, 0, 0, 0, time.UTC)

	initial := health.Apply(provider, now)
	if initial.Ready || initial.ProbeReady {
		t.Fatalf("unprobed provider reported ready: %+v", initial)
	}
	health.RecordAttempt(ProviderChatGPT, now, "fixture-001")
	pending := health.Apply(provider, now)
	if pending.Ready || pending.ProbeReady || pending.ProbeReason != "file:synthetic probe is stale" && pending.ProbeReason != "file:probe_pending" {
		t.Fatalf("pending provider reported ready: %+v", pending)
	}
	if pending.FileProbe == nil || pending.FileProbe.Ready || pending.FileProbe.Reason != "probe_pending" {
		t.Fatalf("pending file path reported ready: %+v", pending.FileProbe)
	}
	health.RecordSuccess(ProviderChatGPT, now, "fixture-001")
	ready := health.Apply(provider, now.Add(time.Second))
	if !ready.Ready || !ready.ProbeReady || ready.ProbeAgeSec > 1 || ready.FileProbe == nil || !ready.FileProbe.Ready {
		t.Fatalf("recently probed provider was not ready: %+v", ready)
	}
	health.RecordPathAttempt(ProviderChatGPT, ProbePathFile, now.Add(time.Minute), "fixture-002")
	pendingFresh := health.Apply(provider, now.Add(time.Minute))
	if !pendingFresh.Ready || !pendingFresh.ProbeReady || pendingFresh.FileProbe == nil || !pendingFresh.FileProbe.Ready {
		t.Fatalf("fresh evidence was invalidated while the next probe was pending: %+v", pendingFresh)
	}
	stale := health.Apply(provider, now.Add(maxAge+time.Second))
	if stale.Ready || stale.ProbeReady || stale.ProbeReason != "file:synthetic probe is stale" || stale.FileProbe == nil || stale.FileProbe.Reason != "synthetic probe is stale" {
		t.Fatalf("stale provider reported ready: %+v", stale)
	}
}

func TestProbeHealthKeepsPathFailureAgeTiedToLastSuccess(t *testing.T) {
	health := NewProbeHealth(10 * time.Minute)
	provider := ProviderCapability{Provider: ProviderM365, File: true, Realtime: true, Ready: true}
	now := time.Date(2026, 8, 20, 20, 0, 0, 0, time.UTC)
	health.RecordPathSuccess(ProviderM365, ProbePathFile, now, "fixture-001")
	health.RecordPathSuccess(ProviderM365, ProbePathRealtime, now, "fixture-001")
	health.RecordPathFailure(ProviderM365, ProbePathRealtime, now.Add(time.Minute), "fixture-002", "m365_final_result_timeout")

	degraded := health.Apply(provider, now.Add(2*time.Minute))
	if degraded.Ready || degraded.ProbeReady {
		t.Fatalf("provider with failed realtime path reported ready: %+v", degraded)
	}
	if degraded.FileProbe == nil || !degraded.FileProbe.Ready || degraded.FileProbe.AgeSec != 120 {
		t.Fatalf("file path freshness = %+v, want a 120-second fresh success", degraded.FileProbe)
	}
	if degraded.RealtimeProbe == nil || degraded.RealtimeProbe.Ready || degraded.RealtimeProbe.Reason != "m365_final_result_timeout" || degraded.RealtimeProbe.AgeSec != 120 {
		t.Fatalf("realtime path freshness = %+v, want the failed retry and last-success age", degraded.RealtimeProbe)
	}
	if !strings.Contains(degraded.ProbeReason, "realtime:m365_final_result_timeout") {
		t.Fatalf("aggregate probe reason = %q, want realtime path context", degraded.ProbeReason)
	}
}

func TestProbeStateMigratesLegacyFlatFileHealth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe-state.json")
	legacy := `{
  "schema_version": "cdp-cli-transcription-probes/v1",
  "fixtures": {},
  "providers": {
    "chatgpt-web": {
      "last_attempt_at": "2026-08-20T20:00:01Z",
      "last_success_at": "2026-08-20T20:00:00Z",
      "last_fixture_id": "fixture-001"
    }
  }
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	state := loadProbeState(path)
	parsed := parseProbeHealthStates(state.Providers)
	fileState, ok := parsed[probeStateKey{Provider: ProviderChatGPT, Path: ProbePathFile}]
	if state.SchemaVersion != probeStateSchemaVersion || !ok || fileState.LastFixtureID != "fixture-001" || fileState.LastSuccessAt.IsZero() {
		t.Fatalf("migrated state = schema %q paths %+v", state.SchemaVersion, parsed)
	}
}

func TestSyntheticProbeCoordinatorIsolatesProvidersAndPersistsRedactedState(t *testing.T) {
	successful := &fakeProvider{id: ProviderChatGPT, result: Result{Text: "synthetic response"}}
	failed := &fakeProvider{
		id:     ProviderM365,
		err:    providerError(http.StatusUnauthorized, "provider_auth", "auth_expired", "do not persist this message", false),
		result: Result{Text: "unused"},
	}
	coordinator, err := NewSyntheticProbeCoordinator(
		NewRegistry(successful, failed),
		[]ProbeFixture{{ID: "fixture-001", Path: "/tmp/fixture-001.webm", FileName: "fixture-001.webm", MIMEType: "audio/webm", Bytes: 32}},
		t.TempDir(),
		5*time.Minute,
		time.Second,
		15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.RunOnce(context.Background())

	if successful.transcribeCall != 1 || failed.transcribeCall != 1 {
		t.Fatalf("provider calls = %d/%d, want one each", successful.transcribeCall, failed.transcribeCall)
	}
	states := coordinator.Health().Snapshot()
	if states[ProviderChatGPT].LastSuccessAt.IsZero() {
		t.Fatalf("successful provider state = %+v", states[ProviderChatGPT])
	}
	coordinator.RecordObservedFileFailure(ProviderChatGPT, "provider_transcript_unavailable")
	degraded := coordinator.Health().Apply(successful.Capabilities(context.Background()), time.Now().UTC())
	if degraded.Ready || degraded.FileProbe == nil || degraded.FileProbe.Ready || degraded.FileProbe.Reason != "provider_transcript_unavailable" {
		t.Fatalf("observed live failure did not invalidate file health: %+v", degraded)
	}
	if !states[ProviderM365].LastSuccessAt.IsZero() || states[ProviderM365].Reason != "auth_expired" {
		t.Fatalf("failed provider state = %+v", states[ProviderM365])
	}

	stateBytes, err := os.ReadFile(filepath.Join(coordinatorStateRoot(coordinator), "probe-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	stateText := string(stateBytes)
	if strings.Contains(stateText, "synthetic response") || strings.Contains(stateText, "do not persist") || strings.Contains(stateText, `"text"`) {
		t.Fatalf("probe state leaked provider content: %s", stateText)
	}
	var document probeStateDocument
	if err := json.Unmarshal(stateBytes, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != probeStateSchemaVersion || document.Providers[string(ProviderChatGPT)].File == nil || document.Providers[string(ProviderChatGPT)].File.LastFixtureID != "live-request" || document.Providers[string(ProviderChatGPT)].File.Reason != "provider_transcript_unavailable" {
		t.Fatalf("unexpected persisted probe state: %+v", document)
	}
}

type lifecycleRefreshingProbeProvider struct {
	*fakeProvider
	capabilityRefreshCalls int
}

func (p *lifecycleRefreshingProbeProvider) EnsureCapabilitiesFresh(context.Context) error {
	p.capabilityRefreshCalls++
	return nil
}

func TestSyntheticProbeCoordinatorDoesNotRefreshProviderLifecycleHooks(t *testing.T) {
	provider := &lifecycleRefreshingProbeProvider{
		fakeProvider: &fakeProvider{
			id:     ProviderM365,
			result: Result{Text: "cached-capability probe"},
		},
	}
	coordinator, err := NewSyntheticProbeCoordinator(
		NewRegistry(provider),
		[]ProbeFixture{{ID: "fixture-001", Path: "/tmp/fixture-001.webm", FileName: "fixture-001.webm", MIMEType: "audio/webm", Bytes: 32}},
		t.TempDir(),
		5*time.Minute,
		time.Second,
		15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.RunOnce(context.Background())

	provider.requestMu.Lock()
	authRefreshCalls := provider.ensureCalls
	provider.requestMu.Unlock()
	if authRefreshCalls != 0 || provider.capabilityRefreshCalls != 0 {
		t.Fatalf("lifecycle refresh calls = auth %d/capabilities %d, want zero", authRefreshCalls, provider.capabilityRefreshCalls)
	}
	if provider.transcribeCall != 1 {
		t.Fatalf("transcribe calls = %d, want one cached-capability probe", provider.transcribeCall)
	}
}

type realtimeProbeFakeProvider struct {
	*fakeProvider
	realtimeErr error
	probeCalls  int
}

func (p *realtimeProbeFakeProvider) ProbeRealtime(context.Context, ProbeFixture) error {
	p.probeCalls++
	return p.realtimeErr
}

func TestSyntheticProbeCoordinatorProbesEveryAdvertisedPath(t *testing.T) {
	provider := &realtimeProbeFakeProvider{
		fakeProvider: &fakeProvider{
			id:       ProviderM365,
			result:   Result{Text: "file response"},
			realtime: &fakeRealtime{},
		},
	}
	coordinator, err := NewSyntheticProbeCoordinator(
		NewRegistry(provider),
		[]ProbeFixture{{ID: "fixture-001", Path: "/tmp/fixture-001.webm", FileName: "fixture-001.webm", MIMEType: "audio/webm", Bytes: 32}},
		t.TempDir(),
		5*time.Minute,
		time.Second,
		15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.RunOnce(context.Background())
	if provider.transcribeCall != 1 || provider.probeCalls != 1 {
		t.Fatalf("path calls = file %d/realtime %d, want one each", provider.transcribeCall, provider.probeCalls)
	}
	capability := coordinator.Health().Apply(provider.Capabilities(context.Background()), time.Now().UTC())
	if !capability.Ready || !capability.ProbeReady || capability.FileProbe == nil || !capability.FileProbe.Ready || capability.RealtimeProbe == nil || !capability.RealtimeProbe.Ready {
		t.Fatalf("all-path capability = %+v", capability)
	}

	provider.realtimeErr = providerError(http.StatusGatewayTimeout, "provider", "m365_final_result_timeout", "realtime failed", true)
	coordinator.RunOnce(context.Background())
	capability = coordinator.Health().Apply(provider.Capabilities(context.Background()), time.Now().UTC())
	if capability.Ready || capability.ProbeReady || capability.FileProbe == nil || !capability.FileProbe.Ready || capability.RealtimeProbe == nil || capability.RealtimeProbe.Ready {
		t.Fatalf("failed realtime capability = %+v", capability)
	}
	if !strings.Contains(capability.ProbeReason, "realtime:m365_final_result_timeout") {
		t.Fatalf("failed realtime reason = %q", capability.ProbeReason)
	}
}

func coordinatorStateRoot(coordinator *SyntheticProbeCoordinator) string {
	return filepath.Dir(coordinator.statePath)
}

func TestHealthEndpointUsesProbeFreshnessAsItsReadinessGate(t *testing.T) {
	provider := &fakeProvider{id: ProviderChatGPT, result: Result{Text: "ok"}}
	store, err := NewEphemeralStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	health := NewProbeHealth(15 * time.Minute)
	server, err := NewServer(ServerConfig{
		Registry:        NewRegistry(provider),
		Store:           store,
		DefaultProvider: ProviderChatGPT,
		ProbeHealth:     health,
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	readHealth := func() map[string]any {
		response, requestErr := http.Get(httpServer.URL + "/healthz")
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		var payload map[string]any
		if decodeErr := json.NewDecoder(response.Body).Decode(&payload); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return payload
	}
	if status := readHealth()["status"]; status != "degraded" {
		t.Fatalf("unprobed health status = %v, want degraded", status)
	}
	health.RecordSuccess(ProviderChatGPT, time.Now().UTC(), "fixture-001")
	if status := readHealth()["status"]; status != "ok" {
		t.Fatalf("freshly probed health status = %v, want ok", status)
	}
}
