package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestProtocolExecAcceptsSourceCompatiblePositionalParams(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "positional-page", "type": "page", "title": "Positional", "url": "https://example.test/positional", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"protocol", "exec", "Runtime.evaluate",
		`{"expression":"document.title","returnByValue":true}`,
		"--target", "positional-page", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("positional protocol exec exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
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
		t.Fatalf("positional protocol exec output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || got.Method != "Runtime.evaluate" || got.Result.Result.Value != "Example App" {
		t.Fatalf("positional protocol exec = %+v, want forwarded Runtime.evaluate params", got)
	}
}

func TestProtocolExecPositionalParamsReuseValidationAndShapeGuards(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantCode   string
		wantText   string
		needServer bool
	}{
		{
			name:       "validated object",
			args:       []string{"Page.navigate", `{"url":"https://example.test/next"}`, "--target", "positional-page", "--validate", "--json"},
			wantExit:   cli.ExitOK,
			needServer: true,
		},
		{
			name:     "array rejected locally",
			args:     []string{"Browser.getVersion", "[]", "--json"},
			wantExit: cli.ExitUsage,
			wantCode: "invalid_json",
			wantText: "JSON object",
		},
		{
			name:     "explicit flag conflict",
			args:     []string{"Browser.getVersion", `{}`, "--params", `{}`, "--json"},
			wantExit: cli.ExitUsage,
			wantCode: "conflicting_params",
			wantText: "cannot be combined",
		},
		{
			name:     "too many positional values",
			args:     []string{"Browser.getVersion", `{}`, `{}`, "--json"},
			wantExit: cli.ExitUsage,
			wantCode: "usage",
			wantText: "accepts between 1 and 2 arg(s)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.needServer {
				server := newFakeCDPServer(t, []map[string]any{
					{"targetId": "positional-page", "type": "page", "title": "Positional", "url": "https://example.test/positional", "attached": false},
				})
				defer server.Close()
				startFakeDaemon(t, server, "browser_url")
			}

			args := append([]string{"protocol", "exec"}, test.args...)
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != test.wantExit {
				t.Fatalf("protocol exec exit=%d, want %d; stdout=%s stderr=%s", code, test.wantExit, out.String(), errOut.String())
			}
			if test.wantExit == cli.ExitOK {
				var success struct {
					OK bool `json:"ok"`
				}
				if err := json.Unmarshal(out.Bytes(), &success); err != nil || !success.OK {
					t.Fatalf("successful positional output = %s, want ok envelope", out.String())
				}
				return
			}
			var failure struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(out.Bytes(), &failure); err != nil {
				t.Fatalf("positional failure output is invalid JSON: %v; output=%s", err, out.String())
			}
			if failure.Code != test.wantCode || !strings.Contains(failure.Message, test.wantText) {
				t.Fatalf("positional failure = %+v, want %s containing %q", failure, test.wantCode, test.wantText)
			}
		})
	}
}
