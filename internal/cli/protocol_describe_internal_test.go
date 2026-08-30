package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestFormatProtocolDescription(t *testing.T) {
	desc := cdp.EntityDescription{
		Kind:         "command",
		Path:         "Page.navigate",
		Description:  "Navigate the page.\nReturns after navigation starts.",
		Experimental: true,
		Deprecated:   true,
		Schema: json.RawMessage(`{
			"name":"navigate",
			"parameters":[
				{"name":"url","type":"string","description":"Destination URL."},
				{"name":"frameId","$ref":"Page.FrameId","optional":true,"description":"Target frame."},
				{"name":"headers","type":"array","optional":true,"items":{"$ref":"Network.HeaderEntry"}}
			],
			"returns":[
				{"name":"loaderId","$ref":"Network.LoaderId","description":"Navigation loader."}
			]
		}`),
	}

	got := formatProtocolDescription(desc)
	wants := []string{
		"command\tPage.navigate",
		"Navigate the page. Returns after navigation starts.",
		"Flags: experimental, deprecated",
		"Parameters:",
		"  url: string (required)",
		"    Destination URL.",
		"  frameId: Page.FrameId (optional)",
		"  headers: array<Network.HeaderEntry> (optional)",
		"Returns:",
		"  loaderId: Network.LoaderId",
		"    Navigation loader.",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("formatProtocolDescription() missing %q:\n%s", want, got)
		}
	}
}

func TestFormatProtocolDescriptionWithSparseSchema(t *testing.T) {
	got := formatProtocolDescription(cdp.EntityDescription{Kind: "domain", Path: "Page", Description: "Page domain"})
	if got != "domain\tPage\nPage domain" {
		t.Fatalf("sparse protocol description = %q", got)
	}
}
