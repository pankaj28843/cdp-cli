package chatgpt

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	ArtifactDownloadSchemaVersion    = "chatgpt-artifact-download/v1"
	chatGPTArtifactAdmissionProvider = "chatgpt-artifact"
	artifactMetadataRoute            = "/backend-api/conversation/:conversation_id/interpreter/download"
	artifactContentRoute             = "/backend-api/estuary/content"
	maxArtifactBytes                 = 32 << 20
)

var sandboxPathPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sandbox:(/mnt/data/[^\s)\]"'<>]+)`),
	regexp.MustCompile(`(?:^|[^:])(/mnt/data/[^\s)\]"'<>]+)`),
}

type ArtifactDownloadConfig struct {
	BrowserConfig
	Store      *Store
	OutputPath string
	Overwrite  bool
	Timeout    time.Duration
	Now        func() time.Time
}

type ArtifactDownloadData struct {
	SchemaVersion   string         `json:"schema_version"`
	ConversationID  string         `json:"conversation_id"`
	FileName        string         `json:"file_name"`
	OutputPath      string         `json:"output_path"`
	DownloadedBytes int            `json:"downloaded_bytes"`
	SHA256          string         `json:"sha256"`
	MIMEType        string         `json:"mime_type,omitempty"`
	ReadMode        string         `json:"read_mode"`
	Metadata        map[string]any `json:"metadata"`
}

type artifactLocator struct {
	ConversationID string
	MessageID      string
	FileName       string
	SandboxPath    string
}

type artifactMetadata struct {
	DownloadURL  string
	FileName     string
	Size         int
	SizePresent  bool
	SizeEncoding string
	MIMEType     string
}

type browserBinaryFetchResult struct {
	OK          bool   `json:"ok"`
	StatusCode  int    `json:"status_code"`
	BodyBase64  string `json:"body_base64"`
	BodyBytes   int    `json:"body_bytes"`
	ContentType string `json:"content_type"`
	RetryAfter  string `json:"retry_after"`
	Error       string `json:"error"`
}

