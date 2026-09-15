package browser_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
)

func TestStopManagedChromeObservationTimeoutIsNotShutdownProof(t *testing.T) {
	for _, mode := range []string{"first_process_observation", "second_process_observation", "endpoint_observation"} {
		t.Run(mode, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			if err := browser.SaveManagedMetadata(stateDir, browser.ManagedMetadata{
				BrowserMode: "headless", ChromePID: 123,
				StartedAt: "2026-05-21T12:00:00Z", ProcessStartTime: "2026-05-21T12:00:00Z",
				UserDataDir: browser.ManagedProfileDir(stateDir), DebuggingPort: "9222",
				ProfileSeedStrategy: browser.ProfileSeedStrategyManaged, OwnedMarker: "synthetic-owned-marker",
			}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			result, err := browser.StopManagedChrome(context.Background(), stateDir, browser.ManagedStopOptions{
				Signal: func(int) error { return nil },
				ProcessLister: func(ctx context.Context, _ string) ([]int, error) {
					calls++
					if mode == "endpoint_observation" {
						return nil, nil
					}
					if mode == "second_process_observation" && calls == 1 {
						return []int{123, 456}, nil
					}
					<-ctx.Done()
					return nil, ctx.Err()
				},
				EndpointReachable: func(ctx context.Context, _ string) bool {
					if mode == "endpoint_observation" {
						<-ctx.Done()
					}
					return false
				},
				VerificationTimeout:      20 * time.Millisecond,
				VerificationPollInterval: time.Second,
			})
			if result.Stopped || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unobserved shutdown treated as proof: stopped=%v err=%v result=%+v", result.Stopped, err, result)
			}
			wantPIDs := []int{123}
			if mode == "second_process_observation" {
				wantPIDs = []int{123, 456}
			}
			if !reflect.DeepEqual(result.RemainingPIDs, wantPIDs) {
				t.Fatalf("last known PIDs = %v, want %v", result.RemainingPIDs, wantPIDs)
			}
			for _, check := range result.SafetyChecks {
				if check == "shutdown_process_tree_verified" {
					t.Fatal("unobserved shutdown marked verified")
				}
			}
		})
	}
}
