package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestRenderedExtractExposesTargetIndex(t *testing.T) {
	var describeOut, describeErr bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "workflow rendered-extract", "--json"}, &describeOut, &describeErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe workflow rendered-extract exit=%d stdout=%s stderr=%s", code, describeOut.String(), describeErr.String())
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
		t.Fatalf("describe workflow rendered-extract output is invalid JSON: %v; output=%s", err, describeOut.String())
	}
	foundFlag := false
	for _, flag := range describe.Commands.Flags {
		if flag.Name == "target-index" && flag.Type == "int" {
			foundFlag = true
			break
		}
	}
	if !describe.OK || !foundFlag || !examplesContainTargetIndex(describe.Commands.Examples) {
		t.Fatalf("describe workflow rendered-extract = %+v, want integer target-index flag and example", describe)
	}

	var schemaOut, schemaErr bytes.Buffer
	code = cli.Execute(context.Background(), []string{"schema", "workflow-rendered-extract", "--json"}, &schemaOut, &schemaErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("schema workflow-rendered-extract exit=%d stdout=%s stderr=%s", code, schemaOut.String(), schemaErr.String())
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
		t.Fatalf("schema workflow-rendered-extract output is invalid JSON: %v; output=%s", err, schemaOut.String())
	}
	foundField := false
	for _, field := range schema.Schema.Fields {
		if field.Name == "target_index" && field.Type == "integer" {
			foundField = true
			break
		}
	}
	if !schema.OK || schema.Schema.Name != "workflow-rendered-extract" || !foundField {
		t.Fatalf("schema workflow-rendered-extract = %+v, want integer target_index field", schema)
	}
}

func TestRenderedExtractRejectsInvalidTargetIndexBeforeConnection(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "zero", args: []string{"--target-index", "0"}},
		{name: "negative", args: []string{"--target-index", "-1"}},
		{name: "target conflict", args: []string{"--target-index", "1", "--target", "page-one"}},
		{name: "url conflict", args: []string{"--target-index", "1", "--url-contains", "example.test"}},
		{name: "title conflict", args: []string{"--target-index", "1", "--title-contains", "Example"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"workflow", "rendered-extract"}, test.args...)
			args = append(args, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("rendered-extract %s exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			wantCode := "invalid_target_index"
			if test.name != "zero" && test.name != "negative" {
				wantCode = "invalid_target_selector"
			}
			assertTargetIndexError(t, out.Bytes(), wantCode)
		})
	}
}

func TestRenderedExtractSelectsExistingPageByTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", "--target-index", "2", "--state-dir", stateDir,
		"--out-dir", t.TempDir(), "--wait", "0s", "--settle", "0s",
		"--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("rendered-extract target-index exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var report struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		TargetIndex int `json:"target_index"`
		Workflow    struct {
			CreatedPage bool `json:"created_page"`
			ReusedPage  bool `json:"reused_page"`
			Closed      bool `json:"closed"`
			Cleanup     struct {
				Skipped bool   `json:"skipped"`
				Reason  string `json:"reason"`
			} `json:"cleanup"`
		} `json:"workflow"`
		Quality struct {
			Passed bool `json:"passed"`
		} `json:"quality"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("rendered-extract target-index output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !report.OK || report.Target.ID != "page-two" || report.TargetIndex != 2 || report.Workflow.CreatedPage || !report.Workflow.ReusedPage || report.Workflow.Closed || !report.Workflow.Cleanup.Skipped || report.Workflow.Cleanup.Reason != "caller_owned" || !report.Quality.Passed {
		t.Fatalf("rendered-extract target-index report = %+v, want reused page-two/index 2 with passed quality", report)
	}
	if pages := fakePagesCount(t); pages != 2 {
		t.Fatalf("rendered-extract target-index page count=%d, want existing pages preserved", pages)
	}
}

func TestRenderedExtractTargetIndexPreservesPositionalURLReuse(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", "https://example.test/navigated", "--target-index", "2",
		"--state-dir", stateDir, "--out-dir", t.TempDir(), "--wait", "0s", "--settle", "0s",
		"--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("rendered-extract positional URL target-index exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var report struct {
		OK     bool `json:"ok"`
		Target struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"target"`
		TargetIndex int `json:"target_index"`
		Workflow    struct {
			Trigger     string `json:"trigger"`
			CreatedPage bool   `json:"created_page"`
			ReusedPage  bool   `json:"reused_page"`
			Closed      bool   `json:"closed"`
			Cleanup     struct {
				Skipped bool   `json:"skipped"`
				Reason  string `json:"reason"`
			} `json:"cleanup"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("rendered-extract positional URL output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !report.OK || report.Target.ID != "page-two" || report.Target.URL != "https://example.test/navigated" || report.TargetIndex != 2 || report.Workflow.Trigger != "navigate" || report.Workflow.CreatedPage || !report.Workflow.ReusedPage || report.Workflow.Closed || !report.Workflow.Cleanup.Skipped || report.Workflow.Cleanup.Reason != "caller_owned" {
		t.Fatalf("rendered-extract positional URL report = %+v, want indexed existing-page navigation", report)
	}
	if pages := fakePagesCount(t); pages != 2 {
		t.Fatalf("rendered-extract positional URL page count=%d, want no workflow-created page", pages)
	}
}

func TestRenderedExtractReportsOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "only-page", "type": "page", "title": "Only page", "url": "https://example.test/only",
	}})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", "--target-index", "2", "--state-dir", stateDir, "--wait", "0s", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("rendered-extract out-of-range exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	assertTargetIndexError(t, out.Bytes(), "target_not_found")
}
