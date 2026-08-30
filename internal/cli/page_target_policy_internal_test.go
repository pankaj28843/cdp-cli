package cli

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type keepOpenPolicyFakeClient struct {
	mu                  sync.Mutex
	targets             map[string]cdp.TargetInfo
	events              []string
	persistentErr       error
	closeErr            error
	closeDelay          int
	pendingClose        map[string]int
	failListsAfterClose bool
}

func (f *keepOpenPolicyFakeClient) Call(_ context.Context, method string, params, result any) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch method {
	case "Target.getTargets":
		if f.failListsAfterClose {
			return errors.New("synthetic target list failure after close")
		}
		for targetID, remaining := range f.pendingClose {
			if remaining <= 1 {
				delete(f.targets, targetID)
				delete(f.pendingClose, targetID)
				continue
			}
			f.pendingClose[targetID] = remaining - 1
		}
		rows := make([]cdp.TargetInfo, 0, len(f.targets))
		for _, target := range f.targets {
			rows = append(rows, target)
		}
		return keepOpenPolicyJSONRoundTrip(map[string]any{"targetInfos": rows}, result)
	case "Target.createTarget":
		targetID := "created-policy-page"
		var input struct {
			URL string `json:"url"`
		}
		if err := keepOpenPolicyJSONRoundTrip(params, &input); err != nil {
			return err
		}
		f.targets[targetID] = cdp.TargetInfo{TargetID: targetID, Type: "page", URL: input.URL}
		return keepOpenPolicyJSONRoundTrip(map[string]any{"targetId": targetID}, result)
	case "Browser.getWindowForTarget":
		return keepOpenPolicyJSONRoundTrip(map[string]any{"windowId": 1}, result)
	case "Target.closeTarget":
		var input struct {
			TargetID string `json:"targetId"`
		}
		if err := keepOpenPolicyJSONRoundTrip(params, &input); err != nil {
			return err
		}
		f.events = append(f.events, "close:"+input.TargetID)
		if f.closeErr != nil {
			f.failListsAfterClose = true
			return f.closeErr
		}
		if f.closeDelay > 0 {
			f.pendingClose[input.TargetID] = f.closeDelay
		} else {
			delete(f.targets, input.TargetID)
		}
		return keepOpenPolicyJSONRoundTrip(map[string]any{"success": true}, result)
	default:
		return nil
	}
}

func (f *keepOpenPolicyFakeClient) CallSession(context.Context, string, string, any, any) error {
	return errors.New("unexpected session call")
}

func (f *keepOpenPolicyFakeClient) MarkTargetDisposable(_ context.Context, targetID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "mark-disposable:"+targetID)
	return nil
}

func (f *keepOpenPolicyFakeClient) MarkTargetPersistent(_ context.Context, targetID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "mark-persistent:"+targetID)
	return f.persistentErr
}

func keepOpenPolicyJSONRoundTrip(input, output any) error {
	if output == nil {
		return nil
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, output)
}

func newKeepOpenPolicyTestApp(t *testing.T) *app {
	t.Helper()
	a := &app{opts: options{stateDir: t.TempDir(), allowOverBudget: true}}
	a.newRoot()
	return a
}

