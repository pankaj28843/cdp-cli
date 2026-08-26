package m365

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
	Origin                           = "https://m365.cloud.microsoft"
	HomeURL                          = Origin + "/chat"
	AugLoopURL                       = "wss://augloop.svc.cloud.microsoft/"
	AuthTemplateSchemaVersion        = "m365-auth-template/v1"
	RuntimeCapabilitiesSchemaVersion = "m365-runtime-capabilities/v1"
	TranscriptionSchemaVersion       = "m365-transcription/v1"
	DefaultAuthTTL                   = 45 * time.Minute
	DefaultCapabilitiesTTL           = 14 * 24 * time.Hour
	RelativeTemplatePath             = "webagent/m365/auth-template.json"
	RelativeCapabilitiesPath         = "webagent/m365/capabilities.json"
	legacyAuthTemplateSource         = "headed-cdp-observed-token-provision"
	directAuthTemplateSource         = "headed-cdp-runtime-token-provider"
	legacyRuntimeSource              = "headed-cdp-dictation-probe"
	directRuntimeSource              = "headed-cdp-direct-websocket-probe"
	maxAuthTokenBytes                = 128 << 10
)

type ClientMetadata struct {
	AppName              string `json:"app_name"`
	AppPlatform          string `json:"app_platform"`
	AppVersion           string `json:"app_version"`
	ReleaseAudienceGroup string `json:"release_audience_group"`
	ReleaseChannel       string `json:"release_channel"`
	ReleaseFork          string `json:"release_fork"`
	Flights              string `json:"flights"`
	UserSystemTimezone   string `json:"user_system_timezone,omitempty"`
	RuntimeVersion       string `json:"runtime_version"`
}

type AuthTemplate struct {
	SchemaVersion    string         `json:"schema_version"`
	AuthToken        string         `json:"auth_token"`
	ClientMetadata   ClientMetadata `json:"client_metadata"`
	BrowserUserAgent string         `json:"browser_user_agent"`
	CapturedAt       string         `json:"captured_at"`
	Source           string         `json:"source"`
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
	SchemaVersion       string `json:"schema_version"`
	State               string `json:"state"`
	CapturedAt          string `json:"captured_at"`
	ComposerObserved    bool   `json:"composer_observed"`
	DictationObserved   bool   `json:"dictation_observed"`
	WebSocketObserved   bool   `json:"websocket_observed"`
	TokenProvisioned    bool   `json:"token_provisioned"`
	AudioProtocol       string `json:"audio_protocol"`
	FinalResultObserved bool   `json:"final_result_observed"`
	Source              string `json:"source"`
	Message             string `json:"message,omitempty"`
}

type RuntimeStatus struct {
	SchemaVersion       string `json:"schema_version"`
	State               string `json:"state"`
	Ready               bool   `json:"ready"`
	Stale               bool   `json:"stale"`
	StatePath           string `json:"state_path"`
	CapturedAt          string `json:"captured_at,omitempty"`
	ExpiresAt           string `json:"expires_at,omitempty"`
	ComposerObserved    bool   `json:"composer_observed"`
	DictationObserved   bool   `json:"dictation_observed"`
	WebSocketObserved   bool   `json:"websocket_observed"`
	TokenProvisioned    bool   `json:"token_provisioned"`
	AudioProtocol       string `json:"audio_protocol"`
	FinalResultObserved bool   `json:"final_result_observed"`
	Reason              string `json:"reason,omitempty"`
	Message             string `json:"message,omitempty"`
}

type Store struct {
	templatePath     string
	capabilitiesPath string
}

func NewStore(stateDir string) (*Store, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("Microsoft 365 state directory is required")
	}
	return &Store{
		templatePath:     filepath.Join(stateDir, filepath.FromSlash(RelativeTemplatePath)),
		capabilitiesPath: filepath.Join(stateDir, filepath.FromSlash(RelativeCapabilitiesPath)),
	}, nil
}

