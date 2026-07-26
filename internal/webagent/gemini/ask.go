package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AskSchemaVersion         = "gemini-ask/v1"
	MaxPromptCharacters      = 18_000
	defaultAskTimeout        = 3 * time.Minute
	defaultComposerTimeout   = 30 * time.Second
	defaultAmbiguousCooldown = 5 * time.Minute
)

type AskConfig struct {
	BrowserConfig
	Store           *Store
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
	CurrentMode        string         `json:"current_mode,omitempty"`
	PromptFingerprint  string         `json:"prompt_fingerprint,omitempty"`
	PromptCharacters   int            `json:"prompt_characters"`
	DetailReadAttempts int            `json:"detail_read_attempts"`
	Metadata           map[string]any `json:"metadata"`
}

type composerObservation struct {
	RouteReady    bool   `json:"route_ready"`
	EditorReady   bool   `json:"editor_ready"`
	EditorCount   int    `json:"editor_count"`
	CurrentMode   string `json:"current_mode"`
	PickerCount   int    `json:"picker_count"`
	AnswerCount   int    `json:"answer_count"`
	PromptMatches bool   `json:"prompt_matches"`
}

func Ask(ctx context.Context, config AskConfig, prompt string) webagent.Result {
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
			"gemini_prompt_required", "usage",
			"Gemini prompt must not be empty", "", data,
			[]string{"cdp workflow agent gemini ask --stdin --json"},
		)
	}
	if data.PromptCharacters > MaxPromptCharacters {
		data.Metadata["max_prompt_characters"] = MaxPromptCharacters
		data.Metadata["excess_characters"] = data.PromptCharacters - MaxPromptCharacters
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"gemini_prompt_too_long", "usage",
			"Gemini prompt exceeds the safe character limit", "", data,
			[]string{"Split the request into self-contained prompts below the limit."},
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
			"gemini_state_unavailable", "internal",
			"Gemini owner-only state is unavailable", "", data,
			[]string{"cdp workflow agent gemini doctor --json"},
		)
	}
	now := nowForAsk(config)
	auth := config.Store.AuthStatus(ctx, now, DefaultAuthTTL)
	if !auth.Ready {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"gemini_auth_"+auth.State, "auth",
			"Gemini auth evidence is not ready before Send", "", data,
			[]string{"cdp workflow agent gemini auth refresh --json"},
		)
	}
	runtimeStatus := config.Store.RuntimeStatus(
		ctx,
		now,
		DefaultCapabilitiesTTL,
	)
	if !runtimeStatus.Ready {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"gemini_runtime_capabilities_"+runtimeStatus.State, "capability",
			"Gemini runtime capability evidence is not ready before Send", "", data,
			[]string{"cdp workflow agent gemini capabilities refresh --json"},
		)
	}
	expectedMode := strings.TrimSpace(runtimeStatus.CurrentMode)
	if expectedMode == "" {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"gemini_runtime_mode_missing", "capability",
			"Gemini cached runtime mode is missing before Send", "", data,
			[]string{"cdp workflow agent gemini capabilities refresh --json"},
		)
	}
	data.CurrentMode = expectedMode

	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationAsk,
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
					"gemini_composer_prepare_failed", "connection",
					"Gemini composer could not be prepared on the exact headed target",
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
						composer.PickerCount == 1 &&
						composer.AnswerCount == 0 &&
						composer.CurrentMode == expectedMode, nil
				},
			)
			data.Metadata["composer_attempts"] = composerAttempts
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"gemini_composer_not_ready", "provider",
					"Gemini fresh composer and cached runtime mode did not become ready before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := prepareExactPrompt(ctx, session, prompt); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"gemini_prompt_prepare_failed", "provider",
					"Gemini composer did not preserve the exact prompt before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := observeComposer(ctx, session, prompt, &composer); err != nil ||
				!composer.PromptMatches ||
				composer.CurrentMode != expectedMode ||
				!composer.RouteReady ||
				composer.AnswerCount != 0 {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"gemini_prompt_verify_failed", "provider",
					"Gemini exact prompt, blank route, or cached mode changed before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"gemini_prompt_prepare_state_failed", "internal",
					"Gemini prepared state could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			dispatcher := config.Send
			if dispatcher == nil {
				dispatcher = browserflow.DispatchFunc(browserflow.PressEnter)
			}
			outcome, _ := lease.Dispatch(ctx, dispatcher)
			action := actionEvidence(lease.Record())
			_ = lease.ReleaseInput()
			if outcome.Dispatch == browserflow.DispatchNotPerformed ||
				(outcome.Dispatch == "" && lease.Record().RawInputCount == 0) {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StagePrepared, target, pending,
					action, nil,
					"gemini_send_not_performed", "provider",
					"Gemini Send was not performed; retrying the ask is safe",
					"", data, cleanupCommands(runID, pending),
				)
			}

			deadline := time.Now().Add(config.Timeout)
			var observation detailObservation
			ackAttempts := 0
			for {
				ackAttempts++
				_ = observeAskState(ctx, session, &observation)
				if conversationIDPattern.MatchString(observation.ConversationID) &&
					observation.RouteMatches {
					break
				}
				remaining := time.Until(deadline)
				if remaining <= 0 || !waitRendered(ctx, config.PollInterval, remaining) {
					break
				}
			}
			data.Metadata["acknowledgement_attempts"] = ackAttempts
			if !conversationIDPattern.MatchString(observation.ConversationID) ||
				!observation.RouteMatches {
				_ = lease.MarkIncomplete(context.Background())
				retryAt := retryAtFromRecord(lease.Record(), nowForAsk(config))
				data.CompletionState = "submission_unacknowledged"
				return askFailure(
					runID, config, webagent.StageActionDispatched, target, pending,
					action, nil,
					"gemini_submission_unacknowledged", "completion",
					"Gemini Send was attempted but the exact conversation was not acknowledged; do not resubmit",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			conversationID := observation.ConversationID
			conversation := conversationRef(conversationID)
			if err := lease.Acknowledge(ctx, conversationID); err != nil {
				retryAt := retryAtFromRecord(lease.Record(), nowForAsk(config))
				return askFailure(
					runID, config, webagent.StageActionDispatched, target, pending,
					action, conversation,
					"gemini_acknowledgement_state_failed", "internal",
					"Gemini conversation acknowledgement could not be persisted; do not resubmit",
					retryAt.Format(time.RFC3339Nano), data,
					cleanupCommands(runID, pending),
				)
			}
			action = actionEvidence(lease.Record())

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
				remaining := time.Until(deadline)
				if remaining <= 0 || !waitRendered(ctx, config.PollInterval, remaining) {
					break
				}
			}
			data.DetailReadAttempts = detailAttempts
			data.ReadMode = "headed_browser"
			data.Metadata["source"] = "headed-cdp-visible-submit-rendered-answer"
			data.Metadata["answer_count"] = observation.AnswerCount
			var promptCapture promptCaptureObservation
			captureErr := captureExactRenderedPrompt(ctx, session, &promptCapture)
			data.Metadata["prompt_query_count"] = promptCapture.QueryCount
			data.Metadata["prompt_copy_button_count"] = promptCapture.CopyButtonCount
			data.Metadata["prompt_clipboard_intercepted"] = promptCapture.ClipboardIntercepted
			renderedFingerprint := exactCapturedPromptFingerprint(&promptCapture)
			if captureErr != nil || renderedFingerprint == "" {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "prompt_identity_unavailable"
				return askFailure(
					runID, config, webagent.StageAcknowledged, target, pending,
					action, conversation,
					"gemini_prompt_identity_unavailable", "completion",
					"Gemini acknowledged a conversation without an exact copied prompt identity; do not resubmit",
					"", data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent gemini conversations detail %s --json",
							conversationID,
						),
					},
				)
			}
			data.Metadata["rendered_prompt_fingerprint"] = renderedFingerprint
			data.Metadata["prompt_capture_source"] = "intercepted_copy_prompt"
			if renderedFingerprint != data.PromptFingerprint {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "prompt_identity_mismatch"
				return askFailure(
					runID, config, webagent.StageAcknowledged, target, pending,
					action, conversation,
					"gemini_prompt_identity_mismatch", "completion",
					"Gemini acknowledged a conversation whose exact copied prompt identity did not match; do not resubmit",
					"", data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent gemini conversations detail %s --json",
							conversationID,
						),
					},
				)
			}
			terminal := observation.RouteMatches &&
				observation.ConversationID == conversationID &&
				observation.AnswerCount > 0 &&
				strings.TrimSpace(observation.Text) != "" &&
				!observation.Streaming
			if terminal {
				data.Text = strings.TrimSpace(observation.Text)
				data.CompletionState = "terminal"
				if err := lease.MarkTerminal(ctx); err != nil {
					return askFailure(
						runID, config, webagent.StageAcknowledged, target, pending,
						action, conversation,
						"gemini_terminal_state_failed", "internal",
						"Gemini terminal state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return operationSuccess(
					runID, config.BuildCommit, webagent.OperationAsk,
					webagent.StateTerminal, webagent.StageObserveTerminal,
					"headed_browser", target, pending, action, conversation, data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent gemini conversations detail %s --json",
							conversationID,
						),
						fmt.Sprintf(
							"cdp workflow agent gemini conversations delete %s --json",
							conversationID,
						),
					},
				)
			}
			_ = lease.MarkIncomplete(context.Background())
			data.CompletionState = "incomplete"
			data.Metadata["is_streaming"] = observation.Streaming
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationAsk,
				webagent.StateIncomplete, webagent.StageObserveTerminal,
				"headed_browser", target, pending, action, conversation, data,
				[]string{
					fmt.Sprintf(
						"cdp workflow agent gemini conversations await %s --json",
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
		return fmt.Errorf("encode Gemini prompt verification")
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
	  const editors = Array.from(
	    document.querySelectorAll('[role=textbox][contenteditable=true]')
	  ).filter(visible);
	  const editor = editors.length === 1 ? editors[0] : null;
	  const pickers = Array.from(document.querySelectorAll('button')).filter(button =>
	    visible(button) &&
	    (button.getAttribute('aria-label') || '').startsWith(
	      'Open mode picker, currently '
	    )
	  );
	  const aria = pickers.length === 1
	    ? pickers[0].getAttribute('aria-label') || ''
	    : '';
	  const editorText = editor?.children.length
	    ? Array.from(editor.children).map(node => node.textContent || '').join('\n')
	    : (editor?.innerText || editor?.textContent || '');
	  return {
	    route_ready: location.origin === 'https://gemini.google.com' &&
	      location.pathname === '/app',
	    editor_ready: Boolean(editor),
	    editor_count: editors.length,
	    current_mode: aria.split('currently ')[1] || '',
	    picker_count: pickers.length,
	    answer_count: document.querySelectorAll(
	      'model-response message-content'
	    ).length,
	    prompt_matches: Boolean(editor) && (editorText || '').trim() === expected
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
	  const editor = document.querySelector('[role=textbox][contenteditable=true]');
	  if (!editor) return {ok: false};
	  editor.focus();
	  const selection = window.getSelection();
	  const range = document.createRange();
	  range.selectNodeContents(editor);
	  selection.removeAllRanges();
	  selection.addRange(range);
	  return {ok: true};
	})()`, &selected); err != nil || !selected.OK {
		return fmt.Errorf("select exact Gemini composer")
	}
	return browserflow.InsertText(ctx, session, prompt)
}

func observeAskState(
	ctx context.Context,
	session *cdp.PageSession,
	observation *detailObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
	  const match = location.pathname.match(/^\/app\/([A-Za-z0-9_-]{16})$/);
	  const answers = Array.from(
	    document.querySelectorAll('model-response message-content')
	  ).map(element =>
	    (element.innerText || element.textContent || '').trim()
	  ).filter(Boolean);
	  const streaming = Array.from(document.querySelectorAll('button')).some(button =>
	    /stop/i.test(button.getAttribute('aria-label') || '')
	  );
	  return {
	    route_matches: location.origin === 'https://gemini.google.com' && Boolean(match),
	    conversation_id: match ? match[1] : '',
	    text: answers.at(-1) || '',
	    is_streaming: streaming,
	    answer_count: answers.length
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

func retryAtFromRecord(record browserflow.Record, fallback time.Time) time.Time {
	if record.RetryAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, record.RetryAt); err == nil {
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

func waitRendered(ctx context.Context, poll time.Duration, remaining time.Duration) bool {
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	if poll > remaining {
		poll = remaining
	}
	timer := time.NewTimer(poll)
	select {
	case <-ctx.Done():
		timer.Stop()
		return false
	case <-timer.C:
		return true
	}
}
