package chatgpt

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
	Origin                           = "https://chatgpt.com"
	HomeURL                          = Origin + "/"
	ConversationListPath             = "/backend-api/conversations"
	RoomsSummaryPath                 = "/backend-api/calpico/chatgpt/rooms/summary"
	AuthTemplateSchemaVersion        = "chatgpt-auth-template/v1"
	RuntimeCapabilitiesSchemaVersion = "chatgpt-runtime-capabilities/v1"
	RelativeTemplatePath             = "webagent/chatgpt/request-template.json"
	RelativeCapabilitiesPath         = "webagent/chatgpt/capabilities.json"
	DefaultAuthTTL                   = time.Hour
	DefaultCapabilitiesTTL           = 14 * 24 * time.Hour
)

type RequestTemplate struct {
	SchemaVersion    string            `json:"schema_version"`
	Method           string            `json:"method"`
	URL              string            `json:"url"`
	Headers          map[string]string `json:"headers"`
	Cookies          map[string]string `json:"cookies"`
	CookieHeader     string            `json:"cookie_header"`
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
	SchemaVersion        string   `json:"schema_version"`
	State                string   `json:"state"`
	CapturedAt           string   `json:"captured_at"`
	ComposerObserved     bool     `json:"composer_observed"`
	ProductModes         []string `json:"product_modes"`
	SelectedProduct      string   `json:"selected_product,omitempty"`
	IntelligenceOptions  []string `json:"intelligence_options"`
	SelectedIntelligence string   `json:"selected_intelligence,omitempty"`
	ModelOptions         []string `json:"model_options"`
	SelectedModel        string   `json:"selected_model,omitempty"`
	ModelOptionsObserved bool     `json:"model_options_observed"`
	FileUploadObserved   bool     `json:"file_upload_observed"`
	Tools                []string `json:"tools"`
	Source               string   `json:"source"`
	Message              string   `json:"message,omitempty"`
}

type RuntimeStatus struct {
	SchemaVersion        string   `json:"schema_version"`
	State                string   `json:"state"`
	Ready                bool     `json:"ready"`
	Stale                bool     `json:"stale"`
	StatePath            string   `json:"state_path"`
	CapturedAt           string   `json:"captured_at,omitempty"`
	ExpiresAt            string   `json:"expires_at,omitempty"`
	ComposerObserved     bool     `json:"composer_observed"`
	ProductModes         []string `json:"product_modes"`
	SelectedProduct      string   `json:"selected_product,omitempty"`
	IntelligenceOptions  []string `json:"intelligence_options"`
	SelectedIntelligence string   `json:"selected_intelligence,omitempty"`
	ModelOptions         []string `json:"model_options"`
	SelectedModel        string   `json:"selected_model,omitempty"`
	ModelOptionsObserved bool     `json:"model_options_observed"`
	FileUploadObserved   bool     `json:"file_upload_observed"`
	Tools                []string `json:"tools"`
	Reason               string   `json:"reason,omitempty"`
	Message              string   `json:"message,omitempty"`
}

type Store struct {
	templatePath     string
	capabilitiesPath string
}

func NewStore(stateDir string) (*Store, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("ChatGPT state directory is required")
	}
	return &Store{
		templatePath:     filepath.Join(stateDir, filepath.FromSlash(RelativeTemplatePath)),
		capabilitiesPath: filepath.Join(stateDir, filepath.FromSlash(RelativeCapabilitiesPath)),
	}, nil
}

func (s *Store) SaveTemplate(ctx context.Context, template RequestTemplate) error {
	if s == nil || s.templatePath == "" {
		return fmt.Errorf("ChatGPT auth store is not configured")
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("validate ChatGPT request template: %w", err)
	}
	return saveOwnerJSON(ctx, s.templatePath, template)
}

