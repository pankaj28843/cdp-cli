package cli

import (
	"strings"
	"testing"
)

func TestPlanRenderedExtractContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		rawURL             string
		selector           string
		mode               string
		wantProfile        string
		wantStrategy       string
		wantNavigationURL  string
		wantSelector       string
		wantRepresentation string
		wantRewritten      bool
		wantDomainMatched  bool
		wantHTMLURL        string
		wantPDFURL         string
		wantSourceURL      string
		wantAbstractURL    string
	}{
		{
			name:               "generic page",
			rawURL:             "https://example.test/article",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "generic",
			wantStrategy:       "legacy-html",
			wantNavigationURL:  "https://example.test/article",
			wantSelector:       "body",
			wantRepresentation: "rendered-html",
		},
		{
			name:               "arxiv html",
			rawURL:             "https://arxiv.org/html/2606.26289v1",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "arxiv",
			wantStrategy:       "semantic-dom",
			wantNavigationURL:  "https://arxiv.org/html/2606.26289v1",
			wantSelector:       "article.ltx_document",
			wantRepresentation: "html",
			wantDomainMatched:  true,
			wantHTMLURL:        "https://arxiv.org/html/2606.26289v1",
			wantPDFURL:         "https://arxiv.org/pdf/2606.26289v1",
			wantSourceURL:      "https://arxiv.org/src/2606.26289v1",
			wantAbstractURL:    "https://arxiv.org/abs/2606.26289v1",
		},
		{
			name:               "arxiv pdf chooses html",
			rawURL:             "https://arxiv.org/pdf/2603.26487.pdf",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "arxiv",
			wantStrategy:       "semantic-dom",
			wantNavigationURL:  "https://arxiv.org/html/2603.26487",
			wantSelector:       "article.ltx_document",
			wantRepresentation: "html",
			wantRewritten:      true,
			wantDomainMatched:  true,
			wantHTMLURL:        "https://arxiv.org/html/2603.26487",
			wantPDFURL:         "https://arxiv.org/pdf/2603.26487",
			wantSourceURL:      "https://arxiv.org/src/2603.26487",
			wantAbstractURL:    "https://arxiv.org/abs/2603.26487",
		},
		{
			name:               "arxiv tex source chooses html",
			rawURL:             "https://arxiv.org/src/2603.26487",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "arxiv",
			wantStrategy:       "semantic-dom",
			wantNavigationURL:  "https://arxiv.org/html/2603.26487",
			wantSelector:       "article.ltx_document",
			wantRepresentation: "html",
			wantRewritten:      true,
			wantDomainMatched:  true,
			wantHTMLURL:        "https://arxiv.org/html/2603.26487",
			wantPDFURL:         "https://arxiv.org/pdf/2603.26487",
			wantSourceURL:      "https://arxiv.org/src/2603.26487",
			wantAbstractURL:    "https://arxiv.org/abs/2603.26487",
		},
		{
			name:               "legacy arxiv identifier",
			rawURL:             "https://www.arxiv.org/abs/cs/9901001v2",
			selector:           "main",
			mode:               "auto",
			wantProfile:        "arxiv",
			wantStrategy:       "semantic-dom",
			wantNavigationURL:  "https://arxiv.org/html/cs/9901001v2",
			wantSelector:       "main",
			wantRepresentation: "html",
			wantRewritten:      true,
			wantDomainMatched:  true,
			wantHTMLURL:        "https://arxiv.org/html/cs/9901001v2",
			wantPDFURL:         "https://arxiv.org/pdf/cs/9901001v2",
			wantSourceURL:      "https://arxiv.org/src/cs/9901001v2",
			wantAbstractURL:    "https://arxiv.org/abs/cs/9901001v2",
		},
		{
			name:               "hacker news discussion",
			rawURL:             "https://news.ycombinator.com/item?id=46641042",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "hacker-news",
			wantStrategy:       "discussion-tree",
			wantNavigationURL:  "https://news.ycombinator.com/item?id=46641042",
			wantSelector:       "table.fatitem",
			wantRepresentation: "discussion",
			wantDomainMatched:  true,
		},
		{
			name:               "arxiv lookalike host stays generic",
			rawURL:             "https://arxiv.org.example/pdf/2603.26487",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "generic",
			wantStrategy:       "legacy-html",
			wantNavigationURL:  "https://arxiv.org.example/pdf/2603.26487",
			wantSelector:       "body",
			wantRepresentation: "rendered-html",
		},
		{
			name:               "malformed modern arxiv identifier stays generic",
			rawURL:             "https://arxiv.org/pdf/2603.26487/appendix",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "generic",
			wantStrategy:       "legacy-html",
			wantNavigationURL:  "https://arxiv.org/pdf/2603.26487/appendix",
			wantSelector:       "body",
			wantRepresentation: "rendered-html",
		},
		{
			name:               "encoded arxiv path punctuation stays generic",
			rawURL:             "https://arxiv.org/pdf/2603.26487%3Fdownload=1",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "generic",
			wantStrategy:       "legacy-html",
			wantNavigationURL:  "https://arxiv.org/pdf/2603.26487%3Fdownload=1",
			wantSelector:       "body",
			wantRepresentation: "rendered-html",
		},
		{
			name:               "zero arxiv version stays generic",
			rawURL:             "https://arxiv.org/abs/2603.26487v0",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "generic",
			wantStrategy:       "legacy-html",
			wantNavigationURL:  "https://arxiv.org/abs/2603.26487v0",
			wantSelector:       "body",
			wantRepresentation: "rendered-html",
		},
		{
			name:               "hacker news lookalike host stays generic",
			rawURL:             "https://news.ycombinator.com.example/item?id=46641042",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "generic",
			wantStrategy:       "legacy-html",
			wantNavigationURL:  "https://news.ycombinator.com.example/item?id=46641042",
			wantSelector:       "body",
			wantRepresentation: "rendered-html",
		},
		{
			name:               "arxiv unsupported scheme stays generic",
			rawURL:             "file://arxiv.org/pdf/2603.26487",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "generic",
			wantStrategy:       "legacy-html",
			wantNavigationURL:  "file://arxiv.org/pdf/2603.26487",
			wantSelector:       "body",
			wantRepresentation: "rendered-html",
		},
		{
			name:               "hacker news unsupported scheme stays generic",
			rawURL:             "file://news.ycombinator.com/item?id=46641042",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "generic",
			wantStrategy:       "legacy-html",
			wantNavigationURL:  "file://news.ycombinator.com/item?id=46641042",
			wantSelector:       "body",
			wantRepresentation: "rendered-html",
		},
		{
			name:               "hacker news non discussion stays generic",
			rawURL:             "https://news.ycombinator.com/news",
			selector:           "body",
			mode:               "auto",
			wantProfile:        "generic",
			wantStrategy:       "legacy-html",
			wantNavigationURL:  "https://news.ycombinator.com/news",
			wantSelector:       "body",
			wantRepresentation: "rendered-html",
		},
		{
			name:               "generic override disables arxiv profile",
			rawURL:             "https://arxiv.org/pdf/2603.26487",
			selector:           "body",
			mode:               "generic",
			wantProfile:        "generic",
			wantStrategy:       "legacy-html",
			wantNavigationURL:  "https://arxiv.org/pdf/2603.26487",
			wantSelector:       "body",
			wantRepresentation: "rendered-html",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := planRenderedExtractContent(tt.rawURL, tt.selector, tt.mode)
			if err != nil {
				t.Fatalf("planRenderedExtractContent() error = %v", err)
			}
			if string(got.Profile) != tt.wantProfile ||
				string(got.Strategy) != tt.wantStrategy ||
				got.NavigationURL != tt.wantNavigationURL ||
				got.Selector != tt.wantSelector ||
				got.Representation != tt.wantRepresentation ||
				got.Rewritten != tt.wantRewritten ||
				got.DomainMatched != tt.wantDomainMatched {
				t.Fatalf("planRenderedExtractContent() = %+v", got)
			}
			if got.Representations.HTML != tt.wantHTMLURL ||
				got.Representations.PDF != tt.wantPDFURL ||
				got.Representations.Source != tt.wantSourceURL ||
				got.Representations.Abstract != tt.wantAbstractURL {
				t.Fatalf("planRenderedExtractContent() representations = %+v", got.Representations)
			}
		})
	}
}

