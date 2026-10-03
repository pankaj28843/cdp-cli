package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestSnapshotEvaluationErrorClassification(t *testing.T) {
	for _, test := range []struct {
		name    string
		cause   error
		timeout bool
	}{
		{"deadline", fmt.Errorf("evaluate: %w", context.DeadlineExceeded), true},
		{"cancelled", fmt.Errorf("evaluate: %w", context.Canceled), true},
		{"transport", errors.New("connection closed"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := snapshotEvaluationError("synthetic-page", test.cause)
			var got *CommandError
			if !errors.As(err, &got) {
				t.Fatal(err)
			}
			code, class, exit := "connection_failed", "connection", ExitConnection
			if test.timeout {
				code, class, exit = "timeout", "timeout", ExitTimeout
			}
			if got.Code != code || got.Class != class || got.ExitCode != exit {
				t.Fatalf("classification=%+v want %s/%s/%d", got, code, class, exit)
			}
			if !errors.Is(err, test.cause) {
				t.Fatal("evaluation cause was lost")
			}
		})
	}
}
