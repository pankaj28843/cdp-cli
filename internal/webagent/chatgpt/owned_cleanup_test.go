package chatgpt

import (
	"context"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestRunOwnedClosesOnlyRecoverableOrUntouchedJobs(t *testing.T) {
	for _, tc := range []struct {
		name, dispatch                                                   string
		prepare, acknowledge, terminal, image, mismatched, panicDispatch bool
		wantClosed                                                       bool
		timeout                                                          bool
	}{
		{name: "pretyping failure", wantClosed: true},
		{name: "prepared draft with zero Send actions", prepare: true},
		{name: "timeout after preparing draft", prepare: true, timeout: true},
		{name: "unknown dispatch", prepare: true, dispatch: "unknown"},
		{name: "exception while action pending", prepare: true, panicDispatch: true},
		{name: "acknowledged but incomplete", prepare: true, dispatch: "performed", acknowledge: true},
		{name: "terminal text with durable matching history", prepare: true, dispatch: "performed", acknowledge: true, terminal: true, wantClosed: true},
		{name: "terminal text missing durable acknowledgement", prepare: true, dispatch: "performed", terminal: true},
		{name: "terminal text mismatched history", prepare: true, dispatch: "performed", acknowledge: true, terminal: true, mismatched: true},
		{name: "terminal image still needs original recovery", prepare: true, dispatch: "performed", acknowledge: true, terminal: true, image: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testsupport.NewBrowser("user-page")
			engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
			if err != nil {
				t.Fatal(err)
			}
			var result webagent.Result
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				result = runOwned(ctx, BrowserConfig{Client: client, Engine: engine, Journal: journal},
					"owned-cleanup", webagent.OperationAsk, "send", "about:blank", "rendered_same_target", nil,
					func(lease *browserflow.Lease, target *webagent.TargetEvidence, pending webagent.CleanupEvidence) webagent.Result {
						if tc.prepare {
							if err := lease.BindInputFingerprint(context.Background(), fingerprintPrompt("synthetic owned draft")); err != nil {
								t.Fatal(err)
							}
						}
						if tc.dispatch != "" || tc.panicDispatch {
							if err := lease.MarkPrepared(context.Background()); err != nil {
								t.Fatal(err)
							}
							_, err := lease.Dispatch(context.Background(), browserflow.DispatchFunc(func(context.Context, *cdp.PageSession) (browserflow.DispatchOutcome, error) {
								if tc.panicDispatch {
									panic("synthetic dispatcher exception")
								}
								return browserflow.DispatchOutcome{Dispatch: browserflow.Dispatch(tc.dispatch), RawInputAttempted: true}, nil
							}))
							if err != nil {
								t.Fatal(err)
							}
						}
						if tc.acknowledge {
							if err := lease.Acknowledge(context.Background(), "known-conversation"); err != nil {
								t.Fatal(err)
							}
						}
						if tc.terminal {
							if err := lease.MarkTerminal(context.Background()); err != nil {
								t.Fatal(err)
							}
						}
						if tc.timeout {
							cancel()
							if ctx.Err() == nil {
								t.Fatal("synthetic deadline did not cancel")
							}
						}
						current := operationFailure("owned-cleanup", "test", webagent.OperationAsk, webagent.StageObserveTerminal, "rendered_same_target", target, pending,
							"chatgpt_await_timeout", "timeout", "synthetic bounded timeout", AskData{Text: "Completed synthetic answer"}, nil)
						current.Action = actionEvidence(lease.Record())
						current.Error.RetrySafe = current.Action.RetrySafe
						if tc.terminal {
							current.OK, current.State, current.Error = true, webagent.StateTerminal, nil
							current.Conversation = conversationRef("known-conversation")
							if tc.mismatched {
								current.Conversation = conversationRef("different-conversation")
							}
							if tc.image {
								current.Data = AskData{Tool: ToolCreateImage, OutputKind: "image"}
							}
						}
						return current
					})
			}()
			if panicked != tc.panicDispatch {
				t.Fatalf("unexpected panic: %v", panicked)
			}
			if got := client.Counts["Target.closeTarget"] > 0; got != tc.wantClosed {
				t.Fatalf("closed=%v want=%v", got, tc.wantClosed)
			}
			if _, exists := client.Targets["user-page"]; !exists {
				t.Fatal("user target closed")
			}
			record, err := journal.Load(context.Background(), "owned-cleanup")
			if err != nil {
				t.Fatal(err)
			}
			if !tc.wantClosed {
				if _, exists := client.Targets[record.TargetID]; !exists {
					t.Fatal("unreconciled target lost")
				}
				if !tc.panicDispatch && (result.Cleanup.State != webagent.CleanupPending || result.Evidence.Target.Closed) {
					t.Fatalf("preservation evidence missing: %+v", result)
				}
				// Preserving a tab must still release the global input owner.
				next, err := engine.Acquire(context.Background(), browserflow.AcquireRequest{RunID: "next-owned-job", Provider: "chatgpt", Operation: "capabilities", InitialURL: "about:blank"})
				if err != nil {
					t.Fatalf("input lease remained locked: %v", err)
				}
				_, _ = next.Close(context.Background())
			}
		})
	}
}
