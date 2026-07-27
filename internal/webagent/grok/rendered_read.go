package grok

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	defaultRenderedReadTimeout = 30 * time.Second
	renderedReadPollInterval   = 250 * time.Millisecond
)

type RenderedReadConfig struct {
	Client       cdp.CommandClient
	Engine       *browserflow.Engine
	Journal      browserflow.Journal
	BuildCommit  string
	Timeout      time.Duration
	PollInterval time.Duration
}

type RenderedFallbackFactory func(
	context.Context,
) (RenderedReadConfig, func(context.Context) error, error)

type renderedSidebarObservation struct {
	RouteReady    bool    `json:"route_ready"`
	Expanded      bool    `json:"expanded"`
	ControlCount  int     `json:"control_count"`
	ControlReady  bool    `json:"control_ready"`
	X             float64 `json:"x"`
	Y             float64 `json:"y"`
	Conversations []struct {
		ID    string `json:"conversation_id"`
		Title string `json:"title"`
	} `json:"conversations"`
}

type renderedDetailObservation struct {
	RouteMatches   bool   `json:"route_matches"`
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
	Prompt         string `json:"prompt"`
	Streaming      bool   `json:"is_streaming"`
	AnswerCount    int    `json:"answer_count"`
}

func renderedListConversations(
	ctx context.Context,
	config RenderedReadConfig,
	runID string,
	limit int,
) (result webagent.Result) {
	operation := webagent.OperationConversationsList
	if failure := validateRenderedReadConfig(config); failure != nil {
		return renderedReadFailure(
			runID, config.BuildCommit, operation, webagent.StagePlanned,
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			*failure,
			map[string]any{"schema_version": ConversationListSchemaVersion},
			nil,
		)
	}
	normalizeRenderedReadConfig(&config)
	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID:      runID,
		Provider:   string(webagent.ProviderGrok),
		Operation:  string(operation),
		InitialURL: "about:blank",
	})
	if err != nil {
		return renderedAcquireFailure(config, runID, operation, err)
	}
	target, pending := renderedTargetEvidence(lease, runID)
	defer closeRenderedRead(lease, target, runID, &result)
	if err := prepareRenderedPage(
		ctx,
		config.Client,
		lease.Session(),
		HomeURL,
	); err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return renderedReadFailure(
			runID, config.BuildCommit, operation, webagent.StageAttached,
			target, pending,
			readFailure{
				code:     "grok_rendered_list_unavailable",
				errClass: "connection",
				message:  "Grok rendered conversation history could not be loaded",
			},
			map[string]any{"schema_version": ConversationListSchemaVersion},
			nil,
		)
	}
	deadline := time.Now().Add(config.Timeout)
	var observation renderedSidebarObservation
	attempts, err := pollUntil(
		ctx,
		time.Until(deadline),
		config.PollInterval,
		func() (bool, error) {
			if err := observeRenderedList(
				ctx,
				lease.Session(),
				&observation,
			); err != nil {
				return false, err
			}
			return observation.RouteReady &&
				(observation.Expanded ||
					(observation.ControlCount == 1 && observation.ControlReady)), nil
		},
	)
	if err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return renderedReadFailure(
			runID, config.BuildCommit, operation, webagent.StageAttached,
			target, pending,
			readFailure{
				code:     "grok_rendered_sidebar_not_ready",
				errClass: "provider",
				message:  "Grok rendered history sidebar did not become uniquely ready",
			},
			map[string]any{
				"schema_version": ConversationListSchemaVersion,
				"attempts":       attempts,
			},
			nil,
		)
	}
	if !observation.Expanded {
		outcome, clickErr := browserflow.ClickPoint(
			ctx,
			lease.Session(),
			observation.X,
			observation.Y,
		)
		if clickErr != nil || outcome.Dispatch != browserflow.DispatchPerformed {
			_ = lease.MarkIncomplete(context.Background())
			return renderedReadFailure(
				runID, config.BuildCommit, operation, webagent.StagePrepared,
				target, pending,
				readFailure{
					code:     "grok_rendered_sidebar_open_failed",
					errClass: "provider",
					message:  "Grok rendered history sidebar was not opened once",
				},
				map[string]any{
					"schema_version": ConversationListSchemaVersion,
					"attempts":       attempts,
				},
				nil,
			)
		}
	}
	stableReads := 0
	lastSignature := ""
	for time.Now().Before(deadline) {
		if err := observeRenderedList(
			ctx,
			lease.Session(),
			&observation,
		); err == nil && observation.Expanded {
			signature := renderedListSignature(observation)
			if signature == lastSignature {
				stableReads++
			} else {
				lastSignature = signature
				stableReads = 1
			}
			if len(observation.Conversations) >= limit || stableReads >= 3 {
				break
			}
		}
		if !waitRendered(ctx, config.PollInterval, time.Until(deadline)) {
			break
		}
	}
	if !observation.Expanded || stableReads < 3 && len(observation.Conversations) < limit {
		_ = lease.MarkIncomplete(context.Background())
		return renderedReadFailure(
			runID, config.BuildCommit, operation, webagent.StagePrepared,
			target, pending,
			readFailure{
				code:     "grok_rendered_list_incomplete",
				errClass: "completion",
				message:  "Grok rendered conversation history did not reach a stable snapshot",
			},
			map[string]any{
				"schema_version": ConversationListSchemaVersion,
				"stable_reads":   stableReads,
			},
			nil,
		)
	}
	conversations := make([]ConversationSummary, 0, min(limit, len(observation.Conversations)))
	seen := map[string]bool{}
	for _, raw := range observation.Conversations {
		id := strings.TrimSpace(raw.ID)
		if !conversationIDPattern.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		conversations = append(conversations, ConversationSummary{
			ID:       id,
			Title:    strings.TrimSpace(raw.Title),
			URL:      Origin + "/c/" + id,
			Metadata: map[string]any{"source": "headed_cdp_rendered_sidebar"},
		})
		if len(conversations) == limit {
			break
		}
	}
	data := ConversationListData{
		SchemaVersion: ConversationListSchemaVersion,
		StatusCode:    http.StatusOK,
		Conversations: conversations,
		ReadMode:      "headed_browser",
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		return renderedReadFailure(
			runID, config.BuildCommit, operation, webagent.StageAttached,
			target, pending,
			*internalReadFailure("Grok rendered list state could not be persisted"),
			data,
			nil,
		)
	}
	if err := lease.MarkTerminal(ctx); err != nil {
		return renderedReadFailure(
			runID, config.BuildCommit, operation, webagent.StageObserveTerminal,
			target, pending,
			*internalReadFailure("Grok rendered list terminal state could not be persisted"),
			data,
			nil,
		)
	}
	result = renderedReadSuccess(
		runID,
		config.BuildCommit,
		operation,
		webagent.StateReady,
		target,
		pending,
		data,
		nil,
	)
	return result
}

