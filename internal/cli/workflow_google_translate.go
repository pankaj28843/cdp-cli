package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "image/jpeg"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const (
	googleTranslateSchemaVersion   = "google-translate/v1"
	googleTranslateMaxCharacters   = 5000
	googleTranslateDefaultChunk    = 4800
	googleTranslateDefaultWait     = 3 * time.Minute
	googleTranslateDefaultPoll     = 500 * time.Millisecond
	googleTranslateTextMaxBytes    = int64(16 << 20)
	googleTranslateWebsiteMaxBytes = int64(8 << 20)
)

var googleTranslateLanguageCodePattern = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z]{2,4})?$`)

type googleTranslateRequest struct {
	Mode       string
	Source     string
	Target     string
	ChunkSize  int
	Wait       time.Duration
	Poll       time.Duration
	OutDir     string
	Output     string
	Text       string
	TextFile   string
	File       string
	URL        string
	FromStdin  bool
	InputFlags int
}

type googleTranslateInput struct {
	Kind       string               `json:"kind"`
	Path       string               `json:"path,omitempty"`
	URL        string               `json:"url,omitempty"`
	Source     string               `json:"source,omitempty"`
	Target     string               `json:"target,omitempty"`
	Characters int                  `json:"characters,omitempty"`
	SourceFile *googleTranslateFile `json:"source_file,omitempty"`
}

type googleTranslateFile struct {
	Path      string `json:"path"`
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	ByteCount int64  `json:"byte_count"`
}

type googleTranslateChunk struct {
	Index           int    `json:"index"`
	SourceChars     int    `json:"source_chars"`
	TranslatedChars int    `json:"translated_chars"`
	Output          string `json:"output,omitempty"`
}

type googleTranslatePage struct {
	Index          int    `json:"index"`
	SourcePath     string `json:"source_path"`
	TranslatedPath string `json:"translated_path"`
	WaitMS         int64  `json:"wait_ms"`
	DownloadBytes  int64  `json:"download_bytes"`
	Validated      bool   `json:"validated"`
}

type googleTranslateArtifacts struct {
	Output   string `json:"output,omitempty"`
	Metadata string `json:"metadata"`
}

type googleTranslateCleanup struct {
	Attempted bool              `json:"attempted"`
	Closed    bool              `json:"closed"`
	TargetIDs []string          `json:"target_ids"`
	Reports   []pageCloseReport `json:"reports"`
	Errors    []string          `json:"errors,omitempty"`
}

type googleTranslateResult struct {
	OK               bool                     `json:"ok"`
	SchemaVersion    string                   `json:"schema_version"`
	Input            googleTranslateInput     `json:"input"`
	Mode             string                   `json:"mode"`
	Source           string                   `json:"source"`
	Target           string                   `json:"target"`
	Chunks           []googleTranslateChunk   `json:"chunks,omitempty"`
	Pages            []googleTranslatePage    `json:"pages,omitempty"`
	DetectedScan     bool                     `json:"detected_scan,omitempty"`
	Artifacts        googleTranslateArtifacts `json:"artifacts"`
	TargetID         string                   `json:"target_id,omitempty"`
	CreatedTargetIDs []string                 `json:"created_target_ids,omitempty"`
	Cleanup          googleTranslateCleanup   `json:"cleanup"`
	Warnings         []string                 `json:"warnings,omitempty"`
	NextCommands     []string                 `json:"next_commands,omitempty"`
}

type googleTranslateState struct {
	Ready       bool   `json:"ready"`
	Download    bool   `json:"download"`
	ImageCount  int    `json:"image_count"`
	Value       string `json:"value,omitempty"`
	Text        string `json:"text,omitempty"`
	Body        string `json:"body,omitempty"`
	URL         string `json:"url,omitempty"`
	Placeholder bool   `json:"placeholder"`
	Error       string `json:"error,omitempty"`
}

type googleTranslatePDFProbe struct {
	Coverage  pdfTextCoverage `json:"coverage"`
	PageCount int             `json:"page_count"`
	Usable    bool            `json:"usable"`
}

type googleTranslateTarget struct {
	Target  cdp.TargetInfo
	Session *cdp.PageSession
}

func (a *app) newWorkflowGoogleTranslateCommand() *cobra.Command {
	var request googleTranslateRequest
	var ownership targetOwnershipMetadata
	cmd := &cobra.Command{
		Use:   "google-translate",
		Short: "Translate text, documents, images, scanned PDFs, or websites through headed Google Translate",
		Long: `Translate through the live headed Google Translate UI. Use exactly one
input flag: --text, --text-file, --file, --url, or --stdin. --file PDFs are
probed for meaningful embedded text; scans are burst into PNG pages and sent
through Image translation, then reassembled into a new PDF. Image generation
and document translation are asynchronous, so --wait is a real bounded wait,
not a fixed sleep. The command creates and closes its own task-owned target.

Google text input is limited to 5,000 characters. The default 4,800-character
chunk size leaves headroom and preserves every source character in order.
Language values accept Google language codes (for example da, en, de, zh-CN)
and common names such as Danish or English; source auto/detect is supported.

Document translation is for text-layer PDFs and supported office files. An
image-only PDF must use the image path; it is never falsely reported as a
successful document translation. Website translation follows the new
translate.goog result target created by Google and closes that target too.`,
		Example: `  cdp --browser-mode headed workflow google-translate --text 'Dette er en kort test.' --source da --target en --json
  cdp --browser-mode headed workflow google-translate --file "$HOME/Downloads/Pelvic floor training confirmation.pdf" --target en --out-dir tmp/translated-scan --json
  cdp --browser-mode headed workflow google-translate --url 'https://da.wikipedia.org/wiki/Danmark' --target en --output tmp/denmark.txt --json
  cdp --browser-mode headed workflow google-translate --text-file tmp/danish.txt --source da --target en --chunk-size 4800 --wait 5m --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			request.InputFlags = countGoogleTranslateInputs(cmd, request)
			if err := normalizeGoogleTranslateRequest(&request); err != nil {
				return err
			}
			if request.FromStdin {
				value, err := readGoogleTranslateText(cmd.InOrStdin(), googleTranslateTextMaxBytes)
				if err != nil {
					return commandError("text_read_failed", "io", fmt.Sprintf("read --stdin: %v", err), ExitUsage, nil)
				}
				request.Text = value
			}
			if request.TextFile != "" {
				value, err := readGoogleTranslateTextFile(request.TextFile)
				if err != nil {
					return err
				}
				request.Text = value
			}
			result, err := a.runGoogleTranslate(ctx, request, ownership)
			if err != nil {
				return err
			}
			human := fmt.Sprintf("google-translate\t%s -> %s\t%s", result.Source, result.Target, result.Artifacts.Output)
			return a.render(ctx, human, result)
		},
	}
	cmd.Flags().StringVar(&request.Mode, "mode", "auto", "workflow mode: auto, text, document, image, or website")
	cmd.Flags().StringVar(&request.Source, "source", "auto", "source language code or name; auto/detect enables detection")
	cmd.Flags().StringVar(&request.Target, "target", "en", "target language code or name; defaults to English")
	cmd.Flags().StringVar(&request.Text, "text", "", "text to translate")
	cmd.Flags().StringVar(&request.TextFile, "text-file", "", "UTF-8 text file to translate")
	cmd.Flags().StringVar(&request.File, "file", "", "local PDF, office document, or image file to translate")
	cmd.Flags().StringVar(&request.URL, "url", "", "website URL to translate")
	cmd.Flags().BoolVar(&request.FromStdin, "stdin", false, "read text to translate from stdin")
	cmd.Flags().IntVar(&request.ChunkSize, "chunk-size", googleTranslateDefaultChunk, "maximum text characters per Google input chunk (1-5000)")
	cmd.Flags().DurationVar(&request.Wait, "wait", googleTranslateDefaultWait, "maximum wait for each asynchronous translation result")
	cmd.Flags().DurationVar(&request.Poll, "poll", googleTranslateDefaultPoll, "poll interval for Google UI state and child-target waits")
	cmd.Flags().StringVar(&request.OutDir, "out-dir", "", "output directory; defaults to a new directory under tmp/google-translate")
	cmd.Flags().StringVar(&request.Output, "output", "", "output file; defaults by mode inside --out-dir")
	addTargetOwnershipFlags(cmd, &ownership, true)
	return cmd
}

