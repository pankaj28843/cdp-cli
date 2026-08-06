package chatgpt

import (
	"reflect"
	"testing"
)

func TestNormalizeSelectionPolicyIsEntitlementNeutralByDefault(t *testing.T) {
	got, err := NormalizeSelectionPolicy(SelectionPolicy{})
	if err != nil {
		t.Fatalf("NormalizeSelectionPolicy returned error: %v", err)
	}
	if got.Thinking != ThinkingCurrent ||
		got.MinimumThinking != "" ||
		got.Model != ModelCurrent {
		t.Fatalf("default policy = %+v, want current/no floor/current", got)
	}
}

func TestNormalizeSelectionPolicyAcceptsAscendingThinkingVocabulary(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"instant", "Instant"},
		{"instant-5.5", "Instant 5.5"},
		{"medium", "Medium"},
		{"high", "High"},
		{"extra-high", "Extra High"},
		{"xhigh", "Extra High"},
		{"pro", "Pro"},
		{"highest", ThinkingHighest},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := NormalizeSelectionPolicy(SelectionPolicy{
				Thinking: test.input,
			})
			if err != nil {
				t.Fatalf("NormalizeSelectionPolicy returned error: %v", err)
			}
			if got.Thinking != test.want {
				t.Fatalf("Thinking = %q, want %q", got.Thinking, test.want)
			}
		})
	}
}

func TestNormalizeSelectionPolicyRejectsPolicyAsMinimum(t *testing.T) {
	for _, value := range []string{"current", "highest", "turbo"} {
		t.Run(value, func(t *testing.T) {
			if _, err := NormalizeSelectionPolicy(SelectionPolicy{
				MinimumThinking: value,
			}); err == nil {
				t.Fatalf("minimum %q unexpectedly accepted", value)
			}
		})
	}
}

func TestThinkingRanksAreLogicalAscending(t *testing.T) {
	ordered := []string{
		"Instant",
		"Instant 5.5",
		"Medium",
		"High",
		"Extra High",
		"Pro",
	}
	last := -1
	for _, label := range ordered {
		rank := thinkingRank(label)
		if rank <= last {
			t.Fatalf("rank(%q) = %d after %d", label, rank, last)
		}
		last = rank
	}
	if !thinkingAtOrAbove("Pro", "Extra High") {
		t.Fatal("Pro must satisfy an Extra High floor")
	}
	if thinkingAtOrAbove("High", "Extra High") {
		t.Fatal("High must not satisfy an Extra High floor")
	}
	if thinkingAtOrAbove("Future Ultra", "Extra High") {
		t.Fatal("unknown thinking labels must fail a known floor closed")
	}
}

func TestThinkingSliderLabelsMatchesCurrentFiveStopComposer(t *testing.T) {
	got := thinkingSliderLabels(4)
	want := []string{"Instant 5.5", "Medium", "High", "Extra High", "Pro"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("thinkingSliderLabels(4) = %v, want %v", got, want)
	}
	if index, ok := thinkingSliderTargetIndex("Medium", 4); !ok || index != 1 {
		t.Fatalf("Medium slider target = (%d, %v), want (1, true)", index, ok)
	}
}

func TestThinkingSliderLabelsRetainsLegacySixStopSurface(t *testing.T) {
	got := thinkingSliderLabels(5)
	want := []string{
		"Instant", "Instant 5.5", "Medium", "High", "Extra High", "Pro",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("thinkingSliderLabels(5) = %v, want %v", got, want)
	}
	if _, ok := thinkingSliderTargetIndex("Instant", 4); ok {
		t.Fatal("legacy Instant must not be selectable on current five-stop slider")
	}
}

func TestThinkingOptionsBecomeLogicalAscendingAndHighestSkipsDisabled(t *testing.T) {
	reordered := []selectableOption{
		{Label: "Pro", Ready: false},
		{Label: "Medium", Ready: true},
		{Label: "Extra High", Ready: true},
		{Label: "Instant 5.5", Ready: true},
		{Label: "High", Ready: true},
	}
	logical := logicalThinkingOptions(reordered)
	got := optionLabels(logical, false)
	want := []string{
		"Instant 5.5",
		"Medium",
		"High",
		"Extra High",
		"Pro",
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("thinking labels = %v, want %v", got, want)
		}
	}
	highest, ok := highestReadyThinkingOption(reordered)
	if !ok || highest.Label != "Extra High" {
		t.Fatalf("highest ready thinking = %+v, ok=%v", highest, ok)
	}
}

func TestFutureThinkingAboveKnownFloorUsesObservedLogicalPosition(
	t *testing.T,
) {
	options := []selectableOption{
		{Label: "Future Ultra", Ready: true},
		{Label: "Pro", Ready: true},
		{Label: "Extra High", Ready: true},
		{Label: "High", Ready: true},
		{Label: "Medium", Ready: true},
	}
	highest, ok := highestReadyThinkingOption(options)
	if !ok || highest.Label != "Future Ultra" {
		t.Fatalf("highest = %#v, %v", highest, ok)
	}
	if !thinkingAtOrAboveObserved(
		highest.Label,
		"Extra High",
		options,
	) {
		t.Fatal("future option observed above Pro did not satisfy Extra High")
	}
}

func TestUnknownThinkingBelowKnownFloorFailsClosed(t *testing.T) {
	options := []selectableOption{
		{Label: "Pro", Ready: true},
		{Label: "Extra High", Ready: true},
		{Label: "High", Ready: true},
		{Label: "Medium", Ready: true},
		{Label: "Unknown Low", Ready: true},
	}
	if thinkingAtOrAboveObserved(
		"Unknown Low",
		"Extra High",
		options,
	) {
		t.Fatal("unknown option observed below the floor must fail closed")
	}
}

func TestModelOptionsBecomeLogicalAscending(t *testing.T) {
	providerDescending := []selectableOption{
		{Label: "GPT-5.6 Sol"},
		{Label: "GPT-5.5"},
		{Label: "GPT-5.3"},
		{Label: "o3"},
	}
	got := optionLabels(providerDescending, true)
	want := []string{"o3", "GPT-5.3", "GPT-5.5", "GPT-5.6 Sol"}
	if len(got) != len(want) {
		t.Fatalf("model labels = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("model labels = %v, want %v", got, want)
		}
	}
}

func TestHighestModelSkipsDisabledProviderLeader(t *testing.T) {
	providerDescending := []selectableOption{
		{Label: "GPT-5.6 Sol", Ready: false},
		{Label: "GPT-5.5", Ready: true},
		{Label: "GPT-5.3", Ready: true},
	}
	highest, ok := highestReadyModelOption(providerDescending)
	if !ok || highest.Label != "GPT-5.5" {
		t.Fatalf("highest ready model = %+v, ok=%v", highest, ok)
	}
}
