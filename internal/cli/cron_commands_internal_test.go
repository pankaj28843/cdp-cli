package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCronLockStateEntryClassifiesLongHeldEmptyFlockMarker(t *testing.T) {
	flock, err := exec.LookPath("flock")
	if err != nil {
		t.Skip("flock is not available")
	}
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, "locks", "keepalive-headless.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("write flock marker: %v", err)
	}

	lockCmd := exec.Command(flock, "-n", path, "sleep", "30")
	if err := lockCmd.Start(); err != nil {
		t.Fatalf("start flock holder: %v", err)
	}
	defer func() {
		if lockCmd.Process != nil {
			_ = lockCmd.Process.Kill()
		}
		_ = lockCmd.Wait()
	}()
	waitForInternalFlockHeld(t, flock, path)

	oldOwnerLookup := cronFlockOwnerForPath
	cronFlockOwnerForPath = func(candidate string) (cronFlockOwner, bool) {
		if candidate != path {
			return cronFlockOwner{}, false
		}
		return cronFlockOwner{PID: lockCmd.Process.Pid, Age: 20 * time.Minute}, true
	}
	t.Cleanup(func() {
		cronFlockOwnerForPath = oldOwnerLookup
	})

	entry := cronLockStateEntry("keepalive-headless", path, 10*time.Minute)
	if entry["marker"] != "flock_lockfile" || entry["locked"] != true || entry["stale"] != true || entry["stale_reason"] != "flock_lock_held_too_long" {
		t.Fatalf("cron lock entry = %+v, want long-held empty flock marker stale", entry)
	}
	if entry["lock_owner_pid"] != lockCmd.Process.Pid {
		t.Fatalf("lock owner pid = %v, want %d", entry["lock_owner_pid"], lockCmd.Process.Pid)
	}
	nextCommands, ok := entry["next_commands"].([]string)
	if !ok || !internalStringSliceContains(nextCommands, "cdp --browser-mode headless daemon stop --json") {
		t.Fatalf("next commands = %+v, want daemon stop guidance for inherited flock lock", entry["next_commands"])
	}
}

func waitForInternalFlockHeld(t *testing.T, flock, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := exec.Command(flock, "-n", path, "true").Run()
		if err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("lock %s was not held before deadline", path)
}

func internalStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
