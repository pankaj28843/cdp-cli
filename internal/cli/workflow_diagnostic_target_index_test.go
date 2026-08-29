package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestDiagnosticWorkflowsExposeTargetIndex(t *testing.T) {
	tests := []struct {
		command string
		schema  string
	}{
		{command: "workflow verify", schema: "workflow-verify"},
		{command: "workflow perf", schema: "workflow-perf"},
		{command: "workflow a11y", schema: "workflow-a11y"},
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
					Name        string `json:"name"`
					Description string `json:"description"`
					Fields      []struct {
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
			if !schema.OK || schema.Schema.Name != test.schema || !foundField || (!strings.Contains(schema.Schema.Description, "target-index") && !strings.Contains(schema.Schema.Description, "existing page")) {
				t.Fatalf("schema %s = %+v, want indexed existing-page contract", test.schema, schema)
			}
		})
	}
}

func TestDiagnosticWorkflowsRejectInvalidTargetIndexBeforeConnection(t *testing.T) {
	for _, workflow := range []string{"verify", "perf", "a11y"} {
		for _, value := range []string{"0", "-1"} {
			t.Run(fmt.Sprintf("%s/%s", workflow, value), func(t *testing.T) {
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), []string{"workflow", workflow, "--target-index", value, "--json"}, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s target-index %s exit=%d stdout=%s stderr=%s", workflow, value, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
			})
		}
	}
}

func TestDiagnosticWorkflowsRequireURLWithoutTargetIndex(t *testing.T) {
	for _, workflow := range []string{"verify", "perf", "a11y"} {
		t.Run(workflow, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"workflow", workflow, "--wait", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s without URL/index exit=%d stdout=%s stderr=%s", workflow, code, out.String(), errOut.String())
			}
		})
	}
}

func TestDiagnosticWorkflowsSelectExistingPageByTargetIndex(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "verify", args: []string{"workflow", "verify", "--target-index", "2", "--wait", "0s", "--limit", "1"}},
		{name: "perf", args: []string{"workflow", "perf", "--target-index", "2", "--wait", "0s", "--trace", filepath.Join(t.TempDir(), "perf-indexed.json")}},
		{name: "a11y", args: []string{"workflow", "a11y", "--target-index", "2", "--wait", "0s", "--limit", "1"}},
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

			fakePageNavigateCount.Store(0)
			args := append(append([]string{}, test.args...), "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s target-index exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			var report struct {
				OK     bool `json:"ok"`
				Target struct {
					ID  string `json:"id"`
					URL string `json:"url"`
				} `json:"target"`
				TargetIndex int `json:"target_index"`
				Workflow    struct {
					RequestedURL string `json:"requested_url"`
				} `json:"workflow"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("%s output is invalid JSON: %v; output=%s", test.name, err, out.String())
			}
			if !report.OK || report.Target.ID != "page-two" || report.TargetIndex != 2 || report.Workflow.RequestedURL != "" || report.Target.URL != "https://example.test/second" {
				t.Fatalf("%s report=%+v, want page-two/index 2 no-URL observation", test.name, report)
			}
			if navigations := fakePageNavigateCount.Load(); navigations != 0 {
				t.Fatalf("%s issued %d navigations for no-URL observation", test.name, navigations)
			}
			if pages := fakePagesCount(t); pages != 2 {
				t.Fatalf("%s page count=%d, want existing pages preserved with no creation", test.name, pages)
			}
		})
	}
}

func TestDiagnosticWorkflowsNavigateSelectedPageByTargetIndex(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "verify", args: []string{"workflow", "verify", "https://example.test/navigated", "--target-index", "2", "--wait", "0s", "--limit", "1"}},
		{name: "perf", args: []string{"workflow", "perf", "https://example.test/navigated", "--target-index", "2", "--wait", "0s", "--trace", filepath.Join(t.TempDir(), "perf-navigated.json")}},
		{name: "a11y", args: []string{"workflow", "a11y", "https://example.test/navigated", "--target-index", "2", "--wait", "0s", "--limit", "1"}},
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

			args := append(append([]string{}, test.args...), "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s indexed navigation exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			var report struct {
				OK     bool `json:"ok"`
				Target struct {
					ID  string `json:"id"`
					URL string `json:"url"`
				} `json:"target"`
				TargetIndex int `json:"target_index"`
				Workflow    struct {
					RequestedURL string `json:"requested_url"`
				} `json:"workflow"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("%s output is invalid JSON: %v; output=%s", test.name, err, out.String())
			}
			if !report.OK || report.Target.ID != "page-two" || report.Target.URL != "https://example.test/navigated" || report.TargetIndex != 2 || report.Workflow.RequestedURL != "https://example.test/navigated" {
				t.Fatalf("%s report=%+v, want indexed navigation of page-two", test.name, report)
			}
			if pages := fakePagesCount(t); pages != 2 {
				t.Fatalf("%s page count=%d, want no workflow-created page", test.name, pages)
			}
		})
	}
}

func TestDiagnosticWorkflowsReportOutOfRangeTargetIndex(t *testing.T) {
	for _, workflow := range []string{"verify", "perf", "a11y"} {
		t.Run(workflow, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "only-page", "type": "page", "title": "Only page", "url": "https://example.test/only",
			}})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"workflow", workflow, "--target-index", "2", "--state-dir", stateDir, "--wait", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s out-of-range exit=%d stdout=%s stderr=%s", workflow, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}
