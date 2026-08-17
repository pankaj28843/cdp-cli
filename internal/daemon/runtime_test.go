package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestRuntimePathsAreModeAware(t *testing.T) {
	stateDir := t.TempDir()
	if got := daemon.RuntimePath(stateDir); got != filepath.Join(stateDir, "daemon.json") {
		t.Fatalf("RuntimePath() = %q, want headed-compatible daemon.json", got)
	}
	if got := daemon.RuntimeSocketPath(stateDir); got != filepath.Join(stateDir, "daemon.sock") {
		t.Fatalf("RuntimeSocketPath() = %q, want headed-compatible daemon.sock", got)
	}
	if got := daemon.RuntimeLogPath(stateDir); got != filepath.Join(stateDir, "daemon.log") {
		t.Fatalf("RuntimeLogPath() = %q, want headed-compatible daemon.log", got)
	}

	if got := daemon.RuntimePathForMode(stateDir, "headed"); got != daemon.RuntimePath(stateDir) {
		t.Fatalf("RuntimePathForMode headed = %q, want %q", got, daemon.RuntimePath(stateDir))
	}
	if got := daemon.RuntimePathForMode(stateDir, ""); got != daemon.RuntimePath(stateDir) {
		t.Fatalf("RuntimePathForMode default = %q, want %q", got, daemon.RuntimePath(stateDir))
	}
	if got := daemon.RuntimePathForMode(stateDir, "headless"); got != filepath.Join(stateDir, "headless", "daemon.json") {
		t.Fatalf("RuntimePathForMode headless = %q, want mode-specific daemon.json", got)
	}
	if got := daemon.RuntimeSocketPathForMode(stateDir, "headless"); got != filepath.Join(stateDir, "headless", "daemon.sock") {
		t.Fatalf("RuntimeSocketPathForMode headless = %q, want mode-specific daemon.sock", got)
	}
	if got := daemon.RuntimeLogPathForMode(stateDir, "headless"); got != filepath.Join(stateDir, "headless", "daemon.log") {
		t.Fatalf("RuntimeLogPathForMode headless = %q, want mode-specific daemon.log", got)
	}
}

func TestRuntimeOperationsAreModeAware(t *testing.T) {
	stateDir := t.TempDir()
	headedRuntime := daemon.Runtime{PID: 111, ConnectionMode: "browser_url", SocketPath: daemon.RuntimeSocketPath(stateDir)}
	headlessRuntime := daemon.Runtime{PID: 222, BrowserMode: "headless", ConnectionMode: "browser_url", SocketPath: daemon.RuntimeSocketPathForMode(stateDir, "headless")}

	if err := daemon.SaveRuntime(context.Background(), stateDir, headedRuntime); err != nil {
		t.Fatalf("SaveRuntime headed returned error: %v", err)
	}
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", headlessRuntime); err != nil {
		t.Fatalf("SaveRuntimeForMode headless returned error: %v", err)
	}

	loadedHeaded, ok, err := daemon.LoadRuntime(context.Background(), stateDir)
	if err != nil || !ok || loadedHeaded.PID != 111 || loadedHeaded.BrowserMode != "headed" {
		t.Fatalf("LoadRuntime headed = %+v, ok=%v, err=%v; want headed pid 111", loadedHeaded, ok, err)
	}
	loadedHeadless, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless")
	if err != nil || !ok || loadedHeadless.PID != 222 || loadedHeadless.BrowserMode != "headless" {
		t.Fatalf("LoadRuntimeForMode headless = %+v, ok=%v, err=%v; want headless pid 222", loadedHeadless, ok, err)
	}

	if err := daemon.ClearRuntimeForMode(context.Background(), stateDir, "headless", 111); err != nil {
		t.Fatalf("ClearRuntimeForMode mismatched pid returned error: %v", err)
	}
	if _, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless"); err != nil || !ok {
		t.Fatalf("headless runtime removed by mismatched pid: ok=%v err=%v", ok, err)
	}
	if err := daemon.ClearRuntimeForMode(context.Background(), stateDir, "headless", 222); err != nil {
		t.Fatalf("ClearRuntimeForMode headless returned error: %v", err)
	}
	if _, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless"); err != nil || ok {
		t.Fatalf("LoadRuntimeForMode headless after clear ok=%v err=%v, want missing", ok, err)
	}
	if _, ok, err := daemon.LoadRuntime(context.Background(), stateDir); err != nil || !ok {
		t.Fatalf("headed runtime was affected by clearing headless: ok=%v err=%v", ok, err)
	}
}

