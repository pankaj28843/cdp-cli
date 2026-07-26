package grok

import (
	"context"
	"encoding/json"
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
	AskSchemaVersion         = "grok-ask/v1"
	MaxPromptCharacters      = 18_000
	defaultAskTimeout        = 3 * time.Minute
	defaultComposerTimeout   = 30 * time.Second
	defaultAmbiguousCooldown = 5 * time.Minute
	renderedWaitFraction     = 0.85
)

type AskConfig struct {
	BrowserConfig
	Store           *Store
	HTTPClient      *http.Client
	Timeout         time.Duration
	ComposerTimeout time.Duration
	PollInterval    time.Duration
	Now             func() time.Time
	Send            browserflow.Dispatcher
}

type AskData struct {
	SchemaVersion      string         `json:"schema_version"`
	ConversationMode   string         `json:"conversation_mode"`
	Text               string         `json:"text"`
	CompletionState    string         `json:"completion_state"`
	ReadMode           string         `json:"read_mode"`
	ModeID             string         `json:"mode_id,omitempty"`
	ModeTitle          string         `json:"mode_title,omitempty"`
	PromptFingerprint  string         `json:"prompt_fingerprint,omitempty"`
	PromptCharacters   int            `json:"prompt_characters"`
	DetailReadAttempts int            `json:"detail_read_attempts"`
	Metadata           map[string]any `json:"metadata"`
}

type composerObservation struct {
	RouteReady     bool    `json:"route_ready"`
	EditorReady    bool    `json:"editor_ready"`
	EditorCount    int     `json:"editor_count"`
	PromptMatches  bool    `json:"prompt_matches"`
	ModeCount      int     `json:"mode_count"`
	ModeTitle      string  `json:"mode_title"`
	SubmitCount    int     `json:"submit_count"`
	SubmitReady    bool    `json:"submit_ready"`
	SubmitX        float64 `json:"submit_x"`
	SubmitY        float64 `json:"submit_y"`
	AssistantCount int     `json:"assistant_count"`
	ConversationID string  `json:"conversation_id"`
}

type askObservation struct {
	RouteMatches   bool   `json:"route_matches"`
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
	Prompt         string `json:"prompt"`
	Streaming      bool   `json:"is_streaming"`
	AnswerCount    int    `json:"answer_count"`
}

type grokSendDispatcher struct {
	prompt    string
	modeTitle string
}

func (d grokSendDispatcher) Dispatch(
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
		!observation.RouteReady ||
		!observation.EditorReady ||
		observation.EditorCount != 1 ||
		!observation.PromptMatches ||
		observation.ModeCount != 1 ||
		observation.ModeTitle != d.modeTitle ||
		observation.SubmitCount != 1 ||
		!observation.SubmitReady ||
		observation.AssistantCount != 0 ||
		observation.ConversationID != "" {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact Grok Send control was not actionable")
	}
	return browserflow.ClickPoint(
		ctx,
		session,
		observation.SubmitX,
		observation.SubmitY,
	)
}

