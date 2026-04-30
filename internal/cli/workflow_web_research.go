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
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		query := webResearchQuery{Text: line}
		if strings.Contains(line, "\t") {
			parts := strings.SplitN(line, "\t", 2)
			query.Text = strings.TrimSpace(parts[0])
			query.TimeFilter = strings.TrimSpace(parts[1])
		}
		if query.Text != "" {
			queries = append(queries, query)
		}
	}
	if len(queries) == 0 {
		return nil, commandError("usage", "usage", "query file contained no queries", ExitUsage, []string{"printf 'agentic engineering\\n' > tmp/queries.txt"})
	}
	return queries, nil
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

func googleSearchURL(query, timeFilter string, start int) string {
	values := url.Values{}
	values.Set("q", query)
	values.Set("safe", "active")
	if strings.TrimSpace(timeFilter) != "" {
		values.Set("tbs", strings.TrimSpace(timeFilter))
	}
	if start > 0 {
		values.Set("start", strconv.Itoa(start))
	}
	return "https://www.google.com/search?" + values.Encode()
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
	b.WriteString("global_rank\tserp_page\trank_on_page\tquery\ttime_filter\ttitle\tsource\turl\tpreview\n")
	for _, candidate := range candidates {
		fields := []string{strconv.Itoa(candidate.GlobalRank), strconv.Itoa(candidate.SerpPage), strconv.Itoa(candidate.RankOnPage), candidate.Query, candidate.TimeFilter, candidate.Title, candidate.Source, candidate.URL, candidate.Preview}
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
