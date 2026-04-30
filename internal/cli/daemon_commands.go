package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
	"os/exec"
	"path/filepath"
)

func (a *app) newDaemonCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the long-running Chrome attach daemon",
	}
	cmd.AddCommand(a.newDaemonStartCommand())
	cmd.AddCommand(a.newDaemonStatusCommand())
	cmd.AddCommand(a.newDaemonStopCommand())
	cmd.AddCommand(a.newDaemonRestartCommand())
	cmd.AddCommand(a.newDaemonKeepaliveCommand())
	cmd.AddCommand(a.newDaemonHoldCommand())
	cmd.AddCommand(a.newDaemonLogsCommand())
	return cmd
}

type daemonStartConfig struct {
	prime          bool
	reconnect      time.Duration
	connectionName string
	remember       bool
}

type daemonStartResult struct {
	human string
	data  map[string]any
}

func (a *app) newDaemonStartCommand() *cobra.Command {
	var prime bool
	var reconnect time.Duration
	var connectionName string
	var remember bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Prepare and probe the browser attach path",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 60*time.Second)
			defer cancel()

			result, err := a.runDaemonStart(ctx, daemonStartConfig{
				prime:          prime,
				reconnect:      reconnect,
				connectionName: connectionName,
				remember:       remember,
			})
			if err != nil {
				return err
			}
			return a.render(ctx, result.human, result.data)
		},
	}
	cmd.Flags().BoolVar(&prime, "prime", false, "compatibility flag; daemon start validates auto-connect by default")
	cmd.Flags().DurationVar(&reconnect, "reconnect", 0, "requested daemon reconnect interval, such as 30s")
	cmd.Flags().StringVar(&connectionName, "connection-name", "default", "connection name to save when --browser-url or --auto-connect is supplied")
	cmd.Flags().BoolVar(&remember, "remember", true, "save supplied connection metadata for future on-demand commands")
	return cmd
}

