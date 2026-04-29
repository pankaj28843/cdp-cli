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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
)

const RuntimeFileName = "daemon.json"
const RuntimeSocketFileName = "daemon.sock"
const RuntimeLogFileName = "daemon.log"

const (
	RPCMethodDrainEvents   = "Daemon.drainEvents"
	RPCMethodReadEvent     = "Daemon.readEvent"
	RPCMethodFetchProtocol = "Daemon.fetchProtocol"
)

var fetchProtocolFallback = cdp.FetchOfficialProtocol

type Runtime struct {
	PID               int    `json:"pid"`
	StartedAt         string `json:"started_at"`
	ConnectionMode    string `json:"connection_mode"`
	ReconnectInterval string `json:"reconnect_interval,omitempty"`
	SocketPath        string `json:"socket_path,omitempty"`
	LogPath           string `json:"log_path,omitempty"`
	Endpoint          string `json:"-"`
	UserDataDir       string `json:"user_data_dir,omitempty"`
}

type runtimeFile struct {
	PID               int    `json:"pid"`
	StartedAt         string `json:"started_at"`
	ConnectionMode    string `json:"connection_mode"`
	ReconnectInterval string `json:"reconnect_interval,omitempty"`
	SocketPath        string `json:"socket_path,omitempty"`
	LogPath           string `json:"log_path,omitempty"`
	Endpoint          string `json:"endpoint,omitempty"`
	UserDataDir       string `json:"user_data_dir,omitempty"`
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
	Params        json.RawMessage `json:"params,omitempty"`
	TimeoutMillis int64           `json:"timeout_ms,omitempty"`
}

type RPCResponse struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type RuntimeClient struct {
	Runtime Runtime
}

func RuntimePath(stateDir string) string {
	return filepath.Join(stateDir, RuntimeFileName)
}

func RuntimeSocketPath(stateDir string) string {
	return filepath.Join(stateDir, RuntimeSocketFileName)
}

func RuntimeLogPath(stateDir string) string {
	return filepath.Join(stateDir, RuntimeLogFileName)
}

func LoadRuntime(ctx context.Context, stateDir string) (Runtime, bool, error) {
	select {
	case <-ctx.Done():
		return Runtime{}, false, ctx.Err()
	default:
	}

	b, err := os.ReadFile(RuntimePath(stateDir))
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
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create daemon state directory: %w", err)
	}
	b, err := json.MarshalIndent(runtimeFileFromRuntime(runtime), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal daemon runtime state: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(RuntimePath(stateDir), b, 0o600); err != nil {
		return fmt.Errorf("write daemon runtime state: %w", err)
	}
	return nil
}

func runtimeFromFile(file runtimeFile) Runtime {
	return Runtime{
		PID:               file.PID,
		StartedAt:         file.StartedAt,
		ConnectionMode:    file.ConnectionMode,
		ReconnectInterval: file.ReconnectInterval,
		SocketPath:        file.SocketPath,
		LogPath:           file.LogPath,
		Endpoint:          file.Endpoint,
		UserDataDir:       file.UserDataDir,
	}
}

func runtimeFileFromRuntime(runtime Runtime) runtimeFile {
	return runtimeFile{
		PID:               runtime.PID,
		StartedAt:         runtime.StartedAt,
		ConnectionMode:    runtime.ConnectionMode,
		ReconnectInterval: runtime.ReconnectInterval,
		SocketPath:        runtime.SocketPath,
		LogPath:           runtime.LogPath,
		Endpoint:          runtime.Endpoint,
		UserDataDir:       runtime.UserDataDir,
	}
}

