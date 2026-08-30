package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestEventsInteractionsStreamsSanitizedBindingEventsAndCleansUp(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "interaction-page",
		"type":     "page",
		"title":    "Interaction",
		"url":      "https://example.test/interaction",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "interactions", "--target", "interaction-page",
		"--match", "click", "--max-events", "1", "--json",
	}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("events interactions exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	records := decodeJSONLines(t, out.String())
	if len(records) != 3 || records[0]["type"] != "ready" || records[1]["type"] != "interaction" || records[2]["type"] != "stopped" {
		t.Fatalf("interaction records = %v, want ready/interaction/stopped", recordTypes(records))
	}
	observer, ok := records[0]["observer"].(map[string]any)
	if !ok || observer["event_dequeue"] != "exact_session" || observer["binding_installed"] != true || observer["current_document_installed"] != true || observer["future_documents_installed"] != true {
		t.Fatalf("ready observer = %#v, want exact-session dequeue and current/future binding setup evidence", records[0]["observer"])
	}
	interaction, ok := records[1]["interaction"].(map[string]any)
	if !ok || interaction["type"] != "click" {
		t.Fatalf("interaction = %#v, want sanitized click", records[1]["interaction"])
	}
	if _, exists := interaction["text"]; exists {
		t.Fatalf("interaction leaked text field: %#v", interaction)
	}
	if _, exists := interaction["value"]; exists {
		t.Fatalf("interaction leaked value field: %#v", interaction)
	}
	if _, exists := interaction["key"]; exists {
		t.Fatalf("interaction leaked key field: %#v", interaction)
	}
	cleanup, ok := records[2]["cleanup"].(map[string]any)
	if !ok || cleanup["current_document_removed"] != true || cleanup["future_document_removed"] != true || cleanup["binding_removed"] != true {
		t.Fatalf("stopped cleanup = %#v, want all cleanup stages", records[2]["cleanup"])
	}
	if records[2]["reason"] != "max_events" {
		t.Fatalf("stopped reason = %#v, want max_events", records[2]["reason"])
	}
}

func TestEventsInteractionsRejectsUnsafeOrUnknownKinds(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "interactions", "--match", "mousemove", "--json",
	}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("unknown interaction kind exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "invalid_interaction_kind") {
		t.Fatalf("unknown interaction kind output=%s, want typed error", out.String())
	}
}

func TestEventsInteractionsDropsArbitraryBindingPayloads(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId":                      "unsafe-interaction-page",
		"type":                          "page",
		"title":                         "Interaction safety",
		"url":                           "https://example.test/interaction-safety",
		"fakeInteractionUnsafePayload":  true,
		"fakeInteractionForeignPayload": true,
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "interactions", "--target", "unsafe-interaction-page",
		"--match", "click", "--max-events", "1", "--json",
	}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("unsafe interaction stream exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	records := decodeJSONLines(t, out.String())
	if strings.Contains(out.String(), "synthetic-secret") || strings.Contains(out.String(), `"text"`) {
		t.Fatalf("unsafe binding payload leaked into output: %s", out.String())
	}
	stopped := records[len(records)-1]
	ignored, ok := stopped["ignored_binding_events"].(float64)
	if !ok || ignored < 1 {
		t.Fatalf("stopped ignored_binding_events = %#v, want rejected payload evidence", stopped["ignored_binding_events"])
	}
	foreign, ok := stopped["foreign_events_dropped"].(float64)
	if !ok || foreign != 0 {
		t.Fatalf("stopped foreign_events_dropped = %#v, want exact-session dequeue with no foreign event", stopped["foreign_events_dropped"])
	}
}

func TestEventsInteractionsHonorsKindFilterAndDuration(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "filtered-interaction-page",
		"type":     "page",
		"title":    "Filtered interaction",
		"url":      "https://example.test/filtered-interaction",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "interactions", "--target", "filtered-interaction-page",
		"--match", "scroll", "--duration", "60ms", "--json",
	}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("filtered interaction exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}
	records := decodeJSONLines(t, out.String())
	if len(records) != 2 || records[0]["type"] != "ready" || records[1]["type"] != "stopped" || records[1]["reason"] != "timeout" {
		t.Fatalf("filtered interaction records = %v, want ready/stopped timeout", recordTypes(records))
	}
	if records[1]["event_count"] != float64(0) {
		t.Fatalf("filtered interaction event_count = %#v, want zero", records[1]["event_count"])
	}
	cleanup, ok := records[1]["cleanup"].(map[string]any)
	if !ok || cleanup["current_document_removed"] != true || cleanup["future_document_removed"] != true || cleanup["binding_removed"] != true {
		t.Fatalf("filtered interaction cleanup = %#v, want complete cleanup on timeout", records[1]["cleanup"])
	}
}
