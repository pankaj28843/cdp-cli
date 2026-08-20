package transcriptionapi

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/resilience"
	"nhooyr.io/websocket"
)

const (
	// Keep the user-facing transcription listener in the documented high-port
	// range so it does not collide with legacy loopback services.
	DefaultListenAddress = "localhost:28765"
	maxRequestBodyBytes  = MaxUploadBytes + 2*1024*1024
)

type ServerConfig struct {
	Registry        *Registry
	Store           *Store
	DefaultProvider ProviderID
	BearerToken     string
	Address         string
	HTTPAddress     string
	TLSCertFile     string
	TLSKeyFile      string
	AuthCoordinator *AuthRefreshCoordinator
}

type Server struct {
	config     ServerConfig
	httpServer *http.Server
}

func NewServer(config ServerConfig) (*Server, error) {
	if config.Registry == nil {
		config.Registry = NewRegistry()
	}
	if config.Store == nil {
		return nil, fmt.Errorf("transcription store is required")
	}
	if strings.TrimSpace(config.Address) == "" {
		config.Address = DefaultListenAddress
	}
	config.Address = strings.TrimSpace(config.Address)
	config.HTTPAddress = strings.TrimSpace(config.HTTPAddress)
	if config.HTTPAddress != "" && config.HTTPAddress == config.Address {
		return nil, fmt.Errorf("transcription HTTP address must differ from the primary address")
	}
	if (strings.TrimSpace(config.TLSCertFile) == "") != (strings.TrimSpace(config.TLSKeyFile) == "") {
		return nil, fmt.Errorf("transcription TLS certificate and key must be provided together")
	}
	server := &Server{config: config}
	server.httpServer = &http.Server{
		Addr:              config.Address,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	return server, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleDemo)
	mux.HandleFunc("/demo.html", s.handleDemo)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/openapi.json", s.handleOpenAPI)
	mux.HandleFunc("/v1/models", s.withAuth(s.handleModels))
	mux.HandleFunc("/v1/audio/transcriptions", s.withAuth(func(w http.ResponseWriter, r *http.Request) {
		s.handleFile(w, r, TaskTranscribe)
	}))
	mux.HandleFunc("/v1/audio/translations", s.withAuth(func(w http.ResponseWriter, r *http.Request) {
		s.handleFile(w, r, TaskTranslate)
	}))
	mux.HandleFunc("/v1/realtime", s.withAuth(s.handleRealtime))
	return mux
}

func (s *Server) handleDemo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/demo.html" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, APIError{Type: "invalid_request_error", Message: "method not allowed"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(DemoHTML())
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if s == nil || s.httpServer == nil {
		return fmt.Errorf("transcription server is not configured")
	}
	defer func() { _ = s.config.Store.Close() }()
	if ctx == nil {
		ctx = context.Background()
	}
	if s.config.AuthCoordinator != nil {
		s.config.AuthCoordinator.Start(ctx)
	}
	rawListener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen for transcription API on %s: %w", s.httpServer.Addr, err)
	}
	listener := rawListener
	if strings.TrimSpace(s.config.TLSCertFile) != "" {
		certificate, err := tls.LoadX509KeyPair(s.config.TLSCertFile, s.config.TLSKeyFile)
		if err != nil {
			_ = rawListener.Close()
			return fmt.Errorf("load transcription TLS certificate: %w", err)
		}
		listener = tls.NewListener(rawListener, &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		})
	}
	listeners := []net.Listener{listener}
	if s.config.HTTPAddress != "" {
		httpListener, listenErr := net.Listen("tcp", s.config.HTTPAddress)
		if listenErr != nil {
			_ = rawListener.Close()
			return fmt.Errorf("listen for transcription HTTP API on %s: %w", s.config.HTTPAddress, listenErr)
		}
		listeners = append(listeners, httpListener)
	}
	serveErr := make(chan error, len(listeners))
	for _, currentListener := range listeners {
		go func(listener net.Listener) {
			serveErr <- s.httpServer.Serve(listener)
		}(currentListener)
	}
	shutdown := func() error {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownContext)
	}
	select {
	case <-ctx.Done():
		if err := shutdown(); err != nil {
			return fmt.Errorf("shutdown transcription API: %w", err)
		}
		for range listeners {
			if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("serve transcription API: %w", err)
			}
		}
		return ctx.Err()
	case err := <-serveErr:
		if shutdownErr := shutdown(); shutdownErr != nil {
			return fmt.Errorf("shutdown transcription API: %w", shutdownErr)
		}
		for index := 1; index < len(listeners); index++ {
			<-serveErr
		}
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve transcription API: %w", err)
	}
}

