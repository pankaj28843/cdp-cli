package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const browserReadMode = "observed_stable_http_via_headed_browser_context"

type browserFetchResult struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
	BodyBytes  int    `json:"body_bytes"`
	RetryAfter string `json:"retry_after"`
	Error      string `json:"error"`
}

func listConversationsViaBrowser(
	ctx context.Context,
	config ReadConfig,
	limit int,
	offset int,
) webagent.Result {
	runID := webagent.NewRunID()
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
	data := ConversationListData{
		SchemaVersion: ConversationListSchemaVersion,
		Conversations: []ConversationSummary{},
		ReadMode:      "candidate_browser_context_http",
		Metadata: map[string]any{
			"limit":       limit,
			"offset":      offset,
			"order":       "updated",
			"is_archived": false,
			"is_starred":  false,
			"transport":   "headed_browser_fetch",
		},
	}
	query := url.Values{}
	query.Set("offset", fmt.Sprint(offset))
	query.Set("limit", fmt.Sprint(limit))
	query.Set("order", "updated")
	query.Set("is_archived", "false")
	query.Set("is_starred", "false")
	endpoint := Origin + ConversationListPath + "?" + query.Encode()
	return runOwned(
		ctx,
		*config.BrowserConfig,
		runID,
		webagent.OperationConversationsList,
		"",
		"about:blank",
		"browser_context_stable_http",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			if failure := prepareBrowserRead(ctx, *config.BrowserConfig, lease); failure != nil {
				return browserReadFailureResult(
					runID, config.BuildCommit, webagent.OperationConversationsList,
					webagent.StageAttached, target, pending, *failure, data, nil,
				)
			}
			response, failure := browserFetch(
				ctx,
				lease.Session(),
				template,
				endpoint,
				ConversationListPath,
			)
			if failure != nil {
				_ = lease.MarkIncomplete(context.Background())
				data.StatusCode = failure.statusCode
				return browserReadFailureResult(
					runID, config.BuildCommit, webagent.OperationConversationsList,
					webagent.StageObserveTerminal, target, pending, *failure, data, nil,
				)
			}
			var payload map[string]any
			if err := decodeBoundedJSON(strings.NewReader(response.Body), &payload); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				data.StatusCode = response.StatusCode
				return browserReadFailureResult(
					runID, config.BuildCommit, webagent.OperationConversationsList,
					webagent.StageObserveTerminal, target, pending,
					readFailure{
						code:       "chatgpt_invalid_list_response",
						errClass:   "provider",
						message:    "ChatGPT conversation list returned an invalid bounded response",
						statusCode: response.StatusCode,
					},
					data,
					nil,
				)
			}
			data, failure = parseConversationListPayload(data, payload, response.StatusCode)
			if failure != nil {
				_ = lease.MarkIncomplete(context.Background())
				return browserReadFailureResult(
					runID, config.BuildCommit, webagent.OperationConversationsList,
					webagent.StageObserveTerminal, target, pending, *failure, data, nil,
				)
			}
			data.ReadMode = browserReadMode
			if err := lease.MarkTerminal(ctx); err != nil {
				return browserReadFailureResult(
					runID, config.BuildCommit, webagent.OperationConversationsList,
					webagent.StageObserveTerminal, target, pending,
					*internalReadFailure(
						"ChatGPT conversation-list terminal state could not be persisted",
					),
					data,
					nil,
				)
			}
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationConversationsList,
				webagent.StageObserveTerminal, browserReadMode,
				target, pending, data, nil,
			)
		},
	)
}

