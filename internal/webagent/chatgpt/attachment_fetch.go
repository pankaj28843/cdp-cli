package chatgpt

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const attachmentFileMetadataRoute = "/backend-api/files/download/:file_id"

const attachmentFileIDPattern = `[A-Za-z0-9_.:-]{1,256}`

var (
	attachmentFileID = regexp.MustCompile(
		`^` + attachmentFileIDPattern + `$`,
	)
	attachmentFileDownloadPath = regexp.MustCompile(
		`^/backend-api/files/download/(` + attachmentFileIDPattern + `)$`,
	)
)

type attachmentRequestKind string

const (
	attachmentRequestMetadata attachmentRequestKind = "metadata"
	attachmentRequestContent  attachmentRequestKind = "content"
	attachmentRequestSandbox  attachmentRequestKind = "sandbox"
)

type attachmentRequest struct {
	Kind        attachmentRequestKind
	Endpoint    string
	TargetRoute string
	MessageID   string
	SandboxPath string
}

type attachmentDownloadMetadata struct {
	DownloadURL string
	FileName    string
	MIMEType    string
	Size        int
	SizePresent bool
}

type directAttachmentResolver struct {
	config         ReadConfig
	template       RequestTemplate
	conversationID string
}

type browserAttachmentResolver struct {
	session        *cdp.PageSession
	template       RequestTemplate
	conversationID string
}

type attachmentFetchTransport interface {
	fetchJSON(
		context.Context,
		string,
		string,
	) (map[string]any, *attachmentItemFailure)
	fetchContent(
		context.Context,
		string,
		string,
		int64,
	) (attachmentPayload, *attachmentItemFailure)
}

func attachmentRequestForCandidate(
	conversationID string,
	candidate ConversationAttachment,
) (attachmentRequest, *attachmentItemFailure) {
	failure := func() (attachmentRequest, *attachmentItemFailure) {
		return attachmentRequest{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_source_unsupported",
		}
	}
	if !conversationIDPattern.MatchString(conversationID) {
		return failure()
	}
	locator := strings.TrimSpace(candidate.sourceLocator)
	if strings.HasPrefix(locator, "sandbox:") {
		sandboxPath, ok := normalizeSandboxPath(
			strings.TrimPrefix(locator, "sandbox:"),
		)
		if !ok || !conversationIDPattern.MatchString(candidate.messageID) {
			return failure()
		}
		return attachmentRequest{
			Kind:        attachmentRequestSandbox,
			MessageID:   candidate.messageID,
			SandboxPath: sandboxPath,
		}, nil
	}

	parsed, err := url.Parse(locator)
	if err != nil || parsed.User != nil {
		return failure()
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "sediment" || scheme == "file-service" {
		if parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Host != "" && parsed.Path != "") {
			return failure()
		}
		fileID := parsed.Host
		if fileID == "" {
			fileID = strings.TrimPrefix(parsed.Path, "/")
		}
		if !attachmentFileID.MatchString(fileID) {
			return failure()
		}
		query := url.Values{}
		query.Set("conversation_id", conversationID)
		query.Set("inline", "false")
		metadataPath := "/backend-api/files/download/" +
			url.PathEscape(fileID)
		return attachmentRequest{
			Kind: attachmentRequestMetadata,
			Endpoint: Origin + metadataPath + "?" +
				query.Encode(),
			TargetRoute: attachmentFileMetadataRoute,
		}, nil
	}
	if locator == "" && candidate.FileID != "" {
		fileID := candidate.FileID
		if !attachmentFileID.MatchString(fileID) {
			return failure()
		}
		query := url.Values{}
		query.Set("conversation_id", conversationID)
		query.Set("inline", "false")
		metadataPath := "/backend-api/files/download/" +
			url.PathEscape(fileID)
		return attachmentRequest{
			Kind: attachmentRequestMetadata,
			Endpoint: Origin + metadataPath + "?" +
				query.Encode(),
			TargetRoute: attachmentFileMetadataRoute,
		}, nil
	}
	if scheme != "" && scheme != "https" {
		return failure()
	}
	if scheme == "https" {
		if !strings.EqualFold(parsed.Hostname(), "chatgpt.com") ||
			parsed.Port() != "" {
			return failure()
		}
	} else if parsed.Host != "" || !strings.HasPrefix(locator, "/") {
		return failure()
	}
	if parsed.Fragment != "" || parsed.RawPath != "" ||
		parsed.Path == "" ||
		path.Clean(parsed.Path) != parsed.Path {
		return failure()
	}
	endpoint := locator
	if scheme == "" {
		endpoint = Origin + locator
	}
	if parsed.Path == artifactContentRoute {
		if !validAttachmentContentQuery(parsed.Query()) {
			return failure()
		}
		return attachmentRequest{
			Kind:        attachmentRequestContent,
			Endpoint:    endpoint,
			TargetRoute: artifactContentRoute,
		}, nil
	}
	if matches := attachmentFileDownloadPath.FindStringSubmatch(
		parsed.Path,
	); len(matches) == 2 {
		if !validAttachmentMetadataQuery(
			parsed.Query(),
			conversationID,
		) {
			return failure()
		}
		return attachmentRequest{
			Kind:        attachmentRequestMetadata,
			Endpoint:    endpoint,
			TargetRoute: attachmentFileMetadataRoute,
		}, nil
	}
	return failure()
}

