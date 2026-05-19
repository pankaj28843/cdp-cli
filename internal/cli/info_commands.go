package cli

import (
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/spf13/cobra"
)

type commandInfo struct {
	Name     string        `json:"name"`
	Use      string        `json:"use"`
	Short    string        `json:"short,omitempty"`
	Aliases  []string      `json:"aliases,omitempty"`
	Examples []string      `json:"examples,omitempty"`
	Flags    []flagInfo    `json:"flags,omitempty"`
	Children []commandInfo `json:"children,omitempty"`
}

type flagInfo struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage"`
}

func (a *app) newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			human := fmt.Sprintf("cdp %s", a.build.Version)
			return a.render(ctx, human, a.build)
		},
	}
}

func (a *app) newDescribeCommand() *cobra.Command {
	var commandPath string
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Describe the command tree as JSON for agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			target := a.root
			if commandPath != "" {
				var err error
				target, err = findCommand(a.root, commandPath)
				if err != nil {
					return err
				}
			}

			data := map[string]any{
				"ok":       true,
				"commands": describeCommand(target),
				"globals": []string{
					"--json",
					"--compact",
					"--jq",
					"--debug",
					"--timeout",
					"--profile",
					"--config",
					"--browser-url",
					"--browserUrl",
					"--auto-connect",
					"--autoConnect",
					"--channel",
					"--user-data-dir",
					"--state-dir",
					"--active-browser-probe",
					"--connection",
				},
			}
			return a.render(ctx, "Use --json to print the command tree.", data)
		},
	}
	cmd.Flags().StringVar(&commandPath, "command", "", "describe one command path, such as 'daemon status'")
	return cmd
}

func (a *app) newDoctorCommand() *cobra.Command {
	var checkName string
	var capabilities bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run local readiness checks",
		Long:  "Run readiness checks for the CLI, selected browser connection, and daemon path.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if capabilities {
				ctx, cancel := a.commandContext(cmd)
				defer cancel()
				rows := capabilityCatalog()
				readiness := agentReadiness(rows)
				lines := make([]string, 0, len(rows))
				for _, row := range rows {
					lines = append(lines, fmt.Sprintf("%s\t%s", capabilityString(row, "name"), capabilityString(row, "status")))
				}
				return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
					"ok":              true,
					"capabilities":    rows,
					"agent_readiness": readiness,
					"next_commands":   readiness["next_commands"],
				})
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			probe, err := a.browserProbe(ctx)
			if err != nil {
				return commandError(
					"invalid_browser_url",
					"usage",
					err.Error(),
					ExitUsage,
					[]string{"cdp doctor --browser-url <browser-url> --json"},
				)
			}
			browserStatus := browserDoctorStatus(a.opts.autoConnect, &probe)
			daemonStatus := a.daemonStatus(ctx, probe)
			daemonCheckStatus := daemonDoctorStatus(daemonStatus.State)
			browserMessage := probe.Message
			browserRemediation := probe.RemediationCommands
			if a.opts.autoConnect && daemonStatus.State == "running" {
				browserStatus = "pass"
				browserMessage = "daemon keepalive process is running; active browser probing was skipped"
				browserRemediation = daemonStatus.NextCommands
			}
			checks := []map[string]any{
				{"name": "cli", "status": "pass", "message": "command scaffold is installed"},
				{
					"name":            "daemon",
					"status":          daemonCheckStatus,
					"state":           daemonStatus.State,
					"message":         daemonStatus.Message,
					"connection_mode": daemonStatus.ConnectionMode,
					"details":         daemonStatus,
				},
			}
			checks = append(checks, map[string]any{
				"name":                 "browser_debug_endpoint",
				"status":               browserStatus,
				"message":              browserMessage,
				"connection_mode":      a.connectionMode(),
				"requires_user_allow":  a.opts.autoConnect,
				"default_profile_flow": a.opts.autoConnect,
				"details":              probe,
				"remediation_commands": browserRemediation,
			})
			if checkName == "" || checkName == "browser-health" || checkName == "browser-budget" {
				health := healthMap(daemonStatus.Health)
				if checkName == "" || checkName == "browser-health" {
					checks = append(checks, map[string]any{
						"name":            "browser-health",
						"status":          doctorStatusFromHealth(health),
						"state":           fmt.Sprint(health["state"]),
						"message":         browserHealthMessage(health),
						"connection_mode": a.connectionMode(),
						"details":         health,
						"next_commands":   safeDiagnosticCommands(),
					})
				}
				if checkName == "" || checkName == "browser-budget" {
					checks = append(checks, map[string]any{
						"name":            "browser-budget",
						"status":          doctorStatusFromBudgetHealth(health),
						"state":           fmt.Sprint(health["state"]),
						"message":         browserBudgetMessage(health),
						"connection_mode": a.connectionMode(),
						"details":         health["resource_budget"],
						"health":          health,
						"next_commands":   []string{"cdp pages --json", "cdp page cleanup --workflow-created --close --json"},
					})
				}
			}
			if checkName != "" {
				checks = filterChecksByName(checks, checkName)
				if len(checks) == 0 {
					return commandError(
						"unknown_check",
						"usage",
						fmt.Sprintf("unknown doctor check %q", checkName),
						ExitUsage,
						[]string{"cdp doctor --json", "cdp doctor --check daemon --json"},
					)
				}
			}

			data := map[string]any{
				"ok":     checksOK(checks),
				"checks": checks,
			}
			human := fmt.Sprintf("cli: pass\ndaemon: %s\nbrowser: %s", daemonStatus.State, browserStatus)
			return a.render(ctx, human, data)
		},
	}
	cmd.Flags().StringVar(&checkName, "check", "", "only return one check by name")
	cmd.Flags().BoolVar(&capabilities, "capabilities", false, "report implemented and planned capability areas without probing Chrome")
	return cmd
}

