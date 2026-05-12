package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func (a *app) newWorkflowConsoleErrorsCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var wait time.Duration
	var limit int
	cmd := &cobra.Command{
		Use:   "console-errors",
		Short: "Summarize console errors and warnings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if wait < 0 || limit < 0 {
				return commandError("usage", "usage", "--wait and --limit must be non-negative", ExitUsage, []string{"cdp workflow console-errors --wait 2s --json"})
			}
			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			messages, truncated, err := collectConsoleMessages(ctx, client, session.SessionID, wait, limit, true, nil)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture console errors target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			lines := consoleMessageLines(messages)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"messages": messages,
				"workflow": map[string]any{
					"name":      "console-errors",
					"count":     len(messages),
					"wait":      durationString(wait),
					"limit":     limit,
					"truncated": truncated,
					"next_commands": []string{
						"cdp console --errors --wait 2s --json",
						"cdp screenshot --out tmp/page.png --json",
					},
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&wait, "wait", time.Second, "how long to collect console/log events")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of messages to return; use 0 for no limit")
	return cmd
}

func (a *app) newWorkflowNetworkFailuresCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var wait time.Duration
	var limit int
	cmd := &cobra.Command{
		Use:   "network-failures",
		Short: "Summarize failed and HTTP error network requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if wait < 0 || limit < 0 {
				return commandError("usage", "usage", "--wait and --limit must be non-negative", ExitUsage, []string{"cdp workflow network-failures --wait 2s --json"})
			}
			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			requests, truncated, err := collectNetworkRequests(ctx, client, session.SessionID, wait, limit, true)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture network failures target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			lines := networkRequestLines(requests)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"requests": requests,
				"workflow": map[string]any{
					"name":      "network-failures",
					"count":     len(requests),
					"wait":      durationString(wait),
					"limit":     limit,
					"truncated": truncated,
					"next_commands": []string{
						"cdp network --failed --wait 2s --json",
						"cdp workflow console-errors --wait 2s --json",
					},
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&wait, "wait", time.Second, "how long to collect network events")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of requests to return; use 0 for no limit")
	return cmd
}
