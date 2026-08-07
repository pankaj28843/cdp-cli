package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AskSchemaVersion              = "chatgpt-ask/v1"
	MaxPromptCharacters           = 18_000
	defaultAskTimeout             = 4 * time.Minute
	defaultProviderGateAskTimeout = 8 * time.Minute
	defaultImageAskTimeout        = 40 * time.Minute
	defaultComposerTimeout        = 45 * time.Second
	defaultAmbiguousCooldown      = 5 * time.Minute
	finalSelectionGuardTimeout    = 5 * time.Second
	renderedWaitFraction          = 0.85
)

type AskConfig struct {
	BrowserConfig
	Store           *Store
	FilePath        string
	Tool            string
	Timeout         time.Duration
	ComposerTimeout time.Duration
	PollInterval    time.Duration
	Now             func() time.Time
	Send            browserflow.Dispatcher
	Selection       SelectionPolicy
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
	SchemaVersion      string                   `json:"schema_version"`
	ConversationMode   string                   `json:"conversation_mode"`
	ProductMode        string                   `json:"product_mode"`
	Intelligence       string                   `json:"intelligence"`
	ThinkingPolicy     string                   `json:"thinking_policy"`
	MinimumThinking    string                   `json:"minimum_thinking,omitempty"`
	ModelPolicy        string                   `json:"model_policy"`
	Model              string                   `json:"model,omitempty"`
	Tool               string                   `json:"tool,omitempty"`
	OutputKind         string                   `json:"output_kind,omitempty"`
	Text               string                   `json:"text"`
	CompletionState    string                   `json:"completion_state"`
	ReadMode           string                   `json:"read_mode"`
	PromptFingerprint  string                   `json:"prompt_fingerprint,omitempty"`
	PromptCharacters   int                      `json:"prompt_characters"`
	DetailReadAttempts int                      `json:"detail_read_attempts"`
	Attachment         *AttachmentData          `json:"attachment,omitempty"`
	Attachments        []ConversationAttachment `json:"attachments"`
	Metadata           map[string]any           `json:"metadata"`
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
	ToolCount               int     `json:"tool_count"`
	SelectedTool            string  `json:"selected_tool"`
}

type selectionObservation struct {
	OK                  bool     `json:"ok"`
	ProductMode         string   `json:"product_mode"`
	ProductAction       string   `json:"product_action"`
	Intelligence        string   `json:"intelligence"`
	IntelligenceAction  string   `json:"intelligence_action"`
	IntelligenceOptions []string `json:"intelligence_options"`
	Model               string   `json:"model"`
	ModelAction         string   `json:"model_action"`
	ModelOptions        []string `json:"model_options"`
	Reason              string   `json:"reason"`
}

func recordSelectionReadiness(
	metadata map[string]any,
	surface selectionSurface,
) {
	metadata["observed_selection_editor_ready"] = surface.Editor.Ready
	metadata["observed_selection_chat_count"] = surface.ChatCount
	metadata["observed_selection_work_count"] = surface.WorkCount
	metadata["observed_selection_chat_ready"] = surface.Chat.Ready
	metadata["observed_selection_picker_count"] = surface.PickerCount
	metadata["observed_selection_picker_ready"] = surface.Picker.Ready
	metadata["observed_selected_thinking"] = surface.SelectedThinking
}

type renderedObservation struct {
	RouteMatches                 bool     `json:"route_matches"`
	ConversationID               string   `json:"conversation_id"`
	Text                         string   `json:"text"`
	PromptCandidates             []string `json:"prompt_candidates"`
	Streaming                    bool     `json:"is_streaming"`
	TerminalControl              bool     `json:"terminal_control_present"`
	AssistantCount               int      `json:"assistant_count"`
	UserMessageCount             int      `json:"user_message_count"`
	StoppedThinkingMarkerPresent bool     `json:"stopped_thinking_marker_present"`
	TerminalNoAnswer             bool     `json:"terminal_no_answer"`
	TerminalNoAnswerReason       string   `json:"terminal_no_answer_reason"`
	GeneratedImageCount          int      `json:"generated_image_count"`
	GeneratedImageReady          bool     `json:"generated_image_ready"`
	GeneratedImageWidth          int      `json:"generated_image_width"`
	GeneratedImageHeight         int      `json:"generated_image_height"`
	ImageRecoveryAttempted       bool     `json:"image_recovery_attempted"`
}

type chatgptSendDispatcher struct {
	prompt       string
	intelligence string
	model        string
	tool         string
	attachment   *attachmentExpectation
}

const chatGPTComposerSelector = "#prompt-textarea"

func pressChatGPTComposerEnter(
	ctx context.Context,
	session *cdp.PageSession,
) (browserflow.DispatchOutcome, error) {
	return browserflow.PressEnterOnSelector(
		ctx,
		session,
		chatGPTComposerSelector,
	)
}

