package cli

import (
	"context"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/availability"
)

// Kept as a seam so command tests can prove that a blocked environment never
// reaches Chrome launch, window activation, or approval-drain code.
var autoHealEnvironmentCheck = availability.Check

func (a *app) checkAutoHealEnvironment(ctx context.Context, stateDir string) (availability.Result, error) {
	return autoHealEnvironmentCheck(ctx, availability.Options{StateDir: stateDir})
}

func (a *app) checkAndAcquireAutoHealEnvironment(ctx context.Context, stateDir string) (availability.Result, func() error, error) {
	if autoHealLeaseAlreadyHeld(ctx) {
		return availability.Result{
			Allowed:   true,
			State:     "ready",
			Network:   "not_checked",
			Reason:    "repair_lease_held",
			CheckedAt: time.Now().UTC().Format(time.RFC3339),
		}, nil, nil
	}
	environment, err := a.checkAutoHealEnvironment(ctx, stateDir)
	if err != nil || !environment.Allowed {
		return environment, nil, err
	}
	lease, acquired, err := availability.TryAcquireRepairLock(ctx, stateDir)
	if err != nil {
		return autoHealEnvironmentFailure(err), nil, err
	}
	if !acquired {
		environment.Allowed = false
		environment.State = "cooldown"
		environment.Reason = "repair_in_progress"
		environment.RetryAfterSeconds = 1
		return environment, nil, nil
	}
	return environment, lease.Release, nil
}

type autoHealLeaseContextKey struct{}

func withAutoHealLease(ctx context.Context) context.Context {
	return context.WithValue(ctx, autoHealLeaseContextKey{}, true)
}

func autoHealLeaseAlreadyHeld(ctx context.Context) bool {
	held, _ := ctx.Value(autoHealLeaseContextKey{}).(bool)
	return held
}

func autoHealEnvironmentFailure(_ error) availability.Result {
	return availability.Result{
		Allowed:   false,
		State:     "unknown",
		Network:   "unknown",
		Reason:    "availability_check_failed",
		CheckedAt: "",
	}
}

func autoHealEnvironmentNextCommands(browserMode string) []string {
	if browserMode == "headless" {
		return []string{
			"cdp --browser-mode headless daemon status --json",
			"cdp --browser-mode headless daemon maintenance --dry-run --json",
			"cdp cron status --json",
		}
	}
	return []string{
		"cdp --browser-mode headed daemon status --json",
		"cdp --browser-mode headed daemon keepalive --probe passive --json",
		"cdp cron status --json",
	}
}
