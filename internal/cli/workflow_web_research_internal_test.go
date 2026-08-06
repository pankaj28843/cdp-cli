package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebResearchNavigationPacerZeroDoesNotSleep(t *testing.T) {
	sleepCalls := 0
	pacer := newWebResearchNavigationPacer(0, time.Now, func(context.Context, time.Duration) error {
		sleepCalls++
		return nil
	})

	for i := 0; i < 3; i++ {
		if err := pacer.Wait(context.Background()); err != nil {
			t.Fatalf("Wait() call %d error = %v", i+1, err)
		}
	}
	if sleepCalls != 0 {
		t.Fatalf("sleep calls = %d, want 0 when navigation delay is disabled", sleepCalls)
	}
}

func TestWebResearchNavigationPacerDelaysBetweenConcurrentNavigations(t *testing.T) {
	const delay = 30 * time.Second
	now := time.Unix(1_700_000_000, 0)
	var sleeps []time.Duration
	pacer := newWebResearchNavigationPacer(delay, func() time.Time {
		return now
	}, func(_ context.Context, duration time.Duration) error {
		sleeps = append(sleeps, duration)
		now = now.Add(duration)
		return nil
	})

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- pacer.Wait(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
	}
	if len(sleeps) != 2 || sleeps[0] != delay || sleeps[1] != delay {
		t.Fatalf("sleep durations = %v, want [%s %s] with no delay before the first navigation", sleeps, delay, delay)
	}
}

func TestWebResearchNavigationPacerCancellationInterruptsDelay(t *testing.T) {
	const delay = 30 * time.Second
	now := time.Unix(1_700_000_000, 0)
	sleepCalls := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pacer := newWebResearchNavigationPacer(delay, func() time.Time {
		return now
	}, func(sleepCtx context.Context, duration time.Duration) error {
		sleepCalls++
		if duration != delay {
			t.Fatalf("sleep duration = %s, want %s", duration, delay)
		}
		cancel()
		<-sleepCtx.Done()
		return sleepCtx.Err()
	})
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait() error = %v", err)
	}

	err := pacer.Wait(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("second Wait() error = %v, want context.Canceled", err)
	}
	if sleepCalls != 1 {
		t.Fatalf("sleep calls = %d, want 1", sleepCalls)
	}
}

func TestWebResearchNavigationPacerMeasuresRecordedNavigationStarts(t *testing.T) {
	const delay = 30 * time.Second
	now := time.Unix(1_700_000_000, 0)
	pacer := newWebResearchNavigationPacer(delay, func() time.Time {
		return now
	}, func(_ context.Context, duration time.Duration) error {
		now = now.Add(duration)
		return nil
	})
	var starts []time.Time
	navigate := func(context.Context, string) (string, error) {
		starts = append(starts, now)
		return "frame", nil
	}

	for _, setupDuration := range []time.Duration{20 * time.Second, time.Second} {
		now = now.Add(setupDuration)
		if _, err := dispatchRenderedExtractNavigation(context.Background(), "https://example.test", pacer.Wait, navigate); err != nil {
			t.Fatalf("dispatch navigation: %v", err)
		}
	}
	if len(starts) != 2 {
		t.Fatalf("navigation starts = %v, want two starts", starts)
	}
	if spacing := starts[1].Sub(starts[0]); spacing < delay {
		t.Fatalf("navigation start spacing = %s, want at least %s", spacing, delay)
	}
}

func TestWebResearchSERPArtifactIDIncludesInputPositionAndWindowIdentity(t *testing.T) {
	const dateFilter = "cdr:1,cd_min:01/01/2026,cd_max:07/01/2026"
	evergreen := webResearchSERPArtifactID(0, webResearchQuery{Text: "Production LLM systems"})
	dated := webResearchSERPArtifactID(1, webResearchQuery{Text: "Production LLM systems", TimeFilter: dateFilter})
	repeatedDated := webResearchSERPArtifactID(2, webResearchQuery{Text: "Production LLM systems", TimeFilter: dateFilter})

	if evergreen != "001-production-llm-systems--all-time" {
		t.Fatalf("evergreen artifact ID = %q", evergreen)
	}
	if !strings.HasPrefix(dated, "002-production-llm-systems--tbs-") {
		t.Fatalf("dated artifact ID = %q, want stable input position and hashed tbs identity", dated)
	}
	if dated == repeatedDated || !strings.HasPrefix(repeatedDated, "003-production-llm-systems--tbs-") {
		t.Fatalf("repeated dated artifact IDs = %q and %q, want collision-free input positions", dated, repeatedDated)
	}
	if strings.Contains(dated, dateFilter) || strings.ContainsAny(dated, ",:/") {
		t.Fatalf("dated artifact ID %q exposes unsafe raw time filter %q", dated, dateFilter)
	}
}

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

func TestWebResearchGoogleAIResponseExpressionUsesSemanticFallbacks(t *testing.T) {
	expression := webResearchGoogleAIResponseExpression("auto")
	for _, want := range []string{
		"#m-x-content",
		"AI Overview",
		"data-sfc-root",
		"AI Mode conversation:",
		"udm",
		"sources_truncated",
		"externalSource",
	} {
		if !strings.Contains(expression, want) {
			t.Fatalf("Google AI response expression missing %q", want)
		}
	}
}

func TestWebResearchGoogleAIExpansionExpressionStaysInline(t *testing.T) {
	expression := webResearchGoogleAIExpansionExpression("auto")
	for _, want := range []string{
		"data-aim",
		"show more ai overview",
		"aria-expanded=\"false\"",
		"control.click()",
		"clicked",
	} {
		if !strings.Contains(expression, want) {
			t.Fatalf("Google AI expansion expression missing %q", want)
		}
	}
	if strings.Contains(expression, "location.assign") || strings.Contains(expression, "location.replace") {
		t.Fatal("inline Google AI expansion must not navigate the page")
	}
}

func TestWebResearchGoogleAIResponseBoundsInlineTextAndSources(t *testing.T) {
	response := &webResearchGoogleAIResponse{
		RequestedMode: "auto",
		Mode:          "overview",
		Status:        "present",
		fullText:      strings.Repeat("response ", webResearchGoogleAIInlineTextLimit),
		Sources: []webResearchGoogleAISource{
			{Title: "B", URL: "https://b.example.test", Source: "b.example.test"},
			{Title: "A", URL: "https://a.example.test", Source: "a.example.test"},
		},
	}
	response.Text, response.TextTruncated = boundWebResearchGoogleAIText(response.fullText, webResearchGoogleAIInlineTextLimit)
	response.TextLength = len([]rune(response.fullText))
	if !response.TextTruncated || len([]rune(response.Text)) == 0 || len([]rune(response.Text)) > webResearchGoogleAIInlineTextLimit {
		t.Fatalf("bounded response = truncated=%v length=%d, want explicit bounded text", response.TextTruncated, len([]rune(response.Text)))
	}
	markdown := webResearchGoogleAIResponseMarkdown(response)
	if !strings.Contains(markdown, "## Rendered response") || !strings.Contains(markdown, "https://a.example.test") || !strings.Contains(markdown, "https://b.example.test") {
		preview := markdown
		if len(preview) > 400 {
			preview = preview[:400]
		}
		t.Fatalf("Google AI response Markdown = %q, want full response and sorted source links", preview)
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
