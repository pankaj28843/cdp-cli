package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func TestManagedStopRejectsMismatchedStrongProcessIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	stateDir := t.TempDir()
	profileDir := ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	command, err := processgroup.StartWithOptions("sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start managed identity fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	metadata := ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           command.Process.Pid,
		StartedAt:           "2026-08-30T12:30:00Z",
		UserDataDir:         profileDir,
		DebuggingPort:       "9222",
		ProfileSeedStrategy: ProfileSeedStrategyManaged,
		OwnedMarker:         "managed-owner",
		ProcessStartTime:    "proc:not-the-live-process",
	}
	if err := SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}

	var signaled []int
	result, err := StopManagedChrome(context.Background(), stateDir, ManagedStopOptions{
		Signal: func(pid int) error {
			signaled = append(signaled, pid)
			return nil
		},
		ProcessLister: func(context.Context, string) ([]int, error) {
			return []int{command.Process.Pid}, nil
		},
		EndpointReachable: func(context.Context, string) bool { return false },
	})
	if err != nil {
		t.Fatalf("StopManagedChrome returned error: %v", err)
	}
	if !result.Checked || !result.Skipped || result.Stopped || len(signaled) != 0 {
		t.Fatalf("StopManagedChrome = %+v signaled=%v, want skipped without signaling reused PID", result, signaled)
	}
	if !strings.Contains(result.Reason, "identity") {
		t.Fatalf("StopManagedChrome reason = %q, want identity mismatch classification", result.Reason)
	}
	if !managedProcessAliveForTest(command.Process.Pid) {
		t.Fatal("mismatched managed identity caused the live process to stop")
	}
}

func TestManagedStopAcceptsMatchingStrongProcessIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	stateDir := t.TempDir()
	profileDir := ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	command, err := processgroup.StartWithOptions("sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start managed identity fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	token, err := processgroup.ProcessStartTime(context.Background(), command.Process.Pid)
	if err != nil {
		t.Fatalf("ProcessStartTime returned error: %v", err)
	}
	if err := SaveManagedMetadata(stateDir, ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           command.Process.Pid,
		StartedAt:           "2026-08-30T12:30:00Z",
		UserDataDir:         profileDir,
		DebuggingPort:       "9222",
		ProfileSeedStrategy: ProfileSeedStrategyManaged,
		OwnedMarker:         "managed-owner",
		ProcessStartTime:    token,
	}); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	var signaled []int
	result, err := StopManagedChrome(context.Background(), stateDir, ManagedStopOptions{
		Signal: func(pid int) error {
			signaled = append(signaled, pid)
			return nil
		},
		ProcessLister: func(context.Context, string) ([]int, error) {
			return nil, nil
		},
		EndpointReachable: func(context.Context, string) bool { return false },
	})
	if err != nil {
		t.Fatalf("StopManagedChrome returned error: %v", err)
	}
	if !result.Stopped || result.Skipped || len(signaled) != 1 || signaled[0] != command.Process.Pid {
		t.Fatalf("StopManagedChrome = %+v signaled=%v, want matching identity stop", result, signaled)
	}
}

func TestManagedStopRechecksStrongIdentityBeforeSignaling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	stateDir := t.TempDir()
	profileDir := ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	command, err := processgroup.StartWithOptions("sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start managed identity fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	token, err := processgroup.ProcessStartTime(context.Background(), command.Process.Pid)
	if err != nil {
		t.Fatalf("ProcessStartTime returned error: %v", err)
	}
	if err := SaveManagedMetadata(stateDir, ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           command.Process.Pid,
		StartedAt:           "2026-08-30T12:30:00Z",
		UserDataDir:         profileDir,
		DebuggingPort:       "9222",
		ProfileSeedStrategy: ProfileSeedStrategyManaged,
		OwnedMarker:         "managed-owner",
		ProcessStartTime:    token,
	}); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}

	original := managedStopProcessStartTime
	probeCalls := 0
	managedStopProcessStartTime = func(context.Context, int) (string, error) {
		probeCalls++
		if probeCalls == 1 {
			return token, nil
		}
		return "proc:replacement", nil
	}
	t.Cleanup(func() { managedStopProcessStartTime = original })

	signaled := 0
	result, err := StopManagedChrome(context.Background(), stateDir, ManagedStopOptions{
		Signal: func(int) error {
			signaled++
			return nil
		},
		ProcessLister: func(context.Context, string) ([]int, error) {
			return nil, nil
		},
		EndpointReachable: func(context.Context, string) bool { return false },
	})
	if err != nil {
		t.Fatalf("StopManagedChrome returned error: %v", err)
	}
	if probeCalls < 2 {
		t.Fatalf("managed stop identity probes = %d, want initial and final checks", probeCalls)
	}
	if !result.Skipped || result.Stopped || signaled != 0 {
		t.Fatalf("StopManagedChrome = %+v signaled=%d, want skipped without signaling replacement", result, signaled)
	}
	if !strings.Contains(result.Reason, "identity") {
		t.Fatalf("StopManagedChrome reason = %q, want identity mismatch classification", result.Reason)
	}
	if !managedProcessAliveForTest(command.Process.Pid) {
		t.Fatal("final identity mismatch caused the live process to stop")
	}
}