func (s *Server) withAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(s.config.BearerToken) != "" && !authorized(r, s.config.BearerToken) {
			writeAPIError(w, http.StatusUnauthorized, APIError{
				Type:    "authentication_error",
				Code:    "invalid_api_key",
				Message: "a valid local bearer token is required",
			})
			return
		}
		handler(w, r)
	}
}

func authorized(request *http.Request, expected string) bool {
	const prefix = "Bearer "
	value := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, APIError{Type: "invalid_request_error", Message: "method not allowed"})
		return
	}
	capabilities := s.config.Registry.Capabilities(r.Context())
	status := "degraded"
	for _, capability := range capabilities {
		if capability.Ready {
			status = "ok"
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           status,
		"contract_version": ContractVersion,
		"transport":        requestTransport(r),
		"default_provider": s.config.DefaultProvider,
		"listeners":        s.listenerHealth(),
		"providers":        capabilities,
		"observability": map[string]any{
			"request_records": true,
			"trace_file":      true,
		},
	})
}

type healthListener struct {
	Scheme  string `json:"scheme"`
	Address string `json:"address"`
	TLS     bool   `json:"tls"`
}

func requestTransport(r *http.Request) string {
	if r != nil && r.TLS != nil {
		return "https"
	}
	return "http"
}

