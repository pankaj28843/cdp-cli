package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestCronInstallIsIdempotentAndPreservesUserEntries(t *testing.T) {
	crontabPath, crontabBin := fakeCrontab(t, "SHELL=/bin/sh\n0 0 * * * /usr/local/bin/backup\n")
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var first cronInstallResult
	executeCronJSON(t, []string{"cron", "install", "--state-dir", stateDir, "--json"}, &first)
	if !first.OK || !first.Changed || first.Action != "installed" || !first.Installed {
		t.Fatalf("first cron install = %+v, want installed change", first)
	}
	afterFirst := readFileString(t, crontabPath)
	if strings.Count(afterFirst, "# cdp-cli managed browser runtime tasks") != 1 {
		t.Fatalf("crontab after first install has duplicate/missing managed block:\n%s", afterFirst)
	}
	if !strings.Contains(afterFirst, "SHELL=/bin/sh\n0 0 * * * /usr/local/bin/backup\n") {
		t.Fatalf("crontab after install did not preserve existing lines:\n%s", afterFirst)
	}
	for _, want := range []string{"artifacts run-managed --task headed-daemon-keepalive", "--browser-mode headed daemon keepalive --auto-connect --repair --probe passive", "artifacts run-managed --task headless-maintenance", "--browser-mode headless daemon maintenance --profile-seed-strategy managed --profile-seed-if-older-than 6h --repair --force --reconnect 30s --health-check --cleanup --cleanup-close --json", "artifacts prune --older-than 168h --max-log-size 64MiB --apply", "headless-maintenance.log", "command -v flock", "--help 2>&1 | grep -q -- '--close'"} {
		if !strings.Contains(afterFirst, want) {
			t.Fatalf("crontab after install missing %q:\n%s", want, afterFirst)
		}
	}
	if strings.Contains(afterFirst, ">> ") {
		t.Fatalf("crontab after install append-opens a managed log:\n%s", afterFirst)
	}
	for _, oldCommand := range []string{"--browser-mode headless daemon keepalive --managed-process-sweep", "--browser-mode headless daemon health-check --managed-process-sweep", "--browser-mode headless browser profile seed", "--browser-mode headless page cleanup"} {
		if strings.Contains(afterFirst, oldCommand) {
			t.Fatalf("crontab after install still has legacy headless task %q:\n%s", oldCommand, afterFirst)
		}
	}
	if strings.Contains(afterFirst, " cdp pages ") || strings.Contains(afterFirst, " cdp pages --browser-mode") || strings.Contains(afterFirst, " pages --browser-mode headed") {
		t.Fatalf("crontab after install still uses pages polling instead of daemon keepalive:\n%s", afterFirst)
	}
	if strings.Contains(afterFirst, "cron heal headed") {
		t.Fatalf("crontab after install still uses headed cron heal instead of daemon keepalive:\n%s", afterFirst)
	}
	if strings.Contains(afterFirst, "--strategy copy-default") || strings.Contains(afterFirst, "/usr/bin/flock") {
		t.Fatalf("crontab after install used non-portable or unsafe defaults:\n%s", afterFirst)
	}

	var second cronInstallResult
	executeCronJSON(t, []string{"cron", "install", "--state-dir", stateDir, "--json"}, &second)
	if !second.OK || second.Changed || second.Action != "unchanged" {
		t.Fatalf("second cron install = %+v, want unchanged", second)
	}
	afterSecond := readFileString(t, crontabPath)
	if afterSecond != afterFirst {
		t.Fatalf("idempotent install changed crontab:\nfirst:\n%s\nsecond:\n%s", afterFirst, afterSecond)
	}
}

func TestCronStatusReportsManagedTaskIDsAndSweepHooks(t *testing.T) {
	_, crontabBin := fakeCrontab(t, "")
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var install cronInstallResult
	executeCronJSON(t, []string{"cron", "install", "--state-dir", stateDir, "--json"}, &install)
	if !install.OK || !install.Changed {
		t.Fatalf("cron install = %+v, want changed", install)
	}

	var status struct {
		OK    bool `json:"ok"`
		Tasks []struct {
			ID                           string `json:"id"`
			Status                       string `json:"status"`
			Installed                    bool   `json:"installed"`
			MatchesIntended              bool   `json:"matches_intended"`
			LaunchCapable                bool   `json:"launch_capable"`
			RequiresManagedProcessSweep  bool   `json:"requires_managed_process_sweep"`
			ManagedProcessSweepInstalled bool   `json:"managed_process_sweep_installed"`
		} `json:"tasks"`
		ScheduledTasks struct {
			Details struct {
				ExpectedManagedTaskIDs                      []string `json:"expected_managed_task_ids"`
				InstalledManagedTaskIDs                     []string `json:"installed_managed_task_ids"`
				MissingManagedTaskIDs                       []string `json:"missing_managed_task_ids"`
				BlockedManagedTaskIDs                       []string `json:"blocked_managed_task_ids"`
				HasManagedProcessSweep                      bool     `json:"has_managed_process_sweep"`
				HasHeadlessLaunchWithoutManagedProcessSweep bool     `json:"has_headless_launch_without_managed_process_sweep"`
			} `json:"details"`
		} `json:"scheduled_tasks"`
	}
	executeCronJSON(t, []string{"cron", "status", "--state-dir", stateDir, "--json"}, &status)
	if !status.OK || len(status.Tasks) != 3 {
		t.Fatalf("cron status tasks = %+v ok=%v, want three managed tasks", status.Tasks, status.OK)
	}
	taskIDs := map[string]bool{}
	for _, task := range status.Tasks {
		taskIDs[task.ID] = true
	}
	wantIDs := []string{"artifact-prune", "headed-daemon-keepalive", "headless-maintenance"}
	for _, id := range wantIDs {
		if !taskIDs[id] || !containsString(status.ScheduledTasks.Details.ExpectedManagedTaskIDs, id) || !containsString(status.ScheduledTasks.Details.InstalledManagedTaskIDs, id) {
			t.Fatalf("cron status missing task id %q in tasks=%+v details=%+v", id, status.Tasks, status.ScheduledTasks.Details)
		}
	}
	if len(status.ScheduledTasks.Details.MissingManagedTaskIDs) != 0 || len(status.ScheduledTasks.Details.BlockedManagedTaskIDs) != 0 || !status.ScheduledTasks.Details.HasManagedProcessSweep || status.ScheduledTasks.Details.HasHeadlessLaunchWithoutManagedProcessSweep {
		t.Fatalf("scheduled task details = %+v, want all managed tasks installed with sweep hooks", status.ScheduledTasks.Details)
	}
	for _, task := range status.Tasks {
		if !task.Installed || !task.MatchesIntended || task.Status != "installed" {
			t.Fatalf("task %q = %+v, want installed and matching intended", task.ID, task)
		}
		if task.ID == "headless-maintenance" && (!task.LaunchCapable || !task.RequiresManagedProcessSweep || !task.ManagedProcessSweepInstalled) {
			t.Fatalf("headless maintenance task %q = %+v, want sweep phase installed", task.ID, task)
		}
	}
}

