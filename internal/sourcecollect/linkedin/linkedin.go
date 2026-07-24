// Package linkedin contains pure LinkedIn collection policy. It deliberately
// has no browser, filesystem, Cobra, or artifact dependency.
package linkedin

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	directActivityPath = regexp.MustCompile(`^/posts/[A-Za-z0-9_-]+-activity-([0-9]+)(?:-[A-Za-z0-9_-]+)?/?$`)
	feedActivityPath   = regexp.MustCompile(`^/feed/update/urn:li:activity:([0-9]+)/?$`)
	companyPostsPath   = regexp.MustCompile(`^/company/([A-Za-z0-9-]+)/posts/?$`)
	localeHost         = regexp.MustCompile(`^[a-z]{2,3}\.linkedin\.com$`)
	decimal            = regexp.MustCompile(`^[0-9]+$`)
)

type Kind string

const (
	KindActivityThread Kind = "activity_thread"
	KindCompanyPosts   Kind = "company_posts"
)

type Request struct {
	URL        string `json:"url"`
	Kind       Kind   `json:"kind"`
	ActivityID string `json:"activity_id,omitempty"`
	Company    string `json:"company,omitempty"`
}

// Page is source-level pagination evidence. TerminalExtent is the observed
// scroll extent after the caller's current advancement attempt.
type Page struct {
	ActivityIDs       []string `json:"activity_ids"`
	TerminalExtent    int      `json:"terminal_extent"`
	ContinuationToken string   `json:"continuation_token,omitempty"`
}

type Traversal struct {
	seen       map[string]struct{}
	lastExtent int
	lastToken  string
	stable     int
}

type TraversalObservation struct {
	Added     int  `json:"added"`
	Exhausted bool `json:"exhausted"`
}

func NewTraversal() *Traversal { return &Traversal{seen: make(map[string]struct{})} }

// Observe requires two settled, unchanged no-progress cycles. An unresolved
// matching reply control or an unsettled page can never establish exhaustion.
func (t *Traversal) Observe(page Page, unresolvedControl, settled bool) TraversalObservation {
	added := 0
	for _, id := range page.ActivityIDs {
		if !isNonZeroDecimal(id) {
			continue
		}
		id = canonicalDecimal(id)
		if _, exists := t.seen[id]; exists {
			continue
		}
		t.seen[id] = struct{}{}
		added++
	}
	if added > 0 || !settled || unresolvedControl || page.TerminalExtent != t.lastExtent || page.ContinuationToken != t.lastToken {
		t.stable = 0
	} else {
		t.stable++
	}
	t.lastExtent = page.TerminalExtent
	t.lastToken = page.ContinuationToken
	return TraversalObservation{Added: added, Exhausted: settled && !unresolvedControl && t.stable >= 2}
}

func Parse(rawURL string) (Request, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Request{}, fmt.Errorf("unsupported LinkedIn URL")
	}
	if !isLinkedInHost(parsed.Hostname()) {
		return Request{}, fmt.Errorf("unsupported LinkedIn host")
	}
	if matches := directActivityPath.FindStringSubmatch(parsed.Path); len(matches) == 2 && isNonZeroDecimal(matches[1]) {
		return Request{URL: rawURL, Kind: KindActivityThread, ActivityID: canonicalDecimal(matches[1])}, nil
	}
	if matches := feedActivityPath.FindStringSubmatch(parsed.Path); len(matches) == 2 && isNonZeroDecimal(matches[1]) {
		return Request{URL: rawURL, Kind: KindActivityThread, ActivityID: canonicalDecimal(matches[1])}, nil
	}
	if matches := companyPostsPath.FindStringSubmatch(parsed.Path); len(matches) == 2 {
		return Request{URL: rawURL, Kind: KindCompanyPosts, Company: strings.ToLower(matches[1])}, nil
	}
	return Request{}, fmt.Errorf("unsupported LinkedIn route")
}

func ValidateFinalURL(request Request, finalURL string) (Request, error) {
	final, err := Parse(finalURL)
	if err != nil {
		return Request{}, fmt.Errorf("invalid final LinkedIn URL: %w", err)
	}
	if final.Kind != request.Kind || final.ActivityID != request.ActivityID || final.Company != request.Company {
		return Request{}, fmt.Errorf("LinkedIn identity changed during navigation")
	}
	return final, nil
}

func isLinkedInHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "linkedin.com" || host == "www.linkedin.com" || localeHost.MatchString(host)
}

func isNonZeroDecimal(value string) bool {
	return decimal.MatchString(value) && canonicalDecimal(value) != "0"
}

func canonicalDecimal(value string) string {
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0"
	}
	return value
}

// ProgressExpression captures only pagination evidence; record extraction stays
// in records.go so callers cannot mistake viewport count for terminal extent.
func ProgressExpression() string {
	return `({__cdp_cli_linkedin_progress__: true, terminal_extent: Math.max(document.documentElement.scrollHeight || 0, document.body && document.body.scrollHeight || 0)})`
}
