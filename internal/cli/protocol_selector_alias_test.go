package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestProtocolExecSourceSelectorAliases(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "target id through root alias",
			args: []string{"--target-id", "selector-alias-page", "--json", "Runtime.evaluate", `{"expression":"document.title","returnByValue":true}`},
		},
		{
			name: "url through protocol exec",
			args: []string{"protocol", "exec", "Runtime.evaluate", "--url", "selector-alias", "--params", `{"expression":"document.title","returnByValue":true}`, "--json"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "selector-alias-page", "type": "page", "title": "Selector alias", "url": "https://example.test/selector-alias", "attached": false},
			})
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), test.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("selector alias exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
			}
			var got struct {
				OK     bool   `json:"ok"`
				Method string `json:"method"`
				Result struct {
					Result struct {
						Value string `json:"value"`
					} `json:"result"`
				} `json:"result"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("selector alias output is invalid JSON: %v; output=%s", err, out.String())
			}
			if !got.OK || got.Method != "Runtime.evaluate" || got.Result.Result.Value != "Example App" {
				t.Fatalf("selector alias = %+v, want Runtime.evaluate value Example App", got)
			}
		})
	}
}

func TestProtocolExecRejectsDuplicateSelectorAliasSpellings(t *testing.T) {
	tests := []struct {
		name string
		args []string
		text string
	}{
		{
			name: "target aliases",
			args: []string{"protocol", "exec", "Runtime.evaluate", "--target", "one", "--target-id", "two", "--json"},
			text: "--target and --target-id are aliases",
		},
		{
			name: "url aliases",
			args: []string{"protocol", "exec", "Runtime.evaluate", "--url-contains", "one", "--url", "two", "--json"},
			text: "--url and --url-contains are aliases",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), test.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("duplicate selector exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
			}
			var failure struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(out.Bytes(), &failure); err != nil {
				t.Fatalf("duplicate selector output is invalid JSON: %v; output=%s", err, out.String())
			}
			if failure.Code != "invalid_target_selector" || !strings.Contains(failure.Message, test.text) {
				t.Fatalf("duplicate selector failure = %+v, want invalid_target_selector containing %q", failure, test.text)
			}
		})
	}
}