func TestCronStatusReportsPolicyDriftAndLastCleanup(t *testing.T) {
	_, crontabBin := fakeCrontab(t, "")
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var install cronInstallResult
	executeCronJSON(t, []string{"cron", "install", "--state-dir", stateDir, "--json"}, &install)
	if !install.OK || !install.Changed {
		t.Fatalf("cron install = %+v, want changed", install)
	}
	var prune struct {
		OK bool `json:"ok"`
	}
	executeCronJSON(t, []string{"artifacts", "prune", "--apply", "--state-dir", stateDir, "--json"}, &prune)
	if !prune.OK {
		t.Fatalf("artifact prune = %+v, want successful summary", prune)
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"artifacts":{"retention":"336h","max_log_size":"8MiB"}}`), 0o600); err != nil {
		t.Fatalf("write artifact policy config: %v", err)
	}
	var status struct {
		OK              bool `json:"ok"`
		MatchesIntended bool `json:"matches_intended"`
		ArtifactPolicy  struct {
			RetentionSeconds int64 `json:"retention_seconds"`
			MaxLogSizeBytes  int64 `json:"max_log_size_bytes"`
		} `json:"artifact_policy"`
		LastCleanup struct {
			Exists         bool   `json:"exists"`
			Status         string `json:"status"`
			FailedCount    int    `json:"failed_count"`
			BytesReclaimed int64  `json:"bytes_reclaimed"`
		} `json:"last_cleanup"`
		Tasks []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"tasks"`
	}
	executeCronJSON(t, []string{"--config", configPath, "cron", "status", "--state-dir", stateDir, "--json"}, &status)
	if !status.OK || status.MatchesIntended || status.ArtifactPolicy.RetentionSeconds != 336*60*60 || status.ArtifactPolicy.MaxLogSizeBytes != 8<<20 {
		t.Fatalf("cron policy drift status = %+v, want current config policy and block mismatch", status)
	}
	if !status.LastCleanup.Exists || status.LastCleanup.Status != "pass" || status.LastCleanup.FailedCount != 0 {
		t.Fatalf("last cleanup = %+v, want successful persisted execution", status.LastCleanup)
	}
	foundStalePrune := false
	for _, task := range status.Tasks {
		if task.ID == "artifact-prune" && task.Status == "stale" {
			foundStalePrune = true
		}
	}
	if !foundStalePrune {
		t.Fatalf("cron tasks = %+v, want artifact-prune marked stale for policy drift", status.Tasks)
	}
	var doctor struct {
		Checks []struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details struct {
				MatchesCurrentPolicy bool     `json:"matches_current_policy"`
				StaleManagedTaskIDs  []string `json:"stale_managed_task_ids"`
				ArtifactPolicy       struct {
					RetentionSeconds int64 `json:"retention_seconds"`
					MaxLogSizeBytes  int64 `json:"max_log_size_bytes"`
				} `json:"artifact_policy"`
				LastCleanup struct {
					Exists bool `json:"exists"`
				} `json:"last_cleanup"`
			} `json:"details"`
		} `json:"checks"`
	}
	executeCronJSON(t, []string{"--config", configPath, "doctor", "--check", "scheduled-tasks", "--state-dir", stateDir, "--json"}, &doctor)
	if len(doctor.Checks) != 1 || doctor.Checks[0].Status != "warn" || doctor.Checks[0].Details.MatchesCurrentPolicy || !containsString(doctor.Checks[0].Details.StaleManagedTaskIDs, "artifact-prune") || doctor.Checks[0].Details.ArtifactPolicy.RetentionSeconds != 336*60*60 || doctor.Checks[0].Details.ArtifactPolicy.MaxLogSizeBytes != 8<<20 || !doctor.Checks[0].Details.LastCleanup.Exists {
		t.Fatalf("scheduled-tasks doctor = %+v, want policy drift and last cleanup observability", doctor.Checks)
	}
}

