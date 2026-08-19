package cdp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type budgetFakeClient struct {
	targets     []cdp.TargetInfo
	windows     map[string]int
	failWindows bool
	processes   []map[string]any
	failProcess bool
}

func (f budgetFakeClient) Call(ctx context.Context, method string, params any, result any) error {
	switch method {
	case "Target.getTargets":
		return remarshal(map[string]any{"targetInfos": f.targets}, result)
	case "Browser.getWindowForTarget":
		if f.failWindows {
			return fmt.Errorf("method not found")
		}
		var p struct {
			TargetID string `json:"targetId"`
		}
		if err := remarshal(params, &p); err != nil {
			return err
		}
		windowID := f.windows[p.TargetID]
		if windowID == 0 {
			return fmt.Errorf("no window for target %s", p.TargetID)
		}
		return remarshal(map[string]any{"windowId": windowID}, result)
	case "SystemInfo.getProcessInfo":
		if f.failProcess {
			return fmt.Errorf("method not found")
		}
		return remarshal(map[string]any{"processInfo": f.processes}, result)
	default:
		return fmt.Errorf("unexpected method %s", method)
	}
}

func TestBrowserBudgetEnforcesConfiguredRendererLimit(t *testing.T) {
	client := budgetFakeClient{
		targets:   []cdp.TargetInfo{{TargetID: "page-1", Type: "page"}},
		windows:   map[string]int{"page-1": 1},
		processes: []map[string]any{{"type": "browser", "id": 100}, {"type": "renderer", "id": 101}, {"type": "renderer", "id": 102}},
	}
	got, err := cdp.BrowserBudget(context.Background(), client, cdp.BrowserResourceBudgetOptions{MaxRendererProcesses: 2, MaxRendererProcessesSource: "config"})
	if err != nil {
		t.Fatalf("BrowserBudget returned error: %v", err)
	}
	if !got.RendererCountKnown || got.RendererProcessCount != 2 || got.MaxRendererProcesses != 2 || got.MaxRendererProcessesSource != "config" || !got.RendererProcessesOverBudget || !got.OverBudgetForNewPage() {
		t.Fatalf("BrowserBudget = %+v, want renderer budget exceeded", got)
	}
	if got.TargetResourceAttribution.State != "unavailable" {
		t.Fatalf("target resource attribution = %+v, want explicit unavailable state", got.TargetResourceAttribution)
	}
	if !containsString(got.Reasons(), "renderer_processes_over_budget") {
		t.Fatalf("budget reasons = %+v, want renderer_processes_over_budget", got.Reasons())
	}
}

func TestBrowserBudgetFailsClosedWhenRendererCountUnavailable(t *testing.T) {
	client := budgetFakeClient{
		targets:     []cdp.TargetInfo{{TargetID: "page-1", Type: "page"}},
		windows:     map[string]int{"page-1": 1},
		failProcess: true,
	}
	got, err := cdp.BrowserBudget(context.Background(), client, cdp.BrowserResourceBudgetOptions{MaxRendererProcesses: 4})
	if err != nil {
		t.Fatalf("BrowserBudget returned error: %v", err)
	}
	if got.RendererCountKnown || got.ProcessInfoError == "" || !got.OverBudgetForNewPage() {
		t.Fatalf("BrowserBudget = %+v, want unknown renderer count to block new pages", got)
	}
	if !containsString(got.Reasons(), "renderer_process_count_unknown") {
		t.Fatalf("budget reasons = %+v, want renderer_process_count_unknown", got.Reasons())
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (f budgetFakeClient) CallSession(ctx context.Context, sessionID, method string, params any, result any) error {
	return f.Call(ctx, method, params, result)
}

func remarshal(in any, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func TestBrowserBudgetCountsTabsWindowsAndAttachedPages(t *testing.T) {
	client := budgetFakeClient{
		targets: []cdp.TargetInfo{
			{TargetID: "page-1", Type: "page", Attached: true},
			{TargetID: "page-2", Type: "page"},
			{TargetID: "worker-1", Type: "service_worker"},
		},
		windows: map[string]int{"page-1": 7, "page-2": 7},
	}
	got, err := cdp.BrowserBudget(context.Background(), client, cdp.BrowserResourceBudgetOptions{MaxTabs: 2, MaxWindows: 1, ConnectionMode: "browser_url"})
	if err != nil {
		t.Fatalf("BrowserBudget returned error: %v", err)
	}
	if got.TabCount != 2 || got.MaxTabs != 2 || !got.TabsOverBudget || got.WindowCount != 1 || !got.WindowsOverBudget || !got.WindowCountKnown || got.AttachedPageCount != 1 {
		t.Fatalf("BrowserBudget = %+v, want over-budget tab/window counts", got)
	}
	if got.TargetTypeCounts["page"] != 2 || got.TargetTypeCounts["service_worker"] != 1 || got.ConnectionMode != "browser_url" {
		t.Fatalf("BrowserBudget target counts = %+v", got)
	}
}

func TestBrowserBudgetTreatsWindowMappingFailureAsUnknown(t *testing.T) {
	client := budgetFakeClient{
		targets:     []cdp.TargetInfo{{TargetID: "page-1", Type: "page"}},
		failWindows: true,
	}
	got, err := cdp.BrowserBudget(context.Background(), client, cdp.BrowserResourceBudgetOptions{})
	if err != nil {
		t.Fatalf("BrowserBudget returned error: %v", err)
	}
	if got.WindowCountKnown || len(got.WindowMappingFailures) != 1 || len(got.Warnings) == 0 {
		t.Fatalf("BrowserBudget = %+v, want unknown window count with warning", got)
	}
	if got.OverBudgetForNewPage() {
		t.Fatalf("OverBudgetForNewPage = true, want false when only window count is unknown and tabs are under budget")
	}
}

func TestBrowserBudgetUsesModeSpecificDefaultTabs(t *testing.T) {
	client := budgetFakeClient{windows: map[string]int{}}

	headed, err := cdp.BrowserBudget(context.Background(), client, cdp.BrowserResourceBudgetOptions{BrowserMode: "headed"})
	if err != nil {
		t.Fatalf("headed BrowserBudget returned error: %v", err)
	}
	if headed.MaxTabs != cdp.DefaultHeadedMaxTabs || headed.MaxTabsSource != "mode_default" || headed.BrowserMode != "headed" {
		t.Fatalf("headed budget = %+v, want headed mode default", headed)
	}

	headless, err := cdp.BrowserBudget(context.Background(), client, cdp.BrowserResourceBudgetOptions{BrowserMode: "headless"})
	if err != nil {
		t.Fatalf("headless BrowserBudget returned error: %v", err)
	}
	if headless.MaxTabs != cdp.DefaultHeadlessMaxTabs || headless.MaxTabsSource != "mode_default" || headless.BrowserMode != "headless" {
		t.Fatalf("headless budget = %+v, want headless mode default", headless)
	}
}