func DownloadArtifact(
	ctx context.Context,
	config ArtifactDownloadConfig,
	conversationID string,
	fileName string,
) webagent.Result {
	conversationID = strings.TrimSpace(conversationID)
	runID := webagent.NewRunID()
	data := ArtifactDownloadData{
		SchemaVersion:  ArtifactDownloadSchemaVersion,
		ConversationID: conversationID,
		FileName:       strings.TrimSpace(fileName),
		ReadMode:       "not_started",
		Metadata:       map[string]any{},
	}
	conversation := conversationRef(conversationID)
	if !conversationIDPattern.MatchString(conversationID) {
		return artifactFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"chatgpt_invalid_conversation_id", "usage",
			"ChatGPT conversation id contains unsupported characters",
			data, nil,
		)
	}
	safeName, err := safeArtifactFileName(fileName)
	if err != nil {
		return artifactFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"chatgpt_invalid_artifact_filename", "usage",
			err.Error(), data, nil,
		)
	}
	data.FileName = safeName
	destination, err := validateArtifactDestination(
		config.OutputPath,
		config.Overwrite,
	)
	if err != nil {
		return artifactFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"chatgpt_invalid_artifact_destination", "usage",
			err.Error(), data, nil,
		)
	}
	data.OutputPath = destination
	if config.Store == nil {
		return artifactFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"chatgpt_state_unavailable", "internal",
			"ChatGPT owner-only auth state is unavailable",
			data, nil,
		)
	}
	now := time.Now().UTC()
	if config.Now != nil {
		now = config.Now().UTC()
	}
	template, auth, templateErr := config.Store.LoadTemplateStatus(
		ctx,
		now,
		DefaultAuthTTL,
	)
	if templateErr != nil && auth.State == "invalid" {
		return artifactFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"chatgpt_auth_invalid", "auth",
			"ChatGPT owner-only auth evidence is invalid before artifact download",
			data,
			[]string{"cdp workflow agent chatgpt auth refresh --json"},
		)
	}
	if !auth.Ready {
		return artifactFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"chatgpt_auth_"+auth.State, "auth",
			"ChatGPT auth evidence is not ready before artifact download",
			data,
			[]string{"cdp workflow agent chatgpt auth refresh --json"},
		)
	}
	if config.Timeout <= 0 {
		config.Timeout = 2 * time.Minute
	}

	return runOwned(
		ctx,
		artifactBrowserConfig(config.BrowserConfig),
		runID,
		webagent.OperationArtifactDownload,
		"",
		"about:blank",
		"browser_context_stable_http",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			refreshedTemplate, failure := prepareBrowserRead(
				ctx,
				config.BrowserConfig,
				config.Store,
				lease,
			)
			if failure != nil {
				return artifactFailure(
					runID, config, webagent.StageAttached,
					target, pending,
					failure.code, failure.errClass,
					failure.message, data, nil,
				)
			}
			template = refreshedTemplate
			session := lease.Session()
			data.ReadMode = "candidate_browser_context_http"
			detailPath := "/backend-api/conversation/" +
				url.PathEscape(conversationID)
			data.Metadata["detail_read_attempts"] = 1
			detailResponse, failure := browserFetch(
				ctx,
				config.Admission,
				session,
				template,
				Origin+detailPath,
				ConversationDetailRoute,
			)
			if failure != nil {
				return artifactReadFailure(
					runID, config, target, pending,
					*failure, data,
				)
			}
			var detail map[string]any
			if err := decodeBoundedJSON(
				strings.NewReader(detailResponse.Body),
				&detail,
			); err != nil {
				return artifactFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending,
					"chatgpt_invalid_detail_response", "provider",
					"ChatGPT artifact detail returned invalid bounded JSON",
					data, nil,
				)
			}
			locator, err := locateArtifact(
				detail,
				conversationID,
				safeName,
			)
			if err != nil {
				return artifactFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending,
					"chatgpt_artifact_locator_unavailable",
					"provider", err.Error(), data, nil,
				)
			}
			query := url.Values{}
			query.Set("message_id", locator.MessageID)
			query.Set("sandbox_path", locator.SandboxPath)
			metadataPath := detailPath +
				"/interpreter/download?" + query.Encode()
			data.Metadata["metadata_read_attempts"] = 1
			metadataResponse, failure := browserFetch(
				ctx,
				config.Admission,
				session,
				template,
				Origin+metadataPath,
				artifactMetadataRoute,
			)
			if failure != nil {
				return artifactReadFailure(
					runID, config, target, pending,
					*failure, data,
				)
			}
			var metadataPayload map[string]any
			if err := decodeBoundedJSON(
				strings.NewReader(metadataResponse.Body),
				&metadataPayload,
			); err != nil {
				return artifactFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending,
					"chatgpt_invalid_artifact_metadata",
					"provider",
					"ChatGPT artifact metadata returned invalid bounded JSON",
					data, nil,
				)
			}
			metadata, err := validateArtifactMetadata(
				metadataPayload,
				safeName,
			)
			if err != nil {
				return artifactFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending,
					"chatgpt_invalid_artifact_metadata",
					"provider", err.Error(), data, nil,
				)
			}
			data.Metadata["file_size_encoding"] =
				metadata.SizeEncoding
			data.Metadata["content_read_attempts"] = 1
			binary, failure := browserFetchBinary(
				ctx,
				config.Admission,
				session,
				template,
				metadata.DownloadURL,
				artifactContentRoute,
				maxArtifactBytes,
			)
			if failure != nil {
				return artifactReadFailure(
					runID, config, target, pending,
					*failure, data,
				)
			}
			content, err := base64.StdEncoding.DecodeString(
				binary.BodyBase64,
			)
			if err != nil ||
				len(content) != binary.BodyBytes ||
				(metadata.SizePresent &&
					len(content) != metadata.Size) {
				return artifactFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending,
					"chatgpt_artifact_content_mismatch",
					"provider",
					"ChatGPT artifact content did not match its bounded metadata",
					data, nil,
				)
			}
			if err := writeArtifactAtomic(
				destination,
				content,
				config.Overwrite,
			); err != nil {
				return artifactFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending,
					"chatgpt_artifact_write_failed", "filesystem",
					"ChatGPT artifact could not be written to the explicit destination",
					data, nil,
				)
			}
			digest := sha256.Sum256(content)
			data.DownloadedBytes = len(content)
			data.SHA256 = hex.EncodeToString(digest[:])
			data.MIMEType = metadata.MIMEType
			if data.MIMEType == "" {
				data.MIMEType = binary.ContentType
			}
			data.ReadMode = browserReadMode
			data.Metadata["content_bound_bytes"] = maxArtifactBytes
			if err := lease.MarkTerminal(ctx); err != nil {
				return artifactFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending,
					"chatgpt_artifact_terminal_state_failed",
					"internal",
					"ChatGPT artifact terminal state could not be persisted",
					data, nil,
				)
			}
			result := operationSuccess(
				runID, config.BuildCommit,
				webagent.OperationArtifactDownload,
				webagent.StageObserveTerminal, browserReadMode,
				target, pending, data, nil,
			)
			result.State = webagent.StateTerminal
			result.Conversation = conversation
			return result
		},
	)
}