func TestCronStatusIncludesProfileSeedLastStatusArtifact(t *testing.T) {
	_, crontabBin := fakeCrontab(t, "")
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var install cronInstallResult
	executeCronJSON(t, []string{"cron", "install", "--state-dir", stateDir, "--json"}, &install)
	if !install.OK || !install.Changed {
		t.Fatalf("cron install = %+v, want changed", install)
	}

	var seed struct {
		OK             bool   `json:"ok"`
		SeedAction     string `json:"seed_action"`
		SeedStatusPath string `json:"seed_status_path"`
		LastSeed       struct {
			SchemaVersion string `json:"schema_version"`
			SeedStrategy  string `json:"seed_strategy"`
			SeedAction    string `json:"seed_action"`
		} `json:"last_seed"`
	}
	executeCronJSON(t, []string{"--browser-mode", "headless", "--state-dir", stateDir, "browser", "profile", "seed", "--strategy", "managed", "--json"}, &seed)
	if !seed.OK || seed.SeedAction != "seeded" || seed.SeedStatusPath == "" || seed.LastSeed.SchemaVersion != "cdp-profile-seed-status/v1" || seed.LastSeed.SeedStrategy != "managed" || seed.LastSeed.SeedAction != "seeded" {
		t.Fatalf("profile seed = %+v, want persisted managed seed summary", seed)
	}

	var status struct {
		OK          bool `json:"ok"`
		ProfileSeed struct {
			Strategy string `json:"strategy"`
			LastSeed struct {
				Path          string `json:"path"`
				Exists        bool   `json:"exists"`
				SchemaVersion string `json:"schema_version"`
				Status        string `json:"status"`
				State         string `json:"state"`
				SeedStrategy  string `json:"seed_strategy"`
				SeedAction    string `json:"seed_action"`
			} `json:"last_seed"`
		} `json:"profile_seed"`
		LastRunArtifacts map[string]struct {
			Path   string `json:"path"`
			Exists bool   `json:"exists"`
		} `json:"last_run_artifacts"`
	}
	executeCronJSON(t, []string{"cron", "status", "--state-dir", stateDir, "--json"}, &status)
	artifact := status.LastRunArtifacts["headless_profile_seed_summary"]
	if !status.OK || status.ProfileSeed.Strategy != "managed" || status.ProfileSeed.LastSeed.Path != seed.SeedStatusPath || !status.ProfileSeed.LastSeed.Exists || status.ProfileSeed.LastSeed.SchemaVersion != "cdp-profile-seed-status/v1" || status.ProfileSeed.LastSeed.Status != "pass" || status.ProfileSeed.LastSeed.State != "ready" || status.ProfileSeed.LastSeed.SeedStrategy != "managed" || status.ProfileSeed.LastSeed.SeedAction != "seeded" {
		t.Fatalf("cron status profile seed = %+v, artifact=%+v, want last seed summary", status.ProfileSeed, artifact)
	}
	if artifact.Path != seed.SeedStatusPath || !artifact.Exists {
		t.Fatalf("cron status last_run_artifacts = %+v, want profile seed summary at %s", status.LastRunArtifacts, seed.SeedStatusPath)
	}
}

func TestCronInstallUsesHeadlessSeedConfigDryRun(t *testing.T) {
	initial := "SHELL=/bin/sh\n"
	crontabPath, crontabBin := fakeCrontab(t, initial)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"headless":{"profile_seed_strategy":"copy-default","profile_refresh_after":"30m"}},"artifacts":{"retention":"336h","max_log_size":"8MiB"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var got struct {
		OK          bool `json:"ok"`
		DryRun      bool `json:"dry_run"`
		ProfileSeed struct {
			Strategy           string `json:"strategy"`
			IfOlderThan        string `json:"if_older_than"`
			IfOlderThanSeconds int64  `json:"if_older_than_seconds"`
			Schedule           string `json:"schedule"`
		} `json:"profile_seed"`
		ArtifactPolicy struct {
			Retention        string `json:"retention"`
			RetentionSeconds int64  `json:"retention_seconds"`
			MaxLogSize       string `json:"max_log_size"`
			MaxLogSizeBytes  int64  `json:"max_log_size_bytes"`
		} `json:"artifact_policy"`
		IntendedBlock struct {
			Entries []string `json:"entries"`
		} `json:"intended_block"`
	}
	executeCronJSON(t, []string{"--config", configPath, "cron", "install", "--dry-run", "--state-dir", stateDir, "--json"}, &got)
	if !got.OK || !got.DryRun || got.ProfileSeed.Strategy != "copy-default" || got.ProfileSeed.IfOlderThan != "30m" || got.ProfileSeed.IfOlderThanSeconds != 1800 || got.ProfileSeed.Schedule != "*/15 * * * *" {
		t.Fatalf("cron install dry-run profile_seed = %+v, want copy-default 30m cadence", got.ProfileSeed)
	}
	if got.ArtifactPolicy.Retention != "336h0m0s" || got.ArtifactPolicy.RetentionSeconds != 336*60*60 || got.ArtifactPolicy.MaxLogSize != "8MiB" || got.ArtifactPolicy.MaxLogSizeBytes != 8<<20 {
		t.Fatalf("cron install dry-run artifact_policy = %+v, want config-backed 336h/8MiB", got.ArtifactPolicy)
	}
	block := strings.Join(got.IntendedBlock.Entries, "\n")
	for _, want := range []string{"--browser-mode headless daemon maintenance --profile-seed-strategy copy-default --profile-seed-if-older-than 30m", "--max-log-size 8MiB", "artifacts prune --older-than 336h --max-log-size 8MiB --apply", "--health-check --cleanup --cleanup-close --json"} {
		if !strings.Contains(block, want) {
			t.Fatalf("intended cron block missing %q:\n%s", want, block)
		}
	}
	if after := readFileString(t, crontabPath); after != initial {
		t.Fatalf("dry-run mutated crontab:\n%s", after)
	}
}

