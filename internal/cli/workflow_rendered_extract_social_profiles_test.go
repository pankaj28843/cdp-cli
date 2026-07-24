package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowRenderedExtractUsesSocialNativeProfiles(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tests := []struct {
		name         string
		rawURL       string
		wantProfile  string
		wantMarkdown string
	}{
		{
			name:         "x",
			rawURL:       "https://x.com/karpathy/status/2079610838143623371",
			wantProfile:  "x",
			wantMarkdown: "# Synthetic X post",
		},
		{
			name:         "linkedin",
			rawURL:       "https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD",
			wantProfile:  "linkedin",
			wantMarkdown: "# Synthetic LinkedIn post",
		},
		{
			name:         "reddit any subreddit",
			rawURL:       "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/",
			wantProfile:  "reddit",
			wantMarkdown: "# Synthetic Reddit post",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			outDir := t.TempDir()
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{
				"workflow", "rendered-extract", tt.rawURL,
				"--content-extractor", "auto",
				"--out-dir", outDir,
				"--wait", "0",
				"--settle", "0",
				"--min-visible-words", "1",
				"--min-markdown-words", "1",
				"--min-html-chars", "1",
				"--json",
			}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("workflow rendered-extract exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
			}

			var got struct {
				Content struct {
					Profile          string `json:"profile"`
					Strategy         string `json:"strategy"`
					NativeAttempted  bool   `json:"native_attempted"`
					NativeSucceeded  bool   `json:"native_succeeded"`
					FallbackUsed     bool   `json:"fallback_used"`
					DiscussionLimit  int    `json:"discussion_limit"`
					DiscussionStatus string `json:"discussion_status"`
				} `json:"content"`
				Quality struct {
					Passed bool `json:"passed"`
				} `json:"quality"`
				Workflow struct {
					Partial bool `json:"partial"`
				} `json:"workflow"`
				Artifacts struct {
					Markdown string `json:"markdown"`
				} `json:"artifacts"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("decode rendered extraction: %v", err)
			}
			if got.Content.Profile != tt.wantProfile || got.Content.Strategy != "semantic-dom" ||
				!got.Content.NativeAttempted || !got.Content.NativeSucceeded || got.Content.FallbackUsed ||
				got.Content.DiscussionLimit != 500 || got.Content.DiscussionStatus != "exhausted" ||
				!got.Quality.Passed || got.Workflow.Partial {
				t.Fatalf("content provenance = %+v", got.Content)
			}
			markdown, err := os.ReadFile(got.Artifacts.Markdown)
			if err != nil {
				t.Fatalf("read markdown: %v", err)
			}
			if !strings.Contains(string(markdown), tt.wantMarkdown) {
				t.Fatalf("markdown missing %q:\n%s", tt.wantMarkdown, markdown)
			}
		})
	}
}

func TestWorkflowRenderedExtractUsesSocialProfileFeeds(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	for _, tt := range []struct{ rawURL, profile string }{
		{"https://x.com/karpathy", "x-profile"},
		{"https://www.reddit.com/user/CelticPaladin/", "reddit-user-profile"},
		{"https://www.linkedin.com/company/the-pragmatic-engineer/posts/", "linkedin-company-posts"},
	} {
		t.Run(tt.profile, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{
				"workflow", "rendered-extract", tt.rawURL,
				"--content-extractor", "auto", "--out-dir", t.TempDir(),
				"--wait", "0", "--settle", "0", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json",
			}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			var got struct {
				Content struct {
					Profile         string `json:"profile"`
					NativeSucceeded bool   `json:"native_succeeded"`
					FallbackUsed    bool   `json:"fallback_used"`
				} `json:"content"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Content.Profile != tt.profile || !got.Content.NativeSucceeded || got.Content.FallbackUsed {
				t.Fatalf("content=%+v", got.Content)
			}
		})
	}
}

func TestWorkflowWebResearchExtractUsesSocialNativeProfiles(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	urlFile := filepath.Join(tmpDir, "urls.txt")
	if err := os.WriteFile(urlFile, []byte(strings.Join([]string{
		"https://x.com/karpathy/status/2079610838143623371",
		"https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD",
		"https://www.reddit.com/r/any_subreddit/comments/1v010h6/the_sun_came_out/",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write URL file: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "web-research", "extract",
		"--url-file", urlFile,
		"--content-extractor", "auto",
		"--parallel", "1",
		"--out-dir", filepath.Join(tmpDir, "pages"),
		"--wait", "0",
		"--settle", "0",
		"--min-visible-words", "1",
		"--min-markdown-words", "1",
		"--min-html-chars", "1",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research extract exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		Workflow struct {
			ContentExtractor string `json:"content_extractor"`
		} `json:"workflow"`
		Pages []struct {
			Report struct {
				Content struct {
					Profile         string `json:"profile"`
					NativeSucceeded bool   `json:"native_succeeded"`
					FallbackUsed    bool   `json:"fallback_used"`
				} `json:"content"`
			} `json:"report"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode web-research extraction: %v", err)
	}
	if got.Workflow.ContentExtractor != "auto" || len(got.Pages) != 3 {
		t.Fatalf("workflow/pages = %+v/%+v", got.Workflow, got.Pages)
	}
	seen := map[string]bool{}
	for _, page := range got.Pages {
		content := page.Report.Content
		if !content.NativeSucceeded || content.FallbackUsed {
			t.Fatalf("page content = %+v, want native extraction", content)
		}
		seen[content.Profile] = true
	}
	for _, wantProfile := range []string{"x", "linkedin", "reddit"} {
		if !seen[wantProfile] {
			t.Fatalf("native profiles = %#v, missing %q", seen, wantProfile)
		}
	}
}