func capabilityCatalog() []map[string]any {
	return []map[string]any{
		capabilityRow("connection", "implemented", "connection, daemon, doctor", []string{"cdp connection current --json", "cdp daemon status --json"}, []string{"cdp doctor --json"}),
		capabilityRow("target_discovery", "implemented", "targets, pages", []string{"cdp targets --json", "cdp pages --json"}, []string{"cdp doctor --check browser-budget --json"}),
		capabilityRow("page_control", "implemented", "page reload/back/forward/activate/close, open", []string{"cdp page select --url-contains example --json", "cdp page reload --target <target-id> --json"}, []string{"cdp pages --json"}),
		capabilityRow("page_inspection", "implemented", "eval, text, html, snapshot, dom query, css inspect, layout overflow", []string{"cdp snapshot --json", "cdp eval 'document.title' --json"}, []string{"cdp text body --json"}),
		capabilityRow("artifacts", "implemented", "screenshot", []string{"cdp screenshot --out tmp/page.png --json"}, []string{"cdp workflow debug-bundle --out-dir tmp/debug-bundle --json"}),
		capabilityRow("console", "implemented", "console, workflow console-errors", []string{"cdp console --errors --wait 1s --json"}, []string{"cdp workflow console-errors --wait 1s --json"}),
		capabilityRow("network", "implemented", "network, workflow network-failures", []string{"cdp network --wait 1s --json"}, []string{"cdp workflow network-failures --wait 1s --json"}),
		capabilityRow("storage", "implemented", "storage list/get/set/delete/clear/snapshot/diff, storage cookies", []string{"cdp storage list --json", "cdp storage snapshot --json"}, []string{"cdp storage cookies list --json"}),
		capabilityRow("raw_protocol", "implemented", "protocol metadata/domains/search/describe/exec", []string{"cdp protocol metadata --json", "cdp protocol search screenshot --json"}, []string{"cdp protocol exec Browser.getVersion --json"}),
		capabilityRow("input_automation", "implemented", "click, fill, type, press, hover, drag", []string{"cdp form values --json"}, []string{"cdp click <selector> --json"}),
		capabilityRow("accessibility", "implemented", "a11y tree/find/node, workflow a11y", []string{"cdp a11y tree --json", "cdp a11y find --role button --json"}, []string{"cdp workflow a11y https://example.com --json"}),
		capabilityRow("performance", "implemented", "perf summary, workflow perf, workflow page-load metrics", []string{"cdp perf summary --duration 1s --json"}, []string{"cdp workflow perf https://example.com --wait 1s --trace tmp/perf.local.json --json"}),
		capabilityRow("memory", "implemented", "memory counters, heap snapshot artifact", []string{"cdp memory counters --json"}, []string{"cdp memory heap-snapshot --out tmp/heap.heapsnapshot --json"}),
		capabilityRow("advanced_storage", "implemented", "storage indexeddb, storage cache, storage service-workers", []string{"cdp storage indexeddb list --json", "cdp storage cache list --json", "cdp storage service-workers list --json"}, []string{"cdp storage snapshot --json"}),
		capabilityRow("emulation", "implemented", "viewport, media, user-agent, geolocation, responsive audit", []string{"cdp emulate viewport --help", "cdp emulate user-agent --help", "cdp emulate geolocation --help"}, []string{"cdp workflow responsive-audit https://example.com --json"}),
		capabilityRow("network_cpu_throttling", "planned", "network and CPU throttling presets", []string{"cdp protocol describe Emulation.setCPUThrottlingRate --json", "cdp protocol describe Network.emulateNetworkConditions --json"}, []string{"cdp workflow responsive-audit https://example.com --include network --json"}),
	}
}

