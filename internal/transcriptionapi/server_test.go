package transcriptionapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

type fakeProvider struct {
	id             ProviderID
	result         Result
	err            error
	ensureErr      error
	realtime       *fakeRealtime
	lastRequest    FileRequest
	requestMu      sync.Mutex
	transcribeCall int
	ensureCalls    int
}

func (p *fakeProvider) ID() ProviderID { return p.id }

func (p *fakeProvider) Capabilities(context.Context) ProviderCapability {
	return ProviderCapability{
		Provider:    p.id,
		Models:      []string{DefaultModel},
		File:        true,
		Translation: true,
		Streaming:   true,
		Realtime:    p.realtime != nil,
		Ready:       true,
	}
}

func (p *fakeProvider) Transcribe(_ context.Context, request FileRequest) (Result, error) {
	p.requestMu.Lock()
	defer p.requestMu.Unlock()
	p.transcribeCall++
	p.lastRequest = request
	if p.err != nil {
		return Result{}, p.err
	}
	result := p.result
	result.Task = request.Task
	return result, nil
}

func (p *fakeProvider) EnsureAuthFresh(context.Context) error {
	p.requestMu.Lock()
	defer p.requestMu.Unlock()
	p.ensureCalls++
	return p.ensureErr
}

func (p *fakeProvider) NewRealtime(context.Context, RealtimeSessionConfig) (RealtimeSession, error) {
	if p.realtime == nil {
		return nil, providerError(501, "unsupported", "realtime_unsupported", "fake realtime is disabled", false)
	}
	return p.realtime, nil
}

type fakeRealtime struct {
	appendEvents []ProviderEvent
	commitEvents []ProviderEvent
	closed       bool
}

func (s *fakeRealtime) Append(context.Context, []byte) ([]ProviderEvent, error) {
	return append([]ProviderEvent(nil), s.appendEvents...), nil
}

func (s *fakeRealtime) Commit(context.Context) ([]ProviderEvent, error) {
	return append([]ProviderEvent(nil), s.commitEvents...), nil
}

func (s *fakeRealtime) Close() error {
	s.closed = true
	return nil
}

func newTestServer(t *testing.T, provider Provider, token string) (*Server, *Store) {
	t.Helper()
	store, err := NewStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Registry:        NewRegistry(provider),
		Store:           store,
		DefaultProvider: provider.ID(),
		BearerToken:     token,
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, store
}

func TestServerTranscriptionsPersistBeforeProviderAndReturnOpenAIShape(t *testing.T) {
	provider := &fakeProvider{id: ProviderLocal, result: Result{Text: "hello from the provider"}}
	server, store := newTestServer(t, provider, "secret")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "req-file-1", "speech.mp3", []byte("fake-mp3"), map[string]string{
		"model": "whisper-1",
	})
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d body = %s", response.StatusCode, body)
	}
	var result Result
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello from the provider" || result.Task != TaskTranscribe {
		t.Fatalf("result = %+v", result)
	}
	provider.requestMu.Lock()
	lastRequest := provider.lastRequest
	provider.requestMu.Unlock()
	if lastRequest.Audio.PersistedPath == "" {
		t.Fatal("provider did not receive a persisted path")
	}
	provider.requestMu.Lock()
	ensureCalls := provider.ensureCalls
	provider.requestMu.Unlock()
	if ensureCalls != 1 {
		t.Fatalf("ensure auth calls = %d, want 1 before dispatch", ensureCalls)
	}
	if _, err := os.Stat(lastRequest.Audio.PersistedPath); err != nil {
		t.Fatalf("persisted audio is missing: %v", err)
	}
	record, err := store.LoadRecord(context.Background(), "req-file-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != PhaseCompleted || record.Text != result.Text || record.Attempts != 1 {
		t.Fatalf("record = %+v", record)
	}
}

func TestServerAuthAndCompletedFileSSE(t *testing.T) {
	provider := &fakeProvider{id: ProviderLocal, result: Result{Text: strings.Repeat("x", 5000)}}
	server, _ := newTestServer(t, provider, "secret")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	unauthorized, err := http.Get(httpServer.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.StatusCode)
	}

	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "req-sse-1", "speech.webm", []byte("fake-webm"), map[string]string{
		"model":  "whisper-1",
		"stream": "true",
	})
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "transcript.text.delta") || !strings.Contains(string(body), "transcript.text.done") {
		t.Fatalf("SSE status=%d body prefix=%q", response.StatusCode, string(body[:minInt(len(body), 200)]))
	}
}

