package grok

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
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	ConversationListSchemaVersion   = "grok-conversation-list/v1"
	ConversationDetailSchemaVersion = "grok-conversation-detail/v1"
	maxGrokResponseBytes            = 16 << 20
)

var (
	conversationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	defaultAwaitDelays    = []time.Duration{
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
	Store               *Store
	Admission           *admission.Gate
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
	promptText      string
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
) webagent.Result {
	runID := webagent.NewRunID()
	if limit < 0 || limit > 100 {
		return readFailureResult(
			runID,
			config.BuildCommit,
			webagent.OperationConversationsList,
			readFailure{
				code:     "grok_invalid_list_limit",
				errClass: "usage",
				message:  "Grok conversation limit must be between 0 and 100",
			},
			map[string]any{
				"schema_version": ConversationListSchemaVersion,
				"limit":          limit,
			},
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
		return readFailureResult(
			runID,
			config.BuildCommit,
			webagent.OperationConversationsList,
			*failure,
			map[string]any{"schema_version": ConversationListSchemaVersion},
			nil,
		)
	}
	lease, failure := acquireReadAdmission(
		ctx,
		config,
		runID,
		webagent.OperationConversationsList,
	)
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
	data, failure := fetchConversationList(ctx, config, template, limit)
	if failure != nil &&
		failure.code == "grok_browser_context_required" &&
		config.NewRenderedFallback != nil {
		rendered, closeRendered, err := config.NewRenderedFallback(ctx)
		if err == nil {
			rendered.BuildCommit = config.BuildCommit
			result := renderedListConversations(ctx, rendered, runID, limit)
			if closeRendered != nil {
				_ = closeRendered(context.Background())
			}
			return releaseReadAdmission(result, lease, time.Time{})
		}
		failure = &readFailure{
			code:       "grok_rendered_fallback_unavailable",
			errClass:   "connection",
			message:    "Grok rendered conversation-list fallback is unavailable",
			statusCode: failure.statusCode,
		}
	}
	if failure != nil {
		result := readFailureResult(
			runID,
			config.BuildCommit,
			webagent.OperationConversationsList,
			*failure,
			map[string]any{"schema_version": ConversationListSchemaVersion},
			nil,
		)
		return releaseReadAdmission(result, lease, failure.retryAt)
	}
	return releaseReadAdmission(
		readSuccessResult(
			runID,
			config.BuildCommit,
			webagent.OperationConversationsList,
			webagent.StateReady,
			data,
			nil,
		),
		lease,
		time.Time{},
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
				code:     "grok_invalid_conversation_id",
				errClass: "usage",
				message:  "Grok conversation id contains unsupported characters",
			},
			map[string]any{
				"schema_version":   ConversationDetailSchemaVersion,
				"completion_state": "invalid_request",
			},
			nil,
		)
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
	lease, failure := acquireReadAdmission(ctx, config, runID, operation)
	if failure != nil {
		return readFailureResult(
			runID, config.BuildCommit, operation, *failure,
			map[string]any{"schema_version": ConversationDetailSchemaVersion},
			conversation,
		)
	}
	deadline := time.Time{}
	if await {
		deadline = nowForRead(config).Add(timeout)
	}
	delays := config.AwaitDelays
	if len(delays) == 0 {
		delays = defaultAwaitDelays
	}
	attempt := 0
	var data ConversationDetailData
	for {
		attempt++
		data, failure = fetchConversationDetail(
			ctx,
			config,
			template,
			conversationID,
		)
		if failure != nil ||
			data.CompletionState != "incomplete" ||
			!await ||
			attempt > len(delays) {
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
			failure = &readFailure{
				code:     "grok_await_canceled",
				errClass: "timeout",
				message:  "Grok conversation await was canceled before terminal detail",
			}
		case <-timer.C:
		}
		if failure != nil {
			break
		}
	}
	if failure != nil &&
		failure.code == "grok_browser_context_required" &&
		config.NewRenderedFallback != nil {
		rendered, closeRendered, err := config.NewRenderedFallback(ctx)
		if err == nil {
			rendered.BuildCommit = config.BuildCommit
			result := renderedConversationDetail(
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
			return releaseReadAdmission(result, lease, time.Time{})
		}
		failure = &readFailure{
			code:       "grok_rendered_fallback_unavailable",
			errClass:   "connection",
			message:    "Grok rendered conversation-detail fallback is unavailable",
			statusCode: failure.statusCode,
		}
	}
	if data.Metadata == nil {
		data.Metadata = map[string]any{}
	}
	data.Metadata["detail_read_attempts"] = attempt
	if failure != nil {
		result := readFailureResult(
			runID,
			config.BuildCommit,
			operation,
			*failure,
			data,
			conversation,
		)
		return releaseReadAdmission(result, lease, failure.retryAt)
	}
	state := webagent.StateTerminal
	if data.CompletionState != "terminal" {
		state = webagent.StateIncomplete
	}
	return releaseReadAdmission(
		readSuccessResult(
			runID,
			config.BuildCommit,
			operation,
			state,
			data,
			conversation,
		),
		lease,
		time.Time{},
	)
}

func fetchConversationList(
	ctx context.Context,
	config ReadConfig,
	template RequestTemplate,
	limit int,
) (ConversationListData, *readFailure) {
	data := ConversationListData{
		SchemaVersion: ConversationListSchemaVersion,
		Conversations: []ConversationSummary{},
		ReadMode:      "observed_stable_http",
	}
	request, err := newGrokRequest(ctx, template, http.MethodGet, template.URL, nil)
	if err != nil {
		return data, internalReadFailure(
			"Grok conversation-list request could not be prepared",
		)
	}
	response, failure := doGrokRequest(config, request)
	if failure != nil {
		data.StatusCode = failure.statusCode
		return data, failure
	}
	defer response.Body.Close()
	var payload struct {
		Conversations []struct {
			ID         string `json:"conversationId"`
			Title      string `json:"title"`
			CreateTime string `json:"createTime"`
			ModifyTime string `json:"modifyTime"`
			Starred    bool   `json:"starred"`
			Temporary  bool   `json:"temporary"`
		} `json:"conversations"`
	}
	if err := decodeBoundedJSON(response.Body, &payload); err != nil {
		data.StatusCode = response.StatusCode
		return data, &readFailure{
			code:       "grok_invalid_list_response",
			errClass:   "provider",
			message:    "Grok conversation list returned an invalid bounded response",
			statusCode: response.StatusCode,
		}
	}
	conversations := make([]ConversationSummary, 0, min(limit, len(payload.Conversations)))
	for _, raw := range payload.Conversations {
		id := strings.TrimSpace(raw.ID)
		if !conversationIDPattern.MatchString(id) {
			continue
		}
		conversations = append(conversations, ConversationSummary{
			ID:    id,
			Title: strings.TrimSpace(raw.Title),
			URL:   Origin + "/c/" + id,
			Metadata: map[string]any{
				"create_time": raw.CreateTime,
				"modify_time": raw.ModifyTime,
				"starred":     raw.Starred,
				"temporary":   raw.Temporary,
			},
		})
		if len(conversations) == limit {
			break
		}
	}
	data.StatusCode = response.StatusCode
	data.Conversations = conversations
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
		ReadMode:        "observed_stable_http",
		Metadata: map[string]any{
			"source":     "observed_load_responses_http",
			"formatting": "provider_stored_message",
		},
	}
	baseURL := Origin + "/rest/app-chat/conversations/" +
		url.PathEscape(conversationID)
	nodesRequest, err := newGrokRequest(
		ctx,
		template,
		http.MethodGet,
		baseURL+"/response-node",
		nil,
	)
	if err != nil {
		return data, internalReadFailure(
			"Grok response-node request could not be prepared",
		)
	}
	nodesResponse, failure := doGrokRequest(config, nodesRequest)
	if failure != nil {
		data.StatusCode = failure.statusCode
		return data, failure
	}
	var nodesPayload struct {
		ResponseNodes []struct {
			ResponseID string `json:"responseId"`
		} `json:"responseNodes"`
	}
	if err := decodeBoundedJSON(nodesResponse.Body, &nodesPayload); err != nil {
		_ = nodesResponse.Body.Close()
		data.StatusCode = nodesResponse.StatusCode
		return data, &readFailure{
			code:       "grok_invalid_response_nodes",
			errClass:   "provider",
			message:    "Grok response-node endpoint returned an invalid bounded response",
			statusCode: nodesResponse.StatusCode,
		}
	}
	_ = nodesResponse.Body.Close()
	responseIDs := make([]string, 0, len(nodesPayload.ResponseNodes))
	for _, raw := range nodesPayload.ResponseNodes {
		if id := strings.TrimSpace(raw.ResponseID); id != "" && len(id) <= 512 {
			responseIDs = append(responseIDs, id)
		}
	}
	data.StatusCode = nodesResponse.StatusCode
	if len(responseIDs) == 0 {
		data.Metadata["response_count"] = 0
		return data, nil
	}
	body, err := json.Marshal(map[string]any{"responseIds": responseIDs})
	if err != nil {
		return data, internalReadFailure(
			"Grok load-responses request could not be encoded",
		)
	}
	loadRequest, err := newGrokRequest(
		ctx,
		template,
		http.MethodPost,
		baseURL+"/load-responses",
		bytes.NewReader(body),
	)
	if err != nil {
		return data, internalReadFailure(
			"Grok load-responses request could not be prepared",
		)
	}
	loadRequest.Header.Set("Content-Type", "application/json")
	response, failure := doGrokRequest(config, loadRequest)
	if failure != nil {
		data.StatusCode = failure.statusCode
		return data, failure
	}
	defer response.Body.Close()
	var payload struct {
		Responses []struct {
			Sender       string `json:"sender"`
			Message      string `json:"message"`
			Model        string `json:"model"`
			Partial      *bool  `json:"partial"`
			StreamErrors []any  `json:"streamErrors"`
		} `json:"responses"`
	}
	if err := decodeBoundedJSON(response.Body, &payload); err != nil {
		data.StatusCode = response.StatusCode
		return data, &readFailure{
			code:       "grok_invalid_detail_response",
			errClass:   "provider",
			message:    "Grok load-responses endpoint returned an invalid bounded response",
			statusCode: response.StatusCode,
		}
	}
	data.StatusCode = response.StatusCode
	data.Metadata["response_count"] = len(payload.Responses)
	assistantIndex := -1
	for index, raw := range payload.Responses {
		if raw.Sender == "assistant" {
			assistantIndex = index
		}
	}
	if assistantIndex < 0 {
		return data, nil
	}
	assistant := payload.Responses[assistantIndex]
	text := strings.TrimSpace(assistant.Message)
	data.Metadata["model"] = assistant.Model
	data.Metadata["partial"] = assistant.Partial
	data.Metadata["stream_error_count"] = len(assistant.StreamErrors)
	for index := assistantIndex - 1; index >= 0; index-- {
		raw := payload.Responses[index]
		if raw.Sender == "human" && strings.TrimSpace(raw.Message) != "" {
			data.promptText = raw.Message
			data.Metadata["prompt_fingerprint"] = fingerprintPrompt(raw.Message)
			break
		}
	}
	if assistant.Partial != nil &&
		!*assistant.Partial &&
		len(assistant.StreamErrors) > 0 {
		data.Text = text
		data.CompletionState = "provider_stream_error"
		return data, &readFailure{
			code:       "grok_provider_stream_error",
			errClass:   "completion",
			message:    "Grok stored response ended with a provider stream error",
			statusCode: response.StatusCode,
		}
	}
	if assistant.Partial != nil &&
		!*assistant.Partial &&
		len(assistant.StreamErrors) == 0 &&
		text != "" {
		data.Text = text
		data.CompletionState = "terminal"
	}
	return data, nil
}

func newGrokRequest(
	ctx context.Context,
	template RequestTemplate,
	method string,
	rawURL string,
	body io.Reader,
) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	for name, value := range template.Headers {
		request.Header.Set(name, value)
	}
	request.Header.Del("Accept-Encoding")
	request.Header.Set("User-Agent", template.BrowserUserAgent)
	for name, value := range template.Cookies {
		request.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	return request, nil
}

func doGrokRequest(
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
			code:     "grok_http_unavailable",
			errClass: "connection",
			message:  "Grok stable HTTP read is unavailable",
		}
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	_ = response.Body.Close()
	failure := &readFailure{
		code:       "grok_http_failed",
		errClass:   "provider",
		message:    fmt.Sprintf("Grok stable HTTP read returned status %d", response.StatusCode),
		statusCode: response.StatusCode,
	}
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		failure.code = "grok_browser_context_required"
		failure.errClass = "auth"
		failure.message = "Grok stable HTTP read requires refreshed browser context"
	case http.StatusTooManyRequests:
		failure.code = "grok_rate_limited"
		failure.errClass = "rate_limit"
		failure.message = "Grok stable HTTP read was rate limited"
		failure.retryAt = retryAtFromHeader(
			response.Header.Get("Retry-After"),
			nowForRead(config),
		)
	}
	return nil, failure
}

