package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

const autoConnectHumanAction = "Open chrome://inspect/#remote-debugging in Chrome and click Allow for the cdp remote debugging request."

func safeDiagnosticCommands() []string {
	return []string{
		"cdp daemon status --json",
		"cdp doctor --check daemon --json",
		"cdp doctor --check browser-health --json",
		"cdp daemon logs --tail 50 --json",
	}
}

func permissionRemediationCommands() []string {
	return append([]string{"open chrome://inspect/#remote-debugging"}, safeDiagnosticCommands()...)
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
		MaxTabs:        maxTabs,
		MaxTabsSource:  source,
		BrowserMode:    browserMode,
		ConnectionMode: a.connectionMode(),
	}
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
		return budget, commandErrorWithData(
			"browser_resource_budget_exceeded",
			"resource_budget",
			fmt.Sprintf("browser resource budget exceeded: %d/%d tabs, %d/%d windows", budget.TabCount, budget.MaxTabs, budget.WindowCount, budget.MaxWindows),
			ExitConnection,
			[]string{"cdp pages --json", "cdp page cleanup --workflow-created --close --json", "cdp doctor --check browser-budget --json"},
			map[string]any{"resource_budget": budget},
		)
	}
	return budget, nil
}

func (a *app) browserHealthSnapshot(ctx context.Context, status daemon.Status, includeProcessInfo bool) map[string]any {
	health := map[string]any{
		"state":                      daemonHealthState(status),
		"reasons":                    []string{},
		"browser_mode":               status.BrowserMode,
		"connection_mode":            status.ConnectionMode,
		"browser_endpoint_reachable": status.BrowserProbe.State == "cdp_available",
		"daemon_process_running":     status.ProcessRunning,
		"daemon_rpc_ready":           false,
		"managed_chrome_owned":       false,
		"recent_crashes":             []map[string]any{},
		"crash_capture":              "not_enabled",
		"next_commands":              status.NextCommands,
	}
	health["daemon_processes_by_mode"] = a.daemonProcessesByMode(ctx)
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
		a.applyManagedBrowserHealth(health, status.Runtime)
		if strings.EqualFold(status.BrowserMode, string(config.BrowserModeHeadless)) {
			health["state"] = "degraded"
			health["code"] = headlessHealthFailureCode(status, health)
			health["next_commands"] = uniqueCommands(a.connectionRemediationCommands(), []string{modeScopedCommand(a.browserModeName(), "daemon logs --tail 50 --json")})
		}
		health["reasons"] = appendStringReasons(health["reasons"], daemonHealthState(status))
		return health
	}
	if !status.RuntimeSocketReady {
		a.applyManagedBrowserHealth(health, status.Runtime)
		health["reasons"] = appendStringReasons(health["reasons"], daemonHealthState(status))
		health["next_commands"] = uniqueCommands(toStringSlice(health["next_commands"]), status.NextCommands, a.connectionRemediationCommands())
		if strings.EqualFold(status.BrowserMode, string(config.BrowserModeHeadless)) {
			health["state"] = "degraded"
			health["code"] = headlessHealthFailureCode(status, health)
		}
		return health
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
		return health
	}
	health["browser_endpoint_reachable"] = true
	health["daemon_rpc_ready"] = true
	applyBudgetToHealth(health, budget)
	a.applyManagedBrowserHealth(health, status.Runtime)
	if includeProcessInfo {
		processInfo, err := collectProcessInfo(ctx, client)
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
	return health
}

func (a *app) applyManagedBrowserHealth(health map[string]any, runtime *daemon.Runtime) {
	ok, detail := managedRuntimeProcessCheck(runtime)
	if detail == nil {
		return
	}
	health["managed_browser_health"] = detail
	health["managed_chrome_owned"] = true
	if ok {
		return
	}
	if ready, _ := health["daemon_rpc_ready"].(bool); ready {
		detail["state"] = "daemon_rpc_ready_pid_not_running"
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
	if managed, ok := health["managed_browser_health"].(map[string]any); ok {
		if running, _ := managed["running"].(bool); !running {
			return "managed_chrome_not_running"
		}
	}
	return "headless_runtime_degraded"
}

func managedRuntimeProcessCheck(runtime *daemon.Runtime) (bool, map[string]any) {
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
	if !daemon.ProcessRunning(chromePID) {
		detail["state"] = "process_not_running"
		return false, detail
	}
	detail["state"] = "running"
	detail["running"] = true
	return true, detail
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
		running := daemon.RuntimeRunning(runtime)
		ready := running && daemon.RuntimeSocketReady(ctx, runtime)
		summary["daemon_process_running"] = running
		summary["daemon_rpc_ready"] = ready
		if !ready {
			out[mode] = summary
			continue
		}
		processInfo, err := collectProcessInfo(ctx, daemon.RuntimeClient{Runtime: runtime})
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
	warns := 0
	errs := 0
	lastDisconnect := ""
	recentCrashes := []map[string]any{}
	for _, entry := range entries {
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
		if crash, ok := daemonCrashLogEntry(entry); ok {
			recentCrashes = append(recentCrashes, crash)
			if len(recentCrashes) > 5 {
				recentCrashes = recentCrashes[1:]
			}
		}
	}
	out["recent_log_warnings"] = warns
	out["recent_log_errors"] = errs
	if lastDisconnect != "" {
		out["last_browser_keepalive_error"] = lastDisconnect
	}
	if len(recentCrashes) > 0 {
		out["crash_capture"] = "daemon_logs"
		out["recent_crashes"] = recentCrashes
	}
	return out
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
	health["resource_budget"] = budget
	health["reasons"] = appendStringReasons(health["reasons"], budget.Reasons()...)
}

type browserProcessRow struct {
	Type    string  `json:"type"`
	ID      int     `json:"id"`
	CPUTime float64 `json:"cpu_time"`
}

type browserProcessInfo struct {
	ProcessCount int                 `json:"process_count"`
	TypeCounts   map[string]int      `json:"type_counts"`
	Processes    []browserProcessRow `json:"processes"`
}

func collectProcessInfo(ctx context.Context, client cdp.CommandClient) (browserProcessInfo, error) {
	var result struct {
		ProcessInfo []struct {
			Type    string  `json:"type"`
			ID      int     `json:"id"`
			CPUTime float64 `json:"cpuTime"`
		} `json:"processInfo"`
	}
	if err := client.Call(ctx, "SystemInfo.getProcessInfo", map[string]any{}, &result); err != nil {
		return browserProcessInfo{}, err
	}
	info := browserProcessInfo{ProcessCount: len(result.ProcessInfo), TypeCounts: map[string]int{}, Processes: make([]browserProcessRow, 0, len(result.ProcessInfo))}
	for _, process := range result.ProcessInfo {
		info.TypeCounts[process.Type]++
		info.Processes = append(info.Processes, browserProcessRow{Type: process.Type, ID: process.ID, CPUTime: process.CPUTime})
	}
	return info, nil
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