func renderedConversationDetail(
	ctx context.Context,
	config RenderedReadConfig,
	runID string,
	conversationID string,
	await bool,
	timeout time.Duration,
) (result webagent.Result) {
	operation := webagent.OperationConversationsDetail
	if await {
		operation = webagent.OperationConversationsAwait
	}
	if failure := validateRenderedReadConfig(config); failure != nil {
		return renderedReadFailure(
			runID, config.BuildCommit, operation, webagent.StagePlanned,
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			*failure,
			map[string]any{"schema_version": ConversationDetailSchemaVersion},
			conversationRef(conversationID),
		)
	}
	normalizeRenderedReadConfig(&config)
	if await && timeout > 0 {
		config.Timeout = timeout
	}
	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID:      runID,
		Provider:   string(webagent.ProviderGrok),
		Operation:  string(operation),
		InitialURL: "about:blank",
	})
	if err != nil {
		return renderedAcquireFailure(config, runID, operation, err)
	}
	target, pending := renderedTargetEvidence(lease, runID)
	defer closeRenderedRead(lease, target, runID, &result)
	if err := prepareRenderedPage(
		ctx,
		config.Client,
		lease.Session(),
		Origin+"/c/"+conversationID,
	); err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return renderedReadFailure(
			runID, config.BuildCommit, operation, webagent.StageAttached,
			target, pending,
			readFailure{
				code:     "grok_rendered_detail_unavailable",
				errClass: "connection",
				message:  "Grok rendered conversation detail could not be loaded",
			},
			map[string]any{"schema_version": ConversationDetailSchemaVersion},
			conversationRef(conversationID),
		)
	}
	deadline := time.Now().Add(config.Timeout)
	var observation renderedDetailObservation
	attempts := 0
	stableReads := 0
	lastText := ""
	for time.Now().Before(deadline) {
		attempts++
		if err := observeRenderedDetail(
			ctx,
			lease.Session(),
			conversationID,
			&observation,
		); err == nil &&
			observation.RouteMatches &&
			observation.ConversationID == conversationID &&
			observation.AnswerCount > 0 &&
			strings.TrimSpace(observation.Text) != "" &&
			!observation.Streaming {
			if observation.Text == lastText {
				stableReads++
			} else {
				lastText = observation.Text
				stableReads = 1
			}
			if stableReads >= 2 {
				break
			}
		} else {
			stableReads = 0
			lastText = ""
		}
		if !waitRendered(ctx, config.PollInterval, time.Until(deadline)) {
			break
		}
	}
	data := ConversationDetailData{
		SchemaVersion:   ConversationDetailSchemaVersion,
		StatusCode:      http.StatusOK,
		ConversationID:  conversationID,
		CompletionState: "incomplete",
		ReadMode:        "headed_browser",
		Metadata: map[string]any{
			"source":                "headed_cdp_rendered_detail",
			"formatting":            "rendered_visible_text",
			"detail_read_attempts":  attempts,
			"answer_count":          observation.AnswerCount,
			"terminal_stable_reads": stableReads,
		},
	}
	if strings.TrimSpace(observation.Prompt) != "" {
		data.Metadata["prompt_fingerprint"] = fingerprintPrompt(observation.Prompt)
	}
	state := webagent.StateIncomplete
	if observation.RouteMatches &&
		observation.ConversationID == conversationID &&
		!observation.Streaming &&
		stableReads >= 2 {
		data.Text = strings.TrimSpace(observation.Text)
		data.CompletionState = "terminal"
		state = webagent.StateTerminal
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		return renderedReadFailure(
			runID, config.BuildCommit, operation, webagent.StageAttached,
			target, pending,
			*internalReadFailure("Grok rendered detail state could not be persisted"),
			data,
			conversationRef(conversationID),
		)
	}
	if state == webagent.StateTerminal {
		if err := lease.MarkTerminal(ctx); err != nil {
			return renderedReadFailure(
				runID, config.BuildCommit, operation, webagent.StageObserveTerminal,
				target, pending,
				*internalReadFailure("Grok rendered detail terminal state could not be persisted"),
				data,
				conversationRef(conversationID),
			)
		}
	} else {
		_ = lease.MarkIncomplete(context.Background())
	}
	result = renderedReadSuccess(
		runID,
		config.BuildCommit,
		operation,
		state,
		target,
		pending,
		data,
		conversationRef(conversationID),
	)
	return result
}

