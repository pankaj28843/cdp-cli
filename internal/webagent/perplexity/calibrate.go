package perplexity

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	CalibrationSchemaVersion  = "perplexity-calibration/v1"
	defaultCalibrationTimeout = 3 * time.Minute
	calibrationPrompt         = "In one concise sentence, explain why an at-most-once browser action should persist a pending marker before raw input."
)

type CalibrationConfig struct {
	BrowserConfig
	AuthStore       *Store
	Store           *CalibrationStore
	HTTPClient      *http.Client
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
	CapabilityID          string                   `json:"capability_id"`
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
		ReadMode:          "not_started",
		CapabilityID:      "search",
		Metadata: map[string]any{
			"conversation_mode": "fresh_disposable",
		},
	}
	notPerformed := notPerformedAction()
	if config.AuthStore == nil || config.Store == nil {
		return calibrationFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{
				State: webagent.CleanupNotRequired,
			},
			notPerformed, nil,
			"perplexity_calibration_unavailable", "internal",
			"Perplexity calibration transaction is not configured",
			"", data,
			[]string{"cdp workflow agent perplexity doctor --json"},
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
	auth := config.AuthStore.AuthStatus(
		ctx,
		now,
		DefaultAuthTTL,
	)
	if !auth.Ready {
		return calibrationFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{
				State: webagent.CleanupNotRequired,
			},
			notPerformed, nil,
			"perplexity_auth_"+auth.State, "auth",
			"Perplexity auth evidence is not ready before calibration",
			"", data,
			[]string{"cdp workflow agent perplexity auth refresh --json"},
		)
	}
	template, err := config.AuthStore.LoadTemplate(ctx)
	if err != nil {
		return calibrationFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{
				State: webagent.CleanupNotRequired,
			},
			notPerformed, nil,
			"perplexity_auth_state_unreadable", "internal",
			"Perplexity owner-only request template could not be loaded before calibration",
			"", data,
			[]string{"cdp workflow agent perplexity auth refresh --json"},
		)
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
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{
				State: webagent.CleanupNotRequired,
			},
			notPerformed, nil,
			"perplexity_calibration_state_unavailable", "internal",
			"Perplexity calibration state could not be persisted",
			"", data,
			[]string{
				"cdp workflow agent perplexity calibration status --json",
			},
		)
	}
	overallDeadline := time.Now().Add(config.Timeout)
	result = runOwned(
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
			record.UpdatedAt = nowForCalibration(config).Format(
				time.RFC3339Nano,
			)
			if err := config.Store.Save(ctx, record); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageTargetOwned,
					target, pending, notPerformed, nil,
					"perplexity_calibration_state_unavailable", "internal",
					"Perplexity calibration target ownership could not be persisted",
					"", data, cleanupCommands(runID, pending),
				)
			}
			session := lease.Session()
			if err := preparePage(
				ctx,
				config.Client,
				session,
				HomeURL,
			); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, nil,
					"perplexity_calibration_composer_unavailable", "connection",
					"Perplexity calibration composer could not be loaded",
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
					if err := observeComposer(
						ctx,
						session,
						"",
						&composer,
					); err != nil {
						return false, err
					}
					return composer.RouteReady &&
						composer.EditorReady &&
						composer.EditorCount == 1 &&
						composer.SearchCount == 1 &&
						composer.SearchSelected &&
						composer.AssistantCount == 0 &&
						composer.ConversationID == "", nil
				},
			)
			data.Metadata["composer_attempts"] = composerAttempts
			if err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, nil,
					"perplexity_calibration_composer_not_ready", "provider",
					"Perplexity calibration fresh Search composer was not ready before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			verifyAttempts, composer, verifyErr := prepareVerifiedPrompt(
				ctx,
				session,
				calibrationPrompt,
				minDuration(
					config.ComposerTimeout,
					time.Until(overallDeadline),
				),
				config.PollInterval,
			)
			data.Metadata["prompt_verify_attempts"] = verifyAttempts
			if verifyErr != nil {
				data.Metadata["observed_route_ready"] = composer.RouteReady
				data.Metadata["observed_editor_count"] = composer.EditorCount
				data.Metadata["observed_prompt_matches"] = composer.PromptMatches
				data.Metadata["observed_search_count"] = composer.SearchCount
				data.Metadata["observed_search_selected"] =
					composer.SearchSelected
				data.Metadata["observed_assistant_count"] =
					composer.AssistantCount
				data.Metadata["observed_blank_conversation"] =
					composer.ConversationID == ""
				return calibrationFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, nil,
					"perplexity_calibration_prompt_verify_failed", "provider",
					"Perplexity calibration exact prompt, blank route, or Search mode changed before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.BindInputFingerprint(
				ctx,
				fingerprintPrompt(calibrationPrompt),
			); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, nil,
					"perplexity_calibration_prompt_identity_state_failed", "internal",
					"Perplexity calibration prompt fingerprint could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, nil,
					"perplexity_calibration_send_state_failed", "internal",
					"Perplexity calibration Send state could not be persisted",
					"", data, cleanupCommands(runID, pending),
				)
			}
			sendDispatcher := config.Send
			if sendDispatcher == nil {
				sendDispatcher = perplexitySendDispatcher{
					prompt: calibrationPrompt,
				}
			}
			sendOutcome, _ := lease.Dispatch(ctx, sendDispatcher)
			data.SendAction = actionEvidence(lease.Record())
			if sendOutcome.Dispatch ==
				browserflow.DispatchNotPerformed ||
				(sendOutcome.Dispatch == "" &&
					lease.Record().RawInputCount == 0) {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "send_not_performed"
				return calibrationFailure(
					runID, config, webagent.StagePrepared,
					target, pending, data.SendAction, nil,
					"perplexity_calibration_send_not_performed", "provider",
					"Perplexity calibration Send was not performed; retrying calibration is safe",
					"", data, cleanupCommands(runID, pending),
				)
			}

			var observation askObservation
			ackAttempts := 0
			for {
				ackAttempts++
				_ = observeAskState(ctx, session, &observation)
				if observation.RouteMatches &&
					conversationIDPattern.MatchString(
						observation.ConversationID,
					) {
					break
				}
				remaining := time.Until(overallDeadline)
				if remaining <= 0 ||
					!waitRendered(
						ctx,
						config.PollInterval,
						remaining,
					) {
					break
				}
			}
			data.Metadata["acknowledgement_attempts"] = ackAttempts
			if !observation.RouteMatches ||
				!conversationIDPattern.MatchString(
					observation.ConversationID,
				) {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "submission_unacknowledged"
				retryAt := retryAtFromRecord(
					lease.Record(),
					nowForCalibration(config),
				)
				return calibrationFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, data.SendAction, nil,
					"perplexity_calibration_submission_unacknowledged",
					"completion",
					"Perplexity calibration Send was attempted but not acknowledged; do not rerun",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			conversationID := observation.ConversationID
			conversation := conversationRef(conversationID)
			if err := lease.Acknowledge(
				ctx,
				conversationID,
			); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_acknowledgement_state_failed",
					"internal",
					"Perplexity calibration acknowledgement could not be persisted; do not rerun",
					"", data, cleanupCommands(runID, pending),
				)
			}
			data.SendAction = actionEvidence(lease.Record())
			record.State = "acknowledged"
			record.ConversationID = conversationID
			record.SendDispatch = lease.Record().Dispatch
			record.UpdatedAt = nowForCalibration(config).Format(
				time.RFC3339Nano,
			)
			if err := config.Store.Save(ctx, record); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAcknowledged,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_state_unavailable", "internal",
					"Perplexity calibration acknowledgement state could not be persisted; do not rerun",
					"", data, cleanupCommands(runID, pending),
				)
			}

			renderedStable := 0
			lastRenderedText := ""
			detailAttempts := 0
			for time.Now().Before(overallDeadline) {
				detailAttempts++
				_ = observeAskState(ctx, session, &observation)
				if observation.RouteMatches &&
					observation.ConversationID == conversationID &&
					observation.AnswerCount > 0 &&
					strings.TrimSpace(observation.Text) != "" &&
					!observation.Streaming {
					if observation.Text == lastRenderedText {
						renderedStable++
					} else {
						lastRenderedText = observation.Text
						renderedStable = 1
					}
					if renderedStable >= 2 {
						break
					}
				} else {
					renderedStable = 0
					lastRenderedText = ""
				}
				if !waitRendered(
					ctx,
					config.PollInterval,
					time.Until(overallDeadline),
				) {
					break
				}
			}
			data.DetailReadAttempts = detailAttempts
			renderedPromptFingerprint := ""
			if strings.TrimSpace(observation.Prompt) != "" {
				renderedPromptFingerprint = fingerprintPrompt(
					observation.Prompt,
				)
			}
			data.Metadata["rendered_prompt_identity_proved"] =
				renderedPromptFingerprint == data.PromptFingerprint
			data.Metadata["rendered_terminal_stable_reads"] =
				renderedStable
			stored, storedFailure := fetchConversationDetail(
				ctx,
				ReadConfig{
					Store:       config.AuthStore,
					HTTPClient:  config.HTTPClient,
					BuildCommit: config.BuildCommit,
					Now:         config.Now,
				},
				template,
				conversationID,
			)
			data.Metadata["stored_detail_attempts"] = 1
			storedPromptFingerprint, _ :=
				stored.Metadata["prompt_fingerprint"].(string)
			renderedTerminal := renderedStable >= 2 &&
				renderedPromptFingerprint == data.PromptFingerprint &&
				strings.TrimSpace(observation.Text) != ""
			storedTerminal := storedFailure == nil &&
				stored.CompletionState == "terminal" &&
				strings.TrimSpace(stored.Text) != "" &&
				storedPromptFingerprint == data.PromptFingerprint
			answerText := ""
			if storedTerminal {
				answerText = stored.Text
				data.ReadMode = stored.ReadMode
				data.Metadata["formatting"] = "provider_stored_markdown"
			} else if renderedTerminal &&
				storedFailure != nil &&
				storedFailure.code == "perplexity_browser_context_required" {
				answerText = observation.Text
				data.ReadMode = "headed_browser_fallback"
				data.Metadata["formatting"] = "rendered_visible_text"
				data.Metadata["stored_detail_state"] =
					"browser_context_required"
			}
			if strings.TrimSpace(answerText) == "" {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "answer_incomplete"
				record.State = "answer_incomplete"
				return calibrationFailure(
					runID, config, webagent.StageAcknowledged,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_answer_incomplete", "completion",
					"Perplexity calibration answer or exact prompt identity was incomplete; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration cleanup --json",
					},
				)
			}
			data.AnswerCaptured = true
			data.AnswerCharacters = utf8.RuneCountInString(
				strings.TrimSpace(answerText),
			)
			data.CompletionState = "answer_captured"
			if err := lease.MarkTerminal(ctx); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_answer_state_failed", "internal",
					"Perplexity calibration answer state could not be persisted; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration cleanup --json",
					},
				)
			}
			record.State = "answer_captured"
			record.UpdatedAt = nowForCalibration(config).Format(
				time.RFC3339Nano,
			)
			if err := config.Store.Save(ctx, record); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_state_unavailable", "internal",
					"Perplexity calibration answer state could not be persisted; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration cleanup --json",
					},
				)
			}
			if err := lease.BeginNextAction(
				ctx,
				"delete",
			); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_delete_state_failed", "internal",
					"Perplexity calibration could not persist the delete action slot; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration cleanup --json",
					},
				)
			}
			record.State = "delete_pending"
			record.UpdatedAt = nowForCalibration(config).Format(
				time.RFC3339Nano,
			)
			if err := config.Store.Save(ctx, record); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageActionPending,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_state_unavailable", "internal",
					"Perplexity calibration delete-pending state could not be persisted; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration cleanup --json",
					},
				)
			}
			preparationAttempts, actionabilityAttempts, err :=
				prepareCalibrationDelete(
					ctx,
					session,
					conversationID,
					overallDeadline,
					config.PollInterval,
				)
			data.PreparationAttempts = preparationAttempts
			data.ActionabilityAttempts = actionabilityAttempts
			if err != nil {
				data.CompletionState = "delete_not_performed"
				record.State = "delete_not_performed"
				return calibrationFailure(
					runID, config, webagent.StageAttached,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_delete_prepare_failed", "provider",
					"Perplexity calibration exact Delete confirmation was not actionable; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration cleanup --json",
					},
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageAttached,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_delete_prepare_state_failed",
					"internal",
					"Perplexity calibration prepared delete state could not be persisted; do not rerun",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration cleanup --json",
					},
				)
			}
			confirmDispatcher := config.Confirm
			if confirmDispatcher == nil {
				confirmDispatcher = deleteDispatcher{
					conversationID: conversationID,
				}
			}
			deleteOutcome, _ := lease.Dispatch(
				ctx,
				confirmDispatcher,
			)
			data.DeleteAction = actionEvidence(lease.Record())
			record.DeleteDispatch = lease.Record().Dispatch
			if deleteOutcome.Dispatch ==
				browserflow.DispatchNotPerformed ||
				(deleteOutcome.Dispatch == "" &&
					lease.Record().RawInputCount == 0) {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "delete_not_performed"
				record.State = "delete_not_performed"
				return calibrationFailure(
					runID, config, webagent.StagePrepared,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_delete_not_performed", "provider",
					"Perplexity calibration delete was not performed; use cleanup and do not rerun calibration",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration cleanup --json",
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
					"perplexity_calibration_delete_unconfirmed", "completion",
					"Perplexity calibration delete was attempted without a same-target postcondition; do not repeat it",
					retryAt.Format(time.RFC3339Nano), data,
					[]string{
						"cdp workflow agent perplexity calibration status --json",
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
					"perplexity_calibration_delete_postcondition_state_failed",
					"internal",
					"Perplexity calibration delete postcondition could not be persisted; do not repeat it",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration status --json",
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
					"perplexity_calibration_delete_terminal_state_failed",
					"internal",
					"Perplexity calibration delete terminal state could not be persisted",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration status --json",
					},
				)
			}
			record.State = "deleted"
			record.Postcondition = deletePostconditionProof
			record.UpdatedAt = nowForCalibration(config).Format(
				time.RFC3339Nano,
			)
			if err := config.Store.Save(ctx, record); err != nil {
				return calibrationFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending, data.SendAction, conversation,
					"perplexity_calibration_state_unavailable", "internal",
					"Perplexity calibration delete outcome could not be persisted",
					"", data,
					[]string{
						"cdp workflow agent perplexity calibration status --json",
					},
				)
			}
			return operationSuccess(
				runID, config.BuildCommit,
				webagent.OperationCalibrate,
				webagent.StateTerminal,
				webagent.StageObserveTerminal,
				"headed_browser", target, pending,
				data.DeleteAction, conversation, data,
				[]string{
					"cdp workflow agent perplexity calibration status --json",
				},
			)
		},
	)

	record.State = calibrationRecordState(result, data)
	record.SendDispatch = browserflow.Dispatch(
		actionDispatch(data.SendAction),
	)
	record.DeleteDispatch = browserflow.Dispatch(
		actionDispatch(data.DeleteAction),
	)
	record.Postcondition = data.Postcondition
	if result.Conversation != nil {
		record.ConversationID = result.Conversation.ID
	}
	if result.Evidence.Target != nil {
		record.TargetID = result.Evidence.Target.TargetID
	}
	record.TargetClosed =
		result.Cleanup.State == webagent.CleanupNotRequired ||
			(result.Cleanup.State == webagent.CleanupClosed &&
				result.Cleanup.TargetClosed)
	record.UpdatedAt = nowForCalibration(config).Format(
		time.RFC3339Nano,
	)
	if err := config.Store.Save(
		context.Background(),
		record,
	); err != nil {
		result = replaceFailure(
			result,
			"perplexity_calibration_state_unavailable",
			"internal",
			"Perplexity calibration outcome could not be persisted",
			[]string{
				"cdp workflow agent perplexity calibration status --json",
			},
		)
	}
	return result
}

