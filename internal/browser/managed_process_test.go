package browser

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func TestManagedProcessSnapshotsReportsContextCancellation(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process snapshots are only implemented on Unix")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := managedProcessSnapshots(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("managedProcessSnapshots error = %v, want context.Canceled", err)
	}
}

func TestManagedProcessSnapshotsParsesCompleteTable(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process snapshots are only implemented on Unix")
	}
	binDir := t.TempDir()
	writeManagedPSFixture(t, binDir, `#!/bin/sh
printf '%s\n' '101 100 /usr/bin/other' '102 101 /usr/bin/google-chrome --headless --remote-debugging-port=9222 --user-data-dir=/tmp/managed'
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	snapshots, err := managedProcessSnapshots(context.Background())
	if err != nil {
		t.Fatalf("managedProcessSnapshots() error = %v", err)
	}
	if len(snapshots) != 2 || snapshots[1].PID != 102 || snapshots[1].ParentPID != 101 {
		t.Fatalf("managedProcessSnapshots() = %+v, want complete synthetic table", snapshots)
	}
}

func TestManagedChromeProcessesPSParsesCompleteTable(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("ps process scan is only used on Darwin")
	}
	binDir := t.TempDir()
	writeManagedPSFixture(t, binDir, `#!/bin/sh
printf '%s\n' '201 /usr/bin/google-chrome --headless --remote-debugging-port=9222 --user-data-dir=/tmp/managed' '202 /usr/bin/other'
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pids, err := managedChromeProcessesPS(context.Background(), "/tmp/managed")
	if err != nil {
		t.Fatalf("managedChromeProcessesPS() error = %v", err)
	}
	if len(pids) != 1 || pids[0] != 201 {
		t.Fatalf("managedChromeProcessesPS() = %+v, want managed PID 201", pids)
	}
}

func TestManagedChromeProcessesPSRejectsProbeFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("ps process scan is only used on Darwin")
	}
	binDir := t.TempDir()
	writeManagedPSFixture(t, binDir, `#!/bin/sh
exit 7
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pids, err := managedChromeProcessesPS(context.Background(), "/tmp/managed")
	if err == nil || pids != nil {
		t.Fatalf("managedChromeProcessesPS() = pids=%+v err=%v, want explicit probe failure", pids, err)
	}
}

func TestManagedProcessSnapshotsRejectsOversizedTable(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process snapshots are only implemented on Unix")
	}
	binDir := t.TempDir()
	writeManagedPSFixture(t, binDir, `#!/bin/sh
/usr/bin/yes p | /usr/bin/head -c 4194305
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := managedProcessSnapshots(context.Background())
	if err == nil || !strings.Contains(err.Error(), "managed process table output exceeded") {
		t.Fatalf("managedProcessSnapshots() error = %v, want explicit overflow failure", err)
	}
}

func TestManagedProcessSnapshotsTerminatesOwnedDescendantOnCancel(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process snapshots are only implemented on Unix")
	}
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	binDir := t.TempDir()
	pidPath := filepath.Join(t.TempDir(), "managed-ps-child.pid")
	writeManagedPSFixture(t, binDir, `#!/bin/sh
set -eu
(
  trap '' TERM INT
  while :; do /bin/sleep 1; done
) &
child=$!
printf '%s\n' "$child" > "$CDP_MANAGED_PS_CHILD_PID_FILE"
while :; do /bin/sleep 1; done
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CDP_MANAGED_PS_CHILD_PID_FILE", pidPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := managedProcessSnapshots(ctx)
		done <- err
	}()
	finished := false
	defer func() {
		if finished {
			return
		}
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("canceled managed process probe did not terminate")
		}
	}()

	childPID := waitForManagedPSChildPID(t, pidPath)
	cancel()
	select {
	case err := <-done:
		finished = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("managedProcessSnapshots() error = %v, want context cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("managedProcessSnapshots() did not return after cancellation")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && managedPSProcessAlive(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if managedPSProcessAlive(childPID) {
		t.Fatalf("managed process probe descendant %d survived process-group cancellation", childPID)
	}
}

func writeManagedPSFixture(t *testing.T, binDir, body string) string {
	t.Helper()
	path := filepath.Join(binDir, "ps")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write ps fixture: %v", err)
	}
	return path
}

func waitForManagedPSChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
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
	t.Fatalf("managed ps fixture did not publish child PID")
	return 0
}

func managedPSProcessAlive(pid int) bool {
	output, err := exec.Command("/bin/ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}
