package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowVerifyCommand() *cobra.Command {
	var wait time.Duration
	var limit int
	var outPath string
	cmd := &cobra.Command{
		Use:   "verify <url>",
		Short: "Open a URL and collect basic verification evidence",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 {
				return commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp workflow verify https://example.com --wait 2s --json"})
			}
			if limit < 0 {
				return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp workflow verify https://example.com --limit 50 --json"})
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
			createdID, err := a.createWorkflowPageTarget(ctx, client, "about:blank", "verify")
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

			includeSet := map[string]bool{"console": true, "network": true}
			collectorErrors := enablePageLoadCollectors(ctx, client, session.SessionID, includeSet)
			trigger := "observe"
			_, err = session.Navigate(ctx, rawURL)
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("navigation", err))
			} else {
				target.URL = rawURL
				trigger = "navigate"
			}

			requests, requestsTruncated, messages, messagesTruncated, err := collectPageLoadEvents(ctx, client, session.SessionID, wait, limit, includeSet)
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
			requests = failedRequests
			messages = errorMessages

			report := map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"requests": requests,
				"messages": messages,
				"workflow": map[string]any{
					"name":               "verify",
					"trigger":            trigger,
					"requested_url":      rawURL,
					"wait":               durationString(wait),
					"limit":              limit,
					"request_count":      len(requests),
					"message_count":      len(messages),
					"requests_truncated": requestsTruncated,
					"messages_truncated": messagesTruncated,
					"collector_errors":   collectorErrors,
					"partial":            len(collectorErrors) > 0,
					"next_commands": []string{
						fmt.Sprintf("cdp console --target %s --errors --wait 2s --json", target.TargetID),
						fmt.Sprintf("cdp network --target %s --failed --wait 2s --json", target.TargetID),
					},
				},
			}
			if strings.TrimSpace(outPath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal verify report: %v", err), ExitInternal, []string{"cdp workflow verify --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "workflow-verify", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "workflow-verify", "path": writtenPath, "bytes": len(b) + 1}}
			}

			human := fmt.Sprintf("verify\t%s\t%d failed requests\t%d errors", rawURL, len(requests), len(messages))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect evidence after navigation")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum number of events to return; use 0 for no limit")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the JSON verification report artifact")
	return cmd
}
