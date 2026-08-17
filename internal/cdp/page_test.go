package cdp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type pageSessionFakeClient struct {
	mu        sync.Mutex
	calls     []string
	responses map[string]error
}

func (c *pageSessionFakeClient) Call(_ context.Context, method string, _ any, result any) error {
	if method == "Target.attachToTarget" && result != nil {
		value := reflect.ValueOf(result)
		if value.Kind() == reflect.Pointer && !value.IsNil() {
			field := value.Elem().FieldByName("SessionID")
			if field.IsValid() && field.CanSet() && field.Kind() == reflect.String {
				field.SetString("session-1")
			}
		}
	}
	return c.call(method)
}

func (c *pageSessionFakeClient) CallSession(_ context.Context, _, method string, _ any, _ any) error {
	return c.call(method)
}

func (c *pageSessionFakeClient) call(method string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, method)
	return c.responses[method]
}

func (c *pageSessionFakeClient) callCount(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, call := range c.calls {
		if call == method {
			count++
		}
	}
	return count
}

func TestPageSessionCloseIsIdempotentAndPreservesCleanupErrors(t *testing.T) {
	detachErr := errors.New("synthetic detach failure")
	closeErr := errors.New("synthetic transport close failure")
	client := &pageSessionFakeClient{responses: map[string]error{
		"Target.detachFromTarget": detachErr,
	}}
	callbackCalls := 0
	session, err := cdp.AttachToTargetWithClient(
		context.Background(),
		client,
		"target-1",
		func(context.Context) error {
			callbackCalls++
			return closeErr
		},
	)
	if err != nil {
		t.Fatalf("AttachToTargetWithClient returned error: %v", err)
	}

	firstErr := session.Close(context.Background())
	secondErr := session.Close(context.Background())
	if !errors.Is(firstErr, detachErr) || !errors.Is(firstErr, closeErr) {
		t.Fatalf("first Close error = %v, want detach and transport errors", firstErr)
	}
	if !errors.Is(secondErr, detachErr) || !errors.Is(secondErr, closeErr) {
		t.Fatalf("second Close error = %v, want the same joined errors", secondErr)
	}
	if got := client.callCount("Target.detachFromTarget"); got != 1 {
		t.Fatalf("detach call count = %d, want one", got)
	}
	if callbackCalls != 1 {
		t.Fatalf("close callback count = %d, want one", callbackCalls)
	}
}

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
	raw, err := session.Exec(context.Background(), "Runtime.evaluate", json.RawMessage(`{"expression":"document.title","returnByValue":true}`))
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	var execResult struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &execResult); err != nil {
		t.Fatalf("Unmarshal Exec result returned error: %v", err)
	}
	if execResult.Result.Value != "Example App" {
		t.Fatalf("Exec value = %q, want Example App", execResult.Result.Value)
	}
	shot, err := session.CaptureScreenshot(context.Background(), cdp.ScreenshotOptions{Format: "png", FullPage: true})
	if err != nil {
		t.Fatalf("CaptureScreenshot returned error: %v", err)
	}
	if string(shot.Data) != "synthetic screenshot" || shot.Format != "png" {
		t.Fatalf("CaptureScreenshot = %+v, want synthetic png data", shot)
	}
}
