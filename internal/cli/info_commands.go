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
				lines := make([]string, 0, len(rows))
				for _, row := range rows {
					lines = append(lines, fmt.Sprintf("%s\t%s", row["name"], row["status"]))
				}
				return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
					"ok":           true,
					"capabilities": rows,
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
				"ok":     browserStatus != "fail" && daemonCheckStatus != "fail",
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

func capabilityCatalog() []map[string]string {
	return []map[string]string{
		{"name": "connection", "status": "implemented", "commands": "connection, daemon, doctor"},
		{"name": "target_discovery", "status": "implemented", "commands": "targets, pages"},
		{"name": "page_control", "status": "implemented", "commands": "page reload/back/forward/activate/close, open"},
		{"name": "page_inspection", "status": "implemented", "commands": "eval, text, html, snapshot, dom query, css inspect, layout overflow"},
		{"name": "artifacts", "status": "implemented", "commands": "screenshot"},
		{"name": "console", "status": "implemented", "commands": "console, workflow console-errors"},
		{"name": "network", "status": "implemented", "commands": "network, workflow network-failures"},
		{"name": "storage", "status": "implemented", "commands": "storage list/get/set/delete/clear/snapshot/diff, storage cookies"},
		{"name": "raw_protocol", "status": "implemented", "commands": "protocol metadata/domains/search/describe/exec"},
		{"name": "input_automation", "status": "implemented", "commands": "click, fill, type, press, hover, drag"},
		{"name": "emulation", "status": "planned", "commands": "viewport, media, user-agent, geolocation, network, cpu"},
		{"name": "performance", "status": "planned", "commands": "trace, Lighthouse, performance insights"},
		{"name": "memory", "status": "planned", "commands": "heap snapshot"},
		{"name": "advanced_storage", "status": "implemented", "commands": "storage indexeddb, storage cache, storage service-workers"},
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
