package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/spf13/cobra"
)

func (a *app) newNetworkCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var wait time.Duration
	var limit int
	var failedOnly bool
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Inspect network requests from a page target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if wait < 0 {
				return commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp network --wait 2s --json"})
			}
			if limit < 0 {
				return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp network --limit 50 --json"})
			}

			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			requests, truncated, err := collectNetworkRequests(ctx, client, session.SessionID, wait, limit, failedOnly)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture network target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			lines := networkRequestLines(requests)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"requests": requests,
				"network": map[string]any{
					"count":       len(requests),
					"wait":        durationString(wait),
					"limit":       limit,
					"truncated":   truncated,
					"failed_only": failedOnly,
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&wait, "wait", time.Second, "how long to collect network events after attaching")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum number of requests to return; use 0 for no limit")
	cmd.Flags().BoolVar(&failedOnly, "failed", false, "only return failed requests and HTTP 4xx/5xx responses")
	cmd.AddCommand(a.newNetworkCaptureCommand())
	cmd.AddCommand(a.newNetworkWebSocketCommand())
	cmd.AddCommand(a.newNetworkBlockCommand())
	cmd.AddCommand(a.newNetworkMockCommand())
	return cmd
}

func (a *app) newNetworkWebSocketCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var wait time.Duration
	var limit int
	var outPath string
	var includePayloads bool
	var payloadLimit int
	var redact string
	cmd := &cobra.Command{
		Use:   "websocket",
		Short: "Capture WebSocket lifecycle events and frames from a page target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || limit < 0 || payloadLimit <= 0 {
				return commandError("usage", "usage", "--wait and --limit must be non-negative and --payload-limit must be positive", ExitUsage, []string{"cdp network websocket --wait 20s --payload-limit 262144 --json"})
			}
			redact = strings.ToLower(strings.TrimSpace(redact))
			if redact == "" {
				redact = "none"
			}
			if redact != "none" && redact != "safe" && redact != "headers" {
				return commandError("usage", "usage", "--redact must be none, safe, or headers", ExitUsage, []string{"cdp network websocket --redact safe --json"})
			}
			fallback := wait + 10*time.Second
			if fallback < 10*time.Second {
				fallback = 10 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			records, truncated, collectorErrors, err := collectNetworkCapture(ctx, client, session.SessionID, networkCaptureOptions{
				Wait:                  wait,
				Limit:                 limit,
				IncludeHeaders:        true,
				IncludeInitiators:     true,
				IncludeWebSockets:     true,
				WebSocketPayloads:     includePayloads,
				WebSocketPayloadLimit: payloadLimit,
			})
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture websocket target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			websockets := filterWebSocketRecords(records)
			redactor := artifacts.NewRedactor(redact)
			applyNetworkCaptureRedaction(websockets, redactor)
			artifactWarning := "websocket capture may include cookies, authorization headers, tokens, and frame payloads; keep this artifact local"
			artifactSafety := redactor.Metadata(strings.TrimSpace(outPath) != "", artifactWarning)
			capture := map[string]any{
				"count":            len(websockets),
				"wait":             durationString(wait),
				"limit":            limit,
				"truncated":        truncated,
				"include_payloads": includePayloads,
				"payload_limit":    payloadLimit,
				"redact":           redact,
				"collector_errors": collectorErrors,
				"artifact_safety":  artifactSafety,
			}
			if strings.TrimSpace(outPath) != "" && redact == "none" {
				capture["local_artifact_warning"] = artifactWarning
			}
			report := map[string]any{
				"ok":         true,
				"target":     pageRow(target),
				"websockets": websockets,
				"capture":    capture,
			}
			if strings.TrimSpace(outPath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal websocket capture report: %v", err), ExitInternal, []string{"cdp network websocket --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "network-websocket", "path": writtenPath, "bytes": len(b) + 1, "safety": artifactSafety}
				report["artifacts"] = []map[string]any{{"type": "network-websocket", "path": writtenPath}}
			}
			human := fmt.Sprintf("websocket-capture\t%d sockets", len(websockets))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect WebSocket events after attaching")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum WebSocket records to return; use 0 for no limit")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the JSON WebSocket capture artifact")
	cmd.Flags().BoolVar(&includePayloads, "include-payloads", false, "include WebSocket frame payload text")
	cmd.Flags().IntVar(&payloadLimit, "payload-limit", 256*1024, "positive maximum WebSocket frame payload bytes to include")
	cmd.Flags().StringVar(&redact, "redact", "none", "redaction preset for output and artifacts: none, safe, or headers")
	return cmd
}

func filterWebSocketRecords(records []networkCaptureRecord) []networkCaptureRecord {
	websockets := make([]networkCaptureRecord, 0, len(records))
	for _, record := range records {
		if record.WebSocket != nil {
			websockets = append(websockets, record)
		}
	}
	return websockets
}

