package cli_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		os.Exit(cli.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr, cli.BuildInfo{}))
	}
	os.Exit(runWithShortTempDir(m.Run))
}

func runWithShortTempDir(run func() int) int {
	if os.Getenv("CDP_CLI_TEST_SHORT_TMPDIR") == "1" {
		return run()
	}
	dir, err := os.MkdirTemp("/tmp", "cdp-cli-test-*")
	if err != nil {
		return run()
	}
	defer os.RemoveAll(dir)
	oldTMPDIR, oldMarker := os.Getenv("TMPDIR"), os.Getenv("CDP_CLI_TEST_SHORT_TMPDIR")
	_ = os.Setenv("TMPDIR", dir)
	_ = os.Setenv("CDP_CLI_TEST_SHORT_TMPDIR", "1")
	code := run()
	_ = os.Setenv("TMPDIR", oldTMPDIR)
	if oldMarker == "" {
		_ = os.Unsetenv("CDP_CLI_TEST_SHORT_TMPDIR")
	} else {
		_ = os.Setenv("CDP_CLI_TEST_SHORT_TMPDIR", oldMarker)
	}
	return code
}

func fakeWebSocketEndpoint(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse fake server URL: %v", err)
	}
	u.Scheme = "ws"
	u.Path = "/devtools/browser/test"
	return u.String()
}

func startFakeDaemon(t *testing.T, server *httptest.Server, connectionMode string) string {
	t.Helper()
	stateDir := shortCLIStateDir(t)
	t.Setenv("CDP_STATE_DIR", stateDir)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeWebSocketEndpoint(t, server.URL), connectionMode, 30*time.Second)
	}()
	waitForDaemonRuntime(t, ctx, stateDir)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})
	return stateDir
}

func shortCLIStateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cdp-cli-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp returned error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "state")
}

func waitForDaemonRuntime(t *testing.T, ctx context.Context, stateDir string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime, ok, err := daemon.LoadRuntime(ctx, stateDir)
		if err != nil {
			t.Fatalf("LoadRuntime returned error: %v", err)
		}
		if ok && daemon.RuntimeRunning(runtime) && daemon.RuntimeSocketReady(ctx, runtime) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("daemon runtime did not become ready")
}

