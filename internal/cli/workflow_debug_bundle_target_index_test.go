package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestDebugBundleExposesTargetIndex(t *testing.T) {
	var describeOut, describeErr bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "workflow debug-bundle", "--json"}, &describeOut, &describeErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe workflow debug-bundle exit=%d stdout=%s stderr=%s", code, describeOut.String(), describeErr.String())
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
		t.Fatalf("describe workflow debug-bundle output is invalid JSON: %v; output=%s", err, describeOut.String())
	}
	foundFlag := false
	for _, flag := range describe.Commands.Flags {
		if flag.Name == "target-index" && flag.Type == "int" {
			foundFlag = true
			break
		}
	}
	if !describe.OK || !foundFlag || !examplesContainTargetIndex(describe.Commands.Examples) {
		t.Fatalf("describe workflow debug-bundle = %+v, want integer target-index flag and example", describe)
	}

	var schemaOut, schemaErr bytes.Buffer
	code = cli.Execute(context.Background(), []string{"schema", "workflow-debug-bundle", "--json"}, &schemaOut, &schemaErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("schema workflow-debug-bundle exit=%d stdout=%s stderr=%s", code, schemaOut.String(), schemaErr.String())
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
		t.Fatalf("schema workflow-debug-bundle output is invalid JSON: %v; output=%s", err, schemaOut.String())
	}
	foundField := false
	for _, field := range schema.Schema.Fields {
		if field.Name == "target_index" && field.Type == "integer" {
			foundField = true
			break
		}
	}
	if !schema.OK || schema.Schema.Name != "workflow-debug-bundle" || !foundField {
		t.Fatalf("schema workflow-debug-bundle = %+v, want integer target_index field", schema)
	}
}

func TestDebugBundleRejectsInvalidTargetIndexBeforeConnection(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code string
	}{
		{name: "zero", args: []string{"--target-index", "0"}, code: "invalid_target_index"},
		{name: "negative", args: []string{"--target-index", "-1"}, code: "invalid_target_index"},
		{name: "target conflict", args: []string{"--target-index", "1", "--target", "page-one"}, code: "invalid_target_selector"},
		{name: "url filter conflict", args: []string{"--target-index", "1", "--url-contains", "example.test"}, code: "invalid_target_selector"},
		{name: "title filter conflict", args: []string{"--target-index", "1", "--title-contains", "Example"}, code: "invalid_target_selector"},
		{name: "created URL conflict", args: []string{"--target-index", "1", "--url", "https://example.test/app"}, code: "invalid_target_selector"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"workflow", "debug-bundle"}, test.args...)
			args = append(args, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("debug-bundle %s exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), test.code)
		})
	}
}

func TestDebugBundleSelectsExistingPageByTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")
	outDir := t.TempDir()

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "debug-bundle", "--target-index", "2", "--state-dir", stateDir,
		"--since", "0s", "--out-dir", outDir, "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("debug-bundle target-index exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var report struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		TargetIndex int `json:"target_index"`
		Bundle      struct {
			DefaultJSON   string `json:"default_json"`
			RedactionMode string `json:"redaction_mode"`
			Commands      []struct {
				Argv []string `json:"argv"`
			} `json:"commands"`
		} `json:"bundle"`
		Workflow struct {
			Trigger     string `json:"trigger"`
			Reloaded    bool   `json:"reloaded"`
			IgnoreCache bool   `json:"ignore_cache"`
			CachePolicy string `json:"cache_policy"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("debug-bundle target-index output is invalid JSON: %v; output=%s", err, out.String())
	}
	argv := ""
	if len(report.Bundle.Commands) > 0 {
		argv = strings.Join(report.Bundle.Commands[0].Argv, " ")
	}
	if !report.OK || report.Target.ID != "page-two" || report.TargetIndex != 2 || report.Workflow.Trigger != "reload" || !report.Workflow.Reloaded || !report.Workflow.IgnoreCache || report.Workflow.CachePolicy != "bypass_http_cache" || report.Bundle.DefaultJSON != "artifact_references" || report.Bundle.RedactionMode != "safe" || !strings.Contains(argv, "--target-index 2") {
		t.Fatalf("debug-bundle target-index report = %+v argv=%q, want indexed existing-page reload with safe artifact references", report, argv)
	}
	if pages := fakePagesCount(t); pages != 2 {
		t.Fatalf("debug-bundle target-index page count=%d, want existing pages preserved", pages)
	}
}

func TestDebugBundleReportsOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "only-page", "type": "page", "title": "Only page", "url": "https://example.test/only",
	}})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "debug-bundle", "--target-index", "2", "--state-dir", stateDir, "--since", "0s", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("debug-bundle out-of-range exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	assertTargetIndexError(t, out.Bytes(), "target_not_found")
}
