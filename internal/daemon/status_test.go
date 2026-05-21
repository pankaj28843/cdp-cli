package daemon_test

import (
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestSnapshotAutoConnectPermissionPending(t *testing.T) {
	got := daemon.Snapshot("auto_connect", true, browser.ProbeResult{State: "listening_not_cdp"})
	if got.State != "permission_pending" || got.BrowserMode != "headed" || !got.RequiresUserAllow || !got.DefaultProfileFlow {
		t.Fatalf("Snapshot() = %+v, want auto-connect permission_pending headed", got)
	}
}

func TestSnapshotBrowserURLConnected(t *testing.T) {
	got := daemon.Snapshot("browser_url", false, browser.ProbeResult{State: "cdp_available"})
	if got.State != "connected" || got.RequiresUserAllow || got.DefaultProfileFlow {
		t.Fatalf("Snapshot() = %+v, want browser-url connected", got)
	}
}

func TestSnapshotAutoConnectPassive(t *testing.T) {
	got := daemon.Snapshot("auto_connect", true, browser.ProbeResult{State: "active_probe_skipped", Message: "skipped"})
	if got.State != "passive" || !got.RequiresUserAllow || !got.DefaultProfileFlow {
		t.Fatalf("Snapshot() = %+v, want auto-connect passive", got)
	}
}

func TestSnapshotForModeHeadlessNextCommands(t *testing.T) {
	got := daemon.SnapshotForMode("headless", "browser_url", false, browser.ProbeResult{State: "cdp_available"})
	if got.BrowserMode != "headless" || got.State != "connected" || !containsStatusCommand(got.NextCommands, "cdp --browser-mode headless daemon keepalive --repair --json") {
		t.Fatalf("SnapshotForMode headless = %+v, want headless connected next commands", got)
	}

	got = daemon.SnapshotForMode("headless", "browser_url", false, browser.ProbeResult{State: "active_probe_skipped"})
	if got.BrowserMode != "headless" || got.State != "passive" || !containsStatusCommand(got.NextCommands, "cdp --browser-mode headless daemon status --active-browser-probe --json") {
		t.Fatalf("SnapshotForMode headless passive = %+v, want mode-specific status command", got)
	}
}

func TestWithRuntimeHeadlessNextCommands(t *testing.T) {
	status := daemon.SnapshotForMode("headless", "browser_url", false, browser.ProbeResult{State: "cdp_available"})
	got := daemon.WithRuntime(status, daemon.Runtime{PID: 123, BrowserMode: "headless"}, true)
	if got.BrowserMode != "headless" || got.State != "running" || !containsStatusCommand(got.NextCommands, "cdp --browser-mode headless daemon stop --json") {
		t.Fatalf("WithRuntime headless = %+v, want mode-specific stop command", got)
	}
}

func containsStatusCommand(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
