package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestAcquireLockKeepsActiveOwnerLock(t *testing.T) {
	stateDir := t.TempDir()
	name := "daemon-keepalive-headless-browser-url-headless"
	writeLockFile(t, stateDir, name, daemon.LockMetadata{
		Name:      name,
		PID:       os.Getpid(),
		StartedAt: "2099-01-01T00:00:00Z",
		Phase:     "active_probe",
	})

	_, acquired, existing, err := daemon.AcquireLock(context.Background(), stateDir, name, 0, 10*time.Minute, daemon.LockMetadata{Name: name})
	if err != nil {
		t.Fatalf("AcquireLock returned error: %v", err)
	}
	if acquired {
		t.Fatalf("AcquireLock acquired active-owner lock, want locked")
	}
	if existing.PID != os.Getpid() || existing.Phase != "active_probe" {
		t.Fatalf("existing lock = %+v, want active owner metadata", existing)
	}
}

func TestAcquireLockRemovesDeadOwnerLock(t *testing.T) {
	stateDir := t.TempDir()
	name := "daemon-keepalive-headless-browser-url-headless"
	deadPID := deadProcessPID(t)
	writeLockFile(t, stateDir, name, daemon.LockMetadata{
		Name:      name,
		PID:       deadPID,
		StartedAt: "2099-01-01T00:00:00Z",
		Phase:     "active_probe",
	})

	lock, acquired, existing, err := daemon.AcquireLock(context.Background(), stateDir, name, 0, 10*time.Minute, daemon.LockMetadata{Name: name, Phase: "checking"})
	if err != nil {
		t.Fatalf("AcquireLock returned error: %v", err)
	}
	if !acquired {
		t.Fatalf("AcquireLock acquired=%v existing=%+v, want dead-owner lock removed", acquired, existing)
	}
	if lock.Metadata.PID != os.Getpid() || lock.Metadata.Phase != "checking" {
		t.Fatalf("new lock metadata = %+v, want current process checking lock", lock.Metadata)
	}
	info := daemon.InspectLock(lock.Path, 10*time.Minute)
	if !info.Exists || info.Stale || info.OwnerRunning == nil || !*info.OwnerRunning {
		t.Fatalf("InspectLock after acquire = %+v, want live current-owner lock", info)
	}
}

func TestInspectLockClassifiesDeadOwner(t *testing.T) {
	stateDir := t.TempDir()
	name := "daemon-keepalive-headless-browser-url-headless"
	deadPID := deadProcessPID(t)
	path := writeLockFile(t, stateDir, name, daemon.LockMetadata{
		Name:      name,
		PID:       deadPID,
		StartedAt: "2099-01-01T00:00:00Z",
		Phase:     "active_probe",
	})

	info := daemon.InspectLock(path, 10*time.Minute)
	if !info.Exists || !info.Stale || info.StaleReason != "owner_process_not_running" || info.OwnerRunning == nil || *info.OwnerRunning {
		t.Fatalf("InspectLock = %+v, want dead owner stale state", info)
	}
}

func writeLockFile(t *testing.T, stateDir, name string, metadata daemon.LockMetadata) string {
	t.Helper()
	lockDir := filepath.Join(stateDir, "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	path := filepath.Join(lockDir, name+".lock")
	b, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}

func deadProcessPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait helper process: %v", err)
	}
	return pid
}
