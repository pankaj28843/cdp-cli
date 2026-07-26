package perplexity

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestParseRuntimeCapabilitiesPreservesProviderCatalog(t *testing.T) {
	runtime, err := parseRuntimeCapabilities([]byte(`{
	  "search_config": [
	    {
	      "label": "Search",
	      "description": "Search the web",
	      "is_default": true,
	      "subscription_tier": "free",
	      "audience": "all",
	      "has_new_tag": false
	    },
	    {
	      "label": "Research",
	      "description": "Long-running research",
	      "subscription_tier": "pro",
	      "audience": "paid",
	      "has_new_tag": true
	    }
	  ],
	  "computer_config": []
	}`), "2026-07-25T20:00:00Z")
	if err != nil {
		t.Fatalf("parse runtime capabilities: %v", err)
	}
	if runtime.State != "ready" || len(runtime.Capabilities) != 2 {
		t.Fatalf("runtime=%+v", runtime)
	}
	if got := runtime.Capabilities[0]; got.ID != "search" ||
		!got.Selected || !got.Available {
		t.Fatalf("search capability=%+v", got)
	}
	if got := runtime.Capabilities[1]; got.ID != "research" ||
		got.Available ||
		got.FailureReason != "entitlement_unverified:pro" {
		t.Fatalf("research capability=%+v", got)
	}
}

func TestParseRuntimeCapabilitiesTreatsSelectedPaidDefaultAsAvailable(t *testing.T) {
	runtime, err := parseRuntimeCapabilities([]byte(`{
	  "search_config": [{
	    "label": "Search",
	    "is_default": true,
	    "subscription_tier": "pro"
	  }],
	  "computer_config": []
	}`), "2026-07-25T20:00:00Z")
	if err != nil {
		t.Fatalf("parse runtime capabilities: %v", err)
	}
	if len(runtime.Capabilities) != 1 ||
		!runtime.Capabilities[0].Selected ||
		!runtime.Capabilities[0].Available ||
		runtime.Capabilities[0].FailureReason != "" {
		t.Fatalf("runtime=%+v", runtime)
	}
}

func TestRuntimeCapabilitiesAllowsHealthyUnknownCatalog(t *testing.T) {
	runtime := RuntimeCapabilities{
		SchemaVersion: RuntimeCapabilitiesSchemaVersion,
		State:         "unknown",
		CapturedAt:    "2026-07-25T20:00:00Z",
		Capabilities:  []ComposerCapability{},
		Source:        "headed-cdp-model-config-not-observed",
	}
	if err := runtime.Validate(); err != nil {
		t.Fatalf("unknown runtime should remain healthy: %v", err)
	}
}

func TestParseRuntimeCapabilitiesAcceptsCurrentConfigArray(t *testing.T) {
	runtime, err := parseRuntimeCapabilities([]byte(`{
	  "config": [{
	    "label": "Search",
	    "description": "Search the web",
	    "is_default": true,
	    "subscription_tier": "free",
	    "audience": null,
	    "has_new_tag": false
	  }],
	  "models": {},
	  "default_models": {}
	}`), "2026-07-25T20:00:00Z")
	if err != nil {
		t.Fatalf("parse current config array: %v", err)
	}
	if len(runtime.Capabilities) != 1 ||
		runtime.Capabilities[0].Kind != "search" ||
		runtime.Capabilities[0].ID != "search" {
		t.Fatalf("runtime=%+v", runtime)
	}
}

func TestFetchConversationDetailReturnsOnlyTerminalAskText(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/rest/thread/conversation-1" {
			t.Fatalf("path=%q", request.URL.Path)
		}
		if request.URL.Query().Get("with_parent_info") != "true" ||
			len(request.URL.Query()["supported_block_use_cases"]) == 0 {
			t.Fatalf("query=%q", request.URL.RawQuery)
		}
		return jsonResponse(http.StatusOK, `{
		  "entries": [
		    {"query_str":"Review the design"},
		    {
		      "uuid":"entry-1",
		      "query_str":"Review the design",
		      "status":"COMPLETED",
		      "step_type":"FINAL",
		      "mode":"search",
		      "search_mode":"web",
		      "blocks":[{
		        "intended_usage":"ask_text",
		        "markdown_block":{"answer":"Useful answer","progress":"DONE"}
		      }]
		    }
		  ]
		}`), nil
	})}
	data, failure := fetchConversationDetail(
		context.Background(),
		ReadConfig{HTTPClient: client},
		validTemplateForTest(),
		"conversation-1",
	)
	if failure != nil {
		t.Fatalf("failure=%+v", failure)
	}
	if data.CompletionState != "terminal" ||
		data.Text != "Useful answer" ||
		data.ReadMode != "observed_stable_http" {
		t.Fatalf("data=%+v", data)
	}
	if data.promptText != "Review the design" {
		t.Fatalf("memory-only prompt=%q", data.promptText)
	}
	if got, _ := data.Metadata["prompt_fingerprint"].(string); got != fingerprintPrompt("Review the design") {
		t.Fatalf("prompt fingerprint=%q", got)
	}
}

func TestFetchConversationDetailDoesNotPromoteNonFinalBlock(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
		  "entries": [{
		    "query_str":"Review",
		    "status":"STREAMING",
		    "step_type":"FINAL",
		    "blocks":[{
		      "intended_usage":"ask_text",
		      "markdown_block":{"answer":"Partial","progress":"IN_PROGRESS"}
		    }]
		  }]
		}`), nil
	})}
	data, failure := fetchConversationDetail(
		context.Background(),
		ReadConfig{HTTPClient: client},
		validTemplateForTest(),
		"conversation-1",
	)
	if failure != nil {
		t.Fatalf("failure=%+v", failure)
	}
	if data.CompletionState != "incomplete" || data.Text != "" {
		t.Fatalf("data=%+v", data)
	}
}

func TestReadFailureWithEmptyProviderDataStillValidates(t *testing.T) {
	result := readFailureResult(
		"run-1",
		"test",
		webagent.OperationConversationsDetail,
		readFailure{
			code:       "perplexity_http_failed",
			errClass:   "provider",
			message:    "candidate read failed",
			statusCode: http.StatusBadGateway,
		},
		ConversationDetailData{},
		nil,
	)
	if result.Evidence.ReadMode != "not_started" {
		t.Fatalf("read mode=%q", result.Evidence.ReadMode)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("validate result: %v", err)
	}
}

func TestPromptFingerprintNormalizesOnlyLineEndingsAndWhitespaceOnlyLines(t *testing.T) {
	left := "first\r\n \t\r\n  indented"
	right := "first\n\n  indented"
	if fingerprintPrompt(left) != fingerprintPrompt(right) {
		t.Fatal("blank-line whitespace and line endings should normalize")
	}
	if fingerprintPrompt("first\n indented") == fingerprintPrompt("first\n  indented") {
		t.Fatal("nonempty-line indentation must remain identity-bearing")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func validTemplateForTest() RequestTemplate {
	return RequestTemplate{
		SchemaVersion:    AuthTemplateSchemaVersion,
		Method:           http.MethodGet,
		URL:              Origin + ConversationListPath + "?version=2.18&source=default",
		Headers:          map[string]string{"accept": "application/json"},
		Cookies:          map[string]string{"__Secure-pplx.session": "private-cookie"},
		BrowserUserAgent: "test-agent",
		CapturedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Source:           "headed-cdp-observed-list-request",
	}
}
