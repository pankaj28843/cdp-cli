package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestDaemonStatusJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"daemon", "status", "--state-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Daemon struct {
			State          string   `json:"state"`
			BrowserMode    string   `json:"browser_mode"`
			ConnectionMode string   `json:"connection_mode"`
			NextCommands   []string `json:"next_commands"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon status output is invalid JSON: %v", err)
	}
	if !got.OK || got.Daemon.State != "not_running" || got.Daemon.BrowserMode != "headed" || got.Daemon.ConnectionMode != "browser_url" || !containsString(got.Daemon.NextCommands, "cdp daemon start --help") {
		t.Fatalf("daemon status = %+v, want not_running headed browser_url", got)
	}
}

func TestDaemonStatusReportsRuntimeJSON(t *testing.T) {
	stateDir := t.TempDir()
	socketPath := filepath.Join(stateDir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer listener.Close()

	if err := daemon.SaveRuntime(context.Background(), stateDir, daemon.Runtime{
		PID:               os.Getpid(),
		StartedAt:         time.Now().UTC().Format(time.RFC3339),
		ConnectionMode:    "auto_connect",
		ReconnectInterval: "30s",
		SocketPath:        socketPath,
	}); err != nil {
		t.Fatalf("SaveRuntime returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "status", "--auto-connect", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon status exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Daemon struct {
			State          string `json:"state"`
			ProcessRunning bool   `json:"process_running"`
			Runtime        struct {
				PID         int    `json:"pid"`
				BrowserMode string `json:"browser_mode"`
			} `json:"runtime"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon status output is invalid JSON: %v", err)
	}
	if got.Daemon.State != "running" || !got.Daemon.ProcessRunning || got.Daemon.Runtime.PID != os.Getpid() || got.Daemon.Runtime.BrowserMode != "headed" {
		t.Fatalf("daemon status = %+v, want running current pid headed runtime", got.Daemon)
	}
}

func TestDaemonHealthClassifiesRuntimeSocketUnreadyJSON(t *testing.T) {
	stateDir := t.TempDir()
	socketPath := daemon.RuntimeSocketPathForMode(stateDir, "headless")
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", daemon.Runtime{
		PID:            os.Getpid(),
		StartedAt:      time.Now().UTC().Format(time.RFC3339),
		BrowserMode:    "headless",
		ConnectionMode: "browser_url",
		SocketPath:     socketPath,
	}); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("daemon health exit code = %d, want %d; stderr=%s", code, cli.ExitCheckFailed, errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		Code   string `json:"code"`
		State  string `json:"state"`
		Daemon struct {
			State              string `json:"state"`
			ProcessRunning     bool   `json:"process_running"`
			RuntimeSocketReady bool   `json:"runtime_socket_ready"`
		} `json:"daemon"`
		Health struct {
			State        string   `json:"state"`
			Code         string   `json:"code"`
			Reasons      []string `json:"reasons"`
			DaemonRPC    bool     `json:"daemon_rpc_ready"`
			NextCommands []string `json:"next_commands"`
		} `json:"health"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon health output is invalid JSON: %v", err)
	}
	if got.Daemon.State != "runtime_socket_unready" || !got.Daemon.ProcessRunning || got.Daemon.RuntimeSocketReady {
		t.Fatalf("daemon = %+v, want live process with unready runtime socket", got.Daemon)
	}
	if got.OK || got.Code != "headless_daemon_rpc_not_ready" || got.State != "degraded" || got.Health.State != "degraded" || got.Health.Code != "headless_daemon_rpc_not_ready" || got.Health.DaemonRPC || !containsString(got.Health.Reasons, "daemon_socket_unready") {
		t.Fatalf("health = %+v, envelope code=%s state=%s ok=%v; want degraded daemon_rpc_not_ready", got.Health, got.Code, got.State, got.OK)
	}
	if !containsString(got.Health.NextCommands, "cdp --browser-mode headless daemon keepalive --repair --json") {
		t.Fatalf("next_commands = %+v, want headless keepalive repair", got.Health.NextCommands)
	}
}

func TestDaemonHealthReportsRecentCrashLogsJSON(t *testing.T) {
	stateDir := t.TempDir()
	logPath := daemon.RuntimeLogPathForMode(stateDir, "headless")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatalf("MkdirAll log dir returned error: %v", err)
	}
	entries := []string{
		`{"time":"2026-06-05T00:00:00Z","level":"info","event":"runtime_saved","message":"daemon runtime state saved","pid":101}`,
		`{"time":"2026-06-05T00:00:01Z","level":"warn","event":"hold_connection_ended","message":"failed to get reader: failed to read frame header: EOF","pid":101}`,
		`{"time":"2026-06-05T00:00:02Z","level":"error","event":"rpc_listen_failed","message":"listen daemon rpc socket: bind failed","pid":102}`,
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(entries, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile log returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("daemon health exit code = %d, want %d; stderr=%s", code, cli.ExitCheckFailed, errOut.String())
	}
	var got struct {
		Health struct {
			CrashCapture              string `json:"crash_capture"`
			RecentLogWarnings         int    `json:"recent_log_warnings"`
			RecentLogErrors           int    `json:"recent_log_errors"`
			LastBrowserKeepaliveError string `json:"last_browser_keepalive_error"`
			RecentCrashes             []struct {
				Type    string `json:"type"`
				Event   string `json:"event"`
				Level   string `json:"level"`
				Message string `json:"message"`
				PID     int    `json:"pid"`
			} `json:"recent_crashes"`
		} `json:"health"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon health output is invalid JSON: %v", err)
	}
	if got.Health.CrashCapture != "daemon_logs" || got.Health.RecentLogWarnings != 1 || got.Health.RecentLogErrors != 1 || len(got.Health.RecentCrashes) != 2 {
		t.Fatalf("health log summary = %+v, want daemon_logs capture with warning/error crash entries", got.Health)
	}
	if got.Health.RecentCrashes[0].Type != "browser_connection_ended" || got.Health.RecentCrashes[0].Event != "hold_connection_ended" || got.Health.RecentCrashes[0].PID != 101 {
		t.Fatalf("first recent crash = %+v, want hold connection ended classification", got.Health.RecentCrashes[0])
	}
	if got.Health.RecentCrashes[1].Type != "daemon_rpc_listen_failed" || got.Health.RecentCrashes[1].Event != "rpc_listen_failed" || got.Health.RecentCrashes[1].PID != 102 {
		t.Fatalf("second recent crash = %+v, want rpc listen failed classification", got.Health.RecentCrashes[1])
	}
	if !strings.Contains(got.Health.LastBrowserKeepaliveError, "hold_connection_ended") {
		t.Fatalf("last_browser_keepalive_error = %q, want hold_connection_ended summary", got.Health.LastBrowserKeepaliveError)
	}
}

