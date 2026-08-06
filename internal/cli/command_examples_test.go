package cli

import (
	"strings"
	"testing"
)

func TestCommandExamplesHighRiskPaths(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{path: "cdp version", want: []string{"version --json"}},
		{path: "cdp daemon start", want: []string{"--auto-connect"}},
		{path: "cdp daemon stop", want: []string{"--force-managed", "--stale-lock-after"}},
		{path: "cdp daemon restart", want: []string{"--autoConnect", "--force-managed", "--stale-lock-after"}},
		{path: "cdp daemon keepalive", want: []string{"--browser-mode headed", "--browser-mode headless"}},
		{path: "cdp daemon maintenance", want: []string{"--browser-mode headless", "--dry-run", "--profile-seed-strategy copy-default", "--stale-lock-after 1s"}},
		{path: "cdp daemon logs", want: []string{"--tail"}},
		{path: "cdp cron status", want: []string{"managed_processes", "last_run_artifacts"}},
		{path: "cdp cron install", want: []string{"cdp cron install --json", "--config cdp.json", "--artifact-retention 168h", "--max-log-size 64MiB"}},
		{path: "cdp cron run", want: []string{"headed-daemon-keepalive", "headless-maintenance", "artifact-prune"}},
		{path: "cdp artifacts prune", want: []string{"--older-than 168h", "--max-log-size 64MiB", "--dry-run", "--apply"}},
		{path: "cdp artifacts run-managed", want: []string{"--task example", "--log tmp/example.log"}},
		{path: "cdp cron heal headed", want: []string{"--reconnect 30s"}},
		{path: "cdp browser preflight", want: []string{"--repair", "--cleanup", "--profile-seed managed"}},
		{path: "cdp targets", want: []string{"--retry transient"}},
		{path: "cdp pages", want: []string{"--title-contains", "--retry transient"}},
		{path: "cdp page cleanup", want: []string{"--browser-mode headed", "--browser-mode headless", "--close", "--root-task-id"}},
		{path: "cdp open", want: []string{"--task-id", "--reuse", "--budget-summary"}},
		{path: "cdp stop-state classify", want: []string{"--rule-text-contains", "--target", "Sign in to continue"}},
		{path: "cdp eval", want: []string{"--title-contains"}},
		{path: "cdp wait eval", want: []string{"--ready-expr", "--out-dir", "--retry transient", "--classify-stop-state"}},
		{path: "cdp html", want: []string{"--diagnose-empty"}},
		{path: "cdp screenshot", want: []string{"--element"}},
		{path: "cdp screenshot render", want: []string{"--serve"}},
		{path: "cdp storage cookies set", want: []string{"--name"}},
		{path: "cdp storage indexeddb put", want: []string{"@tmp/value.json"}},
		{path: "cdp storage cache put", want: []string{"--content-type"}},
		{path: "cdp storage service-workers unregister", want: []string{"--scope"}},
		{path: "cdp protocol exec", want: []string{"--target"}},
		{path: "cdp file chooser", want: []string{"--target <target-id>", "--trial", "first.epub", "second.epub"}},
		{path: "cdp protocol examples", want: []string{"Page.captureScreenshot"}},
		{path: "cdp workflow page-load", want: []string{"--reload"}},
		{path: "cdp workflow rendered-extract", want: []string{"--serp google", "x.com/karpathy/status", "x.com/karpathy'", "linkedin.com/company", "reddit.com/user", "reddit.com/r/example/comments"}},
		{path: "cdp workflow reddit posts", want: []string{"reddit.com/r/formula1/top/?t=week", "--limit 200", "reddit.com/r/golang/new/"}},
		{path: "cdp workflow reddit collect", want: []string{"reddit.com/r/formula1/top/?t=week", "reddit.com/r/codex/comments", "reddit.com/user/celticpaladin/comments"}},
		{path: "cdp workflow x collect", want: []string{"x.com/karpathy/status", "x.com/karpathy", "--limit 200"}},
		{path: "cdp workflow linkedin collect", want: []string{"linkedin.com/posts", "linkedin.com/company", "--limit 200"}},
		{path: "cdp workflow arxiv collect", want: []string{"arxiv.org/abs", "--json"}},
		{path: "cdp workflow web-research serp", want: []string{"--result-pages 3", "cdr:1,cd_min:07/01/2026,cd_max:07/01/2026", "--google-ai auto", "--google-ai mode"}},
		{path: "cdp workflow web-research extract", want: []string{"--parallel 4", "--parallel 10"}},
		{path: "cdp workflow feeds", want: []string{"--wait-load"}},
		{path: "cdp workflow visible-posts", want: []string{"visible-posts"}},
		{path: "cdp workflow hacker-news", want: []string{"hacker-news"}},
		{path: "cdp workflow hacker-news collect", want: []string{"news.ycombinator.com/item?id", "--limit 500"}},
		{path: "cdp workflow verify", want: []string{"workflow verify"}},
		{path: "cdp workflow debug-bundle", want: []string{"debug-bundle"}},
		{path: "cdp workflow action-capture", want: []string{"--evidence-out-dir", "--include-bodies json,text", "--body-url-contains /api/"}},
		{path: "cdp workflow submit-search", want: []string{"--wait-url-contains", "--suggestion", "--submit none", "--wait-load-state", "--wait-response"}},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			examples := commandExamples(tt.path)
			if len(examples) == 0 {
				t.Fatalf("commandExamples(%q) returned no examples", tt.path)
			}
			for _, want := range tt.want {
				if !examplesContain(examples, want) {
					t.Fatalf("commandExamples(%q) = %#v, want an example containing %q", tt.path, examples, want)
				}
			}
		})
	}
}

func examplesContain(examples []string, needle string) bool {
	for _, example := range examples {
		if strings.Contains(example, needle) {
			return true
		}
	}
	return false
}
