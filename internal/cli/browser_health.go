package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

const autoConnectHumanAction = "Open chrome://inspect/#remote-debugging in Chrome and click Allow for the cdp remote debugging request."

var managedRuntimeProcessRunning = daemon.ProcessRunningContext

var managedRuntimeEndpointReachable = browser.ManagedBrowserEndpointReachable

func safeDiagnosticCommands() []string {
	return []string{
		"cdp daemon status --json",
		"cdp doctor --check daemon --json",
		"cdp doctor --check browser-health --json",
		"cdp daemon logs --tail 50 --json",
	}
}

func permissionRemediationCommands() []string {
	return append([]string{remoteDebuggingApprovalCommand(), "open chrome://inspect/#remote-debugging"}, safeDiagnosticCommands()...)
}

func permissionPendingData(extra map[string]any) map[string]any {
	data := map[string]any{
		"human_required":    true,
		"agent_should_stop": true,
		"human_action":      autoConnectHumanAction,
		"safe_diagnostics":  safeDiagnosticCommands(),
	}
	for key, value := range extra {
		data[key] = value
	}
	return data
}

func modeScopedCommand(browserMode, command string) string {
	browserMode = strings.TrimSpace(browserMode)
	if browserMode == "" {
		browserMode = string(config.BrowserModeHeaded)
	}
	return fmt.Sprintf("cdp --browser-mode %s %s", browserMode, command)
}

func (a *app) modeDiagnosticCommands() []string {
	browserMode := a.browserModeName()
	return []string{
		modeScopedCommand(browserMode, "daemon status --json"),
		modeScopedCommand(browserMode, "doctor --check daemon --json"),
		modeScopedCommand(browserMode, "doctor --check browser-health --json"),
		modeScopedCommand(browserMode, "connection resolve --json"),
		modeScopedCommand(browserMode, "connection current --json"),
	}
}

func (a *app) connectionRemediationCommands() []string {
	if a.browserModeName() == string(config.BrowserModeHeadless) {
		return []string{
			modeScopedCommand(a.browserModeName(), "daemon keepalive --repair --json"),
			modeScopedCommand(a.browserModeName(), "daemon status --json"),
			modeScopedCommand(a.browserModeName(), "daemon health-check --repair --json"),
			"cdp cron status --json",
		}
	}
	return a.modeDiagnosticCommands()
}

func (a *app) browserBudget(ctx context.Context, client cdp.CommandClient) (cdp.BrowserResourceBudget, error) {
	return cdp.BrowserBudget(ctx, client, a.browserResourceBudgetOptions())
}

func (a *app) browserResourceBudgetOptions() cdp.BrowserResourceBudgetOptions {
	browserMode := a.browserModeName()
	maxTabs, source := a.maxTabsBudget(browserMode)
	return cdp.BrowserResourceBudgetOptions{
		MaxTabs:                    maxTabs,
		MaxTabsSource:              source,
		MaxRendererProcesses:       a.maxRendererProcessesBudget(browserMode),
		MaxRendererProcessesSource: a.maxRendererProcessesBudgetSource(browserMode),
		BrowserMode:                browserMode,
		ConnectionMode:             a.connectionMode(),
	}
}

func (a *app) maxRendererProcessesBudget(browserMode string) int {
	if a.root != nil {
		flags := a.root.PersistentFlags()
		if flags.Changed("max-renderer-processes") {
			return a.opts.maxRendererProcesses
		}
	}
	if strings.TrimSpace(os.Getenv("CDP_MAX_RENDERER_PROCESSES")) != "" {
		return a.opts.maxRendererProcesses
	}
	cfg, err := config.Load(a.opts.config)
	if err == nil && cfg.Browser.ResourceBudget.MaxRendererProcesses > 0 {
		return cfg.Browser.ResourceBudget.MaxRendererProcesses
	}
	return 0
}

func (a *app) maxRendererProcessesBudgetSource(browserMode string) string {
	if a.root != nil && a.root.PersistentFlags().Changed("max-renderer-processes") {
		return "flag"
	}
	if strings.TrimSpace(os.Getenv("CDP_MAX_RENDERER_PROCESSES")) != "" {
		return "env"
	}
	if cfg, err := config.Load(a.opts.config); err == nil && cfg.Browser.ResourceBudget.MaxRendererProcesses > 0 {
		return "config"
	}
	return "disabled"
}

