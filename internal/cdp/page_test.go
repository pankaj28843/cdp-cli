package cdp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestCreateTargetAttachAndEvaluate(t *testing.T) {
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
			resp := map[string]any{"id": req.ID}
			switch req.Method {
			case "Target.createTarget":
				resp["result"] = map[string]any{"targetId": "page-1"}
			case "Target.attachToTarget":
				resp["result"] = map[string]any{"sessionId": "session-1"}
			case "Runtime.evaluate":
				if req.SessionID != "session-1" {
					t.Errorf("Runtime.evaluate session = %q, want session-1", req.SessionID)
				}
				resp["sessionId"] = req.SessionID
				resp["result"] = map[string]any{
					"result": map[string]any{
						"type":  "string",
						"value": "Example App",
					},
				}
			case "Page.captureScreenshot":
				if req.SessionID != "session-1" {
					t.Errorf("Page.captureScreenshot session = %q, want session-1", req.SessionID)
				}
				resp["sessionId"] = req.SessionID
				resp["result"] = map[string]any{
					"data": base64.StdEncoding.EncodeToString([]byte("synthetic screenshot")),
				}
			case "Target.detachFromTarget":
				resp["result"] = map[string]any{}
			default:
				resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
			}
			if err := wsjson.Write(r.Context(), conn, resp); err != nil {
				return
			}
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
	targetID, err := cdp.CreateTarget(context.Background(), endpoint, "https://example.test")
	if err != nil {
		t.Fatalf("CreateTarget returned error: %v", err)
	}
	if targetID != "page-1" {
		t.Fatalf("targetID = %q, want page-1", targetID)
	}

	session, err := cdp.AttachToTarget(context.Background(), endpoint, targetID)
	if err != nil {
		t.Fatalf("AttachToTarget returned error: %v", err)
	}
	defer session.Close(context.Background())
	result, err := session.Evaluate(context.Background(), "document.title", true)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if string(result.Object.Value) != `"Example App"` {
		t.Fatalf("Evaluate value = %s, want Example App", result.Object.Value)
	}
	shot, err := session.CaptureScreenshot(context.Background(), cdp.ScreenshotOptions{Format: "png", FullPage: true})
	if err != nil {
		t.Fatalf("CaptureScreenshot returned error: %v", err)
	}
	if string(shot.Data) != "synthetic screenshot" || shot.Format != "png" {
		t.Fatalf("CaptureScreenshot = %+v, want synthetic png data", shot)
	}
}
