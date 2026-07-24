package cli

import (
	"strings"
	"testing"
)

func TestPlanRenderedExtractContentSocialProfiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rawURL      string
		wantProfile string
	}{
		{
			name:        "x status",
			rawURL:      "https://x.com/karpathy/status/2079610838143623371",
			wantProfile: "x",
		},
		{
			name:        "x www status with query",
			rawURL:      "https://www.x.com/karpathy/status/2079610838143623371?ref=feed",
			wantProfile: "x",
		},
		{
			name:        "linkedin direct post",
			rawURL:      "https://www.linkedin.com/posts/the-pragmatic-engineer_what-is-loop-engineering-looking-closer-activity-7482842673645584386-9aSD/",
			wantProfile: "linkedin",
		},
		{
			name:        "linkedin locale post",
			rawURL:      "https://de.linkedin.com/posts/example-activity-7482842673645584386-9aSD",
			wantProfile: "linkedin",
		},
		{
			name:        "reddit post any subreddit",
			rawURL:      "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/",
			wantProfile: "reddit",
		},
		{
			name:        "reddit comment permalink any subreddit",
			rawURL:      "https://reddit.com/r/golang/comments/1abc234/a_post/xyz789/",
			wantProfile: "reddit",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan, err := planRenderedExtractContent(tt.rawURL, "body", "auto")
			if err != nil {
				t.Fatalf("planRenderedExtractContent() error = %v", err)
			}
			if got := string(plan.Profile); got != tt.wantProfile {
				t.Fatalf("profile = %q, want %q; plan = %+v", got, tt.wantProfile, plan)
			}
			if !plan.DomainMatched {
				t.Fatalf("DomainMatched = false; plan = %+v", plan)
			}
		})
	}
}

func TestPlanRenderedExtractContentSocialProfileFeeds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		rawURL      string
		wantProfile string
	}{
		{"https://x.com/karpathy", "x-profile"},
		{"https://x.com/karpathy/", "x-profile"},
		{"https://www.reddit.com/user/CelticPaladin/", "reddit-user-profile"},
		{"https://www.reddit.com/r/formula1/", "reddit-subreddit"},
		{"https://www.reddit.com/r/formula1/top/?t=week", "reddit-subreddit"},
		{"https://www.linkedin.com/company/the-pragmatic-engineer/posts/", "linkedin-company-posts"},
	}
	for _, tt := range tests {
		t.Run(tt.rawURL, func(t *testing.T) {
			plan, err := planRenderedExtractContent(tt.rawURL, "body", "auto")
			if err != nil {
				t.Fatalf("planRenderedExtractContent() error = %v", err)
			}
			if got := string(plan.Profile); got != tt.wantProfile || !plan.DomainMatched {
				t.Fatalf("profile/domain = %q/%v, want %q/true; plan=%+v", got, plan.DomainMatched, tt.wantProfile, plan)
			}
			if tt.wantProfile == "x-profile" && !strings.Contains(renderedExtractContentExpression(plan), `article.querySelector("article")`) {
				t.Fatal("X profile extraction must reject nested quoted tweet cards")
			}
		})
	}
}

func TestPlanRenderedExtractContentSocialLookalikesAndRoutesStayGeneric(t *testing.T) {
	t.Parallel()

	for _, rawURL := range []string{
		"https://x.com/i/status/2079610838143623371",
		"https://x.com/karpathy/status/not-a-decimal",
		"https://x.com/karpathy/status/2079610838143623371/photo/1",
		"https://x.com.example/karpathy/status/2079610838143623371",
		"https://linkedin.com.example/posts/example-activity-7482842673645584386",
		"https://engineering.linkedin.com/posts/example-activity-7482842673645584386",
		"https://de.eu.linkedin.com/posts/example-activity-7482842673645584386",
		"https://www.linkedin.com/feed/update/urn:li:activity:7482842673645584386",
		"https://www.reddit.com/r/codex/top/?t=century",
		"https://www.reddit.com/r/codex/new/?t=week",
		"https://x.com/home",
		"https://x.com/karpathy/with_replies",
		"https://www.reddit.com/user/CelticPaladin/submitted/",
		"https://www.linkedin.com/company/the-pragmatic-engineer/about/",
		"https://www.reddit.com/comments/1v010h6/the_sun_came_out/",
		"https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/xyz789/extra",
		"https://reddit.com.example/r/codex/comments/1v010h6/the_sun_came_out/",
	} {
		rawURL := rawURL
		t.Run(rawURL, func(t *testing.T) {
			t.Parallel()
			plan, err := planRenderedExtractContent(rawURL, "body", "auto")
			if err != nil {
				t.Fatalf("planRenderedExtractContent() error = %v", err)
			}
			if got := string(plan.Profile); got != "generic" {
				t.Fatalf("profile = %q, want generic; plan = %+v", got, plan)
			}
		})
	}
}

