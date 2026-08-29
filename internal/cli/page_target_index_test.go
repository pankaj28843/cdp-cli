package cli

import (
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestResolvePageTargetByIndexCountsOnlyPagesInListOrder(t *testing.T) {
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
