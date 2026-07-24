package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type renderedExtractContentProfile string

const (
	renderedExtractContentProfileGeneric         renderedExtractContentProfile = "generic"
	renderedExtractContentProfileArxiv           renderedExtractContentProfile = "arxiv"
	renderedExtractContentProfileHackerNews      renderedExtractContentProfile = "hacker-news"
	renderedExtractContentProfileX               renderedExtractContentProfile = "x"
	renderedExtractContentProfileLinkedIn        renderedExtractContentProfile = "linkedin"
	renderedExtractContentProfileReddit          renderedExtractContentProfile = "reddit"
	renderedExtractContentProfileRedditSubreddit renderedExtractContentProfile = "reddit-subreddit"
	renderedExtractContentProfileXProfile        renderedExtractContentProfile = "x-profile"
	renderedExtractContentProfileRedditUser      renderedExtractContentProfile = "reddit-user-profile"
	renderedExtractContentProfileLinkedInCompany renderedExtractContentProfile = "linkedin-company-posts"
)

type renderedExtractContentStrategy string

const (
	renderedExtractContentStrategyLegacyHTML     renderedExtractContentStrategy = "legacy-html"
	renderedExtractContentStrategySemanticDOM    renderedExtractContentStrategy = "semantic-dom"
	renderedExtractContentStrategyDiscussionTree renderedExtractContentStrategy = "discussion-tree"
)

