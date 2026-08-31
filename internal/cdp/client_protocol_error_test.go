package cdp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestProtocolErrorPreservesChromeRejection(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := wsjson.Read(r.Context(), conn, &req); err != nil {
			return
		}
		_ = wsjson.Write(r.Context(), conn, map[string]any{
			"id": req.ID,
			"error": map[string]any{
				"code":    -32601,
				"message": "method not found",
			},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
	client, err := cdp.Dial(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer client.CloseNormal()

	err = client.Call(context.Background(), "Browser.notReal", map[string]any{}, nil)
	var protocolErr *cdp.ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("Call error = %T %v, want *cdp.ProtocolError", err, err)
	}
	if protocolErr.Method != "Browser.notReal" || protocolErr.Code != -32601 || protocolErr.Message != "method not found" {
		t.Fatalf("ProtocolError = %+v, want method/code/message preserved", protocolErr)
	}
}
