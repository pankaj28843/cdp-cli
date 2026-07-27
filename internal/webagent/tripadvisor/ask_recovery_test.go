package tripadvisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestAskTreatsSessionCacheAsAdvisoryAndRecoversBeforePromptMutation(
	t *testing.T,
) {
	const (
		prompt         = "Review Tripadvisor live recovery"
		conversationID = "12345678-1234-1234-1234-123456789abc"
	)
	stateDir := t.TempDir()
	client := testsupport.NewBrowser("user-page")
	client.Evaluate = func(expression string, browser *testsupport.Browser) (any, error) {
		switch {
		case strings.Contains(expression, "origin_ready"):
			ready := len(browser.Reloads) >= 2
			count := 0
			if ready {
				count = 1
			}
			return map[string]any{
				"origin_ready":       ready,
				"panel_count":        count,
				"panel_ready":        ready,
				"composer_count":     count,
				"composer_ready":     ready,
				"history_count":      count,
				"history_ready":      ready,
				"sign_in_visible":    false,
				"panel_opener_count": 0,
				"panel_opener_ready": false,
				"new_chat_count":     0,
				"new_chat_ready":     false,
			}, nil
		case strings.Contains(expression, "send_ready"):
			return map[string]any{
				"blank":           true,
				"panel_count":     1,
				"editor_count":    1,
				"prompt_matches":  browser.InsertedText == prompt,
				"send_count":      1,
				"send_ready":      true,
				"send_x":          20,
				"send_y":          30,
				"answer_count":    0,
				"prompt_count":    0,
				"conversation_id": "",
			}, nil
		case strings.Contains(expression, "selectionStart"):
			return map[string]any{"ok": true}, nil
		case strings.Contains(expression, "provider_error"):
			if browser.SendCount == 0 {
				return map[string]any{"blank": true}, nil
			}
			return map[string]any{
				"blank":           false,
				"route_matches":   true,
				"conversation_id": conversationID,
				"text":            "Tripadvisor terminal answer",
				"prompt":          prompt,
				"is_streaming":    false,
				"answer_count":    1,
				"prompt_count":    1,
				"provider_error":  false,
			}, nil
		default:
			return map[string]any{}, nil
		}
	}
	engine, journal, gate, err := testsupport.NewRuntime(stateDir, client)
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
			Admission:   gate,
			BuildCommit: "test-commit",
		},
		Store:           store,
		Timeout:         time.Second,
		ComposerTimeout: time.Second,
		PollInterval:    time.Millisecond,
		QuietInterval:   time.Millisecond,
		Now:             testsupport.FixedNow,
	}

	result := Ask(context.Background(), config, prompt)
	data, _ := result.Data.(AskData)
	if !result.OK ||
		(result.State != webagent.StateTerminal &&
			result.State != webagent.StateIncomplete) ||
		result.Action == nil ||
		result.Action.RawInputCount != 1 ||
		data.SessionMode != "signed_in" ||
		data.ReadMode != "headed_browser" ||
		result.Conversation == nil ||
		result.Conversation.ID != conversationID {
		t.Fatalf(
			"Ask result=%+v error=%+v data=%+v",
			result,
			result.Error,
			data,
		)
	}
	if result.State == webagent.StateTerminal &&
		data.Text != "Tripadvisor terminal answer" {
		t.Fatalf("terminal Ask result=%+v data=%+v", result, data)
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
	if lastTraceIndex(trace, "Page.reload") >=
		lastTraceIndex(trace, "Input.insertText") {
		t.Fatalf("reload occurred after prompt mutation: %v", trace)
	}
}

func lastTraceIndex(values []string, wanted string) int {
	index := -1
	for i, value := range values {
		if value == wanted {
			index = i
		}
	}
	return index
}