func TestProcessRunningReportsExitedProcess(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait helper process: %v", err)
	}
	if daemon.ProcessRunning(pid) {
		t.Fatalf("ProcessRunning(%d) = true, want false for exited helper process", pid)
	}
}

func TestSaveRuntimeDoesNotExposePartialStateToReaders(t *testing.T) {
	stateDir := t.TempDir()
	runtime := daemon.Runtime{
		PID:            os.Getpid(),
		StartedAt:      "2026-06-05T00:00:00Z",
		ConnectionMode: "browser_url",
		SocketPath:     daemon.RuntimeSocketPath(stateDir),
		Endpoint:       "ws://example.test/devtools/browser/" + strings.Repeat("x", 1<<20),
	}
	if err := daemon.SaveRuntime(context.Background(), stateDir, runtime); err != nil {
		t.Fatalf("initial SaveRuntime returned error: %v", err)
	}

	done := make(chan struct{})
	readerDone := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, ok, err := daemon.LoadRuntime(context.Background(), stateDir); err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			} else if !ok {
				select {
				case errCh <- errors.New("runtime unexpectedly missing"):
				default:
				}
				return
			}
		}
	}()

	for i := 0; i < 100; i++ {
		runtime.PID = os.Getpid() + i + 1
		if err := daemon.SaveRuntime(context.Background(), stateDir, runtime); err != nil {
			close(done)
			<-readerDone
			t.Fatalf("SaveRuntime iteration %d returned error: %v", i, err)
		}
		select {
		case err := <-errCh:
			close(done)
			<-readerDone
			t.Fatalf("LoadRuntime observed partial state: %v", err)
		default:
		}
	}
	close(done)
	<-readerDone
	select {
	case err := <-errCh:
		t.Fatalf("LoadRuntime observed partial state: %v", err)
	default:
	}
}

