package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowDebugBundleCommand() *cobra.Command {
	var rawURL string
	var targetID string
	var urlContains string
	var titleContains string
	var targetIndex int
	var outDir string
	var since time.Duration
	var screenshotFull bool
	var screenshotView bool
	var snapshotInteractiveOnly bool
	var redact string
	var inlinePayloads bool
	var runID string
	var taskID string
	var stageName string
	var keepOpen bool
	var reload bool
	var ignoreCache bool
	cmd := &cobra.Command{
		Use:   "debug-bundle",
		Short: "Collect a full debug bundle with events, snapshot, screenshot, and artifact references",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			if targetIndex > 0 && strings.TrimSpace(rawURL) != "" {
				return commandError("invalid_target_selector", "usage", "--target-index cannot be combined with workflow-created --url", ExitUsage, []string{"cdp workflow debug-bundle --target-index 1 --json", "cdp workflow debug-bundle --url https://example.com --json"})
			}
			if since < 0 {
				return commandError("usage", "usage", "--since must be non-negative", ExitUsage, []string{"cdp workflow debug-bundle --url 'https://example.com' --since 2s --json"})
			}
			if screenshotFull && screenshotView {
				return commandError(
					"usage",
					"usage",
					"--screenshot-full and --screenshot-view cannot be used together",
					ExitUsage,
					[]string{"cdp workflow debug-bundle --url 'https://example.com' --screenshot-view --json"},
				)
			}
			redact = artifacts.NormalizeMode(redact)
			if redact != artifacts.ModeSafe && redact != artifacts.ModeNone {
				return commandError("usage", "usage", "--redact must be safe or none", ExitUsage, []string{"cdp workflow debug-bundle --redact safe --json"})
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
			if rawURL == "" && !reload && ignoreCache {
				return commandError("usage", "usage", "--reload=false cannot be combined with --ignore-cache=true for an existing target", ExitUsage, []string{"cdp workflow debug-bundle --target <target-id> --reload=false --ignore-cache=false --json"})
			}
			outDir = strings.TrimSpace(outDir)
			runID = strings.TrimSpace(runID)
			taskID = strings.TrimSpace(taskID)
			stageName = strings.TrimSpace(stageName)
			if stageName == "" {
				stageName = "debug-bundle"
			}
			started := time.Now()
			layout := debugBundleLayout{Root: outDir}
			if outDir != "" {
				layout.Manifest = filepath.Clean(filepath.Join(outDir, "debug-bundle.bundle.json"))
				layout.CommandLog = filepath.Clean(filepath.Join(outDir, "debug-bundle.command-log.jsonl"))
				layout.StageLog = filepath.Clean(filepath.Join(outDir, "debug-bundle.stage-log.json"))
			}
			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			requestedURL := rawURL
			trigger := "observe"
			reloaded := false
			cachePolicy := "normal_http_cache"
			var session *cdp.PageSession
			var err error
			var client browserEventClient
			var closeClient func(context.Context) error
			var collectorErrors []map[string]string
			artifactRecords := []debugBundleArtifactRecord{}
			commands := []debugBundleCommandRecord{}
			stages := []debugBundleStageRecord{}

			addArtifact := func(artifact debugBundleArtifactRecord) {
				if strings.TrimSpace(artifact.Path) == "" || strings.TrimSpace(artifact.Type) == "" {
					return
				}
				artifactRecords = append(artifactRecords, artifact)
			}
			writeBundleArtifact := func(name, content string, safety artifacts.SafetyMetadata, payload any) (debugBundleArtifactRecord, error) {
				if outDir == "" {
					return debugBundleArtifactRecord{}, nil
				}
				raw, err := json.MarshalIndent(payload, "", "  ")
				if err != nil {
					return debugBundleArtifactRecord{}, commandError("internal", "internal", fmt.Sprintf("marshal debug bundle artifact %s: %v", name, err), ExitInternal, []string{"cdp workflow debug-bundle --json"})
				}
				path := filepath.Join(outDir, "debug-bundle."+name+".json")
				writtenPath, err := writeArtifactFile(path, append(raw, '\n'))
				if err != nil {
					return debugBundleArtifactRecord{}, err
				}
				kind := "workflow-debug-bundle-" + name
				meta := debugBundleArtifactRecord{
					Type:    kind,
					Path:    writtenPath,
					Bytes:   len(raw) + 1,
					Content: content,
					Safety:  safety,
				}
				addArtifact(meta)
				return meta, nil
			}
			writeSnapshotArtifact := func(snapshot pageSnapshot) {
				if outDir == "" {
					return
				}
				snapshotRedactor := artifacts.NewRedactor(redact)
				snapshotPayload := debugBundleRedactedSnapshot(snapshot, snapshotRedactor)
				snapshotSafety := debugBundleArtifactSafety(snapshotRedactor, true, debugBundleSnapshotWarning)
				_, err := writeBundleArtifact("snapshot", "snapshot", snapshotSafety, map[string]any{
					"url":      snapshotPayload.URL,
					"title":    snapshot.Title,
					"selector": snapshot.Selector,
					"count":    snapshot.Count,
					"items":    snapshotPayload.Items,
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
				targetID, err = a.createWorkflowPageTargetWithKeepOpen(ctx, client, "about:blank", "debug-bundle", keepOpen)
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
				client, session, target, err = a.attachPageEventSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
				if err != nil {
					return err
				}
				defer session.Close(ctx)
				requestedURL = target.URL
			}

			collectorErrors = enablePageLoadCollectors(ctx, client, session.SessionID, map[string]bool{"console": true, "network": true})
			if len(collectorErrors) > 0 {
				return commandErrorWithData("collector_enable_failed", "connection", "debug-bundle collectors must enable before the evidence trigger", ExitConnection, []string{"cdp pages --json", "cdp doctor --json"}, map[string]any{"collector_errors": collectorErrors, "target_id": target.TargetID})
			}
			cacheRestoreNeeded := false
			defer func() {
				if !cacheRestoreNeeded {
					return
				}
				restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer restoreCancel()
				_ = client.CallSession(restoreCtx, session.SessionID, "Network.setCacheDisabled", map[string]any{"cacheDisabled": false}, nil)
			}()
			if rawURL != "" {
				if ignoreCache {
					if err := client.CallSession(ctx, session.SessionID, "Network.setCacheDisabled", map[string]any{"cacheDisabled": true}, nil); err != nil {
						return commandError("debug_bundle_trigger_failed", "connection", fmt.Sprintf("enable cache bypass for target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp workflow debug-bundle --ignore-cache=false --json", "cdp doctor --json"})
					}
					cacheRestoreNeeded = true
					cachePolicy = "bypass_http_cache"
				}
				if _, err := session.Navigate(ctx, target.URL); err != nil {
					return commandError("debug_bundle_trigger_failed", "connection", fmt.Sprintf("navigate target %s after enabling collectors: %v", target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
				}
				trigger = "navigate"
			} else if reload {
				if err := session.Reload(ctx, ignoreCache); err != nil {
					return commandError("debug_bundle_trigger_failed", "connection", fmt.Sprintf("reload target %s after enabling collectors: %v", target.TargetID, err), ExitConnection, []string{"cdp workflow debug-bundle --target " + target.TargetID + " --reload=false --ignore-cache=false --json", "cdp doctor --json"})
				}
				trigger = "reload"
				reloaded = true
				if ignoreCache {
					cachePolicy = "bypass_http_cache"
				}
			}

			requests, requestsTruncated, messages, messagesTruncated, err := collectPageLoadEvents(ctx, client, session.SessionID, since, 100, map[string]bool{"console": true, "network": true})
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("events", err))
			}
			if cacheRestoreNeeded {
				restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 2*time.Second)
				restoreErr := client.CallSession(restoreCtx, session.SessionID, "Network.setCacheDisabled", map[string]any{"cacheDisabled": false}, nil)
				restoreCancel()
				cacheRestoreNeeded = false
				if restoreErr != nil {
					collectorErrors = append(collectorErrors, collectorError("cache_restore", restoreErr))
				}
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
							meta := debugBundleArtifactRecord{
								Type:    "workflow-debug-bundle-screenshot",
								Path:    writtenPath,
								Bytes:   len(shot.Data),
								Content: "screenshot",
								Safety:  debugBundleArtifactSafety(artifacts.NewRedactor(redact), true, debugBundleScreenshotWarning),
							}
							addArtifact(meta)
						}
					}
				}

				networkRedactor := artifacts.NewRedactor(redact)
				networkRequests := debugBundleRedactedRequests(requests, networkRedactor)
				networkSafety := debugBundleArtifactSafety(networkRedactor, false, "network summary contains redacted request URLs and no headers or bodies by default")
				if _, err := writeBundleArtifact("network", "network-summary", networkSafety, map[string]any{
					"requests": networkRequests,
				}); err != nil {
					collectorErrors = append(collectorErrors, collectorError("artifact", err))
				}
				consoleRedactor := artifacts.NewRedactor(redact)
				consoleMessages := debugBundleRedactedMessages(messages, consoleRedactor)
				consoleSafety := debugBundleArtifactSafety(consoleRedactor, true, debugBundleConsoleWarning)
				if _, err := writeBundleArtifact("console", "console", consoleSafety, map[string]any{
					"messages": consoleMessages,
				}); err != nil {
					collectorErrors = append(collectorErrors, collectorError("artifact", err))
				}
				pageMetadataRedactor := artifacts.NewRedactor(redact)
				pageMetadataSafety := debugBundleArtifactSafety(pageMetadataRedactor, true, debugBundlePageMetadataWarning)
				if _, err := writeBundleArtifact("page-metadata", "page-metadata", pageMetadataSafety, map[string]any{
					"url":              pageMetadataRedactor.URL(target.URL, "page_metadata.url"),
					"title":            snapshot.Title,
					"type":             target.Type,
					"id":               target.TargetID,
					"snapshot":         snapshot.Count,
					"requests":         len(requests),
					"messages":         len(messages),
					"trigger":          trigger,
					"reloaded":         reloaded,
					"ignore_cache":     ignoreCache,
					"cache_policy":     cachePolicy,
					"since":            durationString(since),
					"partial":          len(collectorErrors) > 0,
					"interactive_only": snapshotInteractiveOnly,
				}); err != nil {
					collectorErrors = append(collectorErrors, collectorError("artifact", err))
				}
				workflowRedactor := artifacts.NewRedactor(redact)
				workflowPayload := map[string]any{
					"name":         "debug-bundle",
					"requested":    workflowRedactor.URL(requestedURL, "workflow.requested"),
					"trigger":      trigger,
					"reloaded":     reloaded,
					"ignore_cache": ignoreCache,
					"cache_policy": cachePolicy,
					"run_id":       runID,
					"task_id":      taskID,
					"stage":        stageName,
					"redact":       redact,
					"out_dir":      outDir,
					"since":        durationString(since),
					"browser_mode": a.browserModeName(),
				}
				workflowSafety := debugBundleArtifactSafety(workflowRedactor, false, debugBundleCommandRecordWarning)
				if _, err := writeBundleArtifact("workflow", "workflow-metadata", workflowSafety, workflowPayload); err != nil {
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

			outputRedactor := artifacts.NewRedactor(redact)
			outputTarget := debugBundleRedactedPageRow(target, outputRedactor)
			requestedURLOutput := outputRedactor.URL(requestedURL, "workflow.requested_url")
			nextVerifyCommand := "cdp workflow verify " + requestedURLOutput + " --json"
			if strings.TrimSpace(requestedURLOutput) == "" {
				nextVerifyCommand = "cdp workflow verify <url> --json"
			}
			argvRedactor := artifacts.NewRedactor(redact)
			commandArgv := []string{"cdp", "workflow", "debug-bundle"}
			if rawURL != "" {
				commandArgv = append(commandArgv, "--url", argvRedactor.URL(rawURL, "command.argv.url"))
			} else if targetIndex > 0 {
				commandArgv = append(commandArgv, "--target-index", fmt.Sprintf("%d", targetIndex))
			} else if targetID != "" {
				commandArgv = append(commandArgv, "--target", targetID)
			} else if urlContains != "" {
				commandArgv = append(commandArgv, "--url-contains", urlContains)
			} else if titleContains != "" {
				commandArgv = append(commandArgv, "--title-contains", titleContains)
			}
			commandArgv = append(commandArgv, "--since", durationString(since), "--redact", redact)
			commandArgv = append(commandArgv, fmt.Sprintf("--reload=%t", reload), fmt.Sprintf("--ignore-cache=%t", ignoreCache))
			if outDir != "" {
				commandArgv = append(commandArgv, "--out-dir", outDir)
			}
			if screenshotFull {
				commandArgv = append(commandArgv, "--screenshot-full")
			} else if screenshotView {
				commandArgv = append(commandArgv, "--screenshot-view")
			}
			if inlinePayloads {
				commandArgv = append(commandArgv, "--inline-payloads")
			}
			if runID != "" {
				commandArgv = append(commandArgv, "--run-id", runID)
			}
			if taskID != "" {
				commandArgv = append(commandArgv, "--task-id", taskID)
			}
			if stageName != "" {
				commandArgv = append(commandArgv, "--stage", stageName)
			}
			if keepOpen {
				commandArgv = append(commandArgv, "--keep-open")
			}
			commands = append(commands, newDebugBundleCommandRecord(debugBundleCommandRecordOptions{
				Name:         "workflow debug-bundle",
				BrowserMode:  a.browserModeName(),
				Timeout:      durationString(fallback),
				ExitCode:     ExitOK,
				Status:       "ok",
				TaskID:       taskID,
				RunID:        runID,
				Stage:        stageName,
				Attempt:      1,
				ArtifactPath: layout.Manifest,
				Argv:         commandArgv,
				ArgvRedacted: len(argvRedactor.ChangedFields()) > 0,
			}))
			stages = append(stages, newDebugBundleStageRecord(stageName, "ok", taskID, runID, time.Since(started), commands, artifactRecords))
			if outDir != "" {
				commandSafety := debugBundleArtifactSafety(artifacts.NewRedactor(redact), false, debugBundleCommandRecordWarning)
				commandBytes, err := debugBundleCommandLogJSONL(commands)
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal debug bundle command log: %v", err), ExitInternal, []string{"cdp workflow debug-bundle --json"})
				}
				writtenPath, err := writeArtifactFile(layout.CommandLog, commandBytes)
				if err != nil {
					return err
				}
				addArtifact(debugBundleArtifactRecord{
					Type:    "workflow-debug-bundle-command-log",
					Path:    writtenPath,
					Bytes:   len(commandBytes),
					Content: "command-log",
					Safety:  commandSafety,
				})
				stageBytes, err := json.MarshalIndent(stages, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal debug bundle stage log: %v", err), ExitInternal, []string{"cdp workflow debug-bundle --json"})
				}
				writtenPath, err = writeArtifactFile(layout.StageLog, append(stageBytes, '\n'))
				if err != nil {
					return err
				}
				addArtifact(debugBundleArtifactRecord{
					Type:    "workflow-debug-bundle-stage-log",
					Path:    writtenPath,
					Bytes:   len(stageBytes) + 1,
					Content: "stage-log",
					Safety:  commandSafety,
				})
			}
			bundleSummary := newDebugBundleSummary(layout, redact, inlinePayloads, artifactRecords, commands, stages)
			report := map[string]any{
				"ok":       true,
				"target":   outputTarget,
				"evidence": evidence,
				"bundle":   bundleSummary,
				"workflow": map[string]any{
					"name":                "debug-bundle",
					"requested_url":       requestedURLOutput,
					"trigger":             trigger,
					"reloaded":            reloaded,
					"ignore_cache":        ignoreCache,
					"cache_policy":        cachePolicy,
					"since":               durationString(since),
					"request_count":       len(requests),
					"message_count":       len(messages),
					"snapshot_item_count": len(snapshot.Items),
					"requests_truncated":  requestsTruncated,
					"messages_truncated":  messagesTruncated,
					"collector_errors":    collectorErrors,
					"partial":             len(collectorErrors) > 0,
					"next_commands": []string{
						nextVerifyCommand,
						"cdp console --target " + target.TargetID + " --errors --wait 5s --json",
						"cdp network --target " + target.TargetID + " --failed --wait 5s --json",
					},
					"screenshot_view": screenshotView,
					"screenshot_full": screenshotFull,
					"redact":          redact,
					"inline_payloads": inlinePayloads,
					"keep_open":       keepOpen,
					"run_id":          runID,
					"task_id":         taskID,
					"stage":           stageName,
				},
			}
			addWorkflowTargetIndex(report, targetIndex)
			if inlinePayloads {
				report["requests"] = debugBundleRedactedRequests(requests, artifacts.NewRedactor(redact))
				report["messages"] = debugBundleRedactedMessages(messages, artifacts.NewRedactor(redact))
				report["snapshot"] = debugBundleRedactedSnapshot(snapshot, artifacts.NewRedactor(redact))
			}
			if outDir != "" {
				bundleSafety := debugBundleArtifactSafety(artifacts.NewRedactor(redact), false, debugBundleCommandRecordWarning)
				bundleMeta, err := writeBundleArtifact("bundle", "bundle-manifest", bundleSafety, report)
				if err != nil {
					return err
				}
				report["artifact"] = bundleMeta
				bundleSummary = newDebugBundleSummary(layout, redact, inlinePayloads, artifactRecords, commands, stages)
				report["bundle"] = bundleSummary
			}
			if len(artifactRecords) > 0 {
				report["artifacts"] = artifactRecords
				report["artifact_list"] = debugBundleArtifactList(artifactRecords)
			}
			return a.render(ctx, fmt.Sprintf("debug-bundle\t%s", target.TargetID), report)
		},
	}
	cmd.Flags().StringVar(&rawURL, "url", "", "open this URL before collecting the debug bundle")
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "use the 1-based existing page index; workers do not consume indexes and this cannot be combined with --url, --target, --url-contains, or --title-contains")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "optional directory for debug bundle artifacts")
	cmd.Flags().DurationVar(&since, "since", 5*time.Second, "how long to collect evidence after navigation/attach")
	cmd.Flags().BoolVar(&screenshotFull, "screenshot-full", false, "capture full-page screenshot in the debug bundle")
	cmd.Flags().BoolVar(&screenshotView, "screenshot-view", false, "capture viewport screenshot in the debug bundle")
	cmd.Flags().BoolVar(&snapshotInteractiveOnly, "snapshot-interactive-only", false, "reserved compatibility flag; snapshot still returns visible text items")
	cmd.Flags().StringVar(&redact, "redact", artifacts.ModeSafe, "redaction preset for URLs and obvious secrets: safe or none")
	cmd.Flags().BoolVar(&inlinePayloads, "inline-payloads", false, "include redacted request, console, and snapshot payloads in command JSON instead of artifact references only")
	cmd.Flags().StringVar(&runID, "run-id", "", "optional run id recorded in bundle command and stage logs")
	cmd.Flags().StringVar(&taskID, "task-id", "", "optional task id recorded in bundle command and stage logs")
	cmd.Flags().StringVar(&stageName, "stage", "", "optional stage name recorded in bundle command and stage logs")
	cmd.Flags().BoolVar(&keepOpen, "keep-open", false, "leave the workflow-created page open for follow-up commands")
	cmd.Flags().BoolVar(&reload, "reload", true, "reload an existing selected target after collectors are armed")
	cmd.Flags().BoolVar(&ignoreCache, "ignore-cache", true, "bypass ordinary HTTP cache for the evidence-triggering reload or navigation")
	return cmd
}
