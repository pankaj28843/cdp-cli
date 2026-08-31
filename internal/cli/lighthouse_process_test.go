//go:build unix

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunLighthouseCommandTerminatesProcessGroupOnCancel(t *testing.T) {
	if ownedProcessTerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "lighthouse")
	script := `#!/bin/sh
set -eu
pid_file=$1
(
  trap '' TERM INT
  while :; do
    sleep 1
  done
) &
child=$!
printf '%s\n' "$child" > "$pid_file"
while :; do
  sleep 1
done
`
	if err := os.WriteFile(commandPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	finished := false
	go func() {
		done <- runLighthouseCommand(ctx, commandPath, []string{pidFile}, io.Discard, io.Discard)
	}()
	defer func() {
		if finished {
			return
		}
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("canceled Lighthouse command did not terminate")
		}
	}()

	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatalf("child pid was not published: %v", childPID)
	}

	cancel()
	select {
	case err := <-done:
		finished = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runLighthouseCommand error=%v, want context cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runLighthouseCommand did not return after cancellation")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && processAliveForTest(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAliveForTest(childPID) {
		t.Fatalf("Lighthouse child process %d survived process-group cancellation", childPID)
	}
}

func processAliveForTest(pid int) bool {
	output, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}

func TestBoundedLighthouseOutputMarksTruncation(t *testing.T) {
	output := &boundedProcessOutput{maxBytes: 4}
	if _, err := output.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "abcd\n<truncated>" || !output.truncated {
		t.Fatalf("bounded output=%q truncated=%v", got, output.truncated)
	}
}
