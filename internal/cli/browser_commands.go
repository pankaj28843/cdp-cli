package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
)

const profileSeedStatusSchemaVersion = "cdp-profile-seed-status/v1"

func (a *app) newBrowserCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browser",
		Short: "Inspect and prepare browser runtime modes",
	}
	cmd.AddCommand(a.newBrowserPreflightCommand())
	cmd.AddCommand(a.newBrowserModeCommand())
	cmd.AddCommand(a.newBrowserProfileCommand())
	return cmd
}

func (a *app) newBrowserModeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mode",
		Short: "Inspect the selected browser runtime mode",
	}
	cmd.AddCommand(a.newBrowserModeGetCommand())
	return cmd
}

func (a *app) newBrowserModeGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the effective browser runtime mode",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			mode, err := a.resolveBrowserMode(cmd)
			if err != nil {
				return err
			}

			selected, err := a.selectedConnectionSummary(ctx)
			if err != nil {
				return err
			}

			data := map[string]any{
				"ok":                  true,
				"browser_mode":        mode.Mode,
				"browser_mode_source": mode.Source,
				"config_path":         mode.ConfigPath,
				"next_commands":       mode.NextCommands,
			}
			if len(mode.Warnings) > 0 {
				data["warnings"] = mode.Warnings
			}
			if selected != nil {
				data["selected_connection"] = selected
			}

			return a.render(ctx, fmt.Sprintf("browser mode %s (%s)", mode.Mode, mode.Source), data)
		},
	}
}

func (a *app) newBrowserProfileCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Inspect and seed managed headless browser profiles",
		Long:  "Inspect and seed the cdp-owned managed profile used by --browser-mode headless. The managed strategy creates an empty owner-only profile; copy-default is an explicit local full-state snapshot of Chrome's default profile for developer-controlled authenticated automation.",
	}
	cmd.AddCommand(a.newBrowserProfileStatusCommand())
	cmd.AddCommand(a.newBrowserProfileSeedCommand())
	return cmd
}

func (a *app) newBrowserProfileStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show managed headless browser profile status",
		Long:  "Show metadata-only status for the cdp-owned managed headless profile, including owner-only permission checks and next commands without dumping copied profile file values.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			status, err := a.browserProfileStatus(ctx)
			if err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("browser profile %s", status.State), status)
		},
	}
}

func (a *app) newBrowserProfileSeedCommand() *cobra.Command {
	var strategy string
	var ifOlderThan time.Duration
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Create managed headless browser profile metadata",
		Long:  "Create the cdp-owned managed profile metadata for --browser-mode headless. Use --strategy managed for an empty owner-only profile, or --strategy copy-default to fully replace the managed profile with a local full-state snapshot of Chrome's default profile. copy-default preserves developer browser-state files such as cookies, Local Storage, IndexedDB, extensions, history, and cache in the local managed profile. If headless is running, copy-default stops the headless daemon and owned managed Chrome, seeds the profile, then starts headless again. Normal JSON output reports metadata and counts rather than file values. Preserving files does not guarantee every platform-encrypted cookie or credential is usable from the copied headless profile; use headed mode for the live default-profile session.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			strategy = browser.NormalizeProfileSeedStrategy(strategy)
			if !browser.SupportedProfileSeedStrategy(strategy) {
				return commandError(
					"invalid_profile_seed_strategy",
					"usage",
					"--strategy must be managed or copy-default",
					ExitUsage,
					[]string{"cdp browser profile seed --strategy managed --json", "cdp browser profile seed --strategy copy-default --json"},
				)
			}

			if ifOlderThan < 0 {
				return commandError(
					"invalid_profile_seed_age",
					"usage",
					"--if-older-than must be non-negative",
					ExitUsage,
					[]string{"cdp browser profile seed --strategy copy-default --if-older-than 6h --json"},
				)
			}

			human, status, err := a.runBrowserProfileSeed(ctx, browserProfileSeedOptions{
				Strategy:    strategy,
				IfOlderThan: ifOlderThan,
				Now:         time.Now().UTC(),
			})
			if err != nil {
				return err
			}
			return a.render(ctx, human, status)
		},
	}
	cmd.Flags().StringVar(&strategy, "strategy", "managed", "profile seed strategy: managed or copy-default")
	cmd.Flags().DurationVar(&ifOlderThan, "if-older-than", 0, "skip seeding when existing metadata is newer than this duration")
	return cmd
}

