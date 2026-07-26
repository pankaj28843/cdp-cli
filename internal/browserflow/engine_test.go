package browserflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestNewRejectsUnboundedDispatchAttempts(t *testing.T) {
	_, err := New(Config{
		Client:              newFakeBrowserClient(),
		Journal:             &phaseFailJournal{},
		MaxDispatchAttempts: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "must not exceed 2") {
		t.Fatalf("New error = %v", err)
	}
}

func TestInputFingerprintIsDurableBeforePrepared(t *testing.T) {
	client := newFakeBrowserClient()
	engine, journal := newTestEngine(t, client, Config{})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-input-fingerprint",
		Provider:   "perplexity",
		Operation:  "ask",
		InitialURL: "https://example.test",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.BindInputFingerprint(
		context.Background(),
		"not-a-fingerprint",
	); err == nil {
		t.Fatal("invalid fingerprint was accepted")
	}
	fingerprint := strings.Repeat("a", 64)
	if err := lease.BindInputFingerprint(
		context.Background(),
		fingerprint,
	); err != nil {
		t.Fatalf("BindInputFingerprint: %v", err)
	}
	stored, err := journal.Load(context.Background(), "run-input-fingerprint")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.InputFingerprint != fingerprint ||
		stored.Phase != PhaseAttached {
		t.Fatalf("stored record=%+v", stored)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	if err := lease.BindInputFingerprint(
		context.Background(),
		strings.Repeat("b", 64),
	); err == nil {
		t.Fatal("fingerprint changed after prepared")
	}
	if _, err := lease.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineAcquireDispatchCloseExactTarget(t *testing.T) {
	client := newFakeBrowserClient("user-page")
	engine, journal := newTestEngine(t, client, Config{})
	ctx := context.Background()

	lease, err := engine.Acquire(ctx, AcquireRequest{
		RunID:      "run-success",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	dispatchCalls := 0
	outcome, err := lease.Dispatch(ctx, DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		dispatchCalls++
		return DispatchOutcome{Dispatch: DispatchPerformed, RawInputAttempted: true}, nil
	}))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if outcome.Dispatch != DispatchPerformed || dispatchCalls != 1 {
		t.Fatalf("dispatch outcome = %+v, calls=%d", outcome, dispatchCalls)
	}
	if err := lease.Acknowledge(ctx, "conversation-1"); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if err := lease.MarkTerminal(ctx); err != nil {
		t.Fatalf("MarkTerminal: %v", err)
	}
	cleanup, err := lease.Close(ctx)
	if err != nil {
		t.Fatalf("Close: %v; cleanup=%+v", err, cleanup)
	}
	if cleanup.State != CleanupClosed || !cleanup.CloseSent || !cleanup.TargetGone {
		t.Fatalf("cleanup = %+v", cleanup)
	}

	record, err := journal.Load(ctx, "run-success")
	if err != nil {
		t.Fatalf("load recovery record: %v", err)
	}
	if record.Phase != PhaseClosed ||
		record.Cleanup != CleanupClosed ||
		record.Dispatch != DispatchPerformed ||
		record.ActionAttemptCount != 1 ||
		record.RawInputCount != 1 ||
		!record.PendingPersisted ||
		record.ConversationID != "conversation-1" {
		t.Fatalf("final record = %+v", record)
	}
	if client.hasTarget("owned-1") {
		t.Fatal("workflow-owned target remains after exact close")
	}
	if !client.hasTarget("user-page") {
		t.Fatal("user target was closed by workflow cleanup")
	}
	wantTrace := []string{
		"Target.getTargets",
		"Browser.getWindowForTarget:user-page",
		"Target.createTarget",
		"Target.attachToTarget:owned-1",
		"Target.detachFromTarget:session-owned-1",
		"Target.closeTarget:owned-1",
		"Target.getTargets",
	}
	if got := client.traceSnapshot(); !equalStrings(got, wantTrace) {
		t.Fatalf("CDP trace = %#v, want %#v", got, wantTrace)
	}
}

func TestEngineSerializesHeadedInputUntilExplicitRelease(t *testing.T) {
	lockPath := HeadedInputLockPath(t.TempDir())
	firstClient := newFakeBrowserClient()
	secondClient := newFakeBrowserClient()
	firstEngine, _ := newTestEngine(t, firstClient, Config{InputLockPath: lockPath})
	secondEngine, _ := newTestEngine(t, secondClient, Config{InputLockPath: lockPath})

	first, err := firstEngine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-input-first",
		Provider:   "gemini",
		Operation:  "ask",
		InitialURL: "about:blank",
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	secondResult := make(chan struct {
		lease *Lease
		err   error
	}, 1)
	go func() {
		lease, acquireErr := secondEngine.Acquire(
			context.Background(),
			AcquireRequest{
				RunID:      "run-input-second",
				Provider:   "chatgpt",
				Operation:  "ask",
				InitialURL: "about:blank",
			},
		)
		secondResult <- struct {
			lease *Lease
			err   error
		}{lease: lease, err: acquireErr}
	}()

	select {
	case result := <-secondResult:
		t.Fatalf("second Acquire bypassed input lease: %v", result.err)
	case <-time.After(30 * time.Millisecond):
	}
	if calls := secondClient.callCount("Target.createTarget"); calls != 0 {
		t.Fatalf("second target create calls = %d before input release, want zero", calls)
	}
	if err := first.ReleaseInput(); err != nil {
		t.Fatalf("ReleaseInput: %v", err)
	}

	var second *Lease
	select {
	case result := <-secondResult:
		if result.err != nil {
			t.Fatalf("second Acquire after release: %v", result.err)
		}
		second = result.lease
	case <-time.After(time.Second):
		t.Fatal("second Acquire remained blocked after explicit input release")
	}
	if calls := secondClient.callCount("Target.createTarget"); calls != 1 {
		t.Fatalf("second target create calls = %d after input release, want one", calls)
	}
	if _, err := first.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if _, err := second.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestEngineBudgetFailureNeverCreatesTarget(t *testing.T) {
	client := newFakeBrowserClient("user-page")
	engine, journal := newTestEngine(t, client, Config{
		Budget: cdp.BrowserResourceBudgetOptions{MaxTabs: 1, MaxWindows: 5, BrowserMode: "headed"},
	})

	_, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-budget",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("Acquire error = %v, want BudgetExceededError", err)
	}
	if client.callCount("Target.createTarget") != 0 {
		t.Fatalf("Target.createTarget calls = %d, want zero", client.callCount("Target.createTarget"))
	}
	record, loadErr := journal.Load(context.Background(), "run-budget")
	if loadErr != nil {
		t.Fatalf("load budget record: %v", loadErr)
	}
	if record.Phase != PhaseFailed ||
		record.LastErrorClass != "browser_resource_budget_exceeded" ||
		record.Budget == nil ||
		!record.Budget.OverBudget ||
		record.TargetID != "" ||
		record.Cleanup != CleanupNotRequired {
		t.Fatalf("budget record = %+v", record)
	}
}

func TestEngineCreateFailureIsNotRetried(t *testing.T) {
	client := newFakeBrowserClient()
	client.fail["Target.createTarget"] = errors.New("synthetic create failure")
	engine, journal := newTestEngine(t, client, Config{})

	_, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-create-failure",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err == nil || !strings.Contains(err.Error(), "synthetic create failure") {
		t.Fatalf("Acquire error = %v", err)
	}
	if got := client.callCount("Target.createTarget"); got != 1 {
		t.Fatalf("Target.createTarget calls = %d, want exactly one", got)
	}
	record, loadErr := journal.Load(context.Background(), "run-create-failure")
	if loadErr != nil {
		t.Fatalf("load create-failure record: %v", loadErr)
	}
	if record.Phase != PhaseFailed || record.LastErrorClass != "target_create_failed" || record.TargetID != "" {
		t.Fatalf("create-failure record = %+v", record)
	}
}

func TestEngineAttachFailurePersistsCleanupAndClosesOnlyOwnedTarget(t *testing.T) {
	client := newFakeBrowserClient("user-page")
	client.fail["Target.attachToTarget"] = errors.New("synthetic attach failure")
	engine, journal := newTestEngine(t, client, Config{})

	_, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-attach-failure",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err == nil || !strings.Contains(err.Error(), "synthetic attach failure") {
		t.Fatalf("Acquire error = %v", err)
	}
	if client.hasTarget("owned-1") {
		t.Fatal("owned target remains after attach failure")
	}
	if !client.hasTarget("user-page") {
		t.Fatal("attach failure cleanup closed a sibling user target")
	}
	record, loadErr := journal.Load(context.Background(), "run-attach-failure")
	if loadErr != nil {
		t.Fatalf("load attach-failure record: %v", loadErr)
	}
	if record.Phase != PhaseClosed ||
		record.Cleanup != CleanupClosed ||
		record.LastErrorClass != "target_attach_failed" ||
		record.TargetID != "owned-1" ||
		record.SessionID != "" {
		t.Fatalf("attach-failure record = %+v", record)
	}
}

func TestTargetOwnershipPersistenceRetriesWithoutCreatingAnotherTarget(t *testing.T) {
	client := newFakeBrowserClient("user-page")
	base, err := NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	journal := &phaseFailJournal{Journal: base, phase: PhaseTargetOwned, remaining: 1}
	engine, err := New(Config{
		Client:  client,
		Journal: journal,
		Budget: cdp.BrowserResourceBudgetOptions{
			MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
		},
		CloseTimeout:      500 * time.Millisecond,
		ClosePollInterval: time.Millisecond,
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 17, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-ownership-retry",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if client.callCount("Target.createTarget") != 1 || lease.TargetID() != "owned-1" {
		t.Fatalf("create calls=%d target=%q", client.callCount("Target.createTarget"), lease.TargetID())
	}
	record, err := base.Load(context.Background(), "run-ownership-retry")
	if err != nil || record.Phase != PhaseAttached || record.TargetID != "owned-1" {
		t.Fatalf("ownership record=%+v err=%v", record, err)
	}
	if _, err := lease.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if client.hasTarget("owned-1") || !client.hasTarget("user-page") {
		t.Fatalf("targets after close: owned=%v user=%v", client.hasTarget("owned-1"), client.hasTarget("user-page"))
	}
}

func TestPreparationFailureClosesOnlyOwnedTarget(t *testing.T) {
	client := newFakeBrowserClient("user-page")
	engine, journal := newTestEngine(t, client, Config{})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-prepare-failure",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Provider preparation fails before MarkPrepared; the lease still owns the
	// exact target and must be safely closeable from PhaseAttached.
	cleanup, err := lease.Close(context.Background())
	if err != nil || !cleanup.TargetGone {
		t.Fatalf("Close cleanup=%+v err=%v", cleanup, err)
	}
	record, loadErr := journal.Load(context.Background(), "run-prepare-failure")
	if loadErr != nil || record.Phase != PhaseClosed || record.RawInputCount != 0 {
		t.Fatalf("prepare-failure record=%+v err=%v", record, loadErr)
	}
	if client.hasTarget("owned-1") || !client.hasTarget("user-page") {
		t.Fatalf("targets after prepare failure: owned=%v user=%v", client.hasTarget("owned-1"), client.hasTarget("user-page"))
	}
}

func TestActionPendingMustPersistBeforeDispatcherRuns(t *testing.T) {
	client := newFakeBrowserClient()
	base, err := NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	journal := &phaseFailJournal{Journal: base, phase: PhaseActionPending, remaining: 1}
	engine, err := New(Config{
		Client:  client,
		Journal: journal,
		Budget: cdp.BrowserResourceBudgetOptions{
			MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
		},
		CloseTimeout:      500 * time.Millisecond,
		ClosePollInterval: time.Millisecond,
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 17, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-pending-failure",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	dispatchCalls := 0
	_, err = lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		dispatchCalls++
		return DispatchOutcome{Dispatch: DispatchPerformed, RawInputAttempted: true}, nil
	}))
	if err == nil || !strings.Contains(err.Error(), "persist action_pending") {
		t.Fatalf("Dispatch error = %v", err)
	}
	if dispatchCalls != 0 {
		t.Fatalf("dispatcher calls = %d, want zero", dispatchCalls)
	}
	if record := lease.Record(); record.Phase != PhasePrepared ||
		record.PendingPersisted ||
		record.ActionAttemptCount != 0 ||
		record.RawInputCount != 0 {
		t.Fatalf("lease record after pending failure = %+v", record)
	}
	if _, err := lease.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPostInputPersistenceFailureRecoversAsUnknownWithoutResend(t *testing.T) {
	client := newFakeBrowserClient()
	base, err := NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	journal := &phaseFailJournal{Journal: base, phase: PhaseActionPerformed, remaining: 1}
	engine, err := New(Config{
		Client:  client,
		Journal: journal,
		Budget: cdp.BrowserResourceBudgetOptions{
			MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
		},
		CloseTimeout:      500 * time.Millisecond,
		ClosePollInterval: time.Millisecond,
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 17, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-post-input-failure",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	dispatchCalls := 0
	outcome, err := lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		dispatchCalls++
		return DispatchOutcome{Dispatch: DispatchPerformed, RawInputAttempted: true}, nil
	}))
	if err == nil || outcome.Dispatch != DispatchPerformed || dispatchCalls != 1 {
		t.Fatalf("Dispatch outcome=%+v calls=%d err=%v", outcome, dispatchCalls, err)
	}
	if record := lease.Record(); record.Phase != PhaseActionPerformed || record.RawInputCount != 1 {
		t.Fatalf("local post-input record = %+v", record)
	}
	persisted, err := base.Load(context.Background(), "run-post-input-failure")
	if err != nil {
		t.Fatalf("load persisted pending state: %v", err)
	}
	if persisted.Phase != PhaseActionPending || !persisted.PendingPersisted || persisted.RawInputCount != 0 {
		t.Fatalf("persisted post-input state = %+v", persisted)
	}

	cleanup, err := engine.Recover(context.Background(), "run-post-input-failure")
	if err != nil || !cleanup.TargetGone {
		t.Fatalf("Recover cleanup=%+v err=%v", cleanup, err)
	}
	recovered, err := base.Load(context.Background(), "run-post-input-failure")
	if err != nil {
		t.Fatalf("load recovered record: %v", err)
	}
	if recovered.Dispatch != DispatchUnknown ||
		recovered.RawInputCount != 1 ||
		recovered.LastErrorClass != "action_dispatch_ambiguous_after_restart" ||
		dispatchCalls != 1 {
		t.Fatalf("recovered post-input record=%+v calls=%d", recovered, dispatchCalls)
	}
}

func TestDispatchRetriesOnlyNotPerformedAndRawInputAtMostOnce(t *testing.T) {
	client := newFakeBrowserClient()
	engine, _ := newTestEngine(t, client, Config{MaxDispatchAttempts: 2})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-retry",
		Provider:   "gemini",
		Operation:  "ask",
		InitialURL: "https://gemini.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}

	first, err := lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		return DispatchOutcome{Dispatch: DispatchNotPerformed, RawInputAttempted: false}, errors.New("control became stale before raw input")
	}))
	if err == nil || first.Dispatch != DispatchNotPerformed {
		t.Fatalf("first dispatch = %+v, err=%v", first, err)
	}
	afterFirst := lease.Record()
	if afterFirst.Phase != PhasePrepared ||
		afterFirst.Dispatch != DispatchNotPerformed ||
		afterFirst.ActionAttemptCount != 1 ||
		afterFirst.RawInputCount != 0 {
		t.Fatalf("record after not_performed = %+v", afterFirst)
	}

	second, err := lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		return DispatchOutcome{Dispatch: DispatchPerformed, RawInputAttempted: true}, nil
	}))
	if err != nil || second.Dispatch != DispatchPerformed {
		t.Fatalf("second dispatch = %+v, err=%v", second, err)
	}
	afterSecond := lease.Record()
	if afterSecond.Phase != PhaseActionPerformed ||
		afterSecond.ActionAttemptCount != 2 ||
		afterSecond.RawInputCount != 1 {
		t.Fatalf("record after performed = %+v", afterSecond)
	}
	if _, err := lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		t.Fatal("third dispatcher must not run")
		return DispatchOutcome{}, nil
	})); err == nil {
		t.Fatal("third dispatch succeeded after performed action")
	}
	if _, err := lease.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestUnknownDispatchCanOnlyCompleteAsIncomplete(t *testing.T) {
	client := newFakeBrowserClient()
	engine, _ := newTestEngine(t, client, Config{})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-unknown-incomplete",
		Provider:   "grok",
		Operation:  "ask",
		InitialURL: "https://grok.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	if _, err := lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		return DispatchOutcome{Dispatch: DispatchUnknown, RawInputAttempted: true}, nil
	})); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if err := lease.MarkTerminal(context.Background()); err == nil {
		t.Fatal("unknown dispatch was incorrectly marked terminal")
	}
	if err := lease.MarkIncomplete(context.Background()); err != nil {
		t.Fatalf("MarkIncomplete: %v", err)
	}
	if lease.Record().Phase != PhaseIncomplete {
		t.Fatalf("phase = %q, want incomplete", lease.Record().Phase)
	}
	if _, err := lease.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAcknowledgementAndTerminalPersistenceFailuresRemainRetryable(t *testing.T) {
	client := newFakeBrowserClient()
	base, err := NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	journal := &phaseFailJournal{Journal: base}
	engine := newEngineWithJournal(t, client, journal)
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-safe-state-retry",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	if _, err := lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		return DispatchOutcome{Dispatch: DispatchPerformed, RawInputAttempted: true}, nil
	})); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	journal.failNext(PhaseAcknowledged)
	if err := lease.Acknowledge(context.Background(), "conversation-safe-retry"); err == nil {
		t.Fatal("Acknowledge succeeded despite injected persistence failure")
	}
	if record := lease.Record(); record.Phase != PhaseActionPerformed || record.ConversationID != "" {
		t.Fatalf("local record after failed acknowledgement = %+v", record)
	}
	if err := lease.Acknowledge(context.Background(), "conversation-safe-retry"); err != nil {
		t.Fatalf("retry Acknowledge: %v", err)
	}

	journal.failNext(PhaseTerminal)
	if err := lease.MarkTerminal(context.Background()); err == nil {
		t.Fatal("MarkTerminal succeeded despite injected persistence failure")
	}
	if record := lease.Record(); record.Phase != PhaseAcknowledged {
		t.Fatalf("local record after failed terminal persistence = %+v", record)
	}
	if err := lease.MarkTerminal(context.Background()); err != nil {
		t.Fatalf("retry MarkTerminal: %v", err)
	}
	if _, err := lease.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAcknowledgementResolvesUnknownDispatchAndCooldown(t *testing.T) {
	client := newFakeBrowserClient()
	engine, _ := newTestEngine(t, client, Config{})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-ack-resolves-unknown",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	if _, err := lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		return DispatchOutcome{Dispatch: DispatchUnknown, RawInputAttempted: true}, errors.New("private transport ambiguity")
	})); err == nil {
		t.Fatal("Dispatch succeeded despite ambiguous provider result")
	}
	if record := lease.Record(); record.Dispatch != DispatchUnknown || record.RetryAt == "" {
		t.Fatalf("unknown record before acknowledgement = %+v", record)
	}
	if err := lease.Acknowledge(context.Background(), "conversation-acknowledged"); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	record := lease.Record()
	if record.Dispatch != DispatchPerformed ||
		record.RetryAt != "" ||
		record.LastErrorClass != "" ||
		record.Phase != PhaseAcknowledged {
		t.Fatalf("acknowledged record = %+v", record)
	}
	if _, err := lease.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPostconditionResolvesUnknownDispatch(t *testing.T) {
	client := newFakeBrowserClient()
	engine, _ := newTestEngine(t, client, Config{})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-postcondition",
		Provider:   "claude",
		Operation:  "conversations.delete",
		InitialURL: "about:blank",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	if _, err := lease.Dispatch(context.Background(), DispatchFunc(
		func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
			return DispatchOutcome{
				Dispatch: DispatchUnknown, RawInputAttempted: true,
			}, errors.New("private transport ambiguity")
		},
	)); err == nil {
		t.Fatal("Dispatch succeeded despite ambiguous provider result")
	}
	if err := lease.ConfirmPostcondition(
		context.Background(),
		"redirected_to_new_without_conversation_id",
	); err != nil {
		t.Fatalf("ConfirmPostcondition: %v", err)
	}
	if err := lease.MarkTerminal(context.Background()); err != nil {
		t.Fatalf("MarkTerminal: %v", err)
	}
	record := lease.Record()
	if record.Dispatch != DispatchPerformed ||
		record.Phase != PhaseTerminal ||
		record.Postcondition != "redirected_to_new_without_conversation_id" ||
		record.RetryAt != "" {
		t.Fatalf("record = %+v", record)
	}
	if _, err := lease.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestLeaseArchivesCompletedActionBeforeSecondDispatch(t *testing.T) {
	client := newFakeBrowserClient()
	engine, _ := newTestEngine(t, client, Config{})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-two-actions",
		Provider:   "claude",
		Operation:  "calibrate",
		ActionName: "send",
		InitialURL: "about:blank",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared send: %v", err)
	}
	if _, err := lease.Dispatch(context.Background(), DispatchFunc(
		func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
			return DispatchOutcome{
				Dispatch: DispatchPerformed, RawInputAttempted: true,
			}, nil
		},
	)); err != nil {
		t.Fatalf("Dispatch send: %v", err)
	}
	if err := lease.Acknowledge(context.Background(), "conversation-two-actions"); err != nil {
		t.Fatalf("Acknowledge send: %v", err)
	}
	if err := lease.MarkTerminal(context.Background()); err != nil {
		t.Fatalf("MarkTerminal send: %v", err)
	}
	if err := lease.BeginNextAction(context.Background(), "delete"); err != nil {
		t.Fatalf("BeginNextAction: %v", err)
	}
	record := lease.Record()
	if record.Phase != PhaseAttached ||
		record.ActionName != "delete" ||
		record.Dispatch != "" ||
		record.RawInputCount != 0 ||
		len(record.CompletedActions) != 1 ||
		record.CompletedActions[0].Name != "send" ||
		record.CompletedActions[0].Dispatch != DispatchPerformed ||
		record.CompletedActions[0].CompletionPhase != PhaseTerminal {
		t.Fatalf("record after action advance = %+v", record)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared delete: %v", err)
	}
	if _, err := lease.Dispatch(context.Background(), DispatchFunc(
		func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
			return DispatchOutcome{
				Dispatch: DispatchPerformed, RawInputAttempted: true,
			}, nil
		},
	)); err != nil {
		t.Fatalf("Dispatch delete: %v", err)
	}
	if err := lease.ConfirmPostcondition(
		context.Background(),
		"redirected_to_new_without_conversation_id",
	); err != nil {
		t.Fatalf("ConfirmPostcondition delete: %v", err)
	}
	if err := lease.MarkTerminal(context.Background()); err != nil {
		t.Fatalf("MarkTerminal delete: %v", err)
	}
	record = lease.Record()
	if record.ActionName != "delete" ||
		record.Dispatch != DispatchPerformed ||
		record.RawInputCount != 1 ||
		record.Postcondition == "" ||
		len(record.CompletedActions) != 1 {
		t.Fatalf("terminal two-action record = %+v", record)
	}
	if _, err := lease.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestUnknownDispatchBlocksRetryAndRecoveryOnlyCloses(t *testing.T) {
	client := newFakeBrowserClient()
	engine, journal := newTestEngine(t, client, Config{AmbiguousCooldown: 15 * time.Minute})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-unknown",
		Provider:   "grok",
		Operation:  "ask",
		InitialURL: "https://grok.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	dispatchCalls := 0
	outcome, err := lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		dispatchCalls++
		return DispatchOutcome{Dispatch: DispatchUnknown, RawInputAttempted: true}, errors.New("transport lost after raw input")
	}))
	if err == nil || outcome.Dispatch != DispatchUnknown {
		t.Fatalf("unknown dispatch = %+v, err=%v", outcome, err)
	}
	if _, err := lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		dispatchCalls++
		return DispatchOutcome{}, nil
	})); err == nil {
		t.Fatal("retry succeeded after unknown dispatch")
	}
	if dispatchCalls != 1 {
		t.Fatalf("dispatcher calls = %d, want one", dispatchCalls)
	}

	cleanup, err := engine.Recover(context.Background(), "run-unknown")
	if err != nil {
		t.Fatalf("Recover: %v; cleanup=%+v", err, cleanup)
	}
	if !cleanup.TargetGone || client.hasTarget("owned-1") || dispatchCalls != 1 {
		t.Fatalf("recovery cleanup=%+v target=%v dispatchCalls=%d", cleanup, client.hasTarget("owned-1"), dispatchCalls)
	}
	record, err := journal.Load(context.Background(), "run-unknown")
	if err != nil {
		t.Fatalf("load recovered record: %v", err)
	}
	if record.Phase != PhaseClosed ||
		record.Dispatch != DispatchUnknown ||
		record.RawInputCount != 1 ||
		record.RetryAt == "" {
		t.Fatalf("recovered unknown record = %+v", record)
	}
}

