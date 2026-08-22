package transcriptionapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
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
	ensureSequence []error
	ensureWait     time.Duration
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

func (p *fakeProvider) EnsureAuthFresh(ctx context.Context) error {
	if p.ensureWait > 0 {
		timer := time.NewTimer(p.ensureWait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.requestMu.Lock()
	defer p.requestMu.Unlock()
	p.ensureCalls++
	if len(p.ensureSequence) > 0 {
		err := p.ensureSequence[0]
		p.ensureSequence = p.ensureSequence[1:]
		return err
	}
	return p.ensureErr
}

func TestEnsureProviderAuthRetriesWithinShortRequestBudget(t *testing.T) {
	provider := &fakeProvider{
		id:             ProviderChatGPT,
		ensureSequence: []error{errors.New("headed browser is still repairing"), errors.New("auth evidence is not ready")},
	}
	if err := ensureProviderAuth(context.Background(), provider, 2*time.Second); err != nil {
		t.Fatalf("ensureProviderAuth() error = %v", err)
	}
	provider.requestMu.Lock()
	calls := provider.ensureCalls
	provider.requestMu.Unlock()
	if calls != 3 {
		t.Fatalf("ensure auth calls = %d, want three bounded attempts", calls)
	}
}

func TestServerBoundsRequestTimeAuthRepair(t *testing.T) {
	provider := &fakeProvider{id: ProviderChatGPT, ensureWait: time.Second}
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Registry:        NewRegistry(provider),
		Store:           store,
		DefaultProvider: ProviderChatGPT,
		AuthTimeout:     25 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	started := time.Now()
	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "req-auth-timeout-1", "speech.webm", []byte("fake-webm"), map[string]string{"model": DefaultModel})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d body = %s, want 504", response.StatusCode, body)
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "auth_refresh_timeout" {
		t.Fatalf("error = %+v, want auth_refresh_timeout", envelope.Error)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("request took %v, want bounded auth timeout", elapsed)
	}
	record, err := store.LoadRecord(context.Background(), "req-auth-timeout-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != PhaseFailed || record.Error == nil || record.Error.Code != "auth_refresh_timeout" {
		t.Fatalf("record = %+v, want failed auth timeout", record)
	}
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

func newTestServer(t *testing.T, provider Provider) (*Server, *Store) {
	t.Helper()
	store, err := NewStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Registry:        NewRegistry(provider),
		Store:           store,
		DefaultProvider: provider.ID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, store
}

func TestDemoPageIsServedWithoutAuthentication(t *testing.T) {
	provider := &fakeProvider{id: ProviderLocal, result: Result{Text: "demo"}}
	server, _ := newTestServer(t, provider)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	for _, path := range []string{"/", "/demo.html"} {
		request, err := http.NewRequest(http.MethodGet, httpServer.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, response.StatusCode, http.StatusOK)
		}
		if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Fatalf("GET %s content type = %q, want text/html", path, got)
		}
		if !strings.Contains(string(body), "cdp transcription API") || !strings.Contains(string(body), "getUserMedia") || !strings.Contains(string(body), "duration_ms") || !strings.Contains(string(body), "secureLink") {
			t.Fatalf("GET %s did not return the microphone demo", path)
		}
	}
}

func TestServerRequiresCompleteTLSConfiguration(t *testing.T) {
	store, err := NewStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(ServerConfig{Store: store, TLSCertFile: "cert.pem"}); err == nil {
		t.Fatal("NewServer accepted a TLS certificate without a key")
	}
}

