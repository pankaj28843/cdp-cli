package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

type BrowserConfig struct {
	Client      cdp.CommandClient
	Engine      *browserflow.Engine
	Journal     browserflow.Journal
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
	initialURL string,
	readMode string,
	data any,
	callback ownedCallback,
) webagent.Result {
	return runOwnedAction(
		ctx,
		config,
		runID,
		operation,
		"",
		initialURL,
		readMode,
		data,
		callback,
	)
}

func runOwnedAction(
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
		callback == nil {
		return operationFailure(
			runID, config.BuildCommit, operation, webagent.StagePlanned, readMode,
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil,
			"gemini_browserflow_unavailable", "internal",
			"Gemini exact-target browser transaction is not configured", "",
			data, []string{"cdp workflow agent gemini doctor --json"},
		)
	}
	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID:      runID,
		Provider:   string(webagent.ProviderGemini),
		Operation:  string(operation),
		ActionName: actionName,
		InitialURL: initialURL,
	})
	if err != nil {
		target, cleanup, stage := reconcileAcquireFailure(config, runID)
		code := "gemini_browser_start_failed"
		errClass := "connection"
		message := "Gemini workflow could not acquire one exact headed target"
		var budget *browserflow.BudgetExceededError
		if errors.As(err, &budget) {
			code = "gemini_browser_resource_budget_exceeded"
			errClass = "resource"
			message = "Gemini workflow was blocked by the headed browser resource budget"
		}
		if cleanup.State == webagent.CleanupFailed {
			code = "gemini_exact_target_cleanup_failed"
			errClass = "cleanup"
			message = "Gemini workflow could not prove exact target cleanup"
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
		Required: true,
		State:    webagent.CleanupPending,
		TargetID: lease.TargetID(),
	}
	defer func() {
		cleanup, closeErr := lease.Close(context.Background())
		if closeErr != nil || cleanup.State != browserflow.CleanupClosed || !cleanup.TargetGone {
			target.Closed = false
			result.Evidence.Target = target
			result.Cleanup = webagent.CleanupEvidence{
				Required: true,
				State:    webagent.CleanupFailed,
				TargetID: lease.TargetID(),
			}
			result.Stage = webagent.StageCleanupPending
			result = replaceFailure(
				result,
				"gemini_exact_target_cleanup_failed",
				"cleanup",
				"Gemini workflow could not prove exact target cleanup",
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
		Required: true,
		State:    webagent.CleanupFailed,
		TargetID: record.TargetID,
	}, webagent.StageCleanupPending
}

func cleanupCommands(_ string, _ webagent.CleanupEvidence) []string {
	return nil
}
