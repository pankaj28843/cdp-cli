package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
)

const RuntimeFileName = "daemon.json"
const RuntimeSocketFileName = "daemon.sock"
const RuntimeLogFileName = "daemon.log"

const (
	RPCMethodDrainEvents          = "Daemon.drainEvents"
	RPCMethodReadEvent            = "Daemon.readEvent"
	RPCMethodFetchProtocol        = "Daemon.fetchProtocol"
	RPCMethodBeginInvocationLease = "Daemon.beginInvocationLease"
	RPCMethodRenewInvocationLease = "Daemon.renewInvocationLease"
	RPCMethodEndInvocationLease   = "Daemon.endInvocationLease"
	RPCMethodMarkTargetDisposable = "Daemon.markTargetDisposable"
	RPCMethodMarkTargetPersistent = "Daemon.markTargetPersistent"
	RPCMethodEnableWindowMarker   = "Daemon.enableWindowMarker"
	RPCMethodDisableWindowMarker  = "Daemon.disableWindowMarker"
	RPCMethodWindowMarkerStatus   = "Daemon.windowMarkerStatus"
)

type Runtime struct {
	PID                 int                    `json:"pid"`
	StartedAt           string                 `json:"started_at"`
	BrowserMode         string                 `json:"browser_mode,omitempty"`
	ConnectionMode      string                 `json:"connection_mode"`
	ReconnectInterval   string                 `json:"reconnect_interval,omitempty"`
	SocketPath          string                 `json:"socket_path,omitempty"`
	LogPath             string                 `json:"log_path,omitempty"`
	Endpoint            string                 `json:"-"`
	UserDataDir         string                 `json:"user_data_dir,omitempty"`
	ManagedBrowser      *browser.ManagedStatus `json:"managed_browser,omitempty"`
	ManagedProfilePath  string                 `json:"managed_profile_path,omitempty"`
	ProfileSeedStrategy string                 `json:"profile_seed_strategy,omitempty"`
	ChromePID           int                    `json:"chrome_pid,omitempty"`
	ChromePort          string                 `json:"chrome_port,omitempty"`
}

type KeepAliveMetadata struct {
	UserDataDir         string
	ManagedBrowser      *browser.ManagedStatus
	ManagedProfilePath  string
	ProfileSeedStrategy string
	ChromePID           int
	ChromePort          string
}

type runtimeFile struct {
	PID                 int                    `json:"pid"`
	StartedAt           string                 `json:"started_at"`
	BrowserMode         string                 `json:"browser_mode,omitempty"`
	ConnectionMode      string                 `json:"connection_mode"`
	ReconnectInterval   string                 `json:"reconnect_interval,omitempty"`
	SocketPath          string                 `json:"socket_path,omitempty"`
	LogPath             string                 `json:"log_path,omitempty"`
	Endpoint            string                 `json:"endpoint,omitempty"`
	UserDataDir         string                 `json:"user_data_dir,omitempty"`
	ManagedBrowser      *browser.ManagedStatus `json:"managed_browser,omitempty"`
	ManagedProfilePath  string                 `json:"managed_profile_path,omitempty"`
	ProfileSeedStrategy string                 `json:"profile_seed_strategy,omitempty"`
	ChromePID           int                    `json:"chrome_pid,omitempty"`
	ChromePort          string                 `json:"chrome_port,omitempty"`
}

type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Event   string `json:"event"`
	Message string `json:"message,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

type RPCRequest struct {
	Method        string          `json:"method"`
	SessionID     string          `json:"session_id,omitempty"`
	OwnerID       string          `json:"owner_id,omitempty"`
	Params        json.RawMessage `json:"params,omitempty"`
	TimeoutMillis int64           `json:"timeout_ms,omitempty"`
}

type RPCResponse struct {
	OK            bool            `json:"ok"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
	ErrorEnvelope *RPCError       `json:"error_envelope,omitempty"`
}

