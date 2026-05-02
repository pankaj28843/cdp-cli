package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Run high-level browser debugging workflows",
	}
	cmd.AddCommand(a.newWorkflowVerifyCommand())
	cmd.AddCommand(a.newWorkflowPerfCommand())
	cmd.AddCommand(a.newWorkflowA11yCommand())
	cmd.AddCommand(a.newWorkflowDebugBundleCommand())
	cmd.AddCommand(a.newWorkflowActionCaptureCommand())
	cmd.AddCommand(a.newWorkflowVisiblePostsCommand())
	cmd.AddCommand(a.newWorkflowHackerNewsCommand())
	cmd.AddCommand(a.newWorkflowConsoleErrorsCommand())
	cmd.AddCommand(a.newWorkflowNetworkFailuresCommand())
	cmd.AddCommand(a.newWorkflowPageLoadCommand())
	cmd.AddCommand(a.newWorkflowFeedsCommand())
	cmd.AddCommand(a.newWorkflowRenderedExtractCommand())
	cmd.AddCommand(a.newWorkflowWebResearchCommand())
	cmd.AddCommand(a.newWorkflowResponsiveAuditCommand())
	cmd.AddCommand(a.newWorkflowPerfSmokeCommand())
	cmd.AddCommand(a.newWorkflowMemorySmokeCommand())
	return cmd
}

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
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			closeOwned := true
			defer func() {
				if closeOwned {
					_ = closeClient(ctx)
				}
			}()

			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			createdID, err := a.createPageTarget(ctx, client, "about:blank")
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
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			closeOwned := true
			defer func() {
				if closeOwned {
					_ = closeClient(ctx)
				}
			}()

			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			createdID, err := a.createPageTarget(ctx, client, "about:blank")
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
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			closeOwned := true
			defer func() {
				if closeOwned {
					_ = closeClient(ctx)
				}
			}()

			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			createdID, err := a.createPageTarget(ctx, client, "about:blank")
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

type hackerNewsFrontpage struct {
	URL          string            `json:"url"`
	Title        string            `json:"title"`
	Count        int               `json:"count"`
	Stories      []hackerNewsStory `json:"stories"`
	Organization map[string]string `json:"organization"`
	Error        *snapshotError    `json:"error,omitempty"`
}

type hackerNewsStory struct {
	Rank        int    `json:"rank,omitempty"`
	ID          string `json:"id,omitempty"`
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	Site        string `json:"site,omitempty"`
	Score       int    `json:"score,omitempty"`
	User        string `json:"user,omitempty"`
	Age         string `json:"age,omitempty"`
	Comments    int    `json:"comments,omitempty"`
	CommentsURL string `json:"comments_url,omitempty"`
}

func (a *app) newWorkflowHackerNewsCommand() *cobra.Command {
	var limit int
	var wait time.Duration
	var keepOpen bool
	cmd := &cobra.Command{
		Use:   "hacker-news [url]",
		Short: "Open Hacker News and summarize visible stories",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()

			rawURL := "https://news.ycombinator.com/"
			if len(args) == 1 {
				rawURL = strings.TrimSpace(args[0])
			}
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, []string{"cdp daemon start --auto-connect --json", "cdp connection current --json"})
			}
			targetID, err := a.createPageTarget(ctx, client, rawURL)
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

			frontpage, err := waitForHackerNewsStories(ctx, session, limit, wait)
			if err != nil {
				return err
			}
			if len(frontpage.Stories) == 0 {
				return commandError("no_visible_posts", "check_failed", "no Hacker News story rows matched tr.athing", ExitCheckFailed, []string{"cdp workflow hacker-news --wait 30s --json", "cdp snapshot --selector '.titleline' --json"})
			}
			closed, closeErr := closeWorkflowPage()
			lines := hackerNewsStoryLines(frontpage.Stories)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":           true,
				"url":          rawURL,
				"target":       pageRow(cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}),
				"workflow":     map[string]any{"name": "hacker-news", "count": len(frontpage.Stories), "wait": durationString(wait), "limit": limit, "created_page": true, "closed": closed, "close_error": closeErr, "next_commands": []string{fmt.Sprintf("cdp page close --target %s --json", targetID)}},
				"organization": frontpage.Organization,
				"stories":      frontpage.Stories,
				"frontpage":    frontpage,
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 30, "maximum number of stories to return; use 0 for no limit")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "how long to wait for Hacker News story rows")
	cmd.Flags().BoolVar(&keepOpen, "keep-open", false, "leave the workflow-created page open for debugging")
	return cmd
}

func waitForHackerNewsStories(ctx context.Context, session *cdp.PageSession, limit int, wait time.Duration) (hackerNewsFrontpage, error) {
	if limit < 0 {
		return hackerNewsFrontpage{}, commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp workflow hacker-news --limit 30 --json"})
	}
	if wait < 0 {
		return hackerNewsFrontpage{}, commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp workflow hacker-news --wait 30s --json"})
	}
	deadline := time.Now().Add(wait)
	var last hackerNewsFrontpage
	for {
		frontpage, err := collectHackerNewsFrontpage(ctx, session, limit)
		if err != nil {
			return hackerNewsFrontpage{}, err
		}
		last = frontpage
		if len(frontpage.Stories) > 0 || wait == 0 || time.Now().After(deadline) {
			return last, nil
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return hackerNewsFrontpage{}, commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp workflow hacker-news --timeout 45s --json"})
		case <-timer.C:
		}
	}
}

