package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	DeleteSchemaVersion       = "claude-conversation-delete/v1"
	defaultDeleteTimeout      = 45 * time.Second
	defaultDeletePollInterval = 250 * time.Millisecond
	deletePostconditionProof  = "redirected_to_new_without_conversation_id"
)

type DeleteConfig struct {
	Client       cdp.CommandClient
	Engine       *browserflow.Engine
	Journal      browserflow.Journal
	Admission    *admission.Gate
	BuildCommit  string
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

type deletePoint struct {
	Ready bool    `json:"ready"`
	Count int     `json:"count"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

type deleteObservation struct {
	RouteMatches bool        `json:"route_matches"`
	Header       deletePoint `json:"header"`
	MenuDelete   deletePoint `json:"menu_delete"`
	Confirm      deletePoint `json:"confirm"`
}

type deleteDispatcher struct {
	conversationID string
}

func (d deleteDispatcher) Dispatch(
	ctx context.Context,
	session *cdp.PageSession,
) (browserflow.DispatchOutcome, error) {
	observation, err := observeDeleteControls(ctx, session, d.conversationID)
	if err != nil ||
		!observation.RouteMatches ||
		!observation.Confirm.Ready ||
		observation.Confirm.Count != 1 {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact Claude delete confirmation was not actionable")
	}
	return browserflow.ClickPoint(
		ctx,
		session,
		observation.Confirm.X,
		observation.Confirm.Y,
	)
}

func UnavailableDelete(
	buildCommit string,
	code string,
	errClass string,
	message string,
	nextCommands []string,
) webagent.Result {
	return deleteFailure(
		webagent.NewRunID(),
		buildCommit,
		webagent.StagePlanned,
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		notPerformedAction(),
		code,
		errClass,
		message,
		"",
		DeleteData{
			SchemaVersion:   DeleteSchemaVersion,
			CompletionState: "not_deleted",
			Metadata:        map[string]any{},
		},
		nil,
		nextCommands,
	)
}

func DeleteConversation(
	ctx context.Context,
	config DeleteConfig,
	conversationID string,
) (result webagent.Result) {
	conversationID = strings.TrimSpace(conversationID)
	runID := webagent.NewRunID()
	data := DeleteData{
		SchemaVersion:   DeleteSchemaVersion,
		CompletionState: "not_deleted",
		Metadata:        map[string]any{},
	}
	notPerformed := notPerformedAction()
	if !organizationPattern.MatchString(conversationID) {
		return deleteFailure(
			runID,
			config.BuildCommit,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed,
			"claude_invalid_conversation_id",
			"usage",
			"Claude conversation id contains unsupported characters",
			"",
			data,
			nil,
			nil,
		)
	}
	conversation := conversationRef(conversationID)
	if config.Client == nil ||
		config.Engine == nil ||
		config.Journal == nil ||
		config.Admission == nil {
		return deleteFailure(
			runID,
			config.BuildCommit,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed,
			"claude_delete_unavailable",
			"internal",
			"Claude delete transaction is not configured",
			"",
			data,
			conversation,
			[]string{"cdp workflow agent claude doctor --json"},
		)
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultDeleteTimeout
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultDeletePollInterval
	}

	admissionLease, err := config.Admission.Acquire(ctx, admission.Request{
		Provider:  string(webagent.ProviderClaude),
		Operation: string(webagent.OperationConversationsDelete),
		RunID:     runID,
	})
	if err != nil {
		code := "claude_admission_unavailable"
		errClass := "internal"
		message := "Claude provider admission state is unavailable"
		retryAt := ""
		nextCommands := []string{}
		var blocked *admission.BlockedError
		if errors.As(err, &blocked) {
			code = "claude_admission_blocked"
			errClass = "admission"
			message = blocked.Error()
			if blocked.ResolutionNeeded {
				nextCommands = []string{"cdp workflow agent admission status claude --json"}
			} else {
				retryAt = blocked.RetryAt.UTC().Format(time.RFC3339Nano)
			}
		}
		return deleteFailure(
			runID,
			config.BuildCommit,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed,
			code,
			errClass,
			message,
			retryAt,
			data,
			conversation,
			nextCommands,
		)
	}
	var releaseCooldown time.Time
	defer func() {
		outcome := admission.OutcomeFailed
		if result.OK && result.State == webagent.StateTerminal {
			outcome = admission.OutcomeTerminal
		} else if result.Action != nil &&
			(result.Action.Dispatch == webagent.DispatchUnknown ||
				(result.Action.Dispatch == webagent.DispatchPerformed &&
					data.Postcondition == "")) {
			outcome = admission.OutcomeUnknown
		}
		if err := admissionLease.Release(admission.Release{
			Outcome:       outcome,
			CooldownUntil: releaseCooldown,
		}); err != nil {
			result = replaceDeleteFailure(
				result,
				"claude_admission_release_failed",
				"internal",
				"Claude provider admission outcome could not be persisted",
				"",
			)
		}
	}()

	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID:      runID,
		Provider:   string(webagent.ProviderClaude),
		Operation:  string(webagent.OperationConversationsDelete),
		InitialURL: "about:blank",
	})
	if err != nil {
		target, cleanup, stage := reconcileAcquireFailure(AuthRefreshConfig{
			Engine:  config.Engine,
			Journal: config.Journal,
		}, runID)
		code, errClass, message := classifyAcquireFailure(err)
		if cleanup.State == webagent.CleanupFailed || cleanup.State == webagent.CleanupPending {
			code = "claude_exact_target_cleanup_failed"
			errClass = "cleanup"
			message = "Claude delete could not prove exact target cleanup"
		}
		return deleteFailure(
			runID,
			config.BuildCommit,
			stage,
			target,
			cleanup,
			notPerformed,
			code,
			errClass,
			message,
			"",
			data,
			conversation,
			authRefreshNextCommands(runID, cleanup),
		)
	}

	target := &webagent.TargetEvidence{
		TargetID:  lease.TargetID(),
		SessionID: lease.Session().SessionID,
		Owned:     true,
		Created:   true,
	}
	pendingCleanup := webagent.CleanupEvidence{
		Required:        true,
		State:           webagent.CleanupPending,
		TargetID:        lease.TargetID(),
		RecoveryCommand: fmt.Sprintf("cdp workflow agent recovery close %s --json", runID),
	}
	defer func() {
		cleanup, closeErr := lease.Close(context.Background())
		if closeErr != nil ||
			cleanup.State != browserflow.CleanupClosed ||
			!cleanup.TargetGone {
			target.Closed = false
			result.Evidence.Target = target
			result.Cleanup = webagent.CleanupEvidence{
				Required: true,
				State:    webagent.CleanupFailed,
				TargetID: lease.TargetID(),
				RecoveryCommand: fmt.Sprintf(
					"cdp workflow agent recovery close %s --json",
					runID,
				),
			}
			result.Stage = webagent.StageCleanupPending
			result = replaceDeleteFailure(
				result,
				"claude_exact_target_cleanup_failed",
				"cleanup",
				"Claude delete could not prove exact target cleanup",
				"",
			)
			return
		}
		target.Closed = true
		result.Evidence.Target = target
		result.Cleanup = webagent.CleanupEvidence{
			Required:     true,
			State:        webagent.CleanupClosed,
			TargetID:     lease.TargetID(),
			TargetClosed: true,
			CloseProof:   "exact_target_absent_after_close",
		}
		result.Stage = webagent.StageClosed
	}()

	session := lease.Session()
	if err := enableClaudeURL(
		ctx,
		config.Client,
		session,
		Origin+"/chat/"+conversationID,
	); err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return deleteFailure(
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			notPerformed,
			"claude_delete_page_unavailable",
			"connection",
			"Claude exact conversation page could not be loaded",
			"",
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
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
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			notPerformed,
			"claude_delete_prepare_failed",
			"provider",
			"Claude exact delete confirmation did not become uniquely actionable",
			"",
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		return deleteFailure(
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			notPerformed,
			"claude_delete_prepare_state_failed",
			"internal",
			"Claude prepared delete state could not be persisted",
			"",
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}

	dispatcher := config.Confirm
	if dispatcher == nil {
		dispatcher = deleteDispatcher{conversationID: conversationID}
	}
	outcome, dispatchErr := lease.Dispatch(ctx, dispatcher)
	action := actionEvidence(lease.Record())
	if outcome.Dispatch == browserflow.DispatchNotPerformed ||
		(outcome.Dispatch == "" && lease.Record().RawInputCount == 0) {
		_ = lease.MarkIncomplete(context.Background())
		return deleteFailure(
			runID,
			config.BuildCommit,
			webagent.StagePrepared,
			target,
			pendingCleanup,
			action,
			"claude_delete_not_performed",
			"provider",
			"Claude delete confirmation was not performed; retrying is safe",
			"",
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if dispatchErr != nil || outcome.Dispatch == browserflow.DispatchUnknown {
		releaseCooldown = retryAtFromRecord(lease.Record(), nowForDelete(config))
	}

	if waitForDeletePostcondition(
		ctx,
		session,
		conversationID,
		deadline,
		config.PollInterval,
	) {
		if err := lease.ConfirmPostcondition(ctx, deletePostconditionProof); err != nil {
			releaseCooldown = retryAtFromRecord(lease.Record(), nowForDelete(config))
			return deleteFailure(
				runID,
				config.BuildCommit,
				webagent.StageActionDispatched,
				target,
				pendingCleanup,
				action,
				"claude_delete_postcondition_state_failed",
				"internal",
				"Claude delete postcondition could not be persisted; do not repeat deletion",
				formatRetryAt(releaseCooldown),
				data,
				conversation,
				authRefreshNextCommands(runID, pendingCleanup),
			)
		}
		action = actionEvidence(lease.Record())
		data.CompletionState = "deleted"
		data.Postcondition = deletePostconditionProof
		data.Metadata["same_target"] = true
		if err := lease.MarkTerminal(ctx); err != nil {
			return deleteFailure(
				runID,
				config.BuildCommit,
				webagent.StageObserveTerminal,
				target,
				pendingCleanup,
				action,
				"claude_delete_terminal_state_failed",
				"internal",
				"Claude terminal delete state could not be persisted",
				"",
				data,
				conversation,
				authRefreshNextCommands(runID, pendingCleanup),
			)
		}
		result = deleteSuccess(
			runID,
			config.BuildCommit,
			target,
			pendingCleanup,
			action,
			data,
			conversation,
		)
		return result
	}

	_ = lease.MarkIncomplete(context.Background())
	if releaseCooldown.IsZero() {
		releaseCooldown = nowForDelete(config).Add(defaultAmbiguousCooldown)
	}
	data.CompletionState = "deletion_ambiguous"
	return deleteFailure(
		runID,
		config.BuildCommit,
		webagent.StageActionDispatched,
		target,
		pendingCleanup,
		action,
		"claude_delete_unconfirmed",
		"completion",
		"Claude delete was attempted but its same-target postcondition was not proved; do not repeat deletion",
		formatRetryAt(releaseCooldown),
		data,
		conversation,
		authRefreshNextCommands(runID, pendingCleanup),
	)
}

func enableClaudeURL(
	ctx context.Context,
	client cdp.CommandClient,
	session *cdp.PageSession,
	rawURL string,
) error {
	if err := client.CallSession(
		ctx,
		session.SessionID,
		"Runtime.enable",
		map[string]any{},
		nil,
	); err != nil {
		return err
	}
	if err := client.CallSession(
		ctx,
		session.SessionID,
		"Page.enable",
		map[string]any{},
		nil,
	); err != nil {
		return err
	}
	if err := cdp.ActivateTargetWithClient(ctx, client, session.TargetID); err != nil {
		return err
	}
	_, err := session.Navigate(ctx, rawURL)
	return err
}

func prepareDeleteDialog(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	deadline time.Time,
	poll time.Duration,
) (bool, int, error) {
	attempts := 0
	var lastErr error
	for {
		attempts++
		observation, err := observeDeleteControls(ctx, session, conversationID)
		if err != nil {
			lastErr = err
		} else if !observation.RouteMatches {
			lastErr = fmt.Errorf("exact Claude conversation route was not observed")
		} else {
			switch {
			case observation.Confirm.Count > 1 ||
				observation.MenuDelete.Count > 1 ||
				observation.Header.Count > 1:
				return false, attempts, fmt.Errorf("Claude delete control was ambiguous")
			case observation.Confirm.Ready && observation.Confirm.Count == 1:
				return true, attempts, nil
			case observation.MenuDelete.Ready && observation.MenuDelete.Count == 1:
				_, lastErr = browserflow.ClickPoint(
					ctx,
					session,
					observation.MenuDelete.X,
					observation.MenuDelete.Y,
				)
			case observation.Header.Ready && observation.Header.Count == 1:
				_, lastErr = browserflow.ClickPoint(
					ctx,
					session,
					observation.Header.X,
					observation.Header.Y,
				)
			default:
				lastErr = fmt.Errorf("Claude delete controls are not actionable")
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if lastErr == nil {
				lastErr = fmt.Errorf("Claude delete preparation deadline exhausted")
			}
			return false, attempts, lastErr
		}
		delay := poll
		if delay > remaining {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, attempts, ctx.Err()
		case <-timer.C:
		}
	}
}

func observeDeleteControls(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
) (deleteObservation, error) {
	idJSON, err := json.Marshal(conversationID)
	if err != nil {
		return deleteObservation{}, fmt.Errorf("encode conversation id")
	}
	expression := fmt.Sprintf(`(() => {
	  const conversationId = %s;
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
	    const receives = Boolean(top && (top === element || element.contains(top)));
	    return {ready: enabled && receives, count: 1, x, y};
	  };
	  const headers = document.querySelectorAll(
	    '[data-testid="chat-title-split"] button[aria-label^="More options for "]'
	  );
	  const menuDeletes = document.querySelectorAll('[data-testid="delete-chat-trigger"]');
	  const dialogs = Array.from(document.querySelectorAll('[role="alertdialog"]')).filter(visible);
	  const confirmButtons = dialogs.flatMap(dialog =>
	    Array.from(dialog.querySelectorAll('button')).filter(button => {
	      const name = (button.getAttribute('aria-label') || button.innerText || button.textContent || '')
	        .replace(/\s+/g, ' ').trim();
	      return name === 'Delete';
	    })
	  );
	  return {
	    route_matches: location.origin === 'https://claude.ai' &&
	      location.pathname === '/chat/' + conversationId,
	    header: inspect(headers),
	    menu_delete: inspect(menuDeletes),
	    confirm: inspect(confirmButtons)
	  };
	})()`, idJSON)
	var observation deleteObservation
	if err := evaluateInto(ctx, session, expression, &observation); err != nil {
		return deleteObservation{}, err
	}
	return observation, nil
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
	  deleted: location.origin === 'https://claude.ai' &&
	    location.pathname === '/new' &&
	    !location.href.includes(%s)
	}))()`, idJSON)
	for {
		var observation struct {
			Deleted bool `json:"deleted"`
		}
		if err := evaluateInto(ctx, session, expression, &observation); err == nil &&
			observation.Deleted {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		delay := poll
		if delay > remaining {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func notPerformedAction() *webagent.ActionEvidence {
	return &webagent.ActionEvidence{
		Dispatch:      webagent.DispatchNotPerformed,
		AttemptCount:  0,
		RawInputCount: 0,
		RetrySafe:     true,
	}
}

func deleteSuccess(
	runID string,
	buildCommit string,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	data DeleteData,
	conversation *webagent.ConversationRef,
) webagent.Result {
	return webagent.Result{
		OK:            true,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationConversationsDelete,
		State:         webagent.StateTerminal,
		Stage:         webagent.StageObserveTerminal,
		Action:        action,
		Conversation:  conversation,
		Data:          data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "headed",
			ReadMode:    "same_target_rendered_postcondition",
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: []string{},
	}
}

func deleteFailure(
	runID string,
	buildCommit string,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	code string,
	errClass string,
	message string,
	retryAt string,
	data DeleteData,
	conversation *webagent.ConversationRef,
	nextCommands []string,
) webagent.Result {
	if nextCommands == nil {
		nextCommands = []string{}
	}
	retrySafe := true
	if action != nil {
		retrySafe = action.RetrySafe
	}
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationConversationsDelete,
		State:         webagent.StateFailed,
		Stage:         stage,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  errClass,
			Message:   message,
			RetrySafe: retrySafe,
			RetryAt:   retryAt,
		},
		Action:       action,
		Conversation: conversation,
		Data:         data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "headed",
			ReadMode:    "same_target_rendered_postcondition",
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: webagent.CloneCommands(nextCommands),
	}
}

func replaceDeleteFailure(
	result webagent.Result,
	code string,
	errClass string,
	message string,
	retryAt string,
) webagent.Result {
	result.OK = false
	result.State = webagent.StateFailed
	retrySafe := true
	if result.Action != nil {
		retrySafe = result.Action.RetrySafe
	}
	result.Error = &webagent.OperationError{
		Code:      code,
		ErrClass:  errClass,
		Message:   message,
		RetrySafe: retrySafe,
		RetryAt:   retryAt,
	}
	if result.Cleanup.State == webagent.CleanupFailed {
		result.NextCommands = authRefreshNextCommands(result.Evidence.RunID, result.Cleanup)
	}
	return result
}

func nowForDelete(config DeleteConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}
