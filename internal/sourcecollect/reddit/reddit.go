// Package reddit contains pure Reddit collection policy. It deliberately has
// no browser, filesystem, Cobra, or artifact dependency.
package reddit

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	listingPath = regexp.MustCompile(`^/r/([A-Za-z0-9_]{1,21})(?:/(best|hot|new|top))?/?$`)
	threadPath  = regexp.MustCompile(`^/r/([A-Za-z0-9_]{1,21})/comments/([A-Za-z0-9]+)(?:/[^/]+(?:/([A-Za-z0-9]+))?)?/?$`)
	userPath    = regexp.MustCompile(`^/(?:user|u)/([A-Za-z0-9_-]{1,20})(?:/(?:submitted|comments))?/?$`)
	threadID    = regexp.MustCompile(`^t3_[A-Za-z0-9]+$`)
)

type Kind string

const (
	KindSubredditListing Kind = "subreddit_listing"
	KindThread           Kind = "thread"
	KindUserProfile      Kind = "user_profile"
)

type Request struct {
	URL       string `json:"url"`
	Kind      Kind   `json:"kind"`
	Subreddit string `json:"subreddit,omitempty"`
	Sort      string `json:"sort,omitempty"`
	Window    string `json:"window,omitempty"`
	PostID    string `json:"post_id,omitempty"`
	CommentID string `json:"comment_id,omitempty"`
	Username  string `json:"username,omitempty"`
}