func (s *Server) listenerHealth() []healthListener {
	primary := healthListener{Scheme: "http", Address: s.config.Address}
	if strings.TrimSpace(s.config.TLSCertFile) != "" {
		primary.Scheme = "https"
		primary.TLS = true
	}
	listeners := []healthListener{primary}
	if s.config.HTTPAddress != "" {
		listeners = append(listeners, healthListener{Scheme: "http", Address: s.config.HTTPAddress})
	}
	return listeners
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, APIError{Type: "invalid_request_error", Message: "method not allowed"})
		return
	}
	w.Header().Set("Content-Type", "application/vnd.oai.openapi+json;version=3.1")
	_, _ = w.Write(OpenAPISpec())
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, APIError{Type: "invalid_request_error", Message: "method not allowed"})
		return
	}
	models := make([]Model, 0)
	for _, capability := range s.config.Registry.Capabilities(r.Context()) {
		for _, model := range capability.Models {
			models = append(models, Model{
				ID:       model,
				Object:   "model",
				OwnedBy:  "voxinput",
				Provider: capability.Provider,
				Ready:    capability.Ready,
			})
		}
	}
	writeJSON(w, http.StatusOK, ModelList{Object: "list", Data: models})
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request, task Task) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, APIError{Type: "invalid_request_error", Message: "method not allowed"})
		return
	}
	if r.ContentLength > maxRequestBodyBytes {
		writeAPIError(w, http.StatusRequestEntityTooLarge, APIError{Type: "invalid_request_error", Code: "file_too_large", Message: "multipart request exceeds the 25 MB audio limit"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := r.ParseMultipartForm(maxRequestBodyBytes); err != nil {
		writeAPIError(w, http.StatusBadRequest, APIError{Type: "invalid_request_error", Code: "invalid_multipart", Message: "audio request must be a valid multipart form"})
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, APIError{Type: "invalid_request_error", Code: "file_required", Param: "file", Message: "audio file is required"})
		return
	}
	defer file.Close()

	requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if requestID == "" {
		requestID = NewRequestID()
	}
	defer func() {
		_ = s.config.Store.RemoveAudio(context.Background(), requestID)
	}()
	request := FileRequest{
		RequestID:              requestID,
		Task:                   task,
		Provider:               ProviderID(strings.TrimSpace(r.FormValue("provider"))),
		Model:                  strings.TrimSpace(r.FormValue("model")),
		Audio:                  AudioAsset{FileName: header.Filename, MIMEType: header.Header.Get("Content-Type"), Bytes: 1},
		Language:               strings.TrimSpace(r.FormValue("language")),
		Prompt:                 r.FormValue("prompt"),
		ResponseFormat:         ResponseFormat(strings.TrimSpace(r.FormValue("response_format"))),
		Stream:                 parseBool(r.FormValue("stream")),
		TimestampGranularities: parseGranularities(r.MultipartForm.Value),
		Include:                append([]string(nil), r.MultipartForm.Value["include[]"]...),
	}
	request.Include = append(request.Include, r.MultipartForm.Value["include"]...)
	if duration := strings.TrimSpace(r.FormValue("duration_ms")); duration != "" {
		value, parseErr := strconv.ParseInt(duration, 10, 64)
		if parseErr != nil || value < 0 {
			writeAPIError(w, http.StatusBadRequest, APIError{Type: "invalid_request_error", Code: "invalid_duration", Param: "duration_ms", Message: "duration_ms must be a non-negative integer"})
			return
		}
		request.Audio.DurationMS = value
	}
	if temperature := strings.TrimSpace(r.FormValue("temperature")); temperature != "" {
		value, parseErr := strconv.ParseFloat(temperature, 64)
		if parseErr != nil {
			writeAPIError(w, http.StatusBadRequest, APIError{Type: "invalid_request_error", Code: "invalid_temperature", Param: "temperature", Message: "temperature must be a number between 0 and 1"})
			return
		}
		request.Temperature = &value
	}
	request = request.Normalized()
	if err := request.Validate(); err != nil {
		writeValidationError(w, err)
		return
	}

	asset, err := s.config.Store.PersistAudio(r.Context(), requestID, header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrAudioTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeAPIError(w, status, APIError{Type: "invalid_request_error", Code: "audio_persist_failed", Param: "file", Message: safeStoreError(err)})
		return
	}
	asset.DurationMS = request.Audio.DurationMS
	request.Audio = asset
	if err := request.Validate(); err != nil {
		writeValidationError(w, err)
		return
	}

	provider, providerErr := s.config.Registry.Select(request.Provider, s.config.DefaultProvider)
	record := newRequestRecord(request, providerID(provider), PhasePersisted)
	if err := s.config.Store.SaveRecord(r.Context(), record); err != nil {
		writeAPIError(w, http.StatusInternalServerError, APIError{Type: "internal_error", Code: "record_persist_failed", Message: "transcription request could not be persisted"})
		return
	}
	if providerErr != nil {
		s.failRecord(r.Context(), &record, providerErr)
		writeProviderError(w, providerErr)
		return
	}
	if err := ensureProviderAuth(r.Context(), provider); err != nil {
		s.failRecord(r.Context(), &record, err)
		writeProviderError(w, err)
		return
	}
	record.Phase = PhaseDispatched
	_ = s.config.Store.SaveRecord(r.Context(), record)

	result, runErr, attempts := runFileProvider(r.Context(), provider, request)
	record.Attempts = attempts
	if runErr != nil {
		s.failRecord(r.Context(), &record, runErr)
		if request.Stream {
			s.writeSSEError(w, runErr)
			return
		}
		writeProviderError(w, runErr)
		return
	}
	result.Task = request.Task
	record.Phase = PhaseCompleted
	record.Text = result.Text
	record.UpdatedAt = time.Now().UTC()
	if err := s.config.Store.SaveRecord(r.Context(), record); err != nil {
		writeAPIError(w, http.StatusInternalServerError, APIError{Type: "internal_error", Code: "record_save_failed", Message: "transcription result could not be persisted"})
		return
	}
	_ = s.config.Store.SaveResult(r.Context(), record, result)
	if request.Stream {
		s.writeSSE(w, result)
		return
	}
	writeResult(w, request.ResponseFormat, result)
}

func newRequestRecord(request FileRequest, provider ProviderID, phase RecordPhase) RequestRecord {
	now := time.Now().UTC()
	return RequestRecord{
		SchemaVersion: ContractVersion,
		RequestID:     request.RequestID,
		Provider:      provider,
		Model:         request.Model,
		Task:          request.Task,
		Audio:         request.Audio,
		Phase:         phase,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func providerID(provider Provider) ProviderID {
	if provider == nil {
		return ""
	}
	return provider.ID()
}

func (s *Server) failRecord(ctx context.Context, record *RequestRecord, err error) {
	if record == nil {
		return
	}
	record.Phase = PhaseFailed
	record.UpdatedAt = time.Now().UTC()
	apiError := apiErrorFrom(err)
	record.Error = &apiError
	_ = s.config.Store.SaveRecord(ctx, *record)
}

func runFileProvider(ctx context.Context, provider Provider, request FileRequest) (Result, error, int) {
	if provider == nil {
		return Result{}, providerError(503, "provider_unavailable", "provider_not_configured", "transcription provider is not configured", false), 0
	}
	result, report, err := resilience.Run(
		ctx,
		resilience.DefaultPolicy(),
		resilience.Hooks[Result]{
			Attempt: func(attemptContext context.Context, _ int) (Result, error) {
				return provider.Transcribe(attemptContext, request)
			},
			Classify: func(err error) resilience.Decision {
				var providerErr *ProviderError
				if errors.As(err, &providerErr) && providerErr != nil {
					return resilience.Decision{Retry: providerErr.Retryable}
				}
				return resilience.Decision{}
			},
		},
	)
	return result, err, report.Attempts
}

func runRealtimeProvider(ctx context.Context, provider Provider, config RealtimeSessionConfig) (RealtimeSession, int, error) {
	if provider == nil {
		return nil, 0, providerError(503, "provider_unavailable", "provider_not_configured", "transcription provider is not configured", false)
	}
	session, report, err := resilience.Run(
		ctx,
		resilience.DefaultPolicy(),
		resilience.Hooks[RealtimeSession]{
			Attempt: func(attemptContext context.Context, _ int) (RealtimeSession, error) {
				return provider.NewRealtime(attemptContext, config)
			},
			Classify: func(err error) resilience.Decision {
				var providerErr *ProviderError
				if errors.As(err, &providerErr) && providerErr != nil {
					return resilience.Decision{Retry: providerErr.Retryable}
				}
				return resilience.Decision{}
			},
		},
	)
	return session, report.Attempts, err
}

type realtimeConnection struct {
	server      *Server
	provider    Provider
	session     RealtimeSession
	state       *SessionState
	request     FileRequest
	record      RequestRecord
	requestID   string
	itemID      string
	queryModel  string
	initialized bool
	audioBytes  int64
	audioChunks int64
	startedAt   time.Time
}

func (s *Server) trace(event TraceEvent) {
	if s == nil || s.config.Store == nil {
		return
	}
	_ = s.config.Store.AppendTrace(context.Background(), event)
}

func (c *realtimeConnection) trace(event string, phase RecordPhase, err error) {
	if c == nil || c.server == nil {
		return
	}
	value := TraceEvent{
		Event:       event,
		Transport:   "websocket",
		RequestID:   c.requestID,
		Provider:    providerID(c.provider),
		Model:       c.request.Model,
		Phase:       phase,
		Attempts:    c.record.Attempts,
		AudioBytes:  c.audioBytes,
		AudioChunks: c.audioChunks,
	}
	if !c.startedAt.IsZero() {
		value.DurationMS = time.Since(c.startedAt).Milliseconds()
	}
	if err != nil {
		apiError := apiErrorFrom(err)
		value.ErrorType = apiError.Type
		value.ErrorCode = apiError.Code
		value.ErrorMessage = apiError.Message
	}
	c.server.trace(value)
}

func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, APIError{Type: "invalid_request_error", Message: "method not allowed"})
		return
	}
	intent := r.URL.Query().Get("intent")
	if intent != "transcription" && strings.TrimSpace(r.URL.Query().Get("model")) == "" {
		writeAPIError(w, http.StatusBadRequest, APIError{Type: "invalid_request_error", Code: "intent_required", Param: "intent", Message: "realtime intent=transcription is required"})
		return
	}
	requestID := NewRequestID()
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.trace(TraceEvent{
			Event:        "realtime.accept_failed",
			Provider:     ProviderID(strings.TrimSpace(r.URL.Query().Get("provider"))),
			Model:        strings.TrimSpace(r.URL.Query().Get("model")),
			RequestID:    requestID,
			Transport:    "websocket",
			ErrorType:    "connection_error",
			ErrorMessage: err.Error(),
		})
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "done")
	state, stateErr := NewSessionState(requestID)
	if stateErr != nil {
		s.trace(TraceEvent{Event: "realtime.session_state_failed", RequestID: requestID, Transport: "websocket", ErrorType: "internal_error", ErrorMessage: stateErr.Error()})
		s.writeRealtimeError(r.Context(), connection, stateErr)
		return
	}
	client := &realtimeConnection{
		server:     s,
		state:      state,
		requestID:  state.ID,
		itemID:     "item-0",
		queryModel: strings.TrimSpace(r.URL.Query().Get("model")),
		startedAt:  time.Now(),
	}
	client.trace("realtime.accepted", PhaseReceived, nil)
	defer func() {
		if client.session != nil {
			_ = client.session.Close()
		}
		_ = s.config.Store.RemoveAudio(context.Background(), client.requestID)
		if client.audioBytes > 0 && client.record.Phase != PhaseCompleted && client.record.Phase != PhaseFailed {
			client.record.Phase = PhaseCancelled
			client.record.UpdatedAt = time.Now().UTC()
			_ = s.config.Store.SaveRecord(context.Background(), client.record)
			client.trace("realtime.cancelled", PhaseCancelled, nil)
		}
	}()
	for {
		messageType, payload, readErr := connection.Read(r.Context())
		if readErr != nil {
			return
		}
		if messageType != websocket.MessageText {
			client.sendError(r.Context(), connection, APIError{Type: "invalid_request_error", Code: "text_event_required", Message: "realtime client events must be JSON text"})
			continue
		}
		var event realtimeClientEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			client.sendError(r.Context(), connection, APIError{Type: "invalid_request_error", Code: "invalid_json", Message: "realtime client event must be valid JSON"})
			continue
		}
		switch event.Type {
		case "session.update":
			if err := client.initialize(r.Context(), event); err != nil {
				client.trace("realtime.session_failed", PhaseFailed, err)
				client.sendError(r.Context(), connection, apiErrorFrom(err))
				return
			}
			client.trace("realtime.session_ready", PhaseReceived, nil)
			if err := client.sendSession(r.Context(), connection, "session.created"); err != nil {
				return
			}
		case "input_audio_buffer.append":
			if err := client.append(r.Context(), connection, event.Audio); err != nil {
				client.trace("realtime.append_failed", PhaseFailed, err)
				client.sendError(r.Context(), connection, apiErrorFrom(err))
			}
		case "input_audio_buffer.commit":
			if err := client.commit(r.Context(), connection); err != nil {
				client.sendError(r.Context(), connection, apiErrorFrom(err))
				return
			}
		default:
			client.sendError(r.Context(), connection, APIError{Type: "invalid_request_error", Code: "event_not_supported", Message: "unsupported realtime client event"})
		}
	}
}

