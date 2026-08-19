package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const (
	InvocationLeaseFileName     = "daemon-leases.json"
	DefaultInvocationLeaseTTL   = 2 * time.Minute
	MaxInvocationLeaseTTL       = 24 * time.Hour
	defaultLeaseCleanupTimeout  = 5 * time.Second
	defaultLeaseSweepInterval   = time.Second
	invocationLeaseStateVersion = 1
	leaseStateActive            = "active"
	leaseStateClosing           = "closing"
	leaseStateCleanupPending    = "cleanup_pending"
)

// InvocationLeasePathForMode returns the owner-only state file used to
// recover targets after a short-lived CLI process disappears.
func InvocationLeasePathForMode(stateDir, browserMode string) string {
	if runtimeModeName(browserMode) == "headless" {
		return filepath.Join(stateDir, "headless", InvocationLeaseFileName)
	}
	return filepath.Join(stateDir, InvocationLeaseFileName)
}

type LeaseInfo struct {
	LeaseID   string `json:"lease_id"`
	ExpiresAt string `json:"expires_at"`
	TTLMillis int64  `json:"ttl_ms"`
}

type LeaseTarget struct {
	TargetID   string `json:"target_id"`
	TargetType string `json:"target_type"`
	CreatedAt  string `json:"created_at"`
	Disposable bool   `json:"disposable,omitempty"`
}

type LeaseEndResult struct {
	LeaseID           string   `json:"lease_id"`
	State             string   `json:"state"`
	ClosedTargetCount int      `json:"closed_target_count"`
	PendingTargetIDs  []string `json:"pending_target_ids,omitempty"`
	LastError         string   `json:"last_error,omitempty"`
}

func (r LeaseEndResult) Error() error {
	if len(r.PendingTargetIDs) == 0 && strings.TrimSpace(r.LastError) == "" {
		return nil
	}
	if strings.TrimSpace(r.LastError) != "" {
		return fmt.Errorf("lease %s cleanup pending: %s", r.LeaseID, r.LastError)
	}
	return fmt.Errorf("lease %s cleanup pending for %d target(s)", r.LeaseID, len(r.PendingTargetIDs))
}

type LeaseReconcileResult struct {
	Checked           bool     `json:"checked"`
	ExpiredLeaseCount int      `json:"expired_lease_count"`
	ClosedTargetCount int      `json:"closed_target_count"`
	PendingTargetIDs  []string `json:"pending_target_ids,omitempty"`
	LastError         string   `json:"last_error,omitempty"`
}

type invocationLeaseFile struct {
	Version int               `json:"version"`
	Leases  []invocationLease `json:"leases"`
}

type invocationLease struct {
	LeaseInfo
	CreatedAt string        `json:"created_at"`
	State     string        `json:"state"`
	Targets   []LeaseTarget `json:"targets,omitempty"`
	LastError string        `json:"last_error,omitempty"`
}

type LeaseManager struct {
	mu             sync.Mutex
	reconcileMu    sync.Mutex
	path           string
	now            func() time.Time
	cleanupTimeout time.Duration
	sweepInterval  time.Duration
	leases         map[string]invocationLease
}

func NewLeaseManager(ctx context.Context, stateDir, browserMode string) (*LeaseManager, error) {
	return newLeaseManager(ctx, stateDir, browserMode, time.Now)
}