func TestManagedStopDoesNotSignalAfterFinalIdentityProbeCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	stateDir := t.TempDir()
	profileDir := ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	command, err := processgroup.StartWithOptions("sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start managed identity fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	token, err := processgroup.ProcessStartTime(context.Background(), command.Process.Pid)
	if err != nil {
		t.Fatalf("ProcessStartTime returned error: %v", err)
	}
	if err := SaveManagedMetadata(stateDir, ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           command.Process.Pid,
		StartedAt:           "2026-08-30T12:30:00Z",
		UserDataDir:         profileDir,
		DebuggingPort:       "9222",
		ProfileSeedStrategy: ProfileSeedStrategyManaged,
		OwnedMarker:         "managed-owner",
		ProcessStartTime:    token,
	}); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}

	original := managedStopProcessStartTime
	probeCalls := 0
	managedStopProcessStartTime = func(ctx context.Context, _ int) (string, error) {
		probeCalls++
		if probeCalls == 1 {
			return token, nil
		}
		return "", context.Canceled
	}
	t.Cleanup(func() { managedStopProcessStartTime = original })

	signaled := 0
	result, err := StopManagedChrome(context.Background(), stateDir, ManagedStopOptions{
		Signal: func(int) error {
			signaled++
			return nil
		},
		ProcessLister: func(context.Context, string) ([]int, error) {
			return nil, nil
		},
		EndpointReachable: func(context.Context, string) bool { return false },
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StopManagedChrome error = %v, want final identity cancellation", err)
	}
	if result.Stopped || signaled != 0 {
		t.Fatalf("StopManagedChrome = %+v signaled=%d, want no stop claim or signal", result, signaled)
	}
}

func TestReconcileManagedProcessesRejectsMismatchedStrongIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	stateDir := t.TempDir()
	profileDir := ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	command, err := processgroup.StartWithOptions("sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start managed identity fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	if err := SaveManagedMetadata(stateDir, ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           command.Process.Pid,
		StartedAt:           "2026-08-30T12:30:00Z",
		UserDataDir:         profileDir,
		DebuggingPort:       "9222",
		ProfileSeedStrategy: ProfileSeedStrategyManaged,
		OwnedMarker:         "managed-owner",
		ProcessStartTime:    "proc:not-the-live-process",
	}); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	result, err := ReconcileManagedProcesses(context.Background(), stateDir, ManagedProcessReconcileOptions{
		ReadOnly: true,
		ProcessLister: func(context.Context, string) ([]int, error) {
			return []int{command.Process.Pid}, nil
		},
	})
	if err != nil {
		t.Fatalf("ReconcileManagedProcesses returned error: %v", err)
	}
	if result.LiveCount != 0 || result.State == "error" {
		t.Fatalf("ReconcileManagedProcesses = %+v, want mismatched PID excluded from live ownership", result)
	}
	if !managedProcessAliveForTest(command.Process.Pid) {
		t.Fatal("reconciliation signaled the mismatched managed process")
	}
}

func TestVerifyManagedOwnershipRejectsMismatchedStrongIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	stateDir := t.TempDir()
	profileDir := ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	command, err := processgroup.StartWithOptions("sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start managed identity fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	metadata := ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           command.Process.Pid,
		StartedAt:           "2026-08-30T12:30:00Z",
		UserDataDir:         profileDir,
		DebuggingPort:       "9222",
		ProfileSeedStrategy: ProfileSeedStrategyManaged,
		OwnedMarker:         "managed-owner",
		ProcessStartTime:    "proc:not-the-live-process",
	}
	if err := SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	if err := RegisterManagedProcessLaunch(stateDir, metadata); err != nil {
		t.Fatalf("RegisterManagedProcessLaunch returned error: %v", err)
	}
	evidence := VerifyManagedOwnership(stateDir, ManagedMetadataStatus(metadata))
	if evidence.Owned || !containsManagedIdentityString(evidence.Reasons, "process_start_identity_mismatch") {
		t.Fatalf("VerifyManagedOwnership = %+v, want safe identity mismatch", evidence)
	}
	if !managedProcessAliveForTest(command.Process.Pid) {
		t.Fatal("ownership evidence signaled the mismatched managed process")
	}
}

func TestVerifyManagedOwnershipAcceptsMatchingStrongIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	stateDir := t.TempDir()
	profileDir := ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	command, err := processgroup.StartWithOptions("sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start managed identity fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	token, err := processgroup.ProcessStartTime(context.Background(), command.Process.Pid)
	if err != nil {
		t.Fatalf("ProcessStartTime returned error: %v", err)
	}
	metadata := ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           command.Process.Pid,
		StartedAt:           "2026-08-30T12:30:00Z",
		UserDataDir:         profileDir,
		DebuggingPort:       "9222",
		ProfileSeedStrategy: ProfileSeedStrategyManaged,
		OwnedMarker:         "managed-owner",
		ProcessStartTime:    token,
	}
	if err := SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	if err := RegisterManagedProcessLaunch(stateDir, metadata); err != nil {
		t.Fatalf("RegisterManagedProcessLaunch returned error: %v", err)
	}
	evidence := VerifyManagedOwnership(stateDir, ManagedMetadataStatus(metadata))
	if !evidence.Owned || !containsManagedIdentityString(evidence.SafetyChecks, "process_start_identity_matches") {
		t.Fatalf("VerifyManagedOwnership = %+v, want matching strong identity", evidence)
	}
}

func TestVerifyManagedOwnershipContextRejectsPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	original := managedOwnershipProcessStartTime
	managedOwnershipProcessStartTime = func(context.Context, int) (string, error) {
		called = true
		return "proc:unexpected", nil
	}
	t.Cleanup(func() { managedOwnershipProcessStartTime = original })

	evidence, err := VerifyManagedOwnershipContext(ctx, t.TempDir(), ManagedStatus{BrowserMode: "headless"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyManagedOwnershipContext error = %v, want context cancellation", err)
	}
	if evidence.Checked || evidence.Owned || called || !containsManagedIdentityString(evidence.Reasons, "ownership_check_canceled") {
		t.Fatalf("VerifyManagedOwnershipContext = %+v called=%v, want unchecked canceled evidence without probing", evidence, called)
	}
}

func TestVerifyManagedOwnershipContextStopsDuringIdentityProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	stateDir := t.TempDir()
	profileDir := ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	token, err := processgroup.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	metadata := ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           os.Getpid(),
		StartedAt:           "2026-08-30T12:30:00Z",
		UserDataDir:         profileDir,
		DebuggingPort:       "9222",
		ProfileSeedStrategy: ProfileSeedStrategyManaged,
		OwnedMarker:         "managed-owner",
		ProcessStartTime:    token,
	}
	if err := SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	if err := RegisterManagedProcessLaunch(stateDir, metadata); err != nil {
		t.Fatalf("RegisterManagedProcessLaunch returned error: %v", err)
	}

	entered := make(chan struct{})
	original := managedOwnershipProcessStartTime
	managedOwnershipProcessStartTime = func(ctx context.Context, _ int) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	t.Cleanup(func() { managedOwnershipProcessStartTime = original })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	var evidence ManagedOwnershipEvidence
	var inspectErr error
	go func() {
		evidence, inspectErr = VerifyManagedOwnershipContext(ctx, stateDir, ManagedMetadataStatus(metadata))
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("managed ownership inspection did not reach identity probe")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("managed ownership inspection did not stop after cancellation")
	}
	if !errors.Is(inspectErr, context.DeadlineExceeded) {
		t.Fatalf("VerifyManagedOwnershipContext error = %v, want deadline", inspectErr)
	}
	if evidence.Checked || evidence.Owned || !containsManagedIdentityString(evidence.Reasons, "ownership_check_canceled") {
		t.Fatalf("VerifyManagedOwnershipContext = %+v, want unchecked canceled evidence", evidence)
	}
	publicJSON, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal canceled ownership evidence: %v", err)
	}
	if containsManagedIdentityString([]string{string(publicJSON)}, token) {
		t.Fatalf("canceled ownership evidence exposed process identity: %s", publicJSON)
	}
}

func TestStartManagedChromeRecordsOSProcessIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process identity fixture is Unix-only")
	}
	stateDir := t.TempDir()
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	script := `#!/bin/sh
set -eu
user_data_dir=
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) user_data_dir="${arg#--user-data-dir=}" ;;
  esac
done
mkdir -p "$user_data_dir"
printf '12345\n/devtools/browser/test\n' > "$user_data_dir/DevToolsActivePort"
while :; do sleep 1; done
`
	if err := os.WriteFile(chromePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake chrome: %v", err)
	}
	launch, err := StartManagedChrome(context.Background(), ManagedOptions{StateDir: stateDir, Chrome: chromePath})
	if err != nil {
		t.Fatalf("StartManagedChrome returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = processgroup.TerminatePID(launch.Metadata.ChromePID)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && managedProcessAliveForTest(launch.Metadata.ChromePID) {
			time.Sleep(20 * time.Millisecond)
		}
	})
	if !isStrongProcessStartIdentity(launch.Metadata.ProcessStartTime) {
		t.Fatalf("managed launch process identity = %q, want an OS token", launch.Metadata.ProcessStartTime)
	}
	registry, ok, err := LoadManagedProcessRegistry(stateDir)
	if err != nil || !ok || len(registry.Records) != 1 || registry.Records[0].ProcessStartTime != launch.Metadata.ProcessStartTime {
		t.Fatalf("managed registry = %+v, ok=%v, err=%v; want matching process identity", registry, ok, err)
	}
}

func managedProcessAliveForTest(pid int) bool {
	process, err := os.FindProcess(pid)
	return err == nil && process.Signal(syscall.Signal(0)) == nil
}

func containsManagedIdentityString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
