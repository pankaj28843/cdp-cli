package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	ConversationListSchemaVersion   = "claude-conversation-list/v1"
	ConversationDetailSchemaVersion = "claude-conversation-detail/v1"
	maxClaudeResponseBytes          = 16 << 20
)

var defaultAwaitDelays = []time.Duration{
	time.Second,
	2 * time.Second,
	3 * time.Second,
	5 * time.Second,
	8 * time.Second,
	13 * time.Second,
	20 * time.Second,
	30 * time.Second,
}

type ReadConfig struct {
	Store               *Store
	HTTPClient          *http.Client
	BuildCommit         string
	Now                 func() time.Time
	AwaitDelays         []time.Duration
	NewRenderedFallback RenderedFallbackFactory
}

type ConversationSummary struct {
	ID       string         `json:"conversation_id"`
	Title    string         `json:"title"`
	URL      string         `json:"url"`
	Metadata map[string]any `json:"metadata"`
}

type ConversationListData struct {
	SchemaVersion string                `json:"schema_version"`
	StatusCode    int                   `json:"status_code"`
	Conversations []ConversationSummary `json:"conversations"`
	HasMore       bool                  `json:"has_more"`
	ReadMode      string                `json:"read_mode"`
}

type ConversationDetailData struct {
	SchemaVersion   string         `json:"schema_version"`
	StatusCode      int            `json:"status_code"`
	ConversationID  string         `json:"conversation_id"`
	Text            string         `json:"text"`
	CompletionState string         `json:"completion_state"`
	ReadMode        string         `json:"read_mode"`
	Metadata        map[string]any `json:"metadata"`
}

type readFailure struct {
	code         string
	errClass     string
	message      string
	retryAt      time.Time
	statusCode   int
	nextCommands []string
}

func UnavailableRead(
	buildCommit string,
	operation webagent.Operation,
	code string,
	errClass string,
	message string,
) webagent.Result {
	data := map[string]any{
		"schema_version": ConversationDetailSchemaVersion,
	}
	if operation == webagent.OperationConversationsList {
		data["schema_version"] = ConversationListSchemaVersion
	}
	return readFailureResult(
		webagent.NewRunID(),
		buildCommit,
		operation,
		code,
		errClass,
		message,
		time.Time{},
		data,
		nil,
		nil,
	)
}

func ListConversations(ctx context.Context, config ReadConfig, limit int) webagent.Result {
	runID := webagent.NewRunID()
	if limit < 0 || limit > 100 {
		return readFailureResult(
			runID,
			config.BuildCommit,
			webagent.OperationConversationsList,
			"claude_invalid_list_limit",
			"usage",
			"Claude conversation limit must be between 0 and 100",
			time.Time{},
			map[string]any{
				"schema_version": ConversationListSchemaVersion,
				"limit":          limit,
			},
			nil,
			nil,
		)
	}
	if limit == 0 {
		return readSuccessResult(
			runID,
			config.BuildCommit,
			webagent.OperationConversationsList,
			webagent.StateReady,
			ConversationListData{
				SchemaVersion: ConversationListSchemaVersion,
				StatusCode:    http.StatusOK,
				Conversations: []ConversationSummary{},
				ReadMode:      "local_empty_limit",
			},
			nil,
		)
	}

	template, failure := loadFreshReadTemplate(ctx, config)
	if failure != nil {
		return readFailureFrom(runID, config.BuildCommit, webagent.OperationConversationsList, *failure, nil)
	}
	return func() webagent.Result {
		data, failure := fetchConversationList(ctx, config, template, limit)
		if failure != nil {
			if failure.code == "claude_browser_context_required" &&
				config.NewRenderedFallback != nil {
				rendered, closeRendered, err := config.NewRenderedFallback(ctx)
				if err == nil {
					rendered.BuildCommit = config.BuildCommit
					renderedResult := renderedListConversations(
						ctx,
						rendered,
						runID,
						limit,
					)
					if closeRendered != nil {
						_ = closeRendered(context.Background())
					}
					return renderedResult
				}
				failure = &readFailure{
					code:       "claude_rendered_fallback_unavailable",
					errClass:   "connection",
					message:    "Claude rendered fallback is unavailable",
					statusCode: failure.statusCode,
				}
			}
			return readFailureFrom(runID, config.BuildCommit, webagent.OperationConversationsList, *failure, nil)
		}
		return readSuccessResult(
			runID,
			config.BuildCommit,
			webagent.OperationConversationsList,
			webagent.StateReady,
			data,
			nil,
		)
	}()
}

