package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestPlainErrorRendersEffectiveBoundedSafeNextSteps(t *testing.T) {
	t.Run("effective permission policy", func(t *testing.T) {
		var stderr bytes.Buffer
		a := &app{err: &stderr, opts: options{autoConnect: true}}
		err := commandError(
			"permission_pending",
			"permission",
			"browser approval is pending",
			ExitPermission,
			[]string{"stale remediation"},
		)
		if renderErr := a.renderError(context.Background(), err); renderErr != nil {
			t.Fatalf("render plain error: %v", renderErr)
		}
		got := stderr.String()
		if !strings.HasPrefix(got, "browser approval is pending\nNext steps:\n") {
			t.Fatalf("plain error = %q, want stable message followed by next steps", got)
		}
		for _, step := range permissionRemediationCommands() {
			if !strings.Contains(got, "  "+step+"\n") {
				t.Fatalf("plain error = %q, want effective step %q", got, step)
			}
		}
		if strings.Contains(got, "stale remediation") {
			t.Fatalf("plain error = %q, must use effective envelope remediation", got)
		}
	})

	t.Run("bounds and terminal safety", func(t *testing.T) {
		steps := []string{
			"",
			"multiline\nstep",
			"terminal\x1b[31mstep",
			strings.Repeat("x", 1025),
		}
		for i := 1; i <= 12; i++ {
			steps = append(steps, fmt.Sprintf("cdp recovery-%02d --json", i))
		}
		var stderr bytes.Buffer
		a := &app{err: &stderr}
		if err := a.renderError(context.Background(), commandError("failed", "check_failed", "failed safely", ExitCheckFailed, steps)); err != nil {
			t.Fatalf("render plain error: %v", err)
		}
		got := stderr.String()
		if strings.Count(got, "cdp recovery-") != 10 || !strings.Contains(got, "cdp recovery-10 --json") || strings.Contains(got, "cdp recovery-11 --json") {
			t.Fatalf("plain error = %q, want first ten safe steps only", got)
		}
		if strings.Contains(got, "multiline") || strings.Contains(got, "terminal") || strings.Contains(got, strings.Repeat("x", 40)) {
			t.Fatalf("plain error emitted unsafe or oversized guidance: %q", got)
		}
	})
}
