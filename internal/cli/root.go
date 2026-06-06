package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
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
	json            bool
	compact         bool
	jq              string
	debug           bool
	timeout         time.Duration
	profile         string
	config          string
	browserURL      string
	autoConnect     bool
	channel         string
	userDataDir     string
	stateDir        string
	browserMode     string
	activeProbe     bool
	connection      string
	allowOverBudget bool
	maxTabs         int
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
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if a.opts.maxTabs < 0 {
				return commandError("invalid_resource_budget", "usage", "--max-tabs must be non-negative", ExitUsage, []string{"cdp --max-tabs 25 pages --json"})
			}
			_, err := a.resolveBrowserMode(cmd)
			return err
		},
		Long: "cdp is a shell-first Chrome DevTools Protocol CLI for coding agents.\n\n" +
			"The project is being built around a long-running local attach daemon, compact\n" +
			"JSON output, jq-friendly filtering, high-level browser debugging workflows, and\n" +
			"cleanup routines such as `cdp page cleanup --json` for cron-safe tab hygiene.",
	}
	a.root = root

	root.PersistentFlags().BoolVar(&a.opts.json, "json", false, "emit JSON on stdout")
	root.PersistentFlags().BoolVar(&a.opts.compact, "compact", false, "emit compact JSON without indentation")
	root.PersistentFlags().StringVar(&a.opts.jq, "jq", "", "filter JSON output with jq expression; implies --json")
	root.PersistentFlags().BoolVar(&a.opts.debug, "debug", false, "write debug details to stderr")
	root.PersistentFlags().DurationVar(&a.opts.timeout, "timeout", 0, "ceiling-bound command execution, such as 30s or 2m")
	root.PersistentFlags().StringVar(&a.opts.profile, "profile", config.DefaultProfile, "named cdp-cli profile to use")
	root.PersistentFlags().StringVar(&a.opts.config, "config", "", "path to config file")
	root.PersistentFlags().StringVar(&a.opts.browserURL, "browser-url", os.Getenv("CDP_BROWSER_URL"), "Chrome DevTools browser URL for daemon lifecycle and connection management; can also be set with CDP_BROWSER_URL")
	root.PersistentFlags().StringVar(&a.opts.browserURL, "browserUrl", os.Getenv("CDP_BROWSER_URL"), "alias for --browser-url")
	root.PersistentFlags().BoolVar(&a.opts.autoConnect, "auto-connect", os.Getenv("CDP_AUTO_CONNECT") == "1" || os.Getenv("CDP_AUTO_CONNECT") == "true", "select Chrome's default-profile remote debugging flow for daemon lifecycle commands")
	root.PersistentFlags().BoolVar(&a.opts.autoConnect, "autoConnect", os.Getenv("CDP_AUTO_CONNECT") == "1" || os.Getenv("CDP_AUTO_CONNECT") == "true", "alias for --auto-connect")
	root.PersistentFlags().StringVar(&a.opts.channel, "channel", envDefault("CDP_CHANNEL", "stable"), "Chrome channel for --auto-connect: stable, beta, canary, or dev")
	root.PersistentFlags().StringVar(&a.opts.userDataDir, "user-data-dir", os.Getenv("CDP_USER_DATA_DIR"), "Chrome user data directory for --auto-connect")
	root.PersistentFlags().StringVar(&a.opts.stateDir, "state-dir", os.Getenv("CDP_STATE_DIR"), "directory for local cdp-cli state; defaults to $HOME/.cdp-cli")
	root.PersistentFlags().StringVar(&a.opts.browserMode, "browser-mode", "", "primary browser runtime selector: headed or headless; can also be set with CDP_BROWSER_MODE")
	root.PersistentFlags().StringVar(&a.opts.browserMode, "browserMode", "", "alias for --browser-mode")
	root.PersistentFlags().BoolVar(&a.opts.activeProbe, "active-browser-probe", os.Getenv("CDP_ACTIVE_BROWSER_PROBE") == "1" || os.Getenv("CDP_ACTIVE_BROWSER_PROBE") == "true", "actively connect to Chrome during daemon status/start checks; may trigger a Chrome remote-debugging prompt")
	root.PersistentFlags().StringVar(&a.opts.connection, "connection", os.Getenv("CDP_CONNECTION"), "advanced named browser endpoint override from local state")
	root.PersistentFlags().BoolVar(&a.opts.allowOverBudget, "allow-over-budget", envBool("CDP_ALLOW_OVER_BUDGET"), "human override: allow creating browser tabs even when the selected profile is over the cdp resource budget")
	root.PersistentFlags().IntVar(&a.opts.maxTabs, "max-tabs", envInt("CDP_MAX_TABS", 0), "maximum page-tab resource budget for the selected browser mode; 0 uses the mode default")

	root.AddCommand(a.newVersionCommand())
	root.AddCommand(a.newDescribeCommand())
	root.AddCommand(a.newDoctorCommand())
	root.AddCommand(a.newExplainErrorCommand())
	root.AddCommand(a.newExitCodesCommand())
	root.AddCommand(a.newSchemaCommand())
	root.AddCommand(a.newDaemonCommand())
	root.AddCommand(a.newCronCommand())
	root.AddCommand(a.newConnectionCommand())
	root.AddCommand(a.newBrowserCommand())
	root.AddCommand(a.newTargetsCommand())
	root.AddCommand(a.newPagesCommand())
	root.AddCommand(a.newPageCommand())
	root.AddCommand(a.newOpenCommand())
	root.AddCommand(a.newEvalCommand())
	root.AddCommand(a.newFramesCommand())
	root.AddCommand(a.newObserveCommand())
	root.AddCommand(a.newTextCommand())
	root.AddCommand(a.newLocatorCommand())
	root.AddCommand(a.newFormCommand())
	root.AddCommand(a.newAssertCommand())
	root.AddCommand(a.newClickCommand())
	root.AddCommand(a.newFillCommand())
	root.AddCommand(a.newCheckCommand())
	root.AddCommand(a.newUncheckCommand())
	root.AddCommand(a.newTypeCommand())
	root.AddCommand(a.newInsertTextCommand())
	root.AddCommand(a.newPressCommand())
	root.AddCommand(a.newHoverCommand())
	root.AddCommand(a.newDragCommand())
	root.AddCommand(a.newScrollCommand())
	root.AddCommand(a.newHTMLCommand())
	root.AddCommand(a.newDOMCommand())
	root.AddCommand(a.newCSSCommand())
	root.AddCommand(a.newLayoutCommand())
	root.AddCommand(a.newWaitCommand())
	root.AddCommand(a.newFocusCommand())
	root.AddCommand(a.newClearCommand())
	root.AddCommand(a.newSelectCommand())
	root.AddCommand(a.newFileCommand())
	root.AddCommand(a.newDialogCommand())
	root.AddCommand(a.newEmulateCommand())
	root.AddCommand(a.newPermissionsCommand())
	root.AddCommand(a.newA11yCommand())
	root.AddCommand(a.newPerfCommand())
	root.AddCommand(a.newMemoryCommand())
	root.AddCommand(a.newSnapshotCommand())
	root.AddCommand(a.newScreenshotCommand())
	root.AddCommand(a.newConsoleCommand())
	root.AddCommand(a.newNetworkCommand())
	root.AddCommand(a.newEventsCommand())
	root.AddCommand(a.newStorageCommand())
	root.AddCommand(a.newCDPCommand())
	root.AddCommand(a.newWorkflowCommand())

	return root
}

