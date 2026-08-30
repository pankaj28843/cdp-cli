package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func TestAcquireLockPersistsProcessIdentityPrivately(t *testing.T) {
	stateDir := t.TempDir()
	token, err := processgroup.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	lock, acquired, _, err := AcquireLock(context.Background(), stateDir, "identity-lock", 0, time.Hour, LockMetadata{})
	if err != nil || !acquired {
		t.Fatalf("AcquireLock = acquired=%v err=%v, want acquired lock", acquired, err)
	}
	defer lock.Release()
	if lock.Metadata.ProcessStartTime != token {
		t.Fatalf("lock process identity = %q, want %q", lock.Metadata.ProcessStartTime, token)
	}
	info := InspectLock(lock.Path, time.Hour)
	if info.Metadata.ProcessStartTime != token || info.Stale || info.OwnerRunning == nil || !*info.OwnerRunning {
		t.Fatalf("InspectLock = %+v, want matching private process identity", info)
	}
	raw, err := os.ReadFile(lock.Path)
	if err != nil {
		t.Fatalf("ReadFile lock: %v", err)
	}
	if !bytes.Contains(raw, []byte("process_start_time")) || !bytes.Contains(raw, []byte(token)) {
		t.Fatalf("lock file omitted private process identity: %s", raw)
	}
	publicJSON, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal lock info: %v", err)
	}
	if bytes.Contains(publicJSON, []byte("process_start_time")) || bytes.Contains(publicJSON, []byte(token)) {
		t.Fatalf("public lock info exposed process identity: %s", publicJSON)
	}
}

func TestInspectLockTreatsMismatchedProcessIdentityAsStale(t *testing.T) {
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, "locks", "identity-lock.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll lock directory: %v", err)
	}
	metadata := LockMetadata{
		Name:             "identity-lock",
		PID:              os.Getpid(),
		StartedAt:        "2099-01-01T00:00:00Z",
		ProcessStartTime: "proc:not-the-live-process",
	}
	if err := os.WriteFile(path, mustMarshalLockFile(metadata), 0o600); err != nil {
		t.Fatalf("WriteFile lock: %v", err)
	}
	info := InspectLock(path, time.Hour)
	if !info.Stale || info.StaleReason != "owner_process_identity_mismatch" || info.OwnerRunning == nil || *info.OwnerRunning {
		t.Fatalf("InspectLock = %+v, want mismatched identity stale state", info)
	}
	lock, acquired, existing, err := AcquireLock(context.Background(), stateDir, "identity-lock", 0, time.Hour, LockMetadata{})
	if err != nil || !acquired {
		t.Fatalf("AcquireLock after mismatch = acquired=%v err=%v existing=%+v, want replacement", acquired, err, existing)
	}
	defer lock.Release()
	if lock.Metadata.ProcessStartTime == "proc:not-the-live-process" {
		t.Fatal("replacement lock retained mismatched process identity")
	}
}

func TestUnavailableLockProcessIdentityFailsClosedForRecovery(t *testing.T) {
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, "locks", "identity-lock.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll lock directory: %v", err)
	}
	if err := os.WriteFile(path, mustMarshalLockFile(LockMetadata{
		Name:             "identity-lock",
		PID:              os.Getpid(),
		StartedAt:        "2099-01-01T00:00:00Z",
		ProcessStartTime: "proc:expected-but-unavailable",
	}), 0o600); err != nil {
		t.Fatalf("WriteFile lock: %v", err)
	}
	original := lockProcessStartTime
	lockProcessStartTime = func(context.Context, int) (string, error) {
		return "", errors.New("synthetic identity probe unavailable")
	}
	defer func() { lockProcessStartTime = original }()

	info := InspectLock(path, time.Hour)
	if !info.Stale || info.StaleReason != "owner_process_identity_unavailable" || info.OwnerRunning == nil || !*info.OwnerRunning {
		t.Fatalf("InspectLock = %+v, want held-but-unverifiable identity", info)
	}
	_, acquired, existing, err := AcquireLock(context.Background(), stateDir, "identity-lock", 0, time.Hour, LockMetadata{})
	if err != nil || acquired || existing.ProcessStartTime == "" {
		t.Fatalf("AcquireLock with unavailable identity = acquired=%v err=%v existing=%+v, want preserved lock", acquired, err, existing)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unverifiable lock was removed: %v", err)
	}
	cleanup, err := RemoveStaleLocks(context.Background(), stateDir, time.Hour, "identity-lock")
	if err != nil {
		t.Fatalf("RemoveStaleLocks returned error: %v", err)
	}
	if len(cleanup.Removed) != 0 {
		t.Fatalf("RemoveStaleLocks removed unverifiable lock: %+v", cleanup.Removed)
	}
}