func (a *app) newNetworkCaptureCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var wait time.Duration
	var limit int
	var outPath string
	var harOutPath string
	var includeHeaders bool
	var includeInitiators bool
	var includeTiming bool
	var includePostData bool
	var includeBodies string
	var bodyLimit int
	var bodyOutDir string
	var bodyArtifactLimit int
	var includeWebSockets bool
	var includeWebSocketPayloads bool
	var websocketPayloadLimit int
	var redact string
	var reload bool
	var ignoreCache bool
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Capture full local network metadata from a page target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || limit < 0 || bodyLimit <= 0 || websocketPayloadLimit <= 0 || bodyArtifactLimit < 1 || bodyArtifactLimit > 100 {
				return commandError("usage", "usage", "--wait and --limit must be non-negative; body and WebSocket byte limits must be positive; --body-artifact-limit must be between 1 and 100", ExitUsage, []string{"cdp network capture --wait 10s --body-limit 262144 --json"})
			}
			redact = strings.ToLower(strings.TrimSpace(redact))
			if redact == "" {
				redact = "none"
			}
			if redact != "none" && redact != "safe" && redact != "headers" {
				return commandError("usage", "usage", "--redact must be none, safe, or headers", ExitUsage, []string{"cdp network capture --redact safe --json"})
			}
			rawBodyKinds := parseCSVSet(includeBodies)
			if invalid := invalidBodyKinds(rawBodyKinds); len(invalid) > 0 {
				return commandError("usage", "usage", "--include-bodies only accepts json, text, base64, all, or none", ExitUsage, []string{"cdp network capture --include-bodies json,text --json"})
			}
			bodyKinds := parseBodyKinds(includeBodies)
			fallback := wait + 10*time.Second
			if fallback < 10*time.Second {
				fallback = 10 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			trigger := "observe"
			var afterEnable func() error
			if reload {
				trigger = "reload"
				afterEnable = func() error {
					return session.Reload(ctx, ignoreCache)
				}
			}
			records, truncated, collectorErrors, err := collectNetworkCapture(ctx, client, session.SessionID, networkCaptureOptions{
				Wait:                  wait,
				Limit:                 limit,
				IncludeHeaders:        includeHeaders,
				IncludeInitiators:     includeInitiators,
				IncludeTiming:         includeTiming,
				IncludePostData:       includePostData,
				BodyKinds:             bodyKinds,
				BodyLimit:             bodyLimit,
				IncludeWebSockets:     includeWebSockets,
				WebSocketPayloads:     includeWebSocketPayloads,
				WebSocketPayloadLimit: websocketPayloadLimit,
				AfterEnable:           afterEnable,
			})
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture full network target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			redactor := artifacts.NewRedactor(redact)
			applyNetworkCaptureRedaction(records, redactor)
			artifactWarning := "network capture may include cookies, authorization headers, tokens, request bodies, and response bodies; keep this artifact local"
			writesArtifact := strings.TrimSpace(outPath) != "" || strings.TrimSpace(harOutPath) != "" || strings.TrimSpace(bodyOutDir) != ""
			artifactSafety := redactor.Metadata(writesArtifact, artifactWarning)
			capture := map[string]any{
				"count":                      len(records),
				"wait":                       durationString(wait),
				"limit":                      limit,
				"truncated":                  truncated,
				"include_headers":            includeHeaders,
				"include_initiators":         includeInitiators,
				"include_timing":             includeTiming,
				"include_post_data":          includePostData,
				"include_bodies":             setKeys(bodyKinds),
				"body_limit":                 bodyLimit,
				"include_websockets":         includeWebSockets,
				"include_websocket_payloads": includeWebSocketPayloads,
				"websocket_payload_limit":    websocketPayloadLimit,
				"redact":                     redact,
				"trigger":                    trigger,
				"ignore_cache":               ignoreCache,
				"collector_errors":           collectorErrors,
				"artifact_safety":            artifactSafety,
			}
			if writesArtifact && redact == "none" {
				capture["local_artifact_warning"] = artifactWarning
			}
			report := map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"requests": records,
				"capture":  capture,
			}
			artifactList := []map[string]any{}
			if strings.TrimSpace(bodyOutDir) != "" {
				bodyArtifacts, bodyArtifactRefs, err := writeNetworkBodyArtifacts(bodyOutDir, records, bodyArtifactLimit, artifactSafety)
				if err != nil {
					return err
				}
				report["body_artifacts"] = bodyArtifacts
				artifactList = append(artifactList, bodyArtifactRefs...)
			}
			if strings.TrimSpace(outPath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal network capture report: %v", err), ExitInternal, []string{"cdp network capture --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "network-capture", "path": writtenPath, "bytes": len(b) + 1, "safety": artifactSafety}
				artifactList = append(artifactList, map[string]any{"type": "network-capture", "path": writtenPath})
			}
			if strings.TrimSpace(harOutPath) != "" {
				har := networkCaptureHAR(records)
				b, err := json.MarshalIndent(har, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal network HAR artifact: %v", err), ExitInternal, []string{"cdp network capture --har-out tmp/network.har --json"})
				}
				writtenPath, err := writeArtifactFile(harOutPath, append(b, '\n'))
				if err != nil {
					return err
				}
				harArtifact := map[string]any{"type": "network-har", "path": writtenPath, "bytes": len(b) + 1, "safety": artifactSafety}
				report["har"] = harArtifact
				artifactList = append(artifactList, map[string]any{"type": "network-har", "path": writtenPath})
			}
			if len(artifactList) > 0 {
				report["artifacts"] = artifactList
			}
			human := fmt.Sprintf("network-capture\t%d requests", len(records))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect network events after attaching")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum requests to return; use 0 for no limit")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the JSON network capture artifact")
	cmd.Flags().StringVar(&harOutPath, "har-out", "", "optional path for a HAR 1.2 artifact generated from captured records")
	cmd.Flags().BoolVar(&includeHeaders, "include-headers", true, "include request and response headers")
	cmd.Flags().BoolVar(&includeInitiators, "include-initiators", true, "include CDP initiator metadata and stack frames")
	cmd.Flags().BoolVar(&includeTiming, "include-timing", true, "include response timing and connection metadata")
	cmd.Flags().BoolVar(&includePostData, "include-post-data", true, "include request post data when CDP exposes it")
	cmd.Flags().StringVar(&includeBodies, "include-bodies", "json,text", "comma-separated response body kinds to include: json,text,base64,all")
	cmd.Flags().IntVar(&bodyLimit, "body-limit", 256*1024, "positive maximum request/response body bytes to include")
	cmd.Flags().StringVar(&bodyOutDir, "body-out-dir", "", "optional directory for bounded per-response body artifacts")
	cmd.Flags().IntVar(&bodyArtifactLimit, "body-artifact-limit", 20, "maximum response body artifacts to write (1-100)")
	cmd.Flags().BoolVar(&includeWebSockets, "include-websockets", false, "include WebSocket lifecycle events and frames")
	cmd.Flags().BoolVar(&includeWebSocketPayloads, "include-websocket-payloads", false, "include WebSocket frame payload text")
	cmd.Flags().IntVar(&websocketPayloadLimit, "websocket-payload-limit", 256*1024, "positive maximum WebSocket frame payload bytes to include")
	cmd.Flags().StringVar(&redact, "redact", "none", "redaction preset for output and artifacts: none, safe, or headers")
	cmd.Flags().BoolVar(&reload, "reload", false, "reload the selected page after attaching network collectors")
	cmd.Flags().BoolVar(&ignoreCache, "ignore-cache", false, "reload while bypassing cache")
	return cmd
}