func conversationViaBrowser(
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
	conversation := conversationRef(conversationID)
	template, failure := loadFreshReadTemplate(ctx, config)
	if failure != nil {
		return readFailureResult(
			runID,
			config.BuildCommit,
			operation,
			*failure,
			map[string]any{"schema_version": ConversationDetailSchemaVersion},
			conversation,
		)
	}
	data := ConversationDetailData{
		SchemaVersion:   ConversationDetailSchemaVersion,
		ConversationID:  conversationID,
		CompletionState: "incomplete",
		ReadMode:        "candidate_browser_context_http",
		Metadata: map[string]any{
			"source":    "hydrated_conversation_detail",
			"transport": "headed_browser_fetch",
		},
	}
	path := "/backend-api/conversation/" + url.PathEscape(conversationID)
	endpoint := Origin + path
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	return runOwned(
		ctx,
		*config.BrowserConfig,
		runID,
		operation,
		"",
		"about:blank",
		"browser_context_stable_http",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			if failure := prepareBrowserRead(ctx, *config.BrowserConfig, lease); failure != nil {
				return browserReadFailureResult(
					runID, config.BuildCommit, operation,
					webagent.StageAttached, target, pending, *failure, data, conversation,
				)
			}
			delays := config.AwaitDelays
			if len(delays) == 0 {
				delays = defaultAwaitDelays
			}
			deadline := nowForRead(config).Add(timeout)
			attempts := 0
			for {
				attempts++
				response, fetchFailure := browserFetch(
					ctx,
					lease.Session(),
					template,
					endpoint,
					ConversationDetailRoute,
				)
				if fetchFailure != nil {
					failure = fetchFailure
					data.StatusCode = fetchFailure.statusCode
					break
				}
				var payload map[string]any
				if err := decodeBoundedJSON(
					strings.NewReader(response.Body),
					&payload,
				); err != nil {
					failure = &readFailure{
						code:       "chatgpt_invalid_detail_response",
						errClass:   "provider",
						message:    "ChatGPT conversation detail returned an invalid bounded response",
						statusCode: response.StatusCode,
					}
					data.StatusCode = response.StatusCode
					break
				}
				data, failure = parseConversationDetailPayload(
					data,
					payload,
					response.StatusCode,
				)
				if failure != nil ||
					data.CompletionState != "incomplete" ||
					!await ||
					attempts > len(delays) {
					break
				}
				delay := delays[attempts-1]
				if deadline.Sub(nowForRead(config)) <= delay {
					break
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					failure = &readFailure{
						code:     "chatgpt_await_canceled",
						errClass: "timeout",
						message:  "ChatGPT conversation await was canceled before terminal detail",
					}
				case <-timer.C:
				}
				if failure != nil {
					break
				}
			}
			data.ReadMode = browserReadMode
			data.Metadata["detail_read_attempts"] = attempts
			if failure != nil {
				_ = lease.MarkIncomplete(context.Background())
				return browserReadFailureResult(
					runID, config.BuildCommit, operation,
					webagent.StageObserveTerminal, target, pending,
					*failure, data, conversation,
				)
			}
			state := webagent.StateTerminal
			if data.CompletionState != "terminal" {
				state = webagent.StateIncomplete
				if err := lease.MarkIncomplete(ctx); err != nil {
					return browserReadFailureResult(
						runID, config.BuildCommit, operation,
						webagent.StageObserveTerminal, target, pending,
						*internalReadFailure(
							"ChatGPT incomplete detail state could not be persisted",
						),
						data,
						conversation,
					)
				}
			} else if err := lease.MarkTerminal(ctx); err != nil {
				return browserReadFailureResult(
					runID, config.BuildCommit, operation,
					webagent.StageObserveTerminal, target, pending,
					*internalReadFailure(
						"ChatGPT terminal detail state could not be persisted",
					),
					data,
					conversation,
				)
			}
			result := operationSuccess(
				runID, config.BuildCommit, operation,
				webagent.StageObserveTerminal, browserReadMode,
				target, pending, data, nil,
			)
			result.State = state
			result.Conversation = conversation
			return result
		},
	)
}

func prepareBrowserRead(
	ctx context.Context,
	config BrowserConfig,
	lease *browserflow.Lease,
) *readFailure {
	if err := preparePage(ctx, config.Client, lease.Session(), HomeURL); err != nil {
		return &readFailure{
			code:     "chatgpt_browser_read_prepare_failed",
			errClass: "connection",
			message:  "ChatGPT stable browser-context read could not prepare the exact target",
		}
	}
	if !signedInUIObserved(ctx, lease.Session()) {
		return &readFailure{
			code:     "chatgpt_signed_out",
			errClass: "auth",
			message:  "Signed-in ChatGPT UI was not observed before the stable read",
		}
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		return internalReadFailure(
			"ChatGPT stable browser-context read preparation could not be persisted",
		)
	}
	if err := lease.ReleaseInput(); err != nil {
		return internalReadFailure(
			"ChatGPT stable browser-context read could not release the headed input lease",
		)
	}
	return nil
}

