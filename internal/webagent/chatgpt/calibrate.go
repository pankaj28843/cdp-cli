package chatgpt

import (
	"context"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	CalibrationSchemaVersion = "chatgpt-calibration/v1"
	calibrationPrompt        = "In one concise sentence, explain why an at-most-once browser action should persist a pending marker before raw input."
)

type CalibrationConfig struct {
	BrowserConfig
	AuthStore    *Store
	Store        *CalibrationStore
	Timeout      time.Duration
	PollInterval time.Duration
	Now          func() time.Time
	Send         browserflow.Dispatcher
	Confirm      browserflow.Dispatcher
}

type CalibrationData struct {
	SchemaVersion     string                   `json:"schema_version"`
	CompletionState   string                   `json:"completion_state"`
	Text              string                   `json:"text"`
	ReadMode          string                   `json:"read_mode"`
	PromptFingerprint string                   `json:"prompt_fingerprint"`
	SendAction        *webagent.ActionEvidence `json:"send_action,omitempty"`
	DeleteAction      *webagent.ActionEvidence `json:"delete_action,omitempty"`
	Postcondition     string                   `json:"postcondition,omitempty"`
	Metadata          map[string]any           `json:"metadata"`
}

type calibrationDeletePreparationError struct {
	code    string
	message string
}

func (e *calibrationDeletePreparationError) Error() string {
	return e.message
}