func (a *app) resolveBrowserMode(cmd *cobra.Command) (browserModeState, error) {
	cfg, err := config.Load(a.opts.config)
	if err != nil {
		return browserModeState{}, commandError(
			"invalid_config",
			"usage",
			err.Error(),
			ExitUsage,
			[]string{"cdp --config <path> browser mode get --json"},
		)
	}

	flagValue := ""
	if root := cmd.Root(); root != nil {
		flags := root.PersistentFlags()
		if flags.Changed("browser-mode") || flags.Changed("browserMode") {
			flagValue = a.opts.browserMode
		}
	}

	resolution, err := config.ResolveBrowserMode(flagValue, os.Getenv("CDP_BROWSER_MODE"), cfg)
	if err != nil {
		return browserModeState{}, commandError(
			"invalid_browser_mode",
			"usage",
			err.Error(),
			ExitUsage,
			[]string{"cdp --browser-mode headed --json", "cdp --browser-mode headless --json"},
		)
	}

	return browserModeState{
		Mode:         resolution.Mode,
		Source:       resolution.Source,
		ConfigPath:   cfg.Path,
		Warnings:     nil,
		NextCommands: browserModeNextCommands(resolution.Mode),
	}, nil
}

type browserModeState struct {
	Mode         config.BrowserMode       `json:"browser_mode"`
	Source       config.BrowserModeSource `json:"browser_mode_source"`
	ConfigPath   string                   `json:"config_path,omitempty"`
	Warnings     []string                 `json:"warnings,omitempty"`
	NextCommands []string                 `json:"next_commands"`
}

