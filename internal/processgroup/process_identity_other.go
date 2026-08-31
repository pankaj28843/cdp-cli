//go:build !unix

package processgroup

import "errors"

func processStartTimeFromProc(int) (string, error) {
	return "", errors.New("/proc process identity is unavailable")
}
