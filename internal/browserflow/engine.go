package browserflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const (
	defaultCloseTimeout        = 10 * time.Second
	defaultClosePollInterval   = 50 * time.Millisecond
	defaultMaxDispatchAttempts = 2
	defaultAmbiguousCooldown   = 5 * time.Minute
	headedInputLockName        = "headed-browser-input.lock"
)

type Config struct {
	Client              cdp.CommandClient
	Journal             Journal
	Budget              cdp.BrowserResourceBudgetOptions
	AllowOverBudget     bool
	CloseTimeout        time.Duration
	ClosePollInterval   time.Duration
	MaxDispatchAttempts int
	AmbiguousCooldown   time.Duration
	InputLockPath       string
	Now                 func() time.Time
}

type Engine struct {
	client              cdp.CommandClient
	journal             Journal
	budget              cdp.BrowserResourceBudgetOptions
	allowOverBudget     bool
	closeTimeout        time.Duration
	closePollInterval   time.Duration
	maxDispatchAttempts int
	ambiguousCooldown   time.Duration
	inputLockPath       string
	now                 func() time.Time
}

type AcquireRequest struct {
	RunID      string
	Provider   string
	Operation  string
	ActionName string
	InitialURL string
}

type BudgetExceededError struct {
	Budget cdp.BrowserResourceBudget
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf(
		"browser resource budget exceeded: %d/%d tabs, %d/%d windows",
		e.Budget.TabCount,
		e.Budget.MaxTabs,
		e.Budget.WindowCount,
		e.Budget.MaxWindows,
	)
}

type Dispatcher interface {
	Dispatch(context.Context, *cdp.PageSession) (DispatchOutcome, error)
}

type DispatchFunc func(context.Context, *cdp.PageSession) (DispatchOutcome, error)

func (f DispatchFunc) Dispatch(ctx context.Context, session *cdp.PageSession) (DispatchOutcome, error) {
	return f(ctx, session)
}

type DispatchOutcome struct {
	Dispatch          Dispatch
	RawInputAttempted bool
}

type CleanupResult struct {
	State              CleanupState `json:"state"`
	TargetID           string       `json:"target_id"`
	DetachError        string       `json:"detach_error,omitempty"`
	CloseAttemptCount  int          `json:"close_attempt_count"`
	CloseSent          bool         `json:"close_sent"`
	TargetPollObserved bool         `json:"target_poll_observed"`
	TargetGone         bool         `json:"target_gone"`
	FailurePhase       string       `json:"failure_phase,omitempty"`
	CloseError         string       `json:"close_error,omitempty"`
	PollError          string       `json:"poll_error,omitempty"`
}

func (r CleanupResult) Error() error {
	if r.State == CleanupClosed && r.TargetGone {
		return nil
	}
	return fmt.Errorf("exact target %s cleanup did not settle", r.TargetID)
}

type Lease struct {
	engine  *Engine
	session *cdp.PageSession

	mu     sync.Mutex
	record Record
	closed bool

	inputLock *artifacts.OwnerOnlyFileLock
}

// HeadedInputLockPath is the cross-repository owner-local focus/input lease.
// Every automated provider that drives the same headed browser must use this
// exact path before it activates a target or dispatches browser input.
func HeadedInputLockPath(stateDir string) string {
	return filepath.Join(stateDir, "locks", headedInputLockName)
}

