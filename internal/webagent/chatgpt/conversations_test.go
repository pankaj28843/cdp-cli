package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func fixedHTTPClient(status int, body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: status,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		},
	)}
}

const terminalDetailPayload = `{
  "current_node":"answer",
  "mapping":{
    "answer":{
      "parent":"",
      "message":{
        "author":{"role":"assistant"},
        "status":"finished_successfully",
        "end_turn":true,
        "content":{"content_type":"text","parts":["Terminal review."]}
      }
    }
  }
}`

const incompleteDetailPayload = `{
  "current_node":"tool",
  "mapping":{
    "tool":{
      "parent":"progress",
      "message":{
        "author":{"role":"tool"},
        "content":{"content_type":"text","parts":["work"]}
      }
    },
    "progress":{
      "parent":"prompt",
      "message":{
        "author":{"role":"assistant"},
        "status":"finished_successfully",
        "end_turn":true,
        "content":{"content_type":"text","parts":["Still working."]}
      }
    },
    "prompt":{
      "parent":"",
      "message":{
        "author":{"role":"user"},
        "content":{"content_type":"text","parts":["Review."]}
      }
    }
  }
}`

type authenticatedReadBrowser struct {
	*testsupport.Browser
	events []cdp.Event
}

type cancelOnCloseBrowser struct {
	*authenticatedReadBrowser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBrowser) Call(
	ctx context.Context,
	method string,
	params any,
	result any,
) error {
	err := b.authenticatedReadBrowser.Call(ctx, method, params, result)
	if method == "Target.closeTarget" {
		b.cancel()
	}
	return err
}

type recordingJournal struct {
	browserflow.Journal
	phases     []browserflow.Phase
	beforeSave func(context.Context, browserflow.Record) error
}

func (j *recordingJournal) Save(
	ctx context.Context,
	record browserflow.Record,
) error {
	j.phases = append(j.phases, record.Phase)
	if j.beforeSave != nil {
		if err := j.beforeSave(ctx, record); err != nil {
			return err
		}
	}
	return j.Journal.Save(ctx, record)
}

func newReadTestEngine(
	t *testing.T,
	client cdp.CommandClient,
	journal browserflow.Journal,
) *browserflow.Engine {
	t.Helper()
	engine, err := browserflow.New(browserflow.Config{
		Client:  client,
		Journal: journal,
		Budget: cdp.BrowserResourceBudgetOptions{
			MaxTabs:       15,
			MaxTabsSource: "test",
			MaxWindows:    5,
			BrowserMode:   "headed",
		},
		CloseTimeout:      20 * time.Millisecond,
		ClosePollInterval: time.Millisecond,
		Now:               testsupport.FixedNow,
	})
	if err != nil {
		t.Fatalf("browserflow.New: %v", err)
	}
	return engine
}

func (b *authenticatedReadBrowser) CallSession(
	ctx context.Context,
	sessionID string,
	method string,
	params any,
	result any,
) error {
	if method == "Network.getCookies" {
		return json.Unmarshal(
			[]byte(`{"cookies":[{"name":"__Secure-next-auth.session-token","value":"test-only"}]}`),
			result,
		)
	}
	return b.Browser.CallSession(ctx, sessionID, method, params, result)
}

func (b *authenticatedReadBrowser) ReadEvent(
	ctx context.Context,
) (cdp.Event, error) {
	if len(b.events) > 0 {
		event := b.events[0]
		b.events = b.events[1:]
		return event, nil
	}
	return b.Browser.ReadEvent(ctx)
}

func newAuthenticatedReadBrowser(
	evaluate func(string, *testsupport.Browser) (any, error),
) *authenticatedReadBrowser {
	base := testsupport.NewBrowser("user-page")
	base.Evaluate = evaluate
	return &authenticatedReadBrowser{
		Browser: base,
		events: []cdp.Event{
			{
				SessionID: "session-owned-1",
				Method:    "Network.requestWillBeSent",
				Params: json.RawMessage(`{
				  "requestId":"read-1",
				  "request":{
				    "url":"https://chatgpt.com/backend-api/conversations",
				    "method":"GET",
				    "headers":{
				      "authorization":"Bearer test-only",
				      "cookie":"__Secure-next-auth.session-token=test-only"
				    }
				  }
				}`),
			},
			{
				SessionID: "session-owned-1",
				Method:    "Network.responseReceived",
				Params: json.RawMessage(`{
				  "requestId":"read-1",
				  "response":{
				    "url":"https://chatgpt.com/backend-api/conversations",
				    "status":200
				  }
				}`),
			},
		},
	}
}

func newReadTestStore(t *testing.T, now time.Time) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	template := RequestTemplate{
		SchemaVersion: AuthTemplateSchemaVersion,
		Method:        http.MethodGet,
		URL:           Origin + ConversationListPath,
		Headers: map[string]string{
			"user-agent":    "test-agent",
			"authorization": "Bearer test-only",
		},
		Cookies: map[string]string{
			"__Secure-next-auth.session-token": "test-only",
		},
		CookieHeader:     "__Secure-next-auth.session-token=test-only",
		BrowserUserAgent: "test-agent",
		CapturedAt:       now.Format(time.RFC3339Nano),
		Source:           "headed-cdp-observed-read-request",
	}
	if err := store.SaveTemplate(context.Background(), template); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}
	return store
}

