package chatgpt

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

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
		{name: "deep research", input: "deep-research", want: ToolDeepResearch},
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

func TestResearchNeedsDedicatedCommandBeforeBrowser(t *testing.T) {
	result := Ask(context.Background(), AskConfig{Tool: ToolDeepResearch}, "research this")
	if result.Error == nil || result.Error.Code != "chatgpt_research_command_required" ||
		result.Operation != webagent.OperationAsk || result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchNotPerformed {
		t.Fatalf("ordinary ask accepted research tool: %+v", result)
	}
	continued := ContinueConversation(context.Background(),
		ContinueConfig{Tool: ToolDeepResearch}, "conversation-1", "research this")
	if continued.Error == nil || continued.Error.Code != "chatgpt_research_command_required" ||
		continued.Action == nil || continued.Action.Dispatch != webagent.DispatchNotPerformed {
		t.Fatalf("ordinary continuation accepted research tool: %+v", continued)
	}
}

func TestResearchSendRejectsMissingAppMention(t *testing.T) {
	client := &selectionActivationClient{evaluations: []json.RawMessage{
		exactSelectionGuard("Pro", ""),
		json.RawMessage(`{"route_ready":true,"editor_ready":true,"editor_count":1,"prompt_matches":true,"default_chat_surface":true,"intelligence_count":1,"selected_intelligence":"Pro","send_count":1,"send_ready":true,"assistant_count":0,"user_message_count":0,"selected_tool":""}`),
	}}
	dispatcher := chatgptSendDispatcher{prompt: "research this", intelligence: "Pro", tool: ToolDeepResearch}
	outcome, err := dispatcher.Dispatch(context.Background(), newSelectionActivationSession(t, client))
	if err == nil || outcome.Dispatch != browserflow.DispatchNotPerformed || outcome.RawInputAttempted {
		t.Fatalf("missing research mention reached Send: %+v %v", outcome, err)
	}
	for _, call := range client.calls {
		if call.method != "Runtime.evaluate" {
			t.Fatalf("missing mention caused raw input: %s", call.method)
		}
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