func TestDaemonHealthReportsUsableDegradedKeepaliveReadErrorJSON(t *testing.T) {
	stateDir := shortCLIStateDir(t)
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()

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
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	daemon.AppendLogForMode(context.Background(), stateDir, "headless", daemon.LogEntry{
		Time:    "2026-06-11T16:50:02Z",
		Level:   "warn",
		Event:   "hold_connection_ended",
		Message: "failed to read JSON message: failed to get reader: use of closed network connection",
	})

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon health exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Health struct {
			State                     string   `json:"state"`
			Usable                    bool     `json:"usable"`
			DegradedReasons           []string `json:"degraded_reasons"`
			LastBrowserKeepaliveError string   `json:"last_browser_keepalive_error"`
			RecommendedRepair         struct {
				Command string `json:"command"`
				Urgency string `json:"urgency"`
			} `json:"recommended_repair"`
		} `json:"health"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon health output is invalid JSON: %v", err)
	}
	if !got.OK || got.Health.State != "degraded" || !got.Health.Usable || !containsString(got.Health.DegradedReasons, "recent_keepalive_read_error") {
		t.Fatalf("daemon health = %+v, want usable degraded keepalive read error", got.Health)
	}
	if !strings.Contains(got.Health.LastBrowserKeepaliveError, "hold_connection_ended") {
		t.Fatalf("last_browser_keepalive_error = %q, want hold_connection_ended", got.Health.LastBrowserKeepaliveError)
	}
	if got.Health.RecommendedRepair.Command != "cdp --browser-mode headless daemon health-check --repair --json" || got.Health.RecommendedRepair.Urgency != "before_long_crawl" {
		t.Fatalf("recommended_repair = %+v, want health-check before long crawl", got.Health.RecommendedRepair)
	}
}

func TestDaemonMaintenanceDryRunContractJSON(t *testing.T) {
	stateDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"headless":{"profile_seed_strategy":"copy-default","profile_refresh_after":"30m"}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"--browser-mode", "headless",
		"--state-dir", stateDir,
		"--config", configPath,
		"daemon", "maintenance",
		"--dry-run",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon maintenance dry-run exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK            bool   `json:"ok"`
		SchemaVersion string `json:"schema_version"`
		BrowserMode   string `json:"browser_mode"`
		State         string `json:"state"`
		Status        string `json:"status"`
		Action        string `json:"action"`
		DryRun        bool   `json:"dry_run"`
		Options       struct {
			ProfileSeedStrategy           string `json:"profile_seed_strategy"`
			ProfileSeedIfOlderThan        string `json:"profile_seed_if_older_than"`
			ProfileSeedIfOlderThanSeconds int64  `json:"profile_seed_if_older_than_seconds"`
			Reconnect                     string `json:"reconnect"`
			Repair                        bool   `json:"repair"`
			Force                         bool   `json:"force"`
			Cleanup                       bool   `json:"cleanup"`
			CleanupClose                  bool   `json:"cleanup_close"`
			CleanupMax                    int    `json:"cleanup_max"`
		} `json:"options"`
		Phases []struct {
			Order         int    `json:"order"`
			Name          string `json:"name"`
			Status        string `json:"status"`
			Required      bool   `json:"required"`
			Mutates       bool   `json:"mutates"`
			HeavyWork     bool   `json:"heavy_work"`
			ResourceGated bool   `json:"resource_gated"`
			Command       string `json:"command"`
			ArtifactKey   string `json:"artifact_key"`
		} `json:"phases"`
		Artifacts struct {
			Summary string `json:"summary"`
		} `json:"artifacts"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon maintenance dry-run output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || got.SchemaVersion != "cdp-headless-maintenance/v1" || got.BrowserMode != "headless" || got.State != "planned" || got.Status != "dry_run" || got.Action != "planned" || !got.DryRun {
		t.Fatalf("daemon maintenance dry-run = %+v, want planned dry-run contract", got)
	}
	if got.Options.ProfileSeedStrategy != "copy-default" || got.Options.ProfileSeedIfOlderThan != "30m" || got.Options.ProfileSeedIfOlderThanSeconds != 1800 || got.Options.Reconnect != "30s" || !got.Options.Repair || !got.Options.Force || !got.Options.Cleanup || !got.Options.CleanupClose || got.Options.CleanupMax != 25 {
		t.Fatalf("maintenance options = %+v, want cron-safe defaults with configured copy-default seed cadence", got.Options)
	}
	wantPhases := []string{
		"acquire_lock",
		"managed_process_sweep",
		"resource_preflight",
		"profile_seed",
		"daemon_keepalive",
		"daemon_health_check",
		"page_cleanup",
		"write_artifact",
	}
	if len(got.Phases) != len(wantPhases) {
		t.Fatalf("maintenance phases = %+v, want %d phases", got.Phases, len(wantPhases))
	}
	for i, want := range wantPhases {
		if got.Phases[i].Order != i+1 || got.Phases[i].Name != want || got.Phases[i].Status != "planned" {
			t.Fatalf("phase[%d] = %+v, want %s order %d planned", i, got.Phases[i], want, i+1)
		}
	}
	if !got.Phases[1].Required || !got.Phases[1].Mutates {
		t.Fatalf("managed process sweep phase = %+v, want required mutating phase before launch work", got.Phases[1])
	}
	if !got.Phases[3].HeavyWork || !got.Phases[3].ResourceGated || !strings.Contains(got.Phases[3].Command, "browser profile seed --strategy copy-default --if-older-than 30m") {
		t.Fatalf("profile seed phase = %+v, want resource-gated copy-default seed command", got.Phases[3])
	}
	if !strings.Contains(got.Phases[4].Command, "daemon keepalive --managed-process-sweep --repair --force") {
		t.Fatalf("keepalive phase command = %q, want managed-process sweep repair command", got.Phases[4].Command)
	}
	wantSummary := filepath.Join(stateDir, "headless-maintenance", "latest.json")
	if got.Artifacts.Summary != wantSummary {
		t.Fatalf("summary artifact path = %q, want %q", got.Artifacts.Summary, wantSummary)
	}
	if _, err := os.Stat(wantSummary); !os.IsNotExist(err) {
		t.Fatalf("dry-run summary artifact stat err = %v, want artifact not written", err)
	}
	if !containsString(got.NextCommands, "cdp --browser-mode headless daemon maintenance --json") || !containsString(got.NextCommands, "cdp cron install --json") {
		t.Fatalf("next_commands = %+v, want maintenance and cron install commands", got.NextCommands)
	}
}

func TestDaemonMaintenanceDescribeJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "daemon maintenance", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe daemon maintenance exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK       bool `json:"ok"`
		Commands struct {
			Name     string `json:"name"`
			Short    string `json:"short"`
			Examples []string
			Flags    []struct {
				Name string `json:"name"`
			} `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("describe daemon maintenance output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || got.Commands.Name != "maintenance" || !strings.Contains(got.Commands.Short, "unattended managed headless maintenance") {
		t.Fatalf("describe daemon maintenance = %+v, want maintenance command metadata", got.Commands)
	}
	if !hasExampleContaining(got.Commands.Examples, "daemon maintenance --dry-run --json") || !hasExampleContaining(got.Commands.Examples, "daemon maintenance --json") {
		t.Fatalf("daemon maintenance examples = %+v, want dry-run and run examples", got.Commands.Examples)
	}
	if !flagInfoContains(got.Commands.Flags, "dry-run") || !flagInfoContains(got.Commands.Flags, "profile-seed-strategy") || !flagInfoContains(got.Commands.Flags, "cleanup-close") {
		t.Fatalf("daemon maintenance flags = %+v, want dry-run/profile-seed-strategy/cleanup-close", got.Commands.Flags)
	}
}

func flagInfoContains(flags []struct {
	Name string `json:"name"`
}, want string) bool {
	for _, flag := range flags {
		if flag.Name == want {
			return true
		}
	}
	return false
}

func TestDaemonHealthCheckClearsKeepaliveReadErrorDegradationJSON(t *testing.T) {
	stateDir := shortCLIStateDir(t)
	artifactDir := filepath.Join(stateDir, "health-artifacts")
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()

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
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	runtime, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless")
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeForMode headless ok=%v err=%v, want runtime", ok, err)
	}
	runtime.ManagedProfilePath = browser.ManagedProfileDir(stateDir)
	runtime.ProfileSeedStrategy = "managed"
	runtime.ChromePort = "9222"
	runtime.ManagedBrowser = &browser.ManagedStatus{BrowserMode: "headless", UserDataDir: browser.ManagedProfileDir(stateDir), DebuggingPort: "9222", ProfileSeedStrategy: "managed"}
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", runtime); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	daemon.AppendLogForMode(context.Background(), stateDir, "headless", daemon.LogEntry{
		Time:    "2026-06-11T16:50:02Z",
		Level:   "warn",
		Event:   "hold_connection_ended",
		Message: "failed to read JSON message: failed to get reader: use of closed network connection",
	})

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon health before health-check exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var before struct {
		Health struct {
			State           string   `json:"state"`
			Usable          bool     `json:"usable"`
			DegradedReasons []string `json:"degraded_reasons"`
		} `json:"health"`
	}
	if err := json.Unmarshal(out.Bytes(), &before); err != nil {
		t.Fatalf("daemon health before health-check output is invalid JSON: %v", err)
	}
	if before.Health.State != "degraded" || !before.Health.Usable || !containsString(before.Health.DegradedReasons, "recent_keepalive_read_error") {
		t.Fatalf("daemon health before health-check = %+v, want usable keepalive degradation", before.Health)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health-check", "--require-healthy", "--state-dir", stateDir, "--out-dir", artifactDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("daemon health-check --require-healthy exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health-check", "--state-dir", stateDir, "--out-dir", artifactDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon health-check exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var check struct {
		OK                bool     `json:"ok"`
		State             string   `json:"state"`
		Status            string   `json:"status"`
		Usable            bool     `json:"usable"`
		DegradedReasons   []string `json:"degraded_reasons"`
		RecommendedAction string   `json:"recommended_action"`
	}
	if err := json.Unmarshal(out.Bytes(), &check); err != nil {
		t.Fatalf("daemon health-check output is invalid JSON: %v", err)
	}
	if !check.OK || check.State != "usable_degraded" || check.Status != "warn" || !check.Usable || !containsString(check.DegradedReasons, "recent_keepalive_read_error") || check.RecommendedAction != "safe_for_single_command_but_repair_before_long_crawl" {
		t.Fatalf("daemon health-check = %+v, want usable degraded warning success", check)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon health after health-check exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var after struct {
		Health struct {
			State           string   `json:"state"`
			Usable          bool     `json:"usable"`
			DegradedReasons []string `json:"degraded_reasons"`
		} `json:"health"`
	}
	if err := json.Unmarshal(out.Bytes(), &after); err != nil {
		t.Fatalf("daemon health after health-check output is invalid JSON: %v", err)
	}
	if after.Health.State != "healthy" || !after.Health.Usable || containsString(after.Health.DegradedReasons, "recent_keepalive_read_error") {
		t.Fatalf("daemon health after health-check = %+v, want cleared keepalive degradation", after.Health)
	}
}

func TestDaemonHealthCheckHeadlessHealthyJSON(t *testing.T) {
	stateDir := shortCLIStateDir(t)
	artifactDir := filepath.Join(stateDir, "health-artifacts")
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()

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
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	runtime, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless")
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeForMode headless ok=%v err=%v, want runtime", ok, err)
	}
	runtime.ManagedProfilePath = browser.ManagedProfileDir(stateDir)
	runtime.ProfileSeedStrategy = "managed"
	runtime.ChromePort = "9222"
	runtime.ManagedBrowser = &browser.ManagedStatus{BrowserMode: "headless", UserDataDir: browser.ManagedProfileDir(stateDir), DebuggingPort: "9222", ProfileSeedStrategy: "managed"}
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", runtime); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health-check", "--state-dir", stateDir, "--out-dir", artifactDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon health-check exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK          bool   `json:"ok"`
		State       string `json:"state"`
		Action      string `json:"action"`
		BrowserMode string `json:"browser_mode"`
		Health      struct {
			State string `json:"state"`
		} `json:"health"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Steps []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"steps"`
		Artifacts struct {
			RunDir     string `json:"run_dir"`
			Summary    string `json:"summary"`
			Screenshot string `json:"screenshot"`
		} `json:"artifacts"`
		FailureCount int `json:"failure_count"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon health-check output is invalid JSON: %v", err)
	}
	if !got.OK || got.State != "healthy" || got.Action != "validated" || got.BrowserMode != "headless" || got.Health.State != "healthy" || got.Target.ID == "" || got.FailureCount != 0 {
		t.Fatalf("daemon health-check = %+v, want healthy validated headless result", got)
	}
	for _, want := range []string{"health", "open", "javascript", "dom_text", "screenshot"} {
		found := false
		for _, step := range got.Steps {
			if step.Name == want && step.OK {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("steps = %+v, missing successful %s step", got.Steps, want)
		}
	}
	for _, path := range []string{got.Artifacts.RunDir, got.Artifacts.Summary, got.Artifacts.Screenshot} {
		if path == "" {
			t.Fatalf("artifacts = %+v, want populated paths", got.Artifacts)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("artifact %s missing: %v", path, err)
		}
	}
}

func TestDaemonHealthCheckRepairUsesKeepaliveForStaleHeadlessRuntime(t *testing.T) {
	stateDir := shortCLIStateDir(t)
	artifactDir := filepath.Join(stateDir, "health-artifacts")
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
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health-check", "--repair", "--state-dir", stateDir, "--out-dir", artifactDir, "--chrome-command", "", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon health-check repair exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		State  string `json:"state"`
		Action string `json:"action"`
		Repair struct {
			RepairSource   string `json:"repair_source"`
			PreviousState  string `json:"previous_state"`
			Classification string `json:"classification"`
			State          string `json:"state"`
			Action         string `json:"action"`
			Keepalive      struct {
				State    string `json:"state"`
				Action   string `json:"action"`
				Previous struct {
					State string `json:"state"`
				} `json:"previous"`
				Chrome struct {
					Running bool `json:"running"`
				} `json:"chrome"`
			} `json:"keepalive"`
		} `json:"repair"`
		Steps []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon health-check repair output is invalid JSON: %v\n%s", err, out.String())
	}
	if !got.OK || got.State != "healthy" || got.Action != "validated" {
		t.Fatalf("daemon health-check repair = %+v, want healthy validated result", got)
	}
	if got.Repair.RepairSource != "daemon_keepalive" || got.Repair.PreviousState != "stale_state" || got.Repair.Classification != "headless_daemon_not_running" || got.Repair.State != "repaired" || got.Repair.Action != "repaired" {
		t.Fatalf("repair = %+v, want daemon_keepalive repair from stale runtime", got.Repair)
	}
	if got.Repair.Keepalive.State != "repaired" || got.Repair.Keepalive.Action != "repaired" || got.Repair.Keepalive.Previous.State != "stale_state" || !got.Repair.Keepalive.Chrome.Running {
		t.Fatalf("keepalive repair = %+v, want reused managed Chrome and repaired daemon", got.Repair.Keepalive)
	}
	foundRepair := false
	for _, step := range got.Steps {
		if step.Name == "repair" && step.OK {
			foundRepair = true
			break
		}
	}
	if !foundRepair {
		t.Fatalf("steps = %+v, missing successful repair step", got.Steps)
	}
}

