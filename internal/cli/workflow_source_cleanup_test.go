package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestSourceCollectionOwnedPagesCloseOnSuccess(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "reddit",
			args: []string{"workflow", "reddit", "collect", "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/", "--limit", "2", "--wait", "0", "--json"},
		},
		{
			name: "x",
			args: []string{"workflow", "x", "collect", "https://x.com/karpathy/status/2079610838143623371", "--limit", "2", "--wait", "0", "--json"},
		},
		{
			name: "linkedin",
			args: []string{"workflow", "linkedin", "collect", "https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD/", "--limit", "2", "--wait", "0", "--json"},
		},
		{
			name: "hacker news",
			args: []string{"workflow", "hacker-news", "collect", "https://news.ycombinator.com/item?id=46641042", "--limit", "10", "--json"},
		},
		{
			name: "arxiv",
			args: []string{"workflow", "arxiv", "collect", "https://arxiv.org/abs/2604.12374v2", "--json"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, nil)
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), test.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
			}
			if pages := fakePagesCount(t); pages != 0 {
				t.Fatalf("successful %s collection page count=%d, want baseline", test.name, pages)
			}
			var result struct {
				Workflow struct {
					CreatedPage bool   `json:"created_page"`
					Closed      bool   `json:"closed"`
					CloseError  string `json:"close_error"`
				} `json:"workflow"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("successful %s output is invalid JSON: %v; output=%s", test.name, err, out.String())
			}
			if !result.Workflow.CreatedPage || !result.Workflow.Closed || result.Workflow.CloseError != "" {
				t.Fatalf("successful %s cleanup=%+v, want created and settled closed", test.name, result.Workflow)
			}
		})
	}
}

func TestSourceCollectionOwnedPagesCloseOnAttachAndRuntimeErrors(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		errorCode string
	}{
		{
			name:      "reddit attach",
			args:      []string{"workflow", "reddit", "collect", "https://www.reddit.com/r/attach_error/", "--limit", "2", "--wait", "0", "--json"},
			errorCode: "connection_failed",
		},
		{
			name:      "x attach",
			args:      []string{"workflow", "x", "collect", "https://x.com/attach_error", "--limit", "2", "--wait", "0", "--json"},
			errorCode: "connection_failed",
		},
		{
			name:      "linkedin attach",
			args:      []string{"workflow", "linkedin", "collect", "https://www.linkedin.com/company/attach-error/posts/", "--limit", "2", "--wait", "0", "--json"},
			errorCode: "connection_failed",
		},
		{
			name:      "reddit runtime",
			args:      []string{"workflow", "reddit", "collect", "https://www.reddit.com/user/evaluate-error/", "--limit", "2", "--wait", "0", "--json"},
			errorCode: "connection_failed",
		},
		{
			name:      "x runtime",
			args:      []string{"workflow", "x", "collect", "https://x.com/evaluate_error", "--limit", "2", "--wait", "0", "--json"},
			errorCode: "connection_failed",
		},
		{
			name:      "linkedin runtime",
			args:      []string{"workflow", "linkedin", "collect", "https://www.linkedin.com/company/evaluate-error/posts/", "--limit", "2", "--wait", "0", "--json"},
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
			if code != cli.ExitConnection {
				t.Fatalf("exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitConnection, out.String(), errOut.String())
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
			if pages := fakePagesCount(t); pages != 0 {
				t.Fatalf("failed %s collection page count=%d, want baseline", test.name, pages)
			}
		})
	}
}

func TestSourceCollectionKeepOpenRetainsWorkflowPage(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "x", "collect", "https://x.com/karpathy/status/2079610838143623371",
		"--limit", "2", "--wait", "0", "--keep-open", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var result struct {
		Workflow struct {
			Closed bool `json:"closed"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("keep-open output is invalid JSON: %v; output=%s", err, out.String())
	}
	if result.Workflow.Closed {
		t.Fatalf("keep-open workflow reported closed: %+v", result.Workflow)
	}
	if pages := fakePagesCount(t); pages != 1 {
		t.Fatalf("keep-open collection page count=%d, want one retained workflow page", pages)
	}
}
