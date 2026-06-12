package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/spf13/cobra"
)

const (
	cronManagedBlockStart = "# cdp-cli managed browser runtime tasks"
	cronManagedBlockEnd   = "# End cdp-cli managed browser runtime tasks"
)

type cronRenderOptions struct {
	Profile       string
	CDPBin        string
	LogDir        string
	Display       string
	XDGRuntimeDir string
	Reconnect     time.Duration
	BrowserMode   string
	SeedStrategy  string
	SeedAfter     time.Duration
}

type cronBlockState struct {
	Installed bool     `json:"installed"`
	Entries   []string `json:"entries"`
	Text      string   `json:"text,omitempty"`
}

const (
	cronTaskHeadedDaemonKeepalive    = "headed-daemon-keepalive"
	cronTaskHeadlessMaintenance      = "headless-maintenance"
	cronTaskHeadlessDaemonKeepalive  = "headless-daemon-keepalive"
	cronTaskHeadlessDaemonHealth     = "headless-daemon-health-check"
	cronTaskHeadlessProfileSeed      = "headless-profile-seed"
	cronTaskHeadlessPageCleanup      = "headless-page-cleanup"
	cronManagedProcessSweepFlag      = "--managed-process-sweep"
	cronManagedProcessSweepPhaseName = "managed-process-sweep"
)

type managedCronTask struct {
	ID                          string
	BrowserMode                 string
	Schedule                    string
	LockName                    string
	LogName                     string
	LogArtifactKey              string
	Purpose                     string
	Command                     string
	ProbeWords                  []string
	ConfigDependencies          []string
	LaunchCapable               bool
	RequiresManagedProcessSweep bool
	ProvidesManagedProcessSweep bool
	CronEntry                   string
}

type cronTaskStatus struct {
	ID                           string   `json:"id"`
	BrowserMode                  string   `json:"browser_mode"`
	Schedule                     string   `json:"schedule"`
	LockName                     string   `json:"lock_name"`
	LogName                      string   `json:"log_name"`
	Purpose                      string   `json:"purpose"`
	ConfigDependencies           []string `json:"config_dependencies,omitempty"`
	LaunchCapable                bool     `json:"launch_capable"`
	RequiresManagedProcessSweep  bool     `json:"requires_managed_process_sweep"`
	ManagedProcessSweepPhase     string   `json:"managed_process_sweep_phase,omitempty"`
	ManagedProcessSweepInstalled bool     `json:"managed_process_sweep_installed"`
	Installed                    bool     `json:"installed"`
	MatchesIntended              bool     `json:"matches_intended"`
	Status                       string   `json:"status"`
}

type cronFlockOwner struct {
	PID int
	Age time.Duration
}

var cronFlockOwnerForPath = cronFlockOwnerFromProcLocks

func (a *app) newCronCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage cdp-managed browser runtime cron tasks",
		Long:  "Install, inspect, diff, remove, and heal the cdp-managed user crontab block for headed daemon keepalive and canonical unattended headless maintenance tasks.",
	}
	cmd.AddCommand(a.newCronStatusCommand())
	cmd.AddCommand(a.newCronDiffCommand())
	cmd.AddCommand(a.newCronInstallCommand())
	cmd.AddCommand(a.newCronMigrateCommand())
	cmd.AddCommand(a.newCronRemoveCommand())
	cmd.AddCommand(a.newCronHealCommand())
	return cmd
}

func (a *app) newCronStatusCommand() *cobra.Command {
	opts := defaultCronRenderOptions()
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the installed cdp-managed cron block and runtime state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			if err := a.applyCronBrowserMode(cmd, &opts); err != nil {
				return err
			}
			current, err := readUserCrontab(ctx)
			available := !isCrontabMissing(err)
			if err != nil && !isEmptyCrontab(err) && !isCrontabMissing(err) {
				return cronCommandError("read crontab", err)
			}
			state := extractCronManagedBlock(current)
			tasks := managedCronTasks(opts)
			intended := managedCronBlock(opts)
			matchesIntended := normalizeCronBlock(state.Text) == normalizeCronBlock(intended)
			status := scheduledTasksStatusForSummary(available, err, summarizeCrontab(current))
			store, storeErr := a.stateStore()
			locks := map[string]any{}
			daemonLocks := map[string]any{}
			artifacts := map[string]any{}
			profileSeed := cronProfileSeedMetadata(opts)
			managedProcesses := any(map[string]any{"checked": false, "state": "unknown"})
			if storeErr == nil {
				locks = cronLockStates(store.Dir)
				daemonLocks = cronDaemonLockStates(store.Dir)
				artifacts = cronLastRunArtifacts(store.Dir)
				if lastSeed, ok, seedErr := loadProfileSeedStatus(store.Dir); seedErr == nil && ok {
					profileSeed["last_seed"] = lastSeed
				}
				if lifecycle, lifecycleErr := browser.ReconcileManagedProcesses(ctx, store.Dir, browser.ManagedProcessReconcileOptions{ReadOnly: true}); lifecycleErr == nil {
					managedProcesses = lifecycle
				} else {
					managedProcesses = map[string]any{"checked": true, "state": "error", "reason": lifecycleErr.Error()}
				}
			}
			health := cronStatusHealth(available, state.Installed, matchesIntended, locks, daemonLocks)
			data := map[string]any{
				"ok":                 true,
				"state":              health["state"],
				"browser_mode":       opts.BrowserMode,
				"profile_seed":       profileSeed,
				"available":          available,
				"installed":          state.Installed,
				"matches_intended":   matchesIntended,
				"health":             health,
				"managed_block":      state,
				"intended_block":     extractCronManagedBlock(intended),
				"tasks":              cronTaskStatuses(state.Entries, tasks),
				"scheduled_tasks":    status,
				"locks":              locks,
				"daemon_locks":       daemonLocks,
				"last_run_artifacts": artifacts,
				"managed_processes":  managedProcesses,
				"processes_by_mode":  a.daemonProcessesByMode(ctx),
				"next_commands":      health["next_commands"],
			}
			human := "cdp cron block not installed"
			if state.Installed {
				human = fmt.Sprintf("cdp cron block installed with %d entries", len(state.Entries))
			}
			return a.render(ctx, human, data)
		},
	}
	addCronRenderFlags(cmd, &opts)
	return cmd
}

