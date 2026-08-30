//go:build unix

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunManagedCronTaskTerminatesOwnedDescendantOnCancel(t *testing.T) {
	if ownedProcessTerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	stateDir := t.TempDir()
	command := writeExternalProcessFixture(t, `#!/bin/sh
set -eu
state_dir=""
previous=""
for arg do
  if [ "$previous" = "--state-dir" ]; then state_dir="$arg"; fi
  previous="$arg"
done
(
  trap '' TERM INT
  while :; do sleep 1; done
) &
child=$!
printf '%s\n' "$child" > "$state_dir/descendant.pid"
while :; do sleep 1; done
`)

	previousExecutable := cronRunExecutable
	cronRunExecutable = func() (string, error) { return command, nil }
	t.Cleanup(func() { cronRunExecutable = previousExecutable })

	opts := defaultCronRenderOptions()
	task, ok := managedCronTaskByID(opts, cronTaskHeadedDaemonKeepalive)
	if !ok {
		t.Fatal("headed managed task is missing")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	finished := false
	go func() {
		_, err := runManagedCronTask(ctx, stateDir, task, opts)
		done <- err
	}()
	defer func() {
		if finished {
			return
		}
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("canceled managed cron task did not terminate")
		}
	}()

	pidPath := filepath.Join(stateDir, "descendant.pid")
	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidPath)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatalf("managed cron child PID was not published: %d", childPID)
	}

	cancel()
	select {
	case err := <-done:
		finished = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runManagedCronTask error = %v, want context cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runManagedCronTask did not return after cancellation")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && processAliveForTest(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAliveForTest(childPID) {
		t.Fatalf("managed cron descendant %d survived process-group cancellation", childPID)
	}
}

func TestArtifactsRunManagedTerminatesOwnedDescendantOnCancel(t *testing.T) {
	if ownedProcessTerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	stateDir := t.TempDir()
	command := writeExternalProcessFixture(t, `#!/bin/sh
set -eu
state_dir="$CDP_MANAGED_TEST_STATE_DIR"
(
  trap '' TERM INT
  while :; do sleep 1; done
) &
child=$!
printf '%s\n' "$child" > "$state_dir/descendant.pid"
while :; do sleep 1; done
`)

	t.Setenv("CDP_MANAGED_TEST_STATE_DIR", stateDir)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	exitCode := Execute(ctx, []string{
		"artifacts", "run-managed",
		"--state-dir", stateDir,
		"--task", "managed-child-test",
		"--log", "managed-child.log",
		"--max-log-size", "1KiB",
		"--json",
		"--", command,
	}, io.Discard, io.Discard, BuildInfo{})
	if exitCode == ExitOK {
		t.Fatal("artifacts run-managed returned success after context cancellation")
	}

	childPID := waitForManagedChildPID(t, filepath.Join(stateDir, "descendant.pid"))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && processAliveForTest(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAliveForTest(childPID) {
		t.Fatalf("artifacts run-managed descendant %d survived process-group cancellation", childPID)
	}
}

func waitForManagedChildPID(t *testing.T, path string) int {
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
	t.Fatalf("managed child PID was not published in %s", path)
	return 0
}
