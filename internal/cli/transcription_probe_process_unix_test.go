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

func TestDecodeTranscriptionProbeCancellationTerminatesProcessGroup(t *testing.T) {
	if ownedProcessTerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	binDir := t.TempDir()
	writeFakeTranscriptionExecutable(t, binDir, "ffmpeg", `#!/bin/sh
set -eu
fixture=""
previous=""
for arg do
  if [ "$previous" = "-i" ]; then fixture="$arg"; fi
  previous="$arg"
done
pid_file="${fixture}.child.pid"
(
  trap '' TERM INT
  while :; do sleep 1; done
) &
child=$!
printf '%s\n' "$child" > "$pid_file"
while :; do sleep 1; done
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	fixture := filepath.Join(t.TempDir(), "fixture.webm")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := decodeTranscriptionProbePCM(ctx, fixture)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("decodeTranscriptionProbePCM() error = %v, want context deadline", err)
	}

	pidPath := fixture + ".child.pid"
	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidPath)
		if readErr == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatalf("fake ffmpeg child PID was not published: %d", childPID)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && processAliveForTest(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAliveForTest(childPID) {
		t.Fatalf("ffmpeg descendant process %d survived process-group cancellation", childPID)
	}
}