type realtimeClientEvent struct {
	Type     string          `json:"type"`
	Audio    string          `json:"audio,omitempty"`
	Provider ProviderID      `json:"provider,omitempty"`
	Session  json.RawMessage `json:"session,omitempty"`
}

type realtimeSessionWire struct {
	Type        string              `json:"type"`
	Model       string              `json:"model,omitempty"`
	Language    string              `json:"language,omitempty"`
	Prompt      string              `json:"prompt,omitempty"`
	Provider    ProviderID          `json:"provider,omitempty"`
	InputFormat RealtimeAudioFormat `json:"input_format,omitempty"`
	Audio       struct {
		Input struct {
			Format RealtimeAudioFormat `json:"format,omitempty"`
		} `json:"input,omitempty"`
	} `json:"audio,omitempty"`
}

func (c *realtimeConnection) initialize(ctx context.Context, event realtimeClientEvent) error {
	var wire realtimeSessionWire
	if len(event.Session) > 0 {
		if err := json.Unmarshal(event.Session, &wire); err != nil {
			return fmt.Errorf("decode realtime session: %w", err)
		}
	}
	if wire.Type == "" {
		wire.Type = "transcription"
	}
	format := wire.InputFormat
	if format.Type == "" {
		format = wire.Audio.Input.Format
	}
	if format.Type == "" {
		format = RealtimeAudioFormat{Type: "audio/pcm", Rate: 24_000}
	}
	config := RealtimeSessionConfig{
		Type:        wire.Type,
		Model:       strings.TrimSpace(wire.Model),
		Language:    strings.TrimSpace(wire.Language),
		Prompt:      wire.Prompt,
		InputFormat: format,
	}
	if config.Model == "" {
		config.Model = c.queryModel
	}
	if config.Model == "" {
		config.Model = DefaultModel
	}
	if err := config.Validate(); err != nil {
		return err
	}
	providerID := wire.Provider
	if providerID == "" {
		providerID = c.server.config.DefaultProvider
	}
	provider, err := c.server.config.Registry.Select(providerID, c.server.config.DefaultProvider)
	if err != nil {
		return err
	}
	if err := ensureProviderAuth(ctx, provider); err != nil {
		return err
	}
	session, attempts, err := runRealtimeProvider(ctx, provider, config)
	if err != nil {
		return err
	}
	c.provider = provider
	c.session = session
	c.request = FileRequest{
		RequestID: c.requestID,
		Task:      TaskTranscribe,
		Provider:  provider.ID(),
		Model:     config.Model,
		Audio: AudioAsset{
			FileName: "realtime.pcm",
			MIMEType: "audio/pcm",
		},
		Language: config.Language,
		Prompt:   config.Prompt,
	}
	c.record = newRequestRecord(c.request, provider.ID(), PhaseReceived)
	c.record.Attempts = attempts
	c.initialized = true
	return nil
}