func (a *app) newWorkflowDebugBundleCommand() *cobra.Command {
	var rawURL string
	var targetID string
	var urlContains string
	var titleContains string
	var outDir string
	var since time.Duration
	var screenshotFull bool
	var screenshotView bool
	var snapshotInteractiveOnly bool
	cmd := &cobra.Command{
		Use:   "debug-bundle",
		Short: "Collect a full debug bundle with events, snapshot, screenshot, and artifact references",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if since < 0 {
				return commandError("usage", "usage", "--since must be non-negative", ExitUsage, []string{"cdp workflow debug-bundle --url https://example.com --since 2s --json"})
			}
			if screenshotFull && screenshotView {
				return commandError(
					"usage",
					"usage",
					"--screenshot-full and --screenshot-view cannot be used together",
					ExitUsage,
					[]string{"cdp workflow debug-bundle --url https://example.com --screenshot-view --json"},
				)
			}
			if !screenshotFull && !screenshotView {
				screenshotView = true
			}

			fallback := since + 10*time.Second
			if fallback < 30*time.Second {
				fallback = 30 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			rawURL = strings.TrimSpace(rawURL)
			outDir = strings.TrimSpace(outDir)
			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			requestedURL := rawURL
			trigger := "attached"
			var session *cdp.PageSession
			var err error
			var client browserEventClient
			var closeClient func(context.Context) error
			var collectorErrors []map[string]string
			artifacts := []map[string]any{}
			artifactList := []map[string]any{}

			addArtifact := func(kind, path string, artifact map[string]any) {
				if strings.TrimSpace(path) == "" || artifact == nil {
					return
				}
				artifacts = append(artifacts, artifact)
				artifactList = append(artifactList, map[string]any{"type": kind, "path": path})
			}
			writeBundleArtifact := func(name string, payload any) (map[string]any, error) {
				if outDir == "" {
					return nil, nil
				}
				raw, err := json.MarshalIndent(payload, "", "  ")
				if err != nil {
					return nil, commandError("internal", "internal", fmt.Sprintf("marshal debug bundle artifact %s: %v", name, err), ExitInternal, []string{"cdp workflow debug-bundle --json"})
				}
				path := filepath.Join(outDir, "debug-bundle."+name+".json")
				writtenPath, err := writeArtifactFile(path, append(raw, '\n'))
				if err != nil {
					return nil, err
				}
				kind := "workflow-debug-bundle-" + name
				meta := map[string]any{
					"type":  kind,
					"path":  writtenPath,
					"bytes": len(raw) + 1,
				}
				addArtifact(kind, writtenPath, meta)
				return meta, nil
			}
			writeSnapshotArtifact := func(snapshot pageSnapshot) {
				if outDir == "" {
					return
				}
				_, err := writeBundleArtifact("snapshot", map[string]any{
					"url":      snapshot.URL,
					"title":    snapshot.Title,
					"selector": snapshot.Selector,
					"count":    snapshot.Count,
					"items":    snapshot.Items,
				})
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("artifact", err))
					return
				}
			}

			if rawURL != "" {
				client, closeClient, err = a.browserEventCDPClient(ctx)
				if err != nil {
					return commandError(
						"connection_not_configured",
						"connection",
						err.Error(),
						ExitConnection,
						[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
					)
				}
				targetID, err = a.createPageTarget(ctx, client, rawURL)
				if err != nil {
					closeClient(ctx)
					return err
				}
				target.TargetID = targetID
				session, err = cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
				if err != nil {
					closeClient(ctx)
					return commandError(
						"connection_failed",
						"connection",
						fmt.Sprintf("attach target %s: %v", target.TargetID, err),
						ExitConnection,
						[]string{"cdp pages --json", "cdp doctor --json"},
					)
				}
				defer session.Close(ctx)
				trigger = "navigate"
			} else {
				client, session, target, err = a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
				if err != nil {
					return err
				}
				defer session.Close(ctx)
				requestedURL = target.URL
			}

			collectorErrors = enablePageLoadCollectors(ctx, client, session.SessionID, map[string]bool{"console": true, "network": true})
			if rawURL != "" {
				if _, err := session.Navigate(ctx, target.URL); err != nil {
					collectorErrors = append(collectorErrors, collectorError("navigation", err))
				}
			}

			requests, requestsTruncated, messages, messagesTruncated, err := collectPageLoadEvents(ctx, client, session.SessionID, since, 100, map[string]bool{"console": true, "network": true})
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("events", err))
			}
			if len(messages) > 0 {
				for i := range messages {
					messages[i].ID = i
				}
			}

			var snapshot pageSnapshot
			snapshot, err = collectPageSnapshot(ctx, session, "body", 50, 1)
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("snapshot", err))
			}
			if outDir != "" {
				writeSnapshotArtifact(snapshot)
			}

			if outDir != "" {
				if snapshotInteractiveOnly {
					artifactList = append(artifactList, map[string]any{
						"type":    "snapshot-interactive-only",
						"path":    filepath.Join(outDir, "debug-bundle.snapshot_interactive_only"),
						"enabled": true,
						"note":    "reserved compatibility flag",
					})
				}
				if screenshotView || screenshotFull {
					shot, err := session.CaptureScreenshot(ctx, cdp.ScreenshotOptions{
						Format:   "png",
						FullPage: screenshotFull,
					})
					if err != nil {
						collectorErrors = append(collectorErrors, collectorError("screenshot", err))
					} else {
						shotPath := filepath.Join(outDir, fmt.Sprintf("debug-bundle.screenshot.%s", shot.Format))
						writtenPath, err := writeArtifactFile(shotPath, shot.Data)
						if err != nil {
							collectorErrors = append(collectorErrors, collectorError("artifact", err))
						} else {
							meta := map[string]any{
								"type":      "workflow-debug-bundle-screenshot",
								"path":      writtenPath,
								"bytes":     len(shot.Data),
								"format":    shot.Format,
								"full_page": screenshotFull,
							}
							addArtifact("workflow-debug-bundle-screenshot", writtenPath, meta)
						}
					}
				}

				if _, err := writeBundleArtifact("network", map[string]any{
					"requests": requests,
				}); err != nil {
					collectorErrors = append(collectorErrors, collectorError("artifact", err))
				}
				if _, err := writeBundleArtifact("console", map[string]any{
					"messages": messages,
				}); err != nil {
					collectorErrors = append(collectorErrors, collectorError("artifact", err))
				}
				if _, err := writeBundleArtifact("page-metadata", map[string]any{
					"url":              target.URL,
					"title":            snapshot.Title,
					"type":             target.Type,
					"id":               target.TargetID,
					"snapshot":         snapshot.Count,
					"requests":         len(requests),
					"messages":         len(messages),
					"trigger":          trigger,
					"since":            durationString(since),
					"partial":          len(collectorErrors) > 0,
					"interactive_only": snapshotInteractiveOnly,
				}); err != nil {
					collectorErrors = append(collectorErrors, collectorError("artifact", err))
				}
				if _, err := writeBundleArtifact("workflow", map[string]any{
					"name":      "debug-bundle",
					"requested": requestedURL,
					"trigger":   trigger,
				}); err != nil {
					collectorErrors = append(collectorErrors, collectorError("artifact", err))
				}
			}

			evidence := map[string]any{
				"requests":                  len(requests),
				"messages":                  len(messages),
				"snapshot_items":            snapshot.Count,
				"requests_truncated":        requestsTruncated,
				"messages_truncated":        messagesTruncated,
				"screenshot_requested":      screenshotFull || screenshotView,
				"snapshot_interactive_only": snapshotInteractiveOnly,
			}
			if target.Title == "" && snapshot.Title != "" {
				target.Title = snapshot.Title
			}
			if target.URL == "" && requestedURL != "" {
				target.URL = requestedURL
			}

			report := map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"requests": requests,
				"messages": messages,
				"snapshot": snapshot,
				"evidence": evidence,
				"workflow": map[string]any{
					"name":                "debug-bundle",
					"requested_url":       requestedURL,
					"trigger":             trigger,
					"since":               durationString(since),
					"request_count":       len(requests),
					"message_count":       len(messages),
					"snapshot_item_count": len(snapshot.Items),
					"requests_truncated":  requestsTruncated,
					"messages_truncated":  messagesTruncated,
					"collector_errors":    collectorErrors,
					"partial":             len(collectorErrors) > 0,
					"next_commands": []string{
						"cdp workflow verify " + requestedURL + " --json",
						"cdp console --target " + target.TargetID + " --errors --wait 5s --json",
						"cdp network --target " + target.TargetID + " --failed --wait 5s --json",
					},
					"screenshot_view": screenshotView,
					"screenshot_full": screenshotFull,
				},
			}
			if outDir != "" {
				bundleMeta, err := writeBundleArtifact("bundle", report)
				if err != nil {
					return err
				}
				if bundleMeta != nil {
					report["artifact"] = bundleMeta
				}
			}
			if len(artifacts) > 0 {
				report["artifacts"] = artifacts
				report["artifact_list"] = artifactList
			}
			return a.render(ctx, fmt.Sprintf("debug-bundle\t%s", target.TargetID), report)
		},
	}
	cmd.Flags().StringVar(&rawURL, "url", "", "open this URL before collecting the debug bundle")
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "optional directory for debug bundle artifacts")
	cmd.Flags().DurationVar(&since, "since", 5*time.Second, "how long to collect evidence after navigation/attach")
	cmd.Flags().BoolVar(&screenshotFull, "screenshot-full", false, "capture full-page screenshot in the debug bundle")
	cmd.Flags().BoolVar(&screenshotView, "screenshot-view", false, "capture viewport screenshot in the debug bundle")
	cmd.Flags().BoolVar(&snapshotInteractiveOnly, "snapshot-interactive-only", false, "reserved compatibility flag; snapshot still returns visible text items")
	return cmd
}

