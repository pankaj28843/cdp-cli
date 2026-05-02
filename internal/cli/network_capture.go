package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type networkRequest struct {
	ID                string  `json:"id"`
	URL               string  `json:"url,omitempty"`
	Method            string  `json:"method,omitempty"`
	ResourceType      string  `json:"resource_type,omitempty"`
	Status            int     `json:"status,omitempty"`
	StatusText        string  `json:"status_text,omitempty"`
	MimeType          string  `json:"mime_type,omitempty"`
	Failed            bool    `json:"failed"`
	ErrorText         string  `json:"error_text,omitempty"`
	Canceled          bool    `json:"canceled,omitempty"`
	EncodedDataLength float64 `json:"encoded_data_length,omitempty"`
}

type networkCaptureOptions struct {
	Wait                  time.Duration
	Limit                 int
	IncludeHeaders        bool
	IncludeInitiators     bool
	IncludeTiming         bool
	IncludePostData       bool
	BodyKinds             map[string]bool
	BodyLimit             int
	IncludeWebSockets     bool
	WebSocketPayloads     bool
	WebSocketPayloadLimit int
	AfterEnable           func() error
}

type networkCaptureRecord struct {
	ID                 string                   `json:"id"`
	URL                string                   `json:"url,omitempty"`
	Method             string                   `json:"method,omitempty"`
	ResourceType       string                   `json:"resource_type,omitempty"`
	DocumentURL        string                   `json:"document_url,omitempty"`
	LoaderID           string                   `json:"loader_id,omitempty"`
	Timestamp          float64                  `json:"timestamp,omitempty"`
	WallTime           float64                  `json:"wall_time,omitempty"`
	RequestHeaders     map[string]any           `json:"request_headers,omitempty"`
	RequestPostData    *networkCaptureBody      `json:"request_post_data,omitempty"`
	RequestHasPostData bool                     `json:"-"`
	ResponseHeaders    map[string]any           `json:"response_headers,omitempty"`
	Status             int                      `json:"status,omitempty"`
	StatusText         string                   `json:"status_text,omitempty"`
	MimeType           string                   `json:"mime_type,omitempty"`
	Protocol           string                   `json:"protocol,omitempty"`
	RemoteIPAddress    string                   `json:"remote_ip_address,omitempty"`
	RemotePort         int                      `json:"remote_port,omitempty"`
	ConnectionID       float64                  `json:"connection_id,omitempty"`
	ConnectionReused   bool                     `json:"connection_reused,omitempty"`
	FromDiskCache      bool                     `json:"from_disk_cache,omitempty"`
	FromServiceWorker  bool                     `json:"from_service_worker,omitempty"`
	EncodedDataLength  float64                  `json:"encoded_data_length,omitempty"`
	DecodedBodyLength  float64                  `json:"decoded_body_length,omitempty"`
	Initiator          json.RawMessage          `json:"initiator,omitempty"`
	Timing             json.RawMessage          `json:"timing,omitempty"`
	Redirects          []networkCaptureRecord   `json:"redirects,omitempty"`
	Body               *networkCaptureBody      `json:"body,omitempty"`
	Failed             bool                     `json:"failed"`
	ErrorText          string                   `json:"error_text,omitempty"`
	Canceled           bool                     `json:"canceled,omitempty"`
	WebSocket          *networkWebSocketCapture `json:"websocket,omitempty"`
}

type networkWebSocketCapture struct {
	RequestID       string                  `json:"request_id"`
	URL             string                  `json:"url,omitempty"`
	Initiator       json.RawMessage         `json:"initiator,omitempty"`
	RequestHeaders  map[string]any          `json:"request_headers,omitempty"`
	ResponseHeaders map[string]any          `json:"response_headers,omitempty"`
	Status          int                     `json:"status,omitempty"`
	StatusText      string                  `json:"status_text,omitempty"`
	Frames          []networkWebSocketFrame `json:"frames,omitempty"`
	Errors          []networkWebSocketError `json:"errors,omitempty"`
	Closed          bool                    `json:"closed,omitempty"`
	CreatedAt       float64                 `json:"created_at,omitempty"`
	ClosedAt        float64                 `json:"closed_at,omitempty"`
}

type networkWebSocketFrame struct {
	Direction string              `json:"direction"`
	Opcode    float64             `json:"opcode,omitempty"`
	Mask      bool                `json:"mask,omitempty"`
	Payload   *networkCaptureBody `json:"payload,omitempty"`
	Timestamp float64             `json:"timestamp,omitempty"`
}

