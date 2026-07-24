package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

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
		Report: map[string]any{"workflow": map[string]any{"final_url": "https://duckduckgo.com/?q=agentic"}},
		Warnings: []string{
			"page text suggests consent, CAPTCHA, auth, or bot-check handling",
		},
	})
	if !blocked || !stringListContains(signals, "block_page_text") {
		t.Fatalf("detectSERPBlocked() = %v, %+v; want blocked from rendered bot-check page text", blocked, signals)
	}

	blocked, signals = detectSERPBlocked(renderedExtractResult{
		Report:   map[string]any{"workflow": map[string]any{"final_url": "https://www.google.com/search?q=agentic"}},
		Warnings: []string{"google SERP extraction found no decoded external result links"},
	})
	if blocked || !stringListContains(signals, "no_external_result_links") {
		t.Fatalf("detectSERPBlocked() = %v, %+v; want empty SERP warning without blocked classification", blocked, signals)
	}
}

func TestRenderedExtractLinksExpressionDecodesDuckDuckGoRedirects(t *testing.T) {
	expr := renderedExtractLinksExpression("duckduckgo")
	for _, want := range []string{"decodeDuckDuckGoURL", "uddg", "serp === \"duckduckgo\""} {
		if !strings.Contains(expr, want) {
			t.Fatalf("renderedExtractLinksExpression(duckduckgo) missing %q in expression", want)
		}
	}
}

func TestSelectFairWebResearchCandidatesRepresentsEachProductiveQueryBeforeRefill(t *testing.T) {
	queries := []webResearchQuery{{Text: "first"}, {Text: "second"}, {Text: "third"}}
	pool := []webResearchCandidate{
		{QueryIndex: 0, Query: "first", URL: "https://example.test/first-1", Rank: 1},
		{QueryIndex: 0, Query: "first", URL: "https://example.test/first-2", Rank: 2},
		{QueryIndex: 1, Query: "second", URL: "https://example.test/second-1", Rank: 1},
		{QueryIndex: 2, Query: "third", URL: "https://example.test/third-1", Rank: 1},
	}

	selected, coverage := selectFairWebResearchCandidates(queries, pool, 3)
	if len(selected) != 3 {
		t.Fatalf("selected = %#v, want three candidates", selected)
	}
	for index, wantQuery := range []string{"first", "second", "third"} {
		if selected[index].Query != wantQuery {
			t.Fatalf("selected[%d].Query = %q, want %q; selection must be round-robin", index, selected[index].Query, wantQuery)
		}
		if coverage[index].SelectedCandidates != 1 {
			t.Fatalf("coverage[%d] = %+v, want one represented candidate", index, coverage[index])
		}
	}
	if coverage[0].OmittedByCap != 1 || coverage[0].ProducedCandidates != 2 {
		t.Fatalf("first query coverage = %+v, want one selected and one omitted", coverage[0])
	}
}

func TestSelectFairWebResearchCandidatesCountsCrossQueryDuplicates(t *testing.T) {
	queries := []webResearchQuery{{Text: "first"}, {Text: "second"}}
	pool := []webResearchCandidate{
		{QueryIndex: 0, Query: "first", URL: "https://example.test/shared#one", Rank: 1},
		{QueryIndex: 1, Query: "second", URL: "https://example.test/shared#two", Rank: 1},
		{QueryIndex: 1, Query: "second", URL: "https://example.test/second", Rank: 2},
	}

	selected, coverage := selectFairWebResearchCandidates(queries, pool, 2)
	if len(selected) != 2 || selected[0].Query != "first" || selected[1].URL != "https://example.test/second" {
		t.Fatalf("selected = %#v, want shared first candidate plus second query's unique candidate", selected)
	}
	if coverage[1].DuplicateCandidates != 1 || coverage[1].SelectedCandidates != 1 || coverage[1].ProducedCandidates != 2 {
		t.Fatalf("second query coverage = %+v, want one cross-query duplicate and one selected", coverage[1])
	}
}

func TestSelectFairWebResearchCandidatesCapBelowProductiveQueriesUsesInputOrder(t *testing.T) {
	queries := []webResearchQuery{{Text: "first"}, {Text: "second"}, {Text: "third"}}
	pool := []webResearchCandidate{
		{QueryIndex: 2, Query: "third", URL: "https://example.test/third", Rank: 1},
		{QueryIndex: 0, Query: "first", URL: "https://example.test/first", Rank: 1},
		{QueryIndex: 1, Query: "second", URL: "https://example.test/second", Rank: 1},
	}

	selected, coverage := selectFairWebResearchCandidates(queries, pool, 2)
	if len(selected) != 2 || selected[0].QueryIndex != 0 || selected[1].QueryIndex != 1 {
		t.Fatalf("selected = %#v, want deterministic input-order priority when cap is insufficient", selected)
	}
	if coverage[2].SelectedCandidates != 0 || coverage[2].OmittedByCap != 1 || !coverage[2].Productive || coverage[2].Represented {
		t.Fatalf("third query coverage = %+v, want productive but cap-omitted", coverage[2])
	}
}

func TestWebResearchRetryCommandPreservesSettle(t *testing.T) {
	execution := webResearchRetryExecutionContext{
		BrowserMode: "headed",
		StateDir:    "/tmp/cdp state",
		Connection:  "research-browser",
	}
	command := webResearchRetryCommand("tmp/pages", execution, 20*time.Second, 3*time.Second, "useful-content", "main", "auto", 12, 10, 8, 256, 2)
	for _, want := range []string{"--browser-mode headed", "--state-dir '/tmp/cdp state'", "--connection research-browser", "--wait 20s", "--settle 3s", "--wait-until useful-content", "--selector main", "--content-extractor auto"} {
		if !strings.Contains(command, want) {
			t.Fatalf("retry command %q missing %q", command, want)
		}
	}
}

func TestClassifyWorkflowExtractFailureKeepsQualityRetryBoundaryDistinct(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "timeout command", err: commandError("timeout", "timeout", "page deadline", ExitTimeout, nil), want: "page_timeout"},
		{name: "context deadline", err: context.DeadlineExceeded, want: "page_timeout"},
		{name: "ordinary connection failure", err: commandError("connection_failed", "connection", "dial refused", ExitConnection, nil), want: "collector_error"},
		{name: "invalid input", err: commandError("invalid_selector", "usage", "bad selector", ExitUsage, nil), want: "collector_error"},
		{name: "collector failure", err: errors.New("snapshot collector failed"), want: "collector_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyWorkflowExtractFailure(test.err)
			if got != test.want {
				t.Fatalf("classifyWorkflowExtractFailure(%v) = %q, want %q", test.err, got, test.want)
			}
			if got == "quality_gate_failed" {
				t.Fatalf("non-quality error %v inherited retryable quality classification", test.err)
			}
		})
	}
}
