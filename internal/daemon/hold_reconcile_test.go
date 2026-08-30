package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReconcileOrphanedDaemonHoldsReclaimsOnlyExactSupersededGeneration(t *testing.T) {
	stateDir := t.TempDir()
	profileDir := filepath.Join(stateDir, "profile")
	socketPath := RuntimeSocketPathForMode(stateDir, "headless")
	saveDaemonHoldReconcileRuntime(t, stateDir, Runtime{
		PID:              202,
		BrowserMode:      "headless",
		ConnectionMode:   "browser_url",
		SocketPath:       socketPath,
		Endpoint:         "ws://current.invalid/browser",
		ProcessStartTime: "proc:current",
		UserDataDir:      profileDir,
	})

	processes := []daemonHoldProcess{
		{PID: 101, ParentPID: 1, Executable: "/exact/cdp", Args: []string{"/exact/cdp", "daemon", "hold"}},
		{PID: 102, ParentPID: 1, Executable: "/lookalike/cdp", Args: []string{"/lookalike/cdp", "daemon", "hold"}},
		{PID: 103, ParentPID: 1, Executable: "/exact/cdp", Args: []string{"/exact/cdp", "daemon", "hold"}},
		{PID: 104, ParentPID: 1, Executable: "/exact/cdp", Args: []string{"/exact/cdp", "daemon", "hold"}},
		{PID: 105, ParentPID: 77, Executable: "/exact/cdp", Args: []string{"/exact/cdp", "daemon", "hold"}},
		{PID: 202, ParentPID: 1, Executable: "/exact/cdp", Args: []string{"/exact/cdp", "daemon", "hold"}},
	}
	environments := map[int]map[string]string{
		101: daemonHoldTestEnvironment(stateDir, profileDir, socketPath, "ws://old.invalid/browser"),
		102: daemonHoldTestEnvironment(stateDir, profileDir, socketPath, "ws://lookalike.invalid/browser"),
		103: daemonHoldTestEnvironment(filepath.Join(stateDir, "other-state"), profileDir, socketPath, "ws://wrong-state.invalid/browser"),
		104: map[string]string{
			"CDP_DAEMON_STATE_DIR":       stateDir,
			"CDP_DAEMON_BROWSER_MODE":    "headless",
			"CDP_DAEMON_CONNECTION_MODE": "browser_url",
			"CDP_DAEMON_SOCKET":          socketPath,
			"CDP_DAEMON_HOLD_ENDPOINT":   "ws://missing-generation.invalid/browser",
			"CDP_DAEMON_USER_DATA_DIR":   profileDir,
		},
		202: daemonHoldTestEnvironment(stateDir, profileDir, socketPath, "ws://current.invalid/browser"),
	}

	var signaled []int
	reclaimed := false
	withDaemonHoldReconcileSeams(t, processes, environments, func(pid int) (string, error) {
		switch pid {
		case 101:
			return "proc:old", nil
		case 104:
			return "", errors.New("synthetic missing identity")
		default:
			return "proc:other", nil
		}
	}, func(pid int) (bool, error) {
		if pid == 101 {
			return !reclaimed, nil
		}
		return true, nil
	}, func(pid int) error {
		signaled = append(signaled, pid)
		reclaimed = true
		return nil
	}, func() (string, error) { return "/exact/cdp", nil })

	got, err := ReconcileOrphanedDaemonHolds(context.Background(), stateDir, "headless", true)
	if err != nil {
		t.Fatalf("ReconcileOrphanedDaemonHolds() error = %v", err)
	}
	if !reflect.DeepEqual(signaled, []int{101}) {
		t.Fatalf("signaled = %v, want only exact superseded PID 101", signaled)
	}
	if !reflect.DeepEqual(got.ReclaimedPIDs, []int{101}) {
		t.Fatalf("reclaimed_pids = %v, want [101]", got.ReclaimedPIDs)
	}
	if got.ActivePID != 202 {
		t.Fatalf("active_pid = %d, want 202", got.ActivePID)
	}
	for _, pid := range []int{102, 103, 104, 105, 202} {
		if !containsInt(got.SkippedPIDs, pid) {
			t.Fatalf("skipped_pids = %v, want PID %d skipped", got.SkippedPIDs, pid)
		}
	}
	for _, reason := range []string{"executable_mismatch", "state_root_mismatch", "generation_unavailable", "not_orphaned", "current_runtime"} {
		if got.SkipReasons[reason] == 0 {
			t.Fatalf("skip_reasons = %+v, want %q", got.SkipReasons, reason)
		}
	}
	if got.State != "reclaimed" {
		t.Fatalf("state = %q, want reclaimed", got.State)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal reconciliation result: %v", err)
	}
	for _, secret := range []string{"ws://old.invalid", stateDir, profileDir, socketPath} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("reconciliation result exposes private value %q: %s", secret, encoded)
		}
	}
}

