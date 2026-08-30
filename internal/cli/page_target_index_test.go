package cli

import (
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestResolvePageTargetByIndexCountsOnlyPagesInStableTargetIDOrder(t *testing.T) {
	target, err := resolvePageTargetByIndex([]cdp.TargetInfo{
		{TargetID: "worker", Type: "worker"},
		{TargetID: "first-page", Type: "page"},
		{TargetID: "frame", Type: "iframe"},
		{TargetID: "second-page", Type: "page"},
	}, 2)
	if err != nil {
		t.Fatalf("resolve page index: %v", err)
	}
	if target.TargetID != "second-page" {
		t.Fatalf("target = %q, want second-page", target.TargetID)
	}
}

func TestPageIndexesUseStableTargetIDOrderAcrossResponsePermutations(t *testing.T) {
	permutations := [][]cdp.TargetInfo{
		{
			{TargetID: "PAGEC333", Type: "page", URL: "https://example.test/shared"},
			{TargetID: "WORKER00", Type: "worker"},
			{TargetID: "PAGEA111", Type: "page", URL: "https://example.test/shared"},
			{TargetID: "PAGEB222", Type: "page", URL: "https://example.test/shared"},
		},
		{
			{TargetID: "PAGEB222", Type: "page", URL: "https://example.test/shared"},
			{TargetID: "PAGEC333", Type: "page", URL: "https://example.test/shared"},
			{TargetID: "PAGEA111", Type: "page", URL: "https://example.test/shared"},
			{TargetID: "WORKER00", Type: "worker"},
		},
	}
	for index, targets := range permutations {
		originalFirstID := targets[0].TargetID
		rows := pageRows(targets)
		if len(rows) != 3 || rows[0]["id"] != "PAGEA111" || rows[1]["id"] != "PAGEB222" || rows[2]["id"] != "PAGEC333" {
			t.Fatalf("permutation %d page rows = %#v, want target-ID order", index, rows)
		}
		selected, err := resolvePageTargetByIndex(targets, 2)
		if err != nil || selected.TargetID != "PAGEB222" {
			t.Fatalf("permutation %d index 2 = %+v, %v; want PAGEB222", index, selected, err)
		}
		_, err = resolvePageTarget(targets, "", "example.test/shared", "")
		commandErr, ok := err.(*CommandError)
		if !ok || commandErr.Code != "ambiguous_target" {
			t.Fatalf("permutation %d error = %v, want ambiguous_target", index, err)
		}
		data, ok := commandErr.Data.(map[string]any)
		ids, idsOK := data["candidate_ids"].([]string)
		indexes, indexesOK := data["candidate_indexes"].([]int)
		if !ok || !idsOK || !indexesOK || len(ids) != 3 || ids[0] != "PAGEA111" || ids[1] != "PAGEB222" || ids[2] != "PAGEC333" || len(indexes) != 3 || indexes[0] != 1 || indexes[1] != 2 || indexes[2] != 3 {
			t.Fatalf("permutation %d candidate evidence = %#v, want stable IDs/indexes", index, commandErr.Data)
		}
		if targets[0].TargetID != originalFirstID || targetRows(targets)[0]["id"] != originalFirstID {
			t.Fatalf("permutation %d input/generic order mutated: %+v", index, targets)
		}
	}
}

func TestResolvePageTargetByIndexRejectsInvalidAndStaleIndexes(t *testing.T) {
	tests := []struct {
		name  string
		index int
		code  string
	}{
		{name: "zero", index: 0, code: "invalid_target_index"},
		{name: "stale", index: 2, code: "target_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolvePageTargetByIndex([]cdp.TargetInfo{
				{TargetID: "only-page", Type: "page"},
			}, test.index)
			if err == nil {
				t.Fatal("resolve page index succeeded, want error")
			}
			commandErr, ok := err.(*CommandError)
			if !ok {
				t.Fatalf("error type = %T, want *CommandError", err)
			}
			if commandErr.Code != test.code {
				t.Fatalf("error code = %q, want %q", commandErr.Code, test.code)
			}
		})
	}
}
