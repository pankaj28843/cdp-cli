package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/config"
)

const (
	webResearchGoogleAIAuto    = "auto"
	webResearchGoogleAIMode    = "mode"
	webResearchGoogleAIOff     = "off"
	webResearchGoogleAIDefault = webResearchGoogleAIAuto

	webResearchGoogleAIInlineTextLimit = 12000
	webResearchGoogleAISourceLimit     = 60
)

type webResearchGoogleAISource struct {
	Title  string `json:"title"`
	URL    string `json:"url"`
	Source string `json:"source,omitempty"`
}

type webResearchGoogleAIResponse struct {
	RequestedMode    string                      `json:"requested_mode"`
	Mode             string                      `json:"mode"`
	Status           string                      `json:"status"`
	Heading          string                      `json:"heading,omitempty"`
	Text             string                      `json:"text,omitempty"`
	TextLength       int                         `json:"text_length"`
	TextTruncated    bool                        `json:"text_truncated,omitempty"`
	Sources          []webResearchGoogleAISource `json:"sources,omitempty"`
	SourceCount      int                         `json:"source_count"`
	SourcesTruncated bool                        `json:"sources_truncated,omitempty"`
	Expanded         bool                        `json:"expanded,omitempty"`
	Artifacts        map[string]string           `json:"artifacts,omitempty"`
	Warnings         []string                    `json:"warnings,omitempty"`

	fullText string `json:"-"`
}

type webResearchGoogleAIResponseCapture struct {
	Status           string                      `json:"status"`
	Mode             string                      `json:"mode"`
	Heading          string                      `json:"heading"`
	Text             string                      `json:"text"`
	TextTruncated    bool                        `json:"text_truncated"`
	Sources          []webResearchGoogleAISource `json:"sources"`
	SourceCount      int                         `json:"source_count"`
	SourcesTruncated bool                        `json:"sources_truncated"`
	Expanded         bool                        `json:"expanded"`
}

type webResearchGoogleAIExpansionResult struct {
	Clicked bool `json:"clicked"`
}

const (
	webResearchGoogleAIPolicySourceFlag    = "flag"
	webResearchGoogleAIPolicySourceConfig  = "config"
	webResearchGoogleAIPolicySourceDefault = "default"
)

type webResearchGoogleAIPolicyResolution struct {
	Policy    string
	Source    string
	Exclusive bool
}

func parseWebResearchGoogleAIPolicy(value string) (string, error) {
	policy := strings.ToLower(strings.TrimSpace(value))
	if policy == "" {
		policy = webResearchGoogleAIDefault
	}
	switch policy {
	case webResearchGoogleAIAuto, webResearchGoogleAIMode, webResearchGoogleAIOff:
		return policy, nil
	default:
		return "", fmt.Errorf("unsupported Google AI response policy %q", policy)
	}
}

func resolveWebResearchGoogleAIPolicy(requested string, explicit bool, cfg config.Config) (webResearchGoogleAIPolicyResolution, error) {
	if explicit {
		policy, err := parseWebResearchGoogleAIPolicy(requested)
		if err != nil {
			return webResearchGoogleAIPolicyResolution{}, err
		}
		return webResearchGoogleAIPolicyResolution{
			Policy:    policy,
			Source:    webResearchGoogleAIPolicySourceFlag,
			Exclusive: policy == webResearchGoogleAIMode,
		}, nil
	}
	if cfg.GoogleExclusiveAIModeConfigured() {
		policy := webResearchGoogleAIAuto
		if cfg.Agents.Google.ExclusiveAIMode {
			policy = webResearchGoogleAIMode
		}
		return webResearchGoogleAIPolicyResolution{
			Policy:    policy,
			Source:    webResearchGoogleAIPolicySourceConfig,
			Exclusive: cfg.Agents.Google.ExclusiveAIMode,
		}, nil
	}
	policy, err := parseWebResearchGoogleAIPolicy(requested)
	if err != nil {
		return webResearchGoogleAIPolicyResolution{}, err
	}
	return webResearchGoogleAIPolicyResolution{
		Policy:    policy,
		Source:    webResearchGoogleAIPolicySourceDefault,
		Exclusive: false,
	}, nil
}