func browserModeNextCommands(mode config.BrowserMode) []string {
	if mode == config.BrowserModeHeadless {
		return []string{
			"cdp daemon keepalive --browser-mode headless --repair --json",
			"cdp daemon status --browser-mode headless --json",
		}
	}
	return []string{
		"cdp daemon status --browser-mode headed --json",
		"cdp daemon start --auto-connect --json",
	}
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

func (a *app) daemonStatus(ctx context.Context, probe browser.ProbeResult) daemon.Status {
	browserMode := a.browserModeName()
	status := daemon.SnapshotForMode(browserMode, a.connectionMode(), a.opts.autoConnect, probe)
	status = a.statusWithModeRuntime(ctx, status, probe)
	status.Health = a.browserHealthSnapshot(ctx, status, false)
	return status
}

func (a *app) statusWithModeRuntime(ctx context.Context, status daemon.Status, probe browser.ProbeResult) daemon.Status {
	store, err := a.stateStore()
	if err != nil {
		return status
	}
	browserMode := status.BrowserMode
	attempts := 1
	if browserMode == string(config.BrowserModeHeadless) && status.State != "running" {
		attempts = 4
	}
	var runtime daemon.Runtime
	var ok bool
	var loadErr error
	for attempt := 0; attempt < attempts; attempt++ {
		runtime, ok, loadErr = daemon.LoadRuntimeForMode(ctx, store.Dir, browserMode)
		if loadErr == nil && ok && daemon.RuntimeRunning(runtime) && daemon.RuntimeSocketReady(ctx, runtime) {
			break
		}
		if attempt+1 < attempts {
			timer := time.NewTimer(75 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return status
			case <-timer.C:
			}
		}
	}
	if loadErr != nil || !ok {
		return status
	}
	if !a.runtimeMatchesConnection(runtime) {
		return status
	}
	processRunning := daemon.RuntimeRunning(runtime)
	socketReady := processRunning && daemon.RuntimeSocketReady(ctx, runtime)
	if a.runtimeOverridesSelectedConnection(runtime) {
		message := "mode-specific managed headless daemon runtime is ready"
		if !socketReady {
			message = "mode-specific managed headless daemon runtime socket is not ready"
		}
		probe = browser.ProbeResult{
			State:                "cdp_available",
			Message:              message,
			ConnectionMode:       runtime.ConnectionMode,
			WebSocketDebuggerURL: true,
		}
		status = daemon.SnapshotForMode(browserMode, runtime.ConnectionMode, runtime.ConnectionMode == "auto_connect", probe)
	} else if !a.hasExplicitConnectionOptions() && runtime.ConnectionMode != status.ConnectionMode {
		status = daemon.SnapshotForMode(browserMode, runtime.ConnectionMode, runtime.ConnectionMode == "auto_connect", probe)
	}
	status = daemon.WithRuntimeReadiness(status, runtime, processRunning, socketReady)
	return status
}

func (a *app) browserModeName() string {
	mode, err := a.resolveBrowserMode(a.root)
	if err != nil {
		return string(config.BrowserModeHeaded)
	}
	return string(mode.Mode)
}

func (a *app) runtimeMatchesConnection(runtime daemon.Runtime) bool {
	runtimeMode := strings.TrimSpace(runtime.BrowserMode)
	if runtimeMode == "" {
		runtimeMode = string(config.BrowserModeHeaded)
	}
	if runtimeMode != a.browserModeName() {
		return false
	}
	if a.runtimeOverridesSelectedConnection(runtime) {
		return true
	}
	if a.opts.userDataDir != "" && runtime.UserDataDir != a.opts.userDataDir {
		return false
	}
	if runtime.ConnectionMode != a.connectionMode() {
		if !a.hasExplicitConnectionOptions() {
			return true
		}
		return false
	}
	return true
}

func (a *app) runtimeOverridesSelectedConnection(runtime daemon.Runtime) bool {
	runtimeMode := strings.TrimSpace(runtime.BrowserMode)
	if runtimeMode == "" {
		runtimeMode = string(config.BrowserModeHeaded)
	}
	if a.browserModeName() != string(config.BrowserModeHeadless) || runtimeMode != string(config.BrowserModeHeadless) || runtime.ConnectionMode != "browser_url" {
		return false
	}
	return runtime.ManagedBrowser != nil || strings.TrimSpace(runtime.ManagedProfilePath) != "" || strings.TrimSpace(runtime.ProfileSeedStrategy) == "managed" || strings.TrimSpace(runtime.ChromePort) != ""
}

func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func (a *app) stateStore() (state.Store, error) {
	return state.NewStore(a.opts.stateDir)
}

func (a *app) applySelectedConnection(ctx context.Context) error {
	if a.opts.browserURL != "" || a.opts.autoConnect {
		return nil
	}
	conn, source, ok, err := a.resolveConnection(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if source == "browser_mode_default" {
		return nil
	}
	a.applyConnection(conn)
	return nil
}

func (a *app) hasExplicitConnectionOptions() bool {
	return a.opts.browserURL != "" || a.opts.autoConnect
}

func (a *app) applyConnection(conn state.Connection) {
	a.opts.browserURL = conn.BrowserURL
	a.opts.autoConnect = conn.AutoConnect || conn.Mode == "auto_connect"
	if conn.Channel != "" {
		a.opts.channel = conn.Channel
	}
	if conn.UserDataDir != "" {
		a.opts.userDataDir = conn.UserDataDir
	}
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return value == "1" || value == "true" || value == "yes"
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
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
		JSON:    a.opts.json,
		JQ:      a.opts.jq,
		Compact: a.opts.compact,
	}, human, data)
}

