package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	DeleteSchemaVersion      = "gemini-conversation-delete/v1"
	deletePostconditionProof = "redirected_to_app_without_conversation_id"
)

type DeleteConfig struct {
	BrowserConfig
	Store        *Store
	Timeout      time.Duration
	PollInterval time.Duration
	Now          func() time.Time
	Confirm      browserflow.Dispatcher
}

type DeleteData struct {
	SchemaVersion         string         `json:"schema_version"`
	CompletionState       string         `json:"completion_state"`
	PreparationAttempts   int            `json:"preparation_attempts"`
	ActionabilityAttempts int            `json:"actionability_attempts"`
	Postcondition         string         `json:"postcondition,omitempty"`
	Metadata              map[string]any `json:"metadata"`
}

type deleteObservation struct {
	RouteMatches bool            `json:"route_matches"`
	Header       actionablePoint `json:"header"`
	MenuDelete   actionablePoint `json:"menu_delete"`
	Confirm      actionablePoint `json:"confirm"`
}

type deleteDispatcher struct {
	conversationID string
}

func (d deleteDispatcher) Dispatch(
	ctx context.Context,
	session *cdp.PageSession,
) (browserflow.DispatchOutcome, error) {
	var observation deleteObservation
	if err := observeDeleteControls(
		ctx,
		session,
		d.conversationID,
		&observation,
	); err != nil ||
		!observation.RouteMatches ||
		observation.Confirm.Count != 1 ||
		!observation.Confirm.Ready {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact Gemini delete confirmation was not actionable")
	}
	return browserflow.ClickPoint(
		ctx,
		session,
		observation.Confirm.X,
		observation.Confirm.Y,
	)
}

func DeleteConversation(
	ctx context.Context,
	config DeleteConfig,
	conversationID string,
) webagent.Result {
	conversationID = strings.TrimSpace(conversationID)
	runID := webagent.NewRunID()
	data := DeleteData{
		SchemaVersion:   DeleteSchemaVersion,
		CompletionState: "not_deleted",
		Metadata:        map[string]any{},
	}
	notPerformed := notPerformedAction()
	if !conversationIDPattern.MatchString(conversationID) {
		return deleteFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"gemini_invalid_conversation_id", "usage",
			"Gemini conversation id must contain exactly 16 safe characters",
			"", data, nil,
		)
	}
	conversation := conversationRef(conversationID)
	if config.Store == nil {
		return deleteFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversation,
			"gemini_state_unavailable", "internal",
			"Gemini owner-only state is unavailable", "", data,
			[]string{"cdp workflow agent gemini doctor --json"},
		)
	}
	auth := config.Store.AuthStatus(ctx, nowForDelete(config), DefaultAuthTTL)
	if !auth.Ready {
		return deleteFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversation,
			"gemini_auth_"+auth.State, "auth",
			"Gemini auth evidence is not ready before delete", "", data,
			[]string{"cdp workflow agent gemini auth refresh --json"},
		)
	}
	if config.Timeout <= 0 {
		config.Timeout = 45 * time.Second
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationConversationsDelete,
		"about:blank",
		"headed_browser",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			if err := preparePage(
				ctx,
				config.Client,
				session,
				HomeURL+"/"+conversationID,
			); err != nil {
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"gemini_delete_page_unavailable", "connection",
					"Gemini exact conversation page could not be prepared",
					"", data, cleanupCommands(runID, pending),
				)
			}
			deadline := time.Now().Add(config.Timeout)
			prepared, attempts, err := prepareDeleteDialog(
				ctx,
				session,
				conversationID,
				deadline,
				config.PollInterval,
			)
			data.PreparationAttempts = attempts
			data.ActionabilityAttempts = attempts
			if err != nil || !prepared {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"gemini_delete_prepare_failed", "provider",
					"Gemini exact delete confirmation did not become uniquely actionable",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"gemini_delete_prepare_state_failed", "internal",
					"Gemini prepared delete state could not be persisted",
					"", data, cleanupCommands(runID, pending),
				)
			}
			dispatcher := config.Confirm
			if dispatcher == nil {
				dispatcher = deleteDispatcher{conversationID: conversationID}
			}
			outcome, _ := lease.Dispatch(ctx, dispatcher)
			action := actionEvidence(lease.Record())
			if outcome.Dispatch == browserflow.DispatchNotPerformed ||
				(outcome.Dispatch == "" && lease.Record().RawInputCount == 0) {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StagePrepared, target, pending,
					action, conversation,
					"gemini_delete_not_performed", "provider",
					"Gemini delete confirmation was not performed; retrying is safe",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if waitForDeletePostcondition(
				ctx,
				session,
				conversationID,
				deadline,
				config.PollInterval,
			) {
				if err := lease.ConfirmPostcondition(
					ctx,
					deletePostconditionProof,
				); err != nil {
					retryAt := retryAtFromRecord(
						lease.Record(),
						nowForDelete(config),
					)
					return deleteFailure(
						runID, config, webagent.StageActionDispatched,
						target, pending, action, conversation,
						"gemini_delete_postcondition_state_failed", "internal",
						"Gemini delete postcondition could not be persisted; do not repeat deletion",
						retryAt.Format(time.RFC3339Nano), data,
						cleanupCommands(runID, pending),
					)
				}
				action = actionEvidence(lease.Record())
				data.CompletionState = "deleted"
				data.Postcondition = deletePostconditionProof
				data.Metadata["same_target"] = true
				if err := lease.MarkTerminal(ctx); err != nil {
					return deleteFailure(
						runID, config, webagent.StageObserveTerminal,
						target, pending, action, conversation,
						"gemini_delete_terminal_state_failed", "internal",
						"Gemini terminal delete state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return operationSuccess(
					runID, config.BuildCommit,
					webagent.OperationConversationsDelete,
					webagent.StateTerminal, webagent.StageObserveTerminal,
					"headed_browser", target, pending, action, conversation,
					data, []string{"cdp workflow agent gemini conversations list --json"},
				)
			}
			_ = lease.MarkIncomplete(context.Background())
			retryAt := retryAtFromRecord(lease.Record(), nowForDelete(config))
			data.CompletionState = "deletion_ambiguous"
			return deleteFailure(
				runID, config, webagent.StageActionDispatched,
				target, pending, action, conversation,
				"gemini_delete_unconfirmed", "completion",
				"Gemini delete was attempted but its same-target postcondition was not proved; do not repeat deletion",
				retryAt.Format(time.RFC3339Nano), data,
				cleanupCommands(runID, pending),
			)
		},
	)
}

