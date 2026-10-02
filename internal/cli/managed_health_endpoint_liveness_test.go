package cli

import (
	"context"
	"os"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestManagedRuntimeProcessCheckUsesManagedEndpointAfterLauncherExit(t *testing.T) {
	port := "9222"
	profile := t.TempDir()

	originalRunning := managedRuntimeProcessRunning
	originalEndpoint := managedRuntimeEndpointReachable
	managedRuntimeProcessRunning = func(context.Context, int) (bool, error) {
		return false, nil
	}
	managedRuntimeEndpointReachable = func(ctx context.Context, userDataDir, expectedPort string) bool {
		if ctx.Err() != nil || userDataDir != profile || expectedPort != port {
			t.Fatalf("unexpected endpoint fallback arguments")
		}
		return true
	}
	t.Cleanup(func() {
		managedRuntimeProcessRunning = originalRunning
		managedRuntimeEndpointReachable = originalEndpoint
	})

	result, detail := managedRuntimeProcessCheck(context.Background(), &daemon.Runtime{
		BrowserMode:         "headless",
		ChromePID:           12345,
		ChromePort:          port,
		ManagedProfilePath:  profile,
		ProfileSeedStrategy: "managed",
	})
	if !result || detail == nil || detail["running"] != true || detail["state"] != "running" || detail["liveness_source"] != "debugging_endpoint" {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v, want endpoint-backed running detail", result, detail)
	}
}

func TestManagedRuntimeProcessCheckDoesNotRescueIdentityMismatchWithEndpoint(t *testing.T) {
	endpointCalled := false
	originalRunning := managedRuntimeProcessRunning
	originalEndpoint := managedRuntimeEndpointReachable
	managedRuntimeProcessRunning = func(context.Context, int) (bool, error) {
		return true, nil
	}
	managedRuntimeEndpointReachable = func(context.Context, string, string) bool {
		endpointCalled = true
		return true
	}
	t.Cleanup(func() {
		managedRuntimeProcessRunning = originalRunning
		managedRuntimeEndpointReachable = originalEndpoint
	})

	result, detail := managedRuntimeProcessCheck(context.Background(), &daemon.Runtime{
		BrowserMode:            "headless",
		ChromePID:              os.Getpid(),
		ChromePort:             "9222",
		ManagedProfilePath:     t.TempDir(),
		ChromeProcessStartTime: "proc:not-the-live-process",
	})
	if result || detail == nil || detail["running"] != false || detail["state"] != "process_identity_mismatch" || endpointCalled {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v endpointCalled=%v, want identity mismatch without endpoint rescue", result, detail, endpointCalled)
	}
}

func TestManagedRuntimeProcessCheckPreservesCancellationDuringEndpointFallback(t *testing.T) {
	originalRunning := managedRuntimeProcessRunning
	originalEndpoint := managedRuntimeEndpointReachable
	managedRuntimeProcessRunning = func(context.Context, int) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() {
		managedRuntimeProcessRunning = originalRunning
		managedRuntimeEndpointReachable = originalEndpoint
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	managedRuntimeEndpointReachable = func(ctx context.Context, _, _ string) bool {
		cancel()
		<-ctx.Done()
		return false
	}
	result, detail := managedRuntimeProcessCheck(ctx, &daemon.Runtime{
		BrowserMode:        "headless",
		ChromePID:          12345,
		ChromePort:         "9222",
		ManagedProfilePath: t.TempDir(),
	})
	if result || detail == nil || detail["running"] != false || detail["state"] != "process_check_canceled" {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v, want canceled fallback detail", result, detail)
	}
}