func decodeBoundedJSON(body io.Reader, target any) error {
	limited := io.LimitReader(body, maxGrokResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxGrokResponseBytes {
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
			"Grok owner-only auth state is unavailable",
		)
	}
	status := config.Store.AuthStatus(ctx, nowForRead(config), DefaultAuthTTL)
	if !status.Ready {
		return RequestTemplate{}, &readFailure{
			code:     "grok_auth_" + status.State,
			errClass: "auth",
			message:  "Grok auth evidence is not ready for stable reads",
		}
	}
	template, err := config.Store.LoadTemplate(ctx)
	if err != nil {
		return RequestTemplate{}, internalReadFailure(
			"Grok owner-only auth state could not be loaded",
		)
	}
	return template, nil
}

func acquireReadAdmission(
	ctx context.Context,
	config ReadConfig,
	runID string,
	operation webagent.Operation,
) (*admission.Lease, *readFailure) {
	if config.Admission == nil {
		return nil, internalReadFailure(
			"Grok provider admission is unavailable",
		)
	}
	lease, err := config.Admission.Acquire(ctx, admission.Request{
		Provider:  string(webagent.ProviderGrok),
		Operation: string(operation),
		RunID:     runID,
	})
	if err == nil {
		return lease, nil
	}
	var blocked *admission.BlockedError
	if errors.As(err, &blocked) {
		failure := &readFailure{
			code:     "grok_admission_blocked",
			errClass: "admission",
			message:  blocked.Error(),
		}
		if blocked.ResolutionNeeded {
			failure.nextCommands = []string{"cdp workflow agent admission status grok --json"}
		} else {
			failure.retryAt = blocked.RetryAt
		}
		return nil, failure
	}
	return nil, internalReadFailure(
		"Grok provider admission state is unavailable",
	)
}