func (d chatgptSendDispatcher) Dispatch(
	ctx context.Context,
	session *cdp.PageSession,
) (browserflow.DispatchOutcome, error) {
	// Selection preflight, including the resolved model, finishes immediately
	// before MarkPrepared. Once action_pending is durable this dispatcher must
	// perform no reversible raw clicks: it passively observes the selection
	// guard and composer, then emits at most the single irreversible Send input.
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
	var attachment attachmentObservation
	if err := observeExpectedAttachment(
		ctx,
		session,
		d.attachment,
		&attachment,
	); err != nil || !attachment.OK {
		return browserflow.DispatchOutcome{
				Dispatch: browserflow.DispatchNotPerformed,
			}, fmt.Errorf(
				"exact ChatGPT attachment was not retained and ready at Send",
			)
	}
	// Keep the composer observation immediately before the one raw Send input.
	// ChatGPT's current composer is a ProseMirror textbox whose Enter handler
	// submits the form. Focus the exact editor and send one trusted CDP Enter;
	// do not synthesize DOM KeyboardEvents or click a potentially re-rendered
	// coordinate.
	var observation composerObservation
	observeErr := observeComposer(
		ctx,
		session,
		d.prompt,
		d.intelligence,
		&observation,
	)
	if d.tool != "" {
		observeErr = observeComposerWithTool(
			ctx,
			session,
			d.prompt,
			toolDisplayLabel(d.tool),
			d.intelligence,
			&observation,
		)
	}
	if observeErr != nil ||
		!observation.RouteReady ||
		!observation.EditorReady ||
		observation.EditorCount != 1 ||
		!observation.PromptMatches ||
		observation.ChatCount != 1 ||
		observation.WorkCount != 1 ||
		!observation.ChatSelected ||
		observation.IntelligenceCount != 1 ||
		!strings.EqualFold(
			observation.SelectedIntelligence,
			d.intelligence,
		) ||
		observation.SendCount != 1 ||
		!observation.SendReady ||
		observation.AssistantCount != 0 ||
		observation.UserMessageCount != 0 ||
		observation.ConversationID != "" ||
		(d.tool != "" && !strings.EqualFold(
			observation.SelectedTool,
			toolDisplayLabel(d.tool),
		)) {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact ChatGPT Send control was not actionable")
	}
	return pressChatGPTComposerEnter(ctx, session)
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
	selectionPolicy, selectionErr := NormalizeSelectionPolicy(config.Selection)
	data := AskData{
		SchemaVersion:    AskSchemaVersion,
		ConversationMode: "fresh_only",
		ProductMode:      "Chat",
		ThinkingPolicy:   selectionPolicy.Thinking,
		MinimumThinking:  selectionPolicy.MinimumThinking,
		ModelPolicy:      selectionPolicy.Model,
		CompletionState:  "not_submitted",
		ReadMode:         "not_started",
		PromptCharacters: utf8.RuneCountInString(prompt),
		Attachments:      []ConversationAttachment{},
		Metadata:         map[string]any{},
	}
	notPerformed := notPerformedAction()
	if selectionErr != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"chatgpt_selection_invalid", "usage",
			selectionErr.Error(), "", data,
			[]string{"cdp workflow agent chatgpt ask --help"},
		)
	}
	tool, toolErr := NormalizeTool(config.Tool)
	data.Tool = tool
	if toolErr != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"chatgpt_tool_invalid", "usage",
			toolErr.Error(), "", data,
			[]string{"cdp workflow agent chatgpt ask --help"},
		)
	}
	config.Selection = selectionPolicy
	config.Tool = tool
	if tool != "" {
		data.Metadata["tool_policy"] = tool
	}
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
		if usesAnswerNowGate(tool) {
			config.Timeout = defaultProviderGateAskTimeout
		} else if isImageTool(tool) {
			config.Timeout = defaultImageAskTimeout
		}
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
	template, auth, _ := config.Store.LoadTemplateStatus(
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
			var surface selectionSurface
			readiness, err := authreadiness.WaitForEvidence(
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
					composerReady := composer.RouteReady &&
						composer.EditorReady &&
						composer.EditorCount == 1 &&
						composer.ChatCount == 1 &&
						composer.WorkCount == 1 &&
						composer.IntelligenceCount == 1 &&
						composer.AssistantCount == 0 &&
						composer.UserMessageCount == 0 &&
						composer.ConversationID == ""
					if !composerReady {
						return false, nil
					}
					if err := observeSelectionSurface(
						observationCtx,
						session,
						&surface,
					); err != nil {
						return false, err
					}
					return selectionSurfaceReady(
						surface,
						false,
					), nil
				},
			)
			data.Metadata["composer_readiness_attempt"] = readiness.Attempt
			data.Metadata["composer_readiness_stage"] = readiness.Stage
			data.Metadata["composer_observations"] =
				readiness.SuccessfulObservations
			if err != nil || readiness.ObservationFailed() {
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
				recordSelectionReadiness(data.Metadata, surface)
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"chatgpt_composer_observation_failed", "connection",
					"ChatGPT fresh-composer and selection-control observation could not complete its bounded load, reload, hard-reload, and final-grace sequence",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if !readiness.Observed {
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
				recordSelectionReadiness(data.Metadata, surface)
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"chatgpt_composer_not_ready", "provider",
					"ChatGPT fresh composer and selection controls were not observed after bounded load, reload, cache-bypassing hard reload, and final grace; the browser session may still be active",
					"", data, cleanupCommands(runID, pending),
				)
			}
			var selection selectionObservation
			selection, err = selectChatGPT(
				ctx,
				session,
				config.Selection,
				false,
				config.ComposerTimeout,
				config.PollInterval,
			)
			if err != nil || !selection.OK {
				if err != nil {
					data.Metadata["selection_failure"] = err.Error()
				} else {
					data.Metadata["selection_failure"] = selection.Reason
				}
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"chatgpt_selection_failed", "capability",
					"ChatGPT could not verify the requested Chat product, thinking, model, and minimum before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			data.Metadata["product_selection_action"] = selection.ProductAction
			data.Metadata["intelligence_selection_action"] =
				selection.IntelligenceAction
			data.Metadata["available_thinking"] =
				append([]string{}, selection.IntelligenceOptions...)
			data.Metadata["model_selection_action"] = selection.ModelAction
			data.Metadata["available_models"] =
				append([]string{}, selection.ModelOptions...)
			data.Intelligence = selection.Intelligence
			data.Model = selection.Model
			if tool != "" {
				selectedTool, toolSelectionErr := selectChatGPTTool(
					ctx,
					session,
					tool,
					config.ComposerTimeout,
					config.PollInterval,
				)
				if toolSelectionErr != nil {
					data.Metadata["tool_selection_failure"] = toolSelectionErr.Error()
					_ = lease.MarkIncomplete(context.Background())
					return askFailure(
						runID, config, webagent.StageAttached, target, pending,
						notPerformed, nil,
						"chatgpt_tool_selection_failed", "capability",
						"ChatGPT could not select and verify the requested plus-menu tool before Send",
						"", data, cleanupCommands(runID, pending),
					)
				}
				data.Tool = selectedTool
				data.Metadata["selected_tool"] = selectedTool
			}
			var expectedAttachment *attachmentExpectation
			if upload != nil {
				attachment, expectation, attachFailure := attachLocalFileOnce(
					ctx,
					session,
					*upload,
					minDuration(config.ComposerTimeout, 60*time.Second),
					config.PollInterval,
				)
				data.Attachment = &attachment
				expectedAttachment = expectation
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
			dispatcher := config.Send
			verifyAttempts, composer, verifyErr := prepareVerifiedPromptWithTool(
				ctx,
				session,
				prompt,
				data.Intelligence,
				data.Model,
				data.Tool,
				expectedAttachment,
				config.ComposerTimeout,
				config.PollInterval,
			)
			data.Metadata["prompt_verify_attempts"] = verifyAttempts
			if verifyErr != nil {
				data.Metadata["prompt_verify_failure"] = verifyErr.Error()
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
					"ChatGPT exact prompt, fresh route, Chat product, or selected thinking changed before Send",
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
			if dispatcher == nil {
				if err := prepareSelectionGuardAtSend(
					ctx,
					session,
					data.Intelligence,
					data.Model,
					minDuration(
						config.ComposerTimeout,
						finalSelectionGuardTimeout,
					),
					config.PollInterval,
				); err != nil {
					_ = lease.MarkIncomplete(context.Background())
					return askFailure(
						runID, config, webagent.StageAttached,
						target, pending, notPerformed, nil,
						"chatgpt_final_send_guard_failed", "provider",
						"ChatGPT final thinking or model guard changed before Send",
						"", data, cleanupCommands(runID, pending),
					)
				}
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
			if dispatcher == nil {
				dispatcher = chatgptSendDispatcher{
					prompt:       prompt,
					intelligence: data.Intelligence,
					model:        data.Model,
					tool:         data.Tool,
					attachment:   expectedAttachment,
				}
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
			renderedDeadline := webagent.FractionalDeadline(
				time.Now(),
				deadline,
				renderedWaitFraction,
			)
			if usesAnswerNowGate(data.Tool) {
				data.Metadata["rendered_wait_policy"] =
					"full_timeout_for_provider_answer_gate"
				renderedDeadline = deadline
			} else if isImageTool(data.Tool) {
				renderedDeadline = deadline
				data.Metadata["rendered_wait_policy"] =
					"full_timeout_for_image_or_visualization_generation"
			}
			if usesAnswerNowGate(data.Tool) {
				gateClicked, gateAttempts, gateErr :=
					advanceChatGPTAnswerNowGate(
						ctx,
						session,
						renderedDeadline,
						config.PollInterval,
					)
				data.Metadata["answer_gate_observation_attempts"] =
					gateAttempts
				data.Metadata["answer_gate_click_count"] = 0
				if gateClicked {
					data.Metadata["answer_gate_click_count"] = 1
				}
				if gateErr != nil {
					_ = lease.MarkIncomplete(context.Background())
					data.CompletionState = "answer_gate_unavailable"
					data.ReadMode = browserReadMode
					data.Metadata["answer_source"] =
						"same_target_rendered_provider_gate"
					return finishAsk(
						ctx, lease,
						runID, config, webagent.StateIncomplete,
						target, pending, action, conversation, data,
						[]string{
							conversationAwaitCommand(conversationID),
						},
					)
				}
			}
			rendered, renderedStable, renderedAttempts := waitRenderedAnswer(
				ctx,
				session,
				conversationID,
				prompt,
				data.Tool,
				renderedDeadline,
				config.PollInterval,
			)
			data.Metadata["rendered_read_attempts"] = renderedAttempts
			data.Metadata["rendered_assistant_count"] = rendered.AssistantCount
			data.Metadata["rendered_terminal_stable_reads"] = renderedStable
			data.Metadata["rendered_generated_image_count"] =
				rendered.GeneratedImageCount
			data.Metadata["rendered_generated_image_ready"] =
				rendered.GeneratedImageReady
			data.Metadata["rendered_generated_image_width"] =
				rendered.GeneratedImageWidth
			data.Metadata["rendered_generated_image_height"] =
				rendered.GeneratedImageHeight
			data.Metadata["rendered_image_recovery_attempted"] =
				rendered.ImageRecoveryAttempted
			data.Metadata["rendered_prompt_candidate_count"] =
				len(rendered.PromptCandidates)
			renderedPromptMatched := renderedPromptMatches(
				rendered,
				data.PromptFingerprint,
			)
			data.Metadata["rendered_prompt_identity_proved"] =
				renderedPromptMatched
			textAnswerReady := !isImageTool(data.Tool) &&
				len(strings.TrimSpace(rendered.Text)) >=
					minimumUsefulAnswerChars(prompt) &&
				terminalAnswerTextValid(rendered.Text, map[string]any{})
			imageAnswerReady := isImageTool(data.Tool) &&
				rendered.GeneratedImageReady
			renderedTerminal := rendered.RouteMatches &&
				rendered.ConversationID == conversationID &&
				rendered.UserMessageCount == 1 &&
				renderedPromptMatched &&
				!rendered.Streaming &&
				(textAnswerReady || imageAnswerReady) &&
				(rendered.TerminalControl || imageAnswerReady)
			if renderedTerminal {
				data.Text = strings.TrimSpace(rendered.Text)
				if imageAnswerReady && !textAnswerReady {
					// The rendered turn can contain only UI controls such as
					// "Edit". The image attachment is the answer; do not expose
					// that control label as answer text.
					data.Text = ""
					data.OutputKind = "image"
					data.Metadata["answer_kind"] = "generated_image"
					data.Attachments = []ConversationAttachment{{
						Kind:   "image",
						Alt:    "Generated image",
						Source: "headed_browser_rendered",
						Width:  rendered.GeneratedImageWidth,
						Height: rendered.GeneratedImageHeight,
					}}
				}
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
			if imageGenerationPending(
				data.Tool,
				rendered,
				renderedPromptMatched,
				textAnswerReady,
			) {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "incomplete"
				data.ReadMode = browserReadMode
				data.Metadata["answer_source"] =
					"same_target_rendered_generated_image_pending"
				data.Metadata["generated_image_pending"] = true
				return finishAsk(
					ctx, lease,
					runID, config, webagent.StateIncomplete,
					target, pending, action, conversation, data,
					[]string{
						conversationAwaitCommand(conversationID),
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
						conversationAwaitCommand(conversationID),
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
				newConversationDetailData(
					conversationID,
					browserReadMode,
					"same_target_browser_fetch",
				),
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
			storedAnswerReady := len(stored.Attachments) > 0 ||
				(!isImageTool(data.Tool) && strings.TrimSpace(stored.Text) != "")
			if stored.CompletionState == "terminal" && storedAnswerReady {
				data.Text = stored.Text
				data.Attachments = stored.Attachments
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
					conversationAwaitCommand(conversationID),
				},
			)
		},
	)
}

func observeComposer(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
	expectedThinking string,
	observation *composerObservation,
) error {
	return observeComposerWithTool(
		ctx,
		session,
		prompt,
		"",
		expectedThinking,
		observation,
	)
}

func observeComposerWithTool(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
	expectedTool string,
	expectedThinking string,
	observation *composerObservation,
) error {
	promptJSON, err := json.Marshal(prompt)
	if err != nil {
		return fmt.Errorf("encode ChatGPT prompt verification")
	}
	thinkingJSON, err := json.Marshal(expectedThinking)
	if err != nil {
		return fmt.Errorf("encode ChatGPT thinking verification")
	}
	toolJSON, err := json.Marshal(expectedTool)
	if err != nil {
		return fmt.Errorf("encode ChatGPT tool verification")
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  const expectedTool = %s;
	  const expectedThinking = %s;
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
	  const allEditors = Array.from(document.querySelectorAll(
	    '#prompt-textarea,[contenteditable="true"][role="textbox"]'
	  )).filter((element, index, values) =>
	    values.indexOf(element) === index && visible(element) && element.isContentEditable
	  );
	  const editors = expectedTool ? allEditors.filter(editor =>
	    Array.from(editor.querySelectorAll(
	      '[data-inline-selection-pill][data-keyword]'
	    )).some(pill => label(pill).toLowerCase() === expectedTool.toLowerCase())
	  ) : allEditors;
	  const editor = editors.length === 1 ? editors[0] : null;
	  const inlineText = node => {
	    if (node.nodeType === Node.TEXT_NODE) {
	      return node.nodeValue || '';
	    }
	    if (!(node instanceof HTMLElement)) return '';
	    if (node.matches(
	      '[data-inline-selection-pill],[data-inline-selection-pill-cursor-target]'
	    )) return '';
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
	  const withoutToolPill = node => {
	    if (node.nodeType === Node.TEXT_NODE) return node.nodeValue || '';
	    if (!(node instanceof HTMLElement)) return '';
	    if (node.matches(
	      '[data-inline-selection-pill],[data-inline-selection-pill-cursor-target]'
	    )) return '';
	    if (node.tagName === 'BR') return '\n';
	    return Array.from(node.childNodes).map(withoutToolPill).join('');
	  };
	  const toolFreeEditorText = editor ? normalize(
	    Array.from(editor.childNodes).map(withoutToolPill).join('\n')
	  ).replace(/^\s+|\s+$/g, '') : '';
	  const editorPromptText = expectedTool ? toolFreeEditorText : canonicalEditorText;
	  const selectedTools = editor ? Array.from(editor.querySelectorAll(
	    '[data-inline-selection-pill][data-keyword]'
	  )).filter(visible).map(pill => String(
	    pill.getAttribute('data-keyword') || label(pill)
	  ).trim()).filter(Boolean) : [];
	  const radios = Array.from(document.querySelectorAll(
	    'button[role="radio"]'
	  )).filter(visible);
	  const chats = radios.filter(button => label(button) === 'Chat');
	  const works = radios.filter(button => label(button) === 'Work');
	  const knownThinking = [
	    'Instant', 'Instant 5.5', 'Medium', 'High',
	    'Extra High', 'Pro'
	  ];
	  const intelligence = Array.from(document.querySelectorAll(
	    'button[aria-haspopup="menu"]'
	  )).filter(button =>
	    visible(button) && (
	      expectedThinking ?
	        label(button).toLowerCase() === expectedThinking.toLowerCase() :
	        knownThinking.some(item =>
	          item.toLowerCase() === label(button).toLowerCase()
	        ) || button.classList.contains('__composer-pill')
	    )
	  );
	  const sends = Array.from(document.querySelectorAll(
	    'button[data-testid="send-button"],button#composer-submit-button,' +
	    'button[aria-label="Send prompt"]'
	  )).filter((button, index, values) =>
	    values.indexOf(button) === index && visible(button)
	  );
	  const send = sends.length === 1 ? sends[0] : null;
	  const sendAction = actionable(send);
	  const route = location.pathname.match(/^\/c\/([A-Za-z0-9_-]+)$/);
	  const assistantTurns = Array.from(document.querySelectorAll(
	    'section[data-turn="assistant"],[data-turn="assistant"]'
	  ));
	  const assistantMessages = Array.from(document.querySelectorAll(
	    '[data-message-author-role="assistant"]'
	  )).filter((element, index, values) =>
	    values.indexOf(element) === index
	  );
	  const assistants = assistantTurns.length ?
	    assistantTurns : assistantMessages;
	  const userTurns = Array.from(document.querySelectorAll(
	    'section[data-turn="user"],[data-turn="user"]'
	  ));
	  const userMessages = Array.from(document.querySelectorAll(
	    '[data-message-author-role="user"]'
	  )).filter((element, index, values) =>
	    values.indexOf(element) === index
	  );
	  const users = userTurns.length ? userTurns : userMessages;
	  const specialized = Array.from(document.querySelectorAll(
	    'iframe[src*="deep-research"],iframe[src*="connector_openai_deep_research"],' +
	    '[data-testid*="deep-research"][aria-pressed="true"],' +
	    '[data-testid*="agent"][aria-pressed="true"]'
	  ));
	  const editorInnerText = editor ? (expectedTool ? toolFreeEditorText : normalize(editor.innerText || '')) : '';
	  const editorTextContent = editor ? (expectedTool ? toolFreeEditorText : normalize(editor.textContent || '')) : '';
	  const expectedPrompt = expectedTool ? normalize(expected).replace(/^\s+|\s+$/g, '') : normalize(expected);
	  return {
	    route_ready: location.origin === 'https://chatgpt.com' &&
	      location.pathname === '/',
	    editor_ready: Boolean(editor),
	    editor_count: editors.length,
	    prompt_matches: Boolean(editor) &&
	      editorPromptText === expectedPrompt,
	    inner_text_matches: Boolean(editor) &&
	      editorInnerText === expectedPrompt,
	    text_content_matches: Boolean(editor) &&
	      editorTextContent === expectedPrompt,
	    canonical_matches: Boolean(editor) &&
	      editorPromptText === expectedPrompt,
	    expected_characters: expectedPrompt.length,
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
	    specialized_surface_count: specialized.length,
	    tool_count: selectedTools.length,
	    selected_tool: selectedTools.length === 1 ? selectedTools[0] : ''
	  };
	})()`, promptJSON, toolJSON, thinkingJSON)
	return evaluateInto(ctx, session, expression, observation)
}

func prepareExactPrompt(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
) error {
	return prepareExactPromptWithTool(ctx, session, prompt, "")
}

func prepareExactPromptWithTool(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
	tool string,
) error {
	activationKind := "editor"
	activationLabel := ""
	if tool != "" {
		activationKind = "editor-tool"
		activationLabel = toolDisplayLabel(tool)
	}
	if err := activateSelectionControl(
		ctx,
		session,
		activationKind,
		activationLabel,
	); err != nil {
		return fmt.Errorf("activate exact ChatGPT composer before text input: %w", err)
	}
	var selected struct {
		OK bool `json:"ok"`
	}
	toolLabelJSON, err := json.Marshal(toolDisplayLabel(tool))
	if err != nil {
		return fmt.Errorf("encode ChatGPT tool label")
	}
	expression := fmt.Sprintf(`(() => {
	  const editors = Array.from(document.querySelectorAll(
	    '#prompt-textarea,[contenteditable="true"][role="textbox"]'
	  )).filter((element, index, values) =>
	    values.indexOf(element) === index && element.isContentEditable &&
	    (%s === '' || Array.from(element.querySelectorAll(
	      '[data-inline-selection-pill][data-keyword]'
	    )).some(pill => String(
	      pill.getAttribute('data-keyword') || pill.innerText || pill.textContent || ''
	    ).trim().toLowerCase() === %s.toLowerCase()))
	  );
	  if (editors.length !== 1) return {ok: false};
	  const editor = editors[0];
	  editor.focus();
	  const selection = window.getSelection();
	  if (!selection) return {ok: false};
	  const range = document.createRange();
	  range.selectNodeContents(editor);
	  if (%s !== '') range.collapse(false);
	  selection.removeAllRanges();
	  selection.addRange(range);
	  return {ok: document.activeElement === editor};
	})()`, toolLabelJSON, toolLabelJSON, toolLabelJSON)
	if err := evaluateInto(ctx, session, expression, &selected); err != nil || !selected.OK {
		return fmt.Errorf("select exact ChatGPT composer")
	}
	return browserflow.InsertText(ctx, session, prompt)
}

func observeComposerForTool(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
	tool string,
	intelligence string,
	observation *composerObservation,
) error {
	if tool == "" {
		return observeComposer(ctx, session, prompt, intelligence, observation)
	}
	return observeComposerWithTool(
		ctx,
		session,
		prompt,
		toolDisplayLabel(tool),
		intelligence,
		observation,
	)
}

func composerPreparedForTool(
	observation composerObservation,
	intelligence string,
	tool string,
) bool {
	if !composerPreparedExceptSend(observation, intelligence) {
		return false
	}
	return tool == "" || strings.EqualFold(
		observation.SelectedTool,
		toolDisplayLabel(tool),
	)
}

func composerReadyForTool(
	observation composerObservation,
	intelligence string,
	tool string,
) bool {
	return composerPreparedForTool(observation, intelligence, tool) &&
		observation.SendReady
}

func prepareVerifiedPrompt(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
	intelligence string,
	model string,
	attachment *attachmentExpectation,
	timeout time.Duration,
	poll time.Duration,
) (int, composerObservation, error) {
	return prepareVerifiedPromptWithTool(
		ctx,
		session,
		prompt,
		intelligence,
		model,
		"",
		attachment,
		timeout,
		poll,
	)
}

func prepareVerifiedPromptWithTool(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
	intelligence string,
	model string,
	tool string,
	attachment *attachmentExpectation,
	timeout time.Duration,
	poll time.Duration,
) (int, composerObservation, error) {
	deadline := time.Now().Add(timeout)
	var observation composerObservation
	var attachmentState attachmentObservation
	var lastErr error
	attempts := 0
	// File processing and paid-mode composer rerenders are nondeterministic.
	// Keep retrying reversible preparation until the caller's bounded composer
	// deadline instead of closing the exact target after a fixed small count.
	for attempt := 1; ; attempt++ {
		attempts = attempt
		if err := prepareExactPromptWithTool(ctx, session, prompt, tool); err != nil {
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
		} else if err := observeExpectedAttachment(
			ctx,
			session,
			attachment,
			&attachmentState,
		); err != nil {
			lastErr = err
		} else if !attachmentState.OK {
			lastErr = fmt.Errorf(
				"exact ChatGPT attachment changed before Send",
			)
		} else if err := observeComposerForTool(
			ctx,
			session,
			prompt,
			tool,
			intelligence,
			&observation,
		); err != nil {
			lastErr = err
		} else if composerReadyForTool(observation, intelligence, tool) {
			return attempt, observation, nil
		} else if composerPreparedForTool(observation, intelligence, tool) {
			waitAttempts, waitErr := pollUntil(
				ctx,
				time.Until(deadline),
				poll,
				func() (bool, error) {
					if err := observeComposerForTool(
						ctx,
						session,
						prompt,
						tool,
						intelligence,
						&observation,
					); err != nil {
						return false, err
					}
					if err := observeExpectedAttachment(
						ctx,
						session,
						attachment,
						&attachmentState,
					); err != nil {
						return false, err
					}
					return composerReadyForTool(
						observation,
						intelligence,
						tool,
					) && attachmentState.OK, nil
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
				} else if err := observeComposerForTool(
					ctx,
					session,
					prompt,
					tool,
					intelligence,
					&observation,
				); err != nil {
					lastErr = fmt.Errorf(
						"ChatGPT composer changed after final selection verification",
					)
				} else if err := observeExpectedAttachment(
					ctx,
					session,
					attachment,
					&attachmentState,
				); err != nil ||
					!attachmentState.OK ||
					!composerReadyForTool(observation, intelligence, tool) {
					lastErr = fmt.Errorf(
						"ChatGPT composer or attachment changed after final selection verification",
					)
				} else {
					return attempts, observation, nil
				}
			} else {
				lastErr = waitErr
			}
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

func composerPreparedExceptSend(
	observation composerObservation,
	intelligence string,
) bool {
	return observation.RouteReady &&
		observation.PromptMatches &&
		observation.ChatCount == 1 &&
		observation.WorkCount == 1 &&
		observation.ChatSelected &&
		observation.IntelligenceCount == 1 &&
		strings.EqualFold(
			observation.SelectedIntelligence,
			intelligence,
		) &&
		observation.SendCount == 1 &&
		observation.AssistantCount == 0 &&
		observation.UserMessageCount == 0 &&
		observation.ConversationID == ""
}

func composerReadyForSend(
	observation composerObservation,
	intelligence string,
) bool {
	return composerPreparedExceptSend(observation, intelligence) &&
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
	  const assistantTurns = unique(Array.from(document.querySelectorAll(
	    'section[data-turn="assistant"],[data-turn="assistant"]'
	  )));
	  const assistantMessages = unique(Array.from(document.querySelectorAll(
	    '[data-message-author-role="assistant"]'
	  )));
	  const assistants = assistantTurns.length ?
	    assistantTurns : assistantMessages;
	  const userTurns = unique(Array.from(document.querySelectorAll(
	    'section[data-turn="user"],[data-turn="user"]'
	  )));
	  const userMessages = unique(Array.from(document.querySelectorAll(
	    '[data-message-author-role="user"]'
	  )));
	  const users = userTurns.length ? userTurns : userMessages;
	  const imageAssistantTurn = [...assistants].reverse().find(turn =>
	    turn.querySelector('img[alt^="Generated image"]')
	  );
	  const assistantTurn = imageAssistantTurn || (assistants.length ?
	    assistants[assistants.length - 1] : null);
	  const userTurn = users.length ? users[users.length - 1] : null;
	  const assistant = assistantTurn && (
	    assistantTurn.matches('[data-message-author-role="assistant"]') ?
	      assistantTurn :
	      assistantTurn.querySelector(
	        '[data-message-author-role="assistant"],.markdown'
	      ) || assistantTurn
	  );
	  const user = userTurn && (
	    userTurn.matches('[data-message-author-role="user"]') ?
	      userTurn :
	      userTurn.querySelector('[data-message-author-role="user"]') ||
	        userTurn
	  );
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
	    if (node.matches(
	      '[data-inline-selection-pill],[data-inline-selection-pill-cursor-target]'
	    )) return '';
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
	    const value = blockStructured ?
	      children.map(blockText).join('\n') :
	      inlineText(element);
	    const hasSelectionPill = Boolean(element.querySelector(
	      '[data-inline-selection-pill],[data-inline-selection-pill-cursor-target]'
	    ));
	    return hasSelectionPill ? value.replace(/^\s+/, '') : value;
	  };
	  const promptCandidates = unique([
	    user && user.innerText,
	    user && user.textContent,
	    promptNode && promptNode.innerText,
	    promptNode && promptNode.textContent,
	    canonical(promptNode),
	  ].map(trimTrailingNewlines).filter(Boolean));
	  const turn = assistantTurn || assistant && assistant.closest(
	    'section[data-turn="assistant"],[data-turn="assistant"]'
	  );
	  const generatedImages = assistantTurn ? unique(Array.from(
	    assistantTurn.querySelectorAll('img[alt^="Generated image"]')
	  ).filter(visible)) : [];
	  const readyImage = generatedImages.find(image =>
	    image.complete && image.naturalWidth > 0
	  );
	  const generatedImageReady = Boolean(readyImage);
	  const streamingState = turn ? Array.from(turn.querySelectorAll(
	    '[data-is-streaming="true"],[aria-busy="true"]'
	  )).some(visible) : false;
	  const streamingControl = Array.from(document.querySelectorAll(
	    'button[data-testid="stop-button"],button[aria-label*="Stop"],' +
	    'button[aria-label*="stop"]'
	  )).some(visible);
	  const normalizeMarker = value => String(value || '')
	    .replace(/\s+/g, ' ')
	    .trim()
	    .toLowerCase();
	  const rawAssistantText = assistant ?
	    String(assistant.innerText || assistant.textContent || '').trim() : '';
	  const substantiveAssistantText =
	    normalizeMarker(rawAssistantText) === 'stopped thinking' ?
	      '' : rawAssistantText;
	  const terminalControl = Boolean(turn && Array.from(turn.querySelectorAll(
	    'button[data-testid="copy-turn-action-button"],' +
	    'button[aria-label^="Copy"],button[aria-label="Regenerate response"]'
	  )).some(visible)) ||
	    (substantiveAssistantText.length >= 40 &&
	      !streamingState && !streamingControl);
	  const stoppedThinkingMarker = Boolean(turn && Array.from(
	    turn.querySelectorAll('*')
	  ).some(node =>
	    visible(node) &&
	    normalizeMarker(node.innerText || node.textContent) ===
	      'stopped thinking'
	  ));
	  const terminalNoAnswer =
	    stoppedThinkingMarker &&
	    !streamingState &&
	    !streamingControl &&
	    substantiveAssistantText === '';
	  const match = location.pathname.match(/^\/c\/([A-Za-z0-9_-]+)$/);
	  return {
	    route_matches: Boolean(match),
	    conversation_id: match ? match[1] : '',
	    text: substantiveAssistantText,
	    prompt_candidates: promptCandidates,
	    is_streaming: streamingState || streamingControl,
	    terminal_control_present: terminalControl,
	    assistant_count: assistants.length,
	    user_message_count: users.length,
	    stopped_thinking_marker_present: stoppedThinkingMarker,
	    terminal_no_answer: terminalNoAnswer,
	    terminal_no_answer_reason: terminalNoAnswer ?
      'stopped_thinking' : '',
	    generated_image_count: generatedImages.length,
	    generated_image_ready: generatedImageReady,
	    generated_image_width: readyImage ?
	      Number(readyImage.naturalWidth || readyImage.width || 0) : 0,
	    generated_image_height: readyImage ?
	      Number(readyImage.naturalHeight || readyImage.height || 0) : 0
	  };
	})()`, observation)
}

type answerNowObservation struct {
	Count              int     `json:"count"`
	X                  float64 `json:"x"`
	Y                  float64 `json:"y"`
	TerminalControl    bool    `json:"terminal_control"`
	AssistantTextReady bool    `json:"assistant_text_ready"`
}

// advanceChatGPTAnswerNowGate handles the provider-side continuation that can
// appear after a tool has gathered sources or prepared an output. It is not a second Send:
// the prompt is already acknowledged, and this exact button is clicked at
// most once. If the provider answers without showing the gate, the normal
// rendered-answer reader continues unchanged.
func advanceChatGPTAnswerNowGate(
	ctx context.Context,
	session *cdp.PageSession,
	deadline time.Time,
	poll time.Duration,
) (bool, int, error) {
	attempts := 0
	for time.Now().Before(deadline) {
		attempts++
		var gate answerNowObservation
		if err := observeAnswerNowGate(ctx, session, &gate); err != nil {
			if ctx.Err() != nil {
				return false, attempts, ctx.Err()
			}
			if !waitForObservation(ctx, poll, time.Until(deadline)) {
				break
			}
			continue
		}
		if gate.Count > 1 {
			return false, attempts, fmt.Errorf(
				"ChatGPT exposed multiple Answer now controls",
			)
		}
		if gate.Count == 1 {
			outcome, err := browserflow.ClickPoint(ctx, session, gate.X, gate.Y)
			if err != nil {
				return false, attempts, fmt.Errorf(
					"click ChatGPT Answer now control: %w", err,
				)
			}
			if outcome.Dispatch != browserflow.DispatchPerformed {
				return false, attempts, fmt.Errorf(
					"ChatGPT Answer now control click was not performed",
				)
			}
			return true, attempts, nil
		}
		if gate.TerminalControl || gate.AssistantTextReady {
			return false, attempts, nil
		}
		if !waitForObservation(ctx, poll, time.Until(deadline)) {
			break
		}
	}
	return false, attempts, nil
}

func observeAnswerNowGate(
	ctx context.Context,
	session *cdp.PageSession,
	observation *answerNowObservation,
) error {
	expression := `(() => {
  const normalize = value => String(value || '').replace(/\s+/g, ' ').trim();
  const visible = element => {
    if (!(element instanceof HTMLElement)) return false;
    const rect = element.getBoundingClientRect();
    const style = getComputedStyle(element);
    return rect.width > 0 && rect.height > 0 &&
      style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || '1') !== 0 && !element.disabled;
  };
  const buttons = Array.from(document.querySelectorAll('button')).filter(
    button => visible(button) && normalize(button.innerText || button.textContent) === 'Answer now'
  );
  const turns = Array.from(document.querySelectorAll(
    'section[data-turn="assistant"],[data-message-author-role="assistant"]'
  ));
  const turn = turns.length ? turns[turns.length - 1] : null;
  const text = normalize(turn && (turn.innerText || turn.textContent));
  const terminalButton = Boolean(turn && Array.from(turn.querySelectorAll(
	    'button[data-testid="copy-turn-action-button"],button[aria-label^="Copy"],button[aria-label="Regenerate response"]'
  )).some(visible));
  const streaming = Array.from(document.querySelectorAll(
    'button[data-testid="stop-button"],button[aria-label*="Stop"],button[aria-label*="stop"]'
  )).some(visible);
  const terminal = terminalButton || (text.length >= 40 && !streaming);
  const ready = text.length >= 40 && terminal;
  const button = buttons.length === 1 ? buttons[0] : null;
  const rect = button ? button.getBoundingClientRect() : null;
  return {
    count: buttons.length,
    x: rect ? rect.left + rect.width / 2 : 0,
    y: rect ? rect.top + rect.height / 2 : 0,
    terminal_control: terminal,
    assistant_text_ready: ready
  };

})()`
	return evaluateInto(ctx, session, expression, observation)
}

func waitRenderedAnswer(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	prompt string,
	tool string,
	deadline time.Time,
	poll time.Duration,
) (renderedObservation, int, int) {
	var observation renderedObservation
	lastResult := ""
	stable := 0
	attempts := 0
	imagePendingReads := 0
	imageRecoveryAttempted := false
	for time.Now().Before(deadline) {
		attempts++
		current := renderedObservation{}
		if err := observeRendered(ctx, session, &current); err == nil {
			if imagePlaceholderNeedsRecovery(tool, current) {
				imagePendingReads++
			} else {
				imagePendingReads = 0
			}
			if imagePendingReads >= 2 && !imageRecoveryAttempted {
				imageRecoveryAttempted = true
				if err := recoverImageNavigation(
					ctx,
					session,
					conversationID,
				); err == nil {
					lastResult = ""
					stable = 0
					if !waitForObservation(
						ctx,
						poll,
						time.Until(deadline),
					) {
						break
					}
					continue
				}
			}
			current.ImageRecoveryAttempted = imageRecoveryAttempted
			observation = current
			text := strings.TrimSpace(observation.Text)
			promptMatched := renderedPromptMatches(
				observation,
				fingerprintPrompt(prompt),
			)
			valid := observation.RouteMatches &&
				observation.ConversationID == conversationID &&
				observation.UserMessageCount == 1 &&
				promptMatched &&
				!observation.Streaming &&
				((!isImageTool(tool) &&
					len(text) >= minimumUsefulAnswerChars(prompt) &&
					terminalAnswerTextValid(text, map[string]any{})) ||
					(isImageTool(tool) && observation.GeneratedImageReady))
			if valid {
				resultKey := text
				if isImageTool(tool) && observation.GeneratedImageReady {
					resultKey = fmt.Sprintf(
						"image:%d",
						observation.GeneratedImageCount,
					)
				}
				if resultKey == lastResult {
					stable++
				} else {
					lastResult = resultKey
					stable = 1
				}
				if observation.TerminalControl ||
					(isImageTool(tool) && stable >= 2) {
					return observation, stable, attempts
				}
			} else {
				lastResult = ""
				stable = 0
			}
		}
		if !waitForObservation(ctx, poll, time.Until(deadline)) {
			break
		}
	}
	current := renderedObservation{}
	if err := observeRendered(ctx, session, &current); err == nil {
		current.ImageRecoveryAttempted = imageRecoveryAttempted
		observation = current
	}
	return observation, stable, attempts
}

func recoverImageNavigation(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
) error {
	if _, err := session.Navigate(ctx, "about:blank"); err != nil {
		return fmt.Errorf("navigate to image recovery blank page: %w", err)
	}
	conversationURL := Origin + "/c/" + url.PathEscape(conversationID)
	if _, err := session.Navigate(ctx, conversationURL); err != nil {
		return fmt.Errorf("navigate back to image conversation: %w", err)
	}
	return nil
}

func imagePlaceholderNeedsRecovery(
	tool string,
	observation renderedObservation,
) bool {
	return isImageTool(tool) &&
		observation.RouteMatches &&
		!observation.Streaming &&
		observation.GeneratedImageCount > 0 &&
		!observation.GeneratedImageReady
}

func imageGenerationPending(
	tool string,
	observation renderedObservation,
	promptMatched bool,
	textAnswerReady bool,
) bool {
	return isImageTool(tool) &&
		promptMatched &&
		observation.RouteMatches &&
		observation.UserMessageCount == 1 &&
		!observation.Streaming &&
		!textAnswerReady &&
		!observation.GeneratedImageReady
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
	data := newConversationDetailData(
		conversationID,
		browserReadMode,
		"same_target_browser_fetch",
	)
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
		return data, failure
	}
	var payload map[string]any
	if err := decodeBoundedJSON(
		strings.NewReader(response.Body),
		&payload,
	); err != nil {
		return data, &readFailure{
			code:       "chatgpt_invalid_detail_response",
			errClass:   "provider",
			message:    "ChatGPT hydrated conversation detail was invalid",
			statusCode: response.StatusCode,
		}
	}
	stored, parseFailure := parseConversationDetailPayload(
		data,
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
