package reddit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSubredditListing(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		url, subreddit, sort, window string
	}{
		{"https://www.reddit.com/r/formula1/", "formula1", "hot", ""},
		{"https://www.reddit.com/r/formula1/new/", "formula1", "new", ""},
		{"https://www.reddit.com/r/formula1/top/?t=week", "formula1", "top", "week"},
	} {
		req, err := Parse(tt.url)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.url, err)
		}
		if req.Subreddit != tt.subreddit || req.Sort != tt.sort || req.Window != tt.window {
			t.Fatalf("Parse(%q) = %+v", tt.url, req)
		}
	}
}

func TestParseClassifiesDiscoveredRedditURLsWithoutCoercion(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		rawURL    string
		kind      Kind
		subreddit string
		postID    string
		commentID string
		username  string
	}{
		{"https://www.reddit.com/r/formula1/top/?t=week", KindSubredditListing, "formula1", "", "", ""},
		{"https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/", KindThread, "codex", "t3_1v010h6", "", ""},
		{"https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/ozckogc/", KindThread, "codex", "t3_1v010h6", "ozckogc", ""},
		{"https://www.reddit.com/user/celticpaladin/comments/", KindUserProfile, "", "", "", "celticpaladin"},
	} {
		got, err := Parse(tt.rawURL)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.rawURL, err)
		}
		if got.Kind != tt.kind || got.Subreddit != tt.subreddit || got.PostID != tt.postID || got.CommentID != tt.commentID || got.Username != tt.username {
			t.Fatalf("Parse(%q) = %+v", tt.rawURL, got)
		}
	}
	if _, err := ParseExpected("https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/", KindUserProfile); err == nil {
		t.Fatal("ParseExpected accepted a thread as user profile")
	}
}

func TestParseRejectsUnsupportedRoutesAndQueries(t *testing.T) {
	t.Parallel()
	for _, rawURL := range []string{
		"https://www.reddit.com/r/formula1/top/?t=century",
		"https://www.reddit.com/r/formula1/new/?t=week",
		"https://www.reddit.com/r/formula1/?feedViewType=compactView",
		"https://www.reddit.com/r/formula1/top/?t=week&t=day",
		"https://www.reddit.com/r/formula1/comments//post/",
		"https://www.reddit.com/r/formula1/comments/abc123/post/def456/extra/",
		"https://www.reddit.com/r/formula1/comments/abc123/post/?context=3",
		"https://www.reddit.com/user/example/posts/",
		"https://reddit.com.example/r/formula1/",
	} {
		if _, err := Parse(rawURL); err == nil {
			t.Fatalf("Parse(%q) succeeded", rawURL)
		}
	}
}

func TestValidateFinalURLPreservesCommentIdentity(t *testing.T) {
	t.Parallel()
	request, err := Parse("https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/ozckogc/")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFinalURL(request, "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/ozckogc/"); err != nil {
		t.Fatalf("same comment: %v", err)
	}
	if err := ValidateFinalURL(request, "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/otherid/"); err == nil {
		t.Fatal("ValidateFinalURL accepted a different comment in the same thread")
	}
}

func TestValidateFinalURLPreservesListingIdentity(t *testing.T) {
	t.Parallel()
	req, err := Parse("https://www.reddit.com/r/formula1/top/?t=week")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFinalURL(req, "https://www.reddit.com/r/formula1/top/?t=week"); err != nil {
		t.Fatalf("same listing: %v", err)
	}
	if err := ValidateFinalURL(req, "https://www.reddit.com/r/formula1/top/?t=week&feedViewType=compactView"); err != nil {
		t.Fatalf("presentation-only final query: %v", err)
	}
	for _, finalURL := range []string{
		"https://www.reddit.com/r/formula1/top/?t=day",
		"https://www.reddit.com/r/motorsports/top/?t=week",
		"https://www.reddit.com/r/formula1/comments/1abc234/post/",
	} {
		if err := ValidateFinalURL(req, finalURL); err == nil {
			t.Fatalf("ValidateFinalURL(%q) succeeded", finalURL)
		}
	}
}

