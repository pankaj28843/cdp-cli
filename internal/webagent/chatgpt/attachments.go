package chatgpt

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AttachmentBatchSchemaVersion          = "chatgpt-attachment-batch/v1"
	AttachmentManifestSchemaVersion       = "chatgpt-attachment-manifest/v1"
	attachmentManifestFileName            = "chatgpt-attachments-manifest.json"
	attachmentBatchComplete               = "complete"
	attachmentBatchPartial                = "partial"
	attachmentItemSuccess                 = "success"
	attachmentItemFailed                  = "failed"
	maxAttachmentBatchBytes         int64 = 256 << 20
)

var errAttachmentManifestExists = errors.New(
	"attachment manifest destination already exists",
)

type AttachmentExportItem struct {
	Ordinal   int    `json:"ordinal"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	FileName  string `json:"file_name,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	MIMEType  string `json:"mime_type,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

type AttachmentBatchData struct {
	SchemaVersion   string                 `json:"schema_version"`
	Status          string                 `json:"status"`
	ManifestPath    string                 `json:"manifest_path"`
	DiscoveredCount int                    `json:"discovered_count"`
	SucceededCount  int                    `json:"succeeded_count"`
	FailedCount     int                    `json:"failed_count"`
	TotalBytes      int64                  `json:"total_bytes"`
	Items           []AttachmentExportItem `json:"items"`
	ReadMode        string                 `json:"read_mode,omitempty"`
}

type AttachmentManifest struct {
	SchemaVersion   string                 `json:"schema_version"`
	Status          string                 `json:"status"`
	DiscoveredCount int                    `json:"discovered_count"`
	SucceededCount  int                    `json:"succeeded_count"`
	FailedCount     int                    `json:"failed_count"`
	TotalBytes      int64                  `json:"total_bytes"`
	Items           []AttachmentExportItem `json:"items"`
}

type attachmentBatchLimits struct {
	MaxItems      int
	MaxItemBytes  int64
	MaxTotalBytes int64
}

type attachmentPayload struct {
	Content     []byte
	ContentType string
	FileName    string
}

type verifiedAttachmentPayload struct {
	Content  []byte
	MIMEType string
	FileName string
	Width    int
	Height   int
	SHA256   string
}

type attachmentItemFailure struct {
	Code string
}

type attachmentResolver interface {
	Resolve(
		context.Context,
		ConversationAttachment,
		int64,
	) (attachmentPayload, *attachmentItemFailure)
}

type attachmentResolverFunc func(
	context.Context,
	ConversationAttachment,
	int64,
) (attachmentPayload, *attachmentItemFailure)

type AttachmentBatchConfig struct {
	ReadConfig
	OutputDir string
}

func DownloadAttachments(
	ctx context.Context,
	config AttachmentBatchConfig,
	conversationID string,
) webagent.Result {
	runID := webagent.NewRunID()
	initial := AttachmentBatchData{
		SchemaVersion: AttachmentBatchSchemaVersion,
		Status:        attachmentBatchPartial,
		Items:         []AttachmentExportItem{},
		ReadMode:      "not_started",
	}
	conversationID = strings.TrimSpace(conversationID)
	if !conversationIDPattern.MatchString(conversationID) {
		return attachmentBatchFailureResult(
			runID,
			config.BuildCommit,
			webagent.StagePlanned,
			"not_started",
			"chatgpt_invalid_conversation_id",
			"usage",
			"ChatGPT conversation id contains unsupported characters",
			initial,
		)
	}
	if strings.TrimSpace(config.OutputDir) == "" {
		return attachmentBatchFailureResult(
			runID,
			config.BuildCommit,
			webagent.StagePlanned,
			"not_started",
			"chatgpt_attachment_output_required",
			"usage",
			"ChatGPT attachment export requires an explicit output directory",
			initial,
		)
	}
	resolvedOutputDir, err := prepareAttachmentOutputDir(config.OutputDir)
	if err != nil {
		return attachmentBatchFailureResult(
			runID,
			config.BuildCommit,
			webagent.StagePlanned,
			"not_started",
			"chatgpt_attachment_output_unsafe",
			"usage",
			"ChatGPT attachment output directory is unsafe or unavailable",
			initial,
		)
	}
	if err := requireAttachmentManifestAbsent(resolvedOutputDir); err != nil {
		code := "chatgpt_attachment_output_unsafe"
		errClass := "filesystem"
		message := "ChatGPT attachment output directory could not be inspected safely"
		if errors.Is(err, errAttachmentManifestExists) {
			code = "chatgpt_attachment_destination_exists"
			message = "ChatGPT attachment manifest already exists; no paths were overwritten"
		}
		return attachmentBatchFailureResult(
			runID,
			config.BuildCommit,
			webagent.StagePlanned,
			"not_started",
			code,
			errClass,
			message,
			initial,
		)
	}
	config.OutputDir = resolvedOutputDir
	directConfig := config.ReadConfig
	directConfig.BrowserConfig = nil
	directConfig.BrowserFallback = nil
	template, readFailure := loadFreshReadTemplate(ctx, directConfig)
	if readFailure != nil {
		if attachmentBrowserFallbackEligible(config.ReadConfig, readFailure) {
			return downloadAttachmentsViaBrowser(
				ctx,
				config,
				runID,
				conversationID,
				initial,
			)
		}
		return attachmentBatchFailureResult(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			"not_started",
			readFailure.code,
			readFailure.errClass,
			readFailure.message,
			initial,
		)
	}
	detail, readFailure := fetchConversationDetail(
		ctx,
		directConfig,
		template,
		conversationID,
	)
	if readFailure != nil {
		initial.ReadMode = detail.ReadMode
		if attachmentBrowserFallbackEligible(config.ReadConfig, readFailure) {
			return downloadAttachmentsViaBrowser(
				ctx,
				config,
				runID,
				conversationID,
				initial,
			)
		}
		return attachmentBatchFailureResult(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			detail.ReadMode,
			readFailure.code,
			readFailure.errClass,
			readFailure.message,
			initial,
		)
	}
	if !terminalConversationCompletion(detail.CompletionState) {
		initial.ReadMode = detail.ReadMode
		return attachmentBatchFailureResult(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			detail.ReadMode,
			"chatgpt_attachment_conversation_incomplete",
			"provider",
			"ChatGPT attachment export requires one terminal canonical answer",
			initial,
		)
	}
	truncated, _ := detail.Metadata["attachments_truncated"].(bool)
	data, err := exportAttachmentCandidates(
		ctx,
		config.OutputDir,
		detail.Attachments,
		truncated,
		directAttachmentResolver{
			config:         directConfig,
			template:       template,
			conversationID: conversationID,
		},
		defaultAttachmentBatchLimits(),
	)
	if err != nil {
		initial.ReadMode = detail.ReadMode
		return attachmentBatchFailureResult(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			detail.ReadMode,
			"chatgpt_attachment_publication_failed",
			"filesystem",
			"ChatGPT attachment batch could not be published to the explicit output directory",
			initial,
		)
	}
	data.ReadMode = detail.ReadMode
	result := operationSuccess(
		runID,
		config.BuildCommit,
		webagent.OperationAttachmentsDownload,
		webagent.StageObserveTerminal,
		detail.ReadMode,
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		data,
		nil,
	)
	result.State = webagent.StateTerminal
	result.Evidence.BrowserMode = "none"
	return result
}

func attachmentBrowserFallbackEligible(
	config ReadConfig,
	failure *readFailure,
) bool {
	if failure == nil ||
		(config.BrowserConfig == nil && config.BrowserFallback == nil) ||
		failure.errClass == "usage" ||
		failure.errClass == "rate_limit" ||
		failure.code == "chatgpt_rate_limited" {
		return false
	}
	return failure.errClass == "auth" ||
		failure.errClass == "connection" ||
		failure.code == "chatgpt_http_failed" ||
		failure.code == "chatgpt_browser_context_required"
}

func downloadAttachmentsViaBrowser(
	ctx context.Context,
	config AttachmentBatchConfig,
	runID string,
	conversationID string,
	initial AttachmentBatchData,
) webagent.Result {
	browserConfig, failure := resolveBrowserFallback(ctx, config.ReadConfig)
	if failure != nil {
		return attachmentBatchFailureResult(
			runID,
			config.BuildCommit,
			webagent.StagePlanned,
			"not_started",
			failure.code,
			failure.errClass,
			failure.message,
			initial,
		)
	}
	result := runOwned(
		ctx,
		*browserConfig,
		runID,
		webagent.OperationAttachmentsDownload,
		"",
		"about:blank",
		"browser_context_stable_http",
		initial,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			browserFailure := func(
				stage webagent.Stage,
				code string,
				errClass string,
				message string,
				data AttachmentBatchData,
			) webagent.Result {
				return operationFailure(
					runID,
					config.BuildCommit,
					webagent.OperationAttachmentsDownload,
					stage,
					data.ReadMode,
					target,
					pending,
					code,
					errClass,
					message,
					data,
					[]string{},
				)
			}
			template, readFailure := prepareBrowserRead(
				ctx,
				*browserConfig,
				config.Store,
				lease,
			)
			if readFailure != nil {
				return browserFailure(
					webagent.StageAttached,
					readFailure.code,
					readFailure.errClass,
					readFailure.message,
					initial,
				)
			}
			if readFailure = commitBrowserReadPreparation(
				ctx,
				lease,
			); readFailure != nil {
				return browserFailure(
					webagent.StageAttached,
					readFailure.code,
					readFailure.errClass,
					readFailure.message,
					initial,
				)
			}
			detailPath := "/backend-api/conversation/" +
				url.PathEscape(conversationID)
			response, readFailure := browserFetch(
				ctx,
				lease.Session(),
				template,
				Origin+detailPath,
				ConversationDetailRoute,
			)
			if readFailure != nil && readFailure.errClass == "auth" {
				refreshed, refreshFailure :=
					refreshBrowserConversationReadTemplate(
						ctx,
						*browserConfig,
						config.Store,
						lease,
						template,
						conversationID,
					)
				if refreshFailure != nil {
					return browserFailure(
						webagent.StageObserveTerminal,
						refreshFailure.code,
						refreshFailure.errClass,
						refreshFailure.message,
						initial,
					)
				}
				template = refreshed
				response, readFailure = browserFetch(
					ctx,
					lease.Session(),
					template,
					Origin+detailPath,
					ConversationDetailRoute,
				)
			}
			if readFailure != nil {
				return browserFailure(
					webagent.StageObserveTerminal,
					readFailure.code,
					readFailure.errClass,
					readFailure.message,
					initial,
				)
			}
			var payload map[string]any
			if err := decodeBoundedJSON(
				strings.NewReader(response.Body),
				&payload,
			); err != nil {
				return browserFailure(
					webagent.StageObserveTerminal,
					"chatgpt_invalid_detail_response",
					"provider",
					"ChatGPT attachment detail returned invalid bounded JSON",
					initial,
				)
			}
			detail := newConversationDetailData(
				conversationID,
				browserReadMode,
				"headed_browser_fetch",
			)
			detail, readFailure = parseConversationDetailPayload(
				detail,
				payload,
				response.StatusCode,
			)
			if readFailure != nil {
				return browserFailure(
					webagent.StageObserveTerminal,
					readFailure.code,
					readFailure.errClass,
					readFailure.message,
					initial,
				)
			}
			if !terminalConversationCompletion(detail.CompletionState) {
				return browserFailure(
					webagent.StageObserveTerminal,
					"chatgpt_attachment_conversation_incomplete",
					"provider",
					"ChatGPT attachment export requires one terminal canonical answer",
					initial,
				)
			}
			truncated, _ := detail.Metadata["attachments_truncated"].(bool)
			data, err := exportAttachmentCandidates(
				ctx,
				config.OutputDir,
				detail.Attachments,
				truncated,
				browserAttachmentResolver{
					session:        lease.Session(),
					template:       template,
					conversationID: conversationID,
				},
				defaultAttachmentBatchLimits(),
			)
			if err != nil {
				return browserFailure(
					webagent.StageObserveTerminal,
					"chatgpt_attachment_publication_failed",
					"filesystem",
					"ChatGPT attachment batch could not be published to the explicit output directory",
					initial,
				)
			}
			data.ReadMode = browserReadMode
			if err := lease.MarkTerminal(ctx); err != nil {
				return browserFailure(
					webagent.StageObserveTerminal,
					"chatgpt_attachment_terminal_state_failed",
					"internal",
					"ChatGPT attachment terminal state could not be persisted",
					data,
				)
			}
			result := operationSuccess(
				runID,
				config.BuildCommit,
				webagent.OperationAttachmentsDownload,
				webagent.StageObserveTerminal,
				browserReadMode,
				target,
				pending,
				data,
				[]string{},
			)
			result.State = webagent.StateTerminal
			return result
		},
	)
	return sanitizeAttachmentTargetEvidence(result)
}

