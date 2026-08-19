// Package resilience contains the provider-neutral execution core for
// retryable webagent operations. Providers supply effects and failure
// classification; this package owns the bounded attempt plan, cancellation,
// backoff, and one-shot auth-repair hook.
package resilience

import (
	"context"
	"time"
)

const MaxAttempts = 3

type Policy struct {
	MaxAttempts int
	Backoff     []time.Duration
}

func DefaultPolicy() Policy {
	return Policy{
		MaxAttempts: MaxAttempts,
		Backoff:     []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
	}
}

// Normalized is pure: it copies caller-owned slices and applies the shared
// safety ceiling without performing any work or sleeping.
func (p Policy) Normalized() Policy {
	defaults := DefaultPolicy()
	maxAttempts := p.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = defaults.MaxAttempts
	}
	if maxAttempts > MaxAttempts {
		maxAttempts = MaxAttempts
	}
	backoff := append([]time.Duration(nil), p.Backoff...)
	if len(backoff) == 0 {
		backoff = append([]time.Duration(nil), defaults.Backoff...)
	}
	for index, delay := range backoff {
		if delay < 0 {
			backoff[index] = 0
		}
	}
	return Policy{MaxAttempts: maxAttempts, Backoff: backoff}
}

// DelayBeforeAttempt returns the pure schedule value. Attempt one is
// immediate; later attempts use the corresponding exponential slot. Keeping
// the third 4-second slot in the plan makes the policy explicit even though
// the shared safety ceiling currently permits three total attempts.
func (p Policy) DelayBeforeAttempt(attempt int) time.Duration {
	normalized := p.Normalized()
	if attempt <= 1 || len(normalized.Backoff) == 0 {
		return 0
	}
	index := attempt - 2
	if index >= len(normalized.Backoff) {
		index = len(normalized.Backoff) - 1
	}
	return normalized.Backoff[index]
}

type Decision struct {
	Retry       bool
	RefreshAuth bool
}

type Hooks[T any] struct {
	Attempt          func(context.Context, int) (T, error)
	Classify         func(error) Decision
	RefreshAuth      func(context.Context) error
	OnRefreshFailure func(error) error
	Sleep            func(context.Context, time.Duration) error
}

type Report struct {
	Attempts      int
	AuthRefreshes int
	WaitDelays    []time.Duration
}

// Run is the effectful shell around the pure Policy. The provider owns the
// attempt effect and classification; this loop guarantees that every provider
// gets the same bounded retry, cancellation, and auth-refresh semantics.
func Run[T any](
	ctx context.Context,
	policy Policy,
	hooks Hooks[T],
) (T, Report, error) {
	var zero T
	if hooks.Attempt == nil {
		return zero, Report{}, context.Canceled
	}
	normalized := policy.Normalized()
	sleep := hooks.Sleep
	if sleep == nil {
		sleep = sleepWithContext
	}
	var report Report
	var lastErr error
	authRefreshUsed := false
	for attempt := 1; attempt <= normalized.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, report, err
		}
		retryDelay := normalized.DelayBeforeAttempt(attempt)
		if retryDelay > 0 {
			report.WaitDelays = append(report.WaitDelays, retryDelay)
			if err := sleep(ctx, retryDelay); err != nil {
				return zero, report, err
			}
		}
		report.Attempts = attempt
		value, err := hooks.Attempt(ctx, attempt)
		if err == nil {
			return value, report, nil
		}
		lastErr = err
		decision := Decision{}
		if hooks.Classify != nil {
			decision = hooks.Classify(err)
		}
		if !decision.Retry || attempt == normalized.MaxAttempts {
			return zero, report, lastErr
		}
		if decision.RefreshAuth && !authRefreshUsed && hooks.RefreshAuth != nil {
			authRefreshUsed = true
			report.AuthRefreshes++
			if refreshErr := hooks.RefreshAuth(ctx); refreshErr != nil {
				if hooks.OnRefreshFailure != nil {
					return zero, report, hooks.OnRefreshFailure(refreshErr)
				}
				return zero, report, refreshErr
			}
		}
	}
	return zero, report, lastErr
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
