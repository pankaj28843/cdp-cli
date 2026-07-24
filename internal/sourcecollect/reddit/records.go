package reddit

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type RecordKind string

const (
	RecordSubmission  RecordKind = "submission"
	RecordComment     RecordKind = "comment"
	RecordListingItem RecordKind = "listing_thread"
)

// Record is a source-native object. Fields that establish membership are kept
// explicit so callers never infer identity from the command or DOM ancestry.
type Record struct {
	Kind             RecordKind `json:"kind"`
	ID               string     `json:"id"`
	CanonicalURL     string     `json:"canonical_url"`
	Subreddit        string     `json:"subreddit,omitempty"`
	RootThreadID     string     `json:"root_thread_id,omitempty"`
	ParentID         string     `json:"parent_id,omitempty"`
	Author           string     `json:"author,omitempty"`
	Title            string     `json:"title,omitempty"`
	Body             string     `json:"body,omitempty"`
	DiscoverySurface string     `json:"discovery_surface"`
}

type RecordPage struct {
	Records []Record `json:"records"`
}

func DecodeThreadPage(request Request, raw json.RawMessage) (RecordPage, error) {
	if request.Kind != KindThread {
		return RecordPage{}, fmt.Errorf("thread decoder requires thread request")
	}
	var page RecordPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return RecordPage{}, fmt.Errorf("decode Reddit thread page: %w", err)
	}
	seen := map[string]struct{}{}
	commentIDs := map[string]struct{}{}
	rootCount := 0
	canonicalPrefix := "/r/" + request.Subreddit + "/comments/" + strings.TrimPrefix(request.PostID, "t3_") + "/"
	for _, record := range page.Records {
		if _, exists := seen[record.ID]; exists {
			return RecordPage{}, fmt.Errorf("duplicate Reddit record id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
		if !strings.HasPrefix(strings.ToLower(record.CanonicalURL), canonicalPrefix) || strings.ToLower(record.Subreddit) != request.Subreddit {
			return RecordPage{}, fmt.Errorf("cross-subreddit Reddit thread record")
		}
		switch record.Kind {
		case RecordSubmission:
			if record.ID != request.PostID || record.RootThreadID != request.PostID || strings.TrimSpace(record.Title) == "" || !canonicalRecordURLMatches(record) {
				return RecordPage{}, fmt.Errorf("invalid Reddit thread root")
			}
			rootCount++
		case RecordComment:
			if !strings.HasPrefix(record.ID, "t1_") || record.RootThreadID != request.PostID || strings.TrimSpace(record.Author) == "" || !canonicalRecordURLMatches(record) {
				return RecordPage{}, fmt.Errorf("invalid Reddit thread comment")
			}
			commentIDs[record.ID] = struct{}{}
			if request.CommentID != "" && record.ID != "t1_"+request.CommentID && record.ParentID == "" {
				return RecordPage{}, fmt.Errorf("requested Reddit comment is absent from thread root")
			}
		default:
			return RecordPage{}, fmt.Errorf("invalid Reddit thread record kind")
		}
	}
	if rootCount != 1 {
		return RecordPage{}, fmt.Errorf("Reddit thread requires exactly one root submission")
	}
	for _, record := range page.Records {
		if record.Kind != RecordComment || record.ParentID == request.PostID {
			continue
		}
		if _, exists := commentIDs[record.ParentID]; !exists {
			return RecordPage{}, fmt.Errorf("unresolved Reddit thread comment parent")
		}
	}
	return page, nil
}

func ThreadExpression(request Request, limit int) string {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	return fmt.Sprintf(`(() => {
  "__cdp_cli_reddit_thread_records__";
  const subreddit = %q, root = %q, limit = %d;
  const visible = n => { const s = n && getComputedStyle(n); return Boolean(n && n.isConnected && s && s.display !== "none" && s.visibility !== "hidden" && n.getClientRects().length); };
  const text = n => String(n && n.innerText || "").replace(/\s+/g, " ").trim();
  const records = [], seen = new Set();
  const post = Array.from(document.querySelectorAll("shreddit-post")).find(n => visible(n) && n.getAttribute("id") === root && (n.getAttribute("subreddit-name") || "").toLowerCase() === subreddit);
	if (post) records.push({kind:"submission",id:root,canonical_url:post.getAttribute("permalink") || "",subreddit,root_thread_id:root,author:post.getAttribute("author") || "",title:post.getAttribute("post-title") || "",body:text(post),discovery_surface:"thread_root"});
  for (const node of Array.from(document.querySelectorAll("shreddit-comment"))) {
    if (!visible(node) || records.length >= limit) continue;
    const id = node.getAttribute("thingid") || "", postid = node.getAttribute("postid") || "", permalink = node.getAttribute("permalink") || "";
    if (!/^t1_[A-Za-z0-9]+$/.test(id) || postid !== root || !new RegExp("^/r/" + subreddit + "/comments/", "i").test(permalink) || seen.has(id)) continue;
	seen.add(id); records.push({kind:"comment",id,canonical_url:permalink,subreddit,root_thread_id:postid,parent_id:node.getAttribute("parentid") || root,author:(node.getAttribute("author") || "").trim(),body:text(node),discovery_surface:"thread_comment_tree"});
  }
  return {records};
})()`, request.Subreddit, request.PostID, limit)
}

func DecodeUserPage(request Request, raw json.RawMessage) (RecordPage, error) {
	if request.Kind != KindUserProfile {
		return RecordPage{}, fmt.Errorf("user decoder requires user-profile request")
	}
	var page RecordPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return RecordPage{}, fmt.Errorf("decode Reddit user page: %w", err)
	}
	seen := map[string]struct{}{}
	for _, record := range page.Records {
		if _, exists := seen[record.ID]; exists {
			return RecordPage{}, fmt.Errorf("duplicate Reddit user record id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
		if strings.ToLower(record.Author) != request.Username || !strings.HasPrefix(strings.ToLower(record.CanonicalURL), "/r/") || !strings.Contains(strings.ToLower(record.CanonicalURL), "/comments/") {
			return RecordPage{}, fmt.Errorf("invalid Reddit user record identity")
		}
		switch record.Kind {
		case RecordSubmission:
			if !strings.HasPrefix(record.ID, "t3_") || record.RootThreadID != record.ID || strings.TrimSpace(record.Title) == "" || !canonicalRecordURLMatches(record) {
				return RecordPage{}, fmt.Errorf("invalid Reddit user submission")
			}
		case RecordComment:
			if !strings.HasPrefix(record.ID, "t1_") || !strings.HasPrefix(record.RootThreadID, "t3_") || !canonicalRecordURLMatches(record) {
				return RecordPage{}, fmt.Errorf("invalid Reddit user comment")
			}
		default:
			return RecordPage{}, fmt.Errorf("invalid Reddit user record kind")
		}
	}
	return page, nil
}

func canonicalRecordURLMatches(record Record) bool {
	u, err := url.Parse(record.CanonicalURL)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	parts := strings.Split(strings.Trim(strings.ToLower(u.Path), "/"), "/")
	if len(parts) < 5 || parts[0] != "r" || parts[1] == "" || parts[2] != "comments" || parts[3] == "" {
		return false
	}
	switch record.Kind {
	case RecordSubmission:
		return record.ID == "t3_"+parts[3]
	case RecordComment:
		return len(parts) >= 6 && record.ID == "t1_"+parts[len(parts)-1]
	default:
		return false
	}
}

func UserExpression(request Request, limit int) string {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	return fmt.Sprintf(`(() => {
  "__cdp_cli_reddit_user_records__";
  const username = %q, limit = %d;
  const visible = n => { const s = n && getComputedStyle(n); return Boolean(n && n.isConnected && s && s.display !== "none" && s.visibility !== "hidden" && n.getClientRects().length); };
  const text = n => String(n && n.innerText || "").replace(/\s+/g, " ").trim();
  const canonical = value => String(value || "").split("?")[0];
  const author = n => { const a = n.querySelector('a[href^="/user/"],a[href^="/u/"]'); return ((a && a.getAttribute("href")) || "").split("/").filter(Boolean).pop().toLowerCase(); };
  const records = [], seen = new Set();
  for (const node of Array.from(document.querySelectorAll("shreddit-feed shreddit-post"))) {
    if (!visible(node) || records.length >= limit) continue;
    const id = node.getAttribute("id") || "", permalink = canonical(node.getAttribute("permalink")), who = (node.getAttribute("author") || "").toLowerCase();
    if (!/^t3_[A-Za-z0-9]+$/.test(id) || who !== username || !/^\/r\/[^/]+\/comments\/[A-Za-z0-9]+\//i.test(permalink) || seen.has(id)) continue;
    seen.add(id); records.push({kind:"submission",id,canonical_url:permalink,subreddit:(node.getAttribute("subreddit-name") || "").toLowerCase(),root_thread_id:id,author:who,title:node.getAttribute("post-title") || "",body:text(node),discovery_surface:"user_submission"});
  }
  for (const node of Array.from(document.querySelectorAll("shreddit-profile-comment"))) {
    if (!visible(node) || records.length >= limit) continue;
    const id = node.getAttribute("comment-id") || "", permalink = canonical(node.getAttribute("href")), who = author(node);
    const match = permalink.match(/^\/r\/([^/]+)\/comments\/([A-Za-z0-9]+)\/comment\/([A-Za-z0-9]+)\/?$/i);
    if (!/^t1_[A-Za-z0-9]+$/.test(id) || who !== username || !match || id.toLowerCase() !== "t1_" + match[3].toLowerCase() || seen.has(id)) continue;
    seen.add(id); records.push({kind:"comment",id,canonical_url:permalink,subreddit:match[1].toLowerCase(),root_thread_id:"t3_" + match[2].toLowerCase(),author:who,body:text(node),discovery_surface:"user_comment"});
  }
  return {records};
})()`, request.Username, limit)
}
