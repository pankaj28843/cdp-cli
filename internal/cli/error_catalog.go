package cli

type errorInfo struct {
	Code                string   `json:"code"`
	Class               string   `json:"err_class"`
	ExitCode            int      `json:"exit_code"`
	Message             string   `json:"message"`
	Meaning             string   `json:"meaning"`
	RemediationCommands []string `json:"remediation_commands"`
}

func errorCatalog() []errorInfo {
	return []errorInfo{
		{
			Code:     "check_failed",
			Class:    "check_failed",
			ExitCode: ExitCheckFailed,
			Message:  "a readiness or validation check failed",
			Meaning:  "The command completed its checks and found at least one blocking problem.",
			RemediationCommands: []string{
				"cdp doctor --json",
				"cdp describe --json",
			},
		},
		{
			Code:     "usage",
			Class:    "usage",
			ExitCode: ExitUsage,
			Message:  "the command or flags are invalid",
			Meaning:  "The CLI could not run because the invocation is malformed or incomplete.",
			RemediationCommands: []string{
				"cdp --help",
				"cdp describe --json",
			},
		},
		{
			Code:     "connection_failed",
			Class:    "connection",
			ExitCode: ExitConnection,
			Message:  "Chrome or the cdp daemon could not be reached",
			Meaning:  "The command needs a browser or daemon connection and none is currently available.",
			RemediationCommands: []string{
				"cdp daemon status --json",
				"cdp doctor --json",
			},
		},
		{
			Code:     "permission_pending",
			Class:    "permission",
			ExitCode: ExitPermission,
			Message:  "Chrome permission is required before continuing",
			Meaning:  "The browser needs explicit user approval before this CLI can inspect the active session.",
			RemediationCommands: []string{
				"cdp daemon status --json",
				"cdp daemon start --help",
			},
		},
		{
			Code:     "timeout",
			Class:    "timeout",
			ExitCode: ExitTimeout,
			Message:  "the command exceeded its timeout",
			Meaning:  "The requested operation did not complete before the command deadline.",
			RemediationCommands: []string{
				"cdp doctor --json",
				"cdp --help",
			},
		},
		{
			Code:     "not_implemented",
			Class:    "not_implemented",
			ExitCode: ExitNotImplemented,
			Message:  "the command is planned but not implemented yet",
			Meaning:  "The command surface is reserved and documented, but execution has not shipped.",
			RemediationCommands: []string{
				"cdp describe --json",
				"cdp --help",
			},
		},
		{
			Code:     "internal",
			Class:    "internal",
			ExitCode: ExitInternal,
			Message:  "an unexpected internal error occurred",
			Meaning:  "The CLI hit an unclassified failure and should be improved to return a more specific code.",
			RemediationCommands: []string{
				"cdp doctor --json",
				"cdp version --json",
			},
		},
	}
}

func findErrorInfo(code string) (errorInfo, bool) {
	for _, info := range errorCatalog() {
		if info.Code == code || info.Class == code {
			return info, true
		}
	}
	return errorInfo{}, false
}
