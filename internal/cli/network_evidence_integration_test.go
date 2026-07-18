package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestNetworkHAROnlyMarksUnsafeOptIn(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	harPath := filepath.Join(t.TempDir(), "network.har")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"network", "capture", "--wait", "10ms", "--har-out", harPath, "--redact", "none", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network capture exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Capture struct {
			ArtifactSafety artifacts.SafetyMetadata `json:"artifact_safety"`
		} `json:"capture"`
		HAR struct {
			Path   string                   `json:"path"`
			Safety artifacts.SafetyMetadata `json:"safety"`
		} `json:"har"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.HAR.Path != harPath || !got.HAR.Safety.UnsafeOptIn || got.HAR.Safety.Classification != "unsafe_opt_in" || !got.Capture.ArtifactSafety.UnsafeOptIn {
		t.Fatalf("HAR safety = har=%+v capture=%+v", got.HAR.Safety, got.Capture.ArtifactSafety)
	}
}

func TestNetworkWebSocketSafeRedactsBeforeTruncation(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"network", "websocket", "--wait", "50ms", "--include-payloads", "--payload-limit", "262144", "--redact", "safe", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network websocket exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if strings.Contains(out.String(), `"auth":"secret"`) || strings.Contains(out.String(), "wss://example.test/socket?token=abc") {
		t.Fatalf("safe WebSocket output leaked synthetic payload/query secret: %s", out.String())
	}
	if !strings.Contains(out.String(), "redacted") {
		t.Fatalf("safe WebSocket output did not report redaction: %s", out.String())
	}
}
