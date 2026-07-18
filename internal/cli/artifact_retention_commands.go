package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/spf13/cobra"
)

func (a *app) newArtifactsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifacts",
		Short: "Inspect and safely prune cdp-owned local artifacts",
		Long:  "Inspect and safely prune allowlisted cdp-owned historical artifacts without touching browser profiles, runtime metadata, locks, sockets, connection state, current summaries, or unknown paths.",
	}
	cmd.AddCommand(a.newArtifactsPruneCommand())
	cmd.AddCommand(a.newArtifactsRunManagedCommand())
	return cmd
}

func (a *app) newArtifactsPruneCommand() *cobra.Command {
	olderThan := artifacts.DefaultRetention
	maxLogSize := artifacts.FormatByteSize(artifacts.DefaultMaxLogSizeBytes)
	var dryRun bool
	var apply bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Plan or apply allowlisted artifact retention",
		Long:  "Plan by default, or explicitly apply, seven-day retention and hard managed-log bounds within the canonical cdp state directory. Unknown and protected paths are always reported and retained.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && apply {
				return commandError("invalid_artifact_prune_mode", "usage", "use only one of --dry-run or --apply", ExitUsage, []string{"cdp artifacts prune --older-than 168h --dry-run --json", "cdp artifacts prune --older-than 168h --apply --json"})
			}
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			policy, err := a.resolveArtifactRetentionPolicy(cmd, store.Dir, olderThan, maxLogSize)
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()
			plan, err := artifacts.PlanRetention(ctx, policy)
			if err != nil {
				return commandError("artifact_prune_plan_failed", "io", fmt.Sprintf("plan artifact retention: %v", err), ExitInternal, []string{"cdp artifacts prune --dry-run --json", "cdp cron status --json"})
			}
			if !apply {
				if plan.FailedCount > 0 {
					return artifactPrunePartialError(plan)
				}
				return a.render(ctx, artifactPruneHuman(plan), plan)
			}
			report := artifacts.ApplyRetention(ctx, plan)
			if err := writeArtifactRetentionSummary(store.Dir, report); err != nil {
				return commandErrorWithData("artifact_prune_summary_failed", "io", fmt.Sprintf("write artifact retention summary: %v", err), ExitInternal, []string{"cdp artifacts prune --apply --json", "cdp cron status --json"}, report)
			}
			if report.FailedCount > 0 {
				return artifactPrunePartialError(report)
			}
			return a.render(ctx, artifactPruneHuman(report), report)
		},
	}
	cmd.Flags().DurationVar(&olderThan, "older-than", olderThan, "strict historical retention cutoff; artifacts exactly at the boundary are retained")
	cmd.Flags().StringVar(&maxLogSize, "max-log-size", maxLogSize, "hard size bound for each managed task log, such as 64MiB")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "inspect the exact retention plan without filesystem mutations (the default)")
	cmd.Flags().BoolVar(&apply, "apply", false, "apply the exact reported plan after unchanged-state safety checks")
	return cmd
}

func (a *app) newArtifactsRunManagedCommand() *cobra.Command {
	var task string
	var logPath string
	maxLogSize := artifacts.FormatByteSize(artifacts.DefaultMaxLogSizeBytes)
	cmd := &cobra.Command{
		Use:   "run-managed -- <command> [args...]",
		Short: "Run one managed task with latest-run bounded log replacement",
		Long:  "Run a cdp-managed scheduled task while writing stdout and stderr to an owner-only latest-run log capped at the configured hard size. The target log is bounded before child output opens.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.ArgsLenAtDash() < 0 {
				return commandError("managed_task_separator_required", "usage", "pass the managed child command after --", ExitUsage, []string{"cdp artifacts run-managed --task example --log tmp/example.log -- echo ok"})
			}
			task = strings.TrimSpace(task)
			logPath = strings.TrimSpace(logPath)
			if task == "" || logPath == "" {
				return commandError("invalid_managed_task", "usage", "--task and --log are required", ExitUsage, []string{"cdp artifacts run-managed --task example --log tmp/example.log -- echo ok"})
			}
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			if !filepath.IsAbs(logPath) {
				logPath = filepath.Join(store.Dir, logPath)
			}
			policy, err := a.resolveArtifactRetentionPolicy(cmd, store.Dir, artifacts.DefaultRetention, maxLogSize)
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			child := exec.CommandContext(ctx, args[0], args[1:]...)
			result, runErr := artifacts.WriteBoundedManagedLog(ctx, store.Dir, logPath, policy.MaxLogSizeBytes, func(writer io.Writer) error {
				child.Stdout = writer
				child.Stderr = writer
				return child.Run()
			})
			data := map[string]any{
				"ok":      runErr == nil,
				"task":    task,
				"command": args,
				"policy":  policy.Summary(),
				"log":     result,
			}
			if runErr != nil {
				data["error"] = runErr.Error()
				return commandErrorWithData("managed_task_failed", "check_failed", fmt.Sprintf("managed task %s failed: %v", task, runErr), ExitCheckFailed, []string{"cdp cron status --json", "cdp artifacts prune --dry-run --json"}, data)
			}
			return a.render(ctx, fmt.Sprintf("managed-task\t%s\t%d bytes", task, result.SizeBytes), data)
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "stable managed task identity")
	cmd.Flags().StringVar(&logPath, "log", "", "managed latest-run log path within the cdp state directory")
	cmd.Flags().StringVar(&maxLogSize, "max-log-size", maxLogSize, "hard size bound for the managed latest-run log")
	return cmd
}