func (a *app) newCronDiffCommand() *cobra.Command {
	opts := defaultCronRenderOptions()
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Compare the installed cdp-managed cron block with the intended block",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			if err := a.applyCronBrowserMode(cmd, &opts); err != nil {
				return err
			}
			current, err := readUserCrontab(ctx)
			if err != nil && !isEmptyCrontab(err) {
				return cronCommandError("read crontab", err)
			}
			installed := extractCronManagedBlock(current)
			tasks := managedCronTasks(opts)
			intendedText := managedCronBlock(opts)
			intended := extractCronManagedBlock(intendedText)
			without := withoutCronManagedBlock(current)
			wanted := appendCronManagedBlock(without, intendedText)
			data := map[string]any{
				"ok":               true,
				"browser_mode":     opts.BrowserMode,
				"profile_seed":     cronProfileSeedMetadata(opts),
				"installed":        installed.Installed,
				"matches_intended": normalizeCronBlock(installed.Text) == normalizeCronBlock(intendedText),
				"current_block":    installed,
				"intended_block":   intended,
				"tasks":            cronTaskStatuses(installed.Entries, tasks),
				"actions":          cronDiffActions(current, wanted, installed.Installed),
				"next_commands":    []string{"cdp cron install --json", "cdp cron remove --json"},
			}
			human := "cdp cron block differs"
			if data["matches_intended"] == true {
				human = "cdp cron block matches intended entries"
			}
			return a.render(ctx, human, data)
		},
	}
	addCronRenderFlags(cmd, &opts)
	return cmd
}

func (a *app) newCronInstallCommand() *cobra.Command {
	opts := defaultCronRenderOptions()
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install or repair the cdp-managed user crontab block",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			if err := a.applyCronBrowserMode(cmd, &opts); err != nil {
				return err
			}
			current, err := readUserCrontab(ctx)
			if err != nil && !isEmptyCrontab(err) {
				return cronCommandError("read crontab", err)
			}
			summary := summarizeCrontab(current)
			tasks := managedCronTasks(opts)
			block := managedCronBlock(opts)
			next := appendCronManagedBlock(withoutCronManagedBlock(current), block)
			changed := current != next
			if changed && !dryRun {
				if err := writeUserCrontab(ctx, next); err != nil {
					return cronCommandError("write crontab", err)
				}
			}
			installed := extractCronManagedBlock(next)
			if dryRun {
				installed = extractCronManagedBlock(block)
			}
			data := map[string]any{
				"ok":               true,
				"browser_mode":     opts.BrowserMode,
				"profile_seed":     cronProfileSeedMetadata(opts),
				"action":           actionString(changed, "installed", "unchanged"),
				"changed":          changed,
				"dry_run":          dryRun,
				"installed":        !dryRun,
				"matches_intended": true,
				"managed_block":    installed,
				"intended_block":   extractCronManagedBlock(block),
				"tasks":            cronTaskStatuses(installed.Entries, tasks),
				"warnings":         cronInstallWarnings(opts, summary),
				"next_commands":    []string{"cdp cron status --json", "cdp doctor --check scheduled-tasks --json"},
			}
			if dryRun {
				data["action"] = actionString(changed, "would_install", "unchanged")
				data["next_commands"] = []string{"cdp cron install --json", "cdp cron diff --json"}
			}
			return a.render(ctx, fmt.Sprintf("cdp cron block %s", data["action"]), data)
		},
	}
	addCronRenderFlags(cmd, &opts)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "render the managed crontab block without installing it")
	return cmd
}

func (a *app) newCronRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove only the cdp-managed user crontab block",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			current, err := readUserCrontab(ctx)
			if err != nil && !isEmptyCrontab(err) {
				return cronCommandError("read crontab", err)
			}
			installed := extractCronManagedBlock(current)
			next := withoutCronManagedBlock(current)
			changed := current != next
			if changed {
				if err := writeUserCrontab(ctx, next); err != nil {
					return cronCommandError("write crontab", err)
				}
			}
			data := map[string]any{
				"ok":            true,
				"action":        actionString(changed, "removed", "unchanged"),
				"changed":       changed,
				"removed":       installed.Installed,
				"removed_block": installed,
				"next_commands": []string{"cdp cron status --json", "cdp cron install --json"},
			}
			return a.render(ctx, fmt.Sprintf("cdp cron block %s", data["action"]), data)
		},
	}
}

func (a *app) newCronMigrateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate legacy unmanaged cron entries to cdp-managed tasks",
	}
	cmd.AddCommand(a.newCronMigratePagesPollingCommand())
	return cmd
}

func (a *app) newCronMigratePagesPollingCommand() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "pages-polling",
		Short: "Remove unmanaged cdp pages polling entries after the managed cron block is installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			current, err := readUserCrontab(ctx)
			if err != nil && !isEmptyCrontab(err) {
				return cronCommandError("read crontab", err)
			}
			managed := extractCronManagedBlock(current)
			managedKeepaliveInstalled := summarizeCrontab(managed.Text).HasDaemonKeepalive
			next, candidates := withoutLegacyPagesPollingCronEntries(current)
			changed := current != next
			dryRun := !apply
			warnings := cronPagesPollingMigrationWarnings(changed, dryRun, managedKeepaliveInstalled)
			nextCommands := cronPagesPollingMigrationNextCommands(changed, dryRun, managedKeepaliveInstalled)
			candidateEntries := stringSliceOrEmpty(candidates)

			if apply && changed && !managedKeepaliveInstalled {
				return commandError(
					"managed_keepalive_required",
					"usage",
					"managed daemon keepalive is not installed; run cdp cron install --json and verify cdp cron status before removing legacy pages polling entries",
					ExitUsage,
					[]string{"cdp cron install --json", "cdp cron status --json", "cdp cron migrate pages-polling --apply --json"},
				)
			}
			if apply && changed {
				if err := writeUserCrontab(ctx, next); err != nil {
					return cronCommandError("write crontab", err)
				}
			}

			action := "unchanged"
			if changed && dryRun {
				action = "would_remove"
			} else if changed {
				action = "removed"
			}
			data := map[string]any{
				"ok":                          true,
				"action":                      action,
				"changed":                     changed,
				"dry_run":                     dryRun,
				"applied":                     apply && changed,
				"candidate_count":             len(candidates),
				"removed_count":               removedCount(changed, dryRun, len(candidates)),
				"managed_keepalive_installed": managedKeepaliveInstalled,
				"candidate_entries":           candidateEntries,
				"removed_entries":             removedEntries(changed, dryRun, candidateEntries),
				"warnings":                    warnings,
				"next_commands":               nextCommands,
			}
			return a.render(ctx, fmt.Sprintf("legacy pages polling cron entries %s", action), data)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "write the crontab change after the managed cron block is installed")
	return cmd
}

