package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const (
	retryPolicyNone      = "none"
	retryPolicyTransient = "transient"
)

type commandRetryOptions struct {
	Policy      string
	MaxAttempts int
	Backoff     time.Duration
}

type commandRetryResult struct {
	Human  string
	Data   map[string]any
	Target *cdp.TargetInfo
}

type commandRetryReport struct {
	Policy             string                `json:"retry_policy"`
	AttemptCount       int                   `json:"attempt_count"`
	MaxAttempts        int                   `json:"max_attempts"`
	ElapsedMS          int64                 `json:"elapsed_ms"`
	LastError          string                `json:"last_error,omitempty"`
	LastObservedTarget map[string]any        `json:"last_observed_target,omitempty"`
	Attempts           []commandRetryAttempt `json:"attempts"`
}

type commandRetryAttempt struct {
	Attempt int            `json:"attempt"`
	OK      bool           `json:"ok"`
	Elapsed int64          `json:"elapsed_ms"`
	Code    string         `json:"code,omitempty"`
	Class   string         `json:"class,omitempty"`
	Error   string         `json:"error,omitempty"`
	Retry   bool           `json:"retry,omitempty"`
	Target  map[string]any `json:"target,omitempty"`
}

func addCommandRetryFlags(cmd *cobra.Command, opts *commandRetryOptions) {
	opts.Policy = retryPolicyNone
	opts.MaxAttempts = 3
	opts.Backoff = 200 * time.Millisecond
	cmd.Flags().StringVar(&opts.Policy, "retry", opts.Policy, "retry policy for transient daemon/target failures: none or transient")
	cmd.Flags().IntVar(&opts.MaxAttempts, "max-attempts", opts.MaxAttempts, "maximum attempts when --retry transient is enabled")
}

func validateCommandRetryOptions(opts commandRetryOptions) error {
	switch strings.ToLower(strings.TrimSpace(opts.Policy)) {
	case "", retryPolicyNone:
		return nil
	case retryPolicyTransient:
		if opts.MaxAttempts <= 0 {
			return commandError("invalid_argument", "usage", "--max-attempts must be positive", ExitUsage, []string{"cdp eval 'document.title' --retry transient --max-attempts 3 --json"})
		}
		return nil
	default:
		return commandError("invalid_argument", "usage", "--retry must be one of: none, transient", ExitUsage, []string{"cdp eval 'document.title' --retry transient --max-attempts 3 --json"})
	}
}

func commandRetryEnabled(opts commandRetryOptions) bool {
	return strings.EqualFold(strings.TrimSpace(opts.Policy), retryPolicyTransient)
}

func runCommandWithRetry(ctx context.Context, opts commandRetryOptions, run func(context.Context) (commandRetryResult, error)) (commandRetryResult, *commandRetryReport, error) {
	if err := validateCommandRetryOptions(opts); err != nil {
		return commandRetryResult{}, nil, err
	}
	if !commandRetryEnabled(opts) {
		result, err := run(ctx)
		return result, nil, err
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	backoff := opts.Backoff
	if backoff <= 0 {
		backoff = 200 * time.Millisecond
	}
	report := &commandRetryReport{
		Policy:      retryPolicyTransient,
		MaxAttempts: maxAttempts,
		Attempts:    []commandRetryAttempt{},
	}
	start := time.Now()
	var lastErr error
	var lastResult commandRetryResult
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptStart := time.Now()
		result, err := run(ctx)
		lastResult = result
		attemptReport := commandRetryAttempt{
			Attempt: attempt,
			OK:      err == nil,
			Elapsed: time.Since(attemptStart).Milliseconds(),
		}
		if result.Target != nil {
			attemptReport.Target = pageRow(*result.Target)
			report.LastObservedTarget = attemptReport.Target
		}
		if err == nil {
			report.Attempts = append(report.Attempts, attemptReport)
			report.AttemptCount = len(report.Attempts)
			report.ElapsedMS = time.Since(start).Milliseconds()
			return result, report, nil
		}
		lastErr = err
		code, class := commandErrorCodeClass(err)
		attemptReport.Code = code
		attemptReport.Class = class
		attemptReport.Error = err.Error()
		report.LastError = err.Error()
		retry := retryableTransientCommandError(ctx, err) && attempt < maxAttempts
		attemptReport.Retry = retry
		report.Attempts = append(report.Attempts, attemptReport)
		report.AttemptCount = len(report.Attempts)
		report.ElapsedMS = time.Since(start).Milliseconds()
		if !retry {
			break
		}
		if err := sleepForRetryBackoff(ctx, backoff, attempt); err != nil {
			break
		}
	}
	report.ElapsedMS = time.Since(start).Milliseconds()
	if lastErr == nil {
		lastErr = fmt.Errorf("retry attempts exhausted")
	}
	return lastResult, report, commandRetryError(lastErr, report)
}

func attachCommandRetryReport(data map[string]any, report *commandRetryReport) {
	if data == nil || report == nil {
		return
	}
	data["attempts"] = report.Attempts
	data["attempt_count"] = report.AttemptCount
	data["max_attempts"] = report.MaxAttempts
	data["retry_policy"] = report.Policy
	data["elapsed_ms"] = report.ElapsedMS
	if report.LastError != "" {
		data["last_error"] = report.LastError
	}
	if report.LastObservedTarget != nil {
		data["last_observed_target"] = report.LastObservedTarget
	}
}

func commandRetryError(err error, report *commandRetryReport) error {
	data := map[string]any{}
	attachCommandRetryReport(data, report)
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		if cmdErr.Data != nil {
			data["error_data"] = cmdErr.Data
		}
		return commandErrorWithData(cmdErr.Code, cmdErr.Class, cmdErr.Message, cmdErr.ExitCode, cmdErr.RemediationCommands, data)
	}
	return commandErrorWithData("connection_failed", "connection", err.Error(), ExitConnection, []string{"cdp pages --json", "cdp daemon health --json"}, data)
}

func commandErrorCodeClass(err error) (string, string) {
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.Code, cmdErr.Class
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled", "canceled"
	}
	return "", ""
}

func retryableTransientCommandError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if commandRetrySafetyBlocked(message) {
		return false
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		switch cmdErr.Code {
		case "target_not_found":
			return true
		case "connection_failed", "timeout":
			return transientRetryMessage(message)
		default:
			return false
		}
	}
	return transientRetryMessage(message)
}

func commandRetrySafetyBlocked(message string) bool {
	for _, needle := range []string{
		"permission",
		"login",
		"sign in",
		"payment",
		"personal data",
		"captcha",
		"unusual traffic",
		"blocked page",
		"blocked state",
	} {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

func transientRetryMessage(message string) bool {
	for _, needle := range []string{
		"failed to read json message",
		"failed to get reader",
		"use of closed network connection",
		"closed network connection",
		"connection reset",
		"broken pipe",
		"i/o timeout",
		"target not found",
		"no target",
		"cannot find target",
		"target closed",
		"target detached",
		"execution context was destroyed",
		"cannot find context with specified id",
		"inspected target navigated or closed",
		"context canceled",
	} {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

func sleepForRetryBackoff(ctx context.Context, base time.Duration, attempt int) error {
	if attempt <= 0 {
		attempt = 1
	}
	delay := time.Duration(attempt) * base
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}
