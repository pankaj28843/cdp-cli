package cli

import (
	"context"
	"encoding/json"
	"fmt"
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
