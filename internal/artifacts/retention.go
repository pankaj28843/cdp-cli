package artifacts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	RetentionSchemaVersion = "cdp-artifact-retention/v1"
	DefaultRetention       = 168 * time.Hour
	DefaultMaxLogSizeBytes = int64(64 << 20)
	runTimestampLayout     = "20060102T150405Z"

	ActionDelete   = "delete"
	ActionBoundLog = "bound_log"
	ActionRetain   = "retain"
	ActionSkip     = "skip"
)

var byteSizePattern = regexp.MustCompile(`^([0-9]+)\s*([kmgt]?i?b)?$`)

type RetentionPolicy struct {
	StateDir        string
	OlderThan       time.Duration
	MaxLogSizeBytes int64
	Now             time.Time
}

type PolicySummary struct {
	StateDir          string `json:"state_dir"`
	Retention         string `json:"retention"`
	RetentionSeconds  int64  `json:"retention_seconds"`
	MaxLogSize        string `json:"max_log_size"`
	MaxLogSizeBytes   int64  `json:"max_log_size_bytes"`
	LogStrategy       string `json:"log_strategy"`
	AllowlistEnforced bool   `json:"allowlist_enforced"`
}

type RetentionItem struct {
	Class             string `json:"artifact_class"`
	Path              string `json:"path"`
	Action            string `json:"action"`
	Reason            string `json:"reason"`
	AgeSource         string `json:"age_source,omitempty"`
	ObservedAt        string `json:"observed_at,omitempty"`
	ObservedModTime   string `json:"observed_mod_time,omitempty"`
	ObservedSizeBytes int64  `json:"size_bytes"`
	EligibleBytes     int64  `json:"eligible_bytes,omitempty"`
	Eligible          bool   `json:"eligible"`
	Deleted           bool   `json:"deleted,omitempty"`
	Bounded           bool   `json:"bounded,omitempty"`
	BytesReclaimed    int64  `json:"bytes_reclaimed,omitempty"`
	Error             string `json:"error,omitempty"`
	device            uint64
}

type RetentionError struct {
	Class  string `json:"artifact_class,omitempty"`
	Path   string `json:"path"`
	Action string `json:"action,omitempty"`
	Error  string `json:"error"`
	Cause  error  `json:"-"`
}

type RetentionReport struct {
	OK             bool             `json:"ok"`
	SchemaVersion  string           `json:"schema_version"`
	State          string           `json:"state"`
	Status         string           `json:"status"`
	Action         string           `json:"action"`
	DryRun         bool             `json:"dry_run"`
	Applied        bool             `json:"applied"`
	Policy         PolicySummary    `json:"policy"`
	CutoffTime     string           `json:"cutoff_time"`
	StartedAt      string           `json:"started_at"`
	FinishedAt     string           `json:"finished_at,omitempty"`
	ScannedCount   int              `json:"scanned_count"`
	EligibleCount  int              `json:"eligible_count"`
	DeletedCount   int              `json:"deleted_count"`
	BoundedCount   int              `json:"bounded_count"`
	SkippedCount   int              `json:"skipped_count"`
	FailedCount    int              `json:"failed_count"`
	BytesEligible  int64            `json:"bytes_eligible"`
	BytesReclaimed int64            `json:"bytes_reclaimed"`
	Items          []RetentionItem  `json:"items"`
	Errors         []RetentionError `json:"errors"`
	NextCommands   []string         `json:"next_commands"`
}

type ManagedLogResult struct {
	Path                 string `json:"path"`
	MaxSizeBytes         int64  `json:"max_size_bytes"`
	SizeBytes            int64  `json:"size_bytes"`
	InputBytes           int64  `json:"input_bytes"`
	DroppedBytes         int64  `json:"dropped_bytes"`
	PreRunReclaimedBytes int64  `json:"pre_run_reclaimed_bytes"`
}

