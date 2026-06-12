package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/spf13/cobra"
)

const defaultBrowserPreflightURL = "data:text/html,%3Cmain%20data-cdp-preflight%3D%22ok%22%3Ecdp-browser-preflight%3C%2Fmain%3E"

type browserPreflightOptions struct {
	Repair               bool
	Force                bool
	Reconnect            time.Duration
	ChromeCommand        string
	ProfileSeed          string
	ProfileSeedIfOlder   time.Duration
	Cleanup              bool
	CleanupClose         bool
	CleanupForce         bool
	IncludeAttached      bool
	IncludeURL           string
	ExcludeURL           string
	CreatedBy            string
	WorkflowCreated      bool
	CleanupIdleFor       time.Duration
	CleanupMax           int
	CleanupMaxChanged    bool
	CleanupMaxAttempts   int
	CleanupConcurrency   int
	CleanupWaitGone      bool
	OpenReadiness        bool
	OpenURL              string
	KeepOpenReadinessTab bool
}

func (a *app) newBrowserPreflightCommand() *cobra.Command {
	opts := browserPreflightOptions{
		Reconnect:          30 * time.Second,
		ChromeCommand:      defaultChromeCommand(),
		CleanupIdleFor:     30 * time.Minute,
		CleanupMaxAttempts: defaultPageCloseMaxAttempts,
		CleanupConcurrency: defaultPageCleanupCloseConcurrency,
		CleanupWaitGone:    true,
		OpenURL:            defaultBrowserPreflightURL,
	}
	cmd := &cobra.Command{
		Use:   "preflight",
		Short: "Check and optionally repair the selected browser runtime before long work",
		Long:  "Check the selected browser runtime mode, report daemon/browser health and tab budget, optionally repair managed headless, optionally seed the managed headless profile, optionally run conservative page cleanup, and optionally open a neutral readiness page. Headed mode remains passive unless an explicit active probe has already been configured.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Reconnect < 0 || opts.ProfileSeedIfOlder < 0 || opts.CleanupIdleFor < 0 || opts.CleanupMax < 0 || opts.CleanupMaxAttempts <= 0 || opts.CleanupConcurrency <= 0 {
				return commandError(
					"invalid_argument",
					"usage",
					"durations and --cleanup-max must be non-negative, and cleanup attempts/concurrency must be positive",
					ExitUsage,
					[]string{"cdp browser preflight --repair --json", "cdp browser preflight --cleanup --json"},
				)
			}
			opts.ProfileSeed = strings.TrimSpace(opts.ProfileSeed)
			if opts.ProfileSeed != "" {
				opts.ProfileSeed = browser.NormalizeProfileSeedStrategy(opts.ProfileSeed)
				if !browser.SupportedProfileSeedStrategy(opts.ProfileSeed) {
					return commandError(
						"invalid_profile_seed_strategy",
						"usage",
						"--profile-seed must be managed or copy-default",
						ExitUsage,
						[]string{"cdp --browser-mode headless browser preflight --profile-seed managed --json", "cdp --browser-mode headless browser preflight --profile-seed copy-default --json"},
					)
				}
			}
			opts.CleanupMaxChanged = cmd.Flags().Changed("cleanup-max")
			ctx, cancel := a.commandContextWithDefault(cmd, 90*time.Second)
			defer cancel()
			return a.runBrowserPreflight(ctx, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.Repair, "repair", false, "for headless, start or repair the managed daemon when health is not usable; headed remains passive")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "with --repair in headless mode, clear stale managed runtime state before relaunching")
	cmd.Flags().DurationVar(&opts.Reconnect, "reconnect", opts.Reconnect, "daemon reconnect interval to use when --repair starts managed headless")
	cmd.Flags().StringVar(&opts.ChromeCommand, "chrome-command", opts.ChromeCommand, "Chrome command for managed headless repair; empty disables launch")
	cmd.Flags().StringVar(&opts.ProfileSeed, "profile-seed", "", "optional managed headless profile seed strategy: managed or copy-default")
	cmd.Flags().DurationVar(&opts.ProfileSeedIfOlder, "profile-seed-if-older-than", 0, "skip matching profile seed when existing metadata is newer than this duration")
	cmd.Flags().BoolVar(&opts.Cleanup, "cleanup", false, "evaluate conservative page cleanup candidates after health is usable")
	cmd.Flags().BoolVar(&opts.CleanupClose, "cleanup-close", false, "close matching cleanup candidates; default cleanup mode is dry-run")
	cmd.Flags().BoolVar(&opts.CleanupForce, "cleanup-force", false, "allow cleanup to bypass selected, attached, and visible protections")
	cmd.Flags().BoolVar(&opts.IncludeAttached, "include-attached", false, "cleanup may consider attached page targets")
	cmd.Flags().StringVar(&opts.IncludeURL, "include-url", "", "cleanup only considers pages whose URL contains this text")
	cmd.Flags().StringVar(&opts.ExcludeURL, "exclude-url", "", "cleanup excludes pages whose URL contains this text")
	cmd.Flags().StringVar(&opts.CreatedBy, "created-by", "", "cleanup only considers pages tagged with this creator, such as cdp")
	cmd.Flags().BoolVar(&opts.WorkflowCreated, "workflow-created", false, "cleanup considers pages tagged as created by cdp workflows")
	cmd.Flags().DurationVar(&opts.CleanupIdleFor, "cleanup-idle-for", opts.CleanupIdleFor, "minimum duration a page must remain inactive before --cleanup-close can close it")
	cmd.Flags().IntVar(&opts.CleanupMax, "cleanup-max", 0, "maximum ready cleanup candidates to close or report; 0 uses the mode default")
	cmd.Flags().IntVar(&opts.CleanupMaxAttempts, "cleanup-max-attempts", opts.CleanupMaxAttempts, "maximum close attempts per cleanup target")
	cmd.Flags().IntVar(&opts.CleanupConcurrency, "cleanup-concurrency", opts.CleanupConcurrency, "maximum cleanup targets to close concurrently")
	cmd.Flags().BoolVar(&opts.CleanupWaitGone, "cleanup-wait-gone", opts.CleanupWaitGone, "wait until each cleanup-closed target disappears from target listing")
	cmd.Flags().BoolVar(&opts.OpenReadiness, "open-readiness", false, "open a neutral page and evaluate document readiness after health is usable")
	cmd.Flags().StringVar(&opts.OpenURL, "open-url", opts.OpenURL, "neutral URL used by --open-readiness")
	cmd.Flags().BoolVar(&opts.KeepOpenReadinessTab, "keep-open-readiness-tab", false, "leave the --open-readiness target open for manual inspection")
	return cmd
}

