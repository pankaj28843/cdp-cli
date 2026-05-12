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
	if !got.OK || got.Scope != "target" || got.Method != "Runtime.evaluate" || got.Target.ID != "page-1" || got.SessionID != "session-page-1" || got.Result.Result.Value != "Example App" {
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
