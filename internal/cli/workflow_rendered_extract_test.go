package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRenderedExtractReadinessRequiresAllEnabledThresholdsBeforeSettling(t *testing.T) {
	start := time.Unix(1_000, 0)
	tracker := renderedExtractReadinessTracker{}
	policy := renderedExtractReadinessPolicy{
		WaitUntil:    "useful-content",
		MinWords:     5,
		MinHTMLChars: 64,
		Settle:       2 * time.Second,
	}
	shell := renderedExtractReadiness{
		URL:                     "https://example.test/app",
		DocumentReadyState:      "complete",
		SelectorMatched:         true,
		SelectorMatchCount:      1,
		SelectedTextLength:      20,
		SelectedHTMLLength:      1_347,
		SelectedWordCount:       3,
		BodyTextLength:          20,
		BodyHTMLLength:          1_347,
		ElementCount:            5,
		DOMSignature:            "v2:shell",
		NavigatedFromAboutBlank: true,
		LoadSeen:                true,
	}

	got, ready := tracker.Observe(shell, start, policy)
	if ready || got.ThresholdsMet || got.UsefulContentSeen {
		t.Fatalf("initial shell readiness = %+v, ready=%v; want subthreshold", got, ready)
	}
	got, ready = tracker.Observe(shell, start.Add(10*time.Second), policy)
	if ready || got.ContentSettledSeen || got.SettledFor != "0s" {
		t.Fatalf("stable subthreshold shell readiness = %+v, ready=%v; quiet time must not authorize capture", got, ready)
	}

	hydrated := shell
	hydrated.SelectedTextLength = 120
	hydrated.SelectedHTMLLength = 4_200
	hydrated.SelectedWordCount = 20
	hydrated.BodyTextLength = 120
	hydrated.BodyHTMLLength = 4_200
	hydrated.ElementCount = 12
	hydrated.DOMSignature = "v2:hydrated"
	got, ready = tracker.Observe(hydrated, start.Add(11*time.Second), policy)
	if ready || !got.ThresholdsMet || !got.UsefulContentSeen || got.ContentSettledSeen {
		t.Fatalf("first threshold-qualified sample = %+v, ready=%v; want settle timer to start", got, ready)
	}
	got, ready = tracker.Observe(hydrated, start.Add(13*time.Second), policy)
	if !ready || !got.ContentSettledSeen || got.Outcome != "settled" || got.SettledFor != "2s" {
		t.Fatalf("settled hydrated readiness = %+v, ready=%v", got, ready)
	}
	if got.NetworkIdleSeen {
		t.Fatalf("network_idle_seen = true; DOM stability must not impersonate network evidence")
	}
}

func TestRenderedExtractReadinessFingerprintChangeResetsQuietWindow(t *testing.T) {
	start := time.Unix(2_000, 0)
	tracker := renderedExtractReadinessTracker{}
	policy := renderedExtractReadinessPolicy{WaitUntil: "useful-content", MinWords: 5, MinHTMLChars: 64, Settle: 2 * time.Second}
	base := renderedExtractReadiness{
		URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true, SelectorMatchCount: 1,
		SelectedTextLength: 100, SelectedHTMLLength: 1_000, SelectedWordCount: 20,
		BodyTextLength: 100, BodyHTMLLength: 1_000, ElementCount: 10, DOMSignature: "v2:content-a",
		NavigatedFromAboutBlank: true, LoadSeen: true,
	}

	if _, ready := tracker.Observe(base, start, policy); ready {
		t.Fatal("first sample unexpectedly ready")
	}
	changed := base
	changed.DOMSignature = "v2:content-b"
	got, ready := tracker.Observe(changed, start.Add(2*time.Second), policy)
	if ready || got.ContentStableSeen || got.ContentSettledSeen || got.SettledFor != "0s" {
		t.Fatalf("same-length changed content = %+v, ready=%v; want quiet window reset", got, ready)
	}
	got, ready = tracker.Observe(changed, start.Add(4*time.Second), policy)
	if !ready || !got.ContentStableSeen || !got.ContentSettledSeen {
		t.Fatalf("unchanged content after reset = %+v, ready=%v; want settled", got, ready)
	}
}

