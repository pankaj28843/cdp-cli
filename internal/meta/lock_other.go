//go:build !unix

package meta

import (
	"errors"
	"os"
	"sync"
)

func acquireProcessLock(path string) (func(), bool, error) {
	file, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if errors.Is(err, os.ErrExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = file.Close()
			_ = os.Remove(path)
		})
	}, false, nil
}
