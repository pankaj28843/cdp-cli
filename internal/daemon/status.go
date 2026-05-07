package daemon

import "github.com/pankaj28843/cdp-cli/internal/browser"

func statusNextCommands(autoConnect bool) []string {
	if autoConnect {
		return safeAutoConnectStatusCommands()
	}
	return []string{"cdp daemon start --help", "cdp doctor --json"}
}

func safeAutoConnectStatusCommands() []string {
	return []string{
		"cdp daemon status --json",
		"cdp doctor --check daemon --json",
		"cdp doctor --check browser-health --json",
		"cdp daemon logs --tail 50 --json",
	}
}

type Status struct {
	State               string              `json:"state"`
	Message             string              `json:"message"`
	ConnectionMode      string              `json:"connection_mode"`
	RequiresUserAllow   bool                `json:"requires_user_allow"`
	DefaultProfileFlow  bool                `json:"default_profile_flow"`
	ProcessRunning      bool                `json:"process_running"`
	Runtime             *Runtime            `json:"runtime,omitempty"`
	BrowserProbe        browser.ProbeResult `json:"browser_probe"`
	NextCommands        []string            `json:"next_commands"`
	HumanRepairCommands []string            `json:"human_repair_commands,omitempty"`
	Health              any                 `json:"health,omitempty"`
}

func Snapshot(connectionMode string, autoConnect bool, probe browser.ProbeResult) Status {
	status := Status{
		State:              "not_running",
		Message:            "cdp daemon is not running",
		ConnectionMode:     connectionMode,
		RequiresUserAllow:  autoConnect,
		DefaultProfileFlow: autoConnect,
		BrowserProbe:       probe,
		NextCommands:       statusNextCommands(autoConnect),
	}

	switch probe.State {
	case "cdp_available":
		status.State = "connected"
		status.Message = "browser endpoint is available; daemon process is not running yet"
		if autoConnect {
			status.NextCommands = safeAutoConnectStatusCommands()
			status.HumanRepairCommands = []string{"cdp daemon start --auto-connect --json"}
		} else {
			status.NextCommands = []string{"cdp daemon start --help", "cdp pages --help"}
		}
	case "listening_not_cdp":
		if autoConnect {
			status.State = "permission_pending"
			status.Message = "auto-connect endpoint is listening, but Chrome has not exposed a CDP session to this CLI"
			status.NextCommands = safeAutoConnectStatusCommands()
			status.HumanRepairCommands = []string{"cdp daemon keepalive --auto-connect --repair --json"}
		}
	case "permission_pending":
		status.State = "permission_pending"
		status.Message = probe.Message
		status.NextCommands = safeAutoConnectStatusCommands()
	case "active_probe_skipped":
		status.State = "passive"
		status.Message = probe.Message
		if autoConnect {
			status.NextCommands = safeAutoConnectStatusCommands()
			status.HumanRepairCommands = []string{"cdp daemon keepalive --auto-connect --repair --json"}
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
	status.Runtime = &runtime
	status.ProcessRunning = running
	if running {
		status.State = "running"
		status.Message = "daemon keepalive process is running"
		if status.DefaultProfileFlow {
			status.NextCommands = []string{"cdp pages --json", "cdp doctor --check browser-health --json", "cdp daemon logs --tail 50 --json"}
			status.HumanRepairCommands = []string{"cdp daemon stop --json"}
		} else {
			status.NextCommands = []string{"cdp pages --json", "cdp daemon stop --json"}
		}
	} else if runtime.PID > 0 {
		status.State = "stale_state"
		status.Message = "daemon runtime state exists but the process is not running"
		if status.DefaultProfileFlow {
			status.NextCommands = []string{"cdp daemon keepalive --auto-connect --repair --json", "cdp daemon status --json", "cdp daemon logs --tail 50 --json"}
			status.HumanRepairCommands = []string{"cdp daemon stop --json", "cdp daemon start --auto-connect --json"}
		} else {
			status.NextCommands = []string{"cdp daemon stop --json", "cdp daemon start --json"}
		}
	}
	return status
}
