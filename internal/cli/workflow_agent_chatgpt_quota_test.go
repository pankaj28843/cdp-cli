package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const quotaFixtureScope = "Plan limits\nShared across Codex, Work, Workspace Agents, and ChatGPT for Excel. Chat conversations are not included.\nWeekly limit\nResets in 5d 13h\n19% left"

func fixtureQuotaReading(row string) quotaPageReading {
	return quotaPageReading{
		URL: chatgptQuotaURL, SectionCount: 1, RowCount: 1,
		SectionText: quotaFixtureScope, RowText: row, TimeOriginMS: 1234,
	}
}

func TestParseQuotaReadingPreservesRelativeResetAndScope(t *testing.T) {
	now := time.Date(2026, 10, 25, 5, 40, 0, 0, time.UTC)
	snapshot, resetIn, err := parseQuotaReading(fixtureQuotaReading("Weekly limit\nResets in 5d 13h\n19% left"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.OK || snapshot.RemainingPercent != 19 || snapshot.RemainingUnit != "percent" ||
		snapshot.QuotaScope != chatgptQuotaScope || snapshot.ResetDisplay != "Resets in 5d 13h" ||
		snapshot.ResetAt != nil || snapshot.ResetTimezone != nil || resetIn != 5*24*time.Hour+13*time.Hour {
		t.Fatalf("unexpected parsed snapshot: %+v, resetIn=%v", snapshot, resetIn)
	}
	for _, reading := range []quotaPageReading{
		func() quotaPageReading {
			r := fixtureQuotaReading("Weekly limit\nResets in 5d 13h\n19% left")
			r.RowCount = 2
			return r
		}(),
		func() quotaPageReading {
			r := fixtureQuotaReading("Weekly limit\nResets in 5d 13h\n19% left")
			r.SectionText = "Plan limits\nChat conversations\nWeekly limit"
			return r
		}(),
		fixtureQuotaReading("Weekly limit\nResets tomorrow\n19% left"),
		fixtureQuotaReading("Weekly limit\nResets in 5d 13h\n119% left"),
		fixtureQuotaReading("Weekly limit\nResets in 5d 13h\n19 credits left"),
		func() quotaPageReading {
			r := fixtureQuotaReading("Weekly limit\nResets in 5d 13h\n19% left")
			r.URL = "https://evil.example/settings/usage"
			return r
		}(),
	} {
		if _, _, err := parseQuotaReading(reading, now); err == nil {
			t.Fatalf("ambiguous or changed reading was accepted: %+v", reading)
		}
	}
}

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
