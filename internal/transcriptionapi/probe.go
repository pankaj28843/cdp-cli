package transcriptionapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	FixtureManifestSchemaVersion = "cdp-cli-transcription-fixtures/v2"
	DefaultFixtureCount          = 100
	DefaultProbeInterval         = 5 * time.Minute
	DefaultProbeTimeout          = 2 * time.Minute
	DefaultProbeMaxAge           = 15 * time.Minute
	DefaultProbeDurationMS       = 1500
	probeStateSchemaVersion      = "cdp-cli-transcription-probes/v1"
)

// ProbeFixture is checked-in synthetic input used by the service's bounded
// provider health probe. It contains no expected transcript so probe state can
// never become a transcript store.
type ProbeFixture struct {
	ID         string
	Path       string
	FileName   string
	MIMEType   string
	Bytes      int64
	DurationMS int64
}

type fixtureManifest struct {
	SchemaVersion string                 `json:"schema_version"`
	Count         int                    `json:"count"`
	Entries       []fixtureManifestEntry `json:"entries"`
}

type fixtureManifestEntry struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	WebM       string `json:"webm"`
	WebMBytes  int64  `json:"webm_bytes"`
	WebMSHA256 string `json:"webm_sha256"`
}

// LoadFixtureCatalog validates the exact checked-in WebM corpus before the
// service starts probing providers. The loader deliberately rejects a partial
// corpus so a green health endpoint cannot silently mean "only some fixtures
// were exercised".
func LoadFixtureCatalog(root string) ([]ProbeFixture, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("transcription fixture directory is required")
	}
	absoluteRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("resolve transcription fixture directory: %w", err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(absoluteRoot, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read transcription fixture manifest: %w", err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse transcription fixture manifest: %w", err)
	}
	if manifest.SchemaVersion != FixtureManifestSchemaVersion {
		return nil, fmt.Errorf("unsupported transcription fixture manifest schema %q", manifest.SchemaVersion)
	}
	if manifest.Count != DefaultFixtureCount || len(manifest.Entries) != DefaultFixtureCount {
		return nil, fmt.Errorf("transcription fixture corpus must contain exactly %d entries", DefaultFixtureCount)
	}

	fixtures := make([]ProbeFixture, 0, len(manifest.Entries))
	seenIDs := make(map[string]struct{}, len(manifest.Entries))
	seenPaths := make(map[string]struct{}, len(manifest.Entries))
	for index, entry := range manifest.Entries {
		wantID := fmt.Sprintf("fixture-%03d", index+1)
		if entry.ID != wantID || strings.TrimSpace(entry.Text) == "" {
			return nil, fmt.Errorf("invalid transcription fixture entry %d", index+1)
		}
		if _, ok := seenIDs[entry.ID]; ok {
			return nil, fmt.Errorf("duplicate transcription fixture id %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		name := filepath.Base(strings.TrimSpace(entry.WebM))
		if name != entry.WebM || filepath.Ext(name) != ".webm" {
			return nil, fmt.Errorf("transcription fixture %q is not a safe WebM filename", entry.ID)
		}
		if _, ok := seenPaths[name]; ok {
			return nil, fmt.Errorf("duplicate transcription fixture path %q", name)
		}
		seenPaths[name] = struct{}{}
		path := filepath.Join(absoluteRoot, name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxUploadBytes {
			return nil, fmt.Errorf("transcription fixture %q is not a bounded regular file", entry.ID)
		}
		if info.Size() != entry.WebMBytes {
			return nil, fmt.Errorf("transcription fixture %q size changed", entry.ID)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read transcription fixture %q: %w", entry.ID, err)
		}
		if len(data) < 4 || data[0] != 0x1a || data[1] != 0x45 || data[2] != 0xdf || data[3] != 0xa3 {
			return nil, fmt.Errorf("transcription fixture %q is not an EBML WebM file", entry.ID)
		}
		digest := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(digest[:]), entry.WebMSHA256) {
			return nil, fmt.Errorf("transcription fixture %q hash changed", entry.ID)
		}
		fixtures = append(fixtures, ProbeFixture{
			ID:         entry.ID,
			Path:       path,
			FileName:   name,
			MIMEType:   "audio/webm",
			Bytes:      info.Size(),
			DurationMS: DefaultProbeDurationMS,
		})
	}
	return fixtures, nil
}

type probeHealthState struct {
	LastAttemptAt time.Time
	LastSuccessAt time.Time
	LastFixtureID string
	Reason        string
}

// ProbeHealth is the server-side freshness gate for provider health. A
// provider's cached capability is insufficient: the endpoint reports it
// ready only after a recent synthetic transcription has succeeded.
type ProbeHealth struct {
	mu     sync.RWMutex
	maxAge time.Duration
	states map[ProviderID]probeHealthState
}

func NewProbeHealth(maxAge time.Duration) *ProbeHealth {
	if maxAge <= 0 {
		maxAge = DefaultProbeMaxAge
	}
	return &ProbeHealth{maxAge: maxAge, states: make(map[ProviderID]probeHealthState)}
}

func (h *ProbeHealth) Restore(states map[ProviderID]probeHealthState) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for provider, state := range states {
		h.states[provider] = state
	}
}

