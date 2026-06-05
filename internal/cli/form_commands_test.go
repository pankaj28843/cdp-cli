package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

type assertionStringDiffJSON struct {
	Mode            string `json:"mode"`
	Reason          string `json:"reason"`
	ExpectedLength  int    `json:"expected_length"`
	ActualLength    int    `json:"actual_length"`
	PrefixLength    int    `json:"prefix_length"`
	SuffixLength    int    `json:"suffix_length"`
	ExpectedSnippet string `json:"expected_snippet"`
	ActualSnippet   string `json:"actual_snippet"`
}

type assertionCountDiffJSON struct {
	Reason        string `json:"reason"`
	ExpectedCount int    `json:"expected_count"`
	ActualCount   int    `json:"actual_count"`
	Delta         int    `json:"delta"`
}

type assertionStateDiffJSON struct {
	Reason         string `json:"reason"`
	Expected       string `json:"expected"`
	Actual         string `json:"actual"`
	Count          int    `json:"count"`
	MatchingCount  int    `json:"matching_count"`
	FailingCount   int    `json:"failing_count"`
	ActiveSelector string `json:"active_selector"`
	ActiveRole     string `json:"active_role"`
	ActiveName     string `json:"active_name"`
}

func TestFormValuesAndSelectorAssertionsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	cases := []struct {
		name string
		args []string
	}{
		{"form get id", []string{"form", "get", "#out", "--json"}},
		{"form get aria", []string{"form", "get", `textarea[aria-label="Output"]`, "--json"}},
		{"form get input name", []string{"form", "get", "input[name=q]", "--json"}},
		{"assert value", []string{"assert", "value", "#out", "SGVsbG8gVVg=", "--json"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s exit code = %d, want %d; stdout=%s stderr=%s", tt.name, code, cli.ExitOK, out.String(), errOut.String())
			}
		})
	}
}

func TestAssertValueByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "value", "Search", "hello", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert value by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Tag          string `json:"tag"`
			} `json:"matches"`
		} `json:"locator"`
		Assertion struct {
			Selector string `json:"selector"`
			Expected string `json:"expected"`
			Actual   string `json:"actual"`
			Passed   bool   `json:"passed"`
			Control  struct {
				SelectorHint string `json:"selector_hint"`
				Name         string `json:"name"`
				Value        string `json:"value"`
			} `json:"control"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert value by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "input#q" || got.Locator.By != "label" || got.Locator.Query != "Search" || !got.Locator.Strict || len(got.Locator.Matches) != 1 {
		t.Fatalf("assert value locator = %+v, want strict label locator", got)
	}
	if got.Assertion.Selector != "input#q" || got.Assertion.Expected != "hello" || got.Assertion.Actual != "hello" || !got.Assertion.Passed || got.Assertion.Control.SelectorHint != "input#q" || got.Assertion.Control.Name != "Search" || got.Assertion.Control.Value != "hello" {
		t.Fatalf("assert value assertion = %+v, want assertion on resolved input", got.Assertion)
	}
}

func TestAssertValueRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertValueAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "value", "Delayed value", "ready", "--by", "label", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert value retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Assertion        struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Actual       string `json:"actual"`
			Mode         string `json:"mode"`
			Passed       bool   `json:"passed"`
			Count        int    `json:"count"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert value retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "input#delayed-value" || got.Assertion.Selector != "input#delayed-value" || got.Assertion.Expected != "ready" || got.Assertion.Actual != "ready" || got.Assertion.Mode != "exact" || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert value retry = %+v, want retried value assertion with timing evidence", got)
	}
}

func TestAssertValueTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "value", "Search", "never", "--by", "label", "--timeout", "120ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert value timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Selector     string                   `json:"selector"`
				Expected     string                   `json:"expected"`
				Actual       string                   `json:"actual"`
				Mode         string                   `json:"mode"`
				Diff         *assertionStringDiffJSON `json:"diff"`
				Passed       bool                     `json:"passed"`
				Count        int                      `json:"count"`
				Attempts     int                      `json:"attempts"`
				ElapsedMS    int64                    `json:"elapsed_ms"`
				PollInterval string                   `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert value timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "input#q" || got.Data.Assertion.Selector != "input#q" || got.Data.Assertion.Expected != "never" || got.Data.Assertion.Actual != "hello" || got.Data.Assertion.Mode != "exact" || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Mode != "exact" || got.Data.Assertion.Diff.Reason != "different" || got.Data.Assertion.Diff.ExpectedLength != 5 || got.Data.Assertion.Diff.ActualLength != 5 || got.Data.Assertion.Diff.PrefixLength != 0 || got.Data.Assertion.Diff.SuffixLength != 0 || got.Data.Assertion.Diff.ExpectedSnippet != "never" || got.Data.Assertion.Diff.ActualSnippet != "hello" || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || !containsString(got.RemediationCommands, "cdp form get 'input#q' --json") {
		t.Fatalf("assert value timeout = %+v, want timeout with last value diagnostics", got)
	}
}

func TestAssertTextBodyCompatibilityJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "text", "Synthetic main text", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert text body exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Assertion struct {
			Selector string `json:"selector"`
			Expected string `json:"expected"`
			Actual   string `json:"actual"`
			Mode     string `json:"mode"`
			Passed   bool   `json:"passed"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert text body output is invalid JSON: %v", err)
	}
	if !got.OK || got.Assertion.Selector != "body" || got.Assertion.Expected != "Synthetic main text" || got.Assertion.Actual != "Synthetic main text" || got.Assertion.Mode != "contains" || !got.Assertion.Passed {
		t.Fatalf("assert text body = %+v, want body compatibility assertion", got)
	}
}

func TestAssertTextByRoleLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "text", "Search", "Search button", "--by", "role", "--role", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert text by role exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Role    string `json:"role"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Role         string `json:"role"`
			} `json:"matches"`
		} `json:"locator"`
		Assertion struct {
			Selector string `json:"selector"`
			Expected string `json:"expected"`
			Actual   string `json:"actual"`
			Mode     string `json:"mode"`
			Passed   bool   `json:"passed"`
			Count    int    `json:"count"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert text by role output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#submit" || got.Locator.By != "role" || got.Locator.Query != "Search" || got.Locator.Role != "button" || !got.Locator.Strict || len(got.Locator.Matches) != 1 {
		t.Fatalf("assert text locator = %+v, want strict role locator", got)
	}
	if got.Assertion.Selector != "button#submit" || got.Assertion.Expected != "Search button" || got.Assertion.Actual != "Search button" || got.Assertion.Mode != "contains" || !got.Assertion.Passed || got.Assertion.Count != 1 {
		t.Fatalf("assert text assertion = %+v, want assertion on resolved button text", got.Assertion)
	}
}

func TestAssertTextRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertTextAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "text", "Delayed text", "Ready text", "--by", "role", "--role", "button", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert text retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Assertion        struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Actual       string `json:"actual"`
			Mode         string `json:"mode"`
			Passed       bool   `json:"passed"`
			Count        int    `json:"count"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert text retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#delayed-text" || got.Assertion.Selector != "button#delayed-text" || got.Assertion.Expected != "Ready text" || got.Assertion.Actual != "Ready text" || got.Assertion.Mode != "contains" || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert text retry = %+v, want retried text assertion with timing evidence", got)
	}
}

func TestAssertTextTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "text", "Search", "Never text", "--by", "role", "--role", "button", "--timeout", "120ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert text timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Selector     string                   `json:"selector"`
				Expected     string                   `json:"expected"`
				Actual       string                   `json:"actual"`
				Mode         string                   `json:"mode"`
				Diff         *assertionStringDiffJSON `json:"diff"`
				Passed       bool                     `json:"passed"`
				Count        int                      `json:"count"`
				Attempts     int                      `json:"attempts"`
				ElapsedMS    int64                    `json:"elapsed_ms"`
				PollInterval string                   `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert text timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "button#submit" || got.Data.Assertion.Selector != "button#submit" || got.Data.Assertion.Expected != "Never text" || got.Data.Assertion.Actual != "Search button" || got.Data.Assertion.Mode != "contains" || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Mode != "contains" || got.Data.Assertion.Diff.Reason != "missing_substring" || got.Data.Assertion.Diff.ExpectedLength != 10 || got.Data.Assertion.Diff.ActualLength != 13 || got.Data.Assertion.Diff.ExpectedSnippet != "Never text" || got.Data.Assertion.Diff.ActualSnippet != "Search button" || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || !containsString(got.RemediationCommands, "cdp text 'button#submit' --limit 0 --json") {
		t.Fatalf("assert text timeout = %+v, want timeout with last text diagnostics", got)
	}
}

func TestAssertURLRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertPageAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/loading", "title": "Loading"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "url", "ready", "--mode", "contains", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert url retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"target"`
		Assertion struct {
			Field        string `json:"field"`
			Expected     string `json:"expected"`
			Actual       string `json:"actual"`
			Mode         string `json:"mode"`
			Passed       bool   `json:"passed"`
			URL          string `json:"url"`
			Title        string `json:"title"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert url retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.URL != "https://example.test/ready" || got.Target.Title != "Ready Page" || got.Assertion.Field != "url" || got.Assertion.Expected != "ready" || got.Assertion.Actual != "https://example.test/ready" || got.Assertion.Mode != "contains" || !got.Assertion.Passed || got.Assertion.URL != "https://example.test/ready" || got.Assertion.Title != "Ready Page" || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert url retry = %+v, want retried URL assertion with final page evidence", got)
	}
}

func TestAssertTitleRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertPageAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/loading", "title": "Loading"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "title", "Ready Page", "--mode", "exact", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert title retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"target"`
		Assertion struct {
			Field        string `json:"field"`
			Expected     string `json:"expected"`
			Actual       string `json:"actual"`
			Mode         string `json:"mode"`
			Passed       bool   `json:"passed"`
			URL          string `json:"url"`
			Title        string `json:"title"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert title retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.URL != "https://example.test/ready" || got.Target.Title != "Ready Page" || got.Assertion.Field != "title" || got.Assertion.Expected != "Ready Page" || got.Assertion.Actual != "Ready Page" || got.Assertion.Mode != "exact" || !got.Assertion.Passed || got.Assertion.URL != "https://example.test/ready" || got.Assertion.Title != "Ready Page" || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert title retry = %+v, want retried title assertion with final page evidence", got)
	}
}

func TestAssertURLTimeoutJSON(t *testing.T) {
	fakeDelayedAssertPageAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/loading", "title": "Loading"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "url", "missing", "--mode", "contains", "--timeout", "120ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert url timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Target struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"target"`
			Assertion struct {
				Field        string                   `json:"field"`
				Expected     string                   `json:"expected"`
				Actual       string                   `json:"actual"`
				Mode         string                   `json:"mode"`
				Diff         *assertionStringDiffJSON `json:"diff"`
				Passed       bool                     `json:"passed"`
				URL          string                   `json:"url"`
				Title        string                   `json:"title"`
				Attempts     int                      `json:"attempts"`
				ElapsedMS    int64                    `json:"elapsed_ms"`
				PollInterval string                   `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert url timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Target.URL != "https://example.test/ready" || got.Data.Target.Title != "Ready Page" || got.Data.Assertion.Field != "url" || got.Data.Assertion.Expected != "missing" || got.Data.Assertion.Actual != "https://example.test/ready" || got.Data.Assertion.Mode != "contains" || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Mode != "contains" || got.Data.Assertion.Diff.Reason != "missing_substring" || got.Data.Assertion.Diff.ExpectedLength != 7 || got.Data.Assertion.Diff.ActualLength != 26 || got.Data.Assertion.Diff.ExpectedSnippet != "missing" || got.Data.Assertion.Diff.ActualSnippet != "https://example.test/ready" || got.Data.Assertion.Passed || got.Data.Assertion.URL != "https://example.test/ready" || got.Data.Assertion.Title != "Ready Page" || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || !containsString(got.RemediationCommands, "cdp pages --json") || !containsString(got.RemediationCommands, "cdp assert url missing --mode contains --json") {
		t.Fatalf("assert url timeout = %+v, want timeout with last page diagnostics", got)
	}
}

func TestAssertTitleTimeoutJSON(t *testing.T) {
	fakeDelayedAssertPageAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/loading", "title": "Loading"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "title", "Never Ready", "--mode", "exact", "--timeout", "120ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert title timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Target struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"target"`
			Assertion struct {
				Field        string                   `json:"field"`
				Expected     string                   `json:"expected"`
				Actual       string                   `json:"actual"`
				Mode         string                   `json:"mode"`
				Diff         *assertionStringDiffJSON `json:"diff"`
				Passed       bool                     `json:"passed"`
				URL          string                   `json:"url"`
				Title        string                   `json:"title"`
				Attempts     int                      `json:"attempts"`
				ElapsedMS    int64                    `json:"elapsed_ms"`
				PollInterval string                   `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert title timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Target.URL != "https://example.test/ready" || got.Data.Target.Title != "Ready Page" || got.Data.Assertion.Field != "title" || got.Data.Assertion.Expected != "Never Ready" || got.Data.Assertion.Actual != "Ready Page" || got.Data.Assertion.Mode != "exact" || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Mode != "exact" || got.Data.Assertion.Diff.Reason != "different" || got.Data.Assertion.Diff.ExpectedLength != 11 || got.Data.Assertion.Diff.ActualLength != 10 || got.Data.Assertion.Diff.ExpectedSnippet != "Never Ready" || got.Data.Assertion.Diff.ActualSnippet != "Ready Page" || got.Data.Assertion.Passed || got.Data.Assertion.URL != "https://example.test/ready" || got.Data.Assertion.Title != "Ready Page" || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || !containsString(got.RemediationCommands, "cdp pages --json") || !containsString(got.RemediationCommands, "cdp assert title 'Never Ready' --mode exact --json") {
		t.Fatalf("assert title timeout = %+v, want timeout with last page diagnostics", got)
	}
}

func TestAssertCountCSSRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertCountAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "count", ".cart-item", "3", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert count css retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Assertion struct {
			Selector     string `json:"selector"`
			Expected     int    `json:"expected"`
			Actual       int    `json:"actual"`
			Passed       bool   `json:"passed"`
			Count        int    `json:"count"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
			Items        []struct {
				Tag string `json:"tag"`
				ID  string `json:"id"`
			} `json:"items"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert count css retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.Assertion.Selector != ".cart-item" || got.Assertion.Expected != 3 || got.Assertion.Actual != 3 || got.Assertion.Count != 3 || !got.Assertion.Passed || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" || len(got.Assertion.Items) != 3 || got.Assertion.Items[0].Tag != "li" || got.Assertion.Items[0].ID != "cart-item-1" {
		t.Fatalf("assert count css retry = %+v, want retried count assertion with item diagnostics", got)
	}
}

func TestAssertCountLocatorRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertCountAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "count", "Cart item", "3", "--by", "role", "--role", "listitem", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert count locator retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK      bool `json:"ok"`
		Locator struct {
			By       string `json:"by"`
			Query    string `json:"query"`
			Role     string `json:"role"`
			Count    int    `json:"count"`
			Returned int    `json:"returned"`
			Strict   bool   `json:"strict"`
		} `json:"locator"`
		Assertion struct {
			Query        string `json:"query"`
			Expected     int    `json:"expected"`
			Actual       int    `json:"actual"`
			Passed       bool   `json:"passed"`
			Count        int    `json:"count"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert count locator retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.Locator.By != "role" || got.Locator.Query != "Cart item" || got.Locator.Role != "listitem" || got.Locator.Count != 3 || got.Locator.Returned != 3 || got.Locator.Strict || got.Assertion.Query != "Cart item" || got.Assertion.Expected != 3 || got.Assertion.Actual != 3 || got.Assertion.Count != 3 || !got.Assertion.Passed || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert count locator retry = %+v, want retried locator count assertion with locator diagnostics", got)
	}
}

func TestAssertCountTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "count", ".cart-item", "5", "--timeout", "120ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert count timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Assertion struct {
				Selector     string                  `json:"selector"`
				Expected     int                     `json:"expected"`
				Actual       int                     `json:"actual"`
				Diff         *assertionCountDiffJSON `json:"diff"`
				Passed       bool                    `json:"passed"`
				Count        int                     `json:"count"`
				Attempts     int                     `json:"attempts"`
				ElapsedMS    int64                   `json:"elapsed_ms"`
				PollInterval string                  `json:"poll_interval"`
				Items        []struct {
					Tag string `json:"tag"`
					ID  string `json:"id"`
				} `json:"items"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert count timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Assertion.Selector != ".cart-item" || got.Data.Assertion.Expected != 5 || got.Data.Assertion.Actual != 3 || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Reason != "too_few" || got.Data.Assertion.Diff.ExpectedCount != 5 || got.Data.Assertion.Diff.ActualCount != 3 || got.Data.Assertion.Diff.Delta != -2 || got.Data.Assertion.Passed || got.Data.Assertion.Count != 3 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || len(got.Data.Assertion.Items) != 3 || got.Data.Assertion.Items[0].Tag != "li" || got.Data.Assertion.Items[0].ID != "cart-item-1" || !containsString(got.RemediationCommands, "cdp dom query .cart-item --json") || !containsString(got.RemediationCommands, "cdp assert count .cart-item 5 --json") {
		t.Fatalf("assert count timeout = %+v, want timeout with count diff diagnostics", got)
	}
}

func TestAssertAttributeByRoleRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertAttributeAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "attribute", "Checkout", "data-state", "ready", "--by", "role", "--role", "button", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert attribute role retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By     string `json:"by"`
			Query  string `json:"query"`
			Role   string `json:"role"`
			Strict bool   `json:"strict"`
		} `json:"locator"`
		Assertion struct {
			Selector         string `json:"selector"`
			Attribute        string `json:"attribute"`
			AttributePresent bool   `json:"attribute_present"`
			Expected         string `json:"expected"`
			Actual           string `json:"actual"`
			Mode             string `json:"mode"`
			Passed           bool   `json:"passed"`
			Count            int    `json:"count"`
			Attempts         int    `json:"attempts"`
			ElapsedMS        int64  `json:"elapsed_ms"`
			PollInterval     string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert attribute role retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#checkout" || got.Locator.By != "role" || got.Locator.Query != "Checkout" || got.Locator.Role != "button" || !got.Locator.Strict || got.Assertion.Selector != "button#checkout" || got.Assertion.Attribute != "data-state" || !got.Assertion.AttributePresent || got.Assertion.Expected != "ready" || got.Assertion.Actual != "ready" || got.Assertion.Mode != "exact" || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert attribute role retry = %+v, want retried strict locator attribute assertion", got)
	}
}

func TestAssertAttributeTimeoutJSON(t *testing.T) {
	fakeDelayedAssertAttributeAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "attribute", "button#checkout", "data-state", "never", "--timeout", "120ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert attribute timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Assertion struct {
				Selector         string                   `json:"selector"`
				Attribute        string                   `json:"attribute"`
				AttributePresent bool                     `json:"attribute_present"`
				Expected         string                   `json:"expected"`
				Actual           string                   `json:"actual"`
				Mode             string                   `json:"mode"`
				Diff             *assertionStringDiffJSON `json:"diff"`
				Passed           bool                     `json:"passed"`
				Count            int                      `json:"count"`
				Attempts         int                      `json:"attempts"`
				ElapsedMS        int64                    `json:"elapsed_ms"`
				PollInterval     string                   `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert attribute timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Assertion.Selector != "button#checkout" || got.Data.Assertion.Attribute != "data-state" || !got.Data.Assertion.AttributePresent || got.Data.Assertion.Expected != "never" || got.Data.Assertion.Actual != "ready" || got.Data.Assertion.Mode != "exact" || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Mode != "exact" || got.Data.Assertion.Diff.Reason != "different" || got.Data.Assertion.Diff.ExpectedLength != 5 || got.Data.Assertion.Diff.ActualLength != 5 || got.Data.Assertion.Diff.ExpectedSnippet != "never" || got.Data.Assertion.Diff.ActualSnippet != "ready" || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || !containsString(got.RemediationCommands, "cdp dom query 'button#checkout' --json") || !containsString(got.RemediationCommands, "cdp assert attribute 'button#checkout' data-state never --mode exact --json") {
		t.Fatalf("assert attribute timeout = %+v, want timeout with last attribute diagnostics", got)
	}
}

func TestAssertFocusedByLabelRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertFocusedAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "focused", "Search", "--by", "label", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert focused label retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By     string `json:"by"`
			Query  string `json:"query"`
			Strict bool   `json:"strict"`
		} `json:"locator"`
		Assertion struct {
			Selector       string `json:"selector"`
			Expected       string `json:"expected"`
			Focused        bool   `json:"focused"`
			Passed         bool   `json:"passed"`
			Count          int    `json:"count"`
			FocusedCount   int    `json:"focused_count"`
			ActiveSelector string `json:"active_selector"`
			ActiveTag      string `json:"active_tag"`
			ActiveID       string `json:"active_id"`
			ActiveRole     string `json:"active_role"`
			ActiveName     string `json:"active_name"`
			Attempts       int    `json:"attempts"`
			ElapsedMS      int64  `json:"elapsed_ms"`
			PollInterval   string `json:"poll_interval"`
			Items          []struct {
				Tag     string `json:"tag"`
				ID      string `json:"id"`
				Role    string `json:"role"`
				Focused bool   `json:"focused"`
			} `json:"items"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert focused label retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "input#q" || got.Locator.By != "label" || got.Locator.Query != "Search" || !got.Locator.Strict || got.Assertion.Selector != "input#q" || got.Assertion.Expected != "focused" || !got.Assertion.Focused || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.FocusedCount != 1 || got.Assertion.ActiveSelector != "input#q" || got.Assertion.ActiveTag != "input" || got.Assertion.ActiveID != "q" || got.Assertion.ActiveRole != "searchbox" || got.Assertion.ActiveName != "Search" || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" || len(got.Assertion.Items) != 1 || got.Assertion.Items[0].Tag != "input" || got.Assertion.Items[0].ID != "q" || got.Assertion.Items[0].Role != "searchbox" || !got.Assertion.Items[0].Focused {
		t.Fatalf("assert focused label retry = %+v, want strict locator focused diagnostics", got)
	}
}

func TestAssertFocusedTimeoutJSON(t *testing.T) {
	fakeDelayedAssertFocusedAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "focused", "input#never-focused", "--timeout", "120ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert focused timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Assertion struct {
				Selector       string                  `json:"selector"`
				Focused        bool                    `json:"focused"`
				Diff           *assertionStateDiffJSON `json:"diff"`
				Passed         bool                    `json:"passed"`
				Count          int                     `json:"count"`
				FocusedCount   int                     `json:"focused_count"`
				ActiveSelector string                  `json:"active_selector"`
				Attempts       int                     `json:"attempts"`
				ElapsedMS      int64                   `json:"elapsed_ms"`
				PollInterval   string                  `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert focused timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Assertion.Selector != "input#never-focused" || got.Data.Assertion.Focused || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Reason != "state_mismatch" || got.Data.Assertion.Diff.Expected != "focused" || got.Data.Assertion.Diff.Actual != "not_focused" || got.Data.Assertion.Diff.Count != 1 || got.Data.Assertion.Diff.MatchingCount != 0 || got.Data.Assertion.Diff.FailingCount != 1 || got.Data.Assertion.Diff.ActiveSelector != "body" || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.FocusedCount != 0 || got.Data.Assertion.ActiveSelector != "body" || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || !containsString(got.RemediationCommands, "cdp dom query 'input#never-focused' --json") || !containsString(got.RemediationCommands, "cdp assert focused 'input#never-focused' --json") {
		t.Fatalf("assert focused timeout = %+v, want timeout with last focus diagnostics", got)
	}
}

func TestAssertCSSByRoleRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertCSSAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "css", "Checkout", "background-color", "rgb(20, 92, 160)", "--by", "role", "--role", "button", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert css role retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By     string `json:"by"`
			Query  string `json:"query"`
			Role   string `json:"role"`
			Strict bool   `json:"strict"`
		} `json:"locator"`
		Assertion struct {
			Selector     string `json:"selector"`
			Property     string `json:"property"`
			Expected     string `json:"expected"`
			Actual       string `json:"actual"`
			Mode         string `json:"mode"`
			Passed       bool   `json:"passed"`
			Count        int    `json:"count"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert css role retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#checkout" || got.Locator.By != "role" || got.Locator.Query != "Checkout" || got.Locator.Role != "button" || !got.Locator.Strict || got.Assertion.Selector != "button#checkout" || got.Assertion.Property != "background-color" || got.Assertion.Expected != "rgb(20, 92, 160)" || got.Assertion.Actual != "rgb(20, 92, 160)" || got.Assertion.Mode != "exact" || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert css role retry = %+v, want retried strict locator CSS assertion", got)
	}
}

func TestAssertCSSTimeoutJSON(t *testing.T) {
	fakeDelayedAssertCSSAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "css", "button#checkout", "background-color", "rgb(1, 2, 3)", "--timeout", "120ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert css timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Assertion struct {
				Selector     string                   `json:"selector"`
				Property     string                   `json:"property"`
				Expected     string                   `json:"expected"`
				Actual       string                   `json:"actual"`
				Mode         string                   `json:"mode"`
				Diff         *assertionStringDiffJSON `json:"diff"`
				Passed       bool                     `json:"passed"`
				Count        int                      `json:"count"`
				Attempts     int                      `json:"attempts"`
				ElapsedMS    int64                    `json:"elapsed_ms"`
				PollInterval string                   `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert css timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Assertion.Selector != "button#checkout" || got.Data.Assertion.Property != "background-color" || got.Data.Assertion.Expected != "rgb(1, 2, 3)" || got.Data.Assertion.Actual != "rgb(20, 92, 160)" || got.Data.Assertion.Mode != "exact" || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Mode != "exact" || got.Data.Assertion.Diff.Reason != "different" || got.Data.Assertion.Diff.ExpectedLength != 12 || got.Data.Assertion.Diff.ActualLength != 16 || got.Data.Assertion.Diff.PrefixLength != 4 || got.Data.Assertion.Diff.SuffixLength != 1 || got.Data.Assertion.Diff.ExpectedSnippet != "rgb(1, 2, 3)" || got.Data.Assertion.Diff.ActualSnippet != "rgb(20, 92, 160)" || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || !containsString(got.RemediationCommands, "cdp css inspect 'button#checkout' --json") || !containsString(got.RemediationCommands, "cdp assert css 'button#checkout' background-color 'rgb(1, 2, 3)' --mode exact --json") {
		t.Fatalf("assert css timeout = %+v, want timeout with last CSS diagnostics", got)
	}
}

func TestAssertRoleByTextRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertRoleAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "role", "Delayed role", "button", "--by", "text", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert role retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Role         string `json:"role"`
				Name         string `json:"name"`
			} `json:"matches"`
		} `json:"locator"`
		Assertion struct {
			Query        string `json:"query"`
			Selector     string `json:"selector"`
			Field        string `json:"field"`
			Expected     string `json:"expected"`
			Actual       string `json:"actual"`
			Mode         string `json:"mode"`
			Passed       bool   `json:"passed"`
			Count        int    `json:"count"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert role retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#delayed-role" || got.Locator.By != "text" || got.Locator.Query != "Delayed role" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || got.Locator.Matches[0].Role != "button" || got.Assertion.Query != "Delayed role" || got.Assertion.Selector != "button#delayed-role" || got.Assertion.Field != "role" || got.Assertion.Expected != "button" || got.Assertion.Actual != "button" || got.Assertion.Mode != "exact" || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert role retry = %+v, want retried accessible role assertion", got)
	}
}

func TestAssertNameByRoleRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertNameAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "name", "Delayed name", "Ready name", "--by", "role", "--role", "button", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert name retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Role    string `json:"role"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Role         string `json:"role"`
				Name         string `json:"name"`
			} `json:"matches"`
		} `json:"locator"`
		Assertion struct {
			Query        string `json:"query"`
			Selector     string `json:"selector"`
			Field        string `json:"field"`
			Expected     string `json:"expected"`
			Actual       string `json:"actual"`
			Mode         string `json:"mode"`
			Passed       bool   `json:"passed"`
			Count        int    `json:"count"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert name retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#delayed-name" || got.Locator.By != "role" || got.Locator.Query != "Delayed name" || got.Locator.Role != "button" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || got.Locator.Matches[0].Name != "Ready name" || got.Assertion.Query != "Delayed name" || got.Assertion.Selector != "button#delayed-name" || got.Assertion.Field != "name" || got.Assertion.Expected != "Ready name" || got.Assertion.Actual != "Ready name" || got.Assertion.Mode != "exact" || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert name retry = %+v, want retried accessible name assertion", got)
	}
}

func TestAssertRoleTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "role", "Checkout", "link", "--by", "role", "--role", "button", "--timeout", "120ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert role timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Query        string                   `json:"query"`
				Selector     string                   `json:"selector"`
				Field        string                   `json:"field"`
				Expected     string                   `json:"expected"`
				Actual       string                   `json:"actual"`
				Mode         string                   `json:"mode"`
				Diff         *assertionStringDiffJSON `json:"diff"`
				Passed       bool                     `json:"passed"`
				Count        int                      `json:"count"`
				Attempts     int                      `json:"attempts"`
				ElapsedMS    int64                    `json:"elapsed_ms"`
				PollInterval string                   `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert role timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "button#checkout" || got.Data.Assertion.Query != "Checkout" || got.Data.Assertion.Selector != "button#checkout" || got.Data.Assertion.Field != "role" || got.Data.Assertion.Expected != "link" || got.Data.Assertion.Actual != "button" || got.Data.Assertion.Mode != "exact" || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Mode != "exact" || got.Data.Assertion.Diff.Reason != "different" || got.Data.Assertion.Diff.ExpectedLength != 4 || got.Data.Assertion.Diff.ActualLength != 6 || got.Data.Assertion.Diff.ExpectedSnippet != "link" || got.Data.Assertion.Diff.ActualSnippet != "button" || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || !containsString(got.RemediationCommands, "cdp locator find Checkout --by role --role button --json") || !containsString(got.RemediationCommands, "cdp a11y node 'button#checkout' --json") || !containsString(got.RemediationCommands, "cdp assert role Checkout link --mode exact --by role --role button --json") {
		t.Fatalf("assert role timeout = %+v, want timeout with last accessible role diagnostics", got)
	}
}

func TestAssertNameTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "name", "Checkout", "Submit", "--by", "role", "--role", "button", "--timeout", "120ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert name timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Query        string                   `json:"query"`
				Selector     string                   `json:"selector"`
				Field        string                   `json:"field"`
				Expected     string                   `json:"expected"`
				Actual       string                   `json:"actual"`
				Mode         string                   `json:"mode"`
				Diff         *assertionStringDiffJSON `json:"diff"`
				Passed       bool                     `json:"passed"`
				Count        int                      `json:"count"`
				Attempts     int                      `json:"attempts"`
				ElapsedMS    int64                    `json:"elapsed_ms"`
				PollInterval string                   `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert name timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "button#checkout" || got.Data.Assertion.Query != "Checkout" || got.Data.Assertion.Selector != "button#checkout" || got.Data.Assertion.Field != "name" || got.Data.Assertion.Expected != "Submit" || got.Data.Assertion.Actual != "Checkout" || got.Data.Assertion.Mode != "exact" || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Mode != "exact" || got.Data.Assertion.Diff.Reason != "different" || got.Data.Assertion.Diff.ExpectedLength != 6 || got.Data.Assertion.Diff.ActualLength != 8 || got.Data.Assertion.Diff.ExpectedSnippet != "Submit" || got.Data.Assertion.Diff.ActualSnippet != "Checkout" || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || !containsString(got.RemediationCommands, "cdp locator find Checkout --by role --role button --json") || !containsString(got.RemediationCommands, "cdp a11y node 'button#checkout' --json") || !containsString(got.RemediationCommands, "cdp assert name Checkout Submit --mode exact --by role --role button --json") {
		t.Fatalf("assert name timeout = %+v, want timeout with last accessible name diagnostics", got)
	}
}

func TestAssertIndeterminateByLabelJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "indeterminate", "Partial selection", "--by", "label", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert indeterminate by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Role         string `json:"role"`
				Name         string `json:"name"`
			} `json:"matches"`
		} `json:"locator"`
		Assertion struct {
			Selector           string `json:"selector"`
			Expected           string `json:"expected"`
			Checked            bool   `json:"checked"`
			Unchecked          bool   `json:"unchecked"`
			Indeterminate      bool   `json:"indeterminate"`
			Passed             bool   `json:"passed"`
			Count              int    `json:"count"`
			CheckedCount       int    `json:"checked_count"`
			UncheckedCount     int    `json:"unchecked_count"`
			IndeterminateCount int    `json:"indeterminate_count"`
			UnsupportedCount   int    `json:"unsupported_count"`
			Attempts           int    `json:"attempts"`
			ElapsedMS          int64  `json:"elapsed_ms"`
			PollInterval       string `json:"poll_interval"`
			Items              []struct {
				Role          string `json:"role"`
				Name          string `json:"name"`
				Checked       bool   `json:"checked"`
				Indeterminate bool   `json:"indeterminate"`
			} `json:"items"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert indeterminate output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "input#partial-selection" || got.Locator.By != "label" || got.Locator.Query != "Partial selection" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || got.Assertion.Selector != "input#partial-selection" || got.Assertion.Expected != "indeterminate" || got.Assertion.Checked || got.Assertion.Unchecked || !got.Assertion.Indeterminate || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.CheckedCount != 0 || got.Assertion.UncheckedCount != 0 || got.Assertion.IndeterminateCount != 1 || got.Assertion.UnsupportedCount != 0 || got.Assertion.Attempts != 1 || got.Assertion.PollInterval != "10ms" || len(got.Assertion.Items) != 1 || !got.Assertion.Items[0].Indeterminate || got.Assertion.Items[0].Checked {
		t.Fatalf("assert indeterminate = %+v, want indeterminate checkbox diagnostics", got)
	}
}

func TestAssertVisibleByRoleLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "visible", "Search", "--by", "role", "--role", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert visible by role exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By            string `json:"by"`
			Query         string `json:"query"`
			Role          string `json:"role"`
			IncludeHidden bool   `json:"include_hidden"`
			Strict        bool   `json:"strict"`
		} `json:"locator"`
		Assertion struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Visible      bool   `json:"visible"`
			Passed       bool   `json:"passed"`
			Count        int    `json:"count"`
			VisibleCount int    `json:"visible_count"`
			HiddenCount  int    `json:"hidden_count"`
			Items        []struct {
				Tag     string `json:"tag"`
				Role    string `json:"role"`
				Visible bool   `json:"visible"`
			} `json:"items"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert visible by role output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#submit" || got.Locator.By != "role" || got.Locator.Query != "Search" || got.Locator.Role != "button" || !got.Locator.IncludeHidden || !got.Locator.Strict {
		t.Fatalf("assert visible locator = %+v, want strict role locator including hidden for visibility assertion", got)
	}
	if got.Assertion.Selector != "button#submit" || got.Assertion.Expected != "visible" || !got.Assertion.Visible || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.VisibleCount != 1 || got.Assertion.HiddenCount != 0 || len(got.Assertion.Items) != 1 || got.Assertion.Items[0].Tag != "button" || got.Assertion.Items[0].Role != "button" || !got.Assertion.Items[0].Visible {
		t.Fatalf("assert visible assertion = %+v, want visible resolved button", got.Assertion)
	}
}

func TestAssertVisibleRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertVisibleAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "visible", "Delayed visible", "--by", "role", "--role", "button", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert visible retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Assertion        struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Visible      bool   `json:"visible"`
			Passed       bool   `json:"passed"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert visible retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#delayed-visible" || got.Assertion.Selector != "button#delayed-visible" || got.Assertion.Expected != "visible" || !got.Assertion.Visible || !got.Assertion.Passed || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert visible retry = %+v, want retried visible assertion with timing evidence", got)
	}
}

func TestAssertVisibleTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "visible", "#hidden-button", "--timeout", "1s", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert visible hidden exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Assertion struct {
				Selector     string                  `json:"selector"`
				Visible      bool                    `json:"visible"`
				Diff         *assertionStateDiffJSON `json:"diff"`
				Passed       bool                    `json:"passed"`
				Count        int                     `json:"count"`
				VisibleCount int                     `json:"visible_count"`
				HiddenCount  int                     `json:"hidden_count"`
				Attempts     int                     `json:"attempts"`
				ElapsedMS    int64                   `json:"elapsed_ms"`
				PollInterval string                  `json:"poll_interval"`
				Items        []struct {
					Visible bool   `json:"visible"`
					Display string `json:"display"`
				} `json:"items"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert visible hidden output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Assertion.Selector != "#hidden-button" || got.Data.Assertion.Visible || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Reason != "state_mismatch" || got.Data.Assertion.Diff.Expected != "visible" || got.Data.Assertion.Diff.Actual != "hidden" || got.Data.Assertion.Diff.Count != 1 || got.Data.Assertion.Diff.MatchingCount != 0 || got.Data.Assertion.Diff.FailingCount != 1 || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.VisibleCount != 0 || got.Data.Assertion.HiddenCount != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || len(got.Data.Assertion.Items) != 1 || got.Data.Assertion.Items[0].Visible || got.Data.Assertion.Items[0].Display != "none" {
		t.Fatalf("assert visible hidden = %+v, want timeout with last visibility diagnostics", got)
	}
}

func TestAssertHiddenCSSJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "hidden", "#hidden-button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert hidden css exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Assertion struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Visible      bool   `json:"visible"`
			Hidden       bool   `json:"hidden"`
			Passed       bool   `json:"passed"`
			Count        int    `json:"count"`
			VisibleCount int    `json:"visible_count"`
			HiddenCount  int    `json:"hidden_count"`
			Items        []struct {
				Visible bool   `json:"visible"`
				Hidden  bool   `json:"hidden"`
				Display string `json:"display"`
			} `json:"items"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert hidden css output is invalid JSON: %v", err)
	}
	if !got.OK || got.Assertion.Selector != "#hidden-button" || got.Assertion.Expected != "hidden" || got.Assertion.Visible || !got.Assertion.Hidden || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.VisibleCount != 0 || got.Assertion.HiddenCount != 1 || len(got.Assertion.Items) != 1 || got.Assertion.Items[0].Visible || !got.Assertion.Items[0].Hidden || got.Assertion.Items[0].Display != "none" {
		t.Fatalf("assert hidden css = %+v, want passing hidden assertion with item diagnostics", got)
	}
}

func TestAssertHiddenRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertHiddenAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "hidden", "#delayed-hidden", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert hidden retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Assertion struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Visible      bool   `json:"visible"`
			Hidden       bool   `json:"hidden"`
			Passed       bool   `json:"passed"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert hidden retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.Assertion.Selector != "#delayed-hidden" || got.Assertion.Expected != "hidden" || got.Assertion.Visible || !got.Assertion.Hidden || !got.Assertion.Passed || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert hidden retry = %+v, want retried hidden assertion with timing evidence", got)
	}
}

func TestAssertHiddenMissingLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "hidden", "Gone", "--by", "text", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert hidden missing locator exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By            string `json:"by"`
			Query         string `json:"query"`
			IncludeHidden bool   `json:"include_hidden"`
			Count         int    `json:"count"`
			Returned      int    `json:"returned"`
			Strict        bool   `json:"strict"`
		} `json:"locator"`
		Assertion struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Visible      bool   `json:"visible"`
			Hidden       bool   `json:"hidden"`
			Passed       bool   `json:"passed"`
			Count        int    `json:"count"`
			VisibleCount int    `json:"visible_count"`
			HiddenCount  int    `json:"hidden_count"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert hidden missing locator output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "" || got.Locator.By != "text" || got.Locator.Query != "Gone" || !got.Locator.IncludeHidden || got.Locator.Count != 0 || got.Locator.Returned != 0 || got.Locator.Strict || got.Assertion.Selector != "Gone" || got.Assertion.Expected != "hidden" || got.Assertion.Visible || !got.Assertion.Hidden || !got.Assertion.Passed || got.Assertion.Count != 0 || got.Assertion.VisibleCount != 0 || got.Assertion.HiddenCount != 0 {
		t.Fatalf("assert hidden missing locator = %+v, want hidden pass with zero-match locator evidence", got)
	}
}

func TestAssertHiddenFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "hidden", "Search", "--by", "role", "--role", "button", "--timeout", "1s", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert hidden visible locator exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			ResolvedSelector string `json:"resolved_selector"`
			Locator          struct {
				By     string `json:"by"`
				Query  string `json:"query"`
				Role   string `json:"role"`
				Strict bool   `json:"strict"`
			} `json:"locator"`
			Assertion struct {
				Selector     string                  `json:"selector"`
				Expected     string                  `json:"expected"`
				Visible      bool                    `json:"visible"`
				Hidden       bool                    `json:"hidden"`
				Diff         *assertionStateDiffJSON `json:"diff"`
				Passed       bool                    `json:"passed"`
				Count        int                     `json:"count"`
				VisibleCount int                     `json:"visible_count"`
				HiddenCount  int                     `json:"hidden_count"`
				Attempts     int                     `json:"attempts"`
				ElapsedMS    int64                   `json:"elapsed_ms"`
				PollInterval string                  `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert hidden failure output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "button#submit" || got.Data.Locator.By != "role" || got.Data.Locator.Query != "Search" || got.Data.Locator.Role != "button" || !got.Data.Locator.Strict || got.Data.Assertion.Selector != "button#submit" || got.Data.Assertion.Expected != "hidden" || !got.Data.Assertion.Visible || got.Data.Assertion.Hidden || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Reason != "state_mismatch" || got.Data.Assertion.Diff.Expected != "hidden" || got.Data.Assertion.Diff.Actual != "visible" || got.Data.Assertion.Diff.Count != 1 || got.Data.Assertion.Diff.MatchingCount != 0 || got.Data.Assertion.Diff.FailingCount != 1 || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.VisibleCount != 1 || got.Data.Assertion.HiddenCount != 0 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert hidden failure = %+v, want timeout hidden assertion with locator diagnostics", got)
	}
}

func TestAssertEnabledByRoleLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "enabled", "Search", "--by", "role", "--role", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert enabled by role exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By     string `json:"by"`
			Query  string `json:"query"`
			Role   string `json:"role"`
			Strict bool   `json:"strict"`
		} `json:"locator"`
		Assertion struct {
			Selector      string `json:"selector"`
			Expected      string `json:"expected"`
			Enabled       bool   `json:"enabled"`
			Disabled      bool   `json:"disabled"`
			Passed        bool   `json:"passed"`
			Count         int    `json:"count"`
			EnabledCount  int    `json:"enabled_count"`
			DisabledCount int    `json:"disabled_count"`
			Items         []struct {
				Tag      string `json:"tag"`
				Role     string `json:"role"`
				Enabled  bool   `json:"enabled"`
				Disabled bool   `json:"disabled"`
			} `json:"items"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert enabled by role output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#submit" || got.Locator.By != "role" || got.Locator.Query != "Search" || got.Locator.Role != "button" || !got.Locator.Strict {
		t.Fatalf("assert enabled locator = %+v, want strict role locator", got)
	}
	if got.Assertion.Selector != "button#submit" || got.Assertion.Expected != "enabled" || !got.Assertion.Enabled || got.Assertion.Disabled || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.EnabledCount != 1 || got.Assertion.DisabledCount != 0 || len(got.Assertion.Items) != 1 || got.Assertion.Items[0].Tag != "button" || got.Assertion.Items[0].Role != "button" || !got.Assertion.Items[0].Enabled || got.Assertion.Items[0].Disabled {
		t.Fatalf("assert enabled assertion = %+v, want enabled resolved button", got.Assertion)
	}
}

func TestAssertDisabledByRoleLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "disabled", "Disabled target", "--by", "role", "--role", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert disabled by role exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Role    string `json:"role"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				Disabled bool `json:"disabled"`
			} `json:"matches"`
		} `json:"locator"`
		Assertion struct {
			Selector      string `json:"selector"`
			Expected      string `json:"expected"`
			Enabled       bool   `json:"enabled"`
			Disabled      bool   `json:"disabled"`
			Passed        bool   `json:"passed"`
			Count         int    `json:"count"`
			EnabledCount  int    `json:"enabled_count"`
			DisabledCount int    `json:"disabled_count"`
			Items         []struct {
				Enabled        bool     `json:"enabled"`
				Disabled       bool     `json:"disabled"`
				DisabledReason []string `json:"disabled_reason"`
				NativeDisabled bool     `json:"native_disabled"`
			} `json:"items"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert disabled by role output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#disabled-action" || got.Locator.By != "role" || got.Locator.Query != "Disabled target" || got.Locator.Role != "button" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || !got.Locator.Matches[0].Disabled {
		t.Fatalf("assert disabled locator = %+v, want strict disabled role locator", got)
	}
	if got.Assertion.Selector != "button#disabled-action" || got.Assertion.Expected != "disabled" || got.Assertion.Enabled || !got.Assertion.Disabled || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.EnabledCount != 0 || got.Assertion.DisabledCount != 1 || len(got.Assertion.Items) != 1 || got.Assertion.Items[0].Enabled || !got.Assertion.Items[0].Disabled || !got.Assertion.Items[0].NativeDisabled || !containsString(got.Assertion.Items[0].DisabledReason, "native_disabled") {
		t.Fatalf("assert disabled assertion = %+v, want disabled resolved button", got.Assertion)
	}
}

func TestAssertEnabledRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertEnabledAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "enabled", "Delayed enabled", "--by", "role", "--role", "button", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert enabled retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Assertion        struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Enabled      bool   `json:"enabled"`
			Disabled     bool   `json:"disabled"`
			Passed       bool   `json:"passed"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert enabled retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#delayed-enabled" || got.Assertion.Selector != "button#delayed-enabled" || got.Assertion.Expected != "enabled" || !got.Assertion.Enabled || got.Assertion.Disabled || !got.Assertion.Passed || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert enabled retry = %+v, want retried enabled assertion with timing evidence", got)
	}
}

func TestAssertDisabledRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertDisabledAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "disabled", "Delayed disabled", "--by", "role", "--role", "button", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert disabled retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Assertion        struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Enabled      bool   `json:"enabled"`
			Disabled     bool   `json:"disabled"`
			Passed       bool   `json:"passed"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert disabled retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#delayed-disabled" || got.Assertion.Selector != "button#delayed-disabled" || got.Assertion.Expected != "disabled" || got.Assertion.Enabled || !got.Assertion.Disabled || !got.Assertion.Passed || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert disabled retry = %+v, want retried disabled assertion with timing evidence", got)
	}
}

func TestAssertCheckedByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "checked", "Subscribe to newsletter", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert checked by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Role         string `json:"role"`
			} `json:"matches"`
		} `json:"locator"`
		Assertion struct {
			Selector         string `json:"selector"`
			Expected         string `json:"expected"`
			Checked          bool   `json:"checked"`
			Unchecked        bool   `json:"unchecked"`
			Passed           bool   `json:"passed"`
			Count            int    `json:"count"`
			CheckedCount     int    `json:"checked_count"`
			UncheckedCount   int    `json:"unchecked_count"`
			UnsupportedCount int    `json:"unsupported_count"`
			Items            []struct {
				Tag             string `json:"tag"`
				Type            string `json:"type"`
				Role            string `json:"role"`
				Name            string `json:"name"`
				Checked         bool   `json:"checked"`
				SupportsChecked bool   `json:"supports_checked"`
			} `json:"items"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert checked by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "input#subscribe" || got.Locator.By != "label" || got.Locator.Query != "Subscribe to newsletter" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || got.Locator.Matches[0].SelectorHint != "input#subscribe" || got.Locator.Matches[0].Role != "checkbox" {
		t.Fatalf("assert checked locator = %+v, want strict checkbox label locator", got)
	}
	if got.Assertion.Selector != "input#subscribe" || got.Assertion.Expected != "checked" || !got.Assertion.Checked || got.Assertion.Unchecked || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.CheckedCount != 1 || got.Assertion.UncheckedCount != 0 || got.Assertion.UnsupportedCount != 0 || len(got.Assertion.Items) != 1 || got.Assertion.Items[0].Tag != "input" || got.Assertion.Items[0].Type != "checkbox" || got.Assertion.Items[0].Role != "checkbox" || got.Assertion.Items[0].Name != "Subscribe to newsletter" || !got.Assertion.Items[0].Checked || !got.Assertion.Items[0].SupportsChecked {
		t.Fatalf("assert checked assertion = %+v, want checked checkbox diagnostics", got.Assertion)
	}
}

func TestAssertCheckedRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertCheckedAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "checked", "Delayed checkbox", "--by", "label", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert checked retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Assertion        struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Checked      bool   `json:"checked"`
			Passed       bool   `json:"passed"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert checked retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "input#delayed-check" || got.Assertion.Selector != "input#delayed-check" || got.Assertion.Expected != "checked" || !got.Assertion.Checked || !got.Assertion.Passed || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert checked retry = %+v, want retried checked assertion with timing evidence", got)
	}
}

func TestAssertUncheckedByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "unchecked", "Optional updates", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert unchecked by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By     string `json:"by"`
			Query  string `json:"query"`
			Strict bool   `json:"strict"`
		} `json:"locator"`
		Assertion struct {
			Selector       string `json:"selector"`
			Expected       string `json:"expected"`
			Checked        bool   `json:"checked"`
			Unchecked      bool   `json:"unchecked"`
			Passed         bool   `json:"passed"`
			CheckedCount   int    `json:"checked_count"`
			UncheckedCount int    `json:"unchecked_count"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert unchecked by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "input#optional-updates" || got.Locator.By != "label" || got.Locator.Query != "Optional updates" || !got.Locator.Strict {
		t.Fatalf("assert unchecked locator = %+v, want strict optional checkbox label locator", got)
	}
	if got.Assertion.Selector != "input#optional-updates" || got.Assertion.Expected != "unchecked" || got.Assertion.Checked || !got.Assertion.Unchecked || !got.Assertion.Passed || got.Assertion.CheckedCount != 0 || got.Assertion.UncheckedCount != 1 {
		t.Fatalf("assert unchecked assertion = %+v, want unchecked checkbox diagnostics", got.Assertion)
	}
}

func TestAssertCheckedTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "checked", "Optional updates", "--by", "label", "--timeout", "1s", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert checked timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Selector       string                  `json:"selector"`
				Expected       string                  `json:"expected"`
				Checked        bool                    `json:"checked"`
				Unchecked      bool                    `json:"unchecked"`
				Diff           *assertionStateDiffJSON `json:"diff"`
				Passed         bool                    `json:"passed"`
				Count          int                     `json:"count"`
				CheckedCount   int                     `json:"checked_count"`
				UncheckedCount int                     `json:"unchecked_count"`
				Attempts       int                     `json:"attempts"`
				ElapsedMS      int64                   `json:"elapsed_ms"`
				PollInterval   string                  `json:"poll_interval"`
				Items          []struct {
					Checked bool `json:"checked"`
				} `json:"items"`
			} `json:"assertion"`
		} `json:"data"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert checked timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "input#optional-updates" || got.Data.Assertion.Selector != "input#optional-updates" || got.Data.Assertion.Expected != "checked" || got.Data.Assertion.Checked || !got.Data.Assertion.Unchecked || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Reason != "state_mismatch" || got.Data.Assertion.Diff.Expected != "checked" || got.Data.Assertion.Diff.Actual != "unchecked" || got.Data.Assertion.Diff.Count != 1 || got.Data.Assertion.Diff.MatchingCount != 0 || got.Data.Assertion.Diff.FailingCount != 1 || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.CheckedCount != 0 || got.Data.Assertion.UncheckedCount != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || len(got.Data.Assertion.Items) != 1 || got.Data.Assertion.Items[0].Checked || !containsString(got.RemediationCommands, "cdp form get 'input#optional-updates' --json") {
		t.Fatalf("assert checked timeout = %+v, want timeout with last unchecked diagnostics", got)
	}
}

func TestAssertEnabledTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "enabled", "Disabled target", "--by", "role", "--role", "button", "--timeout", "1s", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert enabled disabled locator exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Selector      string                  `json:"selector"`
				Expected      string                  `json:"expected"`
				Enabled       bool                    `json:"enabled"`
				Disabled      bool                    `json:"disabled"`
				Diff          *assertionStateDiffJSON `json:"diff"`
				Passed        bool                    `json:"passed"`
				Count         int                     `json:"count"`
				EnabledCount  int                     `json:"enabled_count"`
				DisabledCount int                     `json:"disabled_count"`
				Attempts      int                     `json:"attempts"`
				ElapsedMS     int64                   `json:"elapsed_ms"`
				PollInterval  string                  `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert enabled timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "button#disabled-action" || got.Data.Assertion.Selector != "button#disabled-action" || got.Data.Assertion.Expected != "enabled" || got.Data.Assertion.Enabled || !got.Data.Assertion.Disabled || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Reason != "state_mismatch" || got.Data.Assertion.Diff.Expected != "enabled" || got.Data.Assertion.Diff.Actual != "disabled" || got.Data.Assertion.Diff.Count != 1 || got.Data.Assertion.Diff.MatchingCount != 0 || got.Data.Assertion.Diff.FailingCount != 1 || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.EnabledCount != 0 || got.Data.Assertion.DisabledCount != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert enabled timeout = %+v, want timeout with disabled diagnostics", got)
	}
}

func TestAssertDisabledTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "disabled", "Search", "--by", "role", "--role", "button", "--timeout", "1s", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert disabled enabled locator exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Selector      string                  `json:"selector"`
				Expected      string                  `json:"expected"`
				Enabled       bool                    `json:"enabled"`
				Disabled      bool                    `json:"disabled"`
				Diff          *assertionStateDiffJSON `json:"diff"`
				Passed        bool                    `json:"passed"`
				Count         int                     `json:"count"`
				EnabledCount  int                     `json:"enabled_count"`
				DisabledCount int                     `json:"disabled_count"`
				Attempts      int                     `json:"attempts"`
				ElapsedMS     int64                   `json:"elapsed_ms"`
				PollInterval  string                  `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert disabled timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "button#submit" || got.Data.Assertion.Selector != "button#submit" || got.Data.Assertion.Expected != "disabled" || !got.Data.Assertion.Enabled || got.Data.Assertion.Disabled || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Reason != "state_mismatch" || got.Data.Assertion.Diff.Expected != "disabled" || got.Data.Assertion.Diff.Actual != "enabled" || got.Data.Assertion.Diff.Count != 1 || got.Data.Assertion.Diff.MatchingCount != 0 || got.Data.Assertion.Diff.FailingCount != 1 || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.EnabledCount != 1 || got.Data.Assertion.DisabledCount != 0 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert disabled timeout = %+v, want timeout with enabled diagnostics", got)
	}
}

func TestAssertEditableByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "editable", "Search", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert editable by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By     string `json:"by"`
			Query  string `json:"query"`
			Strict bool   `json:"strict"`
		} `json:"locator"`
		Assertion struct {
			Selector         string `json:"selector"`
			Expected         string `json:"expected"`
			Editable         bool   `json:"editable"`
			ReadOnly         bool   `json:"read_only"`
			Passed           bool   `json:"passed"`
			Count            int    `json:"count"`
			EditableCount    int    `json:"editable_count"`
			ReadOnlyCount    int    `json:"read_only_count"`
			DisabledCount    int    `json:"disabled_count"`
			UnsupportedCount int    `json:"unsupported_count"`
			Items            []struct {
				Tag              string `json:"tag"`
				Role             string `json:"role"`
				Editable         bool   `json:"editable"`
				ReadOnly         bool   `json:"read_only"`
				SupportsEditable bool   `json:"supports_editable"`
				Enabled          bool   `json:"enabled"`
			} `json:"items"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert editable by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "input#q" || got.Locator.By != "label" || got.Locator.Query != "Search" || !got.Locator.Strict {
		t.Fatalf("assert editable locator = %+v, want strict label locator", got)
	}
	if got.Assertion.Selector != "input#q" || got.Assertion.Expected != "editable" || !got.Assertion.Editable || got.Assertion.ReadOnly || !got.Assertion.Passed || got.Assertion.Count != 1 || got.Assertion.EditableCount != 1 || got.Assertion.ReadOnlyCount != 0 || got.Assertion.DisabledCount != 0 || got.Assertion.UnsupportedCount != 0 || len(got.Assertion.Items) != 1 || got.Assertion.Items[0].Tag != "input" || got.Assertion.Items[0].Role != "searchbox" || !got.Assertion.Items[0].Editable || got.Assertion.Items[0].ReadOnly || !got.Assertion.Items[0].SupportsEditable || !got.Assertion.Items[0].Enabled {
		t.Fatalf("assert editable assertion = %+v, want editable resolved input", got.Assertion)
	}
}

func TestAssertReadonlyByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "readonly", "Read-only notes", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert readonly by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				ReadOnly bool `json:"read_only"`
			} `json:"matches"`
		} `json:"locator"`
		Assertion struct {
			Selector      string `json:"selector"`
			Expected      string `json:"expected"`
			Editable      bool   `json:"editable"`
			ReadOnly      bool   `json:"read_only"`
			Passed        bool   `json:"passed"`
			EditableCount int    `json:"editable_count"`
			ReadOnlyCount int    `json:"read_only_count"`
			Items         []struct {
				ReadOnly       bool     `json:"read_only"`
				ReadOnlyReason []string `json:"read_only_reason"`
				NativeReadOnly bool     `json:"native_read_only"`
			} `json:"items"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert readonly by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "textarea#readonly-notes" || got.Locator.By != "label" || got.Locator.Query != "Read-only notes" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || !got.Locator.Matches[0].ReadOnly {
		t.Fatalf("assert readonly locator = %+v, want strict read-only label locator", got)
	}
	if got.Assertion.Selector != "textarea#readonly-notes" || got.Assertion.Expected != "readonly" || got.Assertion.Editable || !got.Assertion.ReadOnly || !got.Assertion.Passed || got.Assertion.EditableCount != 0 || got.Assertion.ReadOnlyCount != 1 || len(got.Assertion.Items) != 1 || !got.Assertion.Items[0].ReadOnly || !got.Assertion.Items[0].NativeReadOnly || !containsString(got.Assertion.Items[0].ReadOnlyReason, "native_readonly") {
		t.Fatalf("assert readonly assertion = %+v, want read-only resolved textarea", got.Assertion)
	}
}

func TestAssertEditableRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertEditableAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "editable", "Delayed editable", "--by", "label", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert editable retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Assertion        struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Editable     bool   `json:"editable"`
			ReadOnly     bool   `json:"read_only"`
			Passed       bool   `json:"passed"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert editable retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "input#delayed-editable" || got.Assertion.Selector != "input#delayed-editable" || got.Assertion.Expected != "editable" || !got.Assertion.Editable || got.Assertion.ReadOnly || !got.Assertion.Passed || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert editable retry = %+v, want retried editable assertion with timing evidence", got)
	}
}

func TestAssertReadonlyRetriesUntilPassJSON(t *testing.T) {
	fakeDelayedAssertReadonlyAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "readonly", "Delayed readonly", "--by", "label", "--timeout", "250ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert readonly retry exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Assertion        struct {
			Selector     string `json:"selector"`
			Expected     string `json:"expected"`
			Editable     bool   `json:"editable"`
			ReadOnly     bool   `json:"read_only"`
			Passed       bool   `json:"passed"`
			Attempts     int    `json:"attempts"`
			ElapsedMS    int64  `json:"elapsed_ms"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert readonly retry output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "textarea#delayed-readonly" || got.Assertion.Selector != "textarea#delayed-readonly" || got.Assertion.Expected != "readonly" || got.Assertion.Editable || !got.Assertion.ReadOnly || !got.Assertion.Passed || got.Assertion.Attempts < 3 || got.Assertion.ElapsedMS <= 0 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert readonly retry = %+v, want retried read-only assertion with timing evidence", got)
	}
}

func TestAssertEditableTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "editable", "Read-only notes", "--by", "label", "--timeout", "1s", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert editable readonly locator exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Selector      string                  `json:"selector"`
				Expected      string                  `json:"expected"`
				Editable      bool                    `json:"editable"`
				ReadOnly      bool                    `json:"read_only"`
				Diff          *assertionStateDiffJSON `json:"diff"`
				Passed        bool                    `json:"passed"`
				Count         int                     `json:"count"`
				EditableCount int                     `json:"editable_count"`
				ReadOnlyCount int                     `json:"read_only_count"`
				Attempts      int                     `json:"attempts"`
				ElapsedMS     int64                   `json:"elapsed_ms"`
				PollInterval  string                  `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert editable timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "textarea#readonly-notes" || got.Data.Assertion.Selector != "textarea#readonly-notes" || got.Data.Assertion.Expected != "editable" || got.Data.Assertion.Editable || !got.Data.Assertion.ReadOnly || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Reason != "state_mismatch" || got.Data.Assertion.Diff.Expected != "editable" || got.Data.Assertion.Diff.Actual != "readonly" || got.Data.Assertion.Diff.Count != 1 || got.Data.Assertion.Diff.MatchingCount != 0 || got.Data.Assertion.Diff.FailingCount != 1 || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.EditableCount != 0 || got.Data.Assertion.ReadOnlyCount != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert editable timeout = %+v, want timeout with read-only diagnostics", got)
	}
}

func TestAssertReadonlyTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "readonly", "Search", "--by", "label", "--timeout", "1s", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert readonly editable locator exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Selector      string                  `json:"selector"`
				Expected      string                  `json:"expected"`
				Editable      bool                    `json:"editable"`
				ReadOnly      bool                    `json:"read_only"`
				Diff          *assertionStateDiffJSON `json:"diff"`
				Passed        bool                    `json:"passed"`
				Count         int                     `json:"count"`
				EditableCount int                     `json:"editable_count"`
				ReadOnlyCount int                     `json:"read_only_count"`
				Attempts      int                     `json:"attempts"`
				ElapsedMS     int64                   `json:"elapsed_ms"`
				PollInterval  string                  `json:"poll_interval"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert readonly timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "input#q" || got.Data.Assertion.Selector != "input#q" || got.Data.Assertion.Expected != "readonly" || !got.Data.Assertion.Editable || got.Data.Assertion.ReadOnly || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Reason != "state_mismatch" || got.Data.Assertion.Diff.Expected != "readonly" || got.Data.Assertion.Diff.Actual != "editable" || got.Data.Assertion.Diff.Count != 1 || got.Data.Assertion.Diff.MatchingCount != 0 || got.Data.Assertion.Diff.FailingCount != 1 || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.EditableCount != 1 || got.Data.Assertion.ReadOnlyCount != 0 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert readonly timeout = %+v, want timeout with editable diagnostics", got)
	}
}

func TestFormValuesListsVisibleControls(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"form", "values", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("form values exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		Controls []struct {
			SelectorHint string `json:"selector_hint"`
			Name         string `json:"name"`
			Value        string `json:"value"`
			Visible      bool   `json:"visible"`
		} `json:"controls"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("form values output is invalid JSON: %v", err)
	}
	if len(got.Controls) != 2 || got.Controls[1].SelectorHint != "textarea#out" || got.Controls[1].Value != "SGVsbG8=" || !got.Controls[1].Visible {
		t.Fatalf("form values controls = %+v, want visible output textarea", got.Controls)
	}
}