func (a *app) runDaemonStart(ctx context.Context, cfg daemonStartConfig) (daemonStartResult, error) {
	if a.opts.browserURL != "" && a.opts.autoConnect {
		return daemonStartResult{}, commandError(
			"conflicting_connection_flags",
			"usage",
			"use either --browser-url or --auto-connect, not both",
			ExitUsage,
			[]string{"cdp daemon start --auto-connect --json", "cdp daemon start --browser-url <browser-url> --json"},
		)
	}
	if cfg.reconnect < 0 {
		return daemonStartResult{}, commandError(
			"invalid_reconnect_interval",
			"usage",
			"--reconnect must be a non-negative duration",
			ExitUsage,
			[]string{"cdp daemon start --reconnect 30s --json"},
		)
	}

	var err error
	if err := a.applySelectedConnection(ctx); err != nil {
		return daemonStartResult{}, err
	}
	explicitConnection := a.opts.browserURL != "" || a.opts.autoConnect
	keepAlive := explicitConnection
	if (keepAlive && a.opts.autoConnect) || cfg.prime {
		a.opts.activeProbe = true
	}

	var endpoint string
	var runtime *daemon.Runtime
	var alreadyRunning bool
	var savedConnection *state.Connection
	var statePath string
	if keepAlive && explicitConnection && cfg.remember {
		savedConnection, statePath, err = a.rememberDaemonConnection(ctx, cfg.connectionName)
		if err != nil {
			return daemonStartResult{}, err
		}
	}
	if keepAlive {
		endpoint, err = a.browserEndpoint(ctx)
		if err != nil {
			return daemonStartResult{}, commandError(
				"permission_pending",
				"permission",
				err.Error(),
				ExitPermission,
				[]string{"open chrome://inspect/#remote-debugging", "cdp daemon start --auto-connect --json"},
			)
		}
	}

	var probe browser.ProbeResult
	if keepAlive {
		probe = browser.ProbeResult{
			State:                "cdp_available",
			Message:              "daemon keepalive process holds the approved Chrome DevTools WebSocket",
			ConnectionMode:       a.connectionMode(),
			Channel:              a.opts.channel,
			WebSocketDebuggerURL: true,
		}
	} else {
		probe, err = a.browserProbe(ctx)
		if err != nil {
			return daemonStartResult{}, commandError(
				"invalid_browser_url",
				"usage",
				err.Error(),
				ExitUsage,
				[]string{"cdp daemon start --browser-url <browser-url> --json"},
			)
		}
	}

	if savedConnection == nil && explicitConnection && cfg.remember {
		savedConnection, statePath, err = a.rememberDaemonConnection(ctx, cfg.connectionName)
		if err != nil {
			return daemonStartResult{}, err
		}
	}

	if keepAlive {
		r, reused, err := a.startKeepAlive(ctx, endpoint, cfg.reconnect)
		if err != nil {
			return daemonStartResult{}, commandError(
				"permission_pending",
				"permission",
				fmt.Sprintf("start daemon keepalive: %v", err),
				ExitPermission,
				[]string{"open chrome://inspect/#remote-debugging", "cdp daemon start --auto-connect --json"},
			)
		}
		runtime = &r
		alreadyRunning = reused
	}

	status := a.daemonStatus(ctx, probe)
	if runtime != nil {
		status = daemon.WithRuntime(status, *runtime, true)
	}
	if !keepAlive {
		if err := daemonStartFailure(probe, status); err != nil {
			return daemonStartResult{}, err
		}
	}

	start := map[string]any{
		"state":              status.State,
		"message":            status.Message,
		"connection_mode":    status.ConnectionMode,
		"prime":              cfg.prime,
		"connection_saved":   savedConnection != nil,
		"next_commands":      status.NextCommands,
		"reconnect_interval": durationString(cfg.reconnect),
		"keepalive_started":  runtime != nil && !alreadyRunning,
		"already_running":    alreadyRunning,
	}
	data := map[string]any{
		"ok":      true,
		"daemon":  status,
		"start":   start,
		"browser": probe,
	}
	if savedConnection != nil {
		start["connection_name"] = savedConnection.Name
		start["state_path"] = statePath
		data["connection"] = savedConnection
	}
	if runtime != nil {
		start["runtime"] = runtime
		data["runtime"] = runtime
	}
	human := status.Message
	if savedConnection != nil {
		human = fmt.Sprintf("%s\nconnection %s saved", human, savedConnection.Name)
	}
	return daemonStartResult{human: human, data: data}, nil
}

func (a *app) rememberDaemonConnection(ctx context.Context, name string) (*state.Connection, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", commandError(
			"invalid_connection_name",
			"usage",
			"--connection-name cannot be empty",
			ExitUsage,
			[]string{"cdp daemon start --auto-connect --connection-name default --json"},
		)
	}

	store, err := a.stateStore()
	if err != nil {
		return nil, "", err
	}
	file, err := store.Load(ctx)
	if err != nil {
		return nil, "", err
	}
	conn := state.Connection{
		Name:        name,
		Mode:        a.connectionMode(),
		BrowserURL:  a.opts.browserURL,
		AutoConnect: a.opts.autoConnect,
		UserDataDir: a.opts.userDataDir,
	}
	if a.opts.autoConnect {
		conn.Channel = a.opts.channel
	}
	file = state.UpsertConnection(file, conn)
	file.Selected = conn.Name
	if err := store.Save(ctx, file); err != nil {
		return nil, "", err
	}
	return &conn, store.Path(), nil
}

func daemonStartFailure(probe browser.ProbeResult, status daemon.Status) error {
	remediation := uniqueCommands(probe.RemediationCommands, status.NextCommands, []string{"cdp doctor --json", "cdp daemon status --json"})
	switch probe.State {
	case "cdp_available", "active_probe_skipped":
		return nil
	case "not_configured":
		return commandError(
			"connection_not_configured",
			"connection",
			probe.Message,
			ExitConnection,
			remediation,
		)
	case "permission_pending":
		return commandError(
			"permission_pending",
			"permission",
			probe.Message,
			ExitPermission,
			remediation,
		)
	case "unreachable", "listening_not_cdp", "invalid_response", "missing_browser_websocket":
		return commandError(
			"connection_failed",
			"connection",
			probe.Message,
			ExitConnection,
			remediation,
		)
	default:
		if status.State == "connected" || status.State == "passive" {
			return nil
		}
		return commandError(
			"connection_failed",
			"connection",
			probe.Message,
			ExitConnection,
			remediation,
		)
	}
}

