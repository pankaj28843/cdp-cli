package claude

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
)

func TestCalibrationStatusTreatsProvenDeletionAsNoRemainingConversation(t *testing.T) {
	store, journal := newCalibrationStateTestStores(t)
	record := completedCalibrationState()
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}

	result := CalibrationStatus(context.Background(), store, journal, "test-commit")

	if !result.OK {
		t.Fatalf("CalibrationStatus = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("CalibrationStatus validation: %v", err)
	}
	data, ok := result.Data.(CalibrationStatusData)
	if !ok {
		t.Fatalf("status data type = %T", result.Data)
	}
	if data.State != "deleted" ||
		data.ConversationPresent ||
		!data.TargetClosed ||
		data.RecoveryRequired {
		t.Fatalf("status data = %+v", data)
	}
}

func TestCleanupCalibrationIsBrowserFreeWhenNothingRemains(t *testing.T) {
	store, journal := newCalibrationStateTestStores(t)
	record := completedCalibrationState()
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}

	result := CleanupCalibration(context.Background(), CalibrationCleanupConfig{
		Store:       store,
		Journal:     journal,
		BuildCommit: "test-commit",
	})

	if !result.OK {
		t.Fatalf("CleanupCalibration = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("CleanupCalibration validation: %v", err)
	}
	data, ok := result.Data.(CalibrationStatusData)
	if !ok || data.RecoveryRequired || data.ConversationPresent {
		t.Fatalf("cleanup data = %#v", result.Data)
	}
}

func newCalibrationStateTestStores(
	t *testing.T,
) (*CalibrationStore, *browserflow.FileJournal) {
	t.Helper()
	stateDir := t.TempDir()
	store, err := NewCalibrationStore(stateDir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}
	journal, err := browserflow.NewFileJournal(stateDir)
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	return store, journal
}

func completedCalibrationState() CalibrationStateRecord {
	return CalibrationStateRecord{
		SchemaVersion:     CalibrationStateSchemaVersion,
		RunID:             "wa-calibration-test",
		State:             "deleted",
		PromptFingerprint: strings.Repeat("a", 64),
		TargetID:          "target-test",
		ConversationID:    "conversation-test",
		SendDispatch:      browserflow.DispatchPerformed,
		DeleteDispatch:    browserflow.DispatchPerformed,
		Postcondition:     deletePostconditionProof,
		TargetClosed:      true,
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}
}
