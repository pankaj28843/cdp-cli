package cli

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

const (
	defaultMinFreeMemoryMB = int64(1024)
	defaultMinFreeDiskMB   = int64(1024)
	defaultMaxLoadPerCPU   = 2.0
)

type resourcePreflightPolicy struct {
	MinFreeMemoryMB int64   `json:"min_free_memory_mb"`
	MinFreeDiskMB   int64   `json:"min_free_disk_mb"`
	MaxLoadPerCPU   float64 `json:"max_load_per_cpu"`
}

type resourcePreflightCheck struct {
	Name          string  `json:"name"`
	Status        string  `json:"status"`
	Reason        string  `json:"reason,omitempty"`
	Source        string  `json:"source,omitempty"`
	State         string  `json:"state,omitempty"`
	AvailableMB   int64   `json:"available_mb,omitempty"`
	TotalMB       int64   `json:"total_mb,omitempty"`
	RequiredMB    int64   `json:"required_mb,omitempty"`
	Load1         float64 `json:"load1,omitempty"`
	LoadPerCPU    float64 `json:"load_per_cpu,omitempty"`
	MaxLoadPerCPU float64 `json:"max_load_per_cpu,omitempty"`
	CPUCount      int     `json:"cpu_count,omitempty"`
	LiveCount     int     `json:"live_count,omitempty"`
	StaleCount    int     `json:"stale_count,omitempty"`
	MaxLiveCount  int     `json:"max_live_count,omitempty"`
	TabCount      int     `json:"tab_count,omitempty"`
	MaxTabs       int     `json:"max_tabs,omitempty"`
	WindowCount   int     `json:"window_count,omitempty"`
	MaxWindows    int     `json:"max_windows,omitempty"`
	WindowKnown   bool    `json:"window_count_known,omitempty"`
	RendererCount int     `json:"renderer_process_count,omitempty"`
	MaxRenderers  int     `json:"max_renderer_processes,omitempty"`
	RendererKnown bool    `json:"renderer_count_known,omitempty"`
	Retryable     bool    `json:"retryable"`
	NextCommand   string  `json:"next_command,omitempty"`
}

type resourcePreflightResult struct {
	Checked          bool                     `json:"checked"`
	BrowserMode      string                   `json:"browser_mode"`
	State            string                   `json:"state"`
	Status           string                   `json:"status"`
	HeavyWorkAllowed bool                     `json:"heavy_work_allowed"`
	Policy           resourcePreflightPolicy  `json:"policy"`
	Checks           []resourcePreflightCheck `json:"checks"`
	Reasons          []string                 `json:"reasons"`
	NextCommands     []string                 `json:"next_commands"`
}

type hostResourceSnapshot struct {
	Memory resourcePreflightCheck
	Disk   resourcePreflightCheck
	Load   resourcePreflightCheck
}

func (a *app) maintenanceResourcePreflight(ctx context.Context, status daemon.Status, health map[string]any) resourcePreflightResult {
	store, err := a.stateStore()
	if err != nil {
		return evaluateResourcePreflight(a.resourcePreflightPolicy(), a.browserModeName(), hostResourceSnapshot{
			Memory: unknownResourceCheck("memory", "state directory unavailable: "+err.Error()),
			Disk:   unknownResourceCheck("disk", "state directory unavailable: "+err.Error()),
			Load:   probeLoadAverage(ctx, a.resourcePreflightPolicy()),
		}, nil, health)
	}
	return a.maintenanceResourcePreflightForState(ctx, store.Dir, status, health)
}

func (a *app) maintenanceResourcePreflightForState(ctx context.Context, stateDir string, status daemon.Status, health map[string]any) resourcePreflightResult {
	return a.maintenanceResourcePreflightForStateWithManaged(ctx, stateDir, status, health, nil)
}

