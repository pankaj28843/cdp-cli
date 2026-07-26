package perplexity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

type EventClient interface {
	cdp.CommandClient
	ReadEvent(context.Context) (cdp.Event, error)
}

type BrowserConfig struct {
	Client      EventClient
	Engine      *browserflow.Engine
	Journal     browserflow.Journal
	Admission   *admission.Gate
	BuildCommit string
}

type ownedCallback func(
	*browserflow.Lease,
	*webagent.TargetEvidence,
	webagent.CleanupEvidence,
) webagent.Result

func runOwned(
	ctx context.Context,
	config BrowserConfig,
	runID string,
	operation webagent.Operation,
	actionName string,
	initialURL string,
	readMode string,
	data any,
	callback ownedCallback,
) (result webagent.Result) {
	if config.Client == nil ||
		config.Engine == nil ||
		config.Journal == nil ||
		config.Admission == nil ||
		callback == nil {
		return operationFailure(
			runID, config.BuildCommit, operation, webagent.StagePlanned, readMode,
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil,
			"perplexity_browserflow_unavailable", "internal",
			"Perplexity exact-target browser transaction is not configured", "",
			data, []string{"cdp workflow agent perplexity doctor --json"},
		)
	}
	admissionLease, err := config.Admission.Acquire(ctx, admission.Request{
		Provider:  string(webagent.ProviderPerplexity),
		Operation: string(operation),
		RunID:     runID,
	})
	if err != nil {
		code := "perplexity_admission_unavailable"
		errClass := "internal"
		message := "Perplexity provider admission state is unavailable"
		retryAt := ""
		nextCommands := []string{"cdp workflow agent perplexity doctor --json"}
		var blocked *admission.BlockedError
		if errors.As(err, &blocked) {
			code = "perplexity_admission_blocked"
			errClass = "admission"
			message = blocked.Error()
			if blocked.ResolutionNeeded {
				nextCommands = []string{"cdp workflow agent admission status perplexity --json"}
			} else {
				retryAt = blocked.RetryAt.UTC().Format(time.RFC3339Nano)
			}
		}
		return operationFailure(
			runID, config.BuildCommit, operation, webagent.StagePlanned, readMode,
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil, code, errClass, message, retryAt, data,
			nextCommands,
		)
	}
	defer func() {
		outcome := admission.OutcomeFailed
		switch {
		case result.OK && result.State == webagent.StateTerminal:
			outcome = admission.OutcomeTerminal
		case result.OK && result.State == webagent.StateIncomplete:
			outcome = admission.OutcomeIncomplete
		case result.OK:
			outcome = admission.OutcomeCompleted
		case result.Action != nil &&
			(result.Action.Dispatch == webagent.DispatchUnknown ||
				(result.Action.Dispatch == webagent.DispatchPerformed &&
					result.State != webagent.StateTerminal)):
			outcome = admission.OutcomeUnknown
		}
		var cooldown time.Time
		if result.Error != nil && result.Error.RetryAt != "" {
			cooldown, _ = time.Parse(time.RFC3339Nano, result.Error.RetryAt)
		}
		if err := admissionLease.Release(admission.Release{
			Outcome:       outcome,
			CooldownUntil: cooldown,
		}); err != nil {
			result = replaceFailure(
				result,
				"perplexity_admission_release_failed",
				"internal",
				"Perplexity provider admission outcome could not be persisted",
				[]string{"cdp workflow agent perplexity doctor --json"},
			)
		}
	}()

	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID:      runID,
		Provider:   string(webagent.ProviderPerplexity),
		Operation:  string(operation),
		ActionName: actionName,
		InitialURL: initialURL,
	})
	if err != nil {
		target, cleanup, stage := reconcileAcquireFailure(config, runID)
		code := "perplexity_browser_start_failed"
		errClass := "connection"
		message := "Perplexity workflow could not acquire one exact headed target"
		var budget *browserflow.BudgetExceededError
		if errors.As(err, &budget) {
			code = "perplexity_browser_resource_budget_exceeded"
			errClass = "resource"
			message = "Perplexity workflow was blocked by the headed browser resource budget"
		}
		if cleanup.State == webagent.CleanupFailed {
			code = "perplexity_exact_target_cleanup_failed"
			errClass = "cleanup"
			message = "Perplexity workflow could not prove exact target cleanup"
		}
		return operationFailure(
			runID, config.BuildCommit, operation, stage, readMode,
			target, cleanup, nil, nil, code, errClass, message, "",
			data, cleanupCommands(runID, cleanup),
		)
	}

	target := &webagent.TargetEvidence{
		TargetID:  lease.TargetID(),
		SessionID: lease.Session().SessionID,
		Owned:     true,
		Created:   true,
	}
	pending := webagent.CleanupEvidence{
		Required:        true,
		State:           webagent.CleanupPending,
		TargetID:        lease.TargetID(),
		RecoveryCommand: fmt.Sprintf("cdp workflow agent recovery close %s --json", runID),
	}
	defer func() {
		cleanup, closeErr := lease.Close(context.Background())
		if closeErr != nil || cleanup.State != browserflow.CleanupClosed || !cleanup.TargetGone {
			target.Closed = false
			result.Evidence.Target = target
			result.Cleanup = webagent.CleanupEvidence{
				Required:        true,
				State:           webagent.CleanupFailed,
				TargetID:        lease.TargetID(),
				RecoveryCommand: fmt.Sprintf("cdp workflow agent recovery close %s --json", runID),
			}
			result.Stage = webagent.StageCleanupPending
			result = replaceFailure(
				result,
				"perplexity_exact_target_cleanup_failed",
				"cleanup",
				"Perplexity workflow could not prove exact target cleanup",
				cleanupCommands(runID, result.Cleanup),
			)
			return
		}
		target.Closed = true
		result.Evidence.Target = target
		result.Cleanup = webagent.CleanupEvidence{
			Required:     true,
			State:        webagent.CleanupClosed,
			TargetID:     lease.TargetID(),
			TargetClosed: true,
			CloseProof:   "exact_target_absent_after_close",
		}
		result.Stage = webagent.StageClosed
	}()
	return callback(lease, target, pending)
}

