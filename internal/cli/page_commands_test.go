package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

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

func TestPageCleanupJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-visible", "type": "page", "title": "Visible Page", "url": "https://example.test/visible", "attached": false},
		{"targetId": "page-hidden", "type": "page", "title": "Hidden Page", "url": "https://example.test/hidden", "attached": false},
		{"targetId": "page-attached", "type": "page", "title": "Attached Page", "url": "https://example.test/attached", "attached": true},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"page", "cleanup", "--include-url", "example.test", "--idle-for", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page cleanup exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK      bool `json:"ok"`
		Cleanup struct {
			DryRun         bool `json:"dry_run"`
			CandidateCount int  `json:"candidate_count"`
			ClosedCount    int  `json:"closed_count"`
		} `json:"cleanup"`
		Candidates []struct {
			Target struct {
				ID string `json:"targetId"`
			} `json:"target"`
			VisibilityState string `json:"visibility_state"`
			Hidden          bool   `json:"hidden"`
			KeepReason      string `json:"keep_reason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("page cleanup output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Cleanup.DryRun || got.Cleanup.CandidateCount != 1 || got.Cleanup.ClosedCount != 0 {
		t.Fatalf("page cleanup summary = %+v, want one dry-run candidate", got.Cleanup)
	}
	if len(got.Candidates) != 3 || got.Candidates[0].KeepReason != "visible" || got.Candidates[1].KeepReason != "" || !got.Candidates[1].Hidden || got.Candidates[2].KeepReason != "attached" {
		t.Fatalf("page cleanup candidates = %+v, want visible kept, hidden candidate, attached kept", got.Candidates)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"page", "cleanup", "--include-url", "example.test", "--idle-for", "0s", "--close", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("page cleanup close exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var closed struct {
		Cleanup struct {
			DryRun      bool `json:"dry_run"`
			ClosedCount int  `json:"closed_count"`
		} `json:"cleanup"`
		Closed []struct {
			Target struct {
				ID string `json:"targetId"`
			} `json:"target"`
		} `json:"closed"`
	}
	if err := json.Unmarshal(out.Bytes(), &closed); err != nil {
		t.Fatalf("page cleanup close output is invalid JSON: %v", err)
	}
	if closed.Cleanup.DryRun || closed.Cleanup.ClosedCount != 1 || len(closed.Closed) != 1 || closed.Closed[0].Target.ID != "page-hidden" {
		t.Fatalf("page cleanup close = %+v, want hidden page closed", closed)
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

	stateDir := shortCLIStateDir(t)
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

func TestPagesIncludeBrowserBudgetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": true},
		{"targetId": "page-window-2", "type": "page", "title": "Docs", "url": "https://docs.example.test/", "attached": false},
		{"targetId": "worker-1", "type": "service_worker", "title": "Worker", "url": "https://example.test/sw.js", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Budget struct {
			TabCount          int            `json:"tab_count"`
			WindowCount       int            `json:"window_count"`
			WindowCountKnown  bool           `json:"window_count_known"`
			AttachedPageCount int            `json:"attached_page_count"`
			TargetTypeCounts  map[string]int `json:"target_type_counts"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("pages output is invalid JSON: %v", err)
	}
	if !got.OK || got.Budget.TabCount != 2 || got.Budget.WindowCount != 2 || !got.Budget.WindowCountKnown || got.Budget.AttachedPageCount != 1 || got.Budget.TargetTypeCounts["service_worker"] != 1 {
		t.Fatalf("pages budget = %+v, want tab/window budget summary", got.Budget)
	}
}

