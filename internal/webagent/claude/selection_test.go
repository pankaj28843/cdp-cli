package claude

import (
	"context"
	"testing"
	"time"
)

func TestAskSelectionFailureNeverSends(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rows   int
		after  string
		code   string
		insert int
	}{
		{"unavailable", 0, "", "claude_selection_unavailable", 0},
		{"ambiguous", 2, "", "claude_selection_unavailable", 0},
		{"changed_during_preparation", 1, "Haiku 4.5 Medium", "claude_selection_changed", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newAuthFakeClient("user-page")
			client.modelLabel = "Sonnet 5.5 Medium"
			client.selectionRowCount = tc.rows
			client.modelAfterInsert = tc.after
			config := newAskTestConfig(t, t.TempDir(), client)
			config.Timeout = time.Second
			config.ComposerTimeout = 20 * time.Millisecond
			config.Model = "Sonnet 5.5"
			result := Ask(context.Background(), config, "Synthetic selection guard")
			if result.OK || result.Error == nil || result.Error.Code != tc.code {
				t.Fatalf("result = %+v", result)
			}
			if client.callCount("Input.dispatchKeyEvent") != 0 || client.callCount("Input.insertText") != tc.insert {
				t.Fatalf("unexpected input: %+v", client.countSnapshot())
			}
			if client.hasTarget("owned-1") || !client.hasTarget("user-page") {
				t.Fatal("exact target cleanup failed")
			}
		})
	}
}
