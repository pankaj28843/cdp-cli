package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

type LockMetadata struct {
	Name             string `json:"name"`
	PID              int    `json:"pid"`
	StartedAt        string `json:"started_at"`
	Phase            string `json:"phase,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	ProcessStartTime string `json:"-"`
}

type lockFileMetadata struct {
	Name             string `json:"name"`
	PID              int    `json:"pid"`
	StartedAt        string `json:"started_at"`
	Phase            string `json:"phase,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	ProcessStartTime string `json:"process_start_time,omitempty"`
}

const lockProcessIdentityTimeout = time.Second

var lockProcessStartTime = processgroup.ProcessStartTime

type LockHandle struct {
	Path     string
	Metadata LockMetadata
}

type LockInfo struct {
	Path         string       `json:"path"`
	Exists       bool         `json:"exists"`
	Metadata     LockMetadata `json:"metadata,omitempty"`
	ModifiedAt   string       `json:"modified_at,omitempty"`
	Stale        bool         `json:"stale"`
	StaleReason  string       `json:"stale_reason,omitempty"`
	OwnerRunning *bool        `json:"owner_running,omitempty"`
}

type StaleLockCleanup struct {
	Checked []LockInfo `json:"checked"`
	Removed []LockInfo `json:"removed"`
}

func RemoveStaleLocks(ctx context.Context, stateDir string, staleAfter time.Duration, prefixes ...string) (StaleLockCleanup, error) {
	result := StaleLockCleanup{Checked: []LockInfo{}, Removed: []LockInfo{}}
	if strings.TrimSpace(stateDir) == "" {
		return result, fmt.Errorf("state directory is required for stale lock cleanup")
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "locks"))
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("read lock directory: %w", err)
	}
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") || !hasAnyPrefix(entry.Name(), prefixes) {
			continue
		}
		path := filepath.Join(stateDir, "locks", entry.Name())
		info, inspectErr := InspectLockContext(ctx, path, staleAfter)
		if inspectErr != nil {
			return result, inspectErr
		}
		result.Checked = append(result.Checked, info)
		if !info.Stale || (info.OwnerRunning != nil && *info.OwnerRunning && info.StaleReason != "lease_expired") {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return result, fmt.Errorf("remove stale lock %s: %w", entry.Name(), err)
		}
		result.Removed = append(result.Removed, info)
	}
	return result, nil
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func AcquireLock(ctx context.Context, stateDir, name string, timeout, staleAfter time.Duration, metadata LockMetadata) (LockHandle, bool, LockMetadata, error) {
	return AcquireLockWithLease(ctx, stateDir, name, timeout, staleAfter, 0, metadata)
}

