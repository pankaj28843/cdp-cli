package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func TestRuntimePersistsProcessIdentityPrivately(t *testing.T) {
	stateDir := t.TempDir()
	token, err := processgroup.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Fatalf("ProcessStartTime returned error: %v", err)
	}
	runtime := Runtime{
		PID:                    os.Getpid(),
		ProcessStartTime:       token,
		ConnectionMode:         "browser_url",
		SocketPath:             RuntimeSocketPath(stateDir),
		ChromePID:              os.Getpid(),
		ChromeProcessStartTime: token,
	}
	if err := SaveRuntime(context.Background(), stateDir, runtime); err != nil {
		t.Fatalf("SaveRuntime returned error: %v", err)
	}
	loaded, ok, err := LoadRuntime(context.Background(), stateDir)
	if err != nil || !ok || loaded.ProcessStartTime != token || loaded.ChromeProcessStartTime != token {
		t.Fatalf("LoadRuntime = %+v, ok=%v, err=%v; want private process identity", loaded, ok, err)
	}
	publicJSON, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("json.Marshal runtime returned error: %v", err)
	}
	if string(publicJSON) == "" || containsJSONField(publicJSON, "process_start_time") || containsJSONField(publicJSON, "chrome_process_start_time") || containsJSONValue(publicJSON, token) {
		t.Fatalf("public runtime JSON exposed process identity: %s", publicJSON)
	}
}

func TestCheckRuntimeProcessAcceptsMatchingStrongIdentity(t *testing.T) {
	token, err := processgroup.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	check := CheckRuntimeProcess(context.Background(), Runtime{PID: os.Getpid(), ProcessStartTime: token})
	if !check.Running || check.State != RuntimeProcessStateRunning {
		t.Fatalf("CheckRuntimeProcess = %+v, want matching process identity", check)
	}
}

func TestCheckRuntimeProcessRejectsMismatchedStrongIdentity(t *testing.T) {
	token, err := processgroup.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	check := CheckRuntimeProcess(context.Background(), Runtime{PID: os.Getpid(), ProcessStartTime: "proc:not-the-live-process"})
	if check.Running || check.State != RuntimeProcessStateIdentityMismatch {
		t.Fatalf("CheckRuntimeProcess = %+v, want mismatched process identity", check)
	}
	if token == "" {
		t.Fatal("matching process identity was empty")
	}
}

func TestCheckRuntimeProcessFailsClosedWhenIdentityUnavailable(t *testing.T) {
	token, err := processgroup.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Skipf("host does not expose process identity: %v", err)
	}
	original := runtimeProcessStartTime
	runtimeProcessStartTime = func(context.Context, int) (string, error) {
		return "", errors.New("synthetic identity probe unavailable")
	}
	defer func() { runtimeProcessStartTime = original }()

	check := CheckRuntimeProcess(context.Background(), Runtime{PID: os.Getpid(), ProcessStartTime: token})
	if check.Running || check.State != RuntimeProcessStateIdentityUnavailable {
		t.Fatalf("CheckRuntimeProcess = %+v, want unavailable identity", check)
	}
}

func TestCheckRuntimeProcessKeepsLegacyRuntimeCompatible(t *testing.T) {
	check := CheckRuntimeProcess(context.Background(), Runtime{PID: os.Getpid(), ProcessStartTime: "2026-08-30T12:30:00Z"})
	if !check.Running || check.State != RuntimeProcessStateRunning {
		t.Fatalf("CheckRuntimeProcess = %+v, want legacy PID-compatible running state", check)
	}
}

func TestStopRuntimeTreatsMismatchedProcessIdentityAsStale(t *testing.T) {
	stateDir := t.TempDir()
	command, err := processgroup.StartWithOptions("sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start identity fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	if err := SaveRuntime(context.Background(), stateDir, Runtime{
		PID:              command.Process.Pid,
		ProcessStartTime: "proc:not-the-live-process",
		BrowserMode:      "headed",
		ConnectionMode:   "browser_url",
		SocketPath:       RuntimeSocketPath(stateDir),
	}); err != nil {
		t.Fatalf("SaveRuntime returned error: %v", err)
	}

	_, stopped, err := StopRuntime(context.Background(), stateDir)
	if err != nil {
		t.Fatalf("StopRuntime returned error: %v", err)
	}
	if stopped {
		t.Fatal("StopRuntime reported a stale mismatched identity as stopped")
	}
	if !ProcessRunning(command.Process.Pid) {
		t.Fatal("StopRuntime signaled the process despite mismatched identity")
	}
	if _, ok, err := LoadRuntime(context.Background(), stateDir); err != nil || ok {
		t.Fatalf("stale runtime after StopRuntime = ok=%v err=%v, want cleared", ok, err)
	}
}

