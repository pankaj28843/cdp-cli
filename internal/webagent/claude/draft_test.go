package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAskPreservesExistingDraftBeforeInput(t *testing.T) {
	for _, files := range []bool{false, true} {
		for _, attachment := range []bool{false, true} {
			name := "text"
			if attachment {
				name = "attachment"
			}
			if files {
				name += "_with_files"
			}
			t.Run(name, func(t *testing.T) {
				const draft = "Existing synthetic draft"
				client := newAuthFakeClient("user-page")
				client.insertedPrompt = draft
				if attachment {
					client.draftAttachmentCount = 1
				} else {
					client.draftTextCharacters = len(draft)
				}
				client.ackConversationID = "synthetic-conversation"
				config := newAskTestConfig(t, t.TempDir(), client)
				config.HTTPClient = terminalDetailClient("Synthetic request")
				config.Model = "Test Model"
				client.selectionRowCount = 1
				if files {
					path := filepath.Join(t.TempDir(), "synthetic.txt")
					if err := os.WriteFile(path, []byte("Synthetic attachment"), 0600); err != nil {
						t.Fatal(err)
					}
					config.FilePaths = []string{path}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				defer cancel()
				result := Ask(ctx, config, "Synthetic request")
				if result.OK || result.Error == nil || result.Error.Code != "claude_existing_draft" {
					t.Fatalf("existing draft was not rejected: state=%s error=%+v", result.State, result.Error)
				}
				for _, method := range []string{"Input.insertText", "Input.dispatchKeyEvent", "Input.dispatchMouseEvent", "DOM.setFileInputFiles"} {
					if client.callCount(method) != 0 {
						t.Fatalf("existing draft received %s", method)
					}
				}
				if client.insertedPrompt != draft || result.Action == nil || result.Action.AttemptCount != 0 || result.Action.RawInputCount != 0 || result.Cleanup.TargetClosed != true {
					t.Fatalf("draft/action/cleanup changed: action=%+v cleanup=%+v", result.Action, result.Cleanup)
				}
				if client.hasTarget("owned-1") || !client.hasTarget("user-page") {
					t.Fatal("exact cleanup failed")
				}
			})
		}
	}
}
