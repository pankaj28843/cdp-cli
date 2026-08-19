package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestTargetsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
		{"targetId": "worker-1", "type": "service_worker", "title": "Worker", "url": "https://example.test/sw.js", "attached": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"targets", "--limit", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("targets exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK      bool `json:"ok"`
		Targets []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("targets output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Targets) != 1 || got.Targets[0].ID != "page-1" {
		t.Fatalf("targets output = %+v, want one limited target", got)
	}
}

func TestTargetsRetriesTransientListFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false, "fakeListTargetsErrorOnce": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"targets", "--retry", "transient", "--max-attempts", "2", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("targets retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK           bool   `json:"ok"`
		RetryPolicy  string `json:"retry_policy"`
		AttemptCount int    `json:"attempt_count"`
		Targets      []struct {
			ID string `json:"id"`
		} `json:"targets"`
		Attempts []struct {
			Retry bool   `json:"retry"`
			Code  string `json:"code"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("targets retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.RetryPolicy != "transient" || got.AttemptCount != 2 || len(got.Attempts) != 2 || !got.Attempts[0].Retry || got.Attempts[0].Code != "connection_failed" || len(got.Targets) != 1 || got.Targets[0].ID != "page-1" {
		t.Fatalf("targets retry output = %+v, want one transient retry before success", got)
	}
}

func TestTargetsTypeFilterJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
		{"targetId": "worker-1", "type": "service_worker", "title": "Worker", "url": "https://example.test/sw.js", "attached": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"targets", "--type", "service_worker", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("targets exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Targets []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("targets output is invalid JSON: %v", err)
	}
	if len(got.Targets) != 1 || got.Targets[0].ID != "worker-1" || got.Targets[0].Type != "service_worker" {
		t.Fatalf("targets output = %+v, want service worker only", got)
	}
}

func TestPagesJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
		{"targetId": "worker-1", "type": "service_worker", "title": "Worker", "url": "https://example.test/sw.js", "attached": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Pages []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			URL      string `json:"url"`
			Attached bool   `json:"attached"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Pages) != 1 || got.Pages[0].ID != "page-1" || got.Pages[0].Type != "page" {
		t.Fatalf("pages output = %+v, want one page target", got)
	}
}

func TestPagesRetriesTransientListFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false, "fakeListTargetsErrorOnce": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--retry", "transient", "--max-attempts", "2", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK           bool   `json:"ok"`
		RetryPolicy  string `json:"retry_policy"`
		AttemptCount int    `json:"attempt_count"`
		Pages        []struct {
			ID string `json:"id"`
		} `json:"pages"`
		Attempts []struct {
			Retry bool   `json:"retry"`
			Code  string `json:"code"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.RetryPolicy != "transient" || got.AttemptCount != 2 || len(got.Attempts) != 2 || !got.Attempts[0].Retry || got.Attempts[0].Code != "connection_failed" || len(got.Pages) != 1 || got.Pages[0].ID != "page-1" {
		t.Fatalf("pages retry output = %+v, want one transient retry before success", got)
	}
}

func TestPageCloseWaitsUntilTargetGoneJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-close", "type": "page", "title": "Close Me", "url": "https://example.test/close", "attached": false},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "close", "--target", "page-close", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page close exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK           bool `json:"ok"`
		Closed       bool `json:"closed"`
		TargetGone   bool `json:"target_gone"`
		AttemptCount int  `json:"attempt_count"`
		MaxAttempts  int  `json:"max_attempts"`
		Attempts     []struct {
			CloseSent  bool `json:"close_sent"`
			TargetGone bool `json:"target_gone"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page close output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Closed || !got.TargetGone || got.AttemptCount != 1 || got.MaxAttempts != 3 || len(got.Attempts) != 1 || !got.Attempts[0].CloseSent || !got.Attempts[0].TargetGone {
		t.Fatalf("page close = %+v, want one settled close attempt with target_gone", got)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"pages", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages after close exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var pages struct {
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &pages); err != nil {
		t.Fatalf("pages output is invalid JSON: %v", err)
	}
	if len(pages.Pages) != 0 {
		t.Fatalf("pages after close = %+v, want target gone", pages.Pages)
	}
}

func TestPageCleanupJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-visible", "type": "page", "title": "Visible Page", "url": "https://example.test/visible", "attached": false},
		{"targetId": "page-hidden", "type": "page", "title": "Hidden Page", "url": "https://example.test/hidden", "attached": false},
		{"targetId": "page-attached", "type": "page", "title": "Attached Page", "url": "https://example.test/attached", "attached": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "cleanup", "--include-url", "example.test", "--idle-for", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page cleanup exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK      bool `json:"ok"`
		Cleanup struct {
			DryRun          bool `json:"dry_run"`
			CandidateCount  int  `json:"candidate_count"`
			ReadyCount      int  `json:"ready_count"`
			WouldCloseCount int  `json:"would_close_count"`
			CloseRequired   bool `json:"close_required"`
			ClosedCount     int  `json:"closed_count"`
		} `json:"cleanup"`
		Candidates []struct {
			Target struct {
				ID string `json:"targetId"`
			} `json:"target"`
			VisibilityState string `json:"visibility_state"`
			Hidden          bool   `json:"hidden"`
			KeepReason      string `json:"keep_reason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page cleanup output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Cleanup.DryRun || got.Cleanup.CandidateCount != 1 || got.Cleanup.ReadyCount != 1 || got.Cleanup.WouldCloseCount != 1 || !got.Cleanup.CloseRequired || got.Cleanup.ClosedCount != 0 {
		t.Fatalf("page cleanup summary = %+v, want one dry-run candidate", got.Cleanup)
	}
	if len(got.Candidates) != 3 || got.Candidates[0].KeepReason != "visible" || got.Candidates[1].KeepReason != "" || !got.Candidates[1].Hidden || got.Candidates[2].KeepReason != "attached" {
		t.Fatalf("page cleanup candidates = %+v, want visible kept, hidden candidate, attached kept", got.Candidates)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"page", "cleanup", "--include-url", "example.test", "--idle-for", "0s", "--close", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page cleanup close exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var closed struct {
		Cleanup struct {
			DryRun          bool `json:"dry_run"`
			WouldCloseCount int  `json:"would_close_count"`
			CloseRequired   bool `json:"close_required"`
			ClosedCount     int  `json:"closed_count"`
		} `json:"cleanup"`
		Closed []struct {
			Target struct {
				ID string `json:"targetId"`
			} `json:"target"`
		} `json:"closed"`
	}
	if err := json.Unmarshal(out.Bytes(), &closed); err != nil {
		t.Fatalf("page cleanup close output is invalid JSON: %v", err)
	}
	if closed.Cleanup.DryRun || closed.Cleanup.WouldCloseCount != 0 || closed.Cleanup.CloseRequired || closed.Cleanup.ClosedCount != 1 || len(closed.Closed) != 1 || closed.Closed[0].Target.ID != "page-hidden" {
		t.Fatalf("page cleanup close = %+v, want hidden page closed", closed)
	}
}

func TestPageCleanupRecoversEmptyStateJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-empty-state", "type": "page", "title": "Empty State", "url": "https://example.test/empty-state", "attached": false},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll state dir returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "page-cleanup.json"), nil, 0o600); err != nil {
		t.Fatalf("write empty cleanup state: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "cleanup", "--include-url", "example.test", "--idle-for", "0s", "--force", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page cleanup exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK      bool `json:"ok"`
		Cleanup struct {
			StateWarnings []string `json:"state_warnings"`
			ReadyCount    int      `json:"ready_count"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page cleanup output is invalid JSON: %v", err)
	}
	if !got.OK || got.Cleanup.ReadyCount != 1 || len(got.Cleanup.StateWarnings) != 1 || !strings.Contains(got.Cleanup.StateWarnings[0], "empty") {
		t.Fatalf("page cleanup = %+v, want recovered empty state warning and ready candidate", got)
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "page-cleanup.json"))
	if err != nil {
		t.Fatalf("read cleanup state: %v", err)
	}
	var saved struct {
		Pages []struct {
			TargetID string `json:"target_id"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatalf("saved cleanup state is invalid JSON after recovery: %v\n%s", err, string(b))
	}
	if len(saved.Pages) != 1 || saved.Pages[0].TargetID != "page-empty-state" {
		t.Fatalf("saved cleanup state = %+v, want recovered page record", saved.Pages)
	}
}

func TestPageCleanupForceClosesMatchingPagesJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-visible", "type": "page", "title": "Visible Page", "url": "https://example.test/visible", "attached": false},
		{"targetId": "page-hidden", "type": "page", "title": "Hidden Page", "url": "https://example.test/hidden", "attached": false},
		{"targetId": "page-attached", "type": "page", "title": "Attached Page", "url": "https://example.test/attached", "attached": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "cleanup", "--include-url", "example.test", "--idle-for", "0s", "--close", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page cleanup force close exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Cleanup struct {
			ClosedCount int  `json:"closed_count"`
			Force       bool `json:"force"`
		} `json:"cleanup"`
		Closed []struct {
			Target struct {
				ID string `json:"targetId"`
			} `json:"target"`
		} `json:"closed"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page cleanup force output is invalid JSON: %v", err)
	}
	if !got.Cleanup.Force || got.Cleanup.ClosedCount != 3 || len(got.Closed) != 3 {
		t.Fatalf("page cleanup force = %+v, want all matching pages closed", got)
	}
}

func TestPageCleanupStateIsScopedByBrowserModeJSON(t *testing.T) {
	headlessServer := newFakeCDPServer(t, []map[string]any{
		{"targetId": "headless-page", "type": "page", "title": "Headless Page", "url": "https://example.test/headless", "attached": false},
	})
	defer headlessServer.Close()

	stateDir := shortCLIStateDir(t)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	cleanupStatePath := filepath.Join(stateDir, "page-cleanup.json")
	cleanupState := []byte(`{
  "pages": [
    {"browser_mode":"headed","connection":"default","target_id":"headed-page","first_seen":"2026-05-21T12:00:00Z","last_seen":"2026-05-21T12:00:00Z"},
    {"browser_mode":"headless","connection":"default","target_id":"stale-headless-page","first_seen":"2026-05-21T12:00:00Z","last_seen":"2026-05-21T12:00:00Z"}
  ]
}
`)
	if err := os.WriteFile(cleanupStatePath, cleanupState, 0o600); err != nil {
		t.Fatalf("write cleanup state: %v", err)
	}

	headlessCtx, headlessCancel := context.WithCancel(context.Background())
	headlessErr := make(chan error, 1)
	go func() {
		oldMode := os.Getenv("CDP_DAEMON_BROWSER_MODE")
		_ = os.Setenv("CDP_DAEMON_BROWSER_MODE", "headless")
		defer os.Setenv("CDP_DAEMON_BROWSER_MODE", oldMode)
		headlessErr <- daemon.Hold(headlessCtx, stateDir, fakeWebSocketEndpoint(t, headlessServer.URL), "browser_url", 30*time.Second)
	}()
	waitForDaemonRuntimeForMode(t, headlessCtx, stateDir, "headless")
	defer func() {
		headlessCancel()
		select {
		case err := <-headlessErr:
			if err != nil && err != context.Canceled {
				t.Fatalf("headless daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("headless daemon hold did not stop")
		}
	}()

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "page", "cleanup", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("headless cleanup exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Cleanup struct {
			BrowserMode string `json:"browser_mode"`
			Max         int    `json:"max"`
			MaxSource   string `json:"max_source"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("headless cleanup output is invalid JSON: %v", err)
	}
	if got.Cleanup.BrowserMode != "headless" {
		t.Fatalf("cleanup browser mode = %q, want headless", got.Cleanup.BrowserMode)
	}
	if got.Cleanup.Max != cdp.DefaultHeadlessMaxTabs || got.Cleanup.MaxSource != "mode_default" {
		t.Fatalf("cleanup max = %d source %q, want headless mode default", got.Cleanup.Max, got.Cleanup.MaxSource)
	}

	b, err := os.ReadFile(cleanupStatePath)
	if err != nil {
		t.Fatalf("read cleanup state: %v", err)
	}
	var saved struct {
		Pages []struct {
			BrowserMode string `json:"browser_mode"`
			Connection  string `json:"connection"`
			TargetID    string `json:"target_id"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatalf("cleanup state is invalid JSON: %v", err)
	}
	var sawHeaded, sawStaleHeadless bool
	for _, page := range saved.Pages {
		if page.BrowserMode == "headed" && page.TargetID == "headed-page" {
			sawHeaded = true
		}
		if page.BrowserMode == "headless" && page.TargetID == "stale-headless-page" {
			sawStaleHeadless = true
		}
	}
	if !sawHeaded || sawStaleHeadless {
		t.Fatalf("cleanup state = %+v, want headed preserved and stale headless pruned", saved.Pages)
	}
}

func TestPageCleanupHeadlessConnectionErrorUsesModeScopedRemediation(t *testing.T) {
	var out, errOut bytes.Buffer
	stateDir := t.TempDir()
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "page", "cleanup", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitConnection {
		t.Fatalf("page cleanup exit code = %d, want %d; stderr=%s", code, cli.ExitConnection, errOut.String())
	}
	if runtimeState, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless"); err != nil || ok {
		t.Fatalf("headless runtime after cleanup error = %+v ok=%v err=%v, want no daemon auto-repair side effect", runtimeState, ok, err)
	}
	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page cleanup error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "connection_not_configured" {
		t.Fatalf("page cleanup error = %+v, want connection_not_configured", got)
	}
	if !containsString(got.RemediationCommands, "cdp --browser-mode headless daemon keepalive --repair --json") {
		t.Fatalf("page cleanup remediation commands = %+v, want headless repair command", got.RemediationCommands)
	}
	for _, command := range got.RemediationCommands {
		if strings.Contains(command, "daemon keepalive --auto-connect") {
			t.Fatalf("page cleanup remediation commands = %+v, must not suggest headed auto-connect repair for headless", got.RemediationCommands)
		}
	}
}

func TestPageSelectJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "First Page", "url": "https://example.test/first", "attached": false},
		{"targetId": "page-2", "type": "page", "title": "Second Page", "url": "https://example.test/second", "attached": false},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "select", "page-2", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page select exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK           bool `json:"ok"`
		SelectedPage struct {
			BrowserMode string `json:"browser_mode"`
			Connection  string `json:"connection"`
			TargetID    string `json:"target_id"`
			URL         string `json:"url"`
		} `json:"selected_page"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page select output is invalid JSON: %v", err)
	}
	if !got.OK || got.SelectedPage.TargetID != "page-2" || got.SelectedPage.Connection != "default" || got.SelectedPage.BrowserMode != "headed" || got.Target.ID != "page-2" {
		t.Fatalf("page select = %+v, want headed default page-2 selection", got)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"eval", "document.title", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("eval exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var evalOut struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &evalOut); err != nil {
		t.Fatalf("eval output is invalid JSON: %v", err)
	}
	if !evalOut.OK || evalOut.Target.ID != "page-2" {
		t.Fatalf("eval target = %+v, want selected page-2", evalOut.Target)
	}
}

func TestPageSelectionIsScopedByBrowserMode(t *testing.T) {
	headedServer := newFakeCDPServer(t, []map[string]any{
		{"targetId": "headed-page", "type": "page", "title": "Headed Page", "url": "https://example.test/headed", "attached": false},
	})
	defer headedServer.Close()
	headlessServer := newFakeCDPServer(t, []map[string]any{
		{"targetId": "headless-page", "type": "page", "title": "Headless Page", "url": "https://example.test/headless", "attached": false},
	})
	defer headlessServer.Close()

	stateDir := shortCLIStateDir(t)
	t.Setenv("CDP_STATE_DIR", stateDir)

	headedCtx, headedCancel := context.WithCancel(context.Background())
	headedErr := make(chan error, 1)
	go func() {
		headedErr <- daemon.Hold(headedCtx, stateDir, fakeWebSocketEndpoint(t, headedServer.URL), "browser_url", 30*time.Second)
	}()
	waitForDaemonRuntime(t, headedCtx, stateDir)
	defer func() {
		headedCancel()
		select {
		case err := <-headedErr:
			if err != nil && err != context.Canceled {
				t.Fatalf("headed daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("headed daemon hold did not stop")
		}
	}()

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "select", "headed-page", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("headed page select exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	headlessCtx, headlessCancel := context.WithCancel(context.Background())
	headlessErr := make(chan error, 1)
	go func() {
		oldMode := os.Getenv("CDP_DAEMON_BROWSER_MODE")
		_ = os.Setenv("CDP_DAEMON_BROWSER_MODE", "headless")
		defer os.Setenv("CDP_DAEMON_BROWSER_MODE", oldMode)
		headlessErr <- daemon.Hold(headlessCtx, stateDir, fakeWebSocketEndpoint(t, headlessServer.URL), "browser_url", 30*time.Second)
	}()
	waitForDaemonRuntimeForMode(t, headlessCtx, stateDir, "headless")
	defer func() {
		headlessCancel()
		select {
		case err := <-headlessErr:
			if err != nil && err != context.Canceled {
				t.Fatalf("headless daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("headless daemon hold did not stop")
		}
	}()

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "page", "select", "headless-page", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("headless page select exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"eval", "document.title", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("headed eval exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var headedEval struct {
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &headedEval); err != nil {
		t.Fatalf("headed eval output is invalid JSON: %v", err)
	}
	if headedEval.Target.ID != "headed-page" {
		t.Fatalf("headed eval target = %+v, want headed-page", headedEval.Target)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "eval", "document.title", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("headless eval exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var headlessEval struct {
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &headlessEval); err != nil {
		t.Fatalf("headless eval output is invalid JSON: %v", err)
	}
	if headlessEval.Target.ID != "headless-page" {
		t.Fatalf("headless eval target = %+v, want headless-page", headlessEval.Target)
	}
}

func TestPagesUsesRunningDaemonByDefaultJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	stateDir := shortCLIStateDir(t)
	var addOut, addErr bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "default", "--auto-connect", "--state-dir", stateDir, "--json"}, &addOut, &addErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stderr=%s", code, cli.ExitOK, addErr.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeWebSocketEndpoint(t, server.URL), "auto_connect", 30*time.Second)
	}()
	waitForDaemonRuntime(t, ctx, stateDir)
	defer func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	}()

	var out, errOut bytes.Buffer
	code = cli.Execute(context.Background(), []string{"pages", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK    bool `json:"ok"`
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Pages) != 1 || got.Pages[0].ID != "page-1" {
		t.Fatalf("pages output = %+v, want daemon-backed page target", got)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"doctor", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var doctor struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			State  string `json:"state"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &doctor); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	var sawDaemon, sawBrowser bool
	for _, check := range doctor.Checks {
		if check.Name == "daemon" {
			sawDaemon = true
			if check.Status != "pass" || check.State != "running" {
				t.Fatalf("daemon check = %+v, want running pass", check)
			}
		}
		if check.Name == "browser_debug_endpoint" {
			sawBrowser = true
			if check.Status != "pass" {
				t.Fatalf("browser check = %+v, want pass when daemon is running", check)
			}
		}
	}
	if !sawDaemon || !sawBrowser {
		t.Fatalf("doctor checks = %+v, want daemon and browser checks", doctor.Checks)
	}
}