func countGoogleTranslateInputs(cmd *cobra.Command, request googleTranslateRequest) int {
	count := 0
	for _, name := range []string{"text", "text-file", "file", "url", "stdin"} {
		if cmd.Flags().Changed(name) {
			count++
		}
	}
	if count == 0 {
		if request.Text != "" || request.TextFile != "" || request.File != "" || request.URL != "" || request.FromStdin {
			return 1
		}
	}
	return count
}

func normalizeGoogleTranslateRequest(request *googleTranslateRequest) error {
	if request.InputFlags != 1 {
		return commandError("input_required", "usage", "provide exactly one of --text, --text-file, --file, --url, or --stdin", ExitUsage, []string{"cdp workflow google-translate --text 'Hello' --source auto --target en --json"})
	}
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	switch request.Mode {
	case "auto", "text", "document", "image", "website":
	default:
		return commandError("invalid_mode", "usage", "--mode must be auto, text, document, image, or website", ExitUsage, nil)
	}
	var err error
	request.Source, err = normalizeGoogleTranslateLanguage(request.Source, true)
	if err != nil {
		return commandError("invalid_source_language", "usage", err.Error(), ExitUsage, []string{"cdp workflow google-translate --source da --target en --text 'Hej' --json"})
	}
	request.Target, err = normalizeGoogleTranslateLanguage(request.Target, false)
	if err != nil {
		return commandError("invalid_target_language", "usage", err.Error(), ExitUsage, []string{"cdp workflow google-translate --source auto --target en --text 'Hello' --json"})
	}
	if request.ChunkSize <= 0 || request.ChunkSize > googleTranslateMaxCharacters {
		return commandError("invalid_chunk_size", "usage", fmt.Sprintf("--chunk-size must be between 1 and %d", googleTranslateMaxCharacters), ExitUsage, nil)
	}
	if request.Wait <= 0 || request.Poll <= 0 {
		return commandError("invalid_wait", "usage", "--wait and --poll must be positive", ExitUsage, []string{"cdp workflow google-translate --wait 5m --poll 500ms --text 'Hello' --json"})
	}
	if request.TextFile != "" {
		request.TextFile, err = filepath.Abs(request.TextFile)
		if err != nil {
			return commandError("invalid_text_file", "usage", fmt.Sprintf("resolve --text-file: %v", err), ExitUsage, nil)
		}
	}
	if request.Text == "" && request.TextFile == "" && request.File == "" && request.URL == "" && !request.FromStdin {
		return commandError("empty_input", "usage", "the selected input is empty", ExitUsage, nil)
	}
	if request.File != "" {
		request.File, err = filepath.Abs(request.File)
		if err != nil {
			return commandError("invalid_file", "usage", fmt.Sprintf("resolve --file: %v", err), ExitUsage, nil)
		}
	}
	if request.URL != "" {
		rawURL := strings.TrimSpace(request.URL)
		parsed, parseErr := url.Parse(rawURL)
		if parseErr != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
			return commandError("invalid_url", "usage", "--url must be an absolute http(s) URL", ExitUsage, nil)
		}
		request.URL = rawURL
	}
	if request.Text != "" && !utf8Valid(request.Text) {
		return commandError("invalid_text", "usage", "--text must be valid UTF-8", ExitUsage, nil)
	}
	return nil
}

func normalizeGoogleTranslateLanguage(value string, source bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if source {
			return "auto", nil
		}
		return "en", nil
	}
	normalized := strings.ToLower(strings.ReplaceAll(value, "_", "-"))
	aliases := map[string]string{
		"auto": "auto", "detect": "auto", "detected": "auto", "detect-language": "auto",
		"english": "en", "danish": "da", "german": "de", "french": "fr", "spanish": "es",
		"italian": "it", "dutch": "nl", "swedish": "sv", "norwegian": "no", "finnish": "fi",
		"polish": "pl", "portuguese": "pt", "russian": "ru", "ukrainian": "uk", "hindi": "hi",
		"telugu": "te", "japanese": "ja", "korean": "ko", "chinese": "zh-CN", "simplified chinese": "zh-CN",
	}
	if alias, ok := aliases[normalized]; ok {
		normalized = alias
	}
	if normalized == "auto" {
		if source {
			return normalized, nil
		}
		return "", errors.New("target language cannot be auto/detect")
	}
	if !googleTranslateLanguageCodePattern.MatchString(strings.ToLower(normalized)) {
		return "", fmt.Errorf("language %q is not a Google language code or supported common name", value)
	}
	parts := strings.Split(normalized, "-")
	parts[0] = strings.ToLower(parts[0])
	if len(parts) == 2 && len(parts[1]) == 2 {
		parts[1] = strings.ToUpper(parts[1])
	} else if len(parts) == 2 {
		parts[1] = strings.ToLower(parts[1])
	}
	return strings.Join(parts, "-"), nil
}

func utf8Valid(value string) bool {
	return strings.ToValidUTF8(value, "") == value
}

func readGoogleTranslateText(reader io.Reader, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		return "", errors.New("text byte limit must be positive")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("text exceeds %d-byte input limit", maxBytes)
	}
	if !utf8Valid(string(data)) {
		return "", errors.New("text is not valid UTF-8")
	}
	value := string(data)
	if strings.TrimSpace(value) == "" {
		return "", errors.New("text is empty")
	}
	return value, nil
}

func readGoogleTranslateTextFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", commandError("text_file_not_found", "usage", fmt.Sprintf("open --text-file %s: %v", path, err), ExitUsage, nil)
	}
	defer file.Close()
	value, err := readGoogleTranslateText(file, googleTranslateTextMaxBytes)
	if err != nil {
		return "", commandError("text_file_read_failed", "usage", fmt.Sprintf("read --text-file %s: %v", path, err), ExitUsage, nil)
	}
	return value, nil
}

