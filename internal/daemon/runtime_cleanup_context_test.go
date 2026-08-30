package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestClearRuntimeForModeHonorsCancellationBeforeRuntimeRemoval(t *testing.T) {
	stateDir := t.TempDir()
	runtime := runtimeCleanupTestRuntime(stateDir)
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headless", runtime); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	originalRemove := runtimeRemoveFile
	var removeCalled bool
	ctx, cancel := context.WithCancel(context.Background())
	runtimeRemoveFile = func(ctx context.Context, _ string) error {
		removeCalled = true
		cancel()
		return ctx.Err()
	}
	t.Cleanup(func() {
		runtimeRemoveFile = originalRemove
		cancel()
	})

	err := ClearRuntimeForMode(ctx, stateDir, "headless", runtime.PID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ClearRuntimeForMode error = %v, want cancellation", err)
	}
	if !removeCalled {
		t.Fatal("runtime removal seam was not reached after the current-record check")
	}
	if _, statErr := os.Stat(RuntimePathForMode(stateDir, "headless")); statErr != nil {
		t.Fatalf("runtime state after canceled removal = %v, want preserved file", statErr)
	}
}

func TestClearRuntimeForModeSkipsSocketAfterRuntimeCleanupCancellation(t *testing.T) {
	stateDir := t.TempDir()
	runtime := runtimeCleanupTestRuntime(stateDir)
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headless", runtime); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(runtime.SocketPath), 0o700); err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	if err := os.WriteFile(runtime.SocketPath, []byte("socket fixture"), 0o600); err != nil {
		t.Fatalf("write socket fixture: %v", err)
	}

	originalRemove := runtimeRemoveFile
	var removeCalls int
	var socketRemoveAttempted bool
	ctx, cancel := context.WithCancel(context.Background())
	runtimeRemoveFile = func(_ context.Context, path string) error {
		removeCalls++
		if path == runtime.SocketPath {
			socketRemoveAttempted = true
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		cancel()
		return nil
	}
	t.Cleanup(func() {
		runtimeRemoveFile = originalRemove
		cancel()
	})

	err := clearRuntimeForMode(ctx, stateDir, "headless", runtime)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("clearRuntimeForMode error = %v, want cancellation", err)
	}
	if removeCalls != 1 || socketRemoveAttempted {
		t.Fatalf("runtime cleanup removal calls=%d socket_attempted=%v, want state-only removal", removeCalls, socketRemoveAttempted)
	}
	if _, statErr := os.Stat(runtime.SocketPath); statErr != nil {
		t.Fatalf("socket after canceled cleanup = %v, want preserved socket", statErr)
	}
}

func TestClearRuntimeForModeIfCurrentStillPreservesMismatchedRuntime(t *testing.T) {
	stateDir := t.TempDir()
	expected := runtimeCleanupTestRuntime(stateDir)
	replacement := expected
	replacement.PID++
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headless", replacement); err != nil {
		t.Fatalf("SaveRuntimeForMode replacement returned error: %v", err)
	}

	originalRemove := runtimeRemoveFile
	removeCalled := false
	runtimeRemoveFile = func(context.Context, string) error {
		removeCalled = true
		return nil
	}
	t.Cleanup(func() { runtimeRemoveFile = originalRemove })

	cleared, err := clearRuntimeForModeIfCurrent(context.Background(), stateDir, "headless", expected)
	if err != nil {
		t.Fatalf("clearRuntimeForModeIfCurrent returned error: %v", err)
	}
	if cleared || removeCalled {
		t.Fatalf("clearRuntimeForModeIfCurrent = cleared=%v remove_called=%v, want preserved replacement", cleared, removeCalled)
	}
	loaded, ok, err := LoadRuntimeForMode(context.Background(), stateDir, "headless")
	if err != nil || !ok || loaded.PID != replacement.PID {
		t.Fatalf("replacement runtime = %+v ok=%v err=%v, want preserved replacement", loaded, ok, err)
	}
}

func TestStopRuntimeForModeSkipsSocketAfterIdentityMismatchCleanupCancellation(t *testing.T) {
	stateDir := t.TempDir()
	runtime := runtimeCleanupTestRuntime(stateDir)
	runtime.ProcessStartTime = "proc:expected"
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headless", runtime); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(runtime.SocketPath), 0o700); err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	if err := os.WriteFile(runtime.SocketPath, []byte("socket fixture"), 0o600); err != nil {
		t.Fatalf("write socket fixture: %v", err)
	}

	originalRunning := runtimeProcessRunning
	originalStartTime := runtimeProcessStartTime
	originalRemove := runtimeRemoveFile
	runtimeProcessRunning = func(context.Context, int) (bool, error) { return true, nil }
	runtimeProcessStartTime = func(context.Context, int) (string, error) {
		return "proc:replacement", nil
	}
	var removeCalls int
	var socketRemoveAttempted bool
	ctx, cancel := context.WithCancel(context.Background())
	runtimeRemoveFile = func(_ context.Context, path string) error {
		removeCalls++
		if path == runtime.SocketPath {
			socketRemoveAttempted = true
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		cancel()
		return nil
	}
	t.Cleanup(func() {
		runtimeProcessRunning = originalRunning
		runtimeProcessStartTime = originalStartTime
		runtimeRemoveFile = originalRemove
		cancel()
	})

	_, stopped, err := StopRuntimeForMode(ctx, stateDir, "headless")
	if !errors.Is(err, context.Canceled) || stopped {
		t.Fatalf("StopRuntimeForMode = stopped=%v err=%v, want canceled cleanup", stopped, err)
	}
	if removeCalls != 1 || socketRemoveAttempted {
		t.Fatalf("cleanup removal calls=%d socket_attempted=%v, want state-only removal", removeCalls, socketRemoveAttempted)
	}
	if _, statErr := os.Stat(runtime.SocketPath); statErr != nil {
		t.Fatalf("socket after canceled cleanup = %v, want preserved socket", statErr)
	}
}

func runtimeCleanupTestRuntime(stateDir string) Runtime {
	return Runtime{
		PID:            4242,
		StartedAt:      "2026-08-30T17:20:00Z",
		BrowserMode:    "headless",
		ConnectionMode: "browser_url",
		SocketPath:     filepath.Join(stateDir, "headless", RuntimeSocketFileName),
	}
}