// AcquireLockWithLease acquires a lock whose owner lease expires after lease.
// A zero lease preserves the legacy lock behavior. The lease is deliberately
// separate from staleAfter: staleAfter handles abandoned legacy locks, while
// the lease bounds a live repair attempt.
func AcquireLockWithLease(ctx context.Context, stateDir, name string, timeout, staleAfter, lease time.Duration, metadata LockMetadata) (LockHandle, bool, LockMetadata, error) {
	if strings.TrimSpace(stateDir) == "" {
		return LockHandle{}, false, LockMetadata{}, fmt.Errorf("state directory is required for lock")
	}
	if lease < 0 {
		return LockHandle{}, false, LockMetadata{}, fmt.Errorf("lock lease cannot be negative")
	}
	name = sanitizeLockName(name)
	path := filepath.Join(stateDir, "locks", name+".lock")
	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return LockHandle{}, false, LockMetadata{}, ctx.Err()
		default:
		}

		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return LockHandle{}, false, LockMetadata{}, fmt.Errorf("create lock directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if metadata.Name == "" {
				metadata.Name = name
			}
			if metadata.PID == 0 {
				metadata.PID = os.Getpid()
			}
			identityCtx, identityCancel := context.WithTimeout(ctx, lockProcessIdentityTimeout)
			if token, identityErr := lockProcessStartTime(identityCtx, metadata.PID); identityErr == nil {
				metadata.ProcessStartTime = token
			}
			identityCancel()
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return LockHandle{}, false, LockMetadata{}, err
			}
			now := time.Now().UTC()
			if metadata.StartedAt == "" {
				metadata.StartedAt = now.Format(time.RFC3339Nano)
			}
			if lease > 0 {
				metadata.ExpiresAt = now.Add(lease).Format(time.RFC3339Nano)
			}
			if err := json.NewEncoder(file).Encode(lockFileMetadataFromLockMetadata(metadata)); err != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return LockHandle{}, false, LockMetadata{}, fmt.Errorf("write lock metadata: %w", err)
			}
			if err := file.Close(); err != nil {
				_ = os.Remove(path)
				return LockHandle{}, false, LockMetadata{}, fmt.Errorf("close lock metadata: %w", err)
			}
			return LockHandle{Path: path, Metadata: metadata}, true, LockMetadata{}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return LockHandle{}, false, LockMetadata{}, fmt.Errorf("create lock file: %w", err)
		}

		existingInfo, inspectErr := InspectLockContext(ctx, path, staleAfter)
		if inspectErr != nil {
			return LockHandle{}, false, LockMetadata{}, inspectErr
		}
		existing := existingInfo.Metadata
		if existingInfo.Stale && !(existingInfo.OwnerRunning != nil && *existingInfo.OwnerRunning && existingInfo.StaleReason != "lease_expired") {
			if removeErr := os.Remove(path); removeErr == nil || os.IsNotExist(removeErr) {
				continue
			}
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return LockHandle{Path: path}, false, existing, nil
		}

		sleep := 100 * time.Millisecond
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		if sleep <= 0 {
			return LockHandle{Path: path}, false, existing, nil
		}
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return LockHandle{}, false, LockMetadata{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (h LockHandle) Update(ctx context.Context, phase string) error {
	if strings.TrimSpace(h.Path) == "" {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if !h.ownsCurrentFile() {
		return fmt.Errorf("lock is no longer owned")
	}
	h.Metadata.Phase = phase
	b, err := json.MarshalIndent(lockFileMetadataFromLockMetadata(h.Metadata), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lock metadata: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(h.Path, b, 0o600); err != nil {
		return fmt.Errorf("write lock metadata: %w", err)
	}
	return nil
}

func (h LockHandle) Release() error {
	if strings.TrimSpace(h.Path) == "" {
		return nil
	}
	if !h.ownsCurrentFile() {
		return nil
	}
	if err := os.Remove(h.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("release lock: %w", err)
	}
	return nil
}

func (h LockHandle) ownsCurrentFile() bool {
	b, err := os.ReadFile(h.Path)
	if err != nil {
		return false
	}
	var currentFile lockFileMetadata
	if err := json.Unmarshal(b, &currentFile); err != nil {
		return false
	}
	current := lockMetadataFromLockFile(currentFile)
	if current.Name != h.Metadata.Name || current.PID != h.Metadata.PID || current.StartedAt != h.Metadata.StartedAt {
		return false
	}
	if current.ProcessStartTime != h.Metadata.ProcessStartTime {
		return false
	}
	if current.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339Nano, current.ExpiresAt)
		if err != nil || !time.Now().UTC().Before(expires) {
			return false
		}
	}
	return true
}

func InspectLock(path string, staleAfter time.Duration) LockInfo {
	info, _ := InspectLockContext(context.Background(), path, staleAfter)
	return info
}

// InspectLockContext inspects a lock without making a recovery decision after
// its caller has been canceled. InspectLock remains the compatibility wrapper
// for callers that do not have an operation context.
func InspectLockContext(ctx context.Context, path string, staleAfter time.Duration) (LockInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return LockInfo{Path: path}, err
	}
	stat, statErr := os.Stat(path)
	info := LockInfo{Path: path, Exists: statErr == nil}
	if err := ctx.Err(); err != nil {
		return info, err
	}
	var metadata LockMetadata
	if b, err := os.ReadFile(path); err == nil {
		var file lockFileMetadata
		if err := json.Unmarshal(b, &file); err == nil {
			metadata = lockMetadataFromLockFile(file)
		}
	}
	if err := ctx.Err(); err != nil {
		return info, err
	}
	info.Metadata = metadata
	if statErr == nil {
		info.ModifiedAt = stat.ModTime().UTC().Format(time.RFC3339)
	}
	identityUnavailable := false
	if metadata.PID > 0 {
		running, identityState, inspectErr := lockOwnerStatus(ctx, metadata)
		if inspectErr != nil {
			return info, inspectErr
		}
		info.OwnerRunning = &running
		if identityState == "mismatch" {
			info.Stale = true
			info.StaleReason = "owner_process_identity_mismatch"
			return info, nil
		}
		identityUnavailable = identityState == "unavailable"
		if !running {
			info.Stale = true
			info.StaleReason = "owner_process_not_running"
			return info, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return info, err
	}
	if metadata.ExpiresAt != "" {
		if expires, err := time.Parse(time.RFC3339Nano, metadata.ExpiresAt); err == nil && !time.Now().UTC().Before(expires) {
			info.Stale = true
			info.StaleReason = "lease_expired"
			return info, nil
		}
	}
	if identityUnavailable {
		info.Stale = true
		info.StaleReason = "owner_process_identity_unavailable"
		return info, nil
	}
	if staleAfter <= 0 {
		return info, nil
	}
	var started time.Time
	if metadata.StartedAt != "" {
		started, _ = time.Parse(time.RFC3339, metadata.StartedAt)
	}
	if started.IsZero() && statErr == nil {
		started = stat.ModTime()
	}
	if !started.IsZero() && time.Since(started) > staleAfter {
		info.Stale = true
		info.StaleReason = "age_exceeded"
	}
	if err := ctx.Err(); err != nil {
		return info, err
	}
	return info, nil
}

func lockOwnerStatus(ctx context.Context, metadata LockMetadata) (bool, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, "", err
	}
	if metadata.PID <= 0 {
		return false, "", nil
	}
	process, err := os.FindProcess(metadata.PID)
	if err != nil {
		return false, "", nil
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, "", ctxErr
		}
		return errors.Is(err, syscall.EPERM), "", nil
	}
	if err := ctx.Err(); err != nil {
		return true, "", err
	}
	if !processgroup.IsStrongProcessStartIdentity(metadata.ProcessStartTime) {
		return true, "", nil
	}
	identityCtx, cancel := context.WithTimeout(ctx, lockProcessIdentityTimeout)
	actual, identityErr := lockProcessStartTime(identityCtx, metadata.PID)
	cancel()
	if err := ctx.Err(); err != nil {
		return true, "", err
	}
	if identityErr != nil {
		return true, "unavailable", nil
	}
	if actual != strings.TrimSpace(metadata.ProcessStartTime) {
		return false, "mismatch", nil
	}
	return true, "", nil
}

func lockFileMetadataFromLockMetadata(metadata LockMetadata) lockFileMetadata {
	return lockFileMetadata{
		Name:             metadata.Name,
		PID:              metadata.PID,
		StartedAt:        metadata.StartedAt,
		Phase:            metadata.Phase,
		ExpiresAt:        metadata.ExpiresAt,
		ProcessStartTime: strings.TrimSpace(metadata.ProcessStartTime),
	}
}

func lockMetadataFromLockFile(file lockFileMetadata) LockMetadata {
	return LockMetadata{
		Name:             file.Name,
		PID:              file.PID,
		StartedAt:        file.StartedAt,
		Phase:            file.Phase,
		ExpiresAt:        file.ExpiresAt,
		ProcessStartTime: strings.TrimSpace(file.ProcessStartTime),
	}
}

func sanitizeLockName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "default"
	}
	return out
}
