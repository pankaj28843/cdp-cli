package linkedin

import (
	"encoding/json"
	"fmt"
	"strings"
)

type RecordKind string

const (
	RecordActivity RecordKind = "activity"
	RecordComment  RecordKind = "comment"
)

// Record makes each source-local membership proof explicit. Activity records
// need both data_urn and an article-local canonical link; comments need their
// own stable comment URN plus the exact root activity link.
type Record struct {
	Kind             RecordKind `json:"kind"`
	ID               string     `json:"id"`
	DataURN          string     `json:"data_urn,omitempty"`
	CanonicalURL     string     `json:"canonical_url"`
	ActivityID       string     `json:"activity_id"`
	Company          string     `json:"company,omitempty"`
	Body             string     `json:"body,omitempty"`
	Timestamp        string     `json:"timestamp,omitempty"`
	DiscoverySurface string     `json:"discovery_surface"`
}

type RecordPage struct {
	Records []Record `json:"records"`
}

func DecodeThreadPage(request Request, raw json.RawMessage) (RecordPage, error) {
	if request.Kind != KindActivityThread {
		return RecordPage{}, fmt.Errorf("thread decoder requires activity-thread request")
	}
	page, err := decodeRecordPage(raw)
	if err != nil {
		return RecordPage{}, err
	}
	seen, roots := map[string]struct{}{}, 0
	for _, record := range page.Records {
		if _, exists := seen[record.ID]; exists {
			return RecordPage{}, fmt.Errorf("duplicate LinkedIn record id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
		if err := validateThreadRecord(request, record, true); err != nil {
			return RecordPage{}, err
		}
		if record.Kind == RecordActivity {
			roots++
		}
	}
	if roots != 1 {
		return RecordPage{}, fmt.Errorf("LinkedIn thread requires exactly one root activity")
	}
	return page, nil
}

// DecodeThreadComments accepts a post-expansion snapshot after a verified root
// may have been virtualized out of the page. It never permits a replacement.
func DecodeThreadComments(request Request, raw json.RawMessage) (RecordPage, error) {
	if request.Kind != KindActivityThread {
		return RecordPage{}, fmt.Errorf("thread comment decoder requires activity-thread request")
	}
	page, err := decodeRecordPage(raw)
	if err != nil {
		return RecordPage{}, err
	}
	seen := map[string]struct{}{}
	for _, record := range page.Records {
		if _, exists := seen[record.ID]; exists {
			return RecordPage{}, fmt.Errorf("duplicate LinkedIn comment id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
		if record.Kind != RecordComment || validateThreadRecord(request, record, false) != nil {
			return RecordPage{}, fmt.Errorf("invalid LinkedIn thread comment")
		}
	}
	return page, nil
}

func DecodeCompanyPage(request Request, raw json.RawMessage) (RecordPage, error) {
	if request.Kind != KindCompanyPosts {
		return RecordPage{}, fmt.Errorf("company decoder requires company-posts request")
	}
	page, err := decodeRecordPage(raw)
	if err != nil {
		return RecordPage{}, err
	}
	seen := map[string]struct{}{}
	for _, record := range page.Records {
		if record.Kind != RecordActivity || record.Company != request.Company || record.ID != record.ActivityID || record.DataURN != "urn:li:activity:"+record.ID || strings.TrimSpace(record.Timestamp) == "" {
			return RecordPage{}, fmt.Errorf("invalid LinkedIn company activity")
		}
		canonical, parseErr := Parse("https://www.linkedin.com" + record.CanonicalURL)
		if parseErr != nil || canonical.Kind != KindActivityThread || canonical.ActivityID != record.ID {
			return RecordPage{}, fmt.Errorf("invalid LinkedIn company activity link")
		}
		if _, exists := seen[record.ID]; exists {
			return RecordPage{}, fmt.Errorf("duplicate LinkedIn company activity %q", record.ID)
		}
		seen[record.ID] = struct{}{}
	}
	return page, nil
}

func decodeRecordPage(raw json.RawMessage) (RecordPage, error) {
	var page RecordPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return RecordPage{}, fmt.Errorf("decode LinkedIn record page: %w", err)
	}
	return page, nil
}

func validateThreadRecord(request Request, record Record, allowRoot bool) error {
	if record.ActivityID != request.ActivityID || strings.TrimSpace(record.ID) == "" {
		return fmt.Errorf("invalid LinkedIn activity scope")
	}
	canonical, err := Parse("https://www.linkedin.com" + record.CanonicalURL)
	if err != nil || canonical.Kind != KindActivityThread || canonical.ActivityID != request.ActivityID {
		return fmt.Errorf("invalid LinkedIn article-local activity link")
	}
	switch record.Kind {
	case RecordActivity:
		if !allowRoot || record.ID != request.ActivityID || record.DataURN != "urn:li:activity:"+request.ActivityID {
			return fmt.Errorf("invalid LinkedIn root activity identity")
		}
	case RecordComment:
		if !validCommentURN(record.ID, request.ActivityID) {
			return fmt.Errorf("invalid LinkedIn comment identity")
		}
	default:
		return fmt.Errorf("invalid LinkedIn record kind")
	}
	return nil
}

func validCommentURN(id, activityID string) bool {
	prefix := "urn:li:comment:(activity:" + activityID + ","
	if !strings.HasPrefix(id, prefix) || !strings.HasSuffix(id, ")") {
		return false
	}
	commentID := strings.TrimSuffix(strings.TrimPrefix(id, prefix), ")")
	return isNonZeroDecimal(commentID)
}

func ThreadExpression(request Request, limit int) string {
	return recordsExpression(request, limit, true)
}
func CompanyExpression(request Request, limit int) string {
	return recordsExpression(request, limit, false)
}

func recordsExpression(request Request, limit int, thread bool) string {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	return fmt.Sprintf(`(() => {
  "__cdp_cli_linkedin_records__";
  const activityID = %q, company = %q, limit = %d, isThread = %t;
  const visible = n => { const s = n && getComputedStyle(n); return Boolean(n && n.isConnected && s && s.display !== "none" && s.visibility !== "hidden" && n.getClientRects().length); };
  const own = (article, selector) => Array.from(article.querySelectorAll(selector)).filter(n => n.closest("article,[role=article]") === article);
  const pageActivity = (() => { const m = location.pathname.match(/-activity-([0-9]+)(?:-|\/|$)/) || location.pathname.match(/urn:li:activity:([0-9]+)/); return m ? {id:m[1], path:location.pathname} : null; })();
  const activityLink = (article, id) => { const links = new Set(own(article, "a[href]").map(a => { try { const u = new URL(a.href, location.href); const m = u.pathname.match(/-activity-([0-9]+)(?:-|\/|$)/) || u.pathname.match(/urn:li:activity:([0-9]+)/); return m && m[1] === id ? u.pathname : ""; } catch (_) { return ""; } }).filter(Boolean)); return links.size === 1 ? [...links][0] : pageActivity && pageActivity.id === id ? pageActivity.path : ""; };
  const records = [], seen = new Set();
  for (const article of Array.from(document.querySelectorAll("article,[role=article]"))) {
    if (!visible(article) || records.length >= limit) continue;
    const canonical_url = activityLink(article, activityID);
    const data_urn = article.getAttribute("data-urn") || "", commentID = article.getAttribute("data-id") || "";
    const timestamp = (own(article,"time[datetime]")[0] || {}).dateTime || "";
    if (isThread && data_urn === "urn:li:activity:" + activityID && canonical_url && !seen.has(activityID)) { records.push({kind:"activity",id:activityID,data_urn,canonical_url,activity_id:activityID,timestamp,discovery_surface:"activity_root"}); seen.add(activityID); continue; }
    if (isThread && /^urn:li:comment:/.test(commentID) && !seen.has(commentID)) { records.push({kind:"comment",id:commentID,canonical_url,activity_id:activityID,body:String(article.innerText || "").trim(),discovery_surface:"activity_comment"}); seen.add(commentID); continue; }
    const activity = data_urn.match(/^urn:li:activity:([0-9]+)$/);
    if (!isThread && activity && !seen.has(activity[1])) { const link = activityLink(article, activity[1]); if (!link) continue; records.push({kind:"activity",id:activity[1],data_urn,canonical_url:link,activity_id:activity[1],company,timestamp:(own(article,"time")[0] || {}).dateTime || "",discovery_surface:"company_posts"}); seen.add(activity[1]); }
  }
  return {records};
})()`, request.ActivityID, request.Company, limit, thread)
}
