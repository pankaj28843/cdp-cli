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

func TestDialogActionsExposeOwnedWaitFlags(t *testing.T) {
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
			want := map[string]bool{"wait": false, "type": false, "message": false, "message-contains": false}
			for _, rawFlag := range flags {
				flag, ok := rawFlag.(map[string]any)
				if !ok {
					continue
				}
				if nameValue, ok := flag["name"].(string); ok {
					if _, wanted := want[nameValue]; wanted {
						want[nameValue] = true
					}
				}
			}
			for flag, found := range want {
				if !found {
					t.Errorf("describe %s did not expose --%s: %s", name, flag, out.String())
				}
			}
		})
	}
}

func TestDialogSchemaDescribesOwnedWaitEvidence(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"schema", "dialog", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("schema dialog exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result struct {
		Schema struct {
			Description string `json:"description"`
			Fields      []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"fields"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode dialog schema: %v; output=%s", err, out.String())
	}
	if !strings.Contains(result.Schema.Description, "same attached session") {
		t.Fatalf("dialog schema description=%q, want same-session wait contract", result.Schema.Description)
	}
	found := map[string]bool{}
	for _, field := range result.Schema.Fields {
		found[field.Name] = true
		if field.Name == "wait" && !strings.Contains(field.Description, "same attached session") {
			t.Fatalf("dialog wait schema description=%q, want same-session evidence", field.Description)
		}
	}
	for _, name := range []string{"target_index", "wait", "dialog", "last_event", "next_commands"} {
		if !found[name] {
			t.Errorf("dialog schema missing %q", name)
		}
	}
}

func TestDialogOwnedWaitSelectsIndexedPageAndHandlesSameSession(t *testing.T) {
	commands := []struct {
		name     string
		args     []string
		accepted bool
		prompt   bool
	}{
		{name: "accept", args: []string{"dialog", "accept", "--prompt-text", "yes"}, accepted: true, prompt: true},
		{name: "dismiss", args: []string{"dialog", "dismiss"}, accepted: false},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "baseline-page", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "dialog-page", "type": "page", "title": "Dialog", "url": "https://example.test/dialog", "requireDialogSession": true},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{}, command.args...)
			args = append(args, "--wait", "--type", "confirm", "--message-contains", "Delete", "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("dialog %s --wait exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			var result struct {
				OK          bool           `json:"ok"`
				Target      map[string]any `json:"target"`
				TargetIndex int            `json:"target_index"`
				Wait        struct {
					Kind    string `json:"kind"`
					Matched bool   `json:"matched"`
				} `json:"wait"`
				Dialog struct {
					Action             string `json:"action"`
					Accepted           bool   `json:"accepted"`
					Handled            bool   `json:"handled"`
					PromptTextSupplied bool   `json:"prompt_text_supplied"`
				} `json:"dialog"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode dialog %s --wait: %v; output=%s", command.name, err, out.String())
			}
			if !result.OK || result.Target["id"] != "dialog-page" || result.TargetIndex != 2 || result.Wait.Kind != "dialog" || !result.Wait.Matched {
				t.Fatalf("dialog %s --wait result=%#v, want indexed same-session match", command.name, result)
			}
			if result.Dialog.Action != command.name || result.Dialog.Accepted != command.accepted || !result.Dialog.Handled || result.Dialog.PromptTextSupplied != command.prompt {
				t.Fatalf("dialog %s --wait dialog=%#v, want handled action", command.name, result.Dialog)
			}
		})
	}
}

func TestDialogOwnedWaitRejectsInvalidOptionsBeforeAttachment(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "zero index", args: []string{"dialog", "accept", "--wait", "--target-index", "0", "--json"}, want: "invalid_target_index"},
		{name: "selector conflict", args: []string{"dialog", "dismiss", "--wait", "--target-index", "1", "--target", "page-one", "--json"}, want: "invalid_target_selector"},
		{name: "invalid type", args: []string{"dialog", "dismiss", "--wait", "--type", "modal", "--json"}, want: "usage"},
		{name: "message conflict", args: []string{"dialog", "accept", "--wait", "--message", "Delete", "--message-contains", "Del", "--json"}, want: "usage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s exit=%d stdout=%s stderr=%s", tt.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s: %v; output=%s", tt.name, err, out.String())
			}
			if result["code"] != tt.want && !strings.Contains(fmt.Sprint(result["message"]), tt.want) {
				t.Fatalf("%s result=%#v, want code/message containing %q", tt.name, result, tt.want)
			}
		})
	}
}

func TestDialogOwnedWaitTimesOutWithStructuredEvidence(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"dialog", "dismiss", "--wait", "--type", "alert", "--timeout", "500ms", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("dialog --wait timeout exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Wait struct {
				Kind    string `json:"kind"`
				Matched bool   `json:"matched"`
			} `json:"wait"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode dialog --wait timeout: %v; output=%s", err, out.String())
	}
	if result.OK || result.Code != "timeout" || result.Data.Wait.Kind != "dialog" || result.Data.Wait.Matched {
		t.Fatalf("dialog --wait timeout=%#v, want structured timeout evidence", result)
	}
	requireFakeLifecycleEvent(t, server, "detach:session-page-one")
}
