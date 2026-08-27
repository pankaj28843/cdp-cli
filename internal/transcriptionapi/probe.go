package transcriptionapi

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// Keep cached provider evidence fresh enough to detect a stale headed
	// session before the next user turn, while bounding each provider attempt
	// so a wedged provider cannot block the cadence indefinitely.
	DefaultProbeInterval          = time.Minute
	DefaultProbeTimeout           = 30 * time.Second
	DefaultProbeMaxAge            = 3 * time.Minute
	probeStateSchemaVersion       = "cdp-cli-transcription-probes/v2"
	legacyProbeStateSchemaVersion = "cdp-cli-transcription-probes/v1"
)

type probeHealthState struct {
	LastAttemptAt time.Time
	LastSuccessAt time.Time
	LastFixtureID string
	Reason        string
}

type ProbePath string

const (
	ProbePathFile     ProbePath = "file"
	ProbePathRealtime ProbePath = "realtime"
)

type probeStateKey struct {
	Provider ProviderID
	Path     ProbePath
}

// ProbeHealth is the server-side freshness gate for provider health. A
// provider's cached capability is insufficient: the endpoint reports it
// ready only after a recent synthetic transcription has succeeded.
type ProbeHealth struct {
	mu     sync.RWMutex
	maxAge time.Duration
	states map[probeStateKey]probeHealthState
}

func NewProbeHealth(maxAge time.Duration) *ProbeHealth {
	if maxAge <= 0 {
		maxAge = DefaultProbeMaxAge
	}
	return &ProbeHealth{maxAge: maxAge, states: make(map[probeStateKey]probeHealthState)}
}

func (h *ProbeHealth) Restore(states map[ProviderID]probeHealthState) {
	pathStates := make(map[probeStateKey]probeHealthState, len(states))
	for provider, state := range states {
		pathStates[probeStateKey{Provider: provider, Path: ProbePathFile}] = state
	}
	h.RestorePaths(pathStates)
}

func (h *ProbeHealth) RestorePaths(states map[probeStateKey]probeHealthState) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, state := range states {
		h.states[key] = state
	}
}

func (h *ProbeHealth) RecordAttempt(provider ProviderID, at time.Time, fixtureID string) {
	h.RecordPathAttempt(provider, ProbePathFile, at, fixtureID)
}

func (h *ProbeHealth) RecordPathAttempt(provider ProviderID, path ProbePath, at time.Time, fixtureID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	key := probeStateKey{Provider: provider, Path: path}
	state := h.states[key]
	state.LastAttemptAt = at.UTC()
	state.LastFixtureID = fixtureID
	state.Reason = "probe_pending"
	h.states[key] = state
}

func (h *ProbeHealth) RecordSuccess(provider ProviderID, at time.Time, fixtureID string) {
	h.RecordPathSuccess(provider, ProbePathFile, at, fixtureID)
}

func (h *ProbeHealth) RecordPathSuccess(provider ProviderID, path ProbePath, at time.Time, fixtureID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	at = at.UTC()
	h.states[probeStateKey{Provider: provider, Path: path}] = probeHealthState{
		LastAttemptAt: at,
		LastSuccessAt: at,
		LastFixtureID: fixtureID,
	}
}

func (h *ProbeHealth) RecordFailure(provider ProviderID, at time.Time, fixtureID, reason string) {
	h.RecordPathFailure(provider, ProbePathFile, at, fixtureID, reason)
}

func (h *ProbeHealth) RecordPathFailure(provider ProviderID, path ProbePath, at time.Time, fixtureID, reason string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	key := probeStateKey{Provider: provider, Path: path}
	state := h.states[key]
	state.LastAttemptAt = at.UTC()
	state.LastFixtureID = fixtureID
	state.Reason = safeProbeReason(reason)
	h.states[key] = state
}

func (h *ProbeHealth) Snapshot() map[ProviderID]probeHealthState {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	copyStates := make(map[ProviderID]probeHealthState)
	for key, state := range h.states {
		if key.Path == ProbePathFile {
			copyStates[key.Provider] = state
		}
	}
	return copyStates
}

