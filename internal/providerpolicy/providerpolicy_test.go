package providerpolicy

import (
	"reflect"
	"testing"
)

func TestNormalizeCanonicalizesAliasesAndSorts(t *testing.T) {
	got, err := Normalize([]string{" Microsoft-365-web ", "CHATGPT"})
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	want := []string{"chatgpt", "m365"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize = %#v, want %#v", got, want)
	}
}

func TestNormalizeRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name  string
		value []string
	}{
		{name: "blank", value: []string{"  "}},
		{name: "unknown", value: []string{"chat-gpt"}},
		{name: "local", value: []string{"local"}},
		{name: "duplicate canonical", value: []string{"chatgpt", "chatgpt-web"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Normalize(test.value); err == nil {
				t.Fatalf("Normalize(%#v) returned nil error", test.value)
			}
		})
	}
}

func TestPolicyDecisionsAreDefaultEnabledAndExplicitlyDisabled(t *testing.T) {
	policy, err := New([]string{"chatgpt-web"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if got := policy.Decision("chatgpt"); got.Reason != ReasonDisabledByConfig || got.Enabled {
		t.Fatalf("disabled decision = %+v", got)
	}
	if got := policy.Decision("m365"); got.Reason != ReasonEnabled || !got.Enabled {
		t.Fatalf("enabled decision = %+v", got)
	}
	if got := policy.Decision("typo"); got.Reason != ReasonUnknownProvider || got.Enabled {
		t.Fatalf("unknown decision = %+v", got)
	}
	if got := policy.DisabledIDs(); !reflect.DeepEqual(got, []string{"chatgpt"}) {
		t.Fatalf("DisabledIDs = %#v", got)
	}
}

func TestDescriptorsAreCanonicalAndDeterministicallySorted(t *testing.T) {
	descriptors := Descriptors()
	if len(descriptors) != 9 {
		t.Fatalf("descriptor count = %d, want 9", len(descriptors))
	}
	for index := 1; index < len(descriptors); index++ {
		if descriptors[index-1].ID >= descriptors[index].ID {
			t.Fatalf("descriptors are not sorted: %#v", descriptors)
		}
	}
	chatGPT, ok := DescriptorFor("chatgpt-web")
	if !ok || chatGPT.ID != ProviderChatGPT || chatGPT.TranscriptionID != "chatgpt-web" {
		t.Fatalf("ChatGPT descriptor = %+v, ok=%v", chatGPT, ok)
	}
	m365, ok := DescriptorFor("microsoft-365-web")
	if !ok || m365.ID != ProviderM365 || m365.TranscriptionID != "microsoft-365-web" {
		t.Fatalf("M365 descriptor = %+v, ok=%v", m365, ok)
	}
	bing, ok := DescriptorFor("bing-web")
	if !ok || bing.ID != ProviderBing || bing.TranscriptionID != "bing-web" || !bing.TranscriptionOnly {
		t.Fatalf("Bing descriptor = %+v, ok=%v", bing, ok)
	}
}

func TestOnlyStandaloneTranscriptionProvidersHaveTranscriptionIDs(t *testing.T) {
	want := map[ProviderID]string{
		ProviderBing:    "bing-web",
		ProviderChatGPT: "chatgpt-web",
		ProviderClaude:  "claude-web",
		ProviderGemini:  "gemini-web",
		ProviderM365:    "microsoft-365-web",
	}
	for _, descriptor := range Descriptors() {
		if descriptor.TranscriptionID == "" {
			continue
		}
		if want[descriptor.ID] != descriptor.TranscriptionID {
			t.Fatalf("provider %q exposes stale transcription ID %q", descriptor.ID, descriptor.TranscriptionID)
		}
		delete(want, descriptor.ID)
	}
	if len(want) != 0 {
		t.Fatalf("standalone transcription providers missing from policy: %#v", want)
	}
	for _, raw := range []string{"claude", "gemini"} {
		descriptor, ok := DescriptorFor(raw)
		if !ok {
			t.Fatalf("general %s provider disappeared", raw)
		}
		if descriptor.TranscriptionID != raw+"-web" || descriptor.TranscriptionOnly {
			t.Fatalf("general %s descriptor transcription mapping = %+v", raw, descriptor)
		}
	}
}
