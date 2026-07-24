package x

import "testing"

func TestParseCanonicalizesAcceptedXRoutes(t *testing.T) {
	tests := []struct {
		name, rawURL string
		want         Request
	}{
		{"x post", "https://x.com/Karpathy/status/2079610838143623371?ref=feed#top", Request{Kind: KindPostThread, Handle: "karpathy", StatusID: "2079610838143623371"}},
		{"twitter media", "https://www.twitter.com/karpathy/status/2079610838143623371/photo/1", Request{Kind: KindPostThread, Handle: "karpathy", StatusID: "2079610838143623371", MediaIndex: 1}},
		{"profile", "https://www.x.com/Karpathy/", Request{Kind: KindProfilePosts, Handle: "karpathy"}},
		{"with replies", "https://twitter.com/karpathy/with_replies", Request{Kind: KindProfilePosts, Handle: "karpathy", Surface: "with_replies"}},
		{"canonical decimal status", "https://x.com/karpathy/status/0000000000000000000000000000000000000000123", Request{Kind: KindPostThread, Handle: "karpathy", StatusID: "123"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.rawURL, err)
			}
			if got.Kind != tt.want.Kind || got.Handle != tt.want.Handle || got.StatusID != tt.want.StatusID || got.MediaIndex != tt.want.MediaIndex || got.Surface != tt.want.Surface {
				t.Fatalf("Parse(%q) = %+v, want %+v", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestParseRejectsUnsupportedXRoutes(t *testing.T) {
	for _, rawURL := range []string{
		"http://x.com/karpathy/status/2079610838143623371",
		"https://mobile.x.com/karpathy/status/2079610838143623371",
		"https://x.com/i/status/2079610838143623371",
		"https://x.com/karpathy/status/not-a-number",
		"https://x.com/karpathy/status/000",
		"https://x.com/karpathy/status/2079610838143623371/photo/0",
		"https://x.com/karpathy/status/2079610838143623371/photo/1/extra",
		"https://x.com/karpathy/media",
		"https://x.com/search?q=collector",
		"https://x.com.example/karpathy/status/2079610838143623371",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := Parse(rawURL); err == nil {
				t.Fatalf("Parse(%q) succeeded, want rejection", rawURL)
			}
		})
	}
}

func TestParseRejectsReservedProfileRoutes(t *testing.T) {
	for _, route := range []string{"home", "explore", "search", "i", "settings", "messages", "notifications", "compose", "login", "signup", "intent", "share"} {
		rawURL := "https://x.com/" + route
		t.Run(route, func(t *testing.T) {
			if _, err := Parse(rawURL); err == nil {
				t.Fatalf("Parse(%q) succeeded, want reserved-route rejection", rawURL)
			}
		})
	}
}

func TestValidateFinalURLRequiresIdentityAgreement(t *testing.T) {
	post, err := Parse("https://x.com/karpathy/status/2079610838143623371")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateFinalURL(post, "https://twitter.com/Karpathy/status/2079610838143623371"); err != nil {
		t.Fatalf("equivalent post alias rejected: %v", err)
	}
	for _, finalURL := range []string{
		"https://x.com/karpathy/status/2079610838143623372",
		"https://x.com/i/flow/login",
		"https://x.com/other/status/2079610838143623371",
	} {
		if _, err := ValidateFinalURL(post, finalURL); err == nil {
			t.Fatalf("ValidateFinalURL(%q) succeeded, want mismatch", finalURL)
		}
	}

	profile, err := Parse("https://x.com/old_handle")
	if err != nil {
		t.Fatal(err)
	}
	final, err := ValidateFinalURL(profile, "https://x.com/new_handle")
	if err != nil {
		t.Fatalf("profile rename rejected: %v", err)
	}
	if final.Handle != "new_handle" {
		t.Fatalf("final profile handle = %q, want new_handle", final.Handle)
	}
	if !final.HandleChanged || final.RequestedHandle != "old_handle" {
		t.Fatalf("profile rename verification state = %+v, want changed old_handle", final)
	}
}

func TestTraversalExhaustionNeedsTwoTerminalNoProgressCycles(t *testing.T) {
	traversal := NewTraversal()
	if got := traversal.Observe(Page{StatusIDs: []string{"1"}, TerminalExtent: 100}, false); got.Exhausted {
		t.Fatal("new status must not be exhausted")
	}
	if got := traversal.Observe(Page{TerminalExtent: 100}, false); got.Exhausted {
		t.Fatal("one terminal no-progress cycle must not be exhausted")
	}
	if got := traversal.Observe(Page{TerminalExtent: 101}, false); got.Exhausted {
		t.Fatal("terminal extent movement must disprove exhaustion")
	}
	if got := traversal.Observe(Page{TerminalExtent: 101}, false); got.Exhausted {
		t.Fatal("first stable terminal no-progress cycle must not be exhausted")
	}
	if got := traversal.Observe(Page{TerminalExtent: 101}, false); !got.Exhausted {
		t.Fatal("second stable terminal no-progress cycle must prove exhaustion")
	}
}

func TestTraversalChangedContinuationTokenPreventsExhaustion(t *testing.T) {
	traversal := NewTraversal()
	traversal.Observe(Page{TerminalExtent: 100, ContinuationToken: "cursor-a"}, false)
	if got := traversal.Observe(Page{TerminalExtent: 100, ContinuationToken: "cursor-b"}, false); got.Exhausted {
		t.Fatal("changed continuation token must reset exhaustion evidence")
	}
	if got := traversal.Observe(Page{TerminalExtent: 100, ContinuationToken: "cursor-b"}, false); got.Exhausted {
		t.Fatal("first stable token observation must not be exhausted")
	}
	if got := traversal.Observe(Page{TerminalExtent: 100, ContinuationToken: "cursor-b"}, false); !got.Exhausted {
		t.Fatal("second stable token observation must be exhausted")
	}
}
