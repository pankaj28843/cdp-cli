package chatgpt

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestDeleteObserverCurrentExactSidebarRow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	capture := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	var unused deleteObservation
	if err := observeDeleteControls(context.Background(), newSelectionActivationSession(t, capture), "owned-id", &unused); err != nil {
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
 constructor(tag, attrs={}) {this.tagName=tag;this.attrs=attrs;this.dataset={};this.id=attrs.id||'';this.innerText='';this.textContent='';}
 getAttribute(n){return this.attrs[n]??null;}
 hasAttribute(n){return this.attrs[n]!==undefined;}
 getBoundingClientRect(){return {left:10,top:10,width:20,height:20};}
 contains(e){return this===e;}
 querySelectorAll(s){
  if(this===row&&s==='button[aria-haspopup="menu"]')return [menu];
  if(this===row&&s==='button')return [menu];
  return [];
 }
 closest(s){return this===link&&s==='[data-sidebar-cycle-item]'?row:null;}
}

const link=new HTMLElement('A',{href:'/c/owned-id'});
const row=new HTMLElement('DIV',{'data-sidebar-cycle-item':''});
const menu=new HTMLElement('BUTTON',{'aria-haspopup':'menu','aria-label':'Chat actions'});
const aside=new HTMLElement('ASIDE',{'data-app-shell-left-panel-appearance':'default'});
const location={origin:'https://chatgpt.com',pathname:'/c/owned-id'};
const getComputedStyle=()=>({display:'block',visibility:'visible',opacity:'1'});
const document={
 elementFromPoint(){return menu;},
 querySelector(s){return s==='aside[data-app-shell-left-panel-appearance]'?aside:null;},
 querySelectorAll(s){
  if(s==='a[href]')return [link];
  if(s==='button')return [menu];
	  if(s==='aside[data-app-shell-left-panel-appearance]')return [aside];
  if(s==='[role="menuitem"]'||s==='[role="dialog"]')return [];
  return [];
 }
};
console.log(JSON.stringify(OBSERVER));`
	for _, test := range []struct {
		name  string
		patch string
		ready bool
	}{
		{name: "exact_owned_row", ready: true},
		{name: "wrong_row", patch: "href:'/c/other-id'"},
		{name: "ambiguous_menu", patch: `if(this===row&&s==='button[aria-haspopup="menu"]')return [menu,new HTMLElement('BUTTON',{'aria-haspopup':'menu','aria-label':'Chat actions'})];`},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := fixture
			switch test.name {
			case "wrong_row":
				script = strings.Replace(script, "href:'/c/owned-id'", test.patch, 1)
			case "ambiguous_menu":
				script = strings.Replace(script,
					`if(this===row&&s==='button[aria-haspopup="menu"]')return [menu];`,
					test.patch, 1)
			}
			script = strings.ReplaceAll(script, "OBSERVER", params.Expression)
			out, err := exec.Command(node, "-e", script).CombinedOutput()
			if err != nil {
				t.Fatalf("production delete observer: %v: %s", err, out)
			}
			var got deleteObservation
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if (strictDeleteIdentity(got) && got.PageButtonReady) != test.ready {
				t.Fatalf("exact row readiness = %+v", got)
			}
		})
	}
}

func TestDeleteObserverCurrentConfirmation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	capture := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	var unused deleteObservation
	if err := observeDeleteControls(context.Background(), newSelectionActivationSession(t, capture), "owned-id", &unused); err != nil {
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
 constructor(tag,attrs={}){this.tagName=tag;this.attrs=attrs;this.id=attrs.id||'';this.innerText='';this.textContent='';this.dataset={};}
 getAttribute(n){return this.attrs[n]??null;}
 hasAttribute(n){return this.attrs[n]!==undefined;}
 getBoundingClientRect(){return this===confirm?{left:50,top:10,width:20,height:20}:{left:10,top:10,width:20,height:20};}
 contains(e){return this===e;}
 closest(s){return this===link&&s==='[data-sidebar-cycle-item]'?row:null;}
 querySelectorAll(s){
  if(this===row&&s==='button[aria-haspopup="menu"]')return [menu];
  if(this===dialog&&s==='button')return [cancel,confirm,close];
  return [];
 }
}
const link=new HTMLElement('A',{href:'/c/owned-id'});
const row=new HTMLElement('DIV',{'data-sidebar-cycle-item':''});
const menu=new HTMLElement('BUTTON',{'aria-haspopup':'menu','aria-label':'Chat actions'});
const aside=new HTMLElement('ASIDE',{'data-app-shell-left-panel-appearance':'default'});
const dialog=new HTMLElement('DIV',{'role':'dialog'});
const otherDialog=new HTMLElement('DIV',{'role':'dialog'});
const cancel=new HTMLElement('BUTTON');cancel.innerText='Cancel';
const confirm=new HTMLElement('BUTTON');confirm.innerText='Delete chat';
const close=new HTMLElement('BUTTON');close.innerText='Close dialog';
const location={origin:'https://chatgpt.com',pathname:'/c/owned-id'};
const getComputedStyle=()=>({display:'block',visibility:'visible',opacity:'1'});
const document={
 elementFromPoint(x){return x>40?confirm:menu;},
 querySelector(){return null;},
 querySelectorAll(s){
  if(s==='a[href]')return [link];
  if(s==='button')return [menu];
  if(s==='aside[data-app-shell-left-panel-appearance]')return [aside];
  if(s==='[role="dialog"]')return [dialog];
  return [];
 }
};
console.log(JSON.stringify(OBSERVER));`
	for _, test := range []struct {
		name  string
		patch string
		ready bool
	}{
		{name: "exact_confirmation", ready: true},
		{name: "wrong_dialog", patch: `if(s==='[role="dialog"]')return [dialog,otherDialog];`},
		{name: "ambiguous_confirmation", patch: `if(this===dialog&&s==='button')return [cancel,confirm,(()=>{const b=new HTMLElement('BUTTON');b.innerText='Delete chat';return b})(),close];`},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := fixture
			switch test.name {
			case "wrong_dialog":
				script = strings.Replace(script, `if(s==='[role="dialog"]')return [dialog];`, test.patch, 1)
			case "ambiguous_confirmation":
				script = strings.Replace(script, `if(this===dialog&&s==='button')return [cancel,confirm,close];`, test.patch, 1)
			}
			script = strings.ReplaceAll(script, "OBSERVER", params.Expression)
			out, err := exec.Command(node, "-e", script).CombinedOutput()
			if err != nil {
				t.Fatalf("production confirmation observer: %v: %s", err, out)
			}
			var got deleteObservation
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			ready := strictDeleteIdentity(got) && got.DialogCount == 1 &&
				got.ConfirmationCount == 1 && got.ConfirmationReady
			if ready != test.ready {
				t.Fatalf("exact dialog readiness = %+v", got)
			}
		})
	}
}
