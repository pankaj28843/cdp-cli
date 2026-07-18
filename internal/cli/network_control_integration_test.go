package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestNetworkBlockJSONIncludesCleanup(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"network", "block", "--pattern", "*://*/analytics/*", "--duration", "50ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network block exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		OK           bool `json:"ok"`
		MatchedCount int  `json:"matched_count"`
		Rules        []struct {
			URLPattern string `json:"url_pattern"`
		} `json:"rules"`
		Cleanup struct {
			Attempted          bool `json:"attempted"`
			BlockedURLsCleared bool `json:"blocked_urls_cleared"`
			NetworkDisabled    bool `json:"network_disabled"`
			Complete           bool `json:"complete"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.MatchedCount != 1 || len(got.Rules) != 1 || got.Rules[0].URLPattern != "*://*/analytics/*" || !got.Cleanup.Attempted || !got.Cleanup.BlockedURLsCleared || !got.Cleanup.NetworkDisabled || !got.Cleanup.Complete {
		t.Fatalf("network block = %+v", got)
	}
}

func TestNetworkMockJSONResolvesEveryPausedRequest(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	const rule = `{"url_pattern":"*://*/api/config","method":"GET","status":200,"headers":{"Content-Type":"application/json"},"body":"{\"enabled\":true}","max_matches":1}`
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"network", "mock", "--rule", rule, "--duration", "50ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network mock exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		OK           bool `json:"ok"`
		MatchedCount int  `json:"matched_count"`
		Rules        []struct {
			MatchedCount int `json:"matched_count"`
			BodyBytes    int `json:"body_bytes"`
		} `json:"rules"`
		Actions map[string]int `json:"actions"`
		Cleanup struct {
			Attempted     bool `json:"attempted"`
			FetchDisabled bool `json:"fetch_disabled"`
			Complete      bool `json:"complete"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.MatchedCount != 1 || got.Actions["fulfilled"] != 1 || got.Actions["continued"] != 1 || len(got.Rules) != 1 || got.Rules[0].MatchedCount != 1 || got.Rules[0].BodyBytes == 0 || !got.Cleanup.Attempted || !got.Cleanup.FetchDisabled || !got.Cleanup.Complete {
		t.Fatalf("network mock = %+v", got)
	}
	if strings.Contains(out.String(), `"enabled":true`) {
		t.Fatalf("network mock JSON leaked mock response body: %s", out.String())
	}
}
