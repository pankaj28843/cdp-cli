package chatgpt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	ConversationListSchemaVersion   = "chatgpt-conversation-list/v1"
	ConversationDetailSchemaVersion = "chatgpt-conversation-detail/v1"
	ConversationDetailRoute         = "/backend-api/conversation/:conversation_id"
	maxChatGPTResponseBytes         = 32 << 20
)

var (
	conversationIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	errAwaitDeadlineElapsed = errors.New(
		"ChatGPT conversation await deadline elapsed",
	)
	defaultAwaitDelays = []time.Duration{
		time.Second,
		2 * time.Second,
		3 * time.Second,
		5 * time.Second,
		8 * time.Second,
		13 * time.Second,
		20 * time.Second,
		30 * time.Second,
	}
)

type ReadConfig struct {
	Store           *Store
	BrowserConfig   *BrowserConfig
	BrowserFallback func(context.Context) (*BrowserConfig, error)
	HTTPClient      *http.Client
	BuildCommit     string
	Now             func() time.Time
	Wait            func(context.Context, time.Duration) error
	AwaitDelays     []time.Duration
}

type ConversationSummary struct {
	ID               string         `json:"conversation_id"`
	Title            string         `json:"title,omitempty"`
	ShortDescription string         `json:"short_description"`
	CreateTime       any            `json:"create_time,omitempty"`
	UpdateTime       any            `json:"update_time,omitempty"`
	URL              string         `json:"url"`
	Metadata         map[string]any `json:"metadata"`
}

type ConversationListData struct {
	SchemaVersion string                `json:"schema_version"`
	StatusCode    int                   `json:"status_code"`
	Conversations []ConversationSummary `json:"conversations"`
	ReadMode      string                `json:"read_mode"`
	Metadata      map[string]any        `json:"metadata"`
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
	schema := ConversationDetailSchemaVersion
	if operation == webagent.OperationConversationsList {
		schema = ConversationListSchemaVersion
	}
	return readFailureResult(
		webagent.NewRunID(),
		buildCommit,
		operation,
		readFailure{code: code, errClass: errClass, message: message},
		map[string]any{"schema_version": schema},
		nil,
	)
}

func ListConversations(
	ctx context.Context,
	config ReadConfig,
	limit int,
	offset int,
) webagent.Result {
	runID := webagent.NewRunID()
	if limit < 1 || limit > 100 || offset < 0 {
		return readFailureResult(
			runID,
			config.BuildCommit,
			webagent.OperationConversationsList,
			readFailure{
				code:     "chatgpt_invalid_list_window",
				errClass: "usage",
				message:  "ChatGPT list requires limit between 1 and 100 and a non-negative offset",
			},
			map[string]any{
				"schema_version": ConversationListSchemaVersion,
				"limit":          limit,
				"offset":         offset,
			},
			nil,
		)
	}
	if config.BrowserConfig != nil || config.BrowserFallback != nil {
		directConfig := config
		directConfig.BrowserConfig = nil
		directConfig.BrowserFallback = nil
		direct := ListConversations(
			ctx,
			directConfig,
			limit,
			offset,
		)
		if direct.OK || !browserReadFallbackEligible(direct) {
			return direct
		}
		browserConfig, failure := resolveBrowserFallback(ctx, config)
		if failure != nil {
			return recordDirectReadFallback(
				readFailureResult(
					webagent.NewRunID(),
					config.BuildCommit,
					webagent.OperationConversationsList,
					*failure,
					ConversationListData{
						SchemaVersion: ConversationListSchemaVersion,
						Conversations: []ConversationSummary{},
						ReadMode:      "not_started",
						Metadata:      map[string]any{},
					},
					nil,
				),
				direct,
			)
		}
		config.BrowserConfig = browserConfig
		config.BrowserFallback = nil
		return recordDirectReadFallback(
			listConversationsViaBrowser(ctx, config, limit, offset),
			direct,
		)
	}
	template, failure := loadFreshReadTemplate(ctx, config)
	if failure != nil {
		return readFailureResult(
			runID,
			config.BuildCommit,
			webagent.OperationConversationsList,
			*failure,
			map[string]any{"schema_version": ConversationListSchemaVersion},
			nil,
		)
	}
	data, failure := fetchConversationList(ctx, config, template, limit, offset)
	if failure != nil {
		return readFailureResult(
			runID,
			config.BuildCommit,
			webagent.OperationConversationsList,
			*failure,
			data,
			nil,
		)
	}
	return readSuccessResult(
		runID,
		config.BuildCommit,
		webagent.OperationConversationsList,
		webagent.StateReady,
		data,
		nil,
	)
}