func TestReconcileOrphanedDaemonHoldsSkipsPIDReuseAndRuntimeReplacement(t *testing.T) {
	t.Run("pid_reuse", func(t *testing.T) {
		stateDir := t.TempDir()
		profileDir := filepath.Join(stateDir, "profile")
		socketPath := RuntimeSocketPathForMode(stateDir, "headless")
		saveDaemonHoldReconcileRuntime(t, stateDir, Runtime{
			PID:              302,
			BrowserMode:      "headless",
			ConnectionMode:   "browser_url",
			SocketPath:       socketPath,
			ProcessStartTime: "proc:current",
			UserDataDir:      profileDir,
		})
		processes := []daemonHoldProcess{{PID: 301, ParentPID: 1, Executable: "/exact/cdp", Args: []string{"/exact/cdp", "daemon", "hold"}}}
		environments := map[int]map[string]string{301: daemonHoldTestEnvironment(stateDir, profileDir, socketPath, "ws://old.invalid/browser")}
		calls := 0
		var signaled []int
		withDaemonHoldReconcileSeams(t, processes, environments, func(pid int) (string, error) {
			calls++
			if calls == 1 {
				return "proc:old", nil
			}
			return "proc:reused", nil
		}, func(int) (bool, error) { return true, nil }, func(pid int) error {
			signaled = append(signaled, pid)
			return nil
		}, func() (string, error) { return "/exact/cdp", nil })

		got, err := ReconcileOrphanedDaemonHolds(context.Background(), stateDir, "headless", true)
		if err != nil {
			t.Fatalf("ReconcileOrphanedDaemonHolds() error = %v", err)
		}
		if len(signaled) != 0 || !containsInt(got.SkippedPIDs, 301) || got.SkipReasons["pid_reused"] != 1 {
			t.Fatalf("result = %+v, signaled = %v; want PID reuse skip without signal", got, signaled)
		}
	})

	t.Run("runtime_replaced", func(t *testing.T) {
		stateDir := t.TempDir()
		profileDir := filepath.Join(stateDir, "profile")
		socketPath := RuntimeSocketPathForMode(stateDir, "headless")
		saveDaemonHoldReconcileRuntime(t, stateDir, Runtime{
			PID:              402,
			BrowserMode:      "headless",
			ConnectionMode:   "browser_url",
			SocketPath:       socketPath,
			ProcessStartTime: "proc:current",
			UserDataDir:      profileDir,
		})
		processes := []daemonHoldProcess{{PID: 401, ParentPID: 1, Executable: "/exact/cdp", Args: []string{"/exact/cdp", "daemon", "hold"}}}
		environments := map[int]map[string]string{401: daemonHoldTestEnvironment(stateDir, profileDir, socketPath, "ws://old.invalid/browser")}
		var signaled []int
		withDaemonHoldReconcileSeams(t, processes, environments, func(int) (string, error) {
			return "proc:old", nil
		}, func(pid int) (bool, error) {
			if pid == 401 {
				if err := SaveRuntimeForMode(context.Background(), stateDir, "headless", Runtime{
					PID:              403,
					BrowserMode:      "headless",
					ConnectionMode:   "browser_url",
					SocketPath:       socketPath,
					ProcessStartTime: "proc:new-current",
					UserDataDir:      profileDir,
				}); err != nil {
					t.Fatalf("replace runtime: %v", err)
				}
			}
			return true, nil
		}, func(pid int) error {
			signaled = append(signaled, pid)
			return nil
		}, func() (string, error) { return "/exact/cdp", nil })

		got, err := ReconcileOrphanedDaemonHolds(context.Background(), stateDir, "headless", true)
		if err != nil {
			t.Fatalf("ReconcileOrphanedDaemonHolds() error = %v", err)
		}
		if len(signaled) != 0 || got.SkipReasons["runtime_replaced"] != 1 {
			t.Fatalf("result = %+v, signaled = %v; want runtime replacement skip without signal", got, signaled)
		}
	})
}

