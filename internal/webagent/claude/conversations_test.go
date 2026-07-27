package claude

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestListConversationsUsesObservedPrivateTemplate(t *testing.T) {
	config := newReadTestConfig(t)
	var observed *http.Request
	config.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		observed = request.Clone(context.Background())
		return jsonResponse(http.StatusOK, map[string]any{
			"data": []map[string]any{
				{
					"uuid":                      "conversation-1",
					"name":                      "Adapter review",
					"summary":                   "Boundary findings",
					"model":                     "provider-model",
					"created_at":                "2026-07-19T00:00:00Z",
					"updated_at":                "2026-07-19T01:00:00Z",
					"is_starred":                false,
					"is_temporary":              false,
					"current_leaf_message_uuid": "assistant-1",
				},
			},
			"has_more": true,
		}), nil
	})}

	result := ListConversations(context.Background(), config, 1)
	if !result.OK ||
		result.Operation != webagent.OperationConversationsList ||
		result.State != webagent.StateReady ||
		result.Evidence.BrowserMode != "none" ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("list result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("list result validation: %v", err)
	}
	data, ok := result.Data.(ConversationListData)
	if !ok ||
		len(data.Conversations) != 1 ||
		data.Conversations[0].ID != "conversation-1" ||
		!data.HasMore ||
		data.ReadMode != "observed_stable_http" {
		t.Fatalf("list data = %#v", result.Data)
	}
	if observed == nil ||
		observed.URL.String() != validAuthTemplate(readTestNow()).ListURL ||
		observed.UserAgent() != "Browser/Test" ||
		strings.Contains(observed.Header.Get("Accept-Encoding"), "zstd") {
		t.Fatalf("observed request = %+v", observed)
	}
	cookie, err := observed.Cookie("sessionKey")
	if err != nil || cookie.Value != "private-session-cookie" {
		t.Fatalf("observed session cookie = %+v err=%v", cookie, err)
	}
}

func TestDetailAndAwaitUseProviderTerminalSemanticsWithoutPromptLeak(t *testing.T) {
	t.Run("terminal detail", func(t *testing.T) {
		config := newReadTestConfig(t)
		config.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/api/organizations/org-1/chat_conversations/conversation-1" ||
				request.URL.Query().Get("tree") != "True" ||
				request.URL.Query().Get("rendering_mode") != "messages" ||
				request.URL.Query().Get("render_all_tools") != "true" {
				t.Fatalf("detail URL = %s", request.URL)
			}
			return jsonResponse(http.StatusOK, terminalDetailPayload("Review private migration")), nil
		})}

		result := DetailConversation(context.Background(), config, "conversation-1")
		if !result.OK ||
			result.State != webagent.StateTerminal ||
			result.Conversation == nil ||
			result.Conversation.ID != "conversation-1" {
			t.Fatalf("detail result = %+v", result)
		}
		data, ok := result.Data.(ConversationDetailData)
		if !ok ||
			data.Text != "Useful review" ||
			data.CompletionState != "terminal" ||
			data.Metadata["prompt_fingerprint"] != fingerprintPrompt("Review private migration") {
			t.Fatalf("detail data = %#v", result.Data)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal detail result: %v", err)
		}
		if strings.Contains(string(encoded), "Review private migration") ||
			strings.Contains(string(encoded), "private thinking") {
			t.Fatalf("prompt or thinking leaked into detail result: %s", encoded)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("detail validation: %v", err)
		}
	})

	t.Run("await retries only incomplete detail", func(t *testing.T) {
		config := newReadTestConfig(t)
		config.AwaitDelays = []time.Duration{0}
		calls := 0
		config.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			payload := terminalDetailPayload("Review")
			if calls == 1 {
				messages := payload["chat_messages"].([]map[string]any)
				messages[1]["truncated"] = true
				messages[1]["content"] = []map[string]any{
					{"type": "text", "text": "Partial", "stop_timestamp": nil},
				}
			}
			return jsonResponse(http.StatusOK, payload), nil
		})}

		result := AwaitConversation(context.Background(), config, "conversation-1", time.Minute)
		if !result.OK || result.State != webagent.StateTerminal || calls != 2 {
			t.Fatalf("await result = %+v calls=%d", result, calls)
		}
		data := result.Data.(ConversationDetailData)
		if data.Metadata["detail_read_attempts"] != 2 {
			t.Fatalf("await attempts = %#v", data.Metadata["detail_read_attempts"])
		}
	})
}

func TestConversationReadsRateLimitAndInvalidResponseStayTyped(t *testing.T) {
	t.Run("rate limit does not gate the next read", func(t *testing.T) {
		config := newReadTestConfig(t)
		calls := 0
		config.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			response := &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": []string{"23"}},
				Body:       io.NopCloser(strings.NewReader("rate limited")),
			}
			return response, nil
		})}

		first := DetailConversation(context.Background(), config, "conversation-1")
		if first.OK ||
			first.Error == nil ||
			first.Error.Code != "claude_rate_limited" ||
			first.Error.RetryAt != readTestNow().Add(23*time.Second).Format(time.RFC3339Nano) ||
			calls != 1 {
			t.Fatalf("rate-limit result = %+v calls=%d", first, calls)
		}
		second := DetailConversation(context.Background(), config, "conversation-1")
		if second.OK ||
			second.Error == nil ||
			second.Error.Code != "claude_rate_limited" ||
			calls != 2 {
			t.Fatalf("second rate-limit result = %+v calls=%d", second, calls)
		}
	})

	t.Run("invalid body is sanitized", func(t *testing.T) {
		const privateCanary = "PRIVATE-RESPONSE-CANARY"
		config := newReadTestConfig(t)
		config.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("{invalid " + privateCanary)),
			}, nil
		})}
		result := DetailConversation(context.Background(), config, "conversation-1")
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal invalid response result: %v", err)
		}
		if result.OK ||
			result.Error == nil ||
			result.Error.Code != "claude_invalid_detail_response" ||
			strings.Contains(string(encoded), privateCanary) {
			t.Fatalf("invalid response result = %s", encoded)
		}
	})
}

