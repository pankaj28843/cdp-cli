package browserflow

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

// InsertText performs reversible composer preparation inside an already owned
// exact target. The caller must verify the resulting editor value before
// crossing the irreversible dispatch boundary.
func InsertText(ctx context.Context, session *cdp.PageSession, text string) error {
	if session == nil {
		return fmt.Errorf("page session is required")
	}
	params, err := json.Marshal(map[string]any{"text": text})
	if err != nil {
		return fmt.Errorf("marshal text insertion")
	}
	if _, err := session.Exec(ctx, "Input.insertText", params); err != nil {
		return fmt.Errorf("prepare exact-target text input")
	}
	return nil
}

// PressEnter dispatches one logical Enter action. Any keyDown transport error
// is ambiguous because Chrome may have received it. Once keyDown succeeds the
// action is performed even if best-effort keyUp fails.
func PressEnter(ctx context.Context, session *cdp.PageSession) (DispatchOutcome, error) {
	if session == nil {
		return DispatchOutcome{Dispatch: DispatchNotPerformed}, fmt.Errorf("page session is required")
	}
	keyDown := json.RawMessage(`{"type":"keyDown","key":"Enter","code":"Enter","windowsVirtualKeyCode":13,"nativeVirtualKeyCode":13}`)
	if _, err := session.Exec(ctx, "Input.dispatchKeyEvent", keyDown); err != nil {
		return DispatchOutcome{Dispatch: DispatchUnknown, RawInputAttempted: true}, fmt.Errorf("Enter keyDown outcome is ambiguous")
	}
	keyUp := json.RawMessage(`{"type":"keyUp","key":"Enter","code":"Enter","windowsVirtualKeyCode":13,"nativeVirtualKeyCode":13}`)
	if _, err := session.Exec(ctx, "Input.dispatchKeyEvent", keyUp); err != nil {
		return DispatchOutcome{Dispatch: DispatchPerformed, RawInputAttempted: true}, fmt.Errorf("Enter keyUp confirmation failed after keyDown")
	}
	return DispatchOutcome{Dispatch: DispatchPerformed, RawInputAttempted: true}, nil
}

// PressEscape dismisses one reversible browser control such as a menu. It uses
// the same tri-state transport classification as other raw input helpers so
// callers can observe the resulting page state after an ambiguous key event.
func PressEscape(
	ctx context.Context,
	session *cdp.PageSession,
) (DispatchOutcome, error) {
	if session == nil {
		return DispatchOutcome{Dispatch: DispatchNotPerformed},
			fmt.Errorf("page session is required")
	}
	keyDown := json.RawMessage(
		`{"type":"rawKeyDown","key":"Escape","code":"Escape","windowsVirtualKeyCode":27,"nativeVirtualKeyCode":27}`,
	)
	if _, err := session.Exec(ctx, "Input.dispatchKeyEvent", keyDown); err != nil {
		return DispatchOutcome{
			Dispatch:          DispatchUnknown,
			RawInputAttempted: true,
		}, fmt.Errorf("Escape keyDown outcome is ambiguous")
	}
	keyUp := json.RawMessage(
		`{"type":"keyUp","key":"Escape","code":"Escape","windowsVirtualKeyCode":27,"nativeVirtualKeyCode":27}`,
	)
	if _, err := session.Exec(ctx, "Input.dispatchKeyEvent", keyUp); err != nil {
		return DispatchOutcome{
			Dispatch:          DispatchPerformed,
			RawInputAttempted: true,
		}, fmt.Errorf("Escape keyUp confirmation failed after keyDown")
	}
	return DispatchOutcome{
		Dispatch:          DispatchPerformed,
		RawInputAttempted: true,
	}, nil
}

