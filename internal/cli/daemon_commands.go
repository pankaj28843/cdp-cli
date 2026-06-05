package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
)

func (a *app) newDaemonCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the long-running Chrome attach daemon",
		Long:  "Manage the long-running Chrome attach daemon. In --auto-connect mode, Chrome/default-profile access is human-in-the-loop: agents may inspect status, doctor, health, and logs, but should not retry start/restart/stop loops when permission is pending. If Chrome asks for remote debugging approval, stop and ask the human to open chrome://inspect/#remote-debugging and click Allow.",
	}
	cmd.AddCommand(a.newDaemonStartCommand())
	cmd.AddCommand(a.newDaemonStatusCommand())
	cmd.AddCommand(a.newDaemonStopCommand())
	cmd.AddCommand(a.newDaemonRestartCommand())
	cmd.AddCommand(a.newDaemonKeepaliveCommand())
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
				return daemonStartResult{}, commandErrorWithData(
					"permission_pending",
					"permission",
					err.Error(),
					ExitPermission,
					permissionRemediationCommands(),
					permissionPendingData(map[string]any{"daemon_start": map[string]any{"phase": "resolve_browser_endpoint", "waiting_for_user_approval": a.opts.autoConnect}}),
				)
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
			return daemonStartResult{}, commandErrorWithData(
				"permission_pending",
				"permission",
				fmt.Sprintf("start daemon keepalive: %v", err),
				ExitPermission,
				permissionRemediationCommands(),
				permissionPendingData(map[string]any{"daemon_start": map[string]any{"phase": "start_keepalive", "waiting_for_user_approval": a.opts.autoConnect, "browser_endpoint_seen": endpoint != ""}}),
			)
		}
		runtime = &r
		alreadyRunning = reused
	}

	status := a.daemonStatus(ctx, probe)
	if runtime != nil {
		status = daemon.WithRuntime(status, *runtime, true)
		status.Health = a.browserHealthSnapshot(ctx, status, false)
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
			UserDataDir:         managed.Metadata.UserDataDir,
			ManagedBrowser:      managed.ManagedBrowser,
			ManagedProfilePath:  managed.Metadata.UserDataDir,
			ProfileSeedStrategy: managed.Metadata.ProfileSeedStrategy,
			ChromePID:           managed.Metadata.ChromePID,
			ChromePort:          managed.Metadata.DebuggingPort,
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
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the attach daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			stop, err := a.stopSelectedRuntime(ctx)
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
				"runtime":                 stop.Runtime,
			})
		},
	}
}

func (a *app) stopSelectedRuntime(ctx context.Context) (daemonStopResult, error) {
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
		managedStop, err := browser.StopOwnedManagedChrome(ctx, store.Dir, nil)
		if err != nil {
			return result, err
		}
		result.ManagedBrowser = managedStop
		result.ManagedBrowserStopped = managedStop.Stopped
	}
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
			return a.render(ctx, fmt.Sprintf("daemon-health\t%s", health["state"]), map[string]any{"ok": true, "daemon": status, "health": health})
		},
	}
	cmd.Flags().BoolVar(&processInfo, "process-info", false, "include optional SystemInfo.getProcessInfo process counts when a daemon runtime is healthy")
	return cmd
}

const defaultHeadlessHealthCheckURL = "data:text/html,%3Cmain%20data-cdp-health%3D%22ok%22%3Ecdp-headless-health%3C%2Fmain%3E"

