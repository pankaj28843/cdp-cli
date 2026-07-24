package arxiv

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodePaperPageBindsReferencesToResolvedPaper(t *testing.T) {
	request, _ := Parse("https://arxiv.org/abs/2604.12374v2")
	good := json.RawMessage(`{"paper":{"identifier":"2604.12374v2","canonical_url":"/abs/2604.12374v2","title":"Paper"},"references":[{"id":"ref-1","paper_identifier":"2604.12374v2","text":"Reference"}]}`)
	if page, err := DecodePaperPage(request, good); err != nil || len(page.References) != 1 {
		t.Fatalf("DecodePaperPage()=%+v,%v", page, err)
	}
	bad := json.RawMessage(`{"paper":{"identifier":"2604.12374v3","canonical_url":"/abs/2604.12374v3","title":"Paper"},"references":[]}`)
	if _, err := DecodePaperPage(request, bad); err == nil {
		t.Fatal("version drift accepted")
	}
}

func TestPaperExpressionUsesSemanticPaperAndReferenceSelectors(t *testing.T) {
	expression := PaperExpression("2604.12374v2", 500)
	for _, want := range []string{"__cdp_cli_arxiv_paper__", "article.ltx_document", ".ltx_bibliography", "references", "500"} {
		if !strings.Contains(expression, want) {
			t.Fatalf("expression missing %q", want)
		}
	}
}
