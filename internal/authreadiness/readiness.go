package authreadiness

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrObservationIncomplete reports that the final readiness stage did not
	// complete even one valid observation inside its time window.
	ErrObservationIncomplete = errors.New(
		"auth readiness stage completed without a valid observation",
	)
	errStageWindowExhausted = errors.New(
		"auth readiness stage observation window exhausted",
	)
)

// Stage names the browser-readiness action taken before an auth observation.
type Stage string

const (
	// MinimumAttempts is the smallest sequence that can include initial load,
	// ordinary reload, and cache-bypassing hard reload as distinct stages.
	MinimumAttempts        = 3
	StageInitialLoad Stage = "initial_load"
	StageReload      Stage = "reload"
	StageHardReload  Stage = "hard_reload"
)

// Reloader is the narrow browser boundary needed by the readiness policy.
type Reloader interface {
	Reload(context.Context, bool) error
}

// Result records the final stage of a bounded readiness observation.
type Result struct {
	Attempt                int
	Stage                  Stage
	Observed               bool
	SuccessfulObservations int
	StageObservations      int
	LastObservationError   error
}

// ObservationFailed reports an inconclusive browser-observation boundary.
func (r Result) ObservationFailed() bool {
	return !r.Observed &&
		(r.LastObservationError != nil || r.StageObservations == 0)
}

// EvidenceAbsent reports reliable absence after the complete readiness policy.
func (r Result) EvidenceAbsent() bool {
	return !r.Observed &&
		r.Attempt >= MinimumAttempts &&
		r.Stage == StageHardReload &&
		r.SuccessfulObservations > 0 &&
		r.StageObservations > 0 &&
		r.LastObservationError == nil
}

// SubObservationContext bounds one blocking observation strictly inside its
// readiness stage so a no-evidence result arrives before the stage expires.
func SubObservationContext(
	ctx context.Context,
	maximum time.Duration,
) (context.Context, context.CancelFunc, error) {
	if maximum <= 0 {
		return nil, nil, fmt.Errorf(
			"auth readiness observation maximum must be positive",
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	wait := maximum
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, nil, errStageWindowExhausted
		}
		wait = remaining / 2
		if wait > maximum {
			wait = maximum
		}
		if wait <= 0 {
			return nil, nil, errStageWindowExhausted
		}
	}
	child, cancel := context.WithTimeout(ctx, wait)
	return child, cancel, nil
}

// PrepareAttempt maps one position in a caller-defined readiness sequence.
// WaitForEvidence enforces the complete three-stage auth contract.
func PrepareAttempt(
	ctx context.Context,
	reloader Reloader,
	attempt int,
	total int,
) (Stage, error) {
	if reloader == nil {
		return "", fmt.Errorf("auth readiness reloader is required")
	}
	if total < 1 {
		return "", fmt.Errorf("auth readiness total attempts must be positive")
	}
	if attempt < 1 || attempt > total {
		return "", fmt.Errorf(
			"auth readiness attempt %d is outside 1..%d",
			attempt,
			total,
		)
	}
	if attempt == 1 {
		return StageInitialLoad, nil
	}
	if attempt == total {
		return StageHardReload, reloader.Reload(ctx, true)
	}
	return StageReload, reloader.Reload(ctx, false)
}

// WaitForEvidence divides one total wait budget across initial load, ordinary
// reload, and cache-bypassing hard reload. The final stage remains observable
// for its share of the budget before reliable absence can be reported.
func WaitForEvidence(
	ctx context.Context,
	reloader Reloader,
	attempts int,
	totalWait time.Duration,
	interval time.Duration,
	observe func(context.Context) (bool, error),
) (Result, error) {
	if attempts < MinimumAttempts {
		return Result{}, fmt.Errorf(
			"auth readiness requires at least %d attempts",
			MinimumAttempts,
		)
	}
	if totalWait <= 0 {
		return Result{}, fmt.Errorf("auth readiness total wait must be positive")
	}
	if interval <= 0 {
		return Result{}, fmt.Errorf("auth readiness interval must be positive")
	}
	if observe == nil {
		return Result{}, fmt.Errorf("auth readiness observer is required")
	}

	result := Result{}
	deadline := time.Now().Add(totalWait)
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return result, fmt.Errorf(
				"auth readiness total wait exhausted before attempt %d",
				attempt,
			)
		}
		attemptsLeft := attempts - attempt + 1
		stageWait := remaining
		if attemptsLeft > 1 {
			// Reserve an extra share for the hard-reload stage so normal
			// scheduling overhead cannot consume its final grace window.
			stageWait = remaining / time.Duration(attemptsLeft+1)
		}
		if stageWait <= 0 {
			return result, fmt.Errorf(
				"auth readiness stage budget exhausted before attempt %d",
				attempt,
			)
		}
		stageCtx, cancelStage := context.WithTimeout(ctx, stageWait)
		stage, err := PrepareAttempt(
			stageCtx,
			reloader,
			attempt,
			attempts,
		)
		result.Attempt = attempt
		result.Stage = stage
		result.StageObservations = 0
		result.LastObservationError = nil
		if err != nil {
			cancelStage()
			return result, err
		}

		for {
			if stageCtx.Err() != nil {
				break
			}
			ready, observationErr := observe(stageCtx)
			if stageCtx.Err() != nil {
				break
			}
			if errors.Is(observationErr, errStageWindowExhausted) {
				break
			}
			if observationErr != nil {
				result.LastObservationError = observationErr
			} else {
				result.SuccessfulObservations++
				result.StageObservations++
				result.LastObservationError = nil
			}
			if ready {
				result.Observed = true
				cancelStage()
				return result, nil
			}

			delay := interval
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				cancelStage()
				return result, ctx.Err()
			case <-stageCtx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
		cancelStage()
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if result.StageObservations == 0 &&
			result.LastObservationError == nil {
			result.LastObservationError = ErrObservationIncomplete
		}
	}
	return result, nil
}
