package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
}

type cronBlockState struct {
	Installed bool     `json:"installed"`
	Entries   []string `json:"entries"`
	Text      string   `json:"text,omitempty"`
}

func (a *app) newCronCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage cdp-managed browser runtime cron tasks",
		Long:  "Install, inspect, diff, remove, and heal the cdp-managed user crontab block for browser runtime keepalive tasks.",
	}
	cmd.AddCommand(a.newCronStatusCommand())
	cmd.AddCommand(a.newCronDiffCommand())
	cmd.AddCommand(a.newCronInstallCommand())
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
			current, err := readUserCrontab(ctx)
			available := !isCrontabMissing(err)
			if err != nil && !isEmptyCrontab(err) && !isCrontabMissing(err) {
				return cronCommandError("read crontab", err)
			}
			state := extractCronManagedBlock(current)
			intended := managedCronBlock(opts)
			status := scheduledTasksStatusForSummary(available, err, summarizeCrontab(current))
			store, storeErr := a.stateStore()
			locks := map[string]any{}
			artifacts := map[string]any{}
			if storeErr == nil {
				locks = cronLockStates(store.Dir)
				artifacts = cronLastRunArtifacts(store.Dir)
			}
			data := map[string]any{
				"ok":                 true,
				"available":          available,
				"installed":          state.Installed,
				"matches_intended":   normalizeCronBlock(state.Text) == normalizeCronBlock(intended),
				"managed_block":      state,
				"intended_block":     extractCronManagedBlock(intended),
				"scheduled_tasks":    status,
				"locks":              locks,
				"last_run_artifacts": artifacts,
				"processes_by_mode":  a.daemonProcessesByMode(ctx),
				"next_commands":      []string{"cdp cron diff --json", "cdp cron install --profile agent --json", "cdp doctor --check scheduled-tasks --json"},
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
			current, err := readUserCrontab(ctx)
			if err != nil && !isEmptyCrontab(err) {
				return cronCommandError("read crontab", err)
			}
			installed := extractCronManagedBlock(current)
			intendedText := managedCronBlock(opts)
			intended := extractCronManagedBlock(intendedText)
			without := withoutCronManagedBlock(current)
			wanted := appendCronManagedBlock(without, intendedText)
			data := map[string]any{
				"ok":               true,
				"installed":        installed.Installed,
				"matches_intended": normalizeCronBlock(installed.Text) == normalizeCronBlock(intendedText),
				"current_block":    installed,
				"intended_block":   intended,
				"actions":          cronDiffActions(current, wanted, installed.Installed),
				"next_commands":    []string{"cdp cron install --profile agent --json", "cdp cron remove --json"},
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
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install or repair the cdp-managed user crontab block",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			current, err := readUserCrontab(ctx)
			if err != nil && !isEmptyCrontab(err) {
				return cronCommandError("read crontab", err)
			}
			block := managedCronBlock(opts)
			next := appendCronManagedBlock(withoutCronManagedBlock(current), block)
			changed := current != next
			if changed {
				if err := writeUserCrontab(ctx, next); err != nil {
					return cronCommandError("write crontab", err)
				}
			}
			installed := extractCronManagedBlock(next)
			data := map[string]any{
				"ok":               true,
				"action":           actionString(changed, "installed", "unchanged"),
				"changed":          changed,
				"installed":        true,
				"matches_intended": true,
				"managed_block":    installed,
				"warnings":         cronInstallWarnings(opts),
				"next_commands":    []string{"cdp cron status --json", "cdp doctor --check scheduled-tasks --json"},
			}
			return a.render(ctx, fmt.Sprintf("cdp cron block %s", data["action"]), data)
		},
	}
	addCronRenderFlags(cmd, &opts)
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
				"next_commands": []string{"cdp cron status --json", "cdp cron install --profile agent --json"},
			}
			return a.render(ctx, fmt.Sprintf("cdp cron block %s", data["action"]), data)
		},
	}
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
	}
}

