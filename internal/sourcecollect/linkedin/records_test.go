package linkedin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeThreadPageRequiresExactActivityRootAndCommentScope(t *testing.T) {
	request, _ := Parse("https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD/")
	good := json.RawMessage(`{"records":[
  {"kind":"activity","id":"7482842673645584386","data_urn":"urn:li:activity:7482842673645584386","canonical_url":"/posts/example-activity-7482842673645584386-9aSD/","activity_id":"7482842673645584386","timestamp":"2026-07-24T10:00:00Z","discovery_surface":"activity_root"},
  {"kind":"comment","id":"urn:li:comment:(activity:7482842673645584386,7482842673645584387)","canonical_url":"/posts/example-activity-7482842673645584386-9aSD/","activity_id":"7482842673645584386","body":"reply","discovery_surface":"activity_comment"}
]}`)
	if page, err := DecodeThreadPage(request, good); err != nil || len(page.Records) != 2 {
		t.Fatalf("DecodeThreadPage() = %+v, %v", page, err)
	}
	for _, bad := range []json.RawMessage{
		json.RawMessage(`{"records":[{"kind":"activity","id":"7482842673645584387","data_urn":"urn:li:activity:7482842673645584387","canonical_url":"/posts/example-activity-7482842673645584387/","activity_id":"7482842673645584387","timestamp":"now","discovery_surface":"activity_root"}]}`),
		json.RawMessage(`{"records":[{"kind":"activity","id":"7482842673645584386","data_urn":"urn:li:activity:7482842673645584386","canonical_url":"/posts/example-activity-7482842673645584386/","activity_id":"7482842673645584386","timestamp":"now","discovery_surface":"activity_root"},{"kind":"comment","id":"author-only","canonical_url":"/posts/example-activity-7482842673645584386/","activity_id":"7482842673645584386","body":"bad","discovery_surface":"activity_comment"}]}`),
		json.RawMessage(`{"records":[{"kind":"activity","id":"7482842673645584386","data_urn":"urn:li:activity:7482842673645584386","canonical_url":"/posts/example-activity-7482842673645584386/","activity_id":"7482842673645584386","timestamp":"now","discovery_surface":"activity_root"},{"kind":"comment","id":"urn:li:comment:(activity:9999999999999999999,7482842673645584387)","canonical_url":"/posts/example-activity-7482842673645584386/","activity_id":"7482842673645584386","body":"cross activity","discovery_surface":"activity_comment"}]}`),
	} {
		if _, err := DecodeThreadPage(request, bad); err == nil {
			t.Fatalf("DecodeThreadPage(%s) succeeded, want identity rejection", bad)
		}
	}
}

func TestDecodeThreadCommentsRejectsRootReplacementAndCrossActivity(t *testing.T) {
	request, _ := Parse("https://www.linkedin.com/feed/update/urn:li:activity:7482842673645584386/")
	valid := json.RawMessage(`{"records":[{"kind":"comment","id":"urn:li:comment:(activity:7482842673645584386,7482842673645584387)","canonical_url":"/feed/update/urn:li:activity:7482842673645584386/","activity_id":"7482842673645584386","body":"reply","discovery_surface":"activity_comment"}]}`)
	if _, err := DecodeThreadComments(request, valid); err != nil {
		t.Fatalf("DecodeThreadComments() = %v", err)
	}
	for _, bad := range []json.RawMessage{
		json.RawMessage(`{"records":[{"kind":"activity","id":"7482842673645584386","data_urn":"urn:li:activity:7482842673645584386","canonical_url":"/feed/update/urn:li:activity:7482842673645584386/","activity_id":"7482842673645584386","timestamp":"now","discovery_surface":"activity_root"}]}`),
		json.RawMessage(`{"records":[{"kind":"comment","id":"urn:li:comment:(activity:7482842673645584387,7482842673645584388)","canonical_url":"/feed/update/urn:li:activity:7482842673645584387/","activity_id":"7482842673645584387","body":"cross","discovery_surface":"activity_comment"}]}`),
	} {
		if _, err := DecodeThreadComments(request, bad); err == nil {
			t.Fatalf("DecodeThreadComments(%s) succeeded", bad)
		}
	}
}

func TestExpressionsContainArticleLocalIdentityGuards(t *testing.T) {
	thread, _ := Parse("https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD/")
	company, _ := Parse("https://www.linkedin.com/company/the-pragmatic-engineer/posts/")
	for name, expression := range map[string]string{
		"thread":  ThreadExpression(thread, 501),
		"company": CompanyExpression(company, 501),
	} {
		for _, want := range []string{"article", "data-urn", "canonical_url", "__cdp_cli_linkedin_", "500"} {
			if !strings.Contains(expression, want) {
				t.Fatalf("%s expression missing %q", name, want)
			}
		}
	}
	if expression := ThreadExpression(thread, 10); !strings.Contains(expression, "new Set") || !strings.Contains(expression, "canonical_url && !seen") {
		t.Fatalf("thread expression must deduplicate matching links and require canonical root proof: %s", expression)
	}
}
