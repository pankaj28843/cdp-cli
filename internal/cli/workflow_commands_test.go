package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowA11yJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"workflow", "a11y", "https://example.test/app", "--wait", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow a11y exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Requests []struct {
			ID string `json:"id"`
		} `json:"requests"`
		Messages []struct {
			ID int `json:"id"`
		} `json:"messages"`
		Signals struct {
			ImagesWithoutAlt        int `json:"images_without_alt"`
			FormControlsWithoutName int `json:"form_controls_without_name"`
			HeadingSkips            int `json:"heading_skips"`
			FocusableWithoutLabel   int `json:"focusable_without_label"`
		} `json:"a11y"`
		Workflow struct {
			Name         string `json:"name"`
			IssueCount   int    `json:"issue_count"`
			RequestedURL string `json:"requested_url"`
			Partial      bool   `json:"partial"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow a11y output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "a11y" || got.Workflow.RequestedURL != "https://example.test/app" {
		t.Fatalf("workflow a11y = %+v, want complete workflow output", got)
	}
	if len(got.Requests) != 1 {
		t.Fatalf("workflow a11y requests = %+v, want one failed request", got.Requests)
	}
	if len(got.Messages) == 0 {
		t.Fatalf("workflow a11y messages = %+v, want at least one issue message", got.Messages)
	}
	if got.Workflow.Partial {
		t.Fatalf("workflow a11y = %+v, want no collector errors for synthetic page", got)
	}
	if got.Signals.ImagesWithoutAlt < 0 || got.Signals.FormControlsWithoutName < 0 || got.Signals.HeadingSkips < 0 || got.Signals.FocusableWithoutLabel < 0 {
		t.Fatalf("workflow a11y signals = %+v", got.Signals)
	}
	if got.Workflow.IssueCount != got.Signals.ImagesWithoutAlt+got.Signals.FormControlsWithoutName+got.Signals.HeadingSkips+got.Signals.FocusableWithoutLabel {
		t.Fatalf("workflow a11y summary = %+v, want issue_count to match signal sum", got)
	}
}

func TestWorkflowWebResearchSERPHelp(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--help"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp --help exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	for _, want := range []string{
		"query<TAB>google-tbs-time-filter",
		"Blank lines and lines whose first non-space",
		"character is # are ignored",
		"applies it only to Google; other engines",
		"cdr:1,cd_min:07/01/2026,cd_max:07/01/2026",
		"--navigation-delay",
		"minimum delay between navigation starts in each SERP engine lane",
		"no delay before the first navigation",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("workflow web-research serp --help missing %q:\n%s", want, out.String())
		}
	}
}

func TestDescribeWorkflowWebResearchSERP(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"describe", "--command", "workflow web-research serp", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe workflow web-research serp exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK       bool `json:"ok"`
		Commands struct {
			Examples []string `json:"examples"`
			Flags    []struct {
				Name  string `json:"name"`
				Usage string `json:"usage"`
			} `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("describe workflow web-research serp output is invalid JSON: %v", err)
	}
	if !got.OK {
		t.Fatalf("describe workflow web-research serp output indicates failure: %s", out.String())
	}
	var queryFileUsage string
	var navigationDelayUsage string
	for _, flag := range got.Commands.Flags {
		if flag.Name == "query-file" {
			queryFileUsage = flag.Usage
		}
		if flag.Name == "navigation-delay" {
			navigationDelayUsage = flag.Usage
		}
	}
	for _, want := range []string{"query<TAB>Google tbs time filter", "# comment rows ignored"} {
		if !strings.Contains(queryFileUsage, want) {
			t.Fatalf("describe query-file usage %q missing %q", queryFileUsage, want)
		}
	}
	for _, want := range []string{"printf '%s\\t%s\\n'", "cdr:1,cd_min:07/01/2026,cd_max:07/01/2026", "--browser-mode headed", "--serp google"} {
		if !hasExampleContaining(got.Commands.Examples, want) {
			t.Fatalf("describe examples %#v missing %q", got.Commands.Examples, want)
		}
	}
	for _, want := range []string{"minimum delay", "engine lane", "first navigation"} {
		if !strings.Contains(navigationDelayUsage, want) {
			t.Fatalf("describe navigation-delay usage %q missing %q", navigationDelayUsage, want)
		}
	}
	if !hasExampleContaining(got.Commands.Examples, "--navigation-delay 30s") {
		t.Fatalf("describe examples %#v missing progressive pacing example", got.Commands.Examples)
	}
}

func TestWorkflowWebResearchSERPRejectsMalformedQueryBeforeBrowser(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("CDP_STATE_DIR", stateDir)
	queryFile := filepath.Join(stateDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("valid query\n\tcdr:1,cd_min:07/01/2026,cd_max:07/01/2026\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("malformed query exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		Class               string   `json:"err_class"`
		Message             string   `json:"message"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("malformed query output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || got.Class != "usage" || !strings.Contains(got.Message, "invalid query file line 2") || !strings.Contains(got.Message, "query column must not be empty") {
		t.Fatalf("malformed query error = %+v", got)
	}
	if len(got.RemediationCommands) != 1 || !strings.Contains(got.RemediationCommands[0], "query<TAB>") && !strings.Contains(got.RemediationCommands[0], "printf '%s\\t%s\\n'") {
		t.Fatalf("malformed query remediation = %#v", got.RemediationCommands)
	}
}

func TestWorkflowSubmitSearchFillEnterWaitURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "submit-search", "Search", "typed value", "--by", "label", "--wait-url-contains", "results", "--poll", "100ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow submit-search exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Target           struct {
			ID    string `json:"id"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"target"`
		BeforeTarget struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"before_target"`
		FinalTarget struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"final_target"`
		PageState struct {
			SameTarget bool `json:"same_target"`
			URLChanged bool `json:"url_changed"`
		} `json:"page_state"`
		Input struct {
			Mode     string `json:"mode"`
			Selector string `json:"selector"`
			Query    string `json:"query"`
		} `json:"input"`
		Workflow struct {
			Name          string `json:"name"`
			InputMode     string `json:"input_mode"`
			Submit        string `json:"submit"`
			SubmitKey     string `json:"submit_key"`
			WaitRequested bool   `json:"wait_requested"`
			Verified      bool   `json:"verified"`
			PollInterval  string `json:"poll_interval"`
		} `json:"workflow"`
		Fill struct {
			Selector string `json:"selector"`
			URL      string `json:"url"`
			Title    string `json:"title"`
			Filled   bool   `json:"filled"`
			Verified *bool  `json:"verified"`
			Value    string `json:"value"`
		} `json:"fill"`
		Press struct {
			Selector   string `json:"selector"`
			URL        string `json:"url"`
			Title      string `json:"title"`
			Key        string `json:"key"`
			Dispatched bool   `json:"dispatched"`
			Verified   *bool  `json:"verified"`
		} `json:"press"`
		Verification struct {
			Kind         string `json:"kind"`
			Needle       string `json:"needle"`
			Condition    string `json:"condition"`
			URL          string `json:"url"`
			Title        string `json:"title"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
		Locator struct {
			Strict bool `json:"strict"`
		} `json:"locator"`
		Actionability struct {
			Actionable bool `json:"actionable"`
		} `json:"actionability"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow submit-search output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "submit_search" || got.ResolvedSelector != "input#q" || got.Workflow.Name != "submit-search" || got.Workflow.InputMode != "fill" || got.Workflow.Submit != "enter" || got.Workflow.SubmitKey != "Enter" || !got.Workflow.WaitRequested || !got.Workflow.Verified || got.Workflow.PollInterval != "100ms" {
		t.Fatalf("workflow submit-search metadata = %+v, want verified fill/enter workflow", got)
	}
	if got.Input.Mode != "fill" || got.Input.Selector != "input#q" || got.Input.Query != "typed value" || !got.Fill.Filled || got.Fill.Value != "typed value" || got.Fill.Verified == nil || !*got.Fill.Verified {
		t.Fatalf("workflow submit-search fill = %+v, input=%+v", got.Fill, got.Input)
	}
	if got.Press.Selector != "input#q" || got.Press.Key != "Enter" || !got.Press.Dispatched || got.Press.Verified == nil || !*got.Press.Verified {
		t.Fatalf("workflow submit-search press = %+v, want dispatched Enter", got.Press)
	}
	if got.Verification.Kind != "url" || got.Verification.Needle != "results" || got.Verification.Condition != "contains" || !strings.Contains(got.Verification.URL, "results") || got.Verification.Title != "Example App" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "100ms" {
		t.Fatalf("workflow submit-search verification = %+v, want matched URL wait", got.Verification)
	}
	if got.Target.ID != got.BeforeTarget.ID || got.Target.ID != got.FinalTarget.ID || !got.PageState.SameTarget || !got.PageState.URLChanged || got.Target.URL != got.Verification.URL || got.FinalTarget.URL != got.Verification.URL || got.Fill.URL != got.Verification.URL || got.Press.URL != got.Verification.URL {
		t.Fatalf("workflow submit-search target/result = %+v, want same target with final URL evidence", got)
	}
	hasSnapshotCommand := false
	for _, next := range got.NextCommands {
		if strings.Contains(next, "snapshot") {
			hasSnapshotCommand = true
			break
		}
	}
	if !got.Locator.Strict || !got.Actionability.Actionable || !hasSnapshotCommand {
		t.Fatalf("workflow submit-search support fields = locator=%+v actionability=%+v next=%+v", got.Locator, got.Actionability, got.NextCommands)
	}
}

func TestWorkflowSubmitSearchSuggestionSelectionJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "submit-search", "Search", "typed value",
		"--by", "label",
		"--suggestion", "Checkout",
		"--suggestion-by", "text",
		"--submit", "none",
		"--wait-url-contains", "results",
		"--poll", "100ms",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow submit-search suggestion exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Workflow         struct {
			Name                string `json:"name"`
			InputMode           string `json:"input_mode"`
			Submit              string `json:"submit"`
			SuggestionRequested bool   `json:"suggestion_requested"`
			SuggestionSelected  bool   `json:"suggestion_selected"`
			SuggestionStrategy  string `json:"suggestion_strategy"`
			Verified            bool   `json:"verified"`
		} `json:"workflow"`
		Fill struct {
			Selector string `json:"selector"`
			Filled   bool   `json:"filled"`
			Verified *bool  `json:"verified"`
		} `json:"fill"`
		Suggestion struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Count   int    `json:"count"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Role         string `json:"role"`
				Name         string `json:"name"`
			} `json:"matches"`
		} `json:"suggestion"`
		SuggestionSelector string `json:"suggestion_selector"`
		SuggestionClick    struct {
			Selector string `json:"selector"`
			Clicked  bool   `json:"clicked"`
			Verified *bool  `json:"verified"`
			URL      string `json:"url"`
		} `json:"suggestion_click"`
		SuggestionActionability struct {
			Actionable bool `json:"actionable"`
		} `json:"suggestion_actionability"`
		Verification struct {
			Kind    string `json:"kind"`
			Needle  string `json:"needle"`
			URL     string `json:"url"`
			Matched bool   `json:"matched"`
		} `json:"verification"`
		FinalTarget struct {
			URL string `json:"url"`
		} `json:"final_target"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow submit-search suggestion output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "submit_search" || got.ResolvedSelector != "input#q" || got.Workflow.Name != "submit-search" || got.Workflow.InputMode != "fill" || got.Workflow.Submit != "none" || !got.Workflow.SuggestionRequested || !got.Workflow.SuggestionSelected || got.Workflow.SuggestionStrategy != "auto" || !got.Workflow.Verified {
		t.Fatalf("workflow submit-search suggestion metadata = %+v, want selected suggestion workflow", got.Workflow)
	}
	if got.Suggestion.By != "text" || got.Suggestion.Query != "Checkout" || got.Suggestion.Count != 1 || !got.Suggestion.Strict || len(got.Suggestion.Matches) != 1 || got.Suggestion.Matches[0].SelectorHint != "button#checkout" || got.SuggestionSelector != "button#checkout" {
		t.Fatalf("workflow submit-search suggestion = %+v selector=%q, want strict checkout candidate", got.Suggestion, got.SuggestionSelector)
	}
	if !got.Fill.Filled || got.Fill.Verified == nil || !*got.Fill.Verified || !got.SuggestionActionability.Actionable || !got.SuggestionClick.Clicked || got.SuggestionClick.Selector != "button#checkout" || got.SuggestionClick.Verified == nil || !*got.SuggestionClick.Verified {
		t.Fatalf("workflow submit-search suggestion actions = fill=%+v actionability=%+v click=%+v", got.Fill, got.SuggestionActionability, got.SuggestionClick)
	}
	if got.Verification.Kind != "url" || got.Verification.Needle != "results" || !got.Verification.Matched || !strings.Contains(got.Verification.URL, "results") || got.FinalTarget.URL != got.Verification.URL || got.SuggestionClick.URL != got.Verification.URL {
		t.Fatalf("workflow submit-search suggestion verification = %+v final=%+v click=%+v", got.Verification, got.FinalTarget, got.SuggestionClick)
	}
}

func TestWorkflowSubmitSearchWaitResponseJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "network-page", "type": "page", "title": "Network App", "url": "https://example.test/network", "attached": false, "networkOnClick": true, "networkURL": "https://example.test/api/suggest?token=abc", "networkMethod": "POST", "networkResourceType": "Fetch", "networkStatus": 202, "networkStatusText": "Accepted", "networkMimeType": "application/json"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "submit-search", "Search", "typed value",
		"--target", "network-page",
		"--by", "label",
		"--suggestion", "Checkout",
		"--suggestion-by", "text",
		"--submit", "none",
		"--wait-response",
		"--wait-response-match-url", "/api/suggest",
		"--wait-response-method", "POST",
		"--wait-response-resource-type", "Fetch",
		"--wait-response-status", "202",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow submit-search wait response exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool   `json:"ok"`
		Action   string `json:"action"`
		Workflow struct {
			Name                string `json:"name"`
			SuggestionRequested bool   `json:"suggestion_requested"`
			SuggestionSelected  bool   `json:"suggestion_selected"`
			Submit              string `json:"submit"`
			WaitRequested       bool   `json:"wait_requested"`
			Verified            bool   `json:"verified"`
		} `json:"workflow"`
		Fill struct {
			Filled   bool  `json:"filled"`
			Verified *bool `json:"verified"`
		} `json:"fill"`
		SuggestionClick struct {
			Clicked  bool  `json:"clicked"`
			Verified *bool `json:"verified"`
		} `json:"suggestion_click"`
		ResponseWait struct {
			Kind          string `json:"kind"`
			Matched       bool   `json:"matched"`
			CDPMethod     string `json:"cdp_method"`
			EventCount    int    `json:"event_count"`
			ObservedCount int    `json:"observed_count"`
			Criteria      struct {
				URLContains  string `json:"url_contains"`
				Method       string `json:"method"`
				ResourceType string `json:"resource_type"`
				Status       int    `json:"status"`
			} `json:"criteria"`
			Evidence struct {
				Headers bool `json:"headers"`
				Bodies  bool `json:"bodies"`
				Bounded bool `json:"bounded"`
			} `json:"evidence"`
		} `json:"response_wait"`
		Response struct {
			Kind         string `json:"kind"`
			CDPMethod    string `json:"cdp_method"`
			RequestID    string `json:"request_id"`
			URL          string `json:"url"`
			Method       string `json:"method"`
			ResourceType string `json:"resource_type"`
			Status       int    `json:"status"`
			StatusText   string `json:"status_text"`
			MimeType     string `json:"mime_type"`
		} `json:"response"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow submit-search wait response output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "submit_search" || got.Workflow.Name != "submit-search" || !got.Workflow.SuggestionRequested || !got.Workflow.SuggestionSelected || got.Workflow.Submit != "none" || !got.Workflow.WaitRequested || !got.Workflow.Verified {
		t.Fatalf("workflow submit-search wait response metadata = %+v", got.Workflow)
	}
	if !got.Fill.Filled || got.Fill.Verified == nil || !*got.Fill.Verified || !got.SuggestionClick.Clicked || got.SuggestionClick.Verified == nil || !*got.SuggestionClick.Verified {
		t.Fatalf("workflow submit-search wait response actions = fill=%+v suggestion_click=%+v", got.Fill, got.SuggestionClick)
	}
	if got.ResponseWait.Kind != "response" || !got.ResponseWait.Matched || got.ResponseWait.CDPMethod != "Network.responseReceived" || got.ResponseWait.EventCount == 0 || got.ResponseWait.ObservedCount < 1 || got.ResponseWait.Criteria.URLContains != "/api/suggest" || got.ResponseWait.Criteria.Method != "POST" || got.ResponseWait.Criteria.ResourceType != "Fetch" || got.ResponseWait.Criteria.Status != 202 || got.ResponseWait.Evidence.Headers || got.ResponseWait.Evidence.Bodies || !got.ResponseWait.Evidence.Bounded {
		t.Fatalf("workflow submit-search wait response wait = %+v, want matched bounded response evidence", got.ResponseWait)
	}
	if got.Response.Kind != "response" || got.Response.CDPMethod != "Network.responseReceived" || got.Response.RequestID != "click-request-1" || got.Response.Method != "POST" || got.Response.ResourceType != "Fetch" || got.Response.Status != 202 || got.Response.StatusText != "Accepted" || got.Response.MimeType != "application/json" || strings.Contains(got.Response.URL, "token=abc") {
		t.Fatalf("workflow submit-search wait response event = %+v, want redacted response event", got.Response)
	}
}

