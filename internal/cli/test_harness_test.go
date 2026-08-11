package cli_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

var fakeDelayedAssertCheckedAttempts atomic.Int64
var fakeDelayedAssertVisibleAttempts atomic.Int64
var fakeDelayedAssertHiddenAttempts atomic.Int64
var fakeDelayedAssertEnabledAttempts atomic.Int64
var fakeDelayedAssertDisabledAttempts atomic.Int64
var fakeDelayedAssertEditableAttempts atomic.Int64
var fakeDelayedAssertReadonlyAttempts atomic.Int64
var fakeDelayedAssertTextAttempts atomic.Int64
var fakeDelayedAssertValueAttempts atomic.Int64
var fakeDelayedAssertPageAttempts atomic.Int64
var fakeDelayedAssertCountAttempts atomic.Int64
var fakeDelayedAssertAttributeAttempts atomic.Int64
var fakeDelayedAssertFocusedAttempts atomic.Int64
var fakeDelayedAssertCSSAttempts atomic.Int64
var fakeDelayedAssertRoleAttempts atomic.Int64
var fakeDelayedAssertNameAttempts atomic.Int64
var fakeDelayedAssertViewportAttempts atomic.Int64
var fakeDelayedWaitEvalAttempts atomic.Int64
var fakeSemanticDriftActionabilityAttempts atomic.Int64
var fakeSemanticReplacementDescribeAttempts atomic.Int64
var fakeTargetCreateCount atomic.Int64

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
	oldTMPDIR, oldConfigHome, oldMarker := os.Getenv("TMPDIR"), os.Getenv("XDG_CONFIG_HOME"), os.Getenv("CDP_CLI_TEST_SHORT_TMPDIR")
	_ = os.Setenv("TMPDIR", dir)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	_ = os.Setenv("CDP_CLI_TEST_SHORT_TMPDIR", "1")
	code := run()
	_ = os.Setenv("TMPDIR", oldTMPDIR)
	_ = os.Setenv("XDG_CONFIG_HOME", oldConfigHome)
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
	waitForDaemonRuntimeForMode(t, ctx, stateDir, "headed")
}

func waitForDaemonRuntimeForMode(t *testing.T, ctx context.Context, stateDir, browserMode string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime, ok, err := daemon.LoadRuntimeForMode(ctx, stateDir, browserMode)
		if err != nil {
			t.Fatalf("LoadRuntimeForMode returned error: %v", err)
		}
		if ok && daemon.RuntimeRunning(runtime) && daemon.RuntimeSocketReady(ctx, runtime) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s daemon runtime did not become ready", browserMode)
}