func TestDaemonStatusUsesSelectedBrowserModeRuntime(t *testing.T) {
	stateDir := filepath.Join(os.TempDir(), "cdp-cli-mode-runtime-test")
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatalf("RemoveAll state dir returned error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll state dir returned error: %v", err)
	}
	headedSocketPath := filepath.Join(stateDir, "daemon.sock")
	headedListener, err := net.Listen("unix", headedSocketPath)
	if err != nil {
		t.Fatalf("Listen headed returned error: %v", err)
	}
	defer headedListener.Close()
	headlessSocketPath := daemon.RuntimeSocketPathForMode(stateDir, "headless")
	if err := os.MkdirAll(filepath.Dir(headlessSocketPath), 0o700); err != nil {
		t.Fatalf("MkdirAll headless socket dir returned error: %v", err)
	}
	headlessListener, err := net.Listen("unix", headlessSocketPath)
	if err != nil {
		t.Fatalf("Listen headless returned error: %v", err)
	}
	defer headlessListener.Close()

	if err := daemon.SaveRuntime(context.Background(), stateDir, daemon.Runtime{PID: os.Getpid(), BrowserMode: "headed", ConnectionMode: "browser_url", SocketPath: headedSocketPath}); err != nil {
		t.Fatalf("SaveRuntime headed returned error: %v", err)
	}
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", daemon.Runtime{PID: os.Getpid(), BrowserMode: "headless", ConnectionMode: "browser_url", SocketPath: headlessSocketPath}); err != nil {
		t.Fatalf("SaveRuntimeForMode headless returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "status", "--browser-mode", "headless", "--browser-url", "http://localhost/devtools", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon status headless exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Daemon struct {
			State        string   `json:"state"`
			BrowserMode  string   `json:"browser_mode"`
			NextCommands []string `json:"next_commands"`
			Runtime      struct {
				BrowserMode string `json:"browser_mode"`
				SocketPath  string `json:"socket_path"`
			} `json:"runtime"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon status headless output is invalid JSON: %v", err)
	}
	if got.Daemon.State != "running" || got.Daemon.BrowserMode != "headless" || got.Daemon.Runtime.BrowserMode != "headless" || got.Daemon.Runtime.SocketPath != headlessSocketPath || !containsString(got.Daemon.NextCommands, "cdp --browser-mode headless daemon stop --json") {
		t.Fatalf("daemon status headless = %+v, want headless runtime and next commands", got.Daemon)
	}
}

func TestHeadedBrowserModeIgnoresSelectedHeadlessConnection(t *testing.T) {
	stateDir := filepath.Join(os.TempDir(), "cdp-cli-headed-mode-selected-headless-test")
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatalf("RemoveAll state dir returned error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll state dir returned error: %v", err)
	}
	socketPath := filepath.Join(stateDir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen headed returned error: %v", err)
	}
	defer listener.Close()
	if err := daemon.SaveRuntime(context.Background(), stateDir, daemon.Runtime{PID: os.Getpid(), BrowserMode: "headed", ConnectionMode: "browser_url", SocketPath: socketPath}); err != nil {
		t.Fatalf("SaveRuntime headed returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "connection", "add", "headless", "--browser-url", "http://headless.test/devtools", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headed", "daemon", "status", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon status exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Daemon struct {
			State          string `json:"state"`
			BrowserMode    string `json:"browser_mode"`
			ConnectionMode string `json:"connection_mode"`
			Runtime        struct {
				BrowserMode    string `json:"browser_mode"`
				ConnectionMode string `json:"connection_mode"`
			} `json:"runtime"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon status output is invalid JSON: %v; output=%s", err, out.String())
	}
	if got.Daemon.State != "running" || got.Daemon.BrowserMode != "headed" || got.Daemon.ConnectionMode != "browser_url" || got.Daemon.Runtime.BrowserMode != "headed" || got.Daemon.Runtime.ConnectionMode != "browser_url" {
		t.Fatalf("daemon status = %+v, want headed runtime despite selected headless connection", got.Daemon)
	}
}

func TestManagedHeadlessRuntimeOverridesSelectedAutoConnect(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := shortCLIStateDir(t)
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
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	runtime, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless")
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeForMode headless ok=%v err=%v, want runtime", ok, err)
	}
	runtime.ManagedProfilePath = browser.ManagedProfileDir(stateDir)
	runtime.ProfileSeedStrategy = "managed"
	runtime.ChromePort = "9222"
	managed := browser.ManagedStatus{BrowserMode: "headless", UserDataDir: browser.ManagedProfileDir(stateDir), DebuggingPort: "9222", ProfileSeedStrategy: "managed"}
	runtime.ManagedBrowser = &managed
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", runtime); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "default", "--auto-connect", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon health exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var health struct {
		Daemon struct {
			State          string `json:"state"`
			ConnectionMode string `json:"connection_mode"`
			Runtime        struct {
				BrowserMode    string `json:"browser_mode"`
				ConnectionMode string `json:"connection_mode"`
			} `json:"runtime"`
			BrowserProbe struct {
				State          string `json:"state"`
				ConnectionMode string `json:"connection_mode"`
			} `json:"browser_probe"`
		} `json:"daemon"`
		Health struct {
			State          string `json:"state"`
			ConnectionMode string `json:"connection_mode"`
		} `json:"health"`
	}
	if err := json.Unmarshal(out.Bytes(), &health); err != nil {
		t.Fatalf("daemon health output is invalid JSON: %v", err)
	}
	if health.Daemon.State != "running" || health.Daemon.ConnectionMode != "browser_url" || health.Daemon.Runtime.BrowserMode != "headless" || health.Health.State != "healthy" || health.Health.ConnectionMode != "browser_url" {
		t.Fatalf("daemon health = %+v, want healthy managed headless browser-url runtime despite selected auto_connect", health)
	}
	if health.Daemon.BrowserProbe.State != "cdp_available" || health.Daemon.BrowserProbe.ConnectionMode != "browser_url" {
		t.Fatalf("daemon health browser probe = %+v, want mode-runtime browser_url probe", health.Daemon.BrowserProbe)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "status", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon status exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var status struct {
		Daemon struct {
			State          string `json:"state"`
			ConnectionMode string `json:"connection_mode"`
			Runtime        struct {
				BrowserMode    string `json:"browser_mode"`
				ConnectionMode string `json:"connection_mode"`
			} `json:"runtime"`
			BrowserProbe struct {
				State          string `json:"state"`
				ConnectionMode string `json:"connection_mode"`
			} `json:"browser_probe"`
			Health struct {
				State          string `json:"state"`
				ConnectionMode string `json:"connection_mode"`
			} `json:"health"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatalf("daemon status output is invalid JSON: %v", err)
	}
	if status.Daemon.State != "running" || status.Daemon.ConnectionMode != "browser_url" || status.Daemon.Runtime.BrowserMode != "headless" || status.Daemon.Runtime.ConnectionMode != "browser_url" || status.Daemon.BrowserProbe.State != "cdp_available" || status.Daemon.BrowserProbe.ConnectionMode != "browser_url" || status.Daemon.Health.State != "healthy" {
		t.Fatalf("daemon status = %+v, want reconciled managed headless runtime without selected auto_connect contradiction", status.Daemon)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "pages", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s stdout=%s", code, cli.ExitOK, errOut.String(), out.String())
	}
}

func TestDaemonHealthKeepsReadyHeadlessHealthyWhenLauncherPIDExited(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := shortCLIStateDir(t)
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
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	deadChromePID := exitedProcessPID(t)
	runtime, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless")
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeForMode headless ok=%v err=%v, want runtime", ok, err)
	}
	runtime.ManagedProfilePath = browser.ManagedProfileDir(stateDir)
	runtime.ProfileSeedStrategy = "managed"
	runtime.ChromePID = deadChromePID
	runtime.ChromePort = "9222"
	managed := browser.ManagedStatus{BrowserMode: "headless", ChromePID: deadChromePID, UserDataDir: browser.ManagedProfileDir(stateDir), DebuggingPort: "9222", ProfileSeedStrategy: "managed"}
	runtime.ManagedBrowser = &managed
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", runtime); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "health", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon health exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Health struct {
			State                string   `json:"state"`
			Reasons              []string `json:"reasons"`
			NextCommands         []string `json:"next_commands"`
			ManagedBrowserHealth struct {
				Expected  bool   `json:"expected"`
				State     string `json:"state"`
				Running   bool   `json:"running"`
				ChromePID int    `json:"chrome_pid"`
			} `json:"managed_browser_health"`
		} `json:"health"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon health output is invalid JSON: %v", err)
	}
	if got.Health.State != "healthy" || containsString(got.Health.Reasons, "managed_chrome_process_not_running") {
		t.Fatalf("daemon health = %+v, want healthy daemon RPC despite exited launcher PID", got.Health)
	}
	if !got.Health.ManagedBrowserHealth.Expected || got.Health.ManagedBrowserHealth.State != "daemon_rpc_ready_pid_not_running" || got.Health.ManagedBrowserHealth.Running || got.Health.ManagedBrowserHealth.ChromePID != deadChromePID {
		t.Fatalf("managed_browser_health = %+v, want diagnostic launcher PID classification", got.Health.ManagedBrowserHealth)
	}
	if containsString(got.Health.NextCommands, "cdp --browser-mode headless daemon keepalive --repair --json") {
		t.Fatalf("next_commands = %+v, should not recommend repair while daemon RPC is healthy", got.Health.NextCommands)
	}
}

func TestDaemonStopNotRunningJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "stop", "--state-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon stop exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK      bool `json:"ok"`
		Stopped bool `json:"stopped"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon stop output is invalid JSON: %v", err)
	}
	if !got.OK || got.Stopped {
		t.Fatalf("daemon stop = %+v, want ok not stopped", got)
	}
}

func TestDaemonStopHeadlessReportsManagedOwnershipJSON(t *testing.T) {
	stateDir := t.TempDir()
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           123456,
		StartedAt:           "2026-05-21T12:00:00Z",
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		DebuggingPort:       "9222",
		ProfileSeedStrategy: "managed",
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "stop", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon stop exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK                    bool   `json:"ok"`
		BrowserMode           string `json:"browser_mode"`
		DaemonStopped         bool   `json:"daemon_stopped"`
		ManagedBrowserStopped bool   `json:"managed_browser_stopped"`
		ManagedBrowser        struct {
			Checked bool   `json:"checked"`
			Skipped bool   `json:"skipped"`
			Reason  string `json:"reason"`
			Browser struct {
				DebuggingPort string `json:"debugging_port"`
			} `json:"browser"`
		} `json:"managed_browser"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon stop output is invalid JSON: %v", err)
	}
	if !got.OK || got.BrowserMode != "headless" || got.DaemonStopped || got.ManagedBrowserStopped || !got.ManagedBrowser.Checked || !got.ManagedBrowser.Skipped || got.ManagedBrowser.Reason == "" || got.ManagedBrowser.Browser.DebuggingPort != "9222" {
		t.Fatalf("daemon stop = %+v, want headless managed ownership checked and skipped safely", got)
	}
	if strings.Contains(out.String(), "ownership_token") || strings.Contains(out.String(), "process_start_time") {
		t.Fatalf("daemon stop leaked internal managed ownership metadata: %s", out.String())
	}
}

func TestDaemonStartBrowserURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()

	stateDir := t.TempDir()
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "start", "--browser-url", server.URL, "--connection-name", "local", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon start exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Daemon struct {
			State          string `json:"state"`
			ConnectionMode string `json:"connection_mode"`
		} `json:"daemon"`
		Start struct {
			ConnectionSaved bool   `json:"connection_saved"`
			ConnectionName  string `json:"connection_name"`
			Keepalive       bool   `json:"keepalive_started"`
		} `json:"start"`
		Connection struct {
			Name       string `json:"name"`
			Mode       string `json:"mode"`
			BrowserURL string `json:"browser_url"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon start output is invalid JSON: %v", err)
	}
	if !got.OK || got.Daemon.State != "running" || got.Daemon.ConnectionMode != "browser_url" || !got.Start.ConnectionSaved || got.Start.ConnectionName != "local" || !got.Start.Keepalive {
		t.Fatalf("daemon start = %+v, want running saved browser-url keepalive connection", got)
	}
	if got.Connection.Name != "local" || got.Connection.Mode != "browser_url" || got.Connection.BrowserURL != server.URL {
		t.Fatalf("daemon start connection = %+v, want saved local browser-url", got.Connection)
	}
}

func TestDaemonKeepaliveStartsBrowserURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()

	stateDir := t.TempDir()
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "keepalive", "--browser-url", server.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK          bool   `json:"ok"`
		BrowserMode string `json:"browser_mode"`
		State       string `json:"state"`
		Action      string `json:"action"`
		Daemon      struct {
			State string `json:"state"`
		} `json:"daemon"`
		Start struct {
			Keepalive bool `json:"keepalive_started"`
		} `json:"start"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v", err)
	}
	if !got.OK || got.BrowserMode != "headed" || got.State != "started" || got.Action != "started" || got.Daemon.State != "running" || !got.Start.Keepalive {
		t.Fatalf("daemon keepalive = %+v, want started running daemon", got)
	}
}

func TestDaemonKeepaliveHealthyJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "keepalive", "--browser-url", server.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK          bool   `json:"ok"`
		BrowserMode string `json:"browser_mode"`
		State       string `json:"state"`
		Action      string `json:"action"`
		Daemon      struct {
			State string `json:"state"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v", err)
	}
	if !got.OK || got.BrowserMode != "headed" || got.State != "healthy" || got.Action != "none" || got.Daemon.State != "running" {
		t.Fatalf("daemon keepalive = %+v, want healthy running daemon", got)
	}
}

func TestDaemonKeepaliveHeadlessDoesNotRepairWhenDaemonRPCReadyAndLauncherPIDExited(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := shortCLIStateDir(t)
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
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	deadChromePID := exitedProcessPID(t)
	runtimeState, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless")
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeForMode headless ok=%v err=%v, want runtime", ok, err)
	}
	runtimeState.ManagedProfilePath = browser.ManagedProfileDir(stateDir)
	runtimeState.ProfileSeedStrategy = "managed"
	runtimeState.ChromePID = deadChromePID
	runtimeState.ChromePort = "9222"
	managed := browser.ManagedStatus{BrowserMode: "headless", ChromePID: deadChromePID, UserDataDir: browser.ManagedProfileDir(stateDir), DebuggingPort: "9222", ProfileSeedStrategy: "managed"}
	runtimeState.ManagedBrowser = &managed
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", runtimeState); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "keepalive", "--managed-process-sweep", "--repair", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		State  string `json:"state"`
		Action string `json:"action"`
		Health struct {
			Result              string `json:"result"`
			ManagedProcessSweep struct {
				Checked   bool   `json:"checked"`
				State     string `json:"state"`
				LiveCount int    `json:"live_count"`
			} `json:"managed_process_sweep"`
			ManagedBrowserHealth struct {
				State          string `json:"state"`
				DaemonRPCReady bool   `json:"daemon_rpc_ready"`
			} `json:"managed_browser_health"`
		} `json:"health"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v\n%s", err, out.String())
	}
	if !got.OK || got.State != "healthy" || got.Action != "none" || got.Health.Result != "target_list_ok" {
		t.Fatalf("daemon keepalive = %+v, want healthy/no repair when daemon RPC works", got)
	}
	if got.Health.ManagedBrowserHealth.State != "daemon_rpc_ready_pid_not_running" || !got.Health.ManagedBrowserHealth.DaemonRPCReady {
		t.Fatalf("managed browser health = %+v, want launcher PID diagnostic only", got.Health.ManagedBrowserHealth)
	}
	if !got.Health.ManagedProcessSweep.Checked || got.Health.ManagedProcessSweep.State != "healthy" || got.Health.ManagedProcessSweep.LiveCount != 0 {
		t.Fatalf("managed process sweep = %+v, want executed healthy sweep", got.Health.ManagedProcessSweep)
	}
}