func TestWorkflowSubmitSearchSuggestionNotFoundJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "submit-search", "Search", "typed value",
		"--by", "label",
		"--suggestion", "Gone",
		"--suggestion-by", "text",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("workflow submit-search missing suggestion exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action   string `json:"action"`
			Workflow struct {
				Name                string `json:"name"`
				SuggestionRequested bool   `json:"suggestion_requested"`
				SuggestionSelected  bool   `json:"suggestion_selected"`
				Verified            bool   `json:"verified"`
			} `json:"workflow"`
			Fill struct {
				Filled bool   `json:"filled"`
				Value  string `json:"value"`
			} `json:"fill"`
			Suggestion struct {
				By      string `json:"by"`
				Query   string `json:"query"`
				Count   int    `json:"count"`
				Strict  bool   `json:"strict"`
				Matches []any  `json:"matches"`
			} `json:"suggestion"`
			NextCommands []string `json:"next_commands"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow submit-search missing suggestion output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "suggestion_not_found" || got.Data.Action != "suggestion_not_found" || got.Data.Workflow.Name != "submit-search" || !got.Data.Workflow.SuggestionRequested || got.Data.Workflow.SuggestionSelected || got.Data.Workflow.Verified || !got.Data.Fill.Filled || got.Data.Fill.Value != "typed value" || got.Data.Suggestion.By != "text" || got.Data.Suggestion.Query != "Gone" || got.Data.Suggestion.Count != 0 || got.Data.Suggestion.Strict || len(got.Data.Suggestion.Matches) != 0 || !containsSubstring(got.Data.NextCommands, "snapshot") {
		t.Fatalf("workflow submit-search missing suggestion = %+v, want not-found diagnostic with candidates", got)
	}
}

func TestWorkflowSubmitSearchWaitLoadStateJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "submit-search", "Search", "typed value",
		"--by", "label",
		"--submit", "none",
		"--wait-load-state", "domcontentloaded",
		"--poll", "100ms",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow submit-search wait load-state exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Workflow struct {
			Name          string `json:"name"`
			WaitRequested bool   `json:"wait_requested"`
			Verified      bool   `json:"verified"`
			PollInterval  string `json:"poll_interval"`
		} `json:"workflow"`
		Fill struct {
			Filled   bool  `json:"filled"`
			Verified *bool `json:"verified"`
		} `json:"fill"`
		Verification struct {
			Kind         string         `json:"kind"`
			State        string         `json:"state"`
			ReadyState   string         `json:"ready_state"`
			Condition    string         `json:"condition"`
			Matched      bool           `json:"matched"`
			URL          string         `json:"url"`
			Title        string         `json:"title"`
			PollInterval string         `json:"poll_interval"`
			Evidence     map[string]any `json:"evidence"`
		} `json:"verification"`
		FinalTarget struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"final_target"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow submit-search wait load-state output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "submit-search" || !got.Workflow.WaitRequested || !got.Workflow.Verified || got.Workflow.PollInterval != "100ms" || !got.Fill.Filled || got.Fill.Verified == nil || !*got.Fill.Verified {
		t.Fatalf("workflow submit-search wait load-state metadata = %+v fill=%+v", got.Workflow, got.Fill)
	}
	if got.Verification.Kind != "load-state" || got.Verification.State != "domcontentloaded" || got.Verification.ReadyState != "complete" || !got.Verification.Matched || got.Verification.URL != "https://example.test/app" || got.Verification.Title != "Example App" || !strings.Contains(got.Verification.Condition, "interactive or complete") || got.Verification.Evidence["ready_state"] != "complete" || got.Verification.PollInterval != "100ms" {
		t.Fatalf("workflow submit-search load-state verification = %+v, want matched load-state evidence", got.Verification)
	}
	if got.FinalTarget.URL != got.Verification.URL || got.FinalTarget.Title != got.Verification.Title {
		t.Fatalf("workflow submit-search load-state target = %+v, want final load-state URL/title", got.FinalTarget)
	}
}

func TestWorkflowActionCaptureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	dir := t.TempDir()
	outPath := filepath.Join(dir, "action.local.json")
	evidenceDir := filepath.Join(dir, "evidence")
	beforePath := filepath.Join(dir, "before.png")
	afterPath := filepath.Join(dir, "after.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "action-capture",
		"--action", "insert-text:hello",
		"--selector", "[contenteditable=true]",
		"--wait-before", "0s",
		"--wait-after", "0s",
		"--include", "network,websocket,console,dom,text,a11y,screenshot,storage-diff",
		"--a11y-depth", "4",
		"--a11y-limit", "10",
		"--before-screenshot", beforePath,
		"--after-screenshot", afterPath,
		"--screenshot-full-page",
		"--evidence-out-dir", evidenceDir,
		"--out", outPath,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow action-capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Workflow struct {
			Name    string   `json:"name"`
			Include []string `json:"include"`
		} `json:"workflow"`
		Action struct {
			Type   string `json:"type"`
			Result struct {
				Strategy string `json:"strategy"`
				Value    string `json:"value"`
			} `json:"result"`
		} `json:"action"`
		Requests []struct {
			Body *struct {
				Text string `json:"text"`
			} `json:"body,omitempty"`
		} `json:"requests"`
		WebSockets  []map[string]any `json:"websockets"`
		Messages    []map[string]any `json:"messages"`
		StorageDiff struct {
			HasDiff bool `json:"has_diff"`
		} `json:"storage_diff"`
		Evidence struct {
			ArtifactCount int `json:"artifact_count"`
			Before        struct {
				Screenshot struct {
					Bytes    int  `json:"bytes"`
					FullPage bool `json:"full_page"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"screenshot"`
				Text struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"text"`
				DOM struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"dom"`
				A11y struct {
					Count     int  `json:"count"`
					Truncated bool `json:"truncated"`
					Artifact  struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"a11y"`
			} `json:"before"`
			After struct {
				Screenshot struct {
					Bytes    int  `json:"bytes"`
					FullPage bool `json:"full_page"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"screenshot"`
				Text struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"text"`
				DOM struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"dom"`
				A11y struct {
					Count     int  `json:"count"`
					Truncated bool `json:"truncated"`
					Artifact  struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"a11y"`
			} `json:"after"`
			Events struct {
				Network struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"network"`
				WebSockets struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"websockets"`
				Console struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"console"`
			} `json:"events"`
			Manifest struct {
				ReferencedArtifactCount int `json:"referenced_artifact_count"`
				CollectorErrorCount     int `json:"collector_error_count"`
				Artifact                struct {
					Type string `json:"type"`
					Path string `json:"path"`
				} `json:"artifact"`
			} `json:"manifest"`
		} `json:"evidence"`
		Artifacts []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"artifacts"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow action-capture output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "action-capture" || got.Action.Type != "insert-text" || got.Action.Result.Strategy != "insert-text" || got.Action.Result.Value != "beforehello" {
		t.Fatalf("workflow action-capture = %+v, want insert-text action result", got)
	}
	if len(got.Requests) == 0 || len(got.WebSockets) == 0 || len(got.Messages) == 0 || got.Artifact.Path != outPath {
		t.Fatalf("workflow action-capture collectors = %+v, want network, websocket, console, and artifact", got)
	}
	for _, request := range got.Requests {
		if request.Body != nil {
			t.Fatalf("workflow action-capture default request body = %+v, want bodies omitted unless explicitly enabled", request.Body)
		}
	}
	wantEvidence := map[string]string{
		"workflow-action-capture-before-screenshot": filepath.Join(evidenceDir, "action-capture.before.screenshot.png"),
		"workflow-action-capture-before-text":       filepath.Join(evidenceDir, "action-capture.before.text.json"),
		"workflow-action-capture-before-dom":        filepath.Join(evidenceDir, "action-capture.before.dom.json"),
		"workflow-action-capture-before-a11y":       filepath.Join(evidenceDir, "action-capture.before.a11y.json"),
		"workflow-action-capture-after-screenshot":  filepath.Join(evidenceDir, "action-capture.after.screenshot.png"),
		"workflow-action-capture-after-text":        filepath.Join(evidenceDir, "action-capture.after.text.json"),
		"workflow-action-capture-after-dom":         filepath.Join(evidenceDir, "action-capture.after.dom.json"),
		"workflow-action-capture-after-a11y":        filepath.Join(evidenceDir, "action-capture.after.a11y.json"),
		"workflow-action-capture-action-network":    filepath.Join(evidenceDir, "action-capture.action.network.json"),
		"workflow-action-capture-action-websockets": filepath.Join(evidenceDir, "action-capture.action.websockets.json"),
		"workflow-action-capture-action-console":    filepath.Join(evidenceDir, "action-capture.action.console.json"),
		"workflow-action-capture-manifest":          filepath.Join(evidenceDir, "action-capture.manifest.json"),
	}
	if got.Evidence.ArtifactCount != len(wantEvidence) ||
		got.Evidence.Before.Screenshot.Bytes == 0 ||
		!got.Evidence.Before.Screenshot.FullPage ||
		got.Evidence.Before.Text.Count == 0 ||
		got.Evidence.Before.DOM.Count == 0 ||
		got.Evidence.Before.A11y.Count == 0 ||
		got.Evidence.After.Screenshot.Bytes == 0 ||
		!got.Evidence.After.Screenshot.FullPage ||
		got.Evidence.After.Text.Count == 0 ||
		got.Evidence.After.DOM.Count == 0 ||
		got.Evidence.After.A11y.Count == 0 ||
		got.Evidence.Events.Network.Count == 0 ||
		got.Evidence.Events.WebSockets.Count == 0 ||
		got.Evidence.Events.Console.Count == 0 ||
		got.Evidence.Manifest.ReferencedArtifactCount != len(wantEvidence)+1 {
		t.Fatalf("workflow action-capture evidence = %+v, want before/after and event evidence", got.Evidence)
	}
	if got.Evidence.Before.Screenshot.Artifact.Path != wantEvidence["workflow-action-capture-before-screenshot"] ||
		got.Evidence.Before.Text.Artifact.Path != wantEvidence["workflow-action-capture-before-text"] ||
		got.Evidence.After.Screenshot.Artifact.Path != wantEvidence["workflow-action-capture-after-screenshot"] ||
		got.Evidence.After.DOM.Artifact.Path != wantEvidence["workflow-action-capture-after-dom"] ||
		got.Evidence.Before.A11y.Artifact.Path != wantEvidence["workflow-action-capture-before-a11y"] ||
		got.Evidence.After.A11y.Artifact.Path != wantEvidence["workflow-action-capture-after-a11y"] ||
		got.Evidence.Events.Network.Artifact.Path != wantEvidence["workflow-action-capture-action-network"] ||
		got.Evidence.Events.Console.Artifact.Path != wantEvidence["workflow-action-capture-action-console"] ||
		got.Evidence.Manifest.Artifact.Path != wantEvidence["workflow-action-capture-manifest"] {
		t.Fatalf("workflow action-capture evidence paths = %+v, want stable before/after artifact paths", got.Evidence)
	}
	seenEvidence := map[string]string{}
	for _, artifact := range got.Artifacts {
		if _, ok := wantEvidence[artifact.Type]; ok {
			seenEvidence[artifact.Type] = artifact.Path
		}
	}
	for typ, path := range wantEvidence {
		if seenEvidence[typ] != path {
			t.Fatalf("workflow action-capture artifacts = %+v, missing %s at %s", got.Artifacts, typ, path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("evidence artifact %s was not written: %v", path, err)
		}
		if strings.Contains(typ, "screenshot") {
			if !bytes.Contains(raw, []byte("synthetic screenshot")) {
				t.Fatalf("screenshot evidence artifact %s = %q, want screenshot bytes", path, string(raw))
			}
			continue
		}
		if typ == "workflow-action-capture-manifest" {
			if !bytes.Contains(raw, []byte(`"workflow"`)) ||
				!bytes.Contains(raw, []byte(`"evidence"`)) ||
				!bytes.Contains(raw, []byte(`"artifacts"`)) ||
				bytes.Contains(raw, []byte(`"hello"`)) {
				t.Fatalf("manifest artifact %s = %s, want manifest metadata without typed text payload", path, string(raw))
			}
			continue
		}
		if !bytes.Contains(raw, []byte(`"phase"`)) || !bytes.Contains(raw, []byte(`"collector"`)) {
			t.Fatalf("evidence artifact %s = %s, want phase and collector metadata", path, string(raw))
		}
	}
	if _, err := os.Stat(beforePath); err != nil {
		t.Fatalf("before screenshot was not written: %v", err)
	}
	if _, err := os.Stat(afterPath); err != nil {
		t.Fatalf("after screenshot was not written: %v", err)
	}
}

func TestWorkflowActionCaptureIncludesSelectedBoundedResponseBodies(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "action-capture",
		"--action", "press:Enter",
		"--selector", "body",
		"--wait-before", "0s",
		"--wait-after", "0s",
		"--include", "network",
		"--include-bodies", "json",
		"--body-limit", "8",
		"--body-url-contains", "/app",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow action-capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		Workflow struct {
			IncludeBodies []string `json:"include_bodies"`
			BodyLimit     int      `json:"body_limit"`
			BodyURL       string   `json:"body_url_contains"`
		} `json:"workflow"`
		Requests []struct {
			ID   string `json:"id"`
			Body *struct {
				Text      string `json:"text"`
				Bytes     int    `json:"bytes"`
				Truncated bool   `json:"truncated"`
			} `json:"body,omitempty"`
		} `json:"requests"`
		LocalCaptureWarning string `json:"local_capture_warning"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow action-capture output is invalid JSON: %v", err)
	}
	if len(got.Workflow.IncludeBodies) != 1 || got.Workflow.IncludeBodies[0] != "json" || got.Workflow.BodyLimit != 8 || got.Workflow.BodyURL != "/app" {
		t.Fatalf("workflow action-capture body options = %+v, want json bodies bounded at 8 bytes", got.Workflow)
	}
	var bodyText string
	var bodyBytes int
	var truncated bool
	for _, request := range got.Requests {
		if request.ID == "request-ok" && request.Body != nil {
			bodyText = request.Body.Text
			bodyBytes = request.Body.Bytes
			truncated = request.Body.Truncated
		}
	}
	if bodyText != `{"ok":tr` || bodyBytes <= 8 || !truncated {
		t.Fatalf("workflow action-capture response body = text %q bytes %d truncated %t, want bounded JSON body", bodyText, bodyBytes, truncated)
	}
	if !strings.Contains(got.LocalCaptureWarning, "response bodies") || !strings.Contains(got.LocalCaptureWarning, "local") {
		t.Fatalf("workflow action-capture local warning = %q, want response-body privacy warning", got.LocalCaptureWarning)
	}
}

func TestWorkflowActionCaptureBodyURLFilterOmitsNonMatchingBodies(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "action-capture",
		"--action", "press:Enter",
		"--selector", "body",
		"--wait-before", "0s",
		"--wait-after", "0s",
		"--include", "network",
		"--include-bodies", "json",
		"--body-url-contains", "/only-this-endpoint",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow action-capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		Requests []struct {
			Body *struct{} `json:"body,omitempty"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow action-capture output is invalid JSON: %v", err)
	}
	for _, request := range got.Requests {
		if request.Body != nil {
			t.Fatalf("workflow action-capture filtered request body = %+v, want bodies omitted for URL mismatch", request.Body)
		}
	}
}

func TestWorkflowActionCaptureRejectsInvalidBodyOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "kind", args: []string{"--include-bodies", "video"}, want: "--include-bodies"},
		{name: "limit", args: []string{"--body-limit", "0"}, want: "--body-limit"},
		{name: "without network", args: []string{"--include", "console", "--include-bodies", "json"}, want: "requires --include network"},
		{name: "url without bodies", args: []string{"--body-url-contains", "/api"}, want: "requires --include-bodies"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"workflow", "action-capture", "--action", "press:Enter", "--selector", "body", "--json"}, tt.args...)
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("workflow action-capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Fatalf("workflow action-capture error = %s, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestWorkflowActionCaptureA11yRequiresEvidenceOutDir(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "action-capture",
		"--action", "press:Enter",
		"--selector", "body",
		"--include", "a11y",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("workflow action-capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow action-capture usage output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || !strings.Contains(got.Message, "--evidence-out-dir") {
		t.Fatalf("workflow action-capture usage = %+v, want evidence-out-dir usage error", got)
	}
}

func TestWorkflowActionCaptureScreenshotRequiresEvidenceOutDir(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "action-capture",
		"--action", "press:Enter",
		"--selector", "body",
		"--include", "screenshot",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("workflow action-capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow action-capture usage output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || !strings.Contains(got.Message, "--include screenshot requires --evidence-out-dir") {
		t.Fatalf("workflow action-capture usage = %+v, want screenshot evidence-out-dir usage error", got)
	}
}

func TestWorkflowConsoleErrorsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "console-errors", "--wait", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow console-errors exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Workflow struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"workflow"`
		Messages []struct {
			Type       string          `json:"type"`
			Level      string          `json:"level"`
			Text       string          `json:"text"`
			Exception  json.RawMessage `json:"exception"`
			StackTrace json.RawMessage `json:"stack_trace"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow console-errors output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "console-errors" || got.Workflow.Count != 3 || got.Messages[0].Level != "error" {
		t.Fatalf("workflow console-errors = %+v, want error summary", got)
	}
	if got.Messages[1].Type != "exception" || !strings.Contains(got.Messages[1].Text, "failed to fetch dashboard") || len(got.Messages[1].Exception) == 0 || len(got.Messages[1].StackTrace) == 0 {
		t.Fatalf("workflow console exception = %+v, want reason, exception, and stack", got.Messages[1])
	}
}

func TestWorkflowNetworkFailuresJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "network-failures", "--wait", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow network-failures exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Workflow struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"workflow"`
		Requests []struct {
			ID     string `json:"id"`
			Failed bool   `json:"failed"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow network-failures output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "network-failures" || got.Workflow.Count != 1 || got.Requests[0].ID != "request-failed" {
		t.Fatalf("workflow network-failures = %+v, want failed request summary", got)
	}
}

func TestWorkflowDebugBundleJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "debug-bundle", "--url", "https://example.test/app?token=abc&client_id=public", "--since", "250ms", "--out-dir", outDir, "--run-id", "run-1", "--task-id", "task-1", "--stage", "preflight", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow debug-bundle exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	if strings.Contains(out.String(), "token=abc") || strings.Contains(out.String(), "Synthetic console error") {
		t.Fatalf("workflow debug-bundle stdout contains raw browser payload: %s", out.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"target"`
		Evidence struct {
			Requests      int `json:"requests"`
			Messages      int `json:"messages"`
			SnapshotItems int `json:"snapshot_items"`
		} `json:"evidence"`
		Artifacts []struct {
			Type    string                   `json:"type"`
			Path    string                   `json:"path"`
			Content string                   `json:"content"`
			Safety  artifacts.SafetyMetadata `json:"safety"`
		} `json:"artifacts"`
		Artifact struct {
			Path   string                   `json:"path"`
			Safety artifacts.SafetyMetadata `json:"safety"`
		} `json:"artifact"`
		Bundle struct {
			SchemaVersion        string `json:"schema_version"`
			RedactionMode        string `json:"redaction_mode"`
			DefaultJSON          string `json:"default_json"`
			InlinePayloads       bool   `json:"inline_payloads"`
			ArtifactCount        int    `json:"artifact_count"`
			PublicSafeArtifacts  int    `json:"public_safe_artifacts"`
			LocalOnlyArtifacts   int    `json:"local_only_artifacts"`
			UnsafeOptInArtifacts int    `json:"unsafe_opt_in_artifacts"`
			Layout               struct {
				Manifest   string `json:"manifest"`
				CommandLog string `json:"command_log"`
				StageLog   string `json:"stage_log"`
			} `json:"layout"`
			Commands []struct {
				Name         string   `json:"name"`
				BrowserMode  string   `json:"browser_mode"`
				Timeout      string   `json:"timeout"`
				ExitCode     int      `json:"exit_code"`
				Status       string   `json:"status"`
				TaskID       string   `json:"task_id"`
				RunID        string   `json:"run_id"`
				Stage        string   `json:"stage"`
				Attempt      int      `json:"attempt"`
				ArtifactPath string   `json:"artifact_path"`
				Argv         []string `json:"argv"`
				ArgvRedacted bool     `json:"argv_redacted"`
			} `json:"commands"`
			Stages []struct {
				Name         string `json:"name"`
				Status       string `json:"status"`
				TaskID       string `json:"task_id"`
				RunID        string `json:"run_id"`
				AttemptCount int    `json:"attempt_count"`
			} `json:"stages"`
		} `json:"bundle"`
		Workflow struct {
			Name              string `json:"name"`
			Trigger           string `json:"trigger"`
			Reloaded          bool   `json:"reloaded"`
			IgnoreCache       bool   `json:"ignore_cache"`
			CachePolicy       string `json:"cache_policy"`
			RequestedURL      string `json:"requested_url"`
			RequestCount      int    `json:"request_count"`
			MessageCount      int    `json:"message_count"`
			RequestsTruncated bool   `json:"requests_truncated"`
			MessagesTruncated bool   `json:"messages_truncated"`
			Partial           bool   `json:"partial"`
			Redact            string `json:"redact"`
			InlinePayloads    bool   `json:"inline_payloads"`
			RunID             string `json:"run_id"`
			TaskID            string `json:"task_id"`
			Stage             string `json:"stage"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow debug-bundle output is invalid JSON: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatalf("workflow debug-bundle raw output is invalid JSON: %v", err)
	}
	for _, field := range []string{"requests", "messages", "snapshot"} {
		if _, ok := raw[field]; ok {
			t.Fatalf("workflow debug-bundle default JSON includes %q payload; want artifact references only", field)
		}
	}

	if !got.OK || got.Target.ID == "" || got.Target.Type != "page" || got.Target.Title == "" || !strings.Contains(got.Target.URL, "https://example.test/app") || strings.Contains(got.Target.URL, "token=abc") {
		t.Fatalf("workflow debug-bundle target = %+v, want selected page target", got.Target)
	}
	if got.Workflow.Name != "debug-bundle" || got.Workflow.Trigger != "navigate" || got.Workflow.Reloaded || !got.Workflow.IgnoreCache || got.Workflow.CachePolicy != "bypass_http_cache" || !strings.Contains(got.Workflow.RequestedURL, "token=%3Credacted%3E") || got.Workflow.Redact != artifacts.ModeSafe || got.Workflow.InlinePayloads || got.Workflow.RunID != "run-1" || got.Workflow.TaskID != "task-1" || got.Workflow.Stage != "preflight" {
		t.Fatalf("workflow debug-bundle metadata = %+v, want debug-bundle workflow metadata", got.Workflow)
	}
	if got.Evidence.Requests < 2 || got.Evidence.Messages == 0 || got.Evidence.SnapshotItems == 0 || got.Evidence.Requests != got.Workflow.RequestCount || got.Evidence.Messages != got.Workflow.MessageCount {
		t.Fatalf("workflow debug-bundle evidence = %+v workflow=%+v, want summarized request/message/snapshot counts", got.Evidence, got.Workflow)
	}
	if got.Workflow.RequestsTruncated || got.Workflow.MessagesTruncated {
		t.Fatalf("workflow debug-bundle = %+v, expect no truncation in synthetic window", got.Workflow)
	}
	if got.Workflow.Partial {
		t.Fatalf("workflow debug-bundle = %+v, expect zero collector errors with synthetic events", got.Workflow)
	}
	if got.Bundle.SchemaVersion != "cdp-evidence-bundle/v1" || got.Bundle.RedactionMode != artifacts.ModeSafe || got.Bundle.DefaultJSON != "artifact_references" || got.Bundle.InlinePayloads {
		t.Fatalf("workflow bundle = %+v, want safe artifact-reference manifest", got.Bundle)
	}
	if got.Bundle.Layout.Manifest != filepath.Join(outDir, "debug-bundle.bundle.json") || got.Bundle.Layout.CommandLog != filepath.Join(outDir, "debug-bundle.command-log.jsonl") || got.Bundle.Layout.StageLog != filepath.Join(outDir, "debug-bundle.stage-log.json") {
		t.Fatalf("workflow bundle layout = %+v, want stable artifact paths under out dir", got.Bundle.Layout)
	}
	if got.Bundle.ArtifactCount != len(got.Artifacts) || got.Bundle.PublicSafeArtifacts == 0 || got.Bundle.LocalOnlyArtifacts == 0 || got.Bundle.UnsafeOptInArtifacts != 0 {
		t.Fatalf("workflow bundle safety counts = %+v artifacts=%d, want public-safe and local-only counts", got.Bundle, len(got.Artifacts))
	}
	if len(got.Bundle.Commands) != 1 || got.Bundle.Commands[0].Name != "workflow debug-bundle" || got.Bundle.Commands[0].BrowserMode == "" || got.Bundle.Commands[0].Timeout == "" || got.Bundle.Commands[0].ExitCode != 0 || got.Bundle.Commands[0].Status != "ok" || got.Bundle.Commands[0].TaskID != "task-1" || got.Bundle.Commands[0].RunID != "run-1" || got.Bundle.Commands[0].Stage != "preflight" || got.Bundle.Commands[0].Attempt != 1 || got.Bundle.Commands[0].ArtifactPath != filepath.Join(outDir, "debug-bundle.bundle.json") || !got.Bundle.Commands[0].ArgvRedacted {
		t.Fatalf("workflow bundle commands = %+v, want reproducible command log shape", got.Bundle.Commands)
	}
	if strings.Join(got.Bundle.Commands[0].Argv, " ") == "" || strings.Contains(strings.Join(got.Bundle.Commands[0].Argv, " "), "token=abc") {
		t.Fatalf("workflow bundle command argv = %+v, want redacted argv", got.Bundle.Commands[0].Argv)
	}
	if len(got.Bundle.Stages) != 1 || got.Bundle.Stages[0].Name != "preflight" || got.Bundle.Stages[0].Status != "ok" || got.Bundle.Stages[0].TaskID != "task-1" || got.Bundle.Stages[0].RunID != "run-1" || got.Bundle.Stages[0].AttemptCount != 1 {
		t.Fatalf("workflow bundle stages = %+v, want stage log shape", got.Bundle.Stages)
	}
	if len(got.Artifacts) < 8 {
		t.Fatalf("workflow artifacts = %+v, want artifact list with bundle + evidence", got.Artifacts)
	}
	if got.Artifact.Path == "" {
		t.Fatalf("workflow artifact path = %q, want non-empty", got.Artifact.Path)
	}
	if filepath.Dir(got.Artifact.Path) != filepath.Clean(outDir) {
		t.Fatalf("workflow artifact path = %s, want inside %q", got.Artifact.Path, outDir)
	}
	if _, err := os.Stat(got.Artifact.Path); err != nil {
		t.Fatalf("workflow artifact file was not written: %v", err)
	}
	requiredArtifacts := map[string]struct{}{
		"workflow-debug-bundle-bundle":        {},
		"workflow-debug-bundle-command-log":   {},
		"workflow-debug-bundle-network":       {},
		"workflow-debug-bundle-console":       {},
		"workflow-debug-bundle-page-metadata": {},
		"workflow-debug-bundle-snapshot":      {},
		"workflow-debug-bundle-stage-log":     {},
		"workflow-debug-bundle-workflow":      {},
	}
	seenArtifacts := map[string]struct{}{}
	artifactInBundleList := false
	networkPath := ""
	snapshotSawLocalOnly := false
	commandLogPath := ""
	for _, artifact := range got.Artifacts {
		if artifact.Path == "" || artifact.Type == "" || artifact.Content == "" || artifact.Safety.RedactionMode == "" || artifact.Safety.Classification == "" {
			t.Fatalf("workflow artifacts = %+v, want typed file metadata", got.Artifacts)
		}
		if artifact.Path == got.Artifact.Path {
			artifactInBundleList = true
		}
		if artifact.Type == "workflow-debug-bundle-network" {
			networkPath = artifact.Path
			if artifact.Safety.Classification != "public_safe" || !artifact.Safety.Shareable {
				t.Fatalf("workflow network artifact safety = %+v, want public-safe redacted summary", artifact.Safety)
			}
		}
		if artifact.Type == "workflow-debug-bundle-snapshot" && artifact.Safety.Classification == "local_only" && !artifact.Safety.Shareable {
			snapshotSawLocalOnly = true
		}
		if artifact.Type == "workflow-debug-bundle-command-log" {
			commandLogPath = artifact.Path
		}
		seenArtifacts[artifact.Type] = struct{}{}
		if _, err := os.Stat(artifact.Path); err != nil {
			t.Fatalf("workflow artifact %s was not written: %v", artifact.Path, err)
		}
		if filepath.Dir(artifact.Path) != filepath.Clean(outDir) {
			t.Fatalf("workflow artifact %q path %q, want inside %q", artifact.Type, artifact.Path, outDir)
		}
	}
	if !artifactInBundleList {
		t.Fatalf("workflow artifacts = %+v, want bundle path included in artifacts", got.Artifacts)
	}
	if networkPath == "" {
		t.Fatalf("workflow artifacts = %+v, want network artifact path", got.Artifacts)
	}
	networkBytes, err := os.ReadFile(networkPath)
	if err != nil {
		t.Fatalf("read network artifact: %v", err)
	}
	if strings.Contains(string(networkBytes), "token=abc") || !strings.Contains(string(networkBytes), "token=%3Credacted%3E") {
		t.Fatalf("network artifact = %s, want redacted token URL", string(networkBytes))
	}
	if !snapshotSawLocalOnly {
		t.Fatalf("workflow artifacts = %+v, want snapshot marked local-only", got.Artifacts)
	}
	if commandLogPath == "" {
		t.Fatalf("workflow artifacts = %+v, want command log artifact", got.Artifacts)
	}
	commandBytes, err := os.ReadFile(commandLogPath)
	if err != nil {
		t.Fatalf("read command log artifact: %v", err)
	}
	if !strings.Contains(string(commandBytes), `"task_id":"task-1"`) || !strings.Contains(string(commandBytes), `"artifact_path":"`+filepath.Join(outDir, "debug-bundle.bundle.json")+`"`) {
		t.Fatalf("command log artifact = %s, want task id and primary artifact path", string(commandBytes))
	}
	for artifactType := range requiredArtifacts {
		if _, ok := seenArtifacts[artifactType]; !ok {
			t.Fatalf("workflow artifacts = %+v, missing required type %q", got.Artifacts, artifactType)
		}
	}
}

