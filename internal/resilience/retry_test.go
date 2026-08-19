package resilience

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPolicyNormalizesSharedThreeAttemptSchedule(t *testing.T) {
	policy := (Policy{MaxAttempts: 99}).Normalized()
	if policy.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d, want 3", policy.MaxAttempts)
	}
	if got := []time.Duration{
		policy.DelayBeforeAttempt(1),
		policy.DelayBeforeAttempt(2),
		policy.DelayBeforeAttempt(3),
		policy.DelayBeforeAttempt(4),
	}; !reflect.DeepEqual(got, []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second}) {
		t.Fatalf("retry schedule = %v", got)
	}
}

func TestRunSharesBoundedRetryAndOneShotAuthRepair(t *testing.T) {
	attempts := 0
	refreshes := 0
	var waits []time.Duration
	value, report, err := Run(
		context.Background(),
		Policy{MaxAttempts: 9, Backoff: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}},
		Hooks[string]{
			Attempt: func(context.Context, int) (string, error) {
				attempts++
				if attempts < 3 {
					return "", errors.New("transient")
				}
				return "ready", nil
			},
			Classify: func(error) Decision {
				return Decision{Retry: true, RefreshAuth: true}
			},
			RefreshAuth: func(context.Context) error {
				refreshes++
				return nil
			},
			Sleep: func(_ context.Context, delay time.Duration) error {
				waits = append(waits, delay)
				return nil
			},
		},
	)
	if err != nil || value != "ready" {
		t.Fatalf("Run = value=%q report=%+v err=%v", value, report, err)
	}
	if attempts != 3 || report.Attempts != 3 || refreshes != 1 {
		t.Fatalf("attempt accounting = attempts=%d report=%+v refreshes=%d", attempts, report, refreshes)
	}
	if !reflect.DeepEqual(waits, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatalf("waits = %v", waits)
	}
}

func TestRunStopsOnRefreshFailure(t *testing.T) {
	attempts := 0
	_, report, err := Run(
		context.Background(),
		DefaultPolicy(),
		Hooks[string]{
			Attempt: func(context.Context, int) (string, error) {
				attempts++
				return "", errors.New("auth")
			},
			Classify: func(error) Decision {
				return Decision{Retry: true, RefreshAuth: true}
			},
			RefreshAuth: func(context.Context) error {
				return errors.New("refresh failed")
			},
			OnRefreshFailure: func(err error) error {
				return errors.New("wrapped: " + err.Error())
			},
			Sleep: func(context.Context, time.Duration) error { return nil },
		},
	)
	if err == nil || err.Error() != "wrapped: refresh failed" || attempts != 1 || report.AuthRefreshes != 1 {
		t.Fatalf("refresh failure = attempts=%d report=%+v err=%v", attempts, report, err)
	}
}
