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

func TestGuideCommandPrintsBundledContent(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"guide"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("guide exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	if !strings.Contains(out.String(), "# cdp-cli Agent Guide") || !strings.Contains(out.String(), "cdp --browser-mode") {
		t.Fatalf("guide output = %q, want the bundled public agent guide", out.String())
	}
}

func TestGuideCommandJSONContainsContentContract(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"guide", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("guide JSON exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK            bool   `json:"ok"`
		SchemaVersion string `json:"schema_version"`
		Mode          string `json:"mode"`
		Bytes         int    `json:"bytes"`
		Content       string `json:"content"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("guide JSON is invalid: %v; output=%s", err, out.String())
	}
	if !got.OK || got.SchemaVersion != "guide/v1" || got.Mode != "content" || got.Bytes != len(got.Content) || !strings.Contains(got.Content, "sense") || !strings.Contains(got.Content, "cdp events wait") || !strings.Contains(got.Content, "from-offset") {
		t.Fatalf("guide JSON = %+v, want versioned content contract", got)
	}
}

func TestGuideCommandPathIsReadable(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"guide", "--path", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("guide path JSON exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		Mode   string `json:"mode"`
		Path   string `json:"path"`
		Bytes  int    `json:"bytes"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("guide path JSON is invalid: %v; output=%s", err, out.String())
	}
	if !got.OK || got.Mode != "path" || got.Path == "" || got.Source == "" {
		t.Fatalf("guide path JSON = %+v, want a useful readable path", got)
	}
	content, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read guide path %q: %v", got.Path, err)
	}
	if got.Bytes != len(content) || !strings.Contains(string(content), "# cdp-cli Agent Guide") {
		t.Fatalf("guide path content bytes=%d, reported=%d; content=%q", len(content), got.Bytes, string(content))
	}
	if strings.HasPrefix(filepath.Base(got.Path), "cdp-cli-guide-") {
		t.Cleanup(func() { _ = os.Remove(got.Path) })
	}
}

func TestGuideCommandIsDiscoverableAndRejectsUnknownFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK || !strings.Contains(out.String(), `"name": "guide"`) {
		t.Fatalf("describe guide command code=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"guide", "--unknown", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code == cli.ExitOK {
		t.Fatalf("unknown guide flag unexpectedly succeeded: stdout=%s", out.String())
	}
}