func TestServerServesPrimaryAndOptionalHTTPListeners(t *testing.T) {
	provider := &fakeProvider{id: ProviderLocal, result: Result{Text: "ready"}}
	store, err := NewStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	primaryAddress := reserveTCPAddress(t)
	httpAddress := reserveTCPAddress(t)
	server, err := NewServer(ServerConfig{
		Registry:        NewRegistry(provider),
		Store:           store,
		DefaultProvider: ProviderLocal,
		Address:         primaryAddress,
		HTTPAddress:     httpAddress,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ListenAndServe(ctx) }()

	client := &http.Client{Timeout: 2 * time.Second}
	for _, address := range []string{primaryAddress, httpAddress} {
		var response *http.Response
		for attempt := 0; attempt < 20; attempt++ {
			response, err = client.Get("http://" + address + "/healthz")
			if err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("GET /healthz on %s: %v", address, err)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET /healthz on %s status = %d body = %s", address, response.StatusCode, body)
		}
		var health struct {
			Transport string `json:"transport"`
			Listeners []struct {
				Address string `json:"address"`
			} `json:"listeners"`
		}
		if err := json.Unmarshal(body, &health); err != nil {
			t.Fatal(err)
		}
		if health.Transport != "http" || len(health.Listeners) != 2 {
			t.Fatalf("health on %s = %+v, want HTTP and two listeners", address, health)
		}
	}
	cancel()
	if err := <-serveDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("ListenAndServe error = %v, want context.Canceled", err)
	}
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort("localhost", "0"))
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func TestServerTranscriptionsPersistBeforeProviderAndReturnOpenAIShape(t *testing.T) {
	provider := &fakeProvider{id: ProviderLocal, result: Result{Text: "hello from the provider"}}
	server, store := newTestServer(t, provider)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "req-file-1", "speech.mp3", []byte("fake-mp3"), map[string]string{
		"model":       "whisper-1",
		"duration_ms": "1250",
	})
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
	if lastRequest.Audio.DurationMS != 1250 {
		t.Fatalf("provider duration_ms = %d, want 1250", lastRequest.Audio.DurationMS)
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

func TestServerEphemeralMediaIsRemovedAfterFileTransaction(t *testing.T) {
	provider := &fakeProvider{id: ProviderLocal, result: Result{Text: "ephemeral result"}}
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Registry: NewRegistry(provider), Store: store, DefaultProvider: ProviderLocal})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "ephemeral-http-1", "speech.webm", []byte("fake-webm"), map[string]string{"model": DefaultModel})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	provider.requestMu.Lock()
	path := provider.lastRequest.Audio.PersistedPath
	provider.requestMu.Unlock()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transaction audio err = %v, want not exist after response", err)
	}
	if _, err := store.LoadRecord(context.Background(), "ephemeral-http-1"); err != nil {
		t.Fatalf("result record was not retained: %v", err)
	}
}

func TestServerUnauthenticatedAndCompletedFileSSE(t *testing.T) {
	provider := &fakeProvider{id: ProviderLocal, result: Result{Text: strings.Repeat("x", 5000)}}
	server, _ := newTestServer(t, provider)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	models, err := http.Get(httpServer.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	models.Body.Close()
	if models.StatusCode != http.StatusOK {
		t.Fatalf("models status = %d, want %d", models.StatusCode, http.StatusOK)
	}

	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "req-sse-1", "speech.webm", []byte("fake-webm"), map[string]string{
		"model":  "whisper-1",
		"stream": "true",
	})
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
	server, store := newTestServer(t, provider)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "req-failed-1", "speech.wav", []byte("fake-wav"), map[string]string{
		"model": "whisper-1",
	})
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

func TestServerMarksObservedFileFailureUntilSyntheticRecovery(t *testing.T) {
	provider := &fakeProvider{
		id:  ProviderChatGPT,
		err: providerError(http.StatusBadGateway, "provider", "provider_response_changed", "provider returned no transcript", false),
	}
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	health := NewProbeHealth(15 * time.Minute)
	health.RecordSuccess(ProviderChatGPT, time.Now().UTC(), "fixture-001")
	server, err := NewServer(ServerConfig{
		Registry:        NewRegistry(provider),
		Store:           store,
		DefaultProvider: ProviderChatGPT,
		ProbeHealth:     health,
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "req-health-failed-1", "speech.webm", []byte("fake-webm"), map[string]string{
		"model": DefaultModel,
	})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.StatusCode)
	}
	capability := health.Apply(provider.Capabilities(context.Background()), time.Now().UTC())
	if capability.Ready || capability.ProbeReady || capability.FileProbe == nil || capability.FileProbe.Ready {
		t.Fatalf("observed file failure left health ready: %+v", capability)
	}
	if capability.FileProbe.Reason != "provider_response_changed" {
		t.Fatalf("observed failure reason = %q", capability.FileProbe.Reason)
	}

	health.RecordSuccess(ProviderChatGPT, time.Now().UTC(), "fixture-002")
	recovered := health.Apply(provider.Capabilities(context.Background()), time.Now().UTC())
	if !recovered.Ready || !recovered.ProbeReady || recovered.FileProbe == nil || !recovered.FileProbe.Ready {
		t.Fatalf("synthetic recovery did not restore health: %+v", recovered)
	}
}

