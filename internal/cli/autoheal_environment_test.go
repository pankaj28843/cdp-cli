package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/availability"
)

func TestKeepaliveSkipsBeforeChromeRepairWhenEnvironmentIsOffline(t *testing.T) {
	previous := autoHealEnvironmentCheck
	autoHealEnvironmentCheck = func(context.Context, availability.Options) (availability.Result, error) {
		return availability.Result{
			Allowed:   false,
			State:     "offline",
			Network:   "offline",
			Reason:    "connectivity_probe_failed",
			CheckedAt: "2026-08-26T12:00:00Z",
		}, nil
	}
	t.Cleanup(func() { autoHealEnvironmentCheck = previous })

	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"--browser-mode", "headed",
		"--state-dir", t.TempDir(),
		"daemon", "keepalive",
		"--auto-connect",
		"--chrome-command", "/path/that/does-not-exist",
		"--json",
	}, &stdout, &stderr, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("keepalive exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, stdout.String(), stderr.String())
	}
	var got struct {
		State       string                 `json:"state"`
		Action      string                 `json:"action"`
		Environment availability.Result    `json:"environment"`
		Chrome      struct{ Skipped bool } `json:"chrome"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("keepalive output is invalid JSON: %v; output=%s", err, stdout.String())
	}
	if got.State != "environment_unavailable" || got.Action != "skipped" || got.Environment.Reason != "connectivity_probe_failed" || !got.Chrome.Skipped {
		t.Fatalf("keepalive result = %+v, want offline safe skip", got)
	}
}

func TestHeadlessMaintenanceSkipsAllMutatingPhasesWhenEnvironmentIsUnavailable(t *testing.T) {
	previous := autoHealEnvironmentCheck
	var checks int
	autoHealEnvironmentCheck = func(context.Context, availability.Options) (availability.Result, error) {
		checks++
		return availability.Result{
			Allowed:          false,
			State:            "suspended",
			Network:          "not_checked",
			SleepGapDetected: true,
			Reason:           "wake_gap_detected",
			CheckedAt:        "2026-08-26T12:00:00Z",
		}, nil
	}
	t.Cleanup(func() { autoHealEnvironmentCheck = previous })

	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"--browser-mode", "headless",
		"--state-dir", t.TempDir(),
		"daemon", "maintenance",
		"--chrome-command", "/path/that/does-not-exist",
		"--json",
	}, &stdout, &stderr, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("maintenance exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, stdout.String(), stderr.String())
	}
	var got struct {
		State       string              `json:"state"`
		Status      string              `json:"status"`
		Action      string              `json:"action"`
		Environment availability.Result `json:"environment"`
		Phases      []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"phases"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("maintenance output is invalid JSON: %v; output=%s", err, stdout.String())
	}
	if got.State != "environment_unavailable" || got.Status != "skipped" || got.Action != "skipped" || got.Environment.State != "suspended" || checks != 1 {
		t.Fatalf("maintenance result = %+v, checks=%d, want one suspended safe skip", got, checks)
	}
	for _, phase := range got.Phases {
		if phase.Name == "write_artifact" {
			if phase.Status != "passed" {
				t.Fatalf("write_artifact phase = %+v, want passed local artifact write", phase)
			}
			continue
		}
		if phase.Name != "acquire_lock" && phase.Status != "skipped" {
			t.Fatalf("phase = %+v, want skipped mutating phase", phase)
		}
	}
}
