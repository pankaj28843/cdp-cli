package admission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

const SchemaVersion = "webagent-admission/v1"

type Phase string

const (
	PhaseActive   Phase = "active"
	PhaseReleased Phase = "released"
)

type Config struct {
	StateDir       string
	MinimumSpacing time.Duration
	Now            func() time.Time
	WaitUntil      func(context.Context, time.Time) error
}

type Gate struct {
	dir            string
	minimumSpacing time.Duration
	now            func() time.Time
	waitUntil      func(context.Context, time.Time) error
}

type Request struct {
	Provider  string
	Operation string
	RunID     string
}

type Outcome string

const (
	OutcomeCompleted    Outcome = "completed"
	OutcomeTerminal     Outcome = "terminal"
	OutcomeIncomplete   Outcome = "incomplete"
	OutcomeFailed       Outcome = "failed"
	OutcomeRateLimited  Outcome = "rate_limited"
	OutcomeUnknown      Outcome = "unknown"
	OutcomeAbandoned    Outcome = "abandoned"
	OutcomeAcknowledged Outcome = "acknowledged"
)

type Release struct {
	Outcome       Outcome
	CooldownUntil time.Time
}

type Record struct {
	SchemaVersion   string  `json:"schema_version"`
	Provider        string  `json:"provider"`
	Operation       string  `json:"operation"`
	RunID           string  `json:"run_id"`
	Mutating        bool    `json:"mutating"`
	Phase           Phase   `json:"phase"`
	Outcome         Outcome `json:"outcome,omitempty"`
	StartedAt       string  `json:"started_at"`
	ReleasedAt      string  `json:"released_at,omitempty"`
	NextAllowedAt   string  `json:"next_allowed_at"`
	CooldownUntil   string  `json:"cooldown_until,omitempty"`
	PreviousRunID   string  `json:"previous_run_id,omitempty"`
	PreviousOutcome Outcome `json:"previous_outcome,omitempty"`
}

type BlockedError struct {
	Provider         string
	Operation        string
	RunID            string
	Reason           string
	RetryAt          time.Time
	ResolutionNeeded bool
}

func (e *BlockedError) Error() string {
	if e.ResolutionNeeded {
		return fmt.Sprintf(
			"provider %s admission blocked by %s for run %s; explicit recovery resolution is required",
			e.Provider,
			e.Reason,
			e.RunID,
		)
	}
	return fmt.Sprintf(
		"provider %s admission blocked by %s until %s",
		e.Provider,
		e.Reason,
		e.RetryAt.UTC().Format(time.RFC3339Nano),
	)
}

type Lease struct {
	gate *Gate
	lock *artifacts.OwnerOnlyFileLock
	path string

	mu       sync.Mutex
	record   Record
	released bool
}

