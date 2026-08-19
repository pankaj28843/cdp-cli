package transcriptionapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

// OpenAIHTTPProvider adapts a local or remote OpenAI-compatible audio service
// into the provider-neutral server contract. File and realtime endpoints may
// share one base URL or use separate HTTP/WebSocket origins.
type OpenAIHTTPProvider struct {
	ProviderID      ProviderID
	BaseURL         string
	RealtimeBaseURL string
	APIKey          string
	HTTPClient      *http.Client
	Translation     bool
	Realtime        bool
	ModelNames      []string
	Ready           bool
	ReadinessReason string
}

func NewOpenAIHTTPProvider(providerID ProviderID, baseURL, apiKey string) (*OpenAIHTTPProvider, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("OpenAI-compatible provider base URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("OpenAI-compatible provider base URL must be an absolute HTTP(S) URL")
	}
	if providerID == "" {
		providerID = ProviderLocal
	}
	return &OpenAIHTTPProvider{
		ProviderID:  providerID,
		BaseURL:     baseURL,
		APIKey:      strings.TrimSpace(apiKey),
		Translation: true,
		Realtime:    true,
		ModelNames:  []string{DefaultModel},
		Ready:       true,
	}, nil
}

func (p *OpenAIHTTPProvider) ID() ProviderID {
	if p == nil || p.ProviderID == "" {
		return ProviderLocal
	}
	return p.ProviderID
}

func (p *OpenAIHTTPProvider) Capabilities(context.Context) ProviderCapability {
	if p == nil {
		return ProviderCapability{Provider: ProviderLocal, Models: []string{DefaultModel}}
	}
	models := append([]string(nil), p.ModelNames...)
	if len(models) == 0 {
		models = []string{DefaultModel}
	}
	return ProviderCapability{
		Provider:    p.ID(),
		Models:      models,
		File:        true,
		Translation: p.Translation,
		Streaming:   true,
		Realtime:    p.Realtime,
		Ready:       p.Ready,
		Reason:      p.ReadinessReason,
	}
}

