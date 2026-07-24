package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowHackerNewsCollectReturnsNativeThreadRecords(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "hacker-news", "collect", "https://news.ycombinator.com/item?id=46641042", "--limit", "10", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Kind     string `json:"kind"`
		Coverage struct {
			ObservedRecordKinds        []string `json:"observed_record_kinds"`
			PossiblyMissingRecordKinds []string `json:"possibly_missing_record_kinds"`
			Continuation               string   `json:"continuation"`
			TerminationEvidence        []string `json:"termination_evidence"`
		} `json:"coverage"`
		Records []struct {
			ID string `json:"id"`
		} `json:"records"`
		Workflow struct {
			Interactions *int `json:"interactions"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "thread" || len(got.Records) != 3 || got.Workflow.Interactions == nil || *got.Workflow.Interactions != 0 || len(got.Coverage.ObservedRecordKinds) != 2 || got.Coverage.Continuation != "not_applicable" || len(got.Coverage.PossiblyMissingRecordKinds) != 0 || len(got.Coverage.TerminationEvidence) != 1 || got.Coverage.TerminationEvidence[0] != "fully_rendered_document" {
		t.Fatalf("collection=%+v", got)
	}
}

func TestWorkflowHackerNewsCollectRejectsResolvedItemDrift(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId":                    "hn-redirect-mismatch",
		"type":                        "page",
		"url":                         "https://example.test/",
		"title":                       "HN redirect mismatch fixture",
		"fakeRenderedExtractFinalURL": "https://news.ycombinator.com/item?id=46641043",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "hacker-news", "collect", "https://news.ycombinator.com/item?id=46641042", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed || !bytes.Contains(out.Bytes(), []byte("hacker_news_identity_changed")) {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}