func validAttachmentContentQuery(values url.Values) bool {
	if len(values) == 0 || strings.TrimSpace(values.Get("sig")) == "" {
		return false
	}
	allowed := map[string]bool{
		"cid": true,
		"cd":  true,
		"fn":  true,
		"id":  true,
		"p":   true,
		"sig": true,
		"ts":  true,
		"v":   true,
	}
	for key, candidates := range values {
		if !allowed[key] || len(candidates) != 1 ||
			strings.TrimSpace(candidates[0]) == "" ||
			len(candidates[0]) > 2048 {
			return false
		}
	}
	return true
}

func validAttachmentMetadataQuery(
	values url.Values,
	conversationID string,
) bool {
	if values.Get("conversation_id") != conversationID {
		return false
	}
	for key, candidates := range values {
		if len(candidates) != 1 || len(candidates[0]) > 256 {
			return false
		}
		switch key {
		case "conversation_id":
			if candidates[0] != conversationID {
				return false
			}
		case "inline":
			if candidates[0] != "true" && candidates[0] != "false" {
				return false
			}
		case "download_intent":
			if !attachmentFileID.MatchString(candidates[0]) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (resolver directAttachmentResolver) Resolve(
	ctx context.Context,
	candidate ConversationAttachment,
	maxBytes int64,
) (attachmentPayload, *attachmentItemFailure) {
	return resolveAttachment(
		ctx,
		resolver.conversationID,
		candidate,
		maxBytes,
		resolver,
	)
}

func (resolver browserAttachmentResolver) Resolve(
	ctx context.Context,
	candidate ConversationAttachment,
	maxBytes int64,
) (attachmentPayload, *attachmentItemFailure) {
	return resolveAttachment(
		ctx,
		resolver.conversationID,
		candidate,
		maxBytes,
		resolver,
	)
}

func resolveAttachment(
	ctx context.Context,
	conversationID string,
	candidate ConversationAttachment,
	maxBytes int64,
	transport attachmentFetchTransport,
) (attachmentPayload, *attachmentItemFailure) {
	request, failure := attachmentRequestForCandidate(
		conversationID,
		candidate,
	)
	if failure != nil {
		return attachmentPayload{}, failure
	}
	if request.Kind == attachmentRequestContent {
		return transport.fetchContent(
			ctx,
			request.Endpoint,
			request.TargetRoute,
			maxBytes,
		)
	}
	metadataEndpoint := request.Endpoint
	metadataRoute := request.TargetRoute
	if request.Kind == attachmentRequestSandbox {
		query := url.Values{}
		query.Set("message_id", request.MessageID)
		query.Set("sandbox_path", request.SandboxPath)
		detailPath := "/backend-api/conversation/" +
			url.PathEscape(conversationID)
		metadataEndpoint = Origin + detailPath +
			"/interpreter/download?" + query.Encode()
		metadataRoute = artifactMetadataRoute
	}
	payload, failure := transport.fetchJSON(
		ctx,
		metadataEndpoint,
		metadataRoute,
	)
	if failure != nil {
		return attachmentPayload{}, failure
	}
	metadata, failure := validateAttachmentDownloadMetadata(
		payload,
		candidate,
	)
	if failure != nil {
		return attachmentPayload{}, failure
	}
	content, failure := transport.fetchContent(
		ctx,
		metadata.DownloadURL,
		artifactContentRoute,
		maxBytes,
	)
	if failure != nil {
		return attachmentPayload{}, failure
	}
	if metadata.SizePresent && len(content.Content) != metadata.Size {
		return attachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_size_mismatch",
		}
	}
	metadataMIME := normalizedAttachmentMIME(metadata.MIMEType)
	contentMIME := normalizedAttachmentMIME(content.ContentType)
	if metadataMIME != "" && contentMIME != "" &&
		!compatibleAttachmentContentMIME(
			metadataMIME,
			contentMIME,
			content.Content,
		) {
		return attachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_mime_mismatch",
		}
	}
	if content.ContentType == "" {
		content.ContentType = metadata.MIMEType
	}
	content.FileName = metadata.FileName
	return content, nil
}

func (resolver browserAttachmentResolver) fetchJSON(
	ctx context.Context,
	endpoint string,
	targetRoute string,
) (map[string]any, *attachmentItemFailure) {
	response, readFailure := browserFetch(
		ctx,
		resolver.session,
		resolver.template,
		endpoint,
		targetRoute,
	)
	if readFailure != nil {
		return nil, attachmentItemFailureFromRead(readFailure)
	}
	var payload map[string]any
	if err := decodeBoundedJSON(
		strings.NewReader(response.Body),
		&payload,
	); err != nil {
		return nil, &attachmentItemFailure{
			Code: "chatgpt_attachment_metadata_invalid",
		}
	}
	return payload, nil
}

func (resolver browserAttachmentResolver) fetchContent(
	ctx context.Context,
	endpoint string,
	targetRoute string,
	maxBytes int64,
) (attachmentPayload, *attachmentItemFailure) {
	candidateRequest, failure := attachmentRequestForCandidate(
		resolver.conversationID,
		ConversationAttachment{
			Kind:          "file",
			sourceLocator: endpoint,
		},
	)
	if failure != nil || candidateRequest.Kind != attachmentRequestContent {
		return attachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_source_unsupported",
		}
	}
	if maxBytes > int64(maxArtifactBytes) {
		maxBytes = int64(maxArtifactBytes)
	}
	response, readFailure := browserFetchBinary(
		ctx,
		resolver.session,
		resolver.template,
		endpoint,
		targetRoute,
		int(maxBytes),
	)
	if readFailure != nil {
		return attachmentPayload{}, attachmentItemFailureFromRead(readFailure)
	}
	content, err := base64.StdEncoding.DecodeString(response.BodyBase64)
	if err != nil || len(content) != response.BodyBytes {
		return attachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_content_mismatch",
		}
	}
	return attachmentPayload{
		Content:     content,
		ContentType: response.ContentType,
	}, nil
}

