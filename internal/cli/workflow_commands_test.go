package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowA11yJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"workflow", "a11y", "https://example.test/app", "--wait", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow a11y exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Requests []struct {
			ID string `json:"id"`
		} `json:"requests"`
		Messages []struct {
			ID int `json:"id"`
		} `json:"messages"`
		Signals struct {
			ImagesWithoutAlt        int `json:"images_without_alt"`
			FormControlsWithoutName int `json:"form_controls_without_name"`
			HeadingSkips            int `json:"heading_skips"`
			FocusableWithoutLabel   int `json:"focusable_without_label"`
		} `json:"a11y"`
		Workflow struct {
			Name         string `json:"name"`
			IssueCount   int    `json:"issue_count"`
			RequestedURL string `json:"requested_url"`
			Partial      bool   `json:"partial"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow a11y output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "a11y" || got.Workflow.RequestedURL != "https://example.test/app" {
		t.Fatalf("workflow a11y = %+v, want complete workflow output", got)
	}
	if len(got.Requests) != 1 {
		t.Fatalf("workflow a11y requests = %+v, want one failed request", got.Requests)
	}
	if len(got.Messages) == 0 {
		t.Fatalf("workflow a11y messages = %+v, want at least one issue message", got.Messages)
	}
	if got.Workflow.Partial {
		t.Fatalf("workflow a11y = %+v, want no collector errors for synthetic page", got)
	}
	if got.Signals.ImagesWithoutAlt < 0 || got.Signals.FormControlsWithoutName < 0 || got.Signals.HeadingSkips < 0 || got.Signals.FocusableWithoutLabel < 0 {
		t.Fatalf("workflow a11y signals = %+v", got.Signals)
	}
	if got.Workflow.IssueCount != got.Signals.ImagesWithoutAlt+got.Signals.FormControlsWithoutName+got.Signals.HeadingSkips+got.Signals.FocusableWithoutLabel {
		t.Fatalf("workflow a11y summary = %+v, want issue_count to match signal sum", got)
	}
}

func TestWorkflowActionCaptureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	dir := t.TempDir()
	outPath := filepath.Join(dir, "action.local.json")
	beforePath := filepath.Join(dir, "before.png")
	afterPath := filepath.Join(dir, "after.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "action-capture",
		"--action", "insert-text:hello",
		"--selector", "[contenteditable=true]",
		"--wait-before", "0s",
		"--wait-after", "0s",
		"--include", "network,websocket,console,dom,text,storage-diff",
		"--before-screenshot", beforePath,
		"--after-screenshot", afterPath,
		"--out", outPath,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow action-capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Workflow struct {
			Name    string   `json:"name"`
			Include []string `json:"include"`
		} `json:"workflow"`
		Action struct {
			Type   string `json:"type"`
			Result struct {
				Strategy string `json:"strategy"`
				Value    string `json:"value"`
			} `json:"result"`
		} `json:"action"`
		Requests    []map[string]any `json:"requests"`
		WebSockets  []map[string]any `json:"websockets"`
		Messages    []map[string]any `json:"messages"`
		StorageDiff struct {
			HasDiff bool `json:"has_diff"`
		} `json:"storage_diff"`
		Artifacts []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"artifacts"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow action-capture output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "action-capture" || got.Action.Type != "insert-text" || got.Action.Result.Strategy != "insert-text" || got.Action.Result.Value != "beforehello" {
		t.Fatalf("workflow action-capture = %+v, want insert-text action result", got)
	}
	if len(got.Requests) == 0 || len(got.WebSockets) == 0 || len(got.Messages) == 0 || got.Artifact.Path != outPath {
		t.Fatalf("workflow action-capture collectors = %+v, want network, websocket, console, and artifact", got)
	}
	if _, err := os.Stat(beforePath); err != nil {
		t.Fatalf("before screenshot was not written: %v", err)
	}
	if _, err := os.Stat(afterPath); err != nil {
		t.Fatalf("after screenshot was not written: %v", err)
	}
}

func TestWorkflowConsoleErrorsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "console-errors", "--wait", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow console-errors exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Workflow struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"workflow"`
		Messages []struct {
			Type       string          `json:"type"`
			Level      string          `json:"level"`
			Text       string          `json:"text"`
			Exception  json.RawMessage `json:"exception"`
			StackTrace json.RawMessage `json:"stack_trace"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow console-errors output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "console-errors" || got.Workflow.Count != 3 || got.Messages[0].Level != "error" {
		t.Fatalf("workflow console-errors = %+v, want error summary", got)
	}
	if got.Messages[1].Type != "exception" || !strings.Contains(got.Messages[1].Text, "failed to fetch dashboard") || len(got.Messages[1].Exception) == 0 || len(got.Messages[1].StackTrace) == 0 {
		t.Fatalf("workflow console exception = %+v, want reason, exception, and stack", got.Messages[1])
	}
}

func TestWorkflowNetworkFailuresJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "network-failures", "--wait", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow network-failures exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Workflow struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"workflow"`
		Requests []struct {
			ID     string `json:"id"`
			Failed bool   `json:"failed"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow network-failures output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "network-failures" || got.Workflow.Count != 1 || got.Requests[0].ID != "request-failed" {
		t.Fatalf("workflow network-failures = %+v, want failed request summary", got)
	}
}

func TestWorkflowDebugBundleJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "debug-bundle", "--url", "https://example.test/app", "--since", "250ms", "--out-dir", outDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow debug-bundle exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"target"`
		Requests []struct {
			ID     string `json:"id"`
			Failed bool   `json:"failed"`
		} `json:"requests"`
		Messages []struct {
			ID int `json:"id"`
		} `json:"messages"`
		Snapshot struct {
			Count int    `json:"count"`
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"snapshot"`
		Evidence struct {
			Requests int `json:"requests"`
			Messages int `json:"messages"`
		} `json:"evidence"`
		Artifacts []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"artifacts"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
		Workflow struct {
			Name              string `json:"name"`
			RequestedURL      string `json:"requested_url"`
			RequestCount      int    `json:"request_count"`
			MessageCount      int    `json:"message_count"`
			RequestsTruncated bool   `json:"requests_truncated"`
			MessagesTruncated bool   `json:"messages_truncated"`
			Partial           bool   `json:"partial"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow debug-bundle output is invalid JSON: %v", err)
	}

	expectedURL, err := url.Parse("https://example.test/app")
	if err != nil {
		t.Fatalf("invalid expected URL: %v", err)
	}
	targetURL, err := url.Parse(got.Target.URL)
	if err != nil {
		t.Fatalf("invalid target URL %q: %v", got.Target.URL, err)
	}
	if !got.OK || got.Target.ID == "" || got.Target.Type != "page" || got.Target.Title == "" || targetURL.Host != expectedURL.Host || targetURL.Scheme != expectedURL.Scheme {
		t.Fatalf("workflow debug-bundle target = %+v, want selected page target", got.Target)
	}
	if got.Workflow.Name != "debug-bundle" || got.Workflow.RequestedURL != "https://example.test/app" {
		t.Fatalf("workflow debug-bundle metadata = %+v, want debug-bundle workflow metadata", got.Workflow)
	}
	if len(got.Requests) < 2 || len(got.Messages) == 0 || got.Evidence.Requests == 0 || got.Evidence.Messages == 0 || got.Snapshot.Count == 0 {
		t.Fatalf("workflow debug-bundle evidence = %+v, want requests, messages, and snapshot", got)
	}
	hasFailed := false
	for _, request := range got.Requests {
		if request.Failed {
			hasFailed = true
			break
		}
	}
	if !hasFailed {
		t.Fatalf("workflow debug-bundle requests = %+v, want at least one failed request", got.Requests)
	}
	if len(got.Requests) != got.Workflow.RequestCount {
		t.Fatalf("workflow request_count = %d, got %d requests", got.Workflow.RequestCount, len(got.Requests))
	}
	if len(got.Messages) != got.Workflow.MessageCount {
		t.Fatalf("workflow message_count = %d, got %d messages", got.Workflow.MessageCount, len(got.Messages))
	}
	if got.Workflow.RequestsTruncated || got.Workflow.MessagesTruncated {
		t.Fatalf("workflow debug-bundle = %+v, expect no truncation in synthetic window", got.Workflow)
	}
	if got.Workflow.Partial {
		t.Fatalf("workflow debug-bundle = %+v, expect zero collector errors with synthetic events", got.Workflow)
	}
	snapshotURL, err := url.Parse(got.Snapshot.URL)
	if err != nil {
		t.Fatalf("invalid snapshot URL %q: %v", got.Snapshot.URL, err)
	}
	if snapshotURL.Host != targetURL.Host {
		t.Fatalf("workflow snapshot url = %q, want same host as target %q", got.Snapshot.URL, got.Target.URL)
	}
	if got.Snapshot.Title != got.Target.Title {
		t.Fatalf("workflow snapshot title = %q, want %q", got.Snapshot.Title, got.Target.Title)
	}
	if len(got.Artifacts) < 5 {
		t.Fatalf("workflow artifacts = %+v, want artifact list with bundle + evidence", got.Artifacts)
	}
	if got.Artifact.Path == "" {
		t.Fatalf("workflow artifact path = %q, want non-empty", got.Artifact.Path)
	}
	if filepath.Dir(got.Artifact.Path) != filepath.Clean(outDir) {
		t.Fatalf("workflow artifact path = %s, want inside %q", got.Artifact.Path, outDir)
	}
	if _, err := os.Stat(got.Artifact.Path); err != nil {
		t.Fatalf("workflow artifact file was not written: %v", err)
	}
	requiredArtifacts := map[string]struct{}{
		"workflow-debug-bundle-bundle":        {},
		"workflow-debug-bundle-network":       {},
		"workflow-debug-bundle-console":       {},
		"workflow-debug-bundle-page-metadata": {},
		"workflow-debug-bundle-snapshot":      {},
		"workflow-debug-bundle-workflow":      {},
	}
	seenArtifacts := map[string]struct{}{}
	artifactInBundleList := false
	for _, artifact := range got.Artifacts {
		if artifact.Path == "" || artifact.Type == "" {
			t.Fatalf("workflow artifacts = %+v, want typed file metadata", got.Artifacts)
		}
		if artifact.Path == got.Artifact.Path {
			artifactInBundleList = true
		}
		seenArtifacts[artifact.Type] = struct{}{}
		if _, err := os.Stat(artifact.Path); err != nil {
			t.Fatalf("workflow artifact %s was not written: %v", artifact.Path, err)
		}
		if filepath.Dir(artifact.Path) != filepath.Clean(outDir) {
			t.Fatalf("workflow artifact %q path %q, want inside %q", artifact.Type, artifact.Path, outDir)
		}
	}
	if !artifactInBundleList {
		t.Fatalf("workflow artifacts = %+v, want bundle path included in artifacts", got.Artifacts)
	}
	for artifactType := range requiredArtifacts {
		if _, ok := seenArtifacts[artifactType]; !ok {
			t.Fatalf("workflow artifacts = %+v, missing required type %q", got.Artifacts, artifactType)
		}
	}
}

func TestWorkflowVerifyJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "verify.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "verify", "https://example.test/app", "--wait", "250ms", "--out", outPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow verify exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Requests []struct {
			ID     string `json:"id"`
			Failed bool   `json:"failed"`
		} `json:"requests"`
		Messages []struct {
			Level string `json:"level"`
		} `json:"messages"`
		Workflow struct {
			Name         string `json:"name"`
			RequestedURL string `json:"requested_url"`
		} `json:"workflow"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow verify output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "verify" || got.Workflow.RequestedURL != "https://example.test/app" {
		t.Fatalf("workflow verify = %+v, want ok verification workflow result", got)
	}
	if len(got.Requests) != 1 || got.Requests[0].ID != "request-failed" || !got.Requests[0].Failed {
		t.Fatalf("workflow verify requests = %+v, want one failed request", got.Requests)
	}
	if len(got.Messages) == 0 {
		t.Fatalf("workflow verify messages = %+v, want at least one console/network message", got.Messages)
	}
	if got.Artifact.Path != outPath {
		t.Fatalf("workflow verify artifact = %+v, want artifact at %s", got.Artifact, outPath)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("workflow verify artifact was not written: %v", err)
	}
}

func TestWorkflowPageLoadJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "page-load.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "page-load", "https://example.test/app", "--wait", "250ms", "--out", outPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow page-load exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Requests []struct {
			ID     string `json:"id"`
			Status int    `json:"status"`
		} `json:"requests"`
		Messages []struct {
			Text string `json:"text"`
		} `json:"messages"`
		Workflow struct {
			Name         string `json:"name"`
			Trigger      string `json:"trigger"`
			RequestedURL string `json:"requested_url"`
			Partial      bool   `json:"partial"`
		} `json:"workflow"`
		Storage struct {
			LocalStorageKeys []string `json:"local_storage_keys"`
		} `json:"storage"`
		Performance struct {
			Count int `json:"count"`
		} `json:"performance"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow page-load output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "page-load" || got.Workflow.Trigger != "navigate" || got.Workflow.RequestedURL != "https://example.test/app" || got.Workflow.Partial {
		t.Fatalf("workflow page-load metadata = %+v, want complete navigate workflow", got.Workflow)
	}
	if len(got.Requests) != 2 || got.Requests[0].Status != 200 || len(got.Messages) != 3 || !strings.Contains(got.Messages[1].Text, "failed to fetch dashboard") {
		t.Fatalf("workflow page-load evidence requests=%+v messages=%+v, want network and rich console evidence", got.Requests, got.Messages)
	}
	if len(got.Storage.LocalStorageKeys) != 1 || got.Storage.LocalStorageKeys[0] != "feature" || got.Performance.Count != 2 || got.Artifact.Path != outPath {
		t.Fatalf("workflow page-load storage/performance/artifact = storage=%+v performance=%+v artifact=%+v", got.Storage, got.Performance, got.Artifact)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("page-load artifact was not written: %v", err)
	}
}

func TestWorkflowRenderedExtractJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outDir := t.TempDir()
	rawURL := "https://www.google.com/search?q=agentic+engineering+2026+evolutions&safe=active&tbs=qdr:m"
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "rendered-extract", rawURL, "--serp", "google", "--out-dir", outDir, "--wait", "1500ms", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow rendered-extract exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK        bool                 `json:"ok"`
		Target    struct{ URL string } `json:"target"`
		Readiness struct {
			NavigatedFromAboutBlank bool   `json:"navigated_from_about_blank"`
			DocumentReadyState      string `json:"document_ready_state"`
			UsefulContentSeen       bool   `json:"useful_content_seen"`
			ContentStableSeen       bool   `json:"content_stable_seen"`
			StablePolls             int    `json:"stable_polls"`
			PollCount               int    `json:"poll_count"`
		} `json:"readiness"`
		Artifacts struct {
			VisibleJSON string `json:"visible_json"`
			VisibleTXT  string `json:"visible_txt"`
			HTMLJSON    string `json:"html_json"`
			Markdown    string `json:"markdown"`
			LinksJSON   string `json:"links_json"`
		} `json:"artifacts"`
		Quality struct {
			SnapshotCount     int `json:"snapshot_count"`
			VisibleWordCount  int `json:"visible_word_count"`
			HTMLLength        int `json:"html_length"`
			MarkdownWordCount int `json:"markdown_word_count"`
			ExternalLinkCount int `json:"external_link_count"`
		} `json:"quality"`
		Links struct {
			Query      string `json:"query"`
			TimeFilter string `json:"time_filter"`
			Serp       string `json:"serp"`
		} `json:"links"`
		Warnings []string `json:"warnings"`
		Workflow struct {
			Name   string `json:"name"`
			Closed bool   `json:"closed"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow rendered-extract output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "rendered-extract" || !got.Workflow.Closed || !got.Readiness.NavigatedFromAboutBlank || got.Readiness.DocumentReadyState != "complete" || !got.Readiness.UsefulContentSeen || !got.Readiness.ContentStableSeen || got.Readiness.StablePolls < 2 || got.Readiness.PollCount < 3 {
		t.Fatalf("workflow rendered-extract metadata = %+v readiness=%+v", got.Workflow, got.Readiness)
	}
	if got.Target.URL == "about:blank" || got.Links.Query != "agentic engineering 2026 evolutions" || got.Links.TimeFilter != "qdr:m" || got.Links.Serp != "google" {
		t.Fatalf("workflow rendered-extract target/links = target=%+v links=%+v", got.Target, got.Links)
	}
	if got.Quality.SnapshotCount == 0 || got.Quality.VisibleWordCount == 0 || got.Quality.HTMLLength == 0 || got.Quality.MarkdownWordCount == 0 || got.Quality.ExternalLinkCount == 0 || len(got.Warnings) != 0 {
		t.Fatalf("workflow rendered-extract quality=%+v warnings=%+v", got.Quality, got.Warnings)
	}
	for _, path := range []string{got.Artifacts.VisibleJSON, got.Artifacts.VisibleTXT, got.Artifacts.HTMLJSON, got.Artifacts.Markdown, got.Artifacts.LinksJSON} {
		if path == "" {
			t.Fatalf("workflow rendered-extract artifacts = %+v, want all artifact paths", got.Artifacts)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("workflow rendered-extract artifact %q was not written: %v", path, err)
		}
		if !strings.HasPrefix(path, outDir) {
			t.Fatalf("workflow rendered-extract artifact %q, want under %q", path, outDir)
		}
	}
	linksBytes, err := os.ReadFile(got.Artifacts.LinksJSON)
	if err != nil {
		t.Fatalf("read links artifact: %v", err)
	}
	if !strings.Contains(string(linksBytes), "https://example.test/story") || strings.Contains(string(linksBytes), "google.com/url") {
		t.Fatalf("links artifact = %s, want decoded external result", string(linksBytes))
	}
}

func TestWorkflowWebResearchSERPPaginates(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("agentic engineering\tqdr:m\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "research")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--result-pages", "2", "--max-candidates", "20", "--parallel", "3", "--out-dir", outDir, "--wait", "250ms", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		SERPs []struct {
			Query    string `json:"query"`
			SerpPage int    `json:"serp_page"`
			Report   struct {
				Artifacts struct {
					Markdown string `json:"markdown"`
				} `json:"artifacts"`
			} `json:"report"`
		} `json:"serps"`
		Candidates []struct {
			Query      string `json:"query"`
			TimeFilter string `json:"time_filter"`
			SerpPage   int    `json:"serp_page"`
			RankOnPage int    `json:"rank_on_page"`
			GlobalRank int    `json:"global_rank"`
			URL        string `json:"url"`
		} `json:"candidates"`
		Artifacts struct {
			CandidatesJSON string `json:"candidates_json"`
			CandidatesTSV  string `json:"candidates_tsv"`
		} `json:"artifacts"`
		Workflow struct {
			Name        string `json:"name"`
			QueryCount  int    `json:"query_count"`
			ResultPages int    `json:"result_pages"`
			Parallel    int    `json:"parallel"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "web-research-serp" || got.Workflow.QueryCount != 1 || got.Workflow.ResultPages != 2 || got.Workflow.Parallel != 3 {
		t.Fatalf("workflow web-research serp metadata = %+v", got.Workflow)
	}
	if len(got.SERPs) != 2 || got.SERPs[0].SerpPage != 1 || got.SERPs[1].SerpPage != 2 {
		t.Fatalf("workflow web-research serp pages = %+v", got.SERPs)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].SerpPage != 1 || got.Candidates[0].RankOnPage != 1 || got.Candidates[0].GlobalRank != 1 || got.Candidates[0].TimeFilter != "qdr:m" {
		t.Fatalf("workflow web-research candidates = %+v", got.Candidates)
	}
	for _, path := range []string{got.SERPs[0].Report.Artifacts.Markdown, got.SERPs[1].Report.Artifacts.Markdown, got.Artifacts.CandidatesJSON, got.Artifacts.CandidatesTSV} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("workflow web-research serp artifact %q was not written: %v", path, err)
		}
		if !strings.HasPrefix(path, outDir) {
			t.Fatalf("workflow web-research serp artifact %q, want under %q", path, outDir)
		}
	}
	if !strings.Contains(got.SERPs[0].Report.Artifacts.Markdown, filepath.Join("serps", "agentic-engineering", "page-1", "page.md")) || !strings.Contains(got.SERPs[1].Report.Artifacts.Markdown, filepath.Join("serps", "agentic-engineering", "page-2", "page.md")) {
		t.Fatalf("workflow web-research serp artifact layout = %+v", got.SERPs)
	}
}

func TestWorkflowWebResearchExtractJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	urlFile := filepath.Join(tmpDir, "urls.txt")
	if err := os.WriteFile(urlFile, []byte("https://example.test/story\nhttps://example.test/story#section\n"), 0o600); err != nil {
		t.Fatalf("write url file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "pages")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "extract", "--url-file", urlFile, "--max-pages", "1", "--parallel", "10", "--out-dir", outDir, "--wait", "250ms", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research extract exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Pages []struct {
			URL    string `json:"url"`
			Report struct {
				Artifacts struct {
					Markdown  string `json:"markdown"`
					LinksJSON string `json:"links_json"`
				} `json:"artifacts"`
				Workflow struct {
					Name string `json:"name"`
				} `json:"workflow"`
			} `json:"report"`
		} `json:"pages"`
		Quality []struct {
			URL      string   `json:"url"`
			Warnings []string `json:"warnings"`
		} `json:"quality"`
		Artifacts struct {
			PageQualityJSON string `json:"page_quality_json"`
			FailuresJSON    string `json:"failures_json"`
			FailedURLs      string `json:"failed_urls"`
			RemainingURLs   string `json:"remaining_urls"`
			RetryCommand    string `json:"retry_command"`
		} `json:"artifacts"`
		Workflow struct {
			Name         string `json:"name"`
			URLCount     int    `json:"url_count"`
			PageCount    int    `json:"page_count"`
			Parallel     int    `json:"parallel"`
			FailureCount int    `json:"failure_count"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research extract output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "web-research-extract" || got.Workflow.URLCount != 1 || got.Workflow.PageCount != 1 || got.Workflow.Parallel != 10 || got.Workflow.FailureCount != 0 {
		t.Fatalf("workflow web-research extract metadata = %+v", got.Workflow)
	}
	if len(got.Pages) != 1 || got.Pages[0].Report.Workflow.Name != "web-research-extract" || got.Pages[0].Report.Artifacts.Markdown == "" || got.Pages[0].Report.Artifacts.LinksJSON == "" {
		t.Fatalf("workflow web-research extract pages = %+v", got.Pages)
	}
	for _, path := range []string{got.Pages[0].Report.Artifacts.Markdown, got.Pages[0].Report.Artifacts.LinksJSON, got.Artifacts.PageQualityJSON, got.Artifacts.FailuresJSON, got.Artifacts.FailedURLs, got.Artifacts.RemainingURLs, got.Artifacts.RetryCommand} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("workflow web-research extract artifact %q was not written: %v", path, err)
		}
		if !strings.HasPrefix(path, outDir) {
			t.Fatalf("workflow web-research extract artifact %q, want under %q", path, outDir)
		}
	}
	if len(got.Quality) != 1 || len(got.Quality[0].Warnings) != 0 {
		t.Fatalf("workflow web-research extract quality = %+v", got.Quality)
	}
}

func TestWorkflowPerfJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "perf.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "perf", "https://example.test/app", "--wait", "250ms", "--trace", outPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow perf exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK          bool `json:"ok"`
		Performance struct {
			Metrics []struct {
				Name  string  `json:"name"`
				Value float64 `json:"value"`
			} `json:"metrics"`
		} `json:"performance"`
		Workflow struct {
			Name         string `json:"name"`
			RequestedURL string `json:"requested_url"`
			MetricCount  int    `json:"metric_count"`
			Partial      bool   `json:"partial"`
		} `json:"workflow"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow perf output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "perf" || got.Workflow.RequestedURL != "https://example.test/app" {
		t.Fatalf("workflow perf = %+v, want complete perf workflow result", got)
	}
	if len(got.Performance.Metrics) != got.Workflow.MetricCount {
		t.Fatalf("workflow perf = %+v, want metric count to match performance.metrics", got)
	}
	if got.Workflow.MetricCount == 0 || got.Artifact.Path != outPath || got.Workflow.Partial {
		t.Fatalf("workflow perf = %+v, want captured performance metrics and trace artifact", got)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("workflow perf artifact was not written: %v", err)
	}
}

func TestWorkflowVisiblePostsJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "visible-posts", "https://example.test/feed", "--wait", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow visible-posts exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Items []struct {
			Text string `json:"text"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow visible-posts output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Items) != 1 || got.Items[0].Text != "First visible synthetic post" {
		t.Fatalf("workflow visible-posts = %+v, want synthetic post", got)
	}
}

func TestWorkflowHackerNewsJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "hacker-news", "https://news.ycombinator.com/", "--wait", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow hacker-news exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK           bool              `json:"ok"`
		Organization map[string]string `json:"organization"`
		Stories      []struct {
			Rank     int    `json:"rank"`
			Title    string `json:"title"`
			Score    int    `json:"score"`
			Comments int    `json:"comments"`
		} `json:"stories"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow hacker-news output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Stories) != 1 || got.Stories[0].Title != "Synthetic HN story" || got.Stories[0].Score != 42 || got.Organization["story_row_selector"] != "tr.athing" {
		t.Fatalf("workflow hacker-news = %+v, want synthetic HN story and organization", got)
	}
}

func TestWorkflowHackerNewsHumanTable(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "hacker-news", "https://news.ycombinator.com/", "--wait", "0s"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow hacker-news exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	want := "rank  points  comments  title\n#1    42 pts 7 comments  Synthetic HN story\n"
	if out.String() != want {
		t.Fatalf("workflow hacker-news human output = %q, want %q", out.String(), want)
	}
}
