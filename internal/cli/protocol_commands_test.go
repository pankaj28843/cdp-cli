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
	if !got.OK || got.Protocol.DomainCount != 3 || got.Protocol.Domains[0].Name != "Page" || got.Protocol.Domains[0].CommandCount != 2 {
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
	if !got.OK || got.DomainCount != 3 || got.Domains[2].Name != "Runtime" || got.Domains[2].EventCount != 1 {
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
			Command        string         `json:"command"`
			Scope          string         `json:"scope"`
			Params         string         `json:"params"`
			RequiredParams []string       `json:"required_params"`
			OptionalParams []string       `json:"optional_params"`
			ParamsSample   map[string]any `json:"params_sample"`
			ScopeNote      string         `json:"scope_note"`
			Notes          []string       `json:"notes"`
		} `json:"examples"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol examples output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Examples) == 0 || got.Examples[0].Scope != "target" || !strings.Contains(got.Examples[0].Command, "Page.captureScreenshot") || got.Examples[0].Params == "" {
		t.Fatalf("protocol examples = %+v, want target-scoped example", got)
	}
	if len(got.Examples[0].RequiredParams) != 0 || !containsString(got.Examples[0].OptionalParams, "format") || got.Examples[0].ScopeNote == "" || len(got.Examples[0].Notes) == 0 {
		t.Fatalf("protocol examples metadata = %+v, want optional param and scope notes", got.Examples[0])
	}
}

func TestProtocolExamplesBrowserScopedJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"protocol", "examples", "Browser.getVersion", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol examples browser exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Examples []struct {
			Command        string   `json:"command"`
			Scope          string   `json:"scope"`
			RequiredParams []string `json:"required_params"`
			ScopeNote      string   `json:"scope_note"`
		} `json:"examples"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol examples browser output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.Examples) == 0 || got.Examples[0].Scope != "browser" || strings.Contains(got.Examples[0].Command, "--target") || len(got.Examples[0].RequiredParams) != 0 || !strings.Contains(got.Examples[0].ScopeNote, "Browser-scoped") {
		t.Fatalf("protocol examples browser = %+v, want browser-scoped no-target example", got)
	}
}

