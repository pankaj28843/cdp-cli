package chatgpt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestAskRejectsInvalidPromptBeforeBrowserWork(t *testing.T) {
	for _, test := range []struct {
		name   string
		prompt string
		code   string
	}{
		{
			name: "empty",
			code: "chatgpt_prompt_required",
		},
		{
			name:   "too_long",
			prompt: strings.Repeat("a", MaxPromptCharacters+1),
			code:   "chatgpt_prompt_too_long",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := Ask(context.Background(), AskConfig{}, test.prompt)
			if result.OK ||
				result.Stage != webagent.StagePlanned ||
				result.Action == nil ||
				result.Action.Dispatch != webagent.DispatchNotPerformed ||
				result.Action.RawInputCount != 0 ||
				result.Error == nil ||
				result.Error.Code != test.code ||
				!result.Error.RetrySafe ||
				result.Cleanup.State != webagent.CleanupNotRequired {
				t.Fatalf("Ask invalid prompt result = %+v", result)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("Ask invalid prompt validation: %v", err)
			}
		})
	}
}

func TestAskRejectsInvalidAttachmentBeforeBrowserWork(t *testing.T) {
	result := Ask(
		context.Background(),
		AskConfig{FilePath: filepath.Join(t.TempDir(), "missing.txt")},
		"review this attachment",
	)
	if result.OK ||
		result.Stage != webagent.StagePlanned ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchNotPerformed ||
		result.Action.RawInputCount != 0 ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_attachment_invalid" ||
		!result.Error.RetrySafe ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("Ask invalid attachment result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Ask invalid attachment validation: %v", err)
	}
}

func TestRenderedPromptMatchesAnyExactCandidate(t *testing.T) {
	prompt := "first line\nsecond line  "
	fingerprint := fingerprintPrompt(prompt)
	if !renderedPromptMatches(renderedObservation{
		PromptCandidates: []string{
			"lossy rendered representation",
			prompt,
		},
	}, fingerprint) {
		t.Fatal("exact candidate did not prove prompt identity")
	}
	if renderedPromptMatches(renderedObservation{
		PromptCandidates: []string{
			strings.TrimSpace(prompt),
		},
	}, fingerprint) {
		t.Fatal("lossy whitespace normalization proved prompt identity")
	}
}

func TestStrictDeleteIdentityAcceptsCurrentAndLegacyShapes(t *testing.T) {
	current := deleteObservation{
		RouteMatches:    true,
		SidebarState:    "open",
		LinkCount:       1,
		RowButtonCount:  0,
		PageButtonCount: 1,
	}
	if !strictDeleteIdentity(current) {
		t.Fatal("current exact link plus exact id button was rejected")
	}
	legacy := current
	legacy.RowButtonCount = 1
	legacy.RowButtonNameOK = true
	if !strictDeleteIdentity(legacy) {
		t.Fatal("legacy exact row trigger was rejected")
	}
	ambiguous := legacy
	ambiguous.RowButtonCount = 2
	if strictDeleteIdentity(ambiguous) {
		t.Fatal("ambiguous legacy row triggers were accepted")
	}
	missingPageButton := current
	missingPageButton.PageButtonCount = 0
	if strictDeleteIdentity(missingPageButton) {
		t.Fatal("missing exact conversation options button was accepted")
	}
}

func TestDeleteRejectsInvalidIDBeforeBrowserWork(t *testing.T) {
	result := DeleteConversation(
		context.Background(),
		DeleteConfig{},
		"invalid/id",
	)
	if result.OK ||
		result.Stage != webagent.StagePlanned ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchNotPerformed ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_invalid_conversation_id" ||
		!result.Error.RetrySafe ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("DeleteConversation invalid id result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("DeleteConversation invalid id validation: %v", err)
	}
}

func TestContinueRejectsInvalidInputBeforeBrowserWork(t *testing.T) {
	for _, test := range []struct {
		name           string
		conversationID string
		prompt         string
		code           string
	}{
		{
			name:           "invalid_conversation",
			conversationID: "invalid/id",
			prompt:         "continue",
			code:           "chatgpt_invalid_conversation_id",
		},
		{
			name:           "empty_prompt",
			conversationID: "conversation-safe",
			code:           "chatgpt_prompt_required",
		},
		{
			name:           "long_prompt",
			conversationID: "conversation-safe",
			prompt:         strings.Repeat("a", MaxPromptCharacters+1),
			code:           "chatgpt_prompt_too_long",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := ContinueConversation(
				context.Background(),
				ContinueConfig{},
				test.conversationID,
				test.prompt,
			)
			if result.OK ||
				result.Stage != webagent.StagePlanned ||
				result.Action == nil ||
				result.Action.Dispatch != webagent.DispatchNotPerformed ||
				result.Action.RawInputCount != 0 ||
				result.Error == nil ||
				result.Error.Code != test.code ||
				!result.Error.RetrySafe ||
				result.Cleanup.State != webagent.CleanupNotRequired {
				t.Fatalf("ContinueConversation invalid input result = %+v", result)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("ContinueConversation invalid input validation: %v", err)
			}
		})
	}
}
