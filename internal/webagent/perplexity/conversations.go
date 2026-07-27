package perplexity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	ConversationListSchemaVersion   = "perplexity-conversation-list/v1"
	ConversationDetailSchemaVersion = "perplexity-conversation-detail/v1"
	maxPerplexityResponseBytes      = 16 << 20
)

var (
	conversationIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,256}$`)
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
	detailBlockUseCases = []string{
		"answer_modes",
		"media_items",
		"knowledge_cards",
		"inline_entity_cards",
		"place_widgets",
		"finance_widgets",
		"sports_widgets",
		"news_widgets",
		"shopping_widgets",
		"jobs_widgets",
		"search_result_widgets",
		"inline_images",
		"inline_assets",
		"placeholder_cards",
		"diff_blocks",
		"inline_knowledge_cards",
		"entity_group_v2",
		"refinement_filters",
		"canvas_mode",
		"maps_preview",
		"answer_tabs",
		"price_comparison_widgets",
		"preserve_latex",
		"generic_onboarding_widgets",
		"in_context_suggestions",
		"pending_followups",
		"inline_claims",
		"unified_assets",
		"workflow_steps",
		"workflow_widgets",
		"navigation_results",
		"background_agents",
	}
)

type ReadConfig struct {
	Store               *Store
	HTTPClient          *http.Client
	BuildCommit         string
	Now                 func() time.Time
	AwaitDelays         []time.Duration
	NewRenderedFallback RenderedFallbackFactory
}

type ConversationSummary struct {
	ID        string         `json:"conversation_id"`
	Title     string         `json:"title"`
	UpdatedAt string         `json:"updated_at,omitempty"`
	Preview   string         `json:"preview,omitempty"`
	URL       string         `json:"url"`
	Metadata  map[string]any `json:"metadata"`
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
				code:     "perplexity_invalid_list_limit",
				errClass: "usage",
				message:  "Perplexity conversation limit must be between 0 and 100",
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
	data, failure := fetchConversationList(ctx, config, template, limit)
	if failure != nil &&
		failure.code == "perplexity_browser_context_required" &&
		config.NewRenderedFallback != nil {
		rendered, closeRendered, err := config.NewRenderedFallback(ctx)
		if err == nil {
			rendered.BuildCommit = config.BuildCommit
			result := renderedListConversations(ctx, rendered, runID, limit)
			if closeRendered != nil {
				_ = closeRendered(context.Background())
			}
			return result
		}
		failure = &readFailure{
			code:       "perplexity_rendered_fallback_unavailable",
			errClass:   "connection",
			message:    "Perplexity rendered conversation-list fallback is unavailable",
			statusCode: failure.statusCode,
		}
	}
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
				code:     "perplexity_invalid_conversation_id",
				errClass: "usage",
				message:  "Perplexity conversation id contains unsupported characters",
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
				code:     "perplexity_await_canceled",
				errClass: "timeout",
				message:  "Perplexity conversation await was canceled before terminal detail",
			}
		case <-timer.C:
		}
		if failure != nil {
			break
		}
	}
	if failure != nil &&
		failure.code == "perplexity_browser_context_required" &&
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
			return result
		}
		failure = &readFailure{
			code:       "perplexity_rendered_fallback_unavailable",
			errClass:   "connection",
			message:    "Perplexity rendered conversation-detail fallback is unavailable",
			statusCode: failure.statusCode,
		}
	}
	if data.Metadata == nil {
		data.Metadata = map[string]any{}
	}
	data.Metadata["detail_read_attempts"] = attempt
	if failure != nil {
		return readFailureResult(
			runID,
			config.BuildCommit,
			operation,
			*failure,
			data,
			conversation,
		)
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
		conversation,
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
		ReadMode:      "candidate_http",
	}
	request, err := newPerplexityRequest(
		ctx,
		template,
		http.MethodGet,
		template.URL,
	)
	if err != nil {
		return data, internalReadFailure(
			"Perplexity conversation-list request could not be prepared",
		)
	}
	response, failure := doPerplexityRequest(config, request)
	if failure != nil {
		data.StatusCode = failure.statusCode
		return data, failure
	}
	defer response.Body.Close()
	var payload []struct {
		ID        string `json:"uuid"`
		Title     string `json:"title"`
		UpdatedAt string `json:"updated_datetime"`
		Preview   string `json:"answer_preview"`
		Link      string `json:"link"`
		Status    string `json:"status"`
		ModeType  any    `json:"mode_type"`
		Variant   string `json:"variant"`
	}
	if err := decodeBoundedJSON(response.Body, &payload); err != nil {
		data.StatusCode = response.StatusCode
		return data, &readFailure{
			code:       "perplexity_invalid_list_response",
			errClass:   "provider",
			message:    "Perplexity conversation list returned an invalid bounded response",
			statusCode: response.StatusCode,
		}
	}
	conversations := make([]ConversationSummary, 0, min(limit, len(payload)))
	for _, raw := range payload {
		id := strings.TrimSpace(raw.ID)
		if !conversationIDPattern.MatchString(id) {
			continue
		}
		conversationURL := strings.TrimSpace(raw.Link)
		if strings.HasPrefix(conversationURL, "/") {
			conversationURL = Origin + conversationURL
		}
		if conversationURL == "" {
			conversationURL = Origin + "/search/" + id
		}
		conversations = append(conversations, ConversationSummary{
			ID:        id,
			Title:     strings.TrimSpace(raw.Title),
			UpdatedAt: strings.TrimSpace(raw.UpdatedAt),
			Preview:   strings.TrimSpace(raw.Preview),
			URL:       conversationURL,
			Metadata: map[string]any{
				"status":    strings.TrimSpace(raw.Status),
				"mode_type": raw.ModeType,
				"variant":   strings.TrimSpace(raw.Variant),
			},
		})
		if len(conversations) == limit {
			break
		}
	}
	data.StatusCode = response.StatusCode
	data.Conversations = conversations
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
			"source":     "observed_thread_detail_http",
			"formatting": "provider_stored_markdown",
		},
	}
	detailURL := perplexityDetailURL(conversationID)
	request, err := newPerplexityRequest(
		ctx,
		template,
		http.MethodGet,
		detailURL,
	)
	if err != nil {
		return data, internalReadFailure(
			"Perplexity conversation-detail request could not be prepared",
		)
	}
	response, failure := doPerplexityRequest(config, request)
	if failure != nil {
		data.StatusCode = failure.statusCode
		return data, failure
	}
	defer response.Body.Close()
	var payload struct {
		Entries []struct {
			ID         string `json:"uuid"`
			Query      string `json:"query_str"`
			Status     string `json:"status"`
			StepType   string `json:"step_type"`
			Mode       string `json:"mode"`
			SearchMode string `json:"search_mode"`
			Blocks     []struct {
				IntendedUsage string `json:"intended_usage"`
				MarkdownBlock *struct {
					Answer   string `json:"answer"`
					Progress string `json:"progress"`
				} `json:"markdown_block"`
			} `json:"blocks"`
		} `json:"entries"`
	}
	if err := decodeBoundedJSON(response.Body, &payload); err != nil {
		data.StatusCode = response.StatusCode
		return data, &readFailure{
			code:       "perplexity_invalid_detail_response",
			errClass:   "provider",
			message:    "Perplexity conversation detail returned an invalid bounded response",
			statusCode: response.StatusCode,
		}
	}
	data.StatusCode = response.StatusCode
	data.ReadMode = "observed_stable_http"
	observedPrompt := ""
	for _, entry := range payload.Entries {
		if query := strings.TrimSpace(entry.Query); query != "" {
			observedPrompt = entry.Query
			break
		}
	}
	for entryIndex := len(payload.Entries) - 1; entryIndex >= 0; entryIndex-- {
		entry := payload.Entries[entryIndex]
		if !strings.EqualFold(strings.TrimSpace(entry.Status), "COMPLETED") ||
			!strings.EqualFold(strings.TrimSpace(entry.StepType), "FINAL") {
			continue
		}
		for blockIndex := len(entry.Blocks) - 1; blockIndex >= 0; blockIndex-- {
			block := entry.Blocks[blockIndex]
			if block.IntendedUsage != "ask_text" || block.MarkdownBlock == nil {
				continue
			}
			text := strings.TrimSpace(block.MarkdownBlock.Answer)
			progress := strings.ToUpper(strings.TrimSpace(block.MarkdownBlock.Progress))
			if text == "" ||
				(progress != "" && progress != "DONE" && progress != "COMPLETED") {
				continue
			}
			prompt := entry.Query
			if strings.TrimSpace(prompt) == "" {
				prompt = observedPrompt
			}
			if strings.TrimSpace(prompt) != "" {
				data.promptText = prompt
				data.Metadata["prompt_fingerprint"] = fingerprintPrompt(prompt)
			}
			data.Text = text
			data.CompletionState = "terminal"
			data.Metadata["entry_id"] = strings.TrimSpace(entry.ID)
			data.Metadata["mode"] = strings.TrimSpace(entry.Mode)
			data.Metadata["search_mode"] = strings.TrimSpace(entry.SearchMode)
			data.Metadata["provider_status"] = strings.TrimSpace(entry.Status)
			data.Metadata["step_type"] = strings.TrimSpace(entry.StepType)
			return data, nil
		}
	}
	if strings.TrimSpace(observedPrompt) != "" {
		data.promptText = observedPrompt
		data.Metadata["prompt_fingerprint"] = fingerprintPrompt(observedPrompt)
	}
	return data, nil
}

func perplexityDetailURL(conversationID string) string {
	query := url.Values{}
	query.Set("with_parent_info", "true")
	query.Set("with_schematized_response", "true")
	query.Set("version", "2.18")
	query.Set("source", "default")
	query.Set("limit", "10")
	query.Set("offset", "0")
	query.Set("from_first", "true")
	for _, value := range detailBlockUseCases {
		query.Add("supported_block_use_cases", value)
	}
	return Origin + "/rest/thread/" + url.PathEscape(conversationID) + "?" + query.Encode()
}

func newPerplexityRequest(
	ctx context.Context,
	template RequestTemplate,
	method string,
	rawURL string,
) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
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

func doPerplexityRequest(
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
			code:     "perplexity_http_unavailable",
			errClass: "connection",
			message:  "Perplexity candidate HTTP read is unavailable",
		}
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	_ = response.Body.Close()
	failure := &readFailure{
		code:       "perplexity_http_failed",
		errClass:   "provider",
		message:    fmt.Sprintf("Perplexity candidate HTTP read returned status %d", response.StatusCode),
		statusCode: response.StatusCode,
	}
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		failure.code = "perplexity_browser_context_required"
		failure.errClass = "auth"
		failure.message = "Perplexity candidate HTTP read requires headed browser context"
	case http.StatusTooManyRequests:
		failure.code = "perplexity_rate_limited"
		failure.errClass = "rate_limit"
		failure.message = "Perplexity candidate HTTP read was rate limited"
		failure.retryAt = retryAtFromHeader(
			response.Header.Get("Retry-After"),
			nowForRead(config),
		)
	}
	return nil, failure
}

func decodeBoundedJSON(body io.Reader, target any) error {
	limited := io.LimitReader(body, maxPerplexityResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxPerplexityResponseBytes {
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
			"Perplexity owner-only auth state is unavailable",
		)
	}
	status := config.Store.AuthStatus(ctx, nowForRead(config), DefaultAuthTTL)
	if !status.Ready {
		return RequestTemplate{}, &readFailure{
			code:     "perplexity_auth_" + status.State,
			errClass: "auth",
			message:  "Perplexity auth evidence is not ready for candidate HTTP reads",
		}
	}
	template, err := config.Store.LoadTemplate(ctx)
	if err != nil {
		return RequestTemplate{}, internalReadFailure(
			"Perplexity owner-only auth state could not be loaded",
		)
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
	commands := []string{"cdp workflow agent perplexity auth refresh --json"}
	if conversation != nil && conversation.ID != "" {
		commands = append(
			commands,
			fmt.Sprintf(
				"cdp workflow agent perplexity conversations detail %s --json",
				conversation.ID,
			),
		)
	}
	return commands
}

func internalReadFailure(message string) *readFailure {
	return &readFailure{
		code:     "perplexity_read_internal",
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

func normalizePromptIdentity(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[index] = ""
		}
	}
	return strings.Join(lines, "\n")
}
