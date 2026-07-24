package cli

import (
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/sourcecollect/arxiv"
	"github.com/pankaj28843/cdp-cli/internal/sourcecollect/hackernews"
	"github.com/pankaj28843/cdp-cli/internal/sourcecollect/linkedin"
	"github.com/pankaj28843/cdp-cli/internal/sourcecollect/reddit"
	"github.com/pankaj28843/cdp-cli/internal/sourcecollect/x"
)

type markdownRecord struct {
	Kind, URL, Byline, Title, Body string
	Depth                          int
}

func sourceCollectionMarkdown(source, kind, requestURL string, records []markdownRecord, workflow map[string]any, coverage sourceCollectionCoverage) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# %s collection\n\n", source)
	fmt.Fprintf(&out, "- Kind: `%s`\n- Source: <%s>\n- Records: %v of %v\n- Status: `%v`", kind, requestURL, workflow["count"], workflow["limit"], workflow["status"])
	if reason, _ := workflow["partial_reason"].(string); reason != "" {
		fmt.Fprintf(&out, "\n- Partial reason: `%s`", reason)
	}
	fmt.Fprintf(&out, "\n- Continuation: `%s`\n- Termination evidence: %s", coverage.Continuation, strings.Join(coverage.TerminationEvidence, ", "))
	out.WriteString("\n\n## Records\n")
	for index, record := range records {
		fmt.Fprintf(&out, "\n### %d. %s\n\n", index+1, record.Kind)
		if record.URL != "" {
			fmt.Fprintf(&out, "Source: <%s>  \n", record.URL)
		}
		if record.Byline != "" {
			fmt.Fprintf(&out, "%s  \n", record.Byline)
		}
		if record.Title != "" {
			fmt.Fprintf(&out, "**%s**\n\n", record.Title)
		}
		if record.Body != "" {
			fmt.Fprintf(&out, "%s\n", strings.TrimSpace(record.Body))
		}
	}
	return strings.TrimSpace(out.String())
}

func absoluteSourceURL(origin, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return origin + path
}

func xCollectionMarkdown(requestURL string, kind x.Kind, records []x.Record, workflow map[string]any, coverage sourceCollectionCoverage) string {
	items := make([]markdownRecord, 0, len(records))
	for _, r := range records {
		byline := "@" + r.Handle
		if r.Timestamp != "" {
			byline += " · " + r.Timestamp
		}
		items = append(items, markdownRecord{Kind: string(r.Kind), URL: absoluteSourceURL("https://x.com", r.CanonicalURL), Byline: byline, Body: r.Body})
	}
	return sourceCollectionMarkdown("X", string(kind), requestURL, items, workflow, coverage)
}
func redditCollectionMarkdown(requestURL string, kind reddit.Kind, records []reddit.Record, threads []reddit.Thread, workflow map[string]any, coverage sourceCollectionCoverage) string {
	items := make([]markdownRecord, 0, len(records)+len(threads))
	for _, r := range records {
		items = append(items, markdownRecord{Kind: string(r.Kind), URL: absoluteSourceURL("https://www.reddit.com", r.CanonicalURL), Byline: "u/" + r.Author, Title: r.Title, Body: r.Body})
	}
	for _, t := range threads {
		items = append(items, markdownRecord{Kind: "listing thread", URL: absoluteSourceURL("https://www.reddit.com", t.Permalink), Byline: fmt.Sprintf("u/%s · %d points · %d comments", t.Author, t.Score, t.CommentCount), Title: t.Title})
	}
	return sourceCollectionMarkdown("Reddit", string(kind), requestURL, items, workflow, coverage)
}
func linkedInCollectionMarkdown(requestURL string, kind linkedin.Kind, records []linkedin.Record, workflow map[string]any, coverage sourceCollectionCoverage) string {
	items := make([]markdownRecord, 0, len(records))
	for _, r := range records {
		byline := r.Company
		if r.Timestamp != "" {
			if byline != "" {
				byline += " · "
			}
			byline += r.Timestamp
		}
		items = append(items, markdownRecord{Kind: string(r.Kind), URL: absoluteSourceURL("https://www.linkedin.com", r.CanonicalURL), Byline: byline, Body: r.Body})
	}
	return sourceCollectionMarkdown("LinkedIn", string(kind), requestURL, items, workflow, coverage)
}
func hackerNewsCollectionMarkdown(requestURL string, records []hackernews.Record, workflow map[string]any, coverage sourceCollectionCoverage) string {
	items := make([]markdownRecord, 0, len(records))
	for _, r := range records {
		byline := r.Author
		if r.Timestamp != "" {
			if byline != "" {
				byline += " · "
			}
			byline += r.Timestamp
		}
		items = append(items, markdownRecord{Kind: string(r.Kind), URL: absoluteSourceURL("https://news.ycombinator.com", r.CanonicalURL), Byline: byline, Title: r.Title, Body: r.Body, Depth: r.Depth})
	}
	return sourceCollectionMarkdown("Hacker News", "thread", requestURL, items, workflow, coverage)
}
func arxivCollectionMarkdown(requestURL string, paper arxiv.Paper, references []arxiv.Reference, workflow map[string]any, coverage sourceCollectionCoverage) string {
	items := []markdownRecord{{Kind: "paper", URL: absoluteSourceURL("https://arxiv.org", paper.CanonicalURL), Title: paper.Title}}
	for _, r := range references {
		items = append(items, markdownRecord{Kind: "reference", Title: r.ID, Body: r.Text})
	}
	return sourceCollectionMarkdown("arXiv", "paper", requestURL, items, workflow, coverage)
}
