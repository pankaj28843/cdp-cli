package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
)

type commandInfo struct {
	Name     string        `json:"name"`
	Use      string        `json:"use"`
	Short    string        `json:"short,omitempty"`
	Aliases  []string      `json:"aliases,omitempty"`
	Examples []string      `json:"examples,omitempty"`
	Children []commandInfo `json:"children,omitempty"`
}

func (a *app) newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			human := fmt.Sprintf("cdp %s", a.build.Version)
			return a.render(ctx, human, a.build)
		},
	}
}

func (a *app) newDescribeCommand() *cobra.Command {
	var commandPath string
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Describe the command tree as JSON for agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			target := a.root
			if commandPath != "" {
				var err error
				target, err = findCommand(a.root, commandPath)
				if err != nil {
					return err
				}
			}

			data := map[string]any{
				"ok":       true,
				"commands": describeCommand(target),
				"globals": []string{
					"--json",
					"--compact",
					"--jq",
					"--debug",
					"--timeout",
					"--profile",
					"--config",
					"--browser-url",
					"--browserUrl",
					"--auto-connect",
					"--autoConnect",
					"--channel",
					"--user-data-dir",
					"--state-dir",
					"--active-browser-probe",
					"--connection",
				},
			}
			return a.render(ctx, "Use --json to print the command tree.", data)
		},
	}
	cmd.Flags().StringVar(&commandPath, "command", "", "describe one command path, such as 'daemon status'")
	return cmd
}

func (a *app) newDoctorCommand() *cobra.Command {
	var checkName string
	var capabilities bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run local readiness checks",
		Long:  "Run readiness checks for the CLI, selected browser connection, and daemon path.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if capabilities {
				ctx, cancel := a.commandContext(cmd)
				defer cancel()
				rows := capabilityCatalog()
				lines := make([]string, 0, len(rows))
				for _, row := range rows {
					lines = append(lines, fmt.Sprintf("%s\t%s", row["name"], row["status"]))
				}
				return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
					"ok":           true,
					"capabilities": rows,
				})
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			probe, err := a.browserProbe(ctx)
			if err != nil {
				return commandError(
					"invalid_browser_url",
					"usage",
					err.Error(),
					ExitUsage,
					[]string{"cdp doctor --browser-url <browser-url> --json"},
				)
			}
			browserStatus := browserDoctorStatus(a.opts.autoConnect, &probe)
			daemonStatus := a.daemonStatus(ctx, probe)
			daemonCheckStatus := daemonDoctorStatus(daemonStatus.State)
			browserMessage := probe.Message
			browserRemediation := probe.RemediationCommands
			if a.opts.autoConnect && daemonStatus.State == "running" {
				browserStatus = "pass"
				browserMessage = "daemon keepalive process is running; active browser probing was skipped"
				browserRemediation = daemonStatus.NextCommands
			}
			checks := []map[string]any{
				{"name": "cli", "status": "pass", "message": "command scaffold is installed"},
				{
					"name":            "daemon",
					"status":          daemonCheckStatus,
					"state":           daemonStatus.State,
					"message":         daemonStatus.Message,
					"connection_mode": daemonStatus.ConnectionMode,
					"details":         daemonStatus,
				},
			}
			checks = append(checks, map[string]any{
				"name":                 "browser_debug_endpoint",
				"status":               browserStatus,
				"message":              browserMessage,
				"connection_mode":      a.connectionMode(),
				"requires_user_allow":  a.opts.autoConnect,
				"default_profile_flow": a.opts.autoConnect,
				"details":              probe,
				"remediation_commands": browserRemediation,
			})
			if checkName != "" {
				checks = filterChecksByName(checks, checkName)
				if len(checks) == 0 {
					return commandError(
						"unknown_check",
						"usage",
						fmt.Sprintf("unknown doctor check %q", checkName),
						ExitUsage,
						[]string{"cdp doctor --json", "cdp doctor --check daemon --json"},
					)
				}
			}

			data := map[string]any{
				"ok":     browserStatus != "fail" && daemonCheckStatus != "fail",
				"checks": checks,
			}
			human := fmt.Sprintf("cli: pass\ndaemon: %s\nbrowser: %s", daemonStatus.State, browserStatus)
			return a.render(ctx, human, data)
		},
	}
	cmd.Flags().StringVar(&checkName, "check", "", "only return one check by name")
	cmd.Flags().BoolVar(&capabilities, "capabilities", false, "report implemented and planned capability areas without probing Chrome")
	return cmd
}

func capabilityCatalog() []map[string]string {
	return []map[string]string{
		{"name": "connection", "status": "implemented", "commands": "connection, daemon, doctor"},
		{"name": "target_discovery", "status": "implemented", "commands": "targets, pages"},
		{"name": "page_control", "status": "implemented", "commands": "page reload/back/forward/activate/close, open"},
		{"name": "page_inspection", "status": "implemented", "commands": "eval, text, html, snapshot, dom query, css inspect, layout overflow"},
		{"name": "artifacts", "status": "implemented", "commands": "screenshot"},
		{"name": "console", "status": "implemented", "commands": "console, workflow console-errors"},
		{"name": "network", "status": "implemented", "commands": "network, workflow network-failures"},
		{"name": "storage", "status": "implemented", "commands": "storage list/get/set/delete/clear/snapshot/diff, storage cookies"},
		{"name": "raw_protocol", "status": "implemented", "commands": "protocol metadata/domains/search/describe/exec"},
		{"name": "input_automation", "status": "planned", "commands": "click, fill, type, press, hover, drag, upload"},
		{"name": "emulation", "status": "planned", "commands": "viewport, media, user-agent, geolocation, network, cpu"},
		{"name": "performance", "status": "planned", "commands": "trace, Lighthouse, performance insights"},
		{"name": "memory", "status": "planned", "commands": "heap snapshot"},
		{"name": "advanced_storage", "status": "planned", "commands": "IndexedDB, Cache Storage, service workers"},
	}
}

func filterChecksByName(checks []map[string]any, name string) []map[string]any {
	filtered := make([]map[string]any, 0, len(checks))
	for _, check := range checks {
		if check["name"] == name {
			filtered = append(filtered, check)
		}
	}
	return filtered
}

func browserDoctorStatus(autoConnect bool, probe *browser.ProbeResult) string {
	switch probe.State {
	case "cdp_available":
		return "pass"
	case "not_configured", "permission_pending", "active_probe_skipped":
		return "pending"
	case "listening_not_cdp", "missing_browser_websocket", "invalid_response":
		if autoConnect && probe.State == "listening_not_cdp" {
			probe.Message = "auto-connect endpoint is listening, but a CDP session is not established yet"
			return "pending"
		}
		return "warn"
	case "stale_state":
		return "warn"
	default:
		return "fail"
	}
}

func daemonDoctorStatus(state string) string {
	switch state {
	case "connected", "running":
		return "pass"
	case "not_running", "permission_pending", "passive":
		return "pending"
	case "chrome_unavailable", "disconnected", "stale_state":
		return "warn"
	default:
		return "pending"
	}
}

func (a *app) newExplainErrorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "explain-error [code]",
		Short: "Explain stable cdp error codes and recovery commands",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			if len(args) == 1 {
				info, ok := findErrorInfo(args[0])
				if !ok {
					return commandError(
						"unknown_error_code",
						"usage",
						fmt.Sprintf("unknown error code %q", args[0]),
						ExitUsage,
						[]string{"cdp explain-error --json", "cdp explain-error not_implemented --json"},
					)
				}
				human := fmt.Sprintf("%s: %s\n%s", info.Code, info.Message, info.Meaning)
				return a.render(ctx, human, map[string]any{"ok": true, "error": info})
			}

			catalog := errorCatalog()
			var lines []string
			for _, info := range catalog {
				lines = append(lines, fmt.Sprintf("%s (%d): %s", info.Code, info.ExitCode, info.Message))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{"ok": true, "errors": catalog})
		},
	}
}

func (a *app) newExitCodesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "exit-codes",
		Short: "Print stable process exit codes",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			catalog := errorCatalog()
			var lines []string
			lines = append(lines, fmt.Sprintf("%d: ok", ExitOK))
			for _, info := range catalog {
				lines = append(lines, fmt.Sprintf("%d: %s", info.ExitCode, info.Code))
			}

			data := map[string]any{
				"ok": true,
				"exit_codes": append([]map[string]any{{
					"code":    ExitOK,
					"name":    "ok",
					"meaning": "the command completed successfully",
				}}, exitCodeRows(catalog)...),
			}
			return a.render(ctx, strings.Join(lines, "\n"), data)
		},
	}
}

func exitCodeRows(catalog []errorInfo) []map[string]any {
	rows := make([]map[string]any, 0, len(catalog))
	for _, info := range catalog {
		rows = append(rows, map[string]any{
			"code":      info.ExitCode,
			"name":      info.Code,
			"err_class": info.Class,
			"meaning":   info.Meaning,
		})
	}
	return rows
}

func (a *app) newSchemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "schema [name]",
		Short: "Print stable JSON output schemas",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			catalog := schemaCatalog()
			if len(args) == 1 {
				schema, ok := catalog[args[0]]
				if !ok {
					return commandError(
						"unknown_schema",
						"usage",
						fmt.Sprintf("unknown schema %q", args[0]),
						ExitUsage,
						[]string{"cdp schema --json", "cdp describe --json"},
					)
				}
				return a.render(ctx, fmt.Sprintf("%s: %s", schema.Name, schema.Description), map[string]any{"ok": true, "schema": schema})
			}

			names := schemaNames()
			return a.render(ctx, strings.Join(names, "\n"), map[string]any{"ok": true, "schemas": names})
		},
	}
}

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

func (a *app) newConnectionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connection",
		Short: "Manage disk-backed browser connection memory",
	}
	cmd.AddCommand(a.newConnectionAddCommand())
	cmd.AddCommand(a.newConnectionListCommand())
	cmd.AddCommand(a.newConnectionSelectCommand())
	cmd.AddCommand(a.newConnectionRemoveCommand())
	cmd.AddCommand(a.newConnectionPruneCommand())
	cmd.AddCommand(a.newConnectionCurrentCommand())
	cmd.AddCommand(a.newConnectionResolveCommand())
	return cmd
}

func (a *app) newConnectionAddCommand() *cobra.Command {
	var browserURL string
	var autoConnect bool
	var channel string
	var project string

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update a named browser connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			mode := "browser_url"
			if autoConnect {
				mode = "auto_connect"
			}
			if mode == "browser_url" && strings.TrimSpace(browserURL) == "" {
				return commandError(
					"missing_browser_url",
					"usage",
					"connection add requires --browser-url unless --auto-connect is set",
					ExitUsage,
					[]string{"cdp connection add local --browser-url <browser-url> --json"},
				)
			}
			projectPath, err := normalizeProjectPath(project)
			if err != nil {
				return commandError(
					"invalid_project",
					"usage",
					err.Error(),
					ExitUsage,
					[]string{"cdp connection add local --browser-url <browser-url> --project <path> --json"},
				)
			}

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			conn := state.Connection{
				Name:        args[0],
				Mode:        mode,
				BrowserURL:  browserURL,
				AutoConnect: autoConnect,
				UserDataDir: a.opts.userDataDir,
				Project:     project,
			}
			conn.Project = projectPath
			if autoConnect {
				conn.Channel = channel
			}
			file = state.UpsertConnection(file, conn)
			if file.Selected == "" {
				file.Selected = conn.Name
			}
			if err := store.Save(ctx, file); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("connection %s saved", conn.Name), map[string]any{
				"ok":         true,
				"connection": conn,
				"selected":   file.Selected,
				"state_path": store.Path(),
			})
		},
	}
	cmd.Flags().StringVar(&browserURL, "browser-url", "", "Chrome DevTools browser URL")
	cmd.Flags().StringVar(&browserURL, "browserUrl", "", "alias for --browser-url")
	cmd.Flags().BoolVar(&autoConnect, "auto-connect", false, "use Chrome's default-profile auto-connect flow")
	cmd.Flags().BoolVar(&autoConnect, "autoConnect", false, "alias for --auto-connect")
	cmd.Flags().StringVar(&channel, "channel", "stable", "Chrome channel for auto-connect")
	cmd.Flags().StringVar(&project, "project", "", "optional project selector")
	return cmd
}

func normalizeProjectPath(project string) (string, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return "", nil
	}
	abs, err := filepath.Abs(project)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	return filepath.Clean(abs), nil
}

func (a *app) newConnectionListCommand() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List saved browser connections",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			projectPath, err := normalizeProjectPath(project)
			if err != nil {
				return commandError(
					"invalid_project",
					"usage",
					err.Error(),
					ExitUsage,
					[]string{"cdp connection list --project <path> --json"},
				)
			}
			connections := filterConnectionsByProject(file.Connections, projectPath)
			var lines []string
			for _, conn := range connections {
				marker := " "
				if conn.Name == file.Selected {
					marker = "*"
				}
				lines = append(lines, fmt.Sprintf("%s %s %s", marker, conn.Name, conn.Mode))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":          true,
				"connections": connections,
				"selected":    file.Selected,
				"state_path":  store.Path(),
			})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "only list connections scoped to this project path")
	return cmd
}

func filterConnectionsByProject(connections []state.Connection, project string) []state.Connection {
	if project == "" {
		return connections
	}
	filtered := make([]state.Connection, 0, len(connections))
	for _, conn := range connections {
		if conn.Project == project {
			filtered = append(filtered, conn)
		}
	}
	return filtered
}

func (a *app) newConnectionSelectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "select <name>",
		Short: "Select the default browser connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			file, ok := state.SelectConnection(file, args[0])
			if !ok {
				return commandError(
					"unknown_connection",
					"usage",
					fmt.Sprintf("unknown connection %q", args[0]),
					ExitUsage,
					[]string{"cdp connection list --json", "cdp connection add <name> --browser-url <browser-url> --json"},
				)
			}
			if err := store.Save(ctx, file); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("connection %s selected", args[0]), map[string]any{
				"ok":         true,
				"selected":   file.Selected,
				"state_path": store.Path(),
			})
		},
	}
}

func (a *app) newConnectionRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a saved browser connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			file, ok := state.RemoveConnection(file, args[0])
			if !ok {
				return commandError(
					"unknown_connection",
					"usage",
					fmt.Sprintf("unknown connection %q", args[0]),
					ExitUsage,
					[]string{"cdp connection list --json"},
				)
			}
			if err := store.Save(ctx, file); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("connection %s removed", args[0]), map[string]any{
				"ok":          true,
				"removed":     args[0],
				"selected":    file.Selected,
				"connections": file.Connections,
				"state_path":  store.Path(),
			})
		},
	}
}

func (a *app) newConnectionPruneCommand() *cobra.Command {
	var missingProjects bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune stale saved browser connections",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			if !missingProjects {
				return commandError(
					"missing_prune_criteria",
					"usage",
					"connection prune requires --missing-projects",
					ExitUsage,
					[]string{"cdp connection prune --missing-projects --json"},
				)
			}
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			prunedFile, removed := state.PruneMissingProjects(file, pathExists)
			if !dryRun {
				if err := store.Save(ctx, prunedFile); err != nil {
					return err
				}
			}
			return a.render(ctx, fmt.Sprintf("pruned %d connections", len(removed)), map[string]any{
				"ok":          true,
				"dry_run":     dryRun,
				"removed":     removed,
				"connections": prunedFile.Connections,
				"selected":    prunedFile.Selected,
				"state_path":  store.Path(),
			})
		},
	}
	cmd.Flags().BoolVar(&missingProjects, "missing-projects", false, "remove connections whose project path no longer exists")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report stale connections without writing state")
	return cmd
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (a *app) newConnectionCurrentCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the selected browser connection",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			conn, ok := state.CurrentConnection(file)
			if !ok {
				return commandError(
					"connection_not_configured",
					"connection",
					"no browser connection is selected",
					ExitConnection,
					[]string{"cdp connection add default --auto-connect --json", "cdp connection list --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("%s %s", conn.Name, conn.Mode), map[string]any{
				"ok":         true,
				"connection": conn,
				"state_path": store.Path(),
			})
		},
	}
}

func (a *app) newConnectionResolveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve",
		Short: "Show the effective browser connection for this command",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			conn, source, ok, err := a.resolveConnection(ctx)
			if err != nil {
				return err
			}
			if !ok {
				return commandError(
					"connection_not_configured",
					"connection",
					"no browser connection is configured",
					ExitConnection,
					[]string{"cdp connection add default --auto-connect --json", "cdp connection list --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("%s %s", source, conn.Name), map[string]any{
				"ok":         true,
				"source":     source,
				"connection": conn,
				"state_path": store.Path(),
			})
		},
	}
}

func (a *app) resolveConnection(ctx context.Context) (state.Connection, string, bool, error) {
	if a.opts.browserURL != "" || a.opts.autoConnect {
		conn := state.Connection{
			Name:        "flags",
			Mode:        a.connectionMode(),
			BrowserURL:  a.opts.browserURL,
			AutoConnect: a.opts.autoConnect,
			UserDataDir: a.opts.userDataDir,
		}
		if a.opts.autoConnect {
			conn.Channel = a.opts.channel
		}
		return conn, "flags", true, nil
	}
	store, err := a.stateStore()
	if err != nil {
		return state.Connection{}, "", false, err
	}
	file, err := store.Load(ctx)
	if err != nil {
		return state.Connection{}, "", false, err
	}
	if a.opts.connection != "" {
		conn, ok := state.ConnectionByName(file, a.opts.connection)
		if !ok {
			return state.Connection{}, "", false, commandError(
				"unknown_connection",
				"usage",
				fmt.Sprintf("unknown connection %q", a.opts.connection),
				ExitUsage,
				[]string{"cdp connection list --json", "cdp connection add <name> --browser-url <browser-url> --json"},
			)
		}
		return conn, "named", true, nil
	}
	cwd, cwdErr := filepath.Abs(".")
	if cwdErr == nil {
		if conn, ok := state.ProjectConnection(file, cwd); ok {
			return conn, "project", true, nil
		}
	}
	if file.Selected != "" {
		conn, ok := state.ConnectionByName(file, file.Selected)
		return conn, "selected", ok, nil
	}
	conn, ok := state.CurrentConnection(file)
	if ok {
		return conn, "single", true, nil
	}
	return state.Connection{}, "", false, nil
}

func (a *app) newTargetsCommand() *cobra.Command {
	var limit int
	var targetType string
	cmd := &cobra.Command{
		Use:   "targets",
		Short: "List browser targets",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			targets, err := a.listTargets(ctx)
			if err != nil {
				return err
			}
			targets = filterTargetsByType(targets, targetType)
			rows := targetRows(targets)
			rows = limitRows(rows, limit)
			var lines []string
			for _, target := range rows {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s", target["id"], target["type"], target["title"]))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{"ok": true, "targets": rows})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of targets to return; use 0 for no limit")
	cmd.Flags().StringVar(&targetType, "type", "", "only return targets of this CDP type, such as page or service_worker")
	return cmd
}

func (a *app) newPagesCommand() *cobra.Command {
	var limit int
	var urlContains string
	var includeURL string
	var excludeURL string
	cmd := &cobra.Command{
		Use:   "pages",
		Short: "List open pages and tabs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			targets, err := a.listTargets(ctx)
			if err != nil {
				return err
			}
			pages := pageRows(targets)
			pages = filterRowsContains(pages, "url", firstNonEmpty(urlContains, includeURL))
			pages = filterRowsExcludes(pages, "url", excludeURL)
			pages = limitRows(pages, limit)
			var lines []string
			for _, page := range pages {
				lines = append(lines, fmt.Sprintf("%s\t%s", page["id"], page["title"]))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{"ok": true, "pages": pages})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of pages to return; use 0 for no limit")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "only return pages whose URL contains this text")
	cmd.Flags().StringVar(&includeURL, "include-url", "", "only return pages whose URL contains this text")
	cmd.Flags().StringVar(&excludeURL, "exclude-url", "", "exclude pages whose URL contains this text")
	return cmd
}

func (a *app) listTargets(ctx context.Context) ([]cdp.TargetInfo, error) {
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return nil, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
		)
	}
	defer closeClient(ctx)

	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return nil, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("list targets: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp daemon status --json"},
		)
	}
	return targets, nil
}

func targetRows(targets []cdp.TargetInfo) []map[string]any {
	rows := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		rows = append(rows, map[string]any{
			"id":       target.TargetID,
			"type":     target.Type,
			"title":    target.Title,
			"url":      target.URL,
			"attached": target.Attached,
		})
	}
	return rows
}

func filterTargetsByType(targets []cdp.TargetInfo, targetType string) []cdp.TargetInfo {
	targetType = strings.TrimSpace(targetType)
	if targetType == "" {
		return targets
	}
	filtered := make([]cdp.TargetInfo, 0, len(targets))
	for _, target := range targets {
		if target.Type == targetType {
			filtered = append(filtered, target)
		}
	}
	return filtered
}

func limitRows(rows []map[string]any, limit int) []map[string]any {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}

