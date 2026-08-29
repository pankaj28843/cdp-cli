package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestPageConditionWaitsSelectPageByTargetIndex(t *testing.T) {
	tests := []struct {
		name       string
		targetID   string
		args       []string
		waitKind   string
		checkExtra func(t *testing.T, report map[string]any)
	}{
		{
			name:     "text",
			targetID: "page-two",
			args:     []string{"wait", "text", "Ready"},
			waitKind: "text",
			checkExtra: func(t *testing.T, report map[string]any) {
				wait := report["wait"].(map[string]any)
				if wait["needle"] != "Ready" || wait["count"] != float64(1) {
					t.Fatalf("text wait = %#v, want preserved needle/count", wait)
				}
			},
		},
		{
			name:     "selector",
			targetID: "page-two",
			args:     []string{"wait", "selector", "main"},
			waitKind: "selector",
			checkExtra: func(t *testing.T, report map[string]any) {
				wait := report["wait"].(map[string]any)
				if wait["selector"] != "main" || wait["count"] != float64(1) {
					t.Fatalf("selector wait = %#v, want preserved selector/count", wait)
				}
			},
		},
		{
			name:     "url",
			targetID: "page-two",
			args:     []string{"wait", "url", "https://example.test/app", "--mode", "exact"},
			waitKind: "url",
			checkExtra: func(t *testing.T, report map[string]any) {
				wait := report["wait"].(map[string]any)
				if wait["condition"] != "exact" || wait["url"] != "https://example.test/app" {
					t.Fatalf("url wait = %#v, want preserved exact URL evidence", wait)
				}
			},
		},
		{
			name:     "locator",
			targetID: "page-two",
			args:     []string{"wait", "locator", "Search", "--by", "text", "--strict"},
			waitKind: "locator",
			checkExtra: func(t *testing.T, report map[string]any) {
				locator, ok := report["locator"].(map[string]any)
				matches, matchesOK := report["matches"].([]any)
				if !ok || locator["query"] != "Search" || !matchesOK || len(matches) != 1 {
					t.Fatalf("locator report = %#v, want preserved strict locator evidence", report)
				}
			},
		},
		{
			name:     "eval",
			targetID: "page-two",
			args:     []string{"wait", "eval", "window.__rendered === true"},
			waitKind: "eval",
			checkExtra: func(t *testing.T, report map[string]any) {
				wait := report["wait"].(map[string]any)
				if wait["expression"] != "window.__rendered === true" || wait["value"] != true {
					t.Fatalf("eval wait = %#v, want preserved expression/value", wait)
				}
			},
		},
		{
			name:     "load-state",
			targetID: "page-two",
			args:     []string{"wait", "load-state", "load"},
			waitKind: "load-state",
			checkExtra: func(t *testing.T, report map[string]any) {
				wait := report["wait"].(map[string]any)
				if wait["state"] != "load" || wait["ready_state"] != "complete" {
					t.Fatalf("load-state wait = %#v, want preserved readiness evidence", wait)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": test.targetID, "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{}, test.args...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s target-index exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			var report map[string]any
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("%s target-index output is invalid JSON: %v; output=%s", test.name, err, out.String())
			}
			if report["ok"] != true {
				t.Fatalf("%s report = %#v, want ok", test.name, report)
			}
			target, ok := report["target"].(map[string]any)
			if !ok || target["id"] != test.targetID {
				t.Fatalf("%s target = %#v, want %s", test.name, report["target"], test.targetID)
			}
			if got, ok := report["target_index"].(float64); !ok || got != 2 {
				t.Fatalf("%s target_index = %#v, want 2", test.name, report["target_index"])
			}
			wait, ok := report["wait"].(map[string]any)
			if !ok || wait["kind"] != test.waitKind || wait["matched"] != true {
				t.Fatalf("%s wait = %#v, want matched %s", test.name, report["wait"], test.waitKind)
			}
			if test.checkExtra != nil {
				test.checkExtra(t, report)
			}
		})
	}
}

func TestPageConditionWaitsRejectInvalidTargetIndex(t *testing.T) {
	commands := [][]string{
		{"wait", "text", "Ready"},
		{"wait", "selector", "main"},
		{"wait", "url", "https://example.test/app"},
		{"wait", "locator", "Search"},
		{"wait", "eval", "window.__rendered === true"},
		{"wait", "load-state", "load"},
	}
	for _, command := range commands {
		name := strings.Join(command[1:], "-")
		for _, value := range []string{"0", "-1"} {
			t.Run(fmt.Sprintf("%s/%s", name, value), func(t *testing.T) {
				args := append(append([]string{}, command...), "--target-index", value, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s target-index %s exit=%d stdout=%s stderr=%s", name, value, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
			})
		}

		t.Run(name+"/conflict", func(t *testing.T) {
			args := append(append([]string{}, command...), "--target-index", "1", "--url-contains", "example.test", "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s target conflict exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
		})
	}
}

func TestPageConditionWaitsReportOutOfRangeTargetIndex(t *testing.T) {
	commands := [][]string{
		{"wait", "text", "Ready"},
		{"wait", "selector", "main"},
		{"wait", "url", "https://example.test/app"},
		{"wait", "locator", "Search"},
		{"wait", "eval", "window.__rendered === true"},
		{"wait", "load-state", "load"},
	}
	for _, command := range commands {
		name := strings.Join(command[1:], "-")
		t.Run(name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "only-page", "type": "page", "title": "Only page", "url": "https://example.test/only",
			}})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")
			args := append(append([]string{}, command...), "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s out-of-range exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}
