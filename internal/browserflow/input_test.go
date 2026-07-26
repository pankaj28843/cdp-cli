package browserflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestInputHelpersPreservePreparationAndDispatchClassification(t *testing.T) {
	t.Run("insert text", func(t *testing.T) {
		session, input := newInputSession(t)
		if err := InsertText(context.Background(), session, "private prompt"); err != nil {
			t.Fatalf("InsertText: %v", err)
		}
		if len(input.calls) != 1 ||
			input.calls[0].method != "Input.insertText" ||
			!strings.Contains(string(input.calls[0].params), "private prompt") {
			t.Fatalf("insert calls = %+v", input.calls)
		}
	})

	t.Run("keyDown ambiguous", func(t *testing.T) {
		session, input := newInputSession(t)
		input.failAt = 1
		outcome, err := PressEnter(context.Background(), session)
		if err == nil ||
			outcome.Dispatch != DispatchUnknown ||
			!outcome.RawInputAttempted ||
			len(input.calls) != 1 {
			t.Fatalf("keyDown outcome=%+v err=%v calls=%+v", outcome, err, input.calls)
		}
	})

	t.Run("keyUp failure remains performed", func(t *testing.T) {
		session, input := newInputSession(t)
		input.failAt = 2
		outcome, err := PressEnter(context.Background(), session)
		if err == nil ||
			outcome.Dispatch != DispatchPerformed ||
			!outcome.RawInputAttempted ||
			len(input.calls) != 2 {
			t.Fatalf("keyUp outcome=%+v err=%v calls=%+v", outcome, err, input.calls)
		}
	})

	t.Run("success", func(t *testing.T) {
		session, input := newInputSession(t)
		outcome, err := PressEnter(context.Background(), session)
		if err != nil ||
			outcome.Dispatch != DispatchPerformed ||
			!outcome.RawInputAttempted ||
			len(input.calls) != 2 {
			t.Fatalf("success outcome=%+v err=%v calls=%+v", outcome, err, input.calls)
		}
	})
}

func TestSelectorBoundEnterPreservesTriStateBoundary(t *testing.T) {
	t.Run("explicit selector miss is not performed", func(t *testing.T) {
		session, input := newInputSession(t)
		input.evaluateValue = json.RawMessage(
			`{"target_found":false,"focused":false}`,
		)
		outcome, err := PressEnterOnSelector(
			context.Background(),
			session,
			"#ask-input",
		)
		if err == nil ||
			outcome.Dispatch != DispatchNotPerformed ||
			outcome.RawInputAttempted ||
			len(input.calls) != 1 {
			t.Fatalf("outcome=%+v err=%v calls=%+v", outcome, err, input.calls)
		}
	})

	t.Run("focus transport failure is not performed", func(t *testing.T) {
		session, input := newInputSession(t)
		input.failAt = 1
		outcome, err := PressEnterOnSelector(
			context.Background(),
			session,
			"#ask-input",
		)
		if err == nil ||
			outcome.Dispatch != DispatchNotPerformed ||
			outcome.RawInputAttempted ||
			len(input.calls) != 1 {
			t.Fatalf("outcome=%+v err=%v calls=%+v", outcome, err, input.calls)
		}
	})

	t.Run("rawKeyDown transport failure is ambiguous", func(t *testing.T) {
		session, input := newInputSession(t)
		input.evaluateValue = json.RawMessage(
			`{"target_found":true,"focused":true}`,
		)
		input.failAt = 2
		outcome, err := PressEnterOnSelector(
			context.Background(),
			session,
			"#ask-input",
		)
		if err == nil ||
			outcome.Dispatch != DispatchUnknown ||
			!outcome.RawInputAttempted ||
			len(input.calls) != 2 {
			t.Fatalf("outcome=%+v err=%v calls=%+v", outcome, err, input.calls)
		}
	})

	t.Run("keyUp transport failure remains performed", func(t *testing.T) {
		session, input := newInputSession(t)
		input.evaluateValue = json.RawMessage(
			`{"target_found":true,"focused":true}`,
		)
		input.failAt = 3
		outcome, err := PressEnterOnSelector(
			context.Background(),
			session,
			"#ask-input",
		)
		if err == nil ||
			outcome.Dispatch != DispatchPerformed ||
			!outcome.RawInputAttempted ||
			len(input.calls) != 3 {
			t.Fatalf("outcome=%+v err=%v calls=%+v", outcome, err, input.calls)
		}
	})

	t.Run("one exact selector receives one logical Enter", func(t *testing.T) {
		session, input := newInputSession(t)
		input.evaluateValue = json.RawMessage(
			`{"target_found":true,"focused":true}`,
		)
		outcome, err := PressEnterOnSelector(
			context.Background(),
			session,
			"#ask-input",
		)
		if err != nil ||
			outcome.Dispatch != DispatchPerformed ||
			!outcome.RawInputAttempted ||
			len(input.calls) != 3 ||
			input.calls[0].method != "Runtime.evaluate" ||
			input.calls[1].method != "Input.dispatchKeyEvent" ||
			input.calls[2].method != "Input.dispatchKeyEvent" ||
			!strings.Contains(string(input.calls[0].params), "#ask-input") ||
			!strings.Contains(string(input.calls[1].params), "rawKeyDown") {
			t.Fatalf("outcome=%+v err=%v calls=%+v", outcome, err, input.calls)
		}
	})
}

