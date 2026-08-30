package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/processgroup"
	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
)

func (a *app) newDaemonCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the long-running Chrome attach daemon",
		Long:  "Manage the long-running Chrome attach daemon. In --auto-connect headed/default-profile mode, browser access is human-in-the-loop: agents may inspect status, doctor, health, and logs, but should not retry start/restart/stop loops when permission is pending. In --browser-mode headless, cdp owns the managed browser profile and repair commands are noninteractive.",
	}
	cmd.AddCommand(a.newDaemonApproveCommand())
	cmd.AddCommand(a.newDaemonStartCommand())
	cmd.AddCommand(a.newDaemonStatusCommand())
	cmd.AddCommand(a.newDaemonStopCommand())
	cmd.AddCommand(a.newDaemonRestartCommand())
	cmd.AddCommand(a.newDaemonKeepaliveCommand())
	cmd.AddCommand(a.newDaemonMaintenanceCommand())
	cmd.AddCommand(a.newDaemonHealthCommand())
	cmd.AddCommand(a.newDaemonHealthCheckCommand())
	cmd.AddCommand(a.newDaemonHoldCommand())
	cmd.AddCommand(a.newDaemonLogsCommand())
	return cmd
}

type daemonStartConfig struct {
	prime             bool
	reconnect         time.Duration
	connectionName    string
	remember          bool
	managedKeepAlive  *managedKeepAlive
	skipSelectedApply bool
}

type daemonStartResult struct {
	human string
	data  map[string]any
}

type daemonStopResult struct {
	Runtime               daemon.Runtime            `json:"runtime"`
	DaemonStopped         bool                      `json:"daemon_stopped"`
	ManagedBrowserStopped bool                      `json:"managed_browser_stopped"`
	ManagedBrowser        browser.ManagedStopResult `json:"managed_browser"`
	RecoveryState         *managedRecoveryState     `json:"recovery_state,omitempty"`
}

type managedRecoveryState struct {
	ConnectionsRemoved      []string                `json:"connections_removed"`
	StaleLocks              daemon.StaleLockCleanup `json:"stale_locks"`
	RuntimeArtifactsCleared bool                    `json:"runtime_artifacts_cleared"`
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
	if !cfg.skipSelectedApply {
		if err := a.applySelectedConnection(ctx); err != nil {
			return daemonStartResult{}, err
		}
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
		if cfg.managedKeepAlive != nil {
			endpoint = cfg.managedKeepAlive.Endpoint
		} else {
			endpoint, err = a.browserEndpoint(ctx)
			if err != nil {
				return daemonStartResult{}, a.daemonStartError("resolve_browser_endpoint", err.Error(), map[string]any{"waiting_for_user_approval": a.opts.autoConnect})
			}
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
		r, reused, err := a.startKeepAlive(ctx, endpoint, cfg.managedKeepAlive, cfg.reconnect)
		if err != nil {
			return daemonStartResult{}, a.daemonStartError("start_keepalive", fmt.Sprintf("start daemon keepalive: %v", err), map[string]any{"waiting_for_user_approval": a.opts.autoConnect, "browser_endpoint_seen": endpoint != ""})
		}
		runtime = &r
		alreadyRunning = reused
	}

	status := a.daemonStatus(ctx, probe)
	if runtime != nil {
		status = daemon.WithRuntime(status, *runtime, true)
		health := a.browserHealthSnapshot(ctx, status, false)
		status.Health = health
		if cfg.managedKeepAlive != nil && !healthUsable(health) {
			code, _ := stringMapField(health, "code")
			if code == "" {
				code = "managed_headless_start_unhealthy"
			}
			nextCommands := uniqueCommands(toStringSlice(health["next_commands"]), a.connectionRemediationCommands())
			return daemonStartResult{}, commandErrorWithData(
				code,
				"connection",
				"managed headless daemon started but daemon RPC and browser CDP discovery did not become usable",
				ExitConnection,
				nextCommands,
				map[string]any{
					"browser_mode":      "headless",
					"human_required":    false,
					"agent_should_stop": false,
					"daemon":            status,
					"health":            status.Health,
				},
			)
		}
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

func (a *app) daemonStartError(phase, message string, details map[string]any) error {
	if a.browserModeName() == string(config.BrowserModeHeadless) {
		return commandErrorWithData(
			"managed_headless_start_failed",
			"connection",
			message,
			ExitConnection,
			a.connectionRemediationCommands(),
			map[string]any{
				"browser_mode":      "headless",
				"human_required":    false,
				"agent_should_stop": false,
				"daemon_start": map[string]any{
					"phase":   phase,
					"details": details,
				},
			},
		)
	}
	return commandErrorWithData(
		"permission_pending",
		"permission",
		message,
		ExitPermission,
		permissionRemediationCommands(),
		permissionPendingData(map[string]any{"daemon_start": map[string]any{"phase": phase, "details": details}}),
	)
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
		BrowserMode: a.browserModeName(),
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
		return commandErrorWithData(
			"permission_pending",
			"permission",
			probe.Message,
			ExitPermission,
			permissionRemediationCommands(),
			permissionPendingData(map[string]any{"daemon_start": map[string]any{"phase": "browser_probe", "waiting_for_user_approval": true}}),
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

func (a *app) startKeepAlive(ctx context.Context, endpoint string, managed *managedKeepAlive, reconnect time.Duration) (daemon.Runtime, bool, error) {
	metadata := daemon.KeepAliveMetadata{UserDataDir: a.opts.userDataDir}
	if managed != nil {
		metadata = daemon.KeepAliveMetadata{
			UserDataDir:            managed.Metadata.UserDataDir,
			ManagedBrowser:         managed.ManagedBrowser,
			ManagedProfilePath:     managed.Metadata.UserDataDir,
			ProfileSeedStrategy:    managed.Metadata.ProfileSeedStrategy,
			ChromePID:              managed.Metadata.ChromePID,
			ChromePort:             managed.Metadata.DebuggingPort,
			ChromeProcessStartTime: managed.Metadata.ProcessStartTime,
		}
	}
	return a.startKeepAliveFromEndpoint(ctx, endpoint, a.connectionMode(), metadata, reconnect)
}

func (a *app) startKeepAliveFromEndpoint(ctx context.Context, endpoint, connectionMode string, metadata daemon.KeepAliveMetadata, reconnect time.Duration) (daemon.Runtime, bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return daemon.Runtime{}, false, fmt.Errorf("resolve current executable: %w", err)
	}
	store, err := a.stateStore()
	if err != nil {
		return daemon.Runtime{}, false, err
	}
	return daemon.StartKeepAliveForModeWithMetadata(ctx, executable, store.Dir, a.browserModeName(), endpoint, connectionMode, metadata, reconnect)
}

func (a *app) newDaemonStopCommand() *cobra.Command {
	var forceManaged bool
	var staleLockAfter time.Duration
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the attach daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			if staleLockAfter < 0 {
				return commandError("invalid_duration", "usage", "--stale-lock-after must be non-negative", ExitUsage, []string{"cdp --browser-mode headless daemon stop --force-managed --stale-lock-after 10m --json"})
			}
			stop, err := a.stopSelectedRuntime(ctx, forceManaged, staleLockAfter)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("stop daemon: %v", err),
					ExitConnection,
					[]string{"cdp daemon status --json"},
				)
			}
			if forceManaged && a.browserModeName() == string(config.BrowserModeHeadless) && !stop.DaemonStopped && !stop.ManagedBrowserStopped {
				status, health, healthErr := a.selectedDaemonHealth(ctx)
				state := "unknown"
				if healthErr == nil {
					state = healthState(health)
				}
				if healthErr != nil || state != "healthy" {
					data := map[string]any{
						"state":                   "degraded",
						"browser_mode":            a.browserModeName(),
						"daemon_stopped":          stop.DaemonStopped,
						"managed_browser_stopped": stop.ManagedBrowserStopped,
						"managed_browser":         stop.ManagedBrowser,
						"recovery_state":          stop.RecoveryState,
						"runtime":                 stop.Runtime,
						"daemon":                  status,
						"health":                  health,
						"next_commands":           []string{"cdp --browser-mode headless daemon status --json", "cdp --browser-mode headless daemon keepalive --repair --force --json"},
					}
					return commandErrorWithData(
						"managed_headless_cleanup_failed",
						"connection",
						"no managed headless daemon or Chrome process was reclaimed and the runtime is still not healthy",
						ExitConnection,
						[]string{"cdp --browser-mode headless daemon status --json", "cdp --browser-mode headless daemon keepalive --repair --force --json"},
						data,
					)
				}
			}
			human := "daemon was not running"
			if stop.DaemonStopped {
				human = fmt.Sprintf("daemon process %d stopped", stop.Runtime.PID)
			}
			return a.render(ctx, human, map[string]any{
				"ok":                      true,
				"browser_mode":            a.browserModeName(),
				"stopped":                 stop.DaemonStopped,
				"daemon_stopped":          stop.DaemonStopped,
				"managed_browser_stopped": stop.ManagedBrowserStopped,
				"managed_browser":         stop.ManagedBrowser,
				"recovery_state":          stop.RecoveryState,
				"runtime":                 stop.Runtime,
			})
		},
	}
	cmd.Flags().BoolVar(&forceManaged, "force-managed", false, "for --browser-mode headless, reclaim cdp-owned managed Chrome even when ownership metadata is incomplete")
	cmd.Flags().DurationVar(&staleLockAfter, "stale-lock-after", 10*time.Minute, "with --force-managed, remove eligible inactive headless recovery locks older than this duration; 0 disables age cleanup")
	return cmd
}

