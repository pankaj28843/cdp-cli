//go:build unix

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCronFlockMarkerProbeTerminatesOwnedDescendantOnCancel(t *testing.T) {
	if ownedProcessTerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	flockPath := filepath.Join(t.TempDir(), "flock")
	script := `#!/bin/sh
set -eu
(
  trap '' TERM INT
  while :; do sleep 1; done
) &
child=$!
printf '%s\n' "$child" > "$CDP_TEST_FLOCK_CHILD_PID"
trap '' TERM INT
while :; do sleep 1; done
`
	if err := os.WriteFile(flockPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake flock: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(flockPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CDP_TEST_FLOCK_CHILD_PID", childPIDPath)

	ctx, cancel := context.WithCancel(context.Background())
	markerPath := filepath.Join(t.TempDir(), "marker.lock")
	done := make(chan struct {
		locked bool
		known  bool
	}, 1)
	go func() {
		locked, known := cronFlockMarkerLocked(ctx, markerPath)
		done <- struct {
			locked bool
			known  bool
		}{locked: locked, known: known}
	}()

	childPID := waitForCronFlockChildPID(t, childPIDPath)
	cancel()
	select {
	case result := <-done:
		if result.locked || result.known {
			t.Fatalf("cronFlockMarkerLocked() = locked=%v known=%v, want unknown cancellation", result.locked, result.known)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cronFlockMarkerLocked did not return after cancellation")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && processAliveForTest(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAliveForTest(childPID) {
		t.Fatalf("flock probe descendant %d survived process-group cancellation", childPID)
	}
}

func waitForCronFlockChildPID(t *testing.T, path string) int {
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
	t.Fatal("flock probe did not publish child PID")
	return 0
}