func (a *app) maxTabsBudget(browserMode string) (int, string) {
	if a.root != nil {
		flags := a.root.PersistentFlags()
		if flags.Changed("max-tabs") && a.opts.maxTabs > 0 {
			return a.opts.maxTabs, "flag"
		}
	}
	if strings.TrimSpace(os.Getenv("CDP_MAX_TABS")) != "" && a.opts.maxTabs > 0 {
		return a.opts.maxTabs, "env"
	}
	cfg, err := config.Load(a.opts.config)
	if err == nil && cfg.Browser.ResourceBudget.MaxTabs > 0 {
		return cfg.Browser.ResourceBudget.MaxTabs, "config"
	}
	return cdp.DefaultMaxTabsForMode(browserMode), "mode_default"
}

func (a *app) enforceBrowserBudgetForNewPage(ctx context.Context, client cdp.CommandClient) (cdp.BrowserResourceBudget, error) {
	budget, err := a.browserBudget(ctx, client)
	if err != nil {
		return cdp.BrowserResourceBudget{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("check browser resource budget: %v", err),
			ExitConnection,
			a.connectionRemediationCommands(),
		)
	}
	if budget.OverBudgetForNewPage() && !a.opts.allowOverBudget {
		message := fmt.Sprintf("browser resource budget exceeded: %d/%d tabs, %d/%d windows", budget.TabCount, budget.MaxTabs, budget.WindowCount, budget.MaxWindows)
		if budget.MaxRendererProcesses > 0 {
			rendererCount := "unknown"
			if budget.RendererCountKnown {
				rendererCount = fmt.Sprint(budget.RendererProcessCount)
			}
			message += fmt.Sprintf(", %s/%d renderer processes", rendererCount, budget.MaxRendererProcesses)
		}
		return budget, commandErrorWithData(
			"browser_resource_budget_exceeded",
			"resource_budget",
			message,
			ExitConnection,
			[]string{"cdp pages --json", "cdp page cleanup --workflow-created --close --json", "cdp doctor --check browser-budget --json"},
			map[string]any{"resource_budget": budget},
		)
	}
	return budget, nil
}