func TestRuntimeLogsAreModeAware(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(daemon.RuntimeLogPath(stateDir), []byte(`{"event":"headed_event"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write headed log returned error: %v", err)
	}
	headlessLogPath := daemon.RuntimeLogPathForMode(stateDir, "headless")
	if err := os.MkdirAll(filepath.Dir(headlessLogPath), 0o700); err != nil {
		t.Fatalf("create headless log directory returned error: %v", err)
	}
	if err := os.WriteFile(headlessLogPath, []byte(`{"event":"headless_event"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write headless log returned error: %v", err)
	}

	headedEntries, err := daemon.ReadLogs(context.Background(), stateDir, 10)
	if err != nil {
		t.Fatalf("ReadLogs headed returned error: %v", err)
	}
	if len(headedEntries) != 1 || headedEntries[0].Event != "headed_event" {
		t.Fatalf("ReadLogs headed = %+v, want headed_event only", headedEntries)
	}
	headlessEntries, err := daemon.ReadLogsForMode(context.Background(), stateDir, "headless", 10)
	if err != nil {
		t.Fatalf("ReadLogsForMode headless returned error: %v", err)
	}
	if len(headlessEntries) != 1 || headlessEntries[0].Event != "headless_event" {
		t.Fatalf("ReadLogsForMode headless = %+v, want headless_event only", headlessEntries)
	}
}

func TestRuntimeEndpointPersistsButDoesNotMarshal(t *testing.T) {
	stateDir := t.TempDir()
	wantEndpoint := "ws://example.test/devtools/browser/test"
	runtime := daemon.Runtime{
		PID:            os.Getpid(),
		StartedAt:      "2026-04-29T00:00:00Z",
		ConnectionMode: "auto_connect",
		SocketPath:     "daemon.sock",
		Endpoint:       wantEndpoint,
	}

	if err := daemon.SaveRuntime(context.Background(), stateDir, runtime); err != nil {
		t.Fatalf("SaveRuntime returned error: %v", err)
	}
	loaded, ok, err := daemon.LoadRuntime(context.Background(), stateDir)
	if err != nil {
		t.Fatalf("LoadRuntime returned error: %v", err)
	}
	if !ok || loaded.Endpoint != wantEndpoint {
		t.Fatalf("LoadRuntime endpoint = %q, ok=%v; want %q", loaded.Endpoint, ok, wantEndpoint)
	}

	b, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(b), "endpoint") || strings.Contains(string(b), wantEndpoint) {
		t.Fatalf("Runtime JSON exposed endpoint: %s", b)
	}
}

func TestRuntimeManagedMetadataRoundTripAndRedaction(t *testing.T) {
	stateDir := t.TempDir()
	runtime := daemon.Runtime{
		PID:                 os.Getpid(),
		StartedAt:           "2026-05-21T15:00:00Z",
		BrowserMode:         "headless",
		ConnectionMode:      "browser_url",
		SocketPath:          daemon.RuntimeSocketPathForMode(stateDir, "headless"),
		LogPath:             daemon.RuntimeLogPathForMode(stateDir, "headless"),
		Endpoint:            "ws://localhost/devtools/browser/test",
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		ManagedProfilePath:  browser.ManagedProfileDir(stateDir),
		ProfileSeedStrategy: "managed",
		ChromePID:           456,
		ChromePort:          "9222",
		ManagedBrowser: &browser.ManagedStatus{
			BrowserMode:         "headless",
			ChromePID:           456,
			StartedAt:           "2026-05-21T15:00:00Z",
			UserDataDir:         browser.ManagedProfileDir(stateDir),
			DebuggingPort:       "9222",
			ProfileSeedStrategy: "managed",
			LastSeededAt:        "2026-05-21T14:00:00Z",
		},
	}
	if err := daemon.SaveRuntime(context.Background(), stateDir, runtime); err != nil {
		t.Fatalf("SaveRuntime returned error: %v", err)
	}
	loaded, ok, err := daemon.LoadRuntime(context.Background(), stateDir)
	if err != nil {
		t.Fatalf("LoadRuntime returned error: %v", err)
	}
	if !ok || loaded.BrowserMode != "headless" || loaded.ChromePID != 456 || loaded.ChromePort != "9222" || loaded.ProfileSeedStrategy != "managed" {
		t.Fatalf("LoadRuntime() = %+v, want managed headless metadata", loaded)
	}
	if loaded.ManagedBrowser == nil || loaded.ManagedBrowser.DebuggingPort != "9222" || loaded.ManagedBrowser.ProfileSeedStrategy != "managed" {
		t.Fatalf("ManagedBrowser = %+v, want safe managed browser metadata", loaded.ManagedBrowser)
	}

	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(encoded), "devtools/browser/test") || strings.Contains(string(encoded), "endpoint") || strings.Contains(string(encoded), "ownership_token") || strings.Contains(string(encoded), "process_start_time") {
		t.Fatalf("Runtime JSON leaked internal endpoint or ownership metadata: %s", string(encoded))
	}
}

func TestHoldPersistsManagedKeepAliveMetadata(t *testing.T) {
	server := newRuntimeRPCFakeServer(t)
	defer server.Close()

	stateDir := shortStateDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	managedStatus := browser.ManagedStatus{
		BrowserMode:         "headless",
		ChromePID:           456,
		StartedAt:           "2026-05-21T15:00:00Z",
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		DebuggingPort:       "9222",
		ProfileSeedStrategy: "managed",
		LastSeededAt:        "2026-05-21T14:00:00Z",
	}
	managedJSON, err := json.Marshal(managedStatus)
	if err != nil {
		t.Fatalf("Marshal managed status returned error: %v", err)
	}
	t.Setenv("CDP_DAEMON_BROWSER_MODE", "headless")
	t.Setenv("CDP_DAEMON_USER_DATA_DIR", managedStatus.UserDataDir)
	t.Setenv("CDP_DAEMON_MANAGED_BROWSER", string(managedJSON))
	t.Setenv("CDP_DAEMON_MANAGED_PROFILE_PATH", managedStatus.UserDataDir)
	t.Setenv("CDP_DAEMON_PROFILE_SEED_STRATEGY", "managed")
	t.Setenv("CDP_DAEMON_CHROME_PID", "456")
	t.Setenv("CDP_DAEMON_CHROME_PORT", "9222")

	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeEndpoint(t, server.URL), "browser_url", 30*time.Second)
	}()
	runtime := waitForRuntimeForMode(t, ctx, stateDir, "headless")
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	if runtime.BrowserMode != "headless" || runtime.UserDataDir != managedStatus.UserDataDir || runtime.ChromePID != 456 || runtime.ChromePort != "9222" || runtime.ProfileSeedStrategy != "managed" {
		t.Fatalf("runtime = %+v, want managed headless metadata", runtime)
	}
	if runtime.ManagedBrowser == nil || runtime.ManagedBrowser.DebuggingPort != "9222" || runtime.ManagedBrowser.ProfileSeedStrategy != "managed" {
		t.Fatalf("ManagedBrowser = %+v, want safe managed browser metadata", runtime.ManagedBrowser)
	}
}

