//go:build unix

package daemon

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

func TestStartKeepAliveCleansOwnedLaunchTreeBeforeDetach(t *testing.T) {
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	stateDir := t.TempDir()
	childPIDPath := filepath.Join(t.TempDir(), "daemon-launch-child.pid")
	parentPIDPath := filepath.Join(t.TempDir(), "daemon-launch-parent.pid")
	launcherPath := filepath.Join(t.TempDir(), "fake-daemon-launcher")
	launcherScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf '%s\\n' \"$$\" > \"$CDP_DAEMON_LAUNCH_PARENT_PID_FILE\"\n" +
		"( trap '' TERM INT; exec /bin/sleep 30 ) &\n" +
		"child=$!\n" +
		"printf '%s\\n' \"$child\" > \"$CDP_DAEMON_LAUNCH_CHILD_PID_FILE\"\n" +
		"while :; do /bin/sleep 1; done\n"
	if err := os.WriteFile(launcherPath, []byte(launcherScript), 0o700); err != nil {
		t.Fatalf("write daemon launcher fixture: %v", err)
	}
	t.Setenv("CDP_DAEMON_LAUNCH_CHILD_PID_FILE", childPIDPath)
	t.Setenv("CDP_DAEMON_LAUNCH_PARENT_PID_FILE", parentPIDPath)

	parentPID := 0
	childPID := 0
	t.Cleanup(func() {
		for _, pid := range []int{parentPID, childPID} {
			if pid > 0 {
				if process, err := os.FindProcess(pid); err == nil {
					_ = process.Kill()
				}
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := StartKeepAliveForModeWithMetadata(ctx, launcherPath, stateDir, "headed", "ws://synthetic.invalid/devtools/browser/test", "browser_url", KeepAliveMetadata{}, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartKeepAliveForModeWithMetadata error = %v, want context deadline", err)
	}
	parentPID = waitForDaemonLaunchPID(t, parentPIDPath)
	childPID = waitForDaemonLaunchPID(t, childPIDPath)
	waitForDaemonLaunchExit(t, childPID)
}

func TestStopRuntimeTerminatesExactOwnedProcessGroup(t *testing.T) {
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	stateDir := t.TempDir()
	childPIDPath := filepath.Join(t.TempDir(), "daemon-stop-child.pid")
	launcherPath := filepath.Join(t.TempDir(), "fake-daemon-runtime")
	launcherScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"( trap '' TERM INT; exec /bin/sleep 30 ) &\n" +
		"child=$!\n" +
		"printf '%s\\n' \"$child\" > \"$CDP_DAEMON_STOP_CHILD_PID_FILE\"\n" +
		"trap '' TERM INT\n" +
		"while :; do /bin/sleep 1; done\n"
	if err := os.WriteFile(launcherPath, []byte(launcherScript), 0o700); err != nil {
		t.Fatalf("write daemon runtime fixture: %v", err)
	}
	t.Setenv("CDP_DAEMON_STOP_CHILD_PID_FILE", childPIDPath)
	command, err := processgroup.StartWithOptions(launcherPath, nil, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start daemon runtime fixture: %v", err)
	}
	childPID := waitForDaemonLaunchPID(t, childPIDPath)
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
		if process, findErr := os.FindProcess(childPID); findErr == nil {
			_ = process.Kill()
		}
	})
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headed", Runtime{
		PID:              command.Process.Pid,
		ProcessStartTime: mustProcessStartTime(t, command.Process.Pid),
		BrowserMode:      "headed",
		SocketPath:       filepath.Join(stateDir, RuntimeSocketFileName),
	}); err != nil {
		t.Fatalf("save runtime fixture: %v", err)
	}

	_, stopped, err := StopRuntimeForMode(context.Background(), stateDir, "headed")
	if err != nil || !stopped {
		t.Fatalf("StopRuntimeForMode() = stopped=%v err=%v, want exact runtime stop", stopped, err)
	}
	waitForDaemonLaunchExit(t, childPID)
}

func mustProcessStartTime(t *testing.T, pid int) string {
	t.Helper()
	token, err := processgroup.ProcessStartTime(context.Background(), pid)
	if err != nil {
		t.Fatalf("ProcessStartTime(%d) returned error: %v", pid, err)
	}
	return token
}

func waitForDaemonLaunchPID(t *testing.T, path string) int {
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
	t.Fatalf("daemon launch fixture did not publish PID in %s", path)
	return 0
}

func waitForDaemonLaunchExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !daemonLaunchProcessAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("owned daemon launch process %d survived cleanup", pid)
}

func daemonLaunchProcessAlive(pid int) bool {
	output, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err == nil {
		fields := strings.Fields(string(output))
		return len(fields) > 2 && fields[2] != "Z"
	}
	output, err = daemonLaunchProcessState(pid)
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}

func daemonLaunchProcessState(pid int) ([]byte, error) {
	path := "/bin/ps"
	if _, err := os.Stat(path); err != nil {
		path = "/usr/bin/ps"
	}
	return exec.Command(path, "-o", "state=", "-p", strconv.Itoa(pid)).Output()
}
