package authreadiness

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type recordingReloader struct {
	ignoreCache []bool
	err         error
}

func (r *recordingReloader) Reload(_ context.Context, ignoreCache bool) error {
	r.ignoreCache = append(r.ignoreCache, ignoreCache)
	return r.err
}

func TestPrepareAttemptUsesInitialReloadHardReloadSequence(t *testing.T) {
	reloader := &recordingReloader{}
	var stages []Stage
	for attempt := 1; attempt <= 3; attempt++ {
		stage, err := PrepareAttempt(
			context.Background(),
			reloader,
			attempt,
			3,
		)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		stages = append(stages, stage)
	}

	if want := []Stage{
		StageInitialLoad,
		StageReload,
		StageHardReload,
	}; !reflect.DeepEqual(stages, want) {
		t.Fatalf("stages = %v, want %v", stages, want)
	}
	if want := []bool{false, true}; !reflect.DeepEqual(reloader.ignoreCache, want) {
		t.Fatalf("ignoreCache = %v, want %v", reloader.ignoreCache, want)
	}
}

func TestPrepareAttemptSupportsExplicitShortNonAuthSequences(t *testing.T) {
	reloader := &recordingReloader{}
	if stage, err := PrepareAttempt(
		context.Background(),
		reloader,
		1,
		1,
	); err != nil || stage != StageInitialLoad {
		t.Fatalf("single attempt = (%q, %v)", stage, err)
	}
	if len(reloader.ignoreCache) != 0 {
		t.Fatalf("single attempt unexpectedly reloaded: %v", reloader.ignoreCache)
	}

	if _, err := PrepareAttempt(
		context.Background(),
		reloader,
		2,
		2,
	); err != nil {
		t.Fatalf("final attempt: %v", err)
	}
	if want := []bool{true}; !reflect.DeepEqual(reloader.ignoreCache, want) {
		t.Fatalf("ignoreCache = %v, want %v", reloader.ignoreCache, want)
	}
}

func TestPrepareAttemptReturnsReloadAndContractErrors(t *testing.T) {
	reloadErr := errors.New("reload failed")
	if stage, err := PrepareAttempt(
		context.Background(),
		&recordingReloader{err: reloadErr},
		3,
		3,
	); stage != StageHardReload || !errors.Is(err, reloadErr) {
		t.Fatalf("hard reload = (%q, %v)", stage, err)
	}

	for _, tc := range []struct {
		name     string
		reloader Reloader
		attempt  int
		total    int
	}{
		{"nil reloader", nil, 1, 3},
		{"zero total", &recordingReloader{}, 1, 0},
		{"low attempt", &recordingReloader{}, 0, 3},
		{"high attempt", &recordingReloader{}, 4, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PrepareAttempt(
				context.Background(),
				tc.reloader,
				tc.attempt,
				tc.total,
			); err == nil {
				t.Fatal("expected contract error")
			}
		})
	}
}

func TestWaitForEvidenceGivesHardReloadAFinalGraceWindow(t *testing.T) {
	reloader := &recordingReloader{}
	observations := 0
	hardReloadObservations := 0
	result, err := WaitForEvidence(
		context.Background(),
		reloader,
		3,
		30*time.Millisecond,
		time.Millisecond,
		func(context.Context) (bool, error) {
			observations++
			if len(reloader.ignoreCache) == 2 {
				hardReloadObservations++
			}
			return hardReloadObservations >= 2, nil
		},
	)
	if err != nil {
		t.Fatalf("WaitForEvidence: %v", err)
	}
	if !result.Observed ||
		result.Attempt != 3 ||
		result.Stage != StageHardReload ||
		hardReloadObservations < 2 {
		t.Fatalf(
			"result = %+v, observations = %d, hard reload observations = %d",
			result,
			observations,
			hardReloadObservations,
		)
	}
	if want := []bool{false, true}; !reflect.DeepEqual(reloader.ignoreCache, want) {
		t.Fatalf("ignoreCache = %v, want %v", reloader.ignoreCache, want)
	}
}

func TestWaitForEvidenceRejectsInvalidContract(t *testing.T) {
	reloader := &recordingReloader{}
	observe := func(context.Context) (bool, error) { return false, nil }
	for _, tc := range []struct {
		name      string
		attempts  int
		totalWait time.Duration
		interval  time.Duration
		observe   func(context.Context) (bool, error)
	}{
		{"zero attempts", 0, time.Second, time.Millisecond, observe},
		{"one attempt", 1, time.Second, time.Millisecond, observe},
		{"two attempts", 2, time.Second, time.Millisecond, observe},
		{"total wait", 3, 0, time.Millisecond, observe},
		{"interval", 3, time.Second, 0, observe},
		{"observer", 3, time.Second, time.Millisecond, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := WaitForEvidence(
				context.Background(),
				reloader,
				tc.attempts,
				tc.totalWait,
				tc.interval,
				tc.observe,
			); err == nil {
				t.Fatal("expected contract error")
			}
		})
	}
}