func artifactBrowserConfig(config BrowserConfig) BrowserConfig {
	config.AdmissionProvider = chatGPTArtifactAdmissionProvider
	return config
}

func locateArtifact(
	detail map[string]any,
	conversationID string,
	fileName string,
) (artifactLocator, error) {
	detailID, _ := detail["conversation_id"].(string)
	if detailID != "" && detailID != conversationID {
		return artifactLocator{}, fmt.Errorf(
			"ChatGPT artifact detail id did not match the request",
		)
	}
	mapping, ok := detail["mapping"].(map[string]any)
	if !ok {
		return artifactLocator{}, fmt.Errorf(
			"ChatGPT conversation detail is missing its mapping",
		)
	}
	candidates := make([]artifactLocator, 0, 1)
	sawUnfinished := false
	for nodeKey, rawNode := range mapping {
		node, ok := rawNode.(map[string]any)
		if !ok {
			continue
		}
		message, ok := node["message"].(map[string]any)
		if !ok {
			continue
		}
		author, _ := message["author"].(map[string]any)
		role, _ := author["role"].(string)
		if role != "assistant" {
			continue
		}
		paths := matchingSandboxPaths(message, fileName)
		if len(paths) == 0 {
			continue
		}
		status, _ := message["status"].(string)
		endTurn, _ := message["end_turn"].(bool)
		if status != "finished_successfully" || !endTurn {
			sawUnfinished = true
			continue
		}
		messageID := nodeKey
		if !conversationIDPattern.MatchString(messageID) {
			messageID, _ = node["id"].(string)
		}
		if !conversationIDPattern.MatchString(messageID) {
			return artifactLocator{}, fmt.Errorf(
				"ChatGPT artifact assistant message id is unsafe",
			)
		}
		for _, sandboxPath := range paths {
			candidates = append(candidates, artifactLocator{
				ConversationID: conversationID,
				MessageID:      messageID,
				FileName:       fileName,
				SandboxPath:    sandboxPath,
			})
		}
	}
	if len(candidates) == 0 {
		if sawUnfinished {
			return artifactLocator{}, fmt.Errorf(
				"ChatGPT artifact exists only on an unfinished assistant turn",
			)
		}
		return artifactLocator{}, fmt.Errorf(
			"ChatGPT conversation has no finished artifact with the requested name",
		)
	}
	if len(candidates) != 1 {
		return artifactLocator{}, fmt.Errorf(
			"ChatGPT artifact filename is ambiguous in the conversation",
		)
	}
	return candidates[0], nil
}