func capabilityRow(name, status, commands string, verifyCommands, evidenceCommands []string) map[string]any {
	return map[string]any{
		"name":              name,
		"status":            status,
		"commands":          commands,
		"verify_commands":   verifyCommands,
		"evidence_commands": evidenceCommands,
	}
}

func capabilityString(row map[string]any, key string) string {
	value, _ := row[key].(string)
	return value
}

func agentReadiness(capabilities []map[string]any) map[string]any {
	implemented := 0
	planned := 0
	for _, capability := range capabilities {
		switch capabilityString(capability, "status") {
		case "implemented":
			implemented++
		case "planned":
			planned++
		}
	}
	return map[string]any{
		"status":       "ready",
		"mode":         "daemon_first_cli",
		"implemented":  implemented,
		"planned":      planned,
		"safe_default": "passive diagnostics avoid active Chrome approval prompts unless --active-browser-probe is supplied",
		"next_commands": []string{
			"cdp doctor --json",
			"cdp daemon status --json",
			"cdp pages --json",
			"cdp page cleanup --workflow-created --close --json",
		},
		"browser_commands": []string{
			"cdp pages --json",
			"cdp open https://example.com --json",
			"cdp snapshot --json",
			"cdp workflow debug-bundle --out-dir tmp/debug-bundle --json",
		},
	}
}

func filterChecksByName(checks []map[string]any, name string) []map[string]any {
	filtered := make([]map[string]any, 0, len(checks))
	for _, check := range checks {
		if check["name"] == name {
			filtered = append(filtered, check)
		}
	}
	return filtered
}

func browserDoctorStatus(autoConnect bool, probe *browser.ProbeResult) string {
	switch probe.State {
	case "cdp_available":
		return "pass"
	case "not_configured", "permission_pending", "active_probe_skipped":
		return "pending"
	case "listening_not_cdp", "missing_browser_websocket", "invalid_response":
		if autoConnect && probe.State == "listening_not_cdp" {
			probe.Message = "auto-connect endpoint is listening, but a CDP session is not established yet"
			return "pending"
		}
		return "warn"
	case "stale_state":
		return "warn"
	default:
		return "fail"
	}
}

func daemonDoctorStatus(state string) string {
	switch state {
	case "connected", "running":
		return "pass"
	case "not_running", "permission_pending", "passive":
		return "pending"
	case "chrome_unavailable", "disconnected", "stale_state":
		return "warn"
	default:
		return "pending"
	}
}

func healthMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{"state": "unknown", "reasons": []string{"health_unavailable"}}
}