func (a *app) stopSelectedRuntime(ctx context.Context, forceManaged bool, staleLockAfter time.Duration) (daemonStopResult, error) {
	store, err := a.stateStore()
	if err != nil {
		return daemonStopResult{}, err
	}
	runtime, daemonStopped, err := daemon.StopRuntimeForMode(ctx, store.Dir, a.browserModeName())
	if err != nil {
		return daemonStopResult{}, err
	}
	result := daemonStopResult{Runtime: runtime, DaemonStopped: daemonStopped}
	if a.browserModeName() == "headless" {
		managedStop, err := browser.StopManagedChrome(ctx, store.Dir, browser.ManagedStopOptions{Force: forceManaged})
		if err != nil {
			return result, err
		}
		result.ManagedBrowser = managedStop
		result.ManagedBrowserStopped = managedStop.Stopped
		if forceManaged {
			cleanup, err := a.clearManagedHeadlessRecoveryState(ctx, store.Dir, staleLockAfter)
			if err != nil {
				return result, err
			}
			result.RecoveryState = &cleanup
		}
	}
	return result, nil
}

func (a *app) clearManagedHeadlessRecoveryState(ctx context.Context, stateDir string, staleLockAfter time.Duration) (managedRecoveryState, error) {
	result := managedRecoveryState{ConnectionsRemoved: []string{}}
	store := state.Store{Dir: stateDir}
	file, err := store.Load(ctx)
	if err != nil {
		return result, err
	}
	for _, connection := range append([]state.Connection(nil), file.Connections...) {
		if connection.BrowserMode != "headless" || connection.Mode != "browser_url" || connection.Project != "" {
			continue
		}
		file, _ = state.RemoveConnection(file, connection.Name)
		result.ConnectionsRemoved = append(result.ConnectionsRemoved, connection.Name)
	}
	if len(result.ConnectionsRemoved) > 0 {
		if err := store.Save(ctx, file); err != nil {
			return result, err
		}
	}
	locks, err := daemon.RemoveStaleLocks(ctx, stateDir, staleLockAfter,
		"daemon-keepalive-headless-",
		"daemon-health-check-headless",
		"keepalive-headless",
		"headless-",
	)
	if err != nil {
		return result, err
	}
	result.StaleLocks = locks
	if err := browser.ClearManagedRuntimeArtifacts(stateDir); err != nil {
		return result, err
	}
	result.RuntimeArtifactsCleared = true
	return result, nil
}

func (a *app) newDaemonHealthCommand() *cobra.Command {
	var processInfo bool
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Show safe daemon/browser health telemetry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			probe, err := a.browserProbe(ctx)
			if err != nil {
				return commandError("invalid_browser_url", "usage", err.Error(), ExitUsage, []string{"cdp daemon health --browser-url <browser-url> --json"})
			}
			status := a.daemonStatus(ctx, probe)
			health := a.browserHealthSnapshot(ctx, status, processInfo)
			status.Health = health
			if a.browserModeName() == string(config.BrowserModeHeadless) && healthState(health) != "healthy" && !healthUsable(health) {
				code, _ := stringMapField(health, "code")
				if code == "" {
					code = "headless_runtime_degraded"
				}
				nextCommands := uniqueCommands(toStringSlice(health["next_commands"]), a.connectionRemediationCommands())
				data := map[string]any{
					"state":         healthState(health),
					"daemon":        status,
					"health":        health,
					"next_commands": nextCommands,
				}
				return commandErrorWithData(
					code,
					"connection",
					fmt.Sprintf("headless daemon health is %s", healthState(health)),
					ExitCheckFailed,
					nextCommands,
					data,
				)
			}
			return a.render(ctx, fmt.Sprintf("daemon-health\t%s", health["state"]), map[string]any{"ok": true, "daemon": status, "health": health})
		},
	}
	cmd.Flags().BoolVar(&processInfo, "process-info", false, "include optional SystemInfo.getProcessInfo process counts when a daemon runtime is healthy")
	return cmd
}

func healthUsable(health map[string]any) bool {
	usable, ok := health["usable"].(bool)
	return ok && usable
}

const defaultHeadlessHealthCheckURL = "data:text/html,%3Cmain%20data-cdp-health%3D%22ok%22%3Ecdp-headless-health%3C%2Fmain%3E"

type daemonHealthCheckOptions struct {
	Repair              bool
	Force               bool
	ManagedProcessSweep bool
	RequireHealthy      bool
	HealthURL           string
	OutDir              string
	FailureThreshold    int
	LockTimeout         time.Duration
	StaleLockAfter      time.Duration
	Reconnect           time.Duration
	ChromeCommand       string
}

