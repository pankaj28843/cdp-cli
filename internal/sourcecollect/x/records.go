package x

import (
	"encoding/json"
	"fmt"
	"strings"
)

type RecordKind string

const (
	RecordPost  RecordKind = "post"
	RecordReply RecordKind = "reply"
)

// Record keeps source-local proof explicit: its canonical URL jointly proves
// handle and status ID, while RootStatusID scopes thread membership.
type Record struct {
	Kind             RecordKind `json:"kind"`
	ID               string     `json:"id"`
	CanonicalURL     string     `json:"canonical_url"`
	Handle           string     `json:"handle"`
	RootStatusID     string     `json:"root_status_id"`
	Body             string     `json:"body"`
	Timestamp        string     `json:"timestamp,omitempty"`
	DiscoverySurface string     `json:"discovery_surface"`
}

type RecordPage struct {
	Records []Record `json:"records"`
}

func DecodeThreadPage(request Request, raw json.RawMessage) (RecordPage, error) {
	if request.Kind != KindPostThread {
		return RecordPage{}, fmt.Errorf("thread decoder requires post-thread request")
	}
	page, err := decodeRecordPage(raw)
	if err != nil {
		return RecordPage{}, err
	}
	seen := map[string]struct{}{}
	rootCount := 0
	for _, record := range page.Records {
		if err := validateRecordURL(record); err != nil {
			return RecordPage{}, err
		}
		if _, exists := seen[record.ID]; exists {
			return RecordPage{}, fmt.Errorf("duplicate X record id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
		switch record.Kind {
		case RecordPost:
			if record.ID != request.StatusID || !strings.EqualFold(record.Handle, request.Handle) || record.RootStatusID != request.StatusID {
				return RecordPage{}, fmt.Errorf("invalid X thread root")
			}
			rootCount++
		case RecordReply:
			if record.ID == request.StatusID || record.RootStatusID != request.StatusID {
				return RecordPage{}, fmt.Errorf("invalid X thread reply")
			}
		default:
			return RecordPage{}, fmt.Errorf("invalid X thread record kind")
		}
	}
	if rootCount != 1 {
		return RecordPage{}, fmt.Errorf("X thread requires exactly one root post")
	}
	return page, nil
}

func DecodeProfilePage(request Request, raw json.RawMessage) (RecordPage, error) {
	if request.Kind != KindProfilePosts {
		return RecordPage{}, fmt.Errorf("profile decoder requires profile request")
	}
	if request.HandleChanged {
		return RecordPage{}, fmt.Errorf("X profile handle changed and requires stable account verification")
	}
	page, err := decodeRecordPage(raw)
	if err != nil {
		return RecordPage{}, err
	}
	seen := map[string]struct{}{}
	for _, record := range page.Records {
		if err := validateRecordURL(record); err != nil {
			return RecordPage{}, err
		}
		if record.Kind != RecordPost || !strings.EqualFold(record.Handle, request.Handle) || record.RootStatusID != record.ID {
			return RecordPage{}, fmt.Errorf("invalid X profile record identity")
		}
		if _, exists := seen[record.ID]; exists {
			return RecordPage{}, fmt.Errorf("duplicate X profile record id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
	}
	return page, nil
}

// DecodeThreadReplies accepts a post-expansion snapshot after the verified root
// may have been virtualized out of the DOM. It never admits a replacement root.
func DecodeThreadReplies(request Request, raw json.RawMessage) (RecordPage, error) {
	if request.Kind != KindPostThread {
		return RecordPage{}, fmt.Errorf("thread reply decoder requires post-thread request")
	}
	page, err := decodeRecordPage(raw)
	if err != nil {
		return RecordPage{}, err
	}
	seen := map[string]struct{}{}
	for _, record := range page.Records {
		if err := validateRecordURL(record); err != nil {
			return RecordPage{}, err
		}
		if record.Kind != RecordReply || record.ID == request.StatusID || record.RootStatusID != request.StatusID {
			return RecordPage{}, fmt.Errorf("invalid X thread reply")
		}
		if _, exists := seen[record.ID]; exists {
			return RecordPage{}, fmt.Errorf("duplicate X thread reply id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
	}
	return page, nil
}

func decodeRecordPage(raw json.RawMessage) (RecordPage, error) {
	var page RecordPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return RecordPage{}, fmt.Errorf("decode X record page: %w", err)
	}
	return page, nil
}

func validateRecordURL(record Record) error {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Handle) == "" || strings.TrimSpace(record.CanonicalURL) == "" {
		return fmt.Errorf("incomplete X record identity")
	}
	matches := postPath.FindStringSubmatch(record.CanonicalURL)
	if len(matches) != 4 || strings.ToLower(matches[1]) != strings.ToLower(record.Handle) || canonicalDecimal(matches[2]) != record.ID || record.ID == "0" {
		return fmt.Errorf("invalid X article-local status identity")
	}
	return nil
}

func ThreadExpression(request Request, limit int) string {
	return recordsExpression(request, limit, true)
}

func ProfileExpression(request Request, limit int) string {
	return recordsExpression(request, limit, false)
}

func recordsExpression(request Request, limit int, thread bool) string {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	root := ""
	if thread {
		root = request.StatusID
	}
	return fmt.Sprintf(`(() => {
  "__cdp_cli_x_records__";
  const handle = %q, root = %q, limit = %d, isThread = %t;
  const visible = n => { const s = n && getComputedStyle(n); return Boolean(n && n.isConnected && s && s.display !== "none" && s.visibility !== "hidden" && n.getClientRects().length); };
  const own = (article, selector) => Array.from(article.querySelectorAll(selector)).filter(n => n.closest("article") === article);
  const normalize = value => String(value || "").replace(/\s+/g, " ").trim();
  const record = article => {
    const parseAnchor = anchor => { try { const parsed = new URL(anchor.href, location.href), match = parsed.pathname.match(/^\/([A-Za-z0-9_]{1,15})\/status\/([0-9]+)$/); return match && match[2].replace(/^0+/, "") ? match : null; } catch (_) { return null; } };
    const time = own(article, "time")[0], timeAnchor = time && time.closest("a[href]");
    let match = timeAnchor && parseAnchor(timeAnchor);
    if (!match) { const candidates = own(article, "a[href]").map(parseAnchor).filter(Boolean); if (candidates.length === 1) match = candidates[0]; else return null; }
    const id = match[2].replace(/^0+/, ""), who = match[1].toLowerCase();
    const text = own(article, '[data-testid="tweetText"]')[0];
    return {id, canonical_url:"/" + who + "/status/" + id, handle:who, body:normalize(text && text.innerText), timestamp:(time && time.getAttribute("datetime")) || ""};
  };
  const scope = isThread ? (document.querySelector('[aria-label="Timeline: Conversation"]') || document) : document;
  const records = [], seen = new Set();
  if (isThread) {
    for (const article of Array.from(document.querySelectorAll('article[data-testid="tweet"]'))) {
      if (!visible(article)) continue;
      const item = record(article); if (!item || item.id !== root) continue;
      records.push({...item,kind:"post",root_status_id:root,discovery_surface:"thread_root"}); seen.add(item.id); break;
    }
  }
  for (const article of Array.from(scope.querySelectorAll('article[data-testid="tweet"]'))) {
    if (!visible(article) || records.length >= limit) continue;
    const item = record(article); if (!item || seen.has(item.id)) continue;
    if (isThread) {
      records.push({...item,kind:"reply",root_status_id:root,discovery_surface:"conversation"});
    } else if (item.handle === handle) records.push({...item,kind:"post",root_status_id:item.id,discovery_surface:"profile_posts"});
    else continue;
    seen.add(item.id);
  }
  return {records};
})()`, request.Handle, root, limit, thread)
}