func TestProtocolExecValidateEnforcesLiveSchemaBeforeExecution(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tests := []struct {
		name     string
		args     []string
		wantExit int
		wantCode string
		wantText string
	}{
		{name: "unknown parameter", args: []string{"Page.captureScreenshot", "--target", "page-1", "--params", `{"unexpected":true}`, "--validate"}, wantExit: cli.ExitUsage, wantCode: "cdp_invalid_params", wantText: "unexpected"},
		{name: "missing required parameter", args: []string{"Page.navigate", "--target", "page-1", "--params", `{}`, "--validate"}, wantExit: cli.ExitUsage, wantCode: "cdp_invalid_params", wantText: "url"},
		{name: "target command in browser scope", args: []string{"Page.captureScreenshot", "--params", `{}`, "--validate"}, wantExit: cli.ExitUsage, wantCode: "cdp_invalid_scope", wantText: "target-scoped"},
		{name: "browser command in target scope", args: []string{"Browser.getVersion", "--target", "page-1", "--params", `{}`, "--validate"}, wantExit: cli.ExitUsage, wantCode: "cdp_invalid_scope", wantText: "browser-scoped"},
		{name: "unknown command", args: []string{"Page.notReal", "--target", "page-1", "--params", `{}`, "--validate"}, wantExit: cli.ExitUsage, wantCode: "unknown_protocol_entity", wantText: "Page.notReal"},
		{name: "valid target command", args: []string{"Page.captureScreenshot", "--target", "page-1", "--params", `{"format":"png"}`, "--validate"}, wantExit: cli.ExitOK},
		{name: "valid browser command", args: []string{"Browser.getVersion", "--params", `{}`, "--validate"}, wantExit: cli.ExitOK},
		{name: "raw mode stays permissive", args: []string{"Page.captureScreenshot", "--target", "page-1", "--params", `{"unexpected":true}`}, wantExit: cli.ExitOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			args := append([]string{"protocol", "exec"}, test.args...)
			args = append(args, "--json")
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != test.wantExit {
				t.Fatalf("validated protocol exec exit=%d, want %d; stdout=%s stderr=%s", code, test.wantExit, out.String(), errOut.String())
			}
			var got struct {
				OK      bool   `json:"ok"`
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("validated protocol exec output is invalid JSON: %v; output=%s", err, out.String())
			}
			if test.wantExit == cli.ExitOK {
				if !got.OK {
					t.Fatalf("validated protocol exec = %+v, want success", got)
				}
				return
			}
			if got.OK || got.Code != test.wantCode || !strings.Contains(got.Message, test.wantText) {
				t.Fatalf("validated protocol exec = %+v, want %s containing %q", got, test.wantCode, test.wantText)
			}
		})
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

func TestProtocolExecClassifiesChromeRejection(t *testing.T) {
	tests := []struct {
		name   string
		method string
		args   []string
		target string
	}{
		{name: "browser", method: "Browser.notReal"},
		{name: "target", method: "Runtime.notReal", args: []string{"--target", "page-1"}, target: "page-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
			})
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			args := append([]string{"protocol", "exec", test.method}, test.args...)
			args = append(args, "--params", "{}", "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitCheckFailed {
				t.Fatalf("protocol exec exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
			}

			var got struct {
				OK                  bool     `json:"ok"`
				Code                string   `json:"code"`
				Class               string   `json:"err_class"`
				RemediationCommands []string `json:"remediation_commands"`
				Data                struct {
					Scope           string `json:"scope"`
					TargetID        string `json:"target_id"`
					Method          string `json:"method"`
					ProtocolCode    int    `json:"protocol_code"`
					ProtocolMessage string `json:"protocol_message"`
				} `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("decode protocol rejection: %v; output=%s", err, out.String())
			}
			if got.OK || got.Code != "cdp_command_failed" || got.Class != "protocol" {
				t.Fatalf("protocol rejection envelope = %+v", got)
			}
			if got.Data.Method != test.method || got.Data.ProtocolCode != -32601 || got.Data.ProtocolMessage != "method not found" || got.Data.Scope != test.name || got.Data.TargetID != test.target {
				t.Fatalf("protocol rejection data = %+v, want method/code/message/scope/target", got.Data)
			}
			for _, remediation := range got.RemediationCommands {
				if strings.Contains(remediation, "doctor") || strings.Contains(remediation, "daemon") {
					t.Fatalf("protocol rejection remediation %q treats Chrome rejection as connectivity failure", remediation)
				}
			}
		})
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
	if !got.OK || got.Scope != "target" || got.Method != "Runtime.evaluate" || got.Target.ID != "page-1" || got.SessionID != "session-page-1" || got.Result.Result.Value != "Example App" {
		t.Fatalf("protocol exec target = %+v, want target-scoped Runtime.evaluate", got)
	}
}

func TestProtocolExecServiceWorkerTargetScopedJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
		{"targetId": "worker-1", "type": "service_worker", "title": "Service Worker", "url": "chrome-extension://example/background.js", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"protocol", "exec", "Runtime.evaluate",
		"--target", "worker",
		"--target-type", "service_worker",
		"--params", `{"expression":"Object.keys(globalThis).slice(0,3)","returnByValue":true}`,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("protocol exec service worker exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Scope  string `json:"scope"`
		Target struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"target"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("protocol exec service worker output is invalid JSON: %v", err)
	}
	if !got.OK || got.Scope != "target" || got.Target.ID != "worker-1" || got.Target.Type != "service_worker" || got.Target.URL != "chrome-extension://example/background.js" || got.SessionID != "session-worker-1" {
		t.Fatalf("protocol exec service worker = %+v, want service_worker target evidence", got)
	}
}

func TestProtocolExecTypedTargetAmbiguityUsesRequestedType(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "worker-a", "type": "worker", "title": "Worker A", "url": "https://example.test/worker-a", "attached": false},
		{"targetId": "worker-b", "type": "worker", "title": "Worker B", "url": "https://example.test/worker-b", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"protocol", "exec", "Runtime.evaluate",
		"--target-type", "worker",
		"--params", `{"expression":"1","returnByValue":true}`,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("ambiguous typed target exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("ambiguous typed target output is invalid JSON: %v; output=%s", err, out.String())
	}
	if got.OK || got.Code != "ambiguous_target" {
		t.Fatalf("ambiguous typed target = %+v, want ambiguous_target error", got)
	}
	remediation := strings.Join(got.RemediationCommands, "\n")
	if !strings.Contains(remediation, "--target-type worker") || strings.Contains(remediation, "service_worker") {
		t.Fatalf("ambiguous typed target remediation = %q, want requested worker type only", remediation)
	}
}

func TestProtocolExecTypedTargetNotFoundSuggestsTargets(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example", "url": "https://example.test", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"protocol", "exec", "Runtime.evaluate",
		"--target-type", "service_worker",
		"--url-contains", "chrome-extension://",
		"--params", `{"expression":"1","returnByValue":true}`,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("missing typed target exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("missing typed target output is invalid JSON: %v; output=%s", err, out.String())
	}
	if got.OK || got.Code != "target_not_found" {
		t.Fatalf("missing typed target = %+v, want target_not_found error", got)
	}
	if len(got.RemediationCommands) == 0 || got.RemediationCommands[0] != "cdp targets --json" {
		t.Fatalf("missing typed target remediation = %v, want cdp targets discovery", got.RemediationCommands)
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
