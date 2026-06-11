package cli

import (
	"context"
	"testing"
)

func TestRetryableTransientCommandErrorClassifiesDaemonAndTargetRaces(t *testing.T) {
	tests := []error{
		commandError("connection_failed", "connection", "daemon failed to read JSON message: use of closed network connection", ExitConnection, nil),
		commandError("target_not_found", "check_failed", "no target matched", ExitCheckFailed, nil),
		commandError("connection_failed", "connection", "attach target page-1: target closed", ExitConnection, nil),
		commandError("connection_failed", "connection", "evaluate target page-1: execution context was destroyed", ExitConnection, nil),
	}
	for _, err := range tests {
		if !retryableTransientCommandError(context.Background(), err) {
			t.Fatalf("retryableTransientCommandError(%v) = false, want true", err)
		}
	}
}

func TestRetryableTransientCommandErrorSkipsSafetyStates(t *testing.T) {
	tests := []error{
		commandError("connection_failed", "connection", "permission denied by browser", ExitPermission, nil),
		commandError("connection_failed", "connection", "login required before continuing", ExitCheckFailed, nil),
		commandError("connection_failed", "connection", "captcha unusual traffic page", ExitCheckFailed, nil),
		commandError("connection_failed", "connection", "payment confirmation blocked state", ExitCheckFailed, nil),
		commandError("javascript_exception", "runtime", "javascript exception: login required", ExitCheckFailed, nil),
	}
	for _, err := range tests {
		if retryableTransientCommandError(context.Background(), err) {
			t.Fatalf("retryableTransientCommandError(%v) = true, want false", err)
		}
	}
}

func TestRetryableTransientCommandErrorStopsWhenContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := commandError("connection_failed", "connection", "target closed", ExitConnection, nil)
	if retryableTransientCommandError(ctx, err) {
		t.Fatalf("retryableTransientCommandError with canceled context = true, want false")
	}
}
