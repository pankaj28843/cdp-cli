package transcriptionapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageFailureIsUnavailableAndRecovers(t *testing.T) {
	t.Run("audio is repaired", func(t *testing.T) {
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
		blocked := store.audioRootPath()
		if err := os.Remove(blocked); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blocked, []byte("synthetic obstruction"), 0600); err != nil {
			t.Fatal(err)
		}
		checkHealth(t, server, http.StatusOK, "ok", true, blocked)
		current := store.audioRootPath()
		if current == blocked {
			t.Fatal("audio repair kept an obstructed root")
		}
		if info, err := os.Stat(blocked); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("audio obstruction was not preserved: info=%v err=%v", info, err)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, newMultipartRequest(t, "/v1/audio/transcriptions", "repaired", "synthetic.webm", []byte("synthetic audio"), map[string]string{"model": DefaultModel, "provider": string(ProviderLocal)}))
		if response.Code != http.StatusOK || provider.transcribeCall != 1 {
			t.Fatalf("repaired upload = %d, calls = %d", response.Code, provider.transcribeCall)
		}
		entries, err := os.ReadDir(current)
		if err != nil || len(entries) != 0 {
			t.Fatalf("ephemeral storage was not cleaned: entries=%d err=%v", len(entries), err)
		}
	})

	t.Run("deleted audio directory is recreated", func(t *testing.T) {
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
		root := store.audioRootPath()
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		checkHealth(t, server, http.StatusOK, "ok", true, root)
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			t.Fatalf("deleted audio root was not recreated: info=%v err=%v", info, err)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, newMultipartRequest(t, "/v1/audio/transcriptions", "recreated", "synthetic.webm", []byte("synthetic audio"), map[string]string{"model": DefaultModel, "provider": string(ProviderLocal)}))
		if response.Code != http.StatusOK || provider.transcribeCall != 1 {
			t.Fatalf("recreated upload = %d, calls = %d", response.Code, provider.transcribeCall)
		}
	})

	t.Run("durable records remain fail closed", func(t *testing.T) {
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
		blocked := filepath.Join(store.root, "requests")
		if err := os.Remove(blocked); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blocked, []byte("synthetic obstruction"), 0600); err != nil {
			t.Fatal(err)
		}
		checkHealth(t, server, http.StatusServiceUnavailable, "degraded", false, blocked)
		if provider.transcribeCall != 0 {
			t.Fatal("storage checks called the provider")
		}
		if err := os.Remove(blocked); err != nil {
			t.Fatal(err)
		}
		checkHealth(t, server, http.StatusOK, "ok", true, blocked)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, newMultipartRequest(t, "/v1/audio/transcriptions", "recovered", "synthetic.webm", []byte("synthetic audio"), map[string]string{"model": DefaultModel, "provider": string(ProviderLocal)}))
		if response.Code != http.StatusOK || provider.transcribeCall != 1 {
			t.Fatalf("recovered upload = %d, calls = %d", response.Code, provider.transcribeCall)
		}
	})
}

