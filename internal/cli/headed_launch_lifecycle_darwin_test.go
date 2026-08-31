//go:build darwin

package cli

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

func TestEnsureChromeForKeepaliveCleansOwnedLaunchBeforeDetach(t *testing.T) {
	binDir := t.TempDir()
	homeDir := t.TempDir()
	stateDir := filepath.Join(homeDir, "Library", "Application Support", "Google", "Chrome")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("create Chrome profile fixture: %v", err)
	}
	childPIDPath := filepath.Join(t.TempDir(), "headed-launch-child.pid")
	parentPIDPath := filepath.Join(t.TempDir(), "headed-launch-parent.pid")
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	chromeScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf '%s\\n' \"$$\" > \"$CDP_HEADED_LAUNCH_PARENT_PID_FILE\"\n" +
		"( trap '' TERM INT; exec /bin/sleep 30 ) &\n" +
		"child=$!\n" +
		"printf '%s\\n' \"$child\" > \"$CDP_HEADED_LAUNCH_CHILD_PID_FILE\"\n" +
		"while :; do /bin/sleep 1; done\n"
	if err := os.WriteFile(chromePath, []byte(chromeScript), 0o700); err != nil {
		t.Fatalf("write Chrome fixture: %v", err)
	}
	writeLaunchLifecycleFixture(t, binDir, "ps", "#!/bin/sh\nexit 0\n")
	writeLaunchLifecycleFixture(t, binDir, "open", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", homeDir)
	t.Setenv("CDP_HEADED_LAUNCH_CHILD_PID_FILE", childPIDPath)
	t.Setenv("CDP_HEADED_LAUNCH_PARENT_PID_FILE", parentPIDPath)

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

	status, err := ensureChromeForKeepalive(context.Background(), "", chromePath, nil)
	if err == nil {
		t.Fatalf("ensureChromeForKeepalive() status=%+v returned nil after headed-window readiness failure", status)
	}
	parentPID = waitForLaunchLifecyclePID(t, parentPIDPath)
	childPID = waitForLaunchLifecyclePID(t, childPIDPath)
	waitForLaunchLifecycleExit(t, parentPID)
	waitForLaunchLifecycleExit(t, childPID)
}

func writeLaunchLifecycleFixture(t *testing.T, binDir, name, body string) {
	t.Helper()
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write %s fixture: %v", name, err)
	}
}

func waitForLaunchLifecyclePID(t *testing.T, path string) int {
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
	t.Fatalf("headed launch fixture did not publish PID in %s", path)
	return 0
}

func waitForLaunchLifecycleExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !launchLifecycleProcessAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("owned headed launch process %d survived cleanup", pid)
}

func launchLifecycleProcessAlive(pid int) bool {
	output, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err == nil {
		fields := strings.Fields(string(output))
		return len(fields) > 2 && fields[2] != "Z"
	}
	output, err = launchLifecyclePS(pid)
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}

func launchLifecyclePS(pid int) ([]byte, error) {
	path := "/bin/ps"
	if _, err := os.Stat(path); err != nil {
		path = "/usr/bin/ps"
	}
	command := exec.Command(path, "-o", "state=", "-p", strconv.Itoa(pid))
	return command.Output()
}
