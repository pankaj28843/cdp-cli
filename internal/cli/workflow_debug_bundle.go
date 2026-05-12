package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

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
						a.connectionRemediationCommands(),
					)
				}
				targetID, err = a.createWorkflowPageTarget(ctx, client, rawURL, "debug-bundle")
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
