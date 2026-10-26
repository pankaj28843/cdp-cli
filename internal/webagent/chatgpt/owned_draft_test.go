package chatgpt

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
)

type ownedDraftJournal struct{ record browserflow.Record }

func (j ownedDraftJournal) Create(context.Context, browserflow.Record) error { return nil }
func (j ownedDraftJournal) Save(context.Context, browserflow.Record) error   { return nil }
func (j ownedDraftJournal) Load(_ context.Context, runID string) (browserflow.Record, error) {
	if runID != j.record.RunID {
		return browserflow.Record{}, os.ErrNotExist
	}
	return j.record, nil
}

func TestOwnedDraftReceiptRequiresCompletedClosedAsk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prior.json")
	fingerprint := fingerprintPrompt("owned draft")
	journal := ownedDraftJournal{record: browserflow.Record{
		RunID: "owned-run", Provider: "chatgpt", Operation: "ask",
		Phase: browserflow.PhaseClosed, Dispatch: browserflow.DispatchPerformed,
		RawInputCount: 1, ActionAttemptCount: 1, PendingPersisted: true,
		Cleanup:  browserflow.CleanupClosed,
		TargetID: "owned-target", ConversationID: "owned-conversation",
		InputFingerprint: fingerprint,
	}}
	prior := map[string]any{
		"ok":       true,
		"provider": "chatgpt", "operation": "ask", "state": "terminal", "stage": "closed",
		"action":       map[string]any{"dispatch": "performed", "attempt_count": 1, "raw_input_count": 1, "pending_persisted": true},
		"conversation": map[string]any{"id": "owned-conversation"},
		"cleanup":      map[string]any{"state": "closed", "target_closed": true, "target_id": "owned-target"},
		"evidence":     map[string]any{"run_id": "owned-run", "target": map[string]any{"target_id": "owned-target", "owned": true, "closed": true}},
		"data":         map[string]any{"prompt_fingerprint": fingerprint},
	}
	write := func() {
		t.Helper()
		data, err := json.Marshal(prior)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if got, err := ownedDraftFingerprintFromReceipt(context.Background(), path, journal); err != nil || got != fingerprint {
		t.Fatalf("completed receipt rejected: %q %v", got, err)
	}
	for _, change := range []func(){
		func() { prior["state"] = "failed" },
		func() { prior["action"] = map[string]any{"dispatch": "not_performed", "raw_input_count": 0} },
		func() { prior["data"] = map[string]any{"prompt_fingerprint": "invalid"} },
	} {
		change()
		write()
		if _, err := ownedDraftFingerprintFromReceipt(context.Background(), path, journal); err == nil {
			t.Fatal("unproved owned draft receipt accepted")
		}
	}
}

func TestOwnedDraftReceiptAcceptsOneSentIdentityUnprovedAsk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prior.json")
	fingerprint := fingerprintPrompt("owned sent draft")
	record := browserflow.Record{
		RunID: "sent-run", Provider: "chatgpt", Operation: "ask",
		Phase: browserflow.PhaseIncomplete, Dispatch: browserflow.DispatchPerformed,
		RawInputCount: 1, ActionAttemptCount: 1, PendingPersisted: true,
		Cleanup: browserflow.CleanupPending, TargetID: "sent-target",
		InputFingerprint: fingerprint,
	}
	prior := map[string]any{
		"ok": false, "provider": "chatgpt", "operation": "ask",
		"state": "failed", "stage": "action_dispatched",
		"action": map[string]any{"dispatch": "performed", "attempt_count": 1,
			"raw_input_count": 1, "pending_persisted": true, "retry_safe": false},
		"error":   map[string]any{"code": "chatgpt_submission_identity_unproven", "retry_safe": false},
		"cleanup": map[string]any{"state": "pending", "target_id": "sent-target"},
		"evidence": map[string]any{"run_id": "sent-run", "target": map[string]any{
			"target_id": "sent-target", "owned": true, "closed": false}},
		"data": map[string]any{"prompt_fingerprint": fingerprint},
	}
	write := func() {
		t.Helper()
		data, err := json.Marshal(prior)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if got, err := ownedDraftFingerprintFromReceipt(context.Background(), path, ownedDraftJournal{record}); err != nil || got != fingerprint {
		t.Fatalf("one-Send identity-unproved receipt rejected: %q %v", got, err)
	}
	for _, change := range []func(){
		func() { prior["error"] = map[string]any{"code": "other_failure", "retry_safe": false} },
		func() {
			prior["action"] = map[string]any{"dispatch": "performed", "attempt_count": 1,
				"raw_input_count": 0, "pending_persisted": true}
		},
	} {
		change()
		write()
		if _, err := ownedDraftFingerprintFromReceipt(context.Background(), path, ownedDraftJournal{record}); err == nil {
			t.Fatal("unproved one-Send receipt accepted")
		}
	}
	prior["error"] = map[string]any{"code": "chatgpt_submission_identity_unproven", "retry_safe": false}
	prior["action"] = map[string]any{"dispatch": "performed", "attempt_count": 1,
		"raw_input_count": 1, "pending_persisted": true}
	write()
	record.InputFingerprint = fingerprintPrompt("different prompt")
	if _, err := ownedDraftFingerprintFromReceipt(context.Background(), path, ownedDraftJournal{record}); err == nil {
		t.Fatal("journal fingerprint mismatch accepted")
	}
}

