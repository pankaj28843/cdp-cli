package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	cronFlockOwnerForPath = func(_ context.Context, candidate string) (cronFlockOwner, bool) {
		if candidate != path {
			return cronFlockOwner{}, false
		}
		return cronFlockOwner{PID: os.Getpid(), Age: 20 * time.Minute}, true
	}
	t.Cleanup(func() {
		cronFlockOwnerForPath = oldOwnerLookup
	})

	entry := cronLockStateEntry(context.Background(), "keepalive-headless", path, 10*time.Minute)
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

func TestCronFlockOwnerFromProcLocksReturnsOwnerAfterCompleteScan(t *testing.T) {
	stateDir := t.TempDir()
	marker := filepath.Join(stateDir, "marker.lock")
	if err := os.WriteFile(marker, []byte{}, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	locksPath := filepath.Join(stateDir, "locks")
	if err := os.WriteFile(locksPath, []byte(procLocksLineForMarker(t, marker)), 0o600); err != nil {
		t.Fatalf("write synthetic proc locks: %v", err)
	}

	oldPath := cronProcLocksPath
	cronProcLocksPath = locksPath
	t.Cleanup(func() { cronProcLocksPath = oldPath })

	owner, known := cronFlockOwnerFromProcLocks(context.Background(), marker)
	if !known || owner.PID != os.Getpid() {
		t.Fatalf("cronFlockOwnerFromProcLocks() = %+v known=%v, want current owner", owner, known)
	}
}

func TestCronFlockOwnerFromProcLocksRejectsOversizedCompleteScan(t *testing.T) {
	stateDir := t.TempDir()
	marker := filepath.Join(stateDir, "marker.lock")
	if err := os.WriteFile(marker, []byte{}, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	locksPath := filepath.Join(stateDir, "locks")
	content := procLocksLineForMarker(t, marker) + strings.Repeat("x", cronProcLocksMaxBytes) + "\n"
	if err := os.WriteFile(locksPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write oversized synthetic proc locks: %v", err)
	}

	oldPath := cronProcLocksPath
	cronProcLocksPath = locksPath
	t.Cleanup(func() { cronProcLocksPath = oldPath })

	if owner, known := cronFlockOwnerFromProcLocks(context.Background(), marker); known || owner.PID != 0 {
		t.Fatalf("cronFlockOwnerFromProcLocks() = %+v known=%v, want unknown after overflow", owner, known)
	}
}

func TestCronFlockOwnerFromProcLocksHonorsCancellationDuringScan(t *testing.T) {
	stateDir := t.TempDir()
	marker := filepath.Join(stateDir, "marker.lock")
	if err := os.WriteFile(marker, []byte{}, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	oldOpen := cronProcLocksOpen
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readCount := 0
	cronProcLocksOpen = func() (io.ReadCloser, error) {
		return io.NopCloser(&cronLockSlowReader{
			content: procLocksLineForMarker(t, marker),
			read: func() {
				readCount++
				cancel()
			},
		}), nil
	}
	t.Cleanup(func() { cronProcLocksOpen = oldOpen })

	owner, known := cronFlockOwnerFromProcLocks(ctx, marker)
	if known || owner.PID != 0 || readCount == 0 {
		t.Fatalf("cronFlockOwnerFromProcLocks() = %+v known=%v reads=%d, want unknown cancellation", owner, known, readCount)
	}
}

func TestCronFlockOwnerFromProcLocksRequiresACompleteSuccessfulScan(t *testing.T) {
	stateDir := t.TempDir()
	marker := filepath.Join(stateDir, "marker.lock")
	if err := os.WriteFile(marker, []byte{}, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	oldOpen := cronProcLocksOpen
	cronProcLocksOpen = func() (io.ReadCloser, error) {
		return io.NopCloser(&cronLockErrorReader{
			content: "malformed\n" + procLocksLineForMarker(t, marker),
		}), nil
	}
	t.Cleanup(func() { cronProcLocksOpen = oldOpen })

	owner, known := cronFlockOwnerFromProcLocks(context.Background(), marker)
	if known || owner.PID != 0 {
		t.Fatalf("cronFlockOwnerFromProcLocks() = %+v known=%v, want unknown after read error", owner, known)
	}
}

func TestCronLockStatesContextPropagatesInspectionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	locks, err := cronLockStatesContext(ctx, t.TempDir())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cronLockStatesContext error = %v, want context cancellation", err)
	}
	if locks == nil {
		t.Fatal("cronLockStatesContext returned nil partial map after cancellation")
	}
}

func TestCronFlockOwnerFromProcLocksKeepsNoMatchUnknown(t *testing.T) {
	stateDir := t.TempDir()
	marker := filepath.Join(stateDir, "marker.lock")
	if err := os.WriteFile(marker, []byte{}, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	locksPath := filepath.Join(stateDir, "locks")
	if err := os.WriteFile(locksPath, []byte("malformed\n1: POSIX ADVISORY WRITE 123 00:00:1 0 EOF\n"), 0o600); err != nil {
		t.Fatalf("write non-matching synthetic proc locks: %v", err)
	}

	oldPath := cronProcLocksPath
	cronProcLocksPath = locksPath
	t.Cleanup(func() { cronProcLocksPath = oldPath })

	owner, known := cronFlockOwnerFromProcLocks(context.Background(), marker)
	if known || owner.PID != 0 {
		t.Fatalf("cronFlockOwnerFromProcLocks() = %+v known=%v, want no owner", owner, known)
	}
}

func TestCronFlockOwnerFromProcLocksRejectsPreCancellation(t *testing.T) {
	oldOpen := cronProcLocksOpen
	opened := false
	cronProcLocksOpen = func() (io.ReadCloser, error) {
		opened = true
		return io.NopCloser(strings.NewReader("unexpected")), nil
	}
	t.Cleanup(func() { cronProcLocksOpen = oldOpen })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	owner, known := cronFlockOwnerFromProcLocks(ctx, filepath.Join(t.TempDir(), "missing-marker.lock"))
	if known || owner.PID != 0 || opened {
		t.Fatalf("cronFlockOwnerFromProcLocks() = %+v known=%v opened=%v, want pre-canceled unknown without open", owner, known, opened)
	}
}

func procLocksLineForMarker(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat marker: %v", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("marker stat type = %T, want syscall.Stat_t", info.Sys())
	}
	return fmt.Sprintf("1: FLOCK ADVISORY WRITE %d %s:%d 0 EOF\n", os.Getpid(), procLocksDevice(uint64(stat.Dev)), stat.Ino)
}

type cronLockSlowReader struct {
	content string
	read    func()
	done    bool
}

type cronLockErrorReader struct {
	content string
	done    bool
}

func (r *cronLockErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("synthetic proc locks read failure")
	}
	r.done = true
	return copy(p, r.content), nil
}

func (r *cronLockSlowReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	if r.read != nil {
		r.read()
	}
	copy(p, r.content)
	r.done = true
	return len(r.content), nil
}

func TestCronFlockMarkerProbeClassifiesExactExitStatus(t *testing.T) {
	flockPath := filepath.Join(t.TempDir(), "flock")
	if err := os.WriteFile(flockPath, []byte("#!/bin/sh\nexit \"$CDP_TEST_FLOCK_EXIT\"\n"), 0o700); err != nil {
		t.Fatalf("write fake flock: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(flockPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	cases := []struct {
		exit   string
		locked bool
		known  bool
	}{
		{exit: "0", locked: false, known: true},
		{exit: "1", locked: true, known: true},
		{exit: "2", locked: false, known: false},
	}
	for _, tc := range cases {
		t.Run("exit-"+tc.exit, func(t *testing.T) {
			t.Setenv("CDP_TEST_FLOCK_EXIT", tc.exit)
			locked, known := cronFlockMarkerLocked(context.Background(), filepath.Join(t.TempDir(), "marker.lock"))
			if locked != tc.locked || known != tc.known {
				t.Fatalf("cronFlockMarkerLocked() = locked=%v known=%v, want locked=%v known=%v", locked, known, tc.locked, tc.known)
			}
		})
	}
}

func TestCronFlockMarkerProbeHonorsPreCancellation(t *testing.T) {
	startedPath := filepath.Join(t.TempDir(), "started")
	flockPath := filepath.Join(t.TempDir(), "flock")
	if err := os.WriteFile(flockPath, []byte("#!/bin/sh\ntouch \""+startedPath+"\"\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake flock: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(flockPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	locked, known := cronFlockMarkerLocked(ctx, filepath.Join(t.TempDir(), "marker.lock"))
	if locked || known {
		t.Fatalf("cronFlockMarkerLocked() = locked=%v known=%v, want unknown after cancellation", locked, known)
	}
	if _, err := os.Stat(startedPath); !os.IsNotExist(err) {
		t.Fatalf("pre-canceled flock probe started fake command: stat error=%v", err)
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
