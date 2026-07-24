package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestSourceCollectorsRejectInvalidIdentityBeforeBrowserOrGenericFallback(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code string
	}{
		{"reddit wrong platform", []string{"workflow", "reddit", "collect", "https://example.test/r/formula1/", "--json"}, "invalid_reddit_url"},
		{"reddit listing alias rejects thread", []string{"workflow", "reddit", "posts", "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/", "--json"}, "invalid_reddit_url"},
		{"x wrong platform", []string{"workflow", "x", "collect", "https://example.test/karpathy", "--json"}, "invalid_x_url"},
		{"linkedin wrong platform", []string{"workflow", "linkedin", "collect", "https://example.test/company/example/posts/", "--json"}, "invalid_linkedin_url"},
		{"hacker news wrong platform", []string{"workflow", "hacker-news", "collect", "https://example.test/item?id=46641042", "--json"}, "invalid_hacker_news_url"},
		{"arxiv wrong platform", []string{"workflow", "arxiv", "collect", "https://example.test/abs/2604.12374v1", "--json"}, "invalid_arxiv_url"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("exit=%d, want usage; stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			var got struct {
				OK   bool   `json:"ok"`
				Code string `json:"code"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("decode error envelope: %v; stdout=%s", err, out.String())
			}
			if got.OK || got.Code != tt.code {
				t.Fatalf("error envelope=%+v, want code %q", got, tt.code)
			}
		})
	}
}
