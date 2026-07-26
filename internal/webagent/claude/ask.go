package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AskSchemaVersion         = "claude-ask/v1"
	MaxPromptCharacters      = 18_000
	defaultAskTimeout        = 3 * time.Minute
	defaultComposerTimeout   = 30 * time.Second
	defaultAskPollInterval   = 250 * time.Millisecond
	defaultAmbiguousCooldown = 5 * time.Minute
)

type AskConfig struct {
	Client          cdp.CommandClient
	Engine          *browserflow.Engine
	Journal         browserflow.Journal
	Admission       *admission.Gate
	Store           *Store
	HTTPClient      *http.Client
	BuildCommit     string
	Timeout         time.Duration
	ComposerTimeout time.Duration
	PollInterval    time.Duration
	DetailDelays    []time.Duration
	Now             func() time.Time
	Send            browserflow.Dispatcher
}

type AskData struct {
	SchemaVersion      string         `json:"schema_version"`
	ConversationMode   string         `json:"conversation_mode"`
	Text               string         `json:"text"`
	CompletionState    string         `json:"completion_state"`
	ReadMode           string         `json:"read_mode"`
	ModelLabel         string         `json:"model_label,omitempty"`
	PromptFingerprint  string         `json:"prompt_fingerprint,omitempty"`
	PromptCharacters   int            `json:"prompt_characters"`
	DetailReadAttempts int            `json:"detail_read_attempts"`
	Metadata           map[string]any `json:"metadata"`
}

type composerObservation struct {
	Ready        bool   `json:"composer_ready"`
	QuotaLimited bool   `json:"quota_limited"`
	ModelLabel   string `json:"model_label"`
}

type acknowledgementObservation struct {
	ConversationID string `json:"conversation_id"`
	Streaming      bool   `json:"is_streaming"`
}

func UnavailableAsk(
	buildCommit string,
	code string,
	errClass string,
	message string,
	nextCommands []string,
) webagent.Result {
	return askFailure(
		webagent.NewRunID(),
		buildCommit,
		webagent.StagePlanned,
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		&webagent.ActionEvidence{
			Dispatch:      webagent.DispatchNotPerformed,
			AttemptCount:  0,
			RawInputCount: 0,
			RetrySafe:     true,
		},
		code,
		errClass,
		message,
		"",
		AskData{
			SchemaVersion:    AskSchemaVersion,
			ConversationMode: "fresh_only",
			CompletionState:  "not_submitted",
			ReadMode:         "not_started",
			Metadata:         map[string]any{},
		},
		nil,
		nextCommands,
	)
}

