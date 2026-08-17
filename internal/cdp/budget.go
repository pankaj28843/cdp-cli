package cdp

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	DefaultHeadedMaxTabs   = 15
	DefaultHeadlessMaxTabs = 25
	DefaultMaxTabs         = DefaultHeadedMaxTabs
	DefaultMaxWindows      = 5
)

type BrowserResourceBudgetOptions struct {
	MaxTabs                    int
	MaxTabsSource              string
	MaxWindows                 int
	MaxRendererProcesses       int
	MaxRendererProcessesSource string
	BrowserMode                string
	ConnectionMode             string
}

type TargetResourceAttribution struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

func UnavailableTargetResourceAttribution() TargetResourceAttribution {
	return TargetResourceAttribution{
		State:  "unavailable",
		Reason: "Chrome CDP does not expose a stable target-to-renderer process mapping",
	}
}

type WindowMappingFailure struct {
	TargetID string `json:"target_id"`
	Error    string `json:"error"`
}

type BrowserResourceBudget struct {
	TabCount                    int                       `json:"tab_count"`
	MaxTabs                     int                       `json:"max_tabs"`
	MaxTabsSource               string                    `json:"max_tabs_source,omitempty"`
	TabsOverBudget              bool                      `json:"tabs_over_budget"`
	WindowCount                 int                       `json:"window_count"`
	MaxWindows                  int                       `json:"max_windows"`
	WindowsOverBudget           bool                      `json:"windows_over_budget"`
	WindowCountKnown            bool                      `json:"window_count_known"`
	WindowMappingFailures       []WindowMappingFailure    `json:"window_mapping_failures,omitempty"`
	TargetTypeCounts            map[string]int            `json:"target_type_counts"`
	AttachedPageCount           int                       `json:"attached_page_count"`
	RendererProcessCount        int                       `json:"renderer_process_count"`
	MaxRendererProcesses        int                       `json:"max_renderer_processes"`
	MaxRendererProcessesSource  string                    `json:"max_renderer_processes_source,omitempty"`
	RendererCountKnown          bool                      `json:"renderer_count_known"`
	RendererProcessesOverBudget bool                      `json:"renderer_processes_over_budget"`
	ProcessInfoError            string                    `json:"process_info_error,omitempty"`
	TargetResourceAttribution   TargetResourceAttribution `json:"target_resource_attribution"`
	BrowserMode                 string                    `json:"browser_mode,omitempty"`
	ConnectionMode              string                    `json:"connection_mode,omitempty"`
	Warnings                    []string                  `json:"warnings,omitempty"`
}

func BrowserBudget(ctx context.Context, client CommandClient, opts BrowserResourceBudgetOptions) (BrowserResourceBudget, error) {
	targets, err := ListTargetsWithClient(ctx, client)
	if err != nil {
		return BrowserResourceBudget{}, err
	}
	return BrowserBudgetForTargets(ctx, client, targets, opts), nil
}