// PressEnterOnSelector focuses one provider-validated DOM control and dispatches
// one logical browser-level Enter action. The focus evaluation is reversible,
// so any failure before rawKeyDown is explicitly not performed. Any rawKeyDown
// transport error is ambiguous because Chrome may already have received it.
func PressEnterOnSelector(
	ctx context.Context,
	session *cdp.PageSession,
	selector string,
) (DispatchOutcome, error) {
	if session == nil {
		return DispatchOutcome{Dispatch: DispatchNotPerformed}, fmt.Errorf("page session is required")
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return DispatchOutcome{Dispatch: DispatchNotPerformed}, fmt.Errorf("selector is required")
	}
	encodedSelector, err := json.Marshal(selector)
	if err != nil {
		return DispatchOutcome{Dispatch: DispatchNotPerformed}, fmt.Errorf("encode selector")
	}
	focusExpression := fmt.Sprintf(`(() => {
	  const selector = %s;
	  let elements;
	  try {
	    elements = Array.from(document.querySelectorAll(selector));
	  } catch (_) {
	    return {target_found: false, focused: false};
	  }
	  if (elements.length !== 1) {
	    return {target_found: false, focused: false};
	  }
	  const target = elements[0];
	  target.focus();
	  return {target_found: true, focused: document.activeElement === target};
	})()`, encodedSelector)
	evaluated, err := session.Evaluate(ctx, focusExpression, true)
	if err != nil || evaluated.Exception != nil || len(evaluated.Object.Value) == 0 {
		return DispatchOutcome{Dispatch: DispatchNotPerformed},
			fmt.Errorf("exact selector could not be focused before Enter")
	}
	var result struct {
		TargetFound bool `json:"target_found"`
		Focused     bool `json:"focused"`
	}
	if err := json.Unmarshal(evaluated.Object.Value, &result); err != nil {
		return DispatchOutcome{Dispatch: DispatchNotPerformed},
			fmt.Errorf("exact selector focus could not be verified before Enter")
	}
	if !result.TargetFound || !result.Focused {
		return DispatchOutcome{Dispatch: DispatchNotPerformed},
			fmt.Errorf("exact selector was not focused before Enter")
	}
	rawKeyDown := json.RawMessage(
		`{"type":"rawKeyDown","key":"Enter","code":"Enter","windowsVirtualKeyCode":13,"nativeVirtualKeyCode":13}`,
	)
	if _, err := session.Exec(ctx, "Input.dispatchKeyEvent", rawKeyDown); err != nil {
		return DispatchOutcome{
			Dispatch:          DispatchUnknown,
			RawInputAttempted: true,
		}, fmt.Errorf("selector-bound Enter rawKeyDown outcome is ambiguous")
	}
	keyUp := json.RawMessage(
		`{"type":"keyUp","key":"Enter","code":"Enter","windowsVirtualKeyCode":13,"nativeVirtualKeyCode":13}`,
	)
	if _, err := session.Exec(ctx, "Input.dispatchKeyEvent", keyUp); err != nil {
		return DispatchOutcome{
			Dispatch:          DispatchPerformed,
			RawInputAttempted: true,
		}, fmt.Errorf("selector-bound Enter keyUp confirmation failed after rawKeyDown")
	}
	return DispatchOutcome{Dispatch: DispatchPerformed, RawInputAttempted: true}, nil
}

// ClickPoint dispatches one logical raw-input mouse click at an already
// provider-validated point. Mouse movement failure is proven not performed;
// transport failure at or after mousePressed is ambiguous and must not be
// retried across an irreversible boundary.
func ClickPoint(
	ctx context.Context,
	session *cdp.PageSession,
	x float64,
	y float64,
) (DispatchOutcome, error) {
	if session == nil {
		return DispatchOutcome{Dispatch: DispatchNotPerformed}, fmt.Errorf("page session is required")
	}
	if math.IsNaN(x) || math.IsNaN(y) ||
		math.IsInf(x, 0) || math.IsInf(y, 0) ||
		x < 0 || y < 0 {
		return DispatchOutcome{Dispatch: DispatchNotPerformed}, fmt.Errorf("click point is invalid")
	}
	events := []struct {
		params map[string]any
		raw    bool
	}{
		{
			params: map[string]any{
				"type": "mouseMoved", "x": x, "y": y, "button": "none",
			},
		},
		{
			params: map[string]any{
				"type": "mousePressed", "x": x, "y": y, "button": "left",
				"buttons": 1, "clickCount": 1,
			},
			raw: true,
		},
		{
			params: map[string]any{
				"type": "mouseReleased", "x": x, "y": y, "button": "left",
				"buttons": 0, "clickCount": 1,
			},
			raw: true,
		},
	}
	rawAttempted := false
	for _, event := range events {
		params, err := json.Marshal(event.params)
		if err != nil {
			return DispatchOutcome{Dispatch: DispatchNotPerformed}, fmt.Errorf("marshal mouse click")
		}
		if event.raw {
			rawAttempted = true
		}
		if _, err := session.Exec(ctx, "Input.dispatchMouseEvent", params); err != nil {
			if !rawAttempted {
				return DispatchOutcome{Dispatch: DispatchNotPerformed}, fmt.Errorf("mouse click was not performed")
			}
			return DispatchOutcome{
				Dispatch:          DispatchUnknown,
				RawInputAttempted: true,
			}, fmt.Errorf("mouse click outcome is ambiguous")
		}
	}
	return DispatchOutcome{Dispatch: DispatchPerformed, RawInputAttempted: true}, nil
}
