package chatgpt

import (
	"context"
	"errors"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

type closeFailBrowser struct {
	*testsupport.Browser
}

func (b *closeFailBrowser) Call(
	ctx context.Context,
	method string,
	params any,
	result any,
) error {
	if method == "Target.closeTarget" {
		return errors.New("synthetic exact close failure")
	}
	return b.Browser.Call(ctx, method, params, result)
}

func TestRunOwnedCleanupFailurePreservesPerformedActionSafety(t *testing.T) {
	t.Parallel()

	const retryAt = "2026-07-27T12:00:30Z"
	stateDir := t.TempDir()
	client := &closeFailBrowser{Browser: testsupport.NewBrowser("user-page")}
	engine, journal, err := testsupport.NewRuntime(stateDir, client)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	const runID = "run-cleanup-after-dispatch"
	result := runOwned(
		context.Background(),
		BrowserConfig{
			Client:      client,
			Engine:      engine,
			Journal:     journal,
			BuildCommit: "test-commit",
		},
		runID,
		webagent.OperationAsk,
		"send",
		Origin+"/",
		"rendered_same_target",
		map[string]any{"schema_version": "test/v1"},
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			cleanup webagent.CleanupEvidence,
		) webagent.Result {
			if err := lease.MarkPrepared(context.Background()); err != nil {
				t.Fatalf("MarkPrepared: %v", err)
			}
			if _, err := lease.Dispatch(
				context.Background(),
				browserflow.DispatchFunc(
					func(
						context.Context,
						*cdp.PageSession,
					) (browserflow.DispatchOutcome, error) {
						return browserflow.DispatchOutcome{
							Dispatch:          browserflow.DispatchPerformed,
							RawInputAttempted: true,
						}, nil
					},
				),
			); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if err := lease.MarkIncomplete(context.Background()); err != nil {
				t.Fatalf("MarkIncomplete: %v", err)
			}
			current := operationFailure(
				runID,
				"test-commit",
				webagent.OperationAsk,
				webagent.StageObserveTerminal,
				"rendered_same_target",
				target,
				cleanup,
				"chatgpt_rate_limited",
				"rate_limit",
				"ChatGPT was rate limited",
				map[string]any{"schema_version": "test/v1"},
				nil,
			)
			current.Error.RetryAt = retryAt
			current.Action = actionEvidence(lease.Record())
			return current
		},
	)

	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_exact_target_cleanup_failed" ||
		result.Error.RetryAt != retryAt ||
		result.Error.RetrySafe ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchPerformed ||
		result.Action.RetrySafe ||
		result.Cleanup.FailurePhase == "" {
		t.Fatalf("result=%+v error=%+v", result, result.Error)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("cleanup failure result is invalid: %v; result=%+v", err, result)
	}
}

func TestRunOwnedPreservesTargetCloseProofWhenJournalFinalizationFails(
	t *testing.T,
) {
	client := testsupport.NewBrowser("user-page")
	fileJournal, err := browserflow.NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	failed := false
	journal := &recordingJournal{
		Journal: fileJournal,
		beforeSave: func(
			_ context.Context,
			record browserflow.Record,
		) error {
			if record.Phase == browserflow.PhaseClosed && !failed {
				failed = true
				return errors.New("synthetic final journal failure")
			}
			return nil
		},
	}
	engine := newReadTestEngine(t, client, journal)
	const runID = "run-close-proof-with-journal-failure"
	result := runOwned(
		context.Background(),
		BrowserConfig{Client: client, Engine: engine, Journal: journal},
		runID,
		webagent.OperationConversationsDetail,
		"",
		"about:blank",
		"browser_context_stable_http",
		map[string]any{"schema_version": "test/v1"},
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			cleanup webagent.CleanupEvidence,
		) webagent.Result {
			if err := lease.MarkPrepared(context.Background()); err != nil {
				t.Fatalf("MarkPrepared: %v", err)
			}
			return operationSuccess(
				runID,
				"test-commit",
				webagent.OperationConversationsDetail,
				webagent.StageObserveTerminal,
				"browser_context_stable_http",
				target,
				cleanup,
				map[string]any{"schema_version": "test/v1"},
				nil,
			)
		},
	)

	if !failed ||
		!result.OK ||
		result.Error != nil ||
		result.Evidence.Target == nil ||
		!result.Evidence.Target.Closed ||
		result.Cleanup.State != webagent.CleanupClosed ||
		!result.Cleanup.TargetClosed ||
		result.Cleanup.CloseProof != "exact_target_absent_after_close" {
		t.Fatalf("result=%+v", result)
	}
}
