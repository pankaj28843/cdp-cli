package cdp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestListTargets(t *testing.T) {
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
			t.Errorf("read CDP request: %v", err)
			return
		}
		if req.Method != "Target.getTargets" {
			t.Errorf("method = %q, want Target.getTargets", req.Method)
			return
		}
		resp := map[string]any{
			"id": req.ID,
			"result": map[string]any{
				"targetInfos": []map[string]any{
					{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": true},
				},
			},
		}
		if err := wsjson.Write(r.Context(), conn, resp); err != nil {
			t.Errorf("write CDP response: %v", err)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
	got, err := cdp.ListTargets(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("ListTargets returned error: %v", err)
	}
	if len(got) != 1 || got[0].TargetID != "page-1" || got[0].Type != "page" || !got[0].Attached {
		t.Fatalf("ListTargets() = %+v, want one attached page", got)
	}
}
