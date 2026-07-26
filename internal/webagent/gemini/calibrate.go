package gemini

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	CalibrationSchemaVersion  = "gemini-calibration/v1"
	defaultCalibrationTimeout = 3 * time.Minute
	calibrationPrompt         = "In one concise sentence, explain why an at-most-once browser action should persist a pending marker before raw input."
)

type CalibrationConfig struct {
	BrowserConfig
	AuthStore       *Store
	Store           *CalibrationStore
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
	CurrentMode           string                   `json:"current_mode,omitempty"`
	DetailReadAttempts    int                      `json:"detail_read_attempts"`
	PreparationAttempts   int                      `json:"preparation_attempts"`
	ActionabilityAttempts int                      `json:"actionability_attempts"`
	SendAction            *webagent.ActionEvidence `json:"send_action,omitempty"`
	DeleteAction          *webagent.ActionEvidence `json:"delete_action,omitempty"`
	Postcondition         string                   `json:"postcondition,omitempty"`
	Metadata              map[string]any           `json:"metadata"`
}

func Calibrate(
	ctx context.Context,
	config CalibrationConfig,
) (result webagent.Result) {
	runID := webagent.NewRunID()
	data := CalibrationData{
		SchemaVersion:     CalibrationSchemaVersion,
		CompletionState:   "planned",
		PromptFingerprint: fingerprintPrompt(calibrationPrompt),
		ReadMode:          "headed_browser",
		Metadata: map[string]any{
			"conversation_mode": "fresh_disposable",
		},
	}
	notPerformed := notPerformedAction()
	if config.AuthStore == nil || config.Store == nil {
		return calibrationFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"gemini_calibration_unavailable", "internal",
			"Gemini calibration transaction is not configured",
			"", data,
			[]string{"cdp workflow agent gemini doctor --json"},
		)
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultCalibrationTimeout
	}
	if config.ComposerTimeout <= 0 {
		config.ComposerTimeout = defaultComposerTimeout
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	now := nowForCalibration(config)
	auth := config.AuthStore.AuthStatus(ctx, now, DefaultAuthTTL)
	if !auth.Ready {
		return calibrationFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"gemini_auth_"+auth.State, "auth",
			"Gemini auth evidence is not ready before calibration",
			"", data,
			[]string{"cdp workflow agent gemini auth refresh --json"},
		)
	}
	runtime := config.AuthStore.RuntimeStatus(
		ctx,
		now,
		DefaultCapabilitiesTTL,
	)
	if !runtime.Ready || strings.TrimSpace(runtime.CurrentMode) == "" {
		return calibrationFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"gemini_runtime_capabilities_"+runtime.State, "capability",
			"Gemini runtime capability evidence is not ready before calibration",
			"", data,
			[]string{"cdp workflow agent gemini capabilities refresh --json"},
		)
	}
	data.CurrentMode = strings.TrimSpace(runtime.CurrentMode)
	record := CalibrationStateRecord{
		SchemaVersion:     CalibrationStateSchemaVersion,
		RunID:             runID,
		State:             "planned",
		PromptFingerprint: data.PromptFingerprint,
		UpdatedAt:         now.Format(time.RFC3339Nano),
	}
	if err := config.Store.Save(ctx, record); err != nil {
		return calibrationFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"gemini_calibration_state_unavailable", "internal",
			"Gemini calibration state could not be persisted",
			"", data,
			[]string{"cdp workflow agent gemini calibration status --json"},
		)
	}
	overallDeadline := time.Now().Add(config.Timeout)
	result = runOwnedAction(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationCalibrate,
		"send",
		"about:blank",
		"headed_browser",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			record.State = "target_owned"
			record.TargetID = lease.TargetID()
			record.UpdatedAt = nowForCalibration(config).Format(time.RFC3339Nano)
			if err := config.Store.Save(ctx, record); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageTargetOwned, target, pending,
					notPerformed, nil,
					"gemini_calibration_state_unavailable", "internal",
					"Gemini calibration target ownership could not be persisted",
					"", data, cleanupCommands(runID, pending),
				)
			}
			session := lease.Session()
			if err := preparePage(ctx, config.Client, session, HomeURL); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"gemini_calibration_composer_unavailable", "connection",
					"Gemini calibration composer could not be loaded",
					"", data, cleanupCommands(runID, pending),
				)
			}
			var composer composerObservation
			composerAttempts, err := pollUntil(
				ctx,
				minDuration(
					config.ComposerTimeout,
					time.Until(overallDeadline),
				),
				config.PollInterval,
				func() (bool, error) {
					if err := observeComposer(ctx, session, "", &composer); err != nil {
						return false, err
					}
					return composer.RouteReady &&
						composer.EditorReady &&
						composer.EditorCount == 1 &&
						composer.PickerCount == 1 &&
						composer.AnswerCount == 0 &&
						composer.CurrentMode == data.CurrentMode, nil
				},
			)
			data.Metadata["composer_attempts"] = composerAttempts
			if err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"gemini_calibration_composer_not_ready", "provider",
					"Gemini calibration fresh composer and cached mode were not ready before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := prepareExactPrompt(
				ctx,
				session,
				calibrationPrompt,
			); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"gemini_calibration_prompt_prepare_failed", "provider",
					"Gemini calibration prompt was not preserved exactly",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := observeComposer(
				ctx,
				session,
				calibrationPrompt,
				&composer,
			); err != nil ||
				!composer.PromptMatches ||
				composer.CurrentMode != data.CurrentMode ||
				!composer.RouteReady ||
				composer.AnswerCount != 0 {
				return calibrationFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"gemini_calibration_prompt_verify_failed", "provider",
					"Gemini calibration exact prompt, blank route, or cached mode changed before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"gemini_calibration_send_state_failed", "internal",
					"Gemini calibration Send state could not be persisted",
					"", data, cleanupCommands(runID, pending),
				)
			}
			sendDispatcher := config.Send
			if sendDispatcher == nil {
				sendDispatcher = browserflow.DispatchFunc(browserflow.PressEnter)
			}
			sendOutcome, _ := lease.Dispatch(ctx, sendDispatcher)
			data.SendAction = actionEvidence(lease.Record())
			if sendOutcome.Dispatch == browserflow.DispatchNotPerformed ||
				(sendOutcome.Dispatch == "" && lease.Record().RawInputCount == 0) {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "send_not_performed"
				return calibrationFailure(
					runID, config, webagent.StagePrepared, target, pending,
					data.SendAction, nil,
					"gemini_calibration_send_not_performed", "provider",
					"Gemini calibration Send was not performed; retrying calibration is safe",
					"", data, cleanupCommands(runID, pending),
				)
			}

			var observation detailObservation
			ackAttempts := 0
			for {
				ackAttempts++
				_ = observeAskState(ctx, session, &observation)
				if observation.RouteMatches &&
					conversationIDPattern.MatchString(observation.ConversationID) {
					break
				}
				remaining := time.Until(overallDeadline)
				if remaining <= 0 ||
					!waitRendered(ctx, config.PollInterval, remaining) {
					break
				}
			}
			data.Metadata["acknowledgement_attempts"] = ackAttempts
			if !observation.RouteMatches ||
				!conversationIDPattern.MatchString(observation.ConversationID) {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "submission_unacknowledged"
				retryAt := retryAtFromRecord(
					lease.Record(),
					nowForCalibration(config),
				)
				return calibrationFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, data.SendAction, nil,
					"gemini_calibration_submission_unacknowledged", "completion",
					"Gemini calibration Send was attempted but not acknowledged; do not rerun",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			conversationID := observation.ConversationID
			conversation := conversationRef(conversationID)
			if err := lease.Acknowledge(ctx, conversationID); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_acknowledgement_state_failed", "internal",
					"Gemini calibration acknowledgement could not be persisted; do not rerun",
					"", data, cleanupCommands(runID, pending),
				)
			}
			data.SendAction = actionEvidence(lease.Record())
			record.State = "acknowledged"
			record.ConversationID = conversationID
			record.SendDispatch = lease.Record().Dispatch
			record.UpdatedAt = nowForCalibration(config).Format(time.RFC3339Nano)
			if err := config.Store.Save(ctx, record); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAcknowledged,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_state_unavailable", "internal",
					"Gemini calibration acknowledgement state could not be persisted; do not rerun",
					"", data, cleanupCommands(runID, pending),
				)
			}

			detailAttempts := 0
			for {
				detailAttempts++
				_ = observeConversationDetail(
					ctx,
					session,
					conversationID,
					&observation,
				)
				if observation.RouteMatches &&
					observation.ConversationID == conversationID &&
					observation.AnswerCount > 0 &&
					strings.TrimSpace(observation.Text) != "" &&
					!observation.Streaming {
					break
				}
				remaining := time.Until(overallDeadline)
				if remaining <= 0 ||
					!waitRendered(ctx, config.PollInterval, remaining) {
					break
				}
			}
			data.DetailReadAttempts = detailAttempts
			var promptCapture promptCaptureObservation
			captureErr := captureExactRenderedPrompt(
				ctx,
				session,
				&promptCapture,
			)
			data.Metadata["prompt_query_count"] = promptCapture.QueryCount
			data.Metadata["prompt_copy_button_count"] = promptCapture.CopyButtonCount
			data.Metadata["prompt_clipboard_intercepted"] = promptCapture.ClipboardIntercepted
			capturedFingerprint := exactCapturedPromptFingerprint(&promptCapture)
			if capturedFingerprint != "" {
				data.Metadata["prompt_capture_source"] = "intercepted_copy_prompt"
			}
			terminal := observation.RouteMatches &&
				observation.ConversationID == conversationID &&
				observation.AnswerCount > 0 &&
				strings.TrimSpace(observation.Text) != "" &&
				!observation.Streaming &&
				captureErr == nil &&
				capturedFingerprint != "" &&
				capturedFingerprint == data.PromptFingerprint
			if !terminal {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "answer_incomplete"
				record.State = "answer_incomplete"
				return calibrationFailure(
					runID, config, webagent.StageAcknowledged,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_answer_incomplete", "completion",
					"Gemini calibration answer or prompt identity was incomplete; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration cleanup --json",
					},
				)
			}
			data.AnswerCaptured = true
			data.AnswerCharacters = utf8.RuneCountInString(
				strings.TrimSpace(observation.Text),
			)
			data.CompletionState = "answer_captured"
			if err := lease.MarkTerminal(ctx); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_answer_state_failed", "internal",
					"Gemini calibration answer state could not be persisted; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration cleanup --json",
					},
				)
			}
			record.State = "answer_captured"
			record.UpdatedAt = nowForCalibration(config).Format(time.RFC3339Nano)
			if err := config.Store.Save(ctx, record); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_state_unavailable", "internal",
					"Gemini calibration answer state could not be persisted; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration cleanup --json",
					},
				)
			}
			if err := lease.BeginNextAction(ctx, "delete"); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_delete_state_failed", "internal",
					"Gemini calibration could not persist the delete action slot; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration cleanup --json",
					},
				)
			}
			record.State = "delete_pending"
			record.UpdatedAt = nowForCalibration(config).Format(time.RFC3339Nano)
			if err := config.Store.Save(ctx, record); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageActionPending,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_state_unavailable", "internal",
					"Gemini calibration delete-pending state could not be persisted; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration cleanup --json",
					},
				)
			}
			prepared, attempts, err := prepareDeleteDialog(
				ctx,
				session,
				conversationID,
				overallDeadline,
				config.PollInterval,
			)
			data.PreparationAttempts = attempts
			data.ActionabilityAttempts = attempts
			if err != nil || !prepared {
				data.CompletionState = "delete_not_performed"
				record.State = "delete_not_performed"
				return calibrationFailure(
					runID, config, webagent.StageAttached,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_delete_prepare_failed", "provider",
					"Gemini calibration exact delete confirmation was not actionable; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration cleanup --json",
					},
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAttached,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_delete_prepare_state_failed", "internal",
					"Gemini calibration prepared delete state could not be persisted; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration cleanup --json",
					},
				)
			}
			confirmDispatcher := config.Confirm
			if confirmDispatcher == nil {
				confirmDispatcher = deleteDispatcher{
					conversationID: conversationID,
				}
			}
			deleteOutcome, _ := lease.Dispatch(ctx, confirmDispatcher)
			data.DeleteAction = actionEvidence(lease.Record())
			record.DeleteDispatch = lease.Record().Dispatch
			if deleteOutcome.Dispatch == browserflow.DispatchNotPerformed ||
				(deleteOutcome.Dispatch == "" &&
					lease.Record().RawInputCount == 0) {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "delete_not_performed"
				record.State = "delete_not_performed"
				return calibrationFailure(
					runID, config, webagent.StagePrepared,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_delete_not_performed", "provider",
					"Gemini calibration delete was not performed; use cleanup and do not rerun calibration",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration cleanup --json",
					},
				)
			}
			if !waitForDeletePostcondition(
				ctx,
				session,
				conversationID,
				overallDeadline,
				config.PollInterval,
			) {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "deletion_ambiguous"
				record.State = "deletion_ambiguous"
				retryAt := retryAtFromRecord(
					lease.Record(),
					nowForCalibration(config),
				)
				return calibrationFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_delete_unconfirmed", "completion",
					"Gemini calibration delete was attempted without a same-target postcondition; do not repeat it",
					retryAt.Format(time.RFC3339Nano), data,
					[]string{
						"cdp workflow agent gemini calibration status --json",
					},
				)
			}
			if err := lease.ConfirmPostcondition(
				ctx,
				deletePostconditionProof,
			); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_delete_postcondition_state_failed", "internal",
					"Gemini calibration delete postcondition could not be persisted; do not repeat it",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration status --json",
					},
				)
			}
			data.DeleteAction = actionEvidence(lease.Record())
			data.Postcondition = deletePostconditionProof
			data.CompletionState = "deleted"
			if err := lease.MarkTerminal(ctx); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_delete_terminal_state_failed", "internal",
					"Gemini calibration delete terminal state could not be persisted",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration status --json",
					},
				)
			}
			record.State = "deleted"
			record.Postcondition = deletePostconditionProof
			record.UpdatedAt = nowForCalibration(config).Format(time.RFC3339Nano)
			if err := config.Store.Save(ctx, record); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending, data.SendAction, conversation,
					"gemini_calibration_state_unavailable", "internal",
					"Gemini calibration delete outcome could not be persisted",
					"", data,
					[]string{
						"cdp workflow agent gemini calibration status --json",
					},
				)
			}
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationCalibrate,
				webagent.StateTerminal, webagent.StageObserveTerminal,
				"headed_browser", target, pending, data.DeleteAction,
				conversation, data,
				[]string{
					"cdp workflow agent gemini calibration status --json",
				},
			)
		},
	)

	record.State = calibrationRecordState(result, data)
	record.SendDispatch = browserflow.Dispatch(actionDispatch(data.SendAction))
	record.DeleteDispatch = browserflow.Dispatch(actionDispatch(data.DeleteAction))
	record.Postcondition = data.Postcondition
	if result.Conversation != nil {
		record.ConversationID = result.Conversation.ID
	}
	if result.Evidence.Target != nil {
		record.TargetID = result.Evidence.Target.TargetID
	}
	record.TargetClosed = result.Cleanup.State == webagent.CleanupNotRequired ||
		(result.Cleanup.State == webagent.CleanupClosed &&
			result.Cleanup.TargetClosed)
	record.UpdatedAt = nowForCalibration(config).Format(time.RFC3339Nano)
	if err := config.Store.Save(context.Background(), record); err != nil {
		result = replaceFailure(
			result,
			"gemini_calibration_state_unavailable",
			"internal",
			"Gemini calibration outcome could not be persisted",
			[]string{
				"cdp workflow agent gemini calibration status --json",
			},
		)
	}
	return result
}