func TestCloseFailureLeavesExactRecoveryRecordThenRecoverySettles(t *testing.T) {
	client := newFakeBrowserClient("user-page")
	engine, journal := newTestEngine(t, client, Config{
		CloseTimeout:      5 * time.Millisecond,
		ClosePollInterval: time.Millisecond,
	})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-close-failure",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	if err := lease.MarkIncomplete(context.Background()); err != nil {
		t.Fatalf("MarkIncomplete: %v", err)
	}
	client.fail["Target.closeTarget"] = errors.New("synthetic close failure")
	cleanup, err := lease.Close(context.Background())
	if err == nil || cleanup.State != CleanupFailed || cleanup.TargetGone {
		t.Fatalf("Close cleanup=%+v err=%v", cleanup, err)
	}
	record, loadErr := journal.Load(context.Background(), "run-close-failure")
	if loadErr != nil {
		t.Fatalf("load cleanup-pending record: %v", loadErr)
	}
	if record.Phase != PhaseCleanupPending ||
		record.Cleanup != CleanupFailed ||
		record.LastErrorClass != "exact_target_cleanup_failed" ||
		record.TargetID != "owned-1" {
		t.Fatalf("cleanup-pending record = %+v", record)
	}
	if !client.hasTarget("owned-1") || !client.hasTarget("user-page") {
		t.Fatalf("targets after failed close: owned=%v user=%v", client.hasTarget("owned-1"), client.hasTarget("user-page"))
	}

	delete(client.fail, "Target.closeTarget")
	cleanup, err = engine.Recover(context.Background(), "run-close-failure")
	if err != nil || !cleanup.TargetGone {
		t.Fatalf("Recover cleanup=%+v err=%v", cleanup, err)
	}
	if client.hasTarget("owned-1") || !client.hasTarget("user-page") {
		t.Fatalf("targets after recovery: owned=%v user=%v", client.hasTarget("owned-1"), client.hasTarget("user-page"))
	}
	record, loadErr = journal.Load(context.Background(), "run-close-failure")
	if loadErr != nil || record.Phase != PhaseClosed || record.Cleanup != CleanupClosed {
		t.Fatalf("settled recovery record=%+v err=%v", record, loadErr)
	}
}