func uniqueCommands(groups ...[]string) []string {
	var commands []string
	seen := map[string]bool{}
	for _, group := range groups {
		for _, command := range group {
			command = strings.TrimSpace(command)
			if command == "" || seen[command] {
				continue
			}
			seen[command] = true
			commands = append(commands, command)
		}
	}
	return commands
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

func (a *app) newDaemonStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show attach daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			probe, err := a.browserProbe(ctx)
			if err != nil {
				return commandError(
					"invalid_browser_url",
					"usage",
					err.Error(),
					ExitUsage,
					[]string{"cdp daemon status --browser-url <browser-url> --json"},
				)
			}
			status := a.daemonStatus(ctx, probe)
			data := map[string]any{
				"ok":     true,
				"daemon": status,
			}
			return a.render(ctx, status.Message, data)
		},
	}
}

func (a *app) startKeepAlive(ctx context.Context, endpoint string, reconnect time.Duration) (daemon.Runtime, bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return daemon.Runtime{}, false, fmt.Errorf("resolve current executable: %w", err)
	}
	store, err := a.stateStore()
	if err != nil {
		return daemon.Runtime{}, false, err
	}
	return daemon.StartKeepAlive(ctx, executable, store.Dir, endpoint, a.connectionMode(), a.opts.userDataDir, reconnect)
}

func (a *app) newDaemonStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the attach daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			runtime, stopped, err := daemon.StopRuntime(ctx, store.Dir)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("stop daemon: %v", err),
					ExitConnection,
					[]string{"cdp daemon status --json"},
				)
			}
			human := "daemon was not running"
			if stopped {
				human = fmt.Sprintf("daemon process %d stopped", runtime.PID)
			}
			return a.render(ctx, human, map[string]any{
				"ok":      true,
				"stopped": stopped,
				"runtime": runtime,
			})
		},
	}
}

func (a *app) newDaemonLogsCommand() *cobra.Command {
	var tail int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show attach daemon logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail < 0 {
				return commandError("usage", "usage", "--tail must be non-negative", ExitUsage, []string{"cdp daemon logs --tail 100 --json"})
			}
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			entries, err := daemon.ReadLogs(ctx, store.Dir, tail)
			if err != nil {
				return commandError("internal", "internal", err.Error(), ExitInternal, []string{"cdp daemon logs --json"})
			}
			lines := make([]string, 0, len(entries))
			for _, entry := range entries {
				line := strings.TrimSpace(strings.Join([]string{entry.Time, entry.Level, entry.Event, entry.Message}, "\t"))
				lines = append(lines, line)
			}
			human := strings.Join(lines, "\n")
			if human == "" {
				human = "daemon log is empty"
			}
			return a.render(ctx, human, map[string]any{
				"ok":      true,
				"log":     map[string]any{"path": daemon.RuntimeLogPath(store.Dir), "tail": tail, "count": len(entries)},
				"entries": entries,
			})
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 100, "maximum log entries to return; use 0 for all")
	return cmd
}

