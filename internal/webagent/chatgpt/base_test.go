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
			current := operationSuccess(
				runID,
				"test-commit",
				webagent.OperationAsk,
				webagent.StageObserveTerminal,
				"rendered_same_target",
				target,
				cleanup,
				map[string]any{"schema_version": "test/v1"},
				nil,
			)
			current.State = webagent.StateIncomplete
			current.Action = actionEvidence(lease.Record())
			return current
		},
	)

	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_exact_target_cleanup_failed" ||
		result.Error.RetrySafe ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchPerformed ||
		result.Action.RetrySafe ||
		result.Cleanup.CloseAttemptCount != 2 ||
		result.Cleanup.FailurePhase == "" {
		t.Fatalf("result=%+v error=%+v", result, result.Error)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("cleanup failure result is invalid: %v; result=%+v", err, result)
	}
}