func TestBrowserReadFallbackEligible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		code     string
		errClass string
		want     bool
	}{
		{name: "browser context", code: "chatgpt_browser_context_required", errClass: "auth", want: true},
		{name: "stale auth", code: "chatgpt_auth_stale", errClass: "auth", want: true},
		{name: "HTTP unavailable", code: "chatgpt_http_unavailable", errClass: "connection", want: true},
		{name: "HTTP failure", code: "chatgpt_http_failed", errClass: "provider", want: true},
		{name: "rate limit code", code: "chatgpt_rate_limited", errClass: "provider", want: false},
		{name: "rate limit class", code: "chatgpt_http_failed", errClass: "rate_limit", want: false},
		{name: "usage", code: "chatgpt_invalid_conversation_id", errClass: "usage", want: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := webagent.Result{
				Error: &webagent.OperationError{
					Code:     test.code,
					ErrClass: test.errClass,
				},
			}
			if got := browserReadFallbackEligible(result); got != test.want {
				t.Fatalf("browserReadFallbackEligible() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBrowserReadTemplateRetainsKnownShapeWhenFreshRequestIsAbsent(
	t *testing.T,
) {
	t.Parallel()

	existing := RequestTemplate{
		SchemaVersion: AuthTemplateSchemaVersion,
		Method:        http.MethodGet,
		URL:           Origin + ConversationListPath,
		Headers: map[string]string{
			"authorization": "Bearer retained-test-token",
			"user-agent":    "old-agent",
		},
		Cookies:          map[string]string{"old": "cookie"},
		CookieHeader:     "__Secure-next-auth.session-token=retained-test-cookie",
		BrowserUserAgent: "old-agent",
		CapturedAt:       "2026-07-25T12:00:00Z",
		Source:           "headed-cdp-observed-read-request",
	}
	currentCookies := map[string]string{
		"__Secure-next-auth.session-token": "current-test-cookie",
	}
	template, persist := browserReadTemplate(
		&existing,
		readObservation{},
		false,
		currentCookies,
		"current-agent",
		time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	)

	if persist {
		t.Fatal("retained request shape must not be repersisted as fresh evidence")
	}
	if template.Headers["authorization"] != "Bearer retained-test-token" ||
		template.Headers["user-agent"] != "current-agent" ||
		template.BrowserUserAgent != "current-agent" ||
		template.Cookies["__Secure-next-auth.session-token"] !=
			"current-test-cookie" {
		t.Fatalf("retained browser template = %#v", template)
	}
	if existing.Headers["user-agent"] != "old-agent" ||
		existing.Cookies["old"] != "cookie" {
		t.Fatalf("existing template was mutated: %#v", existing)
	}
}

func TestBrowserReadTemplateAllowsSessionOnlyFetchWithoutCachedShape(
	t *testing.T,
) {
	t.Parallel()

	template, persist := browserReadTemplate(
		nil,
		readObservation{},
		false,
		map[string]string{
			"__Secure-next-auth.session-token": "current-test-cookie",
		},
		"current-agent",
		time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	)

	if persist {
		t.Fatal("session-only browser template must remain ephemeral")
	}
	headers := browserFetchHeaders(
		template.Headers,
		Origin+ConversationListPath,
		ConversationListPath,
	)
	if headers["accept"] != "*/*" ||
		headers["x-openai-target-path"] != ConversationListPath ||
		headers["x-openai-target-route"] != ConversationListPath {
		t.Fatalf("session-only browser headers = %#v", headers)
	}
}

func TestListFallbackKeepsCompletedDirectObservation(t *testing.T) {
	now := time.Now().UTC()
	store := newReadTestStore(t, now)
	fetches := 0
	client := newAuthenticatedReadBrowser(func(
		expression string,
		_ *testsupport.Browser,
	) (any, error) {
		if strings.Contains(expression, "signed_in:") {
			return map[string]any{"signed_in": true, "signed_out": false}, nil
		}
		if strings.Contains(expression, "const response = await fetch") {
			fetches++
			return map[string]any{
				"ok": false, "status_code": 0, "error": "fetch_failed",
			}, nil
		}
		return map[string]any{}, nil
	})
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	result := ListConversations(context.Background(), ReadConfig{
		Store:      store,
		HTTPClient: fixedHTTPClient(http.StatusServiceUnavailable, ""),
		BrowserConfig: &BrowserConfig{
			Client: client, Engine: engine, Journal: journal,
		},
		Now: func() time.Time { return now },
	}, 20, 0)

	data, ok := result.Data.(ConversationListData)
	attempted, _ := data.Metadata["direct_http_attempted"].(bool)
	failureCode, _ := data.Metadata["direct_http_failure_code"].(string)
	record, loadErr := journal.Load(
		context.Background(),
		result.Evidence.RunID,
	)
	if !ok ||
		data.StatusCode != http.StatusServiceUnavailable ||
		!attempted ||
		failureCode != "chatgpt_http_failed" ||
		result.Evidence.ReadMode != data.ReadMode ||
		result.Evidence.Target == nil ||
		result.Cleanup.State != webagent.CleanupClosed ||
		fetches != 1 ||
		loadErr != nil ||
		record.RunID != result.Evidence.RunID ||
		record.TargetID != result.Evidence.Target.TargetID {
		t.Fatalf(
			"fetches=%d load_err=%v record=%+v result=%+v data=%+v",
			fetches,
			loadErr,
			record,
			result,
			data,
		)
	}
}

func TestConversationDetailRejectsMismatchedProviderIdentity(t *testing.T) {
	tests := []map[string]any{
		{"id": 2},
		{"id": " conversation-1 "},
		{
			"conversation_id": "conversation-1",
			"id":              "conversation-2",
		},
	}
	for _, payload := range tests {
		data, failure := parseConversationDetailPayload(
			newConversationDetailData(
				"conversation-1",
				"candidate_http",
				"",
			),
			payload,
			http.StatusOK,
		)
		if failure == nil ||
			failure.code != "chatgpt_conversation_identity_mismatch" ||
			data.ConversationID != "conversation-1" ||
			data.Text != "" {
			t.Fatalf(
				"payload=%v data=%+v failure=%+v",
				payload,
				data,
				failure,
			)
		}
	}
}

func TestListConversationsDirectSuccessDoesNotInitializeBrowserFallback(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store := newReadTestStore(t, now)
	browserFallbackCalled := false
	client := fixedHTTPClient(http.StatusOK, `{"items":[]}`)

	result := ListConversations(context.Background(), ReadConfig{
		Store:      store,
		HTTPClient: client,
		Now:        func() time.Time { return now },
		BrowserFallback: func(context.Context) (*BrowserConfig, error) {
			browserFallbackCalled = true
			return nil, errors.New("browser must remain lazy")
		},
	}, 20, 0)
	if !result.OK {
		t.Fatalf("ListConversations: %+v", result.Error)
	}
	if browserFallbackCalled {
		t.Fatal("healthy direct read initialized the headed browser fallback")
	}
}

func TestListConversationsInterruptedBodyIsStatusZero(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	result := ListConversations(context.Background(), ReadConfig{
		Store: newReadTestStore(t, now),
		HTTPClient: &http.Client{Transport: roundTripFunc(
			func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(io.MultiReader(
						strings.NewReader(`{"items":`),
						iotest.ErrReader(io.ErrUnexpectedEOF),
					)),
				}, nil
			},
		)},
		Now: func() time.Time { return now },
	}, 20, 0)

	data, _ := result.Data.(ConversationListData)
	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_http_unavailable" ||
		data.StatusCode != 0 {
		t.Fatalf("result=%+v data=%+v", result, data)
	}
}

func TestAwaitDirectIncompleteHonorsDeadlineWithoutBrowserFallback(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store := newReadTestStore(t, now)
	client := fixedHTTPClient(http.StatusOK, incompleteDetailPayload)
	browserFallbackCalled := false
	result := AwaitConversation(context.Background(), ReadConfig{
		Store:       store,
		HTTPClient:  client,
		Now:         func() time.Time { return now },
		AwaitDelays: []time.Duration{time.Second},
		Wait: func(_ context.Context, delay time.Duration) error {
			now = now.Add(delay)
			return nil
		},
		BrowserFallback: func(context.Context) (*BrowserConfig, error) {
			browserFallbackCalled = true
			return nil, errors.New("browser must remain lazy")
		},
	}, "conversation-1", 5*time.Second)
	if !result.OK || result.State != webagent.StateIncomplete {
		t.Fatalf("AwaitConversation result = %+v", result)
	}
	if browserFallbackCalled {
		t.Fatal("successful incomplete direct await initialized browser fallback")
	}
	data, ok := result.Data.(ConversationDetailData)
	if !ok {
		t.Fatalf("result data type = %T", result.Data)
	}
	attempts, _ := data.Metadata["detail_read_attempts"].(int)
	if attempts <= 2 {
		t.Fatalf(
			"detail_read_attempts = %d, want repeated capped backoff",
			attempts,
		)
	}
}

func TestAwaitRetriesRateLimitWithinDeadline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		retryAfter []string
		wantWait   time.Duration
	}{
		{
			name:       "valid delay seconds controls retry",
			retryAfter: []string{"1"},
			wantWait:   time.Second,
		},
		{
			name:       "non-fitting provider delay uses local backoff",
			retryAfter: []string{"30"},
			wantWait:   2 * time.Second,
		},
		{
			name:       "duplicate provider delays use local backoff",
			retryAfter: []string{"1", "30"},
			wantWait:   2 * time.Second,
		},
		{
			name: "duplicate fields cannot synthesize an HTTP date",
			retryAfter: []string{
				"Mon",
				"27 Jul 2026 12:00:01 GMT",
			},
			wantWait: 2 * time.Second,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
			store := newReadTestStore(t, now)
			attempts := 0
			client := &http.Client{Transport: roundTripFunc(
				func(*http.Request) (*http.Response, error) {
					attempts++
					if attempts == 1 {
						return &http.Response{
							StatusCode: http.StatusTooManyRequests,
							Header: http.Header{
								"Retry-After": test.retryAfter,
							},
							Body: io.NopCloser(strings.NewReader("")),
						}, nil
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body: io.NopCloser(
							strings.NewReader(terminalDetailPayload),
						),
					}, nil
				},
			)}
			var waits []time.Duration
			result := AwaitConversation(context.Background(), ReadConfig{
				Store:       store,
				HTTPClient:  client,
				Now:         func() time.Time { return now },
				AwaitDelays: []time.Duration{2 * time.Second},
				Wait: func(_ context.Context, delay time.Duration) error {
					waits = append(waits, delay)
					now = now.Add(delay)
					return nil
				},
			}, "conversation-1", 10*time.Second)
			if !result.OK || result.State != webagent.StateTerminal {
				t.Fatalf("AwaitConversation result = %+v", result)
			}
			data, ok := result.Data.(ConversationDetailData)
			if !ok {
				t.Fatalf("result data type = %T", result.Data)
			}
			if attempts != 2 ||
				len(waits) != 1 ||
				waits[0] != test.wantWait ||
				data.Text != "Terminal review." {
				t.Fatalf(
					"attempts=%d waits=%v data=%+v",
					attempts,
					waits,
					data,
				)
			}
		})
	}
}