func TestReadLogsTailsJSONLines(t *testing.T) {
	stateDir := shortStateDir(t)
	content := strings.Join([]string{
		`{"time":"2026-04-29T00:00:00Z","level":"info","event":"hold_start","pid":123}`,
		`{"time":"2026-04-29T00:00:01Z","level":"info","event":"rpc_listening","pid":123}`,
	}, "\n") + "\n"
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(daemon.RuntimeLogPath(stateDir), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	entries, err := daemon.ReadLogs(context.Background(), stateDir, 1)
	if err != nil {
		t.Fatalf("ReadLogs returned error: %v", err)
	}
	if len(entries) != 1 || entries[0].Event != "rpc_listening" {
		t.Fatalf("ReadLogs = %+v, want last rpc_listening entry", entries)
	}

	empty, err := daemon.ReadLogs(context.Background(), t.TempDir(), 100)
	if err != nil {
		t.Fatalf("ReadLogs missing file returned error: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ReadLogs missing file = %+v, want empty entries", empty)
	}
}

const largeCDPResponseSize = 70 << 20

func TestRuntimeClientRPCErrorCompatibility(t *testing.T) {
	legacyRuntime := runtimeWithRPCResponse(t, daemon.RPCResponse{OK: false, Error: "legacy failure"})
	_, err := daemon.CallRuntime(context.Background(), legacyRuntime, "", "Test.legacy", nil)
	if err == nil || err.Error() != "legacy failure" {
		t.Fatalf("legacy CallRuntime error = %v, want legacy failure", err)
	}

	structuredRuntime := runtimeWithRPCResponse(t, daemon.RPCResponse{
		OK:    false,
		Error: "legacy structured failure",
		ErrorEnvelope: &daemon.RPCError{
			Code:    "daemon_rpc_failed",
			Class:   "connection",
			Message: "structured failure",
		},
	})
	_, err = daemon.CallRuntime(context.Background(), structuredRuntime, "", "Test.structured", nil)
	var rpcErr *daemon.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != "daemon_rpc_failed" || rpcErr.Class != "connection" || rpcErr.Error() != "structured failure" {
		t.Fatalf("structured CallRuntime error = %#v, want RPCError with code/class/message", err)
	}

	var decoded daemon.RPCResponse
	if err := json.Unmarshal([]byte(`{"ok":false,"error":"legacy","error_envelope":{"code":"daemon_rpc_failed","class":"connection","message":"structured failure"}}`), &decoded); err != nil {
		t.Fatalf("Unmarshal structured RPCResponse returned error: %v", err)
	}
	if decoded.Error != "legacy" || decoded.ErrorEnvelope == nil || decoded.ErrorEnvelope.Code != "daemon_rpc_failed" || decoded.ErrorEnvelope.Class != "connection" || decoded.ErrorEnvelope.Error() != "structured failure" {
		t.Fatalf("decoded RPCResponse = %+v, want legacy string plus structured envelope", decoded)
	}
}

func TestRuntimeClientStructuredContextErrors(t *testing.T) {
	timeoutRuntime := runtimeWithRPCResponse(t, daemon.RPCResponse{
		OK: false,
		ErrorEnvelope: &daemon.RPCError{
			Code:    "timeout",
			Class:   "timeout",
			Message: context.DeadlineExceeded.Error(),
		},
	})
	_, err := daemon.CallRuntime(context.Background(), timeoutRuntime, "", "Test.timeout", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("structured timeout error = %v, want context deadline exceeded", err)
	}

	canceledRuntime := runtimeWithRPCResponse(t, daemon.RPCResponse{
		OK: false,
		ErrorEnvelope: &daemon.RPCError{
			Code:    "canceled",
			Class:   "canceled",
			Message: context.Canceled.Error(),
		},
	})
	_, err = daemon.CallRuntime(context.Background(), canceledRuntime, "", "Test.canceled", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("structured canceled error = %v, want context canceled", err)
	}
}

func TestRuntimeClientEventAndProtocolRPC(t *testing.T) {
	server := newRuntimeRPCFakeServer(t)
	defer server.Close()

	stateDir := shortStateDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeEndpoint(t, server.URL), "browser_url", 30*time.Second)
	}()
	runtime := waitForRuntime(t, ctx, stateDir)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	client := daemon.RuntimeClient{Runtime: runtime}
	if err := client.CallSession(ctx, "session-1", "Runtime.enable", map[string]any{}, nil); err != nil {
		t.Fatalf("CallSession returned error: %v", err)
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		t.Fatalf("DrainEvents returned error: %v", err)
	}
	if len(events) != 1 || events[0].Method != "Runtime.consoleAPICalled" || events[0].SessionID != "session-1" {
		t.Fatalf("DrainEvents = %+v, want buffered console event", events)
	}

	readCtx, readCancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer readCancel()
	if _, err := client.ReadEvent(readCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReadEvent error = %v, want deadline exceeded", err)
	}

	protocol, err := client.FetchProtocol(ctx)
	if err != nil {
		t.Fatalf("FetchProtocol returned error: %v", err)
	}
	if protocol.Source != "daemon" || len(protocol.Domains) != 1 || protocol.Domains[0].Domain != "Runtime" {
		t.Fatalf("FetchProtocol = %+v, want daemon Runtime protocol", protocol)
	}
}

