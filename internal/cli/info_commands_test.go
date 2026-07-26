package cli

import "testing"

func TestSummarizeCrontabDetectsUserLevelCDPTasks(t *testing.T) {
	crontab := `
# cdp examples should not count:
# * * * * * $HOME/.local/bin/cdp daemon keepalive --json
* * * * * flock -n $HOME/.cdp-cli/locks/keepalive-headed.lock env DISPLAY=:0 XDG_RUNTIME_DIR=/run/user/1000 $HOME/.local/bin/cdp --browser-mode headed daemon keepalive --auto-connect --repair --display :0 --json >> $HOME/.cdp-cli/keepalive.log 2>&1
*/15 * * * * flock -n $HOME/.cdp-cli/locks/page-cleanup-headless.lock $HOME/.local/bin/cdp --browser-mode headless page cleanup --workflow-created --close --json
* * * * * crontab -l | grep cdp
`
	got := summarizeCrontab(crontab)
	if got.EntryCount != 2 || !got.HasDaemonKeepalive || !got.HasPageCleanup {
		t.Fatalf("summarizeCrontab = %+v, want keepalive and cleanup entries only", got)
	}
}

func TestSummarizeCrontabDetectsPagesPollingKeepaliveHack(t *testing.T) {
	got := summarizeCrontab(`
* * * * * /bin/sh -c 'for i in 1 2 3 4 5 6 7 8 9 10 11 12; do nohup $HOME/.local/bin/cdp pages --browser-mode headed >/dev/null 2>&1 & sleep 5; done'
*/5 * * * * $HOME/.local/bin/cdp --browser-mode headless pages --json >/dev/null 2>&1
`)
	if got.EntryCount != 2 ||
		!got.HasPagesPollingKeepalive ||
		!got.HasHeadedPagesPolling ||
		!got.HasHeadlessPagesPolling ||
		got.PagesPollingCount != 2 ||
		!got.HasUnflockedCDPTask {
		t.Fatalf("summarizeCrontab = %+v, want headed/headless pages polling hack detection", got)
	}
}

func TestScheduledTasksDoctorCheckReportsCleanupTask(t *testing.T) {
	check := scheduledTasksStatusForSummary(true, nil, crontabSummary{EntryCount: 2, HasDaemonKeepalive: true, HasHeadlessDaemonKeepalive: true, HasPageCleanup: true, HasModeExplicitPageCleanup: true})
	if check["status"] != "pass" || check["message"] != "user crontab includes exclusively locked cdp daemon maintenance/keepalive and mode-explicit cleanup" {
		t.Fatalf("scheduled task check = %+v, want pass message for exclusively locked mode-explicit keepalive and cleanup", check)
	}
	next, ok := check["next_commands"].([]string)
	if !ok || !testContainsString(next, "cdp cron status --json") || !testContainsString(next, "cdp cron install --json") || !testContainsString(next, "cdp --browser-mode headless daemon maintenance --dry-run --json") {
		t.Fatalf("scheduled task next_commands = %+v, want built-in cdp cron management commands", check["next_commands"])
	}
	details, ok := check["details"].(map[string]any)
	if !ok {
		t.Fatalf("scheduled task details = %+v, want details map", check["details"])
	}
	tasks, ok := details["tasks"].([]cronTaskStatus)
	if !ok || len(tasks) != 0 {
		t.Fatalf("scheduled task details tasks = %+v, want empty task array for hand-built summary", details["tasks"])
	}
}

func TestSummarizeCrontabDetectsHeadlessMaintenance(t *testing.T) {
	got := summarizeCrontab(`
* * * * * flock -n $HOME/.cdp-cli/locks/headless-maintenance.lock $HOME/.local/bin/cdp --browser-mode headless daemon maintenance --profile-seed-strategy managed --profile-seed-if-older-than 6h --json
`)
	if got.EntryCount != 1 ||
		!got.HasDaemonKeepalive ||
		!got.HasHeadlessDaemonKeepalive ||
		!got.HasPageCleanup ||
		!got.HasModeExplicitPageCleanup ||
		!got.HasManagedProcessSweep ||
		got.HasHeadlessLaunchWithoutManagedProcessSweep ||
		got.HasUnflockedCDPTask {
		t.Fatalf("summarizeCrontab = %+v, want flocked headless maintenance as keepalive, cleanup, and sweep", got)
	}
	if len(got.TaskStatuses) != 3 || got.TaskStatuses[1].ID != "headless-maintenance" || !got.TaskStatuses[1].RequiresManagedProcessSweep || got.TaskStatuses[2].ID != "artifact-prune" {
		t.Fatalf("summarizeCrontab task statuses = %+v, want headless maintenance task model with sweep requirement", got.TaskStatuses)
	}
}

