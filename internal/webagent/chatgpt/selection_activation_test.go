package chatgpt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestActivateSelectionControlUsesOneIdentityBoundEvaluation(t *testing.T) {
	client := &selectionActivationClient{
		evaluation: json.RawMessage(
			`{"ok":true,"count":1,"activated":true}`,
		),
	}
	session := newSelectionActivationSession(t, client)

	if err := activateSelectionControl(
		context.Background(),
		session,
		"picker",
		"Extra High",
	); err != nil {
		t.Fatalf("activateSelectionControl: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %+v, want one identity-bound activation", client.calls)
	}
	if client.calls[0].method != "Runtime.evaluate" {
		t.Fatalf("first call = %q, want Runtime.evaluate", client.calls[0].method)
	}
	evaluation := string(client.calls[0].params)
	if strings.Contains(evaluation, "control.click()") ||
		!strings.Contains(evaluation, "Extra High") ||
		!strings.Contains(evaluation, "elementFromPoint") ||
		!strings.Contains(evaluation, "new PointerEvent('pointerdown'") ||
		!strings.Contains(evaluation, "control.dispatchEvent") {
		t.Fatalf("selection observation expression = %s", evaluation)
	}
	for _, call := range client.calls {
		if call.method == "Input.dispatchMouseEvent" ||
			call.method == "Input.dispatchKeyEvent" {
			t.Fatalf(
				"selection activation escaped exact DOM identity through %q",
				call.method,
			)
		}
	}
}

func TestActivateSelectionControlDoesNotDispatchOnIdentityMiss(t *testing.T) {
	client := &selectionActivationClient{
		evaluation: json.RawMessage(
			`{"ok":false,"count":2,"x":-1,"y":-1}`,
		),
	}
	session := newSelectionActivationSession(t, client)

	err := activateSelectionControl(
		context.Background(),
		session,
		"option",
		"Pro",
	)
	if err == nil || !strings.Contains(err.Error(), "count was 2") {
		t.Fatalf("error = %v, want exact-count failure", err)
	}
	if len(client.calls) != 1 ||
		client.calls[0].method != "Runtime.evaluate" {
		t.Fatalf("calls = %+v, want observation only", client.calls)
	}
}

func TestVerifySelectionAtSendAcceptsCurrentSliderWithoutOpeningMenu(t *testing.T) {
	client := &selectionActivationClient{
		evaluation: json.RawMessage(`{
			"picker_count": 1,
			"picker": {"ready": true},
			"selected_thinking": "Extra High",
			"thinking_menu_open": false,
			"model_menu_open": false
		}`),
	}
	session := newSelectionActivationSession(t, client)

	if err := verifySelectionAtSend(
		context.Background(),
		session,
		"Extra High",
		"",
		time.Second,
		time.Millisecond,
	); err != nil {
		t.Fatalf("verifySelectionAtSend: %v", err)
	}
	if len(client.calls) != 1 || client.calls[0].method != "Runtime.evaluate" {
		t.Fatalf("calls = %+v, want one passive selection observation", client.calls)
	}
}

func TestActivateChatGPTToolUsesVisiblePlusMenuIdentity(t *testing.T) {
	client := &selectionActivationClient{
		evaluation: json.RawMessage(
			`{"ok":true,"count":1,"activated":true}`,
		),
	}
	session := newSelectionActivationSession(t, client)

	if err := activateSelectionControl(
		context.Background(),
		session,
		"tool",
		"Create image",
	); err != nil {
		t.Fatalf("activateSelectionControl: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls = %+v, want one identity-bound activation", client.calls)
	}
	evaluation := string(client.calls[0].params)
	for _, required := range []string{
		"div[tabindex",
		"data-fill",
		"Create image",
		"elementFromPoint",
	} {
		if !strings.Contains(evaluation, required) {
			t.Fatalf("tool activation expression missing %q: %s", required, evaluation)
		}
	}
}

func TestPrepareExactPromptActivatesEditorBeforeTextInput(t *testing.T) {
	client := &selectionActivationClient{
		evaluations: []json.RawMessage{
			json.RawMessage(`{"ok":true,"count":1,"activated":true}`),
			json.RawMessage(`{"ok":true}`),
		},
	}
	session := newSelectionActivationSession(t, client)

	if err := prepareExactPrompt(
		context.Background(),
		session,
		"current prompt",
	); err != nil {
		t.Fatalf("prepareExactPrompt: %v", err)
	}
	wantMethods := []string{
		"Runtime.evaluate",
		"Runtime.evaluate",
		"Input.insertText",
	}
	if len(client.calls) != len(wantMethods) {
		t.Fatalf("calls = %+v, want %v", client.calls, wantMethods)
	}
	for index, want := range wantMethods {
		if client.calls[index].method != want {
			t.Fatalf(
				"call %d = %q, want %q",
				index,
				client.calls[index].method,
				want,
			)
		}
	}
}