func expandWebResearchGoogleAIResponse(ctx context.Context, session *cdp.PageSession, requestedPolicy string) (bool, error) {
	result, err := session.Evaluate(ctx, webResearchGoogleAIExpansionExpression(requestedPolicy), true)
	if err != nil {
		return false, fmt.Errorf("evaluate Google AI response expansion: %w", err)
	}
	if result.Exception != nil {
		return false, fmt.Errorf("Google AI response expansion javascript exception: %s", result.Exception.Text)
	}
	var expansion webResearchGoogleAIExpansionResult
	if err := json.Unmarshal(result.Object.Value, &expansion); err != nil {
		return false, fmt.Errorf("decode Google AI response expansion: %w", err)
	}
	return expansion.Clicked, nil
}

func webResearchGoogleAIModeForURL(rawURL, requestedPolicy string) string {
	if parsed, err := url.Parse(strings.TrimSpace(rawURL)); err == nil {
		if parsed.Query().Get("udm") == "50" {
			return "ai_mode"
		}
	}
	if strings.TrimSpace(strings.ToLower(requestedPolicy)) == webResearchGoogleAIMode {
		return "ai_mode"
	}
	return "overview"
}

func newUnavailableWebResearchGoogleAIResponse(rawURL, requestedPolicy string, err error) *webResearchGoogleAIResponse {
	response := &webResearchGoogleAIResponse{
		RequestedMode: requestedPolicy,
		Mode:          webResearchGoogleAIModeForURL(rawURL, requestedPolicy),
		Status:        "unavailable",
		Sources:       []webResearchGoogleAISource{},
	}
	if err != nil {
		response.Warnings = []string{"Google AI response collection was unavailable: " + err.Error()}
	}
	return response
}

func collectWebResearchGoogleAIResponse(ctx context.Context, session *cdp.PageSession, rawURL, requestedPolicy string) (*webResearchGoogleAIResponse, error) {
	if strings.TrimSpace(requestedPolicy) == "" {
		requestedPolicy = webResearchGoogleAIDefault
	}
	result, err := session.Evaluate(ctx, webResearchGoogleAIResponseExpression(requestedPolicy), true)
	if err != nil {
		return nil, fmt.Errorf("evaluate Google AI response collector: %w", err)
	}
	if result.Exception != nil {
		return nil, fmt.Errorf("Google AI response collector javascript exception: %s", result.Exception.Text)
	}
	var capture webResearchGoogleAIResponseCapture
	if err := json.Unmarshal(result.Object.Value, &capture); err != nil {
		return nil, fmt.Errorf("decode Google AI response collector: %w", err)
	}
	if capture.Status == "" {
		return nil, fmt.Errorf("Google AI response collector returned no status")
	}
	sources := boundWebResearchGoogleAISources(capture.Sources)
	if sources == nil {
		sources = []webResearchGoogleAISource{}
	}
	response := &webResearchGoogleAIResponse{
		RequestedMode:    requestedPolicy,
		Mode:             capture.Mode,
		Status:           capture.Status,
		Heading:          strings.TrimSpace(capture.Heading),
		SourceCount:      capture.SourceCount,
		SourcesTruncated: capture.SourcesTruncated,
		Expanded:         capture.Expanded,
		Sources:          sources,
		fullText:         strings.TrimSpace(capture.Text),
	}
	if response.Mode == "" {
		response.Mode = webResearchGoogleAIModeForURL(rawURL, requestedPolicy)
	}
	response.Text, response.TextTruncated = boundWebResearchGoogleAIText(response.fullText, webResearchGoogleAIInlineTextLimit)
	response.TextTruncated = response.TextTruncated || capture.TextTruncated
	response.TextLength = len([]rune(response.fullText))
	if response.SourceCount > len(response.Sources) {
		response.SourcesTruncated = true
	}
	if response.SourceCount == 0 {
		response.SourceCount = len(response.Sources)
	}
	if response.Status != "present" {
		response.Text = ""
		response.TextLength = 0
		response.fullText = ""
	}
	return response, nil
}