func (a *app) runGoogleTranslate(ctx context.Context, request googleTranslateRequest, ownership targetOwnershipMetadata) (googleTranslateResult, error) {
	result := googleTranslateResult{
		SchemaVersion: googleTranslateSchemaVersion,
		Source:        request.Source,
		Target:        request.Target,
		Warnings:      []string{},
		Chunks:        []googleTranslateChunk{},
		Pages:         []googleTranslatePage{},
		Cleanup:       googleTranslateCleanup{TargetIDs: []string{}, Reports: []pageCloseReport{}},
	}
	input, kind, probe, err := resolveGoogleTranslateInput(ctx, request)
	if err != nil {
		return result, err
	}
	if request.Mode != "auto" && request.Mode != kind {
		return result, commandError("input_mode_mismatch", "usage", fmt.Sprintf("--mode %s does not match the selected input; resolved mode is %s", request.Mode, kind), ExitUsage, nil)
	}
	result.Input = input
	result.Mode = kind
	result.DetectedScan = strings.EqualFold(filepath.Ext(input.Path), ".pdf") && kind == "image"
	if probe != nil {
		result.DetectedScan = !probe.Usable
	}
	if err := validateGoogleTranslateOutput(request.Output, input, kind); err != nil {
		return result, err
	}
	outDir, err := prepareGoogleTranslateOutputDir(request.OutDir, googleTranslateOutputSlug(input, kind))
	if err != nil {
		return result, err
	}
	ownership.CreatedBy = strings.TrimSpace(ownership.CreatedBy)
	if ownership.CreatedBy == "" {
		ownership.CreatedBy = "cdp"
	}
	if strings.TrimSpace(ownership.TaskID) == "" {
		ownership.TaskID = fmt.Sprintf("google-translate-%d", time.Now().UnixNano())
	}
	ownership.Workflow = "google-translate"
	ownership, err = normalizeTargetOwnership(ownership, "cdp")
	if err != nil {
		return result, err
	}

	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return result, commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
	}
	defer closeClient(context.Background())
	initialURL := googleTranslateRoute(request.Source, request.Target, "translate")
	targetID, createErr := a.createPageTargetWithOwnership(ctx, client, initialURL, ownership)
	if targetID != "" {
		result.TargetID = targetID
		result.CreatedTargetIDs = append(result.CreatedTargetIDs, targetID)
	}
	if createErr != nil {
		if targetID != "" {
			result.Cleanup = closeGoogleTranslateTargets(client, []cdp.TargetInfo{{TargetID: targetID, Type: "page", URL: initialURL}}, a.browserModeName())
		}
		return result, createErr
	}
	target := cdp.TargetInfo{TargetID: targetID, Type: "page", URL: initialURL}
	target, _ = cdp.TargetInfoWithClient(ctx, client, targetID)
	if target.TargetID == "" {
		target = cdp.TargetInfo{TargetID: targetID, Type: "page", URL: initialURL}
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, func(context.Context) error { return nil })
	if err != nil {
		result.Cleanup = closeGoogleTranslateTargets(client, []cdp.TargetInfo{target}, a.browserModeName())
		return result, commandError("connection_failed", "connection", fmt.Sprintf("attach Google Translate target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --browser-mode headed --json"})
	}
	sessions := []*cdp.PageSession{session}
	targets := []cdp.TargetInfo{target}
	operationErr := error(nil)
	switch kind {
	case "text":
		operationErr = a.translateGoogleText(ctx, session, request, input, outDir, &result)
	case "website":
		var child *googleTranslateTarget
		child, operationErr = a.translateGoogleWebsite(ctx, client, session, request, outDir, &result)
		if child != nil {
			sessions = append(sessions, child.Session)
			targets = append(targets, child.Target)
			result.CreatedTargetIDs = append(result.CreatedTargetIDs, child.Target.TargetID)
		}
	case "document":
		operationErr = a.translateGoogleDocument(ctx, session, request, input, outDir, &result)
	case "image":
		operationErr = a.translateGoogleImages(ctx, session, request, input, outDir, &result)
	default:
		operationErr = commandError("invalid_mode", "usage", fmt.Sprintf("unsupported resolved mode %q", kind), ExitUsage, nil)
	}
	for _, activeSession := range sessions {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = activeSession.Close(closeCtx)
		cancel()
	}
	result.Cleanup = closeGoogleTranslateTargets(client, targets, a.browserModeName())
	result.Input.Target = request.Target
	result.Input.Source = request.Source
	result.Artifacts.Metadata = nextGoogleTranslatePath(outDir, "metadata.json")
	if operationErr != nil {
		return result, attachGoogleTranslateResult(operationErr, result)
	}
	result.OK = true
	result.NextCommands = []string{
		"jq . " + shellQuote(result.Artifacts.Metadata),
		"cdp --browser-mode headed pages --json",
	}
	metadata, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return result, commandError("metadata_encode_failed", "internal", fmt.Sprintf("encode Google Translate metadata: %v", err), ExitInternal, nil)
	}
	metadata = append(metadata, '\n')
	if err := artifacts.WriteOwnerOnlyFileAtomic(result.Artifacts.Metadata, metadata); err != nil {
		return result, commandError("artifact_write_failed", "io", fmt.Sprintf("write Google Translate metadata: %v", err), ExitInternal, nil)
	}
	return result, nil
}

func attachGoogleTranslateResult(err error, result googleTranslateResult) error {
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		commandErr.Data = result
		return err
	}
	return commandErrorWithData("google_translate_failed", "runtime", err.Error(), ExitCheckFailed, []string{"cdp workflow google-translate --help"}, result)
}

func closeGoogleTranslateTargets(client cdp.CommandClient, targets []cdp.TargetInfo, browserMode string) googleTranslateCleanup {
	cleanup := googleTranslateCleanup{Attempted: len(targets) > 0, Closed: true, TargetIDs: []string{}, Reports: []pageCloseReport{}}
	seen := map[string]bool{}
	for index := len(targets) - 1; index >= 0; index-- {
		target := targets[index]
		if target.TargetID == "" || seen[target.TargetID] {
			continue
		}
		seen[target.TargetID] = true
		cleanup.TargetIDs = append(cleanup.TargetIDs, target.TargetID)
		closeCtx, cancel := context.WithTimeout(context.Background(), pageCloseAttemptTimeout(browserMode)*2)
		report := closePageTargetSettled(closeCtx, client, target, pageCloseOptions{
			WaitGone:     true,
			MaxAttempts:  2,
			AttemptWait:  pageCloseAttemptTimeout(browserMode),
			PollInterval: defaultPageClosePollInterval,
			RetryBackoff: defaultPageCloseRetryBackoff,
		})
		cancel()
		cleanup.Reports = append(cleanup.Reports, report)
		if !report.Closed || !report.TargetGone {
			cleanup.Closed = false
			if report.LastError != "" {
				cleanup.Errors = append(cleanup.Errors, fmt.Sprintf("%s: %s", target.TargetID, report.LastError))
			}
		}
	}
	if len(cleanup.TargetIDs) == 0 {
		cleanup.Closed = false
	}
	return cleanup
}

func resolveGoogleTranslateInput(ctx context.Context, request googleTranslateRequest) (googleTranslateInput, string, *googleTranslatePDFProbe, error) {
	input := googleTranslateInput{Source: request.Source, Target: request.Target}
	switch {
	case request.TextFile != "":
		file, err := googleTranslateFileIdentity(request.TextFile)
		if err != nil {
			return input, "", nil, err
		}
		input.Kind, input.Path, input.Characters, input.SourceFile = "text", file.Path, len([]rune(request.Text)), &file
		return input, "text", nil, nil
	case request.Text != "":
		input.Kind = "text"
		input.Characters = len([]rune(request.Text))
		return input, "text", nil, nil
	case request.URL != "":
		input.Kind, input.URL = "website", request.URL
		return input, "website", nil, nil
	case request.File != "":
		file, err := googleTranslateFileIdentity(request.File)
		if err != nil {
			return input, "", nil, err
		}
		input.Path, input.SourceFile = file.Path, &file
		mode := request.Mode
		ext := strings.ToLower(filepath.Ext(file.Path))
		isPDF := ext == ".pdf"
		isImage := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true}[ext]
		if mode == "text" || mode == "website" {
			return input, "", nil, commandError("input_mode_mismatch", "usage", fmt.Sprintf("--mode %s requires its matching text or URL input", mode), ExitUsage, nil)
		}
		if mode == "image" && !isPDF && !isImage {
			return input, "", nil, commandError("unsupported_image", "usage", "--mode image accepts PDF, PNG, JPEG, or WebP files", ExitUsage, nil)
		}
		if mode == "document" && isImage {
			return input, "", nil, commandError("document_input_invalid", "usage", "image files cannot use --mode document", ExitUsage, nil)
		}
		if mode == "image" || isImage {
			input.Kind = "image"
			return input, "image", nil, nil
		}
		if isPDF && mode == "auto" {
			probe, err := probeGoogleTranslatePDF(ctx, file.Path)
			if err != nil {
				return input, "", nil, err
			}
			if probe.Usable {
				input.Kind = "document"
				return input, "document", &probe, nil
			}
			input.Kind = "image"
			return input, "image", &probe, nil
		}
		if isPDF || map[string]bool{".docx": true, ".pptx": true, ".xlsx": true}[ext] {
			input.Kind = "document"
			return input, "document", nil, nil
		}
		return input, "", nil, commandError("unsupported_file", "usage", fmt.Sprintf("unsupported --file extension %q; use PDF, DOCX, PPTX, XLSX, PNG, JPEG, or WebP", ext), ExitUsage, nil)
	default:
		return input, "", nil, commandError("input_required", "usage", "an input is required", ExitUsage, nil)
	}
}

