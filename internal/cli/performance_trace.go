package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type performanceTraceResult struct {
	Stream   cdp.IOStreamResult       `json:"stream"`
	Artifact map[string]any           `json:"artifact,omitempty"`
	Safety   artifacts.SafetyMetadata `json:"artifact_safety"`
	Insights map[string]any           `json:"insights"`
}

type traceDocument struct {
	TraceEvents []traceEvent `json:"traceEvents"`
}

type traceEvent struct {
	Name string         `json:"name"`
	TS   float64        `json:"ts"`
	Dur  float64        `json:"dur"`
	Args map[string]any `json:"args"`
}

func startPerformanceTrace(ctx context.Context, client browserEventClient, sessionID string) error {
	params := map[string]any{
		"transferMode": "ReturnAsStream",
		"traceConfig": map[string]any{
			"recordMode": "recordContinuously",
			"includedCategories": []string{
				"devtools.timeline",
				"loading",
				"blink.user_timing",
				"disabled-by-default-devtools.timeline",
			},
		},
	}
	return client.CallSession(ctx, sessionID, "Tracing.start", params, nil)
}

func stopPerformanceTraceBestEffort(client browserEventClient, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = client.CallSession(ctx, sessionID, "Tracing.end", map[string]any{}, nil)
}

func finishPerformanceTrace(ctx context.Context, client browserEventClient, sessionID, outPath, redact string, maxBytes int) (performanceTraceResult, error) {
	redactor := artifacts.NewRedactor(redact)
	result := performanceTraceResult{Safety: redactor.Metadata(strings.TrimSpace(outPath) != "", "performance traces can contain URLs, script names, and page data; keep unredacted traces local")}
	if err := client.CallSession(ctx, sessionID, "Tracing.end", map[string]any{}, nil); err != nil {
		return result, fmt.Errorf("end performance trace: %w", err)
	}
	handle, err := waitForTraceStream(ctx, client, sessionID)
	if err != nil {
		return result, err
	}
	var raw bytes.Buffer
	stream, err := cdp.ReadIOStream(ctx, client, sessionID, handle, maxBytes, &raw)
	result.Stream = stream
	if err != nil {
		return result, err
	}
	result.Insights = performanceTraceInsights(raw.Bytes())

	artifactBytes := raw.Bytes()
	if result.Stream.Truncated && redactor.Mode() != artifacts.ModeNone {
		artifactBytes, _ = json.MarshalIndent(map[string]any{
			"truncated":      true,
			"omitted_reason": "raw trace exceeded the configured bound and was omitted because a partial trace cannot be safely redacted",
			"insights":       result.Insights,
		}, "", "  ")
		artifactBytes = append(artifactBytes, '\n')
	} else if redactor.Mode() != artifacts.ModeNone {
		artifactBytes = []byte(redactor.BodyText(string(artifactBytes), "trace"))
		if len(artifactBytes) > maxBytes {
			result.Stream.Truncated = true
			artifactBytes, _ = json.MarshalIndent(map[string]any{
				"truncated":      true,
				"omitted_reason": "redacted trace exceeded the configured bound and was omitted",
				"insights":       result.Insights,
			}, "", "  ")
			artifactBytes = append(artifactBytes, '\n')
		}
	}
	result.Safety = redactor.Metadata(strings.TrimSpace(outPath) != "", "performance traces can contain URLs, script names, and page data; keep unredacted traces local")
	if strings.TrimSpace(outPath) != "" {
		writtenPath, err := writeArtifactFile(outPath, artifactBytes)
		if err != nil {
			return result, err
		}
		result.Artifact = map[string]any{
			"type":      "performance-trace",
			"path":      writtenPath,
			"bytes":     len(artifactBytes),
			"truncated": result.Stream.Truncated,
			"safety":    result.Safety,
		}
	}
	return result, nil
}

