package chatgpt

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const ContinueSchemaVersion = "chatgpt-conversation-continue/v1"

type ContinueConfig struct {
	BrowserConfig
	Store           *Store
	Timeout         time.Duration
	ComposerTimeout time.Duration
	PollInterval    time.Duration
	Now             func() time.Time
	Send            browserflow.Dispatcher
	Selection       SelectionPolicy
}

type ContinueData struct {
	SchemaVersion          string         `json:"schema_version"`
	ConversationID         string         `json:"conversation_id"`
	ProductMode            string         `json:"product_mode"`
	Intelligence           string         `json:"intelligence"`
	ThinkingPolicy         string         `json:"thinking_policy"`
	MinimumThinking        string         `json:"minimum_thinking,omitempty"`
	ModelPolicy            string         `json:"model_policy"`
	Model                  string         `json:"model,omitempty"`
	Text                   string         `json:"text"`
	CompletionState        string         `json:"completion_state"`
	ReadMode               string         `json:"read_mode"`
	PromptFingerprint      string         `json:"prompt_fingerprint,omitempty"`
	PromptCharacters       int            `json:"prompt_characters"`
	BaselineUserTurns      int            `json:"baseline_user_turns"`
	BaselineAssistantTurns int            `json:"baseline_assistant_turns"`
	Metadata               map[string]any `json:"metadata"`
}

type chatgptContinueDispatcher struct {
	prompt             string
	conversationID     string
	baselineUsers      int
	baselineAssistants int
	intelligence       string
	model              string
}

func (d chatgptContinueDispatcher) Dispatch(
	ctx context.Context,
	session *cdp.PageSession,
) (browserflow.DispatchOutcome, error) {
	// Selection preflight, including the resolved model, finishes immediately
	// before MarkPrepared. After action_pending this dispatcher only observes
	// the selection guard and composer, then emits at most one irreversible
	// Send input.
	if err := observeSelectionGuardAtSend(
		ctx,
		session,
		d.intelligence,
		d.model,
	); err != nil {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, err
	}
	var observation composerObservation
	if err := observeComposer(
		ctx,
		session,
		d.prompt,
		d.intelligence,
		&observation,
	); err != nil ||
		!continuationComposerReadyForSend(
			observation,
			d.conversationID,
			d.baselineUsers,
			d.baselineAssistants,
			d.intelligence,
		) {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact ChatGPT continuation Send control was not actionable")
	}
	outcome, clickErr := browserflow.ClickPoint(
		ctx,
		session,
		observation.SendX,
		observation.SendY,
	)
	return outcome, clickErr
}

