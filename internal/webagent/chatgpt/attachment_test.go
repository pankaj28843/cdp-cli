package chatgpt

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
)

func TestAttachmentPreflightCurrentInput(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for JavaScript observation validation")
	}
	client := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	_, _ = verifyAttachmentPreflight(context.Background(), newSelectionActivationSession(t, client))
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(client.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	selector, _ := json.Marshal(chatGPTFileInputSelector)
	script := `const assert = require('node:assert/strict');
 const selector = ` + string(selector) + `;
 assert.ok(selector.includes('form:has('));
 assert.ok(selector.includes(':not([accept])'));
 const editor = {isContentEditable:true};
 const form = {contains: node => node === editor, querySelectorAll: () => []};
 const input = {type:'file', files:[], closest: () => form};
 let inputs = [input], editors = [editor];
 const document = {querySelectorAll: query => {
  if (query === selector) return inputs;
  if (query === '[contenteditable="true"][role="textbox"]') return editors;
  throw new Error('unexpected discovery selector');
 }};
 function observe() { return ` + params.Expression + `; }
 assert.equal(observe().ok, true);
 inputs = [input, input]; assert.equal(observe().ok, false);
 inputs = [input]; input.files = [{name:'synthetic.md'}]; assert.equal(observe().ok, false);
 input.files = []; editors = [editor, editor]; assert.equal(observe().ok, false);
 editors = [editor]; form.contains = () => false; assert.equal(observe().ok, false);
 `
	command := exec.Command(node)
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("attachment preflight JavaScript: %v\n%s", err, output)
	}
}

// CDP assignment is one side effect for the whole batch, including ambiguous
// transport failure. Observation may repeat; assignment must never repeat.
type batchAttachmentClient struct {
	selectionActivationClient
	assignmentError error
}

func (c *batchAttachmentClient) CallSession(ctx context.Context, session, method string, params any, result any) error {
	if method == "Runtime.evaluate" {
		return c.selectionActivationClient.CallSession(ctx, session, method, params, result)
	}
	raw, _ := json.Marshal(params)
	c.calls = append(c.calls, selectionActivationCall{method: method, params: raw})
	switch method {
	case "DOM.getDocument":
		return decodeSelectionActivationResult(result, map[string]any{"root": map[string]any{"nodeId": 1}})
	case "DOM.querySelector":
		return decodeSelectionActivationResult(result, map[string]any{"nodeId": 2})
	case "DOM.describeNode":
		return decodeSelectionActivationResult(result, map[string]any{"node": map[string]any{"nodeName": "INPUT", "attributes": []string{"type", "file"}}})
	case "DOM.setFileInputFiles":
		return c.assignmentError
	}
	return nil
}

func TestAttachmentBatchAssignedOnce(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "ambiguous"}[ambiguous], func(t *testing.T) {
			dir := t.TempDir()
			config := AskConfig{}
			for _, name := range []string{"mobile.png", "desktop.png", "contract.md"} {
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte("synthetic fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				config.FilePaths = append(config.FilePaths, path)
			}
			uploads, err := resolveLocalUploads(config)
			if err != nil {
				t.Fatal(err)
			}
			client := &batchAttachmentClient{}
			client.evaluation = json.RawMessage(`{"ok":true,"input_match":true,"rendered_attachment_added":true,"rendered_name_match":true}`)
			if ambiguous {
				client.assignmentError = errors.New("synthetic transport interruption")
			}
			data, expectation, failure := attachLocalFilesOnce(context.Background(), newSelectionActivationSession(t, client), uploads, time.Second, time.Millisecond)
			assignments := 0
			for _, call := range client.calls {
				if call.method != "DOM.setFileInputFiles" {
					continue
				}
				assignments++
				var value struct {
					Files []string `json:"files"`
				}
				if err := json.Unmarshal(call.params, &value); err != nil {
					t.Fatal(err)
				}
				if len(value.Files) != 3 {
					t.Fatalf("assignment paths=%v", value.Files)
				}
			}
			if assignments != 1 || len(data) != 3 {
				t.Fatalf("assignments=%d data=%+v", assignments, data)
			}
			if ambiguous {
				if failure == nil || failure.RetrySafe || failure.Code != "chatgpt_attachment_assignment_unknown" || expectation != nil {
					t.Fatalf("ambiguous assignment failure=%+v", failure)
				}
			} else if failure != nil || expectation == nil || len(expectation.Names) != 3 {
				t.Fatalf("expectation=%+v failure=%+v", expectation, failure)
			}
			for _, item := range data {
				if item.AssignmentAttempts != 1 {
					t.Fatalf("attempts=%d", item.AssignmentAttempts)
				}
				if ambiguous && item.AssignmentOutcome != attachmentAssignmentUnknown {
					t.Fatalf("outcome=%s", item.AssignmentOutcome)
				}
			}
		})
	}
}

