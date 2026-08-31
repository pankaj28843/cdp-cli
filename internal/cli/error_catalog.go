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
			Code:     "cdp_command_failed",
			Class:    "protocol",
			ExitCode: ExitCheckFailed,
			Message:  "Chrome rejected a DevTools Protocol command",
			Meaning:  "The browser connection worked, but Chrome rejected the requested CDP method or parameters.",
			RemediationCommands: []string{
				"cdp protocol describe <method> --json",
				"cdp protocol examples <method> --json",
			},
		},
		{
			Code:     "permission_pending",
			Class:    "permission",
			ExitCode: ExitPermission,
			Message:  "Chrome permission is required before continuing",
			Meaning:  "The browser needs explicit user approval before this CLI can inspect the active session. Agents should stop and report the required human action instead of retrying daemon lifecycle commands.",
			RemediationCommands: []string{
				"open chrome://inspect/#remote-debugging",
				"cdp daemon status --json",
				"cdp doctor --check daemon --json",
			},
		},
		{
			Code:     "browser_resource_budget_exceeded",
			Class:    "resource_budget",
			ExitCode: ExitConnection,
			Message:  "the selected browser profile is over the cdp tab/window budget",
			Meaning:  "The command would create another page while the approved browser profile is already at or over the safe agent resource budget.",
			RemediationCommands: []string{
				"cdp pages --json",
				"cdp page cleanup --workflow-created --close --json",
				"cdp doctor --check browser-budget --json",
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
			Code:     "target_not_found",
			Class:    "usage",
			ExitCode: ExitUsage,
			Message:  "the requested browser target was not found",
			Meaning:  "A page-scoped command could not find the requested tab. List pages and pass a full target id or a unique prefix.",
			RemediationCommands: []string{
				"cdp pages --json",
				"cdp open <url> --json",
			},
		},
		{
			Code:     "invalid_target_index",
			Class:    "usage",
			ExitCode: ExitUsage,
			Message:  "the page target index is invalid",
			Meaning:  "Page target indexes are 1-based and an explicitly supplied index must be greater than zero.",
			RemediationCommands: []string{
				"cdp pages --json",
				"cdp snapshot --target-index 1 --json",
			},
		},
		{
			Code:     "ambiguous_target",
			Class:    "usage",
			ExitCode: ExitUsage,
			Message:  "the browser target selector matched multiple pages",
			Meaning:  "The provided target prefix is not specific enough for a page-scoped command.",
			RemediationCommands: []string{
				"cdp pages --json",
				"cdp snapshot --target <target-id> --json",
			},
		},
		{
			Code:     "javascript_exception",
			Class:    "runtime",
			ExitCode: ExitCheckFailed,
			Message:  "page JavaScript evaluation failed",
			Meaning:  "The page-scoped command reached Chrome, but the JavaScript expression threw in the selected page.",
			RemediationCommands: []string{
				"cdp eval 'document.title' --json",
				"cdp pages --json",
			},
		},
		{
			Code:     "invalid_selector",
			Class:    "usage",
			ExitCode: ExitUsage,
			Message:  "the CSS selector is invalid",
			Meaning:  "The snapshot command could not run because the supplied selector is not valid CSS.",
			RemediationCommands: []string{
				"cdp snapshot --selector body --json",
				"cdp snapshot --selector article --json",
			},
		},
		{
			Code:     "no_visible_posts",
			Class:    "check_failed",
			ExitCode: ExitCheckFailed,
			Message:  "no visible post elements were found",
			Meaning:  "The visible-posts workflow opened the page, but no visible elements matched the post selector before the wait deadline.",
			RemediationCommands: []string{
				"cdp snapshot --selector article --json",
				"cdp workflow visible-posts <url> --selector article --wait 30s --json",
			},
		},
		{
			Code:     "stop_state",
			Class:    "stop_state",
			ExitCode: ExitCheckFailed,
			Message:  "a conservative browser stop state was reached",
			Meaning:  "A browser page reached a login, bot-check, access-denied, payment, personal-data, permission, or configured app-specific boundary. Agents should stop automated follow-up instead of trying to bypass it.",
			RemediationCommands: []string{
				"cdp stop-state classify --target <target-id> --json",
				"cdp pages --json",
			},
		},
		{
			Code:     "invalid_stop_state_rule",
			Class:    "usage",
			ExitCode: ExitUsage,
			Message:  "a stop-state rule is invalid",
			Meaning:  "A configured stop-state rule was not in state=pattern form or supplied an invalid regular expression.",
			RemediationCommands: []string{
				"cdp stop-state classify --rule-text-contains app_block='Something went wrong' --json",
				"cdp stop-state classify --rule-text-regex app_block='blocked|unavailable' --json",
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
			Code:     "lighthouse_failed",
			Class:    "check_failed",
			ExitCode: ExitCheckFailed,
			Message:  "the Lighthouse CLI failed against daemon-owned Chrome",
			Meaning:  "The external Lighthouse process failed; its captured output is bounded and cancellation uses the platform-safe process termination policy.",
			RemediationCommands: []string{
				"cdp --browser-mode headless daemon status --json",
				"cdp workflow a11y <url> --json",
			},
		},
		{
			Code:     "artifact_missing",
			Class:    "internal",
			ExitCode: ExitInternal,
			Message:  "a required report artifact was missing or invalid",
			Meaning:  "A workflow could not prove that its expected file-backed artifact exists as a regular non-empty file.",
			RemediationCommands: []string{
				"cdp workflow lighthouse --help",
				"cdp pages --json",
			},
		},
		{
			Code:     "pdf_burst_failed",
			Class:    "extraction",
			ExitCode: ExitCheckFailed,
			Message:  "the Poppler PDF image burst failed",
			Meaning:  "The local pdftoppm process failed or was canceled; its diagnostics are bounded and its owned-process termination policy is reported.",
			RemediationCommands: []string{
				"verify the PDF opens normally",
				"cdp workflow google-translate --help",
			},
		},
		{
			Code:     "pdf_burst_invalid_page",
			Class:    "extraction",
			ExitCode: ExitCheckFailed,
			Message:  "the Poppler PDF image burst produced an invalid page artifact",
			Meaning:  "Scanned-PDF translation requires every discovered page artifact to be a regular, non-empty file before browser translation starts.",
			RemediationCommands: []string{
				"verify the PDF opens normally",
				"cdp workflow google-translate --help",
			},
		},
		{
			Code:     "pdf_burst_empty",
			Class:    "extraction",
			ExitCode: ExitCheckFailed,
			Message:  "the Poppler PDF image burst produced no page artifacts",
			Meaning:  "The local pdftoppm process completed without producing a usable PNG page set for scanned-PDF translation.",
			RemediationCommands: []string{
				"verify the PDF opens normally",
				"cdp workflow google-translate --help",
			},
		},
		{
			Code:     "pdf_text_extraction_canceled",
			Class:    "extraction",
			ExitCode: ExitTimeout,
			Message:  "the Poppler PDF text-layer extraction was canceled",
			Meaning:  "The local pdftotext process exceeded the command deadline or was canceled; bounded diagnostics and the platform process-termination policy are reported without extracted text.",
			RemediationCommands: []string{
				"retry with a larger --timeout",
				"cdp workflow pdf-to-markdown --help",
			},
		},
		{
			Code:     "pdf_text_extraction_failed",
			Class:    "extraction",
			ExitCode: ExitCheckFailed,
			Message:  "the Poppler PDF text-layer extraction failed",
			Meaning:  "The local pdftotext process failed; its diagnostics are bounded and its owned-process termination policy is reported without embedding extracted text.",
			RemediationCommands: []string{
				"verify the PDF opens normally",
				"cdp workflow pdf-to-markdown --help",
			},
		},
		{
			Code:     "pdf_text_output_too_large",
			Class:    "extraction",
			ExitCode: ExitCheckFailed,
			Message:  "the Poppler PDF text layer exceeded the extraction limit",
			Meaning:  "The local pdftotext stdout stream exceeded the bounded extraction buffer; reduce or split the PDF before extracting.",
			RemediationCommands: []string{
				"use a smaller PDF",
				"split the PDF before extraction",
				"cdp workflow pdf-to-markdown --help",
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
		if info.Code == code {
			return info, true
		}
	}
	for _, info := range errorCatalog() {
		if info.Class == code {
			return info, true
		}
	}
	return errorInfo{}, false
}