func (c *realtimeConnection) sendSession(ctx context.Context, connection *websocket.Conn, eventType string) error {
	return connection.Write(ctx, websocket.MessageText, marshalRealtime(map[string]any{
		"type":     eventType,
		"event_id": NewRequestID(),
		"session": map[string]any{
			"id":    c.requestID,
			"type":  "transcription",
			"model": c.request.Model,
			"audio": map[string]any{"input": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}}},
		},
	}))
}

func (c *realtimeConnection) append(ctx context.Context, connection *websocket.Conn, encoded string) error {
	if !c.initialized || c.session == nil {
		return fmt.Errorf("realtime session.update is required before audio")
	}
	if strings.TrimSpace(encoded) == "" {
		return fmt.Errorf("audio is required")
	}
	audio, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode audio: %w", err)
	}
	if len(audio) == 0 || int64(len(audio)) > MaxChunkBytes || len(audio)%2 != 0 {
		return fmt.Errorf("audio chunk must be non-empty 16-bit PCM and at most %d bytes", MaxChunkBytes)
	}
	if c.audioBytes+int64(len(audio)) > MaxRealtimeAudioBytes {
		return fmt.Errorf("realtime audio exceeds the session limit")
	}
	asset, err := c.server.config.Store.AppendAudio(ctx, c.requestID, "realtime.pcm", "audio/pcm", audio)
	if err != nil {
		return err
	}
	c.audioBytes = asset.Bytes
	c.audioChunks++
	c.request.Audio = asset
	if c.record.Phase == PhaseReceived {
		c.record.Audio = asset
		c.record.Phase = PhasePersisted
		c.record.UpdatedAt = time.Now().UTC()
		if err := c.server.config.Store.SaveRecord(ctx, c.record); err != nil {
			return err
		}
	}
	events, err := c.session.Append(ctx, audio)
	if err != nil {
		return err
	}
	return c.sendEvents(ctx, connection, events)
}

