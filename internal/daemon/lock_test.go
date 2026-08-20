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

func TestInspectLockClassifiesExpiredLiveLease(t *testing.T) {
	stateDir := t.TempDir()
	name := "headed-remote-debugging-repair"
	path := writeLockFile(t, stateDir, name, daemon.LockMetadata{
		Name:      name,
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano),
		ExpiresAt: time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano),
		Phase:     "approval",
	})

	info := daemon.InspectLock(path, 10*time.Minute)
	if !info.Exists || !info.Stale || info.StaleReason != "lease_expired" || info.OwnerRunning == nil || !*info.OwnerRunning {
		t.Fatalf("InspectLock = %+v, want expired live lease", info)
	}
}

func TestAcquireLockWithLeaseRecordsExpiry(t *testing.T) {
	stateDir := t.TempDir()
	name := "headed-remote-debugging-repair"
	before := time.Now().UTC()
	lock, acquired, _, err := daemon.AcquireLockWithLease(context.Background(), stateDir, name, 0, 10*time.Minute, 20*time.Second, daemon.LockMetadata{Name: name, Phase: "starting"})
	if err != nil {
		t.Fatalf("AcquireLockWithLease returned error: %v", err)
	}
	if !acquired {
		t.Fatal("AcquireLockWithLease acquired=false, want true")
	}
	defer lock.Release()
	expires, err := time.Parse(time.RFC3339Nano, lock.Metadata.ExpiresAt)
	if err != nil {
		t.Fatalf("parse ExpiresAt %q: %v", lock.Metadata.ExpiresAt, err)
	}
	if !expires.After(before) || !expires.Before(before.Add(21*time.Second)) {
		t.Fatalf("ExpiresAt = %s, want roughly 20 seconds after %s", expires, before)
	}
}

func TestExpiredHandleCannotReleaseReplacement(t *testing.T) {
	stateDir := t.TempDir()
	name := "headed-remote-debugging-repair"
	old, acquired, _, err := daemon.AcquireLockWithLease(context.Background(), stateDir, name, 0, 10*time.Minute, time.Millisecond, daemon.LockMetadata{Name: name, Phase: "old"})
	if err != nil {
		t.Fatalf("acquire old lease: %v", err)
	}
	if !acquired {
		t.Fatal("old lease acquired=false, want true")
	}
	time.Sleep(5 * time.Millisecond)
	newLock, acquired, _, err := daemon.AcquireLockWithLease(context.Background(), stateDir, name, 0, 10*time.Minute, 20*time.Second, daemon.LockMetadata{Name: name, Phase: "new"})
	if err != nil {
		t.Fatalf("acquire replacement lease: %v", err)
	}
	if !acquired {
		t.Fatal("replacement lease acquired=false, want true")
	}
	defer newLock.Release()
	if err := old.Update(context.Background(), "stale"); err == nil {
		t.Fatal("old Update returned nil, want ownership error")
	}
	if err := old.Release(); err != nil {
		t.Fatalf("old Release returned error: %v", err)
	}
	info := daemon.InspectLock(newLock.Path, 10*time.Minute)
	if !info.Exists || info.Stale || info.Metadata.Phase != "new" {
		t.Fatalf("replacement lock after old Release = %+v, want live new lock", info)
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
