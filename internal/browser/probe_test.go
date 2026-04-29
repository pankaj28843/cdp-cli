package browser_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browser"
)

func TestProbeAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("path = %q, want /json/version", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"Browser":"Chrome/144.0","Protocol-Version":"1.3","webSocketDebuggerUrl":"ws://example/devtools/browser/id"}`))
	}))
	defer server.Close()

	got, err := browser.Probe(context.Background(), browser.ProbeOptions{BrowserURL: server.URL})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if got.State != "cdp_available" || !got.WebSocketDebuggerURL || got.Browser == "" {
		t.Fatalf("Probe() = %+v, want available browser metadata", got)
	}
}

func TestProbeListeningNotCDP(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	got, err := browser.Probe(context.Background(), browser.ProbeOptions{BrowserURL: server.URL})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if got.State != "listening_not_cdp" || got.HTTPStatus != http.StatusNotFound {
		t.Fatalf("Probe() = %+v, want listening_not_cdp 404", got)
	}
}

func TestProbeNotConfigured(t *testing.T) {
	got, err := browser.Probe(context.Background(), browser.ProbeOptions{})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if got.State != "not_configured" {
		t.Fatalf("Probe() state = %q, want not_configured", got.State)
	}
}

func TestProbeAutoConnectAvailable(t *testing.T) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer ln.Close()

	errCh := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		req, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			errCh <- err
			return
		}
		if req.URL.Path != "/devtools/browser/test" || !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
			errCh <- fmt.Errorf("unexpected websocket request: %s upgrade=%s", req.URL.Path, req.Header.Get("Upgrade"))
			return
		}
		_, err = fmt.Fprint(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: test\r\n\r\n")
		errCh <- err
	}()

	userDataDir := t.TempDir()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte(port+"\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	got, err := browser.Probe(context.Background(), browser.ProbeOptions{AutoConnect: true, Channel: "stable", UserDataDir: userDataDir, ActiveProbe: true})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if got.State != "cdp_available" || got.ConnectionMode != "auto_connect" || !got.WebSocketDebuggerURL {
		t.Fatalf("Probe() = %+v, want auto-connect availability", got)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("websocket server returned error: %v", err)
	}
}

func TestProbeAutoConnectPermissionPending(t *testing.T) {
	got, err := browser.Probe(context.Background(), browser.ProbeOptions{AutoConnect: true, UserDataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if got.State != "permission_pending" || got.ConnectionMode != "auto_connect" {
		t.Fatalf("Probe() = %+v, want permission_pending auto_connect", got)
	}
}

func TestProbeAutoConnectPassiveSkipsNetwork(t *testing.T) {
	userDataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte("1\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	got, err := browser.Probe(context.Background(), browser.ProbeOptions{AutoConnect: true, UserDataDir: userDataDir})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if got.State != "active_probe_skipped" || got.ConnectionMode != "auto_connect" || got.WebSocketDebuggerURL {
		t.Fatalf("Probe() = %+v, want passive auto-connect probe", got)
	}
}
