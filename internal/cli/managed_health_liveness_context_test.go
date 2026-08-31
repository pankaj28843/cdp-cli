package cli

import (
	"context"
	"os"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestManagedRuntimeProcessCheckRejectsPreCanceledBeforeLivenessProbe(t *testing.T) {
	original := managedRuntimeProcessRunning
	called := false
	managedRuntimeProcessRunning = func(context.Context, int) (bool, error) {
		called = true
		return true, nil
	}
	t.Cleanup(func() { managedRuntimeProcessRunning = original })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, detail := managedRuntimeProcessCheck(ctx, &daemon.Runtime{
		BrowserMode:            "headless",
		ChromePID:              os.Getpid(),
		ChromeProcessStartTime: "2026-08-30T12:30:00Z",
	})
	if result || detail == nil || called || detail["running"] != false || detail["state"] != "process_check_canceled" {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v called=%v, want canceled detail without liveness probe", result, detail, called)
	}
}

func TestManagedRuntimeProcessCheckReportsLivenessCancellationSafely(t *testing.T) {
	original := managedRuntimeProcessRunning
	managedRuntimeProcessRunning = func(context.Context, int) (bool, error) {
		return false, context.DeadlineExceeded
	}
	t.Cleanup(func() { managedRuntimeProcessRunning = original })

	result, detail := managedRuntimeProcessCheck(context.Background(), &daemon.Runtime{
		BrowserMode: "headless",
		ChromePID:   os.Getpid(),
	})
	if result || detail == nil || detail["running"] != false || detail["state"] != "process_check_canceled" {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v, want safe cancellation detail", result, detail)
	}
}