type RPCError struct {
	Code    string `json:"code,omitempty"`
	Class   string `json:"class,omitempty"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "daemon rpc call failed"
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	if strings.TrimSpace(e.Code) != "" {
		return e.Code
	}
	if strings.TrimSpace(e.Class) != "" {
		return e.Class
	}
	return "daemon rpc call failed"
}

type RuntimeClient struct {
	Runtime Runtime
	LeaseID string
}

// IsInvocationLeaseUnsupported reports the compatibility error returned by a
// daemon created before invocation leases were added. A newer client can still
// make a bounded browser call through that daemon, but it cannot attribute or
// clean up targets through the lease protocol until the daemon is refreshed.
func IsInvocationLeaseUnsupported(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, strings.ToLower(RPCMethodBeginInvocationLease)) &&
		(strings.Contains(message, "-32601") || strings.Contains(message, "not found"))
}

type holdOptions struct {
	fetchProtocolFallback func(context.Context) (cdp.Protocol, error)
}

func defaultHoldOptions() holdOptions {
	return holdOptions{fetchProtocolFallback: cdp.FetchOfficialProtocol}
}

func RuntimePath(stateDir string) string {
	return RuntimePathForMode(stateDir, "headed")
}

func RuntimeSocketPath(stateDir string) string {
	return RuntimeSocketPathForMode(stateDir, "headed")
}

func RuntimeLogPath(stateDir string) string {
	return RuntimeLogPathForMode(stateDir, "headed")
}

func RuntimePathForMode(stateDir, browserMode string) string {
	if runtimeModeName(browserMode) == "headless" {
		return filepath.Join(stateDir, "headless", RuntimeFileName)
	}
	return filepath.Join(stateDir, RuntimeFileName)
}

func RuntimeSocketPathForMode(stateDir, browserMode string) string {
	if runtimeModeName(browserMode) == "headless" {
		return filepath.Join(stateDir, "headless", RuntimeSocketFileName)
	}
	return filepath.Join(stateDir, RuntimeSocketFileName)
}

func RuntimeLogPathForMode(stateDir, browserMode string) string {
	if runtimeModeName(browserMode) == "headless" {
		return filepath.Join(stateDir, "headless", RuntimeLogFileName)
	}
	return filepath.Join(stateDir, RuntimeLogFileName)
}

func runtimeModeName(browserMode string) string {
	if strings.EqualFold(strings.TrimSpace(browserMode), "headless") {
		return "headless"
	}
	return "headed"
}

func LoadRuntime(ctx context.Context, stateDir string) (Runtime, bool, error) {
	return LoadRuntimeForMode(ctx, stateDir, "headed")
}

func LoadRuntimeForMode(ctx context.Context, stateDir, browserMode string) (Runtime, bool, error) {
	select {
	case <-ctx.Done():
		return Runtime{}, false, ctx.Err()
	default:
	}

	b, err := os.ReadFile(RuntimePathForMode(stateDir, browserMode))
	if err != nil {
		if os.IsNotExist(err) {
			return Runtime{}, false, nil
		}
		return Runtime{}, false, fmt.Errorf("read daemon runtime state: %w", err)
	}
	var file runtimeFile
	if err := json.Unmarshal(b, &file); err != nil {
		return Runtime{}, false, fmt.Errorf("parse daemon runtime state: %w", err)
	}
	return runtimeFromFile(file), true, nil
}

func SaveRuntime(ctx context.Context, stateDir string, runtime Runtime) error {
	return SaveRuntimeForMode(ctx, stateDir, "headed", runtime)
}

func SaveRuntimeForMode(ctx context.Context, stateDir, browserMode string, runtime Runtime) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	runtimePath := RuntimePathForMode(stateDir, browserMode)
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o700); err != nil {
		return fmt.Errorf("create daemon state directory: %w", err)
	}
	if strings.TrimSpace(runtime.BrowserMode) == "" {
		runtime.BrowserMode = runtimeModeName(browserMode)
	}
	b, err := json.MarshalIndent(runtimeFileFromRuntime(runtime), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal daemon runtime state: %w", err)
	}
	b = append(b, '\n')
	if err := writeFileAtomic(runtimePath, b, 0o600); err != nil {
		return fmt.Errorf("write daemon runtime state: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func runtimeFromFile(file runtimeFile) Runtime {
	return Runtime{
		PID:                 file.PID,
		StartedAt:           file.StartedAt,
		BrowserMode:         file.BrowserMode,
		ConnectionMode:      file.ConnectionMode,
		ReconnectInterval:   file.ReconnectInterval,
		SocketPath:          file.SocketPath,
		LogPath:             file.LogPath,
		Endpoint:            file.Endpoint,
		UserDataDir:         file.UserDataDir,
		ManagedBrowser:      file.ManagedBrowser,
		ManagedProfilePath:  file.ManagedProfilePath,
		ProfileSeedStrategy: file.ProfileSeedStrategy,
		ChromePID:           file.ChromePID,
		ChromePort:          file.ChromePort,
	}
}

func runtimeFileFromRuntime(runtime Runtime) runtimeFile {
	return runtimeFile{
		PID:                 runtime.PID,
		StartedAt:           runtime.StartedAt,
		BrowserMode:         runtime.BrowserMode,
		ConnectionMode:      runtime.ConnectionMode,
		ReconnectInterval:   runtime.ReconnectInterval,
		SocketPath:          runtime.SocketPath,
		LogPath:             runtime.LogPath,
		Endpoint:            runtime.Endpoint,
		UserDataDir:         runtime.UserDataDir,
		ManagedBrowser:      runtime.ManagedBrowser,
		ManagedProfilePath:  runtime.ManagedProfilePath,
		ProfileSeedStrategy: runtime.ProfileSeedStrategy,
		ChromePID:           runtime.ChromePID,
		ChromePort:          runtime.ChromePort,
	}
}

func ClearRuntime(ctx context.Context, stateDir string, pid int) error {
	return ClearRuntimeForMode(ctx, stateDir, "headed", pid)
}

func ClearRuntimeForMode(ctx context.Context, stateDir, browserMode string, pid int) error {
	runtime, ok, err := LoadRuntimeForMode(ctx, stateDir, browserMode)
	if err != nil || !ok {
		return err
	}
	if pid > 0 && runtime.PID != pid {
		return nil
	}
	if err := os.Remove(RuntimePathForMode(stateDir, browserMode)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove daemon runtime state: %w", err)
	}
	return nil
}

func RuntimeRunning(runtime Runtime) bool {
	return ProcessRunning(runtime.PID)
}

func ProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

func StartKeepAlive(ctx context.Context, executable, stateDir, endpoint, connectionMode, userDataDir string, reconnect time.Duration) (Runtime, bool, error) {
	return StartKeepAliveForMode(ctx, executable, stateDir, "headed", endpoint, connectionMode, userDataDir, reconnect)
}

func StartKeepAliveForMode(ctx context.Context, executable, stateDir, browserMode, endpoint, connectionMode, userDataDir string, reconnect time.Duration) (Runtime, bool, error) {
	return StartKeepAliveForModeWithMetadata(ctx, executable, stateDir, browserMode, endpoint, connectionMode, KeepAliveMetadata{UserDataDir: userDataDir}, reconnect)
}

func StartKeepAliveForModeWithMetadata(ctx context.Context, executable, stateDir, browserMode, endpoint, connectionMode string, metadata KeepAliveMetadata, reconnect time.Duration) (Runtime, bool, error) {
	if runtime, ok, err := LoadRuntimeForMode(ctx, stateDir, browserMode); err != nil {
		return Runtime{}, false, err
	} else if ok && RuntimeRunning(runtime) {
		if runtimeMatchesKeepAliveRequest(runtime, browserMode, endpoint, connectionMode, metadata, reconnect) {
			if RuntimeSocketReady(ctx, runtime) {
				return runtime, true, nil
			}
			if ready, waitErr := waitForRuntimeSocket(ctx, runtime); waitErr == nil {
				return ready, true, nil
			} else {
				return Runtime{}, true, fmt.Errorf("existing daemon keepalive did not become ready: %w", waitErr)
			}
		}
		if _, stopped, stopErr := StopRuntimeForMode(ctx, stateDir, browserMode); stopErr != nil {
			return Runtime{}, true, fmt.Errorf("stop mismatched daemon keepalive: %w", stopErr)
		} else if !stopped || RuntimeRunning(runtime) {
			return Runtime{}, true, fmt.Errorf("mismatched daemon keepalive did not stop")
		}
	}

	cmd := exec.Command(executable, "daemon", "hold")
	socketPath := RuntimeSocketPathForMode(stateDir, browserMode)
	managedBrowser := ""
	if metadata.ManagedBrowser != nil {
		if b, err := json.Marshal(metadata.ManagedBrowser); err == nil {
			managedBrowser = string(b)
		}
	}
	cmd.Env = append(os.Environ(),
		"CDP_DAEMON_HOLD_ENDPOINT="+endpoint,
		"CDP_DAEMON_STATE_DIR="+stateDir,
		"CDP_DAEMON_CONNECTION_MODE="+connectionMode,
		"CDP_DAEMON_RECONNECT="+reconnect.String(),
		"CDP_DAEMON_SOCKET="+socketPath,
		"CDP_DAEMON_BROWSER_MODE="+runtimeModeName(browserMode),
		"CDP_DAEMON_USER_DATA_DIR="+metadata.UserDataDir,
		"CDP_DAEMON_MANAGED_BROWSER="+managedBrowser,
		"CDP_DAEMON_MANAGED_PROFILE_PATH="+metadata.ManagedProfilePath,
		"CDP_DAEMON_PROFILE_SEED_STRATEGY="+metadata.ProfileSeedStrategy,
		"CDP_DAEMON_CHROME_PID="+strconv.Itoa(metadata.ChromePID),
		"CDP_DAEMON_CHROME_PORT="+metadata.ChromePort,
	)
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return Runtime{}, false, fmt.Errorf("open null device: %w", err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return Runtime{}, false, fmt.Errorf("start daemon keepalive process: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	runtime, err := waitForRuntimeForMode(ctx, stateDir, browserMode, pid)
	if err != nil {
		if process, findErr := os.FindProcess(pid); findErr == nil {
			_ = process.Kill()
		}
		_ = ClearRuntimeForMode(context.Background(), stateDir, browserMode, pid)
		return Runtime{}, false, err
	}
	return runtime, false, nil
}

func waitForRuntimeSocket(ctx context.Context, runtime Runtime) (Runtime, error) {
	for {
		if !RuntimeRunning(runtime) {
			return Runtime{}, fmt.Errorf("daemon keepalive process exited")
		}
		if RuntimeSocketReady(ctx, runtime) {
			return runtime, nil
		}
		select {
		case <-ctx.Done():
			return Runtime{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func runtimeMatchesKeepAliveRequest(runtime Runtime, browserMode, endpoint, connectionMode string, metadata KeepAliveMetadata, reconnect time.Duration) bool {
	if runtimeModeName(runtime.BrowserMode) != runtimeModeName(browserMode) {
		return false
	}
	if strings.TrimSpace(runtime.ConnectionMode) != strings.TrimSpace(connectionMode) {
		return false
	}
	if strings.TrimSpace(endpoint) != "" && strings.TrimSpace(runtime.Endpoint) != strings.TrimSpace(endpoint) {
		return false
	}
	if reconnect > 0 && runtime.ReconnectInterval != durationString(reconnect) {
		return false
	}
	if strings.TrimSpace(metadata.UserDataDir) != "" && runtime.UserDataDir != metadata.UserDataDir {
		return false
	}
	if strings.TrimSpace(metadata.ManagedProfilePath) != "" && runtime.ManagedProfilePath != metadata.ManagedProfilePath {
		return false
	}
	if strings.TrimSpace(metadata.ProfileSeedStrategy) != "" && runtime.ProfileSeedStrategy != metadata.ProfileSeedStrategy {
		return false
	}
	if metadata.ChromePID > 0 && runtime.ChromePID != metadata.ChromePID {
		return false
	}
	if strings.TrimSpace(metadata.ChromePort) != "" && runtime.ChromePort != metadata.ChromePort {
		return false
	}
	if metadata.ManagedBrowser != nil {
		if runtime.ManagedBrowser == nil {
			return false
		}
		if strings.TrimSpace(metadata.ManagedBrowser.DebuggingPort) != "" && runtime.ManagedBrowser.DebuggingPort != metadata.ManagedBrowser.DebuggingPort {
			return false
		}
		if strings.TrimSpace(metadata.ManagedBrowser.ProfileSeedStrategy) != "" && runtime.ManagedBrowser.ProfileSeedStrategy != metadata.ManagedBrowser.ProfileSeedStrategy {
			return false
		}
	}
	return true
}

func StopRuntime(ctx context.Context, stateDir string) (Runtime, bool, error) {
	return StopRuntimeForMode(ctx, stateDir, "headed")
}

func StopRuntimeForMode(ctx context.Context, stateDir, browserMode string) (Runtime, bool, error) {
	runtime, ok, err := LoadRuntimeForMode(ctx, stateDir, browserMode)
	if err != nil || !ok {
		return Runtime{}, false, err
	}
	if !RuntimeRunning(runtime) {
		_ = os.Remove(runtime.SocketPath)
		return runtime, false, ClearRuntimeForMode(ctx, stateDir, browserMode, runtime.PID)
	}
	process, err := os.FindProcess(runtime.PID)
	if err != nil {
		_ = os.Remove(runtime.SocketPath)
		return runtime, false, ClearRuntimeForMode(ctx, stateDir, browserMode, runtime.PID)
	}
	if err := process.Signal(os.Interrupt); err != nil {
		if killErr := process.Kill(); killErr != nil {
			return runtime, true, fmt.Errorf("stop daemon process: interrupt: %v; kill: %w", err, killErr)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !RuntimeRunning(runtime) {
			_ = os.Remove(runtime.SocketPath)
			return runtime, true, ClearRuntimeForMode(ctx, stateDir, browserMode, runtime.PID)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := process.Kill(); err != nil {
		return runtime, true, fmt.Errorf("kill daemon process: %w", err)
	}
	_ = os.Remove(runtime.SocketPath)
	return runtime, true, ClearRuntimeForMode(ctx, stateDir, browserMode, runtime.PID)
}

func Hold(ctx context.Context, stateDir, endpoint, connectionMode string, reconnect time.Duration) error {
	return holdWithOptions(ctx, stateDir, endpoint, connectionMode, reconnect, defaultHoldOptions())
}

func holdWithOptions(ctx context.Context, stateDir, endpoint, connectionMode string, reconnect time.Duration, opts holdOptions) error {
	if opts.fetchProtocolFallback == nil {
		opts.fetchProtocolFallback = cdp.FetchOfficialProtocol
	}
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("daemon hold endpoint is required")
	}
	if strings.TrimSpace(stateDir) == "" {
		return fmt.Errorf("daemon hold state directory is required")
	}
	browserMode := runtimeModeName(os.Getenv("CDP_DAEMON_BROWSER_MODE"))
	pid := os.Getpid()
	defer ClearRuntimeForMode(context.Background(), stateDir, browserMode, pid)
	appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "info", Event: "hold_start", Message: "daemon hold process starting", PID: pid})

	socketPath := os.Getenv("CDP_DAEMON_SOCKET")
	if strings.TrimSpace(socketPath) == "" {
		socketPath = RuntimeSocketPathForMode(stateDir, browserMode)
	}

	for {
		client, err := cdp.Dial(ctx, endpoint)
		if err == nil {
			appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "info", Event: "browser_connected", Message: "connected to browser endpoint", PID: pid})
			err = holdConnection(ctx, stateDir, socketPath, client, pid, connectionMode, reconnect, opts)
			if err != nil {
				appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "warn", Event: "hold_connection_ended", Message: err.Error(), PID: pid})
			}
			_ = ClearRuntimeForMode(context.Background(), stateDir, browserMode, pid)
		} else {
			appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "warn", Event: "browser_dial_failed", Message: err.Error(), PID: pid})
		}
		if reconnect <= 0 {
			return err
		}
		select {
		case <-ctx.Done():
			appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "info", Event: "hold_stop", Message: ctx.Err().Error(), PID: pid})
			return ctx.Err()
		case <-time.After(reconnect):
			appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "info", Event: "reconnect_wait_elapsed", Message: "attempting browser reconnect", PID: pid})
		}
	}
}

func HoldFromEnv(ctx context.Context) error {
	reconnect, err := time.ParseDuration(os.Getenv("CDP_DAEMON_RECONNECT"))
	if err != nil && os.Getenv("CDP_DAEMON_RECONNECT") != "" {
		return fmt.Errorf("parse CDP_DAEMON_RECONNECT: %w", err)
	}
	return Hold(ctx, os.Getenv("CDP_DAEMON_STATE_DIR"), os.Getenv("CDP_DAEMON_HOLD_ENDPOINT"), os.Getenv("CDP_DAEMON_CONNECTION_MODE"), reconnect)
}

func (c RuntimeClient) Call(ctx context.Context, method string, params any, result any) error {
	return c.CallSession(ctx, "", method, params, result)
}

func (c RuntimeClient) CallSession(ctx context.Context, sessionID, method string, params any, result any) error {
	raw, err := CallRuntimeWithOwner(ctx, c.Runtime, c.LeaseID, sessionID, method, params)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`null`)
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("decode daemon rpc response %s: %w", method, err)
	}
	return nil
}

func (c RuntimeClient) BeginLease(ctx context.Context, ttl time.Duration) (RuntimeClient, LeaseInfo, error) {
	params := map[string]any{}
	if ttl > 0 {
		params["ttl_ms"] = ttl.Milliseconds()
	}
	raw, err := CallRuntimeWithOwner(ctx, c.Runtime, "", "", RPCMethodBeginInvocationLease, params)
	if err != nil {
		return RuntimeClient{}, LeaseInfo{}, err
	}
	var info LeaseInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return RuntimeClient{}, LeaseInfo{}, fmt.Errorf("decode daemon invocation lease: %w", err)
	}
	if strings.TrimSpace(info.LeaseID) == "" {
		return RuntimeClient{}, LeaseInfo{}, fmt.Errorf("daemon returned an empty invocation lease id")
	}
	return RuntimeClient{Runtime: c.Runtime, LeaseID: info.LeaseID}, info, nil
}

func (c RuntimeClient) RenewLease(ctx context.Context, ttl time.Duration) (LeaseInfo, error) {
	if strings.TrimSpace(c.LeaseID) == "" {
		return LeaseInfo{}, fmt.Errorf("runtime client has no invocation lease")
	}
	params := map[string]any{}
	if ttl > 0 {
		params["ttl_ms"] = ttl.Milliseconds()
	}
	raw, err := CallRuntimeWithOwner(ctx, c.Runtime, c.LeaseID, "", RPCMethodRenewInvocationLease, params)
	if err != nil {
		return LeaseInfo{}, err
	}
	var info LeaseInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return LeaseInfo{}, fmt.Errorf("decode renewed daemon invocation lease: %w", err)
	}
	return info, nil
}

func (c RuntimeClient) EndLease(ctx context.Context) error {
	if strings.TrimSpace(c.LeaseID) == "" {
		return nil
	}
	raw, err := CallRuntimeWithOwner(ctx, c.Runtime, c.LeaseID, "", RPCMethodEndInvocationLease, nil)
	if err != nil {
		return err
	}
	var result LeaseEndResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode ended daemon invocation lease: %w", err)
	}
	return result.Error()
}

func (c RuntimeClient) MarkTargetDisposable(ctx context.Context, targetID string) error {
	return c.markTargetPolicy(ctx, RPCMethodMarkTargetDisposable, targetID)
}

func (c RuntimeClient) MarkTargetPersistent(ctx context.Context, targetID string) error {
	return c.markTargetPolicy(ctx, RPCMethodMarkTargetPersistent, targetID)
}

func (c RuntimeClient) markTargetPolicy(ctx context.Context, method, targetID string) error {
	if strings.TrimSpace(c.LeaseID) == "" {
		return nil
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return fmt.Errorf("target id is required")
	}
	_, err := CallRuntimeWithOwner(ctx, c.Runtime, c.LeaseID, "", method, map[string]any{"target_id": targetID})
	return err
}

func (c RuntimeClient) DrainEvents(ctx context.Context) ([]cdp.Event, error) {
	raw, err := CallRuntime(ctx, c.Runtime, "", RPCMethodDrainEvents, nil)
	if err != nil {
		return nil, err
	}
	var events []cdp.Event
	if len(raw) == 0 {
		return events, nil
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, fmt.Errorf("decode daemon rpc response %s: %w", RPCMethodDrainEvents, err)
	}
	return events, nil
}

func (c RuntimeClient) ReadEvent(ctx context.Context) (cdp.Event, error) {
	raw, err := CallRuntime(ctx, c.Runtime, "", RPCMethodReadEvent, nil)
	if err != nil {
		return cdp.Event{}, err
	}
	var event cdp.Event
	if err := json.Unmarshal(raw, &event); err != nil {
		return cdp.Event{}, fmt.Errorf("decode daemon rpc response %s: %w", RPCMethodReadEvent, err)
	}
	return event, nil
}

func (c RuntimeClient) FetchProtocol(ctx context.Context) (cdp.Protocol, error) {
	raw, err := CallRuntime(ctx, c.Runtime, "", RPCMethodFetchProtocol, nil)
	if err != nil {
		return cdp.Protocol{}, err
	}
	var protocol cdp.Protocol
	if err := json.Unmarshal(raw, &protocol); err != nil {
		return cdp.Protocol{}, fmt.Errorf("decode daemon rpc response %s: %w", RPCMethodFetchProtocol, err)
	}
	return protocol, nil
}

func (c RuntimeClient) EnableWindowMarker(ctx context.Context, name string) (WindowMarkerStatus, error) {
	raw, err := CallRuntimeWithOwner(ctx, c.Runtime, c.LeaseID, "", RPCMethodEnableWindowMarker, map[string]any{"name": name})
	if err != nil {
		return WindowMarkerStatus{}, err
	}
	var status WindowMarkerStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return WindowMarkerStatus{}, fmt.Errorf("decode daemon window marker status: %w", err)
	}
	return status, nil
}

func (c RuntimeClient) DisableWindowMarker(ctx context.Context) (WindowMarkerStatus, error) {
	raw, err := CallRuntimeWithOwner(ctx, c.Runtime, c.LeaseID, "", RPCMethodDisableWindowMarker, nil)
	if err != nil {
		return WindowMarkerStatus{}, err
	}
	var status WindowMarkerStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return WindowMarkerStatus{}, fmt.Errorf("decode daemon window marker status: %w", err)
	}
	return status, nil
}

func (c RuntimeClient) WindowMarkerStatus(ctx context.Context) (WindowMarkerStatus, error) {
	raw, err := CallRuntimeWithOwner(ctx, c.Runtime, c.LeaseID, "", RPCMethodWindowMarkerStatus, nil)
	if err != nil {
		return WindowMarkerStatus{}, err
	}
	var status WindowMarkerStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return WindowMarkerStatus{}, fmt.Errorf("decode daemon window marker status: %w", err)
	}
	return status, nil
}

func CallRuntime(ctx context.Context, runtime Runtime, sessionID, method string, params any) (json.RawMessage, error) {
	return CallRuntimeWithOwner(ctx, runtime, "", sessionID, method, params)
}

func CallRuntimeWithOwner(ctx context.Context, runtime Runtime, ownerID, sessionID, method string, params any) (json.RawMessage, error) {
	if strings.TrimSpace(runtime.SocketPath) == "" {
		return nil, fmt.Errorf("daemon runtime does not expose an rpc socket; restart the daemon")
	}
	rawParams, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", runtime.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("connect daemon rpc socket: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	req := RPCRequest{
		Method:        method,
		SessionID:     sessionID,
		OwnerID:       strings.TrimSpace(ownerID),
		Params:        rawParams,
		TimeoutMillis: timeoutMillis(ctx),
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("write daemon rpc request %s: %w", method, err)
	}
	var resp RPCResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("read daemon rpc response %s: %w", method, err)
	}
	if !resp.OK {
		return nil, errorFromRPCResponse(resp)
	}
	return resp.Result, nil
}

func errorFromRPCResponse(resp RPCResponse) error {
	if resp.ErrorEnvelope != nil {
		rpcErr := *resp.ErrorEnvelope
		if strings.TrimSpace(rpcErr.Message) == "" {
			rpcErr.Message = resp.Error
		}
		if err := rpcContextError(rpcErr.Code, rpcErr.Class, rpcErr.Error()); err != nil {
			return err
		}
		return &rpcErr
	}
	message := resp.Error
	if strings.TrimSpace(message) == "" {
		message = "daemon rpc call failed"
	}
	if err := rpcContextError("", "", message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func rpcContextError(code, class, message string) error {
	code = strings.ToLower(strings.TrimSpace(code))
	class = strings.ToLower(strings.TrimSpace(class))
	message = strings.ToLower(message)
	if code == "timeout" || class == "timeout" || strings.Contains(message, context.DeadlineExceeded.Error()) {
		return context.DeadlineExceeded
	}
	if code == "canceled" || code == "cancelled" || class == "canceled" || class == "cancelled" || strings.Contains(message, context.Canceled.Error()) {
		return context.Canceled
	}
	return nil
}

func RuntimeSocketReady(ctx context.Context, runtime Runtime) bool {
	if strings.TrimSpace(runtime.SocketPath) == "" {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(checkCtx, "unix", runtime.SocketPath)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForRuntime(ctx context.Context, stateDir string, pid int) (Runtime, error) {
	return waitForRuntimeForMode(ctx, stateDir, "headed", pid)
}

func waitForRuntimeForMode(ctx context.Context, stateDir, browserMode string, pid int) (Runtime, error) {
	deadline := time.Now().Add(60 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	for time.Now().Before(deadline) {
		runtime, ok, err := LoadRuntimeForMode(ctx, stateDir, browserMode)
		if err != nil {
			return Runtime{}, err
		}
		if ok && runtime.PID == pid && RuntimeRunning(runtime) && RuntimeSocketReady(ctx, runtime) {
			return runtime, nil
		}
		select {
		case <-ctx.Done():
			return Runtime{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return Runtime{}, fmt.Errorf("daemon keepalive process did not become ready")
}

func holdConnection(ctx context.Context, stateDir, socketPath string, client *cdp.Client, pid int, connectionMode string, reconnect time.Duration, opts holdOptions) error {
	browserMode := runtimeModeName(os.Getenv("CDP_DAEMON_BROWSER_MODE"))
	listener, err := listenRuntimeSocket(socketPath)
	if err != nil {
		_ = client.Close(websocket.StatusInternalError, "rpc listen failed")
		appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "error", Event: "rpc_listen_failed", Message: err.Error(), PID: pid})
		return err
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	leases, err := NewLeaseManager(context.Background(), stateDir, browserMode)
	if err != nil {
		_ = client.Close(websocket.StatusInternalError, "lease state read failed")
		appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "error", Event: "lease_state_read_failed", Message: err.Error(), PID: pid})
		return err
	}
	if result, reconcileErr := leases.ReconcileExpired(context.Background(), client); reconcileErr != nil {
		appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "warn", Event: "lease_reconcile_failed", Message: reconcileErr.Error(), PID: pid})
	} else if result.ExpiredLeaseCount > 0 || result.ClosedTargetCount > 0 || len(result.PendingTargetIDs) > 0 {
		appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "info", Event: "lease_reconciled", Message: fmt.Sprintf("expired_leases=%d closed_targets=%d pending_targets=%d", result.ExpiredLeaseCount, result.ClosedTargetCount, len(result.PendingTargetIDs)), PID: pid})
	}
	appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "info", Event: "rpc_listening", Message: "daemon rpc socket ready", PID: pid})

	runtime := Runtime{
		PID:                 pid,
		StartedAt:           time.Now().UTC().Format(time.RFC3339),
		BrowserMode:         browserMode,
		ConnectionMode:      connectionMode,
		ReconnectInterval:   durationString(reconnect),
		SocketPath:          socketPath,
		LogPath:             RuntimeLogPathForMode(stateDir, browserMode),
		Endpoint:            client.Endpoint(),
		UserDataDir:         os.Getenv("CDP_DAEMON_USER_DATA_DIR"),
		ManagedBrowser:      managedBrowserFromEnv(),
		ManagedProfilePath:  os.Getenv("CDP_DAEMON_MANAGED_PROFILE_PATH"),
		ProfileSeedStrategy: os.Getenv("CDP_DAEMON_PROFILE_SEED_STRATEGY"),
		ChromePID:           intEnv("CDP_DAEMON_CHROME_PID"),
		ChromePort:          os.Getenv("CDP_DAEMON_CHROME_PORT"),
	}
	if err := SaveRuntimeForMode(ctx, stateDir, browserMode, runtime); err != nil {
		_ = client.Close(websocket.StatusInternalError, "state write failed")
		appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "error", Event: "runtime_write_failed", Message: err.Error(), PID: pid})
		return err
	}
	appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "info", Event: "runtime_saved", Message: "daemon runtime state saved", PID: pid})

	cycleCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	marker := newWindowMarkerController(stateDir, browserMode, client)
	if err := marker.rehydrate(cycleCtx); err != nil {
		appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "warn", Event: "window_marker_rehydrate_failed", Message: err.Error(), PID: pid})
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = marker.close(closeCtx)
		closeCancel()
	}()
	go leases.Run(cycleCtx, client, func(result LeaseReconcileResult, reconcileErr error) {
		if reconcileErr != nil {
			appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "warn", Event: "lease_reconcile_failed", Message: reconcileErr.Error(), PID: pid})
			return
		}
		if result.ExpiredLeaseCount > 0 || result.ClosedTargetCount > 0 || len(result.PendingTargetIDs) > 0 {
			appendLogForMode(context.Background(), stateDir, browserMode, LogEntry{Level: "info", Event: "lease_reconciled", Message: fmt.Sprintf("expired_leases=%d closed_targets=%d pending_targets=%d", result.ExpiredLeaseCount, result.ClosedTargetCount, len(result.PendingTargetIDs)), PID: pid})
		}
	})
	go serveRPC(cycleCtx, listener, client, opts, leases, marker)
	return keepAlive(cycleCtx, client, reconnect)
}

func managedBrowserFromEnv() *browser.ManagedStatus {
	raw := strings.TrimSpace(os.Getenv("CDP_DAEMON_MANAGED_BROWSER"))
	if raw == "" {
		return nil
	}
	var status browser.ManagedStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return nil
	}
	return &status
}

func intEnv(key string) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return 0
	}
	return value
}

func listenRuntimeSocket(socketPath string) (net.Listener, error) {
	if strings.TrimSpace(socketPath) == "" {
		return nil, fmt.Errorf("daemon rpc socket path is required")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, fmt.Errorf("create daemon socket directory: %w", err)
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen daemon rpc socket: %w", err)
	}
	_ = os.Chmod(socketPath, 0o600)
	return listener, nil
}

func serveRPC(ctx context.Context, listener net.Listener, client *cdp.Client, opts holdOptions, leases *LeaseManager, marker *windowMarkerController) {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go handleRPC(ctx, conn, client, opts, leases, marker)
	}
}

func handleRPC(ctx context.Context, conn net.Conn, client *cdp.Client, opts holdOptions, leases *LeaseManager, marker *windowMarkerController) {
	defer conn.Close()
	var req RPCRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(rpcErrorResponse("rpc_request_invalid", "usage", fmt.Sprintf("decode daemon rpc request: %v", err)))
		return
	}
	req.Method = strings.TrimSpace(req.Method)
	if req.Method == "" {
		_ = json.NewEncoder(conn).Encode(rpcErrorResponse("rpc_method_required", "usage", "daemon rpc method is required"))
		return
	}
	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	go cancelWhenRPCClientDisconnects(conn, cancelRequest)
	callCtx := requestCtx
	cancel := func() {}
	if req.TimeoutMillis > 0 {
		callCtx, cancel = context.WithTimeout(requestCtx, time.Duration(req.TimeoutMillis)*time.Millisecond)
	}
	defer cancel()
	ownerID := strings.TrimSpace(req.OwnerID)

	switch req.Method {
	case RPCMethodBeginInvocationLease:
		var params struct {
			TTLMillis int64 `json:"ttl_ms"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				_ = json.NewEncoder(conn).Encode(rpcErrorResponse("lease_params_invalid", "usage", err.Error()))
				return
			}
		}
		info, err := leases.Begin(callCtx, time.Duration(params.TTLMillis)*time.Millisecond)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("lease_begin_failed", "lifecycle", err))
			return
		}
		writeRPCResult(conn, info)
		return
	case RPCMethodRenewInvocationLease:
		if ownerID == "" {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponse("lease_id_required", "usage", "invocation lease owner id is required"))
			return
		}
		var params struct {
			TTLMillis int64 `json:"ttl_ms"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				_ = json.NewEncoder(conn).Encode(rpcErrorResponse("lease_params_invalid", "usage", err.Error()))
				return
			}
		}
		info, err := leases.Renew(callCtx, ownerID, time.Duration(params.TTLMillis)*time.Millisecond)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("lease_renew_failed", "lifecycle", err))
			return
		}
		writeRPCResult(conn, info)
		return
	case RPCMethodEndInvocationLease:
		if ownerID == "" {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponse("lease_id_required", "usage", "invocation lease owner id is required"))
			return
		}
		result, err := leases.End(context.Background(), client, ownerID)
		if err != nil && result.LeaseID == "" {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("lease_end_failed", "lifecycle", err))
			return
		}
		if err != nil {
			result.LastError = err.Error()
		}
		writeRPCResult(conn, result)
		return
	case RPCMethodMarkTargetDisposable, RPCMethodMarkTargetPersistent:
		if ownerID == "" {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponse("lease_id_required", "usage", "invocation lease owner id is required"))
			return
		}
		var params struct {
			TargetID string `json:"target_id"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || strings.TrimSpace(params.TargetID) == "" {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponse("target_id_required", "usage", "target id is required"))
			return
		}
		disposable := req.Method == RPCMethodMarkTargetDisposable
		if err := leases.SetTargetDisposable(callCtx, ownerID, params.TargetID, disposable); err != nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("lease_target_policy_failed", "lifecycle", err))
			return
		}
		writeRPCResult(conn, map[string]any{"target_id": params.TargetID, "disposable": disposable})
		return
	case RPCMethodEnableWindowMarker:
		if marker == nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponse("window_marker_unavailable", "lifecycle", "window marker controller is unavailable"))
			return
		}
		var params struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponse("window_marker_params_invalid", "usage", err.Error()))
			return
		}
		status, err := marker.Enable(callCtx, params.Name)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("window_marker_enable_failed", "lifecycle", err))
			return
		}
		writeRPCResult(conn, status)
		return
	case RPCMethodDisableWindowMarker:
		if marker == nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponse("window_marker_unavailable", "lifecycle", "window marker controller is unavailable"))
			return
		}
		status, err := marker.Disable(callCtx)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("window_marker_disable_failed", "lifecycle", err))
			return
		}
		writeRPCResult(conn, status)
		return
	case RPCMethodWindowMarkerStatus:
		if marker == nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponse("window_marker_unavailable", "lifecycle", "window marker controller is unavailable"))
			return
		}
		writeRPCResult(conn, marker.Status())
		return
	case RPCMethodDrainEvents:
		writeRPCResult(conn, client.DrainEvents())
		return
	case RPCMethodReadEvent:
		event, err := client.ReadEvent(callCtx)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("rpc_read_event_failed", "connection", err))
			return
		}
		writeRPCResult(conn, event)
		return
	case RPCMethodFetchProtocol:
		protocolURL, err := protocolURLFromEndpoint(client.Endpoint())
		if err != nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("protocol_endpoint_invalid", "connection", err))
			return
		}
		protocol, err := cdp.FetchProtocol(callCtx, protocolURL)
		if err != nil {
			var httpErr cdp.ProtocolHTTPError
			if !errors.As(err, &httpErr) {
				_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("protocol_fetch_failed", "connection", err))
				return
			}
			protocol, err = opts.fetchProtocolFallback(callCtx)
			if err != nil {
				_ = json.NewEncoder(conn).Encode(rpcErrorResponse("protocol_fetch_failed", "connection", fmt.Sprintf("fetch protocol metadata: live endpoint returned %d; fallback failed: %v", httpErr.StatusCode, err)))
				return
			}
			protocol.Source = "daemon-fallback"
			writeRPCResult(conn, protocol)
			return
		}
		protocol.Source = "daemon"
		writeRPCResult(conn, protocol)
		return
	}

	var result json.RawMessage
	params := any(req.Params)
	if len(req.Params) == 0 {
		params = map[string]any{}
	}
	if ownerID != "" {
		if err := leases.Touch(context.Background(), ownerID); err != nil {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("lease_touch_failed", "lifecycle", err))
			return
		}
	}
	err := client.CallSession(callCtx, req.SessionID, req.Method, params, &result)
	if err != nil {
		if ownerID != "" && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			if _, cleanupErr := leases.End(context.Background(), client, ownerID); cleanupErr != nil {
				appendLogForMode(context.Background(), os.Getenv("CDP_DAEMON_STATE_DIR"), runtimeModeName(os.Getenv("CDP_DAEMON_BROWSER_MODE")), LogEntry{Level: "warn", Event: "lease_cleanup_failed", Message: cleanupErr.Error(), PID: os.Getpid()})
			}
		}
		_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("rpc_call_failed", "connection", err))
		return
	}
	if ownerID != "" && req.Method == "Target.createTarget" {
		var created struct {
			TargetID string `json:"targetId"`
		}
		if err := json.Unmarshal(result, &created); err != nil || strings.TrimSpace(created.TargetID) == "" {
			_ = json.NewEncoder(conn).Encode(rpcErrorResponse("lease_target_registration_failed", "lifecycle", "Target.createTarget returned no target id for lease registration"))
			return
		}
		if err := leases.RegisterTarget(context.Background(), ownerID, LeaseTarget{TargetID: created.TargetID, TargetType: "page", Disposable: true}); err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), defaultLeaseCleanupTimeout)
			_ = closeOwnedTarget(cleanupCtx, client, created.TargetID)
			cleanupCancel()
			_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("lease_target_registration_failed", "lifecycle", err))
			return
		}
	}
	if ownerID != "" && req.Method == "Target.closeTarget" {
		var params struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(req.Params, &params) == nil {
			_ = leases.UnregisterTarget(context.Background(), ownerID, params.TargetID)
		}
	}
	_ = json.NewEncoder(conn).Encode(RPCResponse{OK: true, Result: result})
}

