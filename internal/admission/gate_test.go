package admission

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGateSerializesProviderAndPersistsOwnerOnlyState(t *testing.T) {
	clock := newTestClock(time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC))
	stateDir := t.TempDir()
	gate, err := New(Config{
		StateDir:       stateDir,
		MinimumSpacing: 2 * time.Minute,
		Now:            clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := gate.Acquire(context.Background(), Request{
		Provider: "claude", Operation: "ask", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = gate.Acquire(ctx, Request{
		Provider: "claude", Operation: "ask", RunID: "run-2",
	})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("concurrent Acquire error = %v, want context deadline", err)
	}
	if err := first.Release(Release{Outcome: "terminal"}); err != nil {
		t.Fatalf("Release: %v", err)
	}

	path := filepath.Join(stateDir, "webagent", "admission", "claude.json")
	for _, path := range []string{path, path + ".lock"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
	record, found, err := readRecord(path)
	if err != nil || !found {
		t.Fatalf("readRecord found=%v err=%v", found, err)
	}
	if record.Phase != PhaseReleased ||
		record.Outcome != "terminal" ||
		record.RunID != "run-1" ||
		record.NextAllowedAt != "2026-07-25T18:02:00Z" {
		t.Fatalf("released record = %+v", record)
	}
}

func TestGateEnforcesSpacingAndCooldownThenCarriesHistory(t *testing.T) {
	clock := newTestClock(time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC))
	gate, err := New(Config{
		StateDir:       t.TempDir(),
		MinimumSpacing: time.Minute,
		Now:            clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := gate.Acquire(context.Background(), Request{
		Provider: "gemini", Operation: "ask", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	cooldown := clock.Now().Add(10 * time.Minute)
	if err := first.Release(Release{Outcome: "rate_limited", CooldownUntil: cooldown}); err != nil {
		t.Fatalf("first Release: %v", err)
	}

	_, err = gate.Acquire(context.Background(), Request{
		Provider: "gemini", Operation: "ask", RunID: "run-2",
	})
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Acquire error = %v, want BlockedError", err)
	}
	if blocked.Reason != "cooldown" || !blocked.RetryAt.Equal(cooldown) {
		t.Fatalf("blocked = %+v", blocked)
	}

	clock.Advance(10 * time.Minute)
	second, err := gate.Acquire(context.Background(), Request{
		Provider: "gemini", Operation: "ask", RunID: "run-2",
	})
	if err != nil {
		t.Fatalf("second Acquire after cooldown: %v", err)
	}
	record := second.Record()
	if record.PreviousRunID != "run-1" || record.PreviousOutcome != "rate_limited" {
		t.Fatalf("history = %+v", record)
	}
	if err := second.Release(Release{Outcome: "terminal"}); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestGateWaitsForMinimumSpacingAndRechecksAtomically(t *testing.T) {
	clock := newTestClock(
		time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC),
	)
	waitCalls := 0
	gate, err := New(Config{
		StateDir:       t.TempDir(),
		MinimumSpacing: time.Minute,
		Now:            clock.Now,
		WaitUntil: func(
			ctx context.Context,
			until time.Time,
		) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			waitCalls++
			clock.Set(until)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := gate.Acquire(context.Background(), Request{
		Provider: "gemini", Operation: "auth.refresh", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := first.Release(Release{Outcome: OutcomeCompleted}); err != nil {
		t.Fatalf("first Release: %v", err)
	}

	second, err := gate.Acquire(context.Background(), Request{
		Provider: "gemini", Operation: "ask", RunID: "run-2",
	})
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if waitCalls != 1 {
		t.Fatalf("minimum-spacing waits = %d, want 1", waitCalls)
	}
	record := second.Record()
	if record.PreviousRunID != "run-1" ||
		record.PreviousOutcome != OutcomeCompleted {
		t.Fatalf("rechecked admission history = %+v", record)
	}
	if err := second.Release(Release{Outcome: OutcomeTerminal}); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestGateMinimumSpacingWaitHonorsContext(t *testing.T) {
	clock := newTestClock(
		time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC),
	)
	gate, err := New(Config{
		StateDir:       t.TempDir(),
		MinimumSpacing: time.Minute,
		Now:            clock.Now,
		WaitUntil: func(
			ctx context.Context,
			_ time.Time,
		) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := gate.Acquire(context.Background(), Request{
		Provider: "claude", Operation: "auth.refresh", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := first.Release(Release{Outcome: OutcomeCompleted}); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = gate.Acquire(ctx, Request{
		Provider: "claude", Operation: "ask", RunID: "run-2",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled spacing wait error = %v", err)
	}
}

func TestGateFailsClosedForOrphanedActiveMutationAfterSpacing(t *testing.T) {
	clock := newTestClock(time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC))
	gate, err := New(Config{
		StateDir:       t.TempDir(),
		MinimumSpacing: time.Second,
		Now:            clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	orphan, err := gate.Acquire(context.Background(), Request{
		Provider: "chatgpt", Operation: "ask", RunID: "run-orphan",
	})
	if err != nil {
		t.Fatalf("orphan Acquire: %v", err)
	}
	if err := orphan.lock.Release(); err != nil {
		t.Fatalf("simulate process exit: %v", err)
	}
	clock.Advance(time.Hour)

	_, err = gate.Acquire(context.Background(), Request{
		Provider: "chatgpt", Operation: "ask", RunID: "run-retry",
	})
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Acquire after orphan = %v, want BlockedError", err)
	}
	if blocked.Reason != "unreconciled_active_mutation" ||
		blocked.RunID != "run-orphan" ||
		blocked.Operation != "ask" ||
		!blocked.ResolutionNeeded ||
		blocked.RetryAt.Year() != 9999 {
		t.Fatalf("orphan block = %+v, want indefinite exact-run resolution requirement", blocked)
	}
}

func TestGateAutomaticallyAbandonsOrphanedReadOnlyRun(t *testing.T) {
	clock := newTestClock(time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC))
	gate, err := New(Config{
		StateDir:       t.TempDir(),
		MinimumSpacing: time.Second,
		Now:            clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	orphan, err := gate.Acquire(context.Background(), Request{
		Provider: "claude", Operation: "conversations.list", RunID: "run-read",
	})
	if err != nil {
		t.Fatalf("orphan read Acquire: %v", err)
	}
	if err := orphan.lock.Release(); err != nil {
		t.Fatalf("simulate read process exit: %v", err)
	}
	clock.Advance(time.Hour)

	next, err := gate.Acquire(context.Background(), Request{
		Provider: "claude", Operation: "conversations.detail", RunID: "run-next",
	})
	if err != nil {
		t.Fatalf("Acquire after read-only orphan: %v", err)
	}
	record := next.Record()
	if record.PreviousRunID != "run-read" || record.PreviousOutcome != OutcomeAbandoned {
		t.Fatalf("read-only orphan history = %+v, want safely abandoned predecessor", record)
	}
	if err := next.Release(Release{Outcome: OutcomeCompleted}); err != nil {
		t.Fatalf("release next read: %v", err)
	}
}

func TestGateUnknownOutcomeBlocksUntilExactExplicitResolution(t *testing.T) {
	clock := newTestClock(time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC))
	gate, err := New(Config{
		StateDir:       t.TempDir(),
		MinimumSpacing: time.Second,
		Now:            clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := gate.Acquire(context.Background(), Request{
		Provider: "gemini", Operation: "ask", RunID: "run-unknown",
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := first.Release(Release{Outcome: OutcomeUnknown}); err != nil {
		t.Fatalf("release unknown: %v", err)
	}
	clock.Advance(time.Hour)

	_, err = gate.Acquire(context.Background(), Request{
		Provider: "gemini", Operation: "ask", RunID: "run-retry",
	})
	var blocked *BlockedError
	if !errors.As(err, &blocked) ||
		blocked.Reason != "unresolved_unknown_outcome" ||
		!blocked.ResolutionNeeded {
		t.Fatalf("unknown outcome block = %+v err=%v", blocked, err)
	}
	if _, err := gate.Resolve(context.Background(), Request{
		Provider: "gemini", Operation: "ask", RunID: "wrong-run",
	}); err == nil {
		t.Fatal("Resolve accepted a mismatched run")
	}
	resolved, err := gate.Resolve(context.Background(), Request{
		Provider: "gemini", Operation: "ask", RunID: "run-unknown",
	})
	if err != nil {
		t.Fatalf("Resolve exact unknown run: %v", err)
	}
	if resolved.Phase != PhaseReleased || resolved.Outcome != OutcomeAcknowledged {
		t.Fatalf("resolved record = %+v", resolved)
	}
	status, found, err := gate.Status(context.Background(), "gemini")
	if err != nil || !found || status.Outcome != OutcomeAcknowledged {
		t.Fatalf("Status after resolve = found %v record %+v err %v", found, status, err)
	}
	clock.Advance(time.Second)
	next, err := gate.Acquire(context.Background(), Request{
		Provider: "gemini", Operation: "ask", RunID: "run-new",
	})
	if err != nil {
		t.Fatalf("Acquire after explicit resolution: %v", err)
	}
	if err := next.Release(Release{Outcome: OutcomeTerminal}); err != nil {
		t.Fatalf("release new run: %v", err)
	}
}

func TestGateStateContainsNoPrivateRequestData(t *testing.T) {
	const canary = "PRIVATE_PROMPT_CANARY_2ac4"
	stateDir := t.TempDir()
	gate, err := New(Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lease, err := gate.Acquire(context.Background(), Request{
		Provider: "grok", Operation: "ask", RunID: "run-private",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	_ = canary
	if err := lease.Release(Release{Outcome: "terminal"}); err != nil {
		t.Fatalf("Release: %v", err)
	}
	err = filepath.Walk(stateDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), canary) {
			t.Fatalf("private request canary leaked into %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk admission state: %v", err)
	}
}

func TestGateFailsClosedForCorruptOrSymlinkedState(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{
			name: "corrupt",
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			setup: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(filepath.Dir(path), "target")
				if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			dir := filepath.Join(stateDir, "webagent", "admission")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			test.setup(t, filepath.Join(dir, "claude.json"))
			gate, err := New(Config{StateDir: stateDir})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			lease, err := gate.Acquire(context.Background(), Request{
				Provider: "claude", Operation: "ask", RunID: "run-corrupt",
			})
			if err == nil {
				_ = lease.Release(Release{})
				t.Fatal("Acquire succeeded with unsafe state")
			}
		})
	}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(now time.Time) *testClock {
	return &testClock{now: now}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func (c *testClock) Set(value time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = value
}
