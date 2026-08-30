package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowPerfCommand() *cobra.Command {
	var wait time.Duration
	var tracePath string
	var traceMaxBytes int
	var traceRedact string
	var targetIndex int
	cmd := &cobra.Command{
		Use:   "perf [url]",
		Short: "Collect post-load performance metrics",
		Long:  "Collect post-load performance metrics. URL-only runs own one disposable page and report bounded exact-target cleanup; --target-index runs retain the caller-owned page.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (retErr error) {
			if wait < 0 || traceMaxBytes <= 0 || traceMaxBytes > 64*1024*1024 {
				return commandError("usage", "usage", "--wait must be non-negative and --trace-max-bytes must be between 1 and 67108864", ExitUsage, []string{"cdp workflow perf 'https://example.com' --wait 5s --trace-max-bytes 16777216 --json"})
			}
			traceRedact = strings.ToLower(strings.TrimSpace(traceRedact))
			if traceRedact != "none" && traceRedact != "safe" && traceRedact != "headers" {
				return commandError("usage", "usage", "--redact must be none, safe, or headers", ExitUsage, []string{"cdp workflow perf 'https://example.com' --redact safe --json"})
			}
			rawURL, err := diagnosticWorkflowURL(cmd, args, targetIndex, "cdp workflow perf 'https://example.com' --wait 5s --json")
			if err != nil {
				return err
			}
			fallback := wait + 10*time.Second
			if fallback < 30*time.Second {
				fallback = 30 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			client, closeClient, err := a.browserEventCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					a.connectionRemediationCommands(),
				)
			}
			closeClientOnce := idempotentContextCloser(closeClient)
			defer func() { _ = closeClientOnce(context.Background()) }()
			target, createdPage, err := a.selectOrCreateDiagnosticPage(ctx, client, rawURL, "perf", targetIndex)
			if err != nil {
				return err
			}
			cleanupGuard := &renderedExtractCleanupGuard{client: client, targetID: target.TargetID, owned: createdPage}

			session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClientOnce)
			if err != nil {
				cleanup := cleanupGuard.cleanup()
				primary := commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("attach target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
				closeRenderedExtractSession(nil, closeClientOnce)
				return diagnosticWorkflowErrorWithCleanup("perf", primary, cleanup)
			}
			defer closeRenderedExtractSession(session, nil)
			defer func() {
				retErr = diagnosticWorkflowErrorWithCleanup("perf", retErr, cleanupGuard.cleanup())
			}()

			collectorErrors := enablePageLoadCollectors(ctx, client, session.SessionID, map[string]bool{"performance": true})
			traceStarted := false
			if err := startPerformanceTrace(ctx, client, session.SessionID); err != nil {
				collectorErrors = append(collectorErrors, collectorError("trace_start", err))
			} else {
				traceStarted = true
				defer func() {
					if traceStarted {
						stopPerformanceTraceBestEffort(client, session.SessionID)
					}
				}()
			}
			if rawURL != "" {
				_, err = session.Navigate(ctx, rawURL)
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("navigation", err))
				} else {
					target.URL = rawURL
				}
			}

			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return commandError(
						"timeout",
						"timeout",
						ctx.Err().Error(),
						ExitTimeout,
						[]string{"cdp workflow perf --wait 10s --json", "cdp workflow page-load --wait 10s --json"},
					)
				case <-timer.C:
				}
			}

			performance, err := collectPerformanceMetrics(ctx, session)
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("performance", err))
			}

			trace := performanceTraceResult{Insights: map[string]any{
				"lcp":               unavailableInsight("trace capture did not start"),
				"cls":               unavailableInsight("trace capture did not start"),
				"long_tasks":        unavailableInsight("trace capture did not start"),
				"blocking_requests": unavailableInsight("trace capture did not start"),
			}}
			if traceStarted {
				trace, err = finishPerformanceTrace(ctx, client, session.SessionID, tracePath, traceRedact, traceMaxBytes)
				traceStarted = false
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("trace", err))
				}
			}

			report := map[string]any{
				"ok":          true,
				"target":      pageRow(target),
				"cleanup":     cleanupGuard.cleanup(),
				"performance": map[string]any{"metrics": performance, "count": len(performance)},
				"insights":    trace.Insights,
				"trace": map[string]any{
					"stream":          trace.Stream,
					"artifact_safety": trace.Safety,
					"max_bytes":       traceMaxBytes,
				},
				"workflow": map[string]any{
					"name":             "perf",
					"created_page":     createdPage,
					"requested_url":    rawURL,
					"wait":             durationString(wait),
					"metric_count":     len(performance),
					"collector_errors": collectorErrors,
					"partial":          len(collectorErrors) > 0,
					"next_commands": []string{
						fmt.Sprintf("cdp protocol exec Performance.getMetrics --target %s --json", target.TargetID),
						diagnosticWorkflowTargetCommand("page-load", rawURL, targetIndex, target.TargetID) + " --wait 10s",
					},
				},
			}
			addWorkflowTargetIndex(report, targetIndex)
			cleanup := cleanupGuard.cleanup()
			if cleanup.Error != "" {
				return diagnosticWorkflowErrorWithCleanup("perf", nil, cleanup)
			}
			if trace.Artifact != nil {
				report["artifact"] = trace.Artifact
				report["artifacts"] = []map[string]any{trace.Artifact}
			}

			displayURL := rawURL
			if displayURL == "" {
				displayURL = target.URL
			}
			human := fmt.Sprintf("perf\t%s\t%d metrics", displayURL, len(performance))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect evidence before sampling metrics")
	cmd.Flags().StringVar(&tracePath, "trace", "", "optional path for the streamed JSON performance trace artifact")
	cmd.Flags().IntVar(&traceMaxBytes, "trace-max-bytes", 16*1024*1024, "positive maximum trace artifact bytes (up to 64 MiB)")
	cmd.Flags().StringVar(&traceRedact, "redact", "safe", "trace artifact redaction preset: safe, headers, or none")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "use the 1-based existing page index; workers do not consume indexes")
	return cmd
}