func Ask(
	ctx context.Context,
	config AskConfig,
	prompt string,
) webagent.Result {
	prompt = strings.TrimSpace(prompt)
	runID := webagent.NewRunID()
	data := AskData{
		SchemaVersion:    AskSchemaVersion,
		ConversationMode: "fresh_only",
		CompletionState:  "not_submitted",
		ReadMode:         "not_started",
		PromptCharacters: utf8.RuneCountInString(prompt),
		Metadata:         map[string]any{},
	}
	notPerformed := notPerformedAction()
	if prompt == "" {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"grok_prompt_required", "usage",
			"Grok prompt must not be empty", "", data,
			[]string{"cdp workflow agent grok ask --stdin --json"},
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
			"grok_prompt_too_long", "usage",
			"Grok prompt exceeds the safe character limit", "", data,
			[]string{
				"Split the request into self-contained prompts below the limit.",
			},
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
			"grok_state_unavailable", "internal",
			"Grok owner-only state is unavailable", "", data,
			[]string{"cdp workflow agent grok doctor --json"},
		)
	}
	now := nowForAsk(config)
	auth := config.Store.AuthStatus(ctx, now, DefaultAuthTTL)
	if !auth.Ready {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"grok_auth_"+auth.State, "auth",
			"Grok auth evidence is not ready before Send", "", data,
			[]string{"cdp workflow agent grok auth refresh --json"},
		)
	}
	runtime := config.Store.RuntimeStatus(
		ctx,
		now,
		DefaultCapabilitiesTTL,
	)
	if !runtime.Ready {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"grok_runtime_capabilities_"+runtime.State, "capability",
			"Grok runtime capability evidence is not ready before Send", "", data,
			[]string{"cdp workflow agent grok capabilities refresh --json"},
		)
	}
	selected, ok := selectedRuntimeMode(runtime)
	if !ok {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"grok_default_mode_unavailable", "capability",
			"Grok cached default mode is missing, unavailable, or not selected",
			"", data,
			[]string{"cdp workflow agent grok capabilities refresh --json"},
		)
	}
	data.ModeID = selected.ID
	data.ModeTitle = selected.Title
	template, err := config.Store.LoadTemplate(ctx)
	if err != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"grok_auth_state_unreadable", "internal",
			"Grok owner-only request template could not be loaded before Send",
			"", data,
			[]string{"cdp workflow agent grok auth refresh --json"},
		)
	}

	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationAsk,
		"send",
		"about:blank",
		"headed_browser",
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
					"grok_composer_prepare_failed", "connection",
					"Grok composer could not be prepared on the exact headed target",
					"", data, cleanupCommands(runID, pending),
				)
			}
			var composer composerObservation
			composerAttempts, err := pollUntil(
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
					return composer.RouteReady &&
						composer.EditorReady &&
						composer.EditorCount == 1 &&
						composer.ModeCount == 1 &&
						composer.ModeTitle == selected.Title &&
						composer.AssistantCount == 0 &&
						composer.ConversationID == "", nil
				},
			)
			data.Metadata["composer_attempts"] = composerAttempts
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"grok_composer_not_ready", "provider",
					"Grok fresh composer and cached default mode did not become ready before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			verifyAttempts, composer, verifyErr := prepareVerifiedPrompt(
				ctx,
				session,
				prompt,
				selected.Title,
				config.ComposerTimeout,
				config.PollInterval,
			)
			data.Metadata["prompt_verify_attempts"] = verifyAttempts
			if verifyErr != nil {
				data.Metadata["observed_route_ready"] = composer.RouteReady
				data.Metadata["observed_editor_count"] = composer.EditorCount
				data.Metadata["observed_prompt_matches"] = composer.PromptMatches
				data.Metadata["observed_mode_count"] = composer.ModeCount
				data.Metadata["observed_mode_matches"] =
					composer.ModeTitle == selected.Title
				data.Metadata["observed_submit_count"] = composer.SubmitCount
				data.Metadata["observed_submit_ready"] = composer.SubmitReady
				data.Metadata["observed_assistant_count"] =
					composer.AssistantCount
				data.Metadata["observed_blank_conversation"] =
					composer.ConversationID == ""
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"grok_prompt_verify_failed", "provider",
					"Grok exact prompt, blank route, or cached default mode changed before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"grok_prompt_prepare_state_failed", "internal",
					"Grok prepared state could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			dispatcher := config.Send
			if dispatcher == nil {
				dispatcher = grokSendDispatcher{
					prompt:    prompt,
					modeTitle: selected.Title,
				}
			}
			outcome, _ := lease.Dispatch(ctx, dispatcher)
			action := actionEvidence(lease.Record())
			_ = lease.ReleaseInput()
			if outcome.Dispatch == browserflow.DispatchNotPerformed ||
				(outcome.Dispatch == "" &&
					lease.Record().RawInputCount == 0) {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StagePrepared, target, pending,
					action, nil,
					"grok_send_not_performed", "provider",
					"Grok Send was not performed; retrying the ask is safe",
					"", data, cleanupCommands(runID, pending),
				)
			}

			deadline := time.Now().Add(config.Timeout)
			var observation askObservation
			ackAttempts := 0
			for {
				ackAttempts++
				_ = observeAskState(ctx, session, &observation)
				if conversationIDPattern.MatchString(
					observation.ConversationID,
				) && observation.RouteMatches {
					break
				}
				remaining := time.Until(deadline)
				if remaining <= 0 ||
					!waitRendered(ctx, config.PollInterval, remaining) {
					break
				}
			}
			data.Metadata["acknowledgement_attempts"] = ackAttempts
			if !conversationIDPattern.MatchString(observation.ConversationID) ||
				!observation.RouteMatches {
				_ = lease.MarkIncomplete(context.Background())
				retryAt := retryAtFromRecord(
					lease.Record(),
					nowForAsk(config),
				)
				data.CompletionState = "submission_unacknowledged"
				return askFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, action, nil,
					"grok_submission_unacknowledged", "completion",
					"Grok Send was attempted but the exact conversation was not acknowledged; do not resubmit",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			conversationID := observation.ConversationID
			conversation := conversationRef(conversationID)
			if err := lease.Acknowledge(ctx, conversationID); err != nil {
				retryAt := retryAtFromRecord(
					lease.Record(),
					nowForAsk(config),
				)
				return askFailure(
					runID, config, webagent.StageActionDispatched,
					target, pending, action, conversation,
					"grok_acknowledgement_state_failed", "internal",
					"Grok conversation acknowledgement could not be persisted; do not resubmit",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			action = actionEvidence(lease.Record())

			renderedStable := 0
			lastRenderedText := ""
			detailAttempts := 0
			renderedDeadline := webagent.FractionalDeadline(
				time.Now(),
				deadline,
				renderedWaitFraction,
			)
			data.Metadata["rendered_wait_fraction"] =
				renderedWaitFraction
			for time.Now().Before(renderedDeadline) {
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
					time.Until(renderedDeadline),
				) {
					break
				}
			}
			data.DetailReadAttempts = detailAttempts
			data.Metadata["rendered_answer_count"] = observation.AnswerCount
			data.Metadata["rendered_terminal_stable_reads"] = renderedStable
			renderedPromptMatched := false
			if promptFingerprint := fingerprintPrompt(observation.Prompt); strings.TrimSpace(observation.Prompt) != "" {
				data.Metadata["rendered_prompt_fingerprint"] = promptFingerprint
				renderedPromptMatched =
					promptFingerprint == data.PromptFingerprint
				data.Metadata["rendered_prompt_identity_proved"] =
					renderedPromptMatched
				if !renderedPromptMatched {
					data.Metadata["rendered_prompt_diagnostics"] =
						promptIdentityDiagnostics(prompt, observation.Prompt)
				}
			}

			stored, storedFailure := fetchConversationDetail(
				ctx,
				ReadConfig{
					Store:       config.Store,
					HTTPClient:  config.HTTPClient,
					BuildCommit: config.BuildCommit,
					Now:         config.Now,
				},
				template,
				conversationID,
			)
			data.Metadata["stored_detail_attempts"] = 1
			if storedFailure == nil &&
				stored.CompletionState == "terminal" &&
				strings.TrimSpace(stored.Text) != "" {
				value, present :=
					stored.Metadata["prompt_fingerprint"].(string)
				if !present || strings.TrimSpace(stored.promptText) == "" {
					_ = lease.MarkIncomplete(context.Background())
					data.CompletionState = "prompt_identity_unavailable"
					return askFailure(
						runID, config, webagent.StageAcknowledged,
						target, pending, action, conversation,
						"grok_stored_prompt_identity_unavailable", "completion",
						"Grok stored terminal detail did not prove the exact prompt identity; do not resubmit",
						"", data,
						[]string{
							fmt.Sprintf(
								"cdp workflow agent grok conversations detail %s --json",
								conversationID,
							),
						},
					)
				}
				if value != data.PromptFingerprint {
					_ = lease.MarkIncomplete(context.Background())
					data.CompletionState = "prompt_identity_mismatch"
					data.Metadata["stored_prompt_diagnostics"] =
						promptIdentityDiagnostics(prompt, stored.promptText)
					return askFailure(
						runID, config, webagent.StageAcknowledged,
						target, pending, action, conversation,
						"grok_stored_prompt_identity_mismatch", "completion",
						"Grok stored terminal detail did not match the exact submitted prompt identity; do not resubmit",
						"", data,
						[]string{
							fmt.Sprintf(
								"cdp workflow agent grok conversations detail %s --json",
								conversationID,
							),
						},
					)
				}
				data.Text = stored.Text
				data.CompletionState = "terminal"
				data.ReadMode = stored.ReadMode
				data.Metadata["source"] =
					"headed_cdp_visible_submit_then_observed_stable_http"
				data.Metadata["formatting"] = "provider_stored_message"
				data.Metadata["model"] = stored.Metadata["model"]
				if err := lease.MarkTerminal(ctx); err != nil {
					return askFailure(
						runID, config, webagent.StageAcknowledged,
						target, pending, action, conversation,
						"grok_terminal_state_failed", "internal",
						"Grok terminal state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return operationSuccess(
					runID, config.BuildCommit, webagent.OperationAsk,
					webagent.StateTerminal, webagent.StageObserveTerminal,
					data.ReadMode, target, pending, action, conversation, data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent grok conversations detail %s --json",
							conversationID,
						),
						fmt.Sprintf(
							"cdp workflow agent grok conversations delete %s --json",
							conversationID,
						),
					},
				)
			}
			renderedTerminal := renderedStable >= 2 &&
				observation.RouteMatches &&
				observation.ConversationID == conversationID &&
				renderedPromptMatched &&
				!observation.Streaming &&
				strings.TrimSpace(observation.Text) != ""
			if renderedTerminal &&
				storedFailure != nil &&
				storedFailure.code == "grok_browser_context_required" {
				data.Text = strings.TrimSpace(observation.Text)
				data.CompletionState = "terminal"
				data.ReadMode = "headed_browser_fallback"
				data.Metadata["source"] =
					"headed_cdp_visible_submit_rendered_answer"
				data.Metadata["formatting"] = "rendered_visible_text"
				data.Metadata["stored_detail_state"] = "browser_context_required"
				if err := lease.MarkTerminal(ctx); err != nil {
					return askFailure(
						runID, config, webagent.StageAcknowledged,
						target, pending, action, conversation,
						"grok_terminal_state_failed", "internal",
						"Grok rendered fallback terminal state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return operationSuccess(
					runID, config.BuildCommit, webagent.OperationAsk,
					webagent.StateTerminal, webagent.StageObserveTerminal,
					data.ReadMode, target, pending, action, conversation, data,
					[]string{
						"cdp workflow agent grok auth refresh --json",
						fmt.Sprintf(
							"cdp workflow agent grok conversations detail %s --json",
							conversationID,
						),
					},
				)
			}
			if storedFailure != nil &&
				storedFailure.code != "grok_browser_context_required" {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "detail_unavailable"
				return askFailure(
					runID, config, webagent.StageAcknowledged,
					target, pending, action, conversation,
					storedFailure.code, storedFailure.errClass,
					"Grok acknowledged the request, but canonical stored detail was unavailable; do not resubmit",
					formatRetryAt(storedFailure.retryAt), data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent grok conversations detail %s --json",
							conversationID,
						),
					},
				)
			}
			_ = lease.MarkIncomplete(context.Background())
			data.CompletionState = "incomplete"
			data.ReadMode = "observed_stable_http"
			if stored.CompletionState != "" {
				data.CompletionState = stored.CompletionState
			}
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationAsk,
				webagent.StateIncomplete, webagent.StageObserveTerminal,
				data.ReadMode, target, pending, action, conversation, data,
				[]string{
					fmt.Sprintf(
						"cdp workflow agent grok conversations await %s --json",
						conversationID,
					),
				},
			)
		},
	)
}