func New(config Config) (*Engine, error) {
	if config.Client == nil {
		return nil, fmt.Errorf("browserflow client is required")
	}
	if config.Journal == nil {
		return nil, fmt.Errorf("browserflow journal is required")
	}
	if config.CloseTimeout <= 0 {
		config.CloseTimeout = defaultCloseTimeout
	}
	if config.ClosePollInterval <= 0 {
		config.ClosePollInterval = defaultClosePollInterval
	}
	if config.MaxDispatchAttempts <= 0 {
		config.MaxDispatchAttempts = defaultMaxDispatchAttempts
	}
	if config.MaxDispatchAttempts > defaultMaxDispatchAttempts {
		return nil, fmt.Errorf("max dispatch attempts must not exceed %d", defaultMaxDispatchAttempts)
	}
	if config.AmbiguousCooldown <= 0 {
		config.AmbiguousCooldown = defaultAmbiguousCooldown
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Engine{
		client:              config.Client,
		journal:             config.Journal,
		budget:              config.Budget,
		allowOverBudget:     config.AllowOverBudget,
		closeTimeout:        config.CloseTimeout,
		closePollInterval:   config.ClosePollInterval,
		maxDispatchAttempts: config.MaxDispatchAttempts,
		ambiguousCooldown:   config.AmbiguousCooldown,
		inputLockPath:       strings.TrimSpace(config.InputLockPath),
		now:                 config.Now,
	}, nil
}

func (e *Engine) Acquire(ctx context.Context, request AcquireRequest) (*Lease, error) {
	if err := validateAcquireRequest(request); err != nil {
		return nil, err
	}
	now := e.timestamp()
	record := Record{
		SchemaVersion: RecoverySchemaVersion,
		RunID:         request.RunID,
		Provider:      request.Provider,
		Operation:     request.Operation,
		ActionName:    strings.TrimSpace(request.ActionName),
		Phase:         PhasePlanned,
		Cleanup:       CleanupNotRequired,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := e.journal.Create(ctx, record); err != nil {
		return nil, fmt.Errorf("create browserflow recovery record: %w", err)
	}

	budget, err := cdp.BrowserBudget(ctx, e.client, e.budget)
	if err != nil {
		return nil, e.failWithoutTarget(ctx, record, "budget_check_failed", fmt.Errorf("check browser resource budget: %w", err))
	}
	record.Budget = budgetEvidence(budget, e.allowOverBudget)
	if budget.OverBudgetForNewPage() && !e.allowOverBudget {
		return nil, e.failWithoutTarget(ctx, record, "browser_resource_budget_exceeded", &BudgetExceededError{Budget: budget})
	}
	record.Phase = PhaseBudgetChecked
	if err := e.save(ctx, &record); err != nil {
		return nil, fmt.Errorf("persist budget_checked: %w", err)
	}

	var inputLock *artifacts.OwnerOnlyFileLock
	if e.inputLockPath != "" {
		inputLock, err = artifacts.AcquireOwnerOnlyFileLock(ctx, e.inputLockPath)
		if err != nil {
			return nil, e.failWithoutTarget(
				ctx,
				record,
				"browser_input_lock_unavailable",
				fmt.Errorf("acquire headed browser input lease: %w", err),
			)
		}
	}
	releaseInput := func() error {
		if inputLock == nil {
			return nil
		}
		err := inputLock.Release()
		inputLock = nil
		return err
	}

	targetID, err := cdp.CreateTargetWithClient(ctx, e.client, request.InitialURL)
	if err != nil {
		return nil, errors.Join(
			e.failWithoutTarget(ctx, record, "target_create_failed", fmt.Errorf("create exact target: %w", err)),
			releaseInput(),
		)
	}
	record.TargetID = targetID
	record.Cleanup = CleanupPending
	record.Phase = PhaseTargetOwned
	if err := e.save(ctx, &record); err != nil {
		retryErr := e.save(context.Background(), &record)
		if retryErr != nil {
			cleanup := e.closeExactTarget(nil, targetID)
			return nil, errors.Join(
				fmt.Errorf("persist exact target %s ownership: %w", targetID, err),
				fmt.Errorf("retry exact target %s ownership persistence: %w", targetID, retryErr),
				cleanup.Error(),
				releaseInput(),
			)
		}
	}

	session, err := cdp.AttachToTargetWithClient(ctx, e.client, targetID, nil)
	if err != nil {
		record.Phase = PhaseCleanupPending
		record.Cleanup = CleanupPending
		record.LastErrorClass = "target_attach_failed"
		persistPendingErr := e.save(context.Background(), &record)
		cleanup := e.closeExactTarget(nil, targetID)
		var persistResultErr error
		if cleanup.Error() == nil {
			record.Phase = PhaseClosed
			record.Cleanup = CleanupClosed
		} else {
			record.Phase = PhaseCleanupPending
			record.Cleanup = CleanupFailed
		}
		if persistPendingErr == nil {
			persistResultErr = e.save(context.Background(), &record)
		}
		return nil, errors.Join(
			fmt.Errorf("attach exact target %s: %w", targetID, err),
			persistPendingErr,
			cleanup.Error(),
			persistResultErr,
			releaseInput(),
		)
	}
	record.SessionID = session.SessionID
	record.Phase = PhaseAttached
	if err := e.save(ctx, &record); err != nil {
		cleanup := e.closeExactTarget(session, targetID)
		return nil, errors.Join(
			fmt.Errorf("persist attached target: %w", err),
			cleanup.Error(),
			releaseInput(),
		)
	}
	return &Lease{
		engine:    e,
		session:   session,
		record:    record,
		inputLock: inputLock,
	}, nil
}

func (e *Engine) Recover(ctx context.Context, runID string) (CleanupResult, error) {
	record, err := e.journal.Load(ctx, runID)
	if err != nil {
		return CleanupResult{}, err
	}
	if record.Phase == PhaseClosed {
		return CleanupResult{
			State:      CleanupClosed,
			TargetID:   record.TargetID,
			TargetGone: true,
		}, nil
	}
	if record.TargetID == "" {
		return CleanupResult{
			State:    CleanupNotRequired,
			TargetID: "",
		}, nil
	}

	var evidenceErr error
	if record.Phase == PhaseActionPending {
		record.Phase = PhaseActionUnknown
		record.Dispatch = DispatchUnknown
		record.RawInputCount = 1
		record.PendingPersisted = true
		record.RetryAt = e.now().UTC().Add(e.ambiguousCooldown).Format(time.RFC3339Nano)
		record.LastErrorClass = "action_dispatch_ambiguous_after_restart"
		evidenceErr = e.save(ctx, &record)
	}
	record.Phase = PhaseCleanupPending
	record.Cleanup = CleanupPending
	if err := e.save(ctx, &record); err != nil {
		evidenceErr = errors.Join(evidenceErr, err)
	}

	cleanup := e.closeExactTarget(nil, record.TargetID)
	if cleanup.Error() == nil {
		record.Phase = PhaseClosed
		record.Cleanup = CleanupClosed
		if record.Dispatch != DispatchUnknown {
			record.LastErrorClass = ""
		}
	} else {
		record.Phase = PhaseCleanupPending
		record.Cleanup = CleanupFailed
		record.LastErrorClass = "exact_target_cleanup_failed"
	}
	saveErr := e.save(context.Background(), &record)
	return cleanup, errors.Join(evidenceErr, cleanup.Error(), saveErr)
}

func (l *Lease) TargetID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.record.TargetID
}

func (l *Lease) Session() *cdp.PageSession {
	return l.session
}

func (l *Lease) Record() Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.record
}

