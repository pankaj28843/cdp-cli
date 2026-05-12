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

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

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
		"--wait", "250ms",
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
		OK         bool `json:"ok"`
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
			Count           int    `json:"count"`
			IncludePayloads bool   `json:"include_payloads"`
			PayloadLimit    int    `json:"payload_limit"`
			Redact          string `json:"redact"`
		} `json:"capture"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("network websocket output is invalid JSON: %v", err)
	}
	if !got.OK || got.Capture.Count != 1 || !got.Capture.IncludePayloads || got.Capture.PayloadLimit != 12 || got.Capture.Redact != "safe" || got.Artifact.Path != outPath {
		t.Fatalf("network websocket = %+v, want one safe-redacted websocket artifact", got)
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
