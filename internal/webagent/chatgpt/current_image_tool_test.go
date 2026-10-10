package chatgpt

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestCurrentImageToolObserverUsesPrimaryMenuAndTypedPill(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	client := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	var ignored chatGPTToolSurface
	if err := observeChatGPTToolSurface(context.Background(), newSelectionActivationSession(t, client), &ignored); err != nil {
		t.Fatal(err)
	}
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(client.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	script := `const assert=require('node:assert/strict');
 class HTMLElement {
  constructor(text='',attrs={}){this.innerText=text;this.textContent=text;this.attrs=attrs;this.children=[];this.classList={contains:()=>false};this.hidden=false;this.isContentEditable=false;}
  getAttribute(k){return this.attrs[k]??null}getBoundingClientRect(){return{width:44,height:44}}
  matches(s){return s==='button[data-list-navigation-item]'&&this.current===true}
  closest(s){if(s==='[data-composer-overlay-floating-ui]')return this.menu??null;if(s==='[data-mention-section-id="chatgpt-primary"]')return this.primary?this.menu:null;return null}
  querySelectorAll(s){if(s==='[data-menu-row-content] span')return[this.label,this.description].filter(Boolean);if(s.includes('data-inline-selection-pill'))return this.pills.filter(p=>p.attrs['data-keyword']||p.attrs['data-system-hint-type']==='picture_v2'&&p.attrs['data-system-hint-label']==='Create image');return []}
 }
 global.HTMLElement=HTMLElement;global.getComputedStyle=e=>({display:e.hidden?'none':'block',visibility:'visible',opacity:'1'});
 const menu=new HTMLElement();const row=new HTMLElement();row.current=true;row.menu=menu;row.primary=true;row.label=new HTMLElement('Create image');row.description=new HTMLElement('Visualize anything');row.description.classList={contains:x=>x==='text-codex-description'};
 const editor=new HTMLElement();editor.isContentEditable=true;editor.pills=[];
 let rows=[row];const document={querySelectorAll:s=>s.includes('data-list-navigation-item')?rows:s.includes('contenteditable')?[editor]:[]};
 function observe(){return ` + params.Expression + `}
 let o=observe();assert.equal(o.menu_open,true);assert.deepEqual(o.options,['Create image']);assert.equal(o.selected,'');
 row.primary=false;assert.equal(observe().menu_open,false);row.primary=true;row.hidden=true;assert.equal(observe().menu_open,false);row.hidden=false;
 rows=[];const pill=new HTMLElement('Create image',{'data-system-hint-type':'picture_v2','data-system-hint-label':'Create image'});editor.pills=[pill];assert.equal(observe().selected,'Create image');
 pill.attrs['data-system-hint-type']='unknown';assert.equal(observe().selected,'');pill.attrs['data-system-hint-type']='picture_v2';pill.attrs['data-system-hint-label']='Web search';assert.equal(observe().selected,'');
 editor.pills=[new HTMLElement('Web search',{'data-keyword':'Web search'})];assert.equal(observe().selected,'Web search');
 `
	cmd := exec.Command(node)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual tool observer: %v\n%s", err, out)
	}
}