func TestCreateWorkflowPageTargetKeepOpenPromotionFailureSettlesCreatedTarget(t *testing.T) {
	fake := &keepOpenPolicyFakeClient{
		targets:       map[string]cdp.TargetInfo{"baseline": {TargetID: "baseline", Type: "page", URL: "https://example.test/baseline"}},
		pendingClose:  map[string]int{},
		persistentErr: errors.New("synthetic persistent policy failure"),
		closeDelay:    2,
	}
	a := newKeepOpenPolicyTestApp(t)

	targetID, err := a.createWorkflowPageTargetWithKeepOpen(context.Background(), fake, "https://example.test/created", "policy-test", true)
	if err == nil {
		t.Fatal("createWorkflowPageTargetWithKeepOpen succeeded, want persistent-policy failure")
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.Code != "lease_target_policy_failed" || commandErr.ExitCode != ExitConnection {
		t.Fatalf("command error = %+v, want stable lease policy failure", commandErr)
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("command error data = %T, want map", commandErr.Data)
	}
	closeReport, ok := data["close"].(pageCloseReport)
	if !ok {
		t.Fatalf("close evidence = %T, want pageCloseReport", data["close"])
	}
	if !closeReport.Closed || !closeReport.TargetGone || closeReport.AttemptCount < 1 || data["recovery_command"] == nil || data["policy_error"] == nil {
		t.Fatalf("close evidence = %+v data=%+v, want delayed exact-target cleanup and primary policy metadata", closeReport, data)
	}
	if targetID != "created-policy-page" {
		t.Fatalf("target id = %q, want created-policy-page", targetID)
	}
	if len(fake.targets) != 1 {
		t.Fatalf("remaining targets = %+v, want only unrelated baseline", fake.targets)
	}
	if len(fake.events) < 3 || fake.events[0] != "mark-disposable:created-policy-page" || fake.events[1] != "mark-persistent:created-policy-page" || fake.events[2] != "close:created-policy-page" {
		t.Fatalf("lifecycle events = %v, want policy failure followed by exact target close", fake.events)
	}
}

func TestCreateWorkflowPageTargetKeepOpenPromotionCleanupFailurePreservesPolicyError(t *testing.T) {
	fake := &keepOpenPolicyFakeClient{
		targets:       map[string]cdp.TargetInfo{"baseline": {TargetID: "baseline", Type: "page", URL: "https://example.test/baseline"}},
		pendingClose:  map[string]int{},
		persistentErr: errors.New("synthetic persistent policy failure"),
		closeErr:      errors.New("synthetic close failure"),
	}
	a := newKeepOpenPolicyTestApp(t)

	_, err := a.createWorkflowPageTargetWithKeepOpen(context.Background(), fake, "https://example.test/created", "policy-test", true)
	if err == nil {
		t.Fatal("createWorkflowPageTargetWithKeepOpen succeeded, want policy failure")
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("command error data = %T, want map", commandErr.Data)
	}
	closeReport, ok := data["close"].(pageCloseReport)
	if !ok {
		t.Fatalf("close evidence = %T, want pageCloseReport", data["close"])
	}
	if closeReport.TargetGone || closeReport.LastError == "" || data["recovery_command"] == nil || data["policy_error"] == nil {
		t.Fatalf("close evidence = %+v data=%+v, want cleanup failure/recovery plus policy error", closeReport, data)
	}
	if commandErr.Code != "lease_target_policy_failed" || commandErr.ExitCode != ExitConnection {
		t.Fatalf("command error = %+v, want stable primary policy error", commandErr)
	}
	if len(fake.targets) != 2 {
		t.Fatalf("remaining targets = %+v, want baseline plus recoverable created target", fake.targets)
	}
}

func TestCreateWorkflowPageTargetKeepOpenSuccessRetainsOnlyCreatedTarget(t *testing.T) {
	fake := &keepOpenPolicyFakeClient{
		targets:      map[string]cdp.TargetInfo{"baseline": {TargetID: "baseline", Type: "page", URL: "https://example.test/baseline"}},
		pendingClose: map[string]int{},
	}
	a := newKeepOpenPolicyTestApp(t)

	targetID, err := a.createWorkflowPageTargetWithKeepOpen(context.Background(), fake, "https://example.test/created", "policy-test", true)
	if err != nil {
		t.Fatalf("createWorkflowPageTargetWithKeepOpen error = %v", err)
	}
	if targetID != "created-policy-page" || len(fake.targets) != 2 {
		t.Fatalf("target id=%q targets=%+v, want created target retained beside baseline", targetID, fake.targets)
	}
	for _, event := range fake.events {
		if event == "close:created-policy-page" {
			t.Fatalf("successful keep-open promotion closed target: %v", fake.events)
		}
	}
}
