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

// The live composer can leave only the top of Send in the viewport after a
// long prompt and attachments. Its geometric center is then off-screen.
func TestComposerSendUsesVisibleIntersection(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	capture := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	var unused composerObservation
	if err := observeComposer(context.Background(), newSelectionActivationSession(t, capture), "test", "Pro", &unused); err != nil {
		t.Fatal(err)
	}
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(capture.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	fixture := `
class HTMLElement {
 constructor(tag='DIV',children=[]){this.tagName=tag;this.childNodes=children;this.dataset={};this.isContentEditable=false;this.innerText='';this.textContent='';this.attrs={};this.type='';}
 getAttribute(n){return this.attrs[n]??null;}
 getBoundingClientRect(){return this===send?{left:100,top:950,right:136,bottom:990,width:36,height:40}:{left:0,top:0,right:100,bottom:30,width:100,height:30};}
 closest(s){return s==='form'?form:null;}
 querySelectorAll(s){return this===form&&s==='button'?[send]:[];}
 contains(e){return e===this;}
 matches(){return false;}
}
const Node={TEXT_NODE:3};const paragraph=new HTMLElement('P',[{nodeType:3,nodeValue:'test'}]);
const editor=new HTMLElement('DIV',[paragraph]);editor.isContentEditable=true;editor.innerText='test';editor.textContent='test';editor.attrs={'aria-label':'Ask ChatGPT'};
const form=new HTMLElement('FORM');const send=new HTMLElement('BUTTON');send.type='submit';send.attrs={'aria-label':'Send'};
const picker=new HTMLElement('BUTTON');picker.innerText='6 Pro';picker.attrs={'aria-haspopup':'menu','aria-label':'Thinking effort'};
const model=new HTMLElement('BUTTON');const window={innerWidth:1920,innerHeight:966};const performance={timeOrigin:1};
const getComputedStyle=()=>({display:'block',visibility:'visible',opacity:'1'});
const location={origin:'https://chatgpt.com',pathname:'/'};
const document={readyState:'complete',elementFromPoint(x,y){return x>=100&&x<=136&&y>=950&&y<966?send:editor;},getElementById(){return null;},querySelectorAll(s){
 if(s==='[contenteditable="true"][role="textbox"]')return [editor];
 if(s==='button[aria-haspopup="menu"]')return [picker];
 if(s==='button[aria-label="Select ChatGPT model"]')return [model];
 return [];
}};
console.log(JSON.stringify(OBSERVER));`
	fixture = strings.ReplaceAll(fixture, "OBSERVER", params.Expression)
	out, err := exec.Command(node, "-e", fixture).CombinedOutput()
	if err != nil {
		t.Fatalf("production composer observation: %v: %s", err, out)
	}
	var got composerObservation
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !got.SendReady || got.SendCount != 1 || got.SendY < 950 || got.SendY >= 966 {
		t.Fatalf("partially visible Send = %+v", got)
	}
}
