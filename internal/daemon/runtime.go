package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"nhooyr.io/websocket"
)

const RuntimeFileName = "daemon.json"

type Runtime struct {
	PID               int    `json:"pid"`
	StartedAt         string `json:"started_at"`
	ConnectionMode    string `json:"connection_mode"`
	ReconnectInterval string `json:"reconnect_interval,omitempty"`
}

func RuntimePath(stateDir string) string {
	return filepath.Join(stateDir, RuntimeFileName)
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
	var runtime Runtime
	if err := json.Unmarshal(b, &runtime); err != nil {
		return Runtime{}, false, fmt.Errorf("parse daemon runtime state: %w", err)
	}
	return runtime, true, nil
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
	b, err := json.MarshalIndent(runtime, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal daemon runtime state: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(RuntimePath(stateDir), b, 0o600); err != nil {
		return fmt.Errorf("write daemon runtime state: %w", err)
	}
	return nil
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

func StartKeepAlive(ctx context.Context, executable, stateDir, endpoint, connectionMode string, reconnect time.Duration) (Runtime, bool, error) {
	if runtime, ok, err := LoadRuntime(ctx, stateDir); err != nil {
		return Runtime{}, false, err
	} else if ok && RuntimeRunning(runtime) {
		return runtime, true, nil
	}

	cmd := exec.Command(executable, "daemon", "hold")
	cmd.Env = append(os.Environ(),
		"CDP_DAEMON_HOLD_ENDPOINT="+endpoint,
		"CDP_DAEMON_STATE_DIR="+stateDir,
		"CDP_DAEMON_CONNECTION_MODE="+connectionMode,
		"CDP_DAEMON_RECONNECT="+reconnect.String(),
	)
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return Runtime{}, false, fmt.Errorf("open null device: %w", err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull

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
		return runtime, false, ClearRuntime(ctx, stateDir, runtime.PID)
	}
	process, err := os.FindProcess(runtime.PID)
	if err != nil {
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
			return runtime, true, ClearRuntime(ctx, stateDir, runtime.PID)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := process.Kill(); err != nil {
		return runtime, true, fmt.Errorf("kill daemon process: %w", err)
	}
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

	for {
		client, err := cdp.Dial(ctx, endpoint)
		if err == nil {
			runtime := Runtime{
				PID:               pid,
				StartedAt:         time.Now().UTC().Format(time.RFC3339),
				ConnectionMode:    connectionMode,
				ReconnectInterval: durationString(reconnect),
			}
			if err := SaveRuntime(ctx, stateDir, runtime); err != nil {
				_ = client.Close(websocket.StatusInternalError, "state write failed")
				return err
			}
			err = keepAlive(ctx, client, reconnect)
		}
		if reconnect <= 0 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(reconnect):
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
		if ok && runtime.PID == pid && RuntimeRunning(runtime) {
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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			var result json.RawMessage
			if err := client.Call(ctx, "Browser.getVersion", map[string]any{}, &result); err != nil {
				return err
			}
		}
	}
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}
