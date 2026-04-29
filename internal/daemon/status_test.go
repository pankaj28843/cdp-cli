package daemon_test

import (
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestSnapshotAutoConnectPermissionPending(t *testing.T) {
	got := daemon.Snapshot("auto_connect", true, browser.ProbeResult{State: "listening_not_cdp"})
	if got.State != "permission_pending" || !got.RequiresUserAllow || !got.DefaultProfileFlow {
		t.Fatalf("Snapshot() = %+v, want auto-connect permission_pending", got)
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
