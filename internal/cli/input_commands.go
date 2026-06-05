package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type clickResult struct {
	URL        string       `json:"url"`
	Title      string       `json:"title"`
	Selector   string       `json:"selector"`
	Count      int          `json:"count"`
	Clicked    bool         `json:"clicked"`
	Trial      bool         `json:"trial,omitempty"`
	Force      bool         `json:"force,omitempty"`
	Strategy   string       `json:"strategy,omitempty"`
	X          float64      `json:"x,omitempty"`
	Y          float64      `json:"y,omitempty"`
	Rect       snapshotRect `json:"rect,omitempty"`
	Verified   *bool        `json:"verified,omitempty"`
	TargetID   string       `json:"target_id,omitempty"`
	FinalURL   string       `json:"final_url,omitempty"`
	FinalTitle string       `json:"final_title,omitempty"`
	Error      *evalError   `json:"error,omitempty"`
}

type fillResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Filled   bool       `json:"filled"`
	Trial    bool       `json:"trial,omitempty"`
	Force    bool       `json:"force,omitempty"`
	Value    string     `json:"value"`
	Previous string     `json:"previous"`
	Error    *evalError `json:"error,omitempty"`
}

type locatorActionOptions struct {
	By            string
	Role          string
	TestIDAttr    string
	Exact         bool
	IncludeHidden bool
	Limit         int
}

type typeResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Typing   bool       `json:"typing"`
	Trial    bool       `json:"trial,omitempty"`
	Force    bool       `json:"force,omitempty"`
	Typed    string     `json:"typed"`
	Previous string     `json:"previous"`
	Value    string     `json:"value,omitempty"`
	Kind     string     `json:"kind,omitempty"`
	Strategy string     `json:"strategy,omitempty"`
	Error    *evalError `json:"error,omitempty"`
}

type pressResult struct {
	URL        string     `json:"url"`
	Title      string     `json:"title"`
	Selector   string     `json:"selector"`
	Key        string     `json:"key"`
	Count      int        `json:"count"`
	Dispatched bool       `json:"dispatched"`
	Trial      bool       `json:"trial,omitempty"`
	Error      *evalError `json:"error,omitempty"`
}

type hoverResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Hovered  bool       `json:"hovered"`
	Trial    bool       `json:"trial,omitempty"`
	Force    bool       `json:"force,omitempty"`
	X        float64    `json:"x"`
	Y        float64    `json:"y"`
	Error    *evalError `json:"error,omitempty"`
}

type dragResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Dragged  bool       `json:"dragged"`
	Trial    bool       `json:"trial,omitempty"`
	Force    bool       `json:"force,omitempty"`
	DeltaX   int        `json:"delta_x"`
	DeltaY   int        `json:"delta_y"`
	StartX   float64    `json:"start_x"`
	StartY   float64    `json:"start_y"`
	EndX     float64    `json:"end_x"`
	EndY     float64    `json:"end_y"`
	Error    *evalError `json:"error,omitempty"`
}

type frameTreeResponse struct {
	FrameTree *frameTreeNode `json:"frameTree"`
}

type frameTreeNode struct {
	Frame       *frameInfo      `json:"frame"`
	ChildFrames []frameTreeNode `json:"childFrames"`
}

type frameInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	SecurityOrigin string `json:"securityOrigin"`
	MimeType       string `json:"mimeType"`
}