func TestWorkflowDebugBundleReloadDefaultsAndPassiveOptOut(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "debug-existing",
		"type":     "page",
		"title":    "Existing App",
		"url":      "https://example.test/existing",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	run := func(args ...string) struct {
		Trigger     string `json:"trigger"`
		Reloaded    bool   `json:"reloaded"`
		IgnoreCache bool   `json:"ignore_cache"`
		CachePolicy string `json:"cache_policy"`
	} {
		t.Helper()
		var out, errOut bytes.Buffer
		code := cli.Execute(context.Background(), append([]string{"workflow", "debug-bundle", "--target", "debug-existing", "--since", "0s", "--json"}, args...), &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("debug-bundle %v exit=%d stdout=%s stderr=%s", args, code, out.String(), errOut.String())
		}
		var report struct {
			Workflow struct {
				Trigger     string `json:"trigger"`
				Reloaded    bool   `json:"reloaded"`
				IgnoreCache bool   `json:"ignore_cache"`
				CachePolicy string `json:"cache_policy"`
			} `json:"workflow"`
		}
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatalf("decode debug-bundle: %v", err)
		}
		return report.Workflow
	}

	defaultPolicy := run()
	if defaultPolicy.Trigger != "reload" || !defaultPolicy.Reloaded || !defaultPolicy.IgnoreCache || defaultPolicy.CachePolicy != "bypass_http_cache" {
		t.Fatalf("default policy = %+v, want cache-bypassing reload", defaultPolicy)
	}
	passive := run("--reload=false", "--ignore-cache=false")
	if passive.Trigger != "observe" || passive.Reloaded || passive.IgnoreCache || passive.CachePolicy != "normal_http_cache" {
		t.Fatalf("passive policy = %+v, want no-load observation", passive)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "debug-bundle", "--target", "debug-existing", "--reload=false", "--ignore-cache=true", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage || !strings.Contains(out.String(), "--reload=false") {
		t.Fatalf("contradictory policy exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

func TestWorkflowVerifyJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "verify.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "verify", "https://example.test/app", "--wait", "250ms", "--out", outPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow verify exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Requests []struct {
			ID     string `json:"id"`
			Failed bool   `json:"failed"`
		} `json:"requests"`
		Messages []struct {
			Level string `json:"level"`
		} `json:"messages"`
		Workflow struct {
			Name         string `json:"name"`
			RequestedURL string `json:"requested_url"`
		} `json:"workflow"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow verify output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "verify" || got.Workflow.RequestedURL != "https://example.test/app" {
		t.Fatalf("workflow verify = %+v, want ok verification workflow result", got)
	}
	if len(got.Requests) != 1 || got.Requests[0].ID != "request-failed" || !got.Requests[0].Failed {
		t.Fatalf("workflow verify requests = %+v, want one failed request", got.Requests)
	}
	if len(got.Messages) == 0 {
		t.Fatalf("workflow verify messages = %+v, want at least one console/network message", got.Messages)
	}
	if got.Artifact.Path != outPath {
		t.Fatalf("workflow verify artifact = %+v, want artifact at %s", got.Artifact, outPath)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("workflow verify artifact was not written: %v", err)
	}
}

func TestWorkflowPageLoadJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "page-load.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "page-load", "https://example.test/app", "--wait", "250ms", "--out", outPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow page-load exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Requests []struct {
			ID     string `json:"id"`
			Status int    `json:"status"`
		} `json:"requests"`
		Messages []struct {
			Text string `json:"text"`
		} `json:"messages"`
		Workflow struct {
			Name         string `json:"name"`
			Trigger      string `json:"trigger"`
			RequestedURL string `json:"requested_url"`
			Partial      bool   `json:"partial"`
		} `json:"workflow"`
		ContentState struct {
			Class           string `json:"class"`
			FinalURL        string `json:"final_url"`
			MainStatus      int    `json:"main_status"`
			Actionable      bool   `json:"actionable"`
			TextSampleBytes int    `json:"text_sample_bytes"`
		} `json:"content_state"`
		Storage struct {
			LocalStorageKeys []string `json:"local_storage_keys"`
		} `json:"storage"`
		Performance struct {
			Count int `json:"count"`
		} `json:"performance"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow page-load output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "page-load" || got.Workflow.Trigger != "navigate" || got.Workflow.RequestedURL != "https://example.test/app" || got.Workflow.Partial {
		t.Fatalf("workflow page-load metadata = %+v, want complete navigate workflow", got.Workflow)
	}
	if len(got.Requests) != 2 || got.Requests[0].Status != 200 || len(got.Messages) != 3 || !strings.Contains(got.Messages[1].Text, "failed to fetch dashboard") {
		t.Fatalf("workflow page-load evidence requests=%+v messages=%+v, want network and rich console evidence", got.Requests, got.Messages)
	}
	if got.ContentState.Class != "content" || got.ContentState.FinalURL != "https://example.test/current" || got.ContentState.MainStatus != 200 || !got.ContentState.Actionable || got.ContentState.TextSampleBytes == 0 {
		t.Fatalf("workflow page-load content_state = %+v, want actionable content classification", got.ContentState)
	}
	if len(got.Storage.LocalStorageKeys) != 1 || got.Storage.LocalStorageKeys[0] != "feature" || got.Performance.Count != 2 || got.Artifact.Path != outPath {
		t.Fatalf("workflow page-load storage/performance/artifact = storage=%+v performance=%+v artifact=%+v", got.Storage, got.Performance, got.Artifact)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("page-load artifact was not written: %v", err)
	}
}

func TestWorkflowRenderedExtractJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outDir := t.TempDir()
	rawURL := "https://www.google.com/search?q=agentic+engineering+2026+evolutions&safe=active&tbs=qdr:m"
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "rendered-extract", rawURL, "--serp", "google", "--out-dir", outDir, "--wait", "1500ms", "--settle", "1s", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow rendered-extract exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK        bool                 `json:"ok"`
		Target    struct{ URL string } `json:"target"`
		Readiness struct {
			NavigatedFromAboutBlank   bool   `json:"navigated_from_about_blank"`
			DocumentReadyState        string `json:"document_ready_state"`
			UsefulContentSeen         bool   `json:"useful_content_seen"`
			ContentStableSeen         bool   `json:"content_stable_seen"`
			CaptureConsistencyChecked bool   `json:"capture_consistency_checked"`
			CaptureConsistent         bool   `json:"capture_consistent"`
			StablePolls               int    `json:"stable_polls"`
			PollCount                 int    `json:"poll_count"`
		} `json:"readiness"`
		Artifacts struct {
			VisibleJSON string `json:"visible_json"`
			VisibleTXT  string `json:"visible_txt"`
			HTMLJSON    string `json:"html_json"`
			Markdown    string `json:"markdown"`
			LinksJSON   string `json:"links_json"`
		} `json:"artifacts"`
		Quality struct {
			SnapshotCount     int `json:"snapshot_count"`
			VisibleWordCount  int `json:"visible_word_count"`
			HTMLLength        int `json:"html_length"`
			MarkdownWordCount int `json:"markdown_word_count"`
			ExternalLinkCount int `json:"external_link_count"`
		} `json:"quality"`
		Links struct {
			Query      string `json:"query"`
			TimeFilter string `json:"time_filter"`
			Serp       string `json:"serp"`
		} `json:"links"`
		Warnings []string `json:"warnings"`
		Workflow struct {
			Name   string `json:"name"`
			Closed bool   `json:"closed"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow rendered-extract output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "rendered-extract" || !got.Workflow.Closed || !got.Readiness.NavigatedFromAboutBlank || got.Readiness.DocumentReadyState != "complete" || !got.Readiness.UsefulContentSeen || !got.Readiness.ContentStableSeen || !got.Readiness.CaptureConsistencyChecked || !got.Readiness.CaptureConsistent || got.Readiness.StablePolls < 2 || got.Readiness.PollCount < 3 {
		t.Fatalf("workflow rendered-extract metadata = %+v readiness=%+v", got.Workflow, got.Readiness)
	}
	if got.Target.URL == "about:blank" || got.Links.Query != "agentic engineering 2026 evolutions" || got.Links.TimeFilter != "qdr:m" || got.Links.Serp != "google" {
		t.Fatalf("workflow rendered-extract target/links = target=%+v links=%+v", got.Target, got.Links)
	}
	if got.Quality.SnapshotCount == 0 || got.Quality.VisibleWordCount == 0 || got.Quality.HTMLLength == 0 || got.Quality.MarkdownWordCount == 0 || got.Quality.ExternalLinkCount == 0 || len(got.Warnings) != 0 {
		t.Fatalf("workflow rendered-extract quality=%+v warnings=%+v", got.Quality, got.Warnings)
	}
	for _, path := range []string{got.Artifacts.VisibleJSON, got.Artifacts.VisibleTXT, got.Artifacts.HTMLJSON, got.Artifacts.Markdown, got.Artifacts.LinksJSON} {
		if path == "" {
			t.Fatalf("workflow rendered-extract artifacts = %+v, want all artifact paths", got.Artifacts)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("workflow rendered-extract artifact %q was not written: %v", path, err)
		}
		if !strings.HasPrefix(path, outDir) {
			t.Fatalf("workflow rendered-extract artifact %q, want under %q", path, outDir)
		}
	}
	linksBytes, err := os.ReadFile(got.Artifacts.LinksJSON)
	if err != nil {
		t.Fatalf("read links artifact: %v", err)
	}
	if !strings.Contains(string(linksBytes), "https://example.test/story") || strings.Contains(string(linksBytes), "google.com/url") {
		t.Fatalf("links artifact = %s, want decoded external result", string(linksBytes))
	}
}

func TestWorkflowRenderedExtractUsesArxivSemanticContentProfile(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outDir := t.TempDir()
	rawURL := "https://arxiv.org/pdf/2603.26487"
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", rawURL,
		"--content-extractor", "auto",
		"--out-dir", outDir,
		"--wait", "0",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow rendered-extract arxiv exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		Content struct {
			Mode                    string `json:"mode"`
			Profile                 string `json:"profile"`
			Strategy                string `json:"strategy"`
			Representation          string `json:"representation"`
			RepresentationRewritten bool   `json:"representation_rewritten"`
			NativeAttempted         bool   `json:"native_attempted"`
			NativeSucceeded         bool   `json:"native_succeeded"`
			FallbackUsed            bool   `json:"fallback_used"`
			RootSelector            string `json:"root_selector"`
			Representations         struct {
				HTML   string `json:"html"`
				PDF    string `json:"pdf"`
				Source string `json:"source"`
			} `json:"representations"`
		} `json:"content"`
		Artifacts struct {
			Markdown string `json:"markdown"`
		} `json:"artifacts"`
		Workflow struct {
			RequestedURL  string `json:"requested_url"`
			NavigationURL string `json:"navigation_url"`
			FinalURL      string `json:"final_url"`
			Selector      string `json:"selector"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode arxiv rendered extraction: %v", err)
	}
	if got.Content.Mode != "auto" || got.Content.Profile != "arxiv" || got.Content.Strategy != "semantic-dom" ||
		got.Content.Representation != "html" || !got.Content.RepresentationRewritten ||
		!got.Content.NativeAttempted || !got.Content.NativeSucceeded || got.Content.FallbackUsed ||
		got.Content.RootSelector != "article.ltx_document" {
		t.Fatalf("arxiv content provenance = %+v", got.Content)
	}
	if got.Content.Representations.HTML != "https://arxiv.org/html/2603.26487" ||
		got.Content.Representations.PDF != "https://arxiv.org/pdf/2603.26487" ||
		got.Content.Representations.Source != "https://arxiv.org/src/2603.26487" {
		t.Fatalf("arxiv representations = %+v", got.Content.Representations)
	}
	if got.Workflow.RequestedURL != rawURL ||
		got.Workflow.NavigationURL != "https://arxiv.org/html/2603.26487" ||
		got.Workflow.Selector != "article.ltx_document" {
		t.Fatalf("arxiv workflow = %+v", got.Workflow)
	}
	markdown, err := os.ReadFile(got.Artifacts.Markdown)
	if err != nil {
		t.Fatalf("read arxiv markdown: %v", err)
	}
	for _, want := range []string{"# Synthetic arXiv Paper", "## Results", "$x^2$", "[supporting source](https://example.test/source)"} {
		if !strings.Contains(string(markdown), want) {
			t.Fatalf("arxiv markdown missing %q:\n%s", want, string(markdown))
		}
	}
}

