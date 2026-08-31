package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowOwnedPagesCloseOnErrors(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		exit      int
		errorCode string
		pageCount int
	}{
		{
			name:      "feeds result error",
			args:      []string{"workflow", "feeds", "https://example.test/no-results", "--wait-load", "0s", "--json"},
			exit:      cli.ExitCheckFailed,
			errorCode: "feed_not_found",
		},
		{
			name:      "visible posts result error",
			args:      []string{"workflow", "visible-posts", "https://example.test/feed", "--selector", "empty", "--wait", "0s", "--json"},
			exit:      cli.ExitCheckFailed,
			errorCode: "no_visible_posts",
		},
		{
			name:      "hacker news result error",
			args:      []string{"workflow", "hacker-news", "https://news.ycombinator.com/no-results", "--wait", "0s", "--json"},
			exit:      cli.ExitCheckFailed,
			errorCode: "no_visible_posts",
		},
		{
			name:      "attach error",
			args:      []string{"workflow", "feeds", "https://example.test/attach-error", "--wait-load", "0s", "--json"},
			exit:      cli.ExitConnection,
			errorCode: "connection_failed",
		},
		{
			name:      "evaluation error",
			args:      []string{"workflow", "feeds", "https://example.test/evaluate-error", "--wait-load", "0s", "--json"},
			exit:      cli.ExitConnection,
			errorCode: "connection_failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, nil)
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), test.args, &out, &errOut, cli.BuildInfo{})
			if code != test.exit {
				t.Fatalf("exit=%d, want %d; stdout=%s stderr=%s", code, test.exit, out.String(), errOut.String())
			}
			var envelope struct {
				OK   bool   `json:"ok"`
				Code string `json:"code"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatalf("error output is invalid JSON: %v; output=%s", err, out.String())
			}
			if envelope.OK || envelope.Code != test.errorCode {
				t.Fatalf("error envelope=%+v, want code %q", envelope, test.errorCode)
			}
			if pages := fakePagesCount(t); pages != test.pageCount {
				t.Fatalf("workflow error page count=%d, want %d", pages, test.pageCount)
			}
		})
	}
}

func TestWorkflowOwnedErrorPreservesKeepOpen(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "feeds", "https://example.test/no-results", "--wait-load", "0s", "--keep-open", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}
	if pages := fakePagesCount(t); pages != 1 {
		t.Fatalf("keep-open error page count=%d, want one retained workflow page", pages)
	}
}

func TestWorkflowOwnedPagesCloseOnCommandTimeout(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "feeds",
			args: []string{"--timeout", "500ms", "workflow", "feeds", "https://example.test/timeout", "--wait-load", "5s", "--json"},
		},
		{
			name: "visible posts",
			args: []string{"--timeout", "500ms", "workflow", "visible-posts", "https://example.test/no-results", "--wait", "5s", "--json"},
		},
		{
			name: "hacker news",
			args: []string{"--timeout", "500ms", "workflow", "hacker-news", "https://news.ycombinator.com/no-results", "--wait", "5s", "--json"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, nil)
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), test.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitTimeout {
				t.Fatalf("exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
			}
			var envelope struct {
				OK   bool   `json:"ok"`
				Code string `json:"code"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatalf("timeout output is invalid JSON: %v; output=%s", err, out.String())
			}
			if envelope.OK || envelope.Code != "timeout" {
				t.Fatalf("timeout envelope=%+v", envelope)
			}
			if pages := fakePagesCount(t); pages != 0 {
				t.Fatalf("workflow timeout page count=%d, want baseline", pages)
			}
		})
	}
}
