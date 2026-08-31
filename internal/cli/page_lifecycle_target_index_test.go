package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestPageLifecycleCommandsSelectPageByTargetIndex(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		action string
	}{
		{name: "select", args: []string{"page", "select", "--target-index", "2"}},
		{name: "reload", args: []string{"page", "reload", "--target-index", "2"}, action: "reloaded"},
		{name: "back", args: []string{"page", "back", "--target-index", "2"}, action: "back"},
		{name: "forward", args: []string{"page", "forward", "--target-index", "2"}, action: "forward"},
		{name: "activate", args: []string{"page", "activate", "--target-index", "2"}, action: "activated"},
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
			args = append(args, "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s target-index exit=%d, stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s target-index output: %v; output=%s", test.name, err, out.String())
			}
			target, ok := result["target"].(map[string]any)
			if !ok || target["id"] != "page-two" || result["ok"] != true {
				t.Fatalf("%s target-index result = %#v, want page-two target", test.name, result)
			}
			if test.action != "" && result["action"] != test.action {
				t.Fatalf("%s action = %#v, want %q", test.name, result["action"], test.action)
			}
			if test.name == "select" {
				selected, ok := result["selected_page"].(map[string]any)
				if !ok || selected["target_id"] != "page-two" {
					t.Fatalf("select target-index result = %#v, want selected page-two", result)
				}
			}
		})
	}
}

func TestPageLifecycleCommandsRejectInvalidTargetIndex(t *testing.T) {
	commands := []string{"select", "reload", "back", "forward", "activate"}
	values := []struct {
		name  string
		value string
		code  string
	}{
		{name: "zero", value: "0", code: "invalid_target_index"},
		{name: "negative", value: "-1", code: "invalid_target_index"},
		{name: "out-of-range", value: "3", code: "target_not_found"},
	}
	for _, command := range commands {
		for _, test := range values {
			t.Run(command+"/"+test.name, func(t *testing.T) {
				server := newFakeCDPServer(t, []map[string]any{
					{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
					{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
				})
				defer server.Close()
				stateDir := startFakeDaemon(t, server, "browser_url")

				args := []string{"page", command}
				args = append(args, "--target-index", test.value)
				args = append(args, "--state-dir", stateDir, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s %s exit=%d, stdout=%s stderr=%s", command, test.name, code, out.String(), errOut.String())
				}
				var result map[string]any
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatalf("decode %s %s error: %v; output=%s", command, test.name, err, out.String())
				}
				if result["ok"] != false || result["code"] != test.code {
					t.Fatalf("%s %s error = %#v, want %s", command, test.name, result, test.code)
				}
			})
		}
	}
}

func TestPageLifecycleCommandsRejectTargetIndexSelectorConflicts(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "select positional target", args: []string{"select", "page-one", "--target-index", "1"}},
		{name: "reload target", args: []string{"reload", "--target-index", "1", "--target", "page-one"}},
		{name: "back url", args: []string{"back", "--target-index", "1", "--url-contains", "example.test"}},
		{name: "forward title", args: []string{"forward", "--target-index", "1", "--title-contains", "First"}},
		{name: "activate target", args: []string{"activate", "--target-index", "1", "--target", "page-one"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "page-one",
				"type":     "page",
				"title":    "First",
				"url":      "https://example.test/first",
			}})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{"page"}, test.args...)
			args = append(args, "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s conflict exit=%d, stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s conflict error: %v; output=%s", test.name, err, out.String())
			}
			message, _ := result["message"].(string)
			if result["ok"] != false || result["code"] != "invalid_target_selector" || !strings.Contains(message, "--target-index") {
				t.Fatalf("%s conflict error = %#v, want invalid_target_selector", test.name, result)
			}
		})
	}
}