func (a *app) browserHealthSnapshot(ctx context.Context, status daemon.Status, includeProcessInfo bool) map[string]any {
	health := map[string]any{
		"state":                       daemonHealthState(status),
		"usable":                      false,
		"reasons":                     []string{},
		"degraded_reasons":            []string{},
		"browser_mode":                status.BrowserMode,
		"connection_mode":             status.ConnectionMode,
		"browser_endpoint_reachable":  status.BrowserProbe.State == "cdp_available",
		"daemon_process_running":      status.ProcessRunning,
		"daemon_rpc_ready":            false,
		"managed_chrome_owned":        false,
		"recent_crashes":              []map[string]any{},
		"crash_capture":               "not_enabled",
		"target_resource_attribution": cdp.UnavailableTargetResourceAttribution(),
		"next_commands":               status.NextCommands,
	}
	if status.ProcessIdentityState == daemon.RuntimeProcessStateIdentityMismatch || status.ProcessIdentityState == daemon.RuntimeProcessStateIdentityUnavailable {
		health["daemon_process_identity_state"] = status.ProcessIdentityState
		health["reasons"] = appendStringReasons(health["reasons"], "daemon_process_identity_unverified")
	}
	health["daemon_processes_by_mode"] = a.daemonProcessesByMode(ctx)
	if strings.EqualFold(status.BrowserMode, string(config.BrowserModeHeadless)) {
		health["managed_processes"] = a.managedProcessLifecycleStatus(ctx, status)
	}
	if status.Runtime != nil {
		health["runtime"] = map[string]any{
			"pid":          status.Runtime.PID,
			"browser_mode": status.Runtime.BrowserMode,
			"started_at":   status.Runtime.StartedAt,
			"socket_path":  status.Runtime.SocketPath,
		}
	}
	logs := a.daemonLogHealth(ctx, 50)
	for key, value := range logs {
		health[key] = value
	}
	if status.Runtime == nil || !status.ProcessRunning {
		a.applyManagedBrowserHealth(ctx, health, status.Runtime)
		if strings.EqualFold(status.BrowserMode, string(config.BrowserModeHeadless)) {
			health["state"] = "degraded"
			health["code"] = headlessHealthFailureCode(status, health)
			health["next_commands"] = uniqueCommands(a.connectionRemediationCommands(), []string{modeScopedCommand(a.browserModeName(), "daemon logs --tail 50 --json")})
		}
		health["reasons"] = appendStringReasons(health["reasons"], daemonHealthState(status))
		return finalizeBrowserHealth(a.browserModeName(), health)
	}
	if !status.RuntimeSocketReady {
		a.applyManagedBrowserHealth(ctx, health, status.Runtime)
		health["reasons"] = appendStringReasons(health["reasons"], daemonHealthState(status))
		health["next_commands"] = uniqueCommands(toStringSlice(health["next_commands"]), status.NextCommands, a.connectionRemediationCommands())
		if strings.EqualFold(status.BrowserMode, string(config.BrowserModeHeadless)) {
			health["state"] = "degraded"
			health["code"] = headlessHealthFailureCode(status, health)
		}
		return finalizeBrowserHealth(a.browserModeName(), health)
	}
	client := daemon.RuntimeClient{Runtime: *status.Runtime}
	budgetOpts := a.browserResourceBudgetOptions()
	budgetOpts.BrowserMode = status.BrowserMode
	budgetOpts.ConnectionMode = status.ConnectionMode
	budget, err := cdp.BrowserBudget(ctx, client, budgetOpts)
	if err != nil {
		health["state"] = "degraded"
		health["code"] = headlessHealthFailureCode(status, health)
		health["reasons"] = appendStringReasons(health["reasons"], "target_list_failed")
		health["target_list_error"] = err.Error()
		return finalizeBrowserHealth(a.browserModeName(), health)
	}
	health["browser_endpoint_reachable"] = true
	health["daemon_rpc_ready"] = true
	health["usable"] = true
	applyBudgetToHealth(health, budget)
	a.applyManagedBrowserHealth(ctx, health, status.Runtime)
	if includeProcessInfo {
		processInfo, err := cdp.CollectProcessInfo(ctx, client)
		if err != nil {
			health["process_info_error"] = err.Error()
		} else {
			health["process_info"] = processInfo
		}
	}
	if len(toStringSlice(health["reasons"])) > 0 && health["state"] == "healthy" {
		health["state"] = "degraded"
	}
	if strings.EqualFold(status.BrowserMode, string(config.BrowserModeHeadless)) && health["state"] != "healthy" {
		health["code"] = headlessHealthFailureCode(status, health)
	}
	return finalizeBrowserHealth(a.browserModeName(), health)
}

func (a *app) managedProcessLifecycleStatus(ctx context.Context, status daemon.Status) any {
	store, err := a.stateStore()
	if err != nil {
		return map[string]any{"checked": true, "state": "error", "reason": err.Error()}
	}
	result, err := browser.ReconcileManagedProcesses(ctx, store.Dir, browser.ManagedProcessReconcileOptions{
		ActivePID: managedChromeActivePID(status),
		ReadOnly:  true,
	})
	if err != nil {
		return map[string]any{"checked": true, "state": "error", "reason": err.Error()}
	}
	return result
}

func (a *app) applyManagedBrowserHealth(ctx context.Context, health map[string]any, runtime *daemon.Runtime) {
	ok, detail := managedRuntimeProcessCheck(ctx, runtime)
	if detail == nil {
		return
	}
	health["managed_browser_health"] = detail
	store, err := a.stateStore()
	if err != nil {
		health["managed_ownership"] = map[string]any{"checked": true, "owned": false, "reasons": []string{err.Error()}}
		health["managed_chrome_owned"] = false
		return
	}
	expected := browser.ManagedStatus{BrowserMode: "headless"}
	if runtime.ManagedBrowser != nil {
		expected = *runtime.ManagedBrowser
	}
	if runtime.ChromePID > 0 {
		expected.ChromePID = runtime.ChromePID
	}
	if runtime.ChromePort != "" {
		expected.DebuggingPort = runtime.ChromePort
	}
	if runtime.ManagedProfilePath != "" {
		expected.UserDataDir = runtime.ManagedProfilePath
	} else if expected.UserDataDir == "" {
		expected.UserDataDir = runtime.UserDataDir
	}
	ownership, ownershipErr := browser.VerifyManagedOwnershipContext(ctx, store.Dir, expected)
	health["managed_ownership"] = ownership
	if ownershipErr != nil {
		health["managed_chrome_owned"] = false
		detail["state"] = "ownership_check_canceled"
		detail["running"] = false
		health["state"] = "degraded"
		health["reasons"] = appendStringReasons(health["reasons"], "managed_chrome_ownership_check_canceled")
		health["next_commands"] = uniqueCommands(toStringSlice(health["next_commands"]), a.connectionRemediationCommands(), []string{modeScopedCommand(a.browserModeName(), "daemon logs --tail 50 --json")})
		return
	}
	health["managed_chrome_owned"] = ownership.Owned
	identityReason := managedOwnershipIdentityReason(ownership)
	if identityReason != "" {
		detail["state"] = identityReason
		detail["running"] = false
		ok = false
	}
	identityFailure := managedProcessIdentityFailure(detail)
	if ok {
		return
	}
	if ready, _ := health["daemon_rpc_ready"].(bool); ready {
		if !identityFailure {
			detail["state"] = "daemon_rpc_ready_pid_not_running"
		} else {
			health["state"] = "degraded"
			health["reasons"] = appendStringReasons(health["reasons"], "managed_chrome_process_identity_unverified")
		}
		detail["daemon_rpc_ready"] = true
		return
	}
	health["state"] = "degraded"
	health["reasons"] = appendStringReasons(health["reasons"], "managed_chrome_process_not_running")
	health["next_commands"] = uniqueCommands(toStringSlice(health["next_commands"]), a.connectionRemediationCommands(), []string{modeScopedCommand(a.browserModeName(), "daemon logs --tail 50 --json")})
}

