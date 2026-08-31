package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestApplyManagedBrowserHealthDoesNotPublishOwnershipAfterCancellation(t *testing.T) {
	stateDir := t.TempDir()
	a := &app{opts: options{stateDir: stateDir}}
	a.root = a.newRoot()
	a.opts.stateDir = stateDir
	health := map[string]any{
		"state":            "healthy",
		"reasons":          []string{},
		"next_commands":    []string{},
		"daemon_rpc_ready": true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	a.applyManagedBrowserHealth(ctx, health, &daemon.Runtime{
		BrowserMode:            "headless",
		ChromePID:              os.Getpid(),
		ManagedProfilePath:     filepath.Join(stateDir, "browser", browser.ManagedProfileDirName),
		ChromeProcessStartTime: "",
	})

	ownership, ok := health["managed_ownership"].(browser.ManagedOwnershipEvidence)
	if !ok {
		t.Fatalf("managed_ownership = %#v, want browser.ManagedOwnershipEvidence", health["managed_ownership"])
	}
	if ownership.Checked || ownership.Owned {
		t.Fatalf("managed ownership after cancellation = %+v, want unchecked and unowned", ownership)
	}
	if health["managed_chrome_owned"] != false || health["state"] != "degraded" {
		t.Fatalf("health after ownership cancellation = %+v, want degraded and unowned", health)
	}
	foundCancellation := false
	for _, reason := range toStringSlice(health["reasons"]) {
		if reason == "managed_chrome_ownership_check_canceled" {
			foundCancellation = true
			break
		}
	}
	if !foundCancellation {
		t.Fatalf("health reasons = %v, want cancellation classification", health["reasons"])
	}
}
