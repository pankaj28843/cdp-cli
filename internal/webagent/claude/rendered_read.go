package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	renderedListStableReads    = 4
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
	Expanded bool    `json:"expanded"`
	Ready    bool    `json:"ready"`
	Count    int     `json:"count"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
}

type renderedListPayload struct {
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
			runID,
			config.BuildCommit,
			operation,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			*failure,
			map[string]any{"schema_version": ConversationListSchemaVersion},
			nil,
			nil,
		)
	}
	normalizeRenderedReadConfig(&config)
	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID:      runID,
		Provider:   string(webagent.ProviderClaude),
		Operation:  string(operation),
		InitialURL: "about:blank",
	})
	if err != nil {
		return renderedAcquireFailure(config, runID, operation, nil, err)
	}
	target, pendingCleanup := renderedTargetEvidence(lease, runID)
	defer closeRenderedRead(lease, target, runID, &result)

	if err := enableClaudeURL(ctx, config.Client, lease.Session(), HomeURL); err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return renderedReadFailure(
			runID,
			config.BuildCommit,
			operation,
			webagent.StageAttached,
			target,
			pendingCleanup,
			readFailure{
				code:     "claude_rendered_list_unavailable",
				errClass: "connection",
				message:  "Claude rendered conversation history could not be loaded",
			},
			map[string]any{"schema_version": ConversationListSchemaVersion},
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	deadline := time.Now().Add(config.Timeout)
	payload, attempts, err := readRenderedList(
		ctx,
		lease.Session(),
		limit,
		deadline,
		config.PollInterval,
	)
	if err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return renderedReadFailure(
			runID,
			config.BuildCommit,
			operation,
			webagent.StageAttached,
			target,
			pendingCleanup,
			readFailure{
				code:     "claude_rendered_list_incomplete",
				errClass: "completion",
				message:  "Claude rendered conversation history did not become ready",
			},
			map[string]any{
				"schema_version": ConversationListSchemaVersion,
				"read_mode":      "headed_browser",
				"attempts":       attempts,
			},
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	conversations := make([]ConversationSummary, 0, len(payload.Conversations))
	seen := map[string]bool{}
	for _, raw := range payload.Conversations {
		id := strings.TrimSpace(raw.ID)
		if !organizationPattern.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		conversations = append(conversations, ConversationSummary{
			ID:       id,
			Title:    strings.TrimSpace(raw.Title),
			URL:      Origin + "/chat/" + id,
			Metadata: map[string]any{},
		})
		if len(conversations) >= limit {
			break
		}
	}
	data := ConversationListData{
		SchemaVersion: ConversationListSchemaVersion,
		StatusCode:    http.StatusOK,
		Conversations: conversations,
		HasMore:       len(conversations) >= limit,
		ReadMode:      "headed_browser",
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		return renderedReadFailure(
			runID,
			config.BuildCommit,
			operation,
			webagent.StageAttached,
			target,
			pendingCleanup,
			internalReadFailureValue("Claude rendered list state could not be persisted"),
			data,
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if err := lease.MarkTerminal(ctx); err != nil {
		return renderedReadFailure(
			runID,
			config.BuildCommit,
			operation,
			webagent.StageObserveTerminal,
			target,
			pendingCleanup,
			internalReadFailureValue("Claude rendered list terminal state could not be persisted"),
			data,
			nil,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	result = renderedReadSuccess(
		runID,
		config.BuildCommit,
		operation,
		webagent.StateReady,
		target,
		pendingCleanup,
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
	conversation := conversationRef(conversationID)
	if failure := validateRenderedReadConfig(config); failure != nil {
		return renderedReadFailure(
			runID,
			config.BuildCommit,
			operation,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			*failure,
			map[string]any{"schema_version": ConversationDetailSchemaVersion},
			conversation,
			nil,
		)
	}
	normalizeRenderedReadConfig(&config)
	if await && timeout > 0 {
		config.Timeout = timeout
	}
	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID:      runID,
		Provider:   string(webagent.ProviderClaude),
		Operation:  string(operation),
		InitialURL: "about:blank",
	})
	if err != nil {
		return renderedAcquireFailure(config, runID, operation, conversation, err)
	}
	target, pendingCleanup := renderedTargetEvidence(lease, runID)
	defer closeRenderedRead(lease, target, runID, &result)

	if err := enableClaudeURL(
		ctx,
		config.Client,
		lease.Session(),
		Origin+"/chat/"+conversationID,
	); err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return renderedReadFailure(
			runID,
			config.BuildCommit,
			operation,
			webagent.StageAttached,
			target,
			pendingCleanup,
			readFailure{
				code:     "claude_rendered_detail_unavailable",
				errClass: "connection",
				message:  "Claude rendered exact conversation could not be loaded",
			},
			map[string]any{"schema_version": ConversationDetailSchemaVersion},
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	deadline := time.Now().Add(config.Timeout)
	observation, attempts, observed := readRenderedDetail(
		ctx,
		lease.Session(),
		conversationID,
		await,
		deadline,
		config.PollInterval,
	)
	if !observed {
		_ = lease.MarkIncomplete(context.Background())
		return renderedReadFailure(
			runID,
			config.BuildCommit,
			operation,
			webagent.StageAttached,
			target,
			pendingCleanup,
			readFailure{
				code:     "claude_rendered_detail_incomplete",
				errClass: "completion",
				message:  "Claude rendered exact conversation did not become readable",
			},
			map[string]any{
				"schema_version": ConversationDetailSchemaVersion,
				"read_mode":      "headed_browser",
				"attempts":       attempts,
			},
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	data, state := conversationDetailFromRendered(
		observation,
		conversationID,
		attempts,
	)
	if err := lease.MarkPrepared(ctx); err != nil {
		return renderedReadFailure(
			runID,
			config.BuildCommit,
			operation,
			webagent.StageAttached,
			target,
			pendingCleanup,
			internalReadFailureValue("Claude rendered detail state could not be persisted"),
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	var completionErr error
	if state == webagent.StateTerminal {
		completionErr = lease.MarkTerminal(ctx)
	} else {
		completionErr = lease.MarkIncomplete(ctx)
	}
	if completionErr != nil {
		return renderedReadFailure(
			runID,
			config.BuildCommit,
			operation,
			webagent.StageObserveTerminal,
			target,
			pendingCleanup,
			internalReadFailureValue("Claude rendered detail completion state could not be persisted"),
			data,
			conversation,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	result = renderedReadSuccess(
		runID,
		config.BuildCommit,
		operation,
		state,
		target,
		pendingCleanup,
		data,
		conversation,
	)
	return result
}

func readRenderedList(
	ctx context.Context,
	session *cdp.PageSession,
	limit int,
	deadline time.Time,
	poll time.Duration,
) (renderedListPayload, int, error) {
	attempts := 0
	stableSignature := ""
	stableReads := 0
	for {
		attempts++
		sidebar, err := observeRenderedSidebar(ctx, session)
		if err == nil && sidebar.Expanded {
			payload, readErr := extractRenderedList(ctx, session, limit)
			if readErr == nil {
				signature := renderedListSignature(payload)
				if signature == stableSignature {
					stableReads++
				} else {
					stableSignature = signature
					stableReads = 1
				}
				if len(payload.Conversations) >= limit ||
					stableReads >= renderedListStableReads {
					return payload, attempts, nil
				}
			}
		} else if err == nil && sidebar.Ready && sidebar.Count == 1 {
			_, _ = browserflow.ClickPoint(ctx, session, sidebar.X, sidebar.Y)
		} else if err == nil && sidebar.Count > 1 {
			return renderedListPayload{}, attempts, fmt.Errorf("Claude sidebar toggle is ambiguous")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return renderedListPayload{}, attempts, fmt.Errorf("Claude sidebar deadline exhausted")
		}
		if !waitRenderedPoll(ctx, poll, remaining) {
			return renderedListPayload{}, attempts, ctx.Err()
		}
	}
}

func renderedListSignature(payload renderedListPayload) string {
	var signature strings.Builder
	for _, conversation := range payload.Conversations {
		signature.WriteString(conversation.ID)
		signature.WriteByte(0)
		signature.WriteString(conversation.Title)
		signature.WriteByte(0)
	}
	return signature.String()
}

func observeRenderedSidebar(
	ctx context.Context,
	session *cdp.PageSession,
) (renderedSidebarObservation, error) {
	const expression = `(() => {
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const toggles = Array.from(
	    document.querySelectorAll('[data-testid="pin-sidebar-toggle"]')
	  ).filter(visible);
	  if (toggles.length !== 1) {
	    return {expanded: false, ready: false, count: toggles.length, x: 0, y: 0};
	  }
	  const toggle = toggles[0];
	  const label = (toggle.getAttribute('aria-label') || '').trim();
	  if (label === 'Close sidebar') {
	    return {expanded: true, ready: true, count: 1, x: 0, y: 0};
	  }
	  toggle.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
	  const rect = toggle.getBoundingClientRect();
	  const x = rect.left + rect.width / 2;
	  const y = rect.top + rect.height / 2;
	  const top = document.elementFromPoint(x, y);
	  const receives = Boolean(top && (top === toggle || toggle.contains(top)));
	  const enabled = !toggle.hasAttribute('disabled') &&
	    toggle.getAttribute('aria-disabled') !== 'true';
	  return {
	    expanded: false,
	    ready: label === 'Open sidebar' && receives && enabled,
	    count: 1,
	    x,
	    y
	  };
	})()`
	var observation renderedSidebarObservation
	if err := evaluateInto(ctx, session, expression, &observation); err != nil {
		return renderedSidebarObservation{}, err
	}
	return observation, nil
}

func extractRenderedList(
	ctx context.Context,
	session *cdp.PageSession,
	limit int,
) (renderedListPayload, error) {
	expression := fmt.Sprintf(`(() => {
	  const limit = %d;
	  const seen = new Set();
	  const conversations = [];
	  for (const anchor of document.querySelectorAll('a[href^="/chat/"]')) {
	    if (!anchor.getClientRects().length) continue;
	    const match = (anchor.getAttribute('href') || '')
	      .match(/^\/chat\/([A-Za-z0-9_-]+)(?:[/?#]|$)/);
	    if (!match || seen.has(match[1])) continue;
	    seen.add(match[1]);
	    conversations.push({
	      conversation_id: match[1],
	      title: (anchor.innerText || anchor.textContent || '').trim()
	    });
	    if (conversations.length >= limit) break;
	  }
	  return {conversations};
	})()`, limit)
	var payload renderedListPayload
	if err := evaluateInto(ctx, session, expression, &payload); err != nil {
		return renderedListPayload{}, err
	}
	return payload, nil
}

func readRenderedDetail(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	await bool,
	deadline time.Time,
	poll time.Duration,
) (renderedDetailObservation, int, bool) {
	attempts := 0
	var last renderedDetailObservation
	for {
		attempts++
		observation, err := observeRenderedDetail(ctx, session, conversationID)
		if err == nil && observation.RouteMatches &&
			observation.ConversationID == conversationID &&
			observation.AnswerCount > 0 {
			last = observation
			if !await || (!observation.Streaming && strings.TrimSpace(observation.Text) != "") {
				return observation, attempts, true
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return last, attempts, last.AnswerCount > 0
		}
		if !waitRenderedPoll(ctx, poll, remaining) {
			return last, attempts, last.AnswerCount > 0
		}
	}
}

func observeRenderedDetail(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
) (renderedDetailObservation, error) {
	idJSON, err := json.Marshal(conversationID)
	if err != nil {
		return renderedDetailObservation{}, err
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  const match = location.pathname.match(/^\/chat\/([A-Za-z0-9_-]+)$/);
	  const answers = Array.from(document.querySelectorAll('[data-is-streaming]'));
	  const answer = answers.at(-1);
	  const prompts = Array.from(document.querySelectorAll('[data-testid="user-message"]'));
	  const prompt = prompts.at(-1);
	  return {
	    route_matches: location.origin === 'https://claude.ai' &&
	      Boolean(match) && match[1] === expected,
	    conversation_id: match ? match[1] : '',
	    text: (answer?.innerText || answer?.textContent || '').trim(),
	    prompt: (prompt?.innerText || prompt?.textContent || '').trim(),
	    is_streaming: answer?.getAttribute('data-is-streaming') !== 'false',
	    answer_count: answers.length
	  };
	})()`, idJSON)
	var observation renderedDetailObservation
	if err := evaluateInto(ctx, session, expression, &observation); err != nil {
		return renderedDetailObservation{}, err
	}
	return observation, nil
}

func conversationDetailFromRendered(
	observation renderedDetailObservation,
	conversationID string,
	attempts int,
) (ConversationDetailData, webagent.State) {
	completion := "incomplete"
	state := webagent.StateIncomplete
	if !observation.Streaming && strings.TrimSpace(observation.Text) != "" {
		completion = "terminal"
		state = webagent.StateTerminal
	}
	metadata := map[string]any{
		"source":               "headed-cdp-rendered-detail",
		"exact_route_ready":    observation.RouteMatches,
		"answer_count":         observation.AnswerCount,
		"detail_read_attempts": attempts,
	}
	if prompt := strings.TrimSpace(observation.Prompt); prompt != "" {
		metadata["prompt_fingerprint"] = renderedPromptFingerprint(prompt)
	}
	return ConversationDetailData{
		SchemaVersion:   ConversationDetailSchemaVersion,
		StatusCode:      http.StatusOK,
		ConversationID:  conversationID,
		Text:            strings.TrimSpace(observation.Text),
		CompletionState: completion,
		ReadMode:        "headed_browser",
		Metadata:        metadata,
	}, state
}

func waitRenderedPoll(ctx context.Context, poll time.Duration, remaining time.Duration) bool {
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

func validateRenderedReadConfig(config RenderedReadConfig) *readFailure {
	if config.Client == nil || config.Engine == nil || config.Journal == nil {
		return &readFailure{
			code:     "claude_rendered_fallback_unavailable",
			errClass: "connection",
			message:  "Claude rendered fallback is unavailable",
		}
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
	cleanup := webagent.CleanupEvidence{
		Required: true,
		State:    webagent.CleanupPending,
		TargetID: lease.TargetID(),
	}
	return target, cleanup
}

func closeRenderedRead(
	lease *browserflow.Lease,
	target *webagent.TargetEvidence,
	runID string,
	result *webagent.Result,
) {
	cleanup, closeErr := lease.Close(context.Background())
	if closeErr != nil ||
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
		*result = replaceReadFailure(
			*result,
			"claude_exact_target_cleanup_failed",
			"cleanup",
			"Claude rendered read could not prove exact target cleanup",
			time.Time{},
		)
		result.NextCommands = authRefreshNextCommands(runID, result.Cleanup)
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
	conversation *webagent.ConversationRef,
	err error,
) webagent.Result {
	target, cleanup, stage := reconcileAcquireFailure(AuthRefreshConfig{
		Engine:  config.Engine,
		Journal: config.Journal,
	}, runID)
	code, errClass, message := classifyAcquireFailure(err)
	if cleanup.State == webagent.CleanupFailed || cleanup.State == webagent.CleanupPending {
		code = "claude_exact_target_cleanup_failed"
		errClass = "cleanup"
		message = "Claude rendered read could not prove exact target cleanup"
	}
	schema := ConversationDetailSchemaVersion
	if operation == webagent.OperationConversationsList {
		schema = ConversationListSchemaVersion
	}
	return renderedReadFailure(
		runID,
		config.BuildCommit,
		operation,
		stage,
		target,
		cleanup,
		readFailure{code: code, errClass: errClass, message: message},
		map[string]any{"schema_version": schema},
		conversation,
		authRefreshNextCommands(runID, cleanup),
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
	return webagent.Result{
		OK:            true,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     operation,
		State:         state,
		Stage:         webagent.StageObserveTerminal,
		Conversation:  conversation,
		Data:          data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "headed",
			ReadMode:    "headed_browser",
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: []string{},
	}
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
	nextCommands []string,
) webagent.Result {
	if nextCommands == nil {
		nextCommands = []string{}
	}
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     operation,
		State:         webagent.StateFailed,
		Stage:         stage,
		Error: &webagent.OperationError{
			Code:      failure.code,
			ErrClass:  failure.errClass,
			Message:   failure.message,
			RetrySafe: true,
			RetryAt:   formatRetryAt(failure.retryAt),
		},
		Conversation: conversation,
		Data:         data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "headed",
			ReadMode:    "headed_browser",
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: webagent.CloneCommands(nextCommands),
	}
}

func internalReadFailureValue(message string) readFailure {
	return readFailure{
		code:     "claude_read_internal",
		errClass: "internal",
		message:  message,
	}
}

func renderedPromptFingerprint(prompt string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(prompt)))
	return hex.EncodeToString(sum[:])
}
