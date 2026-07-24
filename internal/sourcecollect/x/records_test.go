package x

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeThreadPageRequiresOneExactRootAndArticleLocalReplies(t *testing.T) {
	request, err := Parse("https://x.com/karpathy/status/2079610838143623371")
	if err != nil {
		t.Fatal(err)
	}
	valid := json.RawMessage(`{"records":[
  {"kind":"post","id":"2079610838143623371","canonical_url":"/karpathy/status/2079610838143623371","handle":"Karpathy","root_status_id":"2079610838143623371","body":"long root","discovery_surface":"thread_root"},
  {"kind":"reply","id":"2079610838143623999","canonical_url":"/reply/status/2079610838143623999","handle":"reply","root_status_id":"2079610838143623371","body":"reply","discovery_surface":"conversation"}
]}`)
	page, err := DecodeThreadPage(request, valid)
	if err != nil || len(page.Records) != 2 {
		t.Fatalf("DecodeThreadPage() = %+v, %v", page, err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"records":[{"kind":"post","id":"2079610838143623999","canonical_url":"/karpathy/status/2079610838143623999","handle":"karpathy","root_status_id":"2079610838143623999","body":"wrong root","discovery_surface":"thread_root"}]}`),
		json.RawMessage(`{"records":[{"kind":"post","id":"2079610838143623371","canonical_url":"/karpathy/status/2079610838143623371","handle":"karpathy","root_status_id":"2079610838143623371","body":"root","discovery_surface":"thread_root"},{"kind":"reply","id":"2079610838143623999","canonical_url":"/other/status/2079610838143623999","handle":"reply","root_status_id":"2079610838143623000","body":"cross root","discovery_surface":"conversation"}]}`),
	} {
		if _, err := DecodeThreadPage(request, raw); err == nil {
			t.Fatalf("DecodeThreadPage(%s) succeeded, want identity rejection", raw)
		}
	}
}

func TestDecodeThreadRepliesRejectsRootOrCrossRoot(t *testing.T) {
	request, err := Parse("https://x.com/karpathy/status/2079610838143623371")
	if err != nil {
		t.Fatal(err)
	}
	valid := json.RawMessage(`{"records":[{"kind":"reply","id":"2079610838143623999","canonical_url":"/reply/status/2079610838143623999","handle":"reply","root_status_id":"2079610838143623371","body":"reply","discovery_surface":"conversation"}]}`)
	if _, err := DecodeThreadReplies(request, valid); err != nil {
		t.Fatalf("DecodeThreadReplies() error = %v", err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"records":[{"kind":"post","id":"2079610838143623371","canonical_url":"/karpathy/status/2079610838143623371","handle":"karpathy","root_status_id":"2079610838143623371","body":"root","discovery_surface":"thread_root"}]}`),
		json.RawMessage(`{"records":[{"kind":"reply","id":"2079610838143623999","canonical_url":"/reply/status/2079610838143623999","handle":"reply","root_status_id":"2079610838143623000","body":"cross root","discovery_surface":"conversation"}]}`),
	} {
		if _, err := DecodeThreadReplies(request, raw); err == nil {
			t.Fatalf("DecodeThreadReplies(%s) succeeded, want rejection", raw)
		}
	}
}

func TestDecodeProfilePageRejectsPendingRenameAndCrossHandle(t *testing.T) {
	request, err := Parse("https://x.com/karpathy")
	if err != nil {
		t.Fatal(err)
	}
	valid := json.RawMessage(`{"records":[{"kind":"post","id":"2079610838143623371","canonical_url":"/karpathy/status/2079610838143623371","handle":"karpathy","root_status_id":"2079610838143623371","body":"post","discovery_surface":"profile_posts"}]}`)
	if _, err := DecodeProfilePage(request, valid); err != nil {
		t.Fatalf("DecodeProfilePage() error = %v", err)
	}
	if _, err := DecodeProfilePage(request, json.RawMessage(`{"records":[{"kind":"post","id":"2079610838143623371","canonical_url":"/other/status/2079610838143623371","handle":"other","root_status_id":"2079610838143623371","body":"cross handle","discovery_surface":"profile_posts"}]}`)); err == nil {
		t.Fatal("cross-handle profile post accepted")
	}
	renamed, err := ValidateFinalURL(request, "https://x.com/renamed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeProfilePage(renamed, valid); err == nil {
		t.Fatal("profile rename without stable account verification accepted")
	}
}

func TestExpressionsContainSourceScopedIdentityGuards(t *testing.T) {
	thread, _ := Parse("https://x.com/karpathy/status/2079610838143623371")
	profile, _ := Parse("https://x.com/karpathy/with_replies")
	for name, expression := range map[string]string{
		"thread":  ThreadExpression(thread, 501),
		"profile": ProfileExpression(profile, 501),
	} {
		for _, want := range []string{"article[data-testid=\"tweet\"]", "/status/", "__cdp_cli_x_", "500", "candidates.length === 1"} {
			if !strings.Contains(expression, want) {
				t.Fatalf("%s expression missing %q", name, want)
			}
		}
	}
}
