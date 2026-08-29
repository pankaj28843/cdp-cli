package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestControlSelectionCommandsExposeTargetIndex(t *testing.T) {
	for _, name := range []string{"focus", "clear", "check", "uncheck", "select"} {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"describe", "--command", name, "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("describe %s exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			var result struct {
				Commands struct {
					Flags []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"flags"`
				} `json:"commands"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode describe %s: %v; output=%s", name, err, out.String())
			}
			for _, flag := range result.Commands.Flags {
				if flag.Name == "target-index" {
					if flag.Type != "int" {
						t.Fatalf("describe %s target-index type=%q, want int", name, flag.Type)
					}
					return
				}
			}
			t.Fatalf("describe %s did not expose target-index: %s", name, out.String())
		})
	}
}

func TestControlSelectionCommandsRejectInvalidTargetIndexBeforeAttachment(t *testing.T) {
	commands := [][]string{
		{"focus", "input#q"},
		{"clear", "input#q"},
		{"check", "input#subscribe"},
		{"uncheck", "input#subscribe"},
		{"select", "select#plan", "pro"},
	}
	for _, command := range commands {
		for _, value := range []string{"0", "-1"} {
			t.Run(fmt.Sprintf("%s/%s", command[0], value), func(t *testing.T) {
				args := append([]string{}, command...)
				args = append(args, "--target-index", value, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s target-index %s exit=%d stdout=%s stderr=%s", command[0], value, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
			})
		}
	}
}

func TestControlSelectionCommandsRejectTargetIndexSelectorConflicts(t *testing.T) {
	commands := [][]string{
		{"focus", "input#q"},
		{"clear", "input#q"},
		{"check", "input#subscribe"},
		{"uncheck", "input#subscribe"},
		{"select", "select#plan", "pro"},
	}
	for _, command := range commands {
		t.Run(command[0], func(t *testing.T) {
			args := append([]string{}, command...)
			args = append(args, "--target-index", "1", "--target", "page-one", "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s target-index conflict exit=%d stdout=%s stderr=%s", command[0], code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
		})
	}
}

func TestControlSelectionCommandsSelectPageByTargetIndex(t *testing.T) {
	commands := []struct {
		name  string
		args  []string
		field string
	}{
		{name: "focus", args: []string{"focus", "input#q"}, field: "focus"},
		{name: "clear", args: []string{"clear", "input#q"}, field: "clear"},
		{name: "check", args: []string{"check", "input#subscribe", "--trial"}, field: "check"},
		{name: "uncheck", args: []string{"uncheck", "input#subscribe", "--trial"}, field: "uncheck"},
		{name: "select", args: []string{"select", "select#plan", "pro", "--trial"}, field: "select"},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{}, command.args...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s target-index exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s target-index output: %v; output=%s", command.name, err, out.String())
			}
			target, ok := result["target"].(map[string]any)
			if !ok || result["ok"] != true || target["id"] != "page-two" || result["target_index"] != float64(2) {
				t.Fatalf("%s target-index result=%#v, want successful page-two evidence", command.name, result)
			}
			if _, ok := result[command.field].(map[string]any); !ok {
				t.Fatalf("%s target-index result missing %q evidence: %#v", command.name, command.field, result)
			}
		})
	}
}

func TestControlSelectionCommandsPreserveIndexedAutoScrollEvidence(t *testing.T) {
	for _, command := range []struct {
		name string
		args []string
	}{
		{name: "check", args: []string{"check", "input#below-fold-checkbox"}},
		{name: "uncheck", args: []string{"uncheck", "input#below-fold-checkbox"}},
	} {
		t.Run(command.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{}, command.args...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s indexed auto-scroll exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s indexed auto-scroll output: %v; output=%s", command.name, err, out.String())
			}
			target, targetOK := result["target"].(map[string]any)
			autoScroll, autoScrollOK := result["auto_scroll"].(map[string]any)
			actionability, actionabilityOK := result["actionability"].(map[string]any)
			if result["ok"] != true || !targetOK || target["id"] != "page-two" || result["target_index"] != float64(2) || !autoScrollOK || autoScroll["scrolled"] != true || autoScroll["changed"] != true || !actionabilityOK || actionability["actionable"] != true {
				t.Fatalf("%s indexed auto-scroll result=%#v, want page-two, viewport-changing scroll, and rechecked actionability", command.name, result)
			}
		})
	}
}

func TestControlSelectionCommandsReportOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	for _, args := range [][]string{
		{"focus", "input#q"},
		{"clear", "input#q"},
		{"check", "input#subscribe", "--trial"},
		{"uncheck", "input#subscribe", "--trial"},
		{"select", "select#plan", "pro", "--trial"},
	} {
		name := args[0]
		t.Run(name, func(t *testing.T) {
			commandArgs := append([]string{}, args...)
			commandArgs = append(commandArgs, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), commandArgs, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s out-of-range exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}

func TestControlSelectionCommandsIncludeTargetIndexInActionabilityFailures(t *testing.T) {
	commands := [][]string{
		{"check", "input#disabled-checkbox"},
		{"uncheck", "input#disabled-checkbox"},
		{"select", "select#disabled-plan", "pro"},
	}
	for _, args := range commands {
		name := args[0]
		t.Run(name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			commandArgs := append([]string{}, args...)
			commandArgs = append(commandArgs, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), commandArgs, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitCheckFailed {
				t.Fatalf("%s actionability exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s actionability output: %v; output=%s", name, err, out.String())
			}
			data, ok := result["data"].(map[string]any)
			target, targetOK := data["target"].(map[string]any)
			if result["code"] != "actionability_failed" || !ok || !targetOK || target["id"] != "page-two" || data["target_index"] != float64(2) {
				t.Fatalf("%s actionability result=%#v, want page-two target and index 2 in bounded failure data", name, result)
			}
		})
	}
}