func DetailConversation(ctx context.Context, config ReadConfig, conversationID string) webagent.Result {
	return readConversation(ctx, config, conversationID, false, 0)
}

func AwaitConversation(ctx context.Context, config ReadConfig, conversationID string, timeout time.Duration) webagent.Result {
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	return readConversation(ctx, config, conversationID, true, timeout)
}

func readConversation(
	ctx context.Context,
	config ReadConfig,
	conversationID string,
	await bool,
	timeout time.Duration,
) webagent.Result {
	operation := webagent.OperationConversationsDetail
	if await {
		operation = webagent.OperationConversationsAwait
	}
	runID := webagent.NewRunID()
	if !organizationPattern.MatchString(conversationID) {
		return readFailureResult(
			runID,
			config.BuildCommit,
			operation,
			"claude_invalid_conversation_id",
			"usage",
			"Claude conversation id contains unsupported characters",
			time.Time{},
			map[string]any{
				"schema_version":   ConversationDetailSchemaVersion,
				"completion_state": "invalid_request",
			},
			nil,
			nil,
		)
	}
	template, failure := loadFreshReadTemplate(ctx, config)
	if failure != nil {
		return readFailureFrom(runID, config.BuildCommit, operation, *failure, conversationRef(conversationID))
	}
	deadline := time.Time{}
	if await {
		deadline = nowFor(config).Add(timeout)
	}
	delays := config.AwaitDelays
	if len(delays) == 0 {
		delays = defaultAwaitDelays
	}
	attempt := 0
	var data ConversationDetailData
	var readErr *readFailure
	for {
		attempt++
		data, readErr = fetchConversationDetail(ctx, config, template, conversationID)
		if readErr != nil || data.CompletionState != "incomplete" || !await {
			break
		}
		if attempt > len(delays) {
			break
		}
		delay := delays[attempt-1]
		remaining := time.Until(deadline)
		if config.Now != nil {
			remaining = deadline.Sub(config.Now())
		}
		if remaining <= delay {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			readErr = &readFailure{
				code:     "claude_await_canceled",
				errClass: "timeout",
				message:  "Claude conversation await was canceled before terminal detail",
			}
		case <-timer.C:
		}
		if readErr != nil {
			break
		}
	}
	if readErr != nil &&
		readErr.code == "claude_browser_context_required" &&
		config.NewRenderedFallback != nil {
		rendered, closeRendered, err := config.NewRenderedFallback(ctx)
		if err == nil {
			rendered.BuildCommit = config.BuildCommit
			renderedResult := renderedConversationDetail(
				ctx,
				rendered,
				runID,
				conversationID,
				await,
				timeout,
			)
			if closeRendered != nil {
				_ = closeRendered(context.Background())
			}
			return renderedResult
		}
		readErr = &readFailure{
			code:       "claude_rendered_fallback_unavailable",
			errClass:   "connection",
			message:    "Claude rendered fallback is unavailable",
			statusCode: readErr.statusCode,
		}
	}
	if data.Metadata == nil {
		data.Metadata = map[string]any{}
	}
	data.Metadata["detail_read_attempts"] = attempt
	if readErr != nil {
		return readFailureFrom(runID, config.BuildCommit, operation, *readErr, conversationRef(conversationID))
	}
	state := webagent.StateTerminal
	if data.CompletionState != "terminal" {
		state = webagent.StateIncomplete
	}
	return readSuccessResult(
		runID,
		config.BuildCommit,
		operation,
		state,
		data,
		conversationRef(conversationID),
	)
}