func TestStaleIdentityCannotClearReplacementRuntime(t *testing.T) {
	stateDir := t.TempDir()
	oldRuntime := Runtime{
		PID:              101,
		ProcessStartTime: "proc:old",
		BrowserMode:      "headed",
		ConnectionMode:   "browser_url",
		SocketPath:       RuntimeSocketPath(stateDir),
	}
	replacement := Runtime{
		PID:              202,
		ProcessStartTime: "proc:new",
		BrowserMode:      "headed",
		ConnectionMode:   "browser_url",
		SocketPath:       RuntimeSocketPath(stateDir),
	}
	if err := SaveRuntime(context.Background(), stateDir, replacement); err != nil {
		t.Fatalf("SaveRuntime replacement returned error: %v", err)
	}
	if err := os.WriteFile(replacement.SocketPath, []byte("replacement-socket"), 0o600); err != nil {
		t.Fatalf("write replacement socket fixture: %v", err)
	}
	cleared, err := clearRuntimeForModeIfCurrent(context.Background(), stateDir, "headed", oldRuntime)
	if err != nil {
		t.Fatalf("clearRuntimeForModeIfCurrent returned error: %v", err)
	}
	if cleared {
		t.Fatal("stale identity cleared a replacement runtime")
	}
	loaded, ok, err := LoadRuntime(context.Background(), stateDir)
	if err != nil || !ok || loaded.PID != replacement.PID || loaded.ProcessStartTime != replacement.ProcessStartTime {
		t.Fatalf("replacement runtime = %+v, ok=%v, err=%v; want preserved replacement", loaded, ok, err)
	}
	if _, err := os.Stat(replacement.SocketPath); err != nil {
		t.Fatalf("replacement socket was removed with stale runtime: %v", err)
	}
}

func TestStopRuntimeKeepsLegacyRuntimeWithoutProcessIdentity(t *testing.T) {
	stateDir := t.TempDir()
	command, err := processgroup.StartWithOptions("sleep", []string{"30"}, processgroup.Options{NewSession: true})
	if err != nil {
		t.Fatalf("start legacy identity fixture: %v", err)
	}
	t.Cleanup(func() {
		processgroup.Terminate(command)
		_ = command.Wait()
	})
	if err := SaveRuntime(context.Background(), stateDir, Runtime{
		PID:            command.Process.Pid,
		BrowserMode:    "headed",
		ConnectionMode: "browser_url",
		SocketPath:     RuntimeSocketPath(stateDir),
	}); err != nil {
		t.Fatalf("SaveRuntime legacy returned error: %v", err)
	}
	_, stopped, err := StopRuntime(context.Background(), stateDir)
	if err != nil || !stopped {
		t.Fatalf("StopRuntime legacy = stopped=%v err=%v, want compatible stop", stopped, err)
	}
}

func TestHoldRecordsProcessIdentityWhenHostProvidesIt(t *testing.T) {
	server := newProtocolFallbackFakeServer(t)
	defer server.Close()
	stateDir := shortInternalStateDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- holdWithOptions(ctx, stateDir, fakeProtocolFallbackEndpoint(t, server.URL), "browser_url", time.Second, holdOptions{
			fetchProtocolFallback: func(context.Context) (cdp.Protocol, error) {
				return cdp.Protocol{Domains: []cdp.Domain{{Domain: "Runtime"}}}, nil
			},
		})
	}()
	runtime := waitForProtocolFallbackRuntime(t, ctx, stateDir)
	if processgroup.TerminationMode() == "process_group" && runtime.ProcessStartTime == "" {
		t.Fatal("daemon runtime omitted process identity on a process-group host")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("hold returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon hold did not stop after context cancellation")
	}
}

func containsJSONField(data []byte, field string) bool {
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return false
	}
	_, ok := values[field]
	return ok
}

func containsJSONValue(data []byte, value string) bool {
	return len(value) > 0 && bytes.Contains(data, []byte(value))
}