func (a *app) newWorkflowA11yCommand() *cobra.Command {
	var wait time.Duration
	var limit int
	var targetIndex int
	cmd := &cobra.Command{
		Use:   "a11y [url]",
		Short: "Run a focused accessibility workflow",
		Long:  "Run a focused accessibility workflow. URL-only runs own one disposable page and report bounded exact-target cleanup; --target-index runs retain the caller-owned page.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (retErr error) {
			if wait < 0 {
				return commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp workflow a11y 'https://example.com' --wait 5s --json"})
			}
			if limit < 0 {
				return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp workflow a11y 'https://example.com' --limit 100 --json"})
			}
			rawURL, err := diagnosticWorkflowURL(cmd, args, targetIndex, "cdp workflow a11y 'https://example.com' --wait 5s --json")
			if err != nil {
				return err
			}
			fallback := wait + 10*time.Second
			if fallback < 30*time.Second {
				fallback = 30 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			client, closeClient, err := a.browserEventCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					a.connectionRemediationCommands(),
				)
			}
			closeClientOnce := idempotentContextCloser(closeClient)
			defer func() { _ = closeClientOnce(context.Background()) }()
			target, createdPage, err := a.selectOrCreateDiagnosticPage(ctx, client, rawURL, "a11y", targetIndex)
			if err != nil {
				return err
			}
			cleanupGuard := &renderedExtractCleanupGuard{client: client, targetID: target.TargetID, owned: createdPage}

			session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClientOnce)
			if err != nil {
				cleanup := cleanupGuard.cleanup()
				primary := commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("attach target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
				closeRenderedExtractSession(nil, closeClientOnce)
				return diagnosticWorkflowErrorWithCleanup("a11y", primary, cleanup)
			}
			defer closeRenderedExtractSession(session, nil)
			defer func() {
				retErr = diagnosticWorkflowErrorWithCleanup("a11y", retErr, cleanupGuard.cleanup())
			}()

			collectorErrors := enablePageLoadCollectors(ctx, client, session.SessionID, map[string]bool{"console": true, "network": true})
			if rawURL != "" {
				if _, err = session.Navigate(ctx, rawURL); err != nil {
					collectorErrors = append(collectorErrors, collectorError("navigation", err))
				} else {
					target.URL = rawURL
				}
			}

			requests, requestsTruncated, messages, messagesTruncated, err := collectPageLoadEvents(ctx, client, session.SessionID, wait, limit, map[string]bool{"console": true, "network": true})
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("events", err))
			}
			failedRequests := make([]networkRequest, 0, len(requests))
			for _, request := range requests {
				if requestFailed(request) {
					failedRequests = append(failedRequests, request)
				}
			}
			errorMessages := make([]consoleMessage, 0, len(messages))
			for _, message := range messages {
				if keepConsoleMessage(message, true, nil) {
					errorMessages = append(errorMessages, message)
				}
			}
			for i := range errorMessages {
				errorMessages[i].ID = i
			}

			signalResult, err := session.Evaluate(ctx, workflowA11yExpression(), true)
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("signals", err))
			}
			var a11ySignals workflowA11ySignals
			if signalResult.Exception != nil {
				collectorErrors = append(collectorErrors, collectorError("signals", fmt.Errorf("javascript exception: %s", signalResult.Exception.Text)))
			} else if len(signalResult.Object.Value) > 0 {
				if err := json.Unmarshal(signalResult.Object.Value, &a11ySignals); err != nil {
					collectorErrors = append(collectorErrors, collectorError("signals", fmt.Errorf("decode accessibility signals: %w", err)))
				}
			}
			issueCount := a11ySignals.ImagesWithoutAlt + a11ySignals.FormControlsWithoutName + a11ySignals.HeadingSkips + a11ySignals.FocusableWithoutLabel

			report := map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"cleanup":  cleanupGuard.cleanup(),
				"requests": failedRequests,
				"messages": errorMessages,
				"a11y": map[string]any{
					"images_without_alt":         a11ySignals.ImagesWithoutAlt,
					"form_controls_without_name": a11ySignals.FormControlsWithoutName,
					"heading_skips":              a11ySignals.HeadingSkips,
					"focusable_without_label":    a11ySignals.FocusableWithoutLabel,
					"next_commands":              []string{diagnosticWorkflowTargetCommand("page-load", rawURL, targetIndex, target.TargetID) + " --wait 10s", diagnosticWorkflowTargetCommand("verify", rawURL, targetIndex, target.TargetID) + " --wait 5s"},
				},
				"workflow": map[string]any{
					"name":               "a11y",
					"created_page":       createdPage,
					"requested_url":      rawURL,
					"wait":               durationString(wait),
					"issue_count":        issueCount,
					"requests_count":     len(failedRequests),
					"message_count":      len(errorMessages),
					"requests_truncated": requestsTruncated,
					"messages_truncated": messagesTruncated,
					"collector_errors":   collectorErrors,
					"partial":            len(collectorErrors) > 0,
				},
			}
			addWorkflowTargetIndex(report, targetIndex)
			cleanup := cleanupGuard.cleanup()
			if cleanup.Error != "" {
				return diagnosticWorkflowErrorWithCleanup("a11y", nil, cleanup)
			}

			displayURL := rawURL
			if displayURL == "" {
				displayURL = target.URL
			}
			human := fmt.Sprintf("a11y\t%s\t%d potential issues", displayURL, issueCount)
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect evidence before sampling signals")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum number of events per collector; use 0 for no limit")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "use the 1-based existing page index; workers do not consume indexes")
	return cmd
}
