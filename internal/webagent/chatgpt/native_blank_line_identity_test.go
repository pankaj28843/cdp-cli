package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestRenderedNativeBlankLinesPreserveExactPromptIdentity(t *testing.T) {
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
	// Anonymized actual DOM shape: three paragraphs of 429/738/2192
	// characters, two copy-only newline spans, and 31 BRs in the last one.
	// This preserves the observed 3363-character brief without private prose.
	parts := make([]string, 32)
	for i := range parts {
		parts[i] = strings.Repeat("C", 67)
	}
	parts[31] = strings.Repeat("C", 84)
	first, second, third := strings.Repeat("A", 429), strings.Repeat("B", 738), strings.Join(parts, "\n")
	prompt := first + "\n\n" + second + "\n\n" + third
	if len(prompt) != 3363 {
		t.Fatal("fixture no longer matches observed shape")
	}
	for _, tc := range []struct {
		name, separator                 string
		marked, changed, rawExact, want bool
	}{
		{"observed native spacing and ordinary inline spans", "\n", true, false, false, true},
		{"one changed character", "\n", true, true, false, false},
		{"malformed nonblank separator", "\nX", true, false, true, false},
		{"malformed whitespace separator", "\n ", true, false, true, false},
		{"ordinary unmarked span is not paragraph spacing", "\n", false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := fmt.Sprintf(`
class Element {
 constructor(tag='DIV',children=[],text=''){this.tagName=tag;this.childNodes=children;this.innerText=text;this.textContent=text;this.dataset={};this.attrs={};this.offsetWidth=100;this.offsetHeight=100;}
 getAttribute(k){return this.attrs[k]??null;}getClientRects(){return [1];}closest(){return null;}
 matches(s){return !!this.pill&&s.includes('chatgpt-system-hint://picture_v2');}
 querySelector(s){return this.pillNode&&s.includes('selection-pill')?this.pillNode:null;}
 querySelectorAll(s){return this.roots&&s==='[data-markdown-text-tone="user-message"]'?this.roots:[];}
}
global.HTMLElement=Element;global.Node={TEXT_NODE:3,DOCUMENT_POSITION_FOLLOWING:4};
const text=s=>({nodeType:3,nodeValue:s});const span=s=>new Element('SPAN',[text(s)],s);
const pill=new Element('SPAN',[], 'Create image');pill.pill=true;
const first=%q, second=%q, third=%q, expected=%q;
const actualSecond=%t?'D'+second.slice(1):second;
const firstP=new Element('P',[pill,span(' '+first)]), secondP=new Element('P',[span(actualSecond)]);
const thirdChildren=[];third.split('\n').forEach((part,i)=>{if(i)thirdChildren.push(new Element('BR'));thirdChildren.push(span(part));});
const separator=()=>{const s=span(%q);if(%t)s.attrs['data-markdown-copy']='blank-lines';return s;};
const root=new Element('DIV',[firstP,separator(),secondP,separator(),new Element('P',thirdChildren)]);root.pillNode=pill;
const actual=first+'\n\n'+actualSecond+'\n\n'+third;
const user=new Element('DIV',[], %t?expected:'Create image\n '+actual+'\n…\nShow more');user.roots=[root];
global.document={querySelectorAll(s){return s==='[data-user-message-bubble]'?[user]:[];}};global.location={origin:'https://chatgpt.com',pathname:'/c/example-id'};
console.log(JSON.stringify(%s));`, first, second, third, prompt, tc.changed, tc.separator, tc.marked, tc.rawExact, params.Expression)
			out, err := exec.Command(node, "-e", fixture).CombinedOutput()
			if err != nil {
				t.Fatalf("production observer: %v %s", err, out)
			}
			var got renderedObservation
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if renderedPromptMatches(got, fingerprintPrompt(prompt)) != tc.want {
				t.Fatalf("exact identity want=%v; candidate lengths=%v", tc.want, promptCandidateLengths(got))
			}
		})
	}
}

func promptCandidateLengths(observation renderedObservation) []int {
	lengths := make([]int, len(observation.PromptCandidates))
	for i, candidate := range observation.PromptCandidates {
		lengths[i] = len(candidate)
	}
	return lengths
}
