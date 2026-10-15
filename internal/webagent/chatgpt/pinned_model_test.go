package chatgpt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Exercise the entire reversible selection transaction, not only its observer.
func TestSelectChatGPTAcceptsPinnedCheckedModelWithoutClick(t *testing.T) {
	for _, checked := range []bool{true, false} {
		t.Run(map[bool]string{true: "checked pinned model", false: "unselected disabled model"}[checked], func(t *testing.T) {
			client := &pinnedModelClient{checked: checked}
			got, err := selectChatGPT(context.Background(), newSelectionActivationSession(t, client), SelectionPolicy{Thinking: "Pro", MinimumThinking: "Pro", Model: "GPT-6"}, false, 250*time.Millisecond, time.Millisecond)
			if checked && (err != nil || got.Model != "GPT-6" || got.ModelAction != "already_selected" || got.Intelligence != "Pro" || got.ThinkingSliderValue != 4) {
				t.Fatalf("selection=%+v err=%v", got, err)
			}
			if !checked && (err == nil || !strings.Contains(err.Error(), "unavailable")) {
				t.Fatalf("disabled unselected model accepted: %+v %v", got, err)
			}
			if client.optionClicks != 0 {
				t.Fatal("clicked a pinned or disabled model")
			}
		})
	}
}

type pinnedModelClient struct {
	lateSelectionClient
	open, checked bool
	optionClicks  int
}

func (c *pinnedModelClient) CallSession(_ context.Context, _ string, method string, params any, result any) error {
	raw, _ := json.Marshal(params)
	var args struct {
		Expression string `json:"expression"`
	}
	_ = json.Unmarshal(raw, &args)
	value := map[string]any{}
	if method == "Runtime.evaluate" {
		switch {
		case strings.Contains(args.Expression, `const kind = "picker"`):
			c.open = !c.open
			value = map[string]any{"ok": true, "count": 1, "activated": true}
		case strings.Contains(args.Expression, `const kind = "option"`):
			c.optionClicks++
			value = map[string]any{"ok": false}
		case strings.Contains(args.Expression, "thinkingKnown"):
			options := []map[string]any{}
			if c.open {
				options = append(options, map[string]any{"label": "GPT-6", "checked": c.checked, "ready": false})
			}
			value = map[string]any{"editor": map[string]any{"ready": true}, "default_chat_surface": true, "picker_count": 1, "picker": map[string]any{"ready": true}, "selected_thinking": "Pro", "thinking_menu_open": c.open, "thinking_slider_ready": c.open, "thinking_slider_min": 0, "thinking_slider_max": 4, "thinking_slider_value": 4, "model_menu_open": c.open, "model_options_provider_order": options, "selected_model": "GPT-6"}
		}
	}
	return decodeSelectionActivationResult(result, map[string]any{"result": map[string]any{"type": "object", "value": value}})
}