func TestRenderedExtractSocialProfileExpressionsKeepPrimaryIdentityBoundaries(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		url  string
		want []string
	}{
		{"https://x.com/karpathy", []string{"article.querySelector(\"article\")", "[data-testid=\"User-Name\"]", "tweet-text-show-more-link", "/status/"}},
		{"https://www.reddit.com/user/CelticPaladin/", []string{"shreddit-profile-comment", "thread for ", "a[href^=\"/user/\"]"}},
		{"https://www.linkedin.com/company/the-pragmatic-engineer/posts/", []string{".update-components-actor__meta", "data-urn^=\"urn:li:activity:\"", "more.click()"}},
	} {
		plan, err := planRenderedExtractContent(tt.url, "body", "auto")
		if err != nil {
			t.Fatalf("plan %q: %v", tt.url, err)
		}
		expression := renderedExtractContentExpression(plan)
		for _, token := range tt.want {
			if !strings.Contains(expression, token) {
				t.Fatalf("%q expression lost identity boundary %q", tt.url, token)
			}
		}
	}
}

func TestPlanRenderedExtractContentRejectsUnknownMode(t *testing.T) {
	t.Parallel()

	if _, err := planRenderedExtractContent("https://example.test/article", "body", "native-only"); err == nil {
		t.Fatal("planRenderedExtractContent() error = nil, want unsupported mode error")
	}
}

