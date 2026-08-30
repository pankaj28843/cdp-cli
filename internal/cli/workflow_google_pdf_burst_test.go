package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBurstGooglePDFBoundsFailedProcessOutput(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGooglePDFPoppler(t, binDir, `#!/bin/sh
set -eu
i=0
while [ "$i" -lt 200000 ]; do
  printf x >&2
  i=$((i + 1))
done
exit 7
`)
	t.Setenv("PATH", binDir)

	_, err := burstGooglePDF(context.Background(), "synthetic.pdf", filepath.Join(t.TempDir(), "burst"))
	if err == nil {
		t.Fatal("burstGooglePDF succeeded, want bounded process failure")
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "pdf_burst_failed" {
		t.Fatalf("burst error = %v, want pdf_burst_failed", err)
	}
	if !strings.Contains(commandErr.Message, "<truncated>") || len(commandErr.Message) >= 4096 {
		t.Fatalf("burst failure message length=%d, want explicit bounded truncation", len(commandErr.Message))
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok || data["output_truncated"] != true {
		t.Fatalf("burst failure data = %#v, want output_truncated=true", commandErr.Data)
	}
	if data["max_output_bytes_per_stream"] == nil || data["process_termination"] == nil {
		t.Fatalf("burst failure data = %#v, want output and process policy metadata", commandErr.Data)
	}
}

func TestBurstGooglePDFRejectsInvalidPageArtifacts(t *testing.T) {
	tests := []struct {
		name string
		mode string
	}{
		{name: "empty", mode: "empty"},
		{name: "directory", mode: "directory"},
		{name: "symlink", mode: "symlink"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binDir := t.TempDir()
			writeFakeGooglePDFPoppler(t, binDir, `#!/bin/sh
set -eu
prefix=$5
case "$BURST_MODE" in
empty)
  : > "$prefix-1.png"
  ;;
directory)
  /bin/mkdir "$prefix-1.png"
  ;;
symlink)
  printf 'synthetic page' > "$prefix-real.png"
  /bin/ln -s "$prefix-real.png" "$prefix-1.png"
  ;;
esac
`)
			t.Setenv("PATH", binDir)
			t.Setenv("BURST_MODE", test.mode)

			_, err := burstGooglePDF(context.Background(), "synthetic.pdf", filepath.Join(t.TempDir(), "burst"))
			var commandErr *CommandError
			if !errors.As(err, &commandErr) || commandErr.Code != "pdf_burst_invalid_page" {
				t.Fatalf("burst error = %v, want pdf_burst_invalid_page", err)
			}
			data, ok := commandErr.Data.(map[string]any)
			if !ok || data["page_path"] == nil || data["reason"] == nil {
				t.Fatalf("invalid page data = %#v, want path and reason metadata", commandErr.Data)
			}
			if strings.Contains(commandErr.Message, "synthetic page") {
				t.Fatalf("invalid page error exposed page contents: %q", commandErr.Message)
			}
		})
	}
}

func TestBurstGooglePDFReturnsOrderedRegularNonEmptyPages(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGooglePDFPoppler(t, binDir, `#!/bin/sh
set -eu
prefix=$5
printf 'second synthetic page' > "$prefix-2.png"
printf 'first synthetic page' > "$prefix-1.png"
`)
	t.Setenv("PATH", binDir)

	paths, err := burstGooglePDF(context.Background(), "synthetic.pdf", filepath.Join(t.TempDir(), "burst"))
	if err != nil {
		t.Fatalf("burstGooglePDF error = %v", err)
	}
	if len(paths) != 2 || filepath.Base(paths[0]) != "page-1.png" || filepath.Base(paths[1]) != "page-2.png" {
		t.Fatalf("burst paths = %v, want numeric page order", paths)
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("stat burst page %q: %v", path, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("burst page %q info=%v, want regular non-empty file", path, info)
		}
	}
}

func writeFakeGooglePDFPoppler(t *testing.T, binDir, script string) {
	t.Helper()
	path := filepath.Join(binDir, "pdftoppm")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake pdftoppm: %v", err)
	}
}
