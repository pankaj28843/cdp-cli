package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

const (
	Origin                           = "https://gemini.google.com"
	HomeURL                          = Origin + "/app"
	AuthStateSchemaVersion           = "gemini-auth-state/v1"
	RequestTemplateSchemaVersion     = "gemini-dictation-template/v1"
	RuntimeCapabilitiesSchemaVersion = "gemini-runtime-capabilities/v1"
	RelativeAuthStatePath            = "webagent/gemini/auth-state.json"
	RelativeTemplatePath             = "webagent/gemini/dictation-template.json"
	RelativeCapabilitiesPath         = "webagent/gemini/capabilities.json"
	DefaultAuthTTL                   = time.Hour
	DefaultCapabilitiesTTL           = 24 * time.Hour
)

type AuthState struct {
	SchemaVersion         string `json:"schema_version"`
	CapturedAt            string `json:"captured_at"`
	SignedIn              bool   `json:"signed_in"`
	SessionCookieObserved bool   `json:"session_cookie_observed"`
	Source                string `json:"source"`
}

// RequestTemplate is the minimum owner-only state needed to replay Gemini's
// observed dictation WebChannel without involving a browser on the audio path.
type RequestTemplate struct {
	SchemaVersion    string            `json:"schema_version"`
	APIKey           string            `json:"api_key"`
	AuthUser         string            `json:"auth_user"`
	Cookies          map[string]string `json:"cookies"`
	BrowserUserAgent string            `json:"browser_user_agent"`
	CapturedAt       string            `json:"captured_at"`
	Source           string            `json:"source"`
}