func TestWorkflowRenderedExtractUsesHackerNewsDiscussionProfile(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outDir := t.TempDir()
	rawURL := "https://news.ycombinator.com/item?id=46641042"
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", rawURL,
		"--out-dir", outDir,
		"--wait", "0",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow rendered-extract HN exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		Content struct {
			Mode            string `json:"mode"`
			Profile         string `json:"profile"`
			Strategy        string `json:"strategy"`
			Representation  string `json:"representation"`
			NativeAttempted bool   `json:"native_attempted"`
			NativeSucceeded bool   `json:"native_succeeded"`
			FallbackUsed    bool   `json:"fallback_used"`
			ItemCount       int    `json:"item_count"`
		} `json:"content"`
		Artifacts struct {
			Markdown string `json:"markdown"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode HN rendered extraction: %v", err)
	}
	if got.Content.Mode != "auto" || got.Content.Profile != "hacker-news" ||
		got.Content.Strategy != "discussion-tree" || got.Content.Representation != "discussion" ||
		!got.Content.NativeAttempted || !got.Content.NativeSucceeded || got.Content.FallbackUsed ||
		got.Content.ItemCount != 2 {
		t.Fatalf("HN content provenance = %+v", got.Content)
	}
	markdown, err := os.ReadFile(got.Artifacts.Markdown)
	if err != nil {
		t.Fatalf("read HN markdown: %v", err)
	}
	for _, want := range []string{
		"# Synthetic HN discussion",
		"## Comments (2)",
		"- **alice** · [1 hour ago](https://news.ycombinator.com/item?id=101)",
		"    - **bob** · [45 minutes ago](https://news.ycombinator.com/item?id=102)",
	} {
		if !strings.Contains(string(markdown), want) {
			t.Fatalf("HN markdown missing %q:\n%s", want, string(markdown))
		}
	}
	if strings.Contains(string(markdown), "Hacker Newsnew | past") {
		t.Fatalf("HN markdown retained navigation chrome:\n%s", string(markdown))
	}
}

func TestWorkflowRenderedExtractFallsBackWhenNativeContentFails(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId":                   "native-content-failure",
		"type":                       "page",
		"url":                        "https://example.test/",
		"title":                      "Native content failure fixture",
		"fakeRenderedContentFailure": true,
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", "https://news.ycombinator.com/item?id=46641042",
		"--out-dir", t.TempDir(),
		"--wait", "0",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("native content fallback exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		Content struct {
			Profile               string `json:"profile"`
			PlannedStrategy       string `json:"planned_strategy"`
			Strategy              string `json:"strategy"`
			PlannedRepresentation string `json:"planned_representation"`
			Representation        string `json:"representation"`
			RootSelector          string `json:"root_selector"`
			NativeAttempted       bool   `json:"native_attempted"`
			NativeSucceeded       bool   `json:"native_succeeded"`
			FallbackUsed          bool   `json:"fallback_used"`
			FallbackReason        string `json:"fallback_reason"`
		} `json:"content"`
		Warnings  []string `json:"warnings"`
		Artifacts struct {
			Markdown        string `json:"markdown"`
			DiagnosticsJSON string `json:"diagnostics_json"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode native content fallback: %v", err)
	}
	if got.Content.Profile != "hacker-news" ||
		got.Content.PlannedStrategy != "discussion-tree" || got.Content.Strategy != "legacy-html" ||
		got.Content.PlannedRepresentation != "discussion" || got.Content.Representation != "rendered-html" ||
		got.Content.RootSelector != "body" ||
		!got.Content.NativeAttempted || got.Content.NativeSucceeded ||
		!got.Content.FallbackUsed || !strings.Contains(got.Content.FallbackReason, "synthetic native content failure") {
		t.Fatalf("native content fallback provenance = %+v", got.Content)
	}
	if !testContainsSubstring(got.Warnings, "fell back to generic HTML conversion") {
		t.Fatalf("native content fallback warnings = %+v", got.Warnings)
	}
	for _, path := range []string{got.Artifacts.Markdown, got.Artifacts.DiagnosticsJSON} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("native content fallback artifact %q: %v", path, err)
		}
	}
}

func TestWorkflowRenderedExtractArxivNativeFailureKeepsGenericBodyFallback(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId":                   "arxiv-native-content-failure",
		"type":                       "page",
		"url":                        "https://example.test/",
		"title":                      "arXiv native content failure fixture",
		"fakeRenderedContentFailure": true,
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", "https://arxiv.org/pdf/2603.26487",
		"--out-dir", t.TempDir(),
		"--wait", "0",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("arxiv native fallback exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		Content struct {
			Profile         string `json:"profile"`
			PlannedStrategy string `json:"planned_strategy"`
			Strategy        string `json:"strategy"`
			Representation  string `json:"representation"`
			RootSelector    string `json:"root_selector"`
			NativeAttempted bool   `json:"native_attempted"`
			NativeSucceeded bool   `json:"native_succeeded"`
			FallbackUsed    bool   `json:"fallback_used"`
		} `json:"content"`
		Artifacts struct {
			Markdown string `json:"markdown"`
		} `json:"artifacts"`
		Workflow struct {
			Selector string `json:"selector"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode arxiv native fallback: %v", err)
	}
	if got.Content.Profile != "arxiv" ||
		got.Content.PlannedStrategy != "semantic-dom" || got.Content.Strategy != "legacy-html" ||
		got.Content.Representation != "rendered-html" || got.Content.RootSelector != "body" ||
		!got.Content.NativeAttempted || got.Content.NativeSucceeded || !got.Content.FallbackUsed ||
		got.Workflow.Selector != "article.ltx_document" {
		t.Fatalf("arxiv native fallback provenance = %+v; workflow=%+v", got.Content, got.Workflow)
	}
	markdown, err := os.ReadFile(got.Artifacts.Markdown)
	if err != nil {
		t.Fatalf("read arxiv fallback markdown: %v", err)
	}
	if !strings.Contains(string(markdown), "Synthetic main text") {
		t.Fatalf("arxiv fallback did not retain generic body Markdown:\n%s", string(markdown))
	}
}

func TestWorkflowRenderedExtractArxivRedirectMismatchUsesGenericBodyFallback(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId":                    "arxiv-redirect-mismatch",
		"type":                        "page",
		"url":                         "https://example.test/",
		"title":                       "arXiv redirect mismatch fixture",
		"fakeRenderedExtractFinalURL": "https://example.test/article",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", "https://arxiv.org/pdf/2603.26487",
		"--out-dir", t.TempDir(),
		"--wait", "0",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("arxiv redirect fallback exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		Content struct {
			Strategy        string `json:"strategy"`
			Representation  string `json:"representation"`
			RootSelector    string `json:"root_selector"`
			NativeAttempted bool   `json:"native_attempted"`
			FallbackUsed    bool   `json:"fallback_used"`
			FallbackReason  string `json:"fallback_reason"`
		} `json:"content"`
		Workflow struct {
			FinalURL string `json:"final_url"`
			Selector string `json:"selector"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode arxiv redirect fallback: %v", err)
	}
	if got.Content.Strategy != "legacy-html" || got.Content.Representation != "rendered-html" ||
		got.Content.RootSelector != "body" || got.Content.NativeAttempted || !got.Content.FallbackUsed ||
		!strings.Contains(got.Content.FallbackReason, "resolved final URL") ||
		got.Workflow.FinalURL != "https://example.test/article" || got.Workflow.Selector != "article.ltx_document" {
		t.Fatalf("arxiv redirect fallback content=%+v workflow=%+v", got.Content, got.Workflow)
	}
}

func TestWorkflowRenderedExtractGenericContentOverride(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	rawURL := "https://arxiv.org/pdf/2603.26487"
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", rawURL,
		"--content-extractor", "generic",
		"--out-dir", t.TempDir(),
		"--wait", "0",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow rendered-extract generic override exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Content struct {
			Mode            string `json:"mode"`
			Profile         string `json:"profile"`
			NativeAttempted bool   `json:"native_attempted"`
		} `json:"content"`
		Workflow struct {
			NavigationURL string `json:"navigation_url"`
			Selector      string `json:"selector"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode generic rendered extraction: %v", err)
	}
	if got.Content.Mode != "generic" || got.Content.Profile != "generic" || got.Content.NativeAttempted ||
		got.Workflow.NavigationURL != rawURL || got.Workflow.Selector != "body" {
		t.Fatalf("generic override content=%+v workflow=%+v", got.Content, got.Workflow)
	}
}

func TestWorkflowRenderedExtractRejectsUnknownContentExtractor(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", "https://example.test/article",
		"--content-extractor", "native-only",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("unknown content extractor exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
}

func TestRenderedExtractionCommandsRejectSettleBeyondPositiveWait(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "rendered-extract", args: []string{"workflow", "rendered-extract", "https://example.test/app", "--wait", "1s", "--settle", "2s", "--json"}},
		{name: "web-research-serp", args: []string{"workflow", "web-research", "serp", "--wait", "1s", "--settle", "2s", "--json"}},
		{name: "web-research-extract", args: []string{"workflow", "web-research", "extract", "--wait", "1s", "--settle", "2s", "--json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tc.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
			}
			var got struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("invalid JSON error: %v; stdout=%s", err, out.String())
			}
			if got.Code != "usage" || !strings.Contains(got.Message, "--settle must not exceed positive --wait") {
				t.Fatalf("error = %+v", got)
			}
		})
	}
}

func TestWorkflowRenderedExtractReusesReloadsAndKeepsExistingTarget(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "existing-page-123",
		"type":     "page",
		"title":    "Existing dashboard",
		"url":      "https://example.test/dashboard",
		"attached": false,
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract",
		"--target", "existing-page",
		"--reload",
		"--ignore-cache",
		"--out-dir", outDir,
		"--wait", "1500ms",
		"--settle", "1s",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("rendered-extract reuse exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			TargetID string `json:"id"`
		} `json:"target"`
		Workflow struct {
			Trigger     string `json:"trigger"`
			CreatedPage bool   `json:"created_page"`
			ReusedPage  bool   `json:"reused_page"`
			Reloaded    bool   `json:"reloaded"`
			IgnoreCache bool   `json:"ignore_cache"`
			Closed      bool   `json:"closed"`
			Cleanup     struct {
				Skipped bool   `json:"skipped"`
				Reason  string `json:"reason"`
			} `json:"cleanup"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode rendered-extract reuse output: %v", err)
	}
	if !got.OK || got.Target.TargetID != "existing-page-123" || got.Workflow.Trigger != "reload" || got.Workflow.CreatedPage || !got.Workflow.ReusedPage || !got.Workflow.Reloaded || !got.Workflow.IgnoreCache || got.Workflow.Closed || !got.Workflow.Cleanup.Skipped || got.Workflow.Cleanup.Reason != "caller_owned" {
		t.Fatalf("rendered-extract reuse ownership = %+v", got)
	}
	if count := fakePagesCount(t); count != 1 {
		t.Fatalf("page count after reused extraction = %d, want 1", count)
	}
}

func TestWorkflowRenderedExtractTargetSelectorsFailClosed(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-a", "type": "page", "title": "Dashboard A", "url": "https://example.test/a"},
		{"targetId": "page-b", "type": "page", "title": "Dashboard B", "url": "https://example.test/b"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "rendered-extract", "--url-contains", "example.test", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("ambiguous rendered-extract exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode ambiguous rendered-extract output: %v", err)
	}
	if got.OK || got.Code != "ambiguous_target" {
		t.Fatalf("ambiguous rendered-extract output = %+v", got)
	}
}