func TestAwaitCancellationPreservesCompletedRetryEvidence(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	result := AwaitConversation(ctx, ReadConfig{
		Store: newReadTestStore(t, now),
		HTTPClient: &http.Client{Transport: roundTripFunc(
			func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header: http.Header{
						"Retry-After": []string{"30"},
					},
					Body: io.NopCloser(strings.NewReader("")),
				}, nil
			},
		)},
		Now: func() time.Time { return now },
		Wait: func(context.Context, time.Duration) error {
			cancel()
			return context.Canceled
		},
	}, "conversation-1", time.Minute)

	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_await_canceled" ||
		result.Error.RetryAt != now.Add(30*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("result=%+v", result)
	}
}

func TestAwaitPreservesLatestCompletedDirectObservation(t *testing.T) {
	type response struct {
		status  int
		body    string
		bodyErr error
		err     error
	}
	tests := []struct {
		name       string
		responses  []response
		wantOK     bool
		wantState  webagent.State
		wantCode   string
		wantStatus int
		wantText   string
	}{
		{
			name: "status-zero interruption preserves rate limit",
			responses: []response{
				{status: http.StatusTooManyRequests},
				{err: errors.New("test transport failure")},
			},
			wantState:  webagent.StateFailed,
			wantCode:   "chatgpt_rate_limited",
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name: "first interrupted 200 body is status zero",
			responses: []response{
				{
					status:  http.StatusOK,
					body:    `{"current_node":`,
					bodyErr: io.ErrUnexpectedEOF,
				},
			},
			wantState: webagent.StateFailed,
			wantCode:  "chatgpt_http_unavailable",
		},
		{
			name: "interrupted non-200 body is status zero",
			responses: []response{
				{
					status:  http.StatusServiceUnavailable,
					bodyErr: io.ErrUnexpectedEOF,
				},
			},
			wantState: webagent.StateFailed,
			wantCode:  "chatgpt_http_unavailable",
		},
		{
			name: "completed HTTP failure replaces rate limit",
			responses: []response{
				{status: http.StatusTooManyRequests},
				{status: http.StatusServiceUnavailable},
			},
			wantState:  webagent.StateFailed,
			wantCode:   "chatgpt_http_failed",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "interrupted non-200 body preserves incomplete response",
			responses: []response{
				{status: http.StatusOK, body: incompleteDetailPayload},
				{
					status:  http.StatusServiceUnavailable,
					bodyErr: io.ErrUnexpectedEOF,
				},
			},
			wantOK:     true,
			wantState:  webagent.StateIncomplete,
			wantStatus: http.StatusOK,
			wantText:   "Still working.",
		},
		{
			name: "interrupted 200 body preserves incomplete response",
			responses: []response{
				{status: http.StatusOK, body: incompleteDetailPayload},
				{
					status:  http.StatusOK,
					body:    `{"current_node":`,
					bodyErr: io.ErrUnexpectedEOF,
				},
			},
			wantOK:     true,
			wantState:  webagent.StateIncomplete,
			wantStatus: http.StatusOK,
			wantText:   "Still working.",
		},
		{
			name: "oversized 200 body remains a completed observation",
			responses: []response{
				{
					status:  http.StatusOK,
					body:    strings.Repeat("x", maxChatGPTResponseBytes+1),
					bodyErr: io.ErrUnexpectedEOF,
				},
			},
			wantState:  webagent.StateFailed,
			wantCode:   "chatgpt_invalid_detail_response",
			wantStatus: http.StatusOK,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
			store := newReadTestStore(t, now)
			attempts := 0
			client := &http.Client{Transport: roundTripFunc(
				func(*http.Request) (*http.Response, error) {
					if attempts >= len(test.responses) {
						return nil, errors.New("unexpected extra direct fetch")
					}
					next := test.responses[attempts]
					attempts++
					if next.err != nil {
						return nil, next.err
					}
					header := make(http.Header)
					if next.status == http.StatusTooManyRequests {
						header.Set("Retry-After", "0")
					}
					body := io.Reader(strings.NewReader(next.body))
					if next.bodyErr != nil {
						body = io.MultiReader(
							body,
							iotest.ErrReader(next.bodyErr),
						)
					}
					return &http.Response{
						StatusCode: next.status,
						Header:     header,
						Body:       io.NopCloser(body),
					}, nil
				},
			)}
			result := AwaitConversation(context.Background(), ReadConfig{
				Store:       store,
				HTTPClient:  client,
				Now:         func() time.Time { return now },
				AwaitDelays: []time.Duration{time.Millisecond},
				Wait:        func(context.Context, time.Duration) error { return nil },
			}, "conversation-1", time.Second)

			data, _ := result.Data.(ConversationDetailData)
			gotCode := ""
			if result.Error != nil {
				gotCode = result.Error.Code
			}
			if result.OK != test.wantOK ||
				result.State != test.wantState ||
				gotCode != test.wantCode ||
				data.StatusCode != test.wantStatus ||
				data.Text != test.wantText ||
				attempts != len(test.responses) {
				t.Fatalf(
					"attempts=%d result=%+v data=%+v",
					attempts,
					result,
					data,
				)
			}
		})
	}
}