func TestCronInstallRejectsMalformedProfileRefreshAfterConfig(t *testing.T) {
	_, crontabBin := fakeCrontab(t, "SHELL=/bin/sh\n")
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"headless":{"profile_seed_strategy":"copy-default","profile_refresh_after":"daily"}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--config", configPath, "cron", "install", "--dry-run", "--state-dir", stateDir, "--json"}, &stdout, &stderr, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("cron install malformed refresh exit = %d, want %d; stderr=%s stdout=%s", code, cli.ExitUsage, stderr.String(), stdout.String())
	}
	var got struct {
		OK       bool   `json:"ok"`
		Code     string `json:"code"`
		ErrClass string `json:"err_class"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode malformed refresh error: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != "invalid_config" || got.ErrClass != "usage" || !strings.Contains(got.Message, "browser.headless.profile_refresh_after") {
		t.Fatalf("malformed refresh error = %+v, want invalid_config usage with refresh path", got)
	}
}

func TestCronInstallHeadedOnlyDryRunDoesNotMutateCrontab(t *testing.T) {
	initial := "SHELL=/bin/sh\n0 0 * * * /usr/local/bin/backup\n"
	crontabPath, crontabBin := fakeCrontab(t, initial)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var got struct {
		OK            bool `json:"ok"`
		Changed       bool `json:"changed"`
		Installed     bool `json:"installed"`
		DryRun        bool `json:"dry_run"`
		IntendedBlock struct {
			Entries []string `json:"entries"`
		} `json:"intended_block"`
	}
	executeCronJSON(t, []string{"--browser-mode", "headed", "cron", "install", "--dry-run", "--state-dir", stateDir, "--json"}, &got)
	if !got.OK || !got.Changed || got.Installed || !got.DryRun || len(got.IntendedBlock.Entries) != 2 {
		t.Fatalf("cron install headed dry-run = %+v, want headed keepalive plus daily artifact prune without install", got)
	}
	entry := got.IntendedBlock.Entries[0]
	if !strings.Contains(entry, "--browser-mode headed daemon keepalive --auto-connect --repair --probe passive") || strings.Contains(entry, "--browser-mode headless") || strings.Contains(entry, "cron heal headed") || strings.Contains(entry, " pages ") {
		t.Fatalf("headed dry-run entry = %q, want headed daemon keepalive only", entry)
	}
	if after := readFileString(t, crontabPath); after != initial {
		t.Fatalf("dry-run mutated crontab:\n%s", after)
	}
}

func TestCronInstallWarnsWhenPreservingPagesPollingHack(t *testing.T) {
	initial := "SHELL=/bin/sh\n* * * * * $HOME/.local/bin/cdp pages --browser-mode headed >/dev/null 2>&1\n"
	crontabPath, crontabBin := fakeCrontab(t, initial)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var got struct {
		OK       bool     `json:"ok"`
		DryRun   bool     `json:"dry_run"`
		Warnings []string `json:"warnings"`
	}
	executeCronJSON(t, []string{"cron", "install", "--dry-run", "--state-dir", stateDir, "--json"}, &got)
	if !got.OK || !got.DryRun || !containsString(got.Warnings, "current crontab contains unmanaged cdp pages polling; cron install preserves unmanaged lines, so remove the manual pages loop after managed keepalive is verified") {
		t.Fatalf("cron install dry-run = %+v, want pages polling preservation warning", got)
	}
	if after := readFileString(t, crontabPath); after != initial {
		t.Fatalf("dry-run mutated crontab:\n%s", after)
	}
}

func TestCronMigratePagesPollingDryRunReportsCandidatesAndPreservesCrontab(t *testing.T) {
	initial := "SHELL=/bin/sh\n0 0 * * * /usr/local/bin/backup\n* * * * * $HOME/.local/bin/cdp pages --browser-mode headed >/dev/null 2>&1\n"
	crontabPath, crontabBin := fakeCrontab(t, initial)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var got struct {
		OK                        bool     `json:"ok"`
		Action                    string   `json:"action"`
		Changed                   bool     `json:"changed"`
		DryRun                    bool     `json:"dry_run"`
		Applied                   bool     `json:"applied"`
		CandidateCount            int      `json:"candidate_count"`
		RemovedCount              int      `json:"removed_count"`
		ManagedKeepaliveInstalled bool     `json:"managed_keepalive_installed"`
		CandidateEntries          []string `json:"candidate_entries"`
		RemovedEntries            []string `json:"removed_entries"`
		Warnings                  []string `json:"warnings"`
		NextCommands              []string `json:"next_commands"`
	}
	executeCronJSON(t, []string{"cron", "migrate", "pages-polling", "--state-dir", stateDir, "--json"}, &got)
	if !got.OK || got.Action != "would_remove" || !got.Changed || !got.DryRun || got.Applied || got.CandidateCount != 1 || got.RemovedCount != 0 || got.ManagedKeepaliveInstalled {
		t.Fatalf("cron migrate pages-polling dry-run = %+v, want one unapplied candidate and no managed keepalive", got)
	}
	if len(got.CandidateEntries) != 1 || !strings.Contains(got.CandidateEntries[0], "cdp pages --browser-mode headed") {
		t.Fatalf("candidate entries = %+v, want pages polling line", got.CandidateEntries)
	}
	if len(got.RemovedEntries) != 0 {
		t.Fatalf("removed entries = %+v, want none on dry-run", got.RemovedEntries)
	}
	if !containsString(got.Warnings, "managed daemon keepalive is not installed; run cdp cron install --json and verify cdp cron status before applying this migration") {
		t.Fatalf("warnings = %+v, want managed keepalive prerequisite", got.Warnings)
	}
	if !containsString(got.NextCommands, "cdp cron install --json") {
		t.Fatalf("next commands = %+v, want cron install guidance", got.NextCommands)
	}
	if after := readFileString(t, crontabPath); after != initial {
		t.Fatalf("dry-run mutated crontab:\n%s", after)
	}
}

func TestCronMigratePagesPollingApplyRequiresManagedKeepalive(t *testing.T) {
	initial := "SHELL=/bin/sh\n* * * * * $HOME/.local/bin/cdp pages --browser-mode headed >/dev/null 2>&1\n"
	crontabPath, crontabBin := fakeCrontab(t, initial)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var stdout, stderr bytes.Buffer
	code := cli.Execute(context.Background(), []string{"cron", "migrate", "pages-polling", "--apply", "--state-dir", stateDir, "--json"}, &stdout, &stderr, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("cron migrate pages-polling --apply exit = %d, want %d; stderr=%s stdout=%s", code, cli.ExitUsage, stderr.String(), stdout.String())
	}
	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		ErrClass            string   `json:"err_class"`
		Message             string   `json:"message"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode apply error output: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != "managed_keepalive_required" || got.ErrClass != "usage" || !strings.Contains(got.Message, "cdp cron install --json") {
		t.Fatalf("error envelope = %+v, want managed_keepalive_required usage guidance", got)
	}
	if !containsString(got.RemediationCommands, "cdp cron install --json") {
		t.Fatalf("remediation commands = %+v, want cron install", got.RemediationCommands)
	}
	if after := readFileString(t, crontabPath); after != initial {
		t.Fatalf("failed apply mutated crontab:\n%s", after)
	}
}