func (a *app) runBrowserPreflight(ctx context.Context, opts browserPreflightOptions) error {
	browserMode := a.browserModeName()
	cleanupRequested := opts.Cleanup || opts.CleanupClose || opts.CleanupForce || opts.IncludeAttached || strings.TrimSpace(opts.IncludeURL) != "" || strings.TrimSpace(opts.ExcludeURL) != "" || strings.TrimSpace(opts.CreatedBy) != "" || opts.WorkflowCreated
	report := map[string]any{
		"ok":                     false,
		"browser_mode":           browserMode,
		"state":                  "checking",
		"status":                 "pending",
		"action":                 "inspected",
		"repair_requested":       opts.Repair,
		"profile_seed_requested": opts.ProfileSeed != "",
		"cleanup_requested":      cleanupRequested,
		"open_readiness":         opts.OpenReadiness,
		"warnings":               []string{},
		"repair_actions":         []map[string]any{},
		"next_commands":          browserPreflightNextCommands(browserMode),
	}
	addWarning := func(warning string) {
		report["warnings"] = appendStringReasons(report["warnings"], warning)
	}
	addRepairAction := func(action map[string]any) {
		actions, _ := report["repair_actions"].([]map[string]any)
		report["repair_actions"] = append(actions, action)
	}
	fail := func(code, class, message string, exit int) error {
		report["ok"] = false
		report["status"] = "fail"
		if _, ok := report["state"].(string); !ok || report["state"] == "checking" {
			report["state"] = code
		}
		return commandErrorWithData(code, class, message, exit, toStringSlice(report["next_commands"]), report)
	}

	if opts.ProfileSeed != "" && browserMode != string(config.BrowserModeHeadless) {
		return commandError(
			"invalid_browser_mode",
			"usage",
			"--profile-seed is only supported for --browser-mode headless",
			ExitUsage,
			[]string{"cdp --browser-mode headless browser preflight --profile-seed managed --json"},
		)
	}

	status, health, healthErr := a.selectedDaemonHealth(ctx)
	report["daemon"] = status
	report["health"] = health
	applyPreflightBudget(report, health)
	if healthErr != nil {
		report["health_error"] = healthErr.Error()
	}

	if opts.ProfileSeed != "" {
		human, seedStatus, err := a.runBrowserProfileSeed(ctx, browserProfileSeedOptions{
			Strategy:    opts.ProfileSeed,
			IfOlderThan: opts.ProfileSeedIfOlder,
			Now:         time.Now().UTC(),
		})
		seed := map[string]any{
			"action": opts.ProfileSeed,
			"human":  human,
			"status": seedStatus,
		}
		report["profile_seed"] = seed
		if err != nil {
			seed["error"] = err.Error()
			report["state"] = "profile_seed_failed"
			return fail("profile_seed_failed", "connection", fmt.Sprintf("browser profile seed failed: %v", err), ExitConnection)
		}
		status, health, healthErr = a.selectedDaemonHealth(ctx)
		report["daemon"] = status
		report["health"] = health
		applyPreflightBudget(report, health)
		if healthErr != nil {
			report["health_error"] = healthErr.Error()
		} else {
			delete(report, "health_error")
		}
	}

	if opts.Repair {
		if browserMode == string(config.BrowserModeHeadless) {
			if !healthUsable(health) || healthErr != nil {
				repair, err := a.runBrowserPreflightHeadlessRepair(ctx, status, health, opts)
				addRepairAction(repair)
				report["repair"] = repair
				if err != nil {
					report["state"] = "repair_failed"
					return fail("browser_preflight_repair_failed", "connection", fmt.Sprintf("browser preflight repair failed: %v", err), ExitConnection)
				}
				status, health, healthErr = a.selectedDaemonHealth(ctx)
				report["daemon"] = status
				report["health"] = health
				applyPreflightBudget(report, health)
				if healthErr != nil {
					report["health_error"] = healthErr.Error()
				} else {
					delete(report, "health_error")
				}
			} else {
				addRepairAction(map[string]any{"action": "none", "reason": "health_usable"})
			}
		} else {
			addWarning("headed repair is passive; use diagnostics and human approval rather than unattended repair")
			addRepairAction(map[string]any{"action": "skipped", "reason": "headed_human_managed"})
		}
	}

	if status.State == "permission_pending" || healthState(health) == "permission_pending" {
		report["state"] = "permission_pending"
		for key, value := range permissionPendingData(nil) {
			report[key] = value
		}
		report["next_commands"] = permissionRemediationCommands()
		return fail("permission_pending", "permission", "browser preflight is waiting for headed browser approval", ExitPermission)
	}
	if healthErr != nil || !healthUsable(health) {
		code := "browser_preflight_unusable"
		if healthCode, _ := stringMapField(health, "code"); healthCode != "" {
			code = healthCode
		}
		report["state"] = "unusable"
		report["next_commands"] = uniqueCommands(toStringSlice(report["next_commands"]), toStringSlice(health["next_commands"]), a.connectionRemediationCommands())
		return fail(code, "connection", fmt.Sprintf("browser preflight failed: runtime health is %s", healthState(health)), ExitCheckFailed)
	}

	if cleanupRequested {
		_, cleanup, err := a.runPageCleanup(ctx, pageCleanupRunOptions{
			Close:            opts.CleanupClose,
			IncludeAttached:  opts.IncludeAttached,
			IncludeURL:       opts.IncludeURL,
			ExcludeURL:       opts.ExcludeURL,
			CreatedBy:        opts.CreatedBy,
			WorkflowCreated:  opts.WorkflowCreated,
			Force:            opts.CleanupForce,
			WaitGone:         opts.CleanupWaitGone,
			MaxAttempts:      opts.CleanupMaxAttempts,
			CloseConcurrency: opts.CleanupConcurrency,
			IdleFor:          opts.CleanupIdleFor,
			Max:              opts.CleanupMax,
			MaxChanged:       opts.CleanupMaxChanged,
		})
		report["cleanup"] = cleanup
		if err != nil {
			report["cleanup_error"] = err.Error()
			report["state"] = "cleanup_failed"
			return fail("browser_preflight_cleanup_failed", "connection", fmt.Sprintf("browser preflight cleanup failed: %v", err), ExitConnection)
		}
		if cleanupCloseRequired(cleanup) {
			report["state"] = "cleanup_required"
			report["next_commands"] = uniqueCommands(toStringSlice(report["next_commands"]), []string{modeScopedCommand(browserMode, "browser preflight --cleanup --cleanup-close --json")})
			return fail("browser_preflight_cleanup_required", "resource_budget", "browser preflight found cleanup candidates; rerun with --cleanup-close to mutate", ExitCheckFailed)
		}
		status, health, healthErr = a.selectedDaemonHealth(ctx)
		report["daemon"] = status
		report["health"] = health
		applyPreflightBudget(report, health)
		if healthErr != nil {
			report["health_error"] = healthErr.Error()
		} else {
			delete(report, "health_error")
		}
	}

	if healthOverBudget(health) && !a.opts.allowOverBudget {
		report["state"] = "over_budget"
		report["next_commands"] = uniqueCommands(toStringSlice(report["next_commands"]), []string{modeScopedCommand(browserMode, "browser preflight --cleanup --json"), modeScopedCommand(browserMode, "page cleanup --json")})
		return fail("browser_resource_budget_exceeded", "resource_budget", "browser preflight failed: tab or window budget is over limit", ExitCheckFailed)
	}
	if healthOverBudget(health) {
		addWarning("browser resource budget is over limit but --allow-over-budget is set")
	}

	if opts.OpenReadiness {
		readiness, err := a.runBrowserPreflightOpenReadiness(ctx, opts.OpenURL, opts.KeepOpenReadinessTab)
		report["readiness"] = readiness
		if err != nil {
			report["state"] = "open_readiness_failed"
			return fail("browser_preflight_open_readiness_failed", "check_failed", fmt.Sprintf("browser preflight open-readiness failed: %v", err), ExitCheckFailed)
		}
	}

	report["ok"] = true
	report["usable"] = true
	if healthState(health) == "healthy" {
		report["state"] = "healthy"
		report["status"] = "pass"
	} else {
		report["state"] = "usable_degraded"
		report["status"] = "warn"
		report["degraded_reasons"] = toStringSlice(health["degraded_reasons"])
		addWarning("runtime is usable but degraded; repair before long or expensive crawls when possible")
	}
	return a.render(ctx, fmt.Sprintf("browser-preflight\t%s", report["state"]), report)
}