func googleTranslateFileIdentity(path string) (googleTranslateFile, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return googleTranslateFile{}, commandError("invalid_file", "usage", fmt.Sprintf("resolve file %s: %v", path, err), ExitUsage, nil)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return googleTranslateFile{}, commandError("file_not_found", "usage", fmt.Sprintf("open %s: %v", absolute, err), ExitUsage, nil)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return googleTranslateFile{}, commandError("invalid_file", "usage", fmt.Sprintf("%s must be a regular file", absolute), ExitUsage, nil)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return googleTranslateFile{}, commandError("file_read_failed", "io", fmt.Sprintf("hash %s: %v", absolute, err), ExitInternal, nil)
	}
	return googleTranslateFile{Path: absolute, Filename: filepath.Base(absolute), SHA256: hex.EncodeToString(hash.Sum(nil)), ByteCount: info.Size()}, nil
}

func probeGoogleTranslatePDF(ctx context.Context, path string) (googleTranslatePDFProbe, error) {
	tool, err := exec.LookPath("pdftotext")
	if err != nil {
		return googleTranslatePDFProbe{}, commandError("dependency_missing", "usage", "pdftotext is required to distinguish text-layer PDFs from scans", ExitUsage, []string{"brew install poppler", "sudo apt-get install poppler-utils"})
	}
	raw, err := runPDFTextExtraction(ctx, tool, path, path, pdfTextMaxOutputBytes)
	if err != nil {
		return googleTranslatePDFProbe{}, err
	}
	pages := normalizePDFTextPages(string(raw))
	_, stats := describePDFTextPages(pages)
	coverage := assessPDFTextCoverage(pages)
	return googleTranslatePDFProbe{Coverage: coverage, PageCount: stats.PageCount, Usable: coverage.Usable}, nil
}

func prepareGoogleTranslateOutputDir(requested, slug string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		base := filepath.Join("tmp", "google-translate", slug)
		path := nextGoogleTranslateDirectory(base)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return "", commandError("output_dir_unavailable", "io", fmt.Sprintf("create output directory %s: %v", path, err), ExitInternal, nil)
		}
		return path, nil
	}
	path, err := filepath.Abs(requested)
	if err != nil {
		return "", commandError("invalid_output_path", "usage", fmt.Sprintf("resolve --out-dir: %v", err), ExitUsage, nil)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", commandError("output_dir_unavailable", "io", fmt.Sprintf("create output directory %s: %v", path, err), ExitInternal, nil)
	}
	return path, nil
}

func nextGoogleTranslateDirectory(base string) string {
	base = filepath.Clean(base)
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return base
	}
	for index := 2; index < 10000; index++ {
		candidate := fmt.Sprintf("%s-%d", base, index)
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

func googleTranslateOutputSlug(input googleTranslateInput, kind string) string {
	name := kind
	if input.Path != "" {
		name = strings.TrimSuffix(filepath.Base(input.Path), filepath.Ext(input.Path))
	}
	name = strings.ToLower(name)
	var builder strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteRune('-')
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		slug = "translation"
	}
	return slug + "-" + kind
}

func validateGoogleTranslateOutput(output string, input googleTranslateInput, kind string) error {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(output))
	if ext == "" {
		return nil
	}
	allowed := map[string]bool{}
	switch kind {
	case "text", "website":
		allowed[".txt"] = true
	case "document":
		allowed[strings.ToLower(filepath.Ext(input.Path))] = true
	case "image":
		allowed[".png"] = true
		if strings.EqualFold(filepath.Ext(input.Path), ".pdf") {
			allowed = map[string]bool{".pdf": true}
		} else {
			allowed[".pdf"] = true
		}
	}
	if allowed[ext] {
		return nil
	}
	return commandError("invalid_output_extension", "usage", fmt.Sprintf("output extension %s is not valid for %s mode", ext, kind), ExitUsage, nil)
}

func nextGoogleTranslatePath(dir, filename string) string {
	filename = filepath.Base(filename)
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	path := filepath.Join(dir, filename)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	for index := 2; index < 10000; index++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, index, ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, time.Now().UnixNano(), ext))
}

func googleTranslateRoute(source, target, operation string) string {
	parsed, _ := url.Parse("https://translate.google.com/")
	query := parsed.Query()
	query.Set("sl", source)
	query.Set("tl", target)
	query.Set("op", operation)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func googleTranslateTextChunks(text string, maxCharacters int) []string {
	if maxCharacters <= 0 || text == "" {
		return []string{text}
	}
	runes := []rune(text)
	chunks := make([]string, 0, (len(runes)+maxCharacters-1)/maxCharacters)
	for len(runes) > maxCharacters {
		cut := googleTranslateChunkCut(runes, maxCharacters)
		chunks = append(chunks, string(runes[:cut]))
		runes = runes[cut:]
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}

func googleTranslateChunkCut(runes []rune, maxCharacters int) int {
	minimum := maxCharacters / 2
	if minimum < 1 {
		minimum = 1
	}
	for index := maxCharacters; index >= minimum; index-- {
		if index >= 2 && runes[index-1] == '\n' && runes[index-2] == '\n' {
			return index
		}
	}
	for index := maxCharacters; index >= minimum; index-- {
		if runes[index-1] == '\n' {
			return index
		}
	}
	for index := maxCharacters; index >= minimum; index-- {
		if googleTranslateSentenceEnd(runes[index-1]) && index < len(runes) && unicode.IsSpace(runes[index]) {
			return index
		}
	}
	for index := maxCharacters; index >= minimum; index-- {
		if unicode.IsSpace(runes[index-1]) {
			return index
		}
	}
	return maxCharacters
}

func googleTranslateSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', '。', '！', '？':
		return true
	default:
		return false
	}
}

func googleVisibleButtonClickExpression(aria, text string) string {
	ariaJSON, _ := json.Marshal(aria)
	textJSON, _ := json.Marshal(strings.TrimSpace(text))
	return fmt.Sprintf(`(() => {
  const aria = %s;
  const wanted = %s;
  const visible = (el) => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el); return r.width > 0 && r.height > 0 && s.display !== "none" && s.visibility !== "hidden"; };
  const buttons = Array.from(document.querySelectorAll("button"));
  const button = buttons.find((el) => visible(el) && ((aria && el.getAttribute("aria-label") === aria) || (wanted && (el.innerText || "").trim() === wanted)));
  if (!button) return false;
  button.click();
  return true;
})()`, string(ariaJSON), string(textJSON))
}

func googleFocusExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => { const el = document.querySelector(%s); if (!el) return false; el.focus(); return document.activeElement === el; })()`, string(selectorJSON))
}

func googleStateExpression(kind string) string {
	switch kind {
	case "text":
		return `(() => {
  const visible = (el) => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el); return r.width > 0 && r.height > 0 && s.display !== "none" && s.visibility !== "hidden"; };
  const body = (document.body?.innerText || "").replace(/\s+/g, " ").trim();
	  let nodes = Array.from(document.querySelectorAll(".HwtZe")).filter(visible).filter((el) => !el.parentElement?.closest(".HwtZe"));
  if (!nodes.length) nodes = Array.from(document.querySelectorAll("span[jsname=txFAF]")).filter(visible);
  const text = nodes.map((el) => (el.innerText || el.textContent || "").trim()).filter(Boolean).join("\n").trim();
  return {ready: Boolean(text) && body.includes("Translation results"), text, body: body.slice(0, 4000), url: location.href, placeholder: body.includes("Loading")};
})()`
	case "image":
		return `(() => {
  const visible = (el) => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el); return r.width > 0 && r.height > 0 && s.display !== "none" && s.visibility !== "hidden"; };
  const body = (document.body?.innerText || "").replace(/\s+/g, " ").trim();
  const download = Array.from(document.querySelectorAll("button[aria-label='Download translation']")).some(visible);
  const images = Array.from(document.querySelectorAll("img.Jmlpdc")).filter(visible).length;
  const ready = download && (body.includes("Image translation results available") || images > 0);
  return {ready, download, image_count: images, body: body.slice(0, 4000), url: location.href, placeholder: !ready && (body.includes("Loading") || body.includes("Translating"))};
})()`
	case "document":
		return `(() => {
  const visible = (el) => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el); return r.width > 0 && r.height > 0 && s.display !== "none" && s.visibility !== "hidden"; };
  const body = (document.body?.innerText || "").replace(/\s+/g, " ").trim();
  const download = Array.from(document.querySelectorAll("button")).some((el) => visible(el) && (el.innerText || "").trim() === "Download translation");
		const ready = download;
  return {ready, download, body: body.slice(0, 4000), url: location.href, placeholder: body.includes("Translating") || body.includes("Loading")};
})()`
	case "website-input":
		return `(() => { const el = document.querySelector("input[type=url]"); return {ready: Boolean(el), value: el ? el.value : "", body: (document.body?.innerText || "").slice(0, 2000), url: location.href}; })()`
	default:
		return `(() => ({ready: false, body: (document.body?.innerText || "").slice(0, 2000), url: location.href}))()`
	}
}

func waitGoogleState(ctx context.Context, session *cdp.PageSession, kind string, poll time.Duration, predicate func(googleTranslateState) bool) (googleTranslateState, error) {
	expression := googleStateExpression(kind)
	var last googleTranslateState
	for {
		if err := evaluateJSONValue(ctx, session, expression, "Google Translate state", &last); err != nil {
			return last, err
		}
		if predicate(last) {
			return last, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, commandErrorWithData("google_translate_timeout", "timeout", fmt.Sprintf("Google Translate %s did not reach its final state within the wait bound", kind), ExitTimeout, []string{"increase --wait", "cdp --browser-mode headed pages --json"}, last)
		case <-timer.C:
		}
	}
}

func (a *app) translateGoogleText(ctx context.Context, session *cdp.PageSession, request googleTranslateRequest, input googleTranslateInput, outDir string, result *googleTranslateResult) error {
	value := request.Text
	chunks := googleTranslateTextChunks(value, request.ChunkSize)
	translated := make([]string, 0, len(chunks))
	for index, chunk := range chunks {
		chunkCtx, cancel := context.WithTimeout(ctx, request.Wait)
		if _, err := session.Navigate(chunkCtx, chunkGoogleRoute(request.Source, request.Target)); err != nil {
			cancel()
			return commandError("google_translate_navigation_failed", "connection", fmt.Sprintf("navigate text chunk %d: %v", index+1, err), ExitConnection, nil)
		}
		if _, err := waitGoogleState(chunkCtx, session, "text", request.Poll, func(state googleTranslateState) bool {
			return !state.Ready && strings.Contains(state.Body, "Source text")
		}); err != nil {
			cancel()
			return err
		}
		var focused bool
		if err := evaluateJSONValue(chunkCtx, session, googleFocusExpression("textarea"), "focus Google Translate text input", &focused); err != nil || !focused {
			cancel()
			if err != nil {
				return err
			}
			return commandError("google_translate_ui_drift", "check_failed", "Google Translate text input was not focusable", ExitCheckFailed, []string{"cdp --browser-mode headed snapshot --json"})
		}
		params, _ := json.Marshal(map[string]any{"text": chunk})
		if _, err := session.Exec(chunkCtx, "Input.insertText", params); err != nil {
			cancel()
			return commandError("google_translate_input_failed", "connection", fmt.Sprintf("insert text chunk %d: %v", index+1, err), ExitConnection, []string{"cdp protocol exec Input.insertText --params '{\"text\":\"hello\"}' --json"})
		}
		state, err := waitGoogleState(chunkCtx, session, "text", request.Poll, func(state googleTranslateState) bool { return state.Ready && strings.TrimSpace(state.Text) != "" })
		cancel()
		if err != nil {
			return err
		}
		translated = append(translated, state.Text)
		result.Chunks = append(result.Chunks, googleTranslateChunk{Index: index + 1, SourceChars: len([]rune(chunk)), TranslatedChars: len([]rune(state.Text))})
	}
	outPath := request.Output
	if strings.TrimSpace(outPath) == "" {
		outPath = filepath.Join(outDir, "translation.txt")
	}
	outPath = nextGoogleTranslatePathForRequested(outPath, ".txt")
	if err := artifacts.WriteOwnerOnlyFileAtomic(outPath, []byte(strings.Join(translated, "\n\n"))); err != nil {
		return commandError("artifact_write_failed", "io", fmt.Sprintf("write translated text: %v", err), ExitInternal, nil)
	}
	result.Artifacts.Output = outPath
	result.Input.Characters = len([]rune(value))
	result.Input.Kind = input.Kind
	return nil
}

func chunkGoogleRoute(source, target string) string {
	return googleTranslateRoute(source, target, "translate")
}

func (a *app) translateGoogleWebsite(ctx context.Context, client cdp.CommandClient, session *cdp.PageSession, request googleTranslateRequest, outDir string, result *googleTranslateResult) (*googleTranslateTarget, error) {
	baseline, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return nil, commandError("target_list_failed", "connection", fmt.Sprintf("list targets before website translation: %v", err), ExitConnection, nil)
	}
	inputCtx, cancel := context.WithTimeout(ctx, request.Wait)
	defer cancel()
	if _, err := session.Navigate(inputCtx, googleTranslateRoute(request.Source, request.Target, "websites")); err != nil {
		return nil, commandError("google_translate_navigation_failed", "connection", fmt.Sprintf("navigate website mode: %v", err), ExitConnection, nil)
	}
	if _, err := waitGoogleState(inputCtx, session, "website-input", request.Poll, func(state googleTranslateState) bool { return state.Ready }); err != nil {
		return nil, err
	}
	var focused bool
	if err := evaluateJSONValue(inputCtx, session, googleFocusExpression("input[type=url]"), "focus Google Translate website input", &focused); err != nil || !focused {
		if err != nil {
			return nil, err
		}
		return nil, commandError("google_translate_ui_drift", "check_failed", "Google Translate website input was not focusable", ExitCheckFailed, nil)
	}
	params, _ := json.Marshal(map[string]any{"text": request.URL})
	if _, err := session.Exec(inputCtx, "Input.insertText", params); err != nil {
		return nil, commandError("google_translate_input_failed", "connection", fmt.Sprintf("insert website URL: %v", err), ExitConnection, nil)
	}
	var clicked bool
	if err := evaluateJSONValue(inputCtx, session, googleVisibleButtonClickExpression("Translate website", ""), "click Google Translate website", &clicked); err != nil || !clicked {
		if err != nil {
			return nil, err
		}
		return nil, commandError("google_translate_ui_drift", "check_failed", "Google Translate website submit button was not visible", ExitCheckFailed, nil)
	}
	child, err := waitGoogleWebsiteTarget(inputCtx, client, baseline, session.TargetID, request.Poll)
	if err != nil {
		return nil, err
	}
	childSession, err := cdp.AttachToTargetWithClient(inputCtx, client, child.Target.TargetID, func(context.Context) error { return nil })
	if err != nil {
		return nil, commandError("website_target_attach_failed", "connection", fmt.Sprintf("attach translated website target %s: %v", child.Target.TargetID, err), ExitConnection, nil)
	}
	child.Session = childSession
	state, err := waitGoogleWebsiteBody(inputCtx, childSession, request.Poll)
	if err != nil {
		_ = childSession.Close(context.Background())
		return nil, err
	}
	outPath := request.Output
	if strings.TrimSpace(outPath) == "" {
		outPath = filepath.Join(outDir, "website.txt")
	}
	outPath = nextGoogleTranslatePathForRequested(outPath, ".txt")
	if err := artifacts.WriteOwnerOnlyFileAtomic(outPath, []byte(state.Text)); err != nil {
		_ = childSession.Close(context.Background())
		return nil, commandError("artifact_write_failed", "io", fmt.Sprintf("write translated website: %v", err), ExitInternal, nil)
	}
	result.Artifacts.Output = outPath
	result.Input.Characters = len([]rune(state.Text))
	return child, nil
}

func waitGoogleWebsiteTarget(ctx context.Context, client cdp.CommandClient, baseline []cdp.TargetInfo, mainTargetID string, poll time.Duration) (*googleTranslateTarget, error) {
	known := map[string]bool{}
	for _, target := range baseline {
		known[target.TargetID] = true
	}
	for {
		targets, err := cdp.ListTargetsWithClient(ctx, client)
		if err != nil {
			return nil, commandError("target_list_failed", "connection", fmt.Sprintf("list website translation targets: %v", err), ExitConnection, nil)
		}
		for _, target := range targets {
			if target.Type == "page" && target.TargetID != mainTargetID && !known[target.TargetID] && strings.Contains(target.URL, "translate.goog") {
				return &googleTranslateTarget{Target: target}, nil
			}
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, commandError("website_target_timeout", "timeout", "Google Translate did not create a translate.goog result target before --wait expired", ExitTimeout, []string{"increase --wait", "cdp --browser-mode headed pages --json"})
		case <-timer.C:
		}
	}
}

func waitGoogleWebsiteBody(ctx context.Context, session *cdp.PageSession, poll time.Duration) (googleTranslateState, error) {
	expression := fmt.Sprintf(`(() => { const raw = document.body?.innerText || ""; const text = raw.replace(/\n{3,}/g, "\n\n").trim(); return {ready: text.length >= 20 && !text.includes("Loading...") && !text.includes("Something went wrong"), text: text.slice(0, %d), body: text.slice(0, 4000), url: location.href}; })()`, googleTranslateWebsiteMaxBytes)
	return waitGoogleExpression(ctx, session, expression, "website", poll, func(state googleTranslateState) bool { return state.Ready })
}

func waitGoogleExpression(ctx context.Context, session *cdp.PageSession, expression, label string, poll time.Duration, predicate func(googleTranslateState) bool) (googleTranslateState, error) {
	var state googleTranslateState
	for {
		if err := evaluateJSONValue(ctx, session, expression, "Google Translate "+label, &state); err != nil {
			return state, err
		}
		if predicate(state) {
			return state, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return state, commandErrorWithData("google_translate_timeout", "timeout", fmt.Sprintf("Google Translate %s did not reach its final state within the wait bound", label), ExitTimeout, []string{"increase --wait", "cdp --browser-mode headed pages --json"}, state)
		case <-timer.C:
		}
	}
}

func (a *app) translateGoogleDocument(ctx context.Context, session *cdp.PageSession, request googleTranslateRequest, input googleTranslateInput, outDir string, result *googleTranslateResult) error {
	if request.Output != "" {
		ext := strings.ToLower(filepath.Ext(request.Output))
		want := strings.ToLower(filepath.Ext(input.Path))
		if ext != "" && ext != want {
			return commandError("invalid_output_extension", "usage", fmt.Sprintf("document output extension %s must match the input extension %s", ext, want), ExitUsage, nil)
		}
	}
	if _, err := session.Navigate(ctx, googleTranslateRoute(request.Source, request.Target, "docs")); err != nil {
		return commandError("google_translate_navigation_failed", "connection", fmt.Sprintf("navigate document mode: %v", err), ExitConnection, nil)
	}
	operationCtx, cancel := context.WithTimeout(ctx, request.Wait)
	defer cancel()
	selector := googleDocumentFileSelector(input.Path)
	if _, err := waitGoogleExpression(operationCtx, session, googleFileInputExpression(selector), "document upload surface", request.Poll, func(state googleTranslateState) bool { return state.Ready }); err != nil {
		return err
	}
	if err := googleSetFileInput(operationCtx, session, selector, input.Path); err != nil {
		return err
	}
	if _, err := waitGoogleExpression(operationCtx, session, googleDocumentUploadedExpression(filepath.Base(input.Path)), "document upload", request.Poll, func(state googleTranslateState) bool { return state.Ready }); err != nil {
		return err
	}
	var clicked bool
	if err := evaluateJSONValue(operationCtx, session, googleVisibleButtonClickExpression("", "Translate"), "click Google Translate document", &clicked); err != nil || !clicked {
		if err != nil {
			return err
		}
		return commandError("google_translate_ui_drift", "check_failed", "Google Translate document Translate button was not visible", ExitCheckFailed, nil)
	}
	documentState, err := waitGoogleState(operationCtx, session, "document", request.Poll, func(state googleTranslateState) bool { return state.Ready && state.Download })
	if err != nil {
		return err
	}
	if documentState.Placeholder {
		result.Warnings = append(result.Warnings, "Google exposed the document download control while its visible status still said Translating; the downloaded artifact was signature-validated.")
	}
	downloaded, cleanupDownload, err := a.googleDownload(operationCtx, session, func() error {
		var ok bool
		if evalErr := evaluateJSONValue(operationCtx, session, googleVisibleButtonClickExpression("", "Download translation"), "click Google Translate document download", &ok); evalErr != nil {
			return evalErr
		}
		if !ok {
			return commandError("google_translate_ui_drift", "check_failed", "Google Translate document download button was not visible after completion", ExitCheckFailed, nil)
		}
		return nil
	})
	if err != nil {
		return err
	}
	defer cleanupDownload()
	if err := validateGoogleDownloadedFile(downloaded, "document"); err != nil {
		return err
	}
	outputName := googleDocumentOutputName(input.Path)
	outPath := request.Output
	if strings.TrimSpace(outPath) == "" {
		outPath = filepath.Join(outDir, outputName)
	}
	outPath = nextGoogleTranslatePathForRequested(outPath, filepath.Ext(input.Path))
	if err := copyGoogleFileNoOverwrite(downloaded, outPath); err != nil {
		return commandError("artifact_write_failed", "io", fmt.Sprintf("write translated document: %v", err), ExitInternal, nil)
	}
	result.Artifacts.Output = outPath
	result.Input.Kind = input.Kind
	return nil
}

func googleDocumentUploadedExpression(filename string) string {
	filenameJSON, _ := json.Marshal(filename)
	return fmt.Sprintf(`(() => { const body = (document.body?.innerText || "").replace(/\s+/g, " ").trim(); const filename = %s; return {ready: body.includes(filename) && body.includes("Translate"), body: body.slice(0, 4000), url: location.href}; })()`, string(filenameJSON))
}

func googleFileInputExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => ({ready: Boolean(document.querySelector(%s)), body: (document.body?.innerText || "").slice(0, 2000), url: location.href}))()`, string(selectorJSON))
}

