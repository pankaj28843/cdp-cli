package transcriptionapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUnsafeRequestIDCannotDeleteStorageParent(t *testing.T) {
	for _, id := range []string{".", "..", "nested/..", "nested/id", `nested\id`} {
		for _, boundary := range []string{"http", "cleanup"} {
			t.Run(boundary+"/"+id, func(t *testing.T) {
				// Even the unfixed cleanup can only remove this disposable sandbox.
				sandbox := t.TempDir()
				store, err := NewStore(filepath.Join(sandbox, "state"), 8<<20)
				if err != nil {
					t.Fatal(err)
				}
				store.retainAudio = false
				store.audioRoot = filepath.Join(sandbox, "audio")
				if err := os.Mkdir(store.audioRoot, 0700); err != nil {
					t.Fatal(err)
				}
				sentinel := filepath.Join(sandbox, "keep")
				if err := os.WriteFile(sentinel, []byte("synthetic sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
				if boundary == "cleanup" {
					if err := store.RemoveAudio(context.Background(), id); err == nil {
						t.Error("cleanup accepted an unsafe request ID")
					}
				} else {
					provider := &fakeProvider{id: ProviderLocal, result: Result{Text: "synthetic"}}
					server, err := NewServer(ServerConfig{Store: store, Registry: NewRegistry(provider), DefaultProvider: ProviderLocal})
					if err != nil {
						t.Fatal(err)
					}
					response := httptest.NewRecorder()
					server.Handler().ServeHTTP(response, newMultipartRequest(t, "/v1/audio/transcriptions", id, "synthetic.webm", []byte("synthetic audio"), map[string]string{"model": DefaultModel}))
					if response.Code != http.StatusBadRequest || provider.transcribeCall != 0 {
						t.Errorf("unsafe ID: status=%d provider calls=%d", response.Code, provider.transcribeCall)
					}
				}
				for _, path := range []string{sentinel, store.audioRoot} {
					if _, err := os.Stat(path); err != nil {
						t.Errorf("storage boundary was deleted: %v", err)
					}
				}
			})
		}
	}
}
