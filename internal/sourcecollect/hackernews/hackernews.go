// Package hackernews contains pure Hacker News thread identity policy.
package hackernews

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type Request struct {
	URL    string `json:"url"`
	ItemID string `json:"item_id"`
}
type RecordKind string

const (
	RecordStory   RecordKind = "story"
	RecordComment RecordKind = "comment"
)

type Record struct {
	Kind         RecordKind `json:"kind"`
	ID           string     `json:"id"`
	CanonicalURL string     `json:"canonical_url"`
	Title        string     `json:"title,omitempty"`
	Body         string     `json:"body,omitempty"`
	Author       string     `json:"author,omitempty"`
	Timestamp    string     `json:"timestamp,omitempty"`
	Depth        int        `json:"depth,omitempty"`
	ParentID     string     `json:"parent_id,omitempty"`
}
type RecordPage struct {
	Records []Record `json:"records"`
}

func Parse(raw string) (Request, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme != "https" || u.Hostname() != "news.ycombinator.com" || u.Path != "/item" || len(u.Query()) != 1 || len(u.Query()["id"]) != 1 || u.Fragment != "" {
		return Request{}, fmt.Errorf("unsupported Hacker News URL")
	}
	id := u.Query().Get("id")
	if !decimal(id) || id == "0" {
		return Request{}, fmt.Errorf("unsupported Hacker News item id")
	}
	return Request{URL: raw, ItemID: id}, nil
}

// ValidateFinalURL rejects redirects and intervening navigation that change the
// requested Hacker News discussion identity.
func ValidateFinalURL(request Request, finalURL string) error {
	final, err := Parse(finalURL)
	if err != nil {
		return fmt.Errorf("invalid final Hacker News URL: %w", err)
	}
	if final.ItemID != request.ItemID {
		return fmt.Errorf("Hacker News item identity changed")
	}
	return nil
}

func DecodeThreadPage(request Request, raw json.RawMessage) (RecordPage, error) {
	var p RecordPage
	if e := json.Unmarshal(raw, &p); e != nil {
		return p, e
	}
	if len(p.Records) == 0 || p.Records[0].Kind != RecordStory || p.Records[0].ID != request.ItemID || p.Records[0].Depth != 0 || p.Records[0].ParentID != "" || !validURL(p.Records[0].CanonicalURL, p.Records[0].ID) {
		return p, fmt.Errorf("invalid Hacker News story")
	}
	stack := []string{}
	seen := map[string]bool{request.ItemID: true}
	for _, r := range p.Records[1:] {
		if r.Kind != RecordComment || seen[r.ID] || !decimal(r.ID) || !validURL(r.CanonicalURL, r.ID) || r.Depth < 0 || r.Depth > len(stack) {
			return p, fmt.Errorf("invalid Hacker News comment")
		}
		if r.Depth == 0 {
			if r.ParentID != "" {
				return p, fmt.Errorf("invalid Hacker News root comment")
			}
			stack = []string{r.ID}
		} else {
			stack = stack[:r.Depth]
			if len(stack) == 0 || r.ParentID != stack[len(stack)-1] {
				return p, fmt.Errorf("invalid Hacker News parent")
			}
			stack = append(stack, r.ID)
		}
		seen[r.ID] = true
	}
	return p, nil
}
func decimal(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func validURL(raw, id string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "" && u.Host == "" && u.User == nil && u.Path == "/item" && u.RawQuery == "id="+id && u.Fragment == ""
}

func ThreadExpression(itemID string, limit int) string {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	return fmt.Sprintf(`(() => { "__cdp_cli_hn_thread_records__"; const root=%q, limit=%d, records=[], stack=[], text=n=>String(n&&n.innerText||"").replace(/\s+/g," ").trim(), own=(row,s)=>Array.from(row.querySelectorAll(s)).filter(n=>n.closest("tr.athing.comtr")===row), link=(row)=>{const a=own(row,'a[href*="item?id="]').find(a=>{try{return new URL(a.href,location.href).searchParams.get("id")===row.id}catch(_){return false}});return a?"/item?id="+row.id:""}; const story=document.querySelector("tr.athing"); records.push({kind:"story",id:root,canonical_url:"/item?id="+root,title:text(story&&story.querySelector(".titleline")),author:text(story&&story.nextElementSibling&&story.nextElementSibling.querySelector(".hnuser")),timestamp:text(story&&story.nextElementSibling&&story.nextElementSibling.querySelector(".age"))}); for(const row of Array.from(document.querySelectorAll("tr.athing.comtr"))){if(records.length>=limit)break;const id=row.id,url=link(row),indent=Number((own(row,"td.ind img")[0]||{}).width||0)/40;if(!/^\d+$/.test(id)||!url||!Number.isInteger(indent)||indent<0||indent>stack.length)continue;stack.length=indent;const parent_id=indent?stack[indent-1]:"";records.push({kind:"comment",id,canonical_url:url,depth:indent,parent_id,body:text(own(row,".commtext")[0]),author:text(own(row,".hnuser")[0]),timestamp:text(own(row,".age")[0])});stack.push(id)} return {records}; })()`, itemID, limit)
}