type daemonHealthCheckOptions struct {
	Repair           bool
	HealthURL        string
	OutDir           string
	FailureThreshold int
	LockTimeout      time.Duration
	StaleLockAfter   time.Duration
	Reconnect        time.Duration
	ChromeCommand    string
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
	store, err := a.stateStore()
	if err != nil {
		return err
	}
	outDir := strings.TrimSpace(opts.OutDir)
	if outDir == "" {
		outDir = filepath.Join(store.Dir, "headless-health")
	}
	lockName := "daemon-health-check-headless"
	lock, acquired, existingLock, err := daemon.AcquireLock(ctx, store.Dir, lockName, opts.LockTimeout, opts.StaleLockAfter, daemon.LockMetadata{Name: lockName, Phase: "checking"})
	if err != nil {
		return commandError("lock_failed", "connection", fmt.Sprintf("acquire health-check lock: %v", err), ExitConnection, []string{"cdp --browser-mode headless daemon health --json"})
	}
	if !acquired {
		return a.render(ctx, "headless health-check locked", map[string]any{
			"ok":           true,
			"browser_mode": a.browserModeName(),
			"state":        "locked",
			"action":       "skipped",
			"locked":       true,
			"lock":         existingLock,
			"next_commands": []string{
				"cdp --browser-mode headless daemon health --json",
				"cdp cron status --json",
			},
		})
	}
	defer lock.Release()

	runID := time.Now().UTC().Format("20060102T150405Z")
	runDir := filepath.Join(outDir, runID)
	summaryPath := filepath.Join(outDir, "latest.json")
	screenshotPath := filepath.Join(runDir, "screenshot.png")
	steps := []map[string]any{}
	report := map[string]any{
		"ok":           false,
		"browser_mode": a.browserModeName(),
		"state":        "failed",
		"action":       "diagnosed",
		"run_id":       runID,
		"locked":       false,
		"lock":         map[string]any{"name": lock.Metadata.Name, "acquired": true},
		"steps":        steps,
		"artifacts": map[string]any{
			"run_dir":    runDir,
			"summary":    summaryPath,
			"screenshot": screenshotPath,
		},
		"next_commands": headlessHealthCheckNextCommands(),
	}
	fail := func(failure string, cause error) error {
		if cause != nil {
			report["error"] = cause.Error()
		}
		report["failure"] = failure
		count := a.updateHeadlessHealthCheckFailure(ctx, outDir, runDir, summaryPath, opts.FailureThreshold, true)
		report["failure_count"] = count
		_ = writeJSONArtifact(summaryPath, report)
		return commandErrorWithData("headless_health_check_failed", "check_failed", fmt.Sprintf("headless health-check failed: %s", failure), ExitCheckFailed, headlessHealthCheckNextCommands(), report)
	}
	addStep := func(name string, ok bool, fields map[string]any) {
		step := map[string]any{"name": name, "ok": ok}
		for key, value := range fields {
			step[key] = value
		}
		steps = append(steps, step)
		report["steps"] = steps
	}

	status, health, err := a.selectedDaemonHealth(ctx)
	report["daemon"] = status
	report["health"] = health
	if err != nil {
		addStep("health", false, map[string]any{"error": err.Error()})
		if !opts.Repair {
			return fail("health_failed", err)
		}
	} else {
		addStep("health", healthState(health) == "healthy", map[string]any{"state": healthState(health)})
	}

	if (err != nil || healthState(health) != "healthy") && opts.Repair {
		if err := lock.Update(ctx, "repairing"); err != nil {
			return err
		}
		repair, err := a.repairManagedHeadlessForHealthCheck(ctx, store.Dir, opts)
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
		addStep("health_after_repair", healthState(health) == "healthy", map[string]any{"state": healthState(health)})
	}
	if healthState(health) != "healthy" {
		return fail("health_not_healthy", nil)
	}

	target, session, closeSession, err := a.openHealthCheckTarget(ctx, opts.HealthURL)
	if err != nil {
		addStep("open", false, map[string]any{"error": err.Error()})
		return fail("navigate_failed", err)
	}
	defer closeSession()
	report["target"] = pageRow(target)
	addStep("open", true, map[string]any{"target_id": target.TargetID})

	var js struct {
		OK   bool   `json:"ok"`
		Text string `json:"text"`
	}
	if err := evaluateJSONValue(ctx, session, headlessHealthCheckExpression(), "headless health-check javascript", &js); err != nil {
		addStep("javascript", false, map[string]any{"error": err.Error()})
		return fail("javascript_failed", err)
	}
	addStep("javascript", js.OK, map[string]any{"text": js.Text})
	if !js.OK {
		return fail("javascript_unexpected_result", nil)
	}

	var text textResult
	if err := evaluateJSONValue(ctx, session, textExpression("body", 1, 1), "headless health-check text", &text); err != nil {
		addStep("dom_text", false, map[string]any{"error": err.Error()})
		return fail("dom_text_failed", err)
	}
	addStep("dom_text", text.Count > 0, map[string]any{"count": text.Count})
	if text.Count == 0 {
		return fail("dom_text_empty", nil)
	}

	shot, err := session.CaptureScreenshot(ctx, cdp.ScreenshotOptions{Format: "png"})
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

	report["ok"] = true
	report["state"] = "healthy"
	report["action"] = "validated"
	report["failure"] = nil
	report["failure_count"] = a.updateHeadlessHealthCheckFailure(ctx, outDir, runDir, summaryPath, opts.FailureThreshold, false)
	if err := lock.Update(ctx, "healthy"); err != nil {
		return err
	}
	if err := writeJSONArtifact(summaryPath, report); err != nil {
		return err
	}
	return a.render(ctx, fmt.Sprintf("headless-health-check\t%s", report["state"]), report)
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

func (a *app) repairManagedHeadlessForHealthCheck(ctx context.Context, storeDir string, opts daemonHealthCheckOptions) (map[string]any, error) {
	repair := map[string]any{"previous_state": "unknown"}
	stop, err := a.stopSelectedRuntime(ctx)
	if err != nil {
		return repair, err
	}
	repair["stop"] = stop
	if stop.DaemonStopped {
		repair["previous_state"] = "runtime_stopped"
	} else {
		repair["previous_state"] = "not_running"
	}
	managed, chrome, err := a.ensureManagedChromeForKeepalive(ctx, storeDir, opts.ChromeCommand)
	if err != nil {
		return repair, err
	}
	a.opts.browserURL = managedHTTPURL(managed.Endpoint)
	a.opts.autoConnect = false
	a.opts.userDataDir = managed.Metadata.UserDataDir
	result, err := a.runDaemonStart(ctx, daemonStartConfig{
		reconnect:         opts.Reconnect,
		connectionName:    a.connectionStateName(ctx),
		remember:          true,
		managedKeepAlive:  managed,
		skipSelectedApply: true,
	})
	if err != nil {
		return repair, err
	}
	repair["chrome"] = chrome
	repair["daemon"] = result.data["daemon"]
	repair["start"] = result.data["start"]
	return repair, nil
}

func (a *app) openHealthCheckTarget(ctx context.Context, rawURL string) (cdp.TargetInfo, *cdp.PageSession, func(), error) {
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return cdp.TargetInfo{}, nil, nil, err
	}
	targetID, err := a.createPageTarget(ctx, client, rawURL)
	if err != nil {
		_ = closeClient(ctx)
		return cdp.TargetInfo{}, nil, nil, err
	}
	target, err := cdp.TargetInfoWithClient(ctx, client, targetID)
	if err != nil {
		_ = cdp.CloseTargetWithClient(ctx, client, targetID)
		_ = closeClient(ctx)
		return cdp.TargetInfo{}, nil, nil, err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
	if err != nil {
		_ = cdp.CloseTargetWithClient(ctx, client, targetID)
		_ = closeClient(ctx)
		return cdp.TargetInfo{}, nil, nil, err
	}
	closeSession := func() {
		_ = cdp.CloseTargetWithClient(context.Background(), client, targetID)
		_ = session.Close(context.Background())
	}
	return target, session, closeSession, nil
}

func headlessHealthCheckExpression() string {
	return `(() => {
  const marker = "__cdp_cli_headless_health_check__";
  const text = String(document.querySelector("[data-cdp-health]")?.textContent || "");
  return { ok: text === "cdp-headless-health", text, marker };
})()`
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

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the attach daemon and reconnect through the daemon gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 60*time.Second)
			defer cancel()

			stop, err := a.stopSelectedRuntime(ctx)
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
	return cmd
}