func TestDaemonKeepaliveRepairsHeadedFromStaleApprovedEndpointWithoutActiveProbe(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := shortCLIStateDir(t)
	userDataDir := t.TempDir()
	staleRuntime := daemon.Runtime{
		PID:               999999999,
		StartedAt:         time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		BrowserMode:       "headed",
		ConnectionMode:    "auto_connect",
		ReconnectInterval: "30s",
		SocketPath:        daemon.RuntimeSocketPathForMode(stateDir, "headed"),
		Endpoint:          fakeWebSocketEndpoint(t, server.URL),
		UserDataDir:       userDataDir,
	}
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headed", staleRuntime); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"--browser-mode", "headed", "daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headed", "daemon", "keepalive", "--auto-connect", "--user-data-dir", userDataDir, "--repair", "--probe", "passive", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stderr=%s stdout=%s", code, cli.ExitOK, errOut.String(), out.String())
	}
	var got struct {
		OK           bool   `json:"ok"`
		BrowserMode  string `json:"browser_mode"`
		State        string `json:"state"`
		Action       string `json:"action"`
		RepairSource string `json:"repair_source"`
		Probe        struct {
			Mode   string `json:"mode"`
			Result string `json:"result"`
		} `json:"probe"`
		Previous struct {
			State string `json:"state"`
		} `json:"previous"`
		Daemon struct {
			State   string `json:"state"`
			Runtime struct {
				ConnectionMode string `json:"connection_mode"`
			} `json:"runtime"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v\n%s", err, out.String())
	}
	if !got.OK || got.BrowserMode != "headed" || got.State != "repaired" || got.Action != "repaired" || got.RepairSource != "stale_runtime_endpoint" {
		t.Fatalf("daemon keepalive = %+v, want repaired from stale runtime endpoint", got)
	}
	if got.Probe.Mode != "passive" || got.Probe.Result != "permission_pending" || got.Previous.State != "stale_state" {
		t.Fatalf("daemon keepalive probe/previous = probe %+v previous %+v, want passive stale-state repair", got.Probe, got.Previous)
	}
	if got.Daemon.State != "running" || got.Daemon.Runtime.ConnectionMode != "auto_connect" {
		t.Fatalf("daemon keepalive daemon = %+v, want running runtime on last approved endpoint", got.Daemon)
	}
}

func TestDaemonKeepalivePassiveDoesNotKillHealthyRuntimeForReconnectMismatch(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := shortCLIStateDir(t)
	userDataDir := t.TempDir()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse fake server URL: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte(u.Port()+"\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatalf("write DevToolsActivePort: %v", err)
	}
	t.Setenv("CDP_DAEMON_USER_DATA_DIR", userDataDir)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeWebSocketEndpoint(t, server.URL), "auto_connect", 0)
	}()
	waitForDaemonRuntimeForMode(t, ctx, stateDir, "headed")
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	runtimeBefore, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headed")
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeForMode before keepalive ok=%v err=%v, want headed runtime", ok, err)
	}
	if runtimeBefore.ReconnectInterval != "" {
		t.Fatalf("runtime reconnect interval = %q, want empty initial interval", runtimeBefore.ReconnectInterval)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headed", "daemon", "keepalive", "--auto-connect", "--user-data-dir", userDataDir, "--repair", "--probe", "passive", "--reconnect", "30s", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		State  string `json:"state"`
		Action string `json:"action"`
		Health struct {
			OK                        bool   `json:"ok"`
			Result                    string `json:"result"`
			ReconnectIntervalMismatch bool   `json:"reconnect_interval_mismatch"`
			CurrentReconnect          string `json:"current_reconnect"`
			WantedReconnect           string `json:"wanted_reconnect"`
		} `json:"health"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v\n%s", err, out.String())
	}
	if !got.OK || got.State != "healthy" || got.Action != "none" || !got.Health.OK || got.Health.Result != "target_list_ok" {
		t.Fatalf("daemon keepalive = %+v, want healthy no-op output for reconnect mismatch", got)
	}
	if !got.Health.ReconnectIntervalMismatch || got.Health.CurrentReconnect != "" || got.Health.WantedReconnect != "30s" {
		t.Fatalf("health = %+v, want reconnect mismatch reported without daemon teardown", got.Health)
	}

	time.Sleep(100 * time.Millisecond)
	runtimeAfter, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headed")
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeForMode after keepalive ok=%v err=%v, want headed runtime still present", ok, err)
	}
	if runtimeAfter.PID != runtimeBefore.PID || !daemon.RuntimeRunning(runtimeAfter) || !daemon.RuntimeSocketReady(context.Background(), runtimeAfter) {
		t.Fatalf("runtime after keepalive = %+v, want same healthy runtime still running", runtimeAfter)
	}
}

func TestDaemonKeepaliveLockedJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := t.TempDir()
	writeKeepaliveLock(t, stateDir, "daemon-keepalive-headed-browser_url-browser-url", os.Getpid(), "active_probe")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "keepalive", "--browser-url", server.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK          bool   `json:"ok"`
		BrowserMode string `json:"browser_mode"`
		State       string `json:"state"`
		Action      string `json:"action"`
		Locked      bool   `json:"locked"`
		Lock        struct {
			Phase string `json:"phase"`
		} `json:"lock"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v", err)
	}
	if !got.OK || got.BrowserMode != "headed" || got.State != "locked" || got.Action != "skipped" || !got.Locked || got.Lock.Phase != "active_probe" {
		t.Fatalf("daemon keepalive = %+v, want locked skip", got)
	}
}

func TestDaemonKeepaliveLockIsScopedByBrowserMode(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := t.TempDir()
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})
	writeKeepaliveLock(t, stateDir, "daemon-keepalive-headless-browser_url-browser-url", os.Getpid(), "headless_active_probe")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "keepalive", "--browser-url", server.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		State  string `json:"state"`
		Action string `json:"action"`
		Lock   struct {
			Name string `json:"name"`
		} `json:"lock"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v", err)
	}
	if !got.OK || got.State != "started" || got.Action != "started" || got.Lock.Name != "daemon-keepalive-headed-browser_url-browser-url" {
		t.Fatalf("daemon keepalive = %+v, want headed lock independent from existing headless lock", got)
	}
}