func waitForTraceStream(ctx context.Context, client browserEventClient, sessionID string) (string, error) {
	for {
		event, err := client.ReadEvent(ctx)
		if err != nil {
			return "", fmt.Errorf("wait for Tracing.tracingComplete: %w", err)
		}
		if event.Method != "Tracing.tracingComplete" || (event.SessionID != "" && event.SessionID != sessionID) {
			continue
		}
		var params struct {
			Stream string `json:"stream"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return "", fmt.Errorf("decode Tracing.tracingComplete: %w", err)
		}
		if strings.TrimSpace(params.Stream) == "" {
			return "", errors.New("Tracing.tracingComplete did not include a stream handle")
		}
		return params.Stream, nil
	}
}

func performanceTraceInsights(raw []byte) map[string]any {
	events, err := decodeTraceEvents(raw)
	if err != nil {
		reason := "trace artifact could not be decoded: " + err.Error()
		return map[string]any{
			"lcp":               unavailableInsight(reason),
			"cls":               unavailableInsight(reason),
			"long_tasks":        unavailableInsight(reason),
			"blocking_requests": unavailableInsight(reason),
		}
	}
	return map[string]any{
		"lcp":               traceLCPInsight(events),
		"cls":               traceCLSInsight(events),
		"long_tasks":        traceLongTaskInsight(events),
		"blocking_requests": traceBlockingRequestInsight(events),
	}
}

func decodeTraceEvents(raw []byte) ([]traceEvent, error) {
	var document traceDocument
	if err := json.Unmarshal(raw, &document); err == nil && document.TraceEvents != nil {
		return document.TraceEvents, nil
	}
	var events []traceEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, err
	}
	return events, nil
}

func traceLCPInsight(events []traceEvent) map[string]any {
	var navigationStart float64
	var candidate *traceEvent
	for i := range events {
		lower := strings.ToLower(events[i].Name)
		if lower == "navigationstart" && (navigationStart == 0 || events[i].TS < navigationStart) {
			navigationStart = events[i].TS
		}
		if strings.Contains(lower, "largestcontentfulpaint") && (candidate == nil || events[i].TS > candidate.TS) {
			candidate = &events[i]
		}
	}
	if candidate == nil {
		return unavailableInsight("LargestContentfulPaint was not present in the trace")
	}
	insight := map[string]any{"available": true, "source": "trace", "event": candidate.Name, "timestamp_us": candidate.TS}
	if navigationStart > 0 && candidate.TS >= navigationStart {
		insight["value_ms"] = (candidate.TS - navigationStart) / 1000
		insight["scope"] = "navigation-relative"
	} else {
		insight["reason"] = "navigationStart was unavailable; reporting the raw trace timestamp without claiming a page-load LCP"
		insight["scope"] = "trace-timestamp"
	}
	return insight
}

func traceCLSInsight(events []traceEvent) map[string]any {
	count := 0
	total := 0.0
	for _, event := range events {
		if !strings.Contains(strings.ToLower(event.Name), "layoutshift") {
			continue
		}
		data := nestedMap(event.Args, "data")
		if recent, ok := data["had_recent_input"].(bool); ok && recent {
			continue
		}
		delta, ok := firstFloat(data, "weighted_score_delta", "score", "cumulative_score")
		if !ok {
			continue
		}
		count++
		total += delta
	}
	if count == 0 {
		return unavailableInsight("no layout-shift score events without recent input were present in the trace")
	}
	return map[string]any{"available": true, "source": "trace", "value": total, "event_count": count, "scope": "captured-trace-window"}
}

func traceLongTaskInsight(events []traceEvent) map[string]any {
	count := 0
	longest := 0.0
	totalBlocking := 0.0
	for _, event := range events {
		lower := strings.ToLower(event.Name)
		if lower != "runtask" && !strings.Contains(lower, "longtask") {
			continue
		}
		durationMS := event.Dur / 1000
		if durationMS < 50 {
			continue
		}
		count++
		if durationMS > longest {
			longest = durationMS
		}
		totalBlocking += durationMS - 50
	}
	return map[string]any{"available": true, "source": "trace", "threshold_ms": 50, "count": count, "longest_ms": longest, "total_blocking_time_ms": totalBlocking, "scope": "captured-trace-window"}
}

func traceBlockingRequestInsight(events []traceEvent) map[string]any {
	type requestStart struct{ ts float64 }
	starts := map[string]requestStart{}
	durations := []float64{}
	for _, event := range events {
		data := nestedMap(event.Args, "data")
		requestID := fmt.Sprint(data["requestId"])
		if requestID == "" || requestID == "<nil>" {
			continue
		}
		switch strings.ToLower(event.Name) {
		case "resourcesendrequest":
			starts[requestID] = requestStart{ts: event.TS}
		case "resourcefinish":
			if start, ok := starts[requestID]; ok && event.TS >= start.ts {
				durations = append(durations, (event.TS-start.ts)/1000)
			}
		}
	}
	if len(durations) == 0 {
		return unavailableInsight("paired ResourceSendRequest/ResourceFinish timing was not present in the trace")
	}
	const thresholdMS = 1000.0
	count := 0
	longest := 0.0
	for _, duration := range durations {
		if duration >= thresholdMS {
			count++
		}
		if duration > longest {
			longest = duration
		}
	}
	return map[string]any{"available": true, "source": "trace", "definition": "request duration at least threshold_ms", "threshold_ms": thresholdMS, "count": count, "longest_ms": longest, "paired_request_count": len(durations), "scope": "captured-trace-window"}
}

func unavailableInsight(reason string) map[string]any {
	return map[string]any{"available": false, "reason": reason}
}

func nestedMap(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return map[string]any{}
	}
	value, _ := parent[key].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func firstFloat(values map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		switch value := values[key].(type) {
		case float64:
			return value, true
		case json.Number:
			parsed, err := value.Float64()
			return parsed, err == nil
		}
	}
	return 0, false
}