func TestServerRetainsAudioWhenProactiveAuthRefreshFails(t *testing.T) {
	provider := &fakeProvider{
		id:        ProviderLocal,
		ensureErr: providerError(401, "authentication_error", "auth_refresh_failed", "auth fixture failed", false),
	}
	server, store := newTestServer(t, provider)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request := newMultipartRequest(t, httpServer.URL+"/v1/audio/transcriptions", "req-auth-failed-1", "speech.webm", []byte("fake-webm"), map[string]string{
		"model": DefaultModel,
	})
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
	server, store := newTestServer(t, provider)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/realtime?intent=transcription"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, wsURL, nil)
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
	record, err := store.LoadRecord(context.Background(), entries[0].Name())
	if err != nil {
		t.Fatal(err)
	}
	if record.Audio.Bytes != 4 {
		t.Fatalf("realtime record audio bytes = %d, want final chunk count 4", record.Audio.Bytes)
	}
	var trace []byte
	for attempt := 0; attempt < 20; attempt++ {
		trace, err = os.ReadFile(store.TracePath())
		if err == nil && strings.Contains(string(trace), `"event":"realtime.completed"`) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(trace), `"event":"realtime.completed"`) || !strings.Contains(string(trace), `"audio_chunks":1`) {
		t.Fatalf("realtime trace = %s", trace)
	}
	if strings.Contains(string(trace), "hello world") {
		t.Fatal("realtime trace leaked transcript text")
	}
}

func TestServerRealtimeAcceptsPCMChunkAboveDefaultWebSocketLimit(t *testing.T) {
	provider := &fakeProvider{
		id: ProviderLocal,
		realtime: &fakeRealtime{
			appendEvents: []ProviderEvent{{Kind: EventHypothesis, Text: "chunk received", Sequence: 1}},
			commitEvents: []ProviderEvent{{Kind: EventFinal, Text: "chunk received", Sequence: 2}},
		},
	}
	server, _ := newTestServer(t, provider)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/realtime?intent=transcription"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, wsURL, nil)
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
		"audio": EncodeRealtimeAudio(bytes.Repeat([]byte{0x01}, 48*1024)),
	})
	readRealtimeTestEvent(t, ctx, connection, "conversation.item.input_audio_transcription.delta")
	writeRealtimeTestEvent(t, ctx, connection, map[string]any{"type": "input_audio_buffer.commit"})
	readRealtimeTestEvent(t, ctx, connection, "input_audio_buffer.committed")
	readRealtimeTestEvent(t, ctx, connection, "conversation.item.input_audio_transcription.completed")
}

