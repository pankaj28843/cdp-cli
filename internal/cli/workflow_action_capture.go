package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

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
	var evidenceOutDir string
	var beforeScreenshot string
	var afterScreenshot string
	var limit int
	var a11yDepth int
	var a11yLimit int
	var storageDiff bool
	cmd := &cobra.Command{
		Use:   "action-capture",
		Short: "Capture browser evidence around one declared page action",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if waitBefore < 0 || waitAfter < 0 || limit < 0 || a11yDepth < 0 || a11yLimit < 0 {
				return commandError("usage", "usage", "--wait-before, --wait-after, --limit, --a11y-depth, and --a11y-limit must be non-negative", ExitUsage, []string{"cdp workflow action-capture --action press:Enter --json"})
			}
			evidenceOutDir = strings.TrimSpace(evidenceOutDir)
			includeSet := parseCSVSet(include)
			if len(includeSet) == 0 || includeSet["all"] {
				includeSet = parseCSVSet("network,websocket,console,dom,text")
			}
			if storageDiff {
				includeSet["storage-diff"] = true
			}
			if invalid := invalidActionCaptureIncludes(includeSet); len(invalid) > 0 {
				return commandError("usage", "usage", fmt.Sprintf("unknown action-capture include %q", invalid[0]), ExitUsage, []string{"cdp workflow action-capture --include network,websocket,console,dom,text,a11y --json"})
			}
			if includeSet["a11y"] && evidenceOutDir == "" {
				return commandError("usage", "usage", "--include a11y requires --evidence-out-dir because accessibility snapshots may include page content", ExitUsage, []string{"cdp workflow action-capture --action click:button --include dom,text,a11y --evidence-out-dir tmp/action-capture --json"})
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

			evidenceReport := map[string]any{}
			if evidenceOutDir != "" {
				beforeEvidence, beforeArtifacts, beforeErrors := collectActionCaptureEvidence(ctx, session, includeSet, evidenceOutDir, "before", a11yDepth, a11yLimit)
				evidenceReport["before"] = beforeEvidence
				artifacts = append(artifacts, beforeArtifacts...)
				collectorErrors = append(collectorErrors, beforeErrors...)
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
			if evidenceOutDir != "" {
				afterEvidence, afterArtifacts, afterErrors := collectActionCaptureEvidence(ctx, session, includeSet, evidenceOutDir, "after", a11yDepth, a11yLimit)
				evidenceReport["after"] = afterEvidence
				artifacts = append(artifacts, afterArtifacts...)
				collectorErrors = append(collectorErrors, afterErrors...)
				evidenceReport["artifact_count"] = len(actionCaptureEvidenceArtifacts(evidenceReport))
			}

			workflowReport := map[string]any{
				"name":               "action-capture",
				"include":            setKeys(includeSet),
				"wait_before":        durationString(waitBefore),
				"wait_after":         durationString(waitAfter),
				"before_at":          beforeAt,
				"action_started_at":  actionStarted,
				"action_finished_at": actionFinished,
				"after_at":           afterAt,
				"collector_errors":   collectorErrors,
			}
			report := map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"workflow": workflowReport,
				"action":   actionResult,
			}
			if evidenceOutDir != "" {
				report["evidence"] = evidenceReport
				report["local_artifact_warning"] = "action capture artifacts may include local page content, headers, tokens, and message data; keep these artifacts local"
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
					if evidenceOutDir != "" {
						eventEvidence, eventArtifacts, eventErrors := collectActionCaptureEventEvidence(evidenceOutDir, actionStarted, actionFinished, includeSet, limit, requests, websockets, messages)
						if len(eventEvidence) > 0 {
							evidenceReport["events"] = eventEvidence
						}
						artifacts = append(artifacts, eventArtifacts...)
						collectorErrors = append(collectorErrors, eventErrors...)
						evidenceReport["artifact_count"] = len(actionCaptureEvidenceArtifacts(evidenceReport))
						report["evidence"] = evidenceReport
						if len(artifacts) > 0 {
							report["artifacts"] = artifacts
						}
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
			workflowReport["collector_errors"] = collectorErrors
			if evidenceOutDir != "" {
				manifestArtifact, err := writeActionCaptureManifestArtifact(evidenceOutDir, parsedAction, report, artifacts, collectorErrors)
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("manifest_artifact", err))
					workflowReport["collector_errors"] = collectorErrors
				} else {
					evidenceReport["manifest"] = map[string]any{
						"artifact":                  manifestArtifact,
						"referenced_artifact_count": len(artifacts),
						"collector_error_count":     len(collectorErrors),
					}
					artifacts = append(artifacts, manifestArtifact)
					evidenceReport["artifact_count"] = len(actionCaptureEvidenceArtifacts(evidenceReport))
					report["evidence"] = evidenceReport
					report["artifacts"] = artifacts
				}
			}
			if strings.TrimSpace(outPath) != "" {
				report["local_artifact_warning"] = "action capture artifacts may include local page content, headers, tokens, and message data; keep these artifacts local"
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
	cmd.Flags().StringVar(&include, "include", "network,websocket,console,dom,text", "comma-separated collectors: network,websocket,console,dom,text,a11y,all")
	cmd.Flags().StringVar(&action, "action", "", "action shorthand: click:<selector>, type:<text>, insert-text:<text>, or press:<key>")
	cmd.Flags().StringVar(&actionJSON, "action-json", "", "JSON action object with type, selector, text/value, or key")
	cmd.Flags().StringVar(&selector, "selector", "", "selector for click/type/insert-text or optional press focus target")
	cmd.Flags().DurationVar(&waitBefore, "wait-before", time.Second, "delay after arming collectors and before action")
	cmd.Flags().DurationVar(&waitAfter, "wait-after", 5*time.Second, "delay after action before collecting evidence")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the unified JSON artifact")
	cmd.Flags().StringVar(&evidenceOutDir, "evidence-out-dir", "", "optional directory for before/after text, DOM, accessibility, action event, and manifest artifacts")
	cmd.Flags().StringVar(&beforeScreenshot, "before-screenshot", "", "optional before-action screenshot path")
	cmd.Flags().StringVar(&afterScreenshot, "after-screenshot", "", "optional after-action screenshot path")
	cmd.Flags().IntVar(&limit, "limit", 500, "maximum events per collector; use 0 for no limit")
	cmd.Flags().IntVar(&a11yDepth, "a11y-depth", 4, "maximum accessibility tree depth for --include a11y evidence")
	cmd.Flags().IntVar(&a11yLimit, "a11y-limit", 100, "maximum accessibility nodes per before/after --include a11y artifact")
	cmd.Flags().BoolVar(&storageDiff, "storage-diff", false, "include before/after storage diff evidence")
	return cmd
}

func invalidActionCaptureIncludes(includeSet map[string]bool) []string {
	valid := parseCSVSet("network,websocket,console,dom,text,a11y,storage-diff,all")
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

func collectActionCaptureEvidence(ctx context.Context, session *cdp.PageSession, includeSet map[string]bool, outDir, phase string, a11yDepth, a11yLimit int) (map[string]any, []map[string]any, []map[string]string) {
	capturedAt := time.Now().UTC().Format(time.RFC3339Nano)
	evidence := map[string]any{
		"at":      capturedAt,
		"out_dir": outDir,
	}
	artifacts := []map[string]any{}
	collectorErrors := []map[string]string{}
	if includeSet["text"] {
		var text textResult
		if err := evaluateJSONValue(ctx, session, textExpression("body", 1, 0), "action-capture "+phase+" text", &text); err != nil {
			collectorErrors = append(collectorErrors, collectorError(phase+"_text", err))
		} else {
			artifact, err := writeActionCaptureEvidenceArtifact(outDir, phase, "text", map[string]any{
				"phase":       phase,
				"captured_at": capturedAt,
				"collector":   "text",
				"text":        text,
			})
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError(phase+"_text_artifact", err))
			} else {
				evidence["text"] = map[string]any{
					"selector": text.Selector,
					"count":    text.Count,
					"url":      text.URL,
					"title":    text.Title,
					"artifact": artifact,
				}
				artifacts = append(artifacts, artifact)
			}
		}
	}
	if includeSet["dom"] {
		var dom htmlResult
		if err := evaluateJSONValue(ctx, session, htmlExpression("body", 1, 20000), "action-capture "+phase+" dom", &dom); err != nil {
			collectorErrors = append(collectorErrors, collectorError(phase+"_dom", err))
		} else {
			artifact, err := writeActionCaptureEvidenceArtifact(outDir, phase, "dom", map[string]any{
				"phase":       phase,
				"captured_at": capturedAt,
				"collector":   "dom",
				"dom":         dom,
			})
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError(phase+"_dom_artifact", err))
			} else {
				evidence["dom"] = map[string]any{
					"selector": dom.Selector,
					"count":    dom.Count,
					"url":      dom.URL,
					"title":    dom.Title,
					"artifact": artifact,
				}
				artifacts = append(artifacts, artifact)
			}
		}
	}
	if includeSet["a11y"] {
		nodes, truncated, err := collectA11yNodes(ctx, session, a11yDepth, a11yLimit, false)
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError(phase+"_a11y", err))
		} else {
			artifact, err := writeActionCaptureEvidenceArtifact(outDir, phase, "a11y", map[string]any{
				"phase":       phase,
				"captured_at": capturedAt,
				"collector":   "a11y",
				"depth":       a11yDepth,
				"limit":       a11yLimit,
				"count":       len(nodes),
				"truncated":   truncated,
				"nodes":       nodes,
			})
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError(phase+"_a11y_artifact", err))
			} else {
				evidence["a11y"] = map[string]any{
					"count":     len(nodes),
					"truncated": truncated,
					"depth":     a11yDepth,
					"limit":     a11yLimit,
					"artifact":  artifact,
				}
				artifacts = append(artifacts, artifact)
			}
		}
	}
	return evidence, artifacts, collectorErrors
}

