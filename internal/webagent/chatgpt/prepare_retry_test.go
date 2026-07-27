package chatgpt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPrepareVerifiedPromptRetriesLateSelectionMismatch(t *testing.T) {
	client := &lateSelectionClient{}
	session := newSelectionActivationSession(t, client)

	attempts, observation, err := prepareVerifiedPrompt(
		context.Background(),
		session,
		"review current diff",
		"High",
		"GPT Test",
		nil,
		250*time.Millisecond,
		time.Millisecond,
	)
	if err != nil ||
		attempts < 2 ||
		!composerReadyForSend(observation, "High") ||
		!client.mismatchReturned ||
		client.insertCount < 2 {
		t.Fatalf(
			"attempts=%d observation=%+v err=%v mismatch=%v inserts=%d",
			attempts,
			observation,
			err,
			client.mismatchReturned,
			client.insertCount,
		)
	}
	client.assertNoRawSend(t)
}

func TestPrepareVerifiedContinuationRetriesLateSelectionMismatch(t *testing.T) {
	const conversationID = "conversation-1"
	client := &lateSelectionClient{
		conversationID: conversationID,
		userCount:      2,
		assistantCount: 1,
	}
	session := newSelectionActivationSession(t, client)

	attempts, observation, err := prepareVerifiedContinuationPrompt(
		context.Background(),
		session,
		conversationID,
		"follow up on the current diff",
		2,
		1,
		"High",
		"GPT Test",
		250*time.Millisecond,
		time.Millisecond,
	)
	if err != nil ||
		attempts < 2 ||
		!continuationComposerReadyForSend(
			observation,
			conversationID,
			2,
			1,
			"High",
		) ||
		!client.mismatchReturned ||
		client.insertCount < 2 {
		t.Fatalf(
			"attempts=%d observation=%+v err=%v mismatch=%v inserts=%d",
			attempts,
			observation,
			err,
			client.mismatchReturned,
			client.insertCount,
		)
	}
	client.assertNoRawSend(t)
}

type lateSelectionClient struct {
	thinkingOpen     bool
	modelOpen        bool
	thinkingOpens    int
	mismatchNext     bool
	mismatchReturned bool
	composerReads    int
	insertCount      int
	methods          []string
	conversationID   string
	userCount        int
	assistantCount   int
}

func (c *lateSelectionClient) Call(
	_ context.Context,
	method string,
	_ any,
	result any,
) error {
	if method == "Target.attachToTarget" {
		return decodeSelectionActivationResult(
			result,
			map[string]any{"sessionId": "late-selection-session"},
		)
	}
	return nil
}

func (c *lateSelectionClient) CallSession(
	_ context.Context,
	_ string,
	method string,
	params any,
	result any,
) error {
	c.methods = append(c.methods, method)
	if method == "Input.insertText" {
		c.insertCount++
		return decodeSelectionActivationResult(result, map[string]any{})
	}
	if method != "Runtime.evaluate" {
		return decodeSelectionActivationResult(result, map[string]any{})
	}
	raw, _ := json.Marshal(params)
	values := map[string]any{}
	_ = json.Unmarshal(raw, &values)
	expression, _ := values["expression"].(string)
	value := any(map[string]any{})
	switch {
	case strings.Contains(expression, `const kind = "editor"`):
		value = map[string]any{"ok": true, "count": 1, "activated": true}
	case strings.Contains(expression, `const kind = "picker"`):
		if c.thinkingOpen || c.modelOpen {
			c.thinkingOpen = false
			c.modelOpen = false
		} else {
			c.thinkingOpen = true
			c.thinkingOpens++
			if c.thinkingOpens == 2 {
				c.mismatchNext = true
			}
		}
		value = map[string]any{"ok": true, "count": 1, "activated": true}
	case strings.Contains(expression, `const kind = "model-trigger"`):
		c.modelOpen = true
		value = map[string]any{"ok": true, "count": 1, "activated": true}
	case strings.Contains(expression, "thinkingKnown"):
		selectedThinking := "High"
		if c.mismatchNext {
			selectedThinking = "Medium"
			c.mismatchNext = false
			c.mismatchReturned = true
		}
		thinkingOptions := []map[string]any{}
		if c.thinkingOpen {
			thinkingOptions = append(thinkingOptions, map[string]any{
				"label": "High", "checked": true, "ready": true,
				"x": 10, "y": 10,
			})
		}
		modelOptions := []map[string]any{}
		if c.modelOpen {
			modelOptions = append(modelOptions, map[string]any{
				"label": "GPT Test", "checked": true, "ready": true,
				"x": 20, "y": 20,
			})
		}
		value = map[string]any{
			"editor":                       map[string]any{"ready": true},
			"chat_count":                   1,
			"work_count":                   1,
			"chat_selected":                true,
			"chat":                         map[string]any{"ready": true},
			"specialized_count":            0,
			"picker_count":                 1,
			"picker":                       map[string]any{"ready": true},
			"selected_thinking":            selectedThinking,
			"thinking_menu_open":           c.thinkingOpen,
			"thinking_options":             thinkingOptions,
			"model_trigger_count":          boolInt(c.thinkingOpen),
			"model_trigger":                map[string]any{"ready": c.thinkingOpen},
			"model_trigger_label":          "Models",
			"model_menu_open":              c.modelOpen,
			"model_options_provider_order": modelOptions,
			"selected_model":               "GPT Test",
		}
	case strings.Contains(expression, "send_ready:"):
		c.composerReads++
		value = map[string]any{
			"route_ready":               c.conversationID == "",
			"editor_ready":              true,
			"editor_count":              1,
			"prompt_matches":            true,
			"chat_count":                1,
			"work_count":                1,
			"chat_selected":             true,
			"intelligence_count":        1,
			"selected_intelligence":     "High",
			"send_count":                1,
			"send_ready":                c.composerReads >= 2,
			"assistant_count":           c.assistantCount,
			"user_message_count":        c.userCount,
			"conversation_id":           c.conversationID,
			"specialized_surface_count": 0,
		}
	case strings.Contains(expression, "range.selectNodeContents"):
		value = map[string]any{"ok": true}
	}
	return decodeSelectionActivationResult(
		result,
		map[string]any{
			"result": map[string]any{
				"type":  "object",
				"value": value,
			},
		},
	)
}

func (c *lateSelectionClient) assertNoRawSend(t *testing.T) {
	t.Helper()
	for _, method := range c.methods {
		if method == "Input.dispatchKeyEvent" ||
			method == "Input.dispatchMouseEvent" {
			t.Fatalf("raw Send occurred during reversible preparation: %v", c.methods)
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
