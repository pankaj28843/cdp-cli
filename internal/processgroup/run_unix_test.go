//go:build unix

package processgroup

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunTerminatesOwnedProcessGroupOnCancel(t *testing.T) {
	bin := writeProcessGroupFixture(t, `#!/bin/sh
set -eu
pid_file=$1
(
  trap '' TERM INT
  while :; do sleep 1; done
) &
child=$!
printf '%s\n' "$child" > "$pid_file"
while :; do sleep 1; done
`)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, bin, []string{pidFile}, io.Discard, io.Discard) }()

	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		cancel()
		<-done
		t.Fatalf("owned child PID was not published: %d", childPID)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	waitForProcessExit(t, childPID)
}

func TestTerminateStopsStartedOwnedProcessGroup(t *testing.T) {
	bin := writeProcessGroupFixture(t, `#!/bin/sh
set -eu
pid_file=$1
(
  trap '' TERM INT
  while :; do sleep 1; done
) &
child=$!
printf '%s\n' "$child" > "$pid_file"
while :; do sleep 1; done
`)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	command := exec.Command(bin, pidFile)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start owned process: %v", err)
	}
	childPID := waitForProcessPID(t, pidFile)
	waited := false
	t.Cleanup(func() {
		if !waited {
			Terminate(command)
			_ = command.Wait()
		}
	})

	Terminate(command)
	if err := command.Wait(); err == nil {
		t.Fatal("Wait returned nil after owned process-group termination")
	}
	waited = true
	waitForProcessExit(t, childPID)
}

func processAlive(pid int) bool {
	output, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && processAlive(pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatalf("process %d survived owned cancellation", pid)
	}
}

func waitForProcessPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("owned process fixture did not publish child PID")
	return 0
}
