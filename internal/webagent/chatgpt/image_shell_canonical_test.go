package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestImageShellAndCanonicalProductionWalkers(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	source, err := os.ReadFile("ask.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(source)
	mappingStart := strings.Index(s, ".map(heading => {")
	mappingEnd := strings.Index(s[mappingStart:], "}).filter(Boolean)") + mappingStart + len("}).filter(Boolean)")
	mapping := s[mappingStart:mappingEnd]
	if strings.Count(s, mapping) != 2 {
		t.Fatal("composer/rendered shell mappings diverged")
	}
	a := strings.Index(s, "  const inlineText = node =>")
	b := strings.Index(s[a:], "  const editorPromptText =") + a
	walkers := s[a:b]
	fixture := `
const Node={TEXT_NODE:3};
class HTMLElement {
 constructor(tag='DIV',children=[],dataset={}){this.tagName=tag;this.childNodes=children;this.dataset=dataset;}
 matches(s){return !!this.dataset.pill&&s.includes('data-inline-selection-pill');}
}
const txt=s=>({nodeType:3,nodeValue:s});const p=s=>new HTMLElement('P',[txt(s)]);
const blank=()=>new HTMLElement('P',[new HTMLElement('BR')],{emptyParagraph:'true'});
const expected='Danish æøå\n\nsecond\n\nthird\n\nfourth';
const editor=new HTMLElement('DIV',[p('Danish æøå'),blank(),p('second'),blank(),p('third'),blank(),p('fourth')]);
editor.childNodes[0].childNodes.unshift(new HTMLElement('SPAN',[txt('Create image')],{pill:true}));
const normalize=value=>String(value||'').replace(/\r\n/g,'\n');
` + walkers + `
if(canonicalEditorText!==expected||toolFreeEditorText!==expected)throw Error('multiline canonical mismatch');
if(toolFreeEditorText===expected.replace('æøå','aeoa'))throw Error('unicode equality weakened');
function heading(user=false,ambiguous=false){
 const h={closest(){return null;}};
 h.parentElement={querySelectorAll(s){return s.includes('data-conversation-role')?(ambiguous?[h,h]:[h]):[{}];},querySelector(s){return s.includes('data-user-message-bubble')?(user?{}:null):{};}};
 return h;
}
const valid=heading(),upload=heading(true),ambiguous=heading(false,true);
const mapped=[valid,upload,ambiguous]` + mapping + `;
if(mapped.length!==3||mapped[0]!==valid.parentElement||mapped[1]!==upload||mapped[2]!==ambiguous)throw Error('assistant shell identity unsafe');
console.log('production mappings agree; native empty paragraphs/pill exact; user/ambiguous rejected');`
	out, err := exec.Command(node, "-e", fixture).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
}

func TestFullObserversImageOnlyShell(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	c := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	session := newSelectionActivationSession(t, c)
	var composer composerObservation
	var rendered renderedObservation
	if err := observeComposerForTool(context.Background(), session, "draw", ToolCreateImage, "", &composer); err != nil {
		t.Fatal(err)
	}
	if err := observeRendered(context.Background(), session, &rendered); err != nil {
		t.Fatal(err)
	}
	var expressions []string
	for _, call := range c.calls {
		if call.method == "Runtime.evaluate" {
			var p struct {
				Expression string `json:"expression"`
			}
			if err := json.Unmarshal(call.params, &p); err != nil {
				t.Fatal(err)
			}
			expressions = append(expressions, p.Expression)
		}
	}
	for _, tc := range []struct {
		name, state string
		old, user   bool
		count       int
		ready       bool
	}{
		{"complete", "complete", false, false, 1, true}, {"decoded pending", "pending", false, false, 1, false},
		{"old complete latest pending", "pending", true, false, 2, false}, {"user upload", "complete", false, true, 1, false},
		{"old complete latest no gallery", "no-gallery", true, false, 2, false},
		{"old complete latest unknown shell", "unknown", true, false, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := fmt.Sprintf(`
class Element {
 constructor(){this.childNodes=[];this.dataset={};this.innerText='';this.textContent='';this.offsetWidth=100;this.offsetHeight=100;this.parentElement=null;}
 getAttribute(n){return n==='data-talvt-turn-state'?this.state:null;}
 getClientRects(){return [1];}getBoundingClientRect(){return {width:100,height:100,left:0,top:0};}
 matches(){return false;}closest(s){return s.includes('data-talvt-turn-state')?this:null;}
 querySelector(s){if(s==='[data-user-message-bubble]')return this.user?{}:null;if(s==='[data-chatgpt-search-message-ids]')return this.message||null;return null;}
 querySelectorAll(s){if(s==='[data-conversation-role="assistant"]')return this.heading?[this.heading]:[];if(s==='[data-chatgpt-search-message-ids]')return this.message?[this.message]:[];if(s.includes('generated-image-gallery'))return this.message&&this.image?[{}]:[];if(s.includes('Generated image'))return this.image?[this.image]:[];return [];}
}
global.HTMLElement=Element;global.Node={TEXT_NODE:3,DOCUMENT_POSITION_FOLLOWING:4};global.getComputedStyle=()=>({display:'block',visibility:'visible',opacity:'1'});
function shell(state,user){const e=new Element();e.state=state;e.user=user;e.message=state==='unknown'?null:{};e.heading=new Element();e.heading.parentElement=e;e.image=state==='no-gallery'||state==='unknown'?null:new Element();if(e.image){e.image.complete=true;e.image.naturalWidth=852;e.image.naturalHeight=1847;}return e;}
const latest=shell(%q,%t);const shells=%t?[shell('complete',false),latest]:[latest];
const user=new Element();user.innerText='draw';user.textContent='draw';
global.document={readyState:'complete',querySelectorAll(s){if(s==='[data-conversation-role="assistant"]')return shells.map(e=>e.heading);if(s==='[data-user-message-bubble]')return [user];return [];},querySelector(){return null;},getElementById(){return null;}};
global.location={origin:'https://chatgpt.com',pathname:'/c/example-id'};
console.log(JSON.stringify({composer:%s,rendered:%s}));`, tc.state, tc.user, tc.old, expressions[0], expressions[1])
			out, err := exec.Command(node, "-e", fixture).CombinedOutput()
			if err != nil {
				t.Fatalf("%v %s", err, out)
			}
			var got struct {
				Composer composerObservation `json:"composer"`
				Rendered renderedObservation `json:"rendered"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if got.Composer.AssistantCount != tc.count || got.Rendered.AssistantCount != tc.count || got.Rendered.GeneratedImageReady != tc.ready {
				t.Fatalf("count/readiness mismatch: %+v", got)
			}
		})
	}
}