func filterRowsContains(rows []map[string]any, key, needle string) []map[string]any {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return rows
	}
	filtered := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		value, _ := row[key].(string)
		if strings.Contains(value, needle) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterRowsExcludes(rows []map[string]any, key, needle string) []map[string]any {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return rows
	}
	filtered := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		value, _ := row[key].(string)
		if !strings.Contains(value, needle) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func pageRows(targets []cdp.TargetInfo) []map[string]any {
	pages := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		if target.Type != "page" {
			continue
		}
		pages = append(pages, pageRow(target))
	}
	return pages
}

func pageRow(target cdp.TargetInfo) map[string]any {
	return map[string]any{
		"id":       target.TargetID,
		"type":     target.Type,
		"title":    target.Title,
		"url":      target.URL,
		"attached": target.Attached,
	}
}

func (a *app) newPageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "page",
		Short: "Control an open page target",
	}
	cmd.AddCommand(a.newPageSelectCommand())
	cmd.AddCommand(a.newPageReloadCommand())
	cmd.AddCommand(a.newPageHistoryCommand("back", "Navigate the selected page back in history", -1))
	cmd.AddCommand(a.newPageHistoryCommand("forward", "Navigate the selected page forward in history", 1))
	cmd.AddCommand(a.newPageActivateCommand())
	cmd.AddCommand(a.newPageCloseCommand())
	return cmd
}

func (a *app) newPageSelectCommand() *cobra.Command {
	var urlContains string
	cmd := &cobra.Command{
		Use:   "select [target-id]",
		Short: "Select the default page target for subsequent commands",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetID := ""
			if len(args) == 1 {
				targetID = args[0]
			}
			if strings.TrimSpace(targetID) == "" && strings.TrimSpace(urlContains) == "" {
				return commandError(
					"missing_page_selector",
					"usage",
					"page select requires a target id/prefix or --url-contains",
					ExitUsage,
					[]string{"cdp page select <target-id> --json", "cdp page select --url-contains localhost --json"},
				)
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			defer closeClient(ctx)

			target, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains)
			if err != nil {
				return err
			}
			selection := state.PageSelection{
				Connection: a.connectionStateName(ctx),
				TargetID:   target.TargetID,
				URL:        target.URL,
				Title:      target.Title,
				SelectedAt: time.Now().UTC().Format(time.RFC3339),
			}
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			file = state.UpsertPageSelection(file, selection)
			if err := store.Save(ctx, file); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("selected\t%s", target.TargetID), map[string]any{
				"ok":            true,
				"selected_page": selection,
				"target":        pageRow(target),
				"state_path":    store.Path(),
			})
		},
	}
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "select the first page whose URL contains this text")
	return cmd
}

func (a *app) newPageReloadCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var ignoreCache bool
	cmd := &cobra.Command{
		Use:   "reload",
		Short: "Reload a page target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			if err := session.Reload(ctx, ignoreCache); err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("reload target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("reloaded\t%s", target.TargetID), map[string]any{
				"ok":           true,
				"action":       "reloaded",
				"target":       pageRow(target),
				"ignore_cache": ignoreCache,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().BoolVar(&ignoreCache, "ignore-cache", false, "reload while bypassing cache")
	return cmd
}

func (a *app) newPageHistoryCommand(name, short string, offset int) *cobra.Command {
	var targetID string
	var urlContains string
	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			history, err := session.NavigationHistory(ctx)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("read navigation history for target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			targetIndex := history.CurrentIndex + offset
			if targetIndex < 0 || targetIndex >= len(history.Entries) {
				return commandError(
					"navigation_unavailable",
					"usage",
					fmt.Sprintf("page has no %s history entry", name),
					ExitUsage,
					[]string{"cdp page reload --json", "cdp open <url> --new-tab=false --target <target-id> --json"},
				)
			}
			entry := history.Entries[targetIndex]
			if err := session.NavigateToHistoryEntry(ctx, entry.ID); err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("navigate %s for target %s: %v", name, target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("%s\t%s\t%d", name, target.TargetID, entry.ID), map[string]any{
				"ok":     true,
				"action": name,
				"target": pageRow(target),
				"history": map[string]any{
					"current_index": history.CurrentIndex,
					"target_index":  targetIndex,
					"entry_id":      entry.ID,
					"entry":         entry,
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	return cmd
}

func (a *app) newPageActivateCommand() *cobra.Command {
	return a.newPageTargetCommand("activate", "Bring a page target to the foreground", "activated", cdp.ActivateTargetWithClient)
}

func (a *app) newPageCloseCommand() *cobra.Command {
	return a.newPageTargetCommand("close", "Close a page target", "closed", cdp.CloseTargetWithClient)
}

func (a *app) newPageTargetCommand(use, short, action string, run func(context.Context, cdp.CommandClient, string) error) *cobra.Command {
	var targetID string
	var urlContains string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			defer closeClient(ctx)

			target, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains)
			if err != nil {
				return err
			}
			if err := run(ctx, client, target.TargetID); err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("%s target %s: %v", use, target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("%s\t%s", action, target.TargetID), map[string]any{
				"ok":     true,
				"action": action,
				"target": pageRow(target),
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	return cmd
}

type browserEventClient interface {
	cdp.CommandClient
	DrainEvents(context.Context) ([]cdp.Event, error)
	ReadEvent(context.Context) (cdp.Event, error)
}

func (a *app) browserCDPClient(ctx context.Context) (cdp.CommandClient, func(context.Context) error, error) {
	runtime, err := a.requiredDaemonRuntime(ctx)
	if err != nil {
		return nil, nil, err
	}
	return daemon.RuntimeClient{Runtime: runtime}, func(context.Context) error { return nil }, nil
}

func (a *app) browserEventCDPClient(ctx context.Context) (browserEventClient, func(context.Context) error, error) {
	runtime, err := a.requiredDaemonRuntime(ctx)
	if err != nil {
		return nil, nil, err
	}
	return daemon.RuntimeClient{Runtime: runtime}, func(context.Context) error { return nil }, nil
}

func (a *app) requiredDaemonRuntime(ctx context.Context) (daemon.Runtime, error) {
	if _, err := a.browserOptions(ctx); err != nil {
		return daemon.Runtime{}, err
	}
	store, err := a.stateStore()
	if err != nil {
		return daemon.Runtime{}, err
	}
	runtime, ok, err := daemon.LoadRuntime(ctx, store.Dir)
	if err != nil {
		return daemon.Runtime{}, err
	}
	if !ok {
		return daemon.Runtime{}, fmt.Errorf("browser commands require a running cdp daemon; run `cdp daemon start --auto-connect --json` or `cdp daemon start --browser-url <browser-url> --json`")
	}
	if !a.runtimeMatchesConnection(runtime) {
		return daemon.Runtime{}, fmt.Errorf("running daemon does not match the selected browser connection; run `cdp daemon status --json` or restart it with `cdp daemon stop --json` then `cdp daemon start --json`")
	}
	if !daemon.RuntimeRunning(runtime) {
		return daemon.Runtime{}, fmt.Errorf("daemon runtime state exists but the process is not running; run `cdp daemon start --json`")
	}
	if !daemon.RuntimeSocketReady(ctx, runtime) {
		return daemon.Runtime{}, fmt.Errorf("daemon runtime socket is not ready; run `cdp daemon status --json` or restart it with `cdp daemon stop --json` then `cdp daemon start --json`")
	}
	return runtime, nil
}

func (a *app) attachPageSession(ctx context.Context, targetID, urlContains string) (*cdp.PageSession, cdp.TargetInfo, error) {
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return nil, cdp.TargetInfo{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
		)
	}
	if strings.TrimSpace(targetID) != "" && strings.TrimSpace(urlContains) == "" {
		session, target, handled, err := a.attachExactPageSession(ctx, client, closeClient, targetID)
		if handled {
			return session, target, err
		}
	}
	target, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains)
	if err != nil {
		_ = closeClient(ctx)
		return nil, cdp.TargetInfo{}, err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
	if err != nil {
		_ = closeClient(ctx)
		return nil, cdp.TargetInfo{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("attach target %s: %v", target.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	return session, target, nil
}

func (a *app) attachExactPageSession(ctx context.Context, client cdp.CommandClient, closeClient func(context.Context) error, targetID string) (*cdp.PageSession, cdp.TargetInfo, bool, error) {
	targetID = strings.TrimSpace(targetID)
	target, err := cdp.TargetInfoWithClient(ctx, client, targetID)
	if err != nil {
		return nil, cdp.TargetInfo{}, false, nil
	}
	if target.Type != "page" {
		_ = closeClient(ctx)
		return nil, cdp.TargetInfo{}, true, targetNotFound(fmt.Sprintf("target %q is %q, not page", targetID, target.Type))
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
	if err != nil {
		_ = closeClient(ctx)
		return nil, cdp.TargetInfo{}, true, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("attach target %s: %v", target.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	return session, target, true, nil
}

func (a *app) attachPageEventSession(ctx context.Context, targetID, urlContains string) (browserEventClient, *cdp.PageSession, cdp.TargetInfo, error) {
	client, closeClient, err := a.browserEventCDPClient(ctx)
	if err != nil {
		return nil, nil, cdp.TargetInfo{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
		)
	}
	if strings.TrimSpace(targetID) != "" && strings.TrimSpace(urlContains) == "" {
		session, target, handled, err := a.attachExactPageSession(ctx, client, closeClient, targetID)
		if handled {
			return client, session, target, err
		}
	}
	target, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains)
	if err != nil {
		_ = closeClient(ctx)
		return nil, nil, cdp.TargetInfo{}, err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
	if err != nil {
		_ = closeClient(ctx)
		return nil, nil, cdp.TargetInfo{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("attach target %s: %v", target.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	return client, session, target, nil
}

func (a *app) resolvePageTarget(ctx context.Context, targetID, urlContains string) (cdp.TargetInfo, error) {
	targets, err := a.listTargets(ctx)
	if err != nil {
		return cdp.TargetInfo{}, err
	}
	return resolvePageTarget(targets, targetID, urlContains)
}

func (a *app) resolvePageTargetWithClient(ctx context.Context, client cdp.CommandClient, targetID, urlContains string) (cdp.TargetInfo, error) {
	if strings.TrimSpace(targetID) == "" && strings.TrimSpace(urlContains) == "" {
		if target, ok := a.selectedPageTarget(ctx, client); ok {
			return target, nil
		}
	}
	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return cdp.TargetInfo{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("list targets: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp daemon status --json"},
		)
	}
	return resolvePageTarget(targets, targetID, urlContains)
}

func (a *app) selectedPageTarget(ctx context.Context, client cdp.CommandClient) (cdp.TargetInfo, bool) {
	store, err := a.stateStore()
	if err != nil {
		return cdp.TargetInfo{}, false
	}
	file, err := store.Load(ctx)
	if err != nil {
		return cdp.TargetInfo{}, false
	}
	selection, ok := state.PageSelectionForConnection(file, a.connectionStateName(ctx))
	if !ok || strings.TrimSpace(selection.TargetID) == "" {
		return cdp.TargetInfo{}, false
	}
	target, err := cdp.TargetInfoWithClient(ctx, client, selection.TargetID)
	if err != nil || target.Type != "page" {
		return cdp.TargetInfo{}, false
	}
	return target, true
}

func (a *app) createPageTarget(ctx context.Context, client cdp.CommandClient, rawURL string) (string, error) {
	targetID, err := cdp.CreateTargetWithClient(ctx, client, rawURL)
	if err != nil {
		return "", commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("open page: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp pages --json"},
		)
	}
	return targetID, nil
}

func resolvePageTarget(targets []cdp.TargetInfo, targetID, urlContains string) (cdp.TargetInfo, error) {
	targetID = strings.TrimSpace(targetID)
	urlContains = strings.TrimSpace(urlContains)
	var pages []cdp.TargetInfo
	for _, target := range targets {
		if target.Type == "page" {
			pages = append(pages, target)
		}
	}
	if targetID != "" {
		var matches []cdp.TargetInfo
		for _, page := range pages {
			if page.TargetID == targetID || strings.HasPrefix(page.TargetID, targetID) {
				matches = append(matches, page)
			}
		}
		return onePageTarget(matches, fmt.Sprintf("target %q", targetID))
	}
	if urlContains != "" {
		for _, page := range pages {
			if strings.Contains(page.URL, urlContains) {
				return page, nil
			}
		}
		return cdp.TargetInfo{}, targetNotFound(fmt.Sprintf("no page URL contains %q", urlContains))
	}
	return onePageTarget(pages, "default page")
}

func onePageTarget(matches []cdp.TargetInfo, label string) (cdp.TargetInfo, error) {
	switch len(matches) {
	case 0:
		return cdp.TargetInfo{}, targetNotFound(fmt.Sprintf("no %s matched", label))
	case 1:
		return matches[0], nil
	default:
		return cdp.TargetInfo{}, commandError(
			"ambiguous_target",
			"usage",
			fmt.Sprintf("%s matched %d pages; pass a longer --target", label, len(matches)),
			ExitUsage,
			[]string{"cdp pages --json", "cdp snapshot --target <target-id> --json"},
		)
	}
}

func targetNotFound(message string) error {
	return commandError(
		"target_not_found",
		"usage",
		message,
		ExitUsage,
		[]string{"cdp pages --json", "cdp open <url> --json"},
	)
}

type pageSnapshot struct {
	URL      string         `json:"url"`
	Title    string         `json:"title"`
	Selector string         `json:"selector"`
	Count    int            `json:"count"`
	Items    []snapshotItem `json:"items"`
	Error    *snapshotError `json:"error,omitempty"`
}

type snapshotItem struct {
	Index      int          `json:"index"`
	Tag        string       `json:"tag"`
	Role       string       `json:"role,omitempty"`
	AriaLabel  string       `json:"aria_label,omitempty"`
	Text       string       `json:"text"`
	TextLength int          `json:"text_length"`
	Href       string       `json:"href,omitempty"`
	Rect       snapshotRect `json:"rect"`
}

type snapshotRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type snapshotError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func collectPageSnapshot(ctx context.Context, session *cdp.PageSession, selector string, limit, minChars int) (pageSnapshot, error) {
	if limit < 0 {
		return pageSnapshot{}, commandError(
			"usage",
			"usage",
			"--limit must be non-negative",
			ExitUsage,
			[]string{"cdp snapshot --limit 20 --json"},
		)
	}
	if minChars < 0 {
		return pageSnapshot{}, commandError(
			"usage",
			"usage",
			"--min-chars must be non-negative",
			ExitUsage,
			[]string{"cdp snapshot --min-chars 1 --json"},
		)
	}
	result, err := session.Evaluate(ctx, snapshotExpression(selector, limit, minChars), true)
	if err != nil {
		return pageSnapshot{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("snapshot target %s: %v", session.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	if result.Exception != nil {
		return pageSnapshot{}, commandError(
			"javascript_exception",
			"runtime",
			fmt.Sprintf("snapshot javascript exception: %s", result.Exception.Text),
			ExitCheckFailed,
			[]string{"cdp snapshot --selector body --json", "cdp pages --json"},
		)
	}
	var snapshot pageSnapshot
	if err := json.Unmarshal(result.Object.Value, &snapshot); err != nil {
		return pageSnapshot{}, commandError(
			"invalid_snapshot_result",
			"internal",
			fmt.Sprintf("decode snapshot result: %v", err),
			ExitInternal,
			[]string{"cdp doctor --json", "cdp eval 'document.title' --json"},
		)
	}
	if snapshot.Error != nil {
		return pageSnapshot{}, commandError(
			"invalid_selector",
			"usage",
			fmt.Sprintf("invalid selector %q: %s", selector, snapshot.Error.Message),
			ExitUsage,
			[]string{"cdp snapshot --selector body --json", "cdp snapshot --selector article --json"},
		)
	}
	return snapshot, nil
}

func snapshotExpression(selector string, limit, minChars int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const selector = %s;
  const limit = %d;
  const minChars = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const isVisible = (element) => {
    const style = window.getComputedStyle(element);
    if (style.visibility === "hidden" || style.display === "none") return false;
    const rect = element.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return {
      url: location.href,
      title: document.title,
      selector,
      count: 0,
      items: [],
      error: { name: error.name, message: error.message }
    };
  }
  const items = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    if (!isVisible(element)) continue;
    const text = normalize(element.innerText || element.textContent);
    if (text.length < minChars) continue;
    const rect = element.getBoundingClientRect();
    items.push({
      index,
      tag: element.tagName.toLowerCase(),
      role: element.getAttribute("role") || "",
      aria_label: element.getAttribute("aria-label") || "",
      text,
      text_length: text.length,
      href: element.href || "",
      rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height }
    });
    if (limit > 0 && items.length >= limit) break;
  }
  return { url: location.href, title: document.title, selector, count: items.length, items };
})()`, string(selectorJSON), limit, minChars)
}

func snapshotTextLines(items []snapshotItem) []string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		text := item.Text
		if len([]rune(text)) > 240 {
			text = string([]rune(text)[:240]) + "..."
		}
		lines = append(lines, fmt.Sprintf("%d\t%s", item.Index, text))
	}
	return lines
}

func (a *app) newOpenCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var newTab bool
	cmd := &cobra.Command{
		Use:   "open <url>",
		Short: "Open a URL in a new tab or navigate a selected page",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			closeOwned := true
			defer func() {
				if closeOwned {
					_ = closeClient(ctx)
				}
			}()
			rawURL := strings.TrimSpace(args[0])
			pageAction := "created"
			frameID := ""
			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			if newTab || (targetID == "" && urlContains == "") {
				createdID, err := a.createPageTarget(ctx, client, rawURL)
				if err != nil {
					return err
				}
				target.TargetID = createdID
			} else {
				selected, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains)
				if err != nil {
					return err
				}
				closeOwned = false
				session, err := cdp.AttachToTargetWithClient(ctx, client, selected.TargetID, closeClient)
				if err != nil {
					closeOwned = true
					return commandError(
						"connection_failed",
						"connection",
						fmt.Sprintf("attach target %s: %v", selected.TargetID, err),
						ExitConnection,
						[]string{"cdp pages --json", "cdp doctor --json"},
					)
				}
				defer session.Close(ctx)
				frameID, err = session.Navigate(ctx, rawURL)
				if err != nil {
					return commandError(
						"connection_failed",
						"connection",
						fmt.Sprintf("navigate target %s: %v", selected.TargetID, err),
						ExitConnection,
						[]string{"cdp pages --json", "cdp doctor --json"},
					)
				}
				target = selected
				target.URL = rawURL
				pageAction = "navigated"
			}
			page := pageRow(target)
			page["action"] = pageAction
			page["frame_id"] = frameID
			human := fmt.Sprintf("%s\t%s\t%s", pageAction, target.TargetID, rawURL)
			return a.render(ctx, human, map[string]any{
				"ok":     true,
				"action": pageAction,
				"page":   page,
			})
		},
	}
	cmd.Flags().BoolVar(&newTab, "new-tab", true, "open a new tab instead of navigating an existing page")
	cmd.Flags().StringVar(&targetID, "target", "", "navigate a page target by exact id or unique prefix when --new-tab=false")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "navigate the first page whose URL contains this text when --new-tab=false")
	return cmd
}

func (a *app) newEvalCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var awaitPromise bool
	cmd := &cobra.Command{
		Use:   "eval <expression>",
		Short: "Evaluate JavaScript in a page target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			result, err := session.Evaluate(ctx, args[0], awaitPromise)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("evaluate target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			if result.Exception != nil {
				return commandError(
					"javascript_exception",
					"runtime",
					fmt.Sprintf("javascript exception: %s", result.Exception.Text),
					ExitCheckFailed,
					[]string{"cdp eval 'document.title' --json", "cdp pages --json"},
				)
			}
			human := string(result.Object.Value)
			if human == "" {
				human = result.Object.Description
			}
			return a.render(ctx, human, map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"result": result.Object,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().BoolVar(&awaitPromise, "await-promise", true, "wait for promise results before returning")
	return cmd
}

type textResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Text     string     `json:"text"`
	Items    []textItem `json:"items"`
	Error    *evalError `json:"error,omitempty"`
}

type textItem struct {
	Index      int          `json:"index"`
	Tag        string       `json:"tag"`
	Text       string       `json:"text"`
	TextLength int          `json:"text_length"`
	Rect       snapshotRect `json:"rect"`
}

type htmlResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Items    []htmlItem `json:"items"`
	Error    *evalError `json:"error,omitempty"`
}

type htmlItem struct {
	Index      int    `json:"index"`
	Tag        string `json:"tag"`
	HTML       string `json:"html"`
	HTMLLength int    `json:"html_length"`
	Truncated  bool   `json:"truncated"`
}

type domQueryResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Nodes    []domNode  `json:"nodes"`
	Error    *evalError `json:"error,omitempty"`
}

type domNode struct {
	UID       string       `json:"uid"`
	Index     int          `json:"index"`
	Tag       string       `json:"tag"`
	ID        string       `json:"id_attr,omitempty"`
	Classes   []string     `json:"classes,omitempty"`
	Role      string       `json:"role,omitempty"`
	AriaLabel string       `json:"aria_label,omitempty"`
	Text      string       `json:"text,omitempty"`
	Href      string       `json:"href,omitempty"`
	Rect      snapshotRect `json:"rect"`
}

type cssInspectResult struct {
	URL      string            `json:"url"`
	Title    string            `json:"title"`
	Selector string            `json:"selector"`
	Found    bool              `json:"found"`
	Count    int               `json:"count"`
	Tag      string            `json:"tag,omitempty"`
	Styles   map[string]string `json:"styles,omitempty"`
	Rect     snapshotRect      `json:"rect"`
	Error    *evalError        `json:"error,omitempty"`
}

type layoutOverflowResult struct {
	URL      string               `json:"url"`
	Title    string               `json:"title"`
	Selector string               `json:"selector"`
	Count    int                  `json:"count"`
	Items    []layoutOverflowItem `json:"items"`
	Error    *evalError           `json:"error,omitempty"`
}

type layoutOverflowItem struct {
	UID          string       `json:"uid"`
	Index        int          `json:"index"`
	Tag          string       `json:"tag"`
	Text         string       `json:"text,omitempty"`
	Rect         snapshotRect `json:"rect"`
	ClientWidth  int          `json:"client_width"`
	ScrollWidth  int          `json:"scroll_width"`
	ClientHeight int          `json:"client_height"`
	ScrollHeight int          `json:"scroll_height"`
}

type waitResult struct {
	Kind         string     `json:"kind"`
	Needle       string     `json:"needle,omitempty"`
	Selector     string     `json:"selector,omitempty"`
	Matched      bool       `json:"matched"`
	Count        int        `json:"count,omitempty"`
	ElapsedMS    int64      `json:"elapsed_ms"`
	PollInterval string     `json:"poll_interval"`
	Error        *evalError `json:"error,omitempty"`
}

type evalError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func (a *app) newTextCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var limit int
	var minChars int
	cmd := &cobra.Command{
		Use:   "text <selector>",
		Short: "Extract compact visible text for a CSS selector",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if limit < 0 || minChars < 0 {
				return commandError("usage", "usage", "--limit and --min-chars must be non-negative", ExitUsage, []string{"cdp text main --limit 20 --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result textResult
			if err := evaluateJSONValue(ctx, session, textExpression(args[0], limit, minChars), "text", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp text body --json")
			}
			return a.render(ctx, result.Text, map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"text":   result,
				"items":  result.Items,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum number of text elements to return; use 0 for no limit")
	cmd.Flags().IntVar(&minChars, "min-chars", 1, "minimum normalized text length per item")
	return cmd
}

func (a *app) newHTMLCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var limit int
	var maxChars int
	cmd := &cobra.Command{
		Use:   "html <selector>",
		Short: "Extract compact HTML for a CSS selector",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if limit < 0 || maxChars < 0 {
				return commandError("usage", "usage", "--limit and --max-chars must be non-negative", ExitUsage, []string{"cdp html main --max-chars 4000 --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result htmlResult
			if err := evaluateJSONValue(ctx, session, htmlExpression(args[0], limit, maxChars), "html", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp html body --json")
			}
			lines := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				lines = append(lines, fmt.Sprintf("%d\t%s", item.Index, item.HTML))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"html":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().IntVar(&limit, "limit", 5, "maximum number of elements to return; use 0 for no limit")
	cmd.Flags().IntVar(&maxChars, "max-chars", 4000, "maximum HTML characters per element; use 0 for no truncation")
	return cmd
}

func (a *app) newDOMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dom",
		Short: "Inspect DOM nodes",
	}
	cmd.AddCommand(a.newDOMQueryCommand())
	return cmd
}

func (a *app) newDOMQueryCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var limit int
	cmd := &cobra.Command{
		Use:   "query <selector>",
		Short: "Return DOM node summaries for a CSS selector",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if limit < 0 {
				return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp dom query button --limit 20 --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result domQueryResult
			if err := evaluateJSONValue(ctx, session, domQueryExpression(args[0], limit), "dom query", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp dom query button --json")
			}
			lines := make([]string, 0, len(result.Nodes))
			for _, node := range result.Nodes {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s", node.UID, node.Tag, node.Text))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"query":  result,
				"nodes":  result.Nodes,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().IntVar(&limit, "limit", 25, "maximum number of nodes to return; use 0 for no limit")
	return cmd
}

func (a *app) newCSSCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "css",
		Short: "Inspect CSS and layout data",
	}
	cmd.AddCommand(a.newCSSInspectCommand())
	return cmd
}

func (a *app) newCSSInspectCommand() *cobra.Command {
	var targetID string
	var urlContains string
	cmd := &cobra.Command{
		Use:   "inspect <selector>",
		Short: "Return computed style and box data for the first matching element",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result cssInspectResult
			if err := evaluateJSONValue(ctx, session, cssInspectExpression(args[0]), "css inspect", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp css inspect main --json")
			}
			human := "no matching element"
			if result.Found {
				human = fmt.Sprintf("%s\tdisplay=%s\tposition=%s", result.Tag, result.Styles["display"], result.Styles["position"])
			}
			return a.render(ctx, human, map[string]any{
				"ok":      true,
				"target":  pageRow(target),
				"inspect": result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	return cmd
}

func (a *app) newLayoutCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "layout",
		Short: "Run page layout diagnostics",
	}
	cmd.AddCommand(a.newLayoutOverflowCommand())
	return cmd
}

func (a *app) newLayoutOverflowCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var selector string
	var limit int
	cmd := &cobra.Command{
		Use:   "overflow",
		Short: "Detect elements whose scroll size exceeds their client box",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if limit < 0 {
				return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp layout overflow --limit 20 --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result layoutOverflowResult
			if err := evaluateJSONValue(ctx, session, layoutOverflowExpression(selector, limit), "layout overflow", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(selector, result.Error, "cdp layout overflow --selector 'body *' --json")
			}
			lines := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%d>%d", item.UID, item.Tag, item.ScrollWidth, item.ClientWidth))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"overflow": result,
				"items":    result.Items,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&selector, "selector", "body *", "CSS selector to scan for overflow")
	cmd.Flags().IntVar(&limit, "limit", 25, "maximum number of overflowing elements to return; use 0 for no limit")
	return cmd
}

func (a *app) newWaitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for page conditions",
	}
	cmd.AddCommand(a.newWaitTextCommand())
	cmd.AddCommand(a.newWaitSelectorCommand())
	return cmd
}

func (a *app) newWaitTextCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "text <needle>",
		Short: "Wait until visible page text contains a string",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp wait text Ready --poll 250ms --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			result, err := waitForPageCondition(ctx, session, poll, func() (waitResult, error) {
				var result waitResult
				err := evaluateJSONValue(ctx, session, waitTextExpression(args[0]), "wait text", &result)
				return result, err
			})
			if err != nil {
				return err
			}
			if result.Error != nil {
				return commandError("javascript_exception", "runtime", result.Error.Message, ExitCheckFailed, []string{"cdp wait text Ready --json"})
			}
			result.ElapsedMS = time.Since(start).Milliseconds()
			result.PollInterval = poll.String()
			return a.render(ctx, fmt.Sprintf("matched text\t%s", args[0]), map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"wait":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while waiting")
	return cmd
}

func (a *app) newWaitSelectorCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "selector <css>",
		Short: "Wait until a CSS selector matches at least one element",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp wait selector main --poll 250ms --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			result, err := waitForPageCondition(ctx, session, poll, func() (waitResult, error) {
				var result waitResult
				err := evaluateJSONValue(ctx, session, waitSelectorExpression(args[0]), "wait selector", &result)
				return result, err
			})
			if err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp wait selector main --json")
			}
			result.ElapsedMS = time.Since(start).Milliseconds()
			result.PollInterval = poll.String()
			return a.render(ctx, fmt.Sprintf("matched selector\t%s", args[0]), map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"wait":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while waiting")
	return cmd
}

func waitForPageCondition(ctx context.Context, session *cdp.PageSession, poll time.Duration, check func() (waitResult, error)) (waitResult, error) {
	for {
		result, err := check()
		if err != nil {
			return waitResult{}, err
		}
		if result.Matched || result.Error != nil {
			return result, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return waitResult{}, commandError(
				"timeout",
				"timeout",
				fmt.Sprintf("wait condition not met for target %s: %v", session.TargetID, ctx.Err()),
				ExitTimeout,
				[]string{"cdp wait text <needle> --timeout 15s --json", "cdp wait selector <css> --timeout 15s --json"},
			)
		case <-timer.C:
		}
	}
}

func evaluateJSONValue(ctx context.Context, session *cdp.PageSession, expression, label string, out any) error {
	result, err := session.Evaluate(ctx, expression, true)
	if err != nil {
		return commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("%s target %s: %v", label, session.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	if result.Exception != nil {
		return commandError(
			"javascript_exception",
			"runtime",
			fmt.Sprintf("%s javascript exception: %s", label, result.Exception.Text),
			ExitCheckFailed,
			[]string{"cdp eval 'document.title' --json", "cdp pages --json"},
		)
	}
	if err := json.Unmarshal(result.Object.Value, out); err != nil {
		return commandError(
			"invalid_runtime_result",
			"internal",
			fmt.Sprintf("decode %s result: %v", label, err),
			ExitInternal,
			[]string{"cdp doctor --json", "cdp eval 'document.title' --json"},
		)
	}
	return nil
}

func invalidSelectorError(selector string, evalErr *evalError, example string) error {
	return commandError(
		"invalid_selector",
		"usage",
		fmt.Sprintf("invalid selector %q: %s", selector, evalErr.Message),
		ExitUsage,
		[]string{example},
	)
}

func textExpression(selector string, limit, minChars int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_text__";
  const selector = %s;
  const limit = %d;
  const minChars = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const rectFor = (element) => {
    const rect = element.getBoundingClientRect();
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, text: "", items: [], error: { name: error.name, message: error.message } };
  }
  const items = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    const text = normalize(element.innerText || element.textContent);
    if (text.length < minChars) continue;
    items.push({ index, tag: element.tagName.toLowerCase(), text, text_length: text.length, rect: rectFor(element) });
    if (limit > 0 && items.length >= limit) break;
  }
  return { url: location.href, title: document.title, selector, count: items.length, text: items.map((item) => item.text).join("\n"), items, marker };
})()`, string(selectorJSON), limit, minChars)
}

func htmlExpression(selector string, limit, maxChars int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_html__";
  const selector = %s;
  const limit = %d;
  const maxChars = %d;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, items: [], error: { name: error.name, message: error.message } };
  }
  const items = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    const full = element.outerHTML || "";
    const truncated = maxChars > 0 && full.length > maxChars;
    const html = truncated ? full.slice(0, maxChars) : full;
    items.push({ index, tag: element.tagName.toLowerCase(), html, html_length: full.length, truncated });
    if (limit > 0 && items.length >= limit) break;
  }
  return { url: location.href, title: document.title, selector, count: items.length, items, marker };
})()`, string(selectorJSON), limit, maxChars)
}

func domQueryExpression(selector string, limit int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_dom_query__";
  const selector = %s;
  const limit = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const rectFor = (element) => {
    const rect = element.getBoundingClientRect();
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, nodes: [], error: { name: error.name, message: error.message } };
  }
  const nodes = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    nodes.push({
      uid: "css:" + selector + ":" + index,
      index,
      tag: element.tagName.toLowerCase(),
      id_attr: element.id || "",
      classes: Array.from(element.classList || []),
      role: element.getAttribute("role") || "",
      aria_label: element.getAttribute("aria-label") || "",
      text: normalize(element.innerText || element.textContent).slice(0, 500),
      href: element.href || "",
      rect: rectFor(element)
    });
    if (limit > 0 && nodes.length >= limit) break;
  }
  return { url: location.href, title: document.title, selector, count: nodes.length, nodes, marker };
})()`, string(selectorJSON), limit)
}

func cssInspectExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_css_inspect__";
  const selector = %s;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, found: false, count: 0, error: { name: error.name, message: error.message } };
  }
  const element = elements[0];
  if (!element) return { url: location.href, title: document.title, selector, found: false, count: 0, marker };
  const style = window.getComputedStyle(element);
  const rect = element.getBoundingClientRect();
  const pick = ["display", "position", "overflow", "overflowX", "overflowY", "color", "backgroundColor", "fontSize", "fontWeight", "lineHeight", "zIndex"];
  const styles = {};
  for (const key of pick) styles[key] = style[key] || "";
  return {
    url: location.href,
    title: document.title,
    selector,
    found: true,
    count: elements.length,
    tag: element.tagName.toLowerCase(),
    styles,
    rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
    marker
  };
})()`, string(selectorJSON))
}

func layoutOverflowExpression(selector string, limit int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_layout_overflow__";
  const selector = %s;
  const limit = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const rectFor = (element) => {
    const rect = element.getBoundingClientRect();
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, items: [], error: { name: error.name, message: error.message } };
  }
  const items = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    if (element.scrollWidth <= element.clientWidth && element.scrollHeight <= element.clientHeight) continue;
    items.push({
      uid: "overflow:" + index,
      index,
      tag: element.tagName.toLowerCase(),
      text: normalize(element.innerText || element.textContent).slice(0, 240),
      rect: rectFor(element),
      client_width: element.clientWidth,
      scroll_width: element.scrollWidth,
      client_height: element.clientHeight,
      scroll_height: element.scrollHeight
    });
    if (limit > 0 && items.length >= limit) break;
  }
  return { url: location.href, title: document.title, selector, count: items.length, items, marker };
})()`, string(selectorJSON), limit)
}

