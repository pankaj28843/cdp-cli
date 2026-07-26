package perplexity

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
	DeleteSchemaVersion      = "perplexity-conversation-delete/v1"
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
	RouteMatches    bool    `json:"route_matches"`
	MenuCount       int     `json:"menu_count"`
	MenuReady       bool    `json:"menu_ready"`
	MenuExpanded    bool    `json:"menu_expanded"`
	MenuX           float64 `json:"menu_x"`
	MenuY           float64 `json:"menu_y"`
	DeleteItemCount int     `json:"delete_item_count"`
	DeleteItemReady bool    `json:"delete_item_ready"`
	DeleteItemX     float64 `json:"delete_item_x"`
	DeleteItemY     float64 `json:"delete_item_y"`
	ConfirmCount    int     `json:"confirm_count"`
	ConfirmReady    bool    `json:"confirm_ready"`
	ConfirmX        float64 `json:"confirm_x"`
	ConfirmY        float64 `json:"confirm_y"`
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
		observation.ConfirmCount != 1 ||
		!observation.ConfirmReady {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact Perplexity delete confirmation was not actionable")
	}
	return browserflow.ClickPoint(
		ctx,
		session,
		observation.ConfirmX,
		observation.ConfirmY,
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
			"perplexity_invalid_conversation_id", "usage",
			"Perplexity conversation id contains unsupported characters",
			"", data, nil,
		)
	}
	conversation := conversationRef(conversationID)
	if config.Store == nil {
		return deleteFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversation,
			"perplexity_state_unavailable", "internal",
			"Perplexity owner-only state is unavailable", "", data,
			[]string{"cdp workflow agent perplexity doctor --json"},
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
			"perplexity_auth_"+auth.State, "auth",
			"Perplexity auth evidence is not ready before delete", "", data,
			[]string{"cdp workflow agent perplexity auth refresh --json"},
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
				Origin+"/search/"+conversationID,
			); err != nil {
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"perplexity_delete_page_unavailable", "connection",
					"Perplexity exact conversation page could not be prepared",
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
						observation.MenuCount == 1 &&
						observation.MenuReady, nil
				},
			)
			data.PreparationAttempts = attempts
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"perplexity_delete_controls_not_ready", "provider",
					"Perplexity exact conversation menu did not become uniquely actionable",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if observation.ConfirmCount != 0 {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"perplexity_preexisting_delete_dialog", "provider",
					"Perplexity delete dialog existed before the current exact menu action",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if !observation.MenuExpanded {
				outcome, clickErr := browserflow.ClickPoint(
					ctx,
					session,
					observation.MenuX,
					observation.MenuY,
				)
				if clickErr != nil ||
					outcome.Dispatch != browserflow.DispatchPerformed {
					_ = lease.MarkIncomplete(context.Background())
					return deleteFailure(
						runID, config, webagent.StageAttached,
						target, pending, notPerformed, conversation,
						"perplexity_delete_menu_open_failed", "provider",
						"Perplexity exact current-session menu was not opened once",
						"", data, cleanupCommands(runID, pending),
					)
				}
			}
			menuItemAttempts, err := pollUntil(
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
						observation.MenuCount == 1 &&
						observation.MenuExpanded &&
						observation.DeleteItemCount == 1 &&
						observation.DeleteItemReady &&
						observation.ConfirmCount == 0, nil
				},
			)
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StagePrepared, target, pending,
					notPerformed, conversation,
					"perplexity_delete_menuitem_not_ready", "provider",
					"Perplexity exact Delete menuitem did not become uniquely actionable",
					"", data, cleanupCommands(runID, pending),
				)
			}
			data.Metadata["menu_item_attempts"] = menuItemAttempts
			menuItemOutcome, clickErr := browserflow.ClickPoint(
				ctx,
				session,
				observation.DeleteItemX,
				observation.DeleteItemY,
			)
			if clickErr != nil &&
				menuItemOutcome.Dispatch == browserflow.DispatchNotPerformed {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StagePrepared, target, pending,
					notPerformed, conversation,
					"perplexity_delete_dialog_not_requested", "provider",
					"Perplexity Delete menuitem was not performed",
					"", data, cleanupCommands(runID, pending),
				)
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
						observation.ConfirmCount == 1 &&
						observation.ConfirmReady, nil
				},
			)
			data.ActionabilityAttempts = actionabilityAttempts
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StagePrepared, target, pending,
					notPerformed, conversation,
					"perplexity_delete_confirmation_not_ready", "provider",
					"Perplexity exact Delete confirmation did not become actionable",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"perplexity_delete_prepare_state_failed", "internal",
					"Perplexity prepared delete state could not be persisted",
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
					"perplexity_delete_not_performed", "provider",
					"Perplexity Delete confirmation was not performed; retrying is safe",
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
						"perplexity_delete_postcondition_state_failed", "internal",
						"Perplexity delete postcondition could not be persisted; do not repeat deletion",
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
						"perplexity_delete_terminal_state_failed", "internal",
						"Perplexity terminal delete state could not be persisted",
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
						"cdp workflow agent perplexity conversations list --json",
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
				"perplexity_delete_unconfirmed", "completion",
				"Perplexity Delete confirmation was attempted but its same-target postcondition was not proved; do not repeat deletion",
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
	  const menus = Array.from(document.querySelectorAll(
	    'button[aria-label="Session actions"][aria-haspopup="menu"]'
	  )).filter(button => {
	    if (!visible(button)) return false;
	    const rect = button.getBoundingClientRect();
	    return rect.top >= 0 && rect.top < 96 &&
	      rect.right > window.innerWidth / 2;
	  });
	  const deleteItems = Array.from(
	    document.querySelectorAll('[role="menuitem"]')
	  ).filter(
	    item => visible(item) &&
	      (item.getAttribute('aria-label') || item.innerText ||
	        item.textContent || '').trim() === 'Delete'
	  );
	  const dialogs = Array.from(document.querySelectorAll('[role="dialog"]')).filter(
	    visible
	  );
	  const confirms = dialogs.length === 1
	    ? Array.from(dialogs[0].querySelectorAll('button')).filter(
	        button => visible(button) &&
	          (button.getAttribute('aria-label') || button.innerText ||
	            button.textContent || '').trim() === 'Delete'
	      )
	    : [];
	  const menu = menus.length === 1 ? menus[0] : null;
	  const menuAction = actionable(menu);
	  const deleteItemAction = actionable(
	    deleteItems.length === 1 ? deleteItems[0] : null
	  );
	  const confirmAction = actionable(confirms.length === 1 ? confirms[0] : null);
	  return {
	    route_matches: location.origin === 'https://www.perplexity.ai' &&
	      location.pathname === '/search/' + expected,
	    menu_count: menus.length,
	    menu_ready: menuAction.ready,
	    menu_expanded: Boolean(menu && menu.getAttribute('aria-expanded') === 'true'),
	    menu_x: menuAction.x,
	    menu_y: menuAction.y,
	    delete_item_count: deleteItems.length,
	    delete_item_ready: deleteItemAction.ready,
	    delete_item_x: deleteItemAction.x,
	    delete_item_y: deleteItemAction.y,
	    confirm_count: confirms.length,
	    confirm_ready: confirmAction.ready,
	    confirm_x: confirmAction.x,
	    confirm_y: confirmAction.y,
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
	    confirmed: location.origin === 'https://www.perplexity.ai' &&
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