func DetailConversation(
	ctx context.Context,
	config ReadConfig,
	conversationID string,
) webagent.Result {
	return readConversation(ctx, config, conversationID, false, 0)
}

func AwaitConversation(
	ctx context.Context,
	config ReadConfig,
	conversationID string,
	timeout time.Duration,
) webagent.Result {
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
	deadline := time.Time{}
	if await {
		if timeout <= 0 {
			timeout = 3 * time.Minute
		}
		deadline = nowForRead(config).Add(timeout)
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(
			ctx,
			timeout,
			errAwaitDeadlineElapsed,
		)
		defer cancel()
	}
	return readConversationUntil(
		ctx,
		config,
		conversationID,
		await,
		deadline,
	)
}

func readConversationUntil(
	ctx context.Context,
	config ReadConfig,
	conversationID string,
	await bool,
	deadline time.Time,
) webagent.Result {
	operation := webagent.OperationConversationsDetail
	if await {
		operation = webagent.OperationConversationsAwait
	}
	conversationID = strings.TrimSpace(conversationID)
	runID := webagent.NewRunID()
	if !conversationIDPattern.MatchString(conversationID) {
		return readFailureResult(
			runID,
			config.BuildCommit,
			operation,
			readFailure{
				code:     "chatgpt_invalid_conversation_id",
				errClass: "usage",
				message:  "ChatGPT conversation id contains unsupported characters",
			},
			map[string]any{
				"schema_version":   ConversationDetailSchemaVersion,
				"completion_state": "invalid_request",
			},
			nil,
		)
	}
	if config.BrowserConfig != nil || config.BrowserFallback != nil {
		directConfig := config
		directConfig.BrowserConfig = nil
		directConfig.BrowserFallback = nil
		direct := readConversationUntil(
			ctx,
			directConfig,
			conversationID,
			await,
			deadline,
		)
		if direct.OK || !browserReadFallbackEligible(direct) {
			return direct
		}
		fallbackCtx := ctx
		cancelFallback := func() {}
		if await {
			remaining := deadline.Sub(nowForRead(config))
			if remaining <= 0 {
				return direct
			}
			fallbackCtx, cancelFallback = context.WithTimeoutCause(
				ctx,
				remaining,
				errAwaitDeadlineElapsed,
			)
		}
		defer cancelFallback()
		browserConfig, fallbackFailure := resolveBrowserFallback(
			fallbackCtx,
			config,
		)
		if fallbackFailure != nil {
			return recordDirectReadFallback(
				readFailureResult(
					webagent.NewRunID(),
					config.BuildCommit,
					operation,
					*fallbackFailure,
					ConversationDetailData{
						SchemaVersion:   ConversationDetailSchemaVersion,
						ConversationID:  conversationID,
						CompletionState: "incomplete",
						ReadMode:        "not_started",
						Metadata:        map[string]any{},
					},
					conversationRef(conversationID),
				),
				direct,
			)
		}
		config.BrowserConfig = browserConfig
		config.BrowserFallback = nil
		return recordDirectReadFallback(conversationViaBrowser(
			fallbackCtx,
			config,
			conversationID,
			await,
			deadline,
		), direct)
	}
	conversation := conversationRef(conversationID)
	template, failure := loadFreshReadTemplate(ctx, config)
	if failure != nil {
		return readFailureResult(
			runID, config.BuildCommit, operation, *failure,
			map[string]any{"schema_version": ConversationDetailSchemaVersion},
			conversation,
		)
	}
	delays := config.AwaitDelays
	if len(delays) == 0 {
		delays = defaultAwaitDelays
	}
	attempts := 0
	var data ConversationDetailData
	for {
		fetchCtx, cancelFetch, fetchAllowed := boundedAwaitFetchContext(
			ctx,
			config,
			await,
			deadline,
		)
		if !fetchAllowed {
			break
		}
		attempts++
		data, failure = fetchConversationDetail(
			fetchCtx,
			config,
			template,
			conversationID,
		)
		fetchDeadlineHit := errors.Is(
			fetchCtx.Err(),
			context.DeadlineExceeded,
		)
		cancelFetch()
		if failure != nil &&
			await &&
			fetchDeadlineHit &&
			awaitDeadlineElapsed(fetchCtx) {
			failure = nil
			break
		}
		if failure != nil ||
			data.CompletionState != "incomplete" ||
			!await {
			break
		}
		remaining := deadline.Sub(nowForRead(config))
		delay, ok := nextAwaitDelay(delays, attempts, remaining)
		if !ok {
			break
		}
		if err := waitReadDelay(ctx, config, delay); err != nil {
			if awaitDeadlineElapsed(ctx) {
				failure = nil
			} else {
				failure = &readFailure{
					code:     "chatgpt_await_canceled",
					errClass: "timeout",
					message:  "ChatGPT conversation await was canceled before terminal detail",
				}
			}
		}
		if failure != nil {
			break
		}
		if !nowForRead(config).Before(deadline) {
			break
		}
	}
	if data.Metadata == nil {
		data.Metadata = map[string]any{}
	}
	data.Metadata["detail_read_attempts"] = attempts
	if failure != nil {
		return readFailureResult(
			runID, config.BuildCommit, operation, *failure, data, conversation,
		)
	}
	state := webagent.StateTerminal
	if data.CompletionState != "terminal" {
		state = webagent.StateIncomplete
	}
	return readSuccessResult(
		runID, config.BuildCommit, operation, state, data, conversation,
	)
}