func TestCronMigratePagesPollingApplyRemovesOnlyLegacyAfterManagedInstalled(t *testing.T) {
	initial := "SHELL=/bin/sh\n0 0 * * * /usr/local/bin/backup\n* * * * * $HOME/.local/bin/cdp pages --browser-mode headed >/dev/null 2>&1\n"
	crontabPath, crontabBin := fakeCrontab(t, initial)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var install cronInstallResult
	executeCronJSON(t, []string{"cron", "install", "--state-dir", stateDir, "--json"}, &install)
	if !install.OK || !install.Changed {
		t.Fatalf("cron install = %+v, want changed", install)
	}

	var got struct {
		OK                        bool     `json:"ok"`
		Action                    string   `json:"action"`
		Changed                   bool     `json:"changed"`
		DryRun                    bool     `json:"dry_run"`
		Applied                   bool     `json:"applied"`
		CandidateCount            int      `json:"candidate_count"`
		RemovedCount              int      `json:"removed_count"`
		ManagedKeepaliveInstalled bool     `json:"managed_keepalive_installed"`
		RemovedEntries            []string `json:"removed_entries"`
	}
	executeCronJSON(t, []string{"cron", "migrate", "pages-polling", "--apply", "--state-dir", stateDir, "--json"}, &got)
	if !got.OK || got.Action != "removed" || !got.Changed || got.DryRun || !got.Applied || got.CandidateCount != 1 || got.RemovedCount != 1 || !got.ManagedKeepaliveInstalled {
		t.Fatalf("cron migrate pages-polling apply = %+v, want applied removal with managed keepalive", got)
	}
	if len(got.RemovedEntries) != 1 || !strings.Contains(got.RemovedEntries[0], "cdp pages --browser-mode headed") {
		t.Fatalf("removed entries = %+v, want pages polling line", got.RemovedEntries)
	}
	after := readFileString(t, crontabPath)
	if strings.Contains(after, " cdp pages ") {
		t.Fatalf("migration left pages polling line behind:\n%s", after)
	}
	if !strings.Contains(after, "SHELL=/bin/sh\n0 0 * * * /usr/local/bin/backup\n") {
		t.Fatalf("migration did not preserve unmanaged backup line:\n%s", after)
	}
	for _, want := range []string{"# cdp-cli managed browser runtime tasks", "--browser-mode headed daemon keepalive --auto-connect --repair --probe passive", "--browser-mode headless daemon maintenance --profile-seed-strategy managed --profile-seed-if-older-than 6h", "# End cdp-cli managed browser runtime tasks"} {
		if !strings.Contains(after, want) {
			t.Fatalf("migration did not preserve managed block content %q:\n%s", want, after)
		}
	}
}

