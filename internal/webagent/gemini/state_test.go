package gemini

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnerOnlyDictationTemplateRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	template := testGeminiTemplate(time.Now().UTC())
	if err := store.SaveTemplate(context.Background(), template); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadTemplate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIKey != template.APIKey || loaded.BrowserUserAgent != template.BrowserUserAgent {
		t.Fatalf("loaded template did not preserve observed replay fields")
	}
	info, err := os.Stat(filepath.Join(stateDir, filepath.FromSlash(RelativeTemplatePath)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("template permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestRetainedDictationTemplateRotatesBrowserCredentialsOnly(t *testing.T) {
	old := testGeminiTemplate(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	capturedAt := time.Date(2026, 8, 28, 12, 15, 0, 0, time.UTC)
	cookies := map[string]string{
		"SAPISID": "new-sapisid", "__Secure-1PAPISID": "new-1p", "__Secure-3PAPISID": "new-3p",
		"SID": "full-browser-cookie",
	}
	got, ok := retainedDictationTemplate(&old, cookies, "Browser/New", "new-api-key", capturedAt)
	if !ok {
		t.Fatal("valid browser credentials did not refresh the retained template")
	}
	if got.APIKey != "new-api-key" {
		t.Fatal("auth refresh did not rotate the labeled dictation API key")
	}
	if got.Cookies["SAPISID"] != "new-sapisid" || got.Cookies["SID"] != "full-browser-cookie" || got.BrowserUserAgent != "Browser/New" || got.Source != "headed-cdp-retained-dictation-template" {
		t.Fatalf("refreshed template = %+v", got)
	}
	if got.CapturedAt != capturedAt.Format(time.RFC3339Nano) {
		t.Fatalf("captured_at = %q", got.CapturedAt)
	}

	delete(cookies, "SAPISID")
	if _, ok := retainedDictationTemplate(&old, cookies, "Browser/New", "new-api-key", capturedAt); ok {
		t.Fatal("refresh accepted incomplete SAPISID credential set")
	}
	if created, ok := retainedDictationTemplate(nil, map[string]string{
		"SAPISID": "new-sapisid", "__Secure-1PAPISID": "new-1p", "__Secure-3PAPISID": "new-3p",
	}, "Browser/New", "first-api-key", capturedAt); !ok || created.Source != "headed-cdp-observed-dictation-template" {
		t.Fatalf("first refresh did not self-seed: %+v, %v", created, ok)
	}
}

func TestOwnerOnlyStateRoundTripAfterLiveContract(t *testing.T) {
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	now := time.Date(2026, 7, 25, 20, 0, 0, 0, time.UTC)
	if err := store.SaveAuth(context.Background(), AuthState{
		SchemaVersion:         AuthStateSchemaVersion,
		CapturedAt:            now.Format(time.RFC3339Nano),
		SignedIn:              true,
		SessionCookieObserved: true,
		Source:                "headed-cdp-safe-auth-evidence",
	}); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}
	if err := store.SaveRuntime(context.Background(), RuntimeCapabilities{
		SchemaVersion:         RuntimeCapabilitiesSchemaVersion,
		CapturedAt:            now.Format(time.RFC3339Nano),
		CurrentMode:           "Flash",
		ModeOptions:           []string{"Flash", "Pro"},
		FileUploadControl:     "observed",
		FileUploadAction:      "unsupported",
		DeepResearchSelected:  false,
		ExplicitModeSelection: "request_shape_unobserved",
		Source:                "headed-cdp-rendered-controls",
	}); err != nil {
		t.Fatalf("SaveRuntime: %v", err)
	}

	if status := store.AuthStatus(
		context.Background(),
		now.Add(time.Minute),
		DefaultAuthTTL,
	); !status.Ready || status.State != "ready" {
		t.Fatalf("AuthStatus = %+v", status)
	}
	runtime := store.RuntimeStatus(
		context.Background(),
		now.Add(time.Minute),
		DefaultCapabilitiesTTL,
	)
	if !runtime.Ready ||
		runtime.CurrentMode != "Flash" ||
		len(runtime.ModeOptions) != 2 {
		t.Fatalf("RuntimeStatus = %+v", runtime)
	}

	for _, relative := range []string{
		RelativeAuthStatePath,
		RelativeCapabilitiesPath,
	} {
		path := filepath.Join(stateDir, filepath.FromSlash(relative))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", relative, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s permissions = %o", relative, info.Mode().Perm())
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		for _, forbidden := range []string{
			"cookie_value",
			"authorization",
			"csrf",
			"prompt",
			"answer",
		} {
			if strings.Contains(strings.ToLower(string(raw)), forbidden) {
				t.Fatalf("%s contains forbidden field %q", relative, forbidden)
			}
		}
	}
}

func TestExactCapturedPromptFingerprintRequiresStrictCopyEvidence(t *testing.T) {
	prompt := "Review this design.\n\tpreserve := \"two  spaces\""
	want := fingerprintPrompt(prompt)
	observation := promptCaptureObservation{
		Prompt:               prompt,
		QueryCount:           1,
		CopyButtonCount:      1,
		ClipboardIntercepted: true,
		Captured:             true,
	}
	if got := exactCapturedPromptFingerprint(&observation); got != want {
		t.Fatalf("exact captured prompt fingerprint = %q, want %q", got, want)
	}

	observation.Prompt = strings.Replace(prompt, "two  spaces", "two spaces", 1)
	if got := exactCapturedPromptFingerprint(&observation); got == want {
		t.Fatal("interior whitespace mutation retained exact prompt identity")
	}

	observation.Prompt = "Review this design.  \nnext line"
	trailingBeforeNewline := exactCapturedPromptFingerprint(&observation)
	observation.Prompt = "Review this design.\nnext line"
	if got := exactCapturedPromptFingerprint(&observation); got == trailingBeforeNewline {
		t.Fatal("whitespace before an interior newline lost exact prompt identity")
	}

	observation.Prompt = prompt
	observation.CopyButtonCount = 2
	if got := exactCapturedPromptFingerprint(&observation); got != "" {
		t.Fatalf("ambiguous Copy prompt controls produced fingerprint %q", got)
	}
}

func TestConversationListSignatureDeduplicatesOnlySafeIDs(t *testing.T) {
	signature, count := conversationListSignature([]renderedConversation{
		{ID: "1234567890abcdef"},
		{ID: "1234567890abcdef"},
		{ID: "fedcba0987654321"},
		{ID: "unsafe"},
	})
	if count != 2 ||
		signature != "1234567890abcdef\nfedcba0987654321" {
		t.Fatalf("signature = %q, count = %d", signature, count)
	}
}

func TestConversationListAtEndRequiresReadyBottom(t *testing.T) {
	for name, test := range map[string]struct {
		scroller listScrollerObservation
		want     bool
	}{
		"not ready": {
			scroller: listScrollerObservation{
				ScrollTop:    900,
				ScrollHeight: 1000,
				ClientHeight: 100,
			},
		},
		"not at bottom": {
			scroller: listScrollerObservation{
				Ready:        true,
				ScrollTop:    800,
				ScrollHeight: 1000,
				ClientHeight: 100,
			},
		},
		"at bottom": {
			scroller: listScrollerObservation{
				Ready:        true,
				ScrollTop:    900,
				ScrollHeight: 1000,
				ClientHeight: 100,
			},
			want: true,
		},
		"within layout tolerance": {
			scroller: listScrollerObservation{
				Ready:        true,
				ScrollTop:    898.5,
				ScrollHeight: 1000,
				ClientHeight: 100,
			},
			want: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := conversationListAtEnd(test.scroller); got != test.want {
				t.Fatalf("conversationListAtEnd() = %v, want %v", got, test.want)
			}
		})
	}
}