func TestAwaitDeadlineRetainsCompletedDirectResponse(t *testing.T) {
	now := time.Now().UTC()
	store := newReadTestStore(t, now)
	ctx, cancel := context.WithCancel(context.Background())
	deadlineWon := false
	client := &http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			deadlineWon = errors.Is(
				context.Cause(request.Context()),
				errAwaitDeadlineElapsed,
			)
			cancel()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(terminalDetailPayload)),
			}, nil
		},
	)}
	fallbackCalled := false
	result := AwaitConversation(ctx, ReadConfig{
		Store:      store,
		HTTPClient: client,
		BrowserFallback: func(context.Context) (*BrowserConfig, error) {
			fallbackCalled = true
			return nil, errors.New("browser must remain lazy")
		},
	}, "conversation-1", 50*time.Millisecond)

	data, _ := result.Data.(ConversationDetailData)
	if !deadlineWon ||
		!result.OK ||
		result.State != webagent.StateTerminal ||
		result.Error != nil ||
		data.StatusCode != http.StatusOK ||
		data.Text != "Terminal review." ||
		fallbackCalled {
		t.Fatalf(
			"deadline_won=%v fallback_called=%v result=%+v data=%+v",
			deadlineWon,
			fallbackCalled,
			result,
			data,
		)
	}
}

func TestAwaitPreCanceledBrowserFallbackDoesNotAcquireTarget(t *testing.T) {
	now := time.Now().UTC()
	store := newReadTestStore(t, now)
	client := testsupport.NewBrowser("user-page")
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := AwaitConversation(ctx, ReadConfig{
		Store: store,
		BrowserConfig: &BrowserConfig{
			Client:  client,
			Engine:  engine,
			Journal: journal,
		},
	}, "conversation-1", time.Minute)

	counts, _, _, _, _, _, _ := client.Snapshot()
	_, journalErr := journal.Load(
		context.Background(),
		result.Evidence.RunID,
	)
	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_await_canceled" ||
		counts["Target.createTarget"] != 0 ||
		!errors.Is(journalErr, browserflow.ErrRunNotFound) {
		t.Fatalf(
			"create_target=%d journal_err=%v result=%+v",
			counts["Target.createTarget"],
			journalErr,
			result,
		)
	}
}