func (a *app) newDaemonRestartCommand() *cobra.Command {
	var prime bool
	var reconnect time.Duration
	var connectionName string
	var remember bool

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the attach daemon and reconnect through the daemon gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 60*time.Second)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			previousRuntime, stopped, err := daemon.StopRuntime(ctx, store.Dir)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("stop daemon before restart: %v", err),
					ExitConnection,
					[]string{"cdp daemon status --json", "cdp daemon stop --json"},
				)
			}

			result, err := a.runDaemonStart(ctx, daemonStartConfig{
				prime:          prime,
				reconnect:      reconnect,
				connectionName: connectionName,
				remember:       remember,
			})
			if err != nil {
				return err
			}
			restart := map[string]any{
				"stopped": stopped,
			}
			if previousRuntime.PID > 0 {
				restart["previous_runtime"] = previousRuntime
			}
			result.data["restart"] = restart
			if stopped {
				result.human = fmt.Sprintf("daemon process %d stopped\n%s", previousRuntime.PID, result.human)
			} else {
				result.human = fmt.Sprintf("daemon was not running\n%s", result.human)
			}
			return a.render(ctx, result.human, result.data)
		},
	}
	cmd.Flags().BoolVar(&prime, "prime", false, "compatibility flag; daemon restart validates auto-connect by default")
	cmd.Flags().DurationVar(&reconnect, "reconnect", 0, "requested daemon reconnect interval, such as 30s")
	cmd.Flags().StringVar(&connectionName, "connection-name", "default", "connection name to save when --browser-url or --auto-connect is supplied")
	cmd.Flags().BoolVar(&remember, "remember", true, "save supplied connection metadata for future on-demand commands")
	return cmd
}

