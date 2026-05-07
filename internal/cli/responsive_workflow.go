package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type responsiveAuditOptions struct {
	Viewports string
	Include   string
	OutDir    string
	Wait      time.Duration
	Limit     int
}

func runResponsiveAuditWorkflow(ctx context.Context, a *app, rawURL string, opts responsiveAuditOptions) error {
	client, closeClient, err := a.browserEventCDPClient(ctx)
	if err != nil {
		return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
	}
	defer closeClient(ctx)
	targetID, err := a.createWorkflowPageTarget(ctx, client, "about:blank", "responsive-audit")
	if err != nil {
		return err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, nil)
	if err != nil {
		return commandError("connection_failed", "connection", fmt.Sprintf("attach target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	defer session.Close(ctx)
	defer func() {
		_ = client.CallSession(ctx, session.SessionID, "Emulation.clearDeviceMetricsOverride", map[string]any{}, nil)
	}()

	presets, err := responsiveViewportPresets(opts.Viewports)
	if err != nil {
		return err
	}
	includeSet := parseCSVSet(opts.Include)
	if len(includeSet) == 0 || includeSet["all"] {
		includeSet = parseCSVSet("console,network,layout,screenshot,a11y")
	}
	outDir := strings.TrimSpace(opts.OutDir)
	if outDir == "" {
		outDir = filepath.Join("tmp", "responsive-audit")
	}
	results := []map[string]any{}
	artifacts := []map[string]any{}
	for _, vp := range presets {
		viewportReport, viewportArtifacts, err := collectResponsiveViewport(ctx, client, session, rawURL, vp, includeSet, outDir, opts.Wait, opts.Limit)
		if err != nil {
			return err
		}
		results = append(results, viewportReport)
		artifacts = append(artifacts, viewportArtifacts...)
	}
	_ = client.CallSession(ctx, session.SessionID, "Emulation.clearDeviceMetricsOverride", map[string]any{}, nil)
	report := map[string]any{"ok": true, "target": pageRow(cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}), "workflow": map[string]any{"name": "responsive-audit", "url": rawURL, "viewports": setKeys(parseCSVSet(opts.Viewports)), "include": setKeys(includeSet), "wait": durationString(opts.Wait), "limit": opts.Limit, "out_dir": outDir, "cleanup": "emulation-cleared"}, "results": results, "artifacts": artifacts}
	return a.render(ctx, fmt.Sprintf("responsive-audit\t%d viewports", len(results)), report)
}

func collectResponsiveViewport(ctx context.Context, client browserEventClient, session *cdp.PageSession, rawURL string, vp responsiveViewport, includeSet map[string]bool, outDir string, wait time.Duration, limit int) (map[string]any, []map[string]any, error) {
	_ = client.CallSession(ctx, session.SessionID, "Emulation.clearDeviceMetricsOverride", map[string]any{}, nil)
	params := map[string]any{"width": vp.Width, "height": vp.Height, "deviceScaleFactor": vp.DeviceScaleFactor, "mobile": vp.Mobile}
	if err := client.CallSession(ctx, session.SessionID, "Emulation.setDeviceMetricsOverride", params, nil); err != nil {
		return nil, nil, commandError("connection_failed", "connection", fmt.Sprintf("set viewport %s: %v", vp.Name, err), ExitConnection, []string{"cdp emulate viewport --preset mobile --json"})
	}
	pageLoadSet := pageLoadIncludeSet("console,network,performance,navigation")
	collectorErrors := enablePageLoadCollectors(ctx, client, session.SessionID, pageLoadSet)
	_, navErr := session.Navigate(ctx, rawURL)
	requests, requestsTruncated, messages, messagesTruncated, collectErr := collectPageLoadEvents(ctx, client, session.SessionID, wait, limit, pageLoadSet)
	if navErr != nil {
		collectorErrors = append(collectorErrors, collectorError("navigation", navErr))
	}
	if collectErr != nil {
		collectorErrors = append(collectorErrors, collectorError("events", collectErr))
	}
	viewportReport := map[string]any{"name": vp.Name, "width": vp.Width, "height": vp.Height, "mobile": vp.Mobile, "device_scale_factor": vp.DeviceScaleFactor, "requests": requests, "messages": messages, "failed_request_count": countFailedRequests(requests), "console_issue_count": countConsoleIssues(messages), "requests_truncated": requestsTruncated, "messages_truncated": messagesTruncated, "collector_errors": collectorErrors}
	artifacts := []map[string]any{}
	if includeSet["layout"] {
		var overflow layoutOverflowResult
		if err := evaluateJSONValue(ctx, session, layoutOverflowExpression("body *", limit), "responsive-audit layout", &overflow); err == nil {
			viewportReport["layout"] = map[string]any{"overflow_count": len(overflow.Items), "items": overflow.Items}
		} else {
			collectorErrors = append(collectorErrors, collectorError("layout", err))
		}
	}
	if includeSet["a11y"] {
		var signals workflowA11ySignals
		if err := evaluateJSONValue(ctx, session, workflowA11yExpression(), "responsive-audit a11y", &signals); err == nil {
			viewportReport["a11y"] = signals
		} else {
			collectorErrors = append(collectorErrors, collectorError("a11y", err))
		}
	}
	if includeSet["screenshot"] {
		shot, err := session.CaptureScreenshot(ctx, cdp.ScreenshotOptions{Format: "png", FullPage: true})
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("screenshot", err))
		} else {
			path := filepath.Join(outDir, vp.Name+".png")
			written, err := writeArtifactFile(path, shot.Data)
			if err != nil {
				return nil, nil, err
			}
			artifact := map[string]any{"type": "responsive-audit-screenshot", "viewport": vp.Name, "path": written, "bytes": len(shot.Data)}
			viewportReport["screenshot"] = artifact
			artifacts = append(artifacts, artifact)
		}
	}
	return viewportReport, artifacts, nil
}
