package cli

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestDaemonMaintenanceFlowRunsOrderedPhases(t *testing.T) {
	ctx := context.Background()
	clock := newMaintenanceTestClock()
	var calls []string
	var wrote bool
	report := testDaemonMaintenanceReport()
	ops := testDaemonMaintenanceOps(&calls, clock)
	ops.WriteArtifact = func(ctx context.Context, path string, value any) error {
		calls = append(calls, "write")
		wrote = true
		if path != "/tmp/cdp-maintenance/latest.json" {
			t.Fatalf("summary path = %q, want fixture path", path)
		}
		return nil
	}

	got, err := runDaemonMaintenanceFlow(ctx, report, daemonMaintenanceOptions{HealthCheck: true, Cleanup: true}, ops)
	if err != nil {
		t.Fatalf("runDaemonMaintenanceFlow returned error: %v", err)
	}
	if !got.OK || got.State != "healthy" || got.Status != "pass" || got.Action != "maintained" {
		t.Fatalf("maintenance report = %+v, want healthy pass", got)
	}
	wantCalls := []string{"acquire", "daemon_health", "sweep", "resource", "seed", "daemon_health", "keepalive", "health_check", "page_cleanup", "write", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %+v, want %+v", calls, wantCalls)
	}
	if !wrote {
		t.Fatalf("WriteArtifact was not called")
	}
	for _, phase := range got.Phases {
		if phase.Status != "passed" {
			t.Fatalf("phase %s status = %q, want passed; phases=%+v", phase.Name, phase.Status, got.Phases)
		}
	}
}

func TestDaemonMaintenanceFlowResourceBlockedSkipsHeavyWork(t *testing.T) {
	ctx := context.Background()
	clock := newMaintenanceTestClock()
	var calls []string
	report := testDaemonMaintenanceReport()
	ops := testDaemonMaintenanceOps(&calls, clock)
	ops.ResourcePreflight = func(ctx context.Context, status daemon.Status, health map[string]any, sweep *browser.ManagedProcessReconcileResult) resourcePreflightResult {
		calls = append(calls, "resource")
		return resourcePreflightResult{
			Checked:          true,
			BrowserMode:      "headless",
			State:            "blocked",
			Status:           "skip",
			HeavyWorkAllowed: false,
			Reasons:          []string{"memory_below_minimum"},
			NextCommands:     []string{"cdp browser preflight --json"},
		}
	}

	got, err := runDaemonMaintenanceFlow(ctx, report, daemonMaintenanceOptions{HealthCheck: true, Cleanup: true}, ops)
	if err != nil {
		t.Fatalf("runDaemonMaintenanceFlow returned error: %v", err)
	}
	if !got.OK || got.State != "resource_blocked" || got.Status != "skipped" || got.Action != "skipped" {
		t.Fatalf("maintenance report = %+v, want resource-blocked skip", got)
	}
	wantCalls := []string{"acquire", "daemon_health", "sweep", "resource", "write", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %+v, want %+v", calls, wantCalls)
	}
	for _, name := range []string{"profile_seed", "daemon_keepalive", "daemon_health_check", "page_cleanup"} {
		phase := maintenanceTestPhase(t, got, name)
		if phase.Status != "skipped" || phase.Error != "resource_preflight_blocked" {
			t.Fatalf("phase %s = %+v, want resource-preflight skip", name, phase)
		}
	}
}

