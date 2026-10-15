package chatgpt

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestProductionSelectionCombinedModelEffortMenu(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	capture := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	var unused selectionSurface
	if err := observeSelectionSurface(context.Background(), newSelectionActivationSession(t, capture), &unused); err != nil {
		t.Fatal(err)
	}
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(capture.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	fixture := `
const assert=require('node:assert/strict');
class HTMLElement {
 constructor(text='',attrs={}){this.innerText=text;this.textContent=text;this.attrs=attrs;this.items=[];this.parent=null;}
 getAttribute(n){return this.attrs[n]??null;}
 getBoundingClientRect(){return {left:0,top:0,right:100,bottom:30,width:100,height:30};}
 closest(s){if(s==='form')return form;if(s==='[role="menu"]')return this.attrs.role==='menu'?this:this.parent;if(s==='[data-model-reasoning-effort-slider]')return this.attrs.role==='slider'?this:null;return null;}
 querySelectorAll(s){const match=s.match(/^\[role="(.*)"\]$/);return match?this.items.filter(e=>e.getAttribute('role')===match[1]):[];}
 contains(){return true;}
}
const form=new HTMLElement(),editor=new HTMLElement(),picker=new HTMLElement('Pro',{'aria-haspopup':'menu','aria-expanded':'true','aria-controls':'picker-menu','aria-label':'Select ChatGPT model'});
const menu=new HTMLElement('',{role:'menu'});
const slider=new HTMLElement('',{role:'slider','aria-valuemin':'0','aria-valuemax':'4','aria-valuenow':'4','aria-valuetext':'Pro'});
const gpt6=new HTMLElement('GPT-6',{role:'menuitemradio','aria-checked':'true'}),sol=new HTMLElement('GPT-5.6 Sol',{role:'menuitemradio','aria-checked':'false'});
menu.items=[slider,gpt6,sol];for(const e of menu.items)e.parent=menu;
let menus=[menu];const window={innerWidth:1920,innerHeight:966};const location={origin:'https://chatgpt.com',pathname:'/'};
const getComputedStyle=()=>({display:'block',visibility:'visible',opacity:'1'});
const document={querySelector(s){return s==='[contenteditable="true"][role="textbox"]'?editor:null;},getElementById(){return menu;},elementFromPoint(){return editor;},querySelectorAll(s){if(s==='button[aria-haspopup="menu"]'||s==='button[aria-label="Select ChatGPT model"]')return [picker];if(s==='[role="menu"]')return menus;return [];}};
function observe(){return OBSERVER;}
let result=observe();assert.equal(result.selected_thinking,'Pro');assert.equal(result.thinking_slider_value,4);assert.equal(result.thinking_slider_max,4);assert.equal(result.selected_model,'GPT-6');assert.equal(result.model_menu_open,true);assert.equal(result.model_trigger_count,0);assert.equal(result.model_options_provider_order.length,2);
const unrelated=new HTMLElement('',{role:'menu'});const duplicate=new HTMLElement('Unrelated',{role:'menuitemradio','aria-checked':'true'});duplicate.parent=unrelated;unrelated.items=[duplicate];menus=[menu,unrelated];result=observe();assert.equal(result.model_menu_open,false);assert.equal(result.selected_model,'');assert.equal(result.model_options_provider_order.length,0);
`
	command := exec.Command(node)
	command.Stdin = strings.NewReader(strings.ReplaceAll(fixture, "OBSERVER", params.Expression))
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("production combined menu: %v %s", err, out)
	}
}
