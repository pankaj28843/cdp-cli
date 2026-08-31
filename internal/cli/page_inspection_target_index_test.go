package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestPageInspectionCommandsSelectPageByTargetIndex(t *testing.T) {
	commands := []struct {
		name        string
		args        []string
		outputField string
	}{
		{name: "frames", args: []string{"frames"}, outputField: "frames"},
		{name: "locator-find", args: []string{"locator", "find", "Search"}, outputField: "locator"},
		{name: "dom-query", args: []string{"dom", "query", "button"}, outputField: "query"},
		{name: "css-inspect", args: []string{"css", "inspect", "button"}, outputField: "inspect"},
		{name: "layout-overflow", args: []string{"layout", "overflow"}, outputField: "overflow"},
		{name: "a11y-tree", args: []string{"a11y", "tree"}, outputField: "nodes"},
		{name: "a11y-find", args: []string{"a11y", "find", "--role", "button"}, outputField: "nodes"},
		{name: "a11y-node", args: []string{"a11y", "node", "button"}, outputField: "node"},
		{name: "a11y-snapshot", args: []string{"a11y", "snapshot", "--selector", "body"}, outputField: "snapshot"},
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
				t.Fatalf("%s target-index result = %#v, want page-two and index 2", command.name, result)
			}
			if _, ok := result[command.outputField]; !ok {
				t.Fatalf("%s target-index result = %#v, want command output field %q", command.name, result, command.outputField)
			}
		})
	}
}

func TestPageInspectionCommandsRejectInvalidTargetIndex(t *testing.T) {
	commands := pageInspectionTargetIndexCommands()
	for _, command := range commands {
		for _, value := range []string{"0", "-1"} {
			t.Run(command.name+"/"+value, func(t *testing.T) {
				args := append([]string{}, command.args...)
				args = append(args, "--target-index", value, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s target-index %s exit=%d stdout=%s stderr=%s", command.name, value, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
			})
		}
	}
}

func TestPageInspectionCommandsRejectTargetIndexSelectorConflicts(t *testing.T) {
	commands := pageInspectionTargetIndexCommands()
	selectors := [][]string{
		{"--target", "page-one"},
		{"--url-contains", "example.test"},
		{"--title-contains", "First"},
	}
	for _, command := range commands {
		for _, selector := range selectors {
			name := command.name + "/" + strings.Join(selector, "-")
			t.Run(name, func(t *testing.T) {
				args := append([]string{}, command.args...)
				args = append(args, "--target-index", "1")
				args = append(args, selector...)
				args = append(args, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s conflict exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
			})
		}
	}
}

func TestPageInspectionCommandsReportOutOfRangeTargetIndex(t *testing.T) {
	for _, command := range pageInspectionTargetIndexCommands() {
		t.Run(command.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "only-page",
				"type":     "page",
				"title":    "Only page",
				"url":      "https://example.test/only",
			}})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{}, command.args...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s out-of-range exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}

func pageInspectionTargetIndexCommands() []struct {
	name string
	args []string
} {
	return []struct {
		name string
		args []string
	}{
		{name: "frames", args: []string{"frames"}},
		{name: "locator-find", args: []string{"locator", "find", "Search"}},
		{name: "dom-query", args: []string{"dom", "query", "button"}},
		{name: "css-inspect", args: []string{"css", "inspect", "button"}},
		{name: "layout-overflow", args: []string{"layout", "overflow"}},
		{name: "a11y-tree", args: []string{"a11y", "tree"}},
		{name: "a11y-find", args: []string{"a11y", "find", "--role", "button"}},
		{name: "a11y-node", args: []string{"a11y", "node", "button"}},
		{name: "a11y-snapshot", args: []string{"a11y", "snapshot", "--selector", "body"}},
	}
}
