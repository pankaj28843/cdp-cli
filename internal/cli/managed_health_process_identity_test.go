package cli

import (
	"context"
	"os"
	"runtime"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func TestManagedRuntimeProcessCheckRejectsMismatchedStrongIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	result, detail := managedRuntimeProcessCheck(context.Background(), &daemon.Runtime{
		BrowserMode:            "headless",
		ChromePID:              os.Getpid(),
		ChromeProcessStartTime: "proc:not-the-live-process",
	})
	if result || detail == nil || detail["running"] != false || detail["state"] != "process_identity_mismatch" {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v, want safe identity mismatch", result, detail)
	}
}

func TestManagedRuntimeProcessCheckAcceptsMatchingStrongIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	token, err := processgroup.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Fatalf("ProcessStartTime returned error: %v", err)
	}
	result, detail := managedRuntimeProcessCheck(context.Background(), &daemon.Runtime{
		BrowserMode:            "headless",
		ChromePID:              os.Getpid(),
		ChromeProcessStartTime: token,
	})
	if !result || detail == nil || detail["running"] != true || detail["state"] != "running" {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v, want matching identity", result, detail)
	}
}

func TestManagedRuntimeProcessCheckKeepsLegacyTimestampCompatibility(t *testing.T) {
	result, detail := managedRuntimeProcessCheck(context.Background(), &daemon.Runtime{
		BrowserMode:            "headless",
		ChromePID:              os.Getpid(),
		ChromeProcessStartTime: "2026-08-30T12:30:00Z",
	})
	if !result || detail == nil || detail["running"] != true || detail["state"] != "running" {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v, want legacy-compatible running state", result, detail)
	}
}
