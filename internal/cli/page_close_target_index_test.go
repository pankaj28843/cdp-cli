package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestPageCloseSelectsPageByTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"page", "close", "--target-index", "2", "--state-dir", stateDir, "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page close target-index exit=%d, stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result struct {
		OK         bool `json:"ok"`
		TargetGone bool `json:"target_gone"`
		Target     struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode page close target-index output: %v; output=%s", err, out.String())
	}
	if !result.OK || !result.TargetGone || result.Target.ID != "page-two" {
		t.Fatalf("page close target-index result = %+v, want settled page-two close", result)
	}
}

func TestPageCloseRejectsInvalidTargetIndexBeforeClose(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "invalid-index-page", "type": "page", "title": "Invalid index", "url": "https://example.test/invalid-index", "fakeCloseTargetError": true},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{
				"page", "close", "--target-index", value, "--state-dir", stateDir, "--json",
			}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("target-index %s exit=%d, stdout=%s stderr=%s", value, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode target-index %s error: %v; output=%s", value, err, out.String())
			}
			if result["ok"] != false || result["code"] != "invalid_target_index" {
				t.Fatalf("target-index %s error = %#v, want invalid_target_index and no close", value, result)
			}
		})
	}
}

func TestPageCloseRejectsTargetIndexSelectorConflicts(t *testing.T) {
	tests := []struct {
		name  string
		extra []string
	}{
		{name: "target", extra: []string{"--target", "page-one"}},
		{name: "url", extra: []string{"--url-contains", "example.test"}},
		{name: "title", extra: []string{"--title-contains", "First"}},
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
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := []string{"page", "close", "--target-index", "1"}
			args = append(args, test.extra...)
			args = append(args, "--state-dir", stateDir, "--json")
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

func TestPageCloseReportsOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "only-page",
		"type":     "page",
		"title":    "Only page",
		"url":      "https://example.test/only",
	}})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"page", "close", "--target-index", "2", "--state-dir", stateDir, "--json",
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