func (a *app) newCronHealCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "heal", Short: "Run a bounded browser runtime healing flow"}
	cmd.AddCommand(a.newCronHealHeadedCommand())
	return cmd
}

func (a *app) newCronHealHeadedCommand() *cobra.Command {
	var reconnect time.Duration
	var lockTimeout time.Duration
	var staleLockAfter time.Duration
	var display string
	var chromeCommand string
	var chromeArgs []string
	cmd := &cobra.Command{
		Use:   "headed",
		Short: "Heal the headed daemon keepalive path for scheduled tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if reconnect < 0 || lockTimeout < 0 || staleLockAfter < 0 {
				return commandError("invalid_duration", "usage", "--reconnect, --lock-timeout, and --stale-lock-after must be non-negative", ExitUsage, []string{"cdp cron heal headed --reconnect 30s --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 60*time.Second)
			defer cancel()
			a.opts.browserMode = "headed"
			a.opts.autoConnect = true
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			lockName := "cron-heal-headed"
			lock, acquired, existingLock, err := daemon.AcquireLock(ctx, store.Dir, lockName, lockTimeout, staleLockAfter, daemon.LockMetadata{Name: lockName, Phase: "checking"})
			if err != nil {
				return commandError("lock_failed", "connection", fmt.Sprintf("acquire cron heal lock: %v", err), ExitConnection, []string{"cdp cron status --json"})
			}
			if !acquired {
				return a.render(ctx, "cron heal headed locked", map[string]any{
					"ok":            true,
					"browser_mode":  "headed",
					"state":         "locked",
					"action":        "skipped",
					"locked":        true,
					"lock":          existingLock,
					"next_commands": []string{"cdp cron status --json", "cdp daemon logs --tail 50 --json"},
				})
			}
			defer lock.Release()

			a.opts.activeProbe = false
			probe, err := a.browserProbe(ctx)
			if err != nil {
				return commandError("invalid_browser_url", "usage", err.Error(), ExitUsage, []string{"cdp cron heal headed --auto-connect --json"})
			}
			before := a.daemonStatus(ctx, probe)
			if before.State == "running" && daemon.RuntimeRunning(runtimeOrZero(before)) {
				health := a.browserHealthSnapshot(ctx, before, false)
				if health["state"] == "healthy" {
					return a.render(ctx, "cron heal headed healthy", map[string]any{
						"ok":            true,
						"browser_mode":  "headed",
						"state":         "healthy",
						"action":        "none",
						"locked":        false,
						"daemon":        before,
						"health":        health,
						"lock":          map[string]any{"name": lockName, "acquired": true},
						"next_commands": []string{"cdp --browser-mode headed daemon health --json", "cdp cron status --json"},
					})
				}
			}
			if before.State == "running" {
				if err := lock.Update(ctx, "stopping_unhealthy_daemon"); err != nil {
					return err
				}
				if _, _, err := daemon.StopRuntimeForMode(ctx, store.Dir, "headed"); err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("stop unhealthy headed daemon: %v", err), ExitConnection, []string{"cdp --browser-mode headed daemon stop --json", "cdp cron heal headed --json"})
				}
			}
			if err := lock.Update(ctx, "ensuring_chrome"); err != nil {
				return err
			}
			chrome, err := ensureChromeForKeepalive(ctx, display, chromeCommand, chromeArgs)
			if err != nil {
				return commandError("chrome_start_failed", "connection", fmt.Sprintf("ensure headed Chrome is running: %v", err), ExitConnection, []string{"cdp cron heal headed --chrome-command <command> --json", "open chrome://inspect/#remote-debugging"})
			}
			if err := lock.Update(ctx, "starting_daemon"); err != nil {
				return err
			}
			a.opts.activeProbe = true
			result, err := a.runDaemonStart(ctx, daemonStartConfig{reconnect: reconnect, connectionName: "default", remember: true})
			if err != nil {
				return err
			}
			health := map[string]any{}
			if status, ok := result.data["daemon"].(daemon.Status); ok {
				health = a.browserHealthSnapshot(ctx, status, false)
			}
			if err := lock.Update(ctx, "healed"); err != nil {
				return err
			}
			result.data["browser_mode"] = "headed"
			result.data["state"] = "healed"
			result.data["action"] = "repaired"
			result.data["locked"] = false
			result.data["chrome"] = chrome
			result.data["previous"] = before
			result.data["health"] = health
			result.data["lock"] = map[string]any{"name": lockName, "acquired": true}
			result.data["next_commands"] = []string{"cdp --browser-mode headed daemon health --json", "cdp daemon logs --tail 50 --json", "cdp cron status --json"}
			return a.render(ctx, "cron heal headed repaired", result.data)
		},
	}
	cmd.Flags().DurationVar(&reconnect, "reconnect", 30*time.Second, "daemon reconnect interval, such as 30s")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 0, "how long to wait for another cron heal lock; 0s skips immediately")
	cmd.Flags().DurationVar(&staleLockAfter, "stale-lock-after", 10*time.Minute, "remove a cron heal lock older than this duration; 0 disables stale cleanup")
	cmd.Flags().StringVar(&display, "display", os.Getenv("DISPLAY"), "DISPLAY value to use when launching Chrome for headed repair")
	cmd.Flags().StringVar(&chromeCommand, "chrome-command", "google-chrome-stable", "Chrome command to launch for headed repair; empty disables launch")
	cmd.Flags().StringArrayVar(&chromeArgs, "chrome-args", nil, "extra Chrome argument; repeat for multiple arguments")
	return cmd
}