func TestDoctorBrowserCheckUsesHealthyDaemonWhenSavedEndpointIsStale(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()

	stateDir := shortCLIStateDir(t)
	var addOut, addErr bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "default", "--browser-url", "http://stale-endpoint.invalid", "--state-dir", stateDir, "--json"}, &addOut, &addErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stderr=%s", code, cli.ExitOK, addErr.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeWebSocketEndpoint(t, server.URL), "browser_url", 30*time.Second)
	}()
	waitForDaemonRuntime(t, ctx, stateDir)
	defer func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	}()

	var out, errOut bytes.Buffer
	code = cli.Execute(context.Background(), []string{"doctor", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var doctor struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Message string `json:"message"`
			Details struct {
				State string `json:"state"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &doctor); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	if !doctor.OK {
		t.Fatalf("doctor ok = false; checks = %+v", doctor.Checks)
	}
	for _, check := range doctor.Checks {
		if check.Name == "browser_debug_endpoint" {
			if check.Status != "pass" || check.Details.State != "running" || !strings.Contains(check.Message, "daemon") {
				t.Fatalf("browser check = %+v, want pass from healthy daemon despite stale saved endpoint", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want browser_debug_endpoint", doctor.Checks)
}

func TestPagesURLFilterJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
		{"targetId": "page-2", "type": "page", "title": "Docs", "url": "https://docs.example.test/", "attached": false},
		{"targetId": "page-3", "type": "page", "title": "Docs Admin", "url": "https://docs.example.test/admin", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"contains", []string{"pages", "--url-contains", "docs", "--json"}, []string{"page-2", "page-3"}},
		{"include", []string{"pages", "--include-url", "docs", "--json"}, []string{"page-2", "page-3"}},
		{"exclude", []string{"pages", "--include-url", "docs", "--exclude-url", "admin", "--json"}, []string{"page-2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
			}

			var got struct {
				Pages []struct {
					ID  string `json:"id"`
					URL string `json:"url"`
				} `json:"pages"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("pages output is invalid JSON: %v", err)
			}
			var ids []string
			for _, page := range got.Pages {
				ids = append(ids, page.ID)
			}
			if strings.Join(ids, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("pages output ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestPagesIncludeBrowserBudgetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": true},
		{"targetId": "page-window-2", "type": "page", "title": "Docs", "url": "https://docs.example.test/", "attached": false},
		{"targetId": "worker-1", "type": "service_worker", "title": "Worker", "url": "https://example.test/sw.js", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Budget struct {
			TabCount                  int            `json:"tab_count"`
			MaxTabs                   int            `json:"max_tabs"`
			MaxTabsSource             string         `json:"max_tabs_source"`
			BrowserMode               string         `json:"browser_mode"`
			WindowCount               int            `json:"window_count"`
			WindowCountKnown          bool           `json:"window_count_known"`
			AttachedPageCount         int            `json:"attached_page_count"`
			TargetTypeCounts          map[string]int `json:"target_type_counts"`
			TargetResourceAttribution struct {
				State  string `json:"state"`
				Reason string `json:"reason"`
			} `json:"target_resource_attribution"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages output is invalid JSON: %v", err)
	}
	if !got.OK || got.Budget.TabCount != 2 || got.Budget.MaxTabs != cdp.DefaultHeadedMaxTabs || got.Budget.MaxTabsSource != "mode_default" || got.Budget.BrowserMode != "headed" || got.Budget.WindowCount != 2 || !got.Budget.WindowCountKnown || got.Budget.AttachedPageCount != 1 || got.Budget.TargetTypeCounts["service_worker"] != 1 || got.Budget.TargetResourceAttribution.State != "unavailable" || got.Budget.TargetResourceAttribution.Reason == "" {
		t.Fatalf("pages budget = %+v, want tab/window budget summary", got.Budget)
	}
}

func TestPagesHeadlessBudgetDefaultAndOverrideJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	stateDir := shortCLIStateDir(t)
	t.Setenv("CDP_STATE_DIR", stateDir)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		oldMode := os.Getenv("CDP_DAEMON_BROWSER_MODE")
		_ = os.Setenv("CDP_DAEMON_BROWSER_MODE", "headless")
		defer os.Setenv("CDP_DAEMON_BROWSER_MODE", oldMode)
		errCh <- daemon.Hold(ctx, stateDir, fakeWebSocketEndpoint(t, server.URL), "browser_url", 30*time.Second)
	}()
	waitForDaemonRuntimeForMode(t, ctx, stateDir, "headless")
	defer func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("headless daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("headless daemon hold did not stop")
		}
	}()

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("headless pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Budget struct {
			MaxTabs       int    `json:"max_tabs"`
			MaxTabsSource string `json:"max_tabs_source"`
			BrowserMode   string `json:"browser_mode"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("headless pages output is invalid JSON: %v", err)
	}
	if got.Budget.MaxTabs != cdp.DefaultHeadlessMaxTabs || got.Budget.MaxTabsSource != "mode_default" || got.Budget.BrowserMode != "headless" {
		t.Fatalf("headless pages budget = %+v, want headless mode default", got.Budget)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "--max-tabs", "33", "pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("headless pages override exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("headless pages override output is invalid JSON: %v", err)
	}
	if got.Budget.MaxTabs != 33 || got.Budget.MaxTabsSource != "flag" || got.Budget.BrowserMode != "headless" {
		t.Fatalf("headless pages override budget = %+v, want flag override", got.Budget)
	}
}

func TestPagesBudgetConfigOverrideJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"resource_budget":{"max_tabs":33}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--config", configPath, "pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages config override exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Budget struct {
			MaxTabs       int    `json:"max_tabs"`
			MaxTabsSource string `json:"max_tabs_source"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages config override output is invalid JSON: %v", err)
	}
	if got.Budget.MaxTabs != 33 || got.Budget.MaxTabsSource != "config" {
		t.Fatalf("pages config override budget = %+v, want config max-tabs", got.Budget)
	}
}

func TestOpenRefusesConfiguredRendererBudgetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"resource_budget":{"max_renderer_processes":1}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--config", configPath, "open", "https://example.test/new", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitConnection {
		t.Fatalf("open exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitConnection, out.String(), errOut.String())
	}
	var got struct {
		OK             bool   `json:"ok"`
		Code           string `json:"code"`
		Message        string `json:"message"`
		ResourceBudget struct {
			RendererProcessCount        int  `json:"renderer_process_count"`
			MaxRendererProcesses        int  `json:"max_renderer_processes"`
			RendererCountKnown          bool `json:"renderer_count_known"`
			RendererProcessesOverBudget bool `json:"renderer_processes_over_budget"`
		} `json:"resource_budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("open renderer budget output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "browser_resource_budget_exceeded" || !strings.Contains(got.Message, "1/1 renderer processes") || !got.ResourceBudget.RendererCountKnown || got.ResourceBudget.RendererProcessCount != 1 || got.ResourceBudget.MaxRendererProcesses != 1 || !got.ResourceBudget.RendererProcessesOverBudget {
		t.Fatalf("open renderer budget error = %+v, want renderer budget refusal", got)
	}
}

func TestOpenRefusesOverBudgetJSON(t *testing.T) {
	targets := make([]map[string]any, 0, cdp.DefaultMaxTabs)
	for i := 0; i < cdp.DefaultMaxTabs; i++ {
		targets = append(targets, map[string]any{"targetId": fmt.Sprintf("page-%02d", i+1), "type": "page", "title": "Tab", "url": "https://example.test/tab", "attached": false})
	}
	server := newFakeCDPServer(t, targets)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"open", "https://example.test/new", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitConnection {
		t.Fatalf("open exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitConnection, out.String(), errOut.String())
	}
	var got struct {
		OK             bool   `json:"ok"`
		Code           string `json:"code"`
		ErrClass       string `json:"err_class"`
		ResourceBudget struct {
			TabCount       int  `json:"tab_count"`
			MaxTabs        int  `json:"max_tabs"`
			TabsOverBudget bool `json:"tabs_over_budget"`
		} `json:"resource_budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("open error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "browser_resource_budget_exceeded" || got.ErrClass != "resource_budget" || got.ResourceBudget.TabCount != cdp.DefaultMaxTabs || !got.ResourceBudget.TabsOverBudget {
		t.Fatalf("open error = %+v, want resource budget refusal", got)
	}
}

func TestEvalAmbiguousTargetPrefixFailsBeforeAttach(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-prefix-one", "type": "page", "title": "First Page", "url": "https://example.test/first", "attached": false},
		{"targetId": "page-prefix-two", "type": "page", "title": "Second Page", "url": "https://example.test/second", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"eval", "document.title", "--target", "page-prefix", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("eval ambiguous target exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	var got struct {
		OK       bool     `json:"ok"`
		Code     string   `json:"code"`
		ErrClass string   `json:"err_class"`
		Commands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("eval ambiguous target output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "ambiguous_target" || got.ErrClass != "usage" || len(got.Commands) == 0 {
		t.Fatalf("eval ambiguous target = %+v, want structured ambiguity envelope", got)
	}
}

func TestPageReloadJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "reload", "--target", "page", "--ignore-cache", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page reload exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page reload output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "reloaded" || got.Target.ID != "page-1" {
		t.Fatalf("page reload = %+v, want reloaded page-1", got)
	}
}

func TestPageHistoryNavigationJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/current", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tests := []struct {
		name   string
		args   []string
		action string
		entry  int
	}{
		{"back", []string{"page", "back", "--target", "page", "--json"}, "back", 1},
		{"forward", []string{"page", "forward", "--target", "page", "--json"}, "forward", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s exit code = %d, want %d; stderr=%s", tt.name, code, cli.ExitOK, errOut.String())
			}

			var got struct {
				OK      bool   `json:"ok"`
				Action  string `json:"action"`
				History struct {
					EntryID int `json:"entry_id"`
				} `json:"history"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("%s output is invalid JSON: %v", tt.name, err)
			}
			if !got.OK || got.Action != tt.action || got.History.EntryID != tt.entry {
				t.Fatalf("%s = %+v, want action %s entry %d", tt.name, got, tt.action, tt.entry)
			}
		})
	}
}

func TestPageCloseAndActivateJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	tests := []struct {
		name   string
		args   []string
		action string
	}{
		{"activate", []string{"page", "activate", "--target", "page", "--json"}, "activated"},
		{"close", []string{"page", "close", "--target", "page", "--json"}, "closed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			args := append([]string{"--state-dir", stateDir}, tt.args...)
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s exit code = %d, want %d; stderr=%s", tt.name, code, cli.ExitOK, errOut.String())
			}

			var got struct {
				OK     bool   `json:"ok"`
				Action string `json:"action"`
				Target struct {
					ID string `json:"id"`
				} `json:"target"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("%s output is invalid JSON: %v", tt.name, err)
			}
			if !got.OK || got.Action != tt.action || got.Target.ID != "page-1" {
				t.Fatalf("%s = %+v, want action %s on page-1", tt.name, got, tt.action)
			}
		})
	}
}

func TestTextCommandJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"text", "main", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("text exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Text struct {
			Selector string `json:"selector"`
			Text     string `json:"text"`
			Items    []struct {
				Text string `json:"text"`
			} `json:"items"`
		} `json:"text"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("text output is invalid JSON: %v", err)
	}
	if !got.OK || got.Text.Selector != "main" || got.Text.Text != "Synthetic main text" || len(got.Text.Items) != 1 {
		t.Fatalf("text output = %+v, want compact text result", got)
	}
}

func TestTextRetriesTargetLookupRaceJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false, "fakeListTargetsErrorOnce": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"text", "main", "--retry", "transient", "--max-attempts", "2", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("text retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK           bool   `json:"ok"`
		RetryPolicy  string `json:"retry_policy"`
		AttemptCount int    `json:"attempt_count"`
		Text         struct {
			Text string `json:"text"`
		} `json:"text"`
		Attempts []struct {
			Retry bool   `json:"retry"`
			Code  string `json:"code"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("text retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.Text.Text != "Synthetic main text" || got.RetryPolicy != "transient" || got.AttemptCount != 2 || len(got.Attempts) != 2 || !got.Attempts[0].Retry || got.Attempts[0].Code != "connection_failed" {
		t.Fatalf("text retry output = %+v, want target-list retry before success", got)
	}
}

func TestHTMLCommandJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"html", "main", "--max-chars", "80", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("html exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		HTML struct {
			Selector string `json:"selector"`
			Items    []struct {
				HTML string `json:"html"`
			} `json:"items"`
		} `json:"html"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("html output is invalid JSON: %v", err)
	}
	if !got.OK || got.HTML.Selector != "main" || len(got.HTML.Items) != 1 || !strings.Contains(got.HTML.Items[0].HTML, "Synthetic") {
		t.Fatalf("html output = %+v, want compact html result", got)
	}
}

func TestHTMLCommandEmptyDiagnosticsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"html", "empty", "--diagnose-empty", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("html exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK          bool                `json:"ok"`
		Warnings    []string            `json:"warnings"`
		HTML        struct{ Count int } `json:"html"`
		Diagnostics struct {
			SelectorMatched    bool     `json:"selector_matched"`
			SelectorMatchCount int      `json:"selector_match_count"`
			FrameCount         int      `json:"frame_count"`
			ShadowRootCount    int      `json:"shadow_root_count"`
			PossibleCauses     []string `json:"possible_causes"`
			SuggestedCommands  []string `json:"suggested_commands"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("html output is invalid JSON: %v", err)
	}
	if !got.OK || got.HTML.Count != 0 || len(got.Warnings) == 0 || !got.Diagnostics.SelectorMatched || got.Diagnostics.SelectorMatchCount != 1 || got.Diagnostics.FrameCount != 2 || got.Diagnostics.ShadowRootCount != 1 {
		t.Fatalf("html empty diagnostics = %+v, want empty extraction diagnostics", got)
	}
	if !containsString(got.Diagnostics.PossibleCauses, "iframe_content") || !containsString(got.Diagnostics.SuggestedCommands, "cdp frames --target page-1 --json") {
		t.Fatalf("html empty diagnostics = %+v, want causes and suggested commands", got.Diagnostics)
	}
}

func TestObserveJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"observe", "--selector", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("observe exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK      bool `json:"ok"`
		Observe struct {
			Count       int `json:"count"`
			Interactive []struct {
				Ref      string `json:"ref"`
				Role     string `json:"role"`
				Name     string `json:"name"`
				Selector string `json:"selector"`
				Visible  bool   `json:"visible"`
			} `json:"interactive"`
		} `json:"observe"`
		Interactive []struct {
			Ref string `json:"ref"`
		} `json:"interactive"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("observe output is invalid JSON: %v", err)
	}
	if !got.OK || got.Observe.Count != 1 || len(got.Interactive) != 1 {
		t.Fatalf("observe output = %+v, want one interactive element", got)
	}
	node := got.Observe.Interactive[0]
	if node.Ref != "obs:0" || node.Role != "button" || node.Name != "Save changes" || node.Selector != "button#save" || !node.Visible {
		t.Fatalf("observe interactive node = %+v, want stable agent action hint", node)
	}
}

func TestDOMQueryJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"dom", "query", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("dom query exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Query struct {
			Selector string `json:"selector"`
			Nodes    []struct {
				UID  string `json:"uid"`
				Role string `json:"role"`
				Text string `json:"text"`
			} `json:"nodes"`
		} `json:"query"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("dom query output is invalid JSON: %v", err)
	}
	if !got.OK || got.Query.Selector != "button" || len(got.Query.Nodes) != 1 || got.Query.Nodes[0].Role != "button" {
		t.Fatalf("dom query output = %+v, want button node", got)
	}
}

func TestCSSInspectJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"css", "inspect", "main", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("css inspect exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK      bool `json:"ok"`
		Inspect struct {
			Selector string            `json:"selector"`
			Styles   map[string]string `json:"styles"`
		} `json:"inspect"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("css inspect output is invalid JSON: %v", err)
	}
	if !got.OK || got.Inspect.Selector != "main" || got.Inspect.Styles["display"] != "block" {
		t.Fatalf("css inspect output = %+v, want display block", got)
	}
}

func TestLayoutOverflowJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"layout", "overflow", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("layout overflow exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Overflow struct {
			Count int `json:"count"`
			Items []struct {
				UID string `json:"uid"`
			} `json:"items"`
		} `json:"overflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("layout overflow output is invalid JSON: %v", err)
	}
	if !got.OK || got.Overflow.Count != 1 || got.Overflow.Items[0].UID == "" {
		t.Fatalf("layout overflow output = %+v, want one overflow item", got)
	}
}

func TestWaitTextJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "text", "Ready", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait text exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind      string         `json:"kind"`
			Needle    string         `json:"needle"`
			Matched   bool           `json:"matched"`
			Condition string         `json:"condition"`
			Evidence  map[string]any `json:"evidence"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait text output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "text" || got.Wait.Needle != "Ready" || !got.Wait.Matched || !strings.Contains(got.Wait.Condition, "Ready") || got.Wait.Evidence["needle"] != "Ready" {
		t.Fatalf("wait text output = %+v, want matched text", got)
	}
}

func TestWaitSelectorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "selector", "main", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait selector exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind      string         `json:"kind"`
			Selector  string         `json:"selector"`
			Matched   bool           `json:"matched"`
			Condition string         `json:"condition"`
			Evidence  map[string]any `json:"evidence"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait selector output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "selector" || got.Wait.Selector != "main" || !got.Wait.Matched || !strings.Contains(got.Wait.Condition, "main") || got.Wait.Evidence["selector"] != "main" {
		t.Fatalf("wait selector output = %+v, want matched selector", got)
	}
}

func TestWaitURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "url", "https://example.test/results", "--mode", "exact", "--poll", "100ms", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait url exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID    string `json:"id"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"target"`
		Wait struct {
			Kind         string         `json:"kind"`
			Needle       string         `json:"needle"`
			Condition    string         `json:"condition"`
			URL          string         `json:"url"`
			Title        string         `json:"title"`
			Matched      bool           `json:"matched"`
			Count        int            `json:"count"`
			PollInterval string         `json:"poll_interval"`
			Evidence     map[string]any `json:"evidence"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait url output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "page-1" || got.Target.URL != "https://example.test/results" || got.Target.Title != "Example App" || got.Wait.Kind != "url" || got.Wait.Needle != "https://example.test/results" || got.Wait.Condition != "exact" || got.Wait.URL != "https://example.test/results" || got.Wait.Title != "Example App" || !got.Wait.Matched || got.Wait.Count != 1 || got.Wait.PollInterval != "100ms" {
		t.Fatalf("wait url output = %+v, want matched exact URL evidence", got)
	}
	if got.Wait.Evidence["needle"] != "https://example.test/results" || got.Wait.Evidence["condition"] != "exact" || got.Wait.Evidence["url"] != "https://example.test/results" || got.Wait.Evidence["title"] != "Example App" || got.Wait.Evidence["matched"] != true {
		t.Fatalf("wait url evidence = %+v, want URL evidence", got.Wait.Evidence)
	}
}

func TestWaitURLDefaultContainsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "url", "results", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait url contains exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind      string `json:"kind"`
			Needle    string `json:"needle"`
			Condition string `json:"condition"`
			URL       string `json:"url"`
			Matched   bool   `json:"matched"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait url contains output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "url" || got.Wait.Needle != "results" || got.Wait.Condition != "contains" || !strings.Contains(got.Wait.URL, "results") || !got.Wait.Matched {
		t.Fatalf("wait url contains output = %+v, want default contains URL match", got)
	}
}

func TestWaitLocatorByRoleJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "locator", "Search", "--by", "role", "--role", "button", "--strict", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait locator exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind             string         `json:"kind"`
			By               string         `json:"by"`
			Query            string         `json:"query"`
			Role             string         `json:"role"`
			Strict           bool           `json:"strict"`
			Matched          bool           `json:"matched"`
			Count            int            `json:"count"`
			ResolvedSelector string         `json:"resolved_selector"`
			Condition        string         `json:"condition"`
			Evidence         map[string]any `json:"evidence"`
		} `json:"wait"`
		Locator struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Role    string `json:"role"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Role         string `json:"role"`
			} `json:"matches"`
		} `json:"locator"`
		Matches []struct {
			SelectorHint string `json:"selector_hint"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait locator output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "locator" || got.Wait.By != "role" || got.Wait.Query != "Search" || got.Wait.Role != "button" || !got.Wait.Strict || !got.Wait.Matched || got.Wait.Count != 1 {
		t.Fatalf("wait locator result = %+v, want strict matched role locator", got)
	}
	if got.Wait.ResolvedSelector != "button#submit" || !strings.Contains(got.Wait.Condition, "exactly one") || got.Wait.Evidence["by"] != "role" || got.Wait.Evidence["resolved_selector"] != "button#submit" {
		t.Fatalf("wait locator evidence = %+v, want resolved selector evidence", got.Wait)
	}
	if got.Locator.By != "role" || got.Locator.Query != "Search" || got.Locator.Role != "button" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || got.Locator.Matches[0].SelectorHint != "button#submit" || got.Locator.Matches[0].Role != "button" {
		t.Fatalf("top-level locator = %+v, want role locator metadata", got.Locator)
	}
	if len(got.Matches) != 1 || got.Matches[0].SelectorHint != "button#submit" {
		t.Fatalf("top-level matches = %+v, want jq-friendly locator matches", got.Matches)
	}
}

func TestWaitEvalJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "eval", "window.__rendered === true", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait eval exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind       string          `json:"kind"`
			Expression string          `json:"expression"`
			Matched    bool            `json:"matched"`
			Value      json.RawMessage `json:"value"`
			Condition  string          `json:"condition"`
			Evidence   map[string]any  `json:"evidence"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait eval output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "eval" || got.Wait.Expression != "window.__rendered === true" || !got.Wait.Matched || string(got.Wait.Value) != "true" || !strings.Contains(got.Wait.Condition, "__rendered") || got.Wait.Evidence["expression"] != "window.__rendered === true" {
		t.Fatalf("wait eval output = %+v, want matched eval", got)
	}
}

func TestWaitEvalRetriesTransientRuntimeFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false, "fakeRuntimeEvaluateErrorOnce": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "eval", "window.__rendered === true", "--retry", "transient", "--max-attempts", "2", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait eval retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK           bool   `json:"ok"`
		RetryPolicy  string `json:"retry_policy"`
		AttemptCount int    `json:"attempt_count"`
		Wait         struct {
			Kind         string `json:"kind"`
			Ready        bool   `json:"ready"`
			Matched      bool   `json:"matched"`
			AttemptCount int    `json:"attempt_count"`
		} `json:"wait"`
		Attempts []struct {
			Retry bool   `json:"retry"`
			Code  string `json:"code"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait eval retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.RetryPolicy != "transient" || got.AttemptCount != 2 || len(got.Attempts) != 2 || !got.Attempts[0].Retry || got.Attempts[0].Code != "connection_failed" || got.Wait.Kind != "eval" || !got.Wait.Ready || !got.Wait.Matched || got.Wait.AttemptCount == 0 {
		t.Fatalf("wait eval retry output = %+v, want runtime retry before ready result", got)
	}
}

func TestWaitEvalSemanticReadinessJSONAndArtifacts(t *testing.T) {
	fakeDelayedWaitEvalAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	outDir := t.TempDir()

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"wait", "eval", "window.__semanticState",
		"--ready-expr", `value.terminalCondition === "fare_rows"`,
		"--poll", "10ms",
		"--timeout", "1s",
		"--out-dir", outDir,
		"--artifact-prefix", "stage-ready",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait eval semantic exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind            string            `json:"kind"`
			Expression      string            `json:"expression"`
			ReadyExpression string            `json:"ready_expression"`
			Ready           bool              `json:"ready"`
			Matched         bool              `json:"matched"`
			AttemptCount    int               `json:"attempt_count"`
			PollInterval    string            `json:"poll_interval"`
			LastValue       json.RawMessage   `json:"last_value"`
			Attempts        []json.RawMessage `json:"attempts"`
			Artifacts       []struct {
				Type    string `json:"type"`
				Path    string `json:"path"`
				Attempt int    `json:"attempt"`
			} `json:"artifacts"`
		} `json:"wait"`
		Artifacts []struct {
			Path    string `json:"path"`
			Attempt int    `json:"attempt"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait eval semantic output is invalid JSON: %v", err)
	}
	var lastValue struct {
		TerminalCondition string `json:"terminalCondition"`
		RowCount          int    `json:"rowCount"`
	}
	if err := json.Unmarshal(got.Wait.LastValue, &lastValue); err != nil {
		t.Fatalf("wait eval semantic last_value is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "eval" || got.Wait.Expression != "window.__semanticState" || got.Wait.ReadyExpression != `value.terminalCondition === "fare_rows"` || !got.Wait.Ready || !got.Wait.Matched || got.Wait.AttemptCount < 3 || len(got.Wait.Attempts) != got.Wait.AttemptCount || got.Wait.PollInterval != "10ms" || lastValue.TerminalCondition != "fare_rows" || lastValue.RowCount != 12 {
		t.Fatalf("wait eval semantic output = %+v last=%+v, want ready semantic state after attempts", got.Wait, lastValue)
	}
	if len(got.Wait.Artifacts) != got.Wait.AttemptCount || len(got.Artifacts) != got.Wait.AttemptCount {
		t.Fatalf("wait eval semantic artifacts = wait:%d top:%d attempts:%d", len(got.Wait.Artifacts), len(got.Artifacts), got.Wait.AttemptCount)
	}
	for i, artifact := range got.Wait.Artifacts {
		if artifact.Type != "wait-eval-attempt" || artifact.Attempt != i+1 || !strings.HasPrefix(artifact.Path, outDir) {
			t.Fatalf("wait eval semantic artifact[%d] = %+v, want attempt artifact under out dir", i, artifact)
		}
		if _, err := os.Stat(artifact.Path); err != nil {
			t.Fatalf("wait eval semantic artifact[%d] was not written: %v", i, err)
		}
	}
}