func resolveBrowserFallback(
	ctx context.Context,
	config ReadConfig,
) (*BrowserConfig, *readFailure) {
	if config.BrowserConfig != nil {
		return config.BrowserConfig, nil
	}
	if config.BrowserFallback == nil {
		return nil, &readFailure{
			code:     "chatgpt_browser_fallback_unavailable",
			errClass: "connection",
			message:  "ChatGPT headed read fallback is unavailable",
		}
	}
	browserConfig, err := config.BrowserFallback(ctx)
	if err != nil || browserConfig == nil {
		return nil, &readFailure{
			code:     "chatgpt_browser_fallback_unavailable",
			errClass: "connection",
			message:  "ChatGPT headed read fallback could not be initialized after the direct path failed",
		}
	}
	return browserConfig, nil
}

func nextAwaitDelay(
	delays []time.Duration,
	attempt int,
	remaining time.Duration,
) (time.Duration, bool) {
	if len(delays) == 0 || attempt < 1 || remaining <= 0 {
		return 0, false
	}
	index := attempt - 1
	if index >= len(delays) {
		index = len(delays) - 1
	}
	delay := delays[index]
	if delay <= 0 {
		return 0, false
	}
	if delay > remaining {
		delay = remaining
	}
	return delay, delay > 0
}

