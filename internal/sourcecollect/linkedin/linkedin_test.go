package linkedin

import "testing"

func TestParseAcceptsOnlyCanonicalLinkedInCollectionRoutes(t *testing.T) {
	tests := []struct {
		name, rawURL string
		want         Request
	}{
		{"direct activity", "https://www.linkedin.com/posts/the-pragmatic-engineer_topic-activity-7482842673645584386-9aSD/", Request{Kind: KindActivityThread, ActivityID: "7482842673645584386"}},
		{"locale activity", "https://de.linkedin.com/posts/example-activity-7482842673645584386", Request{Kind: KindActivityThread, ActivityID: "7482842673645584386"}},
		{"feed urn", "https://www.linkedin.com/feed/update/urn:li:activity:7482842673645584386/", Request{Kind: KindActivityThread, ActivityID: "7482842673645584386"}},
		{"company posts", "https://www.linkedin.com/company/the-pragmatic-engineer/posts/", Request{Kind: KindCompanyPosts, Company: "the-pragmatic-engineer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.rawURL, err)
			}
			if got.Kind != tt.want.Kind || got.ActivityID != tt.want.ActivityID || got.Company != tt.want.Company {
				t.Fatalf("Parse(%q) = %+v, want %+v", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestParseRejectsAmbiguousOrUnsupportedLinkedInRoutes(t *testing.T) {
	for _, rawURL := range []string{
		"http://www.linkedin.com/posts/example-activity-7482842673645584386",
		"https://engineering.linkedin.com/posts/example-activity-7482842673645584386",
		"https://www.linkedin.com/posts/example-activity-000",
		"https://www.linkedin.com/posts/example-activity-7482842673645584386?activity=7482842673645584387",
		"https://www.linkedin.com/feed/update/urn:li:share:7482842673645584386/",
		"https://www.linkedin.com/feed/update/urn:li:activity:7482842673645584386?trk=feed",
		"https://www.linkedin.com/feed/",
		"https://www.linkedin.com/in/example/",
		"https://www.linkedin.com/company/example/about/",
		"https://linkedin.com.example/posts/example-activity-7482842673645584386",
	} {
		if _, err := Parse(rawURL); err == nil {
			t.Fatalf("Parse(%q) succeeded, want rejection", rawURL)
		}
	}
}

func TestValidateFinalURLRequiresExactActivityOrCompanyIdentity(t *testing.T) {
	activity, err := Parse("https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateFinalURL(activity, "https://www.linkedin.com/feed/update/urn:li:activity:7482842673645584386/"); err != nil {
		t.Fatalf("equivalent final activity URL rejected: %v", err)
	}
	for _, finalURL := range []string{
		"https://www.linkedin.com/posts/example-activity-7482842673645584387-9aSD/",
		"https://www.linkedin.com/company/example/posts/",
	} {
		if _, err := ValidateFinalURL(activity, finalURL); err == nil {
			t.Fatalf("ValidateFinalURL(%q) succeeded, want activity mismatch", finalURL)
		}
	}

	company, err := Parse("https://www.linkedin.com/company/the-pragmatic-engineer/posts/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateFinalURL(company, "https://www.linkedin.com/company/other/posts/"); err == nil {
		t.Fatal("ValidateFinalURL accepted company drift")
	}
}

func TestTraversalRequiresTwoSettledNoProgressCycles(t *testing.T) {
	traversal := NewTraversal()
	if got := traversal.Observe(Page{ActivityIDs: []string{"7482842673645584386"}, TerminalExtent: 100}, false, true); got.Exhausted {
		t.Fatal("new activity must not be exhausted")
	}
	if got := traversal.Observe(Page{TerminalExtent: 100}, false, true); got.Exhausted {
		t.Fatal("one settled no-progress cycle must not exhaust")
	}
	if got := traversal.Observe(Page{TerminalExtent: 100}, true, true); got.Exhausted {
		t.Fatal("unresolved reply control must prevent exhaustion")
	}
	if got := traversal.Observe(Page{TerminalExtent: 100}, false, false); got.Exhausted {
		t.Fatal("unsettled observation must prevent exhaustion")
	}
	if got := traversal.Observe(Page{TerminalExtent: 100}, false, true); got.Exhausted {
		t.Fatal("first settled stable cycle must not exhaust")
	}
	if got := traversal.Observe(Page{TerminalExtent: 100}, false, true); !got.Exhausted {
		t.Fatal("second settled stable cycle must exhaust")
	}
}
