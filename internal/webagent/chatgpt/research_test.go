package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const syntheticResearchReport = `# Synthetic research report

This synthetic report stands in for a completed embedded research document. It
contains enough ordinary prose to prove that the reader accepts a terminal
report root, writes exactly that text to an explicit destination, and does not
place the report body in the operation envelope or its metadata.`

const syntheticDeepResearchDetailPayload = `{
  "conversation_id":"conversation-1",
  "async_status":4,
  "current_node":"control",
  "mapping":{
    "control":{
      "parent":"startup",
      "message":{
        "author":{"role":"assistant"},
        "status":"finished_successfully",
        "end_turn":true,
        "content":{"content_type":"text","parts":["{\"path\":\"/Deep Research App/synthetic\",\"session_id\":\"private-test-session\",\"connector_settings\":{}}"]}
      }
    },
    "startup":{
      "parent":"prompt",
      "message":{
        "author":{"role":"assistant"},
        "status":"finished_successfully",
        "end_turn":true,
        "content":{"content_type":"text","parts":["I will research this topic."]}
      }
    },
    "prompt":{
      "parent":"",
      "message":{
        "author":{"role":"user"},
        "content":{"content_type":"text","parts":["Research this synthetic topic."]}
      }
    }
  }
}`

type researchTestBrowser struct {
	*authenticatedReadBrowser
	frameURL                string
	treeFrameURL            string
	report                  string
	frameTreeCalls          int
	isolatedWorldCalls      int
	oopifTargetID           string
	oopifRootFrames         map[string]string
	oopifChildFrames        map[string]researchFrameTargetRoot
	frameOwnerNodes         map[string]int64
	semanticOwnerNode       int64
	semanticOwnerIDs        []int64
	semanticOwnerReads      int
	surfaceSources          []string
	oopifRootURLs           map[string][]string
	oopifRootReads          map[string]int
	reportReadCalls         int
	targetOrder             []string
	detachFailures          map[string]bool
	parentTreeOnly          bool
	semanticFrameID         string
	isolatedFrameIDs        []string
	isolatedWorldFailures   map[string]error
	frameDocumentTrusted    bool
	observationDocTrusted   bool
	documentTrustProbeCalls int
}

func newResearchTestBrowser(report string) *researchTestBrowser {
	return newResearchTestBrowserWithFrameURLs(
		report,
		"https://chatgpt.com/deep-research/synthetic-report",
		"https://chatgpt.com/deep-research/synthetic-report",
	)
}

func newResearchTestBrowserWithFrameURLs(
	report string,
	frameURL string,
	treeFrameURL string,
) *researchTestBrowser {
	base := newAuthenticatedReadBrowser(nil)
	browser := &researchTestBrowser{
		authenticatedReadBrowser: base,
		frameURL:                 frameURL,
		treeFrameURL:             treeFrameURL,
		report:                   report,
		semanticOwnerNode:        101,
		frameDocumentTrusted:     true,
		observationDocTrusted:    true,
		frameOwnerNodes: map[string]int64{
			"research-frame": 101,
		},
	}
	base.Evaluate = func(
		expression string,
		_ *testsupport.Browser,
	) (any, error) {
		if strings.Contains(expression, "frame_sources") {
			sources := browser.surfaceSources
			if sources == nil {
				sources = []string{frameURL}
			}
			return map[string]any{
				"route_matches":   true,
				"conversation_id": "conversation-1",
				"frame_sources":   sources,
			}, nil
		}
		return map[string]any{}, nil
	}
	return browser
}

func newResearchTestBrowserWithOOPIF(
	report string,
	frameURL string,
	targetURL string,
) *researchTestBrowser {
	browser := newResearchTestBrowserWithFrameURLs(report, frameURL, targetURL)
	browser.oopifTargetID = "research-oopif"
	browser.addOOPIF(
		browser.oopifTargetID,
		targetURL,
		"oopif-root-frame",
		browser.semanticOwnerNode,
	)
	return browser
}

func (b *researchTestBrowser) addOOPIF(
	targetID string,
	targetURL string,
	rootFrameID string,
	ownerNodeID int64,
) {
	if b.oopifRootFrames == nil {
		b.oopifRootFrames = map[string]string{}
	}
	if b.oopifRootURLs == nil {
		b.oopifRootURLs = map[string][]string{}
	}
	if b.oopifRootReads == nil {
		b.oopifRootReads = map[string]int{}
	}
	if b.frameOwnerNodes == nil {
		b.frameOwnerNodes = map[string]int64{}
	}
	b.Browser.Targets[targetID] = cdp.TargetInfo{
		TargetID: targetID,
		Type:     "iframe",
		URL:      targetURL,
	}
	b.oopifRootFrames[targetID] = rootFrameID
	b.oopifRootURLs[targetID] = []string{targetURL}
	b.frameOwnerNodes[rootFrameID] = ownerNodeID
}

