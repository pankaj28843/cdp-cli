package browserflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileJournalCreateSaveLoadAndPermissions(t *testing.T) {
	stateDir := t.TempDir()
	journal, err := NewFileJournal(stateDir)
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	now := time.Date(2026, 7, 25, 17, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	record := Record{
		SchemaVersion: RecoverySchemaVersion,
		RunID:         "run-journal",
		Provider:      "claude",
		Operation:     "ask",
		Phase:         PhasePlanned,
		Cleanup:       CleanupNotRequired,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := journal.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := journal.Create(context.Background(), record); !errors.Is(err, ErrRunExists) {
		t.Fatalf("duplicate Create error = %v, want ErrRunExists", err)
	}
	record.Phase = PhaseBudgetChecked
	record.Budget = &BudgetEvidence{TabCount: 1, MaxTabs: 15, WindowCount: 1, MaxWindows: 5, WindowCountKnown: true}
	if err := journal.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := journal.Load(context.Background(), record.RunID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Phase != PhaseBudgetChecked || loaded.Budget == nil || loaded.Budget.TabCount != 1 {
		t.Fatalf("loaded record = %+v", loaded)
	}
	path, err := journal.Path(record.RunID)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	for _, path := range []string{path, path + ".lock"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, got)
		}
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("stat recovery directory: %v", err)
	} else if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("recovery directory mode = %o, want 700", got)
	}
}

func TestFileJournalRejectsInvalidTransitionAndPathTraversal(t *testing.T) {
	journal, err := NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	now := time.Date(2026, 7, 25, 17, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	record := Record{
		SchemaVersion: RecoverySchemaVersion,
		RunID:         "run-transition",
		Provider:      "claude",
		Operation:     "ask",
		Phase:         PhasePlanned,
		Cleanup:       CleanupNotRequired,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := journal.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	record.Phase = PhaseTerminal
	record.TargetID = "target"
	record.SessionID = "session"
	if err := journal.Save(context.Background(), record); err == nil || !strings.Contains(err.Error(), "invalid phase transition") {
		t.Fatalf("invalid transition error = %v", err)
	}
	if _, err := journal.Path("../../escape"); err == nil {
		t.Fatal("Path accepted traversal run id")
	}
}

func TestRecordRejectsPrivateControlCharactersAndImpossibleDispatch(t *testing.T) {
	now := time.Date(2026, 7, 25, 17, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	record := Record{
		SchemaVersion:    RecoverySchemaVersion,
		RunID:            "run-validation",
		Provider:         "claude",
		Operation:        "ask",
		Phase:            PhaseActionPerformed,
		Dispatch:         DispatchPerformed,
		RawInputCount:    1,
		TargetID:         "target",
		SessionID:        "session",
		Cleanup:          CleanupPending,
		CreatedAt:        now,
		UpdatedAt:        now,
		PendingPersisted: true,
	}
	record.ConversationID = "conversation\nprivate"
	if err := record.Validate(); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("control-character validation error = %v", err)
	}
	record.ConversationID = ""
	record.Dispatch = DispatchNotPerformed
	if err := record.Validate(); err == nil || !strings.Contains(err.Error(), "raw_input_count=0") {
		t.Fatalf("impossible dispatch validation error = %v", err)
	}
}