// ReleaseInput ends the shared headed-browser focus/input lease. Provider
// workflows should call it immediately after their last raw-input dispatch;
// Close is a conservative fallback for every earlier return path.
func (l *Lease) ReleaseInput() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.releaseInputLocked()
}

// BindInputFingerprint persists a privacy-safe identity proof before the
// irreversible action is marked prepared. Recovery can then reconcile an
// ambiguous dispatch without storing the raw provider input.
func (l *Lease) BindInputFingerprint(
	ctx context.Context,
	fingerprint string,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return fmt.Errorf("browserflow lease is closed")
	}
	if l.record.Phase != PhaseAttached {
		return fmt.Errorf(
			"bind input fingerprint from phase %q: expected attached",
			l.record.Phase,
		)
	}
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return fmt.Errorf("input fingerprint is required")
	}
	next := l.record
	next.InputFingerprint = fingerprint
	if err := next.Validate(); err != nil {
		return fmt.Errorf("validate input fingerprint: %w", err)
	}
	if err := l.engine.save(ctx, &next); err != nil {
		return err
	}
	l.record = next
	return nil
}

func (l *Lease) MarkPrepared(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return fmt.Errorf("browserflow lease is closed")
	}
	if l.record.Phase != PhaseAttached {
		return fmt.Errorf("mark prepared from phase %q: expected attached", l.record.Phase)
	}
	next := l.record
	next.Phase = PhasePrepared
	next.LastErrorClass = ""
	if err := l.engine.save(ctx, &next); err != nil {
		return err
	}
	l.record = next
	return nil
}

