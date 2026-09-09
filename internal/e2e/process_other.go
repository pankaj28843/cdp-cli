//go:build !unix

package e2e

import (
	"os"
	"os/exec"
)

func configureOwnedProcess(_ *exec.Cmd) {}

func signalOwnedProcess(command *exec.Cmd, signal os.Signal) {
	if command.Process != nil {
		_ = command.Process.Signal(signal)
	}
}

func interruptSignal() os.Signal {
	return os.Interrupt
}

func terminateSignal() os.Signal {
	return os.Interrupt
}

func killSignal() os.Signal {
	return os.Kill
}
