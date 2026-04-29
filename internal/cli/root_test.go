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

func TestPlannedCommandJSONError(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"workflow", "perf", "https://example.test", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitNotImplemented {
		t.Fatalf("Execute exit code = %d, want %d", code, cli.ExitNotImplemented)
	}

	var got struct {
		OK       bool   `json:"ok"`
		Code     string `json:"code"`
		ErrClass string `json:"err_class"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "not_implemented" || got.ErrClass != "not_implemented" {
		t.Fatalf("error envelope = %+v, want not_implemented", got)
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
			Level string `json:"level"`
			Text  string `json:"text"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("workflow console-errors output is invalid JSON: %v", err)
	}
	if !got.OK || got.Workflow.Name != "console-errors" || got.Workflow.Count == 0 || got.Messages[0].Level != "error" {
		t.Fatalf("workflow console-errors = %+v, want error summary", got)
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
	if len(got.Requests) != 2 || got.Requests[0].Status != 200 || len(got.Messages) != 2 {
		t.Fatalf("workflow page-load evidence requests=%+v messages=%+v, want network and console evidence", got.Requests, got.Messages)
	}
	if len(got.Storage.LocalStorageKeys) != 1 || got.Storage.LocalStorageKeys[0] != "feature" || got.Performance.Count != 2 || got.Artifact.Path != outPath {
		t.Fatalf("workflow page-load storage/performance/artifact = storage=%+v performance=%+v artifact=%+v", got.Storage, got.Performance, got.Artifact)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("page-load artifact was not written: %v", err)
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
			ID     int    `json:"id"`
			Source string `json:"source"`
			Type   string `json:"type"`
			Level  string `json:"level"`
			Text   string `json:"text"`
		} `json:"messages"`
		Console struct {
			Count      int  `json:"count"`
			ErrorsOnly bool `json:"errors_only"`
		} `json:"console"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("console output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "page-1" || got.Console.Count != 2 || !got.Console.ErrorsOnly {
		t.Fatalf("console output = %+v, want two error messages", got)
	}
	if got.Messages[0].ID != 0 || got.Messages[0].Source != "runtime" || got.Messages[0].Type != "error" || got.Messages[0].Text != "Synthetic console error" {
		t.Fatalf("first console message = %+v, want runtime error", got.Messages[0])
	}
	if got.Messages[1].Source != "network" || got.Messages[1].Level != "error" || got.Messages[1].Text != "Synthetic network failure" {
		t.Fatalf("second console message = %+v, want network log error", got.Messages[1])
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
							"requestId": "request-ok",
							"type":      "Document",
							"request":   map[string]any{"url": "https://example.test/app", "method": "GET"},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.responseReceived",
						"params": map[string]any{
							"requestId": "request-ok",
							"type":      "Document",
							"response":  map[string]any{"url": "https://example.test/app", "status": 200, "statusText": "OK", "mimeType": "text/html"},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.requestWillBeSent",
						"params": map[string]any{
							"requestId": "request-failed",
							"type":      "Fetch",
							"request":   map[string]any{"url": "https://example.test/api", "method": "POST"},
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