func browserFetch(
	ctx context.Context,
	session *cdp.PageSession,
	template RequestTemplate,
	endpoint string,
	targetRoute string,
) (browserFetchResult, *readFailure) {
	headers := browserFetchHeaders(template.Headers, endpoint, targetRoute)
	encodedEndpoint, err := json.Marshal(endpoint)
	if err != nil {
		return browserFetchResult{}, internalReadFailure(
			"ChatGPT stable browser-context URL could not be encoded",
		)
	}
	encodedHeaders, err := json.Marshal(headers)
	if err != nil {
		return browserFetchResult{}, internalReadFailure(
			"ChatGPT stable browser-context headers could not be encoded",
		)
	}
	expression := fmt.Sprintf(`(async () => {
	  try {
	    const response = await fetch(%s, {
	      method: 'GET',
	      headers: %s,
	      credentials: 'include',
	      cache: 'no-store',
	      redirect: 'manual'
	    });
	    const body = await response.text();
	    if (body.length > %d) {
	      return {
	        ok: false,
	        status_code: response.status,
	        body: '',
	        body_bytes: body.length,
	        retry_after: response.headers.get('retry-after') || '',
	        error: 'response_too_large'
	      };
	    }
	    return {
	      ok: response.status === 200,
	      status_code: response.status,
	      body,
	      body_bytes: body.length,
	      retry_after: response.headers.get('retry-after') || '',
	      error: ''
	    };
	  } catch (_) {
	    return {
	      ok: false,
	      status_code: 0,
	      body: '',
	      body_bytes: 0,
	      retry_after: '',
	      error: 'fetch_failed'
	    };
	  }
	})()`, encodedEndpoint, encodedHeaders, maxChatGPTResponseBytes)
	var response browserFetchResult
	if err := evaluateInto(ctx, session, expression, &response); err != nil {
		return browserFetchResult{}, &readFailure{
			code:     "chatgpt_browser_fetch_unavailable",
			errClass: "connection",
			message:  "ChatGPT stable browser-context HTTP read was unavailable",
		}
	}
	if response.OK && response.StatusCode == http.StatusOK {
		return response, nil
	}
	failure := &readFailure{
		code:       "chatgpt_browser_fetch_failed",
		errClass:   "provider",
		message:    fmt.Sprintf("ChatGPT stable browser-context read returned status %d", response.StatusCode),
		statusCode: response.StatusCode,
	}
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		failure.code = "chatgpt_browser_context_auth_failed"
		failure.errClass = "auth"
		failure.message = "ChatGPT stable browser-context read requires refreshed headed auth"
	case http.StatusTooManyRequests:
		failure.code = "chatgpt_rate_limited"
		failure.errClass = "rate_limit"
		failure.message = "ChatGPT stable browser-context read was rate limited"
		failure.retryAt = retryAtFromHeader(response.RetryAfter, time.Now().UTC())
	case 0:
		failure.code = "chatgpt_browser_fetch_unavailable"
		failure.errClass = "connection"
		failure.message = "ChatGPT stable browser-context HTTP fetch failed"
	}
	if response.Error == "response_too_large" {
		failure.code = "chatgpt_browser_fetch_response_too_large"
		failure.errClass = "provider"
		failure.message = "ChatGPT stable browser-context response exceeded its bound"
	}
	return browserFetchResult{}, failure
}

func browserFetchHeaders(
	observed map[string]string,
	endpoint string,
	targetRoute string,
) map[string]string {
	allowed := map[string]bool{
		"accept":                      true,
		"authorization":               true,
		"chatgpt-account-id":          true,
		"oai-client-build-number":     true,
		"oai-client-version":          true,
		"oai-device-id":               true,
		"oai-language":                true,
		"oai-session-id":              true,
		"x-oai-is-client-observation": true,
	}
	headers := map[string]string{}
	for name, value := range observed {
		lower := strings.ToLower(strings.TrimSpace(name))
		if allowed[lower] && strings.TrimSpace(value) != "" {
			headers[lower] = value
		}
	}
	parsed, _ := url.Parse(endpoint)
	headers["accept"] = "*/*"
	headers["x-openai-target-path"] = parsed.Path
	headers["x-openai-target-route"] = targetRoute
	return headers
}

func browserReadFailureResult(
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
	result := operationFailure(
		runID, buildCommit, operation,
		stage, "browser_context_stable_http",
		target, cleanup,
		failure.code, failure.errClass, failure.message,
		data, readNextCommands(operation, conversation),
	)
	result.Conversation = conversation
	if result.Error != nil && !failure.retryAt.IsZero() {
		result.Error.RetryAt = failure.retryAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}
