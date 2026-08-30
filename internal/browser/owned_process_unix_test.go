//go:build unix

package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunOwnedBrowserCommandTerminatesDescendantOnCancel(t *testing.T) {
	binDir := t.TempDir()
	pidPath := filepath.Join(t.TempDir(), "browser-helper-child.pid")
	helperPath := filepath.Join(binDir, "browser-helper")
	helper := "#!/bin/sh\nset -eu\n(\n  trap '' TERM INT\n  exec /bin/sleep 30\n) &\nchild=$!\nprintf '%s\\n' \"$child\" > \"$CDP_BROWSER_HELPER_CHILD_PID_FILE\"\nwhile :; do /bin/sleep 1; done\n"
	if err := os.WriteFile(helperPath, []byte(helper), 0o700); err != nil {
		t.Fatalf("write browser helper fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CDP_BROWSER_HELPER_CHILD_PID_FILE", pidPath)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runOwnedBrowserCommand(ctx, "browser-helper")
		done <- err
	}()
	childPID := waitForManagedPSChildPID(t, pidPath)
	t.Cleanup(func() {
		if process, err := os.FindProcess(childPID); err == nil {
			_ = process.Kill()
		}
	})

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("runOwnedBrowserCommand error = %v, want context deadline", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owned browser helper remained blocked after cancellation")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && managedPSProcessAlive(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if managedPSProcessAlive(childPID) {
		t.Fatalf("owned browser helper descendant %d survived process-group cancellation", childPID)
	}
}

func TestRunOwnedBrowserCommandRejectsOverflow(t *testing.T) {
	binDir := t.TempDir()
	helperPath := filepath.Join(binDir, "browser-helper")
	if err := os.WriteFile(helperPath, []byte("#!/bin/sh\n/usr/bin/yes x | /usr/bin/head -c 70000\n"), 0o700); err != nil {
		t.Fatalf("write browser helper fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := runOwnedBrowserCommand(context.Background(), "browser-helper")
	if err == nil || !strings.Contains(err.Error(), "browser helper output exceeded") {
		t.Fatalf("runOwnedBrowserCommand error = %v, want bounded-output error", err)
	}
	if !result.truncated {
		t.Fatal("runOwnedBrowserCommand did not record helper output truncation")
	}
	if len(result.stdout) > browserHelperMaxOutputBytes || len(result.stderr) > browserHelperMaxOutputBytes {
		t.Fatalf("helper output exceeded bound: stdout=%d stderr=%d", len(result.stdout), len(result.stderr))
	}
}