func TestDecodePageRejectsCrossSubredditAndDuplicateIDs(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"threads":[{"id":"t3_abc123","permalink":"/r/formula1/comments/abc123/a_post/","subreddit":"formula1","title":"A post","author":"driver","score":7,"comment_count":2}],"next_cursor":"cursor"}`)
	page, err := DecodePage("formula1", payload)
	if err != nil || len(page.Threads) != 1 || page.NextCursor != "cursor" {
		t.Fatalf("DecodePage() = %+v, %v", page, err)
	}
	for _, bad := range []string{
		`{"threads":[{"id":"t3_abc123","permalink":"/r/motorsports/comments/abc123/a_post/","subreddit":"motorsports","title":"A","author":"u"}]}`,
		`{"threads":[{"id":"t3_abc123","permalink":"/r/formula1/comments/abc123/a/","subreddit":"formula1","title":"A","author":"u"},{"id":"t3_abc123","permalink":"/r/formula1/comments/abc123/a/","subreddit":"formula1","title":"B","author":"u"}]}`,
	} {
		if _, err := DecodePage("formula1", json.RawMessage(bad)); err == nil {
			t.Fatalf("DecodePage(%s) succeeded", bad)
		}
	}
}

func TestCollectionExpressionBindsCardAndSubredditIdentity(t *testing.T) {
	t.Parallel()
	req, err := Parse("https://www.reddit.com/r/formula1/top/?t=week")
	if err != nil {
		t.Fatal(err)
	}
	expression := CollectionExpression(req, 20)
	for _, want := range []string{"shreddit-feed shreddit-post", "subreddit-name", "t3_", "permalink", "post-title", "threads"} {
		if !strings.Contains(expression, want) {
			t.Fatalf("expression missing %q", want)
		}
	}
}

func TestTraversalTreatsRepeatedPagesAsTransientPlateau(t *testing.T) {
	t.Parallel()
	thread := func(id string) Thread {
		return Thread{ID: id, Permalink: "/r/formula1/comments/" + strings.TrimPrefix(id, "t3_") + "/post/", Subreddit: "formula1", Title: id}
	}
	traversal := NewTraversal()
	for _, step := range []struct {
		page          Page
		wantAdded     int
		wantStalled   int
		wantExhausted bool
	}{
		{Page{Threads: []Thread{thread("t3_a"), thread("t3_b")}, NextCursor: "cursor-a"}, 2, 0, false},
		{Page{Threads: []Thread{thread("t3_a"), thread("t3_b")}, NextCursor: "cursor-a"}, 0, 1, false},
		{Page{Threads: []Thread{thread("t3_a"), thread("t3_b")}, NextCursor: "cursor-a"}, 0, 2, false},
		{Page{Threads: []Thread{thread("t3_a"), thread("t3_b"), thread("t3_c")}, NextCursor: "cursor-b"}, 1, 0, false},
		{Page{}, 0, 1, true},
	} {
		got := traversal.Observe(step.page, 500)
		if got.Added != step.wantAdded || got.Stalled != step.wantStalled || got.Exhausted != step.wantExhausted {
			t.Fatalf("Observe(%+v) = %+v", step.page, got)
		}
	}
	if got := traversal.Threads(); len(got) != 3 || got[2].ID != "t3_c" {
		t.Fatalf("Threads() = %+v", got)
	}
}

func TestAdvanceExpressionUsesSemanticContinuationControls(t *testing.T) {
	t.Parallel()
	expression := AdvanceExpression()
	for _, want := range []string{"__cdp_cli_reddit_advance__", "aria-label", "[role=\"button\"]", "continuation_control", "scroll"} {
		if !strings.Contains(expression, want) {
			t.Fatalf("AdvanceExpression missing %q", want)
		}
	}
}

func TestDecodeThreadPageEnforcesRootAndRecordKinds(t *testing.T) {
	t.Parallel()
	request, err := Parse("https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/")
	if err != nil {
		t.Fatal(err)
	}
	good := `{"records":[{"kind":"submission","id":"t3_1v010h6","canonical_url":"/r/codex/comments/1v010h6/the_sun_came_out/","subreddit":"codex","root_thread_id":"t3_1v010h6","title":"The sun came out.","discovery_surface":"thread_root"},{"kind":"comment","id":"t1_ozckogc","canonical_url":"/r/codex/comments/1v010h6/comment/ozckogc/","subreddit":"codex","root_thread_id":"t3_1v010h6","parent_id":"t3_1v010h6","author":"CelticPaladin","discovery_surface":"thread_comment_tree"}]}`
	if page, err := DecodeThreadPage(request, json.RawMessage(good)); err != nil || len(page.Records) != 2 {
		t.Fatalf("DecodeThreadPage() = %+v, %v", page, err)
	}
	for _, bad := range []string{
		`{"records":[{"kind":"comment","id":"t1_other","canonical_url":"/r/codex/comments/other/thread/","subreddit":"codex","root_thread_id":"t3_other","author":"other","discovery_surface":"thread_comment_tree"}]}`,
		`{"records":[{"kind":"listing_thread","id":"t3_1v010h6","canonical_url":"/r/codex/comments/1v010h6/the_sun_came_out/","root_thread_id":"t3_1v010h6","title":"bad","discovery_surface":"thread_root"}]}`,
		`{"records":[{"kind":"comment","id":"t1_ozckogc","canonical_url":"/r/codex/comments/1v010h6/comment/ozckogc/","subreddit":"codex","root_thread_id":"t3_1v010h6","parent_id":"t3_1v010h6","author":"CelticPaladin","discovery_surface":"thread_comment_tree"}]}`,
		`{"records":[{"kind":"submission","id":"t3_1v010h6","canonical_url":"/r/codex/comments/other/post/","subreddit":"codex","root_thread_id":"t3_1v010h6","title":"wrong URL","discovery_surface":"thread_root"}]}`,
		`{"records":[{"kind":"submission","id":"t3_1v010h6","canonical_url":"/r/codex/comments/1v010h6/the_sun_came_out/","subreddit":"codex","root_thread_id":"t3_1v010h6","title":"root","discovery_surface":"thread_root"},{"kind":"comment","id":"t1_ozckogc","canonical_url":"/r/codex/comments/1v010h6/comment/othercomment/","subreddit":"codex","root_thread_id":"t3_1v010h6","parent_id":"t3_1v010h6","author":"CelticPaladin","discovery_surface":"thread_comment_tree"}]}`,
	} {
		if _, err := DecodeThreadPage(request, json.RawMessage(bad)); err == nil {
			t.Fatalf("DecodeThreadPage(%s) succeeded", bad)
		}
	}
}

func TestThreadExpressionBindsRootAndCommentIdentity(t *testing.T) {
	t.Parallel()
	request, err := Parse("https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/")
	if err != nil {
		t.Fatal(err)
	}
	expression := ThreadExpression(request, 500)
	for _, want := range []string{"__cdp_cli_reddit_thread_records__", "shreddit-comment", "thingid", "postid", "root_thread_id", "thread_comment_tree"} {
		if !strings.Contains(expression, want) {
			t.Fatalf("ThreadExpression missing %q", want)
		}
	}
}

func TestDecodeUserPageEnforcesAuthorAndRecordKinds(t *testing.T) {
	t.Parallel()
	request, err := Parse("https://www.reddit.com/user/celticpaladin/comments/")
	if err != nil {
		t.Fatal(err)
	}
	good := `{"records":[{"kind":"comment","id":"t1_ozckogc","canonical_url":"/r/codex/comments/1v010h6/comment/ozckogc/","subreddit":"codex","root_thread_id":"t3_1v010h6","author":"celticpaladin","discovery_surface":"user_comment"}]}`
	if page, err := DecodeUserPage(request, json.RawMessage(good)); err != nil || len(page.Records) != 1 {
		t.Fatalf("DecodeUserPage() = %+v, %v", page, err)
	}
	for _, bad := range []string{
		`{"records":[{"kind":"comment","id":"t1_ozckogc","canonical_url":"/r/codex/comments/1v010h6/comment/ozckogc/","root_thread_id":"t3_1v010h6","author":"other","discovery_surface":"user_comment"}]}`,
		`{"records":[{"kind":"listing_thread","id":"t3_1v010h6","canonical_url":"/r/codex/comments/1v010h6/post/","author":"celticpaladin","discovery_surface":"user_submission"}]}`,
		`{"records":[{"kind":"comment","id":"t1_ozckogc","canonical_url":"/r/codex/comments/1v010h6/comment/othercomment/","root_thread_id":"t3_1v010h6","author":"celticpaladin","discovery_surface":"user_comment"}]}`,
	} {
		if _, err := DecodeUserPage(request, json.RawMessage(bad)); err == nil {
			t.Fatalf("DecodeUserPage(%s) succeeded", bad)
		}
	}
}

func TestUserExpressionBindsAuthorAndCanonicalComment(t *testing.T) {
	t.Parallel()
	request, err := Parse("https://www.reddit.com/user/celticpaladin/comments/")
	if err != nil {
		t.Fatal(err)
	}
	expression := UserExpression(request, 100)
	for _, want := range []string{"__cdp_cli_reddit_user_records__", "shreddit-profile-comment", "comment-id", "user_comment", "user_submission"} {
		if !strings.Contains(expression, want) {
			t.Fatalf("UserExpression missing %q", want)
		}
	}
}
