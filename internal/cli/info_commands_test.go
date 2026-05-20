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

func TestSummarizeCrontabIgnoresUnrelatedEntries(t *testing.T) {
	got := summarizeCrontab(`
SHELL=/bin/sh
0 0 * * * /usr/local/bin/backup
`)
	if got.EntryCount != 0 || got.HasDaemonKeepalive || got.HasPageCleanup {
		t.Fatalf("summarizeCrontab = %+v, want no cdp entries", got)
	}
}
