package tripadvisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	AskSchemaVersion         = "tripadvisor-ask/v1"
	defaultAskTimeout        = 3 * time.Minute
	defaultComposerTimeout   = 30 * time.Second
	defaultRenderedQuietTime = 3 * time.Second
	defaultAmbiguousCooldown = 5 * time.Minute
)

type AskConfig struct {
	BrowserConfig
	Store           *Store
	Timeout         time.Duration
	ComposerTimeout time.Duration
	PollInterval    time.Duration
	QuietInterval   time.Duration
	Now             func() time.Time
	Send            browserflow.Dispatcher
}

type AskData struct {
	SchemaVersion      string         `json:"schema_version"`
	ConversationMode   string         `json:"conversation_mode"`
	Text               string         `json:"text"`
	CompletionState    string         `json:"completion_state"`
	ReadMode           string         `json:"read_mode"`
	PromptFingerprint  string         `json:"prompt_fingerprint,omitempty"`
	PromptCharacters   int            `json:"prompt_characters"`
	DetailReadAttempts int            `json:"detail_read_attempts"`
	SessionMode        string         `json:"session_mode"`
	Metadata           map[string]any `json:"metadata"`
}

type composerObservation struct {
	Blank          bool    `json:"blank"`
	PanelCount     int     `json:"panel_count"`
	EditorCount    int     `json:"editor_count"`
	PromptMatches  bool    `json:"prompt_matches"`
	SendCount      int     `json:"send_count"`
	SendReady      bool    `json:"send_ready"`
	SendX          float64 `json:"send_x"`
	SendY          float64 `json:"send_y"`
	AnswerCount    int     `json:"answer_count"`
	PromptCount    int     `json:"prompt_count"`
	ConversationID string  `json:"conversation_id"`
}

type routeObservation struct {
	Blank          bool   `json:"blank"`
	RouteMatches   bool   `json:"route_matches"`
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
	Prompt         string `json:"prompt"`
	Streaming      bool   `json:"is_streaming"`
	AnswerCount    int    `json:"answer_count"`
	PromptCount    int    `json:"prompt_count"`
	ProviderError  bool   `json:"provider_error"`
}

type tripadvisorSendDispatcher struct {
	prompt string
}