func TestRuntimeClientInvocationLeaseLifecycle(t *testing.T) {
	server := newRuntimeRPCFakeServer(t)
	defer server.Close()

	stateDir := shortStateDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeEndpoint(t, server.URL), "browser_url", 30*time.Second)
	}()
	runtime := waitForRuntime(t, ctx, stateDir)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	client := daemon.RuntimeClient{Runtime: runtime}
	leased, info, err := client.BeginLease(ctx, 30*time.Second)
	if err != nil {
		t.Fatalf("BeginLease: %v", err)
	}
	if info.LeaseID == "" || leased.LeaseID != info.LeaseID || info.TTLMillis != 30000 {
		t.Fatalf("lease info = %+v leased=%+v", info, leased)
	}
	if err := leased.Call(ctx, "Runtime.enable", map[string]any{}, nil); err != nil {
		t.Fatalf("leased Call: %v", err)
	}
	beforeDrain, err := readInvocationLeaseExpiry(t, stateDir, info.LeaseID)
	if err != nil {
		t.Fatalf("read lease expiry before DrainEvents: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	events, err := leased.DrainEvents(ctx)
	if err != nil {
		t.Fatalf("leased DrainEvents: %v", err)
	}
	if len(events) != 1 || events[0].Method != "Runtime.consoleAPICalled" {
		t.Fatalf("leased DrainEvents = %+v, want the daemon event", events)
	}
	afterDrain, err := readInvocationLeaseExpiry(t, stateDir, info.LeaseID)
	if err != nil {
		t.Fatalf("read lease expiry after DrainEvents: %v", err)
	}
	if !afterDrain.After(beforeDrain) {
		t.Fatalf("DrainEvents expiry = %s, want renewal after %s", afterDrain, beforeDrain)
	}
	time.Sleep(20 * time.Millisecond)
	readCtx, readCancel := context.WithTimeout(ctx, 20*time.Millisecond)
	if _, err := leased.ReadEvent(readCtx); !errors.Is(err, context.DeadlineExceeded) {
		readCancel()
		t.Fatalf("leased ReadEvent error = %v, want deadline exceeded", err)
	}
	readCancel()
	afterRead, err := readInvocationLeaseExpiry(t, stateDir, info.LeaseID)
	if err != nil {
		t.Fatalf("read lease expiry after ReadEvent: %v", err)
	}
	if !afterRead.After(afterDrain) {
		t.Fatalf("ReadEvent expiry = %s, want renewal after %s", afterRead, afterDrain)
	}
	renewed, err := leased.RenewLease(ctx, 45*time.Second)
	if err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	if renewed.LeaseID != info.LeaseID || renewed.TTLMillis != 45000 {
		t.Fatalf("renewed lease = %+v, want same id and 45-second TTL", renewed)
	}
	if err := leased.EndLease(ctx); err != nil {
		t.Fatalf("EndLease: %v", err)
	}
	if _, err := leased.RenewLease(ctx, time.Second); err == nil {
		t.Fatal("RenewLease after EndLease returned nil")
	}
}

func readInvocationLeaseExpiry(t *testing.T, stateDir, leaseID string) (time.Time, error) {
	t.Helper()
	b, err := os.ReadFile(daemon.InvocationLeasePathForMode(stateDir, "headed"))
	if err != nil {
		return time.Time{}, err
	}
	var state struct {
		Leases []struct {
			LeaseID   string `json:"lease_id"`
			ExpiresAt string `json:"expires_at"`
		} `json:"leases"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return time.Time{}, err
	}
	for _, lease := range state.Leases {
		if lease.LeaseID == leaseID {
			return time.Parse(time.RFC3339Nano, lease.ExpiresAt)
		}
	}
	return time.Time{}, errors.New("invocation lease not found")
}

func TestRuntimeClientReadsVeryLargeCDPResponsesAndStaysRunning(t *testing.T) {
	server := newRuntimeRPCLargeFakeServer(t)
	defer server.Close()

	stateDir := shortStateDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeEndpoint(t, server.URL), "browser_url", 30*time.Second)
	}()
	runtime := waitForRuntime(t, ctx, stateDir)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	client := daemon.RuntimeClient{Runtime: runtime}
	var screenshot struct {
		Data string `json:"data"`
	}
	if err := client.CallSession(ctx, "session-1", "Page.captureScreenshot", map[string]any{"format": "png"}, &screenshot); err != nil {
		t.Fatalf("CallSession Page.captureScreenshot returned error: %v", err)
	}
	if len(screenshot.Data) != largeCDPResponseSize {
		t.Fatalf("screenshot data length = %d, want %d", len(screenshot.Data), largeCDPResponseSize)
	}

	var targets struct {
		TargetInfos []cdp.TargetInfo `json:"targetInfos"`
	}
	if err := client.Call(ctx, "Target.getTargets", map[string]any{}, &targets); err != nil {
		t.Fatalf("follow-up Target.getTargets returned error: %v", err)
	}
	if len(targets.TargetInfos) != 1 || targets.TargetInfos[0].TargetID != "page-1" {
		t.Fatalf("Target.getTargets = %+v, want page-1", targets.TargetInfos)
	}
	if !daemon.RuntimeRunning(runtime) || !daemon.RuntimeSocketReady(ctx, runtime) {
		t.Fatalf("daemon runtime stopped after large response")
	}
}

func TestRuntimeClientAllowsConcurrentRPCCalls(t *testing.T) {
	server, maxActive := newRuntimeRPCConcurrentFakeServer(t)
	defer server.Close()

	stateDir := shortStateDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeEndpoint(t, server.URL), "browser_url", 30*time.Second)
	}()
	runtime := waitForRuntime(t, ctx, stateDir)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	client := daemon.RuntimeClient{Runtime: runtime}
	const calls = 5
	errs := make(chan error, calls)
	for i := 0; i < calls; i++ {
		go func(i int) {
			var result struct {
				Value int `json:"value"`
			}
			err := client.Call(ctx, "Runtime.evaluate", map[string]any{"i": i}, &result)
			if err == nil && result.Value != i {
				err = errors.New("unexpected concurrent result value")
			}
			errs <- err
		}(i)
	}
	for i := 0; i < calls; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Runtime.evaluate returned error: %v", err)
		}
	}
	if maxActive.Load() < 2 {
		t.Fatalf("max concurrent CDP calls = %d, want daemon to allow overlapping runtime RPC calls", maxActive.Load())
	}
}

func newRuntimeRPCFakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/json/protocol", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(cdp.Protocol{
			Version: cdp.ProtocolVersion{Major: "1", Minor: "3"},
			Domains: []cdp.Domain{
				{Domain: "Runtime"},
			},
		})
	})
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			var req struct {
				ID        int64           `json:"id"`
				SessionID string          `json:"sessionId"`
				Method    string          `json:"method"`
				Params    json.RawMessage `json:"params"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			if req.Method == "Runtime.enable" {
				event := map[string]any{
					"sessionId": req.SessionID,
					"method":    "Runtime.consoleAPICalled",
					"params": map[string]any{
						"type": "error",
						"args": []map[string]any{{"type": "string", "value": "daemon event"}},
					},
				}
				if err := wsjson.Write(r.Context(), conn, event); err != nil {
					return
				}
			}
			resp := map[string]any{"id": req.ID, "result": map[string]any{}}
			if req.SessionID != "" {
				resp["sessionId"] = req.SessionID
			}
			if err := wsjson.Write(r.Context(), conn, resp); err != nil {
				return
			}
		}
	})
	return httptest.NewServer(mux)
}

func newRuntimeRPCConcurrentFakeServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var active atomic.Int64
	var maxActive atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		var writeMu sync.Mutex
		for {
			var req struct {
				ID        int64           `json:"id"`
				SessionID string          `json:"sessionId"`
				Method    string          `json:"method"`
				Params    json.RawMessage `json:"params"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			go func(req struct {
				ID        int64           `json:"id"`
				SessionID string          `json:"sessionId"`
				Method    string          `json:"method"`
				Params    json.RawMessage `json:"params"`
			}) {
				now := active.Add(1)
				for {
					previous := maxActive.Load()
					if now <= previous || maxActive.CompareAndSwap(previous, now) {
						break
					}
				}
				defer active.Add(-1)
				time.Sleep(100 * time.Millisecond)
				var params struct {
					I int `json:"i"`
				}
				_ = json.Unmarshal(req.Params, &params)
				resp := map[string]any{"id": req.ID, "result": map[string]any{"value": params.I}}
				if req.SessionID != "" {
					resp["sessionId"] = req.SessionID
				}
				writeMu.Lock()
				defer writeMu.Unlock()
				_ = wsjson.Write(r.Context(), conn, resp)
			}(req)
		}
	})
	return httptest.NewServer(mux), &maxActive
}

func newRuntimeRPCLargeFakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/browser/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			var req struct {
				ID        int64  `json:"id"`
				SessionID string `json:"sessionId"`
				Method    string `json:"method"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			result := map[string]any{}
			switch req.Method {
			case "Page.captureScreenshot":
				result["data"] = strings.Repeat("x", largeCDPResponseSize)
			case "Target.getTargets":
				result["targetInfos"] = []map[string]any{{"targetId": "page-1", "type": "page", "url": "https://example.test/"}}
			}
			resp := map[string]any{"id": req.ID, "result": result}
			if req.SessionID != "" {
				resp["sessionId"] = req.SessionID
			}
			if err := wsjson.Write(r.Context(), conn, resp); err != nil {
				return
			}
		}
	})
	return httptest.NewServer(mux)
}