func (a *app) runBrowserPreflightHeadlessRepair(ctx context.Context, status daemon.Status, health map[string]any, opts browserPreflightOptions) (map[string]any, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, err
	}
	lockName := "browser-preflight-headless"
	lock, acquired, existingLock, err := daemon.AcquireLock(ctx, store.Dir, lockName, 0, 10*time.Minute, daemon.LockMetadata{Name: lockName, Phase: "repairing"})
	if err != nil {
		return nil, commandError("lock_failed", "connection", fmt.Sprintf("acquire preflight lock: %v", err), ExitConnection, []string{"cdp --browser-mode headless daemon health --json"})
	}
	if !acquired {
		return map[string]any{
			"action": "skipped",
			"state":  "locked",
			"locked": true,
			"lock":   existingLock,
		}, fmt.Errorf("browser preflight repair is locked")
	}
	defer lock.Release()

	repair, err := a.repairManagedHeadlessForHealthCheck(ctx, store.Dir, daemonHealthCheckOptions{
		Repair:        true,
		Force:         opts.Force,
		Reconnect:     opts.Reconnect,
		ChromeCommand: opts.ChromeCommand,
	}, lock, status, health)
	if repair == nil {
		repair = map[string]any{}
	}
	repair["lock"] = map[string]any{"name": lock.Metadata.Name, "acquired": true}
	return repair, err
}

