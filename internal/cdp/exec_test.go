package cdp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestExec(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := wsjson.Read(r.Context(), conn, &req); err != nil {
			t.Errorf("read CDP request: %v", err)
			return
		}
		if req.Method != "Browser.getVersion" || !json.Valid(req.Params) {
			t.Errorf("request = %+v, want Browser.getVersion with JSON params", req)
			return
		}
		resp := map[string]any{
			"id": req.ID,
			"result": map[string]any{
				"product": "Chrome/Test",
			},
		}
		if err := wsjson.Write(r.Context(), conn, resp); err != nil {
			t.Errorf("write CDP response: %v", err)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
	raw, err := cdp.Exec(context.Background(), endpoint, "Browser.getVersion", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	var got struct {
		Product string `json:"product"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got.Product != "Chrome/Test" {
		t.Fatalf("product = %q, want Chrome/Test", got.Product)
	}
}

func TestExecReadsLargeResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		var req struct {
			ID int64 `json:"id"`
		}
		if err := wsjson.Read(r.Context(), conn, &req); err != nil {
			t.Errorf("read CDP request: %v", err)
			return
		}
		resp := map[string]any{
			"id": req.ID,
			"result": map[string]any{
				"data": strings.Repeat("x", 64*1024),
			},
		}
		if err := wsjson.Write(r.Context(), conn, resp); err != nil {
			t.Errorf("write CDP response: %v", err)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
	raw, err := cdp.Exec(context.Background(), endpoint, "Page.captureScreenshot", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Exec returned error for large response: %v", err)
	}
	var got struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(got.Data) != 64*1024 {
		t.Fatalf("data length = %d, want %d", len(got.Data), 64*1024)
	}
}