func TestDaemonMaintenanceFlowSweepFailureBlocksLaunchWork(t *testing.T) {
	ctx := context.Background()
	clock := newMaintenanceTestClock()
	var calls []string
	report := testDaemonMaintenanceReport()
	ops := testDaemonMaintenanceOps(&calls, clock)
	ops.ManagedProcessSweep = func(ctx context.Context, lock daemon.LockHandle, status daemon.Status) (browser.ManagedProcessReconcileResult, error) {
		calls = append(calls, "sweep")
		return browser.ManagedProcessReconcileResult{
			Checked:      true,
			State:        "over_budget",
			BrowserMode:  "headless",
			LiveCount:    2,
			NextCommands: []string{"cdp --browser-mode headless daemon keepalive --managed-process-sweep --repair --force --json"},
		}, nil
	}

	got, err := runDaemonMaintenanceFlow(ctx, report, daemonMaintenanceOptions{HealthCheck: true, Cleanup: true}, ops)
	if err == nil {
		t.Fatalf("runDaemonMaintenanceFlow returned nil error, want degraded sweep error")
	}
	if got.OK || got.State != "managed_process_sweep_failed" || got.Status != "fail" {
		t.Fatalf("maintenance report = %+v, want failed sweep report", got)
	}
	wantCalls := []string{"acquire", "daemon_health", "sweep", "write", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %+v, want %+v", calls, wantCalls)
	}
	phase := maintenanceTestPhase(t, got, "managed_process_sweep")
	if phase.Status != "failed" || phase.Error == "" {
		t.Fatalf("managed_process_sweep phase = %+v, want failed phase with error", phase)
	}
}

func TestDaemonMaintenanceFlowRepairDisabledDoesNotClaimHealthy(t *testing.T) {
	ctx := context.Background()
	clock := newMaintenanceTestClock()
	var calls []string
	report := testDaemonMaintenanceReport()
	ops := testDaemonMaintenanceOps(&calls, clock)
	ops.Keepalive = func(ctx context.Context, lock daemon.LockHandle, status daemon.Status, health map[string]any, sweep *browser.ManagedProcessReconcileResult, resourcePreflight resourcePreflightResult) (string, map[string]any, error) {
		calls = append(calls, "keepalive")
		return "keepalive\tskipped", map[string]any{
			"ok":            true,
			"state":         "unhealthy",
			"status":        "skipped",
			"action":        "skipped",
			"next_commands": []string{"cdp --browser-mode headless daemon maintenance --repair --json"},
		}, nil
	}
	ops.HealthCheck = func(ctx context.Context) (string, map[string]any, error) {
		t.Fatalf("health-check must not run when disabled after skipped keepalive")
		return "", nil, nil
	}
	ops.PageCleanup = func(ctx context.Context) (string, map[string]any, error) {
		t.Fatalf("page cleanup must not run against an unhealthy daemon")
		return "", nil, nil
	}

	got, err := runDaemonMaintenanceFlow(ctx, report, daemonMaintenanceOptions{Repair: false, HealthCheck: false, Cleanup: true}, ops)
	if err != nil {
		t.Fatalf("runDaemonMaintenanceFlow returned error: %v", err)
	}
	if !got.OK || got.State != "daemon_unhealthy" || got.Status != "warn" || got.Action != "skipped" {
		t.Fatalf("maintenance report = %+v, want daemon_unhealthy warning", got)
	}
	keepalive := maintenanceTestPhase(t, got, "daemon_keepalive")
	if keepalive.Status != "skipped" || keepalive.Error != "repair_skipped" {
		t.Fatalf("daemon_keepalive phase = %+v, want repair_skipped", keepalive)
	}
	cleanup := maintenanceTestPhase(t, got, "page_cleanup")
	if cleanup.Status != "skipped" || cleanup.Error != "daemon_unhealthy" {
		t.Fatalf("page_cleanup phase = %+v, want daemon_unhealthy skip", cleanup)
	}
}

func testDaemonMaintenanceReport() daemonMaintenanceReport {
	opts := daemonMaintenanceResolvedOptions{
		Repair:                        true,
		Force:                         true,
		Reconnect:                     "30s",
		ProfileSeedStrategy:           browser.ProfileSeedStrategyManaged,
		ProfileSeedIfOlderThan:        "6h",
		ProfileSeedIfOlderThanSeconds: int64((6 * time.Hour).Seconds()),
		HealthCheck:                   true,
		HealthURL:                     defaultHeadlessHealthCheckURL,
		Cleanup:                       true,
		CleanupClose:                  true,
		CleanupIdleFor:                "30m",
		CleanupIdleForSeconds:         int64((30 * time.Minute).Seconds()),
		CleanupMax:                    25,
		CleanupMaxAttempts:            3,
		CleanupConcurrency:            4,
		LockTimeout:                   "0s",
		StaleLockAfter:                "10m",
	}
	return daemonMaintenanceReport{
		OK:            true,
		SchemaVersion: daemonMaintenanceSchemaVersion,
		BrowserMode:   "headless",
		State:         "planned",
		Status:        "dry_run",
		Action:        "planned",
		Options:       opts,
		Phases:        daemonMaintenancePhases(opts, "/tmp/cdp-maintenance/latest.json"),
		Artifacts:     map[string]string{"summary": "/tmp/cdp-maintenance/latest.json"},
		NextCommands:  []string{"cdp --browser-mode headless daemon maintenance --json"},
	}
}

