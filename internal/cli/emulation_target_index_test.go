package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

type emulationTargetIndexCommand struct {
	name string
	args []string
}

func emulationTargetIndexCommands() []emulationTargetIndexCommand {
	return []emulationTargetIndexCommand{
		{name: "viewport", args: []string{"emulate", "viewport"}},
		{name: "clear", args: []string{"emulate", "clear"}},
		{name: "media", args: []string{"emulate", "media"}},
		{name: "color-scheme", args: []string{"emulate", "color-scheme", "--scheme", "dark"}},
		{name: "user-agent", args: []string{"emulate", "user-agent", "--user-agent", "SyntheticAgent/1.0"}},
		{name: "geolocation", args: []string{"emulate", "geolocation", "--latitude", "55", "--longitude", "12", "--accuracy", "50"}},
		{name: "timezone", args: []string{"emulate", "timezone", "--timezone-id", "UTC"}},
		{name: "locale", args: []string{"emulate", "locale", "--locale", "de-DE"}},
		{name: "cpu", args: []string{"emulate", "cpu", "--rate", "2"}},
		{name: "network", args: []string{"emulate", "network", "--preset", "fast-3g"}},
	}
}

func TestEmulationCommandsExposeTargetIndex(t *testing.T) {
	for _, command := range emulationTargetIndexCommands() {
		t.Run(command.name, func(t *testing.T) {
			args := []string{"describe", "--command", "emulate " + command.name, "--json"}
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("describe %s exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode describe %s: %v; output=%s", command.name, err, out.String())
			}
			commands, ok := result["commands"].(map[string]any)
			if !ok {
				t.Fatalf("describe %s commands=%#v", command.name, result["commands"])
			}
			flags, ok := commands["flags"].([]any)
			if !ok {
				t.Fatalf("describe %s flags=%#v", command.name, commands["flags"])
			}
			for _, rawFlag := range flags {
				flag, ok := rawFlag.(map[string]any)
				if !ok || flag["name"] != "target-index" {
					continue
				}
				if flag["type"] != "int" {
					t.Fatalf("describe %s target-index type=%v, want int", command.name, flag["type"])
				}
				return
			}
			t.Fatalf("describe %s did not expose target-index: %s", command.name, out.String())
		})
	}
}

func TestEmulationSchemasExposeTargetIndex(t *testing.T) {
	for _, command := range emulationTargetIndexCommands() {
		t.Run(command.name, func(t *testing.T) {
			args := []string{"schema", "emulate-" + command.name, "--json"}
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("schema emulate-%s exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode schema emulate-%s: %v; output=%s", command.name, err, out.String())
			}
			schema, ok := result["schema"].(map[string]any)
			if !ok || schema["name"] != "emulate-"+command.name {
				t.Fatalf("schema emulate-%s result=%#v", command.name, result)
			}
			fields, ok := schema["fields"].([]any)
			if !ok {
				t.Fatalf("schema emulate-%s fields=%#v", command.name, schema["fields"])
			}
			for _, rawField := range fields {
				field, ok := rawField.(map[string]any)
				if !ok || field["name"] != "target_index" {
					continue
				}
				if field["type"] != "integer" {
					t.Fatalf("schema emulate-%s target_index type=%v, want integer", command.name, field["type"])
				}
				return
			}
			t.Fatalf("schema emulate-%s did not expose target_index: %s", command.name, out.String())
		})
	}
}

func TestEmulationCommandsRejectInvalidTargetIndexBeforeAttachment(t *testing.T) {
	for _, command := range emulationTargetIndexCommands() {
		for _, value := range []string{"0", "-1"} {
			t.Run(fmt.Sprintf("%s/%s", command.name, value), func(t *testing.T) {
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

func TestEmulationCommandsRejectTargetIndexSelectorConflicts(t *testing.T) {
	for _, command := range emulationTargetIndexCommands() {
		for _, selector := range []string{"--target", "--url-contains", "--title-contains"} {
			t.Run(fmt.Sprintf("%s/%s", command.name, selector), func(t *testing.T) {
				args := append([]string{}, command.args...)
				args = append(args, "--target-index", "1", selector, "page-one", "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s selector conflict exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
			})
		}
	}
}

func TestEmulationCommandsSelectIndexedPageAndPreserveOutput(t *testing.T) {
	for _, command := range emulationTargetIndexCommands() {
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
				t.Fatalf("decode %s target-index: %v; output=%s", command.name, err, out.String())
			}
			target, ok := result["target"].(map[string]any)
			if !ok || result["ok"] != true || target["id"] != "page-two" || result["target_index"] != float64(2) {
				t.Fatalf("%s target-index result=%#v, want page-two/index 2", command.name, result)
			}
			if _, ok := result["emulation"].(map[string]any); !ok {
				t.Fatalf("%s result=%#v, want emulation payload", command.name, result)
			}
		})
	}
}

func TestEmulationCommandsReportOutOfRangeTargetIndex(t *testing.T) {
	for _, command := range emulationTargetIndexCommands() {
		t.Run(command.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
			})
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