func (c *realtimeConnection) commit(ctx context.Context, connection *websocket.Conn) error {
	if !c.initialized || c.session == nil {
		err := fmt.Errorf("realtime session.update is required before commit")
		c.trace("realtime.commit_failed", PhaseFailed, err)
		return err
	}
	if c.audioBytes == 0 {
		err := fmt.Errorf("at least one audio chunk is required before commit")
		c.trace("realtime.commit_failed", PhaseFailed, err)
		return err
	}
	c.trace("realtime.commit_started", PhaseCommitting, nil)
	c.record.Audio = c.request.Audio
	c.record.Phase = PhaseCommitting
	c.record.UpdatedAt = time.Now().UTC()
	_ = c.server.config.Store.SaveRecord(ctx, c.record)
	if err := connection.Write(ctx, websocket.MessageText, marshalRealtime(map[string]any{
		"type":     "input_audio_buffer.committed",
		"event_id": NewRequestID(),
		"item_id":  c.itemID,
	})); err != nil {
		c.trace("realtime.commit_write_failed", PhaseFailed, err)
		return err
	}
	events, err := c.session.Commit(ctx)
	if err != nil {
		c.record.Phase = PhaseFailed
		c.record.Error = apiErrorPtr(apiErrorFrom(err))
		c.record.UpdatedAt = time.Now().UTC()
		_ = c.server.config.Store.SaveRecord(context.Background(), c.record)
		c.trace("realtime.commit_failed", PhaseFailed, err)
		return err
	}
	if err := c.sendEvents(ctx, connection, events); err != nil {
		c.record.Phase = PhaseFailed
		c.record.Error = apiErrorPtr(apiErrorFrom(err))
		c.record.UpdatedAt = time.Now().UTC()
		_ = c.server.config.Store.SaveRecord(context.Background(), c.record)
		c.trace("realtime.send_failed", PhaseFailed, err)
		return err
	}
	if c.state.Phase == SessionFailed {
		c.record.Phase = PhaseFailed
		c.record.Error = apiErrorPtr(APIError{Type: "provider_error", Message: c.state.FailureReason})
		c.record.UpdatedAt = time.Now().UTC()
		_ = c.server.config.Store.SaveRecord(context.Background(), c.record)
		c.trace("realtime.provider_failed", PhaseFailed, errors.New(c.state.FailureReason))
		return fmt.Errorf("realtime provider failed: %s", c.state.FailureReason)
	}
	if !c.state.AllCompleted() {
		c.record.Phase = PhaseFailed
		c.record.Error = apiErrorPtr(APIError{Type: "provider_error", Code: "final_transcript_missing", Message: "realtime provider did not return a final transcription"})
		c.record.UpdatedAt = time.Now().UTC()
		_ = c.server.config.Store.SaveRecord(context.Background(), c.record)
		c.trace("realtime.final_missing", PhaseFailed, errors.New("realtime provider did not return a final transcription"))
		return errors.New("realtime provider did not return a final transcription")
	}
	if c.state.AllCompleted() {
		c.record.Phase = PhaseCompleted
		c.record.Text = c.state.Text(c.itemID)
		c.record.UpdatedAt = time.Now().UTC()
		if err := c.server.config.Store.SaveRecord(ctx, c.record); err != nil {
			return err
		}
		_ = c.server.config.Store.SaveResult(ctx, c.record, Result{Task: TaskTranscribe, Text: c.record.Text})
		c.trace("realtime.completed", PhaseCompleted, nil)
	}
	return nil
}

