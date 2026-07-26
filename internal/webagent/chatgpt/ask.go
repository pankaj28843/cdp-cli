package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AskSchemaVersion         = "chatgpt-ask/v1"
	MaxPromptCharacters      = 18_000
	defaultAskTimeout        = 4 * time.Minute
	defaultComposerTimeout   = 45 * time.Second
	defaultAmbiguousCooldown = 5 * time.Minute
	renderedWaitFraction     = 0.85
)

type AskConfig struct {
	BrowserConfig
	Store           *Store
	FilePath        string
	Timeout         time.Duration
	ComposerTimeout time.Duration
	PollInterval    time.Duration
	Now             func() time.Time
	Send            browserflow.Dispatcher
	operation       webagent.Operation
	runID           string
	holdInput       bool
	completionHook  askCompletionHook
}

type askCompletionHook func(
	context.Context,
	*browserflow.Lease,
	*webagent.TargetEvidence,
	webagent.CleanupEvidence,
	webagent.State,
	*webagent.ActionEvidence,
	*webagent.ConversationRef,
	AskData,
) webagent.Result

type AskData struct {
	SchemaVersion      string          `json:"schema_version"`
	ConversationMode   string          `json:"conversation_mode"`
	ProductMode        string          `json:"product_mode"`
	Intelligence       string          `json:"intelligence"`
	Text               string          `json:"text"`
	CompletionState    string          `json:"completion_state"`
	ReadMode           string          `json:"read_mode"`
	PromptFingerprint  string          `json:"prompt_fingerprint,omitempty"`
	PromptCharacters   int             `json:"prompt_characters"`
	DetailReadAttempts int             `json:"detail_read_attempts"`
	Attachment         *AttachmentData `json:"attachment,omitempty"`
	Metadata           map[string]any  `json:"metadata"`
}

type composerObservation struct {
	RouteReady              bool    `json:"route_ready"`
	EditorReady             bool    `json:"editor_ready"`
	EditorCount             int     `json:"editor_count"`
	PromptMatches           bool    `json:"prompt_matches"`
	InnerTextMatches        bool    `json:"inner_text_matches"`
	TextContentMatches      bool    `json:"text_content_matches"`
	CanonicalMatches        bool    `json:"canonical_matches"`
	ExpectedCharacters      int     `json:"expected_characters"`
	InnerTextCharacters     int     `json:"inner_text_characters"`
	TextContentCharacters   int     `json:"text_content_characters"`
	CanonicalCharacters     int     `json:"canonical_characters"`
	ChatCount               int     `json:"chat_count"`
	WorkCount               int     `json:"work_count"`
	ChatSelected            bool    `json:"chat_selected"`
	IntelligenceCount       int     `json:"intelligence_count"`
	SelectedIntelligence    string  `json:"selected_intelligence"`
	SendCount               int     `json:"send_count"`
	SendReady               bool    `json:"send_ready"`
	SendX                   float64 `json:"send_x"`
	SendY                   float64 `json:"send_y"`
	AssistantCount          int     `json:"assistant_count"`
	UserMessageCount        int     `json:"user_message_count"`
	ConversationID          string  `json:"conversation_id"`
	SpecializedSurfaceCount int     `json:"specialized_surface_count"`
}

type selectionObservation struct {
	OK                 bool   `json:"ok"`
	ProductMode        string `json:"product_mode"`
	ProductAction      string `json:"product_action"`
	Intelligence       string `json:"intelligence"`
	IntelligenceAction string `json:"intelligence_action"`
	Reason             string `json:"reason"`
}

type renderedObservation struct {
	RouteMatches     bool     `json:"route_matches"`
	ConversationID   string   `json:"conversation_id"`
	Text             string   `json:"text"`
	PromptCandidates []string `json:"prompt_candidates"`
	Streaming        bool     `json:"is_streaming"`
	TerminalControl  bool     `json:"terminal_control_present"`
	AssistantCount   int      `json:"assistant_count"`
	UserMessageCount int      `json:"user_message_count"`
}

type chatgptSendDispatcher struct {
	prompt string
}

func (d chatgptSendDispatcher) Dispatch(
	ctx context.Context,
	session *cdp.PageSession,
) (browserflow.DispatchOutcome, error) {
	var observation composerObservation
	if err := observeComposer(ctx, session, d.prompt, &observation); err != nil ||
		!observation.RouteReady ||
		!observation.EditorReady ||
		observation.EditorCount != 1 ||
		!observation.PromptMatches ||
		observation.ChatCount != 1 ||
		observation.WorkCount != 1 ||
		!observation.ChatSelected ||
		observation.IntelligenceCount != 1 ||
		!strings.EqualFold(observation.SelectedIntelligence, "Medium") ||
		observation.SendCount != 1 ||
		!observation.SendReady ||
		observation.AssistantCount != 0 ||
		observation.UserMessageCount != 0 ||
		observation.ConversationID != "" {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact ChatGPT Send control was not actionable")
	}
	return browserflow.ClickPoint(
		ctx,
		session,
		observation.SendX,
		observation.SendY,
	)
}

