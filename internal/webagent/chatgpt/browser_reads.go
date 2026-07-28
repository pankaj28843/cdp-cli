package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	browserReadMode = "observed_stable_http_via_headed_browser_context"
)

type browserFetchResult struct {
	OK                 bool   `json:"ok"`
	StatusCode         int    `json:"status_code"`
	Body               string `json:"body"`
	BodyBytes          int    `json:"body_bytes"`
	RetryAfter         string `json:"retry_after"`
	ResponseObservedAt string `json:"response_observed_at"`
	Error              string `json:"error"`
}

func listConversationsViaBrowser(
	ctx context.Context,
	config ReadConfig,
	runID string,
	limit int,
	offset int,
) webagent.Result {
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
			template, failure := prepareBrowserRead(
				ctx,
				*config.BrowserConfig,
				config.Store,
				lease,
			)
			if failure != nil {
				return browserReadFailureResult(
					runID, config.BuildCommit, webagent.OperationConversationsList,
					webagent.StageAttached, target, pending, *failure, data, nil,
				)
			}
			if failure = commitBrowserReadPreparation(ctx, lease); failure != nil {
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
				data.StatusCode = failure.statusCode
				return browserReadFailureResult(
					runID, config.BuildCommit, webagent.OperationConversationsList,
					webagent.StageObserveTerminal, target, pending, *failure, data, nil,
				)
			}
			var payload map[string]any
			if err := decodeBoundedJSON(strings.NewReader(response.Body), &payload); err != nil {
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
				return browserReadFailureResult(
					runID, config.BuildCommit, webagent.OperationConversationsList,
					webagent.StageObserveTerminal, target, pending, *failure, data, nil,
				)
			}
			data.ReadMode = browserReadMode
			if err := lease.MarkTerminal(context.Background()); err != nil {
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
	runID string,
	conversationID string,
	await bool,
	deadline time.Time,
	stopState *awaitStopState,
) webagent.Result {
	operation := webagent.OperationConversationsDetail
	if await {
		operation = webagent.OperationConversationsAwait
	}
	conversation := conversationRef(conversationID)
	data := newConversationDetailData(
		conversationID,
		"candidate_browser_context_http",
		"headed_browser_fetch",
	)
	path := "/backend-api/conversation/" + url.PathEscape(conversationID)
	endpoint := Origin + path
	switch stopState.observe() {
	case awaitCancellation:
		canceled := awaitCanceledFailure()
		return readFailureResult(
			runID,
			config.BuildCommit,
			operation,
			*canceled,
			data,
			conversation,
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
	statePersistenceFailed := false
	result := runOwned(
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
			incompleteResult := func() webagent.Result {
				result := operationSuccess(
					runID,
					config.BuildCommit,
					operation,
					webagent.StageAttached,
					readModeFromData(data),
					target,
					pending,
					data,
					nil,
				)
				result.State = webagent.StateIncomplete
				result.Conversation = conversation
				return result
			}
			switch stopState.observe() {
			case awaitCancellation:
				return browserReadFailureResult(
					runID, config.BuildCommit, operation,
					webagent.StageAttached, target, pending,
					*awaitCanceledFailure(), data, conversation,
				)
			case awaitDeadline:
				return incompleteResult()
			}
			template, failure := prepareBrowserRead(
				ctx,
				*config.BrowserConfig,
				config.Store,
				lease,
			)
			switch stopState.observe() {
			case awaitCancellation:
				return browserReadFailureResult(
					runID, config.BuildCommit, operation,
					webagent.StageAttached, target, pending,
					*awaitCanceledFailure(), data, conversation,
				)
			case awaitDeadline:
				return incompleteResult()
			}
			if failure != nil {
				return browserReadFailureResult(
					runID, config.BuildCommit, operation,
					webagent.StageAttached, target, pending, *failure, data, conversation,
				)
			}
			failure = commitBrowserReadPreparation(ctx, lease)
			switch stopState.observe() {
			case awaitCancellation:
				return browserReadFailureResult(
					runID, config.BuildCommit, operation,
					webagent.StageAttached, target, pending,
					*awaitCanceledFailure(), data, conversation,
				)
			case awaitDeadline:
				return incompleteResult()
			}
			if failure != nil {
				return browserReadFailureResult(
					runID, config.BuildCommit, operation,
					webagent.StageAttached, target, pending,
					*failure, data, conversation,
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
				response, fetchFailure := browserFetch(
					ctx,
					lease.Session(),
					template,
					endpoint,
					ConversationDetailRoute,
				)
				fetchStop := stopState.observe()
				nextData := newConversationDetailData(
					conversationID,
					"candidate_browser_context_http",
					"headed_browser_fetch",
				)
				nextFailure := fetchFailure
				if fetchFailure != nil {
					nextData.StatusCode = fetchFailure.statusCode
				} else {
					var payload map[string]any
					if err := decodeBoundedJSON(
						strings.NewReader(response.Body),
						&payload,
					); err != nil {
						nextFailure = &readFailure{
							code:       "chatgpt_invalid_detail_response",
							errClass:   "provider",
							message:    "ChatGPT conversation detail returned an invalid bounded response",
							statusCode: response.StatusCode,
						}
						nextData.StatusCode = response.StatusCode
					} else {
						nextData, nextFailure =
							parseConversationDetailPayload(
								nextData,
								payload,
								response.StatusCode,
							)
					}
				}
				if nextData.StatusCode != 0 {
					nextData.ReadMode = browserReadMode
				}
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
				rateLimited := failure != nil &&
					failure.errClass == "rate_limit"
				if failure != nil && (!rateLimited || !await) {
					break
				}
				if failure == nil &&
					(data.CompletionState != "incomplete" || !await) {
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
			if stop == awaitDeadline && data.StatusCode == 0 {
				failure = nil
			}
			if failure != nil {
				return browserReadFailureResult(
					runID, config.BuildCommit, operation,
					webagent.StageObserveTerminal, target, pending,
					*failure, data, conversation,
				)
			}
			state := webagent.StateTerminal
			if data.CompletionState != "terminal" {
				state = webagent.StateIncomplete
				if err := lease.MarkIncomplete(context.Background()); err != nil {
					statePersistenceFailed = true
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
			} else if err := lease.MarkTerminal(context.Background()); err != nil {
				statePersistenceFailed = true
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
			stopState.observe()
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
	if result.Cleanup.State == webagent.CleanupFailed ||
		statePersistenceFailed {
		return result
	}
	switch stopState.observe() {
	case awaitCancellation:
		return replaceWithAwaitCanceled(
			result, operation, conversationID,
		)
	case awaitDeadline:
		return incompleteAwaitAtDeadline(result)
	}
	return result
}

func prepareBrowserRead(
	ctx context.Context,
	config BrowserConfig,
	store *Store,
	lease *browserflow.Lease,
) (RequestTemplate, *readFailure) {
	if store == nil {
		return RequestTemplate{}, internalReadFailure(
			"ChatGPT owner-only auth state is unavailable",
		)
	}
	session := lease.Session()
	userAgent, err := prepareAuthObservation(
		ctx,
		config.Client,
		session,
	)
	if err != nil {
		return RequestTemplate{}, &readFailure{
			code:     "chatgpt_browser_read_prepare_failed",
			errClass: "connection",
			message:  "ChatGPT stable browser-context read could not prepare the exact target",
		}
	}
	signedIn, err := signedInUIObservedWithReadiness(ctx, session)
	if err != nil {
		return RequestTemplate{}, &readFailure{
			code:     "chatgpt_browser_read_readiness_failed",
			errClass: "connection",
			message:  "ChatGPT stable browser-context read could not complete its bounded reload sequence",
		}
	}
	if !signedIn {
		return RequestTemplate{}, &readFailure{
			code:     "chatgpt_auth_evidence_not_observed",
			errClass: "auth",
			message:  "ChatGPT auth UI evidence was not observed after initial load, reload, and cache-bypassing hard reload; the browser session may still be active",
		}
	}
	cookies, err := readCookies(ctx, session)
	if err != nil {
		return RequestTemplate{}, &readFailure{
			code:     "chatgpt_browser_read_cookie_observation_failed",
			errClass: "connection",
			message:  "ChatGPT browser-context cookies could not be observed",
		}
	}
	if !hasSessionCookie(cookies) {
		return RequestTemplate{}, &readFailure{
			code:     "chatgpt_auth_evidence_not_observed",
			errClass: "auth",
			message:  "ChatGPT signed-in session-cookie evidence was not observed; the browser session may still be active",
		}
	}
	existing := loadExistingTemplate(ctx, store)
	observation, found, err := observeReadRequest(
		ctx,
		config.Client,
		session,
		defaultObservationAttempts,
		defaultObservationTimeout,
	)
	if err != nil {
		return RequestTemplate{}, &readFailure{
			code:     "chatgpt_browser_read_request_observation_failed",
			errClass: "connection",
			message:  "ChatGPT fresh conversation-read request observation failed on the exact headed target",
		}
	}
	now := time.Now().UTC()
	template, persist := browserReadTemplate(
		existing,
		observation,
		found,
		cookies,
		userAgent,
		now,
	)
	if persist {
		err = store.SaveTemplate(ctx, template)
	}
	if err != nil {
		return RequestTemplate{}, internalReadFailure(
			"ChatGPT refreshed browser-context read evidence could not be persisted",
		)
	}
	return template, nil
}

func commitBrowserReadPreparation(
	ctx context.Context,
	lease *browserflow.Lease,
) *readFailure {
	if ctx.Err() != nil {
		return internalReadFailure(
			"ChatGPT stable browser-context read preparation was canceled",
		)
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

func browserReadTemplate(
	existing *RequestTemplate,
	observation readObservation,
	found bool,
	cookies map[string]string,
	userAgent string,
	capturedAt time.Time,
) (RequestTemplate, bool) {
	if found {
		if observation.Headers == nil {
			observation.Headers = map[string]string{}
		}
		observation.Headers["user-agent"] = userAgent
		return RequestTemplate{
			SchemaVersion:    AuthTemplateSchemaVersion,
			Method:           http.MethodGet,
			URL:              observation.URL,
			Headers:          observation.Headers,
			Cookies:          cookies,
			CookieHeader:     observation.CookieHeader,
			BrowserUserAgent: userAgent,
			CapturedAt:       capturedAt.Format(time.RFC3339Nano),
			Source:           "headed-cdp-observed-read-request",
		}, true
	}
	if existing != nil {
		template := *existing
		template.Headers = maps.Clone(existing.Headers)
		if template.Headers == nil {
			template.Headers = map[string]string{}
		}
		template.Headers["user-agent"] = userAgent
		template.Cookies = maps.Clone(cookies)
		template.BrowserUserAgent = userAgent
		return template, false
	}
	return RequestTemplate{
		SchemaVersion:    AuthTemplateSchemaVersion,
		Method:           http.MethodGet,
		URL:              Origin + ConversationListPath,
		Headers:          map[string]string{"user-agent": userAgent},
		Cookies:          maps.Clone(cookies),
		BrowserUserAgent: userAgent,
		CapturedAt:       capturedAt.Format(time.RFC3339Nano),
		Source:           "headed-browser-session-only",
	}, false
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
	    const responseObservedAt = new Date().toISOString();
	    const declaredHeader = response.headers.get('content-length') || '';
	    const declared = Number(declaredHeader);
	    if (declaredHeader !== '' &&
	        Number.isFinite(declared) &&
	        declared > %d) {
	      if (response.body) {
	        try { await response.body.cancel(); } catch (_) {}
	      }
	      return {
	        ok: false,
	        status_code: response.status,
	        body: '',
	        body_bytes: declared,
	        retry_after: response.headers.get('retry-after') || '',
	        response_observed_at: responseObservedAt,
	        error: 'response_too_large'
	      };
	    }
	    if (!response.body) {
	      return {
	        ok: response.status === 200,
	        status_code: response.status,
	        body: '',
	        body_bytes: 0,
	        retry_after: response.headers.get('retry-after') || '',
	        response_observed_at: responseObservedAt,
	        error: ''
	      };
	    }
	    const reader = response.body.getReader();
	    const chunks = [];
	    let total = 0;
	    while (true) {
	      const next = await reader.read();
	      if (next.done) break;
	      if (!(next.value instanceof Uint8Array)) {
	        try { await reader.cancel(); } catch (_) {}
	        throw new Error('invalid_response_chunk');
	      }
	      total += next.value.byteLength;
	      if (total > %d) {
	        try { await reader.cancel(); } catch (_) {}
	        return {
	          ok: false,
	          status_code: response.status,
	          body: '',
	          body_bytes: total,
	          retry_after: response.headers.get('retry-after') || '',
	          response_observed_at: responseObservedAt,
	          error: 'response_too_large'
	        };
	      }
	      chunks.push(next.value);
	    }
	    const bytes = new Uint8Array(total);
	    let offset = 0;
	    for (const chunk of chunks) {
	      bytes.set(chunk, offset);
	      offset += chunk.byteLength;
	    }
	    const body = new TextDecoder().decode(bytes);
	    return {
	      ok: response.status === 200,
	      status_code: response.status,
	      body,
	      body_bytes: total,
	      retry_after: response.headers.get('retry-after') || '',
	      response_observed_at: responseObservedAt,
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
	})()`,
		encodedEndpoint,
		encodedHeaders,
		maxChatGPTResponseBytes,
		maxChatGPTResponseBytes,
	)
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
		retryBase := time.Now().UTC()
		if observedAt, err := time.Parse(
			time.RFC3339Nano,
			response.ResponseObservedAt,
		); err == nil {
			retryBase = observedAt.UTC()
		}
		failure.retryAt, failure.retryAuthoritative = retryAtFromHeader(
			response.RetryAfter,
			retryBase,
		)
	case 0:
		failure.code = "chatgpt_browser_fetch_unavailable"
		failure.errClass = "connection"
		failure.message = "ChatGPT stable browser-context HTTP fetch failed"
	}
	if response.Error == "response_too_large" &&
		response.StatusCode != http.StatusTooManyRequests {
		failure.code = "chatgpt_browser_fetch_response_too_large"
		failure.errClass = "provider"
		failure.message = "ChatGPT stable browser-context response exceeded its bound"
	}
	return response, failure
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
		stage, readModeFromData(data),
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