func writeActionCaptureEvidenceArtifact(outDir, phase, collector string, payload any) (map[string]any, error) {
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, commandError("internal", "internal", fmt.Sprintf("marshal action capture %s %s evidence: %v", phase, collector, err), ExitInternal, []string{"cdp workflow action-capture --json"})
	}
	path := filepath.Join(outDir, fmt.Sprintf("action-capture.%s.%s.json", phase, collector))
	writtenPath, err := writeArtifactFile(path, append(raw, '\n'))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"type":      "workflow-action-capture-" + phase + "-" + collector,
		"path":      writtenPath,
		"bytes":     len(raw) + 1,
		"phase":     phase,
		"collector": collector,
	}, nil
}

func writeActionCaptureManifestArtifact(outDir string, action actionCaptureAction, report map[string]any, artifacts []map[string]any, collectorErrors []map[string]string) (map[string]any, error) {
	payload := map[string]any{
		"captured_at":      time.Now().UTC().Format(time.RFC3339Nano),
		"workflow":         report["workflow"],
		"target":           report["target"],
		"action":           actionCaptureManifestAction(action),
		"evidence":         report["evidence"],
		"artifacts":        artifacts,
		"collector_errors": collectorErrors,
		"counts":           actionCaptureManifestCounts(report, artifacts, collectorErrors),
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, commandError("internal", "internal", fmt.Sprintf("marshal action capture manifest: %v", err), ExitInternal, []string{"cdp workflow action-capture --json"})
	}
	path := filepath.Join(outDir, "action-capture.manifest.json")
	writtenPath, err := writeArtifactFile(path, append(raw, '\n'))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"type":      "workflow-action-capture-manifest",
		"path":      writtenPath,
		"bytes":     len(raw) + 1,
		"collector": "manifest",
	}, nil
}

