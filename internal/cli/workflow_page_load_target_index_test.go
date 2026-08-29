package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestPageLoadExposesTargetIndex(t *testing.T) {
	var describeOut, describeErr bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "workflow page-load", "--json"}, &describeOut, &describeErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe workflow page-load exit=%d stdout=%s stderr=%s", code, describeOut.String(), describeErr.String())
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
		t.Fatalf("describe workflow page-load output is invalid JSON: %v; output=%s", err, describeOut.String())
	}
	foundFlag := false
	for _, flag := range describe.Commands.Flags {
		if flag.Name == "target-index" && flag.Type == "int" {
			foundFlag = true
			break
		}
	}
	if !describe.OK || !foundFlag || !examplesContainTargetIndex(describe.Commands.Examples) {
		t.Fatalf("describe workflow page-load = %+v, want integer target-index flag and example", describe)
	}

	var schemaOut, schemaErr bytes.Buffer
	code = cli.Execute(context.Background(), []string{"schema", "workflow-page-load", "--json"}, &schemaOut, &schemaErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("schema workflow-page-load exit=%d stdout=%s stderr=%s", code, schemaOut.String(), schemaErr.String())
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
		t.Fatalf("schema workflow-page-load output is invalid JSON: %v; output=%s", err, schemaOut.String())
	}
	foundField := false
	for _, field := range schema.Schema.Fields {
		if field.Name == "target_index" && field.Type == "integer" {
			foundField = true
			break
		}
	}
	if !schema.OK || schema.Schema.Name != "workflow-page-load" || !foundField {
		t.Fatalf("schema workflow-page-load = %+v, want integer target_index field", schema)
	}
}

func TestPageLoadRejectsInvalidTargetIndexBeforeConnection(t *testing.T) {
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
			args := append([]string{"workflow", "page-load"}, test.args...)
			args = append(args, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("page-load %s exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			wantCode := "invalid_target_index"
			if test.name != "zero" && test.name != "negative" {
				wantCode = "invalid_target_selector"
			}
			assertTargetIndexError(t, out.Bytes(), wantCode)
		})
	}
}

func TestPageLoadSelectsExistingPageByTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "page-load", "--target-index", "2", "--state-dir", stateDir,
		"--wait", "250ms", "--limit", "2", "--include", "console,network", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page-load target-index exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var report struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		TargetIndex int `json:"target_index"`
		Requests    []struct {
			ID string `json:"id"`
		} `json:"requests"`
		Messages []struct {
			Text string `json:"text"`
		} `json:"messages"`
		Workflow struct {
			Trigger      string `json:"trigger"`
			RequestedURL string `json:"requested_url"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("page-load target-index output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !report.OK || report.Target.ID != "page-two" || report.TargetIndex != 2 || report.Workflow.Trigger != "observe" || report.Workflow.RequestedURL != "" {
		t.Fatalf("page-load target-index report = %+v, want page-two/index 2 observe report", report)
	}
	if len(report.Requests) == 0 || len(report.Messages) == 0 || !strings.Contains(report.Messages[0].Text, "Synthetic") {
		t.Fatalf("page-load target-index evidence requests=%+v messages=%+v, want bounded collector evidence", report.Requests, report.Messages)
	}
	if pages := fakePagesCount(t); pages != 2 {
		t.Fatalf("page-load target-index page count=%d, want existing pages preserved", pages)
	}
}

func TestPageLoadTargetIndexPreservesPositionalURLNavigation(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "page-load", "https://example.test/navigated", "--target-index", "2",
		"--state-dir", stateDir, "--wait", "0s", "--limit", "1", "--include", "console", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page-load positional URL target-index exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var report struct {
		OK     bool `json:"ok"`
		Target struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"target"`
		TargetIndex int `json:"target_index"`
		Workflow    struct {
			Trigger      string `json:"trigger"`
			RequestedURL string `json:"requested_url"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("page-load positional URL output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !report.OK || report.Target.ID != "page-two" || report.Target.URL != "https://example.test/navigated" || report.TargetIndex != 2 || report.Workflow.Trigger != "navigate" || report.Workflow.RequestedURL != "https://example.test/navigated" {
		t.Fatalf("page-load positional URL report = %+v, want indexed existing-page navigation", report)
	}
	if pages := fakePagesCount(t); pages != 2 {
		t.Fatalf("page-load positional URL page count=%d, want no workflow-created page", pages)
	}
}

func TestPageLoadReportsOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "only-page", "type": "page", "title": "Only page", "url": "https://example.test/only",
	}})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "page-load", "--target-index", "2", "--state-dir", stateDir, "--wait", "0s", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("page-load out-of-range exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	assertTargetIndexError(t, out.Bytes(), "target_not_found")
}
