package cli

import (
	"context"
	"testing"
	"time"
)

func TestRenderedExtractReadinessWaitsForContentStability(t *testing.T) {
	checks := []renderedExtractReadiness{
		{URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true, SelectorMatchCount: 1, SelectedTextLength: 20, SelectedHTMLLength: 80, SelectedWordCount: 4, BodyTextLength: 20, BodyHTMLLength: 80, ElementCount: 5, DOMSignature: "small"},
		{URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true, SelectorMatchCount: 1, SelectedTextLength: 120, SelectedHTMLLength: 420, SelectedWordCount: 20, BodyTextLength: 120, BodyHTMLLength: 420, ElementCount: 12, DOMSignature: "hydrated"},
		{URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true, SelectorMatchCount: 1, SelectedTextLength: 120, SelectedHTMLLength: 420, SelectedWordCount: 20, BodyTextLength: 120, BodyHTMLLength: 420, ElementCount: 12, DOMSignature: "hydrated"},
		{URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true, SelectorMatchCount: 1, SelectedTextLength: 120, SelectedHTMLLength: 420, SelectedWordCount: 20, BodyTextLength: 120, BodyHTMLLength: 420, ElementCount: 12, DOMSignature: "hydrated"},
	}
	index := 0
	got, err := waitForRenderedExtractReadinessFunc(context.Background(), func(context.Context, string) (renderedExtractReadiness, error) {
		if index >= len(checks) {
			return checks[len(checks)-1], nil
		}
		check := checks[index]
		index++
		return check, nil
	}, "body", 2*time.Second, "useful-content", 5, 64, time.Nanosecond)
	if err != nil {
		t.Fatalf("wait readiness: %v", err)
	}
	if index != 4 {
		t.Fatalf("readiness polls = %d, want 4 to wait through growth and two stable polls", index)
	}
	if !got.UsefulContentSeen || !got.ContentGrewSeen || !got.ContentStableSeen || !got.TextStableSeen || !got.HTMLStableSeen || !got.NetworkIdleSeen || got.StablePolls != 2 || got.PollCount != 4 {
		t.Fatalf("readiness = %+v, want useful grown stable content", got)
	}
}
