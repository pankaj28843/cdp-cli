package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestConsoleNetworkObserversSelectPageByTargetIndex(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		resultKey  string
		itemsKey   string
		countField string
	}{
		{name: "console", args: []string{"console", "--wait", "0s", "--limit", "1"}, resultKey: "console", itemsKey: "messages", countField: "count"},
		{name: "network", args: []string{"network", "--wait", "0s", "--limit", "1"}, resultKey: "network", itemsKey: "requests", countField: "count"},
		{name: "network websocket", args: []string{"network", "websocket", "--wait", "0s", "--limit", "1", "--redact", "safe"}, resultKey: "capture", itemsKey: "websockets", countField: "count"},
		{name: "network capture", args: []string{"network", "capture", "--wait", "0s", "--limit", "1", "--include-bodies", "none", "--redact", "safe"}, resultKey: "capture", itemsKey: "requests", countField: "count"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{}, test.args...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s target-index exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}

			var report map[string]any
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("%s target-index output is invalid JSON: %v; output=%s", test.name, err, out.String())
			}
			if report["ok"] != true {
				t.Fatalf("%s report = %#v, want ok", test.name, report)
			}
			target, ok := report["target"].(map[string]any)
			if !ok || target["id"] != "page-two" {
				t.Fatalf("%s target = %#v, want page-two", test.name, report["target"])
			}
			if got, ok := report["target_index"].(float64); !ok || got != 2 {
				t.Fatalf("%s target_index = %#v, want 2", test.name, report["target_index"])
			}
			items, ok := report[test.itemsKey].([]any)
			if !ok {
				t.Fatalf("%s %s = %#v, want an array", test.name, test.itemsKey, report[test.itemsKey])
			}
			result, ok := report[test.resultKey].(map[string]any)
			if !ok {
				t.Fatalf("%s %s = %#v, want an object", test.name, test.resultKey, report[test.resultKey])
			}
			if count, ok := result[test.countField].(float64); !ok || int(count) != len(items) {
				t.Fatalf("%s %s.%s = %#v, want item count %d", test.name, test.resultKey, test.countField, result[test.countField], len(items))
			}
		})
	}
}

func TestConsoleNetworkObserversRejectInvalidTargetIndex(t *testing.T) {
	commands := [][]string{
		{"console"},
		{"network"},
		{"network", "websocket"},
		{"network", "capture"},
	}
	for _, command := range commands {
		name := strings.Join(command, "-")
		for _, value := range []string{"0", "-1"} {
			t.Run(fmt.Sprintf("%s/%s", name, value), func(t *testing.T) {
				args := append(append([]string{}, command...), "--target-index", value, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s target-index %s exit=%d stdout=%s stderr=%s", name, value, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
			})
		}

		t.Run(name+"/conflict", func(t *testing.T) {
			args := append(append([]string{}, command...), "--target-index", "1", "--url-contains", "example.test", "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s target conflict exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
		})
	}
}

func TestConsoleNetworkObserversReportOutOfRangeTargetIndex(t *testing.T) {
	commands := [][]string{
		{"console"},
		{"network"},
		{"network", "websocket"},
		{"network", "capture"},
	}
	for _, command := range commands {
		name := strings.Join(command, "-")
		t.Run(name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "only-page", "type": "page", "title": "Only page", "url": "https://example.test/only",
			}})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")
			args := append(append([]string{}, command...), "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s out-of-range exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}