func Ask(ctx context.Context, config AskConfig, prompt string) (result webagent.Result) {
	prompt = strings.TrimSpace(prompt)
	runID := webagent.NewRunID()
	promptCharacters := utf8.RuneCountInString(prompt)
	baseData := AskData{
		SchemaVersion:    AskSchemaVersion,
		ConversationMode: "fresh_only",
		CompletionState:  "not_submitted",
		ReadMode:         "not_started",
		PromptCharacters: promptCharacters,
		Metadata:         map[string]any{},
	}
	notPerformed := &webagent.ActionEvidence{
		Dispatch:      webagent.DispatchNotPerformed,
		AttemptCount:  0,
		RawInputCount: 0,
		RetrySafe:     true,
	}
	if prompt == "" {
		return askFailure(
			runID, config.BuildCommit, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed,
			"claude_prompt_required", "usage", "Claude prompt must not be empty", "",
			baseData, nil,
			[]string{"cdp workflow agent claude ask --stdin --json"},
		)
	}
	if promptCharacters > MaxPromptCharacters {
		baseData.Metadata["max_prompt_characters"] = MaxPromptCharacters
		baseData.Metadata["excess_characters"] = promptCharacters - MaxPromptCharacters
		return askFailure(
			runID, config.BuildCommit, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed,
			"claude_prompt_too_long", "usage", "Claude prompt exceeds the safe character limit", "",
			baseData, nil,
			[]string{"Split the material into coherent self-contained requests below the limit."},
		)
	}
	baseData.PromptFingerprint = fingerprintPrompt(prompt)
	if config.Timeout <= 0 {
		config.Timeout = defaultAskTimeout
	}
	if config.ComposerTimeout <= 0 {
		config.ComposerTimeout = defaultComposerTimeout
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultAskPollInterval
	}
	if config.Client == nil ||
		config.Engine == nil ||
		config.Journal == nil ||
		config.Admission == nil ||
		config.Store == nil {
		return askFailure(
			runID, config.BuildCommit, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed,
			"claude_ask_unavailable", "internal", "Claude ask transaction is not configured", "",
			baseData, nil,
			[]string{"cdp workflow agent claude doctor --json"},
		)
	}
	template, failure := loadFreshReadTemplate(ctx, ReadConfig{
		Store: config.Store,
		Now:   config.Now,
	})
	if failure != nil {
		return askFailure(
			runID, config.BuildCommit, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed,
			failure.code, failure.errClass, failure.message, "",
			baseData, nil,
			[]string{
				"cdp workflow agent claude auth refresh --json",
				"cdp workflow agent claude doctor --json",
			},
		)
	}

	admissionLease, err := config.Admission.Acquire(ctx, admission.Request{
		Provider:  string(webagent.ProviderClaude),
		Operation: string(webagent.OperationAsk),
		RunID:     runID,
	})
	if err != nil {
		var blocked *admission.BlockedError
		retryAt := ""
		code := "claude_admission_unavailable"
		errClass := "internal"
		message := "Claude provider admission state is unavailable"
		nextCommands := []string{"cdp workflow agent claude doctor --json"}
		if errors.As(err, &blocked) {
			code = "claude_admission_blocked"
			errClass = "admission"
			message = blocked.Error()
			if blocked.ResolutionNeeded {
				nextCommands = []string{"cdp workflow agent admission status claude --json"}
			} else {
				retryAt = blocked.RetryAt.UTC().Format(time.RFC3339Nano)
			}
		}
		return askFailure(
			runID, config.BuildCommit, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			notPerformed,
			code, errClass, message, retryAt,
			baseData, nil,
			nextCommands,
		)
	}
	var releaseCooldown time.Time
	defer func() {
		outcome := admission.OutcomeFailed
		if result.OK && result.State == webagent.StateTerminal {
			outcome = admission.OutcomeTerminal
		} else if result.OK && result.State == webagent.StateIncomplete {
			outcome = admission.OutcomeIncomplete
		} else if result.Action != nil &&
			(result.Action.Dispatch == webagent.DispatchUnknown ||
				(result.Action.Dispatch == webagent.DispatchPerformed && result.Conversation == nil)) {
			outcome = admission.OutcomeUnknown
		} else if result.Error != nil && result.Error.Code == "claude_rate_limited" {
			outcome = admission.OutcomeRateLimited
		}
		if err := admissionLease.Release(admission.Release{
			Outcome:       outcome,
			CooldownUntil: releaseCooldown,
		}); err != nil {
			result = replaceAskFailure(
				result,
				"claude_admission_release_failed",
				"internal",
				"Claude provider admission outcome could not be persisted",
				"",
			)
		}
	}()

	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID:      runID,
		Provider:   string(webagent.ProviderClaude),
		Operation:  string(webagent.OperationAsk),
		InitialURL: "about:blank",
	})
	if err != nil {
		target, cleanup, stage := reconcileAcquireFailure(AuthRefreshConfig{
			Engine:  config.Engine,
			Journal: config.Journal,
		}, runID)
		code, errClass, message := classifyAcquireFailure(err)
		if cleanup.State == webagent.CleanupFailed || cleanup.State == webagent.CleanupPending {
			code = "claude_exact_target_cleanup_failed"
			errClass = "cleanup"
			message = "Claude ask could not prove exact target cleanup"
		}
		return askFailure(
			runID, config.BuildCommit, stage, target, cleanup, notPerformed,
			code, errClass, message, "",
			baseData, nil,
			authRefreshNextCommands(runID, cleanup),
		)
	}

	target := &webagent.TargetEvidence{
		TargetID:  lease.TargetID(),
		SessionID: lease.Session().SessionID,
		Owned:     true,
		Created:   true,
	}
	pendingCleanup := webagent.CleanupEvidence{
		Required:        true,
		State:           webagent.CleanupPending,
		TargetID:        lease.TargetID(),
		RecoveryCommand: fmt.Sprintf("cdp workflow agent recovery close %s --json", runID),
	}
	defer func() {
		cleanup, closeErr := lease.Close(context.Background())
		if closeErr != nil || cleanup.State != browserflow.CleanupClosed || !cleanup.TargetGone {
			target.Closed = false
			result.Evidence.Target = target
			result.Cleanup = webagent.CleanupEvidence{
				Required:        true,
				State:           webagent.CleanupFailed,
				TargetID:        lease.TargetID(),
				RecoveryCommand: fmt.Sprintf("cdp workflow agent recovery close %s --json", runID),
			}
			result.Stage = webagent.StageCleanupPending
			result = replaceAskFailure(
				result,
				"claude_exact_target_cleanup_failed",
				"cleanup",
				"Claude ask could not prove exact target cleanup",
				"",
			)
			return
		}
		target.Closed = true
		result.Evidence.Target = target
		result.Cleanup = webagent.CleanupEvidence{
			Required:     true,
			State:        webagent.CleanupClosed,
			TargetID:     lease.TargetID(),
			TargetClosed: true,
			CloseProof:   "exact_target_absent_after_close",
		}
		result.Stage = webagent.StageClosed
	}()

	session := lease.Session()
	if err := enableClaudePage(ctx, config.Client, session); err != nil {
		return askFailure(
			runID, config.BuildCommit, webagent.StageAttached, target, pendingCleanup,
			notPerformed,
			"claude_composer_prepare_failed", "connection",
			"Claude composer could not be prepared on the exact headed target", "",
			baseData, nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	composer, err := waitForComposer(ctx, session, config.ComposerTimeout, config.PollInterval)
	if err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return askFailure(
			runID, config.BuildCommit, webagent.StageAttached, target, pendingCleanup,
			notPerformed,
			"claude_composer_not_ready", "provider",
			"Claude composer did not become ready before Send", "",
			baseData, nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	baseData.ModelLabel = composer.ModelLabel
	if composer.QuotaLimited {
		_ = lease.MarkIncomplete(context.Background())
		releaseCooldown = nowForAsk(config).Add(time.Hour)
		return askFailure(
			runID, config.BuildCommit, webagent.StageAttached, target, pendingCleanup,
			notPerformed,
			"claude_visible_quota_limit", "rate_limit",
			"Claude visible quota limit is active; Send was not attempted",
			releaseCooldown.Format(time.RFC3339Nano),
			baseData, nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if err := prepareExactPrompt(ctx, session, prompt); err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return askFailure(
			runID, config.BuildCommit, webagent.StageAttached, target, pendingCleanup,
			notPerformed,
			"claude_prompt_prepare_failed", "provider",
			"Claude composer did not preserve the exact prompt before Send", "",
			baseData, nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		return askFailure(
			runID, config.BuildCommit, webagent.StageAttached, target, pendingCleanup,
			notPerformed,
			"claude_prompt_prepare_state_failed", "internal",
			"Claude prepared state could not be persisted before Send", "",
			baseData, nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}

	dispatcher := config.Send
	if dispatcher == nil {
		dispatcher = browserflow.DispatchFunc(browserflow.PressEnter)
	}
	outcome, dispatchErr := lease.Dispatch(ctx, dispatcher)
	record := lease.Record()
	action := actionEvidence(record)
	_ = lease.ReleaseInput()
	if outcome.Dispatch == browserflow.DispatchNotPerformed ||
		(outcome.Dispatch == "" && record.RawInputCount == 0) {
		_ = lease.MarkIncomplete(context.Background())
		baseData.CompletionState = "not_submitted"
		return askFailure(
			runID, config.BuildCommit, webagent.StagePrepared, target, pendingCleanup,
			action,
			"claude_send_not_performed", "provider",
			"Claude Send was not performed; retrying the ask is safe", "",
			baseData, nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if dispatchErr != nil && outcome.Dispatch == browserflow.DispatchUnknown {
		releaseCooldown = retryAtFromRecord(record, nowForAsk(config))
	}

	ackDeadline := time.Now().Add(config.Timeout)
	ack, ackErr := waitForAcknowledgement(ctx, session, ackDeadline, config.PollInterval)
	if ackErr != nil || ack.ConversationID == "" {
		_ = lease.MarkIncomplete(context.Background())
		if releaseCooldown.IsZero() {
			releaseCooldown = nowForAsk(config).Add(defaultAmbiguousCooldown)
		}
		baseData.CompletionState = "submission_unacknowledged"
		return askFailure(
			runID, config.BuildCommit, webagent.StageActionDispatched, target, pendingCleanup,
			action,
			"claude_submission_unacknowledged", "completion",
			"Claude Send was attempted but the exact conversation was not acknowledged; do not resubmit",
			releaseCooldown.Format(time.RFC3339Nano),
			baseData, nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if err := lease.Acknowledge(ctx, ack.ConversationID); err != nil {
		if releaseCooldown.IsZero() {
			releaseCooldown = nowForAsk(config).Add(defaultAmbiguousCooldown)
		}
		return askFailure(
			runID, config.BuildCommit, webagent.StageActionDispatched, target, pendingCleanup,
			action,
			"claude_acknowledgement_state_failed", "internal",
			"Claude conversation acknowledgement could not be persisted; do not resubmit",
			releaseCooldown.Format(time.RFC3339Nano),
			baseData, conversationRef(ack.ConversationID),
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	releaseCooldown = time.Time{}
	action = actionEvidence(lease.Record())

	detail, detailAttempts, detailFailure := reconcileAskDetail(
		ctx,
		config,
		template,
		ack.ConversationID,
		ackDeadline,
	)
	if detailFailure != nil &&
		detailFailure.code == "claude_browser_context_required" {
		observation, renderedAttempts, observed := readRenderedDetail(
			ctx,
			session,
			ack.ConversationID,
			true,
			ackDeadline,
			config.PollInterval,
		)
		detailAttempts += renderedAttempts
		if observed {
			detail, _ = conversationDetailFromRendered(
				observation,
				ack.ConversationID,
				renderedAttempts,
			)
			detailFailure = nil
		}
	}
	baseData.DetailReadAttempts = detailAttempts
	baseData.ReadMode = detail.ReadMode
	if baseData.ReadMode == "" {
		baseData.ReadMode = "observed_stable_http"
	}
	if detailFailure != nil {
		_ = lease.MarkIncomplete(context.Background())
		if !detailFailure.retryAt.IsZero() {
			releaseCooldown = detailFailure.retryAt
		}
		baseData.CompletionState = "incomplete"
		return askFailure(
			runID, config.BuildCommit, webagent.StageAcknowledged, target, pendingCleanup,
			action,
			detailFailure.code, detailFailure.errClass, detailFailure.message,
			formatRetryAt(detailFailure.retryAt),
			baseData, conversationRef(ack.ConversationID),
			[]string{
				fmt.Sprintf("cdp workflow agent claude conversations await %s --json", ack.ConversationID),
				fmt.Sprintf("cdp workflow agent recovery inspect %s --json", runID),
			},
		)
	}

	baseData.Text = detail.Text
	baseData.CompletionState = detail.CompletionState
	baseData.Metadata = detail.Metadata
	if detail.CompletionState == "terminal" {
		if err := lease.MarkTerminal(ctx); err != nil {
			return askFailure(
				runID, config.BuildCommit, webagent.StageAcknowledged, target, pendingCleanup,
				action,
				"claude_terminal_state_failed", "internal",
				"Claude terminal state could not be persisted", "",
				baseData, conversationRef(ack.ConversationID),
				authRefreshNextCommands(runID, pendingCleanup),
			)
		}
		result = askSuccess(
			runID, config.BuildCommit, webagent.StateTerminal,
			target, pendingCleanup, action, baseData, ack.ConversationID,
			[]string{
				fmt.Sprintf("cdp workflow agent claude conversations detail %s --json", ack.ConversationID),
				fmt.Sprintf("cdp workflow agent claude conversations delete %s --json", ack.ConversationID),
			},
		)
		return result
	}

	_ = lease.MarkIncomplete(context.Background())
	result = askSuccess(
		runID, config.BuildCommit, webagent.StateIncomplete,
		target, pendingCleanup, action, baseData, ack.ConversationID,
		[]string{
			fmt.Sprintf("cdp workflow agent claude conversations await %s --json", ack.ConversationID),
		},
	)
	return result
}

func enableClaudePage(ctx context.Context, client cdp.CommandClient, session *cdp.PageSession) error {
	if err := client.CallSession(ctx, session.SessionID, "Runtime.enable", map[string]any{}, nil); err != nil {
		return err
	}
	if err := client.CallSession(ctx, session.SessionID, "Page.enable", map[string]any{}, nil); err != nil {
		return err
	}
	if err := cdp.ActivateTargetWithClient(ctx, client, session.TargetID); err != nil {
		return err
	}
	_, err := session.Navigate(ctx, HomeURL)
	return err
}

func waitForComposer(
	ctx context.Context,
	session *cdp.PageSession,
	timeout time.Duration,
	poll time.Duration,
) (composerObservation, error) {
	deadline := time.Now().Add(timeout)
	var last composerObservation
	for {
		observation, err := evaluateComposer(ctx, session)
		if err == nil {
			last = observation
			if observation.Ready || observation.QuotaLimited {
				return observation, nil
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return last, fmt.Errorf("composer deadline exhausted")
		}
		delay := poll
		if delay > remaining {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func evaluateComposer(ctx context.Context, session *cdp.PageSession) (composerObservation, error) {
	const expression = `(() => {
	  const editor = document.querySelector('[contenteditable="true"][aria-label="Write your prompt to Claude"]');
	  const model = document.querySelector('button[data-testid="model-selector-dropdown"]');
	  const text = (document.body?.innerText || '').replace(/\s+/g, ' ').trim();
	  const quota = /(?:out of free messages|message limit|usage limit|reached (?:your|the) limit|limit (?:will )?reset|try again (?:at|after))/i.test(text);
	  return {
	    composer_ready: Boolean(editor),
	    quota_limited: quota,
	    model_label: (model?.getAttribute('aria-label') || '').replace(/^Model:\s*/, '')
	  };
	})()`
	var observation composerObservation
	if err := evaluateInto(ctx, session, expression, &observation); err != nil {
		return composerObservation{}, err
	}
	return observation, nil
}

func prepareExactPrompt(ctx context.Context, session *cdp.PageSession, prompt string) error {
	const selectExpression = `(() => {
	  const editor = document.querySelector('[contenteditable="true"][aria-label="Write your prompt to Claude"]');
	  if (!editor) return {ok: false};
	  editor.focus();
	  const selection = getSelection();
	  const range = document.createRange();
	  range.selectNodeContents(editor);
	  selection.removeAllRanges();
	  selection.addRange(range);
	  return {ok: true};
	})()`
	var selected struct {
		OK bool `json:"ok"`
	}
	if err := evaluateInto(ctx, session, selectExpression, &selected); err != nil || !selected.OK {
		return fmt.Errorf("select exact composer")
	}
	if err := browserflow.InsertText(ctx, session, prompt); err != nil {
		return err
	}
	promptJSON, err := json.Marshal(prompt)
	if err != nil {
		return fmt.Errorf("marshal prompt verification")
	}
	expression := fmt.Sprintf(`(() => {
	  const editor = document.querySelector('[contenteditable="true"][aria-label="Write your prompt to Claude"]');
	  const visible = editor?.children.length
	    ? Array.from(editor.children).map(node => node.textContent || '').join('\n')
	    : (editor?.innerText || editor?.textContent || '');
	  return {ok: Boolean(editor), matches: (visible || '').trim() === %s};
	})()`, promptJSON)
	var verified struct {
		OK      bool `json:"ok"`
		Matches bool `json:"matches"`
	}
	if err := evaluateInto(ctx, session, expression, &verified); err != nil ||
		!verified.OK ||
		!verified.Matches {
		return fmt.Errorf("verify exact composer")
	}
	return nil
}

func waitForAcknowledgement(
	ctx context.Context,
	session *cdp.PageSession,
	deadline time.Time,
	poll time.Duration,
) (acknowledgementObservation, error) {
	var last acknowledgementObservation
	for {
		observation, err := evaluateAcknowledgement(ctx, session)
		if err == nil {
			last = observation
			if observation.ConversationID != "" {
				return observation, nil
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return last, fmt.Errorf("acknowledgement deadline exhausted")
		}
		delay := poll
		if delay > remaining {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func evaluateAcknowledgement(
	ctx context.Context,
	session *cdp.PageSession,
) (acknowledgementObservation, error) {
	const expression = `(() => {
	  const match = location.pathname.match(/^\/chat\/([A-Za-z0-9_-]+)$/);
	  const streaming = Array.from(document.querySelectorAll('button')).some(button =>
	    /stop/i.test((button.getAttribute('aria-label') || '') + ' ' + (button.innerText || ''))
	  );
	  return {conversation_id: match ? match[1] : '', is_streaming: streaming};
	})()`
	var observation acknowledgementObservation
	if err := evaluateInto(ctx, session, expression, &observation); err != nil {
		return acknowledgementObservation{}, err
	}
	if observation.ConversationID != "" && !organizationPattern.MatchString(observation.ConversationID) {
		return acknowledgementObservation{}, fmt.Errorf("invalid acknowledged conversation id")
	}
	return observation, nil
}

func evaluateInto(ctx context.Context, session *cdp.PageSession, expression string, target any) error {
	evaluated, err := session.Evaluate(ctx, expression, true)
	if err != nil {
		return err
	}
	if evaluated.Exception != nil {
		return fmt.Errorf("exact-target evaluation failed")
	}
	if len(evaluated.Object.Value) == 0 {
		return fmt.Errorf("exact-target evaluation returned no value")
	}
	if err := json.Unmarshal(evaluated.Object.Value, target); err != nil {
		return fmt.Errorf("decode exact-target evaluation")
	}
	return nil
}

func reconcileAskDetail(
	ctx context.Context,
	config AskConfig,
	template AuthTemplate,
	conversationID string,
	deadline time.Time,
) (ConversationDetailData, int, *readFailure) {
	delays := config.DetailDelays
	if len(delays) == 0 {
		delays = defaultAwaitDelays
	}
	readConfig := ReadConfig{
		Store:       config.Store,
		HTTPClient:  config.HTTPClient,
		BuildCommit: config.BuildCommit,
		Now:         config.Now,
	}
	attempts := 0
	var detail ConversationDetailData
	for {
		attempts++
		var failure *readFailure
		detail, failure = fetchConversationDetail(ctx, readConfig, template, conversationID)
		if failure != nil || detail.CompletionState == "terminal" {
			return detail, attempts, failure
		}
		if attempts > len(delays) {
			return detail, attempts, nil
		}
		delay := delays[attempts-1]
		if time.Until(deadline) <= delay {
			return detail, attempts, nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return detail, attempts, &readFailure{
				code:     "claude_detail_canceled",
				errClass: "timeout",
				message:  "Claude exact conversation detail was canceled",
			}
		case <-timer.C:
		}
	}
}

func actionEvidence(record browserflow.Record) *webagent.ActionEvidence {
	dispatch := webagent.DispatchNotPerformed
	switch record.Dispatch {
	case browserflow.DispatchPerformed:
		dispatch = webagent.DispatchPerformed
	case browserflow.DispatchUnknown:
		dispatch = webagent.DispatchUnknown
	case browserflow.DispatchNotPerformed:
		dispatch = webagent.DispatchNotPerformed
	}
	retrySafe := dispatch == webagent.DispatchNotPerformed
	return &webagent.ActionEvidence{
		Dispatch:         dispatch,
		AttemptCount:     record.ActionAttemptCount,
		RawInputCount:    record.RawInputCount,
		RetrySafe:        retrySafe,
		PendingPersisted: record.PendingPersisted,
	}
}

func askSuccess(
	runID string,
	buildCommit string,
	state webagent.State,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	data AskData,
	conversationID string,
	nextCommands []string,
) webagent.Result {
	return webagent.Result{
		OK:            true,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationAsk,
		State:         state,
		Stage:         webagent.StageObserveTerminal,
		Action:        action,
		Conversation:  conversationRef(conversationID),
		Data:          data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "headed",
			ReadMode:    data.ReadMode,
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: webagent.CloneCommands(nextCommands),
	}
}

func askFailure(
	runID string,
	buildCommit string,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	code string,
	errClass string,
	message string,
	retryAt string,
	data AskData,
	conversation *webagent.ConversationRef,
	nextCommands []string,
) webagent.Result {
	retrySafe := true
	if action != nil {
		retrySafe = action.RetrySafe
	}
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationAsk,
		State:         webagent.StateFailed,
		Stage:         stage,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  errClass,
			Message:   message,
			RetrySafe: retrySafe,
			RetryAt:   retryAt,
		},
		Action:       action,
		Conversation: conversation,
		Data:         data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "headed",
			ReadMode:    data.ReadMode,
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: webagent.CloneCommands(nextCommands),
	}
}

func replaceAskFailure(
	result webagent.Result,
	code string,
	errClass string,
	message string,
	retryAt string,
) webagent.Result {
	result.OK = false
	result.State = webagent.StateFailed
	retrySafe := true
	if result.Action != nil {
		retrySafe = result.Action.RetrySafe
	}
	result.Error = &webagent.OperationError{
		Code:      code,
		ErrClass:  errClass,
		Message:   message,
		RetrySafe: retrySafe,
		RetryAt:   retryAt,
	}
	if result.Cleanup.State == webagent.CleanupFailed {
		result.NextCommands = authRefreshNextCommands(result.Evidence.RunID, result.Cleanup)
	}
	return result
}

func retryAtFromRecord(record browserflow.Record, fallback time.Time) time.Time {
	if record.RetryAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, record.RetryAt); err == nil {
			return parsed
		}
	}
	return fallback.Add(defaultAmbiguousCooldown)
}

func nowForAsk(config AskConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}

func formatRetryAt(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