func refreshBrowserConversationReadTemplate(
	ctx context.Context,
	config BrowserConfig,
	store *Store,
	lease *browserflow.Lease,
	current RequestTemplate,
	conversationID string,
) (RequestTemplate, *readFailure) {
	if store == nil || lease == nil ||
		!conversationIDPattern.MatchString(conversationID) {
		return RequestTemplate{}, internalReadFailure(
			"ChatGPT exact-conversation read refresh is unavailable",
		)
	}
	session := lease.Session()
	exactURL := Origin + "/c/" + url.PathEscape(conversationID)
	if _, err := session.Navigate(ctx, exactURL); err != nil {
		return RequestTemplate{}, &readFailure{
			code:     "chatgpt_browser_conversation_read_refresh_failed",
			errClass: "connection",
			message:  "ChatGPT exact-conversation read refresh could not navigate the owned target",
		}
	}
	observation, found, err := observeReadRequest(
		ctx,
		config.Client,
		session,
		defaultObservationAttempts,
		defaultObservationTimeout,
	)
	if err == nil && !found {
		if reloadErr := session.Reload(ctx, true); reloadErr != nil {
			return RequestTemplate{}, &readFailure{
				code:     "chatgpt_browser_conversation_read_refresh_failed",
				errClass: "connection",
				message:  "ChatGPT exact-conversation read refresh could not reload the owned target",
			}
		}
		observation, found, err = observeReadRequest(
			ctx,
			config.Client,
			session,
			defaultObservationAttempts,
			defaultObservationTimeout,
		)
	}
	if err != nil || !found {
		return RequestTemplate{}, &readFailure{
			code:     "chatgpt_browser_conversation_read_observation_failed",
			errClass: "connection",
			message:  "ChatGPT exact-conversation authenticated read request was not observed",
		}
	}
	template, _ := browserReadTemplate(
		nil,
		observation,
		true,
		current.Cookies,
		current.BrowserUserAgent,
		time.Now().UTC(),
	)
	if err := store.SaveTemplate(ctx, template); err != nil {
		return RequestTemplate{}, internalReadFailure(
			"ChatGPT exact-conversation read evidence could not be persisted",
		)
	}
	return template, nil
}