func waitTextExpression(needle string) string {
	needleJSON, _ := json.Marshal(needle)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_wait_text__";
  const needle = %s;
  const text = (document.body && (document.body.innerText || document.body.textContent) || "");
  return { kind: "text", needle, matched: text.includes(needle), count: text.includes(needle) ? 1 : 0, marker };
})()`, string(needleJSON))
}

func waitSelectorExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_wait_selector__";
  const selector = %s;
  try {
    const count = document.querySelectorAll(selector).length;
    return { kind: "selector", selector, matched: count > 0, count, marker };
  } catch (error) {
    return { kind: "selector", selector, matched: false, count: 0, error: { name: error.name, message: error.message }, marker };
  }
})()`, string(selectorJSON))
}

func (a *app) newSnapshotCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var selector string
	var limit int
	var minChars int
	var interactiveOnly bool
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Print compact visible text from a page target",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			snapshot, err := collectPageSnapshot(ctx, session, selector, limit, minChars)
			if err != nil {
				return err
			}
			lines := snapshotTextLines(snapshot.Items)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":               true,
				"target":           pageRow(target),
				"snapshot":         snapshot,
				"items":            snapshot.Items,
				"interactive_only": interactiveOnly,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&selector, "selector", "body", "CSS selector to extract visible text from; use article for social feeds")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of text items to return; use 0 for no limit")
	cmd.Flags().IntVar(&minChars, "min-chars", 1, "minimum normalized text length per item")
	cmd.Flags().BoolVar(&interactiveOnly, "interactive-only", false, "reserved compatibility flag; snapshot still returns visible text items")
	return cmd
}

func (a *app) newScreenshotCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var outPath string
	var format string
	var quality int
	var fullPage bool
	cmd := &cobra.Command{
		Use:   "screenshot",
		Short: "Capture a page screenshot to a file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			outPath = strings.TrimSpace(outPath)
			if outPath == "" {
				return commandError(
					"missing_output_path",
					"usage",
					"screenshot requires --out <path>",
					ExitUsage,
					[]string{"cdp screenshot --out tmp/page.png --json"},
				)
			}
			normalizedFormat, err := normalizeScreenshotFormat(format, outPath)
			if err != nil {
				return err
			}
			if quality < 0 || quality > 100 {
				return commandError(
					"invalid_screenshot_quality",
					"usage",
					"--quality must be between 0 and 100",
					ExitUsage,
					[]string{"cdp screenshot --format jpeg --quality 80 --out tmp/page.jpg --json"},
				)
			}
			if normalizedFormat == "png" && quality > 0 {
				return commandError(
					"invalid_screenshot_quality",
					"usage",
					"--quality is only supported for jpeg and webp screenshots",
					ExitUsage,
					[]string{"cdp screenshot --format jpeg --quality 80 --out tmp/page.jpg --json"},
				)
			}

			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			shot, err := session.CaptureScreenshot(ctx, cdp.ScreenshotOptions{
				Format:   normalizedFormat,
				Quality:  quality,
				FullPage: fullPage,
			})
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture screenshot target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			writtenPath, err := writeArtifactFile(outPath, shot.Data)
			if err != nil {
				return err
			}
			screenshot := map[string]any{
				"path":      writtenPath,
				"bytes":     len(shot.Data),
				"format":    shot.Format,
				"full_page": fullPage,
			}
			if quality > 0 {
				screenshot["quality"] = quality
			}
			human := fmt.Sprintf("%s\t%d bytes", writtenPath, len(shot.Data))
			return a.render(ctx, human, map[string]any{
				"ok":         true,
				"target":     pageRow(target),
				"screenshot": screenshot,
				"artifacts": []map[string]any{
					{"type": "screenshot", "path": writtenPath},
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&outPath, "out", "", "required path to write the screenshot image")
	cmd.Flags().StringVar(&format, "format", "", "screenshot format: png, jpeg, or webp; defaults to file extension or png")
	cmd.Flags().IntVar(&quality, "quality", 0, "jpeg/webp quality from 1 to 100; 0 uses Chrome's default")
	cmd.Flags().BoolVar(&fullPage, "full-page", false, "capture beyond the viewport when Chrome supports it")
	return cmd
}

func normalizeScreenshotFormat(format, outPath string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		switch strings.ToLower(filepath.Ext(outPath)) {
		case ".jpg", ".jpeg":
			format = "jpeg"
		case ".webp":
			format = "webp"
		default:
			format = "png"
		}
	}
	if format == "jpg" {
		format = "jpeg"
	}
	switch format {
	case "png", "jpeg", "webp":
		return format, nil
	default:
		return "", commandError(
			"invalid_screenshot_format",
			"usage",
			fmt.Sprintf("unsupported screenshot format %q", format),
			ExitUsage,
			[]string{"cdp screenshot --format png --out tmp/page.png --json", "cdp screenshot --format jpeg --out tmp/page.jpg --json"},
		)
	}
}

func writeArtifactFile(path string, data []byte) (string, error) {
	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", commandError(
				"artifact_write_failed",
				"io",
				fmt.Sprintf("create artifact directory: %v", err),
				ExitInternal,
				[]string{"cdp screenshot --out tmp/page.png --json"},
			)
		}
	}
	if err := os.WriteFile(cleanPath, data, 0o600); err != nil {
		return "", commandError(
			"artifact_write_failed",
			"io",
			fmt.Sprintf("write artifact %s: %v", cleanPath, err),
			ExitInternal,
			[]string{"cdp screenshot --out tmp/page.png --json"},
		)
	}
	return cleanPath, nil
}

func (a *app) newConsoleCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var wait time.Duration
	var limit int
	var errorsOnly bool
	var types string
	cmd := &cobra.Command{
		Use:   "console",
		Short: "Capture console and browser log messages from a page target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if wait < 0 {
				return commandError(
					"usage",
					"usage",
					"--wait must be non-negative",
					ExitUsage,
					[]string{"cdp console --wait 2s --json"},
				)
			}
			if limit < 0 {
				return commandError(
					"usage",
					"usage",
					"--limit must be non-negative",
					ExitUsage,
					[]string{"cdp console --limit 50 --json"},
				)
			}

			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			typeSet := parseCSVSet(types)
			messages, truncated, err := collectConsoleMessages(ctx, client, session.SessionID, wait, limit, errorsOnly, typeSet)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture console target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			lines := consoleMessageLines(messages)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"messages": messages,
				"console": map[string]any{
					"count":       len(messages),
					"wait":        durationString(wait),
					"limit":       limit,
					"truncated":   truncated,
					"errors_only": errorsOnly,
					"types":       setKeys(typeSet),
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().DurationVar(&wait, "wait", time.Second, "how long to collect console/log events after attaching")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of messages to return; use 0 for no limit")
	cmd.Flags().BoolVar(&errorsOnly, "errors", false, "only return warnings, errors, assertions, and exceptions")
	cmd.Flags().StringVar(&types, "types", "", "comma-separated console types or log levels to keep, such as error,warning")
	return cmd
}

func (a *app) newNetworkCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var wait time.Duration
	var limit int
	var failedOnly bool
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Inspect network requests from a page target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if wait < 0 {
				return commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp network --wait 2s --json"})
			}
			if limit < 0 {
				return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp network --limit 50 --json"})
			}

			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			requests, truncated, err := collectNetworkRequests(ctx, client, session.SessionID, wait, limit, failedOnly)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture network target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			lines := networkRequestLines(requests)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"requests": requests,
				"network": map[string]any{
					"count":       len(requests),
					"wait":        durationString(wait),
					"limit":       limit,
					"truncated":   truncated,
					"failed_only": failedOnly,
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().DurationVar(&wait, "wait", time.Second, "how long to collect network events after attaching")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum number of requests to return; use 0 for no limit")
	cmd.Flags().BoolVar(&failedOnly, "failed", false, "only return failed requests and HTTP 4xx/5xx responses")
	cmd.AddCommand(a.newNetworkCaptureCommand())
	return cmd
}

