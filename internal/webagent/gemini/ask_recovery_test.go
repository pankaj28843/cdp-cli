package gemini

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestAskTreatsCachesAsAdvisoryAndRecoversBeforePromptMutation(t *testing.T) {
	const (
		prompt         = "Review Gemini live recovery"
		conversationID = "abcdefghijklmnop"
	)
	stateDir := t.TempDir()
	client := testsupport.NewBrowser("user-page")
	client.Evaluate = func(expression string, browser *testsupport.Browser) (any, error) {
		switch {
		case strings.Contains(expression, "Open mode picker, currently "):
			ready := len(browser.Reloads) >= 2
			return map[string]any{
				"route_ready":    ready,
				"editor_ready":   ready,
				"editor_count":   boolCountForRecovery(ready),
				"current_mode":   "Deep Think",
				"picker_count":   boolCountForRecovery(ready),
				"answer_count":   0,
				"prompt_matches": ready && browser.InsertedText == prompt,
			}, nil
		case strings.Contains(expression, "range.selectNodeContents"):
			return map[string]any{"ok": true}, nil
		case strings.Contains(expression, "navigator.clipboard"):
			return map[string]any{
				"prompt":                prompt,
				"query_count":           1,
				"copy_button_count":     1,
				"clipboard_intercepted": true,
				"captured":              true,
			}, nil
		case strings.Contains(expression, "conversation_id"):
			if browser.SendCount == 0 {
				return map[string]any{}, nil
			}
			return map[string]any{
				"route_matches":   true,
				"conversation_id": conversationID,
				"text":            "Gemini terminal answer",
				"is_streaming":    false,
				"answer_count":    1,
			}, nil
		default:
			return map[string]any{}, nil
		}
	}
	engine, journal, err := testsupport.NewRuntime(stateDir, client)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	config := AskConfig{
		BrowserConfig: BrowserConfig{
			Client:      client,
			Engine:      engine,
			Journal:     journal,
			BuildCommit: "test-commit",
		},
		Store:           store,
		Timeout:         time.Second,
		ComposerTimeout: time.Second,
		PollInterval:    time.Millisecond,
		Now:             testsupport.FixedNow,
	}

	result := Ask(context.Background(), config, prompt)
	data, _ := result.Data.(AskData)
	if !result.OK ||
		result.State != webagent.StateTerminal ||
		result.Action == nil ||
		result.Action.RawInputCount != 1 ||
		data.CurrentMode != "Deep Think" ||
		data.Text != "Gemini terminal answer" {
		t.Fatalf("Ask result=%+v data=%+v", result, data)
	}
	counts, trace, reloads, inserted, insertCount, sendCount, targets :=
		client.Snapshot()
	if len(reloads) != 2 || reloads[0] || !reloads[1] {
		t.Fatalf("reloads=%v, want [false true]", reloads)
	}
	if inserted != prompt || insertCount != 1 || sendCount != 1 {
		t.Fatalf(
			"inserted=%q insert_count=%d send_count=%d counts=%v",
			inserted,
			insertCount,
			sendCount,
			counts,
		)
	}
	if _, exists := targets["owned-1"]; exists {
		t.Fatalf("owned target was not closed: %v", targets)
	}
	if _, exists := targets["user-page"]; !exists {
		t.Fatalf("pre-existing target was closed: %v", targets)
	}
	if lastIndex(trace, "Page.reload") >= lastIndex(trace, "Input.insertText") {
		t.Fatalf("reload occurred after prompt mutation: %v", trace)
	}
}

func boolCountForRecovery(value bool) int {
	if value {
		return 1
	}
	return 0
}

func lastIndex(values []string, wanted string) int {
	index := -1
	for i, value := range values {
		if value == wanted {
			index = i
		}
	}
	return index
}
