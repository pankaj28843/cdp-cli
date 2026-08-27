package webagent

import (
	"context"
	"testing"
	"time"
)

func TestDurablePersistenceContextOutlivesOperationCancellation(t *testing.T) {
	type contextKey string
	operation := context.WithValue(context.Background(), contextKey("request"), "refresh-1")
	operation, cancelOperation := context.WithCancel(operation)
	cancelOperation()

	persistence, cancelPersistence := DurablePersistenceContext(operation)
	defer cancelPersistence()
	if err := persistence.Err(); err != nil {
		t.Fatalf("persistence context inherited cancellation: %v", err)
	}
	if got := persistence.Value(contextKey("request")); got != "refresh-1" {
		t.Fatalf("persistence context value = %v, want refresh-1", got)
	}
	deadline, ok := persistence.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
		t.Fatalf("persistence deadline = %s, want a live bound within five seconds", deadline)
	}
}

func TestFractionalDeadlineReservesTail(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	deadline := now.Add(10 * time.Minute)
	got := FractionalDeadline(now, deadline, 0.85)
	want := now.Add(8*time.Minute + 30*time.Second)
	if !got.Equal(want) {
		t.Fatalf("FractionalDeadline = %s, want %s", got, want)
	}
	if reserve := deadline.Sub(got); reserve != 90*time.Second {
		t.Fatalf("reserved tail = %s, want 1m30s", reserve)
	}
}

func TestFractionalDeadlineClampsBounds(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Minute)
	for _, test := range []struct {
		name     string
		deadline time.Time
		fraction float64
		want     time.Time
	}{
		{
			name:     "nonpositive fraction",
			deadline: deadline, fraction: 0, want: now,
		},
		{
			name:     "full fraction",
			deadline: deadline, fraction: 1, want: deadline,
		},
		{
			name:     "expired",
			deadline: now.Add(-time.Second), fraction: 0.5, want: now,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := FractionalDeadline(
				now,
				test.deadline,
				test.fraction,
			)
			if !got.Equal(test.want) {
				t.Fatalf(
					"FractionalDeadline = %s, want %s",
					got,
					test.want,
				)
			}
		})
	}
}
