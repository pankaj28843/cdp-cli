package chatgpt

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestAskRecoversSelectionControlsBeforePromptMutation(t *testing.T) {
	const (
		prompt         = "Review ChatGPT live recovery"
		conversationID = "12345678-1234-1234-1234-123456789abc"
	)
	answer := strings.TrimSpace(
		strings.Repeat("ChatGPT terminal answer with useful detail. ", 4),
	)
	stateDir := t.TempDir()
	client := testsupport.NewBrowser("user-page")
	thinkingMenuOpen := false
	client.Evaluate = func(expression string, browser *testsupport.Browser) (any, error) {
		switch {
		case strings.Contains(expression, `const kind = "editor"`):
			return map[string]any{
				"ok": true, "count": 1, "activated": true,
			}, nil
		case strings.Contains(expression, `const kind = "picker"`):
			thinkingMenuOpen = !thinkingMenuOpen
			return map[string]any{
				"ok": true, "count": 1, "activated": true,
			}, nil
		case strings.Contains(expression, "thinkingKnown"):
			ready := len(browser.Reloads) >= 2
			thinkingOptions := []map[string]any{}
			if thinkingMenuOpen {
				thinkingOptions = append(thinkingOptions, map[string]any{
					"label": "High", "checked": true, "ready": true,
					"x": 10, "y": 10,
				})
			}
			return map[string]any{
				"editor":             map[string]any{"ready": ready},
				"chat_count":         boolInt(ready),
				"work_count":         boolInt(ready),
				"chat_selected":      ready,
				"chat":               map[string]any{"ready": ready},
				"specialized_count":  0,
				"picker_count":       boolInt(ready),
				"picker":             map[string]any{"ready": ready},
				"selected_thinking":  "High",
				"thinking_menu_open": thinkingMenuOpen,
				"thinking_options":   thinkingOptions,
				"model_menu_open":    false,
			}, nil
		case strings.Contains(expression, "send_ready:"):
			promptMatches := browser.InsertedText == prompt
			if browser.InsertedText == "" {
				promptMatches = true
			}
			sendReady := browser.InsertedText == prompt
			return map[string]any{
				"route_ready":               true,
				"editor_ready":              true,
				"editor_count":              1,
				"prompt_matches":            promptMatches,
				"inner_text_matches":        promptMatches,
				"text_content_matches":      promptMatches,
				"canonical_matches":         promptMatches,
				"chat_count":                1,
				"work_count":                1,
				"chat_selected":             true,
				"intelligence_count":        1,
				"selected_intelligence":     "High",
				"send_count":                boolInt(sendReady),
				"send_ready":                sendReady,
				"send_x":                    20,
				"send_y":                    30,
				"assistant_count":           0,
				"user_message_count":        0,
				"conversation_id":           "",
				"specialized_surface_count": 0,
			}, nil
		case strings.Contains(expression, "range.selectNodeContents"):
			return map[string]any{"ok": true}, nil
		case strings.Contains(expression, "terminal_control_present"):
			if browser.SendCount == 0 {
				return map[string]any{}, nil
			}
			return map[string]any{
				"route_matches":            true,
				"conversation_id":          conversationID,
				"text":                     answer,
				"prompt_candidates":        []string{prompt},
				"is_streaming":             false,
				"terminal_control_present": true,
				"assistant_count":          1,
				"user_message_count":       1,
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
		Timeout:         2 * time.Second,
		ComposerTimeout: time.Second,
		PollInterval:    time.Millisecond,
		Now:             testsupport.FixedNow,
		Send:            browserflow.DispatchFunc(browserflow.PressEnter),
	}

	result := Ask(context.Background(), config, prompt)
	data, _ := result.Data.(AskData)
	if !result.OK ||
		result.State != webagent.StateTerminal ||
		result.Action == nil ||
		result.Action.RawInputCount != 1 ||
		data.Intelligence != "High" ||
		data.Text != answer {
		t.Fatalf(
			"Ask result=%+v error=%+v data=%+v",
			result,
			result.Error,
			data,
		)
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
	if lastMethodIndex(trace, "Page.reload") >=
		lastMethodIndex(trace, "Input.insertText") {
		t.Fatalf("reload occurred after prompt mutation: %v", trace)
	}
}

func TestAskHydratedAttachmentOnlyCompletionRequiresTerminalEnvelope(
	t *testing.T,
) {
	const (
		prompt         = "Generate an image."
		conversationID = "conversation-1"
	)
	tests := []struct {
		name            string
		withAttachment  bool
		asyncStatus     int
		recipient       string
		wantState       webagent.State
		wantCompletion  string
		wantAttachments int
	}{
		{
			name:            "terminal image only",
			withAttachment:  true,
			asyncStatus:     4,
			recipient:       "all",
			wantState:       webagent.StateTerminal,
			wantCompletion:  conversationCompletionTerminal,
			wantAttachments: 1,
		},
		{
			name:           "terminal empty without attachments",
			asyncStatus:    4,
			recipient:      "all",
			wantState:      webagent.StateIncomplete,
			wantCompletion: conversationCompletionIncomplete,
		},
		{
			name:           "active image only",
			withAttachment: true,
			asyncStatus:    3,
			recipient:      "all",
			wantState:      webagent.StateIncomplete,
			wantCompletion: conversationCompletionIncomplete,
		},
		{
			name:           "non-terminal image envelope",
			withAttachment: true,
			asyncStatus:    4,
			recipient:      "python",
			wantState:      webagent.StateIncomplete,
			wantCompletion: conversationCompletionIncomplete,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := hydratedAskDetailPayload(
				t,
				prompt,
				test.withAttachment,
				test.asyncStatus,
				test.recipient,
			)
			result := runHydratedAskTest(
				t,
				prompt,
				conversationID,
				payload,
			)
			data, ok := result.Data.(AskData)
			if !ok {
				t.Fatalf("Ask data type = %T", result.Data)
			}
			if !result.OK ||
				result.State != test.wantState ||
				data.CompletionState != test.wantCompletion ||
				data.Text != "" ||
				len(data.Attachments) != test.wantAttachments ||
				data.DetailReadAttempts != 1 ||
				result.Action == nil ||
				result.Action.RawInputCount != 1 {
				t.Fatalf(
					"Ask result=%+v error=%+v data=%+v",
					result,
					result.Error,
					data,
				)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("Ask result validation: %v", err)
			}
		})
	}
}

func TestAskAllowsPreparationAndSendWithoutProviderGate(t *testing.T) {
	const (
		prompt         = "Review ChatGPT throttle scope"
		conversationID = "87654321-4321-4321-4321-cba987654321"
		answer         = "The shared transport gate remained short lived."
		stageTimeout   = 10 * time.Second
	)
	preparationStarted := make(chan struct{})
	releasePreparation := make(chan struct{})
	sendArmed := make(chan struct{})
	releaseSend := make(chan struct{})
	resultReady := make(chan webagent.Result, 1)
	var releasePreparationOnce sync.Once
	var releaseSendOnce sync.Once
	releasePreparationBoundary := func() {
		releasePreparationOnce.Do(func() {
			close(releasePreparation)
		})
	}
	releaseSendBoundary := func() {
		releaseSendOnce.Do(func() {
			close(releaseSend)
		})
	}
	defer releasePreparationBoundary()
	defer releaseSendBoundary()

	stateDir := t.TempDir()
	client := testsupport.NewBrowser("user-page")
	thinkingMenuOpen := false
	promptObservations := 0
	client.Evaluate = func(expression string, browser *testsupport.Browser) (any, error) {
		switch {
		case strings.Contains(expression, `const kind = "editor"`):
			return map[string]any{
				"ok": true, "count": 1, "activated": true,
			}, nil
		case strings.Contains(expression, `const kind = "picker"`):
			thinkingMenuOpen = !thinkingMenuOpen
			return map[string]any{
				"ok": true, "count": 1, "activated": true,
			}, nil
		case strings.Contains(expression, "thinkingKnown"):
			thinkingOptions := []map[string]any{}
			if thinkingMenuOpen {
				thinkingOptions = append(thinkingOptions, map[string]any{
					"label": "High", "checked": true, "ready": true,
					"x": 10, "y": 10,
				})
			}
			return map[string]any{
				"editor":             map[string]any{"ready": true},
				"chat_count":         1,
				"work_count":         1,
				"chat_selected":      true,
				"chat":               map[string]any{"ready": true},
				"specialized_count":  0,
				"picker_count":       1,
				"picker":             map[string]any{"ready": true},
				"selected_thinking":  "High",
				"thinking_menu_open": thinkingMenuOpen,
				"thinking_options":   thinkingOptions,
				"model_menu_open":    false,
			}, nil
		case strings.Contains(expression, "send_ready:"):
			promptMatches := browser.InsertedText == prompt
			if promptMatches && browser.SendCount == 0 {
				promptObservations++
				switch promptObservations {
				case 1:
					close(preparationStarted)
					<-releasePreparation
				case 2:
					close(sendArmed)
					<-releaseSend
				}
			}
			return map[string]any{
				"route_ready":               true,
				"editor_ready":              true,
				"editor_count":              1,
				"prompt_matches":            promptMatches,
				"inner_text_matches":        promptMatches,
				"text_content_matches":      promptMatches,
				"canonical_matches":         promptMatches,
				"chat_count":                1,
				"work_count":                1,
				"chat_selected":             true,
				"intelligence_count":        1,
				"selected_intelligence":     "High",
				"send_count":                1,
				"send_ready":                true,
				"send_x":                    20,
				"send_y":                    30,
				"assistant_count":           0,
				"user_message_count":        0,
				"conversation_id":           "",
				"specialized_surface_count": 0,
			}, nil
		case strings.Contains(expression, "range.selectNodeContents"):
			return map[string]any{"ok": true}, nil
		case strings.Contains(expression, "terminal_control_present"):
			if browser.SendCount == 0 {
				return map[string]any{}, nil
			}
			return map[string]any{
				"route_matches":            true,
				"conversation_id":          conversationID,
				"text":                     answer,
				"prompt_candidates":        []string{prompt},
				"is_streaming":             false,
				"terminal_control_present": true,
				"assistant_count":          1,
				"user_message_count":       1,
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
		Timeout:         stageTimeout,
		ComposerTimeout: 5 * time.Second,
		PollInterval:    time.Millisecond,
		Now:             testsupport.FixedNow,
	}
	go func() {
		resultReady <- Ask(context.Background(), config, prompt)
	}()

	select {
	case <-preparationStarted:
	case result := <-resultReady:
		t.Fatalf("Ask ended before reversible preparation stalled: %+v", result)
	case <-time.After(stageTimeout):
		t.Fatal("Ask did not reach reversible preparation")
	}
	releasePreparationBoundary()

	select {
	case <-sendArmed:
	case result := <-resultReady:
		t.Fatalf("Ask ended before Send was armed: %+v", result)
	case <-time.After(stageTimeout):
		t.Fatal("Ask did not reach the armed Send boundary")
	}
	releaseSendBoundary()

	var result webagent.Result
	select {
	case result = <-resultReady:
	case <-time.After(stageTimeout):
		t.Fatal("Ask did not finish after releasing the Send boundary")
	}
	if !result.OK || result.State != webagent.StateTerminal {
		t.Fatalf("Ask result = %+v, error=%+v", result, result.Error)
	}
}

func lastMethodIndex(methods []string, wanted string) int {
	index := -1
	for i, method := range methods {
		if method == wanted {
			index = i
		}
	}
	return index
}

func hydratedAskDetailPayload(
	t *testing.T,
	prompt string,
	withAttachment bool,
	asyncStatus int,
	recipient string,
) string {
	t.Helper()
	parts := []any{""}
	contentType := "text"
	if withAttachment {
		contentType = "multimodal_text"
		parts = []any{map[string]any{
			"content_type":  "image_asset_pointer",
			"asset_pointer": "sediment://file_synthetic_generated_image",
			"alt_text":      "A synthetic generated image",
			"size_bytes":    1024,
			"width":         512,
			"height":        512,
		}}
	}
	payload := map[string]any{
		"conversation_id": "conversation-1",
		"async_status":    asyncStatus,
		"current_node":    "answer",
		"mapping": map[string]any{
			"answer": map[string]any{
				"parent": "prompt",
				"message": map[string]any{
					"author":    map[string]any{"role": "assistant"},
					"recipient": recipient,
					"status":    "finished_successfully",
					"end_turn":  true,
					"content": map[string]any{
						"content_type": contentType,
						"parts":        parts,
					},
				},
			},
			"prompt": map[string]any{
				"parent": "",
				"message": map[string]any{
					"author": map[string]any{"role": "user"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{prompt},
					},
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal hydrated Ask detail: %v", err)
	}
	return string(encoded)
}

func runHydratedAskTest(
	t *testing.T,
	prompt string,
	conversationID string,
	payload string,
) webagent.Result {
	t.Helper()
	stateDir := t.TempDir()
	client := testsupport.NewBrowser("user-page")
	thinkingMenuOpen := false
	client.Evaluate = func(
		expression string,
		browser *testsupport.Browser,
	) (any, error) {
		switch {
		case strings.Contains(expression, `const kind = "editor"`):
			return map[string]any{
				"ok": true, "count": 1, "activated": true,
			}, nil
		case strings.Contains(expression, `const kind = "picker"`):
			thinkingMenuOpen = !thinkingMenuOpen
			return map[string]any{
				"ok": true, "count": 1, "activated": true,
			}, nil
		case strings.Contains(expression, "thinkingKnown"):
			thinkingOptions := []map[string]any{}
			if thinkingMenuOpen {
				thinkingOptions = append(thinkingOptions, map[string]any{
					"label": "High", "checked": true, "ready": true,
					"x": 10, "y": 10,
				})
			}
			return map[string]any{
				"editor":             map[string]any{"ready": true},
				"chat_count":         1,
				"work_count":         1,
				"chat_selected":      true,
				"chat":               map[string]any{"ready": true},
				"specialized_count":  0,
				"picker_count":       1,
				"picker":             map[string]any{"ready": true},
				"selected_thinking":  "High",
				"thinking_menu_open": thinkingMenuOpen,
				"thinking_options":   thinkingOptions,
				"model_menu_open":    false,
			}, nil
		case strings.Contains(expression, "send_ready:"):
			promptMatches := browser.InsertedText == "" ||
				browser.InsertedText == prompt
			sendReady := browser.InsertedText == prompt
			return map[string]any{
				"route_ready":               true,
				"editor_ready":              true,
				"editor_count":              1,
				"prompt_matches":            promptMatches,
				"inner_text_matches":        promptMatches,
				"text_content_matches":      promptMatches,
				"canonical_matches":         promptMatches,
				"chat_count":                1,
				"work_count":                1,
				"chat_selected":             true,
				"intelligence_count":        1,
				"selected_intelligence":     "High",
				"send_count":                boolInt(sendReady),
				"send_ready":                sendReady,
				"send_x":                    20,
				"send_y":                    30,
				"assistant_count":           0,
				"user_message_count":        0,
				"conversation_id":           "",
				"specialized_surface_count": 0,
			}, nil
		case strings.Contains(expression, "range.selectNodeContents"):
			return map[string]any{"ok": true}, nil
		case strings.Contains(expression, "terminal_control_present"):
			if browser.SendCount == 0 {
				return map[string]any{}, nil
			}
			return map[string]any{
				"route_matches":            true,
				"conversation_id":          conversationID,
				"text":                     "",
				"prompt_candidates":        []string{prompt},
				"is_streaming":             false,
				"terminal_control_present": true,
				"assistant_count":          1,
				"user_message_count":       1,
			}, nil
		case strings.Contains(expression, "const response = await fetch"):
			return map[string]any{
				"ok": true, "status_code": 200, "body": payload,
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
	return Ask(context.Background(), AskConfig{
		BrowserConfig: BrowserConfig{
			Client:      client,
			Engine:      engine,
			Journal:     journal,
			BuildCommit: "test-commit",
		},
		Store:           store,
		Timeout:         25 * time.Millisecond,
		ComposerTimeout: time.Second,
		PollInterval:    time.Millisecond,
		Now:             testsupport.FixedNow,
		Send:            browserflow.DispatchFunc(browserflow.PressEnter),
	}, prompt)
}
