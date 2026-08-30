package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestDiagnosticWorkflowOwnedPagesCloseOnSuccess(t *testing.T) {
	tests := []struct {
		name string
		args func(string) []string
	}{
		{
			name: "verify",
			args: func(_ string) []string {
				return []string{"workflow", "verify", "https://example.test/app", "--wait", "0s", "--limit", "1", "--json"}
			},
		},
		{
			name: "perf",
			args: func(dir string) []string {
				return []string{"workflow", "perf", "https://example.test/app", "--wait", "0s", "--trace", filepath.Join(dir, "perf.json"), "--json"}
			},
		},
		{
			name: "a11y",
			args: func(_ string) []string {
				return []string{"workflow", "a11y", "https://example.test/app", "--wait", "0s", "--limit", "1", "--json"}
			},
		},
		{
			name: "page-load",
			args: func(_ string) []string {
				return []string{"workflow", "page-load", "https://example.test/app", "--wait", "0s", "--limit", "1", "--include", "console", "--json"}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, nil)
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), test.args(t.TempDir()), &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s exit=%d, want %d; stdout=%s stderr=%s", test.name, code, cli.ExitOK, out.String(), errOut.String())
			}
			if pages := fakePagesCount(t); pages != 0 {
				t.Fatalf("%s workflow-created page count=%d, want baseline", test.name, pages)
			}

			var result struct {
				OK      bool `json:"ok"`
				Cleanup struct {
					Attempted  bool `json:"attempted"`
					Closed     bool `json:"closed"`
					TargetGone bool `json:"target_gone"`
				} `json:"cleanup"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("%s output is invalid JSON: %v; output=%s", test.name, err, out.String())
			}
			if !result.OK || !result.Cleanup.Attempted || !result.Cleanup.Closed || !result.Cleanup.TargetGone {
				t.Fatalf("%s cleanup=%+v, want settled workflow-owned cleanup", test.name, result.Cleanup)
			}
		})
	}
}

func TestDiagnosticWorkflowCleanupWaitsBeforeSessionRelease(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{
			"targetId":             "cleanup-sentinel",
			"type":                 "page",
			"title":                "Sentinel",
			"url":                  "https://example.test/sentinel",
			"fakeCloseTargetDelay": true,
		},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "verify", "https://example.test/app", "--wait", "0s", "--limit", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("verify delayed cleanup exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var result struct {
		Cleanup struct {
			Closed       bool   `json:"closed"`
			TargetGone   bool   `json:"target_gone"`
			AttemptCount int    `json:"attempt_count"`
			RetryPolicy  string `json:"retry_policy"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("verify delayed cleanup output is invalid JSON: %v", err)
	}
	if !result.Cleanup.Closed || !result.Cleanup.TargetGone || result.Cleanup.AttemptCount < 1 || result.Cleanup.RetryPolicy != "target_gone" {
		t.Fatalf("verify delayed cleanup=%+v, want target-gone evidence", result.Cleanup)
	}
	events := fakeLifecycleEvents(t, server)
	closeAt, detachAt := -1, -1
	for index, event := range events {
		switch event {
		case "close:created-page":
			closeAt = index
		case "detach:session-created-page":
			detachAt = index
		}
	}
	if closeAt < 0 || detachAt < 0 || closeAt > detachAt {
		t.Fatalf("verify lifecycle events=%v, want close before session detach", events)
	}
	if pages := fakePagesCount(t); pages != 1 {
		t.Fatalf("verify delayed cleanup page count=%d, want sentinel baseline", pages)
	}
}

