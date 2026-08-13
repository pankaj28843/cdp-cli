package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
)

func TestChatGPTAttachmentBatchCommandIsDiscoverable(t *testing.T) {
	root := (&app{out: &bytes.Buffer{}, err: &bytes.Buffer{}}).newRoot()
	command, _, err := root.Find([]string{
		"workflow", "agent", "chatgpt", "conversations",
		"download-attachments",
	})
	if err != nil || command == nil {
		t.Fatalf("download-attachments command not found: %v", err)
	}
	if command.Flag("output-dir") == nil ||
		!strings.Contains(command.Use, "CONVERSATION_ID") ||
		!strings.Contains(command.Long, "never overwritten") {
		t.Fatalf("attachment batch command = %+v", command)
	}
}

func TestSelectHeadedProviderRuntimeOverridesAmbientDefaultOnly(t *testing.T) {
	t.Run("ambient default", func(t *testing.T) {
		a := &app{opts: options{}}
		a.newRoot()
		t.Setenv("CDP_BROWSER_MODE", "headless")
		if !a.selectHeadedProviderRuntime() {
			t.Fatal("ambient headless default was not overridden")
		}
		if got := a.browserModeName(); got != "headed" {
			t.Fatalf("provider browser mode = %q, want headed", got)
		}
	})

	t.Run("explicit headless", func(t *testing.T) {
		a := &app{opts: options{}}
		root := a.newRoot()
		if err := root.PersistentFlags().Set("browser-mode", "headless"); err != nil {
			t.Fatalf("set browser mode: %v", err)
		}
		if a.selectHeadedProviderRuntime() {
			t.Fatal("explicit headless mode was silently overridden")
		}
	})
}

func TestRenderWebAgentFailurePreservesEnvelopeAndNonzeroExit(t *testing.T) {
	var out bytes.Buffer
	a := &app{
		out:  &out,
		err:  &bytes.Buffer{},
		opts: options{json: true},
	}
	result := webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationDoctor,
		State:         webagent.StateFailed,
		Stage:         webagent.StagePlanned,
		Error: &webagent.OperationError{
			Code:      "claude_auth_missing",
			ErrClass:  "auth",
			Message:   "Claude auth evidence is missing",
			RetrySafe: true,
		},
		Data: map[string]any{"auth_state": "missing"},
		Evidence: webagent.Evidence{
			RunID:       "wa-render-failure",
			BuildCommit: "abc123",
			BrowserMode: "none",
			ReadMode:    "local_metadata",
		},
		Cleanup:      webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{"cdp workflow agent claude auth refresh --json"},
	}

	err := a.renderWebAgentResult(context.Background(), "Claude auth missing", result)
	var rendered *renderedResultExit
	if !errors.As(err, &rendered) {
		t.Fatalf("render error = %v, want renderedResultExit", err)
	}
	if rendered.ExitCode != ExitPermission {
		t.Fatalf("exit code = %d, want %d", rendered.ExitCode, ExitPermission)
	}
	var got webagent.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode rendered result: %v\n%s", err, out.String())
	}
	if got.OK || got.Error == nil || got.Error.Code != "claude_auth_missing" {
		t.Fatalf("rendered result = %+v", got)
	}
}

func TestRenderWebAgentResultPreservesAttachmentOnlyChatGPTDetail(
	t *testing.T,
) {
	var out bytes.Buffer
	a := &app{
		out:  &out,
		err:  &bytes.Buffer{},
		opts: options{json: true},
	}
	result := webagent.Result{
		OK:            true,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderChatGPT,
		Operation:     webagent.OperationConversationsDetail,
		State:         webagent.StateTerminal,
		Stage:         webagent.StageObserveTerminal,
		Data: chatgpt.ConversationDetailData{
			SchemaVersion:  chatgpt.ConversationDetailSchemaVersion,
			StatusCode:     200,
			ConversationID: "conversation-image-only",
			Attachments: []chatgpt.ConversationAttachment{
				{
					Kind:      "image",
					Alt:       "Generated high-resolution image",
					Source:    "sediment://file_generated_image",
					SizeBytes: 2457600,
					Width:     1536,
					Height:    1024,
				},
			},
			CompletionState: "terminal",
			ReadMode:        "candidate_http",
			Metadata:        map[string]any{},
		},
		Evidence: webagent.Evidence{
			RunID:       "wa-render-attachment-only",
			BuildCommit: "test-commit",
			BrowserMode: "none",
			ReadMode:    "candidate_http",
		},
		Cleanup:      webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{},
	}

	if err := a.renderWebAgentResult(
		context.Background(),
		"chatgpt conversation detail: terminal",
		result,
	); err != nil {
		t.Fatalf("render attachment-only detail: %v", err)
	}
	var rendered struct {
		Data struct {
			Text        string                           `json:"text"`
			Attachments []chatgpt.ConversationAttachment `json:"attachments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &rendered); err != nil {
		t.Fatalf("decode rendered detail: %v\n%s", err, out.String())
	}
	if rendered.Data.Text != "" ||
		len(rendered.Data.Attachments) != 1 ||
		rendered.Data.Attachments[0].Source !=
			"sediment://file_generated_image" ||
		rendered.Data.Attachments[0].Width != 1536 ||
		rendered.Data.Attachments[0].Height != 1024 {
		t.Fatalf("rendered detail = %+v", rendered.Data)
	}
}
