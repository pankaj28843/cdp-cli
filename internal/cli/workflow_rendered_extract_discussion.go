package cli

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const renderedExtractDiscussionLimit = 500

// renderedExtractDiscussionExpansion is deliberately separate from semantic
// rendering: it owns only source-scoped interaction and its partial result.
type renderedExtractDiscussionExpansion struct {
	Status       string `json:"status"`
	Interactions int    `json:"interactions"`
}

func renderedExtractContentHasExpandableDiscussion(plan renderedExtractContentPlan) bool {
	switch plan.Profile {
	case renderedExtractContentProfileX, renderedExtractContentProfileLinkedIn, renderedExtractContentProfileReddit:
		return true
	default:
		return false
	}
}

func expandRenderedExtractDiscussion(ctx context.Context, session *cdp.PageSession, plan renderedExtractContentPlan) (renderedExtractDiscussionExpansion, error) {
	var expansion renderedExtractDiscussionExpansion
	if err := evaluateJSONValue(ctx, session, renderedExtractDiscussionExpansionExpression(plan), "rendered discussion expansion", &expansion); err != nil {
		return renderedExtractDiscussionExpansion{}, err
	}
	return expansion, nil
}

func renderedExtractDiscussionExpansionExpression(plan renderedExtractContentPlan) string {
	profileJSON, _ := json.Marshal(plan.Profile)
	template := `(async () => {
  const marker = "__cdp_cli_rendered_discussion_expand__";
  const profile = __PROFILE__;
  const limit = 500;
  const maxRounds = 80;
  const settleMs = 250;
  const byteLimit = 102400;
  const text = (node) => String((node && (node.getAttribute("aria-label") || node.innerText || node.textContent)) || "").replace(/\s+/g, " ").trim();
  const visible = (node) => {
    if (!node || !node.isConnected || node.closest('[aria-hidden="true"], [inert]')) return false;
    const style = getComputedStyle(node);
    return style.display !== "none" && style.visibility !== "hidden" && style.visibility !== "collapse" && node.getClientRects().length > 0;
  };
  const enabledButton = (node) => visible(node) && !node.disabled && (node.tagName === "BUTTON" || node.getAttribute("role") === "button");
  const controls = () => Array.from(document.querySelectorAll('button,[role="button"]')).filter(enabledButton);
  const xThreadArticles = () => {
    const conversation = document.querySelector('[aria-label="Timeline: Conversation"]');
    if (!conversation) return [];
    return Array.from(conversation.querySelectorAll('article[data-testid="tweet"]')).filter(visible);
  };
  const xMatch = location.pathname.match(/^\/[^/]+\/status\/([0-9]+)$/);
  const xCache = window.__cdp_cli_rendered_x_discussion__ || (window.__cdp_cli_rendered_x_discussion__ = {path: location.pathname, requested_id: xMatch && xMatch[1] || "", root: null, replies: {}, bytes: 0, size_ceiling: false});
  const xCacheValid = () => xCache.path === location.pathname && xCache.requested_id && xCache.requested_id === (location.pathname.match(/^\/[^/]+\/status\/([0-9]+)$/) || [])[1];
  const own = (article, selector) => Array.from(article.querySelectorAll(selector)).filter((node) => node.closest("article") === article);
  const xRecord = (article) => {
    const timeNode = own(article, "time")[0];
    const anchor = timeNode && timeNode.closest("a[href]");
    if (!anchor) return null;
    const match = new URL(anchor.href, location.href).pathname.match(/\/status\/([0-9]+)$/);
    const textNode = own(article, '[data-testid="tweetText"]')[0];
    const value = textNode && String(textNode.innerText || textNode.textContent || "").replace(/\s+/g, " ").trim();
    if (!match || !value) return null;
    const authorNode = own(article, '[data-testid="User-Name"]')[0];
    return {id: match[1], url: anchor.href, author: String(authorNode && (authorNode.innerText || authorNode.textContent) || "").replace(/\s+/g, " ").trim(), text: value, timestamp: String(timeNode && timeNode.getAttribute("datetime") || ""), depth: Number.parseInt(article.getAttribute("data-thread-depth") || "0", 10) || 0};
  };
  const snapshotX = () => {
    if (!xCacheValid()) return false;
    const requestedID = xCache.requested_id;
    const allTweets = Array.from(document.querySelectorAll('article[data-testid="tweet"]')).filter(visible);
    for (const article of allTweets) {
      const record = xRecord(article);
      if (!record) continue;
      if (record.id === requestedID) xCache.root = record;
    }
    for (const article of xThreadArticles()) {
      const record = xRecord(article);
      if (!record || record.id === requestedID || xCache.replies[record.id] || Object.keys(xCache.replies).length >= limit) continue;
      const bytes = new TextEncoder().encode(record.author + record.text + record.url).length;
      if (xCache.bytes + bytes > byteLimit) { xCache.size_ceiling = true; continue; }
      xCache.replies[record.id] = record;
      xCache.bytes += bytes;
    }
    return true;
  };
  const discussionItems = () => {
    if (profile === "x") {
      const match = location.pathname.match(/^\/[^/]+\/status\/([0-9]+)$/);
      const statusID = match && match[1];
      const tweets = xThreadArticles();
      const root = statusID && tweets.find((tweet) => Array.from(tweet.querySelectorAll("a[href]")).some((anchor) => {
        try { return new URL(anchor.href, location.href).pathname.endsWith("/status/" + statusID); } catch (_) { return false; }
      }));
      return tweets.filter((tweet) => tweet !== root);
    }
    if (profile === "linkedin") return Array.from(document.querySelectorAll('article[data-id^="urn:li:comment:"]')).filter(visible);
    if (profile === "reddit") return Array.from(document.querySelectorAll("shreddit-comment")).filter(visible);
    return [];
  };
  if (profile === "x" && !document.querySelector('[aria-label="Timeline: Conversation"]')) {
    snapshotX();
    return {status: "unknown", interactions: 0};
  }
  const relevantControls = () => {
    if (profile === "x") return controls().filter((node) => /^show more$/i.test(text(node)) && node.getAttribute("data-testid") === "tweet-text-show-more-link" && Boolean(node.closest('[aria-label="Timeline: Conversation"] article[data-testid="tweet"]')));
    if (profile === "linkedin") return controls().filter((node) => /^(click to see more pages|load previous replies(?: on .+)?)$/i.test(text(node)) && Boolean(node.closest("main, [role=dialog]")));
    if (profile === "reddit") return Array.from(document.querySelectorAll("button")).filter((node) => enabledButton(node) && /^(?:[0-9]+ )?more repl(?:y|ies)$/i.test(text(node)) && Boolean(node.closest("shreddit-comment, shreddit-post")));
    return [];
  };
  let interactions = 0;
  let previousCount = -1;
  let stalledRounds = 0;
  for (let round = 0; round < maxRounds; round += 1) {
    if (profile === "x" && !snapshotX()) return {status: "invalid", interactions};
    const count = discussionItems().length;
    if (count >= limit) return {status: "ceiling", interactions};
    const candidates = relevantControls().slice(0, Math.max(1, Math.min(12, limit - count)));
    if (candidates.length) {
      for (const control of candidates) {
        control.scrollIntoView({block: "center", inline: "nearest"});
        control.click();
        interactions += 1;
      }
    } else {
      const items = discussionItems();
      const last = items[items.length - 1];
      if (last) last.scrollIntoView({block: "end", inline: "nearest"});
      else window.scrollBy({top: Math.max(480, window.innerHeight), behavior: "instant"});
    }
    await new Promise((resolve) => setTimeout(resolve, settleMs));
    if (profile === "x" && !snapshotX()) return {status: "invalid", interactions};
    if (profile === "x" && xCache.size_ceiling) return {status: "ceiling", interactions};
    const nextCount = discussionItems().length;
    if (nextCount >= limit) return {status: "ceiling", interactions};
    if (nextCount <= previousCount && !relevantControls().length) stalledRounds += 1;
    else stalledRounds = 0;
    if (stalledRounds >= 4) return {status: "exhausted", interactions};
    previousCount = nextCount;
  }
  return {status: "partial", interactions};
})()`
	return strings.ReplaceAll(template, "__PROFILE__", string(profileJSON))
}
