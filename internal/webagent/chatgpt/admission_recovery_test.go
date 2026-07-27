package chatgpt

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestReconcilePriorSubmittedMutationAllowsOnlyDifferentFingerprint(t *testing.T) {
	for _, test := range []struct {
		name               string
		currentFingerprint string
		wantReconciled     bool
	}{
		{
			name:               "different request self heals",
			currentFingerprint: strings.Repeat("b", 64),
			wantReconciled:     true,
		},
		{
			name:               "same request remains fail closed",
			currentFingerprint: strings.Repeat("a", 64),
			wantReconciled:     false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			gate, err := admission.New(admission.Config{
				StateDir: stateDir,
			})
			if err != nil {
				t.Fatalf("admission.New: %v", err)
			}
			const runID = "prior-chatgpt-run"
			lease, err := gate.Acquire(context.Background(), admission.Request{
				Provider:  "chatgpt",
				Operation: "ask",
				RunID:     runID,
			})
			if err != nil {
				t.Fatalf("Acquire prior admission: %v", err)
			}
			if err := lease.Release(admission.Release{
				Outcome: admission.OutcomeUnknown,
			}); err != nil {
				t.Fatalf("release prior admission: %v", err)
			}

			journal, err := browserflow.NewFileJournal(stateDir)
			if err != nil {
				t.Fatalf("NewFileJournal: %v", err)
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			if err := journal.Create(
				context.Background(),
				browserflow.Record{
					SchemaVersion:      browserflow.RecoverySchemaVersion,
					RunID:              runID,
					Provider:           "chatgpt",
					Operation:          "ask",
					Phase:              browserflow.PhaseClosed,
					ActionName:         "send",
					Dispatch:           browserflow.DispatchPerformed,
					ActionAttemptCount: 1,
					RawInputCount:      1,
					PendingPersisted:   true,
					TargetID:           "prior-target",
					SessionID:          "prior-session",
					ConversationID:     "prior-conversation",
					InputFingerprint:   strings.Repeat("a", 64),
					Cleanup:            browserflow.CleanupClosed,
					CreatedAt:          now,
					UpdatedAt:          now,
				},
			); err != nil {
				t.Fatalf("create recovery evidence: %v", err)
			}

			reconciled, err := reconcilePriorSubmittedMutation(
				context.Background(),
				BrowserConfig{
					Admission: gate,
					Journal:   journal,
				},
				test.currentFingerprint,
			)
			if err != nil {
				t.Fatalf("reconcilePriorSubmittedMutation: %v", err)
			}
			if test.wantReconciled && reconciled != runID {
				t.Fatalf("reconciled run = %q, want %q", reconciled, runID)
			}
			if !test.wantReconciled && reconciled != "" {
				t.Fatalf("reconciled run = %q, want none", reconciled)
			}
			record, found, err := gate.Status(context.Background(), "chatgpt")
			if err != nil || !found {
				t.Fatalf("admission status found=%v err=%v", found, err)
			}
			wantOutcome := admission.OutcomeUnknown
			if test.wantReconciled {
				wantOutcome = admission.OutcomeAcknowledged
			}
			if record.Outcome != wantOutcome {
				t.Fatalf("admission outcome = %q, want %q", record.Outcome, wantOutcome)
			}
		})
	}
}

func TestRecoveryProvesAcknowledgementRequiresJournalConversationTransition(
	t *testing.T,
) {
	base := browserflow.Record{
		Provider:           "chatgpt",
		Operation:          string(webagent.OperationAsk),
		ActionName:         "send",
		Phase:              browserflow.PhaseClosed,
		Cleanup:            browserflow.CleanupClosed,
		Dispatch:           browserflow.DispatchPerformed,
		ActionAttemptCount: 1,
		RawInputCount:      1,
		PendingPersisted:   true,
		ConversationID:     "conversation-1",
	}
	tests := []struct {
		name        string
		mutate      func(*browserflow.Record)
		acknowledge bool
	}{
		{
			name:        "ask Send",
			acknowledge: true,
		},
		{
			name: "continue Send",
			mutate: func(record *browserflow.Record) {
				record.Operation = string(
					webagent.OperationConversationsContinue,
				)
			},
			acknowledge: true,
		},
		{
			name: "legacy continue action",
			mutate: func(record *browserflow.Record) {
				record.Operation = string(
					webagent.OperationConversationsContinue,
				)
				record.ActionName = "continue"
			},
		},
		{
			name: "calibrate send",
			mutate: func(record *browserflow.Record) {
				record.Operation = string(webagent.OperationCalibrate)
			},
		},
		{
			name: "missing conversation transition",
			mutate: func(record *browserflow.Record) {
				record.ConversationID = ""
			},
		},
		{
			name: "missing action name",
			mutate: func(record *browserflow.Record) {
				record.ActionName = ""
			},
		},
		{
			name: "delete action",
			mutate: func(record *browserflow.Record) {
				record.ActionName = "delete"
			},
		},
		{
			name: "delete operation",
			mutate: func(record *browserflow.Record) {
				record.Operation = string(
					webagent.OperationConversationsDelete,
				)
			},
		},
		{
			name: "read operation",
			mutate: func(record *browserflow.Record) {
				record.Operation = string(
					webagent.OperationConversationsDetail,
				)
			},
		},
		{
			name: "artifact operation",
			mutate: func(record *browserflow.Record) {
				record.Operation = string(webagent.OperationArtifactDownload)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recovery := base
			if test.mutate != nil {
				test.mutate(&recovery)
			}
			if got := recoveryProvesAcknowledgement(recovery); got != test.acknowledge {
				t.Fatalf(
					"recoveryProvesAcknowledgement() = %v, want %v; record=%+v",
					got,
					test.acknowledge,
					recovery,
				)
			}
		})
	}
}
