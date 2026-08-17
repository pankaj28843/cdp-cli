package cdp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestCanceledCommandDoesNotCloseSharedConnection(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		for {
			var req struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			response := map[string]any{
				"id": req.ID,
				"result": map[string]any{
					"product": "Chrome/Test",
				},
			}
			if err := wsjson.Write(r.Context(), conn, response); err != nil {
				return
			}
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
	client, err := cdp.Dial(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer client.CloseNormal()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Call(canceled, "Runtime.evaluate", map[string]any{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Call error = %v, want context.Canceled", err)
	}
	// Give the websocket timeout machinery enough time to close the transport
	// when a canceled context is passed to a write. The fixed client must stay
	// usable through this same interval.
	time.Sleep(20 * time.Millisecond)

	var result struct {
		Product string `json:"product"`
	}
	if err := client.Call(context.Background(), "Browser.getVersion", map[string]any{}, &result); err != nil {
		t.Fatalf("follow-up Call returned error: %v", err)
	}
	if result.Product != "Chrome/Test" {
		t.Fatalf("follow-up result product = %q, want Chrome/Test", result.Product)
	}
}