type networkWebSocketError struct {
	ErrorMessage string  `json:"error_message"`
	Timestamp    float64 `json:"timestamp,omitempty"`
}

type networkCaptureBody struct {
	Text          string `json:"text,omitempty"`
	Base64Encoded bool   `json:"base64_encoded,omitempty"`
	Bytes         int    `json:"bytes"`
	Truncated     bool   `json:"truncated,omitempty"`
	OmittedReason string `json:"omitted_reason,omitempty"`
}

func collectNetworkRequests(ctx context.Context, client browserEventClient, sessionID string, wait time.Duration, limit int, failedOnly bool) ([]networkRequest, bool, error) {
	if err := client.CallSession(ctx, sessionID, "Network.enable", map[string]any{}, nil); err != nil {
		return nil, false, err
	}

	requestsByID := map[string]*networkRequest{}
	var order []string
	addEvent := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		req, ok := networkRequestFromEvent(event)
		if !ok || req.ID == "" {
			return
		}
		existing, ok := requestsByID[req.ID]
		if !ok {
			copyReq := req
			requestsByID[req.ID] = &copyReq
			order = append(order, req.ID)
			return
		}
		mergeNetworkRequest(existing, req)
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, event := range events {
		addEvent(event)
	}
	if wait > 0 {
		eventCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for {
			event, err := client.ReadEvent(eventCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded) {
					break
				}
				return nil, false, err
			}
			addEvent(event)
		}
	}

	var requests []networkRequest
	for _, id := range order {
		req := *requestsByID[id]
		if failedOnly && !requestFailed(req) {
			continue
		}
		requests = append(requests, req)
	}
	truncated := false
	if limit > 0 && len(requests) > limit {
		requests = requests[:limit]
		truncated = true
	}
	return requests, truncated, nil
}

func collectNetworkCapture(ctx context.Context, client browserEventClient, sessionID string, opts networkCaptureOptions) ([]networkCaptureRecord, bool, []map[string]string, error) {
	enableParams := map[string]any{}
	if opts.BodyLimit > 0 {
		enableParams["maxPostDataSize"] = opts.BodyLimit
	}
	if err := client.CallSession(ctx, sessionID, "Network.enable", enableParams, nil); err != nil {
		return nil, false, nil, err
	}
	collectorErrors := []map[string]string{}
	if opts.AfterEnable != nil {
		if err := opts.AfterEnable(); err != nil {
			collectorErrors = append(collectorErrors, collectorError("trigger", err))
		}
	}

	recordsByID := map[string]*networkCaptureRecord{}
	var order []string
	ensure := func(id string) *networkCaptureRecord {
		record, ok := recordsByID[id]
		if !ok {
			record = &networkCaptureRecord{ID: id}
			recordsByID[id] = record
			order = append(order, id)
		}
		return record
	}
	addEvent := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		switch event.Method {
		case "Network.requestWillBeSent":
			mergeCaptureRequestWillBeSent(event.Params, ensure, opts)
		case "Network.requestWillBeSentExtraInfo":
			if opts.IncludeHeaders {
				mergeCaptureRequestExtraInfo(event.Params, ensure)
			}
		case "Network.responseReceived":
			mergeCaptureResponseReceived(event.Params, ensure, opts)
		case "Network.responseReceivedExtraInfo":
			if opts.IncludeHeaders {
				mergeCaptureResponseExtraInfo(event.Params, ensure)
			}
		case "Network.loadingFinished":
			mergeCaptureLoadingFinished(event.Params, ensure)
		case "Network.loadingFailed":
			mergeCaptureLoadingFailed(event.Params, ensure)
		case "Network.webSocketCreated":
			if opts.IncludeWebSockets {
				mergeCaptureWebSocketCreated(event.Params, ensure, opts)
			}
		case "Network.webSocketWillSendHandshakeRequest":
			if opts.IncludeWebSockets {
				mergeCaptureWebSocketWillSendHandshakeRequest(event.Params, ensure, opts)
			}
		case "Network.webSocketHandshakeResponseReceived":
			if opts.IncludeWebSockets {
				mergeCaptureWebSocketHandshakeResponseReceived(event.Params, ensure, opts)
			}
		case "Network.webSocketFrameSent":
			if opts.IncludeWebSockets {
				mergeCaptureWebSocketFrame(event.Params, ensure, opts, "sent")
			}
		case "Network.webSocketFrameReceived":
			if opts.IncludeWebSockets {
				mergeCaptureWebSocketFrame(event.Params, ensure, opts, "received")
			}
		case "Network.webSocketFrameError":
			if opts.IncludeWebSockets {
				mergeCaptureWebSocketFrameError(event.Params, ensure)
			}
		case "Network.webSocketClosed":
			if opts.IncludeWebSockets {
				mergeCaptureWebSocketClosed(event.Params, ensure)
			}
		}
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return nil, false, nil, err
	}
	for _, event := range events {
		addEvent(event)
	}
	if opts.Wait > 0 {
		eventCtx, cancel := context.WithTimeout(ctx, opts.Wait)
		defer cancel()
		for {
			event, err := client.ReadEvent(eventCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded) {
					break
				}
				return nil, false, nil, err
			}
			addEvent(event)
		}
	}

	for _, id := range order {
		record := recordsByID[id]
		if opts.IncludePostData && record.RequestHasPostData {
			if err := enrichRequestPostData(ctx, client, sessionID, record, opts.BodyLimit); err != nil {
				collectorErrors = append(collectorErrors, collectorError("request_post_data", err))
			}
		}
		if len(opts.BodyKinds) > 0 && shouldCaptureResponseBody(*record, opts.BodyKinds) {
			if err := enrichResponseBody(ctx, client, sessionID, record, opts.BodyLimit); err != nil {
				collectorErrors = append(collectorErrors, collectorError("response_body", err))
			}
		}
	}

	records := make([]networkCaptureRecord, 0, len(order))
	for _, id := range order {
		records = append(records, *recordsByID[id])
	}
	truncated := false
	if opts.Limit > 0 && len(records) > opts.Limit {
		records = records[:opts.Limit]
		truncated = true
	}
	return records, truncated, collectorErrors, nil
}