func BrowserBudgetForTargets(ctx context.Context, client CommandClient, targets []TargetInfo, opts BrowserResourceBudgetOptions) BrowserResourceBudget {
	if opts.MaxTabs <= 0 {
		opts.MaxTabs = DefaultMaxTabsForMode(opts.BrowserMode)
		if strings.TrimSpace(opts.MaxTabsSource) == "" {
			opts.MaxTabsSource = "mode_default"
		}
	}
	if opts.MaxWindows <= 0 {
		opts.MaxWindows = DefaultMaxWindows
	}
	budget := BrowserResourceBudget{
		MaxTabs:                    opts.MaxTabs,
		MaxTabsSource:              strings.TrimSpace(opts.MaxTabsSource),
		MaxWindows:                 opts.MaxWindows,
		TargetTypeCounts:           map[string]int{},
		RendererCountKnown:         false,
		MaxRendererProcesses:       opts.MaxRendererProcesses,
		MaxRendererProcessesSource: strings.TrimSpace(opts.MaxRendererProcessesSource),
		TargetResourceAttribution:  UnavailableTargetResourceAttribution(),
		BrowserMode:                strings.TrimSpace(opts.BrowserMode),
		ConnectionMode:             strings.TrimSpace(opts.ConnectionMode),
	}
	pageTargets := make([]TargetInfo, 0)
	for _, target := range targets {
		budget.TargetTypeCounts[target.Type]++
		if target.Type != "page" {
			continue
		}
		budget.TabCount++
		if target.Attached {
			budget.AttachedPageCount++
		}
		pageTargets = append(pageTargets, target)
	}
	budget.TabsOverBudget = budget.MaxTabs > 0 && budget.TabCount >= budget.MaxTabs
	budget.WindowCountKnown = true
	windows := map[int]bool{}
	for _, target := range pageTargets {
		windowID, err := WindowForTarget(ctx, client, target.TargetID)
		if err != nil {
			budget.WindowCountKnown = false
			budget.WindowMappingFailures = append(budget.WindowMappingFailures, WindowMappingFailure{TargetID: target.TargetID, Error: err.Error()})
			continue
		}
		windows[windowID] = true
	}
	budget.WindowCount = len(windows)
	budget.WindowsOverBudget = budget.WindowCountKnown && budget.MaxWindows > 0 && budget.WindowCount >= budget.MaxWindows
	if opts.MaxRendererProcesses > 0 {
		processInfo, err := CollectProcessInfo(ctx, client)
		if err != nil {
			budget.ProcessInfoError = err.Error()
			budget.Warnings = append(budget.Warnings, "renderer process count is unavailable; the configured renderer budget is treated as exceeded")
		} else {
			budget.RendererCountKnown = true
			budget.RendererProcessCount = processInfo.RendererProcessCount
			budget.RendererProcessesOverBudget = budget.RendererProcessCount >= budget.MaxRendererProcesses
		}
	}
	if !budget.WindowCountKnown {
		budget.Warnings = append(budget.Warnings, "window count is conservative because Browser.getWindowForTarget failed for at least one page target")
	}
	return budget
}

func DefaultMaxTabsForMode(browserMode string) int {
	if strings.EqualFold(strings.TrimSpace(browserMode), "headless") {
		return DefaultHeadlessMaxTabs
	}
	return DefaultHeadedMaxTabs
}

func WindowForTarget(ctx context.Context, client CommandClient, targetID string) (int, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return 0, fmt.Errorf("target id is required")
	}
	var result struct {
		WindowID int `json:"windowId"`
	}
	if err := client.Call(ctx, "Browser.getWindowForTarget", map[string]any{"targetId": targetID}, &result); err != nil {
		return 0, err
	}
	if result.WindowID == 0 {
		return 0, fmt.Errorf("Browser.getWindowForTarget returned an empty window id")
	}
	return result.WindowID, nil
}

func (b BrowserResourceBudget) OverBudgetForNewPage() bool {
	return b.TabsOverBudget || b.WindowsOverBudget || b.RendererProcessesOverBudget || (b.MaxRendererProcesses > 0 && !b.RendererCountKnown)
}

func (b BrowserResourceBudget) RemainingTabs() int {
	if b.MaxTabs <= 0 {
		return 0
	}
	remaining := b.MaxTabs - b.TabCount
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (b BrowserResourceBudget) Reasons() []string {
	var reasons []string
	if b.TabsOverBudget {
		reasons = append(reasons, "tabs_over_budget")
	}
	if b.WindowsOverBudget {
		reasons = append(reasons, "windows_over_budget")
	}
	if b.RendererProcessesOverBudget {
		reasons = append(reasons, "renderer_processes_over_budget")
	}
	if b.MaxRendererProcesses > 0 && !b.RendererCountKnown {
		reasons = append(reasons, "renderer_process_count_unknown")
	}
	if !b.WindowCountKnown {
		reasons = append(reasons, "window_count_unknown")
	}
	sort.Strings(reasons)
	return reasons
}
