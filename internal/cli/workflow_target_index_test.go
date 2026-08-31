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

func TestExistingPageWorkflowsExposeTargetIndexMetadata(t *testing.T) {
	tests := []struct {
		command string
		schema  string
	}{
		{command: "workflow action-capture", schema: "workflow-action-capture"},
		{command: "workflow console-errors", schema: "workflow-console-errors"},
		{command: "workflow network-failures", schema: "workflow-network-failures"},
		{command: "workflow submit-search", schema: "workflow-submit-search"},
	}

	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			var describeOut, describeErr bytes.Buffer
			code := cli.Execute(context.Background(), []string{"describe", "--command", test.command, "--json"}, &describeOut, &describeErr, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("describe %s exit=%d stdout=%s stderr=%s", test.command, code, describeOut.String(), describeErr.String())
			}
			var describe struct {
				OK       bool `json:"ok"`
				Commands struct {
					Flags []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"flags"`
					Examples []string `json:"examples"`
				} `json:"commands"`
			}
			if err := json.Unmarshal(describeOut.Bytes(), &describe); err != nil {
				t.Fatalf("describe %s output is invalid JSON: %v; output=%s", test.command, err, describeOut.String())
			}
			foundFlag := false
			for _, flag := range describe.Commands.Flags {
				if flag.Name == "target-index" && flag.Type == "int" {
					foundFlag = true
					break
				}
			}
			if !describe.OK || !foundFlag || !examplesContainTargetIndex(describe.Commands.Examples) {
				t.Fatalf("describe %s = %+v, want integer target-index flag and example", test.command, describe)
			}

			var schemaOut, schemaErr bytes.Buffer
			code = cli.Execute(context.Background(), []string{"schema", test.schema, "--json"}, &schemaOut, &schemaErr, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("schema %s exit=%d stdout=%s stderr=%s", test.schema, code, schemaOut.String(), schemaErr.String())
			}
			var schema struct {
				OK     bool `json:"ok"`
				Schema struct {
					Name   string `json:"name"`
					Fields []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"fields"`
				} `json:"schema"`
			}
			if err := json.Unmarshal(schemaOut.Bytes(), &schema); err != nil {
				t.Fatalf("schema %s output is invalid JSON: %v; output=%s", test.schema, err, schemaOut.String())
			}
			foundField := false
			for _, field := range schema.Schema.Fields {
				if field.Name == "target_index" && field.Type == "integer" {
					foundField = true
					break
				}
			}
			if !schema.OK || schema.Schema.Name != test.schema || !foundField {
				t.Fatalf("schema %s = %+v, want integer target_index field", test.schema, schema)
			}
		})
	}
}

func TestExistingPageWorkflowsRejectInvalidTargetIndexBeforeAttachment(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "action-capture", args: []string{"workflow", "action-capture", "--action", "press:Enter", "--selector", "body"}},
		{name: "console-errors", args: []string{"workflow", "console-errors"}},
		{name: "network-failures", args: []string{"workflow", "network-failures"}},
		{name: "submit-search", args: []string{"workflow", "submit-search", "Search", "query", "--submit", "none"}},
	}

	for _, test := range tests {
		for _, value := range []string{"0", "-1"} {
			t.Run(fmt.Sprintf("%s/%s", test.name, value), func(t *testing.T) {
				args := append(append([]string{}, test.args...), "--target-index", value, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s target-index %s exit=%d stdout=%s stderr=%s", test.name, value, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
			})
		}

		t.Run(test.name+"/conflict", func(t *testing.T) {
			args := append(append([]string{}, test.args...), "--target-index", "1", "--target", "page-one", "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s target conflict exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
		})
	}
}

func TestExistingPageWorkflowsSelectPageByTargetIndex(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		check func(t *testing.T, report map[string]any)
	}{
		{
			name: "action-capture",
			args: []string{"workflow", "action-capture", "--action", "press:Enter", "--selector", "body", "--wait-before", "0s", "--wait-after", "0s", "--include", "network", "--limit", "1"},
			check: func(t *testing.T, report map[string]any) {
				action, ok := report["action"].(map[string]any)
				if !ok || action["type"] != "press" {
					t.Fatalf("action-capture action=%#v, want press action", report["action"])
				}
			},
		},
		{
			name: "console-errors",
			args: []string{"workflow", "console-errors", "--wait", "0s", "--limit", "1"},
			check: func(t *testing.T, report map[string]any) {
				if messages, ok := report["messages"].([]any); !ok || len(messages) == 0 {
					t.Fatalf("console-errors messages=%#v, want bounded synthetic evidence", report["messages"])
				}
			},
		},
		{
			name: "network-failures",
			args: []string{"workflow", "network-failures", "--wait", "250ms", "--limit", "1"},
			check: func(t *testing.T, report map[string]any) {
				if requests, ok := report["requests"].([]any); !ok || len(requests) == 0 {
					t.Fatalf("network-failures requests=%#v, want bounded synthetic evidence", report["requests"])
				}
			},
		},
		{
			name: "submit-search",
			args: []string{"workflow", "submit-search", "Search", "query", "--by", "label", "--submit", "none"},
			check: func(t *testing.T, report map[string]any) {
				if report["action"] != "submit_search" {
					t.Fatalf("submit-search action=%#v, want submit_search", report["action"])
				}
				fill, ok := report["fill"].(map[string]any)
				if !ok || fill["filled"] != true {
					t.Fatalf("submit-search fill=%#v, want filled evidence", report["fill"])
				}
			},
		},
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

			args := append(append([]string{}, test.args...), "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s target-index exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			var report map[string]any
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("%s output is invalid JSON: %v; output=%s", test.name, err, out.String())
			}
			if report["ok"] != true {
				t.Fatalf("%s report=%#v, want ok", test.name, report)
			}
			target, ok := report["target"].(map[string]any)
			if !ok || target["id"] != "page-two" || report["target_index"] != float64(2) {
				t.Fatalf("%s target evidence=%#v index=%#v, want page-two/index 2", test.name, report["target"], report["target_index"])
			}
			test.check(t, report)
		})
	}
}

func TestExistingPageWorkflowsReportOutOfRangeTargetIndex(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "action-capture", args: []string{"workflow", "action-capture", "--action", "press:Enter", "--selector", "body"}},
		{name: "console-errors", args: []string{"workflow", "console-errors"}},
		{name: "network-failures", args: []string{"workflow", "network-failures"}},
		{name: "submit-search", args: []string{"workflow", "submit-search", "Search", "query", "--submit", "none"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "only-page", "type": "page", "title": "Only page", "url": "https://example.test/only",
			}})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append(append([]string{}, test.args...), "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s out-of-range exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}

func examplesContainTargetIndex(examples []string) bool {
	for _, example := range examples {
		if strings.Contains(example, "--target-index 2") {
			return true
		}
	}
	return false
}
