package cli

import (
	"strings"
	"testing"
)

func TestSanitizeInteractionPayloadAllowListsMetadata(t *testing.T) {
	got, ok := sanitizeInteractionPayload(`{"type":"click","x":12.5,"y":34,"button":0,"detail":1,"target":{"tag":"button","editable":false}}`)
	if !ok || got.Type != "click" {
		t.Fatalf("sanitize click = %#v, %v; want valid click", got, ok)
	}
	if got.Data["x"] != 12.5 || got.Data["y"] != float64(34) {
		t.Fatalf("sanitized click coordinates = %#v", got.Data)
	}
	if _, exists := got.Data["text"]; exists {
		t.Fatalf("sanitized click contains text: %#v", got.Data)
	}

	for _, raw := range []string{
		`{"type":"selectionchange","has_selection":true,"collapsed":false,"text":"secret"}`,
		`{"type":"keydown","key":"A","key_kind":"printable","alt":false,"ctrl":false,"meta":false,"shift":false,"repeat":false}`,
		`{"type":"click","x":1,"y":2,"button":0,"detail":1} trailing`,
		`{"type":"click","x":1e20,"y":2,"button":0,"detail":1}`,
	} {
		if _, ok := sanitizeInteractionPayload(raw); ok {
			t.Fatalf("unsafe interaction payload accepted: %s", raw)
		}
	}
}

func TestSanitizeInteractionPayloadSupportsEveryDocumentedKind(t *testing.T) {
	fixtures := []struct {
		kind string
		raw  string
	}{
		{"click", `{"type":"click","x":1,"y":2,"button":0,"detail":1}`},
		{"scroll", `{"type":"scroll","scroll_x":3,"scroll_y":4}`},
		{"selectionchange", `{"type":"selectionchange","has_selection":true,"collapsed":false}`},
		{"keydown", `{"type":"keydown","key_kind":"control","alt":true,"ctrl":false,"meta":false,"shift":true,"repeat":false}`},
	}
	for _, fixture := range fixtures {
		got, ok := sanitizeInteractionPayload(fixture.raw)
		if !ok || got.Type != fixture.kind || got.Data["type"] != fixture.kind {
			t.Errorf("sanitize %s = %#v, %v; want valid allow-listed payload", fixture.kind, got, ok)
		}
	}
}

func TestInteractionObserverScriptIsCurrentFutureAndContentSafe(t *testing.T) {
	script := interactionObserverScript("__cdp_cli_test_binding")
	for _, want := range []string{"click", "scroll", "selectionchange", "keydown", interactionObserverMarker} {
		if !strings.Contains(script, want) {
			t.Fatalf("interaction script missing %q: %s", want, script)
		}
	}
	for _, forbidden := range []string{"textContent", "selection.toString", "input.value", "innerHTML", "cookies"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("interaction script contains forbidden page-content access %q", forbidden)
		}
	}
	cleanup := interactionObserverCleanupScript("__cdp_cli_test_binding")
	if !strings.Contains(cleanup, interactionObserverMarker) || !strings.Contains(cleanup, "cleanup") {
		t.Fatalf("cleanup script = %s, want marker cleanup", cleanup)
	}
}

func TestParseInteractionKindsDefaultsAndRejectsUnknown(t *testing.T) {
	all, err := parseInteractionKinds("")
	if err != nil || len(all) != len(interactionKinds) {
		t.Fatalf("default interaction kinds = %#v, %v; want all supported kinds", all, err)
	}
	selected, err := parseInteractionKinds("click, keydown,click")
	if err != nil || len(selected) != 2 || !selected["click"] || !selected["keydown"] {
		t.Fatalf("selected interaction kinds = %#v, %v", selected, err)
	}
	if _, err := parseInteractionKinds("mousemove"); err == nil {
		t.Fatal("unknown interaction kind unexpectedly accepted")
	}
}
