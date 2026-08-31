package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestResponsiveAuditExposesTargetIndex(t *testing.T) {
	var describeOut, describeErr bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "workflow responsive-audit", "--json"}, &describeOut, &describeErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe responsive-audit exit=%d stdout=%s stderr=%s", code, describeOut.String(), describeErr.String())
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
		t.Fatalf("describe responsive-audit output is invalid JSON: %v; output=%s", err, describeOut.String())
	}
	foundFlag := false
	for _, flag := range describe.Commands.Flags {
		if flag.Name == "target-index" && flag.Type == "int" {
			foundFlag = true
			break
		}
	}
	if !describe.OK || !foundFlag || !examplesContainTargetIndex(describe.Commands.Examples) {
		t.Fatalf("describe responsive-audit = %+v, want integer target-index and example", describe)
	}
	var helpOut, helpErr bytes.Buffer
	code = cli.Execute(context.Background(), []string{"workflow", "responsive-audit", "--help"}, &helpOut, &helpErr, cli.BuildInfo{})
	if code != cli.ExitOK || !containsAny(helpOut.String(), "exact-target cleanup", "caller-owned page") {
		t.Fatalf("responsive-audit help exit=%d stdout=%s stderr=%s, want ownership cleanup contract", code, helpOut.String(), helpErr.String())
	}

	var schemaOut, schemaErr bytes.Buffer
	code = cli.Execute(context.Background(), []string{"schema", "workflow-responsive-audit", "--json"}, &schemaOut, &schemaErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("schema responsive-audit exit=%d stdout=%s stderr=%s", code, schemaOut.String(), schemaErr.String())
	}
	var schema struct {
		OK     bool `json:"ok"`
		Schema struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Fields      []struct {
				Name        string `json:"name"`
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"fields"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(schemaOut.Bytes(), &schema); err != nil {
		t.Fatalf("schema responsive-audit output is invalid JSON: %v; output=%s", err, schemaOut.String())
	}
	foundField := false
	foundCleanup := false
	for _, field := range schema.Schema.Fields {
		if field.Name == "target_index" && field.Type == "integer" {
			foundField = true
		}
		if field.Name == "cleanup" && field.Type == "workflow_page_cleanup" && containsAny(field.Description, "target_gone", "caller-owned") {
			foundCleanup = true
		}
	}
	if !schema.OK || schema.Schema.Name != "workflow-responsive-audit" || !foundField || !foundCleanup || !containsAny(schema.Schema.Description, "target-index", "existing page") {
		t.Fatalf("schema responsive-audit = %+v, want indexed existing-page and cleanup contract", schema)
	}
}

func TestResponsiveAuditRejectsInvalidTargetIndexBeforeConnection(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"workflow", "responsive-audit", "--target-index", value, "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("target-index %s exit=%d stdout=%s stderr=%s", value, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
		})
	}
}

func TestResponsiveAuditRequiresURLWithoutTargetIndex(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "responsive-audit", "--viewports", "desktop", "--wait", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("responsive-audit without URL/index exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

func TestResponsiveAuditSelectsExistingPageByTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")
	fakePageNavigateCount.Store(0)

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "responsive-audit", "--target-index", "2", "--viewports", "desktop", "--include", "layout", "--wait", "0s", "--limit", "1", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("responsive-audit target-index exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var report struct {
		OK     bool `json:"ok"`
		Target struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"target"`
		TargetIndex int `json:"target_index"`
		Workflow    struct {
			URL       string   `json:"url"`
			Viewports []string `json:"viewports"`
		} `json:"workflow"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("responsive-audit target-index output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !report.OK || report.Target.ID != "page-two" || report.Target.URL != "https://example.test/second" || report.TargetIndex != 2 || report.Workflow.URL != "https://example.test/second" || len(report.Results) != 1 {
		t.Fatalf("responsive-audit report=%+v, want page-two/index 2 and one viewport", report)
	}
	if navigations := fakePageNavigateCount.Load(); navigations != 1 {
		t.Fatalf("responsive-audit indexed no-URL navigations=%d, want one viewport navigation", navigations)
	}
	if pages := fakePagesCount(t); pages != 2 {
		t.Fatalf("responsive-audit indexed page count=%d, want caller pages retained", pages)
	}
}

func TestResponsiveAuditNavigatesSelectedPageByTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")
	fakePageNavigateCount.Store(0)
	requestedURL := "https://example.test/responsive"

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "responsive-audit", requestedURL, "--target-index", "2", "--viewports", "desktop", "--include", "layout", "--wait", "0s", "--limit", "1", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("responsive-audit indexed navigation exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var report struct {
		OK     bool `json:"ok"`
		Target struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"target"`
		TargetIndex int `json:"target_index"`
		Workflow    struct {
			URL string `json:"url"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("responsive-audit indexed navigation output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !report.OK || report.Target.ID != "page-two" || report.Target.URL != requestedURL || report.TargetIndex != 2 || report.Workflow.URL != requestedURL {
		t.Fatalf("responsive-audit report=%+v, want indexed navigation of page-two", report)
	}
	if navigations := fakePageNavigateCount.Load(); navigations != 1 {
		t.Fatalf("responsive-audit indexed URL navigations=%d, want one viewport navigation", navigations)
	}
	if pages := fakePagesCount(t); pages != 2 {
		t.Fatalf("responsive-audit indexed navigation page count=%d, want caller pages retained", pages)
	}
}

func TestResponsiveAuditClosesWorkflowOwnedPage(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "existing-page", "type": "page", "title": "Existing", "url": "https://example.test/existing",
	}})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "responsive-audit", "https://example.test/new", "--viewports", "desktop", "--include", "layout", "--wait", "0s", "--limit", "1", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("responsive-audit URL-only exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if pages := fakePagesCount(t); pages != 1 {
		t.Fatalf("responsive-audit URL-only page count=%d, want created page cleaned up", pages)
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
