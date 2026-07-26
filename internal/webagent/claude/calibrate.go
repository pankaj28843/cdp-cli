package claude

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	CalibrationSchemaVersion  = "claude-calibration/v1"
	defaultCalibrationTimeout = 3 * time.Minute
	calibrationPrompt         = "In one concise sentence, explain why an at-most-once browser action should persist a pending marker before raw input."
)

type CalibrationConfig struct {
	Client          cdp.CommandClient
	Engine          *browserflow.Engine
	Journal         browserflow.Journal
	Admission       *admission.Gate
	Store           *CalibrationStore
	BuildCommit     string
	Timeout         time.Duration
	ComposerTimeout time.Duration
	PollInterval    time.Duration
	Now             func() time.Time
	Send            browserflow.Dispatcher
	Confirm         browserflow.Dispatcher
}

type CalibrationData struct {
	SchemaVersion         string                   `json:"schema_version"`
	CompletionState       string                   `json:"completion_state"`
	PromptFingerprint     string                   `json:"prompt_fingerprint"`
	AnswerCaptured        bool                     `json:"answer_captured"`
	AnswerCharacters      int                      `json:"answer_characters"`
	ReadMode              string                   `json:"read_mode"`
	ModelLabel            string                   `json:"model_label,omitempty"`
	DetailReadAttempts    int                      `json:"detail_read_attempts"`
	PreparationAttempts   int                      `json:"preparation_attempts"`
	ActionabilityAttempts int                      `json:"actionability_attempts"`
	SendAction            *webagent.ActionEvidence `json:"send_action,omitempty"`
	DeleteAction          *webagent.ActionEvidence `json:"delete_action,omitempty"`
	Postcondition         string                   `json:"postcondition,omitempty"`
	Metadata              map[string]any           `json:"metadata"`
}

func UnavailableCalibration(
	buildCommit string,
	code string,
	errClass string,
	message string,
	nextCommands []string,
) webagent.Result {
	return calibrationFailure(
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
		CalibrationData{
			SchemaVersion:     CalibrationSchemaVersion,
			CompletionState:   "not_started",
			PromptFingerprint: fingerprintPrompt(calibrationPrompt),
			ReadMode:          "not_started",
			Metadata:          map[string]any{},
		},
		nil,
		nextCommands,
	)
}