func managedCronBlock(opts cronRenderOptions) string {
	cdpBin := cronValue(opts.CDPBin)
	logDir := cronValue(opts.LogDir)
	display := cronValue(opts.Display)
	xdgRuntimeDir := cronValue(opts.XDGRuntimeDir)
	reconnect := opts.Reconnect.String()
	if opts.Reconnect <= 0 {
		reconnect = "30s"
	}
	lines := []string{
		cronManagedBlockStart,
		fmt.Sprintf("* * * * * %s", cronLockedCommand(fmt.Sprintf("%s/locks/cron-headed-heal.lock", logDir), fmt.Sprintf("env DISPLAY=%s XDG_RUNTIME_DIR=%s %s --browser-mode headed cron heal headed --reconnect %s --display %s --json >> %s/keepalive-headed.log 2>&1", display, xdgRuntimeDir, cdpBin, reconnect, display, logDir))),
		fmt.Sprintf("* * * * * %s", cronLockedCommand(fmt.Sprintf("%s/locks/keepalive-headless.lock", logDir), fmt.Sprintf("%s --browser-mode headless daemon keepalive --repair --reconnect %s --json >> %s/keepalive-headless.log 2>&1", cdpBin, reconnect, logDir))),
		fmt.Sprintf("* * * * * %s", cronLockedCommand(fmt.Sprintf("%s/locks/headless-health.lock", logDir), fmt.Sprintf("%s --browser-mode headless daemon health --json >> %s/headless-health.log 2>&1", cdpBin, logDir))),
		fmt.Sprintf("0 */6 * * * %s", cronLockedCommand(fmt.Sprintf("%s/locks/headless-profile-seed.lock", logDir), fmt.Sprintf("%s --browser-mode headless browser profile seed --strategy managed --if-older-than 6h --json >> %s/profile-seed-headless.log 2>&1", cdpBin, logDir))),
		fmt.Sprintf("* * * * * %s", cronLockedCommand(fmt.Sprintf("%s/locks/page-cleanup-headless.lock", logDir), fmt.Sprintf("%s --browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --max 10 --json >> %s/page-cleanup-headless.log 2>&1", cdpBin, logDir))),
		cronManagedBlockEnd,
	}
	return strings.Join(lines, "\n") + "\n"
}

func cronLockedCommand(lockPath, command string) string {
	quotedCommand := cronValue(command)
	return fmt.Sprintf("cdp_lock=%s; mkdir -p \"$(dirname \"$cdp_lock\")\"; cdp_flock=$(command -v flock 2>/dev/null || true); if [ -n \"$cdp_flock\" ]; then \"$cdp_flock\" -n \"$cdp_lock\" sh -c %s; elif mkdir \"$cdp_lock.dir\" 2>/dev/null; then trap 'rmdir \"$cdp_lock.dir\"' EXIT; %s; fi", lockPath, quotedCommand, command)
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

func cronInstallWarnings(opts cronRenderOptions) []string {
	var warnings []string
	if strings.TrimSpace(opts.Profile) != "agent" {
		warnings = append(warnings, "only the agent profile is currently rendered; generated entries still use agent defaults")
	}
	if strings.TrimSpace(opts.CDPBin) == "" {
		warnings = append(warnings, "cdp binary path is empty")
	}
	return warnings
}

func cronLockStates(stateDir string) map[string]any {
	locks := map[string]any{}
	for _, name := range []string{"cron-headed-heal", "keepalive-headless", "headless-health", "headless-profile-seed", "page-cleanup-headless"} {
		path := filepath.Join(stateDir, "locks", name+".lock")
		info, err := os.Stat(path)
		locks[name] = map[string]any{
			"path":   path,
			"exists": err == nil,
			"stale":  err == nil && time.Since(info.ModTime()) > 10*time.Minute,
		}
	}
	return locks
}

func cronLastRunArtifacts(stateDir string) map[string]any {
	paths := map[string]string{
		"headed_keepalive_log":      filepath.Join(stateDir, "keepalive-headed.log"),
		"headed_heal_summary":       filepath.Join(stateDir, "headed-heal", "latest.json"),
		"headless_keepalive_log":    filepath.Join(stateDir, "keepalive-headless.log"),
		"headless_health_log":       filepath.Join(stateDir, "headless-health.log"),
		"headless_profile_seed_log": filepath.Join(stateDir, "profile-seed-headless.log"),
		"headless_page_cleanup_log": filepath.Join(stateDir, "page-cleanup-headless.log"),
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