func waitReadDelay(
	ctx context.Context,
	config ReadConfig,
	delay time.Duration,
) error {
	if config.Wait != nil {
		return config.Wait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func boundedAwaitFetchContext(
	ctx context.Context,
	config ReadConfig,
	await bool,
	deadline time.Time,
) (context.Context, context.CancelFunc, bool) {
	if !await {
		return ctx, func() {}, true
	}
	remaining := deadline.Sub(nowForRead(config))
	if remaining <= 0 {
		return ctx, func() {}, false
	}
	fetchCtx, cancel := context.WithTimeoutCause(
		ctx,
		remaining,
		errAwaitDeadlineElapsed,
	)
	return fetchCtx, cancel, true
}

func awaitDeadlineElapsed(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errAwaitDeadlineElapsed)
}

func fetchConversationList(
	ctx context.Context,
	config ReadConfig,
	template RequestTemplate,
	limit int,
	offset int,
) (ConversationListData, *readFailure) {
	data := ConversationListData{
		SchemaVersion: ConversationListSchemaVersion,
		Conversations: []ConversationSummary{},
		ReadMode:      "candidate_http",
		Metadata: map[string]any{
			"limit":       limit,
			"offset":      offset,
			"order":       "updated",
			"is_archived": false,
			"is_starred":  false,
		},
	}
	query := url.Values{}
	query.Set("offset", strconv.Itoa(offset))
	query.Set("limit", strconv.Itoa(limit))
	query.Set("order", "updated")
	query.Set("is_archived", "false")
	query.Set("is_starred", "false")
	endpoint := Origin + ConversationListPath + "?" + query.Encode()
	request, err := newChatGPTRequest(
		ctx,
		template,
		endpoint,
		ConversationListPath,
	)
	if err != nil {
		return data, internalReadFailure(
			"ChatGPT conversation-list request could not be prepared",
		)
	}
	response, failure := doChatGPTRequest(config, request)
	if failure != nil {
		data.StatusCode = failure.statusCode
		return data, failure
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := decodeBoundedJSON(response.Body, &payload); err != nil {
		data.StatusCode = response.StatusCode
		return data, &readFailure{
			code:       "chatgpt_invalid_list_response",
			errClass:   "provider",
			message:    "ChatGPT conversation list returned an invalid bounded response",
			statusCode: response.StatusCode,
		}
	}
	return parseConversationListPayload(data, payload, response.StatusCode)
}

func parseConversationListPayload(
	data ConversationListData,
	payload map[string]any,
	statusCode int,
) (ConversationListData, *readFailure) {
	items, ok := payload["items"].([]any)
	if !ok {
		items, ok = payload["conversations"].([]any)
	}
	if !ok {
		data.StatusCode = statusCode
		return data, &readFailure{
			code:       "chatgpt_invalid_list_response",
			errClass:   "provider",
			message:    "ChatGPT conversation list did not contain an items array",
			statusCode: statusCode,
		}
	}
	skipped := 0
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			skipped++
			continue
		}
		summary, ok := conversationSummaryFromRaw(raw)
		if !ok {
			skipped++
			continue
		}
		data.Conversations = append(data.Conversations, summary)
	}
	for _, key := range []string{"total", "limit", "offset", "has_missing_conversations"} {
		if value, exists := boundedScalar(payload[key]); exists {
			data.Metadata[key] = value
		}
	}
	if skipped > 0 {
		data.Metadata["skipped_items"] = skipped
	}
	data.Metadata["returned_count"] = len(data.Conversations)
	data.StatusCode = statusCode
	data.ReadMode = "observed_stable_http"
	return data, nil
}