func TestScheduledTasksDoctorCheckWarnsForPagesPollingKeepalive(t *testing.T) {
	check := scheduledTasksStatusForSummary(true, nil, crontabSummary{EntryCount: 1, HasPagesPollingKeepalive: true, HasHeadedPagesPolling: true, PagesPollingCount: 1})
	if check["status"] != "warn" || check["message"] != "current user crontab uses cdp pages polling; install managed daemon keepalive instead" {
		t.Fatalf("scheduled task check = %+v, want pages polling warning", check)
	}
	details, ok := check["details"].(map[string]any)
	if !ok ||
		details["has_pages_polling_keepalive"] != true ||
		details["has_headed_pages_polling"] != true ||
		details["pages_polling_count"] != 1 {
		t.Fatalf("scheduled task details = %+v, want pages polling details", check["details"])
	}
}

func TestScheduledTasksDoctorCheckWarnsForUnflockedCDPTask(t *testing.T) {
	check := scheduledTasksStatusForSummary(true, nil, crontabSummary{EntryCount: 2, HasDaemonKeepalive: true, HasHeadlessDaemonKeepalive: true, HasPageCleanup: true, HasModeExplicitPageCleanup: true, HasUnflockedCDPTask: true})
	if check["status"] != "warn" || check["message"] != "current user crontab has cdp daemon or cleanup tasks without exclusive locking" {
		t.Fatalf("scheduled task check = %+v, want unlocked task warning", check)
	}
	details, ok := check["details"].(map[string]any)
	if !ok || details["has_unflocked_cdp_task"] != true {
		t.Fatalf("scheduled task details = %+v, want unflocked detail", check["details"])
	}
}

func TestScheduledTasksDoctorCheckWarnsWithoutCleanupTask(t *testing.T) {
	check := scheduledTasksStatusForSummary(true, nil, crontabSummary{EntryCount: 1, HasDaemonKeepalive: true})
	if check["status"] != "warn" || check["message"] != "current user crontab has cdp daemon keepalive but no page cleanup task" {
		t.Fatalf("scheduled task check = %+v, want cleanup warning", check)
	}
}

func TestScheduledTasksDoctorCheckWarnsForAmbiguousCleanupTask(t *testing.T) {
	check := scheduledTasksStatusForSummary(true, nil, crontabSummary{EntryCount: 2, HasDaemonKeepalive: true, HasPageCleanup: true, HasAmbiguousPageCleanup: true})
	if check["status"] != "warn" || check["message"] != "current user crontab has page cleanup task without explicit browser mode" {
		t.Fatalf("scheduled task check = %+v, want ambiguous cleanup warning", check)
	}
	details, ok := check["details"].(map[string]any)
	if !ok || details["has_ambiguous_page_cleanup"] != true || details["has_mode_explicit_page_cleanup"] != false {
		t.Fatalf("scheduled task details = %+v, want ambiguous cleanup details", check["details"])
	}
}

func TestScheduledTaskUsesFlock(t *testing.T) {
	if !scheduledTaskUsesFlock("* * * * * /usr/bin/flock -n $HOME/.cdp-cli/locks/cdp.lock cdp daemon keepalive --json") {
		t.Fatalf("scheduledTaskUsesFlock returned false for flocked command")
	}
	if !scheduledTaskUsesFlock("* * * * * $HOME/.local/bin/cdp cron run headed-daemon-keepalive --json") {
		t.Fatalf("scheduledTaskUsesFlock returned false for Go-owned cron runner")
	}
	if scheduledTaskUsesFlock("* * * * * cdp daemon keepalive --json") {
		t.Fatalf("scheduledTaskUsesFlock returned true for unflocked command")
	}
}

func TestScheduledTaskBrowserModeParsesFlagAndEnvForms(t *testing.T) {
	tests := map[string]string{
		"* * * * * cdp --browser-mode headless daemon keepalive --repair --json": "headless",
		"* * * * * cdp --browserMode headed page cleanup --json":                 "headed",
		"* * * * * cdp --browser-mode=headless daemon keepalive --repair --json": "headless",
		"* * * * * CDP_BROWSER_MODE=headed cdp daemon keepalive --repair --json": "headed",
		"* * * * * cdp daemon keepalive --repair --json":                         "",
		"* * * * * cdp --browser-mode headed cron heal headed --json":            "headed",
	}
	for line, want := range tests {
		if got := scheduledTaskBrowserMode(line); got != want {
			t.Fatalf("scheduledTaskBrowserMode(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestSummarizeCrontabIgnoresUnrelatedEntries(t *testing.T) {
	got := summarizeCrontab(`
SHELL=/bin/sh
0 0 * * * /usr/local/bin/backup
`)
	if got.EntryCount != 0 || got.HasDaemonKeepalive || got.HasPageCleanup {
		t.Fatalf("summarizeCrontab = %+v, want no cdp entries", got)
	}
}

func testContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