func sanitizeAttachmentTargetEvidence(result webagent.Result) webagent.Result {
	result.Evidence.Target = nil
	if result.Cleanup.Required {
		result.Cleanup.TargetID = ""
		result.Cleanup.IdentityOmitted = true
	}
	return result
}

func attachmentBatchFailureResult(
	runID string,
	buildCommit string,
	stage webagent.Stage,
	readMode string,
	code string,
	errClass string,
	message string,
	data AttachmentBatchData,
) webagent.Result {
	result := operationFailure(
		runID,
		buildCommit,
		webagent.OperationAttachmentsDownload,
		stage,
		readMode,
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		code,
		errClass,
		message,
		data,
		[]string{},
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func (resolve attachmentResolverFunc) Resolve(
	ctx context.Context,
	candidate ConversationAttachment,
	maxBytes int64,
) (attachmentPayload, *attachmentItemFailure) {
	return resolve(ctx, candidate, maxBytes)
}

func defaultAttachmentBatchLimits() attachmentBatchLimits {
	return attachmentBatchLimits{
		MaxItems:      maxConversationAttachments,
		MaxItemBytes:  maxArtifactBytes,
		MaxTotalBytes: maxAttachmentBatchBytes,
	}
}

func prepareAttachmentOutputDir(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("attachment output directory is required")
	}
	absolute, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve attachment output directory")
	}
	absolute = filepath.Clean(absolute)
	volume := filepath.VolumeName(absolute)
	remainder := strings.TrimPrefix(absolute, volume)
	components := strings.FieldsFunc(remainder, func(character rune) bool {
		return os.IsPathSeparator(uint8(character))
	})
	current := volume + string(os.PathSeparator)
	if volume == "" {
		current = string(os.PathSeparator)
	}
	for _, component := range components {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			if mkdirErr := os.Mkdir(current, 0o700); mkdirErr != nil &&
				!errors.Is(mkdirErr, fs.ErrExist) {
				return "", fmt.Errorf(
					"create attachment output directory component",
				)
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil {
			return "", fmt.Errorf(
				"inspect attachment output directory component",
			)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf(
				"attachment output directory contains a symlink component",
			)
		}
		if !info.IsDir() {
			return "", fmt.Errorf(
				"attachment output path component is not a directory",
			)
		}
	}
	return absolute, nil
}

func exportAttachmentCandidates(
	ctx context.Context,
	outputDir string,
	candidates []ConversationAttachment,
	truncated bool,
	resolver attachmentResolver,
	limits attachmentBatchLimits,
) (AttachmentBatchData, error) {
	if resolver == nil {
		return AttachmentBatchData{}, fmt.Errorf(
			"attachment resolver is unavailable",
		)
	}
	if limits.MaxItems < 1 ||
		limits.MaxItemBytes < 1 ||
		limits.MaxTotalBytes < 1 {
		return AttachmentBatchData{}, fmt.Errorf(
			"attachment export limits are invalid",
		)
	}
	resolvedDir, err := prepareAttachmentOutputDir(outputDir)
	if err != nil {
		return AttachmentBatchData{}, err
	}
	manifestPath := filepath.Join(resolvedDir, attachmentManifestFileName)
	if err := requireAttachmentManifestAbsent(resolvedDir); err != nil {
		return AttachmentBatchData{}, err
	}

	data := AttachmentBatchData{
		SchemaVersion: AttachmentBatchSchemaVersion,
		Status:        attachmentBatchComplete,
		ManifestPath:  manifestPath,
		Items:         []AttachmentExportItem{},
	}
	processCount := len(candidates)
	countBounded := false
	if processCount > limits.MaxItems {
		processCount = limits.MaxItems
		countBounded = true
	}
	data.DiscoveredCount = processCount
	usedNames := map[string]int{}
	kindOrdinals := map[string]int{}
	resolvedBytes := int64(0)
	for index := 0; index < processCount; index++ {
		candidate := candidates[index]
		kindOrdinals[candidate.Kind]++
		item := AttachmentExportItem{
			Ordinal: index + 1,
			Kind:    candidate.Kind,
			Status:  attachmentItemFailed,
		}
		fail := func(code string) {
			item.ErrorCode = code
			data.FailedCount++
			data.Status = attachmentBatchPartial
			data.Items = append(data.Items, item)
		}
		if ctx.Err() != nil {
			fail("chatgpt_attachment_export_canceled")
			continue
		}
		if candidate.SizeBytes > limits.MaxItemBytes {
			fail("chatgpt_attachment_item_bytes_exceeded")
			continue
		}
		if candidate.SizeBytes > 0 &&
			resolvedBytes+candidate.SizeBytes > limits.MaxTotalBytes {
			fail("chatgpt_attachment_total_bytes_exceeded")
			continue
		}
		payload, failure := resolver.Resolve(
			ctx,
			candidate,
			limits.MaxItemBytes,
		)
		if failure != nil {
			fail(stableAttachmentFailureCode(failure.Code))
			continue
		}
		payloadBytes := int64(len(payload.Content))
		resolvedBytes += payloadBytes
		if payloadBytes > limits.MaxItemBytes {
			fail("chatgpt_attachment_item_bytes_exceeded")
			continue
		}
		if resolvedBytes > limits.MaxTotalBytes {
			fail("chatgpt_attachment_total_bytes_exceeded")
			continue
		}
		verified, failure := verifyAttachmentPayload(candidate, payload)
		if failure != nil {
			fail(failure.Code)
			continue
		}
		name := attachmentOutputFileName(
			candidate,
			verified,
			kindOrdinals[candidate.Kind],
		)
		name = allocateAttachmentFileName(name, usedNames)
		item.FileName = name
		destination := filepath.Join(resolvedDir, name)
		if err := writeArtifactAtomic(destination, verified.Content, false); err != nil {
			if errors.Is(err, fs.ErrExist) {
				fail("chatgpt_attachment_destination_exists")
			} else {
				fail("chatgpt_attachment_write_failed")
			}
			continue
		}
		item.Status = attachmentItemSuccess
		item.Bytes = int64(len(verified.Content))
		item.SHA256 = verified.SHA256
		item.MIMEType = verified.MIMEType
		item.Width = verified.Width
		item.Height = verified.Height
		data.SucceededCount++
		data.TotalBytes += item.Bytes
		data.Items = append(data.Items, item)
	}
	if truncated || countBounded {
		data.Status = attachmentBatchPartial
		data.DiscoveredCount++
		data.FailedCount++
		data.Items = append(data.Items, AttachmentExportItem{
			Ordinal:   processCount + 1,
			Kind:      "unknown",
			Status:    attachmentItemFailed,
			ErrorCode: "chatgpt_attachment_count_exceeded",
		})
	}
	manifest := AttachmentManifest{
		SchemaVersion:   AttachmentManifestSchemaVersion,
		Status:          data.Status,
		DiscoveredCount: data.DiscoveredCount,
		SucceededCount:  data.SucceededCount,
		FailedCount:     data.FailedCount,
		TotalBytes:      data.TotalBytes,
		Items:           append([]AttachmentExportItem{}, data.Items...),
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return AttachmentBatchData{}, fmt.Errorf(
			"encode attachment manifest",
		)
	}
	encoded = append(encoded, '\n')
	if err := writeArtifactAtomic(manifestPath, encoded, false); err != nil {
		return AttachmentBatchData{}, fmt.Errorf(
			"publish attachment manifest: %w",
			err,
		)
	}
	return data, nil
}

func requireAttachmentManifestAbsent(outputDir string) error {
	manifestPath := filepath.Join(outputDir, attachmentManifestFileName)
	if _, err := os.Lstat(manifestPath); err == nil {
		return errAttachmentManifestExists
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect attachment manifest destination")
	}
	return nil
}

func stableAttachmentFailureCode(code string) string {
	code = strings.TrimSpace(code)
	if strings.HasPrefix(code, "chatgpt_attachment_") &&
		len(code) <= 128 {
		for _, character := range code {
			if unicode.IsLower(character) ||
				unicode.IsDigit(character) ||
				character == '_' {
				continue
			}
			return "chatgpt_attachment_fetch_failed"
		}
		return code
	}
	return "chatgpt_attachment_fetch_failed"
}

func verifyAttachmentPayload(
	candidate ConversationAttachment,
	payload attachmentPayload,
) (verifiedAttachmentPayload, *attachmentItemFailure) {
	if len(payload.Content) == 0 {
		return verifiedAttachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_content_missing",
		}
	}
	if candidate.SizeBytes > 0 &&
		candidate.SizeBytes != int64(len(payload.Content)) {
		return verifiedAttachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_size_mismatch",
		}
	}
	detected := normalizedAttachmentMIME(http.DetectContentType(payload.Content))
	declared := normalizedAttachmentMIME(candidate.MIMEType)
	responseType := normalizedAttachmentMIME(payload.ContentType)
	if strings.TrimSpace(payload.ContentType) != "" && responseType == "" {
		return verifiedAttachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_mime_mismatch",
		}
	}
	if candidate.Kind == "image" {
		if !strings.HasPrefix(detected, "image/") ||
			(declared != "" &&
				!compatibleAttachmentMIME(declared, detected)) ||
			(responseType != "" &&
				!compatibleAttachmentMIME(responseType, detected)) {
			return verifiedAttachmentPayload{}, &attachmentItemFailure{
				Code: "chatgpt_attachment_mime_mismatch",
			}
		}
	} else {
		if declared != "" && responseType != "" &&
			!compatibleAttachmentContentMIME(
				declared,
				responseType,
				payload.Content,
			) {
			return verifiedAttachmentPayload{}, &attachmentItemFailure{
				Code: "chatgpt_attachment_mime_mismatch",
			}
		}
		for _, expected := range []string{declared, responseType} {
			if expected != "" &&
				!compatibleAttachmentContentMIME(
					expected,
					detected,
					payload.Content,
				) {
				return verifiedAttachmentPayload{}, &attachmentItemFailure{
					Code: "chatgpt_attachment_mime_mismatch",
				}
			}
		}
	}
	verifiedMIME := detected
	if declared != "" && declared != "application/octet-stream" {
		verifiedMIME = declared
	} else if responseType != "" &&
		responseType != "application/octet-stream" {
		verifiedMIME = responseType
	}
	verified := verifiedAttachmentPayload{
		Content:  payload.Content,
		MIMEType: verifiedMIME,
		FileName: payload.FileName,
	}
	digest := sha256.Sum256(payload.Content)
	verified.SHA256 = hex.EncodeToString(digest[:])
	if strings.HasPrefix(verifiedMIME, "image/") {
		width, height := 0, 0
		if verifiedMIME == "image/webp" {
			width, height = webPDimensions(payload.Content)
		} else {
			config, _, err := image.DecodeConfig(bytes.NewReader(payload.Content))
			if err == nil {
				width, height = config.Width, config.Height
			}
		}
		if width < 1 || height < 1 {
			return verifiedAttachmentPayload{}, &attachmentItemFailure{
				Code: "chatgpt_attachment_image_invalid",
			}
		}
		verified.Width = width
		verified.Height = height
		if (candidate.Width > 0 && candidate.Width != width) ||
			(candidate.Height > 0 && candidate.Height != height) {
			return verifiedAttachmentPayload{}, &attachmentItemFailure{
				Code: "chatgpt_attachment_dimension_mismatch",
			}
		}
	}
	return verified, nil
}

