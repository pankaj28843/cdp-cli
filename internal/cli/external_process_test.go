package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBoundedExternalCommandPreservesStreamsAndFailure(t *testing.T) {
	command := writeExternalProcessFixture(t, `#!/bin/sh
printf 'stdout'
printf 'stderr' >&2
exit 7
`)

	result, err := runBoundedExternalCommand(context.Background(), command)
	if err == nil {
		t.Fatal("runBoundedExternalCommand() error = nil, want process failure")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("runBoundedExternalCommand() error = %v, want exit error", err)
	}
	if result.stdout != "stdout" || result.stderr != "stderr" || result.truncated {
		t.Fatalf("result = %+v, want separate untruncated streams", result)
	}
}

func TestRunBoundedExternalCommandHonorsPreCancellation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	command := writeExternalProcessFixture(t, "#!/bin/sh\ntouch "+shellQuoteForTest(marker)+"\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runBoundedExternalCommand(ctx, command)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runBoundedExternalCommand() error = %v, want context cancellation", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-canceled command marker error = %v, want command not started", err)
	}
}

func TestRunBoundedExternalCommandCancelsOnOutputOverflow(t *testing.T) {
	command := writeExternalProcessFixture(t, `#!/bin/sh
i=0
while [ "$i" -lt 20000 ]; do
  printf '0123456789'
  i=$((i + 1))
done
`)

	result, err := runBoundedExternalCommand(context.Background(), command)
	if !errors.Is(err, errExternalProcessOutputTooLarge) {
		t.Fatalf("runBoundedExternalCommand() error = %v, want output-bound error", err)
	}
	if !result.truncated || len(result.stdout) > maxExternalProcessOutputBytes || len(result.stderr) > maxExternalProcessOutputBytes {
		t.Fatalf("result = %+v, want bounded truncated output", result)
	}
}

func writeExternalProcessFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "external-command")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write external command fixture: %v", err)
	}
	return path
}

func shellQuoteForTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\\"'\\\"'") + "'"
}
