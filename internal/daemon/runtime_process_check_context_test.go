package daemon

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browser"
)

func TestCheckRuntimeProcessRejectsPreCanceledLegacyContextBeforeLiveness(t *testing.T) {
	original := runtimeProcessRunning
	called := false
	runtimeProcessRunning = func(context.Context, int) (bool, error) {
		called = true
		return true, nil
	}
	t.Cleanup(func() { runtimeProcessRunning = original })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	check := CheckRuntimeProcess(ctx, Runtime{PID: os.Getpid(), ProcessStartTime: "2026-08-30T12:30:00Z"})
	if check.Running || check.State != RuntimeProcessStateCanceled || called {
		t.Fatalf("CheckRuntimeProcess = %+v called=%v, want canceled legacy check without liveness", check, called)
	}
}

func TestCheckRuntimeProcessRejectsPreCanceledStrongContextBeforeIdentity(t *testing.T) {
	originalRunning := runtimeProcessRunning
	originalIdentity := runtimeProcessStartTime
	runningCalled := false
	identityCalled := false
	runtimeProcessRunning = func(context.Context, int) (bool, error) {
		runningCalled = true
		return true, nil
	}
	runtimeProcessStartTime = func(context.Context, int) (string, error) {
		identityCalled = true
		return "proc:unexpected", nil
	}
	t.Cleanup(func() {
		runtimeProcessRunning = originalRunning
		runtimeProcessStartTime = originalIdentity
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	check := CheckRuntimeProcess(ctx, Runtime{PID: os.Getpid(), ProcessStartTime: "proc:expected"})
	if check.Running || check.State != RuntimeProcessStateCanceled || runningCalled || identityCalled {
		t.Fatalf("CheckRuntimeProcess = %+v runningCalled=%v identityCalled=%v, want canceled check without probes", check, runningCalled, identityCalled)
	}
}

func TestCheckRuntimeProcessObservesCancellationAfterLiveness(t *testing.T) {
	original := runtimeProcessRunning
	ctx, cancel := context.WithCancel(context.Background())
	runtimeProcessRunning = func(context.Context, int) (bool, error) {
		cancel()
		return true, nil
	}
	t.Cleanup(func() {
		runtimeProcessRunning = original
		cancel()
	})

	check := CheckRuntimeProcess(ctx, Runtime{PID: os.Getpid(), ProcessStartTime: "2026-08-30T12:30:00Z"})
	if check.Running || check.State != RuntimeProcessStateCanceled {
		t.Fatalf("CheckRuntimeProcess = %+v, want cancellation after liveness", check)
	}
}

func TestCheckRuntimeProcessObservesCancellationDuringIdentityProbe(t *testing.T) {
	originalRunning := runtimeProcessRunning
	originalIdentity := runtimeProcessStartTime
	ctx, cancel := context.WithCancel(context.Background())
	runtimeProcessRunning = func(context.Context, int) (bool, error) {
		return true, nil
	}
	runtimeProcessStartTime = func(context.Context, int) (string, error) {
		cancel()
		return "", context.Canceled
	}
	t.Cleanup(func() {
		runtimeProcessRunning = originalRunning
		runtimeProcessStartTime = originalIdentity
		cancel()
	})

	check := CheckRuntimeProcess(ctx, Runtime{PID: os.Getpid(), ProcessStartTime: "proc:expected"})
	if check.Running || check.State != RuntimeProcessStateCanceled || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("CheckRuntimeProcess = %+v ctx=%v, want canceled identity check", check, ctx.Err())
	}
}

func TestWithRuntimeProcessCheckDoesNotPublishCanceledRuntimeAsRunning(t *testing.T) {
	status := SnapshotForMode("headless", "daemon", false, browser.ProbeResult{})
	got := WithRuntimeProcessCheck(status, Runtime{PID: 4242, BrowserMode: "headless"}, RuntimeProcessCheck{State: RuntimeProcessStateCanceled}, false)
	if got.ProcessRunning || got.RuntimeSocketReady || got.ProcessIdentityState != RuntimeProcessStateCanceled || got.State == "running" {
		t.Fatalf("WithRuntimeProcessCheck = %+v, want canceled non-running status", got)
	}
}
