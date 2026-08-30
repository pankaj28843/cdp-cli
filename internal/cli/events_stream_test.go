package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestEventsStreamIsSessionScopedBoundedAndIndexAddressable(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "first-page", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-target", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "stream-page", "type": "page", "title": "Stream", "url": "https://example.test/stream"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	closeInput := time.AfterFunc(2*time.Second, func() { _ = writer.Close() })
	defer closeInput.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(ctx, []string{
		"events", "stream", "--target-index", "2", "--enable", "runtime",
		"--match", "Runtime.consoleAPICalled", "--max-events", "1", "--json",
	}, reader, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("events stream exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	records := decodeJSONLines(t, out.String())
	if len(records) != 3 {
		t.Fatalf("events stream records=%d, want ready/event/stopped: %s", len(records), out.String())
	}
	if records[0]["type"] != "ready" || records[1]["type"] != "event" || records[2]["type"] != "stopped" {
		t.Fatalf("events stream record types = %v, want ready/event/stopped", recordTypes(records))
	}
	readyTarget, ok := records[0]["target"].(map[string]any)
	if !ok || readyTarget["id"] != "stream-page" {
		t.Fatalf("ready target = %#v, want stream-page", records[0]["target"])
	}
	streamMetadata, ok := records[0]["stream"].(map[string]any)
	if !ok || streamMetadata["session_bound"] != true || streamMetadata["event_dequeue"] != "exact_session" || streamMetadata["target_index"] != float64(2) {
		t.Fatalf("ready stream metadata = %#v, want exact-session dequeue and index 2", records[0]["stream"])
	}
	liveness, ok := streamMetadata["liveness"].(map[string]any)
	if !ok || liveness["enabled"] != true || liveness["heartbeat"] != "Runtime.evaluate" || liveness["poll_interval"] != "15s" || liveness["failure_threshold"] != float64(2) || liveness["read_only"] != true {
		t.Fatalf("ready liveness metadata = %#v, want read-only 15s two-strike heartbeat", streamMetadata["liveness"])
	}
	event, ok := records[1]["event"].(map[string]any)
	if !ok || event["sessionId"] != "session-stream-page" || event["method"] != "Runtime.consoleAPICalled" {
		t.Fatalf("stream event = %#v, want attached session runtime event", records[1]["event"])
	}
	if dropped, ok := records[2]["foreign_events_dropped"].(float64); !ok || dropped != 0 {
		t.Fatalf("stream stopped record = %#v, want exact-session dequeue with no foreign event", records[2])
	}
	if records[2]["reason"] == "" {
		t.Fatalf("stopped record has no reason: %#v", records[2])
	}
}

func TestEventsStreamAcceptsDynamicSubscriptionCommands(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "subscription-page",
		"type":     "page",
		"title":    "Subscription",
		"url":      "https://example.test/subscription",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "stream", "--target", "subscription-page", "--enable", "page",
		"--match", "Never.emitted", "--duration", "1s", "--json",
	}, strings.NewReader("+Runtime.consoleAPICalled\n-Runtime.consoleAPICalled\n"), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("events stream subscriptions exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	records := decodeJSONLines(t, out.String())
	if len(records) < 4 {
		t.Fatalf("events stream records=%d, want ready, two subscription records, stopped: %s", len(records), out.String())
	}
	var operations []string
	for _, record := range records {
		if record["type"] != "subscription" {
			continue
		}
		if method, ok := record["method"].(string); !ok || method != "Runtime.consoleAPICalled" {
			t.Fatalf("subscription record = %#v, want Runtime method", record)
		}
		operation, ok := record["operation"].(string)
		if !ok {
			t.Fatalf("subscription record has no operation: %#v", record)
		}
		operations = append(operations, operation)
	}
	if strings.Join(operations, ",") != "add,remove" {
		t.Fatalf("subscription operations = %v, want add,remove; output=%s", operations, out.String())
	}
}

func TestEventsStreamRejectsExplicitZeroTargetIndex(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "stream", "--target-index", "0", "--json",
	}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("events stream zero index exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var record struct {
		OK    bool   `json:"ok"`
		Code  string `json:"code"`
		Error string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatalf("zero-index error is invalid JSON: %v", err)
	}
	if record.OK || record.Code != "invalid_target_index" || !strings.Contains(record.Error, "greater than zero") {
		t.Fatalf("zero-index error = %+v, want invalid_target_index usage envelope", record)
	}
}

func TestEventsStreamStopsOnContextDeadline(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "deadline-page",
		"type":     "page",
		"title":    "Deadline",
		"url":      "https://example.test/deadline",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(ctx, []string{
		"events", "stream", "--target", "deadline-page", "--enable", "page",
		"--match", "Never.emitted", "--json",
	}, reader, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("events stream deadline exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}
	records := decodeJSONLines(t, out.String())
	if len(records) < 2 || records[0]["type"] != "ready" || records[len(records)-1]["type"] != "stopped" || records[len(records)-1]["reason"] != "timeout" {
		t.Fatalf("events stream deadline records = %v, want ready/stopped timeout", recordTypes(records))
	}
}

func TestEventsStreamRetiresAfterExactSessionLivenessLoss(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId":                       "unhealthy-page",
		"type":                           "page",
		"title":                          "Unhealthy",
		"url":                            "https://example.test/unhealthy",
		"fakeRuntimeEvaluateErrorAlways": true,
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(ctx, []string{
		"events", "stream", "--target", "unhealthy-page", "--enable", "page",
		"--match", "Never.emitted", "--json",
	}, reader, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("events stream liveness exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	records := decodeJSONLines(t, out.String())
	if len(records) != 2 || records[0]["type"] != "ready" || records[1]["type"] != "stopped" {
		t.Fatalf("events stream liveness records = %v, want ready/stopped: %s", recordTypes(records), out.String())
	}
	if records[1]["reason"] != "liveness" {
		t.Fatalf("events stream liveness stop reason = %#v, want liveness", records[1]["reason"])
	}
	stream, ok := records[1]["stream"].(map[string]any)
	if !ok {
		t.Fatalf("events stream liveness stream metadata = %#v, want object", records[1]["stream"])
	}
	liveness, ok := stream["liveness"].(map[string]any)
	if !ok || liveness["state"] != "retired" || liveness["reason"] != "exact_session_unhealthy" || liveness["consecutive_failures"] != float64(2) || liveness["read_only"] != true {
		t.Fatalf("events stream liveness metadata = %#v, want metadata-only two-strike retirement", stream["liveness"])
	}
}

func decodeJSONLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var records []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode JSONL record %q: %v", scanner.Text(), err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read JSONL records: %v", err)
	}
	return records
}

func recordTypes(records []map[string]any) []any {
	types := make([]any, 0, len(records))
	for _, record := range records {
		types = append(types, record["type"])
	}
	return types
}
