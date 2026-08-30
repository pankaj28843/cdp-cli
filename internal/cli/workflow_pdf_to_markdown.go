package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/spf13/cobra"
)

const pdfToMarkdownSchemaVersion = "pdf-to-markdown/v1"

const (
	pdfTextMaxOutputBytes                    = int64(64 << 20)
	pdfTextMaxStderrBytes                    = int64(64 << 10)
	pdfTextMinMeaningfulPageWords            = 3
	pdfTextMinMeaningfulPageAlphaNumeric     = 12
	pdfTextMinMeaningfulDocumentWords        = 5
	pdfTextMinMeaningfulDocumentAlphaNumeric = 24
)

type pdfSource struct {
	Path      string `json:"path"`
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	ByteCount int64  `json:"byte_count"`
}

type pdfTextExtraction struct {
	Engine         string `json:"engine"`
	Representation string `json:"representation"`
	OCRUsed        bool   `json:"ocr_used"`
}

type pdfTextPage struct {
	PageNumber      int    `json:"page_number"`
	MarkdownHeading string `json:"markdown_heading"`
	CharacterCount  int    `json:"character_count"`
	WordCount       int    `json:"word_count"`
	LineCount       int    `json:"line_count"`
	TextSHA256      string `json:"text_sha256"`
}

type pdfTextStats struct {
	PageCount      int `json:"page_count"`
	PagesWithText  int `json:"pages_with_text"`
	CharacterCount int `json:"character_count"`
	WordCount      int `json:"word_count"`
	LineCount      int `json:"line_count"`
}

type pdfTextCoverage struct {
	Usable                      bool `json:"usable"`
	MeaningfulPageCount         int  `json:"meaningful_page_count"`
	WordCount                   int  `json:"word_count"`
	AlphaNumericCharacterCount  int  `json:"alphanumeric_character_count"`
	MinMeaningfulPageWords      int  `json:"min_meaningful_page_words"`
	MinMeaningfulPageCharacters int  `json:"min_meaningful_page_characters"`
	MinDocumentWords            int  `json:"min_document_words"`
	MinDocumentCharacters       int  `json:"min_document_characters"`
}

type pdfTextArtifacts struct {
	Markdown       string `json:"markdown"`
	Metadata       string `json:"metadata"`
	MarkdownSHA256 string `json:"markdown_sha256"`
	MetadataSHA256 string `json:"metadata_sha256"`
}

type pdfToMarkdownMetadata struct {
	SchemaVersion  string            `json:"schema_version"`
	Source         pdfSource         `json:"source"`
	Extraction     pdfTextExtraction `json:"extraction"`
	Pages          []pdfTextPage     `json:"pages"`
	Stats          pdfTextStats      `json:"stats"`
	Coverage       pdfTextCoverage   `json:"coverage"`
	MarkdownSHA256 string            `json:"markdown_sha256"`
}

type pdfToMarkdownResult struct {
	OK            bool              `json:"ok"`
	SchemaVersion string            `json:"schema_version"`
	Source        pdfSource         `json:"source"`
	Extraction    pdfTextExtraction `json:"extraction"`
	Pages         []pdfTextPage     `json:"pages"`
	Stats         pdfTextStats      `json:"stats"`
	Coverage      pdfTextCoverage   `json:"coverage"`
	Artifacts     pdfTextArtifacts  `json:"artifacts"`
	Workflow      struct {
		Name         string   `json:"name"`
		LocalFile    bool     `json:"local_file"`
		BrowserUsed  bool     `json:"browser_used"`
		OCRUsed      bool     `json:"ocr_used"`
		NextCommands []string `json:"next_commands"`
	} `json:"workflow"`
}