func TestBrowserContextRejectionUsesOneRenderedExactTarget(t *testing.T) {
	stateDir := t.TempDir()
	config := newReadTestConfigAt(t, stateDir)
	httpCalls := 0
	config.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("browser context required")),
		}, nil
	})}
	client := newAuthFakeClient("user-page")
	client.renderedSidebarExpanded = true
	client.renderedConversations = []map[string]any{
		{"conversation_id": "conversation-1", "title": "Rendered review"},
	}
	config.NewRenderedFallback = renderedFallbackForTest(t, stateDir, client)

	result := ListConversations(context.Background(), config, 30)

	if !result.OK ||
		result.State != webagent.StateReady ||
		result.Evidence.BrowserMode != "headed" ||
		result.Evidence.ReadMode != "headed_browser" ||
		result.Evidence.Target == nil ||
		!result.Evidence.Target.Closed ||
		result.Cleanup.State != webagent.CleanupClosed ||
		httpCalls != 1 ||
		client.callCount("Target.createTarget") != 1 ||
		client.callCount("Target.closeTarget") != 1 ||
		client.hasTarget("owned-1") ||
		!client.hasTarget("user-page") {
		t.Fatalf(
			"rendered fallback result=%+v http_calls=%d cdp=%+v",
			result,
			httpCalls,
			client.countSnapshot(),
		)
	}
	data, ok := result.Data.(ConversationListData)
	if !ok ||
		data.ReadMode != "headed_browser" ||
		len(data.Conversations) != 1 ||
		data.Conversations[0].ID != "conversation-1" {
		t.Fatalf("rendered list data = %#v", result.Data)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("rendered fallback validation: %v", err)
	}
}

func TestRenderedConversationListWaitsForLateHistoryAnchors(t *testing.T) {
	stateDir := t.TempDir()
	config := newReadTestConfigAt(t, stateDir)
	config.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("browser context required")),
		}, nil
	})}
	client := newAuthFakeClient("user-page")
	client.renderedSidebarExpanded = true
	client.renderedListSnapshots = [][]map[string]any{
		{
			{"conversation_id": "conversation-1", "title": "First"},
		},
		{
			{"conversation_id": "conversation-1", "title": "First"},
			{"conversation_id": "conversation-2", "title": "Late"},
		},
	}
	config.NewRenderedFallback = renderedFallbackForTest(t, stateDir, client)

	result := ListConversations(context.Background(), config, 3)

	data, ok := result.Data.(ConversationListData)
	if !result.OK ||
		!ok ||
		len(data.Conversations) != 2 ||
		data.Conversations[1].ID != "conversation-2" ||
		client.renderedListReads < renderedListStableReads+1 {
		t.Fatalf(
			"settled rendered list result=%+v data=%#v reads=%d",
			result,
			result.Data,
			client.renderedListReads,
		)
	}
}

func newReadTestConfig(t *testing.T) ReadConfig {
	t.Helper()
	return newReadTestConfigAt(t, t.TempDir())
}

func newReadTestConfigAt(t *testing.T, stateDir string) ReadConfig {
	t.Helper()
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Save(context.Background(), validAuthTemplate(readTestNow())); err != nil {
		t.Fatalf("save auth template: %v", err)
	}
	return ReadConfig{
		Store:       store,
		BuildCommit: "test-commit",
		Now:         readTestNow,
	}
}

func renderedFallbackForTest(
	t *testing.T,
	stateDir string,
	client *authFakeClient,
) RenderedFallbackFactory {
	t.Helper()
	return func(context.Context) (RenderedReadConfig, func(context.Context) error, error) {
		journal, err := browserflow.NewFileJournal(stateDir)
		if err != nil {
			return RenderedReadConfig{}, nil, err
		}
		engine, err := browserflow.New(browserflow.Config{
			Client:  client,
			Journal: journal,
			Budget: cdp.BrowserResourceBudgetOptions{
				MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
			},
		})
		if err != nil {
			return RenderedReadConfig{}, nil, err
		}
		return RenderedReadConfig{
			Client:       client,
			Engine:       engine,
			Journal:      journal,
			Timeout:      100 * time.Millisecond,
			PollInterval: time.Millisecond,
		}, func(context.Context) error { return nil }, nil
	}
}

func terminalDetailPayload(prompt string) map[string]any {
	return map[string]any{
		"uuid":                      "conversation-1",
		"current_leaf_message_uuid": "assistant-1",
		"chat_messages": []map[string]any{
			{
				"uuid":      "human-1",
				"sender":    "human",
				"index":     0,
				"truncated": false,
				"content": []map[string]any{
					{"type": "text", "text": prompt, "stop_timestamp": "done"},
				},
			},
			{
				"uuid":      "assistant-1",
				"sender":    "assistant",
				"index":     1,
				"truncated": false,
				"content": []map[string]any{
					{"type": "thinking", "thinking": "private thinking", "stop_timestamp": "done"},
					{"type": "text", "text": "Useful review", "stop_timestamp": "done"},
				},
			},
		},
	}
}

func jsonResponse(status int, payload any) *http.Response {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}
}

func readTestNow() time.Time {
	return time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
