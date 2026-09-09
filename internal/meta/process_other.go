//go:build !unix

package meta

import "os/exec"

func prepareOwnedCommand(*exec.Cmd) {}

func ownedCommandGroup(*exec.Cmd) int {
	return 0
}

func stopOwnedCommand(
	command *exec.Cmd,
	_ int,
	waited <-chan error,
) error {
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
	}
	return <-waited
}