func TestClickPointPreservesTriStateMouseBoundary(t *testing.T) {
	tests := []struct {
		name          string
		failAt        int
		wantDispatch  Dispatch
		wantRaw       bool
		wantCallCount int
	}{
		{
			name:          "movement failure is not performed",
			failAt:        1,
			wantDispatch:  DispatchNotPerformed,
			wantRaw:       false,
			wantCallCount: 1,
		},
		{
			name:          "press failure is unknown",
			failAt:        2,
			wantDispatch:  DispatchUnknown,
			wantRaw:       true,
			wantCallCount: 2,
		},
		{
			name:          "release failure is unknown",
			failAt:        3,
			wantDispatch:  DispatchUnknown,
			wantRaw:       true,
			wantCallCount: 3,
		},
		{
			name:          "complete click is performed",
			wantDispatch:  DispatchPerformed,
			wantRaw:       true,
			wantCallCount: 3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, input := newInputSession(t)
			input.failAt = test.failAt
			outcome, _ := ClickPoint(context.Background(), session, 20, 30)
			if outcome.Dispatch != test.wantDispatch ||
				outcome.RawInputAttempted != test.wantRaw ||
				len(input.calls) != test.wantCallCount {
				t.Fatalf("outcome=%+v calls=%+v", outcome, input.calls)
			}
		})
	}
}

type inputCall struct {
	method string
	params json.RawMessage
}

type inputClient struct {
	cdp.CommandClient
	calls         []inputCall
	failAt        int
	evaluateValue json.RawMessage
}

func (c *inputClient) Call(ctx context.Context, method string, params any, result any) error {
	if method == "Target.attachToTarget" {
		data, _ := json.Marshal(map[string]any{"sessionId": "session-target-1"})
		return json.Unmarshal(data, result)
	}
	return c.CommandClient.Call(ctx, method, params, result)
}

func (c *inputClient) CallSession(_ context.Context, _ string, method string, params any, result any) error {
	raw, _ := json.Marshal(params)
	c.calls = append(c.calls, inputCall{method: method, params: raw})
	if c.failAt == len(c.calls) {
		return errors.New("private synthetic transport failure")
	}
	if method == "Runtime.evaluate" && result != nil && len(c.evaluateValue) != 0 {
		response, _ := json.Marshal(map[string]any{
			"result": map[string]any{
				"type":  "object",
				"value": c.evaluateValue,
			},
		})
		return json.Unmarshal(response, result)
	}
	if result != nil {
		return json.Unmarshal([]byte(`{}`), result)
	}
	return nil
}

func newInputSession(t *testing.T) (*cdp.PageSession, *inputClient) {
	t.Helper()
	client := &inputClient{CommandClient: newFakeBrowserClient()}
	session, err := cdp.AttachToTargetWithClient(context.Background(), client, "target-1", nil)
	if err != nil {
		t.Fatalf("AttachToTargetWithClient: %v", err)
	}
	client.calls = nil
	return session, client
}