func ContinueConversation(
	ctx context.Context,
	config ContinueConfig,
	conversationID string,
	prompt string,
) webagent.Result {
	conversationID = strings.TrimSpace(conversationID)
	prompt = strings.TrimRight(prompt, "\r\n")
	runID := webagent.NewRunID()
	selectionPolicy, selectionErr := NormalizeSelectionPolicy(config.Selection)
	data := ContinueData{
		SchemaVersion:    ContinueSchemaVersion,
		ConversationID:   conversationID,
		ProductMode:      "Chat",
		ThinkingPolicy:   selectionPolicy.Thinking,
		MinimumThinking:  selectionPolicy.MinimumThinking,
		ModelPolicy:      selectionPolicy.Model,
		CompletionState:  "not_submitted",
		ReadMode:         "not_started",
		PromptCharacters: utf8.RuneCountInString(prompt),
		Metadata:         map[string]any{},
	}
	notPerformed := notPerformedAction()
	if selectionErr != nil {
		return continueFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversationRef(conversationID),
			"chatgpt_selection_invalid", "usage",
			selectionErr.Error(), "", data,
			[]string{
				"cdp workflow agent chatgpt conversations continue --help",
			},
		)
	}
	config.Selection = selectionPolicy
	if !conversationIDPattern.MatchString(conversationID) {
		return continueFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"chatgpt_invalid_conversation_id", "usage",
			"ChatGPT conversation id contains unsupported characters",
			"", data, nil,
		)
	}
	if strings.TrimSpace(prompt) == "" {
		return continueFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversationRef(conversationID),
			"chatgpt_prompt_required", "usage",
			"ChatGPT continuation prompt must not be empty",
			"", data, nil,
		)
	}
	if data.PromptCharacters > MaxPromptCharacters {
		data.Metadata["max_prompt_characters"] = MaxPromptCharacters
		return continueFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversationRef(conversationID),
			"chatgpt_prompt_too_long", "usage",
			"ChatGPT continuation prompt exceeds the safe character limit",
			"", data, nil,
		)
	}
	data.PromptFingerprint = fingerprintPrompt(prompt)
	if config.Timeout <= 0 {
		config.Timeout = defaultAskTimeout
	}
	if config.ComposerTimeout <= 0 {
		config.ComposerTimeout = defaultComposerTimeout
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	if config.Store == nil {
		return continueFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversationRef(conversationID),
			"chatgpt_state_unavailable", "internal",
			"ChatGPT owner-only state is unavailable", "", data,
			[]string{"cdp workflow agent chatgpt doctor --json"},
		)
	}
	now := nowForContinue(config)
	_, auth, _ := config.Store.LoadTemplateStatus(
		ctx,
		now,
		DefaultAuthTTL,
	)
	data.Metadata["cached_auth_state"] = auth.State
	runtime := config.Store.RuntimeStatus(
		ctx,
		now,
		DefaultCapabilitiesTTL,
	)
	data.Metadata["cached_capability_state"] = runtime.State

	conversation := conversationRef(conversationID)
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationConversationsContinue,
		"send",
		"about:blank",
		"headed_browser_rendered",
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
				return continueFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_continuation_prepare_failed", "connection",
					"ChatGPT conversation could not be prepared on the exact headed target",
					"", data, cleanupCommands(runID, pending),
				)
			}

			var composer composerObservation
			var rendered renderedObservation
			readiness, loadErr := authreadiness.WaitForEvidence(
				ctx,
				session,
				authreadiness.MinimumAttempts,
				config.ComposerTimeout,
				config.PollInterval,
				func(observationCtx context.Context) (bool, error) {
					if err := observeComposer(
						observationCtx,
						session,
						"",
						"",
						&composer,
					); err != nil {
						return false, err
					}
					if err := observeRendered(
						observationCtx,
						session,
						&rendered,
					); err != nil {
						return false, err
					}
					return composer.ConversationID == conversationID &&
						rendered.RouteMatches &&
						rendered.ConversationID == conversationID &&
						composer.EditorReady &&
						composer.EditorCount == 1 &&
						composer.PromptMatches &&
						continuationProductModeReady(composer) &&
						composer.IntelligenceCount == 1 &&
						composer.SpecializedSurfaceCount == 0 &&
						rendered.UserMessageCount >= 1 &&
						rendered.AssistantCount >= 1 &&
						!rendered.Streaming &&
						rendered.TerminalControl, nil
				},
			)
			data.Metadata["conversation_readiness_attempt"] =
				readiness.Attempt
			data.Metadata["conversation_readiness_stage"] = readiness.Stage
			data.Metadata["conversation_observations"] =
				readiness.SuccessfulObservations
			if loadErr != nil || readiness.ObservationFailed() {
				data.Metadata["observed_route_conversation"] =
					composer.ConversationID == conversationID
				data.Metadata["observed_editor_count"] =
					composer.EditorCount
				data.Metadata["observed_chat_count"] =
					composer.ChatCount
				data.Metadata["observed_work_count"] =
					composer.WorkCount
				data.Metadata["observed_intelligence_count"] =
					composer.IntelligenceCount
				data.Metadata["observed_specialized_surface_count"] =
					composer.SpecializedSurfaceCount
				data.Metadata["observed_user_turns"] =
					rendered.UserMessageCount
				data.Metadata["observed_assistant_turns"] =
					rendered.AssistantCount
				data.Metadata["observed_streaming"] =
					rendered.Streaming
				data.Metadata["observed_terminal_control"] =
					rendered.TerminalControl
				_ = lease.MarkIncomplete(context.Background())
				return continueFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_conversation_observation_failed", "connection",
					"ChatGPT exact-conversation observation could not complete its bounded load, reload, hard-reload, and final-grace sequence",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if !readiness.Observed {
				data.Metadata["observed_route_conversation"] =
					composer.ConversationID == conversationID
				data.Metadata["observed_editor_count"] =
					composer.EditorCount
				data.Metadata["observed_chat_count"] =
					composer.ChatCount
				data.Metadata["observed_work_count"] =
					composer.WorkCount
				data.Metadata["observed_intelligence_count"] =
					composer.IntelligenceCount
				data.Metadata["observed_specialized_surface_count"] =
					composer.SpecializedSurfaceCount
				data.Metadata["observed_user_turns"] =
					rendered.UserMessageCount
				data.Metadata["observed_assistant_turns"] =
					rendered.AssistantCount
				data.Metadata["observed_streaming"] =
					rendered.Streaming
				data.Metadata["observed_terminal_control"] =
					rendered.TerminalControl
				_ = lease.MarkIncomplete(context.Background())
				return continueFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_conversation_not_ready", "provider",
					"ChatGPT exact conversation was not observed as terminal and continuation-ready after bounded load, reload, cache-bypassing hard reload, and final grace; the browser session may still be active",
					"", data, cleanupCommands(runID, pending),
				)
			}
			data.BaselineUserTurns = rendered.UserMessageCount
			data.BaselineAssistantTurns = rendered.AssistantCount

			selection, selectionErr := selectChatGPT(
				ctx,
				session,
				config.Selection,
				true,
				config.ComposerTimeout,
				config.PollInterval,
			)
			if selectionErr != nil || !selection.OK {
				if selectionErr != nil {
					data.Metadata["selection_failure"] =
						selectionErr.Error()
				} else {
					data.Metadata["selection_failure"] = selection.Reason
				}
				_ = lease.MarkIncomplete(context.Background())
				return continueFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_selection_failed", "capability",
					"ChatGPT could not verify the requested Chat product, thinking, model, and minimum before continuation",
					"", data, cleanupCommands(runID, pending),
				)
			}
			data.Metadata["product_selection_action"] =
				selection.ProductAction
			data.Metadata["intelligence_selection_action"] =
				selection.IntelligenceAction
			data.Metadata["available_thinking"] =
				append([]string{}, selection.IntelligenceOptions...)
			data.Metadata["model_selection_action"] = selection.ModelAction
			data.Metadata["available_models"] =
				append([]string{}, selection.ModelOptions...)
			data.Intelligence = selection.Intelligence
			data.Model = selection.Model

			dispatcher := config.Send
			verifyAttempts, verified, verifyErr :=
				prepareVerifiedContinuationPrompt(
					ctx,
					session,
					conversationID,
					prompt,
					data.BaselineUserTurns,
					data.BaselineAssistantTurns,
					data.Intelligence,
					data.Model,
					config.ComposerTimeout,
					config.PollInterval,
				)
			data.Metadata["prompt_verify_attempts"] = verifyAttempts
			if verifyErr != nil {
				data.Metadata["prompt_verify_failure"] = verifyErr.Error()
				_ = lease.MarkIncomplete(context.Background())
				return continueFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_continuation_prompt_verify_failed", "provider",
					"ChatGPT exact continuation prompt or route changed before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if verified.ConversationID != conversationID {
				_ = lease.MarkIncomplete(context.Background())
				return continueFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_continuation_route_changed", "provider",
					"ChatGPT continuation route changed before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.BindInputFingerprint(
				ctx,
				data.PromptFingerprint,
			); err != nil {
				return continueFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_prompt_identity_state_failed", "internal",
					"ChatGPT continuation prompt fingerprint could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if dispatcher == nil {
				guardErr := prepareSelectionGuardAtSend(
					ctx,
					session,
					data.Intelligence,
					data.Model,
					minDuration(
						config.ComposerTimeout,
						finalSelectionGuardTimeout,
					),
					config.PollInterval,
				)
				var guardedComposer composerObservation
				if guardErr == nil {
					guardErr = observeComposer(
						ctx,
						session,
						prompt,
						data.Intelligence,
						&guardedComposer,
					)
				}
				if guardErr != nil ||
					!continuationComposerReadyForSend(
						guardedComposer,
						conversationID,
						data.BaselineUserTurns,
						data.BaselineAssistantTurns,
						data.Intelligence,
					) {
					_ = lease.MarkIncomplete(context.Background())
					return continueFailure(
						runID, config, webagent.StageAttached,
						target, pending, notPerformed, conversation,
						"chatgpt_final_send_guard_failed", "provider",
						"ChatGPT final thinking, model, route, or composer guard changed before Send",
						"", data, cleanupCommands(runID, pending),
					)
				}
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return continueFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_prompt_prepare_state_failed", "internal",
					"ChatGPT continuation prepared state could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if dispatcher == nil {
				dispatcher = chatgptContinueDispatcher{
					prompt:             prompt,
					conversationID:     conversationID,
					baselineUsers:      data.BaselineUserTurns,
					baselineAssistants: data.BaselineAssistantTurns,
					intelligence:       data.Intelligence,
					model:              data.Model,
				}
			}
			outcome, dispatchErr := lease.Dispatch(ctx, dispatcher)
			record := lease.Record()
			action := actionEvidence(record)
			if dispatchErr != nil {
				data.Metadata["dispatch_error_observed"] = true
			}
			if err := lease.ReleaseInput(); err != nil {
				data.Metadata["input_release_failed"] = true
			}
			if record.RawInputCount == 0 {
				_ = lease.MarkIncomplete(context.Background())
				return continueFailure(
					runID, config, webagent.StagePrepared,
					target, pending, action, conversation,
					"chatgpt_continue_not_performed", "provider",
					"ChatGPT continuation Send was not performed; retrying is safe",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if outcome.Dispatch != browserflow.DispatchPerformed &&
				outcome.Dispatch != browserflow.DispatchUnknown {
				_ = lease.MarkIncomplete(context.Background())
				retryAt := nowForContinue(config).Add(
					defaultAmbiguousCooldown,
				)
				return continueFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, action, conversation,
					"chatgpt_continue_dispatch_unknown", "completion",
					"ChatGPT continuation raw Send input had an unclassified outcome; do not resend",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}

			ackDeadline := time.Now().Add(
				minDuration(45*time.Second, config.Timeout),
			)
			ackAttempts := 0
			acknowledged := false
			for time.Now().Before(ackDeadline) {
				ackAttempts++
				current := renderedObservation{}
				if err := observeRendered(
					ctx,
					session,
					&current,
				); err == nil {
					rendered = current
					if current.RouteMatches &&
						current.ConversationID == conversationID &&
						current.UserMessageCount ==
							data.BaselineUserTurns+1 &&
						current.AssistantCount >=
							data.BaselineAssistantTurns &&
						renderedPromptMatches(
							current,
							data.PromptFingerprint,
						) {
						acknowledged = true
						break
					}
					if current.ConversationID != "" &&
						current.ConversationID != conversationID {
						break
					}
					if current.UserMessageCount >
						data.BaselineUserTurns+1 {
						break
					}
				}
				if !waitForObservation(
					ctx,
					config.PollInterval,
					time.Until(ackDeadline),
				) {
					break
				}
			}
			data.Metadata["acknowledgement_attempts"] = ackAttempts
			data.Metadata["acknowledgement_prompt_identity_proved"] =
				acknowledged
			if !acknowledged {
				_ = lease.MarkIncomplete(context.Background())
				retryAt := nowForContinue(config).Add(
					defaultAmbiguousCooldown,
				)
				return continueFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, action, conversation,
					"chatgpt_continuation_identity_unproven",
					"completion",
					"ChatGPT continuation Send was attempted but the exact new turn was not proved; do not resend",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			if err := lease.Acknowledge(ctx, conversationID); err != nil {
				retryAt := nowForContinue(config).Add(
					defaultAmbiguousCooldown,
				)
				return continueFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, action, conversation,
					"chatgpt_acknowledgement_state_failed", "internal",
					"ChatGPT continuation acknowledgement could not be persisted; do not resend",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			action = actionEvidence(lease.Record())
			deadline := time.Now().Add(config.Timeout)
			rendered, stable, renderedAttempts :=
				waitRenderedContinuationAnswer(
					ctx,
					session,
					conversationID,
					prompt,
					data.BaselineUserTurns+1,
					data.BaselineAssistantTurns+1,
					deadline,
					config.PollInterval,
				)
			data.Metadata["rendered_read_attempts"] = renderedAttempts
			data.Metadata["rendered_terminal_stable_reads"] = stable
			data.Metadata["rendered_prompt_identity_proved"] =
				renderedPromptMatches(
					rendered,
					data.PromptFingerprint,
				)
			terminal := rendered.RouteMatches &&
				rendered.ConversationID == conversationID &&
				rendered.UserMessageCount ==
					data.BaselineUserTurns+1 &&
				rendered.AssistantCount ==
					data.BaselineAssistantTurns+1 &&
				!rendered.Streaming &&
				rendered.TerminalControl &&
				len(strings.TrimSpace(rendered.Text)) >=
					minimumUsefulAnswerChars(prompt) &&
				terminalAnswerTextValid(
					rendered.Text,
					map[string]any{},
				)
			if terminal {
				data.Text = strings.TrimSpace(rendered.Text)
				data.CompletionState = "terminal"
				data.ReadMode = "headed_browser_rendered"
				data.Metadata["answer_source"] =
					"same_target_rendered_assistant_message"
				if err := lease.MarkTerminal(ctx); err != nil {
					return continueFailure(
						runID, config, webagent.StageAcknowledged,
						target, pending, action, conversation,
						"chatgpt_terminal_state_failed", "internal",
						"ChatGPT continuation terminal state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return continueSuccess(
					runID, config, webagent.StateTerminal,
					target, pending, action, conversation, data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent chatgpt conversations detail %s --json",
							conversationID,
						),
					},
				)
			}
			_ = lease.MarkIncomplete(context.Background())
			data.CompletionState = "incomplete"
			data.ReadMode = "headed_browser_rendered"
			return continueSuccess(
				runID, config, webagent.StateIncomplete,
				target, pending, action, conversation, data,
				[]string{
					conversationAwaitCommand(conversationID),
				},
			)
		},
	)
}

