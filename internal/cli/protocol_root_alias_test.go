package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestBareProtocolMethodAliasUsesDaemonBackedExec(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "root-alias-page", "type": "page", "title": "Root alias", "url": "https://example.test/root-alias", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"--target", "root-alias-page", "--json",
		"Runtime.evaluate", `{"expression":"document.title","returnByValue":true}`,
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("bare protocol alias exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Method string `json:"method"`
		Result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("bare protocol alias output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || got.Method != "Runtime.evaluate" || got.Result.Result.Value != "Example App" {
		t.Fatalf("bare protocol alias = %+v, want forwarded Runtime.evaluate params", got)
	}
}

func TestBareProtocolMethodAliasReusesLocalShapeGuard(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"Runtime.evaluate", "[]", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("bare protocol invalid shape exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var failure struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &failure); err != nil {
		t.Fatalf("bare protocol failure output is invalid JSON: %v; output=%s", err, out.String())
	}
	if failure.Code != "invalid_json" || !strings.Contains(failure.Message, "JSON object") {
		t.Fatalf("bare protocol failure = %+v, want invalid_json object-shape error", failure)
	}
}