func TestCronRemoveOnlyRemovesManagedBlock(t *testing.T) {
	initial := strings.Join([]string{
		"SHELL=/bin/sh",
		"# cdp-cli managed browser runtime tasks",
		"* * * * * /usr/bin/flock -n $HOME/.cdp-cli/locks/test.lock $HOME/.local/bin/cdp --browser-mode headless daemon health --json",
		"# End cdp-cli managed browser runtime tasks",
		"0 0 * * * /usr/local/bin/backup",
		"",
	}, "\n")
	crontabPath, crontabBin := fakeCrontab(t, initial)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var got struct {
		OK      bool   `json:"ok"`
		Action  string `json:"action"`
		Changed bool   `json:"changed"`
		Removed bool   `json:"removed"`
	}
	executeCronJSON(t, []string{"cron", "remove", "--state-dir", stateDir, "--json"}, &got)
	if !got.OK || !got.Changed || got.Action != "removed" || !got.Removed {
		t.Fatalf("cron remove = %+v, want removed", got)
	}
	after := readFileString(t, crontabPath)
	if strings.Contains(after, "cdp-cli managed browser runtime tasks") || strings.Contains(after, "daemon health") {
		t.Fatalf("cron remove left managed block behind:\n%s", after)
	}
	if after != "SHELL=/bin/sh\n0 0 * * * /usr/local/bin/backup\n" {
		t.Fatalf("cron remove changed unmanaged lines: %q", after)
	}
}

func TestCronStatusAndDiffUseFakeCrontab(t *testing.T) {
	_, crontabBin := fakeCrontab(t, "")
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var status struct {
		OK        bool   `json:"ok"`
		State     string `json:"state"`
		Installed bool   `json:"installed"`
		Health    struct {
			State              string   `json:"state"`
			Status             string   `json:"status"`
			IssueCount         int      `json:"issue_count"`
			RecommendedCommand string   `json:"recommended_command"`
			NextCommands       []string `json:"next_commands"`
		} `json:"health"`
		NextCommands []string `json:"next_commands"`
	}
	executeCronJSON(t, []string{"cron", "status", "--state-dir", stateDir, "--json"}, &status)
	if !status.OK || status.Installed || status.State != "not_installed" || status.Health.State != "not_installed" || status.Health.Status != "warn" || status.Health.IssueCount != 1 {
		t.Fatalf("cron status = %+v, want ok not_installed warning", status)
	}
	if status.Health.RecommendedCommand != "cdp cron install --json" || !containsString(status.NextCommands, "cdp cron install --json") {
		t.Fatalf("cron status next commands = %+v health=%+v, want install guidance", status.NextCommands, status.Health)
	}

	var diff struct {
		OK        bool `json:"ok"`
		Installed bool `json:"installed"`
		Actions   []struct {
			Action string `json:"action"`
		} `json:"actions"`
	}
	executeCronJSON(t, []string{"cron", "diff", "--state-dir", stateDir, "--json"}, &diff)
	if !diff.OK || diff.Installed || len(diff.Actions) != 1 || diff.Actions[0].Action != "append_managed_block" {
		t.Fatalf("cron diff = %+v, want append action", diff)
	}
}

func TestCronStatusBlocksHeadlessLaunchTasksWithoutManagedProcessSweep(t *testing.T) {
	legacy := strings.Join([]string{
		"SHELL=/bin/sh",
		"# cdp-cli managed browser runtime tasks",
		"* * * * * cdp_lock=$HOME/.cdp-cli/locks/keepalive-headless.lock; flock -n \"$cdp_lock\" $HOME/.local/bin/cdp --browser-mode headless daemon keepalive --repair --force --json",
		"* * * * * cdp_lock=$HOME/.cdp-cli/locks/headless-health.lock; flock -n \"$cdp_lock\" $HOME/.local/bin/cdp --browser-mode headless daemon health-check --repair --force --json",
		"# End cdp-cli managed browser runtime tasks",
		"",
	}, "\n")
	_, crontabBin := fakeCrontab(t, legacy)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)

	var status struct {
		OK              bool   `json:"ok"`
		State           string `json:"state"`
		MatchesIntended bool   `json:"matches_intended"`
		Tasks           []struct {
			ID                           string `json:"id"`
			Status                       string `json:"status"`
			Installed                    bool   `json:"installed"`
			MatchesIntended              bool   `json:"matches_intended"`
			RequiresManagedProcessSweep  bool   `json:"requires_managed_process_sweep"`
			ManagedProcessSweepInstalled bool   `json:"managed_process_sweep_installed"`
		} `json:"tasks"`
		ScheduledTasks struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details struct {
				BlockedManagedTaskIDs                       []string `json:"blocked_managed_task_ids"`
				HasManagedProcessSweep                      bool     `json:"has_managed_process_sweep"`
				HasHeadlessLaunchWithoutManagedProcessSweep bool     `json:"has_headless_launch_without_managed_process_sweep"`
			} `json:"details"`
		} `json:"scheduled_tasks"`
	}
	executeCronJSON(t, []string{"cron", "status", "--state-dir", stateDir, "--json"}, &status)
	if !status.OK || status.MatchesIntended || status.State != "needs_update" {
		t.Fatalf("cron status = %+v, want needs_update for legacy block without sweep", status)
	}
	for _, task := range status.Tasks {
		if task.ID == "headless-maintenance" {
			if task.Installed || task.Status != "missing" || !task.RequiresManagedProcessSweep || task.ManagedProcessSweepInstalled {
				t.Fatalf("headless maintenance task = %+v, want missing current task with sweep requirement", task)
			}
		}
	}
	if len(status.ScheduledTasks.Details.BlockedManagedTaskIDs) != 0 {
		t.Fatalf("blocked managed task ids = %+v, want none for legacy tasks outside current managed model", status.ScheduledTasks.Details.BlockedManagedTaskIDs)
	}
	if status.ScheduledTasks.Status != "warn" || !strings.Contains(status.ScheduledTasks.Message, "managed process sweep") || status.ScheduledTasks.Details.HasManagedProcessSweep || !status.ScheduledTasks.Details.HasHeadlessLaunchWithoutManagedProcessSweep {
		t.Fatalf("scheduled tasks = %+v, want sweep warning", status.ScheduledTasks)
	}
}

