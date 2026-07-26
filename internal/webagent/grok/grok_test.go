package grok

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestOperationResultNormalizesEmptyNextCommands(t *testing.T) {
	result := operationSuccess(
		"wa-0123456789abcdef0123456789abcdef",
		"test",
		webagent.OperationConversationsList,
		webagent.StateReady,
		webagent.StageObserveTerminal,
		"observed_stable_http",
		nil,
		webagent.CleanupEvidence{
			State: webagent.CleanupNotRequired,
		},
		nil,
		nil,
		map[string]any{"schema_version": ConversationListSchemaVersion},
		nil,
	)
	if result.NextCommands == nil {
		t.Fatal("next_commands must be an empty array, not null")
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("validated result: %v", err)
	}
}

func TestParseRuntimeCapabilitiesRequiresAvailableSelectedDefault(t *testing.T) {
	body := []byte(`{
	  "defaultModeId": "fast",
	  "modes": [
	    {
	      "id": "fast",
	      "title": "Fast",
	      "description": "Quick responses",
	      "availability": {"available": {}},
	      "tags": ["TAG_PRIMARY"]
	    },
	    {
	      "id": "expert",
	      "title": "Expert",
	      "availability": {
	        "requiresUpgrade": {"minimumSubscriptionTier": "TIER_SUPERGROK"}
	      }
	    }
	  ]
	}`)
	runtime, err := parseRuntimeCapabilities(
		body,
		"2026-07-26T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("parse runtime: %v", err)
	}
	if runtime.DefaultModeID != "fast" ||
		len(runtime.Modes) != 2 ||
		!runtime.Modes[0].Available ||
		!runtime.Modes[0].Selected ||
		runtime.Modes[1].Available ||
		runtime.Modes[1].FailureReason !=
			"requires_upgrade:TIER_SUPERGROK" {
		t.Fatalf("unexpected runtime: %+v", runtime)
	}

	_, err = parseRuntimeCapabilities(
		[]byte(`{
		  "defaultModeId": "expert",
		  "modes": [{
		    "id": "expert",
		    "title": "Expert",
		    "availability": {
		      "requiresUpgrade": {"minimumSubscriptionTier": "TIER_SUPERGROK"}
		    }
		  }]
		}`),
		"2026-07-26T00:00:00Z",
	)
	if err == nil {
		t.Fatal("unavailable default mode must not pass capability refresh")
	}
}

func TestFetchConversationDetailReturnsCanonicalStoredMessage(t *testing.T) {
	const (
		conversationID = "conversation-safe-id"
		prompt         = "Review the exact provider workflow."
		answer         = "## Highest risk\n\nA rendered false positive."
	)
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if got := request.Header.Get("User-Agent"); got != "test-browser" {
				t.Fatalf("user agent=%q", got)
			}
			cookie, err := request.Cookie("sso")
			if err != nil || cookie.Value != "private-cookie" {
				t.Fatalf("cookie=%v err=%v", cookie, err)
			}
			switch {
			case request.Method == http.MethodGet &&
				strings.HasSuffix(request.URL.Path, "/response-node"):
				return jsonResponse(http.StatusOK,
					`{"responseNodes":[{"responseId":"response-1"}]}`), nil
			case request.Method == http.MethodPost &&
				strings.HasSuffix(request.URL.Path, "/load-responses"):
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatalf("read request body: %v", err)
				}
				if string(body) != `{"responseIds":["response-1"]}` {
					t.Fatalf("load response body=%s", body)
				}
				return jsonResponse(http.StatusOK,
					`{"responses":[`+
						`{"sender":"human","message":`+strconv.Quote(prompt)+`},`+
						`{"sender":"assistant","message":`+strconv.Quote(answer)+`,`+
						`"model":"grok-test","partial":false,"streamErrors":[]}`+
						`]}`), nil
			default:
				t.Fatalf("unexpected request %s %s", request.Method, request.URL)
				return nil, nil
			}
		}),
	}
	data, failure := fetchConversationDetail(
		context.Background(),
		ReadConfig{HTTPClient: client},
		validTemplateForTest(),
		conversationID,
	)
	if failure != nil {
		t.Fatalf("detail failure: %+v", failure)
	}
	if data.CompletionState != "terminal" ||
		data.Text != answer ||
		data.promptText != prompt ||
		data.ReadMode != "observed_stable_http" ||
		data.Metadata["formatting"] != "provider_stored_message" ||
		data.Metadata["prompt_fingerprint"] != fingerprintPrompt(prompt) {
		t.Fatalf("unexpected detail: %+v", data)
	}
}

