// Package availability contains the host-level safety gate used by
// launch-capable Auto Heal work.
package availability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

const (
	// DefaultConnectivityURL is deliberately a small endpoint. Its response
	// body is never retained or emitted in a result.
	DefaultConnectivityURL = "https://connectivitycheck.gstatic.com/generate_204"
	ConnectivityURLEnv     = "CDP_AUTO_HEAL_CONNECTIVITY_URL"

	DefaultSleepGapThreshold = 2 * time.Minute
	DefaultCooldown          = 2 * time.Minute
	DefaultProbeTimeout      = 2 * time.Second

	stateDirectoryName = "auto-heal"
	stateFileName      = "environment.json"
	lockFileName       = "environment.lock"
	repairLockFileName = "repair.lock"
	maxStateBytes      = 16 * 1024
)

// ProbeResult is the reduced outcome of one connectivity check. Callers must
// not put transport errors or endpoint URLs into user-facing output.
type ProbeResult struct {
	Online bool
	Reason string
}

// Result is the privacy-safe Auto Heal environment decision. A false Allowed
// result is a successful safety decision: callers should skip launch-capable
// repair and try again on a later scheduled tick.
type Result struct {
	Allowed           bool   `json:"allowed"`
	State             string `json:"state"`
	Network           string `json:"network"`
	SleepGapDetected  bool   `json:"sleep_gap_detected"`
	SleepGapSeconds   int64  `json:"sleep_gap_seconds,omitempty"`
	LastObservedAt    string `json:"last_observed_at,omitempty"`
	SuppressUntil     string `json:"suppress_until,omitempty"`
	RetryAfterSeconds int64  `json:"retry_after_seconds,omitempty"`
	Reason            string `json:"reason"`
	CheckedAt         string `json:"checked_at"`
}

// Options makes the safety decision deterministic in tests while leaving the
// production defaults small and cross-platform.
type Options struct {
	StateDir          string
	Now               func() time.Time
	InternetProbe     func(context.Context) ProbeResult
	ConnectivityURL   string
	SleepGapThreshold time.Duration
	Cooldown          time.Duration
	ProbeTimeout      time.Duration
}

type persistedState struct {
	LastObservedAt time.Time `json:"last_observed_at,omitempty"`
	SuppressUntil  time.Time `json:"suppress_until,omitempty"`
}