func (b *researchTestBrowser) addOOPIFChild(
	targetID string,
	frameID string,
	frameURL string,
) {
	if b.oopifChildFrames == nil {
		b.oopifChildFrames = map[string]researchFrameTargetRoot{}
	}
	b.oopifChildFrames[targetID] = researchFrameTargetRoot{
		ID:  frameID,
		URL: frameURL,
	}
}

func (b *researchTestBrowser) oopifRootURL(targetID string) string {
	urls := b.oopifRootURLs[targetID]
	if len(urls) == 0 {
		return b.treeFrameURL
	}
	index := b.oopifRootReads[targetID]
	if index >= len(urls) {
		index = len(urls) - 1
	}
	b.oopifRootReads[targetID]++
	return urls[index]
}

func (b *researchTestBrowser) Call(
	ctx context.Context,
	method string,
	params any,
	result any,
) error {
	if err := b.authenticatedReadBrowser.Call(ctx, method, params, result); err != nil {
		return err
	}
	if method == "Target.detachFromTarget" &&
		b.detachFailures[researchStringParam(params, "sessionId")] {
		return errors.New("synthetic detach failure")
	}
	if method != "Target.getTargets" || len(b.targetOrder) == 0 {
		return nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	var targets struct {
		TargetInfos []cdp.TargetInfo `json:"targetInfos"`
	}
	if err := json.Unmarshal(encoded, &targets); err != nil {
		return err
	}
	priority := make(map[string]int, len(b.targetOrder))
	for index, targetID := range b.targetOrder {
		priority[targetID] = index
	}
	sort.SliceStable(targets.TargetInfos, func(left int, right int) bool {
		leftPriority, leftKnown := priority[targets.TargetInfos[left].TargetID]
		rightPriority, rightKnown := priority[targets.TargetInfos[right].TargetID]
		switch {
		case leftKnown && rightKnown:
			return leftPriority < rightPriority
		case leftKnown:
			return true
		case rightKnown:
			return false
		default:
			return false
		}
	})
	return assignResearchTestResult(result, targets)
}

func (b *researchTestBrowser) CallSession(
	ctx context.Context,
	sessionID string,
	method string,
	params any,
	result any,
) error {
	switch method {
	case "Page.getFrameTree":
		b.frameTreeCalls++
		if len(b.oopifRootFrames) > 0 {
			targetID := strings.TrimPrefix(sessionID, "session-")
			if rootFrameID, ok := b.oopifRootFrames[targetID]; ok {
				frameTree := map[string]any{
					"frame": map[string]any{
						"id":  rootFrameID,
						"url": b.oopifRootURL(targetID),
					},
				}
				if child, ok := b.oopifChildFrames[targetID]; ok {
					frameTree["childFrames"] = []any{map[string]any{
						"frame": map[string]any{
							"id":  child.ID,
							"url": child.URL,
						},
					}}
				}
				return assignResearchTestResult(result, map[string]any{
					"frameTree": frameTree,
				})
			}
			return assignResearchTestResult(result, map[string]any{
				"frameTree": map[string]any{
					"frame": map[string]any{
						"id":  "root-frame",
						"url": "https://chatgpt.com/c/conversation-1",
					},
				},
			})
		}
		if b.parentTreeOnly {
			return assignResearchTestResult(result, map[string]any{
				"frameTree": map[string]any{
					"frame": map[string]any{
						"id":  "root-frame",
						"url": "https://chatgpt.com/c/conversation-1",
					},
				},
			})
		}
		return assignResearchTestResult(result, map[string]any{
			"frameTree": map[string]any{
				"frame": map[string]any{
					"id":  "root-frame",
					"url": "https://chatgpt.com/c/conversation-1",
				},
				"childFrames": []any{map[string]any{
					"frame": map[string]any{
						"id":  "research-frame",
						"url": b.treeFrameURL,
					},
				}},
			},
		})
	case "Page.createIsolatedWorld":
		b.isolatedWorldCalls++
		frameID := researchStringParam(params, "frameId")
		b.isolatedFrameIDs = append(b.isolatedFrameIDs, frameID)
		if err := b.isolatedWorldFailures[frameID]; err != nil {
			return err
		}
		return assignResearchTestResult(result, map[string]any{
			"executionContextId": 77,
		})
	case "Runtime.evaluate":
		if researchContextID(params) == 77 &&
			strings.Contains(researchExpression(params), "return current.protocol ===") {
			b.documentTrustProbeCalls++
			return assignResearchTestResult(result, map[string]any{
				"result": map[string]any{
					"type":  "boolean",
					"value": b.frameDocumentTrusted,
				},
			})
		}
		if researchContextID(params) == 77 &&
			(b.oopifTargetID == "" || sessionID == "session-"+b.oopifTargetID) {
			if !b.observationDocTrusted {
				return assignResearchTestResult(result, map[string]any{
					"result": map[string]any{
						"type": "object",
						"value": map[string]any{
							"text":             "",
							"root_count":       0,
							"streaming":        false,
							"too_large":        false,
							"document_trusted": false,
						},
					},
				})
			}
			b.reportReadCalls++
			return assignResearchTestResult(result, map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"text":             b.report,
						"root_count":       1,
						"streaming":        false,
						"too_large":        false,
						"document_trusted": true,
					},
				},
			})
		}
		if len(b.frameOwnerNodes) > 0 &&
			sessionID == "session-owned-1" &&
			strings.Contains(researchExpression(params), "const source =") {
			return assignResearchTestResult(result, map[string]any{
				"result": map[string]any{
					"type":     "object",
					"subtype":  "node",
					"objectId": "synthetic-research-iframe",
				},
			})
		}
	case "DOM.describeNode":
		if len(b.frameOwnerNodes) > 0 && sessionID == "session-owned-1" {
			ownerNode := b.semanticOwnerNode
			if len(b.semanticOwnerIDs) > 0 {
				index := b.semanticOwnerReads
				if index >= len(b.semanticOwnerIDs) {
					index = len(b.semanticOwnerIDs) - 1
				}
				ownerNode = b.semanticOwnerIDs[index]
				b.semanticOwnerReads++
			}
			return assignResearchTestResult(result, map[string]any{
				"node": map[string]any{
					"backendNodeId": ownerNode,
					"nodeName":      "IFRAME",
					"frameId":       b.semanticFrameID,
				},
			})
		}
	case "DOM.getFrameOwner":
		if len(b.frameOwnerNodes) > 0 && sessionID == "session-owned-1" {
			return assignResearchTestResult(result, map[string]any{
				"backendNodeId": b.frameOwnerNodes[researchStringParam(params, "frameId")],
			})
		}
	case "Runtime.releaseObject":
		if len(b.frameOwnerNodes) > 0 && sessionID == "session-owned-1" {
			return assignResearchTestResult(result, map[string]any{})
		}
	}
	return b.authenticatedReadBrowser.CallSession(
		ctx,
		sessionID,
		method,
		params,
		result,
	)
}