func webPDimensions(content []byte) (int, int) {
	if len(content) < 20 ||
		string(content[:4]) != "RIFF" ||
		string(content[8:12]) != "WEBP" ||
		uint64(binary.LittleEndian.Uint32(content[4:8]))+8 != uint64(len(content)) {
		return 0, 0
	}
	for offset := uint64(12); offset < uint64(len(content)); {
		if offset+8 > uint64(len(content)) {
			return 0, 0
		}
		chunkStart := offset + 8
		chunkSize := uint64(binary.LittleEndian.Uint32(
			content[offset+4 : offset+8],
		))
		chunkEnd := chunkStart + chunkSize
		paddedEnd := chunkEnd + chunkSize%2
		if chunkEnd < chunkStart || paddedEnd > uint64(len(content)) {
			return 0, 0
		}
		chunk := content[chunkStart:chunkEnd]
		switch string(content[offset : offset+4]) {
		case "VP8 ":
			if len(chunk) < 10 ||
				chunk[0]&1 != 0 ||
				!bytes.Equal(chunk[3:6], []byte{0x9d, 0x01, 0x2a}) {
				return 0, 0
			}
			width := int(binary.LittleEndian.Uint16(chunk[6:8]) & 0x3fff)
			height := int(binary.LittleEndian.Uint16(chunk[8:10]) & 0x3fff)
			return width, height
		case "VP8L":
			if len(chunk) < 5 || chunk[0] != 0x2f || chunk[4]&0xe0 != 0 {
				return 0, 0
			}
			width := 1 + int(chunk[1]) + int(chunk[2]&0x3f)<<8
			height := 1 + int(chunk[2]>>6) + int(chunk[3])<<2 +
				int(chunk[4]&0x0f)<<10
			return width, height
		case "VP8X":
			if len(chunk) != 10 {
				return 0, 0
			}
			width := 1 + int(chunk[4]) + int(chunk[5])<<8 + int(chunk[6])<<16
			height := 1 + int(chunk[7]) + int(chunk[8])<<8 + int(chunk[9])<<16
			return width, height
		}
		offset = paddedEnd
	}
	return 0, 0
}

