package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

const (
	Origin                           = "https://grok.com"
	HomeURL                          = Origin + "/"
	ConversationListPath             = "/rest/app-chat/conversations"
	AuthTemplateSchemaVersion        = "grok-auth-template/v1"
	RuntimeCapabilitiesSchemaVersion = "grok-runtime-capabilities/v1"
	RelativeTemplatePath             = "webagent/grok/request-template.json"
	RelativeCapabilitiesPath         = "webagent/grok/capabilities.json"
	DefaultAuthTTL                   = time.Hour
	DefaultCapabilitiesTTL           = 14 * 24 * time.Hour
)

type RequestTemplate struct {
	SchemaVersion    string            `json:"schema_version"`
	Method           string            `json:"method"`
	URL              string            `json:"url"`
	Headers          map[string]string `json:"headers"`
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

type Mode struct {
	ID            string   `json:"mode_id"`
	Title         string   `json:"title"`
	Description   string   `json:"description,omitempty"`
	Available     bool     `json:"available"`
	Selected      bool     `json:"selected"`
	FailureReason string   `json:"failure_reason,omitempty"`
	Tags          []string `json:"tags"`
}

type RuntimeCapabilities struct {
	SchemaVersion string `json:"schema_version"`
	CapturedAt    string `json:"captured_at"`
	DefaultModeID string `json:"default_mode_id"`
	Modes         []Mode `json:"modes"`
	Source        string `json:"source"`
}

type RuntimeStatus struct {
	SchemaVersion string `json:"schema_version"`
	State         string `json:"state"`
	Ready         bool   `json:"ready"`
	Stale         bool   `json:"stale"`
	StatePath     string `json:"state_path"`
	CapturedAt    string `json:"captured_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	DefaultModeID string `json:"default_mode_id,omitempty"`
	Modes         []Mode `json:"modes"`
	Reason        string `json:"reason,omitempty"`
}

type Store struct {
	templatePath     string
	capabilitiesPath string
}

func NewStore(stateDir string) (*Store, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("Grok state directory is required")
	}
	return &Store{
		templatePath:     filepath.Join(stateDir, filepath.FromSlash(RelativeTemplatePath)),
		capabilitiesPath: filepath.Join(stateDir, filepath.FromSlash(RelativeCapabilitiesPath)),
	}, nil
}

func (s *Store) SaveTemplate(ctx context.Context, template RequestTemplate) error {
	if s == nil || s.templatePath == "" {
		return fmt.Errorf("Grok auth store is not configured")
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("validate Grok request template: %w", err)
	}
	return saveOwnerJSON(ctx, s.templatePath, template)
}

func (s *Store) LoadTemplate(ctx context.Context) (RequestTemplate, error) {
	if s == nil || s.templatePath == "" {
		return RequestTemplate{}, fmt.Errorf("Grok auth store is not configured")
	}
	var template RequestTemplate
	if err := loadOwnerJSON(ctx, s.templatePath, &template); err != nil {
		return RequestTemplate{}, err
	}
	if err := template.Validate(); err != nil {
		return RequestTemplate{}, fmt.Errorf("validate Grok request template: %w", err)
	}
	return template, nil
}

func (s *Store) AuthStatus(ctx context.Context, now time.Time, ttl time.Duration) AuthStatus {
	status := AuthStatus{
		SchemaVersion: AuthTemplateSchemaVersion,
		State:         "missing",
		StatePath:     RelativeTemplatePath,
		Reason:        "auth request template is not present",
	}
	if ttl <= 0 {
		ttl = DefaultAuthTTL
	}
	template, err := s.LoadTemplate(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return status
		}
		status.State = "invalid"
		status.Reason = "auth request template failed owner-only validation"
		return status
	}
	capturedAt, _ := time.Parse(time.RFC3339Nano, template.CapturedAt)
	return statusFromCapturedAt(status, capturedAt, now, ttl)
}

func (s *Store) SaveRuntime(ctx context.Context, runtime RuntimeCapabilities) error {
	if s == nil || s.capabilitiesPath == "" {
		return fmt.Errorf("Grok capability store is not configured")
	}
	if err := runtime.Validate(); err != nil {
		return fmt.Errorf("validate Grok runtime capabilities: %w", err)
	}
	return saveOwnerJSON(ctx, s.capabilitiesPath, runtime)
}

func (s *Store) LoadRuntime(ctx context.Context) (RuntimeCapabilities, error) {
	if s == nil || s.capabilitiesPath == "" {
		return RuntimeCapabilities{}, fmt.Errorf("Grok capability store is not configured")
	}
	var runtime RuntimeCapabilities
	if err := loadOwnerJSON(ctx, s.capabilitiesPath, &runtime); err != nil {
		return RuntimeCapabilities{}, err
	}
	if err := runtime.Validate(); err != nil {
		return RuntimeCapabilities{}, fmt.Errorf("validate Grok runtime capabilities: %w", err)
	}
	return runtime, nil
}

func (s *Store) RuntimeStatus(ctx context.Context, now time.Time, ttl time.Duration) RuntimeStatus {
	status := RuntimeStatus{
		SchemaVersion: RuntimeCapabilitiesSchemaVersion,
		State:         "missing",
		StatePath:     RelativeCapabilitiesPath,
		Modes:         []Mode{},
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
	base := AuthStatus{
		SchemaVersion: RuntimeCapabilitiesSchemaVersion,
		State:         "missing",
		StatePath:     RelativeCapabilitiesPath,
	}
	timing := statusFromCapturedAt(base, capturedAt, now, ttl)
	status.State = timing.State
	status.Ready = timing.Ready
	status.Stale = timing.Stale
	status.CapturedAt = timing.CapturedAt
	status.ExpiresAt = timing.ExpiresAt
	status.Reason = timing.Reason
	status.DefaultModeID = runtime.DefaultModeID
	status.Modes = append([]Mode(nil), runtime.Modes...)
	return status
}

func (t RequestTemplate) Validate() error {
	if t.SchemaVersion != AuthTemplateSchemaVersion {
		return fmt.Errorf("schema_version must be %q", AuthTemplateSchemaVersion)
	}
	if t.Method != "GET" {
		return fmt.Errorf("method must be GET")
	}
	parsed, err := url.Parse(t.URL)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host != "grok.com" ||
		parsed.Path != ConversationListPath ||
		parsed.User != nil ||
		parsed.Fragment != "" {
		return fmt.Errorf("url must be the observed Grok conversation-list endpoint")
	}
	if parsed.Query().Get("pageSize") == "" {
		return fmt.Errorf("url must preserve the observed pageSize query")
	}
	if len(t.Headers) == 0 || len(t.Headers) > 128 {
		return fmt.Errorf("headers must contain between 1 and 128 entries")
	}
	for name, value := range t.Headers {
		if err := validatePrivateValue("header name", name, 1024); err != nil {
			return err
		}
		if err := validatePrivateValue("header value", value, 64<<10); err != nil {
			return err
		}
	}
	if len(t.Cookies) == 0 || len(t.Cookies) > 512 {
		return fmt.Errorf("cookies must contain between 1 and 512 entries")
	}
	for name, value := range t.Cookies {
		if err := validatePrivateValue("cookie name", name, 1024); err != nil {
			return err
		}
		if err := validatePrivateValue("cookie value", value, 64<<10); err != nil {
			return err
		}
	}
	if strings.TrimSpace(t.BrowserUserAgent) == "" || len(t.BrowserUserAgent) > 4096 {
		return fmt.Errorf("browser_user_agent is required and bounded")
	}
	if _, err := time.Parse(time.RFC3339Nano, t.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if t.Source != "headed-cdp-observed-list-request" &&
		t.Source != "headed-cdp-retained-list-shape" {
		return fmt.Errorf("source is not an accepted Grok request observation")
	}
	return nil
}

func (r RuntimeCapabilities) Validate() error {
	if r.SchemaVersion != RuntimeCapabilitiesSchemaVersion {
		return fmt.Errorf("schema_version must be %q", RuntimeCapabilitiesSchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339Nano, r.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if strings.TrimSpace(r.DefaultModeID) == "" || len(r.DefaultModeID) > 256 {
		return fmt.Errorf("default_mode_id is required and bounded")
	}
	if len(r.Modes) == 0 || len(r.Modes) > 32 {
		return fmt.Errorf("modes must contain between 1 and 32 entries")
	}
	selected := 0
	defaultAvailable := false
	seen := map[string]struct{}{}
	for _, mode := range r.Modes {
		if err := mode.Validate(); err != nil {
			return err
		}
		if _, ok := seen[mode.ID]; ok {
			return fmt.Errorf("mode ids must be unique")
		}
		seen[mode.ID] = struct{}{}
		if mode.Selected {
			selected++
		}
		if mode.ID == r.DefaultModeID && mode.Available && mode.Selected {
			defaultAvailable = true
		}
	}
	if selected != 1 || !defaultAvailable {
		return fmt.Errorf("exactly one available selected default mode is required")
	}
	if r.Source != "headed-cdp-observed-modes-response" {
		return fmt.Errorf("source is not an accepted capability observation")
	}
	return nil
}

func (m Mode) Validate() error {
	if strings.TrimSpace(m.ID) == "" || len(m.ID) > 256 {
		return fmt.Errorf("mode_id is required and bounded")
	}
	if strings.TrimSpace(m.Title) == "" || len(m.Title) > 256 {
		return fmt.Errorf("mode title is required and bounded")
	}
	if len(m.Description) > 4096 || len(m.FailureReason) > 512 || len(m.Tags) > 32 {
		return fmt.Errorf("mode metadata exceeds bounds")
	}
	if m.Available && m.FailureReason != "" {
		return fmt.Errorf("available mode must not have failure_reason")
	}
	return nil
}

func statusFromCapturedAt(
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
