package transcriptionapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	availabilitySchemaVersion = "cdp-transcription-availability/v1"
	availabilityWindow        = 7 * 24 * time.Hour
	availabilityInterval      = time.Minute
)

type AvailabilitySummary struct {
	WindowStartedAt       string  `json:"window_started_at"`
	WindowSeconds         int64   `json:"window_seconds"`
	ObservedSeconds       int64   `json:"observed_seconds"`
	AvailableSeconds      int64   `json:"available_seconds"`
	AvailabilityPercent   float64 `json:"availability_percent"`
	ProcessStartedAt      string  `json:"process_started_at"`
	ProcessUptimeSeconds  int64   `json:"process_uptime_seconds"`
	LastHeartbeatAt       string  `json:"last_heartbeat_at"`
	SampleIntervalSeconds int64   `json:"sample_interval_seconds"`
	ServiceStarts         int     `json:"service_starts"`
}

type availabilityStateDocument struct {
	SchemaVersion string                    `json:"schema_version"`
	Periods       []availabilityPeriodState `json:"periods"`
}

type availabilityPeriodState struct {
	StartedAt  string `json:"started_at"`
	LastSeenAt string `json:"last_seen_at"`
}

type availabilityPeriod struct {
	startedAt  time.Time
	lastSeenAt time.Time
}

type availabilityTracker struct {
	mu               sync.Mutex
	path             string
	now              func() time.Time
	processStartedAt time.Time
	periods          []availabilityPeriod
	startOnce        sync.Once
	stopOnce         sync.Once
	cancel           context.CancelFunc
	done             chan struct{}
	logger           *slog.Logger
}

func newAvailabilityTracker(path string, now func() time.Time, logger *slog.Logger) *availabilityTracker {
	if now == nil {
		now = time.Now
	}
	startedAt := now().UTC()
	periods := loadAvailabilityPeriods(path, startedAt.Add(-availabilityWindow))
	periods = append(periods, availabilityPeriod{startedAt: startedAt, lastSeenAt: startedAt})
	return &availabilityTracker{
		path:             strings.TrimSpace(path),
		now:              now,
		processStartedAt: startedAt,
		periods:          periods,
		logger:           logger,
	}
}

func (t *availabilityTracker) start(ctx context.Context) {
	if t == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	t.startOnce.Do(func() {
		var trackerContext context.Context
		trackerContext, t.cancel = context.WithCancel(ctx)
		t.done = make(chan struct{})
		t.persistWithLog()
		go func() {
			defer close(t.done)
			ticker := time.NewTicker(availabilityInterval)
			defer ticker.Stop()
			for {
				select {
				case <-trackerContext.Done():
					t.persistWithLog()
					return
				case <-ticker.C:
					t.persistWithLog()
				}
			}
		}()
	})
}

func (t *availabilityTracker) stop() {
	if t == nil {
		return
	}
	t.stopOnce.Do(func() {
		if t.cancel == nil {
			t.persistWithLog()
			return
		}
		t.cancel()
		<-t.done
	})
}

func (t *availabilityTracker) persistWithLog() {
	if err := t.persist(); err != nil && t.logger != nil {
		t.logger.Error(
			"persist transcription availability",
			"event", "transcription.availability.persist_failed",
			"error", err,
		)
	}
}

func (t *availabilityTracker) snapshot() AvailabilitySummary {
	if t == nil {
		return AvailabilitySummary{}
	}
	now := t.now().UTC()
	t.mu.Lock()
	periods := t.currentPeriodsLocked(now)
	startedAt := t.processStartedAt
	t.mu.Unlock()
	return summarizeAvailability(periods, startedAt, now)
}