func Ask(
	ctx context.Context,
	config AskConfig,
	prompt string,
) webagent.Result {
	prompt = strings.TrimRight(prompt, "\r\n")
	runID := strings.TrimSpace(config.runID)
	if runID == "" {
		runID = webagent.NewRunID()
	}
	data := AskData{
		SchemaVersion:    AskSchemaVersion,
		ConversationMode: "fresh_only",
		ProductMode:      "Chat",
		Intelligence:     "Medium",
		CompletionState:  "not_submitted",
		ReadMode:         "not_started",
		PromptCharacters: utf8.RuneCountInString(prompt),
		Metadata:         map[string]any{},
	}
	notPerformed := notPerformedAction()
	if strings.TrimSpace(prompt) == "" {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"chatgpt_prompt_required", "usage",
			"ChatGPT prompt must not be empty", "", data,
			[]string{"cdp workflow agent chatgpt ask --stdin --json"},
		)
	}
	if data.PromptCharacters > MaxPromptCharacters {
		data.Metadata["max_prompt_characters"] = MaxPromptCharacters
		data.Metadata["excess_characters"] =
			data.PromptCharacters - MaxPromptCharacters
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"chatgpt_prompt_too_long", "usage",
			"ChatGPT prompt exceeds the safe character limit", "", data,
			[]string{
				"Split the request into self-contained prompts below the limit.",
			},
		)
	}
	upload, uploadErr := resolveLocalUpload(config.FilePath)
	if uploadErr != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"chatgpt_attachment_invalid", "usage",
			uploadErr.Error(), "", data,
			[]string{"Pass one readable regular file with --file."},
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
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"chatgpt_state_unavailable", "internal",
			"ChatGPT owner-only state is unavailable", "", data,
			[]string{"cdp workflow agent chatgpt doctor --json"},
		)
	}
	now := nowForAsk(config)
	template, auth, templateErr := config.Store.LoadTemplateStatus(
		ctx,
		now,
		DefaultAuthTTL,
	)
	if templateErr != nil && auth.State == "invalid" {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"chatgpt_auth_invalid", "auth",
			"ChatGPT owner-only auth evidence is invalid before Send",
			"", data,
			[]string{"cdp workflow agent chatgpt auth refresh --json"},
		)
	}
	if !auth.Ready {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"chatgpt_auth_"+auth.State, "auth",
			"ChatGPT auth evidence is not ready before Send", "", data,
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
		!containsString(runtime.IntelligenceOptions, "Medium") ||
		(upload != nil && !runtime.FileUploadObserved) {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"chatgpt_paid_medium_unproven", "capability",
			"Paid Chat product, Medium intelligence, or requested file upload is not proven before Send",
			"", data,
			[]string{"cdp workflow agent chatgpt capabilities refresh --json"},
		)
	}
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		askOperation(config),
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
			if err := preparePage(ctx, config.Client, session, HomeURL); err != nil {
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"chatgpt_composer_prepare_failed", "connection",
					"ChatGPT composer could not be prepared on the exact headed target",
					"", data, cleanupCommands(runID, pending),
				)
			}
			var composer composerObservation
			composerAttempts, err := pollUntil(
				ctx,
				config.ComposerTimeout,
				config.PollInterval,
				func() (bool, error) {
					if err := observeComposer(ctx, session, "", &composer); err != nil {
						return false, err
					}
					return composer.RouteReady &&
						composer.EditorReady &&
						composer.EditorCount == 1 &&
						composer.ChatCount == 1 &&
						composer.WorkCount == 1 &&
						composer.IntelligenceCount == 1 &&
						composer.AssistantCount == 0 &&
						composer.UserMessageCount == 0 &&
						composer.ConversationID == "", nil
				},
			)
			data.Metadata["composer_attempts"] = composerAttempts
			if err != nil {
				data.Metadata["observed_route_ready"] = composer.RouteReady
				data.Metadata["observed_editor_count"] = composer.EditorCount
				data.Metadata["observed_chat_count"] = composer.ChatCount
				data.Metadata["observed_work_count"] = composer.WorkCount
				data.Metadata["observed_intelligence_count"] =
					composer.IntelligenceCount
				data.Metadata["observed_send_count"] = composer.SendCount
				data.Metadata["observed_assistant_count"] =
					composer.AssistantCount
				data.Metadata["observed_user_message_count"] =
					composer.UserMessageCount
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"chatgpt_composer_not_ready", "provider",
					"ChatGPT fresh paid composer did not become ready before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			var selection selectionObservation
			if err := evaluateInto(
				ctx,
				session,
				selectPaidChatMediumExpression,
				&selection,
			); err != nil || !selection.OK {
				data.Metadata["selection_failure"] = selection.Reason
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"chatgpt_paid_medium_selection_failed", "capability",
					"ChatGPT could not verify paid Chat product and Medium before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			data.Metadata["product_selection_action"] = selection.ProductAction
			data.Metadata["intelligence_selection_action"] =
				selection.IntelligenceAction
			if upload != nil {
				attachment, attachFailure := attachLocalFileOnce(
					ctx,
					session,
					*upload,
					minDuration(config.ComposerTimeout, 60*time.Second),
					config.PollInterval,
				)
				data.Attachment = &attachment
				if attachFailure != nil {
					_ = lease.MarkIncomplete(context.Background())
					if !attachFailure.RetrySafe {
						data.Metadata["send_dispatch"] =
							"not_performed"
						data.Metadata["send_raw_input_count"] = 0
						data.Metadata["attachment_retry_safe"] =
							false
						retryAt := nowForAsk(config).Add(
							defaultAmbiguousCooldown,
						)
						return askFailureWithoutActionRetry(
							runID, config, webagent.StageAttached,
							target, pending,
							attachFailure.Code, "completion",
							attachFailure.Message,
							retryAt.Format(time.RFC3339Nano),
							data, cleanupCommands(runID, pending),
						)
					}
					return askFailure(
						runID, config, webagent.StageAttached,
						target, pending, notPerformed, nil,
						attachFailure.Code, "provider",
						attachFailure.Message,
						"", data, cleanupCommands(runID, pending),
					)
				}
			}
			verifyAttempts, composer, verifyErr := prepareVerifiedPrompt(
				ctx,
				session,
				prompt,
				config.ComposerTimeout,
				config.PollInterval,
			)
			data.Metadata["prompt_verify_attempts"] = verifyAttempts
			if verifyErr != nil {
				data.Metadata["observed_route_ready"] = composer.RouteReady
				data.Metadata["observed_editor_count"] = composer.EditorCount
				data.Metadata["observed_prompt_matches"] = composer.PromptMatches
				data.Metadata["observed_inner_text_matches"] =
					composer.InnerTextMatches
				data.Metadata["observed_text_content_matches"] =
					composer.TextContentMatches
				data.Metadata["observed_canonical_matches"] =
					composer.CanonicalMatches
				data.Metadata["observed_expected_characters"] =
					composer.ExpectedCharacters
				data.Metadata["observed_inner_text_characters"] =
					composer.InnerTextCharacters
				data.Metadata["observed_text_content_characters"] =
					composer.TextContentCharacters
				data.Metadata["observed_canonical_characters"] =
					composer.CanonicalCharacters
				data.Metadata["observed_chat_selected"] = composer.ChatSelected
				data.Metadata["observed_selected_intelligence"] =
					composer.SelectedIntelligence
				data.Metadata["observed_send_count"] = composer.SendCount
				data.Metadata["observed_send_ready"] = composer.SendReady
				data.Metadata["observed_assistant_count"] = composer.AssistantCount
				data.Metadata["observed_user_message_count"] =
					composer.UserMessageCount
				data.Metadata["observed_blank_conversation"] =
					composer.ConversationID == ""
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"chatgpt_prompt_verify_failed", "provider",
					"ChatGPT exact prompt, fresh route, Chat product, or Medium changed before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if data.Attachment != nil {
				data.Attachment.SendReadyAfterUpload = composer.SendReady
			}
			if err := lease.BindInputFingerprint(
				ctx,
				data.PromptFingerprint,
			); err != nil {
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"chatgpt_prompt_identity_state_failed", "internal",
					"ChatGPT prompt fingerprint could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"chatgpt_prompt_prepare_state_failed", "internal",
					"ChatGPT prepared state could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			dispatcher := config.Send
			if dispatcher == nil {
				dispatcher = chatgptSendDispatcher{prompt: prompt}
			}
			outcome, dispatchErr := lease.Dispatch(ctx, dispatcher)
			record := lease.Record()
			action := actionEvidence(record)
			if dispatchErr != nil {
				data.Metadata["dispatch_error_observed"] = true
			}
			if !config.holdInput {
				if err := lease.ReleaseInput(); err != nil {
					data.Metadata["input_release_failed"] = true
				}
			}
			if record.RawInputCount == 0 {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StagePrepared, target, pending,
					action, nil,
					"chatgpt_send_not_performed", "provider",
					"ChatGPT Send was not performed; retrying the ask is safe",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if outcome.Dispatch != browserflow.DispatchPerformed &&
				outcome.Dispatch != browserflow.DispatchUnknown {
				action = &webagent.ActionEvidence{
					Dispatch:         webagent.DispatchUnknown,
					AttemptCount:     record.ActionAttemptCount,
					RawInputCount:    record.RawInputCount,
					RetrySafe:        false,
					PendingPersisted: record.PendingPersisted,
				}
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "submission_dispatch_unknown"
				retryAt := nowForAsk(config).Add(defaultAmbiguousCooldown)
				return askFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, action, nil,
					"chatgpt_send_dispatch_unknown", "completion",
					"ChatGPT raw Send input was attempted with an unclassified outcome; do not resubmit",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}

			deadline := time.Now().Add(config.Timeout)
			ackDeadline := time.Now().Add(
				minDuration(45*time.Second, config.Timeout),
			)
			var rendered renderedObservation
			ackAttempts := 0
			ackObservationErrors := 0
			renderedIdentityMisses := 0
			routeObserved := false
			routeUserObserved := false
			routeStable := true
			observedConversationID := ""
			promptIdentityProved := false
			promptIdentitySource := ""
			for time.Now().Before(ackDeadline) {
				ackAttempts++
				current := renderedObservation{}
				if err := observeRendered(ctx, session, &current); err != nil {
					ackObservationErrors++
					if ctx.Err() != nil {
						break
					}
					if !waitForObservation(
						ctx,
						config.PollInterval,
						time.Until(ackDeadline),
					) {
						break
					}
					continue
				}
				rendered = current
				currentRouteValid :=
					conversationIDPattern.MatchString(
						current.ConversationID,
					) &&
						current.RouteMatches
				if currentRouteValid {
					routeObserved = true
					if observedConversationID == "" {
						observedConversationID = current.ConversationID
					} else if current.ConversationID !=
						observedConversationID {
						routeStable = false
						break
					}
					if current.UserMessageCount == 1 {
						routeUserObserved = true
						if renderedPromptMatches(
							current,
							data.PromptFingerprint,
						) {
							promptIdentityProved = true
							promptIdentitySource =
								"same_target_rendered_user_message"
							break
						}
						renderedIdentityMisses++
						if renderedIdentityMisses >= 8 {
							break
						}
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
			data.Metadata["acknowledgement_observation_errors"] =
				ackObservationErrors
			data.Metadata["acknowledgement_route_observed"] =
				routeObserved
			data.Metadata["acknowledgement_route_stable"] = routeStable
			data.Metadata["acknowledgement_route_user_observed"] =
				routeUserObserved
			data.Metadata["rendered_identity_misses"] =
				renderedIdentityMisses
			data.Metadata["acknowledgement_user_message_count"] =
				rendered.UserMessageCount
			detailFallbackUsed := false
			var identityDetail ConversationDetailData
			if routeObserved &&
				routeUserObserved &&
				routeStable &&
				!promptIdentityProved &&
				rendered.UserMessageCount == 1 &&
				rendered.RouteMatches &&
				rendered.ConversationID == observedConversationID {
				detailFallbackUsed = true
				data.DetailReadAttempts = 1
				identityDetail, detailFailure := fetchOneHydratedDetail(
					ctx,
					session,
					template,
					observedConversationID,
				)
				if detailFailure != nil {
					_ = lease.MarkIncomplete(context.Background())
					data.CompletionState = "prompt_identity_detail_unavailable"
					return askFailure(
						runID, config, webagent.StageActionDispatched,
						target, pending, action, nil,
						detailFailure.code, detailFailure.errClass,
						"ChatGPT Send was attempted, but the one allowed same-target detail fallback could not prove prompt identity; do not resubmit",
						formatRetryAt(detailFailure.retryAt), data,
						cleanupCommands(runID, pending),
					)
				}
				storedFingerprint, fingerprintPresent :=
					identityDetail.Metadata["prompt_fingerprint"].(string)
				promptIdentityProved = fingerprintPresent &&
					storedFingerprint == data.PromptFingerprint &&
					identityDetail.ConversationID ==
						observedConversationID
				if promptIdentityProved {
					promptIdentitySource =
						"single_same_target_hydrated_detail_fallback"
				}
			}
			data.Metadata["acknowledgement_prompt_identity_proved"] =
				promptIdentityProved
			data.Metadata["prompt_identity_source"] =
				promptIdentitySource
			if !routeObserved || !routeStable || !promptIdentityProved {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "submission_identity_unproven"
				retryAt := nowForAsk(config).Add(defaultAmbiguousCooldown)
				return askFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, action, nil,
					"chatgpt_submission_identity_unproven", "completion",
					"ChatGPT Send was attempted but the same-target route and exact rendered prompt were not both proved; do not resubmit",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			conversationID := observedConversationID
			conversation := conversationRef(conversationID)
			if err := lease.Acknowledge(ctx, conversationID); err != nil {
				retryAt := nowForAsk(config).Add(defaultAmbiguousCooldown)
				return askFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, action, conversation,
					"chatgpt_acknowledgement_state_failed", "internal",
					"ChatGPT conversation acknowledgement could not be persisted; do not resubmit",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			action = actionEvidence(lease.Record())
			if detailFallbackUsed &&
				identityDetail.CompletionState == "terminal" &&
				strings.TrimSpace(identityDetail.Text) != "" {
				data.Text = strings.TrimSpace(identityDetail.Text)
				data.CompletionState = "terminal"
				data.ReadMode = browserReadMode
				data.Metadata["answer_source"] =
					"single_same_target_hydrated_detail_fallback"
				if err := lease.MarkTerminal(ctx); err != nil {
					return askFailure(
						runID, config, webagent.StageAcknowledged,
						target, pending, action, conversation,
						"chatgpt_terminal_state_failed", "internal",
						"ChatGPT hydrated-detail terminal state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return finishAsk(
					ctx, lease,
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

			renderedDeadline := webagent.FractionalDeadline(
				time.Now(),
				deadline,
				renderedWaitFraction,
			)
			rendered, renderedStable, renderedAttempts := waitRenderedAnswer(
				ctx,
				session,
				conversationID,
				prompt,
				renderedDeadline,
				config.PollInterval,
			)
			data.Metadata["rendered_read_attempts"] = renderedAttempts
			data.Metadata["rendered_assistant_count"] = rendered.AssistantCount
			data.Metadata["rendered_terminal_stable_reads"] = renderedStable
			data.Metadata["rendered_prompt_candidate_count"] =
				len(rendered.PromptCandidates)
			renderedPromptMatched := renderedPromptMatches(
				rendered,
				data.PromptFingerprint,
			)
			data.Metadata["rendered_prompt_identity_proved"] =
				renderedPromptMatched
			renderedTerminal := rendered.RouteMatches &&
				rendered.ConversationID == conversationID &&
				rendered.UserMessageCount == 1 &&
				!rendered.Streaming &&
				(rendered.TerminalControl || renderedStable >= 4) &&
				len(strings.TrimSpace(rendered.Text)) >= minimumUsefulAnswerChars(prompt) &&
				terminalAnswerTextValid(rendered.Text, map[string]any{})
			if renderedTerminal {
				data.Text = strings.TrimSpace(rendered.Text)
				data.CompletionState = "terminal"
				data.ReadMode = "headed_browser_rendered"
				data.Metadata["answer_source"] =
					"same_target_rendered_assistant_message"
				if err := lease.MarkTerminal(ctx); err != nil {
					return askFailure(
						runID, config, webagent.StageAcknowledged,
						target, pending, action, conversation,
						"chatgpt_terminal_state_failed", "internal",
						"ChatGPT rendered terminal state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return finishAsk(
					ctx, lease,
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

			if detailFallbackUsed {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "incomplete"
				data.ReadMode = browserReadMode
				data.Metadata["answer_source"] =
					"single_same_target_hydrated_detail_identity_only"
				return finishAsk(
					ctx, lease,
					runID, config, webagent.StateIncomplete,
					target, pending, action, conversation, data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent chatgpt conversations await %s --json",
							conversationID,
						),
					},
				)
			}

			data.DetailReadAttempts = 1
			detailPath := "/backend-api/conversation/" +
				url.PathEscape(conversationID)
			response, detailFailure := browserFetch(
				ctx,
				session,
				template,
				Origin+detailPath,
				ConversationDetailRoute,
			)
			if detailFailure != nil {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "detail_unavailable"
				return askFailure(
					runID, config, webagent.StageAcknowledged,
					target, pending, action, conversation,
					detailFailure.code, detailFailure.errClass,
					"ChatGPT acknowledged the request, but the one allowed hydrated-detail fallback was unavailable; do not resubmit",
					formatRetryAt(detailFailure.retryAt), data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent chatgpt conversations detail %s --json",
							conversationID,
						),
					},
				)
			}
			var payload map[string]any
			if err := decodeBoundedJSON(
				strings.NewReader(response.Body),
				&payload,
			); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "detail_invalid"
				return askFailure(
					runID, config, webagent.StageAcknowledged,
					target, pending, action, conversation,
					"chatgpt_invalid_detail_response", "provider",
					"ChatGPT acknowledged the request, but the one allowed hydrated-detail fallback was invalid; do not resubmit",
					"", data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent chatgpt conversations detail %s --json",
							conversationID,
						),
					},
				)
			}
			stored, _ := parseConversationDetailPayload(
				ConversationDetailData{
					SchemaVersion:   ConversationDetailSchemaVersion,
					ConversationID:  conversationID,
					CompletionState: "incomplete",
					ReadMode:        browserReadMode,
					Metadata: map[string]any{
						"source":    "hydrated_conversation_detail",
						"transport": "same_target_browser_fetch",
					},
				},
				payload,
				response.StatusCode,
			)
			storedFingerprint, fingerprintPresent :=
				stored.Metadata["prompt_fingerprint"].(string)
			if !fingerprintPresent ||
				storedFingerprint != data.PromptFingerprint {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "prompt_identity_unproven"
				return askFailure(
					runID, config, webagent.StageAcknowledged,
					target, pending, action, conversation,
					"chatgpt_stored_prompt_identity_unproven", "completion",
					"ChatGPT stored detail did not prove the submitted prompt identity; do not resubmit",
					"", data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent chatgpt conversations detail %s --json",
							conversationID,
						),
					},
				)
			}
			if stored.CompletionState == "terminal" &&
				strings.TrimSpace(stored.Text) != "" {
				data.Text = stored.Text
				data.CompletionState = "terminal"
				data.ReadMode = browserReadMode
				data.Metadata["answer_source"] =
					"single_same_target_hydrated_detail_fallback"
				if err := lease.MarkTerminal(ctx); err != nil {
					return askFailure(
						runID, config, webagent.StageAcknowledged,
						target, pending, action, conversation,
						"chatgpt_terminal_state_failed", "internal",
						"ChatGPT hydrated-detail terminal state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return finishAsk(
					ctx, lease,
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
			data.ReadMode = browserReadMode
			data.Metadata["answer_source"] =
				"single_same_target_hydrated_detail_incomplete"
			return finishAsk(
				ctx, lease,
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

func observeComposer(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
	observation *composerObservation,
) error {
	promptJSON, err := json.Marshal(prompt)
	if err != nil {
		return fmt.Errorf("encode ChatGPT prompt verification")
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  const normalize = value => String(value || '').replace(/\r\n/g, '\n');
	  const label = element => String(
	    element && (
	      element.innerText ||
	      element.textContent ||
	      element.getAttribute('aria-label') ||
	      ''
	    ) || ''
	  ).replace(/\s+/g, ' ').trim();
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const actionable = element => {
	    if (!element || !visible(element) || element.disabled ||
	      element.getAttribute('aria-disabled') === 'true') {
	      return {ready: false, x: -1, y: -1};
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
	  const editors = Array.from(document.querySelectorAll(
	    '#prompt-textarea,[contenteditable="true"][role="textbox"]'
	  )).filter((element, index, values) =>
	    values.indexOf(element) === index && visible(element) && element.isContentEditable
	  );
	  const editor = editors.length === 1 ? editors[0] : null;
	  const inlineText = node => {
	    if (node.nodeType === Node.TEXT_NODE) {
	      return node.nodeValue || '';
	    }
	    if (!(node instanceof HTMLElement)) return '';
	    if (node.tagName === 'BR') return '\n';
	    return Array.from(node.childNodes).map(inlineText).join('');
	  };
	  const blockText = node => {
	    const text = inlineText(node);
	    return node instanceof HTMLElement &&
	      node.dataset.emptyParagraph === 'true' &&
	      text === '\n' ? '' : text;
	  };
	  const canonicalEditorText = editor ?
	    normalize(Array.from(editor.childNodes).map(blockText).join('\n')) : '';
	  const radios = Array.from(document.querySelectorAll(
	    'button[role="radio"]'
	  )).filter(visible);
	  const chats = radios.filter(button => label(button) === 'Chat');
	  const works = radios.filter(button => label(button) === 'Work');
	  const known = [
	    'Instant', 'Instant 5.5', 'Medium', 'High',
	    'Extra High', 'Pro', 'GPT-5.6 Sol'
	  ];
	  const intelligence = Array.from(document.querySelectorAll(
	    'button[aria-haspopup="menu"]'
	  )).filter(button =>
	    visible(button) && known.some(item =>
	      item.toLowerCase() === label(button).toLowerCase()
	    )
	  );
	  const sends = Array.from(document.querySelectorAll(
	    'button[data-testid="send-button"],button#composer-submit-button'
	  )).filter((button, index, values) =>
	    values.indexOf(button) === index && visible(button)
	  );
	  const send = sends.length === 1 ? sends[0] : null;
	  const sendAction = actionable(send);
	  const route = location.pathname.match(/^\/c\/([A-Za-z0-9_-]+)$/);
	  const assistants = Array.from(document.querySelectorAll(
	    'section[data-turn="assistant"] [data-message-author-role="assistant"],' +
	    '[data-message-author-role="assistant"]'
	  )).filter((element, index, values) => values.indexOf(element) === index);
	  const users = Array.from(document.querySelectorAll(
	    'section[data-turn="user"] [data-message-author-role="user"],' +
	    '[data-message-author-role="user"]'
	  )).filter((element, index, values) => values.indexOf(element) === index);
	  const specialized = Array.from(document.querySelectorAll(
	    'iframe[src*="deep-research"],iframe[src*="connector_openai_deep_research"],' +
	    '[data-testid*="deep-research"][aria-pressed="true"],' +
	    '[data-testid*="agent"][aria-pressed="true"]'
	  ));
	  const editorInnerText = editor ? normalize(editor.innerText || '') : '';
	  const editorTextContent = editor ? normalize(editor.textContent || '') : '';
	  return {
	    route_ready: location.origin === 'https://chatgpt.com' &&
	      location.pathname === '/',
	    editor_ready: Boolean(editor),
	    editor_count: editors.length,
	    prompt_matches: Boolean(editor) &&
	      canonicalEditorText === normalize(expected),
	    inner_text_matches: Boolean(editor) &&
	      editorInnerText === normalize(expected),
	    text_content_matches: Boolean(editor) &&
	      editorTextContent === normalize(expected),
	    canonical_matches: Boolean(editor) &&
	      canonicalEditorText === normalize(expected),
	    expected_characters: normalize(expected).length,
	    inner_text_characters: editorInnerText.length,
	    text_content_characters: editorTextContent.length,
	    canonical_characters: canonicalEditorText.length,
	    chat_count: chats.length,
	    work_count: works.length,
	    chat_selected: chats.length === 1 &&
	      chats[0].getAttribute('aria-checked') === 'true',
	    intelligence_count: intelligence.length,
	    selected_intelligence: intelligence.length === 1 ?
	      label(intelligence[0]) : '',
	    send_count: sends.length,
	    send_ready: sendAction.ready,
	    send_x: sendAction.x,
	    send_y: sendAction.y,
	    assistant_count: assistants.length,
	    user_message_count: users.length,
	    conversation_id: route ? route[1] : '',
	    specialized_surface_count: specialized.length
	  };
	})()`, promptJSON)
	return evaluateInto(ctx, session, expression, observation)
}

func prepareExactPrompt(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
) error {
	var selected struct {
		OK bool `json:"ok"`
	}
	if err := evaluateInto(ctx, session, `(() => {
	  const editors = Array.from(document.querySelectorAll(
	    '#prompt-textarea,[contenteditable="true"][role="textbox"]'
	  )).filter((element, index, values) =>
	    values.indexOf(element) === index && element.isContentEditable
	  );
	  if (editors.length !== 1) return {ok: false};
	  const editor = editors[0];
	  editor.focus();
	  const selection = window.getSelection();
	  if (!selection) return {ok: false};
	  const range = document.createRange();
	  range.selectNodeContents(editor);
	  selection.removeAllRanges();
	  selection.addRange(range);
	  return {ok: document.activeElement === editor};
	})()`, &selected); err != nil || !selected.OK {
		return fmt.Errorf("select exact ChatGPT composer")
	}
	return browserflow.InsertText(ctx, session, prompt)
}

func prepareVerifiedPrompt(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
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
		} else if composerReadyForSend(observation) {
			return attempt, observation, nil
		} else if composerPreparedExceptSend(observation) {
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
					return composerReadyForSend(observation), nil
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
				"exact ChatGPT prompt and paid composer are not ready",
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
			"exact ChatGPT prompt preparation attempts were exhausted",
		)
	}
	return attempts, observation, lastErr
}

func composerPreparedExceptSend(observation composerObservation) bool {
	return observation.RouteReady &&
		observation.PromptMatches &&
		observation.ChatCount == 1 &&
		observation.WorkCount == 1 &&
		observation.ChatSelected &&
		observation.IntelligenceCount == 1 &&
		strings.EqualFold(
			observation.SelectedIntelligence,
			"Medium",
		) &&
		observation.SendCount == 1 &&
		observation.AssistantCount == 0 &&
		observation.UserMessageCount == 0 &&
		observation.ConversationID == ""
}

func composerReadyForSend(observation composerObservation) bool {
	return composerPreparedExceptSend(observation) &&
		observation.SendReady
}

func observeRendered(
	ctx context.Context,
	session *cdp.PageSession,
	observation *renderedObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
	  const visible = element => Boolean(
	    element && !element.disabled &&
	    element.getAttribute('aria-hidden') !== 'true' &&
	    (element.offsetWidth || element.offsetHeight || element.getClientRects().length)
	  );
	  const unique = nodes => Array.from(new Set(nodes));
	  const assistants = unique(Array.from(document.querySelectorAll(
	    'section[data-turn="assistant"] [data-message-author-role="assistant"],' +
	    '[data-message-author-role="assistant"]'
	  )));
	  const users = unique(Array.from(document.querySelectorAll(
	    'section[data-turn="user"] [data-message-author-role="user"],' +
	    '[data-message-author-role="user"]'
	  )));
	  const assistant = assistants.length ? assistants[assistants.length - 1] : null;
	  const user = users.length ? users[users.length - 1] : null;
	  const promptNode = user && (
	    user.querySelector('.whitespace-pre-wrap') || user
	  );
	  const trimTrailingNewlines = value => String(value || '')
	    .replace(/\r\n/g, '\n')
	    .replace(/[\r\n]+$/, '');
	  const inlineText = node => {
	    if (node.nodeType === Node.TEXT_NODE) {
	      return node.nodeValue || '';
	    }
	    if (!(node instanceof HTMLElement)) return '';
	    if (node.tagName === 'BR') return '\n';
	    return Array.from(node.childNodes).map(inlineText).join('');
	  };
	  const blockText = node => {
	    const text = inlineText(node);
	    return node instanceof HTMLElement &&
	      node.dataset.emptyParagraph === 'true' &&
	      text === '\n' ? '' : text;
	  };
	  const canonical = element => {
	    if (!element) return '';
	    const children = Array.from(element.childNodes);
	    const blockStructured = children.length > 1 &&
	      children.every(node =>
	        node.nodeType === Node.TEXT_NODE ||
	        node instanceof HTMLElement &&
	          ['P', 'DIV'].includes(node.tagName)
	      );
	    return blockStructured ?
	      children.map(blockText).join('\n') :
	      String(element.textContent || '');
	  };
	  const promptCandidates = unique([
	    user && user.innerText,
	    user && user.textContent,
	    promptNode && promptNode.innerText,
	    promptNode && promptNode.textContent,
	    canonical(promptNode),
	  ].map(trimTrailingNewlines).filter(Boolean));
	  const turn = assistant && assistant.closest(
	    'section[data-turn="assistant"],[data-turn="assistant"]'
	  );
	  const streamingState = turn ? Array.from(turn.querySelectorAll(
	    '[data-is-streaming="true"],[aria-busy="true"]'
	  )).some(visible) : false;
	  const streamingControl = Array.from(document.querySelectorAll(
	    'button[data-testid="stop-button"],button[aria-label*="Stop"],' +
	    'button[aria-label*="stop"]'
	  )).some(visible);
	  const terminalControl = Boolean(turn && Array.from(turn.querySelectorAll(
	    'button[data-testid="copy-turn-action-button"][aria-label="Copy response"],' +
	    'button[aria-label="Copy response"],button[aria-label="Regenerate response"]'
	  )).some(visible));
	  const match = location.pathname.match(/^\/c\/([A-Za-z0-9_-]+)$/);
	  return {
	    route_matches: Boolean(match),
	    conversation_id: match ? match[1] : '',
	    text: assistant ? String(assistant.innerText || assistant.textContent || '').trim() : '',
	    prompt_candidates: promptCandidates,
	    is_streaming: streamingState || streamingControl,
	    terminal_control_present: terminalControl,
	    assistant_count: assistants.length,
	    user_message_count: users.length
	  };
	})()`, observation)
}

func waitRenderedAnswer(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	prompt string,
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
				observation.UserMessageCount == 1 &&
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

func renderedPromptMatches(
	observation renderedObservation,
	expectedFingerprint string,
) bool {
	for _, candidate := range observation.PromptCandidates {
		if candidate != "" &&
			fingerprintPrompt(candidate) == expectedFingerprint {
			return true
		}
	}
	return false
}

func fetchOneHydratedDetail(
	ctx context.Context,
	session *cdp.PageSession,
	template RequestTemplate,
	conversationID string,
) (ConversationDetailData, *readFailure) {
	detailPath := "/backend-api/conversation/" +
		url.PathEscape(conversationID)
	response, failure := browserFetch(
		ctx,
		session,
		template,
		Origin+detailPath,
		ConversationDetailRoute,
	)
	if failure != nil {
		return ConversationDetailData{}, failure
	}
	var payload map[string]any
	if err := decodeBoundedJSON(
		strings.NewReader(response.Body),
		&payload,
	); err != nil {
		return ConversationDetailData{}, &readFailure{
			code:       "chatgpt_invalid_detail_response",
			errClass:   "provider",
			message:    "ChatGPT hydrated conversation detail was invalid",
			statusCode: response.StatusCode,
		}
	}
	stored, parseFailure := parseConversationDetailPayload(
		ConversationDetailData{
			SchemaVersion:   ConversationDetailSchemaVersion,
			ConversationID:  conversationID,
			CompletionState: "incomplete",
			ReadMode:        browserReadMode,
			Metadata: map[string]any{
				"source":    "hydrated_conversation_detail",
				"transport": "same_target_browser_fetch",
			},
		},
		payload,
		response.StatusCode,
	)
	stored.ReadMode = browserReadMode
	return stored, parseFailure
}

func waitForObservation(
	ctx context.Context,
	delay time.Duration,
	remaining time.Duration,
) bool {
	if remaining <= 0 {
		return false
	}
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	if delay > remaining {
		delay = remaining
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func askSuccess(
	runID string,
	config AskConfig,
	state webagent.State,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	data AskData,
	nextCommands []string,
) webagent.Result {
	result := operationSuccess(
		runID, config.BuildCommit, askOperation(config),
		webagent.StageObserveTerminal, data.ReadMode,
		target, cleanup, data, nextCommands,
	)
	result.State = state
	result.Action = action
	result.Conversation = conversation
	return result
}

func askFailure(
	runID string,
	config AskConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	code string,
	errClass string,
	message string,
	retryAt string,
	data AskData,
	nextCommands []string,
) webagent.Result {
	result := operationFailure(
		runID, config.BuildCommit, askOperation(config),
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

func askFailureWithoutActionRetry(
	runID string,
	config AskConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	code string,
	errClass string,
	message string,
	retryAt string,
	data AskData,
	nextCommands []string,
) webagent.Result {
	result := operationFailure(
		runID, config.BuildCommit, askOperation(config),
		stage, data.ReadMode, target, cleanup,
		code, errClass, message, data, nextCommands,
	)
	if result.Error != nil {
		result.Error.RetrySafe = false
		result.Error.RetryAt = retryAt
	}
	return result
}

func finishAsk(
	ctx context.Context,
	lease *browserflow.Lease,
	runID string,
	config AskConfig,
	state webagent.State,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	data AskData,
	nextCommands []string,
) webagent.Result {
	if config.completionHook != nil {
		return config.completionHook(
			ctx,
			lease,
			target,
			cleanup,
			state,
			action,
			conversation,
			data,
		)
	}
	return askSuccess(
		runID,
		config,
		state,
		target,
		cleanup,
		action,
		conversation,
		data,
		nextCommands,
	)
}

func askOperation(config AskConfig) webagent.Operation {
	if config.operation != "" {
		return config.operation
	}
	return webagent.OperationAsk
}

func nowForAsk(config AskConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}

func minimumUsefulAnswerChars(prompt string) int {
	if utf8.RuneCountInString(strings.TrimSpace(prompt)) >= 240 {
		return 80
	}
	return 20
}

func minDuration(left time.Duration, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func formatRetryAt(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

const selectPaidChatMediumExpression = `(async () => {
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
  if (chats.length !== 1 || works.length !== 1) {
    return {ok: false, reason: 'ambiguous_product_controls'};
  }
  let productAction = 'already_selected';
  if (chats[0].getAttribute('aria-checked') !== 'true') {
    chats[0].click();
    productAction = 'selected';
    await sleep(400);
  }
  if (chats[0].getAttribute('aria-checked') !== 'true' ||
      works[0].getAttribute('aria-checked') !== 'false') {
    return {ok: false, reason: 'chat_product_not_selected'};
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