func (l *Lease) Dispatch(ctx context.Context, dispatcher Dispatcher) (DispatchOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return DispatchOutcome{}, fmt.Errorf("browserflow lease is closed")
	}
	if dispatcher == nil {
		return DispatchOutcome{}, fmt.Errorf("irreversible action dispatcher is required")
	}
	if l.record.Phase != PhasePrepared {
		return DispatchOutcome{}, fmt.Errorf("dispatch from phase %q: expected prepared", l.record.Phase)
	}
	if l.record.RawInputCount != 0 {
		return DispatchOutcome{}, fmt.Errorf("raw input was already attempted for run %s", l.record.RunID)
	}
	if l.record.ActionAttemptCount >= l.engine.maxDispatchAttempts {
		return DispatchOutcome{}, fmt.Errorf("bounded dispatch attempts exhausted for run %s", l.record.RunID)
	}

	pending := l.record
	pending.Phase = PhaseActionPending
	pending.ActionAttemptCount++
	pending.PendingPersisted = true
	if err := l.engine.save(ctx, &pending); err != nil {
		return DispatchOutcome{}, fmt.Errorf("persist action_pending before raw input: %w", err)
	}
	l.record = pending

	outcome, dispatchErr := dispatcher.Dispatch(ctx, l.session)
	classificationErr := validateDispatchOutcome(outcome)
	if classificationErr != nil {
		outcome = DispatchOutcome{Dispatch: DispatchUnknown, RawInputAttempted: true}
	}

	observed := l.record
	observed.Dispatch = outcome.Dispatch
	if outcome.RawInputAttempted {
		observed.RawInputCount = 1
	}
	observed.RetryAt = ""
	switch outcome.Dispatch {
	case DispatchNotPerformed:
		observed.Phase = PhasePrepared
		observed.LastErrorClass = "action_not_performed"
	case DispatchPerformed:
		observed.Phase = PhaseActionPerformed
		observed.LastErrorClass = ""
	case DispatchUnknown:
		observed.Phase = PhaseActionUnknown
		observed.LastErrorClass = "action_dispatch_unknown"
		observed.RetryAt = l.engine.now().UTC().Add(l.engine.ambiguousCooldown).Format(time.RFC3339Nano)
	}
	saveErr := l.engine.save(context.Background(), &observed)
	l.record = observed
	var safeDispatchErr error
	if dispatchErr != nil {
		safeDispatchErr = fmt.Errorf("provider dispatcher failed with dispatch=%s", outcome.Dispatch)
	}
	return outcome, errors.Join(classificationErr, safeDispatchErr, saveErr)
}

func (l *Lease) Acknowledge(ctx context.Context, conversationID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return fmt.Errorf("conversation id is required")
	}
	if l.record.Phase != PhaseActionPerformed && l.record.Phase != PhaseActionUnknown {
		return fmt.Errorf("acknowledge from phase %q: expected action_performed or action_unknown", l.record.Phase)
	}
	if err := validateSafeValue("conversation_id", conversationID, 512); err != nil {
		return err
	}
	next := l.record
	next.ConversationID = conversationID
	if next.Dispatch == DispatchUnknown {
		next.Dispatch = DispatchPerformed
	}
	next.RetryAt = ""
	next.LastErrorClass = ""
	next.Phase = PhaseAcknowledged
	if err := l.engine.save(ctx, &next); err != nil {
		return err
	}
	l.record = next
	return nil
}

// ConfirmPostcondition persists a provider-observed same-target postcondition.
// It may refine an ambiguous dispatch to performed, but only after the caller
// supplies a non-empty privacy-safe proof label.
func (l *Lease) ConfirmPostcondition(ctx context.Context, proof string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	proof = strings.TrimSpace(proof)
	if proof == "" {
		return fmt.Errorf("postcondition proof is required")
	}
	if l.record.Phase != PhaseActionPerformed && l.record.Phase != PhaseActionUnknown {
		return fmt.Errorf(
			"confirm postcondition from phase %q: expected action_performed or action_unknown",
			l.record.Phase,
		)
	}
	if err := validateSafeValue("postcondition", proof, 512); err != nil {
		return err
	}
	next := l.record
	next.Dispatch = DispatchPerformed
	next.Phase = PhaseActionPerformed
	next.Postcondition = proof
	next.RetryAt = ""
	next.LastErrorClass = ""
	if err := l.engine.save(ctx, &next); err != nil {
		return err
	}
	l.record = next
	return nil
}

