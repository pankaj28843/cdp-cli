package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func TestStaleManagedRegistryLockRejectsRecycledPID(t *testing.T) {
	path := managedRegistryLockFixturePath(t)
	writeManagedRegistryLockFixture(t, path, managedRegistryLockRecord{
		PID:              os.Getpid(),
		CreatedAt:        time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
		ProcessStartTime: "proc:not-the-live-process",
	})

	if !staleManagedRegistryLock(context.Background(), path, time.Minute) {
		t.Fatal("staleManagedRegistryLock returned false for a recycled PID")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("recycled-PID lock stat error = %v, want removed lock", err)
	}
}

func TestStaleManagedRegistryLockKeepsMatchingOwner(t *testing.T) {
	token, err := processgroup.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	path := managedRegistryLockFixturePath(t)
	writeManagedRegistryLockFixture(t, path, managedRegistryLockRecord{
		PID:              os.Getpid(),
		CreatedAt:        time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
		ProcessStartTime: token,
	})

	if staleManagedRegistryLock(context.Background(), path, time.Minute) {
		t.Fatal("staleManagedRegistryLock removed a lock whose process identity matched")
	}
	assertManagedRegistryLockExists(t, path)
}

func TestStaleManagedRegistryLockKeepsUnavailableOwner(t *testing.T) {
	path := managedRegistryLockFixturePath(t)
	writeManagedRegistryLockFixture(t, path, managedRegistryLockRecord{
		PID:              os.Getpid(),
		CreatedAt:        time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
		ProcessStartTime: "proc:expected-but-unavailable",
	})

	original := managedRegistryLockProcessStartTime
	managedRegistryLockProcessStartTime = func(context.Context, int) (string, error) {
		return "", errors.New("synthetic identity unavailable")
	}
	t.Cleanup(func() { managedRegistryLockProcessStartTime = original })

	if staleManagedRegistryLock(context.Background(), path, time.Minute) {
		t.Fatal("staleManagedRegistryLock removed an unverifiable live owner")
	}
	assertManagedRegistryLockExists(t, path)
}

func TestStaleManagedRegistryLockKeepsLegacyOwnerCompatible(t *testing.T) {
	path := managedRegistryLockFixturePath(t)
	writeManagedRegistryLockFixture(t, path, managedRegistryLockRecord{
		PID:       os.Getpid(),
		CreatedAt: time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
	})

	if staleManagedRegistryLock(context.Background(), path, time.Minute) {
		t.Fatal("staleManagedRegistryLock removed a legacy PID-only owner")
	}
	assertManagedRegistryLockExists(t, path)
}

func TestStaleManagedRegistryLockRemovesDeadOwner(t *testing.T) {
	path := managedRegistryLockFixturePath(t)
	writeManagedRegistryLockFixture(t, path, managedRegistryLockRecord{
		PID:       999999,
		CreatedAt: time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
	})

	if !staleManagedRegistryLock(context.Background(), path, time.Minute) {
		t.Fatal("staleManagedRegistryLock returned false for a dead owner")
	}
}

