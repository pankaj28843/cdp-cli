package chatgpt

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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
}

type ContinueData struct {
	SchemaVersion          string         `json:"schema_version"`
	ConversationID         string         `json:"conversation_id"`
	ProductMode            string         `json:"product_mode"`
	Intelligence           string         `json:"intelligence"`
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
}

func (d chatgptContinueDispatcher) Dispatch(
	ctx context.Context,
	session *cdp.PageSession,
) (browserflow.DispatchOutcome, error) {
	var observation composerObservation
	if err := observeComposer(
		ctx,
		session,
		d.prompt,
		&observation,
	); err != nil ||
		!continuationComposerReadyForSend(
			observation,
			d.conversationID,
			d.baselineUsers,
			d.baselineAssistants,
		) {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact ChatGPT continuation Send control was not actionable")
	}
	return browserflow.ClickPoint(
		ctx,
		session,
		observation.SendX,
		observation.SendY,
	)
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
	data := ContinueData{
		SchemaVersion:    ContinueSchemaVersion,
		ConversationID:   conversationID,
		ProductMode:      "Chat",
		Intelligence:     "Medium",
		CompletionState:  "not_submitted",
		ReadMode:         "not_started",
		PromptCharacters: utf8.RuneCountInString(prompt),
		Metadata:         map[string]any{},
	}
	notPerformed := notPerformedAction()
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
	_, auth, templateErr := config.Store.LoadTemplateStatus(
		ctx,
		now,
		DefaultAuthTTL,
	)
	if templateErr != nil && auth.State == "invalid" {
		return continueFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversationRef(conversationID),
			"chatgpt_auth_invalid", "auth",
			"ChatGPT owner-only auth evidence is invalid before continuation",
			"", data,
			[]string{"cdp workflow agent chatgpt auth refresh --json"},
		)
	}
	if !auth.Ready {
		return continueFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversationRef(conversationID),
			"chatgpt_auth_"+auth.State, "auth",
			"ChatGPT auth evidence is not ready before continuation",
			"", data,
			[]string{"cdp workflow agent chatgpt auth refresh --json"},
		)
	}
	runtime := config.Store.RuntimeStatus(
		ctx,
		now,
		DefaultCapabilitiesTTL,
	)
	if !runtime.Ready ||
		!containsString(runtime.ProductModes, "Chat") ||
		!containsString(runtime.IntelligenceOptions, "Medium") {
		return continueFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, conversationRef(conversationID),
			"chatgpt_paid_medium_unproven", "capability",
			"Paid Chat product and Medium intelligence are not proven before continuation",
			"", data,
			[]string{"cdp workflow agent chatgpt capabilities refresh --json"},
		)
	}

	conversation := conversationRef(conversationID)
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationConversationsContinue,
		"continue",
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
			loadAttempts, loadErr := pollUntil(
				ctx,
				config.ComposerTimeout,
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
					if err := observeRendered(
						ctx,
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
			data.Metadata["conversation_load_attempts"] = loadAttempts
			if loadErr != nil {
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
					"ChatGPT exact conversation was not terminal and continuation-ready",
					"", data, cleanupCommands(runID, pending),
				)
			}
			data.BaselineUserTurns = rendered.UserMessageCount
			data.BaselineAssistantTurns = rendered.AssistantCount

			var selection selectionObservation
			if err := evaluateInto(
				ctx,
				session,
				selectContinuationMediumExpression,
				&selection,
			); err != nil || !selection.OK {
				data.Metadata["selection_failure"] = selection.Reason
				_ = lease.MarkIncomplete(context.Background())
				return continueFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_paid_medium_selection_failed", "capability",
					"ChatGPT could not verify paid Chat product and Medium before continuation",
					"", data, cleanupCommands(runID, pending),
				)
			}
			data.Metadata["product_selection_action"] =
				selection.ProductAction
			data.Metadata["intelligence_selection_action"] =
				selection.IntelligenceAction

			verifyAttempts, verified, verifyErr :=
				prepareVerifiedContinuationPrompt(
					ctx,
					session,
					conversationID,
					prompt,
					data.BaselineUserTurns,
					data.BaselineAssistantTurns,
					config.ComposerTimeout,
					config.PollInterval,
				)
			data.Metadata["prompt_verify_attempts"] = verifyAttempts
			if verifyErr != nil {
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
			if err := lease.MarkPrepared(ctx); err != nil {
				return continueFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, conversation,
					"chatgpt_prompt_prepare_state_failed", "internal",
					"ChatGPT continuation prepared state could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			dispatcher := config.Send
			if dispatcher == nil {
				dispatcher = chatgptContinueDispatcher{
					prompt:             prompt,
					conversationID:     conversationID,
					baselineUsers:      data.BaselineUserTurns,
					baselineAssistants: data.BaselineAssistantTurns,
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
				(rendered.TerminalControl || stable >= 4) &&
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
					fmt.Sprintf(
						"cdp workflow agent chatgpt conversations await %s --json",
						conversationID,
					),
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
	timeout time.Duration,
	poll time.Duration,
) (int, composerObservation, error) {
	deadline := time.Now().Add(timeout)
	var observation composerObservation
	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= 8; attempt++ {
		attempts = attempt
		if err := prepareExactPrompt(ctx, session, prompt); err != nil {
			lastErr = err
		} else if err := observeComposer(
			ctx,
			session,
			prompt,
			&observation,
		); err != nil {
			lastErr = err
		} else if continuationComposerReadyForSend(
			observation,
			conversationID,
			baselineUsers,
			baselineAssistants,
		) {
			return attempt, observation, nil
		} else if continuationComposerPreparedExceptSend(
			observation,
			conversationID,
			baselineUsers,
			baselineAssistants,
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
						&observation,
					); err != nil {
						return false, err
					}
					return continuationComposerReadyForSend(
						observation,
						conversationID,
						baselineUsers,
						baselineAssistants,
					), nil
				},
			)
			attempts += waitAttempts
			if waitErr == nil {
				return attempts, observation, nil
			}
			lastErr = waitErr
			break
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
) bool {
	return observation.EditorReady &&
		observation.EditorCount == 1 &&
		observation.PromptMatches &&
		continuationProductModeReady(observation) &&
		observation.IntelligenceCount == 1 &&
		strings.EqualFold(
			observation.SelectedIntelligence,
			"Medium",
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
) bool {
	return continuationComposerPreparedExceptSend(
		observation,
		conversationID,
		baselineUsers,
		baselineAssistants,
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
				if observation.TerminalControl || stable >= 4 {
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

const selectContinuationMediumExpression = `(async () => {
  const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
  const visible = element => {
    if (!(element instanceof HTMLElement)) return false;
    const style = getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
  };
  const label = element => String(
    element && (
      element.innerText ||
      element.textContent ||
      element.getAttribute('aria-label') ||
      ''
    ) || ''
  ).replace(/\s+/g, ' ').trim();
  const radios = Array.from(document.querySelectorAll(
    'button[role="radio"]'
  )).filter(visible);
  const chats = radios.filter(button => label(button) === 'Chat');
  const works = radios.filter(button => label(button) === 'Work');
  let productAction = 'inherited_from_exact_conversation';
  if (chats.length !== 0 || works.length !== 0) {
    if (chats.length !== 1 || works.length !== 1) {
      return {ok: false, reason: 'ambiguous_product_controls'};
    }
    if (chats[0].getAttribute('aria-checked') !== 'true') {
      chats[0].click();
      productAction = 'selected';
      await sleep(400);
    } else {
      productAction = 'already_selected';
    }
    if (chats[0].getAttribute('aria-checked') !== 'true' ||
        works[0].getAttribute('aria-checked') !== 'false') {
      return {ok: false, reason: 'chat_product_not_selected'};
    }
  }
  const specialized = Array.from(document.querySelectorAll(
    'iframe[src*="deep-research"],iframe[src*="connector_openai_deep_research"],' +
    '[data-testid*="deep-research"][aria-pressed="true"],' +
    '[data-testid*="agent"][aria-pressed="true"]'
  ));
  if (specialized.length !== 0) {
    return {ok: false, reason: 'specialized_conversation_surface'};
  }
  const known = [
    'Instant', 'Instant 5.5', 'Medium', 'High',
    'Extra High', 'Pro', 'GPT-5.6 Sol'
  ];
  const pickers = () => Array.from(document.querySelectorAll(
    'button[aria-haspopup="menu"]'
  )).filter(button =>
    visible(button) && known.some(item =>
      item.toLowerCase() === label(button).toLowerCase()
    )
  );
  let current = pickers();
  if (current.length !== 1) {
    return {ok: false, reason: 'ambiguous_intelligence_picker'};
  }
  let intelligenceAction = 'already_selected';
  if (label(current[0]).toLowerCase() !== 'medium') {
    current[0].click();
    await sleep(400);
    const options = Array.from(document.querySelectorAll(
      '[role="menuitemradio"],[role="menuitem"],[role="option"]'
    )).filter(option =>
      visible(option) && label(option).toLowerCase() === 'medium'
    );
    if (options.length !== 1) {
      return {ok: false, reason: 'medium_option_unavailable'};
    }
    options[0].click();
    intelligenceAction = 'selected';
    await sleep(400);
    current = pickers();
  }
  if (current.length !== 1 ||
      label(current[0]).toLowerCase() !== 'medium') {
    return {ok: false, reason: 'medium_not_selected'};
  }
  return {
    ok: true,
    product_mode: 'Chat',
    product_action: productAction,
    intelligence: 'Medium',
    intelligence_action: intelligenceAction,
    reason: ''
  };
})()`
