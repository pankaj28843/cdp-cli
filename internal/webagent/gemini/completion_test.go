package gemini

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestConversationCompletionRequiresConfirmedStableAnswer(t *testing.T) {
	for _, operation := range []string{"ask", "detail", "await"} {
		for _, scenario := range []string{"growing", "unconfirmed", "observation_error", "complete"} {
			t.Run(operation+"/"+scenario, func(t *testing.T) {
				const id = "abcdefghijklmnop"
				const prompt = "Synthetic completion contract"
				partial := strings.Repeat("x", 1877)
				full := strings.Repeat("x", 2988) + " COMPLETE_FINAL_MARKER"
				client := testsupport.NewBrowser("user-page")
				reads := 0
				client.Evaluate = func(expression string, b *testsupport.Browser) (any, error) {
					switch {
					case strings.Contains(expression, "geminiSendControl"):
						return map[string]any{"ready": true, "x": 20, "y": 20}, nil
					case strings.Contains(expression, "Open mode picker, currently "):
						return map[string]any{"route_ready": true, "editor_ready": true, "editor_count": 1, "current_mode": "Pro", "picker_count": 1, "answer_count": 0, "prompt_matches": b.InsertedText == prompt}, nil
					case strings.Contains(expression, "range.selectNodeContents"):
						return map[string]any{"ok": true}, nil
					case strings.Contains(expression, "navigator.clipboard"):
						return map[string]any{"prompt": prompt, "query_count": 1, "copy_button_count": 1, "clipboard_intercepted": true, "captured": true}, nil
					case strings.Contains(expression, "conversation_id"):
						text, ready := partial, false
						// Acknowledgement is not a completion observation.
						if strings.Contains(expression, `const expected = "abcdefghijklmnop"`) {
							reads++
							ready = scenario == "growing" || scenario == "complete" || scenario == "observation_error"
							if scenario == "complete" || scenario == "growing" && reads > 1 {
								text = full
							}
							if scenario == "observation_error" && reads > 1 {
								return nil, errors.New("synthetic observation interrupted")
							}
						}
						return map[string]any{"route_matches": true, "conversation_id": id, "text": text, "is_streaming": false, "answer_count": 1, "completion_ready": ready}, nil
					default:
						return map[string]any{}, nil
					}
				}
				stateDir := t.TempDir()
				engine, journal, err := testsupport.NewRuntime(stateDir, client)
				if err != nil {
					t.Fatal(err)
				}
				store, err := NewStore(stateDir)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SaveAuth(context.Background(), AuthState{SchemaVersion: AuthStateSchemaVersion, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano), SignedIn: true, SessionCookieObserved: true, Source: "headed-cdp-safe-auth-evidence"}); err != nil {
					t.Fatal(err)
				}
				browser := BrowserConfig{Client: client, Engine: engine, Journal: journal, BuildCommit: "test"}
				var result webagent.Result
				if operation == "ask" {
					result = Ask(context.Background(), AskConfig{BrowserConfig: browser, Store: store, Timeout: 200 * time.Millisecond, ComposerTimeout: time.Second, PollInterval: time.Millisecond}, prompt)
				} else {
					cfg := ReadConfig{BrowserConfig: browser, Store: store, Timeout: 200 * time.Millisecond, PollInterval: time.Millisecond}
					if operation == "detail" {
						result = DetailConversation(context.Background(), cfg, id)
					} else {
						result = AwaitConversation(context.Background(), cfg, id)
					}
				}
				want := webagent.StateIncomplete
				if scenario == "growing" || scenario == "complete" {
					want = webagent.StateTerminal
				}
				var text string
				if data, ok := result.Data.(AskData); ok {
					text = data.Text
				}
				if data, ok := result.Data.(ConversationDetailData); ok {
					text = data.Text
				}
				if !result.OK || result.State != want || want == webagent.StateTerminal && text != full {
					t.Fatalf("state=%s want=%s text_length=%d want=%d reads=%d error=%+v", result.State, want, len(text), len(full), reads, result.Error)
				}
				if want == webagent.StateTerminal && reads < 2 {
					t.Fatal("terminal answer was not confirmed in a second observation")
				}
				if result.Conversation == nil || result.Conversation.ID != id {
					t.Fatal("exact resumable conversation was lost")
				}
				if result.Cleanup.State != webagent.CleanupClosed {
					t.Fatalf("cleanup=%+v", result.Cleanup)
				}
				_, _, _, _, _, _, targets := client.Snapshot()
				if _, ok := targets["owned-1"]; ok {
					t.Fatal("owned page remains open")
				}
				if _, ok := targets["user-page"]; !ok {
					t.Fatal("unrelated page closed")
				}
				if operation == "ask" && (result.Action == nil || result.Action.RawInputCount != 1 || result.Action.AttemptCount != 1) {
					t.Fatalf("Send evidence=%+v", result.Action)
				}
				if operation != "ask" && result.Action != nil {
					t.Fatal("read dispatched an action")
				}
			})
		}
	}
}