func preparePage(
	ctx context.Context,
	client cdp.CommandClient,
	session *cdp.PageSession,
	rawURL string,
) error {
	for _, method := range []string{"Runtime.enable", "Page.enable", "Network.enable"} {
		if err := client.CallSession(ctx, session.SessionID, method, map[string]any{}, nil); err != nil {
			return err
		}
	}
	if err := cdp.ActivateTargetWithClient(ctx, client, session.TargetID); err != nil {
		return err
	}
	_, err := session.Navigate(ctx, rawURL)
	return err
}

func evaluateInto(
	ctx context.Context,
	session *cdp.PageSession,
	expression string,
	target any,
) error {
	evaluated, err := session.Evaluate(ctx, expression, true)
	if err != nil {
		return err
	}
	if evaluated.Exception != nil || len(evaluated.Object.Value) == 0 {
		return fmt.Errorf("exact-target evaluation failed")
	}
	if err := json.Unmarshal(evaluated.Object.Value, target); err != nil {
		return fmt.Errorf("decode exact-target evaluation")
	}
	return nil
}

func pollUntil(
	ctx context.Context,
	timeout time.Duration,
	interval time.Duration,
	observe func() (bool, error),
) (int, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	attempts := 0
	var lastErr error
	for {
		attempts++
		ready, err := observe()
		if err == nil && ready {
			return attempts, nil
		}
		if err != nil {
			lastErr = err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if lastErr == nil {
				lastErr = fmt.Errorf("observation deadline exhausted")
			}
			return attempts, lastErr
		}
		delay := interval
		if delay > remaining {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return attempts, ctx.Err()
		case <-timer.C:
		}
	}
}

func reconcileAcquireFailure(
	config BrowserConfig,
	runID string,
) (*webagent.TargetEvidence, webagent.CleanupEvidence, webagent.Stage) {
	recoveryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cleanup, recoverErr := config.Engine.Recover(recoveryCtx, runID)
	record, loadErr := config.Journal.Load(recoveryCtx, runID)
	if loadErr != nil || record.TargetID == "" {
		return nil, webagent.CleanupEvidence{
			State: webagent.CleanupNotRequired,
		}, webagent.StagePlanned
	}
	target := &webagent.TargetEvidence{
		TargetID:  record.TargetID,
		SessionID: record.SessionID,
		Owned:     true,
		Created:   true,
		Closed:    recoverErr == nil && cleanup.TargetGone,
	}
	if target.Closed {
		return target, webagent.CleanupEvidence{
			Required:     true,
			State:        webagent.CleanupClosed,
			TargetID:     record.TargetID,
			TargetClosed: true,
			CloseProof:   "exact_target_absent_after_recovery",
		}, webagent.StageClosed
	}
	return target, webagent.CleanupEvidence{
		Required:        true,
		State:           webagent.CleanupFailed,
		TargetID:        record.TargetID,
		RecoveryCommand: fmt.Sprintf("cdp workflow agent recovery close %s --json", runID),
	}, webagent.StageCleanupPending
}

func cleanupCommands(runID string, cleanup webagent.CleanupEvidence) []string {
	commands := []string{"cdp workflow agent perplexity doctor --json"}
	if cleanup.State == webagent.CleanupPending || cleanup.State == webagent.CleanupFailed {
		commands = append(
			commands,
			fmt.Sprintf("cdp workflow agent recovery inspect %s --json", runID),
			fmt.Sprintf("cdp workflow agent recovery close %s --json", runID),
		)
	}
	return commands
}