func TestCronStatusSummarizesManagedBlockMismatchAndStaleLocks(t *testing.T) {
	oldBlock := strings.Join([]string{
		"SHELL=/bin/sh",
		"# cdp-cli managed browser runtime tasks",
		"* * * * * $HOME/.local/bin/cdp --browser-mode headed cron heal headed --json",
		"# End cdp-cli managed browser runtime tasks",
		"",
	}, "\n")
	_, crontabBin := fakeCrontab(t, oldBlock)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)
	writeStaleCronLock(t, stateDir, "keepalive-headless")

	var status struct {
		OK              bool   `json:"ok"`
		State           string `json:"state"`
		Installed       bool   `json:"installed"`
		MatchesIntended bool   `json:"matches_intended"`
		Health          struct {
			State                   string   `json:"state"`
			Status                  string   `json:"status"`
			IssueCount              int      `json:"issue_count"`
			StaleLockCount          int      `json:"stale_lock_count"`
			StaleLocks              []string `json:"stale_locks"`
			RecommendedCommand      string   `json:"recommended_command"`
			StaleDaemonLockCount    int      `json:"stale_daemon_lock_count"`
			StaleDaemonLocks        []string `json:"stale_daemon_locks"`
			NextCommands            []string `json:"next_commands"`
			ManagedBlockIssueStates []struct {
				State              string   `json:"state"`
				StaleLocks         []string `json:"stale_locks"`
				RecommendedCommand string   `json:"recommended_command"`
			} `json:"issues"`
		} `json:"health"`
		NextCommands []string `json:"next_commands"`
	}
	executeCronJSON(t, []string{"cron", "status", "--state-dir", stateDir, "--json"}, &status)
	if !status.OK || !status.Installed || status.MatchesIntended || status.State != "needs_update" || status.Health.State != "needs_update" || status.Health.Status != "warn" {
		t.Fatalf("cron status = %+v, want installed needs_update warning", status)
	}
	if status.Health.IssueCount != 2 || status.Health.StaleLockCount != 1 || !containsString(status.Health.StaleLocks, "keepalive-headless") || status.Health.StaleDaemonLockCount != 0 || len(status.Health.StaleDaemonLocks) != 0 {
		t.Fatalf("cron status health = %+v, want one stale wrapper lock plus needs_update issue", status.Health)
	}
	if status.Health.RecommendedCommand != "cdp cron install --json" || status.NextCommands[0] != "cdp cron install --json" {
		t.Fatalf("cron status recommended command = %q next=%+v, want install first for stale managed block", status.Health.RecommendedCommand, status.NextCommands)
	}
	var staleIssue struct {
		Found              bool
		RecommendedCommand string
	}
	for _, issue := range status.Health.ManagedBlockIssueStates {
		if issue.State == "stale_locks" {
			staleIssue.Found = true
			staleIssue.RecommendedCommand = issue.RecommendedCommand
		}
	}
	if !staleIssue.Found || staleIssue.RecommendedCommand != "cdp --browser-mode headless daemon maintenance --stale-lock-after 1s --json" {
		t.Fatalf("stale lock issue = %+v, want headless repair guidance", staleIssue)
	}
}

func TestCronStatusClassifiesDeadDaemonKeepaliveLock(t *testing.T) {
	_, crontabBin := fakeCrontab(t, "")
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)
	lockName := "daemon-keepalive-headless-browser_url-headless"
	writeKeepaliveLock(t, stateDir, lockName, exitedProcessPID(t), "checking")

	var status struct {
		OK          bool `json:"ok"`
		DaemonLocks map[string]struct {
			Exists       bool     `json:"exists"`
			Stale        bool     `json:"stale"`
			StaleReason  string   `json:"stale_reason"`
			OwnerRunning bool     `json:"owner_running"`
			PID          int      `json:"pid"`
			Phase        string   `json:"phase"`
			NextCommands []string `json:"next_commands"`
		} `json:"daemon_locks"`
		Health struct {
			StaleDaemonLockCount int      `json:"stale_daemon_lock_count"`
			StaleDaemonLocks     []string `json:"stale_daemon_locks"`
		} `json:"health"`
	}
	executeCronJSON(t, []string{"cron", "status", "--state-dir", stateDir, "--json"}, &status)
	lock, ok := status.DaemonLocks[lockName]
	if !status.OK || !ok || !lock.Exists || !lock.Stale || lock.StaleReason != "owner_process_not_running" || lock.OwnerRunning || lock.PID == 0 || lock.Phase != "checking" {
		t.Fatalf("cron status daemon lock = %+v ok=%v statusOK=%v, want dead-owner stale classification", lock, ok, status.OK)
	}
	if status.Health.StaleDaemonLockCount != 1 || !containsString(status.Health.StaleDaemonLocks, lockName) {
		t.Fatalf("cron status health = %+v, want stale daemon lock summary", status.Health)
	}
	if !containsString(lock.NextCommands, "cdp --browser-mode headless daemon maintenance --stale-lock-after 1s --json") {
		t.Fatalf("next commands = %+v, want safe headless stale-lock repair", lock.NextCommands)
	}
}