func matchingSandboxPaths(
	message map[string]any,
	fileName string,
) []string {
	content, _ := message["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	paths := make([]string, 0)
	seen := map[string]bool{}
	for _, rawPart := range parts {
		part, ok := rawPart.(string)
		if !ok {
			continue
		}
		for _, pattern := range sandboxPathPatterns {
			for _, match := range pattern.FindAllStringSubmatch(
				part,
				-1,
			) {
				if len(match) < 2 {
					continue
				}
				normalized, ok := normalizeSandboxPath(match[1])
				if !ok ||
					path.Base(normalized) != fileName ||
					seen[normalized] {
					continue
				}
				seen[normalized] = true
				paths = append(paths, normalized)
			}
		}
	}
	return paths
}

func normalizeSandboxPath(rawPath string) (string, bool) {
	decoded, err := url.PathUnescape(
		strings.TrimRight(rawPath, ".,"),
	)
	if err != nil ||
		strings.ContainsRune(decoded, '\x00') ||
		strings.ContainsAny(decoded, "?#") ||
		!strings.HasPrefix(decoded, "/mnt/data/") {
		return "", false
	}
	for _, part := range strings.Split(decoded, "/") {
		if part == ".." {
			return "", false
		}
	}
	cleaned := path.Clean(decoded)
	if !strings.HasPrefix(cleaned, "/mnt/data/") {
		return "", false
	}
	return cleaned, true
}

func validateArtifactMetadata(
	payload map[string]any,
	fileName string,
) (artifactMetadata, error) {
	status, _ := payload["status"].(string)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "success", "ready", "complete", "completed":
	default:
		return artifactMetadata{}, fmt.Errorf(
			"ChatGPT artifact metadata is not terminal",
		)
	}
	responseName, _ := payload["file_name"].(string)
	if responseName != fileName {
		return artifactMetadata{}, fmt.Errorf(
			"ChatGPT artifact metadata filename did not match",
		)
	}
	downloadURL, _ := payload["download_url"].(string)
	parsed, err := url.Parse(downloadURL)
	if err != nil ||
		!parsed.IsAbs() ||
		!strings.EqualFold(parsed.Scheme, "https") ||
		!strings.EqualFold(parsed.Host, "chatgpt.com") ||
		parsed.Path != artifactContentRoute {
		return artifactMetadata{}, fmt.Errorf(
			"ChatGPT artifact download URL is not the observed same-origin content route",
		)
	}
	metadata := artifactMetadata{
		DownloadURL: downloadURL,
		FileName:    fileName,
	}
	if mimeType, ok := payload["mime_type"].(string); ok {
		metadata.MIMEType = mimeType
	}
	if rawSize, ok := payload["file_size_bytes"]; ok &&
		rawSize != nil {
		size, encoding, ok := exactNonnegativeJSONInt(rawSize)
		if !ok || size > maxArtifactBytes {
			return artifactMetadata{}, fmt.Errorf(
				"ChatGPT artifact metadata size is invalid or exceeds the bound",
			)
		}
		metadata.Size = size
		metadata.SizePresent = true
		metadata.SizeEncoding = encoding
	}
	if !metadata.SizePresent {
		metadata.SizeEncoding = "absent"
	}
	return metadata, nil
}

func exactNonnegativeJSONInt(value any) (int, string, bool) {
	switch typed := value.(type) {
	case float64:
		if typed < 0 || typed > float64(maxArtifactBytes) {
			return 0, "", false
		}
		integer := int(typed)
		return integer, "number", float64(integer) == typed
	case json.Number:
		integer, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil ||
			integer < 0 ||
			integer > int64(maxArtifactBytes) {
			return 0, "", false
		}
		return int(integer), "number", true
	case string:
		if typed == "" ||
			strings.Trim(typed, "0123456789") != "" {
			return 0, "", false
		}
		integer, err := strconv.ParseUint(typed, 10, 64)
		if err != nil || integer > uint64(maxArtifactBytes) {
			return 0, "", false
		}
		return int(integer), "decimal_string", true
	default:
		return 0, "", false
	}
}

func browserFetchBinary(
	ctx context.Context,
	gate *admission.Gate,
	session *cdp.PageSession,
	template RequestTemplate,
	endpoint string,
	targetRoute string,
	maxBytes int,
) (browserBinaryFetchResult, *readFailure) {
	throttle, failure := acquireChatGPTThrottle(ctx, gate)
	if failure != nil {
		return browserBinaryFetchResult{}, failure
	}
	response, failure := browserFetchBinaryUnthrottled(
		ctx,
		session,
		template,
		endpoint,
		targetRoute,
		maxBytes,
	)
	if err := releaseChatGPTThrottle(throttle, failure); err != nil {
		return browserBinaryFetchResult{}, internalReadFailure(
			"ChatGPT shared provider throttle outcome could not be persisted",
		)
	}
	return response, failure
}