func fetchConversationDetail(
	ctx context.Context,
	config ReadConfig,
	template RequestTemplate,
	conversationID string,
) (ConversationDetailData, *readFailure) {
	data := ConversationDetailData{
		SchemaVersion:   ConversationDetailSchemaVersion,
		ConversationID:  conversationID,
		CompletionState: "incomplete",
		ReadMode:        "candidate_http",
		Metadata: map[string]any{
			"source": "hydrated_conversation_detail",
		},
	}
	path := "/backend-api/conversation/" + url.PathEscape(conversationID)
	request, err := newChatGPTRequest(
		ctx,
		template,
		Origin+path,
		ConversationDetailRoute,
	)
	if err != nil {
		return data, internalReadFailure(
			"ChatGPT conversation-detail request could not be prepared",
		)
	}
	response, failure := doChatGPTRequest(config, request)
	if failure != nil {
		data.StatusCode = failure.statusCode
		return data, failure
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := decodeBoundedJSON(response.Body, &payload); err != nil {
		data.StatusCode = response.StatusCode
		return data, &readFailure{
			code:       "chatgpt_invalid_detail_response",
			errClass:   "provider",
			message:    "ChatGPT conversation detail returned an invalid bounded response",
			statusCode: response.StatusCode,
		}
	}
	return parseConversationDetailPayload(data, payload, response.StatusCode)
}

func parseConversationDetailPayload(
	data ConversationDetailData,
	payload map[string]any,
	statusCode int,
) (ConversationDetailData, *readFailure) {
	data.StatusCode = statusCode
	data.ReadMode = "observed_stable_http"
	extracted := extractConversationText(payload)
	data.Text = extracted.text
	data.CompletionState = extracted.completionState
	for key, value := range extracted.metadata {
		data.Metadata[key] = value
	}
	if id := firstString(payload, "conversation_id", "id"); conversationIDPattern.MatchString(id) {
		data.ConversationID = id
	}
	return data, nil
}

type extractedConversation struct {
	text            string
	completionState string
	metadata        map[string]any
}

type conversationActivityState string

const (
	conversationActivityAbsent   conversationActivityState = "absent"
	conversationActivityInactive conversationActivityState = "inactive"
	conversationActivityActive   conversationActivityState = "active"
	conversationActivityUnknown  conversationActivityState = "unknown"
)

func extractConversationText(payload map[string]any) extractedConversation {
	result := extractedConversation{
		completionState: "incomplete",
		metadata:        map[string]any{},
	}
	activity, asyncStatus, streamStatus := conversationActivity(payload)
	result.metadata["provider_activity_state"] = activity
	if asyncStatus != "" {
		result.metadata["provider_async_status"] = asyncStatus
	}
	if streamStatus != "" {
		result.metadata["provider_stream_status"] = streamStatus
	}
	if activity == conversationActivityActive {
		result.metadata["provider_async_active"] = true
	}
	mapping, ok := payload["mapping"].(map[string]any)
	if !ok {
		return result
	}
	current, _ := payload["current_node"].(string)
	seen := map[string]bool{}
	nodes := make([]map[string]any, 0)
	prompt := ""
	for current != "" && !seen[current] {
		seen[current] = true
		raw, ok := mapping[current].(map[string]any)
		if !ok {
			break
		}
		message, _ := raw["message"].(map[string]any)
		role := messageRole(message)
		if role == "user" {
			prompt = messageText(message, false)
			break
		}
		nodes = append(nodes, raw)
		current, _ = raw["parent"].(string)
	}
	if strings.TrimSpace(prompt) != "" {
		result.metadata["prompt_fingerprint"] = fingerprintPrompt(prompt)
	}
	for index, node := range nodes {
		message, _ := node["message"].(map[string]any)
		if messageRole(message) != "assistant" {
			continue
		}
		text := strings.TrimSpace(messageText(message, true))
		if !terminalAnswerTextValid(text, message) {
			continue
		}
		result.text = text
		result.metadata["assistant_is_current_node"] = index == 0
		if index == 0 &&
			(activity == conversationActivityAbsent ||
				activity == conversationActivityInactive) &&
			message["status"] == "finished_successfully" &&
			message["end_turn"] == true {
			result.completionState = "terminal"
		}
		copyResultMetadata(result.metadata, message)
		return result
	}
	return result
}

func conversationActivity(
	payload map[string]any,
) (conversationActivityState, string, string) {
	asyncState, asyncStatus := classifyConversationActivity(
		payload["async_status"],
		hasMapKey(payload, "async_status"),
	)
	streamState, streamStatus := classifyConversationActivity(
		payload["stream_status"],
		hasMapKey(payload, "stream_status"),
	)
	if asyncState == conversationActivityActive ||
		streamState == conversationActivityActive {
		return conversationActivityActive, asyncStatus, streamStatus
	}
	if asyncState == conversationActivityUnknown ||
		streamState == conversationActivityUnknown {
		return conversationActivityUnknown, asyncStatus, streamStatus
	}
	if asyncState == conversationActivityInactive ||
		streamState == conversationActivityInactive {
		return conversationActivityInactive, asyncStatus, streamStatus
	}
	return conversationActivityAbsent, asyncStatus, streamStatus
}

func classifyConversationActivity(
	raw any,
	exists bool,
) (conversationActivityState, string) {
	if !exists || raw == nil {
		return conversationActivityAbsent, ""
	}
	status, scalar := boundedActivityScalar(raw)
	if !scalar {
		return conversationActivityUnknown, "non_scalar"
	}
	normalized := strings.ToUpper(strings.NewReplacer(
		"-", "_",
		" ", "_",
	).Replace(status))
	switch normalized {
	case "3", "IS_STREAMING", "STREAMING", "RUNNING", "IN_PROGRESS":
		return conversationActivityActive, status
	case "COMPLETE", "COMPLETED", "FINISHED", "FINISHED_SUCCESSFULLY",
		"IDLE", "NOT_STREAMING":
		return conversationActivityInactive, status
	default:
		return conversationActivityUnknown, status
	}
}

func boundedActivityScalar(raw any) (string, bool) {
	var status string
	switch value := raw.(type) {
	case string:
		status = strings.TrimSpace(value)
	case json.Number:
		status = strings.TrimSpace(value.String())
	case float64:
		status = strconv.FormatFloat(value, 'f', -1, 64)
	case float32:
		status = strconv.FormatFloat(float64(value), 'f', -1, 32)
	case int:
		status = strconv.Itoa(value)
	case int64:
		status = strconv.FormatInt(value, 10)
	default:
		return "", false
	}
	if status == "" || len(status) > 64 {
		return "", false
	}
	return status, true
}

func hasMapKey(values map[string]any, key string) bool {
	_, exists := values[key]
	return exists
}

func messageRole(message map[string]any) string {
	author, _ := message["author"].(map[string]any)
	role, _ := author["role"].(string)
	return role
}

func messageText(message map[string]any, allowCode bool) string {
	content, _ := message["content"].(map[string]any)
	contentType, _ := content["content_type"].(string)
	if contentType == "text" || contentType == "multimodal_text" {
		parts, _ := content["parts"].([]any)
		var builder strings.Builder
		for _, part := range parts {
			if text, ok := part.(string); ok {
				builder.WriteString(text)
			}
		}
		return builder.String()
	}
	if allowCode && contentType == "code" {
		text, _ := content["text"].(string)
		return text
	}
	return ""
}

func terminalAnswerTextValid(text string, message map[string]any) bool {
	text = strings.TrimSpace(text)
	if text == "" || deepResearchControlPayload(text) {
		return false
	}
	recipient, _ := message["recipient"].(string)
	if recipient == "" {
		author, _ := message["author"].(map[string]any)
		recipient, _ = author["recipient"].(string)
	}
	if recipient != "" && recipient != "all" {
		return false
	}
	metadata, _ := message["metadata"].(map[string]any)
	for _, key := range []string{
		"system1_search_query",
		"search_query",
		"tool_call",
		"is_visually_hidden_from_conversation",
	} {
		if truthy(metadata[key]) {
			return false
		}
	}
	var control map[string]any
	if json.Unmarshal([]byte(text), &control) == nil {
		for _, key := range []string{
			"search_query", "image_query", "open", "click", "find",
			"screenshot", "session_id", "connector_settings", "path",
		} {
			if _, exists := control[key]; exists {
				return false
			}
		}
	}
	lines := nonEmptyLines(text)
	if len(lines) > 0 {
		headingsOnly := true
		for _, line := range lines {
			if !regexp.MustCompile(`^#{1,6}\s+[^#]+$`).MatchString(line) {
				headingsOnly = false
				break
			}
		}
		if headingsOnly {
			return false
		}
	}
	withoutCitations := regexp.MustCompile(`\[[0-9]+\]\([^)]*\)|\[[0-9]+\]|https?://\S+`).
		ReplaceAllString(text, "")
	letters := 0
	for _, character := range withoutCitations {
		if unicode.IsLetter(character) {
			letters++
		}
	}
	return letters >= 2
}

func deepResearchControlPayload(text string) bool {
	var payload map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(text)), &payload) != nil {
		return false
	}
	path, _ := payload["path"].(string)
	if strings.HasPrefix(path, "/Deep Research App/") {
		return true
	}
	_, session := payload["session_id"].(string)
	_, connector := payload["connector_settings"].(map[string]any)
	return session && connector
}