func (d tripadvisorSendDispatcher) Dispatch(
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
		!observation.Blank ||
		observation.PanelCount != 1 ||
		observation.EditorCount != 1 ||
		!observation.PromptMatches ||
		observation.SendCount != 1 ||
		!observation.SendReady ||
		observation.AnswerCount != 0 ||
		observation.PromptCount != 0 ||
		observation.ConversationID != "" {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact Tripadvisor Send control was not actionable")
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
			runID, config, webagent.StagePlanned,
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"tripadvisor_prompt_required", "usage",
			"Tripadvisor prompt must not be empty",
			"", data,
			[]string{
				"cdp workflow agent tripadvisor ask --stdin --json",
			},
		)
	}
	if data.PromptCharacters > MaxPromptCharacters {
		data.Metadata["max_prompt_characters"] = MaxPromptCharacters
		data.Metadata["excess_characters"] =
			data.PromptCharacters - MaxPromptCharacters
		return askFailure(
			runID, config, webagent.StagePlanned,
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"tripadvisor_prompt_too_long", "usage",
			"Tripadvisor prompt exceeds the rendered composer character limit",
			"", data,
			[]string{
				"Split the request into self-contained prompts below the limit.",
			},
		)
	}
	data.PromptFingerprint = promptFingerprint(prompt)
	if config.Timeout <= 0 {
		config.Timeout = defaultAskTimeout
	}
	if config.ComposerTimeout <= 0 {
		config.ComposerTimeout = defaultComposerTimeout
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	if config.QuietInterval <= 0 {
		config.QuietInterval = defaultRenderedQuietTime
	}
	if config.Store == nil {
		return askFailure(
			runID, config, webagent.StagePlanned,
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"tripadvisor_state_unavailable", "internal",
			"Tripadvisor owner-only session state is unavailable",
			"", data,
			[]string{"cdp workflow agent tripadvisor doctor --json"},
		)
	}
	status := config.Store.Status(
		ctx,
		nowForAsk(config),
		DefaultAuthTTL,
	)
	data.SessionMode = status.SessionMode
	if !status.Ready {
		return askFailure(
			runID, config, webagent.StagePlanned,
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"tripadvisor_session_"+status.State, "auth",
			"Tripadvisor rendered session evidence is not ready before Send",
			"", data,
			[]string{
				"cdp workflow agent tripadvisor auth refresh --json",
			},
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
			if err := preparePage(
				ctx,
				config.Client,
				session,
				HomeURL,
			); err != nil {
				return askFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, nil,
					"tripadvisor_composer_page_unavailable", "connection",
					"Tripadvisor fresh composer could not be prepared",
					"", data, cleanupCommands(runID, pending),
				)
			}
			sessionObservation, sessionAttempts, panelOpened, newChatOpened, err :=
				ensureSession(
					ctx,
					session,
					config.ComposerTimeout,
					config.PollInterval,
					true,
				)
			data.Metadata["session_attempts"] = sessionAttempts
			data.Metadata["panel_opened"] = panelOpened
			data.Metadata["new_chat_opened"] = newChatOpened
			data.Metadata["rendered_sign_in_visible"] =
				sessionObservation.SignInVisible
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, nil,
					"tripadvisor_fresh_composer_unavailable", "provider",
					"Tripadvisor fresh blank composer did not become ready before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			verifyAttempts, composer, err := prepareVerifiedPrompt(
				ctx,
				session,
				prompt,
				config.ComposerTimeout,
				config.PollInterval,
			)
			data.Metadata["prompt_verify_attempts"] = verifyAttempts
			if err != nil {
				data.Metadata["observed_blank_route"] = composer.Blank
				data.Metadata["observed_editor_count"] = composer.EditorCount
				data.Metadata["observed_prompt_matches"] =
					composer.PromptMatches
				data.Metadata["observed_send_count"] = composer.SendCount
				data.Metadata["observed_send_ready"] = composer.SendReady
				data.Metadata["observed_answer_count"] = composer.AnswerCount
				data.Metadata["observed_prompt_count"] = composer.PromptCount
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, nil,
					"tripadvisor_prompt_verify_failed", "provider",
					"Tripadvisor exact prompt, blank route, or unique Send control changed before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.BindInputFingerprint(
				ctx,
				data.PromptFingerprint,
			); err != nil {
				return askFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, nil,
					"tripadvisor_prompt_fingerprint_state_failed", "internal",
					"Tripadvisor prompt identity could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return askFailure(
					runID, config, webagent.StageAttached,
					target, pending, notPerformed, nil,
					"tripadvisor_prompt_prepare_state_failed", "internal",
					"Tripadvisor prepared state could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			dispatcher := config.Send
			if dispatcher == nil {
				dispatcher = tripadvisorSendDispatcher{prompt: prompt}
			}
			outcome, _ := lease.Dispatch(ctx, dispatcher)
			action := actionEvidence(lease.Record())
			_ = lease.ReleaseInput()
			if outcome.Dispatch == browserflow.DispatchNotPerformed ||
				(outcome.Dispatch == "" &&
					lease.Record().RawInputCount == 0) {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StagePrepared,
					target, pending, action, nil,
					"tripadvisor_send_not_performed", "provider",
					"Tripadvisor Send was not performed; retrying the ask is safe",
					"", data, cleanupCommands(runID, pending),
				)
			}

			deadline := time.Now().Add(config.Timeout)
			var observation routeObservation
			ackAttempts := 0
			for {
				ackAttempts++
				_ = observeRoute(ctx, session, &observation)
				if observation.RouteMatches &&
					validConversationID(observation.ConversationID) &&
					observation.PromptCount == 1 &&
					observation.Prompt == prompt {
					break
				}
				if !waitUntilNextPoll(
					ctx,
					config.PollInterval,
					deadline,
				) {
					break
				}
			}
			data.Metadata["acknowledgement_attempts"] = ackAttempts
			if !observation.RouteMatches ||
				!validConversationID(observation.ConversationID) ||
				observation.PromptCount != 1 ||
				observation.Prompt != prompt {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "submission_unacknowledged"
				retryAt := retryAtFromRecord(
					lease.Record(),
					nowForAsk(config),
				)
				return askFailure(
					runID, config,
					webagent.StageActionDispatched,
					target, pending, action, nil,
					"tripadvisor_submission_unacknowledged", "completion",
					"Tripadvisor Send was attempted but the exact rendered conversation was not acknowledged; do not resubmit",
					retryAt.Format(time.RFC3339Nano),
					data, cleanupCommands(runID, pending),
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
					runID, config,
					webagent.StageActionDispatched,
					target, pending, action, conversation,
					"tripadvisor_acknowledgement_state_failed", "internal",
					"Tripadvisor conversation acknowledgement could not be persisted; do not resubmit",
					retryAt.Format(time.RFC3339Nano),
					data, cleanupCommands(runID, pending),
				)
			}
			action = actionEvidence(lease.Record())

			attempts, terminal := awaitStableRenderedAnswer(
				ctx,
				session,
				conversationID,
				prompt,
				deadline,
				config.PollInterval,
				config.QuietInterval,
				&observation,
			)
			data.DetailReadAttempts = attempts
			data.ReadMode = "headed_browser"
			data.Metadata["source"] =
				"headed_cdp_visible_submit_rendered_answer"
			data.Metadata["answer_count"] = observation.AnswerCount
			data.Metadata["prompt_count"] = observation.PromptCount
			data.Metadata["provider_error_visible"] =
				observation.ProviderError
			if terminal {
				data.Text = strings.TrimSpace(observation.Text)
				data.CompletionState = "terminal"
				if err := lease.MarkTerminal(ctx); err != nil {
					return askFailure(
						runID, config,
						webagent.StageAcknowledged,
						target, pending, action, conversation,
						"tripadvisor_terminal_state_failed", "internal",
						"Tripadvisor terminal state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return operationSuccess(
					runID, config.BuildCommit,
					webagent.OperationAsk,
					webagent.StateTerminal,
					webagent.StageObserveTerminal,
					"headed_browser",
					target, pending, action, conversation,
					data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent tripadvisor conversations detail %s --json",
							conversationID,
						),
					},
				)
			}
			_ = lease.MarkIncomplete(context.Background())
			data.Text = strings.TrimSpace(observation.Text)
			data.CompletionState = "incomplete"
			return operationSuccess(
				runID, config.BuildCommit,
				webagent.OperationAsk,
				webagent.StateIncomplete,
				webagent.StageObserveTerminal,
				"headed_browser",
				target, pending, action, conversation,
				data,
				[]string{
					fmt.Sprintf(
						"cdp workflow agent tripadvisor conversations await %s --json",
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
		return fmt.Errorf("encode Tripadvisor prompt verification")
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
	  const panels = Array.from(document.querySelectorAll(
	    'aside[aria-label="AI Chat Assistant"]'
	  )).filter(visible);
	  const panel = panels.length === 1 ? panels[0] : null;
	  const editors = panel ? Array.from(panel.querySelectorAll(
	    'textarea[aria-label="Ask anything"]'
	  )).filter(visible) : [];
	  const sends = panel ? Array.from(panel.querySelectorAll(
	    'button[aria-label="Send message"]'
	  )).filter(visible) : [];
	  const send = sends.length === 1 ? sends[0] : null;
	  const rect = send?.getBoundingClientRect();
	  const x = rect ? rect.left + rect.width / 2 : 0;
	  const y = rect ? rect.top + rect.height / 2 : 0;
	  const top = rect ? document.elementFromPoint(x, y) : null;
	  const sendReady = Boolean(
	    send && top && (top === send || send.contains(top)) &&
	    !send.hasAttribute('disabled') &&
	    send.getAttribute('aria-disabled') !== 'true'
	  );
	  const asked = panel ? Array.from(panel.querySelectorAll('*'))
	    .filter(element => visible(element) && element.children.length === 0 &&
	      (element.innerText || element.textContent || '').trim()
	        .startsWith('Asked while viewing')) : [];
	  const answerRoots = panel ? Array.from(new Set(
	    Array.from(panel.querySelectorAll('p'))
	      .filter(element => visible(element) && !element.closest('button'))
	      .map(element => element.parentElement)
	      .filter(Boolean)
	  )) : [];
	  const match = location.hash.match(
	    /^#\/(?:active-)?chat\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:\?.*)?$/
	  );
	  return {
	    blank: location.origin === 'https://www.tripadvisor.com' &&
	      /^#\/chat\/?(?:\?.*)?$/.test(location.hash),
	    panel_count: panels.length,
	    editor_count: editors.length,
	    prompt_matches: editors.length === 1 && editors[0].value === expected,
	    send_count: sends.length,
	    send_ready: sendReady,
	    send_x: x,
	    send_y: y,
	    answer_count: answerRoots.length,
	    prompt_count: asked.length,
	    conversation_id: match ? match[1] : ''
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
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const panel = Array.from(document.querySelectorAll(
	    'aside[aria-label="AI Chat Assistant"]'
	  )).filter(visible);
	  if (panel.length !== 1) return {ok: false};
	  const editors = Array.from(panel[0].querySelectorAll(
	    'textarea[aria-label="Ask anything"]'
	  )).filter(visible);
	  if (editors.length !== 1) return {ok: false};
	  editors[0].focus();
	  editors[0].select();
	  return {
	    ok: document.activeElement === editors[0] &&
	      editors[0].selectionStart === 0 &&
	      editors[0].selectionEnd === editors[0].value.length
	  };
	})()`, &selected); err != nil || !selected.OK {
		return fmt.Errorf("select exact Tripadvisor composer")
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
	for attempt := 1; attempt <= 6; attempt++ {
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
		} else if observation.Blank &&
			observation.PanelCount == 1 &&
			observation.EditorCount == 1 &&
			observation.PromptMatches &&
			observation.SendCount == 1 &&
			observation.SendReady &&
			observation.AnswerCount == 0 &&
			observation.PromptCount == 0 &&
			observation.ConversationID == "" {
			return attempt, observation, nil
		} else {
			lastErr = fmt.Errorf(
				"exact Tripadvisor prompt and Send control are not ready",
			)
		}
		if !waitUntilNextPoll(ctx, poll, deadline) {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("Tripadvisor prompt verification failed")
	}
	return attempts, observation, lastErr
}

func observeRoute(
	ctx context.Context,
	session *cdp.PageSession,
	observation *routeObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const panels = Array.from(document.querySelectorAll(
	    'aside[aria-label="AI Chat Assistant"]'
	  )).filter(visible);
	  const panel = panels.length === 1 ? panels[0] : null;
	  const messageRoots = [];
	  const prompts = [];
	  const prose = panel ? Array.from(panel.querySelectorAll(
	    'p,li,h1,h2,h3,h4,h5,h6'
	  )).filter(element => visible(element) && !element.closest('button')) : [];
	  for (const element of prose) {
	    let node = element.parentElement;
	    while (node && node !== panel) {
	      const previous = node.previousElementSibling;
	      const previousText = (
	        previous?.innerText || previous?.textContent || ''
	      ).trim();
	      const marker = previousText.lastIndexOf('\nAsked while viewing');
	      if (marker > 0) {
	        const prompt = previousText.slice(0, marker).trim();
	        if (prompt && !messageRoots.includes(node)) {
	          messageRoots.push(node);
	          prompts.push(prompt);
	        }
	        break;
	      }
	      node = node.parentElement;
	    }
	  }
	  const answers = messageRoots.map(element =>
	    (element.innerText || element.textContent || '').trim()
	  ).filter(Boolean);
	  const streaming = panel ? Array.from(panel.querySelectorAll(
	    'button,[aria-busy="true"]'
	  )).some(element => {
	    if (!visible(element)) return false;
	    const value = [
	      element.getAttribute('aria-label') || '',
	      element.innerText || element.textContent || ''
	    ].join(' ');
	    return /\b(stop|cancel)\b.*\b(generat|answer|response)|\bstreaming\b/i
	      .test(value);
	  }) : false;
	  const match = location.hash.match(
	    /^#\/(?:active-)?chat\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:\?.*)?$/
	  );
	  const panelText = (panel?.innerText || panel?.textContent || '').trim();
	  return {
	    blank: location.origin === 'https://www.tripadvisor.com' &&
	      /^#\/chat\/?(?:\?.*)?$/.test(location.hash),
	    route_matches: location.origin === 'https://www.tripadvisor.com' &&
	      Boolean(match),
	    conversation_id: match ? match[1] : '',
	    text: answers.at(-1) || '',
	    prompt: prompts.at(-1) || '',
	    is_streaming: streaming,
	    answer_count: answers.length,
	    prompt_count: prompts.length,
	    provider_error: /something went wrong|couldn['’]t load this conversation/i
	      .test(panelText)
	  };
	})()`, observation)
}

