package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

var errExternalProcessOutputTooLarge = errors.New("external process output exceeded safety bound")

type boundedExternalCommandResult struct {
	stdout    string
	stderr    string
	truncated bool
}

func runBoundedExternalCommand(ctx context.Context, name string, args ...string) (boundedExternalCommandResult, error) {
	executable, err := exec.LookPath(name)
	if err != nil {
		return boundedExternalCommandResult{}, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &boundedProcessOutput{maxBytes: maxExternalProcessOutputBytes, onTruncate: cancel}
	stderr := &boundedProcessOutput{maxBytes: maxExternalProcessOutputBytes, onTruncate: cancel}
	runErr := processgroup.Run(runCtx, executable, args, stdout, stderr)
	result := boundedExternalCommandResult{
		stdout:    stdout.buffer.String(),
		stderr:    stderr.buffer.String(),
		truncated: stdout.truncated || stderr.truncated,
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if result.truncated {
		return result, fmt.Errorf("%w: limit=%d bytes", errExternalProcessOutputTooLarge, maxExternalProcessOutputBytes)
	}
	return result, runErr
}

func (result boundedExternalCommandResult) combinedOutput() string {
	switch {
	case result.stdout == "":
		return result.stderr
	case result.stderr == "":
		return result.stdout
	default:
		return result.stdout + "\n" + result.stderr
	}
}

func boundedExternalCommandDiagnostic(result boundedExternalCommandResult, err error) string {
	if result.truncated || errors.Is(err, errExternalProcessOutputTooLarge) {
		return fmt.Sprintf("external command output exceeded %d bytes", maxExternalProcessOutputBytes)
	}
	message := strings.TrimSpace(result.combinedOutput())
	if message == "" && err != nil {
		message = err.Error()
	}
	return message
}