func newFakeCDPServer(t *testing.T, targets []map[string]any) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		if server == nil {
			http.Error(w, "test server was not initialized", http.StatusInternalServerError)
			return
		}
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/144.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": wsURL,
		})
	})
	mux.HandleFunc("/json/protocol", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": map[string]string{"major": "1", "minor": "3"},
			"domains": []map[string]any{
				{
					"domain":      "Page",
					"description": "Page domain",
					"commands": []map[string]any{
						{"name": "navigate"},
						{"name": "captureScreenshot", "description": "Capture page pixels"},
					},
				},
				{
					"domain":       "Runtime",
					"experimental": true,
					"events": []map[string]any{
						{"name": "consoleAPICalled"},
					},
				},
			},
		})
	})
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
			resp := map[string]any{
				"id": req.ID,
			}
			var events []map[string]any
			if req.SessionID != "" {
				resp["sessionId"] = req.SessionID
			}
			if req.Method == "Target.getTargets" {
				resp["result"] = map[string]any{"targetInfos": targets}
			} else if req.Method == "Target.getTargetInfo" {
				var params struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				var found map[string]any
				for _, target := range targets {
					if target["targetId"] == params.TargetID {
						found = target
						break
					}
				}
				if found == nil {
					resp["error"] = map[string]any{"code": -32000, "message": "target not found"}
				} else {
					resp["result"] = map[string]any{"targetInfo": found}
				}
			} else if req.Method == "Target.createTarget" {
				resp["result"] = map[string]any{"targetId": "created-page"}
			} else if req.Method == "Target.attachToTarget" {
				var params struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				resp["result"] = map[string]any{"sessionId": "session-" + params.TargetID}
			} else if req.Method == "Target.detachFromTarget" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Target.activateTarget" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Target.closeTarget" {
				resp["result"] = map[string]any{"success": true}
			} else if req.Method == "Browser.getWindowForTarget" {
				var params struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				windowID := 1
				if strings.Contains(params.TargetID, "window-2") {
					windowID = 2
				}
				resp["result"] = map[string]any{"windowId": windowID, "bounds": map[string]any{"windowState": "normal"}}
			} else if req.Method == "Page.navigate" {
				resp["result"] = map[string]any{"frameId": "frame-1"}
			} else if req.Method == "Page.enable" || req.Method == "Page.disable" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Page.reload" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Page.getNavigationHistory" {
				resp["result"] = map[string]any{
					"currentIndex": 1,
					"entries": []map[string]any{
						{"id": 1, "url": "https://example.test/previous", "title": "Previous"},
						{"id": 2, "url": "https://example.test/current", "title": "Current"},
						{"id": 3, "url": "https://example.test/next", "title": "Next"},
					},
				}
			} else if req.Method == "Page.navigateToHistoryEntry" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Emulation.setDeviceMetricsOverride" || req.Method == "Emulation.clearDeviceMetricsOverride" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Network.disable" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Network.enable" {
				resp["result"] = map[string]any{}
				events = append(events,
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.requestWillBeSent",
						"params": map[string]any{
							"requestId":   "request-ok",
							"loaderId":    "loader-1",
							"documentURL": "https://example.test/app?session=abc",
							"type":        "Document",
							"timestamp":   1.25,
							"wallTime":    2.5,
							"initiator":   map[string]any{"type": "parser", "url": "https://example.test/app", "lineNumber": 1},
							"request": map[string]any{
								"url":     "https://example.test/app?token=abc",
								"method":  "GET",
								"headers": map[string]any{"Accept": "text/html", "Authorization": "Bearer secret"},
							},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.requestWillBeSentExtraInfo",
						"params": map[string]any{
							"requestId": "request-ok",
							"headers":   map[string]any{"Accept": "text/html", "Authorization": "Bearer secret"},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.responseReceived",
						"params": map[string]any{
							"requestId": "request-ok",
							"type":      "Document",
							"response": map[string]any{
								"url":               "https://example.test/app?token=abc",
								"status":            200,
								"statusText":        "OK",
								"headers":           map[string]any{"Content-Type": "application/json", "Set-Cookie": "session=secret"},
								"mimeType":          "application/json",
								"protocol":          "h2",
								"remoteIPAddress":   "203.0.113.10",
								"remotePort":        443,
								"connectionId":      77,
								"connectionReused":  true,
								"encodedDataLength": 42,
								"timing":            map[string]any{"requestTime": 1.25, "receiveHeadersEnd": 12.5},
							},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.responseReceivedExtraInfo",
						"params": map[string]any{
							"requestId":  "request-ok",
							"statusCode": 200,
							"headers":    map[string]any{"Content-Type": "application/json", "Set-Cookie": "session=secret"},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.loadingFinished",
						"params":    map[string]any{"requestId": "request-ok", "encodedDataLength": 42},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.requestWillBeSent",
						"params": map[string]any{
							"requestId": "request-failed",
							"type":      "Fetch",
							"request": map[string]any{
								"url":         "https://example.test/api",
								"method":      "POST",
								"headers":     map[string]any{"Content-Type": "application/json", "X-CSRF-Token": "secret"},
								"hasPostData": true,
								"postData":    `{"csrf":"secret","query":"value"}`,
							},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.loadingFailed",
						"params": map[string]any{
							"requestId": "request-failed",
							"type":      "Fetch",
							"errorText": "net::ERR_FAILED",
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.webSocketCreated",
						"params": map[string]any{
							"requestId": "ws-1",
							"url":       "wss://example.test/socket?token=abc",
							"initiator": map[string]any{"type": "script"},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.webSocketWillSendHandshakeRequest",
						"params": map[string]any{
							"requestId": "ws-1",
							"timestamp": 3.25,
							"wallTime":  4.5,
							"request":   map[string]any{"headers": map[string]any{"Authorization": "Bearer secret", "Sec-WebSocket-Key": "key"}},
						},
					},
					map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.webSocketHandshakeResponseReceived",
						"params": map[string]any{
							"requestId": "ws-1",
							"response":  map[string]any{"status": 101, "statusText": "Switching Protocols", "headers": map[string]any{"Set-Cookie": "ws=secret"}},
						},
					},
					map[string]any{"sessionId": req.SessionID, "method": "Network.webSocketFrameSent", "params": map[string]any{"requestId": "ws-1", "timestamp": 3.5, "response": map[string]any{"opcode": 1, "mask": true, "payloadData": `{"auth":"secret","kind":"send"}`}}},
					map[string]any{"sessionId": req.SessionID, "method": "Network.webSocketFrameReceived", "params": map[string]any{"requestId": "ws-1", "timestamp": 3.75, "response": map[string]any{"opcode": 1, "payloadData": `{"ok":true}`}}},
					map[string]any{"sessionId": req.SessionID, "method": "Network.webSocketFrameError", "params": map[string]any{"requestId": "ws-1", "timestamp": 3.85, "errorMessage": "synthetic ws warning"}},
					map[string]any{"sessionId": req.SessionID, "method": "Network.webSocketClosed", "params": map[string]any{"requestId": "ws-1", "timestamp": 4.0}},
				)
			} else if req.Method == "Network.getRequestPostData" {
				var params struct {
					RequestID string `json:"requestId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if params.RequestID == "request-failed" {
					resp["result"] = map[string]any{"postData": `{"csrf":"secret","query":"value"}`}
				} else {
					resp["error"] = map[string]any{"code": -32000, "message": "No post data available"}
				}
			} else if req.Method == "Network.getResponseBody" {
				var params struct {
					RequestID string `json:"requestId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if params.RequestID == "request-ok" {
					resp["result"] = map[string]any{"body": `{"ok":true,"token":"secret"}`, "base64Encoded": false}
				} else {
					resp["error"] = map[string]any{"code": -32000, "message": "No resource with given identifier found"}
				}
			} else if req.Method == "Network.getCookies" {
				resp["result"] = map[string]any{"cookies": []map[string]any{{
					"name":     "session",
					"value":    "secret",
					"domain":   "example.test",
					"path":     "/",
					"httpOnly": true,
					"secure":   true,
				}}}
			} else if req.Method == "Network.setCookie" {
				resp["result"] = map[string]any{"success": true}
			} else if req.Method == "Network.deleteCookies" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Input.insertText" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Input.dispatchMouseEvent" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Storage.getUsageAndQuota" {
				resp["result"] = map[string]any{
					"usage":          128,
					"quota":          4096,
					"overrideActive": false,
					"usageBreakdown": []map[string]any{{"storageType": "local_storage", "usage": 64}},
				}
			} else if req.Method == "Runtime.disable" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Runtime.enable" {
				resp["result"] = map[string]any{}
				events = append(events, map[string]any{
					"sessionId": req.SessionID,
					"method":    "Runtime.consoleAPICalled",
					"params": map[string]any{
						"type":      "error",
						"timestamp": 12.25,
						"args": []map[string]any{
							{"type": "string", "value": "Synthetic console error"},
						},
					},
				}, map[string]any{
					"sessionId": req.SessionID,
					"method":    "Runtime.exceptionThrown",
					"params": map[string]any{
						"timestamp": 12.75,
						"exceptionDetails": map[string]any{
							"text":         "Uncaught (in promise)",
							"url":          "https://example.test/assets/app.js",
							"lineNumber":   41,
							"columnNumber": 9,
							"scriptId":     "script-1",
							"exception": map[string]any{
								"type":        "object",
								"subtype":     "error",
								"className":   "TypeError",
								"description": "TypeError: failed to fetch dashboard",
							},
							"stackTrace": map[string]any{
								"callFrames": []map[string]any{{
									"functionName": "loadDashboard",
									"url":          "https://example.test/assets/app.js",
									"lineNumber":   41,
									"columnNumber": 9,
								}},
							},
						},
					},
				})
			} else if req.Method == "Log.disable" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Log.enable" {
				resp["result"] = map[string]any{}
				events = append(events, map[string]any{
					"sessionId": req.SessionID,
					"method":    "Log.entryAdded",
					"params": map[string]any{
						"entry": map[string]any{
							"source":           "network",
							"level":            "error",
							"text":             "Synthetic network failure",
							"timestamp":        12.5,
							"url":              "https://example.test/api",
							"networkRequestId": "request-1",
						},
					},
				})
			} else if req.Method == "Performance.enable" || req.Method == "Performance.disable" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Performance.getMetrics" {
				resp["result"] = map[string]any{
					"metrics": []map[string]any{
						{"name": "Timestamp", "value": 123.5},
						{"name": "DomContentLoaded", "value": 124.5},
					},
				}
			} else if req.Method == "Page.getFrameTree" {
				resp["result"] = map[string]any{
					"frameTree": map[string]any{
						"frame": map[string]any{
							"id":             "frame-main",
							"url":            "https://example.test/app",
							"securityOrigin": "https://example.test",
							"mimeType":       "text/html",
						},
						"childFrames": []map[string]any{{
							"frame": map[string]any{
								"id":             "frame-child",
								"parentId":       "frame-main",
								"url":            "https://example.test/embed",
								"securityOrigin": "https://example.test",
								"mimeType":       "text/html",
							},
						}},
					},
				}
			} else if req.Method == "Runtime.evaluate" {
				if strings.Contains(string(req.Params), "document.visibilityState") {
					hidden := strings.Contains(req.SessionID, "hidden")
					state := "visible"
					if hidden {
						state = "hidden"
					}
					resp["result"] = map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"visibilityState": state, "hidden": hidden, "prerendering": false}}}
				} else {
					resp["result"] = fakeRuntimeEvaluateResult(req.Params)
				}
			} else if req.Method == "Page.captureScreenshot" {
				resp["result"] = map[string]any{
					"data": base64.StdEncoding.EncodeToString([]byte("synthetic screenshot")),
				}
			} else if req.Method == "Browser.getVersion" {
				resp["result"] = map[string]any{"product": "Chrome/Test", "protocolVersion": "1.3"}
			} else if req.Method == "SystemInfo.getProcessInfo" {
				resp["result"] = map[string]any{"processInfo": []map[string]any{{"type": "browser", "id": 100, "cpuTime": 1.5}, {"type": "renderer", "id": 101, "cpuTime": 0.25}}}
			} else {
				resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
			}
			if err := wsjson.Write(r.Context(), conn, resp); err != nil {
				return
			}
			for _, event := range events {
				if err := wsjson.Write(r.Context(), conn, event); err != nil {
					return
				}
			}
		}
	})
	server = httptest.NewServer(mux)
	return server
}

