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
	default:
		return fmt.Errorf("unexpected method %s", method)
	}
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
