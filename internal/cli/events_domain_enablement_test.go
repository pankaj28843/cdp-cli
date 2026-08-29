package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestEventsStreamAcceptsGenericTargetDomain(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "generic-domain-page",
		"type":     "page",
		"title":    "Generic domain",
		"url":      "https://example.test/generic-domain",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "stream", "--target", "generic-domain-page",
		"--enable", "DOM", "--match", "DOM.documentUpdated",
		"--duration", "10ms", "--max-events", "0", "--json",
	}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("generic-domain stream exit=%d, stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	records := decodeJSONLines(t, out.String())
	if len(records) != 2 || records[0]["type"] != "ready" || records[1]["type"] != "stopped" {
		t.Fatalf("generic-domain records = %v, want ready/stopped", recordTypes(records))
	}
	stream, ok := records[0]["stream"].(map[string]any)
	if !ok {
		t.Fatalf("generic-domain ready stream = %#v", records[0]["stream"])
	}
	domains, ok := stream["enabled_domains"].([]any)
	if !ok || len(domains) != 1 || domains[0] != "DOM" {
		t.Fatalf("generic-domain enabled domains = %#v, want [DOM]", stream["enabled_domains"])
	}
}

func TestEventsStreamDynamicallyEnablesGenericTargetDomain(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "dynamic-domain-page",
		"type":     "page",
		"title":    "Dynamic domain",
		"url":      "https://example.test/dynamic-domain",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "stream", "--target", "dynamic-domain-page",
		"--enable", "page", "--match", "Never.emitted",
		"--duration", "100ms", "--max-events", "0", "--json",
	}, strings.NewReader("+DOM.documentUpdated\n"), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("dynamic generic-domain stream exit=%d, stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	records := decodeJSONLines(t, out.String())
	found := false
	for _, record := range records {
		if record["type"] == "subscription" && record["operation"] == "add" &&
			record["method"] == "DOM.documentUpdated" && record["active"] == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("dynamic generic-domain records = %s, want active subscription", out.String())
	}
	if strings.Contains(out.String(), "subscription_enable_failed") {
		t.Fatalf("dynamic generic-domain enable failed: %s", out.String())
	}
}

func TestEventsTapAcceptsGenericTargetDomain(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "generic-tap-page",
		"type":     "page",
		"title":    "Generic tap domain",
		"url":      "https://example.test/generic-tap-domain",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"events", "tap", "--target", "generic-tap-page",
		"--enable", "DOM", "--match", "DOM.documentUpdated",
		"--duration", "10ms", "--max-events", "0", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("generic-domain tap exit=%d, stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result struct {
		OK     bool  `json:"ok"`
		Events []any `json:"events"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode generic-domain tap: %v", err)
	}
	if !result.OK {
		t.Fatalf("generic-domain tap = %#v, want successful bounded result", result)
	}
}

func TestEventsStreamReportsUnsupportedGenericDomain(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "unsupported-domain-page",
		"type":     "page",
		"title":    "Unsupported domain",
		"url":      "https://example.test/unsupported-domain",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "stream", "--target", "unsupported-domain-page",
		"--enable", "ExperimentalUnknown", "--duration", "10ms", "--json",
	}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitConnection {
		t.Fatalf("unsupported generic-domain stream exit=%d, stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Class   string `json:"err_class"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode unsupported generic-domain error: %v; output=%s", err, out.String())
	}
	if result.OK || result.Code != "collector_enable_failed" || result.Class != "connection" || !strings.Contains(result.Message, "ExperimentalUnknown") {
		t.Fatalf("unsupported generic-domain error = %#v, want typed collector_enable_failed envelope", result)
	}
	if strings.Contains(out.String(), `"type":"ready"`) {
		t.Fatalf("unsupported generic-domain output claimed readiness: %s", out.String())
	}
}