func googleDocumentFileSelector(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" {
		return `input[type=file][accept*=".pdf"]`
	}
	if ext == ".docx" {
		return `input[type=file][accept*=".docx"]`
	}
	if ext == ".pptx" {
		return `input[type=file][accept*=".pptx"]`
	}
	if ext == ".xlsx" {
		return `input[type=file][accept*=".xlsx"]`
	}
	return `input[type=file]:not([accept*="image"])`
}

func googleSetFileInput(ctx context.Context, session *cdp.PageSession, selector, path string) error {
	var root struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if err := execSessionJSON(ctx, session, "DOM.getDocument", map[string]any{"depth": 0, "pierce": true}, &root); err != nil {
		return commandError("google_translate_dom_failed", "connection", fmt.Sprintf("get Google Translate DOM: %v", err), ExitConnection, nil)
	}
	if root.Root.NodeID == 0 {
		return commandError("google_translate_dom_failed", "check_failed", "Google Translate returned no DOM root", ExitCheckFailed, nil)
	}
	var match struct {
		NodeID int `json:"nodeId"`
	}
	if err := execSessionJSON(ctx, session, "DOM.querySelector", map[string]any{"nodeId": root.Root.NodeID, "selector": selector}, &match); err != nil {
		return commandError("google_translate_file_input_failed", "connection", fmt.Sprintf("find Google Translate file input: %v", err), ExitConnection, nil)
	}
	if match.NodeID == 0 {
		return commandError("google_translate_ui_drift", "check_failed", fmt.Sprintf("Google Translate file input selector %s did not resolve", selector), ExitCheckFailed, []string{"cdp --browser-mode headed snapshot --json"})
	}
	params, _ := json.Marshal(map[string]any{"nodeId": match.NodeID, "files": []string{path}})
	if _, err := session.Exec(ctx, "DOM.setFileInputFiles", params); err != nil {
		return commandError("google_translate_file_input_failed", "connection", fmt.Sprintf("set Google Translate file input: %v", err), ExitConnection, nil)
	}
	return nil
}

