package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
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

type ProtocolItem struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Experimental bool   `json:"experimental,omitempty"`
	Deprecated   bool   `json:"deprecated,omitempty"`
}

type SearchResult struct {
	Domain       string `json:"domain"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	Description  string `json:"description,omitempty"`
	Experimental bool   `json:"experimental,omitempty"`
	Deprecated   bool   `json:"deprecated,omitempty"`
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

func SearchProtocol(protocol Protocol, query string, limit int) []SearchResult {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil
	}
	var results []SearchResult
	for _, domain := range protocol.Domains {
		domainResult := SearchResult{
			Domain:       domain.Domain,
			Kind:         "domain",
			Name:         domain.Domain,
			Path:         domain.Domain,
			Description:  domain.Description,
			Experimental: domain.Experimental,
			Deprecated:   domain.Deprecated,
		}
		if matchesSearch(domainResult, terms) {
			results = append(results, domainResult)
		}
		results = append(results, searchItems(domain, "command", domain.Commands, terms)...)
		results = append(results, searchItems(domain, "event", domain.Events, terms)...)
		results = append(results, searchItems(domain, "type", domain.Types, terms)...)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return searchRank(results[i], terms) < searchRank(results[j], terms)
	})
	if limit > 0 && len(results) > limit {
		return results[:limit]
	}
	return results
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

func searchItems(domain Domain, kind string, raw json.RawMessage, terms []string) []SearchResult {
	var items []ProtocolItem
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return nil
	}
	results := make([]SearchResult, 0, len(items))
	for _, item := range items {
		result := SearchResult{
			Domain:       domain.Domain,
			Kind:         kind,
			Name:         item.Name,
			Path:         domain.Domain + "." + item.Name,
			Description:  item.Description,
			Experimental: item.Experimental || domain.Experimental,
			Deprecated:   item.Deprecated || domain.Deprecated,
		}
		if matchesSearch(result, terms) {
			results = append(results, result)
		}
	}
	return results
}

func matchesSearch(result SearchResult, terms []string) bool {
	haystack := strings.ToLower(result.Domain + " " + result.Kind + " " + result.Name + " " + result.Path + " " + result.Description)
	for _, term := range terms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func searchRank(result SearchResult, terms []string) int {
	name := strings.ToLower(result.Path)
	first := terms[0]
	switch {
	case strings.EqualFold(result.Path, first), strings.EqualFold(result.Name, first):
		return 0
	case strings.HasPrefix(name, first):
		return 1
	case strings.Contains(name, first):
		return 2
	default:
		return 3
	}
}
