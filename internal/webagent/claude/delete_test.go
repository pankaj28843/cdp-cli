package claude

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestDeleteConversationProvesExactTargetPostcondition(t *testing.T) {
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	client.deleteRoute = true
	config := newDeleteTestConfig(t, stateDir, client)

	result := DeleteConversation(context.Background(), config, "conversation-1")

	if !result.OK ||
		result.State != webagent.StateTerminal ||
		result.Operation != webagent.OperationConversationsDelete ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchPerformed ||
		result.Action.AttemptCount != 1 ||
		result.Action.RawInputCount != 1 ||
		result.Evidence.Target == nil ||
		!result.Evidence.Target.Closed ||
		result.Cleanup.State != webagent.CleanupClosed {
		t.Fatalf("DeleteConversation result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
	data, ok := result.Data.(DeleteData)
	if !ok ||
		data.CompletionState != "deleted" ||
		data.Postcondition != deletePostconditionProof ||
		data.PreparationAttempts < 3 {
		t.Fatalf("delete data = %#v", result.Data)
	}
	if client.callCount("Input.dispatchMouseEvent") != 9 ||
		client.callCount("Target.createTarget") != 1 ||
		client.callCount("Target.closeTarget") != 1 ||
		client.hasTarget("owned-1") ||
		!client.hasTarget("user-page") {
		t.Fatalf("CDP counts=%+v targets=%+v", client.countSnapshot(), client.targets)
	}
	record, err := config.Journal.Load(context.Background(), result.Evidence.RunID)
	if err != nil {
		t.Fatalf("load recovery record: %v", err)
	}
	if record.Phase != browserflow.PhaseClosed ||
		record.Dispatch != browserflow.DispatchPerformed ||
		record.RawInputCount != 1 ||
		record.Postcondition != deletePostconditionProof {
		t.Fatalf("recovery record = %+v", record)
	}
}

func TestDeleteConversationFailsBeforeConfirmationWhenRouteIsUnproved(t *testing.T) {
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	config := newDeleteTestConfig(t, stateDir, client)
	config.Timeout = 3 * time.Millisecond
	config.PollInterval = time.Millisecond

	result := DeleteConversation(context.Background(), config, "conversation-1")

	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "claude_delete_prepare_failed" ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchNotPerformed ||
		client.callCount("Input.dispatchMouseEvent") != 0 ||
		result.Evidence.Target == nil ||
		!result.Evidence.Target.Closed {
		t.Fatalf("route-unproved result=%+v counts=%+v", result, client.countSnapshot())
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
}

func TestDeleteConversationRefinesAmbiguousClickOnlyWithPostcondition(t *testing.T) {
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	client.deleteRoute = true
	config := newDeleteTestConfig(t, stateDir, client)
	config.Confirm = browserflow.DispatchFunc(
		func(context.Context, *cdp.PageSession) (browserflow.DispatchOutcome, error) {
			client.mu.Lock()
			client.deleteStage = 3
			client.mu.Unlock()
			return browserflow.DispatchOutcome{
				Dispatch:          browserflow.DispatchUnknown,
				RawInputAttempted: true,
			}, errors.New("private ambiguous transport canary")
		},
	)

	result := DeleteConversation(context.Background(), config, "conversation-1")

	if !result.OK ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchPerformed ||
		result.Data.(DeleteData).Postcondition != deletePostconditionProof {
		t.Fatalf("postcondition-refined result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
}

func TestDeleteConversationNeverRetriesUnconfirmedAmbiguousClick(t *testing.T) {
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	client.deleteRoute = true
	config := newDeleteTestConfig(t, stateDir, client)
	config.Timeout = 5 * time.Millisecond
	confirmCalls := 0
	config.Confirm = browserflow.DispatchFunc(
		func(context.Context, *cdp.PageSession) (browserflow.DispatchOutcome, error) {
			confirmCalls++
			return browserflow.DispatchOutcome{
				Dispatch:          browserflow.DispatchUnknown,
				RawInputAttempted: true,
			}, errors.New("private ambiguous transport canary")
		},
	)

	result := DeleteConversation(context.Background(), config, "conversation-1")

	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "claude_delete_unconfirmed" ||
		result.Error.RetrySafe ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchUnknown ||
		confirmCalls != 1 {
		t.Fatalf("ambiguous result=%+v confirm_calls=%d", result, confirmCalls)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
}

func TestDeleteConversationRejectsInvalidIDWithoutOpeningBrowser(t *testing.T) {
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	config := newDeleteTestConfig(t, stateDir, client)

	result := DeleteConversation(context.Background(), config, "../private")

	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "claude_invalid_conversation_id" ||
		client.callCount("Target.createTarget") != 0 {
		t.Fatalf("invalid-id result=%+v counts=%+v", result, client.countSnapshot())
	}
}

func newDeleteTestConfig(
	t *testing.T,
	stateDir string,
	client *authFakeClient,
) DeleteConfig {
	t.Helper()
	journal, err := browserflow.NewFileJournal(stateDir)
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	gate, err := admission.New(admission.Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("admission.New: %v", err)
	}
	engine, err := browserflow.New(browserflow.Config{
		Client:  client,
		Journal: journal,
		Budget: cdp.BrowserResourceBudgetOptions{
			MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
		},
	})
	if err != nil {
		t.Fatalf("browserflow.New: %v", err)
	}
	return DeleteConfig{
		Client:       client,
		Engine:       engine,
		Journal:      journal,
		Admission:    gate,
		BuildCommit:  "test-commit",
		Timeout:      100 * time.Millisecond,
		PollInterval: time.Millisecond,
	}
}
