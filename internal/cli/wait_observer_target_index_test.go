package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWaitObserversSelectPageByTargetIndex(t *testing.T) {
	tests := []struct {
		name       string
		targetID   string
		args       func(downloadDir string) []string
		waitKind   string
		checkExtra func(t *testing.T, report map[string]any)
	}{
		{
			name:     "request",
			targetID: "page-two",
			args: func(string) []string {
				return []string{"wait", "request", "--match-url", "/api", "--method", "POST"}
			},
			waitKind: "request",
			checkExtra: func(t *testing.T, report map[string]any) {
				event, ok := report["event"].(map[string]any)
				if !ok || event["kind"] != "request" || event["request_id"] != "request-failed" {
					t.Fatalf("request event = %#v, want preserved request evidence", report["event"])
				}
			},
		},
		{
			name:     "response",
			targetID: "page-two",
			args: func(string) []string {
				return []string{"wait", "response", "--match-url", "/app", "--method", "GET", "--status", "200"}
			},
			waitKind: "response",
			checkExtra: func(t *testing.T, report map[string]any) {
				event, ok := report["event"].(map[string]any)
				if !ok || event["kind"] != "response" || event["status"] != float64(200) {
					t.Fatalf("response event = %#v, want preserved response evidence", report["event"])
				}
			},
		},
		{
			name:     "network-idle",
			targetID: "page-two",
			args: func(string) []string {
				return []string{"wait", "network-idle", "--idle", "10ms"}
			},
			waitKind: "network-idle",
			checkExtra: func(t *testing.T, report map[string]any) {
				wait := report["wait"].(map[string]any)
				if wait["request_count"] != float64(2) || wait["failed_count"] != float64(1) {
					t.Fatalf("network-idle wait = %#v, want preserved lifecycle counts", wait)
				}
			},
		},
		{
			name:     "dialog",
			targetID: "dialog-page-two",
			args: func(string) []string {
				return []string{"wait", "dialog", "--type", "confirm", "--message-contains", "Delete", "--action", "dismiss"}
			},
			waitKind: "dialog",
			checkExtra: func(t *testing.T, report map[string]any) {
				dialog, ok := report["dialog"].(map[string]any)
				if !ok || dialog["type"] != "confirm" || dialog["action"] != "dismiss" || dialog["handled"] != true {
					t.Fatalf("dialog = %#v, want preserved handled dialog evidence", report["dialog"])
				}
			},
		},
		{
			name:     "file-chooser",
			targetID: "file-chooser-page-two",
			args: func(string) []string {
				return []string{"wait", "file-chooser", "--mode", "single"}
			},
			waitKind: "file-chooser",
			checkExtra: func(t *testing.T, report map[string]any) {
				chooser, ok := report["file_chooser"].(map[string]any)
				if !ok || chooser["mode"] != "selectSingle" || chooser["multiple"] != false {
					t.Fatalf("file chooser = %#v, want preserved chooser evidence", report["file_chooser"])
				}
			},
		},
		{
			name:     "popup",
			targetID: "opener-page",
			args: func(string) []string {
				return []string{"wait", "popup", "--match-url", "/oauth/callback"}
			},
			waitKind: "popup",
			checkExtra: func(t *testing.T, report map[string]any) {
				opener, openerOK := report["opener"].(map[string]any)
				popup, popupOK := report["popup"].(map[string]any)
				if !openerOK || opener["id"] != "opener-page" || !popupOK {
					t.Fatalf("popup report = %#v, want opener and popup evidence", report)
				}
				target, targetOK := popup["target"].(map[string]any)
				if !targetOK || target["id"] != "popup-page" {
					t.Fatalf("popup target = %#v, want popup-page", popup["target"])
				}
			},
		},
		{
			name:     "download",
			targetID: "download-page",
			args: func(downloadDir string) []string {
				return []string{"wait", "download", "--match-url", "/download/report.csv", "--filename-contains", "report.csv", "--download-dir", downloadDir}
			},
			waitKind: "download",
			checkExtra: func(t *testing.T, report map[string]any) {
				download, ok := report["download"].(map[string]any)
				if !ok || download["guid"] != "download-1" || download["state"] != "completed" || download["completed"] != true {
					t.Fatalf("download = %#v, want preserved completed download evidence", report["download"])
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			targets := []map[string]any{
				{"targetId": "baseline-page", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": test.targetID, "type": "page", "title": "Second", "url": "https://example.test/second"},
			}
			server := newFakeCDPServer(t, targets)
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")
			downloadDir := t.TempDir()
			if test.name == "download" {
				if err := os.WriteFile(filepath.Join(downloadDir, "download-1"), []byte("report bytes"), 0o600); err != nil {
					t.Fatalf("write download fixture: %v", err)
				}
			}

			args := append(test.args(downloadDir), "--target-index", "2", "--state-dir", stateDir, "--json")
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

func TestWaitObserversRejectInvalidTargetIndex(t *testing.T) {
	commands := [][]string{
		{"wait", "request"},
		{"wait", "response"},
		{"wait", "network-idle"},
		{"wait", "dialog"},
		{"wait", "file-chooser"},
		{"wait", "popup"},
		{"wait", "download"},
	}
	for _, command := range commands {
		name := strings.Join(command, "-")
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

func TestWaitObserversReportOutOfRangeTargetIndex(t *testing.T) {
	commands := [][]string{
		{"wait", "request", "--match-url", "/api"},
		{"wait", "response", "--match-url", "/api"},
		{"wait", "network-idle", "--idle", "10ms"},
		{"wait", "dialog"},
		{"wait", "file-chooser"},
		{"wait", "popup"},
		{"wait", "download"},
	}
	for _, command := range commands {
		name := strings.Join(command[:2], "-")
		t.Run(name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "only-page", "type": "page", "title": "Only page", "url": "https://example.test/only",
			}})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")
			args := append([]string{}, command...)
			if name == "wait-download" {
				args = append(args, "--download-dir", t.TempDir())
			}
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s out-of-range exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}
