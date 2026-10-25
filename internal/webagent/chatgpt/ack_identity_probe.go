package chatgpt

import (
	"context"
	"errors"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

// classifyAckObservationError deliberately returns no browser-supplied text:
// JavaScript exceptions and CDP errors can contain page content or URLs.
func classifyAckObservationError(err error) string {
	var evaluation *evaluationFailure
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context_deadline"
	case errors.As(err, &evaluation):
		return evaluation.kind
	default:
		return "cdp_evaluate_error"
	}
}

// This intentionally smaller DOM probe runs only after repeated evaluation
// errors. It never navigates, sends, or accepts a route without the exact
// submitted prompt fingerprint on the same owned target.
const ackIdentityProbeExpression = `(() => {
  const match = location.pathname.match(/^\/c\/([A-Za-z0-9_-]+)$/);
  const users = [...document.querySelectorAll('[data-user-message-bubble]')].filter(
    element => element && (element.offsetWidth || element.offsetHeight || element.getClientRects().length)
  );
  const user = users.at(-1);
  const text = user && (user.innerText || user.textContent) || '';
  return {
    route_matches: Boolean(match), conversation_id: match ? match[1] : '',
    user_message_count: users.length, prompt_candidates: text ? [text] : []
  };
})()`

func probeAckIdentity(ctx context.Context, session *cdp.PageSession,
	expectedFingerprint string) (renderedObservation, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	for attempt := 0; attempt < 12 && probeCtx.Err() == nil; attempt++ {
		var observed renderedObservation
		if err := evaluateInto(probeCtx, session, ackIdentityProbeExpression,
			&observed); err == nil && validAckIdentity(observed, expectedFingerprint) {
			return observed, true
		}
		select {
		case <-probeCtx.Done():
			return renderedObservation{}, false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return renderedObservation{}, false
}

func validAckIdentity(observed renderedObservation, expectedFingerprint string) bool {
	return observed.RouteMatches &&
		conversationIDPattern.MatchString(observed.ConversationID) &&
		observed.UserMessageCount == 1 &&
		renderedPromptMatches(observed, expectedFingerprint)
}

func probeAckTargetPresence(ctx context.Context, client cdp.CommandClient,
	targetID string) any {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	info, err := cdp.TargetInfoWithClient(probeCtx, client, targetID)
	if err != nil {
		return "unknown"
	}
	return info.TargetID == targetID
}
