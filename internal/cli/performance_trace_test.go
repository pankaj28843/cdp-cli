package cli

import (
	"encoding/json"
	"testing"
)

func TestPerformanceTraceInsightsDoNotClaimMissingMetrics(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"traceEvents": []map[string]any{{"name": "RunTask", "ts": 1, "dur": 10000}}})
	if err != nil {
		t.Fatal(err)
	}
	insights := performanceTraceInsights(raw)
	for _, name := range []string{"lcp", "cls", "blocking_requests"} {
		insight := insights[name].(map[string]any)
		if available, _ := insight["available"].(bool); available || insight["reason"] == "" {
			t.Fatalf("%s insight = %+v, want unavailable with reason", name, insight)
		}
	}
	longTasks := insights["long_tasks"].(map[string]any)
	if available, _ := longTasks["available"].(bool); !available || longTasks["count"] != 0 {
		t.Fatalf("long_tasks insight = %+v, want available zero count", longTasks)
	}
}

func TestPerformanceTraceInsightsRejectInvalidTrace(t *testing.T) {
	insights := performanceTraceInsights([]byte("not-json"))
	for name, raw := range insights {
		insight := raw.(map[string]any)
		if available, _ := insight["available"].(bool); available || insight["reason"] == "" {
			t.Fatalf("%s insight = %+v, want honest unavailable result", name, insight)
		}
	}
}
