package chatgpt

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
	DeleteSchemaVersion      = "chatgpt-conversation-delete/v1"
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
	RouteMatches      bool    `json:"route_matches"`
	SidebarState      string  `json:"sidebar_state"`
	OpenSidebarCount  int     `json:"open_sidebar_count"`
	OpenSidebarReady  bool    `json:"open_sidebar_ready"`
	OpenSidebarX      float64 `json:"open_sidebar_x"`
	OpenSidebarY      float64 `json:"open_sidebar_y"`
	LinkCount         int     `json:"link_count"`
	RowButtonCount    int     `json:"row_button_count"`
	RowButtonNameOK   bool    `json:"row_button_name_ok"`
	PageButtonCount   int     `json:"page_button_count"`
	PageButtonReady   bool    `json:"page_button_ready"`
	PageButtonX       float64 `json:"page_button_x"`
	PageButtonY       float64 `json:"page_button_y"`
	DeleteMenuCount   int     `json:"delete_menu_count"`
	DeleteMenuReady   bool    `json:"delete_menu_ready"`
	DeleteMenuX       float64 `json:"delete_menu_x"`
	DeleteMenuY       float64 `json:"delete_menu_y"`
	DialogCount       int     `json:"dialog_count"`
	ConfirmationCount int     `json:"confirmation_count"`
	ConfirmationReady bool    `json:"confirmation_ready"`
	ConfirmationX     float64 `json:"confirmation_x"`
	ConfirmationY     float64 `json:"confirmation_y"`
}

type chatgptDeleteDispatcher struct {
	conversationID string
}

