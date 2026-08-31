package browser

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func TestSignalProcessHonorsCancellationDuringGraceWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed signal process fixture is Unix-only")
	}
	command := startManagedSignalFixture(t, `#!/bin/sh
trap '' INT TERM
exec sleep 30
`)
	pid := command.Process.Pid

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	err := signalProcess(ctx, pid)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("signalProcess error = %v, want cancellation", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("signalProcess cancellation took %s, want prompt return", elapsed)
	}
	if !managedProcessAliveForTest(pid) {
		t.Fatal("cancellation escalated to kill before the grace window ended")
	}
}

func TestSignalProcessRejectsPreCanceledContextWithoutSignaling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed signal process fixture is Unix-only")
	}
	command := startManagedSignalFixture(t, `#!/bin/sh
exec sleep 30
`)
	pid := command.Process.Pid
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := signalProcess(ctx, pid)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("signalProcess error = %v, want pre-cancellation", err)
	}
	if !managedProcessAliveForTest(pid) {
		t.Fatal("pre-canceled signalProcess stopped the process")
	}
}

func TestSignalProcessPreservesGracefulExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed signal process fixture is Unix-only")
	}
	command := startManagedSignalFixture(t, `#!/bin/sh
exec sleep 30
`, true)
	pid := command.Process.Pid
	time.Sleep(time.Second)

	started := time.Now()
	if err := signalProcess(context.Background(), pid); err != nil {
		t.Fatalf("signalProcess error = %v, want exit during grace wait", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("signalProcess exit handling took %s, want prompt return", elapsed)
	}
	if !waitForManagedProcessGone(t, pid) {
		t.Fatal("signalProcess returned while the exited process remained alive")
	}
}

func TestSignalProcessPreservesBoundedLeaderEscalation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed signal process fixture is Unix-only")
	}
	command := startManagedSignalFixture(t, `#!/bin/sh
trap '' INT TERM
exec sleep 30
`, true)
	pid := command.Process.Pid
	time.Sleep(time.Second)

	started := time.Now()
	if err := signalProcess(context.Background(), pid); err != nil {
		t.Fatalf("signalProcess error = %v, want bounded leader escalation", err)
	}
	if elapsed := time.Since(started); elapsed < 1500*time.Millisecond {
		t.Fatalf("signalProcess escalation took %s, want the existing graceful window", elapsed)
	}
	if !waitForManagedProcessGone(t, pid) {
		t.Fatal("signalProcess returned while the escalated process remained alive")
	}
}

func waitForManagedProcessGone(t *testing.T, pid int) bool {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !managedProcessAliveForTest(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !managedProcessAliveForTest(pid)
}

func startManagedSignalFixture(t *testing.T, script string, detach ...bool) *exec.Cmd {
	t.Helper()
	path := filepath.Join(t.TempDir(), "managed-signal")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write managed signal fixture: %v", err)
	}
	command, err := processgroup.StartWithOptions(path, nil, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start managed signal fixture: %v", err)
	}
	if len(detach) > 0 && detach[0] {
		pid := command.Process.Pid
		processgroup.Detach(command)
		t.Cleanup(func() { _ = processgroup.TerminatePID(pid) })
	} else {
		t.Cleanup(func() {
			processgroup.Terminate(command)
			_ = command.Wait()
		})
	}
	return command
}