func Calibrate(
	ctx context.Context,
	config CalibrationConfig,
) (result webagent.Result) {
	runID := webagent.NewRunID()
	now := nowForCalibration(config)
	data := CalibrationData{
		SchemaVersion:     CalibrationSchemaVersion,
		CompletionState:   "not_submitted",
		ReadMode:          "not_started",
		PromptFingerprint: fingerprintPrompt(calibrationPrompt),
		Metadata:          map[string]any{},
	}
	if config.AuthStore == nil || config.Store == nil {
		return calibrationFailure(
			runID,
			config,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{
				State: webagent.CleanupNotRequired,
			},
			nil,
			nil,
			"chatgpt_calibration_unavailable",
			"internal",
			"ChatGPT calibration transaction is not configured",
			"",
			data,
		)
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultAskTimeout
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	record := CalibrationStateRecord{
		SchemaVersion:     CalibrationStateSchemaVersion,
		RunID:             runID,
		State:             "planned",
		PromptFingerprint: data.PromptFingerprint,
		UpdatedAt:         now.Format(time.RFC3339Nano),
	}
	if err := config.Store.Save(ctx, record); err != nil {
		return calibrationFailure(
			runID,
			config,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{
				State: webagent.CleanupNotRequired,
			},
			nil,
			nil,
			"chatgpt_calibration_state_unavailable",
			"internal",
			"ChatGPT calibration state could not be persisted",
			"",
			data,
		)
	}

	askConfig := AskConfig{
		BrowserConfig: config.BrowserConfig,
		Store:         config.AuthStore,
		Timeout:       config.Timeout,
		ComposerTimeout: minDuration(
			defaultComposerTimeout,
			config.Timeout,
		),
		PollInterval: config.PollInterval,
		Now:          config.Now,
		Send:         config.Send,
		operation:    webagent.OperationCalibrate,
		runID:        runID,
		holdInput:    true,
	}
	askConfig.completionHook = func(
		hookCtx context.Context,
		lease *browserflow.Lease,
		target *webagent.TargetEvidence,
		pending webagent.CleanupEvidence,
		askState webagent.State,
		sendAction *webagent.ActionEvidence,
		conversation *webagent.ConversationRef,
		askData AskData,
	) webagent.Result {
		return finishCalibrationOnLease(
			hookCtx,
			config,
			runID,
			lease,
			target,
			pending,
			askState,
			sendAction,
			conversation,
			askData,
			&record,
			&data,
		)
	}
	result = Ask(ctx, askConfig, calibrationPrompt)

	if result.Conversation != nil {
		record.ConversationID = result.Conversation.ID
	}
	if result.Evidence.Target != nil {
		record.TargetID = result.Evidence.Target.TargetID
	}
	record.TargetClosed =
		result.Cleanup.State == webagent.CleanupClosed ||
			result.Cleanup.State == webagent.CleanupNotRequired
	mergeChatGPTCalibrationRecovery(
		context.Background(),
		&record,
		config.Journal,
	)
	if record.Postcondition == deletePostconditionProof {
		record.State = "deleted"
	} else if result.OK {
		record.State = "answer_captured"
	} else if record.State == "planned" {
		switch record.SendDispatch {
		case browserflow.DispatchPerformed,
			browserflow.DispatchUnknown:
			if record.ConversationID == "" {
				record.State = "submission_unacknowledged"
			} else {
				record.State = "answer_incomplete"
			}
		case browserflow.DispatchNotPerformed:
			record.State = "send_not_performed"
		default:
			record.State = "failed"
		}
	}
	record.UpdatedAt = nowForCalibration(config).Format(
		time.RFC3339Nano,
	)
	if err := config.Store.Save(
		context.Background(),
		record,
	); err != nil {
		return replaceCalibrationResultFailure(
			result,
			"chatgpt_calibration_state_unavailable",
			"internal",
			"ChatGPT calibration outcome could not be persisted",
		)
	}
	return result
}

func finishCalibrationOnLease(
	ctx context.Context,
	config CalibrationConfig,
	runID string,
	lease *browserflow.Lease,
	target *webagent.TargetEvidence,
	pending webagent.CleanupEvidence,
	askState webagent.State,
	sendAction *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	askData AskData,
	record *CalibrationStateRecord,
	data *CalibrationData,
) webagent.Result {
	data.Text = askData.Text
	data.ReadMode = askData.ReadMode
	data.SendAction = sendAction
	data.CompletionState = "answer_captured"
	data.Metadata["ask_completion_state"] =
		askData.CompletionState
	data.Metadata["detail_read_attempts"] =
		askData.DetailReadAttempts
	if source, ok := askData.Metadata["answer_source"]; ok {
		data.Metadata["answer_source"] = source
	}
	if conversation == nil ||
		!conversationIDPattern.MatchString(conversation.ID) {
		data.CompletionState = "submission_unacknowledged"
		return calibrationFailure(
			runID,
			config,
			webagent.StageObserveTerminal,
			target,
			pending,
			sendAction,
			conversation,
			"chatgpt_calibration_identity_unavailable",
			"completion",
			"ChatGPT calibration has no exact acknowledged conversation identity; do not rerun",
			formatRetryAt(
				nowForCalibration(config).Add(
					defaultAmbiguousCooldown,
				),
			),
			*data,
		)
	}
	record.TargetID = lease.TargetID()
	record.ConversationID = conversation.ID
	record.SendDispatch = calibrationActionDispatch(sendAction)
	record.State = "answer_captured"
	record.UpdatedAt = nowForCalibration(config).Format(
		time.RFC3339Nano,
	)
	if err := config.Store.Save(ctx, *record); err != nil {
		return calibrationFailure(
			runID,
			config,
			webagent.StageObserveTerminal,
			target,
			pending,
			sendAction,
			conversation,
			"chatgpt_calibration_state_unavailable",
			"internal",
			"ChatGPT calibration answer state could not be persisted; do not rerun",
			"",
			*data,
		)
	}
	if err := lease.BeginNextAction(ctx, "delete"); err != nil {
		data.CompletionState = "delete_not_performed"
		return calibrationFailure(
			runID,
			config,
			webagent.StageObserveTerminal,
			target,
			pending,
			sendAction,
			conversation,
			"chatgpt_calibration_delete_state_failed",
			"internal",
			"ChatGPT calibration could not persist the delete action slot; do not rerun",
			"",
			*data,
		)
	}
	record.State = "delete_pending"
	record.UpdatedAt = nowForCalibration(config).Format(
		time.RFC3339Nano,
	)
	if err := config.Store.Save(ctx, *record); err != nil {
		data.CompletionState = "delete_not_performed"
		return calibrationFailure(
			runID,
			config,
			webagent.StageActionPending,
			target,
			pending,
			sendAction,
			conversation,
			"chatgpt_calibration_state_unavailable",
			"internal",
			"ChatGPT calibration delete-pending state could not be persisted; do not rerun",
			"",
			*data,
		)
	}

	deleteData := DeleteData{
		SchemaVersion:   DeleteSchemaVersion,
		CompletionState: "not_deleted",
		Metadata:        map[string]any{},
	}
	if err := preparePage(
		ctx,
		config.Client,
		lease.Session(),
		Origin+"/c/"+conversation.ID,
	); err != nil {
		data.CompletionState = "delete_not_performed"
		record.State = "delete_not_performed"
		record.UpdatedAt = nowForCalibration(config).Format(
			time.RFC3339Nano,
		)
		_ = config.Store.Save(context.Background(), *record)
		return calibrationFailure(
			runID,
			config,
			webagent.StageAttached,
			target,
			pending,
			sendAction,
			conversation,
			"chatgpt_calibration_delete_page_unavailable",
			"connection",
			"ChatGPT calibration exact conversation page could not be re-prepared for same-target deletion; do not rerun",
			"",
			*data,
		)
	}
	data.Metadata["delete_page_reprepared"] = true
	deadline := time.Now().Add(
		minDuration(90*time.Second, config.Timeout),
	)
	_, prepareErr := prepareCalibrationDelete(
		ctx,
		lease.Session(),
		conversation.ID,
		deadline,
		config.PollInterval,
		&deleteData,
	)
	data.Metadata["delete_preparation_attempts"] =
		deleteData.PreparationAttempts
	data.Metadata["delete_actionability_attempts"] =
		deleteData.ActionabilityAttempts
	for key, value := range deleteData.Metadata {
		data.Metadata["delete_"+key] = value
	}
	if prepareErr != nil {
		data.CompletionState = "delete_not_performed"
		record.State = "delete_not_performed"
		record.UpdatedAt = nowForCalibration(config).Format(
			time.RFC3339Nano,
		)
		_ = config.Store.Save(context.Background(), *record)
		return calibrationFailure(
			runID,
			config,
			webagent.StageAttached,
			target,
			pending,
			sendAction,
			conversation,
			prepareErr.code,
			"provider",
			prepareErr.message,
			"",
			*data,
		)
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		data.CompletionState = "delete_not_performed"
		return calibrationFailure(
			runID,
			config,
			webagent.StageAttached,
			target,
			pending,
			sendAction,
			conversation,
			"chatgpt_calibration_delete_prepare_state_failed",
			"internal",
			"ChatGPT calibration prepared delete state could not be persisted; do not rerun",
			"",
			*data,
		)
	}
	dispatcher := config.Confirm
	if dispatcher == nil {
		dispatcher = chatgptDeleteDispatcher{
			conversationID: conversation.ID,
		}
	}
	outcome, _ := lease.Dispatch(ctx, dispatcher)
	data.DeleteAction = actionEvidence(lease.Record())
	record.DeleteDispatch = calibrationActionDispatch(
		data.DeleteAction,
	)
	_ = lease.ReleaseInput()
	if outcome.Dispatch == browserflow.DispatchNotPerformed ||
		(outcome.Dispatch == "" &&
			lease.Record().RawInputCount == 0) {
		_ = lease.MarkIncomplete(context.Background())
		data.CompletionState = "delete_not_performed"
		record.State = "delete_not_performed"
		record.UpdatedAt = nowForCalibration(config).Format(
			time.RFC3339Nano,
		)
		_ = config.Store.Save(context.Background(), *record)
		return calibrationFailure(
			runID,
			config,
			webagent.StagePrepared,
			target,
			pending,
			sendAction,
			conversation,
			"chatgpt_calibration_delete_not_performed",
			"provider",
			"ChatGPT calibration delete was not performed; use cleanup and do not rerun calibration",
			"",
			*data,
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
				lease.Session(),
				conversation.ID,
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
		data.CompletionState = "deletion_ambiguous"
		record.State = "deletion_ambiguous"
		record.UpdatedAt = nowForCalibration(config).Format(
			time.RFC3339Nano,
		)
		_ = config.Store.Save(context.Background(), *record)
		return calibrationFailure(
			runID,
			config,
			webagent.StageActionDispatched,
			target,
			pending,
			data.DeleteAction,
			conversation,
			"chatgpt_calibration_delete_unconfirmed",
			"completion",
			"ChatGPT calibration delete was attempted without a same-target postcondition; do not repeat it",
			formatRetryAt(
				nowForCalibration(config).Add(
					defaultAmbiguousCooldown,
				),
			),
			*data,
		)
	}
	if err := lease.ConfirmPostcondition(
		ctx,
		deletePostconditionProof,
	); err != nil {
		data.CompletionState = "deletion_ambiguous"
		return calibrationFailure(
			runID,
			config,
			webagent.StageActionDispatched,
			target,
			pending,
			data.DeleteAction,
			conversation,
			"chatgpt_calibration_delete_postcondition_state_failed",
			"internal",
			"ChatGPT calibration delete postcondition could not be persisted; do not repeat it",
			"",
			*data,
		)
	}
	data.DeleteAction = actionEvidence(lease.Record())
	data.Postcondition = deletePostconditionProof
	data.CompletionState = "deleted"
	if err := lease.MarkTerminal(ctx); err != nil {
		return calibrationFailure(
			runID,
			config,
			webagent.StageObserveTerminal,
			target,
			pending,
			data.DeleteAction,
			conversation,
			"chatgpt_calibration_delete_terminal_state_failed",
			"internal",
			"ChatGPT calibration terminal delete state could not be persisted",
			"",
			*data,
		)
	}
	record.State = "deleted"
	record.Postcondition = deletePostconditionProof
	record.UpdatedAt = nowForCalibration(config).Format(
		time.RFC3339Nano,
	)
	if err := config.Store.Save(ctx, *record); err != nil {
		return calibrationFailure(
			runID,
			config,
			webagent.StageObserveTerminal,
			target,
			pending,
			data.DeleteAction,
			conversation,
			"chatgpt_calibration_state_unavailable",
			"internal",
			"ChatGPT calibration delete outcome could not be persisted",
			"",
			*data,
		)
	}
	if askState != webagent.StateTerminal {
		return calibrationFailure(
			runID,
			config,
			webagent.StageObserveTerminal,
			target,
			pending,
			data.DeleteAction,
			conversation,
			"chatgpt_calibration_answer_incomplete",
			"completion",
			"ChatGPT calibration conversation was deleted but its answer was not terminal",
			"",
			*data,
		)
	}
	result := operationSuccess(
		runID,
		config.BuildCommit,
		webagent.OperationCalibrate,
		webagent.StageObserveTerminal,
		"same_target_rendered_calibration",
		target,
		pending,
		*data,
		[]string{
			"cdp workflow agent chatgpt calibration status --json",
		},
	)
	result.State = webagent.StateTerminal
	result.Action = data.DeleteAction
	result.Conversation = conversation
	return result
}

func prepareCalibrationDelete(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	deadline time.Time,
	pollInterval time.Duration,
	data *DeleteData,
) (deleteObservation, *calibrationDeletePreparationError) {
	var observation deleteObservation
	attempts, err := pollUntil(
		ctx,
		time.Until(deadline),
		pollInterval,
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
		recordDeleteObservation(data, observation)
		return observation, &calibrationDeletePreparationError{
			code:    "chatgpt_calibration_delete_identity_not_ready",
			message: "ChatGPT calibration exact conversation identity did not become ready; do not rerun",
		}
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
			return observation, &calibrationDeletePreparationError{
				code:    "chatgpt_calibration_sidebar_open_not_performed",
				message: "ChatGPT calibration sidebar expansion was not performed; do not rerun",
			}
		}
	}
	identityAttempts, err := pollUntil(
		ctx,
		time.Until(deadline),
		pollInterval,
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
		recordDeleteObservation(data, observation)
		return observation, &calibrationDeletePreparationError{
			code:    "chatgpt_calibration_delete_exact_row_not_ready",
			message: "ChatGPT calibration did not expose one exact history row and options control; do not rerun",
		}
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
			return observation, &calibrationDeletePreparationError{
				code:    "chatgpt_calibration_delete_menu_open_not_performed",
				message: "ChatGPT calibration exact conversation menu was not opened; do not rerun",
			}
		}
	}
	menuAttempts, err := pollUntil(
		ctx,
		time.Until(deadline),
		pollInterval,
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
		recordDeleteObservation(data, observation)
		return observation, &calibrationDeletePreparationError{
			code:    "chatgpt_calibration_delete_menuitem_not_ready",
			message: "ChatGPT calibration exact Delete menuitem did not become actionable; do not rerun",
		}
	}
	outcome, clickErr := browserflow.ClickPoint(
		ctx,
		session,
		observation.DeleteMenuX,
		observation.DeleteMenuY,
	)
	if clickErr != nil &&
		outcome.Dispatch == browserflow.DispatchNotPerformed {
		return observation, &calibrationDeletePreparationError{
			code:    "chatgpt_calibration_delete_dialog_open_not_performed",
			message: "ChatGPT calibration exact Delete menuitem was not pressed; do not rerun",
		}
	}
	actionabilityAttempts, err := pollUntil(
		ctx,
		time.Until(deadline),
		pollInterval,
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
		recordDeleteObservation(data, observation)
		return observation, &calibrationDeletePreparationError{
			code:    "chatgpt_calibration_delete_confirmation_not_ready",
			message: "ChatGPT calibration exact Delete confirmation did not become actionable; do not rerun",
		}
	}
	return observation, nil
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
) webagent.Result {
	result := operationFailure(
		runID,
		config.BuildCommit,
		webagent.OperationCalibrate,
		stage,
		"same_target_rendered_calibration",
		target,
		cleanup,
		code,
		errClass,
		message,
		data,
		[]string{
			"cdp workflow agent chatgpt calibration status --json",
			"cdp workflow agent chatgpt calibration cleanup --json",
		},
	)
	result.Action = action
	result.Conversation = conversation
	if result.Error != nil {
		result.Error.RetryAt = retryAt
		if action != nil {
			result.Error.RetrySafe = action.RetrySafe
		}
	}
	return result
}

func replaceCalibrationResultFailure(
	result webagent.Result,
	code string,
	errClass string,
	message string,
) webagent.Result {
	result = replaceFailure(
		result,
		code,
		errClass,
		message,
		[]string{
			"cdp workflow agent chatgpt calibration status --json",
			"cdp workflow agent chatgpt calibration cleanup --json",
		},
	)
	if result.Error != nil && result.Action != nil {
		result.Error.RetrySafe = result.Action.RetrySafe
	}
	return result
}

func nowForCalibration(config CalibrationConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}
