package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
)

func TestAskRequestedModeBeforeSend(t *testing.T) {
	for _, tc := range []struct {
		name     string
		matches  int
		readback string
		wantOK   bool
		disabled bool
		unknown  bool
		hydrate  bool
	}{
		{"selected", 1, "Flash", true, false, false, false},
		{"uninterrupted_hydration", 1, "Flash", true, false, false, true},
		{"unavailable", 0, "Pro", false, false, false, false},
		{"ambiguous", 2, "Pro", false, false, false, false},
		{"readback_mismatch", 1, "Pro", false, false, false, false},
		{"disabled", 1, "Pro", false, true, false, false},
		{"unknown_click", 1, "Pro", false, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testsupport.NewBrowser("user-page")
			const prompt = "Review requested Gemini mode"
			hydrationStart := time.Now()
			observedReloads := -1
			client.Evaluate = func(expression string, b *testsupport.Browser) (any, error) {
				mode := "Pro"
				if b.Counts["Input.dispatchMouseEvent"] >= 6 {
					mode = tc.readback
				}
				switch {
				case strings.Contains(expression, "geminiSendControl"):
					return map[string]any{"ready": true, "x": 20, "y": 20}, nil
				case strings.Contains(expression, "geminiModeSelection"):
					return map[string]any{"match_count": tc.matches, "ready": tc.matches == 1 && !tc.disabled, "x": 20, "y": 20}, nil
				case strings.Contains(expression, "mode_options:"):
					return map[string]any{"picker_count": 1, "picker_ready": true, "current_mode": mode, "menu_open": b.Counts["Input.dispatchMouseEvent"] >= 3, "mode_options": []string{"3.6 Flash\nAll-around help"}, "x": 10, "y": 10}, nil
				case strings.Contains(expression, "Open mode picker, currently "):
					if tc.hydrate {
						if observedReloads != len(b.Reloads) {
							hydrationStart = time.Now()
							observedReloads = len(b.Reloads)
						}
						if time.Since(hydrationStart) < 20*time.Millisecond {
							return map[string]any{"route_ready": true, "editor_count": 0, "picker_count": 0, "answer_count": 0}, nil
						}
					}
					return map[string]any{"route_ready": true, "editor_ready": true, "editor_count": 1, "current_mode": mode, "picker_count": 1, "answer_count": 0, "prompt_matches": b.InsertedText == prompt}, nil
				case strings.Contains(expression, "range.selectNodeContents"):
					return map[string]any{"ok": true}, nil
				case strings.Contains(expression, "navigator.clipboard"):
					return map[string]any{"prompt": prompt, "query_count": 1, "copy_button_count": 1, "clipboard_intercepted": true, "captured": true}, nil
				case strings.Contains(expression, "conversation_id"):
					return map[string]any{"route_matches": true, "conversation_id": "abcdefghijklmnop", "text": "Mode answer", "is_streaming": false, "completion_ready": true, "answer_count": 1}, nil
				default:
					return map[string]any{}, nil
				}
			}
			stateDir := t.TempDir()
			boundary := &modeClickBoundary{Browser: client, failSelection: tc.unknown}
			engine, journal, err := testsupport.NewRuntime(stateDir, boundary)
			if err != nil {
				t.Fatal(err)
			}
			store, err := NewStore(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			result := Ask(context.Background(), AskConfig{
				BrowserConfig: BrowserConfig{Client: boundary, Engine: engine, Journal: journal, BuildCommit: "test"},
				Store:         store, Mode: "Flash", Timeout: time.Second, ComposerTimeout: 30 * time.Millisecond, PollInterval: time.Millisecond, Now: testsupport.FixedNow,
			}, prompt)
			counts, _, reloads, _, insertCount, _, targets := client.Snapshot()
			if tc.hydrate && len(reloads) != 0 {
				t.Fatalf("hydration interrupted by reloads: %v", reloads)
			}
			if result.OK != tc.wantOK {
				t.Fatalf("OK=%v want %v result=%+v", result.OK, tc.wantOK, result)
			}
			if tc.wantOK {
				data := result.Data.(AskData)
				if data.CurrentMode != "Flash" || result.Action == nil || result.Action.RawInputCount != 1 || counts["Input.dispatchMouseEvent"] != 9 || counts["Input.dispatchKeyEvent"] != 0 {
					t.Fatalf("mode/Send not verified: result=%+v data=%+v counts=%v", result, data, counts)
				}
			} else if counts["Input.dispatchKeyEvent"] != 0 || insertCount != 0 || result.Action == nil || result.Action.RawInputCount != 0 {
				t.Fatalf("mode failure mutated/submitted prompt: result=%+v counts=%v inserts=%d", result, counts, insertCount)
			}
			if tc.unknown && counts["Input.dispatchMouseEvent"] != 5 {
				t.Fatalf("uncertain selection was retried: counts=%v", counts)
			}
			if _, ok := targets["owned-1"]; ok {
				t.Fatal("owned target retained")
			}
			if _, ok := targets["user-page"]; !ok {
				t.Fatal("user target closed")
			}
		})
	}
}

// Inject a transport failure after option mousePressed, when dispatch is uncertain.
type modeClickBoundary struct {
	*testsupport.Browser
	failSelection bool
}

func (b *modeClickBoundary) CallSession(ctx context.Context, session, method string, params any, result any) error {
	if err := b.Browser.CallSession(ctx, session, method, params, result); err != nil {
		return err
	}
	if b.failSelection && method == "Input.dispatchMouseEvent" {
		payload, _ := json.Marshal(params)
		var event struct{ Type string }
		_ = json.Unmarshal(payload, &event)
		if event.Type == "mousePressed" && b.Counts[method] == 5 {
			return fmt.Errorf("synthetic transport loss after option press")
		}
	}
	return nil
}

var _ cdp.CommandClient = (*modeClickBoundary)(nil)