func (a *app) newNetworkCaptureCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var wait time.Duration
	var limit int
	var outPath string
	var includeHeaders bool
	var includeInitiators bool
	var includeTiming bool
	var includePostData bool
	var includeBodies string
	var bodyLimit int
	var redact string
	var reload bool
	var ignoreCache bool
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Capture full local network metadata from a page target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || limit < 0 || bodyLimit < 0 {
				return commandError("usage", "usage", "--wait, --limit, and --body-limit must be non-negative", ExitUsage, []string{"cdp network capture --wait 10s --json"})
			}
			redact = strings.ToLower(strings.TrimSpace(redact))
			if redact == "" {
				redact = "none"
			}
			if redact != "none" && redact != "safe" && redact != "headers" {
				return commandError("usage", "usage", "--redact must be none, safe, or headers", ExitUsage, []string{"cdp network capture --redact safe --json"})
			}
			rawBodyKinds := parseCSVSet(includeBodies)
			if invalid := invalidBodyKinds(rawBodyKinds); len(invalid) > 0 {
				return commandError("usage", "usage", "--include-bodies only accepts json, text, base64, all, or none", ExitUsage, []string{"cdp network capture --include-bodies json,text --json"})
			}
			bodyKinds := parseBodyKinds(includeBodies)
			fallback := wait + 10*time.Second
			if fallback < 10*time.Second {
				fallback = 10 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			trigger := "observe"
			var afterEnable func() error
			if reload {
				trigger = "reload"
				afterEnable = func() error {
					return session.Reload(ctx, ignoreCache)
				}
			}
			records, truncated, collectorErrors, err := collectNetworkCapture(ctx, client, session.SessionID, networkCaptureOptions{
				Wait:              wait,
				Limit:             limit,
				IncludeHeaders:    includeHeaders,
				IncludeInitiators: includeInitiators,
				IncludeTiming:     includeTiming,
				IncludePostData:   includePostData,
				BodyKinds:         bodyKinds,
				BodyLimit:         bodyLimit,
				AfterEnable:       afterEnable,
			})
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture full network target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			applyNetworkCaptureRedaction(records, redact)
			capture := map[string]any{
				"count":              len(records),
				"wait":               durationString(wait),
				"limit":              limit,
				"truncated":          truncated,
				"include_headers":    includeHeaders,
				"include_initiators": includeInitiators,
				"include_timing":     includeTiming,
				"include_post_data":  includePostData,
				"include_bodies":     setKeys(bodyKinds),
				"body_limit":         bodyLimit,
				"redact":             redact,
				"trigger":            trigger,
				"ignore_cache":       ignoreCache,
				"collector_errors":   collectorErrors,
			}
			if strings.TrimSpace(outPath) != "" && redact == "none" {
				capture["local_artifact_warning"] = "network capture may include cookies, authorization headers, tokens, request bodies, and response bodies; keep this artifact local"
			}
			report := map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"requests": records,
				"capture":  capture,
			}
			if strings.TrimSpace(outPath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal network capture report: %v", err), ExitInternal, []string{"cdp network capture --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "network-capture", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "network-capture", "path": writtenPath}}
			}
			human := fmt.Sprintf("network-capture\t%d requests", len(records))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect network events after attaching")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum requests to return; use 0 for no limit")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the JSON network capture artifact")
	cmd.Flags().BoolVar(&includeHeaders, "include-headers", true, "include request and response headers")
	cmd.Flags().BoolVar(&includeInitiators, "include-initiators", true, "include CDP initiator metadata and stack frames")
	cmd.Flags().BoolVar(&includeTiming, "include-timing", true, "include response timing and connection metadata")
	cmd.Flags().BoolVar(&includePostData, "include-post-data", true, "include request post data when CDP exposes it")
	cmd.Flags().StringVar(&includeBodies, "include-bodies", "json,text", "comma-separated response body kinds to include: json,text,base64,all")
	cmd.Flags().IntVar(&bodyLimit, "body-limit", 256*1024, "maximum request/response body bytes to include; 0 means no limit")
	cmd.Flags().StringVar(&redact, "redact", "none", "redaction preset for output and artifacts: none, safe, or headers")
	cmd.Flags().BoolVar(&reload, "reload", false, "reload the selected page after attaching network collectors")
	cmd.Flags().BoolVar(&ignoreCache, "ignore-cache", false, "reload while bypassing cache")
	return cmd
}

type networkRequest struct {
	ID                string  `json:"id"`
	URL               string  `json:"url,omitempty"`
	Method            string  `json:"method,omitempty"`
	ResourceType      string  `json:"resource_type,omitempty"`
	Status            int     `json:"status,omitempty"`
	StatusText        string  `json:"status_text,omitempty"`
	MimeType          string  `json:"mime_type,omitempty"`
	Failed            bool    `json:"failed"`
	ErrorText         string  `json:"error_text,omitempty"`
	Canceled          bool    `json:"canceled,omitempty"`
	EncodedDataLength float64 `json:"encoded_data_length,omitempty"`
}

type networkCaptureOptions struct {
	Wait              time.Duration
	Limit             int
	IncludeHeaders    bool
	IncludeInitiators bool
	IncludeTiming     bool
	IncludePostData   bool
	BodyKinds         map[string]bool
	BodyLimit         int
	AfterEnable       func() error
}

type networkCaptureRecord struct {
	ID                 string                 `json:"id"`
	URL                string                 `json:"url,omitempty"`
	Method             string                 `json:"method,omitempty"`
	ResourceType       string                 `json:"resource_type,omitempty"`
	DocumentURL        string                 `json:"document_url,omitempty"`
	LoaderID           string                 `json:"loader_id,omitempty"`
	Timestamp          float64                `json:"timestamp,omitempty"`
	WallTime           float64                `json:"wall_time,omitempty"`
	RequestHeaders     map[string]any         `json:"request_headers,omitempty"`
	RequestPostData    *networkCaptureBody    `json:"request_post_data,omitempty"`
	RequestHasPostData bool                   `json:"-"`
	ResponseHeaders    map[string]any         `json:"response_headers,omitempty"`
	Status             int                    `json:"status,omitempty"`
	StatusText         string                 `json:"status_text,omitempty"`
	MimeType           string                 `json:"mime_type,omitempty"`
	Protocol           string                 `json:"protocol,omitempty"`
	RemoteIPAddress    string                 `json:"remote_ip_address,omitempty"`
	RemotePort         int                    `json:"remote_port,omitempty"`
	ConnectionID       float64                `json:"connection_id,omitempty"`
	ConnectionReused   bool                   `json:"connection_reused,omitempty"`
	FromDiskCache      bool                   `json:"from_disk_cache,omitempty"`
	FromServiceWorker  bool                   `json:"from_service_worker,omitempty"`
	EncodedDataLength  float64                `json:"encoded_data_length,omitempty"`
	DecodedBodyLength  float64                `json:"decoded_body_length,omitempty"`
	Initiator          json.RawMessage        `json:"initiator,omitempty"`
	Timing             json.RawMessage        `json:"timing,omitempty"`
	Redirects          []networkCaptureRecord `json:"redirects,omitempty"`
	Body               *networkCaptureBody    `json:"body,omitempty"`
	Failed             bool                   `json:"failed"`
	ErrorText          string                 `json:"error_text,omitempty"`
	Canceled           bool                   `json:"canceled,omitempty"`
}

type networkCaptureBody struct {
	Text          string `json:"text,omitempty"`
	Base64Encoded bool   `json:"base64_encoded,omitempty"`
	Bytes         int    `json:"bytes"`
	Truncated     bool   `json:"truncated,omitempty"`
	OmittedReason string `json:"omitted_reason,omitempty"`
}

func collectNetworkRequests(ctx context.Context, client browserEventClient, sessionID string, wait time.Duration, limit int, failedOnly bool) ([]networkRequest, bool, error) {
	if err := client.CallSession(ctx, sessionID, "Network.enable", map[string]any{}, nil); err != nil {
		return nil, false, err
	}

	requestsByID := map[string]*networkRequest{}
	var order []string
	addEvent := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		req, ok := networkRequestFromEvent(event)
		if !ok || req.ID == "" {
			return
		}
		existing, ok := requestsByID[req.ID]
		if !ok {
			copyReq := req
			requestsByID[req.ID] = &copyReq
			order = append(order, req.ID)
			return
		}
		mergeNetworkRequest(existing, req)
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, event := range events {
		addEvent(event)
	}
	if wait > 0 {
		eventCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for {
			event, err := client.ReadEvent(eventCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded) {
					break
				}
				return nil, false, err
			}
			addEvent(event)
		}
	}

	var requests []networkRequest
	for _, id := range order {
		req := *requestsByID[id]
		if failedOnly && !requestFailed(req) {
			continue
		}
		requests = append(requests, req)
	}
	truncated := false
	if limit > 0 && len(requests) > limit {
		requests = requests[:limit]
		truncated = true
	}
	return requests, truncated, nil
}

func collectNetworkCapture(ctx context.Context, client browserEventClient, sessionID string, opts networkCaptureOptions) ([]networkCaptureRecord, bool, []map[string]string, error) {
	enableParams := map[string]any{}
	if opts.BodyLimit > 0 {
		enableParams["maxPostDataSize"] = opts.BodyLimit
	}
	if err := client.CallSession(ctx, sessionID, "Network.enable", enableParams, nil); err != nil {
		return nil, false, nil, err
	}
	collectorErrors := []map[string]string{}
	if opts.AfterEnable != nil {
		if err := opts.AfterEnable(); err != nil {
			collectorErrors = append(collectorErrors, collectorError("trigger", err))
		}
	}

	recordsByID := map[string]*networkCaptureRecord{}
	var order []string
	ensure := func(id string) *networkCaptureRecord {
		record, ok := recordsByID[id]
		if !ok {
			record = &networkCaptureRecord{ID: id}
			recordsByID[id] = record
			order = append(order, id)
		}
		return record
	}
	addEvent := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		switch event.Method {
		case "Network.requestWillBeSent":
			mergeCaptureRequestWillBeSent(event.Params, ensure, opts)
		case "Network.requestWillBeSentExtraInfo":
			if opts.IncludeHeaders {
				mergeCaptureRequestExtraInfo(event.Params, ensure)
			}
		case "Network.responseReceived":
			mergeCaptureResponseReceived(event.Params, ensure, opts)
		case "Network.responseReceivedExtraInfo":
			if opts.IncludeHeaders {
				mergeCaptureResponseExtraInfo(event.Params, ensure)
			}
		case "Network.loadingFinished":
			mergeCaptureLoadingFinished(event.Params, ensure)
		case "Network.loadingFailed":
			mergeCaptureLoadingFailed(event.Params, ensure)
		}
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return nil, false, nil, err
	}
	for _, event := range events {
		addEvent(event)
	}
	if opts.Wait > 0 {
		eventCtx, cancel := context.WithTimeout(ctx, opts.Wait)
		defer cancel()
		for {
			event, err := client.ReadEvent(eventCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded) {
					break
				}
				return nil, false, nil, err
			}
			addEvent(event)
		}
	}

	for _, id := range order {
		record := recordsByID[id]
		if opts.IncludePostData && record.RequestHasPostData {
			if err := enrichRequestPostData(ctx, client, sessionID, record, opts.BodyLimit); err != nil {
				collectorErrors = append(collectorErrors, collectorError("request_post_data", err))
			}
		}
		if len(opts.BodyKinds) > 0 && shouldCaptureResponseBody(*record, opts.BodyKinds) {
			if err := enrichResponseBody(ctx, client, sessionID, record, opts.BodyLimit); err != nil {
				collectorErrors = append(collectorErrors, collectorError("response_body", err))
			}
		}
	}

	records := make([]networkCaptureRecord, 0, len(order))
	for _, id := range order {
		records = append(records, *recordsByID[id])
	}
	truncated := false
	if opts.Limit > 0 && len(records) > opts.Limit {
		records = records[:opts.Limit]
		truncated = true
	}
	return records, truncated, collectorErrors, nil
}

func mergeCaptureRequestWillBeSent(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord, opts networkCaptureOptions) {
	var params struct {
		RequestID        string          `json:"requestId"`
		LoaderID         string          `json:"loaderId"`
		DocumentURL      string          `json:"documentURL"`
		Type             string          `json:"type"`
		Timestamp        float64         `json:"timestamp"`
		WallTime         float64         `json:"wallTime"`
		Initiator        json.RawMessage `json:"initiator"`
		RedirectResponse json.RawMessage `json:"redirectResponse"`
		Request          struct {
			URL         string         `json:"url"`
			Method      string         `json:"method"`
			Headers     map[string]any `json:"headers"`
			HasPostData bool           `json:"hasPostData"`
			PostData    string         `json:"postData"`
		} `json:"request"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	if len(params.RedirectResponse) > 0 && string(params.RedirectResponse) != "null" {
		if redirect := captureRedirectFromResponse(params.RedirectResponse, opts); redirect.Status != 0 || redirect.URL != "" {
			record.Redirects = append(record.Redirects, redirect)
		}
	}
	record.URL = params.Request.URL
	record.Method = params.Request.Method
	record.ResourceType = params.Type
	record.DocumentURL = params.DocumentURL
	record.LoaderID = params.LoaderID
	record.Timestamp = params.Timestamp
	record.WallTime = params.WallTime
	record.RequestHasPostData = params.Request.HasPostData || params.Request.PostData != ""
	if opts.IncludeHeaders && len(params.Request.Headers) > 0 {
		record.RequestHeaders = params.Request.Headers
	}
	if opts.IncludePostData && params.Request.PostData != "" {
		record.RequestPostData = captureBody(params.Request.PostData, false, opts.BodyLimit)
	}
	if opts.IncludeInitiators && len(params.Initiator) > 0 && string(params.Initiator) != "null" {
		record.Initiator = params.Initiator
	}
}

func captureRedirectFromResponse(raw json.RawMessage, opts networkCaptureOptions) networkCaptureRecord {
	var response struct {
		URL          string          `json:"url"`
		Status       int             `json:"status"`
		StatusText   string          `json:"statusText"`
		Headers      map[string]any  `json:"headers"`
		MimeType     string          `json:"mimeType"`
		Protocol     string          `json:"protocol"`
		RemoteIP     string          `json:"remoteIPAddress"`
		RemotePort   int             `json:"remotePort"`
		Timing       json.RawMessage `json:"timing"`
		EncodedBytes float64         `json:"encodedDataLength"`
	}
	_ = json.Unmarshal(raw, &response)
	redirect := networkCaptureRecord{
		URL:               response.URL,
		Status:            response.Status,
		StatusText:        response.StatusText,
		MimeType:          response.MimeType,
		Protocol:          response.Protocol,
		RemoteIPAddress:   response.RemoteIP,
		RemotePort:        response.RemotePort,
		EncodedDataLength: response.EncodedBytes,
	}
	if opts.IncludeHeaders {
		redirect.ResponseHeaders = response.Headers
	}
	if opts.IncludeTiming {
		redirect.Timing = response.Timing
	}
	return redirect
}

func mergeCaptureRequestExtraInfo(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord) {
	var params struct {
		RequestID string         `json:"requestId"`
		Headers   map[string]any `json:"headers"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	if len(params.Headers) > 0 {
		ensure(params.RequestID).RequestHeaders = params.Headers
	}
}

func mergeCaptureResponseReceived(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord, opts networkCaptureOptions) {
	var params struct {
		RequestID string `json:"requestId"`
		Type      string `json:"type"`
		Response  struct {
			URL               string          `json:"url"`
			Status            int             `json:"status"`
			StatusText        string          `json:"statusText"`
			Headers           map[string]any  `json:"headers"`
			MimeType          string          `json:"mimeType"`
			Protocol          string          `json:"protocol"`
			RemoteIPAddress   string          `json:"remoteIPAddress"`
			RemotePort        int             `json:"remotePort"`
			ConnectionID      float64         `json:"connectionId"`
			ConnectionReused  bool            `json:"connectionReused"`
			FromDiskCache     bool            `json:"fromDiskCache"`
			FromServiceWorker bool            `json:"fromServiceWorker"`
			EncodedDataLength float64         `json:"encodedDataLength"`
			Timing            json.RawMessage `json:"timing"`
		} `json:"response"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	record.ResourceType = firstNonEmpty(record.ResourceType, params.Type)
	record.URL = firstNonEmpty(params.Response.URL, record.URL)
	record.Status = params.Response.Status
	record.StatusText = params.Response.StatusText
	record.MimeType = params.Response.MimeType
	record.Protocol = params.Response.Protocol
	record.RemoteIPAddress = params.Response.RemoteIPAddress
	record.RemotePort = params.Response.RemotePort
	record.ConnectionID = params.Response.ConnectionID
	record.ConnectionReused = params.Response.ConnectionReused
	record.FromDiskCache = params.Response.FromDiskCache
	record.FromServiceWorker = params.Response.FromServiceWorker
	record.EncodedDataLength = params.Response.EncodedDataLength
	if opts.IncludeHeaders && len(params.Response.Headers) > 0 {
		record.ResponseHeaders = params.Response.Headers
	}
	if opts.IncludeTiming && len(params.Response.Timing) > 0 && string(params.Response.Timing) != "null" {
		record.Timing = params.Response.Timing
	}
}

func mergeCaptureResponseExtraInfo(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord) {
	var params struct {
		RequestID  string         `json:"requestId"`
		StatusCode int            `json:"statusCode"`
		Headers    map[string]any `json:"headers"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	if params.StatusCode != 0 {
		record.Status = params.StatusCode
	}
	if len(params.Headers) > 0 {
		record.ResponseHeaders = params.Headers
	}
}

func mergeCaptureLoadingFinished(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord) {
	var params struct {
		RequestID         string  `json:"requestId"`
		EncodedDataLength float64 `json:"encodedDataLength"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	ensure(params.RequestID).EncodedDataLength = params.EncodedDataLength
}

func mergeCaptureLoadingFailed(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord) {
	var params struct {
		RequestID string `json:"requestId"`
		Type      string `json:"type"`
		ErrorText string `json:"errorText"`
		Canceled  bool   `json:"canceled"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	record.ResourceType = firstNonEmpty(record.ResourceType, params.Type)
	record.Failed = true
	record.ErrorText = params.ErrorText
	record.Canceled = params.Canceled
}

func enrichRequestPostData(ctx context.Context, client browserEventClient, sessionID string, record *networkCaptureRecord, bodyLimit int) error {
	if record.RequestPostData != nil {
		return nil
	}
	var result struct {
		PostData string `json:"postData"`
	}
	err := client.CallSession(ctx, sessionID, "Network.getRequestPostData", map[string]any{"requestId": record.ID}, &result)
	if err != nil {
		record.RequestPostData = &networkCaptureBody{OmittedReason: err.Error()}
		return nil
	}
	record.RequestPostData = captureBody(result.PostData, false, bodyLimit)
	return nil
}

func enrichResponseBody(ctx context.Context, client browserEventClient, sessionID string, record *networkCaptureRecord, bodyLimit int) error {
	var result struct {
		Body          string `json:"body"`
		Base64Encoded bool   `json:"base64Encoded"`
	}
	err := client.CallSession(ctx, sessionID, "Network.getResponseBody", map[string]any{"requestId": record.ID}, &result)
	if err != nil {
		record.Body = &networkCaptureBody{OmittedReason: err.Error()}
		return nil
	}
	record.Body = captureBody(result.Body, result.Base64Encoded, bodyLimit)
	if !result.Base64Encoded {
		record.DecodedBodyLength = float64(record.Body.Bytes)
	}
	return nil
}

func captureBody(text string, base64Encoded bool, limit int) *networkCaptureBody {
	bytes := len([]byte(text))
	body := &networkCaptureBody{Text: text, Base64Encoded: base64Encoded, Bytes: bytes}
	if limit > 0 && bytes > limit {
		body.Text = string([]byte(text)[:limit])
		body.Truncated = true
	}
	return body
}

func parseBodyKinds(includeBodies string) map[string]bool {
	set := parseCSVSet(includeBodies)
	if set["all"] {
		return parseCSVSet("json,text,base64")
	}
	if set["none"] {
		return map[string]bool{}
	}
	return set
}

func invalidBodyKinds(kinds map[string]bool) []string {
	var invalid []string
	for kind := range kinds {
		if kind != "json" && kind != "text" && kind != "base64" && kind != "all" && kind != "none" {
			invalid = append(invalid, kind)
		}
	}
	sort.Strings(invalid)
	return invalid
}

func shouldCaptureResponseBody(record networkCaptureRecord, kinds map[string]bool) bool {
	if len(kinds) == 0 || record.Failed {
		return false
	}
	mime := strings.ToLower(record.MimeType)
	if kinds["base64"] {
		return true
	}
	if kinds["json"] && strings.Contains(mime, "json") {
		return true
	}
	if kinds["text"] && (strings.HasPrefix(mime, "text/") || strings.Contains(mime, "javascript") || strings.Contains(mime, "xml")) {
		return true
	}
	return false
}

func applyNetworkCaptureRedaction(records []networkCaptureRecord, redact string) {
	if redact == "" || redact == "none" {
		return
	}
	for i := range records {
		redactCaptureRecord(&records[i], redact)
	}
}

func redactCaptureRecord(record *networkCaptureRecord, redact string) {
	record.URL = redactURL(record.URL, redact)
	record.DocumentURL = redactURL(record.DocumentURL, redact)
	record.RequestHeaders = redactHeaderMap(record.RequestHeaders, redact)
	record.ResponseHeaders = redactHeaderMap(record.ResponseHeaders, redact)
	if record.RequestPostData != nil && record.RequestPostData.Text != "" {
		record.RequestPostData.Text = redactBodyText(record.RequestPostData.Text, redact)
	}
	if record.Body != nil && record.Body.Text != "" {
		record.Body.Text = redactBodyText(record.Body.Text, redact)
	}
	for i := range record.Redirects {
		redactCaptureRecord(&record.Redirects[i], redact)
	}
}

func redactHeaderMap(headers map[string]any, redact string) map[string]any {
	if len(headers) == 0 {
		return headers
	}
	out := map[string]any{}
	for key, value := range headers {
		if redact == "headers" || sensitiveName(key) || sensitiveHeaderValue(value) {
			out[key] = "<redacted>"
			continue
		}
		out[key] = value
	}
	return out
}

func sensitiveHeaderValue(value any) bool {
	text, ok := value.(string)
	return ok && strings.Contains(strings.ToLower(text), "bearer ")
}

func redactURL(rawURL, redact string) string {
	if rawURL == "" || redact != "safe" {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		if sensitiveName(key) {
			query.Set(key, "<redacted>")
			changed = true
		}
	}
	if changed {
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}

func redactBodyText(text, redact string) string {
	if redact == "headers" {
		return "<redacted>"
	}
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err == nil {
		return marshalCompact(redactJSONValue(decoded))
	}
	values, err := url.ParseQuery(text)
	if err == nil && len(values) > 0 {
		changed := false
		for key := range values {
			if sensitiveName(key) {
				values.Set(key, "<redacted>")
				changed = true
			}
		}
		if changed {
			return values.Encode()
		}
	}
	if strings.Contains(strings.ToLower(text), "bearer ") {
		return "<redacted>"
	}
	return text
}

func redactJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, child := range typed {
			if sensitiveName(key) {
				out[key] = "<redacted>"
			} else {
				out[key] = redactJSONValue(child)
			}
		}
		return out
	case []any:
		for i := range typed {
			typed[i] = redactJSONValue(typed[i])
		}
		return typed
	default:
		return value
	}
}

func marshalCompact(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return "<redacted>"
	}
	return string(b)
}

func sensitiveName(name string) bool {
	lower := strings.ToLower(name)
	for _, needle := range []string{"authorization", "cookie", "csrf", "xsrf", "token", "secret", "password", "session", "client-transaction-id"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func networkRequestFromEvent(event cdp.Event) (networkRequest, bool) {
	switch event.Method {
	case "Network.requestWillBeSent":
		var params struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			Request   struct {
				URL    string `json:"url"`
				Method string `json:"method"`
			} `json:"request"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return networkRequest{}, false
		}
		return networkRequest{ID: params.RequestID, URL: params.Request.URL, Method: params.Request.Method, ResourceType: params.Type}, true
	case "Network.responseReceived":
		var params struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			Response  struct {
				URL        string `json:"url"`
				Status     int    `json:"status"`
				StatusText string `json:"statusText"`
				MimeType   string `json:"mimeType"`
			} `json:"response"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return networkRequest{}, false
		}
		return networkRequest{ID: params.RequestID, URL: params.Response.URL, ResourceType: params.Type, Status: params.Response.Status, StatusText: params.Response.StatusText, MimeType: params.Response.MimeType}, true
	case "Network.loadingFailed":
		var params struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			ErrorText string `json:"errorText"`
			Canceled  bool   `json:"canceled"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return networkRequest{}, false
		}
		return networkRequest{ID: params.RequestID, ResourceType: params.Type, Failed: true, ErrorText: params.ErrorText, Canceled: params.Canceled}, true
	case "Network.loadingFinished":
		var params struct {
			RequestID         string  `json:"requestId"`
			EncodedDataLength float64 `json:"encodedDataLength"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return networkRequest{}, false
		}
		return networkRequest{ID: params.RequestID, EncodedDataLength: params.EncodedDataLength}, true
	default:
		return networkRequest{}, false
	}
}

func mergeNetworkRequest(dst *networkRequest, src networkRequest) {
	if src.URL != "" {
		dst.URL = src.URL
	}
	if src.Method != "" {
		dst.Method = src.Method
	}
	if src.ResourceType != "" {
		dst.ResourceType = src.ResourceType
	}
	if src.Status != 0 {
		dst.Status = src.Status
	}
	if src.StatusText != "" {
		dst.StatusText = src.StatusText
	}
	if src.MimeType != "" {
		dst.MimeType = src.MimeType
	}
	if src.Failed {
		dst.Failed = true
	}
	if src.ErrorText != "" {
		dst.ErrorText = src.ErrorText
	}
	if src.Canceled {
		dst.Canceled = true
	}
	if src.EncodedDataLength != 0 {
		dst.EncodedDataLength = src.EncodedDataLength
	}
}

func requestFailed(req networkRequest) bool {
	return req.Failed || req.Status >= 400
}

func networkRequestLines(requests []networkRequest) []string {
	lines := make([]string, 0, len(requests))
	for _, req := range requests {
		status := "pending"
		if req.Failed {
			status = "failed"
		} else if req.Status > 0 {
			status = fmt.Sprint(req.Status)
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", req.ID, status, req.Method, req.URL))
	}
	return lines
}

func (a *app) newStorageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Inspect and mutate browser application storage",
	}
	cmd.AddCommand(a.newStorageListCommand())
	cmd.AddCommand(a.newStorageGetCommand())
	cmd.AddCommand(a.newStorageSetCommand())
	cmd.AddCommand(a.newStorageDeleteCommand())
	cmd.AddCommand(a.newStorageClearCommand())
	cmd.AddCommand(a.newStorageSnapshotCommand())
	cmd.AddCommand(a.newStorageDiffCommand())
	cmd.AddCommand(a.newStorageCookiesCommand())
	return cmd
}

