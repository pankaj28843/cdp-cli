package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

func TestManagedCronTasksBoundLogsAndScheduleDailyArtifactPrune(t *testing.T) {
	opts := defaultCronRenderOptions()
	tasks := managedCronTasks(opts)
	if len(tasks) != 3 {
		t.Fatalf("managed cron tasks = %d, want headed, headless, and daily artifact prune", len(tasks))
	}
	if opts.ArtifactRetention != artifacts.DefaultRetention || opts.MaxLogSizeBytes != artifacts.DefaultMaxLogSizeBytes {
		t.Fatalf("default artifact policy = %s/%d, want %s/%d", opts.ArtifactRetention, opts.MaxLogSizeBytes, artifacts.DefaultRetention, artifacts.DefaultMaxLogSizeBytes)
	}
	byID := map[string]managedCronTask{}
	for _, task := range tasks {
		byID[task.ID] = task
		if strings.Contains(task.CronEntry, ">> ") {
			t.Fatalf("task %q still append-opens a managed log: %s", task.ID, task.CronEntry)
		}
		if len(task.CronEntry) > cronMaxEntryBytes {
			t.Fatalf("task %q entry is %d bytes, portable maximum is %d", task.ID, len(task.CronEntry), cronMaxEntryBytes)
		}
	}
	for _, id := range []string{cronTaskHeadedDaemonKeepalive, cronTaskHeadlessMaintenance} {
		entry := byID[id].CronEntry
		if !strings.Contains(entry, "cron run "+id) || !strings.Contains(entry, "--max-log-size 64MiB") || strings.Contains(entry, "flock") || strings.Contains(entry, "sh -c") {
			t.Fatalf("task %q entry = %q, want short Go-owned cron runner", id, entry)
		}
	}
	prune := byID[cronTaskArtifactPrune]
	if prune.Schedule != "23 3 * * *" || !strings.Contains(prune.CronEntry, "cron run artifact-prune --artifact-retention 168h --max-log-size 64MiB") {
		t.Fatalf("artifact prune entry = %q schedule=%q, want daily default policy", prune.CronEntry, prune.Schedule)
	}
	if got := cronArtifactPolicy(opts); got.RetentionSeconds != int64((168*time.Hour)/time.Second) || got.MaxLogSizeBytes != 64<<20 {
		t.Fatalf("cron artifact policy = %+v, want exact default values", got)
	}
}

func TestManagedCronTaskChildSpecsKeepHeadedWorkPassive(t *testing.T) {
	opts := defaultCronRenderOptions()
	args, env, err := managedCronTaskChildSpec(cronTaskHeadedDaemonKeepalive, "/tmp/cdp-state", opts)
	if err != nil {
		t.Fatalf("headed child spec: %v", err)
	}
	command := strings.Join(args, " ")
	for _, want := range []string{"--browser-mode headed", "daemon keepalive", "--probe passive", "--auto-connect", "--repair"} {
		if !strings.Contains(command, want) {
			t.Fatalf("headed child command missing %q: %s", want, command)
		}
	}
	for _, forbidden := range []string{" ask ", " login ", " consent ", " click ", " type "} {
		if strings.Contains(" "+command+" ", forbidden) {
			t.Fatalf("headed child command contains human action %q: %s", forbidden, command)
		}
	}
	joinedEnv := "\n" + strings.Join(env, "\n") + "\n"
	if !strings.Contains(joinedEnv, "\nDISPLAY="+opts.Display+"\n") || !strings.Contains(joinedEnv, "\nXDG_RUNTIME_DIR="+opts.XDGRuntimeDir+"\n") {
		t.Fatalf("headed child environment lacks explicit display/runtime values")
	}
}