func newLeaseManager(ctx context.Context, stateDir, browserMode string, now func() time.Time) (*LeaseManager, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, fmt.Errorf("lease state directory is required")
	}
	if now == nil {
		now = time.Now
	}
	m := &LeaseManager{
		path:           InvocationLeasePathForMode(stateDir, browserMode),
		now:            now,
		cleanupTimeout: defaultLeaseCleanupTimeout,
		sweepInterval:  defaultLeaseSweepInterval,
		leases:         map[string]invocationLease{},
	}
	if err := m.load(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *LeaseManager) Begin(ctx context.Context, ttl time.Duration) (LeaseInfo, error) {
	if m == nil {
		return LeaseInfo{}, fmt.Errorf("lease manager is nil")
	}
	ttl = normalizeLeaseTTL(ttl)
	leaseID, err := newLeaseID()
	if err != nil {
		return LeaseInfo{}, err
	}
	now := m.now().UTC()
	info := LeaseInfo{
		LeaseID:   leaseID,
		ExpiresAt: now.Add(ttl).Format(time.RFC3339Nano),
		TTLMillis: ttl.Milliseconds(),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leases[leaseID] = invocationLease{
		LeaseInfo: info,
		CreatedAt: now.Format(time.RFC3339Nano),
		State:     leaseStateActive,
		Targets:   []LeaseTarget{},
	}
	if err := m.saveLocked(ctx); err != nil {
		delete(m.leases, leaseID)
		return LeaseInfo{}, fmt.Errorf("persist browser invocation lease: %w", err)
	}
	return info, nil
}

func (m *LeaseManager) Renew(ctx context.Context, leaseID string, ttl time.Duration) (LeaseInfo, error) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return LeaseInfo{}, fmt.Errorf("lease id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.leases[leaseID]
	if !ok {
		return LeaseInfo{}, fmt.Errorf("lease %s was not found", leaseID)
	}
	if record.State != leaseStateActive {
		return LeaseInfo{}, fmt.Errorf("lease %s is %s", leaseID, record.State)
	}
	if ttl <= 0 {
		ttl = time.Duration(record.TTLMillis) * time.Millisecond
	}
	ttl = normalizeLeaseTTL(ttl)
	record.TTLMillis = ttl.Milliseconds()
	record.ExpiresAt = m.now().UTC().Add(ttl).Format(time.RFC3339Nano)
	record.LastError = ""
	m.leases[leaseID] = record
	if err := m.saveLocked(ctx); err != nil {
		return LeaseInfo{}, fmt.Errorf("persist renewed browser invocation lease: %w", err)
	}
	return record.LeaseInfo, nil
}

// Touch extends an active lease using its original TTL. Daemon CDP activity
// renews the lease without requiring a separate heartbeat RPC from every CLI.
func (m *LeaseManager) Touch(ctx context.Context, leaseID string) error {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.leases[leaseID]
	if !ok {
		return fmt.Errorf("lease %s was not found", leaseID)
	}
	if record.State != leaseStateActive {
		return fmt.Errorf("lease %s is %s", leaseID, record.State)
	}
	ttl := normalizeLeaseTTL(time.Duration(record.TTLMillis) * time.Millisecond)
	record.ExpiresAt = m.now().UTC().Add(ttl).Format(time.RFC3339Nano)
	record.LastError = ""
	m.leases[leaseID] = record
	if err := m.saveLocked(ctx); err != nil {
		return fmt.Errorf("persist touched browser invocation lease: %w", err)
	}
	return nil
}

func (m *LeaseManager) RegisterTarget(ctx context.Context, leaseID string, target LeaseTarget) error {
	leaseID = strings.TrimSpace(leaseID)
	target.TargetID = strings.TrimSpace(target.TargetID)
	target.TargetType = strings.TrimSpace(target.TargetType)
	if leaseID == "" || target.TargetID == "" {
		return fmt.Errorf("lease id and target id are required")
	}
	if target.TargetType == "" {
		target.TargetType = "page"
	}
	if target.CreatedAt == "" {
		target.CreatedAt = m.now().UTC().Format(time.RFC3339Nano)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.leases[leaseID]
	if !ok {
		return fmt.Errorf("lease %s was not found", leaseID)
	}
	if record.State != leaseStateActive {
		return fmt.Errorf("lease %s is %s", leaseID, record.State)
	}
	for _, existing := range record.Targets {
		if existing.TargetID == target.TargetID {
			return nil
		}
	}
	record.Targets = append(record.Targets, target)
	m.leases[leaseID] = record
	if err := m.saveLocked(ctx); err != nil {
		return fmt.Errorf("persist target ownership for lease %s: %w", leaseID, err)
	}
	return nil
}

func (m *LeaseManager) UnregisterTarget(ctx context.Context, leaseID, targetID string) error {
	leaseID = strings.TrimSpace(leaseID)
	targetID = strings.TrimSpace(targetID)
	if leaseID == "" || targetID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.leases[leaseID]
	if !ok {
		return nil
	}
	filtered := record.Targets[:0]
	for _, target := range record.Targets {
		if target.TargetID != targetID {
			filtered = append(filtered, target)
		}
	}
	record.Targets = filtered
	m.leases[leaseID] = record
	if err := m.saveLocked(ctx); err != nil {
		return fmt.Errorf("persist target release for lease %s: %w", leaseID, err)
	}
	return nil
}

func (m *LeaseManager) SetTargetDisposable(ctx context.Context, leaseID, targetID string, disposable bool) error {
	leaseID = strings.TrimSpace(leaseID)
	targetID = strings.TrimSpace(targetID)
	if leaseID == "" || targetID == "" {
		return fmt.Errorf("lease id and target id are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.leases[leaseID]
	if !ok {
		return fmt.Errorf("lease %s was not found", leaseID)
	}
	for index := range record.Targets {
		if record.Targets[index].TargetID == targetID {
			record.Targets[index].Disposable = disposable
			m.leases[leaseID] = record
			if err := m.saveLocked(ctx); err != nil {
				return fmt.Errorf("persist target lifecycle policy for lease %s: %w", leaseID, err)
			}
			return nil
		}
	}
	return fmt.Errorf("target %s is not registered to lease %s", targetID, leaseID)
}

func (m *LeaseManager) End(ctx context.Context, client cdp.CommandClient, leaseID string) (LeaseEndResult, error) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return LeaseEndResult{}, fmt.Errorf("lease id is required")
	}
	if client == nil {
		return LeaseEndResult{}, fmt.Errorf("lease cleanup client is required")
	}
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	if err := m.markClosing(ctx, leaseID); err != nil {
		return LeaseEndResult{LeaseID: leaseID}, err
	}
	return m.cleanupLease(client, leaseID)
}

func (m *LeaseManager) ReconcileExpired(ctx context.Context, client cdp.CommandClient) (LeaseReconcileResult, error) {
	if m == nil {
		return LeaseReconcileResult{}, fmt.Errorf("lease manager is nil")
	}
	if client == nil {
		return LeaseReconcileResult{}, fmt.Errorf("lease reconciliation client is required")
	}
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()

	now := m.now().UTC()
	m.mu.Lock()
	var leaseIDs []string
	result := LeaseReconcileResult{Checked: true, PendingTargetIDs: []string{}}
	for leaseID, record := range m.leases {
		expired := record.State == leaseStateActive && leaseExpired(record.ExpiresAt, now)
		if expired {
			record.State = leaseStateClosing
			record.LastError = ""
			m.leases[leaseID] = record
			result.ExpiredLeaseCount++
		}
		if expired || record.State == leaseStateClosing || record.State == leaseStateCleanupPending {
			leaseIDs = append(leaseIDs, leaseID)
		}
	}
	markErr := m.saveLocked(ctx)
	m.mu.Unlock()
	if markErr != nil {
		return result, fmt.Errorf("persist expired browser invocation leases: %w", markErr)
	}

	for _, leaseID := range leaseIDs {
		end, err := m.cleanupLease(client, leaseID)
		result.ClosedTargetCount += end.ClosedTargetCount
		result.PendingTargetIDs = append(result.PendingTargetIDs, end.PendingTargetIDs...)
		if err != nil && result.LastError == "" {
			result.LastError = err.Error()
		}
		if end.LastError != "" && result.LastError == "" {
			result.LastError = end.LastError
		}
	}
	return result, nil
}

func (m *LeaseManager) Run(ctx context.Context, client cdp.CommandClient, report func(LeaseReconcileResult, error)) {
	if m == nil || client == nil {
		return
	}
	if report == nil {
		report = func(LeaseReconcileResult, error) {}
	}
	ticker := time.NewTicker(m.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := m.ReconcileExpired(ctx, client)
			report(result, err)
		}
	}
}

func (m *LeaseManager) markClosing(ctx context.Context, leaseID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.leases[leaseID]
	if !ok {
		return fmt.Errorf("lease %s was not found", leaseID)
	}
	if record.State == leaseStateCleanupPending {
		return nil
	}
	record.State = leaseStateClosing
	record.ExpiresAt = m.now().UTC().Format(time.RFC3339Nano)
	m.leases[leaseID] = record
	if err := m.saveLocked(ctx); err != nil {
		return fmt.Errorf("persist closing browser invocation lease: %w", err)
	}
	return nil
}

func (m *LeaseManager) cleanupLease(client cdp.CommandClient, leaseID string) (LeaseEndResult, error) {
	m.mu.Lock()
	record, ok := m.leases[leaseID]
	m.mu.Unlock()
	if !ok {
		return LeaseEndResult{LeaseID: leaseID, State: "closed"}, nil
	}
	result := LeaseEndResult{LeaseID: leaseID, State: record.State}
	for _, target := range append([]LeaseTarget(nil), record.Targets...) {
		if !target.Disposable {
			if removeErr := m.removeTarget(leaseID, target.TargetID); removeErr != nil && result.LastError == "" {
				result.LastError = removeErr.Error()
			}
			continue
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), m.cleanupTimeout)
		err := closeOwnedTarget(cleanupCtx, client, target.TargetID)
		cancel()
		if err == nil {
			result.ClosedTargetCount++
			if removeErr := m.removeTarget(leaseID, target.TargetID); removeErr != nil && result.LastError == "" {
				result.LastError = removeErr.Error()
			}
			continue
		}
		result.PendingTargetIDs = append(result.PendingTargetIDs, target.TargetID)
		if result.LastError == "" {
			result.LastError = err.Error()
		}
		_ = m.recordCleanupError(leaseID, err)
	}

	m.mu.Lock()
	record, ok = m.leases[leaseID]
	if !ok {
		result.State = "closed"
		m.mu.Unlock()
		return result, result.Error()
	}
	if len(record.Targets) == 0 {
		delete(m.leases, leaseID)
		result.State = "closed"
	} else {
		record.State = leaseStateCleanupPending
		if result.LastError != "" {
			record.LastError = result.LastError
		}
		m.leases[leaseID] = record
		result.State = record.State
	}
	persistErr := m.saveLocked(context.Background())
	m.mu.Unlock()
	if persistErr != nil && result.LastError == "" {
		result.LastError = persistErr.Error()
	}
	return result, result.Error()
}

