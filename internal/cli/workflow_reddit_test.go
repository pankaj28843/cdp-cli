package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowRedditCollectReturnsTypedThreadAndProfileRecords(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	for _, tt := range []struct {
		name, url, kind string
		wait            string
		interactions    int
	}{
		{"thread", "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/", "thread", "0", 2},
		{"profile accumulates after scroll", "https://www.reddit.com/user/celticpaladin/comments/", "user_profile", "1s", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"workflow", "reddit", "collect", tt.url, "--limit", "2", "--wait", tt.wait, "--json"}, &out, &errOut, cli.BuildInfo{})
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
					Count           int    `json:"count"`
					Status          string `json:"status"`
					PartialReason   string `json:"partial_reason"`
					Interactions    int    `json:"discussion_interactions"`
					AllInteractions int    `json:"interactions"`
				} `json:"workflow"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got.Kind != tt.kind || len(got.Records) != 2 || got.Workflow.Count != 2 || got.Workflow.Status != "partial" || got.Workflow.PartialReason != "requested_limit" || got.Workflow.Interactions != tt.interactions || got.Workflow.AllInteractions != tt.interactions {
				t.Fatalf("collection = %+v", got)
			}
			if got.Records[0].Kind != "submission" || got.Records[1].Kind != "comment" {
				t.Fatalf("record variants = %+v", got.Records)
			}
			if len(got.Coverage.ObservedRecordKinds) != 2 || len(got.Coverage.PossiblyMissingRecordKinds) != 0 || !got.Coverage.UnresolvedControls || len(got.Coverage.TerminationEvidence) != 1 || got.Coverage.TerminationEvidence[0] != "requested_limit" {
				t.Fatalf("coverage = %+v", got.Coverage)
			}
			if want := "discussion_partial"; tt.kind == "user_profile" {
				want = "profile_scroll"
				if got.Coverage.Continuation != want {
					t.Fatalf("coverage continuation = %q, want %q", got.Coverage.Continuation, want)
				}
			} else if got.Coverage.Continuation != want {
				t.Fatalf("coverage continuation = %q, want %q", got.Coverage.Continuation, want)
			}
			if tt.kind == "user_profile" && (got.Records[0].ID != "t3_1v010h6" || got.Records[1].ID != "t1_ozckogc") {
				t.Fatalf("remounted profile records = %+v, want earliest serialized submission then first new comment", got.Records)
			}
		})
	}
}

func TestWorkflowRedditThreadClearsPartialReasonWhenDiscussionIsExhausted(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "reddit", "collect", "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/", "--limit", "10", "--wait", "0", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Coverage struct {
			Continuation        string   `json:"continuation"`
			UnresolvedControls  bool     `json:"unresolved_controls"`
			TerminationEvidence []string `json:"termination_evidence"`
		} `json:"coverage"`
		Workflow struct {
			Status        string `json:"status"`
			PartialReason string `json:"partial_reason"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Workflow.Status != "exhausted" || got.Workflow.PartialReason != "" {
		t.Fatalf("workflow=%+v, want exhausted with no partial reason", got.Workflow)
	}
	if got.Coverage.Continuation != "discussion_exhausted" || got.Coverage.UnresolvedControls || len(got.Coverage.TerminationEvidence) != 1 || got.Coverage.TerminationEvidence[0] != "source_exhaustion_proven" {
		t.Fatalf("coverage=%+v, want exhausted discussion proof", got.Coverage)
	}
}