type AuthStatus struct {
	SchemaVersion string `json:"schema_version"`
	State         string `json:"state"`
	Ready         bool   `json:"ready"`
	Stale         bool   `json:"stale"`
	StatePath     string `json:"state_path"`
	CapturedAt    string `json:"captured_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type RuntimeCapabilities struct {
	SchemaVersion         string   `json:"schema_version"`
	CapturedAt            string   `json:"captured_at"`
	CurrentMode           string   `json:"current_mode"`
	ModeOptions           []string `json:"mode_options"`
	FileUploadControl     string   `json:"file_upload_control"`
	FileUploadAction      string   `json:"file_upload_action"`
	DeepResearchSelected  bool     `json:"deep_research_selected"`
	ExplicitModeSelection string   `json:"explicit_mode_selection"`
	Source                string   `json:"source"`
}

type RuntimeStatus struct {
	SchemaVersion         string   `json:"schema_version"`
	State                 string   `json:"state"`
	Ready                 bool     `json:"ready"`
	Stale                 bool     `json:"stale"`
	StatePath             string   `json:"state_path"`
	CapturedAt            string   `json:"captured_at,omitempty"`
	ExpiresAt             string   `json:"expires_at,omitempty"`
	CurrentMode           string   `json:"current_mode,omitempty"`
	ModeOptions           []string `json:"mode_options"`
	FileUploadControl     string   `json:"file_upload_control,omitempty"`
	FileUploadAction      string   `json:"file_upload_action,omitempty"`
	DeepResearchSelected  bool     `json:"deep_research_selected,omitempty"`
	ExplicitModeSelection string   `json:"explicit_mode_selection,omitempty"`
	Reason                string   `json:"reason,omitempty"`
}

type Store struct {
	authPath         string
	templatePath     string
	capabilitiesPath string
}

func NewStore(stateDir string) (*Store, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("Gemini state directory is required")
	}
	return &Store{
		authPath:         filepath.Join(stateDir, filepath.FromSlash(RelativeAuthStatePath)),
		templatePath:     filepath.Join(stateDir, filepath.FromSlash(RelativeTemplatePath)),
		capabilitiesPath: filepath.Join(stateDir, filepath.FromSlash(RelativeCapabilitiesPath)),
	}, nil
}

func (s *Store) SaveTemplate(ctx context.Context, template RequestTemplate) error {
	if s == nil || s.templatePath == "" {
		return fmt.Errorf("Gemini dictation template store is not configured")
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("validate Gemini dictation template: %w", err)
	}
	return saveOwnerJSON(ctx, s.templatePath, template)
}

func (s *Store) LoadTemplate(ctx context.Context) (RequestTemplate, error) {
	if s == nil || s.templatePath == "" {
		return RequestTemplate{}, fmt.Errorf("Gemini dictation template store is not configured")
	}
	var template RequestTemplate
	if err := loadOwnerJSON(ctx, s.templatePath, &template); err != nil {
		return RequestTemplate{}, err
	}
	if err := template.Validate(); err != nil {
		return RequestTemplate{}, fmt.Errorf("validate Gemini dictation template: %w", err)
	}
	return template, nil
}

func (s *Store) TemplateStatus(ctx context.Context, now time.Time, ttl time.Duration) AuthStatus {
	_, status, _ := s.LoadTemplateStatus(ctx, now, ttl)
	return status
}

func (s *Store) LoadTemplateStatus(ctx context.Context, now time.Time, ttl time.Duration) (RequestTemplate, AuthStatus, error) {
	status := AuthStatus{
		SchemaVersion: RequestTemplateSchemaVersion,
		State:         "missing",
		StatePath:     RelativeTemplatePath,
		Reason:        "dictation request template is not present",
	}
	if ttl <= 0 {
		ttl = DefaultAuthTTL
	}
	template, err := s.LoadTemplate(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return RequestTemplate{}, status, err
		}
		status.State = "invalid"
		status.Reason = "dictation request template failed owner-only validation"
		return RequestTemplate{}, status, err
	}
	capturedAt, _ := time.Parse(time.RFC3339Nano, template.CapturedAt)
	status = authStatusFromState(status, AuthState{}, capturedAt, now, ttl)
	return template, status, nil
}

func (s *Store) SaveAuth(ctx context.Context, state AuthState) error {
	if s == nil || s.authPath == "" {
		return fmt.Errorf("Gemini auth store is not configured")
	}
	if err := state.Validate(); err != nil {
		return fmt.Errorf("validate Gemini auth state: %w", err)
	}
	return saveOwnerJSON(ctx, s.authPath, state)
}

func (s *Store) LoadAuth(ctx context.Context) (AuthState, error) {
	if s == nil || s.authPath == "" {
		return AuthState{}, fmt.Errorf("Gemini auth store is not configured")
	}
	var state AuthState
	if err := loadOwnerJSON(ctx, s.authPath, &state); err != nil {
		return AuthState{}, err
	}
	if err := state.Validate(); err != nil {
		return AuthState{}, fmt.Errorf("validate Gemini auth state: %w", err)
	}
	return state, nil
}

func (s *Store) AuthStatus(ctx context.Context, now time.Time, ttl time.Duration) AuthStatus {
	status := AuthStatus{
		SchemaVersion: AuthStateSchemaVersion,
		State:         "missing",
		StatePath:     RelativeAuthStatePath,
		Reason:        "auth evidence is not present",
	}
	if ttl <= 0 {
		ttl = DefaultAuthTTL
	}
	state, err := s.LoadAuth(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return status
		}
		status.State = "invalid"
		status.Reason = "auth evidence failed owner-only validation"
		return status
	}
	capturedAt, _ := time.Parse(time.RFC3339Nano, state.CapturedAt)
	return authStatusFromState(status, state, capturedAt, now, ttl)
}

func (s *Store) SaveRuntime(ctx context.Context, capabilities RuntimeCapabilities) error {
	if s == nil || s.capabilitiesPath == "" {
		return fmt.Errorf("Gemini runtime capability store is not configured")
	}
	if err := capabilities.Validate(); err != nil {
		return fmt.Errorf("validate Gemini runtime capabilities: %w", err)
	}
	return saveOwnerJSON(ctx, s.capabilitiesPath, capabilities)
}

func (s *Store) LoadRuntime(ctx context.Context) (RuntimeCapabilities, error) {
	if s == nil || s.capabilitiesPath == "" {
		return RuntimeCapabilities{}, fmt.Errorf("Gemini runtime capability store is not configured")
	}
	var capabilities RuntimeCapabilities
	if err := loadOwnerJSON(ctx, s.capabilitiesPath, &capabilities); err != nil {
		return RuntimeCapabilities{}, err
	}
	if err := capabilities.Validate(); err != nil {
		return RuntimeCapabilities{}, fmt.Errorf("validate Gemini runtime capabilities: %w", err)
	}
	return capabilities, nil
}

func (s *Store) RuntimeStatus(ctx context.Context, now time.Time, ttl time.Duration) RuntimeStatus {
	status := RuntimeStatus{
		SchemaVersion: RuntimeCapabilitiesSchemaVersion,
		State:         "missing",
		StatePath:     RelativeCapabilitiesPath,
		ModeOptions:   []string{},
		Reason:        "runtime capability evidence is not present",
	}
	if ttl <= 0 {
		ttl = DefaultCapabilitiesTTL
	}
	capabilities, err := s.LoadRuntime(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return status
		}
		status.State = "invalid"
		status.Reason = "runtime capability evidence failed owner-only validation"
		return status
	}
	capturedAt, _ := time.Parse(time.RFC3339Nano, capabilities.CapturedAt)
	now = now.UTC()
	expiresAt := capturedAt.Add(ttl)
	status.CapturedAt = capturedAt.UTC().Format(time.RFC3339Nano)
	status.ExpiresAt = expiresAt.UTC().Format(time.RFC3339Nano)
	status.Stale = !now.Before(expiresAt)
	status.CurrentMode = capabilities.CurrentMode
	status.ModeOptions = append([]string(nil), capabilities.ModeOptions...)
	status.FileUploadControl = capabilities.FileUploadControl
	status.FileUploadAction = capabilities.FileUploadAction
	status.DeepResearchSelected = capabilities.DeepResearchSelected
	status.ExplicitModeSelection = capabilities.ExplicitModeSelection
	if capturedAt.After(now.Add(5 * time.Minute)) {
		status.State = "invalid"
		status.Reason = "runtime capability capture time is unexpectedly in the future"
		return status
	}
	if status.Stale {
		status.State = "expired"
		status.Reason = "runtime capability evidence exceeded its freshness window"
		return status
	}
	status.State = "ready"
	status.Ready = true
	status.Reason = ""
	return status
}

func (s AuthState) Validate() error {
	if s.SchemaVersion != AuthStateSchemaVersion {
		return fmt.Errorf("schema_version must be %q", AuthStateSchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339Nano, s.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if !s.SignedIn || !s.SessionCookieObserved {
		return fmt.Errorf("signed-in browser and session-cookie evidence are required")
	}
	if s.Source != "headed-cdp-safe-auth-evidence" {
		return fmt.Errorf("source is not an accepted auth observation")
	}
	return nil
}

func (t RequestTemplate) Validate() error {
	if t.SchemaVersion != RequestTemplateSchemaVersion {
		return fmt.Errorf("schema_version must be %q", RequestTemplateSchemaVersion)
	}
	if strings.TrimSpace(t.APIKey) == "" || len(t.APIKey) > 512 {
		return fmt.Errorf("api_key is required and bounded")
	}
	if strings.TrimSpace(t.AuthUser) == "" || len(t.AuthUser) > 32 {
		return fmt.Errorf("auth_user is required and bounded")
	}
	if len(t.Cookies) == 0 || len(t.Cookies) > 512 {
		return fmt.Errorf("cookies are required and bounded")
	}
	for name, value := range t.Cookies {
		if strings.TrimSpace(name) == "" || len(name) > 4096 || len(value) > 64<<10 {
			return fmt.Errorf("cookie names and values must be bounded")
		}
	}
	for _, name := range []string{"SAPISID", "__Secure-1PAPISID", "__Secure-3PAPISID"} {
		if strings.TrimSpace(t.Cookies[name]) == "" || len(t.Cookies[name]) > 64<<10 {
			return fmt.Errorf("required Gemini auth cookie %q is missing or invalid", name)
		}
	}
	if strings.TrimSpace(t.BrowserUserAgent) == "" || len(t.BrowserUserAgent) > 4096 {
		return fmt.Errorf("browser_user_agent is required and bounded")
	}
	if _, err := time.Parse(time.RFC3339Nano, t.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if t.Source != "headed-cdp-observed-dictation-template" &&
		t.Source != "headed-cdp-retained-dictation-template" {
		return fmt.Errorf("source is not an accepted dictation-template observation")
	}
	return nil
}

func (c RuntimeCapabilities) Validate() error {
	if c.SchemaVersion != RuntimeCapabilitiesSchemaVersion {
		return fmt.Errorf("schema_version must be %q", RuntimeCapabilitiesSchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339Nano, c.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if strings.TrimSpace(c.CurrentMode) == "" || len(c.CurrentMode) > 256 {
		return fmt.Errorf("current_mode is required and bounded")
	}
	if len(c.ModeOptions) == 0 || len(c.ModeOptions) > 32 {
		return fmt.Errorf("mode_options must contain between 1 and 32 entries")
	}
	for _, option := range c.ModeOptions {
		if strings.TrimSpace(option) == "" || len(option) > 256 {
			return fmt.Errorf("mode option is empty or too long")
		}
	}
	if c.FileUploadControl != "observed" && c.FileUploadControl != "not_observed" {
		return fmt.Errorf("file_upload_control is invalid")
	}
	if c.FileUploadAction != "unsupported" {
		return fmt.Errorf("file_upload_action must remain unsupported")
	}
	if c.ExplicitModeSelection != "request_shape_unobserved" {
		return fmt.Errorf("explicit_mode_selection must remain request_shape_unobserved")
	}
	if c.Source != "headed-cdp-rendered-controls" {
		return fmt.Errorf("source is not an accepted capability observation")
	}
	return nil
}

func authStatusFromState(
	status AuthStatus,
	state AuthState,
	capturedAt time.Time,
	now time.Time,
	ttl time.Duration,
) AuthStatus {
	now = now.UTC()
	capturedAt = capturedAt.UTC()
	expiresAt := capturedAt.Add(ttl)
	status.CapturedAt = capturedAt.Format(time.RFC3339Nano)
	status.ExpiresAt = expiresAt.Format(time.RFC3339Nano)
	status.Stale = !now.Before(expiresAt)
	if capturedAt.After(now.Add(5 * time.Minute)) {
		status.State = "invalid"
		status.Reason = "auth evidence capture time is unexpectedly in the future"
		return status
	}
	if status.Stale {
		status.State = "expired"
		status.Reason = "auth evidence exceeded its freshness window"
		return status
	}
	status.State = "ready"
	status.Ready = true
	status.Reason = ""
	return status
}

func saveOwnerJSON(ctx context.Context, path string, value any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal owner-only JSON: %w", err)
	}
	data = append(data, '\n')
	return artifacts.WithOwnerOnlyFileLock(ctx, path+".lock", func() error {
		return artifacts.WriteOwnerOnlyFileAtomic(path, data)
	})
}

func loadOwnerJSON(ctx context.Context, path string, target any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	data, err := artifacts.ReadOwnerOnlyFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("parse owner-only JSON: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("parse trailing owner-only JSON: %w", err)
	}
	return fmt.Errorf("owner-only JSON contains trailing data")
}
