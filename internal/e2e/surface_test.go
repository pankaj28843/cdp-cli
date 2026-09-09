package e2e

import (
	"context"
	"os"
	"os/signal"
	"slices"
	"testing"
	"time"
)

func TestSurfaceGateChecksEveryProviderAndPreservesExactTabSet(t *testing.T) {
	var commands [][]string
	pageCalls := 0
	runner := func(
		_ context.Context,
		command []string,
		_ time.Duration,
	) commandOutcome {
		commands = append(commands, slices.Clone(command))
		if command[0] == "cdp" {
			pageCalls++
			return commandOutcome{
				payload: map[string]any{
					"ok": true,
					"pages": []any{
						map[string]any{"id": "user-tab"},
						map[string]any{"id": "second-tab"},
					},
					"budget": map[string]any{"max_tabs": float64(15)},
				},
			}
		}
		return commandOutcome{payload: map[string]any{"ok": true}}
	}

	report, err := run(context.Background(), runner)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || !report.CommandsOK || !report.TabSetPreserved {
		t.Fatalf("surface report = %+v", report)
	}
	if pageCalls != 2 {
		t.Fatalf("page snapshots = %d, want 2", pageCalls)
	}
	if len(report.Results) != 7 {
		t.Fatalf("surface commands = %d, want 7", len(report.Results))
	}
	seen := make(map[string]bool)
	for _, result := range report.Results {
		seen[result.Provider] = true
	}
	for _, provider := range []string{
		"alex",
		"chatgpt",
		"gemini",
		"perplexity",
		"claude",
		"grok",
		"tripadvisor",
	} {
		if !seen[provider] {
			t.Fatalf("provider %s was not checked", provider)
		}
	}
}

func TestSurfaceGateReportsExactLeakWithoutMaskingCommandHealth(t *testing.T) {
	pageCalls := 0
	runner := func(
		_ context.Context,
		command []string,
		_ time.Duration,
	) commandOutcome {
		if command[0] != "cdp" {
			return commandOutcome{payload: map[string]any{"ok": true}}
		}
		pageCalls++
		pages := []any{map[string]any{"id": "user-tab"}}
		if pageCalls == 2 {
			pages = append(pages, map[string]any{"id": "leaked-tab"})
		}
		return commandOutcome{payload: map[string]any{
			"ok":    true,
			"pages": pages,
		}}
	}

	report, err := run(context.Background(), runner)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || !report.CommandsOK || report.TabSetPreserved {
		t.Fatalf("surface report = %+v", report)
	}
	if !slices.Equal(report.LeakedTargetIDs, []string{"leaked-tab"}) {
		t.Fatalf("leaked ids = %v", report.LeakedTargetIDs)
	}
}

func TestSurfaceGateRequiresAnOpenHeadedTab(t *testing.T) {
	runner := func(
		_ context.Context,
		command []string,
		_ time.Duration,
	) commandOutcome {
		if command[0] == "cdp" {
			return commandOutcome{payload: map[string]any{
				"ok":    true,
				"pages": []any{},
			}}
		}
		return commandOutcome{payload: map[string]any{"ok": true}}
	}

	if _, err := run(context.Background(), runner); err == nil {
		t.Fatal("surface gate accepted headed CDP with no open tabs")
	}
}

func TestTimedOutCommandInterruptsAndReapsOwnedProcess(t *testing.T) {
	if os.Getenv("CDP_CLI_E2E_HELPER") == "wait-for-signal" {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		<-signals
		os.Exit(0)
	}
	t.Setenv("CDP_CLI_E2E_HELPER", "wait-for-signal")
	outcome := runJSONCommand(
		context.Background(),
		[]string{os.Args[0], "-test.run=TestTimedOutCommandInterruptsAndReapsOwnedProcess"},
		100*time.Millisecond,
	)
	if outcome.returnCode != 124 {
		t.Fatalf("timeout return code = %d, want 124", outcome.returnCode)
	}
	if outcome.payload["error_type"] != "timeout" {
		t.Fatalf("timeout payload = %+v", outcome.payload)
	}
}
