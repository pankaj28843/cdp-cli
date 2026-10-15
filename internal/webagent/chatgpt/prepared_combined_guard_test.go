package chatgpt

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Replay the selection surface at the real failed stage: the prompt and four
// attachments were already verified, but the actual pre-dispatch guards still
// rejected the combined menu. Exercise those guards, not the earlier selection
// verifier used by the previous empty-composer live test. No Send is dispatched.
func TestPreparedPromptFinalGuardsUseCheckedCombinedModel(t *testing.T) {
	base := selectionSurface{PickerCount: 1, Picker: selectionPoint{Ready: true}, SelectedThinking: "Pro", ThinkingMenuOpen: true, ThinkingSliderReady: true, ThinkingSliderMin: 0, ThinkingSliderMax: 4, ThinkingSliderValue: 4, ModelMenuOpen: true, SelectedModel: "GPT-6", ModelOptions: []selectableOption{{Label: "GPT-6", Checked: true, Ready: false}, {Label: "GPT-5.6 Sol", Checked: false, Ready: false}}}
	expectation := thinkingSelectionExpectation{Label: "Pro", Minimum: "Pro", SliderMin: 0, SliderMax: 4, SliderTarget: 4, HasSliderTarget: true}
	for _, tc := range []struct {
		name   string
		mutate func(*selectionSurface)
		want   bool
	}{
		{"exact checked pinned model", func(*selectionSurface) {}, true},
		{"wrong selected model", func(s *selectionSurface) {
			s.ModelOptions[0].Checked = false
			s.ModelOptions[1].Checked = true
			s.SelectedModel = "GPT-5.6 Sol"
		}, false},
		{"two checked models", func(s *selectionSurface) { s.ModelOptions[1].Checked = true; s.SelectedModel = "" }, false},
		{"duplicate model label", func(s *selectionSurface) { s.ModelOptions = append(s.ModelOptions, selectableOption{Label: "GPT-6"}) }, false},
		{"changed Pro slider", func(s *selectionSurface) { s.ThinkingSliderValue = 3 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			surface := base
			surface.ModelOptions = append([]selectableOption(nil), base.ModelOptions...)
			tc.mutate(&surface)
			raw, err := json.Marshal(surface)
			if err != nil {
				t.Fatal(err)
			}
			client := &selectionActivationClient{evaluation: raw}
			session := newSelectionActivationSession(t, client)
			prepared := prepareSelectionGuardAtSendWithExpectation(context.Background(), session, expectation, "GPT-6", 20*time.Millisecond, time.Millisecond)
			observed := observeSelectionGuardAtSendWithExpectation(context.Background(), session, expectation, "GPT-6")
			if tc.want && (prepared != nil || observed != nil) {
				t.Fatalf("prepared=%v observed=%v", prepared, observed)
			}
			if !tc.want && (prepared == nil || observed == nil) {
				t.Fatalf("unsafe final guard accepted: prepared=%v observed=%v", prepared, observed)
			}
			for _, call := range client.calls {
				if call.method != "Runtime.evaluate" {
					t.Fatalf("final guard emitted input: %s", call.method)
				}
			}
		})
	}
}