func fetchConversationList(
	ctx context.Context,
	config ReadConfig,
	template AuthTemplate,
	limit int,
) (ConversationListData, *readFailure) {
	request, err := newClaudeRequest(ctx, template, template.ListURL)
	if err != nil {
		return ConversationListData{}, internalReadFailure("Claude conversation list request could not be prepared")
	}
	response, failure := doClaudeRequest(config, request)
	if failure != nil {
		return ConversationListData{}, failure
	}
	defer response.Body.Close()
	var payload struct {
		Data []struct {
			UUID                   string `json:"uuid"`
			Name                   string `json:"name"`
			Summary                string `json:"summary"`
			Model                  string `json:"model"`
			CreatedAt              string `json:"created_at"`
			UpdatedAt              string `json:"updated_at"`
			IsStarred              bool   `json:"is_starred"`
			IsTemporary            bool   `json:"is_temporary"`
			CurrentLeafMessageUUID string `json:"current_leaf_message_uuid"`
		} `json:"data"`
		HasMore bool `json:"has_more"`
	}
	if err := decodeBoundedJSON(response.Body, &payload); err != nil {
		return ConversationListData{}, &readFailure{
			code:       "claude_invalid_list_response",
			errClass:   "provider",
			message:    "Claude conversation list returned an invalid bounded response",
			statusCode: response.StatusCode,
		}
	}
	if len(payload.Data) > limit {
		payload.Data = payload.Data[:limit]
	}
	conversations := make([]ConversationSummary, 0, len(payload.Data))
	for _, item := range payload.Data {
		if !organizationPattern.MatchString(item.UUID) {
			continue
		}
		conversations = append(conversations, ConversationSummary{
			ID:    item.UUID,
			Title: item.Name,
			URL:   Origin + "/chat/" + item.UUID,
			Metadata: map[string]any{
				"summary":                   item.Summary,
				"model":                     item.Model,
				"created_at":                item.CreatedAt,
				"updated_at":                item.UpdatedAt,
				"is_starred":                item.IsStarred,
				"is_temporary":              item.IsTemporary,
				"current_leaf_message_uuid": item.CurrentLeafMessageUUID,
			},
		})
	}
	return ConversationListData{
		SchemaVersion: ConversationListSchemaVersion,
		StatusCode:    response.StatusCode,
		Conversations: conversations,
		HasMore:       payload.HasMore,
		ReadMode:      "observed_stable_http",
	}, nil
}