type browserPreflightReadinessState struct {
	ReadyState     string `json:"ready_state"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	BodyTextLength int    `json:"body_text_length"`
}

func (a *app) runBrowserPreflightOpenReadiness(ctx context.Context, rawURL string, keepOpen bool) (map[string]any, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		rawURL = defaultBrowserPreflightURL
	}
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return map[string]any{"ok": false, "url": rawURL, "error": err.Error()}, err
	}
	targetID, err := a.createWorkflowPageTarget(ctx, client, rawURL, "browser-preflight")
	if err != nil {
		_ = closeClient(ctx)
		return map[string]any{"ok": false, "url": rawURL, "error": err.Error()}, err
	}
	target, err := cdp.TargetInfoWithClient(ctx, client, targetID)
	if err != nil {
		_ = closeClient(ctx)
		return map[string]any{"ok": false, "url": rawURL, "target_id": targetID, "error": err.Error()}, err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
	if err != nil {
		_ = closePageTargetSettled(ctx, client, target, pageCloseOptions{
			WaitGone:     true,
			MaxAttempts:  defaultPageCloseMaxAttempts,
			AttemptWait:  pageCloseAttemptTimeout(a.browserModeName()),
			PollInterval: defaultPageClosePollInterval,
			RetryBackoff: defaultPageCloseRetryBackoff,
		})
		_ = closeClient(ctx)
		return map[string]any{"ok": false, "url": rawURL, "target": pageRow(target), "error": err.Error()}, err
	}
	defer session.Close(ctx)
	if !keepOpen {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), pageCloseDefaultTimeout(a.browserModeName(), defaultPageCloseMaxAttempts))
			defer cancel()
			_ = closePageTargetSettled(closeCtx, client, target, pageCloseOptions{
				WaitGone:     true,
				MaxAttempts:  defaultPageCloseMaxAttempts,
				AttemptWait:  pageCloseAttemptTimeout(a.browserModeName()),
				PollInterval: defaultPageClosePollInterval,
				RetryBackoff: defaultPageCloseRetryBackoff,
			})
		}()
	}

	start := time.Now()
	deadline := start.Add(5 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	attempts := 0
	var last browserPreflightReadinessState
	var lastErr error
	for {
		attempts++
		var current browserPreflightReadinessState
		err := evaluateJSONValue(ctx, session, `(() => {
  const marker = "__cdp_cli_browser_preflight__";
  return {marker, ready_state: document.readyState, title: document.title || "", url: location.href, body_text_length: document.body ? String(document.body.innerText || "").length : 0};
})()`, "browser preflight readiness", &current)
		if err == nil {
			last = current
			lastErr = nil
			if current.ReadyState == "interactive" || current.ReadyState == "complete" {
				return map[string]any{
					"ok":              true,
					"url":             rawURL,
					"target":          pageRow(target),
					"closed":          !keepOpen,
					"attempt_count":   attempts,
					"elapsed_ms":      time.Since(start).Milliseconds(),
					"readiness_state": current,
				}, nil
			}
		} else {
			lastErr = err
		}
		if !time.Now().Before(deadline) {
			result := map[string]any{
				"ok":              false,
				"url":             rawURL,
				"target":          pageRow(target),
				"closed":          !keepOpen,
				"attempt_count":   attempts,
				"elapsed_ms":      time.Since(start).Milliseconds(),
				"readiness_state": last,
			}
			if lastErr != nil {
				result["last_error"] = lastErr.Error()
				return result, lastErr
			}
			return result, fmt.Errorf("document readiness stayed %q", last.ReadyState)
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return map[string]any{
				"ok":              false,
				"url":             rawURL,
				"target":          pageRow(target),
				"closed":          !keepOpen,
				"attempt_count":   attempts,
				"elapsed_ms":      time.Since(start).Milliseconds(),
				"readiness_state": last,
				"last_error":      ctx.Err().Error(),
			}, ctx.Err()
		case <-timer.C:
		}
	}
}

func applyPreflightBudget(report map[string]any, health map[string]any) {
	if budget, ok := health["resource_budget"]; ok {
		report["budget"] = budget
		report["resource_budget"] = budget
	}
}

func healthOverBudget(health map[string]any) bool {
	tabs, _ := health["tabs_over_budget"].(bool)
	windows, _ := health["windows_over_budget"].(bool)
	return tabs || windows
}

func cleanupCloseRequired(data map[string]any) bool {
	cleanup, ok := data["cleanup"].(map[string]any)
	if !ok {
		return false
	}
	required, _ := cleanup["close_required"].(bool)
	return required
}

func browserPreflightNextCommands(browserMode string) []string {
	commands := []string{
		modeScopedCommand(browserMode, "browser preflight --json"),
		modeScopedCommand(browserMode, "daemon health --json"),
		modeScopedCommand(browserMode, "pages --json"),
	}
	if browserMode == string(config.BrowserModeHeadless) {
		commands = append(commands,
			modeScopedCommand(browserMode, "browser preflight --repair --json"),
			modeScopedCommand(browserMode, "browser preflight --cleanup --json"),
			modeScopedCommand(browserMode, "daemon keepalive --repair --json"),
			modeScopedCommand(browserMode, "browser profile seed --strategy managed --json"),
		)
	} else {
		commands = append(commands, modeScopedCommand(browserMode, "daemon status --json"))
	}
	return uniqueCommands(commands)
}