// BeginNextAction immutably archives a completed action slot and resets the
// same exact-target lease for one explicitly named subsequent action.
func (l *Lease) BeginNextAction(ctx context.Context, actionName string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	actionName = strings.TrimSpace(actionName)
	if l.closed {
		return fmt.Errorf("browserflow lease is closed")
	}
	if err := validateIdentity("action_name", actionName, 128); err != nil {
		return err
	}
	if l.record.ActionName == "" {
		return fmt.Errorf("current action_name is required before advancing")
	}
	if l.record.Phase != PhaseTerminal && l.record.Phase != PhaseIncomplete {
		return fmt.Errorf(
			"begin next action from phase %q: expected terminal or incomplete",
			l.record.Phase,
		)
	}
	next := l.record
	next.CompletedActions = append(
		append([]CompletedAction(nil), l.record.CompletedActions...),
		CompletedAction{
			Name:               l.record.ActionName,
			Dispatch:           l.record.Dispatch,
			ActionAttemptCount: l.record.ActionAttemptCount,
			RawInputCount:      l.record.RawInputCount,
			PendingPersisted:   l.record.PendingPersisted,
			CompletionPhase:    l.record.Phase,
			Postcondition:      l.record.Postcondition,
		},
	)
	next.Phase = PhaseAttached
	next.ActionName = actionName
	next.Dispatch = ""
	next.ActionAttemptCount = 0
	next.RawInputCount = 0
	next.PendingPersisted = false
	next.Postcondition = ""
	next.RetryAt = ""
	next.LastErrorClass = ""
	if err := l.engine.save(ctx, &next); err != nil {
		return err
	}
	l.record = next
	return nil
}

func (l *Lease) MarkTerminal(ctx context.Context) error {
	return l.markCompletion(ctx, PhaseTerminal)
}

func (l *Lease) MarkIncomplete(ctx context.Context) error {
	return l.markCompletion(ctx, PhaseIncomplete)
}

func (l *Lease) markCompletion(ctx context.Context, phase Phase) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch l.record.Phase {
	case PhasePrepared, PhaseActionPerformed, PhaseAcknowledged:
	case PhaseActionUnknown:
		if phase != PhaseIncomplete {
			return fmt.Errorf("mark %s from phase %q", phase, l.record.Phase)
		}
	default:
		return fmt.Errorf("mark %s from phase %q", phase, l.record.Phase)
	}
	next := l.record
	next.Phase = phase
	if err := l.engine.save(ctx, &next); err != nil {
		return err
	}
	l.record = next
	return nil
}

func (l *Lease) Close(ctx context.Context) (CleanupResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return CleanupResult{
			State:      CleanupClosed,
			TargetID:   l.record.TargetID,
			TargetGone: true,
		}, nil
	}

	inputReleaseErr := l.releaseInputLocked()
	l.record.Phase = PhaseCleanupPending
	l.record.Cleanup = CleanupPending
	persistPendingErr := l.engine.save(ctx, &l.record)
	cleanup := l.engine.closeExactTarget(l.session, l.record.TargetID)
	if cleanup.Error() == nil {
		l.record.Phase = PhaseClosed
		l.record.Cleanup = CleanupClosed
		l.record.LastErrorClass = ""
		l.closed = true
	} else {
		l.record.Phase = PhaseCleanupPending
		l.record.Cleanup = CleanupFailed
		l.record.LastErrorClass = "exact_target_cleanup_failed"
	}
	persistResultErr := l.engine.save(context.Background(), &l.record)
	return cleanup, errors.Join(
		inputReleaseErr,
		persistPendingErr,
		cleanup.Error(),
		persistResultErr,
	)
}

func (l *Lease) releaseInputLocked() error {
	if l.inputLock == nil {
		return nil
	}
	inputLock := l.inputLock
	l.inputLock = nil
	return inputLock.Release()
}

func (e *Engine) failWithoutTarget(ctx context.Context, record Record, class string, cause error) error {
	record.Phase = PhaseFailed
	record.LastErrorClass = class
	saveErr := e.save(ctx, &record)
	return errors.Join(cause, saveErr)
}

func (e *Engine) save(ctx context.Context, record *Record) error {
	record.UpdatedAt = e.timestamp()
	return e.journal.Save(ctx, *record)
}

func (e *Engine) timestamp() string {
	return e.now().UTC().Format(time.RFC3339Nano)
}