func ensureProviderAuth(ctx context.Context, provider Provider) error {
	refresher, ok := provider.(AuthRefresher)
	if !ok {
		return nil
	}
	return refresher.EnsureAuthFresh(ctx)
}

func apiErrorPtr(value APIError) *APIError {
	return &value
}

func (c *realtimeConnection) sendEvents(ctx context.Context, connection *websocket.Conn, events []ProviderEvent) error {
	for _, event := range events {
		// Provider sessions are implementation details. The reducer and the
		// public WebSocket session share the server-owned request identity.
		event.SessionID = c.state.ID
		if event.ItemID == "" {
			event.ItemID = c.itemID
		}
		before := c.state.Text(event.ItemID)
		if _, err := c.state.Apply(event); err != nil {
			return err
		}
		after := c.state.Text(event.ItemID)
		switch event.Kind {
		case EventHypothesis:
			delta := event.Text
			replaces := false
			if event.Replace {
				if strings.HasPrefix(after, before) {
					delta = strings.TrimPrefix(after, before)
				} else {
					// A cumulative provider hypothesis may revise an earlier
					// word. An append-only delta cannot represent that revision;
					// carry the authoritative text plus a tiny compatibility
					// marker so clients replace their preview instead of
					// duplicating the old hypothesis.
					delta = after
					replaces = true
				}
			}
			if delta == "" && !replaces {
				continue
			}
			payload := map[string]any{
				"type":          "conversation.item.input_audio_transcription.delta",
				"event_id":      NewRequestID(),
				"item_id":       event.ItemID,
				"content_index": 0,
				"delta":         delta,
			}
			if replaces {
				payload["replace"] = true
			}
			if err := connection.Write(ctx, websocket.MessageText, marshalRealtime(payload)); err != nil {
				return err
			}
		case EventFinal:
			if err := connection.Write(ctx, websocket.MessageText, marshalRealtime(map[string]any{
				"type":          "conversation.item.input_audio_transcription.completed",
				"event_id":      NewRequestID(),
				"item_id":       event.ItemID,
				"content_index": 0,
				"transcript":    after,
			})); err != nil {
				return err
			}
		case EventFailure:
			if err := connection.Write(ctx, websocket.MessageText, marshalRealtime(map[string]any{
				"type":     "conversation.item.input_audio_transcription.failed",
				"event_id": NewRequestID(),
				"item_id":  event.ItemID,
				"error":    event.Error,
			})); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *realtimeConnection) sendError(ctx context.Context, connection *websocket.Conn, apiError APIError) {
	_ = c.writeRealtimeError(ctx, connection, apiError)
}

func (c *realtimeConnection) writeRealtimeError(ctx context.Context, connection *websocket.Conn, value any) error {
	var apiError APIError
	switch typed := value.(type) {
	case APIError:
		apiError = typed
	case error:
		apiError = apiErrorFrom(typed)
	default:
		apiError = APIError{Type: "internal_error", Message: "realtime request failed"}
	}
	return connection.Write(ctx, websocket.MessageText, marshalRealtime(map[string]any{
		"type":     "error",
		"event_id": NewRequestID(),
		"error":    apiError,
	}))
}

func (s *Server) writeRealtimeError(ctx context.Context, connection *websocket.Conn, value any) error {
	return (&realtimeConnection{}).writeRealtimeError(ctx, connection, value)
}

func marshalRealtime(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func writeResult(w http.ResponseWriter, format ResponseFormat, result Result) {
	switch format {
	case ResponseText:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, result.Text)
	case ResponseSRT:
		w.Header().Set("Content-Type", "application/x-subrip; charset=utf-8")
		if result.rawText != "" {
			_, _ = io.WriteString(w, result.rawText)
		} else {
			_, _ = io.WriteString(w, formatSRT(result))
		}
	case ResponseVTT:
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		if result.rawText != "" {
			_, _ = io.WriteString(w, result.rawText)
		} else {
			_, _ = io.WriteString(w, formatVTT(result))
		}
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) writeSSE(w http.ResponseWriter, result Result) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	text := result.Text
	for start := 0; start < len(text); {
		end := start + 4096
		if end > len(text) {
			end = len(text)
		}
		event := map[string]any{"type": "transcript.text.delta", "delta": text[start:end]}
		writeSSEEvent(w, "transcript.text.delta", event)
		if flusher != nil {
			flusher.Flush()
		}
		start = end
	}
	writeSSEEvent(w, "transcript.text.done", map[string]any{"type": "transcript.text.done", "text": text})
	if flusher != nil {
		flusher.Flush()
	}
}

func (s *Server) writeSSEError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writeSSEEvent(w, "error", map[string]any{"type": "error", "error": apiErrorFrom(err)})
}

func writeSSEEvent(w io.Writer, name string, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
}

func formatSRT(result Result) string {
	if len(result.Segments) == 0 {
		return "1\n00:00:00,000 --> 00:00:00,000\n" + result.Text + "\n"
	}
	var builder strings.Builder
	for index, segment := range result.Segments {
		fmt.Fprintf(&builder, "%d\n%s --> %s\n%s\n\n", index+1, formatTimestamp(segment.Start, ','), formatTimestamp(segment.End, ','), strings.TrimSpace(segment.Text))
	}
	return builder.String()
}

func formatVTT(result Result) string {
	if len(result.Segments) == 0 {
		return "WEBVTT\n\n" + result.Text + "\n"
	}
	var builder strings.Builder
	builder.WriteString("WEBVTT\n\n")
	for _, segment := range result.Segments {
		fmt.Fprintf(&builder, "%s --> %s\n%s\n\n", formatTimestamp(segment.Start, '.'), formatTimestamp(segment.End, '.'), strings.TrimSpace(segment.Text))
	}
	return builder.String()
}

func formatTimestamp(seconds float64, separator byte) string {
	if seconds < 0 {
		seconds = 0
	}
	totalMilliseconds := int64(seconds*1000 + 0.5)
	hours := totalMilliseconds / 3_600_000
	minutes := (totalMilliseconds / 60_000) % 60
	remaining := totalMilliseconds % 60_000
	wholeSeconds := remaining / 1000
	milliseconds := remaining % 1000
	return fmt.Sprintf("%02d:%02d:%02d%c%03d", hours, minutes, wholeSeconds, separator, milliseconds)
}

func parseGranularities(values map[string][]string) []TimestampGranularity {
	result := make([]TimestampGranularity, 0)
	for _, key := range []string{"timestamp_granularities[]", "timestamp_granularities"} {
		for _, value := range values[key] {
			if strings.TrimSpace(value) != "" {
				result = append(result, TimestampGranularity(strings.TrimSpace(value)))
			}
		}
	}
	return result
}

func parseBool(value string) bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	return err == nil && parsed
}

