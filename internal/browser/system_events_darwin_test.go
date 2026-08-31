//go:build darwin

package browser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunSystemEventsScriptTerminatesDescendantOnTimeout(t *testing.T) {
	binDir := t.TempDir()
	pidPath := filepath.Join(t.TempDir(), "system-events-child.pid")
	osascriptPath := filepath.Join(binDir, "osascript")
	script := "#!/bin/sh\nset -eu\n( trap '' TERM INT; exec /bin/sleep 30 ) &\nchild=$!\nprintf '%s\\n' \"$child\" > \"$CDP_SYSTEM_EVENTS_CHILD_PID_FILE\"\nwhile :; do /bin/sleep 1; done\n"
	if err := os.WriteFile(osascriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write osascript fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CDP_SYSTEM_EVENTS_CHILD_PID_FILE", pidPath)

	_, err := runSystemEventsScript(context.Background(), "tell application \"System Events\" to return 1", "Google Chrome", time.Second)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("runSystemEventsScript error = %v, want bounded timeout", err)
	}
	childPID := waitForManagedPSChildPID(t, pidPath)
	t.Cleanup(func() {
		if process, findErr := os.FindProcess(childPID); findErr == nil {
			_ = process.Kill()
		}
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && managedPSProcessAlive(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if managedPSProcessAlive(childPID) {
		t.Fatalf("System Events helper descendant %d survived owned cancellation", childPID)
	}
}

func TestRunSystemEventsScriptRejectsOverflow(t *testing.T) {
	binDir := t.TempDir()
	osascriptPath := filepath.Join(binDir, "osascript")
	if err := os.WriteFile(osascriptPath, []byte("#!/bin/sh\n/usr/bin/yes x | /usr/bin/head -c 70000\n"), 0o700); err != nil {
		t.Fatalf("write osascript fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := runSystemEventsScript(context.Background(), "synthetic", "Google Chrome", 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "browser helper output exceeded") {
		t.Fatalf("runSystemEventsScript error = %v, want bounded-output error", err)
	}
	if len(output) > browserHelperMaxOutputBytes {
		t.Fatalf("System Events diagnostics length = %d, want <= %d", len(output), browserHelperMaxOutputBytes)
	}
}