func normalizedAttachmentMIME(value string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil || !strings.Contains(mediaType, "/") {
		return ""
	}
	return strings.ToLower(mediaType)
}

func compatibleAttachmentMIME(expected string, observed string) bool {
	if expected == observed ||
		observed == "application/octet-stream" ||
		expected == "application/octet-stream" {
		return true
	}
	return strings.HasPrefix(expected, "text/") && observed == "text/plain"
}

func compatibleAttachmentContentMIME(
	expected string,
	observed string,
	content []byte,
) bool {
	if compatibleAttachmentMIME(expected, observed) {
		return true
	}
	if expected == "application/json" && observed == "text/plain" {
		return json.Valid(content)
	}
	if observed != "application/zip" {
		return false
	}
	return openXMLAttachmentMatches(expected, content)
}

func openXMLAttachmentMatches(mimeType string, content []byte) bool {
	root := ""
	switch mimeType {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.ms-word.document.macroenabled.12":
		root = "word/"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.ms-excel.sheet.macroenabled.12":
		root = "xl/"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.ms-powerpoint.presentation.macroenabled.12":
		root = "ppt/"
	default:
		return false
	}
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil || len(archive.File) > 4096 {
		return false
	}
	hasContentTypes := false
	hasDocumentRoot := false
	for _, file := range archive.File {
		switch {
		case file.Name == "[Content_Types].xml":
			hasContentTypes = true
		case strings.HasPrefix(file.Name, root):
			hasDocumentRoot = true
		}
	}
	return hasContentTypes && hasDocumentRoot
}