func (t *availabilityTracker) persist() error {
	if t == nil || t.path == "" {
		return nil
	}
	now := t.now().UTC()
	t.mu.Lock()
	periods := t.currentPeriodsLocked(now)
	t.periods = append([]availabilityPeriod(nil), periods...)
	t.mu.Unlock()

	state := availabilityStateDocument{
		SchemaVersion: availabilitySchemaVersion,
		Periods:       make([]availabilityPeriodState, 0, len(periods)),
	}
	for _, period := range periods {
		state.Periods = append(state.Periods, availabilityPeriodState{
			StartedAt:  period.startedAt.Format(time.RFC3339Nano),
			LastSeenAt: period.lastSeenAt.Format(time.RFC3339Nano),
		})
	}
	return writePrivateJSON(t.path, state)
}

func (t *availabilityTracker) currentPeriodsLocked(now time.Time) []availabilityPeriod {
	if len(t.periods) == 0 {
		return nil
	}
	periods := append([]availabilityPeriod(nil), t.periods...)
	periods[len(periods)-1].lastSeenAt = now
	cutoff := now.Add(-availabilityWindow)
	kept := periods[:0]
	for _, period := range periods {
		if !period.lastSeenAt.Before(cutoff) {
			kept = append(kept, period)
		}
	}
	return kept
}

func summarizeAvailability(periods []availabilityPeriod, processStartedAt, now time.Time) AvailabilitySummary {
	windowStart := now
	cutoff := now.Add(-availabilityWindow)
	for _, period := range periods {
		if period.startedAt.Before(windowStart) {
			windowStart = period.startedAt
		}
	}
	if windowStart.Before(cutoff) {
		windowStart = cutoff
	}
	observed := nonNegativeDuration(now.Sub(windowStart))

	clipped := make([]availabilityPeriod, 0, len(periods))
	for _, period := range periods {
		start := maxTime(period.startedAt, windowStart)
		end := minTime(period.lastSeenAt, now)
		if end.After(start) {
			clipped = append(clipped, availabilityPeriod{startedAt: start, lastSeenAt: end})
		}
	}
	sort.Slice(clipped, func(i, j int) bool { return clipped[i].startedAt.Before(clipped[j].startedAt) })
	available := time.Duration(0)
	for index := 0; index < len(clipped); {
		start := clipped[index].startedAt
		end := clipped[index].lastSeenAt
		index++
		for index < len(clipped) && !clipped[index].startedAt.After(end) {
			end = maxTime(end, clipped[index].lastSeenAt)
			index++
		}
		available += end.Sub(start)
	}
	percent := 100.0
	if observed > 0 {
		percent = math.Round((float64(available)/float64(observed))*10_000) / 100
	}
	return AvailabilitySummary{
		WindowStartedAt:       windowStart.Format(time.RFC3339Nano),
		WindowSeconds:         int64(availabilityWindow / time.Second),
		ObservedSeconds:       int64(observed / time.Second),
		AvailableSeconds:      int64(available / time.Second),
		AvailabilityPercent:   percent,
		ProcessStartedAt:      processStartedAt.Format(time.RFC3339Nano),
		ProcessUptimeSeconds:  int64(nonNegativeDuration(now.Sub(processStartedAt)) / time.Second),
		LastHeartbeatAt:       now.Format(time.RFC3339Nano),
		SampleIntervalSeconds: int64(availabilityInterval / time.Second),
		ServiceStarts:         len(periods),
	}
}

func loadAvailabilityPeriods(path string, cutoff time.Time) []availabilityPeriod {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil
	}
	var state availabilityStateDocument
	if json.Unmarshal(data, &state) != nil || state.SchemaVersion != availabilitySchemaVersion {
		return nil
	}
	periods := make([]availabilityPeriod, 0, len(state.Periods))
	for _, item := range state.Periods {
		startedAt, startErr := time.Parse(time.RFC3339Nano, item.StartedAt)
		lastSeenAt, seenErr := time.Parse(time.RFC3339Nano, item.LastSeenAt)
		if startErr != nil || seenErr != nil || lastSeenAt.Before(startedAt) || lastSeenAt.Before(cutoff) {
			continue
		}
		periods = append(periods, availabilityPeriod{startedAt: startedAt.UTC(), lastSeenAt: lastSeenAt.UTC()})
	}
	return periods
}

func maxTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}
