package output

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func TestApplyJQPreservesSuccessfulFilteredOutput(t *testing.T) {
	binDir := t.TempDir()
	writeJQFixture(t, binDir, `#!/bin/sh
cat
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	input := []byte(`{"ok":true,"value":"synthetic"}`)
	got, err := applyJQ(context.Background(), input, ".")
	if err != nil {
		t.Fatalf("applyJQ() error = %v", err)
	}
	if string(got) != string(input) {
		t.Fatalf("applyJQ() output = %q, want unchanged filtered output %q", got, input)
	}
}

func TestApplyJQBoundsOversizedDiagnostics(t *testing.T) {
	binDir := t.TempDir()
	writeJQFixture(t, binDir, `#!/bin/sh
i=0
while [ "$i" -lt 20000 ]; do
  printf 'jq-diagnostic-marker' >&2
  i=$((i + 1))
done
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := applyJQ(context.Background(), []byte(`{"ok":true}`), ".")
	if err == nil {
		t.Fatal("applyJQ() error = nil, want jq failure")
	}
	if len(err.Error()) > 70<<10 {
		t.Fatalf("applyJQ() error length = %d, want bounded diagnostic", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "jq diagnostics truncated") {
		t.Fatalf("applyJQ() error = %q, want truncation marker", err)
	}
}

func TestApplyJQPreservesMissingExecutableFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := applyJQ(context.Background(), []byte(`{"ok":true}`), ".")
	if err == nil || !strings.Contains(err.Error(), "run jq:") {
		t.Fatalf("applyJQ() error = %v, want missing-jq failure classification", err)
	}
}

func TestApplyJQTerminatesOwnedDescendantOnCancel(t *testing.T) {
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	binDir := t.TempDir()
	pidPath := filepath.Join(t.TempDir(), "jq-child.pid")
	writeJQFixture(t, binDir, `#!/bin/sh
set -eu
(
  trap '' TERM INT
  while :; do sleep 1; done
) &
child=$!
printf '%s\n' "$child" > "$CDP_JQ_CHILD_PID_FILE"
while :; do sleep 1; done
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CDP_JQ_CHILD_PID_FILE", pidPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := applyJQ(ctx, []byte(`{"ok":true}`), ".")
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
			t.Fatal("canceled jq fixture did not terminate")
		}
	}()

	childPID := waitForJQChildPID(t, pidPath)
	cancel()
	select {
	case err := <-done:
		finished = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("applyJQ() error = %v, want context cancellation", err)
		}
	case <-time.After(3 * time.Second):
		killJQTestProcess(childPID)
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatal("applyJQ() did not return after cancellation")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && jqProcessAlive(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if jqProcessAlive(childPID) {
		t.Fatalf("jq descendant %d survived process-group cancellation", childPID)
	}
}

func writeJQFixture(t *testing.T, binDir, body string) string {
	t.Helper()
	path := filepath.Join(binDir, "jq")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write jq fixture: %v", err)
	}
	return path
}

func waitForJQChildPID(t *testing.T, path string) int {
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
	t.Fatalf("jq fixture did not publish child PID")
	return 0
}

func jqProcessAlive(pid int) bool {
	output, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}

func killJQTestProcess(pid int) {
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
}