func (m *LeaseManager) removeTarget(leaseID, targetID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.leases[leaseID]
	if !ok {
		return nil
	}
	filtered := record.Targets[:0]
	for _, target := range record.Targets {
		if target.TargetID != targetID {
			filtered = append(filtered, target)
		}
	}
	record.Targets = filtered
	m.leases[leaseID] = record
	return m.saveLocked(context.Background())
}

func (m *LeaseManager) recordCleanupError(leaseID string, cleanupErr error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.leases[leaseID]
	if !ok {
		return nil
	}
	record.State = leaseStateCleanupPending
	record.LastError = cleanupErr.Error()
	m.leases[leaseID] = record
	return m.saveLocked(context.Background())
}

func (m *LeaseManager) load(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	b, err := readLeaseFile(m.path)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	var file invocationLeaseFile
	if err := json.Unmarshal(b, &file); err != nil {
		return fmt.Errorf("parse browser invocation leases: %w", err)
	}
	if file.Version != 0 && file.Version != invocationLeaseStateVersion {
		return fmt.Errorf("unsupported browser invocation lease state version %d", file.Version)
	}
	for _, record := range file.Leases {
		if strings.TrimSpace(record.LeaseID) == "" {
			continue
		}
		if record.State == "" {
			record.State = leaseStateActive
		}
		m.leases[record.LeaseID] = record
	}
	return nil
}