func (d chatgptDeleteDispatcher) Dispatch(
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
		!strictDeleteIdentity(observation) ||
		observation.DialogCount != 1 ||
		observation.ConfirmationCount != 1 ||
		!observation.ConfirmationReady {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact ChatGPT delete confirmation was not actionable")
	}
	return browserflow.ClickPoint(
		ctx,
		session,
		observation.ConfirmationX,
		observation.ConfirmationY,
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
			"chatgpt_invalid_conversation_id", "usage",
			"ChatGPT conversation id contains unsupported characters",
			"", data, nil,
		)
	}
	conversation := conversationRef(conversationID)
	if config.Store == nil {
		return deleteFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversation,
			"chatgpt_state_unavailable", "internal",
			"ChatGPT owner-only state is unavailable", "", data,
			[]string{"cdp workflow agent chatgpt doctor --json"},
		)
	}
	_, auth, templateErr := config.Store.LoadTemplateStatus(
		ctx,
		nowForDelete(config),
		DefaultAuthTTL,
	)
	if templateErr != nil && auth.State == "invalid" {
		return deleteFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversation,
			"chatgpt_auth_invalid", "auth",
			"ChatGPT owner-only auth evidence is invalid before delete",
			"", data,
			[]string{"cdp workflow agent chatgpt auth refresh --json"},
		)
	}
	if !auth.Ready {
		return deleteFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversation,
			"chatgpt_auth_"+auth.State, "auth",
			"ChatGPT auth evidence is not ready before delete", "", data,
			[]string{"cdp workflow agent chatgpt auth refresh --json"},
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
					"chatgpt_delete_page_unavailable", "connection",
					"ChatGPT exact conversation page could not be prepared",
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
						(strictDeleteIdentity(observation) ||
							observation.OpenSidebarCount == 1 &&
								observation.OpenSidebarReady), nil
				},
			)
			data.PreparationAttempts = attempts
			if err != nil {
				recordDeleteObservation(&data, observation)
				data.Metadata["observation_failure"] =
					deleteObservationFailure(err)
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"chatgpt_delete_identity_not_ready", "provider",
					"ChatGPT exact conversation identity did not become ready",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if !strictDeleteIdentity(observation) {
				outcome, clickErr := browserflow.ClickPoint(
					ctx,
					session,
					observation.OpenSidebarX,
					observation.OpenSidebarY,
				)
				if clickErr != nil &&
					outcome.Dispatch == browserflow.DispatchNotPerformed {
					_ = lease.MarkIncomplete(context.Background())
					return deleteFailure(
						runID, config, webagent.StageAttached,
						target, pending, notPerformed, conversation,
						"chatgpt_sidebar_open_not_performed", "provider",
						"ChatGPT sidebar expansion was not performed",
						"", data, cleanupCommands(runID, pending),
					)
				}
			}
			identityAttempts, err := pollUntil(
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
					return strictDeleteIdentity(observation) &&
						observation.PageButtonReady, nil
				},
			)
			data.PreparationAttempts += identityAttempts
			if err != nil {
				recordDeleteObservation(&data, observation)
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"chatgpt_delete_exact_row_not_ready", "provider",
					"ChatGPT did not expose one exact history row and options control",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if observation.DeleteMenuCount != 1 ||
				!observation.DeleteMenuReady {
				outcome, clickErr := browserflow.ClickPoint(
					ctx,
					session,
					observation.PageButtonX,
					observation.PageButtonY,
				)
				if clickErr != nil &&
					outcome.Dispatch == browserflow.DispatchNotPerformed {
					_ = lease.MarkIncomplete(context.Background())
					return deleteFailure(
						runID, config, webagent.StageAttached,
						target, pending, notPerformed, conversation,
						"chatgpt_delete_menu_open_not_performed", "provider",
						"ChatGPT exact conversation menu was not opened",
						"", data, cleanupCommands(runID, pending),
					)
				}
			}
			menuAttempts, err := pollUntil(
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
					return strictDeleteIdentity(observation) &&
						observation.DeleteMenuCount == 1 &&
						observation.DeleteMenuReady, nil
				},
			)
			data.PreparationAttempts += menuAttempts
			if err != nil {
				recordDeleteObservation(&data, observation)
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"chatgpt_delete_menuitem_not_ready", "provider",
					"ChatGPT exact Delete menuitem did not become actionable",
					"", data, cleanupCommands(runID, pending),
				)
			}
			outcome, clickErr := browserflow.ClickPoint(
				ctx,
				session,
				observation.DeleteMenuX,
				observation.DeleteMenuY,
			)
			if clickErr != nil &&
				outcome.Dispatch == browserflow.DispatchNotPerformed {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_delete_dialog_open_not_performed", "provider",
					"ChatGPT exact Delete menuitem was not pressed",
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
					return strictDeleteIdentity(observation) &&
						observation.DialogCount == 1 &&
						observation.ConfirmationCount == 1 &&
						observation.ConfirmationReady, nil
				},
			)
			data.ActionabilityAttempts = actionabilityAttempts
			if err != nil {
				recordDeleteObservation(&data, observation)
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"chatgpt_delete_confirmation_not_ready", "provider",
					"ChatGPT exact Delete confirmation did not become actionable",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return deleteFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, conversation,
					"chatgpt_delete_prepare_state_failed", "internal",
					"ChatGPT prepared delete state could not be persisted",
					"", data, cleanupCommands(runID, pending),
				)
			}
			dispatcher := config.Confirm
			if dispatcher == nil {
				dispatcher = chatgptDeleteDispatcher{
					conversationID: conversationID,
				}
			}
			outcome, _ = lease.Dispatch(ctx, dispatcher)
			action := actionEvidence(lease.Record())
			_ = lease.ReleaseInput()
			if outcome.Dispatch == browserflow.DispatchNotPerformed ||
				(outcome.Dispatch == "" &&
					lease.Record().RawInputCount == 0) {
				_ = lease.MarkIncomplete(context.Background())
				return deleteFailure(
					runID, config, webagent.StagePrepared, target, pending,
					action, conversation,
					"chatgpt_delete_not_performed", "provider",
					"ChatGPT Delete was not performed; retrying is safe",
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
			if !postcondition {
				_ = lease.MarkIncomplete(context.Background())
				retryAt := retryAtFromDeleteRecord(
					lease.Record(),
					nowForDelete(config),
				)
				data.CompletionState = "deletion_ambiguous"
				return deleteFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, action, conversation,
					"chatgpt_delete_unconfirmed", "completion",
					"ChatGPT Delete was attempted but its same-target postcondition was not proved; do not repeat deletion",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			if err := lease.ConfirmPostcondition(
				ctx,
				deletePostconditionProof,
			); err != nil {
				retryAt := retryAtFromDeleteRecord(
					lease.Record(),
					nowForDelete(config),
				)
				return deleteFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, action, conversation,
					"chatgpt_delete_postcondition_state_failed", "internal",
					"ChatGPT delete postcondition could not be persisted; do not repeat deletion",
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
					"chatgpt_delete_terminal_state_failed", "internal",
					"ChatGPT terminal delete state could not be persisted",
					"", data, cleanupCommands(runID, pending),
				)
			}
			return deleteSuccess(
				runID, config, target, pending, action, conversation, data,
				[]string{
					"cdp workflow agent chatgpt conversations list --json",
				},
			)
		},
	)
}

