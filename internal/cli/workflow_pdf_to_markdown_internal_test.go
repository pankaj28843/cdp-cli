package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPDFTextCoverageRejectsLoneGlyphsAndAcceptsMeaningfulPage(t *testing.T) {
	thin := assessPDFTextCoverage([]string{"1", "2", "CONFIDENTIAL", ""})
	if thin.Usable || thin.MeaningfulPageCount != 0 {
		t.Fatalf("thin text-layer coverage = %+v, want unusable with no meaningful pages", thin)
	}

	meaningful := assessPDFTextCoverage([]string{"", "Production AI systems need careful evaluation and operational monitoring.", ""})
	if !meaningful.Usable || meaningful.MeaningfulPageCount != 1 {
		t.Fatalf("meaningful text-layer coverage = %+v, want one meaningful page", meaningful)
	}
}

func TestRunPDFTextExtractionBoundsOutputWithStableError(t *testing.T) {
	toolPath := filepath.Join(t.TempDir(), "pdftotext")
	if err := os.WriteFile(toolPath, []byte(`#!/bin/sh
set -eu
i=0
while [ "$i" -lt 20 ]; do
  printf '0123456789'
  i=$((i + 1))
done
`), 0o700); err != nil {
		t.Fatalf("write fake pdftotext: %v", err)
	}

	_, err := runPDFTextExtraction(context.Background(), toolPath, "/tmp/source-snapshot.pdf", "/tmp/source.pdf", 32)
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("runPDFTextExtraction error = %v, want CommandError", err)
	}
	if commandErr.Code != "pdf_text_output_too_large" || commandErr.Class != "extraction" || commandErr.ExitCode != ExitCheckFailed {
		t.Fatalf("bounded extraction error = %+v", commandErr)
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok || data["max_output_bytes"] != int64(32) {
		t.Fatalf("bounded extraction error data = %#v, want max_output_bytes=32", commandErr.Data)
	}
}
