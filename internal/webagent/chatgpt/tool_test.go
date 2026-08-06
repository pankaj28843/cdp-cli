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
		{name: "web search canonical", input: "web-search", want: ToolWebSearch},
		{name: "web search label", input: "Web search", want: ToolWebSearch},
		{name: "github", input: "GitHub", want: ToolGitHub},
		{name: "openai platform", input: "OpenAI Platform", want: ToolOpenAIPlatform},
		{name: "visualize", input: "Visualize", want: ToolVisualize},
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

func TestChatGPTToolPolicies(t *testing.T) {
	for _, tool := range []string{ToolCreateImage, ToolVisualize} {
		if !isImageTool(tool) {
			t.Fatalf("%s should be an image-producing tool", tool)
		}
	}
	for _, tool := range []string{ToolWebSearch, ToolGitHub, ToolOpenAIPlatform, ToolVisualize} {
		if !usesAnswerNowGate(tool) {
			t.Fatalf("%s should use the provider Answer now gate", tool)
		}
	}
	if usesAnswerNowGate(ToolCreateImage) {
		t.Fatal("create-image should not require the provider Answer now gate")
	}
}

func TestRenderedPromptMatchesRequiresTheSubmittedFingerprint(t *testing.T) {
	observation := renderedObservation{
		PromptCandidates: []string{"the submitted prompt"},
	}
	if !renderedPromptMatches(
		observation,
		fingerprintPrompt("the submitted prompt"),
	) {
		t.Fatal("same rendered prompt should prove identity")
	}
	if renderedPromptMatches(
		observation,
		fingerprintPrompt("a stale prompt"),
	) {
		t.Fatal("a stale rendered prompt must not prove identity")
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
