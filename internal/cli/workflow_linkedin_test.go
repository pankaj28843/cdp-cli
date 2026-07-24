package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowLinkedInCollectReturnsNativeThreadAndCompanyRecords(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	for _, tt := range []struct{ name, rawURL, kind, status, reason, limit, wait string }{
		{"activity thread", "https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD/", "activity_thread", "exhausted", "", "2", "0"},
		{"company posts", "https://www.linkedin.com/company/the-pragmatic-engineer/posts/", "company_posts", "exhausted", "", "3", "2s"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"workflow", "linkedin", "collect", tt.rawURL, "--limit", tt.limit, "--wait", tt.wait, "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			var got struct {
				Kind     string `json:"kind"`
				Coverage struct {
					ObservedRecordKinds []string `json:"observed_record_kinds"`
					Continuation        string   `json:"continuation"`
					UnresolvedControls  bool     `json:"unresolved_controls"`
					TerminationEvidence []string `json:"termination_evidence"`
				} `json:"coverage"`
				Records []struct {
					ID string `json:"id"`
				} `json:"records"`
				Workflow struct {
					Count         int    `json:"count"`
					Status        string `json:"status"`
					PartialReason string `json:"partial_reason"`
					Interactions  *int   `json:"interactions"`
				} `json:"workflow"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Kind != tt.kind || len(got.Records) != 2 || got.Workflow.Count != 2 || got.Workflow.Status != tt.status || got.Workflow.PartialReason != tt.reason || got.Workflow.Interactions == nil {
				t.Fatalf("collection = %+v", got)
			}
			if len(got.Coverage.ObservedRecordKinds) == 0 || got.Coverage.UnresolvedControls || len(got.Coverage.TerminationEvidence) != 1 || got.Coverage.TerminationEvidence[0] != "source_exhaustion_proven" {
				t.Fatalf("coverage = %+v", got.Coverage)
			}
			want := "discussion_exhausted"
			if tt.kind == "company_posts" {
				want = "company_feed"
			}
			if got.Coverage.Continuation != want {
				t.Fatalf("coverage continuation = %q, want %q", got.Coverage.Continuation, want)
			}
		})
	}
}