func (e *Engine) closeExactTarget(session *cdp.PageSession, targetID string) CleanupResult {
	result := CleanupResult{
		State:    CleanupFailed,
		TargetID: targetID,
	}
	deadline := time.Now().Add(e.closeTimeout)

	if session != nil {
		detachBudget := time.Until(deadline) / 4
		if detachBudget <= 0 {
			detachBudget = time.Nanosecond
		}
		detachCtx, cancelDetach := context.WithTimeout(
			context.Background(),
			detachBudget,
		)
		if err := session.Close(detachCtx); err != nil {
			result.DetachError = err.Error()
		}
		cancelDetach()
	}

	for attempt := 1; attempt <= 2; attempt++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			result.FailurePhase = "deadline"
			break
		}
		attemptBudget := remaining / time.Duration(3-attempt)
		if attemptBudget <= 0 {
			attemptBudget = remaining
		}
		result.CloseAttemptCount = attempt
		result.CloseError = ""
		result.PollError = ""
		result.FailurePhase = ""
		attemptCtx, cancelAttempt := context.WithTimeout(
			context.Background(),
			attemptBudget,
		)
		if err := cdp.CloseTargetWithClient(
			attemptCtx,
			e.client,
			targetID,
		); err != nil {
			result.CloseError = err.Error()
		} else {
			result.CloseSent = true
		}
		gone, observed, err := waitTargetGone(
			attemptCtx,
			e.client,
			targetID,
			e.closePollInterval,
		)
		cancelAttempt()
		result.TargetPollObserved = result.TargetPollObserved || observed
		result.TargetGone = gone
		if err != nil {
			result.PollError = err.Error()
		}
		if gone {
			result.State = CleanupClosed
			result.FailurePhase = ""
			result.CloseError = ""
			result.PollError = ""
			return result
		}
		switch {
		case result.CloseError != "" && result.PollError != "":
			result.FailurePhase = "close_and_poll"
		case result.CloseError != "":
			result.FailurePhase = "close"
		case result.PollError != "":
			result.FailurePhase = "poll"
		default:
			result.FailurePhase = "unsettled"
		}
	}
	return result
}

func waitTargetGone(
	ctx context.Context,
	client cdp.CommandClient,
	targetID string,
	poll time.Duration,
) (bool, bool, error) {
	observed := false
	for {
		targets, err := cdp.ListTargetsWithClient(ctx, client)
		if err != nil {
			return false, observed, err
		}
		observed = true
		found := false
		for _, target := range targets {
			if target.TargetID == targetID {
				found = true
				break
			}
		}
		if !found {
			return true, observed, nil
		}
		select {
		case <-ctx.Done():
			return false, observed, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func budgetEvidence(budget cdp.BrowserResourceBudget, allowOverBudget bool) *BudgetEvidence {
	return &BudgetEvidence{
		TabCount:          budget.TabCount,
		MaxTabs:           budget.MaxTabs,
		WindowCount:       budget.WindowCount,
		MaxWindows:        budget.MaxWindows,
		WindowCountKnown:  budget.WindowCountKnown,
		OverBudget:        budget.OverBudgetForNewPage(),
		OverridePermitted: allowOverBudget,
	}
}

func validateAcquireRequest(request AcquireRequest) error {
	if err := validateIdentity("run_id", request.RunID, 128); err != nil {
		return err
	}
	if err := validateIdentity("provider", request.Provider, 128); err != nil {
		return err
	}
	if err := validateIdentity("operation", request.Operation, 128); err != nil {
		return err
	}
	if strings.TrimSpace(request.ActionName) != "" {
		if err := validateIdentity("action_name", request.ActionName, 128); err != nil {
			return err
		}
	}
	if strings.TrimSpace(request.InitialURL) == "" {
		return fmt.Errorf("initial URL is required")
	}
	return nil
}

func validateDispatchOutcome(outcome DispatchOutcome) error {
	switch outcome.Dispatch {
	case DispatchNotPerformed:
		if outcome.RawInputAttempted {
			return fmt.Errorf("not_performed dispatch cannot report raw input")
		}
	case DispatchPerformed, DispatchUnknown:
		if !outcome.RawInputAttempted {
			return fmt.Errorf("%s dispatch must report a raw input attempt", outcome.Dispatch)
		}
	default:
		return fmt.Errorf("dispatcher returned invalid dispatch %q", outcome.Dispatch)
	}
	return nil
}