func TestCleanupPersistenceFailuresRemainExactlyRecoverable(t *testing.T) {
	for _, failedPhase := range []Phase{PhaseCleanupPending, PhaseClosed} {
		t.Run(string(failedPhase), func(t *testing.T) {
			client := newFakeBrowserClient("user-page")
			base, err := NewFileJournal(t.TempDir())
			if err != nil {
				t.Fatalf("NewFileJournal: %v", err)
			}
			journal := &phaseFailJournal{Journal: base}
			engine := newEngineWithJournal(t, client, journal)
			runID := "run-cleanup-persist-" + string(failedPhase)
			lease, err := engine.Acquire(context.Background(), AcquireRequest{
				RunID:      runID,
				Provider:   "claude",
				Operation:  "ask",
				InitialURL: "https://claude.test/new",
			})
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if err := lease.MarkPrepared(context.Background()); err != nil {
				t.Fatalf("MarkPrepared: %v", err)
			}
			if err := lease.MarkIncomplete(context.Background()); err != nil {
				t.Fatalf("MarkIncomplete: %v", err)
			}
			journal.failNext(failedPhase)
			cleanup, err := lease.Close(context.Background())
			if err == nil || !cleanup.TargetGone {
				t.Fatalf("Close cleanup=%+v err=%v", cleanup, err)
			}
			if client.hasTarget("owned-1") || !client.hasTarget("user-page") {
				t.Fatalf("targets after close: owned=%v user=%v", client.hasTarget("owned-1"), client.hasTarget("user-page"))
			}

			cleanup, err = engine.Recover(context.Background(), runID)
			if err != nil || !cleanup.TargetGone {
				t.Fatalf("Recover cleanup=%+v err=%v", cleanup, err)
			}
			record, loadErr := base.Load(context.Background(), runID)
			if loadErr != nil || record.Phase != PhaseClosed || record.Cleanup != CleanupClosed {
				t.Fatalf("recovered record=%+v err=%v", record, loadErr)
			}
			if !client.hasTarget("user-page") {
				t.Fatal("recovery closed sibling user target")
			}
		})
	}
}

