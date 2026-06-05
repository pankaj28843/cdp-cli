package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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
	evidenceDir := filepath.Join(dir, "evidence")
	beforePath := filepath.Join(dir, "before.png")
	afterPath := filepath.Join(dir, "after.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "action-capture",
		"--action", "insert-text:hello",
		"--selector", "[contenteditable=true]",
		"--wait-before", "0s",
		"--wait-after", "0s",
		"--include", "network,websocket,console,dom,text,a11y,screenshot,storage-diff",
		"--a11y-depth", "4",
		"--a11y-limit", "10",
		"--before-screenshot", beforePath,
		"--after-screenshot", afterPath,
		"--screenshot-full-page",
		"--evidence-out-dir", evidenceDir,
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
		Evidence struct {
			ArtifactCount int `json:"artifact_count"`
			Before        struct {
				Screenshot struct {
					Bytes    int  `json:"bytes"`
					FullPage bool `json:"full_page"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"screenshot"`
				Text struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"text"`
				DOM struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"dom"`
				A11y struct {
					Count     int  `json:"count"`
					Truncated bool `json:"truncated"`
					Artifact  struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"a11y"`
			} `json:"before"`
			After struct {
				Screenshot struct {
					Bytes    int  `json:"bytes"`
					FullPage bool `json:"full_page"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"screenshot"`
				Text struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"text"`
				DOM struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"dom"`
				A11y struct {
					Count     int  `json:"count"`
					Truncated bool `json:"truncated"`
					Artifact  struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"a11y"`
			} `json:"after"`
			Events struct {
				Network struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"network"`
				WebSockets struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"websockets"`
				Console struct {
					Count    int `json:"count"`
					Artifact struct {
						Type string `json:"type"`
						Path string `json:"path"`
					} `json:"artifact"`
				} `json:"console"`
			} `json:"events"`
			Manifest struct {
				ReferencedArtifactCount int `json:"referenced_artifact_count"`
				CollectorErrorCount     int `json:"collector_error_count"`
				Artifact                struct {
					Type string `json:"type"`
					Path string `json:"path"`
				} `json:"artifact"`
			} `json:"manifest"`
		} `json:"evidence"`
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
	wantEvidence := map[string]string{
		"workflow-action-capture-before-screenshot": filepath.Join(evidenceDir, "action-capture.before.screenshot.png"),
		"workflow-action-capture-before-text":       filepath.Join(evidenceDir, "action-capture.before.text.json"),
		"workflow-action-capture-before-dom":        filepath.Join(evidenceDir, "action-capture.before.dom.json"),
		"workflow-action-capture-before-a11y":       filepath.Join(evidenceDir, "action-capture.before.a11y.json"),
		"workflow-action-capture-after-screenshot":  filepath.Join(evidenceDir, "action-capture.after.screenshot.png"),
		"workflow-action-capture-after-text":        filepath.Join(evidenceDir, "action-capture.after.text.json"),
		"workflow-action-capture-after-dom":         filepath.Join(evidenceDir, "action-capture.after.dom.json"),
		"workflow-action-capture-after-a11y":        filepath.Join(evidenceDir, "action-capture.after.a11y.json"),
		"workflow-action-capture-action-network":    filepath.Join(evidenceDir, "action-capture.action.network.json"),
		"workflow-action-capture-action-websockets": filepath.Join(evidenceDir, "action-capture.action.websockets.json"),
		"workflow-action-capture-action-console":    filepath.Join(evidenceDir, "action-capture.action.console.json"),
		"workflow-action-capture-manifest":          filepath.Join(evidenceDir, "action-capture.manifest.json"),
	}
	if got.Evidence.ArtifactCount != len(wantEvidence) ||
		got.Evidence.Before.Screenshot.Bytes == 0 ||
		!got.Evidence.Before.Screenshot.FullPage ||
		got.Evidence.Before.Text.Count == 0 ||
		got.Evidence.Before.DOM.Count == 0 ||
		got.Evidence.Before.A11y.Count == 0 ||
		got.Evidence.After.Screenshot.Bytes == 0 ||
		!got.Evidence.After.Screenshot.FullPage ||
		got.Evidence.After.Text.Count == 0 ||
		got.Evidence.After.DOM.Count == 0 ||
		got.Evidence.After.A11y.Count == 0 ||
		got.Evidence.Events.Network.Count == 0 ||
		got.Evidence.Events.WebSockets.Count == 0 ||
		got.Evidence.Events.Console.Count == 0 ||
		got.Evidence.Manifest.ReferencedArtifactCount != len(wantEvidence)+1 {
		t.Fatalf("workflow action-capture evidence = %+v, want before/after and event evidence", got.Evidence)
	}
	if got.Evidence.Before.Screenshot.Artifact.Path != wantEvidence["workflow-action-capture-before-screenshot"] ||
		got.Evidence.Before.Text.Artifact.Path != wantEvidence["workflow-action-capture-before-text"] ||
		got.Evidence.After.Screenshot.Artifact.Path != wantEvidence["workflow-action-capture-after-screenshot"] ||
		got.Evidence.After.DOM.Artifact.Path != wantEvidence["workflow-action-capture-after-dom"] ||
		got.Evidence.Before.A11y.Artifact.Path != wantEvidence["workflow-action-capture-before-a11y"] ||
		got.Evidence.After.A11y.Artifact.Path != wantEvidence["workflow-action-capture-after-a11y"] ||
		got.Evidence.Events.Network.Artifact.Path != wantEvidence["workflow-action-capture-action-network"] ||
		got.Evidence.Events.Console.Artifact.Path != wantEvidence["workflow-action-capture-action-console"] ||
		got.Evidence.Manifest.Artifact.Path != wantEvidence["workflow-action-capture-manifest"] {
		t.Fatalf("workflow action-capture evidence paths = %+v, want stable before/after artifact paths", got.Evidence)
	}
	seenEvidence := map[string]string{}
	for _, artifact := range got.Artifacts {
		if _, ok := wantEvidence[artifact.Type]; ok {
			seenEvidence[artifact.Type] = artifact.Path
		}
	}
	for typ, path := range wantEvidence {
		if seenEvidence[typ] != path {
			t.Fatalf("workflow action-capture artifacts = %+v, missing %s at %s", got.Artifacts, typ, path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("evidence artifact %s was not written: %v", path, err)
		}
		if strings.Contains(typ, "screenshot") {
			if !bytes.Contains(raw, []byte("synthetic screenshot")) {
				t.Fatalf("screenshot evidence artifact %s = %q, want screenshot bytes", path, string(raw))
			}
			continue
		}
		if typ == "workflow-action-capture-manifest" {
			if !bytes.Contains(raw, []byte(`"workflow"`)) ||
				!bytes.Contains(raw, []byte(`"evidence"`)) ||
				!bytes.Contains(raw, []byte(`"artifacts"`)) ||
				bytes.Contains(raw, []byte(`"hello"`)) {
				t.Fatalf("manifest artifact %s = %s, want manifest metadata without typed text payload", path, string(raw))
			}
			continue
		}
		if !bytes.Contains(raw, []byte(`"phase"`)) || !bytes.Contains(raw, []byte(`"collector"`)) {
			t.Fatalf("evidence artifact %s = %s, want phase and collector metadata", path, string(raw))
		}
	}
	if _, err := os.Stat(beforePath); err != nil {
		t.Fatalf("before screenshot was not written: %v", err)
	}
	if _, err := os.Stat(afterPath); err != nil {
		t.Fatalf("after screenshot was not written: %v", err)
	}
}

func TestWorkflowActionCaptureA11yRequiresEvidenceOutDir(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "action-capture",
		"--action", "press:Enter",
		"--selector", "body",
		"--include", "a11y",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("workflow action-capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow action-capture usage output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || !strings.Contains(got.Message, "--evidence-out-dir") {
		t.Fatalf("workflow action-capture usage = %+v, want evidence-out-dir usage error", got)
	}
}

func TestWorkflowActionCaptureScreenshotRequiresEvidenceOutDir(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "action-capture",
		"--action", "press:Enter",
		"--selector", "body",
		"--include", "screenshot",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("workflow action-capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow action-capture usage output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || !strings.Contains(got.Message, "--include screenshot requires --evidence-out-dir") {
		t.Fatalf("workflow action-capture usage = %+v, want screenshot evidence-out-dir usage error", got)
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
		ContentState struct {
			Class           string `json:"class"`
			FinalURL        string `json:"final_url"`
			MainStatus      int    `json:"main_status"`
			Actionable      bool   `json:"actionable"`
			TextSampleBytes int    `json:"text_sample_bytes"`
		} `json:"content_state"`
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
	if got.ContentState.Class != "content" || got.ContentState.FinalURL != "https://example.test/current" || got.ContentState.MainStatus != 200 || !got.ContentState.Actionable || got.ContentState.TextSampleBytes == 0 {
		t.Fatalf("workflow page-load content_state = %+v, want actionable content classification", got.ContentState)
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
			Serp       string `json:"serp"`
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
			Serp        string `json:"serp"`
			QueryCount  int    `json:"query_count"`
			ResultPages int    `json:"result_pages"`
			Parallel    int    `json:"parallel"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "web-research-serp" || got.Workflow.Serp != "google" || got.Workflow.QueryCount != 1 || got.Workflow.ResultPages != 2 || got.Workflow.Parallel != 3 {
		t.Fatalf("workflow web-research serp metadata = %+v", got.Workflow)
	}
	if len(got.SERPs) != 2 || got.SERPs[0].SerpPage != 1 || got.SERPs[1].SerpPage != 2 {
		t.Fatalf("workflow web-research serp pages = %+v", got.SERPs)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].Serp != "google" || got.Candidates[0].SerpPage != 1 || got.Candidates[0].RankOnPage != 1 || got.Candidates[0].GlobalRank != 1 || got.Candidates[0].TimeFilter != "qdr:m" {
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

func TestWorkflowWebResearchSERPSupportsMultipleEngines(t *testing.T) {
	engines := []string{"google", "bing", "brave", "duckduckgo", "kagi"}
	for _, engine := range engines {
		t.Run(engine, func(t *testing.T) {
			server := newFakeCDPServer(t, nil)
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			tmpDir := t.TempDir()
			queryFile := filepath.Join(tmpDir, "queries.txt")
			if err := os.WriteFile(queryFile, []byte("agentic engineering\n"), 0o600); err != nil {
				t.Fatalf("write query file: %v", err)
			}
			outDir := filepath.Join(tmpDir, "research")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--serp", engine, "--result-pages", "1", "--out-dir", outDir, "--wait", "250ms", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("workflow web-research serp exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
			}
			var got struct {
				OK    bool `json:"ok"`
				SERPs []struct {
					Serp string `json:"serp"`
				} `json:"serps"`
				Candidates []struct {
					Serp string `json:"serp"`
				} `json:"candidates"`
				Workflow struct {
					Serp string `json:"serp"`
				} `json:"workflow"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
			}
			if !got.OK || got.Workflow.Serp != engine || len(got.SERPs) != 1 || got.SERPs[0].Serp != engine || len(got.Candidates) != 1 || got.Candidates[0].Serp != engine {
				t.Fatalf("workflow web-research serp engine metadata = %+v", got)
			}
		})
	}
}

func TestWorkflowWebResearchSERPRunsMultipleEnginesInOneCommand(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	fakeTargetCreateCount.Store(0)

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("agentic engineering\nplaywright parity\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "research")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--serp", "google,bing", "--fallback-serp", "none", "--result-pages", "1", "--parallel", "3", "--out-dir", outDir, "--wait", "1s", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK    bool `json:"ok"`
		SERPs []struct {
			Serp string `json:"serp"`
		} `json:"serps"`
		Workflow struct {
			Serp                string   `json:"serp"`
			Serps               []string `json:"serps"`
			EngineCount         int      `json:"engine_count"`
			ParallelEngines     bool     `json:"parallel_engines"`
			ParallelEngineCount int      `json:"parallel_engine_count"`
			PerEngineParallel   int      `json:"per_engine_parallel"`
			EngineLanes         []struct {
				Serp        string `json:"serp"`
				PageReused  bool   `json:"page_reused"`
				CreatedPage bool   `json:"created_page"`
				JobCount    int    `json:"job_count"`
			} `json:"engine_lanes"`
			ScheduledResultPages int    `json:"scheduled_result_pages"`
			CompletedResultPages int    `json:"completed_result_pages"`
			FallbackSerp         string `json:"fallback_serp"`
			ResolvedFallbackSerp string `json:"resolved_fallback_serp"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Serp != "google,bing" || !reflect.DeepEqual(got.Workflow.Serps, []string{"google", "bing"}) || got.Workflow.EngineCount != 2 || !got.Workflow.ParallelEngines || got.Workflow.ParallelEngineCount != 2 || got.Workflow.PerEngineParallel != 1 {
		t.Fatalf("workflow multi-engine metadata = %+v", got.Workflow)
	}
	if got.Workflow.ScheduledResultPages != 4 || got.Workflow.CompletedResultPages != 4 || got.Workflow.FallbackSerp != "none" || got.Workflow.ResolvedFallbackSerp != "none" {
		t.Fatalf("workflow multi-engine counts/fallback = %+v", got.Workflow)
	}
	if len(got.Workflow.EngineLanes) != 2 || got.Workflow.EngineLanes[0].Serp != "google" || got.Workflow.EngineLanes[1].Serp != "bing" {
		t.Fatalf("workflow engine lanes = %+v, want deterministic one lane per engine", got.Workflow.EngineLanes)
	}
	for _, lane := range got.Workflow.EngineLanes {
		if !lane.PageReused || !lane.CreatedPage || lane.JobCount != 2 {
			t.Fatalf("workflow engine lane = %+v, want one created reusable page handling two jobs", lane)
		}
	}
	if creates := fakeTargetCreateCount.Load(); creates != 2 {
		t.Fatalf("Target.createTarget calls = %d, want one reusable page per engine", creates)
	}
	if len(got.SERPs) != 4 || got.SERPs[0].Serp != "google" || got.SERPs[1].Serp != "google" || got.SERPs[2].Serp != "bing" || got.SERPs[3].Serp != "bing" {
		t.Fatalf("serp reports = %+v, want deterministic google then bing order", got.SERPs)
	}
}

func TestWorkflowWebResearchSERPFastFailsBlockedPages(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("serp block fixture one\nserp block fixture two\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "research")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--result-pages", "3", "--fast-fail-blocked", "--blocked-failure-threshold", "2", "--progress", "stderr", "--parallel", "1", "--out-dir", outDir, "--wait", "250ms", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		SERPs []struct {
			Query    string `json:"query"`
			SerpPage int    `json:"serp_page"`
		} `json:"serps"`
		Failures []struct {
			ErrClass string `json:"err_class"`
		} `json:"failures"`
		Warnings []string `json:"warnings"`
		Workflow struct {
			FailureCount            int  `json:"failure_count"`
			ScheduledResultPages    int  `json:"scheduled_result_pages"`
			FastFailBlocked         bool `json:"fast_fail_blocked"`
			BlockedFailureThreshold int  `json:"blocked_failure_threshold"`
			FastFailTriggered       bool `json:"fast_fail_triggered"`
		} `json:"workflow"`
		Artifacts struct {
			CandidatesJSON string `json:"candidates_json"`
			CandidatesTSV  string `json:"candidates_tsv"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
	}
	if got.OK || len(got.SERPs) != 2 || len(got.Failures) != 2 || got.Workflow.FailureCount != 2 {
		t.Fatalf("workflow web-research fast-fail summary = %+v, want two blocked failures", got)
	}
	if !got.Workflow.FastFailBlocked || !got.Workflow.FastFailTriggered || got.Workflow.BlockedFailureThreshold != 2 || got.Workflow.ScheduledResultPages != 2 {
		t.Fatalf("workflow web-research fast-fail metadata = %+v, want early stop after two scheduled pages", got.Workflow)
	}
	for _, serp := range got.SERPs {
		if serp.Query != "serp block fixture one" {
			t.Fatalf("workflow web-research fast-fail scheduled query %q, want only first blocked query", serp.Query)
		}
	}
	for _, failure := range got.Failures {
		if failure.ErrClass != "serp_blocked" {
			t.Fatalf("failure = %+v, want serp_blocked", failure)
		}
	}
	if !testContainsSubstring(got.Warnings, "stopped early") {
		t.Fatalf("warnings = %+v, want fast-fail warning", got.Warnings)
	}
	progressLines := strings.Split(strings.TrimSpace(errOut.String()), "\n")
	if len(progressLines) != 2 {
		t.Fatalf("progress stderr = %q, want two JSONL events", errOut.String())
	}
	for _, line := range progressLines {
		var event struct {
			Event    string `json:"event"`
			Blocked  bool   `json:"blocked"`
			ErrClass string `json:"err_class"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("progress event %q is invalid JSON: %v", line, err)
		}
		if event.Event != "serp_page_complete" || !event.Blocked || event.ErrClass != "serp_blocked" {
			t.Fatalf("progress event = %+v, want blocked serp_page_complete", event)
		}
	}
	for _, path := range []string{got.Artifacts.CandidatesJSON, got.Artifacts.CandidatesTSV} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("workflow web-research serp artifact %q was not written: %v", path, err)
		}
	}
}

func TestWorkflowWebResearchSERPFallsBackAfterBlockedPrimary(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tmpDir := t.TempDir()
	queryFile := filepath.Join(tmpDir, "queries.txt")
	if err := os.WriteFile(queryFile, []byte("duck only block fixture\n"), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}
	outDir := filepath.Join(tmpDir, "research")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "web-research", "serp", "--query-file", queryFile, "--serp", "duckduckgo", "--result-pages", "1", "--parallel", "1", "--out-dir", outDir, "--wait", "250ms", "--min-visible-words", "1", "--min-markdown-words", "1", "--min-html-chars", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow web-research serp exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		SERPs []struct {
			Serp string `json:"serp"`
		} `json:"serps"`
		Candidates []struct {
			Serp string `json:"serp"`
			URL  string `json:"url"`
		} `json:"candidates"`
		Failures []struct {
			Serp     string `json:"serp"`
			ErrClass string `json:"err_class"`
		} `json:"failures"`
		Warnings []string `json:"warnings"`
		Workflow struct {
			Serp                   string `json:"serp"`
			FallbackSerp           string `json:"fallback_serp"`
			ResolvedFallbackSerp   string `json:"resolved_fallback_serp"`
			FallbackTriggered      bool   `json:"fallback_triggered"`
			PrimaryCandidateCount  int    `json:"primary_candidate_count"`
			PrimaryFailureCount    int    `json:"primary_failure_count"`
			PrimaryBlockedFailures int    `json:"primary_blocked_failures"`
			CandidateCount         int    `json:"candidate_count"`
			FailureCount           int    `json:"failure_count"`
			ScheduledResultPages   int    `json:"scheduled_result_pages"`
			CompletedResultPages   int    `json:"completed_result_pages"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow web-research serp output is invalid JSON: %v", err)
	}
	if got.OK {
		t.Fatalf("workflow ok = true, want false because primary duckduckgo block is retained")
	}
	if got.Workflow.Serp != "duckduckgo" || got.Workflow.FallbackSerp != "auto" || got.Workflow.ResolvedFallbackSerp != "google" || !got.Workflow.FallbackTriggered {
		t.Fatalf("workflow fallback metadata = %+v", got.Workflow)
	}
	if got.Workflow.PrimaryCandidateCount != 0 || got.Workflow.PrimaryFailureCount != 1 || got.Workflow.PrimaryBlockedFailures != 1 || got.Workflow.CandidateCount != 1 || got.Workflow.FailureCount != 1 {
		t.Fatalf("workflow counts = %+v", got.Workflow)
	}
	if got.Workflow.ScheduledResultPages != 2 || got.Workflow.CompletedResultPages != 2 {
		t.Fatalf("workflow scheduled/completed pages = %+v, want primary and fallback pages", got.Workflow)
	}
	if len(got.SERPs) != 2 || got.SERPs[0].Serp != "duckduckgo" || got.SERPs[1].Serp != "google" {
		t.Fatalf("serp reports = %+v, want duckduckgo primary plus google fallback", got.SERPs)
	}
	if len(got.Failures) != 1 || got.Failures[0].Serp != "duckduckgo" || got.Failures[0].ErrClass != "serp_blocked" {
		t.Fatalf("failures = %+v, want retained duckduckgo serp_blocked failure", got.Failures)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].Serp != "google" || got.Candidates[0].URL == "" {
		t.Fatalf("candidates = %+v, want google fallback candidate", got.Candidates)
	}
	if !testContainsSubstring(got.Warnings, "running fallback SERP google") {
		t.Fatalf("warnings = %+v, want fallback warning", got.Warnings)
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

func testContainsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
