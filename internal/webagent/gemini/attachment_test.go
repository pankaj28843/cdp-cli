package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
)

type attachmentBoundary struct {
	*testsupport.Browser
	assignmentError bool
	sendUnavailable bool
	assignments     int
	assigned        []string
}

func (b *attachmentBoundary) CallSession(ctx context.Context, session, method string, params any, result any) error {
	var value any
	switch method {
	case "Runtime.evaluate":
		if b.sendUnavailable {
			raw, _ := json.Marshal(params)
			var input struct {
				Expression string `json:"expression"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return err
			}
			if strings.Contains(input.Expression, "geminiSendControl") {
				return json.Unmarshal([]byte(`{"result":{"type":"object","value":{"ready":false}}}`), result)
			}
		}
		return b.Browser.CallSession(ctx, session, method, params, result)
	case "DOM.getDocument":
		value = map[string]any{"root": map[string]any{"nodeId": 1}}
	case "DOM.querySelectorAll":
		value = map[string]any{"nodeIds": []int{2}}
	case "DOM.setFileInputFiles":
		b.assignments++
		raw, _ := json.Marshal(params)
		var input struct {
			Files []string `json:"files"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return err
		}
		b.assigned = input.Files
		if b.assignmentError {
			return errors.New("uncertain assignment")
		}
		value = map[string]any{}
	default:
		return b.Browser.CallSession(ctx, session, method, params, result)
	}
	raw, _ := json.Marshal(value)
	return json.Unmarshal(raw, result)
}

func TestAskAttachmentBatchAndFailureBeforeSend(t *testing.T) {
	for _, tc := range []struct {
		name                                                        string
		supported, ready, assignmentError, changes, sendUnavailable bool
	}{
		{"ready_batch", true, true, false, false, false},
		{"unsupported_type", false, true, false, false, false},
		{"processing_incomplete", true, false, false, false, false},
		{"assignment_unknown", true, true, true, false, false},
		{"preview_removed_during_prompt", true, true, false, true, false},
		{"send_control_unavailable", true, true, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &attachmentBoundary{Browser: testsupport.NewBrowser("user-page"), assignmentError: tc.assignmentError, sendUnavailable: tc.sendUnavailable}
			const prompt = "Review the two synthetic attachments"
			client.Evaluate = func(expression string, b *testsupport.Browser) (any, error) {
				switch {
				case strings.Contains(expression, "geminiSendControl"):
					return map[string]any{"ready": true, "x": 20, "y": 20}, nil
				case strings.Contains(expression, "geminiAttachmentNames"):
					return map[string]any{"ready": tc.ready && !(tc.changes && b.InsertCount > 0), "failed": !tc.ready}, nil
				case strings.Contains(expression, "geminiAttachmentInput"):
					return map[string]any{"count": 1, "ready": true, "supported": tc.supported}, nil
				case strings.Contains(expression, `button[aria-label="Upload & tools"]`):
					return map[string]any{"count": 1, "ready": true, "x": 20, "y": 20}, nil
				case strings.Contains(expression, "Open mode picker, currently "):
					return map[string]any{"route_ready": true, "editor_ready": true, "editor_count": 1, "current_mode": "Pro", "picker_count": 1, "answer_count": 0, "prompt_matches": b.InsertedText == prompt}, nil
				case strings.Contains(expression, "range.selectNodeContents"):
					return map[string]any{"ok": true}, nil
				case strings.Contains(expression, "navigator.clipboard"):
					return map[string]any{"prompt": prompt, "query_count": 1, "copy_button_count": 1, "clipboard_intercepted": true, "captured": true}, nil
				case strings.Contains(expression, "conversation_id"):
					return map[string]any{"route_matches": true, "conversation_id": "abcdefghijklmnop", "text": "Synthetic answer", "is_streaming": false, "completion_ready": true, "answer_count": 1}, nil
				default:
					return map[string]any{}, nil
				}
			}
			dir := t.TempDir()
			paths := []string{filepath.Join(dir, "first.txt"), filepath.Join(dir, "second.md")}
			for _, path := range paths {
				if err := os.WriteFile(path, []byte("synthetic fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			engine, journal, err := testsupport.NewRuntime(dir, client)
			if err != nil {
				t.Fatal(err)
			}
			store, err := NewStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			result := Ask(context.Background(), AskConfig{BrowserConfig: BrowserConfig{Client: client, Engine: engine, Journal: journal, BuildCommit: "test"}, Store: store, FilePaths: paths, Timeout: time.Second, ComposerTimeout: time.Second, PollInterval: time.Millisecond, Now: testsupport.FixedNow}, prompt)
			wantOK := tc.supported && tc.ready && !tc.assignmentError && !tc.changes && !tc.sendUnavailable
			if result.OK != wantOK {
				t.Fatalf("OK=%v want %v result=%+v", result.OK, wantOK, result)
			}
			counts, _, _, _, _, _, targets := client.Snapshot()
			wantAssignments := 1
			if !tc.supported {
				wantAssignments = 0
			}
			if client.assignments != wantAssignments {
				t.Fatalf("assignments=%d want %d", client.assignments, wantAssignments)
			}
			if wantAssignments == 1 && len(client.assigned) != 2 {
				t.Fatalf("batch size=%d", len(client.assigned))
			}
			if result.Action == nil || (wantOK && result.Action.RawInputCount != 1) || (!wantOK && (result.Action.RawInputCount != 0 || counts["Input.dispatchKeyEvent"] != 0)) {
				t.Fatalf("Send evidence=%+v counts=%v", result.Action, counts)
			}
			if _, ok := targets["owned-1"]; ok {
				t.Fatal("owned target remains")
			}
			if _, ok := targets["user-page"]; !ok {
				t.Fatal("user target closed")
			}
		})
	}
}

func TestAttachmentReadinessProductionJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for production JS check")
	}
	var expression string
	browser := testsupport.NewBrowser("user-page")
	browser.Evaluate = func(value string, _ *testsupport.Browser) (any, error) {
		expression = value
		return map[string]any{}, nil
	}
	session, err := cdp.AttachToTargetWithClient(context.Background(), browser, "user-page", func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	_, _ = observeAttachments(context.Background(), session, []string{"synthetic.txt"})
	script := `const assert=require('node:assert/strict');
 const box={width:20,height:20};
 const element=()=>({getBoundingClientRect:()=>box,getAttribute:()=>null});
 const chip=element();chip.getAttribute=k=>k==='aria-describedby'?'name-proof':null;
 const send=element();send.disabled=false;
 const progress=element(),alert=element();alert.innerText='Upload failed';
 let chips=[chip],busy=false,failed=false,name='synthetic.txt',composers;
 const composer=element();composer.querySelectorAll=s=>s.includes('file-preview-container')?chips:s.includes('progress')?(busy?[progress]:[]):[send];
 composers=[composer];
 const document={querySelectorAll:s=>s.includes('text-input-field')?composers:(failed?[alert]:[]),getElementById:id=>({textContent:name})};
 const getComputedStyle=()=>({display:'block',visibility:'visible'});
 function observe(){return ` + expression + `;}
 assert.equal(observe().ready,true);
 busy=true;assert.equal(observe().ready,false);busy=false;
 send.disabled=true;assert.equal(observe().ready,false);send.disabled=false;
 name='different.txt';assert.equal(observe().ready,false);name='synthetic.txt';
 chips=[chip,chip];assert.equal(observe().ready,false);chips=[chip];
 failed=true;assert.equal(observe().ready,false);assert.equal(observe().failed,true);failed=false;
 composers=[composer,composer];assert.equal(observe().ready,false);
 `
	cmd := exec.Command(node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("production JS: %v\n%s", err, output)
	}
}

func TestInvalidAttachmentBeforeBrowser(t *testing.T) {
	result := Ask(context.Background(), AskConfig{FilePaths: []string{t.TempDir()}}, "Synthetic prompt")
	if result.OK || result.Error == nil || result.Error.Code != "gemini_attachment_invalid" || result.Action == nil || result.Action.RawInputCount != 0 || result.Cleanup.Required {
		t.Fatalf("result=%+v", result)
	}
}

func TestPromptCopyWaitsForClipboardCompletion(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for production JS check")
	}
	var expression string
	browser := testsupport.NewBrowser("user-page")
	browser.Evaluate = func(value string, _ *testsupport.Browser) (any, error) {
		expression = value
		return map[string]any{}, nil
	}
	session, err := cdp.AttachToTargetWithClient(context.Background(), browser, "user-page", func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var observation promptCaptureObservation
	if err := captureExactRenderedPrompt(context.Background(), session, &observation); err != nil {
		t.Fatal(err)
	}
	script := `const assert=require('node:assert/strict');
class HTMLElement {
 getBoundingClientRect(){return {width:20,height:20};}
}
const getComputedStyle=()=>({display:'block',visibility:'visible',opacity:'1'});
const clipboard={write:async()=>{throw Error('system clipboard touched');},writeText:async()=>{throw Error('system clipboard touched');}};
const originalWrite=clipboard.write, originalWriteText=clipboard.writeText;
const navigator={clipboard};
const prompt='Preserve  two spaces\nand this newline';
const button=new HTMLElement();button.disabled=false;button.getAttribute=()=> 'Copy prompt';
button.click=()=>setTimeout(()=>clipboard.write([{types:['text/html','text/plain'],getType:async()=>({text:async()=>prompt})}]),300);
const content=new HTMLElement(),query=new HTMLElement();query.querySelectorAll=s=>s==='button'?[button]:[content];
const document={querySelectorAll:()=>[query]};
(async()=>{
 const result=await ` + expression + `;
 assert.equal(result.captured,true);
 assert.equal(result.prompt,prompt);
 assert.equal(clipboard.write,originalWrite);
 assert.equal(clipboard.writeText,originalWriteText);
})().catch(error=>{console.error(error);process.exitCode=1;});`
	cmd := exec.Command(node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("delayed production clipboard capture: %v\n%s", err, output)
	}
}