func TestRenderedExtractContentSocialNativeEligibilityUsesFinalIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requested string
		finalURL  string
		want      bool
	}{
		{
			name:      "x canonical query removal",
			requested: "https://x.com/karpathy/status/2079610838143623371?ref=feed",
			finalURL:  "https://x.com/karpathy/status/2079610838143623371",
			want:      true,
		},
		{
			name:      "x different status",
			requested: "https://x.com/karpathy/status/2079610838143623371",
			finalURL:  "https://x.com/karpathy/status/2079610838143623372",
			want:      false,
		},
		{
			name:      "linkedin canonical suffix removal",
			requested: "https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD",
			finalURL:  "https://www.linkedin.com/posts/example-activity-7482842673645584386",
			want:      true,
		},
		{
			name:      "linkedin different activity",
			requested: "https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD",
			finalURL:  "https://www.linkedin.com/posts/example-activity-7482842673645584387",
			want:      false,
		},
		{
			name:      "reddit comment permalink keeps parent identity",
			requested: "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/",
			finalURL:  "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/xyz789/",
			want:      true,
		},
		{
			name:      "reddit different parent post",
			requested: "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/",
			finalURL:  "https://www.reddit.com/r/codex/comments/1v010h7/another_post/",
			want:      false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan, err := planRenderedExtractContent(tt.requested, "body", "auto")
			if err != nil {
				t.Fatalf("planRenderedExtractContent() error = %v", err)
			}
			if got := renderedExtractContentNativeEligible(plan, tt.finalURL); got != tt.want {
				t.Fatalf("renderedExtractContentNativeEligible(%+v, %q) = %v, want %v", plan, tt.finalURL, got, tt.want)
			}
		})
	}
}

func TestRenderedExtractContentSocialProfilesUseIdentityConfirmedBoundedRoots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawURL   string
		contains []string
	}{
		{
			name:   "x",
			rawURL: "https://x.com/karpathy/status/2079610838143623371",
			contains: []string{
				`article[data-testid="tweet"]`,
				`aria-label="Timeline: Conversation"`,
				`const allTweets = Array.from(document.querySelectorAll('article[data-testid="tweet"]'))`,
				`window.__cdp_cli_rendered_x_discussion__`,
				`cached.path === location.pathname`,
				`/status/`,
				`data-testid="tweetText"`,
				`## Replies (`,
				`slice(0, 500)`,
				`Math.min(8`,
				`102400`,
			},
		},
		{
			name:   "linkedin",
			rawURL: "https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD",
			contains: []string{
				`urn:li:activity:`,
				`.feed-shared-update-v2`,
				`article[data-id^="urn:li:comment:"]`,
				`## Comments (`,
				`slice(0, 500)`,
				`Math.min(8`,
				`102400`,
			},
		},
		{
			name:   "reddit",
			rawURL: "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/",
			contains: []string{
				`"t3_" + postID`,
				`shreddit-comment`,
				`slice(0, 500)`,
				`Math.min(8`,
				`102400`,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan, err := planRenderedExtractContent(tt.rawURL, "body", "auto")
			if err != nil {
				t.Fatalf("planRenderedExtractContent() error = %v", err)
			}
			expression := renderedExtractContentExpression(plan)
			for _, want := range tt.contains {
				if !strings.Contains(expression, want) {
					t.Fatalf("native %s expression is missing %q", tt.name, want)
				}
			}
		})
	}
}

func TestRenderedExtractDiscussionExpansionUsesVisibleAccessibleSourceControls(t *testing.T) {
	t.Parallel()
	for _, profile := range []renderedExtractContentProfile{
		renderedExtractContentProfileX,
		renderedExtractContentProfileLinkedIn,
		renderedExtractContentProfileReddit,
	} {
		expression := renderedExtractDiscussionExpansionExpression(renderedExtractContentPlan{Profile: profile})
		for _, want := range []string{
			"__cdp_cli_rendered_discussion_expand__",
			"(async () => {",
			"})()",
			"getAttribute(\"aria-label\")",
			"Timeline: Conversation",
			"tweet-text-show-more-link",
			`return {status: "unknown", interactions: 0}`,
			"snapshotX()",
			"requested_id",
			"node.closest(\"article\") === article",
			"byteLimit = 102400",
			`status: "invalid"`,
			"node.getAttribute(\"role\") === \"button\"",
			"node.getClientRects().length > 0",
			"const limit = 500",
			"setTimeout(resolve, settleMs)",
		} {
			if !strings.Contains(expression, want) {
				t.Fatalf("%s expansion expression is missing %q", profile, want)
			}
		}
		if profile == renderedExtractContentProfileX && strings.Contains(expression, `Boolean(node.closest("main"))`) {
			t.Fatal("X expansion must not scope generic Show more controls only to the main landmark")
		}
	}
}
