package cli

import "testing"

func TestDetectSERPBlocked(t *testing.T) {
	blocked, signals := detectSERPBlocked(renderedExtractResult{
		Report: map[string]any{"workflow": map[string]any{"final_url": "https://www.google.com/sorry/index?continue=https://www.google.com/search%3Fq%3Dagentic"}},
		Warnings: []string{
			"google SERP extraction found no decoded external result links",
			"final URL suggests consent, CAPTCHA, or bot-check handling",
		},
	})
	if !blocked || !stringListContains(signals, "blocked_final_url") || !stringListContains(signals, "bot_check_warning") || !stringListContains(signals, "no_external_result_links") {
		t.Fatalf("detectSERPBlocked() = %v, %+v; want blocked with URL, bot, and empty-link signals", blocked, signals)
	}

	blocked, signals = detectSERPBlocked(renderedExtractResult{
		Report: map[string]any{"workflow": map[string]any{"final_url": "https://www.google.com/search?q=agentic"}},
		Warnings: []string{
			"google SERP extraction found no decoded external result links",
			"In order to continue, please enable javascript on your web browser. Our systems have detected unusual traffic from your computer network.",
		},
	})
	if !blocked || !stringListContains(signals, "block_page_text") {
		t.Fatalf("detectSERPBlocked() = %v, %+v; want blocked from unusual-traffic page text", blocked, signals)
	}

	blocked, signals = detectSERPBlocked(renderedExtractResult{
		Report:   map[string]any{"workflow": map[string]any{"final_url": "https://www.google.com/search?q=agentic"}},
		Warnings: []string{"google SERP extraction found no decoded external result links"},
	})
	if blocked || !stringListContains(signals, "no_external_result_links") {
		t.Fatalf("detectSERPBlocked() = %v, %+v; want empty SERP warning without blocked classification", blocked, signals)
	}
}
