package cli

import (
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
	cmd := &cobra.Command{
		Use:   "perf <url>",
		Short: "Collect post-load performance metrics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 {
				return commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp workflow perf https://example.com --wait 5s --json"})
			}
			fallback := wait + 10*time.Second
			if fallback < 30*time.Second {
				fallback = 30 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			rawURL := strings.TrimSpace(args[0])
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
			closeOwned := true
			defer func() {
				if closeOwned {
					_ = closeClient(ctx)
				}
			}()

			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			createdID, err := a.createWorkflowPageTarget(ctx, client, "about:blank", "perf")
			if err != nil {
				return err
			}
			target.TargetID = createdID

			session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
			if err != nil {
				closeOwned = true
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("attach target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			closeOwned = false
			defer session.Close(ctx)

			collectorErrors := enablePageLoadCollectors(ctx, client, session.SessionID, map[string]bool{"performance": true})
			_, err = session.Navigate(ctx, rawURL)
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("navigation", err))
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

			report := map[string]any{
				"ok":          true,
				"target":      pageRow(target),
				"performance": map[string]any{"metrics": performance, "count": len(performance)},
				"workflow": map[string]any{
					"name":             "perf",
					"requested_url":    rawURL,
					"wait":             durationString(wait),
					"metric_count":     len(performance),
					"collector_errors": collectorErrors,
					"partial":          len(collectorErrors) > 0,
					"next_commands": []string{
						fmt.Sprintf("cdp protocol exec Performance.getMetrics --target %s --json", target.TargetID),
						"cdp workflow page-load " + rawURL + " --wait 10s --json",
					},
				},
			}
			if strings.TrimSpace(tracePath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal perf report: %v", err), ExitInternal, []string{"cdp workflow perf --json"})
				}
				writtenPath, err := writeArtifactFile(tracePath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "workflow-perf", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "workflow-perf", "path": writtenPath, "bytes": len(b) + 1}}
			}

			human := fmt.Sprintf("perf\t%s\t%d metrics", rawURL, len(performance))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect evidence before sampling metrics")
	cmd.Flags().StringVar(&tracePath, "trace", "", "optional path for the JSON performance trace artifact")
	return cmd
}

func (a *app) newWorkflowA11yCommand() *cobra.Command {
	var wait time.Duration
	var limit int
	cmd := &cobra.Command{
		Use:   "a11y <url>",
		Short: "Run a focused accessibility workflow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 {
				return commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp workflow a11y https://example.com --wait 5s --json"})
			}
			if limit < 0 {
				return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp workflow a11y https://example.com --limit 100 --json"})
			}
			fallback := wait + 10*time.Second
			if fallback < 30*time.Second {
				fallback = 30 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			rawURL := strings.TrimSpace(args[0])
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
			closeOwned := true
			defer func() {
				if closeOwned {
					_ = closeClient(ctx)
				}
			}()

			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			createdID, err := a.createWorkflowPageTarget(ctx, client, "about:blank", "a11y")
			if err != nil {
				return err
			}
			target.TargetID = createdID

			session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
			if err != nil {
				closeOwned = true
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("attach target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			closeOwned = false
			defer session.Close(ctx)

			collectorErrors := enablePageLoadCollectors(ctx, client, session.SessionID, map[string]bool{"console": true, "network": true})
			if _, err = session.Navigate(ctx, rawURL); err != nil {
				collectorErrors = append(collectorErrors, collectorError("navigation", err))
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
				"requests": failedRequests,
				"messages": errorMessages,
				"a11y": map[string]any{
					"images_without_alt":         a11ySignals.ImagesWithoutAlt,
					"form_controls_without_name": a11ySignals.FormControlsWithoutName,
					"heading_skips":              a11ySignals.HeadingSkips,
					"focusable_without_label":    a11ySignals.FocusableWithoutLabel,
					"next_commands":              []string{"cdp workflow page-load " + rawURL + " --wait 10s --json", "cdp workflow verify " + rawURL + " --wait 5s --json"},
				},
				"workflow": map[string]any{
					"name":               "a11y",
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

			human := fmt.Sprintf("a11y\t%s\t%d potential issues", rawURL, issueCount)
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect evidence before sampling signals")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum number of events per collector; use 0 for no limit")
	return cmd
}
