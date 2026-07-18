package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestArtifactsPruneDryRunApplyAndIdempotence(t *testing.T) {
	stateDir := t.TempDir()
	oldRun := filepath.Join(stateDir, "headless-health", "20200101T000000Z")
	if err := os.MkdirAll(oldRun, 0o700); err != nil {
		t.Fatalf("mkdir old run: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldRun, "result.json"), []byte("old"), 0o600); err != nil {
		t.Fatalf("write old run: %v", err)
	}
	activeLog := filepath.Join(stateDir, "keepalive-headed.log")
	if err := os.WriteFile(activeLog, []byte(strings.Repeat("x", 65)), 0o600); err != nil {
		t.Fatalf("write active log: %v", err)
	}
	protected := filepath.Join(stateDir, "connections.json")
	if err := os.WriteFile(protected, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write protected state: %v", err)
	}

	dry := executeArtifactPruneJSON(t, []string{"artifacts", "prune", "--older-than", "168h", "--max-log-size", "64B", "--dry-run", "--state-dir", stateDir, "--json"}, cli.ExitOK)
	if dry["ok"] != true || dry["dry_run"] != true || dry["applied"] != false || intJSON(dry["eligible_count"]) != 2 {
		t.Fatalf("dry-run report = %+v", dry)
	}
	if _, err := os.Stat(oldRun); err != nil {
		t.Fatalf("dry-run removed old run: %v", err)
	}
	if info, err := os.Stat(activeLog); err != nil || info.Size() != 65 {
		t.Fatalf("dry-run changed active log info=%v err=%v", info, err)
	}

	applied := executeArtifactPruneJSON(t, []string{"artifacts", "prune", "--older-than", "168h", "--max-log-size", "64B", "--apply", "--state-dir", stateDir, "--json"}, cli.ExitOK)
	if applied["ok"] != true || applied["applied"] != true || intJSON(applied["deleted_count"]) != 1 || intJSON(applied["bounded_count"]) != 1 || intJSON(applied["bytes_reclaimed"]) == 0 {
		t.Fatalf("apply report = %+v", applied)
	}
	if _, err := os.Stat(oldRun); !os.IsNotExist(err) {
		t.Fatalf("apply retained old run: %v", err)
	}
	if info, err := os.Stat(activeLog); err != nil || info.Size() != 64 {
		t.Fatalf("apply did not bound active log info=%v err=%v", info, err)
	}
	if _, err := os.Stat(protected); err != nil {
		t.Fatalf("apply removed protected state: %v", err)
	}
	latest := filepath.Join(stateDir, "artifact-prune", "latest.json")
	if info, err := os.Stat(latest); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("last cleanup summary info=%v err=%v", info, err)
	}

	second := executeArtifactPruneJSON(t, []string{"artifacts", "prune", "--older-than", "168h", "--max-log-size", "64B", "--apply", "--state-dir", stateDir, "--json"}, cli.ExitOK)
	if intJSON(second["eligible_count"]) != 0 || second["action"] != "unchanged" {
		t.Fatalf("second apply report = %+v", second)
	}
}

func TestArtifactsPruneUsesConfigPolicy(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"artifacts":{"retention":"240h","max_log_size":"32MiB"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	got := executeArtifactPruneJSON(t, []string{"--config", configPath, "--state-dir", t.TempDir(), "artifacts", "prune", "--json"}, cli.ExitOK)
	policy, ok := got["policy"].(map[string]any)
	if !ok || policy["retention_seconds"] != float64((240*time.Hour).Seconds()) || policy["max_log_size_bytes"] != float64(32<<20) {
		t.Fatalf("configured policy = %+v", got["policy"])
	}
}

func TestArtifactsRunManagedReplacesAndBoundsLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	stateDir := t.TempDir()
	logPath := filepath.Join(stateDir, "headless-maintenance.log")
	args := []string{"--state-dir", stateDir, "artifacts", "run-managed", "--task", "synthetic", "--log", logPath, "--max-log-size", "128B", "--json", "--", "/bin/sh", "-c", "yes x | head -c 4096"}
	got := executeArtifactPruneJSON(t, args, cli.ExitOK)
	logResult, ok := got["log"].(map[string]any)
	if got["ok"] != true || got["task"] != "synthetic" || !ok || intJSON(logResult["size_bytes"]) != 128 || intJSON(logResult["dropped_bytes"]) == 0 {
		t.Fatalf("run-managed report = %+v", got)
	}
	if info, err := os.Stat(logPath); err != nil || info.Size() != 128 || info.Mode().Perm() != 0o600 {
		t.Fatalf("managed log info=%v err=%v", info, err)
	}
}

func TestArtifactsPruneRejectsConflictingModes(t *testing.T) {
	got := executeArtifactPruneJSON(t, []string{"--state-dir", t.TempDir(), "artifacts", "prune", "--dry-run", "--apply", "--json"}, cli.ExitUsage)
	if got["code"] != "invalid_artifact_prune_mode" {
		t.Fatalf("conflicting mode error = %+v", got)
	}
}

func executeArtifactPruneJSON(t *testing.T, args []string, wantCode int) map[string]any {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Execute(context.Background(), args, &stdout, &stderr, cli.BuildInfo{})
	if code != wantCode {
		t.Fatalf("Execute(%v) exit=%d, want=%d; stdout=%s stderr=%s", args, code, wantCode, stdout.String(), stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v; stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	return got
}

func intJSON(value any) int64 {
	number, _ := value.(float64)
	return int64(number)
}
