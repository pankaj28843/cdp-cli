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
	}
	for _, id := range []string{cronTaskHeadedDaemonKeepalive, cronTaskHeadlessMaintenance} {
		entry := byID[id].CronEntry
		if !strings.Contains(entry, "artifacts run-managed") || !strings.Contains(entry, "--max-log-size 64MiB") {
			t.Fatalf("task %q entry = %q, want bounded latest-run wrapper", id, entry)
		}
	}
	prune := byID[cronTaskArtifactPrune]
	if prune.Schedule != "23 3 * * *" || !strings.Contains(prune.CronEntry, "artifacts prune --older-than 168h --max-log-size 64MiB --apply") {
		t.Fatalf("artifact prune entry = %q schedule=%q, want daily default policy", prune.CronEntry, prune.Schedule)
	}
	if got := cronArtifactPolicy(opts); got.RetentionSeconds != int64((168*time.Hour)/time.Second) || got.MaxLogSizeBytes != 64<<20 {
		t.Fatalf("cron artifact policy = %+v, want exact default values", got)
	}
}