func fetchConversationDetail(
	ctx context.Context,
	config ReadConfig,
	template AuthTemplate,
	conversationID string,
) (ConversationDetailData, *readFailure) {
	rawURL := fmt.Sprintf(
		"%s/api/organizations/%s/chat_conversations/%s?tree=True&rendering_mode=messages&render_all_tools=true",
		Origin,
		url.PathEscape(template.OrganizationID),
		url.PathEscape(conversationID),
	)
	request, err := newClaudeRequest(ctx, template, rawURL)
	if err != nil {
		return ConversationDetailData{}, internalReadFailure("Claude conversation detail request could not be prepared")
	}
	response, failure := doClaudeRequest(config, request)
	if failure != nil {
		return ConversationDetailData{}, failure
	}
	defer response.Body.Close()
	var payload struct {
		CurrentLeafMessageUUID string `json:"current_leaf_message_uuid"`
		ChatMessages           []struct {
			UUID      string `json:"uuid"`
			Sender    string `json:"sender"`
			Index     any    `json:"index"`
			Truncated *bool  `json:"truncated"`
			Content   []struct {
				Type          string `json:"type"`
				Text          string `json:"text"`
				StopTimestamp any    `json:"stop_timestamp"`
			} `json:"content"`
		} `json:"chat_messages"`
	}
	if err := decodeBoundedJSON(response.Body, &payload); err != nil {
		return ConversationDetailData{}, &readFailure{
			code:       "claude_invalid_detail_response",
			errClass:   "provider",
			message:    "Claude conversation detail returned an invalid bounded response",
			statusCode: response.StatusCode,
		}
	}
	data := ConversationDetailData{
		SchemaVersion:   ConversationDetailSchemaVersion,
		StatusCode:      response.StatusCode,
		ConversationID:  conversationID,
		CompletionState: "incomplete",
		ReadMode:        "observed_stable_http",
		Metadata: map[string]any{
			"source": "observed_chat_conversation_http",
		},
	}
	leafIndex := -1
	for index := range payload.ChatMessages {
		if payload.ChatMessages[index].UUID == payload.CurrentLeafMessageUUID {
			leafIndex = index
			break
		}
	}
	if leafIndex < 0 || payload.ChatMessages[leafIndex].Sender != "assistant" {
		return data, nil
	}
	leaf := payload.ChatMessages[leafIndex]
	var textBlocks []string
	var blockTypes []string
	terminalBlocks := true
	for _, block := range leaf.Content {
		blockTypes = append(blockTypes, block.Type)
		if block.Type != "text" || strings.TrimSpace(block.Text) == "" {
			continue
		}
		textBlocks = append(textBlocks, strings.TrimSpace(block.Text))
		if !presentJSONValue(block.StopTimestamp) {
			terminalBlocks = false
		}
	}
	data.Metadata["message_index"] = leaf.Index
	data.Metadata["text_block_count"] = len(textBlocks)
	data.Metadata["content_block_types"] = blockTypes
	data.Metadata["truncated"] = leaf.Truncated
	if prompt := latestHumanPrompt(payload.ChatMessages, leafIndex); prompt != "" {
		data.Metadata["prompt_fingerprint"] = fingerprintPrompt(prompt)
	}
	terminal := len(textBlocks) > 0 &&
		leaf.Truncated != nil &&
		!*leaf.Truncated &&
		terminalBlocks
	if terminal {
		data.Text = strings.Join(textBlocks, "\n\n")
		data.CompletionState = "terminal"
	}
	return data, nil
}

func newClaudeRequest(ctx context.Context, template AuthTemplate, rawURL string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for name, value := range template.Headers {
		request.Header.Set(name, value)
	}
	request.Header.Del("Accept-Encoding")
	request.Header.Set("User-Agent", template.BrowserUserAgent)
	setClaudeCookieHeader(request, template.Cookies)
	return request, nil
}

func setClaudeCookieHeader(request *http.Request, cookies map[string]string) {
	names := make([]string, 0, len(cookies))
	for name := range cookies {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+cookies[name])
	}
	request.Header.Set("Cookie", strings.Join(parts, "; "))
}

func doClaudeRequest(config ReadConfig, request *http.Request) (*http.Response, *readFailure) {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, &readFailure{
			code:     "claude_http_unavailable",
			errClass: "connection",
			message:  "Claude stable HTTP read is unavailable",
		}
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	_ = response.Body.Close()
	failure := &readFailure{
		code:       "claude_http_failed",
		errClass:   "provider",
		message:    fmt.Sprintf("Claude stable HTTP read returned status %d", response.StatusCode),
		statusCode: response.StatusCode,
	}
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		failure.code = "claude_browser_context_required"
		failure.errClass = "auth"
		failure.message = "Claude stable HTTP read requires refreshed browser context"
	case http.StatusTooManyRequests:
		failure.code = "claude_rate_limited"
		failure.errClass = "rate_limit"
		failure.message = "Claude stable HTTP read was rate limited"
		failure.retryAt = retryAtFromHeader(response.Header.Get("Retry-After"), nowFor(config))
	}
	return nil, failure
}

func loadFreshReadTemplate(ctx context.Context, config ReadConfig) (AuthTemplate, *readFailure) {
	if config.Store == nil {
		return AuthTemplate{}, internalReadFailure("Claude owner-only auth state is unavailable")
	}
	status := config.Store.Status(ctx, nowFor(config), DefaultAuthTTL)
	if !status.Ready {
		return AuthTemplate{}, &readFailure{
			code:     "claude_auth_" + status.State,
			errClass: "auth",
			message:  "Claude auth evidence is not ready for stable reads",
		}
	}
	template, err := config.Store.Load(ctx)
	if err != nil {
		return AuthTemplate{}, internalReadFailure("Claude owner-only auth state could not be loaded")
	}
	return template, nil
}