func testDaemonMaintenanceOps(calls *[]string, clock *maintenanceTestClock) daemonMaintenanceOperations {
	return daemonMaintenanceOperations{
		Now: clock.Now,
		AcquireLock: func(ctx context.Context) (daemon.LockHandle, bool, daemon.LockMetadata, error) {
			*calls = append(*calls, "acquire")
			return daemon.LockHandle{Metadata: daemon.LockMetadata{Name: "daemon-maintenance-headless"}}, true, daemon.LockMetadata{}, nil
		},
		ReleaseLock: func(lock daemon.LockHandle) error {
			*calls = append(*calls, "release")
			return nil
		},
		DaemonHealth: func(ctx context.Context) (daemon.Status, map[string]any, error) {
			*calls = append(*calls, "daemon_health")
			return daemon.Status{BrowserMode: "headless", ConnectionMode: "browser_url", State: "running"}, map[string]any{"state": "healthy", "usable": true}, nil
		},
		ManagedProcessSweep: func(ctx context.Context, lock daemon.LockHandle, status daemon.Status) (browser.ManagedProcessReconcileResult, error) {
			*calls = append(*calls, "sweep")
			return browser.ManagedProcessReconcileResult{Checked: true, State: "healthy", BrowserMode: "headless", LiveCount: 1}, nil
		},
		ResourcePreflight: func(ctx context.Context, status daemon.Status, health map[string]any, sweep *browser.ManagedProcessReconcileResult) resourcePreflightResult {
			*calls = append(*calls, "resource")
			return resourcePreflightResult{Checked: true, BrowserMode: "headless", State: "sufficient", Status: "pass", HeavyWorkAllowed: true}
		},
		ProfileSeed: func(ctx context.Context) (string, browserProfileStatus, error) {
			*calls = append(*calls, "seed")
			return "browser profile skipped", browserProfileStatus{OK: true, SeedAction: "skipped", NextCommands: []string{"cdp --browser-mode headless browser profile status --json"}}, nil
		},
		Keepalive: func(ctx context.Context, lock daemon.LockHandle, status daemon.Status, health map[string]any, sweep *browser.ManagedProcessReconcileResult, resourcePreflight resourcePreflightResult) (string, map[string]any, error) {
			*calls = append(*calls, "keepalive")
			return "keepalive\thealthy", map[string]any{"ok": true, "state": "healthy", "action": "none"}, nil
		},
		HealthCheck: func(ctx context.Context) (string, map[string]any, error) {
			*calls = append(*calls, "health_check")
			return "headless-health-check\thealthy", map[string]any{"ok": true, "state": "healthy", "status": "pass"}, nil
		},
		PageCleanup: func(ctx context.Context) (string, map[string]any, error) {
			*calls = append(*calls, "page_cleanup")
			return "", map[string]any{"ok": true, "cleanup": map[string]any{"closed_count": 0}}, nil
		},
		WriteArtifact: func(ctx context.Context, path string, value any) error {
			*calls = append(*calls, "write")
			if path == "" {
				return errors.New("missing path")
			}
			return nil
		},
	}
}

type maintenanceTestClock struct {
	next time.Time
}

func newMaintenanceTestClock() *maintenanceTestClock {
	return &maintenanceTestClock{next: time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)}
}

func (c *maintenanceTestClock) Now() time.Time {
	c.next = c.next.Add(time.Second)
	return c.next
}

func maintenanceTestPhase(t *testing.T, report daemonMaintenanceReport, name string) daemonMaintenancePhase {
	t.Helper()
	for _, phase := range report.Phases {
		if phase.Name == name {
			return phase
		}
	}
	t.Fatalf("phase %q not found in %+v", name, report.Phases)
	return daemonMaintenancePhase{}
}