func (a *app) maintenanceResourcePreflightForStateWithManaged(ctx context.Context, stateDir string, status daemon.Status, health map[string]any, managed *browser.ManagedProcessReconcileResult) resourcePreflightResult {
	policy := a.resourcePreflightPolicy()
	snapshot := hostResourceSnapshot{
		Memory: probeAvailableMemory(ctx, policy),
		Disk:   probeFreeDisk(ctx, stateDir, policy),
		Load:   probeLoadAverage(ctx, policy),
	}
	browserMode := strings.TrimSpace(status.BrowserMode)
	if browserMode == "" {
		browserMode = a.browserModeName()
	}
	if managed == nil && strings.EqualFold(browserMode, string(config.BrowserModeHeadless)) {
		reconcile, err := browser.ReconcileManagedProcesses(ctx, stateDir, browser.ManagedProcessReconcileOptions{
			ActivePID: managedChromeActivePID(status),
			ReadOnly:  true,
		})
		if err != nil {
			reconcile = browser.ManagedProcessReconcileResult{
				Checked:      true,
				State:        "error",
				BrowserMode:  string(config.BrowserModeHeadless),
				Reason:       err.Error(),
				NextCommands: []string{"cdp --browser-mode headless daemon stop --force-managed --json", "cdp --browser-mode headless daemon keepalive --managed-process-sweep --repair --force --json"},
			}
		}
		managed = &reconcile
	}
	return evaluateResourcePreflight(policy, browserMode, snapshot, managed, health)
}

func (a *app) resourcePreflightPolicy() resourcePreflightPolicy {
	policy := resourcePreflightPolicy{
		MinFreeMemoryMB: defaultMinFreeMemoryMB,
		MinFreeDiskMB:   defaultMinFreeDiskMB,
		MaxLoadPerCPU:   defaultMaxLoadPerCPU,
	}
	cfg, err := config.Load(a.opts.config)
	if err != nil {
		return policy
	}
	if cfg.Browser.ResourceBudget.MinFreeMemoryMB > 0 {
		policy.MinFreeMemoryMB = int64(cfg.Browser.ResourceBudget.MinFreeMemoryMB)
	}
	if cfg.Browser.ResourceBudget.MinFreeDiskMB > 0 {
		policy.MinFreeDiskMB = int64(cfg.Browser.ResourceBudget.MinFreeDiskMB)
	}
	if cfg.Browser.ResourceBudget.MaxLoadPerCPU > 0 {
		policy.MaxLoadPerCPU = cfg.Browser.ResourceBudget.MaxLoadPerCPU
	}
	return policy
}

func evaluateResourcePreflight(policy resourcePreflightPolicy, browserMode string, host hostResourceSnapshot, managed *browser.ManagedProcessReconcileResult, health map[string]any) resourcePreflightResult {
	result := resourcePreflightResult{
		Checked:          true,
		BrowserMode:      strings.TrimSpace(browserMode),
		State:            "sufficient",
		Status:           "pass",
		HeavyWorkAllowed: true,
		Policy:           policy,
		Checks:           []resourcePreflightCheck{},
		Reasons:          []string{},
		NextCommands:     []string{},
	}
	add := func(check resourcePreflightCheck) {
		check.Status = normalizeResourceCheckStatus(check.Status)
		result.Checks = append(result.Checks, check)
		if check.Status != "pass" {
			result.Reasons = appendStringReasons(result.Reasons, check.Name+"_"+check.Status)
		}
		if check.Reason != "" && check.Status == "skip" {
			result.Reasons = appendStringReasons(result.Reasons, check.Reason)
		}
		if check.NextCommand != "" {
			result.NextCommands = uniqueCommands(result.NextCommands, []string{check.NextCommand})
		}
	}
	add(host.Memory)
	add(host.Disk)
	add(host.Load)
	if managed != nil {
		add(managedProcessResourceCheck(*managed))
	}
	if check, ok := browserBudgetResourceCheck(health); ok {
		add(check)
	}

	hasSkip := false
	hasWarn := false
	hasUnknown := false
	for _, check := range result.Checks {
		switch check.Status {
		case "skip":
			hasSkip = true
		case "warn":
			hasWarn = true
		case "unknown":
			hasUnknown = true
		}
	}
	switch {
	case hasSkip:
		result.State = "blocked"
		result.Status = "skip"
		result.HeavyWorkAllowed = false
	case hasWarn:
		result.State = "warning"
		result.Status = "warn"
	case hasUnknown:
		result.State = "unknown"
		result.Status = "unknown"
	default:
		result.State = "sufficient"
		result.Status = "pass"
	}
	if len(result.NextCommands) == 0 {
		result.NextCommands = []string{"cdp browser preflight --json", "cdp cron status --json"}
	}
	return result
}

func normalizeResourceCheckStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pass", "warn", "skip", "unknown":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "unknown"
	}
}

func unknownResourceCheck(name, reason string) resourcePreflightCheck {
	return resourcePreflightCheck{Name: name, Status: "unknown", Reason: reason, Retryable: true}
}

func probeAvailableMemory(ctx context.Context, policy resourcePreflightPolicy) resourcePreflightCheck {
	select {
	case <-ctx.Done():
		return unknownResourceCheck("memory", ctx.Err().Error())
	default:
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return unknownResourceCheck("memory", "memory probe unavailable")
	}
	values := parseMeminfoKB(string(data))
	availableKB := values["MemAvailable"]
	totalKB := values["MemTotal"]
	if availableKB <= 0 {
		return unknownResourceCheck("memory", "MemAvailable unavailable")
	}
	check := resourcePreflightCheck{
		Name:        "memory",
		Status:      "pass",
		Source:      "/proc/meminfo",
		AvailableMB: availableKB / 1024,
		TotalMB:     totalKB / 1024,
		RequiredMB:  policy.MinFreeMemoryMB,
		Retryable:   true,
	}
	switch {
	case check.AvailableMB < policy.MinFreeMemoryMB:
		check.Status = "skip"
		check.Reason = "memory_below_minimum"
	case check.AvailableMB < policy.MinFreeMemoryMB*2:
		check.Status = "warn"
		check.Reason = "memory_near_minimum"
	}
	return check
}

func parseMeminfoKB(data string) map[string]int64 {
	values := map[string]int64{}
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		values[key] = value
	}
	return values
}

func probeFreeDisk(ctx context.Context, stateDir string, policy resourcePreflightPolicy) resourcePreflightCheck {
	select {
	case <-ctx.Done():
		return unknownResourceCheck("disk", ctx.Err().Error())
	default:
	}
	path := existingPathForStatfs(stateDir)
	if path == "" {
		return unknownResourceCheck("disk", "disk probe path unavailable")
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return unknownResourceCheck("disk", "disk probe unavailable")
	}
	blockSize := int64(stat.Bsize)
	if blockSize <= 0 {
		return unknownResourceCheck("disk", "disk block size unavailable")
	}
	availableMB := int64(stat.Bavail) * blockSize / 1024 / 1024
	totalMB := int64(stat.Blocks) * blockSize / 1024 / 1024
	check := resourcePreflightCheck{
		Name:        "disk",
		Status:      "pass",
		Source:      "statfs",
		AvailableMB: availableMB,
		TotalMB:     totalMB,
		RequiredMB:  policy.MinFreeDiskMB,
		Retryable:   true,
	}
	switch {
	case availableMB < policy.MinFreeDiskMB:
		check.Status = "skip"
		check.Reason = "disk_below_minimum"
	case availableMB < policy.MinFreeDiskMB*2:
		check.Status = "warn"
		check.Reason = "disk_near_minimum"
	}
	return check
}

