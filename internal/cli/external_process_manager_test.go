package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadUserCrontabBoundsManagerOutput(t *testing.T) {
	command := writeExternalProcessFixture(t, `#!/bin/sh
i=0
while [ "$i" -lt 20000 ]; do
  printf '0123456789'
  i=$((i + 1))
done
`)
	t.Setenv("CDP_CRONTAB_BIN", command)

	output, err := readUserCrontab(context.Background())
	if !errors.Is(err, errExternalProcessOutputTooLarge) {
		t.Fatalf("readUserCrontab() error = %v, want output-bound error", err)
	}
	if len(output) > maxExternalProcessOutputBytes {
		t.Fatalf("readUserCrontab() output length = %d, want <= %d", len(output), maxExternalProcessOutputBytes)
	}
}

func TestWriteUserCrontabDoesNotEchoUnboundedManagerDiagnostics(t *testing.T) {
	command := writeExternalProcessFixture(t, `#!/bin/sh
i=0
while [ "$i" -lt 20000 ]; do
  printf '0123456789' >&2
  i=$((i + 1))
done
exit 1
`)
	t.Setenv("CDP_CRONTAB_BIN", command)

	err := writeUserCrontab(context.Background(), "SHELL=/bin/sh\n")
	if !errors.Is(err, errExternalProcessOutputTooLarge) {
		t.Fatalf("writeUserCrontab() error = %v, want output-bound error", err)
	}
	if strings.Contains(err.Error(), "0123456789") || !strings.Contains(err.Error(), "safety bound") {
		t.Fatalf("writeUserCrontab() error = %v, want stable bounded diagnostic", err)
	}
}

func TestRunServiceCommandPreservesBoundedFailureDiagnostic(t *testing.T) {
	command := writeExternalProcessFixture(t, `#!/bin/sh
printf 'service is not loaded\n' >&2
exit 3
`)

	output, err := runServiceCommand(context.Background(), command, "status")
	if err == nil || !strings.Contains(err.Error(), "service is not loaded") || output != "service is not loaded" {
		t.Fatalf("runServiceCommand() output=%q error=%v, want bounded manager diagnostic", output, err)
	}
}

func TestRunServiceCommandClassifiesDiagnosticOverflow(t *testing.T) {
	command := writeExternalProcessFixture(t, `#!/bin/sh
i=0
while [ "$i" -lt 20000 ]; do
  printf '0123456789' >&2
  i=$((i + 1))
done
`)

	output, err := runServiceCommand(context.Background(), command, "status")
	if !errors.Is(err, errExternalProcessOutputTooLarge) {
		t.Fatalf("runServiceCommand() error = %v, want output-bound classification", err)
	}
	if output != "" || strings.Contains(err.Error(), "0123456789") || !strings.Contains(err.Error(), "safety bound") {
		t.Fatalf("runServiceCommand() output=%q error=%v, want stable no-payload diagnostic", output, err)
	}
}

func TestScheduledTasksStatusClassifiesCrontabDiagnosticOverflow(t *testing.T) {
	check := scheduledTasksStatusForSummary(true, errExternalProcessOutputTooLarge, crontabSummary{})
	if check["status"] != "warn" || check["message"] != "current user crontab could not be inspected because command output exceeded the safety bound" {
		t.Fatalf("scheduled task check = %+v, want bounded-output warning", check)
	}
	details, ok := check["details"].(map[string]any)
	if !ok || details["command_output_truncated"] != true {
		t.Fatalf("scheduled task details = %+v, want command_output_truncated=true", check["details"])
	}
}

func TestDoctorDoesNotClassifyPartialCrontabAsEmpty(t *testing.T) {
	command := writeExternalProcessFixture(t, `#!/bin/sh
i=0
while [ "$i" -lt 20000 ]; do
  printf '0123456789'
  i=$((i + 1))
done
`)
	t.Setenv("CDP_CRONTAB_BIN", command)
	stateDir := filepath.Join(t.TempDir(), "state")

	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"doctor", "--check", "scheduled-tasks", "--state-dir", stateDir, "--json",
	}, &stdout, &stderr, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("doctor exit = %d; stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var response struct {
		Checks []struct {
			Status  string         `json:"status"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode doctor output: %v\n%s", err, stdout.String())
	}
	if len(response.Checks) != 1 || response.Checks[0].Status != "warn" || response.Checks[0].Details["command_output_truncated"] != true || strings.Contains(response.Checks[0].Message, "no cdp entries") {
		t.Fatalf("doctor scheduled-tasks check = %+v, want bounded unclassified warning", response.Checks)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("doctor state directory was not created: %v", err)
	}
}
