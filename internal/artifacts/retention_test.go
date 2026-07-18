package artifacts

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultRetentionPolicy(t *testing.T) {
	policy := DefaultRetentionPolicy(t.TempDir())
	if policy.OlderThan != 168*time.Hour {
		t.Fatalf("OlderThan = %v, want 168h", policy.OlderThan)
	}
	if policy.MaxLogSizeBytes != 64<<20 {
		t.Fatalf("MaxLogSizeBytes = %d, want %d", policy.MaxLogSizeBytes, int64(64<<20))
	}
	if got := policy.Summary(); got.Retention != "168h0m0s" || got.RetentionSeconds != 604800 || got.MaxLogSize != "64MiB" || got.LogStrategy != "latest_run_replacement" {
		t.Fatalf("policy summary = %+v", got)
	}
}

func TestRetentionPlanAndApplyAreAllowlistedAndSafe(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	stateDir, err := os.MkdirTemp("/tmp", "cdp-artifacts-")
	if err != nil {
		t.Fatalf("create short state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	outside := filepath.Join(t.TempDir(), "custom-output")
	mustWriteFixture(t, filepath.Join(outside, "old.txt"), "outside", now.Add(-30*24*time.Hour))

	oldRun := filepath.Join(stateDir, "headless-health", now.Add(-169*time.Hour).Format(runTimestampLayout))
	boundaryRun := filepath.Join(stateDir, "headless-health", now.Add(-168*time.Hour).Format(runTimestampLayout))
	freshRun := filepath.Join(stateDir, "headless-health", now.Add(-167*time.Hour).Format(runTimestampLayout))
	futureRun := filepath.Join(stateDir, "headless-health", now.Add(time.Hour).Format(runTimestampLayout))
	maintenanceRun := filepath.Join(stateDir, "headless-maintenance", "health", now.Add(-200*time.Hour).Format(runTimestampLayout))
	for _, path := range []string{oldRun, boundaryRun, freshRun, futureRun, maintenanceRun} {
		mustWriteFixture(t, filepath.Join(path, "result.json"), strings.Repeat("x", 11), now.Add(-30*24*time.Hour))
	}
	mustWriteFixture(t, filepath.Join(stateDir, "headless-health", "malformed-run", "result.json"), "bad", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "headless-health", "latest.json"), "latest", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "headless-maintenance", "latest.json"), "latest", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "connections.json"), "{}", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "daemon.json"), "{}", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "page-cleanup.json"), "{}", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "browser", "headless-profile", "Default", "Cookies"), "protected", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "headless", "managed-processes.json"), "{}", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "locks", "active.lock"), "locked", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "artifact-prune", "latest.json"), "{}", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "unknown-old.txt"), "unknown", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, ".page-cleanup.json.tmp-stale"), "temp", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "keepalive-headless.log"), "legacy", now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "keepalive-headed.log"), strings.Repeat("a", 65), now.Add(-30*24*time.Hour))
	mustWriteFixture(t, filepath.Join(stateDir, "headless-maintenance.log"), strings.Repeat("b", 64), now.Add(-30*24*time.Hour))
	socketPath := filepath.Join(stateDir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("create protected socket fixture: %v", err)
	}
	defer listener.Close()

	symlinkTarget := filepath.Join(t.TempDir(), "outside-target")
	mustWriteFixture(t, symlinkTarget, "do not remove", now.Add(-30*24*time.Hour))
	symlinkRun := filepath.Join(stateDir, "headless-health", now.Add(-300*time.Hour).Format(runTimestampLayout))
	if err := os.Symlink(symlinkTarget, symlinkRun); err != nil {
		t.Fatalf("create symlink fixture: %v", err)
	}

	policy := DefaultRetentionPolicy(stateDir)
	policy.Now = now
	policy.MaxLogSizeBytes = 64
	plan, err := PlanRetention(context.Background(), policy)
	if err != nil {
		t.Fatalf("PlanRetention returned error: %v", err)
	}
	if plan.CutoffTime != now.Add(-168*time.Hour).Format(time.RFC3339) {
		t.Fatalf("CutoffTime = %q", plan.CutoffTime)
	}
	assertPlanItem(t, plan, oldRun, ActionDelete, "older_than_retention")
	assertPlanItem(t, plan, maintenanceRun, ActionDelete, "older_than_retention")
	assertPlanItem(t, plan, boundaryRun, ActionRetain, "at_retention_boundary")
	assertPlanItem(t, plan, freshRun, ActionRetain, "within_retention")
	assertPlanItem(t, plan, futureRun, ActionRetain, "future_timestamp")
	assertPlanItem(t, plan, filepath.Join(stateDir, "headless-health", "malformed-run"), ActionSkip, "malformed_timestamp")
	assertPlanItem(t, plan, filepath.Join(stateDir, "headless-health", "latest.json"), ActionRetain, "protected_latest_summary")
	assertPlanItem(t, plan, filepath.Join(stateDir, "connections.json"), ActionRetain, "protected_state")
	assertPlanItem(t, plan, filepath.Join(stateDir, "daemon.json"), ActionRetain, "protected_state")
	assertPlanItem(t, plan, socketPath, ActionRetain, "protected_state")
	assertPlanItem(t, plan, filepath.Join(stateDir, "browser"), ActionRetain, "protected_state")
	assertPlanItem(t, plan, filepath.Join(stateDir, "headless"), ActionRetain, "protected_state")
	assertPlanItem(t, plan, filepath.Join(stateDir, "locks"), ActionRetain, "protected_state")
	assertPlanItem(t, plan, filepath.Join(stateDir, "artifact-prune"), ActionRetain, "protected_state")
	assertPlanItem(t, plan, filepath.Join(stateDir, "unknown-old.txt"), ActionSkip, "unknown_path")
	assertPlanItem(t, plan, symlinkRun, ActionSkip, "symlink")
	assertPlanItem(t, plan, filepath.Join(stateDir, "keepalive-headed.log"), ActionBoundLog, "log_exceeds_hard_bound")
	assertPlanItem(t, plan, filepath.Join(stateDir, "headless-maintenance.log"), ActionRetain, "active_log_within_bound")
	assertPlanItem(t, plan, filepath.Join(stateDir, "keepalive-headless.log"), ActionDelete, "older_than_retention")
	assertPlanItem(t, plan, filepath.Join(stateDir, ".page-cleanup.json.tmp-stale"), ActionDelete, "older_than_retention")
	if strings.Contains(strings.Join(planPaths(plan), "\n"), outside) {
		t.Fatalf("plan selected custom output outside state root: %+v", plan.Items)
	}

	report := ApplyRetention(context.Background(), plan)
	if !report.OK || report.FailedCount != 0 || report.DeletedCount != 4 || report.BoundedCount != 1 {
		t.Fatalf("apply report = %+v", report)
	}
	for _, path := range []string{oldRun, maintenanceRun, filepath.Join(stateDir, "keepalive-headless.log"), filepath.Join(stateDir, ".page-cleanup.json.tmp-stale")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("eligible path %s still exists: %v", path, err)
		}
	}
	for _, path := range []string{boundaryRun, freshRun, futureRun, filepath.Join(stateDir, "headless-health", "latest.json"), filepath.Join(stateDir, "connections.json"), filepath.Join(stateDir, "daemon.json"), socketPath, filepath.Join(stateDir, "browser", "headless-profile", "Default", "Cookies"), filepath.Join(stateDir, "headless", "managed-processes.json"), filepath.Join(stateDir, "locks", "active.lock"), filepath.Join(stateDir, "artifact-prune", "latest.json"), filepath.Join(stateDir, "unknown-old.txt"), outside, symlinkTarget} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("retained path %s is missing: %v", path, err)
		}
	}
	if info, err := os.Stat(filepath.Join(stateDir, "keepalive-headed.log")); err != nil || info.Size() != 64 {
		t.Fatalf("bounded active log info=%v err=%v, want 64 bytes", info, err)
	}

	secondPlan, err := PlanRetention(context.Background(), policy)
	if err != nil {
		t.Fatalf("second PlanRetention returned error: %v", err)
	}
	if secondPlan.EligibleCount != 0 {
		t.Fatalf("second plan eligible = %d, want idempotent zero; items=%+v", secondPlan.EligibleCount, secondPlan.Items)
	}
}

