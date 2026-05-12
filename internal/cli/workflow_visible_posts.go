package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowVisiblePostsCommand() *cobra.Command {
	var selector string
	var limit int
	var minChars int
	var wait time.Duration
	var keepOpen bool
	cmd := &cobra.Command{
		Use:   "visible-posts <url>",
		Short: "Open a feed page and list visible post text",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
			}
			rawURL := strings.TrimSpace(args[0])
			targetID, err := a.createWorkflowPageTarget(ctx, client, rawURL, "visible-posts")
			if err != nil {
				_ = closeClient(ctx)
				return err
			}
			closeWorkflowPage := func() (bool, string) {
				if keepOpen {
					return false, ""
				}
				if err := cdp.CloseTargetWithClient(ctx, client, targetID); err != nil {
					return false, err.Error()
				}
				return true, ""
			}
			session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
			if err != nil {
				_ = closeClient(ctx)
				return commandError("connection_failed", "connection", fmt.Sprintf("attach target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
			}
			defer session.Close(ctx)

			snapshot, err := waitForSnapshotItems(ctx, session, selector, limit, minChars, wait)
			if err != nil {
				return err
			}
			if len(snapshot.Items) == 0 {
				return commandError("no_visible_posts", "check_failed", fmt.Sprintf("no visible post elements matched selector %q", selector), ExitCheckFailed, []string{"cdp snapshot --selector article --json", "cdp workflow visible-posts <url> --selector article --wait 30s --json"})
			}
			closed, closeErr := closeWorkflowPage()
			lines := snapshotTextLines(snapshot.Items)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"url":      rawURL,
				"target":   pageRow(cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}),
				"selector": selector,
				"items":    snapshot.Items,
				"snapshot": snapshot,
				"workflow": map[string]any{"name": "visible-posts", "count": len(snapshot.Items), "wait": durationString(wait), "selector": selector, "limit": limit, "created_page": true, "closed": closed, "close_error": closeErr, "next_commands": []string{fmt.Sprintf("cdp page close --target %s --json", targetID)}},
			})
		},
	}
	cmd.Flags().StringVar(&selector, "selector", "article", "CSS selector for post containers")
	cmd.Flags().IntVar(&limit, "limit", 10, "maximum number of visible posts to return")
	cmd.Flags().IntVar(&minChars, "min-chars", 20, "minimum normalized text length per post")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "how long to wait for matching visible posts")
	cmd.Flags().BoolVar(&keepOpen, "keep-open", false, "leave the workflow-created page open for debugging")
	return cmd
}

func waitForSnapshotItems(ctx context.Context, session *cdp.PageSession, selector string, limit, minChars int, wait time.Duration) (pageSnapshot, error) {
	if wait < 0 {
		return pageSnapshot{}, commandError(
			"usage",
			"usage",
			"--wait must be non-negative",
			ExitUsage,
			[]string{"cdp workflow visible-posts <url> --wait 30s --json"},
		)
	}
	deadline := time.Now().Add(wait)
	var last pageSnapshot
	for {
		snapshot, err := collectPageSnapshot(ctx, session, selector, limit, minChars)
		if err != nil {
			return pageSnapshot{}, err
		}
		last = snapshot
		if len(snapshot.Items) > 0 || wait == 0 || time.Now().After(deadline) {
			return last, nil
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return pageSnapshot{}, commandError(
				"timeout",
				"timeout",
				ctx.Err().Error(),
				ExitTimeout,
				[]string{"cdp workflow visible-posts <url> --timeout 45s --json"},
			)
		case <-timer.C:
		}
	}
}