func TestResolveLocalUploadsRejectsAmbiguousNamesAndInvalidBatch(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "contract.md")
	if err := os.WriteFile(first, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{first, first}, {first, filepath.Join(dir, "missing.md")}, {first, ""}} {
		if _, err := resolveLocalUploads(AskConfig{FilePaths: paths}); err == nil {
			t.Fatalf("accepted invalid batch=%v", paths)
		}
	}
	uploads, err := resolveLocalUploads(AskConfig{FilePaths: []string{first}})
	if err != nil || len(uploads) != 1 {
		t.Fatalf("single file: uploads=%v error=%v", uploads, err)
	}
}

func TestAttachmentBatchChangedFilePreventsAllAssignment(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "first.md"), filepath.Join(dir, "second.md")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	uploads, err := resolveLocalUploads(AskConfig{FilePaths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths[1], []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &batchAttachmentClient{}
	_, _, failure := attachLocalFilesOnce(context.Background(), newSelectionActivationSession(t, client), uploads, time.Second, time.Millisecond)
	if failure == nil || failure.Code != "chatgpt_attachment_changed" || !failure.RetrySafe {
		t.Fatalf("failure=%+v", failure)
	}
	if len(client.calls) != 0 {
		t.Fatalf("changed batch performed CDP calls: %+v", client.calls)
	}
}

func TestAttachmentBatchRenderedObservation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for JavaScript observation validation")
	}
	client := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	var observation attachmentObservation
	if err := observeAttachment(context.Background(), newSelectionActivationSession(t, client), "contract.md", 0, &observation, 3); err != nil {
		t.Fatal(err)
	}
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(client.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	selector, _ := json.Marshal(chatGPTFileInputSelector)
	script := `const assert = require('node:assert/strict');
 class HTMLElement {getBoundingClientRect(){return {width:10,height:10}}}
 const getComputedStyle = () => ({display:'block',visibility:'visible',opacity:'1'});
 const editor = {isContentEditable:true};
 let processing=false;
 class Group extends HTMLElement {
  constructor(name){super();this.name=name}
  getAttribute(){return 'Remove '+this.name}
  querySelector(){return {}}
  querySelectorAll(){return processing && this.name==='contract.md' ? [new HTMLElement()] : []}
  get parentElement(){return this}
 }
 let groups = ['mobile.png','desktop.png','contract.md'].map(name=>new Group(name));
 const form = {contains:n=>n===editor,querySelectorAll:query=>{assert.equal(query,'[data-composer-attachments] button[aria-label^="Remove "]');return groups}};
 const input = {files:groups.map(g=>({name:g.name})),closest:()=>form};
 const document = {querySelectorAll:query=>{
  if(query===` + string(selector) + `)return [input];
  if(query==='[contenteditable="true"][role="textbox"]')return [editor];
  if(query==='[role="dialog"]')return [];
  throw new Error('unexpected selector');
 }};
 function observe(){return ` + params.Expression + `;}
 assert.equal(observe().ok,true); assert.equal(observe().input_match,true);
 input.files=[]; assert.equal(observe().ok,true); assert.equal(observe().input_match,false);
 processing=true; assert.equal(observe().processing,true); processing=false;
 groups.pop(); assert.equal(observe().ok,false);
 groups.push(new Group('contract.md'),new Group('extra.md')); assert.equal(observe().ok,false);
 groups=[new Group('contract.md'),new Group('contract.md'),new Group('mobile.png')]; assert.equal(observe().ok,false);
 `
	command := exec.Command(node)
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("batch observation JavaScript: %v\n%s", err, output)
	}
}