func writeNetworkBodyArtifacts(outDir string, records []networkCaptureRecord, limit int, safety artifacts.SafetyMetadata) ([]map[string]any, []map[string]any, error) {
	outDir = filepath.Clean(strings.TrimSpace(outDir))
	bodyArtifacts := []map[string]any{}
	artifactRefs := []map[string]any{}
	written := 0
	for _, record := range records {
		if record.Body == nil || record.Body.Text == "" || len(bodyArtifacts) >= limit {
			continue
		}
		entry := map[string]any{
			"request_id":     record.ID,
			"url":            record.URL,
			"mime_type":      record.MimeType,
			"source_bytes":   record.Body.Bytes,
			"truncated":      record.Body.Truncated,
			"base64_encoded": record.Body.Base64Encoded,
			"safety":         safety,
		}
		if record.Body.Base64Encoded && safety.Shareable {
			entry["omitted_reason"] = "base64 response bodies remain local-only and are omitted from safe artifacts"
			bodyArtifacts = append(bodyArtifacts, entry)
			continue
		}
		written++
		ext := ".txt"
		if record.Body.Base64Encoded {
			ext = ".base64"
		} else if strings.Contains(strings.ToLower(record.MimeType), "json") {
			ext = ".json"
		}
		path := filepath.Join(outDir, fmt.Sprintf("body-%03d%s", written, ext))
		writtenPath, err := writeArtifactFile(path, []byte(record.Body.Text))
		if err != nil {
			return nil, nil, err
		}
		entry["path"] = writtenPath
		entry["bytes"] = len([]byte(record.Body.Text))
		bodyArtifacts = append(bodyArtifacts, entry)
		artifactRefs = append(artifactRefs, map[string]any{"type": "network-response-body", "path": writtenPath, "bytes": len([]byte(record.Body.Text)), "safety": safety})
	}
	return bodyArtifacts, artifactRefs, nil
}
