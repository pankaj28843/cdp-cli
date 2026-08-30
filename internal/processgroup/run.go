// Package processgroup runs short-lived external tools with cancellation
// scoped to the process started by the caller.
package processgroup

import (
	"context"
	"io"
	"os/exec"
)

type Options struct {
	Env        []string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	NewSession bool
}

// StartWithOptions starts bin with process ownership configured by this
// package and returns the still-owned command. The caller must either call
// Terminate followed by Cmd.Wait on a failed setup path, or call Detach once
// readiness has been proven and the process is intentionally being detached.
func StartWithOptions(bin string, args []string, options Options) (*exec.Cmd, error) {
	command := exec.Command(bin, args...)
	command.Env = options.Env
	command.Stdin = options.Stdin
	command.Stdout = options.Stdout
	command.Stderr = options.Stderr
	configure(command, options)
	if err := command.Start(); err != nil {
		return nil, err
	}
	return command, nil
}

// Detach transfers the command's eventual reaping to a background waiter.
// This keeps a long-lived child independent of the caller while ensuring that
// a child which exits before its parent does not remain a zombie. Callers must
// not call Wait or Process.Release after Detach.
func Detach(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	go func() {
		_ = command.Wait()
	}()
}

// Terminate stops an already-started command using the ownership boundary
// configured by this package's platform implementation. The caller must
// still call Cmd.Wait to reap the command. This is useful when setup work
// fails after Start but before a long-lived owner intentionally detaches.
func Terminate(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	terminate(command)
}

// TerminatePID force-stops the exact process ownership boundary represented by
// pid. Callers must have independently established that pid is their owned
// process-group/session leader; this function never searches by name.
func TerminatePID(pid int) error {
	if pid <= 0 {
		return nil
	}
	return terminatePID(pid)
}

// Run starts bin with the supplied arguments and waits for it to exit. If ctx
// is canceled first, only the owned process tree is terminated where the
// platform supports process groups, and ctx.Err() is returned after Wait has
// completed. stdout and stderr are caller-owned; callers that retain output
// must provide bounded writers.
func Run(ctx context.Context, bin string, args []string, stdout, stderr io.Writer) error {
	return RunWithOptions(ctx, bin, args, Options{}, stdout, stderr)
}

// RunWithOptions is Run with process attributes that are safe for the caller
// to supply. A non-nil Env replaces the inherited environment exactly as
// os/exec.Cmd.Env does; process ownership and cancellation remain managed by
// this package.
func RunWithOptions(ctx context.Context, bin string, args []string, options Options, stdout, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command, err := StartWithOptions(bin, args, Options{
		Env:    options.Env,
		Stdin:  options.Stdin,
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		return err
	}

	waited := make(chan error, 1)
	go func() {
		waited <- command.Wait()
	}()
	select {
	case err := <-waited:
		if err := ctx.Err(); err != nil {
			return err
		}
		return err
	case <-ctx.Done():
		Terminate(command)
		<-waited
		return ctx.Err()
	}
}