func strictDeleteIdentity(observation deleteObservation) bool {
	return observation.RouteMatches &&
		observation.SidebarState == "open" &&
		observation.LinkCount == 1 &&
		(observation.RowButtonCount == 0 ||
			observation.RowButtonCount == 1 &&
				observation.RowButtonNameOK) &&
		observation.PageButtonCount == 1
}

func recordDeleteObservation(
	data *DeleteData,
	observation deleteObservation,
) {
	if data == nil {
		return
	}
	data.Metadata["observed_route_matches"] = observation.RouteMatches
	data.Metadata["observed_sidebar_state"] = observation.SidebarState
	data.Metadata["observed_open_sidebar_count"] =
		observation.OpenSidebarCount
	data.Metadata["observed_open_sidebar_ready"] =
		observation.OpenSidebarReady
	data.Metadata["observed_link_count"] = observation.LinkCount
	data.Metadata["observed_row_button_count"] =
		observation.RowButtonCount
	data.Metadata["observed_row_button_name_ok"] =
		observation.RowButtonNameOK
	data.Metadata["observed_page_button_count"] =
		observation.PageButtonCount
	data.Metadata["observed_page_button_ready"] =
		observation.PageButtonReady
	data.Metadata["observed_delete_menu_count"] =
		observation.DeleteMenuCount
	data.Metadata["observed_delete_menu_ready"] =
		observation.DeleteMenuReady
	data.Metadata["observed_dialog_count"] = observation.DialogCount
	data.Metadata["observed_confirmation_count"] =
		observation.ConfirmationCount
	data.Metadata["observed_confirmation_ready"] =
		observation.ConfirmationReady
}

