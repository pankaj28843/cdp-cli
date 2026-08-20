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
	if pending.Ready || pending.ProbeReady || pending.ProbeReason != "synthetic probe is stale" && pending.ProbeReason != "probe_pending" {
		t.Fatalf("pending provider reported ready: %+v", pending)
	}
	health.RecordSuccess(ProviderChatGPT, now, "fixture-001")
	ready := health.Apply(provider, now.Add(time.Second))
	if !ready.Ready || !ready.ProbeReady || ready.ProbeAgeSec > 1 {
		t.Fatalf("recently probed provider was not ready: %+v", ready)
	}
	stale := health.Apply(provider, now.Add(maxAge+time.Second))
	if stale.Ready || stale.ProbeReady || stale.ProbeReason != "synthetic probe is stale" {
		t.Fatalf("stale provider reported ready: %+v", stale)
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
	if document.SchemaVersion != probeStateSchemaVersion || document.Providers[string(ProviderChatGPT)].LastFixtureID != "fixture-001" {
		t.Fatalf("unexpected persisted probe state: %+v", document)
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
