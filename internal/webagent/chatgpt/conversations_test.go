package chatgpt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
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
		{name: "admission", code: "chatgpt_admission_blocked", errClass: "admission", want: false},
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

func TestRecordDirectReadFallbackPreservesTypedFailureEvidence(t *testing.T) {
	t.Parallel()

	direct := webagent.Result{
		Error: &webagent.OperationError{
			Code:     "chatgpt_browser_context_required",
			ErrClass: "auth",
		},
	}
	result := webagent.Result{
		Data: ConversationDetailData{
			Metadata: map[string]any{"source": "rendered_browser"},
		},
	}

	got := recordDirectReadFallback(result, direct)
	data, ok := got.Data.(ConversationDetailData)
	if !ok {
		t.Fatalf("result data type = %T, want ConversationDetailData", got.Data)
	}
	if attempted, _ := data.Metadata["direct_http_attempted"].(bool); !attempted {
		t.Fatal("direct_http_attempted was not recorded")
	}
	if code, _ := data.Metadata["direct_http_failure_code"].(string); code != direct.Error.Code {
		t.Fatalf("direct_http_failure_code = %q, want %q", code, direct.Error.Code)
	}
	if source, _ := data.Metadata["source"].(string); source != "rendered_browser" {
		t.Fatalf("existing metadata source = %q, want rendered_browser", source)
	}
}

func TestNextAwaitDelayRepeatsLastBackoffUntilDeadline(t *testing.T) {
	t.Parallel()

	delays := []time.Duration{time.Second, 2 * time.Second}
	got, ok := nextAwaitDelay(delays, 9, 10*time.Second)
	if !ok || got != 2*time.Second {
		t.Fatalf("nextAwaitDelay() = %v, %v; want 2s, true", got, ok)
	}
	got, ok = nextAwaitDelay(delays, 10, time.Second)
	if !ok || got != time.Second {
		t.Fatalf("bounded nextAwaitDelay() = %v, %v; want 1s, true", got, ok)
	}
}