var (
	arxivModernIdentifierPattern    = regexp.MustCompile(`^[0-9]{4}\.[0-9]{4,5}(v[1-9][0-9]*)?$`)
	arxivLegacyIdentifierPattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.-]*/[0-9]{7}(v[1-9][0-9]*)?$`)
	xStatusPathPattern              = regexp.MustCompile(`^/([A-Za-z0-9_]{1,15})/status/([0-9]+)$`)
	xProfilePathPattern             = regexp.MustCompile(`^/([A-Za-z0-9_]{1,15})/?$`)
	linkedInPostPathPattern         = regexp.MustCompile(`^/posts/[A-Za-z0-9_-]+-activity-([0-9]+)(?:-[A-Za-z0-9_-]+)?/?$`)
	linkedInCompanyPostsPathPattern = regexp.MustCompile(`^/company/([A-Za-z0-9-]+)/posts/?$`)
	redditPostPathPattern           = regexp.MustCompile(`^/r/([A-Za-z0-9_]{1,21})/comments/([A-Za-z0-9]+)(?:/[A-Za-z0-9_-]+)?(?:/[A-Za-z0-9]+)?/?$`)
	redditUserProfilePathPattern    = regexp.MustCompile(`^/user/([A-Za-z0-9_-]{1,40})/?$`)
	redditSubredditPathPattern      = regexp.MustCompile(`^/r/([A-Za-z0-9_]{1,21})(?:/(best|hot|new|top))?/?$`)
	linkedInLocaleHostPattern       = regexp.MustCompile(`^[a-z]{2,3}\.linkedin\.com$`)
)

type renderedExtractContentIdentity struct {
	Profile renderedExtractContentProfile
	Key     string
}

type renderedExtractContentRepresentations struct {
	HTML     string `json:"html,omitempty"`
	PDF      string `json:"pdf,omitempty"`
	Source   string `json:"source,omitempty"`
	Abstract string `json:"abstract,omitempty"`
}

type renderedExtractContentPlan struct {
	Mode            string                                `json:"mode"`
	Profile         renderedExtractContentProfile         `json:"profile"`
	Strategy        renderedExtractContentStrategy        `json:"strategy"`
	RequestedURL    string                                `json:"requested_url"`
	NavigationURL   string                                `json:"navigation_url"`
	GenericSelector string                                `json:"generic_root_selector"`
	Selector        string                                `json:"root_selector"`
	Representation  string                                `json:"representation"`
	Rewritten       bool                                  `json:"representation_rewritten"`
	DomainMatched   bool                                  `json:"domain_matched"`
	Representations renderedExtractContentRepresentations `json:"representations,omitempty"`
}

type renderedExtractContentCapture struct {
	Markdown        string     `json:"markdown"`
	RootSelector    string     `json:"root_selector"`
	ItemCount       int        `json:"item_count"`
	DiscussionCount int        `json:"discussion_count,omitempty"`
	Error           *evalError `json:"error,omitempty"`
}

type renderedExtractContentProvenance struct {
	Mode                    string                                `json:"mode"`
	Profile                 renderedExtractContentProfile         `json:"profile"`
	PlannedStrategy         renderedExtractContentStrategy        `json:"planned_strategy"`
	Strategy                renderedExtractContentStrategy        `json:"strategy"`
	PlannedRepresentation   string                                `json:"planned_representation"`
	Representation          string                                `json:"representation"`
	RepresentationRewritten bool                                  `json:"representation_rewritten"`
	DomainMatched           bool                                  `json:"domain_matched"`
	NativeAttempted         bool                                  `json:"native_attempted"`
	NativeSucceeded         bool                                  `json:"native_succeeded"`
	FallbackUsed            bool                                  `json:"fallback_used"`
	FallbackReason          string                                `json:"fallback_reason,omitempty"`
	RequestedURL            string                                `json:"requested_url"`
	NavigationURL           string                                `json:"navigation_url"`
	FinalURL                string                                `json:"final_url"`
	RootSelector            string                                `json:"root_selector"`
	ItemCount               int                                   `json:"item_count,omitempty"`
	DiscussionCount         int                                   `json:"discussion_count,omitempty"`
	DiscussionLimit         int                                   `json:"discussion_limit,omitempty"`
	DiscussionStatus        string                                `json:"discussion_status,omitempty"`
	DiscussionInteractions  int                                   `json:"discussion_interactions,omitempty"`
	Representations         renderedExtractContentRepresentations `json:"representations,omitempty"`
}

func planRenderedExtractContent(rawURL, selector, mode string) (renderedExtractContentPlan, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "generic" {
		return renderedExtractContentPlan{}, fmt.Errorf("content extractor must be auto or generic")
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		selector = "body"
	}
	rawURL = strings.TrimSpace(rawURL)
	plan := renderedExtractContentPlan{
		Mode:            mode,
		Profile:         renderedExtractContentProfileGeneric,
		Strategy:        renderedExtractContentStrategyLegacyHTML,
		RequestedURL:    rawURL,
		NavigationURL:   rawURL,
		GenericSelector: selector,
		Selector:        selector,
		Representation:  "rendered-html",
	}
	if mode == "generic" {
		return plan, nil
	}

	parsed, host, ok := parseRenderedExtractSourceURL(rawURL)
	if !ok {
		return plan, nil
	}
	if host == "arxiv.org" || host == "www.arxiv.org" {
		if identifier, ok := arxivPaperIdentifier(parsed.Path); ok {
			representations := renderedExtractContentRepresentations{
				HTML:     "https://arxiv.org/html/" + identifier,
				PDF:      "https://arxiv.org/pdf/" + identifier,
				Source:   "https://arxiv.org/src/" + identifier,
				Abstract: "https://arxiv.org/abs/" + identifier,
			}
			rootSelector := selector
			if rootSelector == "body" {
				rootSelector = "article.ltx_document"
			}
			return renderedExtractContentPlan{
				Mode:            mode,
				Profile:         renderedExtractContentProfileArxiv,
				Strategy:        renderedExtractContentStrategySemanticDOM,
				RequestedURL:    rawURL,
				NavigationURL:   representations.HTML,
				GenericSelector: selector,
				Selector:        rootSelector,
				Representation:  "html",
				Rewritten:       rawURL != representations.HTML,
				DomainMatched:   true,
				Representations: representations,
			}, nil
		}
	}
	if host == "news.ycombinator.com" && parsed.Path == "/item" && isDecimalIdentifier(parsed.Query().Get("id")) {
		plan.Profile = renderedExtractContentProfileHackerNews
		plan.Strategy = renderedExtractContentStrategyDiscussionTree
		plan.Representation = "discussion"
		plan.Selector = "table.fatitem"
		plan.DomainMatched = true
		return plan, nil
	}
	if identity, ok := renderedExtractSocialIdentity(parsed, host); ok {
		plan.Profile = identity.Profile
		plan.Strategy = renderedExtractContentStrategySemanticDOM
		plan.Representation = "post"
		plan.DomainMatched = true
		switch identity.Profile {
		case renderedExtractContentProfileX:
			plan.Selector = `article[data-testid="tweet"]`
		case renderedExtractContentProfileLinkedIn:
			plan.Selector = ".feed-shared-update-v2"
		case renderedExtractContentProfileReddit:
			plan.Selector = "shreddit-post"
		case renderedExtractContentProfileRedditSubreddit:
			plan.Selector = "shreddit-feed shreddit-post"
		case renderedExtractContentProfileXProfile:
			plan.Selector = `article[data-testid="tweet"]`
		case renderedExtractContentProfileRedditUser:
			plan.Selector = "main"
		case renderedExtractContentProfileLinkedInCompany:
			plan.Selector = "main"
		}
		return plan, nil
	}
	return plan, nil
}

func renderedExtractSocialIdentity(parsed *url.URL, host string) (renderedExtractContentIdentity, bool) {
	if parsed == nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawPath != "" {
		return renderedExtractContentIdentity{}, false
	}
	switch {
	case host == "x.com" || host == "www.x.com":
		matches := xStatusPathPattern.FindStringSubmatch(parsed.Path)
		if len(matches) == 3 && !strings.EqualFold(matches[1], "i") {
			return renderedExtractContentIdentity{Profile: renderedExtractContentProfileX, Key: matches[2]}, true
		}
		matches = xProfilePathPattern.FindStringSubmatch(parsed.Path)
		if len(matches) != 2 || strings.EqualFold(matches[1], "i") || isXReservedRoute(matches[1]) {
			return renderedExtractContentIdentity{}, false
		}
		return renderedExtractContentIdentity{Profile: renderedExtractContentProfileXProfile, Key: strings.ToLower(matches[1])}, true
	case host == "linkedin.com" || host == "www.linkedin.com" || linkedInLocaleHostPattern.MatchString(host):
		matches := linkedInPostPathPattern.FindStringSubmatch(parsed.Path)
		if len(matches) == 2 {
			return renderedExtractContentIdentity{Profile: renderedExtractContentProfileLinkedIn, Key: matches[1]}, true
		}
		matches = linkedInCompanyPostsPathPattern.FindStringSubmatch(parsed.Path)
		if len(matches) != 2 {
			return renderedExtractContentIdentity{}, false
		}
		return renderedExtractContentIdentity{Profile: renderedExtractContentProfileLinkedInCompany, Key: strings.ToLower(matches[1])}, true
	case host == "reddit.com" || host == "www.reddit.com":
		matches := redditPostPathPattern.FindStringSubmatch(parsed.Path)
		if len(matches) == 3 {
			return renderedExtractContentIdentity{Profile: renderedExtractContentProfileReddit, Key: strings.ToLower(matches[2])}, true
		}
		matches = redditUserProfilePathPattern.FindStringSubmatch(parsed.Path)
		if len(matches) == 2 {
			return renderedExtractContentIdentity{Profile: renderedExtractContentProfileRedditUser, Key: strings.ToLower(matches[1])}, true
		}
		matches = redditSubredditPathPattern.FindStringSubmatch(parsed.Path)
		if len(matches) != 3 {
			return renderedExtractContentIdentity{}, false
		}
		sort := strings.ToLower(matches[2])
		if sort == "" {
			sort = "hot"
		}
		query := parsed.Query()
		if sort == "top" {
			time := strings.ToLower(query.Get("t"))
			if time != "" && time != "hour" && time != "day" && time != "week" && time != "month" && time != "year" && time != "all" {
				return renderedExtractContentIdentity{}, false
			}
		} else if query.Get("t") != "" {
			return renderedExtractContentIdentity{}, false
		}
		return renderedExtractContentIdentity{Profile: renderedExtractContentProfileRedditSubreddit, Key: strings.ToLower(matches[1]) + ":" + sort}, true
	default:
		return renderedExtractContentIdentity{}, false
	}
}

// renderedExtractCaptureSelector keeps readiness, the generic artifact capture,
// and semantic extraction anchored to the same source root. Generic mode keeps
// the caller's selector unchanged.
func renderedExtractCaptureSelector(plan renderedExtractContentPlan) string {
	if plan.Mode == "auto" && plan.Strategy != renderedExtractContentStrategyLegacyHTML && strings.TrimSpace(plan.Selector) != "" {
		return plan.Selector
	}
	return plan.GenericSelector
}

func isXReservedRoute(value string) bool {
	switch strings.ToLower(value) {
	case "home", "explore", "notifications", "messages", "search", "settings", "i", "compose", "lists", "communities", "premium":
		return true
	default:
		return false
	}
}

func parseRenderedExtractSourceURL(rawURL string) (*url.URL, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, "", false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return nil, "", false
	}
	return parsed, host, true
}

func arxivPaperIdentifier(path string) (string, bool) {
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	route, identifier, ok := strings.Cut(path, "/")
	if !ok {
		return "", false
	}
	switch route {
	case "abs", "html", "pdf", "src", "e-print":
	default:
		return "", false
	}
	identifier = strings.Trim(identifier, "/")
	if strings.HasSuffix(strings.ToLower(identifier), ".pdf") {
		identifier = identifier[:len(identifier)-len(".pdf")]
	}
	if !arxivModernIdentifierPattern.MatchString(identifier) && !arxivLegacyIdentifierPattern.MatchString(identifier) {
		return "", false
	}
	return identifier, true
}

func isDecimalIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func renderedExtractContentNativeEligible(plan renderedExtractContentPlan, finalURL string) bool {
	parsed, host, ok := parseRenderedExtractSourceURL(finalURL)
	if !ok {
		return false
	}
	switch plan.Profile {
	case renderedExtractContentProfileArxiv:
		if host != "arxiv.org" && host != "www.arxiv.org" {
			return false
		}
		route, finalIdentifier, ok := arxivPaperRoute(parsed.Path)
		if !ok || route != "html" {
			return false
		}
		planned, plannedHost, plannedURLValid := parseRenderedExtractSourceURL(plan.Representations.HTML)
		if !plannedURLValid || (plannedHost != "arxiv.org" && plannedHost != "www.arxiv.org") {
			return false
		}
		_, plannedIdentifier, plannedRouteValid := arxivPaperRoute(planned.Path)
		return plannedRouteValid && finalIdentifier == plannedIdentifier
	case renderedExtractContentProfileHackerNews:
		if host != "news.ycombinator.com" || parsed.Path != "/item" {
			return false
		}
		finalID := parsed.Query().Get("id")
		planned, plannedHost, plannedURLValid := parseRenderedExtractSourceURL(plan.RequestedURL)
		if !plannedURLValid || plannedHost != "news.ycombinator.com" {
			return false
		}
		plannedID := planned.Query().Get("id")
		return isDecimalIdentifier(finalID) && finalID == plannedID
	case renderedExtractContentProfileX, renderedExtractContentProfileLinkedIn, renderedExtractContentProfileReddit,
		renderedExtractContentProfileXProfile, renderedExtractContentProfileRedditUser, renderedExtractContentProfileLinkedInCompany:
		finalIdentity, finalOK := renderedExtractSocialIdentity(parsed, host)
		planned, plannedHost, plannedURLValid := parseRenderedExtractSourceURL(plan.RequestedURL)
		if !finalOK || !plannedURLValid {
			return false
		}
		plannedIdentity, plannedOK := renderedExtractSocialIdentity(planned, plannedHost)
		return plannedOK && finalIdentity.Profile == plan.Profile && plannedIdentity.Profile == plan.Profile && finalIdentity.Key == plannedIdentity.Key
	default:
		return false
	}
}

func arxivPaperRoute(path string) (string, string, bool) {
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	route, identifier, ok := strings.Cut(path, "/")
	if !ok {
		return "", "", false
	}
	identifier, ok = arxivPaperIdentifier("/" + route + "/" + identifier)
	return route, identifier, ok
}

func collectRenderedExtractContent(ctx context.Context, session *cdp.PageSession, plan renderedExtractContentPlan) (renderedExtractContentCapture, error) {
	var capture renderedExtractContentCapture
	if err := evaluateJSONValue(ctx, session, renderedExtractContentExpression(plan), "rendered content", &capture); err != nil {
		return renderedExtractContentCapture{}, err
	}
	return capture, nil
}

func renderedExtractContentExpression(plan renderedExtractContentPlan) string {
	selectorJSON, _ := json.Marshal(plan.Selector)
	profileJSON, _ := json.Marshal(plan.Profile)
	template := `(() => {
  const marker = "__cdp_cli_rendered_content__";
  const selector = __SELECTOR__;
  const profile = __PROFILE__;
  const error = (name, message) => ({
    markdown: "",
    root_selector: selector,
    item_count: 0,
    error: { name, message }
  });
  const normalizeSpace = (value) => String(value || "").replace(/\u00a0/g, " ").replace(/[ \t\r\n]+/g, " ").trim();
  const escapeText = (value) => String(value || "").replace(/([\\\x60*_[\]])/g, "\\$1");
  const safeURL = (value) => {
    try {
      const parsed = new URL(String(value || ""), location.href);
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return "";
      return parsed.href.replace(/\)/g, "%29");
    } catch (_) {
      return "";
    }
  };
  const skipped = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "TEMPLATE", "SVG"]);
  const blockTags = new Set([
    "ADDRESS", "ARTICLE", "ASIDE", "BLOCKQUOTE", "DD", "DIV", "DL", "DT",
    "FIGCAPTION", "FIGURE", "FOOTER", "FORM", "H1", "H2", "H3", "H4",
    "H5", "H6", "HEADER", "HR", "LI", "MAIN", "NAV", "OL", "P", "PRE",
    "SECTION", "TABLE", "UL"
  ]);
  const textValue = (node) => String(node && node.nodeValue || "").replace(/[ \t\r\n]+/g, " ");
  const mathValue = (node) => {
    const tex = normalizeSpace(node.getAttribute("alttext") || node.getAttribute("data-tex") || "");
    if (!tex) return "";
    const display = node.getAttribute("display") === "block" || Boolean(node.closest(".ltx_equation, .ltx_equationgroup"));
    return display ? "\n\n$$" + tex + "$$\n\n" : "$" + tex + "$";
  };
  const inline = (node) => {
    if (!node) return "";
    if (node.nodeType === Node.TEXT_NODE) return textValue(node);
    if (node.nodeType !== Node.ELEMENT_NODE || skipped.has(node.tagName)) return "";
    const tag = node.tagName;
    const localName = String(node.localName || "").toLowerCase();
    if (tag === "BR") return "\n";
    if (localName === "math") return mathValue(node);
    const children = () => Array.from(node.childNodes).map(inline).join("");
    if (tag === "A") {
      const label = normalizeSpace(children()) || normalizeSpace(node.textContent);
      const href = safeURL(node.href || node.getAttribute("href"));
      return href && label ? "[" + label + "](" + href + ")" : label;
    }
    if (tag === "IMG") {
      const alt = normalizeSpace(node.getAttribute("alt"));
      const src = safeURL(node.currentSrc || node.src || node.getAttribute("src"));
      return src ? "![" + escapeText(alt) + "](" + src + ")" : escapeText(alt);
    }
    if (tag === "CODE") {
      const value = String(node.textContent || "").trim();
      const fence = value.includes("\x60") ? "\x60\x60" : "\x60";
      return value ? fence + value + fence : "";
    }
    if (tag === "STRONG" || tag === "B") {
      const value = normalizeSpace(children());
      return value ? "**" + value + "**" : "";
    }
    if (tag === "EM" || tag === "I") {
      const value = normalizeSpace(children());
      return value ? "*" + value + "*" : "";
    }
    if (tag === "DEL" || tag === "S") {
      const value = normalizeSpace(children());
      return value ? "~~" + value + "~~" : "";
    }
    if (tag === "SUP") {
      const value = normalizeSpace(children());
      return value ? "^" + value + "^" : "";
    }
    if (tag === "SUB") {
      const value = normalizeSpace(children());
      return value ? "~" + value + "~" : "";
    }
    return children();
  };
  const indentLines = (value, prefix) => String(value || "").split("\n").map((line) => line ? prefix + line : "").join("\n");
  const renderTable = (node) => {
    const rows = Array.from(node.querySelectorAll("tr")).map((row) =>
      Array.from(row.querySelectorAll(":scope > th, :scope > td")).map((cell) => normalizeSpace(inline(cell)))
    ).filter((row) => row.length > 0);
    if (!rows.length) return "";
    const width = Math.max(...rows.map((row) => row.length));
    const padded = rows.map((row) => Array.from({length: width}, (_, index) => (row[index] || "").replace(/\|/g, "\\|")));
    const header = padded[0];
    const separator = header.map(() => "---");
    return "\n\n| " + header.join(" | ") + " |\n| " + separator.join(" | ") + " |\n" +
      padded.slice(1).map((row) => "| " + row.join(" | ") + " |").join("\n") + "\n\n";
  };
	  const renderList = (node, depth) => {
	    const ordered = node.tagName === "OL";
	    const items = Array.from(node.children).filter((child) => child.tagName === "LI");
	    return items.map((item, index) => {
	      const nested = Array.from(item.children).filter((child) => child.tagName === "UL" || child.tagName === "OL");
	      const bodyNodes = Array.from(item.childNodes).filter((child) => !(child.nodeType === Node.ELEMENT_NODE &&
	        (child.tagName === "UL" || child.tagName === "OL" || child.matches(".ltx_tag_item"))));
	      const body = normalizeSpace(bodyNodes.map((child) => block(child, depth + 1)).join(" "));
	      const prefix = "    ".repeat(depth) + (ordered ? String(index + 1) + ". " : "- ");
	      const children = nested.map((child) => renderList(child, depth + 1)).join("");
      return prefix + body + "\n" + children;
    }).join("");
  };
  const block = (node, depth = 0) => {
    if (!node) return "";
    if (node.nodeType === Node.TEXT_NODE) return textValue(node);
    if (node.nodeType !== Node.ELEMENT_NODE || skipped.has(node.tagName)) return "";
    const tag = node.tagName;
    if (tag === "NAV" || tag === "FORM") return "";
    if (/^H[1-6]$/.test(tag)) {
      const level = Number(tag.slice(1));
      const value = normalizeSpace(inline(node));
      return value ? "\n\n" + "#".repeat(level) + " " + value + "\n\n" : "";
    }
    if (tag === "P") {
      const value = normalizeSpace(inline(node));
      return value ? "\n\n" + value + "\n\n" : "";
    }
    if (tag === "PRE") {
      const value = String(node.textContent || "").replace(/\s+$/, "");
      return value ? "\n\n\x60\x60\x60\n" + value + "\n\x60\x60\x60\n\n" : "";
    }
    if (tag === "BLOCKQUOTE") {
      const value = finalize(Array.from(node.childNodes).map((child) => block(child, depth)).join(""));
      return value ? "\n\n" + indentLines(value, "> ") + "\n\n" : "";
    }
    if (tag === "UL" || tag === "OL") return "\n" + renderList(node, depth) + "\n";
    if (tag === "TABLE") return renderTable(node);
    if (tag === "HR") return "\n\n---\n\n";
    if (tag === "FIGURE") {
      const image = node.querySelector("img");
      const caption = node.querySelector("figcaption");
      return "\n\n" + (image ? inline(image) : "") + (caption ? "\n\n*" + normalizeSpace(inline(caption)) + "*" : "") + "\n\n";
    }
    if (tag === "DT") {
      const value = normalizeSpace(inline(node));
      return value ? "\n\n**" + value + "**\n\n" : "";
    }
    if (tag === "DD") {
      const value = finalize(Array.from(node.childNodes).map((child) => block(child, depth)).join(""));
      return value ? "\n\n" + indentLines(value, ": ") + "\n\n" : "";
    }
    const hasBlockChild = Array.from(node.children || []).some((child) => blockTags.has(child.tagName));
    if (!hasBlockChild) return inline(node);
    return Array.from(node.childNodes).map((child) => block(child, depth)).join("");
  };
  function finalize(value) {
    return String(value || "")
      .replace(/\u00a0/g, " ")
      .replace(/[ \t]+\n/g, "\n")
      .replace(/^[ \t]+$/gm, "")
      .replace(/\n{3,}/g, "\n\n")
      .trim();
  }
	  const isVisible = (node) => {
	    if (!node || !node.isConnected || node.closest('[aria-hidden="true"], [inert]')) return false;
	    const style = window.getComputedStyle(node);
	    return style.display !== "none" && style.visibility !== "hidden" && style.visibility !== "collapse";
	  };
	  const visibleText = (node) => isVisible(node) ? normalizeSpace(node.innerText || node.textContent) : "";
	  if (profile === "arxiv") {
	    let root;
	    try {
	      root = document.querySelector(selector);
	    } catch (caught) {
	      return error(caught && caught.name || "SyntaxError", caught && caught.message || "invalid root selector");
	    }
	    if (!root) return error("NotFoundError", "arXiv semantic root matched no elements");
	    const citationTitleNode = document.querySelector('meta[name="citation_title"]');
	    const documentTitleNode = root.querySelector("h1.ltx_title_document");
	    const visualTitleNode = root.querySelector(":scope > .ltx_logical-block:first-child .ltx_align_center .ltx_font_bold");
	    const citationTitle = normalizeSpace(
	      citationTitleNode && citationTitleNode.getAttribute("content") ||
	      documentTitleNode && documentTitleNode.textContent ||
	      visualTitleNode && visualTitleNode.textContent
	    );
	    const titleBlock = citationTitle ? Array.from(root.children).find((child) => {
	      const value = normalizeSpace(child.textContent);
	      return value === citationTitle || value.startsWith(citationTitle + " ");
	    }) : null;
	    const bodyMarkdown = finalize(Array.from(root.childNodes)
	      .filter((child) => child !== titleBlock)
	      .map((child) => block(child))
	      .join(""));
	    let frontMatter = titleBlock ? finalize(block(titleBlock)) : "";
	    if (frontMatter === citationTitle) {
	      frontMatter = "";
	    } else if (citationTitle && frontMatter.startsWith(citationTitle + " ")) {
	      frontMatter = frontMatter.slice(citationTitle.length).trim();
	    }
	    const markdown = finalize([
	      citationTitle ? "# " + escapeText(citationTitle) : "",
	      frontMatter,
	      bodyMarkdown
	    ].filter(Boolean).join("\n\n"));
	    if (!markdown) return error("EmptyContentError", "arXiv semantic root produced empty Markdown");
	    return {
      markdown,
      root_selector: selector,
      item_count: root.querySelectorAll("section.ltx_section, section.ltx_appendix, section.ltx_bibliography").length
    };
  }
  if (profile === "hacker-news") {
    const pageText = normalizeSpace(document.body && document.body.innerText);
    if (pageText === "Sorry.") return error("SourceLimitedError", "Hacker News returned its request-limit page");
    const titleLink = document.querySelector("table.fatitem .titleline > a, .titleline > a");
    const storyRow = document.querySelector("table.fatitem tr.athing");
    const commentRows = Array.from(document.querySelectorAll("table.comment-tree tr.athing.comtr")).slice(0, 500);
    if (!titleLink || !storyRow) return error("NotFoundError", "Hacker News story header was not found");
    const title = normalizeSpace(titleLink.textContent);
    const sourceURL = safeURL(titleLink.href);
    const discussionURL = safeURL(location.href);
    const subline = document.querySelector("table.fatitem .subline, .subline");
    const score = normalizeSpace(subline && subline.querySelector(".score") && subline.querySelector(".score").textContent);
    const storyAuthor = normalizeSpace(subline && subline.querySelector(".hnuser") && subline.querySelector(".hnuser").textContent);
    const storyAgeNode = subline && subline.querySelector(".age a");
    const storyAge = normalizeSpace(storyAgeNode && storyAgeNode.textContent);
    const storyAgeURL = safeURL(storyAgeNode && storyAgeNode.href);
    const metadata = [
      score,
      storyAuthor ? "by **" + escapeText(storyAuthor) + "**" : "",
      storyAge ? (storyAgeURL ? "[" + escapeText(storyAge) + "](" + storyAgeURL + ")" : escapeText(storyAge)) : ""
    ].filter(Boolean).join(" · ");
    const lines = ["# " + escapeText(title), ""];
    const sourceLinks = [];
    if (sourceURL) sourceLinks.push("[Source](" + sourceURL + ")");
    if (discussionURL) sourceLinks.push("[HN discussion](" + discussionURL + ")");
    if (sourceLinks.length) lines.push(sourceLinks.join(" · "), "");
    if (metadata) lines.push(metadata, "");
    const storyText = document.querySelector("table.fatitem .toptext, .toptext");
    const storyMarkdown = storyText ? finalize(block(storyText)) : "";
    if (storyMarkdown) lines.push("## Story text", "", storyMarkdown, "");
    lines.push("## Comments (" + String(commentRows.length) + ")", "");
    for (const row of commentRows) {
      const depth = Math.max(0, Number.parseInt(row.querySelector("td.ind") && row.querySelector("td.ind").getAttribute("indent") || "0", 10) || 0);
      const author = normalizeSpace(row.querySelector(".hnuser") && row.querySelector(".hnuser").textContent) || "[deleted]";
      const ageNode = row.querySelector(".age a");
      const age = normalizeSpace(ageNode && ageNode.textContent) || "comment";
      const permalink = safeURL(ageNode && ageNode.href) || (row.id ? "https://news.ycombinator.com/item?id=" + encodeURIComponent(row.id) : "");
      const prefix = "    ".repeat(depth);
      const authorLabel = author === "[deleted]" ? author : "**" + escapeText(author) + "**";
      const ageLabel = permalink ? "[" + escapeText(age) + "](" + permalink + ")" : escapeText(age);
      lines.push(prefix + "- " + authorLabel + " · " + ageLabel);
      const bodyNode = row.querySelector(".commtext");
      const body = bodyNode ? finalize(block(bodyNode)) : "[deleted or unavailable]";
      if (body) {
        lines.push("");
        const bodyPrefix = "    ".repeat(depth + 1);
        lines.push(indentLines(body, bodyPrefix));
      }
      lines.push("");
    }
    return {
      markdown: finalize(lines.join("\n")),
      root_selector: "table.fatitem, table.comment-tree",
      item_count: commentRows.length,
      discussion_count: commentRows.length
    };
  }
	  if (profile === "x") {
	    const clearCache = () => { try { delete window.__cdp_cli_rendered_x_discussion__; } catch (_) {} };
	    const match = location.pathname.match(/^\/[^/]+\/status\/([0-9]+)$/);
	    const statusID = match && match[1];
	    const conversation = document.querySelector('[aria-label="Timeline: Conversation"]');
	    const allTweets = Array.from(document.querySelectorAll('article[data-testid="tweet"]'));
	    const root = statusID && allTweets.find((tweet) => Array.from(tweet.querySelectorAll("a[href]")).some((anchor) => {
	      try {
	        return new URL(anchor.href, location.href).pathname.endsWith("/status/" + statusID);
	      } catch (_) {
	        return false;
	      }
	    }));
	    const cached = window.__cdp_cli_rendered_x_discussion__;
	    const cachedRoot = cached && cached.path === location.pathname && cached.requested_id === statusID && cached.root && cached.root.id === statusID ? cached.root : null;
	    if ((!root || !isVisible(root)) && !cachedRoot) { clearCache(); return error("NotFoundError", "X status root was not found"); }
	    const text = cachedRoot ? cachedRoot.text : visibleText(root.querySelector('[data-testid="tweetText"]'));
	    if (!text) { clearCache(); return error("EmptyContentError", "X status root has no visible post text"); }
	    const author = cachedRoot ? cachedRoot.author : visibleText(root.querySelector('[data-testid="User-Name"]'));
	    const timestamp = cachedRoot ? normalizeSpace(cachedRoot.timestamp) : normalizeSpace(root.querySelector("time") && root.querySelector("time").getAttribute("datetime"));
	    const metadata = [author, timestamp].filter(Boolean).map(escapeText).join(" · ");
	    const replies = cached && cached.replies ? Object.values(cached.replies).slice(0, 500) : (conversation ? Array.from(conversation.querySelectorAll('article[data-testid="tweet"]')) : [])
	      .filter((tweet) => tweet !== root && isVisible(tweet))
	      .slice(0, 500);
	    const lines = ["# X post", metadata, text].filter(Boolean);
	    if (replies.length) lines.push("## Replies (" + String(replies.length) + ")");
	    for (const reply of replies) {
	      const depth = Math.min(8, Math.max(0, Number.parseInt(reply.depth || reply.getAttribute && reply.getAttribute("data-thread-depth") || "0", 10) || 0));
	      const replyAuthor = reply.author || visibleText(reply.querySelector('[data-testid="User-Name"]'));
	      const replyText = reply.text || visibleText(reply.querySelector('[data-testid="tweetText"]'));
	      if (replyText) lines.push("    ".repeat(depth) + "- " + [replyAuthor, replyText].filter(Boolean).map(escapeText).join(": "));
	    }
	    const result = {
	      markdown: finalize(lines.join("\n\n")).slice(0, 102400),
	      root_selector: 'article[data-testid="tweet"]',
	      item_count: 1 + replies.length,
	      discussion_count: replies.length
	    };
	    clearCache();
	    return result;
	  }
	  if (profile === "x-profile") {
	    const match = location.pathname.match(/^\/([A-Za-z0-9_]{1,15})\/?$/);
	    const handle = match && match[1].toLowerCase();
	    if (!handle) return error("NotFoundError", "X profile handle was not found");
	    const rows = [];
	    const seen = new Set();
	    for (const article of Array.from(document.querySelectorAll('article[data-testid="tweet"]'))) {
	      if (!isVisible(article) || article.querySelector("article")) continue;
	      const own = (selector) => Array.from(article.querySelectorAll(selector)).filter((node) => node.closest("article") === article);
	      const time = own("time")[0];
	      const permalink = time && time.closest("a[href]");
	      if (!permalink) continue;
	      let parsed;
	      try { parsed = new URL(permalink.href, location.href); } catch (_) { continue; }
	      const route = parsed.pathname.match(/^\/([A-Za-z0-9_]{1,15})\/status\/([0-9]+)$/);
	      if (!route || route[1].toLowerCase() !== handle || seen.has(route[2])) continue;
	      const authorLink = own('[data-testid="User-Name"] a[href]').find((node) => {
	        try { return new URL(node.href, location.href).pathname.toLowerCase() === "/" + handle; } catch (_) { return false; }
	      });
	      const more = own('[data-testid="tweet-text-show-more-link"]')[0];
	      if (authorLink && more) more.click();
	      const body = visibleText(own('[data-testid="tweetText"]')[0]);
	      if (!authorLink || !body) continue;
	      seen.add(route[2]);
	      rows.push({id: route[2], url: parsed.href, text: body});
	      if (rows.length >= 500) break;
	    }
	    if (!rows.length) return error("NotFoundError", "X profile contained no identity-confirmed post cards");
	    const lines = ["# X profile @" + escapeText(handle), ""];
	    for (const row of rows) lines.push("## [Post](" + safeURL(row.url) + ")", "", row.text, "");
	    return {markdown: finalize(lines.join("\n")).slice(0, 102400), root_selector: 'article[data-testid="tweet"]', item_count: rows.length};
	  }
	  if (profile === "reddit-subreddit") {
	    const match = location.pathname.match(/^\/r\/([A-Za-z0-9_]{1,21})(?:\/(best|hot|new|top))?\/?$/i);
	    const subreddit = match && match[1].toLowerCase();
	    if (!subreddit) return error("NotFoundError", "Reddit subreddit was not found");
	    const rows = [];
	    const seen = new Set();
	    for (const card of Array.from(document.querySelectorAll("shreddit-feed shreddit-post"))) {
	      if (!isVisible(card)) continue;
	      const id = card.getAttribute("id") || "";
	      const permalink = card.getAttribute("permalink") || "";
	      const cardSubreddit = (card.getAttribute("subreddit-name") || "").toLowerCase();
	      if (!/^t3_[A-Za-z0-9]+$/.test(id) || cardSubreddit !== subreddit || !new RegExp("^/r/" + subreddit + "/comments/[A-Za-z0-9]+/", "i").test(permalink) || seen.has(id)) continue;
	      const title = normalizeSpace(card.getAttribute("post-title") || "");
	      if (!title) continue;
	      seen.add(id); rows.push({id, permalink, title, author: normalizeSpace(card.getAttribute("author") || ""), score: normalizeSpace(card.getAttribute("score") || ""), comments: normalizeSpace(card.getAttribute("comment-count") || "")});
	      if (rows.length >= 500) break;
	    }
	    if (!rows.length) return error("NotFoundError", "Reddit subreddit contained no identity-confirmed thread cards");
	    const lines = ["# Reddit r/" + escapeText(subreddit), ""];
	    for (const row of rows) lines.push("## [" + escapeText(row.title) + "](https://www.reddit.com" + row.permalink + ")", "", [row.author && "u/" + escapeText(row.author), row.score && row.score + " points", row.comments && row.comments + " comments"].filter(Boolean).join(" · "), "");
	    return {markdown: finalize(lines.join("\n")).slice(0, 102400), root_selector: "shreddit-feed shreddit-post", item_count: rows.length};
	  }
	  if (profile === "reddit-user-profile") {
	    const match = location.pathname.match(/^\/user\/([A-Za-z0-9_-]{1,40})\/?$/);
	    const user = match && match[1].toLowerCase();
	    if (!user) return error("NotFoundError", "Reddit profile user was not found");
	    const rows = [];
	    const seen = new Set();
	    for (const card of Array.from(document.querySelectorAll("shreddit-feed shreddit-profile-comment"))) {
	      if (!isVisible(card)) continue;
	      const author = Array.from(card.querySelectorAll('a[href^="/user/"]')).some((node) => {
	        try { return new URL(node.href, location.href).pathname.toLowerCase() === "/user/" + user + "/"; } catch (_) { return false; }
	      });
	      const threadLink = Array.from(card.querySelectorAll('a[aria-label]')).find((node) => {
	        return (node.getAttribute("aria-label") || "").toLowerCase().startsWith("thread for " + user + "'s comment");
	      });
	      const permalink = Array.from(card.querySelectorAll("a[href]")).find((node) => {
	        try { return /^\/r\/[^/]+\/comments\/([A-Za-z0-9]+)\/comment\/([A-Za-z0-9]+)\/?$/.test(new URL(node.href, location.href).pathname); } catch (_) { return false; }
	      });
	      if (!author || !threadLink || !permalink) continue;
	      const url = safeURL(permalink.href);
	      if (!url || seen.has(url)) continue;
	      const body = visibleText(card);
	      if (!body) continue;
	      seen.add(url); rows.push({url, body});
	      if (rows.length >= 500) break;
	    }
	    if (!rows.length) return error("NotFoundError", "Reddit profile contained no identity-confirmed comment rows");
	    const lines = ["# Reddit profile u/" + escapeText(user), ""];
	    for (const row of rows) lines.push("## [Comment](" + row.url + ")", "", row.body, "");
	    return {markdown: finalize(lines.join("\n")).slice(0, 102400), root_selector: "shreddit-feed shreddit-profile-comment", item_count: rows.length};
	  }
	  if (profile === "linkedin-company-posts") {
	    const match = location.pathname.match(/^\/company\/([A-Za-z0-9-]+)\/posts\/?$/);
	    const company = match && match[1].toLowerCase();
	    if (!company) return error("NotFoundError", "LinkedIn company slug was not found");
	    const rows = [];
	    const seen = new Set();
	    for (const card of Array.from(document.querySelectorAll('[role="article"][data-urn^="urn:li:activity:"]'))) {
	      if (!isVisible(card)) continue;
	      const urn = card.getAttribute("data-urn") || "";
	      const activity = urn.match(/^urn:li:activity:([0-9]+)$/);
	      if (!activity || seen.has(activity[1])) continue;
	      const actor = card.querySelector(".update-components-actor__meta");
	      const author = actor && Array.from(actor.querySelectorAll("a[href]")).some((node) => {
	        try {
	          const path = new URL(node.href, location.href).pathname.toLowerCase();
	          return path === "/company/" + company + "/" || path === "/company/" + company + "/posts";
	        } catch (_) { return false; }
	      });
	      const more = Array.from(card.querySelectorAll('button,[role="button"]')).find((node) => {
	        return /^…?more$/i.test(normalizeSpace(node.getAttribute("aria-label") || node.innerText || node.textContent));
	      });
	      if (author && more) more.click();
	      const body = visibleText(card.querySelector(".update-components-text, .feed-shared-update-v2__description-wrapper"));
	      if (!author || !body) continue;
	      seen.add(activity[1]); rows.push({activity: activity[1], body});
	      if (rows.length >= 500) break;
	    }
	    if (!rows.length) return error("NotFoundError", "LinkedIn company feed contained no identity-confirmed post cards");
	    const lines = ["# LinkedIn company " + escapeText(company), ""];
	    for (const row of rows) lines.push("## Activity " + row.activity, "", row.body, "");
	    return {markdown: finalize(lines.join("\n")).slice(0, 102400), root_selector: '[role="article"][data-urn^="urn:li:activity:"]', item_count: rows.length};
	  }
	  if (profile === "linkedin") {
	    const match = location.pathname.match(/-activity-([0-9]+)(?:-[A-Za-z0-9_-]+)?\/?$/);
	    const activityID = match && match[1];
	    const urn = activityID ? "urn:li:activity:" + activityID : "";
	    const activityNode = urn && Array.from(document.querySelectorAll("[data-urn]")).find((node) => node.getAttribute("data-urn") === urn);
	    const root = activityNode && (activityNode.matches(".feed-shared-update-v2") ? activityNode : activityNode.querySelector(".feed-shared-update-v2"));
	    if (!root || !isVisible(root)) return error("NotFoundError", "LinkedIn activity root was not found");
	    const text = visibleText(root.querySelector(".update-components-text, .feed-shared-update-v2__description-wrapper") || root);
	    if (!text) return error("EmptyContentError", "LinkedIn activity root has no visible post text");
	    const comments = Array.from(document.querySelectorAll('article[data-id^="urn:li:comment:"]')).filter(isVisible).slice(0, 500);
	    const lines = ["# LinkedIn post", text];
	    if (comments.length) lines.push("## Comments (" + String(comments.length) + ")");
	    for (const comment of comments) {
	      const depth = Math.min(8, Math.max(0, Number.parseInt(comment.getAttribute("data-depth") || "0", 10) || 0));
	      const body = visibleText(comment);
	      if (body) lines.push("    ".repeat(depth) + "- " + body);
	    }
	    return {
	      markdown: finalize(lines.join("\n\n")).slice(0, 102400),
	      root_selector: ".feed-shared-update-v2",
	      item_count: 1 + comments.length,
	      discussion_count: comments.length
	    };
	  }
	  if (profile === "reddit") {
	    const match = location.pathname.match(/^\/r\/[^/]+\/comments\/([A-Za-z0-9]+)/i);
	    const postID = match && match[1];
	    const root = postID && document.getElementById("t3_" + postID);
	    if (!root || root.localName !== "shreddit-post" || !isVisible(root)) return error("NotFoundError", "Reddit submission root was not found");
	    const postText = visibleText(root);
	    if (!postText) return error("EmptyContentError", "Reddit submission root has no visible content");
	    const comments = Array.from(document.querySelectorAll("shreddit-comment"))
	      .filter(isVisible)
	      .slice(0, 500);
	    const lines = ["# Reddit post", "", postText];
	    if (comments.length) lines.push("", "## Rendered comments (" + String(comments.length) + ")");
	    for (const comment of comments) {
	      const depth = Math.min(8, Math.max(0, Number.parseInt(comment.getAttribute("depth") || "0", 10) || 0));
	      const body = visibleText(comment);
	      if (body) lines.push("", "    ".repeat(depth) + "- " + body);
	    }
	    const markdown = finalize(lines.join("\n")).slice(0, 102400);
	    return {
	      markdown,
	      root_selector: "shreddit-post",
	      item_count: 1 + comments.length,
	      discussion_count: comments.length
	    };
	  }
  return error("UnsupportedProfileError", "native extraction profile is not supported");
})()`
	expression := strings.ReplaceAll(template, "__SELECTOR__", string(selectorJSON))
	return strings.ReplaceAll(expression, "__PROFILE__", string(profileJSON))
}

func newRenderedExtractContentProvenance(plan renderedExtractContentPlan, finalURL string) renderedExtractContentProvenance {
	return renderedExtractContentProvenance{
		Mode:                    plan.Mode,
		Profile:                 plan.Profile,
		PlannedStrategy:         plan.Strategy,
		Strategy:                plan.Strategy,
		PlannedRepresentation:   plan.Representation,
		Representation:          plan.Representation,
		RepresentationRewritten: plan.Rewritten,
		DomainMatched:           plan.DomainMatched,
		RequestedURL:            plan.RequestedURL,
		NavigationURL:           plan.NavigationURL,
		FinalURL:                finalURL,
		RootSelector:            plan.GenericSelector,
		Representations:         plan.Representations,
	}
}

func applyRenderedExtractGenericFallback(provenance *renderedExtractContentProvenance, plan renderedExtractContentPlan, reason string) {
	provenance.Strategy = renderedExtractContentStrategyLegacyHTML
	provenance.Representation = "rendered-html"
	provenance.RootSelector = plan.GenericSelector
	provenance.FallbackUsed = true
	provenance.FallbackReason = reason
}

func prependArxivRepresentationLinks(markdown string, representations renderedExtractContentRepresentations) string {
	links := make([]string, 0, 4)
	for _, item := range []struct {
		label string
		url   string
	}{
		{label: "HTML", url: representations.HTML},
		{label: "PDF", url: representations.PDF},
		{label: "TeX source", url: representations.Source},
		{label: "Abstract", url: representations.Abstract},
	} {
		if strings.TrimSpace(item.url) != "" {
			links = append(links, "["+item.label+"]("+item.url+")")
		}
	}
	if len(links) == 0 {
		return markdown
	}
	return "> arXiv representations: " + strings.Join(links, " · ") + "\n\n" + strings.TrimSpace(markdown)
}