func waitForWebResearchGoogleAIResponse(ctx context.Context, session *cdp.PageSession, rawURL, requestedPolicy string, wait, settle time.Duration) (*webResearchGoogleAIResponse, error) {
	if wait <= 0 {
		expanded, expansionErr := expandWebResearchGoogleAIResponse(ctx, session, requestedPolicy)
		response, collectErr := collectWebResearchGoogleAIResponse(ctx, session, rawURL, requestedPolicy)
		if collectErr != nil {
			return nil, collectErr
		}
		if expansionErr == nil {
			response.Expanded = response.Expanded || expanded
		}
		return response, nil
	}

	deadline := time.Now().Add(wait)
	var lastResponse *webResearchGoogleAIResponse
	var lastErr error
	var stableSince time.Time
	var lastFingerprint string
	expanded := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Now().After(deadline) {
			if lastResponse != nil {
				lastResponse.Expanded = lastResponse.Expanded || expanded
				return lastResponse, nil
			}
			if lastErr != nil {
				return nil, lastErr
			}
			return &webResearchGoogleAIResponse{
				RequestedMode: requestedPolicy,
				Mode:          webResearchGoogleAIModeForURL(rawURL, requestedPolicy),
				Status:        "not_present",
				Sources:       []webResearchGoogleAISource{},
			}, nil
		}

		if clicked, err := expandWebResearchGoogleAIResponse(ctx, session, requestedPolicy); err == nil {
			expanded = expanded || clicked
		} else {
			lastErr = err
		}
		response, err := collectWebResearchGoogleAIResponse(ctx, session, rawURL, requestedPolicy)
		if err != nil {
			lastErr = err
		} else {
			lastErr = nil
			response.Expanded = response.Expanded || expanded
			lastResponse = response
			if response.Status == "present" {
				fingerprint := fmt.Sprintf("%s\x00%d\x00%d", response.fullText, response.SourceCount, len(response.Sources))
				if stableSince.IsZero() || fingerprint != lastFingerprint {
					stableSince = time.Now()
					lastFingerprint = fingerprint
				}
				if settle <= 0 || time.Since(stableSince) >= settle {
					return response, nil
				}
			} else {
				stableSince = time.Time{}
				lastFingerprint = ""
			}
		}

		sleepFor := 500 * time.Millisecond
		if remaining := time.Until(deadline); remaining < sleepFor {
			sleepFor = remaining
		}
		if sleepFor <= 0 {
			continue
		}
		timer := time.NewTimer(sleepFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func boundWebResearchGoogleAIText(value string, limit int) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 {
		return value, false
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value, false
	}
	marker := "\n[truncated; read the full response artifact]"
	markerRunes := []rune(marker)
	if len(markerRunes) >= limit {
		return string(runes[:limit]), true
	}
	return string(runes[:limit-len(markerRunes)]) + marker, true
}

func boundWebResearchGoogleAISources(sources []webResearchGoogleAISource) []webResearchGoogleAISource {
	if len(sources) <= webResearchGoogleAISourceLimit {
		return sources
	}
	return sources[:webResearchGoogleAISourceLimit]
}

func webResearchGoogleAIResponseMarkdown(response *webResearchGoogleAIResponse) string {
	if response == nil {
		return "# Google AI response\n\nStatus: not collected\n"
	}
	var b strings.Builder
	b.WriteString("# Google AI response\n\n")
	b.WriteString("- Requested mode: `")
	b.WriteString(response.RequestedMode)
	b.WriteString("`\n- Rendered mode: `")
	b.WriteString(response.Mode)
	b.WriteString("`\n- Status: `")
	b.WriteString(response.Status)
	b.WriteString("`\n")
	b.WriteString("- Expanded inline: ")
	b.WriteString(fmt.Sprintf("%t\n", response.Expanded))
	if response.Heading != "" {
		b.WriteString("- Heading: ")
		b.WriteString(response.Heading)
		b.WriteString("\n")
	}
	b.WriteString("- Response characters: ")
	b.WriteString(fmt.Sprintf("%d\n", response.TextLength))

	if response.fullText != "" {
		b.WriteString("\n## Rendered response\n\n")
		b.WriteString(response.fullText)
		b.WriteString("\n")
	}
	if len(response.Sources) > 0 {
		b.WriteString("\n## Cited external links\n\n")
		sources := append([]webResearchGoogleAISource(nil), response.Sources...)
		sort.SliceStable(sources, func(i, j int) bool {
			if sources[i].Source == sources[j].Source {
				return sources[i].Title < sources[j].Title
			}
			return sources[i].Source < sources[j].Source
		})
		for _, source := range sources {
			title := source.Title
			if title == "" {
				title = source.URL
			}
			b.WriteString("- [")
			b.WriteString(title)
			b.WriteString("](")
			b.WriteString(source.URL)
			b.WriteString(")")
			if source.Source != "" {
				b.WriteString(" — ")
				b.WriteString(source.Source)
			}
			b.WriteString("\n")
		}
	}
	if len(response.Warnings) > 0 {
		b.WriteString("\n## Warnings\n\n")
		for _, warning := range response.Warnings {
			b.WriteString("- ")
			b.WriteString(warning)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func webResearchGoogleAIResponseExpression(requestedPolicy string) string {
	policyJSON, _ := json.Marshal(requestedPolicy)
	return fmt.Sprintf(`(() => {
  "__cdp_cli_google_ai_response__";
  const requested = %s;
  const sourceLimit = %d;
  const normalize = (value) => (value || "").replace(/\u00a0/g, " ").split(/\n+/).map((line) => line.replace(/[ \t]+/g, " ").trim()).filter(Boolean).join("\n");
  const visible = (element) => {
    if (!element) return false;
    const rect = element.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  const externalSource = (href) => {
    try {
      const parsed = new URL(href, document.baseURI);
      if (!/^https?:$/i.test(parsed.protocol)) return false;
      const host = parsed.hostname.toLowerCase();
      return !host.includes("google.") && !host.endsWith("gstatic.com");
    } catch (error) {
      return false;
    }
  };
	  const sourceLinks = (root) => {
    const sources = [];
    const seen = new Set();
    let sourceCount = 0;
    let truncated = false;
    for (const anchor of Array.from(root.querySelectorAll("a[href]"))) {
      let href = "";
      try { href = new URL(anchor.getAttribute("href") || "", document.baseURI).href; } catch (error) { continue; }
      if (!externalSource(href) || seen.has(href)) continue;
      seen.add(href);
      sourceCount++;
      const title = normalize(anchor.innerText || anchor.textContent || anchor.getAttribute("aria-label") || anchor.getAttribute("title") || "");
      let source = "";
      try { source = new URL(href).hostname.replace(/^www\./, ""); } catch (error) {}
      if (sources.length < sourceLimit) sources.push({title, url: href, source});
    }
    truncated = sourceCount > sourceLimit;
    return {sources, sourceCount, truncated};
  };
  const aiModeURL = new URL(location.href).searchParams.get("udm") === "50";
  let mode = aiModeURL || requested === "mode" ? "ai_mode" : "overview";
  let heading = mode === "ai_mode" ? "AI Mode" : "AI Overview";
  let root = null;
  if (mode === "ai_mode") {
    root = document.querySelector('[data-sfc-root="ep"]');
    if (!visible(root)) root = null;
    if (!root) {
      const candidates = Array.from(document.querySelectorAll("#main section, #main div")).filter((element) => visible(element) && normalize(element.innerText).includes("AI Mode conversation:"));
      candidates.sort((a, b) => normalize(b.innerText).length - normalize(a.innerText).length);
      root = candidates[0] || null;
    }
    if (root) {
      const firstLine = normalize(root.innerText).split("\n")[0] || "";
      if (firstLine) heading = firstLine;
    }
  } else {
    const preferred = Array.from(document.querySelectorAll("[data-aim], #m-x-content, [data-fh], [data-subtree]")).filter((element) => visible(element) && normalize(element.innerText).includes("AI Overview") && normalize(element.innerText).length <= 20000);
    preferred.sort((a, b) => {
      const score = (element) => element.matches("[data-aim]") ? 0 : element.matches("#m-x-content") ? 1 : element.matches("[data-fh]") ? 2 : 3;
      return score(a) - score(b) || normalize(a.innerText).length - normalize(b.innerText).length;
    });
    root = preferred[0] || null;
    if (!root) {
      const candidates = Array.from(document.querySelectorAll("body div, body section, body article")).filter((element) => {
        const text = normalize(element.innerText);
        return visible(element) && text.startsWith("AI Overview") && text.length >= 100 && text.length <= 20000;
      });
      candidates.sort((a, b) => normalize(a.innerText).length - normalize(b.innerText).length);
      root = candidates[0] || null;
    }
  }
  if (!root) return {status: "not_present", mode, heading, text: "", sources: [], sources_truncated: false, text_truncated: false};
  const text = normalize(root.innerText);
  const links = sourceLinks(root);
  const expansion = Array.from(root.querySelectorAll("[aria-expanded=\"true\"]")).some((element) => /show more|expand|full response|continue reading/i.test(normalize(element.getAttribute("aria-label") || element.innerText || "")));
  return {status: text ? "present" : "not_present", mode, heading, text, sources: links.sources, source_count: links.sourceCount, sources_truncated: links.truncated, expanded: expansion, text_truncated: false};
})()`, string(policyJSON), webResearchGoogleAISourceLimit)
}

func webResearchGoogleAIExpansionExpression(requestedPolicy string) string {
	policyJSON, _ := json.Marshal(requestedPolicy)
	return fmt.Sprintf(`(() => {
  "__cdp_cli_google_ai_expand__";
  const requested = %s;
  const normalize = (value) => (value || "").replace(/\u00a0/g, " ").split(/\n+/).map((line) => line.replace(/[ \t]+/g, " ").trim()).filter(Boolean).join(" ");
  const visible = (element) => {
    if (!element) return false;
    const rect = element.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  const mode = new URL(location.href).searchParams.get("udm") === "50" || requested === "mode" ? "ai_mode" : "overview";
  const roots = mode === "ai_mode"
    ? Array.from(document.querySelectorAll('[data-sfc-root="ep"], #main section, #main div')).filter((element) => visible(element) && (normalize(element.innerText).includes("AI Mode conversation:") || element.matches('[data-sfc-root="ep"]')))
    : Array.from(document.querySelectorAll("[data-aim], #m-x-content, [data-fh], [data-subtree], #main section, #main div, #main article")).filter((element) => visible(element) && normalize(element.innerText).includes("AI Overview"));
  roots.sort((a, b) => {
    const score = (element) => element.matches("[data-aim]") ? 0 : element.matches("#m-x-content") ? 1 : element.matches("[data-fh]") ? 2 : element.matches("[data-subtree]") ? 3 : 4;
    return score(a) - score(b) || normalize(a.innerText).length - normalize(b.innerText).length;
  });
  const root = roots[0];
  if (!root) return {clicked: false};
  const controls = Array.from(root.querySelectorAll('[aria-expanded="false"], button, [role="button"], a'));
  const expansionLabel = (element) => normalize([element.getAttribute("aria-label"), element.innerText, element.textContent].filter(Boolean).join(" ")).toLowerCase();
  const isExpansion = (element) => {
    const label = expansionLabel(element);
    if (!label || /related links|sites|discussion|result|feedback|learn more|source/.test(label)) return false;
    if (/show more ai overview|expand ai overview|show full answer|full response|continue reading/.test(label)) return true;
    return element.getAttribute("aria-expanded") === "false" && /^(show|see|read|view|expand|continue) more(?: (ai overview|answer|response))?$/.test(label);
  };
  const control = controls.find((element) => visible(element) && isExpansion(element));
  if (!control) return {clicked: false};
  control.click();
  return {clicked: true};
})()`, string(policyJSON))
}