func (p *OpenAIHTTPProvider) Transcribe(ctx context.Context, request FileRequest) (Result, error) {
	if p == nil || !p.Ready {
		return Result{}, providerError(503, "provider_unavailable", "provider_not_ready", "OpenAI-compatible provider is not ready", false)
	}
	if request.Task == TaskTranslate && !p.Translation {
		return Result{}, providerError(422, "unsupported", "translation_unsupported", "provider does not support translation", false)
	}
	file, err := os.Open(request.Audio.PersistedPath)
	if err != nil {
		return Result{}, providerError(500, "internal", "audio_unreadable", "persisted audio could not be opened", false)
	}
	defer file.Close()

	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	part, err := multipartWriter.CreateFormFile("file", request.Audio.FileName)
	if err != nil {
		return Result{}, fmt.Errorf("create provider file part: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return Result{}, fmt.Errorf("read persisted audio: %w", err)
	}
	fields := map[string]string{
		"model":           request.Model,
		"response_format": string(request.ResponseFormat),
	}
	if request.Language != "" {
		fields["language"] = request.Language
	}
	if request.Prompt != "" {
		fields["prompt"] = request.Prompt
	}
	if request.Temperature != nil {
		fields["temperature"] = strconv.FormatFloat(*request.Temperature, 'f', -1, 64)
	}
	if request.Stream {
		fields["stream"] = "true"
	}
	for name, value := range fields {
		if err := multipartWriter.WriteField(name, value); err != nil {
			return Result{}, fmt.Errorf("write provider field %q: %w", name, err)
		}
	}
	for _, granularity := range request.TimestampGranularities {
		if err := multipartWriter.WriteField("timestamp_granularities[]", string(granularity)); err != nil {
			return Result{}, fmt.Errorf("write timestamp granularity: %w", err)
		}
	}
	for _, include := range request.Include {
		if err := multipartWriter.WriteField("include[]", include); err != nil {
			return Result{}, fmt.Errorf("write include field: %w", err)
		}
	}
	if err := multipartWriter.Close(); err != nil {
		return Result{}, fmt.Errorf("close provider multipart request: %w", err)
	}

	path := "/audio/transcriptions"
	if request.Task == TaskTranslate {
		path = "/audio/translations"
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+path, &body)
	if err != nil {
		return Result{}, fmt.Errorf("create provider request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	if p.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return Result{}, providerError(503, "connection", "provider_request_failed", "OpenAI-compatible provider request failed", true)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return Result{}, providerError(502, "provider", "provider_response_unreadable", "provider response could not be read", true)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, providerError(response.StatusCode, "provider", "provider_request_rejected", providerResponseMessage(responseBody), response.StatusCode >= 500 || response.StatusCode == 429)
	}
	result, parseErr := parseProviderResult(response.Header.Get("Content-Type"), responseBody, request.Task, request.ResponseFormat)
	if parseErr != nil {
		return Result{}, providerError(502, "provider", "provider_response_changed", parseErr.Error(), true)
	}
	return result, nil
}

func (p *OpenAIHTTPProvider) NewRealtime(ctx context.Context, config RealtimeSessionConfig) (RealtimeSession, error) {
	if p == nil || !p.Ready || !p.Realtime {
		return nil, providerError(501, "unsupported", "realtime_unsupported", "OpenAI-compatible provider does not expose realtime WebSocket input", false)
	}
	realtimeBaseURL := strings.TrimRight(strings.TrimSpace(p.RealtimeBaseURL), "/")
	if realtimeBaseURL == "" {
		realtimeBaseURL = p.BaseURL
	}
	parsed, err := url.Parse(realtimeBaseURL + "/realtime")
	if err != nil {
		return nil, providerError(503, "connection", "realtime_url_invalid", "realtime provider URL is invalid", false)
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return nil, providerError(503, "connection", "realtime_url_invalid", "realtime provider URL must use HTTP(S)", false)
	}
	query := parsed.Query()
	query.Set("model", config.Model)
	parsed.RawQuery = query.Encode()
	header := http.Header{}
	if p.APIKey != "" {
		header.Set("Authorization", "Bearer "+p.APIKey)
	}
	connection, _, err := websocket.Dial(ctx, parsed.String(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, providerError(503, "connection", "realtime_connect_failed", "OpenAI-compatible realtime provider could not be reached", true)
	}
	connection.SetReadLimit(8 << 20)
	readerContext, readerCancel := context.WithCancel(context.Background())
	session := &openAIRealtimeSession{
		connection:   connection,
		sessionID:    NewRequestID(),
		itemID:       "item-0",
		events:       make(chan map[string]any, 64),
		readerDone:   make(chan struct{}),
		readerCancel: readerCancel,
	}
	go session.readLoop(readerContext)
	if err := session.send(ctx, map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type":  "transcription",
			"model": config.Model,
			"audio": map[string]any{"input": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}}},
		},
	}); err != nil {
		_ = session.Close()
		return nil, providerError(502, "provider", "realtime_session_update_failed", "realtime provider session setup failed", true)
	}
	return session, nil
}

type openAIRealtimeSession struct {
	connection   *websocket.Conn
	sessionID    string
	itemID       string
	events       chan map[string]any
	readerDone   chan struct{}
	readerCancel context.CancelFunc
	closeOnce    sync.Once
}

func (s *openAIRealtimeSession) send(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.connection.Write(ctx, websocket.MessageText, payload)
}

func (s *openAIRealtimeSession) Append(ctx context.Context, audio []byte) ([]ProviderEvent, error) {
	if err := s.send(ctx, map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString(audio),
	}); err != nil {
		return nil, err
	}
	return s.readEventsUntilQuiet(ctx, 100*time.Millisecond), nil
}

func (s *openAIRealtimeSession) Commit(ctx context.Context) ([]ProviderEvent, error) {
	if err := s.send(ctx, map[string]any{"type": "input_audio_buffer.commit"}); err != nil {
		return nil, err
	}
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	events := make([]ProviderEvent, 0, 2)
	for {
		value, err := s.readWithTimeout(deadline, time.Second)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return events, providerError(504, "provider", "realtime_final_timeout", "realtime provider did not return a final transcription", true)
			}
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return events, ctx.Err()
			}
			return events, err
		}
		event, ok := s.providerEvent(value)
		if !ok {
			continue
		}
		events = append(events, event)
		if event.Kind == EventFinal || event.Kind == EventFailure {
			return events, nil
		}
	}
}

func (s *openAIRealtimeSession) readEventsUntilQuiet(ctx context.Context, quiet time.Duration) []ProviderEvent {
	events := make([]ProviderEvent, 0, 2)
	for {
		value, err := s.readWithTimeout(ctx, quiet)
		if err != nil {
			return events
		}
		event, ok := s.providerEvent(value)
		if ok {
			events = append(events, event)
		}
	}
}

func (s *openAIRealtimeSession) readWithTimeout(ctx context.Context, timeout time.Duration) (map[string]any, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case event, ok := <-s.events:
		if !ok {
			return nil, fmt.Errorf("realtime provider connection closed")
		}
		return event, nil
	case <-timer.C:
		return nil, context.DeadlineExceeded
	}
}

