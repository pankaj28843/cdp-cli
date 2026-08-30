package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestVersionJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"version", "--json"}, &out, &errOut, cli.BuildInfo{
		Version:    "1.2.3",
		Commit:     "0123456789abcdef0123456789abcdef01234567",
		Date:       "2026-07-18T12:34:56Z",
		Dirty:      true,
		Verified:   true,
		Provenance: "managed",
	})
	if code != 0 {
		t.Fatalf("Execute exit code = %d, want 0; stderr=%s", code, errOut.String())
	}

	var got cli.BuildInfo
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("version output is invalid JSON: %v", err)
	}
	if got.Version != "1.2.3" || got.Commit != "0123456789abcdef0123456789abcdef01234567" || got.Date != "2026-07-18T12:34:56Z" || !got.Dirty || !got.Verified || got.Provenance != "managed" || got.SourceState != "dirty" {
		t.Fatalf("build info = %+v, want verified dirty managed provenance", got)
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

func TestVersionDirectGoBuildIsExplicitlyUnverified(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"version", "--json"}, &out, &errOut, cli.BuildInfo{Version: "dev", Commit: "unknown", Date: "unknown"})
	if code != cli.ExitOK {
		t.Fatalf("version exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got cli.BuildInfo
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("version output is invalid JSON: %v", err)
	}
	if got.Verified || got.Provenance != "unverified" || got.SourceState != "unverified" {
		t.Fatalf("direct build info = %+v, want explicit unverified provenance", got)
	}

	out.Reset()
	code = cli.Execute(context.Background(), []string{"version"}, &out, &errOut, cli.BuildInfo{Version: "dev", Commit: "unknown", Date: "unknown"})
	if code != cli.ExitOK || !strings.Contains(out.String(), "unverified build") {
		t.Fatalf("direct build text = %q exit=%d, want unverified label", out.String(), code)
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

func TestDoctorBrowserHealthJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--check", "browser-health", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor browser-health exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		Checks []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Details struct {
				State    string `json:"state"`
				TabCount int    `json:"tab_count"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor browser-health output is invalid JSON: %v", err)
	}
	if len(got.Checks) != 1 || got.Checks[0].Name != "browser-health" || got.Checks[0].Status != "pass" || got.Checks[0].Details.State != "healthy" || got.Checks[0].Details.TabCount != 1 {
		t.Fatalf("doctor browser-health = %+v, want healthy tab summary", got.Checks)
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

func TestWaitRequestJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "request", "--match-url", "/api", "--method", "POST", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait request exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind          string `json:"kind"`
			Matched       bool   `json:"matched"`
			ObservedCount int    `json:"observed_count"`
			Criteria      struct {
				URLContains string `json:"url_contains"`
				Method      string `json:"method"`
			} `json:"criteria"`
		} `json:"wait"`
		Event struct {
			Kind      string `json:"kind"`
			CDPMethod string `json:"cdp_method"`
			RequestID string `json:"request_id"`
			URL       string `json:"url"`
			Method    string `json:"method"`
		} `json:"event"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait request output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "request" || !got.Wait.Matched || got.Wait.Criteria.URLContains != "/api" || got.Wait.Criteria.Method != "POST" || got.Event.CDPMethod != "Network.requestWillBeSent" || got.Event.RequestID != "request-failed" || got.Event.Method != "POST" || got.Event.URL != "https://example.test/api" || got.Wait.ObservedCount < 2 {
		t.Fatalf("wait request output = %+v, want matched POST /api request", got)
	}
}

func TestWaitResponseJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "response", "--match-url", "/app", "--method", "GET", "--status", "200", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait response exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind     string `json:"kind"`
			Matched  bool   `json:"matched"`
			Criteria struct {
				URLContains string `json:"url_contains"`
				Method      string `json:"method"`
				Status      int    `json:"status"`
			} `json:"criteria"`
			Evidence struct {
				Headers bool `json:"headers"`
				Bodies  bool `json:"bodies"`
				Bounded bool `json:"bounded"`
			} `json:"evidence"`
		} `json:"wait"`
		Event struct {
			Kind      string `json:"kind"`
			CDPMethod string `json:"cdp_method"`
			RequestID string `json:"request_id"`
			URL       string `json:"url"`
			Method    string `json:"method"`
			Status    int    `json:"status"`
		} `json:"event"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait response output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "response" || !got.Wait.Matched || got.Wait.Criteria.URLContains != "/app" || got.Wait.Criteria.Method != "GET" || got.Wait.Criteria.Status != 200 || got.Event.CDPMethod != "Network.responseReceived" || got.Event.RequestID != "request-ok" || got.Event.Method != "GET" || got.Event.Status != 200 {
		t.Fatalf("wait response output = %+v, want matched GET 200 response", got)
	}
	if got.Wait.Evidence.Headers || got.Wait.Evidence.Bodies || !got.Wait.Evidence.Bounded {
		t.Fatalf("wait response evidence = %+v, want bounded evidence without headers or bodies", got.Wait.Evidence)
	}
	if strings.Contains(got.Event.URL, "token=abc") {
		t.Fatalf("wait response URL was not redacted: %q", got.Event.URL)
	}
}

func TestWaitResponseTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--timeout", "200ms", "wait", "response", "--match-url", "does-not-exist", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("wait response timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Wait struct {
				Kind          string `json:"kind"`
				Matched       bool   `json:"matched"`
				EventCount    int    `json:"event_count"`
				ObservedCount int    `json:"observed_count"`
				Criteria      struct {
					URLContains string `json:"url_contains"`
				} `json:"criteria"`
			} `json:"wait"`
			LastEvent struct {
				Kind   string `json:"kind"`
				Status int    `json:"status"`
				URL    string `json:"url"`
			} `json:"last_event"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait response timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Wait.Kind != "response" || got.Data.Wait.Matched || got.Data.Wait.Criteria.URLContains != "does-not-exist" || got.Data.Wait.EventCount == 0 || got.Data.Wait.ObservedCount == 0 || got.Data.LastEvent.Kind != "response" || got.Data.LastEvent.Status != 200 {
		t.Fatalf("wait response timeout = %+v, want timeout with bounded wait evidence", got)
	}
	if !containsString(got.RemediationCommands, "cdp network --wait 5s --json") {
		t.Fatalf("wait response remediation commands = %+v, want network diagnostic command", got.RemediationCommands)
	}
	if strings.Contains(got.Data.LastEvent.URL, "token=abc") {
		t.Fatalf("wait response timeout last URL was not redacted: %q", got.Data.LastEvent.URL)
	}
}

func TestWaitNetworkIdleJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "network-idle", "--idle", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait network-idle exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Kind           string   `json:"kind"`
			Matched        bool     `json:"matched"`
			Idle           string   `json:"idle"`
			MaxInflight    int      `json:"max_inflight"`
			EventCount     int      `json:"event_count"`
			RequestCount   int      `json:"request_count"`
			CompletedCount int      `json:"completed_count"`
			FailedCount    int      `json:"failed_count"`
			InFlightCount  int      `json:"in_flight_count"`
			InFlight       []any    `json:"in_flight"`
			Warnings       []string `json:"warnings"`
			LastEvent      struct {
				Kind      string `json:"kind"`
				CDPMethod string `json:"cdp_method"`
				RequestID string `json:"request_id"`
			} `json:"last_event"`
			Evidence struct {
				Headers bool `json:"headers"`
				Bodies  bool `json:"bodies"`
				Bounded bool `json:"bounded"`
			} `json:"evidence"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait network-idle output is invalid JSON: %v", err)
	}
	if !got.OK || got.Wait.Kind != "network-idle" || !got.Wait.Matched || got.Wait.Idle != "10ms" || got.Wait.MaxInflight != 0 || got.Wait.EventCount == 0 || got.Wait.RequestCount != 2 || got.Wait.CompletedCount != 1 || got.Wait.FailedCount != 1 || got.Wait.InFlightCount != 0 || len(got.Wait.InFlight) != 0 || got.Wait.LastEvent.Kind != "loading-failed" || got.Wait.LastEvent.CDPMethod != "Network.loadingFailed" || got.Wait.LastEvent.RequestID != "request-failed" {
		t.Fatalf("wait network-idle output = %+v, want quiet network evidence", got)
	}
	if len(got.Wait.Warnings) == 0 || !strings.Contains(got.Wait.Warnings[0], "not proof") {
		t.Fatalf("wait network-idle warnings = %+v, want readiness warning", got.Wait.Warnings)
	}
	if got.Wait.Evidence.Headers || got.Wait.Evidence.Bodies || !got.Wait.Evidence.Bounded {
		t.Fatalf("wait network-idle evidence = %+v, want bounded evidence without headers or bodies", got.Wait.Evidence)
	}
}

func TestWaitNetworkIdleTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "busy-page", "type": "page", "title": "Busy App", "url": "https://example.test/busy", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "network-idle", "--idle", "50ms", "--timeout", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("wait network-idle timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Wait struct {
				Kind          string `json:"kind"`
				Matched       bool   `json:"matched"`
				InFlightCount int    `json:"in_flight_count"`
				InFlight      []struct {
					RequestID string `json:"request_id"`
					URL       string `json:"url"`
				} `json:"in_flight"`
				LastEvent struct {
					Kind      string `json:"kind"`
					RequestID string `json:"request_id"`
					URL       string `json:"url"`
				} `json:"last_event"`
			} `json:"wait"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait network-idle timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Wait.Kind != "network-idle" || got.Data.Wait.Matched || got.Data.Wait.InFlightCount != 1 || len(got.Data.Wait.InFlight) != 1 || got.Data.Wait.InFlight[0].RequestID != "request-pending" || got.Data.Wait.LastEvent.Kind != "request" || got.Data.Wait.LastEvent.RequestID != "request-pending" {
		t.Fatalf("wait network-idle timeout = %+v, want pending request evidence", got)
	}
	if strings.Contains(got.Data.Wait.InFlight[0].URL, "token=abc") || !strings.Contains(got.Data.Wait.InFlight[0].URL, "redacted") {
		t.Fatalf("wait network-idle in-flight URL was not safely redacted: %q", got.Data.Wait.InFlight[0].URL)
	}
	if !containsString(got.RemediationCommands, "cdp network --wait 5s --json") {
		t.Fatalf("wait network-idle remediation commands = %+v, want network diagnostic command", got.RemediationCommands)
	}
}

func TestWaitNetworkIdleIgnoreURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "busy-page", "type": "page", "title": "Busy App", "url": "https://example.test/busy", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "network-idle", "--idle", "50ms", "--ignore-url-contains", "/stream", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait network-idle ignore exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Wait struct {
			Matched           bool     `json:"matched"`
			IgnoredCount      int      `json:"ignored_count"`
			InFlightCount     int      `json:"in_flight_count"`
			IgnoreURLContains []string `json:"ignore_url_contains"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait network-idle ignore output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Wait.Matched || got.Wait.IgnoredCount != 1 || got.Wait.InFlightCount != 0 || len(got.Wait.IgnoreURLContains) != 1 || got.Wait.IgnoreURLContains[0] != "/stream" {
		t.Fatalf("wait network-idle ignore output = %+v, want ignored pending stream and idle match", got)
	}
}

func TestWaitNetworkIdleInvalidOptionsJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "network-idle", "--idle", "0", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("wait network-idle invalid exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait network-idle invalid output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || !strings.Contains(got.Message, "--idle must be positive") {
		t.Fatalf("wait network-idle invalid output = %+v, want usage error", got)
	}
}

func TestWaitDialogJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "dialog-page", "type": "page", "title": "Dialog App", "url": "https://example.test/dialog", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "dialog", "--type", "confirm", "--message-contains", "Delete", "--action", "dismiss", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait dialog exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Wait struct {
			Kind          string `json:"kind"`
			Matched       bool   `json:"matched"`
			CDPMethod     string `json:"cdp_method"`
			EventCount    int    `json:"event_count"`
			ObservedCount int    `json:"observed_count"`
			Criteria      struct {
				Type            string `json:"type"`
				MessageContains string `json:"message_contains"`
			} `json:"criteria"`
			Evidence struct {
				Headers bool `json:"headers"`
				Bodies  bool `json:"bodies"`
				Bounded bool `json:"bounded"`
			} `json:"evidence"`
		} `json:"wait"`
		Dialog struct {
			Type               string `json:"type"`
			Message            string `json:"message"`
			URL                string `json:"url"`
			FrameID            string `json:"frame_id"`
			CDPMethod          string `json:"cdp_method"`
			Action             string `json:"action"`
			Handled            bool   `json:"handled"`
			Accepted           bool   `json:"accepted"`
			PromptTextSupplied bool   `json:"prompt_text_supplied"`
		} `json:"dialog"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait dialog output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "dialog-page" || got.Wait.Kind != "dialog" || !got.Wait.Matched || got.Wait.CDPMethod != "Page.javascriptDialogOpening" || got.Wait.EventCount != 1 || got.Wait.ObservedCount != 1 || got.Wait.Criteria.Type != "confirm" || got.Wait.Criteria.MessageContains != "Delete" {
		t.Fatalf("wait dialog output = %+v, want matched dialog wait evidence", got)
	}
	if got.Dialog.Type != "confirm" || got.Dialog.Message != "Delete item?" || got.Dialog.FrameID != "frame-main" || got.Dialog.CDPMethod != "Page.javascriptDialogOpening" || got.Dialog.Action != "dismiss" || !got.Dialog.Handled || got.Dialog.Accepted || got.Dialog.PromptTextSupplied {
		t.Fatalf("wait dialog dialog = %+v, want dismissed confirm dialog evidence", got.Dialog)
	}
	if strings.Contains(got.Dialog.URL, "token=abc") || !strings.Contains(got.Dialog.URL, "redacted") {
		t.Fatalf("wait dialog URL was not safely redacted: %q", got.Dialog.URL)
	}
	if got.Wait.Evidence.Headers || got.Wait.Evidence.Bodies || !got.Wait.Evidence.Bounded {
		t.Fatalf("wait dialog evidence = %+v, want bounded evidence without headers or bodies", got.Wait.Evidence)
	}
	if !containsString(got.NextCommands, "cdp snapshot --json") {
		t.Fatalf("wait dialog next commands = %+v, want snapshot follow-up after handled dialog", got.NextCommands)
	}
}

func TestWaitDialogTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "dialog", "--type", "alert", "--timeout", "50ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("wait dialog timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Wait struct {
				Kind          string `json:"kind"`
				Matched       bool   `json:"matched"`
				EventCount    int    `json:"event_count"`
				ObservedCount int    `json:"observed_count"`
				Criteria      struct {
					Type string `json:"type"`
				} `json:"criteria"`
			} `json:"wait"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait dialog timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Wait.Kind != "dialog" || got.Data.Wait.Matched || got.Data.Wait.EventCount != 0 || got.Data.Wait.ObservedCount != 0 || got.Data.Wait.Criteria.Type != "alert" {
		t.Fatalf("wait dialog timeout = %+v, want timeout envelope with wait criteria", got)
	}
	if !containsString(got.RemediationCommands, "cdp dialog dismiss --wait --json") {
		t.Fatalf("wait dialog remediation commands = %+v, want dialog dismiss command", got.RemediationCommands)
	}
}

func TestWaitDialogInvalidOptionsJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "dialog", "--type", "modal", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("wait dialog invalid exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait dialog invalid output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || !strings.Contains(got.Message, "--type must be alert") {
		t.Fatalf("wait dialog invalid output = %+v, want usage error", got)
	}
}

func TestWaitFileChooserJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "file-chooser-page", "type": "page", "title": "Upload App", "url": "https://example.test/upload", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "file-chooser", "--mode", "single", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait file-chooser exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Wait struct {
			Kind          string `json:"kind"`
			Matched       bool   `json:"matched"`
			CDPMethod     string `json:"cdp_method"`
			EventCount    int    `json:"event_count"`
			ObservedCount int    `json:"observed_count"`
			Intercepted   bool   `json:"intercepted"`
			Criteria      struct {
				Mode string `json:"mode"`
			} `json:"criteria"`
			Evidence struct {
				Headers bool `json:"headers"`
				Bodies  bool `json:"bodies"`
				Bounded bool `json:"bounded"`
			} `json:"evidence"`
			Warnings []string `json:"warnings"`
		} `json:"wait"`
		FileChooser struct {
			FrameID       string `json:"frame_id"`
			Mode          string `json:"mode"`
			Multiple      bool   `json:"multiple"`
			BackendNodeID int    `json:"backend_node_id"`
			CDPMethod     string `json:"cdp_method"`
		} `json:"file_chooser"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait file-chooser output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "file-chooser-page" || got.Wait.Kind != "file-chooser" || !got.Wait.Matched || got.Wait.CDPMethod != "Page.fileChooserOpened" || got.Wait.EventCount != 1 || got.Wait.ObservedCount != 1 || got.Wait.Criteria.Mode != "selectSingle" || !got.Wait.Intercepted {
		t.Fatalf("wait file-chooser output = %+v, want matched chooser wait evidence", got)
	}
	if got.FileChooser.FrameID != "frame-upload" || got.FileChooser.Mode != "selectSingle" || got.FileChooser.Multiple || got.FileChooser.BackendNodeID != 42 || got.FileChooser.CDPMethod != "Page.fileChooserOpened" {
		t.Fatalf("wait file-chooser event = %+v, want single chooser metadata", got.FileChooser)
	}
	if got.Wait.Evidence.Headers || got.Wait.Evidence.Bodies || !got.Wait.Evidence.Bounded {
		t.Fatalf("wait file-chooser evidence = %+v, want bounded evidence without headers or bodies", got.Wait.Evidence)
	}
	if len(got.Wait.Warnings) == 0 || !strings.Contains(got.Wait.Warnings[0], "native dialog") {
		t.Fatalf("wait file-chooser warnings = %+v, want interception warning", got.Wait.Warnings)
	}
	if !containsString(got.NextCommands, "cdp file input[type=file] tmp/upload.txt --json") {
		t.Fatalf("wait file-chooser next commands = %+v, want cdp file follow-up", got.NextCommands)
	}
}

func TestWaitFileChooserTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "file-chooser", "--mode", "multiple", "--timeout", "50ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("wait file-chooser timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Wait struct {
				Kind          string `json:"kind"`
				Matched       bool   `json:"matched"`
				EventCount    int    `json:"event_count"`
				ObservedCount int    `json:"observed_count"`
				Intercepted   bool   `json:"intercepted"`
				Criteria      struct {
					Mode string `json:"mode"`
				} `json:"criteria"`
			} `json:"wait"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait file-chooser timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Wait.Kind != "file-chooser" || got.Data.Wait.Matched || got.Data.Wait.EventCount != 0 || got.Data.Wait.ObservedCount != 0 || got.Data.Wait.Criteria.Mode != "selectMultiple" || !got.Data.Wait.Intercepted {
		t.Fatalf("wait file-chooser timeout = %+v, want timeout envelope with wait criteria", got)
	}
	if !containsString(got.RemediationCommands, "cdp events tap --enable page --match Page.fileChooserOpened --duration 5s --json") {
		t.Fatalf("wait file-chooser remediation commands = %+v, want events tap command", got.RemediationCommands)
	}
}

func TestWaitFileChooserInvalidModeJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "file-chooser", "--mode", "directory", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("wait file-chooser invalid exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait file-chooser invalid output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || !strings.Contains(got.Message, "--mode must be") {
		t.Fatalf("wait file-chooser invalid output = %+v, want usage error", got)
	}
}

func TestWaitPopupJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "opener-page", "type": "page", "title": "Login App", "url": "https://example.test/login", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "popup", "--match-url", "/oauth/callback", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait popup exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Opener struct {
			ID string `json:"id"`
		} `json:"opener"`
		Wait struct {
			Kind          string   `json:"kind"`
			Matched       bool     `json:"matched"`
			CDPMethods    []string `json:"cdp_methods"`
			BaselineCount int      `json:"baseline_count"`
			EventCount    int      `json:"event_count"`
			ObservedCount int      `json:"observed_count"`
			Criteria      struct {
				OpenerID    string `json:"opener_id"`
				URLContains string `json:"url_contains"`
			} `json:"criteria"`
			Evidence struct {
				Headers bool `json:"headers"`
				Bodies  bool `json:"bodies"`
				Bounded bool `json:"bounded"`
			} `json:"evidence"`
		} `json:"wait"`
		Popup struct {
			CDPMethod     string `json:"cdp_method"`
			NewTarget     bool   `json:"new_target"`
			OpenerMatched bool   `json:"opener_matched"`
			Target        struct {
				ID              string `json:"id"`
				Title           string `json:"title"`
				URL             string `json:"url"`
				OpenerID        string `json:"opener_id"`
				CanAccessOpener bool   `json:"can_access_opener"`
			} `json:"target"`
		} `json:"popup"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait popup output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "opener-page" || got.Opener.ID != "opener-page" || got.Wait.Kind != "popup" || !got.Wait.Matched || got.Wait.BaselineCount != 1 || got.Wait.EventCount != 1 || got.Wait.ObservedCount != 1 || got.Wait.Criteria.OpenerID != "opener-page" || got.Wait.Criteria.URLContains != "/oauth/callback" {
		t.Fatalf("wait popup output = %+v, want matched popup wait evidence", got)
	}
	if !containsString(got.Wait.CDPMethods, "Target.targetCreated") || !containsString(got.Wait.CDPMethods, "Target.targetInfoChanged") {
		t.Fatalf("wait popup CDP methods = %+v, want Target discovery methods", got.Wait.CDPMethods)
	}
	if got.Popup.Target.ID != "popup-page" || got.Popup.Target.Title != "OAuth Popup" || got.Popup.Target.URL != "https://example.test/oauth/callback" || got.Popup.Target.OpenerID != "opener-page" || !got.Popup.Target.CanAccessOpener || got.Popup.CDPMethod != "Target.targetCreated" || !got.Popup.NewTarget || !got.Popup.OpenerMatched {
		t.Fatalf("wait popup popup = %+v, want target discovery metadata", got.Popup)
	}
	if got.Wait.Evidence.Headers || got.Wait.Evidence.Bodies || !got.Wait.Evidence.Bounded {
		t.Fatalf("wait popup evidence = %+v, want bounded evidence without headers or bodies", got.Wait.Evidence)
	}
	if !containsString(got.NextCommands, "cdp page select --target popup-page --json") {
		t.Fatalf("wait popup next commands = %+v, want page select follow-up", got.NextCommands)
	}
}

func TestWaitPopupTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "opener-page", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	// Popup discovery includes target listing, event subscription, and the
	// first event drain; leave scheduler margin for the fixture to observe the
	// non-matching popup before timing out.
	code := cli.Execute(context.Background(), []string{"wait", "popup", "--match-url", "/missing", "--timeout", "250ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("wait popup timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Wait struct {
				Kind          string `json:"kind"`
				Matched       bool   `json:"matched"`
				EventCount    int    `json:"event_count"`
				ObservedCount int    `json:"observed_count"`
				Criteria      struct {
					URLContains string `json:"url_contains"`
				} `json:"criteria"`
			} `json:"wait"`
			LastEvent struct {
				Target struct {
					ID string `json:"id"`
				} `json:"target"`
			} `json:"last_event"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait popup timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Wait.Kind != "popup" || got.Data.Wait.Matched || got.Data.Wait.EventCount != 1 || got.Data.Wait.ObservedCount != 1 || got.Data.Wait.Criteria.URLContains != "/missing" || got.Data.LastEvent.Target.ID != "popup-page" {
		t.Fatalf("wait popup timeout = %+v, want timeout envelope with last popup target", got)
	}
	if !containsString(got.RemediationCommands, "cdp protocol exec Target.getTargets --json") {
		t.Fatalf("wait popup remediation commands = %+v, want Target.getTargets command", got.RemediationCommands)
	}
}

func TestWaitDownloadJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "download-page", "type": "page", "title": "Download App", "url": "https://example.test/downloads", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	downloadDir := t.TempDir()
	guidPath := filepath.Join(downloadDir, "download-1")
	if err := os.WriteFile(guidPath, []byte("report bytes"), 0o600); err != nil {
		t.Fatalf("write GUID download fixture: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "download", "--match-url", "/download/report.csv", "--filename-contains", "report.csv", "--download-dir", downloadDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("wait download exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Wait struct {
			Kind          string   `json:"kind"`
			Matched       bool     `json:"matched"`
			CDPMethods    []string `json:"cdp_methods"`
			DownloadDir   string   `json:"download_dir"`
			EventCount    int      `json:"event_count"`
			ObservedCount int      `json:"observed_count"`
			Criteria      struct {
				URLContains      string `json:"url_contains"`
				FilenameContains string `json:"filename_contains"`
				State            string `json:"state"`
			} `json:"criteria"`
			Evidence struct {
				Headers bool `json:"headers"`
				Bodies  bool `json:"bodies"`
				Bounded bool `json:"bounded"`
			} `json:"evidence"`
			Warnings []string `json:"warnings"`
		} `json:"wait"`
		Event struct {
			Kind              string `json:"kind"`
			GUID              string `json:"guid"`
			URL               string `json:"url"`
			SuggestedFilename string `json:"suggested_filename"`
			FrameID           string `json:"frame_id"`
			CDPMethod         string `json:"cdp_method"`
		} `json:"event"`
		Progress struct {
			Kind          string  `json:"kind"`
			GUID          string  `json:"guid"`
			State         string  `json:"state"`
			TotalBytes    float64 `json:"total_bytes"`
			ReceivedBytes float64 `json:"received_bytes"`
			FilePath      string  `json:"file_path"`
			CDPMethod     string  `json:"cdp_method"`
		} `json:"progress"`
		Download struct {
			GUID              string  `json:"guid"`
			URL               string  `json:"url"`
			SuggestedFilename string  `json:"suggested_filename"`
			State             string  `json:"state"`
			Completed         bool    `json:"completed"`
			Canceled          bool    `json:"canceled"`
			TotalBytes        float64 `json:"total_bytes"`
			ReceivedBytes     float64 `json:"received_bytes"`
			FilePath          string  `json:"file_path"`
		} `json:"download"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait download output is invalid JSON: %v", err)
	}
	if !got.OK || got.Target.ID != "download-page" || got.Wait.Kind != "download" || !got.Wait.Matched || got.Wait.DownloadDir == "" || got.Wait.EventCount != 2 || got.Wait.ObservedCount != 1 || got.Wait.Criteria.URLContains != "/download/report.csv" || got.Wait.Criteria.FilenameContains != "report.csv" || got.Wait.Criteria.State != "completed" {
		t.Fatalf("wait download output = %+v, want matched download wait evidence", got)
	}
	if !containsString(got.Wait.CDPMethods, "Browser.downloadWillBegin") || !containsString(got.Wait.CDPMethods, "Browser.downloadProgress") {
		t.Fatalf("wait download CDP methods = %+v, want Browser download methods", got.Wait.CDPMethods)
	}
	if got.Event.Kind != "will-begin" || got.Event.GUID != "download-1" || got.Event.SuggestedFilename != "report.csv" || got.Event.FrameID != "frame-download" || got.Event.CDPMethod != "Browser.downloadWillBegin" {
		t.Fatalf("wait download event = %+v, want will-begin metadata", got.Event)
	}
	if strings.Contains(got.Event.URL, "token=abc") || strings.Contains(got.Download.URL, "token=abc") || !strings.Contains(got.Event.URL, "redacted") {
		t.Fatalf("wait download URL was not safely redacted: event=%q download=%q", got.Event.URL, got.Download.URL)
	}
	wantPath := filepath.Join(downloadDir, "report.csv")
	if got.Progress.Kind != "progress" || got.Progress.GUID != "download-1" || got.Progress.State != "completed" || got.Progress.TotalBytes != 18 || got.Progress.ReceivedBytes != 18 || got.Progress.FilePath != wantPath || got.Progress.CDPMethod != "Browser.downloadProgress" {
		t.Fatalf("wait download progress = %+v, want completed progress metadata", got.Progress)
	}
	if got.Download.GUID != "download-1" || got.Download.SuggestedFilename != "report.csv" || got.Download.State != "completed" || !got.Download.Completed || got.Download.Canceled || got.Download.TotalBytes != 18 || got.Download.ReceivedBytes != 18 || got.Download.FilePath != wantPath {
		t.Fatalf("wait download summary = %+v, want completed download", got.Download)
	}
	if got.Wait.Evidence.Headers || got.Wait.Evidence.Bodies || !got.Wait.Evidence.Bounded {
		t.Fatalf("wait download evidence = %+v, want bounded evidence without headers or bodies", got.Wait.Evidence)
	}
	if len(got.Wait.Warnings) == 0 || !strings.Contains(got.Wait.Warnings[0], "browser-scoped") {
		t.Fatalf("wait download warnings = %+v, want browser-scoped warning", got.Wait.Warnings)
	}
	hasListCommand := false
	for _, command := range got.NextCommands {
		if strings.HasPrefix(command, "ls -lah ") {
			hasListCommand = true
			break
		}
	}
	if !hasListCommand {
		t.Fatalf("wait download next commands = %+v, want download dir listing", got.NextCommands)
	}
	if content, err := os.ReadFile(wantPath); err != nil {
		t.Fatalf("read retained download: %v", err)
	} else if string(content) != "report bytes" {
		t.Errorf("retained download = %q, want report bytes", content)
	}
	if _, err := os.Lstat(guidPath); !os.IsNotExist(err) {
		t.Errorf("GUID path still exists after download finalization: %v", err)
	}
}

func TestWaitDownloadTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "download", "--match-url", "/missing", "--download-dir", t.TempDir(), "--timeout", "50ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("wait download timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Wait struct {
				Kind          string `json:"kind"`
				Matched       bool   `json:"matched"`
				EventCount    int    `json:"event_count"`
				ObservedCount int    `json:"observed_count"`
				Criteria      struct {
					URLContains string `json:"url_contains"`
					State       string `json:"state"`
				} `json:"criteria"`
			} `json:"wait"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait download timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Wait.Kind != "download" || got.Data.Wait.Matched || got.Data.Wait.EventCount != 0 || got.Data.Wait.ObservedCount != 0 || got.Data.Wait.Criteria.URLContains != "/missing" || got.Data.Wait.Criteria.State != "completed" {
		t.Fatalf("wait download timeout = %+v, want timeout envelope with criteria", got)
	}
	if !containsString(got.RemediationCommands, "cdp pages --json") {
		t.Fatalf("wait download remediation commands = %+v, want pages command", got.RemediationCommands)
	}
}

func TestWaitDownloadInvalidStateJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "download", "--state", "done", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("wait download invalid exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait download invalid output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || !strings.Contains(got.Message, "--state must be") {
		t.Fatalf("wait download invalid output = %+v, want usage error", got)
	}
}

func TestWaitNetworkUnsupportedRedactionJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"wait", "response", "--redact", "headers", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("wait response unsupported redaction exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("wait response unsupported redaction output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "usage" || !strings.Contains(got.Message, "--redact must be safe or none") {
		t.Fatalf("wait response unsupported redaction = %+v, want usage error", got)
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
	harPath := filepath.Join(t.TempDir(), "network.har")
	bodyDir := filepath.Join(t.TempDir(), "bodies")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"network", "capture",
		"--wait", "250ms",
		"--out", outPath,
		"--har-out", harPath,
		"--body-out-dir", bodyDir,
		"--redact", "safe",
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network capture exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK         bool   `json:"ok"`
		OutputMode string `json:"output_mode"`
		Requests   []struct {
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
			Count          int                      `json:"count"`
			Redact         string                   `json:"redact"`
			ArtifactSafety artifacts.SafetyMetadata `json:"artifact_safety"`
		} `json:"capture"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
		HAR struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"har"`
		BodyArtifacts []struct {
			Path      string `json:"path"`
			Bytes     int    `json:"bytes"`
			Truncated bool   `json:"truncated"`
		} `json:"body_artifacts"`
		BodyArtifactCount int `json:"body_artifact_count"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("network capture output is invalid JSON: %v", err)
	}
	if !got.OK || got.OutputMode != "artifact_only" || got.Capture.Count != 2 || len(got.Requests) != 0 || got.Capture.Redact != "safe" || got.Artifact.Path != outPath || got.HAR.Type != "network-har" || got.HAR.Path != harPath || got.BodyArtifactCount != 1 {
		t.Fatalf("network capture manifest = %+v, want safe artifact-only metadata", got)
	}
	if got.Capture.ArtifactSafety.RedactionMode != artifacts.ModeSafe || !got.Capture.ArtifactSafety.Shareable || got.Capture.ArtifactSafety.ChangedFieldCount == 0 {
		t.Fatalf("network capture artifact safety = %+v, want public-safe redaction metadata", got.Capture.ArtifactSafety)
	}
	manifestArtifactPath := got.Artifact.Path
	manifestHARPath := got.HAR.Path
	artifactBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read network capture artifact: %v", err)
	}
	if err := json.Unmarshal(artifactBytes, &got); err != nil {
		t.Fatalf("network capture artifact is invalid JSON: %v", err)
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
	scan := artifacts.ScanBytes(artifactBytes, []string{"Bearer secret", "session=secret", `"token":"secret"`, "csrf=secret"}, 0)
	if len(scan.Findings) != 0 {
		t.Fatalf("network capture artifact leaked synthetic secrets: %+v", scan.Findings)
	}
	harBytes, err := os.ReadFile(harPath)
	if err != nil {
		t.Fatalf("read network HAR artifact: %v", err)
	}
	if manifestArtifactPath != outPath || manifestHARPath != harPath {
		t.Fatalf("artifact manifest paths = %q/%q, want %q/%q", manifestArtifactPath, manifestHARPath, outPath, harPath)
	}
	scan = artifacts.ScanBytes(harBytes, []string{"Bearer secret", "session=secret", `"token":"secret"`, "csrf=secret"}, 0)
	if len(scan.Findings) != 0 {
		t.Fatalf("network HAR artifact leaked synthetic secrets: %+v", scan.Findings)
	}
	bodyBytes, err := os.ReadFile(got.BodyArtifacts[0].Path)
	if err != nil {
		t.Fatalf("read network body artifact: %v", err)
	}
	scan = artifacts.ScanBytes(bodyBytes, []string{"secret", "Bearer", "session="}, 0)
	if len(scan.Findings) != 0 || !strings.Contains(string(bodyBytes), `"ok":true`) {
		t.Fatalf("network body artifact = %q findings=%+v, want safe bounded JSON body", string(bodyBytes), scan.Findings)
	}
	var har struct {
		Log struct {
			Version string `json:"version"`
			Entries []struct {
				Request struct {
					URL     string              `json:"url"`
					Headers []map[string]string `json:"headers"`
				} `json:"request"`
				Response struct {
					Status  int `json:"status"`
					Content struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(harBytes, &har); err != nil {
		t.Fatalf("HAR artifact is invalid JSON: %v", err)
	}
	if har.Log.Version != "1.2" || len(har.Log.Entries) != 2 || har.Log.Entries[0].Response.Status != 200 || !strings.Contains(har.Log.Entries[0].Response.Content.Text, `"ok":true`) {
		t.Fatalf("HAR artifact = %+v, want redacted HAR entries", har)
	}
}

func TestNetworkWebSocketCaptureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "ws.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"network", "websocket",
		"--wait", "1s",
		"--include-payloads",
		"--payload-limit", "12",
		"--redact", "safe",
		"--out", outPath,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network websocket exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK         bool   `json:"ok"`
		OutputMode string `json:"output_mode"`
		WebSockets []struct {
			ID        string `json:"id"`
			URL       string `json:"url"`
			WebSocket struct {
				RequestHeaders  map[string]any `json:"request_headers"`
				ResponseHeaders map[string]any `json:"response_headers"`
				Status          int            `json:"status"`
				Frames          []struct {
					Direction string `json:"direction"`
					Payload   struct {
						Text      string `json:"text"`
						Truncated bool   `json:"truncated"`
					} `json:"payload"`
				} `json:"frames"`
				Errors []struct {
					ErrorMessage string `json:"error_message"`
				} `json:"errors"`
				Closed bool `json:"closed"`
			} `json:"websocket"`
		} `json:"websockets"`
		Capture struct {
			Count           int                      `json:"count"`
			IncludePayloads bool                     `json:"include_payloads"`
			PayloadLimit    int                      `json:"payload_limit"`
			Redact          string                   `json:"redact"`
			ArtifactSafety  artifacts.SafetyMetadata `json:"artifact_safety"`
		} `json:"capture"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("network websocket output is invalid JSON: %v", err)
	}
	if !got.OK || got.OutputMode != "artifact_only" || got.Capture.Count != 1 || !got.Capture.IncludePayloads || got.Capture.PayloadLimit != 12 || got.Capture.Redact != "safe" || len(got.WebSockets) != 0 || got.Artifact.Path != outPath {
		t.Fatalf("network websocket manifest = %+v, want safe artifact-only metadata", got)
	}
	if got.Capture.ArtifactSafety.RedactionMode != artifacts.ModeSafe || !got.Capture.ArtifactSafety.Shareable || got.Capture.ArtifactSafety.ChangedFieldCount == 0 {
		t.Fatalf("network websocket artifact safety = %+v, want public-safe redaction metadata", got.Capture.ArtifactSafety)
	}
	artifactBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read websocket artifact: %v", err)
	}
	if err := json.Unmarshal(artifactBytes, &got); err != nil {
		t.Fatalf("websocket artifact is invalid JSON: %v", err)
	}
	ws := got.WebSockets[0].WebSocket
	if got.WebSockets[0].ID != "ws-1" || ws.Status != 101 || !ws.Closed || len(ws.Frames) != 2 || len(ws.Errors) != 1 {
		t.Fatalf("network websocket record = %+v, want lifecycle, frames, error, and close", got.WebSockets[0])
	}
	if ws.RequestHeaders["Authorization"] != "<redacted>" || ws.ResponseHeaders["Set-Cookie"] != "<redacted>" {
		t.Fatalf("network websocket headers = %+v / %+v, want redacted sensitive headers", ws.RequestHeaders, ws.ResponseHeaders)
	}
	if strings.Contains(ws.Frames[0].Payload.Text, "secret") || !ws.Frames[0].Payload.Truncated {
		t.Fatalf("network websocket payload = %+v, want redacted truncated payload", ws.Frames[0].Payload)
	}
	scan := artifacts.ScanBytes(artifactBytes, []string{"Bearer secret", "session=secret", "secret-frame"}, 0)
	if len(scan.Findings) != 0 {
		t.Fatalf("websocket artifact leaked synthetic secrets: %+v", scan.Findings)
	}
}

func TestNetworkCaptureIncludesWebSocketsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"network", "capture", "--wait", "250ms", "--include-websockets", "--include-websocket-payloads", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network capture websockets exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		Requests []struct {
			ID        string          `json:"id"`
			WebSocket json.RawMessage `json:"websocket"`
		} `json:"requests"`
		Capture struct {
			IncludeWebSockets        bool `json:"include_websockets"`
			IncludeWebSocketPayloads bool `json:"include_websocket_payloads"`
		} `json:"capture"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("network capture websockets output is invalid JSON: %v", err)
	}
	found := false
	for _, request := range got.Requests {
		if request.ID == "ws-1" && len(request.WebSocket) > 0 {
			found = true
		}
	}
	if !got.Capture.IncludeWebSockets || !got.Capture.IncludeWebSocketPayloads || !found {
		t.Fatalf("network capture websockets = %+v, want websocket record included", got)
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

func TestDoctorHeadlessSecurityPendingJSON(t *testing.T) {
	stateDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--check", "headless-security", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor headless-security exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name        string   `json:"name"`
			Status      string   `json:"status"`
			BrowserMode string   `json:"browser_mode"`
			Commands    []string `json:"next_commands"`
			Details     struct {
				ProfileExists  bool     `json:"profile_exists"`
				MetadataExists bool     `json:"metadata_exists"`
				Reasons        []string `json:"reasons"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor headless-security output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Checks) != 1 || got.Checks[0].Name != "headless-security" || got.Checks[0].Status != "pending" || got.Checks[0].BrowserMode != "headless" {
		t.Fatalf("headless security check = %+v, want pending headless check", got.Checks)
	}
	if got.Checks[0].Details.ProfileExists || got.Checks[0].Details.MetadataExists || len(got.Checks[0].Commands) == 0 {
		t.Fatalf("headless security details = %+v commands=%+v, want missing profile with remediation", got.Checks[0].Details, got.Checks[0].Commands)
	}
}

func TestDoctorHeadlessSecurityPassesForManagedStateJSON(t *testing.T) {
	stateDir := t.TempDir()
	metadata, err := browser.PrepareManagedProfile(stateDir, time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PrepareManagedProfile returned error: %v", err)
	}
	metadata.StartedAt = "2026-05-21T12:00:00Z"
	metadata.ChromePID = os.Getpid()
	metadata.DebuggingPort = "9222"
	metadata.OwnedMarker = "owned-token"
	metadata.ProcessStartTime = "2026-05-21T12:00:00Z"
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", daemon.Runtime{PID: os.Getpid(), BrowserMode: "headless", ConnectionMode: "browser_url", UserDataDir: metadata.UserDataDir, SocketPath: daemon.RuntimeSocketPathForMode(stateDir, "headless")}); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--check", "headless-security", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor headless-security exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Status  string `json:"status"`
			Details struct {
				ProfileOwnerOnly       bool   `json:"profile_owner_only"`
				MetadataOwnerOnly      bool   `json:"metadata_owner_only"`
				RuntimeOwnerOnly       bool   `json:"runtime_owner_only"`
				LoopbackEndpoint       bool   `json:"loopback_endpoint"`
				ManagedProfileSelected bool   `json:"managed_profile_selected"`
				ModeMatches            bool   `json:"mode_matches"`
				SeedStrategy           string `json:"seed_strategy"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor headless-security output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Checks) != 1 || got.Checks[0].Status != "pass" {
		t.Fatalf("headless security check = %+v, want pass", got.Checks)
	}
	details := got.Checks[0].Details
	if !details.ProfileOwnerOnly || !details.MetadataOwnerOnly || !details.RuntimeOwnerOnly || !details.LoopbackEndpoint || !details.ManagedProfileSelected || !details.ModeMatches || details.SeedStrategy != "managed" {
		t.Fatalf("headless security details = %+v, want owner-only managed loopback state", details)
	}
	if strings.Contains(out.String(), "owned-token") || strings.Contains(out.String(), "process_start_time") {
		t.Fatalf("headless security output leaked internal ownership metadata: %s", out.String())
	}
}

func TestDoctorHeadlessSecurityPassesForCopyDefaultSeedJSON(t *testing.T) {
	stateDir := t.TempDir()
	metadata, err := browser.PrepareManagedProfile(stateDir, time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PrepareManagedProfile returned error: %v", err)
	}
	metadata.ProfileSeedStrategy = "copy-default"
	metadata.DefaultProfileCopied = true
	metadata.CopiedFileCount = 3
	metadata.StartedAt = "2026-05-21T12:00:00Z"
	metadata.ChromePID = os.Getpid()
	metadata.DebuggingPort = "9222"
	metadata.OwnedMarker = "owned-token"
	metadata.ProcessStartTime = "2026-05-21T12:00:00Z"
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", daemon.Runtime{PID: os.Getpid(), BrowserMode: "headless", ConnectionMode: "browser_url", UserDataDir: metadata.UserDataDir, SocketPath: daemon.RuntimeSocketPathForMode(stateDir, "headless")}); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--check", "headless-security", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor headless-security exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Status  string `json:"status"`
			Details struct {
				SeedStrategy string   `json:"seed_strategy"`
				Reasons      []string `json:"reasons"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor headless-security output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Checks) != 1 || got.Checks[0].Status != "pass" || got.Checks[0].Details.SeedStrategy != "copy-default" || len(got.Checks[0].Details.Reasons) != 0 {
		t.Fatalf("headless security copy-default check = %+v, want pass", got.Checks)
	}
}

func TestDoctorHeadlessSecurityFailsForUnsafeMetadataJSON(t *testing.T) {
	stateDir := t.TempDir()
	metadata, err := browser.PrepareManagedProfile(stateDir, time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PrepareManagedProfile returned error: %v", err)
	}
	metadata.UserDataDir = filepath.Join(stateDir, "default-profile")
	metadata.ProfileSeedStrategy = "unsupported"
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--check", "headless-security", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor headless-security exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Status  string `json:"status"`
			Details struct {
				ManagedProfileSelected bool     `json:"managed_profile_selected"`
				SeedStrategy           string   `json:"seed_strategy"`
				Reasons                []string `json:"reasons"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor headless-security output is invalid JSON: %v", err)
	}
	if got.OK || len(got.Checks) != 1 || got.Checks[0].Status != "fail" || got.Checks[0].Details.ManagedProfileSelected || got.Checks[0].Details.SeedStrategy != "unsupported" || !containsString(got.Checks[0].Details.Reasons, "metadata_user_data_dir_not_managed") {
		t.Fatalf("headless security check = %+v, want fail for unsafe metadata", got.Checks)
	}
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
		OK             bool                `json:"ok"`
		Capabilities   []capabilityTestRow `json:"capabilities"`
		AgentReadiness struct {
			Status        string            `json:"status"`
			Mode          string            `json:"mode"`
			Implemented   int               `json:"implemented"`
			Planned       int               `json:"planned"`
			BootstrapPath bootstrapPathTest `json:"bootstrap_path"`
			NextCommands  []string          `json:"next_commands"`
		} `json:"agent_readiness"`
		BootstrapPath bootstrapPathTest `json:"bootstrap_path"`
		NextCommands  []string          `json:"next_commands"`
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
	if status := capabilityStatus(got.Capabilities, "accessibility"); status != "implemented" {
		t.Fatalf("accessibility capability status = %q, want implemented", status)
	}
	if status := capabilityStatus(got.Capabilities, "performance"); status != "implemented" {
		t.Fatalf("performance capability status = %q, want implemented", status)
	}
	if status := capabilityStatus(got.Capabilities, "memory"); status != "implemented" {
		t.Fatalf("memory capability status = %q, want implemented", status)
	}
	if status := capabilityStatus(got.Capabilities, "emulation"); status != "implemented" {
		t.Fatalf("emulation capability status = %q, want implemented", status)
	}
	if status := capabilityStatus(got.Capabilities, "network_throttling"); status != "" {
		t.Fatalf("network_throttling capability status = %q, want row removed after network emulation shipped", status)
	}
	if commands := capabilityVerifyCommands(got.Capabilities, "raw_protocol"); !containsString(commands, "cdp protocol metadata --json") {
		t.Fatalf("raw_protocol verify_commands = %v, want protocol metadata check", commands)
	}
	if commands := capabilityEvidenceCommands(got.Capabilities, "artifacts"); !containsString(commands, "cdp workflow debug-bundle --out-dir tmp/debug-bundle --json") {
		t.Fatalf("artifacts evidence_commands = %v, want debug-bundle evidence command", commands)
	}
	if commands := capabilityVerifyCommands(got.Capabilities, "accessibility"); !containsString(commands, "cdp a11y tree --json") {
		t.Fatalf("accessibility verify_commands = %v, want a11y tree check", commands)
	}
	if commands := capabilityEvidenceCommands(got.Capabilities, "performance"); !containsString(commands, "cdp workflow perf 'https://example.com' --wait 1s --trace tmp/perf.local.json --json") {
		t.Fatalf("performance evidence_commands = %v, want workflow perf trace evidence", commands)
	}
	if commands := capabilityVerifyCommands(got.Capabilities, "emulation"); !containsString(commands, "cdp emulate user-agent --help") || !containsString(commands, "cdp emulate timezone --help") || !containsString(commands, "cdp emulate locale --help") || !containsString(commands, "cdp emulate color-scheme --help") || !containsString(commands, "cdp permissions grant --help") || !containsString(commands, "cdp permissions set --help") || !containsString(commands, "cdp emulate cpu --help") || !containsString(commands, "cdp emulate network --help") {
		t.Fatalf("emulation verify_commands = %v, want user-agent, timezone, locale, color-scheme, permissions grant/set, CPU, and network help checks", commands)
	}
	if got.AgentReadiness.Status != "ready" || got.AgentReadiness.Mode != "daemon_first_cli" || got.AgentReadiness.Implemented == 0 {
		t.Fatalf("agent_readiness = %+v, want daemon-first readiness summary", got.AgentReadiness)
	}
	if !containsString(got.NextCommands, "cdp doctor --json") || !containsString(got.AgentReadiness.NextCommands, "cdp pages --json") {
		t.Fatalf("next commands = top-level %v readiness %v, want safe bootstrap commands", got.NextCommands, got.AgentReadiness.NextCommands)
	}
	if !containsString(got.BootstrapPath.SetupCommands, "cdp describe --json") || !containsString(got.BootstrapPath.ValidateCommands, "cdp daemon health --json") || !containsString(got.BootstrapPath.RecoverCommands, "cdp daemon logs --tail 50 --json") {
		t.Fatalf("bootstrap_path = %+v, want setup, validate, and recover commands", got.BootstrapPath)
	}
	if !containsString(got.BootstrapPath.StopSignals, "human_required") || !containsString(got.BootstrapPath.StopSignals, "permission_pending") || !containsString(got.BootstrapPath.StopSignals, "unhealthy") {
		t.Fatalf("bootstrap_path stop_signals = %v, want human stop signals", got.BootstrapPath.StopSignals)
	}
	for _, command := range append(append(append([]string{}, got.BootstrapPath.SetupCommands...), got.BootstrapPath.ValidateCommands...), got.BootstrapPath.RecoverCommands...) {
		if strings.Contains(command, "daemon start") || strings.Contains(command, "daemon stop") || strings.Contains(command, "daemon restart") || strings.Contains(command, "keepalive --repair") || strings.Contains(command, "--active-browser-probe") || strings.Contains(command, "--browser-url") {
			t.Fatalf("bootstrap_path command %q mutates daemon lifecycle or probes browser", command)
		}
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
	if !got.OK || got.Schema.Name != "doctor-capabilities" || !schemaHasField(got.Schema.Fields, "capabilities") || !schemaHasField(got.Schema.Fields, "agent_readiness") || !schemaHasField(got.Schema.Fields, "bootstrap_path") || !schemaHasField(got.Schema.Fields, "next_commands") {
		t.Fatalf("schema doctor-capabilities = %+v, want capabilities and bootstrap fields", got)
	}
}

type bootstrapPathTest struct {
	SetupCommands    []string `json:"setup_commands"`
	ValidateCommands []string `json:"validate_commands"`
	RecoverCommands  []string `json:"recover_commands"`
	StopSignals      []string `json:"stop_signals"`
}

type capabilityTestRow struct {
	Name             string   `json:"name"`
	Status           string   `json:"status"`
	VerifyCommands   []string `json:"verify_commands"`
	EvidenceCommands []string `json:"evidence_commands"`
}

func capabilityStatus(capabilities []capabilityTestRow, name string) string {
	for _, capability := range capabilities {
		if capability.Name == name {
			return capability.Status
		}
	}
	return ""
}

func capabilityVerifyCommands(capabilities []capabilityTestRow, name string) []string {
	for _, capability := range capabilities {
		if capability.Name == name {
			return capability.VerifyCommands
		}
	}
	return nil
}

func capabilityEvidenceCommands(capabilities []capabilityTestRow, name string) []string {
	for _, capability := range capabilities {
		if capability.Name == name {
			return capability.EvidenceCommands
		}
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
