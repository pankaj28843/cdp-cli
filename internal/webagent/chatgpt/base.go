package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
		callback == nil {
		return operationFailure(
			runID, config.BuildCommit, operation, webagent.StagePlanned, readMode,
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"chatgpt_browserflow_unavailable", "internal",
			"ChatGPT exact-target browser transaction is not configured",
			data, []string{"cdp workflow agent chatgpt doctor --json"},
		)
	}
	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID:      runID,
		Provider:   string(webagent.ProviderChatGPT),
		Operation:  string(operation),
		ActionName: actionName,
		InitialURL: initialURL,
	})
	if err != nil {
		target, cleanup, stage := reconcileAcquireFailure(config, runID)
		code := "chatgpt_browser_start_failed"
		errClass := "connection"
		message := "ChatGPT workflow could not acquire one exact headed target"
		var budget *browserflow.BudgetExceededError
		if errors.As(err, &budget) {
			code = "chatgpt_browser_resource_budget_exceeded"
			errClass = "resource"
			message = "ChatGPT workflow was blocked by the headed browser resource budget"
		}
		if cleanup.State == webagent.CleanupFailed {
			code = "chatgpt_exact_target_cleanup_failed"
			errClass = "cleanup"
			message = "ChatGPT workflow could not prove exact target cleanup"
		}
		return operationFailure(
			runID, config.BuildCommit, operation, stage, readMode,
			target, cleanup, code, errClass, message, data,
			cleanupCommands(runID, cleanup),
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
		cleanup, _ := lease.Close(context.Background())
		if cleanup.State != browserflow.CleanupClosed || !cleanup.TargetGone {
			target.Closed = false
			result.Evidence.Target = target
			result.Cleanup = webagent.CleanupEvidence{
				Required:           true,
				State:              webagent.CleanupFailed,
				TargetID:           lease.TargetID(),
				CloseAttemptCount:  cleanup.CloseAttemptCount,
				CloseSent:          cleanup.CloseSent,
				TargetPollObserved: cleanup.TargetPollObserved,
				FailurePhase:       cleanup.FailurePhase,
			}
			result.Stage = webagent.StageCleanupPending
			result = replaceFailure(
				result,
				"chatgpt_exact_target_cleanup_failed",
				"cleanup",
				"ChatGPT workflow could not prove exact target cleanup",
				cleanupCommands(runID, result.Cleanup),
			)
			return
		}
		target.Closed = true
		result.Evidence.Target = target
		result.Cleanup = webagent.CleanupEvidence{
			Required:           true,
			State:              webagent.CleanupClosed,
			TargetID:           lease.TargetID(),
			TargetClosed:       true,
			CloseAttemptCount:  cleanup.CloseAttemptCount,
			CloseSent:          cleanup.CloseSent,
			TargetPollObserved: cleanup.TargetPollObserved,
			CloseProof:         "exact_target_absent_after_close",
		}
		result.Stage = webagent.StageClosed
	}()
	return callback(lease, target, pending)
}

func conversationAwaitCommand(conversationID string) string {
	return fmt.Sprintf(
		"cdp workflow agent chatgpt conversations await %s --wait 40m --timeout 40m30s --json",
		strings.TrimSpace(conversationID),
	)
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

func operationSuccess(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	stage webagent.Stage,
	readMode string,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	data any,
	nextCommands []string,
) webagent.Result {
	commands := make([]string, 0, len(nextCommands))
	commands = append(commands, nextCommands...)
	return webagent.Result{
		OK:            true,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderChatGPT,
		Operation:     operation,
		State:         webagent.StateReady,
		Stage:         stage,
		Data:          data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: browserModeForTarget(target),
			ReadMode:    readMode,
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: commands,
	}
}

func operationFailure(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	stage webagent.Stage,
	readMode string,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	code string,
	errClass string,
	message string,
	data any,
	nextCommands []string,
) webagent.Result {
	commands := make([]string, 0, len(nextCommands))
	commands = append(commands, nextCommands...)
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderChatGPT,
		Operation:     operation,
		State:         webagent.StateFailed,
		Stage:         stage,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  errClass,
			Message:   message,
			RetrySafe: true,
		},
		Data: data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: browserModeForTarget(target),
			ReadMode:    readMode,
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: commands,
	}
}