func collectHackerNewsFrontpage(ctx context.Context, session *cdp.PageSession, limit int) (hackerNewsFrontpage, error) {
	result, err := session.Evaluate(ctx, hackerNewsExpression(limit), true)
	if err != nil {
		return hackerNewsFrontpage{}, commandError("connection_failed", "connection", fmt.Sprintf("Hacker News workflow target %s: %v", session.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	if result.Exception != nil {
		return hackerNewsFrontpage{}, commandError("javascript_exception", "runtime", fmt.Sprintf("Hacker News workflow javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp workflow hacker-news --json", "cdp snapshot --selector body --json"})
	}
	var frontpage hackerNewsFrontpage
	if err := json.Unmarshal(result.Object.Value, &frontpage); err != nil {
		return hackerNewsFrontpage{}, commandError("invalid_workflow_result", "internal", fmt.Sprintf("decode Hacker News workflow result: %v", err), ExitInternal, []string{"cdp doctor --json", "cdp eval 'document.title' --json"})
	}
	if frontpage.Error != nil {
		return hackerNewsFrontpage{}, commandError("invalid_selector", "usage", fmt.Sprintf("Hacker News selector failed: %s", frontpage.Error.Message), ExitUsage, []string{"cdp workflow hacker-news --json", "cdp snapshot --selector '.athing' --json"})
	}
	return frontpage, nil
}

func hackerNewsExpression(limit int) string {
	return fmt.Sprintf(`(() => {
  "__cdp_cli_hn_frontpage__";
  const limit = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const parseNumber = (value) => {
    const match = normalize(value).match(/\d+/);
    return match ? Number(match[0]) : 0;
  };
  let rows;
  try {
    rows = Array.from(document.querySelectorAll("tr.athing"));
  } catch (error) {
    return { url: location.href, title: document.title, count: 0, stories: [], organization: {}, error: { name: error.name, message: error.message } };
  }
  const stories = [];
  for (const row of rows) {
    const titleLink = row.querySelector(".titleline > a") || row.querySelector(".storylink");
    if (!titleLink) continue;
    const metaRow = row.nextElementSibling;
    const subtext = metaRow && metaRow.querySelector(".subtext");
    const commentLink = Array.from(subtext ? subtext.querySelectorAll("a") : []).find((link) => /comment|discuss/i.test(link.textContent || ""));
    stories.push({
      rank: parseNumber(row.querySelector(".rank") && row.querySelector(".rank").textContent),
      id: row.getAttribute("id") || "",
      title: normalize(titleLink.textContent),
      url: titleLink.href || titleLink.getAttribute("href") || "",
      site: normalize(row.querySelector(".sitestr") && row.querySelector(".sitestr").textContent),
      score: parseNumber(subtext && subtext.querySelector(".score") && subtext.querySelector(".score").textContent),
      user: normalize(subtext && subtext.querySelector(".hnuser") && subtext.querySelector(".hnuser").textContent),
      age: normalize(subtext && subtext.querySelector(".age") && subtext.querySelector(".age").textContent),
      comments: parseNumber(commentLink && commentLink.textContent),
      comments_url: commentLink ? commentLink.href : ""
    });
    if (limit > 0 && stories.length >= limit) break;
  }
  return {
    url: location.href,
    title: document.title,
    count: stories.length,
    stories,
    organization: {
      page_kind: "table-based link aggregator front page",
      container_selector: "table.itemlist",
      story_row_selector: "tr.athing",
      metadata_row_selector: "tr.athing + tr .subtext",
      title_selector: ".titleline > a",
      rank_selector: ".rank",
      discussion_signal: "score, author, age, and comment links live in the metadata row after each story row"
    }
  };
})()`, limit)
}

func hackerNewsStoryLines(stories []hackerNewsStory) []string {
	lines := make([]string, 0, len(stories)+1)
	lines = append(lines, fmt.Sprintf("%-4s %7s %9s  %s", "rank", "points", "comments", "title"))
	for _, story := range stories {
		lines = append(lines, fmt.Sprintf(
			"#%-3d %7s %9s  %s",
			story.Rank,
			hackerNewsCountLabel(story.Score, "pt", "pts"),
			hackerNewsCountLabel(story.Comments, "comment", "comments"),
			story.Title,
		))
	}
	return lines
}

func hackerNewsCountLabel(count int, singular, plural string) string {
	if count == 0 {
		return "-"
	}
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}

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

type pageLoadStorageKeys struct {
	URL                string   `json:"url"`
	Origin             string   `json:"origin"`
	CookieKeys         []string `json:"cookie_keys"`
	LocalStorageKeys   []string `json:"local_storage_keys"`
	SessionStorageKeys []string `json:"session_storage_keys"`
}

type pageLoadMetric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

type workflowA11ySignals struct {
	ImagesWithoutAlt        int `json:"images_without_alt"`
	FormControlsWithoutName int `json:"form_controls_without_name"`
	HeadingSkips            int `json:"heading_skips"`
	FocusableWithoutLabel   int `json:"focusable_without_label"`
}

func (a *app) newWorkflowPageLoadCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var reload bool
	var ignoreCache bool
	var wait time.Duration
	var include string
	var limit int
	var outPath string
	cmd := &cobra.Command{
		Use:   "page-load [url]",
		Short: "Capture console, network, storage, and performance signals around a page load",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || limit < 0 {
				return commandError("usage", "usage", "--wait and --limit must be non-negative", ExitUsage, []string{"cdp workflow page-load https://example.com --wait 10s --json"})
			}
			fallback := wait + 10*time.Second
			if fallback < 30*time.Second {
				fallback = 30 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			rawURL := ""
			if len(args) == 1 {
				rawURL = strings.TrimSpace(args[0])
			}
			includeSet := pageLoadIncludeSet(include)
			client, closeClient, err := a.browserEventCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			closeOwned := true
			defer func() {
				if closeOwned {
					_ = closeClient(ctx)
				}
			}()

			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			if rawURL != "" && strings.TrimSpace(targetID) == "" && strings.TrimSpace(urlContains) == "" && strings.TrimSpace(titleContains) == "" {
				createdID, err := a.createPageTarget(ctx, client, "about:blank")
				if err != nil {
					return err
				}
				target.TargetID = createdID
			} else {
				selected, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
				if err != nil {
					return err
				}
				target = selected
			}

			closeOwned = false
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
			defer session.Close(ctx)

			collectorErrors := enablePageLoadCollectors(ctx, client, session.SessionID, includeSet)
			trigger := "observe"
			frameID := ""
			if rawURL != "" {
				frameID, err = session.Navigate(ctx, rawURL)
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("navigation", err))
				} else {
					target.URL = rawURL
					trigger = "navigate"
				}
			} else if reload {
				if err := session.Reload(ctx, ignoreCache); err != nil {
					collectorErrors = append(collectorErrors, collectorError("reload", err))
				} else {
					trigger = "reload"
				}
			}

			requests, requestsTruncated, messages, messagesTruncated, err := collectPageLoadEvents(ctx, client, session.SessionID, wait, limit, includeSet)
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("events", err))
			}
			var storage pageLoadStorageKeys
			if includeSet["storage"] {
				if storage, err = collectPageLoadStorageKeys(ctx, session); err != nil {
					collectorErrors = append(collectorErrors, collectorError("storage", err))
				}
			}
			var metrics []pageLoadMetric
			if includeSet["performance"] {
				if metrics, err = collectPerformanceMetrics(ctx, session); err != nil {
					collectorErrors = append(collectorErrors, collectorError("performance", err))
				}
			}
			var history cdp.NavigationHistory
			if includeSet["navigation"] {
				if history, err = session.NavigationHistory(ctx); err != nil {
					collectorErrors = append(collectorErrors, collectorError("navigation_history", err))
				}
			}

			report := map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"requests": requests,
				"messages": messages,
				"workflow": map[string]any{
					"name":               "page-load",
					"trigger":            trigger,
					"requested_url":      rawURL,
					"frame_id":           frameID,
					"wait":               durationString(wait),
					"include":            setKeys(includeSet),
					"limit":              limit,
					"request_count":      len(requests),
					"message_count":      len(messages),
					"requests_truncated": requestsTruncated,
					"messages_truncated": messagesTruncated,
					"collector_errors":   collectorErrors,
					"partial":            len(collectorErrors) > 0,
					"next_commands": []string{
						"cdp console --errors --wait 2s --json",
						"cdp network --failed --wait 2s --json",
						"cdp protocol exec Performance.getMetrics --target <target-id> --json",
					},
				},
			}
			if includeSet["storage"] {
				report["storage"] = storage
			}
			if includeSet["performance"] {
				report["performance"] = map[string]any{"metrics": metrics, "count": len(metrics)}
			}
			if includeSet["navigation"] {
				report["navigation"] = history
			}
			if strings.TrimSpace(outPath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal page-load report: %v", err), ExitInternal, []string{"cdp workflow page-load --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "page-load", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "page-load", "path": writtenPath}}
			}
			human := fmt.Sprintf("page-load\t%s\t%d requests\t%d messages", trigger, len(requests), len(messages))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().BoolVar(&reload, "reload", false, "reload the selected page after attaching collectors")
	cmd.Flags().BoolVar(&ignoreCache, "ignore-cache", false, "reload while bypassing cache")
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect events after navigation or reload")
	cmd.Flags().StringVar(&include, "include", "console,network,storage,performance,navigation", "comma-separated collectors: console,network,storage,performance,navigation")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum console messages and requests per collector; use 0 for no limit")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the JSON page-load report artifact")
	return cmd
}