type browserProfileSeedOptions struct {
	Strategy    string
	IfOlderThan time.Duration
	Now         time.Time
}

func (a *app) runBrowserProfileSeed(ctx context.Context, opts browserProfileSeedOptions) (string, browserProfileStatus, error) {
	store, err := a.stateStore()
	if err != nil {
		return "", browserProfileStatus{}, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if opts.Strategy == browser.ProfileSeedStrategyCopyDefault && opts.IfOlderThan > 0 {
		if existing, skipped, seedAgeSeconds, err := recentManagedProfileSeed(store.Dir, opts.Strategy, now, opts.IfOlderThan); err != nil {
			a.recordBrowserProfileSeedFailure(ctx, store.Dir, opts, now, "freshness_check_failed")
			return "", browserProfileStatus{}, err
		} else if skipped {
			status, err := browserProfileStatusForStore(ctx, store.Dir)
			if err != nil {
				a.recordBrowserProfileSeedFailure(ctx, store.Dir, opts, now, "status_failed")
				return "", browserProfileStatus{}, err
			}
			status.Seeded = true
			status.ManagedBrowser = browser.ManagedMetadataStatus(existing)
			status.SeedAction = "skipped"
			status.SeedIntervalSeconds = int64(opts.IfOlderThan.Seconds())
			status.SeedAgeSeconds = seedAgeSeconds
			if err := recordBrowserProfileSeedStatus(ctx, store.Dir, &status, now); err != nil {
				return "", browserProfileStatus{}, err
			}
			return "browser profile skipped", status, nil
		}
	}
	var maintenance *profileSeedMaintenance
	var seedResourcePreflight *resourcePreflightResult
	if opts.Strategy == browser.ProfileSeedStrategyCopyDefault {
		seedMaintenance, resourcePreflight, err := a.prepareCopyDefaultProfileSeedMaintenance(ctx, store.Dir)
		if err != nil {
			a.recordBrowserProfileSeedFailure(ctx, store.Dir, opts, now, "maintenance_preflight_failed")
			return "", browserProfileStatus{}, err
		}
		maintenance = &seedMaintenance
		seedResourcePreflight = &resourcePreflight
		if !resourcePreflight.HeavyWorkAllowed {
			status, err := browserProfileStatusForStore(ctx, store.Dir)
			if err != nil {
				return "", browserProfileStatus{}, err
			}
			status.State = "resource_blocked"
			status.SeedStrategy = opts.Strategy
			status.SeedAction = "skipped_resource_preflight"
			status.ResourcePreflight = &resourcePreflight
			status.Maintenance = maintenance
			status.NextCommands = uniqueCommands(status.NextCommands, resourcePreflight.NextCommands, []string{"cdp --browser-mode headless browser profile seed --strategy copy-default --json"})
			if err := recordBrowserProfileSeedStatus(ctx, store.Dir, &status, now); err != nil {
				return "", browserProfileStatus{}, err
			}
			return "browser profile skipped", status, nil
		}
		if err := a.stopHeadlessForProfileSeed(ctx, store.Dir, maintenance); err != nil {
			a.recordBrowserProfileSeedFailure(ctx, store.Dir, opts, now, "stop_failed")
			return "", browserProfileStatus{}, err
		}
	}
	metadata, skipped, seedAgeSeconds, err := prepareManagedProfileWithAgeGate(store.Dir, opts.Strategy, now, opts.IfOlderThan)
	if err != nil {
		a.recordBrowserProfileSeedFailure(ctx, store.Dir, opts, now, "seed_failed")
		return "", browserProfileStatus{}, err
	}
	if maintenance != nil && maintenance.RestartRequested && !skipped {
		if err := a.healHeadlessAfterProfileSeed(ctx, store.Dir, maintenance); err != nil {
			a.recordBrowserProfileSeedFailure(ctx, store.Dir, opts, now, "heal_failed")
			return "", browserProfileStatus{}, err
		}
	}
	status, err := browserProfileStatusForStore(ctx, store.Dir)
	if err != nil {
		a.recordBrowserProfileSeedFailure(ctx, store.Dir, opts, now, "status_failed")
		return "", browserProfileStatus{}, err
	}
	status.Seeded = true
	status.ManagedBrowser = browser.ManagedMetadataStatus(metadata)
	if maintenance != nil {
		status.Maintenance = maintenance
	}
	if seedResourcePreflight != nil {
		status.ResourcePreflight = seedResourcePreflight
	}
	if skipped {
		status.SeedAction = "skipped"
	} else {
		status.SeedAction = "seeded"
	}
	if opts.IfOlderThan > 0 {
		status.SeedIntervalSeconds = int64(opts.IfOlderThan.Seconds())
		status.SeedAgeSeconds = seedAgeSeconds
	}
	if err := recordBrowserProfileSeedStatus(ctx, store.Dir, &status, now); err != nil {
		return "", browserProfileStatus{}, err
	}
	return "browser profile " + status.SeedAction, status, nil
}

func prepareManagedProfileWithAgeGate(stateDir, strategy string, now time.Time, ifOlderThan time.Duration) (browser.ManagedMetadata, bool, int64, error) {
	if ifOlderThan <= 0 {
		metadata, err := browser.PrepareManagedProfileWithStrategy(stateDir, strategy, now)
		return metadata, false, 0, err
	}
	if metadata, skipped, age, err := recentManagedProfileSeed(stateDir, strategy, now, ifOlderThan); err != nil || skipped {
		return metadata, skipped, age, err
	}
	metadata, err := browser.PrepareManagedProfileWithStrategy(stateDir, strategy, now)
	return metadata, false, 0, err
}

func recentManagedProfileSeed(stateDir, strategy string, now time.Time, ifOlderThan time.Duration) (browser.ManagedMetadata, bool, int64, error) {
	metadata, ok, err := browser.LoadManagedMetadata(stateDir)
	if err != nil {
		return browser.ManagedMetadata{}, false, 0, err
	}
	if ok && browser.NormalizeProfileSeedStrategy(metadata.ProfileSeedStrategy) == strategy {
		if lastSeededAt, err := time.Parse(time.RFC3339, metadata.LastSeededAt); err == nil {
			age := now.Sub(lastSeededAt)
			if age >= 0 && age < ifOlderThan {
				return metadata, true, int64(age.Seconds()), nil
			}
		}
	}
	return browser.ManagedMetadata{}, false, 0, nil
}

func (a *app) prepareCopyDefaultProfileSeedMaintenance(ctx context.Context, stateDir string) (profileSeedMaintenance, resourcePreflightResult, error) {
	maintenance := profileSeedMaintenance{}
	status := daemon.Status{BrowserMode: string(config.BrowserModeHeadless)}
	if runtime, ok, err := daemon.LoadRuntimeForMode(ctx, stateDir, string(config.BrowserModeHeadless)); err != nil {
		return maintenance, resourcePreflightResult{}, err
	} else if ok {
		status.Runtime = &runtime
		if daemon.RuntimeRunning(runtime) {
			maintenance.WasRunning = true
			maintenance.RuntimeWasRunning = true
			maintenance.RestartRequested = true
		}
	}
	sweep, err := browser.ReconcileManagedProcesses(ctx, stateDir, browser.ManagedProcessReconcileOptions{
		ActivePID:  managedChromeActivePID(status),
		ReapExtras: true,
	})
	if err != nil {
		sweep = browser.ManagedProcessReconcileResult{
			Checked:      true,
			State:        "error",
			BrowserMode:  string(config.BrowserModeHeadless),
			Reason:       err.Error(),
			NextCommands: []string{"cdp --browser-mode headless daemon stop --force-managed --json", "cdp --browser-mode headless daemon keepalive --managed-process-sweep --repair --force --json"},
		}
	}
	maintenance.ManagedProcessSweep = &sweep
	if sweep.LiveCount > 0 {
		maintenance.WasRunning = true
		maintenance.ManagedBrowserWasRunning = true
	}
	if launch, ok, err := browser.ReuseManagedChrome(ctx, stateDir); err != nil {
		return maintenance, resourcePreflightResult{}, err
	} else if ok {
		maintenance.WasRunning = true
		maintenance.ManagedBrowserWasRunning = true
		maintenance.RestartRequested = true
		managed := browser.ManagedMetadataStatus(launch.Metadata)
		maintenance.ManagedBrowser = &managed
	}
	resourcePreflight := a.maintenanceResourcePreflightForStateWithManaged(ctx, stateDir, status, nil, &sweep)
	return maintenance, resourcePreflight, nil
}

func (a *app) stopHeadlessForProfileSeed(ctx context.Context, stateDir string, maintenance *profileSeedMaintenance) error {
	if maintenance == nil || !maintenance.WasRunning {
		return nil
	}
	if maintenance.RuntimeWasRunning {
		if _, stopped, err := daemon.StopRuntimeForMode(ctx, stateDir, string(config.BrowserModeHeadless)); err != nil {
			return err
		} else {
			maintenance.DaemonStopped = stopped
		}
	}
	if maintenance.ManagedBrowserWasRunning {
		managedStop, err := browser.StopManagedChrome(ctx, stateDir, browser.ManagedStopOptions{Force: true})
		if err != nil {
			return err
		}
		maintenance.ManagedStop = &managedStop
		maintenance.ManagedBrowserStopped = managedStop.Stopped
	}
	return nil
}

func (a *app) healHeadlessAfterProfileSeed(ctx context.Context, stateDir string, maintenance *profileSeedMaintenance) error {
	launch, err := browser.StartManagedChrome(ctx, browser.ManagedOptions{StateDir: stateDir})
	if err != nil {
		return err
	}
	managed := browser.ManagedMetadataStatus(launch.Metadata)
	maintenance.ManagedBrowser = &managed

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	_, reused, err := daemon.StartKeepAliveForModeWithMetadata(ctx, executable, stateDir, "headless", launch.Endpoint, "browser_url", daemon.KeepAliveMetadata{
		UserDataDir:         launch.Metadata.UserDataDir,
		ManagedBrowser:      &managed,
		ManagedProfilePath:  launch.Metadata.UserDataDir,
		ProfileSeedStrategy: launch.Metadata.ProfileSeedStrategy,
		ChromePID:           launch.Metadata.ChromePID,
		ChromePort:          launch.Metadata.DebuggingPort,
	}, 30*time.Second)
	if err != nil {
		return err
	}
	maintenance.Healed = true
	if reused {
		maintenance.HealAction = "reused"
	} else {
		maintenance.HealAction = "started"
	}
	return nil
}

type browserProfileStatus struct {
	OK                  bool                     `json:"ok"`
	BrowserMode         string                   `json:"browser_mode"`
	StateDir            string                   `json:"state_dir"`
	ProfileDir          string                   `json:"profile_dir"`
	MetadataPath        string                   `json:"metadata_path"`
	State               string                   `json:"state"`
	Exists              bool                     `json:"exists"`
	Seeded              bool                     `json:"seeded"`
	ProfilePerm         string                   `json:"profile_perm,omitempty"`
	MetadataPerm        string                   `json:"metadata_perm,omitempty"`
	SeedStrategy        string                   `json:"seed_strategy,omitempty"`
	LastSeededAt        string                   `json:"last_seeded_at,omitempty"`
	LastLaunchAt        string                   `json:"last_launch_at,omitempty"`
	ManagedBrowser      browser.ManagedStatus    `json:"managed_browser,omitempty"`
	Warnings            []string                 `json:"warnings,omitempty"`
	Maintenance         *profileSeedMaintenance  `json:"maintenance,omitempty"`
	SeedAction          string                   `json:"seed_action,omitempty"`
	SeedAgeSeconds      int64                    `json:"seed_age_seconds,omitempty"`
	SeedIntervalSeconds int64                    `json:"seed_interval_seconds,omitempty"`
	ResourcePreflight   *resourcePreflightResult `json:"resource_preflight,omitempty"`
	SeedStatusPath      string                   `json:"seed_status_path"`
	LastSeed            *profileSeedStatus       `json:"last_seed,omitempty"`
	NextCommands        []string                 `json:"next_commands"`
}

type profileSeedMaintenance struct {
	WasRunning               bool                                   `json:"was_running"`
	RuntimeWasRunning        bool                                   `json:"runtime_was_running,omitempty"`
	ManagedBrowserWasRunning bool                                   `json:"managed_browser_was_running,omitempty"`
	RestartRequested         bool                                   `json:"restart_requested,omitempty"`
	DaemonStopped            bool                                   `json:"daemon_stopped,omitempty"`
	ManagedBrowserStopped    bool                                   `json:"managed_browser_stopped,omitempty"`
	ManagedProcessSweep      *browser.ManagedProcessReconcileResult `json:"managed_process_sweep,omitempty"`
	ManagedStop              *browser.ManagedStopResult             `json:"managed_stop,omitempty"`
	Healed                   bool                                   `json:"healed,omitempty"`
	HealAction               string                                 `json:"heal_action,omitempty"`
	ManagedBrowser           *browser.ManagedStatus                 `json:"managed_browser,omitempty"`
}

type profileSeedStatus struct {
	Path                 string                        `json:"path,omitempty"`
	Exists               bool                          `json:"exists,omitempty"`
	ModifiedAt           string                        `json:"modified_at,omitempty"`
	SchemaVersion        string                        `json:"schema_version"`
	OK                   bool                          `json:"ok"`
	BrowserMode          string                        `json:"browser_mode"`
	Status               string                        `json:"status"`
	State                string                        `json:"state"`
	SeedStrategy         string                        `json:"seed_strategy"`
	SeedAction           string                        `json:"seed_action"`
	CheckedAt            string                        `json:"checked_at"`
	LastSeededAt         string                        `json:"last_seeded_at,omitempty"`
	SeedAgeSeconds       int64                         `json:"seed_age_seconds,omitempty"`
	SeedIntervalSeconds  int64                         `json:"seed_interval_seconds,omitempty"`
	Fresh                bool                          `json:"fresh,omitempty"`
	DefaultProfileCopied bool                          `json:"default_profile_copied,omitempty"`
	CopiedFileCount      int                           `json:"copied_file_count,omitempty"`
	ResourcePreflight    *profileSeedResourceStatus    `json:"resource_preflight,omitempty"`
	Maintenance          *profileSeedMaintenanceStatus `json:"maintenance,omitempty"`
	Failure              string                        `json:"failure,omitempty"`
	NextCommands         []string                      `json:"next_commands,omitempty"`
}

type profileSeedResourceStatus struct {
	Checked          bool     `json:"checked"`
	State            string   `json:"state"`
	Status           string   `json:"status"`
	HeavyWorkAllowed bool     `json:"heavy_work_allowed"`
	Reasons          []string `json:"reasons,omitempty"`
}

type profileSeedMaintenanceStatus struct {
	WasRunning               bool                            `json:"was_running"`
	RuntimeWasRunning        bool                            `json:"runtime_was_running,omitempty"`
	ManagedBrowserWasRunning bool                            `json:"managed_browser_was_running,omitempty"`
	RestartRequested         bool                            `json:"restart_requested,omitempty"`
	DaemonStopped            bool                            `json:"daemon_stopped,omitempty"`
	ManagedBrowserStopped    bool                            `json:"managed_browser_stopped,omitempty"`
	Healed                   bool                            `json:"healed,omitempty"`
	HealAction               string                          `json:"heal_action,omitempty"`
	ManagedProcessSweep      *profileSeedManagedProcessState `json:"managed_process_sweep,omitempty"`
	ManagedStop              *profileSeedManagedStopState    `json:"managed_stop,omitempty"`
}

type profileSeedManagedProcessState struct {
	Checked         bool   `json:"checked"`
	State           string `json:"state"`
	RegisteredCount int    `json:"registered_count"`
	LiveCount       int    `json:"live_count"`
	StaleCount      int    `json:"stale_count"`
	ReapedCount     int    `json:"reaped_count"`
	Reason          string `json:"reason,omitempty"`
}

type profileSeedManagedStopState struct {
	Checked bool   `json:"checked"`
	Stopped bool   `json:"stopped"`
	Skipped bool   `json:"skipped"`
	Force   bool   `json:"force,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func (a *app) browserProfileStatus(ctx context.Context) (browserProfileStatus, error) {
	store, err := a.stateStore()
	if err != nil {
		return browserProfileStatus{}, err
	}
	return browserProfileStatusForStore(ctx, store.Dir)
}

func browserProfileStatusForStore(ctx context.Context, stateDir string) (browserProfileStatus, error) {
	select {
	case <-ctx.Done():
		return browserProfileStatus{}, ctx.Err()
	default:
	}

	status := browserProfileStatus{
		OK:             true,
		BrowserMode:    "headless",
		StateDir:       stateDir,
		ProfileDir:     browser.ManagedProfileDir(stateDir),
		MetadataPath:   browser.ManagedMetadataPath(stateDir),
		SeedStatusPath: profileSeedStatusPath(stateDir),
		State:          "missing",
		SeedStrategy:   browser.ProfileSeedStrategyManaged,
		NextCommands:   browserProfileNextCommands(false),
	}

	if lastSeed, ok, err := loadProfileSeedStatus(stateDir); err == nil && ok {
		status.LastSeed = &lastSeed
	} else if err != nil {
		status.Warnings = append(status.Warnings, "profile seed status artifact is unreadable")
	}

	profileInfo, profileErr := os.Stat(status.ProfileDir)
	if profileErr == nil {
		status.Exists = true
		status.ProfilePerm = fmt.Sprintf("%03o", profileInfo.Mode().Perm())
		if profileInfo.Mode().Perm() != 0o700 {
			status.Warnings = append(status.Warnings, "managed profile permissions should be 0700")
		}
	} else if !os.IsNotExist(profileErr) {
		return browserProfileStatus{}, fmt.Errorf("stat managed profile: %w", profileErr)
	}

	metadataInfo, metadataErr := os.Stat(status.MetadataPath)
	if metadataErr == nil {
		status.MetadataPerm = fmt.Sprintf("%03o", metadataInfo.Mode().Perm())
		if metadataInfo.Mode().Perm() != 0o600 {
			status.Warnings = append(status.Warnings, "managed metadata permissions should be 0600")
		}
	} else if !os.IsNotExist(metadataErr) {
		return browserProfileStatus{}, fmt.Errorf("stat managed metadata: %w", metadataErr)
	}

	metadata, ok, err := browser.LoadManagedMetadata(stateDir)
	if err != nil {
		return browserProfileStatus{}, err
	}
	if ok {
		status.Seeded = true
		status.SeedStrategy = metadata.ProfileSeedStrategy
		status.LastSeededAt = metadata.LastSeededAt
		status.LastLaunchAt = metadata.StartedAt
		status.ManagedBrowser = browser.ManagedMetadataStatus(metadata)
	}

	switch {
	case status.Exists && status.Seeded:
		status.State = "ready"
	case status.Exists:
		status.State = "profile_exists"
	case status.Seeded:
		status.State = "metadata_only"
	default:
		status.State = "missing"
	}
	status.NextCommands = browserProfileNextCommands(status.Seeded)
	return status, nil
}

func profileSeedStatusPath(stateDir string) string {
	return filepath.Join(stateDir, "profile-seed", "latest.json")
}

func recordBrowserProfileSeedStatus(ctx context.Context, stateDir string, status *browserProfileStatus, checkedAt time.Time) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	summary := profileSeedStatusFromProfileStatus(stateDir, *status, checkedAt)
	if err := writeProfileSeedStatus(summary.Path, summary); err != nil {
		return err
	}
	status.SeedStatusPath = summary.Path
	status.LastSeed = &summary
	return nil
}

func (a *app) recordBrowserProfileSeedFailure(ctx context.Context, stateDir string, opts browserProfileSeedOptions, checkedAt time.Time, failure string) {
	select {
	case <-ctx.Done():
		return
	default:
	}
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	strategy := browser.NormalizeProfileSeedStrategy(opts.Strategy)
	if !browser.SupportedProfileSeedStrategy(strategy) {
		strategy = browser.ProfileSeedStrategyManaged
	}
	summary := profileSeedStatus{
		Path:          profileSeedStatusPath(stateDir),
		Exists:        true,
		SchemaVersion: profileSeedStatusSchemaVersion,
		OK:            false,
		BrowserMode:   "headless",
		Status:        "fail",
		State:         "failed",
		SeedStrategy:  strategy,
		SeedAction:    "failed",
		CheckedAt:     checkedAt.UTC().Format(time.RFC3339),
		Failure:       failure,
		NextCommands:  browserProfileNextCommands(false),
	}
	if opts.IfOlderThan > 0 {
		summary.SeedIntervalSeconds = int64(opts.IfOlderThan.Seconds())
	}
	_ = writeProfileSeedStatus(summary.Path, summary)
}

func profileSeedStatusFromProfileStatus(stateDir string, status browserProfileStatus, checkedAt time.Time) profileSeedStatus {
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	strategy := status.SeedStrategy
	if strategy == "" {
		strategy = status.ManagedBrowser.ProfileSeedStrategy
	}
	strategy = browser.NormalizeProfileSeedStrategy(strategy)
	action := status.SeedAction
	if action == "" {
		action = "inspected"
	}
	lastSeededAt := status.LastSeededAt
	if lastSeededAt == "" {
		lastSeededAt = status.ManagedBrowser.LastSeededAt
	}
	summary := profileSeedStatus{
		Path:                 profileSeedStatusPath(stateDir),
		Exists:               true,
		SchemaVersion:        profileSeedStatusSchemaVersion,
		OK:                   status.OK,
		BrowserMode:          status.BrowserMode,
		Status:               profileSeedArtifactStatus(status),
		State:                profileSeedArtifactState(status),
		SeedStrategy:         strategy,
		SeedAction:           action,
		CheckedAt:            checkedAt.UTC().Format(time.RFC3339),
		LastSeededAt:         lastSeededAt,
		SeedAgeSeconds:       status.SeedAgeSeconds,
		SeedIntervalSeconds:  status.SeedIntervalSeconds,
		Fresh:                action == "skipped",
		DefaultProfileCopied: status.ManagedBrowser.DefaultProfileCopied,
		CopiedFileCount:      status.ManagedBrowser.CopiedFileCount,
		NextCommands:         append([]string(nil), status.NextCommands...),
	}
	if status.ResourcePreflight != nil {
		summary.ResourcePreflight = &profileSeedResourceStatus{
			Checked:          status.ResourcePreflight.Checked,
			State:            status.ResourcePreflight.State,
			Status:           status.ResourcePreflight.Status,
			HeavyWorkAllowed: status.ResourcePreflight.HeavyWorkAllowed,
			Reasons:          append([]string(nil), status.ResourcePreflight.Reasons...),
		}
	}
	if status.Maintenance != nil {
		summary.Maintenance = profileSeedMaintenanceSummary(status.Maintenance)
	}
	return summary
}

func profileSeedArtifactStatus(status browserProfileStatus) string {
	if !status.OK {
		return "fail"
	}
	switch status.SeedAction {
	case "seeded":
		return "pass"
	case "skipped", "skipped_resource_preflight":
		return "skip"
	default:
		return "pass"
	}
}

func profileSeedArtifactState(status browserProfileStatus) string {
	if !status.OK {
		return "failed"
	}
	switch status.SeedAction {
	case "skipped":
		return "fresh"
	case "skipped_resource_preflight":
		return "resource_blocked"
	default:
		if status.State != "" {
			return status.State
		}
		return "unknown"
	}
}

func profileSeedMaintenanceSummary(maintenance *profileSeedMaintenance) *profileSeedMaintenanceStatus {
	if maintenance == nil {
		return nil
	}
	summary := &profileSeedMaintenanceStatus{
		WasRunning:               maintenance.WasRunning,
		RuntimeWasRunning:        maintenance.RuntimeWasRunning,
		ManagedBrowserWasRunning: maintenance.ManagedBrowserWasRunning,
		RestartRequested:         maintenance.RestartRequested,
		DaemonStopped:            maintenance.DaemonStopped,
		ManagedBrowserStopped:    maintenance.ManagedBrowserStopped,
		Healed:                   maintenance.Healed,
		HealAction:               maintenance.HealAction,
	}
	if maintenance.ManagedProcessSweep != nil {
		sweep := maintenance.ManagedProcessSweep
		summary.ManagedProcessSweep = &profileSeedManagedProcessState{
			Checked:         sweep.Checked,
			State:           sweep.State,
			RegisteredCount: sweep.RegisteredCount,
			LiveCount:       sweep.LiveCount,
			StaleCount:      sweep.StaleCount,
			ReapedCount:     sweep.ReapedCount,
			Reason:          sweep.Reason,
		}
	}
	if maintenance.ManagedStop != nil {
		stop := maintenance.ManagedStop
		summary.ManagedStop = &profileSeedManagedStopState{
			Checked: stop.Checked,
			Stopped: stop.Stopped,
			Skipped: stop.Skipped,
			Force:   stop.Force,
			Reason:  stop.Reason,
		}
	}
	return summary
}

func writeProfileSeedStatus(path string, summary profileSeedStatus) error {
	payload, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return commandError("artifact_write_failed", "internal", fmt.Sprintf("marshal profile seed status: %v", err), ExitInternal, []string{"cdp --browser-mode headless browser profile seed --strategy managed --json"})
	}
	payload = append(payload, '\n')
	cleanPath := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o700); err != nil {
		return commandError("artifact_write_failed", "io", fmt.Sprintf("create profile seed status directory: %v", err), ExitInternal, []string{"cdp --browser-mode headless browser profile seed --strategy managed --json"})
	}
	if err := os.WriteFile(cleanPath, payload, 0o600); err != nil {
		return commandError("artifact_write_failed", "io", fmt.Sprintf("write profile seed status %s: %v", cleanPath, err), ExitInternal, []string{"cdp --browser-mode headless browser profile seed --strategy managed --json"})
	}
	return nil
}

func loadProfileSeedStatus(stateDir string) (profileSeedStatus, bool, error) {
	path := profileSeedStatusPath(stateDir)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return profileSeedStatus{}, false, nil
		}
		return profileSeedStatus{}, false, fmt.Errorf("stat profile seed status: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return profileSeedStatus{}, false, fmt.Errorf("read profile seed status: %w", err)
	}
	var status profileSeedStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return profileSeedStatus{}, false, fmt.Errorf("parse profile seed status: %w", err)
	}
	status.Path = path
	status.Exists = true
	status.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
	return status, true, nil
}

func browserProfileNextCommands(seeded bool) []string {
	if seeded {
		return []string{
			"cdp --browser-mode headless daemon keepalive --repair --json",
			"cdp browser profile status --json",
		}
	}
	return []string{
		"cdp browser profile seed --strategy managed --json",
		"cdp browser profile seed --strategy copy-default --json",
		"cdp browser profile status --json",
	}
}

type selectedConnectionSummary struct {
	Name           string `json:"name"`
	BrowserMode    string `json:"browser_mode,omitempty"`
	ConnectionMode string `json:"connection_mode"`
	Source         string `json:"source"`
	AutoConnect    bool   `json:"auto_connect"`
	Channel        string `json:"channel,omitempty"`
	Project        string `json:"project,omitempty"`
}

func (a *app) selectedConnectionSummary(ctx context.Context) (*selectedConnectionSummary, error) {
	conn, source, ok, err := a.resolveConnection(ctx)
	if err != nil {
		return nil, err
	}
	if !ok || source == "browser_mode_default" {
		return nil, nil
	}
	return &selectedConnectionSummary{
		Name:           conn.Name,
		BrowserMode:    conn.BrowserMode,
		ConnectionMode: connectionModeForSummary(conn),
		Source:         source,
		AutoConnect:    conn.AutoConnect,
		Channel:        conn.Channel,
		Project:        conn.Project,
	}, nil
}

func connectionModeForSummary(conn state.Connection) string {
	if conn.Mode != "" {
		return conn.Mode
	}
	if conn.AutoConnect {
		return "auto_connect"
	}
	return "browser_url"
}
