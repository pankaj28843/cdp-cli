package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestDialogCommandsExposeTargetIndex(t *testing.T) {
	for _, name := range []string{"dialog accept", "dialog dismiss"} {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"describe", "--command", name, "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("describe %s exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode describe %s: %v; output=%s", name, err, out.String())
			}
			commands, ok := result["commands"].(map[string]any)
			if !ok {
				t.Fatalf("describe %s commands=%#v", name, result["commands"])
			}
			flags, ok := commands["flags"].([]any)
			if !ok {
				t.Fatalf("describe %s flags=%#v", name, commands["flags"])
			}
			for _, rawFlag := range flags {
				flag, ok := rawFlag.(map[string]any)
				if !ok || flag["name"] != "target-index" {
					continue
				}
				if flag["type"] != "int" {
					t.Fatalf("describe %s target-index type=%v, want int", name, flag["type"])
				}
				return
			}
			t.Fatalf("describe %s did not expose target-index: %s", name, out.String())
		})
	}
}

func TestDialogCommandsRejectInvalidTargetIndexBeforeAttachment(t *testing.T) {
	for _, command := range [][]string{
		{"dialog", "accept"},
		{"dialog", "dismiss"},
	} {
		for _, value := range []string{"0", "-1"} {
			t.Run(fmt.Sprintf("%s/%s", command[1], value), func(t *testing.T) {
				args := append([]string{}, command...)
				args = append(args, "--target-index", value, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s target-index %s exit=%d stdout=%s stderr=%s", command[1], value, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
			})
		}
	}
}

func TestDialogCommandsRejectTargetIndexSelectorConflicts(t *testing.T) {
	for _, selector := range []string{"--target", "--url-contains", "--title-contains"} {
		for _, command := range [][]string{
			{"dialog", "accept"},
			{"dialog", "dismiss"},
		} {
			t.Run(fmt.Sprintf("%s/%s", command[1], selector), func(t *testing.T) {
				args := append([]string{}, command...)
				args = append(args, "--target-index", "1", selector, "page-one", "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s selector conflict exit=%d stdout=%s stderr=%s", command[1], code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
			})
		}
	}
}

func TestDialogCommandsSelectIndexedPageAndPreserveOutput(t *testing.T) {
	commands := []struct {
		name        string
		args        []string
		accepted    bool
		promptGiven bool
	}{
		{name: "accept", args: []string{"dialog", "accept", "--prompt-text", "yes"}, accepted: true, promptGiven: true},
		{name: "dismiss", args: []string{"dialog", "dismiss"}, accepted: false},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "dialog-page", "type": "page", "title": "Dialog", "url": "https://example.test/dialog"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{}, command.args...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("dialog %s target-index exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode dialog %s target-index: %v; output=%s", command.name, err, out.String())
			}
			dialog, dialogOK := result["dialog"].(map[string]any)
			target, targetOK := result["target"].(map[string]any)
			if !dialogOK || !targetOK || result["ok"] != true || target["id"] != "dialog-page" || result["target_index"] != float64(2) {
				t.Fatalf("dialog %s target-index result=%#v, want dialog-page evidence", command.name, result)
			}
			if dialog["accepted"] != command.accepted || dialog["prompt_text_supplied"] != command.promptGiven {
				t.Fatalf("dialog %s result=%#v, want accepted=%t prompt_text_supplied=%t", command.name, dialog, command.accepted, command.promptGiven)
			}
		})
	}
}

func TestDialogCommandsReportOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	for _, command := range [][]string{
		{"dialog", "accept"},
		{"dialog", "dismiss"},
	} {
		t.Run(command[1], func(t *testing.T) {
			args := append([]string{}, command...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s out-of-range exit=%d stdout=%s stderr=%s", command[1], code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}