func New(config Config) (*Gate, error) {
	stateDir := strings.TrimSpace(config.StateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("state directory is required")
	}
	if config.MinimumSpacing < 0 {
		return nil, fmt.Errorf("minimum spacing must not be negative")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.WaitUntil == nil {
		config.WaitUntil = waitUntilContext
	}
	return &Gate{
		dir:            filepath.Join(stateDir, "webagent", "admission"),
		minimumSpacing: config.MinimumSpacing,
		now:            config.Now,
		waitUntil:      config.WaitUntil,
	}, nil
}

func (g *Gate) Acquire(ctx context.Context, request Request) (*Lease, error) {
	if err := validateIdentity("provider", request.Provider); err != nil {
		return nil, err
	}
	if err := validateIdentity("operation", request.Operation); err != nil {
		return nil, err
	}
	if err := validateIdentity("run_id", request.RunID); err != nil {
		return nil, err
	}
	for {
		lease, err := g.acquireOnce(ctx, request)
		var blocked *BlockedError
		if !errors.As(err, &blocked) ||
			blocked.Reason != "minimum_spacing" {
			return lease, err
		}
		if err := g.waitUntil(ctx, blocked.RetryAt); err != nil {
			return nil, fmt.Errorf(
				"wait for provider %s minimum spacing: %w",
				request.Provider,
				err,
			)
		}
	}
}

// Status reads one provider's admission record under the same owner-only lock
// used by Acquire. It never returns provider request or response data.
func (g *Gate) Status(ctx context.Context, provider string) (Record, bool, error) {
	if err := validateIdentity("provider", provider); err != nil {
		return Record{}, false, err
	}
	path := filepath.Join(g.dir, provider+".json")
	lock, err := artifacts.AcquireOwnerOnlyFileLock(ctx, path+".lock")
	if err != nil {
		return Record{}, false, fmt.Errorf("acquire provider %s admission status: %w", provider, err)
	}
	record, found, readErr := readRecord(path)
	releaseErr := lock.Release()
	if readErr != nil || releaseErr != nil {
		return Record{}, false, errors.Join(readErr, releaseErr)
	}
	return record, found, nil
}

// Resolve acknowledges one exact orphaned or unknown mutating run after the
// caller has independently reconciled its durable action/cleanup evidence.
// It cannot resolve a different run or an ordinary completed record.
func (g *Gate) Resolve(ctx context.Context, request Request) (Record, error) {
	if err := validateIdentity("provider", request.Provider); err != nil {
		return Record{}, err
	}
	if err := validateIdentity("operation", request.Operation); err != nil {
		return Record{}, err
	}
	if err := validateIdentity("run_id", request.RunID); err != nil {
		return Record{}, err
	}
	path := filepath.Join(g.dir, request.Provider+".json")
	lock, err := artifacts.AcquireOwnerOnlyFileLock(ctx, path+".lock")
	if err != nil {
		return Record{}, fmt.Errorf("acquire provider %s admission resolution: %w", request.Provider, err)
	}
	releaseWith := func(result Record, resolveErr error) (Record, error) {
		return result, errors.Join(resolveErr, lock.Release())
	}
	record, found, err := readRecord(path)
	if err != nil {
		return releaseWith(Record{}, err)
	}
	if !found {
		return releaseWith(Record{}, fmt.Errorf("provider %s admission record was not found", request.Provider))
	}
	if record.Provider != request.Provider ||
		record.Operation != request.Operation ||
		record.RunID != request.RunID {
		return releaseWith(Record{}, fmt.Errorf("admission resolution identity does not match the persisted run"))
	}
	if record.Phase == PhaseReleased && record.Outcome == OutcomeAcknowledged {
		return releaseWith(record, nil)
	}
	if !((record.Phase == PhaseActive && record.mayMutate()) ||
		(record.Phase == PhaseReleased && record.Outcome == OutcomeUnknown)) {
		return releaseWith(Record{}, fmt.Errorf("admission run %s is not awaiting explicit resolution", request.RunID))
	}

	now := g.now().UTC()
	nextAllowed, err := parseTimestamp("next_allowed_at", record.NextAllowedAt)
	if err != nil {
		return releaseWith(Record{}, err)
	}
	if spaced := now.Add(g.minimumSpacing); spaced.After(nextAllowed) {
		nextAllowed = spaced
	}
	record.Phase = PhaseReleased
	record.Outcome = OutcomeAcknowledged
	record.ReleasedAt = timestamp(now)
	record.NextAllowedAt = timestamp(nextAllowed)
	if err := writeRecord(path, record); err != nil {
		return releaseWith(Record{}, err)
	}
	return releaseWith(record, nil)
}

func (g *Gate) acquireOnce(
	ctx context.Context,
	request Request,
) (*Lease, error) {
	path := filepath.Join(g.dir, request.Provider+".json")
	lock, err := artifacts.AcquireOwnerOnlyFileLock(ctx, path+".lock")
	if err != nil {
		return nil, fmt.Errorf("acquire provider %s admission: %w", request.Provider, err)
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			_ = lock.Release()
		}
	}()

	previous, found, err := readRecord(path)
	if err != nil {
		return nil, fmt.Errorf("read provider %s admission: %w", request.Provider, err)
	}
	now := g.now().UTC()
	if found {
		if previous.Provider != request.Provider {
			return nil, fmt.Errorf("admission provider mismatch: file has %q, request has %q", previous.Provider, request.Provider)
		}
		if previous.Phase == PhaseActive && !previous.mayMutate() {
			previous.Phase = PhaseReleased
			previous.Outcome = OutcomeAbandoned
			previous.ReleasedAt = timestamp(now)
			if err := writeRecord(path, previous); err != nil {
				return nil, fmt.Errorf("abandon orphaned read-only provider %s admission: %w", request.Provider, err)
			}
		}
		retryAt, reason, resolutionNeeded, err := previous.blockedUntil(now)
		if err != nil {
			return nil, err
		}
		if !retryAt.IsZero() {
			return nil, &BlockedError{
				Provider:         request.Provider,
				Operation:        previous.Operation,
				RunID:            previous.RunID,
				Reason:           reason,
				RetryAt:          retryAt,
				ResolutionNeeded: resolutionNeeded,
			}
		}
	}

	nextAllowed := now.Add(g.minimumSpacing)
	record := Record{
		SchemaVersion: SchemaVersion,
		Provider:      request.Provider,
		Operation:     request.Operation,
		RunID:         request.RunID,
		Mutating:      operationMayMutate(request.Operation),
		Phase:         PhaseActive,
		StartedAt:     timestamp(now),
		NextAllowedAt: timestamp(nextAllowed),
	}
	if found {
		record.PreviousRunID = previous.RunID
		record.PreviousOutcome = previous.Outcome
	}
	if err := writeRecord(path, record); err != nil {
		return nil, fmt.Errorf("persist provider %s admission: %w", request.Provider, err)
	}

	releaseOnError = false
	return &Lease{gate: g, lock: lock, path: path, record: record}, nil
}

