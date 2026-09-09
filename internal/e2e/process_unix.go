//go:build unix

package e2e

import (
	"os"
	"os/exec"
	"syscall"
)

func configureOwnedProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalOwnedProcess(command *exec.Cmd, signal os.Signal) {
	if command.Process == nil {
		return
	}
	value, valid := signal.(syscall.Signal)
	if !valid {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, value)
}

func interruptSignal() os.Signal {
	return syscall.SIGINT
}

func terminateSignal() os.Signal {
	return syscall.SIGTERM
}

func killSignal() os.Signal {
	return syscall.SIGKILL
}