func awaitStableRenderedAnswer(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	prompt string,
	deadline time.Time,
	poll time.Duration,
	quiet time.Duration,
	observation *routeObservation,
) (int, bool) {
	if quiet <= 0 {
		quiet = defaultRenderedQuietTime
	}
	attempts := 0
	lastText := ""
	stableSince := time.Time{}
	for {
		attempts++
		_ = observeRoute(ctx, session, observation)
		exact := observation.RouteMatches &&
			observation.ConversationID == conversationID &&
			observation.PromptCount == 1 &&
			observation.Prompt == prompt &&
			observation.AnswerCount > 0 &&
			strings.TrimSpace(observation.Text) != "" &&
			!observation.Streaming &&
			!observation.ProviderError
		if exact {
			text := strings.TrimSpace(observation.Text)
			if text != lastText {
				lastText = text
				stableSince = time.Now()
			} else if !stableSince.IsZero() &&
				time.Since(stableSince) >= quiet {
				return attempts, true
			}
		} else {
			lastText = ""
			stableSince = time.Time{}
		}
		if !waitUntilNextPoll(ctx, poll, deadline) {
			return attempts, false
		}
	}
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
		runID, config.BuildCommit,
		webagent.OperationAsk,
		stage, "headed_browser",
		target, cleanup, action, conversation,
		code, errClass, message, retryAt, data, nextCommands,
	)
}

func promptFingerprint(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
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

func waitUntilNextPoll(
	ctx context.Context,
	poll time.Duration,
	deadline time.Time,
) bool {
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return false
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