func (a *app) newWorkflowFeedsCommand() *cobra.Command {
	var wait time.Duration
	var keepOpen bool
	cmd := &cobra.Command{Use: "feeds <url>", Short: "Discover RSS, Atom, and JSON Feed links", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if wait < 0 {
			return commandError("usage", "usage", "--wait-load must be non-negative", ExitUsage, []string{"cdp workflow feeds https://example.com --wait-load 10s --json"})
		}
		ctx, cancel := a.commandContextWithDefault(cmd, wait+10*time.Second)
		defer cancel()
		client, closeClient, err := a.browserCDPClient(ctx)
		if err != nil {
			return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, []string{"cdp daemon start --auto-connect --json", "cdp connection current --json"})
		}
		rawURL := strings.TrimSpace(args[0])
		targetID, err := a.createPageTarget(ctx, client, rawURL)
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
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp workflow feeds <url> --wait-load 10s --json"})
			case <-timer.C:
			}
		}
		var feeds []map[string]string
		if err := evaluateJSONValue(ctx, session, feedDiscoveryExpression(), "feeds", &feeds); err != nil {
			return err
		}
		closed, closeErr := closeWorkflowPage()
		workflow := map[string]any{"name": "feeds", "url": rawURL, "created_page": true, "closed": closed, "close_error": closeErr}
		if len(feeds) == 0 {
			return commandError("feed_not_found", "check_failed", "No RSS, Atom, or JSON Feed links were advertised by the page", ExitCheckFailed, []string{"cdp workflow feeds <url> --keep-open --json", "cdp eval 'Array.from(document.querySelectorAll(\"link[rel~=alternate]\"))' --json"})
		}
		return a.render(ctx, fmt.Sprintf("feeds\t%d", len(feeds)), map[string]any{"ok": true, "workflow": workflow, "page": map[string]any{"target_id": targetID, "final_url": rawURL}, "feeds": feeds})
	}}
	cmd.Flags().DurationVar(&wait, "wait-load", 5*time.Second, "how long to wait before discovering feed links")
	cmd.Flags().BoolVar(&keepOpen, "keep-open", false, "leave the workflow-created page open for debugging")
	return cmd
}

func (a *app) newWorkflowWebResearchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "web-research",
		Short: "Run batched browser-grounded web research workflows",
	}
	cmd.AddCommand(a.newWorkflowWebResearchSERPCommand())
	cmd.AddCommand(a.newWorkflowWebResearchExtractCommand())
	return cmd
}

func (a *app) newWorkflowWebResearchSERPCommand() *cobra.Command {
	var queryFile string
	var serp string
	var maxCandidates int
	var candidateOut string
	var outDir string
	var wait time.Duration
	var waitUntil string
	var parallel int
	var resultPages int
	var minVisibleWords int
	var minMarkdownWords int
	var minHTMLChars int
	cmd := &cobra.Command{
		Use:   "serp",
		Short: "Collect rendered SERP artifacts and deduped research candidates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || maxCandidates < 0 || parallel < 0 || resultPages < 0 || minVisibleWords < 0 || minMarkdownWords < 0 || minHTMLChars < 0 {
				return commandError("usage", "usage", "--wait, --max-candidates, --parallel, --result-pages, and quality thresholds must be non-negative", ExitUsage, []string{"cdp workflow web-research serp --query-file tmp/queries.txt --result-pages 3 --out-dir tmp/research --json"})
			}
			queries, err := readWebResearchQueries(queryFile)
			if err != nil {
				return err
			}
			if strings.TrimSpace(outDir) == "" {
				outDir = filepath.Join("tmp", "cdp-web-research")
			}
			if strings.TrimSpace(candidateOut) == "" {
				candidateOut = filepath.Join(outDir, "candidates.json")
			}
			if parallel == 0 || parallel > 3 {
				parallel = 3
			}
			if resultPages == 0 {
				resultPages = 1
			}
			if resultPages > 3 {
				resultPages = 3
			}
			serp = strings.TrimSpace(strings.ToLower(serp))
			if serp == "" {
				serp = "google"
			}
			if serp != "google" {
				return commandError("usage", "usage", "--serp must be google", ExitUsage, []string{"cdp workflow web-research serp --serp google --json"})
			}

			ctx := cmd.Context()
			queriesPath := filepath.Join(outDir, "queries.json")
			queriesPayload, err := json.MarshalIndent(map[string]any{"queries": queries, "count": len(queries), "serp": serp, "result_pages": resultPages}, "", "  ")
			if err != nil {
				return commandError("internal", "internal", fmt.Sprintf("marshal web research queries: %v", err), ExitInternal, []string{"cdp workflow web-research serp --json"})
			}
			queriesPath, err = writeArtifactFile(queriesPath, append(queriesPayload, '\n'))
			if err != nil {
				return err
			}

			type serpJob struct {
				QueryIndex int
				SerpPage   int
			}
			type serpResult struct {
				QueryIndex int
				SerpPage   int
				Query      webResearchQuery
				Result     renderedExtractResult
				Err        error
			}
			jobs := make(chan serpJob)
			resultCount := len(queries) * resultPages
			results := make(chan serpResult, resultCount)
			var wg sync.WaitGroup
			for i := 0; i < parallel; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for job := range jobs {
						query := queries[job.QueryIndex]
						queryURL := googleSearchURL(query.Text, query.TimeFilter, (job.SerpPage-1)*10)
						result, err := a.runRenderedExtractWorkflow(cmd, renderedExtractOptions{
							WorkflowName:       "web-research-serp",
							ArtifactTypePrefix: "web-research-serp",
							UsageCommand:       "cdp workflow web-research serp",
							RawURL:             queryURL,
							Selector:           "body",
							Wait:               wait,
							WaitUntil:          waitUntil,
							Formats:            "snapshot,text,html,markdown,links",
							OutDir:             filepath.Join(outDir, "serps", webResearchSlug(query.Text), fmt.Sprintf("page-%d", job.SerpPage)),
							Serp:               "google",
							Limit:              80,
							MinVisibleWords:    minVisibleWords,
							MinMarkdownWords:   minMarkdownWords,
							MinHTMLChars:       minHTMLChars,
						})
						results <- serpResult{QueryIndex: job.QueryIndex, SerpPage: job.SerpPage, Query: query, Result: result, Err: err}
					}
				}()
			}
			for i := range queries {
				for page := 1; page <= resultPages; page++ {
					jobs <- serpJob{QueryIndex: i, SerpPage: page}
				}
			}
			close(jobs)
			wg.Wait()
			close(results)

			serpResults := make([]serpResult, 0, resultCount)
			for result := range results {
				serpResults = append(serpResults, result)
			}
			sort.SliceStable(serpResults, func(i, j int) bool {
				if serpResults[i].QueryIndex == serpResults[j].QueryIndex {
					return serpResults[i].SerpPage < serpResults[j].SerpPage
				}
				return serpResults[i].QueryIndex < serpResults[j].QueryIndex
			})

			serpReports := make([]map[string]any, 0, resultCount)
			failures := make([]map[string]any, 0)
			candidates := make([]webResearchCandidate, 0)
			seen := map[string]bool{}
			for _, result := range serpResults {
				if result.Err != nil {
					failures = append(failures, map[string]any{"query": result.Query.Text, "serp_page": result.SerpPage, "error": result.Err.Error()})
					continue
				}
				serpReports = append(serpReports, map[string]any{"query": result.Query.Text, "time_filter": result.Query.TimeFilter, "serp_page": result.SerpPage, "report": result.Result.Report})
				for _, link := range result.Result.Links.Results {
					key := normalizeResearchURL(link.URL)
					if key == "" || seen[key] {
						continue
					}
					seen[key] = true
					globalRank := (result.SerpPage-1)*10 + link.Rank
					candidates = append(candidates, webResearchCandidate{Query: result.Query.Text, TimeFilter: result.Query.TimeFilter, SerpPage: result.SerpPage, RankOnPage: link.Rank, GlobalRank: globalRank, Rank: globalRank, Title: link.Title, Source: link.DisplayURL, Preview: link.Snippet, URL: link.URL, Type: link.Type})
					if maxCandidates > 0 && len(candidates) >= maxCandidates {
						break
					}
				}
				if maxCandidates > 0 && len(candidates) >= maxCandidates {
					break
				}
			}

			sort.SliceStable(candidates, func(i, j int) bool {
				if candidates[i].Query == candidates[j].Query {
					return candidates[i].Rank < candidates[j].Rank
				}
				return candidates[i].Query < candidates[j].Query
			})
			candidatePayload, err := json.MarshalIndent(candidates, "", "  ")
			if err != nil {
				return commandError("internal", "internal", fmt.Sprintf("marshal web research candidates: %v", err), ExitInternal, []string{"cdp workflow web-research serp --json"})
			}
			candidateOut, err = writeArtifactFile(candidateOut, append(candidatePayload, '\n'))
			if err != nil {
				return err
			}
			candidatesTSV := filepath.Join(outDir, "candidates.tsv")
			candidatesTSV, err = writeArtifactFile(candidatesTSV, []byte(webResearchCandidatesTSV(candidates)))
			if err != nil {
				return err
			}

			report := map[string]any{
				"ok":         len(failures) == 0,
				"queries":    queries,
				"serps":      serpReports,
				"candidates": candidates,
				"failures":   failures,
				"artifacts": map[string]string{
					"queries_json":    queriesPath,
					"candidates_json": candidateOut,
					"candidates_tsv":  candidatesTSV,
				},
				"workflow": map[string]any{
					"name":            "web-research-serp",
					"serp":            serp,
					"query_count":     len(queries),
					"candidate_count": len(candidates),
					"failure_count":   len(failures),
					"max_candidates":  maxCandidates,
					"result_pages":    resultPages,
					"parallel":        parallel,
					"out_dir":         outDir,
					"next_commands":   []string{"jq -r '.[].url' " + candidateOut + " > " + filepath.Join(outDir, "visit-urls.txt"), "cdp workflow web-research extract --url-file " + filepath.Join(outDir, "visit-urls.txt") + " --out-dir " + filepath.Join(outDir, "pages") + " --json"},
				},
			}
			return a.render(ctx, fmt.Sprintf("web-research-serp\t%d queries\t%d candidates", len(queries), len(candidates)), report)
		},
	}
	cmd.Flags().StringVar(&queryFile, "query-file", "", "newline-delimited Google queries to sample")
	cmd.Flags().StringVar(&serp, "serp", "google", "SERP extractor: google")
	cmd.Flags().IntVar(&maxCandidates, "max-candidates", 100, "maximum deduped candidates to emit; use 0 for no limit")
	cmd.Flags().StringVar(&candidateOut, "candidate-out", "", "path for deduped candidates JSON")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "directory for SERP artifacts and candidate files")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "maximum time to wait for each rendered SERP")
	cmd.Flags().StringVar(&waitUntil, "wait-until", "useful-content", "readiness gate: useful-content, load, or dom-stable")
	cmd.Flags().IntVar(&parallel, "parallel", 3, "maximum parallel SERP tabs, capped at 3")
	cmd.Flags().IntVar(&resultPages, "result-pages", 1, "Google result pages per query to sample, capped at 3")
	cmd.Flags().IntVar(&minVisibleWords, "min-visible-words", 5, "warning threshold for visible text word count")
	cmd.Flags().IntVar(&minMarkdownWords, "min-markdown-words", 5, "warning threshold for Markdown word count")
	cmd.Flags().IntVar(&minHTMLChars, "min-html-chars", 64, "warning threshold for extracted HTML character count")
	return cmd
}

