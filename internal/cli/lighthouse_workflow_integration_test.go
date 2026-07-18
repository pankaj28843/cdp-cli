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

func TestWorkflowLighthouseUsesDaemonAndWritesSafeSummaries(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	binDir := t.TempDir()
	lighthousePath := filepath.Join(binDir, "lighthouse")
	script := `#!/bin/sh
set -eu
prefix=''
for arg in "$@"; do
  case "$arg" in
    --output-path=*) prefix=${arg#--output-path=} ;;
  esac
done
test -n "$prefix"
printf '%s\n' '{"finalDisplayedUrl":"https://example.test/app?token=lighthouse-secret","categories":{"performance":{"title":"Performance","score":0.91}},"audits":{"unused-javascript":{"id":"unused-javascript","title":"Unused JavaScript","score":0.5,"displayValue":"secret detail","description":"private page detail"}}}' > "${prefix}.report.json"
printf '%s\n' '<html>lighthouse-secret private page body</html>' > "${prefix}.report.html"
`
	if err := os.WriteFile(lighthousePath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	outDir := filepath.Join(t.TempDir(), "reports")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "lighthouse", "https://example.test/app?token=lighthouse-secret", "--out-dir", outDir, "--redact", "safe", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow lighthouse exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		OK         bool `json:"ok"`
		Categories map[string]struct {
			Score float64 `json:"score"`
		} `json:"categories"`
		Artifacts      map[string]string        `json:"artifacts"`
		ArtifactSafety artifacts.SafetyMetadata `json:"artifact_safety"`
		Workflow       struct {
			DaemonBacked bool `json:"daemon_backed"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Categories["performance"].Score != 0.91 || !got.Workflow.DaemonBacked || !got.ArtifactSafety.Shareable || got.ArtifactSafety.RedactionMode != artifacts.ModeSafe {
		t.Fatalf("workflow lighthouse = %+v", got)
	}
	for _, path := range got.Artifacts {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "lighthouse-secret") || strings.Contains(string(content), "private page") {
			t.Fatalf("safe Lighthouse artifact %s leaked fixture: %s", path, string(content))
		}
	}
}