func (a *app) newStorageListCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var include string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List localStorage, sessionStorage, cookies, and quota for a page",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			includeSet, err := parseStorageInclude(include)
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			snapshot, collectorErrors, err := collectStorageSnapshot(ctx, session, target, includeSet)
			if err != nil {
				return err
			}
			report := map[string]any{
				"ok":               true,
				"target":           pageRow(target),
				"storage":          snapshot,
				"collector_errors": collectorErrors,
			}
			human := fmt.Sprintf("storage\tlocal:%d\tsession:%d\tcookies:%d", snapshot.LocalStorage.Count, snapshot.SessionStorage.Count, len(snapshot.Cookies))
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&include, "include", "localStorage,sessionStorage,cookies,quota", "comma-separated storage areas: localStorage,sessionStorage,cookies,quota,all")
	return cmd
}

func (a *app) newStorageGetCommand() *cobra.Command {
	var targetID string
	var urlContains string
	cmd := &cobra.Command{
		Use:   "get <localStorage|sessionStorage> <key>",
		Short: "Read one Web Storage value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := normalizeWebStorageBackend(args[0])
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runWebStorageOperation(ctx, session, "get", backend, args[1], "")
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("%s\t%s\tfound=%t", result.Backend, result.Key, result.Found)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageSetCommand() *cobra.Command {
	var targetID string
	var urlContains string
	cmd := &cobra.Command{
		Use:   "set <localStorage|sessionStorage> <key> <value|@file>",
		Short: "Set one Web Storage value",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := normalizeWebStorageBackend(args[0])
			if err != nil {
				return err
			}
			value, source, err := readStorageValueInput(args[2])
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runWebStorageOperation(ctx, session, "set", backend, args[1], value)
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result, "value_source": source}
			human := fmt.Sprintf("%s\t%s\tset", result.Backend, result.Key)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageDeleteCommand() *cobra.Command {
	var targetID string
	var urlContains string
	cmd := &cobra.Command{
		Use:     "delete <localStorage|sessionStorage> <key>",
		Aliases: []string{"rm"},
		Short:   "Delete one Web Storage value",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := normalizeWebStorageBackend(args[0])
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runWebStorageOperation(ctx, session, "delete", backend, args[1], "")
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("%s\t%s\tdeleted=%t", result.Backend, result.Key, result.Found)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageClearCommand() *cobra.Command {
	var targetID string
	var urlContains string
	cmd := &cobra.Command{
		Use:   "clear <localStorage|sessionStorage>",
		Short: "Clear one Web Storage area",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := normalizeWebStorageBackend(args[0])
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runWebStorageOperation(ctx, session, "clear", backend, "", "")
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("%s\tcleared=%d", result.Backend, result.Cleared)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageSnapshotCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var include string
	var outPath string
	var redact string
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Write a local forensic storage snapshot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			includeSet, err := parseStorageInclude(include)
			if err != nil {
				return err
			}
			redact = strings.ToLower(strings.TrimSpace(redact))
			if redact == "" {
				redact = "none"
			}
			if redact != "none" && redact != "safe" {
				return commandError("usage", "usage", "--redact must be none or safe", ExitUsage, []string{"cdp storage snapshot --redact safe --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			snapshot, collectorErrors, err := collectStorageSnapshot(ctx, session, target, includeSet)
			if err != nil {
				return err
			}
			applyStorageRedaction(&snapshot, redact)
			meta := map[string]any{
				"include":          setKeys(includeSet),
				"redact":           redact,
				"collector_errors": collectorErrors,
			}
			if strings.TrimSpace(outPath) != "" && redact == "none" {
				meta["local_artifact_warning"] = "storage snapshot may include cookies, tokens, localStorage values, and sessionStorage values; keep this artifact local"
			}
			report := map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"snapshot": snapshot,
				"storage":  meta,
			}
			if strings.TrimSpace(outPath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal storage snapshot: %v", err), ExitInternal, []string{"cdp storage snapshot --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "storage-snapshot", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "storage-snapshot", "path": writtenPath}}
			}
			human := fmt.Sprintf("storage-snapshot\tlocal:%d\tsession:%d\tcookies:%d", snapshot.LocalStorage.Count, snapshot.SessionStorage.Count, len(snapshot.Cookies))
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&include, "include", "localStorage,sessionStorage,cookies,quota", "comma-separated storage areas: localStorage,sessionStorage,cookies,quota,all")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the JSON storage snapshot artifact")
	cmd.Flags().StringVar(&redact, "redact", "none", "redaction preset for output and artifacts: none or safe")
	return cmd
}

func (a *app) newStorageDiffCommand() *cobra.Command {
	var leftPath string
	var rightPath string
	cmd := &cobra.Command{
		Use:   "diff --left before.json --right after.json",
		Short: "Diff two storage snapshot artifacts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(leftPath) == "" || strings.TrimSpace(rightPath) == "" {
				return commandError("usage", "usage", "--left and --right are required", ExitUsage, []string{"cdp storage diff --left before.local.json --right after.local.json --json"})
			}
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			left, err := readStorageSnapshotFile(leftPath)
			if err != nil {
				return commandError("usage", "usage", fmt.Sprintf("read --left snapshot: %v", err), ExitUsage, []string{"cdp storage snapshot --out before.local.json --json"})
			}
			right, err := readStorageSnapshotFile(rightPath)
			if err != nil {
				return commandError("usage", "usage", fmt.Sprintf("read --right snapshot: %v", err), ExitUsage, []string{"cdp storage snapshot --out after.local.json --json"})
			}
			diff := diffStorageSnapshots(left, right)
			report := map[string]any{
				"ok":       true,
				"left":     leftPath,
				"right":    rightPath,
				"diff":     diff,
				"has_diff": storageDiffHasChanges(diff),
			}
			human := fmt.Sprintf("storage-diff\tchanged=%t", storageDiffHasChanges(diff))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&leftPath, "left", "", "left/before storage snapshot JSON path")
	cmd.Flags().StringVar(&rightPath, "right", "", "right/after storage snapshot JSON path")
	return cmd
}

func (a *app) newStorageCookiesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cookies",
		Short: "List, set, and delete cookies",
	}
	cmd.AddCommand(a.newStorageCookiesListCommand())
	cmd.AddCommand(a.newStorageCookiesSetCommand())
	cmd.AddCommand(a.newStorageCookiesDeleteCommand())
	return cmd
}

func (a *app) newStorageCookiesListCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var rawURL string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cookies for a URL or selected page",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			cookieURL, err := storageCommandURL(ctx, session, target, rawURL)
			if err != nil {
				return err
			}
			cookies, err := getStorageCookies(ctx, session, cookieURL)
			if err != nil {
				return storageCommandFailed("list cookies", target.TargetID, err)
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "url": cookieURL, "cookies": cookies, "storage": map[string]any{"count": len(cookies), "names": cookieNames(cookies)}}
			human := fmt.Sprintf("cookies\t%d", len(cookies))
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&rawURL, "url", "", "URL whose applicable cookies should be listed; defaults to selected page URL")
	return cmd
}

func (a *app) newStorageCookiesSetCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var rawURL string
	var name string
	var value string
	var domain string
	var path string
	var secure bool
	var httpOnly bool
	var sameSite string
	var expires float64
	cmd := &cobra.Command{
		Use:   "set --name <name> --value <value>",
		Short: "Set one cookie",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(name) == "" {
				return commandError("usage", "usage", "--name is required", ExitUsage, []string{"cdp storage cookies set --name feature_flag --value enabled --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			cookieURL, err := storageCommandURL(ctx, session, target, rawURL)
			if err != nil {
				return err
			}
			params := map[string]any{"name": name, "value": value, "url": cookieURL}
			if strings.TrimSpace(domain) != "" {
				params["domain"] = domain
			}
			if strings.TrimSpace(path) != "" {
				params["path"] = path
			}
			if secure {
				params["secure"] = true
			}
			if httpOnly {
				params["httpOnly"] = true
			}
			if strings.TrimSpace(sameSite) != "" {
				params["sameSite"] = sameSite
			}
			if expires > 0 {
				params["expires"] = expires
			}
			var result map[string]any
			if err := execSessionJSON(ctx, session, "Network.setCookie", params, &result); err != nil {
				return storageCommandFailed("set cookie", target.TargetID, err)
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "url": cookieURL, "cookie": map[string]any{"name": name, "domain": domain, "path": path}, "result": result}
			human := fmt.Sprintf("cookie\t%s\tset", name)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&rawURL, "url", "", "URL to associate with the cookie; defaults to selected page URL")
	cmd.Flags().StringVar(&name, "name", "", "cookie name")
	cmd.Flags().StringVar(&value, "value", "", "cookie value")
	cmd.Flags().StringVar(&domain, "domain", "", "cookie domain")
	cmd.Flags().StringVar(&path, "path", "", "cookie path")
	cmd.Flags().BoolVar(&secure, "secure", false, "mark the cookie secure")
	cmd.Flags().BoolVar(&httpOnly, "http-only", false, "mark the cookie HTTP-only")
	cmd.Flags().StringVar(&sameSite, "same-site", "", "cookie SameSite value: Strict, Lax, or None")
	cmd.Flags().Float64Var(&expires, "expires", 0, "cookie expiration as seconds since Unix epoch; 0 creates a session cookie")
	return cmd
}

func (a *app) newStorageCookiesDeleteCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var rawURL string
	var name string
	var domain string
	var path string
	cmd := &cobra.Command{
		Use:     "delete --name <name>",
		Aliases: []string{"rm"},
		Short:   "Delete matching cookies",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(name) == "" {
				return commandError("usage", "usage", "--name is required", ExitUsage, []string{"cdp storage cookies delete --name feature_flag --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			cookieURL, err := storageCommandURL(ctx, session, target, rawURL)
			if err != nil {
				return err
			}
			params := map[string]any{"name": name}
			if strings.TrimSpace(domain) != "" || strings.TrimSpace(path) != "" {
				if strings.TrimSpace(domain) != "" {
					params["domain"] = domain
				}
				if strings.TrimSpace(path) != "" {
					params["path"] = path
				}
			} else {
				params["url"] = cookieURL
			}
			if err := execSessionJSON(ctx, session, "Network.deleteCookies", params, nil); err != nil {
				return storageCommandFailed("delete cookie", target.TargetID, err)
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "url": cookieURL, "cookie": map[string]any{"name": name, "domain": domain, "path": path}}
			human := fmt.Sprintf("cookie\t%s\tdeleted", name)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&rawURL, "url", "", "URL whose matching cookie should be deleted; defaults to selected page URL")
	cmd.Flags().StringVar(&name, "name", "", "cookie name")
	cmd.Flags().StringVar(&domain, "domain", "", "cookie domain")
	cmd.Flags().StringVar(&path, "path", "", "cookie path")
	return cmd
}

type storageSnapshot struct {
	URL            string              `json:"url,omitempty"`
	Origin         string              `json:"origin,omitempty"`
	LocalStorage   storageAreaSnapshot `json:"local_storage"`
	SessionStorage storageAreaSnapshot `json:"session_storage"`
	Cookies        []map[string]any    `json:"cookies,omitempty"`
	Quota          map[string]any      `json:"quota,omitempty"`
}

type storageAreaSnapshot struct {
	Count   int            `json:"count"`
	Keys    []string       `json:"keys"`
	Entries []storageEntry `json:"entries"`
	Error   string         `json:"error,omitempty"`
}

type storageEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Bytes int    `json:"bytes"`
}

type webStorageBackend struct {
	JSName string
	Output string
}

type webStorageOperationResult struct {
	URL      string `json:"url,omitempty"`
	Origin   string `json:"origin,omitempty"`
	Backend  string `json:"backend"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	Found    bool   `json:"found,omitempty"`
	Bytes    int    `json:"bytes,omitempty"`
	Cleared  int    `json:"cleared,omitempty"`
	Previous string `json:"previous,omitempty"`
}

type storageDiffReport struct {
	LocalStorage   storageAreaDiff `json:"local_storage"`
	SessionStorage storageAreaDiff `json:"session_storage"`
	Cookies        storageAreaDiff `json:"cookies"`
	Summary        map[string]int  `json:"summary"`
}

type storageAreaDiff struct {
	Added   []storageDiffItem `json:"added"`
	Removed []storageDiffItem `json:"removed"`
	Changed []storageDiffItem `json:"changed"`
}

type storageDiffItem struct {
	Key    string `json:"key"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

func addStorageTargetFlags(cmd *cobra.Command, targetID, urlContains *string) {
	cmd.Flags().StringVar(targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(urlContains, "url-contains", "", "use the first page whose URL contains this text")
}

func parseStorageInclude(value string) (map[string]bool, error) {
	set := parseCSVSet(value)
	if len(set) == 0 || set["all"] {
		return defaultStorageIncludeSet(), nil
	}
	out := map[string]bool{}
	for key := range set {
		switch strings.ToLower(key) {
		case "localstorage", "local", "local_storage":
			out["localStorage"] = true
		case "sessionstorage", "session", "session_storage":
			out["sessionStorage"] = true
		case "cookies", "cookie":
			out["cookies"] = true
		case "quota", "usage":
			out["quota"] = true
		default:
			return nil, commandError("usage", "usage", fmt.Sprintf("unknown storage include %q", key), ExitUsage, []string{"cdp storage list --include localStorage,sessionStorage,cookies --json"})
		}
	}
	return out, nil
}

func defaultStorageIncludeSet() map[string]bool {
	return map[string]bool{"localStorage": true, "sessionStorage": true, "cookies": true, "quota": true}
}

func normalizeWebStorageBackend(value string) (webStorageBackend, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "localstorage", "local", "local_storage":
		return webStorageBackend{JSName: "localStorage", Output: "localStorage"}, nil
	case "sessionstorage", "session", "session_storage":
		return webStorageBackend{JSName: "sessionStorage", Output: "sessionStorage"}, nil
	default:
		return webStorageBackend{}, commandError("usage", "usage", "backend must be localStorage or sessionStorage", ExitUsage, []string{"cdp storage get localStorage feature --json"})
	}
}

func collectStorageSnapshot(ctx context.Context, session *cdp.PageSession, target cdp.TargetInfo, includeSet map[string]bool) (storageSnapshot, []map[string]string, error) {
	collectorErrors := []map[string]string{}
	snapshot, err := collectWebStorageSnapshot(ctx, session)
	if err != nil {
		return storageSnapshot{}, nil, err
	}
	if snapshot.URL == "" {
		snapshot.URL = target.URL
	}
	if snapshot.Origin == "" {
		snapshot.Origin = originForURL(snapshot.URL)
	}
	if !includeSet["localStorage"] {
		snapshot.LocalStorage = storageAreaSnapshot{}
	}
	if !includeSet["sessionStorage"] {
		snapshot.SessionStorage = storageAreaSnapshot{}
	}
	if includeSet["cookies"] {
		cookies, err := getStorageCookies(ctx, session, snapshot.URL)
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("cookies", err))
		} else {
			snapshot.Cookies = cookies
		}
	}
	if includeSet["quota"] && snapshot.Origin != "" {
		quota, err := getStorageQuota(ctx, session, snapshot.Origin)
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("quota", err))
		} else {
			snapshot.Quota = quota
		}
	}
	return snapshot, collectorErrors, nil
}

