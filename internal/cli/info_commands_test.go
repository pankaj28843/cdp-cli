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

func TestScheduledTasksDoctorCheckReportsCleanupTask(t *testing.T) {
	check := scheduledTasksStatusForSummary(true, nil, crontabSummary{EntryCount: 2, HasDaemonKeepalive: true, HasHeadlessDaemonKeepalive: true, HasPageCleanup: true, HasModeExplicitPageCleanup: true})
	if check["status"] != "pass" || check["message"] != "user crontab includes flocked cdp daemon keepalive and mode-explicit page cleanup" {
		t.Fatalf("scheduled task check = %+v, want pass message for flocked mode-explicit keepalive and cleanup", check)
	}
	next, ok := check["next_commands"].([]string)
	if !ok || !testContainsString(next, "make cron-install") || !testContainsString(next, `(crontab -l 2>/dev/null | grep -v 'cdp page cleanup'; echo '* * * * * flock -n $HOME/.cdp-cli/locks/page-cleanup-headless.lock $HOME/.local/bin/cdp --browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --max 10 --json >> $HOME/.cdp-cli/page-cleanup-headless.log 2>&1') | crontab -`) {
		t.Fatalf("scheduled task next_commands = %+v, want make and flocked mode-explicit cleanup install commands", check["next_commands"])
	}
}

func TestScheduledTasksDoctorCheckWarnsForUnflockedCDPTask(t *testing.T) {
	check := scheduledTasksStatusForSummary(true, nil, crontabSummary{EntryCount: 2, HasDaemonKeepalive: true, HasHeadlessDaemonKeepalive: true, HasPageCleanup: true, HasModeExplicitPageCleanup: true, HasUnflockedCDPTask: true})
	if check["status"] != "warn" || check["message"] != "current user crontab has cdp daemon or cleanup tasks without flock" {
		t.Fatalf("scheduled task check = %+v, want unflocked task warning", check)
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