func TestTargetCrashAndCanceledCallerStillSettleExactCleanup(t *testing.T) {
	client := newFakeBrowserClient("user-page")
	engine, journal := newTestEngine(t, client, Config{})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-target-crash",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	client.removeTarget("owned-1")
	client.fail["Target.closeTarget"] = errors.New("target already gone")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cleanup, err := lease.Close(ctx)
	if err != nil || cleanup.State != CleanupClosed || !cleanup.TargetGone {
		t.Fatalf("Close cleanup=%+v err=%v", cleanup, err)
	}
	if !client.hasTarget("user-page") {
		t.Fatal("target-crash cleanup closed sibling user target")
	}
	record, loadErr := journal.Load(context.Background(), "run-target-crash")
	if loadErr != nil || record.Phase != PhaseClosed || record.Cleanup != CleanupClosed {
		t.Fatalf("target-crash record=%+v err=%v", record, loadErr)
	}
}

func TestRecoveryTreatsPersistedActionPendingAsUnknownWithoutDispatch(t *testing.T) {
	client := newFakeBrowserClient()
	engine, journal := newTestEngine(t, client, Config{AmbiguousCooldown: time.Hour})
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-crash",
		Provider:   "perplexity",
		Operation:  "ask",
		InitialURL: "https://perplexity.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	record := lease.Record()
	record.Phase = PhaseActionPending
	record.ActionAttemptCount = 1
	record.PendingPersisted = true
	record.UpdatedAt = time.Date(2026, 7, 25, 17, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if err := journal.Save(context.Background(), record); err != nil {
		t.Fatalf("persist synthetic action_pending: %v", err)
	}

	cleanup, err := engine.Recover(context.Background(), "run-crash")
	if err != nil {
		t.Fatalf("Recover: %v; cleanup=%+v", err, cleanup)
	}
	recovered, err := journal.Load(context.Background(), "run-crash")
	if err != nil {
		t.Fatalf("load recovery result: %v", err)
	}
	if recovered.Phase != PhaseClosed ||
		recovered.Dispatch != DispatchUnknown ||
		recovered.RawInputCount != 1 ||
		recovered.RetryAt == "" ||
		recovered.LastErrorClass != "action_dispatch_ambiguous_after_restart" {
		t.Fatalf("recovered pending record = %+v", recovered)
	}
	if client.callCount("Target.createTarget") != 1 {
		t.Fatalf("recovery create calls = %d, want original create only", client.callCount("Target.createTarget"))
	}
}

func TestRecoveryJournalDoesNotPersistDispatcherPrivateData(t *testing.T) {
	const canary = "PRIVATE_PROMPT_CANARY_7f63"
	stateDir := t.TempDir()
	journal, err := NewFileJournal(stateDir)
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	client := newFakeBrowserClient()
	engine, err := New(Config{
		Client:  client,
		Journal: journal,
		Budget:  cdp.BrowserResourceBudgetOptions{MaxTabs: 15, MaxWindows: 5, BrowserMode: "headed"},
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 17, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lease, err := engine.Acquire(context.Background(), AcquireRequest{
		RunID:      "run-private",
		Provider:   "claude",
		Operation:  "ask",
		InitialURL: "https://claude.test/new",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	if _, err := lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		_ = canary
		return DispatchOutcome{Dispatch: DispatchUnknown, RawInputAttempted: true}, errors.New(canary)
	})); err == nil {
		t.Fatal("Dispatch succeeded despite synthetic private provider error")
	} else if strings.Contains(err.Error(), canary) {
		t.Fatalf("private canary leaked through dispatch error: %v", err)
	}
	if _, err := lease.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err = filepath.Walk(stateDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), canary) {
			return fmt.Errorf("private canary leaked into %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newTestEngine(t *testing.T, client *fakeBrowserClient, override Config) (*Engine, *FileJournal) {
	t.Helper()
	journal, err := NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	config := Config{
		Client:  client,
		Journal: journal,
		Budget: cdp.BrowserResourceBudgetOptions{
			MaxTabs:       15,
			MaxTabsSource: "test",
			MaxWindows:    5,
			BrowserMode:   "headed",
		},
		CloseTimeout:        500 * time.Millisecond,
		ClosePollInterval:   time.Millisecond,
		MaxDispatchAttempts: 2,
		AmbiguousCooldown:   5 * time.Minute,
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 17, 0, 0, 0, time.UTC)
		},
	}
	if override.Budget.MaxTabs != 0 {
		config.Budget = override.Budget
	}
	if override.AllowOverBudget {
		config.AllowOverBudget = true
	}
	if override.CloseTimeout != 0 {
		config.CloseTimeout = override.CloseTimeout
	}
	if override.ClosePollInterval != 0 {
		config.ClosePollInterval = override.ClosePollInterval
	}
	if override.MaxDispatchAttempts != 0 {
		config.MaxDispatchAttempts = override.MaxDispatchAttempts
	}
	if override.AmbiguousCooldown != 0 {
		config.AmbiguousCooldown = override.AmbiguousCooldown
	}
	if override.InputLockPath != "" {
		config.InputLockPath = override.InputLockPath
	}
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return engine, journal
}

func newEngineWithJournal(t *testing.T, client *fakeBrowserClient, journal Journal) *Engine {
	t.Helper()
	engine, err := New(Config{
		Client:  client,
		Journal: journal,
		Budget: cdp.BrowserResourceBudgetOptions{
			MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
		},
		CloseTimeout:        500 * time.Millisecond,
		ClosePollInterval:   time.Millisecond,
		MaxDispatchAttempts: 2,
		AmbiguousCooldown:   5 * time.Minute,
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 17, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return engine
}

type fakeBrowserClient struct {
	mu      sync.Mutex
	targets map[string]cdp.TargetInfo
	trace   []string
	counts  map[string]int
	fail    map[string]error
	nextID  int
}

func newFakeBrowserClient(targetIDs ...string) *fakeBrowserClient {
	client := &fakeBrowserClient{
		targets: map[string]cdp.TargetInfo{},
		counts:  map[string]int{},
		fail:    map[string]error{},
	}
	for _, targetID := range targetIDs {
		client.targets[targetID] = cdp.TargetInfo{
			TargetID: targetID,
			Type:     "page",
			URL:      "https://user.test/",
		}
	}
	return client
}

func (c *fakeBrowserClient) Call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[method]++
	if err := c.fail[method]; err != nil {
		c.trace = append(c.trace, method)
		return err
	}
	switch method {
	case "Target.getTargets":
		c.trace = append(c.trace, method)
		targets := make([]cdp.TargetInfo, 0, len(c.targets))
		for _, target := range c.targets {
			targets = append(targets, target)
		}
		return assignJSON(result, map[string]any{"targetInfos": targets})
	case "Browser.getWindowForTarget":
		targetID := stringParam(params, "targetId")
		c.trace = append(c.trace, method+":"+targetID)
		return assignJSON(result, map[string]any{"windowId": 1})
	case "Target.createTarget":
		c.trace = append(c.trace, method)
		c.nextID++
		targetID := fmt.Sprintf("owned-%d", c.nextID)
		c.targets[targetID] = cdp.TargetInfo{
			TargetID: targetID,
			Type:     "page",
			URL:      stringParam(params, "url"),
		}
		return assignJSON(result, map[string]any{"targetId": targetID})
	case "Target.attachToTarget":
		targetID := stringParam(params, "targetId")
		c.trace = append(c.trace, method+":"+targetID)
		if _, ok := c.targets[targetID]; !ok {
			return fmt.Errorf("target %s not found", targetID)
		}
		return assignJSON(result, map[string]any{"sessionId": "session-" + targetID})
	case "Target.detachFromTarget":
		sessionID := stringParam(params, "sessionId")
		c.trace = append(c.trace, method+":"+sessionID)
		return assignJSON(result, map[string]any{})
	case "Target.closeTarget":
		targetID := stringParam(params, "targetId")
		c.trace = append(c.trace, method+":"+targetID)
		delete(c.targets, targetID)
		return assignJSON(result, map[string]any{"success": true})
	default:
		c.trace = append(c.trace, method)
		return fmt.Errorf("unexpected browser call %s", method)
	}
}

func (c *fakeBrowserClient) CallSession(ctx context.Context, sessionID, method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[method]++
	c.trace = append(c.trace, method+":"+sessionID)
	if err := c.fail[method]; err != nil {
		return err
	}
	return assignJSON(result, map[string]any{})
}

func (c *fakeBrowserClient) traceSnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.trace...)
}

func (c *fakeBrowserClient) callCount(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[method]
}

func (c *fakeBrowserClient) hasTarget(targetID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.targets[targetID]
	return ok
}

func (c *fakeBrowserClient) removeTarget(targetID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.targets, targetID)
}

type phaseFailJournal struct {
	Journal

	mu        sync.Mutex
	phase     Phase
	remaining int
}

func (j *phaseFailJournal) Save(ctx context.Context, record Record) error {
	j.mu.Lock()
	if record.Phase == j.phase && j.remaining > 0 {
		j.remaining--
		j.mu.Unlock()
		return errors.New("synthetic recovery persistence failure")
	}
	j.mu.Unlock()
	return j.Journal.Save(ctx, record)
}

func (j *phaseFailJournal) failNext(phase Phase) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.phase = phase
	j.remaining = 1
}

func stringParam(params any, key string) string {
	data, _ := json.Marshal(params)
	var values map[string]any
	_ = json.Unmarshal(data, &values)
	value, _ := values[key].(string)
	return value
}

func assignJSON(dst any, src any) error {
	if dst == nil {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
