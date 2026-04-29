package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run local readiness checks",
		Long:  "Run readiness checks for the CLI, selected browser connection, and daemon path.",
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
	return cmd
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
	cmd.AddCommand(a.newDaemonHoldCommand())
	cmd.AddCommand(planned("logs", "Show attach daemon logs"))
	return cmd
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

			if a.opts.browserURL != "" && a.opts.autoConnect {
				return commandError(
					"conflicting_connection_flags",
					"usage",
					"use either --browser-url or --auto-connect, not both",
					ExitUsage,
					[]string{"cdp daemon start --auto-connect --json", "cdp daemon start --browser-url <browser-url> --json"},
				)
			}
			if reconnect < 0 {
				return commandError(
					"invalid_reconnect_interval",
					"usage",
					"--reconnect must be a non-negative duration",
					ExitUsage,
					[]string{"cdp daemon start --reconnect 30s --json"},
				)
			}

			var err error
			if err := a.applySelectedConnection(ctx); err != nil {
				return err
			}
			explicitConnection := a.opts.browserURL != "" || a.opts.autoConnect
			keepAlive := a.opts.autoConnect
			if keepAlive || prime {
				a.opts.activeProbe = true
			}

			var endpoint string
			var runtime *daemon.Runtime
			var alreadyRunning bool
			var savedConnection *state.Connection
			var statePath string
			if keepAlive && explicitConnection && remember {
				savedConnection, statePath, err = a.rememberDaemonConnection(ctx, connectionName)
				if err != nil {
					return err
				}
			}
			if keepAlive {
				endpoint, err = a.browserEndpoint(ctx)
				if err != nil {
					return commandError(
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
					ConnectionMode:       "auto_connect",
					Channel:              a.opts.channel,
					WebSocketDebuggerURL: true,
				}
			} else {
				probe, err = a.browserProbe(ctx)
				if err != nil {
					return commandError(
						"invalid_browser_url",
						"usage",
						err.Error(),
						ExitUsage,
						[]string{"cdp daemon start --browser-url <browser-url> --json"},
					)
				}
			}

			if savedConnection == nil && explicitConnection && remember {
				savedConnection, statePath, err = a.rememberDaemonConnection(ctx, connectionName)
				if err != nil {
					return err
				}
			}

			if keepAlive {
				r, reused, err := a.startKeepAlive(ctx, endpoint, reconnect)
				if err != nil {
					return commandError(
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
					return err
				}
			}

			start := map[string]any{
				"state":              status.State,
				"message":            status.Message,
				"connection_mode":    status.ConnectionMode,
				"prime":              prime,
				"connection_saved":   savedConnection != nil,
				"next_commands":      status.NextCommands,
				"reconnect_interval": durationString(reconnect),
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
			return a.render(ctx, human, data)
		},
	}
	cmd.Flags().BoolVar(&prime, "prime", false, "compatibility flag; daemon start validates auto-connect by default")
	cmd.Flags().DurationVar(&reconnect, "reconnect", 0, "requested daemon reconnect interval, such as 30s")
	cmd.Flags().StringVar(&connectionName, "connection-name", "default", "connection name to save when --browser-url or --auto-connect is supplied")
	cmd.Flags().BoolVar(&remember, "remember", true, "save supplied connection metadata for future on-demand commands")
	return cmd
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
			pages = filterRowsContains(pages, "url", urlContains)
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
	cmd.AddCommand(a.newPageReloadCommand())
	cmd.AddCommand(a.newPageHistoryCommand("back", "Navigate the selected page back in history", -1))
	cmd.AddCommand(a.newPageHistoryCommand("forward", "Navigate the selected page forward in history", 1))
	cmd.AddCommand(a.newPageActivateCommand())
	cmd.AddCommand(a.newPageCloseCommand())
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

func (a *app) browserCDPClient(ctx context.Context) (cdp.CommandClient, func(context.Context) error, error) {
	opts, err := a.browserOptions(ctx)
	if err != nil {
		return nil, nil, err
	}
	if runtime, ok := a.runningRuntimeForOptions(ctx, opts); ok {
		return daemon.RuntimeClient{Runtime: runtime}, func(context.Context) error { return nil }, nil
	}
	if opts.AutoConnect && !opts.ActiveProbe {
		return nil, nil, fmt.Errorf("auto-connect browser attach is passive by default to avoid Chrome prompts; run `cdp daemon start --auto-connect --json` once, or pass --active-browser-probe to attach directly")
	}
	endpoint, err := browser.ResolveEndpoint(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	client, err := cdp.Dial(ctx, endpoint)
	if err != nil {
		return nil, nil, err
	}
	return client, func(context.Context) error {
		return client.CloseNormal()
	}, nil
}

func (a *app) browserEventCDPClient(ctx context.Context) (*cdp.Client, func(context.Context) error, error) {
	opts, err := a.browserOptions(ctx)
	if err != nil {
		return nil, nil, err
	}
	if runtime, ok := a.runningRuntimeForOptions(ctx, opts); ok {
		if runtime.Endpoint == "" {
			return nil, nil, fmt.Errorf("running daemon does not expose an event-capable browser endpoint; restart it with `cdp daemon stop --json` then `cdp daemon start --auto-connect --json`")
		}
		client, err := cdp.Dial(ctx, runtime.Endpoint)
		if err != nil {
			return nil, nil, err
		}
		return client, func(context.Context) error {
			return client.CloseNormal()
		}, nil
	}
	if opts.AutoConnect && !opts.ActiveProbe {
		return nil, nil, fmt.Errorf("auto-connect browser attach is passive by default to avoid Chrome prompts; run `cdp daemon start --auto-connect --json` once, or pass --active-browser-probe to attach directly")
	}
	endpoint, err := browser.ResolveEndpoint(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	client, err := cdp.Dial(ctx, endpoint)
	if err != nil {
		return nil, nil, err
	}
	return client, func(context.Context) error {
		return client.CloseNormal()
	}, nil
}

func (a *app) runningRuntimeForOptions(ctx context.Context, opts browser.ProbeOptions) (daemon.Runtime, bool) {
	if !opts.AutoConnect || opts.BrowserURL != "" {
		return daemon.Runtime{}, false
	}
	store, err := a.stateStore()
	if err != nil {
		return daemon.Runtime{}, false
	}
	runtime, ok, err := daemon.LoadRuntime(ctx, store.Dir)
	if err != nil || !ok {
		return daemon.Runtime{}, false
	}
	if runtime.ConnectionMode != "auto_connect" {
		return daemon.Runtime{}, false
	}
	if opts.UserDataDir != "" && runtime.UserDataDir != opts.UserDataDir {
		return daemon.Runtime{}, false
	}
	if !daemon.RuntimeRunning(runtime) || !daemon.RuntimeSocketReady(ctx, runtime) {
		return daemon.Runtime{}, false
	}
	return runtime, true
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

func (a *app) attachPageEventSession(ctx context.Context, targetID, urlContains string) (*cdp.Client, *cdp.PageSession, cdp.TargetInfo, error) {
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

func collectNetworkRequests(ctx context.Context, client *cdp.Client, sessionID string, wait time.Duration, limit int, failedOnly bool) ([]networkRequest, bool, error) {
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
	for _, event := range client.DrainEvents() {
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

func collectConsoleMessages(ctx context.Context, client *cdp.Client, sessionID string, wait time.Duration, limit int, errorsOnly bool, typeSet map[string]bool) ([]consoleMessage, bool, error) {
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
	addEventMessages(client.DrainEvents())

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

func (a *app) newProtocolExecCommand() *cobra.Command {
	var params string
	var targetID string
	var urlContains string
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
				return a.render(ctx, fmt.Sprintf("%s ok", args[0]), map[string]any{
					"ok":         true,
					"scope":      "target",
					"method":     args[0],
					"target":     pageRow(target),
					"session_id": session.SessionID,
					"result":     result,
				})
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
			return a.render(ctx, fmt.Sprintf("%s ok", args[0]), map[string]any{
				"ok":     true,
				"scope":  "browser",
				"method": args[0],
				"result": result,
			})
		},
	}
	cmd.Flags().StringVar(&params, "params", "{}", "JSON params object for the CDP method")
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix for target-scoped execution")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text for target-scoped execution")
	return cmd
}

func (a *app) fetchProtocol(ctx context.Context) (cdp.Protocol, error) {
	opts, err := a.browserOptions(ctx)
	if err != nil {
		return cdp.Protocol{}, err
	}
	var protocolURL string
	allowOfficialFallback := true
	if opts.AutoConnect && !opts.ActiveProbe {
		if _, ok := a.runningRuntimeForOptions(ctx, opts); ok {
			protocolURL, err = browser.ResolveProtocolURL(ctx, opts)
		} else {
			err = nil
		}
	} else {
		protocolURL, err = browser.ResolveProtocolURL(ctx, opts)
		allowOfficialFallback = opts.BrowserURL == "" && !opts.ActiveProbe
	}
	if err != nil {
		return cdp.Protocol{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
		)
	}
	if protocolURL != "" {
		protocol, err := cdp.FetchProtocol(ctx, protocolURL)
		if err == nil {
			return protocol, nil
		}
		var httpErr cdp.ProtocolHTTPError
		if !allowOfficialFallback || !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
			return cdp.Protocol{}, commandError(
				"connection_failed",
				"connection",
				fmt.Sprintf("fetch protocol metadata: %v", err),
				ExitConnection,
				[]string{"cdp doctor --json", "cdp daemon status --json"},
			)
		}
	}
	protocol, err := cdp.FetchOfficialProtocol(ctx)
	if err != nil {
		return cdp.Protocol{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("fetch official protocol metadata: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp daemon status --json", "cdp protocol exec Browser.getVersion --json"},
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
	cmd.AddCommand(planned("console-errors", "Summarize console errors"))
	cmd.AddCommand(planned("network-failures", "Summarize failed network requests"))
	cmd.AddCommand(planned("perf <url>", "Capture and summarize performance evidence"))
	cmd.AddCommand(planned("a11y", "Run a focused accessibility workflow"))
	return cmd
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
			"cdp targets --browser-url <browser-url> --json",
		},
		"cdp pages": {
			"cdp pages --json",
			"cdp pages --limit 10 --json",
			"cdp pages --url-contains localhost --json",
			"cdp pages --browser-url <browser-url> --json",
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
		"cdp protocol metadata": {
			"cdp protocol metadata --json",
			"cdp protocol metadata --browser-url <browser-url> --json",
		},
		"cdp protocol domains": {
			"cdp protocol domains --json",
			"cdp protocol domains --experimental --json",
			"cdp protocol domains --browser-url <browser-url> --json",
		},
		"cdp protocol search": {
			"cdp protocol search screenshot --json",
			"cdp protocol search console --kind event --json",
		},
		"cdp protocol describe": {
			"cdp protocol describe Page.captureScreenshot --json",
		},
		"cdp protocol exec": {
			"cdp protocol exec Browser.getVersion --params '{}' --json",
			"cdp protocol exec Runtime.evaluate --target <target-id> --params '{\"expression\":\"document.title\",\"returnByValue\":true}' --json",
			"cdp protocol exec DOM.getDocument --url-contains localhost --json",
		},
		"cdp workflow verify": {
			"cdp workflow verify https://example.com --json",
		},
		"cdp workflow visible-posts": {
			"cdp workflow visible-posts https://x.com/<handle> --limit 5 --json",
			"cdp workflow visible-posts https://example.com/feed --selector article --wait 30s --json",
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