func doctorStatusFromHealth(health map[string]any) string {
	switch fmt.Sprint(health["state"]) {
	case "healthy":
		return "pass"
	case "permission_pending", "passive_no_process", "not_running", "unknown":
		return "pending"
	default:
		return "warn"
	}
}

func doctorStatusFromBudgetHealth(health map[string]any) string {
	if health["resource_budget"] == nil {
		return "pending"
	}
	if health["tabs_over_budget"] == true || health["windows_over_budget"] == true {
		return "warn"
	}
	return "pass"
}

func browserHealthMessage(health map[string]any) string {
	state := fmt.Sprint(health["state"])
	reasons := toStringSlice(health["reasons"])
	if len(reasons) == 0 {
		return "browser health is " + state
	}
	return fmt.Sprintf("browser health is %s: %s", state, strings.Join(reasons, ", "))
}

func browserBudgetMessage(health map[string]any) string {
	if health["resource_budget"] == nil {
		return "browser budget is unavailable until a daemon runtime is running"
	}
	return fmt.Sprintf("browser budget: %v/%v tabs, %v/%v windows", health["tab_count"], health["max_tabs"], health["window_count"], health["max_windows"])
}

func checksOK(checks []map[string]any) bool {
	for _, check := range checks {
		if check["status"] == "fail" {
			return false
		}
	}
	return true
}

func (a *app) newExplainErrorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "explain-error [code]",
		Short: "Explain stable cdp error codes and recovery commands",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			if len(args) == 1 {
				info, ok := findErrorInfo(args[0])
				if !ok {
					return commandError(
						"unknown_error_code",
						"usage",
						fmt.Sprintf("unknown error code %q", args[0]),
						ExitUsage,
						[]string{"cdp explain-error --json", "cdp explain-error not_implemented --json"},
					)
				}
				human := fmt.Sprintf("%s: %s\n%s", info.Code, info.Message, info.Meaning)
				return a.render(ctx, human, map[string]any{"ok": true, "error": info})
			}

			catalog := errorCatalog()
			var lines []string
			for _, info := range catalog {
				lines = append(lines, fmt.Sprintf("%s (%d): %s", info.Code, info.ExitCode, info.Message))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{"ok": true, "errors": catalog})
		},
	}
}

func (a *app) newExitCodesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "exit-codes",
		Short: "Print stable process exit codes",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			catalog := errorCatalog()
			var lines []string
			lines = append(lines, fmt.Sprintf("%d: ok", ExitOK))
			for _, info := range catalog {
				lines = append(lines, fmt.Sprintf("%d: %s", info.ExitCode, info.Code))
			}

			data := map[string]any{
				"ok": true,
				"exit_codes": append([]map[string]any{{
					"code":    ExitOK,
					"name":    "ok",
					"meaning": "the command completed successfully",
				}}, exitCodeRows(catalog)...),
			}
			return a.render(ctx, strings.Join(lines, "\n"), data)
		},
	}
}

func exitCodeRows(catalog []errorInfo) []map[string]any {
	rows := make([]map[string]any, 0, len(catalog))
	for _, info := range catalog {
		rows = append(rows, map[string]any{
			"code":      info.ExitCode,
			"name":      info.Code,
			"err_class": info.Class,
			"meaning":   info.Meaning,
		})
	}
	return rows
}

func (a *app) newSchemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "schema [name]",
		Short: "Print stable JSON output schemas",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			catalog := schemaCatalog()
			if len(args) == 1 {
				schema, ok := catalog[args[0]]
				if !ok {
					return commandError(
						"unknown_schema",
						"usage",
						fmt.Sprintf("unknown schema %q", args[0]),
						ExitUsage,
						[]string{"cdp schema --json", "cdp describe --json"},
					)
				}
				return a.render(ctx, fmt.Sprintf("%s: %s", schema.Name, schema.Description), map[string]any{"ok": true, "schema": schema})
			}

			names := schemaNames()
			return a.render(ctx, strings.Join(names, "\n"), map[string]any{"ok": true, "schemas": names})
		},
	}
}
