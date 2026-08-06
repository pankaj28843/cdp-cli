package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

const terminalNoAnswerCandidateDetailPayload = `{
  "current_node":"answer",
  "mapping":{
    "answer":{
      "parent":"prompt",
      "message":{
        "author":{"role":"assistant"},
        "status":"finished_successfully",
        "end_turn":true,
        "content":{"content_type":"text","parts":[""]}
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

const imageOnlyDetailPayload = `{
  "conversation_id":"conversation-1",
  "async_status":4,
  "current_node":"answer",
  "mapping":{
    "answer":{
      "parent":"prompt",
      "message":{
        "author":{"role":"assistant"},
        "status":"finished_successfully",
        "end_turn":true,
        "content":{
          "content_type":"multimodal_text",
          "parts":[{
            "content_type":"image_asset_pointer",
            "asset_pointer":"sediment://file_synthetic_generated_image",
            "size_bytes":2457600,
            "width":1536,
            "height":1024,
            "alt_text":"A synthetic high-resolution generated image"
          }]
        }
      }
    },
    "prompt":{
      "parent":"",
      "message":{
        "author":{"role":"user"},
        "content":{"content_type":"text","parts":["Generate an image."]}
      }
    }
  }
}`

const terminalToolImageDetailPayload = `{
  "async_status":null,
  "current_node":"recap",
  "mapping":{
    "recap":{
      "parent":"tool-image",
      "message":{
        "author":{"role":"assistant"},
        "content":{"content_type":"reasoning_recap","content":"Worked for 1m."}
      }
    },
    "tool-image":{
      "parent":"prompt",
      "message":{
        "author":{"role":"tool"},
        "recipient":"all",
        "status":"finished_successfully",
        "content":{
          "content_type":"multimodal_text",
          "parts":[{
            "content_type":"image_asset_pointer",
            "asset_pointer":"sediment://file_synthetic_tool_image",
            "mime_type":"image/png",
            "size_bytes":2048,
            "width":320,
            "height":180
          }]
        }
      }
    },
    "prompt":{
      "parent":"",
      "message":{
        "author":{"role":"user"},
        "content":{"content_type":"text","parts":["Generate an image."]}
      }
    }
  }
}`

const activeNoAnswerDetailPayload = `{
  "async_status":3,
  "current_node":"answer",
  "mapping":{
    "answer":{
      "parent":"prompt",
      "message":{
        "author":{"role":"assistant"},
        "status":"finished_successfully",
        "end_turn":true,
        "content":{"content_type":"text","parts":[""]}
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

func TestDetailAndAwaitProveRenderedTerminalNoAnswer(t *testing.T) {
	tests := []struct {
		name  string
		await bool
	}{
		{name: "detail"},
		{name: "await", await: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
			store := newReadTestStore(t, now)
			client := newAuthenticatedReadBrowser(func(
				expression string,
				_ *testsupport.Browser,
			) (any, error) {
				switch {
				case strings.Contains(expression, "signed_in:"):
					return map[string]any{
						"signed_in": true, "signed_out": false,
					}, nil
				case strings.Contains(expression, "const response = await fetch"):
					return map[string]any{
						"ok":          true,
						"status_code": http.StatusOK,
						"body":        terminalNoAnswerCandidateDetailPayload,
					}, nil
				case strings.Contains(
					expression,
					"terminal_no_answer_reason",
				):
					return map[string]any{
						"route_matches":                   true,
						"conversation_id":                 "conversation-1",
						"text":                            "",
						"prompt_candidates":               []any{"Review."},
						"is_streaming":                    false,
						"terminal_control_present":        true,
						"assistant_count":                 1,
						"user_message_count":              1,
						"stopped_thinking_marker_present": true,
						"terminal_no_answer":              true,
						"terminal_no_answer_reason":       "stopped_thinking",
					}, nil
				default:
					return map[string]any{}, nil
				}
			})
			engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
			if err != nil {
				t.Fatalf("NewRuntime: %v", err)
			}
			config := ReadConfig{
				Store: store,
				HTTPClient: fixedHTTPClient(
					http.StatusOK,
					terminalNoAnswerCandidateDetailPayload,
				),
				BrowserConfig: &BrowserConfig{
					Client: client, Engine: engine, Journal: journal,
				},
				Now:         func() time.Time { return now },
				AwaitDelays: []time.Duration{time.Second},
				Wait: func(_ context.Context, delay time.Duration) error {
					now = now.Add(delay)
					return nil
				},
			}
			var result webagent.Result
			if test.await {
				result = AwaitConversation(
					context.Background(),
					config,
					"conversation-1",
					5*time.Second,
				)
			} else {
				result = DetailConversation(
					context.Background(),
					config,
					"conversation-1",
				)
			}

			data, ok := result.Data.(ConversationDetailData)
			if !ok ||
				!result.OK ||
				result.State != webagent.StateTerminal ||
				data.CompletionState != "terminal_no_answer" ||
				data.CompletionReason != "stopped_thinking" ||
				data.Text != "" ||
				data.ReadMode != "observed_stable_http_plus_headed_rendered" ||
				result.Evidence.Target == nil ||
				!result.Evidence.Target.Closed ||
				result.Cleanup.State != webagent.CleanupClosed {
				t.Fatalf("result=%+v data=%+v", result, data)
			}
		})
	}
}

func TestDetailImageOnlyAssistantIsTerminalWithoutRenderedFallback(
	t *testing.T,
) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	client := newAuthenticatedReadBrowser(func(
		expression string,
		_ *testsupport.Browser,
	) (any, error) {
		switch {
		case strings.Contains(expression, "signed_in:"):
			return map[string]any{
				"signed_in": true, "signed_out": false,
			}, nil
		case strings.Contains(expression, "const response = await fetch"):
			return map[string]any{
				"ok":          true,
				"status_code": http.StatusOK,
				"body":        imageOnlyDetailPayload,
			}, nil
		case strings.Contains(expression, "terminal_no_answer_reason"):
			return map[string]any{
				"route_matches":            true,
				"conversation_id":          "conversation-1",
				"text":                     "",
				"prompt_candidates":        []any{"Generate an image."},
				"is_streaming":             false,
				"terminal_control_present": true,
				"assistant_count":          1,
				"user_message_count":       1,
				"terminal_no_answer":       false,
			}, nil
		default:
			return map[string]any{}, nil
		}
	})
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	result := DetailConversation(context.Background(), ReadConfig{
		Store:      newReadTestStore(t, now),
		HTTPClient: fixedHTTPClient(http.StatusOK, imageOnlyDetailPayload),
		BrowserConfig: &BrowserConfig{
			Client: client, Engine: engine, Journal: journal,
		},
		Now: func() time.Time { return now },
	}, "conversation-1")

	data, ok := result.Data.(ConversationDetailData)
	if !ok {
		t.Fatalf("result data type = %T", result.Data)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal detail data: %v", err)
	}
	var contract struct {
		Attachments []struct {
			Kind      string `json:"kind"`
			Alt       string `json:"alt"`
			Source    string `json:"source"`
			SizeBytes int64  `json:"size_bytes"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(encoded, &contract); err != nil {
		t.Fatalf("unmarshal detail contract: %v", err)
	}
	counts, _, _, _, _, _, _ := client.Snapshot()
	terminalNoAnswer, _ := data.Metadata["terminal_no_answer_candidate"].(bool)
	if !result.OK ||
		result.State != webagent.StateTerminal ||
		data.CompletionState != conversationCompletionTerminal ||
		data.Text != "" ||
		terminalNoAnswer ||
		len(contract.Attachments) != 1 ||
		contract.Attachments[0].Kind != "image" ||
		contract.Attachments[0].Alt !=
			"A synthetic high-resolution generated image" ||
		contract.Attachments[0].Source !=
			"sediment://file_synthetic_generated_image" ||
		contract.Attachments[0].SizeBytes != 2457600 ||
		contract.Attachments[0].Width != 1536 ||
		contract.Attachments[0].Height != 1024 ||
		counts["Target.createTarget"] != 0 ||
		counts["Target.closeTarget"] != 0 ||
		result.Evidence.Target != nil ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf(
			"create=%d close=%d result=%+v data=%+v attachments=%+v",
			counts["Target.createTarget"],
			counts["Target.closeTarget"],
			result,
			data,
			contract.Attachments,
		)
	}
}

func TestConversationDetailAcceptsFinishedToolImageAfterReasoningRecap(
	t *testing.T,
) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(terminalToolImageDetailPayload), &payload); err != nil {
		t.Fatalf("decode synthetic tool image payload: %v", err)
	}
	data, failure := parseConversationDetailPayload(
		newConversationDetailData(
			"conversation-tool-image",
			"candidate_http",
			"",
		),
		payload,
		http.StatusOK,
	)
	if failure != nil ||
		data.CompletionState != conversationCompletionTerminal ||
		data.Text != "" ||
		len(data.Attachments) != 1 ||
		data.Attachments[0].Kind != "image" ||
		data.Attachments[0].Source != "sediment://file_synthetic_tool_image" ||
		data.Attachments[0].MIMEType != "image/png" ||
		data.Attachments[0].SizeBytes != 2048 ||
		data.Attachments[0].Width != 320 ||
		data.Attachments[0].Height != 180 {
		t.Fatalf("data=%+v failure=%+v", data, failure)
	}
	if role, _ := data.Metadata["output_role"].(string); role != "tool" {
		t.Fatalf("output_role=%v metadata=%+v", data.Metadata["output_role"], data.Metadata)
	}
}

func TestDetailImageOnlyAssistantBrowserFallbackClosesOnlyOwnedTarget(
	t *testing.T,
) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	client := newAuthenticatedReadBrowser(func(
		expression string,
		_ *testsupport.Browser,
	) (any, error) {
		switch {
		case strings.Contains(expression, "signed_in:"):
			return map[string]any{
				"signed_in":  true,
				"signed_out": false,
			}, nil
		case strings.Contains(expression, "const response = await fetch"):
			return map[string]any{
				"ok":          true,
				"status_code": http.StatusOK,
				"body":        imageOnlyDetailPayload,
			}, nil
		default:
			return map[string]any{}, nil
		}
	})
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	result := DetailConversation(context.Background(), ReadConfig{
		Store: newReadTestStore(t, now),
		HTTPClient: fixedHTTPClient(
			http.StatusServiceUnavailable,
			"",
		),
		BrowserConfig: &BrowserConfig{
			Client:  client,
			Engine:  engine,
			Journal: journal,
		},
		Now: func() time.Time { return now },
	}, "conversation-1")

	data, ok := result.Data.(ConversationDetailData)
	counts, _, _, _, _, _, targets := client.Snapshot()
	_, userTargetPreserved := targets["user-page"]
	if !ok ||
		!result.OK ||
		result.State != webagent.StateTerminal ||
		data.CompletionState != conversationCompletionTerminal ||
		len(data.Attachments) != 1 ||
		data.Attachments[0].Kind != "image" ||
		counts["Target.createTarget"] != 1 ||
		counts["Target.closeTarget"] != 1 ||
		len(targets) != 1 ||
		!userTargetPreserved ||
		result.Evidence.Target == nil ||
		!result.Evidence.Target.Owned ||
		!result.Evidence.Target.Closed ||
		result.Evidence.Target.TargetID == "user-page" ||
		result.Cleanup.State != webagent.CleanupClosed ||
		result.Cleanup.TargetID != result.Evidence.Target.TargetID {
		t.Fatalf(
			"create=%d close=%d targets=%v result=%+v data=%+v",
			counts["Target.createTarget"],
			counts["Target.closeTarget"],
			targets,
			result,
			data,
		)
	}
}

func TestConversationAttachmentsExposeSafeFileMetadata(t *testing.T) {
	message := map[string]any{
		"content": map[string]any{
			"content_type": "multimodal_text",
			"parts": []any{
				"[Download](sandbox:/mnt/data/synthetic-report.csv)",
			},
		},
		"metadata": map[string]any{
			"attachments": []any{
				map[string]any{
					"content_type":   "file_asset_pointer",
					"file_id":        "file_synthetic_report",
					"file_name":      "synthetic-report.csv",
					"mime_type":      "text/csv",
					"size_bytes":     "128",
					"download_url":   "https://chatgpt.com/backend-api/estuary/content?sig=private-test-value",
					"sandbox_path":   "/mnt/data/synthetic-report.csv",
					"irrelevant_key": "ignored",
				},
			},
		},
	}

	attachments, truncated := conversationAttachments(message)
	if truncated || len(attachments) != 1 {
		t.Fatalf("attachments=%+v truncated=%v", attachments, truncated)
	}
	metadata := attachments[0]
	if metadata.Kind != "file" ||
		metadata.FileID != "file_synthetic_report" ||
		metadata.FileName != "synthetic-report.csv" ||
		metadata.MIMEType != "text/csv" ||
		metadata.SizeBytes != 128 ||
		metadata.Source !=
			"https://chatgpt.com/backend-api/estuary/content" ||
		strings.Contains(metadata.Source, "private-test-value") ||
		strings.Contains(metadata.Source, "/mnt/data/") {
		t.Fatalf("metadata=%+v", metadata)
	}
}

func TestStableAttachmentSourceRejectsPrivateLocations(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "signed HTTPS query and fragment are removed",
			raw:  "https://chatgpt.com/backend-api/files/file-1/download?sig=private#private",
			want: "https://chatgpt.com/backend-api/files/file-1/download",
		},
		{
			name: "same-origin provider route is stable",
			raw:  "/backend-api/files/file-1/download",
			want: "/backend-api/files/file-1/download",
		},
		{
			name: "sandbox URI is opaque",
			raw:  "sandbox:/mnt/data/private-report.csv",
			want: "sandbox_artifact",
		},
		{
			name: "sandbox path is opaque",
			raw:  "/mnt/data/private-report.csv",
			want: "sandbox_artifact",
		},
		{name: "macOS home path", raw: "/Users/example/private.png"},
		{name: "macOS private path", raw: "/private/var/folders/private.png"},
		{name: "Linux home path", raw: "/" + "home/example/private.png"},
		{name: "file URL", raw: "file:///private/var/private.png"},
		{name: "relative filesystem path", raw: "../../private.png"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := stableAttachmentSource(test.raw); got != test.want {
				t.Fatalf(
					"stableAttachmentSource(%q) = %q, want %q",
					test.raw,
					got,
					test.want,
				)
			}
		})
	}
}

func TestConversationAttachmentMetadataDoesNotLeakPrivateLocations(
	t *testing.T,
) {
	attachment, ok := conversationAttachmentFromRaw(map[string]any{
		"content_type":        "image_asset_pointer",
		"image_asset_pointer": "https://chatgpt.com/backend-api/files/file-1/download?sig=private-value",
		"id":                  "sandbox:/mnt/data/private-id",
		"file_name":           "/Users/example/private-image.png",
		"alt_text":            "sandbox:/mnt/data/private-image.png",
		"width":               float64(1536),
		"height":              float64(1024),
	}, "")
	if !ok {
		t.Fatal("safe attachment metadata was discarded")
	}
	encoded, err := json.Marshal(attachment)
	if err != nil {
		t.Fatalf("marshal attachment: %v", err)
	}
	serialized := string(encoded)
	if attachment.Source !=
		"https://chatgpt.com/backend-api/files/file-1/download" ||
		attachment.FileID != "" ||
		attachment.FileName != "private-image.png" ||
		attachment.Alt != "" ||
		strings.Contains(serialized, "private-value") ||
		strings.Contains(serialized, "sandbox:") ||
		strings.Contains(serialized, "/mnt/data/") ||
		strings.Contains(serialized, "/Users/") {
		t.Fatalf("attachment leaked private metadata: %s", serialized)
	}
}

func TestConversationAttachmentAltDoesNotExposeEmbeddedSignedURL(
	t *testing.T,
) {
	attachment, ok := conversationAttachmentFromRaw(map[string]any{
		"content_type":  "image_asset_pointer",
		"asset_pointer": "sediment://file_safe_image",
		"alt_text": "Open https://files.example.test/image.png" +
			"?sig=private-alt-value to download",
	}, "")
	if !ok {
		t.Fatal("safe image pointer was discarded")
	}
	encoded, err := json.Marshal(attachment)
	if err != nil {
		t.Fatalf("marshal attachment: %v", err)
	}
	if attachment.Alt != "" ||
		strings.Contains(string(encoded), "private-alt-value") ||
		strings.Contains(string(encoded), "?sig=") {
		t.Fatalf("attachment leaked signed alt URL: %s", encoded)
	}
}

func TestConversationAttachmentsMergePartialStableIdentities(t *testing.T) {
	byIDAndSource := map[string]any{
		"content_type":  "file_asset_pointer",
		"file_id":       "file_synthetic_shared",
		"asset_pointer": "sediment://file_synthetic_shared",
	}
	byIDAndName := map[string]any{
		"content_type": "file_asset_pointer",
		"file_id":      "file_synthetic_shared",
		"file_name":    "synthetic-shared.pdf",
		"mime_type":    "application/pdf",
	}
	bySourceAndSize := map[string]any{
		"content_type":  "file_asset_pointer",
		"asset_pointer": "sediment://file_synthetic_shared",
		"size_bytes":    float64(4096),
		"alt_text":      "Synthetic shared report",
	}
	orders := [][]any{
		{byIDAndSource, byIDAndName, bySourceAndSize},
		{bySourceAndSize, byIDAndName, byIDAndSource},
	}
	var firstJSON string
	for index, values := range orders {
		attachments, truncated := conversationAttachments(map[string]any{
			"metadata": map[string]any{"attachments": values},
		})
		if truncated || len(attachments) != 1 {
			t.Fatalf(
				"order=%d attachments=%+v truncated=%v",
				index,
				attachments,
				truncated,
			)
		}
		attachment := attachments[0]
		if attachment.Kind != "file" ||
			attachment.FileID != "file_synthetic_shared" ||
			attachment.Source != "sediment://file_synthetic_shared" ||
			attachment.FileName != "synthetic-shared.pdf" ||
			attachment.MIMEType != "application/pdf" ||
			attachment.SizeBytes != 4096 ||
			attachment.Alt != "Synthetic shared report" {
			t.Fatalf("order=%d attachment=%+v", index, attachment)
		}
		encoded, err := json.Marshal(attachment)
		if err != nil {
			t.Fatalf("marshal order %d: %v", index, err)
		}
		if index == 0 {
			firstJSON = string(encoded)
		} else if string(encoded) != firstJSON {
			t.Fatalf(
				"order-dependent merge:\nfirst=%s\nnext=%s",
				firstJSON,
				encoded,
			)
		}
	}
}

func TestConversationAttachmentsDoNotMergeGenericSignedDownloadRoutes(
	t *testing.T,
) {
	attachments, truncated := conversationAttachments(map[string]any{
		"content": map[string]any{
			"parts": []any{
				map[string]any{
					"content_type": "image_asset_pointer",
					"download_url": "https://chatgpt.com/backend-api/estuary/content?sig=first-private-value",
					"width":        float64(1024),
					"height":       float64(1024),
				},
				map[string]any{
					"content_type": "image_asset_pointer",
					"download_url": "https://chatgpt.com/backend-api/estuary/content?sig=second-private-value",
					"width":        float64(1024),
					"height":       float64(1024),
				},
			},
		},
	})
	if truncated || len(attachments) != 2 {
		t.Fatalf("attachments=%+v truncated=%v", attachments, truncated)
	}
	encoded, err := json.Marshal(attachments)
	if err != nil {
		t.Fatalf("marshal attachments: %v", err)
	}
	if strings.Contains(string(encoded), "private-value") ||
		strings.Contains(string(encoded), "sig=") {
		t.Fatalf("attachments leaked signed query data: %s", encoded)
	}
}

func TestConversationAttachmentsPreservePrivateHTTPSIdentityAcrossPermutations(
	t *testing.T,
) {
	first := map[string]any{
		"content_type": "image_asset_pointer",
		"download_url": "https://files.example.test/download" +
			"?asset=first-private-value&sig=first-private-signature",
		"alt_text": "First generated image",
		"width":    float64(1200),
		"height":   float64(800),
	}
	firstDetails := map[string]any{
		"content_type": "image_asset_pointer",
		"download_url": "https://files.example.test/download" +
			"?asset=first-private-value&sig=first-private-signature",
		"mime_type": "image/png",
	}
	second := map[string]any{
		"content_type": "image_asset_pointer",
		"download_url": "https://files.example.test/download" +
			"?asset=second-private-value&sig=second-private-signature",
		"alt_text": "Second generated image",
		"width":    float64(1200),
		"height":   float64(800),
	}
	orders := [][]any{
		{first, firstDetails, second},
		{first, second, firstDetails},
		{firstDetails, first, second},
		{firstDetails, second, first},
		{second, first, firstDetails},
		{second, firstDetails, first},
	}

	var firstJSON string
	for index, values := range orders {
		attachments, truncated := conversationAttachments(map[string]any{
			"content": map[string]any{"parts": values},
		})
		if truncated || len(attachments) != 2 {
			t.Fatalf(
				"permutation=%d attachments=%+v truncated=%v",
				index,
				attachments,
				truncated,
			)
		}
		alts := map[string]bool{}
		for _, attachment := range attachments {
			if attachment.Source != "https://files.example.test/download" {
				t.Fatalf(
					"permutation=%d source=%q",
					index,
					attachment.Source,
				)
			}
			alts[attachment.Alt] = true
		}
		if !alts["First generated image"] ||
			!alts["Second generated image"] {
			t.Fatalf("permutation=%d attachments=%+v", index, attachments)
		}
		encoded, err := json.Marshal(attachments)
		if err != nil {
			t.Fatalf("marshal permutation %d: %v", index, err)
		}
		serialized := string(encoded)
		for _, privateValue := range []string{
			"first-private-value",
			"second-private-value",
			"first-private-signature",
			"second-private-signature",
			"asset=",
			"sig=",
		} {
			if strings.Contains(serialized, privateValue) {
				t.Fatalf(
					"permutation=%d leaked %q: %s",
					index,
					privateValue,
					serialized,
				)
			}
		}
		if index == 0 {
			firstJSON = serialized
		} else if serialized != firstJSON {
			t.Fatalf(
				"order-dependent private identity output:\nfirst=%s\nnext=%s",
				firstJSON,
				serialized,
			)
		}
	}
}

func TestConversationAttachmentsDoNotMergeConflictingIdentityBridge(
	t *testing.T,
) {
	assetA := map[string]any{
		"content_type":  "file_asset_pointer",
		"file_id":       "file_asset_a",
		"asset_pointer": "sediment://source_asset_a",
		"file_name":     "asset-a.pdf",
	}
	assetB := map[string]any{
		"content_type":  "file_asset_pointer",
		"file_id":       "file_asset_b",
		"asset_pointer": "sediment://source_asset_b",
		"file_name":     "asset-b.pdf",
	}
	conflictingBridge := map[string]any{
		"content_type":  "file_asset_pointer",
		"file_id":       "file_asset_a",
		"asset_pointer": "sediment://source_asset_b",
		"alt_text":      "Conflicting bridge record",
	}
	orders := [][]any{
		{assetA, assetB, conflictingBridge},
		{assetA, conflictingBridge, assetB},
		{assetB, assetA, conflictingBridge},
		{assetB, conflictingBridge, assetA},
		{conflictingBridge, assetA, assetB},
		{conflictingBridge, assetB, assetA},
	}

	var firstJSON string
	for index, values := range orders {
		attachments, truncated := conversationAttachments(map[string]any{
			"metadata": map[string]any{"attachments": values},
		})
		if truncated || len(attachments) != 3 {
			t.Fatalf(
				"permutation=%d attachments=%+v truncated=%v",
				index,
				attachments,
				truncated,
			)
		}
		identities := map[string]bool{}
		for _, attachment := range attachments {
			identities[attachment.FileID+"|"+attachment.Source] = true
		}
		for _, identity := range []string{
			"file_asset_a|sediment://source_asset_a",
			"file_asset_b|sediment://source_asset_b",
			"file_asset_a|sediment://source_asset_b",
		} {
			if !identities[identity] {
				t.Fatalf(
					"permutation=%d missing identity %q: %+v",
					index,
					identity,
					attachments,
				)
			}
		}
		encoded, err := json.Marshal(attachments)
		if err != nil {
			t.Fatalf("marshal permutation %d: %v", index, err)
		}
		if index == 0 {
			firstJSON = string(encoded)
		} else if string(encoded) != firstJSON {
			t.Fatalf(
				"order-dependent conflict output:\nfirst=%s\nnext=%s",
				firstJSON,
				encoded,
			)
		}
	}
}

func TestConversationAttachmentsKeepDeterministicOutputBounded(t *testing.T) {
	values := make([]any, 0, maxConversationAttachments+2)
	for index := 0; index < maxConversationAttachments+2; index++ {
		values = append(values, map[string]any{
			"content_type":  "file_asset_pointer",
			"file_id":       fmt.Sprintf("file_%03d", index),
			"asset_pointer": fmt.Sprintf("sediment://source_%03d", index),
		})
	}
	reversed := append([]any(nil), values...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}

	var firstJSON string
	for index, orderedValues := range [][]any{values, reversed} {
		attachments, truncated := conversationAttachments(map[string]any{
			"metadata": map[string]any{"attachments": orderedValues},
		})
		if !truncated || len(attachments) != maxConversationAttachments {
			t.Fatalf(
				"permutation=%d attachments=%d truncated=%v",
				index,
				len(attachments),
				truncated,
			)
		}
		encoded, err := json.Marshal(attachments)
		if err != nil {
			t.Fatalf("marshal permutation %d: %v", index, err)
		}
		if index == 0 {
			firstJSON = string(encoded)
		} else if string(encoded) != firstJSON {
			t.Fatalf(
				"order-dependent bounded output:\nfirst=%s\nnext=%s",
				firstJSON,
				encoded,
			)
		}
	}
}

func TestConversationAttachmentsPreserveObservedDimensionPairs(
	t *testing.T,
) {
	tests := []struct {
		name   string
		first  [2]int
		second [2]int
	}{
		{
			name:   "crossed complete dimensions",
			first:  [2]int{1920, 800},
			second: [2]int{1280, 1080},
		},
		{
			name:   "complete and wider partial dimensions",
			first:  [2]int{1920, 1080},
			second: [2]int{2560, 0},
		},
		{
			name:   "complementary partial dimensions",
			first:  [2]int{2560, 0},
			second: [2]int{0, 1440},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := map[string]any{
				"content_type": "image_asset_pointer",
				"file_id":      "file_shared_image",
				"width":        float64(test.first[0]),
				"height":       float64(test.first[1]),
			}
			second := map[string]any{
				"content_type": "image_asset_pointer",
				"file_id":      "file_shared_image",
				"width":        float64(test.second[0]),
				"height":       float64(test.second[1]),
			}
			orders := [][]any{{first, second}, {second, first}}
			observed := map[[2]int]bool{
				test.first:  true,
				test.second: true,
			}
			var firstJSON string
			for index, values := range orders {
				attachments, truncated := conversationAttachments(map[string]any{
					"content": map[string]any{"parts": values},
				})
				if truncated || len(attachments) != 1 {
					t.Fatalf(
						"permutation=%d attachments=%+v truncated=%v",
						index,
						attachments,
						truncated,
					)
				}
				dimensions := [2]int{
					attachments[0].Width,
					attachments[0].Height,
				}
				if !observed[dimensions] {
					t.Fatalf(
						"permutation=%d invented dimensions=%v, observed=%v",
						index,
						dimensions,
						observed,
					)
				}
				encoded, err := json.Marshal(attachments)
				if err != nil {
					t.Fatalf("marshal permutation %d: %v", index, err)
				}
				if index == 0 {
					firstJSON = string(encoded)
				} else if string(encoded) != firstJSON {
					t.Fatalf(
						"order-dependent dimensions:\nfirst=%s\nnext=%s",
						firstJSON,
						encoded,
					)
				}
			}
		})
	}
}

func TestStableAttachmentAltRejectsSignedURIsAndLocalPaths(t *testing.T) {
	privateValues := []string{
		"Open s3://private-bucket/image.png?X-Amz-Signature=private-value",
		"Open ftp://files.example.test/image.png?token=private-value",
		"Open custom+asset:item?signature=private-value",
		"Open file:/Volumes/External/private-image.png",
		"Rendered from /Volumes/External/private-image.png",
		"Rendered from /opt/project/private-image.png",
		`Rendered from C:\workspace\private-image.png`,
		"Rendered from D:/workspace/private-image.png",
		`Rendered from \\server\share\private-image.png`,
		"Rendered from //server/share/private-image.png",
		"Rendered from ~/private-image.png",
	}
	for _, value := range privateValues {
		if got := stableAttachmentAlt(value); got != "" {
			t.Errorf("stableAttachmentAlt(%q) = %q, want empty", value, got)
		}
	}

	ordinaryProse := []string{
		"A blue/green deployment diagram",
		"A comparison of Windows, macOS, and network storage",
		"Volume 2 cover art",
		"Choose yes/no in the illustrated dialog",
		"A cinematic 16/9 landscape image",
	}
	for _, value := range ordinaryProse {
		if got := stableAttachmentAlt(value); got != value {
			t.Errorf("stableAttachmentAlt(%q) = %q, want unchanged", value, got)
		}
	}
}

func TestConversationDetailTransportsReturnEmptyAttachmentArrays(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		config      func(*testing.T) ReadConfig
		wantTarget  bool
		wantCleanup webagent.CleanupState
	}{
		{
			name: "direct",
			config: func(t *testing.T) ReadConfig {
				return ReadConfig{
					Store:      newReadTestStore(t, now),
					HTTPClient: fixedHTTPClient(http.StatusOK, terminalDetailPayload),
					Now:        func() time.Time { return now },
				}
			},
			wantCleanup: webagent.CleanupNotRequired,
		},
		{
			name: "headed browser fallback",
			config: func(t *testing.T) ReadConfig {
				client := newAuthenticatedReadBrowser(func(
					expression string,
					_ *testsupport.Browser,
				) (any, error) {
					switch {
					case strings.Contains(expression, "signed_in:"):
						return map[string]any{
							"signed_in":  true,
							"signed_out": false,
						}, nil
					case strings.Contains(expression, "const response = await fetch"):
						return map[string]any{
							"ok":          true,
							"status_code": http.StatusOK,
							"body":        terminalDetailPayload,
						}, nil
					default:
						return map[string]any{}, nil
					}
				})
				engine, journal, err := testsupport.NewRuntime(
					t.TempDir(),
					client,
				)
				if err != nil {
					t.Fatalf("NewRuntime: %v", err)
				}
				return ReadConfig{
					Store: newReadTestStore(t, now),
					HTTPClient: fixedHTTPClient(
						http.StatusServiceUnavailable,
						"",
					),
					BrowserConfig: &BrowserConfig{
						Client:  client,
						Engine:  engine,
						Journal: journal,
					},
					Now: func() time.Time { return now },
				}
			},
			wantTarget:  true,
			wantCleanup: webagent.CleanupClosed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := DetailConversation(
				context.Background(),
				test.config(t),
				"conversation-1",
			)
			data, ok := result.Data.(ConversationDetailData)
			if !ok || !result.OK || result.State != webagent.StateTerminal {
				t.Fatalf("result=%+v data=%+v", result, data)
			}
			if data.Attachments == nil || len(data.Attachments) != 0 {
				t.Fatalf("attachments=%#v", data.Attachments)
			}
			encoded, err := json.Marshal(data)
			if err != nil {
				t.Fatalf("marshal detail data: %v", err)
			}
			if !strings.Contains(string(encoded), `"attachments":[]`) {
				t.Fatalf("detail JSON omitted empty attachments: %s", encoded)
			}
			if (result.Evidence.Target != nil) != test.wantTarget ||
				result.Cleanup.State != test.wantCleanup {
				t.Fatalf("result=%+v", result)
			}
			if result.Evidence.Target != nil &&
				!result.Evidence.Target.Closed {
				t.Fatalf("owned target was not closed: %+v", result.Evidence.Target)
			}
		})
	}
}

func TestAwaitTerminalNoAnswerProbeResumesUntilLaterDirectAnswer(t *testing.T) {
	tests := []struct {
		name                string
		renderedObservation func() (any, error)
	}{
		{
			name: "inconclusive rendered confirmation",
			renderedObservation: func() (any, error) {
				return map[string]any{
					"route_matches":            true,
					"conversation_id":          "conversation-1",
					"text":                     "",
					"prompt_candidates":        []any{"Review."},
					"is_streaming":             false,
					"terminal_control_present": true,
					"assistant_count":          1,
					"user_message_count":       1,
					"terminal_no_answer":       false,
				}, nil
			},
		},
		{
			name: "unavailable rendered confirmation",
			renderedObservation: func() (any, error) {
				return nil, errors.New("rendered observation unavailable")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
			browserFetches := 0
			waitCalls := 0
			client := newAuthenticatedReadBrowser(func(
				expression string,
				_ *testsupport.Browser,
			) (any, error) {
				switch {
				case strings.Contains(expression, "signed_in:"):
					return map[string]any{
						"signed_in": true, "signed_out": false,
					}, nil
				case strings.Contains(expression, "const response = await fetch"):
					browserFetches++
					body := terminalNoAnswerCandidateDetailPayload
					if browserFetches > 1 {
						body = terminalDetailPayload
					}
					return map[string]any{
						"ok":          true,
						"status_code": http.StatusOK,
						"body":        body,
					}, nil
				case strings.Contains(
					expression,
					"terminal_no_answer_reason",
				):
					return test.renderedObservation()
				default:
					return map[string]any{}, nil
				}
			})
			engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
			if err != nil {
				t.Fatalf("NewRuntime: %v", err)
			}
			result := AwaitConversation(context.Background(), ReadConfig{
				Store: newReadTestStore(t, now),
				HTTPClient: fixedHTTPClient(
					http.StatusOK,
					terminalNoAnswerCandidateDetailPayload,
				),
				BrowserConfig: &BrowserConfig{
					Client: client, Engine: engine, Journal: journal,
				},
				Now:         func() time.Time { return now },
				AwaitDelays: []time.Duration{100 * time.Millisecond},
				Wait: func(_ context.Context, delay time.Duration) error {
					waitCalls++
					now = now.Add(delay)
					return nil
				},
			}, "conversation-1", 2*time.Second)

			data, ok := result.Data.(ConversationDetailData)
			if !ok ||
				!result.OK ||
				result.State != webagent.StateTerminal ||
				data.CompletionState != conversationCompletionTerminal ||
				data.Text != "Terminal review." {
				t.Fatalf("result=%+v data=%+v", result, data)
			}
			if browserFetches != 2 || waitCalls < 1 {
				t.Fatalf(
					"browser fetches=%d waits=%d, want two fetches with a retry",
					browserFetches,
					waitCalls,
				)
			}
		})
	}
}

func TestAwaitTerminalNoAnswerProbeOnlyReturnsIncompleteAtDeadline(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	browserFetches := 0
	waitCalls := 0
	client := newAuthenticatedReadBrowser(func(
		expression string,
		_ *testsupport.Browser,
	) (any, error) {
		switch {
		case strings.Contains(expression, "signed_in:"):
			return map[string]any{
				"signed_in": true, "signed_out": false,
			}, nil
		case strings.Contains(expression, "const response = await fetch"):
			browserFetches++
			return map[string]any{
				"ok":          true,
				"status_code": http.StatusOK,
				"body":        terminalNoAnswerCandidateDetailPayload,
			}, nil
		case strings.Contains(expression, "terminal_no_answer_reason"):
			return map[string]any{
				"route_matches":            true,
				"conversation_id":          "conversation-1",
				"text":                     "",
				"prompt_candidates":        []any{"Review."},
				"is_streaming":             false,
				"terminal_control_present": true,
				"assistant_count":          1,
				"user_message_count":       1,
				"terminal_no_answer":       false,
			}, nil
		default:
			return map[string]any{}, nil
		}
	})
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	result := AwaitConversation(context.Background(), ReadConfig{
		Store: newReadTestStore(t, now),
		HTTPClient: fixedHTTPClient(
			http.StatusOK,
			terminalNoAnswerCandidateDetailPayload,
		),
		BrowserConfig: &BrowserConfig{
			Client: client, Engine: engine, Journal: journal,
		},
		Now:         func() time.Time { return now },
		AwaitDelays: []time.Duration{250 * time.Millisecond},
		Wait: func(_ context.Context, delay time.Duration) error {
			waitCalls++
			now = now.Add(delay)
			return nil
		},
	}, "conversation-1", time.Second)

	data, ok := result.Data.(ConversationDetailData)
	if !ok ||
		!result.OK ||
		result.State != webagent.StateIncomplete ||
		data.CompletionState != conversationCompletionIncomplete {
		t.Fatalf("result=%+v data=%+v", result, data)
	}
	if browserFetches < 2 || waitCalls < 2 {
		t.Fatalf(
			"browser fetches=%d waits=%d, want polling through the deadline",
			browserFetches,
			waitCalls,
		)
	}
}

func TestAwaitUnavailableRenderedProbeResumesDirectPolling(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	directFetches := 0
	fallbackCalls := 0
	waitCalls := 0
	httpClient := &http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			directFetches++
			body := terminalNoAnswerCandidateDetailPayload
			if directFetches > 1 {
				body = terminalDetailPayload
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		},
	)}
	result := AwaitConversation(context.Background(), ReadConfig{
		Store:      newReadTestStore(t, now),
		HTTPClient: httpClient,
		BrowserFallback: func(context.Context) (*BrowserConfig, error) {
			fallbackCalls++
			return nil, errors.New("headed confirmation unavailable")
		},
		Now:         func() time.Time { return now },
		AwaitDelays: []time.Duration{100 * time.Millisecond},
		Wait: func(_ context.Context, delay time.Duration) error {
			waitCalls++
			now = now.Add(delay)
			return nil
		},
	}, "conversation-1", 2*time.Second)

	data, ok := result.Data.(ConversationDetailData)
	if !ok ||
		!result.OK ||
		result.State != webagent.StateTerminal ||
		data.CompletionState != conversationCompletionTerminal ||
		data.Text != "Terminal review." {
		t.Fatalf("result=%+v data=%+v", result, data)
	}
	if directFetches != 2 || fallbackCalls != 1 || waitCalls < 1 {
		t.Fatalf(
			"direct fetches=%d fallback calls=%d waits=%d, want direct retry after unavailable confirmation",
			directFetches,
			fallbackCalls,
			waitCalls,
		)
	}
}

func TestTerminalNoAnswerCandidateFailsClosed(t *testing.T) {
	tests := []struct {
		name              string
		payload           string
		rendered          map[string]any
		wantState         webagent.State
		wantCompletion    string
		wantText          string
		wantBrowserTarget bool
	}{
		{
			name:           "active async state remains incomplete",
			payload:        activeNoAnswerDetailPayload,
			wantState:      webagent.StateIncomplete,
			wantCompletion: "incomplete",
		},
		{
			name:           "later hydrated answer wins without a browser probe",
			payload:        terminalDetailPayload,
			wantState:      webagent.StateTerminal,
			wantCompletion: "terminal",
			wantText:       "Terminal review.",
		},
		{
			name:    "stale stop marker cannot override latest rendered answer",
			payload: terminalNoAnswerCandidateDetailPayload,
			rendered: map[string]any{
				"route_matches":                   true,
				"conversation_id":                 "conversation-1",
				"text":                            "Later rendered answer.",
				"prompt_candidates":               []any{"Review."},
				"is_streaming":                    false,
				"terminal_control_present":        true,
				"assistant_count":                 2,
				"user_message_count":              1,
				"stopped_thinking_marker_present": false,
				"terminal_no_answer":              false,
				"terminal_no_answer_reason":       "",
			},
			wantState:         webagent.StateTerminal,
			wantCompletion:    "terminal",
			wantText:          "Later rendered answer.",
			wantBrowserTarget: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
			store := newReadTestStore(t, now)
			client := newAuthenticatedReadBrowser(func(
				expression string,
				_ *testsupport.Browser,
			) (any, error) {
				switch {
				case strings.Contains(expression, "signed_in:"):
					return map[string]any{
						"signed_in": true, "signed_out": false,
					}, nil
				case strings.Contains(expression, "const response = await fetch"):
					return map[string]any{
						"ok":          true,
						"status_code": http.StatusOK,
						"body":        test.payload,
					}, nil
				case strings.Contains(
					expression,
					"terminal_no_answer_reason",
				):
					return test.rendered, nil
				default:
					return map[string]any{}, nil
				}
			})
			engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
			if err != nil {
				t.Fatalf("NewRuntime: %v", err)
			}
			result := DetailConversation(context.Background(), ReadConfig{
				Store:      store,
				HTTPClient: fixedHTTPClient(http.StatusOK, test.payload),
				BrowserConfig: &BrowserConfig{
					Client: client, Engine: engine, Journal: journal,
				},
				Now: func() time.Time { return now },
			}, "conversation-1")

			data, ok := result.Data.(ConversationDetailData)
			if !ok ||
				!result.OK ||
				result.State != test.wantState ||
				data.CompletionState != test.wantCompletion ||
				data.CompletionReason != "" ||
				data.Text != test.wantText {
				t.Fatalf("result=%+v data=%+v", result, data)
			}
			if got := result.Evidence.Target != nil; got != test.wantBrowserTarget {
				t.Fatalf(
					"browser target observed = %v, want %v; result=%+v",
					got,
					test.wantBrowserTarget,
					result,
				)
			}
		})
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
