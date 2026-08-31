package browser

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

// Browser accessibility helpers are short-lived and should never be allowed
// to grow an unbounded pipe buffer in the parent cdp process. Keeping this
// limit separate from the larger managed-process-table limit also makes a
// noisy or wedged native helper fail quickly.
const browserHelperMaxOutputBytes = 64 << 10

type browserOwnedCommandResult struct {
	stdout    []byte
	stderr    []byte
	truncated bool
}

type browserHelperOutput struct {
	buffer     bytes.Buffer
	truncated  bool
	onTruncate func()
}

func (b *browserHelperOutput) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	remaining := browserHelperMaxOutputBytes - b.buffer.Len()
	if remaining <= 0 {
		b.markTruncated()
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buffer.Write(p[:remaining])
		b.markTruncated()
		return len(p), nil
	}
	_, _ = b.buffer.Write(p)
	return len(p), nil
}

func (b *browserHelperOutput) markTruncated() {
	if b.truncated {
		return
	}
	b.truncated = true
	if b.onTruncate != nil {
		b.onTruncate()
	}
}

func runOwnedBrowserCommand(ctx context.Context, name string, args ...string) (browserOwnedCommandResult, error) {
	return runOwnedBrowserCommandWithInput(ctx, name, nil, args...)
}

func runOwnedBrowserCommandWithInput(ctx context.Context, name string, stdin io.Reader, args ...string) (browserOwnedCommandResult, error) {
	if err := ctx.Err(); err != nil {
		return browserOwnedCommandResult{}, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := browserHelperOutput{onTruncate: cancel}
	stderr := browserHelperOutput{onTruncate: cancel}
	err := processgroup.RunWithOptions(runCtx, name, args, processgroup.Options{Stdin: stdin}, &stdout, &stderr)
	result := browserOwnedCommandResult{
		stdout:    stdout.buffer.Bytes(),
		stderr:    stderr.buffer.Bytes(),
		truncated: stdout.truncated || stderr.truncated,
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if result.truncated {
		return result, fmt.Errorf("browser helper output exceeded %d bytes", browserHelperMaxOutputBytes)
	}
	return result, err
}