func TestServerRetainsAudioWhenProviderFails(t *testing.T) {
	provider := &fakeProvider{
		id:  ProviderLocal,
		err: providerError(422, "provider", "bad_audio", "provider rejected fixture", false),
	}
	server, store := newTestServer(t, provider, "secret")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "req-failed-1", "speech.wav", []byte("fake-wav"), map[string]string{
		"model": "whisper-1",
	})
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.StatusCode)
	}
	record, err := store.LoadRecord(context.Background(), "req-failed-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != PhaseFailed || record.Error == nil || record.Audio.PersistedPath == "" {
		t.Fatalf("failed record = %+v", record)
	}
	if _, err := os.Stat(record.Audio.PersistedPath); err != nil {
		t.Fatalf("failed audio was not retained: %v", err)
	}
}

func TestServerRetainsAudioWhenProactiveAuthRefreshFails(t *testing.T) {
	provider := &fakeProvider{
		id:        ProviderLocal,
		ensureErr: providerError(401, "authentication_error", "auth_refresh_failed", "auth fixture failed", false),
	}
	server, store := newTestServer(t, provider, "secret")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "req-auth-failed-1", "speech.webm", []byte("fake-webm"), map[string]string{
		"model": DefaultModel,
	})
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
	record, err := store.LoadRecord(context.Background(), "req-auth-failed-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != PhaseFailed || record.Audio.PersistedPath == "" {
		t.Fatalf("auth-failed record = %+v", record)
	}
}

func TestServerRealtimeUsesOpenAIShapedEventsAndPersistsPCM(t *testing.T) {
	provider := &fakeProvider{
		id: ProviderLocal,
		realtime: &fakeRealtime{
			appendEvents: []ProviderEvent{{Kind: EventHypothesis, Text: "hello", Replace: true, Sequence: 1}},
			commitEvents: []ProviderEvent{{Kind: EventFinal, Text: "hello world", Sequence: 2}},
		},
	}
	server, store := newTestServer(t, provider, "secret")
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/realtime?intent=transcription"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test done")

	writeRealtimeTestEvent(t, ctx, connection, map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type":  "transcription",
			"model": "whisper-1",
			"audio": map[string]any{"input": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}}},
		},
	})
	readRealtimeTestEvent(t, ctx, connection, "session.created")
	writeRealtimeTestEvent(t, ctx, connection, map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": EncodeRealtimeAudio([]byte("pcmx")),
	})
	readRealtimeTestEvent(t, ctx, connection, "conversation.item.input_audio_transcription.delta")
	writeRealtimeTestEvent(t, ctx, connection, map[string]any{"type": "input_audio_buffer.commit"})
	readRealtimeTestEvent(t, ctx, connection, "input_audio_buffer.committed")
	readRealtimeTestEvent(t, ctx, connection, "conversation.item.input_audio_transcription.completed")

	entries, err := os.ReadDir(store.Root() + "/requests")
	if err != nil || len(entries) != 1 {
		t.Fatalf("realtime request entries = %d err=%v", len(entries), err)
	}
}