func TestRetentionApplyContinuesAfterPermissionFailure(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	stateDir := t.TempDir()
	first := filepath.Join(stateDir, "headless-health", now.Add(-200*time.Hour).Format(runTimestampLayout))
	second := filepath.Join(stateDir, "headless-health", now.Add(-201*time.Hour).Format(runTimestampLayout))
	mustWriteFixture(t, filepath.Join(first, "result.json"), "first", now.Add(-200*time.Hour))
	mustWriteFixture(t, filepath.Join(second, "result.json"), "second", now.Add(-201*time.Hour))
	policy := DefaultRetentionPolicy(stateDir)
	policy.Now = now
	plan, err := PlanRetention(context.Background(), policy)
	if err != nil {
		t.Fatalf("PlanRetention returned error: %v", err)
	}
	report := applyRetention(context.Background(), plan, retentionFileOps{
		removeAll: func(path string) error {
			if path == first {
				return os.ErrPermission
			}
			return os.RemoveAll(path)
		},
		boundLog: boundLog,
	})
	if report.OK || report.FailedCount != 1 || report.DeletedCount != 1 || len(report.Errors) != 1 || !errors.Is(report.Errors[0].Cause, os.ErrPermission) {
		t.Fatalf("partial report = %+v", report)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("failed candidate was removed: %v", err)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatalf("unrelated safe candidate was not removed: %v", err)
	}
}

