package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
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

func (a *app) connectionRemediationCommands() []string {
	if a.opts.autoConnect {
		return safeDiagnosticCommands()
	}
	return []string{"cdp daemon status --json", "cdp doctor --json", "cdp connection current --json"}
}

func (a *app) browserBudget(ctx context.Context, client cdp.CommandClient) (cdp.BrowserResourceBudget, error) {
	return cdp.BrowserBudget(ctx, client, cdp.BrowserResourceBudgetOptions{ConnectionMode: a.connectionMode()})
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
		"state":                  daemonHealthState(status),
		"reasons":                []string{},
		"connection_mode":        status.ConnectionMode,
		"daemon_process_running": status.ProcessRunning,
		"daemon_rpc_ready":       false,
		"recent_crashes":         []map[string]any{},
		"crash_capture":          "not_enabled",
	}
	if status.Runtime != nil {
		health["runtime"] = map[string]any{
			"pid":         status.Runtime.PID,
			"started_at":  status.Runtime.StartedAt,
			"socket_path": status.Runtime.SocketPath,
		}
	}
	logs := a.daemonLogHealth(ctx, 50)
	for key, value := range logs {
		health[key] = value
	}
	if status.Runtime == nil || !status.ProcessRunning {
		health["reasons"] = appendStringReasons(health["reasons"], daemonHealthState(status))
		return health
	}
	client := daemon.RuntimeClient{Runtime: *status.Runtime}
	budget, err := cdp.BrowserBudget(ctx, client, cdp.BrowserResourceBudgetOptions{ConnectionMode: status.ConnectionMode})
	if err != nil {
		health["state"] = "degraded"
		health["reasons"] = appendStringReasons(health["reasons"], "target_list_failed")
		health["target_list_error"] = err.Error()
		return health
	}
	health["daemon_rpc_ready"] = true
	applyBudgetToHealth(health, budget)
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
	return health
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
	entries, err := daemon.ReadLogs(ctx, store.Dir, tail)
	if err != nil {
		return out
	}
	warns := 0
	errs := 0
	lastDisconnect := ""
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
	}
	out["recent_log_warnings"] = warns
	out["recent_log_errors"] = errs
	if lastDisconnect != "" {
		out["last_browser_keepalive_error"] = lastDisconnect
	}
	return out
}

func applyBudgetToHealth(health map[string]any, budget cdp.BrowserResourceBudget) {
	health["tab_count"] = budget.TabCount
	health["max_tabs"] = budget.MaxTabs
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