func (a *app) googleDownload(ctx context.Context, session *cdp.PageSession, click func() error) (string, func(), error) {
	downloadDir, err := os.MkdirTemp("", "cdp-google-translate-download-")
	if err != nil {
		return "", nil, commandError("download_dir_unavailable", "io", fmt.Sprintf("create private download directory: %v", err), ExitInternal, nil)
	}
	keepDownload := false
	defer func() {
		if !keepDownload {
			_ = os.RemoveAll(downloadDir)
		}
	}()
	eventClient, closeClient, err := a.browserEventCDPClient(ctx)
	if err != nil {
		return "", nil, commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
	}
	defer closeClient(context.Background())
	opts := downloadWaitOptions{
		Criteria:                  downloadWaitCriteria{State: "completed"},
		DownloadDir:               downloadDir,
		Redact:                    "safe",
		FinalizeSuggestedFilename: true,
	}
	if err := os.MkdirAll(opts.DownloadDir, 0o700); err != nil {
		return "", nil, commandError("download_dir_unavailable", "io", fmt.Sprintf("create download directory: %v", err), ExitInternal, nil)
	}
	teardown, err := setupDownloadWait(ctx, eventClient, opts)
	if err != nil {
		return "", nil, commandError("download_wait_setup_failed", "connection", fmt.Sprintf("enable Chrome download events: %v", err), ExitConnection, nil)
	}
	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	type downloadResult struct {
		observation downloadWaitObservation
		err         error
	}
	resultCh := make(chan downloadResult, 1)
	go func() {
		observation, waitErr := collectDownloadEvent(waitCtx, eventClient, opts)
		resultCh <- downloadResult{observation: observation, err: waitErr}
	}()
	clickErr := click()
	if clickErr != nil {
		cancelWait()
		<-resultCh
		teardownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = teardown(teardownCtx)
		cancel()
		return "", nil, clickErr
	}
	observed := <-resultCh
	teardownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = teardown(teardownCtx)
	cancel()
	if observed.err != nil {
		if errors.Is(observed.err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", nil, commandError("google_translate_download_timeout", "timeout", "Google Translate did not complete its download before --wait expired", ExitTimeout, []string{"increase --wait"})
		}
		return "", nil, commandError("google_translate_download_failed", "check_failed", observed.err.Error(), ExitCheckFailed, []string{"retry with a larger --wait", "cdp wait download --state completed --json"})
	}
	path := ""
	if observed.observation.Progress != nil {
		path = observed.observation.Progress.FilePath
	}
	if path == "" && observed.observation.LastEvent != nil {
		path = observed.observation.LastEvent.FilePath
	}
	if path == "" {
		return "", nil, commandError("google_translate_download_missing", "check_failed", "Chrome reported a completed download without a local file path", ExitCheckFailed, nil)
	}
	if _, err := os.Stat(path); err != nil {
		entries, readErr := os.ReadDir(downloadDir)
		if readErr == nil {
			for _, entry := range entries {
				if entry.IsDir() || strings.HasSuffix(entry.Name(), ".crdownload") {
					continue
				}
				candidate := filepath.Join(downloadDir, entry.Name())
				if info, statErr := entry.Info(); statErr == nil && info.Size() > 0 {
					path = candidate
					break
				}
			}
		}
	}
	if _, err := os.Stat(path); err != nil {
		return "", nil, commandError("google_translate_download_missing", "check_failed", fmt.Sprintf("completed Google Translate download is not visible: %v", err), ExitCheckFailed, nil)
	}
	keepDownload = true
	return path, func() { _ = os.RemoveAll(downloadDir) }, nil
}

func validateGoogleDownloadedFile(path, kind string) error {
	file, err := os.Open(path)
	if err != nil {
		return commandError("google_translate_download_invalid", "check_failed", fmt.Sprintf("open downloaded %s: %v", kind, err), ExitCheckFailed, nil)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return commandError("google_translate_download_invalid", "check_failed", fmt.Sprintf("downloaded %s is empty or not a regular file", kind), ExitCheckFailed, nil)
	}
	header := make([]byte, 8)
	n, err := io.ReadFull(file, header)
	if err != nil && !(err == io.ErrUnexpectedEOF && n > 0) {
		return commandError("google_translate_download_invalid", "check_failed", fmt.Sprintf("read downloaded %s: %v", kind, err), ExitCheckFailed, nil)
	}
	valid := false
	switch kind {
	case "image":
		valid = n >= 8 && string(header[:8]) == "\x89PNG\r\n\x1a\n"
	case "document":
		valid = (n >= 4 && string(header[:4]) == "%PDF") || (n >= 2 && string(header[:2]) == "PK")
	default:
		valid = n > 0
	}
	if !valid {
		return commandError("google_translate_download_invalid", "check_failed", fmt.Sprintf("downloaded %s has an unexpected file signature", kind), ExitCheckFailed, nil)
	}
	return nil
}