func TestOwnedDraftMismatchCannotClearOrSend(t *testing.T) {
	client := testsupport.NewBrowser("owned-probe")
	client.Evaluate = func(expression string, _ *testsupport.Browser) (any, error) {
		if strings.Contains(expression, "send_ready:") {
			return map[string]any{"route_ready": true, "editor_ready": true, "editor_count": 1,
				"canonical_text": "unknown user draft", "canonical_characters": 18,
				"draft_characters": 18, "user_message_count": 0, "assistant_count": 0}, nil
		}
		return true, nil
	}
	session, err := cdp.AttachToTargetWithClient(context.Background(), client, "owned-probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clearExactOwnedRestoredDraft(context.Background(), client, session, fingerprintPrompt("owned draft")); err == nil {
		t.Fatal("unknown draft accepted")
	}
	counts, _, _, _, inserts, sends, _ := client.Snapshot()
	if inserts != 0 || sends != 0 || counts["Page.navigate"] != 0 {
		t.Fatalf("mismatched draft mutated: counts=%v inserts=%d keys=%d", counts, inserts, sends)
	}
}

func TestOwnedDraftClearRequiresStableBlankCurrentComposer(t *testing.T) {
	client := testsupport.NewBrowser("owned-probe")
	client.Evaluate = func(expression string, browser *testsupport.Browser) (any, error) {
		if strings.Contains(expression, "send_ready:") {
			if browser.Counts["Input.dispatchKeyEvent"] >= 3 {
				return map[string]any{"document_ready_state": "complete", "document_time_origin": 1,
					"route_ready": true, "editor_ready": true, "editor_count": 1}, nil
			}
			return map[string]any{"document_ready_state": "complete", "document_time_origin": 1,
				"route_ready": true, "editor_ready": true, "editor_count": 1,
				"canonical_text": "owned draft", "canonical_characters": 11,
				"draft_characters": 11}, nil
		}
		return true, nil
	}
	session, err := cdp.AttachToTargetWithClient(context.Background(), client, "owned-probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if phase, err := clearExactOwnedRestoredDraft(context.Background(), client, session, fingerprintPrompt("owned draft")); err != nil || phase != "blank_stable" {
		t.Fatal(err)
	}
	counts, _, _, _, inserts, _, _ := client.Snapshot()
	if inserts != 0 || counts["Page.reload"] != 0 || counts["Input.dispatchKeyEvent"] != 4 {
		t.Fatalf("native clear/rehydration missing: counts=%v inserts=%d", counts, inserts)
	}
}

func TestOwnedDraftPostClearObservationFailureIsNotReplaySafe(t *testing.T) {
	stateDir := t.TempDir()
	client := testsupport.NewBrowser()
	client.Evaluate = func(expression string, browser *testsupport.Browser) (any, error) {
		if strings.Contains(expression, "send_ready:") {
			if browser.Counts["Input.dispatchKeyEvent"] >= 3 {
				return map[string]any{"document_ready_state": "complete", "document_time_origin": 1,
					"route_ready": true, "editor_ready": true, "editor_count": 1}, nil
			}
			return map[string]any{"document_ready_state": "complete", "document_time_origin": 1,
				"route_ready": true, "editor_ready": true, "editor_count": 1,
				"canonical_text": "owned draft", "canonical_characters": 11,
				"draft_characters": 11}, nil
		}
		return true, nil
	}
	engine, journal, err := testsupport.NewRuntime(stateDir, client)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := fingerprintPrompt("owned draft")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := journal.Create(context.Background(), browserflow.Record{
		SchemaVersion: browserflow.RecoverySchemaVersion, RunID: "owned-run",
		Provider: "chatgpt", Operation: "ask", Phase: browserflow.PhaseClosed,
		Dispatch: browserflow.DispatchPerformed, RawInputCount: 1, ActionAttemptCount: 1, PendingPersisted: true,
		TargetID: "prior-target", SessionID: "prior-session", ConversationID: "prior-conversation",
		InputFingerprint: fingerprint, Cleanup: browserflow.CleanupClosed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "prior-result.json")
	prior := map[string]any{
		"ok": true, "provider": "chatgpt", "operation": "ask", "state": "terminal", "stage": "closed",
		"action":       map[string]any{"dispatch": "performed", "attempt_count": 1, "raw_input_count": 1, "pending_persisted": true},
		"conversation": map[string]any{"id": "prior-conversation"},
		"cleanup":      map[string]any{"state": "closed", "target_closed": true, "target_id": "prior-target"},
		"evidence":     map[string]any{"run_id": "owned-run", "target": map[string]any{"target_id": "prior-target", "owned": true, "closed": true}},
		"data":         map[string]any{"prompt_fingerprint": fingerprint},
	}
	encoded, _ := json.Marshal(prior)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	result := Ask(context.Background(), AskConfig{
		BrowserConfig: BrowserConfig{Client: client, Engine: engine, Journal: journal},
		Store:         store, OwnedDraftReceiptPath: path, ComposerTimeout: time.Second, Timeout: time.Second,
	}, "new request")
	if result.Error == nil || result.Error.Code != "chatgpt_owned_draft_cleanup_unproved" ||
		result.Error.RetrySafe || result.Action != nil {
		t.Fatalf("post-clear failure replay policy: %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("post-clear result schema: %v", err)
	}
	data := result.Data.(AskData)
	if data.Metadata["owned_draft_cleared"] != true {
		t.Fatalf("clear path was not reached: %+v", data.Metadata)
	}
	counts, _, _, _, inserts, _, _ := client.Snapshot()
	if inserts != 0 || counts["Page.reload"] != 0 || counts["Input.dispatchKeyEvent"] != 4 {
		t.Fatalf("new ask input or rehydration mismatch: %+v", counts)
	}
}
