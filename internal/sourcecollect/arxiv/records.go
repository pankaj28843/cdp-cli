package arxiv

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Paper struct {
	Identifier   string `json:"identifier"`
	CanonicalURL string `json:"canonical_url"`
	Title        string `json:"title"`
}
type Reference struct {
	ID              string `json:"id"`
	PaperIdentifier string `json:"paper_identifier"`
	Text            string `json:"text"`
}
type PaperPage struct {
	Paper      Paper       `json:"paper"`
	References []Reference `json:"references"`
}

func DecodePaperPage(request Request, raw json.RawMessage) (PaperPage, error) {
	var p PaperPage
	if e := json.Unmarshal(raw, &p); e != nil {
		return p, fmt.Errorf("decode arXiv paper: %w", e)
	}
	if p.Paper.Identifier != request.Identifier || p.Paper.CanonicalURL != "/abs/"+request.Identifier || strings.TrimSpace(p.Paper.Title) == "" {
		return p, fmt.Errorf("invalid arXiv paper identity: got identifier=%q canonical_url=%q title_present=%t", p.Paper.Identifier, p.Paper.CanonicalURL, strings.TrimSpace(p.Paper.Title) != "")
	}
	seen := map[string]bool{}
	for _, r := range p.References {
		if r.ID == "" || seen[r.ID] || r.PaperIdentifier != request.Identifier || strings.TrimSpace(r.Text) == "" {
			return p, fmt.Errorf("invalid arXiv reference")
		}
		seen[r.ID] = true
	}
	return p, nil
}

func PaperExpression(identifier string, limit int) string {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	return fmt.Sprintf(`(() => { "__cdp_cli_arxiv_paper__"; const id=%q, limit=%d, normalize=v=>String(v||"").replace(/\s+/g," ").trim(); const root=document.querySelector("article.ltx_document"); if(!root)return {paper:{},references:[]}; const references=[]; for(const node of Array.from(root.querySelectorAll(".ltx_bibliography li"))){if(references.length>=limit)break;const ref=node.id||"";const text=normalize(node.innerText);if(ref&&text)references.push({id:ref,paper_identifier:id,text})}; return {paper:{identifier:id,canonical_url:"/abs/"+id,title:normalize(root.querySelector("h1")&&root.querySelector("h1").innerText)},references}; })()`, identifier, limit)
}