func copyGoogleFileNoOverwrite(source, destination string) error {
	if source == destination {
		return errors.New("source and destination are the same file")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = output.Close()
		if remove {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func googleDocumentOutputName(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" {
		return "translated-document.pdf"
	}
	if ext == ".docx" {
		return "translated-document.docx"
	}
	if ext == ".pptx" {
		return "translated-document.pptx"
	}
	if ext == ".xlsx" {
		return "translated-document.xlsx"
	}
	return "translated-document.bin"
}

func nextGoogleTranslatePathForRequested(path, defaultExtension string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if filepath.Ext(absolute) == "" && defaultExtension != "" {
		if !strings.HasPrefix(defaultExtension, ".") {
			defaultExtension = "." + defaultExtension
		}
		absolute += defaultExtension
	}
	return nextGoogleTranslatePath(filepath.Dir(absolute), filepath.Base(absolute))
}

func burstGooglePDF(ctx context.Context, inputPath, outDir string) ([]string, error) {
	tool, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil, commandError("dependency_missing", "usage", "pdftoppm is required to burst scanned PDFs into PNG pages", ExitUsage, []string{"brew install poppler", "sudo apt-get install poppler-utils"})
	}
	burstDir := nextGoogleTranslateDirectory(filepath.Join(outDir, "source-pages"))
	if err := os.MkdirAll(burstDir, 0o700); err != nil {
		return nil, commandError("burst_output_unavailable", "io", fmt.Sprintf("create PDF burst directory: %v", err), ExitInternal, nil)
	}
	prefix := filepath.Join(burstDir, "page")
	command := exec.CommandContext(ctx, tool, "-png", "-r", "150", inputPath, prefix)
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, commandError("pdf_burst_failed", "extraction", fmt.Sprintf("burst scanned PDF with pdftoppm: %v: %s", err, strings.TrimSpace(stderr.String())), ExitCheckFailed, []string{"verify the PDF opens normally", "brew install poppler"})
	}
	paths, err := filepath.Glob(prefix + "-*.png")
	if err != nil || len(paths) == 0 {
		return nil, commandError("pdf_burst_empty", "extraction", "pdftoppm produced no PNG pages", ExitCheckFailed, nil)
	}
	sort.Slice(paths, func(i, j int) bool { return googlePageNumber(paths[i]) < googlePageNumber(paths[j]) })
	return paths, nil
}

func googlePageNumber(path string) int {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	index := strings.LastIndex(base, "-")
	if index < 0 {
		return 0
	}
	number, _ := strconv.Atoi(base[index+1:])
	return number
}

func (a *app) translateGoogleImages(ctx context.Context, session *cdp.PageSession, request googleTranslateRequest, input googleTranslateInput, outDir string, result *googleTranslateResult) error {
	paths := []string{input.Path}
	isPDF := strings.EqualFold(filepath.Ext(input.Path), ".pdf")
	requestedExtension := strings.ToLower(filepath.Ext(request.Output))
	if isPDF && requestedExtension != "" && requestedExtension != ".pdf" {
		return commandError("invalid_output_extension", "usage", "scanned PDF image translation must produce a .pdf output", ExitUsage, nil)
	}
	if !isPDF && requestedExtension != "" && requestedExtension != ".png" && requestedExtension != ".pdf" {
		return commandError("invalid_output_extension", "usage", "image translation output must be .png or .pdf", ExitUsage, nil)
	}
	if isPDF {
		var err error
		paths, err = burstGooglePDF(ctx, input.Path, outDir)
		if err != nil {
			return err
		}
	}
	translatedPaths := make([]string, 0, len(paths))
	for index, sourcePath := range paths {
		pageCtx, cancel := context.WithTimeout(ctx, request.Wait)
		started := time.Now()
		if _, err := session.Navigate(pageCtx, googleTranslateRoute(request.Source, request.Target, "images")); err != nil {
			cancel()
			return commandError("google_translate_navigation_failed", "connection", fmt.Sprintf("navigate image page %d: %v", index+1, err), ExitConnection, nil)
		}
		if _, err := waitGoogleExpression(pageCtx, session, googleFileInputExpression(`input[type=file][accept*="image"]`), "image upload surface", request.Poll, func(state googleTranslateState) bool { return state.Ready }); err != nil {
			cancel()
			return err
		}
		if err := googleSetFileInput(pageCtx, session, `input[type=file][accept*="image"]`, sourcePath); err != nil {
			cancel()
			return err
		}
		state, err := waitGoogleState(pageCtx, session, "image", request.Poll, func(state googleTranslateState) bool { return state.Ready && state.Download })
		if err != nil {
			cancel()
			return err
		}
		downloaded, cleanupDownload, err := a.googleDownload(pageCtx, session, func() error {
			var clicked bool
			if evalErr := evaluateJSONValue(pageCtx, session, googleVisibleButtonClickExpression("Download translation", ""), "click Google Translate image download", &clicked); evalErr != nil {
				return evalErr
			}
			if !clicked {
				return commandError("google_translate_ui_drift", "check_failed", "Google Translate image download button was not visible after the final result state", ExitCheckFailed, []string{"increase --wait", "cdp --browser-mode headed snapshot --json"})
			}
			return nil
		})
		if err != nil {
			cancel()
			return err
		}
		if cleanupDownload != nil {
			defer cleanupDownload()
		}
		if err := validateGoogleDownloadedFile(downloaded, "image"); err != nil {
			cancel()
			return err
		}
		outputPath := nextGoogleTranslatePath(outDir, fmt.Sprintf("translated-page-%03d.png", index+1))
		if !isPDF && request.Output != "" && requestedExtension != ".pdf" {
			outputPath = nextGoogleTranslatePathForRequested(request.Output, ".png")
		}
		if err := copyGoogleFileNoOverwrite(downloaded, outputPath); err != nil {
			cancel()
			return commandError("artifact_write_failed", "io", fmt.Sprintf("write translated image page %d: %v", index+1, err), ExitInternal, nil)
		}
		info, _ := os.Stat(outputPath)
		bytes := int64(0)
		if info != nil {
			bytes = info.Size()
		}
		result.Pages = append(result.Pages, googleTranslatePage{Index: index + 1, SourcePath: sourcePath, TranslatedPath: outputPath, WaitMS: time.Since(started).Milliseconds(), DownloadBytes: bytes, Validated: true})
		translatedPaths = append(translatedPaths, outputPath)
		_ = state
		cancel()
	}
	if isPDF || strings.EqualFold(filepath.Ext(request.Output), ".pdf") {
		outputPath := request.Output
		if strings.TrimSpace(outputPath) == "" {
			outputPath = filepath.Join(outDir, "translated-document.pdf")
		}
		outputPath = nextGoogleTranslatePathForRequested(outputPath, ".pdf")
		if err := assembleGooglePNGPDF(translatedPaths, outputPath); err != nil {
			return commandError("pdf_assembly_failed", "io", fmt.Sprintf("assemble translated PNG pages: %v", err), ExitInternal, nil)
		}
		result.Artifacts.Output = outputPath
	} else {
		result.Artifacts.Output = translatedPaths[0]
	}
	result.Input.Kind = input.Kind
	return nil
}
