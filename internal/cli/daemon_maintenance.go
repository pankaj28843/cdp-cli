package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/availability"
	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/spf13/cobra"
)

const daemonMaintenanceSchemaVersion = "cdp-headless-maintenance/v1"

type daemonMaintenanceOptions struct {
	DryRun                    bool
	Repair                    bool
	Force                     bool
	Reconnect                 time.Duration
	ChromeCommand             string
	ProfileSeedStrategy       string
	ProfileSeedIfOlderThan    time.Duration
	ProfileSeedIfOlderThanSet bool
	HealthCheck               bool
	HealthURL                 string
	Cleanup                   bool
	CleanupClose              bool
	CleanupIdleFor            time.Duration
	CleanupMax                int
	CleanupMaxAttempts        int
	CleanupConcurrency        int
	LockTimeout               time.Duration
	StaleLockAfter            time.Duration
	OutDir                    string
}

type daemonMaintenanceResolvedOptions struct {
	Repair                        bool   `json:"repair"`
	Force                         bool   `json:"force"`
	Reconnect                     string `json:"reconnect"`
	ChromeCommand                 string `json:"chrome_command,omitempty"`
	ProfileSeedStrategy           string `json:"profile_seed_strategy"`
	ProfileSeedIfOlderThan        string `json:"profile_seed_if_older_than"`
	ProfileSeedIfOlderThanSeconds int64  `json:"profile_seed_if_older_than_seconds"`
	HealthCheck                   bool   `json:"health_check"`
	HealthURL                     string `json:"health_url"`
	Cleanup                       bool   `json:"cleanup"`
	CleanupClose                  bool   `json:"cleanup_close"`
	CleanupIdleFor                string `json:"cleanup_idle_for"`
	CleanupIdleForSeconds         int64  `json:"cleanup_idle_for_seconds"`
	CleanupMax                    int    `json:"cleanup_max"`
	CleanupMaxAttempts            int    `json:"cleanup_max_attempts"`
	CleanupConcurrency            int    `json:"cleanup_concurrency"`
	LockTimeout                   string `json:"lock_timeout"`
	StaleLockAfter                string `json:"stale_lock_after"`
}

