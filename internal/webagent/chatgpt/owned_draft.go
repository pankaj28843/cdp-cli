package chatgpt

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

// A private ask receipt and its journal bind cleanup to a prompt this workflow
// sent. The receipt never authorizes deletion of a different browser draft.
func ownedDraftFingerprintFromReceipt(ctx context.Context, path string, journal browserflow.Journal) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 || journal == nil {
		return "", errors.New("invalid owned draft receipt file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > 1<<20 {
		return "", errors.New("owned draft receipt changed before read")
	}
	contents, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(contents) > 1<<20 {
		return "", errors.New("owned draft receipt read failed")
	}
	var receipt struct {
		OK           bool                      `json:"ok"`
		Provider     webagent.Provider         `json:"provider"`
		Operation    webagent.Operation        `json:"operation"`
		State        webagent.State            `json:"state"`
		Stage        webagent.Stage            `json:"stage"`
		Action       *webagent.ActionEvidence  `json:"action"`
		Error        *webagent.OperationError  `json:"error"`
		Conversation *webagent.ConversationRef `json:"conversation"`
		Cleanup      webagent.CleanupEvidence  `json:"cleanup"`
		Evidence     webagent.Evidence         `json:"evidence"`
		Data         struct {
			PromptFingerprint string `json:"prompt_fingerprint"`
		} `json:"data"`
	}
	if err := json.Unmarshal(contents, &receipt); err != nil {
		return "", err
	}
	fingerprint := receipt.Data.PromptFingerprint
	decoded, err := hex.DecodeString(fingerprint)
	if err != nil || len(decoded) != 32 ||
		receipt.Provider != webagent.ProviderChatGPT ||
		receipt.Operation != webagent.OperationAsk ||
		receipt.Action == nil || receipt.Action.Dispatch != webagent.DispatchPerformed ||
		receipt.Action.RawInputCount != 1 || receipt.Action.AttemptCount != 1 ||
		!receipt.Action.PendingPersisted ||
		receipt.Evidence.RunID == "" || receipt.Evidence.Target == nil ||
		!receipt.Evidence.Target.Owned {
		return "", errors.New("receipt does not prove one owned ChatGPT Send")
	}
	record, err := journal.Load(ctx, receipt.Evidence.RunID)
	if err != nil || record.Provider != string(webagent.ProviderChatGPT) ||
		record.Operation != string(webagent.OperationAsk) ||
		record.Dispatch != browserflow.DispatchPerformed || record.RawInputCount != 1 ||
		record.ActionAttemptCount != 1 || !record.PendingPersisted ||
		record.InputFingerprint != fingerprint || record.TargetID == "" ||
		record.TargetID != receipt.Evidence.Target.TargetID ||
		record.TargetID != receipt.Cleanup.TargetID {
		return "", errors.New("receipt does not match the private one-Send ask journal")
	}
	closed := receipt.OK && receipt.Error == nil && receipt.State == webagent.StateTerminal &&
		receipt.Stage == webagent.StageClosed && receipt.Conversation != nil &&
		receipt.Conversation.ID != "" && receipt.Cleanup.State == webagent.CleanupClosed &&
		receipt.Cleanup.TargetClosed && receipt.Evidence.Target.Closed &&
		record.Phase == browserflow.PhaseClosed && record.Cleanup == browserflow.CleanupClosed &&
		record.ConversationID == receipt.Conversation.ID
	identityUnproved := !receipt.OK && receipt.State == webagent.StateFailed &&
		receipt.Stage == webagent.StageActionDispatched && receipt.Error != nil &&
		receipt.Error.Code == "chatgpt_submission_identity_unproven" &&
		!receipt.Error.RetrySafe && receipt.Cleanup.State == webagent.CleanupPending &&
		!receipt.Cleanup.TargetClosed && !receipt.Evidence.Target.Closed &&
		record.Phase == browserflow.PhaseIncomplete && record.Cleanup == browserflow.CleanupPending
	if !closed && !identityUnproved {
		return "", errors.New("receipt and journal do not prove an eligible owned ChatGPT Send")
	}
	return fingerprint, nil
}

func ownedDraftCleanupMayHaveMutated(phase string) bool {
	switch phase {
	case "native_clear", "blank", "blur", "stable_blank":
		return true
	default:
		return false
	}
}

