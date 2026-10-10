package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"os/exec"
	"testing"
)

func TestContinuationImageRequiresExactNewTurn(t *testing.T) {
	prompt := "Draw a lighthouse"
	ready := renderedObservation{RouteMatches: true, ConversationID: "example-id", PromptCandidates: []string{prompt}, UserMessageCount: 3, AssistantCount: 3, GeneratedImageReady: true, GeneratedImageWidth: 1024, GeneratedImageHeight: 1024}
	if !continuationAnswerReady(ready, "example-id", prompt, ToolCreateImage, 3, 3) {
		t.Fatal("exact attachment-only answer rejected")
	}
	cases := map[string]func(*renderedObservation){
		"old assistant":      func(o *renderedObservation) { o.AssistantCount = 2 },
		"other conversation": func(o *renderedObservation) { o.ConversationID = "other-id" },
		"unknown prompt":     func(o *renderedObservation) { o.PromptCandidates = []string{"other"} },
		"streaming":          func(o *renderedObservation) { o.Streaming = true },
		"pending image":      func(o *renderedObservation) { o.GeneratedImageReady = false; o.TerminalControl = true; o.Text = "Edit" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o := ready
			mutate(&o)
			if continuationAnswerReady(o, "example-id", prompt, ToolCreateImage, 3, 3) {
				t.Fatal("accepted unproven image terminal")
			}
		})
	}
	if continuationAnswerReady(ready, "example-id", prompt, "", 3, 3) {
		t.Fatal("image controls accepted as text answer")
	}
}

func TestContinuationToolGuardRejectsChangedSelection(t *testing.T) {
	observed := composerObservation{SelectedTool: toolDisplayLabel(ToolCreateImage)}
	if !continuationToolMatches(observed, ToolCreateImage) {
		t.Fatal("selected tool rejected")
	}
	observed.SelectedTool = toolDisplayLabel(ToolWebSearch)
	if continuationToolMatches(observed, ToolCreateImage) {
		t.Fatal("changed tool accepted")
	}
}

func TestContinuationUnsupportedToolFailsBeforeBrowser(t *testing.T) {
	result := ContinueConversation(context.Background(), ContinueConfig{Tool: "unverified-tool"}, "example-id", "draw")
	if result.Error == nil || result.Error.Code != "chatgpt_tool_invalid" {
		t.Fatalf("expected usage failure, got %#v", result)
	}
}

func TestContinuationChangedToolNeverDispatchesInput(t *testing.T) {
	client := &selectionActivationClient{evaluations: []json.RawMessage{
		exactSelectionGuard("Pro", ""),
		json.RawMessage(`{"editor_ready":true,"editor_count":1,"prompt_matches":true,"intelligence_count":1,"selected_intelligence":"Pro","send_count":1,"send_ready":true,"assistant_count":2,"user_message_count":2,"conversation_id":"example-id","selected_tool":"Search the web"}`),
	}}
	dispatcher := chatgptContinueDispatcher{prompt: "draw", conversationID: "example-id", baselineUsers: 2, baselineAssistants: 2, intelligence: "Pro", tool: ToolCreateImage}
	outcome, err := dispatcher.Dispatch(context.Background(), newSelectionActivationSession(t, client))
	if err == nil || outcome.Dispatch != browserflow.DispatchNotPerformed || outcome.RawInputAttempted {
		t.Fatalf("changed tool reached Send: %+v %v", outcome, err)
	}
	for _, call := range client.calls {
		if call.method != "Runtime.evaluate" {
			t.Fatalf("unexpected input: %s", call.method)
		}
	}
}

func TestRenderedContinuationNeverBorrowsAnOlderImage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for actual observation expression regression")
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
	for _, currentReady := range []bool{false, true} {
		fixture := fmt.Sprintf(`
class Element {
 constructor(images=[],text=''){this.images=images;this.innerText=text;this.textContent=text;this.childNodes=[];this.dataset={};this.offsetWidth=100;this.offsetHeight=100;this.parentElement=null;}
 getAttribute(){return null;} getClientRects(){return [1];} matches(){return false;}
 closest(){return this;} querySelector(s){return s.includes('Generated image') ? this.images[0]||null : null;}
 querySelectorAll(s){return s.includes('Generated image') ? this.images : [];}
}
global.HTMLElement=Element;global.Node={TEXT_NODE:3,DOCUMENT_POSITION_FOLLOWING:4};
const oldImage=new Element();oldImage.complete=true;oldImage.naturalWidth=800;oldImage.naturalHeight=600;
const newImage=new Element();newImage.complete=%t;newImage.naturalWidth=%t?1024:0;newImage.naturalHeight=1024;
const assistants=[new Element([oldImage]),new Element([newImage])];
const users=[new Element([], 'old prompt'),new Element([], 'draw')];
global.document={querySelectorAll(s){if(s.includes('data-conversation-role'))return assistants;if(s.includes('data-user-message-bubble'))return users;return [];}};
global.location={origin:'https://chatgpt.com',pathname:'/c/example-id'};
console.log(JSON.stringify(%s));`, currentReady, currentReady, params.Expression)
		output, err := exec.Command(node, "-e", fixture).CombinedOutput()
		if err != nil {
			t.Fatalf("actual observation failed: %v %s", err, output)
		}
		var observed renderedObservation
		if err := json.Unmarshal(output, &observed); err != nil {
			t.Fatal(err)
		}
		got := continuationAnswerReady(observed, "example-id", "draw", ToolCreateImage, 2, 2)
		if got != currentReady {
			t.Fatalf("latest ready=%v observed=%+v terminal=%v", currentReady, observed, got)
		}
	}
}