func (a *app) newDaemonHealthCheckCommand() *cobra.Command {
	opts := daemonHealthCheckOptions{
		HealthURL:        defaultHeadlessHealthCheckURL,
		FailureThreshold: 3,
		StaleLockAfter:   10 * time.Minute,
		Reconnect:        30 * time.Second,
		ChromeCommand:    defaultChromeCommand(),
	}
	cmd := &cobra.Command{
		Use:   "health-check",
		Short: "Repair and validate the managed headless daemon runtime",
		Long:  "Repair and validate the managed headless daemon runtime. The synthetic health-check page is workflow-owned and reports bounded exact-target cleanup with target-gone evidence or a recovery command. When --repair or --managed-process-sweep is enabled, Auto Heal first checks internet reachability and a persisted awake observation; offline or post-wake hosts receive a safe structured skip without launching Chrome. Managed repair also inventories exact superseded daemon holds without touching the current generation.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.FailureThreshold <= 0 || opts.LockTimeout < 0 || opts.StaleLockAfter < 0 || opts.Reconnect < 0 {
				return commandError("invalid_argument", "usage", "--failure-threshold must be positive and durations must be non-negative", ExitUsage, []string{"cdp --browser-mode headless daemon health-check --repair --json"})
			}
			if a.browserModeName() != string(config.BrowserModeHeadless) {
				return commandError("invalid_browser_mode", "usage", "daemon health-check is only supported for --browser-mode headless", ExitUsage, []string{"cdp --browser-mode headless daemon health-check --repair --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 90*time.Second)
			defer cancel()
			return a.runDaemonHealthCheck(ctx, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.Repair, "repair", false, "start or replace the managed headless daemon before validation when health is not healthy")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "when used with --repair, clear stale managed headless runtime state before relaunching")
	cmd.Flags().BoolVar(&opts.ManagedProcessSweep, "managed-process-sweep", false, "run managed headless process and orphaned daemon-hold reconciliation before launch-capable repair work")
	cmd.Flags().BoolVar(&opts.RequireHealthy, "require-healthy", false, "fail when health is usable but degraded instead of returning a warning success")
	cmd.Flags().StringVar(&opts.HealthURL, "health-url", opts.HealthURL, "synthetic URL used for navigation/DOM/JS/screenshot validation")
	cmd.Flags().StringVar(&opts.OutDir, "out-dir", "", "directory for health-check JSON and screenshot artifacts; defaults under the cdp state directory")
	cmd.Flags().IntVar(&opts.FailureThreshold, "failure-threshold", opts.FailureThreshold, "write a feature-request candidate after this many consecutive failures")
	cmd.Flags().DurationVar(&opts.LockTimeout, "lock-timeout", opts.LockTimeout, "how long to wait for another health-check lock; 0s skips immediately")
	cmd.Flags().DurationVar(&opts.StaleLockAfter, "stale-lock-after", opts.StaleLockAfter, "remove a health-check lock older than this duration; 0 disables stale cleanup")
	cmd.Flags().DurationVar(&opts.Reconnect, "reconnect", opts.Reconnect, "daemon reconnect interval to use when --repair starts the managed runtime")
	cmd.Flags().StringVar(&opts.ChromeCommand, "chrome-command", opts.ChromeCommand, "Chrome command for managed headless repair; empty disables launch")
	return cmd
}

func (a *app) runDaemonHealthCheck(ctx context.Context, opts daemonHealthCheckOptions) error {
	human, report, err := a.runDaemonHealthCheckReport(ctx, opts)
	if err != nil {
		return err
	}
	return a.render(ctx, human, report)
}

func (a *app) runDaemonHealthCheckReport(ctx context.Context, opts daemonHealthCheckOptions) (string, map[string]any, error) {
	store, err := a.stateStore()
	if err != nil {
		return "", nil, err
	}
	outDir := strings.TrimSpace(opts.OutDir)
	if outDir == "" {
		outDir = filepath.Join(store.Dir, "headless-health")
	}
	lockName := "daemon-health-check-headless"
	lock, acquired, existingLock, err := daemon.AcquireLock(ctx, store.Dir, lockName, opts.LockTimeout, opts.StaleLockAfter, daemon.LockMetadata{Name: lockName, Phase: "checking"})
	if err != nil {
		return "", nil, commandError("lock_failed", "connection", fmt.Sprintf("acquire health-check lock: %v", err), ExitConnection, []string{"cdp --browser-mode headless daemon health --json"})
	}
	if !acquired {
		return "headless health-check locked", map[string]any{
			"ok":               true,
			"browser_mode":     a.browserModeName(),
			"state":            "locked",
			"status":           "skipped",
			"usable":           false,
			"degraded_reasons": []string{},
			"action":           "skipped",
			"locked":           true,
			"lock":             existingLock,
			"next_commands": []string{
				"cdp --browser-mode headless daemon health --json",
				"cdp cron status --json",
			},
		}, nil
	}
	defer lock.Release()

	runID := time.Now().UTC().Format("20060102T150405Z")
	runDir := filepath.Join(outDir, runID)
	summaryPath := filepath.Join(outDir, "latest.json")
	screenshotPath := filepath.Join(runDir, "screenshot.png")
	steps := []map[string]any{}
	report := map[string]any{
		"ok":               false,
		"browser_mode":     a.browserModeName(),
		"state":            "failed",
		"status":           "fail",
		"usable":           false,
		"action":           "diagnosed",
		"run_id":           runID,
		"locked":           false,
		"lock":             map[string]any{"name": lock.Metadata.Name, "acquired": true},
		"steps":            steps,
		"degraded_reasons": []string{},
		"artifacts": map[string]any{
			"run_dir":    runDir,
			"summary":    summaryPath,
			"screenshot": screenshotPath,
		},
		"next_commands": headlessHealthCheckNextCommands(),
	}
	if opts.ManagedProcessSweep {
		report["managed_process_sweep"] = map[string]any{
			"requested": true,
			"phase":     cronManagedProcessSweepPhaseName,
			"state":     "pending",
		}
	}
	var ownedTarget *headlessHealthCheckTarget
	fail := func(failure string, cause error) (string, map[string]any, error) {
		if cause != nil {
			report["error"] = cause.Error()
		}
		report["failure"] = failure
		if ownedTarget != nil {
			report["cleanup"] = ownedTarget.close()
		}
		count := a.updateHeadlessHealthCheckFailure(ctx, outDir, runDir, summaryPath, opts.FailureThreshold, true)
		report["failure_count"] = count
		_ = writeJSONArtifact(summaryPath, report)
		primary := commandErrorWithData("headless_health_check_failed", "check_failed", fmt.Sprintf("headless health-check failed: %s", failure), ExitCheckFailed, headlessHealthCheckNextCommands(), report)
		if cleanup, ok := report["cleanup"].(renderedExtractCleanupResult); ok && cleanup.Error != "" {
			return "", report, workflowOwnedCleanupError("headless_health_check", primary, cleanup, headlessHealthCheckNextCommands())
		}
		return "", report, primary
	}
	addStep := func(name string, ok bool, fields map[string]any) {
		step := map[string]any{"name": name, "ok": ok}
		for key, value := range fields {
			step[key] = value
		}
		steps = append(steps, step)
		report["steps"] = steps
	}

	if opts.Repair || opts.ManagedProcessSweep {
		environment, releaseAutoHealLease, environmentErr := a.checkAndAcquireAutoHealEnvironment(ctx, store.Dir)
		if environmentErr != nil {
			report["environment"] = autoHealEnvironmentFailure(environmentErr)
			report["failure"] = "environment_check_failed"
			_ = writeJSONArtifact(summaryPath, report)
			return "", report, commandErrorWithData(
				"auto_heal_environment_unavailable",
				"connection",
				"Auto Heal environment check failed; no browser repair was attempted",
				ExitConnection,
				autoHealEnvironmentNextCommands("headless"),
				report,
			)
		}
		report["environment"] = environment
		if !environment.Allowed {
			report["ok"] = true
			report["state"] = "environment_unavailable"
			report["status"] = "skipped"
			report["action"] = "skipped"
			report["next_commands"] = uniqueCommands(headlessHealthCheckNextCommands(), autoHealEnvironmentNextCommands("headless"))
			if err := writeJSONArtifact(summaryPath, report); err != nil {
				return "", report, commandErrorWithData(
					"artifact_write_failed",
					"internal",
					"write health-check summary: "+err.Error(),
					ExitInternal,
					headlessHealthCheckNextCommands(),
					report,
				)
			}
			return "headless-health-check\tenvironment-unavailable", report, nil
		}
		if releaseAutoHealLease != nil {
			defer func() { _ = releaseAutoHealLease() }()
		}
	}

	status, health, err := a.selectedDaemonHealth(ctx)
	if opts.ManagedProcessSweep {
		sweep, sweepErr := a.runManagedProcessSweep(ctx, store.Dir, lock, status)
		if sweepErr != nil {
			report["managed_process_sweep"] = map[string]any{"requested": true, "phase": cronManagedProcessSweepPhaseName, "state": "error", "error": sweepErr.Error()}
			return fail("managed_process_sweep_failed", sweepErr)
		}
		report["managed_process_sweep"] = sweep
		daemon.AppendLogForMode(ctx, store.Dir, a.browserModeName(), daemon.LogEntry{
			Level:   "info",
			Event:   "managed_process_history_compacted",
			Message: fmt.Sprintf("managed process history reconciled: retained=%d compacted=%d", sweep.HistoricalProcesses.Retained, sweep.HistoricalProcesses.Compacted),
		})
		status, health, err = a.selectedDaemonHealth(ctx)
	}
	report["daemon"] = status
	report["health"] = health
	resourcePreflight := a.maintenanceResourcePreflightForState(ctx, store.Dir, status, health)
	report["resource_preflight"] = resourcePreflight
	skipForResources := func() (string, map[string]any, error) {
		report["ok"] = true
		report["state"] = "resource_blocked"
		report["status"] = "skipped"
		report["usable"] = false
		report["action"] = "skipped"
		report["next_commands"] = uniqueCommands(headlessHealthCheckNextCommands(), resourcePreflight.NextCommands)
		_ = writeJSONArtifact(summaryPath, report)
		return "headless-health-check\tresource-blocked", report, nil
	}
	if err != nil {
		addStep("health", false, map[string]any{"error": err.Error()})
		if !opts.Repair {
			return fail("health_failed", err)
		}
	} else {
		addStep("health", healthState(health) == "healthy" || healthUsable(health), map[string]any{"state": healthState(health), "usable": healthUsable(health)})
	}

	if (err != nil || !healthUsable(health)) && opts.Repair {
		if !resourcePreflight.HeavyWorkAllowed {
			addStep("resource_preflight", false, map[string]any{"state": resourcePreflight.State, "status": resourcePreflight.Status})
			return skipForResources()
		}
		if err := lock.Update(ctx, "repairing"); err != nil {
			return "", report, err
		}
		repair, err := a.repairManagedHeadlessForHealthCheck(ctx, store.Dir, opts, lock, status, health)
		addStep("repair", err == nil, map[string]any{"repair": repair})
		if err != nil {
			return fail("repair_failed", err)
		}
		report["repair"] = repair
		status, health, err = a.selectedDaemonHealth(ctx)
		if err != nil {
			addStep("health_after_repair", false, map[string]any{"error": err.Error()})
			return fail("health_after_repair_failed", err)
		}
		report["daemon"] = status
		report["health"] = health
		resourcePreflight = a.maintenanceResourcePreflightForState(ctx, store.Dir, status, health)
		report["resource_preflight"] = resourcePreflight
		addStep("health_after_repair", healthState(health) == "healthy" || healthUsable(health), map[string]any{"state": healthState(health), "usable": healthUsable(health)})
	}
	if healthState(health) != "healthy" && !healthUsable(health) {
		return fail("health_not_healthy", nil)
	}
	if opts.RequireHealthy && healthState(health) != "healthy" {
		report["usable"] = healthUsable(health)
		report["degraded_reasons"] = toStringSlice(health["degraded_reasons"])
		return fail("health_not_healthy", nil)
	}
	if !resourcePreflight.HeavyWorkAllowed {
		addStep("resource_preflight", false, map[string]any{"state": resourcePreflight.State, "status": resourcePreflight.Status})
		return skipForResources()
	}

	ownedTarget, err = a.openHealthCheckTarget(ctx, opts.HealthURL)
	if err != nil {
		addStep("open", false, map[string]any{"error": err.Error()})
		return fail("navigate_failed", err)
	}
	report["target"] = pageRow(ownedTarget.target)
	addStep("open", true, map[string]any{"target_id": ownedTarget.target.TargetID})

	js, jsAttempts, err := evaluateHeadlessHealthCheckJavaScript(ctx, ownedTarget.session)
	if err != nil {
		addStep("javascript", false, map[string]any{"error": err.Error()})
		return fail("javascript_failed", err)
	}
	addStep("javascript", js.OK, map[string]any{"text": js.Text, "attempts": jsAttempts})
	if !js.OK {
		return fail("javascript_unexpected_result", nil)
	}

	var text textResult
	if err := evaluateJSONValue(ctx, ownedTarget.session, textExpression("body", 1, 1), "headless health-check text", &text); err != nil {
		addStep("dom_text", false, map[string]any{"error": err.Error()})
		return fail("dom_text_failed", err)
	}
	addStep("dom_text", text.Count > 0, map[string]any{"count": text.Count})
	if text.Count == 0 {
		return fail("dom_text_empty", nil)
	}

	shot, err := ownedTarget.session.CaptureScreenshot(ctx, cdp.ScreenshotOptions{Format: "png"})
	if err != nil {
		addStep("screenshot", false, map[string]any{"error": err.Error()})
		return fail("screenshot_failed", err)
	}
	writtenScreenshot, err := writeArtifactFile(screenshotPath, shot.Data)
	if err != nil {
		addStep("screenshot", false, map[string]any{"error": err.Error()})
		return fail("screenshot_write_failed", err)
	}
	addStep("screenshot", true, map[string]any{"path": writtenScreenshot, "bytes": len(shot.Data)})

	cleanup := ownedTarget.close()
	report["cleanup"] = cleanup
	if cleanup.Error != "" {
		return fail("cleanup_failed", fmt.Errorf("workflow-owned health-check page cleanup failed: %s", cleanup.Error))
	}
	report["ok"] = true
	report["usable"] = true
	if healthState(health) == "degraded" && healthUsable(health) {
		report["state"] = "usable_degraded"
		report["status"] = "warn"
		report["degraded_reasons"] = toStringSlice(health["degraded_reasons"])
		report["recommended_action"] = "safe_for_single_command_but_repair_before_long_crawl"
	} else {
		report["state"] = "healthy"
		report["status"] = "pass"
		report["degraded_reasons"] = []string{}
	}
	report["action"] = "validated"
	report["failure"] = nil
	report["failure_count"] = a.updateHeadlessHealthCheckFailure(ctx, outDir, runDir, summaryPath, opts.FailureThreshold, false)
	if err := lock.Update(ctx, "healthy"); err != nil {
		return "", report, err
	}
	if err := writeJSONArtifact(summaryPath, report); err != nil {
		return "", report, err
	}
	daemon.AppendLogForMode(ctx, store.Dir, a.browserModeName(), daemon.LogEntry{
		Level:   "info",
		Event:   "health_check_validated",
		Message: "managed headless health-check passed",
	})
	return fmt.Sprintf("headless-health-check\t%s", report["state"]), report, nil
}

func (a *app) selectedDaemonHealth(ctx context.Context) (daemon.Status, map[string]any, error) {
	probe, err := a.browserProbe(ctx)
	if err != nil {
		probe = browser.ProbeResult{
			State:               "probe_failed",
			Message:             err.Error(),
			ConnectionMode:      a.connectionMode(),
			RemediationCommands: headlessHealthCheckNextCommands(),
		}
		status := a.daemonStatus(ctx, probe)
		health := a.browserHealthSnapshot(ctx, status, false)
		status.Health = health
		return status, health, err
	}
	status := a.daemonStatus(ctx, probe)
	health := a.browserHealthSnapshot(ctx, status, false)
	status.Health = health
	return status, health, nil
}

func (a *app) repairManagedHeadlessForHealthCheck(ctx context.Context, storeDir string, opts daemonHealthCheckOptions, lock daemon.LockHandle, status daemon.Status, health map[string]any) (map[string]any, error) {
	probeResult := map[string]any{
		"mode":                            "health-check",
		"result":                          healthFailureResult(health),
		"repair_requested":                true,
		"force_requested":                 opts.Force,
		"managed_process_sweep_requested": opts.ManagedProcessSweep,
	}
	connectionName := a.connectionStateName(ctx)
	mode := strings.TrimSpace(status.ConnectionMode)
	if mode == "" {
		mode = a.connectionMode()
	}
	human, keepalive, err := a.runHeadlessKeepaliveStartOrRepair(ctx, storeDir, lock, connectionName, mode, opts.Reconnect, opts.ChromeCommand, opts.Force, opts.ManagedProcessSweep, opts.Repair || opts.ManagedProcessSweep, opts.StaleLockAfter, status, probeResult, map[string]any{
		"ok":            false,
		"result":        healthFailureResult(health),
		"runtime_state": status.State,
	})
	repair := map[string]any{
		"repair_source":  "daemon_keepalive",
		"previous_state": status.State,
		"classification": keepaliveRepairClassification(keepalive, err),
		"keepalive":      keepalive,
	}
	if human != "" {
		repair["human"] = human
	}
	if state, ok := stringMapField(keepalive, "state"); ok {
		repair["state"] = state
	}
	if action, ok := stringMapField(keepalive, "action"); ok {
		repair["action"] = action
	}
	if err != nil {
		return repair, err
	}
	state, _ := stringMapField(keepalive, "state")
	switch state {
	case "healthy", "started", "repaired":
		return repair, nil
	case "locked":
		return repair, fmt.Errorf("headless keepalive repair is locked")
	default:
		if state == "" {
			state = "unknown"
		}
		return repair, fmt.Errorf("headless keepalive repair ended in state %s", state)
	}
}

type headlessHealthCheckTarget struct {
	closeClient  func(context.Context) error
	target       cdp.TargetInfo
	session      *cdp.PageSession
	cleanupGuard *renderedExtractCleanupGuard
	once         sync.Once
	cleanup      renderedExtractCleanupResult
}

func (h *headlessHealthCheckTarget) close() renderedExtractCleanupResult {
	if h == nil {
		return renderedExtractCleanupResult{Skipped: true, Reason: "not_registered"}
	}
	h.once.Do(func() {
		h.cleanup = h.cleanupGuard.cleanup()
		if h.session != nil {
			_ = h.session.Close(context.Background())
		} else if h.closeClient != nil {
			_ = h.closeClient(context.Background())
		}
	})
	return h.cleanup
}

func (a *app) openHealthCheckTarget(ctx context.Context, rawURL string) (*headlessHealthCheckTarget, error) {
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return nil, err
	}
	closeClientOnce := idempotentContextCloser(closeClient)
	targetID, err := a.createWorkflowPageTarget(ctx, client, rawURL, "headless-health-check")
	if err != nil {
		_ = closeClientOnce(context.Background())
		return nil, err
	}
	handle := &headlessHealthCheckTarget{
		closeClient: closeClientOnce,
		target:      cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL},
		cleanupGuard: &renderedExtractCleanupGuard{
			client:   client,
			targetID: targetID,
			owned:    true,
		},
	}
	target, err := cdp.TargetInfoWithClient(ctx, client, targetID)
	if err != nil {
		return handle, err
	}
	handle.target = target
	handle.cleanupGuard.targetID = target.TargetID
	session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClientOnce)
	if err != nil {
		return handle, err
	}
	handle.session = session
	return handle, nil
}