func observeRenderedList(
	ctx context.Context,
	session *cdp.PageSession,
	observation *renderedSidebarObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const controls = Array.from(document.querySelectorAll('button')).filter(button =>
	    visible(button) &&
	    (button.innerText || button.textContent || '').trim() === 'Toggle Sidebar'
	  );
	  const control = controls.length === 1 ? controls[0] : null;
	  const rect = control?.getBoundingClientRect();
	  const x = rect ? rect.left + rect.width / 2 : 0;
	  const y = rect ? rect.top + rect.height / 2 : 0;
	  const top = rect ? document.elementFromPoint(x, y) : null;
	  const controlReady = Boolean(
	    control && top && (top === control || control.contains(top)) &&
	    !control.hasAttribute('disabled') && control.getAttribute('aria-disabled') !== 'true'
	  );
	  const expanded = control?.closest('[data-collapsible]')?.getAttribute('data-state') ===
	    'expanded';
	  const seen = new Set();
	  const conversations = [];
	  for (const anchor of document.querySelectorAll('a[href^="/c/"]')) {
	    if (!visible(anchor)) continue;
	    const match = (anchor.getAttribute('href') || '').match(
	      /^\/c\/([A-Za-z0-9_-]+)(?:[/?#]|$)/
	    );
	    if (!match || seen.has(match[1])) continue;
	    seen.add(match[1]);
	    conversations.push({
	      conversation_id: match[1],
	      title: (anchor.innerText || anchor.textContent || '').trim(),
	    });
	  }
	  return {
	    route_ready: location.origin === 'https://grok.com' && location.pathname === '/',
	    expanded,
	    control_count: controls.length,
	    control_ready: controlReady,
	    x,
	    y,
	    conversations,
	  };
	})()`, observation)
}

func observeRenderedDetail(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	observation *renderedDetailObservation,
) error {
	expression := fmt.Sprintf(`(() => {
	  const expected = %q;
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
	  const answer = answers.at(-1);
	  const prompt = prompts.at(-1);
	  const streaming = Array.from(document.querySelectorAll('button')).some(button =>
	    /stop/i.test(button.getAttribute('aria-label') || button.innerText || '')
	  );
	  return {
	    route_matches: Boolean(match && match[1] === expected),
	    conversation_id: match ? match[1] : '',
	    text: (answer?.innerText || answer?.textContent || '').trim(),
	    prompt: (prompt?.innerText || prompt?.textContent || '').trim(),
	    is_streaming: streaming,
	    answer_count: answers.length,
	  };
	})()`, conversationID)
	return evaluateInto(ctx, session, expression, observation)
}

func renderedListSignature(observation renderedSidebarObservation) string {
	var builder strings.Builder
	for _, item := range observation.Conversations {
		builder.WriteString(item.ID)
		builder.WriteByte(0)
		builder.WriteString(item.Title)
		builder.WriteByte(0)
	}
	return builder.String()
}

func prepareRenderedPage(
	ctx context.Context,
	client cdp.CommandClient,
	session *cdp.PageSession,
	rawURL string,
) error {
	for _, method := range []string{"Runtime.enable", "Page.enable"} {
		if err := client.CallSession(
			ctx,
			session.SessionID,
			method,
			map[string]any{},
			nil,
		); err != nil {
			return err
		}
	}
	if err := cdp.ActivateTargetWithClient(ctx, client, session.TargetID); err != nil {
		return err
	}
	_, err := session.Navigate(ctx, rawURL)
	return err
}

func waitRendered(
	ctx context.Context,
	delay time.Duration,
	remaining time.Duration,
) bool {
	if remaining <= 0 {
		return false
	}
	if delay <= 0 {
		delay = renderedReadPollInterval
	}
	if delay > remaining {
		delay = remaining
	}
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return false
	case <-timer.C:
		return true
	}
}

func validateRenderedReadConfig(config RenderedReadConfig) *readFailure {
	if config.Client == nil || config.Engine == nil || config.Journal == nil {
		return internalReadFailure(
			"Grok rendered fallback transaction is not configured",
		)
	}
	return nil
}

func normalizeRenderedReadConfig(config *RenderedReadConfig) {
	if config.Timeout <= 0 {
		config.Timeout = defaultRenderedReadTimeout
	}
	if config.PollInterval <= 0 {
		config.PollInterval = renderedReadPollInterval
	}
}

func renderedTargetEvidence(
	lease *browserflow.Lease,
	runID string,
) (*webagent.TargetEvidence, webagent.CleanupEvidence) {
	target := &webagent.TargetEvidence{
		TargetID:  lease.TargetID(),
		SessionID: lease.Session().SessionID,
		Owned:     true,
		Created:   true,
	}
	return target, webagent.CleanupEvidence{
		Required: true,
		State:    webagent.CleanupPending,
		TargetID: lease.TargetID(),
	}
}

func closeRenderedRead(
	lease *browserflow.Lease,
	target *webagent.TargetEvidence,
	runID string,
	result *webagent.Result,
) {
	cleanup, err := lease.Close(context.Background())
	if err != nil ||
		cleanup.State != browserflow.CleanupClosed ||
		!cleanup.TargetGone {
		target.Closed = false
		result.Evidence.Target = target
		result.Cleanup = webagent.CleanupEvidence{
			Required: true,
			State:    webagent.CleanupFailed,
			TargetID: lease.TargetID(),
		}
		result.Stage = webagent.StageCleanupPending
		*result = replaceFailure(
			*result,
			"grok_exact_target_cleanup_failed",
			"cleanup",
			"Grok rendered fallback could not prove exact target cleanup",
			cleanupCommands(runID, result.Cleanup),
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
}

func renderedAcquireFailure(
	config RenderedReadConfig,
	runID string,
	operation webagent.Operation,
	err error,
) webagent.Result {
	failure := readFailure{
		code:     "grok_rendered_target_unavailable",
		errClass: "connection",
		message:  "Grok rendered fallback could not acquire one exact headed target",
	}
	var budget *browserflow.BudgetExceededError
	if errors.As(err, &budget) {
		failure.code = "grok_browser_resource_budget_exceeded"
		failure.errClass = "resource"
		failure.message = "Grok rendered fallback was blocked by the headed browser resource budget"
	}
	return renderedReadFailure(
		runID, config.BuildCommit, operation, webagent.StagePlanned,
		nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		failure,
		map[string]any{"schema_version": ConversationDetailSchemaVersion},
		nil,
	)
}

func renderedReadSuccess(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	state webagent.State,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	data any,
	conversation *webagent.ConversationRef,
) webagent.Result {
	return operationSuccess(
		runID, buildCommit, operation, state,
		webagent.StageObserveTerminal, "headed_browser",
		target, cleanup, nil, conversation, data, nil,
	)
}

func renderedReadFailure(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	failure readFailure,
	data any,
	conversation *webagent.ConversationRef,
) webagent.Result {
	return operationFailure(
		runID, buildCommit, operation, stage, "headed_browser",
		target, cleanup, nil, conversation,
		failure.code, failure.errClass, failure.message,
		formatRetryAt(failure.retryAt), data,
		cleanupCommands(runID, cleanup),
	)
}
