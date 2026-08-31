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
		Artifacts    map[string]string `json:"artifacts"`
		ArtifactList []struct {
			Path  string `json:"path"`
			Bytes int64  `json:"bytes"`
		} `json:"artifact_list"`
		ArtifactSafety artifacts.SafetyMetadata `json:"artifact_safety"`
		Workflow       struct {
			DaemonBacked bool `json:"daemon_backed"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Categories["performance"].Score != 0.91 || len(got.ArtifactList) != 2 || got.ArtifactList[0].Bytes <= 0 || got.ArtifactList[1].Bytes <= 0 || !got.Workflow.DaemonBacked || !got.ArtifactSafety.Shareable || got.ArtifactSafety.RedactionMode != artifacts.ModeSafe {
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

func TestWorkflowLighthouseRequiresPairedReportArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		htmlSetup string
	}{
		{name: "missing", htmlSetup: ""},
		{name: "empty", htmlSetup: `: > "${prefix}.report.html"`},
		{name: "non_regular", htmlSetup: `mkdir "${prefix}.report.html"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
    --output-path=*) prefix="$(printf '%s' "$arg" | sed 's/^--output-path=//')" ;;
  esac
done
test -n "$prefix"
printf '%s\n' '{"categories":{"performance":{"title":"Performance","score":0.91}},"audits":{}}' > "${prefix}.report.json"
` + test.htmlSetup + "\n"
			if err := os.WriteFile(lighthousePath, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			outDir := filepath.Join(t.TempDir(), "reports")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"workflow", "lighthouse", "https://example.test", "--out-dir", outDir, "--redact", "none", "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitInternal {
				t.Fatalf("workflow lighthouse exit=%d stdout=%s stderr=%s, want artifact failure", code, out.String(), errOut.String())
			}
			var envelope struct {
				OK   bool           `json:"ok"`
				Code string         `json:"code"`
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatalf("decode error envelope: %v; stdout=%s", err, out.String())
			}
			if envelope.OK || envelope.Code != "artifact_missing" {
				t.Fatalf("error envelope=%+v, want artifact_missing", envelope)
			}
			if got := envelope.Data["artifact_kind"]; got != "lighthouse-html" {
				t.Fatalf("artifact data=%+v, want lighthouse-html", envelope.Data)
			}
		})
	}
}

func TestWorkflowLighthouseBoundsFailedProcessOutput(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	binDir := t.TempDir()
	lighthousePath := filepath.Join(binDir, "lighthouse")
	script := `#!/bin/sh
set -eu
i=0
while [ "$i" -lt 200000 ]; do
  printf 'lighthouse-output-secret-'
  i=$((i + 1))
done >&2
exit 7
`
	if err := os.WriteFile(lighthousePath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "lighthouse", "https://example.test", "--redact", "none", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("workflow lighthouse exit=%d stdout=%s stderr=%s, want check failure", code, out.String(), errOut.String())
	}
	var envelope struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v; stdout=%s", err, out.String())
	}
	if envelope.Code != "lighthouse_failed" || !strings.Contains(envelope.Message, "<truncated>") {
		t.Fatalf("error envelope=%+v, want bounded truncated failure", envelope)
	}
	if truncated, ok := envelope.Data["output_truncated"].(bool); !ok || !truncated {
		t.Fatalf("failure data=%+v, want output_truncated=true", envelope.Data)
	}
}
