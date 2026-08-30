package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegisterManagedProcessLaunchContextRejectsPreCanceledContextWithoutIO(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RegisterManagedProcessLaunchContext(ctx, stateDir, ManagedMetadata{ChromePID: 4242})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RegisterManagedProcessLaunchContext error = %v, want context cancellation", err)
	}
	if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
		t.Fatalf("pre-canceled registration created state directory: stat error = %v", statErr)
	}
}

func TestRegisterManagedProcessLaunchContextStopsWhileRegistryLockIsContended(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	lockPath := filepath.Join(filepath.Dir(ManagedProcessRegistryPath(stateDir)), ".managed-processes.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatalf("create registry lock directory: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("synthetic-held-lock\n"), 0o600); err != nil {
		t.Fatalf("write held registry lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := RegisterManagedProcessLaunchContext(ctx, stateDir, ManagedMetadata{ChromePID: 4242})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RegisterManagedProcessLaunchContext error = %v, want deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("contended registration took %s, want prompt cancellation", elapsed)
	}
	if _, statErr := os.Stat(ManagedProcessRegistryPath(stateDir)); !os.IsNotExist(statErr) {
		t.Fatalf("canceled registration published registry state: stat error = %v", statErr)
	}
	if _, statErr := os.Stat(lockPath); statErr != nil {
		t.Fatalf("canceled registration removed the caller-held lock: %v", statErr)
	}
}

func TestRegisterManagedProcessLaunchContextStopsWhileProcessLockIsContended(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	managedRegistryProcessLock.Lock()
	released := false
	t.Cleanup(func() {
		if !released {
			managedRegistryProcessLock.Unlock()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RegisterManagedProcessLaunchContext(ctx, stateDir, ManagedMetadata{ChromePID: 4242})
	}()

	var err error
	select {
	case err = <-errCh:
	case <-time.After(time.Second):
		managedRegistryProcessLock.Unlock()
		released = true
		t.Fatal("registration did not stop after process-lock cancellation")
	}
	managedRegistryProcessLock.Unlock()
	released = true
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RegisterManagedProcessLaunchContext error = %v, want deadline", err)
	}
	if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
		t.Fatalf("canceled registration created state directory after process-lock wait: stat error = %v", statErr)
	}
}

func TestRegisterManagedProcessLaunchContextWritesCompatibleLiveRecord(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	metadata := ManagedMetadata{
		BrowserMode:      "headless",
		ChromePID:        4242,
		UserDataDir:      ManagedProfileDir(stateDir),
		ProcessStartTime: "proc:synthetic-start",
		OwnedMarker:      "synthetic-owned-marker",
	}

	if err := RegisterManagedProcessLaunchContext(context.Background(), stateDir, metadata); err != nil {
		t.Fatalf("RegisterManagedProcessLaunchContext returned error: %v", err)
	}
	registry, ok, err := LoadManagedProcessRegistry(stateDir)
	if err != nil || !ok || len(registry.Records) != 1 {
		t.Fatalf("managed registry ok=%v err=%v records=%d, want one live record", ok, err, len(registry.Records))
	}
	record := registry.Records[0]
	if record.PID != metadata.ChromePID || record.State != "live" || record.UserDataDir != metadata.UserDataDir || record.ProcessStartTime != metadata.ProcessStartTime || record.OwnershipMarker != metadata.OwnedMarker {
		t.Fatalf("registered record = %+v, want metadata-compatible live record", record)
	}
}