func attachmentItemFailureFromRead(
	failure *readFailure,
) *attachmentItemFailure {
	if failure == nil {
		return nil
	}
	switch failure.errClass {
	case "auth":
		return &attachmentItemFailure{Code: "chatgpt_attachment_auth_failed"}
	case "rate_limit":
		return &attachmentItemFailure{Code: "chatgpt_attachment_rate_limited"}
	case "connection":
		return &attachmentItemFailure{
			Code: "chatgpt_attachment_content_unavailable",
		}
	default:
		if strings.Contains(failure.code, "too_large") {
			return &attachmentItemFailure{
				Code: "chatgpt_attachment_item_bytes_exceeded",
			}
		}
		return &attachmentItemFailure{Code: "chatgpt_attachment_fetch_failed"}
	}
}

func (resolver directAttachmentResolver) fetchJSON(
	ctx context.Context,
	endpoint string,
	targetRoute string,
) (map[string]any, *attachmentItemFailure) {
	request, err := newChatGPTRequest(
		ctx,
		resolver.template,
		endpoint,
		targetRoute,
	)
	if err != nil {
		return nil, &attachmentItemFailure{
			Code: "chatgpt_attachment_request_invalid",
		}
	}
	response, failure := doDirectAttachmentRequest(
		resolver.config,
		request,
	)
	if failure != nil {
		return nil, failure
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := decodeBoundedJSON(response.Body, &payload); err != nil {
		return nil, &attachmentItemFailure{
			Code: "chatgpt_attachment_metadata_invalid",
		}
	}
	return payload, nil
}

func (resolver directAttachmentResolver) fetchContent(
	ctx context.Context,
	endpoint string,
	targetRoute string,
	maxBytes int64,
) (attachmentPayload, *attachmentItemFailure) {
	candidateRequest, failure := attachmentRequestForCandidate(
		resolver.conversationID,
		ConversationAttachment{
			Kind:          "file",
			sourceLocator: endpoint,
		},
	)
	if failure != nil || candidateRequest.Kind != attachmentRequestContent {
		return attachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_source_unsupported",
		}
	}
	request, err := newChatGPTRequest(
		ctx,
		resolver.template,
		endpoint,
		targetRoute,
	)
	if err != nil {
		return attachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_request_invalid",
		}
	}
	response, failure := doDirectAttachmentRequest(
		resolver.config,
		request,
	)
	if failure != nil {
		return attachmentPayload{}, failure
	}
	defer response.Body.Close()
	if response.ContentLength > maxBytes {
		return attachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_item_bytes_exceeded",
		}
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return attachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_content_unavailable",
		}
	}
	if int64(len(content)) > maxBytes {
		return attachmentPayload{}, &attachmentItemFailure{
			Code: "chatgpt_attachment_item_bytes_exceeded",
		}
	}
	return attachmentPayload{
		Content:     content,
		ContentType: response.Header.Get("Content-Type"),
	}, nil
}

