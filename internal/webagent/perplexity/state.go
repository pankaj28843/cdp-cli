package perplexity

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
	Origin                           = "https://www.perplexity.ai"
	HomeURL                          = Origin + "/"
	ConversationListPath             = "/rest/thread/list_recent"
	ModelConfigPath                  = "/rest/models/config"
	AuthTemplateSchemaVersion        = "perplexity-auth-template/v1"
	RuntimeCapabilitiesSchemaVersion = "perplexity-runtime-capabilities/v1"
	RelativeTemplatePath             = "webagent/perplexity/request-template.json"
	RelativeCapabilitiesPath         = "webagent/perplexity/capabilities.json"
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

type ComposerCapability struct {
	ID            string         `json:"capability_id"`
	Label         string         `json:"label"`
	Description   string         `json:"description,omitempty"`
	Kind          string         `json:"kind"`
	Selected      bool           `json:"selected"`
	Available     bool           `json:"available"`
	FailureReason string         `json:"failure_reason,omitempty"`
	Metadata      map[string]any `json:"metadata"`
}

type RuntimeCapabilities struct {
	SchemaVersion string               `json:"schema_version"`
	State         string               `json:"state"`
	CapturedAt    string               `json:"captured_at"`
	Capabilities  []ComposerCapability `json:"capabilities"`
	Source        string               `json:"source"`
	Message       string               `json:"message,omitempty"`
}

type RuntimeStatus struct {
	SchemaVersion string               `json:"schema_version"`
	State         string               `json:"state"`
	Ready         bool                 `json:"ready"`
	Stale         bool                 `json:"stale"`
	StatePath     string               `json:"state_path"`
	CapturedAt    string               `json:"captured_at,omitempty"`
	ExpiresAt     string               `json:"expires_at,omitempty"`
	Capabilities  []ComposerCapability `json:"capabilities"`
	Reason        string               `json:"reason,omitempty"`
	Message       string               `json:"message,omitempty"`
}

type Store struct {
	templatePath     string
	capabilitiesPath string
}

func NewStore(stateDir string) (*Store, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("Perplexity state directory is required")
	}
	return &Store{
		templatePath:     filepath.Join(stateDir, filepath.FromSlash(RelativeTemplatePath)),
		capabilitiesPath: filepath.Join(stateDir, filepath.FromSlash(RelativeCapabilitiesPath)),
	}, nil
}

func (s *Store) SaveTemplate(ctx context.Context, template RequestTemplate) error {
	if s == nil || s.templatePath == "" {
		return fmt.Errorf("Perplexity auth store is not configured")
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("validate Perplexity request template: %w", err)
	}
	return saveOwnerJSON(ctx, s.templatePath, template)
}

func (s *Store) LoadTemplate(ctx context.Context) (RequestTemplate, error) {
	if s == nil || s.templatePath == "" {
		return RequestTemplate{}, fmt.Errorf("Perplexity auth store is not configured")
	}
	var template RequestTemplate
	if err := loadOwnerJSON(ctx, s.templatePath, &template); err != nil {
		return RequestTemplate{}, err
	}
	if err := template.Validate(); err != nil {
		return RequestTemplate{}, fmt.Errorf("validate Perplexity request template: %w", err)
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
		return fmt.Errorf("Perplexity capability store is not configured")
	}
	if err := runtime.Validate(); err != nil {
		return fmt.Errorf("validate Perplexity runtime capabilities: %w", err)
	}
	return saveOwnerJSON(ctx, s.capabilitiesPath, runtime)
}

func (s *Store) LoadRuntime(ctx context.Context) (RuntimeCapabilities, error) {
	if s == nil || s.capabilitiesPath == "" {
		return RuntimeCapabilities{}, fmt.Errorf("Perplexity capability store is not configured")
	}
	var runtime RuntimeCapabilities
	if err := loadOwnerJSON(ctx, s.capabilitiesPath, &runtime); err != nil {
		return RuntimeCapabilities{}, err
	}
	if err := runtime.Validate(); err != nil {
		return RuntimeCapabilities{}, fmt.Errorf("validate Perplexity runtime capabilities: %w", err)
	}
	return runtime, nil
}

func (s *Store) RuntimeStatus(ctx context.Context, now time.Time, ttl time.Duration) RuntimeStatus {
	status := RuntimeStatus{
		SchemaVersion: RuntimeCapabilitiesSchemaVersion,
		State:         "missing",
		StatePath:     RelativeCapabilitiesPath,
		Capabilities:  []ComposerCapability{},
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
	status.Capabilities = append([]ComposerCapability(nil), runtime.Capabilities...)
	status.Message = runtime.Message
	if status.Ready {
		status.State = runtime.State
	}
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
		parsed.Host != "www.perplexity.ai" ||
		parsed.Path != ConversationListPath ||
		parsed.User != nil ||
		parsed.Fragment != "" {
		return fmt.Errorf("url must be the observed Perplexity recent-thread endpoint")
	}
	query := parsed.Query()
	if query.Get("version") == "" || query.Get("source") == "" {
		return fmt.Errorf("url must preserve the observed version and source query")
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
		return fmt.Errorf("source is not an accepted Perplexity request observation")
	}
	return nil
}

func (r RuntimeCapabilities) Validate() error {
	if r.SchemaVersion != RuntimeCapabilitiesSchemaVersion {
		return fmt.Errorf("schema_version must be %q", RuntimeCapabilitiesSchemaVersion)
	}
	if r.State != "ready" && r.State != "unknown" {
		return fmt.Errorf("runtime state must be ready or unknown")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if len(r.Capabilities) > 64 {
		return fmt.Errorf("capabilities exceed their bound")
	}
	if r.State == "unknown" && len(r.Capabilities) != 0 {
		return fmt.Errorf("unknown runtime state must not invent capabilities")
	}
	for _, capability := range r.Capabilities {
		if err := capability.Validate(); err != nil {
			return err
		}
	}
	if r.Source != "headed-cdp-observed-model-config" &&
		r.Source != "headed-cdp-model-config-not-observed" {
		return fmt.Errorf("source is not an accepted Perplexity capability observation")
	}
	return nil
}

func (c ComposerCapability) Validate() error {
	if strings.TrimSpace(c.ID) == "" || len(c.ID) > 256 {
		return fmt.Errorf("capability_id is required and bounded")
	}
	if strings.TrimSpace(c.Label) == "" || len(c.Label) > 256 {
		return fmt.Errorf("capability label is required and bounded")
	}
	if c.Kind != "search" && c.Kind != "computer" {
		return fmt.Errorf("capability kind must be search or computer")
	}
	if len(c.Description) > 4096 || len(c.FailureReason) > 512 || len(c.Metadata) > 32 {
		return fmt.Errorf("capability metadata exceeds bounds")
	}
	if c.Available && c.FailureReason != "" {
		return fmt.Errorf("available capability must not have failure_reason")
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
