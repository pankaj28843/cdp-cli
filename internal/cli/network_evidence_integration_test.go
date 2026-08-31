package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
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

func TestNetworkCaptureOutReturnsPrivacySafeManifest(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "network.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"network", "capture", "--wait", "10ms", "--out", outPath,
		"--include-websockets", "--include-websocket-payloads", "--redact", "none", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network capture exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	for _, sensitive := range []string{"Bearer secret", "session=secret", `"csrf":"secret"`, "token=abc", `"auth":"secret"`} {
		if strings.Contains(out.String(), sensitive) {
			t.Fatalf("network capture manifest leaked synthetic payload: %s", out.String())
		}
	}
	for _, sensitive := range []string{"Bearer secret", "session=secret", `"csrf":"secret"`, "token=abc", `"auth":"secret"`} {
		if strings.Contains(errOut.String(), sensitive) {
			t.Fatalf("network capture stderr leaked synthetic payload: %s", errOut.String())
		}
	}
	var manifest struct {
		OK       bool           `json:"ok"`
		Requests []any          `json:"requests"`
		Capture  map[string]any `json:"capture"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatalf("decode manifest: %v; output=%s", err, out.String())
	}
	if !manifest.OK || len(manifest.Requests) != 0 || manifest.Artifact.Path != outPath {
		t.Fatalf("manifest = %+v, want successful artifact-only response", manifest)
	}
	if manifest.Capture["output_mode"] != "artifact_only" {
		t.Fatalf("capture output_mode = %v, want artifact_only", manifest.Capture["output_mode"])
	}
	artifact, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read capture artifact: %v", err)
	}
	if !strings.Contains(string(artifact), "Bearer secret") || !strings.Contains(string(artifact), `\"csrf\":\"secret\"`) || !strings.Contains(string(artifact), "token=abc") || !strings.Contains(string(artifact), `\"auth\":\"secret\"`) {
		t.Fatalf("capture artifact lost synthetic payload: %s", artifact)
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

func TestNetworkWebSocketOutReturnsPrivacySafeManifest(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "websocket.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"network", "websocket", "--wait", "10ms", "--out", outPath,
		"--include-payloads", "--redact", "none", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network websocket exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	for _, sensitive := range []string{"Bearer secret", "session=secret", "token=abc", `"auth":"secret"`} {
		if strings.Contains(out.String(), sensitive) || strings.Contains(errOut.String(), sensitive) {
			t.Fatalf("websocket manifest leaked synthetic payload %q: stdout=%s stderr=%s", sensitive, out.String(), errOut.String())
		}
	}
	var manifest struct {
		OK         bool           `json:"ok"`
		WebSockets []any          `json:"websockets"`
		Capture    map[string]any `json:"capture"`
		Artifact   struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatalf("decode WebSocket manifest: %v; output=%s", err, out.String())
	}
	if !manifest.OK || len(manifest.WebSockets) != 0 || manifest.Artifact.Path != outPath || manifest.Capture["output_mode"] != "artifact_only" {
		t.Fatalf("WebSocket manifest = %+v, want successful artifact-only response", manifest)
	}
	artifact, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read WebSocket artifact: %v", err)
	}
	if !strings.Contains(string(artifact), "Bearer secret") || !strings.Contains(string(artifact), "token=abc") || !strings.Contains(string(artifact), `\"auth\":\"secret\"`) {
		t.Fatalf("WebSocket artifact lost synthetic payload: %s", artifact)
	}
}

func TestNetworkCaptureWithoutOutKeepsRecordsInline(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"network", "capture", "--wait", "10ms", "--include-websockets",
		"--include-websocket-payloads", "--redact", "safe", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("network capture exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var report map[string]any
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode inline capture: %v; output=%s", err, out.String())
	}
	requests, requestsOK := report["requests"].([]any)
	capture, captureOK := report["capture"].(map[string]any)
	if !requestsOK || !captureOK || report["output_mode"] != "inline" || len(requests) != 3 || capture["request_count"] != float64(2) || capture["websocket_count"] != float64(1) || capture["frame_count"] != float64(2) {
		t.Fatalf("inline report = %+v, want records and truthful counts", report)
	}
	if strings.Contains(errOut.String(), "Bearer secret") || strings.Contains(errOut.String(), "\"auth\":\"secret\"") {
		t.Fatalf("inline capture stderr leaked synthetic payload: %s", errOut.String())
	}
}