func (h *ProbeHealth) RecordAttempt(provider ProviderID, at time.Time, fixtureID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.states[provider]
	state.LastAttemptAt = at.UTC()
	state.LastFixtureID = fixtureID
	state.Reason = "probe_pending"
	h.states[provider] = state
}

func (h *ProbeHealth) RecordSuccess(provider ProviderID, at time.Time, fixtureID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	at = at.UTC()
	h.states[provider] = probeHealthState{
		LastAttemptAt: at,
		LastSuccessAt: at,
		LastFixtureID: fixtureID,
	}
}

func (h *ProbeHealth) RecordFailure(provider ProviderID, at time.Time, fixtureID, reason string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.states[provider]
	state.LastAttemptAt = at.UTC()
	state.LastFixtureID = fixtureID
	state.Reason = safeProbeReason(reason)
	h.states[provider] = state
}

func (h *ProbeHealth) Snapshot() map[ProviderID]probeHealthState {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	copyStates := make(map[ProviderID]probeHealthState, len(h.states))
	for provider, state := range h.states {
		copyStates[provider] = state
	}
	return copyStates
}

func (h *ProbeHealth) Apply(capability ProviderCapability, now time.Time) ProviderCapability {
	if h == nil {
		return capability
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	h.mu.RLock()
	state, found := h.states[capability.Provider]
	maxAge := h.maxAge
	h.mu.RUnlock()
	capability.ProbeReady = false
	if !found || state.LastAttemptAt.IsZero() {
		capability.Ready = false
		capability.ProbeReason = "synthetic probe has not succeeded recently"
		if strings.TrimSpace(capability.Reason) == "" {
			capability.Reason = capability.ProbeReason
		}
		return capability
	}
	capability.LastProbeAt = state.LastAttemptAt.UTC().Format(time.RFC3339Nano)
	capability.ProbeAgeSec = max(0, int64(now.Sub(state.LastAttemptAt.UTC())/time.Second))
	probeReady := !state.LastSuccessAt.IsZero() &&
		!state.LastSuccessAt.Before(state.LastAttemptAt) &&
		now.Sub(state.LastSuccessAt.UTC()) <= maxAge &&
		state.Reason == ""
	if capability.Ready && probeReady {
		capability.ProbeReady = true
		return capability
	}
	capability.Ready = false
	if strings.TrimSpace(capability.Reason) == "" || capability.ProbeReady == false {
		if state.Reason != "" {
			capability.ProbeReason = safeProbeReason(state.Reason)
		} else {
			capability.ProbeReason = "synthetic probe is stale"
		}
		if strings.TrimSpace(capability.Reason) == "" || strings.HasPrefix(capability.Reason, "synthetic probe") {
			capability.Reason = capability.ProbeReason
		}
	}
	return capability
}

type fixtureSelector struct {
	mu       sync.Mutex
	fixtures []ProbeFixture
	lastUsed map[string]time.Time
	rng      *rand.Rand
}

func newFixtureSelector(fixtures []ProbeFixture, lastUsed map[string]time.Time, seed int64) *fixtureSelector {
	copyFixtures := append([]ProbeFixture(nil), fixtures...)
	copyLastUsed := make(map[string]time.Time, len(lastUsed))
	for id, at := range lastUsed {
		copyLastUsed[id] = at
	}
	return &fixtureSelector{
		fixtures: copyFixtures,
		lastUsed: copyLastUsed,
		rng:      rand.New(rand.NewSource(seed)),
	}
}

func (s *fixtureSelector) Choose(now time.Time) (ProbeFixture, bool) {
	if s == nil {
		return ProbeFixture{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fixtures) == 0 {
		return ProbeFixture{}, false
	}
	candidates := append([]ProbeFixture(nil), s.fixtures...)
	sort.SliceStable(candidates, func(i, j int) bool {
		left, leftOK := s.lastUsed[candidates[i].ID]
		right, rightOK := s.lastUsed[candidates[j].ID]
		if leftOK != rightOK {
			return !leftOK
		}
		if left.Equal(right) {
			return candidates[i].ID < candidates[j].ID
		}
		return left.Before(right)
	})
	poolSize := len(candidates) / 4
	if poolSize < 1 {
		poolSize = 1
	}
	if poolSize > len(candidates) {
		poolSize = len(candidates)
	}
	candidates = candidates[:poolSize]
	totalWeight := 0.0
	weights := make([]float64, len(candidates))
	for index, candidate := range candidates {
		weight := float64(len(candidates) - index)
		if last, ok := s.lastUsed[candidate.ID]; ok && !last.IsZero() {
			ageMinutes := now.Sub(last).Minutes()
			if ageMinutes > 0 {
				weight += minFloat(ageMinutes/float64(DefaultProbeInterval/time.Minute), 10)
			}
		}
		weights[index] = weight
		totalWeight += weight
	}
	choice := s.rng.Float64() * totalWeight
	selected := candidates[len(candidates)-1]
	for index, weight := range weights {
		choice -= weight
		if choice <= 0 {
			selected = candidates[index]
			break
		}
	}
	s.lastUsed[selected.ID] = now.UTC()
	return selected, true
}

func (s *fixtureSelector) Snapshot() map[string]time.Time {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copyLastUsed := make(map[string]time.Time, len(s.lastUsed))
	for id, at := range s.lastUsed {
		copyLastUsed[id] = at
	}
	return copyLastUsed
}

func minFloat(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}

type probeStateDocument struct {
	SchemaVersion string                        `json:"schema_version"`
	Fixtures      map[string]probeFixtureState  `json:"fixtures"`
	Providers     map[string]probeProviderState `json:"providers"`
}

type probeFixtureState struct {
	LastUsedAt string `json:"last_used_at,omitempty"`
}

type probeProviderState struct {
	LastAttemptAt string `json:"last_attempt_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	LastFixtureID string `json:"last_fixture_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// SyntheticProbeCoordinator exercises every configured file-capable provider
// with one weighted-LRU WebM on a bounded cadence. It persists only timestamps,
// fixture ids, and redacted result codes; audio and transcript text never enter
// probe state.
type SyntheticProbeCoordinator struct {
	registry  *Registry
	selector  *fixtureSelector
	health    *ProbeHealth
	statePath string
	interval  time.Duration
	timeout   time.Duration
	startOnce sync.Once
	runMu     sync.Mutex
	stateMu   sync.Mutex
	done      chan struct{}
}

func NewSyntheticProbeCoordinator(
	registry *Registry,
	fixtures []ProbeFixture,
	stateRoot string,
	interval time.Duration,
	timeout time.Duration,
	maxAge time.Duration,
) (*SyntheticProbeCoordinator, error) {
	if registry == nil {
		return nil, fmt.Errorf("probe registry is required")
	}
	if len(fixtures) == 0 {
		return nil, fmt.Errorf("probe fixture catalog is empty")
	}
	if interval <= 0 {
		interval = DefaultProbeInterval
	}
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	if maxAge <= 0 {
		maxAge = DefaultProbeMaxAge
	}
	statePath := ""
	if strings.TrimSpace(stateRoot) != "" {
		statePath = filepath.Join(stateRoot, "probe-state.json")
	}
	state := loadProbeState(statePath)
	lastUsed := make(map[string]time.Time, len(state.Fixtures))
	for id, item := range state.Fixtures {
		if at, err := time.Parse(time.RFC3339Nano, item.LastUsedAt); err == nil {
			lastUsed[id] = at
		}
	}
	health := NewProbeHealth(maxAge)
	health.Restore(parseProbeHealthStates(state.Providers))
	return &SyntheticProbeCoordinator{
		registry:  registry,
		selector:  newFixtureSelector(fixtures, lastUsed, time.Now().UnixNano()),
		health:    health,
		statePath: statePath,
		interval:  interval,
		timeout:   timeout,
	}, nil
}

func (c *SyntheticProbeCoordinator) Health() *ProbeHealth {
	if c == nil {
		return nil
	}
	return c.health
}

func (c *SyntheticProbeCoordinator) Start(ctx context.Context) {
	if c == nil || c.interval <= 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.startOnce.Do(func() {
		c.done = make(chan struct{})
		go func() {
			defer close(c.done)
			c.RunOnce(ctx)
			ticker := time.NewTicker(c.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					c.RunOnce(ctx)
				}
			}
		}()
	})
}

func (c *SyntheticProbeCoordinator) Wait() {
	if c == nil || c.done == nil {
		return
	}
	<-c.done
}

func (c *SyntheticProbeCoordinator) RunOnce(ctx context.Context) {
	if c == nil || c.interval <= 0 || ctx == nil {
		return
	}
	if !c.runMu.TryLock() {
		return
	}
	defer c.runMu.Unlock()
	fixture, ok := c.selector.Choose(time.Now().UTC())
	if !ok {
		return
	}
	_ = c.persistState()
	ids := c.providerIDs()
	var wait sync.WaitGroup
	for _, id := range ids {
		provider, ok := c.registry.Provider(id)
		if !ok {
			continue
		}
		providerForProbe := provider
		wait.Add(1)
		go func() {
			defer wait.Done()
			c.probeProvider(ctx, providerForProbe, fixture)
		}()
	}
	wait.Wait()
}

func (c *SyntheticProbeCoordinator) providerIDs() []ProviderID {
	ids := make([]ProviderID, 0, len(c.registry.providers))
	for id := range c.registry.providers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (c *SyntheticProbeCoordinator) probeProvider(ctx context.Context, provider Provider, fixture ProbeFixture) {
	providerID := provider.ID()
	attemptedAt := time.Now().UTC()
	c.health.RecordAttempt(providerID, attemptedAt, fixture.ID)
	_ = c.persistState()
	probeContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if refresher, ok := provider.(AuthRefresher); ok {
		if err := refresher.EnsureAuthFresh(probeContext); err != nil {
			c.recordFailure(providerID, fixture, probeReason(err))
			return
		}
	}
	if refresher, ok := provider.(CapabilityRefresher); ok {
		if err := refresher.EnsureCapabilitiesFresh(probeContext); err != nil {
			c.recordFailure(providerID, fixture, probeReason(err))
			return
		}
	}
	capability := provider.Capabilities(probeContext)
	if !capability.File || !capability.Ready {
		c.recordFailure(providerID, fixture, "provider_not_ready")
		return
	}
	result, err := provider.Transcribe(probeContext, FileRequest{
		RequestID: NewRequestID(),
		Task:      TaskTranscribe,
		Provider:  providerID,
		Model:     DefaultModel,
		Audio: AudioAsset{
			FileName:      fixture.FileName,
			MIMEType:      fixture.MIMEType,
			Bytes:         fixture.Bytes,
			DurationMS:    fixture.DurationMS,
			PersistedPath: fixture.Path,
			Ephemeral:     true,
		},
	})
	if err != nil {
		c.recordFailure(providerID, fixture, probeReason(err))
		return
	}
	if strings.TrimSpace(result.Text) == "" {
		c.recordFailure(providerID, fixture, "empty_transcript")
		return
	}
	c.health.RecordSuccess(providerID, time.Now().UTC(), fixture.ID)
	_ = c.persistState()
}

func (c *SyntheticProbeCoordinator) recordFailure(provider ProviderID, fixture ProbeFixture, reason string) {
	c.health.RecordFailure(provider, time.Now().UTC(), fixture.ID, reason)
	_ = c.persistState()
}

func probeReason(err error) string {
	if err == nil {
		return "probe_failed"
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr != nil {
		if code := safeProbeReason(providerErr.APIError.Code); code != "" {
			return code
		}
		if code := safeProbeReason(providerErr.APIError.Type); code != "" {
			return code
		}
	}
	return "probe_failed"
}

func safeProbeReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func (c *SyntheticProbeCoordinator) persistState() error {
	if c == nil || strings.TrimSpace(c.statePath) == "" {
		return nil
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	state := probeStateDocument{
		SchemaVersion: probeStateSchemaVersion,
		Fixtures:      make(map[string]probeFixtureState),
		Providers:     make(map[string]probeProviderState),
	}
	for id, at := range c.selector.Snapshot() {
		state.Fixtures[id] = probeFixtureState{LastUsedAt: at.UTC().Format(time.RFC3339Nano)}
	}
	for provider, item := range c.health.Snapshot() {
		state.Providers[string(provider)] = probeProviderState{
			LastAttemptAt: formatProbeTime(item.LastAttemptAt),
			LastSuccessAt: formatProbeTime(item.LastSuccessAt),
			LastFixtureID: item.LastFixtureID,
			Reason:        safeProbeReason(item.Reason),
		}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(c.statePath), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(c.statePath), ".probe-state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, c.statePath)
}

func formatProbeTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func loadProbeState(path string) probeStateDocument {
	state := probeStateDocument{
		SchemaVersion: probeStateSchemaVersion,
		Fixtures:      make(map[string]probeFixtureState),
		Providers:     make(map[string]probeProviderState),
	}
	if strings.TrimSpace(path) == "" {
		return state
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	if json.Unmarshal(data, &state) != nil || state.SchemaVersion != probeStateSchemaVersion {
		return probeStateDocument{
			SchemaVersion: probeStateSchemaVersion,
			Fixtures:      make(map[string]probeFixtureState),
			Providers:     make(map[string]probeProviderState),
		}
	}
	if state.Fixtures == nil {
		state.Fixtures = make(map[string]probeFixtureState)
	}
	if state.Providers == nil {
		state.Providers = make(map[string]probeProviderState)
	}
	return state
}

func parseProbeHealthStates(states map[string]probeProviderState) map[ProviderID]probeHealthState {
	parsed := make(map[ProviderID]probeHealthState, len(states))
	for provider, item := range states {
		state := probeHealthState{
			LastFixtureID: item.LastFixtureID,
			Reason:        safeProbeReason(item.Reason),
		}
		state.LastAttemptAt, _ = time.Parse(time.RFC3339Nano, item.LastAttemptAt)
		state.LastSuccessAt, _ = time.Parse(time.RFC3339Nano, item.LastSuccessAt)
		parsed[ProviderID(provider)] = state
	}
	return parsed
}

func max(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
