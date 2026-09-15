package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

type diagnosticCloseFailure struct {
	*testsupport.Browser
	failAttach bool
}

func (b *diagnosticCloseFailure) Call(ctx context.Context, method string, params any, result any) error {
	if method == "Target.attachToTarget" && b.failAttach {
		return errors.New("synthetic attach failure")
	}
	if method == "Target.closeTarget" {
		return errors.New("synthetic close failure")
	}
	return b.Browser.Call(ctx, method, params, result)
}

func TestCleanupRetainsOriginalOperationFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			client := &diagnosticCloseFailure{Browser: testsupport.NewBrowser("user-page")}
			engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
			if err != nil {
				t.Fatal(err)
			}
			result := runOwnedAction(context.Background(), BrowserConfig{Client: client, Engine: engine, Journal: journal, BuildCommit: "test"},
				"diagnostic-run", webagent.OperationAsk, "send", "about:blank", "rendered_same_target", nil,
				func(_ *browserflow.Lease, _ *webagent.TargetEvidence, _ webagent.CleanupEvidence) webagent.Result {
					r := webagent.Result{Stage: webagent.StageAttached, Action: &webagent.ActionEvidence{Dispatch: webagent.DispatchNotPerformed, RetrySafe: true}}
					if failed {
						r.Error = &webagent.OperationError{Code: "gemini_composer_not_ready", ErrClass: "provider", Message: "private-message-marker", Reason: "private-reason-marker"}
					}
					return r
				})
			if result.Error == nil || result.Error.Code != "gemini_exact_target_cleanup_failed" || result.OK || result.Stage != webagent.StageCleanupPending {
				t.Fatalf("cleanup did not remain primary: %+v", result)
			}
			original := result.Evidence.OperationFailure
			if !failed && original != nil {
				t.Fatalf("invented failure: %+v", original)
			}
			if failed && (original == nil || original.Code != "gemini_composer_not_ready" || original.ErrClass != "provider" || original.Stage != webagent.StageAttached) {
				t.Fatalf("lost original failure: %+v", original)
			}
			if result.Action.Dispatch != webagent.DispatchNotPerformed || !result.Action.RetrySafe {
				t.Fatalf("action changed: %+v", result.Action)
			}
			raw, err := json.Marshal(result.Evidence)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "private-") {
				t.Fatalf("private error content retained: %s", raw)
			}
		})
	}
}

func TestAcquireFailureSurvivesCleanupFailure(t *testing.T) {
	client := &diagnosticCloseFailure{Browser: testsupport.NewBrowser("user-page"), failAttach: true}
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	result := runOwnedAction(context.Background(), BrowserConfig{Client: client, Engine: engine, Journal: journal, BuildCommit: "test"},
		"acquire-diagnostic-run", webagent.OperationAsk, "send", "about:blank", "rendered_same_target", nil,
		func(_ *browserflow.Lease, _ *webagent.TargetEvidence, _ webagent.CleanupEvidence) webagent.Result {
			t.Fatal("callback ran after failed acquisition")
			return webagent.Result{}
		})
	if result.Error == nil || result.Error.Code != "gemini_exact_target_cleanup_failed" {
		t.Fatalf("primary error: %+v", result.Error)
	}
	original := result.Evidence.OperationFailure
	if original == nil || original.Code != "gemini_browser_start_failed" || original.ErrClass != "connection" || original.Stage != webagent.StagePlanned {
		t.Fatalf("missing acquire failure: %+v", original)
	}
}