func readSuccessResult(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	state webagent.State,
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
			BrowserMode: "none",
			ReadMode:    "observed_stable_http",
		},
		Cleanup: webagent.CleanupEvidence{
			Required: false,
			State:    webagent.CleanupNotRequired,
		},
		NextCommands: []string{},
	}
}

func readFailureFrom(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	failure readFailure,
	conversation *webagent.ConversationRef,
) webagent.Result {
	data := map[string]any{
		"schema_version": ConversationDetailSchemaVersion,
		"status_code":    failure.statusCode,
	}
	if operation == webagent.OperationConversationsList {
		data["schema_version"] = ConversationListSchemaVersion
	}
	return readFailureResult(
		runID,
		buildCommit,
		operation,
		failure.code,
		failure.errClass,
		failure.message,
		failure.retryAt,
		data,
		conversation,
		failure.nextCommands,
	)
}

func readFailureResult(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	code string,
	errClass string,
	message string,
	retryAt time.Time,
	data any,
	conversation *webagent.ConversationRef,
	nextCommands []string,
) webagent.Result {
	retryAtValue := ""
	if !retryAt.IsZero() {
		retryAtValue = retryAt.UTC().Format(time.RFC3339Nano)
	}
	if len(nextCommands) == 0 {
		nextCommands = []string{
			"cdp workflow agent claude auth refresh --json",
			"cdp workflow agent claude doctor --json",
		}
	}
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     operation,
		State:         webagent.StateFailed,
		Stage:         webagent.StageObserveTerminal,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  errClass,
			Message:   message,
			RetrySafe: true,
			RetryAt:   retryAtValue,
		},
		Conversation: conversation,
		Data:         data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "none",
			ReadMode:    "observed_stable_http",
		},
		Cleanup: webagent.CleanupEvidence{
			Required: false,
			State:    webagent.CleanupNotRequired,
		},
		NextCommands: nextCommands,
	}
}

func replaceReadFailure(
	result webagent.Result,
	code string,
	errClass string,
	message string,
	retryAt time.Time,
) webagent.Result {
	result.OK = false
	result.State = webagent.StateFailed
	result.Error = &webagent.OperationError{
		Code:      code,
		ErrClass:  errClass,
		Message:   message,
		RetrySafe: true,
	}
	if !retryAt.IsZero() {
		result.Error.RetryAt = retryAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}

func internalReadFailure(message string) *readFailure {
	return &readFailure{
		code:     "claude_read_internal",
		errClass: "internal",
		message:  message,
	}
}

func conversationRef(id string) *webagent.ConversationRef {
	return &webagent.ConversationRef{ID: id, URL: Origin + "/chat/" + id}
}

func nowFor(config ReadConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}

func retryAtFromHeader(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if parsed, err := http.ParseTime(value); err == nil && parsed.After(now) {
		return parsed
	}
	return now.Add(5 * time.Minute)
}

func decodeBoundedJSON(reader io.Reader, target any) error {
	limited := io.LimitReader(reader, maxClaudeResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > maxClaudeResponseBytes {
		return fmt.Errorf("Claude response exceeds bounded read limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func latestHumanPrompt(messages []struct {
	UUID      string `json:"uuid"`
	Sender    string `json:"sender"`
	Index     any    `json:"index"`
	Truncated *bool  `json:"truncated"`
	Content   []struct {
		Type          string `json:"type"`
		Text          string `json:"text"`
		StopTimestamp any    `json:"stop_timestamp"`
	} `json:"content"`
}, before int) string {
	for index := before - 1; index >= 0; index-- {
		if messages[index].Sender != "human" {
			continue
		}
		var text []string
		for _, block := range messages[index].Content {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				text = append(text, strings.TrimSpace(block.Text))
			}
		}
		if len(text) > 0 {
			return strings.Join(text, "\n\n")
		}
	}
	return ""
}

func fingerprintPrompt(prompt string) string {
	lines := strings.Split(strings.TrimSpace(prompt), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func presentJSONValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	default:
		return true
	}
}
