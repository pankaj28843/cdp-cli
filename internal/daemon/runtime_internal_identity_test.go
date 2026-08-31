//go:build unix

package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func TestWaitForRuntimeForModeRejectsMismatchedIdentityBeforeSocketAcceptance(t *testing.T) {
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	stateDir := shortInternalStateDir(t)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("create runtime state directory: %v", err)
	}
	socketPath := filepath.Join(stateDir, "runtime.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on runtime socket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headed", Runtime{
		PID:              os.Getpid(),
		ProcessStartTime: "proc:not-the-live-process",
		BrowserMode:      "headed",
		SocketPath:       socketPath,
	}); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err = waitForRuntimeForMode(ctx, stateDir, "headed", os.Getpid())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitForRuntimeForMode error = %v, want deadline after rejecting mismatched identity", err)
	}
}

func TestWaitForRuntimeSocketRejectsMismatchedIdentityBeforeSocketAcceptance(t *testing.T) {
	stateDir := shortInternalStateDir(t)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("create runtime state directory: %v", err)
	}
	socketPath := filepath.Join(stateDir, "runtime.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on runtime socket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	_, err = waitForRuntimeSocket(context.Background(), Runtime{
		PID:              os.Getpid(),
		ProcessStartTime: "proc:not-the-live-process",
		SocketPath:       socketPath,
	})
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("waitForRuntimeSocket error = %v, want identity mismatch before ready socket", err)
	}
}

func TestStopRuntimeDoesNotEscalateAfterIdentityMismatch(t *testing.T) {
	testStopRuntimeDoesNotEscalateAfterIdentityChange(t, false)
}

func TestStopRuntimeDoesNotEscalateWhenIdentityUnavailable(t *testing.T) {
	testStopRuntimeDoesNotEscalateAfterIdentityChange(t, true)
}

func TestStopRuntimeRechecksIdentityBeforeInterrupt(t *testing.T) {
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}
	stateDir := shortInternalStateDir(t)
	command, err := processgroup.StartWithOptions("/bin/sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start stop fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	token, err := processgroup.ProcessStartTime(context.Background(), command.Process.Pid)
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headed", Runtime{
		PID:              command.Process.Pid,
		ProcessStartTime: token,
		BrowserMode:      "headed",
		SocketPath:       filepath.Join(stateDir, RuntimeSocketFileName),
	}); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	original := runtimeProcessStartTime
	originalInterrupt := runtimeProcessInterrupt
	identityCalls := 0
	signaled := false
	runtimeProcessStartTime = func(context.Context, int) (string, error) {
		identityCalls++
		if identityCalls == 1 {
			return token, nil
		}
		return "proc:reused-runtime", nil
	}
	runtimeProcessInterrupt = func(*os.Process) error {
		signaled = true
		return nil
	}
	t.Cleanup(func() {
		runtimeProcessStartTime = original
		runtimeProcessInterrupt = originalInterrupt
	})

	_, stopped, err := StopRuntimeForMode(context.Background(), stateDir, "headed")
	if err != nil {
		t.Fatalf("StopRuntimeForMode returned error: %v", err)
	}
	if stopped {
		t.Fatal("StopRuntimeForMode claimed stopped after final identity mismatch")
	}
	if signaled {
		t.Fatal("final identity mismatch still signaled runtime")
	}
	if !ProcessRunning(command.Process.Pid) {
		t.Fatal("final identity mismatch terminated the fixture process")
	}
	if _, ok, err := LoadRuntimeForMode(context.Background(), stateDir, "headed"); err != nil || ok {
		t.Fatalf("runtime after final identity mismatch = ok=%v err=%v, want stale record cleared", ok, err)
	}
}

func TestStopRuntimeDoesNotSignalWhenProcessDisappearsBeforeFinalCheck(t *testing.T) {
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}
	stateDir := shortInternalStateDir(t)
	command, err := processgroup.StartWithOptions("/bin/sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start stop fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	token, err := processgroup.ProcessStartTime(context.Background(), command.Process.Pid)
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headed", Runtime{
		PID:              command.Process.Pid,
		ProcessStartTime: token,
		BrowserMode:      "headed",
		SocketPath:       filepath.Join(stateDir, RuntimeSocketFileName),
	}); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	originalIdentity := runtimeProcessStartTime
	originalInterrupt := runtimeProcessInterrupt
	signaled := false
	runtimeProcessStartTime = func(context.Context, int) (string, error) {
		processgroup.Terminate(command)
		_ = command.Wait()
		return token, nil
	}
	runtimeProcessInterrupt = func(*os.Process) error {
		signaled = true
		return nil
	}
	t.Cleanup(func() {
		runtimeProcessStartTime = originalIdentity
		runtimeProcessInterrupt = originalInterrupt
	})

	_, stopped, err := StopRuntimeForMode(context.Background(), stateDir, "headed")
	if err != nil {
		t.Fatalf("StopRuntimeForMode returned error: %v", err)
	}
	if stopped || signaled {
		t.Fatalf("StopRuntimeForMode = stopped=%v signaled=%v, want no signal after process disappearance", stopped, signaled)
	}
	if _, ok, err := LoadRuntimeForMode(context.Background(), stateDir, "headed"); err != nil || ok {
		t.Fatalf("runtime after process disappearance = ok=%v err=%v, want stale record cleared", ok, err)
	}
}

