package browser

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

const managedProcessTableMaxBytes = 4 << 20

type managedProcessTableBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *managedProcessTableBuffer) Len() int {
	return b.buffer.Len()
}

func (b *managedProcessTableBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

func (b *managedProcessTableBuffer) Write(p []byte) (int, error) {
	remaining := managedProcessTableMaxBytes - b.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buffer.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	return b.buffer.Write(p)
}

func runManagedProcessTable(ctx context.Context, args ...string) ([]byte, error) {
	var output managedProcessTableBuffer
	if err := processgroup.Run(ctx, "ps", args, &output, io.Discard); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("run managed process table probe: %w", err)
	}
	if output.truncated {
		return nil, fmt.Errorf("managed process table output exceeded %d bytes", managedProcessTableMaxBytes)
	}
	return output.Bytes(), nil
}
