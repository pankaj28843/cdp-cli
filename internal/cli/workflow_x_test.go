package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowXCollectReturnsTypedThreadAndProfileRecords(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	for _, tt := range []struct {
		name, rawURL, kind string
		wait               string
	}{
		{"thread", "https://x.com/karpathy/status/2079610838143623371", "post_thread", "0"},
		{"profile remount accumulation", "https://x.com/karpathy", "profile_posts", "1s"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"workflow", "x", "collect", tt.rawURL, "--limit", "2", "--wait", tt.wait, "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			var got struct {
				Kind     string `json:"kind"`
				Coverage struct {
					ObservedRecordKinds        []string `json:"observed_record_kinds"`
					PossiblyMissingRecordKinds []string `json:"possibly_missing_record_kinds"`
					Continuation               string   `json:"continuation"`
					UnresolvedControls         bool     `json:"unresolved_controls"`
					TerminationEvidence        []string `json:"termination_evidence"`
				} `json:"coverage"`
				Records []struct {
					Kind string `json:"kind"`
					ID   string `json:"id"`
				} `json:"records"`
				Workflow struct {
					Count         int    `json:"count"`
					Status        string `json:"status"`
					PartialReason string `json:"partial_reason"`
					Interactions  *int   `json:"interactions"`
				} `json:"workflow"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got.Kind != tt.kind || len(got.Records) != 2 || got.Workflow.Count != 2 || got.Workflow.Status != "partial" || got.Workflow.PartialReason != "requested_limit" || got.Workflow.Interactions == nil {
				t.Fatalf("collection = %+v", got)
			}
			if len(got.Coverage.ObservedRecordKinds) == 0 || !got.Coverage.UnresolvedControls || len(got.Coverage.TerminationEvidence) != 1 || got.Coverage.TerminationEvidence[0] != "requested_limit" {
				t.Fatalf("coverage = %+v", got.Coverage)
			}
			want := "discussion_partial"
			if tt.kind == "profile_posts" {
				want = "profile_scroll"
			}
			if got.Coverage.Continuation != want {
				t.Fatalf("coverage continuation = %q, want %q", got.Coverage.Continuation, want)
			}
			if tt.kind == "profile_posts" && (got.Records[0].ID != "2079610838143623371" || got.Records[1].ID != "2079610838143623999") {
				t.Fatalf("remounted profile records = %+v, want earliest serialized posts", got.Records)
			}
		})
	}
}