type daemonMaintenancePhase struct {
	Order         int    `json:"order"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Required      bool   `json:"required"`
	Mutates       bool   `json:"mutates"`
	HeavyWork     bool   `json:"heavy_work"`
	ResourceGated bool   `json:"resource_gated"`
	Command       string `json:"command,omitempty"`
	ArtifactKey   string `json:"artifact_key,omitempty"`
	Description   string `json:"description"`
	StartedAt     string `json:"started_at,omitempty"`
	FinishedAt    string `json:"finished_at,omitempty"`
	Error         string `json:"error,omitempty"`
	Result        any    `json:"result,omitempty"`
}

type daemonMaintenanceReport struct {
	OK            bool                             `json:"ok"`
	SchemaVersion string                           `json:"schema_version"`
	BrowserMode   string                           `json:"browser_mode"`
	State         string                           `json:"state"`
	Status        string                           `json:"status"`
	Action        string                           `json:"action"`
	DryRun        bool                             `json:"dry_run"`
	RunID         string                           `json:"run_id,omitempty"`
	StartedAt     string                           `json:"started_at,omitempty"`
	FinishedAt    string                           `json:"finished_at,omitempty"`
	Locked        bool                             `json:"locked,omitempty"`
	Lock          any                              `json:"lock,omitempty"`
	Options       daemonMaintenanceResolvedOptions `json:"options"`
	Phases        []daemonMaintenancePhase         `json:"phases"`
	Artifacts     map[string]string                `json:"artifacts"`
	NextCommands  []string                         `json:"next_commands"`
	Environment   *availability.Result             `json:"environment,omitempty"`
	Warnings      []string                         `json:"warnings,omitempty"`
}

type daemonMaintenanceOperations struct {
	Now                 func() time.Time
	AcquireLock         func(context.Context) (daemon.LockHandle, bool, daemon.LockMetadata, error)
	ReleaseLock         func(daemon.LockHandle) error
	Environment         func(context.Context) (availability.Result, func() error, error)
	DaemonHealth        func(context.Context) (daemon.Status, map[string]any, error)
	ManagedProcessSweep func(context.Context, daemon.LockHandle, daemon.Status) (browser.ManagedProcessReconcileResult, error)
	ResourcePreflight   func(context.Context, daemon.Status, map[string]any, *browser.ManagedProcessReconcileResult) resourcePreflightResult
	ProfileSeed         func(context.Context) (string, browserProfileStatus, error)
	Keepalive           func(context.Context, daemon.LockHandle, daemon.Status, map[string]any, *browser.ManagedProcessReconcileResult, resourcePreflightResult) (string, map[string]any, error)
	HealthCheck         func(context.Context) (string, map[string]any, error)
	PageCleanup         func(context.Context) (string, map[string]any, error)
	WriteArtifact       func(context.Context, string, any) error
}

func (a *app) newDaemonMaintenanceCommand() *cobra.Command {
	opts := daemonMaintenanceOptions{
		Repair:             true,
		Force:              true,
		Reconnect:          30 * time.Second,
		ChromeCommand:      defaultChromeCommand(),
		HealthCheck:        true,
		HealthURL:          defaultHeadlessHealthCheckURL,
		Cleanup:            true,
		CleanupClose:       true,
		CleanupIdleFor:     30 * time.Minute,
		CleanupMax:         25,
		CleanupMaxAttempts: defaultPageCloseMaxAttempts,
		CleanupConcurrency: defaultPageCleanupCloseConcurrency,
		StaleLockAfter:     10 * time.Minute,
	}
	cmd := &cobra.Command{
		Use:   "maintenance",
		Short: "Plan and run unattended managed headless maintenance",
		Long:  "Plan and run the canonical unattended managed headless maintenance flow used by cron. The flow is ordered as lock, managed-process sweep, resource preflight, profile seed, daemon keepalive repair, synthetic health-check, page cleanup, and summary artifact write. Live maintenance first checks internet reachability and a persisted awake observation; offline or post-wake hosts skip every browser/process mutation and write a structured summary.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Reconnect < 0 || opts.ProfileSeedIfOlderThan < 0 || opts.CleanupIdleFor < 0 || opts.CleanupMax < 0 || opts.CleanupMaxAttempts <= 0 || opts.CleanupConcurrency <= 0 || opts.LockTimeout < 0 || opts.StaleLockAfter < 0 {
				return commandError(
					"invalid_argument",
					"usage",
					"durations and --cleanup-max must be non-negative, and cleanup attempts/concurrency must be positive",
					ExitUsage,
					[]string{"cdp --browser-mode headless daemon maintenance --dry-run --json"},
				)
			}
			if a.browserModeName() != string(config.BrowserModeHeadless) {
				return commandError(
					"invalid_browser_mode",
					"usage",
					"daemon maintenance is only supported for --browser-mode headless",
					ExitUsage,
					[]string{"cdp --browser-mode headless daemon maintenance --dry-run --json"},
				)
			}
			opts.ProfileSeedIfOlderThanSet = cmd.Flags().Changed("profile-seed-if-older-than")
			ctx, cancel := a.commandContextWithDefault(cmd, 90*time.Second)
			defer cancel()
			return a.runDaemonMaintenance(ctx, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "emit the ordered maintenance plan and JSON contract without mutating browser or daemon state")
	cmd.Flags().BoolVar(&opts.Repair, "repair", opts.Repair, "run keepalive repair when the managed headless daemon is unhealthy")
	cmd.Flags().BoolVar(&opts.Force, "force", opts.Force, "allow cdp-owned managed headless cleanup when repair or profile replacement requires it")
	cmd.Flags().DurationVar(&opts.Reconnect, "reconnect", opts.Reconnect, "daemon reconnect interval used when maintenance repairs the runtime")
	cmd.Flags().StringVar(&opts.ChromeCommand, "chrome-command", opts.ChromeCommand, "Chrome command for managed headless repair; empty disables launch")
	cmd.Flags().StringVar(&opts.ProfileSeedStrategy, "profile-seed-strategy", "", "managed headless profile seed strategy for maintenance: managed or copy-default; empty uses config/default")
	cmd.Flags().DurationVar(&opts.ProfileSeedIfOlderThan, "profile-seed-if-older-than", 0, "skip matching profile seed when existing metadata is newer than this duration; empty uses config/default")
	cmd.Flags().BoolVar(&opts.HealthCheck, "health-check", opts.HealthCheck, "run synthetic managed headless daemon health-check after keepalive")
	cmd.Flags().StringVar(&opts.HealthURL, "health-url", opts.HealthURL, "synthetic URL used for health-check validation")
	cmd.Flags().BoolVar(&opts.Cleanup, "cleanup", opts.Cleanup, "evaluate cdp-created headless page cleanup after health-check")
	cmd.Flags().BoolVar(&opts.CleanupClose, "cleanup-close", opts.CleanupClose, "close matching cdp-created headless cleanup candidates")
	cmd.Flags().DurationVar(&opts.CleanupIdleFor, "cleanup-idle-for", opts.CleanupIdleFor, "minimum idle duration before cleanup can close a cdp-created page")
	cmd.Flags().IntVar(&opts.CleanupMax, "cleanup-max", opts.CleanupMax, "maximum cleanup candidates to close or report")
	cmd.Flags().IntVar(&opts.CleanupMaxAttempts, "cleanup-max-attempts", opts.CleanupMaxAttempts, "maximum close attempts per cleanup target")
	cmd.Flags().IntVar(&opts.CleanupConcurrency, "cleanup-concurrency", opts.CleanupConcurrency, "maximum cleanup targets to close concurrently")
	cmd.Flags().DurationVar(&opts.LockTimeout, "lock-timeout", opts.LockTimeout, "how long to wait for another maintenance lock; 0s skips immediately")
	cmd.Flags().DurationVar(&opts.StaleLockAfter, "stale-lock-after", opts.StaleLockAfter, "remove a maintenance lock older than this duration; 0 disables stale cleanup")
	cmd.Flags().StringVar(&opts.OutDir, "out-dir", "", "directory for maintenance JSON artifacts; defaults under the cdp state directory")
	return cmd
}

func (a *app) runDaemonMaintenance(ctx context.Context, opts daemonMaintenanceOptions) error {
	report, err := a.daemonMaintenancePlan(ctx, opts)
	if err != nil {
		return err
	}
	if opts.DryRun {
		return a.render(ctx, "headless-maintenance\tplanned", report)
	}
	ops, err := a.daemonMaintenanceOperations(ctx, opts, report)
	if err != nil {
		return err
	}
	report, err = runDaemonMaintenanceFlow(ctx, report, opts, ops)
	if err != nil {
		return err
	}
	return a.render(ctx, fmt.Sprintf("headless-maintenance\t%s", report.State), report)
}

func (a *app) daemonMaintenancePlan(ctx context.Context, opts daemonMaintenanceOptions) (daemonMaintenanceReport, error) {
	_ = ctx
	cfg, err := config.Load(a.opts.config)
	if err != nil {
		return daemonMaintenanceReport{}, commandError("invalid_config", "usage", err.Error(), ExitUsage, []string{"cdp --config <path> daemon maintenance --dry-run --json"})
	}
	store, err := a.stateStore()
	if err != nil {
		return daemonMaintenanceReport{}, err
	}
	strategy := strings.TrimSpace(opts.ProfileSeedStrategy)
	if strategy == "" {
		strategy = cfg.Browser.Headless.ProfileSeedStrategy
	}
	strategy = browser.NormalizeProfileSeedStrategy(strategy)
	if strategy == "" {
		strategy = browser.ProfileSeedStrategyManaged
	}
	if !browser.SupportedProfileSeedStrategy(strategy) {
		return daemonMaintenanceReport{}, commandError(
			"invalid_profile_seed_strategy",
			"usage",
			"--profile-seed-strategy must be managed or copy-default",
			ExitUsage,
			[]string{"cdp --browser-mode headless daemon maintenance --profile-seed-strategy managed --dry-run --json", "cdp --browser-mode headless daemon maintenance --profile-seed-strategy copy-default --dry-run --json"},
		)
	}
	seedAfter := opts.ProfileSeedIfOlderThan
	if !opts.ProfileSeedIfOlderThanSet && cfg.Browser.Headless.ProfileRefreshAfter > 0 {
		seedAfter = cfg.Browser.Headless.ProfileRefreshAfter
	}
	if !opts.ProfileSeedIfOlderThanSet && seedAfter <= 0 {
		seedAfter = 6 * time.Hour
	}
	outDir := strings.TrimSpace(opts.OutDir)
	if outDir == "" {
		outDir = filepath.Join(store.Dir, "headless-maintenance")
	}
	summaryPath := filepath.Join(outDir, "latest.json")
	resolved := daemonMaintenanceResolvedOptions{
		Repair:                        opts.Repair,
		Force:                         opts.Force,
		Reconnect:                     cronDurationLiteral(opts.Reconnect),
		ChromeCommand:                 opts.ChromeCommand,
		ProfileSeedStrategy:           strategy,
		ProfileSeedIfOlderThan:        cronDurationLiteral(seedAfter),
		ProfileSeedIfOlderThanSeconds: int64(seedAfter.Seconds()),
		HealthCheck:                   opts.HealthCheck,
		HealthURL:                     opts.HealthURL,
		Cleanup:                       opts.Cleanup,
		CleanupClose:                  opts.CleanupClose,
		CleanupIdleFor:                cronDurationLiteral(opts.CleanupIdleFor),
		CleanupIdleForSeconds:         int64(opts.CleanupIdleFor.Seconds()),
		CleanupMax:                    opts.CleanupMax,
		CleanupMaxAttempts:            opts.CleanupMaxAttempts,
		CleanupConcurrency:            opts.CleanupConcurrency,
		LockTimeout:                   cronDurationLiteral(opts.LockTimeout),
		StaleLockAfter:                cronDurationLiteral(opts.StaleLockAfter),
	}
	phases := daemonMaintenancePhases(resolved, summaryPath)
	nextCommands := []string{
		"cdp --browser-mode headless daemon maintenance --json",
		"cdp --browser-mode headless daemon maintenance --dry-run --json",
		"cdp cron install --json",
		"cdp cron status --json",
	}
	return daemonMaintenanceReport{
		OK:            true,
		SchemaVersion: daemonMaintenanceSchemaVersion,
		BrowserMode:   string(config.BrowserModeHeadless),
		State:         "planned",
		Status:        "dry_run",
		Action:        "planned",
		DryRun:        opts.DryRun,
		Options:       resolved,
		Phases:        phases,
		Artifacts: map[string]string{
			"summary": summaryPath,
		},
		NextCommands: uniqueCommands(nextCommands),
	}, nil
}

func (a *app) daemonMaintenanceOperations(ctx context.Context, opts daemonMaintenanceOptions, report daemonMaintenanceReport) (daemonMaintenanceOperations, error) {
	_ = ctx
	store, err := a.stateStore()
	if err != nil {
		return daemonMaintenanceOperations{}, err
	}
	lockName := "daemon-maintenance-headless"
	summaryPath := report.Artifacts["summary"]
	seedAfter := time.Duration(report.Options.ProfileSeedIfOlderThanSeconds) * time.Second
	healthOutDir := filepath.Join(filepath.Dir(summaryPath), "health")
	return daemonMaintenanceOperations{
		Now: time.Now,
		AcquireLock: func(ctx context.Context) (daemon.LockHandle, bool, daemon.LockMetadata, error) {
			return daemon.AcquireLock(ctx, store.Dir, lockName, opts.LockTimeout, opts.StaleLockAfter, daemon.LockMetadata{Name: lockName, Phase: "starting"})
		},
		ReleaseLock: func(lock daemon.LockHandle) error {
			return lock.Release()
		},
		Environment: func(ctx context.Context) (availability.Result, func() error, error) {
			return a.checkAndAcquireAutoHealEnvironment(ctx, store.Dir)
		},
		DaemonHealth: func(ctx context.Context) (daemon.Status, map[string]any, error) {
			return a.selectedDaemonHealth(ctx)
		},
		ManagedProcessSweep: func(ctx context.Context, lock daemon.LockHandle, status daemon.Status) (browser.ManagedProcessReconcileResult, error) {
			return a.runManagedProcessSweep(ctx, store.Dir, lock, status)
		},
		ResourcePreflight: func(ctx context.Context, status daemon.Status, health map[string]any, sweep *browser.ManagedProcessReconcileResult) resourcePreflightResult {
			return a.maintenanceResourcePreflightForStateWithManaged(ctx, store.Dir, status, health, sweep)
		},
		ProfileSeed: func(ctx context.Context) (string, browserProfileStatus, error) {
			return a.runBrowserProfileSeed(ctx, browserProfileSeedOptions{
				Strategy:    report.Options.ProfileSeedStrategy,
				IfOlderThan: seedAfter,
				Now:         time.Now().UTC(),
			})
		},
		Keepalive: func(ctx context.Context, lock daemon.LockHandle, status daemon.Status, health map[string]any, sweep *browser.ManagedProcessReconcileResult, resourcePreflight resourcePreflightResult) (string, map[string]any, error) {
			return a.runDaemonMaintenanceKeepalive(ctx, store.Dir, lock, opts, status, health, sweep, resourcePreflight)
		},
		HealthCheck: func(ctx context.Context) (string, map[string]any, error) {
			return a.runDaemonHealthCheckReport(ctx, daemonHealthCheckOptions{
				Repair:              opts.Repair,
				Force:               opts.Force,
				ManagedProcessSweep: false,
				HealthURL:           opts.HealthURL,
				OutDir:              healthOutDir,
				FailureThreshold:    3,
				LockTimeout:         opts.LockTimeout,
				StaleLockAfter:      opts.StaleLockAfter,
				Reconnect:           opts.Reconnect,
				ChromeCommand:       opts.ChromeCommand,
			})
		},
		PageCleanup: func(ctx context.Context) (string, map[string]any, error) {
			restoreHeadlessRepair := a.disableHeadlessRepair()
			defer restoreHeadlessRepair()
			return a.runPageCleanup(ctx, pageCleanupRunOptions{
				Close:            opts.CleanupClose,
				CreatedBy:        "cdp",
				Force:            opts.Force,
				WaitGone:         true,
				MaxAttempts:      opts.CleanupMaxAttempts,
				CloseConcurrency: opts.CleanupConcurrency,
				IdleFor:          opts.CleanupIdleFor,
				Max:              opts.CleanupMax,
				MaxChanged:       true,
			})
		},
		WriteArtifact: func(ctx context.Context, path string, value any) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			return writeJSONArtifact(path, value)
		},
	}, nil
}

func runDaemonMaintenanceFlow(ctx context.Context, report daemonMaintenanceReport, opts daemonMaintenanceOptions, ops daemonMaintenanceOperations) (daemonMaintenanceReport, error) {
	if ops.Now == nil {
		ops.Now = time.Now
	}
	started := ops.Now().UTC()
	report.OK = false
	report.State = "running"
	report.Status = "running"
	report.Action = "running"
	report.DryRun = false
	report.RunID = started.Format("20060102T150405Z")
	report.StartedAt = started.Format(time.RFC3339)
	report.FinishedAt = ""
	setRemainingPhaseStatus(&report, "pending")

	summaryPath := report.Artifacts["summary"]
	var lock daemon.LockHandle
	var acquired bool
	var status daemon.Status
	var health map[string]any
	var healthErr error
	var sweep *browser.ManagedProcessReconcileResult
	var resourcePreflight resourcePreflightResult
	var releaseErr error
	var autoHealLeaseHeld bool

	fail := func(phaseName, code, class, message string, exit int, next []string, result any, cause error) (daemonMaintenanceReport, error) {
		if cause != nil && message == "" {
			message = cause.Error()
		}
		phaseFail(&report, phaseName, ops.Now(), message, result)
		report.OK = false
		report.State = phaseName + "_failed"
		report.Status = "fail"
		report.Action = "failed"
		report.NextCommands = uniqueCommands(report.NextCommands, next)
		finishDaemonMaintenanceReport(&report, ops.Now())
		_ = writeMaintenanceSummary(ctx, ops, summaryPath, &report)
		if acquired && ops.ReleaseLock != nil {
			releaseErr = ops.ReleaseLock(lock)
			acquired = false
		}
		if releaseErr != nil {
			report.Warnings = appendStringReasons(report.Warnings, "release_lock_failed: "+releaseErr.Error())
		}
		if code == "" {
			code = "headless_maintenance_failed"
		}
		if class == "" {
			class = "connection"
		}
		if message == "" {
			message = "headless maintenance failed"
		}
		return report, commandErrorWithData(code, class, message, exit, uniqueCommands(next, report.NextCommands), report)
	}

	phaseStart(&report, "acquire_lock", ops.Now())
	if ops.AcquireLock == nil {
		return fail("acquire_lock", "headless_maintenance_failed", "internal", "maintenance lock operation is unavailable", ExitInternal, []string{"cdp --browser-mode headless daemon maintenance --json"}, nil, nil)
	}
	existingLock := daemon.LockMetadata{}
	var err error
	lock, acquired, existingLock, err = ops.AcquireLock(ctx)
	if err != nil {
		return fail("acquire_lock", "lock_failed", "connection", fmt.Sprintf("acquire maintenance lock: %v", err), ExitConnection, []string{"cdp --browser-mode headless daemon health --json", "cdp cron status --json"}, nil, err)
	}
	if !acquired {
		phaseSkip(&report, "acquire_lock", ops.Now(), "locked", existingLock)
		report.OK = true
		report.State = "locked"
		report.Status = "skipped"
		report.Action = "skipped"
		report.Locked = true
		report.Lock = existingLock
		skipRemainingPhases(&report, "acquire_lock", "locked")
		finishDaemonMaintenanceReport(&report, ops.Now())
		return report, nil
	}
	report.Lock = map[string]any{"name": lock.Metadata.Name, "acquired": true}
	phasePass(&report, "acquire_lock", ops.Now(), report.Lock)
	defer func() {
		if acquired && ops.ReleaseLock != nil {
			_ = ops.ReleaseLock(lock)
		}
	}()

	if ops.Environment != nil {
		environment, releaseAutoHealLease, environmentErr := ops.Environment(ctx)
		if environmentErr != nil {
			failedEnvironment := autoHealEnvironmentFailure(environmentErr)
			report.Environment = &failedEnvironment
			return fail(
				"environment_check",
				"auto_heal_environment_unavailable",
				"connection",
				"Auto Heal environment check failed; no browser repair was attempted",
				ExitConnection,
				autoHealEnvironmentNextCommands("headless"),
				failedEnvironment,
				nil,
			)
		}
		report.Environment = &environment
		if !environment.Allowed {
			report.OK = true
			report.State = "environment_unavailable"
			report.Status = "skipped"
			report.Action = "skipped"
			report.NextCommands = uniqueCommands(report.NextCommands, autoHealEnvironmentNextCommands("headless"))
			skipPhases(&report, []string{"managed_process_sweep", "resource_preflight", "profile_seed", "daemon_keepalive", "daemon_health_check", "page_cleanup"}, ops.Now(), "environment_unavailable")
			phaseStart(&report, "write_artifact", ops.Now())
			phasePass(&report, "write_artifact", ops.Now(), summaryPath)
			if err := writeMaintenanceSummary(ctx, ops, summaryPath, &report); err != nil {
				return fail("write_artifact", "artifact_write_failed", "internal", err.Error(), ExitInternal, []string{"cdp --browser-mode headless daemon maintenance --json"}, nil, err)
			}
			return report, nil
		}
		if releaseAutoHealLease != nil {
			autoHealLeaseHeld = true
			defer func() { _ = releaseAutoHealLease() }()
		}
	}

	if ops.DaemonHealth != nil {
		status, health, healthErr = ops.DaemonHealth(ctx)
	}
	if health == nil {
		health = map[string]any{}
	}
	if healthErr != nil {
		report.Warnings = appendStringReasons(report.Warnings, "initial_health_failed: "+healthErr.Error())
	}

	phaseStart(&report, "managed_process_sweep", ops.Now())
	if ops.ManagedProcessSweep == nil {
		return fail("managed_process_sweep", "headless_maintenance_failed", "internal", "managed process sweep operation is unavailable", ExitInternal, []string{"cdp --browser-mode headless daemon stop --force-managed --json"}, nil, nil)
	}
	sweepValue, err := ops.ManagedProcessSweep(ctx, lock, status)
	if err != nil {
		return fail("managed_process_sweep", "managed_process_sweep_failed", "connection", fmt.Sprintf("managed headless process sweep: %v", err), ExitConnection, sweepValue.NextCommands, sweepValue, err)
	}
	sweep = &sweepValue
	phasePass(&report, "managed_process_sweep", ops.Now(), sweepValue)
	if sweepValue.State == "over_budget" || sweepValue.State == "degraded" || sweepValue.State == "error" {
		return fail("managed_process_sweep", "managed_process_sweep_degraded", "connection", "managed headless process sweep did not reach a launch-safe state", ExitConnection, sweepValue.NextCommands, sweepValue, nil)
	}

	phaseStart(&report, "resource_preflight", ops.Now())
	if ops.ResourcePreflight == nil {
		return fail("resource_preflight", "headless_maintenance_failed", "internal", "resource preflight operation is unavailable", ExitInternal, []string{"cdp browser preflight --json"}, nil, nil)
	}
	resourcePreflight = ops.ResourcePreflight(ctx, status, health, sweep)
	phasePass(&report, "resource_preflight", ops.Now(), resourcePreflight)
	if !resourcePreflight.HeavyWorkAllowed {
		report.OK = true
		report.State = "resource_blocked"
		report.Status = "skipped"
		report.Action = "skipped"
		report.NextCommands = uniqueCommands(report.NextCommands, resourcePreflight.NextCommands)
		skipPhases(&report, []string{"profile_seed", "daemon_keepalive", "daemon_health_check", "page_cleanup"}, ops.Now(), "resource_preflight_blocked")
		phaseStart(&report, "write_artifact", ops.Now())
		phasePass(&report, "write_artifact", ops.Now(), summaryPath)
		if err := writeMaintenanceSummary(ctx, ops, summaryPath, &report); err != nil {
			return fail("write_artifact", "artifact_write_failed", "internal", err.Error(), ExitInternal, []string{"cdp --browser-mode headless daemon maintenance --json"}, nil, err)
		}
		return report, nil
	}

	phaseStart(&report, "profile_seed", ops.Now())
	if ops.ProfileSeed == nil {
		return fail("profile_seed", "headless_maintenance_failed", "internal", "profile seed operation is unavailable", ExitInternal, []string{"cdp --browser-mode headless browser profile seed --json"}, nil, nil)
	}
	seedHuman, seedStatus, err := ops.ProfileSeed(ctx)
	seedResult := map[string]any{"human": seedHuman, "status": seedStatus}
	if err != nil {
		return fail("profile_seed", "profile_seed_failed", "connection", fmt.Sprintf("browser profile seed failed: %v", err), ExitConnection, seedStatus.NextCommands, seedResult, err)
	}
	phasePass(&report, "profile_seed", ops.Now(), seedResult)

	if ops.DaemonHealth != nil {
		status, health, healthErr = ops.DaemonHealth(ctx)
		if healthErr != nil {
			report.Warnings = appendStringReasons(report.Warnings, "post_seed_health_failed: "+healthErr.Error())
		}
	}
	if health == nil {
		health = map[string]any{}
	}

	phaseStart(&report, "daemon_keepalive", ops.Now())
	if ops.Keepalive == nil {
		return fail("daemon_keepalive", "headless_maintenance_failed", "internal", "daemon keepalive operation is unavailable", ExitInternal, []string{"cdp --browser-mode headless daemon keepalive --repair --json"}, nil, nil)
	}
	keepaliveHuman, keepaliveData, err := ops.Keepalive(ctx, lock, status, health, sweep, resourcePreflight)
	keepaliveResult := map[string]any{"human": keepaliveHuman, "data": keepaliveData}
	if err != nil {
		return fail("daemon_keepalive", "daemon_keepalive_failed", "connection", fmt.Sprintf("daemon keepalive failed: %v", err), ExitConnection, mapNextCommands(keepaliveData), keepaliveResult, err)
	}
	phasePass(&report, "daemon_keepalive", ops.Now(), keepaliveResult)
	keepaliveState, _ := stringMapField(keepaliveData, "state")
	keepaliveStatus, _ := stringMapField(keepaliveData, "status")
	if keepaliveState == "resource_blocked" {
		report.OK = true
		report.State = "resource_blocked"
		report.Status = "skipped"
		report.Action = "skipped"
		report.NextCommands = uniqueCommands(report.NextCommands, mapNextCommands(keepaliveData))
		skipPhases(&report, []string{"daemon_health_check", "page_cleanup"}, ops.Now(), "resource_preflight_blocked")
		phaseStart(&report, "write_artifact", ops.Now())
		phasePass(&report, "write_artifact", ops.Now(), summaryPath)
		if err := writeMaintenanceSummary(ctx, ops, summaryPath, &report); err != nil {
			return fail("write_artifact", "artifact_write_failed", "internal", err.Error(), ExitInternal, []string{"cdp --browser-mode headless daemon maintenance --json"}, nil, err)
		}
		return report, nil
	}
	if keepaliveState == "unhealthy" || keepaliveStatus == "skipped" {
		phaseSkip(&report, "daemon_keepalive", ops.Now(), "repair_skipped", keepaliveResult)
		if !opts.HealthCheck {
			report.OK = true
			report.State = "daemon_unhealthy"
			report.Status = "warn"
			report.Action = "skipped"
			report.NextCommands = uniqueCommands(report.NextCommands, mapNextCommands(keepaliveData))
			phaseSkip(&report, "daemon_health_check", ops.Now(), "disabled", nil)
			if opts.Cleanup {
				phaseSkip(&report, "page_cleanup", ops.Now(), "daemon_unhealthy", nil)
			} else {
				phaseSkip(&report, "page_cleanup", ops.Now(), "disabled", nil)
			}
			phaseStart(&report, "write_artifact", ops.Now())
			phasePass(&report, "write_artifact", ops.Now(), summaryPath)
			if err := writeMaintenanceSummary(ctx, ops, summaryPath, &report); err != nil {
				return fail("write_artifact", "artifact_write_failed", "internal", err.Error(), ExitInternal, []string{"cdp --browser-mode headless daemon maintenance --json"}, nil, err)
			}
			return report, nil
		}
	}

	if opts.HealthCheck {
		phaseStart(&report, "daemon_health_check", ops.Now())
		if ops.HealthCheck == nil {
			return fail("daemon_health_check", "headless_maintenance_failed", "internal", "daemon health-check operation is unavailable", ExitInternal, []string{"cdp --browser-mode headless daemon health-check --repair --json"}, nil, nil)
		}
		healthContext := ctx
		if autoHealLeaseHeld {
			healthContext = withAutoHealLease(ctx)
		}
		healthHuman, healthData, err := ops.HealthCheck(healthContext)
		healthResult := map[string]any{"human": healthHuman, "data": healthData}
		if err != nil {
			return fail("daemon_health_check", "daemon_health_check_failed", "check_failed", fmt.Sprintf("daemon health-check failed: %v", err), ExitCheckFailed, mapNextCommands(healthData), healthResult, err)
		}
		phasePass(&report, "daemon_health_check", ops.Now(), healthResult)
	} else {
		phaseSkip(&report, "daemon_health_check", ops.Now(), "disabled", nil)
	}

	if opts.Cleanup {
		phaseStart(&report, "page_cleanup", ops.Now())
		if ops.PageCleanup == nil {
			return fail("page_cleanup", "headless_maintenance_failed", "internal", "page cleanup operation is unavailable", ExitInternal, []string{"cdp --browser-mode headless page cleanup --json"}, nil, nil)
		}
		cleanupHuman, cleanupData, err := ops.PageCleanup(ctx)
		cleanupResult := map[string]any{"human": cleanupHuman, "data": cleanupData}
		if err != nil {
			return fail("page_cleanup", "page_cleanup_failed", "connection", fmt.Sprintf("page cleanup failed: %v", err), ExitConnection, mapNextCommands(cleanupData), cleanupResult, err)
		}
		phasePass(&report, "page_cleanup", ops.Now(), cleanupResult)
	} else {
		phaseSkip(&report, "page_cleanup", ops.Now(), "disabled", nil)
	}

	report.OK = true
	report.State = "healthy"
	report.Status = "pass"
	report.Action = "maintained"
	phaseStart(&report, "write_artifact", ops.Now())
	phasePass(&report, "write_artifact", ops.Now(), summaryPath)
	if err := writeMaintenanceSummary(ctx, ops, summaryPath, &report); err != nil {
		return fail("write_artifact", "artifact_write_failed", "internal", err.Error(), ExitInternal, []string{"cdp --browser-mode headless daemon maintenance --json"}, nil, err)
	}
	return report, nil
}

func (a *app) runDaemonMaintenanceKeepalive(ctx context.Context, storeDir string, lock daemon.LockHandle, opts daemonMaintenanceOptions, status daemon.Status, health map[string]any, sweep *browser.ManagedProcessReconcileResult, resourcePreflight resourcePreflightResult) (string, map[string]any, error) {
	connectionName := a.connectionStateName(ctx)
	mode := strings.TrimSpace(status.ConnectionMode)
	if mode == "" {
		mode = a.connectionMode()
	}
	runtimeHealthy, runtimeCheck := keepaliveRuntimeCheck(ctx, status)
	runtimeCheck["resource_preflight"] = resourcePreflight
	if sweep != nil {
		runtimeCheck["managed_process_sweep"] = *sweep
	}
	probeResult := map[string]any{
		"mode":                            "maintenance",
		"result":                          healthFailureResult(health),
		"repair_requested":                opts.Repair,
		"force_requested":                 opts.Force,
		"managed_process_sweep_requested": false,
	}
	if status.State == "running" && runtimeHealthy {
		if opts.Repair {
			holdReconcile, reconcileErr := daemon.ReconcileOrphanedDaemonHolds(ctx, storeDir, "headless", true)
			if reconcileErr != nil {
				return "", nil, commandError(
					"daemon_hold_reconciliation_failed",
					"connection",
					fmt.Sprintf("reconcile orphaned headless daemon holds: %v", reconcileErr),
					ExitConnection,
					[]string{"cdp --browser-mode headless daemon logs --tail 50 --json", "cdp --browser-mode headless daemon maintenance --repair --json"},
				)
			}
			runtimeCheck["daemon_hold_reconciliation"] = holdReconcile
			if len(holdReconcile.SignalFailures) > 0 {
				return "", nil, commandErrorWithData(
					"daemon_hold_reconciliation_failed",
					"connection",
					"one or more verified orphaned daemon holds could not be reclaimed",
					ExitConnection,
					[]string{"cdp --browser-mode headless daemon logs --tail 50 --json", "cdp --browser-mode headless daemon maintenance --repair --json"},
					map[string]any{"browser_mode": "headless", "health": runtimeCheck, "daemon": status},
				)
			}
			if len(holdReconcile.ReclaimedPIDs) > 0 {
				status.Health = a.browserHealthSnapshot(ctx, status, false)
			}
		}
		action := "none"
		if holdReconcile, ok := runtimeCheck["daemon_hold_reconciliation"].(daemon.DaemonHoldReconcileResult); ok && len(holdReconcile.ReclaimedPIDs) > 0 {
			action = "reconciled"
		}
		data := map[string]any{
			"ok":                 true,
			"browser_mode":       "headless",
			"connection":         connectionName,
			"mode":               mode,
			"state":              "healthy",
			"action":             action,
			"locked":             false,
			"daemon":             status,
			"probe":              probeResult,
			"health":             runtimeCheck,
			"resource_preflight": resourcePreflight,
			"lock":               map[string]any{"name": lock.Metadata.Name, "acquired": true},
		}
		return fmt.Sprintf("keepalive\t%s\thealthy", connectionName), data, nil
	}
	if !opts.Repair {
		data := map[string]any{
			"ok":                 true,
			"browser_mode":       "headless",
			"connection":         connectionName,
			"mode":               mode,
			"state":              "unhealthy",
			"status":             "skipped",
			"action":             "skipped",
			"daemon":             status,
			"probe":              probeResult,
			"health":             runtimeCheck,
			"resource_preflight": resourcePreflight,
			"next_commands":      []string{"cdp --browser-mode headless daemon maintenance --repair --json"},
			"lock":               map[string]any{"name": lock.Metadata.Name, "acquired": true},
		}
		return fmt.Sprintf("keepalive\t%s\tskipped", connectionName), data, nil
	}
	return a.runHeadlessKeepaliveStartOrRepair(ctx, storeDir, lock, connectionName, mode, opts.Reconnect, opts.ChromeCommand, opts.Force, false, opts.Repair, opts.StaleLockAfter, status, probeResult, runtimeCheck)
}

func writeMaintenanceSummary(ctx context.Context, ops daemonMaintenanceOperations, path string, report *daemonMaintenanceReport) error {
	if ops.WriteArtifact == nil {
		return nil
	}
	finishDaemonMaintenanceReport(report, ops.Now())
	return ops.WriteArtifact(ctx, path, *report)
}

func finishDaemonMaintenanceReport(report *daemonMaintenanceReport, now time.Time) {
	report.FinishedAt = now.UTC().Format(time.RFC3339)
}

func setRemainingPhaseStatus(report *daemonMaintenanceReport, status string) {
	for i := range report.Phases {
		report.Phases[i].Status = status
		report.Phases[i].StartedAt = ""
		report.Phases[i].FinishedAt = ""
		report.Phases[i].Error = ""
		report.Phases[i].Result = nil
	}
}

func phaseStart(report *daemonMaintenanceReport, name string, now time.Time) {
	if idx := maintenancePhaseIndex(report, name); idx >= 0 {
		report.Phases[idx].Status = "running"
		report.Phases[idx].StartedAt = now.UTC().Format(time.RFC3339)
		report.Phases[idx].FinishedAt = ""
		report.Phases[idx].Error = ""
	}
}

func phasePass(report *daemonMaintenanceReport, name string, now time.Time, result any) {
	if idx := maintenancePhaseIndex(report, name); idx >= 0 {
		report.Phases[idx].Status = "passed"
		report.Phases[idx].FinishedAt = now.UTC().Format(time.RFC3339)
		report.Phases[idx].Result = result
		report.Phases[idx].Error = ""
	}
}

func phaseSkip(report *daemonMaintenanceReport, name string, now time.Time, reason string, result any) {
	if idx := maintenancePhaseIndex(report, name); idx >= 0 {
		if report.Phases[idx].StartedAt == "" {
			report.Phases[idx].StartedAt = now.UTC().Format(time.RFC3339)
		}
		report.Phases[idx].Status = "skipped"
		report.Phases[idx].FinishedAt = now.UTC().Format(time.RFC3339)
		report.Phases[idx].Error = reason
		report.Phases[idx].Result = result
	}
}

func phaseFail(report *daemonMaintenanceReport, name string, now time.Time, message string, result any) {
	if idx := maintenancePhaseIndex(report, name); idx >= 0 {
		if report.Phases[idx].StartedAt == "" {
			report.Phases[idx].StartedAt = now.UTC().Format(time.RFC3339)
		}
		report.Phases[idx].Status = "failed"
		report.Phases[idx].FinishedAt = now.UTC().Format(time.RFC3339)
		report.Phases[idx].Error = message
		report.Phases[idx].Result = result
	}
}

func skipPhases(report *daemonMaintenanceReport, names []string, now time.Time, reason string) {
	for _, name := range names {
		phaseSkip(report, name, now, reason, nil)
	}
}

func skipRemainingPhases(report *daemonMaintenanceReport, afterName, reason string) {
	seen := false
	now := time.Now().UTC()
	for i := range report.Phases {
		if report.Phases[i].Name == afterName {
			seen = true
			continue
		}
		if seen {
			report.Phases[i].Status = "skipped"
			report.Phases[i].StartedAt = now.Format(time.RFC3339)
			report.Phases[i].FinishedAt = now.Format(time.RFC3339)
			report.Phases[i].Error = reason
		}
	}
}

func maintenancePhaseIndex(report *daemonMaintenanceReport, name string) int {
	for i := range report.Phases {
		if report.Phases[i].Name == name {
			return i
		}
	}
	return -1
}

func mapNextCommands(data map[string]any) []string {
	if data == nil {
		return nil
	}
	return toStringSlice(data["next_commands"])
}

func daemonMaintenancePhases(opts daemonMaintenanceResolvedOptions, summaryPath string) []daemonMaintenancePhase {
	seedCommand := modeScopedCommand(string(config.BrowserModeHeadless), fmt.Sprintf("browser profile seed --strategy %s --if-older-than %s --json", opts.ProfileSeedStrategy, opts.ProfileSeedIfOlderThan))
	keepaliveParts := []string{"daemon", "keepalive", "--managed-process-sweep"}
	if opts.Repair {
		keepaliveParts = append(keepaliveParts, "--repair")
	}
	if opts.Force {
		keepaliveParts = append(keepaliveParts, "--force")
	}
	keepaliveParts = append(keepaliveParts, "--reconnect", opts.Reconnect, "--json")
	keepaliveCommand := modeScopedCommand(string(config.BrowserModeHeadless), strings.Join(keepaliveParts, " "))
	healthParts := []string{"daemon", "health-check", "--managed-process-sweep"}
	if opts.Repair {
		healthParts = append(healthParts, "--repair")
	}
	if opts.Force {
		healthParts = append(healthParts, "--force")
	}
	healthParts = append(healthParts, "--json")
	healthCommand := modeScopedCommand(string(config.BrowserModeHeadless), strings.Join(healthParts, " "))
	cleanupParts := []string{"page", "cleanup", "--created-by", "cdp", "--idle-for", opts.CleanupIdleFor}
	if opts.CleanupClose {
		cleanupParts = append(cleanupParts, "--close")
	}
	if opts.Force {
		cleanupParts = append(cleanupParts, "--force")
	}
	cleanupParts = append(cleanupParts, "--wait-gone", "--max-attempts", fmt.Sprint(opts.CleanupMaxAttempts), "--close-concurrency", fmt.Sprint(opts.CleanupConcurrency), "--max", fmt.Sprint(opts.CleanupMax), "--json")
	cleanupCommand := modeScopedCommand(string(config.BrowserModeHeadless), strings.Join(cleanupParts, " "))
	return []daemonMaintenancePhase{
		{Order: 1, Name: "acquire_lock", Status: "planned", Required: true, Description: "Acquire the maintenance lock before inspecting or mutating managed headless state."},
		{Order: 2, Name: "managed_process_sweep", Status: "planned", Required: true, Mutates: true, Description: "Reconcile the managed Chrome PID registry and reap stale or duplicate cdp-owned managed headless processes before launch-capable work."},
		{Order: 3, Name: "resource_preflight", Status: "planned", Required: true, Description: "Check host and browser resource budget before profile replacement, repair, or synthetic validation."},
		{Order: 4, Name: "profile_seed", Status: "planned", Required: true, Mutates: true, HeavyWork: opts.ProfileSeedStrategy == browser.ProfileSeedStrategyCopyDefault, ResourceGated: true, Command: seedCommand, ArtifactKey: "headless_profile_seed_summary", Description: "Run configured freshness-gated managed profile seeding without embedding copied profile contents in JSON."},
		{Order: 5, Name: "daemon_keepalive", Status: "planned", Required: true, Mutates: true, HeavyWork: true, ResourceGated: true, Command: keepaliveCommand, Description: "Repair or start the managed headless daemon after lifecycle reconciliation."},
		{Order: 6, Name: "daemon_health_check", Status: plannedStatus(opts.HealthCheck), Required: opts.HealthCheck, Mutates: opts.HealthCheck, HeavyWork: opts.HealthCheck, ResourceGated: true, Command: healthCommand, ArtifactKey: "headless_health_summary", Description: "Validate the managed headless daemon against synthetic browser data and write health artifacts."},
		{Order: 7, Name: "page_cleanup", Status: plannedStatus(opts.Cleanup), Required: opts.Cleanup, Mutates: opts.CleanupClose, ResourceGated: false, Command: cleanupCommand, ArtifactKey: "headless_page_cleanup_log", Description: "Close idle cdp-created headless pages within existing cleanup safety rules."},
		{Order: 8, Name: "write_artifact", Status: "planned", Required: true, Mutates: true, Command: summaryPath, ArtifactKey: "summary", Description: "Write a privacy-safe latest maintenance summary for cron status and doctor surfaces."},
	}
}

func plannedStatus(enabled bool) string {
	if enabled {
		return "planned"
	}
	return "disabled"
}