func waitUntilContext(ctx context.Context, until time.Time) error {
	delay := time.Until(until)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (l *Lease) Record() Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.record
}

// Release persists the post-run spacing/cooldown decision before unlocking the
// provider. It never records prompt, response, cookie, header, or token data.
func (l *Lease) Release(result Release) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true

	outcome := Outcome(strings.TrimSpace(string(result.Outcome)))
	if outcome == "" {
		outcome = OutcomeCompleted
	}
	if !validOutcome(outcome) {
		err := fmt.Errorf("invalid admission outcome %q", outcome)
		return errors.Join(err, l.lock.Release())
	}
	now := l.gate.now().UTC()
	nextAllowed := now.Add(l.gate.minimumSpacing)
	if current, err := parseTimestamp("next_allowed_at", l.record.NextAllowedAt); err != nil {
		return errors.Join(err, l.lock.Release())
	} else if current.After(nextAllowed) {
		nextAllowed = current
	}
	if !result.CooldownUntil.IsZero() && result.CooldownUntil.UTC().After(nextAllowed) {
		nextAllowed = result.CooldownUntil.UTC()
	}

	l.record.Phase = PhaseReleased
	l.record.Outcome = outcome
	l.record.ReleasedAt = timestamp(now)
	l.record.NextAllowedAt = timestamp(nextAllowed)
	if !result.CooldownUntil.IsZero() {
		l.record.CooldownUntil = timestamp(result.CooldownUntil.UTC())
	}
	persistErr := writeRecord(l.path, l.record)
	return errors.Join(persistErr, l.lock.Release())
}

func (r Record) blockedUntil(now time.Time) (time.Time, string, bool, error) {
	if err := r.Validate(); err != nil {
		return time.Time{}, "", false, fmt.Errorf("validate admission record: %w", err)
	}
	if r.RequiresResolution() {
		reason := "unresolved_unknown_outcome"
		if r.Phase == PhaseActive {
			reason = "unreconciled_active_mutation"
		}
		return manualResolutionRetryAt(), reason, true, nil
	}
	nextAllowed, err := parseTimestamp("next_allowed_at", r.NextAllowedAt)
	if err != nil {
		return time.Time{}, "", false, err
	}
	var retryAt time.Time
	reason := ""
	if nextAllowed.After(now) {
		retryAt = nextAllowed
		reason = "minimum_spacing"
	}
	if r.CooldownUntil != "" {
		cooldown, err := parseTimestamp("cooldown_until", r.CooldownUntil)
		if err != nil {
			return time.Time{}, "", false, err
		}
		if cooldown.After(now) && (retryAt.IsZero() || !cooldown.Before(retryAt)) {
			retryAt = cooldown
			reason = "cooldown"
		}
	}
	return retryAt, reason, false, nil
}