func TestServerRealtimeMarksCumulativeHypothesisRevisionsAsReplacement(t *testing.T) {
	provider := &fakeProvider{
		id: ProviderLocal,
		realtime: &fakeRealtime{
			appendEvents: []ProviderEvent{
				{Kind: EventHypothesis, Text: "hello world", Replace: true, Sequence: 1},
				{Kind: EventHypothesis, Text: "hello word", Replace: true, Sequence: 2},
			},
			commitEvents: []ProviderEvent{{Kind: EventFinal, Text: "hello word", Sequence: 3}},
		},
	}
	server, _ := newTestServer(t, provider)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/realtime?intent=transcription"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, wsURL, nil)
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

	first := readRealtimeTestEvent(t, ctx, connection, "conversation.item.input_audio_transcription.delta")
	second := readRealtimeTestEvent(t, ctx, connection, "conversation.item.input_audio_transcription.delta")
	if first["delta"] != "hello world" || first["replace"] != nil {
		t.Fatalf("first hypothesis = %#v, want append-only hello world", first)
	}
	if second["delta"] != "hello word" || second["replace"] != true {
		t.Fatalf("revised hypothesis = %#v, want replacement hello word", second)
	}

	writeRealtimeTestEvent(t, ctx, connection, map[string]any{"type": "input_audio_buffer.commit"})
	readRealtimeTestEvent(t, ctx, connection, "input_audio_buffer.committed")
	readRealtimeTestEvent(t, ctx, connection, "conversation.item.input_audio_transcription.completed")
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

func TestEphemeralStoreRemovesMediaWithoutRemovingRecords(t *testing.T) {
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := store.PersistAudio(context.Background(), "ephemeral-1", "speech.webm", "audio/webm", strings.NewReader("audio"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(asset.PersistedPath); err != nil {
		t.Fatalf("ephemeral media was not available during the transaction: %v", err)
	}
	if !asset.Ephemeral {
		t.Fatal("ephemeral store returned a non-ephemeral asset")
	}
	if err := store.RemoveAudio(context.Background(), "ephemeral-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(asset.PersistedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ephemeral media err = %v, want not exist", err)
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

func readRealtimeTestEvent(t *testing.T, ctx context.Context, connection *websocket.Conn, expectedType string) map[string]any {
	t.Helper()
	_, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	if eventType, _ := event["type"].(string); eventType != expectedType {
		t.Fatalf("event type = %q, want %q; payload=%s", eventType, expectedType, payload)
	}
	return event
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func TestServerRejectsListenerAddressCollision(t *testing.T) {
	store, err := NewStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	address := reserveTCPAddress(t)
	_, err = NewServer(ServerConfig{
		Registry:    NewRegistry(&fakeProvider{id: ProviderLocal}),
		Store:       store,
		Address:     address,
		HTTPAddress: address,
	})
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("collision error = %v, want distinct-listener error", err)
	}
}

func TestHealthReportsRequestTransportListenersAndSelectionWithoutSecrets(t *testing.T) {
	provider := &fakeProvider{id: ProviderLocal, result: Result{Text: "ok"}}
	store, err := NewStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	primaryAddress := reserveTCPAddress(t)
	httpAddress := reserveTCPAddress(t)
	server, err := NewServer(ServerConfig{
		Registry:        NewRegistry(provider),
		Store:           store,
		DefaultProvider: ProviderLocal,
		Address:         primaryAddress,
		HTTPAddress:     httpAddress,
		TLSCertFile:     "synthetic-cert.pem",
		TLSKeyFile:      "synthetic-key.pem",
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "https://example.test/healthz", nil)
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", response.Code)
	}
	var body struct {
		Status          string     `json:"status"`
		ContractVersion string     `json:"contract_version"`
		Transport       string     `json:"transport"`
		DefaultProvider ProviderID `json:"default_provider"`
		Listeners       []struct {
			Scheme  string `json:"scheme"`
			Address string `json:"address"`
			TLS     bool   `json:"tls"`
		} `json:"listeners"`
		Observability struct {
			RequestRecords bool `json:"request_records"`
			TraceFile      bool `json:"trace_file"`
		} `json:"observability"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || body.ContractVersion != ContractVersion || body.Transport != "https" || body.DefaultProvider != ProviderLocal {
		t.Fatalf("health body = %+v", body)
	}
	if len(body.Listeners) != 2 || body.Listeners[0].Scheme != "https" || body.Listeners[0].Address != primaryAddress || !body.Listeners[0].TLS || body.Listeners[1].Scheme != "http" || body.Listeners[1].Address != httpAddress {
		t.Fatalf("health listeners = %#v", body.Listeners)
	}
	if !body.Observability.RequestRecords || !body.Observability.TraceFile {
		t.Fatalf("health observability = %+v", body.Observability)
	}
	if strings.Contains(response.Body.String(), "do-not-leak-this") {
		t.Fatal("health response leaked bearer token")
	}
}
