//go:build unix

package processgroup

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

func configure(command *exec.Cmd, options Options) {
	command.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: !options.NewSession,
		Setsid:  options.NewSession,
	}
}

func terminate(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = terminatePID(command.Process.Pid)
}

func terminatePID(pid int) error {
	groupID, err := syscall.Getpgid(pid)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	if groupID != pid {
		return fmt.Errorf("pid %d is not a process-group leader (pgid %d)", pid, groupID)
	}
	err = syscall.Kill(-groupID, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func TerminationMode() string {
	return "process_group"
}
