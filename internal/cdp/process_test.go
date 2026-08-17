package cdp_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestCollectProcessInfoCountsRenderersWithoutTargetAttribution(t *testing.T) {
	client := processInfoFakeClient{}
	got, err := cdp.CollectProcessInfo(context.Background(), client)
	if err != nil {
		t.Fatalf("CollectProcessInfo returned error: %v", err)
	}
	if got.ProcessCount != 3 || got.RendererProcessCount != 2 || got.TypeCounts["renderer"] != 2 {
		t.Fatalf("process info = %+v, want three processes and two renderers", got)
	}
	if got.Processes[0].CPUTime != 1.5 || got.Processes[1].ID != 101 {
		t.Fatalf("process rows = %+v, want stable CDP process fields", got.Processes)
	}
}

type processInfoFakeClient struct{}

func (processInfoFakeClient) Call(_ context.Context, method string, _ any, result any) error {
	if method != "SystemInfo.getProcessInfo" {
		return fmt.Errorf("method = %q, want SystemInfo.getProcessInfo", method)
	}
	return remarshal(map[string]any{
		"processInfo": []map[string]any{
			{"type": "browser", "id": 100, "cpuTime": 1.5},
			{"type": "renderer", "id": 101, "cpuTime": 0.25},
			{"type": "renderer", "id": 102, "cpuTime": 0.5},
		},
	}, result)
}

func (f processInfoFakeClient) CallSession(ctx context.Context, sessionID, method string, params any, result any) error {
	return f.Call(ctx, method, params, result)
}