func (a *app) resolveArtifactRetentionPolicy(cmd *cobra.Command, stateDir string, olderThan time.Duration, maxLogSize string) (artifacts.RetentionPolicy, error) {
	cfg, err := config.Load(a.opts.config)
	if err != nil {
		return artifacts.RetentionPolicy{}, commandError("invalid_config", "usage", err.Error(), ExitUsage, []string{"cdp --config <path> artifacts prune --dry-run --json"})
	}
	if !cmd.Flags().Changed("older-than") && cfg.Artifacts.Retention > 0 {
		olderThan = cfg.Artifacts.Retention
	}
	maxLogSizeBytes, err := artifacts.ParseByteSize(maxLogSize)
	if err != nil {
		return artifacts.RetentionPolicy{}, commandError("invalid_max_log_size", "usage", err.Error(), ExitUsage, []string{"cdp artifacts prune --max-log-size 64MiB --dry-run --json"})
	}
	if !cmd.Flags().Changed("max-log-size") && cfg.Artifacts.MaxLogSizeBytes > 0 {
		maxLogSizeBytes = cfg.Artifacts.MaxLogSizeBytes
	}
	if olderThan <= 0 {
		return artifacts.RetentionPolicy{}, commandError("invalid_artifact_retention", "usage", "--older-than must be positive", ExitUsage, []string{"cdp artifacts prune --older-than 168h --dry-run --json"})
	}
	policy := artifacts.DefaultRetentionPolicy(stateDir)
	policy.OlderThan = olderThan
	policy.MaxLogSizeBytes = maxLogSizeBytes
	return policy, nil
}

func artifactPrunePartialError(report artifacts.RetentionReport) error {
	return commandErrorWithData(
		"artifact_prune_partial",
		"io",
		fmt.Sprintf("artifact pruning completed with %d failure(s); unrelated safe candidates were still processed", report.FailedCount),
		ExitInternal,
		[]string{"cdp artifacts prune --older-than " + report.Policy.Retention + " --apply --json", "cdp cron status --json"},
		report,
	)
}

func artifactPruneHuman(report artifacts.RetentionReport) string {
	return fmt.Sprintf("artifacts\t%s\t%d eligible\t%d reclaimed", report.Action, report.EligibleCount, report.BytesReclaimed)
}

func writeArtifactRetentionSummary(stateDir string, report artifacts.RetentionReport) error {
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, "artifact-prune", "latest.json")
	return writeLocalFileAtomic(path, append(payload, '\n'), 0o600)
}

func loadArtifactRetentionSummary(stateDir string) map[string]any {
	path := filepath.Join(stateDir, "artifact-prune", "latest.json")
	result := map[string]any{"path": path, "exists": false}
	payload, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			result["error"] = err.Error()
		}
		return result
	}
	result["exists"] = true
	var report artifacts.RetentionReport
	if err := json.Unmarshal(payload, &report); err != nil {
		result["error"] = err.Error()
		return result
	}
	result["finished_at"] = report.FinishedAt
	result["state"] = report.State
	result["status"] = report.Status
	result["action"] = report.Action
	result["bytes_reclaimed"] = report.BytesReclaimed
	result["failed_count"] = report.FailedCount
	result["deleted_count"] = report.DeletedCount
	result["bounded_count"] = report.BoundedCount
	result["policy"] = report.Policy
	return result
}