func TestReconcileOrphanedDaemonHoldsReadOnlyIsIdempotent(t *testing.T) {
	stateDir := t.TempDir()
	profileDir := filepath.Join(stateDir, "profile")
	socketPath := RuntimeSocketPathForMode(stateDir, "headless")
	saveDaemonHoldReconcileRuntime(t, stateDir, Runtime{
		PID:              502,
		BrowserMode:      "headless",
		ConnectionMode:   "browser_url",
		SocketPath:       socketPath,
		ProcessStartTime: "proc:current",
		UserDataDir:      profileDir,
	})
	processes := []daemonHoldProcess{{PID: 501, ParentPID: 1, Executable: "/exact/cdp", Args: []string{"/exact/cdp", "daemon", "hold"}}}
	environments := map[int]map[string]string{501: daemonHoldTestEnvironment(stateDir, profileDir, socketPath, "ws://old.invalid/browser")}
	var signaled []int
	withDaemonHoldReconcileSeams(t, processes, environments, func(int) (string, error) {
		return "proc:old", nil
	}, func(int) (bool, error) { return true, nil }, func(pid int) error {
		signaled = append(signaled, pid)
		return nil
	}, func() (string, error) { return "/exact/cdp", nil })

	first, err := ReconcileOrphanedDaemonHolds(context.Background(), stateDir, "headless", false)
	if err != nil {
		t.Fatalf("first read-only reconciliation error = %v", err)
	}
	second, err := ReconcileOrphanedDaemonHolds(context.Background(), stateDir, "headless", false)
	if err != nil {
		t.Fatalf("second read-only reconciliation error = %v", err)
	}
	if first.State != "inspected" || second.State != "inspected" || !reflect.DeepEqual(first.EligiblePIDs, []int{501}) || !reflect.DeepEqual(second.EligiblePIDs, []int{501}) {
		t.Fatalf("first=%+v second=%+v; want stable read-only eligibility", first, second)
	}
	if len(signaled) != 0 {
		t.Fatalf("signaled = %v, want no read-only signal", signaled)
	}
}

func saveDaemonHoldReconcileRuntime(t *testing.T, stateDir string, runtime Runtime) {
	t.Helper()
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headless", runtime); err != nil {
		t.Fatalf("SaveRuntimeForMode() error = %v", err)
	}
}

func daemonHoldTestEnvironment(stateDir, profileDir, socketPath, endpoint string) map[string]string {
	return map[string]string{
		"CDP_DAEMON_STATE_DIR":       stateDir,
		"CDP_DAEMON_BROWSER_MODE":    "headless",
		"CDP_DAEMON_CONNECTION_MODE": "browser_url",
		"CDP_DAEMON_SOCKET":          socketPath,
		"CDP_DAEMON_HOLD_ENDPOINT":   endpoint,
		"CDP_DAEMON_USER_DATA_DIR":   profileDir,
	}
}

