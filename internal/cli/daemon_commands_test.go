package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"--browser-mode", "headless", "pages", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s stdout=%s", code, cli.ExitOK, errOut.String(), out.String())
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

func TestDaemonKeepaliveRepairsHeadedFromStaleApprovedEndpointWithoutActiveProbe(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := shortCLIStateDir(t)
	staleRuntime := daemon.Runtime{
		PID:               999999999,
		StartedAt:         time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		BrowserMode:       "headed",
		ConnectionMode:    "auto_connect",
		ReconnectInterval: "30s",
		SocketPath:        daemon.RuntimeSocketPathForMode(stateDir, "headed"),
		Endpoint:          fakeWebSocketEndpoint(t, server.URL),
	}
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headed", staleRuntime); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"--browser-mode", "headed", "daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headed", "daemon", "keepalive", "--auto-connect", "--repair", "--probe", "passive", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
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

func TestDaemonKeepaliveLockedJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := t.TempDir()
	writeKeepaliveLock(t, stateDir, "daemon-keepalive-headed-browser_url-browser-url", "active_probe")

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
	writeKeepaliveLock(t, stateDir, "daemon-keepalive-headless-browser_url-browser-url", "headless_active_probe")

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

func writeKeepaliveLock(t *testing.T, stateDir, name, phase string) {
	t.Helper()
	lockDir := filepath.Join(stateDir, "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	lockPath := filepath.Join(lockDir, name+".lock")
	lockBody := []byte(fmt.Sprintf(`{"name":%q,"pid":1234,"started_at":"2099-01-01T00:00:00Z","phase":%q}`+"\n", name, phase))
	if err := os.WriteFile(lockPath, lockBody, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
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

func TestHeadlessPagesRequireDaemonEvenWithManagedMetadata(t *testing.T) {
	stateDir := t.TempDir()
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           123456,
		StartedAt:           "2026-05-21T12:00:00Z",
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		DebuggingPort:       "9222",
		ProfileSeedStrategy: "managed",
		OwnedMarker:         "owned-token",
		ProcessStartTime:    "2026-05-21T12:00:00Z",
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--browser-mode", "headless", "pages", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
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
	if got.OK || got.Code != "connection_not_configured" || !strings.Contains(got.Message, "running headless cdp daemon") {
		t.Fatalf("pages error = %+v, want daemon-required failure without direct managed endpoint fallback", got)
	}
	if !containsString(got.RemediationCommands, "cdp --browser-mode headless daemon keepalive --repair --json") {
		t.Fatalf("pages remediation commands = %+v, want headless repair command", got.RemediationCommands)
	}
	for _, command := range got.RemediationCommands {
		if strings.Contains(command, "daemon keepalive --auto-connect") {
			t.Fatalf("pages remediation commands = %+v, must not suggest headed auto-connect repair for headless", got.RemediationCommands)
		}
	}
}