func selectedRuntimeMode(status RuntimeStatus) (Mode, bool) {
	for _, mode := range status.Modes {
		if mode.ID == status.DefaultModeID &&
			mode.Available &&
			mode.Selected &&
			strings.TrimSpace(mode.Title) != "" {
			return mode, true
		}
	}
	return Mode{}, false
}

func observeComposer(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
	observation *composerObservation,
) error {
	promptJSON, err := json.Marshal(prompt)
	if err != nil {
		return fmt.Errorf("encode Grok prompt verification")
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const editors = Array.from(document.querySelectorAll(
	    '[role=textbox][aria-label="Ask Grok anything"]'
	  )).filter(visible);
	  const editor = editors.length === 1 ? editors[0] : null;
	  const modes = Array.from(document.querySelectorAll(
	    'button[aria-label="Model select"]'
	  )).filter(visible);
	  const mode = modes.length === 1 ? modes[0] : null;
	  const submits = Array.from(document.querySelectorAll(
	    'button[aria-label="Submit"]'
	  )).filter(visible);
	  const submit = submits.length === 1 ? submits[0] : null;
	  const rect = submit?.getBoundingClientRect();
	  const x = rect ? rect.left + rect.width / 2 : 0;
	  const y = rect ? rect.top + rect.height / 2 : 0;
	  const top = rect ? document.elementFromPoint(x, y) : null;
	  const submitReady = Boolean(
	    submit && top && (top === submit || submit.contains(top)) &&
	    !submit.hasAttribute('disabled') && submit.getAttribute('aria-disabled') !== 'true'
	  );
	  const editorText = editor?.children.length
	    ? Array.from(editor.children).map(node => node.textContent || '').join('\n')
	    : (editor?.innerText || editor?.textContent || '');
	  const match = location.pathname.match(/^\/c\/([A-Za-z0-9_-]+)$/);
	  return {
	    route_ready: location.origin === 'https://grok.com' &&
	      location.pathname === '/',
	    editor_ready: Boolean(editor),
	    editor_count: editors.length,
	    prompt_matches: Boolean(editor) && (editorText || '').trim() === expected,
	    mode_count: modes.length,
	    mode_title: (mode?.innerText || mode?.textContent || '').trim(),
	    submit_count: submits.length,
	    submit_ready: submitReady,
	    submit_x: x,
	    submit_y: y,
	    assistant_count: document.querySelectorAll(
	      '[data-testid="assistant-message"] .response-content-markdown,main .items-start .response-content-markdown'
	    ).length,
	    conversation_id: match ? match[1] : '',
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
	  const editor = document.querySelector(
	    '[role=textbox][aria-label="Ask Grok anything"]'
	  );
	  if (!editor) return {ok: false};
	  editor.focus();
	  const selection = window.getSelection();
	  const range = document.createRange();
	  range.selectNodeContents(editor);
	  selection.removeAllRanges();
	  selection.addRange(range);
	  return {ok: true};
	})()`, &selected); err != nil || !selected.OK {
		return fmt.Errorf("select exact Grok composer")
	}
	return browserflow.InsertText(ctx, session, prompt)
}

func prepareVerifiedPrompt(
	ctx context.Context,
	session *cdp.PageSession,
	prompt string,
	modeTitle string,
	timeout time.Duration,
	poll time.Duration,
) (int, composerObservation, error) {
	if timeout <= 0 {
		timeout = defaultComposerTimeout
	}
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
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
		} else if observation.RouteReady &&
			observation.PromptMatches &&
			observation.ModeTitle == modeTitle &&
			observation.ModeCount == 1 &&
			observation.SubmitCount == 1 &&
			observation.SubmitReady &&
			observation.AssistantCount == 0 &&
			observation.ConversationID == "" {
			return attempt, observation, nil
		} else {
			lastErr = fmt.Errorf(
				"exact Grok prompt and Submit control are not ready",
			)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 ||
			!waitRendered(ctx, minDuration(500*time.Millisecond, poll*2), remaining) {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf(
			"exact Grok prompt preparation attempts were exhausted",
		)
	}
	return attempts, observation, lastErr
}

func promptIdentityDiagnostics(
	expected string,
	observed string,
) map[string]any {
	expected = strings.TrimSpace(expected)
	observed = strings.TrimSpace(observed)
	expectedRunes := []rune(expected)
	observedRunes := []rune(observed)
	prefix := 0
	for prefix < len(expectedRunes) &&
		prefix < len(observedRunes) &&
		expectedRunes[prefix] == observedRunes[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(expectedRunes)-prefix &&
		suffix < len(observedRunes)-prefix &&
		expectedRunes[len(expectedRunes)-1-suffix] ==
			observedRunes[len(observedRunes)-1-suffix] {
		suffix++
	}
	return map[string]any{
		"expected_characters":       len(expectedRunes),
		"observed_characters":       len(observedRunes),
		"character_delta":           len(observedRunes) - len(expectedRunes),
		"expected_lines":            promptLineCount(expected),
		"observed_lines":            promptLineCount(observed),
		"common_prefix_characters":  prefix,
		"common_suffix_characters":  suffix,
		"line_endings_equivalent":   normalizePromptLineEndings(expected) == normalizePromptLineEndings(observed),
		"blank_lines_equivalent":    normalizePromptIdentity(expected) == normalizePromptIdentity(observed),
		"whitespace_equivalent":     normalizePromptWhitespace(expected) == normalizePromptWhitespace(observed),
		"trimmed_lines_equivalent":  normalizePromptLines(expected, false) == normalizePromptLines(observed, false),
		"nonempty_lines_equivalent": normalizePromptLines(expected, true) == normalizePromptLines(observed, true),
	}
}

func promptLineCount(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(normalizePromptLineEndings(value), "\n") + 1
}

func normalizePromptLineEndings(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func normalizePromptIdentity(value string) string {
	value = strings.TrimSpace(normalizePromptLineEndings(value))
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[index] = ""
		}
	}
	return strings.Join(lines, "\n")
}

func normalizePromptWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizePromptLines(value string, dropEmpty bool) string {
	lines := strings.Split(normalizePromptLineEndings(value), "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if dropEmpty && line == "" {
			continue
		}
		normalized = append(normalized, line)
	}
	return strings.Join(normalized, "\n")
}

func observeAskState(
	ctx context.Context,
	session *cdp.PageSession,
	observation *askObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
	  const match = location.pathname.match(/^\/c\/([A-Za-z0-9_-]+)$/);
	  const unique = nodes => [...new Set(nodes)];
	  let answers = unique(Array.from(document.querySelectorAll(
	    '[data-testid="assistant-message"] .response-content-markdown'
	  )));
	  if (!answers.length) {
	    answers = unique(Array.from(document.querySelectorAll(
	      'main .items-start .response-content-markdown'
	    )));
	  }
	  let prompts = unique(Array.from(document.querySelectorAll(
	    '[data-testid="user-message"] .response-content-markdown'
	  )));
	  if (!prompts.length) {
	    prompts = unique(Array.from(document.querySelectorAll(
	      'main .items-end .whitespace-pre-wrap'
	    )));
	  }
	  const streaming = Array.from(document.querySelectorAll('button')).some(button =>
	    /stop/i.test(button.getAttribute('aria-label') || button.innerText || '')
	  );
	  const answer = answers.at(-1);
	  const prompt = prompts.at(-1);
	  return {
	    route_matches: location.origin === 'https://grok.com' && Boolean(match),
	    conversation_id: match ? match[1] : '',
	    text: (answer?.innerText || answer?.textContent || '').trim(),
	    prompt: (prompt?.innerText || prompt?.textContent || '').trim(),
	    is_streaming: streaming,
	    answer_count: answers.length,
	  };
	})()`, observation)
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
	return operationFailure(
		runID, config.BuildCommit, webagent.OperationAsk,
		stage, "headed_browser", target, cleanup, action, conversation,
		code, errClass, message, retryAt, data, nextCommands,
	)
}

func retryAtFromRecord(
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

func nowForAsk(config AskConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}
