package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/availability"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestHoldDoesNotDialUnavailableDesktop(t *testing.T) {
	t.Setenv("CDP_DAEMON_BROWSER_MODE", "headed")
	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		http.Error(w, "synthetic transient failure", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	for _, reason := range []string{"screen_locked", "lid_closed", "desktop_state_unknown"} {
		t.Run(reason, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := holdWithOptions(ctx, shortInternalStateDir(t), "ws"+strings.TrimPrefix(server.URL, "http"), "auto_connect", time.Millisecond, holdOptions{checkDesktop: func(context.Context) availability.Result { return availability.Result{Reason: reason} }})
			if err == nil || !strings.Contains(err.Error(), reason) {
				t.Fatalf("Hold error=%v", err)
			}
			if dials.Load() != 0 {
				t.Fatalf("unavailable desktop made %d approval-capable requests", dials.Load())
			}
		})
	}
}

func TestHoldRechecksDesktopBeforeRetry(t *testing.T) {
	t.Setenv("CDP_DAEMON_BROWSER_MODE", "headed")
	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		http.Error(w, "synthetic transient failure", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	checks := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := holdWithOptions(ctx, shortInternalStateDir(t), "ws"+strings.TrimPrefix(server.URL, "http"), "auto_connect", time.Millisecond, holdOptions{checkDesktop: func(context.Context) availability.Result {
		checks++
		return availability.Result{Allowed: checks == 1, Reason: "screen_locked"}
	}})
	if err == nil || !strings.Contains(err.Error(), "screen_locked") || dials.Load() != 1 || checks != 2 {
		t.Fatalf("err=%v dials=%d checks=%d", err, dials.Load(), checks)
	}
}

func TestKeepAlivePreservesTransportAcrossUnansweredHeartbeats(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	recovered := make(chan struct{})
	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		calls := 0
		for {
			var request struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if wsjson.Read(ctx, conn, &request) != nil {
				return
			}
			calls++
			// Emulate Chrome ceasing to answer while its approved socket survives.
			if calls <= 3 {
				continue
			}
			if wsjson.Write(ctx, conn, map[string]any{"id": request.ID, "result": map[string]any{}}) != nil {
				return
			}
			if calls == 4 {
				close(recovered)
			}
		}
	}))
	defer server.Close()
	client, err := cdp.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- keepAlive(ctx, client, 10*time.Millisecond, true) }()
	select {
	case err := <-done:
		t.Fatalf("closed the approved transport during unanswered heartbeats: %v", err)
	case <-ctx.Done():
		t.Fatal("heartbeat did not recover")
	case <-recovered:
	}
	var result json.RawMessage
	if err := client.Call(ctx, "Browser.getVersion", nil, &result); err != nil {
		t.Fatalf("recovered transport unusable: %v", err)
	}
	if dials.Load() != 1 {
		t.Fatalf("dials=%d", dials.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("keepalive did not cancel")
	}
}

func TestKeepAliveRetainsHeadlessFailureRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			var request struct {
				ID int64 `json:"id"`
			}
			if wsjson.Read(ctx, conn, &request) != nil {
				return
			}
			if wsjson.Write(ctx, conn, map[string]any{"id": request.ID, "error": map[string]any{"code": -32000, "message": "synthetic unhealthy browser"}}) != nil {
				return
			}
		}
	}))
	defer server.Close()
	client, err := cdp.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	err = keepAlive(ctx, client, time.Millisecond, false)
	if err == nil || !strings.Contains(err.Error(), "3 consecutive") {
		t.Fatalf("headless heartbeat recovery=%v", err)
	}
}

func TestHoldStopsRetryingRejectedApproval(t *testing.T) {
	t.Setenv("CDP_DAEMON_BROWSER_MODE", "headed")
	for _, status := range []int{http.StatusForbidden, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var dials atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				dials.Add(1)
				http.Error(w, "synthetic approval rejection", status)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := holdWithOptions(ctx, shortInternalStateDir(t), "ws"+strings.TrimPrefix(server.URL, "http"), "auto_connect", time.Millisecond, holdOptions{checkDesktop: func(context.Context) availability.Result { return availability.Result{Allowed: true} }})
			var handshake *cdp.HandshakeError
			if !errors.As(err, &handshake) || handshake.StatusCode != status || dials.Load() != 1 {
				t.Fatalf("err=%v dials=%d", err, dials.Load())
			}
		})
	}
}