func browserFetchBinaryUnthrottled(
	ctx context.Context,
	session *cdp.PageSession,
	template RequestTemplate,
	endpoint string,
	targetRoute string,
	maxBytes int,
) (browserBinaryFetchResult, *readFailure) {
	headers := browserFetchHeaders(
		template.Headers,
		endpoint,
		targetRoute,
	)
	encodedEndpoint, err := json.Marshal(endpoint)
	if err != nil {
		return browserBinaryFetchResult{}, internalReadFailure(
			"ChatGPT artifact content URL could not be encoded",
		)
	}
	encodedHeaders, err := json.Marshal(headers)
	if err != nil {
		return browserBinaryFetchResult{}, internalReadFailure(
			"ChatGPT artifact content headers could not be encoded",
		)
	}
	expression := fmt.Sprintf(`(async () => {
	  try {
	    const response = await fetch(%s, {
	      method: 'GET',
	      headers: %s,
	      credentials: 'include',
	      cache: 'no-store',
	      redirect: 'manual'
	    });
	    const declared = Number(response.headers.get('content-length') || '');
	    if (Number.isFinite(declared) && declared > %d) {
	      return {
	        ok: false,
	        status_code: response.status,
	        body_base64: '',
	        body_bytes: declared,
	        content_type: response.headers.get('content-type') || '',
	        retry_after: response.headers.get('retry-after') || '',
	        error: 'response_too_large'
	      };
	    }
	    if (!response.body) {
	      return {
	        ok: false,
	        status_code: response.status,
	        body_base64: '',
	        body_bytes: 0,
	        content_type: response.headers.get('content-type') || '',
	        retry_after: response.headers.get('retry-after') || '',
	        error: 'response_body_unavailable'
	      };
	    }
	    const reader = response.body.getReader();
	    const chunks = [];
	    let total = 0;
	    while (true) {
	      const next = await reader.read();
	      if (next.done) break;
	      if (!(next.value instanceof Uint8Array)) {
	        await reader.cancel();
	        throw new Error('invalid_response_chunk');
	      }
	      total += next.value.byteLength;
	      if (total > %d) {
	        await reader.cancel();
	        return {
	          ok: false,
	          status_code: response.status,
	          body_base64: '',
	          body_bytes: total,
	          content_type: response.headers.get('content-type') || '',
	          retry_after: response.headers.get('retry-after') || '',
	          error: 'response_too_large'
	        };
	      }
	      chunks.push(next.value);
	    }
	    const bytes = new Uint8Array(total);
	    let offset = 0;
	    for (const chunk of chunks) {
	      bytes.set(chunk, offset);
	      offset += chunk.byteLength;
	    }
	    let binary = '';
	    for (let index = 0; index < bytes.length; index += 32768) {
	      binary += String.fromCharCode(...bytes.subarray(
	        index,
	        Math.min(index + 32768, bytes.length)
	      ));
	    }
	    return {
	      ok: response.status === 200,
	      status_code: response.status,
	      body_base64: btoa(binary),
	      body_bytes: bytes.length,
	      content_type: response.headers.get('content-type') || '',
	      retry_after: response.headers.get('retry-after') || '',
	      error: ''
	    };
	  } catch (_) {
	    return {
	      ok: false,
	      status_code: 0,
	      body_base64: '',
	      body_bytes: 0,
	      content_type: '',
	      retry_after: '',
	      error: 'fetch_failed'
	      };
	  }
	})()`, encodedEndpoint, encodedHeaders, maxBytes, maxBytes)
	var response browserBinaryFetchResult
	if err := evaluateInto(
		ctx,
		session,
		expression,
		&response,
	); err != nil {
		return browserBinaryFetchResult{}, &readFailure{
			code:     "chatgpt_artifact_content_unavailable",
			errClass: "connection",
			message:  "ChatGPT artifact content fetch was unavailable",
		}
	}
	if response.OK && response.StatusCode == http.StatusOK {
		return response, nil
	}
	failure := &readFailure{
		code:       "chatgpt_artifact_content_failed",
		errClass:   "provider",
		message:    fmt.Sprintf("ChatGPT artifact content returned status %d", response.StatusCode),
		statusCode: response.StatusCode,
	}
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		failure.code = "chatgpt_browser_context_auth_failed"
		failure.errClass = "auth"
		failure.message = "ChatGPT artifact content requires refreshed headed auth"
	case http.StatusTooManyRequests:
		failure.code = "chatgpt_rate_limited"
		failure.errClass = "rate_limit"
		failure.message = "ChatGPT artifact content was rate limited"
		failure.retryAt = retryAtFromHeader(
			response.RetryAfter,
			time.Now().UTC(),
		)
	case 0:
		failure.code = "chatgpt_artifact_content_unavailable"
		failure.errClass = "connection"
		failure.message = "ChatGPT artifact content fetch failed"
	}
	if response.Error == "response_too_large" {
		failure.code = "chatgpt_artifact_content_too_large"
		failure.errClass = "provider"
		failure.message = "ChatGPT artifact content exceeded its bounded download size"
	}
	return browserBinaryFetchResult{}, failure
}

