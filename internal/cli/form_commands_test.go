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