func TestAwaitFirstObservedStopWinsAcrossCleanup(t *testing.T) {
	tests := []struct {
		name                      string
		deadlineAtTerminalPersist bool
		wantOK                    bool
		wantError                 string
	}{
		{
			name:      "cancellation during close",
			wantError: "chatgpt_await_canceled",
		},
		{
			name:                      "persistence deadline precedes close cancellation",
			deadlineAtTerminalPersist: true,
			wantOK:                    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start := time.Now().UTC()
			now := start
			store := newReadTestStore(t, start)
			ctx, cancel := context.WithCancel(context.Background())
			baseClient := newAuthenticatedReadBrowser(func(
				expression string,
				_ *testsupport.Browser,
			) (any, error) {
				if strings.Contains(expression, "signed_in:") {
					return map[string]any{
						"signed_in": true, "signed_out": false,
					}, nil
				}
				if strings.Contains(expression, "const response = await fetch") {
					return map[string]any{
						"ok":          true,
						"status_code": http.StatusOK,
						"body":        terminalDetailPayload,
					}, nil
				}
				return map[string]any{}, nil
			})
			client := &cancelOnCloseBrowser{
				authenticatedReadBrowser: baseClient,
				cancel:                   cancel,
			}
			fileJournal, err := browserflow.NewFileJournal(t.TempDir())
			if err != nil {
				t.Fatalf("NewFileJournal: %v", err)
			}
			journal := &recordingJournal{
				Journal: fileJournal,
				beforeSave: func(
					_ context.Context,
					record browserflow.Record,
				) error {
					if test.deadlineAtTerminalPersist &&
						record.Phase == browserflow.PhaseTerminal {
						now = start.Add(time.Minute)
					}
					return nil
				},
			}
			engine := newReadTestEngine(t, client, journal)
			result := AwaitConversation(ctx, ReadConfig{
				Store:      store,
				HTTPClient: fixedHTTPClient(http.StatusForbidden, ""),
				BrowserConfig: &BrowserConfig{
					Client: client, Engine: engine, Journal: journal,
				},
				Now: func() time.Time { return now },
			}, "conversation-1", time.Minute)

			errorCode := ""
			if result.Error != nil {
				errorCode = result.Error.Code
			}
			data, _ := result.Data.(ConversationDetailData)
			if result.OK != test.wantOK ||
				errorCode != test.wantError ||
				data.StatusCode != http.StatusOK ||
				data.Text != "Terminal review." ||
				result.Cleanup.State != webagent.CleanupClosed {
				t.Fatalf("result=%+v data=%+v", result, data)
			}
		})
	}
}

func TestAwaitCancellationAfterTerminalFetchPersistsTerminal(t *testing.T) {
	now := time.Now().UTC()
	store := newReadTestStore(t, now)
	ctx, cancel := context.WithCancel(context.Background())
	client := newAuthenticatedReadBrowser(func(
		expression string,
		_ *testsupport.Browser,
	) (any, error) {
		if strings.Contains(expression, "signed_in:") {
			return map[string]any{"signed_in": true, "signed_out": false}, nil
		}
		if strings.Contains(expression, "const response = await fetch") {
			cancel()
			return map[string]any{
				"ok":          true,
				"status_code": http.StatusOK,
				"body":        terminalDetailPayload,
			}, nil
		}
		return map[string]any{}, nil
	})
	fileJournal, err := browserflow.NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	journal := &recordingJournal{Journal: fileJournal}
	engine := newReadTestEngine(t, client, journal)
	result := AwaitConversation(ctx, ReadConfig{
		Store:      store,
		HTTPClient: fixedHTTPClient(http.StatusForbidden, ""),
		BrowserConfig: &BrowserConfig{
			Client: client, Engine: engine, Journal: journal,
		},
		Now: func() time.Time { return now },
	}, "conversation-1", time.Minute)

	terminal, incomplete := false, false
	for _, phase := range journal.phases {
		terminal = terminal || phase == browserflow.PhaseTerminal
		incomplete = incomplete || phase == browserflow.PhaseIncomplete
	}
	data, _ := result.Data.(ConversationDetailData)
	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_await_canceled" ||
		data.Text != "Terminal review." ||
		!terminal ||
		incomplete ||
		result.Cleanup.State != webagent.CleanupClosed {
		t.Fatalf(
			"phases=%v result=%+v data=%+v",
			journal.phases,
			result,
			data,
		)
	}
}

func TestAwaitTerminalPersistenceFailureSurvivesCancellation(t *testing.T) {
	now := time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())
	client := newAuthenticatedReadBrowser(func(
		expression string,
		_ *testsupport.Browser,
	) (any, error) {
		if strings.Contains(expression, "signed_in:") {
			return map[string]any{"signed_in": true, "signed_out": false}, nil
		}
		if strings.Contains(expression, "const response = await fetch") {
			return map[string]any{
				"ok":          true,
				"status_code": http.StatusOK,
				"body":        terminalDetailPayload,
			}, nil
		}
		return map[string]any{}, nil
	})
	fileJournal, err := browserflow.NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	journal := &recordingJournal{
		Journal: fileJournal,
		beforeSave: func(
			_ context.Context,
			record browserflow.Record,
		) error {
			if record.Phase == browserflow.PhaseTerminal {
				cancel()
				return errors.New("synthetic terminal persistence failure")
			}
			return nil
		},
	}
	engine := newReadTestEngine(t, client, journal)
	result := AwaitConversation(ctx, ReadConfig{
		Store:      newReadTestStore(t, now),
		HTTPClient: fixedHTTPClient(http.StatusForbidden, ""),
		BrowserConfig: &BrowserConfig{
			Client: client, Engine: engine, Journal: journal,
		},
		Now: func() time.Time { return now },
	}, "conversation-1", time.Minute)

	data, _ := result.Data.(ConversationDetailData)
	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_read_internal" ||
		data.Text != "Terminal review." ||
		result.Cleanup.State != webagent.CleanupClosed {
		t.Fatalf("phases=%v result=%+v data=%+v", journal.phases, result, data)
	}
}

func TestAwaitDeadlineBoundariesBeforeBrowserFetch(t *testing.T) {
	tests := []struct {
		name                string
		wait                time.Duration
		advanceAtSignedIn   bool
		failPreparedPersist bool
		wantPreparedAttempt bool
	}{
		{
			name:              "deadline after signed-in observation",
			wait:              time.Second,
			advanceAtSignedIn: true,
		},
		{
			name:                "deadline during prepared persistence",
			wait:                10 * time.Second,
			failPreparedPersist: true,
			wantPreparedAttempt: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start := time.Now().UTC()
			advanceClock := false
			client := newAuthenticatedReadBrowser(func(
				expression string,
				_ *testsupport.Browser,
			) (any, error) {
				if strings.Contains(expression, "signed_in:") {
					advanceClock = test.advanceAtSignedIn
					return map[string]any{
						"signed_in": true, "signed_out": false,
					}, nil
				}
				return map[string]any{}, nil
			})
			fileJournal, err := browserflow.NewFileJournal(t.TempDir())
			if err != nil {
				t.Fatalf("NewFileJournal: %v", err)
			}
			journal := &recordingJournal{
				Journal: fileJournal,
				beforeSave: func(
					_ context.Context,
					record browserflow.Record,
				) error {
					if test.failPreparedPersist &&
						record.Phase == browserflow.PhasePrepared {
						advanceClock = true
						return context.DeadlineExceeded
					}
					return nil
				},
			}
			engine := newReadTestEngine(t, client, journal)
			result := AwaitConversation(
				context.Background(),
				ReadConfig{
					Store:      newReadTestStore(t, start),
					HTTPClient: fixedHTTPClient(http.StatusForbidden, ""),
					BrowserConfig: &BrowserConfig{
						Client: client, Engine: engine, Journal: journal,
					},
					Now: func() time.Time {
						if advanceClock {
							return start.Add(test.wait)
						}
						return start
					},
				},
				"conversation-1",
				test.wait,
			)

			preparedAttempt := false
			for _, phase := range journal.phases {
				preparedAttempt = preparedAttempt ||
					phase == browserflow.PhasePrepared
			}
			record, journalErr := fileJournal.Load(
				context.Background(),
				result.Evidence.RunID,
			)
			data, _ := result.Data.(ConversationDetailData)
			if result.OK ||
				result.Error == nil ||
				result.Error.Code != "chatgpt_browser_context_required" ||
				data.StatusCode != http.StatusForbidden ||
				result.Cleanup.State != webagent.CleanupClosed ||
				result.Evidence.Target == nil ||
				journalErr != nil ||
				record.TargetID != result.Evidence.Target.TargetID ||
				preparedAttempt != test.wantPreparedAttempt {
				t.Fatalf(
					"phases=%v journal_err=%v record=%+v result=%+v",
					journal.phases,
					journalErr,
					record,
					result,
				)
			}
		})
	}
}