func TestRenderedExtractContentNativeEligibleUsesResolvedURL(t *testing.T) {
	t.Parallel()

	arxiv, err := planRenderedExtractContent("https://arxiv.org/pdf/2603.26487", "body", "auto")
	if err != nil {
		t.Fatalf("plan arxiv content: %v", err)
	}
	hn, err := planRenderedExtractContent("https://news.ycombinator.com/item?id=46641042", "body", "auto")
	if err != nil {
		t.Fatalf("plan Hacker News content: %v", err)
	}
	tests := []struct {
		name     string
		plan     renderedExtractContentPlan
		finalURL string
		want     bool
	}{
		{name: "arxiv html", plan: arxiv, finalURL: "https://arxiv.org/html/2603.26487", want: true},
		{name: "arxiv html www", plan: arxiv, finalURL: "https://www.arxiv.org/html/2603.26487", want: true},
		{name: "arxiv pdf shell is not semantic html", plan: arxiv, finalURL: "https://arxiv.org/pdf/2603.26487", want: false},
		{name: "arxiv lookalike redirect", plan: arxiv, finalURL: "https://arxiv.org.example/html/2603.26487", want: false},
		{name: "arxiv unsupported scheme", plan: arxiv, finalURL: "file://arxiv.org/html/2603.26487", want: false},
		{name: "arxiv unrelated redirect", plan: arxiv, finalURL: "https://example.test/article", want: false},
		{name: "hn discussion", plan: hn, finalURL: "https://news.ycombinator.com/item?id=46641042", want: true},
		{name: "hn lookalike redirect", plan: hn, finalURL: "https://news.ycombinator.com.example/item?id=46641042", want: false},
		{name: "hn unsupported scheme", plan: hn, finalURL: "file://news.ycombinator.com/item?id=46641042", want: false},
		{name: "hn frontpage redirect", plan: hn, finalURL: "https://news.ycombinator.com/news", want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := renderedExtractContentNativeEligible(tt.plan, tt.finalURL); got != tt.want {
				t.Fatalf("renderedExtractContentNativeEligible(%+v, %q) = %v, want %v", tt.plan, tt.finalURL, got, tt.want)
			}
		})
	}
}

func TestRenderedExtractCaptureSelectorUsesNativeSemanticRoot(t *testing.T) {
	for _, tt := range []struct {
		name, rawURL, mode, want string
	}{
		{"X post", "https://x.com/karpathy/status/2079610838143623371", "auto", `article[data-testid="tweet"]`},
		{"Reddit thread", "https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/", "auto", "shreddit-post"},
		{"arXiv paper", "https://arxiv.org/html/2604.12374v1", "auto", "article.ltx_document"},
		{"generic override", "https://x.com/karpathy/status/2079610838143623371", "generic", "body"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := planRenderedExtractContent(tt.rawURL, "body", tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			if got := renderedExtractCaptureSelector(plan); got != tt.want {
				t.Fatalf("capture selector = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderedExtractContentExpressionHandlesNamespacedMathML(t *testing.T) {
	t.Parallel()

	plan, err := planRenderedExtractContent("https://arxiv.org/html/2606.26289v1", "body", "auto")
	if err != nil {
		t.Fatalf("plan arxiv content: %v", err)
	}
	expression := renderedExtractContentExpression(plan)
	if !strings.Contains(expression, `const localName = String(node.localName || "").toLowerCase();`) ||
		!strings.Contains(expression, `if (localName === "math") return mathValue(node);`) {
		t.Fatalf("rendered content expression does not recognize namespaced MathML: %s", expression)
	}
}

func TestRenderedExtractContentExpressionNormalizesArxivFrontMatterAndListTags(t *testing.T) {
	t.Parallel()

	plan, err := planRenderedExtractContent("https://arxiv.org/html/2603.17419", "body", "auto")
	if err != nil {
		t.Fatalf("plan arxiv content: %v", err)
	}
	expression := renderedExtractContentExpression(plan)
	for _, want := range []string{
		`meta[name="citation_title"]`,
		`root.querySelector("h1.ltx_title_document")`,
		`root.querySelector(":scope > .ltx_logical-block:first-child .ltx_align_center .ltx_font_bold")`,
		`child.matches(".ltx_tag_item")`,
		`"# " + escapeText(citationTitle)`,
	} {
		if !strings.Contains(expression, want) {
			t.Fatalf("rendered content expression does not normalize arXiv front matter/list tags; missing %q", want)
		}
	}
}

func TestRenderedExtractContentExpressionPreservesDiscussionIndentation(t *testing.T) {
	t.Parallel()

	plan, err := planRenderedExtractContent("https://news.ycombinator.com/item?id=46641042", "body", "auto")
	if err != nil {
		t.Fatalf("plan Hacker News content: %v", err)
	}
	expression := renderedExtractContentExpression(plan)
	if !strings.Contains(expression, `.replace(/^[ \t]+$/gm, "")`) ||
		strings.Contains(expression, `.replace(/\n[ \t]+/g, "\n")`) {
		t.Fatalf("rendered content expression does not preserve structured Markdown indentation: %s", expression)
	}
	for _, want := range []string{`slice(0, 500)`, `Math.min(8`, `102400`} {
		if !strings.Contains(expression, want) {
			t.Fatalf("rendered HN discussion expression is missing bound %q", want)
		}
	}
}
