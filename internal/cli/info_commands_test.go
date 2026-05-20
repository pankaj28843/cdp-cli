package cli

import "testing"

func TestSummarizeCrontabDetectsUserLevelCDPTasks(t *testing.T) {
	crontab := `
# cdp examples should not count:
# * * * * * $HOME/.local/bin/cdp daemon keepalive --json
* * * * * DISPLAY=:0 XDG_RUNTIME_DIR=/run/user/1000 $HOME/.local/bin/cdp daemon keepalive --auto-connect --repair --display :0 --json >> $HOME/.cdp-cli/keepalive.log 2>&1
*/15 * * * * $HOME/.local/bin/cdp page cleanup --workflow-created --close --json
* * * * * crontab -l | grep cdp
`
	got := summarizeCrontab(crontab)
	if got.EntryCount != 2 || !got.HasDaemonKeepalive || !got.HasPageCleanup {
		t.Fatalf("summarizeCrontab = %+v, want keepalive and cleanup entries only", got)
	}
}

func TestScheduledTasksDoctorCheckReportsCleanupTask(t *testing.T) {
	check := scheduledTasksStatusForSummary(true, nil, crontabSummary{EntryCount: 2, HasDaemonKeepalive: true, HasPageCleanup: true})
	if check["status"] != "pass" || check["message"] != "user crontab includes cdp daemon keepalive and page cleanup" {
		t.Fatalf("scheduled task check = %+v, want pass message for keepalive and cleanup", check)
	}
	next, ok := check["next_commands"].([]string)
	if !ok || !testContainsString(next, `(crontab -l 2>/dev/null | grep -v 'cdp page cleanup'; echo '* * * * * $HOME/.local/bin/cdp page cleanup --created-by cdp --idle-for 30m --close --max 10 --json >> $HOME/.cdp-cli/page-cleanup.log 2>&1') | crontab -`) {
		t.Fatalf("scheduled task next_commands = %+v, want cleanup install command", check["next_commands"])
	}
}

func TestScheduledTasksDoctorCheckWarnsWithoutCleanupTask(t *testing.T) {
	check := scheduledTasksStatusForSummary(true, nil, crontabSummary{EntryCount: 1, HasDaemonKeepalive: true})
	if check["status"] != "warn" || check["message"] != "current user crontab has cdp daemon keepalive but no page cleanup task" {
		t.Fatalf("scheduled task check = %+v, want cleanup warning", check)
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