func TestOpenRefusesOverBudgetJSON(t *testing.T) {
	targets := make([]map[string]any, 0, cdp.DefaultMaxTabs)
	for i := 0; i < cdp.DefaultMaxTabs; i++ {
		targets = append(targets, map[string]any{"targetId": fmt.Sprintf("page-%02d", i+1), "type": "page", "title": "Tab", "url": "https://example.test/tab", "attached": false})
	}
	server := newFakeCDPServer(t, targets)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"open", "https://example.test/new", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitConnection {
		t.Fatalf("open exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitConnection, out.String(), errOut.String())
	}
	var got struct {
		OK             bool   `json:"ok"`
		Code           string `json:"code"`
		ErrClass       string `json:"err_class"`
		ResourceBudget struct {
			TabCount       int  `json:"tab_count"`
			MaxTabs        int  `json:"max_tabs"`
			TabsOverBudget bool `json:"tabs_over_budget"`
		} `json:"resource_budget"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("open error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "browser_resource_budget_exceeded" || got.ErrClass != "resource_budget" || got.ResourceBudget.TabCount != cdp.DefaultMaxTabs || !got.ResourceBudget.TabsOverBudget {
		t.Fatalf("open error = %+v, want resource budget refusal", got)
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

func TestHTMLCommandEmptyDiagnosticsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"html", "empty", "--diagnose-empty", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("html exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK          bool                `json:"ok"`
		Warnings    []string            `json:"warnings"`
		HTML        struct{ Count int } `json:"html"`
		Diagnostics struct {
			SelectorMatched    bool     `json:"selector_matched"`
			SelectorMatchCount int      `json:"selector_match_count"`
			FrameCount         int      `json:"frame_count"`
			ShadowRootCount    int      `json:"shadow_root_count"`
			PossibleCauses     []string `json:"possible_causes"`
			SuggestedCommands  []string `json:"suggested_commands"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("html output is invalid JSON: %v", err)
	}
	if !got.OK || got.HTML.Count != 0 || len(got.Warnings) == 0 || !got.Diagnostics.SelectorMatched || got.Diagnostics.SelectorMatchCount != 1 || got.Diagnostics.FrameCount != 2 || got.Diagnostics.ShadowRootCount != 1 {
		t.Fatalf("html empty diagnostics = %+v, want empty extraction diagnostics", got)
	}
	if !containsString(got.Diagnostics.PossibleCauses, "iframe_content") || !containsString(got.Diagnostics.SuggestedCommands, "cdp frames --target page-1 --json") {
		t.Fatalf("html empty diagnostics = %+v, want causes and suggested commands", got.Diagnostics)
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

func TestWaitEvalJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "eval", "window.__rendered === true", "--timeout", "1s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait eval exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind       string          `json:"kind"`
			Expression string          `json:"expression"`
			Matched    bool            `json:"matched"`
			Value      json.RawMessage `json:"value"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait eval output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "eval" || got.Wait.Expression != "window.__rendered === true" || !got.Wait.Matched || string(got.Wait.Value) != "true" {
		t.Fatalf("wait eval output = %+v, want matched eval", got)
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

func TestSnapshotEmptyDiagnosticsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/feed", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"snapshot", "--selector", "empty", "--debug-empty", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("snapshot exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK          bool                `json:"ok"`
		Warnings    []string            `json:"warnings"`
		Snapshot    struct{ Count int } `json:"snapshot"`
		Diagnostics struct {
			SelectorMatched   bool     `json:"selector_matched"`
			BodyTextLength    int      `json:"body_text_length"`
			FrameCount        int      `json:"frame_count"`
			PossibleCauses    []string `json:"possible_causes"`
			SuggestedCommands []string `json:"suggested_commands"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("snapshot output is invalid JSON: %v", err)
	}
	if !got.OK || got.Snapshot.Count != 0 || len(got.Warnings) == 0 || !got.Diagnostics.SelectorMatched || got.Diagnostics.BodyTextLength != 0 || got.Diagnostics.FrameCount != 2 {
		t.Fatalf("snapshot empty diagnostics = %+v, want empty extraction diagnostics", got)
	}
	if !containsString(got.Diagnostics.PossibleCauses, "shadow_dom") || !containsString(got.Diagnostics.SuggestedCommands, "cdp html body --target page-1 --diagnose-empty --json") {
		t.Fatalf("snapshot empty diagnostics = %+v, want causes and suggested commands", got.Diagnostics)
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

func TestScreenshotRenderJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "diagram.html")
	if err := os.WriteFile(htmlPath, []byte("<main>ready</main>"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	outPath := filepath.Join(dir, "diagram.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"screenshot", "render", htmlPath, "--out", outPath, "--width", "800", "--height", "600", "--dpr", "2", "--wait-for", "window.__rendered === true", "--serve", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("screenshot render exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Render struct {
			Served   bool   `json:"served"`
			WaitFor  string `json:"wait_for"`
			Viewport struct {
				Width int `json:"width"`
			} `json:"viewport"`
		} `json:"render"`
		Screenshot struct {
			Path string `json:"path"`
		} `json:"screenshot"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("screenshot render output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Render.Served || got.Render.WaitFor != "window.__rendered === true" || got.Render.Viewport.Width != 800 || got.Screenshot.Path != outPath {
		t.Fatalf("screenshot render output = %+v, want render metadata", got)
	}
}

func TestShotElementNavJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/feed", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "element.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"screenshot", "--out", outPath, "--element", "main", "--navigate", "https://example.test/next", "--wait", "0s", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("screenshot element exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK         bool `json:"ok"`
		Screenshot struct {
			Element  string `json:"element"`
			Navigate struct {
				URL string `json:"url"`
			} `json:"navigate"`
			Clip struct {
				Width float64 `json:"width"`
			} `json:"clip"`
		} `json:"screenshot"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("screenshot element output is invalid JSON: %v", err)
	}
	if !got.OK || got.Screenshot.Element != "main" || got.Screenshot.Navigate.URL != "https://example.test/next" || got.Screenshot.Clip.Width <= 0 {
		t.Fatalf("screenshot element output = %+v, want element metadata", got)
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