func (m *LeaseManager) saveLocked(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	records := make([]invocationLease, 0, len(m.leases))
	for _, record := range m.leases {
		records = append(records, record)
	}
	data, err := json.MarshalIndent(invocationLeaseFile{Version: invocationLeaseStateVersion, Leases: records}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(m.path, data, 0o600); err != nil {
		return err
	}
	return nil
}

func readLeaseFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read browser invocation leases: %w", err)
	}
	return b, nil
}

func closeOwnedTarget(ctx context.Context, client cdp.CommandClient, targetID string) error {
	var result struct {
		Success bool `json:"success"`
	}
	if err := client.Call(ctx, "Target.closeTarget", map[string]any{"targetId": targetID}, &result); err != nil {
		if targetGoneError(err) {
			return nil
		}
		return fmt.Errorf("close owned target %s: %w", targetID, err)
	}
	if result.Success {
		return nil
	}
	if target, err := targetInfo(ctx, client, targetID); err != nil && targetGoneError(err) {
		return nil
	} else if err == nil && target.TargetID == "" {
		return nil
	}
	return fmt.Errorf("close owned target %s returned success=false", targetID)
}

func targetInfo(ctx context.Context, client cdp.CommandClient, targetID string) (cdp.TargetInfo, error) {
	return cdp.TargetInfoWithClient(ctx, client, targetID)
}

func targetGoneError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, needle := range []string{"target not found", "target closed", "no such target", "unknown target"} {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

func leaseExpired(raw string, now time.Time) bool {
	when, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	return err == nil && !when.After(now)
}

func normalizeLeaseTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		ttl = DefaultInvocationLeaseTTL
	}
	if ttl > MaxInvocationLeaseTTL {
		ttl = MaxInvocationLeaseTTL
	}
	return ttl
}

func newLeaseID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate browser invocation lease id: %w", err)
	}
	return "lease-" + hex.EncodeToString(raw), nil
}