func headlessHealthCheckExpression() string {
	return `(() => {
  const marker = "__cdp_cli_headless_health_check__";
  const text = String(document.querySelector("[data-cdp-health]")?.textContent || "");
  return { ok: text === "cdp-headless-health", text, marker };
})()`
}

type headlessHealthCheckJSResult struct {
	OK   bool   `json:"ok"`
	Text string `json:"text"`
}

func evaluateHeadlessHealthCheckJavaScript(ctx context.Context, session *cdp.PageSession) (headlessHealthCheckJSResult, int, error) {
	deadline := time.Now().Add(5 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	var last headlessHealthCheckJSResult
	var lastErr error
	attempts := 0
	for {
		attempts++
		var current headlessHealthCheckJSResult
		err := evaluateJSONValue(ctx, session, headlessHealthCheckExpression(), "headless health-check javascript", &current)
		if err == nil {
			last = current
			lastErr = nil
			if current.OK {
				return current, attempts, nil
			}
		} else {
			lastErr = err
		}
		if !time.Now().Before(deadline) {
			if lastErr != nil {
				return last, attempts, lastErr
			}
			return last, attempts, nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, attempts, ctx.Err()
		case <-timer.C:
		}
	}
}

func headlessHealthCheckNextCommands() []string {
	return []string{
		"cdp --browser-mode headless daemon health --json",
		"cdp --browser-mode headless daemon logs --tail 50 --json",
		"cdp --browser-mode headless daemon keepalive --repair --json",
		"cdp cron status --json",
	}
}

func healthState(health map[string]any) string {
	return fmt.Sprint(health["state"])
}

func healthFailureResult(health map[string]any) string {
	if code, _ := stringMapField(health, "code"); code != "" {
		return code
	}
	return healthState(health)
}

func (a *app) updateHeadlessHealthCheckFailure(ctx context.Context, outDir, runDir, summaryPath string, threshold int, failed bool) int {
	countPath := filepath.Join(outDir, "failure-count")
	count := 0
	if failed {
		if raw, err := os.ReadFile(countPath); err == nil {
			fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &count)
		}
		count++
	} else {
		count = 0
	}
	_ = os.MkdirAll(outDir, 0o700)
	_ = os.WriteFile(countPath, []byte(fmt.Sprintf("%d\n", count)), 0o600)
	if failed && count >= threshold {
		_ = writeHeadlessHealthCheckCandidate(filepath.Join(outDir, "feature-request-candidate.md"), count, runDir, summaryPath)
	}
	return count
}

func writeHeadlessHealthCheckCandidate(path string, count int, runDir, summaryPath string) error {
	body := fmt.Sprintf(`# Investigate Repeated Headless Health-Check Failures

## Problem

The cron-compatible headless health check failed %d consecutive times.

## Current Behaviour

- Run artifacts: %s
- Summary: %s

## Proposed Solution

Inspect the diagnostics, identify whether launch, daemon RPC, navigation, DOM/JS, or screenshot capture failed, then convert this candidate into a managed feature request.
`, count, runDir, summaryPath)
	_, err := writeArtifactFile(path, []byte(body))
	return err
}

func writeJSONArtifact(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return commandError("artifact_write_failed", "internal", fmt.Sprintf("marshal health-check summary: %v", err), ExitInternal, []string{"cdp --browser-mode headless daemon health-check --json"})
	}
	payload = append(payload, '\n')
	_, err = writeArtifactFile(path, payload)
	return err
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
			entries, err := daemon.ReadLogsForMode(ctx, store.Dir, a.browserModeName(), tail)
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
				"ok":           true,
				"browser_mode": a.browserModeName(),
				"log":          map[string]any{"path": daemon.RuntimeLogPathForMode(store.Dir, a.browserModeName()), "tail": tail, "count": len(entries)},
				"entries":      entries,
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
	var forceManaged bool
	var staleLockAfter time.Duration

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the attach daemon and reconnect through the daemon gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 60*time.Second)
			defer cancel()

			if staleLockAfter < 0 {
				return commandError("invalid_duration", "usage", "--stale-lock-after must be non-negative", ExitUsage, []string{"cdp --browser-mode headless daemon restart --force-managed --stale-lock-after 10m --json"})
			}
			stop, err := a.stopSelectedRuntime(ctx, forceManaged, staleLockAfter)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("stop daemon before restart: %v", err),
					ExitConnection,
					[]string{"cdp daemon status --json", "cdp daemon stop --json"},
				)
			}

			if a.browserModeName() == string(config.BrowserModeHeadless) {
				result, err := a.runHeadlessDaemonRestart(ctx, stop, reconnect, connectionName, remember)
				if err != nil {
					return err
				}
				return a.render(ctx, result.human, result.data)
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
				"stopped":                 stop.DaemonStopped,
				"daemon_stopped":          stop.DaemonStopped,
				"managed_browser_stopped": stop.ManagedBrowserStopped,
				"managed_browser":         stop.ManagedBrowser,
			}
			if stop.Runtime.PID > 0 {
				restart["previous_runtime"] = stop.Runtime
			}
			result.data["restart"] = restart
			if stop.DaemonStopped {
				result.human = fmt.Sprintf("daemon process %d stopped\n%s", stop.Runtime.PID, result.human)
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
	cmd.Flags().BoolVar(&forceManaged, "force-managed", false, "for --browser-mode headless, reclaim cdp-owned managed Chrome even when ownership metadata is incomplete")
	cmd.Flags().DurationVar(&staleLockAfter, "stale-lock-after", 10*time.Minute, "with --force-managed, remove eligible inactive headless recovery locks older than this duration; 0 disables age cleanup")
	return cmd
}

