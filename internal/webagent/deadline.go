package webagent

import (
	"context"
	"time"
)

const durablePersistenceTimeout = 5 * time.Second

// DurablePersistenceContext gives terminal state a short atomic-write budget
// after a completed external observation, even when its operation deadline
// expires at that boundary.
func DurablePersistenceContext(operation context.Context) (context.Context, context.CancelFunc) {
	if operation == nil {
		operation = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(operation), durablePersistenceTimeout)
}

// FractionalDeadline reserves the tail of an existing deadline for a distinct
// reconciliation phase. Fractions outside (0, 1) clamp to the nearest bound.
func FractionalDeadline(
	now time.Time,
	deadline time.Time,
	fraction float64,
) time.Time {
	if !deadline.After(now) || fraction <= 0 {
		return now
	}
	if fraction >= 1 {
		return deadline
	}
	return now.Add(
		time.Duration(float64(deadline.Sub(now)) * fraction),
	)
}