func addCronRenderFlags(cmd *cobra.Command, opts *cronRenderOptions) {
	cmd.Flags().StringVar(&opts.Profile, "profile", opts.Profile, "managed cron profile to render; currently only agent")
	cmd.Flags().StringVar(&opts.CDPBin, "cdp-bin", opts.CDPBin, "cdp binary path used in generated cron entries")
	cmd.Flags().StringVar(&opts.LogDir, "log-dir", opts.LogDir, "log directory used in generated cron entries")
	cmd.Flags().StringVar(&opts.Display, "display", opts.Display, "DISPLAY value used for headed cron healing")
	cmd.Flags().StringVar(&opts.XDGRuntimeDir, "xdg-runtime-dir", opts.XDGRuntimeDir, "XDG_RUNTIME_DIR value used for headed cron healing")
	cmd.Flags().DurationVar(&opts.Reconnect, "reconnect", opts.Reconnect, "daemon reconnect interval used in generated cron entries")
}

func defaultCronRenderOptions() cronRenderOptions {
	return cronRenderOptions{
		Profile:       "agent",
		CDPBin:        envDefault("CDP_BIN", "$HOME/.local/bin/cdp"),
		LogDir:        envDefault("CDP_LOG_DIR", "$HOME/.cdp-cli"),
		Display:       envDefault("DISPLAY", ":0"),
		XDGRuntimeDir: envDefault("XDG_RUNTIME_DIR", fmt.Sprintf("/run/user/%d", os.Getuid())),
		Reconnect:     30 * time.Second,
		BrowserMode:   "all",
		SeedStrategy:  browser.ProfileSeedStrategyManaged,
		SeedAfter:     6 * time.Hour,
	}
}

func (a *app) applyCronBrowserMode(cmd *cobra.Command, opts *cronRenderOptions) error {
	if opts == nil {
		return nil
	}
	if root := cmd.Root(); root != nil {
		flags := root.PersistentFlags()
		if flags.Changed("browser-mode") || flags.Changed("browserMode") {
			mode := strings.TrimSpace(strings.ToLower(a.opts.browserMode))
			switch mode {
			case "headed", "headless":
				opts.BrowserMode = mode
			default:
				return commandError("invalid_browser_mode", "usage", "--browser-mode must be headed or headless for cron rendering", ExitUsage, []string{"cdp --browser-mode headless cron install --dry-run --json", "cdp --browser-mode headed cron install --dry-run --json"})
			}
		}
	}
	if opts.BrowserMode == "" {
		opts.BrowserMode = "all"
	}
	cfg, err := config.Load(a.opts.config)
	if err != nil {
		return commandError("invalid_config", "usage", err.Error(), ExitUsage, []string{"cdp --config <path> cron install --dry-run --json"})
	}
	if strategy := strings.TrimSpace(cfg.Browser.Headless.ProfileSeedStrategy); strategy != "" {
		strategy = browser.NormalizeProfileSeedStrategy(strategy)
		if !browser.SupportedProfileSeedStrategy(strategy) {
			return commandError("invalid_profile_seed_strategy", "usage", fmt.Sprintf("unsupported managed profile seed strategy %q", cfg.Browser.Headless.ProfileSeedStrategy), ExitUsage, []string{"cdp --browser-mode headless browser profile seed --strategy managed --json", "cdp --browser-mode headless browser profile seed --strategy copy-default --json"})
		}
		opts.SeedStrategy = strategy
	}
	if cfg.Browser.Headless.ProfileRefreshAfter > 0 {
		opts.SeedAfter = cfg.Browser.Headless.ProfileRefreshAfter
	}
	if opts.SeedStrategy == "" {
		opts.SeedStrategy = browser.ProfileSeedStrategyManaged
	}
	if opts.SeedAfter <= 0 {
		opts.SeedAfter = 6 * time.Hour
	}
	return nil
}

func managedCronBlock(opts cronRenderOptions) string {
	tasks := managedCronTasks(opts)
	lines := []string{cronManagedBlockStart}
	for _, task := range tasks {
		lines = append(lines, task.CronEntry)
	}
	lines = append(lines, cronManagedBlockEnd)
	return strings.Join(lines, "\n") + "\n"
}

func managedCronTasks(opts cronRenderOptions) []managedCronTask {
	cdpBin := cronValue(opts.CDPBin)
	logDir := cronValue(opts.LogDir)
	display := cronValue(opts.Display)
	xdgRuntimeDir := cronValue(opts.XDGRuntimeDir)
	reconnect := opts.Reconnect.String()
	if opts.Reconnect <= 0 {
		reconnect = "30s"
	}
	seedStrategy := cronProfileSeedStrategy(opts)
	seedAfter := cronDurationLiteral(cronProfileSeedAfter(opts))
	var tasks []managedCronTask
	if opts.BrowserMode == "all" || opts.BrowserMode == "headed" {
		tasks = append(tasks, newManagedCronTask(
			managedCronTask{
				ID:                 cronTaskHeadedDaemonKeepalive,
				BrowserMode:        "headed",
				Schedule:           "* * * * *",
				LockName:           "keepalive-headed",
				LogName:            "keepalive-headed.log",
				LogArtifactKey:     "headed_keepalive_log",
				Purpose:            "Keep the headed daemon healthy with passive probing and noninteractive repair when an approved endpoint already exists.",
				Command:            fmt.Sprintf("env DISPLAY=%s XDG_RUNTIME_DIR=%s %s --browser-mode headed daemon keepalive --auto-connect --repair --probe passive --reconnect %s --display %s --json >> %s/keepalive-headed.log 2>&1", display, xdgRuntimeDir, cdpBin, reconnect, display, logDir),
				ProbeWords:         []string{"daemon", "keepalive"},
				ConfigDependencies: []string{"display", "xdg_runtime_dir", "reconnect"},
				LaunchCapable:      true,
			},
			logDir,
		))
	}
	if opts.BrowserMode == "all" || opts.BrowserMode == "headless" {
		tasks = append(tasks, newManagedCronTask(
			managedCronTask{
				ID:                          cronTaskHeadlessMaintenance,
				BrowserMode:                 "headless",
				Schedule:                    "* * * * *",
				LockName:                    "headless-maintenance",
				LogName:                     "headless-maintenance.log",
				LogArtifactKey:              "headless_maintenance_log",
				Purpose:                     "Run the canonical managed headless maintenance flow: sweep, resource preflight, profile seed, daemon repair, health-check, cleanup, and summary artifact write.",
				Command:                     fmt.Sprintf("%s --browser-mode headless daemon maintenance --profile-seed-strategy %s --profile-seed-if-older-than %s --repair --force --reconnect %s --health-check --cleanup --cleanup-close --json >> %s/headless-maintenance.log 2>&1", cdpBin, seedStrategy, seedAfter, reconnect, logDir),
				ProbeWords:                  []string{"daemon", "maintenance"},
				ConfigDependencies:          []string{"reconnect", "headless.profile_seed_strategy", "headless.profile_refresh_after"},
				LaunchCapable:               true,
				RequiresManagedProcessSweep: true,
				ProvidesManagedProcessSweep: true,
			},
			logDir,
		))
	}
	return tasks
}