func releaseReadAdmission(
	result webagent.Result,
	lease *admission.Lease,
	cooldown time.Time,
) webagent.Result {
	if lease == nil {
		return result
	}
	outcome := admission.OutcomeFailed
	switch {
	case result.OK && result.State == webagent.StateTerminal:
		outcome = admission.OutcomeTerminal
	case result.OK && result.State == webagent.StateIncomplete:
		outcome = admission.OutcomeIncomplete
	case result.OK:
		outcome = admission.OutcomeCompleted
	case result.Error != nil && result.Error.Code == "grok_rate_limited":
		outcome = admission.OutcomeRateLimited
	}
	if err := lease.Release(admission.Release{
		Outcome:       outcome,
		CooldownUntil: cooldown,
	}); err != nil {
		return replaceFailure(
			result,
			"grok_admission_release_failed",
			"internal",
			"Grok provider admission outcome could not be persisted",
			[]string{"cdp workflow agent grok doctor --json"},
		)
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
		state,
		webagent.StageObserveTerminal,
		readModeFromData(data),
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		nil,
		conversation,
		data,
		nil,
	)
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
		nil,
		conversation,
		failure.code,
		failure.errClass,
		failure.message,
		formatRetryAt(failure.retryAt),
		data,
		nextCommands,
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func readModeFromData(data any) string {
	mode := ""
	switch value := data.(type) {
	case ConversationListData:
		mode = value.ReadMode
	case ConversationDetailData:
		mode = value.ReadMode
	}
	if strings.TrimSpace(mode) == "" {
		return "not_started"
	}
	return mode
}

func readNextCommands(
	operation webagent.Operation,
	conversation *webagent.ConversationRef,
) []string {
	commands := []string{"cdp workflow agent grok auth refresh --json"}
	if conversation != nil && conversation.ID != "" {
		commands = append(
			commands,
			fmt.Sprintf(
				"cdp workflow agent grok conversations detail %s --json",
				conversation.ID,
			),
		)
	}
	return commands
}

func internalReadFailure(message string) *readFailure {
	return &readFailure{
		code:     "grok_read_internal",
		errClass: "internal",
		message:  message,
	}
}

func retryAtFromHeader(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return now.Add(time.Minute)
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return parsed
	}
	var seconds int
	if _, err := fmt.Sscanf(value, "%d", &seconds); err == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	return now.Add(time.Minute)
}

func nowForRead(config ReadConfig) time.Time {
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

func fingerprintPrompt(prompt string) string {
	sum := sha256.Sum256([]byte(normalizePromptIdentity(prompt)))
	return hex.EncodeToString(sum[:])
}