func (h *ProbeHealth) SnapshotPaths() map[probeStateKey]probeHealthState {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	copyStates := make(map[probeStateKey]probeHealthState, len(h.states))
	for key, state := range h.states {
		copyStates[key] = state
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
	paths := probePathsForCapability(capability)
	if len(paths) == 0 {
		return capability
	}
	h.mu.RLock()
	maxAge := h.maxAge
	states := make(map[probeStateKey]probeHealthState, len(paths))
	for _, path := range paths {
		states[probeStateKey{Provider: capability.Provider, Path: path}] = h.states[probeStateKey{Provider: capability.Provider, Path: path}]
	}
	h.mu.RUnlock()

	capability.ProbeReady = true
	capability.LastProbeAt = ""
	capability.ProbeAgeSec = 0
	probeReasons := make([]string, 0, len(paths))
	oldestSuccess := time.Time{}
	oldestAge := int64(0)
	for _, path := range paths {
		key := probeStateKey{Provider: capability.Provider, Path: path}
		state, found := states[key]
		status := applyPathStatus(state, found, now, maxAge)
		switch path {
		case ProbePathFile:
			capability.FileProbe = status
		case ProbePathRealtime:
			capability.RealtimeProbe = status
		}
		if !status.Ready {
			capability.ProbeReady = false
			probeReasons = append(probeReasons, string(path)+":"+status.Reason)
		}
		if !state.LastSuccessAt.IsZero() {
			success := state.LastSuccessAt.UTC()
			age := max(0, int64(now.Sub(success)/time.Second))
			if oldestSuccess.IsZero() || success.Before(oldestSuccess) {
				oldestSuccess = success
			}
			if age > oldestAge {
				oldestAge = age
			}
		}
	}
	if !oldestSuccess.IsZero() {
		capability.LastProbeAt = oldestSuccess.Format(time.RFC3339Nano)
		capability.ProbeAgeSec = oldestAge
	}
	if !capability.ProbeReady {
		capability.ProbeReason = strings.Join(probeReasons, ";")
		if strings.TrimSpace(capability.Reason) == "" {
			capability.Reason = capability.ProbeReason
		}
	}
	if !capability.Ready || !capability.ProbeReady {
		capability.Ready = false
	}
	return capability
}

func probePathsForCapability(capability ProviderCapability) []ProbePath {
	paths := make([]ProbePath, 0, 2)
	if capability.File {
		paths = append(paths, ProbePathFile)
	}
	if capability.Realtime {
		paths = append(paths, ProbePathRealtime)
	}
	return paths
}

func applyPathStatus(state probeHealthState, found bool, now time.Time, maxAge time.Duration) *ProbePathStatus {
	status := &ProbePathStatus{}
	if !found || state.LastSuccessAt.IsZero() {
		status.Reason = safeProbeReason(state.Reason)
		if status.Reason == "" {
			status.Reason = "synthetic probe has not succeeded recently"
		}
		return status
	}
	success := state.LastSuccessAt.UTC()
	status.LastSuccessAt = success.Format(time.RFC3339Nano)
	status.AgeSec = max(0, int64(now.Sub(success)/time.Second))
	// A new probe is allowed to be in flight while the last successful
	// evidence is still fresh. Marking that evidence unavailable at attempt
	// start made /healthz oscillate to degraded during every normal probe and
	// caused clients to observe a false outage. Real failures still invalidate
	// the path immediately.
	if state.Reason != "" && state.Reason != "probe_pending" {
		status.Reason = safeProbeReason(state.Reason)
		return status
	}
	if now.Sub(success) > maxAge {
		status.Reason = "synthetic probe is stale"
		return status
	}
	status.Ready = true
	return status
}

// SyntheticProbeCoordinator exercises every advertised provider transcription
// path with one weighted-LRU WebM on a bounded cadence. Each cycle first runs
// the provider's bounded auth/capability preflight, then exercises the live
// wire; an auth rejection may therefore trigger one provider-owned repair
// before the health result is recorded. It persists only timestamps, fixture
// ids, and redacted result codes; audio and transcript text never enter probe
// state.
type SyntheticProbeCoordinator struct {
	registry    *Registry
	selector    *fixtureSelector
	health      *ProbeHealth
	statePath   string
	interval    time.Duration
	timeout     time.Duration
	startOnce   sync.Once
	runMu       sync.Mutex
	stateMu     sync.Mutex
	done        chan struct{}
	initialGate func(context.Context) error
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
	health.RestorePaths(parseProbeHealthStates(state.Providers))
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

// RecordObservedFileFailure invalidates the file path after a real user
// request fails. Synthetic probes remain the recovery authority: the next
// bounded fixture run must succeed before health becomes ready again.
func (c *SyntheticProbeCoordinator) RecordObservedFileFailure(provider ProviderID, reason string) {
	if c == nil || c.health == nil {
		return
	}
	c.health.RecordPathFailure(provider, ProbePathFile, time.Now().UTC(), "live-request", reason)
	_ = c.persistState()
}

// SetInitialGate installs the one-shot service-start gate. It must be called
// before Start. The gate may wait for provider lifecycle repair, but it must
// not perform a probe itself; the synthetic probe remains browser-free.
func (c *SyntheticProbeCoordinator) SetInitialGate(gate func(context.Context) error) {
	if c == nil {
		return
	}
	c.initialGate = gate
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
			if c.initialGate != nil {
				if err := c.initialGate(ctx); err != nil {
					return
				}
			}
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
	probeContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	capability := provider.Capabilities(probeContext)
	paths := probePathsForCapability(capability)
	if len(paths) == 0 {
		return
	}
	if err := prepareProviderForProbe(probeContext, provider); err != nil {
		c.recordProviderFailure(providerID, paths, fixture, probeReason(err))
		return
	}
	capability = provider.Capabilities(probeContext)
	paths = probePathsForCapability(capability)
	for _, path := range paths {
		c.recordPathAttempt(providerID, path, fixture)
	}
	if len(paths) == 0 {
		return
	}
	if !capability.Ready {
		c.recordProviderFailure(providerID, paths, fixture, "provider_not_ready")
		return
	}
	if capability.File {
		result, err := provider.Transcribe(probeContext, FileRequest{
			RequestID:      NewRequestID(),
			Task:           TaskTranscribe,
			Provider:       providerID,
			Model:          DefaultModel,
			SyntheticProbe: true,
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
			c.recordPathFailure(providerID, ProbePathFile, fixture, probeReason(err))
		} else if strings.TrimSpace(result.Text) == "" {
			c.recordPathFailure(providerID, ProbePathFile, fixture, "empty_transcript")
		} else {
			c.recordPathSuccess(providerID, ProbePathFile, fixture)
		}
	}
	if capability.Realtime {
		probeProvider, ok := provider.(RealtimeProbeProvider)
		if !ok {
			c.recordPathFailure(providerID, ProbePathRealtime, fixture, "realtime_probe_unsupported")
		} else if err := probeProvider.ProbeRealtime(probeContext, fixture); err != nil {
			c.recordPathFailure(providerID, ProbePathRealtime, fixture, probeReason(err))
		} else {
			c.recordPathSuccess(providerID, ProbePathRealtime, fixture)
		}
	}
}

func prepareProviderForProbe(ctx context.Context, provider Provider) error {
	if provider == nil {
		return ErrProviderUnavailable
	}
	if auth, ok := provider.(AuthRefresher); ok {
		if err := auth.EnsureAuthFresh(ctx); err != nil {
			return err
		}
	}
	if capabilities, ok := provider.(CapabilityRefresher); ok {
		if err := capabilities.EnsureCapabilitiesFresh(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (c *SyntheticProbeCoordinator) recordPathAttempt(provider ProviderID, path ProbePath, fixture ProbeFixture) {
	c.health.RecordPathAttempt(provider, path, time.Now().UTC(), fixture.ID)
	_ = c.persistState()
}

func (c *SyntheticProbeCoordinator) recordPathSuccess(provider ProviderID, path ProbePath, fixture ProbeFixture) {
	c.health.RecordPathSuccess(provider, path, time.Now().UTC(), fixture.ID)
	_ = c.persistState()
}

func (c *SyntheticProbeCoordinator) recordPathFailure(provider ProviderID, path ProbePath, fixture ProbeFixture, reason string) {
	c.health.RecordPathFailure(provider, path, time.Now().UTC(), fixture.ID, reason)
	_ = c.persistState()
}

func (c *SyntheticProbeCoordinator) recordProviderFailure(provider ProviderID, paths []ProbePath, fixture ProbeFixture, reason string) {
	for _, path := range paths {
		c.recordPathFailure(provider, path, fixture, reason)
	}
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

func max(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