func headlessHealthFailureCode(status daemon.Status, health map[string]any) string {
	if !strings.EqualFold(status.BrowserMode, string(config.BrowserModeHeadless)) {
		return ""
	}
	if !status.ProcessRunning {
		return "headless_daemon_not_running"
	}
	if ready, _ := health["daemon_rpc_ready"].(bool); !ready {
		return "headless_daemon_rpc_not_ready"
	}
	if reachable, _ := health["browser_endpoint_reachable"].(bool); !reachable {
		return "headless_browser_endpoint_unreachable"
	}
	if owned, _ := health["managed_chrome_owned"].(bool); !owned {
		return "managed_chrome_ownership_unverified"
	}
	if managed, ok := health["managed_browser_health"].(map[string]any); ok {
		if running, _ := managed["running"].(bool); !running {
			return "managed_chrome_not_running"
		}
	}
	return "headless_runtime_degraded"
}

func managedRuntimeProcessCheck(ctx context.Context, runtime *daemon.Runtime) (bool, map[string]any) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runtime == nil || !strings.EqualFold(strings.TrimSpace(runtime.BrowserMode), string(config.BrowserModeHeadless)) {
		return true, nil
	}
	if runtime.ManagedBrowser == nil && strings.TrimSpace(runtime.ManagedProfilePath) == "" && strings.TrimSpace(runtime.ProfileSeedStrategy) != "managed" && strings.TrimSpace(runtime.ChromePort) == "" && runtime.ChromePID <= 0 {
		return true, nil
	}
	chromePID := runtime.ChromePID
	if chromePID <= 0 && runtime.ManagedBrowser != nil {
		chromePID = runtime.ManagedBrowser.ChromePID
	}
	detail := map[string]any{
		"expected": true,
		"state":    "unknown",
		"running":  false,
	}
	if runtime.ManagedBrowser != nil {
		detail["managed_browser"] = runtime.ManagedBrowser
	}
	if chromePID <= 0 {
		detail["state"] = "missing_pid"
		return true, detail
	}
	detail["chrome_pid"] = chromePID
	if err := ctx.Err(); err != nil {
		detail["state"] = "process_check_canceled"
		return false, detail
	}
	running, livenessErr := managedRuntimeProcessRunning(ctx, chromePID)
	if livenessErr != nil {
		if ctx.Err() != nil || errors.Is(livenessErr, context.Canceled) || errors.Is(livenessErr, context.DeadlineExceeded) {
			detail["state"] = "process_check_canceled"
			return false, detail
		}
		detail["state"] = "process_liveness_unavailable"
		return false, detail
	}
	if err := ctx.Err(); err != nil {
		detail["state"] = "process_check_canceled"
		return false, detail
	}
	if !running {
		profile, port := managedRuntimeManagedProfileAndPort(runtime)
		if profile != "" {
			fallbackCtx, cancel := context.WithTimeout(ctx, time.Second)
			endpointLive := managedRuntimeEndpointReachable(fallbackCtx, profile, port)
			cancel()
			if err := ctx.Err(); err != nil {
				detail["state"] = "process_check_canceled"
				return false, detail
			}
			if endpointLive {
				detail["state"] = "running"
				detail["running"] = true
				detail["liveness_source"] = "debugging_endpoint"
				return true, detail
			}
		}
		detail["state"] = "process_not_running"
		return false, detail
	}
	if isStrongManagedProcessStartIdentity(runtime.ChromeProcessStartTime) {
		identityCtx, cancel := context.WithTimeout(ctx, time.Second)
		actual, err := processgroup.ProcessStartTime(identityCtx, chromePID)
		cancel()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				detail["state"] = "process_check_canceled"
				return false, detail
			}
			detail["state"] = "process_identity_unavailable"
			return false, detail
		}
		if actual != strings.TrimSpace(runtime.ChromeProcessStartTime) {
			detail["state"] = "process_identity_mismatch"
			return false, detail
		}
	}
	detail["state"] = "running"
	detail["running"] = true
	detail["liveness_source"] = "recorded_pid"
	return true, detail
}