func (s *Store) LoadTemplate(ctx context.Context) (RequestTemplate, error) {
	if s == nil || s.templatePath == "" {
		return RequestTemplate{}, fmt.Errorf("ChatGPT auth store is not configured")
	}
	var template RequestTemplate
	if err := loadOwnerJSON(ctx, s.templatePath, &template); err != nil {
		return RequestTemplate{}, err
	}
	if err := template.Validate(); err != nil {
		return RequestTemplate{}, fmt.Errorf("validate ChatGPT request template: %w", err)
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
) (RequestTemplate, AuthStatus, error) {
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
			return RequestTemplate{}, status, err
		}
		status.State = "invalid"
		status.Reason = "auth request template failed owner-only validation"
		return RequestTemplate{}, status, err
	}
	capturedAt, _ := time.Parse(time.RFC3339Nano, template.CapturedAt)
	status = authStatusFromCapturedAt(status, capturedAt, now, ttl)
	return template, status, nil
}

func (s *Store) SaveRuntime(ctx context.Context, runtime RuntimeCapabilities) error {
	if s == nil || s.capabilitiesPath == "" {
		return fmt.Errorf("ChatGPT capability store is not configured")
	}
	if err := runtime.Validate(); err != nil {
		return fmt.Errorf("validate ChatGPT runtime capabilities: %w", err)
	}
	return saveOwnerJSON(ctx, s.capabilitiesPath, runtime)
}

func (s *Store) LoadRuntime(ctx context.Context) (RuntimeCapabilities, error) {
	if s == nil || s.capabilitiesPath == "" {
		return RuntimeCapabilities{}, fmt.Errorf("ChatGPT capability store is not configured")
	}
	var runtime RuntimeCapabilities
	if err := loadOwnerJSON(ctx, s.capabilitiesPath, &runtime); err != nil {
		return RuntimeCapabilities{}, err
	}
	runtime = normalizeRuntimeCapabilities(runtime)
	if err := runtime.Validate(); err != nil {
		return RuntimeCapabilities{}, fmt.Errorf("validate ChatGPT runtime capabilities: %w", err)
	}
	return runtime, nil
}

func normalizeRuntimeCapabilities(runtime RuntimeCapabilities) RuntimeCapabilities {
	runtime.IntelligenceOptions = removeLegacyMixedIntelligenceLabels(
		runtime.IntelligenceOptions,
	)
	if legacyMixedIntelligenceLabel(runtime.SelectedIntelligence) ||
		(strings.TrimSpace(runtime.SelectedIntelligence) != "" &&
			!containsString(
				runtime.IntelligenceOptions,
				runtime.SelectedIntelligence,
			)) {
		runtime.SelectedIntelligence = ""
	}
	if runtime.State == "ready" &&
		strings.TrimSpace(runtime.SelectedIntelligence) == "" {
		runtime.State = "unknown"
	}
	if !runtime.ModelOptionsObserved &&
		len(runtime.ModelOptions) > 0 &&
		strings.TrimSpace(runtime.SelectedModel) != "" &&
		containsString(runtime.ModelOptions, runtime.SelectedModel) {
		// Compatibility with capability state written before the independent
		// observation bit existed. A selected member of the persisted visible
		// catalog is the old format's positive observation evidence.
		runtime.ModelOptionsObserved = true
	}
	_, runtime.Message = capabilityStateAndMessage(capabilityProbe{
		OK:                   runtime.State == "ready",
		ComposerObserved:     runtime.ComposerObserved,
		ProductModes:         runtime.ProductModes,
		IntelligenceOptions:  runtime.IntelligenceOptions,
		SelectedIntelligence: runtime.SelectedIntelligence,
		ModelOptions:         runtime.ModelOptions,
		SelectedModel:        runtime.SelectedModel,
		ModelOptionsObserved: runtime.ModelOptionsObserved,
	})
	return runtime
}

func removeLegacyMixedIntelligenceLabels(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if legacyMixedIntelligenceLabel(value) ||
			containsString(normalized, value) {
			continue
		}
		normalized = append(normalized, value)
	}
	return normalized
}

func legacyMixedIntelligenceLabel(value string) bool {
	// The previous v1 writer admitted this model label into the thinking list.
	// Keep the compatibility rule narrow; do not classify future dynamic
	// thinking labels as models by heuristic.
	return strings.EqualFold(strings.TrimSpace(value), "GPT-5.6 Sol")
}

