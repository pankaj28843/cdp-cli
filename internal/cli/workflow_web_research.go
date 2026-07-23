package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"net/url"
)

type webResearchQuery struct {
	Text       string `json:"query"`
	TimeFilter string `json:"time_filter,omitempty"`
}

type webResearchCandidate struct {
	QueryIndex int    `json:"-"`
	Serp       string `json:"serp"`
	Query      string `json:"query"`
	TimeFilter string `json:"time_filter,omitempty"`
	SerpPage   int    `json:"serp_page"`
	RankOnPage int    `json:"rank_on_page"`
	GlobalRank int    `json:"global_rank"`
	Rank       int    `json:"rank"`
	Title      string `json:"title"`
	Source     string `json:"source,omitempty"`
	Preview    string `json:"preview,omitempty"`
	URL        string `json:"url"`
	Type       string `json:"type,omitempty"`
}

type webResearchQueryCoverage struct {
	Query               string `json:"query"`
	TimeFilter          string `json:"time_filter,omitempty"`
	ProducedCandidates  int    `json:"produced_candidates"`
	DuplicateCandidates int    `json:"duplicate_candidates"`
	SelectedCandidates  int    `json:"selected_candidates"`
	OmittedByCap        int    `json:"omitted_by_cap"`
	BlockedPages        int    `json:"blocked_pages"`
	FailedPages         int    `json:"failed_pages"`
	Productive          bool   `json:"productive"`
	Represented         bool   `json:"represented"`
}

func selectFairWebResearchCandidates(queries []webResearchQuery, pool []webResearchCandidate, maxCandidates int) ([]webResearchCandidate, []webResearchQueryCoverage) {
	coverage := make([]webResearchQueryCoverage, len(queries))
	buckets := make([][]webResearchCandidate, len(queries))
	for index, query := range queries {
		coverage[index] = webResearchQueryCoverage{Query: query.Text, TimeFilter: query.TimeFilter}
	}
	for _, candidate := range pool {
		if candidate.QueryIndex < 0 || candidate.QueryIndex >= len(queries) || normalizeResearchURL(candidate.URL) == "" {
			continue
		}
		buckets[candidate.QueryIndex] = append(buckets[candidate.QueryIndex], candidate)
		coverage[candidate.QueryIndex].ProducedCandidates++
		coverage[candidate.QueryIndex].Productive = true
	}

	selected := make([]webResearchCandidate, 0)
	positions := make([]int, len(queries))
	seen := map[string]bool{}
	for {
		progressed := false
		for queryIndex := range buckets {
			for positions[queryIndex] < len(buckets[queryIndex]) {
				candidate := buckets[queryIndex][positions[queryIndex]]
				positions[queryIndex]++
				progressed = true
				key := normalizeResearchURL(candidate.URL)
				if seen[key] {
					coverage[queryIndex].DuplicateCandidates++
					continue
				}
				seen[key] = true
				if maxCandidates > 0 && len(selected) >= maxCandidates {
					coverage[queryIndex].OmittedByCap++
					continue
				}
				selected = append(selected, candidate)
				coverage[queryIndex].SelectedCandidates++
				coverage[queryIndex].Represented = true
				break
			}
		}
		if !progressed {
			break
		}
	}
	return selected, coverage
}

func readWebResearchQueries(path string) ([]webResearchQuery, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, commandError("usage", "usage", "--query-file is required", ExitUsage, []string{"cdp workflow web-research serp --query-file tmp/queries.txt --json"})
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, commandError("read_failed", "filesystem", fmt.Sprintf("read query file %s: %v", path, err), ExitUsage, []string{"printf 'agentic engineering\\n' > tmp/queries.txt"})
	}
	queries := make([]webResearchQuery, 0)
	for lineIndex, rawLine := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(rawLine, "\t")
		if len(parts) > 2 {
			return nil, invalidWebResearchQueryRow(lineIndex+1, "expected query or query<TAB>Google tbs time filter; found more than two columns")
		}
		query := webResearchQuery{Text: strings.TrimSpace(parts[0])}
		if query.Text == "" {
			return nil, invalidWebResearchQueryRow(lineIndex+1, "query column must not be empty")
		}
		if len(parts) == 2 {
			query.TimeFilter = strings.TrimSpace(parts[1])
		}
		queries = append(queries, query)
	}
	if len(queries) == 0 {
		return nil, commandError("usage", "usage", "query file contained no queries", ExitUsage, []string{"printf 'agentic engineering\\n' > tmp/queries.txt"})
	}
	return queries, nil
}