func copyResultMetadata(target map[string]any, message map[string]any) {
	metadata, _ := message["metadata"].(map[string]any)
	for _, key := range []string{
		"citations",
		"content_references",
		"search_result_groups",
		"selected_sources",
		"selected_mcp_sources",
		"caterpillar_selected_sources",
		"thinking_effort",
		"model_slug",
		"resolved_model_slug",
	} {
		if value, exists := metadata[key]; exists && value != nil {
			target[key] = value
		}
	}
}

func conversationSummaryFromRaw(raw map[string]any) (ConversationSummary, bool) {
	id := firstString(raw, "id", "conversation_id")
	if !conversationIDPattern.MatchString(id) {
		return ConversationSummary{}, false
	}
	title := cleanSingleLine(firstString(raw, "title", "name"))
	preview := cleanSingleLine(firstString(raw, "snippet", "description", "preview"))
	description := title
	if description == "" {
		description = preview
	}
	if description == "" {
		description = "Untitled conversation " + id[:min(8, len(id))]
	}
	if len(description) > 200 {
		description = description[:200]
	}
	metadata := map[string]any{}
	for _, key := range []string{"is_archived", "is_starred", "conversation_template_id"} {
		if value, ok := boundedScalar(raw[key]); ok {
			metadata[key] = value
		}
	}
	return ConversationSummary{
		ID:               id,
		Title:            title,
		ShortDescription: description,
		CreateTime:       raw["create_time"],
		UpdateTime:       raw["update_time"],
		URL:              Origin + "/c/" + url.PathEscape(id),
		Metadata:         metadata,
	}, true
}