func fakeRuntimeEvaluateResult(params json.RawMessage) map[string]any {
	var req struct {
		Expression string `json:"expression"`
	}
	_ = json.Unmarshal(params, &req)
	if strings.Contains(req.Expression, "__cdp_cli_empty_diagnostics__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"selector_matched":         true,
					"selector_match_count":     1,
					"selected_visible_count":   1,
					"selected_text_length":     0,
					"selected_html_length":     64,
					"body_text_length":         0,
					"body_inner_text_length":   0,
					"body_text_content_length": 0,
					"document_ready_state":     "complete",
					"frame_count":              0,
					"iframe_element_count":     1,
					"shadow_root_count":        1,
					"visible_text_candidates":  0,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_rendered_extract_readiness__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":                  "https://www.google.com/search?q=agentic+engineering+2026+evolutions&safe=active&tbs=qdr:m",
					"document_ready_state": "complete",
					"selector_matched":     true,
					"selector_match_count": 1,
					"selected_text_length": 96,
					"selected_html_length": 256,
					"selected_word_count":  12,
					"body_text_length":     96,
					"body_html_length":     256,
					"dom_signature":        "ready",
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_rendered_extract_links__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"source_url": "https://www.google.com/search?q=agentic+engineering+2026+evolutions&safe=active&tbs=qdr:m",
					"serp":       "google",
					"count":      1,
					"results": []map[string]any{{
						"rank":        1,
						"title":       "From OKRs To Intent Engineering",
						"url":         "https://example.test/story",
						"display_url": "example.test",
						"snippet":     "22 Apr 2026 synthetic result for agentic engineering",
						"date_text":   "22 Apr 2026",
						"type":        "web",
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_form_values__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":   "https://example.test/app",
					"title": "Example App",
					"count": 2,
					"controls": []map[string]any{
						{"selector_hint": "input#q", "tag": "input", "name": "Search", "value": "hello", "visible": true, "aria_hidden": false},
						{"selector_hint": "textarea#out", "tag": "textarea", "name": "Output", "value": "SGVsbG8=", "read_only": true, "visible": true, "aria_hidden": false},
					},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_form_get__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "textarea",
					"count":    1,
					"controls": []map[string]any{},
					"control": map[string]any{
						"selector_hint": "textarea[aria-label=\"Base64 output\"]",
						"tag":           "textarea",
						"role":          "textbox",
						"name":          "Base64 output",
						"value":         "SGVsbG8gVVg=",
						"read_only":     true,
						"disabled":      false,
					},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_text__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "main",
					"count":    1,
					"text":     "Synthetic main text",
					"items": []map[string]any{{
						"index":       0,
						"tag":         "main",
						"text":        "Synthetic main text",
						"text_length": 19,
						"rect":        map[string]any{"x": 0, "y": 0, "width": 600, "height": 200},
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_click_point__") {
		if strings.Contains(req.Expression, `"zero"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"url":      "https://example.test/app",
						"title":    "Example App",
						"selector": "zero",
						"count":    1,
						"clicked":  false,
						"strategy": "raw-input",
						"x":        0,
						"y":        0,
						"rect":     map[string]any{"x": 0, "y": 0, "width": 0, "height": 0},
						"error":    map[string]any{"name": "InvalidTargetError", "message": "target has zero width or height"},
					},
				},
			}
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "main",
					"count":    1,
					"clicked":  true,
					"strategy": "raw-input",
					"x":        310,
					"y":        120,
					"rect":     map[string]any{"x": 10, "y": 20, "width": 600, "height": 200},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_click__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "main",
					"count":    1,
					"clicked":  true,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_type__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "[contenteditable=true]",
					"count":    1,
					"typed":    expressionStringArg(req.Expression, "const text = String("),
					"previous": "before",
					"value":    "before",
					"kind":     "contenteditable",
					"strategy": "insert-text",
					"typing":   true,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_insert_text_result__") {
		text := expressionStringArg(req.Expression, "const text = String(")
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "[contenteditable=true]",
					"count":    1,
					"typed":    text,
					"previous": "before",
					"value":    "before" + text,
					"kind":     "contenteditable",
					"strategy": "insert-text",
					"typing":   true,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_html__") {
		if strings.Contains(req.Expression, `"empty"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"url":      "https://example.test/app",
						"title":    "Example App",
						"selector": "empty",
						"count":    0,
						"items":    []map[string]any{},
					},
				},
			}
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "main",
					"count":    1,
					"items": []map[string]any{{
						"index":       0,
						"tag":         "main",
						"html":        "<main>Synthetic main text</main>",
						"html_length": 32,
						"truncated":   false,
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_dom_query__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "button",
					"count":    1,
					"nodes": []map[string]any{{
						"uid":        "css:button:0",
						"index":      0,
						"tag":        "button",
						"id_attr":    "save",
						"classes":    []string{"primary"},
						"role":       "button",
						"aria_label": "Save",
						"text":       "Save changes",
						"rect":       map[string]any{"x": 10, "y": 20, "width": 100, "height": 32},
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_css_inspect__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "main",
					"found":    true,
					"count":    1,
					"tag":      "main",
					"styles": map[string]string{
						"display":  "block",
						"position": "static",
					},
					"rect": map[string]any{"x": 0, "y": 0, "width": 600, "height": 200},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_layout_overflow__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "body *",
					"count":    1,
					"items": []map[string]any{{
						"uid":           "overflow:0",
						"index":         0,
						"tag":           "div",
						"text":          "Too wide",
						"rect":          map[string]any{"x": 0, "y": 0, "width": 320, "height": 20},
						"client_width":  320,
						"scroll_width":  640,
						"client_height": 20,
						"scroll_height": 20,
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_wait_text__") {
		matched := !strings.Contains(req.Expression, "Never Ready")
		count := 0
		if matched {
			count = 1
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"kind":    "text",
					"needle":  expressionStringArg(req.Expression, "const needle = "),
					"matched": matched,
					"count":   count,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_wait_selector__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"kind":     "selector",
					"selector": "main",
					"matched":  true,
					"count":    1,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_screenshot_element__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"found": true,
					"rect":  map[string]any{"x": 10, "y": 20, "width": 300, "height": 200},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_wait_eval__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"kind":       "eval",
					"expression": "window.__rendered === true",
					"matched":    true,
					"value":      true,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_hn_frontpage__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":   "https://news.ycombinator.com/",
					"title": "Hacker News",
					"count": 1,
					"stories": []map[string]any{{
						"rank":         1,
						"id":           "123",
						"title":        "Synthetic HN story",
						"url":          "https://example.test/story",
						"site":         "example.test",
						"score":        42,
						"user":         "alice",
						"age":          "1 hour ago",
						"comments":     7,
						"comments_url": "https://news.ycombinator.com/item?id=123",
					}},
					"organization": map[string]string{
						"page_kind":             "table-based link aggregator front page",
						"container_selector":    "table.itemlist",
						"story_row_selector":    "tr.athing",
						"metadata_row_selector": "tr.athing + tr .subtext",
						"title_selector":        ".titleline > a",
						"rank_selector":         ".rank",
						"discussion_signal":     "score, author, age, and comment links live in the metadata row after each story row",
					},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_page_load_storage__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":                  "https://example.test/app",
					"origin":               "https://example.test",
					"cookie_keys":          []string{"session"},
					"local_storage_keys":   []string{"feature"},
					"session_storage_keys": []string{"nonce"},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_snapshot__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":    "https://example.test/app",
					"origin": "https://example.test",
					"local_storage": map[string]any{
						"count": 2,
						"keys":  []string{"authToken", "feature"},
						"entries": []map[string]any{
							{"key": "authToken", "value": "secret", "bytes": 6},
							{"key": "feature", "value": "enabled", "bytes": 7},
						},
					},
					"session_storage": map[string]any{
						"count":   1,
						"keys":    []string{"nonce"},
						"entries": []map[string]any{{"key": "nonce", "value": "abc", "bytes": 3}},
					},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_page_info__") {
		return map[string]any{
			"result": map[string]any{
				"type":  "object",
				"value": map[string]any{"url": "https://example.test/app", "origin": "https://example.test"},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_indexeddb_dump__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":         "https://example.test/app",
					"origin":      "https://example.test",
					"operation":   "dump",
					"available":   true,
					"found":       true,
					"database":    "cdp-demo-db",
					"store":       "settings",
					"count":       2,
					"limit":       2,
					"offset":      0,
					"page_size":   2,
					"next_cursor": "eyJrZXkiOiJhZ2VudCJ9",
					"has_more":    true,
					"direction":   "next",
					"records": []map[string]any{
						{"key": "feature", "value": map[string]any{"enabled": true, "source": "demo"}},
						{"key": "agent", "value": map[string]any{"from": "cdp"}},
					},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_get__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":     "https://example.test/app",
					"origin":  "https://example.test",
					"backend": "localStorage",
					"key":     "feature",
					"found":   true,
					"value":   "enabled",
					"bytes":   7,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_set__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"origin":   "https://example.test",
					"backend":  "localStorage",
					"key":      "feature",
					"found":    true,
					"value":    "disabled",
					"previous": "enabled",
					"bytes":    8,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_delete__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"origin":   "https://example.test",
					"backend":  "sessionStorage",
					"key":      "nonce",
					"found":    true,
					"previous": "abc",
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_storage_clear__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":     "https://example.test/app",
					"origin":  "https://example.test",
					"backend": "sessionStorage",
					"cleared": 1,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "querySelectorAll") {
		if strings.Contains(req.Expression, `"empty"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"url":      "https://example.test/feed",
						"title":    "Example Feed",
						"selector": "empty",
						"count":    0,
						"items":    []map[string]any{},
					},
				},
			}
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/feed",
					"title":    "Example Feed",
					"selector": "article",
					"count":    1,
					"items": []map[string]any{
						{
							"index":       0,
							"tag":         "article",
							"role":        "article",
							"aria_label":  "",
							"text":        "First visible synthetic post",
							"text_length": 28,
							"href":        "",
							"rect": map[string]any{
								"x": 0, "y": 10, "width": 600, "height": 120,
							},
						},
					},
				},
			},
		}
	}
	return map[string]any{
		"result": map[string]any{
			"type":  "string",
			"value": "Example App",
		},
	}
}

func expressionStringArg(expression, prefix string) string {
	idx := strings.Index(expression, prefix)
	if idx < 0 {
		return ""
	}
	start := idx + len(prefix)
	for end := strings.Index(expression[start:], ";"); end >= 0; end = strings.Index(expression[start:], ";") {
		candidate := strings.TrimSuffix(expression[start:start+end], ")")
		var value string
		if err := json.Unmarshal([]byte(candidate), &value); err == nil {
			return value
		}
		start += end + 1
	}
	return ""
}