func (a *app) newWorkflowWebResearchExtractCommand() *cobra.Command {
	var urlFile string
	var maxPages int
	var parallel int
	var outDir string
	var wait time.Duration
	var waitUntil string
	var selector string
	var minVisibleWords int
	var minMarkdownWords int
	var minHTMLChars int
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract selected research pages with bounded tab concurrency",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || maxPages < 0 || parallel < 0 || minVisibleWords < 0 || minMarkdownWords < 0 || minHTMLChars < 0 {
				return commandError("usage", "usage", "--wait, --max-pages, --parallel, and quality thresholds must be non-negative", ExitUsage, []string{"cdp workflow web-research extract --url-file tmp/urls.txt --json"})
			}
			urls, err := readWebResearchURLs(urlFile, maxPages)
			if err != nil {
				return err
			}
			if strings.TrimSpace(outDir) == "" {
				outDir = filepath.Join("tmp", "cdp-web-research", "pages")
			}
			if parallel == 0 || parallel > 10 {
				parallel = 10
			}

			ctx := cmd.Context()
			type pageResult struct {
				Index  int
				URL    string
				Result renderedExtractResult
				Err    error
			}
			jobs := make(chan int)
			results := make(chan pageResult, len(urls))
			var wg sync.WaitGroup
			for i := 0; i < parallel; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for idx := range jobs {
						rawURL := urls[idx]
						result, err := a.runRenderedExtractWorkflow(cmd, renderedExtractOptions{
							WorkflowName:       "web-research-extract",
							ArtifactTypePrefix: "web-research-extract",
							UsageCommand:       "cdp workflow web-research extract",
							RawURL:             rawURL,
							Selector:           selector,
							Wait:               wait,
							WaitUntil:          waitUntil,
							Formats:            "snapshot,text,html,markdown,links",
							OutDir:             filepath.Join(outDir, fmt.Sprintf("%03d-%s", idx+1, webResearchURLSlug(rawURL))),
							Serp:               "none",
							Limit:              80,
							MinVisibleWords:    minVisibleWords,
							MinMarkdownWords:   minMarkdownWords,
							MinHTMLChars:       minHTMLChars,
						})
						results <- pageResult{Index: idx, URL: rawURL, Result: result, Err: err}
					}
				}()
			}
			for i := range urls {
				jobs <- i
			}
			close(jobs)
			wg.Wait()
			close(results)

			pages := make([]map[string]any, 0, len(urls))
			qualities := make([]map[string]any, 0, len(urls))
			failures := make([]map[string]any, 0)
			warnings := make([]string, 0)
			for result := range results {
				if result.Err != nil {
					failures = append(failures, map[string]any{"url": result.URL, "error": result.Err.Error()})
					continue
				}
				pages = append(pages, map[string]any{"url": result.URL, "report": result.Result.Report})
				quality, _ := result.Result.Report["quality"].(map[string]any)
				artifacts, _ := result.Result.Report["artifacts"].(map[string]string)
				qualities = append(qualities, map[string]any{"url": result.URL, "quality": quality, "warnings": result.Result.Warnings, "artifacts": artifacts})
				for _, warning := range result.Result.Warnings {
					warnings = append(warnings, result.URL+": "+warning)
				}
			}
			sort.SliceStable(pages, func(i, j int) bool { return fmt.Sprint(pages[i]["url"]) < fmt.Sprint(pages[j]["url"]) })
			qualityPath := filepath.Join(outDir, "page-quality.json")
			qualityPayload, err := json.MarshalIndent(qualities, "", "  ")
			if err != nil {
				return commandError("internal", "internal", fmt.Sprintf("marshal page quality: %v", err), ExitInternal, []string{"cdp workflow web-research extract --json"})
			}
			qualityPath, err = writeArtifactFile(qualityPath, append(qualityPayload, '\n'))
			if err != nil {
				return err
			}
			failuresPath := filepath.Join(outDir, "failures.json")
			failuresPayload, err := json.MarshalIndent(failures, "", "  ")
			if err != nil {
				return commandError("internal", "internal", fmt.Sprintf("marshal extraction failures: %v", err), ExitInternal, []string{"cdp workflow web-research extract --json"})
			}
			failuresPath, err = writeArtifactFile(failuresPath, append(failuresPayload, '\n'))
			if err != nil {
				return err
			}

			report := map[string]any{
				"ok":        len(failures) == 0,
				"pages":     pages,
				"quality":   qualities,
				"warnings":  warnings,
				"failures":  failures,
				"artifacts": map[string]string{"page_quality_json": qualityPath, "failures_json": failuresPath},
				"workflow": map[string]any{
					"name":          "web-research-extract",
					"url_count":     len(urls),
					"page_count":    len(pages),
					"failure_count": len(failures),
					"warning_count": len(warnings),
					"max_pages":     maxPages,
					"parallel":      parallel,
					"out_dir":       outDir,
					"next_commands": []string{"jq '.[] | select((.warnings | length) > 0)' " + qualityPath, "jq -r '.[].url' " + failuresPath},
				},
			}
			return a.render(ctx, fmt.Sprintf("web-research-extract\t%d pages\t%d failures", len(pages), len(failures)), report)
		},
	}
	cmd.Flags().StringVar(&urlFile, "url-file", "", "newline-delimited URLs to extract")
	cmd.Flags().IntVar(&maxPages, "max-pages", 100, "maximum URLs to extract; use 0 for no limit")
	cmd.Flags().IntVar(&parallel, "parallel", 10, "maximum parallel page tabs, capped at 10")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "directory for page artifacts")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "maximum time to wait for each rendered page")
	cmd.Flags().StringVar(&waitUntil, "wait-until", "useful-content", "readiness gate: useful-content, load, or dom-stable")
	cmd.Flags().StringVar(&selector, "selector", "body", "CSS selector to extract rendered research content from")
	cmd.Flags().IntVar(&minVisibleWords, "min-visible-words", 5, "warning threshold for visible text word count")
	cmd.Flags().IntVar(&minMarkdownWords, "min-markdown-words", 5, "warning threshold for Markdown word count")
	cmd.Flags().IntVar(&minHTMLChars, "min-html-chars", 64, "warning threshold for extracted HTML character count")
	return cmd
}

