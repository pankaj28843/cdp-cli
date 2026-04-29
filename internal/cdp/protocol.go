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
	Source  string          `json:"source,omitempty"`
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

type EntityDescription struct {
	Domain       string          `json:"domain"`
	Kind         string          `json:"kind"`
	Name         string          `json:"name"`
	Path         string          `json:"path"`
	Description  string          `json:"description,omitempty"`
	Experimental bool            `json:"experimental,omitempty"`
	Deprecated   bool            `json:"deprecated,omitempty"`
	Schema       json.RawMessage `json:"schema"`
}

const (
	OfficialBrowserProtocolURL = "https://raw.githubusercontent.com/ChromeDevTools/devtools-protocol/master/json/browser_protocol.json"
	OfficialJSProtocolURL      = "https://raw.githubusercontent.com/ChromeDevTools/devtools-protocol/master/json/js_protocol.json"
)

type ProtocolHTTPError struct {
	URL        string
	StatusCode int
}

func (e ProtocolHTTPError) Error() string {
	return fmt.Sprintf("fetch protocol metadata: http status %d", e.StatusCode)
}

func FetchProtocol(ctx context.Context, protocolURL string) (Protocol, error) {
	protocol, err := fetchProtocolURL(ctx, protocolURL)
	if err != nil {
		return Protocol{}, err
	}
	protocol.Source = protocolURL
	return protocol, nil
}

func FetchOfficialProtocol(ctx context.Context) (Protocol, error) {
	return FetchProtocolSnapshot(ctx, OfficialBrowserProtocolURL, OfficialJSProtocolURL)
}

func FetchProtocolSnapshot(ctx context.Context, urls ...string) (Protocol, error) {
	if len(urls) == 0 {
		urls = []string{OfficialBrowserProtocolURL, OfficialJSProtocolURL}
	}
	var merged Protocol
	var sources []string
	for _, protocolURL := range urls {
		protocol, err := fetchProtocolURL(ctx, protocolURL)
		if err != nil {
			return Protocol{}, err
		}
		if merged.Version.Major == "" && merged.Version.Minor == "" {
			merged.Version = protocol.Version
		}
		merged.Domains = append(merged.Domains, protocol.Domains...)
		sources = append(sources, protocolURL)
	}
	merged.Source = strings.Join(sources, ",")
	return merged, nil
}

func fetchProtocolURL(ctx context.Context, protocolURL string) (Protocol, error) {
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
		return Protocol{}, ProtocolHTTPError{URL: protocolURL, StatusCode: resp.StatusCode}
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

func FilterSearchResultsByKind(results []SearchResult, kind string) []SearchResult {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return results
	}
	filtered := make([]SearchResult, 0, len(results))
	for _, result := range results {
		if result.Kind == kind {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func DescribeEntity(protocol Protocol, selector string) (EntityDescription, bool) {
	selector = strings.TrimSpace(selector)
	for _, domain := range protocol.Domains {
		if strings.EqualFold(selector, domain.Domain) {
			return describeDomain(domain), true
		}
		prefix, name, ok := strings.Cut(selector, ".")
		if !ok || !strings.EqualFold(prefix, domain.Domain) {
			continue
		}
		if desc, ok := describeItem(domain, "command", domain.Commands, name); ok {
			return desc, true
		}
		if desc, ok := describeItem(domain, "event", domain.Events, name); ok {
			return desc, true
		}
		if desc, ok := describeItem(domain, "type", domain.Types, name); ok {
			return desc, true
		}
	}
	return EntityDescription{}, false
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

func describeDomain(domain Domain) EntityDescription {
	schema, _ := json.Marshal(domain)
	return EntityDescription{
		Domain:       domain.Domain,
		Kind:         "domain",
		Name:         domain.Domain,
		Path:         domain.Domain,
		Description:  domain.Description,
		Experimental: domain.Experimental,
		Deprecated:   domain.Deprecated,
		Schema:       schema,
	}
}

func describeItem(domain Domain, kind string, raw json.RawMessage, name string) (EntityDescription, bool) {
	var items []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return EntityDescription{}, false
	}
	for _, item := range items {
		var meta ProtocolItem
		if json.Unmarshal(item, &meta) != nil || !strings.EqualFold(meta.Name, name) {
			continue
		}
		return EntityDescription{
			Domain:       domain.Domain,
			Kind:         kind,
			Name:         meta.Name,
			Path:         domain.Domain + "." + meta.Name,
			Description:  meta.Description,
			Experimental: meta.Experimental || domain.Experimental,
			Deprecated:   meta.Deprecated || domain.Deprecated,
			Schema:       item,
		}, true
	}
	return EntityDescription{}, false
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
