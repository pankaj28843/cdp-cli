package chatgpt

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestRenderedDetailRecoversBothBrowserReadFailureBranches(t *testing.T) {
	for _, phase := range []string{"readiness", "fetch"} {
		t.Run(phase, func(t *testing.T) {
			seenRendered := 0
			client := newAuthenticatedReadBrowser(func(expression string, _ *testsupport.Browser) (any, error) {
				switch {
				case strings.Contains(expression, "const units = [...document.querySelectorAll"):
					seenRendered++
					return map[string]any{"conversation_id": "synthetic-conversation", "route_matches": true,
						"user_count": 1, "assistant_count": 1, "ordered": true,
						"text": "A complete synthetic answer.", "terminal": true, "streaming": false}, nil
				case strings.Contains(expression, "signed_in:"):
					if phase == "readiness" {
						return nil, errors.New("synthetic readiness failure")
					}
					return map[string]any{"signed_in": true, "signed_out": false}, nil
				case strings.Contains(expression, "const response = await fetch"):
					return map[string]any{"ok": false, "status_code": 401, "body": "", "error": ""}, nil
				default:
					return map[string]any{}, nil
				}
			})
			engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			result := DetailConversation(context.Background(), ReadConfig{
				Store: newReadTestStore(t, now), HTTPClient: fixedHTTPClient(http.StatusForbidden, ""),
				BrowserConfig: &BrowserConfig{Client: client, Engine: engine, Journal: journal},
				Now:           func() time.Time { return now },
			}, "synthetic-conversation")
			data, ok := result.Data.(ConversationDetailData)
			if !ok || !result.OK || result.State != webagent.StateTerminal ||
				data.Text != "A complete synthetic answer." || data.ReadMode != "headed_browser_rendered" ||
				data.StatusCode != 0 || data.Metadata["rendered_terminal_stable_reads"] != 2 ||
				result.Cleanup.State != webagent.CleanupClosed || !result.Cleanup.TargetClosed ||
				result.Evidence.Target.TargetID == "user-page" {
				t.Fatalf("%s recovery rendered=%d: %+v data=%+v", phase, seenRendered, result, data)
			}
		})
	}
}

func TestRenderedDetailStableReads(t *testing.T) {
	previous, count := nextRenderedDetailStability("", 0, "first")
	if previous != "first" || count != 1 {
		t.Fatal(previous, count)
	}
	previous, count = nextRenderedDetailStability(previous, count, "first")
	if count != 2 {
		t.Fatal("identical terminal read was not counted")
	}
	previous, count = nextRenderedDetailStability(previous, count, "changed")
	if previous != "changed" || count != 1 {
		t.Fatal("changed answer retained stale stability")
	}
}

func TestRenderedDetailSuccessClosesOnlyOwnedTarget(t *testing.T) {
	client := testsupport.NewBrowser("preexisting-user-page")
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	config := ReadConfig{BuildCommit: "synthetic"}
	conversation := conversationRef("synthetic-conversation")
	result := runOwned(context.Background(), BrowserConfig{Client: client, Engine: engine, Journal: journal,
		BuildCommit: config.BuildCommit}, "synthetic-rendered-detail", webagent.OperationConversationsDetail,
		"", "about:blank", "headed_browser_rendered", nil,
		func(lease *browserflow.Lease, target *webagent.TargetEvidence, pending webagent.CleanupEvidence) webagent.Result {
			if err := lease.MarkPrepared(context.Background()); err != nil {
				t.Fatal(err)
			}
			data := newConversationDetailData(conversation.ID, "headed_browser_rendered", "")
			data.Text = "Synthetic complete answer"
			data.CompletionState = conversationCompletionTerminal
			return renderedDetailSuccess(context.Background(), lease, "synthetic-rendered-detail", config,
				webagent.OperationConversationsDetail, target, pending, conversation, data, false)
		})
	if !result.OK || result.State != webagent.StateTerminal || result.Cleanup.State != webagent.CleanupClosed ||
		!result.Cleanup.TargetClosed || !result.Evidence.Target.Closed || result.Evidence.Target.TargetID == "preexisting-user-page" {
		t.Fatalf("owned cleanup or terminal state failed: %+v", result)
	}
}

func TestRenderedDetailRequiresExactIdentityAndTerminal(t *testing.T) {
	base := renderedDetail{ConversationID: "expected", RouteMatches: true,
		UserCount: 1, AssistantCount: 1, Ordered: true,
		Text: "A complete synthetic answer.", Terminal: true}
	if !validRenderedDetail(base, "expected") {
		t.Fatal("valid answer rejected")
	}
	for _, mutate := range []func(*renderedDetail){
		func(v *renderedDetail) { v.ConversationID = "other" },
		func(v *renderedDetail) { v.RouteMatches = false },
		func(v *renderedDetail) { v.UserCount = 0 },
		func(v *renderedDetail) { v.AssistantCount = 0 },
		func(v *renderedDetail) { v.Ordered = false },
		func(v *renderedDetail) { v.Text = "  " },
		func(v *renderedDetail) { v.Terminal = false },
		func(v *renderedDetail) { v.Streaming = true },
	} {
		candidate := base
		mutate(&candidate)
		if validRenderedDetail(candidate, "expected") {
			t.Fatalf("accepted invalid observation: %+v", candidate)
		}
	}
}

func TestRenderedDetailDOMMessageOrder(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for DOM fixture")
	}
	script := `const assert = require('node:assert/strict');
global.Node = {DOCUMENT_POSITION_FOLLOWING: 4};
global.location = {pathname: '/c/expected'};
const answer = {innerText: 'A complete synthetic answer.'};
const user = {querySelector: s => s === '[data-user-message-bubble]' ? {innerText:'Exact synthetic prompt.'} : null,
 compareDocumentPosition: () => 4};
const assistant = {querySelector: s => s === '[data-user-message-bubble]' ? null :
  s === '.markdown,[data-markdown-text-style]' ? answer : null,
 querySelectorAll: () => []};
let units = [user, assistant], stop = false, latest = true;
global.document = {querySelectorAll: selector => {
 if (selector === '[data-content-search-unit-key]') return units;
 if (selector.startsWith('button[data-testid="stop-button"]')) return stop ? [{disabled:false,getAttribute:()=>null,offsetWidth:1}] : [];
 return [];
}, querySelector: selector => selector === 'main' ? {innerText: latest ? 'Latest response' : ''} : null};
function observe() { return ` + renderedDetailExpression + `; }
let result = observe();
assert.equal(result.text, 'A complete synthetic answer.');
assert.equal(result.prompt, 'Exact synthetic prompt.');
assert.equal(result.ordered, true);
assert.equal(result.terminal, true);
units = [assistant, user]; result = observe();
assert.equal(result.ordered, false);
units = [user, assistant]; stop = true; result = observe();
assert.equal(result.streaming, true);
assert.equal(result.terminal, false);
stop = false; latest = false; result = observe();
assert.equal(result.terminal, false);
`
	command := exec.Command(node)
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rendered detail DOM fixture: %v\n%s", err, output)
	}
}