func cancelWhenRPCClientDisconnects(conn net.Conn, cancel context.CancelFunc) {
	var probe [1]byte
	if _, err := conn.Read(probe[:]); err != nil {
		cancel()
	}
}

func writeRPCResult(conn net.Conn, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		_ = json.NewEncoder(conn).Encode(rpcErrorResponseForError("rpc_result_marshal_failed", "internal", err))
		return
	}
	_ = json.NewEncoder(conn).Encode(RPCResponse{OK: true, Result: raw})
}

func rpcErrorResponseForError(code, class string, err error) RPCResponse {
	if err == nil {
		return rpcErrorResponse(code, class, "daemon rpc call failed")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return rpcErrorResponse("timeout", "timeout", context.DeadlineExceeded.Error())
	}
	if errors.Is(err, context.Canceled) {
		return rpcErrorResponse("canceled", "canceled", context.Canceled.Error())
	}
	return rpcErrorResponse(code, class, err.Error())
}

func rpcErrorResponse(code, class, message string) RPCResponse {
	if strings.TrimSpace(message) == "" {
		message = "daemon rpc call failed"
	}
	return RPCResponse{
		OK:    false,
		Error: message,
		ErrorEnvelope: &RPCError{
			Code:    code,
			Class:   class,
			Message: message,
		},
	}
}

