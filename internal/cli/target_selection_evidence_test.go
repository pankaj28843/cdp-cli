package cli

import (
	"errors"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestTargetRowsPublishDirectSelectionMetadata(t *testing.T) {
	targets := []cdp.TargetInfo{
		{TargetID: "workerabcdef1234", Type: "worker"},
		{TargetID: "pageabcdef123456", Type: "page"},
		{TargetID: "second1234567890", Type: "page"},
	}
	all := targetRows(targets)
	if got := all[0]["short_id"]; got != "WORKERAB" {
		t.Fatalf("target short_id = %v, want WORKERAB", got)
	}
	pages := pageRows(targets)
	if len(pages) != 2 || pages[0]["index"] != 1 || pages[1]["index"] != 2 {
		t.Fatalf("page indexes = %+v, want page-only 1,2 in listed order", pages)
	}
}

func TestAmbiguousTargetErrorsIncludeBoundedShortIDs(t *testing.T) {
	targets := make([]cdp.TargetInfo, 0, 12)
	for i := 0; i < 12; i++ {
		targets = append(targets, cdp.TargetInfo{
			TargetID: "ABC" + string(rune('A'+i)) + "1234567890",
			Type:     "page",
		})
	}

	assertAmbiguousTargetEvidence(t, func() error {
		_, err := resolvePageTarget(targets, "abc", "", "")
		return err
	}())
	assertAmbiguousTargetEvidence(t, func() error {
		_, err := resolveProtocolTarget(targets, "abc", "", "", "page")
		return err
	}())
}

func assertAmbiguousTargetEvidence(t *testing.T, err error) {
	t.Helper()
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "ambiguous_target" {
		t.Fatalf("error = %v, want ambiguous_target", err)
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("ambiguous target data = %#v, want object", commandErr.Data)
	}
	shortIDs, ok := data["candidate_short_ids"].([]string)
	if data["candidate_count"] != 12 || data["candidate_truncated"] != true || !ok || len(shortIDs) != 10 {
		t.Fatalf("ambiguous target data = %#v, want count 12 and ten bounded IDs", data)
	}
}
