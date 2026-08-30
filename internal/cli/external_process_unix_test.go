//go:build unix

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunBoundedExternalCommandTerminatesOwnedDescendantOnOverflow(t *testing.T) {
	command := writeExternalProcessFixture(t, `#!/bin/sh
set -eu
pid_file=$1
(
  trap '' TERM INT
  while :; do :; done
) &
child=$!
printf '%s\n' "$child" > "$pid_file"
i=0
while [ "$i" -lt 20000 ]; do
  printf '0123456789'
  i=$((i + 1))
done
wait
`)
	pidFile := filepath.Join(t.TempDir(), "child.pid")

	result, err := runBoundedExternalCommand(context.Background(), command, pidFile)
	if !errors.Is(err, errExternalProcessOutputTooLarge) || !result.truncated {
		t.Fatalf("runBoundedExternalCommand() result=%+v error=%v, want bounded overflow", result, err)
	}

	childPID := waitForExternalProcessPID(t, pidFile)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && processAliveForTest(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAliveForTest(childPID) {
		t.Fatalf("external command descendant %d survived overflow cancellation", childPID)
	}
}

func waitForExternalProcessPID(t *testing.T, path string) int {
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
	t.Fatalf("external command did not publish child PID in %s", path)
	return 0
}
