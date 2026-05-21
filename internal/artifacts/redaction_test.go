package artifacts

import (
	"strings"
	"testing"
)

func TestRedactorHeadersURLsAndBodies(t *testing.T) {
	redactor := NewRedactor(ModeSafe)

	headers := redactor.HeaderMap(map[string]any{
		"Authorization": "Bearer secret-token",
		"X-Trace-ID":    "trace-1",
		"X-Api-Key":     "abc123",
	}, "request_headers")
	if headers["Authorization"] != Redacted || headers["X-Api-Key"] != Redacted || headers["X-Trace-ID"] != "trace-1" {
		t.Fatalf("headers = %+v, want sensitive fields redacted and trace preserved", headers)
	}

	gotURL := redactor.URL("https://example.test/app?token=secret&client_id=public", "url")
	if strings.Contains(gotURL, "secret") || !strings.Contains(gotURL, "token=%3Credacted%3E") || !strings.Contains(gotURL, "client_id=public") {
		t.Fatalf("redacted URL = %q, want sensitive query value redacted", gotURL)
	}

	gotJSON := redactor.BodyText(`{"ok":true,"csrf":"secret","nested":{"sessionId":"abc","path":"file:///workspace/private-project"}}`, "body.text")
	if strings.Contains(gotJSON, "secret") || strings.Contains(gotJSON, "file:///workspace") || !strings.Contains(gotJSON, `"ok":true`) {
		t.Fatalf("redacted JSON = %q, want secrets and local path redacted", gotJSON)
	}

	gotForm := redactor.BodyText("name=public&password=secret", "request_post_data.text")
	if strings.Contains(gotForm, "secret") || !strings.Contains(gotForm, "password=%3Credacted%3E") {
		t.Fatalf("redacted form body = %q, want password redacted", gotForm)
	}

	gotText := redactor.BodyText("Authorization: Bearer abc", "log")
	if gotText != Redacted {
		t.Fatalf("bearer text = %q, want full text redacted", gotText)
	}

	meta := redactor.Metadata(true, "")
	if meta.RedactionMode != ModeSafe || meta.Classification != "public_safe" || !meta.Shareable || meta.ChangedFieldCount == 0 {
		t.Fatalf("metadata = %+v, want public-safe metadata with changed fields", meta)
	}
}

func TestRedactorHeadersModeRedactsBodies(t *testing.T) {
	redactor := NewRedactor(ModeHeaders)
	if got := redactor.BodyText(`{"ok":true}`, "body.text"); got != Redacted {
		t.Fatalf("headers-mode body = %q, want redacted", got)
	}
	headers := redactor.HeaderMap(map[string]any{"Content-Type": "application/json"}, "headers")
	if headers["Content-Type"] != Redacted {
		t.Fatalf("headers-mode headers = %+v, want all headers redacted", headers)
	}
}

func TestRedactorNoneMarksArtifactUnsafeOptIn(t *testing.T) {
	redactor := NewRedactor(ModeNone)
	headers := redactor.HeaderMap(map[string]any{"Cookie": "session=secret"}, "headers")
	if headers["Cookie"] != "session=secret" {
		t.Fatalf("none headers = %+v, want unchanged local forensic output", headers)
	}
	meta := redactor.Metadata(true, "keep this artifact local")
	if meta.RedactionMode != ModeNone || meta.Classification != "unsafe_opt_in" || !meta.UnsafeOptIn || meta.Shareable || meta.LocalOnlyWarning == "" {
		t.Fatalf("metadata = %+v, want unsafe opt-in local artifact metadata", meta)
	}
}

func TestScanBytesFindsNeedlesAndReportsTruncation(t *testing.T) {
	result := ScanBytes([]byte("Bearer abc\nsession=secret\nlater-token"), []string{"bearer", "later-token"}, 20)
	if !result.Truncated || result.BytesScanned != 20 {
		t.Fatalf("scan = %+v, want truncated 20-byte scan", result)
	}
	if len(result.Findings) != 1 || result.Findings[0] != "bearer" {
		t.Fatalf("findings = %+v, want only in-window finding", result.Findings)
	}
}
