// Package providerpolicy owns the shared provider identity and owner policy.
// It deliberately contains no browser, HTTP, or CLI dependencies.
package providerpolicy

import (
	"fmt"
	"sort"
	"strings"
)

type ProviderID string

const (
	ProviderAlex        ProviderID = "alex"
	ProviderBing        ProviderID = "bing"
	ProviderChatGPT     ProviderID = "chatgpt"
	ProviderClaude      ProviderID = "claude"
	ProviderGemini      ProviderID = "gemini"
	ProviderGrok        ProviderID = "grok"
	ProviderM365        ProviderID = "m365"
	ProviderPerplexity  ProviderID = "perplexity"
	ProviderTripadvisor ProviderID = "tripadvisor"
)

type Reason string

const (
	ReasonEnabled          Reason = "enabled"
	ReasonDisabledByConfig Reason = "disabled_by_config"
	ReasonUnknownProvider  Reason = "unknown_provider"
)

type Descriptor struct {
	ID                ProviderID
	DisplayName       string
	Aliases           []string
	TranscriptionID   string
	TranscriptionOnly bool
}

var descriptors = []Descriptor{
	{ID: ProviderAlex, DisplayName: "Ask Alex", Aliases: []string{"alex", "ask-alex"}},
	{ID: ProviderBing, DisplayName: "Bing Voice", Aliases: []string{"bing", "bing-web", "bing-voice"}, TranscriptionID: "bing-web", TranscriptionOnly: true},
	{ID: ProviderChatGPT, DisplayName: "ChatGPT", Aliases: []string{"chatgpt", "chatgpt-web"}, TranscriptionID: "chatgpt-web"},
	{ID: ProviderClaude, DisplayName: "Claude", Aliases: []string{"claude", "claude-web"}},
	{ID: ProviderGemini, DisplayName: "Gemini", Aliases: []string{"gemini", "gemini-web"}},
	{ID: ProviderGrok, DisplayName: "Grok", Aliases: []string{"grok", "grok-web"}},
	{ID: ProviderM365, DisplayName: "Microsoft 365 Copilot", Aliases: []string{
		"m365", "microsoft365", "microsoft-365", "microsoft365-web", "microsoft-365-web", "copilot",
	}, TranscriptionID: "microsoft-365-web"},
	{ID: ProviderPerplexity, DisplayName: "Perplexity", Aliases: []string{"perplexity", "perplexity-web"}},
	{ID: ProviderTripadvisor, DisplayName: "Tripadvisor", Aliases: []string{"tripadvisor", "tripadvisor-web"}},
}

type Decision struct {
	ID              ProviderID `json:"provider"`
	DisplayName     string     `json:"display_name"`
	Enabled         bool       `json:"enabled"`
	Reason          Reason     `json:"reason"`
	TranscriptionID string     `json:"transcription_id,omitempty"`
}

type Policy struct {
	disabled map[ProviderID]struct{}
	ordered  []ProviderID
}

func Descriptors() []Descriptor {
	result := make([]Descriptor, len(descriptors))
	for index, descriptor := range descriptors {
		result[index] = Descriptor{
			ID:                descriptor.ID,
			DisplayName:       descriptor.DisplayName,
			Aliases:           append([]string(nil), descriptor.Aliases...),
			TranscriptionID:   descriptor.TranscriptionID,
			TranscriptionOnly: descriptor.TranscriptionOnly,
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func DescriptorFor(raw string) (Descriptor, bool) {
	normalized := normalize(raw)
	for _, descriptor := range descriptors {
		for _, alias := range descriptor.Aliases {
			if normalize(alias) == normalized {
				return descriptor, true
			}
		}
	}
	return Descriptor{}, false
}

func Canonicalize(raw string) (string, bool) {
	descriptor, ok := DescriptorFor(raw)
	if !ok {
		return "", false
	}
	return string(descriptor.ID), true
}

func Normalize(raw []string) ([]string, error) {
	canonical := make([]string, 0, len(raw))
	seen := make(map[ProviderID]struct{}, len(raw))
	for _, value := range raw {
		trimmed := normalize(value)
		if trimmed == "" {
			return nil, fmt.Errorf("agents.disabled_providers contains a blank provider")
		}
		if trimmed == "local" {
			return nil, fmt.Errorf("agents.disabled_providers cannot contain local; local transcription is not an online provider")
		}
		descriptor, ok := DescriptorFor(trimmed)
		if !ok {
			return nil, fmt.Errorf("agents.disabled_providers contains unknown provider %q", value)
		}
		if _, exists := seen[descriptor.ID]; exists {
			return nil, fmt.Errorf("agents.disabled_providers contains duplicate provider %q", value)
		}
		seen[descriptor.ID] = struct{}{}
		canonical = append(canonical, string(descriptor.ID))
	}
	sort.Strings(canonical)
	return canonical, nil
}

func New(raw []string) (Policy, error) {
	canonical, err := Normalize(raw)
	if err != nil {
		return Policy{}, err
	}
	policy := Policy{disabled: make(map[ProviderID]struct{}, len(canonical)), ordered: canonicalIDs()}
	for _, value := range canonical {
		policy.disabled[ProviderID(value)] = struct{}{}
	}
	return policy, nil
}

func Default() Policy {
	policy, _ := New(nil)
	return policy
}

func (p Policy) DisabledIDs() []string {
	result := make([]string, 0, len(p.disabled))
	for _, id := range p.ordered {
		if _, disabled := p.disabled[id]; disabled {
			result = append(result, string(id))
		}
	}
	return result
}

func (p Policy) Decision(raw string) Decision {
	descriptor, ok := DescriptorFor(raw)
	if !ok {
		return Decision{ID: ProviderID(normalize(raw)), Enabled: false, Reason: ReasonUnknownProvider}
	}
	_, disabled := p.disabled[descriptor.ID]
	decision := Decision{
		ID:              descriptor.ID,
		DisplayName:     descriptor.DisplayName,
		Enabled:         !disabled,
		Reason:          ReasonEnabled,
		TranscriptionID: descriptor.TranscriptionID,
	}
	if disabled {
		decision.Reason = ReasonDisabledByConfig
	}
	return decision
}

func (p Policy) IsEnabled(raw string) bool {
	decision := p.Decision(raw)
	return decision.Reason == ReasonEnabled
}

func (p Policy) IsDisabled(raw string) bool {
	decision := p.Decision(raw)
	return decision.Reason == ReasonDisabledByConfig
}

func canonicalIDs() []ProviderID {
	ids := make([]ProviderID, 0, len(descriptors))
	for _, descriptor := range Descriptors() {
		ids = append(ids, descriptor.ID)
	}
	return ids
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