func liftErrorEnvelopeData(env *output.Envelope, data any) {
	fields, ok := data.(map[string]any)
	if !ok {
		return
	}
	if value, ok := fields["human_required"].(bool); ok {
		env.HumanRequired = value
	}
	if value, ok := fields["agent_should_stop"].(bool); ok {
		env.AgentShouldStop = value
	}
	if value, ok := fields["human_action"].(string); ok {
		env.HumanAction = value
	}
	if value, ok := stringSliceField(fields["safe_diagnostics"]); ok {
		env.SafeDiagnostics = value
	}
	if value, ok := fields["resource_budget"]; ok {
		env.ResourceBudget = value
	}
}

func stringSliceField(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, false
	}
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
		Data:                cmdErr.Data,
		RemediationCommands: cmdErr.RemediationCommands,
	}
	liftErrorEnvelopeData(&env, cmdErr.Data)
	if a.opts.autoConnect && cmdErr.Code == "permission_pending" {
		env.RemediationCommands = permissionRemediationCommands()
		env.HumanRequired = true
		env.AgentShouldStop = true
		env.HumanAction = autoConnectHumanAction
		env.SafeDiagnostics = safeDiagnosticCommands()
	}

	if a.opts.json || a.opts.jq != "" {
		return output.Render(ctx, a.out, output.Options{JSON: true, JQ: a.opts.jq, Compact: a.opts.compact}, "", env)
	}

	_, writeErr := fmt.Fprintln(a.err, env.Message)
	return writeErr
}
