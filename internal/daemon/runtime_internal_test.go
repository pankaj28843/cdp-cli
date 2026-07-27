package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const protocolFallbackStageTimeout = 10 * time.Second

func TestRuntimeFetchProtocolFallsBackWhenLiveProtocolMissing(t *testing.T) {
	fallback := func(context.Context) (cdp.Protocol, error) {
		return cdp.Protocol{
			Version: cdp.ProtocolVersion{Major: "1", Minor: "3"},
			Domains: []cdp.Domain{{Domain: "Runtime"}},
		}, nil
	}

	server := newProtocolFallbackFakeServer(t)
	defer server.Close()

	stateDir := shortInternalStateDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- holdWithOptions(ctx, stateDir, fakeProtocolFallbackEndpoint(t, server.URL), "browser_url", 30*time.Second, holdOptions{fetchProtocolFallback: fallback})
	}()
	runtime := waitForProtocolFallbackRuntime(t, ctx, stateDir)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Hold returned error: %v", err)
			}
		case <-time.After(protocolFallbackStageTimeout):
			t.Fatalf("daemon hold did not stop")
		}
	})

	protocol, err := RuntimeClient{Runtime: runtime}.FetchProtocol(ctx)
	if err != nil {
		t.Fatalf("FetchProtocol returned error: %v", err)
	}
	if protocol.Source != "daemon-fallback" || len(protocol.Domains) != 1 || protocol.Domains[0].Domain != "Runtime" {
		t.Fatalf("FetchProtocol = %+v, want fallback Runtime protocol", protocol)
	}
}

func TestRuntimeStructuredDaemonKnownErrors(t *testing.T) {
	fallback := func(context.Context) (cdp.Protocol, error) {
		return cdp.Protocol{}, errors.New("synthetic fallback unavailable")
	}

	server := newProtocolFallbackFakeServer(t)
	defer server.Close()

	stateDir := shortInternalStateDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- holdWithOptions(ctx, stateDir, fakeProtocolFallbackEndpoint(t, server.URL), "browser_url", 30*time.Second, holdOptions{fetchProtocolFallback: fallback})
	}()
	runtime := waitForProtocolFallbackRuntime(t, ctx, stateDir)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Hold returned error: %v", err)
			}
		case <-time.After(protocolFallbackStageTimeout):
			t.Fatalf("daemon hold did not stop")
		}
	})

	_, err := CallRuntime(ctx, runtime, "", "", nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != "rpc_method_required" || rpcErr.Class != "usage" || rpcErr.Message != "daemon rpc method is required" {
		t.Fatalf("empty method error = %#v, want structured rpc_method_required usage", err)
	}

	_, err = RuntimeClient{Runtime: runtime}.FetchProtocol(ctx)
	if !errors.As(err, &rpcErr) || rpcErr.Code != "protocol_fetch_failed" || rpcErr.Class != "connection" {
		t.Fatalf("fetch protocol error = %#v, want structured protocol_fetch_failed connection", err)
	}
}

func TestRuntimeMatchesKeepAliveRequestRejectsMismatchedRuntime(t *testing.T) {
	managed := &browser.ManagedStatus{
		BrowserMode:         "headless",
		ChromePID:           456,
		UserDataDir:         "/tmp/cdp-managed-profile",
		DebuggingPort:       "9222",
		ProfileSeedStrategy: "managed",
	}
	metadata := KeepAliveMetadata{
		UserDataDir:         managed.UserDataDir,
		ManagedBrowser:      managed,
		ManagedProfilePath:  managed.UserDataDir,
		ProfileSeedStrategy: managed.ProfileSeedStrategy,
		ChromePID:           managed.ChromePID,
		ChromePort:          managed.DebuggingPort,
	}
	runtime := Runtime{
		BrowserMode:         "headless",
		ConnectionMode:      "browser_url",
		ReconnectInterval:   "30s",
		Endpoint:            "ws://cdp.example.test/devtools/browser/test",
		UserDataDir:         managed.UserDataDir,
		ManagedBrowser:      managed,
		ManagedProfilePath:  managed.UserDataDir,
		ProfileSeedStrategy: managed.ProfileSeedStrategy,
		ChromePID:           managed.ChromePID,
		ChromePort:          managed.DebuggingPort,
	}
	if !runtimeMatchesKeepAliveRequest(runtime, "headless", runtime.Endpoint, "browser_url", metadata, 30*time.Second) {
		t.Fatalf("runtimeMatchesKeepAliveRequest rejected matching runtime")
	}

	tests := []struct {
		name    string
		runtime Runtime
	}{
		{"browser mode", func() Runtime { r := runtime; r.BrowserMode = "headed"; return r }()},
		{"connection mode", func() Runtime { r := runtime; r.ConnectionMode = "auto_connect"; return r }()},
		{"endpoint", func() Runtime {
			r := runtime
			r.Endpoint = "ws://other-cdp.example.test/devtools/browser/other"
			return r
		}()},
		{"reconnect", func() Runtime { r := runtime; r.ReconnectInterval = ""; return r }()},
		{"user data dir", func() Runtime { r := runtime; r.UserDataDir = "/tmp/other-profile"; return r }()},
		{"managed metadata", func() Runtime { r := runtime; r.ManagedBrowser = nil; return r }()},
		{"chrome port", func() Runtime { r := runtime; r.ChromePort = "9333"; return r }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if runtimeMatchesKeepAliveRequest(tt.runtime, "headless", runtime.Endpoint, "browser_url", metadata, 30*time.Second) {
				t.Fatalf("runtimeMatchesKeepAliveRequest accepted mismatched %s runtime", tt.name)
			}
		})
	}
}

func newProtocolFallbackFakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			var req struct {
				ID        int64           `json:"id"`
				SessionID string          `json:"sessionId"`
				Method    string          `json:"method"`
				Params    json.RawMessage `json:"params"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			resp := map[string]any{"id": req.ID, "result": map[string]any{}}
			if req.SessionID != "" {
				resp["sessionId"] = req.SessionID
			}
			if err := wsjson.Write(r.Context(), conn, resp); err != nil {
				return
			}
		}
	})
	return httptest.NewServer(mux)
}

func fakeProtocolFallbackEndpoint(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	u.Scheme = "ws"
	u.Path = "/devtools/browser/test"
	return u.String()
}

func shortInternalStateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cdp-cli-daemon-*")
	if err != nil {
		t.Fatalf("MkdirTemp returned error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "state")
}

func waitForProtocolFallbackRuntime(t *testing.T, ctx context.Context, stateDir string) Runtime {
	t.Helper()
	deadline := time.Now().Add(protocolFallbackStageTimeout)
	for time.Now().Before(deadline) {
		runtime, ok, err := LoadRuntime(ctx, stateDir)
		if err != nil {
			t.Fatalf("LoadRuntime returned error: %v", err)
		}
		if ok && RuntimeRunning(runtime) && RuntimeSocketReady(ctx, runtime) {
			return runtime
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("daemon runtime did not become ready")
	return Runtime{}
}