func (a *app) runHeadlessDaemonRestart(ctx context.Context, stop daemonStopResult, reconnect time.Duration, connectionName string, remember bool) (daemonStartResult, error) {
	if reconnect < 0 {
		return daemonStartResult{}, commandError(
			"invalid_reconnect_interval",
			"usage",
			"--reconnect must be a non-negative duration",
			ExitUsage,
			[]string{"cdp --browser-mode headless daemon restart --reconnect 30s --json"},
		)
	}
	store, err := a.stateStore()
	if err != nil {
		return daemonStartResult{}, err
	}
	if strings.TrimSpace(connectionName) == "" || connectionName == "default" {
		connectionName = defaultConnectionNameForBrowserMode("headless")
	}
	managed, chrome, err := a.ensureManagedChromeForKeepalive(ctx, store.Dir, defaultChromeCommand())
	if err != nil {
		return daemonStartResult{}, commandErrorWithData(
			"chrome_start_failed",
			"connection",
			fmt.Sprintf("start managed headless Chrome: %v", err),
			ExitConnection,
			[]string{
				"cdp --browser-mode headless browser profile status --json",
				"cdp --browser-mode headless daemon keepalive --repair --json",
			},
			map[string]any{"browser_mode": "headless", "human_required": false, "agent_should_stop": false, "chrome": chrome},
		)
	}
	a.opts.browserURL = managedHTTPURL(managed.Endpoint)
	a.opts.autoConnect = false
	a.opts.userDataDir = managed.Metadata.UserDataDir

	result, err := a.runDaemonStart(ctx, daemonStartConfig{
		reconnect:         reconnect,
		connectionName:    connectionName,
		remember:          remember,
		managedKeepAlive:  managed,
		skipSelectedApply: true,
	})
	if err != nil {
		return daemonStartResult{}, err
	}
	restart := map[string]any{
		"stopped":                 stop.DaemonStopped,
		"daemon_stopped":          stop.DaemonStopped,
		"managed_browser_stopped": stop.ManagedBrowserStopped,
		"managed_browser":         stop.ManagedBrowser,
		"recovery_state":          stop.RecoveryState,
		"managed_restart":         true,
		"chrome":                  chrome,
	}
	if stop.Runtime.PID > 0 {
		restart["previous_runtime"] = stop.Runtime
	}
	result.data["restart"] = restart
	result.data["chrome"] = chrome
	if stop.DaemonStopped {
		result.human = fmt.Sprintf("daemon process %d stopped\n%s", stop.Runtime.PID, result.human)
	} else {
		result.human = fmt.Sprintf("daemon was not running\n%s", result.human)
	}
	return result, nil
}

type keepaliveChromeStatus struct {
	Display        string                      `json:"display,omitempty"`
	Command        string                      `json:"command,omitempty"`
	Args           []string                    `json:"args,omitempty"`
	Checked        bool                        `json:"checked"`
	Running        bool                        `json:"running"`
	Launched       bool                        `json:"launched"`
	Skipped        bool                        `json:"skipped"`
	Reason         string                      `json:"reason,omitempty"`
	Attempts       int                         `json:"attempts,omitempty"`
	MaxAttempts    int                         `json:"max_attempts,omitempty"`
	AttemptErrors  []string                    `json:"attempt_errors,omitempty"`
	ManagedBrowser *browser.ManagedStatus      `json:"managed_browser,omitempty"`
	Window         *browser.HeadedWindowResult `json:"window,omitempty"`
}

type managedKeepAlive struct {
	Endpoint       string
	Metadata       browser.ManagedMetadata
	ManagedBrowser *browser.ManagedStatus
}

