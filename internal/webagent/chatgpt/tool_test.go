package chatgpt

import "testing"

func TestNormalizeTool(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		err   bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "canonical", input: "create-image", want: ToolCreateImage},
		{name: "human label", input: "Create image", want: ToolCreateImage},
		{name: "unsupported", input: "deep-research", err: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeTool(test.input)
			if test.err {
				if err == nil {
					t.Fatalf("NormalizeTool(%q) accepted unsupported tool", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeTool(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeTool(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestImageGenerationPendingDefersHydratedDetail(t *testing.T) {
	observation := renderedObservation{
		RouteMatches:        true,
		UserMessageCount:    1,
		GeneratedImageCount: 1,
		GeneratedImageReady: false,
		Streaming:           false,
	}
	if !imageGenerationPending(
		ToolCreateImage,
		observation,
		true,
		false,
	) {
		t.Fatal("pending generated image should defer detail read")
	}
	if imageGenerationPending(
		ToolCreateImage,
		observation,
		false,
		false,
	) {
		t.Fatal("unproven prompt identity must not defer detail read")
	}
	if imageGenerationPending(
		ToolCreateImage,
		observation,
		true,
		true,
	) {
		t.Fatal("substantive text must not be classified as image pending")
	}
}

func TestImagePlaceholderRecoveryRequiresCompletedGenerationState(t *testing.T) {
	observation := renderedObservation{
		RouteMatches:        true,
		GeneratedImageCount: 1,
		GeneratedImageReady: false,
		Streaming:           false,
	}
	if !imagePlaceholderNeedsRecovery(ToolCreateImage, observation) {
		t.Fatal("stable image placeholder should allow one bounded recovery")
	}
	observation.Streaming = true
	if imagePlaceholderNeedsRecovery(ToolCreateImage, observation) {
		t.Fatal("active generation should not trigger recovery")
	}
	observation.Streaming = false
	observation.GeneratedImageReady = true
	if imagePlaceholderNeedsRecovery(ToolCreateImage, observation) {
		t.Fatal("decoded image should not trigger recovery")
	}
}