func TestWaitEvalSemanticReadinessTimeoutIncludesLastValueJSON(t *testing.T) {
	fakeDelayedWaitEvalAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"wait", "eval", "window.__semanticNeverReady",
		"--ready-expr", `value.terminalCondition === "fare_rows"`,
		"--poll", "10ms",
		"--timeout", "40ms",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("wait eval semantic timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Wait struct {
				Kind            string          `json:"kind"`
				Expression      string          `json:"expression"`
				ReadyExpression string          `json:"ready_expression"`
				Ready           bool            `json:"ready"`
				Matched         bool            `json:"matched"`
				AttemptCount    int             `json:"attempt_count"`
				LastValue       json.RawMessage `json:"last_value"`
				Evidence        map[string]any  `json:"evidence"`
			} `json:"wait"`
		} `json:"data"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait eval semantic timeout output is invalid JSON: %v", err)
	}
	var lastValue struct {
		TerminalCondition string `json:"terminalCondition"`
		RowCount          int    `json:"rowCount"`
	}
	if err := json.Unmarshal(got.Data.Wait.LastValue, &lastValue); err != nil {
		t.Fatalf("wait eval semantic timeout last_value is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Wait.Kind != "eval" || got.Data.Wait.Expression != "window.__semanticNeverReady" || got.Data.Wait.ReadyExpression != `value.terminalCondition === "fare_rows"` || got.Data.Wait.Ready || got.Data.Wait.Matched || got.Data.Wait.AttemptCount == 0 || lastValue.TerminalCondition != "loading" || lastValue.RowCount != 0 {
		t.Fatalf("wait eval semantic timeout = %+v last=%+v, want timeout with last observed semantic state", got, lastValue)
	}
	if got.Data.Wait.Evidence["attempt_count"].(float64) == 0 || got.Data.Wait.Evidence["ready"].(bool) {
		t.Fatalf("wait eval semantic timeout evidence = %+v, want bounded not-ready evidence", got.Data.Wait.Evidence)
	}
	if !containsString(got.RemediationCommands, `cdp wait eval window.__semanticNeverReady --ready-expr 'value.terminalCondition === "fare_rows"' --timeout 15s --json`) {
		t.Fatalf("wait eval semantic timeout remediations = %+v, want predicate retry command", got.RemediationCommands)
	}
}

func TestWaitEvalClassifyStopStateJSON(t *testing.T) {
	fakeDelayedWaitEvalAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"wait", "eval", "window.__semanticNeverReady",
		"--ready-expr", `value.terminalCondition === "fare_rows"`,
		"--classify-stop-state",
		"--poll", "10ms",
		"--timeout", "1s",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("wait eval stop-state exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK              bool     `json:"ok"`
		Code            string   `json:"code"`
		ErrClass        string   `json:"err_class"`
		StopState       string   `json:"stop_state"`
		StopStateClass  string   `json:"stop_state_class"`
		AgentShouldStop bool     `json:"agent_should_stop"`
		HumanRequired   bool     `json:"human_required"`
		NextCommands    []string `json:"next_commands"`
		Data            struct {
			StopState      string `json:"stop_state"`
			StopStateClass string `json:"stop_state_class"`
			Wait           struct {
				Kind      string `json:"kind"`
				Matched   bool   `json:"matched"`
				StopState struct {
					StopState      string `json:"stop_state"`
					StopStateClass string `json:"stop_state_class"`
				} `json:"stop_state_result"`
			} `json:"wait"`
			StopStateResult struct {
				StopState      string `json:"stop_state"`
				StopStateClass string `json:"stop_state_class"`
			} `json:"stop_state_result"`
		} `json:"data"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait eval stop-state output is invalid JSON: %v; stdout=%s", err, out.String())
	}
	if got.OK || got.Code != "stop_state" || got.ErrClass != "auth" || got.StopState != "login_required" || got.StopStateClass != "auth" || !got.AgentShouldStop || !got.HumanRequired {
		t.Fatalf("wait eval stop-state envelope = %+v, want lifted login-required stop state", got)
	}
	if got.Data.StopState != "login_required" || got.Data.StopStateClass != "auth" || got.Data.StopStateResult.StopState != "login_required" || got.Data.Wait.StopState.StopState != "login_required" || got.Data.Wait.Kind != "eval" || got.Data.Wait.Matched {
		t.Fatalf("wait eval stop-state data = %+v, want stop evidence and not matched wait", got.Data)
	}
	if !containsString(got.NextCommands, "cdp --browser-mode headed daemon status --json") || !containsString(got.RemediationCommands, "cdp --browser-mode headed daemon status --json") {
		t.Fatalf("wait eval stop-state commands next=%v remediation=%v, want safe auth diagnostics", got.NextCommands, got.RemediationCommands)
	}
}

func TestWaitLoadStateJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "load-state", "domcontentloaded", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait load-state exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind         string         `json:"kind"`
			State        string         `json:"state"`
			ReadyState   string         `json:"ready_state"`
			Matched      bool           `json:"matched"`
			URL          string         `json:"url"`
			Title        string         `json:"title"`
			Condition    string         `json:"condition"`
			Evidence     map[string]any `json:"evidence"`
			PollInterval string         `json:"poll_interval"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait load-state output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "load-state" || got.Wait.State != "domcontentloaded" || got.Wait.ReadyState != "complete" || !got.Wait.Matched || got.Wait.URL != "https://example.test/app" || got.Wait.Title != "Example App" || !strings.Contains(got.Wait.Condition, "interactive or complete") || got.Wait.Evidence["ready_state"] != "complete" || got.Wait.PollInterval != "250ms" {
		t.Fatalf("wait load-state output = %+v, want matched DOMContentLoaded state", got)
	}
}

func TestWaitLoadStateTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "loading-page", "type": "page", "title": "Loading App", "url": "https://example.test/loading", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "load-state", "load", "--timeout", "1s", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("wait load-state timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Wait struct {
				Kind       string         `json:"kind"`
				State      string         `json:"state"`
				ReadyState string         `json:"ready_state"`
				Matched    bool           `json:"matched"`
				Condition  string         `json:"condition"`
				Evidence   map[string]any `json:"evidence"`
			} `json:"wait"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait load-state timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Wait.Kind != "load-state" || got.Data.Wait.State != "load" || got.Data.Wait.ReadyState != "loading" || got.Data.Wait.Matched || !strings.Contains(got.Data.Wait.Condition, "complete") || got.Data.Wait.Evidence["ready_state"] != "loading" {
		t.Fatalf("wait load-state timeout = %+v, want timeout with last loading state", got)
	}
	if !containsString(got.RemediationCommands, "cdp wait selector main --timeout 15s --json") {
		t.Fatalf("wait load-state remediation commands = %+v, want selector follow-up", got.RemediationCommands)
	}
}

func TestWaitLoadStateUnsupportedStateJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "load-state", "networkidle", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("wait load-state unsupported exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait load-state unsupported output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || !strings.Contains(got.Message, "load-state must be load or domcontentloaded") {
		t.Fatalf("wait load-state unsupported output = %+v, want usage error", got)
	}
}

func TestWaitTimeoutsFailClosedJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tests := []struct {
		name string
		args []string
	}{
		{"text", []string{"wait", "text", "Never Ready", "--timeout", "50ms", "--poll", "10ms", "--json"}},
		{"selector", []string{"wait", "selector", "missing", "--timeout", "50ms", "--poll", "10ms", "--json"}},
		{"eval", []string{"wait", "eval", "window.__never === true", "--timeout", "50ms", "--poll", "10ms", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitTimeout {
				t.Fatalf("wait timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
			}
			var got struct {
				OK   bool   `json:"ok"`
				Code string `json:"code"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("wait timeout output is invalid JSON: %v", err)
			}
			if got.OK || got.Code != "timeout" {
				t.Fatalf("wait timeout output = %+v, want fail-closed timeout", got)
			}
		})
	}
}

func TestEmulateNetworkJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"emulate", "network", "--preset", "fast-3g", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("emulate network exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Emulation struct {
			Preset  string `json:"preset"`
			Network struct {
				Offline            bool    `json:"offline"`
				Latency            int     `json:"latency"`
				DownloadThroughput float64 `json:"downloadThroughput"`
				UploadThroughput   float64 `json:"uploadThroughput"`
			} `json:"network"`
			CleanupCommand string `json:"cleanup_command"`
		} `json:"emulation"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("emulate network output is invalid JSON: %v", err)
	}
	if !got.OK || got.Emulation.Preset != "fast-3g" || got.Emulation.Network.Offline || got.Emulation.Network.Latency != 150 || got.Emulation.Network.DownloadThroughput != 200000 || got.Emulation.Network.UploadThroughput != 93750 || !strings.Contains(got.Emulation.CleanupCommand, "--preset none") {
		t.Fatalf("emulate network output = %+v, want fast-3g params and cleanup command", got)
	}
}

func TestEmulateNetworkCustomJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"emulate", "network", "--latency", "100", "--download-kbps", "750", "--upload-kbps", "250", "--offline", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("emulate network custom exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Emulation struct {
			Preset  string `json:"preset"`
			Network struct {
				Offline            bool    `json:"offline"`
				Latency            int     `json:"latency"`
				DownloadThroughput float64 `json:"downloadThroughput"`
				UploadThroughput   float64 `json:"uploadThroughput"`
			} `json:"network"`
		} `json:"emulation"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("emulate network custom output is invalid JSON: %v", err)
	}
	if !got.OK || got.Emulation.Preset != "custom" || !got.Emulation.Network.Offline || got.Emulation.Network.Latency != 100 || got.Emulation.Network.DownloadThroughput != 93750 || got.Emulation.Network.UploadThroughput != 31250 {
		t.Fatalf("emulate network custom output = %+v, want custom params with kbps converted to bytes/sec", got)
	}
}

func TestEmulateNetworkInvalidArgsJSON(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown preset", []string{"emulate", "network", "--preset", "bogus", "--json"}},
		{"negative latency", []string{"emulate", "network", "--latency", "-1", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("emulate network invalid exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
			}
		})
	}
}

func TestOpenJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"open", "https://example.test/feed", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("open exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Page   struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"page"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("open output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "created" || got.Page.ID != "created-page" || got.Page.URL != "https://example.test/feed" {
		t.Fatalf("open output = %+v, want created page", got)
	}
}

func TestOpenReuseURLFilterNavigatesExistingWithBudgetSummaryJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-flights", "type": "page", "title": "Flights", "url": "https://www.google.com/travel/flights", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"open", "https://example.test/reused", "--reuse", "--url-contains", "google.com/travel/flights", "--budget-summary", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("open reuse exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Action  string `json:"action"`
		Created bool   `json:"created"`
		Reused  bool   `json:"reused"`
		Page    struct {
			ID      string `json:"id"`
			URL     string `json:"url"`
			FrameID string `json:"frame_id"`
		} `json:"page"`
		Reuse struct {
			Requested       bool   `json:"requested"`
			Policy          string `json:"policy"`
			URLContains     string `json:"url_contains"`
			Matched         bool   `json:"matched"`
			FallbackCreated bool   `json:"fallback_created"`
			TargetID        string `json:"target_id"`
		} `json:"reuse"`
		TabBudget struct {
			Policy            string `json:"policy"`
			ReuseTarget       string `json:"reuse_target"`
			ManagedTabID      string `json:"managed_tab_id"`
			ManagedTabCreated bool   `json:"managed_tab_created"`
			CleanupStatus     string `json:"cleanup_status"`
			Before            struct {
				TabCount int `json:"tab_count"`
			} `json:"before"`
			After struct {
				TabCount int `json:"tab_count"`
			} `json:"after"`
		} `json:"tab_budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("open reuse output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "reused" || got.Created || !got.Reused || got.Page.ID != "page-flights" || got.Page.URL != "https://example.test/reused" || got.Page.FrameID != "frame-1" {
		t.Fatalf("open reuse output = %+v, want reused existing target", got)
	}
	if !got.Reuse.Requested || got.Reuse.Policy != "reuse_url_contains" || got.Reuse.URLContains != "google.com/travel/flights" || !got.Reuse.Matched || got.Reuse.FallbackCreated || got.Reuse.TargetID != "page-flights" {
		t.Fatalf("open reuse report = %+v, want matched URL reuse", got.Reuse)
	}
	if got.TabBudget.Policy != "reuse_url_contains" || got.TabBudget.ReuseTarget != "url:google.com/travel/flights" || got.TabBudget.ManagedTabID != "page-flights" || got.TabBudget.ManagedTabCreated || got.TabBudget.CleanupStatus != "skipped_reused_tab" || got.TabBudget.Before.TabCount != 1 || got.TabBudget.After.TabCount != 1 {
		t.Fatalf("open reuse tab budget = %+v, want before/after reused summary", got.TabBudget)
	}
}

func TestOpenReuseURLFilterFallsBackToCreatedWithBudgetSummaryJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-docs", "type": "page", "title": "Docs", "url": "https://docs.example.test/", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"open", "https://example.test/new", "--reuse", "--url-contains", "missing.example", "--budget-summary", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("open reuse fallback exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Action  string `json:"action"`
		Created bool   `json:"created"`
		Reused  bool   `json:"reused"`
		Page    struct {
			ID string `json:"id"`
		} `json:"page"`
		Reuse struct {
			Matched         bool `json:"matched"`
			FallbackCreated bool `json:"fallback_created"`
		} `json:"reuse"`
		TabBudget struct {
			ManagedTabID      string   `json:"managed_tab_id"`
			ManagedTabCreated bool     `json:"managed_tab_created"`
			CleanupStatus     string   `json:"cleanup_status"`
			CleanupCommands   []string `json:"cleanup_commands"`
			Before            struct {
				TabCount int `json:"tab_count"`
			} `json:"before"`
			After struct {
				TabCount int `json:"tab_count"`
			} `json:"after"`
		} `json:"tab_budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("open reuse fallback output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "created" || !got.Created || got.Reused || got.Page.ID != "created-page" || got.Reuse.Matched || !got.Reuse.FallbackCreated {
		t.Fatalf("open reuse fallback output = %+v, want created fallback", got)
	}
	if got.TabBudget.ManagedTabID != "created-page" || !got.TabBudget.ManagedTabCreated || got.TabBudget.CleanupStatus != "not_run" || got.TabBudget.Before.TabCount != 1 || got.TabBudget.After.TabCount != 2 || len(got.TabBudget.CleanupCommands) == 0 {
		t.Fatalf("open reuse fallback tab budget = %+v, want created before/after summary", got.TabBudget)
	}
}

func TestOpenRecordsRootAndChildTaskOwnershipJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"open", "https://example.test/root", "--run-id", "run-1", "--task-id", "task-root", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("root open exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var root struct {
		RunID         string            `json:"run_id"`
		TaskID        string            `json:"task_id"`
		RootTaskID    string            `json:"root_task_id"`
		ParentTaskID  string            `json:"parent_task_id"`
		CreatedBy     string            `json:"created_by"`
		TargetTaskIDs map[string]string `json:"target_task_ids"`
	}
	if err := json.Unmarshal(out.Bytes(), &root); err != nil {
		t.Fatalf("root open output is invalid JSON: %v", err)
	}
	if root.RunID != "run-1" || root.TaskID != "task-root" || root.RootTaskID != "task-root" || root.ParentTaskID != "" || root.CreatedBy != "cdp" || root.TargetTaskIDs["created-page"] != "task-root" {
		t.Fatalf("root ownership = %+v, want root task ownership for created-page", root)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"open", "https://example.test/child", "--run-id", "run-1", "--task-id", "task-child", "--root-task-id", "task-root", "--parent-task-id", "task-root", "--created-by", "gflights", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("child open exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var child struct {
		RunID         string            `json:"run_id"`
		TaskID        string            `json:"task_id"`
		RootTaskID    string            `json:"root_task_id"`
		ParentTaskID  string            `json:"parent_task_id"`
		CreatedBy     string            `json:"created_by"`
		TargetTaskIDs map[string]string `json:"target_task_ids"`
	}
	if err := json.Unmarshal(out.Bytes(), &child); err != nil {
		t.Fatalf("child open output is invalid JSON: %v", err)
	}
	if child.RunID != "run-1" || child.TaskID != "task-child" || child.RootTaskID != "task-root" || child.ParentTaskID != "task-root" || child.CreatedBy != "gflights" || child.TargetTaskIDs["created-page-2"] != "task-child" {
		t.Fatalf("child ownership = %+v, want child task ownership for created-page-2", child)
	}

	b, err := os.ReadFile(filepath.Join(stateDir, "page-cleanup.json"))
	if err != nil {
		t.Fatalf("read page cleanup state: %v", err)
	}
	var saved struct {
		Pages []struct {
			BrowserMode  string `json:"browser_mode"`
			Connection   string `json:"connection"`
			TargetID     string `json:"target_id"`
			CreatedBy    string `json:"created_by"`
			RunID        string `json:"run_id"`
			TaskID       string `json:"task_id"`
			RootTaskID   string `json:"root_task_id"`
			ParentTaskID string `json:"parent_task_id"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatalf("page cleanup state is invalid JSON: %v", err)
	}
	if len(saved.Pages) != 2 {
		t.Fatalf("saved pages = %+v, want root and child ownership records", saved.Pages)
	}
	if saved.Pages[0].BrowserMode != "headed" || saved.Pages[0].Connection != "default" || saved.Pages[0].TargetID != "created-page" || saved.Pages[0].RunID != "run-1" || saved.Pages[0].TaskID != "task-root" || saved.Pages[0].RootTaskID != "task-root" {
		t.Fatalf("root saved ownership = %+v, want mode-scoped root task record", saved.Pages[0])
	}
	if saved.Pages[1].BrowserMode != "headed" || saved.Pages[1].Connection != "default" || saved.Pages[1].TargetID != "created-page-2" || saved.Pages[1].CreatedBy != "gflights" || saved.Pages[1].RunID != "run-1" || saved.Pages[1].TaskID != "task-child" || saved.Pages[1].RootTaskID != "task-root" || saved.Pages[1].ParentTaskID != "task-root" {
		t.Fatalf("child saved ownership = %+v, want mode-scoped child task record", saved.Pages[1])
	}
}

func TestPageCleanupRootTaskClosesOnlyOwnedTargetsJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	commands := [][]string{
		{"open", "https://example.test/root", "--run-id", "run-1", "--task-id", "task-root", "--json"},
		{"open", "https://example.test/child", "--run-id", "run-1", "--task-id", "task-child", "--root-task-id", "task-root", "--parent-task-id", "task-root", "--json"},
		{"open", "https://example.test/unowned", "--json"},
	}
	for _, args := range commands {
		var out, errOut bytes.Buffer
		code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("%v exit code = %d, want %d; stdout=%s stderr=%s", args, code, cli.ExitOK, out.String(), errOut.String())
		}
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "cleanup", "--root-task-id", "task-root", "--idle-for", "0s", "--close", "--force", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("task cleanup exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		Cleanup struct {
			TaskScope         bool              `json:"task_scope"`
			RootTaskID        string            `json:"root_task_id"`
			ClosedCount       int               `json:"closed_count"`
			RecordCountBefore int               `json:"record_count_before"`
			RecordCountAfter  int               `json:"record_count_after"`
			TargetTaskIDs     map[string]string `json:"target_task_ids"`
		} `json:"cleanup"`
		Closed []struct {
			TaskID string `json:"task_id"`
			Target struct {
				ID string `json:"targetId"`
			} `json:"target"`
		} `json:"closed"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("task cleanup output is invalid JSON: %v", err)
	}
	if !got.Cleanup.TaskScope || got.Cleanup.RootTaskID != "task-root" || got.Cleanup.ClosedCount != 2 || got.Cleanup.RecordCountBefore != 3 || got.Cleanup.RecordCountAfter != 1 {
		t.Fatalf("task cleanup summary = %+v, want two owned targets closed and unowned record preserved", got.Cleanup)
	}
	if got.Cleanup.TargetTaskIDs["created-page"] != "task-root" || got.Cleanup.TargetTaskIDs["created-page-2"] != "task-child" {
		t.Fatalf("task cleanup target map = %+v, want root and child target ownership", got.Cleanup.TargetTaskIDs)
	}
	if len(got.Closed) != 2 || got.Closed[0].Target.ID != "created-page" || got.Closed[0].TaskID != "task-root" || got.Closed[1].Target.ID != "created-page-2" || got.Closed[1].TaskID != "task-child" {
		t.Fatalf("closed task targets = %+v, want root and child targets only", got.Closed)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"pages", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages after task cleanup exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var pages struct {
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &pages); err != nil {
		t.Fatalf("pages output is invalid JSON: %v", err)
	}
	if len(pages.Pages) != 1 || pages.Pages[0].ID != "created-page-3" {
		t.Fatalf("remaining pages = %+v, want only unowned target left", pages.Pages)
	}
}

func TestPageCleanupTaskScopeDoesNotInferMissingOwnershipJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-unrecorded", "type": "page", "title": "Unrecorded", "url": "https://example.test/unrecorded", "attached": false},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll state dir returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "page-cleanup.json"), nil, 0o600); err != nil {
		t.Fatalf("write empty cleanup state: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "cleanup", "--task-id", "task-missing", "--idle-for", "0s", "--force", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("task cleanup missing-state exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		Cleanup struct {
			TaskScope     bool     `json:"task_scope"`
			TaskID        string   `json:"task_id"`
			ReadyCount    int      `json:"ready_count"`
			StateWarnings []string `json:"state_warnings"`
		} `json:"cleanup"`
		Candidates []struct {
			Target struct {
				ID string `json:"targetId"`
			} `json:"target"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("task cleanup output is invalid JSON: %v", err)
	}
	if !got.Cleanup.TaskScope || got.Cleanup.TaskID != "task-missing" || got.Cleanup.ReadyCount != 0 || len(got.Candidates) != 0 || len(got.Cleanup.StateWarnings) != 1 || !strings.Contains(got.Cleanup.StateWarnings[0], "empty") {
		t.Fatalf("task cleanup missing-state = %+v candidates=%+v, want no inferred ownership and empty-state warning", got.Cleanup, got.Candidates)
	}
}

func TestOpenRetriesCreateTargetRaceJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "seed-page", "type": "page", "title": "Seed", "url": "about:blank", "attached": false, "fakeCreateTargetErrorOnce": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"open", "https://example.test/retry-open", "--retry", "transient", "--max-attempts", "2", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("open retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK           bool   `json:"ok"`
		Action       string `json:"action"`
		RetryPolicy  string `json:"retry_policy"`
		AttemptCount int    `json:"attempt_count"`
		MaxAttempts  int    `json:"max_attempts"`
		Page         struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"page"`
		Attempts []struct {
			Attempt int    `json:"attempt"`
			Retry   bool   `json:"retry"`
			Code    string `json:"code"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("open retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "created" || got.Page.ID != "created-page" || got.Page.URL != "https://example.test/retry-open" {
		t.Fatalf("open retry output = %+v, want created page", got)
	}
	if got.RetryPolicy != "transient" || got.AttemptCount != 2 || got.MaxAttempts != 2 || len(got.Attempts) != 2 || !got.Attempts[0].Retry || got.Attempts[0].Code != "connection_failed" {
		t.Fatalf("open retry attempts = %+v, want one transient retry before success", got)
	}
}

func TestEvalJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"eval", "document.title", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("eval exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Result struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("eval output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "page-1" || got.Result.Type != "string" || got.Result.Value != "Example App" {
		t.Fatalf("eval output = %+v, want document title result", got)
	}
}