func TestStorePrunesOldAudioButKeepsRecord(t *testing.T) {
	store, err := NewStore(t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.PersistAudio(context.Background(), "first", "a.mp3", "audio/mpeg", strings.NewReader("1234"))
	if err != nil {
		t.Fatal(err)
	}
	// Ensure the second file is newer on filesystems with coarse timestamps.
	time.Sleep(2 * time.Millisecond)
	second, err := store.PersistAudio(context.Background(), "second", "b.mp3", "audio/mpeg", strings.NewReader("5678"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.PersistedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old audio err = %v, want pruned", err)
	}
	if _, err := os.Stat(second.PersistedPath); err != nil {
		t.Fatalf("current audio was pruned: %v", err)
	}
}

func TestOpenAIHTTPProviderRoundTripsRESTAndRealtime(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		responseFormat := r.FormValue("response_format")
		if responseFormat == "srt" {
			_, _ = io.WriteString(w, "1\n00:00:00,000 --> 00:00:01,000\nupstream srt\n")
			return
		}
		if responseFormat == "vtt" {
			_, _ = io.WriteString(w, "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nupstream vtt\n")
			return
		}
		_, _ = io.WriteString(w, `{"text":"upstream transcript"}`)
	})
	mux.HandleFunc("/v1/realtime", func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close(websocket.StatusNormalClosure, "done")
		ctx := r.Context()
		for {
			_, payload, readErr := connection.Read(ctx)
			if readErr != nil {
				return
			}
			var event map[string]any
			if json.Unmarshal(payload, &event) != nil {
				return
			}
			switch event["type"] {
			case "session.update":
				_ = connection.Write(ctx, websocket.MessageText, []byte(`{"type":"session.updated"}`))
			case "input_audio_buffer.append":
				_ = connection.Write(ctx, websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.delta","item_id":"item-0","delta":"upstream "}`))
			case "input_audio_buffer.commit":
				_ = connection.Write(ctx, websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item-0","transcript":"upstream final"}`))
				return
			}
		}
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()
	provider, err := NewOpenAIHTTPProvider(ProviderLocal, upstream.URL+"/v1", "upstream-token")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	audio, err := store.PersistAudio(context.Background(), "http-provider", "speech.mp3", "audio/mpeg", strings.NewReader("audio"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Transcribe(context.Background(), FileRequest{
		RequestID: "http-provider",
		Task:      TaskTranscribe,
		Model:     DefaultModel,
		Audio:     audio,
	})
	if err != nil || result.Text != "upstream transcript" {
		t.Fatalf("REST result=%+v err=%v", result, err)
	}
	for _, testCase := range []struct {
		format ResponseFormat
		text   string
	}{
		{format: ResponseSRT, text: "1\n00:00:00,000 --> 00:00:01,000\nupstream srt\n"},
		{format: ResponseVTT, text: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nupstream vtt\n"},
	} {
		formatted, formatErr := provider.Transcribe(context.Background(), FileRequest{
			RequestID:      "http-provider-" + string(testCase.format),
			Task:           TaskTranscribe,
			Model:          DefaultModel,
			ResponseFormat: testCase.format,
			Audio:          audio,
		})
		if formatErr != nil || formatted.Text != testCase.text || formatted.rawText != testCase.text {
			t.Fatalf("%s result=%+v err=%v", testCase.format, formatted, formatErr)
		}
	}
	session, err := provider.NewRealtime(context.Background(), RealtimeSessionConfig{Type: "transcription", Model: DefaultModel, InputFormat: RealtimeAudioFormat{Type: "audio/pcm", Rate: 24000}})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	appendEvents, err := session.Append(context.Background(), []byte("pcm"))
	if err != nil || len(appendEvents) != 1 || appendEvents[0].Kind != EventHypothesis {
		t.Fatalf("append events=%+v err=%v", appendEvents, err)
	}
	commitEvents, err := session.Commit(context.Background())
	if err != nil || len(commitEvents) != 1 || commitEvents[0].Kind != EventFinal {
		t.Fatalf("commit events=%+v err=%v", commitEvents, err)
	}
}

func TestWriteResultPreservesProviderSubtitleFormats(t *testing.T) {
	for _, testCase := range []struct {
		format ResponseFormat
		want   string
	}{
		{format: ResponseSRT, want: "1\n00:00:00,000 --> 00:00:01,000\nprovider srt\n"},
		{format: ResponseVTT, want: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nprovider vtt\n"},
	} {
		t.Run(string(testCase.format), func(t *testing.T) {
			response := httptest.NewRecorder()
			writeResult(response, testCase.format, Result{Text: "normalized", rawText: testCase.want})
			if got := response.Body.String(); got != testCase.want {
				t.Fatalf("body = %q, want %q", got, testCase.want)
			}
		})
	}
}

func newMultipartRequest(t *testing.T, endpoint, requestID, fileName string, audio []byte, fields map[string]string) *http.Request {
	t.Helper()
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{"form-data; name=\"file\"; filename=\"" + fileName + "\""},
		"Content-Type":        []string{"audio/webm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(audio); err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body.String()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-Request-ID", requestID)
	return request
}

func writeRealtimeTestEvent(t *testing.T, ctx context.Context, connection *websocket.Conn, event map[string]any) {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func readRealtimeTestEvent(t *testing.T, ctx context.Context, connection *websocket.Conn, expectedType string) {
	t.Helper()
	_, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != expectedType {
		t.Fatalf("event type = %q, want %q; payload=%s", event.Type, expectedType, payload)
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