func replaceFailure(
	result webagent.Result,
	code string,
	errClass string,
	message string,
	nextCommands []string,
) webagent.Result {
	retrySafe := true
	retryAt := ""
	if result.Action != nil {
		retrySafe = result.Action.RetrySafe
	}
	if result.Error != nil {
		retryAt = result.Error.RetryAt
	}
	result.OK = false
	result.State = webagent.StateFailed
	result.Error = &webagent.OperationError{
		Code:      code,
		ErrClass:  errClass,
		Message:   message,
		RetrySafe: retrySafe,
		RetryAt:   retryAt,
	}
	result.NextCommands = make([]string, 0, len(nextCommands))
	result.NextCommands = append(result.NextCommands, nextCommands...)
	return result
}

func UnavailableOperation(
	buildCommit string,
	operation webagent.Operation,
	code string,
	errClass string,
	message string,
) webagent.Result {
	result := operationFailure(
		webagent.NewRunID(), buildCommit, operation,
		webagent.StagePlanned, "unavailable",
		nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		code, errClass, message,
		map[string]any{"schema_version": "chatgpt-unavailable/v1"},
		[]string{"cdp workflow agent chatgpt doctor --json"},
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func UnsupportedOperation(
	buildCommit string,
	operation webagent.Operation,
	code string,
	message string,
) webagent.Result {
	result := operationFailure(
		webagent.NewRunID(),
		buildCommit,
		operation,
		webagent.StagePlanned,
		"unavailable",
		nil,
		webagent.CleanupEvidence{
			State: webagent.CleanupNotRequired,
		},
		code,
		"capability",
		message,
		map[string]any{
			"schema_version": "chatgpt-unsupported/v1",
		},
		[]string{
			"cdp workflow agent chatgpt capabilities --json",
			"cdp workflow agent chatgpt capabilities refresh --json",
		},
	)
	result.State = webagent.StateUnsupported
	result.Evidence.BrowserMode = "none"
	return result
}

func reconcileAcquireFailure(
	config BrowserConfig,
	runID string,
) (*webagent.TargetEvidence, webagent.CleanupEvidence, webagent.Stage) {
	recoveryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	cleanup, _ := config.Engine.Recover(recoveryCtx, runID)
	cancel()
	loadCtx, cancelLoad := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelLoad()
	record, loadErr := config.Journal.Load(loadCtx, runID)
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
		Closed: cleanup.State == browserflow.CleanupClosed &&
			cleanup.TargetGone,
	}
	if target.Closed {
		return target, webagent.CleanupEvidence{
			Required:           true,
			State:              webagent.CleanupClosed,
			TargetID:           record.TargetID,
			TargetClosed:       true,
			CloseAttemptCount:  cleanup.CloseAttemptCount,
			CloseSent:          cleanup.CloseSent,
			TargetPollObserved: cleanup.TargetPollObserved,
			CloseProof:         "exact_target_absent_after_recovery",
		}, webagent.StageClosed
	}
	return target, webagent.CleanupEvidence{
		Required:           true,
		State:              webagent.CleanupFailed,
		TargetID:           record.TargetID,
		CloseAttemptCount:  cleanup.CloseAttemptCount,
		CloseSent:          cleanup.CloseSent,
		TargetPollObserved: cleanup.TargetPollObserved,
		FailurePhase:       cleanup.FailurePhase,
	}, webagent.StageCleanupPending
}

func cleanupCommands(_ string, _ webagent.CleanupEvidence) []string {
	return nil
}

func normalizedBuildCommit(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "unknown"
}

func browserModeForTarget(target *webagent.TargetEvidence) string {
	if target == nil {
		return "none"
	}
	return "headed"
}

func notPerformedAction() *webagent.ActionEvidence {
	return &webagent.ActionEvidence{
		Dispatch:      webagent.DispatchNotPerformed,
		AttemptCount:  0,
		RawInputCount: 0,
		RetrySafe:     true,
	}
}

func actionEvidence(record browserflow.Record) *webagent.ActionEvidence {
	dispatch := webagent.DispatchNotPerformed
	switch record.Dispatch {
	case browserflow.DispatchPerformed:
		dispatch = webagent.DispatchPerformed
	case browserflow.DispatchUnknown:
		dispatch = webagent.DispatchUnknown
	}
	return &webagent.ActionEvidence{
		Dispatch:         dispatch,
		AttemptCount:     record.ActionAttemptCount,
		RawInputCount:    record.RawInputCount,
		RetrySafe:        dispatch == webagent.DispatchNotPerformed,
		PendingPersisted: record.PendingPersisted,
	}
}
