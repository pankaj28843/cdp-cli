package cli

import (
	"context"
	"io"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func runLighthouseCommand(ctx context.Context, bin string, args []string, stdout, stderr io.Writer) error {
	return runOwnedCommand(ctx, bin, args, stdout, stderr)
}

func runOwnedCommand(ctx context.Context, bin string, args []string, stdout, stderr io.Writer) error {
	return processgroup.Run(ctx, bin, args, stdout, stderr)
}