func TestEvalTimeoutIsClassifiedAndSharedDaemonRemainsUsable(t *testing.T) {
	target := map[string]any{
		"targetId": "page-1",
		"type":     "page",
		"title":    "Example App",
		"url":      "https://example.test/app",
	}
	var server *httptest.Server
	var delayed atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/144.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": fakeWebSocketEndpoint(t, server.URL),
		})
	})
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			var req struct {
				ID        int64           `json:"id"`
				SessionID string          `json:"sessionId"`
				Method    string          `json:"method"`
				Params    json.RawMessage `json:"params"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			resp := map[string]any{"id": req.ID}
			if req.SessionID != "" {
				resp["sessionId"] = req.SessionID
			}
			switch req.Method {
			case "Target.getTargets":
				resp["result"] = map[string]any{"targetInfos": []map[string]any{target}}
			case "Target.attachToTarget":
				resp["result"] = map[string]any{"sessionId": "session-page-1"}
			case "Target.detachFromTarget":
				resp["result"] = map[string]any{}
			case "Runtime.evaluate":
				if delayed.CompareAndSwap(false, true) {
					time.Sleep(500 * time.Millisecond)
				}
				resp["result"] = map[string]any{
					"result": map[string]any{"type": "string", "value": "Example App"},
				}
			default:
				resp["result"] = map[string]any{}
			}
			if err := wsjson.Write(r.Context(), conn, resp); err != nil {
				return
			}
		}
	})
	server = httptest.NewServer(mux)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var timeoutOut, timeoutErrOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--timeout", "50ms", "eval", "document.title", "--json"}, &timeoutOut, &timeoutErrOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("timed eval exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, timeoutOut.String(), timeoutErrOut.String())
	}
	var timeoutResult struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Class   string `json:"err_class"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(timeoutOut.Bytes(), &timeoutResult); err != nil {
		t.Fatalf("timed eval output is invalid JSON: %v", err)
	}
	if timeoutResult.OK || timeoutResult.Code != "timeout" || timeoutResult.Class != "timeout" || !strings.Contains(timeoutResult.Message, "deadline") {
		t.Fatalf("timed eval result = %+v, want a classified timeout", timeoutResult)
	}

	var followupOut, followupErrOut bytes.Buffer
	code = cli.Execute(context.Background(), []string{"eval", "document.title", "--json"}, &followupOut, &followupErrOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("follow-up eval exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, followupOut.String(), followupErrOut.String())
	}
	var followupResult struct {
		OK     bool `json:"ok"`
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(followupOut.Bytes(), &followupResult); err != nil {
		t.Fatalf("follow-up eval output is invalid JSON: %v", err)
	}
	if !followupResult.OK || followupResult.Result.Value != "Example App" {
		t.Fatalf("follow-up eval result = %+v, want the shared daemon transport to remain usable", followupResult)
	}
}

func TestEvalRetriesAttachRaceJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false, "fakeAttachErrorOnce": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"eval", "document.title", "--retry", "transient", "--max-attempts", "2", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("eval attach retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK                 bool   `json:"ok"`
		RetryPolicy        string `json:"retry_policy"`
		AttemptCount       int    `json:"attempt_count"`
		LastObservedTarget struct {
			ID string `json:"id"`
		} `json:"last_observed_target"`
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		Attempts []struct {
			Retry bool   `json:"retry"`
			Code  string `json:"code"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("eval attach retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.Result.Value != "Example App" || got.RetryPolicy != "transient" || got.AttemptCount != 2 || len(got.Attempts) != 2 || !got.Attempts[0].Retry || got.Attempts[0].Code != "connection_failed" || got.LastObservedTarget.ID != "page-1" {
		t.Fatalf("eval attach retry output = %+v, want target evidence and one retry before success", got)
	}
}

func TestEvalRetriesExecutionContextLossJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false, "fakeRuntimeEvaluateErrorOnce": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"eval", "document.title", "--retry", "transient", "--max-attempts", "2", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("eval runtime retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK           bool   `json:"ok"`
		RetryPolicy  string `json:"retry_policy"`
		AttemptCount int    `json:"attempt_count"`
		Result       struct {
			Value string `json:"value"`
		} `json:"result"`
		Attempts []struct {
			Retry bool   `json:"retry"`
			Code  string `json:"code"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("eval runtime retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.Result.Value != "Example App" || got.RetryPolicy != "transient" || got.AttemptCount != 2 || len(got.Attempts) != 2 || !got.Attempts[0].Retry || got.Attempts[0].Code != "connection_failed" {
		t.Fatalf("eval runtime retry output = %+v, want execution-context retry before success", got)
	}
}

func TestEvalExactTargetIDSkipsTargetListing(t *testing.T) {
	var getTargetsCalled atomic.Bool
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/144.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": wsURL,
		})
	})
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			var req struct {
				ID        int64           `json:"id"`
				SessionID string          `json:"sessionId"`
				Method    string          `json:"method"`
				Params    json.RawMessage `json:"params"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			resp := map[string]any{"id": req.ID}
			if req.SessionID != "" {
				resp["sessionId"] = req.SessionID
			}
			switch req.Method {
			case "Target.getTargets":
				getTargetsCalled.Store(true)
				resp["error"] = map[string]any{"code": -32000, "message": "target list should not be requested"}
			case "Target.getTargetInfo":
				resp["result"] = map[string]any{"targetInfo": map[string]any{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app"}}
			case "Target.attachToTarget":
				resp["result"] = map[string]any{"sessionId": "session-1"}
			case "Target.detachFromTarget":
				resp["result"] = map[string]any{}
			case "Runtime.evaluate":
				resp["result"] = map[string]any{"result": map[string]any{"type": "string", "value": "Example App"}}
			default:
				resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
			}
			if err := wsjson.Write(r.Context(), conn, resp); err != nil {
				return
			}
		}
	})
	server = httptest.NewServer(mux)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"eval", "document.title", "--target", "page-1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("eval exact target exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	if getTargetsCalled.Load() {
		t.Fatalf("eval exact target called Target.getTargets; want Target.getTargetInfo direct attach")
	}
}

func TestConsoleJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"console", "--errors", "--wait", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("console exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Messages []struct {
			ID           int             `json:"id"`
			Source       string          `json:"source"`
			Type         string          `json:"type"`
			Level        string          `json:"level"`
			Text         string          `json:"text"`
			URL          string          `json:"url"`
			LineNumber   int             `json:"line_number"`
			ColumnNumber int             `json:"column_number"`
			ScriptID     string          `json:"script_id"`
			Exception    json.RawMessage `json:"exception"`
			StackTrace   json.RawMessage `json:"stack_trace"`
		} `json:"messages"`
		Console struct {
			Count      int  `json:"count"`
			ErrorsOnly bool `json:"errors_only"`
		} `json:"console"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("console output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "page-1" || got.Console.Count != 3 || !got.Console.ErrorsOnly {
		t.Fatalf("console output = %+v, want three error messages", got)
	}
	if got.Messages[0].ID != 0 || got.Messages[0].Source != "runtime" || got.Messages[0].Type != "error" || got.Messages[0].Text != "Synthetic console error" {
		t.Fatalf("first console message = %+v, want runtime error", got.Messages[0])
	}
	if got.Messages[1].Source != "runtime" || got.Messages[1].Type != "exception" || got.Messages[1].Text != "Uncaught (in promise): TypeError: failed to fetch dashboard" {
		t.Fatalf("second console message = %+v, want rich runtime exception", got.Messages[1])
	}
	if got.Messages[1].URL != "https://example.test/assets/app.js" || got.Messages[1].LineNumber != 41 || got.Messages[1].ColumnNumber != 9 || got.Messages[1].ScriptID != "script-1" {
		t.Fatalf("second console location = %+v, want script location", got.Messages[1])
	}
	if len(got.Messages[1].Exception) == 0 || !strings.Contains(string(got.Messages[1].Exception), "TypeError") {
		t.Fatalf("second console exception = %s, want serialized exception object", got.Messages[1].Exception)
	}
	if len(got.Messages[1].StackTrace) == 0 || !strings.Contains(string(got.Messages[1].StackTrace), "loadDashboard") {
		t.Fatalf("second console stack_trace = %s, want stack frames", got.Messages[1].StackTrace)
	}
	if got.Messages[2].Source != "network" || got.Messages[2].Level != "error" || got.Messages[2].Text != "Synthetic network failure" {
		t.Fatalf("third console message = %+v, want network log error", got.Messages[2])
	}
}

func TestSnapshotJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/feed", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"snapshot", "--selector", "article", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("snapshot exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Snapshot struct {
			Selector string `json:"selector"`
			Count    int    `json:"count"`
		} `json:"snapshot"`
		Items []struct {
			Tag  string `json:"tag"`
			Text string `json:"text"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("snapshot output is invalid JSON: %v", err)
	}
	if !got.OK || got.Snapshot.Selector != "article" || got.Snapshot.Count != 1 || len(got.Items) != 1 || got.Items[0].Text != "First visible synthetic post" {
		t.Fatalf("snapshot output = %+v, want one article item", got)
	}
}

func TestSnapshotEmptyDiagnosticsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/feed", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"snapshot", "--selector", "empty", "--debug-empty", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("snapshot exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK          bool                `json:"ok"`
		Warnings    []string            `json:"warnings"`
		Snapshot    struct{ Count int } `json:"snapshot"`
		Diagnostics struct {
			SelectorMatched   bool     `json:"selector_matched"`
			BodyTextLength    int      `json:"body_text_length"`
			FrameCount        int      `json:"frame_count"`
			PossibleCauses    []string `json:"possible_causes"`
			SuggestedCommands []string `json:"suggested_commands"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("snapshot output is invalid JSON: %v", err)
	}
	if !got.OK || got.Snapshot.Count != 0 || len(got.Warnings) == 0 || !got.Diagnostics.SelectorMatched || got.Diagnostics.BodyTextLength != 0 || got.Diagnostics.FrameCount != 2 {
		t.Fatalf("snapshot empty diagnostics = %+v, want empty extraction diagnostics", got)
	}
	if !containsString(got.Diagnostics.PossibleCauses, "shadow_dom") || !containsString(got.Diagnostics.SuggestedCommands, "cdp html body --target page-1 --diagnose-empty --json") {
		t.Fatalf("snapshot empty diagnostics = %+v, want causes and suggested commands", got.Diagnostics)
	}
}

func TestScreenshotJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/feed", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "shot.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"screenshot", "--out", outPath, "--full-page", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("screenshot exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Screenshot struct {
			Path     string `json:"path"`
			Bytes    int    `json:"bytes"`
			Format   string `json:"format"`
			FullPage bool   `json:"full_page"`
		} `json:"screenshot"`
		Artifacts []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("screenshot output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "page-1" || got.Screenshot.Path != outPath || got.Screenshot.Bytes != len("synthetic screenshot") || got.Screenshot.Format != "png" || !got.Screenshot.FullPage {
		t.Fatalf("screenshot output = %+v, want artifact metadata", got)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Type != "screenshot" || got.Artifacts[0].Path != outPath {
		t.Fatalf("screenshot artifacts = %+v, want screenshot artifact", got.Artifacts)
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(b) != "synthetic screenshot" {
		t.Fatalf("screenshot file = %q, want synthetic screenshot", string(b))
	}
}

func TestScreenshotPresetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/feed", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "mobile.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"screenshot", "--out", outPath, "--preset", "mobile", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("screenshot preset exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK         bool `json:"ok"`
		Screenshot struct {
			Path     string `json:"path"`
			FullPage bool   `json:"full_page"`
			Viewport struct {
				Preset            string  `json:"preset"`
				Width             int     `json:"width"`
				Height            int     `json:"height"`
				DeviceScaleFactor float64 `json:"device_scale_factor"`
				Mobile            bool    `json:"mobile"`
			} `json:"viewport"`
		} `json:"screenshot"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("screenshot preset output is invalid JSON: %v", err)
	}
	if !got.OK || got.Screenshot.Path != outPath || got.Screenshot.FullPage || got.Screenshot.Viewport.Preset != "mobile" || got.Screenshot.Viewport.Width != 390 || got.Screenshot.Viewport.Height != 844 || got.Screenshot.Viewport.DeviceScaleFactor != 3 || !got.Screenshot.Viewport.Mobile {
		t.Fatalf("screenshot preset output = %+v, want mobile viewport metadata", got)
	}
}

func TestScreenshotTileFullPageJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/feed", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outDir := filepath.Join(t.TempDir(), "tiles")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"screenshot", "--tile-full-page", "--out-dir", outDir, "--tile-height", "600", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("screenshot tile exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK         bool `json:"ok"`
		Screenshot struct {
			Path           string `json:"path"`
			Format         string `json:"format"`
			FullPage       bool   `json:"full_page"`
			TileFullPage   bool   `json:"tile_full_page"`
			StitchMode     string `json:"stitch_mode"`
			ManifestPath   string `json:"manifest_path"`
			TileCount      int    `json:"tile_count"`
			ContentHeight  int    `json:"content_height"`
			ViewportHeight int    `json:"viewport_height"`
			Tiles          []struct {
				Index int    `json:"index"`
				Path  string `json:"path"`
				Clip  struct {
					Y      float64 `json:"y"`
					Height float64 `json:"height"`
				} `json:"clip"`
			} `json:"tiles"`
		} `json:"screenshot"`
		Artifacts []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("screenshot tile output is invalid JSON: %v", err)
	}
	if !got.OK || got.Screenshot.Path == "" || got.Screenshot.ManifestPath != got.Screenshot.Path || got.Screenshot.Format != "png" || !got.Screenshot.FullPage || !got.Screenshot.TileFullPage || got.Screenshot.StitchMode != "none" {
		t.Fatalf("screenshot tile output = %+v, want manifest-backed full-page tile metadata", got.Screenshot)
	}
	if got.Screenshot.TileCount != 3 || got.Screenshot.ContentHeight != 1201 || got.Screenshot.ViewportHeight != 600 || len(got.Screenshot.Tiles) != 3 {
		t.Fatalf("screenshot tile metrics = %+v, want three tiles over 1201px content", got.Screenshot)
	}
	if got.Screenshot.Tiles[2].Clip.Y != 1200 || got.Screenshot.Tiles[2].Clip.Height != 1 {
		t.Fatalf("last tile = %+v, want one-extra-pixel coverage", got.Screenshot.Tiles[2])
	}
	if len(got.Artifacts) != 4 || got.Artifacts[0].Type != "screenshot-tile-manifest" {
		t.Fatalf("screenshot tile artifacts = %+v, want manifest plus three tile artifacts", got.Artifacts)
	}
	for _, artifact := range got.Artifacts {
		if _, err := os.Stat(artifact.Path); err != nil {
			t.Fatalf("artifact %s was not written: %v", artifact.Path, err)
		}
	}
}

func TestScreenshotRenderJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "diagram.html")
	if err := os.WriteFile(htmlPath, []byte("<main>ready</main>"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	outPath := filepath.Join(dir, "diagram.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"screenshot", "render", htmlPath, "--out", outPath, "--width", "800", "--height", "600", "--dpr", "2", "--wait-for", "window.__rendered === true", "--serve", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("screenshot render exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Render struct {
			Served   bool   `json:"served"`
			WaitFor  string `json:"wait_for"`
			Viewport struct {
				Width int `json:"width"`
			} `json:"viewport"`
		} `json:"render"`
		Screenshot struct {
			Path string `json:"path"`
		} `json:"screenshot"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("screenshot render output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Render.Served || got.Render.WaitFor != "window.__rendered === true" || got.Render.Viewport.Width != 800 || got.Screenshot.Path != outPath {
		t.Fatalf("screenshot render output = %+v, want render metadata", got)
	}
}

func TestEmulateUserAgentJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"emulate", "user-agent", "--user-agent", "AgentTest/1.0", "--platform", "Linux", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("emulate user-agent exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Emulation struct {
			UserAgent      string `json:"user_agent"`
			Platform       string `json:"platform"`
			CleanupCommand string `json:"cleanup_command"`
		} `json:"emulation"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("emulate user-agent output is invalid JSON: %v", err)
	}
	if !got.OK || got.Emulation.UserAgent != "AgentTest/1.0" || got.Emulation.Platform != "Linux" || !strings.Contains(got.Emulation.CleanupCommand, "cdp emulate clear") {
		t.Fatalf("emulate user-agent output = %+v, want applied override and cleanup command", got)
	}
}

func TestEmulateGeolocationJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"emulate", "geolocation", "--latitude", "55.6761", "--longitude", "12.5683", "--accuracy", "50", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("emulate geolocation exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Emulation struct {
			Geolocation struct {
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
				Accuracy  float64 `json:"accuracy"`
			} `json:"geolocation"`
			CleanupCommand string `json:"cleanup_command"`
		} `json:"emulation"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("emulate geolocation output is invalid JSON: %v", err)
	}
	if !got.OK || got.Emulation.Geolocation.Latitude != 55.6761 || got.Emulation.Geolocation.Longitude != 12.5683 || got.Emulation.Geolocation.Accuracy != 50 || !strings.Contains(got.Emulation.CleanupCommand, "cdp emulate clear") {
		t.Fatalf("emulate geolocation output = %+v, want applied override and cleanup command", got)
	}
}

func TestEmulateTimezoneJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"emulate", "timezone", "--timezone-id", "UTC", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("emulate timezone exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Emulation struct {
			Timezone struct {
				TimezoneID       string `json:"timezone_id"`
				ObservedTimezone string `json:"observed_timezone"`
				Verified         bool   `json:"verified"`
			} `json:"timezone"`
			CleanupCommand string `json:"cleanup_command"`
		} `json:"emulation"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("emulate timezone output is invalid JSON: %v", err)
	}
	if !got.OK || got.Emulation.Timezone.TimezoneID != "UTC" || got.Emulation.Timezone.ObservedTimezone != "UTC" || !got.Emulation.Timezone.Verified || !strings.Contains(got.Emulation.CleanupCommand, "cdp emulate clear") {
		t.Fatalf("emulate timezone output = %+v, want applied timezone override and cleanup command", got)
	}
}

func TestEmulateLocaleJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"emulate", "locale", "--locale", "de-DE", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("emulate locale exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Emulation struct {
			Locale struct {
				Locale         string `json:"locale"`
				ObservedLocale string `json:"observed_locale"`
				Verified       bool   `json:"verified"`
			} `json:"locale"`
			CleanupCommand string `json:"cleanup_command"`
		} `json:"emulation"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("emulate locale output is invalid JSON: %v", err)
	}
	if !got.OK || got.Emulation.Locale.Locale != "de-DE" || got.Emulation.Locale.ObservedLocale != "de-DE" || !got.Emulation.Locale.Verified || !strings.Contains(got.Emulation.CleanupCommand, "cdp emulate clear") {
		t.Fatalf("emulate locale output = %+v, want applied locale override and cleanup command", got)
	}
}

func TestEmulateColorSchemeJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"emulate", "color-scheme", "--scheme", "dark", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("emulate color-scheme exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Emulation struct {
			ColorScheme struct {
				Scheme         string              `json:"scheme"`
				ObservedScheme string              `json:"observed_scheme"`
				Verified       bool                `json:"verified"`
				MediaFeatures  []map[string]string `json:"media_features"`
			} `json:"color_scheme"`
			CleanupCommand string `json:"cleanup_command"`
		} `json:"emulation"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("emulate color-scheme output is invalid JSON: %v", err)
	}
	if !got.OK || got.Emulation.ColorScheme.Scheme != "dark" || got.Emulation.ColorScheme.ObservedScheme != "dark" || !got.Emulation.ColorScheme.Verified || len(got.Emulation.ColorScheme.MediaFeatures) != 1 || !strings.Contains(got.Emulation.CleanupCommand, "cdp emulate clear") {
		t.Fatalf("emulate color-scheme output = %+v, want applied color-scheme override and cleanup command", got)
	}
}

func TestEmulateCPUJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"emulate", "cpu", "--rate", "4", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("emulate cpu exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Emulation struct {
			CPU struct {
				Rate float64 `json:"rate"`
			} `json:"cpu"`
			CleanupCommand string `json:"cleanup_command"`
		} `json:"emulation"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("emulate cpu output is invalid JSON: %v", err)
	}
	if !got.OK || got.Emulation.CPU.Rate != 4 || !strings.Contains(got.Emulation.CleanupCommand, "--rate 1") {
		t.Fatalf("emulate cpu output = %+v, want applied override and cleanup command", got)
	}
}

func TestEmulateClearJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"emulate", "clear", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("emulate clear exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Emulation struct {
			ClearedOverrides []string `json:"cleared_overrides"`
		} `json:"emulation"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("emulate clear output is invalid JSON: %v", err)
	}
	if !got.OK || !containsString(got.Emulation.ClearedOverrides, "network") || !containsString(got.Emulation.ClearedOverrides, "timezone") || !containsString(got.Emulation.ClearedOverrides, "locale") || !containsString(got.Emulation.ClearedOverrides, "media") {
		t.Fatalf("emulate clear output = %+v, want network, timezone, locale, and media cleared", got)
	}
}

func TestShotElementNavJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/feed", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "element.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"screenshot", "--out", outPath, "--element", "main", "--navigate", "https://example.test/next", "--wait", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("screenshot element exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK         bool `json:"ok"`
		Screenshot struct {
			Element  string `json:"element"`
			Navigate struct {
				URL string `json:"url"`
			} `json:"navigate"`
			Clip struct {
				Width float64 `json:"width"`
			} `json:"clip"`
		} `json:"screenshot"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("screenshot element output is invalid JSON: %v", err)
	}
	if !got.OK || got.Screenshot.Element != "main" || got.Screenshot.Navigate.URL != "https://example.test/next" || got.Screenshot.Clip.Width <= 0 {
		t.Fatalf("screenshot element output = %+v, want element metadata", got)
	}
}

func TestPagesTitleContainsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Docs Home", "url": "https://example.test/docs", "attached": false},
		{"targetId": "page-2", "type": "page", "title": "Admin", "url": "https://example.test/admin", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--title-contains", "docs", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Pages []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages output is invalid JSON: %v", err)
	}
	if len(got.Pages) != 1 || got.Pages[0].ID != "page-1" || got.Pages[0].Title != "Docs Home" {
		t.Fatalf("pages output = %+v, want Docs Home only", got.Pages)
	}
}

func TestEvalTitleContainsSelectsPage(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Admin", "url": "https://example.test/app", "attached": false},
		{"targetId": "page-2", "type": "page", "title": "Docs Portal", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"eval", "document.title", "--title-contains", "portal", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("eval exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Target struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("eval output is invalid JSON: %v", err)
	}
	if got.Target.ID != "page-2" || got.Target.Title != "Docs Portal" {
		t.Fatalf("eval target = %+v, want Docs Portal page", got.Target)
	}
}