func (s *Store) SaveTemplate(ctx context.Context, template AuthTemplate) error {
	if s == nil || s.templatePath == "" {
		return fmt.Errorf("Microsoft 365 auth store is not configured")
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("validate Microsoft 365 auth template: %w", err)
	}
	return saveOwnerJSON(ctx, s.templatePath, template)
}

func (s *Store) LoadTemplate(ctx context.Context) (AuthTemplate, error) {
	if s == nil || s.templatePath == "" {
		return AuthTemplate{}, fmt.Errorf("Microsoft 365 auth store is not configured")
	}
	var template AuthTemplate
	if err := loadOwnerJSON(ctx, s.templatePath, &template); err != nil {
		return AuthTemplate{}, err
	}
	if err := template.Validate(); err != nil {
		return AuthTemplate{}, fmt.Errorf("validate Microsoft 365 auth template: %w", err)
	}
	return template, nil
}

func (s *Store) AuthStatus(ctx context.Context, now time.Time, ttl time.Duration) AuthStatus {
	_, status, _ := s.LoadTemplateStatus(ctx, now, ttl)
	return status
}

func (s *Store) LoadTemplateStatus(
	ctx context.Context,
	now time.Time,
	ttl time.Duration,
) (AuthTemplate, AuthStatus, error) {
	status := AuthStatus{
		SchemaVersion: AuthTemplateSchemaVersion,
		State:         "missing",
		StatePath:     RelativeTemplatePath,
		Reason:        "auth token evidence is not present",
	}
	if ttl <= 0 {
		ttl = DefaultAuthTTL
	}
	template, err := s.LoadTemplate(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return AuthTemplate{}, status, err
		}
		status.State = "invalid"
		status.Reason = "auth token evidence failed owner-only validation"
		return AuthTemplate{}, status, err
	}
	capturedAt, _ := time.Parse(time.RFC3339Nano, template.CapturedAt)
	status = authStatusFromCapturedAt(status, capturedAt, now, ttl)
	return template, status, nil
}

func (s *Store) SaveRuntime(ctx context.Context, runtime RuntimeCapabilities) error {
	if s == nil || s.capabilitiesPath == "" {
		return fmt.Errorf("Microsoft 365 capability store is not configured")
	}
	if err := runtime.Validate(); err != nil {
		return fmt.Errorf("validate Microsoft 365 runtime capabilities: %w", err)
	}
	return saveOwnerJSON(ctx, s.capabilitiesPath, runtime)
}

func (s *Store) LoadRuntime(ctx context.Context) (RuntimeCapabilities, error) {
	if s == nil || s.capabilitiesPath == "" {
		return RuntimeCapabilities{}, fmt.Errorf("Microsoft 365 capability store is not configured")
	}
	var runtime RuntimeCapabilities
	if err := loadOwnerJSON(ctx, s.capabilitiesPath, &runtime); err != nil {
		return RuntimeCapabilities{}, err
	}
	if err := runtime.Validate(); err != nil {
		return RuntimeCapabilities{}, fmt.Errorf("validate Microsoft 365 runtime capabilities: %w", err)
	}
	return runtime, nil
}

func (s *Store) RuntimeStatus(ctx context.Context, now time.Time, ttl time.Duration) RuntimeStatus {
	status := RuntimeStatus{
		SchemaVersion: RuntimeCapabilitiesSchemaVersion,
		State:         "missing",
		StatePath:     RelativeCapabilitiesPath,
		AudioProtocol: "AugLoop_Voice_VoiceTile/v2",
		Reason:        "runtime capability evidence is not present",
	}
	if ttl <= 0 {
		ttl = DefaultCapabilitiesTTL
	}
	runtime, err := s.LoadRuntime(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return status
		}
		status.State = "invalid"
		status.Reason = "runtime capability evidence failed owner-only validation"
		return status
	}
	capturedAt, _ := time.Parse(time.RFC3339Nano, runtime.CapturedAt)
	timing := authStatusFromCapturedAt(AuthStatus{}, capturedAt, now, ttl)
	status.State = timing.State
	status.Ready = timing.Ready
	status.Stale = timing.Stale
	status.CapturedAt = timing.CapturedAt
	status.ExpiresAt = timing.ExpiresAt
	status.Reason = timing.Reason
	status.ComposerObserved = runtime.ComposerObserved
	status.DictationObserved = runtime.DictationObserved
	status.WebSocketObserved = runtime.WebSocketObserved
	status.TokenProvisioned = runtime.TokenProvisioned
	status.AudioProtocol = runtime.AudioProtocol
	status.FinalResultObserved = runtime.FinalResultObserved
	status.Message = runtime.Message
	if status.Ready && runtime.State != "ready" {
		status.Ready = false
		status.Reason = "Microsoft 365 dictation capability evidence was not proven"
	}
	return status
}