// Check records one host observation and returns whether launch-capable Auto
// Heal may proceed. It fails closed when state cannot be read or written.
func Check(ctx context.Context, opts Options) (Result, error) {
	if strings.TrimSpace(opts.StateDir) == "" {
		return Result{State: "unknown", Network: "unknown", Reason: "state_directory_missing"}, fmt.Errorf("state directory is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{State: "unknown", Network: "unknown", Reason: "availability_check_cancelled"}, err
	}

	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	checkedAt := now().UTC()
	gapThreshold := opts.SleepGapThreshold
	if gapThreshold <= 0 {
		gapThreshold = DefaultSleepGapThreshold
	}
	cooldown := opts.Cooldown
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	probeTimeout := opts.ProbeTimeout
	if probeTimeout <= 0 {
		probeTimeout = DefaultProbeTimeout
	}

	stateDir := filepath.Join(opts.StateDir, stateDirectoryName)
	statePath := filepath.Join(stateDir, stateFileName)
	lockPath := filepath.Join(stateDir, lockFileName)
	lock, err := artifacts.AcquireOwnerOnlyFileLock(ctx, lockPath)
	if err != nil {
		return Result{State: "unknown", Network: "unknown", Reason: "availability_lock_failed", CheckedAt: checkedAt.Format(time.RFC3339)}, err
	}
	defer lock.Release()

	state, err := loadState(statePath)
	if err != nil {
		return Result{State: "unknown", Network: "unknown", Reason: "availability_state_unreadable", CheckedAt: checkedAt.Format(time.RFC3339)}, err
	}

	result := Result{
		State:          "unknown",
		Network:        "not_checked",
		LastObservedAt: formatTime(state.LastObservedAt),
		SuppressUntil:  formatTime(state.SuppressUntil),
		CheckedAt:      checkedAt.Format(time.RFC3339),
	}

	if !state.LastObservedAt.IsZero() {
		gap := checkedAt.Sub(state.LastObservedAt)
		if gap < 0 {
			state.LastObservedAt = checkedAt
			state.SuppressUntil = checkedAt.Add(cooldown)
			result.State = "unknown"
			result.Reason = "clock_changed"
			result.LastObservedAt = checkedAt.Format(time.RFC3339)
			result.SuppressUntil = state.SuppressUntil.Format(time.RFC3339)
			result.RetryAfterSeconds = secondsUntil(checkedAt, state.SuppressUntil)
			if err := saveState(statePath, state); err != nil {
				return Result{State: "unknown", Network: "unknown", Reason: "availability_state_unwritable", CheckedAt: result.CheckedAt}, err
			}
			return result, nil
		}
		if gap > gapThreshold {
			state.LastObservedAt = checkedAt
			state.SuppressUntil = checkedAt.Add(cooldown)
			result.Allowed = false
			result.State = "suspended"
			result.Network = "not_checked"
			result.SleepGapDetected = true
			result.SleepGapSeconds = int64(gap / time.Second)
			result.LastObservedAt = checkedAt.Format(time.RFC3339)
			result.SuppressUntil = state.SuppressUntil.Format(time.RFC3339)
			result.RetryAfterSeconds = secondsUntil(checkedAt, state.SuppressUntil)
			result.Reason = "wake_gap_detected"
			if err := saveState(statePath, state); err != nil {
				return Result{State: "unknown", Network: "unknown", Reason: "availability_state_unwritable", CheckedAt: result.CheckedAt}, err
			}
			return result, nil
		}
	}

	if state.SuppressUntil.After(checkedAt) {
		state.LastObservedAt = checkedAt
		result.Allowed = false
		result.State = "cooldown"
		result.Network = "not_checked"
		result.LastObservedAt = checkedAt.Format(time.RFC3339)
		result.SuppressUntil = state.SuppressUntil.Format(time.RFC3339)
		result.RetryAfterSeconds = secondsUntil(checkedAt, state.SuppressUntil)
		result.Reason = "post_wake_cooldown"
		if err := saveState(statePath, state); err != nil {
			return Result{State: "unknown", Network: "unknown", Reason: "availability_state_unwritable", CheckedAt: result.CheckedAt}, err
		}
		return result, nil
	}

	probe := opts.InternetProbe
	if probe == nil {
		url := strings.TrimSpace(opts.ConnectivityURL)
		if url == "" {
			url = strings.TrimSpace(os.Getenv(ConnectivityURLEnv))
		}
		if url == "" {
			url = DefaultConnectivityURL
		}
		probe = func(probeCtx context.Context) ProbeResult {
			return probeInternet(probeCtx, url)
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	probeResult := probe(probeCtx)
	cancel()
	if probeResult.Online {
		result.Allowed = true
		result.State = "ready"
		result.Network = "online"
		result.Reason = "environment_ready"
	} else {
		result.Allowed = false
		result.State = "offline"
		result.Network = "offline"
		result.Reason = safeReason(probeResult.Reason, "connectivity_probe_failed")
	}
	state.LastObservedAt = checkedAt
	state.SuppressUntil = time.Time{}
	result.LastObservedAt = checkedAt.Format(time.RFC3339)
	result.SuppressUntil = ""
	if err := saveState(statePath, state); err != nil {
		return Result{State: "unknown", Network: "unknown", Reason: "availability_state_unwritable", CheckedAt: result.CheckedAt}, err
	}
	return result, nil
}

// TryAcquireRepairLock serializes launch-capable Auto Heal work across
// headed and headless scheduled tasks. The returned lock must be released
// after the caller finishes its browser/daemon operation.
func TryAcquireRepairLock(ctx context.Context, stateDir string) (*artifacts.OwnerOnlyFileLock, bool, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, false, fmt.Errorf("state directory is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return artifacts.TryAcquireOwnerOnlyFileLock(filepath.Join(stateDir, stateDirectoryName, repairLockFileName))
}

func loadState(path string) (persistedState, error) {
	data, err := artifacts.ReadOwnerOnlyFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persistedState{}, nil
		}
		return persistedState{}, err
	}
	if len(data) > maxStateBytes {
		return persistedState{}, fmt.Errorf("availability state is too large")
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return persistedState{}, fmt.Errorf("parse availability state: %w", err)
	}
	return state, nil
}

func saveState(path string, state persistedState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal availability state: %w", err)
	}
	data = append(data, '\n')
	return artifacts.WriteOwnerOnlyFileAtomic(path, data)
}

func probeInternet(ctx context.Context, endpoint string) ProbeResult {
	if strings.TrimSpace(endpoint) == "" {
		return ProbeResult{Reason: "connectivity_probe_invalid"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
		return ProbeResult{Reason: "connectivity_probe_invalid"}
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ProbeResult{Reason: "connectivity_probe_timeout"}
		}
		return ProbeResult{Reason: "connectivity_probe_failed"}
	}
	_, _ = io.CopyN(io.Discard, resp.Body, 1024)
	_ = resp.Body.Close()
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusBadRequest {
		return ProbeResult{Online: true, Reason: "connectivity_probe_ok"}
	}
	return ProbeResult{Reason: "connectivity_http_status"}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func secondsUntil(now, until time.Time) int64 {
	if !until.After(now) {
		return 0
	}
	seconds := int64((until.Sub(now) + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func safeReason(reason, fallback string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fallback
	}
	return reason
}
