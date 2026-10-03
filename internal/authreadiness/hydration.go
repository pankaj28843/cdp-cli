package authreadiness

import (
	"context"
	"fmt"
	"time"
)

// WaitForHydration gives a fresh application an uninterrupted observation window
// before the existing reload recovery sequence. The parent deadline bounds both.
func WaitForHydration(ctx context.Context, reloader Reloader, attempts int, window, interval time.Duration, observe func(context.Context) (bool, error)) (Result, error) {
	if reloader == nil || attempts < MinimumAttempts || window <= 0 || interval <= 0 || observe == nil {
		return Result{}, fmt.Errorf("invalid hydration readiness configuration")
	}
	initial := Result{Attempt: 1, Stage: StageInitialLoad}
	initialCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	for initialCtx.Err() == nil {
		ready, err := observe(initialCtx)
		if initialCtx.Err() != nil {
			break
		}
		initial.LastObservationError = err
		if err == nil {
			initial.SuccessfulObservations++
			initial.StageObservations++
		}
		if ready && err == nil {
			initial.Observed = true
			return initial, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-initialCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if ctx.Err() != nil {
		return initial, ctx.Err()
	}
	recovered, err := WaitForEvidence(ctx, reloader, attempts, window, interval, observe)
	recovered.SuccessfulObservations += initial.SuccessfulObservations
	return recovered, err
}