func calibrationFailure(
	runID string,
	config CalibrationConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	code string,
	errClass string,
	message string,
	retryAt string,
	data CalibrationData,
	nextCommands []string,
) webagent.Result {
	if data.SendAction != nil &&
		(data.SendAction.Dispatch == webagent.DispatchPerformed ||
			data.SendAction.Dispatch == webagent.DispatchUnknown) {
		action = data.SendAction
	}
	return operationFailure(
		runID, config.BuildCommit, webagent.OperationCalibrate,
		stage, "headed_browser", target, cleanup, action, conversation,
		code, errClass, message, retryAt, data, nextCommands,
	)
}

func calibrationRecordState(
	result webagent.Result,
	data CalibrationData,
) string {
	switch data.CompletionState {
	case "deleted", "send_not_performed", "submission_unacknowledged",
		"answer_incomplete", "delete_not_performed", "deletion_ambiguous":
		return data.CompletionState
	case "answer_captured":
		return "answer_captured"
	}
	if result.OK {
		return "deleted"
	}
	return "failed"
}

func nowForCalibration(config CalibrationConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}

func minDuration(left time.Duration, right time.Duration) time.Duration {
	if left <= 0 {
		return right
	}
	if right <= 0 || left < right {
		return left
	}
	return right
}
