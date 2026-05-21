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
	if !strings.Contains(afterFirst, "cron heal headed") || !strings.Contains(afterFirst, "/usr/bin/flock -n") {
		t.Fatalf("crontab after install missing headed heal or flock entries:\n%s", afterFirst)
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
