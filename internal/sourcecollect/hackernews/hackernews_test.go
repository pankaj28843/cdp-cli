package hackernews

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseAcceptsOnlyCanonicalHNItemURLs(t *testing.T) {
	request, err := Parse("https://news.ycombinator.com/item?id=46641042")
	if err != nil || request.ItemID != "46641042" {
		t.Fatalf("Parse() = %+v, %v", request, err)
	}
	for _, rawURL := range []string{"http://news.ycombinator.com/item?id=46641042", "https://news.ycombinator.com/item?id=0", "https://news.ycombinator.com/news", "https://news.ycombinator.com/item?id=46641042&next=1", "https://news.ycombinator.com/item?id=46641042&id=999", "https://news.ycombinator.com.example/item?id=46641042"} {
		if _, err := Parse(rawURL); err == nil {
			t.Fatalf("Parse(%q) succeeded", rawURL)
		}
	}
}

func TestValidateFinalURLPreservesItemIdentity(t *testing.T) {
	request, err := Parse("https://news.ycombinator.com/item?id=46641042")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFinalURL(request, "https://news.ycombinator.com/item?id=46641042"); err != nil {
		t.Fatalf("same item: %v", err)
	}
	if err := ValidateFinalURL(request, "https://news.ycombinator.com/item?id=46641043"); err == nil {
		t.Fatal("accepted a different final item")
	}
}

func TestDecodeThreadReconstructsParentsFromIndentStack(t *testing.T) {
	request, _ := Parse("https://news.ycombinator.com/item?id=46641042")
	good := json.RawMessage(`{"records":[{"kind":"story","id":"46641042","canonical_url":"/item?id=46641042"},{"kind":"comment","id":"46642165","canonical_url":"/item?id=46642165","depth":0},{"kind":"comment","id":"46644995","canonical_url":"/item?id=46644995","depth":1,"parent_id":"46642165"},{"kind":"comment","id":"46645895","canonical_url":"/item?id=46645895","depth":1,"parent_id":"46642165"}]}`)
	if page, err := DecodeThreadPage(request, good); err != nil || len(page.Records) != 4 {
		t.Fatalf("DecodeThreadPage() = %+v, %v", page, err)
	}
	bad := json.RawMessage(`{"records":[{"kind":"story","id":"46641042","canonical_url":"/item?id=46641042"},{"kind":"comment","id":"46642165","canonical_url":"/item?id=46642165","depth":0},{"kind":"comment","id":"46644995","canonical_url":"/item?id=46644995","depth":1,"parent_id":"46641042"}]}`)
	if _, err := DecodeThreadPage(request, bad); err == nil {
		t.Fatal("cross-stack parent accepted")
	}
}

func TestThreadExpressionUsesStableRowsAndIndentStack(t *testing.T) {
	expression := ThreadExpression("46641042", 501)
	for _, want := range []string{"__cdp_cli_hn_thread_records__", "tr.athing.comtr", "td.ind img", "parent_id", "500"} {
		if !strings.Contains(expression, want) {
			t.Fatalf("expression missing %q", want)
		}
	}
}