func TestAwaitBrowserReplacesOnlyCompletedObservations(t *testing.T) {
	stalePayload := strings.Replace(
		incompleteDetailPayload,
		`"author":{"role":"assistant"},`,
		`"author":{"role":"assistant"},"metadata":{"model_slug":"stale-model"},`,
		1,
	)
	tests := []struct {
		name       string
		responses  []map[string]any
		wantOK     bool
		wantState  webagent.State
		wantStatus int
		wantError  string
		wantText   string
		wantStale  bool
	}{
		{
			name: "status-zero interruption preserves prior response",
			responses: []map[string]any{
				{"ok": true, "status_code": http.StatusOK, "body": stalePayload},
				{"ok": false, "status_code": 0, "error": "fetch_failed"},
			},
			wantOK:     true,
			wantState:  webagent.StateIncomplete,
			wantStatus: http.StatusOK,
			wantText:   "Still working.",
			wantStale:  true,
		},
		{
			name: "oversized 429 is fresh and remains retryable",
			responses: []map[string]any{
				{"ok": true, "status_code": http.StatusOK, "body": stalePayload},
				{
					"ok":          false,
					"status_code": http.StatusTooManyRequests,
					"body_bytes":  maxChatGPTResponseBytes + 1,
					"retry_after": "0",
					"error":       "response_too_large",
				},
				{"ok": true, "status_code": http.StatusOK, "body": terminalDetailPayload},
			},
			wantOK:     true,
			wantState:  webagent.StateTerminal,
			wantStatus: http.StatusOK,
			wantText:   "Terminal review.",
		},
		{
			name: "status-zero browser preserves completed direct failure",
			responses: []map[string]any{
				{"ok": false, "status_code": 0, "error": "fetch_failed"},
			},
			wantState:  webagent.StateFailed,
			wantStatus: http.StatusForbidden,
			wantError:  "chatgpt_browser_context_required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC()
			store := newReadTestStore(t, now)
			fetches := 0
			client := newAuthenticatedReadBrowser(func(
				expression string,
				_ *testsupport.Browser,
			) (any, error) {
				if strings.Contains(expression, "signed_in:") {
					return map[string]any{
						"signed_in":  true,
						"signed_out": false,
					}, nil
				}
				if strings.Contains(expression, "const response = await fetch") {
					response := test.responses[fetches]
					fetches++
					return response, nil
				}
				return map[string]any{}, nil
			})
			engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
			if err != nil {
				t.Fatalf("NewRuntime: %v", err)
			}
			result := AwaitConversation(context.Background(), ReadConfig{
				Store:      store,
				HTTPClient: fixedHTTPClient(http.StatusForbidden, ""),
				BrowserConfig: &BrowserConfig{
					Client:  client,
					Engine:  engine,
					Journal: journal,
				},
				Now:         func() time.Time { return now },
				AwaitDelays: []time.Duration{time.Millisecond},
				Wait: func(context.Context, time.Duration) error {
					now = now.Add(time.Millisecond)
					return nil
				},
			}, "conversation-1", 10*time.Second)

			data, _ := result.Data.(ConversationDetailData)
			_, stale := data.Metadata["model_slug"]
			errorCode := ""
			if result.Error != nil {
				errorCode = result.Error.Code
			}
			if result.OK != test.wantOK ||
				result.State != test.wantState ||
				errorCode != test.wantError ||
				data.StatusCode != test.wantStatus ||
				data.Text != test.wantText ||
				stale != test.wantStale ||
				fetches != len(test.responses) ||
				result.Evidence.ReadMode != data.ReadMode ||
				result.Cleanup.State != webagent.CleanupClosed {
				t.Fatalf(
					"fetches=%d result=%+v data=%+v stale=%v",
					fetches,
					result,
					data,
					stale,
				)
			}
		})
	}
}

func TestAwaitDeadlineBoundsInflightDirectFetch(t *testing.T) {
	now := time.Now().UTC()
	store := newReadTestStore(t, now)
	transportCanceled := false
	client := &http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			transportCanceled = errors.Is(
				request.Context().Err(),
				context.DeadlineExceeded,
			)
			return nil, request.Context().Err()
		},
	)}

	started := time.Now()
	result := AwaitConversation(context.Background(), ReadConfig{
		Store:      store,
		HTTPClient: client,
	}, "conversation-1", 3*time.Second)
	elapsed := time.Since(started)
	if elapsed > 10*time.Second {
		t.Fatalf("await elapsed = %s, want bounded inflight fetch", elapsed)
	}
	if !transportCanceled ||
		!result.OK ||
		result.State != webagent.StateIncomplete {
		t.Fatalf("AwaitConversation result = %+v", result)
	}
}

func TestConversationAwaitCommandCarriesLongWaitAndOuterGrace(t *testing.T) {
	command := conversationAwaitCommand(" conversation-1 ")
	for _, expected := range []string{
		"conversations await conversation-1",
		"--wait 40m",
		"--timeout 40m30s",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("command %q does not contain %q", command, expected)
		}
	}
}

