package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type preparationBoundaryClient struct {
	lateSelectionClient
	mode string
}

func (c *preparationBoundaryClient) CallSession(ctx context.Context, session, method string, params any, result any) error {
	if method == "Input.insertText" && c.mode == "input-error" {
		c.insertCount++
		return fmt.Errorf("synthetic insertion acknowledgement lost")
	}
	raw, _ := json.Marshal(params)
	expression := string(raw)
	if method == "Runtime.evaluate" && strings.Contains(expression, "expected_characters") {
		o := composerObservation{DocumentReadyState: "complete", RouteReady: true, EditorReady: true, EditorCount: 1, ChatCount: 1, WorkCount: 1, ChatSelected: true, PromptMatches: true, IntelligenceCount: 1, SelectedIntelligence: "High", SendCount: 1, SendReady: true}
		o.ConversationRouteReady = c.conversationID != ""
		o.ConversationID = c.conversationID
		o.RouteReady = c.conversationID == ""
		o.UserMessageCount = c.userCount
		o.AssistantCount = c.assistantCount
		if c.mode == "ambiguous" {
			o.DocumentReadyState = ""
		}
		if c.insertCount > 0 && (c.mode == "partial" || c.mode == "input-error") {
			o.PromptMatches = false
		}
		if c.insertCount > 0 && c.mode != "normal" && c.mode != "partial" && c.mode != "input-error" {
			o.RouteReady = false
			if c.conversationID == "" {
				o.ConversationID = "observed-conversation"
			}
			o.UserMessageCount = c.userCount + 1
			o.AssistantCount = c.assistantCount + 1
		}
		return decodeSelectionActivationResult(result, map[string]any{"result": map[string]any{"value": o}})
	}
	if method == "Runtime.evaluate" && strings.Contains(expression, "prompt_candidates") {
		prompt := "synthetic exact prompt"
		if c.mode == "mismatch" {
			prompt = "a different prompt"
		}
		id := c.conversationID
		if id == "" {
			id = "observed-conversation"
		}
		o := renderedObservation{RouteMatches: c.mode != "route", ConversationID: id, UserMessageCount: c.userCount + 1, AssistantCount: c.assistantCount + 1, PromptCandidates: []string{prompt}}
		return decodeSelectionActivationResult(result, map[string]any{"result": map[string]any{"value": o}})
	}
	return c.lateSelectionClient.CallSession(ctx, session, method, params, result)
}

func TestPreparationUnexpectedTurnStopsBeforeRefillOrSend(t *testing.T) {
	for _, mode := range []string{"matching", "mismatch", "route"} {
		t.Run(mode, func(t *testing.T) {
			c := &preparationBoundaryClient{mode: mode}
			_, _, err := prepareVerifiedPrompt(context.Background(), newSelectionActivationSession(t, c), "synthetic exact prompt", "High", "", nil, time.Second, time.Millisecond)
			boundary, ok := preparationRecovery(err)
			if !ok || c.insertCount != 1 {
				t.Fatalf("err=%v insertions=%d", err, c.insertCount)
			}
			if mode == "mismatch" && boundary.MatchingPrompt {
				t.Fatal("different prompt granted known recovery")
			}
			if mode == "matching" && (!boundary.MatchingPrompt || boundary.ConversationID != "observed-conversation") {
				t.Fatalf("missing exact recovery: %+v", boundary)
			}
			c.assertNoRawSend(t)
		})
	}
}
func TestPreparationAmbiguousBaselinePreventsInput(t *testing.T) {
	c := &preparationBoundaryClient{mode: "ambiguous"}
	_, _, err := prepareVerifiedPrompt(context.Background(), newSelectionActivationSession(t, c), "synthetic exact prompt", "High", "", nil, time.Second, time.Millisecond)
	if _, ok := preparationRecovery(err); !ok || c.insertCount != 0 {
		t.Fatalf("err=%v insertions=%d", err, c.insertCount)
	}
	c.assertNoRawSend(t)
}
func TestPreparationBaselineIdentityIsExact(t *testing.T) {
	o := composerObservation{DocumentReadyState: "complete", EditorReady: true, EditorCount: 1, ConversationRouteReady: true, ConversationID: "retained", UserMessageCount: 3, AssistantCount: 2}
	if !preparationBaselineUnchanged(o, "retained", 3, 2) {
		t.Fatal("unchanged continuation rejected")
	}
	for _, alter := range []func(*composerObservation){func(v *composerObservation) { v.ConversationID = "other" },
		func(v *composerObservation) { v.ConversationRouteReady = false }, func(v *composerObservation) { v.UserMessageCount++ }, func(v *composerObservation) { v.AssistantCount++ }, func(v *composerObservation) { v.EditorCount = 2 }, func(v *composerObservation) { v.DocumentReadyState = "loading" }} {
		v := o
		alter(&v)
		if preparationBaselineUnchanged(v, "retained", 3, 2) {
			t.Fatal("changed baseline accepted")
		}
	}
}