func TestPrepareExactPromptWithToolActivatesSelectedEditorBeforeTextInput(t *testing.T) {
	client := &selectionActivationClient{
		evaluations: []json.RawMessage{
			json.RawMessage(`{"ok":true,"count":1,"activated":true}`),
			json.RawMessage(`{"ok":true}`),
		},
	}
	session := newSelectionActivationSession(t, client)

	if err := prepareExactPromptWithTool(
		context.Background(),
		session,
		"current prompt",
		ToolCreateImage,
	); err != nil {
		t.Fatalf("prepareExactPromptWithTool: %v", err)
	}
	if len(client.calls) != 3 || client.calls[0].method != "Runtime.evaluate" ||
		client.calls[1].method != "Runtime.evaluate" ||
		client.calls[2].method != "Input.insertText" {
		t.Fatalf("calls = %+v, want two identity checks then text input", client.calls)
	}
	evaluation := string(client.calls[1].params)
	for _, required := range []string{
		"data-inline-selection-pill",
		"Create image",
		"range.collapse(false)",
	} {
		if !strings.Contains(evaluation, required) {
			t.Fatalf("tool editor expression missing %q: %s", required, evaluation)
		}
	}
}

func TestPrepareSelectionGuardLeavesExactModelObservableAtSend(t *testing.T) {
	closed := json.RawMessage(`{
		"picker_count":1,
		"picker":{"ready":true},
		"selected_thinking":"Pro",
		"thinking_menu_open":false,
		"model_menu_open":false
	}`)
	open := json.RawMessage(`{
		"picker_count":1,
		"picker":{"ready":true},
		"selected_thinking":"Pro",
		"thinking_menu_open":true,
		"thinking_options":[
			{"label":"Pro","checked":true,"ready":true}
		],
		"model_trigger_count":1,
		"model_trigger_label":"GPT-5.6 Sol",
		"model_menu_open":false
	}`)
	client := &selectionActivationClient{
		evaluations: []json.RawMessage{
			closed,
			closed,
			json.RawMessage(`{"ok":true,"count":1,"activated":true}`),
			open,
			open,
		},
	}
	session := newSelectionActivationSession(t, client)

	if err := prepareSelectionGuardAtSend(
		context.Background(),
		session,
		"Pro",
		"GPT-5.6 Sol",
		time.Second,
		time.Millisecond,
	); err != nil {
		t.Fatalf("prepareSelectionGuardAtSend: %v", err)
	}
	if err := observeSelectionGuardAtSend(
		context.Background(),
		session,
		"Pro",
		"GPT-5.6 Sol",
	); err != nil {
		t.Fatalf("observeSelectionGuardAtSend: %v", err)
	}
	if len(client.calls) != 5 {
		t.Fatalf("calls=%+v, want four preparation calls and one observation", client.calls)
	}
	for _, call := range client.calls {
		if call.method != "Runtime.evaluate" {
			t.Fatalf("call=%+v, selection guard must avoid raw input", call)
		}
	}
	activation := string(client.calls[2].params)
	if !strings.Contains(activation, "const kind") ||
		!strings.Contains(activation, "picker") {
		t.Fatal("third call did not activate the exact picker")
	}
}

type selectionActivationCall struct {
	method string
	params json.RawMessage
}

type selectionActivationClient struct {
	evaluation      json.RawMessage
	evaluations     []json.RawMessage
	evaluationIndex int
	calls           []selectionActivationCall
}

func (c *selectionActivationClient) Call(
	_ context.Context,
	method string,
	_ any,
	result any,
) error {
	if method == "Target.attachToTarget" {
		return decodeSelectionActivationResult(
			result,
			map[string]any{"sessionId": "selection-session"},
		)
	}
	return nil
}

func (c *selectionActivationClient) CallSession(
	_ context.Context,
	_ string,
	method string,
	params any,
	result any,
) error {
	raw, _ := json.Marshal(params)
	c.calls = append(c.calls, selectionActivationCall{
		method: method,
		params: raw,
	})
	if method == "Runtime.evaluate" {
		evaluation := c.evaluation
		if c.evaluationIndex < len(c.evaluations) {
			evaluation = c.evaluations[c.evaluationIndex]
			c.evaluationIndex++
		}
		return decodeSelectionActivationResult(
			result,
			map[string]any{
				"result": map[string]any{
					"type":  "object",
					"value": evaluation,
				},
			},
		)
	}
	if result != nil {
		return json.Unmarshal([]byte(`{}`), result)
	}
	return nil
}

func newSelectionActivationSession(
	t *testing.T,
	client cdp.CommandClient,
) *cdp.PageSession {
	t.Helper()
	session, err := cdp.AttachToTargetWithClient(
		context.Background(),
		client,
		"selection-target",
		nil,
	)
	if err != nil {
		t.Fatalf("AttachToTargetWithClient: %v", err)
	}
	if concrete, ok := client.(*selectionActivationClient); ok {
		concrete.calls = nil
	}
	return session
}

func decodeSelectionActivationResult(result any, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}
