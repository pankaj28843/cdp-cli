package chatgpt

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Execute the production observer against the current message layout.
// The assistant role labels a heading, not the answer itself.
func TestRenderedObservationMessageLayouts(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for JavaScript observation validation")
	}
	client := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	var observation renderedObservation
	if err := observeRendered(context.Background(), newSelectionActivationSession(t, client), &observation); err != nil {
		t.Fatal(err)
	}
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(client.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	script := `const assert = require('node:assert/strict');
global.Node = {TEXT_NODE: 3, DOCUMENT_POSITION_FOLLOWING: 4};
global.HTMLElement = class {
 constructor(text = '') {
  this.innerText = this.textContent = text;
  this.childNodes = [{nodeType: 3, nodeValue: text}];
  this.dataset = {}; this.tagName = 'DIV'; this.offsetWidth = 1;
 }
 getAttribute() { return null; }
 getClientRects() { return [1]; }
 matches() { return false; }
 querySelector() { return null; }
 querySelectorAll() { return []; }
 closest() { return null; }
 compareDocumentPosition(node) { return node.followsAssistant ? 4 : 2; }
};
global.location = {pathname: '/c/synthetic-conversation'};
const prompt = new HTMLElement('Exact synthetic prompt.');
const answer = new HTMLElement('READY');
const copy = new HTMLElement();
copy.followsAssistant = true;
let copyInside = true;
let answerAvailable = true;
const unit = new HTMLElement('ChatGPT said: READY Copy');
unit.querySelector = selector => selector.includes('data-markdown-text-style') && answerAvailable ? answer : null;
unit.querySelectorAll = selector => selector === '[data-conversation-role="assistant"]' ? [heading] : selector.startsWith('button[') && copyInside ? [copy] : [];
const heading = new HTMLElement('ChatGPT said:');
heading.closest = selector => selector === '[data-content-search-unit-key]' ? unit : null;
let modern = true;
global.document = {querySelectorAll(selector) {
 if (modern) {
  if (selector === '[data-conversation-role="assistant"]') return [heading, heading];
  if (selector === '[data-user-message-bubble]') return [prompt, prompt];
 } else {
  if (selector === '[data-message-author-role="assistant"]') return [unit];
  if (selector === '[data-message-author-role="user"]') return [prompt];
 }
 return [];
}};
function observe() { return ` + params.Expression + `; }
{
 const result = observe();
 assert.equal(result.text, 'READY');
 assert.equal(result.assistant_count, 1);
 assert.equal(result.user_message_count, 1);
 assert.deepEqual(result.prompt_candidates, ['Exact synthetic prompt.']);
 assert.equal(result.route_matches, true);
 assert.equal(result.terminal_control_present, true);
 assert.equal(result.is_streaming, false);
}

modern = false;
assert.equal(observe().assistant_count, 0, 'obsolete author-role layout is not a supported answer surface');
modern = true;
answerAvailable = false;
assert.equal(observe().text, '', 'a searchable role heading without a response body is not answer text');
answerAvailable = true;
copyInside = false;
const actions = new HTMLElement();
unit.parentElement = actions;
actions.querySelectorAll = selector => selector === '[data-conversation-role="assistant"]' ? [heading] : selector.startsWith('button[') ? [copy] : [];
assert.equal(observe().completion_control_present, true, 'following assistant action bar proves completion');
copy.followsAssistant = false;
assert.equal(observe().completion_control_present, false, 'preceding user Copy cannot prove completion');
copy.followsAssistant = true;
actions.querySelectorAll = selector => selector === '[data-conversation-role="assistant"]' ? [heading, new HTMLElement()] : [copy];
assert.equal(observe().completion_control_present, false, 'another response prevents widening the scope');
heading.closest = () => null;
modern = true;
assert.equal(observe().assistant_count, 0, 'a role heading alone is not an answer');
`
	command := exec.Command(node)
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rendered message observation: %v\n%s", err, output)
	}
}

func TestWaitRenderedAnswerShortCompletion(t *testing.T) {
	const prompt = "Reply with the single word READY."
	for _, test := range []struct {
		name                                   string
		text                                   string
		terminal, streaming, wrongPrompt, want bool
	}{
		{name: "short completed answer", text: "READY", terminal: true, want: true},
		{name: "no completion evidence", text: "READY"},
		{name: "still streaming", text: "READY", terminal: true, streaming: true},
		{name: "different request", text: "READY", terminal: true, wrongPrompt: true},
		{name: "control payload", text: `{"search_query":"synthetic"}`, terminal: true},
		{name: "heading only", text: "# Answer", terminal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := prompt
			if test.wrongPrompt {
				candidate = "A different synthetic request."
			}
			value, err := json.Marshal(renderedObservation{
				RouteMatches: true, ConversationID: "synthetic-conversation",
				Text: test.text, PromptCandidates: []string{candidate},
				TerminalControl: test.terminal, CompletionControl: test.terminal, Streaming: test.streaming,
				AssistantCount: 1, UserMessageCount: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			client := &selectionActivationClient{evaluation: value}
			_, stable, _ := waitRenderedAnswer(context.Background(),
				newSelectionActivationSession(t, client), "synthetic-conversation",
				prompt, "", time.Now().Add(30*time.Millisecond), time.Millisecond)
			if got := stable > 0; got != test.want {
				t.Fatalf("terminal stable reads = %d, want completion %t", stable, test.want)
			}
		})
	}
}
