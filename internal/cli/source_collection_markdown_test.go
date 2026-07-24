package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestNativeCollectorsRenderDetailedMarkdownByDefault(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	for _, tt := range []struct {
		name       string
		args, want []string
	}{
		{"x", []string{"workflow", "x", "collect", "https://x.com/karpathy/status/2079610838143623371", "--limit", "2", "--wait", "0"}, []string{"# X collection", "Synthetic root", "https://x.com/karpathy/status/2079610838143623371", "Termination evidence"}},
		{"reddit", []string{"workflow", "reddit", "collect", "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/", "--limit", "2", "--wait", "0"}, []string{"# Reddit collection", "Synthetic root", "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/"}},
		{"linkedin", []string{"workflow", "linkedin", "collect", "https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD/", "--limit", "2", "--wait", "0"}, []string{"# LinkedIn collection", "Synthetic reply", "https://www.linkedin.com/posts/"}},
		{"hacker news", []string{"workflow", "hacker-news", "collect", "https://news.ycombinator.com/item?id=46641042", "--limit", "10"}, []string{"# Hacker News collection", "Synthetic HN story", "Synthetic HN comment", "https://news.ycombinator.com/item?id=46642165"}},
		{"arxiv", []string{"workflow", "arxiv", "collect", "https://arxiv.org/abs/2604.12374v2"}, []string{"# arXiv collection", "Synthetic paper", "Synthetic reference", "https://arxiv.org/abs/2604.12374v2"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{}); code != cli.ExitOK {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("plain output missing %q:\n%s", want, out.String())
				}
			}
			if strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
				t.Fatalf("default output unexpectedly JSON: %s", out.String())
			}
		})
	}
}