type keepaliveChromeStatus struct {
	Display  string   `json:"display,omitempty"`
	Command  string   `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
	Checked  bool     `json:"checked"`
	Running  bool     `json:"running"`
	Launched bool     `json:"launched"`
	Skipped  bool     `json:"skipped"`
	Reason   string   `json:"reason,omitempty"`
}

func (a *app) newDaemonKeepaliveCommand() *cobra.Command {
	var reconnect time.Duration
	var lockTimeout time.Duration
	var staleLockAfter time.Duration
	var probeMode string
	var display string
	var chromeCommand string
	var chromeArgs []string

	cmd := &cobra.Command{
		Use:   "keepalive",
		Short: "Idempotently keep the daemon healthy for cron",
		RunE: func(cmd *cobra.Command, args []string) error {
			if reconnect < 0 || lockTimeout < 0 || staleLockAfter < 0 {
				return commandError(
					"invalid_duration",
					"usage",
					"--reconnect, --lock-timeout, and --stale-lock-after must be non-negative",
					ExitUsage,
					[]string{"cdp daemon keepalive --auto-connect --json"},
				)
			}
			if probeMode != "auto" && probeMode != "passive" && probeMode != "active" {
				return commandError(
					"invalid_probe_mode",
					"usage",
					"--probe must be passive, active, or auto",
					ExitUsage,
					[]string{"cdp daemon keepalive --probe auto --json"},
				)
			}

			ctx, cancel := a.commandContextWithDefault(cmd, 60*time.Second)
			defer cancel()

			if err := a.applySelectedConnection(ctx); err != nil {
				return err
			}
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			connectionName := a.connectionStateName(ctx)
			mode := a.connectionMode()
			lockName := "daemon-keepalive-" + mode + "-" + connectionName
			lock, acquired, existingLock, err := daemon.AcquireLock(ctx, store.Dir, lockName, lockTimeout, staleLockAfter, daemon.LockMetadata{
				Name:  lockName,
				Phase: "checking",
			})
			if err != nil {
				return commandError(
					"lock_failed",
					"connection",
					fmt.Sprintf("acquire keepalive lock: %v", err),
					ExitConnection,
					[]string{"cdp daemon status --json"},
				)
			}
			if !acquired {
				return a.render(ctx, fmt.Sprintf("keepalive\t%s\tlocked", connectionName), map[string]any{
					"ok":         true,
					"connection": connectionName,
					"mode":       mode,
					"state":      "locked",
					"action":     "skipped",
					"locked":     true,
					"lock":       existingLock,
				})
			}
			defer lock.Release()

			initialActiveProbe := a.opts.activeProbe
			if probeMode == "passive" || probeMode == "auto" {
				a.opts.activeProbe = false
			}
			if probeMode == "active" {
				a.opts.activeProbe = true
			}
			probe, err := a.browserProbe(ctx)
			if err != nil {
				return commandError(
					"invalid_browser_url",
					"usage",
					err.Error(),
					ExitUsage,
					[]string{"cdp daemon keepalive --browser-url <browser-url> --json"},
				)
			}
			status := a.daemonStatus(ctx, probe)
			probeResult := map[string]any{"mode": probeMode, "result": probe.State}
			runtimeHealthy, runtimeCheck := keepaliveRuntimeCheck(ctx, status)
			if status.State == "running" && runtimeHealthy {
				return a.render(ctx, fmt.Sprintf("keepalive\t%s\thealthy", connectionName), map[string]any{
					"ok":         true,
					"connection": connectionName,
					"mode":       mode,
					"state":      "healthy",
					"action":     "none",
					"locked":     false,
					"daemon":     status,
					"probe":      probeResult,
					"health":     runtimeCheck,
					"lock":       map[string]any{"name": lock.Metadata.Name, "acquired": true},
				})
			}
			if status.State == "running" {
				if err := lock.Update(ctx, "repairing_daemon"); err != nil {
					return err
				}
				if _, _, err := daemon.StopRuntime(ctx, store.Dir); err != nil {
					return commandError(
						"connection_failed",
						"connection",
						fmt.Sprintf("stop unhealthy daemon before repair: %v", err),
						ExitConnection,
						[]string{"cdp daemon stop --json", "cdp daemon keepalive --json"},
					)
				}
			}
			if a.opts.autoConnect && probeMode == "passive" {
				return a.render(ctx, fmt.Sprintf("keepalive\t%s\tpassive", connectionName), map[string]any{
					"ok":         true,
					"connection": connectionName,
					"mode":       mode,
					"state":      "passive",
					"action":     "skipped",
					"locked":     false,
					"daemon":     status,
					"probe":      probeResult,
					"lock":       map[string]any{"name": lock.Metadata.Name, "acquired": true},
				})
			}

			chrome := keepaliveChromeStatus{Skipped: true, Reason: "not required for browser_url mode"}
			if a.opts.autoConnect {
				if err := lock.Update(ctx, "launching_chrome"); err != nil {
					return err
				}
				chrome, err = ensureChromeForKeepalive(ctx, display, chromeCommand, chromeArgs)
				if err != nil {
					return commandError(
						"chrome_start_failed",
						"connection",
						fmt.Sprintf("ensure Chrome is running: %v", err),
						ExitConnection,
						[]string{"cdp daemon keepalive --chrome-command <command> --json", "open chrome://inspect/#remote-debugging"},
					)
				}
				if err := lock.Update(ctx, "active_probe"); err != nil {
					return err
				}
				a.opts.activeProbe = true
			} else {
				a.opts.activeProbe = initialActiveProbe
			}

			if err := lock.Update(ctx, "starting_daemon"); err != nil {
				return err
			}
			result, err := a.runDaemonStart(ctx, daemonStartConfig{
				reconnect:      reconnect,
				connectionName: connectionName,
				remember:       true,
			})
			if err != nil {
				return err
			}
			action := "started"
			state := "started"
			if status.Runtime != nil {
				action = "repaired"
				state = "repaired"
			}
			if start, ok := result.data["start"].(map[string]any); ok {
				if already, ok := start["already_running"].(bool); ok && already {
					action = "none"
					state = "healthy"
				}
			}
			if err := lock.Update(ctx, state); err != nil {
				return err
			}
			data := map[string]any{
				"ok":         true,
				"connection": connectionName,
				"mode":       mode,
				"state":      state,
				"action":     action,
				"locked":     false,
				"daemon":     result.data["daemon"],
				"start":      result.data["start"],
				"chrome":     chrome,
				"probe":      probeResult,
				"previous":   status,
				"health":     runtimeCheck,
				"lock":       map[string]any{"name": lock.Metadata.Name, "acquired": true},
			}
			if conn, ok := result.data["connection"]; ok {
				data["connection_detail"] = conn
			}
			return a.render(ctx, fmt.Sprintf("keepalive\t%s\t%s", connectionName, state), data)
		},
	}
	cmd.Flags().DurationVar(&reconnect, "reconnect", 0, "daemon reconnect interval, such as 30s")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 0, "how long to wait for another keepalive lock; 0s skips immediately")
	cmd.Flags().DurationVar(&staleLockAfter, "stale-lock-after", 10*time.Minute, "remove a keepalive lock older than this duration; 0 disables stale cleanup")
	cmd.Flags().StringVar(&probeMode, "probe", "auto", "probe mode: passive, active, or auto")
	cmd.Flags().StringVar(&display, "display", os.Getenv("DISPLAY"), "DISPLAY value to use when launching Chrome for auto-connect")
	cmd.Flags().StringVar(&chromeCommand, "chrome-command", "google-chrome-stable", "Chrome command to launch for auto-connect repair; empty disables launch")
	cmd.Flags().StringArrayVar(&chromeArgs, "chrome-args", nil, "extra Chrome argument; repeat for multiple arguments")
	return cmd
}

func (a *app) connectionStateName(ctx context.Context) string {
	if strings.TrimSpace(a.opts.connection) != "" {
		return strings.TrimSpace(a.opts.connection)
	}
	store, err := a.stateStore()
	if err == nil {
		if file, loadErr := store.Load(ctx); loadErr == nil {
			if conn, ok := state.CurrentConnection(file); ok && strings.TrimSpace(conn.Name) != "" {
				if strings.TrimSpace(a.opts.browserURL) == "" && !a.opts.autoConnect {
					return conn.Name
				}
				if a.opts.autoConnect && (conn.AutoConnect || conn.Mode == "auto_connect") {
					return conn.Name
				}
				if strings.TrimSpace(a.opts.browserURL) != "" && conn.BrowserURL == a.opts.browserURL {
					return conn.Name
				}
			}
		}
	}
	if a.opts.autoConnect {
		return "default"
	}
	if strings.TrimSpace(a.opts.browserURL) != "" {
		return "browser-url"
	}
	return "default"
}

func keepaliveRuntimeCheck(ctx context.Context, status daemon.Status) (bool, map[string]any) {
	check := map[string]any{
		"runtime_state": status.State,
	}
	if status.Runtime == nil {
		check["ok"] = false
		check["result"] = "no_runtime"
		return false, check
	}
	if !status.ProcessRunning {
		check["ok"] = false
		check["result"] = "not_running"
		return false, check
	}
	var result struct {
		TargetInfos []cdp.TargetInfo `json:"targetInfos"`
	}
	if err := (daemon.RuntimeClient{Runtime: *status.Runtime}).Call(ctx, "Target.getTargets", map[string]any{}, &result); err != nil {
		check["ok"] = false
		check["result"] = "target_list_failed"
		check["error"] = err.Error()
		return false, check
	}
	check["ok"] = true
	check["result"] = "target_list_ok"
	check["target_count"] = len(result.TargetInfos)
	return true, check
}

func ensureChromeForKeepalive(ctx context.Context, display, chromeCommand string, chromeArgs []string) (keepaliveChromeStatus, error) {
	status := keepaliveChromeStatus{
		Display: display,
		Command: chromeCommand,
		Args:    chromeArgs,
		Checked: true,
	}
	if strings.TrimSpace(chromeCommand) == "" {
		status.Skipped = true
		status.Reason = "chrome launch disabled"
		return status, nil
	}
	if chromeProcessRunning(ctx, chromeCommand) {
		status.Running = true
		return status, nil
	}
	cmd := exec.CommandContext(ctx, chromeCommand, chromeArgs...)
	if strings.TrimSpace(display) != "" {
		cmd.Env = append(os.Environ(), "DISPLAY="+display)
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return status, fmt.Errorf("open null device: %w", err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	if err := cmd.Start(); err != nil {
		return status, err
	}
	status.Launched = true
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return status, nil
}

func chromeProcessRunning(ctx context.Context, chromeCommand string) bool {
	pgrep, err := exec.LookPath("pgrep")
	if err != nil {
		return false
	}
	name := filepath.Base(chromeCommand)
	if strings.TrimSpace(name) == "" {
		return false
	}
	cmd := exec.CommandContext(ctx, pgrep, "-x", name)
	return cmd.Run() == nil
}

func (a *app) newDaemonHoldCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "hold",
		Short:  "Hold a browser WebSocket open for daemon start",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signalContext(cmd.Context())
			defer stop()
			return daemon.HoldFromEnv(ctx)
		},
	}
}
