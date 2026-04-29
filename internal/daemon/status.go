package daemon

import "github.com/pankaj28843/cdp-cli/internal/browser"

type Status struct {
	State              string              `json:"state"`
	Message            string              `json:"message"`
	ConnectionMode     string              `json:"connection_mode"`
	RequiresUserAllow  bool                `json:"requires_user_allow"`
	DefaultProfileFlow bool                `json:"default_profile_flow"`
	BrowserProbe       browser.ProbeResult `json:"browser_probe"`
	NextCommands       []string            `json:"next_commands"`
}

func Snapshot(connectionMode string, autoConnect bool, probe browser.ProbeResult) Status {
	status := Status{
		State:              "not_running",
		Message:            "cdp daemon is not running",
		ConnectionMode:     connectionMode,
		RequiresUserAllow:  autoConnect,
		DefaultProfileFlow: autoConnect,
		BrowserProbe:       probe,
		NextCommands: []string{
			"cdp daemon start --help",
			"cdp doctor --json",
		},
	}

	switch probe.State {
	case "cdp_available":
		status.State = "connected"
		status.Message = "browser endpoint is available; daemon process is not running yet"
		status.NextCommands = []string{
			"cdp daemon start --help",
			"cdp pages --help",
		}
	case "listening_not_cdp":
		if autoConnect {
			status.State = "permission_pending"
			status.Message = "auto-connect endpoint is listening, but Chrome has not exposed a CDP session to this CLI"
			status.NextCommands = []string{
				"cdp daemon start --auto-connect --help",
				"cdp doctor --auto-connect --json",
			}
		}
	case "permission_pending":
		status.State = "permission_pending"
		status.Message = probe.Message
		status.NextCommands = []string{
			"cdp daemon start --auto-connect --help",
			"cdp doctor --auto-connect --json",
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
