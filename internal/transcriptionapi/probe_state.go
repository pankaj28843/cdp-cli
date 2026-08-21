package transcriptionapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type probeStateDocument struct {
	SchemaVersion string                        `json:"schema_version"`
	Fixtures      map[string]probeFixtureState  `json:"fixtures"`
	Providers     map[string]probeProviderState `json:"providers"`
}

type probeFixtureState struct {
	LastUsedAt string `json:"last_used_at,omitempty"`
}

type probeProviderState struct {
	File          *probeProviderPathState `json:"file,omitempty"`
	Realtime      *probeProviderPathState `json:"realtime,omitempty"`
	LastAttemptAt string                  `json:"last_attempt_at,omitempty"`
	LastSuccessAt string                  `json:"last_success_at,omitempty"`
	LastFixtureID string                  `json:"last_fixture_id,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
}

type probeProviderPathState struct {
	LastAttemptAt string `json:"last_attempt_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	LastFixtureID string `json:"last_fixture_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
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
	for key, item := range c.health.SnapshotPaths() {
		provider := state.Providers[string(key.Provider)]
		pathState := &probeProviderPathState{
			LastAttemptAt: formatProbeTime(item.LastAttemptAt),
			LastSuccessAt: formatProbeTime(item.LastSuccessAt),
			LastFixtureID: item.LastFixtureID,
			Reason:        safeProbeReason(item.Reason),
		}
		switch key.Path {
		case ProbePathFile:
			provider.File = pathState
		case ProbePathRealtime:
			provider.Realtime = pathState
		default:
			continue
		}
		state.Providers[string(key.Provider)] = provider
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
	if json.Unmarshal(data, &state) != nil || (state.SchemaVersion != probeStateSchemaVersion && state.SchemaVersion != legacyProbeStateSchemaVersion) {
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
	if state.SchemaVersion == legacyProbeStateSchemaVersion {
		for provider, item := range state.Providers {
			if item.File == nil && (item.LastAttemptAt != "" || item.LastSuccessAt != "" || item.LastFixtureID != "" || item.Reason != "") {
				item.File = &probeProviderPathState{
					LastAttemptAt: item.LastAttemptAt,
					LastSuccessAt: item.LastSuccessAt,
					LastFixtureID: item.LastFixtureID,
					Reason:        item.Reason,
				}
			}
			state.Providers[provider] = item
		}
		state.SchemaVersion = probeStateSchemaVersion
	}
	return state
}

func parseProbeHealthStates(states map[string]probeProviderState) map[probeStateKey]probeHealthState {
	parsed := make(map[probeStateKey]probeHealthState, len(states)*2)
	for provider, item := range states {
		pathStates := map[ProbePath]*probeProviderPathState{
			ProbePathFile:     item.File,
			ProbePathRealtime: item.Realtime,
		}
		for path, pathState := range pathStates {
			if pathState == nil {
				continue
			}
			state := probeHealthState{
				LastFixtureID: pathState.LastFixtureID,
				Reason:        safeProbeReason(pathState.Reason),
			}
			state.LastAttemptAt, _ = time.Parse(time.RFC3339Nano, pathState.LastAttemptAt)
			state.LastSuccessAt, _ = time.Parse(time.RFC3339Nano, pathState.LastSuccessAt)
			parsed[probeStateKey{Provider: ProviderID(provider), Path: path}] = state
		}
	}
	return parsed
}