func TestWaitForEvidenceUsesOneOverallBudget(t *testing.T) {
	reloader := &recordingReloader{}
	started := time.Now()
	result, err := WaitForEvidence(
		context.Background(),
		reloader,
		3,
		30*time.Millisecond,
		time.Millisecond,
		func(context.Context) (bool, error) {
			return false, nil
		},
	)
	if err != nil {
		t.Fatalf("WaitForEvidence: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
		t.Fatalf("elapsed = %s, want one bounded total wait", elapsed)
	}
	if !result.EvidenceAbsent() || result.ObservationFailed() {
		t.Fatalf("result = %+v", result)
	}
	if want := []bool{false, true}; !reflect.DeepEqual(reloader.ignoreCache, want) {
		t.Fatalf("ignoreCache = %v, want %v", reloader.ignoreCache, want)
	}
}

func TestWaitForEvidenceBoundsBlockingObserversByOneOverallBudget(t *testing.T) {
	reloader := &recordingReloader{}
	started := time.Now()
	result, err := WaitForEvidence(
		context.Background(),
		reloader,
		3,
		90*time.Millisecond,
		time.Millisecond,
		func(observationCtx context.Context) (bool, error) {
			<-observationCtx.Done()
			return false, nil
		},
	)
	if err != nil {
		t.Fatalf("WaitForEvidence: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 220*time.Millisecond {
		t.Fatalf("elapsed = %s, want one bounded total wait", elapsed)
	}
	if result.EvidenceAbsent() || !result.ObservationFailed() {
		t.Fatalf("result = %+v", result)
	}
	if !errors.Is(result.LastObservationError, ErrObservationIncomplete) {
		t.Fatalf(
			"last observation error = %v, want ErrObservationIncomplete",
			result.LastObservationError,
		)
	}
}

func TestWaitForEvidenceKeepsTerminalObservationErrorsInconclusive(t *testing.T) {
	reloader := &recordingReloader{}
	observationErr := errors.New("observation failed")
	result, err := WaitForEvidence(
		context.Background(),
		reloader,
		3,
		30*time.Millisecond,
		time.Millisecond,
		func(context.Context) (bool, error) {
			if len(reloader.ignoreCache) == 2 {
				return false, observationErr
			}
			return false, nil
		},
	)
	if err != nil {
		t.Fatalf("WaitForEvidence: %v", err)
	}
	if !result.ObservationFailed() ||
		result.EvidenceAbsent() ||
		!errors.Is(result.LastObservationError, observationErr) {
		t.Fatalf("result = %+v", result)
	}
}

func TestWaitForEvidenceClearsRecoveredObservationErrors(t *testing.T) {
	reloader := &recordingReloader{}
	observationErr := errors.New("transient observation failure")
	failedOnce := false
	result, err := WaitForEvidence(
		context.Background(),
		reloader,
		3,
		30*time.Millisecond,
		time.Millisecond,
		func(context.Context) (bool, error) {
			if len(reloader.ignoreCache) == 2 && !failedOnce {
				failedOnce = true
				return false, observationErr
			}
			return false, nil
		},
	)
	if err != nil {
		t.Fatalf("WaitForEvidence: %v", err)
	}
	if !result.EvidenceAbsent() ||
		result.ObservationFailed() ||
		result.LastObservationError != nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestWaitForEvidenceDoesNotClearErrorWithPostExpiryResult(t *testing.T) {
	reloader := &recordingReloader{}
	observationErr := errors.New("final-stage observation failure")
	finalCalls := 0
	result, err := WaitForEvidence(
		context.Background(),
		reloader,
		3,
		30*time.Millisecond,
		time.Millisecond,
		func(observationCtx context.Context) (bool, error) {
			if len(reloader.ignoreCache) < 2 {
				return false, nil
			}
			finalCalls++
			if finalCalls == 1 {
				return false, observationErr
			}
			<-observationCtx.Done()
			return false, nil
		},
	)
	if err != nil {
		t.Fatalf("WaitForEvidence: %v", err)
	}
	if !result.ObservationFailed() ||
		result.EvidenceAbsent() ||
		!errors.Is(result.LastObservationError, observationErr) {
		t.Fatalf("result = %+v", result)
	}
}

func TestSubObservationContextExpiresBeforeParentStage(t *testing.T) {
	parent, cancelParent := context.WithTimeout(
		context.Background(),
		100*time.Millisecond,
	)
	defer cancelParent()
	child, cancelChild, err := SubObservationContext(
		parent,
		20*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("SubObservationContext: %v", err)
	}
	defer cancelChild()
	parentDeadline, _ := parent.Deadline()
	childDeadline, _ := child.Deadline()
	if !childDeadline.Before(parentDeadline) {
		t.Fatalf(
			"child deadline %s is not before parent %s",
			childDeadline,
			parentDeadline,
		)
	}
}

func TestReadinessPollDelayUsesCappedLinearBackoffAndRemainingBudget(
	t *testing.T,
) {
	interval := 10 * time.Millisecond
	for _, test := range []struct {
		name      string
		poll      int
		remaining time.Duration
		want      time.Duration
	}{
		{name: "first", poll: 1, remaining: time.Second, want: 10 * time.Millisecond},
		{name: "second", poll: 2, remaining: time.Second, want: 20 * time.Millisecond},
		{name: "third", poll: 3, remaining: time.Second, want: 30 * time.Millisecond},
		{name: "fourth", poll: 4, remaining: time.Second, want: 40 * time.Millisecond},
		{name: "later capped", poll: 9, remaining: time.Second, want: 40 * time.Millisecond},
		{name: "remaining clamps", poll: 4, remaining: 15 * time.Millisecond, want: 15 * time.Millisecond},
		{name: "exhausted", poll: 1, remaining: 0, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := readinessPollDelay(
				interval,
				test.poll,
				test.remaining,
			); got != test.want {
				t.Fatalf("readinessPollDelay() = %s, want %s", got, test.want)
			}
		})
	}
}