func TestInspectLockContextHonorsCancellationDuringIdentityProbe(t *testing.T) {
	stateDir := t.TempDir()
	path := writeIdentityLockFile(t, stateDir, "identity-cancel", LockMetadata{
		Name:             "identity-cancel",
		PID:              os.Getpid(),
		StartedAt:        "2099-01-01T00:00:00Z",
		ProcessStartTime: "proc:expected-but-cancelled",
	})
	original := lockProcessStartTime
	started := make(chan struct{})
	lockProcessStartTime = func(ctx context.Context, _ int) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	t.Cleanup(func() { lockProcessStartTime = original })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		info LockInfo
		err  error
	}
	done := make(chan result, 1)
	go func() {
		info, err := InspectLockContext(ctx, path, time.Hour)
		done <- result{info: info, err: err}
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("identity probe did not start")
	}
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("InspectLockContext error = %v, want context cancellation", got.err)
		}
		if got.info.Stale {
			t.Fatalf("InspectLockContext returned stale evidence after cancellation: %+v", got.info)
		}
	case <-time.After(time.Second):
		t.Fatal("InspectLockContext remained blocked after cancellation")
	}
}

func TestInspectLockContextRejectsPreCancellationBeforeIdentityProbe(t *testing.T) {
	stateDir := t.TempDir()
	path := writeIdentityLockFile(t, stateDir, "identity-pre-cancel", LockMetadata{
		Name:             "identity-pre-cancel",
		PID:              os.Getpid(),
		StartedAt:        "2099-01-01T00:00:00Z",
		ProcessStartTime: "proc:should-not-probe",
	})
	original := lockProcessStartTime
	probed := false
	lockProcessStartTime = func(context.Context, int) (string, error) {
		probed = true
		return "", errors.New("identity probe should not run")
	}
	t.Cleanup(func() { lockProcessStartTime = original })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	info, err := InspectLockContext(ctx, path, time.Hour)
	if !errors.Is(err, context.Canceled) || probed || info.Stale {
		t.Fatalf("InspectLockContext = %+v err=%v probed=%v, want pre-canceled unknown inspection", info, err, probed)
	}
}

func TestRemoveStaleLocksDoesNotRemoveAfterInspectionCancellation(t *testing.T) {
	stateDir := t.TempDir()
	path := writeIdentityLockFile(t, stateDir, "identity-cleanup-cancel", LockMetadata{
		Name:             "identity-cleanup-cancel",
		PID:              os.Getpid(),
		StartedAt:        "2099-01-01T00:00:00Z",
		ProcessStartTime: "proc:cleanup-cancel",
	})
	original := lockProcessStartTime
	started := make(chan struct{})
	lockProcessStartTime = func(ctx context.Context, _ int) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	t.Cleanup(func() { lockProcessStartTime = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := RemoveStaleLocks(ctx, stateDir, time.Nanosecond, "identity-cleanup-cancel")
		done <- err
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("cleanup identity probe did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RemoveStaleLocks error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RemoveStaleLocks remained blocked after cancellation")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("canceled cleanup removed or lost lock %s: %v", path, err)
	}
}

func TestAcquireLockDoesNotReplaceAfterInspectionCancellation(t *testing.T) {
	stateDir := t.TempDir()
	name := "identity-acquire-cancel"
	path := writeIdentityLockFile(t, stateDir, name, LockMetadata{
		Name:             name,
		PID:              os.Getpid(),
		StartedAt:        "2099-01-01T00:00:00Z",
		ProcessStartTime: "proc:acquire-cancel",
	})
	original := lockProcessStartTime
	started := make(chan struct{})
	lockProcessStartTime = func(ctx context.Context, _ int) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	t.Cleanup(func() { lockProcessStartTime = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, _, err := AcquireLock(ctx, stateDir, name, time.Second, time.Nanosecond, LockMetadata{Name: name})
		done <- err
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("acquisition identity probe did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("AcquireLock error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AcquireLock remained blocked after cancellation")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("canceled acquisition removed or lost lock %s: %v", path, err)
	}
}

func mustMarshalLockFile(metadata LockMetadata) []byte {
	b, err := json.Marshal(lockFileMetadataFromLockMetadata(metadata))
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

func writeIdentityLockFile(t *testing.T, stateDir, name string, metadata LockMetadata) string {
	t.Helper()
	lockDir := filepath.Join(stateDir, "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll lock directory: %v", err)
	}
	path := filepath.Join(lockDir, name+".lock")
	if err := os.WriteFile(path, mustMarshalLockFile(metadata), 0o600); err != nil {
		t.Fatalf("WriteFile lock: %v", err)
	}
	return path
}