func mergeCaptureRequestWillBeSent(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord, opts networkCaptureOptions) {
	var params struct {
		RequestID        string          `json:"requestId"`
		LoaderID         string          `json:"loaderId"`
		DocumentURL      string          `json:"documentURL"`
		Type             string          `json:"type"`
		Timestamp        float64         `json:"timestamp"`
		WallTime         float64         `json:"wallTime"`
		Initiator        json.RawMessage `json:"initiator"`
		RedirectResponse json.RawMessage `json:"redirectResponse"`
		Request          struct {
			URL         string         `json:"url"`
			Method      string         `json:"method"`
			Headers     map[string]any `json:"headers"`
			HasPostData bool           `json:"hasPostData"`
			PostData    string         `json:"postData"`
		} `json:"request"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	if len(params.RedirectResponse) > 0 && string(params.RedirectResponse) != "null" {
		if redirect := captureRedirectFromResponse(params.RedirectResponse, opts); redirect.Status != 0 || redirect.URL != "" {
			record.Redirects = append(record.Redirects, redirect)
		}
	}
	record.URL = params.Request.URL
	record.Method = params.Request.Method
	record.ResourceType = params.Type
	record.DocumentURL = params.DocumentURL
	record.LoaderID = params.LoaderID
	record.Timestamp = params.Timestamp
	record.WallTime = params.WallTime
	record.RequestHasPostData = params.Request.HasPostData || params.Request.PostData != ""
	if opts.IncludeHeaders && len(params.Request.Headers) > 0 {
		record.RequestHeaders = params.Request.Headers
	}
	if opts.IncludePostData && params.Request.PostData != "" {
		record.RequestPostData = captureBody(params.Request.PostData, false, opts.BodyLimit)
	}
	if opts.IncludeInitiators && len(params.Initiator) > 0 && string(params.Initiator) != "null" {
		record.Initiator = params.Initiator
	}
}

func captureRedirectFromResponse(raw json.RawMessage, opts networkCaptureOptions) networkCaptureRecord {
	var response struct {
		URL          string          `json:"url"`
		Status       int             `json:"status"`
		StatusText   string          `json:"statusText"`
		Headers      map[string]any  `json:"headers"`
		MimeType     string          `json:"mimeType"`
		Protocol     string          `json:"protocol"`
		RemoteIP     string          `json:"remoteIPAddress"`
		RemotePort   int             `json:"remotePort"`
		Timing       json.RawMessage `json:"timing"`
		EncodedBytes float64         `json:"encodedDataLength"`
	}
	_ = json.Unmarshal(raw, &response)
	redirect := networkCaptureRecord{
		URL:               response.URL,
		Status:            response.Status,
		StatusText:        response.StatusText,
		MimeType:          response.MimeType,
		Protocol:          response.Protocol,
		RemoteIPAddress:   response.RemoteIP,
		RemotePort:        response.RemotePort,
		EncodedDataLength: response.EncodedBytes,
	}
	if opts.IncludeHeaders {
		redirect.ResponseHeaders = response.Headers
	}
	if opts.IncludeTiming {
		redirect.Timing = response.Timing
	}
	return redirect
}

func mergeCaptureRequestExtraInfo(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord) {
	var params struct {
		RequestID string         `json:"requestId"`
		Headers   map[string]any `json:"headers"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	if len(params.Headers) > 0 {
		ensure(params.RequestID).RequestHeaders = params.Headers
	}
}

func mergeCaptureResponseReceived(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord, opts networkCaptureOptions) {
	var params struct {
		RequestID string `json:"requestId"`
		Type      string `json:"type"`
		Response  struct {
			URL               string          `json:"url"`
			Status            int             `json:"status"`
			StatusText        string          `json:"statusText"`
			Headers           map[string]any  `json:"headers"`
			MimeType          string          `json:"mimeType"`
			Protocol          string          `json:"protocol"`
			RemoteIPAddress   string          `json:"remoteIPAddress"`
			RemotePort        int             `json:"remotePort"`
			ConnectionID      float64         `json:"connectionId"`
			ConnectionReused  bool            `json:"connectionReused"`
			FromDiskCache     bool            `json:"fromDiskCache"`
			FromServiceWorker bool            `json:"fromServiceWorker"`
			EncodedDataLength float64         `json:"encodedDataLength"`
			Timing            json.RawMessage `json:"timing"`
		} `json:"response"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	record.ResourceType = firstNonEmpty(record.ResourceType, params.Type)
	record.URL = firstNonEmpty(params.Response.URL, record.URL)
	record.Status = params.Response.Status
	record.StatusText = params.Response.StatusText
	record.MimeType = params.Response.MimeType
	record.Protocol = params.Response.Protocol
	record.RemoteIPAddress = params.Response.RemoteIPAddress
	record.RemotePort = params.Response.RemotePort
	record.ConnectionID = params.Response.ConnectionID
	record.ConnectionReused = params.Response.ConnectionReused
	record.FromDiskCache = params.Response.FromDiskCache
	record.FromServiceWorker = params.Response.FromServiceWorker
	record.EncodedDataLength = params.Response.EncodedDataLength
	if opts.IncludeHeaders && len(params.Response.Headers) > 0 {
		record.ResponseHeaders = params.Response.Headers
	}
	if opts.IncludeTiming && len(params.Response.Timing) > 0 && string(params.Response.Timing) != "null" {
		record.Timing = params.Response.Timing
	}
}

func mergeCaptureResponseExtraInfo(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord) {
	var params struct {
		RequestID  string         `json:"requestId"`
		StatusCode int            `json:"statusCode"`
		Headers    map[string]any `json:"headers"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	if params.StatusCode != 0 {
		record.Status = params.StatusCode
	}
	if len(params.Headers) > 0 {
		record.ResponseHeaders = params.Headers
	}
}

func mergeCaptureLoadingFinished(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord) {
	var params struct {
		RequestID         string  `json:"requestId"`
		EncodedDataLength float64 `json:"encodedDataLength"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	ensure(params.RequestID).EncodedDataLength = params.EncodedDataLength
}

func mergeCaptureLoadingFailed(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord) {
	var params struct {
		RequestID string `json:"requestId"`
		Type      string `json:"type"`
		ErrorText string `json:"errorText"`
		Canceled  bool   `json:"canceled"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	record.ResourceType = firstNonEmpty(record.ResourceType, params.Type)
	record.Failed = true
	record.ErrorText = params.ErrorText
	record.Canceled = params.Canceled
}

func enrichRequestPostData(ctx context.Context, client browserEventClient, sessionID string, record *networkCaptureRecord, bodyLimit int) error {
	if record.RequestPostData != nil {
		return nil
	}
	var result struct {
		PostData string `json:"postData"`
	}
	err := client.CallSession(ctx, sessionID, "Network.getRequestPostData", map[string]any{"requestId": record.ID}, &result)
	if err != nil {
		record.RequestPostData = &networkCaptureBody{OmittedReason: err.Error()}
		return nil
	}
	record.RequestPostData = captureBody(result.PostData, false, bodyLimit)
	return nil
}

func enrichResponseBody(ctx context.Context, client browserEventClient, sessionID string, record *networkCaptureRecord, bodyLimit int) error {
	var result struct {
		Body          string `json:"body"`
		Base64Encoded bool   `json:"base64Encoded"`
	}
	err := client.CallSession(ctx, sessionID, "Network.getResponseBody", map[string]any{"requestId": record.ID}, &result)
	if err != nil {
		record.Body = &networkCaptureBody{OmittedReason: err.Error()}
		return nil
	}
	record.Body = captureBody(result.Body, result.Base64Encoded, bodyLimit)
	if !result.Base64Encoded {
		record.DecodedBodyLength = float64(record.Body.Bytes)
	}
	return nil
}

func mergeCaptureWebSocketCreated(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord, opts networkCaptureOptions) {
	var params struct {
		RequestID string          `json:"requestId"`
		URL       string          `json:"url"`
		Initiator json.RawMessage `json:"initiator"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	record.ResourceType = "WebSocket"
	record.URL = params.URL
	ws := ensureWebSocket(record, params.RequestID)
	ws.URL = params.URL
	if opts.IncludeInitiators && len(params.Initiator) > 0 && string(params.Initiator) != "null" {
		ws.Initiator = params.Initiator
	}
}

func mergeCaptureWebSocketWillSendHandshakeRequest(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord, opts networkCaptureOptions) {
	var params struct {
		RequestID string  `json:"requestId"`
		Timestamp float64 `json:"timestamp"`
		WallTime  float64 `json:"wallTime"`
		Request   struct {
			Headers map[string]any `json:"headers"`
		} `json:"request"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	record.ResourceType = "WebSocket"
	record.Timestamp = params.Timestamp
	record.WallTime = params.WallTime
	ws := ensureWebSocket(record, params.RequestID)
	ws.CreatedAt = params.Timestamp
	if opts.IncludeHeaders && len(params.Request.Headers) > 0 {
		ws.RequestHeaders = params.Request.Headers
	}
}

func mergeCaptureWebSocketHandshakeResponseReceived(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord, opts networkCaptureOptions) {
	var params struct {
		RequestID string `json:"requestId"`
		Response  struct {
			Status     int            `json:"status"`
			StatusText string         `json:"statusText"`
			Headers    map[string]any `json:"headers"`
		} `json:"response"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	record.ResourceType = "WebSocket"
	ws := ensureWebSocket(record, params.RequestID)
	ws.Status = params.Response.Status
	ws.StatusText = params.Response.StatusText
	if opts.IncludeHeaders && len(params.Response.Headers) > 0 {
		ws.ResponseHeaders = params.Response.Headers
	}
}

func mergeCaptureWebSocketFrame(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord, opts networkCaptureOptions, direction string) {
	var params struct {
		RequestID string  `json:"requestId"`
		Timestamp float64 `json:"timestamp"`
		Response  struct {
			Opcode      float64 `json:"opcode"`
			Mask        bool    `json:"mask"`
			PayloadData string  `json:"payloadData"`
		} `json:"response"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	record.ResourceType = "WebSocket"
	frame := networkWebSocketFrame{Direction: direction, Opcode: params.Response.Opcode, Mask: params.Response.Mask, Timestamp: params.Timestamp}
	if opts.WebSocketPayloads {
		frame.Payload = captureBody(params.Response.PayloadData, false, opts.WebSocketPayloadLimit)
	}
	ws := ensureWebSocket(record, params.RequestID)
	ws.Frames = append(ws.Frames, frame)
}

func mergeCaptureWebSocketFrameError(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord) {
	var params struct {
		RequestID    string  `json:"requestId"`
		Timestamp    float64 `json:"timestamp"`
		ErrorMessage string  `json:"errorMessage"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	record.ResourceType = "WebSocket"
	ws := ensureWebSocket(record, params.RequestID)
	ws.Errors = append(ws.Errors, networkWebSocketError{ErrorMessage: params.ErrorMessage, Timestamp: params.Timestamp})
}

func mergeCaptureWebSocketClosed(paramsRaw json.RawMessage, ensure func(string) *networkCaptureRecord) {
	var params struct {
		RequestID string  `json:"requestId"`
		Timestamp float64 `json:"timestamp"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params.RequestID == "" {
		return
	}
	record := ensure(params.RequestID)
	record.ResourceType = "WebSocket"
	ws := ensureWebSocket(record, params.RequestID)
	ws.Closed = true
	ws.ClosedAt = params.Timestamp
}

func ensureWebSocket(record *networkCaptureRecord, requestID string) *networkWebSocketCapture {
	if record.WebSocket == nil {
		record.WebSocket = &networkWebSocketCapture{RequestID: requestID}
	}
	return record.WebSocket
}

func captureBody(text string, base64Encoded bool, limit int) *networkCaptureBody {
	bytes := len([]byte(text))
	body := &networkCaptureBody{Text: text, Base64Encoded: base64Encoded, Bytes: bytes}
	if limit > 0 && bytes > limit {
		body.Text = string([]byte(text)[:limit])
		body.Truncated = true
	}
	return body
}

func parseBodyKinds(includeBodies string) map[string]bool {
	set := parseCSVSet(includeBodies)
	if set["all"] {
		return parseCSVSet("json,text,base64")
	}
	if set["none"] {
		return map[string]bool{}
	}
	return set
}

func invalidBodyKinds(kinds map[string]bool) []string {
	var invalid []string
	for kind := range kinds {
		if kind != "json" && kind != "text" && kind != "base64" && kind != "all" && kind != "none" {
			invalid = append(invalid, kind)
		}
	}
	sort.Strings(invalid)
	return invalid
}

func shouldCaptureResponseBody(record networkCaptureRecord, kinds map[string]bool) bool {
	if len(kinds) == 0 || record.Failed {
		return false
	}
	mime := strings.ToLower(record.MimeType)
	if kinds["base64"] {
		return true
	}
	if kinds["json"] && strings.Contains(mime, "json") {
		return true
	}
	if kinds["text"] && (strings.HasPrefix(mime, "text/") || strings.Contains(mime, "javascript") || strings.Contains(mime, "xml")) {
		return true
	}
	return false
}

func applyNetworkCaptureRedaction(records []networkCaptureRecord, redact string) {
	if redact == "" || redact == "none" {
		return
	}
	for i := range records {
		redactCaptureRecord(&records[i], redact)
	}
}

func redactCaptureRecord(record *networkCaptureRecord, redact string) {
	record.URL = redactURL(record.URL, redact)
	record.DocumentURL = redactURL(record.DocumentURL, redact)
	record.RequestHeaders = redactHeaderMap(record.RequestHeaders, redact)
	record.ResponseHeaders = redactHeaderMap(record.ResponseHeaders, redact)
	if record.RequestPostData != nil && record.RequestPostData.Text != "" {
		record.RequestPostData.Text = redactBodyText(record.RequestPostData.Text, redact)
	}
	if record.Body != nil && record.Body.Text != "" {
		record.Body.Text = redactBodyText(record.Body.Text, redact)
	}
	if record.WebSocket != nil {
		record.WebSocket.URL = redactURL(record.WebSocket.URL, redact)
		record.WebSocket.RequestHeaders = redactHeaderMap(record.WebSocket.RequestHeaders, redact)
		record.WebSocket.ResponseHeaders = redactHeaderMap(record.WebSocket.ResponseHeaders, redact)
		for i := range record.WebSocket.Frames {
			if record.WebSocket.Frames[i].Payload != nil && record.WebSocket.Frames[i].Payload.Text != "" {
				record.WebSocket.Frames[i].Payload.Text = redactBodyText(record.WebSocket.Frames[i].Payload.Text, redact)
			}
		}
	}
	for i := range record.Redirects {
		redactCaptureRecord(&record.Redirects[i], redact)
	}
}

func redactHeaderMap(headers map[string]any, redact string) map[string]any {
	if len(headers) == 0 {
		return headers
	}
	out := map[string]any{}
	for key, value := range headers {
		if redact == "headers" || sensitiveName(key) || sensitiveHeaderValue(value) {
			out[key] = "<redacted>"
			continue
		}
		out[key] = value
	}
	return out
}

func sensitiveHeaderValue(value any) bool {
	text, ok := value.(string)
	return ok && strings.Contains(strings.ToLower(text), "bearer ")
}

func redactURL(rawURL, redact string) string {
	if rawURL == "" || redact != "safe" {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		if sensitiveName(key) {
			query.Set(key, "<redacted>")
			changed = true
		}
	}
	if changed {
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}

func redactBodyText(text, redact string) string {
	if redact == "headers" {
		return "<redacted>"
	}
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err == nil {
		return marshalCompact(redactJSONValue(decoded))
	}
	values, err := url.ParseQuery(text)
	if err == nil && len(values) > 0 {
		changed := false
		for key := range values {
			if sensitiveName(key) {
				values.Set(key, "<redacted>")
				changed = true
			}
		}
		if changed {
			return values.Encode()
		}
	}
	if strings.Contains(strings.ToLower(text), "bearer ") {
		return "<redacted>"
	}
	return text
}

func redactJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, child := range typed {
			if sensitiveName(key) {
				out[key] = "<redacted>"
			} else {
				out[key] = redactJSONValue(child)
			}
		}
		return out
	case []any:
		for i := range typed {
			typed[i] = redactJSONValue(typed[i])
		}
		return typed
	default:
		return value
	}
}

func marshalCompact(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return "<redacted>"
	}
	return string(b)
}

func sensitiveName(name string) bool {
	lower := strings.ToLower(name)
	for _, needle := range []string{"authorization", "cookie", "csrf", "xsrf", "token", "secret", "password", "session", "client-transaction-id"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func networkRequestFromEvent(event cdp.Event) (networkRequest, bool) {
	switch event.Method {
	case "Network.requestWillBeSent":
		var params struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			Request   struct {
				URL    string `json:"url"`
				Method string `json:"method"`
			} `json:"request"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return networkRequest{}, false
		}
		return networkRequest{ID: params.RequestID, URL: params.Request.URL, Method: params.Request.Method, ResourceType: params.Type}, true
	case "Network.responseReceived":
		var params struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			Response  struct {
				URL        string `json:"url"`
				Status     int    `json:"status"`
				StatusText string `json:"statusText"`
				MimeType   string `json:"mimeType"`
			} `json:"response"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return networkRequest{}, false
		}
		return networkRequest{ID: params.RequestID, URL: params.Response.URL, ResourceType: params.Type, Status: params.Response.Status, StatusText: params.Response.StatusText, MimeType: params.Response.MimeType}, true
	case "Network.loadingFailed":
		var params struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			ErrorText string `json:"errorText"`
			Canceled  bool   `json:"canceled"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return networkRequest{}, false
		}
		return networkRequest{ID: params.RequestID, ResourceType: params.Type, Failed: true, ErrorText: params.ErrorText, Canceled: params.Canceled}, true
	case "Network.loadingFinished":
		var params struct {
			RequestID         string  `json:"requestId"`
			EncodedDataLength float64 `json:"encodedDataLength"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return networkRequest{}, false
		}
		return networkRequest{ID: params.RequestID, EncodedDataLength: params.EncodedDataLength}, true
	default:
		return networkRequest{}, false
	}
}

func mergeNetworkRequest(dst *networkRequest, src networkRequest) {
	if src.URL != "" {
		dst.URL = src.URL
	}
	if src.Method != "" {
		dst.Method = src.Method
	}
	if src.ResourceType != "" {
		dst.ResourceType = src.ResourceType
	}
	if src.Status != 0 {
		dst.Status = src.Status
	}
	if src.StatusText != "" {
		dst.StatusText = src.StatusText
	}
	if src.MimeType != "" {
		dst.MimeType = src.MimeType
	}
	if src.Failed {
		dst.Failed = true
	}
	if src.ErrorText != "" {
		dst.ErrorText = src.ErrorText
	}
	if src.Canceled {
		dst.Canceled = true
	}
	if src.EncodedDataLength != 0 {
		dst.EncodedDataLength = src.EncodedDataLength
	}
}

func requestFailed(req networkRequest) bool {
	return req.Failed || req.Status >= 400
}

func networkRequestLines(requests []networkRequest) []string {
	lines := make([]string, 0, len(requests))
	for _, req := range requests {
		status := "pending"
		if req.Failed {
			status = "failed"
		} else if req.Status > 0 {
			status = fmt.Sprint(req.Status)
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", req.ID, status, req.Method, req.URL))
	}
	return lines
}