func (a *app) newWorkflowResponsiveAuditCommand() *cobra.Command {
	return planned("responsive-audit <url>", "Audit a URL across desktop, tablet, and mobile viewport presets")
}

func (a *app) newWorkflowPerfSmokeCommand() *cobra.Command {
	return planned("perf-smoke <url>", "Run a lightweight performance smoke workflow")
}

func (a *app) newWorkflowMemorySmokeCommand() *cobra.Command {
	return planned("memory-smoke <url>", "Run a bounded memory smoke workflow with local artifacts")
}

func feedDiscoveryExpression() string {
	return `(() => Array.from(document.querySelectorAll('link[rel~="alternate"]')).map((link) => {
		const type = (link.getAttribute('type') || '').toLowerCase();
		const href = link.getAttribute('href') || '';
		const rel = link.getAttribute('rel') || '';
		const isFeed = type.includes('rss') || type.includes('atom') || type.includes('feed+json') || /rss|atom|feed/i.test(href);
		if (!isFeed) return null;
		return { type: type.includes('atom') ? 'atom' : (type.includes('json') ? 'json' : 'rss'), title: link.getAttribute('title') || '', href, url: new URL(href, document.baseURI).href, mime: type, source: 'link[rel~=alternate]', rel };
	}).filter(Boolean))()`
}

func pageLoadIncludeSet(include string) map[string]bool {
	set := parseCSVSet(include)
	if len(set) == 0 {
		set = parseCSVSet("console,network,storage,performance,navigation")
	}
	if set["all"] {
		set = parseCSVSet("console,network,storage,performance,navigation")
	}
	return set
}

func enablePageLoadCollectors(ctx context.Context, client browserEventClient, sessionID string, includeSet map[string]bool) []map[string]string {
	var collectorErrors []map[string]string
	enable := func(name, method string) {
		if err := client.CallSession(ctx, sessionID, method, map[string]any{}, nil); err != nil {
			collectorErrors = append(collectorErrors, collectorError(name, err))
		}
	}
	if includeSet["navigation"] {
		enable("navigation", "Page.enable")
	}
	if includeSet["console"] {
		enable("runtime", "Runtime.enable")
		enable("log", "Log.enable")
	}
	if includeSet["network"] {
		enable("network", "Network.enable")
	}
	if includeSet["performance"] {
		enable("performance", "Performance.enable")
	}
	return collectorErrors
}

func collectPageLoadEvents(ctx context.Context, client browserEventClient, sessionID string, wait time.Duration, limit int, includeSet map[string]bool) ([]networkRequest, bool, []consoleMessage, bool, error) {
	requestsByID := map[string]*networkRequest{}
	var requestOrder []string
	var messages []consoleMessage
	addEvent := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		if includeSet["network"] {
			req, ok := networkRequestFromEvent(event)
			if ok && req.ID != "" {
				existing, ok := requestsByID[req.ID]
				if !ok {
					copyReq := req
					requestsByID[req.ID] = &copyReq
					requestOrder = append(requestOrder, req.ID)
				} else {
					mergeNetworkRequest(existing, req)
				}
			}
		}
		if includeSet["console"] {
			msg, ok := consoleMessageFromEvent(event)
			if ok {
				msg.ID = len(messages)
				messages = append(messages, msg)
			}
		}
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return nil, false, nil, false, err
	}
	for _, event := range events {
		addEvent(event)
	}
	if wait > 0 {
		eventCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for {
			event, err := client.ReadEvent(eventCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded) {
					break
				}
				return nil, false, nil, false, err
			}
			addEvent(event)
		}
	}

	requests := make([]networkRequest, 0, len(requestOrder))
	for _, id := range requestOrder {
		requests = append(requests, *requestsByID[id])
	}
	requestsTruncated := false
	if limit > 0 && len(requests) > limit {
		requests = requests[:limit]
		requestsTruncated = true
	}
	messagesTruncated := false
	if limit > 0 && len(messages) > limit {
		messages = messages[:limit]
		messagesTruncated = true
		for i := range messages {
			messages[i].ID = i
		}
	}
	return requests, requestsTruncated, messages, messagesTruncated, nil
}

func collectorError(name string, err error) map[string]string {
	return map[string]string{"collector": name, "error": err.Error()}
}

func collectPageLoadStorageKeys(ctx context.Context, session *cdp.PageSession) (pageLoadStorageKeys, error) {
	result, err := session.Evaluate(ctx, pageLoadStorageExpression(), true)
	if err != nil {
		return pageLoadStorageKeys{}, err
	}
	if result.Exception != nil {
		return pageLoadStorageKeys{}, fmt.Errorf("javascript exception: %s", result.Exception.Text)
	}
	var storage pageLoadStorageKeys
	if err := json.Unmarshal(result.Object.Value, &storage); err != nil {
		return pageLoadStorageKeys{}, fmt.Errorf("decode storage keys: %w", err)
	}
	return storage, nil
}

func workflowA11yExpression() string {
	return `(() => {
  "__cdp_cli_workflow_a11y__";
  const byTag = (selector, all = false) => {
    try {
      const elements = Array.from(document.querySelectorAll(selector));
      return all ? elements : elements.filter(Boolean);
    } catch (error) {
      return [];
    }
  };
  const hasAccessibleName = (element) => {
    return (
      (element.getAttribute("aria-label") || "").trim() !== "" ||
      (element.getAttribute("aria-labelledby") || "").trim() !== "" ||
      ((element.getAttribute("title") || "").trim() !== "") ||
      ((element.getAttribute("alt") || "").trim() !== "") ||
      element.textContent.trim() !== "" ||
      (element.getAttribute("value") || "").trim() !== ""
    );
  };
  const images = byTag("img");
  const controls = byTag("button, input, textarea, select, option");
  const focusables = byTag("a, button, input, textarea, select, [tabindex]");
  let previousHeadingLevel = 0;
  let headingSkips = 0;
  byTag("h1, h2, h3, h4, h5, h6").forEach((heading) => {
    const level = Number(heading.tagName.substring(1));
    if (previousHeadingLevel > 0 && level - previousHeadingLevel > 1) {
      headingSkips += 1;
    }
    previousHeadingLevel = level;
  });
  return {
    images_without_alt: images.filter((image) => (image.getAttribute("alt") || "").trim() === "").length,
    form_controls_without_name: controls.filter((control) => {
      const hasName = (control.getAttribute("name") || "").trim() !== "";
      const hasId = (control.getAttribute("id") || "").trim() !== "";
      return !hasAccessibleName(control) && !hasName && !hasId;
    }).length,
    heading_skips: headingSkips,
    focusable_without_label: focusables.filter((element) => !hasAccessibleName(element)).length,
  };
})();`
}

func pageLoadStorageExpression() string {
	return `(() => {
  "__cdp_cli_page_load_storage__";
  const keys = (store) => {
    try { return Object.keys(store || {}).sort(); } catch (error) { return []; }
  };
  const cookieKeys = (() => {
    try {
      return (document.cookie || "")
        .split(";")
        .map((part) => part.trim().split("=")[0])
        .filter(Boolean)
        .sort();
    } catch (error) {
      return [];
    }
  })();
  return {
    url: location.href,
    origin: location.origin,
    cookie_keys: cookieKeys,
    local_storage_keys: keys(window.localStorage),
    session_storage_keys: keys(window.sessionStorage)
  };
})()`
}