func (s *Store) RuntimeStatus(ctx context.Context, now time.Time, ttl time.Duration) RuntimeStatus {
	status := RuntimeStatus{
		SchemaVersion:       RuntimeCapabilitiesSchemaVersion,
		State:               "missing",
		StatePath:           RelativeCapabilitiesPath,
		ProductModes:        []string{},
		IntelligenceOptions: []string{},
		ModelOptions:        []string{},
		Tools:               []string{},
		Reason:              "runtime capability evidence is not present",
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
	status.ProductModes = append([]string{}, runtime.ProductModes...)
	status.SelectedProduct = runtime.SelectedProduct
	status.IntelligenceOptions = append([]string{}, runtime.IntelligenceOptions...)
	status.SelectedIntelligence = runtime.SelectedIntelligence
	status.ModelOptions = append([]string{}, runtime.ModelOptions...)
	status.SelectedModel = runtime.SelectedModel
	status.ModelOptionsObserved = runtime.ModelOptionsObserved
	status.FileUploadObserved = runtime.FileUploadObserved
	status.Tools = append([]string{}, runtime.Tools...)
	status.Message = runtime.Message
	if status.Ready {
		status.State = runtime.State
		if runtime.State != "ready" {
			status.Ready = false
			status.Reason = "Chat composer capability evidence was not proven"
		}
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
		parsed.Host != "chatgpt.com" ||
		!isReadAuthPath(parsed.Path) ||
		parsed.User != nil ||
		parsed.Fragment != "" {
		return fmt.Errorf("url must be an observed ChatGPT conversation-read endpoint")
	}
	if len(t.Headers) == 0 || len(t.Headers) > 128 {
		return fmt.Errorf("headers must contain between 1 and 128 entries")
	}
	if strings.TrimSpace(t.Headers["user-agent"]) == "" {
		return fmt.Errorf("headers must preserve the browser user-agent")
	}
	if strings.TrimSpace(t.Headers["authorization"]) == "" {
		return fmt.Errorf("headers must preserve observed conversation-read authorization")
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
	if !hasSessionCookie(t.Cookies) {
		return fmt.Errorf("cookies must include signed-in ChatGPT session evidence")
	}
	if err := validatePrivateValue("cookie_header", t.CookieHeader, 256<<10); err != nil {
		return err
	}
	if !strings.Contains(t.CookieHeader, "__Secure-next-auth.session-token") &&
		!strings.Contains(t.CookieHeader, "oai-client-auth-info=") {
		return fmt.Errorf("cookie_header must preserve signed-in ChatGPT session evidence")
	}
	if strings.TrimSpace(t.BrowserUserAgent) == "" || len(t.BrowserUserAgent) > 4096 {
		return fmt.Errorf("browser_user_agent is required and bounded")
	}
	if _, err := time.Parse(time.RFC3339Nano, t.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if t.Source != "headed-cdp-observed-read-request" &&
		t.Source != "headed-cdp-retained-read-shape" {
		return fmt.Errorf("source is not an accepted ChatGPT request observation")
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
	if len(r.ProductModes) > 8 ||
		len(r.IntelligenceOptions) > 16 ||
		len(r.ModelOptions) > 32 ||
		len(r.Tools) > 32 {
		return fmt.Errorf("runtime capability evidence exceeds bounds")
	}
	for _, values := range [][]string{
		r.ProductModes,
		r.IntelligenceOptions,
		r.ModelOptions,
		r.Tools,
	} {
		for _, value := range values {
			if err := validatePublicLabel(value); err != nil {
				return err
			}
		}
	}
	for _, value := range []string{
		r.SelectedProduct,
		r.SelectedIntelligence,
		r.SelectedModel,
	} {
		if value != "" {
			if err := validatePublicLabel(value); err != nil {
				return err
			}
		}
	}
	if r.ModelOptionsObserved &&
		(len(r.ModelOptions) == 0 ||
			strings.TrimSpace(r.SelectedModel) == "" ||
			!containsString(r.ModelOptions, r.SelectedModel)) {
		return fmt.Errorf(
			"observed model capability requires a selected visible model option",
		)
	}
	if r.Source != "headed-cdp-sanitized-composer-probe" {
		return fmt.Errorf("source is not an accepted ChatGPT capability observation")
	}
	return nil
}

func isReadAuthPath(path string) bool {
	return path == ConversationListPath || path == RoomsSummaryPath
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

func validatePublicLabel(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("runtime capability label is invalid")
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