func TestRetentionApplyRejectsCandidateChangedSincePlan(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	stateDir := t.TempDir()
	run := filepath.Join(stateDir, "headless-health", now.Add(-200*time.Hour).Format(runTimestampLayout))
	resultPath := filepath.Join(run, "result.json")
	mustWriteFixture(t, resultPath, "planned", now.Add(-200*time.Hour))
	policy := DefaultRetentionPolicy(stateDir)
	policy.Now = now
	plan, err := PlanRetention(context.Background(), policy)
	if err != nil {
		t.Fatalf("PlanRetention returned error: %v", err)
	}
	if err := os.WriteFile(resultPath, []byte("changed after plan"), 0o600); err != nil {
		t.Fatalf("change planned candidate: %v", err)
	}
	report := ApplyRetention(context.Background(), plan)
	if report.OK || report.FailedCount != 1 || report.DeletedCount != 0 || len(report.Errors) != 1 || !strings.Contains(report.Errors[0].Error, "changed since plan") {
		t.Fatalf("changed-state report = %+v, want one revalidation failure", report)
	}
	if _, err := os.Stat(run); err != nil {
		t.Fatalf("changed candidate was deleted: %v", err)
	}
}

func TestRetentionHandlesRegisteredRotatedLogGenerations(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	stateDir := t.TempDir()
	old := filepath.Join(stateDir, "headless-maintenance.log.20260701T000000Z")
	fresh := filepath.Join(stateDir, "keepalive-headed.log.20260718T110000Z")
	mustWriteFixture(t, old, strings.Repeat("o", 40), now.Add(-200*time.Hour))
	mustWriteFixture(t, fresh, strings.Repeat("f", 80), now.Add(-time.Hour))
	policy := DefaultRetentionPolicy(stateDir)
	policy.Now = now
	policy.MaxLogSizeBytes = 64
	plan, err := PlanRetention(context.Background(), policy)
	if err != nil {
		t.Fatalf("PlanRetention returned error: %v", err)
	}
	assertPlanItem(t, plan, old, ActionDelete, "older_than_retention")
	assertPlanItem(t, plan, fresh, ActionBoundLog, "log_exceeds_hard_bound")
	report := ApplyRetention(context.Background(), plan)
	if !report.OK || report.DeletedCount != 1 || report.BoundedCount != 1 || report.BytesReclaimed != 56 {
		t.Fatalf("rotated log report = %+v, want 40 deleted and 16 bounded bytes", report)
	}
}