func TestRenderedExtractReadinessSettleBoundaryAndLateReset(t *testing.T) {
	start := time.Unix(2_500, 0)
	policy := renderedExtractReadinessPolicy{WaitUntil: "useful-content", MinWords: 5, MinHTMLChars: 64, Settle: 2 * time.Second}
	content := renderedExtractReadiness{
		URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true,
		SelectedTextLength: 100, SelectedHTMLLength: 1_000, SelectedWordCount: 20,
		BodyTextLength: 100, BodyHTMLLength: 1_000, ElementCount: 10, DOMSignature: "v2:content-a",
	}

	tracker := renderedExtractReadinessTracker{}
	if _, ready := tracker.Observe(content, start, policy); ready {
		t.Fatal("initial threshold-qualified sample unexpectedly settled")
	}
	if got, ready := tracker.Observe(content, start.Add(1800*time.Millisecond), policy); ready || got.ContentSettledSeen {
		t.Fatalf("sample at 1.8s = %+v, ready=%v; want quiet window open", got, ready)
	}
	if got, ready := tracker.Observe(content, start.Add(2100*time.Millisecond), policy); !ready || !got.ContentSettledSeen {
		t.Fatalf("sample at 2.1s = %+v, ready=%v; want settled", got, ready)
	}

	tracker = renderedExtractReadinessTracker{}
	tracker.Observe(content, start, policy)
	changed := content
	changed.DOMSignature = "v2:content-b"
	if got, ready := tracker.Observe(changed, start.Add(2100*time.Millisecond), policy); ready || got.ContentSettledSeen || got.SettledFor != "0s" {
		t.Fatalf("late change = %+v, ready=%v; want settle reset", got, ready)
	}
	if got, ready := tracker.Observe(changed, start.Add(4*time.Second), policy); ready || got.ContentSettledSeen {
		t.Fatalf("sample 1.9s after reset = %+v, ready=%v; want quiet window open", got, ready)
	}
	if got, ready := tracker.Observe(changed, start.Add(4500*time.Millisecond), policy); !ready || !got.ContentSettledSeen {
		t.Fatalf("sample 2.4s after reset = %+v, ready=%v; want settled", got, ready)
	}
}

func TestRenderedExtractReadinessDOMStableStillRequiresEnabledThresholds(t *testing.T) {
	now := time.Unix(2_750, 0)
	tracker := renderedExtractReadinessTracker{}
	got, ready := tracker.Observe(renderedExtractReadiness{
		URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true,
		SelectedWordCount: 1, SelectedHTMLLength: 128, DOMSignature: "v2:shell",
	}, now, renderedExtractReadinessPolicy{WaitUntil: "dom-stable", MinWords: 5, MinHTMLChars: 64, Settle: 0})
	if ready || got.ContentSettledSeen || got.ThresholdsMet {
		t.Fatalf("dom-stable subthreshold shell = %+v, ready=%v; enabled thresholds must gate settling", got, ready)
	}
}

func TestRenderedExtractReadinessZeroThresholdDisablesOnlyThatThreshold(t *testing.T) {
	now := time.Unix(3_000, 0)
	sample := renderedExtractReadiness{
		URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true,
		SelectedHTMLLength: 128, DOMSignature: "v2:html-only", NavigatedFromAboutBlank: true, LoadSeen: true,
	}
	tracker := renderedExtractReadinessTracker{}
	got, ready := tracker.Observe(sample, now, renderedExtractReadinessPolicy{
		WaitUntil: "useful-content", MinWords: 0, MinHTMLChars: 64, Settle: 0,
	})
	if !ready || !got.ThresholdsMet || !got.ContentSettledSeen {
		t.Fatalf("zero word threshold readiness = %+v, ready=%v; want HTML threshold only", got, ready)
	}
}

func TestRenderedExtractQualityGateIsConjunctiveAndSupportsDisabledThresholds(t *testing.T) {
	if renderedExtractQualityPassed(3, 10, 1_347, 5, 5, 64) {
		t.Fatal("quality gate passed with subthreshold visible words")
	}
	if !renderedExtractQualityPassed(3, 10, 1_347, 0, 5, 64) {
		t.Fatal("quality gate did not honor disabled visible-word threshold")
	}
	if !renderedExtractQualityPassed(10, 10, 1_347, 5, 5, 64) {
		t.Fatal("quality gate rejected content satisfying every enabled threshold")
	}
}

