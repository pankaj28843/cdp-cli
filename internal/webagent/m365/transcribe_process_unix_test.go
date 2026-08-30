//go:build unix

package m365

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDecodeWebMToPCMTerminatesOwnedDescendants(t *testing.T) {
	binDir := t.TempDir()
	writeM365ProcessFixture(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	fixture := filepath.Join(t.TempDir(), "fixture.webm")
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("CDP_M365_CHILD_PID_FILE", childPIDPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := decodeWebMToPCM(ctx, fixture)
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
			t.Fatal("canceled Microsoft 365 converter did not terminate")
		}
	}()

	childPID := readM365ChildPID(t, childPIDPath)
	cancel()
	select {
	case err := <-done:
		finished = true
		if err == nil {
			t.Fatal("decodeWebMToPCM returned nil for canceled converter")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("decodeWebMToPCM did not return after cancellation")
	}
	waitForM365ProcessExit(t, childPID)
}

func writeM365ProcessFixture(t *testing.T, binDir string) {
	t.Helper()
	path := filepath.Join(binDir, "ffmpeg")
	script := `#!/bin/sh
set -eu
(
  trap '' TERM INT
  while :; do sleep 1; done
) &
child=$!
printf '%s\n' "$child" > "$CDP_M365_CHILD_PID_FILE"
while :; do sleep 1; done
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func readM365ChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
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
	t.Fatalf("fake Microsoft 365 ffmpeg child PID was not published")
	return 0
}

func waitForM365ProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && m365ProcessAlive(pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if m365ProcessAlive(pid) {
		t.Fatalf("Microsoft 365 ffmpeg descendant %d survived cancellation", pid)
	}
}

func m365ProcessAlive(pid int) bool {
	output, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}