func collectWebStorageSnapshot(ctx context.Context, session *cdp.PageSession) (storageSnapshot, error) {
	result, err := session.Evaluate(ctx, storageSnapshotExpression(), false)
	if err != nil {
		return storageSnapshot{}, storageCommandFailed("inspect storage", session.TargetID, err)
	}
	if result.Exception != nil {
		return storageSnapshot{}, commandError("javascript_exception", "runtime", fmt.Sprintf("storage javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp storage list --json"})
	}
	var snapshot storageSnapshot
	if err := json.Unmarshal(result.Object.Value, &snapshot); err != nil {
		return storageSnapshot{}, commandError("invalid_storage_result", "runtime", fmt.Sprintf("decode storage result: %v", err), ExitCheckFailed, []string{"cdp storage list --json"})
	}
	return snapshot, nil
}

func runWebStorageOperation(ctx context.Context, session *cdp.PageSession, op string, backend webStorageBackend, key, value string) (webStorageOperationResult, error) {
	result, err := session.Evaluate(ctx, webStorageOperationExpression(op, backend.JSName, key, value), false)
	if err != nil {
		return webStorageOperationResult{}, storageCommandFailed(op+" storage", session.TargetID, err)
	}
	if result.Exception != nil {
		return webStorageOperationResult{}, commandError("javascript_exception", "runtime", fmt.Sprintf("storage javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp storage list --json"})
	}
	var opResult webStorageOperationResult
	if err := json.Unmarshal(result.Object.Value, &opResult); err != nil {
		return webStorageOperationResult{}, commandError("invalid_storage_result", "runtime", fmt.Sprintf("decode storage operation result: %v", err), ExitCheckFailed, []string{"cdp storage get localStorage feature --json"})
	}
	opResult.Backend = backend.Output
	return opResult, nil
}

func getStorageCookies(ctx context.Context, session *cdp.PageSession, rawURL string) ([]map[string]any, error) {
	var result struct {
		Cookies []map[string]any `json:"cookies"`
	}
	params := map[string]any{}
	if strings.TrimSpace(rawURL) != "" {
		params["urls"] = []string{rawURL}
	}
	if err := execSessionJSON(ctx, session, "Network.getCookies", params, &result); err != nil {
		return nil, err
	}
	if result.Cookies == nil {
		return []map[string]any{}, nil
	}
	return result.Cookies, nil
}

func getStorageQuota(ctx context.Context, session *cdp.PageSession, origin string) (map[string]any, error) {
	var quota map[string]any
	if err := execSessionJSON(ctx, session, "Storage.getUsageAndQuota", map[string]any{"origin": origin}, &quota); err != nil {
		return nil, err
	}
	return quota, nil
}

func execSessionJSON(ctx context.Context, session *cdp.PageSession, method string, params any, out any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	raw, err := session.Exec(ctx, method, b)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	return nil
}

func storageCommandURL(ctx context.Context, session *cdp.PageSession, target cdp.TargetInfo, rawURL string) (string, error) {
	if strings.TrimSpace(rawURL) != "" {
		return rawURL, nil
	}
	info, err := collectStoragePageInfo(ctx, session)
	if err == nil && info.URL != "" {
		return info.URL, nil
	}
	if strings.TrimSpace(target.URL) != "" {
		return target.URL, nil
	}
	return "", commandError("usage", "usage", "--url is required when the selected page URL is unavailable", ExitUsage, []string{"cdp storage cookies list --url https://example.com --json"})
}

func collectStoragePageInfo(ctx context.Context, session *cdp.PageSession) (storageSnapshot, error) {
	result, err := session.Evaluate(ctx, storagePageInfoExpression(), false)
	if err != nil {
		return storageSnapshot{}, err
	}
	var info storageSnapshot
	if result.Exception != nil {
		return storageSnapshot{}, fmt.Errorf("javascript exception: %s", result.Exception.Text)
	}
	if err := json.Unmarshal(result.Object.Value, &info); err != nil {
		return storageSnapshot{}, err
	}
	return info, nil
}

func storageSnapshotExpression() string {
	return `(() => {
  "__cdp_cli_storage_snapshot__";
  const bytes = (value) => new TextEncoder().encode(String(value ?? "")).length;
  const readArea = (name) => {
    try {
      const store = window[name];
      const entries = [];
      for (let i = 0; i < store.length; i++) {
        const key = store.key(i);
        const value = store.getItem(key);
        entries.push({key, value, bytes: bytes(value)});
      }
      entries.sort((a, b) => a.key.localeCompare(b.key));
      return {count: entries.length, keys: entries.map((entry) => entry.key), entries};
    } catch (error) {
      return {count: 0, keys: [], entries: [], error: String(error && error.message || error)};
    }
  };
  return {
    url: location.href,
    origin: location.origin,
    local_storage: readArea("localStorage"),
    session_storage: readArea("sessionStorage")
  };
})()`
}

func storagePageInfoExpression() string {
	return `(() => {
  "__cdp_cli_storage_page_info__";
  return {url: location.href, origin: location.origin};
})()`
}

func webStorageOperationExpression(op, area, key, value string) string {
	return fmt.Sprintf(`(() => {
  "__cdp_cli_storage_%s__";
  const store = window[%s];
  const key = %s;
  const value = %s;
  const bytes = (input) => new TextEncoder().encode(String(input ?? "")).length;
  if (%q === "get") {
    const current = store.getItem(key);
    return {url: location.href, origin: location.origin, backend: %s, key, found: current !== null, value: current ?? "", bytes: current === null ? 0 : bytes(current)};
  }
  if (%q === "set") {
    const previous = store.getItem(key);
    store.setItem(key, value);
    const current = store.getItem(key);
    return {url: location.href, origin: location.origin, backend: %s, key, found: true, value: current ?? "", previous: previous ?? "", bytes: bytes(current)};
  }
  if (%q === "delete") {
    const previous = store.getItem(key);
    store.removeItem(key);
    return {url: location.href, origin: location.origin, backend: %s, key, found: previous !== null, previous: previous ?? ""};
  }
  if (%q === "clear") {
    const cleared = store.length;
    store.clear();
    return {url: location.href, origin: location.origin, backend: %s, cleared};
  }
  throw new Error("unsupported storage operation");
})()`, op, jsStringLiteral(area), jsStringLiteral(key), jsStringLiteral(value), op, jsStringLiteral(area), op, jsStringLiteral(area), op, jsStringLiteral(area), op, jsStringLiteral(area))
}

func jsStringLiteral(value string) string {
	b, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(b)
}

func readStorageValueInput(input string) (string, string, error) {
	if strings.HasPrefix(input, "@") {
		path := strings.TrimPrefix(input, "@")
		if strings.TrimSpace(path) == "" {
			return "", "", commandError("usage", "usage", "@file value input requires a path", ExitUsage, []string{"cdp storage set localStorage key @tmp/value.json --json"})
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", "", commandError("usage", "usage", fmt.Sprintf("read value file: %v", err), ExitUsage, []string{"cdp storage set localStorage key @tmp/value.json --json"})
		}
		return string(b), "file", nil
	}
	return input, "inline", nil
}

func originForURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func applyStorageRedaction(snapshot *storageSnapshot, redact string) {
	if redact == "" || redact == "none" {
		return
	}
	redactStorageArea(&snapshot.LocalStorage, redact)
	redactStorageArea(&snapshot.SessionStorage, redact)
	redactStorageCookies(snapshot.Cookies, redact)
}

func redactStorageArea(area *storageAreaSnapshot, redact string) {
	for i := range area.Entries {
		if sensitiveName(area.Entries[i].Key) {
			area.Entries[i].Value = "<redacted>"
			continue
		}
		area.Entries[i].Value = redactBodyText(area.Entries[i].Value, redact)
	}
}

func redactStorageCookies(cookies []map[string]any, redact string) {
	for _, cookie := range cookies {
		name, _ := cookie["name"].(string)
		value, _ := cookie["value"].(string)
		if sensitiveName(name) || sensitiveHeaderValue(value) {
			cookie["value"] = "<redacted>"
		} else if value != "" {
			cookie["value"] = redactBodyText(value, redact)
		}
	}
}

func cookieNames(cookies []map[string]any) []string {
	names := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if name, ok := cookie["name"].(string); ok && name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func readStorageSnapshotFile(path string) (storageSnapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return storageSnapshot{}, err
	}
	var envelope struct {
		Snapshot storageSnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(b, &envelope); err == nil && storageSnapshotHasData(envelope.Snapshot) {
		return envelope.Snapshot, nil
	}
	var snapshot storageSnapshot
	if err := json.Unmarshal(b, &snapshot); err != nil {
		return storageSnapshot{}, err
	}
	if !storageSnapshotHasData(snapshot) {
		return storageSnapshot{}, fmt.Errorf("file does not contain a storage snapshot")
	}
	return snapshot, nil
}

func storageSnapshotHasData(snapshot storageSnapshot) bool {
	return snapshot.URL != "" || snapshot.Origin != "" || len(snapshot.LocalStorage.Entries) > 0 || len(snapshot.SessionStorage.Entries) > 0 || len(snapshot.Cookies) > 0
}

func diffStorageSnapshots(left, right storageSnapshot) storageDiffReport {
	local := diffStringMaps(storageEntryValues(left.LocalStorage), storageEntryValues(right.LocalStorage))
	session := diffStringMaps(storageEntryValues(left.SessionStorage), storageEntryValues(right.SessionStorage))
	cookies := diffStringMaps(cookieValues(left.Cookies), cookieValues(right.Cookies))
	summary := map[string]int{
		"added":   len(local.Added) + len(session.Added) + len(cookies.Added),
		"removed": len(local.Removed) + len(session.Removed) + len(cookies.Removed),
		"changed": len(local.Changed) + len(session.Changed) + len(cookies.Changed),
	}
	return storageDiffReport{LocalStorage: local, SessionStorage: session, Cookies: cookies, Summary: summary}
}

func storageEntryValues(area storageAreaSnapshot) map[string]string {
	values := map[string]string{}
	for _, entry := range area.Entries {
		values[entry.Key] = entry.Value
	}
	return values
}

func cookieValues(cookies []map[string]any) map[string]string {
	values := map[string]string{}
	for _, cookie := range cookies {
		key := cookieIdentity(cookie)
		if key == "" {
			continue
		}
		b, _ := json.Marshal(cookie)
		values[key] = string(b)
	}
	return values
}

func cookieIdentity(cookie map[string]any) string {
	name, _ := cookie["name"].(string)
	domain, _ := cookie["domain"].(string)
	path, _ := cookie["path"].(string)
	if name == "" {
		return ""
	}
	return name + "|" + domain + "|" + path
}

func diffStringMaps(left, right map[string]string) storageAreaDiff {
	diff := storageAreaDiff{Added: []storageDiffItem{}, Removed: []storageDiffItem{}, Changed: []storageDiffItem{}}
	keys := map[string]bool{}
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		leftValue, leftOK := left[key]
		rightValue, rightOK := right[key]
		switch {
		case !leftOK && rightOK:
			diff.Added = append(diff.Added, storageDiffItem{Key: key, After: rightValue})
		case leftOK && !rightOK:
			diff.Removed = append(diff.Removed, storageDiffItem{Key: key, Before: leftValue})
		case leftOK && rightOK && leftValue != rightValue:
			diff.Changed = append(diff.Changed, storageDiffItem{Key: key, Before: leftValue, After: rightValue})
		}
	}
	return diff
}

func storageDiffHasChanges(diff storageDiffReport) bool {
	return diff.Summary["added"] > 0 || diff.Summary["removed"] > 0 || diff.Summary["changed"] > 0
}

func storageCommandFailed(action, targetID string, err error) error {
	return commandError(
		"connection_failed",
		"connection",
		fmt.Sprintf("%s target %s: %v", action, targetID, err),
		ExitConnection,
		[]string{"cdp pages --json", "cdp doctor --json"},
	)
}

type consoleMessage struct {
	ID               int                 `json:"id"`
	Source           string              `json:"source"`
	Type             string              `json:"type,omitempty"`
	Level            string              `json:"level,omitempty"`
	Text             string              `json:"text"`
	Timestamp        float64             `json:"timestamp,omitempty"`
	URL              string              `json:"url,omitempty"`
	LineNumber       int                 `json:"line_number,omitempty"`
	NetworkRequestID string              `json:"network_request_id,omitempty"`
	Args             []consoleMessageArg `json:"args,omitempty"`
	StackTrace       json.RawMessage     `json:"stack_trace,omitempty"`
}

type consoleMessageArg struct {
	Type        string          `json:"type"`
	Subtype     string          `json:"subtype,omitempty"`
	Description string          `json:"description,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
}

func collectConsoleMessages(ctx context.Context, client browserEventClient, sessionID string, wait time.Duration, limit int, errorsOnly bool, typeSet map[string]bool) ([]consoleMessage, bool, error) {
	if err := client.CallSession(ctx, sessionID, "Runtime.enable", map[string]any{}, nil); err != nil {
		return nil, false, err
	}
	if err := client.CallSession(ctx, sessionID, "Log.enable", map[string]any{}, nil); err != nil {
		return nil, false, err
	}

	var messages []consoleMessage
	truncated := false
	addEventMessages := func(events []cdp.Event) {
		for _, event := range events {
			if event.SessionID != "" && event.SessionID != sessionID {
				continue
			}
			msg, ok := consoleMessageFromEvent(event)
			if !ok || !keepConsoleMessage(msg, errorsOnly, typeSet) {
				continue
			}
			if limit > 0 && len(messages) >= limit {
				truncated = true
				continue
			}
			msg.ID = len(messages)
			messages = append(messages, msg)
		}
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return nil, false, err
	}
	addEventMessages(events)

	if wait > 0 {
		eventCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for {
			event, err := client.ReadEvent(eventCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded) {
					break
				}
				return nil, false, err
			}
			addEventMessages([]cdp.Event{event})
		}
	}

	return messages, truncated, nil
}

func consoleMessageFromEvent(event cdp.Event) (consoleMessage, bool) {
	switch event.Method {
	case "Runtime.consoleAPICalled":
		var params struct {
			Type       string              `json:"type"`
			Args       []consoleMessageArg `json:"args"`
			Timestamp  float64             `json:"timestamp"`
			StackTrace json.RawMessage     `json:"stackTrace"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return consoleMessage{}, false
		}
		return consoleMessage{
			Source:     "runtime",
			Type:       params.Type,
			Level:      runtimeConsoleLevel(params.Type),
			Text:       consoleArgsText(params.Args),
			Timestamp:  params.Timestamp,
			Args:       params.Args,
			StackTrace: params.StackTrace,
		}, true
	case "Runtime.exceptionThrown":
		var params struct {
			Timestamp        float64 `json:"timestamp"`
			ExceptionDetails struct {
				Text       string            `json:"text"`
				URL        string            `json:"url"`
				LineNumber int               `json:"lineNumber"`
				StackTrace json.RawMessage   `json:"stackTrace"`
				Exception  consoleMessageArg `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return consoleMessage{}, false
		}
		text := params.ExceptionDetails.Text
		if text == "" {
			text = consoleArgText(params.ExceptionDetails.Exception)
		}
		return consoleMessage{
			Source:     "runtime",
			Type:       "exception",
			Level:      "error",
			Text:       text,
			Timestamp:  params.Timestamp,
			URL:        params.ExceptionDetails.URL,
			LineNumber: params.ExceptionDetails.LineNumber,
			StackTrace: params.ExceptionDetails.StackTrace,
		}, true
	case "Log.entryAdded":
		var params struct {
			Entry struct {
				Source           string              `json:"source"`
				Level            string              `json:"level"`
				Text             string              `json:"text"`
				Timestamp        float64             `json:"timestamp"`
				URL              string              `json:"url"`
				LineNumber       int                 `json:"lineNumber"`
				NetworkRequestID string              `json:"networkRequestId"`
				Args             []consoleMessageArg `json:"args"`
				StackTrace       json.RawMessage     `json:"stackTrace"`
			} `json:"entry"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return consoleMessage{}, false
		}
		text := params.Entry.Text
		if text == "" {
			text = consoleArgsText(params.Entry.Args)
		}
		return consoleMessage{
			Source:           params.Entry.Source,
			Level:            params.Entry.Level,
			Text:             text,
			Timestamp:        params.Entry.Timestamp,
			URL:              params.Entry.URL,
			LineNumber:       params.Entry.LineNumber,
			NetworkRequestID: params.Entry.NetworkRequestID,
			Args:             params.Entry.Args,
			StackTrace:       params.Entry.StackTrace,
		}, true
	default:
		return consoleMessage{}, false
	}
}

func runtimeConsoleLevel(consoleType string) string {
	switch consoleType {
	case "error", "assert":
		return "error"
	case "warning":
		return "warning"
	case "debug":
		return "verbose"
	default:
		return "info"
	}
}

func consoleArgsText(args []consoleMessageArg) string {
	texts := make([]string, 0, len(args))
	for _, arg := range args {
		if text := consoleArgText(arg); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, " ")
}

func consoleArgText(arg consoleMessageArg) string {
	if len(arg.Value) > 0 {
		var value any
		if err := json.Unmarshal(arg.Value, &value); err == nil {
			return fmt.Sprint(value)
		}
		return string(arg.Value)
	}
	if arg.Description != "" {
		return arg.Description
	}
	return arg.Type
}

func keepConsoleMessage(msg consoleMessage, errorsOnly bool, typeSet map[string]bool) bool {
	if errorsOnly && msg.Level != "error" && msg.Level != "warning" && msg.Type != "exception" && msg.Type != "assert" {
		return false
	}
	if len(typeSet) == 0 {
		return true
	}
	return typeSet[strings.ToLower(msg.Type)] || typeSet[strings.ToLower(msg.Level)] || typeSet[strings.ToLower(msg.Source)]
}

func consoleMessageLines(messages []consoleMessage) []string {
	lines := make([]string, 0, len(messages))
	for _, msg := range messages {
		label := msg.Level
		if label == "" {
			label = msg.Type
		}
		if label == "" {
			label = msg.Source
		}
		lines = append(lines, fmt.Sprintf("%d\t%s\t%s", msg.ID, label, msg.Text))
	}
	return lines
}

func parseCSVSet(value string) map[string]bool {
	set := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			set[part] = true
		}
	}
	return set
}

func setKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (a *app) newCDPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "protocol",
		Aliases: []string{"cdp"},
		Short:   "Discover and execute raw CDP methods",
	}
	cmd.AddCommand(a.newProtocolMetadataCommand())
	cmd.AddCommand(a.newProtocolDomainsCommand())
	cmd.AddCommand(a.newProtocolSearchCommand())
	cmd.AddCommand(a.newProtocolDescribeCommand())
	cmd.AddCommand(a.newProtocolExamplesCommand())
	cmd.AddCommand(a.newProtocolExecCommand())
	return cmd
}

func (a *app) newProtocolMetadataCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "metadata",
		Short: "Print CDP protocol metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			domains := cdp.SummarizeDomains(protocol.Domains)
			data := map[string]any{
				"ok": true,
				"protocol": map[string]any{
					"version":      protocol.Version,
					"domain_count": len(domains),
					"domains":      domains,
					"source":       protocol.Source,
				},
			}
			human := fmt.Sprintf("CDP %s.%s, %d domains", protocol.Version.Major, protocol.Version.Minor, len(domains))
			return a.render(ctx, human, data)
		},
	}
}

func (a *app) newProtocolDomainsCommand() *cobra.Command {
	var experimentalOnly bool
	var deprecatedOnly bool
	cmd := &cobra.Command{
		Use:   "domains",
		Short: "List CDP domains",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			domains := cdp.SummarizeDomains(protocol.Domains)
			domains = filterDomainSummaries(domains, experimentalOnly, deprecatedOnly)
			var lines []string
			for _, domain := range domains {
				lines = append(lines, fmt.Sprintf("%s\tcommands=%d\tevents=%d", domain.Name, domain.CommandCount, domain.EventCount))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":           true,
				"domain_count": len(domains),
				"domains":      domains,
				"source":       protocol.Source,
			})
		},
	}
	cmd.Flags().BoolVar(&experimentalOnly, "experimental", false, "only return experimental domains")
	cmd.Flags().BoolVar(&deprecatedOnly, "deprecated", false, "only return deprecated domains")
	return cmd
}

