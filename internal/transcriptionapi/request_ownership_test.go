package transcriptionapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestInvalidUploadPreservesExistingAudio(t *testing.T) {
	s, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	asset, err := s.PersistAudio(context.Background(), "shared-request", "synthetic.webm", "audio/webm", strings.NewReader("synthetic bytes"))
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{id: ProviderLocal, result: Result{Text: "synthetic"}}
	server, err := NewServer(ServerConfig{Store: s, Registry: NewRegistry(p), DefaultProvider: ProviderLocal})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, newMultipartRequest(t, "/v1/audio/transcriptions", "shared-request", "synthetic.webm", []byte("synthetic"), map[string]string{"duration_ms": "invalid"}))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
	if _, err := os.Stat(asset.PersistedPath); err != nil {
		t.Fatal("invalid upload removed another request's audio")
	}
}

type blockingAudioProvider struct {
	fakeProvider
	entered chan string
	release chan struct{}
}

func (p *blockingAudioProvider) Transcribe(ctx context.Context, r FileRequest) (Result, error) {
	select {
	case p.entered <- r.Audio.PersistedPath:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	if _, err := os.Stat(r.Audio.PersistedPath); err != nil {
		return Result{}, err
	}
	return Result{Text: "synthetic"}, nil
}

func TestOverlappingRequestIDsPreserveOwner(t *testing.T) {
	s, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := &blockingAudioProvider{fakeProvider: fakeProvider{id: ProviderLocal}, entered: make(chan string, 2), release: make(chan struct{})}
	server, err := NewServer(ServerConfig{Store: s, Registry: NewRegistry(p), DefaultProvider: ProviderLocal})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	first := httptest.NewRecorder()
	request := newMultipartRequest(t, "/v1/audio/transcriptions", "same-id", "synthetic.webm", []byte("synthetic audio"), map[string]string{"model": DefaultModel})
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(first, request)
	}()
	released := false
	defer func() {
		if !released {
			close(p.release)
		}
		<-done
	}()
	var path string
	select {
	case path = <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not reach provider")
	}
	second := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	handler.ServeHTTP(second, newMultipartRequest(t, "/v1/audio/transcriptions", "same-id", "synthetic.webm", []byte("replacement"), map[string]string{"model": DefaultModel}).WithContext(ctx))
	if second.Code != http.StatusConflict {
		t.Errorf("overlap status=%d, want 409", second.Code)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "synthetic audio" {
		t.Error("overlap changed the owner's audio")
	}
	close(p.release)
	released = true
	<-done
	if first.Code != http.StatusOK {
		t.Errorf("owner status=%d", first.Code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("owner audio was not cleaned")
	}
	third := httptest.NewRecorder()
	handler.ServeHTTP(third, newMultipartRequest(t, "/v1/audio/transcriptions", "same-id", "synthetic.webm", []byte("next audio"), map[string]string{"model": DefaultModel}))
	if third.Code != http.StatusOK {
		t.Errorf("released ID status=%d", third.Code)
	}
}

func TestUploadCannotClaimRealtimeID(t *testing.T) {
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider := &fakeProvider{id: ProviderLocal, realtime: &fakeRealtime{}}
	server, err := NewServer(ServerConfig{Store: store, Registry: NewRegistry(provider), DefaultProvider: ProviderLocal})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/realtime?intent=transcription", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test done")
	writeRealtimeTestEvent(t, ctx, connection, map[string]any{"type": "session.update", "session": map[string]any{"type": "transcription", "model": DefaultModel, "audio": map[string]any{"input": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}}}}})
	event := readRealtimeTestEvent(t, ctx, connection, "session.created")
	id := event["session"].(map[string]any)["id"].(string)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, newMultipartRequest(t, "/v1/audio/transcriptions", id, "synthetic.webm", []byte("synthetic audio"), map[string]string{"model": DefaultModel}))
	if response.Code != http.StatusConflict {
		t.Errorf("realtime overlap status=%d, want 409", response.Code)
	}
}
