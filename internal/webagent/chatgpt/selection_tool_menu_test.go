package chatgpt

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestToolMenuActivationExactAccessibleControl(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for production selection expression regression")
	}
	client := &selectionActivationClient{evaluation: json.RawMessage(`{"ok":false,"count":0,"activated":false}`)}
	_ = activateSelectionControl(context.Background(), newSelectionActivationSession(t, client), "tool-menu", "")
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(client.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	script := `const assert = require('node:assert/strict');
class HTMLElement {
 constructor(name, id='') { this.composer={querySelectorAll:()=>[new HTMLElement('editor','editor')]}; this.name=name; this.id=id; this.disabled=false; this.events=[]; this.hidden=false; }
 getAttribute(key) { return key==='aria-label' ? this.name : null; }
 getBoundingClientRect() { return {left:10,right:54,top:10,bottom:54,width:44,height:44}; }
 contains(other) { return other===this; }
 closest() { return this.composer; }
 focus() {}
 dispatchEvent(event) { this.events.push(event.type); }
}
class MouseEvent { constructor(type) { this.type=type; } }
class PointerEvent extends MouseEvent {}
const window={innerWidth:800,innerHeight:600};
const getComputedStyle=element=>({display:element.hidden?'none':'block',visibility:'visible',opacity:'1'});
let buttons=[], hit=null;
const document={querySelectorAll:selector=>{
 assert.equal(selector,'button#composer-plus-btn,button[aria-label="Add files and more"]');
 return buttons.filter(button=>button.id==='composer-plus-btn'||button.name==='Add files and more');
},elementFromPoint:()=>hit};
async function activate() { return ` + params.Expression + `; }
(async()=>{
 const current=new HTMLElement('Add files and more');
 const unrelated=new HTMLElement('Add files');
 buttons=[unrelated,current]; hit=current;
 assert.deepEqual(await activate(),{ok:true,count:1,activated:true});
 assert.equal(current.events.filter(type=>type==='click').length,1);
 assert.equal(unrelated.events.length,0);
 current.disabled=true; assert.equal((await activate()).count,0); current.disabled=false;
 current.hidden=true; assert.equal((await activate()).count,0); current.hidden=false;
 const duplicate=new HTMLElement('Add files and more');
 buttons=[current,duplicate]; assert.equal((await activate()).count,2); assert.equal(duplicate.events.length,0);
 const legacy=new HTMLElement('', 'composer-plus-btn');
 buttons=[legacy]; hit=legacy; assert.equal((await activate()).activated,true);
 buttons=[current]; hit=unrelated; assert.equal((await activate()).activated,false);
 buttons=[unrelated]; assert.equal((await activate()).count,0);
 const foreign=new HTMLElement('Add files and more');foreign.composer=null;buttons=[foreign];hit=foreign;assert.equal((await activate()).count,0);
 buttons=[foreign,current];hit=current;assert.equal((await activate()).count,1);
})().catch(error=>{console.error(error);process.exit(1)});`
	command := exec.Command(node)
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("production tool-menu expression: %v\n%s", err, output)
	}
}