type keepaliveChromeStatus struct {
	Display        string                 `json:"display,omitempty"`
	Command        string                 `json:"command,omitempty"`
	Args           []string               `json:"args,omitempty"`
	Checked        bool                   `json:"checked"`
	Running        bool                   `json:"running"`
	Launched       bool                   `json:"launched"`
	Skipped        bool                   `json:"skipped"`
	Reason         string                 `json:"reason,omitempty"`
	ManagedBrowser *browser.ManagedStatus `json:"managed_browser,omitempty"`
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
			probeResult := map[string]any{"mode": probeMode, "result": probe.State, "repair_requested": repair}
			runtimeHealthy, runtimeCheck := keepaliveRuntimeCheck(ctx, status)
			if runtimeHealthy && reconnect > 0 && status.Runtime != nil && status.Runtime.ReconnectInterval != reconnect.String() {
				runtimeHealthy = false
				runtimeCheck = map[string]any{
					"ok":                false,
					"result":            "reconnect_interval_mismatch",
					"runtime_state":     status.State,
					"current_reconnect": status.Runtime.ReconnectInterval,
					"wanted_reconnect":  reconnect.String(),
				}
			}
			if status.State == "running" && runtimeHealthy {
				return a.render(ctx, fmt.Sprintf("keepalive\t%s\thealthy", connectionName), map[string]any{
					"ok":           true,
					"browser_mode": browserMode,
					"connection":   connectionName,
					"mode":         mode,
					"state":        "healthy",
					"action":       "none",
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

			chrome := keepaliveChromeStatus{Skipped: true, Reason: "not required for browser_url mode"}
			var managed *managedKeepAlive
			if browserMode == "headless" {
				if err := lock.Update(ctx, "launching_managed_chrome"); err != nil {
					return err
				}
				managed, chrome, err = a.ensureManagedChromeForKeepalive(ctx, store.Dir, chromeCommand)
				if err != nil {
					return commandError(
						"chrome_start_failed",
						"connection",
						fmt.Sprintf("start managed headless Chrome: %v", err),
						ExitConnection,
						[]string{"cdp --browser-mode headless browser profile status --json", "cdp --browser-mode headless daemon keepalive --repair --json"},
					)
				}
				a.opts.browserURL = managedHTTPURL(managed.Endpoint)
				a.opts.autoConnect = false
				a.opts.userDataDir = managed.Metadata.UserDataDir
			} else if a.opts.autoConnect {
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
	return cmd
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
		if browserMode == "headless" {
			return preferredName
		}
		return "browser-url"
	}
	return preferredName
}

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
	if cfg, cfgErr := config.Load(a.opts.config); cfgErr == nil {
		seedStrategy = cfg.Browser.Headless.ProfileSeedStrategy
	}
	launch, err := browser.StartManagedChrome(ctx, browser.ManagedOptions{StateDir: stateDir, Chrome: chromeCommand, ProfileSeedStrategy: seedStrategy})
	if err != nil {
		return nil, status, err
	}
	managedStatus := browser.ManagedMetadataStatus(launch.Metadata)
	status.Launched = true
	status.ManagedBrowser = &managedStatus
	return &managedKeepAlive{Endpoint: launch.Endpoint, Metadata: launch.Metadata, ManagedBrowser: &managedStatus}, status, nil
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
	if ok, managed := managedRuntimeProcessCheck(status.Runtime); managed != nil {
		check["managed_browser_health"] = managed
		if !ok {
			check["ok"] = false
			check["result"] = "managed_chrome_process_not_running"
			return false, check
		}
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
