package claude

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
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

const (
	AuthTemplateSchemaVersion = "claude-auth-template/v1"
	AuthStatusSchemaVersion   = "claude-auth-status/v1"
	Origin                    = "https://claude.ai"
	HomeURL                   = Origin + "/new"
	RelativeTemplatePath      = "webagent/claude/request-template.json"
	DefaultAuthTTL            = time.Hour
)

var organizationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

type AuthTemplate struct {
	SchemaVersion    string            `json:"schema_version"`
	Method           string            `json:"method"`
	Origin           string            `json:"origin"`
	OrganizationID   string            `json:"organization_id"`
	ListURL          string            `json:"list_url"`
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
	TemplatePath  string `json:"template_path"`
	CapturedAt    string `json:"captured_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type Store struct {
	path string
}

func NewStore(stateDir string) (*Store, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("Claude state directory is required")
	}
	return &Store{path: filepath.Join(stateDir, filepath.FromSlash(RelativeTemplatePath))}, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) Save(ctx context.Context, template AuthTemplate) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return fmt.Errorf("Claude auth store is not configured")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("validate Claude auth template: %w", err)
	}
	data, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Claude auth template: %w", err)
	}
	data = append(data, '\n')
	return artifacts.WithOwnerOnlyFileLock(ctx, s.path+".lock", func() error {
		return artifacts.WriteOwnerOnlyFileAtomic(s.path, data)
	})
}

func (s *Store) Load(ctx context.Context) (AuthTemplate, error) {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return AuthTemplate{}, fmt.Errorf("Claude auth store is not configured")
	}
	select {
	case <-ctx.Done():
		return AuthTemplate{}, ctx.Err()
	default:
	}
	data, err := artifacts.ReadOwnerOnlyFile(s.path)
	if err != nil {
		return AuthTemplate{}, err
	}
	var template AuthTemplate
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&template); err != nil {
		return AuthTemplate{}, fmt.Errorf("parse Claude auth template: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return AuthTemplate{}, err
	}
	if err := template.Validate(); err != nil {
		return AuthTemplate{}, fmt.Errorf("validate Claude auth template: %w", err)
	}
	return template, nil
}

func (s *Store) Status(ctx context.Context, now time.Time, ttl time.Duration) AuthStatus {
	status := AuthStatus{
		SchemaVersion: AuthStatusSchemaVersion,
		State:         "missing",
		TemplatePath:  RelativeTemplatePath,
		Reason:        "auth template is not present",
	}
	if ttl <= 0 {
		ttl = DefaultAuthTTL
	}
	template, err := s.Load(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return status
		}
		status.State = "invalid"
		status.Reason = "auth template failed owner-only validation"
		return status
	}
	capturedAt, err := time.Parse(time.RFC3339Nano, template.CapturedAt)
	if err != nil {
		status.State = "invalid"
		status.Reason = "auth template capture time is invalid"
		return status
	}
	now = now.UTC()
	capturedAt = capturedAt.UTC()
	if capturedAt.After(now.Add(5 * time.Minute)) {
		status.State = "invalid"
		status.Reason = "auth template capture time is unexpectedly in the future"
		return status
	}
	expiresAt := capturedAt.Add(ttl)
	status.CapturedAt = capturedAt.Format(time.RFC3339Nano)
	status.ExpiresAt = expiresAt.Format(time.RFC3339Nano)
	status.Stale = !now.Before(expiresAt)
	if status.Stale {
		status.State = "expired"
		status.Reason = "auth template exceeded its freshness window"
		return status
	}
	status.State = "ready"
	status.Ready = true
	status.Reason = ""
	return status
}

func (t AuthTemplate) Validate() error {
	if t.SchemaVersion != AuthTemplateSchemaVersion {
		return fmt.Errorf("schema_version must be %q", AuthTemplateSchemaVersion)
	}
	if t.Method != "GET" || t.Origin != Origin {
		return fmt.Errorf("Claude auth template must use the observed HTTPS GET origin")
	}
	if !organizationPattern.MatchString(t.OrganizationID) {
		return fmt.Errorf("organization_id has an invalid shape")
	}
	parsed, err := url.Parse(t.ListURL)
	if err != nil {
		return fmt.Errorf("list_url is invalid")
	}
	expectedPath := "/api/organizations/" + t.OrganizationID + "/chat_conversations_v2"
	if parsed.Scheme != "https" ||
		parsed.Host != "claude.ai" ||
		parsed.User != nil ||
		parsed.Fragment != "" ||
		parsed.Path != expectedPath ||
		parsed.Query().Get("starred") != "false" {
		return fmt.Errorf("list_url does not match the observed Claude organization list endpoint")
	}
	if len(t.Headers) > 128 || len(t.Cookies) > 256 {
		return fmt.Errorf("Claude auth template exceeds bounded header or cookie counts")
	}
	for name, value := range t.Headers {
		if !validHeaderName(name) || forbiddenReplayHeader(name) {
			return fmt.Errorf("headers contain a forbidden name")
		}
		if err := validatePrivateValue("header", value, 16<<10); err != nil {
			return err
		}
	}
	sessionCookie := false
	for name, value := range t.Cookies {
		if err := validatePrivateValue("cookie name", name, 1024); err != nil {
			return err
		}
		if err := validatePrivateValue("cookie value", value, 64<<10); err != nil {
			return err
		}
		if (name == "sessionKey" || name == "sessionKeyLC") && value != "" {
			sessionCookie = true
		}
	}
	if !sessionCookie {
		return fmt.Errorf("Claude session cookie evidence is missing")
	}
	if err := validatePrivateValue("browser_user_agent", t.BrowserUserAgent, 4096); err != nil {
		return err
	}
	if strings.TrimSpace(t.BrowserUserAgent) == "" {
		return fmt.Errorf("browser_user_agent is required")
	}
	if _, err := time.Parse(time.RFC3339Nano, t.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if t.Source != "headed-cdp-observed-list-request" &&
		t.Source != "headed-cdp-retained-list-shape" {
		return fmt.Errorf("source is not an accepted observation source")
	}
	return nil
}

func NormalizeReplayHeaders(raw map[string]any) map[string]string {
	headers := make(map[string]string)
	for rawName, rawValue := range raw {
		value, ok := rawValue.(string)
		if !ok {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(rawName))
		if !validHeaderName(name) || forbiddenReplayHeader(name) {
			continue
		}
		if validatePrivateValue("header", value, 16<<10) != nil {
			continue
		}
		headers[name] = value
	}
	return headers
}

func forbiddenReplayHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie",
		"content-length", "host", "accept-encoding", "connection",
		"proxy-connection", "transfer-encoding", "upgrade":
		return true
	default:
		return strings.HasPrefix(name, ":")
	}
}

func validHeaderName(name string) bool {
	if name == "" || name != strings.ToLower(strings.TrimSpace(name)) {
		return false
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("!#$%&'*+-.^_`|~", r) {
			continue
		}
		return false
	}
	return true
}

func validatePrivateValue(name, value string, max int) error {
	if value == "" {
		return fmt.Errorf("%s is empty", name)
	}
	if len(value) > max {
		return fmt.Errorf("%s exceeds its private-state limit", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("parse trailing Claude auth template data: %w", err)
	}
	return fmt.Errorf("Claude auth template contains multiple JSON values")
}