func actionCaptureManifestAction(action actionCaptureAction) map[string]any {
	summary := map[string]any{"type": action.Type}
	if strings.TrimSpace(action.Selector) != "" {
		summary["selector"] = action.Selector
	}
	if strings.TrimSpace(action.Key) != "" {
		summary["key"] = action.Key
	}
	if action.Text != "" {
		summary["text_length"] = len([]rune(action.Text))
	}
	return summary
}

func actionCaptureManifestCounts(report map[string]any, artifacts []map[string]any, collectorErrors []map[string]string) map[string]any {
	counts := map[string]any{
		"referenced_artifacts": len(artifacts),
		"collector_errors":     len(collectorErrors),
	}
	if count := actionCaptureSliceLen(report["requests"]); count >= 0 {
		counts["requests"] = count
	}
	if count := actionCaptureSliceLen(report["websockets"]); count >= 0 {
		counts["websockets"] = count
	}
	if count := actionCaptureSliceLen(report["messages"]); count >= 0 {
		counts["messages"] = count
	}
	counts["has_storage_diff"] = report["storage_diff"] != nil
	return counts
}

func actionCaptureSliceLen(value any) int {
	switch v := value.(type) {
	case []networkCaptureRecord:
		return len(v)
	case []consoleMessage:
		return len(v)
	case []map[string]any:
		return len(v)
	default:
		return -1
	}
}