func newFakeCDPServer(t *testing.T, targets []map[string]any) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	var server *httptest.Server
	targetInfos := append([]map[string]any(nil), targets...)
	var createdTargets atomic.Int64
	var listTargetsErrors atomic.Int64
	var createTargetErrors atomic.Int64
	var attachTargetErrors sync.Map
	var runtimeEvaluateErrors sync.Map
	var renderedExtractReadinessCalls sync.Map
	var redditUserRecordCalls sync.Map
	var xProfileRecordCalls sync.Map
	var scrolledSelectors sync.Map
	var navigatedURLs sync.Map
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
						{"name": "captureScreenshot", "description": "Capture page pixels", "parameters": []map[string]any{
							{"name": "format", "type": "string", "optional": true},
							{"name": "quality", "type": "integer", "optional": true},
						}},
					},
				},
				{
					"domain":      "Browser",
					"description": "Browser domain",
					"commands": []map[string]any{
						{"name": "getVersion", "description": "Return browser version metadata"},
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

		blockedSessions := map[string]bool{}
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
				if fakeAnyTargetBool(targetInfos, "fakeListTargetsErrorOnce") && listTargetsErrors.Add(1) == 1 {
					resp["error"] = map[string]any{"code": -32000, "message": "target list race: target closed"}
				} else {
					resp["result"] = map[string]any{"targetInfos": targetInfos}
				}
			} else if req.Method == "Target.setDiscoverTargets" {
				resp["result"] = map[string]any{}
				events = append(events, map[string]any{
					"method": "Target.targetCreated",
					"params": map[string]any{
						"targetInfo": map[string]any{
							"targetId":        "popup-page",
							"type":            "page",
							"title":           "OAuth Popup",
							"url":             "https://example.test/oauth/callback",
							"attached":        false,
							"openerId":        "opener-page",
							"canAccessOpener": true,
						},
					},
				})
			} else if req.Method == "Target.getTargetInfo" {
				var params struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				var found map[string]any
				for _, target := range targetInfos {
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
				fakeTargetCreateCount.Add(1)
				if fakeAnyTargetBool(targetInfos, "fakeCreateTargetErrorOnce") && createTargetErrors.Add(1) == 1 {
					resp["error"] = map[string]any{"code": -32000, "message": "target create race: target closed"}
				} else {
					createIndex := createdTargets.Add(1)
					targetID := "created-page"
					if createIndex > 1 {
						targetID = fmt.Sprintf("created-page-%d", createIndex)
					}
					var params struct {
						URL string `json:"url"`
					}
					_ = json.Unmarshal(req.Params, &params)
					targetInfos = append(targetInfos, map[string]any{
						"targetId": targetID,
						"type":     "page",
						"title":    "Created",
						"url":      params.URL,
						"attached": false,
					})
					resp["result"] = map[string]any{"targetId": targetID}
				}
			} else if req.Method == "Target.attachToTarget" {
				var params struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if fakeTargetBool(targetInfos, params.TargetID, "fakeAttachErrorOnce") {
					if _, loaded := attachTargetErrors.LoadOrStore(params.TargetID, true); !loaded {
						resp["error"] = map[string]any{"code": -32000, "message": "attach race: target closed"}
					} else {
						resp["result"] = map[string]any{"sessionId": "session-" + params.TargetID}
					}
				} else {
					resp["result"] = map[string]any{"sessionId": "session-" + params.TargetID}
				}
			} else if req.Method == "Target.detachFromTarget" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Target.activateTarget" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Target.closeTarget" {
				var params struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if fakeAnyTargetBool(targetInfos, "fakeCloseTargetError") {
					resp["error"] = map[string]any{"code": -32000, "message": "synthetic target close failure"}
				} else if params.TargetID != "" {
					filtered := targetInfos[:0]
					for _, target := range targetInfos {
						if target["targetId"] == params.TargetID {
							continue
						}
						filtered = append(filtered, target)
					}
					targetInfos = filtered
					resp["result"] = map[string]any{"success": true}
				} else {
					resp["result"] = map[string]any{"success": true}
				}
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
			} else if req.Method == "Browser.setDownloadBehavior" {
				resp["result"] = map[string]any{}
				var params struct {
					Behavior      string `json:"behavior"`
					EventsEnabled bool   `json:"eventsEnabled"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if params.EventsEnabled && params.Behavior != "default" {
					for _, target := range targetInfos {
						if target["targetId"] == "download-page" {
							events = append(events,
								map[string]any{
									"method": "Browser.downloadWillBegin",
									"params": map[string]any{
										"frameId":           "frame-download",
										"guid":              "download-1",
										"url":               "https://example.test/download/report.csv?token=abc",
										"suggestedFilename": "report.csv",
									},
								},
								map[string]any{
									"method": "Browser.downloadProgress",
									"params": map[string]any{
										"guid":          "download-1",
										"totalBytes":    18,
										"receivedBytes": 18,
										"state":         "completed",
										"filePath":      "/tmp/cdp-downloads/download-1",
									},
								},
							)
							break
						}
					}
				}
			} else if req.Method == "Browser.setPermission" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Browser.resetPermissions" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Page.navigate" {
				var params struct {
					URL string `json:"url"`
				}
				_ = json.Unmarshal(req.Params, &params)
				lowerURL := strings.ToLower(params.URL)
				blocksAllSERPs := strings.Contains(lowerURL, "serp+block+fixture") || strings.Contains(lowerURL, "serp%20block%20fixture") || strings.Contains(lowerURL, "serp-block-fixture") || strings.Contains(lowerURL, "serp block fixture")
				blocksDuckDuckGo := (strings.Contains(lowerURL, "duck-only-block") || strings.Contains(lowerURL, "duck+only+block") || strings.Contains(lowerURL, "duck%20only%20block")) && strings.Contains(lowerURL, "duckduckgo.com")
				blockedSessions[req.SessionID] = blocksAllSERPs || blocksDuckDuckGo
				navigatedURLs.Store(req.SessionID, params.URL)
				resp["result"] = map[string]any{"frameId": "frame-1"}
			} else if req.Method == "Page.enable" {
				resp["result"] = map[string]any{}
				if strings.Contains(req.SessionID, "dialog") {
					events = append(events, map[string]any{
						"sessionId": req.SessionID,
						"method":    "Page.javascriptDialogOpening",
						"params": map[string]any{
							"url":               "https://example.test/dialog?token=abc",
							"frameId":           "frame-main",
							"message":           "Delete item?",
							"type":              "confirm",
							"hasBrowserHandler": false,
						},
					})
				}
			} else if req.Method == "Page.setInterceptFileChooserDialog" {
				resp["result"] = map[string]any{}
				var params struct {
					Enabled bool `json:"enabled"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if params.Enabled && strings.Contains(req.SessionID, "file-chooser") {
					events = append(events, map[string]any{
						"sessionId": req.SessionID,
						"method":    "Page.fileChooserOpened",
						"params": map[string]any{
							"frameId":       "frame-upload",
							"mode":          "selectSingle",
							"backendNodeId": 42,
						},
					})
				}
			} else if req.Method == "Page.disable" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Page.handleJavaScriptDialog" {
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
			} else if req.Method == "DOM.getDocument" {
				resp["result"] = map[string]any{"root": map[string]any{"nodeId": 1}}
			} else if req.Method == "DOM.querySelector" {
				var params struct {
					Selector string `json:"selector"`
				}
				_ = json.Unmarshal(req.Params, &params)
				nodeID := 2
				if strings.Contains(params.Selector, "nth-of-type(4)") {
					nodeID = 4
				}
				if params.Selector == "#missing" {
					nodeID = 0
				}
				resp["result"] = map[string]any{"nodeId": nodeID}
			} else if req.Method == "DOM.describeNode" {
				var params struct {
					NodeID        int `json:"nodeId"`
					BackendNodeID int `json:"backendNodeId"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if params.BackendNodeID > 0 {
					localName := "input"
					nodeName := "INPUT"
					attributes := []string{"type", "file", "multiple", "", "accept", ".epub,application/epub+zip"}
					if params.BackendNodeID == 248 {
						attributes = []string{"type", "file", "accept", ".epub"}
					}
					if params.BackendNodeID == 249 {
						localName = "button"
						nodeName = "BUTTON"
						attributes = []string{"type", "button"}
					}
					resp["result"] = map[string]any{"node": map[string]any{
						"nodeId":        0,
						"backendNodeId": params.BackendNodeID,
						"nodeType":      1,
						"nodeName":      nodeName,
						"localName":     localName,
						"attributes":    attributes,
					}}
				} else {
					backendNodeID := params.NodeID + 100
					if params.NodeID == 4 && fakeSemanticReplacementDescribeAttempts.Add(1) >= 2 {
						backendNodeID++
					}
					resp["result"] = map[string]any{"node": map[string]any{"nodeId": params.NodeID, "backendNodeId": backendNodeID}}
				}
			} else if req.Method == "DOM.setFileInputFiles" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Emulation.setDeviceMetricsOverride" || req.Method == "Emulation.clearDeviceMetricsOverride" || req.Method == "Emulation.setUserAgentOverride" || req.Method == "Emulation.setGeolocationOverride" || req.Method == "Emulation.clearGeolocationOverride" || req.Method == "Emulation.setEmulatedMedia" || req.Method == "Emulation.setTimezoneOverride" || req.Method == "Emulation.setLocaleOverride" || req.Method == "Emulation.setCPUThrottlingRate" || req.Method == "Network.emulateNetworkConditions" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Network.disable" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Network.setBlockedURLs" {
				var params struct {
					URLs []string `json:"urls"`
				}
				_ = json.Unmarshal(req.Params, &params)
				resp["result"] = map[string]any{}
				if len(params.URLs) > 0 {
					events = append(events,
						map[string]any{"sessionId": req.SessionID, "method": "Network.requestWillBeSent", "params": map[string]any{"requestId": "blocked-1", "request": map[string]any{"url": "https://example.test/analytics/pixel", "method": "GET"}}},
						map[string]any{"sessionId": req.SessionID, "method": "Network.loadingFailed", "params": map[string]any{"requestId": "blocked-1", "errorText": "net::ERR_BLOCKED_BY_CLIENT", "blockedReason": "inspector"}},
					)
				}
			} else if req.Method == "Fetch.enable" {
				resp["result"] = map[string]any{}
				events = append(events,
					map[string]any{"sessionId": req.SessionID, "method": "Fetch.requestPaused", "params": map[string]any{"requestId": "mock-1", "resourceType": "Fetch", "request": map[string]any{"url": "https://example.test/api/config", "method": "GET"}}},
					map[string]any{"sessionId": req.SessionID, "method": "Fetch.requestPaused", "params": map[string]any{"requestId": "mock-2", "resourceType": "Fetch", "request": map[string]any{"url": "https://example.test/api/config", "method": "POST"}}},
				)
			} else if req.Method == "Fetch.fulfillRequest" || req.Method == "Fetch.continueRequest" || req.Method == "Fetch.disable" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Network.setCacheDisabled" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Network.enable" {
				resp["result"] = map[string]any{}
				if strings.Contains(req.SessionID, "event-tap") {
					events = append(events, map[string]any{
						"sessionId": "session-foreign-target",
						"method":    "Network.requestWillBeSent",
						"params": map[string]any{
							"requestId": "foreign-request",
							"request":   map[string]any{"url": "https://foreign.example.test/private", "method": "GET"},
						},
					})
				}
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
				if strings.Contains(req.SessionID, "busy") {
					events = append(events, map[string]any{
						"sessionId": req.SessionID,
						"method":    "Network.requestWillBeSent",
						"params": map[string]any{
							"requestId": "request-pending",
							"type":      "Fetch",
							"request": map[string]any{
								"url":    "https://example.test/stream?token=abc",
								"method": "GET",
							},
						},
					})
				}
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
				cookies := []map[string]any{{
					"name":     "session",
					"value":    "secret",
					"domain":   "example.test",
					"path":     "/",
					"httpOnly": true,
					"secure":   true,
				}}
				if strings.Contains(string(req.Params), "youtube.com") {
					cookies = []map[string]any{{
						"name": "SAPISID", "value": "synthetic-youtube-auth", "domain": ".youtube.com",
						"path": "/", "expires": 2_000_000_000, "httpOnly": true, "secure": true,
					}}
				}
				resp["result"] = map[string]any{"cookies": cookies}
			} else if req.Method == "Network.setCookie" {
				resp["result"] = map[string]any{"success": true}
			} else if req.Method == "Network.deleteCookies" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Input.insertText" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Input.dispatchKeyEvent" {
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
			} else if req.Method == "Tracing.start" {
				resp["result"] = map[string]any{}
			} else if req.Method == "Tracing.end" {
				resp["result"] = map[string]any{}
				events = append(events, map[string]any{"sessionId": req.SessionID, "method": "Tracing.tracingComplete", "params": map[string]any{"stream": "trace-stream-1"}})
			} else if req.Method == "IO.read" {
				trace := map[string]any{"traceEvents": []map[string]any{
					{"name": "navigationStart", "ts": 1000000},
					{"name": "largestContentfulPaint::Candidate", "ts": 1250000, "args": map[string]any{"data": map[string]any{"url": "https://example.test/image?token=trace-secret"}}},
					{"name": "LayoutShift", "ts": 1300000, "args": map[string]any{"data": map[string]any{"had_recent_input": false, "weighted_score_delta": 0.125}}},
					{"name": "RunTask", "ts": 1400000, "dur": 80000},
					{"name": "ResourceSendRequest", "ts": 1500000, "args": map[string]any{"data": map[string]any{"requestId": "slow-1"}}},
					{"name": "ResourceFinish", "ts": 2700000, "args": map[string]any{"data": map[string]any{"requestId": "slow-1"}}},
				}}
				traceBytes, _ := json.Marshal(trace)
				resp["result"] = map[string]any{"data": string(traceBytes), "eof": true}
			} else if req.Method == "IO.close" {
				resp["result"] = map[string]any{}
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
			} else if req.Method == "Page.getLayoutMetrics" {
				resp["result"] = map[string]any{
					"cssLayoutViewport": map[string]any{"clientWidth": 800, "clientHeight": 600},
					"cssContentSize":    map[string]any{"x": 0, "y": 0, "width": 800, "height": 1201},
				}
			} else if req.Method == "Runtime.evaluate" {
				if strings.Contains(string(req.Params), "document.visibilityState") {
					hidden := strings.Contains(req.SessionID, "hidden")
					state := "visible"
					if hidden {
						state = "hidden"
					}
					resp["result"] = map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"visibilityState": state, "hidden": hidden, "prerendering": false}}}
				} else if targetID := strings.TrimPrefix(req.SessionID, "session-"); fakeTargetBool(targetInfos, targetID, "fakeRuntimeEvaluateErrorOnce") {
					if _, loaded := runtimeEvaluateErrors.LoadOrStore(targetID, true); !loaded {
						resp["error"] = map[string]any{"code": -32000, "message": "execution context was destroyed"}
					} else {
						resp["result"] = fakeRuntimeEvaluateResult(req.Params, req.SessionID, blockedSessions[req.SessionID], &scrolledSelectors, &renderedExtractReadinessCalls, &redditUserRecordCalls, &xProfileRecordCalls, &navigatedURLs, targetInfos)
						events = append(events, syntheticNetworkEventsForClick(req.SessionID, req.Params, targetInfos)...)
						events = append(events, syntheticPopupEventsForClick(&targetInfos, req.SessionID, req.Params)...)
						events = append(events, syntheticDownloadEventsForClick(req.SessionID, req.Params, targetInfos)...)
						events = append(events, syntheticDialogEventsForClick(req.SessionID, req.Params, targetInfos)...)
						events = append(events, syntheticFileChooserEventsForClick(req.SessionID, req.Params, targetInfos)...)
						applySyntheticTargetAfterWait(targets, req.SessionID, req.Params)
					}
				} else {
					resp["result"] = fakeRuntimeEvaluateResult(req.Params, req.SessionID, blockedSessions[req.SessionID], &scrolledSelectors, &renderedExtractReadinessCalls, &redditUserRecordCalls, &xProfileRecordCalls, &navigatedURLs, targetInfos)
					events = append(events, syntheticNetworkEventsForClick(req.SessionID, req.Params, targetInfos)...)
					events = append(events, syntheticPopupEventsForClick(&targetInfos, req.SessionID, req.Params)...)
					events = append(events, syntheticDownloadEventsForClick(req.SessionID, req.Params, targetInfos)...)
					events = append(events, syntheticDialogEventsForClick(req.SessionID, req.Params, targetInfos)...)
					events = append(events, syntheticFileChooserEventsForClick(req.SessionID, req.Params, targetInfos)...)
					applySyntheticTargetAfterWait(targets, req.SessionID, req.Params)
				}
			} else if req.Method == "Page.captureScreenshot" {
				resp["result"] = map[string]any{
					"data": base64.StdEncoding.EncodeToString([]byte("synthetic screenshot")),
				}
			} else if req.Method == "Accessibility.getFullAXTree" || req.Method == "Accessibility.getPartialAXTree" {
				resp["result"] = map[string]any{
					"nodes": []map[string]any{
						{
							"nodeId":   "1",
							"ignored":  false,
							"role":     map[string]any{"type": "role", "value": "RootWebArea"},
							"name":     map[string]any{"type": "computedString", "value": "Example App"},
							"childIds": []string{"2", "3", "4"},
						},
						{
							"nodeId":  "2",
							"ignored": false,
							"role":    map[string]any{"type": "role", "value": "textbox"},
							"name":    map[string]any{"type": "computedString", "value": "Editor"},
						},
						{
							"nodeId":  "3",
							"ignored": false,
							"role":    map[string]any{"type": "role", "value": "button"},
							"name":    map[string]any{"type": "computedString", "value": "Submit"},
						},
						{
							"nodeId":  "4",
							"ignored": false,
							"role":    map[string]any{"type": "role", "value": "heading"},
							"name":    map[string]any{"type": "computedString", "value": "Welcome"},
							"properties": []map[string]any{
								{"name": "level", "value": map[string]any{"type": "integer", "value": 1}},
							},
						},
					},
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

func syntheticPopupEventsForClick(targetInfos *[]map[string]any, sessionID string, params json.RawMessage) []map[string]any {
	expression := string(params)
	if !strings.Contains(expression, "__cdp_cli_click__") && !strings.Contains(expression, "__cdp_cli_click_point__") {
		return nil
	}
	openerID := strings.TrimPrefix(sessionID, "session-")
	if openerID == "" || openerID == sessionID {
		return nil
	}
	var opener map[string]any
	for _, target := range *targetInfos {
		if target["targetId"] == openerID {
			opener = target
			break
		}
	}
	if opener == nil || opener["popupOnClick"] != true {
		return nil
	}
	popupID := syntheticStringValue(opener, "popupTargetId", "popup-page")
	popupTitle := syntheticStringValue(opener, "popupTitle", "OAuth Popup")
	popupURL := syntheticStringValue(opener, "popupURL", "https://example.test/oauth/callback")
	if !syntheticTargetInfoExists(*targetInfos, popupID) {
		*targetInfos = append(*targetInfos, map[string]any{
			"targetId":        popupID,
			"type":            "page",
			"title":           popupTitle,
			"url":             popupURL,
			"attached":        false,
			"openerId":        openerID,
			"canAccessOpener": true,
		})
	}
	return []map[string]any{{
		"method": "Target.targetCreated",
		"params": map[string]any{
			"targetInfo": map[string]any{
				"targetId":        popupID,
				"type":            "page",
				"title":           popupTitle,
				"url":             popupURL,
				"attached":        false,
				"openerId":        openerID,
				"canAccessOpener": true,
			},
		},
	}}
}

func syntheticDownloadEventsForClick(sessionID string, params json.RawMessage, targetInfos []map[string]any) []map[string]any {
	expression := string(params)
	if !strings.Contains(expression, "__cdp_cli_click__") && !strings.Contains(expression, "__cdp_cli_click_point__") {
		return nil
	}
	targetID := strings.TrimPrefix(sessionID, "session-")
	if targetID == "" || targetID == sessionID {
		return nil
	}
	var target map[string]any
	for _, candidate := range targetInfos {
		if candidate["targetId"] == targetID {
			target = candidate
			break
		}
	}
	if target == nil || target["downloadOnClick"] != true {
		return nil
	}
	guid := syntheticStringValue(target, "downloadGUID", "click-download-1")
	url := syntheticStringValue(target, "downloadURL", "https://example.test/download/click-report.csv")
	filename := syntheticStringValue(target, "downloadFilename", "click-report.csv")
	filePath := syntheticStringValue(target, "downloadFilePath", "/tmp/cdp-downloads/click-download-1")
	return []map[string]any{
		{
			"method": "Browser.downloadWillBegin",
			"params": map[string]any{
				"frameId":           "frame-download",
				"guid":              guid,
				"url":               url,
				"suggestedFilename": filename,
			},
		},
		{
			"method": "Browser.downloadProgress",
			"params": map[string]any{
				"guid":          guid,
				"totalBytes":    24,
				"receivedBytes": 24,
				"state":         "completed",
				"filePath":      filePath,
			},
		},
	}
}

func syntheticDialogEventsForClick(sessionID string, params json.RawMessage, targetInfos []map[string]any) []map[string]any {
	expression := string(params)
	if !strings.Contains(expression, "__cdp_cli_click__") && !strings.Contains(expression, "__cdp_cli_click_point__") {
		return nil
	}
	targetID := strings.TrimPrefix(sessionID, "session-")
	if targetID == "" || targetID == sessionID {
		return nil
	}
	var target map[string]any
	for _, candidate := range targetInfos {
		if candidate["targetId"] == targetID {
			target = candidate
			break
		}
	}
	if target == nil || target["dialogOnClick"] != true {
		return nil
	}
	return []map[string]any{{
		"sessionId": sessionID,
		"method":    "Page.javascriptDialogOpening",
		"params": map[string]any{
			"url":               syntheticStringValue(target, "dialogURL", "https://example.test/dialog?token=abc"),
			"frameId":           syntheticStringValue(target, "dialogFrameID", "frame-dialog"),
			"message":           syntheticStringValue(target, "dialogMessage", "Delete item?"),
			"type":              syntheticStringValue(target, "dialogType", "confirm"),
			"hasBrowserHandler": false,
		},
	}}
}

func syntheticFileChooserEventsForClick(sessionID string, params json.RawMessage, targetInfos []map[string]any) []map[string]any {
	expression := string(params)
	if !strings.Contains(expression, "__cdp_cli_click__") && !strings.Contains(expression, "__cdp_cli_click_point__") {
		return nil
	}
	targetID := strings.TrimPrefix(sessionID, "session-")
	if targetID == "" || targetID == sessionID {
		return nil
	}
	var target map[string]any
	for _, candidate := range targetInfos {
		if candidate["targetId"] == targetID {
			target = candidate
			break
		}
	}
	if target == nil || target["fileChooserOnClick"] != true {
		return nil
	}
	mode := syntheticStringValue(target, "fileChooserMode", "selectSingle")
	backendNodeID := 42
	if value, ok := target["fileChooserBackendNodeID"].(int); ok && value > 0 {
		backendNodeID = value
	}
	return []map[string]any{{
		"sessionId": sessionID,
		"method":    "Page.fileChooserOpened",
		"params": map[string]any{
			"frameId":       syntheticStringValue(target, "fileChooserFrameID", "frame-upload"),
			"mode":          mode,
			"backendNodeId": backendNodeID,
		},
	}}
}

func syntheticNetworkEventsForClick(sessionID string, params json.RawMessage, targetInfos []map[string]any) []map[string]any {
	expression := string(params)
	if !strings.Contains(expression, "__cdp_cli_click__") && !strings.Contains(expression, "__cdp_cli_click_point__") {
		return nil
	}
	targetID := strings.TrimPrefix(sessionID, "session-")
	if targetID == "" || targetID == sessionID {
		return nil
	}
	var target map[string]any
	for _, candidate := range targetInfos {
		if candidate["targetId"] == targetID {
			target = candidate
			break
		}
	}
	if target == nil || target["networkOnClick"] != true {
		return nil
	}
	requestID := syntheticStringValue(target, "networkRequestID", "click-request-1")
	rawURL := syntheticStringValue(target, "networkURL", "https://example.test/api/click?token=abc")
	method := syntheticStringValue(target, "networkMethod", "POST")
	resourceType := syntheticStringValue(target, "networkResourceType", "Fetch")
	status := 201
	if value, ok := target["networkStatus"].(int); ok && value > 0 {
		status = value
	}
	statusText := syntheticStringValue(target, "networkStatusText", "Created")
	mimeType := syntheticStringValue(target, "networkMimeType", "application/json")
	return []map[string]any{
		{
			"sessionId": sessionID,
			"method":    "Network.requestWillBeSent",
			"params": map[string]any{
				"requestId": requestID,
				"type":      resourceType,
				"request": map[string]any{
					"url":    rawURL,
					"method": method,
				},
			},
		},
		{
			"sessionId": sessionID,
			"method":    "Network.responseReceived",
			"params": map[string]any{
				"requestId": requestID,
				"type":      resourceType,
				"response": map[string]any{
					"url":        rawURL,
					"status":     status,
					"statusText": statusText,
					"mimeType":   mimeType,
				},
			},
		},
		{
			"sessionId": sessionID,
			"method":    "Network.loadingFinished",
			"params":    map[string]any{"requestId": requestID, "encodedDataLength": 64},
		},
	}
}

func syntheticStringValue(values map[string]any, key, fallback string) string {
	value, ok := values[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func fakeAnyTargetBool(targetInfos []map[string]any, key string) bool {
	for _, target := range targetInfos {
		if fakeMapBool(target, key) {
			return true
		}
	}
	return false
}

func fakeAnyTargetString(targetInfos []map[string]any, key string) string {
	for _, target := range targetInfos {
		if value, ok := target[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func fakeTargetBool(targetInfos []map[string]any, targetID, key string) bool {
	for _, target := range targetInfos {
		if target["targetId"] == targetID && fakeMapBool(target, key) {
			return true
		}
	}
	return false
}

func fakeMapBool(values map[string]any, key string) bool {
	value, ok := values[key].(bool)
	return ok && value
}

func syntheticTargetInfoExists(targetInfos []map[string]any, targetID string) bool {
	for _, target := range targetInfos {
		if target["targetId"] == targetID {
			return true
		}
	}
	return false
}

func applySyntheticTargetAfterWait(targets []map[string]any, sessionID string, params json.RawMessage) {
	expression := string(params)
	if !strings.Contains(expression, "__cdp_cli_wait_text__") && !strings.Contains(expression, "__cdp_cli_wait_selector__") && !strings.Contains(expression, "__cdp_cli_wait_url__") {
		return
	}
	targetID := strings.TrimPrefix(sessionID, "session-")
	if targetID == sessionID {
		return
	}
	for _, target := range targets {
		if target["targetId"] != targetID {
			continue
		}
		if title, ok := target["afterTitle"].(string); ok {
			target["title"] = title
		}
		if rawURL, ok := target["afterURL"].(string); ok {
			target["url"] = rawURL
		}
		return
	}
}

func fakeRuntimeEvaluateResult(params json.RawMessage, sessionID string, serpBlocked bool, scrolledSelectors, renderedExtractReadinessCalls, redditUserRecordCalls, xProfileRecordCalls, navigatedURLs *sync.Map, targetInfos []map[string]any) map[string]any {
	var req struct {
		Expression string `json:"expression"`
	}
	_ = json.Unmarshal(params, &req)
	if strings.Contains(req.Expression, "window.scrollTo(0, document.body.scrollHeight)") {
		scrolledSelectors.Store(sessionID+":x-profile", true)
	}
	if strings.Contains(req.Expression, "target_found") && strings.Contains(req.Expression, "document.activeElement") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"target_found": true,
					"focused":      true,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_rendered_discussion_expand__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"status":       "exhausted",
					"interactions": 2,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_hn_thread_records__") {
		return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"records": []map[string]any{{"kind": "story", "id": "46641042", "canonical_url": "/item?id=46641042", "title": "Synthetic HN story", "author": "alice"}, {"kind": "comment", "id": "46642165", "canonical_url": "/item?id=46642165", "depth": 0, "body": "Synthetic HN comment", "author": "bob"}, {"kind": "comment", "id": "46644995", "canonical_url": "/item?id=46644995", "depth": 1, "parent_id": "46642165", "body": "Synthetic nested reply"}}}}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_arxiv_paper__") {
		return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"paper": map[string]any{"identifier": "2604.12374v2", "canonical_url": "/abs/2604.12374v2", "title": "Synthetic paper"}, "references": []map[string]any{{"id": "ref-1", "paper_identifier": "2604.12374v2", "text": "Synthetic reference"}}}}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_linkedin_progress__") {
		return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"terminal_extent": 1000}}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_reddit_thread_records__") {
		return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"records": []map[string]any{
			{"kind": "submission", "id": "t3_1v010h6", "canonical_url": "/r/codex/comments/1v010h6/the_sun_came_out/", "subreddit": "codex", "root_thread_id": "t3_1v010h6", "author": "op", "title": "Synthetic root", "discovery_surface": "thread_root"},
			{"kind": "comment", "id": "t1_ozckogc", "canonical_url": "/r/codex/comments/1v010h6/the_sun_came_out/comment/ozckogc/", "subreddit": "codex", "root_thread_id": "t3_1v010h6", "parent_id": "t3_1v010h6", "author": "reply", "discovery_surface": "thread_comment_tree"},
		}}}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_x_records__") {
		if strings.Contains(req.Expression, "isThread = true") {
			return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"records": []map[string]any{
				{"kind": "post", "id": "2079610838143623371", "canonical_url": "/karpathy/status/2079610838143623371", "handle": "karpathy", "root_status_id": "2079610838143623371", "body": "Synthetic root", "discovery_surface": "thread_root"},
				{"kind": "reply", "id": "2079610838143623999", "canonical_url": "/reply/status/2079610838143623999", "handle": "reply", "root_status_id": "2079610838143623371", "body": "Synthetic reply", "discovery_surface": "conversation"},
			}}}}
		}
		calls, _ := xProfileRecordCalls.LoadOrStore(sessionID, 0)
		xProfileRecordCalls.Store(sessionID, calls.(int)+1)
		records := []map[string]any{{"kind": "post", "id": "2079610838143623371", "canonical_url": "/karpathy/status/2079610838143623371", "handle": "karpathy", "root_status_id": "2079610838143623371", "body": "Synthetic profile post", "discovery_surface": "profile_posts"}}
		if _, scrolled := scrolledSelectors.Load(sessionID + ":x-profile"); scrolled && calls.(int) > 0 {
			records = []map[string]any{
				{"kind": "post", "id": "2079610838143623999", "canonical_url": "/karpathy/status/2079610838143623999", "handle": "karpathy", "root_status_id": "2079610838143623999", "body": "Synthetic second post", "discovery_surface": "profile_posts"},
				{"kind": "post", "id": "2079610838143624000", "canonical_url": "/karpathy/status/2079610838143624000", "handle": "karpathy", "root_status_id": "2079610838143624000", "body": "Synthetic third post", "discovery_surface": "profile_posts"},
			}
		}
		return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"records": records}}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_linkedin_records__") {
		if strings.Contains(req.Expression, "isThread = true") {
			return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"records": []map[string]any{
				{"kind": "activity", "id": "7482842673645584386", "data_urn": "urn:li:activity:7482842673645584386", "canonical_url": "/posts/example-activity-7482842673645584386-9aSD/", "activity_id": "7482842673645584386", "timestamp": "2026-07-24T00:00:00Z", "discovery_surface": "activity_root"},
				{"kind": "comment", "id": "urn:li:comment:(activity:7482842673645584386,7482842673645584387)", "canonical_url": "/posts/example-activity-7482842673645584386-9aSD/", "activity_id": "7482842673645584386", "body": "Synthetic reply", "discovery_surface": "activity_comment"},
			}}}}
		}
		return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"records": []map[string]any{
			{"kind": "activity", "id": "7482842673645584386", "data_urn": "urn:li:activity:7482842673645584386", "canonical_url": "/posts/example-activity-7482842673645584386-9aSD/", "activity_id": "7482842673645584386", "company": "the-pragmatic-engineer", "timestamp": "2026-07-24T00:00:00Z", "discovery_surface": "company_posts"},
			{"kind": "activity", "id": "7482842673645584387", "data_urn": "urn:li:activity:7482842673645584387", "canonical_url": "/posts/example-activity-7482842673645584387-9aSD/", "activity_id": "7482842673645584387", "company": "the-pragmatic-engineer", "timestamp": "2026-07-24T00:01:00Z", "discovery_surface": "company_posts"},
		}}}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_reddit_user_records__") {
		calls, _ := redditUserRecordCalls.LoadOrStore(sessionID, 0)
		redditUserRecordCalls.Store(sessionID, calls.(int)+1)
		records := []map[string]any{{"kind": "submission", "id": "t3_1v010h6", "canonical_url": "/r/codex/comments/1v010h6/the_sun_came_out/", "subreddit": "codex", "root_thread_id": "t3_1v010h6", "author": "celticpaladin", "title": "Synthetic submission", "discovery_surface": "user_submission"}}
		if calls.(int) > 0 {
			records = []map[string]any{
				{"kind": "comment", "id": "t1_ozckogc", "canonical_url": "/r/codex/comments/1v010h6/the_sun_came_out/comment/ozckogc/", "subreddit": "codex", "root_thread_id": "t3_1v010h6", "author": "celticpaladin", "discovery_surface": "user_comment"},
				{"kind": "comment", "id": "t1_second", "canonical_url": "/r/codex/comments/1v010h6/the_sun_came_out/comment/second/", "subreddit": "codex", "root_thread_id": "t3_1v010h6", "author": "celticpaladin", "discovery_surface": "user_comment"},
			}
		}
		return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"records": records}}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_rendered_content__") {
		if fakeAnyTargetBool(targetInfos, "fakeRenderedContentFailure") {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"markdown":      "",
						"root_selector": "body",
						"item_count":    0,
						"error": map[string]any{
							"name":    "SyntheticError",
							"message": "synthetic native content failure",
						},
					},
				},
			}
		}
		if strings.Contains(req.Expression, `const profile = "arxiv"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"markdown":      "# Synthetic arXiv Paper\n\nA rendered paper with $x^2$ and a [supporting source](https://example.test/source).\n\n## Results\n\nSemantic extraction works.",
						"root_selector": "article.ltx_document",
						"item_count":    2,
					},
				},
			}
		}
		if strings.Contains(req.Expression, `const profile = "hacker-news"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"markdown":      "# Synthetic HN discussion\n\n[Source](https://example.test/story) · [HN discussion](https://news.ycombinator.com/item?id=46641042)\n\n## Comments (2)\n\n- **alice** · [1 hour ago](https://news.ycombinator.com/item?id=101)\n\n    Parent comment.\n\n    - **bob** · [45 minutes ago](https://news.ycombinator.com/item?id=102)\n\n        Nested reply.",
						"root_selector": "table.fatitem, table.comment-tree",
						"item_count":    2,
					},
				},
			}
		}
		if strings.Contains(req.Expression, `const profile = "x"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"markdown":         "# Synthetic X post\n\nSynthetic author · 2026-07-24T00:00:00.000Z\n\nNative X extraction excludes reply chrome.\n\n## Replies (1)\n\n- A synthetic reply.",
						"root_selector":    `article[data-testid="tweet"]`,
						"item_count":       2,
						"discussion_count": 1,
					},
				},
			}
		}
		if strings.Contains(req.Expression, `const profile = "x-profile"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"markdown":      "# X profile @karpathy\n\n## [Post](https://x.com/karpathy/status/2079610838143623371)\n\nA full synthetic profile post.",
						"root_selector": `article[data-testid="tweet"]`,
						"item_count":    1,
					},
				},
			}
		}
		if strings.Contains(req.Expression, `const profile = "reddit-user-profile"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"markdown":      "# Reddit profile u/celticpaladin\n\n## [Comment](https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/comment/ozckogc/)\n\nA synthetic profile comment.",
						"root_selector": "shreddit-feed shreddit-profile-comment",
						"item_count":    1,
					},
				},
			}
		}
		if strings.Contains(req.Expression, `const profile = "linkedin-company-posts"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"markdown":      "# LinkedIn company the-pragmatic-engineer\n\n## Activity 7482842673645584386\n\nA synthetic company post.",
						"root_selector": `[role="article"][data-urn^="urn:li:activity:"]`,
						"item_count":    1,
					},
				},
			}
		}
		if strings.Contains(req.Expression, `const profile = "linkedin"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"markdown":         "# Synthetic LinkedIn post\n\nNative LinkedIn extraction excludes recommendations.\n\n## Comments (1)\n\n- A synthetic comment.",
						"root_selector":    ".feed-shared-update-v2",
						"item_count":       2,
						"discussion_count": 1,
					},
				},
			}
		}
		if strings.Contains(req.Expression, `const profile = "reddit"`) {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"markdown":         "# Synthetic Reddit post\n\nNative Reddit extraction captures bounded rendered comments only.\n\n## Rendered comments (1)\n\n- A rendered comment.",
						"root_selector":    "shreddit-post",
						"item_count":       2,
						"discussion_count": 1,
					},
				},
			}
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_google_maps_directions__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"title":               "Hvidegaard Møn to Møn Is - Google Maps",
					"url":                 "https://www.google.com/maps/dir/?api=1",
					"visible_text_length": 420,
					"page_state":          "unknown",
					"origin_labels":       []string{"Hvidegaard Møn"},
					"destination_labels":  []string{"Møn Is"},
					"cards": []map[string]any{
						{"text": "7 min 5.1 km via Søndersognsvej Fastest route", "role": "button"},
						{"text": "9 min 6.5 km via Vængesgårdsvej"},
					},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "Intl.DateTimeFormat().resolvedOptions().timeZone") {
		return map[string]any{"result": map[string]any{"type": "string", "value": "UTC"}}
	}
	if strings.Contains(req.Expression, "Intl.DateTimeFormat().resolvedOptions().locale") {
		return map[string]any{"result": map[string]any{"type": "string", "value": "de-DE"}}
	}
	if strings.Contains(req.Expression, "prefers-color-scheme") {
		return map[string]any{"result": map[string]any{"type": "string", "value": "dark"}}
	}
	if strings.Contains(req.Expression, `(document.body && document.body.innerText || "").slice(0, 20000)`) {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":   "https://example.test/login",
					"title": "Example Login",
					"text":  "Please sign in to continue.",
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_locator_find__") {
		by := expressionStringArg(req.Expression, "const by = ")
		query := expressionStringArg(req.Expression, "const query = ")
		roleQuery := expressionStringArg(req.Expression, "const roleQuery = ")
		if by == "" {
			by = "label"
		}
		if query == "" {
			query = "Search"
		}
		includeHidden := strings.Contains(req.Expression, "const includeHidden = true")
		exact := strings.Contains(req.Expression, "const exact = true")
		selector := "input#q"
		selectorHint := selector
		selectorAmbiguous := false
		resolvedNodeSelector := selector
		tag := "input"
		elementType := "search"
		role := "searchbox"
		name := query
		placeholder := "Search"
		disabled := false
		readOnly := false
		contentEditable := false
		if by == "role" && roleQuery == "button" {
			selector = "button#submit"
			selectorHint = selector
			resolvedNodeSelector = selector
			tag = "button"
			elementType = "submit"
			role = "button"
			placeholder = ""
		}
		if query == "Delete Chat" && by == "role" && roleQuery == "menuitem" {
			selector = "main > div:nth-of-type(2) > div:nth-of-type(2)"
			selectorHint = "div[role=\"menuitem\"]"
			selectorAmbiguous = true
			resolvedNodeSelector = selector
			tag = "div"
			elementType = ""
			role = "menuitem"
			name = "Delete Chat"
			placeholder = ""
		}
		if query == "Drifting menuitem" && by == "role" && roleQuery == "menuitem" {
			selector = "main > div:nth-of-type(3) > div:nth-of-type(2)"
			selectorHint = "div[role=\"menuitem\"]"
			selectorAmbiguous = true
			resolvedNodeSelector = selector
			tag = "div"
			elementType = ""
			role = "menuitem"
			name = "Drifting menuitem"
			placeholder = ""
		}
		if query == "Replacing menuitem" && by == "role" && roleQuery == "menuitem" {
			selector = "main > div:nth-of-type(4) > div:nth-of-type(2)"
			selectorHint = "div[role=\"menuitem\"]"
			selectorAmbiguous = true
			resolvedNodeSelector = selector
			tag = "div"
			elementType = ""
			role = "menuitem"
			name = "Replacing menuitem"
			placeholder = ""
		}
		if query == "Duplicate menuitem" {
			matches := make([]map[string]any, 0, 2)
			for index := 0; index < 2; index++ {
				matches = append(matches, map[string]any{
					"index":                  index,
					"selector_hint":          "div[role=\"menuitem\"]",
					"selector_ambiguous":     true,
					"resolved_node_selector": fmt.Sprintf("main > div:nth-of-type(%d)", index+1),
					"tag":                    "div",
					"role":                   "menuitem",
					"name":                   "Duplicate menuitem",
					"visible":                true,
					"rect":                   map[string]any{"x": 10, "y": 20 + index*30, "width": 300, "height": 24},
				})
			}
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"url": "https://example.test/app", "title": "Example App",
						"by": by, "query": query, "role": roleQuery, "exact": exact,
						"include_hidden": includeHidden, "test_id_attr": "data-testid",
						"count": 2, "returned": 2, "strict": false, "matches": matches,
					},
				},
			}
		}
		if query == "Drag target" || query == "drag-target" {
			selector = "div#drag-target"
			tag = "div"
			elementType = ""
			role = ""
			name = "Drag target"
			placeholder = ""
		}
		if query == "Scroll target" || query == "scroll-target" {
			selector = "div#scroll-target"
			tag = "div"
			elementType = ""
			role = ""
			name = "Scroll target"
			placeholder = ""
		}
		if query == "Plan" {
			selector = "select#plan"
			tag = "select"
			elementType = ""
			role = "combobox"
			name = "Plan"
			placeholder = ""
		}
		if query == "Upload file" {
			selector = "input#upload"
			tag = "input"
			elementType = "file"
			role = "textbox"
			name = "Upload file"
			placeholder = ""
		}
		if query == "Subscribe to newsletter" || query == "Subscribe" || (by == "role" && roleQuery == "checkbox") {
			selector = "input#subscribe"
			tag = "input"
			elementType = "checkbox"
			role = "checkbox"
			name = "Subscribe to newsletter"
			placeholder = ""
		}
		if query == "Below fold checkbox" {
			selector = "input#below-fold-checkbox"
			tag = "input"
			elementType = "checkbox"
			role = "checkbox"
			name = "Below fold checkbox"
			placeholder = ""
		}
		if query == "Optional updates" {
			selector = "input#optional-updates"
			tag = "input"
			elementType = "checkbox"
			role = "checkbox"
			name = "Optional updates"
			placeholder = ""
		}
		if query == "Delayed checkbox" {
			selector = "input#delayed-check"
			tag = "input"
			elementType = "checkbox"
			role = "checkbox"
			name = "Delayed checkbox"
			placeholder = ""
		}
		if query == "Delayed visible" {
			selector = "button#delayed-visible"
			tag = "button"
			elementType = "button"
			role = "button"
			name = "Delayed visible"
			placeholder = ""
		}
		if query == "Delayed enabled" {
			selector = "button#delayed-enabled"
			tag = "button"
			elementType = "button"
			role = "button"
			name = "Delayed enabled"
			placeholder = ""
			disabled = true
		}
		if query == "Delayed disabled" {
			selector = "button#delayed-disabled"
			tag = "button"
			elementType = "button"
			role = "button"
			name = "Delayed disabled"
			placeholder = ""
		}
		if query == "Disabled target" {
			selector = "button#disabled-action"
			tag = "button"
			elementType = "button"
			role = "button"
			name = "Disabled target"
			placeholder = ""
			disabled = true
		}
		if query == "Read-only notes" {
			selector = "textarea#readonly-notes"
			tag = "textarea"
			elementType = ""
			role = "textbox"
			name = "Read-only notes"
			placeholder = ""
			readOnly = true
		}
		if query == "Delayed editable" {
			selector = "input#delayed-editable"
			tag = "input"
			elementType = "text"
			role = "textbox"
			name = "Delayed editable"
			placeholder = ""
			readOnly = true
		}
		if query == "Delayed readonly" {
			selector = "textarea#delayed-readonly"
			tag = "textarea"
			elementType = ""
			role = "textbox"
			name = "Delayed readonly"
			placeholder = ""
		}
		if query == "Delayed text" {
			selector = "button#delayed-text"
			tag = "button"
			elementType = "button"
			role = "button"
			name = "Delayed text"
			placeholder = ""
		}
		if query == "Delayed value" {
			selector = "input#delayed-value"
			tag = "input"
			elementType = "text"
			role = "textbox"
			name = "Delayed value"
			placeholder = ""
		}
		if query == "Checkout" {
			selector = "button#checkout"
			tag = "button"
			elementType = "button"
			role = "button"
			name = "Checkout"
			placeholder = ""
		}
		if query == "Delayed role" {
			selector = "button#delayed-role"
			tag = "button"
			elementType = "button"
			role = "status"
			name = "Delayed role"
			placeholder = ""
			if fakeDelayedAssertRoleAttempts.Add(1) >= 3 {
				role = "button"
			}
		}
		if query == "Delayed name" {
			selector = "button#delayed-name"
			tag = "button"
			elementType = "button"
			role = "button"
			name = "Pending name"
			placeholder = ""
			if fakeDelayedAssertNameAttempts.Add(1) >= 3 {
				name = "Ready name"
			}
		}
		if query == "Partial selection" {
			selector = "input#partial-selection"
			tag = "input"
			elementType = "checkbox"
			role = "checkbox"
			name = "Partial selection"
			placeholder = ""
		}
		if query == "Cart item" {
			count := 1
			if fakeDelayedAssertCountAttempts.Add(1) >= 3 {
				count = 3
			}
			matches := make([]map[string]any, 0, count)
			for index := 0; index < count; index++ {
				matches = append(matches, map[string]any{
					"index":              index,
					"selector_hint":      fmt.Sprintf("li#cart-item-%d", index+1),
					"selector_ambiguous": false,
					"tag":                "li",
					"type":               "",
					"role":               "listitem",
					"name":               "Cart item",
					"text":               "Cart item",
					"visible":            true,
					"disabled":           false,
					"read_only":          false,
					"content_editable":   false,
					"rect":               map[string]any{"x": 10, "y": 20 + index*30, "width": 300, "height": 24},
				})
			}
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"url":            "https://example.test/app",
						"title":          "Example App",
						"by":             by,
						"query":          query,
						"role":           roleQuery,
						"exact":          false,
						"include_hidden": includeHidden,
						"test_id_attr":   "data-testid",
						"count":          count,
						"returned":       count,
						"strict":         count == 1,
						"matches":        matches,
					},
				},
			}
		}
		if query == "Gone" {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"url":            "https://example.test/app",
						"title":          "Example App",
						"by":             by,
						"query":          query,
						"role":           roleQuery,
						"exact":          false,
						"include_hidden": includeHidden,
						"test_id_attr":   "data-testid",
						"count":          0,
						"returned":       0,
						"strict":         false,
						"matches":        []map[string]any{},
					},
				},
			}
		}
		if !selectorAmbiguous {
			selectorHint = selector
			resolvedNodeSelector = selector
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":            "https://example.test/app",
					"title":          "Example App",
					"by":             by,
					"query":          query,
					"role":           roleQuery,
					"exact":          exact,
					"include_hidden": includeHidden,
					"test_id_attr":   "data-testid",
					"count":          1,
					"returned":       1,
					"strict":         true,
					"matches": []map[string]any{{
						"index":                  0,
						"selector_hint":          selectorHint,
						"selector_ambiguous":     selectorAmbiguous,
						"resolved_node_selector": resolvedNodeSelector,
						"tag":                    tag,
						"type":                   elementType,
						"role":                   role,
						"name":                   name,
						"text":                   "",
						"placeholder":            placeholder,
						"visible":                true,
						"disabled":               disabled,
						"read_only":              readOnly,
						"content_editable":       contentEditable,
						"rect":                   map[string]any{"x": 10, "y": 20, "width": 300, "height": 40},
					}},
				},
			},
		}
	}
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
		if serpBlocked {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"url":                  "https://www.google.com/sorry/index?continue=https://www.google.com/search%3Fq%3Dserp-block-fixture",
						"document_ready_state": "complete",
						"selector_matched":     true,
						"selector_match_count": 1,
						"selected_text_length": 180,
						"selected_html_length": 256,
						"selected_word_count":  24,
						"body_text_length":     180,
						"body_html_length":     256,
						"dom_signature":        "blocked",
					},
				},
			}
		}
		domSignature := "ready"
		pageURL := "https://www.google.com/search?q=agentic+engineering+2026+evolutions&safe=active&tbs=qdr:m"
		if navigated, ok := navigatedURLs.Load(sessionID); ok && strings.TrimSpace(fmt.Sprint(navigated)) != "" {
			pageURL = fmt.Sprint(navigated)
		}
		if finalURL := fakeAnyTargetString(targetInfos, "fakeRenderedExtractFinalURL"); finalURL != "" {
			pageURL = finalURL
		}
		changesAfterReady := fakeAnyTargetBool(targetInfos, "fakeRenderedExtractChangesAfterReady")
		consistencyUnavailable := fakeAnyTargetBool(targetInfos, "fakeRenderedExtractConsistencyUnavailable")
		if changesAfterReady || consistencyUnavailable {
			counterValue, _ := renderedExtractReadinessCalls.LoadOrStore(sessionID, &atomic.Int64{})
			readinessCall := counterValue.(*atomic.Int64).Add(1)
			if consistencyUnavailable && readinessCall > 1 {
				return map[string]any{
					"result":           map[string]any{"type": "undefined"},
					"exceptionDetails": map[string]any{"text": "synthetic post-capture readiness exception"},
				}
			}
			if changesAfterReady && readinessCall > 1 {
				domSignature = "changed-after-ready"
			}
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":                  pageURL,
					"document_ready_state": "complete",
					"selector_matched":     true,
					"selector_match_count": 1,
					"selected_text_length": 96,
					"selected_html_length": 256,
					"selected_word_count":  12,
					"body_text_length":     96,
					"body_html_length":     256,
					"dom_signature":        domSignature,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_rendered_extract_links__") {
		if serpBlocked {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"source_url": "https://www.google.com/sorry/index?continue=https://www.google.com/search%3Fq%3Dserp-block-fixture",
						"serp":       "google",
						"count":      0,
						"results":    []map[string]any{},
					},
				},
			}
		}
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
	if strings.Contains(req.Expression, "__cdp_cli_page_assertion__") {
		attempts := fakeDelayedAssertPageAttempts.Add(1)
		pageURL := "https://example.test/loading"
		title := "Loading"
		if attempts >= 3 {
			pageURL = "https://example.test/ready"
			title = "Ready Page"
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":   pageURL,
					"title": title,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_assert_count__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		count := 1
		if selector == ".cart-item" && fakeDelayedAssertCountAttempts.Add(1) >= 3 {
			count = 3
		}
		items := make([]map[string]any, 0, count)
		for index := 0; index < count; index++ {
			items = append(items, map[string]any{
				"index": index,
				"tag":   "li",
				"id":    fmt.Sprintf("cart-item-%d", index+1),
				"role":  "listitem",
				"name":  "Cart item",
			})
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": selector,
					"count":    count,
					"items":    items,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_assert_attribute__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		attributeName := expressionStringArg(req.Expression, "const attributeName = ")
		value := "pending"
		if selector == "button#checkout" && attributeName == "data-state" && fakeDelayedAssertAttributeAttempts.Add(1) >= 3 {
			value = "ready"
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":               "https://example.test/app",
					"title":             "Example App",
					"selector":          selector,
					"attribute":         attributeName,
					"attribute_present": true,
					"value":             value,
					"count":             1,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_assert_class__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		className := expressionStringArg(req.Expression, "const className = ")
		classList := []string{"fixture"}
		tag := "button"
		id := "checkout"
		role := "button"
		name := "Checkout"
		count := 1
		if selector == "#missing" {
			classList = nil
			tag = ""
			id = ""
			role = ""
			name = ""
			count = 0
		}
		if selector == "button#checkout" {
			classList = []string{"primary", "checkout"}
		}
		hasClass := false
		for _, item := range classList {
			if item == className {
				hasClass = true
				break
			}
		}
		matchingCount := 0
		if hasClass && count > 0 {
			matchingCount = 1
		}
		items := []map[string]any{}
		if count > 0 {
			items = append(items, map[string]any{
				"index":      0,
				"tag":        tag,
				"id":         id,
				"role":       role,
				"name":       name,
				"class_list": classList,
				"has_class":  hasClass,
				"visible":    true,
				"rect":       map[string]any{"x": 10, "y": 20, "width": 300, "height": 40},
			})
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":            "https://example.test/app",
					"title":          "Example App",
					"selector":       selector,
					"class_name":     className,
					"expected":       className,
					"has_class":      hasClass,
					"passed":         hasClass,
					"count":          count,
					"matching_count": matchingCount,
					"failing_count":  count - matchingCount,
					"items":          items,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_assert_focused__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "input#q"
		}
		focused := selector == "input#q" && fakeDelayedAssertFocusedAttempts.Add(1) >= 3
		focusedCount := 0
		if focused {
			focusedCount = 1
		}
		activeSelector := "body"
		activeTag := "body"
		activeID := ""
		activeRole := ""
		activeName := "Example App"
		if focused {
			activeSelector = selector
			activeTag = "input"
			activeID = "q"
			activeRole = "searchbox"
			activeName = "Search"
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":             "https://example.test/app",
					"title":           "Example App",
					"selector":        selector,
					"expected":        "focused",
					"focused":         focused,
					"passed":          focused,
					"count":           1,
					"focused_count":   focusedCount,
					"active_selector": activeSelector,
					"active_tag":      activeTag,
					"active_id":       activeID,
					"active_role":     activeRole,
					"active_name":     activeName,
					"items": []map[string]any{{
						"index":   0,
						"tag":     "input",
						"id":      "q",
						"role":    "searchbox",
						"name":    "Search",
						"focused": focused,
						"visible": true,
						"rect":    map[string]any{"x": 10, "y": 20, "width": 300, "height": 40},
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_assert_css__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		propertyName := expressionStringArg(req.Expression, "const propertyName = ")
		if selector == "" {
			selector = "button#checkout"
		}
		if propertyName == "" {
			propertyName = "background-color"
		}
		value := "rgb(100, 100, 100)"
		if selector == "button#checkout" && propertyName == "background-color" && fakeDelayedAssertCSSAttempts.Add(1) >= 3 {
			value = "rgb(20, 92, 160)"
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": selector,
					"property": propertyName,
					"value":    value,
					"actual":   value,
					"count":    1,
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
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "textarea"
		}
		control := map[string]any{
			"selector_hint": "textarea[aria-label=\"Base64 output\"]",
			"tag":           "textarea",
			"role":          "textbox",
			"name":          "Base64 output",
			"value":         "SGVsbG8gVVg=",
			"read_only":     true,
			"disabled":      false,
		}
		if selector == "input#q" {
			control = map[string]any{
				"selector_hint": "input#q",
				"tag":           "input",
				"type":          "search",
				"role":          "searchbox",
				"name":          "Search",
				"value":         "hello",
				"read_only":     false,
				"disabled":      false,
			}
		}
		if selector == "input#delayed-value" {
			value := "pending"
			if fakeDelayedAssertValueAttempts.Add(1) >= 3 {
				value = "ready"
			}
			control = map[string]any{
				"selector_hint": "input#delayed-value",
				"tag":           "input",
				"type":          "text",
				"role":          "textbox",
				"name":          "Delayed value",
				"value":         value,
				"read_only":     false,
				"disabled":      false,
			}
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": selector,
					"count":    1,
					"controls": []map[string]any{},
					"control":  control,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_assert_visible__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "main"
		}
		tag := "main"
		role := ""
		name := "Synthetic main text"
		visible := true
		hidden := false
		display := "block"
		visibility := "visible"
		rect := map[string]any{"x": 0, "y": 0, "width": 600, "height": 200}
		if selector == "button#submit" {
			tag = "button"
			role = "button"
			name = "Search"
			rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
		}
		if selector == "#hidden-button" {
			tag = "button"
			role = "button"
			name = "Hidden button"
			visible = false
			hidden = true
			display = "none"
			rect = map[string]any{"x": 0, "y": 0, "width": 0, "height": 0}
		}
		if selector == "button#delayed-visible" {
			tag = "button"
			role = "button"
			name = "Delayed visible"
			visible = fakeDelayedAssertVisibleAttempts.Add(1) >= 3
			hidden = !visible
			if visible {
				display = "block"
				rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
			} else {
				display = "none"
				rect = map[string]any{"x": 0, "y": 0, "width": 0, "height": 0}
			}
		}
		if selector == "#delayed-hidden" {
			tag = "button"
			role = "button"
			name = "Delayed hidden"
			hiddenNow := fakeDelayedAssertHiddenAttempts.Add(1) >= 3
			visible = !hiddenNow
			hidden = hiddenNow
			if hiddenNow {
				display = "none"
				rect = map[string]any{"x": 0, "y": 0, "width": 0, "height": 0}
			} else {
				display = "block"
				rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
			}
		}
		count := 1
		visibleCount := 1
		hiddenCount := 0
		if !visible {
			visibleCount = 0
			hiddenCount = 1
		}
		if selector == "#missing" {
			count = 0
			visibleCount = 0
			hiddenCount = 0
		}
		items := []map[string]any{}
		if count > 0 {
			items = append(items, map[string]any{
				"index":      0,
				"tag":        tag,
				"id":         strings.TrimPrefix(selector, "#"),
				"role":       role,
				"name":       name,
				"visible":    visible,
				"display":    display,
				"visibility": visibility,
				"hidden":     hidden,
				"rect":       rect,
			})
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":           "https://example.test/app",
					"title":         "Example App",
					"selector":      selector,
					"expected":      "visible",
					"visible":       visibleCount > 0,
					"hidden":        visibleCount == 0,
					"passed":        visibleCount > 0,
					"count":         count,
					"visible_count": visibleCount,
					"hidden_count":  hiddenCount,
					"items":         items,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_assert_viewport__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "main"
		}
		tag := "main"
		role := ""
		name := "Synthetic main text"
		rect := map[string]any{"x": 0, "y": 0, "width": 600, "height": 200}
		inViewport := true
		fullyInViewport := true
		if selector == "button#submit" {
			tag = "button"
			role = "button"
			name = "Search"
			rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
		}
		if selector == "button#delayed-viewport" {
			tag = "button"
			role = "button"
			name = "Delayed viewport"
			inViewport = fakeDelayedAssertViewportAttempts.Add(1) >= 3
			fullyInViewport = inViewport
			if inViewport {
				rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
			} else {
				rect = map[string]any{"x": 10, "y": 1200, "width": 300, "height": 40}
			}
		}
		if selector == "#below-fold" {
			tag = "button"
			role = "button"
			name = "Below fold"
			inViewport = false
			fullyInViewport = false
			rect = map[string]any{"x": 10, "y": 1200, "width": 300, "height": 40}
		}
		count := 1
		inViewportCount := 1
		if !inViewport {
			inViewportCount = 0
		}
		if selector == "#missing" {
			count = 0
			inViewportCount = 0
			fullyInViewport = false
		}
		outOfViewportCount := count - inViewportCount
		items := []map[string]any{}
		if count > 0 {
			items = append(items, map[string]any{
				"index":             0,
				"tag":               tag,
				"id":                strings.TrimPrefix(selector, "#"),
				"role":              role,
				"name":              name,
				"visible":           true,
				"in_viewport":       inViewport,
				"fully_in_viewport": fullyInViewport,
				"rect":              rect,
			})
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":                   "https://example.test/app",
					"title":                 "Example App",
					"selector":              selector,
					"expected":              "in-viewport",
					"in_viewport":           inViewportCount > 0,
					"fully_in_viewport":     fullyInViewport,
					"passed":                inViewportCount > 0,
					"count":                 count,
					"in_viewport_count":     inViewportCount,
					"out_of_viewport_count": outOfViewportCount,
					"items":                 items,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_assert_enabled__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "button#submit"
		}
		tag := "button"
		role := "button"
		name := "Search"
		enabled := true
		disabled := false
		disabledReason := []string{}
		if selector == "button#disabled-action" || selector == "#disabled-button" {
			name = "Disabled target"
			enabled = false
			disabled = true
			disabledReason = []string{"native_disabled"}
		}
		if selector == "button#delayed-enabled" {
			name = "Delayed enabled"
			enabled = fakeDelayedAssertEnabledAttempts.Add(1) >= 3
			disabled = !enabled
			if disabled {
				disabledReason = []string{"native_disabled"}
			}
		}
		if selector == "button#delayed-disabled" {
			name = "Delayed disabled"
			disabled = fakeDelayedAssertDisabledAttempts.Add(1) >= 3
			enabled = !disabled
			if disabled {
				disabledReason = []string{"native_disabled"}
			}
		}
		count := 1
		enabledCount := 1
		disabledCount := 0
		if disabled {
			enabledCount = 0
			disabledCount = 1
		}
		if selector == "#missing" {
			count = 0
			enabledCount = 0
			disabledCount = 0
		}
		items := []map[string]any{}
		if count > 0 {
			items = append(items, map[string]any{
				"index":             0,
				"tag":               tag,
				"id":                strings.TrimPrefix(selector, "#"),
				"role":              role,
				"name":              name,
				"enabled":           enabled,
				"disabled":          disabled,
				"disabled_reason":   disabledReason,
				"native_disabled":   disabled,
				"fieldset_disabled": false,
				"aria_disabled":     false,
				"read_only":         false,
				"content_editable":  false,
				"visible":           true,
				"rect":              map[string]any{"x": 10, "y": 20, "width": 300, "height": 40},
			})
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":            "https://example.test/app",
					"title":          "Example App",
					"selector":       selector,
					"expected":       "enabled",
					"enabled":        enabledCount > 0,
					"disabled":       count > 0 && enabledCount == 0,
					"passed":         enabledCount > 0,
					"count":          count,
					"enabled_count":  enabledCount,
					"disabled_count": disabledCount,
					"items":          items,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_assert_editable__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "input#q"
		}
		tag := "input"
		elementType := "search"
		role := "searchbox"
		name := "Search"
		editable := true
		readOnly := false
		readOnlyReason := []string{}
		disabled := false
		supportsEditable := true
		nativeReadOnly := false
		if selector == "textarea#readonly-notes" || selector == "#readonly-notes" {
			tag = "textarea"
			elementType = ""
			role = "textbox"
			name = "Read-only notes"
			editable = false
			readOnly = true
			readOnlyReason = []string{"native_readonly"}
			nativeReadOnly = true
		}
		if selector == "input#delayed-editable" {
			name = "Delayed editable"
			editable = fakeDelayedAssertEditableAttempts.Add(1) >= 3
			readOnly = !editable
			if readOnly {
				readOnlyReason = []string{"native_readonly"}
				nativeReadOnly = true
			}
		}
		if selector == "textarea#delayed-readonly" {
			tag = "textarea"
			elementType = ""
			role = "textbox"
			name = "Delayed readonly"
			readOnly = fakeDelayedAssertReadonlyAttempts.Add(1) >= 3
			editable = !readOnly
			if readOnly {
				readOnlyReason = []string{"native_readonly"}
				nativeReadOnly = true
			}
		}
		if selector == "button#submit" {
			tag = "button"
			elementType = "submit"
			role = "button"
			name = "Search"
			editable = false
			supportsEditable = false
		}
		count := 1
		editableCount := 1
		readOnlyCount := 0
		disabledCount := 0
		unsupportedCount := 0
		if !editable {
			editableCount = 0
		}
		if readOnly {
			readOnlyCount = 1
		}
		if disabled {
			disabledCount = 1
		}
		if !supportsEditable {
			unsupportedCount = 1
		}
		if selector == "#missing" {
			count = 0
			editableCount = 0
			readOnlyCount = 0
			disabledCount = 0
			unsupportedCount = 0
		}
		items := []map[string]any{}
		if count > 0 {
			items = append(items, map[string]any{
				"index":                  0,
				"tag":                    tag,
				"id":                     strings.TrimPrefix(selector, "#"),
				"type":                   elementType,
				"role":                   role,
				"name":                   name,
				"editable":               editable,
				"read_only":              readOnly,
				"read_only_reason":       readOnlyReason,
				"supports_editable":      supportsEditable,
				"supports_aria_readonly": role == "textbox" || role == "searchbox",
				"native_read_only":       nativeReadOnly,
				"aria_read_only":         false,
				"enabled":                !disabled,
				"disabled":               disabled,
				"disabled_reason":        []string{},
				"content_editable":       false,
				"visible":                true,
				"rect":                   map[string]any{"x": 10, "y": 20, "width": 300, "height": 40},
			})
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":               "https://example.test/app",
					"title":             "Example App",
					"selector":          selector,
					"expected":          "editable",
					"editable":          editableCount > 0,
					"read_only":         count > 0 && editableCount == 0 && readOnlyCount > 0,
					"passed":            editableCount > 0,
					"count":             count,
					"editable_count":    editableCount,
					"read_only_count":   readOnlyCount,
					"disabled_count":    disabledCount,
					"unsupported_count": unsupportedCount,
					"items":             items,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_text__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "main"
		}
		tag := "main"
		text := "Synthetic main text"
		switch selector {
		case "body":
			tag = "body"
		case "button#submit":
			tag = "button"
			text = "Search button"
		case "button#delayed-text":
			tag = "button"
			text = "Pending text"
			if fakeDelayedAssertTextAttempts.Add(1) >= 3 {
				text = "Ready text"
			}
		case "input#q":
			tag = "input"
			text = ""
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": selector,
					"count":    1,
					"text":     text,
					"items": []map[string]any{{
						"index":       0,
						"tag":         tag,
						"text":        text,
						"text_length": len(text),
						"rect":        map[string]any{"x": 0, "y": 0, "width": 600, "height": 200},
					}},
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_actionability__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		action := expressionStringArg(req.Expression, "const action = ")
		if selector == "" {
			selector = "main"
		}
		if action == "" {
			action = "click"
		}
		tag := "main"
		elementType := ""
		role := ""
		name := "Synthetic main text"
		visible := true
		stable := true
		receivesEvents := true
		enabled := true
		editable := false
		readOnly := false
		supportsEditing := false
		inViewport := true
		rect := map[string]any{"x": 10, "y": 20, "width": 600, "height": 200}
		point := map[string]any{"x": 310, "y": 120, "hit_tag": tag, "hit_id": "", "hit_role": role, "target_matches": true}
		if selector == "button#submit" {
			tag = "button"
			elementType = "submit"
			role = "button"
			name = "Search"
			rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
			point = map[string]any{"x": 160, "y": 40, "hit_tag": "button", "hit_id": "submit", "hit_role": "button", "target_matches": true}
		}
		if selector == "main > div:nth-of-type(2) > div:nth-of-type(2)" {
			tag = "div"
			role = "menuitem"
			name = "Delete Chat"
			rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
			point = map[string]any{"x": 160, "y": 40, "hit_tag": "div", "hit_id": "", "hit_role": "menuitem", "target_matches": true}
		}
		if selector == "main > div:nth-of-type(3) > div:nth-of-type(2)" {
			tag = "div"
			role = "menuitem"
			name = "Drifting menuitem"
			if fakeSemanticDriftActionabilityAttempts.Add(1) >= 2 {
				name = "Sibling menuitem"
			}
			rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
			point = map[string]any{"x": 160, "y": 40, "hit_tag": "div", "hit_id": "", "hit_role": "menuitem", "target_matches": true}
		}
		if selector == "main > div:nth-of-type(4) > div:nth-of-type(2)" {
			tag = "div"
			role = "menuitem"
			name = "Replacing menuitem"
			rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
			point = map[string]any{"x": 160, "y": 40, "hit_tag": "div", "hit_id": "", "hit_role": "menuitem", "target_matches": true}
		}
		if selector == "button#covered" || selector == "#covered-button" {
			tag = "button"
			elementType = "button"
			role = "button"
			name = "Covered target"
			receivesEvents = false
			rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
			point = map[string]any{"x": 160, "y": 40, "hit_tag": "div", "hit_id": "overlay", "hit_role": "", "target_matches": false}
		}
		if selector == "div#drag-target" || selector == "#drag-target" {
			tag = "div"
			name = "Drag target"
			rect = map[string]any{"x": 20, "y": 40, "width": 140, "height": 60}
			point = map[string]any{"x": 90, "y": 70, "hit_tag": "div", "hit_id": "drag-target", "hit_role": "", "target_matches": true}
		}
		if selector == "div#scroll-target" || selector == "#scroll-target" {
			tag = "div"
			name = "Scroll target"
			inViewport = false
			receivesEvents = false
			rect = map[string]any{"x": 20, "y": 1800, "width": 180, "height": 80}
			point = map[string]any{"x": 110, "y": 1840, "hit_tag": "", "hit_id": "", "hit_role": "", "target_matches": false}
			if _, ok := scrolledSelectors.Load(selector); ok {
				inViewport = true
				receivesEvents = true
				rect = map[string]any{"x": 20, "y": 260, "width": 180, "height": 80}
				point = map[string]any{"x": 110, "y": 300, "hit_tag": "div", "hit_id": "scroll-target", "hit_role": "", "target_matches": true}
			}
		}
		if selector == "#moving-target" {
			tag = "div"
			name = "Moving target"
			stable = false
			rect = map[string]any{"x": 20, "y": 1800, "width": 180, "height": 80}
			point = map[string]any{"x": 110, "y": 1840, "hit_tag": "div", "hit_id": "moving-target", "hit_role": "", "target_matches": true}
		}
		if selector == "select#plan" || selector == "select#disabled-plan" || selector == "select#hidden-plan" {
			tag = "select"
			role = "combobox"
			name = "Plan"
			rect = map[string]any{"x": 10, "y": 120, "width": 300, "height": 40}
			point = map[string]any{"x": 160, "y": 140, "hit_tag": "select", "hit_id": strings.TrimPrefix(selector, "select#"), "hit_role": "combobox", "target_matches": true}
		}
		if selector == "select#disabled-plan" {
			enabled = false
		}
		if selector == "select#hidden-plan" {
			visible = false
			inViewport = false
			receivesEvents = false
			rect = map[string]any{"x": 0, "y": 0, "width": 0, "height": 0}
			point = map[string]any{"x": 0, "y": 0, "hit_tag": "", "hit_id": "", "hit_role": "", "target_matches": false}
		}
		if selector == "input#upload" || selector == "#hidden-upload" {
			tag = "input"
			elementType = "file"
			role = "textbox"
			name = "Upload file"
			rect = map[string]any{"x": 10, "y": 120, "width": 300, "height": 40}
			point = map[string]any{"x": 160, "y": 140, "hit_tag": "input", "hit_id": strings.TrimPrefix(selector, "input#"), "hit_role": "textbox", "target_matches": true}
		}
		if selector == "#hidden-upload" {
			visible = false
			inViewport = false
			receivesEvents = false
			rect = map[string]any{"x": 0, "y": 0, "width": 0, "height": 0}
			point = map[string]any{"x": 0, "y": 0, "hit_tag": "", "hit_id": "", "hit_role": "", "target_matches": false}
		}
		if selector == "input#subscribe" || selector == "input#disabled-checkbox" || selector == "input#covered-checkbox" {
			tag = "input"
			elementType = "checkbox"
			role = "checkbox"
			name = "Subscribe to newsletter"
			rect = map[string]any{"x": 10, "y": 170, "width": 20, "height": 20}
			point = map[string]any{"x": 20, "y": 180, "hit_tag": "input", "hit_id": strings.TrimPrefix(selector, "input#"), "hit_role": "checkbox", "target_matches": true}
		}
		if selector == "input#disabled-checkbox" {
			enabled = false
		}
		if selector == "input#covered-checkbox" {
			receivesEvents = false
			point = map[string]any{"x": 20, "y": 180, "hit_tag": "label", "hit_id": "checkbox-cover", "hit_role": "", "target_matches": false}
		}
		if selector == "input#below-fold-checkbox" {
			tag = "input"
			elementType = "checkbox"
			role = "checkbox"
			name = "Below fold checkbox"
			inViewport = false
			receivesEvents = false
			rect = map[string]any{"x": 10, "y": 1800, "width": 20, "height": 20}
			point = map[string]any{"x": 20, "y": 1810, "hit_tag": "", "hit_id": "", "hit_role": "", "target_matches": false}
			if _, ok := scrolledSelectors.Load(selector); ok {
				inViewport = true
				receivesEvents = true
				rect = map[string]any{"x": 10, "y": 260, "width": 20, "height": 20}
				point = map[string]any{"x": 20, "y": 270, "hit_tag": "input", "hit_id": "below-fold-checkbox", "hit_role": "checkbox", "target_matches": true}
			}
		}
		if selector == "input#q" {
			tag = "input"
			elementType = "search"
			role = "searchbox"
			name = "Search"
			editable = true
			supportsEditing = true
			rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
			point = map[string]any{"x": 160, "y": 40, "hit_tag": "input", "hit_id": "q", "hit_role": "searchbox", "target_matches": true}
		}
		if selector == "[contenteditable=true]" {
			tag = "div"
			role = "textbox"
			name = "Rich editor"
			editable = true
			supportsEditing = true
			rect = map[string]any{"x": 10, "y": 20, "width": 300, "height": 40}
			point = map[string]any{"x": 160, "y": 40, "hit_tag": "div", "hit_id": "", "hit_role": "textbox", "target_matches": true}
		}
		if selector == "input#hidden-field" || selector == "#hidden-input" {
			tag = "input"
			elementType = "text"
			role = "textbox"
			name = "Hidden field"
			visible = false
			inViewport = false
			receivesEvents = false
			editable = true
			supportsEditing = true
			rect = map[string]any{"x": 0, "y": 0, "width": 0, "height": 0}
			point = map[string]any{"x": 0, "y": 0, "hit_tag": "", "hit_id": "", "hit_role": "", "target_matches": false}
		}
		if selector == "textarea#readonly-notes" {
			tag = "textarea"
			role = "textbox"
			name = "Read-only notes"
			editable = false
			readOnly = true
			supportsEditing = true
			rect = map[string]any{"x": 10, "y": 70, "width": 300, "height": 80}
			point = map[string]any{"x": 160, "y": 110, "hit_tag": "textarea", "hit_id": "readonly-notes", "hit_role": "textbox", "target_matches": true}
		}
		if selector == "#hidden-button" {
			tag = "button"
			role = "button"
			name = "Hidden button"
			visible = false
			inViewport = false
			receivesEvents = false
			rect = map[string]any{"x": 0, "y": 0, "width": 0, "height": 0}
			point = map[string]any{"x": 0, "y": 0, "hit_tag": "", "hit_id": "", "hit_role": "", "target_matches": false}
		}
		if selector == "button#disabled-action" || selector == "#disabled-button" {
			tag = "button"
			elementType = "button"
			role = "button"
			name = "Disabled target"
			enabled = false
		}
		count := 1
		if selector == "#missing" {
			count = 0
			visible = false
			inViewport = false
			stable = false
			receivesEvents = false
			enabled = false
			editable = false
			supportsEditing = false
		}
		required := []string{"attached", "visible", "stable", "receives_events", "enabled"}
		switch action {
		case "press":
			required = []string{"attached"}
		case "file":
			required = []string{"attached"}
		case "scroll":
			required = []string{"attached", "stable"}
		case "fill", "type":
			required = []string{"attached", "visible", "enabled", "editable"}
		case "select":
			required = []string{"attached", "visible", "enabled"}
		case "hover", "drag":
			required = []string{"attached", "visible", "stable", "receives_events"}
		}
		requiredSet := map[string]bool{}
		for _, checkName := range required {
			requiredSet[checkName] = true
		}
		check := func(name string, passed bool, message string) map[string]any {
			required := requiredSet[name]
			out := map[string]any{"required": required, "passed": passed}
			if !required {
				out["skipped"] = true
			}
			if message != "" {
				out["message"] = message
			}
			return out
		}
		checks := map[string]any{
			"attached":        check("attached", count > 0, map[bool]string{true: "", false: "selector matched no elements"}[count > 0]),
			"visible":         check("visible", visible, map[bool]string{true: "", false: "element has empty box or hidden state"}[visible]),
			"stable":          check("stable", stable, map[bool]string{true: "", false: "bounding box changed across animation frames"}[stable]),
			"receives_events": check("receives_events", receivesEvents, map[bool]string{true: "", false: "center point is not the hit target"}[receivesEvents]),
			"enabled":         check("enabled", enabled, map[bool]string{true: "", false: "element is disabled"}[enabled]),
			"editable":        check("editable", editable, map[bool]string{true: "", false: "element is disabled, read-only, or does not support editing"}[editable]),
			"in_viewport":     map[string]any{"required": false, "passed": inViewport, "skipped": true},
		}
		passedByName := map[string]bool{
			"attached":        count > 0,
			"visible":         visible,
			"stable":          stable,
			"receives_events": receivesEvents,
			"enabled":         enabled,
			"editable":        editable,
		}
		actionable := true
		for _, checkName := range required {
			actionable = actionable && passedByName[checkName]
		}
		targetElementID := ""
		if strings.HasPrefix(selector, "#") {
			targetElementID = strings.TrimPrefix(selector, "#")
		} else if strings.HasPrefix(selector, tag+"#") {
			targetElementID = strings.TrimPrefix(selector, tag+"#")
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":             "https://example.test/app",
					"title":           "Example App",
					"selector":        selector,
					"action":          action,
					"trial":           false,
					"count":           count,
					"actionable":      actionable,
					"required_checks": required,
					"checks":          checks,
					"target": map[string]any{
						"tag":              tag,
						"id":               targetElementID,
						"type":             elementType,
						"role":             role,
						"name":             name,
						"enabled":          enabled,
						"disabled":         !enabled,
						"editable":         editable,
						"read_only":        readOnly,
						"supports_editing": supportsEditing,
						"content_editable": false,
					},
					"rect":  rect,
					"point": point,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_hover__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "main"
		}
		count := 1
		x := 310.0
		y := 120.0
		if selector == "button#submit" || selector == "button#covered" {
			x = 160
			y = 40
		}
		if selector == "div#drag-target" || selector == "#drag-target" {
			x = 90
			y = 70
		}
		if selector == "#missing" {
			count = 0
		}
		value := map[string]any{
			"url":      "https://example.test/app",
			"title":    "Example App",
			"selector": selector,
			"count":    count,
			"hovered":  count > 0,
			"x":        x,
			"y":        y,
		}
		if count == 0 {
			value["error"] = map[string]any{"name": "NotFoundError", "message": "selector matched no elements"}
		}
		return map[string]any{"result": map[string]any{"type": "object", "value": value}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_drag__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "main"
		}
		dx := expressionIntArg(req.Expression, "const deltaX = ")
		dy := expressionIntArg(req.Expression, "const deltaY = ")
		count := 1
		startX := 310.0
		startY := 120.0
		if selector == "button#submit" || selector == "button#covered" {
			startX = 160
			startY = 40
		}
		if selector == "div#drag-target" || selector == "#drag-target" {
			startX = 90
			startY = 70
		}
		if selector == "#missing" {
			count = 0
		}
		value := map[string]any{
			"url":      "https://example.test/app",
			"title":    "Example App",
			"selector": selector,
			"count":    count,
			"dragged":  count > 0,
			"delta_x":  dx,
			"delta_y":  dy,
			"start_x":  startX,
			"start_y":  startY,
			"end_x":    startX + float64(dx),
			"end_y":    startY + float64(dy),
		}
		if count == 0 {
			value["error"] = map[string]any{"name": "NotFoundError", "message": "selector matched no elements"}
		}
		return map[string]any{"result": map[string]any{"type": "object", "value": value}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_file_input__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		fileName := expressionStringArg(req.Expression, "const fileName = ")
		if selector == "" {
			selector = "input#upload"
		}
		if fileName == "" {
			fileName = "upload.txt"
		}
		count := 1
		tag := "input"
		elementType := "file"
		accepted := true
		if selector == "#missing" {
			count = 0
			accepted = false
		}
		if selector == "button#submit" {
			tag = "button"
			elementType = "submit"
			accepted = false
		}
		value := map[string]any{
			"url":             "https://example.test/app",
			"title":           "Example App",
			"selector":        selector,
			"count":           count,
			"accepted":        accepted,
			"file_set":        false,
			"tag":             tag,
			"type":            elementType,
			"file_name":       fileName,
			"content_omitted": true,
		}
		if count == 0 {
			value["error"] = map[string]any{"name": "NotFoundError", "message": "selector matched no elements"}
		} else if !accepted {
			value["error"] = map[string]any{"name": "InvalidTargetError", "message": "target element is not input[type=file]"}
		}
		return map[string]any{"result": map[string]any{"type": "object", "value": value}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_select__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		value := expressionStringArg(req.Expression, "const requestedValue = String(")
		if selector == "" {
			selector = "select#plan"
		}
		if value == "" {
			value = "pro"
		}
		count := 1
		selected := true
		resultValue := value
		if selector == "#missing" {
			count = 0
			selected = false
			resultValue = ""
		}
		out := map[string]any{
			"url":             "https://example.test/app",
			"title":           "Example App",
			"selector":        selector,
			"count":           count,
			"selected":        selected,
			"previous":        "free",
			"requested_value": value,
			"value":           resultValue,
			"selected_values": []string{resultValue},
			"matched_by":      "value",
		}
		if count == 0 {
			out["error"] = map[string]any{"name": "NotFoundError", "message": "selector matched no elements"}
		}
		return map[string]any{"result": map[string]any{"type": "object", "value": out}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_check__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		desired := expressionBoolArg(req.Expression, "const desiredChecked = ")
		mutate := expressionBoolArg(req.Expression, "const mutate = ")
		if selector == "" {
			selector = "input#subscribe"
		}
		count := 1
		previous := !desired
		checked := previous
		if mutate {
			checked = desired
		}
		out := map[string]any{
			"url":              "https://example.test/app",
			"title":            "Example App",
			"selector":         selector,
			"count":            count,
			"checked":          checked,
			"desired_checked":  desired,
			"previous_checked": previous,
			"changed":          checked != previous,
			"already":          previous == desired,
			"tag":              "input",
			"type":             "checkbox",
			"role":             "checkbox",
			"name":             "Subscribe to newsletter",
		}
		if selector == "input#below-fold-checkbox" {
			out["name"] = "Below fold checkbox"
		}
		if !mutate {
			out["checked"] = previous
			out["changed"] = false
		}
		if selector == "#missing" {
			out["count"] = 0
			out["checked"] = false
			out["previous_checked"] = false
			out["changed"] = false
			out["error"] = map[string]any{"name": "NotFoundError", "message": "selector matched no elements"}
		}
		return map[string]any{"result": map[string]any{"type": "object", "value": out}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_assert_checked__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "input#subscribe"
		}
		checked := true
		indeterminate := false
		name := "Subscribe to newsletter"
		if selector == "input#optional-updates" {
			checked = false
			name = "Optional updates"
		}
		if selector == "input#partial-selection" {
			checked = false
			indeterminate = true
			name = "Partial selection"
		}
		if selector == "input#delayed-check" {
			checked = fakeDelayedAssertCheckedAttempts.Add(1) >= 3
			name = "Delayed checkbox"
		}
		count := 1
		items := []map[string]any{{
			"index":            0,
			"tag":              "input",
			"id":               strings.TrimPrefix(selector, "input#"),
			"type":             "checkbox",
			"role":             "checkbox",
			"name":             name,
			"checked":          checked,
			"indeterminate":    indeterminate,
			"supports_checked": true,
			"visible":          true,
			"rect":             map[string]any{"x": 10, "y": 170, "width": 20, "height": 20},
		}}
		checkedCount := 0
		uncheckedCount := 1
		indeterminateCount := 0
		if indeterminate {
			indeterminateCount = 1
			uncheckedCount = 0
		}
		if checked {
			checkedCount = 1
			uncheckedCount = 0
			indeterminateCount = 0
		}
		if selector == "#missing" {
			count = 0
			checkedCount = 0
			uncheckedCount = 0
			indeterminateCount = 0
			items = nil
		}
		out := map[string]any{
			"url":                 "https://example.test/app",
			"title":               "Example App",
			"selector":            selector,
			"expected":            "checked",
			"checked":             checkedCount > 0,
			"unchecked":           count > 0 && checkedCount == 0 && indeterminateCount == 0,
			"indeterminate":       indeterminateCount > 0,
			"passed":              checkedCount > 0,
			"count":               count,
			"checked_count":       checkedCount,
			"unchecked_count":     uncheckedCount,
			"indeterminate_count": indeterminateCount,
			"unsupported_count":   0,
			"items":               items,
		}
		return map[string]any{"result": map[string]any{"type": "object", "value": out}}
	}
	if strings.Contains(req.Expression, "__cdp_cli_click_point__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "main"
		}
		if selector == "zero" {
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"url":      "https://example.test/app",
						"title":    "Example App",
						"selector": selector,
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
					"selector": selector,
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
		selector := expressionStringArg(req.Expression, "const selector = ")
		if selector == "" {
			selector = "main"
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": selector,
					"count":    1,
					"clicked":  true,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_fill__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		value := expressionStringArg(req.Expression, "const value = String(")
		if selector == "" {
			selector = "input"
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": selector,
					"count":    1,
					"filled":   true,
					"previous": "before",
					"value":    value,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_type__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		text := expressionStringArg(req.Expression, "const text = String(")
		strategy := expressionStringArg(req.Expression, "const strategy = ")
		if selector == "" {
			selector = "input#q"
		}
		kind := "input"
		chosenStrategy := "dom"
		value := "before" + text
		if selector == "[contenteditable=true]" {
			kind = "contenteditable"
			chosenStrategy = "insert-text"
			value = "before"
		}
		if strategy == "insert-text" {
			chosenStrategy = "insert-text"
			if selector != "[contenteditable=true]" {
				value = "before"
			}
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": selector,
					"count":    1,
					"typed":    text,
					"previous": "before",
					"value":    value,
					"kind":     kind,
					"strategy": chosenStrategy,
					"typing":   true,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_press__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		key := expressionStringArg(req.Expression, "const key = String(")
		count := 1
		if selector == "#missing" {
			count = 0
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":        "https://example.test/app",
					"title":      "Example App",
					"selector":   selector,
					"key":        key,
					"count":      count,
					"dispatched": count > 0 || selector == "",
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
	if strings.Contains(req.Expression, "__cdp_cli_observe__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"url":      "https://example.test/app",
					"title":    "Example App",
					"selector": "button",
					"count":    1,
					"interactive": []map[string]any{{
						"ref":      "obs:0",
						"index":    0,
						"tag":      "button",
						"role":     "button",
						"name":     "Save changes",
						"selector": "button#save",
						"text":     "Save changes",
						"disabled": false,
						"visible":  true,
						"rect":     map[string]any{"x": 10, "y": 20, "width": 100, "height": 32},
					}},
					"warnings": []string{},
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
	if strings.Contains(req.Expression, "__cdp_cli_scroll__") {
		selector := expressionStringArg(req.Expression, "const selector = ")
		block := expressionStringArg(req.Expression, "const block = ")
		inline := expressionStringArg(req.Expression, "const inline = ")
		mutate := strings.Contains(req.Expression, "const mutate = true")
		if selector == "" {
			selector = "div#scroll-target"
		}
		if block == "" {
			block = "center"
		}
		if inline == "" {
			inline = "nearest"
		}
		before := map[string]any{
			"rect":              map[string]any{"x": 20, "y": 1800, "width": 180, "height": 80},
			"in_viewport":       false,
			"fully_in_viewport": false,
			"viewport_width":    800,
			"viewport_height":   600,
			"scroll_x":          0,
			"scroll_y":          0,
		}
		after := before
		changed := false
		scrolled := false
		if mutate {
			scrolledSelectors.Store(selector, true)
			after = map[string]any{
				"rect":              map[string]any{"x": 20, "y": 260, "width": 180, "height": 80},
				"in_viewport":       true,
				"fully_in_viewport": true,
				"viewport_width":    800,
				"viewport_height":   600,
				"scroll_x":          0,
				"scroll_y":          1540,
			}
			changed = true
			scrolled = true
		}
		count := 1
		value := map[string]any{
			"url":      "https://example.test/app",
			"title":    "Example App",
			"selector": selector,
			"count":    count,
			"scrolled": scrolled,
			"changed":  changed,
			"trial":    !mutate,
			"block":    block,
			"inline":   inline,
			"before":   before,
			"after":    after,
		}
		if selector == "#missing" {
			empty := map[string]any{
				"rect":              map[string]any{"x": 0, "y": 0, "width": 0, "height": 0},
				"in_viewport":       false,
				"fully_in_viewport": false,
				"viewport_width":    800,
				"viewport_height":   600,
				"scroll_x":          0,
				"scroll_y":          0,
			}
			value["count"] = 0
			value["scrolled"] = false
			value["changed"] = false
			value["before"] = empty
			value["after"] = empty
			value["error"] = map[string]any{"name": "NotFoundError", "message": "selector matched no elements"}
		}
		return map[string]any{"result": map[string]any{"type": "object", "value": value}}
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
		selector := expressionStringArg(req.Expression, "const selector = ")
		matched := selector == "main"
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"kind":     "selector",
					"selector": selector,
					"matched":  matched,
					"count":    boolCount(matched),
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_wait_url__") {
		needle := expressionStringArg(req.Expression, "const needle = ")
		condition := expressionStringArg(req.Expression, "const condition = ")
		url := "https://example.test/app"
		if needle != "" && !strings.Contains(needle, "Never Ready") {
			if condition == "contains" {
				url = "https://example.test/app?matched=" + needle
			} else {
				url = needle
			}
		}
		matched := false
		if condition == "contains" {
			matched = strings.Contains(url, needle)
		} else {
			matched = url == needle
		}
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"kind":      "url",
					"needle":    needle,
					"condition": condition,
					"url":       url,
					"title":     "Example App",
					"matched":   matched,
					"count":     boolCount(matched),
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
		expression := expressionStringArg(req.Expression, "const expression = ")
		readyExpr := expressionStringArg(req.Expression, "const readyExpression = ")
		readyField := expressionStringArg(req.Expression, "const readyField = ")
		if expression == "window.__semanticState" || expression == "window.__semanticNeverReady" {
			attempt := fakeDelayedWaitEvalAttempts.Add(1)
			terminalCondition := "loading"
			rowCount := 0
			ready := false
			if expression == "window.__semanticState" && attempt >= 3 {
				terminalCondition = "fare_rows"
				rowCount = 12
				ready = true
			}
			value := map[string]any{
				"terminalCondition": terminalCondition,
				"rowCount":          rowCount,
				"attempt":           attempt,
			}
			return map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"kind":             "eval",
						"expression":       expression,
						"ready_expression": readyExpr,
						"ready_field":      readyField,
						"ready":            ready,
						"matched":          ready,
						"value":            value,
					},
				},
			}
		}
		matched := expression == "window.__rendered === true"
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"kind":       "eval",
					"expression": expression,
					"ready":      matched,
					"matched":    matched,
					"value":      matched,
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_wait_load_state__") {
		state := expressionStringArg(req.Expression, "const state = ")
		readyState := "complete"
		if strings.Contains(sessionID, "loading") {
			readyState = "loading"
		}
		matched := readyState == "complete" || state == "domcontentloaded" && readyState == "interactive"
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"kind":        "load-state",
					"state":       state,
					"ready_state": readyState,
					"matched":     matched,
					"url":         "https://example.test/app",
					"title":       "Example App",
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_headless_health_check__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"ok":   true,
					"text": "cdp-headless-health",
				},
			},
		}
	}
	if strings.Contains(req.Expression, "__cdp_cli_browser_preflight__") {
		return map[string]any{
			"result": map[string]any{
				"type": "object",
				"value": map[string]any{
					"marker":           "__cdp_cli_browser_preflight__",
					"ready_state":      "complete",
					"title":            "Preflight",
					"url":              "data:text/html,preflight",
					"body_text_length": 21,
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
	if strings.Contains(req.Expression, "document.body ? (document.body.innerText || document.body.textContent || '')") {
		return map[string]any{
			"result": map[string]any{
				"type":  "string",
				"value": "Synthetic app content",
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

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
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

func expressionIntArg(expression, prefix string) int {
	idx := strings.Index(expression, prefix)
	if idx < 0 {
		return 0
	}
	start := idx + len(prefix)
	end := strings.Index(expression[start:], ";")
	if end < 0 {
		return 0
	}
	value, _ := strconv.Atoi(strings.TrimSpace(expression[start : start+end]))
	return value
}

func expressionBoolArg(expression, prefix string) bool {
	idx := strings.Index(expression, prefix)
	if idx < 0 {
		return false
	}
	start := idx + len(prefix)
	end := strings.Index(expression[start:], ";")
	if end < 0 {
		return false
	}
	return strings.TrimSpace(expression[start:start+end]) == "true"
}