func prepareCalibrationDelete(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	deadline time.Time,
	poll time.Duration,
) (int, int, error) {
	var observation deleteObservation
	preparationAttempts, err := pollUntil(
		ctx,
		time.Until(deadline),
		poll,
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
	if err != nil {
		return preparationAttempts, 0, err
	}
	if observation.ConfirmCount != 0 {
		return preparationAttempts, 0, fmt.Errorf(
			"Perplexity delete dialog existed before calibration delete preparation",
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
			return preparationAttempts, 0, clickErr
		}
	}
	menuItemAttempts, err := pollUntil(
		ctx,
		time.Until(deadline),
		poll,
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
		return preparationAttempts, menuItemAttempts, err
	}
	outcome, clickErr := browserflow.ClickPoint(
		ctx,
		session,
		observation.DeleteItemX,
		observation.DeleteItemY,
	)
	if clickErr != nil &&
		outcome.Dispatch == browserflow.DispatchNotPerformed {
		return preparationAttempts, menuItemAttempts, clickErr
	}
	actionabilityAttempts, err := pollUntil(
		ctx,
		time.Until(deadline),
		poll,
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
	return preparationAttempts, actionabilityAttempts, err
}

func waitForDeletePostcondition(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	deadline time.Time,
	poll time.Duration,
) bool {
	confirmed := false
	_, _ = pollUntil(
		ctx,
		time.Until(deadline),
		poll,
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
			confirmed = value.Confirmed
			return confirmed, nil
		},
	)
	return confirmed
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
	if data.DeleteAction != nil &&
		(data.DeleteAction.Dispatch == webagent.DispatchPerformed ||
			data.DeleteAction.Dispatch == webagent.DispatchUnknown) {
		action = data.DeleteAction
	} else if data.SendAction != nil &&
		(data.SendAction.Dispatch == webagent.DispatchPerformed ||
			data.SendAction.Dispatch == webagent.DispatchUnknown) {
		action = data.SendAction
	}
	return operationFailure(
		runID, config.BuildCommit,
		webagent.OperationCalibrate,
		stage, "headed_browser", target, cleanup, action, conversation,
		code, errClass, message, retryAt, data, nextCommands,
	)
}

func calibrationRecordState(
	result webagent.Result,
	data CalibrationData,
) string {
	switch data.CompletionState {
	case "deleted", "send_not_performed",
		"submission_unacknowledged", "answer_incomplete",
		"delete_not_performed", "deletion_ambiguous":
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

func minDuration(
	left time.Duration,
	right time.Duration,
) time.Duration {
	if left <= 0 {
		return right
	}
	if right <= 0 || left < right {
		return left
	}
	return right
}
