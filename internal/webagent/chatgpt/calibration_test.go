package chatgpt

import (
	"context"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

type calibrationTestJournal struct {
	record browserflow.Record
	err    error
}

func (j calibrationTestJournal) Create(
	context.Context,
	browserflow.Record,
) error {
	return nil
}

func (j calibrationTestJournal) Save(
	context.Context,
	browserflow.Record,
) error {
	return nil
}

func (j calibrationTestJournal) Load(
	context.Context,
	string,
) (browserflow.Record, error) {
	return j.record, j.err
}

func TestCalibrationStatusIsBrowserFreeAndRecoveryAware(t *testing.T) {
	store, err := NewCalibrationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	status := CalibrationStatus(
		context.Background(),
		store,
		nil,
		"test",
	)
	if !status.OK {
		t.Fatalf("initial status = %+v", status)
	}
	data, ok := status.Data.(CalibrationStatusData)
	if !ok ||
		data.State != "not_run" ||
		!data.TargetClosed ||
		data.RecoveryRequired {
		t.Fatalf("initial status data = %+v", status.Data)
	}
	if err := status.Validate(); err != nil {
		t.Fatalf("initial status validation: %v", err)
	}

	record := CalibrationStateRecord{
		SchemaVersion:     CalibrationStateSchemaVersion,
		RunID:             "wa-calibration-test",
		State:             "answer_captured",
		PromptFingerprint: fingerprintPrompt(calibrationPrompt),
		TargetID:          "target-calibration",
		ConversationID:    "conversation-calibration",
		SendDispatch:      browserflow.DispatchPerformed,
		UpdatedAt: time.Now().UTC().Format(
			time.RFC3339Nano,
		),
	}
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatalf("save calibration state: %v", err)
	}
	status = CalibrationStatus(
		context.Background(),
		store,
		nil,
		"test",
	)
	data, ok = status.Data.(CalibrationStatusData)
	if !ok ||
		!data.ConversationPresent ||
		data.TargetClosed ||
		!data.RecoveryRequired ||
		data.SendDispatch != browserflow.DispatchPerformed {
		t.Fatalf("recovery status = %+v", status.Data)
	}
}

func TestCalibrationStatusMergesCompletedSameTargetActions(t *testing.T) {
	store, err := NewCalibrationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := CalibrationStateRecord{
		SchemaVersion:     CalibrationStateSchemaVersion,
		RunID:             "wa-calibration-merge",
		State:             "delete_pending",
		PromptFingerprint: fingerprintPrompt(calibrationPrompt),
		TargetID:          "target-calibration",
		ConversationID:    "conversation-calibration",
		UpdatedAt: time.Now().UTC().Format(
			time.RFC3339Nano,
		),
	}
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	journal := calibrationTestJournal{
		record: browserflow.Record{
			SchemaVersion:  browserflow.RecoverySchemaVersion,
			RunID:          record.RunID,
			Provider:       string(webagent.ProviderChatGPT),
			Operation:      string(webagent.OperationCalibrate),
			ActionName:     "delete",
			Phase:          browserflow.PhaseClosed,
			Cleanup:        browserflow.CleanupClosed,
			TargetID:       record.TargetID,
			ConversationID: record.ConversationID,
			Dispatch:       browserflow.DispatchPerformed,
			Postcondition:  deletePostconditionProof,
			CompletedActions: []browserflow.CompletedAction{
				{
					Name:               "send",
					Dispatch:           browserflow.DispatchPerformed,
					ActionAttemptCount: 1,
					RawInputCount:      1,
					PendingPersisted:   true,
					CompletionPhase:    browserflow.PhaseTerminal,
				},
			},
			CreatedAt: now.Format(time.RFC3339Nano),
			UpdatedAt: now.Format(time.RFC3339Nano),
		},
	}
	status := CalibrationStatus(
		context.Background(),
		store,
		journal,
		"test",
	)
	if !status.OK {
		t.Fatalf("merged status = %+v", status)
	}
	data, ok := status.Data.(CalibrationStatusData)
	if !ok ||
		data.State != "deleted" ||
		data.SendDispatch != browserflow.DispatchPerformed ||
		data.DeleteDispatch != browserflow.DispatchPerformed ||
		data.Postcondition != deletePostconditionProof ||
		!data.TargetClosed ||
		data.ConversationPresent ||
		data.RecoveryRequired {
		t.Fatalf("merged data = %+v", status.Data)
	}
}

func TestCleanupCalibrationWithoutRecordNeedsNoBrowser(t *testing.T) {
	store, err := NewCalibrationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result := CleanupCalibration(
		context.Background(),
		CalibrationCleanupConfig{
			Store:       store,
			Journal:     calibrationTestJournal{},
			BuildCommit: "test",
		},
	)
	if !result.OK ||
		result.State != webagent.StateReady ||
		result.Evidence.BrowserMode != "none" {
		t.Fatalf("cleanup without record = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("cleanup result validation: %v", err)
	}
}

func TestUnsupportedResearchResultIsTypedAndBrowserFree(t *testing.T) {
	result := UnsupportedOperation(
		"test",
		webagent.OperationResearch,
		"chatgpt_deep_research_control_unproven",
		"Deep Research control is not proven",
	)
	if result.OK ||
		result.State != webagent.StateUnsupported ||
		result.Stage != webagent.StagePlanned ||
		result.Evidence.BrowserMode != "none" ||
		result.Error == nil ||
		!result.Error.RetrySafe {
		t.Fatalf("unsupported research = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("unsupported research validation: %v", err)
	}
}