func TestAwaitThreadsOneDeadlineIntoDirectFallbackInitialization(t *testing.T) {
	t.Run("status-zero boundary does not initialize browser", func(t *testing.T) {
		start := time.Now().UTC()
		store := newReadTestStore(t, start)
		ctx, cancel := context.WithCancel(context.Background())
		fetched := false
		postFetchClockReads := 0
		fallbackCalled := false
		result := AwaitConversation(ctx, ReadConfig{
			Store: store,
			HTTPClient: &http.Client{Transport: roundTripFunc(
				func(*http.Request) (*http.Response, error) {
					fetched = true
					return nil, errors.New("test direct transport failure")
				},
			)},
			Now: func() time.Time {
				if !fetched {
					return start
				}
				postFetchClockReads++
				cancel()
				return start.Add(300 * time.Millisecond)
			},
			BrowserFallback: func(context.Context) (*BrowserConfig, error) {
				fallbackCalled = true
				return nil, errors.New("browser must remain lazy")
			},
		}, "conversation-1", 300*time.Millisecond)

		data, _ := result.Data.(ConversationDetailData)
		if !result.OK ||
			result.State != webagent.StateIncomplete ||
			data.StatusCode != 0 ||
			data.SchemaVersion != ConversationDetailSchemaVersion ||
			postFetchClockReads != 1 ||
			fallbackCalled {
			t.Fatalf(
				"clock_reads=%d fallback_called=%v result=%+v data=%+v",
				postFetchClockReads,
				fallbackCalled,
				result,
				data,
			)
		}
	})

	t.Run("deadline-bound wait wins over simultaneous cancellation", func(t *testing.T) {
		start := time.Now().UTC()
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		result := AwaitConversation(ctx, ReadConfig{
			Store: newReadTestStore(t, start),
			HTTPClient: &http.Client{Transport: roundTripFunc(
				func(*http.Request) (*http.Response, error) {
					attempts++
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body: io.NopCloser(
							strings.NewReader(incompleteDetailPayload),
						),
					}, nil
				},
			)},
			Now:         func() time.Time { return start },
			AwaitDelays: []time.Duration{300 * time.Millisecond},
			Wait: func(context.Context, time.Duration) error {
				cancel()
				return nil
			},
		}, "conversation-1", 300*time.Millisecond)

		data, _ := result.Data.(ConversationDetailData)
		if !result.OK ||
			result.State != webagent.StateIncomplete ||
			data.StatusCode != http.StatusOK ||
			attempts != 1 {
			t.Fatalf(
				"attempts=%d result=%+v data=%+v",
				attempts,
				result,
				data,
			)
		}
	})

	t.Run("completed HTTP failure survives fallback deadline", func(t *testing.T) {
		start := time.Now().UTC()
		now := start
		store := newReadTestStore(t, start)
		fallbackCalled := false
		result := AwaitConversation(context.Background(), ReadConfig{
			Store:      store,
			HTTPClient: fixedHTTPClient(http.StatusServiceUnavailable, ""),
			Now:        func() time.Time { return now },
			BrowserFallback: func(context.Context) (*BrowserConfig, error) {
				fallbackCalled = true
				now = start.Add(300 * time.Millisecond)
				return nil, errors.New("fallback deadline elapsed")
			},
		}, "conversation-1", 300*time.Millisecond)

		data, _ := result.Data.(ConversationDetailData)
		if result.OK ||
			result.Error == nil ||
			result.Error.Code != "chatgpt_http_failed" ||
			data.StatusCode != http.StatusServiceUnavailable ||
			!fallbackCalled {
			t.Fatalf(
				"fallback_called=%v result=%+v data=%+v",
				fallbackCalled,
				result,
				data,
			)
		}
	})

	t.Run("parent cancellation during fallback preserves direct data", func(t *testing.T) {
		now := time.Now().UTC()
		store := newReadTestStore(t, now)
		ctx, cancel := context.WithCancel(context.Background())
		result := AwaitConversation(ctx, ReadConfig{
			Store:      store,
			HTTPClient: fixedHTTPClient(http.StatusServiceUnavailable, ""),
			BrowserFallback: func(context.Context) (*BrowserConfig, error) {
				cancel()
				return nil, context.Canceled
			},
		}, "conversation-1", time.Minute)

		data, _ := result.Data.(ConversationDetailData)
		if result.OK ||
			result.Error == nil ||
			result.Error.Code != "chatgpt_await_canceled" ||
			data.StatusCode != http.StatusServiceUnavailable ||
			result.Evidence.Target != nil {
			t.Fatalf("result=%+v data=%+v", result, data)
		}
	})
}

func TestExtractConversationTextKeepsProgressAncestorIncomplete(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"current_node": "tool",
		"mapping": map[string]any{
			"tool": map[string]any{
				"parent": "progress",
				"message": map[string]any{
					"author": map[string]any{"role": "tool"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"tool work"},
					},
				},
			},
			"progress": map[string]any{
				"parent": "prompt",
				"message": map[string]any{
					"author":   map[string]any{"role": "assistant"},
					"status":   "finished_successfully",
					"end_turn": true,
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"I am still working."},
					},
				},
			},
			"prompt": map[string]any{
				"parent": "",
				"message": map[string]any{
					"author": map[string]any{"role": "user"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Review this diff."},
					},
				},
			},
		},
	}

	got := extractConversationText(payload)
	if got.completionState != "incomplete" {
		t.Fatalf("completionState = %q, want incomplete", got.completionState)
	}
	if got.text != "I am still working." {
		t.Fatalf("text = %q", got.text)
	}
	if current, _ := got.metadata["assistant_is_current_node"].(bool); current {
		t.Fatal("progress ancestor was marked as the current assistant node")
	}
}

