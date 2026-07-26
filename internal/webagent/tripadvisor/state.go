package tripadvisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

const (
	Origin                    = "https://www.tripadvisor.com"
	HomeURL                   = Origin + "/#/chat"
	SessionStateSchemaVersion = "tripadvisor-session/v1"
	RelativeSessionPath       = "webagent/tripadvisor/session.json"
	DefaultAuthTTL            = time.Hour
	DefaultAdmissionSpacing   = time.Second
	MaxPromptCharacters       = 1_000
)

type SessionState struct {
	SchemaVersion string `json:"schema_version"`
	CapturedAt    string `json:"captured_at"`
	PanelReady    bool   `json:"panel_ready"`
	ComposerReady bool   `json:"composer_ready"`
	HistoryReady  bool   `json:"history_ready"`
	SessionMode   string `json:"session_mode"`
	Source        string `json:"source"`
}

type SessionStatus struct {
	SchemaVersion string `json:"schema_version"`
	State         string `json:"state"`
	Ready         bool   `json:"ready"`
	Stale         bool   `json:"stale"`
	StatePath     string `json:"state_path"`
	CapturedAt    string `json:"captured_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	PanelReady    bool   `json:"panel_ready"`
	ComposerReady bool   `json:"composer_ready"`
	HistoryReady  bool   `json:"history_ready"`
	SessionMode   string `json:"session_mode,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type Store struct {
	sessionPath string
}

func NewStore(stateDir string) (*Store, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("Tripadvisor state directory is required")
	}
	return &Store{
		sessionPath: filepath.Join(
			stateDir,
			filepath.FromSlash(RelativeSessionPath),
		),
	}, nil
}

func (s *Store) SaveSession(ctx context.Context, state SessionState) error {
	if s == nil || s.sessionPath == "" {
		return fmt.Errorf("Tripadvisor session store is not configured")
	}
	if err := state.Validate(); err != nil {
		return fmt.Errorf("validate Tripadvisor session state: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Tripadvisor session state: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := artifacts.WriteOwnerOnlyFileAtomic(s.sessionPath, encoded); err != nil {
		return fmt.Errorf("write Tripadvisor session state: %w", err)
	}
	return nil
}

func (s *Store) LoadSession(ctx context.Context) (SessionState, error) {
	if s == nil || s.sessionPath == "" {
		return SessionState{}, fmt.Errorf("Tripadvisor session store is not configured")
	}
	if err := ctx.Err(); err != nil {
		return SessionState{}, err
	}
	encoded, err := artifacts.ReadOwnerOnlyFile(s.sessionPath)
	if err != nil {
		return SessionState{}, err
	}
	var state SessionState
	if err := json.Unmarshal(encoded, &state); err != nil {
		return SessionState{}, fmt.Errorf("decode Tripadvisor session state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return SessionState{}, fmt.Errorf("validate Tripadvisor session state: %w", err)
	}
	return state, nil
}

func (s *Store) Status(
	ctx context.Context,
	now time.Time,
	ttl time.Duration,
) SessionStatus {
	status := SessionStatus{
		SchemaVersion: SessionStateSchemaVersion,
		State:         "missing",
		StatePath:     RelativeSessionPath,
		Reason:        "rendered Tripadvisor session evidence is not present",
	}
	if ttl <= 0 {
		ttl = DefaultAuthTTL
	}
	state, err := s.LoadSession(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return status
		}
		status.State = "invalid"
		status.Reason = "rendered session evidence failed owner-only validation"
		return status
	}
	capturedAt, _ := time.Parse(time.RFC3339Nano, state.CapturedAt)
	expiresAt := capturedAt.Add(ttl)
	status.CapturedAt = capturedAt.Format(time.RFC3339Nano)
	status.ExpiresAt = expiresAt.Format(time.RFC3339Nano)
	status.PanelReady = state.PanelReady
	status.ComposerReady = state.ComposerReady
	status.HistoryReady = state.HistoryReady
	status.SessionMode = state.SessionMode
	status.Stale = !now.UTC().Before(expiresAt)
	switch {
	case capturedAt.After(now.UTC().Add(5 * time.Minute)):
		status.State = "invalid"
		status.Reason = "session capture time is unexpectedly in the future"
	case status.Stale:
		status.State = "expired"
		status.Reason = "rendered session evidence exceeded its freshness window"
	default:
		status.State = "ready"
		status.Ready = true
		status.Reason = ""
	}
	return status
}

func (s SessionState) Validate() error {
	if s.SchemaVersion != SessionStateSchemaVersion {
		return fmt.Errorf(
			"schema_version must be %q",
			SessionStateSchemaVersion,
		)
	}
	if _, err := time.Parse(time.RFC3339Nano, s.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if !s.PanelReady || !s.ComposerReady || !s.HistoryReady {
		return fmt.Errorf("panel, composer, and history evidence must all be ready")
	}
	if s.SessionMode != "anonymous" && s.SessionMode != "signed_in" {
		return fmt.Errorf("session_mode must be anonymous or signed_in")
	}
	if s.Source != "headed-cdp-rendered-session" {
		return fmt.Errorf("source is not an accepted rendered session observation")
	}
	return nil
}