func doDirectAttachmentRequest(
	config ReadConfig,
	request *http.Request,
) (*http.Response, *attachmentItemFailure) {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, &attachmentItemFailure{
			Code: "chatgpt_attachment_content_unavailable",
		}
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	_ = response.Body.Close()
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return nil, &attachmentItemFailure{
			Code: "chatgpt_attachment_redirect_rejected",
		}
	case response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden:
		return nil, &attachmentItemFailure{
			Code: "chatgpt_attachment_auth_failed",
		}
	case response.StatusCode == http.StatusTooManyRequests:
		return nil, &attachmentItemFailure{
			Code: "chatgpt_attachment_rate_limited",
		}
	default:
		return nil, &attachmentItemFailure{
			Code: "chatgpt_attachment_fetch_failed",
		}
	}
}

func validateAttachmentDownloadMetadata(
	payload map[string]any,
	candidate ConversationAttachment,
) (attachmentDownloadMetadata, *attachmentItemFailure) {
	fail := func(code string) (attachmentDownloadMetadata, *attachmentItemFailure) {
		return attachmentDownloadMetadata{}, &attachmentItemFailure{Code: code}
	}
	status, _ := payload["status"].(string)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "success", "ready", "complete", "completed":
	default:
		return fail("chatgpt_attachment_metadata_status_invalid")
	}
	downloadURL, _ := payload["download_url"].(string)
	if downloadURL == "" {
		downloadURL, _ = payload["url"].(string)
	}
	if strings.TrimSpace(downloadURL) == "" {
		return fail("chatgpt_attachment_metadata_url_missing")
	}
	request, failure := attachmentRequestForCandidate(
		"metadata-validation",
		ConversationAttachment{
			Kind:          candidate.Kind,
			sourceLocator: downloadURL,
		},
	)
	if failure != nil || request.Kind != attachmentRequestContent {
		return fail(classifyAttachmentMetadataURLFailure(downloadURL))
	}
	metadata := attachmentDownloadMetadata{DownloadURL: downloadURL}
	responseName, _ := payload["file_name"].(string)
	if responseName == "" {
		responseName, _ = payload["filename"].(string)
	}
	if responseName != "" {
		responseName = stableAttachmentName(responseName)
		if responseName == "" ||
			(candidate.FileName != "" && responseName != candidate.FileName) {
			return fail("chatgpt_attachment_metadata_name_mismatch")
		}
		metadata.FileName = responseName
	}
	metadata.MIMEType, _ = payload["mime_type"].(string)
	if metadata.MIMEType != "" &&
		normalizedAttachmentMIME(metadata.MIMEType) == "" {
		return fail("chatgpt_attachment_metadata_mime_invalid")
	}
	if rawSize, ok := payload["file_size_bytes"]; ok && rawSize != nil {
		size, _, valid := exactNonnegativeJSONInt(rawSize)
		if !valid {
			return fail("chatgpt_attachment_metadata_size_invalid")
		}
		metadata.Size = size
		metadata.SizePresent = true
	}
	return metadata, nil
}

func classifyAttachmentMetadataURLFailure(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil {
		return "chatgpt_attachment_metadata_url_invalid"
	}
	if parsed.Scheme != "" &&
		(!strings.EqualFold(parsed.Scheme, "https") ||
			!strings.EqualFold(parsed.Hostname(), "chatgpt.com") ||
			parsed.Port() != "") {
		return "chatgpt_attachment_metadata_url_outside_origin"
	}
	if parsed.Path != artifactContentRoute {
		return "chatgpt_attachment_metadata_url_path_unsupported"
	}
	if !validAttachmentContentQuery(parsed.Query()) {
		return "chatgpt_attachment_metadata_url_query_invalid"
	}
	return "chatgpt_attachment_metadata_url_unsupported"
}