type frameSummary struct {
	FrameID        string `json:"frame_id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	SecurityOrigin string `json:"security_origin"`
	MimeType       string `json:"mime_type"`
	ParentID       string `json:"parent_id"`
	ChildCount     int    `json:"child_count"`
}

func (a *app) newClickCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var strategy string
	var locatorOpts locatorActionOptions
	var activate bool
	var waitText string
	var waitSelector string
	var waitPopup bool
	var waitPopupURL string
	var waitPopupTitle string
	var waitDownload bool
	var waitDownloadURL string
	var waitDownloadFilename string
	var waitDownloadState string
	var waitDownloadDir string
	var waitDownloadRedact string
	var waitDialog bool
	var waitDialogType string
	var waitDialogMessage string
	var waitDialogMessageContains string
	var waitDialogAction string
	var waitDialogPromptText string
	var waitDialogRedact string
	var diagnosticsOut string
	var poll time.Duration
	var trial bool
	var force bool
	cmd := &cobra.Command{
		Use:   "click <selector-or-locator>",
		Short: "Click the first matching element by CSS selector or strict locator",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			strategy = strings.ToLower(strings.TrimSpace(strategy))
			if strategy == "" {
				strategy = "auto"
			}
			if strategy != "auto" && strategy != "dom" && strategy != "raw-input" {
				return commandError("usage", "usage", "--strategy must be auto, dom, or raw-input", ExitUsage, []string{"cdp click main --strategy raw-input --json"})
			}
			if strings.TrimSpace(waitText) != "" && strings.TrimSpace(waitSelector) != "" {
				return commandError("usage", "usage", "use only one of --wait-text or --wait-selector", ExitUsage, []string{"cdp click button --wait-text Done --json"})
			}
			waitPopup = waitPopup || strings.TrimSpace(waitPopupURL) != "" || strings.TrimSpace(waitPopupTitle) != ""
			waitDownload = waitDownload || cmd.Flags().Changed("wait-download-url") || cmd.Flags().Changed("wait-download-filename") || cmd.Flags().Changed("wait-download-state") || cmd.Flags().Changed("download-dir") || cmd.Flags().Changed("wait-download-redact")
			waitDialog = waitDialog || cmd.Flags().Changed("wait-dialog-type") || cmd.Flags().Changed("wait-dialog-message") || cmd.Flags().Changed("wait-dialog-message-contains") || cmd.Flags().Changed("wait-dialog-action") || cmd.Flags().Changed("wait-dialog-prompt-text") || cmd.Flags().Changed("wait-dialog-redact")
			waitModeCount := 0
			if strings.TrimSpace(waitText) != "" || strings.TrimSpace(waitSelector) != "" {
				waitModeCount++
			}
			if waitPopup {
				waitModeCount++
			}
			if waitDownload {
				waitModeCount++
			}
			if waitDialog {
				waitModeCount++
			}
			if waitModeCount > 1 {
				return commandError("usage", "usage", "use only one click wait mode: --wait-popup, --wait-download, --wait-dialog, --wait-text, or --wait-selector", ExitUsage, []string{"cdp click 'Sign in' --by role --role link --wait-popup --json"})
			}
			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp click button --wait-text Done --poll 250ms --json"})
			}
			if trial && (strings.TrimSpace(waitText) != "" || strings.TrimSpace(waitSelector) != "") {
				return commandError("usage", "usage", "--trial does not dispatch a click, so it cannot use --wait-text or --wait-selector", ExitUsage, []string{"cdp click 'Search' --by role --role button --trial --json"})
			}
			if trial && waitPopup {
				return commandError("usage", "usage", "--trial does not dispatch a click, so it cannot use --wait-popup", ExitUsage, []string{"cdp click 'Sign in' --by role --role link --wait-popup --json"})
			}
			if trial && waitDownload {
				return commandError("usage", "usage", "--trial does not dispatch a click, so it cannot use --wait-download", ExitUsage, []string{"cdp click 'Download' --by role --role link --wait-download --json"})
			}
			if trial && waitDialog {
				return commandError("usage", "usage", "--trial does not dispatch a click, so it cannot use --wait-dialog", ExitUsage, []string{"cdp click 'Delete' --by role --role button --wait-dialog --json"})
			}
			if trial && strings.TrimSpace(diagnosticsOut) != "" {
				return commandError("usage", "usage", "--trial returns actionability diagnostics inline; omit --diagnostics-out", ExitUsage, []string{"cdp click 'Search' --by role --role button --trial --json"})
			}
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			if activate {
				if err := cdp.ActivateTargetWithClient(ctx, client, target.TargetID); err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("activate target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp page activate --json"})
				}
			}

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "click")
			if err != nil {
				return err
			}

			actionability, err := evaluateActionability(ctx, session, selector, "click")
			if err != nil {
				return err
			}
			if actionability.Error != nil {
				return invalidSelectorError(selector, actionability.Error, "cdp click main --trial --json")
			}
			prepareActionability(&actionability, "click", trial, force)
			var autoScroll *scrollResult
			if !trial && shouldAutoScrollBeforePointerAction("click", actionability) {
				scrolled, err := autoScrollPointerTarget(ctx, session, selector)
				if err != nil {
					return err
				}
				autoScroll = &scrolled
				if scrolled.Error == nil && scrolled.After.InViewport {
					actionability, err = evaluateActionability(ctx, session, selector, "click")
					if err != nil {
						return err
					}
					if actionability.Error != nil {
						return invalidSelectorError(selector, actionability.Error, "cdp click main --trial --json")
					}
					prepareActionability(&actionability, "click", trial, force)
				}
			}
			if trial {
				result := clickResult{
					URL:      actionability.URL,
					Title:    actionability.Title,
					Selector: selector,
					Count:    actionability.Count,
					Clicked:  false,
					Trial:    true,
					Force:    force,
					Strategy: strategy,
					X:        actionability.Point.X,
					Y:        actionability.Point.Y,
					Rect:     actionability.Rect,
				}
				report := map[string]any{
					"ok":            actionability.Actionable,
					"action":        "trial",
					"target":        pageRow(target),
					"before_target": pageRow(target),
					"after_target":  pageRow(target),
					"final_target":  pageRow(target),
					"page_state":    clickPageState(target, target),
					"click":         result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if !actionability.Actionable {
					return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("click", selector, actionability), ExitCheckFailed, actionabilityRemediations("click", args[0], selector, locatorOpts), report)
				}
				return a.render(ctx, fmt.Sprintf("trial\t%s\t%s", target.TargetID, selector), report)
			}
			if !actionability.Actionable {
				result := clickResult{
					URL:      actionability.URL,
					Title:    actionability.Title,
					Selector: selector,
					Count:    actionability.Count,
					Clicked:  false,
					Force:    force,
					Strategy: strategy,
					X:        actionability.Point.X,
					Y:        actionability.Point.Y,
					Rect:     actionability.Rect,
				}
				report := map[string]any{
					"ok":            false,
					"action":        "blocked",
					"target":        pageRow(target),
					"before_target": pageRow(target),
					"after_target":  pageRow(target),
					"final_target":  pageRow(target),
					"page_state":    clickPageState(target, target),
					"click":         result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if autoScroll != nil {
					report["auto_scroll"] = autoScroll
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("click", selector, actionability), ExitCheckFailed, actionabilityRemediations("click", args[0], selector, locatorOpts), report)
			}

			var popupCriteria popupWaitCriteria
			var popupBaseline []popupWaitTarget
			var popupReport map[string]any
			var popupErr error
			var downloadOpts downloadWaitOptions
			var downloadReport map[string]any
			var downloadErr error
			var dialogOpts dialogWaitOptions
			var dialogReport map[string]any
			var dialogErr error
			if waitPopup {
				popupCriteria = popupWaitCriteria{
					OpenerID:    target.TargetID,
					URLContains: strings.TrimSpace(waitPopupURL),
					Title:       strings.TrimSpace(waitPopupTitle),
				}
				popupBaseline, err = popupWaitListTargets(ctx, client)
				if err != nil {
					return popupWaitConnectionError(target.TargetID, err)
				}
				teardown, err := enablePopupTargetDiscovery(ctx, client)
				if err != nil {
					return popupWaitConnectionError(target.TargetID, err)
				}
				defer func() {
					teardownCtx, teardownCancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer teardownCancel()
					_ = teardown(teardownCtx)
				}()
				if _, err := client.DrainEvents(ctx); err != nil {
					return popupWaitConnectionError(target.TargetID, err)
				}
			}
			if waitDownload {
				downloadOpts = downloadWaitOptions{
					Criteria: downloadWaitCriteria{
						URLContains:      strings.TrimSpace(waitDownloadURL),
						FilenameContains: strings.TrimSpace(waitDownloadFilename),
						State:            strings.TrimSpace(waitDownloadState),
					},
					DownloadDir: strings.TrimSpace(waitDownloadDir),
					Redact:      waitDownloadRedact,
				}
				if err := a.normalizeDownloadWaitOptions(&downloadOpts); err != nil {
					return err
				}
				if _, err := networkWaitRedactor(downloadOpts.Redact); err != nil {
					return err
				}
				if err := os.MkdirAll(downloadOpts.DownloadDir, 0o700); err != nil {
					return commandError("download_dir_unavailable", "usage", fmt.Sprintf("create download dir %s: %v", downloadOpts.DownloadDir, err), ExitUsage, []string{"cdp click 'Download' --wait-download --download-dir tmp/downloads --json"})
				}
				teardown, err := setupDownloadWait(ctx, client, downloadOpts)
				if err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("click download wait target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
				}
				defer func() {
					teardownCtx, teardownCancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer teardownCancel()
					_ = teardown(teardownCtx)
				}()
				if _, err := client.DrainEvents(ctx); err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("click download wait target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
				}
			}
			if waitDialog {
				dialogOpts = dialogWaitOptions{
					Criteria: dialogWaitCriteria{
						Type:            waitDialogType,
						Message:         waitDialogMessage,
						MessageContains: waitDialogMessageContains,
					},
					Action:     waitDialogAction,
					PromptText: waitDialogPromptText,
					Redact:     waitDialogRedact,
				}
				if err := normalizeDialogWaitOptions(&dialogOpts); err != nil {
					return err
				}
				if err := setupDialogWait(ctx, client, session.SessionID); err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("click dialog wait target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
				}
				if _, err := client.DrainEvents(ctx); err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("click dialog wait target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
				}
			}

			clickStrategy := strategy
			if (waitPopup || waitDownload || waitDialog) && clickStrategy == "auto" {
				clickStrategy = "raw-input"
			}
			eventWaitStart := time.Now()
			result, err := performClick(ctx, session, selector, clickStrategy)
			if err != nil {
				return err
			}
			result.Force = force
			if result.Error != nil {
				return invalidSelectorError(selector, result.Error, "cdp click main --json")
			}
			if !result.Clicked {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no matching element found for selector %q", selector),
					ExitUsage,
					[]string{"cdp click main --json"},
				)
			}

			verified := true
			var verification *waitResult
			if strings.TrimSpace(waitText) != "" || strings.TrimSpace(waitSelector) != "" {
				wait, err := waitForClickVerification(ctx, session, poll, waitText, waitSelector)
				if err != nil {
					return err
				}
				verification = &wait
				verified = wait.Matched
				if !verified && strategy == "auto" {
					fallback, err := performClick(ctx, session, selector, "raw-input")
					if err != nil {
						return err
					}
					if fallback.Error != nil {
						return invalidSelectorError(selector, fallback.Error, "cdp click main --strategy raw-input --json")
					}
					result = fallback
					wait, err = waitForClickVerification(ctx, session, poll, waitText, waitSelector)
					if err != nil {
						return err
					}
					verification = &wait
					verified = wait.Matched
				}
				result.Verified = &verified
			}
			if waitPopup {
				observation, err := collectPopupEvent(ctx, client, popupBaseline, popupCriteria)
				popupReport = popupWaitReport(observation, popupCriteria, target, time.Since(eventWaitStart), a.effectiveNetworkWaitTimeout(), len(popupBaseline))
				verified = observation.Matched
				result.Verified = &verified
				if err != nil {
					popupErr = err
				}
			}
			if waitDownload {
				redactor, err := networkWaitRedactor(downloadOpts.Redact)
				if err != nil {
					return err
				}
				observation, err := collectDownloadEvent(ctx, client, downloadOpts)
				downloadReport = downloadWaitReport(observation, downloadOpts, target, time.Since(eventWaitStart), a.effectiveNetworkWaitTimeout(), redactor)
				verified = observation.Matched
				result.Verified = &verified
				if err != nil {
					downloadErr = err
				}
			}
			if waitDialog {
				redactor, err := networkWaitRedactor(dialogOpts.Redact)
				if err != nil {
					return err
				}
				observation, err := collectDialogEvent(ctx, client, session.SessionID, dialogOpts.Criteria)
				dialogReport = dialogWaitReport(observation, dialogOpts, time.Since(eventWaitStart), a.effectiveNetworkWaitTimeout(), redactor)
				dialogReport["target"] = pageRow(target)
				verified = observation.Matched
				result.Verified = &verified
				if err != nil {
					dialogErr = err
				}
				if err == nil {
					if err := handleDialogWaitAction(ctx, client, session.SessionID, target.TargetID, dialogOpts, dialogReport); err != nil {
						return err
					}
				}
			}

			finalTarget, refreshErr := refreshedClickTarget(ctx, client, target)
			result.TargetID = finalTarget.TargetID
			result.FinalURL = finalTarget.URL
			result.FinalTitle = finalTarget.Title

			report := map[string]any{
				"ok":            verified,
				"action":        "clicked",
				"target":        pageRow(finalTarget),
				"before_target": pageRow(target),
				"after_target":  pageRow(finalTarget),
				"final_target":  pageRow(finalTarget),
				"page_state":    clickPageState(target, finalTarget),
				"click":         result,
				"actionability": actionability,
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			if autoScroll != nil {
				report["auto_scroll"] = autoScroll
			}
			if refreshErr != nil {
				report["target_refresh"] = map[string]any{
					"ok":        false,
					"target_id": target.TargetID,
					"error":     refreshErr.Error(),
				}
			}
			if verification != nil {
				report["verification"] = verification
			}
			if popupReport != nil {
				addPopupWaitToClickReport(report, popupReport)
			}
			if downloadReport != nil {
				addDownloadWaitToClickReport(report, downloadReport)
			}
			if dialogReport != nil {
				addDialogWaitToClickReport(report, dialogReport)
			}
			if strings.TrimSpace(diagnosticsOut) != "" {
				diagnostics := clickDiagnostics(target, finalTarget, selector, strategy, activate, force, waitText, waitSelector, a.clickTimeout(), result, verification)
				diagnostics["actionability"] = actionability
				if autoScroll != nil {
					diagnostics["auto_scroll"] = autoScroll
				}
				if popupReport != nil {
					addPopupWaitToClickReport(diagnostics, popupReport)
				}
				if downloadReport != nil {
					addDownloadWaitToClickReport(diagnostics, downloadReport)
				}
				if dialogReport != nil {
					addDialogWaitToClickReport(diagnostics, dialogReport)
				}
				report["diagnostics"] = diagnostics
				b, err := json.MarshalIndent(diagnostics, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal click diagnostics: %v", err), ExitInternal, []string{"cdp click button --diagnostics-out tmp/click.local.json --json"})
				}
				writtenPath, err := writeArtifactFile(diagnosticsOut, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "click-diagnostics", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "click-diagnostics", "path": writtenPath, "bytes": len(b) + 1}}
			}
			if popupErr != nil {
				return popupWaitError(ctx, target.TargetID, popupCriteria, report, popupErr)
			}
			if downloadErr != nil {
				return downloadWaitError(ctx, target.TargetID, downloadOpts, report, downloadErr)
			}
			if dialogErr != nil {
				return dialogWaitError(ctx, target.TargetID, dialogOpts, report, dialogErr)
			}
			human := fmt.Sprintf("clicked\t%s\t%s", target.TargetID, result.Selector)
			if waitPopup {
				human = fmt.Sprintf("clicked-popup\t%s\t%s", target.TargetID, result.Selector)
			}
			if waitDownload {
				human = fmt.Sprintf("clicked-download\t%s\t%s", target.TargetID, result.Selector)
			}
			if waitDialog {
				human = fmt.Sprintf("clicked-dialog\t%s\t%s", target.TargetID, result.Selector)
			}
			if !verified {
				human = fmt.Sprintf("click-unverified\t%s\t%s", target.TargetID, result.Selector)
			}
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&strategy, "strategy", "auto", "click strategy: auto, dom, or raw-input")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&activate, "activate", false, "bring the target page to the foreground before clicking")
	cmd.Flags().StringVar(&waitText, "wait-text", "", "verify by waiting until visible page text contains this string")
	cmd.Flags().StringVar(&waitSelector, "wait-selector", "", "verify by waiting until this CSS selector matches")
	cmd.Flags().BoolVar(&waitPopup, "wait-popup", false, "wait for a popup or new tab opened by this click")
	cmd.Flags().StringVar(&waitPopupURL, "wait-popup-url", "", "substring that the popup URL must contain; implies --wait-popup")
	cmd.Flags().StringVar(&waitPopupTitle, "wait-popup-title", "", "substring that the popup title must contain; implies --wait-popup")
	cmd.Flags().BoolVar(&waitDownload, "wait-download", false, "wait for a browser download started by this click")
	cmd.Flags().StringVar(&waitDownloadURL, "wait-download-url", "", "substring that the download URL must contain; implies --wait-download")
	cmd.Flags().StringVar(&waitDownloadFilename, "wait-download-filename", "", "substring that the suggested filename must contain; implies --wait-download")
	cmd.Flags().StringVar(&waitDownloadState, "wait-download-state", "completed", "download wait state when --wait-download is used: started or completed")
	cmd.Flags().StringVar(&waitDownloadDir, "download-dir", "", "directory where Chrome should save downloaded files; implies --wait-download")
	cmd.Flags().StringVar(&waitDownloadRedact, "wait-download-redact", "safe", "redaction preset for returned download URL: safe or none")
	cmd.Flags().BoolVar(&waitDialog, "wait-dialog", false, "wait for a JavaScript dialog opened by this click")
	cmd.Flags().StringVar(&waitDialogType, "wait-dialog-type", "", "dialog type to match: alert, confirm, prompt, or beforeunload; implies --wait-dialog")
	cmd.Flags().StringVar(&waitDialogMessage, "wait-dialog-message", "", "exact dialog message to match; implies --wait-dialog")
	cmd.Flags().StringVar(&waitDialogMessageContains, "wait-dialog-message-contains", "", "substring that the dialog message must contain; implies --wait-dialog")
	cmd.Flags().StringVar(&waitDialogAction, "wait-dialog-action", "none", "handle the matched dialog when --wait-dialog is used: none, accept, or dismiss")
	cmd.Flags().StringVar(&waitDialogPromptText, "wait-dialog-prompt-text", "", "prompt text to send when --wait-dialog-action accept handles a prompt dialog")
	cmd.Flags().StringVar(&waitDialogRedact, "wait-dialog-redact", "safe", "redaction preset for returned dialog URL: safe or none")
	cmd.Flags().StringVar(&diagnosticsOut, "diagnostics-out", "", "optional path for privacy-preserving click diagnostics JSON")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while waiting for verification")
	cmd.Flags().BoolVar(&trial, "trial", false, "run locator resolution and actionability checks without dispatching the click")
	cmd.Flags().BoolVar(&force, "force", false, "skip non-essential click actionability checks and record skipped checks in JSON")
	return cmd
}

func performClick(ctx context.Context, session *cdp.PageSession, selector, strategy string) (clickResult, error) {
	if strategy == "auto" || strategy == "dom" {
		var result clickResult
		if err := evaluateJSONValue(ctx, session, clickExpression(selector), "click", &result); err != nil {
			return clickResult{}, err
		}
		result.Strategy = "dom"
		return result, nil
	}
	var result clickResult
	if err := evaluateJSONValue(ctx, session, rawClickPointExpression(selector), "click point", &result); err != nil {
		return clickResult{}, err
	}
	if result.Error != nil || !result.Clicked {
		return result, nil
	}
	if err := dispatchRawMouseClick(ctx, session, result.X, result.Y); err != nil {
		return clickResult{}, err
	}
	result.Strategy = "raw-input"
	return result, nil
}

func dispatchRawMouseClick(ctx context.Context, session *cdp.PageSession, x, y float64) error {
	events := []map[string]any{
		{"type": "mouseMoved", "x": x, "y": y, "button": "none"},
		{"type": "mousePressed", "x": x, "y": y, "button": "left", "buttons": 1, "clickCount": 1},
		{"type": "mouseReleased", "x": x, "y": y, "button": "left", "buttons": 0, "clickCount": 1},
	}
	for _, event := range events {
		params, _ := json.Marshal(event)
		if _, err := session.Exec(ctx, "Input.dispatchMouseEvent", params); err != nil {
			return commandError("connection_failed", "connection", fmt.Sprintf("dispatch raw mouse event target %s: %v", session.TargetID, err), ExitConnection, []string{"cdp protocol exec Input.dispatchMouseEvent --params '{\"type\":\"mousePressed\",\"x\":100,\"y\":100,\"button\":\"left\",\"buttons\":1,\"clickCount\":1}' --json"})
		}
	}
	return nil
}

func addLocatorActionFlags(cmd *cobra.Command, opts *locatorActionOptions) {
	cmd.Flags().StringVar(&opts.By, "by", "css", "locator strategy before action/assertion: css, role, text, label, placeholder, alt, title, or test-id")
	cmd.Flags().StringVar(&opts.Role, "role", "", "ARIA role to match when --by role is used")
	cmd.Flags().StringVar(&opts.TestIDAttr, "test-id-attr", "data-testid", "attribute name for --by test-id")
	cmd.Flags().BoolVar(&opts.Exact, "exact", false, "require exact normalized text/name/attribute match for locator actions/assertions")
	cmd.Flags().BoolVar(&opts.IncludeHidden, "include-hidden", false, "include hidden locator matches before action/assertion")
	cmd.Flags().IntVar(&opts.Limit, "locator-limit", 20, "maximum locator matches to inspect before action/assertion")
}

func normalizeLocatorActionOptions(opts *locatorActionOptions) error {
	opts.By = normalizeLocatorStrategy(opts.By)
	opts.Role = strings.TrimSpace(opts.Role)
	opts.TestIDAttr = strings.TrimSpace(opts.TestIDAttr)
	return validateLocatorFindOptions(opts.By, opts.Role, opts.TestIDAttr, opts.Limit)
}

func resolveActionSelector(ctx context.Context, session *cdp.PageSession, query string, opts locatorActionOptions, action string) (string, *locatorFindResult, error) {
	if opts.By == "css" {
		return query, nil, nil
	}

	var result locatorFindResult
	if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "locator action", &result); err != nil {
		return "", nil, err
	}
	if result.Error != nil {
		return "", &result, commandError(
			"invalid_locator",
			"usage",
			fmt.Sprintf("%s locator %s %q: %s", action, opts.By, query, result.Error.Message),
			ExitUsage,
			locatorActionRemediations(action, query, opts),
		)
	}
	if result.Count == 0 {
		return "", &result, commandError(
			"locator_not_found",
			"usage",
			fmt.Sprintf("%s locator %s %q matched no elements", action, opts.By, query),
			ExitUsage,
			locatorActionRemediations(action, query, opts),
		)
	}
	if result.Count != 1 || len(result.Matches) != 1 {
		return "", &result, commandError(
			"ambiguous_locator",
			"usage",
			fmt.Sprintf("%s locator %s %q matched %d elements; refine the locator before acting", action, opts.By, query, result.Count),
			ExitUsage,
			locatorActionRemediations(action, query, opts),
		)
	}

	match := result.Matches[0]
	selector := strings.TrimSpace(match.SelectorHint)
	if selector == "" || match.SelectorAmbiguous {
		return "", &result, commandError(
			"ambiguous_locator",
			"usage",
			fmt.Sprintf("%s locator %s %q matched one element but did not produce a unique CSS selector hint", action, opts.By, query),
			ExitUsage,
			[]string{locatorActionFindCommand(query, opts), "cdp snapshot --selector body --json"},
		)
	}
	return selector, &result, nil
}

func locatorActionRemediations(action, query string, opts locatorActionOptions) []string {
	example := "cdp " + action + " " + shellQuote(query)
	switch action {
	case "press":
		example = "cdp press Enter " + shellQuote(query)
	case "fill", "type":
		example += " <value>"
	case "select":
		example += " <value>"
	case "drag":
		example += " 10 20"
	case "assert value", "assert text":
		example += " <expected>"
	}
	example += " --by " + opts.By
	if opts.By == "role" {
		example += " --role " + shellQuote(opts.Role)
	}
	if opts.Exact {
		example += " --exact"
	}
	if opts.IncludeHidden {
		example += " --include-hidden"
	}
	if opts.By == "test-id" && opts.TestIDAttr != "data-testid" {
		example += " --test-id-attr " + shellQuote(opts.TestIDAttr)
	}
	return []string{locatorActionFindCommand(query, opts), example + " --json"}
}

func locatorActionFindCommand(query string, opts locatorActionOptions) string {
	command := "cdp locator find " + shellQuote(query) + " --by " + opts.By
	if opts.By == "role" {
		command += " --role " + shellQuote(opts.Role)
	}
	if opts.Exact {
		command += " --exact"
	}
	if opts.IncludeHidden {
		command += " --include-hidden"
	}
	if opts.By == "test-id" && opts.TestIDAttr != "data-testid" {
		command += " --test-id-attr " + shellQuote(opts.TestIDAttr)
	}
	return command + " --json"
}

func waitForClickVerification(ctx context.Context, session *cdp.PageSession, poll time.Duration, waitText, waitSelector string) (waitResult, error) {
	start := time.Now()
	kind := "text"
	value := strings.TrimSpace(waitText)
	label := "wait text"
	expression := func() string { return waitTextExpression(value) }
	if strings.TrimSpace(waitSelector) != "" {
		kind = "selector"
		value = strings.TrimSpace(waitSelector)
		label = "wait selector"
		expression = func() string { return waitSelectorExpression(value) }
	}
	last := waitResult{Kind: kind, PollInterval: poll.String()}
	if kind == "text" {
		last.Needle = value
	} else {
		last.Selector = value
	}
	for {
		select {
		case <-ctx.Done():
			last.ElapsedMS = time.Since(start).Milliseconds()
			return last, nil
		default:
		}

		var result waitResult
		if err := evaluateJSONValue(ctx, session, expression(), label, &result); err != nil {
			if clickVerificationTimedOut(ctx, err) {
				last.ElapsedMS = time.Since(start).Milliseconds()
				last.PollInterval = poll.String()
				return last, nil
			}
			return waitResult{}, err
		}
		result.ElapsedMS = time.Since(start).Milliseconds()
		result.PollInterval = poll.String()
		last = result
		if result.Matched || result.Error != nil {
			return result, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			last.ElapsedMS = time.Since(start).Milliseconds()
			last.PollInterval = poll.String()
			return last, nil
		case <-timer.C:
		}
	}
}

func clickVerificationTimedOut(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	if commandErr.ExitCode == ExitTimeout {
		return true
	}
	return commandErr.Code == "connection_failed" && strings.Contains(strings.ToLower(commandErr.Message), "timeout")
}

func refreshedClickTarget(ctx context.Context, client cdp.CommandClient, before cdp.TargetInfo) (cdp.TargetInfo, error) {
	if strings.TrimSpace(before.TargetID) == "" {
		return before, nil
	}
	after, err := cdp.TargetInfoWithClient(ctx, client, before.TargetID)
	if err != nil {
		return before, err
	}
	if strings.TrimSpace(after.TargetID) == "" {
		return before, nil
	}
	return after, nil
}

func clickPageState(before, after cdp.TargetInfo) map[string]any {
	return map[string]any{
		"before":        pageRow(before),
		"after":         pageRow(after),
		"final":         pageRow(after),
		"same_target":   before.TargetID == after.TargetID,
		"url_changed":   before.URL != after.URL,
		"title_changed": before.Title != after.Title,
	}
}

func addPopupWaitToClickReport(report map[string]any, popupReport map[string]any) {
	if report == nil || popupReport == nil {
		return
	}
	if wait, ok := popupReport["wait"]; ok {
		report["popup_wait"] = wait
	}
	if popup, ok := popupReport["popup"]; ok {
		report["popup"] = popup
	}
	if lastEvent, ok := popupReport["last_event"]; ok {
		report["last_popup_event"] = lastEvent
	}
	if nextCommands, ok := popupReport["next_commands"]; ok {
		report["next_commands"] = nextCommands
	}
}

func addDownloadWaitToClickReport(report map[string]any, downloadReport map[string]any) {
	if report == nil || downloadReport == nil {
		return
	}
	if wait, ok := downloadReport["wait"]; ok {
		report["download_wait"] = wait
	}
	if event, ok := downloadReport["event"]; ok {
		report["download_event"] = event
	}
	if progress, ok := downloadReport["progress"]; ok {
		report["download_progress"] = progress
	}
	if download, ok := downloadReport["download"]; ok {
		report["download"] = download
	}
	if lastEvent, ok := downloadReport["last_event"]; ok {
		report["last_download_event"] = lastEvent
	}
	if nextCommands, ok := downloadReport["next_commands"]; ok {
		report["next_commands"] = nextCommands
	}
}

func addDialogWaitToClickReport(report map[string]any, dialogReport map[string]any) {
	if report == nil || dialogReport == nil {
		return
	}
	if wait, ok := dialogReport["wait"]; ok {
		report["dialog_wait"] = wait
	}
	if dialog, ok := dialogReport["dialog"]; ok {
		report["dialog"] = dialog
	}
	if lastEvent, ok := dialogReport["last_event"]; ok {
		report["last_dialog_event"] = lastEvent
	}
	if nextCommands, ok := dialogReport["next_commands"]; ok {
		report["next_commands"] = nextCommands
	}
}

func (a *app) clickTimeout() time.Duration {
	if a.opts.timeout > 0 {
		return a.opts.timeout
	}
	return 10 * time.Second
}

func clickDiagnostics(beforeTarget, afterTarget cdp.TargetInfo, selector, requestedStrategy string, activate bool, force bool, waitText, waitSelector string, timeout time.Duration, click clickResult, verification *waitResult) map[string]any {
	diagnostics := map[string]any{
		"selector":           selector,
		"requested_strategy": requestedStrategy,
		"strategy":           click.Strategy,
		"activated":          activate,
		"force":              force,
		"timeout":            timeout.String(),
		"target":             pageRow(afterTarget),
		"before_target":      pageRow(beforeTarget),
		"after_target":       pageRow(afterTarget),
		"page_state":         clickPageState(beforeTarget, afterTarget),
		"click": map[string]any{
			"clicked": click.Clicked,
			"count":   click.Count,
			"rect":    click.Rect,
			"x":       click.X,
			"y":       click.Y,
		},
	}
	if strings.TrimSpace(waitText) != "" {
		diagnostics["wait"] = map[string]any{"kind": "text", "needle": waitText}
	} else if strings.TrimSpace(waitSelector) != "" {
		diagnostics["wait"] = map[string]any{"kind": "selector", "selector": waitSelector}
	}
	if verification != nil {
		diagnostics["verification"] = verification
	}
	return diagnostics
}
func (a *app) newFillCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var locatorOpts locatorActionOptions
	var trial bool
	var force bool
	cmd := &cobra.Command{
		Use:   "fill <selector-or-locator> <value>",
		Short: "Set the value of the first matching form control by CSS selector or strict locator",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "fill")
			if err != nil {
				return err
			}

			actionability, err := evaluateActionability(ctx, session, selector, "fill")
			if err != nil {
				return err
			}
			if actionability.Error != nil {
				return invalidSelectorError(selector, actionability.Error, "cdp fill input.email example@example.com --trial --json")
			}
			prepareActionability(&actionability, "fill", trial, force)
			if trial {
				result := fillResult{URL: actionability.URL, Title: actionability.Title, Selector: selector, Count: actionability.Count, Filled: false, Trial: true, Force: force, Value: args[1], Previous: ""}
				report := map[string]any{
					"ok":            actionability.Actionable,
					"action":        "trial",
					"target":        pageRow(target),
					"fill":          result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if !actionability.Actionable {
					return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("fill", selector, actionability), ExitCheckFailed, actionabilityRemediations("fill", args[0], selector, locatorOpts), report)
				}
				return a.render(ctx, fmt.Sprintf("trial\t%s\t%s", target.TargetID, selector), report)
			}
			if !actionability.Actionable {
				result := fillResult{URL: actionability.URL, Title: actionability.Title, Selector: selector, Count: actionability.Count, Filled: false, Force: force, Value: args[1], Previous: ""}
				report := map[string]any{
					"ok":            false,
					"action":        "blocked",
					"target":        pageRow(target),
					"fill":          result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("fill", selector, actionability), ExitCheckFailed, actionabilityRemediations("fill", args[0], selector, locatorOpts), report)
			}

			var result fillResult
			if err := evaluateJSONValue(ctx, session, fillExpression(selector, args[1]), "fill", &result); err != nil {
				return err
			}
			result.Force = force
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("fill %q: %s", selector, result.Error.Message),
					ExitUsage,
					[]string{"cdp fill input.email example@example.com --json"},
				)
			}
			if !result.Filled {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no editable element found for selector %q", selector),
					ExitUsage,
					[]string{"cdp fill #name Alice --json"},
				)
			}
			report := map[string]any{
				"ok":            true,
				"action":        "filled",
				"target":        pageRow(target),
				"fill":          result,
				"actionability": actionability,
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			return a.render(ctx, fmt.Sprintf("filled\t%s\t%s", target.TargetID, result.Selector), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&trial, "trial", false, "run locator resolution and actionability checks without changing the value")
	cmd.Flags().BoolVar(&force, "force", false, "skip non-essential fill actionability checks and record skipped checks in JSON")
	return cmd
}

func (a *app) newTypeCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var strategy string
	var locatorOpts locatorActionOptions
	var trial bool
	var force bool
	cmd := &cobra.Command{
		Use:   "type <selector-or-locator> <text>",
		Short: "Type text into the first matching editable element by CSS selector or strict locator",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			strategy = strings.ToLower(strings.TrimSpace(strategy))
			if strategy == "" {
				strategy = "auto"
			}
			if strategy != "auto" && strategy != "dom" && strategy != "insert-text" {
				return commandError("usage", "usage", "--strategy must be auto, dom, or insert-text", ExitUsage, []string{"cdp type '[contenteditable=true]' hello --strategy auto --json"})
			}
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "type")
			if err != nil {
				return err
			}

			actionability, err := evaluateActionability(ctx, session, selector, "type")
			if err != nil {
				return err
			}
			if actionability.Error != nil {
				return invalidSelectorError(selector, actionability.Error, "cdp type input[name='email'] user@example.com --trial --json")
			}
			prepareActionability(&actionability, "type", trial, force)
			if trial {
				result := typeResult{URL: actionability.URL, Title: actionability.Title, Selector: selector, Count: actionability.Count, Typing: false, Trial: true, Force: force, Typed: args[1], Previous: "", Strategy: strategy}
				report := map[string]any{
					"ok":            actionability.Actionable,
					"action":        "trial",
					"target":        pageRow(target),
					"type":          result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if !actionability.Actionable {
					return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("type", selector, actionability), ExitCheckFailed, actionabilityRemediations("type", args[0], selector, locatorOpts), report)
				}
				return a.render(ctx, fmt.Sprintf("trial\t%s\t%s", target.TargetID, selector), report)
			}
			if !actionability.Actionable {
				result := typeResult{URL: actionability.URL, Title: actionability.Title, Selector: selector, Count: actionability.Count, Typing: false, Force: force, Typed: args[1], Previous: "", Strategy: strategy}
				report := map[string]any{
					"ok":            false,
					"action":        "blocked",
					"target":        pageRow(target),
					"type":          result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("type", selector, actionability), ExitCheckFailed, actionabilityRemediations("type", args[0], selector, locatorOpts), report)
			}

			result, err := performTextInput(ctx, session, selector, args[1], strategy)
			if err != nil {
				return err
			}
			result.Force = force
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("type %q: %s", selector, result.Error.Message),
					ExitUsage,
					[]string{"cdp type input[name='email'] user@example.com --json"},
				)
			}
			if !result.Typing {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no editable element found for selector %q", selector),
					ExitUsage,
					[]string{"cdp type #name Alice --json"},
				)
			}
			report := map[string]any{
				"ok":            true,
				"action":        "typed",
				"target":        pageRow(target),
				"type":          result,
				"actionability": actionability,
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			return a.render(ctx, fmt.Sprintf("typed\t%s\t%s", target.TargetID, result.Selector), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&strategy, "strategy", "auto", "text input strategy: auto, dom, or insert-text")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&trial, "trial", false, "run locator resolution and actionability checks without typing text")
	cmd.Flags().BoolVar(&force, "force", false, "skip non-essential type actionability checks and record skipped checks in JSON")
	return cmd
}

func (a *app) newInsertTextCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "insert-text <selector> <text>",
		Short: "Insert text through the browser input pipeline",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			result, err := performTextInput(ctx, session, args[0], args[1], "insert-text")
			if err != nil {
				return err
			}
			if result.Error != nil {
				return commandError("invalid_selector", "usage", fmt.Sprintf("insert-text %q: %s", args[0], result.Error.Message), ExitUsage, []string{"cdp insert-text '[contenteditable=true]' hello --json"})
			}
			if !result.Typing {
				return commandError("invalid_selector", "usage", fmt.Sprintf("no editable element found for selector %q", args[0]), ExitUsage, []string{"cdp insert-text '[contenteditable=true]' hello --json"})
			}
			return a.render(ctx, fmt.Sprintf("inserted-text\t%s\t%s", target.TargetID, result.Selector), map[string]any{
				"ok":          true,
				"action":      "inserted_text",
				"target":      pageRow(target),
				"insert_text": result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func performTextInput(ctx context.Context, session *cdp.PageSession, selector, text, strategy string) (typeResult, error) {
	var result typeResult
	if err := evaluateJSONValue(ctx, session, typeExpression(selector, text, strategy), "type", &result); err != nil {
		return typeResult{}, err
	}
	if result.Error != nil || !result.Typing || result.Strategy != "insert-text" {
		return result, nil
	}
	params, _ := json.Marshal(map[string]any{"text": text})
	if _, err := session.Exec(ctx, "Input.insertText", params); err != nil {
		return typeResult{}, commandError("connection_failed", "connection", fmt.Sprintf("insert text target %s: %v", session.TargetID, err), ExitConnection, []string{"cdp protocol exec Input.insertText --params '{\"text\":\"hello\"}' --json"})
	}
	if err := evaluateJSONValue(ctx, session, insertedTextResultExpression(selector, text, result.Previous, result.Kind, result.Count), "insert-text", &result); err != nil {
		return typeResult{}, err
	}
	return result, nil
}

func (a *app) newPressCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var selector string
	var locatorOpts locatorActionOptions
	var trial bool
	cmd := &cobra.Command{
		Use:   "press <key> [selector-or-locator]",
		Short: "Press a key on the focused element or a resolved selector/locator",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}
			if selector != "" && len(args) == 2 {
				return commandError("conflicting_selector", "usage", "use either positional selector-or-locator or --selector, not both", ExitUsage, []string{"cdp press Enter 'Search' --by label --json", "cdp press Enter --selector 'input[name=\"q\"]' --json"})
			}
			if selector != "" && locatorOpts.By != "css" {
				return commandError("conflicting_selector", "usage", "--selector is CSS-only; pass the locator query positionally when using --by", ExitUsage, []string{"cdp press Enter 'Search' --by label --json"})
			}
			query := strings.TrimSpace(selector)
			if len(args) == 2 {
				query = strings.TrimSpace(args[1])
			}
			if locatorOpts.By != "css" && query == "" {
				return commandError("missing_locator_query", "usage", "press with --by requires a selector-or-locator argument after the key", ExitUsage, []string{"cdp press Enter 'Search' --by label --json"})
			}
			if trial && query == "" {
				return commandError("missing_selector", "usage", "press --trial requires a selector or locator query", ExitUsage, []string{"cdp press Enter 'Search' --by label --trial --json"})
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			resolvedSelector := query
			var locator *locatorFindResult
			if query != "" {
				resolvedSelector, locator, err = resolveActionSelector(ctx, session, query, locatorOpts, "press")
				if err != nil {
					return err
				}
			}

			var actionability *actionabilityResult
			if resolvedSelector != "" {
				checks, err := evaluateActionability(ctx, session, resolvedSelector, "press")
				if err != nil {
					return err
				}
				prepareActionability(&checks, "press", trial, false)
				actionability = &checks
				if !checks.Actionable {
					result := pressResult{
						URL:        checks.URL,
						Title:      checks.Title,
						Selector:   resolvedSelector,
						Key:        args[0],
						Count:      checks.Count,
						Dispatched: false,
						Trial:      trial,
					}
					report := map[string]any{
						"ok":                false,
						"action":            "trial",
						"target":            pageRow(target),
						"press":             result,
						"actionability":     checks,
						"resolved_selector": resolvedSelector,
					}
					if locator != nil {
						report["locator"] = locator
					}
					if !trial {
						report["action"] = "blocked"
					}
					return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("press", resolvedSelector, checks), ExitCheckFailed, actionabilityRemediations("press", query, resolvedSelector, locatorOpts), report)
				}
				if trial {
					result := pressResult{
						URL:        checks.URL,
						Title:      checks.Title,
						Selector:   resolvedSelector,
						Key:        args[0],
						Count:      checks.Count,
						Dispatched: false,
						Trial:      true,
					}
					report := map[string]any{
						"ok":                true,
						"action":            "trial",
						"target":            pageRow(target),
						"press":             result,
						"actionability":     checks,
						"resolved_selector": resolvedSelector,
					}
					if locator != nil {
						report["locator"] = locator
					}
					return a.render(ctx, fmt.Sprintf("press-trial\t%s\t%q", target.TargetID, result.Key), report)
				}
			}

			var result pressResult
			if err := evaluateJSONValue(ctx, session, pressExpression(args[0], resolvedSelector), "press", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("press %q: %s", args[0], result.Error.Message),
					ExitUsage,
					[]string{"cdp press Enter --selector 'input[name=\"q\"]' --json"},
				)
			}
			if !result.Dispatched {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no target found for keypress %q", args[0]),
					ExitUsage,
					[]string{"cdp press Enter --selector 'body' --json"},
				)
			}
			report := map[string]any{
				"ok":     true,
				"action": "pressed",
				"target": pageRow(target),
				"press":  result,
			}
			if actionability != nil {
				report["actionability"] = actionability
				report["resolved_selector"] = resolvedSelector
			}
			if locator != nil {
				report["locator"] = locator
			}
			return a.render(ctx, fmt.Sprintf("pressed\t%s\t%q", target.TargetID, result.Key), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&selector, "selector", "", "optional selector to focus before pressing the key")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&trial, "trial", false, "resolve selector/locator and report press target evidence without dispatching keyboard events")
	return cmd
}

func (a *app) newHoverCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var locatorOpts locatorActionOptions
	var trial bool
	var force bool
	cmd := &cobra.Command{
		Use:   "hover <selector-or-locator>",
		Short: "Dispatch pointer hover events over the first matching element by CSS selector or strict locator",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "hover")
			if err != nil {
				return err
			}
			actionability, err := evaluateActionability(ctx, session, selector, "hover")
			if err != nil {
				return err
			}
			if actionability.Error != nil {
				return invalidSelectorError(selector, actionability.Error, "cdp hover 'button.primary' --trial --json")
			}
			prepareActionability(&actionability, "hover", trial, force)
			var autoScroll *scrollResult
			if !trial && shouldAutoScrollBeforePointerAction("hover", actionability) {
				scrolled, err := autoScrollPointerTarget(ctx, session, selector)
				if err != nil {
					return err
				}
				autoScroll = &scrolled
				if scrolled.Error == nil && scrolled.After.InViewport {
					actionability, err = evaluateActionability(ctx, session, selector, "hover")
					if err != nil {
						return err
					}
					if actionability.Error != nil {
						return invalidSelectorError(selector, actionability.Error, "cdp hover 'button.primary' --trial --json")
					}
					prepareActionability(&actionability, "hover", trial, force)
				}
			}
			if trial {
				result := hoverResult{URL: actionability.URL, Title: actionability.Title, Selector: selector, Count: actionability.Count, Hovered: false, Trial: true, Force: force, X: actionability.Point.X, Y: actionability.Point.Y}
				report := map[string]any{
					"ok":            actionability.Actionable,
					"action":        "trial",
					"target":        pageRow(target),
					"hover":         result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if !actionability.Actionable {
					return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("hover", selector, actionability), ExitCheckFailed, actionabilityRemediations("hover", args[0], selector, locatorOpts), report)
				}
				return a.render(ctx, fmt.Sprintf("trial\t%s\t%s", target.TargetID, selector), report)
			}
			if !actionability.Actionable {
				result := hoverResult{URL: actionability.URL, Title: actionability.Title, Selector: selector, Count: actionability.Count, Hovered: false, Force: force, X: actionability.Point.X, Y: actionability.Point.Y}
				report := map[string]any{
					"ok":            false,
					"action":        "blocked",
					"target":        pageRow(target),
					"hover":         result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if autoScroll != nil {
					report["auto_scroll"] = autoScroll
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("hover", selector, actionability), ExitCheckFailed, actionabilityRemediations("hover", args[0], selector, locatorOpts), report)
			}

			var result hoverResult
			if err := evaluateJSONValue(ctx, session, hoverExpression(selector), "hover", &result); err != nil {
				return err
			}
			result.Force = force
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("hover %q: %s", selector, result.Error.Message),
					ExitUsage,
					[]string{"cdp hover 'button.primary' --json"},
				)
			}
			if !result.Hovered {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no matching element found for selector %q", args[0]),
					ExitUsage,
					[]string{"cdp hover 'button.primary' --json"},
				)
			}
			report := map[string]any{
				"ok":            true,
				"action":        "hovered",
				"target":        pageRow(target),
				"hover":         result,
				"actionability": actionability,
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			if autoScroll != nil {
				report["auto_scroll"] = autoScroll
			}
			return a.render(ctx, fmt.Sprintf("hovered\t%s\t%s", target.TargetID, result.Selector), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&trial, "trial", false, "run locator resolution and actionability checks without dispatching hover events")
	cmd.Flags().BoolVar(&force, "force", false, "skip non-essential hover actionability checks and record skipped checks in JSON")
	return cmd
}

func (a *app) newDragCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var locatorOpts locatorActionOptions
	var trial bool
	var force bool
	cmd := &cobra.Command{
		Use:   "drag <selector-or-locator> <dx> <dy>",
		Short: "Drag the first matching element by a delta using CSS selector or strict locator",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			dx, err := strconv.Atoi(strings.TrimSpace(args[1]))
			if err != nil {
				return commandError("invalid_argument", "usage", "dx must be an integer", ExitUsage, []string{"cdp drag '.node' 10 20 --json"})
			}
			dy, err := strconv.Atoi(strings.TrimSpace(args[2]))
			if err != nil {
				return commandError("invalid_argument", "usage", "dy must be an integer", ExitUsage, []string{"cdp drag '.node' 10 20 --json"})
			}

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "drag")
			if err != nil {
				return err
			}
			actionability, err := evaluateActionability(ctx, session, selector, "drag")
			if err != nil {
				return err
			}
			if actionability.Error != nil {
				return invalidSelectorError(selector, actionability.Error, "cdp drag '#drag-me' 10 20 --trial --json")
			}
			prepareActionability(&actionability, "drag", trial, force)
			var autoScroll *scrollResult
			if !trial && shouldAutoScrollBeforePointerAction("drag", actionability) {
				scrolled, err := autoScrollPointerTarget(ctx, session, selector)
				if err != nil {
					return err
				}
				autoScroll = &scrolled
				if scrolled.Error == nil && scrolled.After.InViewport {
					actionability, err = evaluateActionability(ctx, session, selector, "drag")
					if err != nil {
						return err
					}
					if actionability.Error != nil {
						return invalidSelectorError(selector, actionability.Error, "cdp drag '#drag-me' 10 20 --trial --json")
					}
					prepareActionability(&actionability, "drag", trial, force)
				}
			}
			if trial {
				result := dragResult{URL: actionability.URL, Title: actionability.Title, Selector: selector, Count: actionability.Count, Dragged: false, Trial: true, Force: force, DeltaX: dx, DeltaY: dy, StartX: actionability.Point.X, StartY: actionability.Point.Y, EndX: actionability.Point.X + float64(dx), EndY: actionability.Point.Y + float64(dy)}
				report := map[string]any{
					"ok":            actionability.Actionable,
					"action":        "trial",
					"target":        pageRow(target),
					"drag":          result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if !actionability.Actionable {
					return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("drag", selector, actionability), ExitCheckFailed, actionabilityRemediations("drag", args[0], selector, locatorOpts), report)
				}
				return a.render(ctx, fmt.Sprintf("trial\t%s\t%s", target.TargetID, selector), report)
			}
			if !actionability.Actionable {
				result := dragResult{URL: actionability.URL, Title: actionability.Title, Selector: selector, Count: actionability.Count, Dragged: false, Force: force, DeltaX: dx, DeltaY: dy, StartX: actionability.Point.X, StartY: actionability.Point.Y, EndX: actionability.Point.X + float64(dx), EndY: actionability.Point.Y + float64(dy)}
				report := map[string]any{
					"ok":            false,
					"action":        "blocked",
					"target":        pageRow(target),
					"drag":          result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if autoScroll != nil {
					report["auto_scroll"] = autoScroll
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("drag", selector, actionability), ExitCheckFailed, actionabilityRemediations("drag", args[0], selector, locatorOpts), report)
			}

			var result dragResult
			if err := evaluateJSONValue(ctx, session, dragExpression(selector, dx, dy), "drag", &result); err != nil {
				return err
			}
			result.Force = force
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("drag %q: %s", selector, result.Error.Message),
					ExitUsage,
					[]string{"cdp drag '#drag-me' 10 20 --json"},
				)
			}
			if !result.Dragged {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no matching element found for selector %q", args[0]),
					ExitUsage,
					[]string{"cdp drag '#drag-me' 10 20 --json"},
				)
			}
			report := map[string]any{
				"ok":            true,
				"action":        "dragged",
				"target":        pageRow(target),
				"drag":          result,
				"actionability": actionability,
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			if autoScroll != nil {
				report["auto_scroll"] = autoScroll
			}
			return a.render(ctx, fmt.Sprintf("dragged\t%s\t%s", target.TargetID, result.Selector), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&trial, "trial", false, "run locator resolution and actionability checks without dispatching drag events")
	cmd.Flags().BoolVar(&force, "force", false, "skip non-essential drag actionability checks and record skipped checks in JSON")
	return cmd
}
