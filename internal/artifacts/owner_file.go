package artifacts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const ownerLockPollInterval = 10 * time.Millisecond

// OwnerOnlyFileLock is a process-scoped advisory lock backed by a persistent
// owner-only file. The operating system releases the lock if the process exits.
type OwnerOnlyFileLock struct {
	path string
	file *os.File

	mu       sync.Mutex
	released bool
}

// TryAcquireOwnerOnlyFileLock attempts one non-blocking exclusive lock. A
// false acquired result is an ordinary busy state, not an error.
func TryAcquireOwnerOnlyFileLock(lockPath string) (*OwnerOnlyFileLock, bool, error) {
	file, err := openOwnerOnlyLockFile(lockPath)
	if err != nil {
		return nil, false, err
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return &OwnerOnlyFileLock{path: lockPath, file: file}, true, nil
	}
	_ = file.Close()
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("lock owner-only file %s: %w", lockPath, err)
}

// AcquireOwnerOnlyFileLock waits until lockPath can be exclusively locked or
// the context ends. Callers must release the returned handle.
func AcquireOwnerOnlyFileLock(ctx context.Context, lockPath string) (*OwnerOnlyFileLock, error) {
	file, err := openOwnerOnlyLockFile(lockPath)
	if err != nil {
		return nil, err
	}

	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &OwnerOnlyFileLock{path: lockPath, file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("lock owner-only file %s: %w", lockPath, err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("wait for owner-only lock %s: %w", lockPath, ctx.Err())
		case <-time.After(ownerLockPollInterval):
		}
	}
}

func openOwnerOnlyLockFile(lockPath string) (*os.File, error) {
	if err := ensureOwnerOnlyParent(lockPath); err != nil {
		return nil, err
	}
	if err := rejectSymlink(lockPath); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open owner-only lock %s: %w", lockPath, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("restrict owner-only lock %s: %w", lockPath, err)
	}
	return file, nil
}

// Release unlocks and closes the lock handle. It is safe to call more than once.
func (l *OwnerOnlyFileLock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		unlockErr = fmt.Errorf("unlock owner-only file %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close owner-only lock %s: %w", l.path, closeErr)
	}
	return errors.Join(unlockErr, closeErr)
}

// WithOwnerOnlyFileLock serializes a state-file transaction across processes.
// The lock file is retained to avoid unlink/inode races between waiters.
func WithOwnerOnlyFileLock(ctx context.Context, lockPath string, fn func() error) error {
	if fn == nil {
		return fmt.Errorf("locked operation is required")
	}
	lock, err := AcquireOwnerOnlyFileLock(ctx, lockPath)
	if err != nil {
		return err
	}
	return errors.Join(fn(), lock.Release())
}

// WriteOwnerOnlyFileAtomic replaces path through a synced same-directory
// temporary file, atomic rename, and directory sync. Callers which perform a
// read-modify-write transaction must also hold WithOwnerOnlyFileLock.
func WriteOwnerOnlyFileAtomic(path string, data []byte) error {
	if err := ensureOwnerOnlyParent(path); err != nil {
		return err
	}
	if err := rejectSymlink(path); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create owner-only temporary file for %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict owner-only temporary file %s: %w", tmpPath, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write owner-only temporary file %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync owner-only temporary file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close owner-only temporary file %s: %w", tmpPath, err)
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace owner-only file %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict owner-only file %s: %w", path, err)
	}
	dirHandle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open owner-only parent directory %s: %w", dir, err)
	}
	defer dirHandle.Close()
	if err := dirHandle.Sync(); err != nil {
		return fmt.Errorf("sync owner-only parent directory %s: %w", dir, err)
	}
	return nil
}

// ReadOwnerOnlyFile reads one regular, non-symlink state file and rejects
// group/world access. Callers that need a transaction must hold the matching
// owner-only file lock around the read and any subsequent write.
func ReadOwnerOnlyFile(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("owner-only file path is required")
	}
	if err := rejectSymlink(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat owner-only file %s: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("owner-only file %s must not grant group or world access", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read owner-only file %s: %w", path, err)
	}
	return data, nil
}

func ensureOwnerOnlyParent(path string) error {
	if path == "" {
		return fmt.Errorf("owner-only file path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create owner-only directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restrict owner-only directory %s: %w", dir, err)
	}
	return nil
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect owner-only path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("owner-only path %s must not be a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("owner-only path %s must be a regular file", path)
	}
	return nil
}
