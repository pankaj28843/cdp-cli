package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestResponsiveAuditReportsSettledCleanupBeforeSessionRelease(t *testing.T) {
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
	code := cli.Execute(context.Background(), []string{
		"workflow", "responsive-audit", "https://example.test/app",
		"--viewports", "desktop", "--include", "layout", "--wait", "0s", "--limit", "1", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("responsive audit exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var result struct {
		OK      bool `json:"ok"`
		Cleanup struct {
			Attempted       bool   `json:"attempted"`
			Closed          bool   `json:"closed"`
			TargetGone      bool   `json:"target_gone"`
			AttemptCount    int    `json:"attempt_count"`
			RetryPolicy     string `json:"retry_policy"`
			TargetID        string `json:"target_id"`
			RecoveryCommand string `json:"recovery_command"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("responsive audit output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !result.OK || !result.Cleanup.Attempted || !result.Cleanup.Closed || !result.Cleanup.TargetGone || result.Cleanup.AttemptCount < 1 || result.Cleanup.RetryPolicy != "target_gone" || result.Cleanup.TargetID == "" || result.Cleanup.RecoveryCommand == "" {
		t.Fatalf("responsive audit cleanup=%+v, want settled exact-target evidence", result.Cleanup)
	}
	if pages := fakePagesCount(t); pages != 1 {
		t.Fatalf("responsive audit page count=%d, want sentinel baseline", pages)
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
		t.Fatalf("responsive audit lifecycle events=%v, want target close before session detach", events)
	}
}

func TestResponsiveAuditCleanupFailurePreservesArtifactError(t *testing.T) {
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

	blockedOut := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blockedOut, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocked output: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "responsive-audit", "https://example.test/app",
		"--viewports", "desktop", "--include", "screenshot", "--wait", "0s", "--out-dir", blockedOut, "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitInternal {
		t.Fatalf("responsive audit cleanup failure exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitInternal, out.String(), errOut.String())
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
				Closed          bool   `json:"closed"`
				Error           string `json:"error"`
				RecoveryCommand string `json:"recovery_command"`
			} `json:"cleanup"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("responsive audit cleanup failure output is invalid JSON: %v; output=%s", err, out.String())
	}
	if result.OK || result.Code != "workflow_responsive_audit_cleanup_failed" || result.Data.PrimaryError.Code != "artifact_write_failed" || !result.Data.Cleanup.Attempted || result.Data.Cleanup.Closed || result.Data.Cleanup.Error == "" || result.Data.Cleanup.RecoveryCommand == "" {
		t.Fatalf("responsive audit cleanup failure=%+v, want cleanup failure with primary artifact error", result)
	}
	if pages := fakePagesCount(t); pages != 2 {
		t.Fatalf("responsive audit cleanup failure page count=%d, want sentinel plus unrecovered created page", pages)
	}
}

func TestResponsiveAuditAttachFailureReportsOwnedCleanup(t *testing.T) {
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
	code := cli.Execute(context.Background(), []string{
		"workflow", "responsive-audit", "https://example.test/app",
		"--viewports", "desktop", "--wait", "0s", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitConnection {
		t.Fatalf("responsive audit attach failure exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitConnection, out.String(), errOut.String())
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
		t.Fatalf("responsive audit attach failure output is invalid JSON: %v; output=%s", err, out.String())
	}
	if result.OK || result.Code != "connection_failed" || !result.Data.Cleanup.Attempted || !result.Data.Cleanup.Closed || !result.Data.Cleanup.TargetGone {
		t.Fatalf("responsive audit attach failure=%+v, want connection error with settled cleanup", result)
	}
	if pages := fakePagesCount(t); pages != 1 {
		t.Fatalf("responsive audit attach failure page count=%d, want sentinel baseline", pages)
	}
}

func TestResponsiveAuditIndexedPageReportsCallerOwnership(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "responsive-audit", "--target-index", "2", "--viewports", "desktop", "--include", "layout", "--wait", "0s", "--limit", "1", "--state-dir", stateDir, "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("responsive audit indexed exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var result struct {
		Cleanup struct {
			Skipped bool   `json:"skipped"`
			Reason  string `json:"reason"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("responsive audit indexed output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !result.Cleanup.Skipped || result.Cleanup.Reason != "caller_owned" {
		t.Fatalf("responsive audit indexed cleanup=%+v, want caller_owned skip", result.Cleanup)
	}
	if pages := fakePagesCount(t); pages != 2 {
		t.Fatalf("responsive audit indexed page count=%d, want caller pages retained", pages)
	}
}

func TestResponsiveAuditPreservesRuntimeCollectorEvidenceWithCleanup(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "runtime-error-sentinel", "type": "page", "title": "Runtime error", "url": "https://example.test/sentinel", "fakeRuntimeEvaluateErrorForCreated": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "responsive-audit", "https://example.test/app",
		"--viewports", "desktop", "--include", "layout", "--wait", "0s", "--limit", "1", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("responsive audit runtime collector exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var result struct {
		Cleanup struct {
			Closed     bool `json:"closed"`
			TargetGone bool `json:"target_gone"`
		} `json:"cleanup"`
		Results []struct {
			CollectorErrors []struct {
				Collector string `json:"collector"`
			} `json:"collector_errors"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("responsive audit runtime collector output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !result.Cleanup.Closed || !result.Cleanup.TargetGone || len(result.Results) != 1 || len(result.Results[0].CollectorErrors) == 0 || result.Results[0].CollectorErrors[0].Collector != "layout" {
		t.Fatalf("responsive audit runtime collector result=%+v, want layout evidence and settled cleanup", result)
	}
	if pages := fakePagesCount(t); pages != 1 {
		t.Fatalf("responsive audit runtime collector page count=%d, want sentinel baseline", pages)
	}
}