func deleteObservationFailure(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case strings.Contains(err.Error(), "decode exact-target evaluation"):
		return "decode_failed"
	case strings.Contains(err.Error(), "exact-target evaluation failed"):
		return "evaluation_failed"
	case strings.Contains(err.Error(), "context"):
		return "context_unavailable"
	default:
		return "observation_failed"
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
		return err
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  const expectedPath = '/c/' + expected;
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const name = element => String(
	    element && (
	      element.getAttribute('aria-label') ||
	      element.innerText ||
	      element.textContent ||
	      ''
	    ) || ''
	  ).replace(/\s+/g, ' ').trim();
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
	  const unique = nodes => Array.from(new Set(nodes));
	  const links = unique(Array.from(document.querySelectorAll('a[href]')).filter(
	    link => link.getAttribute('href') === expectedPath
	  ));
	  const rowButtons = unique(links.flatMap(link =>
	    Array.from(link.querySelectorAll(
	      'button[data-conversation-options-trigger]'
	    ))
	  )).filter(button =>
	    button.getAttribute('data-conversation-options-trigger') === expected
	  );
	  const pageButtons = unique(Array.from(document.querySelectorAll('button')).filter(
	    button =>
	      button.getAttribute('aria-label') === 'Open conversation options' &&
	      button.id === 'conversation-options-' + expected
	  ));
	  const openSidebar = unique(Array.from(document.querySelectorAll('button')).filter(
	    button => visible(button) && name(button) === 'Open sidebar'
	  ));
	  const deleteMenu = unique(Array.from(document.querySelectorAll(
	    '[role="menuitem"]'
	  )).filter(item => visible(item) && name(item) === 'Delete'));
	  const dialogs = unique(Array.from(document.querySelectorAll(
	    '[role="dialog"]'
	  )).filter(visible));
	  const confirmations = unique(dialogs.flatMap(dialog =>
	    Array.from(dialog.querySelectorAll('button'))
	  ).filter(button => visible(button) && name(button) === 'Delete'));
	  const openAction = actionable(
	    openSidebar.length === 1 ? openSidebar[0] : null
	  );
	  const pageAction = actionable(
	    pageButtons.length === 1 ? pageButtons[0] : null
	  );
	  const menuAction = actionable(
	    deleteMenu.length === 1 ? deleteMenu[0] : null
	  );
	  const confirmationAction = actionable(
	    confirmations.length === 1 ? confirmations[0] : null
	  );
	  return {
	    route_matches: location.origin === 'https://chatgpt.com' &&
	      location.pathname === expectedPath,
	    sidebar_state:
	      document.querySelector('#stage-slideover-sidebar')?.dataset.state || '',
	    open_sidebar_count: openSidebar.length,
	    open_sidebar_ready: openAction.ready,
	    open_sidebar_x: openAction.x,
	    open_sidebar_y: openAction.y,
	    link_count: links.length,
	    row_button_count: rowButtons.length,
	    row_button_name_ok: rowButtons.length === 1 &&
	      name(rowButtons[0]).startsWith('Open conversation options for '),
	    page_button_count: pageButtons.length,
	    page_button_ready: pageAction.ready,
	    page_button_x: pageAction.x,
	    page_button_y: pageAction.y,
	    delete_menu_count: deleteMenu.length,
	    delete_menu_ready: menuAction.ready,
	    delete_menu_x: menuAction.x,
	    delete_menu_y: menuAction.y,
	    dialog_count: dialogs.length,
	    confirmation_count: confirmations.length,
	    confirmation_ready: confirmationAction.ready,
	    confirmation_x: confirmationAction.x,
	    confirmation_y: confirmationAction.y,
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
	    confirmed: location.origin === 'https://chatgpt.com' &&
	      location.pathname === '/' &&
	      !location.href.includes(expected),
	  };
	})()`, idJSON)
	return evaluateInto(ctx, session, expression, value)
}

func deleteSuccess(
	runID string,
	config DeleteConfig,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	data DeleteData,
	nextCommands []string,
) webagent.Result {
	result := operationSuccess(
		runID, config.BuildCommit,
		webagent.OperationConversationsDelete,
		webagent.StageObserveTerminal,
		"headed_browser", target, cleanup, data, nextCommands,
	)
	result.State = webagent.StateTerminal
	result.Action = action
	result.Conversation = conversation
	return result
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
	result := operationFailure(
		runID, config.BuildCommit,
		webagent.OperationConversationsDelete,
		stage, "headed_browser", target, cleanup,
		code, errClass, message, data, nextCommands,
	)
	result.Action = action
	result.Conversation = conversation
	if result.Error != nil {
		result.Error.RetrySafe = action == nil ||
			action.Dispatch == webagent.DispatchNotPerformed
		result.Error.RetryAt = retryAt
	}
	return result
}

func nowForDelete(config DeleteConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}

func retryAtFromDeleteRecord(
	record browserflow.Record,
	fallback time.Time,
) time.Time {
	if record.RetryAt != "" {
		if parsed, err := time.Parse(
			time.RFC3339Nano,
			record.RetryAt,
		); err == nil {
			return parsed.UTC()
		}
	}
	return fallback.UTC().Add(defaultAmbiguousCooldown)
}
