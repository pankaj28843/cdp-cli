package transcriptionapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageFailureIsUnavailableAndRecovers(t *testing.T) {
	for _, broken := range []string{"audio", "records"} {
		t.Run(broken, func(t *testing.T) {
			store, err := NewEphemeralStore(t.TempDir(), 8<<20)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			provider := &fakeProvider{id: ProviderLocal, result: Result{Text: "synthetic"}}
			server, err := NewServer(ServerConfig{Store: store, Registry: NewRegistry(provider), DefaultProvider: ProviderLocal})
			if err != nil {
				t.Fatal(err)
			}
			blocked := store.audioRoot
			if broken == "records" {
				blocked = filepath.Join(store.root, "requests")
			}
			if err := os.Remove(blocked); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(blocked, []byte("synthetic obstruction"), 0600); err != nil {
				t.Fatal(err)
			}
			checkHealth := func(wantCode int, wantStatus string) {
				t.Helper()
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
				var body struct {
					Status string `json:"status"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if response.Code != wantCode || body.Status != wantStatus {
					t.Fatalf("health = %d %s, want %d %s", response.Code, body.Status, wantCode, wantStatus)
				}
				if strings.Contains(response.Body.String(), blocked) {
					t.Fatal("health leaked storage path")
				}
			}
			checkHealth(http.StatusServiceUnavailable, "degraded")
			if broken == "audio" {
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, newMultipartRequest(t, "/v1/audio/transcriptions", "storage-failure", "synthetic.webm", []byte("synthetic audio"), map[string]string{"model": DefaultModel, "provider": string(ProviderLocal)}))
				if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "audio_persist_failed") || strings.Contains(response.Body.String(), blocked) {
					t.Fatalf("upload response = %d %s", response.Code, response.Body.String())
				}
			}
			if provider.transcribeCall != 0 {
				t.Fatal("storage checks called the provider")
			}
			if err := os.Remove(blocked); err != nil {
				t.Fatal(err)
			}
			checkHealth(http.StatusOK, "ok")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, newMultipartRequest(t, "/v1/audio/transcriptions", "recovered", "synthetic.webm", []byte("synthetic audio"), map[string]string{"model": DefaultModel, "provider": string(ProviderLocal)}))
			if response.Code != http.StatusOK || provider.transcribeCall != 1 {
				t.Fatalf("recovered upload = %d, calls = %d", response.Code, provider.transcribeCall)
			}
			entries, err := os.ReadDir(store.audioRoot)
			if err != nil || len(entries) != 0 {
				t.Fatalf("ephemeral storage was not cleaned: entries=%d err=%v", len(entries), err)
			}
		})
	}
}

func TestEmptyUploadRemainsBadRequest(t *testing.T) {
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server, err := NewServer(ServerConfig{Store: store, Registry: NewRegistry(&fakeProvider{id: ProviderLocal}), DefaultProvider: ProviderLocal})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, newMultipartRequest(t, "/v1/audio/transcriptions", "empty", "synthetic.webm", nil, map[string]string{"model": DefaultModel}))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty upload = %d", response.Code)
	}
}