func (s *openAIRealtimeSession) readLoop(ctx context.Context) {
	defer close(s.readerDone)
	defer close(s.events)
	for {
		_, payload, err := s.connection.Read(ctx)
		if err != nil {
			return
		}
		var event map[string]any
		if err := json.Unmarshal(payload, &event); err != nil {
			continue
		}
		select {
		case s.events <- event:
		case <-ctx.Done():
			return
		}
	}
}

func (s *openAIRealtimeSession) providerEvent(value map[string]any) (ProviderEvent, bool) {
	eventType, _ := value["type"].(string)
	itemID, _ := value["item_id"].(string)
	if itemID == "" {
		itemID = s.itemID
	}
	sequence := int64(0)
	if raw, ok := value["sequence"].(float64); ok {
		sequence = int64(raw)
	}
	switch eventType {
	case "conversation.item.input_audio_transcription.delta":
		text, _ := value["delta"].(string)
		return ProviderEvent{SessionID: s.sessionID, ItemID: itemID, Sequence: sequence, Kind: EventHypothesis, Text: text}, text != ""
	case "conversation.item.input_audio_transcription.completed":
		text, _ := value["transcript"].(string)
		return ProviderEvent{SessionID: s.sessionID, ItemID: itemID, Sequence: sequence, Kind: EventFinal, Text: text}, true
	case "conversation.item.input_audio_transcription.failed":
		apiError := &APIError{Type: "provider_error", Message: "realtime transcription failed"}
		if rawError, ok := value["error"].(map[string]any); ok {
			if errorType, ok := rawError["type"].(string); ok && errorType != "" {
				apiError.Type = errorType
			}
			if code, ok := rawError["code"].(string); ok {
				apiError.Code = code
			}
			if message, ok := rawError["message"].(string); ok && message != "" {
				apiError.Message = message
			}
		}
		return ProviderEvent{SessionID: s.sessionID, ItemID: itemID, Sequence: sequence, Kind: EventFailure, Error: apiError}, true
	default:
		return ProviderEvent{}, false
	}
}

func (s *openAIRealtimeSession) Close() error {
	if s == nil || s.connection == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		if s.readerCancel != nil {
			s.readerCancel()
		}
		err = s.connection.Close(websocket.StatusNormalClosure, "done")
		select {
		case <-s.readerDone:
		case <-time.After(time.Second):
		}
	})
	return err
}

func parseProviderResult(contentType string, body []byte, task Task, responseFormat ResponseFormat) (Result, error) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return parseSSEProviderResult(body, task)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return Result{}, fmt.Errorf("provider returned an empty response")
	}
	if trimmed[0] != '{' {
		text := string(trimmed)
		rawText := ""
		if responseFormat == ResponseSRT || responseFormat == ResponseVTT {
			rawText = string(body)
			text = rawText
		}
		return Result{Task: task, Text: text, rawText: rawText}, nil
	}
	var result Result
	if err := json.Unmarshal(trimmed, &result); err != nil {
		return Result{}, fmt.Errorf("parse provider JSON response: %w", err)
	}
	result.Task = task
	return result, nil
}

func parseSSEProviderResult(body []byte, task Task) (Result, error) {
	var deltas strings.Builder
	var doneText string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Text  string `json:"text"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if event.Type == "transcript.text.done" && event.Text != "" {
			// The completed event is authoritative. It contains the complete
			// transcript, so do not append it after the preceding deltas.
			doneText = event.Text
			continue
		}
		if event.Delta != "" {
			deltas.WriteString(event.Delta)
		} else if event.Text != "" {
			deltas.WriteString(event.Text)
		}
	}
	text := doneText
	if text == "" {
		text = deltas.String()
	}
	if strings.TrimSpace(text) == "" {
		return Result{}, fmt.Errorf("provider SSE response contained no transcript text")
	}
	return Result{Task: task, Text: text}, nil
}

func providerResponseMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "provider rejected the transcription request"
	}
	var envelope ErrorEnvelope
	if json.Unmarshal([]byte(trimmed), &envelope) == nil && strings.TrimSpace(envelope.Error.Message) != "" {
		return envelope.Error.Message
	}
	return trimmed
}

// EncodeRealtimeAudio is shared by adapters and tests when constructing the
// OpenAI-shaped input_audio_buffer.append event.
func EncodeRealtimeAudio(audio []byte) string {
	return base64.StdEncoding.EncodeToString(audio)
}
