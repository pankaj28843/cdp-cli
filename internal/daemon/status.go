package daemon

import "github.com/pankaj28843/cdp-cli/internal/browser"

func statusNextCommands(browserMode string, autoConnect bool) []string {
	if autoConnect {
		return safeAutoConnectStatusCommands(browserMode)
	}
	if runtimeModeName(browserMode) == "headless" {
		return []string{
			"cdp --browser-mode headless browser profile status --json",
			"cdp --browser-mode headless daemon keepalive --repair --json",
		}
	}
	return []string{"cdp daemon start --help", "cdp doctor --json"}
}

func safeAutoConnectStatusCommands(browserMode string) []string {
	prefix := commandPrefix(browserMode)
	return []string{
		prefix + " daemon status --json",
		prefix + " doctor --check daemon --json",
		prefix + " doctor --check browser-health --json",
		prefix + " daemon logs --tail 50 --json",
	}
}

func commandPrefix(browserMode string) string {
	if runtimeModeName(browserMode) == "headless" {
		return "cdp --browser-mode headless"
	}
	return "cdp"
}

type Status struct {
	State               string              `json:"state"`
	Message             string              `json:"message"`
	BrowserMode         string              `json:"browser_mode"`
	ConnectionMode      string              `json:"connection_mode"`
	RequiresUserAllow   bool                `json:"requires_user_allow"`
	DefaultProfileFlow  bool                `json:"default_profile_flow"`
	ProcessRunning      bool                `json:"process_running"`
	RuntimeSocketReady  bool                `json:"runtime_socket_ready"`
	Runtime             *Runtime            `json:"runtime,omitempty"`
	BrowserProbe        browser.ProbeResult `json:"browser_probe"`
	NextCommands        []string            `json:"next_commands"`
	HumanRepairCommands []string            `json:"human_repair_commands,omitempty"`
	Health              any                 `json:"health,omitempty"`
}

func Snapshot(connectionMode string, autoConnect bool, probe browser.ProbeResult) Status {
	return SnapshotForMode("headed", connectionMode, autoConnect, probe)
}

func SnapshotForMode(browserMode, connectionMode string, autoConnect bool, probe browser.ProbeResult) Status {
	browserMode = runtimeModeName(browserMode)
	status := Status{
		State:              "not_running",
		Message:            "cdp daemon is not running",
		BrowserMode:        browserMode,
		ConnectionMode:     connectionMode,
		RequiresUserAllow:  autoConnect,
		DefaultProfileFlow: autoConnect,
		BrowserProbe:       probe,
		NextCommands:       statusNextCommands(browserMode, autoConnect),
	}

	switch probe.State {
	case "cdp_available":
		status.State = "connected"
		status.Message = "browser endpoint is available; daemon process is not running yet"
		if autoConnect {
			status.NextCommands = safeAutoConnectStatusCommands(browserMode)
			status.HumanRepairCommands = []string{commandPrefix(browserMode) + " daemon start --auto-connect --json"}
		} else if browserMode == "headless" {
			status.NextCommands = []string{commandPrefix(browserMode) + " daemon keepalive --repair --json", commandPrefix(browserMode) + " pages --json"}
		} else {
			status.NextCommands = []string{"cdp daemon start --help", "cdp pages --help"}
		}
	case "listening_not_cdp":
		if autoConnect {
			status.State = "permission_pending"
			status.Message = "auto-connect endpoint is listening, but Chrome has not exposed a CDP session to this CLI"
			status.NextCommands = safeAutoConnectStatusCommands(browserMode)
			status.HumanRepairCommands = []string{commandPrefix(browserMode) + " daemon keepalive --auto-connect --repair --json"}
		}
	case "permission_pending":
		status.State = "permission_pending"
		status.Message = probe.Message
		status.NextCommands = safeAutoConnectStatusCommands(browserMode)
	case "active_probe_skipped":
		status.State = "passive"
		status.Message = probe.Message
		if autoConnect {
			status.NextCommands = safeAutoConnectStatusCommands(browserMode)
			status.HumanRepairCommands = []string{commandPrefix(browserMode) + " daemon keepalive --auto-connect --repair --json"}
		} else if browserMode == "headless" {
			status.NextCommands = []string{commandPrefix(browserMode) + " daemon keepalive --repair --json", commandPrefix(browserMode) + " daemon status --active-browser-probe --json"}
		} else {
			status.NextCommands = []string{"cdp daemon start --help", "cdp daemon status --active-browser-probe --json"}
		}
	case "unreachable":
		status.State = "chrome_unavailable"
		status.Message = "browser endpoint is not reachable"
	case "invalid_response", "missing_browser_websocket":
		status.State = "disconnected"
		status.Message = probe.Message
	}

	return status
}

func WithRuntime(status Status, runtime Runtime, running bool) Status {
	return WithRuntimeReadiness(status, runtime, running, running)
}

func WithRuntimeReadiness(status Status, runtime Runtime, processRunning, socketReady bool) Status {
	if status.BrowserMode == "" {
		status.BrowserMode = runtimeModeName(runtime.BrowserMode)
	}
	prefix := commandPrefix(status.BrowserMode)
	status.Runtime = &runtime
	status.ProcessRunning = processRunning
	status.RuntimeSocketReady = socketReady
	if processRunning && socketReady {
		status.State = "running"
		status.Message = "daemon keepalive process is running"
		if status.DefaultProfileFlow {
			status.NextCommands = []string{prefix + " pages --json", prefix + " doctor --check browser-health --json", prefix + " daemon logs --tail 50 --json"}
			status.HumanRepairCommands = []string{prefix + " daemon stop --json"}
		} else {
			status.NextCommands = []string{prefix + " pages --json", prefix + " daemon stop --json"}
		}
	} else if processRunning && !socketReady {
		status.State = "runtime_socket_unready"
		status.Message = "daemon process is running but the runtime socket is not ready"
		if status.DefaultProfileFlow {
			status.NextCommands = []string{prefix + " daemon status --json", prefix + " doctor --check browser-health --json", prefix + " daemon logs --tail 50 --json"}
			status.HumanRepairCommands = []string{prefix + " daemon stop --json", prefix + " daemon keepalive --auto-connect --repair --json"}
		} else if status.BrowserMode == "headless" {
			status.NextCommands = []string{prefix + " daemon keepalive --repair --json", prefix + " daemon logs --tail 50 --json", prefix + " daemon stop --json"}
		} else {
			status.NextCommands = []string{prefix + " daemon stop --json", prefix + " daemon keepalive --repair --json", prefix + " daemon logs --tail 50 --json"}
		}
	} else if runtime.PID > 0 {
		status.State = "stale_state"
		status.Message = "daemon runtime state exists but the process is not running"
		if status.DefaultProfileFlow {
			status.NextCommands = []string{prefix + " daemon keepalive --auto-connect --repair --json", prefix + " daemon status --json", prefix + " daemon logs --tail 50 --json"}
			status.HumanRepairCommands = []string{prefix + " daemon stop --json", prefix + " daemon start --auto-connect --json"}
		} else {
			status.NextCommands = []string{prefix + " daemon stop --json", prefix + " daemon start --json"}
		}
	}
	return status
}
