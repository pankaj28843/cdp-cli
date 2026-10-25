package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
)

func TestQuotaGuardBaselineResetAndStopBoundaries(t *testing.T) {
	start := time.Date(2026, 10, 25, 5, 40, 0, 0, time.UTC)
	snapshot := quotaSnapshot{ObservedAt: start, RemainingPercent: 19}
	state, decision, err := applyQuotaGuard(snapshot, 5*24*time.Hour+13*time.Hour, nil, 1, 90)
	if err != nil || decision.State != "allow" || decision.Phase != "before_reset" {
		t.Fatalf("baseline: %+v, %+v, %v", state, decision, err)
	}
	snapshot.ObservedAt = start.Add(time.Hour)
	snapshot.RemainingPercent = 1
	state, decision, err = applyQuotaGuard(snapshot, 5*24*time.Hour+12*time.Hour, &state, 1, 90)
	if err != nil || decision.State != "stop" || decision.Phase != "before_reset" {
		t.Fatalf("pre-reset stop: %+v, %+v, %v", state, decision, err)
	}
	snapshot.ObservedAt = start.Add(5*24*time.Hour + 14*time.Hour)
	snapshot.RemainingPercent = 100
	state, decision, err = applyQuotaGuard(snapshot, 6*24*time.Hour+23*time.Hour, &state, 1, 90)
	if err != nil || decision.State != "allow" || decision.Phase != "after_reset" ||
		decision.ResetEvidence != "countdown_jump_and_remaining_increase_inferred" {
		t.Fatalf("reset transition: %+v, %+v, %v", state, decision, err)
	}
	snapshot.ObservedAt = snapshot.ObservedAt.Add(time.Hour)
	snapshot.RemainingPercent = 90
	_, decision, err = applyQuotaGuard(snapshot, 6*24*time.Hour+22*time.Hour, &state, 1, 90)
	if err != nil || decision.State != "stop" || decision.Phase != "after_reset" {
		t.Fatalf("post-reset stop: %+v, %v", decision, err)
	}
}

func TestQuotaGuardAmbiguousResetFailsClosedAndStateIsPrivate(t *testing.T) {
	start := time.Date(2026, 10, 25, 5, 40, 0, 0, time.UTC)
	previous := quotaGuardState{
		SchemaVersion: chatgptQuotaSchemaVersion, QuotaScope: chatgptQuotaScope,
		Phase: "before_reset", ObservedAt: start, RemainingPercent: 19,
		ResetInSeconds: int64(5*24*time.Hour/time.Second + 13*time.Hour/time.Second),
	}
	current := quotaSnapshot{ObservedAt: start.Add(time.Hour), RemainingPercent: 100}
	if _, _, err := applyQuotaGuard(current, 5*24*time.Hour+12*time.Hour, &previous, 1, 90); err == nil {
		t.Fatal("remaining percent increase without reset countdown jump was accepted")
	}
	for _, invalid := range []quotaGuardState{
		func() quotaGuardState { s := previous; s.RemainingPercent = -1; return s }(),
		func() quotaGuardState { s := previous; s.RemainingPercent = 101; return s }(),
		func() quotaGuardState {
			s := previous
			s.ResetInSeconds = int64(9 * 24 * time.Hour / time.Second)
			return s
		}(),
		func() quotaGuardState {
			s := previous
			s.ObservedAt = s.ObservedAt.In(time.FixedZone("other", 3600))
			return s
		}(),
	} {
		if _, _, err := applyQuotaGuard(current, 5*24*time.Hour, &invalid, 1, 90); err == nil {
			t.Fatalf("malformed persisted guard state was accepted: %+v", invalid)
		}
	}
	if _, _, err := applyQuotaGuard(current, 7*24*time.Hour, &previous, 1, 90); err != nil {
		t.Fatalf("countdown jump plus remaining increase should infer reset: %v", err)
	}
	current.ObservedAt = start.Add(9 * 24 * time.Hour)
	if _, _, err := applyQuotaGuard(current, 5*24*time.Hour, &previous, 1, 90); err == nil {
		t.Fatal("gap spanning a weekly cycle was accepted")
	}
	path := filepath.Join(t.TempDir(), "private", "quota.json")
	if err := saveQuotaGuard(path, previous); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("guard state permissions: %v", info.Mode().Perm())
	}
	loaded, err := loadQuotaGuard(path)
	if err != nil || loaded == nil || loaded.RemainingPercent != 19 {
		t.Fatalf("private guard state round trip: %+v, %v", loaded, err)
	}
	var raw map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["target_id"]; ok {
		t.Fatal("private guard state should not persist target identifiers")
	}
}

func TestQuotaExplicitTargetValidationNeverCreatesTab(t *testing.T) {
	fake := &keepOpenPolicyFakeClient{targets: map[string]cdp.TargetInfo{
		"usage": {TargetID: "usage", Type: "page", URL: "https://chatgpt.com/settings/usage?tab=overview"},
		"other": {TargetID: "other", Type: "page", URL: "https://example.test/"},
	}, pendingClose: map[string]int{}}
	a := newKeepOpenPolicyTestApp(t)
	id, err := a.validateQuotaTarget(context.Background(), fake, "usage")
	if err != nil || id != "usage" {
		t.Fatalf("valid explicit target: %q %v", id, err)
	}
	for _, targetID := range []string{"other", "missing"} {
		if _, err := a.validateQuotaTarget(context.Background(), fake, targetID); err == nil {
			t.Fatalf("invalid explicit target %q was accepted", targetID)
		}
	}
	if len(fake.targets) != 2 || len(fake.events) != 0 {
		t.Fatalf("target validation mutated tabs: targets=%+v events=%+v", fake.targets, fake.events)
	}
}

func TestQuotaCachePreservesObservationAndExpiresAtTwoMinutes(t *testing.T) {
	observed := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	reset := observed.Add(6 * 24 * time.Hour)
	zone := "UTC"
	snapshot := quotaSnapshot{OK: true, SchemaVersion: chatgptQuotaSchemaVersion,
		QuotaScope: chatgptQuotaScope, RemainingPercent: 95, RemainingUnit: "percent",
		ResetAt: &reset, ResetTimezone: &zone, ObservedAt: observed,
		Source: "direct_chatgpt_usage_api_weekly_window", SourceURL: chatgpt.Origin + chatgpt.UsagePath}
	path := quotaCachePath(t.TempDir())
	if err := saveQuotaCache(path, "synthetic-account-scope", snapshot, 6*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("owner-only quota cache: %v %v", info, err)
	}
	cached, resetIn, hit := loadQuotaCache(path, "synthetic-account-scope", observed.Add(119*time.Second))
	if !hit || !cached.CacheHit || cached.CacheAgeMS != 119000 || !cached.ObservedAt.Equal(observed) || resetIn != 6*24*time.Hour {
		t.Fatalf("cache changed source observation: %+v reset=%v hit=%v", cached, resetIn, hit)
	}
	if _, _, hit := loadQuotaCache(path, "synthetic-account-scope", observed.Add(120*time.Second)); hit {
		t.Fatal("two-minute-old reading was served")
	}
	if _, _, hit := loadQuotaCache(path, "different-account-scope", observed.Add(time.Second)); hit {
		t.Fatal("other auth scope reused quota")
	}
	if !cachedQuotaBehindGuard(cached, &quotaGuardState{ObservedAt: observed.Add(time.Second)}) ||
		cachedQuotaBehindGuard(cached, &quotaGuardState{ObservedAt: observed}) {
		t.Fatal("cache did not respect newer guard observation")
	}
}