func collectActionCaptureEventEvidence(outDir, actionStarted, actionFinished string, includeSet map[string]bool, limit int, requests, websockets []networkCaptureRecord, messages []consoleMessage) (map[string]any, []map[string]any, []map[string]string) {
	capturedAt := time.Now().UTC().Format(time.RFC3339Nano)
	window := map[string]any{
		"action_started_at":  actionStarted,
		"action_finished_at": actionFinished,
	}
	evidence := map[string]any{
		"at":      capturedAt,
		"out_dir": outDir,
		"window":  window,
	}
	artifacts := []map[string]any{}
	collectorErrors := []map[string]string{}
	if includeSet["network"] {
		artifact, err := writeActionCaptureEvidenceArtifact(outDir, "action", "network", map[string]any{
			"phase":       "action",
			"captured_at": capturedAt,
			"collector":   "network",
			"window":      window,
			"limit":       limit,
			"count":       len(requests),
			"requests":    requests,
		})
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("action_network_artifact", err))
		} else {
			evidence["network"] = map[string]any{"count": len(requests), "artifact": artifact}
			artifacts = append(artifacts, artifact)
		}
	}
	if includeSet["websocket"] {
		artifact, err := writeActionCaptureEvidenceArtifact(outDir, "action", "websockets", map[string]any{
			"phase":       "action",
			"captured_at": capturedAt,
			"collector":   "websockets",
			"window":      window,
			"limit":       limit,
			"count":       len(websockets),
			"websockets":  websockets,
		})
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("action_websockets_artifact", err))
		} else {
			evidence["websockets"] = map[string]any{"count": len(websockets), "artifact": artifact}
			artifacts = append(artifacts, artifact)
		}
	}
	if includeSet["console"] {
		artifact, err := writeActionCaptureEvidenceArtifact(outDir, "action", "console", map[string]any{
			"phase":       "action",
			"captured_at": capturedAt,
			"collector":   "console",
			"window":      window,
			"limit":       limit,
			"count":       len(messages),
			"messages":    messages,
		})
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("action_console_artifact", err))
		} else {
			evidence["console"] = map[string]any{"count": len(messages), "artifact": artifact}
			artifacts = append(artifacts, artifact)
		}
	}
	return evidence, artifacts, collectorErrors
}

func actionCaptureEvidenceArtifacts(evidence map[string]any) []map[string]any {
	artifacts := []map[string]any{}
	for _, phase := range []string{"before", "after"} {
		phaseEvidence, ok := evidence[phase].(map[string]any)
		if !ok {
			continue
		}
		for _, collector := range []string{"text", "dom", "a11y"} {
			collectorEvidence, ok := phaseEvidence[collector].(map[string]any)
			if !ok {
				continue
			}
			artifact, ok := collectorEvidence["artifact"].(map[string]any)
			if !ok {
				continue
			}
			artifacts = append(artifacts, artifact)
		}
	}
	eventsEvidence, ok := evidence["events"].(map[string]any)
	if ok {
		for _, collector := range []string{"network", "websockets", "console"} {
			collectorEvidence, ok := eventsEvidence[collector].(map[string]any)
			if !ok {
				continue
			}
			artifact, ok := collectorEvidence["artifact"].(map[string]any)
			if !ok {
				continue
			}
			artifacts = append(artifacts, artifact)
		}
	}
	if manifestEvidence, ok := evidence["manifest"].(map[string]any); ok {
		artifact, ok := manifestEvidence["artifact"].(map[string]any)
		if !ok {
			return artifacts
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts
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