func (r Record) mayMutate() bool {
	return r.Mutating || operationMayMutate(r.Operation)
}

// RequiresResolution reports whether this exact run must remain fail-closed
// until durable recovery evidence is reconciled and explicitly acknowledged.
func (r Record) RequiresResolution() bool {
	return (r.Phase == PhaseActive && r.mayMutate()) ||
		(r.Phase == PhaseReleased && r.Outcome == OutcomeUnknown)
}

func manualResolutionRetryAt() time.Time {
	return time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)
}

func (r Record) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %q", SchemaVersion)
	}
	if err := validateIdentity("provider", r.Provider); err != nil {
		return err
	}
	if err := validateIdentity("operation", r.Operation); err != nil {
		return err
	}
	if err := validateIdentity("run_id", r.RunID); err != nil {
		return err
	}
	if r.PreviousRunID != "" {
		if err := validateIdentity("previous_run_id", r.PreviousRunID); err != nil {
			return err
		}
	}
	if r.Phase != PhaseActive && r.Phase != PhaseReleased {
		return fmt.Errorf("invalid admission phase %q", r.Phase)
	}
	if _, err := parseTimestamp("started_at", r.StartedAt); err != nil {
		return err
	}
	if _, err := parseTimestamp("next_allowed_at", r.NextAllowedAt); err != nil {
		return err
	}
	if r.Phase == PhaseReleased {
		if _, err := parseTimestamp("released_at", r.ReleasedAt); err != nil {
			return err
		}
	}
	if r.CooldownUntil != "" {
		if _, err := parseTimestamp("cooldown_until", r.CooldownUntil); err != nil {
			return err
		}
	}
	if r.Outcome != "" && !validOutcome(r.Outcome) {
		return fmt.Errorf("invalid admission outcome %q", r.Outcome)
	}
	if r.PreviousOutcome != "" && !validOutcome(r.PreviousOutcome) {
		return fmt.Errorf("invalid previous admission outcome %q", r.PreviousOutcome)
	}
	return nil
}

func readRecord(path string) (Record, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, false, nil
		}
		return Record{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Record{}, false, fmt.Errorf("admission path %s must be a regular file, not a symlink", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, false, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, false, fmt.Errorf("parse admission record: %w", err)
	}
	if err := record.Validate(); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}

func writeRecord(path string, record Record) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate admission record: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal admission record: %w", err)
	}
	data = append(data, '\n')
	return artifacts.WriteOwnerOnlyFileAtomic(path, data)
}

func timestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTimestamp(name, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339: %w", name, err)
	}
	return parsed, nil
}

func validateIdentity(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > 128 {
		return fmt.Errorf("%s exceeds 128 bytes", name)
	}
	for _, r := range value {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.') {
			return fmt.Errorf("%s contains unsupported character %q", name, r)
		}
	}
	return nil
}

func validOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeCompleted, OutcomeTerminal, OutcomeIncomplete, OutcomeFailed,
		OutcomeRateLimited, OutcomeUnknown, OutcomeAbandoned, OutcomeAcknowledged:
		return true
	default:
		return false
	}
}

func operationMayMutate(operation string) bool {
	switch strings.TrimSpace(operation) {
	case "capabilities",
		"doctor",
		"auth.refresh",
		"catalog.status",
		"catalog.refresh",
		"courses.list",
		"chapters.list",
		"content.fetch",
		"conversations.list",
		"conversations.detail",
		"conversations.await",
		"conversations.download_artifact":
		return false
	default:
		// Unknown future operations fail closed until explicitly classified.
		return true
	}
}
