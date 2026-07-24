// Package x contains pure X collection policy. It deliberately has no browser,
// filesystem, Cobra, or artifact dependency.
package x

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	handlePath  = `[A-Za-z0-9_]{1,15}`
	postPath    = regexp.MustCompile(`^/(` + handlePath + `)/status/([0-9]+)(?:/photo/([1-9][0-9]*))?/?$`)
	profilePath = regexp.MustCompile(`^/(` + handlePath + `)(?:/(with_replies))?/?$`)
)

type Kind string

const (
	KindPostThread   Kind = "post_thread"
	KindProfilePosts Kind = "profile_posts"
)

type Request struct {
	URL             string `json:"url"`
	Kind            Kind   `json:"kind"`
	Handle          string `json:"handle"`
	StatusID        string `json:"status_id,omitempty"`
	MediaIndex      int    `json:"media_index,omitempty"`
	Surface         string `json:"surface,omitempty"`
	RequestedHandle string `json:"requested_handle,omitempty"`
	HandleChanged   bool   `json:"handle_changed,omitempty"`
}

// Page is source-level pagination evidence. TerminalExtent is the observed
// scroll extent after the caller's current advancement attempt.
type Page struct {
	StatusIDs         []string `json:"status_ids"`
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

// Observe requires two no-progress cycles at an unchanged terminal extent.
// Callers must pass unresolvedControl=true while any applicable expansion
// control remains, which always prevents an exhausted result.
func (t *Traversal) Observe(page Page, unresolvedControl bool) TraversalObservation {
	added := 0
	for _, id := range page.StatusIDs {
		if id == "" {
			continue
		}
		if _, exists := t.seen[id]; exists {
			continue
		}
		t.seen[id] = struct{}{}
		added++
	}
	if added > 0 || page.TerminalExtent != t.lastExtent || page.ContinuationToken != t.lastToken || unresolvedControl {
		t.stable = 0
	} else {
		t.stable++
	}
	t.lastExtent = page.TerminalExtent
	t.lastToken = page.ContinuationToken
	return TraversalObservation{Added: added, Exhausted: !unresolvedControl && t.stable >= 2}
}

func Parse(rawURL string) (Request, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawPath != "" {
		return Request{}, fmt.Errorf("unsupported X URL")
	}
	if !isXHost(parsed.Hostname()) {
		return Request{}, fmt.Errorf("unsupported X host")
	}
	if matches := postPath.FindStringSubmatch(parsed.Path); len(matches) == 4 && !isReservedHandle(matches[1]) {
		statusID := canonicalDecimal(matches[2])
		if statusID == "0" {
			return Request{}, fmt.Errorf("unsupported X status id")
		}
		request := Request{URL: rawURL, Kind: KindPostThread, Handle: strings.ToLower(matches[1]), StatusID: statusID}
		if matches[3] != "" {
			for _, digit := range matches[3] {
				request.MediaIndex = request.MediaIndex*10 + int(digit-'0')
			}
		}
		return request, nil
	}
	if matches := profilePath.FindStringSubmatch(parsed.Path); len(matches) == 3 && !isReservedHandle(matches[1]) {
		return Request{URL: rawURL, Kind: KindProfilePosts, Handle: strings.ToLower(matches[1]), Surface: matches[2]}, nil
	}
	return Request{}, fmt.Errorf("unsupported X route")
}

// ValidateFinalURL rejects post drift but returns a renamed final profile
// identity. A requested profile surface is an assertion and must not drift.
func ValidateFinalURL(request Request, finalURL string) (Request, error) {
	final, err := Parse(finalURL)
	if err != nil {
		return Request{}, fmt.Errorf("invalid final X URL: %w", err)
	}
	if final.Kind != request.Kind {
		return Request{}, fmt.Errorf("X identity changed during navigation")
	}
	switch request.Kind {
	case KindPostThread:
		if final.Handle != request.Handle || final.StatusID != request.StatusID {
			return Request{}, fmt.Errorf("X post identity changed during navigation")
		}
	case KindProfilePosts:
		if final.Surface != request.Surface {
			return Request{}, fmt.Errorf("X profile surface changed during navigation")
		}
		if final.Handle != request.Handle {
			final.RequestedHandle = request.Handle
			final.HandleChanged = true
		}
	default:
		return Request{}, fmt.Errorf("unsupported X request kind")
	}
	return final, nil
}

func isXHost(host string) bool {
	switch strings.ToLower(strings.TrimSuffix(host, ".")) {
	case "x.com", "www.x.com", "twitter.com", "www.twitter.com":
		return true
	default:
		return false
	}
}

func isReservedHandle(handle string) bool {
	switch strings.ToLower(handle) {
	case "home", "explore", "search", "i", "settings", "messages", "notifications", "compose", "login", "signup", "intent", "share":
		return true
	default:
		return false
	}
}

func canonicalDecimal(value string) string {
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0"
	}
	return value
}