func TestListConversationsDirectSuccessDoesNotInitializeBrowserFallback(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
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
	gate, err := admission.New(admission.Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("admission.New: %v", err)
	}
	browserFallbackCalled := false
	client := &http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(
					strings.NewReader(`{"items":[]}`),
				),
			}, nil
		},
	)}

	result := ListConversations(context.Background(), ReadConfig{
		Store:      store,
		Admission:  gate,
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

func TestAwaitDirectIncompleteHonorsDeadlineWithoutBrowserFallback(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
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
	gate, err := admission.New(admission.Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("admission.New: %v", err)
	}
	payload := `{
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
	client := &http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(payload)),
			}, nil
		},
	)}
	browserFallbackCalled := false
	result := AwaitConversation(context.Background(), ReadConfig{
		Store:       store,
		Admission:   gate,
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

func TestAwaitDeadlineBoundsInflightDirectFetch(t *testing.T) {
	now := time.Now().UTC()
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
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
	gate, err := admission.New(admission.Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("admission.New: %v", err)
	}
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
		Admission:  gate,
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

func TestAwaitDeadlineBoundsDirectAdmissionPreparation(t *testing.T) {
	now := time.Now().UTC()
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
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
	gate, err := admission.New(admission.Config{
		StateDir:       stateDir,
		MinimumSpacing: time.Second,
	})
	if err != nil {
		t.Fatalf("admission.New: %v", err)
	}
	prior, err := gate.Acquire(context.Background(), admission.Request{
		Provider:  chatGPTReadAdmissionProvider,
		Operation: string(webagent.OperationConversationsDetail),
		RunID:     "prior-read",
	})
	if err != nil {
		t.Fatalf("prior read admission: %v", err)
	}
	if err := prior.Release(admission.Release{
		Outcome: admission.OutcomeCompleted,
	}); err != nil {
		t.Fatalf("release prior read admission: %v", err)
	}
	httpCalls := 0
	client := &http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			httpCalls++
			return nil, errors.New("HTTP must remain unreachable")
		},
	)}

	result := AwaitConversation(context.Background(), ReadConfig{
		Store:      store,
		Admission:  gate,
		HTTPClient: client,
	}, "conversation-1", 50*time.Millisecond)
	if result.OK || httpCalls != 0 {
		t.Fatalf(
			"result=%+v http_calls=%d",
			result,
			httpCalls,
		)
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

func TestPersistBrowserReadStateUsesIndependentBoundedContext(t *testing.T) {
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if expired.Err() == nil {
		t.Fatal("test operation context did not expire")
	}

	tests := []struct {
		name     string
		terminal bool
		want     browserflow.Phase
	}{
		{name: "incomplete", want: browserflow.PhaseIncomplete},
		{name: "terminal", terminal: true, want: browserflow.PhaseTerminal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			client := testsupport.NewBrowser("user-page")
			engine, journal, _, err := testsupport.NewRuntime(stateDir, client)
			if err != nil {
				t.Fatalf("NewRuntime: %v", err)
			}
			runID := "browser-read-persistence-" + test.name
			lease, err := engine.Acquire(
				context.Background(),
				browserflow.AcquireRequest{
					RunID:      runID,
					Provider:   "chatgpt",
					Operation:  "conversations.await",
					InitialURL: "about:blank",
				},
			)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			defer func() {
				if _, closeErr := lease.Close(context.Background()); closeErr != nil {
					t.Fatalf("Close: %v", closeErr)
				}
			}()
			if err := lease.MarkPrepared(context.Background()); err != nil {
				t.Fatalf("MarkPrepared: %v", err)
			}
			if err := persistBrowserReadState(lease, test.terminal); err != nil {
				t.Fatalf("persistBrowserReadState: %v", err)
			}
			record, err := journal.Load(context.Background(), runID)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if record.Phase != test.want {
				t.Fatalf("record phase = %q, want %q", record.Phase, test.want)
			}
		})
	}
}

func TestAwaitThreadsOneDeadlineIntoDirectFallbackInitialization(t *testing.T) {
	now := time.Now().UTC()
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
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
	gate, err := admission.New(admission.Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("admission.New: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			now = now.Add(29900 * time.Millisecond)
			return nil, errors.New("test direct transport failure")
		},
	)}
	fallbackCalled := false
	fallbackHadDeadline := false
	var fallbackRemaining time.Duration

	result := AwaitConversation(context.Background(), ReadConfig{
		Store:      store,
		Admission:  gate,
		HTTPClient: client,
		Now:        func() time.Time { return now },
		BrowserFallback: func(fallbackCtx context.Context) (*BrowserConfig, error) {
			fallbackCalled = true
			deadline, ok := fallbackCtx.Deadline()
			fallbackHadDeadline = ok
			if ok {
				fallbackRemaining = time.Until(deadline)
			}
			return nil, errors.New("test headed fallback unavailable")
		},
	}, "conversation-1", 30*time.Second)

	if result.OK ||
		!fallbackCalled ||
		!fallbackHadDeadline ||
		fallbackRemaining <= 0 ||
		fallbackRemaining >= 500*time.Millisecond {
		t.Fatalf(
			"result=%+v fallback_called=%v deadline=%v remaining=%s",
			result,
			fallbackCalled,
			fallbackHadDeadline,
			fallbackRemaining,
		)
	}
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

func TestBrowserReadUsesReadAdmissionLane(t *testing.T) {
	t.Parallel()

	if got := browserAdmissionProvider(BrowserConfig{}); got != "chatgpt" {
		t.Fatalf("default browser admission provider = %q", got)
	}
	if got := browserAdmissionProvider(
		readBrowserConfig(BrowserConfig{}),
	); got != chatGPTReadAdmissionProvider {
		t.Fatalf("read browser admission provider = %q", got)
	}
}

func TestSharedThrottleCooldownBlocksBothReadAndSendCallers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	gate, err := admission.New(admission.Config{
		StateDir: t.TempDir(),
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("admission.New: %v", err)
	}
	readLease, failure := acquireChatGPTThrottle(
		context.Background(),
		gate,
	)
	if failure != nil {
		t.Fatalf("read throttle acquire: %+v", failure)
	}
	retryAt := now.Add(time.Hour)
	if err := releaseChatGPTThrottle(readLease, &readFailure{
		code:     "chatgpt_rate_limited",
		errClass: "rate_limit",
		retryAt:  retryAt,
	}); err != nil {
		t.Fatalf("read throttle release: %v", err)
	}

	_, sendFailure := acquireChatGPTThrottle(context.Background(), gate)
	if sendFailure == nil ||
		sendFailure.errClass != "rate_limit" ||
		!sendFailure.retryAt.Equal(retryAt) {
		t.Fatalf("send throttle failure = %+v", sendFailure)
	}
}
