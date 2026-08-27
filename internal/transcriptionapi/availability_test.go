package transcriptionapi

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAvailabilityTrackerSummarizesRollingWindowAndRestartGap(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "availability.json")
	state := `{
  "schema_version": "cdp-transcription-availability/v1",
  "periods": [
    {
      "started_at": "2026-08-20T12:00:00Z",
      "last_seen_at": "2026-08-23T12:00:00Z"
    }
  ]
}
`
	if err := os.WriteFile(statePath, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	tracker := newAvailabilityTracker(statePath, func() time.Time { return now }, nil)
	now = now.Add(24 * time.Hour)
	summary := tracker.snapshot()

	if summary.WindowSeconds != int64((7*24*time.Hour)/time.Second) {
		t.Fatalf("window seconds = %d, want one week", summary.WindowSeconds)
	}
	if summary.ObservedSeconds != summary.WindowSeconds {
		t.Fatalf("observed seconds = %d, want %d", summary.ObservedSeconds, summary.WindowSeconds)
	}
	wantAvailable := int64((3 * 24 * time.Hour) / time.Second)
	if summary.AvailableSeconds != wantAvailable {
		t.Fatalf("available seconds = %d, want %d", summary.AvailableSeconds, wantAvailable)
	}
	if summary.AvailabilityPercent < 42.85 || summary.AvailabilityPercent > 42.86 {
		t.Fatalf("availability percent = %f, want 42.86", summary.AvailabilityPercent)
	}
	if summary.ProcessUptimeSeconds != int64((24*time.Hour)/time.Second) {
		t.Fatalf("process uptime seconds = %d, want one day", summary.ProcessUptimeSeconds)
	}
	if summary.ServiceStarts != 2 {
		t.Fatalf("service starts = %d, want 2", summary.ServiceStarts)
	}
	if summary.SampleIntervalSeconds != 60 {
		t.Fatalf("sample interval seconds = %d, want 60", summary.SampleIntervalSeconds)
	}
}

func TestAvailabilityTrackerPersistsPrivateStateAcrossRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "availability.json")
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	first := newAvailabilityTracker(statePath, func() time.Time { return now }, nil)
	now = now.Add(2 * time.Hour)
	if err := first.persist(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("availability mode = %o, want 600", info.Mode().Perm())
	}

	now = now.Add(time.Hour)
	second := newAvailabilityTracker(statePath, func() time.Time { return now }, nil)
	now = now.Add(time.Hour)
	summary := second.snapshot()
	if summary.ServiceStarts != 2 {
		t.Fatalf("service starts after reload = %d, want 2", summary.ServiceStarts)
	}
	wantObserved := int64((4 * time.Hour) / time.Second)
	wantAvailable := int64((3 * time.Hour) / time.Second)
	if summary.ObservedSeconds != wantObserved || summary.AvailableSeconds != wantAvailable {
		t.Fatalf("availability after reload = %+v, want observed=%d available=%d", summary, wantObserved, wantAvailable)
	}
}