func safeArtifactFileName(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" ||
		raw == "." ||
		raw == ".." ||
		strings.ContainsRune(raw, '\x00') ||
		strings.ContainsAny(raw, `/\`) ||
		filepath.Base(raw) != raw {
		return "", fmt.Errorf(
			"artifact filename must be one plain generated filename",
		)
	}
	return raw, nil
}

func validateArtifactDestination(
	raw string,
	overwrite bool,
) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf(
			"artifact output path must include an explicit filename",
		)
	}
	absolute, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve artifact output path")
	}
	parent := filepath.Dir(absolute)
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return "", fmt.Errorf(
			"artifact output parent directory must already exist",
		)
	}
	info, err := os.Lstat(absolute)
	if err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf(
				"artifact output path must be a regular file destination",
			)
		}
		if !overwrite {
			return "", fmt.Errorf(
				"artifact output path exists; pass --overwrite to replace it",
			)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect artifact output path")
	}
	return absolute, nil
}

func writeArtifactAtomic(
	destination string,
	content []byte,
	overwrite bool,
) error {
	parent := filepath.Dir(destination)
	temp, err := os.CreateTemp(parent, ".cdp-chatgpt-artifact-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if overwrite {
		if info, err := os.Lstat(destination); err == nil &&
			!info.Mode().IsRegular() {
			return fmt.Errorf("refuse to replace non-regular destination")
		}
		if err := os.Rename(tempPath, destination); err != nil {
			return err
		}
	} else {
		if err := os.Link(tempPath, destination); err != nil {
			return err
		}
		if err := os.Remove(tempPath); err != nil {
			return err
		}
	}
	directory, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func artifactReadFailure(
	runID string,
	config ArtifactDownloadConfig,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	failure readFailure,
	data ArtifactDownloadData,
) webagent.Result {
	result := artifactFailure(
		runID, config, webagent.StageObserveTerminal,
		target, cleanup,
		failure.code, failure.errClass,
		failure.message, data, nil,
	)
	if result.Error != nil && !failure.retryAt.IsZero() {
		result.Error.RetryAt = failure.retryAt.UTC().Format(
			time.RFC3339Nano,
		)
	}
	return result
}

func artifactFailure(
	runID string,
	config ArtifactDownloadConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	code string,
	errClass string,
	message string,
	data ArtifactDownloadData,
	nextCommands []string,
) webagent.Result {
	result := operationFailure(
		runID, config.BuildCommit,
		webagent.OperationArtifactDownload,
		stage, data.ReadMode, target, cleanup,
		code, errClass, message, data, nextCommands,
	)
	if result.NextCommands == nil {
		result.NextCommands = []string{}
	}
	if data.ConversationID != "" &&
		conversationIDPattern.MatchString(data.ConversationID) {
		result.Conversation = conversationRef(data.ConversationID)
	}
	return result
}