func TestWriteBoundedManagedLogReplacesAndCapsEveryRun(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "headless-maintenance.log")
	mustWriteFixture(t, path, strings.Repeat("old", 100), time.Now().Add(-time.Hour))
	result, err := WriteBoundedManagedLog(context.Background(), root, path, 128, func(w io.Writer) error {
		_, writeErr := io.WriteString(w, strings.Repeat("new", 1000))
		return writeErr
	})
	if err != nil {
		t.Fatalf("WriteBoundedManagedLog returned error: %v", err)
	}
	if result.SizeBytes != 128 || result.DroppedBytes == 0 || result.PreRunReclaimedBytes == 0 {
		t.Fatalf("bounded log result = %+v", result)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 128 || info.Mode().Perm() != 0o600 {
		t.Fatalf("bounded log stat = %+v err=%v", info, err)
	}
}

func TestRetentionAndManagedLogsRejectSymlinkRootsAndTargets(t *testing.T) {
	realRoot := t.TempDir()
	linkedRoot := filepath.Join(t.TempDir(), "linked-state")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatalf("symlink state root: %v", err)
	}
	policy := DefaultRetentionPolicy(linkedRoot)
	if _, err := PlanRetention(context.Background(), policy); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("PlanRetention symlink root error = %v, want root rejection", err)
	}
	if _, err := WriteBoundedManagedLog(context.Background(), linkedRoot, filepath.Join(linkedRoot, "task.log"), 64, func(w io.Writer) error { return nil }); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("WriteBoundedManagedLog symlink root error = %v, want root rejection", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.log")
	mustWriteFixture(t, outside, "outside", time.Now())
	target := filepath.Join(realRoot, "task.log")
	if err := os.Symlink(outside, target); err != nil {
		t.Fatalf("symlink managed log target: %v", err)
	}
	if _, err := WriteBoundedManagedLog(context.Background(), realRoot, target, 64, func(w io.Writer) error { return nil }); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("WriteBoundedManagedLog symlink target error = %v, want target rejection", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "outside" {
		t.Fatalf("outside target = %q err=%v, want unchanged", string(got), err)
	}
}

func TestParseByteSize(t *testing.T) {
	tests := map[string]int64{"64MiB": 64 << 20, "1GiB": 1 << 30, "512KiB": 512 << 10, "42B": 42, "64mb": 64 << 20}
	for raw, want := range tests {
		got, err := ParseByteSize(raw)
		if err != nil || got != want {
			t.Fatalf("ParseByteSize(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "0", "-1MiB", "12watts"} {
		if _, err := ParseByteSize(raw); err == nil {
			t.Fatalf("ParseByteSize(%q) returned nil error", raw)
		}
	}
}

func mustWriteFixture(t *testing.T, path, content string, modified time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatalf("chtimes fixture: %v", err)
	}
}

func assertPlanItem(t *testing.T, plan RetentionReport, path, action, reason string) {
	t.Helper()
	for _, item := range plan.Items {
		if item.Path == path {
			if item.Action != action || item.Reason != reason {
				t.Fatalf("plan item %s = %+v, want action=%s reason=%s", path, item, action, reason)
			}
			return
		}
	}
	t.Fatalf("plan missing path %s; items=%+v", path, plan.Items)
}

func planPaths(plan RetentionReport) []string {
	paths := make([]string, 0, len(plan.Items))
	for _, item := range plan.Items {
		paths = append(paths, item.Path)
	}
	return paths
}