func researchExpression(params any) string {
	return researchStringParam(params, "expression")
}

func researchStringParam(params any, key string) string {
	var decoded map[string]any
	switch value := params.(type) {
	case map[string]any:
		decoded = value
	case json.RawMessage:
		if json.Unmarshal(value, &decoded) != nil {
			return ""
		}
	case []byte:
		if json.Unmarshal(value, &decoded) != nil {
			return ""
		}
	default:
		return ""
	}
	value, _ := decoded[key].(string)
	return value
}

func researchContextID(params any) int {
	var decoded map[string]any
	switch value := params.(type) {
	case map[string]any:
		decoded = value
	case json.RawMessage:
		if json.Unmarshal(value, &decoded) != nil {
			return 0
		}
	case []byte:
		if json.Unmarshal(value, &decoded) != nil {
			return 0
		}
	default:
		return 0
	}
	switch value := decoded["contextId"].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

func assignResearchTestResult(target any, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func newResearchTestRuntime(
	t *testing.T,
	browser *researchTestBrowser,
) (*browserflow.Engine, browserflow.Journal) {
	t.Helper()
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), browser)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return engine, journal
}

func TestExportResearchReadsOneEmbeddedFrameAndKeepsTextOutOfEnvelope(
	t *testing.T,
) {
	browser := newResearchTestBrowser(syntheticResearchReport)
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if !result.OK || result.State != webagent.StateTerminal ||
		result.Operation != webagent.OperationResearchExport ||
		result.Cleanup.State != webagent.CleanupClosed ||
		result.Evidence.Target == nil || !result.Evidence.Target.Closed {
		t.Fatalf("result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
	data, ok := result.Data.(ResearchExportData)
	if !ok || data.OutputPath != outputPath || data.ExportedBytes == 0 ||
		data.SHA256 == "" || data.ReadMode != "rendered_embedded_research_frame" ||
		data.Metadata["source"] != "rendered_embedded_research_frame" {
		t.Fatalf("data = %+v", data)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read exported report: %v", err)
	}
	if string(written) != syntheticResearchReport+"\n" {
		t.Fatalf("exported report = %q", string(written))
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode=%v err=%v, want 600", info.Mode(), err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(encoded), syntheticResearchReport) ||
		strings.Contains(string(encoded), browser.frameURL) {
		t.Fatalf("operation envelope leaked report or frame URL: %s", encoded)
	}
	counts, _, _, _, _, _, targets := browser.Snapshot()
	if counts["Target.createTarget"] != 1 ||
		counts["Target.closeTarget"] != 1 ||
		counts["Input.dispatchMouseEvent"] != 0 ||
		counts["Input.dispatchKeyEvent"] != 0 {
		t.Fatalf("browser counts = %+v", counts)
	}
	if _, exists := targets["user-page"]; !exists {
		t.Fatalf("user target was not preserved: %+v", targets)
	}
}

func TestExportResearchRejectsControlCharacterConversationID(t *testing.T) {
	for _, conversationID := range []string{
		"conversation-1\x00",
		strings.Repeat("a", 257),
		strings.Repeat("a", 2048),
	} {
		result := ExportResearch(
			context.Background(),
			ResearchExportConfig{},
			conversationID,
		)
		if result.OK || result.Error == nil ||
			result.Error.Code != "chatgpt_invalid_conversation_id" ||
			result.Conversation != nil {
			t.Fatalf("conversation id length=%d result=%+v", len(conversationID), result)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("conversation id length=%d result validation: %v", len(conversationID), err)
		}
	}
}

func TestTrustedResearchFrameURLAllowsKnownDeepResearchRenderer(t *testing.T) {
	if !trustedResearchFrameURL(
		"https://connector-openai-deep-research.web-sandbox.oaiusercontent.com/rendered-report",
	) {
		t.Fatal("known Deep Research renderer origin was rejected")
	}
	if trustedResearchFrameURL("https://untrusted.example/rendered-report") {
		t.Fatal("untrusted renderer origin was accepted")
	}
}

func TestExportResearchReadsOOPIFThroughDaemonTarget(t *testing.T) {
	frameURL := "https://chatgpt.com/deep-research/synthetic-report?embed=initial"
	targetURL := "https://chatgpt.com/deep-research/synthetic-report?loaded=final"
	browser := newResearchTestBrowserWithOOPIF(
		syntheticResearchReport,
		frameURL,
		targetURL,
	)
	browser.addOOPIF(
		"foreign-same-url-oopif",
		targetURL,
		"foreign-root-frame",
		browser.semanticOwnerNode+1,
	)
	browser.targetOrder = []string{
		"foreign-same-url-oopif",
		browser.oopifTargetID,
	}
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "oopif-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if !result.OK || result.State != webagent.StateTerminal ||
		result.Cleanup.State != webagent.CleanupClosed ||
		browser.frameTreeCalls < 6 || browser.isolatedWorldCalls != 1 ||
		browser.reportReadCalls != 1 {
		t.Fatalf("result=%+v frame_tree=%d isolated_world=%d", result, browser.frameTreeCalls, browser.isolatedWorldCalls)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil || string(written) != syntheticResearchReport+"\n" {
		t.Fatalf("read OOPIF export=%q err=%v", string(written), err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(encoded), syntheticResearchReport) ||
		strings.Contains(string(encoded), frameURL) ||
		strings.Contains(string(encoded), targetURL) {
		t.Fatalf("operation envelope leaked report or frame URL: %s", encoded)
	}
	counts, _, _, _, _, _, targets := browser.Snapshot()
	if counts["Target.attachToTarget"] != 3 ||
		counts["Target.detachFromTarget"] != 3 ||
		counts["Target.closeTarget"] != 1 {
		t.Fatalf("browser counts = %+v", counts)
	}
	if _, exists := targets["research-oopif"]; !exists {
		t.Fatalf("OOPIF target was closed instead of detached: %+v", targets)
	}
	if _, exists := targets["user-page"]; !exists {
		t.Fatalf("user target was not preserved: %+v", targets)
	}
}

func TestExportResearchReadsRedirectedDirectFrameAfterOwnerBinding(t *testing.T) {
	browser := newResearchTestBrowserWithFrameURLs(
		syntheticResearchReport,
		"https://chatgpt.com/deep-research/initial?embed=initial",
		"https://chatgpt.com/research-runtime/completed?redirected=final",
	)
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "redirected-child-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if !result.OK || result.State != webagent.StateTerminal ||
		result.Cleanup.State != webagent.CleanupClosed ||
		browser.frameTreeCalls < 4 || browser.isolatedWorldCalls != 1 ||
		browser.reportReadCalls != 1 {
		t.Fatalf("result=%+v frame_tree=%d isolated_world=%d", result, browser.frameTreeCalls, browser.isolatedWorldCalls)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil || string(written) != syntheticResearchReport+"\n" {
		t.Fatalf("read redirected child export=%q err=%v", string(written), err)
	}
	counts, _, _, _, _, _, _ := browser.Snapshot()
	if counts["Target.attachToTarget"] != 1 ||
		counts["Target.detachFromTarget"] != 1 {
		t.Fatalf("browser counts = %+v", counts)
	}
}

func TestExportResearchReadsSemanticFrameIDWithoutFrameTreeOrTarget(t *testing.T) {
	rendererURL := "https://connector-openai-deep-research.web-sandbox.oaiusercontent.com/rendered-report?synthetic=1"
	browser := newResearchTestBrowserWithFrameURLs(
		syntheticResearchReport,
		rendererURL,
		rendererURL,
	)
	browser.parentTreeOnly = true
	browser.semanticFrameID = "semantic-renderer-frame"
	browser.frameOwnerNodes[browser.semanticFrameID] = browser.semanticOwnerNode
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "semantic-frame-id-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if !result.OK || result.State != webagent.StateTerminal ||
		browser.frameTreeCalls != 1 || browser.isolatedWorldCalls != 1 ||
		browser.reportReadCalls != 1 ||
		len(browser.isolatedFrameIDs) != 1 ||
		browser.isolatedFrameIDs[0] != browser.semanticFrameID {
		t.Fatalf("result=%+v frame_tree=%d isolated_world=%d report_reads=%d frame_ids=%v", result, browser.frameTreeCalls, browser.isolatedWorldCalls, browser.reportReadCalls, browser.isolatedFrameIDs)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil || string(written) != syntheticResearchReport+"\n" {
		t.Fatalf("read semantic frame-id export=%q err=%v", string(written), err)
	}
	counts, _, _, _, _, _, _ := browser.Snapshot()
	if counts["Target.attachToTarget"] != 1 ||
		counts["Target.detachFromTarget"] != 1 {
		t.Fatalf("fallback attached an unrelated target: %+v", counts)
	}
	encoded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), rendererURL) ||
		strings.Contains(string(encoded), syntheticResearchReport) {
		t.Fatalf("fallback envelope leaked renderer data: %s", encoded)
	}
}

func TestExportResearchFallsBackFromUnreadableParentSemanticFrameToOwnedTarget(t *testing.T) {
	rendererURL := "https://connector-openai-deep-research.web-sandbox.oaiusercontent.com/rendered-report?synthetic=1"
	browser := newResearchTestBrowserWithFrameURLs(
		syntheticResearchReport,
		rendererURL,
		rendererURL,
	)
	browser.parentTreeOnly = true
	browser.semanticFrameID = "semantic-renderer-frame"
	browser.frameOwnerNodes[browser.semanticFrameID] = browser.semanticOwnerNode
	browser.isolatedWorldFailures = map[string]error{
		browser.semanticFrameID: errors.New("synthetic parent OOPIF boundary"),
	}
	browser.addOOPIF(
		"owned-research-oopif",
		rendererURL,
		"auto-attached-root-frame",
		browser.semanticOwnerNode,
	)
	browser.addOOPIFChild(
		"owned-research-oopif",
		"nested-report-frame",
		rendererURL,
	)
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "owned-oopif-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if !result.OK || result.State != webagent.StateTerminal ||
		browser.isolatedWorldCalls != 2 || browser.reportReadCalls != 1 {
		t.Fatalf("result=%+v isolated_world=%d report_reads=%d", result, browser.isolatedWorldCalls, browser.reportReadCalls)
	}
	if len(browser.isolatedFrameIDs) != 2 ||
		browser.isolatedFrameIDs[1] != "nested-report-frame" {
		t.Fatalf("isolated frame ids = %v, want nested report frame", browser.isolatedFrameIDs)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil || string(written) != syntheticResearchReport+"\n" {
		t.Fatalf("read owned OOPIF export=%q err=%v", string(written), err)
	}
	counts, _, _, _, _, _, _ := browser.Snapshot()
	if counts["Target.attachToTarget"] != 2 ||
		counts["Target.detachFromTarget"] != 2 {
		t.Fatalf("browser counts = %+v", counts)
	}
}

func TestExportResearchRejectsForeignDocumentInSemanticFrameIDFallback(t *testing.T) {
	rendererURL := "https://connector-openai-deep-research.web-sandbox.oaiusercontent.com/rendered-report?synthetic=1"
	browser := newResearchTestBrowserWithFrameURLs(
		syntheticResearchReport,
		rendererURL,
		rendererURL,
	)
	browser.parentTreeOnly = true
	browser.semanticFrameID = "semantic-renderer-frame"
	browser.frameOwnerNodes[browser.semanticFrameID] = browser.semanticOwnerNode
	browser.frameDocumentTrusted = false
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "foreign-semantic-frame-id-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if result.OK || result.Error == nil ||
		result.Error.Code != "chatgpt_research_frame_unavailable" ||
		browser.isolatedWorldCalls != 1 || browser.documentTrustProbeCalls != 1 ||
		browser.reportReadCalls != 0 {
		t.Fatalf("result=%+v isolated_world=%d trust_probes=%d report_reads=%d", result, browser.isolatedWorldCalls, browser.documentTrustProbeCalls, browser.reportReadCalls)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("foreign semantic frame wrote output: %v", err)
	}
}

func TestExportResearchRejectsNavigationBetweenTrustProbeAndReportRead(t *testing.T) {
	rendererURL := "https://connector-openai-deep-research.web-sandbox.oaiusercontent.com/rendered-report?synthetic=1"
	browser := newResearchTestBrowserWithFrameURLs(
		syntheticResearchReport,
		rendererURL,
		rendererURL,
	)
	browser.parentTreeOnly = true
	browser.semanticFrameID = "semantic-renderer-frame"
	browser.frameOwnerNodes[browser.semanticFrameID] = browser.semanticOwnerNode
	browser.observationDocTrusted = false
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "navigated-semantic-frame-id-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if result.OK || result.Error == nil ||
		result.Error.Code != "chatgpt_research_frame_unavailable" ||
		browser.isolatedWorldCalls != 1 || browser.documentTrustProbeCalls != 1 ||
		browser.reportReadCalls != 0 {
		t.Fatalf("result=%+v isolated_world=%d trust_probes=%d report_reads=%d", result, browser.isolatedWorldCalls, browser.documentTrustProbeCalls, browser.reportReadCalls)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("navigated semantic frame wrote output: %v", err)
	}
}

func TestExportResearchRejectsSemanticFrameIDOwnerMismatch(t *testing.T) {
	rendererURL := "https://connector-openai-deep-research.web-sandbox.oaiusercontent.com/rendered-report?synthetic=1"
	browser := newResearchTestBrowserWithFrameURLs(
		syntheticResearchReport,
		rendererURL,
		rendererURL,
	)
	browser.parentTreeOnly = true
	browser.semanticFrameID = "semantic-renderer-frame"
	browser.frameOwnerNodes[browser.semanticFrameID] = browser.semanticOwnerNode + 1
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "mismatched-semantic-frame-id-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    25 * time.Millisecond,
	}, "conversation-1")

	if result.OK || result.Error == nil ||
		result.Error.Code != "chatgpt_research_frame_unavailable" ||
		browser.isolatedWorldCalls != 0 || browser.documentTrustProbeCalls != 0 ||
		browser.reportReadCalls != 0 {
		t.Fatalf("result=%+v isolated_world=%d trust_probes=%d report_reads=%d", result, browser.isolatedWorldCalls, browser.documentTrustProbeCalls, browser.reportReadCalls)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("mismatched semantic frame wrote output: %v", err)
	}
}

func TestExportResearchRejectsDuplicateVisibleResearchIframes(t *testing.T) {
	browser := newResearchTestBrowser(syntheticResearchReport)
	browser.surfaceSources = []string{browser.frameURL, browser.frameURL}
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "duplicate-surface-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if result.OK || result.Error == nil ||
		result.Error.Code != "chatgpt_research_frame_ambiguous" ||
		browser.frameTreeCalls != 0 || browser.isolatedWorldCalls != 0 ||
		browser.reportReadCalls != 0 {
		t.Fatalf("result=%+v frame_tree=%d isolated_world=%d report_reads=%d", result, browser.frameTreeCalls, browser.isolatedWorldCalls, browser.reportReadCalls)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("duplicate semantic frames wrote output: %v", err)
	}
}

func TestExportResearchRejectsAllForeignOOPIFCandidates(t *testing.T) {
	frameURL := "https://chatgpt.com/deep-research/synthetic-report?embed=initial"
	targetURL := "https://chatgpt.com/deep-research/synthetic-report?loaded=final"
	browser := newResearchTestBrowserWithOOPIF(
		syntheticResearchReport,
		frameURL,
		targetURL,
	)
	browser.frameOwnerNodes["oopif-root-frame"] = browser.semanticOwnerNode + 1
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "foreign-oopif-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    25 * time.Millisecond,
	}, "conversation-1")

	if result.OK || result.Error == nil ||
		result.Error.Code != "chatgpt_research_frame_unavailable" ||
		browser.isolatedWorldCalls != 0 || browser.reportReadCalls != 0 {
		t.Fatalf("result=%+v isolated_world=%d report_reads=%d", result, browser.isolatedWorldCalls, browser.reportReadCalls)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("foreign OOPIF wrote output: %v", err)
	}
	counts, _, _, _, _, _, _ := browser.Snapshot()
	if counts["Target.attachToTarget"] < 2 ||
		counts["Target.attachToTarget"] != counts["Target.detachFromTarget"] {
		t.Fatalf("browser counts = %+v", counts)
	}
}

func TestExportResearchReleasesBoundOOPIFWhenLaterForeignDetachFails(t *testing.T) {
	frameURL := "https://chatgpt.com/deep-research/synthetic-report?embed=initial"
	targetURL := "https://chatgpt.com/deep-research/synthetic-report?loaded=final"
	browser := newResearchTestBrowserWithOOPIF(
		syntheticResearchReport,
		frameURL,
		targetURL,
	)
	browser.addOOPIF(
		"foreign-after-bound-oopif",
		targetURL,
		"foreign-after-bound-root-frame",
		browser.semanticOwnerNode+1,
	)
	browser.targetOrder = []string{
		browser.oopifTargetID,
		"foreign-after-bound-oopif",
	}
	browser.detachFailures = map[string]bool{
		"session-foreign-after-bound-oopif": true,
	}
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "detach-failure-oopif-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if result.OK || result.Error == nil ||
		result.Error.Code != "chatgpt_research_oopif_unreadable" ||
		browser.isolatedWorldCalls != 0 || browser.reportReadCalls != 0 {
		t.Fatalf("result=%+v isolated_world=%d report_reads=%d", result, browser.isolatedWorldCalls, browser.reportReadCalls)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("detach failure wrote output: %v", err)
	}
	counts, _, _, _, _, _, _ := browser.Snapshot()
	if counts["Target.detachFromTarget"] != 3 {
		t.Fatalf("bound OOPIF was not released after later detach failure: %+v", counts)
	}
}

func TestExportResearchRejectsOOPIFNavigationAfterBinding(t *testing.T) {
	frameURL := "https://chatgpt.com/deep-research/synthetic-report?embed=initial"
	targetURL := "https://chatgpt.com/deep-research/synthetic-report?loaded=final"
	browser := newResearchTestBrowserWithOOPIF(
		syntheticResearchReport,
		frameURL,
		targetURL,
	)
	browser.oopifRootURLs[browser.oopifTargetID] = []string{
		targetURL,
		targetURL,
		targetURL,
		"https://chatgpt.com/deep-research/synthetic-report?loaded=changed",
	}
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "navigated-oopif-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if result.OK || result.Error == nil ||
		result.Error.Code != "chatgpt_research_frame_unavailable" ||
		browser.isolatedWorldCalls != 1 || browser.reportReadCalls != 0 {
		t.Fatalf("result=%+v isolated_world=%d report_reads=%d", result, browser.isolatedWorldCalls, browser.reportReadCalls)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("navigated OOPIF wrote output: %v", err)
	}
}

func TestExportResearchRejectsAmbiguousOOPIFOwnerBinding(t *testing.T) {
	frameURL := "https://chatgpt.com/deep-research/synthetic-report?embed=initial"
	targetURL := "https://chatgpt.com/deep-research/synthetic-report?loaded=final"
	browser := newResearchTestBrowserWithOOPIF(
		syntheticResearchReport,
		frameURL,
		targetURL,
	)
	browser.addOOPIF(
		"duplicate-owned-oopif",
		targetURL,
		"duplicate-root-frame",
		browser.semanticOwnerNode,
	)
	browser.targetOrder = []string{
		browser.oopifTargetID,
		"duplicate-owned-oopif",
	}
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "ambiguous-oopif-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if result.OK || result.Error == nil ||
		result.Error.Code != "chatgpt_research_frame_ambiguous" ||
		result.Cleanup.State != webagent.CleanupClosed ||
		browser.frameTreeCalls < 5 || browser.isolatedWorldCalls != 0 ||
		browser.reportReadCalls != 0 {
		t.Fatalf("result=%+v frame_tree=%d isolated_world=%d", result, browser.frameTreeCalls, browser.isolatedWorldCalls)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("ambiguous OOPIF wrote output: %v", err)
	}
	counts, _, _, _, _, _, targets := browser.Snapshot()
	if counts["Target.attachToTarget"] != 3 ||
		counts["Target.detachFromTarget"] != 3 ||
		counts["Target.closeTarget"] != 1 {
		t.Fatalf("browser counts = %+v", counts)
	}
	if _, exists := targets["duplicate-owned-oopif"]; !exists {
		t.Fatalf("duplicate OOPIF target was closed instead of detached: %+v", targets)
	}
}

func TestExportResearchRejectsSemanticFrameReplacementBeforeRead(t *testing.T) {
	frameURL := "https://chatgpt.com/deep-research/synthetic-report?embed=initial"
	targetURL := "https://chatgpt.com/deep-research/synthetic-report?loaded=final"
	browser := newResearchTestBrowserWithOOPIF(
		syntheticResearchReport,
		frameURL,
		targetURL,
	)
	browser.semanticOwnerIDs = []int64{
		browser.semanticOwnerNode,
		browser.semanticOwnerNode + 1,
	}
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "replaced-oopif-research.txt")
	result := ExportResearch(context.Background(), ResearchExportConfig{
		BrowserConfig: BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		OutputPath: outputPath,
		Timeout:    time.Second,
	}, "conversation-1")

	if result.OK || result.Error == nil ||
		result.Error.Code != "chatgpt_research_frame_unavailable" ||
		browser.isolatedWorldCalls != 0 || browser.semanticOwnerReads != 2 {
		t.Fatalf("result=%+v isolated_world=%d owner_reads=%d", result, browser.isolatedWorldCalls, browser.semanticOwnerReads)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("replaced semantic frame wrote output: %v", err)
	}
	counts, _, _, _, _, _, _ := browser.Snapshot()
	if counts["Target.attachToTarget"] != 2 ||
		counts["Target.detachFromTarget"] != 2 {
		t.Fatalf("browser counts = %+v", counts)
	}
}

func TestDetailConversationReplacesDeepResearchControlWithEmbeddedReport(
	t *testing.T,
) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	browser := newResearchTestBrowser(syntheticResearchReport)
	engine, journal := newResearchTestRuntime(t, browser)
	result := DetailConversation(context.Background(), ReadConfig{
		Store:      newReadTestStore(t, now),
		HTTPClient: fixedHTTPClient(http.StatusOK, syntheticDeepResearchDetailPayload),
		BrowserConfig: &BrowserConfig{
			Client:  browser,
			Engine:  engine,
			Journal: journal,
		},
		Now: func() time.Time { return now },
	}, "conversation-1")

	data, ok := result.Data.(ConversationDetailData)
	if !ok || !result.OK || result.State != webagent.StateTerminal ||
		data.Text != syntheticResearchReport ||
		data.ReadMode != "observed_stable_http_plus_embedded_rendered" ||
		data.Metadata["embedded_research_control"] != true ||
		data.Metadata["answer_source"] != "rendered_embedded_research_frame" {
		t.Fatalf("result=%+v data=%+v", result, data)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	if strings.Contains(string(encoded), "private-test-session") ||
		strings.Contains(string(encoded), "/Deep Research App/") {
		t.Fatalf("detail leaked control payload: %s", encoded)
	}
	counts, _, _, _, _, _, targets := browser.Snapshot()
	if counts["Target.createTarget"] != 1 ||
		counts["Target.closeTarget"] != 1 {
		t.Fatalf("browser counts = %+v", counts)
	}
	if _, exists := targets["user-page"]; !exists {
		t.Fatalf("user target was not preserved: %+v", targets)
	}
}