func withDaemonHoldReconcileSeams(t *testing.T, processes []daemonHoldProcess, environments map[int]map[string]string, start func(int) (string, error), running func(int) (bool, error), signal func(int) error, executable func() (string, error)) {
	t.Helper()
	originalLister := daemonHoldProcessLister
	originalEnvironment := daemonHoldProcessEnvironment
	originalStart := daemonHoldProcessStartTime
	originalRunning := daemonHoldProcessRunning
	originalSignal := daemonHoldProcessSignal
	originalExecutable := daemonHoldExecutable
	originalRuntimeRunning := runtimeProcessRunning
	originalRuntimeStart := runtimeProcessStartTime
	daemonHoldProcessLister = func(context.Context) ([]daemonHoldProcess, error) {
		return append([]daemonHoldProcess(nil), processes...), nil
	}
	daemonHoldProcessEnvironment = func(_ context.Context, pid int) (map[string]string, error) {
		environment, ok := environments[pid]
		if !ok {
			return nil, fmt.Errorf("synthetic environment unavailable")
		}
		return environment, nil
	}
	daemonHoldProcessStartTime = func(_ context.Context, pid int) (string, error) {
		return start(pid)
	}
	daemonHoldProcessRunning = func(_ context.Context, pid int) (bool, error) {
		return running(pid)
	}
	daemonHoldProcessSignal = signal
	daemonHoldExecutable = executable
	runtimeProcessRunning = func(context.Context, int) (bool, error) { return true, nil }
	runtimeProcessStartTime = func(context.Context, int) (string, error) { return "proc:current", nil }
	t.Cleanup(func() {
		daemonHoldProcessLister = originalLister
		daemonHoldProcessEnvironment = originalEnvironment
		daemonHoldProcessStartTime = originalStart
		daemonHoldProcessRunning = originalRunning
		daemonHoldProcessSignal = originalSignal
		daemonHoldExecutable = originalExecutable
		runtimeProcessRunning = originalRuntimeRunning
		runtimeProcessStartTime = originalRuntimeStart
	})
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestParseDaemonHoldEnvironmentDoesNotRetainUnknownKeys(t *testing.T) {
	environment := parseDaemonHoldEnvironment([]byte("CDP_DAEMON_STATE_DIR=/tmp/state\x00SECRET=do-not-retain\x00CDP_DAEMON_HOLD_ENDPOINT=ws://synthetic\x00"))
	if len(environment) != 2 || environment["SECRET"] != "" {
		t.Fatalf("parsed environment = %+v, want only allowlisted keys", environment)
	}
	if environment["CDP_DAEMON_STATE_DIR"] != "/tmp/state" {
		t.Fatalf("state dir = %q, want synthetic state dir", environment["CDP_DAEMON_STATE_DIR"])
	}
}

func TestParseDaemonHoldEnvironmentTextStopsAtUnrelatedKeysAndPreservesSpaces(t *testing.T) {
	raw := []byte("/private/path/cdp daemon hold CDP_DAEMON_STATE_DIR=/tmp/state with spaces CDP_DAEMON_SOCKET=/tmp/socket with spaces SHELL=/bin/zsh CDP_DAEMON_BROWSER_MODE=headless")
	environment := parseDaemonHoldEnvironmentText(raw)
	if environment["CDP_DAEMON_STATE_DIR"] != "/tmp/state with spaces" {
		t.Fatalf("state dir = %q, want path with spaces", environment["CDP_DAEMON_STATE_DIR"])
	}
	if environment["CDP_DAEMON_SOCKET"] != "/tmp/socket with spaces" {
		t.Fatalf("socket = %q, want path with spaces", environment["CDP_DAEMON_SOCKET"])
	}
	if environment["CDP_DAEMON_BROWSER_MODE"] != "headless" {
		t.Fatalf("browser mode = %q, want headless", environment["CDP_DAEMON_BROWSER_MODE"])
	}
	if _, ok := environment["SHELL"]; ok {
		t.Fatalf("unallowlisted shell key was retained: %+v", environment)
	}
}

func TestReconcileOrphanedDaemonHoldsRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReconcileOrphanedDaemonHolds(ctx, t.TempDir(), "headless", true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReconcileOrphanedDaemonHolds() error = %v, want context.Canceled", err)
	}
}

func TestDaemonHoldReconcileResultIsStableJSON(t *testing.T) {
	result := DaemonHoldReconcileResult{
		Checked:      true,
		State:        "healthy",
		BrowserMode:  "headless",
		ActivePID:    10,
		SkipReasons:  map[string]int{},
		SafetyChecks: []string{"exact_executable"},
		NextCommands: []string{"cdp --browser-mode headless daemon health --json"},
	}
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), `"browser_mode":"headless"`) {
		t.Fatalf("marshal result = %s, err=%v", encoded, err)
	}
}

func TestReconcileOrphanedDaemonHoldsUsesRuntimeMode(t *testing.T) {
	got, err := ReconcileOrphanedDaemonHolds(context.Background(), t.TempDir(), "headed", true)
	if err != nil {
		t.Fatalf("headed reconciliation error = %v", err)
	}
	if got.State != "unsupported_mode" || got.BrowserMode != "headed" {
		t.Fatalf("headed result = %+v, want unsupported_mode", got)
	}
}

func TestReconcileOrphanedDaemonHoldsNoRuntimeIsSafeSkip(t *testing.T) {
	got, err := ReconcileOrphanedDaemonHolds(context.Background(), t.TempDir(), "headless", true)
	if err != nil {
		t.Fatalf("no-runtime reconciliation error = %v", err)
	}
	if got.State != "no_runtime" || got.SkipReasons["no_current_runtime"] != 1 {
		t.Fatalf("no-runtime result = %+v, want no_runtime safe skip", got)
	}
}

func TestDaemonHoldEnvironmentKeysAreBounded(t *testing.T) {
	raw := []byte("CDP_DAEMON_STATE_DIR=" + strings.Repeat("x", maxDaemonHoldEnvironmentValueBytes+1) + "\x00")
	environment := parseDaemonHoldEnvironment(raw)
	if _, ok := environment["CDP_DAEMON_STATE_DIR"]; ok {
		t.Fatalf("oversized environment value was retained")
	}
}

func TestDaemonHoldReconcileTestHelpersUseTempState(t *testing.T) {
	if _, err := os.Stat(t.TempDir()); err != nil {
		t.Fatalf("temp state setup: %v", err)
	}
}