func managedRuntimeManagedProfileAndPort(runtime *daemon.Runtime) (string, string) {
	if runtime == nil {
		return "", ""
	}
	profile := strings.TrimSpace(runtime.ManagedProfilePath)
	port := strings.TrimSpace(runtime.ChromePort)
	if runtime.ManagedBrowser != nil {
		if profile == "" {
			profile = strings.TrimSpace(runtime.ManagedBrowser.UserDataDir)
		}
		if port == "" {
			port = strings.TrimSpace(runtime.ManagedBrowser.DebuggingPort)
		}
	}
	return profile, port
}

func isStrongManagedProcessStartIdentity(value string) bool {
	return processgroup.IsStrongProcessStartIdentity(value)
}

func managedOwnershipIdentityReason(evidence browser.ManagedOwnershipEvidence) string {
	for _, reason := range evidence.Reasons {
		switch reason {
		case "process_start_identity_mismatch", "process_start_identity_unavailable":
			return reason
		}
	}
	return ""
}

func managedProcessIdentityFailure(detail map[string]any) bool {
	state, _ := detail["state"].(string)
	return state == "process_identity_mismatch" || state == "process_identity_unavailable"
}

func managedProcessCheckCanceled(detail map[string]any) bool {
	state, _ := detail["state"].(string)
	return state == "process_check_canceled"
}

func (a *app) daemonProcessesByMode(ctx context.Context) map[string]any {
	store, err := a.stateStore()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	out := map[string]any{}
	for _, mode := range []string{"headed", "headless"} {
		summary := map[string]any{
			"browser_mode":           mode,
			"daemon_process_running": false,
			"daemon_rpc_ready":       false,
			"chrome_process_count":   0,
			"process_type_counts":    map[string]int{},
			"connected":              false,
		}
		runtime, ok, err := daemon.LoadRuntimeForMode(ctx, store.Dir, mode)
		if err != nil {
			summary["runtime_error"] = err.Error()
			out[mode] = summary
			continue
		}
		if !ok {
			summary["state"] = "not_running"
			out[mode] = summary
			continue
		}
		summary["state"] = "runtime_present"
		summary["daemon_pid"] = runtime.PID
		summary["socket_path"] = runtime.SocketPath
		if runtime.ChromePID > 0 {
			summary["managed_chrome_pid"] = runtime.ChromePID
		}
		if runtime.ChromePort != "" {
			summary["managed_chrome_port"] = runtime.ChromePort
		}
		processCheck := daemon.CheckRuntimeProcess(ctx, runtime)
		summary["daemon_process_identity_state"] = processCheck.State
		running := processCheck.Running
		ready := running && daemon.RuntimeSocketReady(ctx, runtime)
		summary["daemon_process_running"] = running
		summary["daemon_rpc_ready"] = ready
		if !ready {
			if processCheck.State == daemon.RuntimeProcessStateIdentityMismatch || processCheck.State == daemon.RuntimeProcessStateIdentityUnavailable {
				summary["state"] = processCheck.State
			}
			out[mode] = summary
			continue
		}
		processInfo, err := cdp.CollectProcessInfo(ctx, daemon.RuntimeClient{Runtime: runtime})
		if err != nil {
			summary["process_info_error"] = err.Error()
			out[mode] = summary
			continue
		}
		summary["state"] = "connected"
		summary["connected"] = true
		summary["chrome_process_count"] = processInfo.ProcessCount
		summary["process_type_counts"] = processInfo.TypeCounts
		out[mode] = summary
	}
	return out
}

