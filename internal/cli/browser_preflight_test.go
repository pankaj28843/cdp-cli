package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestBrowserPreflightHealthyJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"browser", "preflight", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("browser preflight exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK          bool   `json:"ok"`
		BrowserMode string `json:"browser_mode"`
		State       string `json:"state"`
		Status      string `json:"status"`
		Health      struct {
			State  string `json:"state"`
			Usable bool   `json:"usable"`
		} `json:"health"`
		Budget struct {
			TabCount int `json:"tab_count"`
		} `json:"budget"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("browser preflight output is invalid JSON: %v", err)
	}
	if !got.OK || got.BrowserMode != "headed" || got.State != "healthy" || got.Status != "pass" || got.Health.State != "healthy" || !got.Health.Usable || got.Budget.TabCount != 1 {
		t.Fatalf("browser preflight = %+v, want healthy headed runtime with budget", got)
	}
	if !containsString(got.NextCommands, "cdp --browser-mode headed daemon health --json") {
		t.Fatalf("next_commands = %+v, want mode-scoped daemon health", got.NextCommands)
	}
}

func TestBrowserPreflightReportsUsableDegradedJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")
	daemon.AppendLogForMode(context.Background(), stateDir, "headed", daemon.LogEntry{
		Time:    "2026-06-11T16:50:02Z",
		Level:   "warn",
		Event:   "hold_connection_ended",
		Message: "failed to read JSON message: failed to get reader: use of closed network connection",
	})

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"browser", "preflight", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("browser preflight degraded exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK              bool     `json:"ok"`
		State           string   `json:"state"`
		Status          string   `json:"status"`
		Warnings        []string `json:"warnings"`
		DegradedReasons []string `json:"degraded_reasons"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("browser preflight degraded output is invalid JSON: %v", err)
	}
	if !got.OK || got.State != "usable_degraded" || got.Status != "warn" || !containsString(got.DegradedReasons, "recent_keepalive_read_error") || len(got.Warnings) == 0 {
		t.Fatalf("browser preflight degraded = %+v, want usable warning with degraded reason", got)
	}
}

func TestBrowserPreflightOverBudgetFailsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example One", "url": "https://example.test/one", "attached": false},
		{"targetId": "page-2", "type": "page", "title": "Example Two", "url": "https://example.test/two", "attached": false},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--max-tabs", "1", "browser", "preflight", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("browser preflight over-budget exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}
	var got struct {
		OK    bool   `json:"ok"`
		Code  string `json:"code"`
		State string `json:"state"`
		Data  struct {
			State  string `json:"state"`
			Budget struct {
				TabCount       int  `json:"tab_count"`
				MaxTabs        int  `json:"max_tabs"`
				TabsOverBudget bool `json:"tabs_over_budget"`
			} `json:"budget"`
		} `json:"data"`
		ResourceBudget struct {
			TabCount       int  `json:"tab_count"`
			TabsOverBudget bool `json:"tabs_over_budget"`
		} `json:"resource_budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("browser preflight over-budget output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "browser_resource_budget_exceeded" || got.State != "over_budget" || got.Data.State != "over_budget" || got.Data.Budget.TabCount != 2 || got.Data.Budget.MaxTabs != 1 || !got.Data.Budget.TabsOverBudget || got.ResourceBudget.TabCount != 2 || !got.ResourceBudget.TabsOverBudget {
		t.Fatalf("browser preflight over-budget = %+v, want budget failure envelope", got)
	}
}

func TestBrowserPreflightPermissionPendingJSON(t *testing.T) {
	stateDir := shortCLIStateDir(t)
	userDataDir := filepath.Join(t.TempDir(), "chrome-profile")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--auto-connect", "--user-data-dir", userDataDir, "browser", "preflight", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitPermission {
		t.Fatalf("browser preflight permission exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitPermission, out.String(), errOut.String())
	}
	var got struct {
		OK              bool     `json:"ok"`
		Code            string   `json:"code"`
		State           string   `json:"state"`
		HumanRequired   bool     `json:"human_required"`
		AgentShouldStop bool     `json:"agent_should_stop"`
		SafeDiagnostics []string `json:"safe_diagnostics"`
		Data            struct {
			State         string `json:"state"`
			HumanAction   string `json:"human_action"`
			HumanRequired bool   `json:"human_required"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("browser preflight permission output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "permission_pending" || got.State != "permission_pending" || !got.HumanRequired || !got.AgentShouldStop || got.Data.State != "permission_pending" || !got.Data.HumanRequired || got.Data.HumanAction == "" || len(got.SafeDiagnostics) == 0 {
		t.Fatalf("browser preflight permission = %+v, want human-required permission envelope", got)
	}
}

