package transcriptionapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthDoesNotWaitForDiskProbeAndExpiresOldSuccess(t *testing.T) {
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server, err := NewServer(ServerConfig{Store: store, Registry: NewRegistry(&fakeProvider{id: ProviderLocal})})
	if err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	var calls atomic.Int64
	blocked, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	server.storageHealth = &storageHealthProbe{
		now: func() time.Time { return time.Unix(0, clock.Load()) },
		check: func() error {
			if calls.Add(1) == 2 {
				close(blocked)
				<-release
			}
			return nil
		},
	}
	request := func(ctx context.Context) (int, storageHealthSnapshot) {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(ctx))
		var body struct {
			Storage storageHealthSnapshot `json:"storage_check"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Error(err)
		}
		return response.Code, body.Storage
	}
	if code, state := request(context.Background()); code != 200 || !state.Ready {
		t.Fatalf("initial health: %d %+v", code, state)
	}
	clock.Add(int64(11 * time.Second))
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if code, state := request(ctx); code != 200 || !state.Ready || !state.Pending || ctx.Err() != nil {
				t.Errorf("blocked disk delayed health: %d %+v", code, state)
			}
		}()
	}
	workers.Wait()
	<-blocked
	if calls.Load() != 2 {
		t.Fatalf("concurrent probes: %d", calls.Load())
	}
	clock.Add(int64(20 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if code, state := request(ctx); code != 503 || state.Ready || !state.Pending {
		t.Fatalf("expired success stayed healthy: %d %+v", code, state)
	}
	if calls.Load() != 2 {
		t.Fatal("expired probe spawned another blocked worker")
	}
}

func TestStorageFailureInvalidatesHealthWithoutAllowingProviderWork(t *testing.T) {
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider := &fakeProvider{id: ProviderLocal}
	server, err := NewServer(ServerConfig{Store: store, Registry: NewRegistry(provider), DefaultProvider: ProviderLocal})
	if err != nil {
		t.Fatal(err)
	}
	checkHealth(t, server, 200, "ok", true, store.root)
	path := filepath.Join(store.root, "requests")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, newMultipartRequest(t, "/v1/audio/transcriptions", "blocked", "synthetic.webm", []byte("synthetic audio"), map[string]string{"model": DefaultModel, "provider": string(ProviderLocal)}))
	if response.Code != 500 || provider.transcribeCall != 0 {
		t.Fatalf("failed persistence served provider: status=%d calls=%d", response.Code, provider.transcribeCall)
	}
	checkHealth(t, server, 503, "degraded", false, store.root)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	checkHealth(t, server, 200, "ok", true, store.root)
}

func TestLateDiskProbeCannotEraseRequestFailure(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	p := &storageHealthProbe{now: time.Now, check: func() error { close(started); <-release; return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.snapshot(ctx)
	<-started
	p.mu.Lock()
	done := p.pending
	p.mu.Unlock()
	p.invalidate()
	close(release)
	<-done
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.snapshotLocked().Ready {
		t.Fatal("late success erased real request failure")
	}
}