func daemonHealthState(status daemon.Status) string {
	if status.ProcessRunning && status.State == "running" {
		return "healthy"
	}
	if status.State == "passive" && !status.ProcessRunning {
		return "passive_no_process"
	}
	if status.State == "permission_pending" {
		return "permission_pending"
	}
	if status.State == "stale_state" {
		return "stale_runtime"
	}
	if status.State == "runtime_socket_unready" {
		return "daemon_socket_unready"
	}
	if status.State == "chrome_unavailable" || status.State == "disconnected" {
		return "browser_unreachable"
	}
	return status.State
}

func (a *app) daemonLogHealth(ctx context.Context, tail int) map[string]any {
	out := map[string]any{}
	store, err := a.stateStore()
	if err != nil {
		return out
	}
	entries, err := daemon.ReadLogsForMode(ctx, store.Dir, a.browserModeName(), tail)
	if err != nil {
		return out
	}
	startIndex := 0
	for i := len(entries) - 1; i >= 0; i-- {
		event := strings.ToLower(strings.TrimSpace(entries[i].Event))
		if event == "health_check_validated" || event == "managed_process_history_compacted" || event == "managed_process_repair_validated" {
			startIndex = i + 1
			break
		}
	}
	historicalWarnings := 0
	historicalErrors := 0
	for _, entry := range entries[:startIndex] {
		level := strings.ToLower(strings.TrimSpace(entry.Level))
		if level == "warn" || level == "warning" {
			historicalWarnings++
		}
		if level == "error" {
			historicalErrors++
		}
	}
	entries = entries[startIndex:]
	retiredHoldPIDs := retiredDaemonHoldPIDs(entries)
	warns := 0
	errs := 0
	lastDisconnect := ""
	recentCrashes := []map[string]any{}
	reasons := []string{}
	for _, entry := range entries {
		if _, retired := retiredHoldPIDs[entry.PID]; retired {
			continue
		}
		level := strings.ToLower(strings.TrimSpace(entry.Level))
		if level == "warn" || level == "warning" {
			warns++
		}
		if level == "error" {
			errs++
		}
		text := strings.ToLower(entry.Event + " " + entry.Message)
		if strings.Contains(text, "connection") || strings.Contains(text, "browser") || strings.Contains(text, "websocket") {
			if level == "warn" || level == "warning" || level == "error" {
				lastDisconnect = strings.TrimSpace(entry.Time + " " + entry.Event + " " + entry.Message)
			}
		}
		if strings.Contains(text, "failed to get reader") || strings.Contains(text, "failed to read json message") || strings.Contains(text, "use of closed network connection") {
			reasons = appendStringReasons(reasons, "recent_keepalive_read_error")
		}
		if crash, ok := daemonCrashLogEntry(entry); ok {
			recentCrashes = append(recentCrashes, crash)
			if len(recentCrashes) > 5 {
				recentCrashes = recentCrashes[1:]
			}
		}
	}
	if warns >= 3 {
		reasons = appendStringReasons(reasons, "high_warning_count")
	}
	if len(recentCrashes) >= 2 {
		reasons = appendStringReasons(reasons, "repeated_connection_churn")
	}
	out["recent_log_warnings"] = warns
	out["recent_log_errors"] = errs
	out["historical_log_warnings"] = historicalWarnings
	out["historical_log_errors"] = historicalErrors
	if lastDisconnect != "" {
		out["last_browser_keepalive_error"] = lastDisconnect
	}
	if len(recentCrashes) > 0 {
		out["crash_capture"] = "daemon_logs"
		out["recent_crashes"] = recentCrashes
	}
	if len(retiredHoldPIDs) > 0 {
		pids := make([]int, 0, len(retiredHoldPIDs))
		for pid := range retiredHoldPIDs {
			pids = append(pids, pid)
		}
		sort.Ints(pids)
		out["retired_hold_pids"] = pids
	}
	if len(reasons) > 0 {
		out["degraded_reasons"] = reasons
	}
	return out
}

func retiredDaemonHoldPIDs(entries []daemon.LogEntry) map[int]struct{} {
	retired := map[int]struct{}{}
	for _, entry := range entries {
		if entry.PID <= 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(entry.Event)) {
		case "hold_superseded", "hold_reclaimed":
			retired[entry.PID] = struct{}{}
		}
	}
	return retired
}