func checkHealth(t *testing.T, server *Server, wantCode int, wantStatus string, wantStorageReady bool, forbiddenPath string) {
	t.Helper()
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body struct {
		Status       string `json:"status"`
		StorageReady bool   `json:"storage_ready"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != wantCode || body.Status != wantStatus || body.StorageReady != wantStorageReady {
		t.Fatalf("health = %d status=%s storage_ready=%t, want %d status=%s storage_ready=%t", response.Code, body.Status, body.StorageReady, wantCode, wantStatus, wantStorageReady)
	}
	if strings.Contains(response.Body.String(), forbiddenPath) {
		t.Fatal("health leaked storage path")
	}
}

func TestEphemeralAudioRepairsDuringPersistence(t *testing.T) {
	t.Run("file upload", func(t *testing.T) {
		store, err := NewEphemeralStore(t.TempDir(), 8<<20)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		blocked := store.audioRootPath()
		if err := os.Remove(blocked); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blocked, []byte("synthetic obstruction"), 0600); err != nil {
			t.Fatal(err)
		}
		asset, err := store.PersistAudio(context.Background(), "direct-repair", "synthetic.webm", "audio/webm", strings.NewReader("synthetic audio"))
		if err != nil {
			t.Fatal(err)
		}
		if asset.Bytes != int64(len("synthetic audio")) || asset.PersistedPath == "" || store.audioRootPath() == blocked {
			t.Fatalf("repaired asset = %+v root=%s blocked=%s", asset, store.audioRootPath(), blocked)
		}
	})

	t.Run("realtime append", func(t *testing.T) {
		store, err := NewEphemeralStore(t.TempDir(), 8<<20)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		blocked := store.audioRootPath()
		if err := os.Remove(blocked); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blocked, []byte("synthetic obstruction"), 0600); err != nil {
			t.Fatal(err)
		}
		asset, err := store.AppendAudio(context.Background(), "direct-repair", "realtime.pcm", "audio/pcm", []byte("pcmx"))
		if err != nil {
			t.Fatal(err)
		}
		if asset.Bytes != 4 || asset.PersistedPath == "" || store.audioRootPath() == blocked {
			t.Fatalf("repaired realtime asset = %+v root=%s blocked=%s", asset, store.audioRootPath(), blocked)
		}
	})
}

func TestEphemeralStoreFallsBackWhenSystemTemporaryStorageIsUnavailable(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	blockedTemp := filepath.Join(t.TempDir(), "tmp-file")
	if err := os.WriteFile(blockedTemp, []byte("synthetic obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", blockedTemp)
	store, err := NewEphemeralStore(stateRoot, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fallbackParent := filepath.Join(stateRoot, ".transcription-audio")
	if !strings.HasPrefix(store.audioRootPath(), fallbackParent+string(os.PathSeparator)) {
		t.Fatalf("audio root = %s, want fallback below %s", store.audioRootPath(), fallbackParent)
	}
	if err := store.CheckWritable(); err != nil {
		t.Fatalf("fallback storage check failed: %v", err)
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

func TestRealtimeAudioSurvivesRootReplacement(t *testing.T) {
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	first, err := store.AppendAudio(ctx, "active", "realtime.pcm", "audio/pcm", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	oldRoot := store.audioRootPath()
	if err := os.WriteFile(filepath.Join(oldRoot, "blocked"), []byte("obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PersistAudio(ctx, "blocked", "synthetic.webm", "audio/webm", strings.NewReader("synthetic")); err != nil {
		t.Fatal(err)
	}
	if store.audioRootPath() == oldRoot {
		t.Fatal("request obstruction did not replace root")
	}
	second, err := store.AppendAudio(ctx, "active", "realtime.pcm", "audio/pcm", []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(second.PersistedPath)
	if err != nil || string(data) != "firstsecond" || second.PersistedPath != first.PersistedPath || second.Bytes != 11 {
		t.Fatalf("stream lost prior bytes: asset=%+v data=%q err=%v", second, data, err)
	}
	if err := store.RemoveAudio(ctx, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.PersistedPath); !os.IsNotExist(err) {
		t.Fatalf("retired stream was not cleaned: %v", err)
	}
}

func TestRealtimeAudioFailsWhenPriorChunksDisappear(t *testing.T) {
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	asset, err := store.AppendAudio(ctx, "active", "realtime.pcm", "audio/pcm", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(asset.PersistedPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendAudio(ctx, "active", "realtime.pcm", "audio/pcm", []byte("second")); err == nil {
		t.Fatal("append silently replaced missing prior chunks")
	}
}

func TestEphemeralRepairDoesNotReopenClosedStore(t *testing.T) {
	store, err := NewEphemeralStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.repairEphemeralAudioRoot(true); err == nil || store.audioRootPath() != "" {
		t.Fatal("repair reopened closed store")
	}
}
