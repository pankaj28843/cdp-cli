package webagent

import (
	"testing"
	"time"
)

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