func daemonCrashLogEntry(entry daemon.LogEntry) (map[string]any, bool) {
	event := strings.TrimSpace(entry.Event)
	level := strings.ToLower(strings.TrimSpace(entry.Level))
	message := strings.TrimSpace(entry.Message)
	crashType := daemonCrashLogType(event, level, message)
	if crashType == "" {
		return nil, false
	}
	out := map[string]any{
		"type":  crashType,
		"event": event,
		"level": level,
	}
	if strings.TrimSpace(entry.Time) != "" {
		out["time"] = strings.TrimSpace(entry.Time)
	}
	if message != "" {
		out["message"] = message
	}
	if entry.PID > 0 {
		out["pid"] = entry.PID
	}
	return out, true
}

func daemonCrashLogType(event, level, message string) string {
	event = strings.ToLower(strings.TrimSpace(event))
	level = strings.ToLower(strings.TrimSpace(level))
	message = strings.ToLower(strings.TrimSpace(message))
	switch event {
	case "hold_connection_ended":
		return "browser_connection_ended"
	case "browser_dial_failed":
		return "browser_dial_failed"
	case "rpc_listen_failed":
		return "daemon_rpc_listen_failed"
	case "runtime_write_failed":
		return "daemon_runtime_write_failed"
	}
	if level != "warn" && level != "warning" && level != "error" {
		return ""
	}
	for _, needle := range []string{"failed to get reader", "failed to read frame", "broken pipe", "connection is closed", "use of closed network connection", "websocket"} {
		if strings.Contains(message, needle) {
			return "daemon_connection_error"
		}
	}
	return ""
}

func applyBudgetToHealth(health map[string]any, budget cdp.BrowserResourceBudget) {
	health["tab_count"] = budget.TabCount
	health["max_tabs"] = budget.MaxTabs
	health["max_tabs_source"] = budget.MaxTabsSource
	health["tabs_over_budget"] = budget.TabsOverBudget
	health["window_count"] = budget.WindowCount
	health["max_windows"] = budget.MaxWindows
	health["windows_over_budget"] = budget.WindowsOverBudget
	health["window_count_known"] = budget.WindowCountKnown
	health["target_type_counts"] = budget.TargetTypeCounts
	health["attached_page_count"] = budget.AttachedPageCount
	health["renderer_process_count"] = budget.RendererProcessCount
	health["max_renderer_processes"] = budget.MaxRendererProcesses
	health["max_renderer_processes_source"] = budget.MaxRendererProcessesSource
	health["renderer_count_known"] = budget.RendererCountKnown
	health["renderer_processes_over_budget"] = budget.RendererProcessesOverBudget
	health["target_resource_attribution"] = budget.TargetResourceAttribution
	health["resource_budget"] = budget
	health["reasons"] = appendStringReasons(health["reasons"], budget.Reasons()...)
}

func finalizeBrowserHealth(browserMode string, health map[string]any) map[string]any {
	degradedReasons := appendStringReasons(health["degraded_reasons"], toStringSlice(health["reasons"])...)
	degradedReasons = appendStringReasons(degradedReasons, toStringSlice(health["degraded_reasons"])...)
	health["degraded_reasons"] = degradedReasons
	if len(degradedReasons) > 0 && health["state"] == "healthy" {
		health["state"] = "degraded"
	}
	usable, _ := health["usable"].(bool)
	if health["state"] == "healthy" {
		health["usable"] = true
		usable = true
	}
	if health["state"] == "degraded" {
		urgency := "required"
		command := modeScopedCommand(browserMode, "daemon keepalive --repair --json")
		if usable {
			urgency = "before_long_crawl"
			command = modeScopedCommand(browserMode, "daemon health-check --repair --json")
		}
		health["recommended_repair"] = map[string]any{
			"command": command,
			"urgency": urgency,
		}
	}
	return health
}

func appendStringReasons(value any, reasons ...string) []string {
	out := toStringSlice(value)
	seen := map[string]bool{}
	for _, reason := range out {
		seen[reason] = true
	}
	for _, reason := range reasons {
		reason = strings.TrimSpace(reason)
		if reason == "" || seen[reason] {
			continue
		}
		seen[reason] = true
		out = append(out, reason)
	}
	return out
}

func toStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return []string{}
	}
}
