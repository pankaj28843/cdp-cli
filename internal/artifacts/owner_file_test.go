package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteOwnerOnlyFileAtomicPermissionsAndReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	if err := WriteOwnerOnlyFileAtomic(path, []byte("first\n")); err != nil {
		t.Fatalf("first atomic write: %v", err)
	}
	if err := WriteOwnerOnlyFileAtomic(path, []byte("second\n")); err != nil {
		t.Fatalf("second atomic write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if string(data) != "second\n" {
		t.Fatalf("state = %q, want second", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state mode = %o, want 600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("parent mode = %o, want 700", got)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".*.tmp-*"))
	if err != nil {
		t.Fatalf("glob temporaries: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %#v", matches)
	}
}

func TestOwnerOnlyFileOperationsRejectSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if err := WriteOwnerOnlyFileAtomic(link, []byte("changed")); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("atomic symlink write error = %v", err)
	}
	if _, err := ReadOwnerOnlyFile(link); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("read symlink error = %v", err)
	}
	if err := WithOwnerOnlyFileLock(context.Background(), link, func() error { return nil }); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("symlink lock error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "unchanged" {
		t.Fatalf("symlink target changed to %q", data)
	}
}

func TestReadOwnerOnlyFileRejectsGroupOrWorldAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("private\n"), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if _, err := ReadOwnerOnlyFile(path); err == nil || !strings.Contains(err.Error(), "group or world access") {
		t.Fatalf("read permissive file error = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod state: %v", err)
	}
	data, err := ReadOwnerOnlyFile(path)
	if err != nil {
		t.Fatalf("read owner-only state: %v", err)
	}
	if string(data) != "private\n" {
		t.Fatalf("state = %q", data)
	}
}

func TestWithOwnerOnlyFileLockHonorsContext(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "state.lock")
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithOwnerOnlyFileLock(context.Background(), lockPath, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := WithOwnerOnlyFileLock(ctx, lockPath, func() error {
		t.Fatal("second lock callback must not run")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("second lock error = %v, want context deadline", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first lock: %v", err)
	}
}

func TestTryAcquireOwnerOnlyFileLockReportsBusyWithoutWaiting(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "state.lock")
	first, acquired, err := TryAcquireOwnerOnlyFileLock(lockPath)
	if err != nil || !acquired {
		t.Fatalf("first try lock = acquired %v, err %v", acquired, err)
	}
	defer first.Release()

	start := time.Now()
	second, acquired, err := TryAcquireOwnerOnlyFileLock(lockPath)
	if err != nil || acquired || second != nil {
		t.Fatalf("second try lock = lock %v, acquired %v, err %v; want ordinary busy state", second, acquired, err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("busy try lock took %s, want non-blocking result", elapsed)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}
	third, acquired, err := TryAcquireOwnerOnlyFileLock(lockPath)
	if err != nil || !acquired {
		t.Fatalf("third try lock = acquired %v, err %v", acquired, err)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("release third lock: %v", err)
	}
}
