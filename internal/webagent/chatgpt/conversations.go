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
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
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
	maxConversationAttachments      = 64
	maxAttachmentSourceRunes        = 4096
	maxAttachmentAltRunes           = 1024
	maxAttachmentNameRunes          = 512
	maxAttachmentMetadataRunes      = 256
	maxAttachmentDimension          = 1 << 20
	maxAttachmentSizeBytes          = 1 << 50

	conversationCompletionIncomplete            = "incomplete"
	conversationCompletionTerminal              = "terminal"
	conversationCompletionTerminalNoAnswer      = "terminal_no_answer"
	conversationCompletionReasonStoppedThinking = "stopped_thinking"
)

var (
	conversationIDPattern               = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	attachmentSignedURIParameterPattern = regexp.MustCompile(
		`(?i)[a-z][a-z0-9+.-]*:[^[:space:]<>"']*[?#][^[:space:]<>"']+`,
	)
	errResponseBodyReadIncomplete = errors.New("response body read did not complete")
	errAwaitDeadlineElapsed       = errors.New(
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
	SchemaVersion    string                   `json:"schema_version"`
	StatusCode       int                      `json:"status_code"`
	ConversationID   string                   `json:"conversation_id"`
	Text             string                   `json:"text"`
	Attachments      []ConversationAttachment `json:"attachments"`
	CompletionState  string                   `json:"completion_state"`
	CompletionReason string                   `json:"completion_reason,omitempty"`
	ReadMode         string                   `json:"read_mode"`
	Metadata         map[string]any           `json:"metadata"`
}

type ConversationAttachment struct {
	Kind               string `json:"kind"`
	Alt                string `json:"alt,omitempty"`
	Source             string `json:"source,omitempty"`
	FileID             string `json:"file_id,omitempty"`
	FileName           string `json:"file_name,omitempty"`
	MIMEType           string `json:"mime_type,omitempty"`
	SizeBytes          int64  `json:"size_bytes,omitempty"`
	Width              int    `json:"width,omitempty"`
	Height             int    `json:"height,omitempty"`
	sourceIdentityHash string
	sourceLocator      string
	sandboxLocator     string
	messageID          string
}

type readFailure struct {
	code               string
	errClass           string
	message            string
	retryAt            time.Time
	retryAuthoritative bool
	statusCode         int
	nextCommands       []string
}

type awaitStop uint8

const (
	awaitContinue awaitStop = iota
	awaitDeadline
	awaitCancellation
)

type awaitStopState struct {
	parentCtx context.Context
	activeCtx context.Context
	config    ReadConfig
	await     bool
	deadline  time.Time
	observed  awaitStop
}

func (s *awaitStopState) observe() awaitStop {
	if s.observed == awaitContinue {
		s.observed = classifyAwaitStop(
			s.parentCtx,
			s.activeCtx,
			s.config,
			s.await,
			s.deadline,
		)
	}
	return s.observed
}

func (s *awaitStopState) claim(stop awaitStop) awaitStop {
	if s.observed == awaitContinue && stop != awaitContinue {
		s.observed = stop
	}
	return s.observed
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
	} else if operation == webagent.OperationAttachmentsDownload {
		schema = AttachmentBatchSchemaVersion
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
	return listConversations(
		ctx,
		config,
		webagent.NewRunID(),
		limit,
		offset,
	)
}

func listConversations(
	ctx context.Context,
	config ReadConfig,
	runID string,
	limit int,
	offset int,
) webagent.Result {
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
		direct := listConversations(
			ctx,
			directConfig,
			runID,
			limit,
			offset,
		)
		if direct.OK || !browserReadFallbackEligible(direct) {
			return direct
		}
		browserConfig, failure := resolveBrowserFallback(ctx, config)
		if failure != nil {
			return mergeDirectBrowserListFallback(
				direct,
				readFailureResult(
					direct.Evidence.RunID,
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
			)
		}
		config.BrowserConfig = browserConfig
		config.BrowserFallback = nil
		return mergeDirectBrowserListFallback(
			direct,
			listConversationsViaBrowser(
				ctx,
				config,
				direct.Evidence.RunID,
				limit,
				offset,
			),
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
	parentCtx := ctx
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
	stopState := &awaitStopState{
		parentCtx: parentCtx,
		activeCtx: ctx,
		config:    config,
		await:     await,
		deadline:  deadline,
	}
	return readConversationUntil(
		ctx,
		config,
		conversationID,
		await,
		deadline,
		stopState,
	)
}

func readConversationUntil(
	ctx context.Context,
	config ReadConfig,
	conversationID string,
	await bool,
	deadline time.Time,
	stopState *awaitStopState,
) webagent.Result {
	operation := webagent.OperationConversationsDetail
	if await {
		operation = webagent.OperationConversationsAwait
	}
	conversationID = strings.TrimSpace(conversationID)
	if !conversationIDPattern.MatchString(conversationID) {
		return readFailureResult(
			webagent.NewRunID(),
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
		renderedProbeAttempts := 0
		for {
			directConfig := config
			directConfig.BrowserConfig = nil
			directConfig.BrowserFallback = nil
			direct := readConversationUntil(
				ctx,
				directConfig,
				conversationID,
				await,
				deadline,
				stopState,
			)
			switch stopState.observe() {
			case awaitCancellation:
				return replaceWithAwaitCanceled(
					direct, operation, conversationID,
				)
			case awaitDeadline:
				return incompleteAwaitAtDeadline(direct)
			}
			renderedNoAnswerProbe :=
				direct.OK && terminalNoAnswerCandidateData(direct.Data)
			if direct.OK && !renderedNoAnswerProbe {
				return direct
			}
			if !renderedNoAnswerProbe && !browserReadFallbackEligible(direct) {
				return direct
			}
			browserConfig, fallbackFailure := resolveBrowserFallback(
				ctx,
				config,
			)
			switch stopState.observe() {
			case awaitCancellation:
				return replaceWithAwaitCanceled(
					direct, operation, conversationID,
				)
			case awaitDeadline:
				return incompleteAwaitAtDeadline(direct)
			}
			if fallbackFailure != nil {
				if renderedNoAnswerProbe {
					direct = recordRenderedNoAnswerProbeUnavailable(direct)
					if !await {
						return direct
					}
					delays := config.AwaitDelays
					if len(delays) == 0 {
						delays = defaultAwaitDelays
					}
					renderedProbeAttempts++
					delay, deadlineBound, ok := nextConversationAwaitDelay(
						config,
						nil,
						delays,
						renderedProbeAttempts,
						deadline,
					)
					if !ok {
						stopState.claim(awaitDeadline)
						return incompleteAwaitAtDeadline(direct)
					}
					if err := waitReadDelay(ctx, config, delay); err != nil {
						switch stopState.observe() {
						case awaitCancellation:
							return replaceWithAwaitCanceled(
								direct,
								operation,
								conversationID,
							)
						case awaitDeadline:
							return incompleteAwaitAtDeadline(direct)
						default:
							return replaceFailure(
								direct,
								"chatgpt_read_internal",
								"internal",
								"ChatGPT conversation await wait failed",
								readNextCommands(
									operation,
									conversationRef(conversationID),
								),
							)
						}
					}
					if deadlineBound {
						stopState.claim(awaitDeadline)
						return incompleteAwaitAtDeadline(direct)
					}
					continue
				}
				return recordDirectReadFallback(
					replaceFailure(
						direct,
						fallbackFailure.code,
						fallbackFailure.errClass,
						fallbackFailure.message,
						readNextCommands(
							operation,
							conversationRef(conversationID),
						),
					),
					direct,
				)
			}
			config.BrowserConfig = browserConfig
			config.BrowserFallback = nil
			browserResult := conversationViaBrowser(
				ctx,
				config,
				direct.Evidence.RunID,
				conversationID,
				await,
				deadline,
				stopState,
			)
			return mergeDirectBrowserFallback(direct, browserResult)
		}
	}
	runID := webagent.NewRunID()
	conversation := conversationRef(conversationID)
	data := newConversationDetailData(
		conversationID,
		"candidate_http",
		"",
	)
	template, failure := loadFreshReadTemplate(ctx, config)
	switch stopState.observe() {
	case awaitCancellation:
		failure = awaitCanceledFailure()
		return readFailureResult(
			runID, config.BuildCommit, operation, *failure, data, conversation,
		)
	case awaitDeadline:
		return readSuccessResult(
			runID,
			config.BuildCommit,
			operation,
			webagent.StateIncomplete,
			data,
			conversation,
		)
	}
	if failure != nil {
		return readFailureResult(
			runID, config.BuildCommit, operation, *failure, data, conversation,
		)
	}
	delays := config.AwaitDelays
	if len(delays) == 0 {
		delays = defaultAwaitDelays
	}
	attempts := 0
	stop := awaitContinue
	for {
		stop = stopState.observe()
		if stop != awaitContinue {
			break
		}
		attempts++
		nextData, nextFailure := fetchConversationDetail(
			ctx,
			config,
			template,
			conversationID,
		)
		fetchStop := stopState.observe()
		if nextData.StatusCode != 0 || data.StatusCode == 0 {
			data, failure = nextData, nextFailure
		}
		if fetchStop != awaitContinue {
			stop = fetchStop
			if stop == awaitDeadline && data.StatusCode == 0 {
				failure = nil
			}
			break
		}
		if nextData.StatusCode == 0 {
			break
		}
		rateLimited := failure != nil && failure.errClass == "rate_limit"
		if failure != nil && (!rateLimited || !await) {
			break
		}
		if failure == nil &&
			(data.CompletionState != conversationCompletionIncomplete ||
				terminalNoAnswerCandidateData(data) ||
				!await) {
			break
		}
		delay, deadlineBound, ok := nextConversationAwaitDelay(
			config,
			failure,
			delays,
			attempts,
			deadline,
		)
		if !ok {
			break
		}
		err := waitReadDelay(ctx, config, delay)
		if deadlineBound && err == nil {
			stop = stopState.claim(awaitDeadline)
			break
		}
		if err != nil {
			stop = stopState.observe()
			if stop == awaitContinue && failure == nil {
				failure = internalReadFailure(
					"ChatGPT conversation await wait failed",
				)
			}
			break
		}
	}
	data.Metadata["detail_read_attempts"] = attempts
	if stop == awaitContinue {
		stop = stopState.observe()
	}
	if stop == awaitCancellation {
		canceled := awaitCanceledFailure()
		if failure != nil {
			canceled.retryAt = failure.retryAt
		}
		failure = canceled
	} else if stop == awaitDeadline && data.StatusCode == 0 {
		failure = nil
	}
	if failure != nil {
		return readFailureResult(
			runID, config.BuildCommit, operation, *failure, data, conversation,
		)
	}
	state := webagent.StateTerminal
	if !terminalConversationCompletion(data.CompletionState) {
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
	if ctx.Err() != nil {
		return nil, &readFailure{
			code:     "chatgpt_browser_fallback_unavailable",
			errClass: "connection",
			message:  "ChatGPT headed read fallback was canceled before initialization",
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
) (time.Duration, bool, bool) {
	if len(delays) == 0 || attempt < 1 || remaining <= 0 {
		return 0, false, false
	}
	index := attempt - 1
	if index >= len(delays) {
		index = len(delays) - 1
	}
	delay := delays[index]
	if delay <= 0 {
		return 0, false, false
	}
	deadlineBound := delay >= remaining
	if delay > remaining {
		delay = remaining
	}
	return delay, deadlineBound, delay > 0
}

func nextConversationAwaitDelay(
	config ReadConfig,
	failure *readFailure,
	delays []time.Duration,
	attempt int,
	deadline time.Time,
) (time.Duration, bool, bool) {
	now := nowForRead(config)
	if failure != nil && failure.errClass == "rate_limit" {
		if failure.retryAuthoritative &&
			failure.retryAt.After(now) &&
			failure.retryAt.Before(deadline) {
			return failure.retryAt.Sub(now), false, true
		}
	}
	return nextAwaitDelay(
		delays,
		attempt,
		deadline.Sub(now),
	)
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

func classifyAwaitStop(
	parentCtx context.Context,
	activeCtx context.Context,
	config ReadConfig,
	await bool,
	deadline time.Time,
) awaitStop {
	if !await {
		return awaitContinue
	}
	cause := context.Cause(activeCtx)
	if errors.Is(cause, errAwaitDeadlineElapsed) {
		return awaitDeadline
	}
	if cause != nil || parentCtx.Err() != nil {
		return awaitCancellation
	}
	if !nowForRead(config).Before(deadline) {
		return awaitDeadline
	}
	return awaitContinue
}

func awaitCanceledFailure() *readFailure {
	return &readFailure{
		code:     "chatgpt_await_canceled",
		errClass: "timeout",
		message:  "ChatGPT conversation await was canceled before terminal detail",
	}
}

func replaceWithAwaitCanceled(
	result webagent.Result,
	operation webagent.Operation,
	conversationID string,
) webagent.Result {
	failure := awaitCanceledFailure()
	return replaceFailure(
		result,
		failure.code,
		failure.errClass,
		failure.message,
		readNextCommands(operation, conversationRef(conversationID)),
	)
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
		if errors.Is(err, errResponseBodyReadIncomplete) {
			return data, &readFailure{
				code:     "chatgpt_http_unavailable",
				errClass: "connection",
				message:  "ChatGPT conversation list body could not be read completely",
			}
		}
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
	data := newConversationDetailData(
		conversationID,
		"candidate_http",
		"",
	)
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
		if errors.Is(err, errResponseBodyReadIncomplete) {
			return data, &readFailure{
				code:     "chatgpt_http_unavailable",
				errClass: "connection",
				message:  "ChatGPT conversation detail body could not be read completely",
			}
		}
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

func newConversationDetailData(
	conversationID string,
	readMode string,
	transport string,
) ConversationDetailData {
	data := ConversationDetailData{
		SchemaVersion:   ConversationDetailSchemaVersion,
		ConversationID:  conversationID,
		Attachments:     []ConversationAttachment{},
		CompletionState: conversationCompletionIncomplete,
		ReadMode:        readMode,
		Metadata: map[string]any{
			"source": "hydrated_conversation_detail",
		},
	}
	if transport != "" {
		data.Metadata["transport"] = transport
	}
	return data
}

func terminalNoAnswerCandidateData(value any) bool {
	var data ConversationDetailData
	switch candidate := value.(type) {
	case ConversationDetailData:
		data = candidate
	case *ConversationDetailData:
		if candidate == nil {
			return false
		}
		data = *candidate
	default:
		return false
	}
	if data.CompletionState != conversationCompletionIncomplete ||
		strings.TrimSpace(data.Text) != "" ||
		len(data.Attachments) != 0 ||
		data.Metadata == nil {
		return false
	}
	candidate, _ := data.Metadata["terminal_no_answer_candidate"].(bool)
	return candidate
}

func terminalConversationCompletion(completionState string) bool {
	return completionState == conversationCompletionTerminal ||
		completionState == conversationCompletionTerminalNoAnswer
}

func recordRenderedNoAnswerProbeUnavailable(
	result webagent.Result,
) webagent.Result {
	data, ok := result.Data.(ConversationDetailData)
	if !ok {
		return result
	}
	if data.Metadata == nil {
		data.Metadata = map[string]any{}
	}
	data.Metadata["rendered_terminal_no_answer_confirmation"] = "unavailable"
	result.Data = data
	return result
}

func parseConversationDetailPayload(
	data ConversationDetailData,
	payload map[string]any,
	statusCode int,
) (ConversationDetailData, *readFailure) {
	data.StatusCode = statusCode
	data.ReadMode = "observed_stable_http"
	if !providerConversationIdentityMatches(payload, data.ConversationID) {
		return data, &readFailure{
			code:       "chatgpt_conversation_identity_mismatch",
			errClass:   "provider",
			message:    "ChatGPT conversation detail did not match the requested conversation",
			statusCode: statusCode,
		}
	}
	extracted := extractConversationText(payload)
	data.Text = extracted.text
	data.Attachments = extracted.attachments
	data.CompletionState = extracted.completionState
	for key, value := range extracted.metadata {
		data.Metadata[key] = value
	}
	return data, nil
}

func providerConversationIdentityMatches(
	payload map[string]any,
	conversationID string,
) bool {
	for _, key := range []string{"conversation_id", "id"} {
		value, present := payload[key]
		if !present {
			continue
		}
		id, valid := value.(string)
		if !valid ||
			id == "" ||
			!conversationIDPattern.MatchString(id) ||
			id != conversationID {
			return false
		}
	}
	return true
}

type extractedConversation struct {
	text            string
	attachments     []ConversationAttachment
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
		attachments:     []ConversationAttachment{},
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
	type pathNode struct {
		id  string
		raw map[string]any
	}
	nodes := make([]pathNode, 0)
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
		nodes = append(nodes, pathNode{id: current, raw: raw})
		current, _ = raw["parent"].(string)
	}
	if strings.TrimSpace(prompt) != "" {
		result.metadata["prompt_fingerprint"] = fingerprintPrompt(prompt)
	}
	for index, node := range nodes {
		message, _ := node.raw["message"].(map[string]any)
		role := messageRole(message)
		if role != "assistant" && role != "tool" {
			continue
		}
		text := strings.TrimSpace(messageText(message, true))
		attachments, attachmentsTruncated := conversationAttachments(message)
		if conversationIDPattern.MatchString(node.id) {
			for attachmentIndex := range attachments {
				attachments[attachmentIndex].messageID = node.id
			}
		}
		if index == 0 &&
			terminalNoAnswerPayloadCandidate(
				text,
				attachments,
				message,
				activity,
			) {
			result.metadata["assistant_is_current_node"] = true
			result.metadata["terminal_no_answer_candidate"] = true
			result.metadata["terminal_no_answer_candidate_reason"] =
				"finished_empty_current_assistant"
			copyResultMetadata(result.metadata, message)
			return result
		}
		if role == "tool" {
			// Current ChatGPT image/file tools persist the answer as a
			// finished tool message. It can sit behind an assistant reasoning
			// recap, so restricting this walk to assistant messages loses a
			// real attachment and falsely reports an incomplete conversation.
			if !terminalToolAttachmentValid(attachments, message) {
				continue
			}
		} else if !terminalAssistantContentValid(text, attachments, message) {
			continue
		}
		result.text = text
		result.attachments = attachments
		if len(attachments) > 0 {
			result.metadata["attachment_count"] = len(attachments)
		}
		if attachmentsTruncated {
			result.metadata["attachments_truncated"] = true
		}
		result.metadata["assistant_is_current_node"] = index == 0
		result.metadata["output_role"] = role
		if (index == 0 || role == "tool") &&
			(activity == conversationActivityAbsent ||
				activity == conversationActivityInactive) &&
			message["status"] == "finished_successfully" &&
			(message["end_turn"] == true || role == "tool") {
			result.completionState = conversationCompletionTerminal
		}
		copyResultMetadata(result.metadata, message)
		return result
	}
	return result
}

func terminalToolAttachmentValid(
	attachments []ConversationAttachment,
	message map[string]any,
) bool {
	return len(attachments) > 0 &&
		message["status"] == "finished_successfully" &&
		terminalAssistantEnvelopeValid(message)
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
	case "4", "COMPLETE", "COMPLETED", "FINISHED", "FINISHED_SUCCESSFULLY",
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

func conversationAttachments(
	message map[string]any,
) ([]ConversationAttachment, bool) {
	candidates := make([]ConversationAttachment, 0)
	appendAttachment := func(attachment ConversationAttachment) {
		if !conversationAttachmentUsable(attachment) {
			return
		}
		candidates = append(candidates, attachment)
	}
	appendValues := func(value any, kindHint string) {
		switch values := value.(type) {
		case []any:
			for _, value := range values {
				switch typed := value.(type) {
				case map[string]any:
					if attachment, ok := conversationAttachmentFromRaw(
						typed,
						kindHint,
					); ok {
						appendAttachment(attachment)
					}
				case string:
					for _, attachment := range sandboxAttachments(typed) {
						appendAttachment(attachment)
					}
				}
			}
		case map[string]any:
			if attachment, ok := conversationAttachmentFromRaw(
				values,
				kindHint,
			); ok {
				appendAttachment(attachment)
			}
		case string:
			for _, attachment := range sandboxAttachments(values) {
				appendAttachment(attachment)
			}
		}
	}

	metadata, _ := message["metadata"].(map[string]any)
	if metadata != nil {
		appendValues(metadata["attachments"], "file")
		appendValues(metadata["files"], "file")
		appendValues(metadata["images"], "image")
	}
	content, _ := message["content"].(map[string]any)
	if content != nil {
		appendValues(content["parts"], "")
		appendValues(content["attachments"], "file")
		appendValues(content["files"], "file")
	}

	sort.Slice(candidates, func(first int, second int) bool {
		return conversationAttachmentLess(candidates[first], candidates[second])
	})
	attachments := make(
		[]ConversationAttachment,
		0,
		min(len(candidates), maxConversationAttachments),
	)
	truncated := false
	for _, candidate := range candidates {
		merged := candidate
		matched := false
		for index := 0; index < len(attachments); {
			if !conversationAttachmentsMatch(attachments[index], merged) {
				index++
				continue
			}
			matched = true
			merged = mergeConversationAttachment(attachments[index], merged)
			attachments = append(
				attachments[:index],
				attachments[index+1:]...,
			)
			// Safe partial records can reveal another compatible identity.
			index = 0
		}
		if !matched && len(attachments) >= maxConversationAttachments {
			truncated = true
			continue
		}
		attachments = append(attachments, merged)
	}
	sort.Slice(attachments, func(first int, second int) bool {
		return conversationAttachmentLess(attachments[first], attachments[second])
	})
	return attachments, truncated
}

func conversationAttachmentFromRaw(
	raw map[string]any,
	kindHint string,
) (ConversationAttachment, bool) {
	metadata, _ := raw["metadata"].(map[string]any)
	contentType := attachmentString(
		raw,
		metadata,
		maxAttachmentMetadataRunes,
		"content_type",
		"attachment_type",
		"type",
		"kind",
	)
	mimeType := stableAttachmentMIMEType(
		attachmentString(
			raw,
			metadata,
			maxAttachmentMetadataRunes,
			"mime_type",
			"mimeType",
		),
	)
	width := attachmentPositiveInt64(
		raw,
		metadata,
		maxAttachmentDimension,
		"width",
		"pixel_width",
		"container_pixel_width",
	)
	height := attachmentPositiveInt64(
		raw,
		metadata,
		maxAttachmentDimension,
		"height",
		"pixel_height",
		"container_pixel_height",
	)
	kind := normalizedAttachmentKind(
		kindHint,
		contentType,
		mimeType,
		raw,
		width,
		height,
	)
	if kind == "" {
		return ConversationAttachment{}, false
	}
	sourceRaw := attachmentSourceValue(raw)
	if sourceRaw == "" && metadata != nil {
		sourceRaw = attachmentSourceValue(metadata)
	}
	source, sourceIdentityHash := stableAttachmentSourceParts(sourceRaw)
	attachment := ConversationAttachment{
		Kind: kind,
		Alt: stableAttachmentAlt(
			attachmentString(
				raw,
				metadata,
				maxAttachmentAltRunes,
				"alt_text",
				"alt",
				"description",
			),
		),
		Source: source,
		FileID: stableAttachmentID(
			attachmentString(
				raw,
				metadata,
				maxAttachmentMetadataRunes,
				"file_id",
				"fileId",
				"id",
			),
		),
		FileName: stableAttachmentName(
			attachmentString(
				raw,
				metadata,
				maxAttachmentNameRunes,
				"file_name",
				"filename",
				"name",
			),
		),
		MIMEType: mimeType,
		SizeBytes: attachmentPositiveInt64(
			raw,
			metadata,
			maxAttachmentSizeBytes,
			"size_bytes",
			"file_size_bytes",
			"size",
		),
		Width:              int(width),
		Height:             int(height),
		sourceIdentityHash: sourceIdentityHash,
		sourceLocator:      privateAttachmentSourceLocator(sourceRaw, source),
		sandboxLocator: privateAttachmentSandboxLocator(
			raw,
			metadata,
			sourceRaw,
			source,
		),
	}
	if attachment.Source == "sandbox_artifact" &&
		attachment.FileName == "" {
		attachment.FileName = sandboxAttachmentName(raw)
		if attachment.FileName == "" && metadata != nil {
			attachment.FileName = sandboxAttachmentName(metadata)
		}
	}
	return attachment, conversationAttachmentUsable(attachment)
}

func privateAttachmentSourceLocator(raw string, publicSource string) string {
	if publicSource == "" {
		return ""
	}
	raw = boundedAttachmentString(raw, maxAttachmentSourceRunes)
	if raw == "" {
		return ""
	}
	if publicSource != "sandbox_artifact" {
		return raw
	}
	return normalizedPrivateSandboxLocator(raw)
}

func privateAttachmentSandboxLocator(
	raw map[string]any,
	metadata map[string]any,
	sourceRaw string,
	publicSource string,
) string {
	for _, values := range []map[string]any{raw, metadata} {
		value, _ := values["sandbox_path"].(string)
		if locator := normalizedPrivateSandboxLocator(value); locator != "" {
			return locator
		}
	}
	if publicSource == "sandbox_artifact" {
		return normalizedPrivateSandboxLocator(sourceRaw)
	}
	return ""
}

func normalizedPrivateSandboxLocator(raw string) string {
	raw = boundedAttachmentString(raw, maxAttachmentSourceRunes)
	raw = strings.TrimPrefix(raw, "sandbox:")
	normalized, ok := normalizeSandboxPath(raw)
	if !ok {
		return ""
	}
	return "sandbox:" + normalized
}

func normalizedAttachmentKind(
	kindHint string,
	contentType string,
	mimeType string,
	raw map[string]any,
	width int64,
	height int64,
) string {
	kindHint = strings.ToLower(strings.TrimSpace(kindHint))
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if kindHint == "image" ||
		strings.Contains(contentType, "image") ||
		strings.HasPrefix(mimeType, "image/") ||
		raw["image_url"] != nil ||
		raw["image_asset_pointer"] != nil ||
		width > 0 ||
		height > 0 {
		return "image"
	}
	if kindHint == "file" ||
		strings.Contains(contentType, "file") ||
		strings.Contains(contentType, "asset") ||
		mimeType != "" ||
		raw["asset_pointer"] != nil ||
		raw["file_asset_pointer"] != nil ||
		raw["file_id"] != nil ||
		raw["file_name"] != nil ||
		raw["filename"] != nil {
		return "file"
	}
	return ""
}

func attachmentString(
	raw map[string]any,
	metadata map[string]any,
	maxRunes int,
	keys ...string,
) string {
	for _, values := range []map[string]any{raw, metadata} {
		for _, key := range keys {
			value, _ := values[key].(string)
			if value = boundedAttachmentString(value, maxRunes); value != "" {
				return value
			}
		}
	}
	return ""
}

func attachmentSourceValue(raw map[string]any) string {
	for _, key := range []string{
		"asset_pointer",
		"image_asset_pointer",
		"file_asset_pointer",
		"source",
		"download_url",
		"url",
		"image_url",
		"sandbox_path",
	} {
		value := raw[key]
		if nested, ok := value.(map[string]any); ok {
			value = nested["url"]
			if value == nil {
				value = nested["asset_pointer"]
			}
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func stableAttachmentSource(value string) string {
	source, _ := stableAttachmentSourceParts(value)
	return source
}

func stableAttachmentSourceParts(value string) (string, string) {
	value = boundedAttachmentString(value, maxAttachmentSourceRunes)
	if value == "" {
		return "", ""
	}
	normalized := strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	if strings.HasPrefix(normalized, "sandbox:") ||
		normalized == "/mnt/data" ||
		strings.HasPrefix(normalized, "/mnt/data/") {
		return "sandbox_artifact", ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil {
		return "", ""
	}
	if privateAttachmentPath(parsed.Path) ||
		(parsed.Path != "" && path.Clean(parsed.Path) != parsed.Path) {
		return "", ""
	}
	switch strings.ToLower(parsed.Scheme) {
	case "sediment", "file-service":
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", ""
		}
		return value, ""
	case "https":
		if parsed.Host == "" {
			return "", ""
		}
		sourceIdentityHash := ""
		if parsed.RawQuery != "" || parsed.ForceQuery {
			identityURL := *parsed
			identityURL.Fragment = ""
			identityURL.RawFragment = ""
			sum := sha256.Sum256([]byte(identityURL.String()))
			sourceIdentityHash = hex.EncodeToString(sum[:])
		}
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		parsed.RawFragment = ""
		return parsed.String(), sourceIdentityHash
	case "":
		if parsed.RawQuery != "" ||
			parsed.Fragment != "" ||
			strings.ContainsAny(value, " \t\r\n") ||
			!strings.HasPrefix(parsed.Path, "/backend-api/") {
			return "", ""
		}
		return value, ""
	default:
		return "", ""
	}
}

func stableAttachmentAlt(value string) string {
	value = boundedAttachmentString(value, maxAttachmentAltRunes)
	if value == "" {
		return ""
	}
	normalized := strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	if strings.Contains(normalized, "sandbox:") ||
		strings.Contains(normalized, "file:/") ||
		privateAttachmentText(value) ||
		attachmentSignedURIParameterPattern.MatchString(value) {
		return ""
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" &&
		(parsed.RawQuery != "" || parsed.Fragment != "") {
		return ""
	}
	return value
}

func stableAttachmentID(value string) string {
	for _, character := range value {
		if unicode.IsLetter(character) ||
			unicode.IsDigit(character) ||
			strings.ContainsRune("-_.:", character) {
			continue
		}
		return ""
	}
	return value
}

func stableAttachmentName(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		value = parsed.Path
	}
	value = path.Base(value)
	if value == "." || value == "/" || strings.ContainsAny(value, "?#") {
		return ""
	}
	return boundedAttachmentString(value, maxAttachmentNameRunes)
}

func stableAttachmentMIMEType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || !strings.Contains(mediaType, "/") {
		return ""
	}
	return strings.ToLower(mediaType)
}

func privateAttachmentPath(value string) bool {
	value = strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	for _, prefix := range []string{
		"/users/",
		"/" + "home/",
		"/private/",
		"/root/",
		"/tmp/",
		"/var/folders/",
		"/var/tmp/",
		"/mnt/",
		"c:/users/",
	} {
		if value == strings.TrimSuffix(prefix, "/") ||
			strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func privateAttachmentText(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	tokens := strings.FieldsFunc(value, func(character rune) bool {
		return unicode.IsSpace(character) ||
			strings.ContainsRune(`()[]{}<>"',;=`, character)
	})
	for _, token := range tokens {
		token = strings.Trim(token, ".!?")
		if localAttachmentPathToken(token) {
			return true
		}
	}
	return false
}

func localAttachmentPathToken(value string) bool {
	value = strings.ToLower(value)
	if strings.HasPrefix(value, "~/") {
		return true
	}
	if len(value) >= 3 &&
		value[0] >= 'a' && value[0] <= 'z' &&
		value[1] == ':' && value[2] == '/' {
		return true
	}
	return len(value) > 1 && strings.HasPrefix(value, "/")
}

func sandboxAttachments(text string) []ConversationAttachment {
	attachments := []ConversationAttachment{}
	seen := map[string]bool{}
	for _, pattern := range sandboxPathPatterns {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			normalized, ok := normalizeSandboxPath(match[1])
			if !ok || seen[normalized] {
				continue
			}
			seen[normalized] = true
			attachments = append(attachments, ConversationAttachment{
				Kind:           "file",
				Source:         "sandbox_artifact",
				FileName:       path.Base(normalized),
				sourceLocator:  "sandbox:" + normalized,
				sandboxLocator: "sandbox:" + normalized,
			})
		}
	}
	return attachments
}

func sandboxAttachmentName(raw map[string]any) string {
	value := attachmentSourceValue(raw)
	value = strings.TrimPrefix(value, "sandbox:")
	normalized, ok := normalizeSandboxPath(value)
	if !ok {
		return ""
	}
	return path.Base(normalized)
}

func boundedAttachmentString(value string, maxRunes int) string {
	value = cleanSingleLine(value)
	if value == "" || maxRunes < 1 {
		return ""
	}
	if len([]rune(value)) > maxRunes {
		return ""
	}
	return value
}

func attachmentPositiveInt64(
	raw map[string]any,
	metadata map[string]any,
	maxValue int64,
	keys ...string,
) int64 {
	for _, values := range []map[string]any{raw, metadata} {
		for _, key := range keys {
			if value, ok := positiveAttachmentInt64(
				values[key],
				maxValue,
			); ok {
				return value
			}
		}
	}
	return 0
}

func positiveAttachmentInt64(value any, maxValue int64) (int64, bool) {
	var parsed int64
	switch typed := value.(type) {
	case float64:
		if typed <= 0 || typed > float64(maxValue) {
			return 0, false
		}
		parsed = int64(typed)
		if float64(parsed) != typed {
			return 0, false
		}
	case float32:
		if typed <= 0 || float64(typed) > float64(maxValue) {
			return 0, false
		}
		parsed = int64(typed)
		if float32(parsed) != typed {
			return 0, false
		}
	case int:
		parsed = int64(typed)
	case int64:
		parsed = typed
	case json.Number:
		parsedValue, err := strconv.ParseInt(typed.String(), 10, 64)
		if err != nil {
			return 0, false
		}
		parsed = parsedValue
	case string:
		parsedValue, err := strconv.ParseInt(
			strings.TrimSpace(typed),
			10,
			64,
		)
		if err != nil {
			return 0, false
		}
		parsed = parsedValue
	default:
		return 0, false
	}
	return parsed, parsed > 0 && parsed <= maxValue
}

func conversationAttachmentUsable(attachment ConversationAttachment) bool {
	if attachment.Kind != "image" && attachment.Kind != "file" {
		return false
	}
	return attachment.Source != "" ||
		attachment.FileID != "" ||
		attachment.FileName != "" ||
		attachment.MIMEType != "" ||
		attachment.Alt != "" ||
		attachment.SizeBytes != 0 ||
		attachment.Width != 0 ||
		attachment.Height != 0
}

func conversationAttachmentLess(
	first ConversationAttachment,
	second ConversationAttachment,
) bool {
	if first.Kind != second.Kind {
		return first.Kind < second.Kind
	}
	if first.FileID != second.FileID {
		return first.FileID < second.FileID
	}
	if first.Source != second.Source {
		return first.Source < second.Source
	}
	if first.sourceIdentityHash != second.sourceIdentityHash {
		return first.sourceIdentityHash < second.sourceIdentityHash
	}
	firstSandbox := comparableAttachmentSandboxLocator(first)
	secondSandbox := comparableAttachmentSandboxLocator(second)
	if firstSandbox != secondSandbox {
		return firstSandbox < secondSandbox
	}
	if first.FileName != second.FileName {
		return first.FileName < second.FileName
	}
	if first.MIMEType != second.MIMEType {
		return first.MIMEType < second.MIMEType
	}
	if first.Alt != second.Alt {
		return first.Alt < second.Alt
	}
	if first.SizeBytes != second.SizeBytes {
		return first.SizeBytes < second.SizeBytes
	}
	if first.Width != second.Width {
		return first.Width < second.Width
	}
	return first.Height < second.Height
}

func conversationAttachmentsMatch(
	current ConversationAttachment,
	candidate ConversationAttachment,
) bool {
	if current.Kind != candidate.Kind ||
		!conversationAttachmentUsable(current) ||
		!conversationAttachmentUsable(candidate) {
		return false
	}
	fileIDMatches := false
	if current.FileID != "" && candidate.FileID != "" {
		if current.FileID != candidate.FileID {
			return false
		}
		fileIDMatches = true
	}
	currentSandbox := comparableAttachmentSandboxLocator(current)
	candidateSandbox := comparableAttachmentSandboxLocator(candidate)
	sandboxMatches := false
	if currentSandbox != "" && candidateSandbox != "" {
		if currentSandbox != candidateSandbox {
			return false
		}
		sandboxMatches = true
	}
	currentSource := comparableAttachmentSource(current)
	candidateSource := comparableAttachmentSource(candidate)
	sourceMatches := false
	if currentSource != "" && candidateSource != "" {
		if currentSource != candidateSource && !sandboxMatches {
			return false
		}
		sourceMatches = currentSource == candidateSource
	}
	fileNameMatches := false
	if current.FileName != "" && candidate.FileName != "" {
		fileNameMatches = strings.EqualFold(
			current.FileName,
			candidate.FileName,
		)
	}
	return fileIDMatches || sandboxMatches || sourceMatches || fileNameMatches
}

func comparableAttachmentSandboxLocator(
	attachment ConversationAttachment,
) string {
	if attachment.sandboxLocator != "" {
		return attachment.sandboxLocator
	}
	if strings.HasPrefix(attachment.sourceLocator, "sandbox:") {
		return attachment.sourceLocator
	}
	return ""
}

func comparableAttachmentSource(attachment ConversationAttachment) string {
	if attachment.sourceIdentityHash != "" {
		return "query:" + attachment.sourceIdentityHash
	}
	source := attachment.Source
	if source == "sandbox_artifact" {
		if strings.HasPrefix(attachment.sourceLocator, "sandbox:") {
			return attachment.sourceLocator
		}
		return ""
	}
	if source == "" {
		return ""
	}
	parsed, err := url.Parse(source)
	if err == nil && parsed.Path == artifactContentRoute {
		return ""
	}
	return source
}

func mergeConversationAttachment(
	current ConversationAttachment,
	candidate ConversationAttachment,
) ConversationAttachment {
	currentSource := current.Source
	current.Alt = preferredAttachmentString(current.Alt, candidate.Alt)
	current.Source = preferredAttachmentSource(current.Source, candidate.Source)
	current.FileID = preferredAttachmentString(current.FileID, candidate.FileID)
	current.FileName = preferredAttachmentString(
		current.FileName,
		candidate.FileName,
	)
	current.MIMEType = preferredAttachmentString(
		current.MIMEType,
		candidate.MIMEType,
	)
	current.SizeBytes = max(current.SizeBytes, candidate.SizeBytes)
	current.Width, current.Height = preferredAttachmentDimensions(
		current.Width,
		current.Height,
		candidate.Width,
		candidate.Height,
	)
	current.sourceIdentityHash = preferredAttachmentString(
		current.sourceIdentityHash,
		candidate.sourceIdentityHash,
	)
	current.sandboxLocator = preferredAttachmentString(
		current.sandboxLocator,
		candidate.sandboxLocator,
	)
	if current.Source == candidate.Source && current.Source != currentSource {
		current.sourceLocator = candidate.sourceLocator
	} else {
		current.sourceLocator = preferredAttachmentString(
			current.sourceLocator,
			candidate.sourceLocator,
		)
	}
	current.messageID = preferredAttachmentString(
		current.messageID,
		candidate.messageID,
	)
	return current
}

func preferredAttachmentDimensions(
	currentWidth int,
	currentHeight int,
	candidateWidth int,
	candidateHeight int,
) (int, int) {
	currentQuality := attachmentDimensionQuality(currentWidth, currentHeight)
	candidateQuality := attachmentDimensionQuality(
		candidateWidth,
		candidateHeight,
	)
	if candidateQuality > currentQuality {
		return candidateWidth, candidateHeight
	}
	if candidateQuality < currentQuality {
		return currentWidth, currentHeight
	}
	currentArea := int64(currentWidth) * int64(currentHeight)
	candidateArea := int64(candidateWidth) * int64(candidateHeight)
	if candidateArea > currentArea ||
		(candidateArea == currentArea && candidateWidth > currentWidth) ||
		(candidateArea == currentArea && candidateWidth == currentWidth &&
			candidateHeight > currentHeight) {
		return candidateWidth, candidateHeight
	}
	return currentWidth, currentHeight
}

func attachmentDimensionQuality(width int, height int) int {
	if width > 0 && height > 0 {
		return 2
	}
	if width > 0 || height > 0 {
		return 1
	}
	return 0
}

func preferredAttachmentString(current string, candidate string) string {
	if current == "" {
		return candidate
	}
	if candidate == "" || current <= candidate {
		return current
	}
	return candidate
}

func preferredAttachmentSource(current string, candidate string) string {
	if current == "sandbox_artifact" && candidate != "" {
		return candidate
	}
	if candidate == "sandbox_artifact" && current != "" {
		return current
	}
	return preferredAttachmentString(current, candidate)
}

func terminalAssistantContentValid(
	text string,
	attachments []ConversationAttachment,
	message map[string]any,
) bool {
	if len(attachments) > 0 {
		return terminalAssistantEnvelopeValid(message)
	}
	return terminalAnswerTextValid(text, message)
}

func terminalAnswerTextValid(text string, message map[string]any) bool {
	text = strings.TrimSpace(text)
	if text == "" || deepResearchControlPayload(text) {
		return false
	}
	if !terminalAssistantEnvelopeValid(message) {
		return false
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

func terminalNoAnswerPayloadCandidate(
	text string,
	attachments []ConversationAttachment,
	message map[string]any,
	activity conversationActivityState,
) bool {
	return strings.TrimSpace(text) == "" &&
		len(attachments) == 0 &&
		(activity == conversationActivityAbsent ||
			activity == conversationActivityInactive) &&
		message["status"] == "finished_successfully" &&
		message["end_turn"] == true &&
		terminalAssistantEnvelopeValid(message)
}

func terminalAssistantEnvelopeValid(message map[string]any) bool {
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
	return true
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
	responseObservedAt := nowForRead(config)
	retryAfter := strings.Join(response.Header.Values("Retry-After"), ", ")
	_, bodyErr := readBoundedBody(response.Body)
	_ = response.Body.Close()
	if errors.Is(bodyErr, errResponseBodyReadIncomplete) {
		return nil, &readFailure{
			code:     "chatgpt_http_unavailable",
			errClass: "connection",
			message:  "ChatGPT candidate HTTP response body could not be read completely",
		}
	}
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
		failure.retryAt, failure.retryAuthoritative = retryAtFromHeader(
			retryAfter,
			responseObservedAt,
		)
	}
	return nil, failure
}

func readBoundedBody(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, maxChatGPTResponseBytes+1)
	data, err := io.ReadAll(limited)
	if len(data) > maxChatGPTResponseBytes {
		return data, fmt.Errorf("response body exceeds its bound")
	}
	if err != nil {
		return data, errors.Join(errResponseBodyReadIncomplete, err)
	}
	return data, nil
}

func decodeBoundedJSON(body io.Reader, target any) error {
	data, err := readBoundedBody(body)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("response body is empty")
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

func mergeDirectBrowserListFallback(
	direct webagent.Result,
	browser webagent.Result,
) webagent.Result {
	data, ok := browser.Data.(ConversationListData)
	directData, directOK := direct.Data.(ConversationListData)
	if !directOK ||
		directData.StatusCode == 0 ||
		(ok && data.StatusCode != 0) {
		return recordDirectReadFallback(browser, direct)
	}
	if browser.Cleanup.State == webagent.CleanupFailed {
		browser.Data = direct.Data
		browser.Evidence.ReadMode = direct.Evidence.ReadMode
		return recordDirectReadFallback(browser, direct)
	}

	merged := direct
	merged.Stage = browser.Stage
	merged.Evidence.BrowserMode = browser.Evidence.BrowserMode
	merged.Evidence.Target = browser.Evidence.Target
	merged.Cleanup = browser.Cleanup
	return recordDirectReadFallback(merged, direct)
}

func mergeDirectBrowserFallback(
	direct webagent.Result,
	browser webagent.Result,
) webagent.Result {
	data, ok := browser.Data.(ConversationDetailData)
	if browser.Cleanup.State == webagent.CleanupFailed {
		if !ok || data.StatusCode == 0 {
			browser.Data = direct.Data
			browser.Evidence.ReadMode = direct.Evidence.ReadMode
		}
		return recordDirectReadFallback(browser, direct)
	}
	if !ok || data.StatusCode != 0 {
		return recordDirectReadFallback(browser, direct)
	}
	directData, directOK := direct.Data.(ConversationDetailData)
	if !directOK || directData.StatusCode == 0 {
		return recordDirectReadFallback(browser, direct)
	}

	merged := direct
	if browser.Error != nil &&
		browser.Error.Code == "chatgpt_await_canceled" {
		merged.OK = browser.OK
		merged.State = browser.State
		merged.Error = browser.Error
		merged.NextCommands = append([]string{}, browser.NextCommands...)
	}
	merged.Stage = browser.Stage
	merged.Evidence.BrowserMode = browser.Evidence.BrowserMode
	merged.Evidence.Target = browser.Evidence.Target
	merged.Cleanup = browser.Cleanup
	return recordDirectReadFallback(merged, direct)
}

func incompleteAwaitAtDeadline(result webagent.Result) webagent.Result {
	data, ok := result.Data.(ConversationDetailData)
	if !ok || data.StatusCode != 0 {
		return result
	}
	result.OK = true
	result.State = webagent.StateIncomplete
	result.Error = nil
	result.NextCommands = []string{}
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

func retryAtFromHeader(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return now.Add(5 * time.Minute), false
	}
	if value[0] >= '0' && value[0] <= '9' {
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil &&
			seconds <= int64(time.Duration(1<<63-1)/time.Second) {
			return now.Add(time.Duration(seconds) * time.Second), true
		}
	}
	return now.Add(5 * time.Minute), false
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