func TestPromptIdentityDiagnosticsClassifiesWhitespaceOnlyChanges(t *testing.T) {
	diagnostics := promptIdentityDiagnostics(
		"first line\n\n  indented value",
		"first line indented value",
	)
	if diagnostics["whitespace_equivalent"] != true ||
		diagnostics["blank_lines_equivalent"] != false ||
		diagnostics["line_endings_equivalent"] != false ||
		diagnostics["nonempty_lines_equivalent"] != false {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}
	if diagnostics["expected_lines"] != 3 ||
		diagnostics["observed_lines"] != 1 {
		t.Fatalf("unexpected line diagnostics: %+v", diagnostics)
	}
}

func TestPromptFingerprintNormalizesOnlyLineEndingsAndWhitespaceOnlyLines(
	t *testing.T,
) {
	expected := "first line\n\n  indented value\nlast line"
	providerStored := "first line\r\n \r\n  indented value\r\nlast line"
	if fingerprintPrompt(expected) != fingerprintPrompt(providerStored) {
		t.Fatal("provider blank-line whitespace must normalize")
	}
	if fingerprintPrompt(expected) ==
		fingerprintPrompt("first line\n\nindented value\nlast line") {
		t.Fatal("non-empty line indentation must remain identity-significant")
	}
}

func TestReadFailureWithEmptyProviderDataStillValidates(t *testing.T) {
	result := readFailureResult(
		"wa-0123456789abcdef0123456789abcdef",
		"test",
		webagent.OperationConversationsDetail,
		readFailure{
			code:     "grok_http_failed",
			errClass: "provider",
			message:  "Grok stored detail failed",
		},
		ConversationDetailData{},
		conversationRef("conversation-safe-id"),
	)
	if result.Evidence.ReadMode != "not_started" {
		t.Fatalf("read mode=%q", result.Evidence.ReadMode)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("validated failure result: %v", err)
	}
}

func TestFetchConversationDetailDoesNotPromotePartialAnswer(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if strings.HasSuffix(request.URL.Path, "/response-node") {
				return jsonResponse(
					http.StatusOK,
					`{"responseNodes":[{"responseId":"response-1"}]}`,
				), nil
			}
			return jsonResponse(
				http.StatusOK,
				`{"responses":[{"sender":"assistant","message":"still writing",`+
					`"partial":true,"streamErrors":[]}]}`,
			), nil
		}),
	}
	data, failure := fetchConversationDetail(
		context.Background(),
		ReadConfig{HTTPClient: client},
		validTemplateForTest(),
		"conversation-safe-id",
	)
	if failure != nil {
		t.Fatalf("detail failure: %+v", failure)
	}
	if data.CompletionState != "incomplete" || data.Text != "" {
		t.Fatalf("partial answer was promoted: %+v", data)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
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
		SchemaVersion: AuthTemplateSchemaVersion,
		Method:        http.MethodGet,
		URL: Origin +
			ConversationListPath +
			"?pageSize=60",
		Headers: map[string]string{
			"accept": "application/json",
		},
		Cookies: map[string]string{
			"sso": "private-cookie",
		},
		BrowserUserAgent: "test-browser",
		CapturedAt: time.Date(
			2026, time.July, 26, 0, 0, 0, 0, time.UTC,
		).Format(time.RFC3339Nano),
		Source: "headed-cdp-observed-list-request",
	}
}