func Calibrate(ctx context.Context, config CalibrationConfig) (result webagent.Result) {
	runID := webagent.NewRunID()
	data := CalibrationData{
		SchemaVersion:     CalibrationSchemaVersion,
		CompletionState:   "not_started",
		PromptFingerprint: fingerprintPrompt(calibrationPrompt),
		ReadMode:          "headed_browser",
		Metadata:          map[string]any{"conversation_mode": "fresh_disposable"},
	}
	notPerformed := notPerformedAction()
	if config.Client == nil ||
		config.Engine == nil ||
		config.Journal == nil ||
		config.Admission == nil ||
		config.Store == nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed,
			"claude_calibration_unavailable",
			"internal",
			"Claude calibration transaction is not configured",
			"",
			data,
			nil,
			[]string{"cdp workflow agent claude doctor --json"},
		)
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultCalibrationTimeout
	}
	if config.ComposerTimeout <= 0 {
		config.ComposerTimeout = defaultComposerTimeout
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultAskPollInterval
	}
	overallDeadline := time.Now().Add(config.Timeout)
	calibrationState := CalibrationStateRecord{
		SchemaVersion:     CalibrationStateSchemaVersion,
		RunID:             runID,
		State:             "planned",
		PromptFingerprint: data.PromptFingerprint,
		UpdatedAt:         nowForCalibration(config).Format(time.RFC3339Nano),
	}
	if err := config.Store.Save(ctx, calibrationState); err != nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed,
			"claude_calibration_state_unavailable",
			"internal",
			"Claude calibration state could not be persisted",
			"",
			data,
			nil,
			[]string{"cdp workflow agent claude calibration status --json"},
		)
	}
	defer func() {
		calibrationState.State = calibrationStateName(result, data)
		calibrationState.SendDispatch = actionDispatch(data.SendAction)
		calibrationState.DeleteDispatch = actionDispatch(data.DeleteAction)
		calibrationState.Postcondition = data.Postcondition
		if result.Conversation != nil {
			calibrationState.ConversationID = result.Conversation.ID
		}
		if result.Evidence.Target != nil {
			calibrationState.TargetID = result.Evidence.Target.TargetID
		}
		calibrationState.TargetClosed = result.Cleanup.State == webagent.CleanupNotRequired ||
			(result.Cleanup.State == webagent.CleanupClosed &&
				result.Cleanup.TargetClosed)
		calibrationState.UpdatedAt = nowForCalibration(config).Format(time.RFC3339Nano)
		if err := config.Store.Save(context.Background(), calibrationState); err != nil {
			result = replaceCalibrationFailure(
				result,
				"claude_calibration_state_unavailable",
				"internal",
				"Claude calibration outcome could not be persisted",
				"",
			)
		}
	}()

	admissionLease, err := config.Admission.Acquire(ctx, admission.Request{
		Provider:  string(webagent.ProviderClaude),
		Operation: string(webagent.OperationCalibrate),
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
		return calibrationFailure(
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
			nil,
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
			result = replaceCalibrationFailure(
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
		Operation:  string(webagent.OperationCalibrate),
		ActionName: "send",
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
			message = "Claude calibration could not prove exact target cleanup"
		}
		return calibrationFailure(
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
			nil,
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
	calibrationState.State = "target_owned"
	calibrationState.TargetID = lease.TargetID()
	calibrationState.UpdatedAt = nowForCalibration(config).Format(time.RFC3339Nano)
	if err := config.Store.Save(ctx, calibrationState); err != nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageTargetOwned,
			target,
			pendingCleanup,
			notPerformed,
			"claude_calibration_state_unavailable",
			"internal",
			"Claude calibration target ownership could not be persisted",
			"",
			data,
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
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
			result = replaceCalibrationFailure(
				result,
				"claude_exact_target_cleanup_failed",
				"cleanup",
				"Claude calibration could not prove exact target cleanup",
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
	if err := enableClaudePage(ctx, config.Client, session); err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			notPerformed,
			"claude_calibration_composer_unavailable",
			"connection",
			"Claude calibration composer could not be loaded",
			"",
			data,
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	composer, err := waitForComposer(
		ctx,
		session,
		config.ComposerTimeout,
		config.PollInterval,
	)
	if err != nil || !composer.Ready || composer.QuotaLimited {
		_ = lease.MarkIncomplete(context.Background())
		if composer.QuotaLimited {
			releaseCooldown = nowForCalibration(config).Add(time.Hour)
		}
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			notPerformed,
			"claude_calibration_composer_not_ready",
			"provider",
			"Claude calibration composer was not ready before Send",
			formatRetryAt(releaseCooldown),
			data,
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	data.ModelLabel = composer.ModelLabel
	if err := prepareExactPrompt(ctx, session, calibrationPrompt); err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			notPerformed,
			"claude_calibration_prompt_prepare_failed",
			"provider",
			"Claude calibration prompt was not preserved exactly",
			"",
			data,
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			notPerformed,
			"claude_calibration_send_state_failed",
			"internal",
			"Claude calibration Send state could not be persisted",
			"",
			data,
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	sendDispatcher := config.Send
	if sendDispatcher == nil {
		sendDispatcher = browserflow.DispatchFunc(browserflow.PressEnter)
	}
	sendOutcome, sendErr := lease.Dispatch(ctx, sendDispatcher)
	sendAction := actionEvidence(lease.Record())
	data.SendAction = sendAction
	if sendOutcome.Dispatch == browserflow.DispatchNotPerformed ||
		(sendOutcome.Dispatch == "" && lease.Record().RawInputCount == 0) {
		_ = lease.MarkIncomplete(context.Background())
		data.CompletionState = "send_not_performed"
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StagePrepared,
			target,
			pendingCleanup,
			sendAction,
			"claude_calibration_send_not_performed",
			"provider",
			"Claude calibration Send was not performed; retrying calibration is safe",
			"",
			data,
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if sendErr != nil || sendOutcome.Dispatch == browserflow.DispatchUnknown {
		releaseCooldown = retryAtFromRecord(lease.Record(), nowForCalibration(config))
	}
	ack, ackErr := waitForAcknowledgement(
		ctx,
		session,
		overallDeadline,
		config.PollInterval,
	)
	if ackErr != nil || ack.ConversationID == "" {
		_ = lease.MarkIncomplete(context.Background())
		if releaseCooldown.IsZero() {
			releaseCooldown = nowForCalibration(config).Add(defaultAmbiguousCooldown)
		}
		data.CompletionState = "submission_unacknowledged"
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageActionDispatched,
			target,
			pendingCleanup,
			sendAction,
			"claude_calibration_submission_unacknowledged",
			"completion",
			"Claude calibration Send was attempted but not acknowledged; do not rerun",
			formatRetryAt(releaseCooldown),
			data,
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	conversation := conversationRef(ack.ConversationID)
	if err := lease.Acknowledge(ctx, ack.ConversationID); err != nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageActionDispatched,
			target,
			pendingCleanup,
			sendAction,
			"claude_calibration_acknowledgement_state_failed",
			"internal",
			"Claude calibration acknowledgement could not be persisted; do not rerun",
			formatRetryAt(releaseCooldown),
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	releaseCooldown = time.Time{}
	sendAction = actionEvidence(lease.Record())
	data.SendAction = sendAction
	calibrationState.State = "acknowledged"
	calibrationState.ConversationID = ack.ConversationID
	calibrationState.SendDispatch = lease.Record().Dispatch
	calibrationState.UpdatedAt = nowForCalibration(config).Format(time.RFC3339Nano)
	if err := config.Store.Save(ctx, calibrationState); err != nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageAcknowledged,
			target,
			pendingCleanup,
			sendAction,
			"claude_calibration_state_unavailable",
			"internal",
			"Claude calibration acknowledgement state could not be persisted",
			"",
			data,
			conversation,
			[]string{
				fmt.Sprintf(
					"cdp workflow agent claude conversations delete %s --json",
					ack.ConversationID,
				),
			},
		)
	}

	captureDeadline := overallDeadline.Add(-45 * time.Second)
	if captureDeadline.Before(time.Now().Add(config.PollInterval)) {
		captureDeadline = overallDeadline
	}
	observation, detailAttempts, observed := readRenderedDetail(
		ctx,
		session,
		ack.ConversationID,
		true,
		captureDeadline,
		config.PollInterval,
	)
	data.DetailReadAttempts = detailAttempts
	data.AnswerCaptured = observed &&
		!observation.Streaming &&
		observation.Text != ""
	data.AnswerCharacters = utf8.RuneCountInString(observation.Text)
	if data.AnswerCaptured {
		if err := lease.MarkTerminal(ctx); err != nil {
			return calibrationFailure(
				runID,
				config.BuildCommit,
				webagent.StageAcknowledged,
				target,
				pendingCleanup,
				sendAction,
				"claude_calibration_capture_state_failed",
				"internal",
				"Claude calibration capture state could not be persisted",
				"",
				data,
				conversation,
				authRefreshNextCommands(runID, pendingCleanup),
			)
		}
	} else {
		_ = lease.MarkIncomplete(context.Background())
	}

	if err := lease.BeginNextAction(ctx, "delete"); err != nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			target,
			pendingCleanup,
			sendAction,
			"claude_calibration_delete_transition_failed",
			"internal",
			"Claude calibration could not persist the transition to exact deletion",
			"",
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	calibrationState.State = "delete_pending"
	calibrationState.UpdatedAt = nowForCalibration(config).Format(time.RFC3339Nano)
	if err := config.Store.Save(ctx, calibrationState); err != nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StagePrepared,
			target,
			pendingCleanup,
			notPerformed,
			"claude_calibration_state_unavailable",
			"internal",
			"Claude calibration delete intent could not be persisted",
			"",
			data,
			conversation,
			[]string{
				fmt.Sprintf(
					"cdp workflow agent claude conversations delete %s --json",
					ack.ConversationID,
				),
			},
		)
	}
	prepared, attempts, err := prepareDeleteDialog(
		ctx,
		session,
		ack.ConversationID,
		overallDeadline,
		config.PollInterval,
	)
	data.PreparationAttempts = attempts
	data.ActionabilityAttempts = attempts
	if err != nil || !prepared {
		_ = lease.MarkIncomplete(context.Background())
		data.DeleteAction = notPerformedAction()
		data.CompletionState = "delete_not_performed"
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			data.DeleteAction,
			"claude_calibration_delete_prepare_failed",
			"provider",
			"Claude calibration exact deletion was not prepared",
			"",
			data,
			conversation,
			[]string{
				fmt.Sprintf(
					"cdp workflow agent claude conversations delete %s --json",
					ack.ConversationID,
				),
			},
		)
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			notPerformed,
			"claude_calibration_delete_state_failed",
			"internal",
			"Claude calibration delete state could not be persisted",
			"",
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	confirmDispatcher := config.Confirm
	if confirmDispatcher == nil {
		confirmDispatcher = deleteDispatcher{conversationID: ack.ConversationID}
	}
	deleteOutcome, deleteErr := lease.Dispatch(ctx, confirmDispatcher)
	deleteAction := actionEvidence(lease.Record())
	data.DeleteAction = deleteAction
	if deleteOutcome.Dispatch == browserflow.DispatchNotPerformed ||
		(deleteOutcome.Dispatch == "" && lease.Record().RawInputCount == 0) {
		_ = lease.MarkIncomplete(context.Background())
		data.CompletionState = "delete_not_performed"
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StagePrepared,
			target,
			pendingCleanup,
			deleteAction,
			"claude_calibration_delete_not_performed",
			"provider",
			"Claude calibration delete confirmation was not performed",
			"",
			data,
			conversation,
			[]string{
				fmt.Sprintf(
					"cdp workflow agent claude conversations delete %s --json",
					ack.ConversationID,
				),
			},
		)
	}
	if deleteErr != nil || deleteOutcome.Dispatch == browserflow.DispatchUnknown {
		releaseCooldown = retryAtFromRecord(lease.Record(), nowForCalibration(config))
	}
	if !waitForDeletePostcondition(
		ctx,
		session,
		ack.ConversationID,
		overallDeadline,
		config.PollInterval,
	) {
		_ = lease.MarkIncomplete(context.Background())
		if releaseCooldown.IsZero() {
			releaseCooldown = nowForCalibration(config).Add(defaultAmbiguousCooldown)
		}
		data.CompletionState = "deletion_ambiguous"
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageActionDispatched,
			target,
			pendingCleanup,
			deleteAction,
			"claude_calibration_delete_unconfirmed",
			"completion",
			"Claude calibration deletion was attempted but unconfirmed; do not repeat it",
			formatRetryAt(releaseCooldown),
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if err := lease.ConfirmPostcondition(ctx, deletePostconditionProof); err != nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageActionDispatched,
			target,
			pendingCleanup,
			deleteAction,
			"claude_calibration_delete_postcondition_state_failed",
			"internal",
			"Claude calibration delete postcondition could not be persisted; do not repeat it",
			formatRetryAt(releaseCooldown),
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	deleteAction = actionEvidence(lease.Record())
	data.DeleteAction = deleteAction
	data.Postcondition = deletePostconditionProof
	data.CompletionState = "deleted"
	if err := lease.MarkTerminal(ctx); err != nil {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			target,
			pendingCleanup,
			deleteAction,
			"claude_calibration_terminal_state_failed",
			"internal",
			"Claude calibration terminal state could not be persisted",
			"",
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if !data.AnswerCaptured {
		return calibrationFailure(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			target,
			pendingCleanup,
			deleteAction,
			"claude_calibration_answer_incomplete",
			"completion",
			"Claude calibration conversation was deleted but its answer was not terminal",
			"",
			data,
			conversation,
			nil,
		)
	}
	result = calibrationSuccess(
		runID,
		config.BuildCommit,
		target,
		pendingCleanup,
		deleteAction,
		data,
		conversation,
	)
	return result
}

func calibrationSuccess(
	runID string,
	buildCommit string,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	data CalibrationData,
	conversation *webagent.ConversationRef,
) webagent.Result {
	return webagent.Result{
		OK:            true,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationCalibrate,
		State:         webagent.StateTerminal,
		Stage:         webagent.StageObserveTerminal,
		Action:        action,
		Conversation:  conversation,
		Data:          data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "headed",
			ReadMode:    "same_target_rendered_calibration",
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: []string{},
	}
}

func calibrationFailure(
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
	data CalibrationData,
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
		Operation:     webagent.OperationCalibrate,
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
			ReadMode:    "same_target_rendered_calibration",
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: webagent.CloneCommands(nextCommands),
	}
}

func replaceCalibrationFailure(
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

func nowForCalibration(config CalibrationConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}

func actionDispatch(action *webagent.ActionEvidence) browserflow.Dispatch {
	if action == nil {
		return ""
	}
	switch action.Dispatch {
	case webagent.DispatchPerformed:
		return browserflow.DispatchPerformed
	case webagent.DispatchNotPerformed:
		return browserflow.DispatchNotPerformed
	case webagent.DispatchUnknown:
		return browserflow.DispatchUnknown
	default:
		return ""
	}
}

func calibrationStateName(result webagent.Result, data CalibrationData) string {
	switch data.CompletionState {
	case "deleted", "send_not_performed", "submission_unacknowledged",
		"delete_not_performed", "deletion_ambiguous":
		return data.CompletionState
	}
	if result.OK {
		return "deleted"
	}
	return "failed"
}
