package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/output"
	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
)

type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type options struct {
	json        bool
	jq          string
	debug       bool
	timeout     time.Duration
	profile     string
	config      string
	browserURL  string
	autoConnect bool
	channel     string
	userDataDir string
	stateDir    string
	activeProbe bool
	connection  string
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
	root.PersistentFlags().StringVar(&a.opts.browserURL, "browser-url", os.Getenv("CDP_BROWSER_URL"), "Chrome DevTools browser URL; can also be set with CDP_BROWSER_URL")
	root.PersistentFlags().StringVar(&a.opts.browserURL, "browserUrl", os.Getenv("CDP_BROWSER_URL"), "alias for --browser-url")
	root.PersistentFlags().BoolVar(&a.opts.autoConnect, "auto-connect", os.Getenv("CDP_AUTO_CONNECT") == "1" || os.Getenv("CDP_AUTO_CONNECT") == "true", "request Chrome's default-profile remote debugging flow when supported")
	root.PersistentFlags().BoolVar(&a.opts.autoConnect, "autoConnect", os.Getenv("CDP_AUTO_CONNECT") == "1" || os.Getenv("CDP_AUTO_CONNECT") == "true", "alias for --auto-connect")
	root.PersistentFlags().StringVar(&a.opts.channel, "channel", envDefault("CDP_CHANNEL", "stable"), "Chrome channel for --auto-connect: stable, beta, canary, or dev")
	root.PersistentFlags().StringVar(&a.opts.userDataDir, "user-data-dir", os.Getenv("CDP_USER_DATA_DIR"), "Chrome user data directory for --auto-connect")
	root.PersistentFlags().StringVar(&a.opts.stateDir, "state-dir", os.Getenv("CDP_STATE_DIR"), "directory for local cdp-cli state; defaults to $HOME/.cdp-cli")
	root.PersistentFlags().BoolVar(&a.opts.activeProbe, "active-browser-probe", os.Getenv("CDP_ACTIVE_BROWSER_PROBE") == "1" || os.Getenv("CDP_ACTIVE_BROWSER_PROBE") == "true", "actively connect to Chrome during auto-connect checks; may trigger a Chrome remote-debugging prompt")
	root.PersistentFlags().StringVar(&a.opts.connection, "connection", os.Getenv("CDP_CONNECTION"), "named browser connection from local state to use for this command")

	root.AddCommand(a.newVersionCommand())
	root.AddCommand(a.newDescribeCommand())
	root.AddCommand(a.newDoctorCommand())
	root.AddCommand(a.newExplainErrorCommand())
	root.AddCommand(a.newExitCodesCommand())
	root.AddCommand(a.newSchemaCommand())
	root.AddCommand(a.newDaemonCommand())
	root.AddCommand(a.newConnectionCommand())
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

func (a *app) browserProbe(ctx context.Context) (browser.ProbeResult, error) {
	opts, err := a.browserOptions(ctx)
	if err != nil {
		return browser.ProbeResult{}, err
	}
	return browser.Probe(ctx, opts)
}

func (a *app) browserEndpoint(ctx context.Context) (string, error) {
	opts, err := a.browserOptions(ctx)
	if err != nil {
		return "", err
	}
	if opts.AutoConnect && !opts.ActiveProbe {
		return "", fmt.Errorf("auto-connect browser attach is passive by default to avoid Chrome prompts; pass --active-browser-probe to attach directly")
	}
	return browser.ResolveEndpoint(ctx, opts)
}

func (a *app) browserProtocolURL(ctx context.Context) (string, error) {
	opts, err := a.browserOptions(ctx)
	if err != nil {
		return "", err
	}
	if opts.AutoConnect && !opts.ActiveProbe {
		return "", fmt.Errorf("auto-connect protocol discovery is passive by default to avoid Chrome prompts; pass --active-browser-probe to query Chrome directly")
	}
	return browser.ResolveProtocolURL(ctx, opts)
}

func (a *app) browserOptions(ctx context.Context) (browser.ProbeOptions, error) {
	if err := a.applySelectedConnection(ctx); err != nil {
		return browser.ProbeOptions{}, err
	}
	return browser.ProbeOptions{
		BrowserURL:  a.opts.browserURL,
		AutoConnect: a.opts.autoConnect,
		Channel:     a.opts.channel,
		UserDataDir: a.opts.userDataDir,
		ActiveProbe: a.opts.activeProbe,
	}, nil
}

func (a *app) connectionMode() string {
	if a.opts.autoConnect {
		return "auto_connect"
	}
	return "browser_url"
}

func (a *app) daemonStatus(probe browser.ProbeResult) daemon.Status {
	return daemon.Snapshot(a.connectionMode(), a.opts.autoConnect, probe)
}

func (a *app) stateStore() (state.Store, error) {
	return state.NewStore(a.opts.stateDir)
}

func (a *app) applySelectedConnection(ctx context.Context) error {
	if a.opts.browserURL != "" || a.opts.autoConnect {
		return nil
	}
	store, err := a.stateStore()
	if err != nil {
		return err
	}
	file, err := store.Load(ctx)
	if err != nil {
		return err
	}
	var conn state.Connection
	var ok bool
	if a.opts.connection != "" {
		conn, ok = state.ConnectionByName(file, a.opts.connection)
		if !ok {
			return commandError(
				"unknown_connection",
				"usage",
				fmt.Sprintf("unknown connection %q", a.opts.connection),
				ExitUsage,
				[]string{"cdp connection list --json", "cdp connection add <name> --browser-url <browser-url> --json"},
			)
		}
	} else {
		cwd, cwdErr := os.Getwd()
		if cwdErr == nil {
			conn, ok = state.ProjectConnection(file, cwd)
		}
		if !ok {
			conn, ok = state.CurrentConnection(file)
		}
	}
	if !ok {
		return nil
	}
	a.opts.browserURL = conn.BrowserURL
	a.opts.autoConnect = conn.AutoConnect || conn.Mode == "auto_connect"
	if conn.Channel != "" {
		a.opts.channel = conn.Channel
	}
	return nil
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (a *app) commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return a.commandContextWithDefault(cmd, 0)
}

func (a *app) browserCommandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return a.commandContextWithDefault(cmd, 10*time.Second)
}

func (a *app) commandContextWithDefault(cmd *cobra.Command, fallback time.Duration) (context.Context, context.CancelFunc) {
	ctx := cmd.Context()
	timeout := a.opts.timeout
	if timeout <= 0 {
		timeout = fallback
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
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