func writeValidationError(w http.ResponseWriter, err error) {
	var validationError *ValidationError
	if errors.As(err, &validationError) && validationError != nil {
		writeAPIError(w, http.StatusBadRequest, APIError{Type: "invalid_request_error", Code: validationError.Code, Param: validationError.Field, Message: validationError.Message})
		return
	}
	writeAPIError(w, http.StatusBadRequest, APIError{Type: "invalid_request_error", Message: "invalid transcription request"})
}

func writeProviderError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr != nil {
		if providerErr.Status >= 400 && providerErr.Status < 600 {
			status = providerErr.Status
		} else if errors.Is(providerErr, ErrProviderUnavailable) {
			status = http.StatusServiceUnavailable
		}
	}
	writeAPIError(w, status, apiErrorFrom(err))
}

func apiErrorFrom(err error) APIError {
	if err == nil {
		return APIError{Type: "internal_error", Message: "unknown transcription error"}
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr != nil {
		apiError := providerErr.APIError
		if apiError.Type == "" {
			apiError.Type = "provider_error"
		}
		if apiError.Message == "" {
			apiError.Message = providerErr.Error()
		}
		return apiError
	}
	var validationError *ValidationError
	if errors.As(err, &validationError) && validationError != nil {
		return APIError{Type: "invalid_request_error", Code: validationError.Code, Param: validationError.Field, Message: validationError.Message}
	}
	return APIError{Type: "internal_error", Message: err.Error()}
}

func safeStoreError(err error) string {
	if errors.Is(err, ErrAudioTooLarge) {
		return "audio file exceeds the 25 MB compatibility limit"
	}
	return "audio file could not be persisted"
}

func writeAPIError(w http.ResponseWriter, status int, apiError APIError) {
	writeJSON(w, status, ErrorEnvelope{Error: apiError})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
