package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowPDFToMarkdownWritesDeterministicPageArtifacts(t *testing.T) {
	binDir := t.TempDir()
	writeFakePDFToText(t, binDir, `#!/bin/sh
set -eu
printf 'First page  \nA line\r\n\fSecond page\n- bulletish\n\f'
`)
	t.Setenv("PATH", binDir)

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "synthetic fixture.pdf")
	inputBytes := []byte("%PDF-1.7\nsynthetic text-layer fixture\n")
	if err := os.WriteFile(inputPath, inputBytes, 0o600); err != nil {
		t.Fatalf("write PDF fixture: %v", err)
	}
	outDir := filepath.Join(t.TempDir(), "pdf-markdown")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "pdf-to-markdown", inputPath,
		"--out-dir", outDir,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow pdf-to-markdown exit = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var got struct {
		OK            bool   `json:"ok"`
		SchemaVersion string `json:"schema_version"`
		Source        struct {
			Path      string `json:"path"`
			SHA256    string `json:"sha256"`
			ByteCount int64  `json:"byte_count"`
		} `json:"source"`
		Extraction struct {
			Engine         string `json:"engine"`
			Representation string `json:"representation"`
			OCRUsed        bool   `json:"ocr_used"`
		} `json:"extraction"`
		Pages []struct {
			PageNumber      int    `json:"page_number"`
			MarkdownHeading string `json:"markdown_heading"`
			CharacterCount  int    `json:"character_count"`
			WordCount       int    `json:"word_count"`
			LineCount       int    `json:"line_count"`
		} `json:"pages"`
		Stats struct {
			PageCount      int `json:"page_count"`
			PagesWithText  int `json:"pages_with_text"`
			CharacterCount int `json:"character_count"`
			WordCount      int `json:"word_count"`
			LineCount      int `json:"line_count"`
		} `json:"stats"`
		Artifacts struct {
			Markdown       string `json:"markdown"`
			Metadata       string `json:"metadata"`
			MarkdownSHA256 string `json:"markdown_sha256"`
			MetadataSHA256 string `json:"metadata_sha256"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	inputSum := sha256.Sum256(inputBytes)
	if !got.OK || got.SchemaVersion != "pdf-to-markdown/v1" ||
		got.Source.SHA256 != hex.EncodeToString(inputSum[:]) || got.Source.ByteCount != int64(len(inputBytes)) {
		t.Fatalf("workflow source identity = %+v", got)
	}
	if got.Source.Path != inputPath {
		t.Fatalf("source path = %q, want %q", got.Source.Path, inputPath)
	}
	if got.Extraction.Engine != "poppler-pdftotext" ||
		got.Extraction.Representation != "embedded-text-layer" || got.Extraction.OCRUsed {
		t.Fatalf("workflow extraction = %+v, want text-layer-only pdftotext", got.Extraction)
	}
	if got.Stats.PageCount != 2 || got.Stats.PagesWithText != 2 ||
		got.Stats.CharacterCount != 40 || got.Stats.WordCount != 8 || got.Stats.LineCount != 4 {
		t.Fatalf("workflow stats = %+v", got.Stats)
	}
	if len(got.Pages) != 2 ||
		got.Pages[0].PageNumber != 1 || got.Pages[0].MarkdownHeading != "Page 1" ||
		got.Pages[0].CharacterCount != 17 || got.Pages[0].WordCount != 4 || got.Pages[0].LineCount != 2 ||
		got.Pages[1].PageNumber != 2 || got.Pages[1].MarkdownHeading != "Page 2" ||
		got.Pages[1].CharacterCount != 23 || got.Pages[1].WordCount != 4 || got.Pages[1].LineCount != 2 {
		t.Fatalf("workflow pages = %+v", got.Pages)
	}

	wantMarkdown := "<!-- cdp:pdf-text-layer-only; ocr=false -->\n\n" +
		"## Page 1\n\nFirst page\nA line\n\n" +
		"## Page 2\n\nSecond page\n- bulletish\n"
	markdown, err := os.ReadFile(got.Artifacts.Markdown)
	if err != nil {
		t.Fatalf("read Markdown artifact: %v", err)
	}
	if string(markdown) != wantMarkdown {
		t.Fatalf("Markdown artifact:\n%s\nwant:\n%s", markdown, wantMarkdown)
	}
	markdownSum := sha256.Sum256(markdown)
	if got.Artifacts.MarkdownSHA256 != hex.EncodeToString(markdownSum[:]) {
		t.Fatalf("Markdown SHA-256 = %q, want %x", got.Artifacts.MarkdownSHA256, markdownSum)
	}
	metadata, err := os.ReadFile(got.Artifacts.Metadata)
	if err != nil {
		t.Fatalf("read metadata artifact: %v", err)
	}
	metadataSum := sha256.Sum256(metadata)
	if got.Artifacts.MetadataSHA256 != hex.EncodeToString(metadataSum[:]) {
		t.Fatalf("metadata SHA-256 = %q, want %x", got.Artifacts.MetadataSHA256, metadataSum)
	}
	var metadataContract struct {
		SchemaVersion string `json:"schema_version"`
		Source        struct {
			SHA256 string `json:"sha256"`
		} `json:"source"`
		Extraction struct {
			OCRUsed bool `json:"ocr_used"`
		} `json:"extraction"`
		Pages []struct {
			PageNumber int `json:"page_number"`
		} `json:"pages"`
		Stats struct {
			PageCount int `json:"page_count"`
		} `json:"stats"`
		MarkdownSHA256 string `json:"markdown_sha256"`
	}
	if err := json.Unmarshal(metadata, &metadataContract); err != nil {
		t.Fatalf("decode metadata artifact: %v", err)
	}
	if metadataContract.SchemaVersion != "pdf-to-markdown/v1" ||
		metadataContract.Source.SHA256 != got.Source.SHA256 ||
		metadataContract.Extraction.OCRUsed ||
		len(metadataContract.Pages) != 2 || metadataContract.Pages[1].PageNumber != 2 ||
		metadataContract.Stats.PageCount != 2 ||
		metadataContract.MarkdownSHA256 != got.Artifacts.MarkdownSHA256 {
		t.Fatalf("metadata contract = %+v", metadataContract)
	}
	for _, path := range []string{got.Artifacts.Markdown, got.Artifacts.Metadata} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat artifact %q: %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("artifact %q mode = %o, want 600", path, info.Mode().Perm())
		}
	}
}

func TestWorkflowPDFToMarkdownExtractsFromHashedSourceSnapshot(t *testing.T) {
	binDir := t.TempDir()
	writeFakePDFToText(t, binDir, `#!/bin/sh
set -eu
printf '%s' "$6" > "$CAPTURED_PDF_INPUT"
printf 'changed after snapshot\n' > "$ORIGINAL_PDF_INPUT"
printf 'Snapshot-backed extraction has enough meaningful words.\f'
`)
	t.Setenv("PATH", binDir)

	inputPath := filepath.Join(t.TempDir(), "mutable.pdf")
	inputBytes := []byte("%PDF-1.7\noriginal source bytes\n")
	if err := os.WriteFile(inputPath, inputBytes, 0o600); err != nil {
		t.Fatalf("write mutable PDF fixture: %v", err)
	}
	capturedInputPath := filepath.Join(t.TempDir(), "captured-input.txt")
	t.Setenv("ORIGINAL_PDF_INPUT", inputPath)
	t.Setenv("CAPTURED_PDF_INPUT", capturedInputPath)

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "pdf-to-markdown", inputPath,
		"--out-dir", filepath.Join(t.TempDir(), "out"),
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow pdf-to-markdown exit = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Source struct {
			SHA256 string `json:"sha256"`
		} `json:"source"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode workflow output: %v", err)
	}
	inputSum := sha256.Sum256(inputBytes)
	if got.Source.SHA256 != hex.EncodeToString(inputSum[:]) {
		t.Fatalf("source SHA-256 = %q, want original snapshot hash %x", got.Source.SHA256, inputSum)
	}
	capturedInput, err := os.ReadFile(capturedInputPath)
	if err != nil {
		t.Fatalf("read captured Poppler input: %v", err)
	}
	snapshotPath := string(capturedInput)
	if snapshotPath == inputPath {
		t.Fatalf("Poppler input = original mutable path %q, want private snapshot", snapshotPath)
	}
	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		t.Fatalf("source snapshot remains after conversion: %v", err)
	}
}