// Clear only the exact canonical draft from a prior owned one-Send ask. Any mismatch
// leaves the fresh target untouched. The current owned composer must remain
// blank before Ask is allowed to prepare its new prompt.
func clearExactOwnedRestoredDraft(ctx context.Context, client cdp.CommandClient, session *cdp.PageSession, fingerprint string) (string, error) {
	var before composerObservation
	if err := observeComposer(ctx, session, "", "", &before); err != nil {
		return "observe", err
	}
	if !restoredComposerDraft(before) || before.AttachmentCount != 0 ||
		fingerprintPrompt(before.CanonicalText) != fingerprint || before.DocumentTimeOrigin <= 0 {
		return "identity", errors.New("restored draft identity changed before cleanup")
	}
	if err := cdp.ActivateTargetWithClient(ctx, client, session.TargetID); err != nil {
		return "activate", err
	}
	expected, _ := json.Marshal(before.CanonicalText)
	focusExpression := fmt.Sprintf(`(() => {
	  const editors=[...document.querySelectorAll('[contenteditable="true"][role="textbox"]')];
	  if(editors.length!==1 || location.origin!=='https://chatgpt.com' || location.pathname!=='/' ||
	     document.querySelector('[data-user-message-bubble],[data-conversation-role="assistant"]') ||
	     document.querySelector('[data-composer-attachments] button[aria-label^="Remove "],[data-composer-attachments] img') ||
	     [...document.querySelectorAll('input[type="file"]')].some(input=>input.files&&input.files.length)) return false;
	  const e=editors[0], expected=%s;
	  const text=node=>node.nodeType===Node.TEXT_NODE?(node.nodeValue||''):
	    !(node instanceof HTMLElement)?'':
	    node.matches('[data-inline-selection-pill],[data-inline-selection-pill-cursor-target],span[app-mention-name="deep-research"][app-mention-path="app://connector_openai_deep_research"]')?'':
	    node.tagName==='BR'?'\n':[...node.childNodes].map(text).join('');
	  const block=node=>node instanceof HTMLElement&&node.dataset.emptyParagraph==='true'&&text(node)==='\n'?'':text(node);
	  if([...e.childNodes].map(block).join('\n').replace(/\r\n/g,'\n')!==expected) return false;
	  e.focus(); return document.activeElement===e;
	})()`, string(expected))
	if _, err := pollUntil(ctx, 3*time.Second, 100*time.Millisecond, func() (bool, error) {
		var focused bool
		err := evaluateInto(ctx, session, focusExpression, &focused)
		return focused, err
	}); err != nil {
		return "focus", errors.New("exact owned draft editor focus not proved")
	}
	if err := browserflow.ClearFocusedComposer(ctx, session); err != nil {
		return "native_clear", err
	}
	blank := func() (bool, error) {
		var observed composerObservation
		if err := observeComposer(ctx, session, "", "", &observed); err != nil {
			return false, err
		}
		return observed.DocumentTimeOrigin == before.DocumentTimeOrigin &&
			observed.DocumentReadyState == "complete" &&
			observed.RouteReady && observed.EditorReady && observed.EditorCount == 1 &&
			observed.CanonicalCharacters == 0 && observed.AttachmentCount == 0 &&
			observed.UserMessageCount == 0 && observed.AssistantCount == 0 &&
			observed.ConversationID == "", nil
	}
	if _, err := pollUntil(ctx, 5*time.Second, 100*time.Millisecond, blank); err != nil {
		return "blank", err
	}
	var blurred bool
	if err := evaluateInto(ctx, session, `(() => {const e=document.querySelector('[contenteditable="true"][role="textbox"]');if(!e)return false;e.blur();return document.activeElement!==e;})()`, &blurred); err != nil || !blurred {
		return "blur", errors.New("owned blank composer blur not proved")
	}
	stableSince := time.Time{}
	if _, err := pollUntil(ctx, 5*time.Second, 250*time.Millisecond, func() (bool, error) {
		ready, err := blank()
		if err != nil || !ready {
			stableSince = time.Time{}
			return false, err
		}
		if stableSince.IsZero() {
			stableSince = time.Now()
		}
		return time.Since(stableSince) >= 1500*time.Millisecond, nil
	}); err != nil {
		return "stable_blank", fmt.Errorf("owned draft blank stability not proved: %w", err)
	}
	return "blank_stable", nil
}