func TestWorkflowRenderedExtractFailureCleansOnlyCreatedTarget(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "baseline-page",
		"type":     "page",
		"title":    "Baseline",
		"url":      "https://example.test/baseline",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	blockedOutDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedOutDir, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write blocked output fixture: %v", err)
	}
	args := []string{"workflow", "rendered-extract", "https://example.test/new", "--out-dir", blockedOutDir, "--wait", "0s", "--json"}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitInternal {
		t.Fatalf("created failure exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitInternal, out.String(), errOut.String())
	}
	if count := fakePagesCount(t); count != 1 {
		t.Fatalf("page count after created-target failure = %d, want baseline 1", count)
	}

	out.Reset()
	errOut.Reset()
	args = []string{"workflow", "rendered-extract", "--target", "baseline-page", "--out-dir", blockedOutDir, "--wait", "0s", "--json"}
	code = cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitInternal {
		t.Fatalf("reused failure exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitInternal, out.String(), errOut.String())
	}
	if count := fakePagesCount(t); count != 1 {
		t.Fatalf("page count after reused-target failure = %d, want unchanged 1", count)
	}
}

func TestWorkflowRenderedExtractCleanupWaitsForTargetGone(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId":             "delayed-close-sentinel",
		"type":                 "page",
		"title":                "Sentinel",
		"url":                  "https://example.test/sentinel",
		"fakeCloseTargetDelay": true,
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", "https://example.test/delayed-cleanup",
		"--out-dir", t.TempDir(), "--wait", "0", "--settle", "0",
		"--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("delayed cleanup exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		Workflow struct {
			Closed  bool `json:"closed"`
			Cleanup struct {
				Closed       bool  `json:"closed"`
				TargetGone   bool  `json:"target_gone"`
				AttemptCount int   `json:"attempt_count"`
				MaxAttempts  int   `json:"max_attempts"`
				ElapsedMS    int64 `json:"elapsed_ms"`
			} `json:"cleanup"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode delayed cleanup output: %v", err)
	}
	if !got.Workflow.Closed || !got.Workflow.Cleanup.Closed || !got.Workflow.Cleanup.TargetGone || got.Workflow.Cleanup.AttemptCount < 1 || got.Workflow.Cleanup.MaxAttempts < got.Workflow.Cleanup.AttemptCount || got.Workflow.Cleanup.ElapsedMS < 0 {
		t.Fatalf("delayed cleanup evidence = %+v", got.Workflow)
	}
	if count := fakePagesCount(t); count != 1 {
		t.Fatalf("delayed cleanup left owned target: page count=%d, want baseline 1", count)
	}
}

func TestWorkflowRenderedExtractKeepOpenRetainsCreatedTarget(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", "https://example.test/keep-open",
		"--out-dir", t.TempDir(), "--wait", "0", "--settle", "0", "--keep-open",
		"--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("keep-open exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		Workflow struct {
			Closed  bool `json:"closed"`
			Cleanup struct {
				Skipped bool   `json:"skipped"`
				Reason  string `json:"reason"`
			} `json:"cleanup"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode keep-open output: %v", err)
	}
	if got.Workflow.Closed || !got.Workflow.Cleanup.Skipped || got.Workflow.Cleanup.Reason != "keep_open" {
		t.Fatalf("keep-open cleanup = %+v", got.Workflow)
	}
	if count := fakePagesCount(t); count != 1 {
		t.Fatalf("keep-open page count=%d, want one created page", count)
	}
}

func TestWorkflowRenderedExtractKeepOpenAttachFailureRetainsCreatedTarget(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId":                  "attach-failure-sentinel",
		"type":                      "page",
		"title":                     "Sentinel",
		"url":                       "https://example.test/sentinel",
		"fakeAttachErrorForCreated": true,
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "rendered-extract", "https://example.test/attach-failure",
		"--out-dir", t.TempDir(), "--wait", "0", "--settle", "0", "--keep-open", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitConnection {
		t.Fatalf("keep-open attach failure exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitConnection, out.String(), errOut.String())
	}
	if count := fakePagesCount(t); count != 2 {
		t.Fatalf("keep-open attach failure page count=%d, want baseline plus retained created page", count)
	}
}

func TestWorkflowRenderedExtractCloseFailurePreservesPrimaryError(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId":             "close-failure-sentinel",
		"type":                 "page",
		"title":                "Sentinel",
		"url":                  "https://example.test/sentinel",
		"fakeCloseTargetError": true,
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	blockedOutDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedOutDir, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write blocked output fixture: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "rendered-extract", "https://example.test/new", "--out-dir", blockedOutDir, "--wait", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitInternal {
		t.Fatalf("cleanup failure exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitInternal, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			PrimaryError map[string]any `json:"primary_error"`
			Cleanup      struct {
				TargetID        string `json:"target_id"`
				Error           string `json:"error"`
				RecoveryCommand string `json:"recovery_command"`
			} `json:"cleanup"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode cleanup failure output: %v", err)
	}
	if got.OK || got.Code != "rendered_extract_cleanup_failed" || got.Data.PrimaryError["code"] != "artifact_write_failed" || got.Data.Cleanup.TargetID == "" || got.Data.Cleanup.Error == "" || got.Data.Cleanup.RecoveryCommand != "cdp page cleanup --target "+got.Data.Cleanup.TargetID+" --force --close --json" {
		t.Fatalf("cleanup failure output = %+v", got)
	}
}

func fakePagesCount(t *testing.T) int {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Pages []json.RawMessage `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode pages output: %v", err)
	}
	return len(got.Pages)
}

func TestWorkflowWebResearchSERPPaginates(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("agentic engineering\tqdr:m\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "research")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--result-pages", "2", "--max-candidates", "20", "--parallel", "3", "--out-dir", outDir, "--wait", "250ms", "--settle", "0", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		SERPs []struct {
			Query    string `json:"query"`
			SerpPage int    `json:"serp_page"`
			Report   struct {
				Artifacts struct {
					Markdown string `json:"markdown"`
				} `json:"artifacts"`
			} `json:"report"`
		} `json:"serps"`
		Candidates []struct {
			Serp       string `json:"serp"`
			Query      string `json:"query"`
			TimeFilter string `json:"time_filter"`
			SerpPage   int    `json:"serp_page"`
			RankOnPage int    `json:"rank_on_page"`
			GlobalRank int    `json:"global_rank"`
			URL        string `json:"url"`
		} `json:"candidates"`
		Artifacts struct {
			CandidatesJSON string `json:"candidates_json"`
			CandidatesTSV  string `json:"candidates_tsv"`
		} `json:"artifacts"`
		Workflow struct {
			Name        string `json:"name"`
			Serp        string `json:"serp"`
			QueryCount  int    `json:"query_count"`
			ResultPages int    `json:"result_pages"`
			Parallel    int    `json:"parallel"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "web-research-serp" || got.Workflow.Serp != "google" || got.Workflow.QueryCount != 1 || got.Workflow.ResultPages != 2 || got.Workflow.Parallel != 3 {
		t.Fatalf("workflow web-research serp metadata = %+v", got.Workflow)
	}
	if len(got.SERPs) != 2 || got.SERPs[0].SerpPage != 1 || got.SERPs[1].SerpPage != 2 {
		t.Fatalf("workflow web-research serp pages = %+v", got.SERPs)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].Serp != "google" || got.Candidates[0].SerpPage != 1 || got.Candidates[0].RankOnPage != 1 || got.Candidates[0].GlobalRank != 1 || got.Candidates[0].TimeFilter != "qdr:m" {
		t.Fatalf("workflow web-research candidates = %+v", got.Candidates)
	}
	for _, path := range []string{got.SERPs[0].Report.Artifacts.Markdown, got.SERPs[1].Report.Artifacts.Markdown, got.Artifacts.CandidatesJSON, got.Artifacts.CandidatesTSV} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("workflow web-research serp artifact %q was not written: %v", path, err)
		}
		if !strings.HasPrefix(path, outDir) {
			t.Fatalf("workflow web-research serp artifact %q, want under %q", path, outDir)
		}
	}
	artifactID := filepath.Base(filepath.Dir(filepath.Dir(got.SERPs[0].Report.Artifacts.Markdown)))
	if !strings.HasPrefix(artifactID, "001-agentic-engineering--tbs-") ||
		!strings.Contains(got.SERPs[0].Report.Artifacts.Markdown, filepath.Join(artifactID, "page-1", "page.md")) ||
		!strings.Contains(got.SERPs[1].Report.Artifacts.Markdown, filepath.Join(artifactID, "page-2", "page.md")) {
		t.Fatalf("workflow web-research serp artifact layout = %+v", got.SERPs)
	}
}

func TestWorkflowWebResearchSERPReportsNavigationDelayWithoutSleepingBeforeFirst(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("agentic engineering\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "research")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	code := cli.Execute(ctx, []string{
		"workflow", "web-research", "serp",
		"--query-file", queryFile,
		"--result-pages", "1",
		"--parallel", "1",
		"--navigation-delay", "30s",
		"--progress", "stderr",
		"--out-dir", outDir,
		"--wait", "250ms",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		Workflow struct {
			NavigationDelay string `json:"navigation_delay"`
			EngineLanes     []struct {
				NavigationDelay string `json:"navigation_delay"`
			} `json:"engine_lanes"`
		} `json:"workflow"`
		Artifacts struct {
			QueriesJSON string `json:"queries_json"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	if got.Workflow.NavigationDelay != "30s" {
		t.Fatalf("workflow navigation_delay = %q, want 30s", got.Workflow.NavigationDelay)
	}
	if len(got.Workflow.EngineLanes) != 1 || got.Workflow.EngineLanes[0].NavigationDelay != "30s" {
		t.Fatalf("workflow engine lanes = %+v, want one lane reporting 30s navigation delay", got.Workflow.EngineLanes)
	}
	queriesPayload, err := os.ReadFile(got.Artifacts.QueriesJSON)
	if err != nil {
		t.Fatalf("read queries metadata: %v", err)
	}
	var queriesMetadata struct {
		NavigationDelay string `json:"navigation_delay"`
	}
	if err := json.Unmarshal(queriesPayload, &queriesMetadata); err != nil {
		t.Fatalf("decode queries metadata: %v", err)
	}
	if queriesMetadata.NavigationDelay != "30s" {
		t.Fatalf("queries metadata navigation_delay = %q, want 30s", queriesMetadata.NavigationDelay)
	}
	var progressEvent struct {
		Event           string `json:"event"`
		NavigationDelay string `json:"navigation_delay"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(errOut.Bytes()), &progressEvent); err != nil {
		t.Fatalf("decode progress event %q: %v", errOut.String(), err)
	}
	if progressEvent.Event != "serp_page_complete" || progressEvent.NavigationDelay != "30s" {
		t.Fatalf("progress event = %+v, want reported 30s navigation delay", progressEvent)
	}
}

func TestWorkflowWebResearchSERPFastFailDoesNotDelayAfterFirstBlockedNavigation(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("serp block fixture one\nsecond query\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	code := cli.Execute(ctx, []string{
		"workflow", "web-research", "serp",
		"--query-file", queryFile,
		"--result-pages", "1",
		"--parallel", "1",
		"--navigation-delay", "1h",
		"--fast-fail-blocked",
		"--blocked-failure-threshold", "1",
		"--fallback-serp", "none",
		"--out-dir", filepath.Join(tmpDir, "research"),
		"--wait", "250ms",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("fast-fail workflow exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Workflow struct {
			ScheduledResultPages int  `json:"scheduled_result_pages"`
			FastFailTriggered    bool `json:"fast_fail_triggered"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode fast-fail workflow: %v", err)
	}
	if got.Workflow.ScheduledResultPages != 1 || !got.Workflow.FastFailTriggered {
		t.Fatalf("fast-fail workflow = %+v, want one navigation and immediate stop", got.Workflow)
	}
}

func TestWorkflowWebResearchSERPSeparatesIdenticalDatedAndUndatedArtifacts(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	const query = "production LLM systems"
	const timeFilter = "cdr:1,cd_min:01/01/2026,cd_max:07/01/2026"
	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte(query+"\n"+query+"\t"+timeFilter+"\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "web-research", "serp",
		"--query-file", queryFile,
		"--result-pages", "1",
		"--parallel", "1",
		"--out-dir", filepath.Join(tmpDir, "research"),
		"--wait", "250ms",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		SERPs []struct {
			TimeFilter string `json:"time_filter"`
			Report     struct {
				Workflow struct {
					RequestedURL string `json:"requested_url"`
				} `json:"workflow"`
				Artifacts struct {
					Markdown string `json:"markdown"`
				} `json:"artifacts"`
			} `json:"report"`
		} `json:"serps"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	if len(got.SERPs) != 2 {
		t.Fatalf("SERP reports = %d, want 2", len(got.SERPs))
	}
	evergreenPath := got.SERPs[0].Report.Artifacts.Markdown
	datedPath := got.SERPs[1].Report.Artifacts.Markdown
	if evergreenPath == datedPath {
		t.Fatalf("evergreen and dated Markdown paths collide at %q", evergreenPath)
	}
	if !strings.Contains(evergreenPath, filepath.Join("001-production-llm-systems--all-time", "page-1")) {
		t.Fatalf("evergreen artifact path = %q", evergreenPath)
	}
	datedArtifactID := filepath.Base(filepath.Dir(filepath.Dir(datedPath)))
	if !strings.HasPrefix(datedArtifactID, "002-production-llm-systems--tbs-") {
		t.Fatalf("dated artifact path = %q, want input position and safe time-filter hash", datedPath)
	}
	for _, path := range []string{evergreenPath, datedPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("artifact %q was not written: %v", path, err)
		}
	}
	if got.SERPs[0].TimeFilter != "" || got.SERPs[1].TimeFilter != timeFilter {
		t.Fatalf("SERP time filters = %q and %q", got.SERPs[0].TimeFilter, got.SERPs[1].TimeFilter)
	}
	if strings.Contains(got.SERPs[0].Report.Workflow.RequestedURL, "tbs=") || !strings.Contains(got.SERPs[1].Report.Workflow.RequestedURL, "tbs=") {
		t.Fatalf("requested URLs do not preserve dated/undated identity: %+v", got.SERPs)
	}
}

func TestWorkflowWebResearchSERPCapRetainsAllQueryReportsAndCoverage(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("first query\nsecond query\nthird query\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "web-research", "serp",
		"--query-file", queryFile,
		"--max-candidates", "1",
		"--result-pages", "2",
		"--parallel", "1",
		"--out-dir", filepath.Join(tmpDir, "research"),
		"--wait", "250ms",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		SERPs      []json.RawMessage `json:"serps"`
		Candidates []json.RawMessage `json:"candidates"`
		Coverage   []struct {
			Query               string `json:"query"`
			ProducedCandidates  int    `json:"produced_candidates"`
			DuplicateCandidates int    `json:"duplicate_candidates"`
			SelectedCandidates  int    `json:"selected_candidates"`
			Productive          bool   `json:"productive"`
			Represented         bool   `json:"represented"`
		} `json:"query_coverage"`
		Workflow struct {
			QueryCount            int `json:"query_count"`
			ProductiveQueryCount  int `json:"productive_query_count"`
			RepresentedQueryCount int `json:"represented_query_count"`
			OmittedQueryCount     int `json:"omitted_query_count"`
			CompletedResultPages  int `json:"completed_result_pages"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
	}
	if len(got.SERPs) != 6 || got.Workflow.CompletedResultPages != 6 {
		t.Fatalf("SERP reports=%d workflow=%+v, want every fetched query report retained", len(got.SERPs), got.Workflow)
	}
	if len(got.Candidates) != 1 || len(got.Coverage) != 3 {
		t.Fatalf("candidates=%d coverage=%+v, want capped candidate plus every query", len(got.Candidates), got.Coverage)
	}
	if got.Workflow.QueryCount != 3 || got.Workflow.ProductiveQueryCount != 3 || got.Workflow.RepresentedQueryCount != 1 || got.Workflow.OmittedQueryCount != 2 {
		t.Fatalf("workflow coverage = %+v", got.Workflow)
	}
	if !got.Coverage[0].Represented || got.Coverage[0].SelectedCandidates != 1 || got.Coverage[0].ProducedCandidates != 1 || got.Coverage[0].DuplicateCandidates != 1 || got.Coverage[1].DuplicateCandidates != 2 || got.Coverage[2].DuplicateCandidates != 2 {
		t.Fatalf("query coverage = %+v, want same-query plus cross-query duplicate visibility", got.Coverage)
	}
}

func TestWorkflowWebResearchSERPSupportsMultipleEngines(t *testing.T) {
	engines := []string{"google", "bing", "brave", "duckduckgo", "kagi"}
	for _, engine := range engines {
		t.Run(engine, func(t *testing.T) {
			server := newFakeCDPServer(t, nil)
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			tmpDir := t.TempDir()
			queryFile := filepath.Join(tmpDir, "queries.txt")
			if err := os.WriteFile(queryFile, []byte("agentic engineering\n"), 0o600); err != nil {
				t.Fatalf("write query file: %v", err)
			}
			outDir := filepath.Join(tmpDir, "research")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--serp", engine, "--result-pages", "1", "--out-dir", outDir, "--wait", "250ms", "--settle", "0", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("workflow web-research serp exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
			}
			var got struct {
				OK    bool `json:"ok"`
				SERPs []struct {
					Serp string `json:"serp"`
				} `json:"serps"`
				Candidates []struct {
					Serp string `json:"serp"`
				} `json:"candidates"`
				Workflow struct {
					Serp string `json:"serp"`
				} `json:"workflow"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
			}
			if !got.OK || got.Workflow.Serp != engine || len(got.SERPs) != 1 || got.SERPs[0].Serp != engine || len(got.Candidates) != 1 || got.Candidates[0].Serp != engine {
				t.Fatalf("workflow web-research serp engine metadata = %+v", got)
			}
		})
	}
}

func TestWorkflowWebResearchSERPRunsMultipleEnginesInOneCommand(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	fakeTargetCreateCount.Store(0)

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("agentic engineering\nplaywright parity\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "research")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--serp", "google,bing", "--fallback-serp", "none", "--result-pages", "1", "--parallel", "3", "--out-dir", outDir, "--wait", "1s", "--settle", "0", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK    bool `json:"ok"`
		SERPs []struct {
			Serp string `json:"serp"`
		} `json:"serps"`
		Workflow struct {
			Serp                string   `json:"serp"`
			Serps               []string `json:"serps"`
			EngineCount         int      `json:"engine_count"`
			ParallelEngines     bool     `json:"parallel_engines"`
			ParallelEngineCount int      `json:"parallel_engine_count"`
			PerEngineParallel   int      `json:"per_engine_parallel"`
			EngineLanes         []struct {
				Serp        string `json:"serp"`
				PageReused  bool   `json:"page_reused"`
				CreatedPage bool   `json:"created_page"`
				JobCount    int    `json:"job_count"`
			} `json:"engine_lanes"`
			ScheduledResultPages int    `json:"scheduled_result_pages"`
			CompletedResultPages int    `json:"completed_result_pages"`
			FallbackSerp         string `json:"fallback_serp"`
			ResolvedFallbackSerp string `json:"resolved_fallback_serp"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Serp != "google,bing" || !reflect.DeepEqual(got.Workflow.Serps, []string{"google", "bing"}) || got.Workflow.EngineCount != 2 || !got.Workflow.ParallelEngines || got.Workflow.ParallelEngineCount != 2 || got.Workflow.PerEngineParallel != 1 {
		t.Fatalf("workflow multi-engine metadata = %+v", got.Workflow)
	}
	if got.Workflow.ScheduledResultPages != 4 || got.Workflow.CompletedResultPages != 4 || got.Workflow.FallbackSerp != "none" || got.Workflow.ResolvedFallbackSerp != "none" {
		t.Fatalf("workflow multi-engine counts/fallback = %+v", got.Workflow)
	}
	if len(got.Workflow.EngineLanes) != 2 || got.Workflow.EngineLanes[0].Serp != "google" || got.Workflow.EngineLanes[1].Serp != "bing" {
		t.Fatalf("workflow engine lanes = %+v, want deterministic one lane per engine", got.Workflow.EngineLanes)
	}
	for _, lane := range got.Workflow.EngineLanes {
		if !lane.PageReused || !lane.CreatedPage || lane.JobCount != 2 {
			t.Fatalf("workflow engine lane = %+v, want one created reusable page handling two jobs", lane)
		}
	}
	if creates := fakeTargetCreateCount.Load(); creates != 2 {
		t.Fatalf("Target.createTarget calls = %d, want one reusable page per engine", creates)
	}
	if len(got.SERPs) != 4 || got.SERPs[0].Serp != "google" || got.SERPs[1].Serp != "google" || got.SERPs[2].Serp != "bing" || got.SERPs[3].Serp != "bing" {
		t.Fatalf("serp reports = %+v, want deterministic google then bing order", got.SERPs)
	}
}

func TestWorkflowWebResearchSERPFastFailsBlockedPages(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("serp block fixture one\nserp block fixture two\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "research")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--result-pages", "3", "--fast-fail-blocked", "--blocked-failure-threshold", "2", "--progress", "stderr", "--parallel", "1", "--out-dir", outDir, "--wait", "250ms", "--settle", "0", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		SERPs []struct {
			Query    string `json:"query"`
			SerpPage int    `json:"serp_page"`
		} `json:"serps"`
		Failures []struct {
			ErrClass string `json:"err_class"`
		} `json:"failures"`
		Warnings []string `json:"warnings"`
		Workflow struct {
			FailureCount            int  `json:"failure_count"`
			ScheduledResultPages    int  `json:"scheduled_result_pages"`
			FastFailBlocked         bool `json:"fast_fail_blocked"`
			BlockedFailureThreshold int  `json:"blocked_failure_threshold"`
			FastFailTriggered       bool `json:"fast_fail_triggered"`
		} `json:"workflow"`
		Artifacts struct {
			CandidatesJSON string `json:"candidates_json"`
			CandidatesTSV  string `json:"candidates_tsv"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
	}
	if got.OK || len(got.SERPs) != 2 || len(got.Failures) != 2 || got.Workflow.FailureCount != 2 {
		t.Fatalf("workflow web-research fast-fail summary = %+v, want two blocked failures", got)
	}
	if !got.Workflow.FastFailBlocked || !got.Workflow.FastFailTriggered || got.Workflow.BlockedFailureThreshold != 2 || got.Workflow.ScheduledResultPages != 2 {
		t.Fatalf("workflow web-research fast-fail metadata = %+v, want early stop after two scheduled pages", got.Workflow)
	}
	for _, serp := range got.SERPs {
		if serp.Query != "serp block fixture one" {
			t.Fatalf("workflow web-research fast-fail scheduled query %q, want only first blocked query", serp.Query)
		}
	}
	for _, failure := range got.Failures {
		if failure.ErrClass != "serp_blocked" {
			t.Fatalf("failure = %+v, want serp_blocked", failure)
		}
	}
	if !testContainsSubstring(got.Warnings, "stopped early") {
		t.Fatalf("warnings = %+v, want fast-fail warning", got.Warnings)
	}
	progressLines := strings.Split(strings.TrimSpace(errOut.String()), "\n")
	if len(progressLines) != 2 {
		t.Fatalf("progress stderr = %q, want two JSONL events", errOut.String())
	}
	for _, line := range progressLines {
		var event struct {
			Event    string `json:"event"`
			Blocked  bool   `json:"blocked"`
			ErrClass string `json:"err_class"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("progress event %q is invalid JSON: %v", line, err)
		}
		if event.Event != "serp_page_complete" || !event.Blocked || event.ErrClass != "serp_blocked" {
			t.Fatalf("progress event = %+v, want blocked serp_page_complete", event)
		}
	}
	for _, path := range []string{got.Artifacts.CandidatesJSON, got.Artifacts.CandidatesTSV} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("workflow web-research serp artifact %q was not written: %v", path, err)
		}
	}
}

func TestWorkflowWebResearchSERPFallsBackAfterBlockedPrimary(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("duck only block fixture\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "research")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--serp", "duckduckgo", "--result-pages", "1", "--parallel", "1", "--out-dir", outDir, "--wait", "250ms", "--settle", "0", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		SERPs []struct {
			Serp string `json:"serp"`
		} `json:"serps"`
		Candidates []struct {
			Serp string `json:"serp"`
			URL  string `json:"url"`
		} `json:"candidates"`
		Failures []struct {
			Serp     string `json:"serp"`
			ErrClass string `json:"err_class"`
		} `json:"failures"`
		Warnings []string `json:"warnings"`
		Workflow struct {
			Serp                   string `json:"serp"`
			FallbackSerp           string `json:"fallback_serp"`
			ResolvedFallbackSerp   string `json:"resolved_fallback_serp"`
			FallbackTriggered      bool   `json:"fallback_triggered"`
			PrimaryCandidateCount  int    `json:"primary_candidate_count"`
			PrimaryFailureCount    int    `json:"primary_failure_count"`
			PrimaryBlockedFailures int    `json:"primary_blocked_failures"`
			CandidateCount         int    `json:"candidate_count"`
			FailureCount           int    `json:"failure_count"`
			ScheduledResultPages   int    `json:"scheduled_result_pages"`
			CompletedResultPages   int    `json:"completed_result_pages"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
	}
	if got.OK {
		t.Fatalf("workflow ok = true, want false because primary duckduckgo block is retained")
	}
	if got.Workflow.Serp != "duckduckgo" || got.Workflow.FallbackSerp != "auto" || got.Workflow.ResolvedFallbackSerp != "google" || !got.Workflow.FallbackTriggered {
		t.Fatalf("workflow fallback metadata = %+v", got.Workflow)
	}
	if got.Workflow.PrimaryCandidateCount != 0 || got.Workflow.PrimaryFailureCount != 1 || got.Workflow.PrimaryBlockedFailures != 1 || got.Workflow.CandidateCount != 1 || got.Workflow.FailureCount != 1 {
		t.Fatalf("workflow counts = %+v", got.Workflow)
	}
	if got.Workflow.ScheduledResultPages != 2 || got.Workflow.CompletedResultPages != 2 {
		t.Fatalf("workflow scheduled/completed pages = %+v, want primary and fallback pages", got.Workflow)
	}
	if len(got.SERPs) != 2 || got.SERPs[0].Serp != "duckduckgo" || got.SERPs[1].Serp != "google" {
		t.Fatalf("serp reports = %+v, want duckduckgo primary plus google fallback", got.SERPs)
	}
	if len(got.Failures) != 1 || got.Failures[0].Serp != "duckduckgo" || got.Failures[0].ErrClass != "serp_blocked" {
		t.Fatalf("failures = %+v, want retained duckduckgo serp_blocked failure", got.Failures)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].Serp != "google" || got.Candidates[0].URL == "" {
		t.Fatalf("candidates = %+v, want google fallback candidate", got.Candidates)
	}
	if !testContainsSubstring(got.Warnings, "running fallback SERP google") {
		t.Fatalf("warnings = %+v, want fallback warning", got.Warnings)
	}
}

func TestWorkflowWebResearchExtractJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	urlFile := filepath.Join(tmpDir, "urls.txt")
	if err := os.WriteFile(urlFile, []byte("https://example.test/story\nhttps://example.test/story#section\n"), 0o600); err != nil {
		t.Fatalf("write url file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "pages")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "extract", "--url-file", urlFile, "--max-pages", "1", "--parallel", "10", "--out-dir", outDir, "--wait", "250ms", "--settle", "0", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research extract exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Pages []struct {
			URL    string `json:"url"`
			Report struct {
				Artifacts struct {
					Markdown  string `json:"markdown"`
					LinksJSON string `json:"links_json"`
				} `json:"artifacts"`
				Workflow struct {
					Name string `json:"name"`
				} `json:"workflow"`
			} `json:"report"`
		} `json:"pages"`
		Quality []struct {
			URL      string   `json:"url"`
			Warnings []string `json:"warnings"`
		} `json:"quality"`
		Artifacts struct {
			PageQualityJSON string `json:"page_quality_json"`
			FailuresJSON    string `json:"failures_json"`
			FailedURLs      string `json:"failed_urls"`
			RemainingURLs   string `json:"remaining_urls"`
			RetryCommand    string `json:"retry_command"`
		} `json:"artifacts"`
		Workflow struct {
			Name         string `json:"name"`
			URLCount     int    `json:"url_count"`
			PageCount    int    `json:"page_count"`
			Parallel     int    `json:"parallel"`
			FailureCount int    `json:"failure_count"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research extract output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "web-research-extract" || got.Workflow.URLCount != 1 || got.Workflow.PageCount != 1 || got.Workflow.Parallel != 10 || got.Workflow.FailureCount != 0 {
		t.Fatalf("workflow web-research extract metadata = %+v", got.Workflow)
	}
	if len(got.Pages) != 1 || got.Pages[0].Report.Workflow.Name != "web-research-extract" || got.Pages[0].Report.Artifacts.Markdown == "" || got.Pages[0].Report.Artifacts.LinksJSON == "" {
		t.Fatalf("workflow web-research extract pages = %+v", got.Pages)
	}
	for _, path := range []string{got.Pages[0].Report.Artifacts.Markdown, got.Pages[0].Report.Artifacts.LinksJSON, got.Artifacts.PageQualityJSON, got.Artifacts.FailuresJSON, got.Artifacts.FailedURLs, got.Artifacts.RemainingURLs, got.Artifacts.RetryCommand} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("workflow web-research extract artifact %q was not written: %v", path, err)
		}
		if !strings.HasPrefix(path, outDir) {
			t.Fatalf("workflow web-research extract artifact %q, want under %q", path, outDir)
		}
	}
	if len(got.Quality) != 1 || len(got.Quality[0].Warnings) != 0 {
		t.Fatalf("workflow web-research extract quality = %+v", got.Quality)
	}
}

func TestWorkflowWebResearchExtractPropagatesContentExtractor(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	urlFile := filepath.Join(tmpDir, "urls.txt")
	if err := os.WriteFile(urlFile, []byte("https://news.ycombinator.com/item?id=46641042\n"), 0o600); err != nil {
		t.Fatalf("write url file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "pages")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"--browser-mode", "headed",
		"workflow", "web-research", "extract",
		"--url-file", urlFile,
		"--content-extractor", "auto",
		"--parallel", "1",
		"--out-dir", outDir,
		"--wait", "0",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("web-research content extractor exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		Pages []struct {
			Report struct {
				Content struct {
					Profile         string `json:"profile"`
					NativeSucceeded bool   `json:"native_succeeded"`
				} `json:"content"`
			} `json:"report"`
		} `json:"pages"`
		Artifacts struct {
			RetryCommand string `json:"retry_command"`
		} `json:"artifacts"`
		Workflow struct {
			ContentExtractor string `json:"content_extractor"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode web-research content extractor output: %v", err)
	}
	if len(got.Pages) != 1 || got.Pages[0].Report.Content.Profile != "hacker-news" || !got.Pages[0].Report.Content.NativeSucceeded {
		t.Fatalf("web-research content profile pages = %+v", got.Pages)
	}
	if got.Workflow.ContentExtractor != "auto" {
		t.Fatalf("web-research content extractor workflow = %+v", got.Workflow)
	}
	retryCommand, err := os.ReadFile(got.Artifacts.RetryCommand)
	if err != nil {
		t.Fatalf("read retry command: %v", err)
	}
	for _, want := range []string{"--browser-mode headed", "--content-extractor auto"} {
		if !strings.Contains(string(retryCommand), want) {
			t.Fatalf("retry command did not preserve %q:\n%s", want, string(retryCommand))
		}
	}
}

func TestWorkflowWebResearchExtractRejectsUnknownContentExtractor(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "web-research", "extract",
		"--url-file", filepath.Join(t.TempDir(), "urls.txt"),
		"--content-extractor", "native-only",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("unknown web-research content extractor exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
}

func TestWorkflowWebResearchExtractPreservesArtifactsForRetryableQualityFailure(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	urlFile := filepath.Join(tmpDir, "urls.txt")
	if err := os.WriteFile(urlFile, []byte("https://example.test/story\n"), 0o600); err != nil {
		t.Fatalf("write url file: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "web-research", "extract",
		"--url-file", urlFile,
		"--out-dir", filepath.Join(tmpDir, "pages"),
		"--parallel", "1",
		"--wait", "250ms",
		"--settle", "0",
		"--min-visible-words", "1000",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research extract exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Pages []struct {
			Report struct {
				Artifacts map[string]string `json:"artifacts"`
			} `json:"report"`
		} `json:"pages"`
		Quality []struct {
			Quality struct {
				Passed bool `json:"passed"`
			} `json:"quality"`
		} `json:"quality"`
		Failures []struct {
			URL       string `json:"url"`
			ErrClass  string `json:"err_class"`
			Retryable bool   `json:"retryable"`
		} `json:"failures"`
		Workflow struct {
			PageCount           int `json:"page_count"`
			FailureCount        int `json:"failure_count"`
			QualityFailureCount int `json:"quality_failure_count"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research extract output is invalid JSON: %v", err)
	}
	if got.OK || got.Workflow.PageCount != 1 || got.Workflow.FailureCount != 1 || got.Workflow.QualityFailureCount != 1 {
		t.Fatalf("quality-failure summary = %+v, ok=%v", got.Workflow, got.OK)
	}
	if len(got.Pages) != 1 || len(got.Pages[0].Report.Artifacts) == 0 || len(got.Quality) != 1 || got.Quality[0].Quality.Passed {
		t.Fatalf("captured page/quality = pages=%+v quality=%+v; artifacts must survive", got.Pages, got.Quality)
	}
	if len(got.Failures) != 1 || got.Failures[0].ErrClass != "quality_gate_failed" || !got.Failures[0].Retryable || got.Failures[0].URL == "" {
		t.Fatalf("failures = %+v, want retryable quality_gate_failed", got.Failures)
	}
}

func TestWorkflowWebResearchExtractClassifiesCaptureMismatchForCallerRetry(t *testing.T) {
	testWorkflowWebResearchExtractQualityFailure(t, webResearchExtractQualityFailureCase{
		fakeTargetFlag:      "fakeRenderedExtractChangesAfterReady",
		wait:                "250ms",
		settle:              "0",
		wantOutcome:         "settled",
		wantChecked:         true,
		wantReadinessPassed: true,
		warning:             "changed while capture",
	})
}

func TestWorkflowWebResearchExtractClassifiesUnavailableCaptureCheckForCallerRetry(t *testing.T) {
	testWorkflowWebResearchExtractQualityFailure(t, webResearchExtractQualityFailureCase{
		fakeTargetFlag:      "fakeRenderedExtractConsistencyUnavailable",
		wait:                "250ms",
		settle:              "0",
		wantOutcome:         "settled",
		wantReadinessPassed: true,
		warning:             "could not be verified",
		wantCollectorError:  true,
	})
}

func TestWorkflowWebResearchExtractClassifiesUnsettledReadinessForCallerRetry(t *testing.T) {
	testWorkflowWebResearchExtractQualityFailure(t, webResearchExtractQualityFailureCase{
		wait:                "100ms",
		settle:              "100ms",
		wantOutcome:         "wait_expired",
		wantChecked:         true,
		wantConsistent:      true,
		wantReadinessPassed: false,
		warning:             "deadline expired",
	})
}

type webResearchExtractQualityFailureCase struct {
	fakeTargetFlag      string
	wait                string
	settle              string
	wantOutcome         string
	wantChecked         bool
	wantConsistent      bool
	wantReadinessPassed bool
	warning             string
	wantCollectorError  bool
}

func testWorkflowWebResearchExtractQualityFailure(t *testing.T, testCase webResearchExtractQualityFailureCase) {
	t.Helper()
	target := map[string]any{
		"targetId": "page-1",
		"type":     "page",
		"title":    "Synthetic",
		"url":      "https://example.test/",
		"attached": false,
	}
	if testCase.fakeTargetFlag != "" {
		target[testCase.fakeTargetFlag] = true
	}
	server := newFakeCDPServer(t, []map[string]any{target})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	urlFile := filepath.Join(tmpDir, "urls.txt")
	if err := os.WriteFile(urlFile, []byte("https://example.test/story\n"), 0o600); err != nil {
		t.Fatalf("write url file: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "web-research", "extract",
		"--url-file", urlFile,
		"--out-dir", filepath.Join(tmpDir, "pages"),
		"--parallel", "1",
		"--wait", testCase.wait,
		"--settle", testCase.settle,
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research extract exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Pages []struct {
			Report struct {
				Readiness struct {
					Outcome                   string `json:"outcome"`
					CaptureConsistencyChecked bool   `json:"capture_consistency_checked"`
					CaptureConsistent         bool   `json:"capture_consistent"`
				} `json:"readiness"`
				Artifacts map[string]string `json:"artifacts"`
				Quality   struct {
					Passed                    bool `json:"passed"`
					ThresholdsPassed          bool `json:"thresholds_passed"`
					ReadinessPassed           bool `json:"readiness_passed"`
					CaptureConsistencyChecked bool `json:"capture_consistency_checked"`
					CaptureConsistent         bool `json:"capture_consistent"`
				} `json:"quality"`
				Warnings []string `json:"warnings"`
			} `json:"report"`
		} `json:"pages"`
		Failures []struct {
			URL       string            `json:"url"`
			ErrClass  string            `json:"err_class"`
			Retryable bool              `json:"retryable"`
			Artifacts map[string]string `json:"artifacts"`
		} `json:"failures"`
		Workflow struct {
			FailureCount          int  `json:"failure_count"`
			QualityFailureCount   int  `json:"quality_failure_count"`
			RetriedAfterReconnect bool `json:"retried_after_reconnect"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research extract output is invalid JSON: %v", err)
	}
	if len(got.Pages) != 1 {
		t.Fatalf("pages = %+v, want one captured page", got.Pages)
	}
	page := got.Pages[0].Report
	if page.Readiness.Outcome != testCase.wantOutcome ||
		page.Readiness.CaptureConsistencyChecked != testCase.wantChecked ||
		page.Readiness.CaptureConsistent != testCase.wantConsistent {
		t.Fatalf("readiness = %+v, want outcome=%q checked=%v consistent=%v", page.Readiness, testCase.wantOutcome, testCase.wantChecked, testCase.wantConsistent)
	}
	if page.Quality.Passed || !page.Quality.ThresholdsPassed ||
		page.Quality.ReadinessPassed != testCase.wantReadinessPassed ||
		page.Quality.CaptureConsistencyChecked != testCase.wantChecked ||
		page.Quality.CaptureConsistent != testCase.wantConsistent ||
		len(page.Artifacts) == 0 || page.Artifacts["diagnostics_json"] == "" {
		t.Fatalf("quality/artifacts = %+v/%+v, want failed quality with preserved artifacts", page.Quality, page.Artifacts)
	}
	if len(page.Warnings) == 0 || !strings.Contains(strings.Join(page.Warnings, "\n"), testCase.warning) {
		t.Fatalf("warnings = %v, want %q warning", page.Warnings, testCase.warning)
	}
	if got.OK || got.Workflow.FailureCount != 1 || got.Workflow.QualityFailureCount != 1 || got.Workflow.RetriedAfterReconnect {
		t.Fatalf("summary = %+v, ok=%v; capture consistency failure must be emitted without an internal retry", got.Workflow, got.OK)
	}
	if len(got.Failures) != 1 || got.Failures[0].URL != "https://example.test/story" ||
		got.Failures[0].ErrClass != "quality_gate_failed" || !got.Failures[0].Retryable ||
		len(got.Failures[0].Artifacts) == 0 {
		t.Fatalf("failures = %+v, want retryable quality_gate_failed", got.Failures)
	}

	diagnosticsPayload, err := os.ReadFile(page.Artifacts["diagnostics_json"])
	if err != nil {
		t.Fatalf("read diagnostics artifact: %v", err)
	}
	var diagnostics struct {
		Readiness struct {
			CaptureConsistencyChecked bool `json:"capture_consistency_checked"`
			CaptureConsistent         bool `json:"capture_consistent"`
		} `json:"readiness"`
		CollectorErrors []struct {
			Collector string `json:"collector"`
			Error     string `json:"error"`
		} `json:"collector_errors"`
	}
	if err := json.Unmarshal(diagnosticsPayload, &diagnostics); err != nil {
		t.Fatalf("diagnostics artifact is invalid JSON: %v", err)
	}
	if diagnostics.Readiness.CaptureConsistencyChecked != testCase.wantChecked ||
		diagnostics.Readiness.CaptureConsistent != testCase.wantConsistent {
		t.Fatalf("diagnostics readiness = %+v, want checked=%v consistent=%v", diagnostics.Readiness, testCase.wantChecked, testCase.wantConsistent)
	}
	hasConsistencyError := false
	for _, collectorError := range diagnostics.CollectorErrors {
		if collectorError.Collector == "capture_consistency" && collectorError.Error != "" {
			hasConsistencyError = true
		}
	}
	if hasConsistencyError != testCase.wantCollectorError {
		t.Fatalf("diagnostics collector errors = %+v, want capture-consistency error=%v", diagnostics.CollectorErrors, testCase.wantCollectorError)
	}
}

func TestWorkflowPerfJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "perf.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "perf", "https://example.test/app", "--wait", "250ms", "--trace", outPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow perf exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK          bool `json:"ok"`
		Performance struct {
			Metrics []struct {
				Name  string  `json:"name"`
				Value float64 `json:"value"`
			} `json:"metrics"`
		} `json:"performance"`
		Workflow struct {
			Name         string `json:"name"`
			RequestedURL string `json:"requested_url"`
			MetricCount  int    `json:"metric_count"`
			Partial      bool   `json:"partial"`
		} `json:"workflow"`
		Artifact struct {
			Type   string                   `json:"type"`
			Path   string                   `json:"path"`
			Safety artifacts.SafetyMetadata `json:"safety"`
		} `json:"artifact"`
		Trace struct {
			Stream struct {
				EOF            bool `json:"eof"`
				CloseAttempted bool `json:"close_attempted"`
				Closed         bool `json:"closed"`
			} `json:"stream"`
			ArtifactSafety artifacts.SafetyMetadata `json:"artifact_safety"`
		} `json:"trace"`
		Insights map[string]struct {
			Available bool    `json:"available"`
			ValueMS   float64 `json:"value_ms"`
			Value     float64 `json:"value"`
			Count     int     `json:"count"`
		} `json:"insights"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow perf output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "perf" || got.Workflow.RequestedURL != "https://example.test/app" {
		t.Fatalf("workflow perf = %+v, want complete perf workflow result", got)
	}
	if len(got.Performance.Metrics) != got.Workflow.MetricCount {
		t.Fatalf("workflow perf = %+v, want metric count to match performance.metrics", got)
	}
	if got.Workflow.MetricCount == 0 || got.Artifact.Path != outPath || got.Artifact.Type != "performance-trace" || got.Workflow.Partial {
		t.Fatalf("workflow perf = %+v, want captured performance metrics and trace artifact", got)
	}
	if !got.Trace.Stream.EOF || !got.Trace.Stream.CloseAttempted || !got.Trace.Stream.Closed || got.Trace.ArtifactSafety.RedactionMode != artifacts.ModeSafe || !got.Trace.ArtifactSafety.Shareable {
		t.Fatalf("workflow perf trace = %+v, want closed public-safe stream", got.Trace)
	}
	if !got.Insights["lcp"].Available || got.Insights["lcp"].ValueMS != 250 || !got.Insights["cls"].Available || got.Insights["cls"].Value != 0.125 || !got.Insights["long_tasks"].Available || got.Insights["long_tasks"].Count != 1 || !got.Insights["blocking_requests"].Available || got.Insights["blocking_requests"].Count != 1 {
		t.Fatalf("workflow perf insights = %+v, want trace-derived LCP/CLS/long-task/blocking request summaries", got.Insights)
	}
	traceBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("workflow perf artifact was not written: %v", err)
	}
	if scan := artifacts.ScanBytes(traceBytes, []string{"trace-secret", "token=trace-secret"}, 0); len(scan.Findings) != 0 {
		t.Fatalf("workflow perf trace leaked synthetic secrets: %+v", scan.Findings)
	}
}

func TestWorkflowVisiblePostsJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "visible-posts", "https://example.test/feed", "--wait", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow visible-posts exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Items []struct {
			Text string `json:"text"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow visible-posts output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Items) != 1 || got.Items[0].Text != "First visible synthetic post" {
		t.Fatalf("workflow visible-posts = %+v, want synthetic post", got)
	}
}

func TestWorkflowHackerNewsJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "hacker-news", "https://news.ycombinator.com/", "--wait", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow hacker-news exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK           bool              `json:"ok"`
		Organization map[string]string `json:"organization"`
		Stories      []struct {
			Rank     int    `json:"rank"`
			Title    string `json:"title"`
			Score    int    `json:"score"`
			Comments int    `json:"comments"`
		} `json:"stories"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow hacker-news output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Stories) != 1 || got.Stories[0].Title != "Synthetic HN story" || got.Stories[0].Score != 42 || got.Organization["story_row_selector"] != "tr.athing" {
		t.Fatalf("workflow hacker-news = %+v, want synthetic HN story and organization", got)
	}
}

func TestWorkflowHackerNewsHumanTable(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "hacker-news", "https://news.ycombinator.com/", "--wait", "0s"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow hacker-news exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	want := "rank  points  comments  title\n#1    42 pts 7 comments  Synthetic HN story\n"
	if out.String() != want {
		t.Fatalf("workflow hacker-news human output = %q, want %q", out.String(), want)
	}
}

func testContainsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
