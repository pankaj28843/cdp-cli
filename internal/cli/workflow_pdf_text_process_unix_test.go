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

func TestRunPDFTextExtractionTerminatesProcessGroupOnCancel(t *testing.T) {
	toolPath := filepath.Join(t.TempDir(), "pdftotext")
	if err := os.WriteFile(toolPath, []byte(`#!/bin/sh
set -eu
(
  trap '' TERM INT
  while :; do
    /bin/sleep 1
  done
) &
child=$!
printf '%s\n' "$child" > "$PDF_TEXT_CHILD_PID_FILE"
while :; do
  /bin/sleep 1
done
`), 0o700); err != nil {
		t.Fatalf("write fake pdftotext: %v", err)
	}
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("PDF_TEXT_CHILD_PID_FILE", childPIDPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runPDFTextExtraction(ctx, toolPath, "synthetic-snapshot.pdf", "synthetic.pdf", 32)
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
		t.Fatal("fake pdftotext did not publish child PID")
	}
	t.Cleanup(func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
	})

	cancel()
	select {
	case err := <-done:
		var commandErr *CommandError
		if !errors.As(err, &commandErr) || commandErr.Code != "pdf_text_extraction_canceled" {
			t.Fatalf("PDF text cancellation error = %v, want pdf_text_extraction_canceled", err)
		}
		data, ok := commandErr.Data.(map[string]any)
		if !ok || data["canceled"] != true || data["process_termination"] != "process_group" {
			t.Fatalf("PDF text cancellation data = %#v, want cancellation/process policy evidence", commandErr.Data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled pdftotext did not return")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && pdfTextProcessAlive(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if pdfTextProcessAlive(childPID) {
		t.Fatalf("pdftotext child process %d survived process-group cancellation", childPID)
	}
}

func pdfTextProcessAlive(pid int) bool {
	output, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}
