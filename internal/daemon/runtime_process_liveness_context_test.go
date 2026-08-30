package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestProcessRunningContextRejectsPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	running, err := ProcessRunningContext(ctx, os.Getpid())
	if !errors.Is(err, context.Canceled) || running {
		t.Fatalf("ProcessRunningContext = running=%v err=%v, want canceled without a running claim", running, err)
	}
}

func TestProcessRunningContextReportsLiveProcess(t *testing.T) {
	running, err := ProcessRunningContext(context.Background(), os.Getpid())
	if err != nil || !running {
		t.Fatalf("ProcessRunningContext = running=%v err=%v, want live process", running, err)
	}
}

func TestProcessRunningContextReportsExitedProcess(t *testing.T) {
	command := exec.Command("sh", "-c", "exit 0")
	if err := command.Start(); err != nil {
		t.Fatalf("start liveness fixture: %v", err)
	}
	pid := command.Process.Pid
	if err := command.Wait(); err != nil {
		t.Fatalf("wait liveness fixture: %v", err)
	}

	running, err := ProcessRunningContext(context.Background(), pid)
	if err != nil || running {
		t.Fatalf("ProcessRunningContext = running=%v err=%v, want exited process", running, err)
	}
}
