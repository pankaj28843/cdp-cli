package cli_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		os.Exit(cli.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr, cli.BuildInfo{}))
	}
	os.Exit(m.Run())
}

func TestVersionJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"version", "--json"}, &out, &errOut, cli.BuildInfo{
		Version: "test",
		Commit:  "abc",
		Date:    "now",
	})
	if code != 0 {
		t.Fatalf("Execute exit code = %d, want 0; stderr=%s", code, errOut.String())
	}

	var got cli.BuildInfo
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("version output is invalid JSON: %v", err)
	}
	if got.Version != "test" {
		t.Fatalf("Version = %q, want %q", got.Version, "test")
	}
}

func TestVersionCompactJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"version", "--json", "--compact"}, &out, &errOut, cli.BuildInfo{Version: "test"})
	if code != cli.ExitOK {
		t.Fatalf("version exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	if strings.Contains(out.String(), "\n  ") {
		t.Fatalf("compact output contains indentation: %q", out.String())
	}
}

func TestDescribeJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"describe", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != 0 {
		t.Fatalf("Execute exit code = %d, want 0; stderr=%s", code, errOut.String())
	}

	if !strings.Contains(out.String(), `"commands"`) {
		t.Fatalf("describe output = %s, want command metadata", out.String())
	}
}

func TestDescribeJSONHasNoMCPCommand(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"describe", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Commands struct {
			Name     string         `json:"name"`
			Children []describeNode `json:"children"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("describe output is invalid JSON: %v", err)
	}
	if !got.OK {
		t.Fatalf("describe output indicates failure: %s", out.String())
	}

	commandPath, found := findCommandPath(got.Commands.Name, got.Commands.Children, "cdp")
	if found {
		t.Fatalf("describe command tree contains disallowed command %q", commandPath)
	}
}

func TestHelpDoesNotContainMCPHints(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"--help"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	if strings.Contains(strings.ToLower(out.String()), "mcp") {
		t.Fatalf("help output unexpectedly mentions MCP: %s", out.String())
	}
}

type describeNode struct {
	Name     string         `json:"name"`
	Children []describeNode `json:"children"`
}

func findCommandPath(name string, children []describeNode, prefix string) (string, bool) {
	if strings.EqualFold(name, "mcp") {
		return strings.TrimSpace(prefix + " " + name), true
	}

	for _, child := range children {
		childPath := prefix
		if child.Name != "" {
			childPath = strings.TrimSpace(prefix + " " + child.Name)
		}
		if foundPath, found := findCommandPath(child.Name, child.Children, childPath); found {
			return foundPath, true
		}
	}
	return "", false
}

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

func TestTargetsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
		{"targetId": "worker-1", "type": "service_worker", "title": "Worker", "url": "https://example.test/sw.js", "attached": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"targets", "--limit", "1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("targets exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK      bool `json:"ok"`
		Targets []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("targets output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Targets) != 1 || got.Targets[0].ID != "page-1" {
		t.Fatalf("targets output = %+v, want one limited target", got)
	}
}

func TestTargetsTypeFilterJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
		{"targetId": "worker-1", "type": "service_worker", "title": "Worker", "url": "https://example.test/sw.js", "attached": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"targets", "--type", "service_worker", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("targets exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Targets []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("targets output is invalid JSON: %v", err)
	}
	if len(got.Targets) != 1 || got.Targets[0].ID != "worker-1" || got.Targets[0].Type != "service_worker" {
		t.Fatalf("targets output = %+v, want service worker only", got)
	}
}

func TestPagesJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
		{"targetId": "worker-1", "type": "service_worker", "title": "Worker", "url": "https://example.test/sw.js", "attached": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Pages []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			URL      string `json:"url"`
			Attached bool   `json:"attached"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Pages) != 1 || got.Pages[0].ID != "page-1" || got.Pages[0].Type != "page" {
		t.Fatalf("pages output = %+v, want one page target", got)
	}
}

func TestPageSelectJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "First Page", "url": "https://example.test/first", "attached": false},
		{"targetId": "page-2", "type": "page", "title": "Second Page", "url": "https://example.test/second", "attached": false},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "select", "page-2", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page select exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK           bool `json:"ok"`
		SelectedPage struct {
			Connection string `json:"connection"`
			TargetID   string `json:"target_id"`
			URL        string `json:"url"`
		} `json:"selected_page"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page select output is invalid JSON: %v", err)
	}
	if !got.OK || got.SelectedPage.TargetID != "page-2" || got.SelectedPage.Connection != "default" || got.Target.ID != "page-2" {
		t.Fatalf("page select = %+v, want default page-2 selection", got)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"eval", "document.title", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("eval exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var evalOut struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &evalOut); err != nil {
		t.Fatalf("eval output is invalid JSON: %v", err)
	}
	if !evalOut.OK || evalOut.Target.ID != "page-2" {
		t.Fatalf("eval target = %+v, want selected page-2", evalOut.Target)
	}
}

func TestPagesUsesRunningDaemonByDefaultJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	stateDir := t.TempDir()
	var addOut, addErr bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "default", "--auto-connect", "--state-dir", stateDir, "--json"}, &addOut, &addErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stderr=%s", code, cli.ExitOK, addErr.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeWebSocketEndpoint(t, server.URL), "auto_connect", 30*time.Second)
	}()
	waitForDaemonRuntime(t, ctx, stateDir)
	defer func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	}()

	var out, errOut bytes.Buffer
	code = cli.Execute(context.Background(), []string{"pages", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK    bool `json:"ok"`
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Pages) != 1 || got.Pages[0].ID != "page-1" {
		t.Fatalf("pages output = %+v, want daemon-backed page target", got)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"doctor", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var doctor struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			State  string `json:"state"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &doctor); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	var sawDaemon, sawBrowser bool
	for _, check := range doctor.Checks {
		if check.Name == "daemon" {
			sawDaemon = true
			if check.Status != "pass" || check.State != "running" {
				t.Fatalf("daemon check = %+v, want running pass", check)
			}
		}
		if check.Name == "browser_debug_endpoint" {
			sawBrowser = true
			if check.Status != "pass" {
				t.Fatalf("browser check = %+v, want pass when daemon is running", check)
			}
		}
	}
	if !sawDaemon || !sawBrowser {
		t.Fatalf("doctor checks = %+v, want daemon and browser checks", doctor.Checks)
	}
}

func TestPagesURLFilterJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
		{"targetId": "page-2", "type": "page", "title": "Docs", "url": "https://docs.example.test/", "attached": false},
		{"targetId": "page-3", "type": "page", "title": "Docs Admin", "url": "https://docs.example.test/admin", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"contains", []string{"pages", "--url-contains", "docs", "--json"}, []string{"page-2", "page-3"}},
		{"include", []string{"pages", "--include-url", "docs", "--json"}, []string{"page-2", "page-3"}},
		{"exclude", []string{"pages", "--include-url", "docs", "--exclude-url", "admin", "--json"}, []string{"page-2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
			}

			var got struct {
				Pages []struct {
					ID  string `json:"id"`
					URL string `json:"url"`
				} `json:"pages"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("pages output is invalid JSON: %v", err)
			}
			var ids []string
			for _, page := range got.Pages {
				ids = append(ids, page.ID)
			}
			if strings.Join(ids, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("pages output ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestPageReloadJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "reload", "--target", "page", "--ignore-cache", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page reload exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page reload output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "reloaded" || got.Target.ID != "page-1" {
		t.Fatalf("page reload = %+v, want reloaded page-1", got)
	}
}

func TestPageHistoryNavigationJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/current", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tests := []struct {
		name   string
		args   []string
		action string
		entry  int
	}{
		{"back", []string{"page", "back", "--target", "page", "--json"}, "back", 1},
		{"forward", []string{"page", "forward", "--target", "page", "--json"}, "forward", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s exit code = %d, want %d; stderr=%s", tt.name, code, cli.ExitOK, errOut.String())
			}

			var got struct {
				OK      bool   `json:"ok"`
				Action  string `json:"action"`
				History struct {
					EntryID int `json:"entry_id"`
				} `json:"history"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("%s output is invalid JSON: %v", tt.name, err)
			}
			if !got.OK || got.Action != tt.action || got.History.EntryID != tt.entry {
				t.Fatalf("%s = %+v, want action %s entry %d", tt.name, got, tt.action, tt.entry)
			}
		})
	}
}

func TestPageCloseAndActivateJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tests := []struct {
		name   string
		args   []string
		action string
	}{
		{"activate", []string{"page", "activate", "--target", "page", "--json"}, "activated"},
		{"close", []string{"page", "close", "--target", "page", "--json"}, "closed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tt.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s exit code = %d, want %d; stderr=%s", tt.name, code, cli.ExitOK, errOut.String())
			}

			var got struct {
				OK     bool   `json:"ok"`
				Action string `json:"action"`
				Target struct {
					ID string `json:"id"`
				} `json:"target"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("%s output is invalid JSON: %v", tt.name, err)
			}
			if !got.OK || got.Action != tt.action || got.Target.ID != "page-1" {
				t.Fatalf("%s = %+v, want action %s on page-1", tt.name, got, tt.action)
			}
		})
	}
}

func TestTextCommandJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"text", "main", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("text exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Text struct {
			Selector string `json:"selector"`
			Text     string `json:"text"`
			Items    []struct {
				Text string `json:"text"`
			} `json:"items"`
		} `json:"text"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("text output is invalid JSON: %v", err)
	}
	if !got.OK || got.Text.Selector != "main" || got.Text.Text != "Synthetic main text" || len(got.Text.Items) != 1 {
		t.Fatalf("text output = %+v, want compact text result", got)
	}
}

func TestClickJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "main", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Click struct {
			Selector string `json:"selector"`
			Count    int    `json:"count"`
			Clicked  bool   `json:"clicked"`
		} `json:"click"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "clicked" || got.Target.ID != "page-1" || got.Click.Selector != "main" || got.Click.Count != 1 || !got.Click.Clicked {
		t.Fatalf("click output = %+v, want clicked main", got)
	}
}

func TestHTMLCommandJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"html", "main", "--max-chars", "80", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("html exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		HTML struct {
			Selector string `json:"selector"`
			Items    []struct {
				HTML string `json:"html"`
			} `json:"items"`
		} `json:"html"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("html output is invalid JSON: %v", err)
	}
	if !got.OK || got.HTML.Selector != "main" || len(got.HTML.Items) != 1 || !strings.Contains(got.HTML.Items[0].HTML, "Synthetic") {
		t.Fatalf("html output = %+v, want compact html result", got)
	}
}

func TestDOMQueryJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"dom", "query", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("dom query exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Query struct {
			Selector string `json:"selector"`
			Nodes    []struct {
				UID  string `json:"uid"`
				Role string `json:"role"`
				Text string `json:"text"`
			} `json:"nodes"`
		} `json:"query"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("dom query output is invalid JSON: %v", err)
	}
	if !got.OK || got.Query.Selector != "button" || len(got.Query.Nodes) != 1 || got.Query.Nodes[0].Role != "button" {
		t.Fatalf("dom query output = %+v, want button node", got)
	}
}

func TestCSSInspectJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"css", "inspect", "main", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("css inspect exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK      bool `json:"ok"`
		Inspect struct {
			Selector string            `json:"selector"`
			Styles   map[string]string `json:"styles"`
		} `json:"inspect"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("css inspect output is invalid JSON: %v", err)
	}
	if !got.OK || got.Inspect.Selector != "main" || got.Inspect.Styles["display"] != "block" {
		t.Fatalf("css inspect output = %+v, want display block", got)
	}
}

func TestLayoutOverflowJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"layout", "overflow", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("layout overflow exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Overflow struct {
			Count int `json:"count"`
			Items []struct {
				UID string `json:"uid"`
			} `json:"items"`
		} `json:"overflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("layout overflow output is invalid JSON: %v", err)
	}
	if !got.OK || got.Overflow.Count != 1 || got.Overflow.Items[0].UID == "" {
		t.Fatalf("layout overflow output = %+v, want one overflow item", got)
	}
}

func TestWaitTextJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "text", "Ready", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait text exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind    string `json:"kind"`
			Needle  string `json:"needle"`
			Matched bool   `json:"matched"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait text output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "text" || got.Wait.Needle != "Ready" || !got.Wait.Matched {
		t.Fatalf("wait text output = %+v, want matched text", got)
	}
}

func TestWaitSelectorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "selector", "main", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait selector exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind     string `json:"kind"`
			Selector string `json:"selector"`
			Matched  bool   `json:"matched"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait selector output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "selector" || got.Wait.Selector != "main" || !got.Wait.Matched {
		t.Fatalf("wait selector output = %+v, want matched selector", got)
	}
}

func TestNetworkJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"network", "--wait", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Requests []struct {
			ID     string `json:"id"`
			URL    string `json:"url"`
			Status int    `json:"status"`
			Failed bool   `json:"failed"`
		} `json:"requests"`
		Network struct {
			Count int `json:"count"`
		} `json:"network"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("network output is invalid JSON: %v", err)
	}
	if !got.OK || got.Network.Count != 2 || len(got.Requests) != 2 || got.Requests[0].Status != 200 {
		t.Fatalf("network output = %+v, want two requests", got)
	}
}

func TestNetworkFailedFilterJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"network", "--failed", "--wait", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network --failed exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		Requests []struct {
			ID        string `json:"id"`
			Failed    bool   `json:"failed"`
			ErrorText string `json:"error_text"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("network --failed output is invalid JSON: %v", err)
	}
	if len(got.Requests) != 1 || got.Requests[0].ID != "request-failed" || !got.Requests[0].Failed {
		t.Fatalf("network --failed output = %+v, want failed request only", got)
	}
}

func TestNetworkCaptureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "network.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"network", "capture",
		"--wait", "250ms",
		"--out", outPath,
		"--redact", "safe",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Requests []struct {
			ID              string         `json:"id"`
			URL             string         `json:"url"`
			RequestHeaders  map[string]any `json:"request_headers"`
			ResponseHeaders map[string]any `json:"response_headers"`
			RequestPostData struct {
				Text string `json:"text"`
			} `json:"request_post_data"`
			Body struct {
				Text string `json:"text"`
			} `json:"body"`
			Initiator json.RawMessage `json:"initiator"`
			Timing    json.RawMessage `json:"timing"`
		} `json:"requests"`
		Capture struct {
			Count  int    `json:"count"`
			Redact string `json:"redact"`
		} `json:"capture"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("network capture output is invalid JSON: %v", err)
	}
	if !got.OK || got.Capture.Count != 2 || len(got.Requests) != 2 || got.Capture.Redact != "safe" || got.Artifact.Path != outPath {
		t.Fatalf("network capture = %+v, want two safe-redacted requests and artifact", got)
	}
	if got.Requests[0].RequestHeaders["Authorization"] != "<redacted>" || got.Requests[0].ResponseHeaders["Set-Cookie"] != "<redacted>" {
		t.Fatalf("network capture headers = request=%+v response=%+v, want sensitive headers redacted", got.Requests[0].RequestHeaders, got.Requests[0].ResponseHeaders)
	}
	if !strings.Contains(got.Requests[0].Body.Text, `"ok":true`) || strings.Contains(got.Requests[0].Body.Text, "secret") || len(got.Requests[0].Initiator) == 0 || len(got.Requests[0].Timing) == 0 {
		t.Fatalf("network capture request-ok = %+v, want body, initiator, and timing", got.Requests[0])
	}
	if !strings.Contains(got.Requests[1].RequestPostData.Text, "redacted") || strings.Contains(got.Requests[1].RequestPostData.Text, "secret") {
		t.Fatalf("network capture post data = %q, want redacted csrf", got.Requests[1].RequestPostData.Text)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("network capture artifact was not written: %v", err)
	}
}

func TestNetworkCaptureDefaultKeepsLocalCredentials(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"network", "capture",
		"--wait", "250ms",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		Requests []struct {
			URL             string         `json:"url"`
			RequestHeaders  map[string]any `json:"request_headers"`
			ResponseHeaders map[string]any `json:"response_headers"`
			Body            struct {
				Text string `json:"text"`
			} `json:"body"`
		} `json:"requests"`
		Capture struct {
			Redact string `json:"redact"`
		} `json:"capture"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("network capture output is invalid JSON: %v", err)
	}
	if len(got.Requests) == 0 || got.Capture.Redact != "none" {
		t.Fatalf("network capture = %+v, want default unredacted local capture", got)
	}
	if got.Requests[0].URL != "https://example.test/app?token=abc" || got.Requests[0].RequestHeaders["Authorization"] != "Bearer secret" || got.Requests[0].ResponseHeaders["Set-Cookie"] != "session=secret" {
		t.Fatalf("network capture local credentials = %+v, want unredacted synthetic credentials", got.Requests[0])
	}
	if !strings.Contains(got.Requests[0].Body.Text, `"token":"secret"`) {
		t.Fatalf("network capture response body = %q, want unredacted synthetic token by default", got.Requests[0].Body.Text)
	}
}

func TestStorageListAndSnapshotJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"storage", "list", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("storage list exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK      bool `json:"ok"`
		Storage struct {
			LocalStorage struct {
				Count   int `json:"count"`
				Entries []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"entries"`
			} `json:"local_storage"`
			SessionStorage struct {
				Keys []string `json:"keys"`
			} `json:"session_storage"`
			Cookies []map[string]any `json:"cookies"`
			Quota   map[string]any   `json:"quota"`
		} `json:"storage"`
		CollectorErrors []map[string]string `json:"collector_errors"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("storage list output is invalid JSON: %v", err)
	}
	if !got.OK || got.Storage.LocalStorage.Count != 2 || got.Storage.LocalStorage.Entries[0].Key != "authToken" || got.Storage.LocalStorage.Entries[0].Value != "secret" || len(got.Storage.Cookies) != 1 || len(got.CollectorErrors) != 0 {
		t.Fatalf("storage list = %+v, want unredacted local forensic storage", got)
	}
	if got.Storage.Quota["usage"] == nil || !containsString(got.Storage.SessionStorage.Keys, "nonce") {
		t.Fatalf("storage list quota/session = %+v / %+v, want quota and session key", got.Storage.Quota, got.Storage.SessionStorage.Keys)
	}

	out.Reset()
	errOut.Reset()
	outPath := filepath.Join(t.TempDir(), "storage.local.json")
	code = cli.Execute(context.Background(), []string{"storage", "snapshot", "--redact", "safe", "--out", outPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("storage snapshot exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var snap struct {
		Snapshot struct {
			LocalStorage struct {
				Entries []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"entries"`
			} `json:"local_storage"`
			SessionStorage struct {
				Entries []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"entries"`
			} `json:"session_storage"`
			Cookies []map[string]any `json:"cookies"`
		} `json:"snapshot"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &snap); err != nil {
		t.Fatalf("storage snapshot output is invalid JSON: %v", err)
	}
	if snap.Artifact.Path != outPath {
		t.Fatalf("storage snapshot artifact = %+v, want %q", snap.Artifact, outPath)
	}
	for _, entry := range snap.Snapshot.LocalStorage.Entries {
		if entry.Value != "<redacted>" {
			t.Fatalf("localStorage entry %q value = %q, want redacted", entry.Key, entry.Value)
		}
	}
	for _, entry := range snap.Snapshot.SessionStorage.Entries {
		if entry.Value != "<redacted>" {
			t.Fatalf("sessionStorage entry %q value = %q, want redacted", entry.Key, entry.Value)
		}
	}
	if snap.Snapshot.Cookies[0]["value"] != "<redacted>" {
		t.Fatalf("storage snapshot cookies = %+v, want redacted values", snap.Snapshot.Cookies)
	}
}

func TestStorageWebStorageMutationJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "get", args: []string{"storage", "get", "localStorage", "feature", "--json"}},
		{name: "set", args: []string{"storage", "set", "localStorage", "feature", "disabled", "--json"}},
		{name: "delete", args: []string{"storage", "delete", "sessionStorage", "nonce", "--json"}},
		{name: "clear", args: []string{"storage", "clear", "sessionStorage", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tc.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s exit code = %d, want %d; stdout=%s stderr=%s", tc.name, code, cli.ExitOK, out.String(), errOut.String())
			}
			var got struct {
				OK      bool `json:"ok"`
				Storage struct {
					Backend string `json:"backend"`
				} `json:"storage"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("%s output is invalid JSON: %v", tc.name, err)
			}
			if !got.OK || got.Storage.Backend == "" {
				t.Fatalf("%s output = %+v, want storage operation result", tc.name, got)
			}
		})
	}
}

func TestStorageCookiesAndDiffJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	for _, args := range [][]string{
		{"storage", "cookies", "list", "--json"},
		{"storage", "cookies", "set", "--name", "feature", "--value", "enabled", "--json"},
		{"storage", "cookies", "delete", "--name", "feature", "--json"},
	} {
		var out, errOut bytes.Buffer
		code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("%v exit code = %d, want %d; stdout=%s stderr=%s", args, code, cli.ExitOK, out.String(), errOut.String())
		}
		var got struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("%v output is invalid JSON: %v", args, err)
		}
		if !got.OK {
			t.Fatalf("%v output = %+v, want ok", args, got)
		}
	}

	dir := t.TempDir()
	left := filepath.Join(dir, "left.json")
	right := filepath.Join(dir, "right.json")
	if err := os.WriteFile(left, []byte(`{"snapshot":{"local_storage":{"entries":[{"key":"feature","value":"enabled"}]},"session_storage":{"entries":[]},"cookies":[]}}`), 0o600); err != nil {
		t.Fatalf("write left snapshot: %v", err)
	}
	if err := os.WriteFile(right, []byte(`{"snapshot":{"local_storage":{"entries":[{"key":"feature","value":"disabled"},{"key":"new","value":"yes"}]},"session_storage":{"entries":[]},"cookies":[]}}`), 0o600); err != nil {
		t.Fatalf("write right snapshot: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"storage", "diff", "--left", left, "--right", right, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("storage diff exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var diff struct {
		HasDiff bool `json:"has_diff"`
		Diff    struct {
			Summary map[string]int `json:"summary"`
		} `json:"diff"`
	}
	if err := json.Unmarshal(out.Bytes(), &diff); err != nil {
		t.Fatalf("storage diff output is invalid JSON: %v", err)
	}
	if !diff.HasDiff || diff.Diff.Summary["added"] != 1 || diff.Diff.Summary["changed"] != 1 {
		t.Fatalf("storage diff = %+v, want one added and one changed", diff)
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

func TestProtocolMetadataJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"protocol", "metadata", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol metadata exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Protocol struct {
			DomainCount int `json:"domain_count"`
			Domains     []struct {
				Name         string `json:"name"`
				CommandCount int    `json:"command_count"`
			} `json:"domains"`
		} `json:"protocol"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol metadata output is invalid JSON: %v", err)
	}
	if !got.OK || got.Protocol.DomainCount != 2 || got.Protocol.Domains[0].Name != "Page" || got.Protocol.Domains[0].CommandCount != 2 {
		t.Fatalf("protocol metadata = %+v, want compact domain summary", got)
	}
}

func TestProtocolDomainsJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"protocol", "domains", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol domains exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK          bool `json:"ok"`
		DomainCount int  `json:"domain_count"`
		Domains     []struct {
			Name       string `json:"name"`
			EventCount int    `json:"event_count"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol domains output is invalid JSON: %v", err)
	}
	if !got.OK || got.DomainCount != 2 || got.Domains[1].Name != "Runtime" || got.Domains[1].EventCount != 1 {
		t.Fatalf("protocol domains = %+v, want compact domains", got)
	}
}

func TestProtocolDomainsExperimentalFilterJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"protocol", "domains", "--experimental", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol domains exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Domains []struct {
			Name         string `json:"name"`
			Experimental bool   `json:"experimental"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol domains output is invalid JSON: %v", err)
	}
	if len(got.Domains) != 1 || got.Domains[0].Name != "Runtime" || !got.Domains[0].Experimental {
		t.Fatalf("protocol domains = %+v, want experimental Runtime only", got)
	}
}

func TestProtocolSearchJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"protocol", "search", "capture", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol search exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Query   string `json:"query"`
		Matches []struct {
			Kind string `json:"kind"`
			Path string `json:"path"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol search output is invalid JSON: %v", err)
	}
	if !got.OK || got.Query != "capture" || len(got.Matches) != 1 || got.Matches[0].Path != "Page.captureScreenshot" {
		t.Fatalf("protocol search = %+v, want captureScreenshot match", got)
	}
}

func TestProtocolSearchKindFilterJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"protocol", "search", "console", "--kind", "event", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol search exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Matches []struct {
			Kind string `json:"kind"`
			Path string `json:"path"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol search output is invalid JSON: %v", err)
	}
	if len(got.Matches) != 1 || got.Matches[0].Kind != "event" || got.Matches[0].Path != "Runtime.consoleAPICalled" {
		t.Fatalf("protocol search = %+v, want console event", got)
	}
}

func TestProtocolDescribeJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"protocol", "describe", "Page.captureScreenshot", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol describe exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Entity struct {
			Kind   string `json:"kind"`
			Path   string `json:"path"`
			Schema struct {
				Name string `json:"name"`
			} `json:"schema"`
		} `json:"entity"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol describe output is invalid JSON: %v", err)
	}
	if !got.OK || got.Entity.Kind != "command" || got.Entity.Path != "Page.captureScreenshot" || got.Entity.Schema.Name != "captureScreenshot" {
		t.Fatalf("protocol describe = %+v, want captureScreenshot schema", got)
	}
}

func TestProtocolExamplesJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"protocol", "examples", "Page.captureScreenshot", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol examples exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Examples []struct {
			Command string `json:"command"`
			Scope   string `json:"scope"`
		} `json:"examples"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol examples output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Examples) == 0 || got.Examples[0].Scope != "target" || !strings.Contains(got.Examples[0].Command, "Page.captureScreenshot") {
		t.Fatalf("protocol examples = %+v, want target-scoped example", got)
	}
}

func TestProtocolExecJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"protocol", "exec", "Browser.getVersion", "--params", "{}", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol exec exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Scope  string `json:"scope"`
		Method string `json:"method"`
		Result struct {
			Product string `json:"product"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol exec output is invalid JSON: %v", err)
	}
	if !got.OK || got.Scope != "browser" || got.Method != "Browser.getVersion" || got.Result.Product != "Chrome/Test" {
		t.Fatalf("protocol exec = %+v, want Browser.getVersion result", got)
	}
}

func TestProtocolExecTargetScopedJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"protocol", "exec", "Runtime.evaluate",
		"--target", "page",
		"--params", `{"expression":"document.title","returnByValue":true}`,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol exec target exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Scope  string `json:"scope"`
		Method string `json:"method"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		SessionID string `json:"session_id"`
		Result    struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol exec target output is invalid JSON: %v", err)
	}
	if !got.OK || got.Scope != "target" || got.Method != "Runtime.evaluate" || got.Target.ID != "page-1" || got.SessionID != "session-1" || got.Result.Result.Value != "Example App" {
		t.Fatalf("protocol exec target = %+v, want target-scoped Runtime.evaluate", got)
	}
}

func TestProtocolExecSaveArtifactJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "protocol-shot.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"protocol", "exec", "Page.captureScreenshot",
		"--target", "page",
		"--params", `{"format":"png"}`,
		"--save", outPath,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol exec save exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Artifact struct {
			Path  string `json:"path"`
			Bytes int    `json:"bytes"`
			Field string `json:"field"`
		} `json:"artifact"`
		Result struct {
			Data struct {
				Omitted bool `json:"omitted"`
			} `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol exec save output is invalid JSON: %v", err)
	}
	if !got.OK || got.Artifact.Path != outPath || got.Artifact.Bytes != len("synthetic screenshot") || got.Artifact.Field != "data" || !got.Result.Data.Omitted {
		t.Fatalf("protocol exec save = %+v, want saved redacted artifact", got)
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(b) != "synthetic screenshot" {
		t.Fatalf("saved protocol artifact = %q, want synthetic screenshot", string(b))
	}
}

func TestOpenJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"open", "https://example.test/feed", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("open exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Page   struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"page"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("open output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "created" || got.Page.ID != "created-page" || got.Page.URL != "https://example.test/feed" {
		t.Fatalf("open output = %+v, want created page", got)
	}
}

func TestEvalJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"eval", "document.title", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("eval exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Result struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("eval output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "page-1" || got.Result.Type != "string" || got.Result.Value != "Example App" {
		t.Fatalf("eval output = %+v, want document title result", got)
	}
}

func TestEvalExactTargetIDSkipsTargetListing(t *testing.T) {
	var getTargetsCalled atomic.Bool
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/144.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": wsURL,
		})
	})
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			var req struct {
				ID        int64           `json:"id"`
				SessionID string          `json:"sessionId"`
				Method    string          `json:"method"`
				Params    json.RawMessage `json:"params"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			resp := map[string]any{"id": req.ID}
			if req.SessionID != "" {
				resp["sessionId"] = req.SessionID
			}
			switch req.Method {
			case "Target.getTargets":
				getTargetsCalled.Store(true)
				resp["error"] = map[string]any{"code": -32000, "message": "target list should not be requested"}
			case "Target.getTargetInfo":
				resp["result"] = map[string]any{"targetInfo": map[string]any{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app"}}
			case "Target.attachToTarget":
				resp["result"] = map[string]any{"sessionId": "session-1"}
			case "Target.detachFromTarget":
				resp["result"] = map[string]any{}
			case "Runtime.evaluate":
				resp["result"] = map[string]any{"result": map[string]any{"type": "string", "value": "Example App"}}
			default:
				resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
			}
			if err := wsjson.Write(r.Context(), conn, resp); err != nil {
				return
			}
		}
	})
	server = httptest.NewServer(mux)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"eval", "document.title", "--target", "page-1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("eval exact target exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	if getTargetsCalled.Load() {
		t.Fatalf("eval exact target called Target.getTargets; want Target.getTargetInfo direct attach")
	}
}

func TestConsoleJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"console", "--errors", "--wait", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("console exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Messages []struct {
			ID           int             `json:"id"`
			Source       string          `json:"source"`
			Type         string          `json:"type"`
			Level        string          `json:"level"`
			Text         string          `json:"text"`
			URL          string          `json:"url"`
			LineNumber   int             `json:"line_number"`
			ColumnNumber int             `json:"column_number"`
			ScriptID     string          `json:"script_id"`
			Exception    json.RawMessage `json:"exception"`
			StackTrace   json.RawMessage `json:"stack_trace"`
		} `json:"messages"`
		Console struct {
			Count      int  `json:"count"`
			ErrorsOnly bool `json:"errors_only"`
		} `json:"console"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("console output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "page-1" || got.Console.Count != 3 || !got.Console.ErrorsOnly {
		t.Fatalf("console output = %+v, want three error messages", got)
	}
	if got.Messages[0].ID != 0 || got.Messages[0].Source != "runtime" || got.Messages[0].Type != "error" || got.Messages[0].Text != "Synthetic console error" {
		t.Fatalf("first console message = %+v, want runtime error", got.Messages[0])
	}
	if got.Messages[1].Source != "runtime" || got.Messages[1].Type != "exception" || got.Messages[1].Text != "Uncaught (in promise): TypeError: failed to fetch dashboard" {
		t.Fatalf("second console message = %+v, want rich runtime exception", got.Messages[1])
	}
	if got.Messages[1].URL != "https://example.test/assets/app.js" || got.Messages[1].LineNumber != 41 || got.Messages[1].ColumnNumber != 9 || got.Messages[1].ScriptID != "script-1" {
		t.Fatalf("second console location = %+v, want script location", got.Messages[1])
	}
	if len(got.Messages[1].Exception) == 0 || !strings.Contains(string(got.Messages[1].Exception), "TypeError") {
		t.Fatalf("second console exception = %s, want serialized exception object", got.Messages[1].Exception)
	}
	if len(got.Messages[1].StackTrace) == 0 || !strings.Contains(string(got.Messages[1].StackTrace), "loadDashboard") {
		t.Fatalf("second console stack_trace = %s, want stack frames", got.Messages[1].StackTrace)
	}
	if got.Messages[2].Source != "network" || got.Messages[2].Level != "error" || got.Messages[2].Text != "Synthetic network failure" {
		t.Fatalf("third console message = %+v, want network log error", got.Messages[2])
	}
}

func TestSnapshotJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/feed", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"snapshot", "--selector", "article", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("snapshot exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Snapshot struct {
			Selector string `json:"selector"`
			Count    int    `json:"count"`
		} `json:"snapshot"`
		Items []struct {
			Tag  string `json:"tag"`
			Text string `json:"text"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("snapshot output is invalid JSON: %v", err)
	}
	if !got.OK || got.Snapshot.Selector != "article" || got.Snapshot.Count != 1 || len(got.Items) != 1 || got.Items[0].Text != "First visible synthetic post" {
		t.Fatalf("snapshot output = %+v, want one article item", got)
	}
}

func TestScreenshotJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/feed", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "shot.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"screenshot", "--out", outPath, "--full-page", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("screenshot exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Screenshot struct {
			Path     string `json:"path"`
			Bytes    int    `json:"bytes"`
			Format   string `json:"format"`
			FullPage bool   `json:"full_page"`
		} `json:"screenshot"`
		Artifacts []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("screenshot output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "page-1" || got.Screenshot.Path != outPath || got.Screenshot.Bytes != len("synthetic screenshot") || got.Screenshot.Format != "png" || !got.Screenshot.FullPage {
		t.Fatalf("screenshot output = %+v, want artifact metadata", got)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Type != "screenshot" || got.Artifacts[0].Path != outPath {
		t.Fatalf("screenshot artifacts = %+v, want screenshot artifact", got.Artifacts)
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(b) != "synthetic screenshot" {
		t.Fatalf("screenshot file = %q, want synthetic screenshot", string(b))
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

func TestDaemonStatusJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"daemon", "status", "--state-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Daemon struct {
			State          string `json:"state"`
			ConnectionMode string `json:"connection_mode"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon status output is invalid JSON: %v", err)
	}
	if !got.OK || got.Daemon.State != "not_running" || got.Daemon.ConnectionMode != "browser_url" {
		t.Fatalf("daemon status = %+v, want not_running browser_url", got)
	}
}

func TestDaemonStatusReportsRuntimeJSON(t *testing.T) {
	stateDir := t.TempDir()
	socketPath := filepath.Join(stateDir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer listener.Close()

	if err := daemon.SaveRuntime(context.Background(), stateDir, daemon.Runtime{
		PID:               os.Getpid(),
		StartedAt:         time.Now().UTC().Format(time.RFC3339),
		ConnectionMode:    "auto_connect",
		ReconnectInterval: "30s",
		SocketPath:        socketPath,
	}); err != nil {
		t.Fatalf("SaveRuntime returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "status", "--auto-connect", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon status exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Daemon struct {
			State          string `json:"state"`
			ProcessRunning bool   `json:"process_running"`
			Runtime        struct {
				PID int `json:"pid"`
			} `json:"runtime"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon status output is invalid JSON: %v", err)
	}
	if got.Daemon.State != "running" || !got.Daemon.ProcessRunning || got.Daemon.Runtime.PID != os.Getpid() {
		t.Fatalf("daemon status = %+v, want running current pid", got.Daemon)
	}
}

func TestDaemonStopNotRunningJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "stop", "--state-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon stop exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK      bool `json:"ok"`
		Stopped bool `json:"stopped"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon stop output is invalid JSON: %v", err)
	}
	if !got.OK || got.Stopped {
		t.Fatalf("daemon stop = %+v, want ok not stopped", got)
	}
}

func TestDaemonStartBrowserURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()

	stateDir := t.TempDir()
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "start", "--browser-url", server.URL, "--connection-name", "local", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon start exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Daemon struct {
			State          string `json:"state"`
			ConnectionMode string `json:"connection_mode"`
		} `json:"daemon"`
		Start struct {
			ConnectionSaved bool   `json:"connection_saved"`
			ConnectionName  string `json:"connection_name"`
			Keepalive       bool   `json:"keepalive_started"`
		} `json:"start"`
		Connection struct {
			Name       string `json:"name"`
			Mode       string `json:"mode"`
			BrowserURL string `json:"browser_url"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon start output is invalid JSON: %v", err)
	}
	if !got.OK || got.Daemon.State != "running" || got.Daemon.ConnectionMode != "browser_url" || !got.Start.ConnectionSaved || got.Start.ConnectionName != "local" || !got.Start.Keepalive {
		t.Fatalf("daemon start = %+v, want running saved browser-url keepalive connection", got)
	}
	if got.Connection.Name != "local" || got.Connection.Mode != "browser_url" || got.Connection.BrowserURL != server.URL {
		t.Fatalf("daemon start connection = %+v, want saved local browser-url", got.Connection)
	}
}

func TestDaemonKeepaliveStartsBrowserURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()

	stateDir := t.TempDir()
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "keepalive", "--browser-url", server.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		State  string `json:"state"`
		Action string `json:"action"`
		Daemon struct {
			State string `json:"state"`
		} `json:"daemon"`
		Start struct {
			Keepalive bool `json:"keepalive_started"`
		} `json:"start"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v", err)
	}
	if !got.OK || got.State != "started" || got.Action != "started" || got.Daemon.State != "running" || !got.Start.Keepalive {
		t.Fatalf("daemon keepalive = %+v, want started running daemon", got)
	}
}

func TestDaemonKeepaliveHealthyJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "keepalive", "--browser-url", server.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		State  string `json:"state"`
		Action string `json:"action"`
		Daemon struct {
			State string `json:"state"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v", err)
	}
	if !got.OK || got.State != "healthy" || got.Action != "none" || got.Daemon.State != "running" {
		t.Fatalf("daemon keepalive = %+v, want healthy running daemon", got)
	}
}

func TestDaemonKeepaliveLockedJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := t.TempDir()
	lockDir := filepath.Join(stateDir, "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	lockPath := filepath.Join(lockDir, "daemon-keepalive-browser_url-browser-url.lock")
	lockBody := []byte(`{"name":"daemon-keepalive-browser_url-browser-url","pid":1234,"started_at":"2099-01-01T00:00:00Z","phase":"active_probe"}` + "\n")
	if err := os.WriteFile(lockPath, lockBody, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "keepalive", "--browser-url", server.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		State  string `json:"state"`
		Action string `json:"action"`
		Locked bool   `json:"locked"`
		Lock   struct {
			Phase string `json:"phase"`
		} `json:"lock"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v", err)
	}
	if !got.OK || got.State != "locked" || got.Action != "skipped" || !got.Locked || got.Lock.Phase != "active_probe" {
		t.Fatalf("daemon keepalive = %+v, want locked skip", got)
	}
}

func TestDaemonRestartBrowserURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()

	stateDir := t.TempDir()
	t.Cleanup(func() {
		var stopOut, stopErr bytes.Buffer
		_ = cli.Execute(context.Background(), []string{"daemon", "stop", "--state-dir", stateDir, "--json"}, &stopOut, &stopErr, cli.BuildInfo{})
	})

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "start", "--browser-url", server.URL, "--connection-name", "local", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon start exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"daemon", "restart", "--browser-url", server.URL, "--connection-name", "local", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon restart exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Daemon struct {
			State          string `json:"state"`
			ConnectionMode string `json:"connection_mode"`
		} `json:"daemon"`
		Start struct {
			Keepalive bool `json:"keepalive_started"`
		} `json:"start"`
		Restart struct {
			Stopped bool `json:"stopped"`
		} `json:"restart"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon restart output is invalid JSON: %v", err)
	}
	if !got.OK || got.Daemon.State != "running" || got.Daemon.ConnectionMode != "browser_url" || !got.Start.Keepalive || !got.Restart.Stopped {
		t.Fatalf("daemon restart = %+v, want stopped previous daemon and running browser-url daemon", got)
	}
}

