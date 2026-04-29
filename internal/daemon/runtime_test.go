package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestRuntimeEndpointPersistsButDoesNotMarshal(t *testing.T) {
	stateDir := t.TempDir()
	wantEndpoint := "ws://example.test/devtools/browser/test"
	runtime := daemon.Runtime{
		PID:            os.Getpid(),
		StartedAt:      "2026-04-29T00:00:00Z",
		ConnectionMode: "auto_connect",
		SocketPath:     "daemon.sock",
		Endpoint:       wantEndpoint,
	}

	if err := daemon.SaveRuntime(context.Background(), stateDir, runtime); err != nil {
		t.Fatalf("SaveRuntime returned error: %v", err)
	}
	loaded, ok, err := daemon.LoadRuntime(context.Background(), stateDir)
	if err != nil {
		t.Fatalf("LoadRuntime returned error: %v", err)
	}
	if !ok || loaded.Endpoint != wantEndpoint {
		t.Fatalf("LoadRuntime endpoint = %q, ok=%v; want %q", loaded.Endpoint, ok, wantEndpoint)
	}

	b, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(b), "endpoint") || strings.Contains(string(b), wantEndpoint) {
		t.Fatalf("Runtime JSON exposed endpoint: %s", b)
	}
}

func TestReadLogsTailsJSONLines(t *testing.T) {
	stateDir := t.TempDir()
	content := strings.Join([]string{
		`{"time":"2026-04-29T00:00:00Z","level":"info","event":"hold_start","pid":123}`,
		`{"time":"2026-04-29T00:00:01Z","level":"info","event":"rpc_listening","pid":123}`,
	}, "\n") + "\n"
	if err := os.WriteFile(daemon.RuntimeLogPath(stateDir), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	entries, err := daemon.ReadLogs(context.Background(), stateDir, 1)
	if err != nil {
		t.Fatalf("ReadLogs returned error: %v", err)
	}
	if len(entries) != 1 || entries[0].Event != "rpc_listening" {
		t.Fatalf("ReadLogs = %+v, want last rpc_listening entry", entries)
	}

	empty, err := daemon.ReadLogs(context.Background(), t.TempDir(), 100)
	if err != nil {
		t.Fatalf("ReadLogs missing file returned error: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ReadLogs missing file = %+v, want empty entries", empty)
	}
}

func TestRuntimeClientEventAndProtocolRPC(t *testing.T) {
	server := newRuntimeRPCFakeServer(t)
	defer server.Close()

	stateDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeEndpoint(t, server.URL), "browser_url", 30*time.Second)
	}()
	runtime := waitForRuntime(t, ctx, stateDir)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	client := daemon.RuntimeClient{Runtime: runtime}
	if err := client.CallSession(ctx, "session-1", "Runtime.enable", map[string]any{}, nil); err != nil {
		t.Fatalf("CallSession returned error: %v", err)
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		t.Fatalf("DrainEvents returned error: %v", err)
	}
	if len(events) != 1 || events[0].Method != "Runtime.consoleAPICalled" || events[0].SessionID != "session-1" {
		t.Fatalf("DrainEvents = %+v, want buffered console event", events)
	}

	readCtx, readCancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer readCancel()
	if _, err := client.ReadEvent(readCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReadEvent error = %v, want deadline exceeded", err)
	}

	protocol, err := client.FetchProtocol(ctx)
	if err != nil {
		t.Fatalf("FetchProtocol returned error: %v", err)
	}
	if protocol.Source != "daemon" || len(protocol.Domains) != 1 || protocol.Domains[0].Domain != "Runtime" {
		t.Fatalf("FetchProtocol = %+v, want daemon Runtime protocol", protocol)
	}
}

func newRuntimeRPCFakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/json/protocol", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(cdp.Protocol{
			Version: cdp.ProtocolVersion{Major: "1", Minor: "3"},
			Domains: []cdp.Domain{
				{Domain: "Runtime"},
			},
		})
	})
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
			if req.Method == "Runtime.enable" {
				event := map[string]any{
					"sessionId": req.SessionID,
					"method":    "Runtime.consoleAPICalled",
					"params": map[string]any{
						"type": "error",
						"args": []map[string]any{{"type": "string", "value": "daemon event"}},
					},
				}
				if err := wsjson.Write(r.Context(), conn, event); err != nil {
					return
				}
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

func fakeEndpoint(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	u.Scheme = "ws"
	u.Path = "/devtools/browser/test"
	return u.String()
}

func waitForRuntime(t *testing.T, ctx context.Context, stateDir string) daemon.Runtime {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime, ok, err := daemon.LoadRuntime(ctx, stateDir)
		if err != nil {
			t.Fatalf("LoadRuntime returned error: %v", err)
		}
		if ok && daemon.RuntimeRunning(runtime) && daemon.RuntimeSocketReady(ctx, runtime) {
			return runtime
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("daemon runtime did not become ready")
	return daemon.Runtime{}
}
