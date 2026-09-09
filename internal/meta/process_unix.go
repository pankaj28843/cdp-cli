//go:build unix

package meta

import (
	"os/exec"
	"syscall"
	"time"
)

const (
	commandInterruptGrace = 30 * time.Second
	commandTerminateGrace = 5 * time.Second
)

func prepareOwnedCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = commandTerminateGrace
}

func ownedCommandGroup(command *exec.Cmd) int {
	if command == nil || command.Process == nil {
		return 0
	}
	pid := command.Process.Pid
	if safeOwnedCommandGroup(pid, pid, syscall.Getpgrp()) {
		return pid
	}
	return 0
}

func stopOwnedCommand(
	command *exec.Cmd,
	processGroup int,
	waited <-chan error,
) error {
	for _, step := range []struct {
		signal syscall.Signal
		grace  time.Duration
	}{
		{signal: syscall.SIGINT, grace: commandInterruptGrace},
		{signal: syscall.SIGTERM, grace: commandTerminateGrace},
	} {
		signalOwnedCommand(command, processGroup, step.signal)
		timer := time.NewTimer(step.grace)
		select {
		case err := <-waited:
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
	signalOwnedCommand(command, processGroup, syscall.SIGKILL)
	return <-waited
}

func signalOwnedCommand(
	command *exec.Cmd,
	processGroup int,
	signal syscall.Signal,
) {
	if command == nil || command.Process == nil {
		return
	}
	pid := command.Process.Pid
	if pid <= 1 {
		return
	}
	if safeOwnedCommandGroup(pid, processGroup, syscall.Getpgrp()) {
		_ = syscall.Kill(-processGroup, signal)
		return
	}
	_ = command.Process.Signal(signal)
}

func safeOwnedCommandGroup(
	pid int,
	processGroup int,
	runnerProcessGroup int,
) bool {
	return pid > 1 &&
		processGroup > 1 &&
		processGroup == pid &&
		processGroup != runnerProcessGroup
}
