package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestProtocolExecSelectsPageByTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"protocol", "exec", "Runtime.evaluate",
		"--target-index", "2",
		"--params", "{\"expression\":\"document.title\",\"returnByValue\":true}",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol exec target-index exit=%d, stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode protocol exec target-index output: %v; output=%s", err, out.String())
	}
	target, ok := result["target"].(map[string]any)
	if !ok {
		t.Fatalf("protocol exec target-index result has no target: %#v", result)
	}
	if result["scope"] != "target" || target["id"] != "page-two" || result["session_id"] != "session-page-two" {
		t.Fatalf("protocol exec target-index result = %#v, want page-two target session", result)
	}
}

func TestProtocolExecRejectsInvalidTargetIndexBeforeAttachment(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "index-validation-page",
				"type":     "page",
				"title":    "Index validation",
				"url":      "https://example.test/index-validation",
			}})
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{
				"protocol", "exec", "Runtime.evaluate",
				"--target-index", value, "--json",
			}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("target-index %s exit=%d, stdout=%s stderr=%s", value, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode target-index %s error: %v; output=%s", value, err, out.String())
			}
			if result["ok"] != false || result["code"] != "invalid_target_index" {
				t.Fatalf("target-index %s error = %#v, want invalid_target_index", value, result)
			}
		})
	}
}

func TestProtocolExecRejectsTargetIndexSelectorConflicts(t *testing.T) {
	tests := []struct {
		name  string
		extra []string
	}{
		{name: "target", extra: []string{"--target", "page-one"}},
		{name: "url", extra: []string{"--url-contains", "example.test"}},
		{name: "title", extra: []string{"--title-contains", "First"}},
		{name: "target type", extra: []string{"--target-type", "page"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "page-one",
				"type":     "page",
				"title":    "First",
				"url":      "https://example.test/first",
			}})
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			args := []string{"protocol", "exec", "Runtime.evaluate", "--target-index", "1"}
			args = append(args, test.extra...)
			args = append(args, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s conflict exit=%d, stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s conflict error: %v; output=%s", test.name, err, out.String())
			}
			message, _ := result["message"].(string)
			if result["ok"] != false || result["code"] != "invalid_target_selector" || !strings.Contains(message, "--target-index") {
				t.Fatalf("%s conflict error = %#v, want invalid_target_selector", test.name, result)
			}
		})
	}
}

func TestProtocolExecReportsOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "only-page",
		"type":     "page",
		"title":    "Only page",
		"url":      "https://example.test/only",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"protocol", "exec", "Runtime.evaluate", "--target-index", "2", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("out-of-range target-index exit=%d, stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode out-of-range target-index error: %v; output=%s", err, out.String())
	}
	message, _ := result["message"].(string)
	if result["ok"] != false || result["code"] != "target_not_found" || !strings.Contains(message, "page target index 2") {
		t.Fatalf("out-of-range target-index error = %#v, want target_not_found", result)
	}
}
