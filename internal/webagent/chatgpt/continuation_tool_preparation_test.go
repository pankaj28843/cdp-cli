package chatgpt

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type toolPreparationClient struct {
	lateSelectionClient
	expressions []string
}

func (c *toolPreparationClient) CallSession(ctx context.Context, sessionID, method string, params any, result any) error {
	if method == "Runtime.evaluate" {
		var p struct {
			Expression string `json:"expression"`
		}
		encoded, _ := json.Marshal(params)
		_ = json.Unmarshal(encoded, &p)
		c.expressions = append(c.expressions, p.Expression)
		if strings.Contains(p.Expression, `const kind = "editor-tool"`) {
			return decodeSelectionActivationResult(result, map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"ok": true, "count": 1, "activated": true}}})
		}
		if strings.Contains(p.Expression, "send_ready:") {
			if !strings.Contains(p.Expression, `const expectedTool = "Create image"`) {
				t := "tool expectation missing"
				return &promptPreparationTestError{t}
			}
			return decodeSelectionActivationResult(result, map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"document_ready_state": "complete", "conversation_route_ready": true, "editor_ready": true, "editor_count": 1, "prompt_matches": true, "chat_count": 1, "work_count": 1, "chat_selected": true, "intelligence_count": 1, "selected_intelligence": "High", "send_count": 1, "send_ready": true, "assistant_count": 1, "user_message_count": 1, "conversation_id": "conversation-1", "selected_tool": "Create image"}}})
		}
	}
	return c.lateSelectionClient.CallSession(ctx, sessionID, method, params, result)
}

type promptPreparationTestError struct{ message string }

func (e *promptPreparationTestError) Error() string { return e.message }
func TestContinuationPreparationPropagatesToolBeforeAndAfterInput(t *testing.T) {
	c := &toolPreparationClient{lateSelectionClient: lateSelectionClient{conversationID: "conversation-1", userCount: 1, assistantCount: 1}}
	_, o, err := prepareVerifiedContinuationPromptWithToolExpectation(context.Background(), newSelectionActivationSession(t, c), "conversation-1", "Long Danish æøå\n\nSecond paragraph", 1, 1, "High", thinkingSelectionExpectation{Label: "High"}, "", ToolCreateImage, time.Second, time.Millisecond)
	if err != nil || !continuationToolMatches(o, ToolCreateImage) || c.insertCount != 1 {
		t.Fatalf("tool propagation/input mismatch: %+v err=%v inserts=%d", o, err, c.insertCount)
	}
	observations := 0
	preservedRange := false
	var rangeExpression string
	for _, e := range c.expressions {
		if strings.Contains(e, "send_ready:") {
			observations++
		}
		if strings.Contains(e, "range.selectNodeContents") {
			rangeExpression = e
			preservedRange = strings.Contains(e, `if ("Create image" !== '')`) && strings.Contains(e, "range.setStartAfter(pills[0])")
		}
	}
	if observations < 3 || !preservedRange {
		t.Fatalf("tool observers=%d preserved range=%v", observations, preservedRange)
	}

	c.assertNoRawSend(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for actual preservation expression")
	}
	fixture := `let afterPill=false;
const pill={getAttribute(n){return n==='data-system-hint-label'?'Create image':null;}};
const editor={isContentEditable:true,querySelectorAll(){return [pill];},focus(){document.activeElement=this;}};
const range={selectNodeContents(e){afterPill=false;},setStartAfter(p){if(p!==pill)throw Error('foreign pill');afterPill=true;}};
global.document={activeElement:null,querySelectorAll(){return [editor];},createRange(){return range;}};
global.window={getSelection(){return {removeAllRanges(){},addRange(r){if(r!==range)throw Error('foreign range');}};}};
const result=` + rangeExpression + `;
if(!result.ok||!afterPill)throw Error('production selection removed Create image pill');`
	if out, err := exec.Command(node, "-e", fixture).CombinedOutput(); err != nil {
		t.Fatalf("actual tool preservation: %v %s", err, out)
	}
}
