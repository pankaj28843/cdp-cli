//go:build unix

package gemini

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

func TestEncodeGeminiWebMTerminatesOwnedDescendants(t *testing.T) {
	binDir := t.TempDir()
	writeGeminiProcessFixture(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	fixture := filepath.Join(t.TempDir(), "fixture.wav")
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("CDP_GEMINI_CHILD_PID_FILE", childPIDPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := encodeGeminiWebM(ctx, fixture)
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
			t.Fatal("canceled Gemini converter did not terminate")
		}
	}()

	childPID := readGeminiChildPID(t, childPIDPath)
	cancel()
	select {
	case err := <-done:
		finished = true
		if err == nil {
			t.Fatal("encodeGeminiWebM returned nil for canceled converter")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("encodeGeminiWebM did not return after cancellation")
	}
	waitForGeminiProcessExit(t, childPID)
}

func writeGeminiProcessFixture(t *testing.T, binDir string) {
	t.Helper()
	path := filepath.Join(binDir, "ffmpeg")
	script := `#!/bin/sh
set -eu
(
  trap '' TERM INT
  while :; do sleep 1; done
) &
child=$!
printf '%s\n' "$child" > "$CDP_GEMINI_CHILD_PID_FILE"
while :; do sleep 1; done
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func readGeminiChildPID(t *testing.T, path string) int {
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
	t.Fatalf("fake Gemini ffmpeg child PID was not published")
	return 0
}

func waitForGeminiProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && geminiProcessAlive(pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if geminiProcessAlive(pid) {
		t.Fatalf("Gemini ffmpeg descendant %d survived cancellation", pid)
	}
}

func geminiProcessAlive(pid int) bool {
	output, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}