func newChatGPTRequest(
	ctx context.Context,
	template RequestTemplate,
	rawURL string,
	targetRoute string,
) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for name, value := range template.Headers {
		lower := strings.ToLower(name)
		if lower == "content-type" ||
			lower == "content-length" ||
			lower == "oai-echo-logs" ||
			lower == "oai-telemetry" ||
			lower == "origin" ||
			lower == "priority" ||
			strings.HasPrefix(lower, "sec-fetch-") ||
			lower == "x-conduit-token" ||
			lower == "x-oai-turn-trace-id" ||
			strings.HasPrefix(lower, "openai-sentinel-") {
			continue
		}
		request.Header.Set(name, value)
	}
	request.Header.Del("Accept-Encoding")
	request.Header.Set("Accept", "*/*")
	request.Header.Set("User-Agent", template.BrowserUserAgent)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-OpenAI-Target-Path", parsed.Path)
	request.Header.Set("X-OpenAI-Target-Route", targetRoute)
	request.Header.Set("Cookie", template.CookieHeader)
	return request, nil
}

func doChatGPTRequest(
	config ReadConfig,
	request *http.Request,
) (*http.Response, *readFailure) {
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
			code:     "chatgpt_http_unavailable",
			errClass: "connection",
			message:  "ChatGPT candidate HTTP read is unavailable",
		}
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	_ = response.Body.Close()
	failure := &readFailure{
		code:       "chatgpt_http_failed",
		errClass:   "provider",
		message:    fmt.Sprintf("ChatGPT candidate HTTP read returned status %d", response.StatusCode),
		statusCode: response.StatusCode,
	}
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		failure.code = "chatgpt_browser_context_required"
		failure.errClass = "auth"
		failure.message = "ChatGPT candidate HTTP read requires refreshed headed browser auth"
	case http.StatusTooManyRequests:
		failure.code = "chatgpt_rate_limited"
		failure.errClass = "rate_limit"
		failure.message = "ChatGPT candidate HTTP read was rate limited"
		failure.retryAt = retryAtFromHeader(
			response.Header.Get("Retry-After"),
			nowForRead(config),
		)
	}
	return nil, failure
}