func keepAlive(ctx context.Context, client *cdp.Client, reconnect time.Duration) error {
	defer client.Close(websocket.StatusNormalClosure, "done")
	tick := 30 * time.Second
	if reconnect > 0 && reconnect < tick {
		tick = reconnect
	}
	if tick <= 0 {
		tick = 30 * time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	heartbeatFailures := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-client.Done():
			if err := client.Err(); err != nil {
				return fmt.Errorf("browser CDP transport ended: %w", err)
			}
			return fmt.Errorf("browser CDP transport ended")
		case <-ticker.C:
			var result json.RawMessage
			heartbeatCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := client.Call(heartbeatCtx, "Browser.getVersion", map[string]any{}, &result)
			cancel()
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				if transportErr := client.Err(); transportErr != nil {
					return fmt.Errorf("browser heartbeat failed: %w", transportErr)
				}
				heartbeatFailures++
				if heartbeatFailures >= 3 {
					return fmt.Errorf("browser heartbeat failed %d consecutive times: %w", heartbeatFailures, err)
				}
				continue
			}
			heartbeatFailures = 0
		}
	}
}

func appendLog(ctx context.Context, stateDir string, entry LogEntry) {
	appendLogForMode(ctx, stateDir, "headed", entry)
}

func AppendLogForMode(ctx context.Context, stateDir, browserMode string, entry LogEntry) {
	appendLogForMode(ctx, stateDir, browserMode, entry)
}