func DefaultRetentionPolicy(stateDir string) RetentionPolicy {
	return RetentionPolicy{
		StateDir:        stateDir,
		OlderThan:       DefaultRetention,
		MaxLogSizeBytes: DefaultMaxLogSizeBytes,
		Now:             time.Now().UTC(),
	}
}

func (p RetentionPolicy) Summary() PolicySummary {
	return PolicySummary{
		StateDir:          filepath.Clean(p.StateDir),
		Retention:         p.OlderThan.String(),
		RetentionSeconds:  int64(p.OlderThan.Seconds()),
		MaxLogSize:        FormatByteSize(p.MaxLogSizeBytes),
		MaxLogSizeBytes:   p.MaxLogSizeBytes,
		LogStrategy:       "latest_run_replacement",
		AllowlistEnforced: true,
	}
}

func PlanRetention(ctx context.Context, policy RetentionPolicy) (RetentionReport, error) {
	if err := validateRetentionPolicy(policy); err != nil {
		return RetentionReport{}, err
	}
	now := policy.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	root, err := filepath.Abs(filepath.Clean(policy.StateDir))
	if err != nil {
		return RetentionReport{}, fmt.Errorf("resolve state root: %w", err)
	}
	policy.StateDir = root
	report := RetentionReport{
		OK:            true,
		SchemaVersion: RetentionSchemaVersion,
		State:         "planned",
		Status:        "pass",
		Action:        "would_prune",
		DryRun:        true,
		Applied:       false,
		Policy:        policy.Summary(),
		CutoffTime:    now.Add(-policy.OlderThan).Format(time.RFC3339),
		StartedAt:     now.Format(time.RFC3339),
		FinishedAt:    now.Format(time.RFC3339),
		Items:         []RetentionItem{},
		Errors:        []RetentionError{},
		NextCommands:  []string{"cdp artifacts prune --older-than 168h --apply --json", "cdp cron status --json"},
	}

	rootInfo, err := os.Lstat(root)
	if os.IsNotExist(err) {
		report.Action = "none"
		return report, nil
	}
	if err != nil {
		return RetentionReport{}, fmt.Errorf("inspect state root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return RetentionReport{}, fmt.Errorf("state root must be a real directory, not a symlink: %s", root)
	}
	rootDevice := fileDevice(rootInfo)
	cutoff := now.Add(-policy.OlderThan)

	if err := checkContext(ctx); err != nil {
		return RetentionReport{}, err
	}
	scanRunContainer(ctx, &report, root, filepath.Join(root, "headless-health"), "headless_health_run", cutoff, now, rootDevice)
	scanMaintenanceContainer(ctx, &report, root, filepath.Join(root, "headless-maintenance"), cutoff, now, rootDevice)
	scanTopLevel(ctx, &report, root, cutoff, now, rootDevice, policy.MaxLogSizeBytes)
	sort.SliceStable(report.Items, func(i, j int) bool { return report.Items[i].Path < report.Items[j].Path })
	recountPlan(&report)
	return report, nil
}

func ApplyRetention(ctx context.Context, plan RetentionReport) RetentionReport {
	return applyRetention(ctx, plan, retentionFileOps{removeAll: os.RemoveAll, boundLog: boundLog})
}

type retentionFileOps struct {
	removeAll func(string) error
	boundLog  func(string, int64) (int64, error)
}

func applyRetention(ctx context.Context, plan RetentionReport, ops retentionFileOps) RetentionReport {
	report := plan
	report.DryRun = false
	report.Applied = true
	report.State = "applied"
	report.Status = "pass"
	report.Action = "pruned"
	report.DeletedCount = 0
	report.BoundedCount = 0
	report.BytesReclaimed = 0
	report.FinishedAt = ""
	initialFailures := report.FailedCount
	root := report.Policy.StateDir

	for i := range report.Items {
		item := &report.Items[i]
		if !item.Eligible {
			continue
		}
		if err := checkContext(ctx); err != nil {
			markApplyFailure(&report, item, err)
			break
		}
		if err := revalidateRetentionItem(root, *item); err != nil {
			markApplyFailure(&report, item, err)
			continue
		}
		switch item.Action {
		case ActionDelete:
			if err := ops.removeAll(item.Path); err != nil {
				markApplyFailure(&report, item, err)
				continue
			}
			item.Deleted = true
			item.BytesReclaimed = item.EligibleBytes
			report.DeletedCount++
			report.BytesReclaimed += item.BytesReclaimed
		case ActionBoundLog:
			reclaimed, err := ops.boundLog(item.Path, report.Policy.MaxLogSizeBytes)
			if err != nil {
				markApplyFailure(&report, item, err)
				continue
			}
			item.Bounded = true
			item.BytesReclaimed = reclaimed
			report.BoundedCount++
			report.BytesReclaimed += reclaimed
		}
	}
	report.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if report.FailedCount > initialFailures || initialFailures > 0 {
		report.OK = false
		report.State = "partial"
		report.Status = "fail"
		report.Action = "partially_pruned"
		report.NextCommands = []string{"cdp artifacts prune --older-than " + report.Policy.Retention + " --apply --json", "cdp cron status --json"}
	} else if report.DeletedCount == 0 && report.BoundedCount == 0 {
		report.Action = "unchanged"
	}
	return report
}

func scanTopLevel(ctx context.Context, report *RetentionReport, root string, cutoff, now time.Time, rootDevice uint64, maxLogSize int64) {
	entries, err := os.ReadDir(root)
	if err != nil {
		addScanError(report, "state_root", root, err)
		return
	}
	protected := map[string]bool{
		"artifact-prune": true, "browser": true, "connections.json": true, "daemon.json": true,
		"daemon.sock": true, "headless": true, "locks": true, "page-cleanup.json": true,
		"profile-seed": true, "headed-heal": true,
	}
	activeLogs := map[string]bool{"keepalive-headed.log": true, "headless-maintenance.log": true}
	legacyLogs := map[string]bool{
		"keepalive-headless.log": true, "headless-health.log": true,
		"profile-seed-headless.log": true, "page-cleanup-headless.log": true,
	}
	for _, entry := range entries {
		if checkContext(ctx) != nil {
			return
		}
		name := entry.Name()
		path := filepath.Join(root, name)
		if name == "headless-health" || name == "headless-maintenance" {
			continue
		}
		if protected[name] {
			addSimpleItem(report, path, "protected_state", ActionRetain, "protected_state")
			continue
		}
		if activeLogs[name] {
			scanManagedLog(report, root, path, "managed_task_log", true, cutoff, now, rootDevice, maxLogSize)
			continue
		}
		if legacyLogs[name] {
			scanManagedLog(report, root, path, "legacy_managed_log", false, cutoff, now, rootDevice, maxLogSize)
			continue
		}
		if isRotatedManagedLog(name) {
			scanManagedLog(report, root, path, "rotated_managed_log", false, cutoff, now, rootDevice, maxLogSize)
			continue
		}
		if isAtomicTempName(name) {
			scanAgeBasedPath(report, root, path, "atomic_temp_file", cutoff, now, rootDevice, false)
			continue
		}
		addSimpleItem(report, path, "unknown", ActionSkip, "unknown_path")
	}
	latest := filepath.Join(root, "artifact-prune", "latest.json")
	if _, err := os.Lstat(latest); err == nil {
		addSimpleItem(report, latest, "retention_summary", ActionRetain, "protected_latest_summary")
	}
}

func scanMaintenanceContainer(ctx context.Context, report *RetentionReport, root, container string, cutoff, now time.Time, rootDevice uint64) {
	info, err := os.Lstat(container)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		addScanError(report, "maintenance_artifacts", container, err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		addSimpleItem(report, container, "maintenance_artifacts", ActionSkip, "symlink")
		return
	}
	entries, err := os.ReadDir(container)
	if err != nil {
		addScanError(report, "maintenance_artifacts", container, err)
		return
	}
	for _, entry := range entries {
		if checkContext(ctx) != nil {
			return
		}
		path := filepath.Join(container, entry.Name())
		switch entry.Name() {
		case "latest.json":
			addSimpleItem(report, path, "maintenance_summary", ActionRetain, "protected_latest_summary")
		case "health":
			scanRunContainer(ctx, report, root, path, "maintenance_health_run", cutoff, now, rootDevice)
		default:
			if isAtomicTempName(entry.Name()) {
				scanAgeBasedPath(report, root, path, "atomic_temp_file", cutoff, now, rootDevice, false)
			} else {
				addSimpleItem(report, path, "unknown", ActionSkip, "unknown_path")
			}
		}
	}
}

func scanRunContainer(ctx context.Context, report *RetentionReport, root, container, class string, cutoff, now time.Time, rootDevice uint64) {
	info, err := os.Lstat(container)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		addScanError(report, class, container, err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		addSimpleItem(report, container, class, ActionSkip, "symlink")
		return
	}
	if fileDevice(info) != rootDevice {
		addSimpleItem(report, container, class, ActionSkip, "cross_filesystem")
		return
	}
	entries, err := os.ReadDir(container)
	if err != nil {
		addScanError(report, class, container, err)
		return
	}
	for _, entry := range entries {
		if checkContext(ctx) != nil {
			return
		}
		path := filepath.Join(container, entry.Name())
		if entry.Name() == "latest.json" {
			addSimpleItem(report, path, class+"_summary", ActionRetain, "protected_latest_summary")
			continue
		}
		if entry.Name() == "failure-count" || entry.Name() == "feature-request-candidate.md" {
			addSimpleItem(report, path, class+"_state", ActionRetain, "protected_current_state")
			continue
		}
		if isAtomicTempName(entry.Name()) {
			scanAgeBasedPath(report, root, path, "atomic_temp_file", cutoff, now, rootDevice, false)
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			addScanError(report, class, path, err)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			addSimpleItem(report, path, class, ActionSkip, "symlink")
			continue
		}
		runTime, err := time.Parse(runTimestampLayout, entry.Name())
		if err != nil || !info.IsDir() {
			addSimpleItem(report, path, class, ActionSkip, "malformed_timestamp")
			continue
		}
		size, reason, err := treeSizeAndSafety(root, path, rootDevice)
		if err != nil {
			if reason == "" {
				reason = "scan_failed"
			}
			item := simpleItem(path, class, ActionSkip, reason)
			item.Error = err.Error()
			report.Items = append(report.Items, item)
			if reason == "scan_failed" {
				addReportError(report, item, err)
			}
			continue
		}
		item := itemForTime(path, class, runTime.UTC(), info.ModTime(), size, cutoff, now, "timestamp_name")
		item.device = fileDevice(info)
		report.Items = append(report.Items, item)
	}
}

func scanManagedLog(report *RetentionReport, root, path, class string, active bool, cutoff, now time.Time, rootDevice uint64, maxLogSize int64) {
	info, err := os.Lstat(path)
	if err != nil {
		addScanError(report, class, path, err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		addSimpleItem(report, path, class, ActionSkip, "symlink")
		return
	}
	if !info.Mode().IsRegular() {
		addSimpleItem(report, path, class, ActionSkip, "not_regular_file")
		return
	}
	if !pathWithinRoot(root, path) || fileDevice(info) != rootDevice {
		addSimpleItem(report, path, class, ActionSkip, "cross_filesystem")
		return
	}
	item := simpleItem(path, class, ActionRetain, "active_log_within_bound")
	item.ObservedAt = info.ModTime().UTC().Format(time.RFC3339)
	item.ObservedModTime = info.ModTime().UTC().Format(time.RFC3339Nano)
	item.ObservedSizeBytes = info.Size()
	item.device = fileDevice(info)
	if !active && info.ModTime().Before(cutoff) {
		item.Action = ActionDelete
		item.Reason = "older_than_retention"
		item.AgeSource = "modification_time"
		item.Eligible = true
		item.EligibleBytes = info.Size()
	} else if info.Size() > maxLogSize {
		item.Action = ActionBoundLog
		item.Reason = "log_exceeds_hard_bound"
		item.Eligible = true
		item.EligibleBytes = info.Size() - maxLogSize
	} else if !active {
		item.Reason = retentionReason(info.ModTime(), cutoff, now)
		item.AgeSource = "modification_time"
	}
	report.Items = append(report.Items, item)
}

func scanAgeBasedPath(report *RetentionReport, root, path, class string, cutoff, now time.Time, rootDevice uint64, allowDirectory bool) {
	info, err := os.Lstat(path)
	if err != nil {
		addScanError(report, class, path, err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		addSimpleItem(report, path, class, ActionSkip, "symlink")
		return
	}
	if info.IsDir() && !allowDirectory {
		addSimpleItem(report, path, class, ActionSkip, "not_regular_file")
		return
	}
	if !pathWithinRoot(root, path) || fileDevice(info) != rootDevice {
		addSimpleItem(report, path, class, ActionSkip, "cross_filesystem")
		return
	}
	size := info.Size()
	if info.IsDir() {
		var reason string
		size, reason, err = treeSizeAndSafety(root, path, rootDevice)
		if err != nil {
			addSimpleItem(report, path, class, ActionSkip, reason)
			return
		}
	}
	item := itemForTime(path, class, info.ModTime().UTC(), info.ModTime(), size, cutoff, now, "modification_time")
	item.device = fileDevice(info)
	report.Items = append(report.Items, item)
}

func itemForTime(path, class string, observed time.Time, modTime time.Time, size int64, cutoff, now time.Time, source string) RetentionItem {
	action := ActionRetain
	reason := retentionReason(observed, cutoff, now)
	eligible := false
	eligibleBytes := int64(0)
	if observed.Before(cutoff) {
		action = ActionDelete
		reason = "older_than_retention"
		eligible = true
		eligibleBytes = size
	}
	return RetentionItem{
		Class: class, Path: path, Action: action, Reason: reason, AgeSource: source,
		ObservedAt: observed.UTC().Format(time.RFC3339), ObservedModTime: modTime.UTC().Format(time.RFC3339Nano),
		ObservedSizeBytes: size, EligibleBytes: eligibleBytes, Eligible: eligible,
	}
}

func retentionReason(observed, cutoff, now time.Time) string {
	switch {
	case observed.After(now):
		return "future_timestamp"
	case observed.Equal(cutoff):
		return "at_retention_boundary"
	default:
		return "within_retention"
	}
}

func revalidateRetentionItem(root string, item RetentionItem) error {
	if !pathWithinRoot(root, item.Path) {
		return fmt.Errorf("candidate escaped state root")
	}
	info, err := os.Lstat(item.Path)
	if err != nil {
		return fmt.Errorf("candidate changed since plan: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("candidate changed to symlink since plan")
	}
	if item.device != 0 && fileDevice(info) != item.device {
		return fmt.Errorf("candidate crossed filesystem since plan")
	}
	if item.ObservedModTime != "" && info.ModTime().UTC().Format(time.RFC3339Nano) != item.ObservedModTime {
		return fmt.Errorf("candidate modification time changed since plan")
	}
	if info.IsDir() {
		size, reason, err := treeSizeAndSafety(root, item.Path, fileDevice(info))
		if err != nil {
			return fmt.Errorf("candidate is unsafe (%s): %w", reason, err)
		}
		if size != item.ObservedSizeBytes {
			return fmt.Errorf("candidate size changed since plan")
		}
	} else if info.Size() != item.ObservedSizeBytes {
		return fmt.Errorf("candidate size changed since plan")
	}
	return nil
}

func treeSizeAndSafety(root, path string, rootDevice uint64) (int64, string, error) {
	if !pathWithinRoot(root, path) {
		return 0, "outside_state_root", fmt.Errorf("path %s is outside %s", path, root)
	}
	var size int64
	err := filepath.WalkDir(path, func(candidate string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(candidate)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errUnsafeSymlink
		}
		if fileDevice(info) != rootDevice {
			return errCrossFilesystem
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	switch {
	case errors.Is(err, errUnsafeSymlink):
		return 0, "contains_symlink", err
	case errors.Is(err, errCrossFilesystem):
		return 0, "cross_filesystem", err
	case err != nil:
		return 0, "scan_failed", err
	default:
		return size, "", nil
	}
}

var (
	errUnsafeSymlink   = errors.New("symlink in candidate tree")
	errCrossFilesystem = errors.New("candidate crosses filesystem boundary")
)

func boundLog(path string, maxSize int64) (int64, error) {
	if maxSize <= 0 {
		return 0, fmt.Errorf("max log size must be positive")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return 0, fmt.Errorf("managed log must be a regular non-symlink file")
	}
	if info.Size() <= maxSize {
		return 0, nil
	}
	source, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer source.Close()
	if _, err := source.Seek(info.Size()-maxSize, io.SeekStart); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if _, err := io.CopyN(tmp, source, maxSize); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return 0, err
	}
	cleanup = false
	return info.Size() - maxSize, nil
}

func WriteBoundedManagedLog(ctx context.Context, root, path string, maxSize int64, produce func(io.Writer) error) (ManagedLogResult, error) {
	result := ManagedLogResult{Path: filepath.Clean(path), MaxSizeBytes: maxSize}
	if maxSize <= 0 {
		return result, fmt.Errorf("max log size must be positive")
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return result, fmt.Errorf("resolve state root: %w", err)
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return result, fmt.Errorf("resolve managed log path: %w", err)
	}
	result.Path = absPath
	if !pathWithinRoot(absRoot, absPath) {
		return result, fmt.Errorf("managed log path %s is outside state root %s", absPath, absRoot)
	}
	if err := secureMkdirAll(absRoot, filepath.Dir(absPath)); err != nil {
		return result, err
	}
	if info, err := os.Lstat(absPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return result, fmt.Errorf("managed log must be a regular non-symlink file")
		}
		result.PreRunReclaimedBytes, err = boundLog(absPath, maxSize)
		if err != nil {
			return result, fmt.Errorf("pre-bound managed log: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return result, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(absPath), "."+filepath.Base(absPath)+".tmp-*")
	if err != nil {
		return result, err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return result, err
	}
	writer := &cappedWriter{writer: tmp, remaining: maxSize}
	produceErr := produce(writer)
	result.InputBytes = writer.inputBytes
	result.DroppedBytes = writer.droppedBytes
	if err := checkContext(ctx); produceErr == nil && err != nil {
		produceErr = err
	}
	if err := tmp.Sync(); err != nil && produceErr == nil {
		produceErr = err
	}
	if err := tmp.Close(); err != nil && produceErr == nil {
		produceErr = err
	}
	info, statErr := os.Stat(tmpPath)
	if statErr != nil {
		return result, statErr
	}
	result.SizeBytes = info.Size()
	if err := os.Rename(tmpPath, absPath); err != nil {
		return result, err
	}
	cleanup = false
	return result, produceErr
}

type cappedWriter struct {
	writer       io.Writer
	remaining    int64
	inputBytes   int64
	droppedBytes int64
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.inputBytes += int64(len(p))
	allowed := len(p)
	if int64(allowed) > w.remaining {
		allowed = int(w.remaining)
	}
	if allowed > 0 {
		written, err := w.writer.Write(p[:allowed])
		w.remaining -= int64(written)
		if err != nil {
			return written, err
		}
		if written != allowed {
			return written, io.ErrShortWrite
		}
	}
	w.droppedBytes += int64(len(p) - allowed)
	return len(p), nil
}

func ParseByteSize(raw string) (int64, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	matches := byteSizePattern.FindStringSubmatch(normalized)
	if len(matches) != 3 {
		return 0, fmt.Errorf("invalid byte size %q", raw)
	}
	value, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("byte size must be positive: %q", raw)
	}
	multipliers := map[string]int64{"": 1, "b": 1, "kb": 1 << 10, "kib": 1 << 10, "mb": 1 << 20, "mib": 1 << 20, "gb": 1 << 30, "gib": 1 << 30, "tb": 1 << 40, "tib": 1 << 40}
	multiplier, ok := multipliers[matches[2]]
	if !ok || value > int64(^uint64(0)>>1)/multiplier {
		return 0, fmt.Errorf("invalid or overflowing byte size %q", raw)
	}
	return value * multiplier, nil
}

func FormatByteSize(value int64) string {
	for _, unit := range []struct {
		name string
		size int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if value >= unit.size && value%unit.size == 0 {
			return fmt.Sprintf("%d%s", value/unit.size, unit.name)
		}
	}
	return fmt.Sprintf("%dB", value)
}

func secureMkdirAll(root, dir string) error {
	if !pathWithinRoot(root, dir) && filepath.Clean(root) != filepath.Clean(dir) {
		return fmt.Errorf("directory %s is outside state root %s", dir, root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("managed log root is not a real directory: %s", root)
	}
	rootDevice := fileDevice(rootInfo)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o700); err != nil && !os.IsExist(err) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("managed log parent is not a real directory: %s", current)
		}
		if fileDevice(info) != rootDevice {
			return fmt.Errorf("managed log parent crosses the state filesystem: %s", current)
		}
	}
	return nil
}

func validateRetentionPolicy(policy RetentionPolicy) error {
	if strings.TrimSpace(policy.StateDir) == "" {
		return fmt.Errorf("state directory is required")
	}
	if policy.OlderThan <= 0 {
		return fmt.Errorf("retention must be positive")
	}
	if policy.MaxLogSizeBytes <= 0 {
		return fmt.Errorf("max log size must be positive")
	}
	return nil
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func fileDevice(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Dev)
	}
	return 0
}

func isAtomicTempName(name string) bool {
	return strings.HasPrefix(name, ".") && strings.Contains(name, ".tmp-")
}

func isRotatedManagedLog(name string) bool {
	for _, base := range []string{"keepalive-headed.log", "headless-maintenance.log", "keepalive-headless.log", "headless-health.log", "profile-seed-headless.log", "page-cleanup-headless.log"} {
		if strings.HasPrefix(name, base+".") {
			return true
		}
	}
	return false
}

func simpleItem(path, class, action, reason string) RetentionItem {
	return RetentionItem{Class: class, Path: path, Action: action, Reason: reason, Eligible: false}
}

func addSimpleItem(report *RetentionReport, path, class, action, reason string) {
	report.Items = append(report.Items, simpleItem(path, class, action, reason))
}

func addScanError(report *RetentionReport, class, path string, err error) {
	item := simpleItem(path, class, ActionSkip, "scan_failed")
	item.Error = err.Error()
	report.Items = append(report.Items, item)
	addReportError(report, item, err)
}

func addReportError(report *RetentionReport, item RetentionItem, err error) {
	report.FailedCount++
	report.OK = false
	report.Status = "fail"
	report.Errors = append(report.Errors, RetentionError{Class: item.Class, Path: item.Path, Action: item.Action, Error: err.Error(), Cause: err})
}

func markApplyFailure(report *RetentionReport, item *RetentionItem, err error) {
	item.Error = err.Error()
	addReportError(report, *item, err)
}

func recountPlan(report *RetentionReport) {
	report.ScannedCount = len(report.Items)
	report.EligibleCount = 0
	report.SkippedCount = 0
	report.BytesEligible = 0
	for _, item := range report.Items {
		if item.Eligible {
			report.EligibleCount++
			report.BytesEligible += item.EligibleBytes
		}
		if item.Action == ActionSkip {
			report.SkippedCount++
		}
	}
	if report.EligibleCount == 0 {
		report.Action = "none"
	}
}

func checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