func TestCronStatusIgnoresOldEmptyFlockLockMarkers(t *testing.T) {
	crontab := strings.Join([]string{
		"# cdp-cli managed browser runtime tasks",
		"* * * * * cdp_lock=$HOME/.cdp-cli/locks/keepalive-headless.lock; flock -n \"$cdp_lock\" true",
		"# End cdp-cli managed browser runtime tasks",
	}, "\n")
	_, crontabBin := fakeCrontab(t, crontab)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)
	writeOldFlockMarker(t, stateDir, "keepalive-headless")

	var status struct {
		OK    bool `json:"ok"`
		Locks map[string]struct {
			Exists bool   `json:"exists"`
			Stale  bool   `json:"stale"`
			Marker string `json:"marker"`
		} `json:"locks"`
		Health struct {
			StaleLockCount int      `json:"stale_lock_count"`
			StaleLocks     []string `json:"stale_locks"`
		} `json:"health"`
	}
	executeCronJSON(t, []string{"cron", "status", "--state-dir", stateDir, "--json"}, &status)
	lock := status.Locks["keepalive-headless"]
	if !status.OK || !lock.Exists || lock.Stale || lock.Marker != "flock_lockfile" {
		t.Fatalf("cron lock = %+v statusOK=%v, want old empty flock marker without stale warning", lock, status.OK)
	}
	if status.Health.StaleLockCount != 0 || len(status.Health.StaleLocks) != 0 {
		t.Fatalf("cron health = %+v, want no stale locks for empty flock marker", status.Health)
	}
}

func TestCronStatusDoesNotClassifyFreshHeldEmptyFlockMarkerAsStale(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock is not available")
	}
	crontab := strings.Join([]string{
		"# cdp-cli managed browser runtime tasks",
		"* * * * * cdp_lock=$HOME/.cdp-cli/locks/keepalive-headless.lock; flock --close -n \"$cdp_lock\" true",
		"# End cdp-cli managed browser runtime tasks",
	}, "\n")
	_, crontabBin := fakeCrontab(t, crontab)
	t.Setenv("CDP_CRONTAB_BIN", crontabBin)
	stateDir := shortCLIStateDir(t)
	path := filepath.Join(stateDir, "locks", "keepalive-headless.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("write flock marker: %v", err)
	}
	old := time.Now().Add(-20 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("age flock marker: %v", err)
	}
	lockFile, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open flock marker: %v", err)
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
	}()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold flock marker: %v", err)
	}

	var status struct {
		OK    bool `json:"ok"`
		Locks map[string]struct {
			Exists       bool     `json:"exists"`
			Stale        bool     `json:"stale"`
			Marker       string   `json:"marker"`
			Locked       bool     `json:"locked"`
			StaleReason  string   `json:"stale_reason"`
			LockOwnerPID int      `json:"lock_owner_pid"`
			NextCommands []string `json:"next_commands"`
		} `json:"locks"`
		Health struct {
			StaleLockCount int      `json:"stale_lock_count"`
			StaleLocks     []string `json:"stale_locks"`
		} `json:"health"`
	}
	executeCronJSON(t, []string{"cron", "status", "--state-dir", stateDir, "--json"}, &status)
	lock := status.Locks["keepalive-headless"]
	if !status.OK || !lock.Exists || lock.Marker != "flock_lockfile" || !lock.Locked || lock.Stale || lock.StaleReason != "" {
		t.Fatalf("cron lock = %+v statusOK=%v, want fresh active flock lock without stale warning", lock, status.OK)
	}
	if lock.LockOwnerPID != os.Getpid() {
		t.Fatalf("lock owner pid = %d, want %d", lock.LockOwnerPID, os.Getpid())
	}
	if status.Health.StaleLockCount != 0 || len(status.Health.StaleLocks) != 0 {
		t.Fatalf("cron health = %+v, want no stale lock summary", status.Health)
	}
}

type cronInstallResult struct {
	OK        bool   `json:"ok"`
	Action    string `json:"action"`
	Changed   bool   `json:"changed"`
	Installed bool   `json:"installed"`
}

func executeCronJSON(t *testing.T, args []string, out any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Execute(context.Background(), args, &stdout, &stderr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute(%v) exit = %d, want 0; stderr=%s stdout=%s", args, code, stderr.String(), stdout.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		t.Fatalf("decode output for %v: %v\n%s", args, err, stdout.String())
	}
}

func fakeCrontab(t *testing.T, initial string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	store := filepath.Join(dir, "crontab.txt")
	if err := os.WriteFile(store, []byte(initial), 0o600); err != nil {
		t.Fatalf("write fake crontab store: %v", err)
	}
	bin := filepath.Join(dir, "crontab")
	script := `#!/bin/sh
set -eu
store="$CDP_FAKE_CRONTAB"
if [ "$#" -eq 1 ] && [ "$1" = "-l" ]; then
  cat "$store"
  exit 0
fi
if [ "$#" -eq 1 ]; then
  cat "$1" > "$store"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake crontab binary: %v", err)
	}
	t.Setenv("CDP_FAKE_CRONTAB", store)
	return store, bin
}

func writeStaleCronLock(t *testing.T, stateDir, name string) {
	t.Helper()
	path := filepath.Join(stateDir, "locks", name+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"name":       name,
		"pid":        exitedProcessPID(t),
		"started_at": time.Now().Add(-20 * time.Minute).UTC().Format(time.RFC3339),
		"phase":      "checking",
	})
	if err != nil {
		t.Fatalf("marshal stale cron lock: %v", err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write stale cron lock: %v", err)
	}
	old := time.Now().Add(-20 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("age stale cron lock: %v", err)
	}
}

func writeOldFlockMarker(t *testing.T, stateDir, name string) {
	t.Helper()
	path := filepath.Join(stateDir, "locks", name+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("write flock marker: %v", err)
	}
	old := time.Now().Add(-20 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("age flock marker: %v", err)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
