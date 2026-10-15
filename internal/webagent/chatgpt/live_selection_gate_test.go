package chatgpt

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

// Explicit opt-in: use only an existing headed daemon, one owned target, and
// the real preparation/selection/guard path. This test never inserts or sends.
func TestLiveExplicitModelSelectionNoSend(t *testing.T) {
	stateDir := os.Getenv("CDP_CHATGPT_LIVE_SELECTION_STATE_DIR")
	if stateDir == "" {
		t.Skip("explicit existing-daemon state directory required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultComposerTimeout)
	defer cancel()
	runtime, ok, err := daemon.LoadRuntimeForMode(ctx, stateDir, "headed")
	if err != nil || !ok {
		t.Fatalf("existing headed runtime unavailable: %v", err)
	}
	client, _, err := (daemon.RuntimeClient{Runtime: runtime}).BeginLease(ctx, defaultComposerTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.EndLease(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	journal, err := browserflow.NewFileJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := browserflow.New(browserflow.Config{Client: client, Journal: journal, InputLockPath: browserflow.HeadedInputLockPath(stateDir), Budget: cdp.BrowserResourceBudgetOptions{BrowserMode: "headed"}})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Acquire(ctx, browserflow.AcquireRequest{RunID: webagent.NewRunID(), Provider: "chatgpt", Operation: "capabilities", InitialURL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, err := lease.Close(context.Background())
		if err != nil || !cleanup.TargetGone {
			t.Errorf("exact owned target cleanup not proved: %v", err)
		}
	}()
	if err := preparePage(ctx, client, lease.Session(), HomeURL); err != nil {
		t.Fatal(err)
	}
	_, err = authreadiness.WaitForEvidence(ctx, lease.Session(), authreadiness.MinimumAttempts, defaultComposerTimeout, 250*time.Millisecond, func(ctx context.Context) (bool, error) {
		var o composerObservation
		if err := observeComposer(ctx, lease.Session(), "", "", &o); err != nil {
			return false, err
		}
		if restoredComposerDraft(o) {
			t.Fatal("restored draft refused before selection")
		}
		return o.RouteReady && o.EditorReady && o.EditorCount == 1 && o.DraftCharacters == 0 && o.UserMessageCount == 0 && o.AssistantCount == 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := selectChatGPT(ctx, lease.Session(), SelectionPolicy{Thinking: "Pro", MinimumThinking: "Pro", Model: "GPT-6"}, false, defaultComposerTimeout, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Model != "GPT-6" || selection.Intelligence != "Pro" || selection.ThinkingSliderValue != selection.ThinkingSliderMax {
		t.Fatal("explicit model/maximum Pro proof missing")
	}
	if _, err := selectChatGPTTool(ctx, lease.Session(), ToolCreateImage, defaultComposerTimeout, 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := verifySelectionAtSendWithExpectation(ctx, lease.Session(), selection.expectation, "GPT-6", finalSelectionGuardTimeout, 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	var composer composerObservation
	if err := observeComposerWithTool(ctx, lease.Session(), "", "Create image", "Pro", &composer); err != nil {
		t.Fatal(err)
	}
	if !composer.PromptMatches || composer.DraftCharacters != 0 || composer.SelectedTool != "Create image" || composer.UserMessageCount != 0 || composer.AssistantCount != 0 {
		t.Fatal("blank Create image composer proof missing")
	}
	t.Log("full explicit GPT-6 / maximum Pro / Create image selection and final guard passed; no text or Send; exact owned cleanup required")
}