func TestStaleManagedRegistryLockHonorsCancellation(t *testing.T) {
	path := managedRegistryLockFixturePath(t)
	writeManagedRegistryLockFixture(t, path, managedRegistryLockRecord{
		PID:       os.Getpid(),
		CreatedAt: time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if staleManagedRegistryLock(ctx, path, time.Minute) {
		t.Fatal("staleManagedRegistryLock removed a lock after cancellation")
	}
	assertManagedRegistryLockExists(t, path)
}

func TestStaleManagedRegistryLockHonorsCancellationDuringIdentityProbe(t *testing.T) {
	path := managedRegistryLockFixturePath(t)
	writeManagedRegistryLockFixture(t, path, managedRegistryLockRecord{
		PID:              os.Getpid(),
		CreatedAt:        time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
		ProcessStartTime: "proc:expected-but-canceled",
	})

	original := managedRegistryLockProcessStartTime
	managedRegistryLockProcessStartTime = func(ctx context.Context, _ int) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	t.Cleanup(func() { managedRegistryLockProcessStartTime = original })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if staleManagedRegistryLock(ctx, path, time.Minute) {
		t.Fatal("staleManagedRegistryLock removed a lock after identity-probe cancellation")
	}
	assertManagedRegistryLockExists(t, path)
}

func TestManagedRegistryLockOwnerStatusHonorsCancellationBeforeProcessProbe(t *testing.T) {
	original := managedRegistryLockProcessProbe
	called := false
	managedRegistryLockProcessProbe = func(context.Context, int) (bool, error) {
		called = true
		return true, nil
	}
	t.Cleanup(func() { managedRegistryLockProcessProbe = original })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	running, identityState, err := managedRegistryLockOwnerStatus(ctx, managedRegistryLockRecord{
		PID:              os.Getpid(),
		ProcessStartTime: "proc:expected",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("managedRegistryLockOwnerStatus error = %v, want context.Canceled", err)
	}
	if running || identityState != "" {
		t.Fatalf("managedRegistryLockOwnerStatus = (%t, %q), want canceled unknown owner", running, identityState)
	}
	if called {
		t.Fatal("managedRegistryLockOwnerStatus invoked the process probe after cancellation")
	}
}

func TestStaleManagedRegistryLockHonorsCancellationAfterInitialLivenessProbe(t *testing.T) {
	path := managedRegistryLockFixturePath(t)
	writeManagedRegistryLockFixture(t, path, managedRegistryLockRecord{
		PID:              os.Getpid(),
		CreatedAt:        time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
		ProcessStartTime: "proc:expected",
	})

	originalProbe := managedRegistryLockProcessProbe
	originalIdentity := managedRegistryLockProcessStartTime
	processCalls := 0
	identityCalls := 0
	ctx, cancel := context.WithCancel(context.Background())
	managedRegistryLockProcessProbe = func(context.Context, int) (bool, error) {
		processCalls++
		cancel()
		return true, nil
	}
	managedRegistryLockProcessStartTime = func(context.Context, int) (string, error) {
		identityCalls++
		return "proc:expected", nil
	}
	t.Cleanup(func() {
		managedRegistryLockProcessProbe = originalProbe
		managedRegistryLockProcessStartTime = originalIdentity
		cancel()
	})

	if staleManagedRegistryLock(ctx, path, time.Minute) {
		t.Fatal("staleManagedRegistryLock removed a lock after initial liveness cancellation")
	}
	assertManagedRegistryLockExists(t, path)
	if processCalls != 1 {
		t.Fatalf("managed registry process probe calls = %d, want 1", processCalls)
	}
	if identityCalls != 0 {
		t.Fatalf("managed registry identity probe calls = %d, want 0 after cancellation", identityCalls)
	}
}

func TestStaleManagedRegistryLockHonorsCancellationBeforeSecondLivenessProbe(t *testing.T) {
	path := managedRegistryLockFixturePath(t)
	writeManagedRegistryLockFixture(t, path, managedRegistryLockRecord{
		PID:              os.Getpid(),
		CreatedAt:        time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
		ProcessStartTime: "proc:expected",
	})

	originalProbe := managedRegistryLockProcessProbe
	originalIdentity := managedRegistryLockProcessStartTime
	processCalls := 0
	ctx, cancel := context.WithCancel(context.Background())
	managedRegistryLockProcessProbe = func(context.Context, int) (bool, error) {
		processCalls++
		return true, nil
	}
	managedRegistryLockProcessStartTime = func(context.Context, int) (string, error) {
		cancel()
		return "", errors.New("synthetic identity unavailable")
	}
	t.Cleanup(func() {
		managedRegistryLockProcessProbe = originalProbe
		managedRegistryLockProcessStartTime = originalIdentity
		cancel()
	})

	if staleManagedRegistryLock(ctx, path, time.Minute) {
		t.Fatal("staleManagedRegistryLock removed a lock after second-probe cancellation")
	}
	assertManagedRegistryLockExists(t, path)
	if processCalls != 1 {
		t.Fatalf("managed registry process probe calls = %d, want 1", processCalls)
	}
}

func TestStaleManagedRegistryLockDoesNotRemoveReplacementFile(t *testing.T) {
	path := managedRegistryLockFixturePath(t)
	writeManagedRegistryLockFixture(t, path, managedRegistryLockRecord{
		PID:              os.Getpid(),
		CreatedAt:        time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
		ProcessStartTime: "proc:old-owner",
	})

	original := managedRegistryLockProcessStartTime
	managedRegistryLockProcessStartTime = func(_ context.Context, _ int) (string, error) {
		replacement := managedRegistryLockRecord{
			PID:       999999,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		data, err := json.Marshal(replacement)
		if err != nil {
			return "", err
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".managed-processes-replacement-*")
		if err != nil {
			return "", err
		}
		tmpPath := tmp.Name()
		if _, err := tmp.Write(append(data, '\n')); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return "", err
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpPath)
			return "", err
		}
		if err := os.Rename(tmpPath, path); err != nil {
			_ = os.Remove(tmpPath)
			return "", err
		}
		return "proc:new-owner", nil
	}
	t.Cleanup(func() { managedRegistryLockProcessStartTime = original })

	if staleManagedRegistryLock(context.Background(), path, time.Minute) {
		t.Fatal("staleManagedRegistryLock removed a replacement file")
	}
	assertManagedRegistryLockExists(t, path)
}

func TestManagedRegistryLockCapturesPrivateIdentity(t *testing.T) {
	token, err := processgroup.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	stateDir := t.TempDir()
	var record managedRegistryLockRecord
	err = withManagedRegistryLock(context.Background(), stateDir, func() error {
		data, readErr := os.ReadFile(filepath.Join(stateDir, "browser", ".managed-processes.lock"))
		if readErr != nil {
			return readErr
		}
		return json.Unmarshal(data, &record)
	})
	if err != nil {
		t.Fatalf("withManagedRegistryLock returned error: %v", err)
	}
	if record.ProcessStartTime != token {
		t.Fatalf("managed registry lock process identity = %q, want private token %q", record.ProcessStartTime, token)
	}
}

func TestManagedRegistryLockFallsBackWhenIdentityUnavailable(t *testing.T) {
	original := managedRegistryLockProcessStartTime
	managedRegistryLockProcessStartTime = func(context.Context, int) (string, error) {
		return "", errors.New("synthetic identity unavailable")
	}
	t.Cleanup(func() { managedRegistryLockProcessStartTime = original })

	stateDir := t.TempDir()
	var record managedRegistryLockRecord
	err := withManagedRegistryLock(context.Background(), stateDir, func() error {
		data, readErr := os.ReadFile(filepath.Join(stateDir, "browser", ".managed-processes.lock"))
		if readErr != nil {
			return readErr
		}
		return json.Unmarshal(data, &record)
	})
	if err != nil {
		t.Fatalf("withManagedRegistryLock returned error: %v", err)
	}
	if record.ProcessStartTime != "" {
		t.Fatalf("managed registry lock recorded unavailable process identity %q", record.ProcessStartTime)
	}
}

func managedRegistryLockFixturePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".managed-processes.lock")
	return path
}

func writeManagedRegistryLockFixture(t *testing.T, path string, record managedRegistryLockRecord) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal managed registry lock fixture: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write managed registry lock fixture: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("age managed registry lock fixture: %v", err)
	}
}

func assertManagedRegistryLockExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("managed registry lock stat error = %v, want retained lock", err)
	}
}
