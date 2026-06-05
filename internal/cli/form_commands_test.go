package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

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

func TestAssertVisibleFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "visible", "#hidden-button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("assert visible hidden exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Assertion struct {
				Selector     string `json:"selector"`
				Visible      bool   `json:"visible"`
				Passed       bool   `json:"passed"`
				Count        int    `json:"count"`
				VisibleCount int    `json:"visible_count"`
				HiddenCount  int    `json:"hidden_count"`
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
	if got.OK || got.Code != "assertion_failed" || got.Data.Assertion.Selector != "#hidden-button" || got.Data.Assertion.Visible || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.VisibleCount != 0 || got.Data.Assertion.HiddenCount != 1 || len(got.Data.Assertion.Items) != 1 || got.Data.Assertion.Items[0].Visible || got.Data.Assertion.Items[0].Display != "none" {
		t.Fatalf("assert visible hidden = %+v, want failed visibility assertion with diagnostics", got)
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
	code := cli.Execute(context.Background(), []string{"assert", "hidden", "Search", "--by", "role", "--role", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("assert hidden visible locator exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
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
				Selector     string `json:"selector"`
				Expected     string `json:"expected"`
				Visible      bool   `json:"visible"`
				Hidden       bool   `json:"hidden"`
				Passed       bool   `json:"passed"`
				Count        int    `json:"count"`
				VisibleCount int    `json:"visible_count"`
				HiddenCount  int    `json:"hidden_count"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert hidden failure output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "assertion_failed" || got.Data.ResolvedSelector != "button#submit" || got.Data.Locator.By != "role" || got.Data.Locator.Query != "Search" || got.Data.Locator.Role != "button" || !got.Data.Locator.Strict || got.Data.Assertion.Selector != "button#submit" || got.Data.Assertion.Expected != "hidden" || !got.Data.Assertion.Visible || got.Data.Assertion.Hidden || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.VisibleCount != 1 || got.Data.Assertion.HiddenCount != 0 {
		t.Fatalf("assert hidden failure = %+v, want failed hidden assertion with locator diagnostics", got)
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
	code := cli.Execute(context.Background(), []string{"assert", "checked", "Optional updates", "--by", "label", "--timeout", "50ms", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert checked timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Selector       string `json:"selector"`
				Expected       string `json:"expected"`
				Checked        bool   `json:"checked"`
				Unchecked      bool   `json:"unchecked"`
				Passed         bool   `json:"passed"`
				CheckedCount   int    `json:"checked_count"`
				UncheckedCount int    `json:"unchecked_count"`
				Attempts       int    `json:"attempts"`
				ElapsedMS      int64  `json:"elapsed_ms"`
				PollInterval   string `json:"poll_interval"`
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
	if got.OK || got.Code != "timeout" || got.Data.ResolvedSelector != "input#optional-updates" || got.Data.Assertion.Selector != "input#optional-updates" || got.Data.Assertion.Expected != "checked" || got.Data.Assertion.Checked || !got.Data.Assertion.Unchecked || got.Data.Assertion.Passed || got.Data.Assertion.CheckedCount != 0 || got.Data.Assertion.UncheckedCount != 1 || got.Data.Assertion.Attempts < 2 || got.Data.Assertion.ElapsedMS <= 0 || got.Data.Assertion.PollInterval != "10ms" || len(got.Data.Assertion.Items) != 1 || got.Data.Assertion.Items[0].Checked || !containsString(got.RemediationCommands, "cdp form get 'input#optional-updates' --json") {
		t.Fatalf("assert checked timeout = %+v, want timeout with last unchecked diagnostics", got)
	}
}

func TestAssertEnabledFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "enabled", "Disabled target", "--by", "role", "--role", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("assert enabled disabled locator exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Selector      string `json:"selector"`
				Expected      string `json:"expected"`
				Enabled       bool   `json:"enabled"`
				Disabled      bool   `json:"disabled"`
				Passed        bool   `json:"passed"`
				Count         int    `json:"count"`
				EnabledCount  int    `json:"enabled_count"`
				DisabledCount int    `json:"disabled_count"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert enabled failure output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "assertion_failed" || got.Data.ResolvedSelector != "button#disabled-action" || got.Data.Assertion.Selector != "button#disabled-action" || got.Data.Assertion.Expected != "enabled" || got.Data.Assertion.Enabled || !got.Data.Assertion.Disabled || got.Data.Assertion.Passed || got.Data.Assertion.Count != 1 || got.Data.Assertion.EnabledCount != 0 || got.Data.Assertion.DisabledCount != 1 {
		t.Fatalf("assert enabled failure = %+v, want failed enabled assertion with disabled diagnostics", got)
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

func TestAssertEditableFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/app", "title": "Example App"}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "editable", "Read-only notes", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("assert editable readonly locator exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			ResolvedSelector string `json:"resolved_selector"`
			Assertion        struct {
				Selector      string `json:"selector"`
				Expected      string `json:"expected"`
				Editable      bool   `json:"editable"`
				ReadOnly      bool   `json:"read_only"`
				Passed        bool   `json:"passed"`
				EditableCount int    `json:"editable_count"`
				ReadOnlyCount int    `json:"read_only_count"`
			} `json:"assertion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert editable failure output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "assertion_failed" || got.Data.ResolvedSelector != "textarea#readonly-notes" || got.Data.Assertion.Selector != "textarea#readonly-notes" || got.Data.Assertion.Expected != "editable" || got.Data.Assertion.Editable || !got.Data.Assertion.ReadOnly || got.Data.Assertion.Passed || got.Data.Assertion.EditableCount != 0 || got.Data.Assertion.ReadOnlyCount != 1 {
		t.Fatalf("assert editable failure = %+v, want failed editable assertion with read-only diagnostics", got)
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
