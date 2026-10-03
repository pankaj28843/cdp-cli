package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/testsupport"
)

type attachmentBoundary struct {
	*authFakeClient
	assignmentError, rejected, removed, focusUnavailable, ambiguous, occupied, sendUnavailable bool
	assignments                                                                                int
	assigned                                                                                   []string
}

func (b *attachmentBoundary) CallSession(ctx context.Context, session, method string, params any, result any) error {
	var value any
	switch method {
	case "Runtime.evaluate":
		expression := authStringParam(params, "expression")
		switch {
		case strings.Contains(expression, "claudeAttachmentInput"):
			value = map[string]any{"ready": !b.occupied, "occupied": b.occupied}
		case strings.Contains(expression, "claudeAttachmentNames"):
			value = map[string]any{"ready": !b.rejected && !(b.removed && b.insertedPrompt != ""), "failed": b.rejected, "send_ready": !b.sendUnavailable}
		case b.focusUnavailable && strings.Contains(expression, "target_found"):
			value = map[string]any{"target_found": true, "focused": false}
		default:
			return b.authFakeClient.CallSession(ctx, session, method, params, result)
		}
		return assignAuthJSON(result, map[string]any{"result": map[string]any{"type": "object", "value": value}})
	case "DOM.getDocument":
		value = map[string]any{"root": map[string]any{"nodeId": 1}}
	case "DOM.querySelectorAll":
		ids := []int{2}
		if b.ambiguous {
			ids = append(ids, 3)
		}
		value = map[string]any{"nodeIds": ids}
	case "DOM.setFileInputFiles":
		b.assignments++
		encoded, _ := json.Marshal(params)
		var input struct {
			Files []string `json:"files"`
		}
		if err := json.Unmarshal(encoded, &input); err != nil {
			return err
		}
		b.assigned = input.Files
		if b.assignmentError {
			return errors.New("uncertain assignment")
		}
		value = map[string]any{}
	default:
		return b.authFakeClient.CallSession(ctx, session, method, params, result)
	}
	return assignAuthJSON(result, value)
}

func TestAskAttachmentBatchAndFailureBeforeSend(t *testing.T) {
	for _, name := range []string{"ready_batch", "assignment_unknown", "rejected", "preview_removed", "focus_unavailable", "ambiguous_input", "occupied_draft", "send_unavailable"} {
		t.Run(name, func(t *testing.T) {
			const prompt = "Review two synthetic Claude files"
			dir := t.TempDir()
			b := &attachmentBoundary{authFakeClient: newAuthFakeClient("user-page"), assignmentError: name == "assignment_unknown", rejected: name == "rejected", removed: name == "preview_removed", focusUnavailable: name == "focus_unavailable", ambiguous: name == "ambiguous_input", occupied: name == "occupied_draft", sendUnavailable: name == "send_unavailable"}
			b.ackConversationID = "conversation-1"
			config := newAskTestConfig(t, dir, b.authFakeClient)
			config.HTTPClient = terminalDetailClient(prompt)
			engine, journal, err := testsupport.NewRuntime(dir, b)
			if err != nil {
				t.Fatal(err)
			}
			config.Engine = engine
			config.Journal = journal
			config.Client = b
			for _, filename := range []string{"first.txt", "second.md"} {
				p := filepath.Join(dir, filename)
				if err := os.WriteFile(p, []byte("synthetic fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				config.FilePaths = append(config.FilePaths, p)
			}
			result := Ask(context.Background(), config, prompt)
			wantOK := name == "ready_batch"
			if result.OK != wantOK {
				t.Fatalf("OK=%v want %v result=%+v", result.OK, wantOK, result)
			}
			wantAssignments := 1
			if name == "ambiguous_input" || name == "occupied_draft" {
				wantAssignments = 0
			}
			if b.assignments != wantAssignments || (wantAssignments == 1 && len(b.assigned) != 2) {
				t.Fatalf("assignments=%d batch=%d", b.assignments, len(b.assigned))
			}
			if result.Action == nil || (wantOK && result.Action.RawInputCount != 1) || (!wantOK && (result.Action.RawInputCount != 0 || b.callCount("Input.dispatchKeyEvent") != 0)) {
				t.Fatalf("Send evidence=%+v", result.Action)
			}
			data := result.Data.(AskData)
			if b.assignmentError && data.Metadata["attachment_prepare_stage"] != "assignment" {
				t.Fatalf("uncertain assignment stage=%v", data.Metadata["attachment_prepare_stage"])
			}
			if wantAssignments == 1 {
				outcome := "confirmed"
				if b.assignmentError {
					outcome = "unknown"
				}
				for _, e := range data.InputAttachments {
					if e.AssignmentAttempts != 1 || e.AssignmentOutcome != outcome {
						t.Fatalf("assignment evidence=%+v", e)
					}
				}
			}
			if b.hasTarget("owned-1") || !b.hasTarget("user-page") {
				t.Fatal("owned cleanup or user target preservation failed")
			}
		})
	}
}

func TestInvalidAttachmentFailsBeforeBrowser(t *testing.T) {
	result := Ask(context.Background(), AskConfig{FilePaths: []string{t.TempDir()}}, "Synthetic prompt")
	if result.Error == nil || result.Error.Code != "claude_attachment_invalid" || result.Action.RawInputCount != 0 || result.Cleanup.Required {
		t.Fatalf("result=%+v", result)
	}
}