func attachmentOutputFileName(
	candidate ConversationAttachment,
	verified verifiedAttachmentPayload,
	kindOrdinal int,
) string {
	if safeAttachmentBatchFileName(candidate.FileName) &&
		attachmentFileNameMatchesMIME(candidate.FileName, verified.MIMEType) {
		return candidate.FileName
	}
	if safeAttachmentBatchFileName(verified.FileName) &&
		attachmentFileNameMatchesMIME(verified.FileName, verified.MIMEType) {
		return verified.FileName
	}
	prefix := "attachment"
	if candidate.Kind == "image" {
		prefix = "generated-image"
	}
	extension := preferredAttachmentExtension(verified.MIMEType)
	return fmt.Sprintf(
		"%s-%02d-%s%s",
		prefix,
		kindOrdinal,
		verified.SHA256[:8],
		extension,
	)
}

func safeAttachmentBatchFileName(value string) bool {
	if value == "" || len(value) > 240 ||
		strings.EqualFold(value, attachmentManifestFileName) {
		return false
	}
	if safe, err := safeArtifactFileName(value); err != nil || safe != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func attachmentFileNameMatchesMIME(fileName string, mimeType string) bool {
	extension := strings.ToLower(filepath.Ext(fileName))
	if extension == "" {
		return !strings.HasPrefix(mimeType, "image/")
	}
	known := normalizedAttachmentMIME(mime.TypeByExtension(extension))
	if known == "" {
		return true
	}
	return compatibleAttachmentMIME(mimeType, known) ||
		compatibleAttachmentMIME(known, mimeType)
}

func preferredAttachmentExtension(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	case "application/json":
		return ".json"
	case "text/csv":
		return ".csv"
	case "text/plain":
		return ".txt"
	case "application/zip":
		return ".zip"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "application/vnd.ms-word.document.macroenabled.12":
		return ".docm"
	case "application/vnd.ms-excel.sheet.macroenabled.12":
		return ".xlsm"
	case "application/vnd.ms-powerpoint.presentation.macroenabled.12":
		return ".pptm"
	default:
		return ".bin"
	}
}

func allocateAttachmentFileName(value string, used map[string]int) string {
	key := strings.ToLower(value)
	if used[key] == 0 {
		used[key] = 1
		return value
	}
	extension := filepath.Ext(value)
	stem := strings.TrimSuffix(value, extension)
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, suffix, extension)
		candidateKey := strings.ToLower(candidate)
		if used[candidateKey] != 0 {
			continue
		}
		used[candidateKey] = 1
		return candidate
	}
}