func ClearRuntime(ctx context.Context, stateDir string, pid int) error {
	runtime, ok, err := LoadRuntime(ctx, stateDir)
	if err != nil || !ok {
		return err
	}
	if pid > 0 && runtime.PID != pid {
		return nil
	}
	if err := os.Remove(RuntimePath(stateDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove daemon runtime state: %w", err)
	}
	return nil
}

func RuntimeRunning(runtime Runtime) bool {
	if runtime.PID <= 0 {
		return false
	}
	process, err := os.FindProcess(runtime.PID)
	if err != nil {
		return false
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

func StartKeepAlive(ctx context.Context, executable, stateDir, endpoint, connectionMode, userDataDir string, reconnect time.Duration) (Runtime, bool, error) {
	if runtime, ok, err := LoadRuntime(ctx, stateDir); err != nil {
		return Runtime{}, false, err
	} else if ok && RuntimeRunning(runtime) {
		if RuntimeSocketReady(ctx, runtime) {
			return runtime, true, nil
		}
		_, _, _ = StopRuntime(ctx, stateDir)
	}

	cmd := exec.Command(executable, "daemon", "hold")
	socketPath := RuntimeSocketPath(stateDir)
	cmd.Env = append(os.Environ(),
		"CDP_DAEMON_HOLD_ENDPOINT="+endpoint,
		"CDP_DAEMON_STATE_DIR="+stateDir,
		"CDP_DAEMON_CONNECTION_MODE="+connectionMode,
		"CDP_DAEMON_RECONNECT="+reconnect.String(),
		"CDP_DAEMON_SOCKET="+socketPath,
		"CDP_DAEMON_USER_DATA_DIR="+userDataDir,
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

	runtime, err := waitForRuntime(ctx, stateDir, pid)
	if err != nil {
		if process, findErr := os.FindProcess(pid); findErr == nil {
			_ = process.Kill()
		}
		_ = ClearRuntime(context.Background(), stateDir, pid)
		return Runtime{}, false, err
	}
	return runtime, false, nil
}

func StopRuntime(ctx context.Context, stateDir string) (Runtime, bool, error) {
	runtime, ok, err := LoadRuntime(ctx, stateDir)
	if err != nil || !ok {
		return Runtime{}, false, err
	}
	if !RuntimeRunning(runtime) {
		_ = os.Remove(runtime.SocketPath)
		return runtime, false, ClearRuntime(ctx, stateDir, runtime.PID)
	}
	process, err := os.FindProcess(runtime.PID)
	if err != nil {
		_ = os.Remove(runtime.SocketPath)
		return runtime, false, ClearRuntime(ctx, stateDir, runtime.PID)
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
			return runtime, true, ClearRuntime(ctx, stateDir, runtime.PID)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := process.Kill(); err != nil {
		return runtime, true, fmt.Errorf("kill daemon process: %w", err)
	}
	_ = os.Remove(runtime.SocketPath)
	return runtime, true, ClearRuntime(ctx, stateDir, runtime.PID)
}

func Hold(ctx context.Context, stateDir, endpoint, connectionMode string, reconnect time.Duration) error {
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("daemon hold endpoint is required")
	}
	if strings.TrimSpace(stateDir) == "" {
		return fmt.Errorf("daemon hold state directory is required")
	}
	pid := os.Getpid()
	defer ClearRuntime(context.Background(), stateDir, pid)
	appendLog(context.Background(), stateDir, LogEntry{Level: "info", Event: "hold_start", Message: "daemon hold process starting", PID: pid})

	socketPath := os.Getenv("CDP_DAEMON_SOCKET")
	if strings.TrimSpace(socketPath) == "" {
		socketPath = RuntimeSocketPath(stateDir)
	}

	for {
		client, err := cdp.Dial(ctx, endpoint)
		if err == nil {
			appendLog(context.Background(), stateDir, LogEntry{Level: "info", Event: "browser_connected", Message: "connected to browser endpoint", PID: pid})
			err = holdConnection(ctx, stateDir, socketPath, client, pid, connectionMode, reconnect)
			if err != nil {
				appendLog(context.Background(), stateDir, LogEntry{Level: "warn", Event: "hold_connection_ended", Message: err.Error(), PID: pid})
			}
			_ = ClearRuntime(context.Background(), stateDir, pid)
		} else {
			appendLog(context.Background(), stateDir, LogEntry{Level: "warn", Event: "browser_dial_failed", Message: err.Error(), PID: pid})
		}
		if reconnect <= 0 {
			return err
		}
		select {
		case <-ctx.Done():
			appendLog(context.Background(), stateDir, LogEntry{Level: "info", Event: "hold_stop", Message: ctx.Err().Error(), PID: pid})
			return ctx.Err()
		case <-time.After(reconnect):
			appendLog(context.Background(), stateDir, LogEntry{Level: "info", Event: "reconnect_wait_elapsed", Message: "attempting browser reconnect", PID: pid})
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
	raw, err := CallRuntime(ctx, c.Runtime, sessionID, method, params)
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

func CallRuntime(ctx context.Context, runtime Runtime, sessionID, method string, params any) (json.RawMessage, error) {
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
		Params:        rawParams,
		TimeoutMillis: timeoutMillis(ctx),
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
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
		if resp.Error == "" {
			resp.Error = "daemon rpc call failed"
		}
		switch resp.Error {
		case context.DeadlineExceeded.Error():
			return nil, context.DeadlineExceeded
		case context.Canceled.Error():
			return nil, context.Canceled
		}
		if strings.Contains(resp.Error, context.DeadlineExceeded.Error()) {
			return nil, context.DeadlineExceeded
		}
		if strings.Contains(resp.Error, context.Canceled.Error()) {
			return nil, context.Canceled
		}
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp.Result, nil
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
	deadline := time.Now().Add(60 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	for time.Now().Before(deadline) {
		runtime, ok, err := LoadRuntime(ctx, stateDir)
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

func holdConnection(ctx context.Context, stateDir, socketPath string, client *cdp.Client, pid int, connectionMode string, reconnect time.Duration) error {
	listener, err := listenRuntimeSocket(socketPath)
	if err != nil {
		_ = client.Close(websocket.StatusInternalError, "rpc listen failed")
		appendLog(context.Background(), stateDir, LogEntry{Level: "error", Event: "rpc_listen_failed", Message: err.Error(), PID: pid})
		return err
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	appendLog(context.Background(), stateDir, LogEntry{Level: "info", Event: "rpc_listening", Message: "daemon rpc socket ready", PID: pid})

	runtime := Runtime{
		PID:               pid,
		StartedAt:         time.Now().UTC().Format(time.RFC3339),
		ConnectionMode:    connectionMode,
		ReconnectInterval: durationString(reconnect),
		SocketPath:        socketPath,
		LogPath:           RuntimeLogPath(stateDir),
		Endpoint:          client.Endpoint(),
		UserDataDir:       os.Getenv("CDP_DAEMON_USER_DATA_DIR"),
	}
	if err := SaveRuntime(ctx, stateDir, runtime); err != nil {
		_ = client.Close(websocket.StatusInternalError, "state write failed")
		appendLog(context.Background(), stateDir, LogEntry{Level: "error", Event: "runtime_write_failed", Message: err.Error(), PID: pid})
		return err
	}
	appendLog(context.Background(), stateDir, LogEntry{Level: "info", Event: "runtime_saved", Message: "daemon runtime state saved", PID: pid})

	cycleCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	go serveRPC(cycleCtx, listener, client, &mu)
	return keepAlive(cycleCtx, client, reconnect, &mu)
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

func serveRPC(ctx context.Context, listener net.Listener, client *cdp.Client, mu *sync.Mutex) {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go handleRPC(ctx, conn, client, mu)
	}
}

func handleRPC(ctx context.Context, conn net.Conn, client *cdp.Client, mu *sync.Mutex) {
	defer conn.Close()
	var req RPCRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}
	req.Method = strings.TrimSpace(req.Method)
	if req.Method == "" {
		_ = json.NewEncoder(conn).Encode(RPCResponse{OK: false, Error: "daemon rpc method is required"})
		return
	}
	callCtx := ctx
	cancel := func() {}
	if req.TimeoutMillis > 0 {
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMillis)*time.Millisecond)
	}
	defer cancel()

	switch req.Method {
	case RPCMethodDrainEvents:
		writeRPCResult(conn, client.DrainEvents())
		return
	case RPCMethodReadEvent:
		event, err := client.ReadEvent(callCtx)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(RPCResponse{OK: false, Error: err.Error()})
			return
		}
		writeRPCResult(conn, event)
		return
	case RPCMethodFetchProtocol:
		protocolURL, err := protocolURLFromEndpoint(client.Endpoint())
		if err != nil {
			_ = json.NewEncoder(conn).Encode(RPCResponse{OK: false, Error: err.Error()})
			return
		}
		protocol, err := cdp.FetchProtocol(callCtx, protocolURL)
		if err != nil {
			var httpErr cdp.ProtocolHTTPError
			if !errors.As(err, &httpErr) {
				_ = json.NewEncoder(conn).Encode(RPCResponse{OK: false, Error: err.Error()})
				return
			}
			protocol, err = fetchProtocolFallback(callCtx)
			if err != nil {
				_ = json.NewEncoder(conn).Encode(RPCResponse{OK: false, Error: fmt.Sprintf("fetch protocol metadata: live endpoint returned %d; fallback failed: %v", httpErr.StatusCode, err)})
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
	mu.Lock()
	err := client.CallSession(callCtx, req.SessionID, req.Method, params, &result)
	mu.Unlock()
	if err != nil {
		_ = json.NewEncoder(conn).Encode(RPCResponse{OK: false, Error: err.Error()})
		return
	}
	_ = json.NewEncoder(conn).Encode(RPCResponse{OK: true, Result: result})
}

func writeRPCResult(conn net.Conn, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		_ = json.NewEncoder(conn).Encode(RPCResponse{OK: false, Error: err.Error()})
		return
	}
	_ = json.NewEncoder(conn).Encode(RPCResponse{OK: true, Result: raw})
}

func keepAlive(ctx context.Context, client *cdp.Client, reconnect time.Duration, mu *sync.Mutex) error {
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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			var result json.RawMessage
			mu.Lock()
			err := client.Call(ctx, "Browser.getVersion", map[string]any{}, &result)
			mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

func appendLog(ctx context.Context, stateDir string, entry LogEntry) {
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
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(RuntimeLogPath(stateDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
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
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	b, err := os.ReadFile(RuntimeLogPath(stateDir))
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