func TestDaemonKeepaliveClearsDeadOwnerLock(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := t.TempDir()
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})
	writeKeepaliveLock(t, stateDir, "daemon-keepalive-headed-browser_url-browser-url", exitedProcessPID(t), "active_probe")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "keepalive", "--browser-url", server.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		State  string `json:"state"`
		Action string `json:"action"`
		Locked bool   `json:"locked"`
		Lock   struct {
			Name string `json:"name"`
		} `json:"lock"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v", err)
	}
	if !got.OK || got.State == "locked" || got.Action == "skipped" || got.Locked || got.Lock.Name != "daemon-keepalive-headed-browser_url-browser-url" {
		t.Fatalf("daemon keepalive = %+v, want dead-owner lock cleared and keepalive started", got)
	}
}

func writeKeepaliveLock(t *testing.T, stateDir, name string, pid int, phase string) {
	t.Helper()
	lockDir := filepath.Join(stateDir, "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	lockPath := filepath.Join(lockDir, name+".lock")
	lockBody := []byte(fmt.Sprintf(`{"name":%q,"pid":%d,"started_at":"2099-01-01T00:00:00Z","phase":%q}`+"\n", name, pid, phase))
	if err := os.WriteFile(lockPath, lockBody, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}

func writeManagedActivePortForEndpoint(t *testing.T, stateDir, endpoint string) string {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse managed endpoint: %v", err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split managed endpoint host: %v", err)
	}
	profileDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll managed profile returned error: %v", err)
	}
	body := []byte(port + "\n" + u.EscapedPath() + "\n")
	if err := os.WriteFile(filepath.Join(profileDir, "DevToolsActivePort"), body, 0o600); err != nil {
		t.Fatalf("WriteFile DevToolsActivePort returned error: %v", err)
	}
	return port
}

func writeFakeManagedChrome(t *testing.T, port, path string) string {
	t.Helper()
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
user_data_dir=""
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) user_data_dir=${arg#--user-data-dir=} ;;
  esac
done
if [ -z "$user_data_dir" ]; then
  echo "missing --user-data-dir" >&2
  exit 2
fi
mkdir -p "$user_data_dir"
printf '%%s\n%%s\n' %q %q > "$user_data_dir/DevToolsActivePort"
trap 'exit 0' INT TERM
while :; do sleep 1; done
`, port, path)
	if err := os.WriteFile(chromePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake chrome: %v", err)
	}
	return chromePath
}

func exitedProcessPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait helper process: %v", err)
	}
	return pid
}

func TestDaemonRestartBrowserURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()

	stateDir := t.TempDir()
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "start", "--browser-url", server.URL, "--connection-name", "local", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon start exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"daemon", "restart", "--browser-url", server.URL, "--connection-name", "local", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon restart exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Daemon struct {
			State          string `json:"state"`
			ConnectionMode string `json:"connection_mode"`
		} `json:"daemon"`
		Start struct {
			Keepalive bool `json:"keepalive_started"`
		} `json:"start"`
		Restart struct {
			Stopped bool `json:"stopped"`
		} `json:"restart"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon restart output is invalid JSON: %v", err)
	}
	if !got.OK || got.Daemon.State != "running" || got.Daemon.ConnectionMode != "browser_url" || !got.Start.Keepalive || !got.Restart.Stopped {
		t.Fatalf("daemon restart = %+v, want stopped previous daemon and running browser-url daemon", got)
	}
}

func TestDaemonRestartHeadlessIgnoresStaleSavedPortAndStartsManagedRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell chrome test is unix-only")
	}
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := shortCLIStateDir(t)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse fake server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split fake server host: %v", err)
	}
	chromePath := writeFakeManagedChrome(t, port, "/devtools/browser/test")
	t.Setenv("CDP_CHROME_CANDIDATES", chromePath)
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})
	managedProfileDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(managedProfileDir, 0o700); err != nil {
		t.Fatalf("create stale managed profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedProfileDir, "DevToolsActivePort"), []byte("1\n/devtools/browser/stale\n"), 0o600); err != nil {
		t.Fatalf("write stale active port: %v", err)
	}
	if err := browser.SaveManagedMetadata(stateDir, browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           os.Getpid(),
		StartedAt:           "2026-05-21T12:00:00Z",
		UserDataDir:         managedProfileDir,
		DebuggingPort:       "1",
		ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
	}); err != nil {
		t.Fatalf("save stale managed metadata: %v", err)
	}

	var out, errOut bytes.Buffer
	staleBrowserURL := "http://" + net.JoinHostPort("127.0.0.1", "1")
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "connection", "add", "default", "--browser-url", staleBrowserURL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add stale headless exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "restart", "--reconnect", "30s", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("headless daemon restart exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Daemon struct {
			State   string `json:"state"`
			Runtime struct {
				BrowserMode        string `json:"browser_mode"`
				ConnectionMode     string `json:"connection_mode"`
				ReconnectInterval  string `json:"reconnect_interval"`
				ChromePID          int    `json:"chrome_pid"`
				ChromePort         string `json:"chrome_port"`
				ManagedProfilePath string `json:"managed_profile_path"`
				ManagedBrowser     *struct {
					ChromePID           int    `json:"chrome_pid"`
					DebuggingPort       string `json:"debugging_port"`
					ProfileSeedStrategy string `json:"profile_seed_strategy"`
				} `json:"managed_browser"`
			} `json:"runtime"`
		} `json:"daemon"`
		Start struct {
			ConnectionName string `json:"connection_name"`
			Keepalive      bool   `json:"keepalive_started"`
		} `json:"start"`
		Restart struct {
			ManagedRestart bool `json:"managed_restart"`
		} `json:"restart"`
		Chrome struct {
			Launched bool `json:"launched"`
		} `json:"chrome"`
		Connection struct {
			Name        string `json:"name"`
			BrowserMode string `json:"browser_mode"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("headless daemon restart output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || got.Daemon.State != "running" || got.Daemon.Runtime.BrowserMode != "headless" || got.Daemon.Runtime.ConnectionMode != "browser_url" || got.Daemon.Runtime.ReconnectInterval != "30s" {
		t.Fatalf("headless daemon restart = %+v, want running managed browser-url daemon", got.Daemon)
	}
	if got.Daemon.Runtime.ChromePID <= 0 || got.Daemon.Runtime.ChromePort != port || got.Daemon.Runtime.ManagedProfilePath == "" || got.Daemon.Runtime.ManagedBrowser == nil {
		t.Fatalf("runtime = %+v, want managed Chrome metadata for fresh headless restart", got.Daemon.Runtime)
	}
	if got.Daemon.Runtime.ManagedBrowser.DebuggingPort != port || got.Daemon.Runtime.ManagedBrowser.ProfileSeedStrategy != "managed" {
		t.Fatalf("managed browser = %+v, want fake server port and managed seed strategy", got.Daemon.Runtime.ManagedBrowser)
	}
	if !got.Start.Keepalive || got.Start.ConnectionName != "headless" || !got.Restart.ManagedRestart || !got.Chrome.Launched || got.Connection.Name != "headless" || got.Connection.BrowserMode != "headless" {
		t.Fatalf("restart/start metadata = start %+v restart %+v chrome %+v connection %+v, want managed headless restart metadata", got.Start, got.Restart, got.Chrome, got.Connection)
	}
}