func TestExtractConversationTextKeepsFinishedProgressIncompleteWhileAsyncActive(
	t *testing.T,
) {
	t.Parallel()

	payload := map[string]any{
		"async_status": float64(3),
		"current_node": "progress",
		"mapping": map[string]any{
			"progress": map[string]any{
				"parent": "prompt",
				"message": map[string]any{
					"author":    map[string]any{"role": "assistant"},
					"status":    "finished_successfully",
					"end_turn":  true,
					"recipient": "all",
					"content": map[string]any{
						"content_type": "text",
						"parts": []any{
							"Three possible blockers remain under review.",
						},
					},
				},
			},
			"prompt": map[string]any{
				"parent": "",
				"message": map[string]any{
					"author": map[string]any{"role": "user"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Review this diff."},
					},
				},
			},
		},
	}

	got := extractConversationText(payload)
	if got.completionState != "incomplete" {
		t.Fatalf("completionState = %q, want incomplete", got.completionState)
	}
	if got.text != "Three possible blockers remain under review." {
		t.Fatalf("text = %q", got.text)
	}
	if active, _ := got.metadata["provider_async_active"].(bool); !active {
		t.Fatalf("metadata = %+v, want provider_async_active", got.metadata)
	}
	if got.metadata["provider_async_status"] != "3" {
		t.Fatalf("metadata = %+v, want provider_async_status=3", got.metadata)
	}
}

func TestConversationActivityClassifiesProviderEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		payload    map[string]any
		want       conversationActivityState
		wantAsync  string
		wantStream string
	}{
		{
			name:    "absent legacy evidence",
			payload: map[string]any{},
			want:    conversationActivityAbsent,
		},
		{
			name:      "string streaming",
			payload:   map[string]any{"async_status": "IS_STREAMING"},
			want:      conversationActivityActive,
			wantAsync: "IS_STREAMING",
		},
		{
			name:       "verified complete stream",
			payload:    map[string]any{"stream_status": "COMPLETE"},
			want:       conversationActivityInactive,
			wantStream: "COMPLETE",
		},
		{
			name:      "observed numeric complete",
			payload:   map[string]any{"async_status": float64(4)},
			want:      conversationActivityInactive,
			wantAsync: "4",
		},
		{
			name:      "unknown scalar fails closed",
			payload:   map[string]any{"async_status": "QUEUED_V2"},
			want:      conversationActivityUnknown,
			wantAsync: "QUEUED_V2",
		},
		{
			name:      "unknown object is bounded",
			payload:   map[string]any{"async_status": map[string]any{"state": "future"}},
			want:      conversationActivityUnknown,
			wantAsync: "non_scalar",
		},
		{
			name: "active wins conflicting inactive evidence",
			payload: map[string]any{
				"async_status":  "IS_STREAMING",
				"stream_status": "COMPLETE",
			},
			want:       conversationActivityActive,
			wantAsync:  "IS_STREAMING",
			wantStream: "COMPLETE",
		},
		{
			name: "unknown blocks conflicting inactive evidence",
			payload: map[string]any{
				"async_status":  "FUTURE_STATE",
				"stream_status": "COMPLETE",
			},
			want:       conversationActivityUnknown,
			wantAsync:  "FUTURE_STATE",
			wantStream: "COMPLETE",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, asyncStatus, streamStatus := conversationActivity(test.payload)
			if got != test.want ||
				asyncStatus != test.wantAsync ||
				streamStatus != test.wantStream {
				t.Fatalf(
					"activity=(%q,%q,%q), want (%q,%q,%q)",
					got,
					asyncStatus,
					streamStatus,
					test.want,
					test.wantAsync,
					test.wantStream,
				)
			}
		})
	}
}

func TestExtractConversationTextFailsClosedForUnknownAsyncStatus(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"async_status": "FUTURE_PROVIDER_STATE",
		"current_node": "answer",
		"mapping": map[string]any{
			"answer": map[string]any{
				"parent": "prompt",
				"message": map[string]any{
					"author":   map[string]any{"role": "assistant"},
					"status":   "finished_successfully",
					"end_turn": true,
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Possibly final answer."},
					},
				},
			},
			"prompt": map[string]any{
				"parent": "",
				"message": map[string]any{
					"author": map[string]any{"role": "user"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Question"},
					},
				},
			},
		},
	}

	got := extractConversationText(payload)
	if got.completionState != "incomplete" ||
		got.metadata["provider_activity_state"] != conversationActivityUnknown {
		t.Fatalf("extracted = %#v", got)
	}
}

func TestExtractConversationTextAcceptsCurrentTerminalAssistant(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"current_node": "answer",
		"mapping": map[string]any{
			"answer": map[string]any{
				"parent": "prompt",
				"message": map[string]any{
					"author":   map[string]any{"role": "assistant"},
					"status":   "finished_successfully",
					"end_turn": true,
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Final answer."},
					},
				},
			},
			"prompt": map[string]any{
				"parent": "",
				"message": map[string]any{
					"author": map[string]any{"role": "user"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Question"},
					},
				},
			},
		},
	}

	got := extractConversationText(payload)
	if got.completionState != "terminal" || got.text != "Final answer." {
		t.Fatalf("extracted = %#v", got)
	}
}

func TestExtractConversationTextAcceptsObservedNumericComplete(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"async_status": float64(4),
		"current_node": "answer",
		"mapping": map[string]any{
			"answer": map[string]any{
				"parent": "prompt",
				"message": map[string]any{
					"author":    map[string]any{"role": "assistant"},
					"status":    "finished_successfully",
					"end_turn":  true,
					"recipient": "all",
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Final observed answer."},
					},
				},
			},
			"prompt": map[string]any{
				"parent": "",
				"message": map[string]any{
					"author": map[string]any{"role": "user"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Question"},
					},
				},
			},
		},
	}

	got := extractConversationText(payload)
	if got.completionState != "terminal" ||
		got.text != "Final observed answer." ||
		got.metadata["provider_async_status"] != "4" {
		t.Fatalf("extracted = %#v", got)
	}
}

func TestExtractConversationTextKeepsNumericCompleteWithoutEndTurnIncomplete(
	t *testing.T,
) {
	t.Parallel()

	payload := map[string]any{
		"async_status": float64(4),
		"current_node": "answer",
		"mapping": map[string]any{
			"answer": map[string]any{
				"parent": "prompt",
				"message": map[string]any{
					"author": map[string]any{"role": "assistant"},
					"status": "finished_successfully",
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Answer without terminal proof."},
					},
				},
			},
			"prompt": map[string]any{
				"parent": "",
				"message": map[string]any{
					"author": map[string]any{"role": "user"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Question"},
					},
				},
			},
		},
	}

	got := extractConversationText(payload)
	if got.completionState != "incomplete" ||
		got.text != "Answer without terminal proof." {
		t.Fatalf("extracted = %#v", got)
	}
}