func (a *app) newWorkflowPDFToMarkdownCommand() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "pdf-to-markdown <local-pdf>",
		Short: "Extract a local PDF text layer into deterministic Markdown",
		Long: `Extract the embedded text layer of a local PDF with Poppler pdftotext.

This browser-free workflow never performs OCR. It writes deterministic,
page-separated Markdown plus metadata with page provenance, statistics, and
SHA-256 identities. Meaningful coverage requires at least one page with three
alphanumeric words and 12 alphanumeric characters, plus five words and 24
alphanumeric characters across the document. Insufficient coverage fails with
text_layer_missing and reason ocr_required. Extracted UTF-8 text is capped at
64 MiB and fails with pdf_text_output_too_large when exceeded. The owned
pdftotext process uses bounded diagnostics and process-group cancellation where
supported; cancellation and failure metadata reports only the process policy
and truncation state, never extracted text.

Acquire browser-hosted PDFs separately in a headed browser. Create an owned
target with cdp open, retain its .page.target_id, pass that exact ID to click
--wait-download with --target, then close the same target and convert the local
file.`,
		Example: `  cdp workflow pdf-to-markdown tmp/downloads/paper.pdf --json
  cdp workflow pdf-to-markdown tmp/downloads/paper.pdf --out-dir tmp/paper-markdown --json
  pdf_target="$(cdp --browser-mode headed open 'https://example.com/paper' --task-id pdf-download --json | jq -r '.page.target_id')" && cdp --browser-mode headed click 'Download PDF' --by role --role link --target "$pdf_target" --wait-download --download-dir tmp/downloads --json && cdp --browser-mode headed page close --target "$pdf_target" --wait-gone --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			result, err := convertPDFToMarkdown(ctx, args[0], outDir)
			if err != nil {
				return err
			}
			human := fmt.Sprintf("pdf-to-markdown\t%d pages\t%d words\t%s", result.Stats.PageCount, result.Stats.WordCount, result.Artifacts.Markdown)
			return a.render(ctx, human, result)
		},
	}
	cmd.Flags().StringVar(&outDir, "out-dir", "", "Output directory (default: input-derived path under tmp/pdf-to-markdown)")
	return cmd
}

func convertPDFToMarkdown(ctx context.Context, inputPath, requestedOutDir string) (pdfToMarkdownResult, error) {
	var result pdfToMarkdownResult
	source, sourceSnapshotPath, err := snapshotLocalPDF(inputPath)
	if err != nil {
		return result, err
	}
	defer os.Remove(sourceSnapshotPath)

	pdftotextPath, err := exec.LookPath("pdftotext")
	if err != nil {
		return result, commandError(
			"dependency_missing",
			"usage",
			"pdftotext is required for embedded PDF text-layer extraction",
			ExitUsage,
			[]string{"sudo apt-get install poppler-utils", "brew install poppler", "cdp workflow pdf-to-markdown --help"},
		)
	}

	rawText, err := runPDFTextExtraction(ctx, pdftotextPath, sourceSnapshotPath, source.Path, pdfTextMaxOutputBytes)
	if err != nil {
		return result, err
	}

	textPages := normalizePDFTextPages(string(rawText))
	pages, stats := describePDFTextPages(textPages)
	coverage := assessPDFTextCoverage(textPages)
	if !coverage.Usable {
		return result, commandErrorWithData(
			"text_layer_missing",
			"extraction",
			"the PDF has no meaningful embedded text-layer coverage and requires OCR, which this workflow intentionally never performs",
			ExitCheckFailed,
			[]string{"cdp workflow pdf-to-markdown --help"},
			map[string]any{
				"reason":        "ocr_required",
				"ocr_used":      false,
				"source_sha256": source.SHA256,
				"coverage":      coverage,
			},
		)
	}

	markdown := renderPDFTextMarkdown(textPages)
	markdownSum := sha256.Sum256(markdown)
	markdownSHA := hex.EncodeToString(markdownSum[:])

	outDir := requestedOutDir
	if strings.TrimSpace(outDir) == "" {
		outDir = filepath.Join("tmp", "pdf-to-markdown", pdfArtifactSlug(strings.TrimSuffix(source.Filename, filepath.Ext(source.Filename)))+"-"+source.SHA256[:12])
	}
	outDir, err = filepath.Abs(outDir)
	if err != nil {
		return result, commandError("invalid_output_path", "usage", fmt.Sprintf("resolve output directory: %v", err), ExitUsage, nil)
	}
	markdownPath := filepath.Join(outDir, "document.md")
	metadataPath := filepath.Join(outDir, "metadata.json")

	extraction := pdfTextExtraction{
		Engine:         "poppler-pdftotext",
		Representation: "embedded-text-layer",
		OCRUsed:        false,
	}
	metadata := pdfToMarkdownMetadata{
		SchemaVersion:  pdfToMarkdownSchemaVersion,
		Source:         source,
		Extraction:     extraction,
		Pages:          pages,
		Stats:          stats,
		Coverage:       coverage,
		MarkdownSHA256: markdownSHA,
	}
	metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return result, commandError("metadata_encode_failed", "internal", fmt.Sprintf("encode PDF extraction metadata: %v", err), ExitInternal, nil)
	}
	metadataBytes = append(metadataBytes, '\n')
	metadataSum := sha256.Sum256(metadataBytes)

	if err := artifacts.WriteOwnerOnlyFileAtomic(markdownPath, markdown); err != nil {
		return result, commandError("artifact_write_failed", "io", fmt.Sprintf("write Markdown artifact: %v", err), ExitInternal, nil)
	}
	if err := artifacts.WriteOwnerOnlyFileAtomic(metadataPath, metadataBytes); err != nil {
		return result, commandError("artifact_write_failed", "io", fmt.Sprintf("write metadata artifact: %v", err), ExitInternal, nil)
	}

	result.OK = true
	result.SchemaVersion = pdfToMarkdownSchemaVersion
	result.Source = source
	result.Extraction = extraction
	result.Pages = pages
	result.Stats = stats
	result.Coverage = coverage
	result.Artifacts = pdfTextArtifacts{
		Markdown:       markdownPath,
		Metadata:       metadataPath,
		MarkdownSHA256: markdownSHA,
		MetadataSHA256: hex.EncodeToString(metadataSum[:]),
	}
	result.Workflow.Name = "pdf-to-markdown"
	result.Workflow.LocalFile = true
	result.Workflow.BrowserUsed = false
	result.Workflow.OCRUsed = false
	result.Workflow.NextCommands = []string{
		"sed -n '1,160p' " + shellQuote(markdownPath),
		"jq . " + shellQuote(metadataPath),
	}
	return result, nil
}

func snapshotLocalPDF(inputPath string) (pdfSource, string, error) {
	var source pdfSource
	if strings.TrimSpace(inputPath) == "" {
		return source, "", commandError("invalid_pdf_input", "usage", "local PDF path is required", ExitUsage, nil)
	}
	absolutePath, err := filepath.Abs(inputPath)
	if err != nil {
		return source, "", commandError("invalid_pdf_input", "usage", fmt.Sprintf("resolve local PDF path: %v", err), ExitUsage, nil)
	}

	file, err := os.Open(absolutePath)
	if err != nil {
		if os.IsNotExist(err) {
			return source, "", commandError("pdf_not_found", "usage", fmt.Sprintf("local PDF does not exist: %s", absolutePath), ExitUsage, nil)
		}
		return source, "", commandError("pdf_read_failed", "io", fmt.Sprintf("open local PDF: %v", err), ExitInternal, nil)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return source, "", commandError("pdf_read_failed", "io", fmt.Sprintf("stat open local PDF: %v", err), ExitInternal, nil)
	}
	if !info.Mode().IsRegular() {
		return source, "", commandError("invalid_pdf_input", "usage", "local PDF path must name a regular file", ExitUsage, nil)
	}

	header := make([]byte, 1024)
	n, readErr := file.Read(header)
	if readErr != nil && readErr != io.EOF {
		return source, "", commandError("pdf_read_failed", "io", fmt.Sprintf("read local PDF header: %v", readErr), ExitInternal, nil)
	}
	if !bytes.Contains(header[:n], []byte("%PDF-")) {
		return source, "", commandError("invalid_pdf", "usage", "local file does not contain a PDF signature", ExitUsage, nil)
	}

	snapshot, err := os.CreateTemp("", "cdp-pdf-source-*.pdf")
	if err != nil {
		return source, "", commandError("pdf_read_failed", "io", fmt.Sprintf("create private PDF snapshot: %v", err), ExitInternal, nil)
	}
	snapshotPath := snapshot.Name()
	snapshotClosed := false
	defer func() {
		if !snapshotClosed {
			_ = snapshot.Close()
		}
		if snapshotPath != "" {
			_ = os.Remove(snapshotPath)
		}
	}()
	if err := snapshot.Chmod(0o600); err != nil {
		return source, "", commandError("pdf_read_failed", "io", fmt.Sprintf("restrict private PDF snapshot: %v", err), ExitInternal, nil)
	}

	hash := sha256.New()
	writer := io.MultiWriter(snapshot, hash)
	if _, err := writer.Write(header[:n]); err != nil {
		return source, "", commandError("pdf_read_failed", "io", fmt.Sprintf("write private PDF snapshot header: %v", err), ExitInternal, nil)
	}
	remainingByteCount, err := io.Copy(writer, file)
	if err != nil {
		return source, "", commandError("pdf_read_failed", "io", fmt.Sprintf("snapshot local PDF: %v", err), ExitInternal, nil)
	}
	if err := snapshot.Sync(); err != nil {
		return source, "", commandError("pdf_read_failed", "io", fmt.Sprintf("sync private PDF snapshot: %v", err), ExitInternal, nil)
	}
	if err := snapshot.Close(); err != nil {
		return source, "", commandError("pdf_read_failed", "io", fmt.Sprintf("close private PDF snapshot: %v", err), ExitInternal, nil)
	}
	snapshotClosed = true
	source = pdfSource{
		Path:      absolutePath,
		Filename:  filepath.Base(absolutePath),
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
		ByteCount: int64(n) + remainingByteCount,
	}
	resultSnapshotPath := snapshotPath
	snapshotPath = ""
	return source, resultSnapshotPath, nil
}

type boundedPDFTextBuffer struct {
	buffer   bytes.Buffer
	maxBytes int64
	exceeded bool
}

func (buffer *boundedPDFTextBuffer) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := buffer.maxBytes - int64(buffer.buffer.Len())
	if remaining <= 0 {
		buffer.exceeded = buffer.exceeded || originalLength > 0
		return originalLength, nil
	}
	if int64(len(value)) > remaining {
		value = value[:remaining]
		buffer.exceeded = true
	}
	_, _ = buffer.buffer.Write(value)
	return originalLength, nil
}

func runPDFTextExtraction(ctx context.Context, toolPath, snapshotPath, displayPath string, maxOutputBytes int64) ([]byte, error) {
	if maxOutputBytes <= 0 {
		return nil, commandError("pdf_text_invalid_output_limit", "internal", "PDF text output limit must be positive", ExitInternal, nil)
	}
	stdout := &boundedPDFTextBuffer{maxBytes: maxOutputBytes}
	stderr := &boundedPDFTextBuffer{maxBytes: pdfTextMaxStderrBytes}
	runErr := runOwnedCommand(ctx, toolPath, []string{"-layout", "-enc", "UTF-8", "-eol", "unix", snapshotPath, "-"}, stdout, stderr)
	if ctx.Err() != nil {
		return nil, commandErrorWithData(
			"pdf_text_extraction_canceled",
			"timeout",
			fmt.Sprintf("extract embedded PDF text layer from %s: %v", displayPath, ctx.Err()),
			ExitTimeout,
			[]string{"retry with a larger --timeout", "cdp workflow pdf-to-markdown --help"},
			map[string]any{
				"canceled":            true,
				"process_termination": ownedProcessTerminationMode(),
			},
		)
	}
	if stdout.exceeded {
		return nil, commandErrorWithData(
			"pdf_text_output_too_large",
			"extraction",
			fmt.Sprintf("embedded PDF text exceeds the %d-byte extraction limit", maxOutputBytes),
			ExitCheckFailed,
			[]string{"use a smaller PDF", "split the PDF before extraction", "cdp workflow pdf-to-markdown --help"},
			map[string]any{
				"max_output_bytes":    maxOutputBytes,
				"process_termination": ownedProcessTerminationMode(),
			},
		)
	}
	if runErr != nil {
		detail := strings.TrimSpace(stderr.buffer.String())
		if detail == "" {
			detail = runErr.Error()
		}
		if len(detail) > 2048 {
			detail = strings.ToValidUTF8(detail[:2048], "") + "..."
		}
		return nil, commandErrorWithData(
			"pdf_text_extraction_failed",
			"extraction",
			fmt.Sprintf("extract embedded PDF text layer from %s: %s", displayPath, detail),
			ExitCheckFailed,
			[]string{"verify the PDF opens normally", "cdp workflow pdf-to-markdown --help"},
			map[string]any{
				"max_output_bytes":    maxOutputBytes,
				"max_stderr_bytes":    pdfTextMaxStderrBytes,
				"stderr_truncated":    stderr.exceeded,
				"process_termination": ownedProcessTerminationMode(),
			},
		)
	}
	return append([]byte(nil), stdout.buffer.Bytes()...), nil
}

func normalizePDFTextPages(raw string) []string {
	raw = strings.ToValidUTF8(raw, "")
	raw = strings.ReplaceAll(raw, "\x00", "")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	segments := strings.Split(raw, "\f")
	if len(segments) > 1 && strings.TrimSpace(segments[len(segments)-1]) == "" {
		segments = segments[:len(segments)-1]
	}
	if len(segments) == 0 {
		return []string{""}
	}
	pages := make([]string, len(segments))
	for index, segment := range segments {
		lines := strings.Split(segment, "\n")
		for lineIndex, line := range lines {
			lines[lineIndex] = strings.TrimRight(line, " \t")
		}
		for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
			lines = lines[1:]
		}
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		pages[index] = strings.Join(lines, "\n")
	}
	return pages
}

func assessPDFTextCoverage(pages []string) pdfTextCoverage {
	coverage := pdfTextCoverage{
		MinMeaningfulPageWords:      pdfTextMinMeaningfulPageWords,
		MinMeaningfulPageCharacters: pdfTextMinMeaningfulPageAlphaNumeric,
		MinDocumentWords:            pdfTextMinMeaningfulDocumentWords,
		MinDocumentCharacters:       pdfTextMinMeaningfulDocumentAlphaNumeric,
	}
	for _, page := range pages {
		pageWords := 0
		pageCharacters := 0
		for _, field := range strings.Fields(page) {
			hasAlphaNumeric := false
			for _, r := range field {
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					hasAlphaNumeric = true
					pageCharacters++
				}
			}
			if hasAlphaNumeric {
				pageWords++
			}
		}
		coverage.WordCount += pageWords
		coverage.AlphaNumericCharacterCount += pageCharacters
		if pageWords >= pdfTextMinMeaningfulPageWords && pageCharacters >= pdfTextMinMeaningfulPageAlphaNumeric {
			coverage.MeaningfulPageCount++
		}
	}
	coverage.Usable = coverage.MeaningfulPageCount > 0 &&
		coverage.WordCount >= pdfTextMinMeaningfulDocumentWords &&
		coverage.AlphaNumericCharacterCount >= pdfTextMinMeaningfulDocumentAlphaNumeric
	return coverage
}

func describePDFTextPages(textPages []string) ([]pdfTextPage, pdfTextStats) {
	pages := make([]pdfTextPage, 0, len(textPages))
	stats := pdfTextStats{PageCount: len(textPages)}
	for index, text := range textPages {
		textSum := sha256.Sum256([]byte(text))
		page := pdfTextPage{
			PageNumber:      index + 1,
			MarkdownHeading: fmt.Sprintf("Page %d", index+1),
			CharacterCount:  utf8.RuneCountInString(text),
			WordCount:       len(strings.Fields(text)),
			TextSHA256:      hex.EncodeToString(textSum[:]),
		}
		if text != "" {
			page.LineCount = strings.Count(text, "\n") + 1
			stats.PagesWithText++
		}
		stats.CharacterCount += page.CharacterCount
		stats.WordCount += page.WordCount
		stats.LineCount += page.LineCount
		pages = append(pages, page)
	}
	return pages, stats
}

func renderPDFTextMarkdown(textPages []string) []byte {
	var markdown strings.Builder
	markdown.WriteString("<!-- cdp:pdf-text-layer-only; ocr=false -->\n\n")
	for index, text := range textPages {
		if index > 0 {
			markdown.WriteByte('\n')
		}
		fmt.Fprintf(&markdown, "## Page %d\n", index+1)
		if text != "" {
			markdown.WriteByte('\n')
			markdown.WriteString(text)
			markdown.WriteByte('\n')
		}
	}
	return []byte(markdown.String())
}

func pdfArtifactSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var slug strings.Builder
	lastDash := false
	for _, char := range value {
		isASCIIAlphaNumeric := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if isASCIIAlphaNumeric {
			slug.WriteRune(char)
			lastDash = false
			continue
		}
		if slug.Len() > 0 && !lastDash {
			slug.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(slug.String(), "-")
	if result == "" {
		return "document"
	}
	return result
}