func runtimeWithRPCResponse(t *testing.T, response daemon.RPCResponse) daemon.Runtime {
	t.Helper()
	stateDir := shortStateDir(t)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	socketPath := filepath.Join(stateDir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req daemon.RPCRequest
		_ = json.NewDecoder(conn).Decode(&req)
		_ = json.NewEncoder(conn).Encode(response)
	}()
	return daemon.Runtime{SocketPath: socketPath}
}

func fakeEndpoint(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	u.Scheme = "ws"
	u.Path = "/devtools/browser/test"
	return u.String()
}

func shortStateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cdp-cli-daemon-*")
	if err != nil {
		t.Fatalf("MkdirTemp returned error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "state")
}

func waitForRuntime(t *testing.T, ctx context.Context, stateDir string) daemon.Runtime {
	t.Helper()
	return waitForRuntimeForMode(t, ctx, stateDir, "headed")
}

func waitForRuntimeForMode(t *testing.T, ctx context.Context, stateDir, browserMode string) daemon.Runtime {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime, ok, err := daemon.LoadRuntimeForMode(ctx, stateDir, browserMode)
		if err != nil {
			t.Fatalf("LoadRuntimeForMode returned error: %v", err)
		}
		if ok && daemon.RuntimeRunning(runtime) && daemon.RuntimeSocketReady(ctx, runtime) {
			return runtime
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("daemon runtime did not become ready")
	return daemon.Runtime{}
}
