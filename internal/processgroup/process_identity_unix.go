//go:build unix

package processgroup

import (
	"os"
	"path/filepath"
	"strconv"
)

func processStartTimeFromProc(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	return parseProcStatStartTime(string(data))
}
