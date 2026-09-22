package availability

import (
	"context"
	"testing"
)

func TestDesktopAvailability(t *testing.T) {
	for _, tt := range []struct {
		name    string
		state   desktopState
		reason  string
		allowed bool
	}{
		{"unlocked", desktopState{true, true, true, true}, "desktop_ready", true},
		{"locked", desktopState{true, true, false, true}, "screen_locked", false},
		{"closed lid", desktopState{true, true, true, false}, "lid_closed", false},
		{"inactive session", desktopState{true, false, true, true}, "desktop_session_inactive", false},
		{"unknown", desktopState{}, "desktop_state_unknown", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := desktopResult(tt.state)
			if result.Allowed != tt.allowed || result.Reason != tt.reason {
				t.Fatalf("result=%+v", result)
			}
			calls := 0
			opts := Options{StateDir: t.TempDir(), RequireDesktop: true, DesktopProbe: func(context.Context) Result { return result }, InternetProbe: func(context.Context) ProbeResult { calls++; return ProbeResult{Online: true} }}
			got, err := Check(context.Background(), opts)
			if err != nil || got.Allowed != tt.allowed {
				t.Fatalf("Check=%+v, %v", got, err)
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