func filterDomainSummaries(domains []cdp.DomainSummary, experimentalOnly, deprecatedOnly bool) []cdp.DomainSummary {
	if !experimentalOnly && !deprecatedOnly {
		return domains
	}
	filtered := make([]cdp.DomainSummary, 0, len(domains))
	for _, domain := range domains {
		if experimentalOnly && !domain.Experimental {
			continue
		}
		if deprecatedOnly && !domain.Deprecated {
			continue
		}
		filtered = append(filtered, domain)
	}
	return filtered
}

func (a *app) newProtocolSearchCommand() *cobra.Command {
	var limit int
	var kind string
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search CDP domains, methods, events, and types",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			results := cdp.SearchProtocol(protocol, args[0], limit)
			results = cdp.FilterSearchResultsByKind(results, kind)
			var lines []string
			for _, result := range results {
				lines = append(lines, fmt.Sprintf("%s\t%s", result.Kind, result.Path))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":      true,
				"query":   args[0],
				"matches": results,
				"source":  protocol.Source,
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 25, "maximum number of search results")
	cmd.Flags().StringVar(&kind, "kind", "", "only return matches of this kind: domain, command, event, or type")
	return cmd
}

func (a *app) newProtocolDescribeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <Domain.entity>",
		Short: "Describe a CDP domain, command, event, or type schema",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			desc, ok := cdp.DescribeEntity(protocol, args[0])
			if !ok {
				return commandError(
					"unknown_protocol_entity",
					"usage",
					fmt.Sprintf("unknown protocol entity %q", args[0]),
					ExitUsage,
					[]string{"cdp protocol search <query> --json", "cdp protocol domains --json"},
				)
			}
			human := fmt.Sprintf("%s\t%s", desc.Kind, desc.Path)
			return a.render(ctx, human, map[string]any{
				"ok":     true,
				"entity": desc,
				"source": protocol.Source,
			})
		},
	}
}

func (a *app) newProtocolExamplesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "examples <Domain.method>",
		Short: "Generate example cdp protocol exec commands",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			desc, ok := cdp.DescribeEntity(protocol, args[0])
			if !ok || desc.Kind != "command" {
				return commandError(
					"unknown_protocol_entity",
					"usage",
					fmt.Sprintf("unknown protocol command %q", args[0]),
					ExitUsage,
					[]string{"cdp protocol search <query> --kind command --json", "cdp protocol domains --json"},
				)
			}
			examples := protocolExecExamples(desc)
			lines := make([]string, 0, len(examples))
			for _, example := range examples {
				lines = append(lines, example["command"])
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"entity":   desc,
				"examples": examples,
				"source":   protocol.Source,
			})
		},
	}
}

func protocolExecExamples(desc cdp.EntityDescription) []map[string]string {
	params := sampleProtocolParams(desc.Schema)
	paramsJSON, _ := json.Marshal(params)
	scope := protocolCommandScope(desc.Domain)
	command := fmt.Sprintf("cdp protocol exec %s --params '%s' --json", desc.Path, paramsJSON)
	if scope == "target" {
		command = fmt.Sprintf("cdp protocol exec %s --target <target-id> --params '%s' --json", desc.Path, paramsJSON)
	}
	return []map[string]string{{
		"scope":   scope,
		"command": command,
		"params":  string(paramsJSON),
	}}
}

func protocolCommandScope(domain string) string {
	switch domain {
	case "Browser", "Target", "Schema", "SystemInfo":
		return "browser"
	default:
		return "target"
	}
}

func sampleProtocolParams(schema json.RawMessage) map[string]any {
	var command struct {
		Parameters []struct {
			Name     string   `json:"name"`
			Type     string   `json:"type"`
			Ref      string   `json:"$ref"`
			Optional bool     `json:"optional"`
			Enum     []string `json:"enum"`
		} `json:"parameters"`
	}
	if len(schema) == 0 || json.Unmarshal(schema, &command) != nil {
		return map[string]any{}
	}
	params := map[string]any{}
	for _, param := range command.Parameters {
		if param.Optional {
			continue
		}
		params[param.Name] = sampleProtocolValue(param.Type, param.Ref, param.Enum)
	}
	return params
}

func sampleProtocolValue(paramType, ref string, enum []string) any {
	if len(enum) > 0 {
		return enum[0]
	}
	if ref != "" {
		return "<" + ref + ">"
	}
	switch paramType {
	case "boolean":
		return true
	case "integer", "number":
		return 0
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	default:
		return "<string>"
	}
}

func (a *app) newProtocolExecCommand() *cobra.Command {
	var params string
	var targetID string
	var urlContains string
	var savePath string
	cmd := &cobra.Command{
		Use:   "exec <Domain.method>",
		Short: "Execute a raw browser-scoped or target-scoped CDP method",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			rawParams := json.RawMessage(params)
			if len(rawParams) == 0 {
				rawParams = json.RawMessage(`{}`)
			}
			if !json.Valid(rawParams) {
				return commandError(
					"invalid_json",
					"usage",
					"--params must be valid JSON",
					ExitUsage,
					[]string{"cdp protocol exec Browser.getVersion --params '{}' --json"},
				)
			}
			if targetID != "" || urlContains != "" {
				session, target, err := a.attachPageSession(ctx, targetID, urlContains)
				if err != nil {
					return err
				}
				defer session.Close(ctx)

				result, err := session.Exec(ctx, args[0], rawParams)
				if err != nil {
					return commandError(
						"connection_failed",
						"connection",
						fmt.Sprintf("execute %s in target %s: %v", args[0], target.TargetID, err),
						ExitConnection,
						[]string{"cdp pages --json", "cdp protocol describe " + args[0] + " --json"},
					)
				}
				data := map[string]any{
					"ok":         true,
					"scope":      "target",
					"method":     args[0],
					"target":     pageRow(target),
					"session_id": session.SessionID,
					"result":     result,
				}
				if strings.TrimSpace(savePath) != "" {
					artifact, redactedResult, err := saveProtocolExecArtifact(savePath, result)
					if err != nil {
						return err
					}
					data["result"] = redactedResult
					data["artifact"] = artifact
					data["artifacts"] = []map[string]any{artifact}
				}
				return a.render(ctx, fmt.Sprintf("%s ok", args[0]), data)
			}
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			defer closeClient(ctx)

			result, err := cdp.ExecWithClient(ctx, client, args[0], rawParams)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("execute %s: %v", args[0], err),
					ExitConnection,
					[]string{"cdp doctor --json", "cdp protocol describe " + args[0] + " --json"},
				)
			}
			data := map[string]any{
				"ok":     true,
				"scope":  "browser",
				"method": args[0],
				"result": result,
			}
			if strings.TrimSpace(savePath) != "" {
				artifact, redactedResult, err := saveProtocolExecArtifact(savePath, result)
				if err != nil {
					return err
				}
				data["result"] = redactedResult
				data["artifact"] = artifact
				data["artifacts"] = []map[string]any{artifact}
			}
			return a.render(ctx, fmt.Sprintf("%s ok", args[0]), data)
		},
	}
	cmd.Flags().StringVar(&params, "params", "{}", "JSON params object for the CDP method")
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix for target-scoped execution")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text for target-scoped execution")
	cmd.Flags().StringVar(&savePath, "save", "", "write a base64 result data field to this artifact path")
	return cmd
}

func saveProtocolExecArtifact(path string, result json.RawMessage) (map[string]any, any, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result, &fields); err != nil {
		return nil, nil, commandError(
			"protocol_result_not_saveable",
			"usage",
			fmt.Sprintf("protocol result is not a JSON object with a base64 data field: %v", err),
			ExitUsage,
			[]string{"cdp protocol exec Page.captureScreenshot --target <target-id> --save tmp/page.png --json"},
		)
	}
	rawData, ok := fields["data"]
	if !ok {
		return nil, nil, commandError(
			"protocol_result_not_saveable",
			"usage",
			"protocol result has no base64 data field to save",
			ExitUsage,
			[]string{"cdp protocol exec Page.captureScreenshot --target <target-id> --save tmp/page.png --json"},
		)
	}
	var encoded string
	if err := json.Unmarshal(rawData, &encoded); err != nil || encoded == "" {
		return nil, nil, commandError(
			"protocol_result_not_saveable",
			"usage",
			"protocol result data field is not a non-empty base64 string",
			ExitUsage,
			[]string{"cdp protocol exec Page.captureScreenshot --target <target-id> --save tmp/page.png --json"},
		)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, nil, commandError(
			"protocol_result_not_saveable",
			"usage",
			fmt.Sprintf("decode protocol result data: %v", err),
			ExitUsage,
			[]string{"cdp protocol exec Page.captureScreenshot --target <target-id> --save tmp/page.png --json"},
		)
	}
	writtenPath, err := writeArtifactFile(path, decoded)
	if err != nil {
		return nil, nil, err
	}
	var redacted map[string]any
	if err := json.Unmarshal(result, &redacted); err != nil {
		return nil, nil, err
	}
	redacted["data"] = map[string]any{
		"omitted": true,
		"reason":  "saved_to_artifact",
	}
	artifact := map[string]any{
		"type":     "protocol-result",
		"path":     writtenPath,
		"bytes":    len(decoded),
		"field":    "data",
		"encoding": "base64",
	}
	return artifact, redacted, nil
}

func (a *app) fetchProtocol(ctx context.Context) (cdp.Protocol, error) {
	runtime, err := a.requiredDaemonRuntime(ctx)
	if err != nil {
		return cdp.Protocol{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
		)
	}
	protocol, err := daemon.RuntimeClient{Runtime: runtime}.FetchProtocol(ctx)
	if err != nil {
		return cdp.Protocol{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("fetch protocol metadata through daemon: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp daemon status --json"},
		)
	}
	return protocol, nil
}

func (a *app) newWorkflowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Run high-level browser debugging workflows",
	}
	cmd.AddCommand(planned("verify <url>", "Open a URL and collect basic verification evidence"))
	cmd.AddCommand(a.newWorkflowVisiblePostsCommand())
	cmd.AddCommand(a.newWorkflowHackerNewsCommand())
	cmd.AddCommand(a.newWorkflowConsoleErrorsCommand())
	cmd.AddCommand(a.newWorkflowNetworkFailuresCommand())
	cmd.AddCommand(a.newWorkflowPageLoadCommand())
	cmd.AddCommand(planned("perf <url>", "Capture and summarize performance evidence"))
	cmd.AddCommand(planned("a11y", "Run a focused accessibility workflow"))
	return cmd
}

type hackerNewsFrontpage struct {
	URL          string            `json:"url"`
	Title        string            `json:"title"`
	Count        int               `json:"count"`
	Stories      []hackerNewsStory `json:"stories"`
	Organization map[string]string `json:"organization"`
	Error        *snapshotError    `json:"error,omitempty"`
}

type hackerNewsStory struct {
	Rank        int    `json:"rank,omitempty"`
	ID          string `json:"id,omitempty"`
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	Site        string `json:"site,omitempty"`
	Score       int    `json:"score,omitempty"`
	User        string `json:"user,omitempty"`
	Age         string `json:"age,omitempty"`
	Comments    int    `json:"comments,omitempty"`
	CommentsURL string `json:"comments_url,omitempty"`
}

func (a *app) newWorkflowHackerNewsCommand() *cobra.Command {
	var limit int
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "hacker-news [url]",
		Short: "Open Hacker News and summarize visible stories",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()

			rawURL := "https://news.ycombinator.com/"
			if len(args) == 1 {
				rawURL = strings.TrimSpace(args[0])
			}

			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			targetID, err := a.createPageTarget(ctx, client, rawURL)
			if err != nil {
				_ = closeClient(ctx)
				return err
			}
			session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
			if err != nil {
				_ = closeClient(ctx)
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("attach target %s: %v", targetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			defer session.Close(ctx)

			frontpage, err := waitForHackerNewsStories(ctx, session, limit, wait)
			if err != nil {
				return err
			}
			if len(frontpage.Stories) == 0 {
				return commandError(
					"no_visible_posts",
					"check_failed",
					"no Hacker News story rows matched tr.athing",
					ExitCheckFailed,
					[]string{"cdp workflow hacker-news --wait 30s --json", "cdp snapshot --selector '.titleline' --json"},
				)
			}
			lines := hackerNewsStoryLines(frontpage.Stories)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":           true,
				"url":          rawURL,
				"target":       pageRow(cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}),
				"workflow":     map[string]any{"name": "hacker-news", "count": len(frontpage.Stories), "wait": durationString(wait), "limit": limit},
				"organization": frontpage.Organization,
				"stories":      frontpage.Stories,
				"frontpage":    frontpage,
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 30, "maximum number of stories to return; use 0 for no limit")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "how long to wait for Hacker News story rows")
	return cmd
}

func waitForHackerNewsStories(ctx context.Context, session *cdp.PageSession, limit int, wait time.Duration) (hackerNewsFrontpage, error) {
	if limit < 0 {
		return hackerNewsFrontpage{}, commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp workflow hacker-news --limit 30 --json"})
	}
	if wait < 0 {
		return hackerNewsFrontpage{}, commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp workflow hacker-news --wait 30s --json"})
	}
	deadline := time.Now().Add(wait)
	var last hackerNewsFrontpage
	for {
		frontpage, err := collectHackerNewsFrontpage(ctx, session, limit)
		if err != nil {
			return hackerNewsFrontpage{}, err
		}
		last = frontpage
		if len(frontpage.Stories) > 0 || wait == 0 || time.Now().After(deadline) {
			return last, nil
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return hackerNewsFrontpage{}, commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp workflow hacker-news --timeout 45s --json"})
		case <-timer.C:
		}
	}
}

func collectHackerNewsFrontpage(ctx context.Context, session *cdp.PageSession, limit int) (hackerNewsFrontpage, error) {
	result, err := session.Evaluate(ctx, hackerNewsExpression(limit), true)
	if err != nil {
		return hackerNewsFrontpage{}, commandError("connection_failed", "connection", fmt.Sprintf("Hacker News workflow target %s: %v", session.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	if result.Exception != nil {
		return hackerNewsFrontpage{}, commandError("javascript_exception", "runtime", fmt.Sprintf("Hacker News workflow javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp workflow hacker-news --json", "cdp snapshot --selector body --json"})
	}
	var frontpage hackerNewsFrontpage
	if err := json.Unmarshal(result.Object.Value, &frontpage); err != nil {
		return hackerNewsFrontpage{}, commandError("invalid_workflow_result", "internal", fmt.Sprintf("decode Hacker News workflow result: %v", err), ExitInternal, []string{"cdp doctor --json", "cdp eval 'document.title' --json"})
	}
	if frontpage.Error != nil {
		return hackerNewsFrontpage{}, commandError("invalid_selector", "usage", fmt.Sprintf("Hacker News selector failed: %s", frontpage.Error.Message), ExitUsage, []string{"cdp workflow hacker-news --json", "cdp snapshot --selector '.athing' --json"})
	}
	return frontpage, nil
}

func hackerNewsExpression(limit int) string {
	return fmt.Sprintf(`(() => {
  "__cdp_cli_hn_frontpage__";
  const limit = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const parseNumber = (value) => {
    const match = normalize(value).match(/\d+/);
    return match ? Number(match[0]) : 0;
  };
  let rows;
  try {
    rows = Array.from(document.querySelectorAll("tr.athing"));
  } catch (error) {
    return { url: location.href, title: document.title, count: 0, stories: [], organization: {}, error: { name: error.name, message: error.message } };
  }
  const stories = [];
  for (const row of rows) {
    const titleLink = row.querySelector(".titleline > a") || row.querySelector(".storylink");
    if (!titleLink) continue;
    const metaRow = row.nextElementSibling;
    const subtext = metaRow && metaRow.querySelector(".subtext");
    const commentLink = Array.from(subtext ? subtext.querySelectorAll("a") : []).find((link) => /comment|discuss/i.test(link.textContent || ""));
    stories.push({
      rank: parseNumber(row.querySelector(".rank") && row.querySelector(".rank").textContent),
      id: row.getAttribute("id") || "",
      title: normalize(titleLink.textContent),
      url: titleLink.href || titleLink.getAttribute("href") || "",
      site: normalize(row.querySelector(".sitestr") && row.querySelector(".sitestr").textContent),
      score: parseNumber(subtext && subtext.querySelector(".score") && subtext.querySelector(".score").textContent),
      user: normalize(subtext && subtext.querySelector(".hnuser") && subtext.querySelector(".hnuser").textContent),
      age: normalize(subtext && subtext.querySelector(".age") && subtext.querySelector(".age").textContent),
      comments: parseNumber(commentLink && commentLink.textContent),
      comments_url: commentLink ? commentLink.href : ""
    });
    if (limit > 0 && stories.length >= limit) break;
  }
  return {
    url: location.href,
    title: document.title,
    count: stories.length,
    stories,
    organization: {
      page_kind: "table-based link aggregator front page",
      container_selector: "table.itemlist",
      story_row_selector: "tr.athing",
      metadata_row_selector: "tr.athing + tr .subtext",
      title_selector: ".titleline > a",
      rank_selector: ".rank",
      discussion_signal: "score, author, age, and comment links live in the metadata row after each story row"
    }
  };
})()`, limit)
}

func hackerNewsStoryLines(stories []hackerNewsStory) []string {
	lines := make([]string, 0, len(stories)+1)
	lines = append(lines, fmt.Sprintf("%-4s %7s %9s  %s", "rank", "points", "comments", "title"))
	for _, story := range stories {
		lines = append(lines, fmt.Sprintf(
			"#%-3d %7s %9s  %s",
			story.Rank,
			hackerNewsCountLabel(story.Score, "pt", "pts"),
			hackerNewsCountLabel(story.Comments, "comment", "comments"),
			story.Title,
		))
	}
	return lines
}

func hackerNewsCountLabel(count int, singular, plural string) string {
	if count == 0 {
		return "-"
	}
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}

func (a *app) newWorkflowConsoleErrorsCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var wait time.Duration
	var limit int
	cmd := &cobra.Command{
		Use:   "console-errors",
		Short: "Summarize console errors and warnings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if wait < 0 || limit < 0 {
				return commandError("usage", "usage", "--wait and --limit must be non-negative", ExitUsage, []string{"cdp workflow console-errors --wait 2s --json"})
			}
			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			messages, truncated, err := collectConsoleMessages(ctx, client, session.SessionID, wait, limit, true, nil)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture console errors target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			lines := consoleMessageLines(messages)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"messages": messages,
				"workflow": map[string]any{
					"name":      "console-errors",
					"count":     len(messages),
					"wait":      durationString(wait),
					"limit":     limit,
					"truncated": truncated,
					"next_commands": []string{
						"cdp console --errors --wait 2s --json",
						"cdp screenshot --out tmp/page.png --json",
					},
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().DurationVar(&wait, "wait", time.Second, "how long to collect console/log events")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of messages to return; use 0 for no limit")
	return cmd
}

func (a *app) newWorkflowNetworkFailuresCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var wait time.Duration
	var limit int
	cmd := &cobra.Command{
		Use:   "network-failures",
		Short: "Summarize failed and HTTP error network requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if wait < 0 || limit < 0 {
				return commandError("usage", "usage", "--wait and --limit must be non-negative", ExitUsage, []string{"cdp workflow network-failures --wait 2s --json"})
			}
			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			requests, truncated, err := collectNetworkRequests(ctx, client, session.SessionID, wait, limit, true)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture network failures target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			lines := networkRequestLines(requests)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"requests": requests,
				"workflow": map[string]any{
					"name":      "network-failures",
					"count":     len(requests),
					"wait":      durationString(wait),
					"limit":     limit,
					"truncated": truncated,
					"next_commands": []string{
						"cdp network --failed --wait 2s --json",
						"cdp workflow console-errors --wait 2s --json",
					},
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().DurationVar(&wait, "wait", time.Second, "how long to collect network events")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of requests to return; use 0 for no limit")
	return cmd
}

type pageLoadStorageKeys struct {
	URL                string   `json:"url"`
	Origin             string   `json:"origin"`
	CookieKeys         []string `json:"cookie_keys"`
	LocalStorageKeys   []string `json:"local_storage_keys"`
	SessionStorageKeys []string `json:"session_storage_keys"`
}

type pageLoadMetric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

func (a *app) newWorkflowPageLoadCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var reload bool
	var ignoreCache bool
	var wait time.Duration
	var include string
	var limit int
	var outPath string
	cmd := &cobra.Command{
		Use:   "page-load [url]",
		Short: "Capture console, network, storage, and performance signals around a page load",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || limit < 0 {
				return commandError("usage", "usage", "--wait and --limit must be non-negative", ExitUsage, []string{"cdp workflow page-load https://example.com --wait 10s --json"})
			}
			fallback := wait + 10*time.Second
			if fallback < 30*time.Second {
				fallback = 30 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			rawURL := ""
			if len(args) == 1 {
				rawURL = strings.TrimSpace(args[0])
			}
			includeSet := pageLoadIncludeSet(include)
			client, closeClient, err := a.browserEventCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			closeOwned := true
			defer func() {
				if closeOwned {
					_ = closeClient(ctx)
				}
			}()

			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			if rawURL != "" && strings.TrimSpace(targetID) == "" && strings.TrimSpace(urlContains) == "" {
				createdID, err := a.createPageTarget(ctx, client, "about:blank")
				if err != nil {
					return err
				}
				target.TargetID = createdID
			} else {
				selected, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains)
				if err != nil {
					return err
				}
				target = selected
			}

			closeOwned = false
			session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
			if err != nil {
				closeOwned = true
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("attach target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			defer session.Close(ctx)

			collectorErrors := enablePageLoadCollectors(ctx, client, session.SessionID, includeSet)
			trigger := "observe"
			frameID := ""
			if rawURL != "" {
				frameID, err = session.Navigate(ctx, rawURL)
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("navigation", err))
				} else {
					target.URL = rawURL
					trigger = "navigate"
				}
			} else if reload {
				if err := session.Reload(ctx, ignoreCache); err != nil {
					collectorErrors = append(collectorErrors, collectorError("reload", err))
				} else {
					trigger = "reload"
				}
			}

			requests, requestsTruncated, messages, messagesTruncated, err := collectPageLoadEvents(ctx, client, session.SessionID, wait, limit, includeSet)
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("events", err))
			}
			var storage pageLoadStorageKeys
			if includeSet["storage"] {
				if storage, err = collectPageLoadStorageKeys(ctx, session); err != nil {
					collectorErrors = append(collectorErrors, collectorError("storage", err))
				}
			}
			var metrics []pageLoadMetric
			if includeSet["performance"] {
				if metrics, err = collectPerformanceMetrics(ctx, session); err != nil {
					collectorErrors = append(collectorErrors, collectorError("performance", err))
				}
			}
			var history cdp.NavigationHistory
			if includeSet["navigation"] {
				if history, err = session.NavigationHistory(ctx); err != nil {
					collectorErrors = append(collectorErrors, collectorError("navigation_history", err))
				}
			}

			report := map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"requests": requests,
				"messages": messages,
				"workflow": map[string]any{
					"name":               "page-load",
					"trigger":            trigger,
					"requested_url":      rawURL,
					"frame_id":           frameID,
					"wait":               durationString(wait),
					"include":            setKeys(includeSet),
					"limit":              limit,
					"request_count":      len(requests),
					"message_count":      len(messages),
					"requests_truncated": requestsTruncated,
					"messages_truncated": messagesTruncated,
					"collector_errors":   collectorErrors,
					"partial":            len(collectorErrors) > 0,
					"next_commands": []string{
						"cdp console --errors --wait 2s --json",
						"cdp network --failed --wait 2s --json",
						"cdp protocol exec Performance.getMetrics --target <target-id> --json",
					},
				},
			}
			if includeSet["storage"] {
				report["storage"] = storage
			}
			if includeSet["performance"] {
				report["performance"] = map[string]any{"metrics": metrics, "count": len(metrics)}
			}
			if includeSet["navigation"] {
				report["navigation"] = history
			}
			if strings.TrimSpace(outPath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal page-load report: %v", err), ExitInternal, []string{"cdp workflow page-load --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "page-load", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "page-load", "path": writtenPath}}
			}
			human := fmt.Sprintf("page-load\t%s\t%d requests\t%d messages", trigger, len(requests), len(messages))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().BoolVar(&reload, "reload", false, "reload the selected page after attaching collectors")
	cmd.Flags().BoolVar(&ignoreCache, "ignore-cache", false, "reload while bypassing cache")
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect events after navigation or reload")
	cmd.Flags().StringVar(&include, "include", "console,network,storage,performance,navigation", "comma-separated collectors: console,network,storage,performance,navigation")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum console messages and requests per collector; use 0 for no limit")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the JSON page-load report artifact")
	return cmd
}