type Thread struct {
	ID           string `json:"id"`
	Permalink    string `json:"permalink"`
	Subreddit    string `json:"subreddit"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	Score        int    `json:"score"`
	CommentCount int    `json:"comment_count"`
}

type Page struct {
	Threads    []Thread `json:"threads"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// Traversal records source-level pagination progress independently of browser
// mechanics. A repeated rendered page is a transient plateau, not exhaustion:
// only an empty cursor together with no visible cards proves exhaustion.
type Traversal struct {
	threads []Thread
	seen    map[string]struct{}
	stalled int
}

type TraversalObservation struct {
	Added        int
	Stalled      int
	ReachedLimit bool
	Exhausted    bool
}

func NewTraversal() *Traversal {
	return &Traversal{seen: make(map[string]struct{})}
}

func (t *Traversal) Observe(page Page, limit int) TraversalObservation {
	added := 0
	for _, thread := range page.Threads {
		if len(t.threads) >= limit {
			break
		}
		if _, exists := t.seen[thread.ID]; exists {
			continue
		}
		t.seen[thread.ID] = struct{}{}
		t.threads = append(t.threads, thread)
		added++
	}
	if added == 0 {
		t.stalled++
	} else {
		t.stalled = 0
	}
	return TraversalObservation{
		Added:        added,
		Stalled:      t.stalled,
		ReachedLimit: len(t.threads) >= limit,
		Exhausted:    page.NextCursor == "" && len(page.Threads) == 0,
	}
}

func (t *Traversal) Threads() []Thread {
	return append([]Thread(nil), t.threads...)
}

func (t *Traversal) Count() int { return len(t.threads) }

func Parse(rawURL string) (Request, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawPath != "" {
		return Request{}, fmt.Errorf("unsupported Reddit listing URL")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host != "reddit.com" && host != "www.reddit.com" {
		return Request{}, fmt.Errorf("unsupported Reddit host")
	}
	if matches := threadPath.FindStringSubmatch(parsed.Path); len(matches) == 4 {
		if parsed.RawQuery != "" {
			return Request{}, fmt.Errorf("unsupported Reddit thread query")
		}
		return Request{URL: rawURL, Kind: KindThread, Subreddit: strings.ToLower(matches[1]), PostID: "t3_" + strings.ToLower(matches[2]), CommentID: strings.ToLower(matches[3])}, nil
	}
	if matches := userPath.FindStringSubmatch(parsed.Path); len(matches) == 2 {
		if parsed.RawQuery != "" {
			return Request{}, fmt.Errorf("unsupported Reddit user profile query")
		}
		return Request{URL: rawURL, Kind: KindUserProfile, Username: strings.ToLower(matches[1])}, nil
	}
	matches := listingPath.FindStringSubmatch(parsed.Path)
	if len(matches) != 3 {
		return Request{}, fmt.Errorf("unsupported Reddit route")
	}
	sort := strings.ToLower(matches[2])
	if sort == "" {
		sort = "hot"
	}
	query := parsed.Query()
	for key := range query {
		if key != "t" {
			return Request{}, fmt.Errorf("unsupported Reddit listing query")
		}
	}
	windows := query["t"]
	if len(windows) > 1 {
		return Request{}, fmt.Errorf("duplicate top time window")
	}
	window := ""
	if len(windows) == 1 {
		window = strings.ToLower(windows[0])
	}
	if sort != "top" && window != "" {
		return Request{}, fmt.Errorf("time window requires top sort")
	}
	if sort == "top" && window != "" && window != "hour" && window != "day" && window != "week" && window != "month" && window != "year" && window != "all" {
		return Request{}, fmt.Errorf("unsupported top time window")
	}
	return Request{URL: rawURL, Kind: KindSubredditListing, Subreddit: strings.ToLower(matches[1]), Sort: sort, Window: window}, nil
}

// ParseExpected classifies a discovered Reddit URL, then turns a route-specific
// command into an assertion rather than permission to reinterpret that URL.
func ParseExpected(rawURL string, expected Kind) (Request, error) {
	request, err := Parse(rawURL)
	if err != nil {
		return Request{}, err
	}
	if expected != "" && request.Kind != expected {
		return Request{}, fmt.Errorf("Reddit route is %s, expected %s", request.Kind, expected)
	}
	return request, nil
}

// ValidateFinalURL rejects navigation drift before a caller decodes listing cards.
func ValidateFinalURL(request Request, finalURL string) error {
	parsed, err := url.Parse(finalURL)
	if err != nil {
		return fmt.Errorf("invalid final Reddit listing URL: %w", err)
	}
	query := parsed.Query()
	query.Del("feedViewType") // Reddit may add a presentation-only view preference.
	parsed.RawQuery = query.Encode()
	final, err := Parse(parsed.String())
	if err != nil {
		return fmt.Errorf("invalid final Reddit listing URL: %w", err)
	}
	if final.Kind != request.Kind || final.Subreddit != request.Subreddit || final.Sort != request.Sort || final.Window != request.Window || final.PostID != request.PostID || final.CommentID != request.CommentID || final.Username != request.Username {
		return fmt.Errorf("Reddit listing identity changed during navigation")
	}
	return nil
}

func DecodePage(subreddit string, raw json.RawMessage) (Page, error) {
	var page Page
	if err := json.Unmarshal(raw, &page); err != nil {
		return Page{}, fmt.Errorf("decode Reddit listing page: %w", err)
	}
	seen := make(map[string]struct{}, len(page.Threads))
	for _, thread := range page.Threads {
		if !threadID.MatchString(thread.ID) || strings.ToLower(thread.Subreddit) != strings.ToLower(subreddit) || !strings.HasPrefix(strings.ToLower(thread.Permalink), "/r/"+strings.ToLower(subreddit)+"/comments/") || strings.TrimSpace(thread.Title) == "" {
			return Page{}, fmt.Errorf("invalid Reddit thread identity")
		}
		if _, exists := seen[thread.ID]; exists {
			return Page{}, fmt.Errorf("duplicate Reddit thread id %q", thread.ID)
		}
		seen[thread.ID] = struct{}{}
	}
	return page, nil
}

// CollectionExpression serializes only identity-qualified visible listing cards.
func CollectionExpression(request Request, limit int) string {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	return fmt.Sprintf(`(() => {
  const subreddit = %q;
  const limit = %d;
  const visible = (node) => { const style = node && getComputedStyle(node); return Boolean(node && node.isConnected && style && style.display !== "none" && style.visibility !== "hidden" && node.getClientRects().length); };
  const normalize = (value) => String(value || "").replace(/\s+/g, " ").trim();
  const threads = [], seen = new Set();
  for (const card of Array.from(document.querySelectorAll("shreddit-feed shreddit-post"))) {
    if (!visible(card)) continue;
    const id = card.getAttribute("id") || "", permalink = card.getAttribute("permalink") || "", cardSubreddit = (card.getAttribute("subreddit-name") || "").toLowerCase();
    if (!/^t3_[A-Za-z0-9]+$/.test(id) || cardSubreddit !== subreddit || !new RegExp("^/r/" + subreddit + "/comments/[A-Za-z0-9]+/", "i").test(permalink) || seen.has(id)) continue;
    const title = normalize(card.getAttribute("post-title")); if (!title) continue;
    seen.add(id); threads.push({id, permalink, subreddit: cardSubreddit, title, author: normalize(card.getAttribute("author")), score: Number(card.getAttribute("score") || 0), comment_count: Number(card.getAttribute("comment-count") || 0)});
    if (threads.length >= limit) break;
  }
  const cursorCard = Array.from(document.querySelectorAll("shreddit-feed shreddit-post[more-posts-cursor]")).pop();
  return {threads, next_cursor: cursorCard && cursorCard.getAttribute("more-posts-cursor") || ""};
})()`, request.Subreddit, limit)
}

// AdvanceExpression activates one visible, semantic listing continuation
// control. It intentionally avoids brittle generated classes and never clicks
// a thread link; scrolling remains the caller's fallback continuation action.
func AdvanceExpression() string {
	return `(() => {
  "__cdp_cli_reddit_advance__";
  const visible = (node) => { const style = node && getComputedStyle(node); return Boolean(node && node.isConnected && style && style.display !== "none" && style.visibility !== "hidden" && node.getClientRects().length); };
  const normalize = (value) => String(value || "").replace(/\s+/g, " ").trim();
  const label = (node) => normalize((node.getAttribute("aria-label") || "") + " " + (node.textContent || ""));
  const continuation = /(?:load|show|view)\s+(?:more|additional)|more\s+posts/i;
  for (const control of Array.from(document.querySelectorAll('shreddit-feed button, shreddit-feed [role="button"]'))) {
    if (!visible(control) || control.hasAttribute("disabled") || !continuation.test(label(control))) continue;
    control.click();
    return {attempted: true, action: "continuation_control"};
  }
  return {attempted: false, action: "scroll"};
})()`
}
