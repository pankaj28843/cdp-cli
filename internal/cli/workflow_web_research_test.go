package cli

import (
	"net/url"
	"testing"
)

func TestWebResearchSearchURLByEngine(t *testing.T) {
	tests := []struct {
		engine        string
		wantHost      string
		wantPageKey   string
		wantPageValue string
	}{
		{engine: "google", wantHost: "www.google.com", wantPageKey: "start", wantPageValue: "10"},
		{engine: "bing", wantHost: "www.bing.com", wantPageKey: "first", wantPageValue: "11"},
		{engine: "brave", wantHost: "search.brave.com", wantPageKey: "offset", wantPageValue: "10"},
		{engine: "duckduckgo", wantHost: "duckduckgo.com", wantPageKey: "s", wantPageValue: "10"},
		{engine: "kagi", wantHost: "kagi.com", wantPageKey: "page", wantPageValue: "2"},
	}
	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			rawURL := webResearchSearchURL(tt.engine, "agentic engineering", "qdr:m", 2)
			parsed, err := url.Parse(rawURL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			if parsed.Hostname() != tt.wantHost {
				t.Fatalf("host = %q, want %q", parsed.Hostname(), tt.wantHost)
			}
			if got := parsed.Query().Get("q"); got != "agentic engineering" {
				t.Fatalf("q = %q", got)
			}
			if got := parsed.Query().Get(tt.wantPageKey); got != tt.wantPageValue {
				t.Fatalf("%s = %q, want %q in %s", tt.wantPageKey, got, tt.wantPageValue, rawURL)
			}
			if tt.engine == "google" && parsed.Query().Get("tbs") != "qdr:m" {
				t.Fatalf("google tbs was not preserved: %s", rawURL)
			}
		})
	}
}

func TestWebResearchSupportedSERPSet(t *testing.T) {
	for _, engine := range []string{"google", "bing", "brave", "duckduckgo", "kagi"} {
		if !isWebResearchSupportedSERP(engine) {
			t.Fatalf("engine %q should be supported", engine)
		}
	}
	if isWebResearchSupportedSERP("yahoo") {
		t.Fatalf("unexpected supported engine")
	}
}