func TestDaemonStartAutoConnectPermissionPendingJSON(t *testing.T) {
	stateDir := t.TempDir()
	userDataDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "start", "--autoConnect", "--user-data-dir", userDataDir, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitPermission {
		t.Fatalf("daemon start exit code = %d, want %d; stderr=%s", code, cli.ExitPermission, errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		ErrClass            string   `json:"err_class"`
		RemediationCommands []string `json:"remediation_commands"`
		HumanRequired       bool     `json:"human_required"`
		AgentShouldStop     bool     `json:"agent_should_stop"`
		HumanAction         string   `json:"human_action"`
		SafeDiagnostics     []string `json:"safe_diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon start error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "permission_pending" || got.ErrClass != "permission" || !containsString(got.RemediationCommands, "open chrome://inspect/#remote-debugging") || !got.HumanRequired || !got.AgentShouldStop || !strings.Contains(got.HumanAction, "chrome://inspect") || !containsString(got.SafeDiagnostics, "cdp daemon status --json") {
		t.Fatalf("daemon start error = %+v, want permission_pending with human-in-loop remediation", got)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "current", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection current exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var current struct {
		Connection struct {
			Name string `json:"name"`
			Mode string `json:"mode"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(out.Bytes(), &current); err != nil {
		t.Fatalf("connection current output is invalid JSON: %v", err)
	}
	if current.Connection.Name != "default" || current.Connection.Mode != "auto_connect" {
		t.Fatalf("connection current = %+v, want remembered auto_connect default", current.Connection)
	}
}

func TestDaemonRestartAutoConnectPermissionPendingJSON(t *testing.T) {
	stateDir := t.TempDir()
	userDataDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "restart", "--debug", "--autoConnect", "--active-browser-probe", "--user-data-dir", userDataDir, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitPermission {
		t.Fatalf("daemon restart exit code = %d, want %d; stderr=%s", code, cli.ExitPermission, errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		ErrClass            string   `json:"err_class"`
		RemediationCommands []string `json:"remediation_commands"`
		HumanRequired       bool     `json:"human_required"`
		AgentShouldStop     bool     `json:"agent_should_stop"`
		HumanAction         string   `json:"human_action"`
		SafeDiagnostics     []string `json:"safe_diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon restart error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "permission_pending" || got.ErrClass != "permission" || !containsString(got.RemediationCommands, "open chrome://inspect/#remote-debugging") || !got.HumanRequired || !got.AgentShouldStop || !strings.Contains(got.HumanAction, "chrome://inspect") || !containsString(got.SafeDiagnostics, "cdp daemon status --json") {
		t.Fatalf("daemon restart error = %+v, want permission_pending with human-in-loop remediation", got)
	}
}

func TestDoctorReportsDaemonConnectedWhenBrowserIsAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/144.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": "ws://example.test/devtools/browser/test",
		})
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--browser-url", server.URL, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			State  string `json:"state"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "daemon" {
			if check.Status != "pass" || check.State != "connected" {
				t.Fatalf("daemon check = %+v, want pass connected", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want daemon check", got.Checks)
}

func TestDoctorDaemonIncludesProcessSummaryByBrowserMode(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--check", "daemon", "--state-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Checks []struct {
			Name            string                    `json:"name"`
			ProcessesByMode map[string]map[string]any `json:"processes_by_mode"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	if len(got.Checks) != 1 || got.Checks[0].Name != "daemon" {
		t.Fatalf("doctor checks = %+v, want only daemon check", got.Checks)
	}
	for _, mode := range []string{"headed", "headless"} {
		summary, ok := got.Checks[0].ProcessesByMode[mode]
		if !ok {
			t.Fatalf("processes_by_mode missing %s: %+v", mode, got.Checks[0].ProcessesByMode)
		}
		if summary["browser_mode"] != mode || summary["state"] != "not_running" {
			t.Fatalf("process summary for %s = %+v, want not_running", mode, summary)
		}
	}
}

func TestDoctorAutoConnectReportsPermissionFlow(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--auto-connect", "--user-data-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Checks []struct {
			Name               string `json:"name"`
			Status             string `json:"status"`
			ConnectionMode     string `json:"connection_mode"`
			RequiresUserAllow  bool   `json:"requires_user_allow"`
			DefaultProfileFlow bool   `json:"default_profile_flow"`
			Details            struct {
				State string `json:"state"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "browser_debug_endpoint" {
			if check.Status != "pending" || check.ConnectionMode != "auto_connect" || !check.RequiresUserAllow || !check.DefaultProfileFlow || check.Details.State != "permission_pending" {
				t.Fatalf("browser check = %+v, want auto_connect pending permission flow", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want browser_debug_endpoint", got.Checks)
}

func TestDoctorAutoConnectPassiveSkipsActiveProbe(t *testing.T) {
	userDataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte("1\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--auto-connect", "--user-data-dir", userDataDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Checks []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			State   string `json:"state"`
			Details struct {
				State string `json:"state"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	var sawDaemon, sawBrowser bool
	for _, check := range got.Checks {
		if check.Name == "daemon" {
			sawDaemon = true
			if check.Status != "pending" || check.State != "passive" {
				t.Fatalf("daemon check = %+v, want passive pending", check)
			}
		}
		if check.Name == "browser_debug_endpoint" {
			sawBrowser = true
			if check.Status != "pending" || check.Details.State != "active_probe_skipped" {
				t.Fatalf("browser check = %+v, want active_probe_skipped pending", check)
			}
		}
	}
	if !sawDaemon || !sawBrowser {
		t.Fatalf("doctor checks = %+v, want daemon and browser checks", got.Checks)
	}
}

func TestAutoConnectPagesRequiresRunningDaemon(t *testing.T) {
	userDataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte("1\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--auto-connect", "--user-data-dir", userDataDir, "--state-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitConnection {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitConnection, errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		Message             string   `json:"message"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "connection_not_configured" || !strings.Contains(got.Message, "running headed cdp daemon") {
		t.Fatalf("pages error = %+v, want daemon-required remediation", got)
	}
	if !containsString(got.RemediationCommands, "cdp --browser-mode headed daemon status --json") {
		t.Fatalf("pages remediation commands = %+v, want headed daemon status diagnostic", got.RemediationCommands)
	}
	for _, command := range got.RemediationCommands {
		if strings.Contains(command, "daemon keepalive --auto-connect") {
			t.Fatalf("pages remediation commands = %+v, must not suggest headed approval repair by default", got.RemediationCommands)
		}
	}
}

func TestHeadlessPagesAutoRepairsManagedDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell chrome test is unix-only")
	}
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "auto-repaired-page", "type": "page", "title": "Recovered Page", "url": "https://example.test/recovered", "attached": false},
	})
	defer server.Close()
	stateDir := shortCLIStateDir(t)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse fake server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split fake server host: %v", err)
	}
	chromePath := writeFakeManagedChrome(t, port, "/devtools/browser/test")
	t.Setenv("CDP_CHROME_CANDIDATES", chromePath)
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})

	var out, errOut bytes.Buffer
	staleBrowserURL := "http://" + net.JoinHostPort("127.0.0.1", "1")
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "connection", "add", "headless", "--browser-url", staleBrowserURL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add stale headless exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "pages", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("headless pages exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK    bool `json:"ok"`
		Pages []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"pages"`
		Budget struct {
			BrowserMode    string `json:"browser_mode"`
			ConnectionMode string `json:"connection_mode"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("headless pages output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || len(got.Pages) != 1 || got.Pages[0].ID != "auto-repaired-page" || got.Budget.BrowserMode != "headless" || got.Budget.ConnectionMode != "browser_url" {
		t.Fatalf("headless pages = %+v, want auto-repaired daemon-backed page list", got)
	}
	runtimeState, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless")
	if err != nil {
		t.Fatalf("LoadRuntimeForMode returned error: %v", err)
	}
	if !ok || !daemon.RuntimeRunning(runtimeState) || !daemon.RuntimeSocketReady(context.Background(), runtimeState) || runtimeState.ReconnectInterval != "30s" || runtimeState.ChromePort != port || runtimeState.ManagedBrowser == nil {
		t.Fatalf("runtime = %+v ok=%v, want running managed headless daemon with 30s reconnect", runtimeState, ok)
	}
}
