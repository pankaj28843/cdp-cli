//go:build !unix

package processgroup

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configure(*exec.Cmd, Options) {}

func terminate(command *exec.Cmd) {
	if command.Process != nil {
		_ = terminatePID(command.Process.Pid)
	}
}

func terminatePID(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	err = process.Kill()
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func TerminationMode() string {
	return "direct_process"
}
