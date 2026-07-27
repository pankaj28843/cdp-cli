package chatgpt

import (
	"strings"
	"testing"
)

func TestCapabilityMessageDoesNotClaimMissingModelCatalog(t *testing.T) {
	probe := capabilityProbe{
		OK:                   true,
		ComposerObserved:     true,
		ProductModes:         []string{"Chat", "Work"},
		IntelligenceOptions:  []string{"Medium", "High", "Extra High", "Pro"},
		SelectedIntelligence: "Pro",
	}
	state, message := capabilityStateAndMessage(probe)
	if state != "ready" {
		t.Fatalf("state = %q, want ready", state)
	}
	if strings.Contains(message, "visible model options were observed") ||
		!strings.Contains(message, "model catalog was not observed") {
		t.Fatalf("message = %q", message)
	}
}

func TestCapabilityMessageReportsIndependentlyObservedModelCatalog(t *testing.T) {
	probe := capabilityProbe{
		OK:                   true,
		ComposerObserved:     true,
		ProductModes:         []string{"Chat", "Work"},
		IntelligenceOptions:  []string{"Medium", "High", "Extra High", "Pro"},
		SelectedIntelligence: "Pro",
		ModelOptions:         []string{"o3", "GPT-5.6 Sol"},
		SelectedModel:        "GPT-5.6 Sol",
		ModelOptionsObserved: true,
	}
	state, message := capabilityStateAndMessage(probe)
	if state != "ready" {
		t.Fatalf("state = %q, want ready", state)
	}
	if !strings.Contains(message, "visible model options were observed") {
		t.Fatalf("message = %q", message)
	}
}

func TestNormalizeRuntimeCapabilitiesMigratesObservedModelCatalog(t *testing.T) {
	runtime := RuntimeCapabilities{
		State:                "ready",
		ComposerObserved:     true,
		ProductModes:         []string{"Chat", "Work"},
		IntelligenceOptions:  []string{"Medium", "High", "Extra High", "Pro"},
		SelectedIntelligence: "Pro",
		ModelOptions:         []string{"o3", "GPT-5.6 Sol"},
		SelectedModel:        "GPT-5.6 Sol",
		Message:              "legacy capability message",
	}

	got := normalizeRuntimeCapabilities(runtime)
	if !got.ModelOptionsObserved {
		t.Fatal("legacy selected visible model catalog was not migrated")
	}
	if !strings.Contains(got.Message, "visible model options were observed") {
		t.Fatalf("message = %q", got.Message)
	}
}

func TestNormalizeRuntimeCapabilitiesDoesNotInventMissingModelCatalog(t *testing.T) {
	runtime := RuntimeCapabilities{
		State:                "ready",
		ComposerObserved:     true,
		ProductModes:         []string{"Chat", "Work"},
		IntelligenceOptions:  []string{"Medium", "High", "Extra High", "Pro"},
		SelectedIntelligence: "Pro",
		Message:              "legacy capability message",
	}

	got := normalizeRuntimeCapabilities(runtime)
	if got.ModelOptionsObserved {
		t.Fatal("missing model catalog was marked observed")
	}
	if !strings.Contains(got.Message, "model catalog was not observed") {
		t.Fatalf("message = %q", got.Message)
	}
}

func TestNormalizeRuntimeCapabilitiesDowngradesLegacyMixedSelection(t *testing.T) {
	runtime := RuntimeCapabilities{
		State:                "ready",
		ComposerObserved:     true,
		ProductModes:         []string{"Chat", "Work"},
		IntelligenceOptions:  []string{"Medium", "GPT-5.6 Sol"},
		SelectedIntelligence: "GPT-5.6 Sol",
		Source:               "headed-cdp-sanitized-composer-probe",
	}

	got := normalizeRuntimeCapabilities(runtime)
	if got.State != "unknown" {
		t.Fatalf("state = %q, want unknown", got.State)
	}
	if got.SelectedIntelligence != "" {
		t.Fatalf(
			"selected_intelligence = %q, want empty",
			got.SelectedIntelligence,
		)
	}
	if containsString(got.IntelligenceOptions, "GPT-5.6 Sol") {
		t.Fatalf(
			"legacy model remains in thinking options: %#v",
			got.IntelligenceOptions,
		)
	}
	if got.ModelOptionsObserved {
		t.Fatal("legacy mixed label invented model observation")
	}
}