func TestDiagnosticWorkflowCleanupPreservesArtifactError(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{
			"targetId":             "cleanup-error-sentinel",
			"type":                 "page",
			"title":                "Sentinel",
			"url":                  "https://example.test/sentinel",
			"fakeCloseTargetError": true,
		},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	blockedOutput := filepath.Join(t.TempDir(), "blocked")
	if err := os.Mkdir(blockedOutput, 0o700); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "page-load", "https://example.test/app", "--wait", "0s", "--include", "console", "--out", blockedOutput, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitInternal {
		t.Fatalf("page-load cleanup error exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitInternal, out.String(), errOut.String())
	}
	var result struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			PrimaryError struct {
				Code string `json:"code"`
			} `json:"primary_error"`
			Cleanup struct {
				Attempted       bool   `json:"attempted"`
				Error           string `json:"error"`
				RecoveryCommand string `json:"recovery_command"`
			} `json:"cleanup"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("page-load cleanup error output is invalid JSON: %v", err)
	}
	if result.OK || result.Code != "workflow_page_load_cleanup_failed" || result.Data.PrimaryError.Code != "artifact_write_failed" || !result.Data.Cleanup.Attempted || result.Data.Cleanup.Error == "" || result.Data.Cleanup.RecoveryCommand == "" {
		t.Fatalf("page-load cleanup error=%+v, want primary and cleanup evidence", result)
	}
	if pages := fakePagesCount(t); pages != 2 {
		t.Fatalf("page-load cleanup error page count=%d, want sentinel plus unrecovered created page", pages)
	}
}

func TestDiagnosticWorkflowHelpDocumentsOwnership(t *testing.T) {
	for _, workflow := range []string{"verify", "perf", "a11y", "page-load"} {
		t.Run(workflow, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"workflow", workflow, "--help"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s help exit=%d, want %d; stderr=%s", workflow, code, cli.ExitOK, errOut.String())
			}
			for _, fragment := range []string{"disposable page", "bounded exact-target cleanup", "caller-owned page"} {
				if !strings.Contains(out.String(), fragment) {
					t.Fatalf("%s help missing %q:\n%s", workflow, fragment, out.String())
				}
			}
		})
	}
}

func TestDiagnosticWorkflowAttachFailureCleansOwnedPage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "verify", args: []string{"workflow", "verify", "https://example.test/app", "--wait", "0s", "--json"}},
		{name: "perf", args: []string{"workflow", "perf", "https://example.test/app", "--wait", "0s", "--json"}},
		{name: "a11y", args: []string{"workflow", "a11y", "https://example.test/app", "--wait", "0s", "--json"}},
		{name: "page-load", args: []string{"workflow", "page-load", "https://example.test/app", "--wait", "0s", "--include", "console", "--json"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{
					"targetId":                  "attach-error-sentinel",
					"type":                      "page",
					"title":                     "Sentinel",
					"url":                       "https://example.test/sentinel",
					"fakeAttachErrorForCreated": true,
				},
			})
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), test.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitConnection {
				t.Fatalf("%s attach failure exit=%d, want %d; stdout=%s stderr=%s", test.name, code, cli.ExitConnection, out.String(), errOut.String())
			}
			var result struct {
				OK   bool   `json:"ok"`
				Code string `json:"code"`
				Data struct {
					Cleanup struct {
						Attempted  bool `json:"attempted"`
						Closed     bool `json:"closed"`
						TargetGone bool `json:"target_gone"`
					} `json:"cleanup"`
				} `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("%s attach failure output is invalid JSON: %v; output=%s", test.name, err, out.String())
			}
			if result.OK || result.Code != "connection_failed" || !result.Data.Cleanup.Attempted || !result.Data.Cleanup.Closed || !result.Data.Cleanup.TargetGone {
				t.Fatalf("%s attach failure result=%+v, want connection error with settled cleanup", test.name, result)
			}
			if pages := fakePagesCount(t); pages != 1 {
				t.Fatalf("%s attach failure page count=%d, want sentinel baseline", test.name, pages)
			}
		})
	}
}

func TestDiagnosticWorkflowCancellationStillCleansOwnedPage(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"--timeout", "100ms", "workflow", "verify", "https://example.test/app", "--wait", "500ms", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("verify cancellation exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	if pages := fakePagesCount(t); pages != 0 {
		t.Fatalf("verify cancellation page count=%d, want baseline after independent cleanup", pages)
	}
	var result struct {
		Cleanup struct {
			Attempted  bool `json:"attempted"`
			Closed     bool `json:"closed"`
			TargetGone bool `json:"target_gone"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("verify cancellation output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !result.Cleanup.Attempted || !result.Cleanup.Closed || !result.Cleanup.TargetGone {
		t.Fatalf("verify cancellation cleanup=%+v, want settled cleanup", result.Cleanup)
	}
}