func TestPreparationUnchangedBaselineAllowsNormalInput(t *testing.T) {
	c := &preparationBoundaryClient{mode: "normal"}
	_, o, err := prepareVerifiedPrompt(context.Background(), newSelectionActivationSession(t, c), "synthetic exact prompt", "High", "", nil, time.Second, time.Millisecond)
	if err != nil || c.insertCount != 1 || !composerReadyForSend(o, "High") {
		t.Fatalf("err=%v insertions=%d observation=%+v", err, c.insertCount, o)
	}
	c.assertNoRawSend(t)
}
func TestPreparationObserverCountsModernTurns(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	client := &selectionActivationClient{evaluation: json.RawMessage(`{}`)}
	var o composerObservation
	if err := observeComposer(context.Background(), newSelectionActivationSession(t, client), "synthetic", "High", &o); err != nil {
		t.Fatal(err)
	}
	var params struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(client.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	start := strings.Index(params.Expression, "const assistants =")
	end := strings.Index(params.Expression[start:], "const specialized") + start
	if start < 0 || end <= start {
		t.Fatal("modern turn observer missing")
	}
	script := `const assert=require('node:assert/strict');
 const turn={};const heading={closest:()=>turn};const bubble={};
 const document={querySelectorAll:selector=>{
 if(selector==='[data-conversation-role="assistant"]')return [heading];
 if(selector==='[data-user-message-bubble]')return [bubble];
 throw new Error('unexpected or stale turn selector '+selector);
 }};
 ` + params.Expression[start:end] + `
 assert.equal(assistants.length,1);assert.equal(users.length,1);`
	cmd := exec.Command(node)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual modern observer: %v %s", err, out)
	}
}

func TestPreparationContinuationUnexpectedTurnStopsRefill(t *testing.T) {
	c := &preparationBoundaryClient{mode: "matching", lateSelectionClient: lateSelectionClient{conversationID: "retained", userCount: 2, assistantCount: 2}}
	_, _, err := prepareVerifiedContinuationPrompt(context.Background(), newSelectionActivationSession(t, c), "retained", "synthetic exact prompt", 2, 2, "High", "", time.Second, time.Millisecond)
	boundary, ok := preparationRecovery(err)
	if !ok || c.insertCount != 1 || !boundary.MatchingPrompt || boundary.ConversationID != "retained" {
		t.Fatalf("err=%v inserts=%d recovery=%+v", err, c.insertCount, boundary)
	}
	c.assertNoRawSend(t)
}

func TestPreparationPartialPromptDoesNotRefill(t *testing.T) {
	c := &preparationBoundaryClient{mode: "partial"}
	_, _, err := prepareVerifiedPrompt(context.Background(), newSelectionActivationSession(t, c), "synthetic exact prompt", "High", "", nil, time.Second, time.Millisecond)
	if _, ok := preparationRecovery(err); !ok || c.insertCount != 1 {
		t.Fatalf("err=%v inserts=%d", err, c.insertCount)
	}
	c.assertNoRawSend(t)
}

func TestPreparationInsertTextErrorNeverRefills(t *testing.T) {
	for _, continuation := range []bool{false, true} {
		t.Run(fmt.Sprint(continuation), func(t *testing.T) {
			c := &preparationBoundaryClient{mode: "input-error"}
			var err error
			if continuation {
				c.conversationID = "retained"
				c.userCount = 2
				c.assistantCount = 2
				_, _, err = prepareVerifiedContinuationPrompt(context.Background(), newSelectionActivationSession(t, c), "retained", "synthetic exact prompt", 2, 2, "High", "", time.Second, time.Millisecond)
			} else {
				_, _, err = prepareVerifiedPrompt(context.Background(), newSelectionActivationSession(t, c), "synthetic exact prompt", "High", "", nil, time.Second, time.Millisecond)
			}
			if _, ok := preparationRecovery(err); !ok || c.insertCount != 1 {
				t.Fatalf("err=%v attempted insertions=%d", err, c.insertCount)
			}
			c.assertNoRawSend(t)
		})
	}
}
