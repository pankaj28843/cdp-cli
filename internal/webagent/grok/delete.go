package grok

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
	DeleteSchemaVersion      = "grok-conversation-delete/v1"
	deletePostconditionProof = "redirected_home_without_conversation_id"
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
	RouteMatches bool    `json:"route_matches"`
	MoreCount    int     `json:"more_count"`
	MoreReady    bool    `json:"more_ready"`
	MoreX        float64 `json:"more_x"`
	MoreY        float64 `json:"more_y"`
	DeleteCount  int     `json:"delete_count"`
	DeleteReady  bool    `json:"delete_ready"`
	DeleteX      float64 `json:"delete_x"`
	DeleteY      float64 `json:"delete_y"`
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
		observation.DeleteCount != 1 ||
		!observation.DeleteReady {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact Grok delete menuitem was not actionable")
	}
	return browserflow.ClickPoint(
		ctx,
		session,
		observation.DeleteX,
		observation.DeleteY,
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
			"grok_invalid_conversation_id", "usage",
			"Grok conversation id contains unsupported characters",
			"", data, nil,
		)
	}
	conversation := conversationRef(conversationID)
	if config.Store == nil {
		return deleteFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversation,
			"grok_state_unavailable", "internal",
			"Grok owner-only state is unavailable", "", data,
			[]string{"cdp workflow agent grok doctor --json"},
		)
	}
	auth := config.Store.AuthStatus(
		ctx,
		nowForDelete(config),
		DefaultAuthTTL,
	)
	if !auth.Ready {
		return deleteFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversation,
			"grok_auth_"+auth.State, "auth",
			"Grok auth evidence is not ready before delete", "", data,
			[]string{"cdp workflow agent grok auth refresh --json"},
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
		"delete",
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
				Origin+"/c/"+conversationID,
			); err != nil {
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"grok_delete_page_unavailable", "connection",
					"Grok exact conversation page could not be prepared",
					"", data, cleanupCommands(runID, pending),
				)
			}
			deadline := time.Now().Add(config.Timeout)
			var observation deleteObservation
			attempts, err := pollUntil(
				ctx,
				time.Until(deadline),
				config.PollInterval,
				func() (bool, error) {
					if err := observeDeleteControls(
						ctx,
						session,
						conversationID,
						&observation,
					); err != nil {
						return false, err
					}
					return observation.RouteMatches &&
						(observation.DeleteCount == 1 &&
							observation.DeleteReady ||
							observation.MoreCount == 1 &&
								observation.MoreReady), nil
				},
			)
			data.PreparationAttempts = attempts
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"grok_delete_controls_not_ready", "provider",
					"Grok exact conversation menu did not become uniquely actionable",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if observation.DeleteCount != 1 || !observation.DeleteReady {
				outcome, clickErr := browserflow.ClickPoint(
					ctx,
					session,
					observation.MoreX,
					observation.MoreY,
				)
				if clickErr != nil ||
					outcome.Dispatch != browserflow.DispatchPerformed {
					_ = lease.MarkIncomplete(context.Background())
					return deleteFailure(
						runID, config, webagent.StageAttached,
						target, pending, notPerformed, conversation,
						"grok_delete_menu_open_failed", "provider",
						"Grok exact conversation menu was not opened once",
						"", data, cleanupCommands(runID, pending),
					)
				}
			}
			actionabilityAttempts, err := pollUntil(
				ctx,
				time.Until(deadline),
				config.PollInterval,
				func() (bool, error) {
					if err := observeDeleteControls(
						ctx,
						session,
						conversationID,
						&observation,
					); err != nil {
						return false, err
					}
					return observation.RouteMatches &&
						observation.DeleteCount == 1 &&
						observation.DeleteReady, nil
				},
			)
			data.ActionabilityAttempts = actionabilityAttempts
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StagePrepared, target, pending,
					notPerformed, conversation,
					"grok_delete_action_not_ready", "provider",
					"Grok exact Delete Chat menuitem did not become uniquely actionable",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"grok_delete_prepare_state_failed", "internal",
					"Grok prepared delete state could not be persisted",
					"", data, cleanupCommands(runID, pending),
				)
			}
			dispatcher := config.Confirm
			if dispatcher == nil {
				dispatcher = deleteDispatcher{
					conversationID: conversationID,
				}
			}
			outcome, _ := lease.Dispatch(ctx, dispatcher)
			action := actionEvidence(lease.Record())
			_ = lease.ReleaseInput()
			if outcome.Dispatch == browserflow.DispatchNotPerformed ||
				(outcome.Dispatch == "" &&
					lease.Record().RawInputCount == 0) {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StagePrepared, target, pending,
					action, conversation,
					"grok_delete_not_performed", "provider",
					"Grok Delete Chat was not performed; retrying is safe",
					"", data, cleanupCommands(runID, pending),
				)
			}
			postcondition := false
			_, _ = pollUntil(
				ctx,
				time.Until(deadline),
				config.PollInterval,
				func() (bool, error) {
					var value struct {
						Confirmed bool `json:"confirmed"`
					}
					if err := observeDeletePostcondition(
						ctx,
						session,
						conversationID,
						&value,
					); err != nil {
						return false, err
					}
					postcondition = value.Confirmed
					return postcondition, nil
				},
			)
			if postcondition {
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
						"grok_delete_postcondition_state_failed", "internal",
						"Grok delete postcondition could not be persisted; do not repeat deletion",
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
						"grok_delete_terminal_state_failed", "internal",
						"Grok terminal delete state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return operationSuccess(
					runID, config.BuildCommit,
					webagent.OperationConversationsDelete,
					webagent.StateTerminal,
					webagent.StageObserveTerminal,
					"headed_browser", target, pending, action,
					conversation, data,
					[]string{
						"cdp workflow agent grok conversations list --json",
					},
				)
			}
			_ = lease.MarkIncomplete(context.Background())
			retryAt := retryAtFromRecord(
				lease.Record(),
				nowForDelete(config),
			)
			data.CompletionState = "deletion_ambiguous"
			return deleteFailure(
				runID, config, webagent.StageActionDispatched,
				target, pending, action, conversation,
				"grok_delete_unconfirmed", "completion",
				"Grok Delete Chat was attempted but its same-target postcondition was not proved; do not repeat deletion",
				retryAt.Format(time.RFC3339Nano), data,
				cleanupCommands(runID, pending),
			)
		},
	)
}