func decodeBoundedJSON(body io.Reader, target any) error {
	limited := io.LimitReader(body, maxChatGPTResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxChatGPTResponseBytes {
		return fmt.Errorf("response body is empty or exceeds its bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("response contains trailing JSON data")
	}
	return nil
}

func loadFreshReadTemplate(
	ctx context.Context,
	config ReadConfig,
) (RequestTemplate, *readFailure) {
	if config.Store == nil {
		return RequestTemplate{}, internalReadFailure(
			"ChatGPT owner-only auth state is unavailable",
		)
	}
	template, status, err := config.Store.LoadTemplateStatus(
		ctx,
		nowForRead(config),
		DefaultAuthTTL,
	)
	if !status.Ready {
		return RequestTemplate{}, &readFailure{
			code:     "chatgpt_auth_" + status.State,
			errClass: "auth",
			message:  "ChatGPT auth evidence is not ready for candidate HTTP reads",
		}
	}
	if err != nil {
		return RequestTemplate{}, internalReadFailure(
			"ChatGPT owner-only auth state could not be loaded",
		)
	}
	return template, nil
}

func browserReadFallbackEligible(result webagent.Result) bool {
	if result.OK || result.Error == nil {
		return false
	}
	if result.Error.Code == "chatgpt_rate_limited" ||
		result.Error.ErrClass == "usage" ||
		result.Error.ErrClass == "rate_limit" {
		return false
	}
	switch result.Error.Code {
	case "chatgpt_browser_context_required",
		"chatgpt_http_unavailable",
		"chatgpt_http_failed":
		return true
	}
	return strings.HasPrefix(result.Error.Code, "chatgpt_auth_")
}

func recordDirectReadFallback(
	result webagent.Result,
	direct webagent.Result,
) webagent.Result {
	if direct.Error == nil {
		return result
	}
	switch data := result.Data.(type) {
	case ConversationListData:
		if data.Metadata == nil {
			data.Metadata = map[string]any{}
		}
		data.Metadata["direct_http_attempted"] = true
		data.Metadata["direct_http_failure_code"] = direct.Error.Code
		result.Data = data
	case ConversationDetailData:
		if data.Metadata == nil {
			data.Metadata = map[string]any{}
		}
		data.Metadata["direct_http_attempted"] = true
		data.Metadata["direct_http_failure_code"] = direct.Error.Code
		result.Data = data
	}
	return result
}

func readSuccessResult(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	state webagent.State,
	data any,
	conversation *webagent.ConversationRef,
) webagent.Result {
	result := operationSuccess(
		runID,
		buildCommit,
		operation,
		webagent.StageObserveTerminal,
		readModeFromData(data),
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		data,
		nil,
	)
	result.State = state
	result.Conversation = conversation
	result.Evidence.BrowserMode = "none"
	return result
}

func readFailureResult(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	failure readFailure,
	data any,
	conversation *webagent.ConversationRef,
) webagent.Result {
	nextCommands := failure.nextCommands
	if len(nextCommands) == 0 {
		nextCommands = readNextCommands(operation, conversation)
	}
	result := operationFailure(
		runID,
		buildCommit,
		operation,
		webagent.StageObserveTerminal,
		readModeFromData(data),
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		failure.code,
		failure.errClass,
		failure.message,
		data,
		nextCommands,
	)
	result.Conversation = conversation
	if result.Error != nil && !failure.retryAt.IsZero() {
		result.Error.RetryAt = failure.retryAt.UTC().Format(time.RFC3339Nano)
	}
	result.Evidence.BrowserMode = "none"
	return result
}

func readModeFromData(data any) string {
	switch value := data.(type) {
	case ConversationListData:
		if value.ReadMode != "" {
			return value.ReadMode
		}
	case ConversationDetailData:
		if value.ReadMode != "" {
			return value.ReadMode
		}
	}
	return "not_started"
}

func readNextCommands(
	operation webagent.Operation,
	conversation *webagent.ConversationRef,
) []string {
	commands := []string{"cdp workflow agent chatgpt auth refresh --json"}
	if conversation != nil && conversation.ID != "" {
		commands = append(
			commands,
			fmt.Sprintf(
				"cdp workflow agent chatgpt conversations detail %s --json",
				conversation.ID,
			),
		)
	}
	return commands
}

func conversationRef(id string) *webagent.ConversationRef {
	return &webagent.ConversationRef{
		ID:  id,
		URL: Origin + "/c/" + url.PathEscape(id),
	}
}

func nowForRead(config ReadConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}

func retryAtFromHeader(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return now.Add(5 * time.Minute)
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return parsed
	}
	return now.Add(5 * time.Minute)
}

func internalReadFailure(message string) *readFailure {
	return &readFailure{
		code:     "chatgpt_read_internal",
		errClass: "internal",
		message:  message,
	}
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cleanSingleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func boundedScalar(value any) (any, bool) {
	switch typed := value.(type) {
	case string:
		if len(typed) <= 4096 {
			return typed, true
		}
	case float64, bool:
		return typed, true
	}
	return nil, false
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return value != nil
	}
}

func nonEmptyLines(text string) []string {
	lines := []string{}
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func fingerprintPrompt(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}
