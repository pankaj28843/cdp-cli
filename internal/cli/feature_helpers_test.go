package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestAssertValueFailureSingleJSONEnvelope(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "value", "textarea", "not-the-value", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("assert value exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Assertion struct {
				Passed bool   `json:"passed"`
				Actual string `json:"actual"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert value output is not one JSON document: %v; stdout=%s", err, out.String())
	}
	if got.OK || got.Code != "assertion_failed" || got.Data.Assertion.Passed || got.Data.Assertion.Actual != "SGVsbG8gVVg=" {
		t.Fatalf("assert failure envelope = %+v, want single error envelope with assertion data", got)
	}
}

func TestResponsiveAuditInvalidViewportJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "responsive-audit", "https://example.test", "--viewports", "watch", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("responsive-audit invalid viewport exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("responsive-audit output is invalid JSON: %v", err)
	}
	if got.Code != "invalid_viewport" {
		t.Fatalf("responsive-audit invalid viewport code = %q, want invalid_viewport", got.Code)
	}
}

func TestPageCleanupWorkflowCreatedClosesRecentVisibleTaggedPage(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "visible-cdp", "type": "page", "url": "https://example.test/workflow", "title": "Workflow", "attached": false}})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")
	now := time.Now().UTC().Format(time.RFC3339)
	state := `{"pages":[{"connection":"default","target_id":"visible-cdp","url":"https://example.test/workflow","title":"Workflow","created_by":"cdp","workflow":"responsive-audit","first_seen":"` + now + `","last_seen":"` + now + `"}]}`
	if err := os.WriteFile(filepath.Join(stateDir, "page-cleanup.json"), []byte(state), 0o600); err != nil {
		t.Fatalf("write page cleanup state: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "cleanup", "--workflow-created", "--close", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page cleanup exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		Cleanup struct {
			ClosedCount int `json:"closed_count"`
		} `json:"cleanup"`
		Closed []struct {
			Ready bool `json:"ready"`
		} `json:"closed"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page cleanup output is invalid JSON: %v", err)
	}
	if got.Cleanup.ClosedCount != 1 || len(got.Closed) != 1 || !got.Closed[0].Ready {
		t.Fatalf("page cleanup = %+v, want tagged visible workflow page closed", got)
	}
}
