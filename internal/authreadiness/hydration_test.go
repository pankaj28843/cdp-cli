package authreadiness

import (
	"context"
	"testing"
	"time"
)

func TestWaitForHydrationDoesNotReloadSlowInitialApplication(t *testing.T) {
	r := &recordingReloader{}
	started := time.Now()
	result, err := WaitForHydration(context.Background(), r, MinimumAttempts, 100*time.Millisecond, time.Millisecond, func(context.Context) (bool, error) {
		return time.Since(started) >= 60*time.Millisecond, nil
	})
	if err != nil || !result.Observed || result.Stage != StageInitialLoad || len(r.ignoreCache) != 0 {
		t.Fatalf("result=%+v err=%v reloads=%v", result, err, r.ignoreCache)
	}
}

func TestWaitForHydrationRetainsBoundedRecovery(t *testing.T) {
	r := &recordingReloader{}
	result, err := WaitForHydration(context.Background(), r, MinimumAttempts, 100*time.Millisecond, time.Millisecond, func(context.Context) (bool, error) {
		return len(r.ignoreCache) == 2, nil
	})
	if err != nil || !result.Observed || result.Stage != StageHardReload || len(r.ignoreCache) != 2 || !r.ignoreCache[1] {
		t.Fatalf("result=%+v err=%v reloads=%v", result, err, r.ignoreCache)
	}
}