func TestDaemonStartAutoConnectPermissionPendingJSON(t *testing.T) {
	stateDir := t.TempDir()
	userDataDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "start", "--autoConnect", "--user-data-dir", userDataDir, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitPermission {
		t.Fatalf("daemon start exit code = %d, want %d; stderr=%s", code, cli.ExitPermission, errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		ErrClass            string   `json:"err_class"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon start error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "permission_pending" || got.ErrClass != "permission" || !containsString(got.RemediationCommands, "open chrome://inspect/#remote-debugging") {
		t.Fatalf("daemon start error = %+v, want permission_pending with Chrome remediation", got)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "current", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection current exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var current struct {
		Connection struct {
			Name string `json:"name"`
			Mode string `json:"mode"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(out.Bytes(), &current); err != nil {
		t.Fatalf("connection current output is invalid JSON: %v", err)
	}
	if current.Connection.Name != "default" || current.Connection.Mode != "auto_connect" {
		t.Fatalf("connection current = %+v, want remembered auto_connect default", current.Connection)
	}
}

func TestDaemonRestartAutoConnectPermissionPendingJSON(t *testing.T) {
	stateDir := t.TempDir()
	userDataDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"daemon", "restart", "--debug", "--autoConnect", "--active-browser-probe", "--user-data-dir", userDataDir, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitPermission {
		t.Fatalf("daemon restart exit code = %d, want %d; stderr=%s", code, cli.ExitPermission, errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		ErrClass            string   `json:"err_class"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon restart error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "permission_pending" || got.ErrClass != "permission" || !containsString(got.RemediationCommands, "open chrome://inspect/#remote-debugging") {
		t.Fatalf("daemon restart error = %+v, want permission_pending with Chrome remediation", got)
	}
}

func TestConnectionMemoryJSON(t *testing.T) {
	stateDir := t.TempDir()
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"connection", "add", "default", "--auto-connect", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(stateDir, "connections.json")); err != nil {
		t.Fatalf("connections.json was not written: %v", err)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "current", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection current exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK         bool `json:"ok"`
		Connection struct {
			Name        string `json:"name"`
			Mode        string `json:"mode"`
			AutoConnect bool   `json:"auto_connect"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("connection current output is invalid JSON: %v", err)
	}
	if !got.OK || got.Connection.Name != "default" || got.Connection.Mode != "auto_connect" || !got.Connection.AutoConnect {
		t.Fatalf("connection current = %+v, want default auto_connect", got)
	}
}

func TestConnectionRemoveJSON(t *testing.T) {
	stateDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "default", "--auto-connect", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add default exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "add", "extra", "--auto-connect", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add extra exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "remove", "extra", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection remove exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK          bool   `json:"ok"`
		Removed     string `json:"removed"`
		Connections []struct {
			Name string `json:"name"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("connection remove output is invalid JSON: %v", err)
	}
	if !got.OK || got.Removed != "extra" || len(got.Connections) != 1 || got.Connections[0].Name != "default" {
		t.Fatalf("connection remove = %+v, want only default remaining", got)
	}
}

func TestConnectionPruneJSON(t *testing.T) {
	stateDir := t.TempDir()
	missingProject := filepath.Join(t.TempDir(), "missing")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "stale", "--browser-url", "http://example.invalid", "--project", missingProject, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add stale exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "prune", "--missing-projects", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection prune exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK      bool `json:"ok"`
		Removed []struct {
			Name string `json:"name"`
		} `json:"removed"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("connection prune output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Removed) != 1 || got.Removed[0].Name != "stale" {
		t.Fatalf("connection prune = %+v, want stale removed", got)
	}
}

func TestConnectionListProjectFilterJSON(t *testing.T) {
	stateDir := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "global", "--auto-connect", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add global exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "add", "project", "--auto-connect", "--project", projectDir, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add project exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "list", "--project", projectDir, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection list exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Connections []struct {
			Name string `json:"name"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("connection list output is invalid JSON: %v", err)
	}
	if len(got.Connections) != 1 || got.Connections[0].Name != "project" {
		t.Fatalf("connection list = %+v, want project only", got)
	}
}

func TestConnectionResolveJSON(t *testing.T) {
	stateDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "default", "--auto-connect", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "resolve", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection resolve exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK         bool   `json:"ok"`
		Source     string `json:"source"`
		Connection struct {
			Name string `json:"name"`
			Mode string `json:"mode"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("connection resolve output is invalid JSON: %v", err)
	}
	if !got.OK || got.Source != "selected" || got.Connection.Name != "default" || got.Connection.Mode != "auto_connect" {
		t.Fatalf("connection resolve = %+v, want selected default", got)
	}
}

func TestDoctorUsesSelectedConnection(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	stateDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "local", "--browser-url", server.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"doctor", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Checks []struct {
			Name           string `json:"name"`
			ConnectionMode string `json:"connection_mode"`
			Details        struct {
				State string `json:"state"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "browser_debug_endpoint" {
			if check.ConnectionMode != "browser_url" || check.Details.State != "listening_not_cdp" {
				t.Fatalf("browser check = %+v, want selected browser_url state", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want browser_debug_endpoint", got.Checks)
}

func TestDoctorCheckFilterJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--check", "daemon", "--state-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Checks []struct {
			Name string `json:"name"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	if len(got.Checks) != 1 || got.Checks[0].Name != "daemon" {
		t.Fatalf("doctor checks = %+v, want daemon only", got.Checks)
	}
}

func TestDoctorUsesNamedConnection(t *testing.T) {
	notCDP := httptest.NewServer(http.NotFoundHandler())
	defer notCDP.Close()
	cdpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/144.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": "ws://example.test/devtools/browser/test",
		})
	}))
	defer cdpServer.Close()

	stateDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "selected", "--browser-url", notCDP.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add selected exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "add", "cdp", "--browser-url", cdpServer.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add cdp exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"doctor", "--connection", "cdp", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Checks []struct {
			Name           string `json:"name"`
			Status         string `json:"status"`
			ConnectionMode string `json:"connection_mode"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "browser_debug_endpoint" {
			if check.Status != "pass" || check.ConnectionMode != "browser_url" {
				t.Fatalf("browser check = %+v, want named cdp connection", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want browser_debug_endpoint", got.Checks)
}

func TestDoctorUsesProjectConnection(t *testing.T) {
	notCDP := httptest.NewServer(http.NotFoundHandler())
	defer notCDP.Close()
	cdpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/144.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": "ws://example.test/devtools/browser/test",
		})
	}))
	defer cdpServer.Close()

	stateDir := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "selected", "--browser-url", notCDP.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add selected exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "add", "project", "--browser-url", cdpServer.URL, "--project", projectDir, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add project exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Chdir returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"doctor", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "browser_debug_endpoint" {
			if check.Status != "pass" {
				t.Fatalf("browser check = %+v, want project connection", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want browser_debug_endpoint", got.Checks)
}

func TestDoctorReportsDaemonConnectedWhenBrowserIsAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/144.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": "ws://example.test/devtools/browser/test",
		})
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--browser-url", server.URL, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			State  string `json:"state"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "daemon" {
			if check.Status != "pass" || check.State != "connected" {
				t.Fatalf("daemon check = %+v, want pass connected", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want daemon check", got.Checks)
}

func TestExplainErrorJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"explain-error", "not_implemented", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Error struct {
			Code     string `json:"code"`
			ExitCode int    `json:"exit_code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("explain-error output is invalid JSON: %v", err)
	}
	if !got.OK || got.Error.Code != "not_implemented" || got.Error.ExitCode != cli.ExitNotImplemented {
		t.Fatalf("explain-error = %+v, want not_implemented metadata", got)
	}
}

func TestExitCodesJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"exit-codes", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		ExitCodes []struct {
			Code int    `json:"code"`
			Name string `json:"name"`
		} `json:"exit_codes"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("exit-codes output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.ExitCodes) < 2 || got.ExitCodes[0].Code != cli.ExitOK {
		t.Fatalf("exit-codes = %+v, want ok plus error rows", got)
	}
}

func TestSchemaJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"schema", "error-envelope", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Schema struct {
			Name   string `json:"name"`
			Fields []struct {
				Name     string `json:"name"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("schema output is invalid JSON: %v", err)
	}
	if !got.OK || got.Schema.Name != "error-envelope" || len(got.Schema.Fields) == 0 {
		t.Fatalf("schema = %+v, want error-envelope fields", got)
	}
}

func TestDescribeCommandJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"describe", "--command", "daemon status", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Commands struct {
			Name     string   `json:"name"`
			Use      string   `json:"use"`
			Examples []string `json:"examples"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("describe output is invalid JSON: %v", err)
	}
	if !got.OK || got.Commands.Name != "status" || !strings.Contains(got.Commands.Use, "daemon status") || len(got.Commands.Examples) == 0 {
		t.Fatalf("describe --command = %+v, want daemon status command", got)
	}
}

func TestDescribeCommandIncludesLocalFlagsJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Commands struct {
			Name  string `json:"name"`
			Flags []struct {
				Name    string `json:"name"`
				Default string `json:"default,omitempty"`
				Usage   string `json:"usage"`
			} `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("describe pages output is invalid JSON: %v", err)
	}
	for _, flag := range got.Commands.Flags {
		if flag.Name == "title-contains" {
			if !got.OK || got.Commands.Name != "pages" || !strings.Contains(flag.Usage, "title") {
				t.Fatalf("title-contains flag = %+v in output %+v, want pages local flag", flag, got)
			}
			return
		}
	}
	t.Fatalf("describe pages flags = %+v, want title-contains", got.Commands.Flags)
}

func TestDescribeProtocolExamplesCommandJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"describe", "--command", "protocol examples", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Commands struct {
			Name     string   `json:"name"`
			Examples []string `json:"examples"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("describe protocol examples output is invalid JSON: %v", err)
	}
	if !got.OK || got.Commands.Name != "examples" || len(got.Commands.Examples) == 0 || !strings.Contains(got.Commands.Examples[0], "Page.captureScreenshot") {
		t.Fatalf("describe protocol examples = %+v, want Page.captureScreenshot example", got)
	}
}

func TestDescribeVersionCommandExamplesJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "version", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe version exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Commands struct {
			Name     string   `json:"name"`
			Examples []string `json:"examples"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("describe version output is invalid JSON: %v", err)
	}
	if !got.OK || got.Commands.Name != "version" || !hasExampleContaining(got.Commands.Examples, "version --json") {
		t.Fatalf("describe version = %+v, want version --json example", got)
	}
}

func hasExampleContaining(examples []string, needle string) bool {
	for _, example := range examples {
		if strings.Contains(example, needle) {
			return true
		}
	}
	return false
}

func TestProtocolExamplesSchemaJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"schema", "protocol-examples", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Schema struct {
			Name   string `json:"name"`
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("schema protocol-examples output is invalid JSON: %v", err)
	}
	if !got.OK || got.Schema.Name != "protocol-examples" || !schemaHasField(got.Schema.Fields, "examples") {
		t.Fatalf("schema protocol-examples = %+v, want examples field", got)
	}
}

func schemaHasField(fields []struct {
	Name string `json:"name"`
}, name string) bool {
	for _, field := range fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func TestDoctorBrowserURLWarnsForNonCDPEndpoint(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--browser-url", server.URL, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Details struct {
				State      string `json:"state"`
				HTTPStatus int    `json:"http_status"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "browser_debug_endpoint" {
			if check.Status != "warn" || check.Details.State != "listening_not_cdp" || check.Details.HTTPStatus != http.StatusNotFound {
				t.Fatalf("browser check = %+v, want listening_not_cdp warning", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want browser_debug_endpoint", got.Checks)
}

func TestDoctorCapabilitiesJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--capabilities", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor --capabilities exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK           bool `json:"ok"`
		Capabilities []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor --capabilities output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Capabilities) == 0 {
		t.Fatalf("doctor --capabilities = %+v, want capabilities", got)
	}
	if got.Capabilities[0].Name != "connection" || got.Capabilities[0].Status != "implemented" {
		t.Fatalf("first capability = %+v, want implemented connection", got.Capabilities[0])
	}
	if status := capabilityStatus(got.Capabilities, "advanced_storage"); status != "implemented" {
		t.Fatalf("advanced_storage capability status = %q, want implemented", status)
	}
}

func TestDoctorCapabilitiesSchemaJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"schema", "doctor-capabilities", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("schema doctor-capabilities exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Schema struct {
			Name   string `json:"name"`
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("schema doctor-capabilities output is invalid JSON: %v", err)
	}
	if !got.OK || got.Schema.Name != "doctor-capabilities" || !schemaHasField(got.Schema.Fields, "capabilities") {
		t.Fatalf("schema doctor-capabilities = %+v, want capabilities field", got)
	}
}

func capabilityStatus(capabilities []struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}, name string) string {
	for _, capability := range capabilities {
		if capability.Name == name {
			return capability.Status
		}
	}
	return ""
}

func TestDoctorAutoConnectReportsPermissionFlow(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--auto-connect", "--user-data-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Checks []struct {
			Name               string `json:"name"`
			Status             string `json:"status"`
			ConnectionMode     string `json:"connection_mode"`
			RequiresUserAllow  bool   `json:"requires_user_allow"`
			DefaultProfileFlow bool   `json:"default_profile_flow"`
			Details            struct {
				State string `json:"state"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "browser_debug_endpoint" {
			if check.Status != "pending" || check.ConnectionMode != "auto_connect" || !check.RequiresUserAllow || !check.DefaultProfileFlow || check.Details.State != "permission_pending" {
				t.Fatalf("browser check = %+v, want auto_connect pending permission flow", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want browser_debug_endpoint", got.Checks)
}

func TestDoctorAutoConnectPassiveSkipsActiveProbe(t *testing.T) {
	userDataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte("1\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--auto-connect", "--user-data-dir", userDataDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Checks []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			State   string `json:"state"`
			Details struct {
				State string `json:"state"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	var sawDaemon, sawBrowser bool
	for _, check := range got.Checks {
		if check.Name == "daemon" {
			sawDaemon = true
			if check.Status != "pending" || check.State != "passive" {
				t.Fatalf("daemon check = %+v, want passive pending", check)
			}
		}
		if check.Name == "browser_debug_endpoint" {
			sawBrowser = true
			if check.Status != "pending" || check.Details.State != "active_probe_skipped" {
				t.Fatalf("browser check = %+v, want active_probe_skipped pending", check)
			}
		}
	}
	if !sawDaemon || !sawBrowser {
		t.Fatalf("doctor checks = %+v, want daemon and browser checks", got.Checks)
	}
}

func TestAutoConnectPagesRequiresRunningDaemon(t *testing.T) {
	userDataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte("1\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--auto-connect", "--user-data-dir", userDataDir, "--state-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitConnection {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitConnection, errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "connection_not_configured" || !strings.Contains(got.Message, "running cdp daemon") {
		t.Fatalf("pages error = %+v, want daemon-required remediation", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func fakeWebSocketEndpoint(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse fake server URL: %v", err)
	}
	u.Scheme = "ws"
	u.Path = "/devtools/browser/test"
	return u.String()
}

func startFakeDaemon(t *testing.T, server *httptest.Server, connectionMode string) string {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv("CDP_STATE_DIR", stateDir)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeWebSocketEndpoint(t, server.URL), connectionMode, 30*time.Second)
	}()
	waitForDaemonRuntime(t, ctx, stateDir)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})
	return stateDir
}

func waitForDaemonRuntime(t *testing.T, ctx context.Context, stateDir string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime, ok, err := daemon.LoadRuntime(ctx, stateDir)
		if err != nil {
			t.Fatalf("LoadRuntime returned error: %v", err)
		}
		if ok && daemon.RuntimeRunning(runtime) && daemon.RuntimeSocketReady(ctx, runtime) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("daemon runtime did not become ready")
}

func newFakeCDPServer(t *testing.T, targets []map[string]any) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		if server == nil {
			http.Error(w, "test server was not initialized", http.StatusInternalServerError)
			return
		}
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/144.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": wsURL,
		})
	})
	mux.HandleFunc("/json/protocol", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": map[string]string{"major": "1", "minor": "3"},
			"domains": []map[string]any{
				{
					"domain":      "Page",
					"description": "Page domain",
					"commands": []map[string]any{
						{"name": "navigate"},
						{"name": "captureScreenshot", "description": "Capture page pixels"},
					},
				},
				{
					"domain":       "Runtime",
					"experimental": true,
					"events": []map[string]any{
						{"name": "consoleAPICalled"},
					},
				},
			},
		})
	})
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		for {
			var req struct {
				ID        int64           `json:"id"`
				SessionID string          `json:"sessionId"`
				Method    string          `json:"method"`
				Params    json.RawMessage `json:"params"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			resp := map[string]any{
				"id": req.ID,
			}
			var events []map[string]any
			if req.SessionID != "" {
				resp["sessionId"] = req.SessionID
			}
			if req.Method == "Target.getTargets" {
				resp["result"] = map[string]any{"targetInfos": targets}
			} else if req.Method == "Target.getTargetInfo" {
				var params struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				var found map[string]any
				for _, target := range targets {
					if target["targetId"] == params.TargetID {
						found = target
						break
					}
				}
				if found == nil {
					resp["error"] = map[string]any{"code": -32000, "message": "target not found"}
				} else {
					resp["result"] = map[string]any{"targetInfo": found}
				}
			} else if req.Method == "Target.createTarget" {
				resp["result"] = map[string]any{"targetId": "created-page"}
			} else if req.Method == "Target.attachToTarget" {
				resp["result"] = map[string]any{"sessionId": "session-1"}
			} else if req.Method == "Target.detachFromTarget" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Target.activateTarget" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Target.closeTarget" {
				resp["result"] = map[string]any{"success": true}
			} else if req.Method == "Page.navigate" {
				resp["result"] = map[string]any{"frameId": "frame-1"}
			} else if req.Method == "Page.enable" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Page.reload" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Page.getNavigationHistory" {
				resp["result"] = map[string]any{
					"currentIndex": 1,
					"entries": []map[string]any{
						{"id": 1, "url": "https://example.test/previous", "title": "Previous"},
						{"id": 2, "url": "https://example.test/current", "title": "Current"},
						{"id": 3, "url": "https://example.test/next", "title": "Next"},
					},
				}
			} else if req.Method == "Page.navigateToHistoryEntry" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Network.enable" {
				resp["result"] = map[string]any{}
				events = append(events,
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.requestWillBeSent",
						"params": map[string]any{
							"requestId":   "request-ok",
							"loaderId":    "loader-1",
							"documentURL": "https://example.test/app?session=abc",
							"type":        "Document",
							"timestamp":   1.25,
							"wallTime":    2.5,
							"initiator":   map[string]any{"type": "parser", "url": "https://example.test/app", "lineNumber": 1},
							"request": map[string]any{
								"url":     "https://example.test/app?token=abc",
								"method":  "GET",
								"headers": map[string]any{"Accept": "text/html", "Authorization": "Bearer secret"},
							},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.requestWillBeSentExtraInfo",
						"params": map[string]any{
							"requestId": "request-ok",
							"headers":   map[string]any{"Accept": "text/html", "Authorization": "Bearer secret"},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.responseReceived",
						"params": map[string]any{
							"requestId": "request-ok",
							"type":      "Document",
							"response": map[string]any{
								"url":               "https://example.test/app?token=abc",
								"status":            200,
								"statusText":        "OK",
								"headers":           map[string]any{"Content-Type": "application/json", "Set-Cookie": "session=secret"},
								"mimeType":          "application/json",
								"protocol":          "h2",
								"remoteIPAddress":   "203.0.113.10",
								"remotePort":        443,
								"connectionId":      77,
								"connectionReused":  true,
								"encodedDataLength": 42,
								"timing":            map[string]any{"requestTime": 1.25, "receiveHeadersEnd": 12.5},
							},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.responseReceivedExtraInfo",
						"params": map[string]any{
							"requestId":  "request-ok",
							"statusCode": 200,
							"headers":    map[string]any{"Content-Type": "application/json", "Set-Cookie": "session=secret"},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.loadingFinished",
						"params":    map[string]any{"requestId": "request-ok", "encodedDataLength": 42},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.requestWillBeSent",
						"params": map[string]any{
							"requestId": "request-failed",
							"type":      "Fetch",
							"request": map[string]any{
								"url":         "https://example.test/api",
								"method":      "POST",
								"headers":     map[string]any{"Content-Type": "application/json", "X-CSRF-Token": "secret"},
								"hasPostData": true,
								"postData":    `{"csrf":"secret","query":"value"}`,
							},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.loadingFailed",
						"params": map[string]any{
							"requestId": "request-failed",
							"type":      "Fetch",
							"errorText": "net::ERR_FAILED",
						},
					},
				)
			} else if req.Method == "Network.getRequestPostData" {
				var params struct {
					RequestID string `json:"requestId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if params.RequestID == "request-failed" {
					resp["result"] = map[string]any{"postData": `{"csrf":"secret","query":"value"}`}
				} else {
					resp["error"] = map[string]any{"code": -32000, "message": "No post data available"}
				}
			} else if req.Method == "Network.getResponseBody" {
				var params struct {
					RequestID string `json:"requestId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if params.RequestID == "request-ok" {
					resp["result"] = map[string]any{"body": `{"ok":true,"token":"secret"}`, "base64Encoded": false}
				} else {
					resp["error"] = map[string]any{"code": -32000, "message": "No resource with given identifier found"}
				}
			} else if req.Method == "Network.getCookies" {
				resp["result"] = map[string]any{"cookies": []map[string]any{{
					"name":     "session",
					"value":    "secret",
					"domain":   "example.test",
					"path":     "/",
					"httpOnly": true,
					"secure":   true,
				}}}
			} else if req.Method == "Network.setCookie" {
				resp["result"] = map[string]any{"success": true}
			} else if req.Method == "Network.deleteCookies" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Storage.getUsageAndQuota" {
				resp["result"] = map[string]any{
					"usage":          128,
					"quota":          4096,
					"overrideActive": false,
					"usageBreakdown": []map[string]any{{"storageType": "local_storage", "usage": 64}},
				}
			} else if req.Method == "Runtime.enable" {
				resp["result"] = map[string]any{}
				events = append(events, map[string]any{
					"sessionId": req.SessionID,
					"method":    "Runtime.consoleAPICalled",
					"params": map[string]any{
						"type":      "error",
						"timestamp": 12.25,
						"args": []map[string]any{
							{"type": "string", "value": "Synthetic console error"},
						},
					},
				}, map[string]any{
					"sessionId": req.SessionID,
					"method":    "Runtime.exceptionThrown",
					"params": map[string]any{
						"timestamp": 12.75,
						"exceptionDetails": map[string]any{
							"text":         "Uncaught (in promise)",
							"url":          "https://example.test/assets/app.js",
							"lineNumber":   41,
							"columnNumber": 9,
							"scriptId":     "script-1",
							"exception": map[string]any{
								"type":        "object",
								"subtype":     "error",
								"className":   "TypeError",
								"description": "TypeError: failed to fetch dashboard",
							},
							"stackTrace": map[string]any{
								"callFrames": []map[string]any{{
									"functionName": "loadDashboard",
									"url":          "https://example.test/assets/app.js",
									"lineNumber":   41,
									"columnNumber": 9,
								}},
							},
						},
					},
				})
			} else if req.Method == "Log.enable" {
				resp["result"] = map[string]any{}
				events = append(events, map[string]any{
					"sessionId": req.SessionID,
					"method":    "Log.entryAdded",
					"params": map[string]any{
						"entry": map[string]any{
							"source":           "network",
							"level":            "error",
							"text":             "Synthetic network failure",
							"timestamp":        12.5,
							"url":              "https://example.test/api",
							"networkRequestId": "request-1",
						},
					},
				})
			} else if req.Method == "Performance.enable" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Performance.getMetrics" {
				resp["result"] = map[string]any{
					"metrics": []map[string]any{
						{"name": "Timestamp", "value": 123.5},
						{"name": "DomContentLoaded", "value": 124.5},
					},
				}
			} else if req.Method == "Runtime.evaluate" {
				resp["result"] = fakeRuntimeEvaluateResult(req.Params)
			} else if req.Method == "Page.captureScreenshot" {
				resp["result"] = map[string]any{
					"data": base64.StdEncoding.EncodeToString([]byte("synthetic screenshot")),
				}
			} else if req.Method == "Browser.getVersion" {
				resp["result"] = map[string]any{"product": "Chrome/Test", "protocolVersion": "1.3"}
			} else {
				resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
			}
			if err := wsjson.Write(r.Context(), conn, resp); err != nil {
				return
			}
			for _, event := range events {
				if err := wsjson.Write(r.Context(), conn, event); err != nil {
					return
				}
			}
		}
	})
	server = httptest.NewServer(mux)
	return server
}

func fakeRuntimeEvaluateResult(params json.RawMessage) map[string]any {
	var req struct {
		Expression string `json:"expression"`
	}
	_ = json.Unmarshal(params, &req)
	if strings.Contains(req.Expression, "__cdp_cli_text__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "main",
					"count":    1,
					"text":     "Synthetic main text",
					"items": []map[string]any{{
						"index":       0,
						"tag":         "main",
						"text":        "Synthetic main text",
						"text_length": 19,
						"rect":        map[string]any{"x": 0, "y": 0, "width": 600, "height": 200},
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_click__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "main",
					"count":    1,
					"clicked":  true,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_html__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "main",
					"count":    1,
					"items": []map[string]any{{
						"index":       0,
						"tag":         "main",
						"html":        "<main>Synthetic main text</main>",
						"html_length": 32,
						"truncated":   false,
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_dom_query__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "button",
					"count":    1,
					"nodes": []map[string]any{{
						"uid":        "css:button:0",
						"index":      0,
						"tag":        "button",
						"id_attr":    "save",
						"classes":    []string{"primary"},
						"role":       "button",
						"aria_label": "Save",
						"text":       "Save changes",
						"rect":       map[string]any{"x": 10, "y": 20, "width": 100, "height": 32},
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_css_inspect__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "main",
					"found":    true,
					"count":    1,
					"tag":      "main",
					"styles": map[string]string{
						"display":  "block",
						"position": "static",
					},
					"rect": map[string]any{"x": 0, "y": 0, "width": 600, "height": 200},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_layout_overflow__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "body *",
					"count":    1,
					"items": []map[string]any{{
						"uid":           "overflow:0",
						"index":         0,
						"tag":           "div",
						"text":          "Too wide",
						"rect":          map[string]any{"x": 0, "y": 0, "width": 320, "height": 20},
						"client_width":  320,
						"scroll_width":  640,
						"client_height": 20,
						"scroll_height": 20,
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_wait_text__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"kind":    "text",
					"needle":  "Ready",
					"matched": true,
					"count":   1,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_wait_selector__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"kind":     "selector",
					"selector": "main",
					"matched":  true,
					"count":    1,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_hn_frontpage__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":   "https://news.ycombinator.com/",
					"title": "Hacker News",
					"count": 1,
					"stories": []map[string]any{{
						"rank":         1,
						"id":           "123",
						"title":        "Synthetic HN story",
						"url":          "https://example.test/story",
						"site":         "example.test",
						"score":        42,
						"user":         "alice",
						"age":          "1 hour ago",
						"comments":     7,
						"comments_url": "https://news.ycombinator.com/item?id=123",
					}},
					"organization": map[string]string{
						"page_kind":             "table-based link aggregator front page",
						"container_selector":    "table.itemlist",
						"story_row_selector":    "tr.athing",
						"metadata_row_selector": "tr.athing + tr .subtext",
						"title_selector":        ".titleline > a",
						"rank_selector":         ".rank",
						"discussion_signal":     "score, author, age, and comment links live in the metadata row after each story row",
					},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_page_load_storage__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":                  "https://example.test/app",
					"origin":               "https://example.test",
					"cookie_keys":          []string{"session"},
					"local_storage_keys":   []string{"feature"},
					"session_storage_keys": []string{"nonce"},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_snapshot__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":    "https://example.test/app",
					"origin": "https://example.test",
					"local_storage": map[string]any{
						"count": 2,
						"keys":  []string{"authToken", "feature"},
						"entries": []map[string]any{
							{"key": "authToken", "value": "secret", "bytes": 6},
							{"key": "feature", "value": "enabled", "bytes": 7},
						},
					},
					"session_storage": map[string]any{
						"count":   1,
						"keys":    []string{"nonce"},
						"entries": []map[string]any{{"key": "nonce", "value": "abc", "bytes": 3}},
					},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_page_info__") {
		return map[string]any{
			"result": map[string]any{
				"type":  "object",
				"value": map[string]any{"url": "https://example.test/app", "origin": "https://example.test"},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_get__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":     "https://example.test/app",
					"origin":  "https://example.test",
					"backend": "localStorage",
					"key":     "feature",
					"found":   true,
					"value":   "enabled",
					"bytes":   7,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_set__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"origin":   "https://example.test",
					"backend":  "localStorage",
					"key":      "feature",
					"found":    true,
					"value":    "disabled",
					"previous": "enabled",
					"bytes":    8,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_delete__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"origin":   "https://example.test",
					"backend":  "sessionStorage",
					"key":      "nonce",
					"found":    true,
					"previous": "abc",
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_clear__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":     "https://example.test/app",
					"origin":  "https://example.test",
					"backend": "sessionStorage",
					"cleared": 1,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "querySelectorAll") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/feed",
					"title":    "Example Feed",
					"selector": "article",
					"count":    1,
					"items": []map[string]any{
						{
							"index":       0,
							"tag":         "article",
							"role":        "article",
							"aria_label":  "",
							"text":        "First visible synthetic post",
							"text_length": 28,
							"href":        "",
							"rect": map[string]any{
								"x": 0, "y": 10, "width": 600, "height": 120,
							},
						},
					},
				},
			},
		}
	}
	return map[string]any{
		"result": map[string]any{
			"type":  "string",
			"value": "Example App",
		},
	}
}

func TestPagesTitleContainsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Docs Home", "url": "https://example.test/docs", "attached": false},
		{"targetId": "page-2", "type": "page", "title": "Admin", "url": "https://example.test/admin", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--title-contains", "docs", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Pages []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages output is invalid JSON: %v", err)
	}
	if len(got.Pages) != 1 || got.Pages[0].ID != "page-1" || got.Pages[0].Title != "Docs Home" {
		t.Fatalf("pages output = %+v, want Docs Home only", got.Pages)
	}
}

func TestEvalTitleContainsSelectsPage(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Admin", "url": "https://example.test/app", "attached": false},
		{"targetId": "page-2", "type": "page", "title": "Docs Portal", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"eval", "document.title", "--title-contains", "portal", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("eval exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Target struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("eval output is invalid JSON: %v", err)
	}
	if got.Target.ID != "page-2" || got.Target.Title != "Docs Portal" {
		t.Fatalf("eval target = %+v, want Docs Portal page", got.Target)
	}
}
