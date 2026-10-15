package chatgpt

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestAskRefusesRestoredDraftBeforeSelectionOrInput(t *testing.T) {
	client := testsupport.NewBrowser("user-page")
	client.Evaluate = func(expression string, _ *testsupport.Browser) (any, error) {
		if !strings.Contains(expression, "send_ready:") {
			t.Fatal("selection or preparation ran before draft refusal")
		}
		return map[string]any{"route_ready": true, "editor_ready": true, "editor_count": 1, "canonical_characters": 39, "draft_characters": 39, "prompt_matches": false}, nil
	}
	stateDir := t.TempDir()
	engine, journal, err := testsupport.NewRuntime(stateDir, client)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	result := Ask(context.Background(), AskConfig{BrowserConfig: BrowserConfig{Client: client, Engine: engine, Journal: journal}, Store: store, ComposerTimeout: time.Second, Timeout: time.Second}, "new storyboard request")
	if result.Error == nil || result.Error.Code != "chatgpt_restored_draft_detected" {
		t.Fatalf("result=%+v", result)
	}
	data := result.Data.(AskData)
	if data.CompletionState != "not_submitted" || result.Action == nil || result.Action.RawInputCount != 0 || !result.Action.RetrySafe {
		t.Fatalf("unsafe result=%+v data=%+v", result, data)
	}
	_, _, reloads, _, inserts, sends, targets := client.Snapshot()
	if inserts != 0 || sends != 0 || len(reloads) != 0 {
		t.Fatalf("mutations: insert=%d send=%d reload=%v", inserts, sends, reloads)
	}
	if len(targets) != 2 || targets["user-page"].TargetID != "user-page" ||
		result.Cleanup.State != webagent.CleanupPending || client.Counts["Target.closeTarget"] != 0 {
		t.Fatalf("unknown restored draft was not preserved: %v", targets)
	}
}

func TestFreshComposerDoesNotConfuseBlankWithDraft(t *testing.T) {
	blank := composerObservation{RouteReady: true, EditorReady: true, EditorCount: 1, InnerTextCharacters: 1, CanonicalCharacters: 1, DraftCharacters: 0}
	if restoredComposerDraft(blank) {
		t.Fatal("native empty paragraph treated as a draft")
	}
	blank.DraftCharacters = 9
	if !restoredComposerDraft(blank) {
		t.Fatal("restored draft missed")
	}
	blank.ConversationID = "existing"
	if restoredComposerDraft(blank) {
		t.Fatal("conversation treated as a fresh composer")
	}
}