func observeDeleteControls(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	observation *deleteObservation,
) error {
	idJSON, err := json.Marshal(conversationID)
	if err != nil {
		return err
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
	  const actionable = element => {
	    if (!element || !visible(element) ||
	      element.hasAttribute('disabled') ||
	      element.getAttribute('aria-disabled') === 'true') {
	      return {ready: false, x: 0, y: 0};
	    }
	    const rect = element.getBoundingClientRect();
	    const x = rect.left + rect.width / 2;
	    const y = rect.top + rect.height / 2;
	    const top = document.elementFromPoint(x, y);
	    return {
	      ready: Boolean(top && (top === element || element.contains(top))),
	      x,
	      y,
	    };
	  };
	  const buttons = Array.from(document.querySelectorAll('button')).filter(visible);
	  const more = buttons.filter(button =>
	    (button.getAttribute('aria-label') || button.innerText ||
	      button.textContent || '').trim() === 'More'
	  );
	  const deletes = Array.from(document.querySelectorAll('[role="menuitem"]')).filter(
	    item => visible(item) &&
	      (item.innerText || item.textContent || '').trim() === 'Delete Chat'
	  );
	  const moreAction = actionable(more.length === 1 ? more[0] : null);
	  const deleteAction = actionable(deletes.length === 1 ? deletes[0] : null);
	  return {
	    route_matches: location.origin === 'https://grok.com' &&
	      location.pathname === '/c/' + expected,
	    more_count: more.length,
	    more_ready: moreAction.ready,
	    more_x: moreAction.x,
	    more_y: moreAction.y,
	    delete_count: deletes.length,
	    delete_ready: deleteAction.ready,
	    delete_x: deleteAction.x,
	    delete_y: deleteAction.y,
	  };
	})()`, idJSON)
	return evaluateInto(ctx, session, expression, observation)
}

func observeDeletePostcondition(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	value any,
) error {
	idJSON, err := json.Marshal(conversationID)
	if err != nil {
		return err
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  return {
	    confirmed: location.origin === 'https://grok.com' &&
	      location.pathname === '/' &&
	      !location.href.includes(expected),
	  };
	})()`, idJSON)
	return evaluateInto(ctx, session, expression, value)
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
