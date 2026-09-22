package chatgpt

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
	frameURL           string
	treeFrameURL       string
	report             string
	frameTreeCalls     int
	isolatedWorldCalls int
	oopifTargetID      string
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
	base := newAuthenticatedReadBrowser(func(
		expression string,
		_ *testsupport.Browser,
	) (any, error) {
		if strings.Contains(expression, "frame_sources") {
			return map[string]any{
				"route_matches":   true,
				"conversation_id": "conversation-1",
				"frame_sources":   []string{frameURL},
			}, nil
		}
		return map[string]any{}, nil
	})
	return &researchTestBrowser{
		authenticatedReadBrowser: base,
		frameURL:                 frameURL,
		treeFrameURL:             treeFrameURL,
		report:                   report,
	}
}

func newResearchTestBrowserWithOOPIF(
	report string,
	frameURL string,
	targetURL string,
) *researchTestBrowser {
	browser := newResearchTestBrowserWithFrameURLs(report, frameURL, targetURL)
	browser.oopifTargetID = "research-oopif"
	browser.Browser.Targets[browser.oopifTargetID] = cdp.TargetInfo{
		TargetID: browser.oopifTargetID,
		Type:     "iframe",
		URL:      targetURL,
	}
	return browser
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
		if b.oopifTargetID != "" {
			if sessionID == "session-"+b.oopifTargetID {
				return assignResearchTestResult(result, map[string]any{
					"frameTree": map[string]any{
						"frame": map[string]any{
							"id":  "oopif-root-frame",
							"url": b.treeFrameURL,
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
		return assignResearchTestResult(result, map[string]any{
			"executionContextId": 77,
		})
	case "Runtime.evaluate":
		if researchContextID(params) == 77 &&
			(b.oopifTargetID == "" || sessionID == "session-"+b.oopifTargetID) {
			return assignResearchTestResult(result, map[string]any{
				"result": map[string]any{
					"type": "object",
					"value": map[string]any{
						"text":       b.report,
						"root_count": 1,
						"streaming":  false,
						"too_large":  false,
					},
				},
			})
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

func TestExportResearchMatchesLoadedFrameAfterQueryDifference(t *testing.T) {
	browser := newResearchTestBrowserWithFrameURLs(
		syntheticResearchReport,
		"https://chatgpt.com/deep-research/synthetic-report?embed=initial",
		"https://chatgpt.com/deep-research/synthetic-report?loaded=final",
	)
	engine, journal := newResearchTestRuntime(t, browser)
	outputPath := filepath.Join(t.TempDir(), "redirected-research.txt")
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
		result.Cleanup.State != webagent.CleanupClosed {
		t.Fatalf("result = %+v", result)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil || string(written) != syntheticResearchReport+"\n" {
		t.Fatalf("read redirected export=%q err=%v", string(written), err)
	}
	counts, _, _, _, _, _, targets := browser.Snapshot()
	if browser.frameTreeCalls != 1 || browser.isolatedWorldCalls != 1 ||
		counts["Input.dispatchMouseEvent"] != 0 ||
		counts["Input.dispatchKeyEvent"] != 0 {
		t.Fatalf("browser counts = %+v", counts)
	}
	if _, exists := targets["user-page"]; !exists {
		t.Fatalf("user target was not preserved: %+v", targets)
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
		browser.frameTreeCalls != 2 || browser.isolatedWorldCalls != 1 {
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
	if counts["Target.attachToTarget"] != 2 ||
		counts["Target.detachFromTarget"] != 2 ||
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

func TestExportResearchUsesSoleSemanticChildAfterRedirect(t *testing.T) {
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
		browser.frameTreeCalls != 1 || browser.isolatedWorldCalls != 1 {
		t.Fatalf("result=%+v frame_tree=%d isolated_world=%d", result, browser.frameTreeCalls, browser.isolatedWorldCalls)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil || string(written) != syntheticResearchReport+"\n" {
		t.Fatalf("read redirected child export=%q err=%v", string(written), err)
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
