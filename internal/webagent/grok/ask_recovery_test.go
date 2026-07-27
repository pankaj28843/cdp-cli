package grok

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestAskUsesLiveModeAndRenderedFallbackAfterPreMutationRecovery(
	t *testing.T,
) {
	const (
		prompt         = "Review Grok live recovery"
		conversationID = "grok-conversation"
	)
	stateDir := t.TempDir()
	client := testsupport.NewBrowser("user-page")
	client.Evaluate = func(expression string, browser *testsupport.Browser) (any, error) {
		switch {
		case strings.Contains(expression, `aria-label="Model select"`):
			ready := len(browser.Reloads) >= 2
			count := 0
			if ready {
				count = 1
			}
			return map[string]any{
				"route_ready":     ready,
				"editor_ready":    ready,
				"editor_count":    count,
				"prompt_matches":  ready && browser.InsertedText == prompt,
				"mode_count":      count,
				"mode_title":      "Think",
				"submit_count":    count,
				"submit_ready":    ready,
				"submit_x":        20,
				"submit_y":        30,
				"assistant_count": 0,
				"conversation_id": "",
			}, nil
		case strings.Contains(expression, "range.selectNodeContents"):
			return map[string]any{"ok": true}, nil
		case strings.Contains(expression, "conversation_id"):
			if browser.SendCount == 0 {
				return map[string]any{}, nil
			}
			return map[string]any{
				"route_matches":   true,
				"conversation_id": conversationID,
				"text":            "Grok terminal answer",
				"prompt":          prompt,
				"is_streaming":    false,
				"answer_count":    1,
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
		Now:             testsupport.FixedNow,
	}

	result := Ask(context.Background(), config, prompt)
	data, _ := result.Data.(AskData)
	if !result.OK ||
		(result.State != webagent.StateTerminal &&
			result.State != webagent.StateIncomplete) ||
		result.Action == nil ||
		result.Action.RawInputCount != 1 ||
		data.ModeTitle != "Think" ||
		result.Conversation == nil ||
		result.Conversation.ID != conversationID {
		t.Fatalf("Ask result=%+v data=%+v", result, data)
	}
	if result.State == webagent.StateTerminal &&
		(data.ReadMode != "headed_browser_fallback" ||
			data.Text != "Grok terminal answer") {
		t.Fatalf("terminal Ask result=%+v data=%+v", result, data)
	}
	if available, _ := data.Metadata["cached_read_template_available"].(bool); available {
		t.Fatalf("missing read template was reported available: %+v", data.Metadata)
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