func TestRenderedExtractPostCaptureQualityRequiresVerifiedConsistency(t *testing.T) {
	if renderedExtractPostCaptureQualityPassed(true, renderedExtractReadiness{Outcome: "settled"}) {
		t.Fatal("quality gate passed without a completed consistency check")
	}
	if renderedExtractPostCaptureQualityPassed(true, renderedExtractReadiness{Outcome: "settled", CaptureConsistencyChecked: true}) {
		t.Fatal("quality gate passed with a detected capture mismatch")
	}
	if renderedExtractPostCaptureQualityPassed(false, renderedExtractReadiness{Outcome: "settled", CaptureConsistencyChecked: true, CaptureConsistent: true}) {
		t.Fatal("quality gate passed with failed content thresholds")
	}
	if renderedExtractPostCaptureQualityPassed(true, renderedExtractReadiness{Outcome: "wait_expired", CaptureConsistencyChecked: true, CaptureConsistent: true}) {
		t.Fatal("quality gate passed after the readiness deadline expired")
	}
	if !renderedExtractPostCaptureQualityPassed(true, renderedExtractReadiness{Outcome: "settled", CaptureConsistencyChecked: true, CaptureConsistent: true}) {
		t.Fatal("quality gate rejected verified, threshold-passing content")
	}
}

func TestRenderedExtractReadinessExpressionUsesVersionedContentFingerprint(t *testing.T) {
	expression := renderedExtractReadinessExpression("body")
	for _, want := range []string{"const hash =", "v2|", "hash(normalize(text))", "hash(html)"} {
		if !strings.Contains(expression, want) {
			t.Fatalf("readiness expression missing %q", want)
		}
	}
}

func TestWaitForRenderedExtractReadinessZeroWaitSamplesOnce(t *testing.T) {
	collectCount := 0
	got, err := waitForRenderedExtractReadinessFunc(context.Background(), func(context.Context, string) (renderedExtractReadiness, error) {
		collectCount++
		return renderedExtractReadiness{URL: "https://example.test/app", DocumentReadyState: "loading", DOMSignature: "v2:shell"}, nil
	}, "body", 0, 2*time.Second, "useful-content", 5, 64, time.Second)
	if err != nil {
		t.Fatalf("immediate readiness returned error: %v", err)
	}
	if collectCount != 1 || got.PollCount != 1 || got.Outcome != "immediate" || got.ContentSettledSeen {
		t.Fatalf("immediate readiness = %+v, collects=%d; want exactly one non-settled sample", got, collectCount)
	}
}

func TestWaitForRenderedExtractReadinessReportsDeadlineState(t *testing.T) {
	t.Run("thresholds unmet", func(t *testing.T) {
		got, err := waitForRenderedExtractReadinessFunc(context.Background(), func(context.Context, string) (renderedExtractReadiness, error) {
			return renderedExtractReadiness{URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true, SelectedWordCount: 1, SelectedHTMLLength: 128, DOMSignature: "v2:shell"}, nil
		}, "body", 5*time.Millisecond, 2*time.Millisecond, "useful-content", 5, 64, 20*time.Millisecond)
		if err != nil || got.Outcome != "wait_expired" || got.ThresholdsMet || got.ContentSettledSeen || got.PollCount < 1 {
			t.Fatalf("deadline readiness = %+v, err=%v", got, err)
		}
	})

	t.Run("thresholds met but changing", func(t *testing.T) {
		collectCount := 0
		got, err := waitForRenderedExtractReadinessFunc(context.Background(), func(context.Context, string) (renderedExtractReadiness, error) {
			collectCount++
			return renderedExtractReadiness{URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true, SelectedWordCount: 10, SelectedHTMLLength: 128, DOMSignature: "v2:change-" + string(rune('a'+collectCount))}, nil
		}, "body", 5*time.Millisecond, 5*time.Millisecond, "useful-content", 5, 64, 20*time.Millisecond)
		if err != nil || got.Outcome != "wait_expired" || !got.ThresholdsMet || got.ContentSettledSeen || got.PollCount < 1 {
			t.Fatalf("changing deadline readiness = %+v, err=%v", got, err)
		}
	})
}

