package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTripOwnerOnlyAndStatus(t *testing.T) {
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	now := time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	template := validAuthTemplate(now)
	if err := store.Save(context.Background(), template); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.OrganizationID != template.OrganizationID ||
		loaded.Cookies["sessionKey"] != template.Cookies["sessionKey"] ||
		loaded.Headers["accept"] != "application/json" {
		t.Fatalf("loaded template = %+v", loaded)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("stat template: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("template mode = %o, want 600", got)
	}
	if got := filepath.ToSlash(strings.TrimPrefix(store.Path(), stateDir+string(os.PathSeparator))); got != RelativeTemplatePath {
		t.Fatalf("relative template path = %q", got)
	}

	ready := store.Status(context.Background(), now.Add(30*time.Minute), time.Hour)
	if !ready.Ready || ready.State != "ready" || ready.Stale || ready.TemplatePath != RelativeTemplatePath {
		t.Fatalf("ready status = %+v", ready)
	}
	expired := store.Status(context.Background(), now.Add(2*time.Hour), time.Hour)
	if expired.Ready || expired.State != "expired" || !expired.Stale {
		t.Fatalf("expired status = %+v", expired)
	}
}

func TestStoreStatusMissingInvalidAndSymlinkAreSafe(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	missing := store.Status(context.Background(), time.Now(), time.Hour)
	if missing.State != "missing" || missing.Ready || strings.Contains(missing.Reason, store.Path()) {
		t.Fatalf("missing status = %+v", missing)
	}

	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write invalid template: %v", err)
	}
	invalid := store.Status(context.Background(), time.Now(), time.Hour)
	if invalid.State != "invalid" || invalid.Ready || strings.Contains(invalid.Reason, "{not-json") {
		t.Fatalf("invalid status = %+v", invalid)
	}

	if err := os.Remove(store.Path()); err != nil {
		t.Fatalf("remove invalid template: %v", err)
	}
	target := filepath.Join(filepath.Dir(store.Path()), "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Symlink(target, store.Path()); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	symlink := store.Status(context.Background(), time.Now(), time.Hour)
	if symlink.State != "invalid" || symlink.Ready || !strings.Contains(symlink.Reason, "owner-only") {
		t.Fatalf("symlink status = %+v", symlink)
	}
}

func TestAuthTemplateValidationAndHeaderNormalization(t *testing.T) {
	now := time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	template := validAuthTemplate(now)
	if err := template.Validate(); err != nil {
		t.Fatalf("valid template: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*AuthTemplate)
	}{
		{name: "wrong host", mutate: func(t *AuthTemplate) {
			t.ListURL = "https://example.test/api/organizations/org-1/chat_conversations_v2?starred=false"
		}},
		{name: "organization mismatch", mutate: func(t *AuthTemplate) { t.OrganizationID = "org-2" }},
		{name: "missing starred false", mutate: func(t *AuthTemplate) {
			t.ListURL = Origin + "/api/organizations/org-1/chat_conversations_v2?starred=true"
		}},
		{name: "missing session", mutate: func(t *AuthTemplate) { t.Cookies = map[string]string{"other": "value"} }},
		{name: "forbidden header", mutate: func(t *AuthTemplate) { t.Headers["authorization"] = "secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := validAuthTemplate(now)
			test.mutate(&got)
			if err := got.Validate(); err == nil {
				t.Fatal("Validate succeeded, want failure")
			}
		})
	}

	headers := NormalizeReplayHeaders(map[string]any{
		"Accept":         "application/json",
		"User-Agent":     "Browser/Test",
		"Cookie":         "secret-cookie",
		"Authorization":  "secret-token",
		"Content-Length": "123",
		"X-Control":      "bad\nvalue",
		"X-Non-String":   42,
		":Authority":     "claude.ai",
	})
	encoded, err := json.Marshal(headers)
	if err != nil {
		t.Fatalf("marshal normalized headers: %v", err)
	}
	if len(headers) != 2 ||
		headers["accept"] != "application/json" ||
		headers["user-agent"] != "Browser/Test" ||
		strings.Contains(string(encoded), "secret") {
		t.Fatalf("normalized headers = %s", encoded)
	}
}

func validAuthTemplate(capturedAt time.Time) AuthTemplate {
	return AuthTemplate{
		SchemaVersion:    AuthTemplateSchemaVersion,
		Method:           "GET",
		Origin:           Origin,
		OrganizationID:   "org-1",
		ListURL:          Origin + "/api/organizations/org-1/chat_conversations_v2?limit=30&starred=false&consistency=eventual",
		Headers:          map[string]string{"accept": "application/json"},
		Cookies:          map[string]string{"sessionKey": "private-session-cookie"},
		BrowserUserAgent: "Browser/Test",
		CapturedAt:       capturedAt.UTC().Format(time.RFC3339Nano),
		Source:           "headed-cdp-observed-list-request",
	}
}
