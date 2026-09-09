//go:build unix

package meta

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

func acquireProcessLock(path string) (func(), bool, error) {
	file, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return nil, false, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, false, err
	}
	err = syscall.Flock(
		int(file.Fd()),
		syscall.LOCK_EX|syscall.LOCK_NB,
	)
	if errors.Is(err, syscall.EWOULDBLOCK) ||
		errors.Is(err, syscall.EAGAIN) {
		_ = file.Close()
		return nil, true, nil
	}
	if err != nil {
		_ = file.Close()
		return nil, false, err
	}
	if err := file.Truncate(0); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, false, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, false, err
	}
	if _, err := fmt.Fprintf(
		file,
		"pid=%d\nstarted_at=%s\n",
		os.Getpid(),
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, false, err
	}
	if err := file.Sync(); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, false, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
		})
	}, false, nil
}
