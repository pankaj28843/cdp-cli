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
	renderedExtractContentProfileGeneric    renderedExtractContentProfile = "generic"
	renderedExtractContentProfileArxiv      renderedExtractContentProfile = "arxiv"
	renderedExtractContentProfileHackerNews renderedExtractContentProfile = "hacker-news"
)

type renderedExtractContentStrategy string

const (
	renderedExtractContentStrategyLegacyHTML     renderedExtractContentStrategy = "legacy-html"
	renderedExtractContentStrategySemanticDOM    renderedExtractContentStrategy = "semantic-dom"
	renderedExtractContentStrategyDiscussionTree renderedExtractContentStrategy = "discussion-tree"
)

var (
	arxivModernIdentifierPattern = regexp.MustCompile(`^[0-9]{4}\.[0-9]{4,5}(v[1-9][0-9]*)?$`)
	arxivLegacyIdentifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.-]*/[0-9]{7}(v[1-9][0-9]*)?$`)
)

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
	Markdown     string     `json:"markdown"`
	RootSelector string     `json:"root_selector"`
	ItemCount    int        `json:"item_count"`
	Error        *evalError `json:"error,omitempty"`
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
		plan.DomainMatched = true
		return plan, nil
	}
	return plan, nil
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
    const commentRows = Array.from(document.querySelectorAll("table.comment-tree tr.athing.comtr"));
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
      item_count: commentRows.length
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