func (a *app) newDaemonKeepaliveCommand() *cobra.Command {
	var reconnect time.Duration
	var lockTimeout time.Duration
	var staleLockAfter time.Duration
	var probeMode string
	var display string
	var chromeCommand string
	var chromeArgs []string
	var repair bool
	var force bool
	var managedProcessSweep bool
	var macOSSelfHealApproval bool

	cmd := &cobra.Command{
		Use:   "keepalive",
		Short: "Idempotently keep the daemon healthy for cron",
		Long:  "Idempotently keep the daemon healthy for cron. Launch-capable Auto Heal checks internet reachability and a persisted awake observation before it can activate headed Chrome, request remote-debugging approval, or start managed headless Chrome; offline and post-wake hosts return a safe structured skip. Headless --repair inventories and safely reclaims exact superseded detached daemon holds; superseded hold generations retire without poisoning current health, and transient endpoint failures remain retryable.",
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
			browserMode := a.browserModeName()
			connectionName := a.connectionStateName(ctx)
			mode := a.connectionMode()
			lockName := "daemon-keepalive-" + browserMode + "-" + mode + "-" + connectionName
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
					"ok":           true,
					"browser_mode": browserMode,
					"connection":   connectionName,
					"mode":         mode,
					"state":        "locked",
					"action":       "skipped",
					"locked":       true,
					"lock":         existingLock,
				})
			}
			defer lock.Release()

			// Every auto-connect headed keepalive and every headless keepalive can
			// reach launch-capable repair code. Check the host before probing in a
			// way that could activate Chrome or ask for remote-debugging approval.
			// Browser-URL mode is an explicit endpoint operation and does not own
			// Chrome lifecycle, so it remains available for diagnostics.
			launchCapable := browserMode == "headless" || (browserMode == "headed" && a.opts.autoConnect)
			if launchCapable {
				environment, releaseAutoHealLease, environmentErr := a.checkAndAcquireAutoHealEnvironment(ctx, store.Dir)
				if environmentErr != nil {
					return commandErrorWithData(
						"auto_heal_environment_unavailable",
						"connection",
						"Auto Heal environment check failed; no browser repair was attempted",
						ExitConnection,
						autoHealEnvironmentNextCommands(browserMode),
						map[string]any{
							"browser_mode":      browserMode,
							"environment":       autoHealEnvironmentFailure(environmentErr),
							"human_required":    false,
							"agent_should_stop": false,
						},
					)
				}
				if !environment.Allowed {
					return a.render(ctx, fmt.Sprintf("keepalive\t%s\tenvironment-unavailable", connectionName), map[string]any{
						"ok":            true,
						"browser_mode":  browserMode,
						"connection":    connectionName,
						"mode":          mode,
						"state":         "environment_unavailable",
						"status":        "skipped",
						"action":        "skipped",
						"locked":        false,
						"environment":   environment,
						"chrome":        keepaliveChromeStatus{Skipped: true, Reason: "auto-heal environment unavailable: " + environment.Reason},
						"next_commands": autoHealEnvironmentNextCommands(browserMode),
						"lock":          map[string]any{"name": lock.Metadata.Name, "acquired": true},
					})
				}
				if releaseAutoHealLease != nil {
					defer func() { _ = releaseAutoHealLease() }()
				}
			}

			selfHealApproval := remoteDebuggingApprovalEnabled(cmd, macOSSelfHealApproval)
			initialActiveProbe := a.opts.activeProbe
			selfHealActive := selfHealApproval && browserMode == "headed" && a.opts.autoConnect && probeMode != "passive"
			probeResult := map[string]any{"mode": probeMode, "repair_requested": repair, "force_requested": force, "managed_process_sweep_requested": managedProcessSweep}
			if selfHealActive {
				// A scheduled keepalive must be a no-op while the existing headed
				// runtime is healthy. Inspect its socket passively before opening
				// Chrome, touching preferences, or starting another hold process.
				a.opts.activeProbe = false
				preflightProbe, probeErr := a.browserProbe(ctx)
				if probeErr != nil {
					return commandError(
						"invalid_browser_url",
						"usage",
						probeErr.Error(),
						ExitUsage,
						[]string{"cdp --browser-mode headed --auto-connect daemon keepalive --json"},
					)
				}
				preflightStatus := a.daemonStatus(ctx, preflightProbe)
				preflightHealthy, preflightCheck := keepaliveRuntimeCheck(ctx, preflightStatus)
				probeResult["result"] = preflightProbe.State
				probeResult["self_heal_requested"] = true
				if preflightStatus.State == "running" && preflightHealthy {
					probeResult["self_heal_skipped"] = "runtime_healthy"
					return a.render(ctx, fmt.Sprintf("keepalive\t%s\thealthy", connectionName), map[string]any{
						"ok":           true,
						"browser_mode": browserMode,
						"connection":   connectionName,
						"mode":         mode,
						"state":        "healthy",
						"action":       "none",
						"locked":       false,
						"daemon":       preflightStatus,
						"probe":        probeResult,
						"health":       preflightCheck,
						"chrome":       keepaliveChromeStatus{Skipped: true, Reason: "headed runtime healthy; self-heal skipped"},
						"lock":         map[string]any{"name": lock.Metadata.Name, "acquired": true},
					})
				}
				// The runtime is missing or unhealthy. The existing bounded repair
				// branch below may now activate Chrome and request one CDP probe.
				a.opts.activeProbe = true
			}

			chrome := keepaliveChromeStatus{Skipped: true, Reason: "not required for browser_url mode"}
			chromePrepared := false
			if browserMode == "headed" && a.opts.autoConnect && (probeMode == "active" || (selfHealApproval && probeMode != "passive")) {
				if err := lock.Update(ctx, "ensuring_chrome_before_probe"); err != nil {
					return err
				}
				chrome, err = ensureChromeForKeepalive(ctx, display, chromeCommand, chromeArgs)
				if err != nil {
					return commandError(
						"chrome_start_failed",
						"connection",
						fmt.Sprintf("ensure headed Chrome is running before active probe: %v", err),
						ExitConnection,
						[]string{"cdp daemon keepalive --chrome-command <command> --json", "open chrome://inspect/#remote-debugging"},
					)
				}
				chromePrepared = true
			}

			var probe browser.ProbeResult
			var approval browser.RemoteDebuggingApprovalResult
			if selfHealActive {
				if err := lock.Update(ctx, "self_healing_remote_debugging_approval"); err != nil {
					return err
				}
				var repairErr error
				probe, approval, repairErr = a.runHeadedRemoteDebuggingRepair(ctx)
				if repairErr != nil {
					return commandErrorWithData(
						"permission_pending",
						"permission",
						fmt.Sprintf("headed remote-debugging self-heal failed: %v", repairErr),
						ExitPermission,
						permissionRemediationCommands(),
						permissionPendingData(map[string]any{"approval": approval, "probe": probe, "probe_result": probeResult}),
					)
				}
				if probe.State != "cdp_available" || !approval.QueueDrained {
					return commandErrorWithData(
						"permission_pending",
						"permission",
						"headed remote-debugging self-heal did not produce a verified CDP transport",
						ExitPermission,
						permissionRemediationCommands(),
						permissionPendingData(map[string]any{"approval": approval, "probe": probe}),
					)
				}
			} else {
				if probeMode == "passive" || probeMode == "auto" {
					a.opts.activeProbe = false
				}
				if probeMode == "active" {
					a.opts.activeProbe = true
				}
				var err error
				probe, err = a.browserProbe(ctx)
				if err != nil {
					return commandError(
						"invalid_browser_url",
						"usage",
						err.Error(),
						ExitUsage,
						[]string{"cdp daemon keepalive --browser-url <browser-url> --json"},
					)
				}
			}
			status := a.daemonStatus(ctx, probe)
			probeResult["result"] = probe.State
			if selfHealActive {
				probeResult["remote_debugging_approval"] = approval
				probeResult["self_heal_requested"] = true
				probeResult["self_heal_message"] = remoteDebuggingApprovalMessage(approval)
			}
			runtimeHealthy, runtimeCheck := keepaliveRuntimeCheck(ctx, status)
			if managedProcessSweep && browserMode == "headless" {
				sweep, err := a.runManagedProcessSweep(ctx, store.Dir, lock, status)
				if err != nil {
					return commandError(
						"managed_process_sweep_failed",
						"connection",
						fmt.Sprintf("managed headless process sweep: %v", err),
						ExitConnection,
						[]string{"cdp --browser-mode headless daemon stop --force-managed --json", "cdp --browser-mode headless daemon keepalive --repair --force --json"},
					)
				}
				runtimeCheck["managed_process_sweep"] = sweep
			}
			if browserMode == "headless" && (repair || managedProcessSweep) && runtimeHealthy {
				holdReconcile, reconcileErr := daemon.ReconcileOrphanedDaemonHolds(ctx, store.Dir, browserMode, true)
				if reconcileErr != nil {
					return commandError(
						"daemon_hold_reconciliation_failed",
						"connection",
						fmt.Sprintf("reconcile orphaned headless daemon holds: %v", reconcileErr),
						ExitConnection,
						[]string{"cdp --browser-mode headless daemon logs --tail 50 --json", "cdp --browser-mode headless daemon keepalive --repair --json"},
					)
				}
				runtimeCheck["daemon_hold_reconciliation"] = holdReconcile
				if len(holdReconcile.SignalFailures) > 0 {
					return commandErrorWithData(
						"daemon_hold_reconciliation_failed",
						"connection",
						"one or more verified orphaned daemon holds could not be reclaimed",
						ExitConnection,
						[]string{"cdp --browser-mode headless daemon logs --tail 50 --json", "cdp --browser-mode headless daemon keepalive --repair --json"},
						map[string]any{"browser_mode": browserMode, "health": runtimeCheck, "daemon": status},
					)
				}
				if len(holdReconcile.ReclaimedPIDs) > 0 {
					status.Health = a.browserHealthSnapshot(ctx, status, false)
				}
			}
			if runtimeHealthy && reconnect > 0 && status.Runtime != nil && status.Runtime.ReconnectInterval != reconnect.String() {
				runtimeCheck["reconnect_interval_mismatch"] = true
				runtimeCheck["current_reconnect"] = status.Runtime.ReconnectInterval
				runtimeCheck["wanted_reconnect"] = reconnect.String()
			}
			if status.State == "running" && runtimeHealthy {
				action := "none"
				if holdReconcile, ok := runtimeCheck["daemon_hold_reconciliation"].(daemon.DaemonHoldReconcileResult); ok && len(holdReconcile.ReclaimedPIDs) > 0 {
					action = "reconciled"
				}
				return a.render(ctx, fmt.Sprintf("keepalive\t%s\thealthy", connectionName), map[string]any{
					"ok":           true,
					"browser_mode": browserMode,
					"connection":   connectionName,
					"mode":         mode,
					"state":        "healthy",
					"action":       action,
					"locked":       false,
					"daemon":       status,
					"probe":        probeResult,
					"health":       runtimeCheck,
					"lock":         map[string]any{"name": lock.Metadata.Name, "acquired": true},
				})
			}
			if repair && browserMode == "headed" && a.opts.autoConnect && (status.State == "stale_state" || status.State == "runtime_socket_unready") && status.Runtime != nil && strings.TrimSpace(status.Runtime.Endpoint) != "" {
				if err := lock.Update(ctx, "starting_daemon_from_stale_runtime_endpoint"); err != nil {
					return err
				}
				metadata := daemon.KeepAliveMetadata{UserDataDir: status.Runtime.UserDataDir}
				if strings.TrimSpace(metadata.UserDataDir) == "" {
					metadata.UserDataDir = a.opts.userDataDir
				}
				connectionMode := strings.TrimSpace(status.Runtime.ConnectionMode)
				if connectionMode == "" {
					connectionMode = a.connectionMode()
				}
				runtime, reused, err := a.startKeepAliveFromEndpoint(ctx, status.Runtime.Endpoint, connectionMode, metadata, reconnect)
				if err != nil {
					return commandErrorWithData(
						"connection_failed",
						"connection",
						fmt.Sprintf("repair daemon from last approved endpoint: %v", err),
						ExitConnection,
						[]string{"cdp --browser-mode headed daemon status --json", "cdp --browser-mode headed daemon logs --tail 50 --json"},
						map[string]any{"repair_source": "stale_runtime_endpoint", "previous": status},
					)
				}
				repairProbe := browser.ProbeResult{
					State:                "cdp_available",
					Message:              "last approved browser endpoint was reused from stale daemon runtime state",
					ConnectionMode:       connectionMode,
					Channel:              a.opts.channel,
					WebSocketDebuggerURL: true,
				}
				repairedStatus := daemon.SnapshotForMode(browserMode, connectionMode, connectionMode == "auto_connect", repairProbe)
				repairedStatus = daemon.WithRuntime(repairedStatus, runtime, true)
				repairedStatus.Health = a.browserHealthSnapshot(ctx, repairedStatus, false)
				repairedHealthy, repairedCheck := keepaliveRuntimeCheck(ctx, repairedStatus)
				state := "repaired"
				action := "repaired"
				if reused && repairedHealthy {
					state = "healthy"
					action = "none"
				}
				if err := lock.Update(ctx, state); err != nil {
					return err
				}
				return a.render(ctx, fmt.Sprintf("keepalive\t%s\t%s", connectionName, state), map[string]any{
					"ok":            true,
					"browser_mode":  browserMode,
					"connection":    connectionName,
					"mode":          mode,
					"state":         state,
					"action":        action,
					"repair_source": "stale_runtime_endpoint",
					"locked":        false,
					"daemon":        repairedStatus,
					"start": map[string]any{
						"keepalive_started":  !reused,
						"already_running":    reused,
						"reconnect_interval": durationString(reconnect),
						"runtime":            runtime,
					},
					"chrome":   keepaliveChromeStatus{Skipped: true, Reason: "reused last approved daemon endpoint"},
					"probe":    probeResult,
					"previous": status,
					"health":   repairedCheck,
					"lock":     map[string]any{"name": lock.Metadata.Name, "acquired": true},
				})
			}
			if browserMode == "headless" {
				human, data, err := a.runHeadlessKeepaliveStartOrRepair(ctx, store.Dir, lock, connectionName, mode, reconnect, chromeCommand, force, managedProcessSweep, repair || managedProcessSweep, staleLockAfter, status, probeResult, runtimeCheck)
				if err != nil {
					return err
				}
				return a.render(ctx, human, data)
			}
			if status.State == "running" {
				if err := lock.Update(ctx, "repairing_daemon"); err != nil {
					return err
				}
				if _, _, err := daemon.StopRuntimeForMode(ctx, store.Dir, a.browserModeName()); err != nil {
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
					"ok":           true,
					"browser_mode": browserMode,
					"connection":   connectionName,
					"mode":         mode,
					"state":        "passive",
					"action":       "skipped",
					"locked":       false,
					"daemon":       status,
					"probe":        probeResult,
					"lock":         map[string]any{"name": lock.Metadata.Name, "acquired": true},
				})
			}

			var managed *managedKeepAlive
			if a.opts.autoConnect {
				if !chromePrepared {
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
				reconnect:         reconnect,
				connectionName:    connectionName,
				remember:          true,
				managedKeepAlive:  managed,
				skipSelectedApply: managed != nil,
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
				"ok":           true,
				"browser_mode": browserMode,
				"connection":   connectionName,
				"mode":         mode,
				"state":        state,
				"action":       action,
				"locked":       false,
				"daemon":       result.data["daemon"],
				"start":        result.data["start"],
				"chrome":       chrome,
				"probe":        probeResult,
				"previous":     status,
				"health":       runtimeCheck,
				"lock":         map[string]any{"name": lock.Metadata.Name, "acquired": true},
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
	cmd.Flags().StringVar(&chromeCommand, "chrome-command", defaultChromeCommand(), "Chrome command to launch for auto-connect repair; empty disables launch")
	cmd.Flags().StringArrayVar(&chromeArgs, "chrome-args", nil, "extra Chrome argument; repeat for multiple arguments")
	cmd.Flags().BoolVar(&repair, "repair", false, "human-managed repair mode: remove stale runtime state and restart the daemon when safe")
	cmd.Flags().BoolVar(&force, "force", false, "for --browser-mode headless repair, clear stale managed runtime state before relaunching")
	cmd.Flags().BoolVar(&managedProcessSweep, "managed-process-sweep", false, "run managed headless process reconciliation before launch-capable repair work; lifecycle enforcement is expanded by cdp cron resilience")
	cmd.Flags().BoolVar(&macOSSelfHealApproval, "macos-self-heal-approval", false, "on macOS headed auto-connect, drain exact Chrome remote-debugging approval sheets across all windows and require a verified CDP probe")
	return cmd
}

func (a *app) runHeadlessKeepaliveStartOrRepair(ctx context.Context, storeDir string, lock daemon.LockHandle, connectionName, mode string, reconnect time.Duration, chromeCommand string, force bool, managedProcessSweep bool, reconcileDaemonHolds bool, staleLockAfter time.Duration, status daemon.Status, probeResult map[string]any, runtimeCheck map[string]any) (string, map[string]any, error) {
	if managedProcessSweep {
		if _, ok := runtimeCheck["managed_process_sweep"]; !ok {
			sweep, err := a.runManagedProcessSweep(ctx, storeDir, lock, status)
			if err != nil {
				return "", nil, err
			}
			runtimeCheck["managed_process_sweep"] = sweep
		}
	}
	resourcePreflight := a.maintenanceResourcePreflightForState(ctx, storeDir, status, nil)
	runtimeCheck["resource_preflight"] = resourcePreflight
	if !resourcePreflight.HeavyWorkAllowed {
		if err := lock.Update(ctx, "resource_blocked"); err != nil {
			return "", nil, err
		}
		data := map[string]any{
			"ok":                 true,
			"browser_mode":       "headless",
			"connection":         connectionName,
			"mode":               mode,
			"state":              "resource_blocked",
			"status":             "skipped",
			"action":             "skipped",
			"locked":             false,
			"daemon":             status,
			"chrome":             keepaliveChromeStatus{Checked: true, Skipped: true, Reason: "resource preflight blocked heavy maintenance"},
			"probe":              probeResult,
			"previous":           status,
			"health":             runtimeCheck,
			"resource_preflight": resourcePreflight,
			"next_commands":      resourcePreflight.NextCommands,
			"lock":               map[string]any{"name": lock.Metadata.Name, "acquired": true},
		}
		return fmt.Sprintf("keepalive\t%s\tresource-blocked", connectionName), data, nil
	}
	if status.State == "running" {
		if err := lock.Update(ctx, "repairing_daemon"); err != nil {
			return "", nil, err
		}
		if _, _, err := daemon.StopRuntimeForMode(ctx, storeDir, a.browserModeName()); err != nil {
			return "", nil, commandError(
				"connection_failed",
				"connection",
				fmt.Sprintf("stop unhealthy daemon before repair: %v", err),
				ExitConnection,
				[]string{"cdp daemon stop --json", "cdp daemon keepalive --json"},
			)
		}
	}
	if force {
		if err := lock.Update(ctx, "force_cleaning_managed_headless"); err != nil {
			return "", nil, err
		}
		if err := daemon.ClearRuntimeForMode(ctx, storeDir, string(config.BrowserModeHeadless), 0); err != nil {
			return "", nil, err
		}
		managedStop, err := browser.StopManagedChrome(ctx, storeDir, browser.ManagedStopOptions{Force: true})
		if err != nil {
			return "", nil, err
		}
		runtimeCheck["forced_managed_cleanup"] = managedStop
		cleanup, err := a.clearManagedHeadlessRecoveryState(ctx, storeDir, staleLockAfter)
		if err != nil {
			return "", nil, err
		}
		runtimeCheck["forced_recovery_state"] = cleanup
	}
	if err := lock.Update(ctx, "launching_managed_chrome"); err != nil {
		return "", nil, err
	}
	managed, chrome, err := a.ensureManagedChromeForKeepalive(ctx, storeDir, chromeCommand)
	if err != nil {
		return "", nil, commandErrorWithData(
			"chrome_start_failed",
			"connection",
			fmt.Sprintf("start managed headless Chrome: %v", err),
			ExitConnection,
			[]string{"cdp --browser-mode headless browser profile status --json", "cdp --browser-mode headless daemon keepalive --repair --json"},
			map[string]any{"browser_mode": "headless", "human_required": false, "agent_should_stop": false, "chrome": chrome},
		)
	}
	a.opts.browserURL = managedHTTPURL(managed.Endpoint)
	a.opts.autoConnect = false
	a.opts.userDataDir = managed.Metadata.UserDataDir

	if err := lock.Update(ctx, "starting_daemon"); err != nil {
		return "", nil, err
	}
	result, err := a.runDaemonStart(ctx, daemonStartConfig{
		reconnect:         reconnect,
		connectionName:    connectionName,
		remember:          true,
		managedKeepAlive:  managed,
		skipSelectedApply: true,
	})
	if err != nil {
		return "", nil, err
	}
	if reconcileDaemonHolds {
		holdReconcile, reconcileErr := daemon.ReconcileOrphanedDaemonHolds(ctx, storeDir, "headless", true)
		if reconcileErr != nil {
			return "", nil, commandError(
				"daemon_hold_reconciliation_failed",
				"connection",
				fmt.Sprintf("reconcile orphaned headless daemon holds: %v", reconcileErr),
				ExitConnection,
				[]string{"cdp --browser-mode headless daemon logs --tail 50 --json", "cdp --browser-mode headless daemon keepalive --repair --json"},
			)
		}
		runtimeCheck["daemon_hold_reconciliation"] = holdReconcile
		if len(holdReconcile.SignalFailures) > 0 {
			return "", nil, commandErrorWithData(
				"daemon_hold_reconciliation_failed",
				"connection",
				"one or more verified orphaned daemon holds could not be reclaimed",
				ExitConnection,
				[]string{"cdp --browser-mode headless daemon logs --tail 50 --json", "cdp --browser-mode headless daemon keepalive --repair --json"},
				map[string]any{"browser_mode": "headless", "health": runtimeCheck, "daemon": result.data["daemon"]},
			)
		}
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
	if holdReconcile, ok := runtimeCheck["daemon_hold_reconciliation"].(daemon.DaemonHoldReconcileResult); ok && len(holdReconcile.ReclaimedPIDs) > 0 {
		action = "reconciled"
	}
	if err := lock.Update(ctx, state); err != nil {
		return "", nil, err
	}
	data := map[string]any{
		"ok":           true,
		"browser_mode": "headless",
		"connection":   connectionName,
		"mode":         mode,
		"state":        state,
		"action":       action,
		"locked":       false,
		"daemon":       result.data["daemon"],
		"start":        result.data["start"],
		"chrome":       chrome,
		"probe":        probeResult,
		"previous":     status,
		"health":       runtimeCheck,
		"lock":         map[string]any{"name": lock.Metadata.Name, "acquired": true},
	}
	if conn, ok := result.data["connection"]; ok {
		data["connection_detail"] = conn
	}
	if managedProcessSweep {
		daemon.AppendLogForMode(ctx, storeDir, string(config.BrowserModeHeadless), daemon.LogEntry{
			Level:   "info",
			Event:   "managed_process_repair_validated",
			Message: "managed headless keepalive completed after process reconciliation",
		})
	}
	return fmt.Sprintf("keepalive\t%s\t%s", connectionName, state), data, nil
}

func (a *app) runManagedProcessSweep(ctx context.Context, storeDir string, lock daemon.LockHandle, status daemon.Status) (browser.ManagedProcessReconcileResult, error) {
	if err := lock.Update(ctx, cronManagedProcessSweepPhaseName); err != nil {
		return browser.ManagedProcessReconcileResult{}, err
	}
	return browser.ReconcileManagedProcesses(ctx, storeDir, browser.ManagedProcessReconcileOptions{
		ActivePID:  managedChromeActivePID(status),
		ReapExtras: true,
	})
}

func managedChromeActivePID(status daemon.Status) int {
	if status.Runtime == nil {
		return 0
	}
	if status.Runtime.ChromePID > 0 {
		return status.Runtime.ChromePID
	}
	if status.Runtime.ManagedBrowser != nil && status.Runtime.ManagedBrowser.ChromePID > 0 {
		return status.Runtime.ManagedBrowser.ChromePID
	}
	return 0
}

func keepaliveRepairClassification(keepalive map[string]any, err error) string {
	if err != nil {
		if code, ok := commandErrorCode(err); ok {
			return code
		}
		return "repair_failed"
	}
	if keepalive == nil {
		return "missing_keepalive_result"
	}
	if locked, ok := keepalive["locked"].(bool); ok && locked {
		return "keepalive_locked"
	}
	if health, ok := keepalive["health"].(map[string]any); ok {
		if result, ok := stringMapField(health, "result"); ok && result != "" {
			return result
		}
	}
	if chrome, ok := keepalive["chrome"].(keepaliveChromeStatus); ok {
		if chrome.Launched {
			return "managed_chrome_launched"
		}
		if chrome.Running {
			return "managed_chrome_reused"
		}
	}
	if state, ok := stringMapField(keepalive, "state"); ok && state != "" {
		return state
	}
	return "unknown"
}

func commandErrorCode(err error) (string, bool) {
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		return "", false
	}
	return cmdErr.Code, strings.TrimSpace(cmdErr.Code) != ""
}

func stringMapField(fields map[string]any, key string) (string, bool) {
	value, ok := fields[key]
	if !ok {
		return "", false
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	return text, text != ""
}

func (a *app) connectionStateName(ctx context.Context) string {
	if strings.TrimSpace(a.opts.connection) != "" {
		return strings.TrimSpace(a.opts.connection)
	}
	browserMode := a.browserModeName()
	preferredName := defaultConnectionNameForBrowserMode(browserMode)
	store, err := a.stateStore()
	if err == nil {
		if file, loadErr := store.Load(ctx); loadErr == nil {
			if conn, ok := state.CurrentConnection(file); ok && strings.TrimSpace(conn.Name) != "" {
				if connectionMatchesBrowserMode(conn, browserMode) {
					if browserMode == "headless" && conn.Name == "default" {
						return preferredName
					}
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
	}
	if a.opts.autoConnect {
		return preferredName
	}
	if strings.TrimSpace(a.opts.browserURL) != "" {
		if runtime, ok, runtimeErr := a.connectionFromRunningManagedRuntime(ctx, store.Dir, browserMode); runtimeErr == nil && ok && runtime.BrowserURL == a.opts.browserURL {
			return preferredName
		}
		if browserMode == "headless" {
			return preferredName
		}
		return "browser-url"
	}
	return preferredName
}

const (
	managedChromeLaunchMaxAttempts    = 3
	managedChromeLaunchAttemptTimeout = 15 * time.Second
)

func (a *app) ensureManagedChromeForKeepalive(ctx context.Context, stateDir, chromeCommand string) (*managedKeepAlive, keepaliveChromeStatus, error) {
	status := keepaliveChromeStatus{Checked: true, Command: chromeCommand}
	if launch, ok, err := browser.ReuseManagedChrome(ctx, stateDir); err != nil {
		return nil, status, err
	} else if ok {
		managedStatus := browser.ManagedMetadataStatus(launch.Metadata)
		status.Running = true
		status.ManagedBrowser = &managedStatus
		return &managedKeepAlive{Endpoint: launch.Endpoint, Metadata: launch.Metadata, ManagedBrowser: &managedStatus}, status, nil
	}
	seedStrategy := ""
	if cfg, cfgErr := config.Load(a.opts.config); cfgErr != nil {
		return nil, status, cfgErr
	} else {
		seedStrategy = cfg.Browser.Headless.ProfileSeedStrategy
	}
	status.MaxAttempts = managedChromeLaunchMaxAttempts
	launch, err := startManagedChromeWithRetries(ctx, stateDir, browser.ManagedOptions{StateDir: stateDir, Chrome: chromeCommand, ProfileSeedStrategy: seedStrategy}, &status, browser.StartManagedChrome, managedChromeLaunchMaxAttempts, managedChromeLaunchAttemptTimeout)
	if err != nil {
		return nil, status, fmt.Errorf("managed Chrome launch failed after %d bounded attempt(s): %w", status.Attempts, err)
	}
	managedStatus := browser.ManagedMetadataStatus(launch.Metadata)
	status.Launched = true
	status.ManagedBrowser = &managedStatus
	return &managedKeepAlive{Endpoint: launch.Endpoint, Metadata: launch.Metadata, ManagedBrowser: &managedStatus}, status, nil
}

func startManagedChromeWithRetries(
	ctx context.Context,
	stateDir string,
	opts browser.ManagedOptions,
	status *keepaliveChromeStatus,
	launch func(context.Context, browser.ManagedOptions) (browser.ManagedLaunch, error),
	maxAttempts int,
	attemptTimeout time.Duration,
) (browser.ManagedLaunch, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status.Attempts = attempt
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		result, err := launch(attemptCtx, opts)
		cancel()
		if err == nil {
			return result, nil
		}
		lastErr = err
		status.AttemptErrors = append(status.AttemptErrors, err.Error())
		if ctx.Err() != nil || attempt == maxAttempts {
			break
		}
		_, _ = browser.StopManagedChrome(ctx, stateDir, browser.ManagedStopOptions{Force: true})
		_ = browser.ClearManagedRuntimeArtifacts(stateDir)
		timer := time.NewTimer(time.Duration(attempt) * 150 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return browser.ManagedLaunch{}, ctx.Err()
		case <-timer.C:
		}
	}
	return browser.ManagedLaunch{}, lastErr
}

func managedHTTPURL(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	u.Scheme = "http"
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
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
	if !status.RuntimeSocketReady {
		check["ok"] = false
		check["result"] = "daemon_socket_unready"
		return false, check
	}
	var result struct {
		TargetInfos []cdp.TargetInfo `json:"targetInfos"`
	}
	if err := (daemon.RuntimeClient{Runtime: *status.Runtime}).Call(ctx, "Target.getTargets", map[string]any{}, &result); err != nil {
		check["ok"] = false
		check["result"] = "target_list_failed"
		check["error"] = err.Error()
		if _, managed := managedRuntimeProcessCheck(ctx, status.Runtime); managed != nil {
			check["managed_browser_health"] = managed
		}
		return false, check
	}
	if ok, managed := managedRuntimeProcessCheck(ctx, status.Runtime); managed != nil {
		if !ok {
			if !managedProcessIdentityFailure(managed) && !managedProcessCheckCanceled(managed) {
				managed["state"] = "daemon_rpc_ready_pid_not_running"
			}
			managed["daemon_rpc_ready"] = true
		}
		check["managed_browser_health"] = managed
	}
	check["ok"] = true
	check["result"] = "target_list_ok"
	check["target_count"] = len(result.TargetInfos)
	return true, check
}

func defaultChromeCommand() string {
	chrome, err := browser.DiscoverChrome("")
	if err != nil {
		return "google-chrome-stable"
	}
	return chrome
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
	running, err := chromeProcessRunning(ctx, chromeCommand)
	if err != nil {
		return status, fmt.Errorf("inspect headed Chrome process state: %w", err)
	}
	if running {
		status.Running = true
		window, err := browser.EnsureHeadedChromeWindow(ctx, "stable")
		if err != nil {
			return status, err
		}
		status.Window = &window
		return status, nil
	}
	if _, err := browser.EnableRemoteDebuggingPreference(ctx, "stable"); err != nil {
		return status, fmt.Errorf("prepare Chrome remote-debugging preference: %w", err)
	}
	env := os.Environ()
	if strings.TrimSpace(display) != "" {
		env = append(env, "DISPLAY="+display)
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return status, fmt.Errorf("open null device: %w", err)
	}
	defer devNull.Close()
	cmd, err := processgroup.StartWithOptions(chromeCommand, chromeArgs, processgroup.Options{
		Env:    env,
		Stdin:  devNull,
		Stdout: devNull,
		Stderr: devNull,
	})
	if err != nil {
		return status, err
	}
	status.Launched = true
	window, err := browser.EnsureHeadedChromeWindow(ctx, "stable")
	if err != nil {
		terminateHeadedChromeLaunch(cmd)
		return status, err
	}
	status.Window = &window
	if window.Supported && !window.WindowReady {
		terminateHeadedChromeLaunch(cmd)
		message := strings.TrimSpace(window.Message)
		if detail := strings.TrimSpace(window.Detail); detail != "" {
			if message == "" {
				message = detail
			} else {
				message += ": " + detail
			}
		}
		if message == "" {
			message = "headed Chrome window was not ready"
		}
		return status, fmt.Errorf("headed Chrome readiness failed: %s", message)
	}
	if cmd.Process != nil {
		processgroup.Detach(cmd)
	}
	return status, nil
}

func terminateHeadedChromeLaunch(command *exec.Cmd) {
	processgroup.Terminate(command)
	_ = command.Wait()
}

func chromeProcessRunning(ctx context.Context, chromeCommand string) (bool, error) {
	name := strings.ToLower(strings.TrimSpace(filepath.Base(chromeCommand)))
	if name == "" {
		return false, nil
	}
	result, err := runBoundedExternalCommand(ctx, "ps", "-axo", "pid=,args=")
	if err != nil {
		return false, fmt.Errorf("run headed Chrome process probe: %w", err)
	}
	for _, line := range strings.Split(result.stdout, "\n") {
		if chromeProcessLineMatches(line, name) {
			return true, nil
		}
	}
	return false, nil
}

func chromeProcessLineMatches(line, requestedName string) bool {
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" || strings.Contains(line, "chrome_crashpad_handler") || strings.Contains(line, "chrome-headless") || strings.Contains(line, "--headless") {
		return false
	}
	for _, name := range []string{requestedName, "google chrome", "google-chrome", "chromium", "chrome"} {
		name = strings.TrimSpace(name)
		if name != "" && strings.Contains(line, name) {
			return true
		}
	}
	return false
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