func collectPerformanceMetrics(ctx context.Context, session *cdp.PageSession) ([]pageLoadMetric, error) {
	raw, err := session.Exec(ctx, "Performance.getMetrics", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var result struct {
		Metrics []pageLoadMetric `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode performance metrics: %w", err)
	}
	return result.Metrics, nil
}

type actionCaptureAction struct {
	Type     string `json:"type"`
	Selector string `json:"selector,omitempty"`
	Value    string `json:"value,omitempty"`
	Text     string `json:"text,omitempty"`
	Key      string `json:"key,omitempty"`
}

func (a *app) newWorkflowActionCaptureCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var include string
	var action string
	var actionJSON string
	var selector string
	var waitBefore time.Duration
	var waitAfter time.Duration
	var outPath string
	var beforeScreenshot string
	var afterScreenshot string
	var limit int
	var storageDiff bool
	cmd := &cobra.Command{
		Use:   "action-capture",
		Short: "Capture browser evidence around one declared page action",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if waitBefore < 0 || waitAfter < 0 || limit < 0 {
				return commandError("usage", "usage", "--wait-before, --wait-after, and --limit must be non-negative", ExitUsage, []string{"cdp workflow action-capture --action press:Enter --json"})
			}
			includeSet := parseCSVSet(include)
			if len(includeSet) == 0 || includeSet["all"] {
				includeSet = parseCSVSet("network,websocket,console,dom,text")
			}
			if storageDiff {
				includeSet["storage-diff"] = true
			}
			if invalid := invalidActionCaptureIncludes(includeSet); len(invalid) > 0 {
				return commandError("usage", "usage", fmt.Sprintf("unknown action-capture include %q", invalid[0]), ExitUsage, []string{"cdp workflow action-capture --include network,websocket,console,dom,text --json"})
			}
			parsedAction, err := parseActionCaptureAction(action, actionJSON, selector)
			if err != nil {
				return err
			}
			fallback := waitBefore + waitAfter + 15*time.Second
			if fallback < 20*time.Second {
				fallback = 20 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			collectorErrors := []map[string]string{}
			if includeSet["console"] {
				if err := client.CallSession(ctx, session.SessionID, "Runtime.enable", map[string]any{}, nil); err != nil {
					collectorErrors = append(collectorErrors, collectorError("runtime", err))
				}
				if err := client.CallSession(ctx, session.SessionID, "Log.enable", map[string]any{}, nil); err != nil {
					collectorErrors = append(collectorErrors, collectorError("log", err))
				}
			}
			if includeSet["network"] || includeSet["websocket"] {
				if err := client.CallSession(ctx, session.SessionID, "Network.enable", map[string]any{}, nil); err != nil {
					collectorErrors = append(collectorErrors, collectorError("network", err))
				}
			}
			preActionEvents, _ := client.DrainEvents(ctx)

			var beforeStorage storageSnapshot
			var beforeStorageErrors []map[string]string
			if includeSet["storage-diff"] {
				beforeStorage, beforeStorageErrors, err = collectStorageSnapshot(ctx, session, target, parseCSVSet("localStorage,sessionStorage,cookies,indexeddb,cache,serviceWorkers"))
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("storage_before", err))
				} else {
					collectorErrors = append(collectorErrors, beforeStorageErrors...)
				}
			}

			artifacts := []map[string]any{}
			beforeAt := time.Now().UTC().Format(time.RFC3339Nano)
			if strings.TrimSpace(beforeScreenshot) != "" {
				artifact, err := captureWorkflowScreenshot(ctx, session, beforeScreenshot, false, "before-screenshot")
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("before_screenshot", err))
				} else {
					artifacts = append(artifacts, artifact)
				}
			}
			if waitBefore > 0 {
				select {
				case <-time.After(waitBefore):
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			actionStarted := time.Now().UTC().Format(time.RFC3339Nano)
			actionResult, err := performActionCaptureAction(ctx, session, parsedAction)
			if err != nil {
				return err
			}
			actionFinished := time.Now().UTC().Format(time.RFC3339Nano)

			if waitAfter > 0 {
				select {
				case <-time.After(waitAfter):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			afterAt := time.Now().UTC().Format(time.RFC3339Nano)
			if strings.TrimSpace(afterScreenshot) != "" {
				artifact, err := captureWorkflowScreenshot(ctx, session, afterScreenshot, false, "after-screenshot")
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("after_screenshot", err))
				} else {
					artifacts = append(artifacts, artifact)
				}
			}

			report := map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"workflow": map[string]any{
					"name":               "action-capture",
					"include":            setKeys(includeSet),
					"wait_before":        durationString(waitBefore),
					"wait_after":         durationString(waitAfter),
					"before_at":          beforeAt,
					"action_started_at":  actionStarted,
					"action_finished_at": actionFinished,
					"after_at":           afterAt,
					"collector_errors":   collectorErrors,
				},
				"action": actionResult,
			}
			if len(artifacts) > 0 {
				report["artifacts"] = artifacts
			}
			if includeSet["network"] || includeSet["websocket"] || includeSet["console"] {
				requests, websockets, messages, err := collectActionCaptureEvents(ctx, client, session.SessionID, includeSet, limit, preActionEvents)
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("events", err))
				} else {
					if includeSet["network"] {
						report["requests"] = requests
					}
					if includeSet["websocket"] {
						report["websockets"] = websockets
					}
					if includeSet["console"] {
						report["messages"] = messages
					}
				}
			}
			if includeSet["text"] {
				var text textResult
				if err := evaluateJSONValue(ctx, session, textExpression("body", 1, 0), "action-capture text", &text); err != nil {
					collectorErrors = append(collectorErrors, collectorError("text", err))
				} else {
					report["text"] = text
				}
			}
			if includeSet["dom"] {
				var html htmlResult
				if err := evaluateJSONValue(ctx, session, htmlExpression("body", 1, 20000), "action-capture dom", &html); err != nil {
					collectorErrors = append(collectorErrors, collectorError("dom", err))
				} else {
					report["dom"] = html
				}
			}
			if includeSet["storage-diff"] && storageSnapshotHasData(beforeStorage) {
				afterStorage, afterStorageErrors, err := collectStorageSnapshot(ctx, session, target, parseCSVSet("localStorage,sessionStorage,cookies,indexeddb,cache,serviceWorkers"))
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("storage_after", err))
				} else {
					collectorErrors = append(collectorErrors, afterStorageErrors...)
					diff := diffStorageSnapshots(beforeStorage, afterStorage)
					report["storage_diff"] = map[string]any{"has_diff": storageDiffHasChanges(diff), "diff": diff}
				}
			}
			if strings.TrimSpace(outPath) != "" {
				report["local_artifact_warning"] = "action capture may include local page content, headers, tokens, and message data; keep this artifact local"
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal action capture report: %v", err), ExitInternal, []string{"cdp workflow action-capture --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "workflow-action-capture", "path": writtenPath, "bytes": len(b) + 1}
				artifacts = append(artifacts, map[string]any{"type": "workflow-action-capture", "path": writtenPath, "bytes": len(b) + 1})
				report["artifacts"] = artifacts
			}
			human := fmt.Sprintf("action-capture\t%s", parsedAction.Type)
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&include, "include", "network,websocket,console,dom,text", "comma-separated collectors: network,websocket,console,dom,text,all")
	cmd.Flags().StringVar(&action, "action", "", "action shorthand: click:<selector>, type:<text>, insert-text:<text>, or press:<key>")
	cmd.Flags().StringVar(&actionJSON, "action-json", "", "JSON action object with type, selector, text/value, or key")
	cmd.Flags().StringVar(&selector, "selector", "", "selector for click/type/insert-text or optional press focus target")
	cmd.Flags().DurationVar(&waitBefore, "wait-before", time.Second, "delay after arming collectors and before action")
	cmd.Flags().DurationVar(&waitAfter, "wait-after", 5*time.Second, "delay after action before collecting evidence")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the unified JSON artifact")
	cmd.Flags().StringVar(&beforeScreenshot, "before-screenshot", "", "optional before-action screenshot path")
	cmd.Flags().StringVar(&afterScreenshot, "after-screenshot", "", "optional after-action screenshot path")
	cmd.Flags().IntVar(&limit, "limit", 500, "maximum events per collector; use 0 for no limit")
	cmd.Flags().BoolVar(&storageDiff, "storage-diff", false, "include before/after storage diff evidence")
	return cmd
}

func invalidActionCaptureIncludes(includeSet map[string]bool) []string {
	valid := parseCSVSet("network,websocket,console,dom,text,storage-diff,all")
	invalid := []string{}
	for key := range includeSet {
		if !valid[key] {
			invalid = append(invalid, key)
		}
	}
	sort.Strings(invalid)
	return invalid
}

func parseActionCaptureAction(action, actionJSON, selector string) (actionCaptureAction, error) {
	if strings.TrimSpace(actionJSON) != "" {
		var parsed actionCaptureAction
		if err := json.Unmarshal([]byte(actionJSON), &parsed); err != nil {
			return actionCaptureAction{}, commandError("usage", "usage", fmt.Sprintf("decode --action-json: %v", err), ExitUsage, []string{`cdp workflow action-capture --action-json '{"type":"press","key":"Enter"}' --json`})
		}
		if parsed.Selector == "" {
			parsed.Selector = selector
		}
		return normalizeActionCaptureAction(parsed)
	}
	parts := strings.SplitN(strings.TrimSpace(action), ":", 2)
	if len(parts) != 2 {
		return actionCaptureAction{}, commandError("usage", "usage", "--action must use type:value syntax or --action-json must be provided", ExitUsage, []string{"cdp workflow action-capture --action press:Enter --selector body --json"})
	}
	parsed := actionCaptureAction{Type: parts[0], Selector: selector}
	switch strings.ToLower(strings.TrimSpace(parts[0])) {
	case "click":
		parsed.Selector = firstNonEmpty(selector, parts[1])
	case "type", "insert-text":
		parsed.Text = parts[1]
	case "press":
		parsed.Key = parts[1]
	}
	return normalizeActionCaptureAction(parsed)
}

func normalizeActionCaptureAction(action actionCaptureAction) (actionCaptureAction, error) {
	action.Type = strings.ToLower(strings.TrimSpace(action.Type))
	if action.Text == "" {
		action.Text = action.Value
	}
	switch action.Type {
	case "click":
		if strings.TrimSpace(action.Selector) == "" {
			return actionCaptureAction{}, commandError("usage", "usage", "click action requires --selector or click:<selector>", ExitUsage, []string{"cdp workflow action-capture --action click:button --json"})
		}
	case "type", "insert-text":
		if strings.TrimSpace(action.Selector) == "" || action.Text == "" {
			return actionCaptureAction{}, commandError("usage", "usage", action.Type+" action requires --selector and text", ExitUsage, []string{"cdp workflow action-capture --action type:hello --selector input --json"})
		}
	case "press":
		if strings.TrimSpace(action.Key) == "" {
			return actionCaptureAction{}, commandError("usage", "usage", "press action requires a key", ExitUsage, []string{"cdp workflow action-capture --action press:Enter --json"})
		}
	default:
		return actionCaptureAction{}, commandError("usage", "usage", "action type must be click, type, insert-text, or press", ExitUsage, []string{"cdp workflow action-capture --action press:Enter --json"})
	}
	return action, nil
}

func performActionCaptureAction(ctx context.Context, session *cdp.PageSession, action actionCaptureAction) (map[string]any, error) {
	switch action.Type {
	case "click":
		var result clickResult
		if err := evaluateJSONValue(ctx, session, clickExpression(action.Selector), "action-capture click", &result); err != nil {
			return nil, err
		}
		return map[string]any{"type": action.Type, "selector": action.Selector, "result": result}, nil
	case "type":
		result, err := performTextInput(ctx, session, action.Selector, action.Text, "auto")
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": action.Type, "selector": action.Selector, "text": action.Text, "result": result}, nil
	case "insert-text":
		result, err := performTextInput(ctx, session, action.Selector, action.Text, "insert-text")
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": action.Type, "selector": action.Selector, "text": action.Text, "result": result}, nil
	case "press":
		var result pressResult
		if err := evaluateJSONValue(ctx, session, pressExpression(action.Key, action.Selector), "action-capture press", &result); err != nil {
			return nil, err
		}
		return map[string]any{"type": action.Type, "selector": action.Selector, "key": action.Key, "result": result}, nil
	default:
		return nil, commandError("usage", "usage", "unsupported action type", ExitUsage, []string{"cdp workflow action-capture --action press:Enter --json"})
	}
}

func captureWorkflowScreenshot(ctx context.Context, session *cdp.PageSession, outPath string, fullPage bool, artifactType string) (map[string]any, error) {
	shot, err := session.CaptureScreenshot(ctx, cdp.ScreenshotOptions{Format: "png", FullPage: fullPage})
	if err != nil {
		return nil, err
	}
	writtenPath, err := writeArtifactFile(outPath, shot.Data)
	if err != nil {
		return nil, err
	}
	return map[string]any{"type": artifactType, "path": writtenPath, "bytes": len(shot.Data), "format": shot.Format, "full_page": fullPage}, nil
}

func collectActionCaptureEvents(ctx context.Context, client browserEventClient, sessionID string, includeSet map[string]bool, limit int, initialEvents []cdp.Event) ([]networkCaptureRecord, []networkCaptureRecord, []consoleMessage, error) {
	recordsByID := map[string]*networkCaptureRecord{}
	var order []string
	ensure := func(id string) *networkCaptureRecord {
		record, ok := recordsByID[id]
		if !ok {
			record = &networkCaptureRecord{ID: id}
			recordsByID[id] = record
			order = append(order, id)
		}
		return record
	}
	messages := []consoleMessage{}
	addEvent := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		switch event.Method {
		case "Network.requestWillBeSent":
			if includeSet["network"] {
				mergeCaptureRequestWillBeSent(event.Params, ensure, networkCaptureOptions{IncludeHeaders: true, IncludeInitiators: true})
			}
		case "Network.responseReceived":
			if includeSet["network"] {
				mergeCaptureResponseReceived(event.Params, ensure, networkCaptureOptions{IncludeHeaders: true, IncludeTiming: true})
			}
		case "Network.loadingFinished":
			if includeSet["network"] {
				mergeCaptureLoadingFinished(event.Params, ensure)
			}
		case "Network.loadingFailed":
			if includeSet["network"] {
				mergeCaptureLoadingFailed(event.Params, ensure)
			}
		case "Network.webSocketCreated":
			if includeSet["websocket"] {
				mergeCaptureWebSocketCreated(event.Params, ensure, networkCaptureOptions{IncludeInitiators: true})
			}
		case "Network.webSocketWillSendHandshakeRequest":
			if includeSet["websocket"] {
				mergeCaptureWebSocketWillSendHandshakeRequest(event.Params, ensure, networkCaptureOptions{IncludeHeaders: true})
			}
		case "Network.webSocketHandshakeResponseReceived":
			if includeSet["websocket"] {
				mergeCaptureWebSocketHandshakeResponseReceived(event.Params, ensure, networkCaptureOptions{IncludeHeaders: true})
			}
		case "Network.webSocketFrameSent":
			if includeSet["websocket"] {
				mergeCaptureWebSocketFrame(event.Params, ensure, networkCaptureOptions{WebSocketPayloads: true, WebSocketPayloadLimit: 64 * 1024}, "sent")
			}
		case "Network.webSocketFrameReceived":
			if includeSet["websocket"] {
				mergeCaptureWebSocketFrame(event.Params, ensure, networkCaptureOptions{WebSocketPayloads: true, WebSocketPayloadLimit: 64 * 1024}, "received")
			}
		case "Network.webSocketFrameError":
			if includeSet["websocket"] {
				mergeCaptureWebSocketFrameError(event.Params, ensure)
			}
		case "Network.webSocketClosed":
			if includeSet["websocket"] {
				mergeCaptureWebSocketClosed(event.Params, ensure)
			}
		case "Runtime.consoleAPICalled", "Log.entryAdded":
			if includeSet["console"] {
				if message, ok := consoleMessageFromEvent(event); ok {
					messages = append(messages, message)
				}
			}
		}
	}
	for _, event := range initialEvents {
		addEvent(event)
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, event := range events {
		addEvent(event)
	}
	requests := make([]networkCaptureRecord, 0, len(order))
	websockets := make([]networkCaptureRecord, 0, len(order))
	for _, id := range order {
		record := *recordsByID[id]
		if record.WebSocket != nil {
			websockets = append(websockets, record)
		} else {
			requests = append(requests, record)
		}
	}
	if limit > 0 {
		if len(requests) > limit {
			requests = requests[:limit]
		}
		if len(websockets) > limit {
			websockets = websockets[:limit]
		}
		if len(messages) > limit {
			messages = messages[:limit]
		}
	}
	for i := range messages {
		messages[i].ID = i
	}
	return requests, websockets, messages, nil
}

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
				return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, []string{"cdp daemon start --auto-connect --json", "cdp connection current --json"})
			}
			rawURL := strings.TrimSpace(args[0])
			targetID, err := a.createPageTarget(ctx, client, rawURL)
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
