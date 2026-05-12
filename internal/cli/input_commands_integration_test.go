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
			Strategy string `json:"strategy"`
		} `json:"click"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "clicked" || got.Target.ID != "page-1" || got.Click.Selector != "main" || got.Click.Count != 1 || !got.Click.Clicked || got.Click.Strategy != "dom" {
		t.Fatalf("click output = %+v, want DOM clicked main", got)
	}
}

func TestClickRawInputVerifiedJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "main", "--strategy", "raw-input", "--activate", "--wait-text", "Ready", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("raw click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Click struct {
			Clicked  bool    `json:"clicked"`
			Strategy string  `json:"strategy"`
			X        float64 `json:"x"`
			Y        float64 `json:"y"`
			Verified *bool   `json:"verified"`
		} `json:"click"`
		Verification struct {
			Matched bool `json:"matched"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("raw click output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Click.Clicked || got.Click.Strategy != "raw-input" || got.Click.X != 310 || got.Click.Y != 120 || got.Click.Verified == nil || !*got.Click.Verified || !got.Verification.Matched {
		t.Fatalf("raw click = %+v, want verified raw-input click", got)
	}
}

func TestClickVerificationTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--timeout", "100ms", "click", "main", "--strategy", "raw-input", "--wait-text", "Never Ready", "--poll", "5ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("unverified click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Click struct {
			Clicked  bool  `json:"clicked"`
			Verified *bool `json:"verified"`
		} `json:"click"`
		Verification struct {
			Matched bool `json:"matched"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unverified click output is invalid JSON: %v", err)
	}
	if got.OK || !got.Click.Clicked || got.Click.Verified == nil || *got.Click.Verified || got.Verification.Matched {
		t.Fatalf("unverified click = %+v, want clicked but not verified", got)
	}
}

func TestClickRawInputZeroRectJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "zero", "--strategy", "raw-input", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("zero rect click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "zero width or height") {
		t.Fatalf("zero rect stdout = %s, want zero rect error", out.String())
	}
}

func TestClickDiagnosticsArtifactJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "click.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "main", "--strategy", "raw-input", "--wait-selector", "main", "--diagnostics-out", outPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("diagnostic click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
		Diagnostics struct {
			Selector string `json:"selector"`
			Strategy string `json:"strategy"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("diagnostic click output is invalid JSON: %v", err)
	}
	if !got.OK || got.Artifact.Path != outPath || got.Diagnostics.Selector != "main" || got.Diagnostics.Strategy != "raw-input" {
		t.Fatalf("diagnostic click = %+v, want artifact metadata", got)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("click diagnostics artifact was not written: %v", err)
	}
}

func TestTypeContentEditableUsesInsertTextJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"type", "[contenteditable=true]", "hello rich editor", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("type contenteditable exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Type struct {
			Selector string `json:"selector"`
			Typing   bool   `json:"typing"`
			Typed    string `json:"typed"`
			Value    string `json:"value"`
			Kind     string `json:"kind"`
			Strategy string `json:"strategy"`
		} `json:"type"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("type contenteditable output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Type.Typing || got.Type.Strategy != "insert-text" || got.Type.Kind != "contenteditable" || got.Type.Value != "beforehello rich editor" {
		t.Fatalf("type contenteditable = %+v, want insert-text strategy and resulting text", got)
	}
}

func TestInsertTextCommandJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"insert-text", "[contenteditable=true]", " inserted", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("insert-text exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK         bool `json:"ok"`
		InsertText struct {
			Typing   bool   `json:"typing"`
			Value    string `json:"value"`
			Strategy string `json:"strategy"`
		} `json:"insert_text"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("insert-text output is invalid JSON: %v", err)
	}
	if !got.OK || !got.InsertText.Typing || got.InsertText.Strategy != "insert-text" || got.InsertText.Value != "before inserted" {
		t.Fatalf("insert-text = %+v, want inserted rich text", got)
	}
}
