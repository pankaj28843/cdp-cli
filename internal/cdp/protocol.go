package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type Protocol struct {
	Version ProtocolVersion `json:"version"`
	Domains []Domain        `json:"domains"`
}

type ProtocolVersion struct {
	Major string `json:"major"`
	Minor string `json:"minor"`
}

type Domain struct {
	Domain       string          `json:"domain"`
	Description  string          `json:"description,omitempty"`
	Experimental bool            `json:"experimental,omitempty"`
	Deprecated   bool            `json:"deprecated,omitempty"`
	Commands     json.RawMessage `json:"commands,omitempty"`
	Events       json.RawMessage `json:"events,omitempty"`
	Types        json.RawMessage `json:"types,omitempty"`
}

type DomainSummary struct {
	Name         string `json:"name"`
	Experimental bool   `json:"experimental"`
	Deprecated   bool   `json:"deprecated"`
	CommandCount int    `json:"command_count"`
	EventCount   int    `json:"event_count"`
	TypeCount    int    `json:"type_count"`
	Description  string `json:"description,omitempty"`
}

func FetchProtocol(ctx context.Context, protocolURL string) (Protocol, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, protocolURL, nil)
	if err != nil {
		return Protocol{}, fmt.Errorf("create protocol request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Protocol{}, fmt.Errorf("fetch protocol metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Protocol{}, fmt.Errorf("fetch protocol metadata: http status %d", resp.StatusCode)
	}

	var protocol Protocol
	if err := json.NewDecoder(resp.Body).Decode(&protocol); err != nil {
		return Protocol{}, fmt.Errorf("decode protocol metadata: %w", err)
	}
	return protocol, nil
}

func SummarizeDomains(domains []Domain) []DomainSummary {
	summaries := make([]DomainSummary, 0, len(domains))
	for _, domain := range domains {
		summaries = append(summaries, DomainSummary{
			Name:         domain.Domain,
			Experimental: domain.Experimental,
			Deprecated:   domain.Deprecated,
			CommandCount: countJSONArray(domain.Commands),
			EventCount:   countJSONArray(domain.Events),
			TypeCount:    countJSONArray(domain.Types),
			Description:  domain.Description,
		})
	}
	return summaries
}

func countJSONArray(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return 0
	}
	return len(values)
}
