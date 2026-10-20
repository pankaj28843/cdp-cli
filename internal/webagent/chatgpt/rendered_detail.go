package chatgpt

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

// A read-only recovery for an already acknowledged conversation. It runs on
// the exact target owned by conversationViaBrowser and never dispatches input.
type renderedDetail struct {
	ConversationID string `json:"conversation_id"`
	RouteMatches   bool   `json:"route_matches"`
	UserCount      int    `json:"user_count"`
	AssistantCount int    `json:"assistant_count"`
	Ordered        bool   `json:"ordered"`
	Text           string `json:"text"`
	Prompt         string `json:"prompt"`
	Streaming      bool   `json:"streaming"`
	Terminal       bool   `json:"terminal"`
}

const renderedDetailExpression = `(() => {
  const match = location.pathname.match(/^\/c\/([A-Za-z0-9_-]+)$/);
  const units = [...document.querySelectorAll('[data-content-search-unit-key]')];
  const users = units.filter(unit => unit.querySelector('[data-user-message-bubble]'));
  const assistants = units.filter(unit =>
    !unit.querySelector('[data-user-message-bubble]') &&
    unit.querySelector('.markdown,[data-markdown-text-style]'));
  const lastUser = users.at(-1);
  const lastAssistant = assistants.at(-1);
  const answer = lastAssistant && lastAssistant.querySelector('.markdown,[data-markdown-text-style]');
  const text = String(answer && (answer.innerText || answer.textContent) || '').trim();
  const promptNode = lastUser && lastUser.querySelector('[data-user-message-bubble]');
  const prompt = String(promptNode && (promptNode.innerText || promptNode.textContent) || '').trim();
  const ordered = Boolean(lastUser && lastAssistant &&
    (lastUser.compareDocumentPosition(lastAssistant) & Node.DOCUMENT_POSITION_FOLLOWING) &&
    units.at(-1) === lastAssistant);
  const visible = node => Boolean(node && !node.disabled &&
    node.getAttribute('aria-hidden') !== 'true' &&
    (node.offsetWidth || node.offsetHeight || node.getClientRects().length));
  const stop = [...document.querySelectorAll(
    'button[data-testid="stop-button"],button[aria-label*="Stop"],button[aria-label*="stop"]'
  )].some(visible);
  const busy = Boolean(lastAssistant && lastAssistant.querySelector(
    '[data-is-streaming="true"],[aria-busy="true"]'));
  const controls = lastAssistant && [...lastAssistant.querySelectorAll(
    'button[data-testid="copy-turn-action-button"],button[aria-label^="Copy"]'
  )].some(visible);
  const latest = [...document.querySelectorAll('[aria-label],[title]')].some(node =>
    visible(node) && /latest response/i.test(
      (node.getAttribute('aria-label') || '') + ' ' + (node.getAttribute('title') || '')
    ));
  const latestText = /\bLatest response\b/i.test(
    String(document.querySelector('main')?.innerText || ''));
  return {
    conversation_id: match ? match[1] : '', route_matches: Boolean(match),
    user_count: users.length, assistant_count: assistants.length,
    ordered, text, prompt, streaming: stop || busy,
    terminal: Boolean((controls || latest || latestText) && !stop && !busy)
  };
})()`

func recoverRenderedDetail(
	ctx context.Context,
	config ReadConfig,
	lease *browserflow.Lease,
	conversationID string,
	data ConversationDetailData,
	sourceFailure string,
	expectedPromptFingerprint string,
) (ConversationDetailData, bool) {
	if config.BrowserConfig == nil || ctx.Err() != nil {
		return data, false
	}
	route := Origin + "/c/" + url.PathEscape(conversationID)
	// Navigating the owned page does not need foreground activation. Browser
	// reads can overlap another agent's input lease without stealing focus.
	for _, method := range []string{"Runtime.enable", "Page.enable"} {
		if err := config.BrowserConfig.Client.CallSession(ctx, lease.Session().SessionID,
			method, map[string]any{}, nil); err != nil {
			return data, false
		}
	}
	if _, err := lease.Session().Navigate(ctx, route); err != nil {
		return data, false
	}
	deadline := time.Now().Add(15 * time.Second)
	var previous string
	stable := 0
	for time.Now().Before(deadline) && ctx.Err() == nil {
		var seen renderedDetail
		if err := evaluateInto(ctx, lease.Session(), renderedDetailExpression, &seen); err == nil &&
			validRenderedDetail(seen, conversationID) &&
			(expectedPromptFingerprint == "" || fingerprintPrompt(seen.Prompt) == expectedPromptFingerprint) {
			previous, stable = nextRenderedDetailStability(previous, stable, seen.Text)
			if stable >= 2 {
				data.Text = seen.Text
				data.StatusCode = 0 // The answer came from DOM, not the failed HTTP read.
				data.CompletionState = conversationCompletionTerminal
				data.ReadMode = "headed_browser_rendered"
				data.Metadata["answer_source"] = "same_conversation_rendered_assistant_message"
				data.Metadata["rendered_recovery_after"] = sourceFailure
				data.Metadata["rendered_terminal_stable_reads"] = stable
				return data, true
			}
		} else {
			previous, stable = "", 0
		}
		select {
		case <-ctx.Done():
			return data, false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return data, false
}

func nextRenderedDetailStability(previous string, stable int, text string) (string, int) {
	if text == previous {
		return previous, stable + 1
	}
	return text, 1
}

func validRenderedDetail(seen renderedDetail, conversationID string) bool {
	return seen.RouteMatches && seen.ConversationID == conversationID &&
		seen.UserCount > 0 && seen.AssistantCount > 0 && seen.Ordered &&
		seen.Terminal && !seen.Streaming && strings.TrimSpace(seen.Text) != ""
}

func renderedDetailSuccess(
	ctx context.Context, lease *browserflow.Lease, runID string,
	config ReadConfig, operation webagent.Operation,
	target *webagent.TargetEvidence, pending webagent.CleanupEvidence,
	conversation *webagent.ConversationRef, data ConversationDetailData,
	needsPreparation bool,
) webagent.Result {
	if needsPreparation {
		if failure := commitBrowserReadPreparation(ctx, lease); failure != nil {
			return browserReadFailureResult(runID, config.BuildCommit, operation,
				webagent.StageObserveTerminal, target, pending, *failure, data, conversation)
		}
	}
	if err := lease.MarkTerminal(ctx); err != nil {
		return browserReadFailureResult(runID, config.BuildCommit, operation,
			webagent.StageObserveTerminal, target, pending,
			*internalReadFailure("ChatGPT rendered detail terminal state could not be persisted"),
			data, conversation)
	}
	result := operationSuccess(runID, config.BuildCommit, operation,
		webagent.StageObserveTerminal, data.ReadMode, target, pending, data, nil)
	result.State = webagent.StateTerminal
	result.Conversation = conversation
	return result
}
