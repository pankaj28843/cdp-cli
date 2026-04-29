package browser_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browser"
)

func TestProbeAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("path = %q, want /json/version", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"Browser":"Chrome/144.0","Protocol-Version":"1.3","webSocketDebuggerUrl":"ws://example/devtools/browser/id"}`))
	}))
	defer server.Close()

	got, err := browser.Probe(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if got.State != "cdp_available" || !got.WebSocketDebuggerURL || got.Browser == "" {
		t.Fatalf("Probe() = %+v, want available browser metadata", got)
	}
}

func TestProbeListeningNotCDP(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	got, err := browser.Probe(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if got.State != "listening_not_cdp" || got.HTTPStatus != http.StatusNotFound {
		t.Fatalf("Probe() = %+v, want listening_not_cdp 404", got)
	}
}

func TestProbeNotConfigured(t *testing.T) {
	got, err := browser.Probe(context.Background(), "")
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if got.State != "not_configured" {
		t.Fatalf("Probe() state = %q, want not_configured", got.State)
	}
}
