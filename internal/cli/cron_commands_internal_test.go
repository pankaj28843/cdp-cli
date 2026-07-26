package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCronLockStateEntryClassifiesLongHeldEmptyFlockMarker(t *testing.T) {
	_, err := exec.LookPath("flock")
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

	lockFile, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open flock marker: %v", err)
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
	}()
	if err := syscall.Flock(
		int(lockFile.Fd()),
		syscall.LOCK_EX|syscall.LOCK_NB,
	); err != nil {
		t.Fatalf("hold flock marker: %v", err)
	}

	oldOwnerLookup := cronFlockOwnerForPath
	cronFlockOwnerForPath = func(candidate string) (cronFlockOwner, bool) {
		if candidate != path {
			return cronFlockOwner{}, false
		}
		return cronFlockOwner{PID: os.Getpid(), Age: 20 * time.Minute}, true
	}
	t.Cleanup(func() {
		cronFlockOwnerForPath = oldOwnerLookup
	})

	entry := cronLockStateEntry("keepalive-headless", path, 10*time.Minute)
	if entry["marker"] != "flock_lockfile" || entry["locked"] != true || entry["stale"] != true || entry["stale_reason"] != "flock_lock_held_too_long" {
		t.Fatalf("cron lock entry = %+v, want long-held empty flock marker stale", entry)
	}
	if entry["lock_owner_pid"] != os.Getpid() {
		t.Fatalf("lock owner pid = %v, want %d", entry["lock_owner_pid"], os.Getpid())
	}
	nextCommands, ok := entry["next_commands"].([]string)
	if !ok || !internalStringSliceContains(nextCommands, "cdp --browser-mode headless daemon stop --json") {
		t.Fatalf("next commands = %+v, want daemon stop guidance for inherited flock lock", entry["next_commands"])
	}
}

func internalStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
