package tripadvisor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
)

func TestSessionPollDelayUsesCappedLinearBackoff(t *testing.T) {
	const poll = 250 * time.Millisecond
	const ampleTime = 10 * time.Second

	tests := []struct {
		name      string
		attempt   int
		remaining time.Duration
		want      time.Duration
	}{
		{name: "first attempt", attempt: 1, remaining: ampleTime, want: poll},
		{name: "second attempt", attempt: 2, remaining: ampleTime, want: 2 * poll},
		{name: "third attempt", attempt: 3, remaining: ampleTime, want: 3 * poll},
		{name: "fourth attempt", attempt: 4, remaining: ampleTime, want: 4 * poll},
		{name: "later attempt stays capped", attempt: 12, remaining: ampleTime, want: 4 * poll},
		{name: "delay respects remaining deadline", attempt: 4, remaining: 600 * time.Millisecond, want: 600 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionPollDelay(poll, tt.attempt, tt.remaining); got != tt.want {
				t.Fatalf("sessionPollDelay(%s, %d, %s) = %s, want %s", poll, tt.attempt, tt.remaining, got, tt.want)
			}
		})
	}
}

func TestSessionRecoveryKeepsLateHardReloadErrorInconclusive(t *testing.T) {
	t.Parallel()

	observationErr := errors.New("late CDP observation failed")
	client := testsupport.NewBrowser("target-1")
	hardReloadObservations := 0
	client.Evaluate = func(
		expression string,
		browser *testsupport.Browser,
	) (any, error) {
		if !strings.Contains(expression, "origin_ready") {
			return map[string]any{}, nil
		}
		if len(browser.Reloads) == 2 {
			hardReloadObservations++
			if hardReloadObservations > 1 {
				return nil, observationErr
			}
		}
		return map[string]any{
			"origin_ready":       true,
			"panel_count":        0,
			"panel_ready":        false,
			"composer_count":     0,
			"composer_ready":     false,
			"history_count":      0,
			"history_ready":      false,
			"sign_in_visible":    false,
			"panel_opener_count": 0,
			"panel_opener_ready": false,
			"new_chat_count":     0,
			"new_chat_ready":     false,
		}, nil
	}
	session, err := cdp.AttachToTargetWithClient(
		context.Background(),
		client,
		"target-1",
		nil,
	)
	if err != nil {
		t.Fatalf("AttachToTargetWithClient: %v", err)
	}
	defer func() {
		if closeErr := session.Close(context.Background()); closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
	}()

	_, _, _, _, readiness, recoveryErr := ensureSessionWithRecovery(
		context.Background(),
		session,
		120*time.Millisecond,
		time.Millisecond,
		false,
	)
	if recoveryErr != nil {
		t.Fatalf("ensureSessionWithRecovery: %v", recoveryErr)
	}
	if !readiness.ObservationFailed() ||
		readiness.EvidenceAbsent() ||
		!errors.Is(readiness.LastObservationError, observationErr) ||
		readiness.Stage != authreadiness.StageHardReload ||
		readiness.StageObservations != 1 {
		t.Fatalf("readiness = %+v", readiness)
	}
}
