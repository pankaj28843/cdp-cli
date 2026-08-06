package perplexity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AskSchemaVersion         = "perplexity-ask/v1"
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
	CapabilityID       string         `json:"capability_id"`
	PromptFingerprint  string         `json:"prompt_fingerprint,omitempty"`
	PromptCharacters   int            `json:"prompt_characters"`
	DetailReadAttempts int            `json:"detail_read_attempts"`
	Metadata           map[string]any `json:"metadata"`
}

type composerObservation struct {
	RouteReady     bool   `json:"route_ready"`
	EditorReady    bool   `json:"editor_ready"`
	EditorCount    int    `json:"editor_count"`
	PromptMatches  bool   `json:"prompt_matches"`
	SearchCount    int    `json:"search_count"`
	SearchSelected bool   `json:"search_selected"`
	AssistantCount int    `json:"assistant_count"`
	ConversationID string `json:"conversation_id"`
}

type askObservation struct {
	RouteMatches   bool   `json:"route_matches"`
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
	Prompt         string `json:"prompt"`
	Streaming      bool   `json:"is_streaming"`
	AnswerCount    int    `json:"answer_count"`
}

type perplexitySendDispatcher struct {
	prompt string
}

func (d perplexitySendDispatcher) Dispatch(
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
		observation.SearchCount != 1 ||
		!observation.SearchSelected ||
		observation.AssistantCount != 0 ||
		observation.ConversationID != "" {
		return browserflow.DispatchOutcome{
			Dispatch: browserflow.DispatchNotPerformed,
		}, fmt.Errorf("exact Perplexity Send control was not actionable")
	}
	return browserflow.PressEnterOnSelector(ctx, session, "#ask-input")
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
		CapabilityID:     "search",
		PromptCharacters: utf8.RuneCountInString(prompt),
		Metadata:         map[string]any{},
	}
	notPerformed := notPerformedAction()
	if prompt == "" {
		return askFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed, nil,
			"perplexity_prompt_required", "usage",
			"Perplexity prompt must not be empty", "", data,
			[]string{"cdp workflow agent perplexity ask --stdin --json"},
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
			"perplexity_prompt_too_long", "usage",
			"Perplexity prompt exceeds the safe character limit", "", data,
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
			"perplexity_state_unavailable", "internal",
			"Perplexity owner-only state is unavailable", "", data,
			[]string{"cdp workflow agent perplexity doctor --json"},
		)
	}
	now := nowForAsk(config)
	auth := config.Store.AuthStatus(ctx, now, DefaultAuthTTL)
	data.Metadata["cached_auth_state"] = auth.State
	template, err := config.Store.LoadTemplate(ctx)
	templateAvailable := auth.Ready && err == nil
	data.Metadata["cached_read_template_available"] = templateAvailable

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
					"perplexity_composer_prepare_failed", "connection",
					"Perplexity composer could not be prepared on the exact headed target",
					"", data, cleanupCommands(runID, pending),
				)
			}
			var composer composerObservation
			readiness, readinessErr := authreadiness.WaitForEvidence(
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
			data.Metadata["composer_readiness_attempt"] = readiness.Attempt
			data.Metadata["composer_readiness_stage"] = readiness.Stage
			if readinessErr != nil || readiness.ObservationFailed() {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"perplexity_composer_readiness_failed", "connection",
					"Perplexity composer readiness could not complete its bounded load, reload, hard-reload, and final grace sequence",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if !readiness.Observed {
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"perplexity_composer_evidence_not_observed", "provider",
					"Perplexity fresh Search composer was not observed after bounded load, reload, cache-bypassing hard reload, and final grace; the browser session may still be active",
					"", data, cleanupCommands(runID, pending),
				)
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
				data.Metadata["observed_search_count"] = composer.SearchCount
				data.Metadata["observed_search_selected"] =
					composer.SearchSelected
				data.Metadata["observed_assistant_count"] =
					composer.AssistantCount
				data.Metadata["observed_blank_conversation"] =
					composer.ConversationID == ""
				_ = lease.MarkIncomplete(context.Background())
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"perplexity_prompt_verify_failed", "provider",
					"Perplexity exact prompt, blank route, or Search mode changed before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.BindInputFingerprint(
				ctx,
				data.PromptFingerprint,
			); err != nil {
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"perplexity_prompt_identity_state_failed", "internal",
					"Perplexity prompt fingerprint could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return askFailure(
					runID, config, webagent.StageAttached, target, pending,
					notPerformed, nil,
					"perplexity_prompt_prepare_state_failed", "internal",
					"Perplexity prepared state could not be persisted before Send",
					"", data, cleanupCommands(runID, pending),
				)
			}
			dispatcher := config.Send
			if dispatcher == nil {
				dispatcher = perplexitySendDispatcher{
					prompt: prompt,
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
					"perplexity_send_not_performed", "provider",
					"Perplexity Send was not performed; retrying the ask is safe",
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
					"perplexity_submission_unacknowledged", "completion",
					"Perplexity Send was attempted but the exact conversation was not acknowledged; do not resubmit",
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
					"perplexity_acknowledgement_state_failed", "internal",
					"Perplexity conversation acknowledgement could not be persisted; do not resubmit",
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

			stored := ConversationDetailData{}
			var storedFailure *readFailure
			if templateAvailable {
				stored, storedFailure = fetchConversationDetail(
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
			} else {
				storedFailure = &readFailure{
					code:     "perplexity_browser_context_required",
					errClass: "auth",
					message:  "Perplexity cached request template was unavailable",
				}
				data.Metadata["stored_detail_attempts"] = 0
			}
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
						"perplexity_stored_prompt_identity_unavailable", "completion",
						"Perplexity stored terminal detail did not prove the exact prompt identity; do not resubmit",
						"", data,
						[]string{
							fmt.Sprintf(
								"cdp workflow agent perplexity conversations detail %s --json",
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
						"perplexity_stored_prompt_identity_mismatch", "completion",
						"Perplexity stored terminal detail did not match the exact submitted prompt identity; do not resubmit",
						"", data,
						[]string{
							fmt.Sprintf(
								"cdp workflow agent perplexity conversations detail %s --json",
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
				data.Metadata["mode"] = stored.Metadata["mode"]
				if err := lease.MarkTerminal(ctx); err != nil {
					return askFailure(
						runID, config, webagent.StageAcknowledged,
						target, pending, action, conversation,
						"perplexity_terminal_state_failed", "internal",
						"Perplexity terminal state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return operationSuccess(
					runID, config.BuildCommit, webagent.OperationAsk,
					webagent.StateTerminal, webagent.StageObserveTerminal,
					data.ReadMode, target, pending, action, conversation, data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent perplexity conversations detail %s --json",
							conversationID,
						),
						fmt.Sprintf(
							"cdp workflow agent perplexity conversations delete %s --json",
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
				storedFailure.code == "perplexity_browser_context_required" {
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
						"perplexity_terminal_state_failed", "internal",
						"Perplexity rendered fallback terminal state could not be persisted",
						"", data, cleanupCommands(runID, pending),
					)
				}
				return operationSuccess(
					runID, config.BuildCommit, webagent.OperationAsk,
					webagent.StateTerminal, webagent.StageObserveTerminal,
					data.ReadMode, target, pending, action, conversation, data,
					[]string{
						"cdp workflow agent perplexity auth refresh --json",
						fmt.Sprintf(
							"cdp workflow agent perplexity conversations detail %s --json",
							conversationID,
						),
					},
				)
			}
			if storedFailure != nil &&
				storedFailure.code != "perplexity_browser_context_required" {
				_ = lease.MarkIncomplete(context.Background())
				data.CompletionState = "detail_unavailable"
				return askFailure(
					runID, config, webagent.StageAcknowledged,
					target, pending, action, conversation,
					storedFailure.code, storedFailure.errClass,
					"Perplexity acknowledged the request, but canonical stored detail was unavailable; do not resubmit",
					formatRetryAt(storedFailure.retryAt), data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent perplexity conversations detail %s --json",
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
						"cdp workflow agent perplexity conversations await %s --json",
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
		return fmt.Errorf("encode Perplexity prompt verification")
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
	  const editors = Array.from(document.querySelectorAll('#ask-input')).filter(
	    element => visible(element) && element.isContentEditable
	  );
	  const editor = editors.length === 1 ? editors[0] : null;
	  const searches = Array.from(document.querySelectorAll('button')).filter(button =>
	    visible(button) && (button.innerText || button.textContent || '').trim() === 'Search'
	  );
	  const editorText = editor?.textContent || '';
	  const match = location.pathname.match(/^\/search\/([A-Za-z0-9-]+)$/);
	  return {
	    route_ready: location.origin === 'https://www.perplexity.ai' &&
	      location.pathname === '/',
	    editor_ready: Boolean(editor),
	    editor_count: editors.length,
	    prompt_matches: Boolean(editor) && (editorText || '').trim() === expected,
	    search_count: searches.length,
	    search_selected: searches.length === 1 &&
	      searches[0].getAttribute('aria-pressed') === 'true',
	    assistant_count: document.querySelectorAll('main div.prose').length,
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
	  const editor = document.querySelector('#ask-input');
	  if (!editor || !editor.isContentEditable) return {ok: false};
	  editor.focus();
	  const selection = window.getSelection();
	  const range = document.createRange();
	  range.selectNodeContents(editor);
	  selection.removeAllRanges();
	  selection.addRange(range);
	  return {ok: true};
	})()`, &selected); err != nil || !selected.OK {
		return fmt.Errorf("select exact Perplexity composer")
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
			observation.SearchCount == 1 &&
			observation.SearchSelected &&
			observation.AssistantCount == 0 &&
			observation.ConversationID == "" {
			return attempt, observation, nil
		} else {
			lastErr = fmt.Errorf(
				"exact Perplexity prompt and Search mode are not ready",
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
			"exact Perplexity prompt preparation attempts were exhausted",
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
	  const match = location.pathname.match(
	    /^\/search\/(?:new\/)?([A-Za-z0-9-]+)$/
	  );
	  const unique = nodes => [...new Set(nodes)];
	  const answers = unique(Array.from(document.querySelectorAll('main div.prose')));
	  answers.sort((left, right) =>
	    (right.innerText || right.textContent || '').trim().length -
	    (left.innerText || left.textContent || '').trim().length
	  );
	  const prompts = unique(Array.from(document.querySelectorAll(
	    '[data-testid="user-query"],[data-testid="query"],' +
	    '[role="heading"][class*="query"],[class*="group/query"]'
	  )));
	  const streaming = Array.from(document.querySelectorAll('button')).some(button =>
	    /stop/i.test(button.getAttribute('aria-label') || button.innerText || '')
	  );
	  const answer = answers[0];
	  const prompt = prompts.at(-1);
	  return {
	    route_matches: location.origin === 'https://www.perplexity.ai' && Boolean(match),
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

func minDuration(left time.Duration, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}
