package claude

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestCalibrateUsesOneTargetForSendCaptureDeleteAndClose(t *testing.T) {
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	client.ackConversationID = "conversation-calibration"
	client.renderedDetailText = "Persisting first separates intent from ambiguous transport."
	client.renderedDetailPrompt = calibrationPrompt
	client.deleteRoute = true
	config := newCalibrationTestConfig(t, stateDir, client)

	result := Calibrate(context.Background(), config)

	if !result.OK ||
		result.State != webagent.StateTerminal ||
		result.Operation != webagent.OperationCalibrate ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchPerformed ||
		result.Evidence.Target == nil ||
		!result.Evidence.Target.Closed ||
		result.Cleanup.State != webagent.CleanupClosed {
		t.Fatalf("Calibrate result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Calibrate validation: %v", err)
	}
	data, ok := result.Data.(CalibrationData)
	if !ok ||
		data.CompletionState != "deleted" ||
		!data.AnswerCaptured ||
		data.AnswerCharacters == 0 ||
		data.SendAction == nil ||
		data.SendAction.Dispatch != webagent.DispatchPerformed ||
		data.DeleteAction == nil ||
		data.DeleteAction.Dispatch != webagent.DispatchPerformed ||
		data.Postcondition != deletePostconditionProof {
		t.Fatalf("calibration data = %#v", result.Data)
	}
	if client.callCount("Target.createTarget") != 1 ||
		client.callCount("Target.closeTarget") != 1 ||
		client.callCount("Input.dispatchKeyEvent") != 2 ||
		client.callCount("Input.dispatchMouseEvent") != 9 ||
		client.hasTarget("owned-1") ||
		!client.hasTarget("user-page") {
		t.Fatalf("CDP counts=%+v targets=%+v", client.countSnapshot(), client.targets)
	}
	record, err := config.Journal.Load(context.Background(), result.Evidence.RunID)
	if err != nil {
		t.Fatalf("load recovery record: %v", err)
	}
	if record.Phase != browserflow.PhaseClosed ||
		record.ActionName != "delete" ||
		record.Dispatch != browserflow.DispatchPerformed ||
		len(record.CompletedActions) != 1 ||
		record.CompletedActions[0].Name != "send" ||
		record.CompletedActions[0].Dispatch != browserflow.DispatchPerformed {
		t.Fatalf("calibration recovery record = %+v", record)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(encoded), calibrationPrompt) {
		t.Fatalf("calibration prompt leaked into result: %s", encoded)
	}
}

func TestCalibrateNeverRetriesAmbiguousDelete(t *testing.T) {
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	client.ackConversationID = "conversation-calibration"
	client.renderedDetailText = "Useful terminal answer."
	client.renderedDetailPrompt = calibrationPrompt
	client.deleteRoute = true
	config := newCalibrationTestConfig(t, stateDir, client)
	config.Timeout = time.Second
	confirmCalls := 0
	config.Confirm = browserflow.DispatchFunc(
		func(context.Context, *cdp.PageSession) (browserflow.DispatchOutcome, error) {
			confirmCalls++
			return browserflow.DispatchOutcome{
				Dispatch:          browserflow.DispatchUnknown,
				RawInputAttempted: true,
			}, errors.New("private ambiguous delete transport")
		},
	)

	result := Calibrate(context.Background(), config)

	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "claude_calibration_delete_unconfirmed" ||
		result.Error.RetrySafe ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchUnknown ||
		confirmCalls != 1 {
		t.Fatalf("ambiguous calibration=%+v confirm_calls=%d", result, confirmCalls)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("ambiguous calibration validation: %v", err)
	}
}

func newCalibrationTestConfig(
	t *testing.T,
	stateDir string,
	client *authFakeClient,
) CalibrationConfig {
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
	store, err := NewCalibrationStore(stateDir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}
	return CalibrationConfig{
		Client:          client,
		Engine:          engine,
		Journal:         journal,
		Admission:       gate,
		Store:           store,
		BuildCommit:     "test-commit",
		Timeout:         2 * time.Second,
		ComposerTimeout: 50 * time.Millisecond,
		PollInterval:    time.Millisecond,
	}
}
