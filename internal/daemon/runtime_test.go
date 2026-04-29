package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/daemon"
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
