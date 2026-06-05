package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for _, want := range []string{"--browser-mode headed daemon keepalive --auto-connect --repair --probe passive", "--browser-mode headless daemon keepalive --repair", "command -v flock", "--strategy managed"} {
		if !strings.Contains(afterFirst, want) {
			t.Fatalf("crontab after install missing %q:\n%s", want, afterFirst)
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
	if !got.OK || !got.Changed || got.Installed || !got.DryRun || len(got.IntendedBlock.Entries) != 1 {
		t.Fatalf("cron install headed dry-run = %+v, want one intended headed entry without install", got)
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
	if !containsString(got.Warnings, "managed daemon keepalive is not installed; run cdp cron install --profile agent --json and verify cdp cron status before applying this migration") {
		t.Fatalf("warnings = %+v, want managed keepalive prerequisite", got.Warnings)
	}
	if !containsString(got.NextCommands, "cdp cron install --profile agent --json") {
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
	if got.OK || got.Code != "managed_keepalive_required" || got.ErrClass != "usage" || !strings.Contains(got.Message, "cdp cron install --profile agent --json") {
		t.Fatalf("error envelope = %+v, want managed_keepalive_required usage guidance", got)
	}
	if !containsString(got.RemediationCommands, "cdp cron install --profile agent --json") {
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
	for _, want := range []string{"# cdp-cli managed browser runtime tasks", "--browser-mode headed daemon keepalive --auto-connect --repair --probe passive", "--browser-mode headless daemon keepalive --repair", "# End cdp-cli managed browser runtime tasks"} {
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
		OK        bool `json:"ok"`
		Installed bool `json:"installed"`
	}
	executeCronJSON(t, []string{"cron", "status", "--state-dir", stateDir, "--json"}, &status)
	if !status.OK || status.Installed {
		t.Fatalf("cron status = %+v, want ok not installed", status)
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

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
