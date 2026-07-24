package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowArxivCollectReturnsNativePaperAndReferences(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "arxiv", "collect", "https://arxiv.org/abs/2604.12374v2", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Kind  string `json:"kind"`
		Paper struct {
			Identifier   string `json:"identifier"`
			CanonicalURL string `json:"canonical_url"`
			Title        string `json:"title"`
		} `json:"paper"`
		Coverage struct {
			ObservedRecordKinds        []string `json:"observed_record_kinds"`
			PossiblyMissingRecordKinds []string `json:"possibly_missing_record_kinds"`
			Continuation               string   `json:"continuation"`
			TerminationEvidence        []string `json:"termination_evidence"`
		} `json:"coverage"`
		References []struct {
			ID string `json:"id"`
		} `json:"references"`
		Workflow struct {
			Interactions *int `json:"interactions"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "paper" || got.Paper.Identifier != "2604.12374v2" || got.Paper.CanonicalURL != "/abs/2604.12374v2" || got.Paper.Title == "" || len(got.References) != 1 || got.Workflow.Interactions == nil || *got.Workflow.Interactions != 0 || len(got.Coverage.ObservedRecordKinds) != 2 || got.Coverage.Continuation != "not_applicable" || len(got.Coverage.PossiblyMissingRecordKinds) != 0 || len(got.Coverage.TerminationEvidence) != 1 || got.Coverage.TerminationEvidence[0] != "fully_rendered_document" {
		t.Fatalf("collection=%+v", got)
	}
}