func (t AuthTemplate) Validate() error {
	if t.SchemaVersion != AuthTemplateSchemaVersion {
		return fmt.Errorf("schema_version must be %q", AuthTemplateSchemaVersion)
	}
	if err := validatePrivateValue("auth_token", t.AuthToken, maxAuthTokenBytes); err != nil {
		return err
	}
	if err := t.ClientMetadata.Validate(); err != nil {
		return err
	}
	if err := validatePrivateValue("browser_user_agent", t.BrowserUserAgent, 4096); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, t.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if t.Source != legacyAuthTemplateSource && t.Source != directAuthTemplateSource {
		return fmt.Errorf("source is not an accepted Microsoft 365 token observation")
	}
	return nil
}

func (m ClientMetadata) Validate() error {
	for name, value := range map[string]string{
		"client_metadata.app_name":               m.AppName,
		"client_metadata.app_platform":           m.AppPlatform,
		"client_metadata.app_version":            m.AppVersion,
		"client_metadata.release_audience_group": m.ReleaseAudienceGroup,
		"client_metadata.flights":                m.Flights,
		"client_metadata.runtime_version":        m.RuntimeVersion,
	} {
		if err := validatePrivateValue(name, value, 4096); err != nil {
			return err
		}
	}
	if m.UserSystemTimezone != "" {
		if err := validatePrivateValue("client_metadata.user_system_timezone", m.UserSystemTimezone, 256); err != nil {
			return err
		}
	}
	return nil
}

func (r RuntimeCapabilities) Validate() error {
	if r.SchemaVersion != RuntimeCapabilitiesSchemaVersion {
		return fmt.Errorf("schema_version must be %q", RuntimeCapabilitiesSchemaVersion)
	}
	if r.State != "ready" {
		return fmt.Errorf("runtime state must be ready")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if r.AudioProtocol != "AugLoop_Voice_VoiceTile/v2" {
		return fmt.Errorf("audio_protocol must be the observed AugLoop VoiceTile v2 protocol")
	}
	if !r.DictationObserved || !r.WebSocketObserved || !r.TokenProvisioned {
		return fmt.Errorf("runtime capability evidence is incomplete")
	}
	if r.Source != legacyRuntimeSource && r.Source != directRuntimeSource {
		return fmt.Errorf("source is not an accepted Microsoft 365 capability observation")
	}
	return nil
}

func authStatusFromCapturedAt(
	status AuthStatus,
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
	switch {
	case capturedAt.After(now.Add(5 * time.Minute)):
		status.State = "invalid"
		status.Reason = "capture time is unexpectedly in the future"
	case status.Stale:
		status.State = "expired"
		status.Reason = "evidence exceeded its freshness window"
	default:
		status.State = "ready"
		status.Ready = true
		status.Reason = ""
	}
	return status
}

func validatePrivateValue(label, value string, maximum int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", label)
	}
	if len(value) > maximum {
		return fmt.Errorf("%s exceeds its bound", label)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s contains unsupported control characters", label)
	}
	return nil
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
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return fmt.Errorf("parse trailing owner-only JSON: %w", err)
		}
		return fmt.Errorf("owner-only JSON contains trailing data")
	}
	return nil
}