func TestWorkflowPDFToMarkdownFailsWhenEmbeddedTextLayerIsMissing(t *testing.T) {
	binDir := t.TempDir()
	writeFakePDFToText(t, binDir, "#!/bin/sh\nset -eu\nprintf '\\f  \\n\\f'\n")
	t.Setenv("PATH", binDir)

	inputPath := filepath.Join(t.TempDir(), "scan.pdf")
	if err := os.WriteFile(inputPath, []byte("%PDF-1.7\nsynthetic scanned fixture\n"), 0o600); err != nil {
		t.Fatalf("write PDF fixture: %v", err)
	}
	outDir := filepath.Join(t.TempDir(), "must-not-exist")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "pdf-to-markdown", inputPath,
		"--out-dir", outDir,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("text-layer-missing exit = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}
	var got struct {
		OK       bool   `json:"ok"`
		Code     string `json:"code"`
		ErrClass string `json:"err_class"`
		Data     struct {
			Reason  string `json:"reason"`
			OCRUsed bool   `json:"ocr_used"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if got.OK || got.Code != "text_layer_missing" || got.ErrClass != "extraction" ||
		got.Data.Reason != "ocr_required" || got.Data.OCRUsed {
		t.Fatalf("text-layer-missing envelope = %+v", got)
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("output directory exists after text-layer failure: %v", err)
	}
}

func TestWorkflowPDFToMarkdownReportsMissingDependency(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	inputPath := filepath.Join(t.TempDir(), "fixture.pdf")
	if err := os.WriteFile(inputPath, []byte("%PDF-1.7\nsynthetic fixture\n"), 0o600); err != nil {
		t.Fatalf("write PDF fixture: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "pdf-to-markdown", inputPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("missing dependency exit = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		Code     string   `json:"code"`
		ErrClass string   `json:"err_class"`
		Commands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode missing dependency envelope: %v", err)
	}
	if got.Code != "dependency_missing" || got.ErrClass != "usage" ||
		!hasExampleContaining(got.Commands, "poppler") {
		t.Fatalf("missing dependency envelope = %+v", got)
	}
}

func writeFakePDFToText(t *testing.T, binDir, script string) {
	t.Helper()
	path := filepath.Join(binDir, "pdftotext")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake pdftotext: %v", err)
	}
}