func appendLogForMode(ctx context.Context, stateDir, browserMode string, entry LogEntry) {
	if strings.TrimSpace(stateDir) == "" {
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	if entry.Time == "" {
		entry.Time = time.Now().UTC().Format(time.RFC3339)
	}
	if entry.Level == "" {
		entry.Level = "info"
	}
	logPath := RuntimeLogPathForMode(stateDir, browserMode)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = file.Write(append(b, '\n'))
}

func ReadLogs(ctx context.Context, stateDir string, tail int) ([]LogEntry, error) {
	return ReadLogsForMode(ctx, stateDir, "headed", tail)
}

func ReadLogsForMode(ctx context.Context, stateDir, browserMode string, tail int) ([]LogEntry, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	b, err := os.ReadFile(RuntimeLogPathForMode(stateDir, browserMode))
	if err != nil {
		if os.IsNotExist(err) {
			return []LogEntry{}, nil
		}
		return nil, fmt.Errorf("read daemon log: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		lines = nil
	}
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	entries := make([]LogEntry, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			entries = append(entries, LogEntry{Level: "warn", Event: "unparseable_log_line", Message: line})
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func timeoutMillis(ctx context.Context) int64 {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 1
	}
	return remaining.Milliseconds()
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return json.RawMessage(`{}`), nil
	}
	switch typed := params.(type) {
	case json.RawMessage:
		if len(typed) == 0 {
			return json.RawMessage(`{}`), nil
		}
		if !json.Valid(typed) {
			return nil, fmt.Errorf("daemon rpc params must be valid JSON")
		}
		return typed, nil
	case []byte:
		if len(typed) == 0 {
			return json.RawMessage(`{}`), nil
		}
		if !json.Valid(typed) {
			return nil, fmt.Errorf("daemon rpc params must be valid JSON")
		}
		return json.RawMessage(typed), nil
	default:
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal daemon rpc params: %w", err)
		}
		return b, nil
	}
}

func protocolURLFromEndpoint(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse daemon browser endpoint: %w", err)
	}
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	default:
		return "", fmt.Errorf("daemon browser endpoint has unsupported scheme %q", parsed.Scheme)
	}
	parsed.Path = "/json/protocol"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}
