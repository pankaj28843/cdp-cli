package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func TestChromeProcessRunningTerminatesProbeDescendantOnCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("headed process probe process-group test is Unix-only")
	}
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	binDir := t.TempDir()
	childPIDPath := filepath.Join(t.TempDir(), "probe-child.pid")
	writeHeadedProbeFixture(t, binDir, `#!/bin/sh
set -eu
(
  trap '' TERM INT
  exec /bin/sleep 30
) &
child=$!
printf '%s\n' "$child" > "$CDP_HEADED_PROBE_CHILD_PID_FILE"
while :; do /bin/sleep 1; done
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CDP_HEADED_PROBE_CHILD_PID_FILE", childPIDPath)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{}, 1)
	go func() {
		_, _ = chromeProcessRunning(ctx, "/synthetic/chrome")
		done <- struct{}{}
	}()
	childPID := waitForHeadedProbeChildPID(t, childPIDPath)
	t.Cleanup(func() {
		if process, err := os.FindProcess(childPID); err == nil {
			_ = process.Kill()
		}
	})

	select {
	case <-done:
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && headedProbeProcessAlive(childPID) {
			time.Sleep(20 * time.Millisecond)
		}
		if headedProbeProcessAlive(childPID) {
			t.Fatalf("chromeProcessRunning returned while probe descendant %d remained alive", childPID)
		}
	case <-time.After(2 * time.Second):
		if process, err := os.FindProcess(childPID); err == nil {
			_ = process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("chromeProcessRunning remained blocked after probe descendant cleanup")
		}
		t.Fatalf("chromeProcessRunning left probe descendant %d alive after cancellation", childPID)
	}
}

func TestChromeProcessRunningMatchesCompleteBoundedTable(t *testing.T) {
	binDir := t.TempDir()
	writeHeadedProbeFixture(t, binDir, "#!/bin/sh\nprintf '%s\\n' '420 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	running, err := chromeProcessRunning(context.Background(), "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	if err != nil || !running {
		t.Fatalf("chromeProcessRunning() = %v, %v; want complete-table match", running, err)
	}
}

func TestChromeProcessRunningRejectsOversizedTable(t *testing.T) {
	binDir := t.TempDir()
	writeHeadedProbeFixture(t, binDir, `#!/bin/sh
i=0
while [ "$i" -lt 7000 ]; do
  printf '0123456789'
  i=$((i + 1))
done
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := chromeProcessRunning(context.Background(), "/synthetic/chrome")
	if !errors.Is(err, errExternalProcessOutputTooLarge) {
		t.Fatalf("chromeProcessRunning() error = %v, want bounded-output error", err)
	}
}

func TestEnsureChromeForKeepaliveFailsClosedOnProcessProbeFailure(t *testing.T) {
	binDir := t.TempDir()
	writeHeadedProbeFixture(t, binDir, "#!/bin/sh\nexit 7\n")
	openPath := filepath.Join(binDir, "open")
	if err := os.WriteFile(openPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write open fixture: %v", err)
	}
	launchMarker := filepath.Join(t.TempDir(), "headed-launch-marker")
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	chromeScript := "#!/bin/sh\nprintf launched > \"" + launchMarker + "\"\n"
	if err := os.WriteFile(chromePath, []byte(chromeScript), 0o700); err != nil {
		t.Fatalf("write Chrome fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := ensureChromeForKeepalive(context.Background(), "", chromePath, nil)
	if err == nil {
		t.Fatal("ensureChromeForKeepalive returned nil error for failed process probe")
	}
	if _, statErr := os.Stat(launchMarker); statErr == nil {
		t.Fatal("ensureChromeForKeepalive launched a second headed browser after unknown process state")
	}
}

func writeHeadedProbeFixture(t *testing.T, binDir, body string) string {
	t.Helper()
	path := filepath.Join(binDir, "ps")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write headed process probe fixture: %v", err)
	}
	return path
}

func waitForHeadedProbeChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
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
	t.Fatalf("headed process probe fixture did not publish child PID")
	return 0
}

func headedProbeProcessAlive(pid int) bool {
	output, err := exec.Command("/bin/ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}