func invalidWebResearchQueryRow(line int, detail string) error {
	return commandError(
		"usage",
		"usage",
		fmt.Sprintf("invalid query file line %d: %s", line, detail),
		ExitUsage,
		[]string{"printf '%s\\t%s\\n' 'agentic engineering' 'cdr:1,cd_min:07/01/2026,cd_max:07/01/2026' > tmp/queries.txt"},
	)
}

func readWebResearchURLs(path string, maxPages int) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, commandError("usage", "usage", "--url-file is required", ExitUsage, []string{"cdp workflow web-research extract --url-file tmp/urls.txt --json"})
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, commandError("read_failed", "filesystem", fmt.Sprintf("read URL file %s: %v", path, err), ExitUsage, []string{"printf 'https://example.com\\n' > tmp/urls.txt"})
	}
	urls := make([]string, 0)
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key := normalizeResearchURL(line)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		urls = append(urls, line)
		if maxPages > 0 && len(urls) >= maxPages {
			break
		}
	}
	if len(urls) == 0 {
		return nil, commandError("usage", "usage", "URL file contained no HTTP(S) URLs", ExitUsage, []string{"printf 'https://example.com\\n' > tmp/urls.txt"})
	}
	return urls, nil
}

func webResearchSupportedSERPs() []string {
	return []string{"google", "bing", "brave", "duckduckgo", "kagi"}
}

func isWebResearchSupportedSERP(serp string) bool {
	for _, supported := range webResearchSupportedSERPs() {
		if serp == supported {
			return true
		}
	}
	return false
}

func webResearchSERPList() string {
	return strings.Join(webResearchSupportedSERPs(), ", ")
}

func parseWebResearchSERPs(value string) ([]string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return []string{"google"}, nil
	}
	if value == "all" {
		return webResearchSupportedSERPs(), nil
	}
	seen := map[string]bool{}
	var engines []string
	for _, part := range strings.Split(value, ",") {
		engine := strings.TrimSpace(strings.ToLower(part))
		if engine == "" {
			continue
		}
		if !isWebResearchSupportedSERP(engine) {
			return nil, fmt.Errorf("unsupported SERP %q", engine)
		}
		if !seen[engine] {
			seen[engine] = true
			engines = append(engines, engine)
		}
	}
	if len(engines) == 0 {
		return []string{"google"}, nil
	}
	return engines, nil
}

func webResearchSearchURL(serp, query, timeFilter string, page int) string {
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * 10
	values := url.Values{}
	values.Set("q", query)
	switch serp {
	case "bing":
		if offset > 0 {
			values.Set("first", strconv.Itoa(offset+1))
		}
		return "https://www.bing.com/search?" + values.Encode()
	case "brave":
		if offset > 0 {
			values.Set("offset", strconv.Itoa(offset))
		}
		return "https://search.brave.com/search?" + values.Encode()
	case "duckduckgo":
		if offset > 0 {
			values.Set("s", strconv.Itoa(offset))
		}
		return "https://duckduckgo.com/?" + values.Encode()
	case "kagi":
		if page > 1 {
			values.Set("page", strconv.Itoa(page))
		}
		return "https://kagi.com/search?" + values.Encode()
	default:
		values.Set("safe", "active")
		if strings.TrimSpace(timeFilter) != "" {
			values.Set("tbs", strings.TrimSpace(timeFilter))
		}
		if offset > 0 {
			values.Set("start", strconv.Itoa(offset))
		}
		return "https://www.google.com/search?" + values.Encode()
	}
}

func normalizeResearchURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	parsed.Fragment = ""
	return parsed.String()
}

func webResearchSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 64 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "item"
	}
	return out
}

func webResearchURLSlug(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return webResearchSlug(rawURL)
	}
	return webResearchSlug(parsed.Host + " " + strings.Trim(parsed.Path, "/"))
}

func webResearchCandidatesTSV(candidates []webResearchCandidate) string {
	var b strings.Builder
	b.WriteString("serp\tglobal_rank\tserp_page\trank_on_page\tquery\ttime_filter\ttitle\tsource\turl\tpreview\n")
	for _, candidate := range candidates {
		fields := []string{candidate.Serp, strconv.Itoa(candidate.GlobalRank), strconv.Itoa(candidate.SerpPage), strconv.Itoa(candidate.RankOnPage), candidate.Query, candidate.TimeFilter, candidate.Title, candidate.Source, candidate.URL, candidate.Preview}
		for i, field := range fields {
			field = strings.ReplaceAll(field, "\t", " ")
			field = strings.ReplaceAll(field, "\n", " ")
			if i > 0 {
				b.WriteByte('\t')
			}
			b.WriteString(field)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
