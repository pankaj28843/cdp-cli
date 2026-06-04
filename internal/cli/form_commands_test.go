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
