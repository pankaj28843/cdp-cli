package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"
)

func TestRenderedUserRootExcludesOnlyNativeToolAndOutsideControls(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	client := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	var ignored renderedObservation
	if err := observeRendered(context.Background(), newSelectionActivationSession(t, client), &ignored); err != nil {
		t.Fatal(err)
	}
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(client.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	const prompt = "Danish æøå: Create image is literal prose.\n\nKeep Show more as actual prompt wording."
	for _, tc := range []struct {
		name               string
		ambiguous, changed bool
		want               bool
	}{{"native chip and outside controls", false, false, true}, {"changed prompt", false, true, false}, {"ambiguous content roots", true, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := fmt.Sprintf(`
class Element {
 constructor(tag='DIV',children=[],text=''){this.tagName=tag;this.childNodes=children;this.innerText=text;this.textContent=text;this.dataset={};this.offsetWidth=100;this.offsetHeight=100;}
 getAttribute(){return null;}getClientRects(){return [1];}closest(){return null;}
 matches(s){return !!this.pill&&s.includes('chatgpt-system-hint://picture_v2');}
 querySelector(s){return this.pillNode&&s.includes('selection-pill')?this.pillNode:null;}
 querySelectorAll(s){return this.roots&&s==='[data-markdown-text-tone="user-message"]'?this.roots:[];}
}
global.HTMLElement=Element;global.Node={TEXT_NODE:3,DOCUMENT_POSITION_FOLLOWING:4};
const text=s=>({nodeType:3,nodeValue:s});const pill=new Element('SPAN',[], 'Create image');pill.pill=true;
const actual=%q;const parts=actual.split('\n\n');
const first=new Element('P',[pill,text(' '+parts[0])]);const blank=new Element('P',[new Element('BR')]);blank.dataset.emptyParagraph='true';
const last=new Element('P',[text(parts[1]+(%t?' altered':''))]);const root=new Element('DIV',[first,blank,last]);root.pillNode=pill;
const user=new Element('DIV',[], %t?actual:'Create image\n '+actual+'\n\n…\nShow more');user.roots=%t?[root,root]:[root];
global.document={querySelectorAll(s){return s==='[data-user-message-bubble]'?[user]:[];}};global.location={origin:'https://chatgpt.com',pathname:'/c/example-id'};
console.log(JSON.stringify(%s));`, prompt, tc.changed, tc.ambiguous, tc.ambiguous, params.Expression)
			out, err := exec.Command(node, "-e", fixture).CombinedOutput()
			if err != nil {
				t.Fatalf("production observer: %v %s", err, out)
			}
			var got renderedObservation
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if renderedPromptMatches(got, fingerprintPrompt(prompt)) != tc.want {
				t.Fatalf("exact prompt identity mismatch: %+v", got)
			}
		})
	}
}