func newManagedCronTask(task managedCronTask, logDir string) managedCronTask {
	task.CronEntry = fmt.Sprintf("%s %s", task.Schedule, cronLockedCommand(fmt.Sprintf("%s/locks/%s.lock", logDir, task.LockName), task.Command))
	return task
}

func cronTaskStatuses(entries []string, tasks []managedCronTask) []cronTaskStatus {
	statuses := make([]cronTaskStatus, 0, len(tasks))
	for _, task := range tasks {
		status := cronTaskStatus{
			ID:                           task.ID,
			BrowserMode:                  task.BrowserMode,
			Schedule:                     task.Schedule,
			LockName:                     task.LockName,
			LogName:                      task.LogName,
			Purpose:                      task.Purpose,
			ConfigDependencies:           append([]string(nil), task.ConfigDependencies...),
			LaunchCapable:                task.LaunchCapable,
			RequiresManagedProcessSweep:  task.RequiresManagedProcessSweep,
			ManagedProcessSweepInstalled: false,
			Status:                       "missing",
		}
		if task.RequiresManagedProcessSweep {
			status.ManagedProcessSweepPhase = cronManagedProcessSweepPhaseName
		}
		for _, entry := range entries {
			if !managedCronTaskMatchesLine(task, entry) {
				continue
			}
			status.Installed = true
			status.MatchesIntended = normalizeCronBlock(entry) == normalizeCronBlock(task.CronEntry)
			if task.RequiresManagedProcessSweep {
				status.ManagedProcessSweepInstalled = cronTaskManagedProcessSweepInstalled(task, entry)
			}
			switch {
			case status.MatchesIntended:
				status.Status = "installed"
			case status.RequiresManagedProcessSweep && !status.ManagedProcessSweepInstalled:
				status.Status = "blocked"
			default:
				status.Status = "stale"
			}
			break
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func cronTaskManagedProcessSweepInstalled(task managedCronTask, entry string) bool {
	if !task.RequiresManagedProcessSweep {
		return false
	}
	return strings.Contains(entry, cronManagedProcessSweepFlag) || task.ProvidesManagedProcessSweep
}

func managedCronTaskMatchesLine(task managedCronTask, line string) bool {
	if strings.TrimSpace(line) == "" {
		return false
	}
	mode := scheduledTaskBrowserMode(line)
	if task.BrowserMode != "" && mode != "" && mode != task.BrowserMode {
		return false
	}
	if len(task.ProbeWords) == 0 {
		return false
	}
	return scheduledTaskContainsCDPCommand(line, task.ProbeWords...)
}

func cronTaskIDs(tasks []managedCronTask) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	sort.Strings(ids)
	return ids
}

func cronTaskIDsByStatus(statuses []cronTaskStatus, states ...string) []string {
	wanted := map[string]bool{}
	for _, state := range states {
		wanted[state] = true
	}
	var ids []string
	for _, status := range statuses {
		if wanted[status.Status] {
			ids = append(ids, status.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func cronProfileSeedStrategy(opts cronRenderOptions) string {
	strategy := browser.NormalizeProfileSeedStrategy(opts.SeedStrategy)
	if !browser.SupportedProfileSeedStrategy(strategy) {
		return browser.ProfileSeedStrategyManaged
	}
	return strategy
}

func cronProfileSeedAfter(opts cronRenderOptions) time.Duration {
	if opts.SeedAfter > 0 {
		return opts.SeedAfter
	}
	return 6 * time.Hour
}

func cronProfileSeedSchedule(seedAfter time.Duration) string {
	if seedAfter <= 0 {
		seedAfter = 6 * time.Hour
	}
	switch {
	case seedAfter <= 15*time.Minute:
		return "*/5 * * * *"
	case seedAfter <= time.Hour:
		return "*/15 * * * *"
	case seedAfter <= 6*time.Hour:
		return "0 * * * *"
	default:
		return "0 */6 * * *"
	}
}

func cronProfileSeedMetadata(opts cronRenderOptions) map[string]any {
	after := cronProfileSeedAfter(opts)
	return map[string]any{
		"strategy":              cronProfileSeedStrategy(opts),
		"if_older_than":         cronDurationLiteral(after),
		"if_older_than_seconds": int64(after.Seconds()),
		"schedule":              cronProfileSeedSchedule(after),
	}
}

func cronDurationLiteral(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int64(d/time.Second))
	}
	return d.String()
}

func cronLockedCommand(lockPath, command string) string {
	quotedCommand := cronValue(command)
	return fmt.Sprintf("cdp_lock=%s; mkdir -p \"$(dirname \"$cdp_lock\")\"; cdp_flock=$(command -v flock 2>/dev/null || true); if [ -n \"$cdp_flock\" ]; then cdp_flock_close=''; \"$cdp_flock\" --help 2>&1 | grep -q -- '--close' && cdp_flock_close='--close'; \"$cdp_flock\" $cdp_flock_close -n \"$cdp_lock\" sh -c %s; elif mkdir \"$cdp_lock.dir\" 2>/dev/null; then trap 'rmdir \"$cdp_lock.dir\"' EXIT; %s; fi", lockPath, quotedCommand, command)
}

func cronValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, " \t\n'\"`;&|<>") {
		return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
	}
	return value
}

func readUserCrontab(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, crontabBinary(), "-l").CombinedOutput()
	return string(out), err
}

func writeUserCrontab(ctx context.Context, content string) error {
	tmp, err := os.CreateTemp("", "cdp-crontab-*.txt")
	if err != nil {
		return fmt.Errorf("create temporary crontab: %w", err)
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary crontab: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary crontab: %w", err)
	}
	if out, err := exec.CommandContext(ctx, crontabBinary(), path).CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func crontabBinary() string {
	if value := strings.TrimSpace(os.Getenv("CDP_CRONTAB_BIN")); value != "" {
		return value
	}
	return "crontab"
}

func isCrontabMissing(err error) bool {
	if err == nil {
		return false
	}
	var pathErr *exec.Error
	return errors.As(err, &pathErr) && errors.Is(pathErr.Err, exec.ErrNotFound)
}

func isEmptyCrontab(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "exit status 1")
}

func cronCommandError(action string, err error) error {
	return commandError("cron_failed", "usage", fmt.Sprintf("%s: %v", action, err), ExitUsage, []string{"cdp cron status --json", "cdp doctor --check scheduled-tasks --json"})
}

func extractCronManagedBlock(text string) cronBlockState {
	var state cronBlockState
	inBlock := false
	for _, chunk := range splitLinesPreserve(text) {
		line := strings.TrimRight(chunk, "\r\n")
		switch line {
		case cronManagedBlockStart:
			inBlock = true
			state.Installed = true
		case cronManagedBlockEnd:
			if inBlock {
				state.Text += chunk
				inBlock = false
			}
			continue
		}
		if inBlock {
			state.Text += chunk
			if line != cronManagedBlockStart && strings.TrimSpace(line) != "" {
				state.Entries = append(state.Entries, line)
			}
		}
	}
	return state
}

func withoutCronManagedBlock(text string) string {
	var out strings.Builder
	inBlock := false
	for _, chunk := range splitLinesPreserve(text) {
		line := strings.TrimRight(chunk, "\r\n")
		switch line {
		case cronManagedBlockStart:
			inBlock = true
			continue
		case cronManagedBlockEnd:
			if inBlock {
				inBlock = false
				continue
			}
		}
		if !inBlock {
			out.WriteString(chunk)
		}
	}
	return out.String()
}

func withoutLegacyPagesPollingCronEntries(text string) (string, []string) {
	var out strings.Builder
	var removed []string
	inBlock := false
	for _, chunk := range splitLinesPreserve(text) {
		line := strings.TrimRight(chunk, "\r\n")
		switch line {
		case cronManagedBlockStart:
			inBlock = true
		case cronManagedBlockEnd:
			if inBlock {
				inBlock = false
			}
		}
		if !inBlock && isLegacyPagesPollingCronLine(line) {
			removed = append(removed, strings.TrimSpace(line))
			continue
		}
		out.WriteString(chunk)
	}
	return out.String(), removed
}

func isLegacyPagesPollingCronLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "grep cdp") {
		return false
	}
	return scheduledTaskContainsCDPCommand(line, "pages")
}