func prepareVerifiedContinuationPrompt(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	prompt string,
	baselineUsers int,
	baselineAssistants int,
	intelligence string,
	model string,
	timeout time.Duration,
	poll time.Duration,
) (int, composerObservation, error) {
	deadline := time.Now().Add(timeout)
	var observation composerObservation
	var lastErr error
	attempts := 0
	for attempt := 1; ; attempt++ {
		attempts = attempt
		if err := prepareExactPrompt(ctx, session, prompt); err != nil {
			lastErr = err
		} else if err := verifySelectionAtSend(
			ctx,
			session,
			intelligence,
			model,
			time.Until(deadline),
			poll,
		); err != nil {
			lastErr = err
		} else if err := observeComposer(
			ctx,
			session,
			prompt,
			intelligence,
			&observation,
		); err != nil {
			lastErr = err
		} else if continuationComposerReadyForSend(
			observation,
			conversationID,
			baselineUsers,
			baselineAssistants,
			intelligence,
		) {
			return attempt, observation, nil
		} else if continuationComposerPreparedExceptSend(
			observation,
			conversationID,
			baselineUsers,
			baselineAssistants,
			intelligence,
		) {
			waitAttempts, waitErr := pollUntil(
				ctx,
				time.Until(deadline),
				poll,
				func() (bool, error) {
					if err := observeComposer(
						ctx,
						session,
						prompt,
						intelligence,
						&observation,
					); err != nil {
						return false, err
					}
					return continuationComposerReadyForSend(
						observation,
						conversationID,
						baselineUsers,
						baselineAssistants,
						intelligence,
					), nil
				},
			)
			attempts += waitAttempts
			if waitErr == nil {
				if err := verifySelectionAtSend(
					ctx,
					session,
					intelligence,
					model,
					time.Until(deadline),
					poll,
				); err != nil {
					lastErr = err
				} else if err := observeComposer(
					ctx,
					session,
					prompt,
					intelligence,
					&observation,
				); err != nil ||
					!continuationComposerReadyForSend(
						observation,
						conversationID,
						baselineUsers,
						baselineAssistants,
						intelligence,
					) {
					lastErr = fmt.Errorf(
						"ChatGPT continuation composer changed after final selection verification",
					)
				} else {
					return attempts, observation, nil
				}
			} else {
				lastErr = waitErr
			}
		} else {
			lastErr = fmt.Errorf(
				"exact ChatGPT continuation composer is not ready",
			)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 ||
			!waitForObservation(
				ctx,
				minDuration(500*time.Millisecond, poll*2),
				remaining,
			) {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf(
			"ChatGPT continuation prompt preparation attempts were exhausted",
		)
	}
	return attempts, observation, lastErr
}

func continuationComposerPreparedExceptSend(
	observation composerObservation,
	conversationID string,
	baselineUsers int,
	baselineAssistants int,
	intelligence string,
) bool {
	return observation.EditorReady &&
		observation.EditorCount == 1 &&
		observation.PromptMatches &&
		continuationProductModeReady(observation) &&
		observation.IntelligenceCount == 1 &&
		strings.EqualFold(
			observation.SelectedIntelligence,
			intelligence,
		) &&
		observation.SendCount == 1 &&
		observation.AssistantCount == baselineAssistants &&
		observation.UserMessageCount == baselineUsers &&
		observation.ConversationID == conversationID &&
		observation.SpecializedSurfaceCount == 0
}

func continuationComposerReadyForSend(
	observation composerObservation,
	conversationID string,
	baselineUsers int,
	baselineAssistants int,
	intelligence string,
) bool {
	return continuationComposerPreparedExceptSend(
		observation,
		conversationID,
		baselineUsers,
		baselineAssistants,
		intelligence,
	) && observation.SendReady
}

func waitRenderedContinuationAnswer(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	prompt string,
	expectedUsers int,
	expectedAssistants int,
	deadline time.Time,
	poll time.Duration,
) (renderedObservation, int, int) {
	var observation renderedObservation
	lastText := ""
	stable := 0
	attempts := 0
	for time.Now().Before(deadline) {
		attempts++
		current := renderedObservation{}
		if err := observeRendered(ctx, session, &current); err == nil {
			observation = current
			text := strings.TrimSpace(observation.Text)
			valid := observation.RouteMatches &&
				observation.ConversationID == conversationID &&
				observation.UserMessageCount == expectedUsers &&
				observation.AssistantCount == expectedAssistants &&
				renderedPromptMatches(
					observation,
					fingerprintPrompt(prompt),
				) &&
				!observation.Streaming &&
				len(text) >= minimumUsefulAnswerChars(prompt) &&
				terminalAnswerTextValid(text, map[string]any{})
			if valid {
				if text == lastText {
					stable++
				} else {
					lastText = text
					stable = 1
				}
				if observation.TerminalControl {
					return observation, stable, attempts
				}
			} else {
				lastText = ""
				stable = 0
			}
		}
		if !waitForObservation(ctx, poll, time.Until(deadline)) {
			break
		}
	}
	current := renderedObservation{}
	if err := observeRendered(ctx, session, &current); err == nil {
		observation = current
	}
	return observation, stable, attempts
}

func continueSuccess(
	runID string,
	config ContinueConfig,
	state webagent.State,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	data ContinueData,
	nextCommands []string,
) webagent.Result {
	result := operationSuccess(
		runID, config.BuildCommit,
		webagent.OperationConversationsContinue,
		webagent.StageObserveTerminal, data.ReadMode,
		target, cleanup, data, nextCommands,
	)
	result.State = state
	result.Action = action
	result.Conversation = conversation
	return result
}

func continueFailure(
	runID string,
	config ContinueConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	code string,
	errClass string,
	message string,
	retryAt string,
	data ContinueData,
	nextCommands []string,
) webagent.Result {
	result := operationFailure(
		runID, config.BuildCommit,
		webagent.OperationConversationsContinue,
		stage, data.ReadMode, target, cleanup,
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

func nowForContinue(config ContinueConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}

func continuationProductModeReady(observation composerObservation) bool {
	if observation.ChatCount == 0 && observation.WorkCount == 0 {
		return true
	}
	return observation.ChatCount == 1 &&
		observation.WorkCount == 1 &&
		observation.ChatSelected
}