func TestWaitForRenderedExtractReadinessDeadlineWinsOverSlowReadySample(t *testing.T) {
	got, err := waitForRenderedExtractReadinessFunc(context.Background(), func(context.Context, string) (renderedExtractReadiness, error) {
		time.Sleep(20 * time.Millisecond)
		return renderedExtractReadiness{
			URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true,
			SelectedWordCount: 10, SelectedHTMLLength: 128, DOMSignature: "v2:ready",
		}, nil
	}, "body", time.Millisecond, 0, "useful-content", 5, 64, time.Second)
	if err != nil {
		t.Fatalf("slow ready sample returned error: %v", err)
	}
	if got.Outcome != "wait_expired" {
		t.Fatalf("slow ready sample outcome = %q, want wait_expired after hard deadline", got.Outcome)
	}
}

func TestWaitForRenderedExtractReadinessDeadlineBoundsBlockedCollector(t *testing.T) {
	started := time.Now()
	got, err := waitForRenderedExtractReadinessFunc(context.Background(), func(context.Context, string) (renderedExtractReadiness, error) {
		time.Sleep(250 * time.Millisecond)
		return renderedExtractReadiness{
			URL: "https://example.test/app", DocumentReadyState: "complete", SelectorMatched: true,
			SelectedWordCount: 10, SelectedHTMLLength: 128, DOMSignature: "v2:ready",
		}, nil
	}, "body", 10*time.Millisecond, 0, "useful-content", 5, 64, time.Second)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("blocked readiness collector returned error: %v", err)
	}
	if got.Outcome != "wait_expired" {
		t.Fatalf("blocked readiness outcome = %q, want wait_expired", got.Outcome)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("blocked readiness elapsed = %s, want hard deadline below 100ms", elapsed)
	}
}

func TestWaitForRenderedExtractReadinessHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitForRenderedExtractReadinessFunc(ctx, func(context.Context, string) (renderedExtractReadiness, error) {
		return renderedExtractReadiness{URL: "https://example.test/app", DOMSignature: "v2:shell"}, nil
	}, "body", time.Second, 100*time.Millisecond, "useful-content", 5, 64, time.Second)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("canceled readiness error = %v", err)
	}
}

func TestWaitForRenderedExtractReadinessBoundsActiveCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := time.Now()
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := waitForRenderedExtractReadinessFunc(ctx, func(context.Context, string) (renderedExtractReadiness, error) {
		time.Sleep(250 * time.Millisecond)
		return renderedExtractReadiness{URL: "https://example.test/app", DOMSignature: "v2:shell"}, nil
	}, "body", time.Second, 100*time.Millisecond, "useful-content", 5, 64, time.Second)
	elapsed := time.Since(started)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("active cancellation error = %v", err)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("active cancellation elapsed = %s, want below 100ms", elapsed)
	}
}

func TestApplyRenderedExtractCaptureConsistencyDetectsChange(t *testing.T) {
	readiness := renderedExtractReadiness{DOMSignature: "v2:before", NavigatedFromAboutBlank: true, SelectorMatched: true, Outcome: "settled"}
	applyRenderedExtractCaptureConsistency(&readiness, renderedExtractReadiness{DOMSignature: "v2:after"})
	if !readiness.CaptureConsistencyChecked || readiness.CaptureConsistent {
		t.Fatalf("capture consistency = %+v, want checked change", readiness)
	}
	if renderedExtractPostCaptureQualityPassed(true, readiness) {
		t.Fatal("post-capture quality passed after a detected content change")
	}
	warnings := renderedExtractWarnings(readiness, "useful content", 1, 2, 128, 2, 0, 0, 0, 0, "none")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "changed while capture") {
		t.Fatalf("capture consistency warnings = %v", warnings)
	}
}

func TestRenderedExtractWorkflowPartialIncludesFailedQuality(t *testing.T) {
	if !renderedExtractWorkflowPartial(false, nil) {
		t.Fatal("failed quality must mark the workflow partial")
	}
	if !renderedExtractWorkflowPartial(true, []map[string]string{{"collector": "capture_consistency", "error": "changed"}}) {
		t.Fatal("collector error must mark the workflow partial")
	}
	if renderedExtractWorkflowPartial(true, nil) {
		t.Fatal("clean quality without collector errors must not be partial")
	}
}