func prepareDeleteDialog(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	deadline time.Time,
	poll time.Duration,
) (bool, int, error) {
	attempts := 0
	headerClicked := false
	menuDeleteClicked := false
	var lastErr error
	for {
		attempts++
		var observation deleteObservation
		err := observeDeleteControls(ctx, session, conversationID, &observation)
		switch {
		case err != nil:
			lastErr = err
		case !observation.RouteMatches:
			lastErr = fmt.Errorf("exact Gemini conversation route was not observed")
		case observation.Header.Count > 1 ||
			observation.MenuDelete.Count > 1 ||
			observation.Confirm.Count > 1:
			return false, attempts, fmt.Errorf("Gemini delete control was ambiguous")
		case observation.Confirm.Count == 1 && observation.Confirm.Ready:
			return true, attempts, nil
		case observation.MenuDelete.Count == 1 &&
			observation.MenuDelete.Ready &&
			!menuDeleteClicked:
			menuDeleteClicked = true
			lastErr = clickReversibleControl(ctx, session, observation.MenuDelete)
		case observation.Header.Count == 1 &&
			observation.Header.Ready &&
			!headerClicked:
			headerClicked = true
			lastErr = clickReversibleControl(ctx, session, observation.Header)
		default:
			lastErr = fmt.Errorf("Gemini delete controls are not actionable")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if lastErr == nil {
				lastErr = fmt.Errorf("Gemini delete preparation deadline exhausted")
			}
			return false, attempts, lastErr
		}
		if !waitRendered(ctx, poll, remaining) {
			return false, attempts, ctx.Err()
		}
	}
}

func observeDeleteControls(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	observation *deleteObservation,
) error {
	idJSON, err := json.Marshal(conversationID)
	if err != nil {
		return fmt.Errorf("encode Gemini conversation id")
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const inspect = elements => {
	    const matches = Array.from(elements).filter(visible);
	    if (matches.length !== 1) return {ready: false, count: matches.length, x: 0, y: 0};
	    const element = matches[0];
	    element.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
	    const rect = element.getBoundingClientRect();
	    const x = rect.left + rect.width / 2;
	    const y = rect.top + rect.height / 2;
	    const top = document.elementFromPoint(x, y);
	    const enabled = !element.hasAttribute('disabled') &&
	      element.getAttribute('aria-disabled') !== 'true';
	    return {
	      ready: enabled && Boolean(top && (top === element || element.contains(top))),
	      count: 1,
	      x,
	      y
	    };
	  };
	  const header = Array.from(document.querySelectorAll('button')).filter(button =>
	    (button.getAttribute('aria-label') || '') ===
	      'Open menu for conversation actions.'
	  );
	  const menuDelete = Array.from(
	    document.querySelectorAll('[role=menuitem],gem-menu-item[data-test-id=delete-button]')
	  ).filter(element =>
	    (element.innerText || element.textContent || '').trim() === 'Delete'
	  );
	  const dialogs = Array.from(document.querySelectorAll('[role=dialog]')).filter(visible);
	  const confirm = dialogs.flatMap(dialog =>
	    Array.from(dialog.querySelectorAll('button')).filter(button =>
	      (button.getAttribute('aria-label') || button.innerText || '').trim() === 'Delete'
	    )
	  );
	  const match = location.pathname.match(/^\/app\/([A-Za-z0-9_-]{16})$/);
	  return {
	    route_matches: location.origin === 'https://gemini.google.com' &&
	      Boolean(match) && match[1] === expected,
	    header: inspect(header),
	    menu_delete: inspect(menuDelete),
	    confirm: inspect(confirm)
	  };
	})()`, idJSON)
	return evaluateInto(ctx, session, expression, observation)
}

func waitForDeletePostcondition(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	deadline time.Time,
	poll time.Duration,
) bool {
	idJSON, err := json.Marshal(conversationID)
	if err != nil {
		return false
	}
	expression := fmt.Sprintf(`(() => ({
	  ready: location.origin === 'https://gemini.google.com' &&
	    location.pathname === '/app' &&
	    !location.href.includes(%s)
	}))()`, idJSON)
	for {
		var observation struct {
			Ready bool `json:"ready"`
		}
		if evaluateInto(ctx, session, expression, &observation) == nil &&
			observation.Ready {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || !waitRendered(ctx, poll, remaining) {
			return false
		}
	}
}

func deleteFailure(
	runID string,
	config DeleteConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	code string,
	errClass string,
	message string,
	retryAt string,
	data DeleteData,
	nextCommands []string,
) webagent.Result {
	return operationFailure(
		runID, config.BuildCommit,
		webagent.OperationConversationsDelete,
		stage, "headed_browser", target, cleanup, action, conversation,
		code, errClass, message, retryAt, data, nextCommands,
	)
}

func nowForDelete(config DeleteConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}