func pageLoadIncludeSet(include string) map[string]bool {
	set := parseCSVSet(include)
	if len(set) == 0 {
		set = parseCSVSet("console,network,storage,performance,navigation")
	}
	if set["all"] {
		set = parseCSVSet("console,network,storage,performance,navigation")
	}
	return set
}

func enablePageLoadCollectors(ctx context.Context, client browserEventClient, sessionID string, includeSet map[string]bool) []map[string]string {
	var collectorErrors []map[string]string
	enable := func(name, method string) {
		if err := client.CallSession(ctx, sessionID, method, map[string]any{}, nil); err != nil {
			collectorErrors = append(collectorErrors, collectorError(name, err))
		}
	}
	if includeSet["navigation"] {
		enable("navigation", "Page.enable")
	}
	if includeSet["console"] {
		enable("runtime", "Runtime.enable")
		enable("log", "Log.enable")
	}
	if includeSet["network"] {
		enable("network", "Network.enable")
	}
	if includeSet["performance"] {
		enable("performance", "Performance.enable")
	}
	return collectorErrors
}

func collectPageLoadEvents(ctx context.Context, client browserEventClient, sessionID string, wait time.Duration, limit int, includeSet map[string]bool) ([]networkRequest, bool, []consoleMessage, bool, error) {
	requestsByID := map[string]*networkRequest{}
	var requestOrder []string
	var messages []consoleMessage
	addEvent := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		if includeSet["network"] {
			req, ok := networkRequestFromEvent(event)
			if ok && req.ID != "" {
				existing, ok := requestsByID[req.ID]
				if !ok {
					copyReq := req
					requestsByID[req.ID] = &copyReq
					requestOrder = append(requestOrder, req.ID)
				} else {
					mergeNetworkRequest(existing, req)
				}
			}
		}
		if includeSet["console"] {
			msg, ok := consoleMessageFromEvent(event)
			if ok {
				msg.ID = len(messages)
				messages = append(messages, msg)
			}
		}
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return nil, false, nil, false, err
	}
	for _, event := range events {
		addEvent(event)
	}
	if wait > 0 {
		eventCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for {
			event, err := client.ReadEvent(eventCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded) {
					break
				}
				return nil, false, nil, false, err
			}
			addEvent(event)
		}
	}

	requests := make([]networkRequest, 0, len(requestOrder))
	for _, id := range requestOrder {
		requests = append(requests, *requestsByID[id])
	}
	requestsTruncated := false
	if limit > 0 && len(requests) > limit {
		requests = requests[:limit]
		requestsTruncated = true
	}
	messagesTruncated := false
	if limit > 0 && len(messages) > limit {
		messages = messages[:limit]
		messagesTruncated = true
		for i := range messages {
			messages[i].ID = i
		}
	}
	return requests, requestsTruncated, messages, messagesTruncated, nil
}

func collectorError(name string, err error) map[string]string {
	return map[string]string{"collector": name, "error": err.Error()}
}

func collectPageLoadStorageKeys(ctx context.Context, session *cdp.PageSession) (pageLoadStorageKeys, error) {
	result, err := session.Evaluate(ctx, pageLoadStorageExpression(), true)
	if err != nil {
		return pageLoadStorageKeys{}, err
	}
	if result.Exception != nil {
		return pageLoadStorageKeys{}, fmt.Errorf("javascript exception: %s", result.Exception.Text)
	}
	var storage pageLoadStorageKeys
	if err := json.Unmarshal(result.Object.Value, &storage); err != nil {
		return pageLoadStorageKeys{}, fmt.Errorf("decode storage keys: %w", err)
	}
	return storage, nil
}

func pageLoadStorageExpression() string {
	return `(() => {
  "__cdp_cli_page_load_storage__";
  const keys = (store) => {
    try { return Object.keys(store || {}).sort(); } catch (error) { return []; }
  };
  const cookieKeys = (() => {
    try {
      return (document.cookie || "")
        .split(";")
        .map((part) => part.trim().split("=")[0])
        .filter(Boolean)
        .sort();
    } catch (error) {
      return [];
    }
  })();
  return {
    url: location.href,
    origin: location.origin,
    cookie_keys: cookieKeys,
    local_storage_keys: keys(window.localStorage),
    session_storage_keys: keys(window.sessionStorage)
  };
})()`
}

func collectPerformanceMetrics(ctx context.Context, session *cdp.PageSession) ([]pageLoadMetric, error) {
	raw, err := session.Exec(ctx, "Performance.getMetrics", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var result struct {
		Metrics []pageLoadMetric `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode performance metrics: %w", err)
	}
	return result.Metrics, nil
}

func (a *app) newWorkflowVisiblePostsCommand() *cobra.Command {
	var selector string
	var limit int
	var minChars int
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "visible-posts <url>",
		Short: "Open a feed page and list visible post text",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()

			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			rawURL := strings.TrimSpace(args[0])
			targetID, err := a.createPageTarget(ctx, client, rawURL)
			if err != nil {
				_ = closeClient(ctx)
				return err
			}
			session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
			if err != nil {
				_ = closeClient(ctx)
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("attach target %s: %v", targetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			defer session.Close(ctx)

			snapshot, err := waitForSnapshotItems(ctx, session, selector, limit, minChars, wait)
			if err != nil {
				return err
			}
			if len(snapshot.Items) == 0 {
				return commandError(
					"no_visible_posts",
					"check_failed",
					fmt.Sprintf("no visible post elements matched selector %q", selector),
					ExitCheckFailed,
					[]string{
						"cdp snapshot --selector article --json",
						"cdp workflow visible-posts <url> --selector article --wait 30s --json",
					},
				)
			}
			lines := snapshotTextLines(snapshot.Items)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"url":      rawURL,
				"target":   pageRow(cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}),
				"selector": selector,
				"items":    snapshot.Items,
				"snapshot": snapshot,
			})
		},
	}
	cmd.Flags().StringVar(&selector, "selector", "article", "CSS selector for post containers")
	cmd.Flags().IntVar(&limit, "limit", 10, "maximum number of visible posts to return")
	cmd.Flags().IntVar(&minChars, "min-chars", 20, "minimum normalized text length per post")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "how long to wait for matching visible posts")
	return cmd
}

func waitForSnapshotItems(ctx context.Context, session *cdp.PageSession, selector string, limit, minChars int, wait time.Duration) (pageSnapshot, error) {
	if wait < 0 {
		return pageSnapshot{}, commandError(
			"usage",
			"usage",
			"--wait must be non-negative",
			ExitUsage,
			[]string{"cdp workflow visible-posts <url> --wait 30s --json"},
		)
	}
	deadline := time.Now().Add(wait)
	var last pageSnapshot
	for {
		snapshot, err := collectPageSnapshot(ctx, session, selector, limit, minChars)
		if err != nil {
			return pageSnapshot{}, err
		}
		last = snapshot
		if len(snapshot.Items) > 0 || wait == 0 || time.Now().After(deadline) {
			return last, nil
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return pageSnapshot{}, commandError(
				"timeout",
				"timeout",
				ctx.Err().Error(),
				ExitTimeout,
				[]string{"cdp workflow visible-posts <url> --timeout 45s --json"},
			)
		case <-timer.C:
		}
	}
}

func (a *app) newMCPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Inspect and generate MCP integration config",
	}
	claude := &cobra.Command{
		Use:   "claude",
		Short: "Inspect and generate Claude MCP config",
	}
	claude.AddCommand(planned("status", "Inspect Claude MCP configuration"))
	claude.AddCommand(planned("print-config", "Print suggested Claude MCP configuration"))
	claude.AddCommand(planned("install", "Install cdp-cli MCP integration"))
	claude.AddCommand(planned("restore-chrome-devtools", "Print restoration commands for official Chrome DevTools MCP"))
	cmd.AddCommand(claude)
	return cmd
}

func planned(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := cmd.CommandPath()
			return notImplemented(name)
		},
	}
}

func describeCommand(cmd *cobra.Command) commandInfo {
	info := commandInfo{
		Name:     cmd.Name(),
		Use:      cmd.UseLine(),
		Short:    cmd.Short,
		Aliases:  cmd.Aliases,
		Examples: commandExamples(cmd.CommandPath()),
	}

	for _, child := range cmd.Commands() {
		if child.Hidden {
			continue
		}
		info.Children = append(info.Children, describeCommand(child))
	}

	return info
}

func commandExamples(path string) []string {
	examples := map[string][]string{
		"cdp": {
			"cdp doctor --json",
			"cdp describe --json | jq '.commands.children | map(.name)'",
		},
		"cdp describe": {
			"cdp describe --json",
			"cdp describe --command 'daemon status' --json",
		},
		"cdp doctor": {
			"cdp doctor --json",
			"cdp doctor --check daemon --json",
		},
		"cdp explain-error": {
			"cdp explain-error not_implemented --json",
		},
		"cdp exit-codes": {
			"cdp exit-codes --json",
		},
		"cdp schema": {
			"cdp schema --json",
			"cdp schema error-envelope --json",
		},
		"cdp daemon start": {
			"cdp daemon start --auto-connect --json",
			"cdp daemon start --browser-url <browser-url> --json",
			"cdp daemon start --autoConnect --json",
		},
		"cdp daemon status": {
			"cdp daemon status --json",
		},
		"cdp daemon stop": {
			"cdp daemon stop --json",
		},
		"cdp daemon restart": {
			"cdp daemon restart --auto-connect --json",
			"cdp daemon restart --debug --autoConnect --active-browser-probe --json",
			"cdp daemon restart --browser-url <browser-url> --json",
		},
		"cdp daemon keepalive": {
			"cdp daemon keepalive --auto-connect --display :0 --json",
			"cdp daemon keepalive --browser-url <browser-url> --json",
			"cdp daemon keepalive --connection default --probe auto --json",
		},
		"cdp daemon logs": {
			"cdp daemon logs --tail 100 --json",
			"cdp daemon logs --tail 0 --json",
		},
		"cdp connection remove": {
			"cdp connection remove stale --json",
		},
		"cdp connection prune": {
			"cdp connection prune --missing-projects --dry-run --json",
		},
		"cdp connection list": {
			"cdp connection list --json",
			"cdp connection list --project . --json",
		},
		"cdp connection resolve": {
			"cdp connection resolve --json",
			"cdp connection resolve --connection default --json",
		},
		"cdp targets": {
			"cdp targets --json",
			"cdp targets --limit 10 --json",
			"cdp targets --type service_worker --json",
		},
		"cdp pages": {
			"cdp pages --json",
			"cdp pages --limit 10 --json",
			"cdp pages --include-url localhost --exclude-url admin --json",
		},
		"cdp page select": {
			"cdp page select <target-id> --json",
			"cdp page select --url-contains localhost --json",
		},
		"cdp page reload": {
			"cdp page reload --target <target-id> --json",
			"cdp page reload --url-contains localhost --ignore-cache --json",
		},
		"cdp page back": {
			"cdp page back --target <target-id> --json",
		},
		"cdp page forward": {
			"cdp page forward --target <target-id> --json",
		},
		"cdp page activate": {
			"cdp page activate --target <target-id> --json",
		},
		"cdp page close": {
			"cdp page close --target <target-id> --json",
		},
		"cdp open": {
			"cdp open https://example.com --json",
			"cdp open https://example.com --new-tab=false --target <target-id> --json",
		},
		"cdp eval": {
			"cdp eval 'document.title' --json",
			"cdp eval 'Array.from(document.querySelectorAll(\"article\"), el => el.innerText)' --url-contains x.com --json",
		},
		"cdp text": {
			"cdp text main --json",
			"cdp text article --limit 10 --url-contains localhost --json",
		},
		"cdp html": {
			"cdp html main --max-chars 4000 --json",
			"cdp html '#root' --limit 1 --json",
		},
		"cdp dom query": {
			"cdp dom query button --json",
			"cdp dom query '[role=\"button\"]' --limit 20 --json",
		},
		"cdp css inspect": {
			"cdp css inspect main --json",
			"cdp css inspect '.panel' --url-contains localhost --json",
		},
		"cdp layout overflow": {
			"cdp layout overflow --json",
			"cdp layout overflow --selector 'body *' --limit 20 --json",
		},
		"cdp wait text": {
			"cdp wait text Ready --timeout 10s --json",
			"cdp wait text 'Dashboard loaded' --url-contains localhost --json",
		},
		"cdp wait selector": {
			"cdp wait selector main --timeout 10s --json",
			"cdp wait selector '[data-ready=\"true\"]' --poll 500ms --json",
		},
		"cdp snapshot": {
			"cdp snapshot --selector body --json",
			"cdp snapshot --selector article --limit 10 --url-contains x.com --json",
		},
		"cdp screenshot": {
			"cdp screenshot --out tmp/page.png --json",
			"cdp screenshot --target <target-id> --full-page --out tmp/page.png --json",
			"cdp screenshot --url-contains localhost --out tmp/page.png --json",
		},
		"cdp console": {
			"cdp console --json",
			"cdp console --errors --wait 2s --json",
			"cdp console --url-contains localhost --types error,warning --json",
		},
		"cdp network": {
			"cdp network --wait 2s --json",
			"cdp network --failed --url-contains localhost --json",
		},
		"cdp network capture": {
			"cdp network capture --reload --wait 20s --out tmp/network.local.json --json",
			"cdp network capture --url-contains localhost --redact safe --out tmp/network-shareable.json --json",
		},
		"cdp storage": {
			"cdp storage list --url-contains localhost --json",
			"cdp storage snapshot --out tmp/storage.local.json --json",
		},
		"cdp storage list": {
			"cdp storage list --url-contains localhost --json",
			"cdp storage list --include localStorage,sessionStorage,cookies --json",
		},
		"cdp storage get": {
			"cdp storage get localStorage feature_flag --url-contains localhost --json",
		},
		"cdp storage set": {
			"cdp storage set localStorage feature_flag enabled --url-contains localhost --json",
			"cdp storage set sessionStorage seed @tmp/seed.json --json",
		},
		"cdp storage delete": {
			"cdp storage delete localStorage feature_flag --url-contains localhost --json",
		},
		"cdp storage clear": {
			"cdp storage clear sessionStorage --url-contains localhost --json",
		},
		"cdp storage snapshot": {
			"cdp storage snapshot --out tmp/app-storage.local.json --json",
			"cdp storage snapshot --redact safe --out tmp/app-storage-shareable.json --json",
		},
		"cdp storage diff": {
			"cdp storage diff --left tmp/before.local.json --right tmp/after.local.json --json",
		},
		"cdp storage cookies": {
			"cdp storage cookies list --url https://example.com --json",
		},
		"cdp storage cookies list": {
			"cdp storage cookies list --url-contains localhost --json",
		},
		"cdp storage cookies set": {
			"cdp storage cookies set --url https://example.com --name feature_flag --value enabled --json",
		},
		"cdp storage cookies delete": {
			"cdp storage cookies delete --url https://example.com --name feature_flag --json",
		},
		"cdp protocol metadata": {
			"cdp protocol metadata --json",
		},
		"cdp protocol domains": {
			"cdp protocol domains --json",
			"cdp protocol domains --experimental --json",
		},
		"cdp protocol search": {
			"cdp protocol search screenshot --json",
			"cdp protocol search console --kind event --json",
		},
		"cdp protocol describe": {
			"cdp protocol describe Page.captureScreenshot --json",
		},
		"cdp protocol examples": {
			"cdp protocol examples Page.captureScreenshot --json",
			"cdp protocol examples Runtime.evaluate --json",
		},
		"cdp protocol exec": {
			"cdp protocol exec Browser.getVersion --params '{}' --json",
			"cdp protocol exec Runtime.evaluate --target <target-id> --params '{\"expression\":\"document.title\",\"returnByValue\":true}' --json",
			"cdp protocol exec Page.captureScreenshot --target <target-id> --params '{\"format\":\"png\"}' --save tmp/page.png --json",
			"cdp protocol exec DOM.getDocument --url-contains localhost --json",
		},
		"cdp workflow verify": {
			"cdp workflow verify https://example.com --json",
		},
		"cdp workflow visible-posts": {
			"cdp workflow visible-posts https://x.com/<handle> --limit 5 --json",
			"cdp workflow visible-posts https://example.com/feed --selector article --wait 30s --json",
		},
		"cdp workflow hacker-news": {
			"cdp workflow hacker-news --limit 10 --json",
			"cdp workflow hacker-news https://news.ycombinator.com/news --wait 30s --json",
		},
		"cdp workflow console-errors": {
			"cdp workflow console-errors --wait 2s --json",
			"cdp workflow console-errors --url-contains localhost --json",
		},
		"cdp workflow network-failures": {
			"cdp workflow network-failures --wait 2s --json",
			"cdp workflow network-failures --url-contains localhost --json",
		},
		"cdp workflow page-load": {
			"cdp workflow page-load https://example.com --wait 10s --json",
			"cdp workflow page-load --url-contains localhost --reload --include console,network,performance --out tmp/page-load.local.json --json",
		},
	}
	return examples[path]
}

func findCommand(root *cobra.Command, path string) (*cobra.Command, error) {
	parts := strings.Fields(path)
	if len(parts) > 0 && parts[0] == root.Name() {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return root, nil
	}

	found, _, err := root.Find(parts)
	if err != nil || found == nil {
		return nil, commandError(
			"unknown_command",
			"usage",
			fmt.Sprintf("unknown command path %q", path),
			ExitUsage,
			[]string{"cdp describe --json", "cdp --help"},
		)
	}
	return found, nil
}