func TestStopRuntimeDoesNotSignalWhenFinalIdentityProbeIsUnavailable(t *testing.T) {
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}
	stateDir := shortInternalStateDir(t)
	command, err := processgroup.StartWithOptions("/bin/sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start stop fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	token, err := processgroup.ProcessStartTime(context.Background(), command.Process.Pid)
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headed", Runtime{
		PID:              command.Process.Pid,
		ProcessStartTime: token,
		BrowserMode:      "headed",
		SocketPath:       filepath.Join(stateDir, RuntimeSocketFileName),
	}); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	original := runtimeProcessStartTime
	originalInterrupt := runtimeProcessInterrupt
	identityCalls := 0
	signaled := false
	runtimeProcessStartTime = func(context.Context, int) (string, error) {
		identityCalls++
		if identityCalls == 1 {
			return token, nil
		}
		return "", errors.New("synthetic final identity unavailable")
	}
	runtimeProcessInterrupt = func(*os.Process) error {
		signaled = true
		return nil
	}
	t.Cleanup(func() {
		runtimeProcessStartTime = original
		runtimeProcessInterrupt = originalInterrupt
	})

	_, stopped, err := StopRuntimeForMode(context.Background(), stateDir, "headed")
	if err == nil || !strings.Contains(err.Error(), "verify daemon process identity before signaling") {
		t.Fatalf("StopRuntimeForMode = stopped=%v err=%v, want final identity error", stopped, err)
	}
	if signaled {
		t.Fatal("final identity unavailability still signaled runtime")
	}
	if !ProcessRunning(command.Process.Pid) {
		t.Fatal("final identity unavailability terminated the fixture process")
	}
	if _, ok, err := LoadRuntimeForMode(context.Background(), stateDir, "headed"); err != nil || !ok {
		t.Fatalf("runtime after final identity unavailability = ok=%v err=%v, want record preserved", ok, err)
	}
}

func TestStopRuntimeDoesNotSignalAfterFinalIdentityCancellation(t *testing.T) {
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}
	stateDir := shortInternalStateDir(t)
	command, err := processgroup.StartWithOptions("/bin/sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start stop fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	token, err := processgroup.ProcessStartTime(context.Background(), command.Process.Pid)
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headed", Runtime{
		PID:              command.Process.Pid,
		ProcessStartTime: token,
		BrowserMode:      "headed",
		SocketPath:       filepath.Join(stateDir, RuntimeSocketFileName),
	}); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	original := runtimeProcessStartTime
	originalInterrupt := runtimeProcessInterrupt
	identityCalls := 0
	signaled := false
	runtimeProcessStartTime = func(ctx context.Context, _ int) (string, error) {
		identityCalls++
		if identityCalls == 1 {
			return token, nil
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	runtimeProcessInterrupt = func(*os.Process) error {
		signaled = true
		return nil
	}
	t.Cleanup(func() {
		runtimeProcessStartTime = original
		runtimeProcessInterrupt = originalInterrupt
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, stopped, err := StopRuntimeForMode(ctx, stateDir, "headed")
	if !errors.Is(err, context.DeadlineExceeded) || stopped {
		t.Fatalf("StopRuntimeForMode = stopped=%v err=%v, want canceled final identity check", stopped, err)
	}
	if signaled {
		t.Fatal("final identity cancellation still signaled runtime")
	}
	if !ProcessRunning(command.Process.Pid) {
		t.Fatal("final identity cancellation terminated the fixture process")
	}
}

func TestCheckRuntimeProcessRechecksLivenessAfterIdentityProbeFailure(t *testing.T) {
	command, err := processgroup.StartWithOptions("/bin/sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start identity probe fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})

	original := runtimeProcessStartTime
	runtimeProcessStartTime = func(context.Context, int) (string, error) {
		processgroup.Terminate(command)
		_ = command.Wait()
		return "", errors.New("synthetic probe observed process exit")
	}
	t.Cleanup(func() { runtimeProcessStartTime = original })

	check := CheckRuntimeProcess(context.Background(), Runtime{
		PID:              command.Process.Pid,
		ProcessStartTime: "proc:expected",
	})
	if check.Running || check.State != RuntimeProcessStateNotRunning {
		t.Fatalf("CheckRuntimeProcess = %+v, want not-running after probe-time process exit", check)
	}
}

func testStopRuntimeDoesNotEscalateAfterIdentityChange(t *testing.T, unavailable bool) {
	t.Helper()
	if processgroup.TerminationMode() != "process_group" {
		t.Skip("process groups are not available on this platform")
	}

	stateDir := shortInternalStateDir(t)
	command, err := processgroup.StartWithOptions("/bin/sh", []string{"-c", "trap '' INT TERM; while :; do /bin/sleep 1; done"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start stop fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	token, err := processgroup.ProcessStartTime(context.Background(), command.Process.Pid)
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	if err := SaveRuntimeForMode(context.Background(), stateDir, "headed", Runtime{
		PID:              command.Process.Pid,
		ProcessStartTime: token,
		BrowserMode:      "headed",
		SocketPath:       filepath.Join(stateDir, "runtime.sock"),
	}); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	original := runtimeProcessStartTime
	identityCalls := 0
	runtimeProcessStartTime = func(context.Context, int) (string, error) {
		identityCalls++
		if identityCalls <= 2 {
			return token, nil
		}
		if unavailable {
			return "", errors.New("synthetic identity unavailable")
		}
		return "proc:reused-process", nil
	}
	t.Cleanup(func() { runtimeProcessStartTime = original })

	started := time.Now()
	_, stopped, err := StopRuntimeForMode(context.Background(), stateDir, "headed")
	if unavailable {
		if err == nil || !strings.Contains(err.Error(), "verify daemon process identity") {
			t.Fatalf("StopRuntimeForMode unavailable = stopped=%v err=%v, want identity error", stopped, err)
		}
	} else if err != nil {
		t.Fatalf("StopRuntimeForMode mismatch error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("StopRuntimeForMode took %s, want no five-second force-kill wait", elapsed)
	}
	if !ProcessRunning(command.Process.Pid) {
		t.Fatal("StopRuntimeForMode escalated to process-group termination after identity changed")
	}
}