func TestBrowserPreflightHeadlessRepairUsesKeepaliveJSON(t *testing.T) {
	stateDir := shortCLIStateDir(t)
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	endpoint := fakeWebSocketEndpoint(t, server.URL)
	port := writeManagedActivePortForEndpoint(t, stateDir, endpoint)
	profileDir := browser.ManagedProfileDir(stateDir)
	managedStatus := browser.ManagedStatus{
		BrowserMode:         "headless",
		ChromePID:           os.Getpid(),
		UserDataDir:         profileDir,
		DebuggingPort:       port,
		ProfileSeedStrategy: "managed",
	}
	if err := browser.SaveManagedMetadata(stateDir, browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           os.Getpid(),
		StartedAt:           time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		UserDataDir:         profileDir,
		DebuggingPort:       port,
		ProfileSeedStrategy: "managed",
		LastSeededAt:        time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", daemon.Runtime{
		PID:                 exitedProcessPID(t),
		StartedAt:           time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		BrowserMode:         "headless",
		ConnectionMode:      "browser_url",
		ReconnectInterval:   "30s",
		SocketPath:          daemon.RuntimeSocketPathForMode(stateDir, "headless"),
		Endpoint:            endpoint,
		ManagedBrowser:      &managedStatus,
		ManagedProfilePath:  profileDir,
		ProfileSeedStrategy: "managed",
		ChromePID:           os.Getpid(),
		ChromePort:          port,
	}); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "browser", "preflight", "--repair", "--chrome-command", "", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("browser preflight repair exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		State  string `json:"state"`
		Repair struct {
			RepairSource   string `json:"repair_source"`
			PreviousState  string `json:"previous_state"`
			Classification string `json:"classification"`
			State          string `json:"state"`
			Action         string `json:"action"`
		} `json:"repair"`
		Actions []struct {
			State  string `json:"state"`
			Action string `json:"action"`
		} `json:"repair_actions"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("browser preflight repair output is invalid JSON: %v\n%s", err, out.String())
	}
	if !got.OK || got.State != "healthy" || got.Repair.RepairSource != "daemon_keepalive" || got.Repair.PreviousState != "stale_state" || got.Repair.Classification != "headless_daemon_not_running" || got.Repair.State != "repaired" || got.Repair.Action != "repaired" || len(got.Actions) != 1 || got.Actions[0].State != "repaired" {
		t.Fatalf("browser preflight repair = %+v, want keepalive-backed repair evidence", got)
	}
}

func TestBrowserPreflightOpenReadinessJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"browser", "preflight", "--open-readiness", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("browser preflight open-readiness exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK        bool   `json:"ok"`
		State     string `json:"state"`
		Readiness struct {
			OK             bool `json:"ok"`
			Closed         bool `json:"closed"`
			AttemptCount   int  `json:"attempt_count"`
			ReadinessState struct {
				ReadyState     string `json:"ready_state"`
				BodyTextLength int    `json:"body_text_length"`
			} `json:"readiness_state"`
			Target struct {
				ID string `json:"id"`
			} `json:"target"`
		} `json:"readiness"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("browser preflight open-readiness output is invalid JSON: %v", err)
	}
	if !got.OK || got.State != "healthy" || !got.Readiness.OK || !got.Readiness.Closed || got.Readiness.AttemptCount != 1 || got.Readiness.ReadinessState.ReadyState != "complete" || got.Readiness.ReadinessState.BodyTextLength == 0 || got.Readiness.Target.ID == "" {
		t.Fatalf("browser preflight open-readiness = %+v, want neutral readiness evidence", got)
	}
}

func TestBrowserPreflightCleanupDryRunAndCloseJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-visible", "type": "page", "title": "Visible Page", "url": "https://example.test/visible", "attached": false},
		{"targetId": "page-hidden", "type": "page", "title": "Hidden Page", "url": "https://example.test/hidden", "attached": false},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"browser", "preflight", "--cleanup", "--include-url", "example.test", "--cleanup-idle-for", "0s", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("browser preflight cleanup dry-run exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}
	var dryRun struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			State   string `json:"state"`
			Cleanup struct {
				Cleanup struct {
					DryRun        bool `json:"dry_run"`
					CloseRequired bool `json:"close_required"`
					ReadyCount    int  `json:"ready_count"`
					ClosedCount   int  `json:"closed_count"`
				} `json:"cleanup"`
			} `json:"cleanup"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &dryRun); err != nil {
		t.Fatalf("browser preflight cleanup dry-run output is invalid JSON: %v", err)
	}
	if dryRun.OK || dryRun.Code != "browser_preflight_cleanup_required" || dryRun.Data.State != "cleanup_required" || !dryRun.Data.Cleanup.Cleanup.DryRun || !dryRun.Data.Cleanup.Cleanup.CloseRequired || dryRun.Data.Cleanup.Cleanup.ReadyCount != 1 || dryRun.Data.Cleanup.Cleanup.ClosedCount != 0 {
		t.Fatalf("browser preflight cleanup dry-run = %+v, want dry-run cleanup-required gate", dryRun)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"browser", "preflight", "--cleanup", "--cleanup-close", "--include-url", "example.test", "--cleanup-idle-for", "0s", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("browser preflight cleanup close exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var closed struct {
		OK      bool   `json:"ok"`
		State   string `json:"state"`
		Cleanup struct {
			Cleanup struct {
				DryRun        bool `json:"dry_run"`
				CloseRequired bool `json:"close_required"`
				ClosedCount   int  `json:"closed_count"`
			} `json:"cleanup"`
			Closed []struct {
				Target struct {
					ID string `json:"targetId"`
				} `json:"target"`
			} `json:"closed"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &closed); err != nil {
		t.Fatalf("browser preflight cleanup close output is invalid JSON: %v", err)
	}
	if !closed.OK || closed.State != "healthy" || closed.Cleanup.Cleanup.DryRun || closed.Cleanup.Cleanup.CloseRequired || closed.Cleanup.Cleanup.ClosedCount != 1 || len(closed.Cleanup.Closed) != 1 || !strings.Contains(closed.Cleanup.Closed[0].Target.ID, "hidden") {
		t.Fatalf("browser preflight cleanup close = %+v, want one closed hidden candidate", closed)
	}
}
