package availability

import (
	"context"
	"testing"
	"time"
)

func TestDesktopAvailability(t *testing.T) {
	checkedAt := time.Date(2026, time.September, 22, 12, 34, 56, 0, time.UTC)
	for _, tt := range []struct {
		name    string
		state   desktopState
		result  string
		reason  string
		allowed bool
	}{
		{"unlocked", desktopState{true, true, true, true}, "ready", "desktop_ready", true},
		{"locked", desktopState{true, true, false, true}, "unavailable", "screen_locked", false},
		{"closed lid", desktopState{true, true, true, false}, "unavailable", "lid_closed", false},
		{"inactive session", desktopState{true, false, true, true}, "unavailable", "desktop_session_inactive", false},
		{"unknown", desktopState{}, "unknown", "desktop_state_unknown", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := desktopResult(tt.state)
			if result.Allowed != tt.allowed || result.State != tt.result || result.Reason != tt.reason {
				t.Fatalf("result=%+v", result)
			}
			calls := 0
			opts := Options{StateDir: t.TempDir(), RequireDesktop: true, DesktopProbe: func(context.Context) Result { return result }, Now: func() time.Time { return checkedAt }, InternetProbe: func(context.Context) ProbeResult { calls++; return ProbeResult{Online: true} }}
			got, err := Check(context.Background(), opts)
			if err != nil || got.Allowed != tt.allowed {
				t.Fatalf("Check=%+v, %v", got, err)
			}
			if got.State != tt.result || got.CheckedAt != checkedAt.Format(time.RFC3339) {
				t.Fatalf("Check=%+v, want state=%q checked_at=%q", got, tt.result, checkedAt.Format(time.RFC3339))
			}
			if !tt.allowed && calls != 0 {
				t.Fatal("unavailable desktop reached internet probe")
			}
			// A later unlocked observation can recover, without poisoning shared
			// headless availability or requiring a process restart.
			opts.DesktopProbe = func(context.Context) Result { return desktopResult(desktopState{true, true, true, true}) }
			got, err = Check(context.Background(), opts)
			if err != nil || !got.Allowed {
				t.Fatalf("recovery=%+v, %v", got, err)
			}
		})
	}
}

func TestHeadlessAvailabilityDoesNotProbeDesktop(t *testing.T) {
	result, err := Check(context.Background(), Options{StateDir: t.TempDir(), DesktopProbe: func(context.Context) Result { t.Fatal("headless checked desktop"); return Result{} }, InternetProbe: func(context.Context) ProbeResult { return ProbeResult{Online: true} }})
	if err != nil || !result.Allowed {
		t.Fatalf("Check=%+v, %v", result, err)
	}
}