func existingPathForStatfs(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	path = filepath.Clean(path)
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func probeLoadAverage(ctx context.Context, policy resourcePreflightPolicy) resourcePreflightCheck {
	select {
	case <-ctx.Done():
		return unknownResourceCheck("load", ctx.Err().Error())
	default:
	}
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return unknownResourceCheck("load", "load probe unavailable")
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return unknownResourceCheck("load", "load average unavailable")
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return unknownResourceCheck("load", "load average parse failed")
	}
	cpus := runtime.NumCPU()
	if cpus <= 0 {
		return unknownResourceCheck("load", "cpu count unavailable")
	}
	loadPerCPU := load1 / float64(cpus)
	check := resourcePreflightCheck{
		Name:          "load",
		Status:        "pass",
		Source:        "/proc/loadavg",
		Load1:         roundFloat(load1, 3),
		LoadPerCPU:    roundFloat(loadPerCPU, 3),
		MaxLoadPerCPU: policy.MaxLoadPerCPU,
		CPUCount:      cpus,
		Retryable:     true,
	}
	switch {
	case loadPerCPU > policy.MaxLoadPerCPU:
		check.Status = "skip"
		check.Reason = "load_above_maximum"
	case loadPerCPU > policy.MaxLoadPerCPU*0.75:
		check.Status = "warn"
		check.Reason = "load_near_maximum"
	}
	return check
}

func roundFloat(value float64, precision int) float64 {
	scale := math.Pow10(precision)
	return math.Round(value*scale) / scale
}

func managedProcessResourceCheck(reconcile browser.ManagedProcessReconcileResult) resourcePreflightCheck {
	check := resourcePreflightCheck{
		Name:         "managed_processes",
		Status:       "pass",
		State:        reconcile.State,
		LiveCount:    reconcile.LiveCount,
		StaleCount:   reconcile.StaleCount,
		MaxLiveCount: 1,
		Retryable:    true,
	}
	if len(reconcile.NextCommands) > 0 {
		check.NextCommand = reconcile.NextCommands[0]
	} else {
		check.NextCommand = "cdp --browser-mode headless daemon keepalive --managed-process-sweep --repair --force --json"
	}
	switch reconcile.State {
	case "healthy", "reaped":
		if reconcile.LiveCount > 1 {
			check.Status = "skip"
			check.Reason = "managed_process_over_budget"
		} else if reconcile.StaleCount > 0 {
			check.Status = "warn"
			check.Reason = "managed_process_stale_records"
		}
	case "over_budget", "degraded", "error":
		check.Status = "skip"
		check.Reason = "managed_process_guard_degraded"
	default:
		check.Status = "unknown"
		check.Reason = "managed_process_state_unknown"
	}
	return check
}

func browserBudgetResourceCheck(health map[string]any) (resourcePreflightCheck, bool) {
	if health == nil {
		return resourcePreflightCheck{}, false
	}
	tabsOver, _ := health["tabs_over_budget"].(bool)
	windowsOver, _ := health["windows_over_budget"].(bool)
	tabCount := intFromAny(health["tab_count"])
	maxTabs := intFromAny(health["max_tabs"])
	windowCount := intFromAny(health["window_count"])
	maxWindows := intFromAny(health["max_windows"])
	windowKnown, _ := health["window_count_known"].(bool)
	if _, ok := health["resource_budget"]; !ok && tabCount == 0 && maxTabs == 0 && windowCount == 0 && maxWindows == 0 {
		return resourcePreflightCheck{}, false
	}
	check := resourcePreflightCheck{
		Name:        "browser_budget",
		Status:      "pass",
		TabCount:    tabCount,
		MaxTabs:     maxTabs,
		WindowCount: windowCount,
		MaxWindows:  maxWindows,
		WindowKnown: windowKnown,
		Retryable:   true,
		NextCommand: "cdp page cleanup --json",
	}
	renderersOver, _ := health["renderer_processes_over_budget"].(bool)
	rendererCountKnown, _ := health["renderer_count_known"].(bool)
	maxRenderers := intFromAny(health["max_renderer_processes"])
	rendererCount := intFromAny(health["renderer_process_count"])
	check.RendererCount = rendererCount
	check.MaxRenderers = maxRenderers
	check.RendererKnown = rendererCountKnown
	if tabsOver || windowsOver || renderersOver {
		check.Status = "skip"
		check.Reason = "browser_budget_exceeded"
	} else if maxRenderers > 0 && !rendererCountKnown {
		check.Status = "skip"
		check.Reason = "renderer_process_count_unknown"
	}
	return check, true
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return 0
	}
}
