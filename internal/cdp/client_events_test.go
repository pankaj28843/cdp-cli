package cdp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestSubscribeEventCanConsumeInternalEventsWithoutQueuePollution(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			var req struct {
				ID int64 `json:"id"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			if err := wsjson.Write(r.Context(), conn, map[string]any{
				"method": "Target.attachedToTarget",
				"params": map[string]any{"targetInfo": map[string]any{"type": "page"}},
			}); err != nil {
				return
			}
			if err := wsjson.Write(r.Context(), conn, map[string]any{
				"id":     req.ID,
				"result": map[string]any{"ok": true},
			}); err != nil {
				return
			}
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
	client, err := cdp.Dial(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.CloseNormal()

	handled := make(chan struct{}, 1)
	unsubscribe := client.SubscribeEvent("Target.attachedToTarget", func(event cdp.Event) bool {
		if event.Method != "Target.attachedToTarget" {
			t.Errorf("handler method = %q", event.Method)
		}
		handled <- struct{}{}
		return true
	})
	if err := client.Call(context.Background(), "Browser.getVersion", map[string]any{}, nil); err != nil {
		t.Fatalf("Call with subscriber: %v", err)
	}
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("subscriber was not called")
	}
	if events := client.DrainEvents(); len(events) != 0 {
		t.Fatalf("consumed events leaked into queue: %+v", events)
	}

	unsubscribe()
	if err := client.Call(context.Background(), "Browser.getVersion", map[string]any{}, nil); err != nil {
		t.Fatalf("Call after unsubscribe: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if events := client.DrainEvents(); len(events) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("unsubscribed event was not buffered")
}
