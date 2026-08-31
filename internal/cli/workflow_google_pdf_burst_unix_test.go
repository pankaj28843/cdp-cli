//go:build unix

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBurstGooglePDFTerminatesProcessGroupOnCancel(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGooglePDFPoppler(t, binDir, `#!/bin/sh
set -eu
(
  trap '' TERM INT
  while :; do
    /bin/sleep 1
  done
) &
child=$!
printf '%s\n' "$child" > "$BURST_CHILD_PID_FILE"
while :; do
  /bin/sleep 1
done
`)
	t.Setenv("PATH", binDir)
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("BURST_CHILD_PID_FILE", childPIDPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := burstGooglePDF(ctx, "synthetic.pdf", filepath.Join(t.TempDir(), "burst"))
		done <- err
	}()

	childPID := 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(childPIDPath)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatal("fake pdftoppm did not publish child PID")
	}
	t.Cleanup(func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
	})

	cancel()
	select {
	case err := <-done:
		var commandErr *CommandError
		if !errors.As(err, &commandErr) || commandErr.Code != "pdf_burst_failed" {
			t.Fatalf("burst cancellation error = %v, want pdf_burst_failed", err)
		}
		data, ok := commandErr.Data.(map[string]any)
		if !ok || data["canceled"] != true || data["process_termination"] != "process_group" {
			t.Fatalf("burst cancellation data = %#v, want cancellation/process policy evidence", commandErr.Data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled pdftoppm did not return")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && googlePDFProcessAlive(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if googlePDFProcessAlive(childPID) {
		t.Fatalf("pdftoppm child process %d survived process-group cancellation", childPID)
	}
}

func googlePDFProcessAlive(pid int) bool {
	output, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}
