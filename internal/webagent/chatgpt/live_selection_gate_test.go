package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

// Explicit opt-in: use only an existing headed daemon, one owned target, and
// the real preparation/selection/guard path. Prepared mode inserts only its
// exact owned nonce and synthetic files, proves them, clears them, and never sends.
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
	ownedPrompt := ""
	preserveProbe := false
	defer func() {
		if preserveProbe {
			_ = lease.ReleaseInput()
			return
		}
		if ownedPrompt != "" {
			cleanupCtx, stop := context.WithTimeout(context.Background(), defaultComposerTimeout)
			defer stop()
			if err := clearOwnedNoSendPrompt(cleanupCtx, lease.Session(), ownedPrompt); err != nil {
				_ = lease.ReleaseInput()
				t.Errorf("own nonce not blank-proved; preserved owned target for reconciliation: %v", err)
				return
			}
			if err := lease.ReleaseInput(); err != nil {
				t.Error(err)
				return
			}
			if err := verifyFreshNoSendDeletion(cleanupCtx, engine, client); err != nil {
				t.Errorf("fresh-page draft deletion not proved; original owned probe preserved: %v", err)
				return
			}
		}
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
			preserveProbe = true
			t.Fatal("restored draft refused before selection; owned target preserved for reconciliation")
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
	var attachment *attachmentExpectation
	uploadCount := 0
	if os.Getenv("CDP_CHATGPT_LIVE_PREPARED_NO_SEND") == "1" {
		nonce := fmt.Sprintf("harness-owned-nosend-%d", time.Now().UnixNano())
		ownedPrompt = nonce + "\n" + strings.Repeat("Synthetic prepared storyboard inspection only. Do not submit this draft.\n", 58)
		dir := t.TempDir()
		files := make([]string, 4)
		for i := range files {
			files[i] = filepath.Join(dir, fmt.Sprintf("%s-fixture-%d.txt", nonce, i))
			if err := os.WriteFile(files[i], []byte(fmt.Sprintf("%s synthetic artifact %d", nonce, i)), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if manifest := os.Getenv("CDP_CHATGPT_LIVE_PREPARED_FILE_LIST"); manifest != "" {
			content, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(content, &files); err != nil || len(files) == 0 {
				t.Fatalf("invalid prepared file list: %v", err)
			}
		}
		uploads, err := resolveLocalUploads(AskConfig{FilePaths: files})
		if err != nil {
			t.Fatal(err)
		}
		receipts, expected, failure := attachLocalFilesOnce(ctx, lease.Session(), uploads, defaultComposerTimeout, 250*time.Millisecond)
		if failure != nil {
			t.Fatal(failure)
		}
		if len(receipts) != len(files) {
			t.Fatal("prepared upload receipts not proved")
		}
		uploadCount = len(receipts)
		attachment = expected
		if _, _, err := prepareVerifiedPromptWithExpectation(ctx, lease.Session(), ownedPrompt, "Pro", selection.expectation, "GPT-6", ToolCreateImage, attachment, defaultComposerTimeout, 250*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	if err := prepareSelectionGuardAtSendWithExpectation(ctx, lease.Session(), selection.expectation, "GPT-6", finalSelectionGuardTimeout, 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if ownedPrompt != "" {
		diagnostics := map[string]any{}
		dispatcher := chatgptSendDispatcher{prompt: ownedPrompt, intelligence: "Pro", model: "GPT-6", tool: ToolCreateImage, attachment: attachment, expectation: selection.expectation, diagnostics: diagnostics}
		if err := dispatcher.verifyBeforeSend(ctx, lease.Session()); err != nil {
			t.Fatalf("actual prepared dispatcher prefix failed at %v: %v", diagnostics["dispatch_guard_phase"], err)
		}
		t.Logf("actual dispatcher prefix passed with exact nonce prompt, %d confirmed uploads, GPT-6 / maximum Pro / Create image; NO Enter or Send; own nonce cleanup and exact close required", uploadCount)
		return
	}
	if err := observeSelectionGuardAtSendWithExpectation(ctx, lease.Session(), selection.expectation, "GPT-6"); err != nil {
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

// Clear only the exact prompt this test prepared on its fresh owned target.
// A route/turn/prompt mismatch preserves the target instead of touching a draft.
func clearOwnedNoSendPrompt(ctx context.Context, session *cdp.PageSession, prompt string) error {
	var before composerObservation
	if err := observeComposerWithTool(ctx, session, prompt, "Create image", "Pro", &before); err != nil {
		return err
	}
	if !before.RouteReady || before.ConversationID != "" || before.UserMessageCount != 0 || before.AssistantCount != 0 {
		return fmt.Errorf("fresh unsent baseline changed")
	}
	if before.DraftCharacters == 0 {
		return nil
	}
	if !before.PromptMatches {
		return fmt.Errorf("composer no longer matches exact owned nonce")
	}
	if _, err := browserflow.PressEscape(ctx, session); err != nil {
		return err
	}
	var focused bool
	if err := evaluateInto(ctx, session, `(() => { const editors=[...document.querySelectorAll('[contenteditable="true"][role="textbox"]')]; if(editors.length!==1)return false;editors[0].focus();return document.activeElement===editors[0]; })()`, &focused); err != nil || !focused {
		return fmt.Errorf("owned composer focus not proved")
	}
	for _, params := range []map[string]any{
		{"type": "keyDown", "key": "a", "code": "KeyA", "modifiers": 2, "windowsVirtualKeyCode": 65},
		{"type": "keyUp", "key": "a", "code": "KeyA", "modifiers": 2, "windowsVirtualKeyCode": 65},
		{"type": "keyDown", "key": "Backspace", "code": "Backspace", "windowsVirtualKeyCode": 8},
		{"type": "keyUp", "key": "Backspace", "code": "Backspace", "windowsVirtualKeyCode": 8},
	} {
		raw, _ := json.Marshal(params)
		if _, err := session.Exec(ctx, "Input.dispatchKeyEvent", raw); err != nil {
			return err
		}
	}
	_, err := pollUntil(ctx, 5*time.Second, 100*time.Millisecond, func() (bool, error) {
		var after composerObservation
		err := observeComposer(ctx, session, "", "Pro", &after)
		return err == nil && after.RouteReady && after.EditorReady && after.DraftCharacters == 0 && after.UserMessageCount == 0 && after.AssistantCount == 0, err
	})
	if err != nil {
		return err
	}
	var blurred bool
	if err := evaluateInto(ctx, session, `(() => {const e=document.querySelector('[contenteditable="true"][role="textbox"]');if(!e)return false;e.blur();return document.activeElement!==e;})()`, &blurred); err != nil || !blurred {
		return fmt.Errorf("owned blank composer blur not proved")
	}
	return nil
}

// DOM blank alone is insufficient: a provider may restore a persisted draft in
// the next conversation. Match the observed native protocol: blur, then hydrate
// a second exactly owned fresh page and prove blank/zero-turn/no-attachment state.
func verifyFreshNoSendDeletion(ctx context.Context, engine *browserflow.Engine, client daemon.RuntimeClient) error {
	fresh, err := engine.Acquire(ctx, browserflow.AcquireRequest{RunID: webagent.NewRunID(), Provider: "chatgpt", Operation: "capabilities", InitialURL: "about:blank"})
	if err != nil {
		return err
	}
	safeClose := false
	defer func() {
		if !safeClose {
			_ = fresh.ReleaseInput()
		}
	}()
	if err := preparePage(ctx, client, fresh.Session(), HomeURL); err != nil {
		return err
	}
	var observed composerObservation
	readiness, err := authreadiness.WaitForEvidence(ctx, fresh.Session(), authreadiness.MinimumAttempts, 60*time.Second, 250*time.Millisecond, func(ctx context.Context) (bool, error) {
		if err := observeComposer(ctx, fresh.Session(), "", "Pro", &observed); err != nil {
			return false, err
		}
		return observed.DocumentReadyState == "complete" && observed.RouteReady && observed.EditorReady && observed.EditorCount == 1 && composerChatProductReady(observed), nil
	})
	if err != nil || !readiness.Observed {
		return fmt.Errorf("fresh owned page hydration was not proved")
	}
	if observed.DraftCharacters != 0 || observed.UserMessageCount != 0 || observed.AssistantCount != 0 || observed.ConversationID != "" {
		return fmt.Errorf("fresh owned page restored input or turns; preserved for reconciliation")
	}
	var attachments int
	if err := evaluateInto(ctx, fresh.Session(), `document.querySelectorAll('[data-composer-attachments] button[aria-label^="Remove "]').length`, &attachments); err != nil || attachments != 0 {
		return fmt.Errorf("fresh owned page still has attachments; preserved for reconciliation")
	}
	cleanup, err := fresh.Close(ctx)
	if err != nil || !cleanup.TargetGone {
		return fmt.Errorf("fresh verification target exact close not proved")
	}
	safeClose = true
	return nil
}
