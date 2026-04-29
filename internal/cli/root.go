package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/output"
	"github.com/spf13/cobra"
)

type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type options struct {
	json    bool
	jq      string
	debug   bool
	timeout time.Duration
	profile string
	config  string
}

type app struct {
	out   io.Writer
	err   io.Writer
	build BuildInfo
	opts  options
	root  *cobra.Command
}

func Execute(ctx context.Context, args []string, out, err io.Writer, build BuildInfo) int {
	a := &app{
		out:   out,
		err:   err,
		build: build,
		opts: options{
			profile: config.DefaultProfile,
		},
	}

	cmd := a.newRoot()
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(err)

	if runErr := cmd.ExecuteContext(ctx); runErr != nil {
		_ = a.renderError(ctx, runErr)
		return exitCode(runErr)
	}

	return ExitOK
}

func (a *app) newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "cdp",
		Short:         "Agent-oriented Chrome DevTools Protocol CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `cdp is a shell-first Chrome DevTools Protocol CLI for coding agents.

The project is being built around a long-running local attach daemon, compact
JSON output, jq-friendly filtering, and high-level browser debugging workflows.`,
	}
	a.root = root

	root.PersistentFlags().BoolVar(&a.opts.json, "json", false, "emit JSON on stdout")
	root.PersistentFlags().StringVar(&a.opts.jq, "jq", "", "filter JSON output with jq expression; implies --json")
	root.PersistentFlags().BoolVar(&a.opts.debug, "debug", false, "write debug details to stderr")
	root.PersistentFlags().DurationVar(&a.opts.timeout, "timeout", 0, "ceiling-bound command execution, such as 30s or 2m")
	root.PersistentFlags().StringVar(&a.opts.profile, "profile", config.DefaultProfile, "named cdp-cli profile to use")
	root.PersistentFlags().StringVar(&a.opts.config, "config", "", "path to config file")

	root.AddCommand(a.newVersionCommand())
	root.AddCommand(a.newDescribeCommand())
	root.AddCommand(a.newDoctorCommand())
	root.AddCommand(a.newDaemonCommand())
	root.AddCommand(a.newTargetsCommand())
	root.AddCommand(a.newPagesCommand())
	root.AddCommand(a.newOpenCommand())
	root.AddCommand(a.newEvalCommand())
	root.AddCommand(a.newSnapshotCommand())
	root.AddCommand(a.newScreenshotCommand())
	root.AddCommand(a.newConsoleCommand())
	root.AddCommand(a.newNetworkCommand())
	root.AddCommand(a.newCDPCommand())
	root.AddCommand(a.newWorkflowCommand())
	root.AddCommand(a.newMCPCommand())

	return root
}

func (a *app) commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	ctx := cmd.Context()
	if a.opts.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, a.opts.timeout)
}

func (a *app) render(ctx context.Context, human string, data any) error {
	return output.Render(ctx, a.out, output.Options{
		JSON: a.opts.json,
		JQ:   a.opts.jq,
	}, human, data)
}

func (a *app) renderError(ctx context.Context, err error) error {
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		cmdErr = &CommandError{
			Code:     "internal",
			Class:    "internal",
			Message:  err.Error(),
			ExitCode: ExitInternal,
		}
	}

	env := output.Envelope{
		OK:                  false,
		Code:                cmdErr.Code,
		ErrClass:            cmdErr.Class,
		Message:             cmdErr.Error(),
		RemediationCommands: cmdErr.RemediationCommands,
	}

	if a.opts.json || a.opts.jq != "" {
		return output.Render(ctx, a.out, output.Options{JSON: true, JQ: a.opts.jq}, "", env)
	}

	_, writeErr := fmt.Fprintln(a.err, env.Message)
	return writeErr
}
