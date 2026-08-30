package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestBrowserPreflightReadinessReportsSettledCleanup(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{
			"targetId":             "preflight-sentinel",
			"type":                 "page",
			"title":                "Sentinel",
			"url":                  "https://example.test/sentinel",
			"fakeCloseTargetDelay": true,
		},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"browser", "preflight", "--open-readiness", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("browser preflight readiness exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var result struct {
		OK        bool `json:"ok"`
		Readiness struct {
			Cleanup struct {
				Attempted       bool   `json:"attempted"`
				Closed          bool   `json:"closed"`
				TargetGone      bool   `json:"target_gone"`
				AttemptCount    int    `json:"attempt_count"`
				RetryPolicy     string `json:"retry_policy"`
				TargetID        string `json:"target_id"`
				RecoveryCommand string `json:"recovery_command"`
			} `json:"cleanup"`
		} `json:"readiness"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("browser preflight readiness output is invalid JSON: %v; output=%s", err, out.String())
	}
	cleanup := result.Readiness.Cleanup
	if !result.OK || !cleanup.Attempted || !cleanup.Closed || !cleanup.TargetGone || cleanup.AttemptCount < 1 || cleanup.RetryPolicy != "target_gone" || cleanup.TargetID == "" || cleanup.RecoveryCommand == "" {
		t.Fatalf("browser preflight readiness cleanup=%+v, want settled exact-target evidence", cleanup)
	}
	if pages := fakePagesCount(t); pages != 1 {
		t.Fatalf("browser preflight readiness page count=%d, want sentinel baseline", pages)
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
		t.Fatalf("browser preflight lifecycle events=%v, want target close before session detach", events)
	}
}

func TestBrowserPreflightReadinessKeepOpenReportsRetention(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"browser", "preflight", "--open-readiness", "--keep-open-readiness-tab", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("browser preflight keep-open exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var result struct {
		Readiness struct {
			Cleanup struct {
				Skipped bool   `json:"skipped"`
				Reason  string `json:"reason"`
				Closed  bool   `json:"closed"`
			} `json:"cleanup"`
		} `json:"readiness"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("browser preflight keep-open output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !result.Readiness.Cleanup.Skipped || result.Readiness.Cleanup.Reason != "keep_open" || result.Readiness.Cleanup.Closed {
		t.Fatalf("browser preflight keep-open cleanup=%+v, want explicit retention", result.Readiness.Cleanup)
	}
	if pages := fakePagesCount(t); pages != 1 {
		t.Fatalf("browser preflight keep-open page count=%d, want retained readiness page", pages)
	}
}

func TestBrowserPreflightReadinessCleanupFailureIsVisible(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "preflight-cleanup-error", "type": "page", "title": "Sentinel", "url": "https://example.test/sentinel", "fakeCloseTargetError": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"browser", "preflight", "--open-readiness", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code == cli.ExitOK {
		t.Fatalf("browser preflight cleanup failure exit=%d, want failure; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Readiness struct {
				Cleanup struct {
					Attempted       bool   `json:"attempted"`
					Closed          bool   `json:"closed"`
					Error           string `json:"error"`
					RecoveryCommand string `json:"recovery_command"`
				} `json:"cleanup"`
			} `json:"readiness"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("browser preflight cleanup failure output is invalid JSON: %v; output=%s", err, out.String())
	}
	cleanup := result.Data.Readiness.Cleanup
	if result.OK || result.Code != "browser_preflight_open_readiness_cleanup_failed" || !cleanup.Attempted || cleanup.Closed || cleanup.Error == "" || cleanup.RecoveryCommand == "" {
		t.Fatalf("browser preflight cleanup failure code=%q cleanup=%+v, want visible recovery envelope", result.Code, cleanup)
	}
}

func TestBrowserPreflightReadinessPreservesPrimaryWhenCleanupFails(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "preflight-primary-error", "type": "page", "title": "Sentinel", "url": "https://example.test/sentinel", "fakeCloseTargetError": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"browser", "preflight", "--open-readiness", "--open-url", "https://example.test/attach-error", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code == cli.ExitOK {
		t.Fatalf("browser preflight primary failure exit=%d, want failure; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("browser preflight primary failure output is invalid JSON: %v; output=%s", err, out.String())
	}
	data, _ := result["data"].(map[string]any)
	readiness, _ := data["readiness"].(map[string]any)
	cleanup, _ := readiness["cleanup"].(map[string]any)
	primaryMessage, _ := readiness["error"].(string)
	if result["code"] != "browser_preflight_open_readiness_cleanup_failed" || cleanup["error"] == nil || cleanup["recovery_command"] == nil || !strings.Contains(primaryMessage, "attach race") {
		t.Fatalf("browser preflight primary failure result=%+v, want primary attach error plus cleanup evidence", result)
	}
}

func TestDaemonHealthCheckReportsSettledOwnedCleanup(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "health-sentinel", "type": "page", "title": "Sentinel", "url": "https://example.test/sentinel", "fakeCloseTargetDelay": true},
	})
	defer server.Close()
	startHeadlessFakeDaemon(t, server)

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health-check", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon health-check exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
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
		t.Fatalf("daemon health-check output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !result.OK || !result.Cleanup.Attempted || !result.Cleanup.Closed || !result.Cleanup.TargetGone || result.Cleanup.AttemptCount < 1 || result.Cleanup.RetryPolicy != "target_gone" || result.Cleanup.TargetID == "" || result.Cleanup.RecoveryCommand == "" {
		t.Fatalf("daemon health-check cleanup=%+v, want settled exact-target evidence", result.Cleanup)
	}
	if pages := fakePagesCountForArgs(t, "--browser-mode", "headless"); pages != 1 {
		t.Fatalf("daemon health-check page count=%d, want sentinel baseline", pages)
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
		t.Fatalf("daemon health-check lifecycle events=%v, want target close before session detach", events)
	}
}

func TestDaemonHealthCheckCleanupFailureIsVisible(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "health-cleanup-error", "type": "page", "title": "Sentinel", "url": "https://example.test/sentinel", "fakeCloseTargetError": true},
	})
	defer server.Close()
	startHeadlessFakeDaemon(t, server)

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health-check", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code == cli.ExitOK {
		t.Fatalf("daemon health-check cleanup failure exit=%d, want failure; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Cleanup struct {
				Attempted       bool   `json:"attempted"`
				Closed          bool   `json:"closed"`
				Error           string `json:"error"`
				RecoveryCommand string `json:"recovery_command"`
			} `json:"cleanup"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("daemon health-check cleanup failure output is invalid JSON: %v; output=%s", err, out.String())
	}
	if result.OK || result.Code != "headless_health_check_cleanup_failed" || !result.Data.Cleanup.Attempted || result.Data.Cleanup.Closed || result.Data.Cleanup.Error == "" || result.Data.Cleanup.RecoveryCommand == "" {
		t.Fatalf("daemon health-check cleanup failure code=%q cleanup=%+v, want visible recovery envelope", result.Code, result.Data.Cleanup)
	}
}

func TestDaemonHealthCheckPreservesPrimaryWhenCleanupFails(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "health-primary-error", "type": "page", "title": "Sentinel", "url": "https://example.test/sentinel", "fakeCloseTargetError": true},
	})
	defer server.Close()
	startHeadlessFakeDaemon(t, server)

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health-check", "--health-url", "https://example.test/attach-error", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code == cli.ExitOK {
		t.Fatalf("daemon health-check primary failure exit=%d, want failure; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("daemon health-check primary failure output is invalid JSON: %v; output=%s", err, out.String())
	}
	data, _ := result["data"].(map[string]any)
	cleanup, _ := data["cleanup"].(map[string]any)
	primary, _ := data["primary_error"].(map[string]any)
	topCode, _ := result["code"].(string)
	primaryCode, _ := primary["code"].(string)
	cleanupError, _ := cleanup["error"].(string)
	recoveryCommand, _ := cleanup["recovery_command"].(string)
	if topCode != "headless_health_check_cleanup_failed" || cleanupError == "" || recoveryCommand == "" || primaryCode != "headless_health_check_failed" {
		t.Fatalf("daemon health-check primary failure result=%+v, want primary error plus cleanup evidence", result)
	}
}

func startHeadlessFakeDaemon(t *testing.T, server *httptest.Server) string {
	t.Helper()
	stateDir := shortCLIStateDir(t)
	t.Setenv("CDP_STATE_DIR", stateDir)
	t.Setenv("CDP_DAEMON_BROWSER_MODE", "headless")
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeWebSocketEndpoint(t, server.URL), "browser_url", 30*time.Second)
	}()
	waitForDaemonRuntimeForMode(t, ctx, stateDir, "headless")
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Errorf("headless fake daemon returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("headless fake daemon did not stop")
		}
	})
	return stateDir
}

func fakePagesCountForArgs(t *testing.T, prefix ...string) int {
	t.Helper()
	args := append(append([]string{}, prefix...), "pages", "--json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages with args %v exit code=%d; stdout=%s stderr=%s", prefix, code, out.String(), errOut.String())
	}
	var result struct {
		Pages []json.RawMessage `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("pages with args %v output is invalid JSON: %v", prefix, err)
	}
	return len(result.Pages)
}