func appendCronManagedBlock(base, block string) string {
	if strings.TrimSpace(base) == "" {
		return block
	}
	if !strings.HasSuffix(base, "\n") {
		base += "\n"
	}
	return base + block
}

func splitLinesPreserve(text string) []string {
	if text == "" {
		return nil
	}
	parts := strings.SplitAfter(text, "\n")
	if parts[len(parts)-1] == "" {
		return parts[:len(parts)-1]
	}
	return parts
}

func normalizeCronBlock(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

func cronDiffActions(current, wanted string, installed bool) []map[string]any {
	if current == wanted {
		return []map[string]any{{"action": "none", "reason": "installed block already matches intended block"}}
	}
	if installed {
		return []map[string]any{{"action": "replace_managed_block", "reason": "current managed block differs from intended block"}}
	}
	return []map[string]any{{"action": "append_managed_block", "reason": "no managed block is installed"}}
}

func cronInstallWarnings(opts cronRenderOptions, summary crontabSummary) []string {
	var warnings []string
	if strings.TrimSpace(opts.Profile) != "agent" {
		warnings = append(warnings, "only the agent profile is currently rendered; generated entries still use agent defaults")
	}
	if strings.TrimSpace(opts.CDPBin) == "" {
		warnings = append(warnings, "cdp binary path is empty")
	}
	if summary.HasPagesPollingKeepalive {
		warnings = append(warnings, "current crontab contains unmanaged cdp pages polling; cron install preserves unmanaged lines, so remove the manual pages loop after managed keepalive is verified")
	}
	return warnings
}

func cronStatusHealth(available, installed, matchesIntended bool, locks, daemonLocks map[string]any) map[string]any {
	staleLocks := cronStaleLockNames(locks)
	staleDaemonLocks := cronStaleLockNames(daemonLocks)
	staleLockCount := len(staleLocks) + len(staleDaemonLocks)
	issues := make([]map[string]any, 0, 3)
	state := "healthy"
	status := "pass"
	message := "cdp cron managed block is installed, current, and has no stale lock markers"
	recommendedCommand := "cdp doctor --check scheduled-tasks --json"
	nextCommands := []string{"cdp doctor --check scheduled-tasks --json", "cdp cron diff --json"}

	if !available {
		state = "crontab_unavailable"
		status = "warn"
		message = "user crontab command is unavailable; install or inspect crontab support before relying on managed cdp cron tasks"
		recommendedCommand = "cdp doctor --check scheduled-tasks --json"
		nextCommands = []string{recommendedCommand}
		issues = append(issues, map[string]any{
			"state":               "crontab_unavailable",
			"message":             "user crontab command is unavailable",
			"recommended_command": recommendedCommand,
		})
	} else if !installed {
		state = "not_installed"
		status = "warn"
		message = "cdp cron managed block is not installed"
		recommendedCommand = "cdp cron install --json"
		nextCommands = []string{recommendedCommand, "cdp cron diff --json", "cdp doctor --check scheduled-tasks --json"}
		issues = append(issues, map[string]any{
			"state":               "not_installed",
			"message":             "managed cron block is missing",
			"recommended_command": recommendedCommand,
		})
	} else if !matchesIntended {
		state = "needs_update"
		status = "warn"
		message = "installed cdp cron managed block differs from the current intended block"
		recommendedCommand = "cdp cron install --json"
		nextCommands = []string{recommendedCommand, "cdp cron diff --json", "cdp doctor --check scheduled-tasks --json"}
		issues = append(issues, map[string]any{
			"state":               "needs_update",
			"message":             "installed managed cron block differs from intended entries",
			"recommended_command": recommendedCommand,
		})
	}

	if staleLockCount > 0 {
		if state == "healthy" {
			state = "stale_locks"
			status = "warn"
			message = "cdp cron has stale lock markers that may block future scheduled ticks"
			recommendedCommand = cronRecommendedStaleLockCommand(locks, daemonLocks)
			nextCommands = []string{recommendedCommand, "cdp cron status --json", "cdp doctor --check scheduled-tasks --json"}
		}
		issues = append(issues, map[string]any{
			"state":                   "stale_locks",
			"message":                 "stale cron or daemon keepalive lock markers are present",
			"stale_lock_count":        len(staleLocks),
			"stale_locks":             staleLocks,
			"stale_daemon_lock_count": len(staleDaemonLocks),
			"stale_daemon_locks":      staleDaemonLocks,
			"recommended_command":     cronRecommendedStaleLockCommand(locks, daemonLocks),
		})
	}

	return map[string]any{
		"state":                   state,
		"status":                  status,
		"message":                 message,
		"installed":               installed,
		"matches_intended":        matchesIntended,
		"stale_lock_count":        len(staleLocks),
		"stale_locks":             staleLocks,
		"stale_daemon_lock_count": len(staleDaemonLocks),
		"stale_daemon_locks":      staleDaemonLocks,
		"issue_count":             len(issues),
		"issues":                  issues,
		"recommended_command":     recommendedCommand,
		"next_commands":           nextCommands,
	}
}

func cronStaleLockNames(locks map[string]any) []string {
	names := make([]string, 0)
	for name, raw := range locks {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		stale, _ := entry["stale"].(bool)
		if stale {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func cronRecommendedStaleLockCommand(locks, daemonLocks map[string]any) string {
	for _, lockSet := range []map[string]any{locks, daemonLocks} {
		for _, name := range cronStaleLockNames(lockSet) {
			entry, ok := lockSet[name].(map[string]any)
			if !ok {
				continue
			}
			commands, ok := entry["next_commands"].([]string)
			if ok && len(commands) > 0 {
				return commands[0]
			}
			if commandsAny, ok := entry["next_commands"].([]any); ok {
				for _, command := range commandsAny {
					if text, ok := command.(string); ok && strings.TrimSpace(text) != "" {
						return text
					}
				}
			}
			return cronLockRepairCommands(name)[0]
		}
	}
	return "cdp cron status --json"
}

func cronPagesPollingMigrationWarnings(changed, dryRun, managedKeepaliveInstalled bool) []string {
	if !changed {
		return nil
	}
	if !managedKeepaliveInstalled {
		return []string{"managed daemon keepalive is not installed; run cdp cron install --json and verify cdp cron status before applying this migration"}
	}
	if dryRun {
		return []string{"dry-run only; rerun with --apply after reviewing candidate entries"}
	}
	return nil
}

func cronPagesPollingMigrationNextCommands(changed, dryRun, managedKeepaliveInstalled bool) []string {
	if !changed {
		return []string{"cdp cron status --json", "cdp doctor --check scheduled-tasks --json"}
	}
	if !managedKeepaliveInstalled {
		return []string{"cdp cron install --json", "cdp cron status --json", "cdp cron migrate pages-polling --apply --json"}
	}
	if dryRun {
		return []string{"cdp cron migrate pages-polling --apply --json", "cdp doctor --check scheduled-tasks --json"}
	}
	return []string{"cdp cron status --json", "cdp doctor --check scheduled-tasks --json"}
}

func removedCount(changed, dryRun bool, count int) int {
	if changed && !dryRun {
		return count
	}
	return 0
}

func removedEntries(changed, dryRun bool, entries []string) []string {
	if changed && !dryRun {
		return entries
	}
	return []string{}
}

func stringSliceOrEmpty(entries []string) []string {
	if entries == nil {
		return []string{}
	}
	return entries
}

func cronTaskStatusSliceOrEmpty(entries []cronTaskStatus) []cronTaskStatus {
	if entries == nil {
		return []cronTaskStatus{}
	}
	return entries
}

func cronLockStates(stateDir string) map[string]any {
	locks := map[string]any{}
	for _, task := range managedCronTasks(defaultCronRenderOptions()) {
		path := filepath.Join(stateDir, "locks", task.LockName+".lock")
		locks[task.LockName] = cronLockStateEntry(task.LockName, path, 10*time.Minute)
	}
	for _, name := range legacyCronLockNames() {
		if _, ok := locks[name]; ok {
			continue
		}
		path := filepath.Join(stateDir, "locks", name+".lock")
		locks[name] = cronLockStateEntry(name, path, 10*time.Minute)
	}
	if _, ok := locks["cron-headed-heal"]; !ok {
		path := filepath.Join(stateDir, "locks", "cron-headed-heal.lock")
		locks["cron-headed-heal"] = cronLockStateEntry("cron-headed-heal", path, 10*time.Minute)
	}
	return locks
}

func legacyCronLockNames() []string {
	return []string{"keepalive-headless", "headless-health", "headless-profile-seed", "page-cleanup-headless"}
}

func cronDaemonLockStates(stateDir string) map[string]any {
	locks := map[string]any{}
	matches, err := filepath.Glob(filepath.Join(stateDir, "locks", "daemon-keepalive-*.lock"))
	if err != nil {
		return locks
	}
	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".lock")
		locks[name] = cronLockStateEntry(name, path, 10*time.Minute)
	}
	return locks
}

func cronLockStateEntry(name, path string, staleAfter time.Duration) map[string]any {
	info := daemon.InspectLock(path, staleAfter)
	entry := map[string]any{
		"path":   path,
		"exists": info.Exists,
		"stale":  info.Stale,
	}
	if !info.Exists {
		return entry
	}
	if info.Metadata.PID == 0 && strings.TrimSpace(info.Metadata.StartedAt) == "" && strings.TrimSpace(info.Metadata.Name) == "" {
		entry["stale"] = false
		entry["marker"] = "flock_lockfile"
		if info.ModifiedAt != "" {
			entry["modified_at"] = info.ModifiedAt
		}
		if locked, known := cronFlockMarkerLocked(path); known {
			entry["locked"] = locked
			if locked {
				if owner, ownerKnown := cronFlockOwnerForPath(path); ownerKnown {
					if owner.PID > 0 {
						entry["lock_owner_pid"] = owner.PID
					}
					if owner.Age > 0 {
						entry["lock_owner_age_seconds"] = int64(owner.Age.Seconds())
					}
					if staleAfter > 0 && owner.Age > staleAfter {
						entry["stale"] = true
						entry["stale_reason"] = "flock_lock_held_too_long"
						entry["next_commands"] = cronHeldFlockRepairCommands(name)
					}
				}
			}
		}
		return entry
	}
	if info.ModifiedAt != "" {
		entry["modified_at"] = info.ModifiedAt
	}
	if info.StaleReason != "" {
		entry["stale_reason"] = info.StaleReason
		entry["next_commands"] = cronLockRepairCommands(name)
	}
	if info.Metadata.Name != "" {
		entry["name"] = info.Metadata.Name
	}
	if info.Metadata.PID > 0 {
		entry["pid"] = info.Metadata.PID
	}
	if info.Metadata.StartedAt != "" {
		entry["started_at"] = info.Metadata.StartedAt
	}
	if info.Metadata.Phase != "" {
		entry["phase"] = info.Metadata.Phase
	}
	if info.OwnerRunning != nil {
		entry["owner_running"] = *info.OwnerRunning
	}
	return entry
}

func cronFlockMarkerLocked(path string) (bool, bool) {
	flock, err := exec.LookPath("flock")
	if err != nil {
		return false, false
	}
	err = exec.Command(flock, "-n", path, "true").Run()
	if err == nil {
		return false, true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return true, true
	}
	return false, false
}

func cronFlockOwnerFromProcLocks(path string) (cronFlockOwner, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return cronFlockOwner{}, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return cronFlockOwner{}, false
	}
	device := procLocksDevice(uint64(stat.Dev))
	inode := strconv.FormatUint(stat.Ino, 10)
	file, err := os.Open("/proc/locks")
	if err != nil {
		return cronFlockOwner{}, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || fields[1] != "FLOCK" {
			continue
		}
		lockRef := strings.Split(fields[5], ":")
		if len(lockRef) != 3 || lockRef[0]+":"+lockRef[1] != device || lockRef[2] != inode {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil || pid <= 0 {
			return cronFlockOwner{}, false
		}
		age, _ := processAge(pid)
		return cronFlockOwner{PID: pid, Age: age}, true
	}
	return cronFlockOwner{}, false
}

func procLocksDevice(dev uint64) string {
	major := (dev >> 8) & 0xfff
	minor := (dev & 0xff) | ((dev >> 12) & 0xfff00)
	return fmt.Sprintf("%x:%02x", major, minor)
}

func processAge(pid int) (time.Duration, bool) {
	statPath := filepath.Join("/proc", strconv.Itoa(pid), "stat")
	b, err := os.ReadFile(statPath)
	if err != nil {
		return 0, false
	}
	text := string(b)
	end := strings.LastIndex(text, ")")
	if end < 0 || end+2 >= len(text) {
		return 0, false
	}
	fields := strings.Fields(text[end+2:])
	if len(fields) < 20 {
		return 0, false
	}
	startTicks, err := strconv.ParseFloat(fields[19], 64)
	if err != nil {
		return 0, false
	}
	uptimeBytes, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, false
	}
	uptimeFields := strings.Fields(string(uptimeBytes))
	if len(uptimeFields) == 0 {
		return 0, false
	}
	uptimeSeconds, err := strconv.ParseFloat(uptimeFields[0], 64)
	if err != nil {
		return 0, false
	}
	const ticksPerSecond = 100
	ageSeconds := uptimeSeconds - startTicks/ticksPerSecond
	if ageSeconds <= 0 {
		return 0, true
	}
	return time.Duration(ageSeconds * float64(time.Second)), true
}

func cronLockRepairCommands(name string) []string {
	if strings.Contains(name, "headless") {
		return []string{"cdp --browser-mode headless daemon maintenance --stale-lock-after 1s --json", "cdp cron status --json"}
	}
	if strings.Contains(name, "headed") {
		return []string{"cdp --browser-mode headed daemon keepalive --auto-connect --repair --probe passive --stale-lock-after 1s --json", "cdp cron status --json"}
	}
	return []string{"cdp daemon keepalive --repair --stale-lock-after 1s --json", "cdp cron status --json"}
}

func cronHeldFlockRepairCommands(name string) []string {
	if strings.Contains(name, "headless") {
		return []string{"cdp --browser-mode headless daemon stop --json", "cdp --browser-mode headless daemon maintenance --json", "cdp cron status --json"}
	}
	if strings.Contains(name, "headed") {
		return []string{"cdp --browser-mode headed daemon stop --json", "cdp --browser-mode headed daemon keepalive --auto-connect --repair --probe passive --json", "cdp cron status --json"}
	}
	return []string{"cdp daemon stop --json", "cdp daemon keepalive --repair --json", "cdp cron status --json"}
}

func cronLastRunArtifacts(stateDir string) map[string]any {
	paths := map[string]string{
		"headed_heal_summary":           filepath.Join(stateDir, "headed-heal", "latest.json"),
		"headless_maintenance_summary":  filepath.Join(stateDir, "headless-maintenance", "latest.json"),
		"headless_profile_seed_summary": profileSeedStatusPath(stateDir),
		"headless_keepalive_log":        filepath.Join(stateDir, "keepalive-headless.log"),
		"headless_health_log":           filepath.Join(stateDir, "headless-health.log"),
		"headless_profile_seed_log":     filepath.Join(stateDir, "profile-seed-headless.log"),
		"headless_page_cleanup_log":     filepath.Join(stateDir, "page-cleanup-headless.log"),
	}
	for _, task := range managedCronTasks(defaultCronRenderOptions()) {
		if strings.TrimSpace(task.LogArtifactKey) == "" || strings.TrimSpace(task.LogName) == "" {
			continue
		}
		paths[task.LogArtifactKey] = filepath.Join(stateDir, task.LogName)
	}
	out := map[string]any{}
	for name, path := range paths {
		info, err := os.Stat(path)
		entry := map[string]any{"path": path, "exists": err == nil}
		if err == nil {
			entry["size_bytes"] = info.Size()
			entry["modified_at"] = info.ModTime().UTC().Format(time.RFC3339)
		}
		out[name] = entry
	}
	return out
}

func actionString(changed bool, changedAction, unchangedAction string) string {
	if changed {
		return changedAction
	}
	return unchangedAction
}

func runtimeOrZero(status daemon.Status) daemon.Runtime {
	if status.Runtime == nil {
		return daemon.Runtime{}
	}
	return *status.Runtime
}
