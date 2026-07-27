package chatgpt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
)

func TestSendDispatcherFailsClosedWhenAttachmentDropsBeforeSend(t *testing.T) {
	client := &selectionActivationClient{
		evaluations: []json.RawMessage{
			exactSelectionGuard("Pro", ""),
			json.RawMessage(`{
				"ok":false,
				"input_match":true,
				"rendered_attachment_added":false,
				"rendered_name_match":false,
				"rendered_attachment_count":0,
				"duplicate_rejected":false,
				"processing":false
			}`),
		},
	}
	session := newSelectionActivationSession(t, client)
	dispatcher := chatgptSendDispatcher{
		prompt:       "review the attached diff",
		intelligence: "Pro",
		attachment: &attachmentExpectation{
			Name:                     "review.zip",
			PreflightAttachmentCount: 0,
		},
	}

	outcome, err := dispatcher.Dispatch(context.Background(), session)
	if err == nil ||
		!strings.Contains(err.Error(), "attachment was not retained") ||
		outcome.Dispatch != browserflow.DispatchNotPerformed ||
		outcome.RawInputAttempted {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	if len(client.calls) != 2 {
		t.Fatalf(
			"calls=%+v, want only selection and failed attachment observations",
			client.calls,
		)
	}
	for _, call := range client.calls {
		if call.method != "Runtime.evaluate" {
			t.Fatalf("call=%+v, raw Send must remain not performed", call)
		}
	}
}

func TestSendDispatcherFailsClosedWhenAttachmentProcessingResumes(t *testing.T) {
	client := &selectionActivationClient{
		evaluations: []json.RawMessage{
			exactSelectionGuard("Pro", ""),
			json.RawMessage(`{
				"ok":false,
				"input_match":true,
				"rendered_attachment_added":true,
				"rendered_name_match":true,
				"rendered_name":"review.zip",
				"rendered_attachment_count":1,
				"duplicate_rejected":false,
				"processing":true
			}`),
		},
	}
	session := newSelectionActivationSession(t, client)
	dispatcher := chatgptSendDispatcher{
		prompt:       "review the attached diff",
		intelligence: "Pro",
		attachment: &attachmentExpectation{
			Name:                     "review.zip",
			PreflightAttachmentCount: 0,
		},
	}

	outcome, err := dispatcher.Dispatch(context.Background(), session)
	if err == nil ||
		outcome.Dispatch != browserflow.DispatchNotPerformed ||
		outcome.RawInputAttempted {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	for _, call := range client.calls {
		if call.method != "Runtime.evaluate" {
			t.Fatalf("call=%+v, processing attachment must suppress Send", call)
		}
	}
}

func TestSendDispatcherFailsClosedWhenProviderRenamesAttachment(t *testing.T) {
	client := &selectionActivationClient{
		evaluations: []json.RawMessage{
			exactSelectionGuard("Pro", ""),
			json.RawMessage(`{
				"ok":true,
				"input_match":true,
				"rendered_attachment_added":true,
				"rendered_name_match":true,
				"rendered_name":"review (1).zip",
				"rendered_attachment_count":1,
				"duplicate_rejected":false,
				"processing":false
			}`),
		},
	}
	session := newSelectionActivationSession(t, client)
	dispatcher := chatgptSendDispatcher{
		prompt:       "review the attached diff",
		intelligence: "Pro",
		attachment: &attachmentExpectation{
			Name:                     "review.zip",
			PreflightAttachmentCount: 0,
		},
	}

	outcome, err := dispatcher.Dispatch(context.Background(), session)
	if err == nil ||
		!strings.Contains(err.Error(), "attachment was not retained") ||
		outcome.Dispatch != browserflow.DispatchNotPerformed ||
		outcome.RawInputAttempted {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	for _, call := range client.calls {
		if call.method != "Runtime.evaluate" {
			t.Fatalf("call=%+v, provider rename must suppress raw Send", call)
		}
	}
}

func TestSendDispatcherAllowsClearedInputBeforeRawSend(t *testing.T) {
	client := &selectionActivationClient{
		evaluations: []json.RawMessage{
			exactSelectionGuard("Pro", ""),
			json.RawMessage(`{
				"ok":true,
				"input_match":false,
				"rendered_attachment_added":true,
				"rendered_name_match":true,
				"rendered_name":"review.zip",
				"rendered_attachment_count":1,
				"duplicate_rejected":false,
				"processing":false
			}`),
			json.RawMessage(`{
				"route_ready":true,
				"editor_ready":true,
				"editor_count":1,
				"prompt_matches":true,
				"chat_count":1,
				"work_count":1,
				"chat_selected":true,
				"intelligence_count":1,
				"selected_intelligence":"Pro",
				"send_count":1,
				"send_ready":true,
				"send_x":100,
				"send_y":200,
				"assistant_count":0,
				"user_message_count":0,
				"conversation_id":""
			}`),
		},
	}
	session := newSelectionActivationSession(t, client)
	dispatcher := chatgptSendDispatcher{
		prompt:       "review the attached diff",
		intelligence: "Pro",
		attachment: &attachmentExpectation{
			Name:                     "review.zip",
			PreflightAttachmentCount: 0,
		},
	}

	outcome, err := dispatcher.Dispatch(context.Background(), session)
	if err != nil ||
		outcome.Dispatch != browserflow.DispatchPerformed ||
		!outcome.RawInputAttempted {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	wantMethods := []string{
		"Runtime.evaluate",
		"Runtime.evaluate",
		"Runtime.evaluate",
		"Input.dispatchMouseEvent",
		"Input.dispatchMouseEvent",
		"Input.dispatchMouseEvent",
	}
	if len(client.calls) != len(wantMethods) {
		t.Fatalf("calls=%+v, want %v", client.calls, wantMethods)
	}
	for index, want := range wantMethods {
		if client.calls[index].method != want {
			t.Fatalf(
				"call %d=%q, want %q; final coordinates must be adjacent to Send",
				index,
				client.calls[index].method,
				want,
			)
		}
	}
	if !strings.Contains(
		string(client.calls[3].params),
		`"x":100`,
	) || !strings.Contains(
		string(client.calls[3].params),
		`"y":200`,
	) {
		t.Fatalf("first raw Send event=%s", client.calls[3].params)
	}
}

func TestSendDispatcherFailsClosedWhenModelChangesBeforeSend(t *testing.T) {
	client := &selectionActivationClient{
		evaluations: []json.RawMessage{
			exactSelectionGuard("Pro", "GPT-5.5"),
		},
	}
	session := newSelectionActivationSession(t, client)
	dispatcher := chatgptSendDispatcher{
		prompt:       "review the current diff",
		intelligence: "Pro",
		model:        "GPT-5.6 Sol",
	}

	outcome, err := dispatcher.Dispatch(context.Background(), session)
	if err == nil ||
		!strings.Contains(err.Error(), "model guard") ||
		outcome.Dispatch != browserflow.DispatchNotPerformed ||
		outcome.RawInputAttempted {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	assertObservationOnly(t, client.calls)
}

func TestContinueDispatcherFailsClosedWhenModelChangesBeforeSend(t *testing.T) {
	client := &selectionActivationClient{
		evaluations: []json.RawMessage{
			exactSelectionGuard("Pro", "GPT-5.5"),
		},
	}
	session := newSelectionActivationSession(t, client)
	dispatcher := chatgptContinueDispatcher{
		prompt:         "follow up on the review",
		conversationID: "12345678-1234-1234-1234-123456789abc",
		intelligence:   "Pro",
		model:          "GPT-5.6 Sol",
	}

	outcome, err := dispatcher.Dispatch(context.Background(), session)
	if err == nil ||
		!strings.Contains(err.Error(), "model guard") ||
		outcome.Dispatch != browserflow.DispatchNotPerformed ||
		outcome.RawInputAttempted {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	assertObservationOnly(t, client.calls)
}

func exactSelectionGuard(thinking string, model string) json.RawMessage {
	modelFields := ""
	if model != "" {
		modelFields = `,
			"thinking_menu_open":true,
			"model_trigger_count":1,
			"model_trigger_label":"` + model + `"`
	}
	return json.RawMessage(`{
		"picker_count":1,
		"selected_thinking":"` + thinking + `",
		"model_menu_open":false` + modelFields + `
	}`)
}

func assertObservationOnly(t *testing.T, calls []selectionActivationCall) {
	t.Helper()
	if len(calls) != 1 || calls[0].method != "Runtime.evaluate" {
		t.Fatalf("calls=%+v, want one passive selection observation", calls)
	}
}
