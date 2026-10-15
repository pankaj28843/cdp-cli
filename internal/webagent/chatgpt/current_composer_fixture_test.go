package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// Execute the production observer on the current native textbox/paragraph DOM,
// including a draft restored on the home route. No fake observation is accepted.
func TestCurrentNativeComposerBlankAndRestoredDraft(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	capture := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	var unused composerObservation
	if err := observeComposer(context.Background(), newSelectionActivationSession(t, capture), "", "", &unused); err != nil {
		t.Fatal(err)
	}
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(capture.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	for _, draft := range []string{"", "old unrelated avatar request"} {
		t.Run(fmt.Sprintf("draft_%d", len(draft)), func(t *testing.T) {
			fixture := `
class HTMLElement {
 constructor(tag='DIV',children=[]){this.tagName=tag;this.childNodes=children;this.dataset={};this.isContentEditable=false;this.innerText='';this.textContent='';}
 getAttribute(n){return this.attrs?.[n]??null;}
 getBoundingClientRect(){return {left:0,top:0,width:100,height:30};}
 closest(s){return s==='form'?form:null;}
 querySelectorAll(){return [];} contains(e){return e===this;}
 matches(s){return false;}
}
const Node={TEXT_NODE:3};const draft=DRAFT;
const paragraph=new HTMLElement('P',draft?[{nodeType:3,nodeValue:draft}]:[new HTMLElement('BR')]);
if(!draft)paragraph.dataset.emptyParagraph='true';
const editor=new HTMLElement('DIV',[paragraph]);editor.isContentEditable=true;editor.innerText=draft||'\n';editor.textContent=draft;
editor.attrs={'role':'textbox','contenteditable':'true','aria-label':'Ask ChatGPT'};
const form=new HTMLElement('FORM');const picker=new HTMLElement('BUTTON');picker.innerText='6 Pro';picker.attrs={'aria-haspopup':'menu','aria-label':'Thinking effort'};
const model=new HTMLElement('BUTTON');
const getComputedStyle=()=>({display:'block',visibility:'visible',opacity:'1'});
const location={origin:'https://chatgpt.com',pathname:'/'};
const document={readyState:'complete',elementFromPoint(){return editor;},getElementById(){return null;},querySelectorAll(s){
 if(s==='[contenteditable="true"][role="textbox"]')return [editor];
 if(s==='button[aria-haspopup="menu"]')return [picker];
 if(s==='button[aria-label="Select ChatGPT model"]')return [model];
 return [];
}};
console.log(JSON.stringify(OBSERVER));`
			encoded, _ := json.Marshal(draft)
			fixture = strings.ReplaceAll(fixture, "DRAFT", string(encoded))
			fixture = strings.ReplaceAll(fixture, "OBSERVER", params.Expression)
			out, err := exec.Command(node, "-e", fixture).CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			var got composerObservation
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if !got.RouteReady || !got.EditorReady || !got.DefaultChatSurface || got.SelectedIntelligence != "Pro" || got.CanonicalCharacters != len(draft) || got.DraftCharacters != len(draft) || got.PromptMatches != (draft == "") || restoredComposerDraft(got) != (draft != "") {
				t.Fatalf("current DOM observation=%+v", got)
			}
		})
	}
}
