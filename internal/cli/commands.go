package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
)

type commandInfo struct {
	Name     string        `json:"name"`
	Use      string        `json:"use"`
	Short    string        `json:"short,omitempty"`
	Aliases  []string      `json:"aliases,omitempty"`
	Examples []string      `json:"examples,omitempty"`
	Children []commandInfo `json:"children,omitempty"`
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
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run local readiness checks",
		Long:  "Run readiness checks for the CLI, selected browser connection, and daemon path.",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			daemonStatus := a.daemonStatus(probe)
			daemonCheckStatus := daemonDoctorStatus(daemonStatus.State)
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
				"message":              probe.Message,
				"connection_mode":      a.connectionMode(),
				"requires_user_allow":  a.opts.autoConnect,
				"default_profile_flow": a.opts.autoConnect,
				"details":              probe,
				"remediation_commands": probe.RemediationCommands,
			})

			data := map[string]any{
				"ok":     browserStatus != "fail" && daemonCheckStatus != "fail",
				"checks": checks,
			}
			human := fmt.Sprintf("cli: pass\ndaemon: %s\nbrowser: %s", daemonStatus.State, browserStatus)
			return a.render(ctx, human, data)
		},
	}
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
	case "connected":
		return "pass"
	case "not_running", "permission_pending", "passive":
		return "pending"
	case "chrome_unavailable", "disconnected":
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

func (a *app) newDaemonCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the long-running Chrome attach daemon",
	}
	cmd.AddCommand(planned("start", "Start the attach daemon"))
	cmd.AddCommand(a.newDaemonStatusCommand())
	cmd.AddCommand(planned("stop", "Stop the attach daemon"))
	cmd.AddCommand(planned("logs", "Show attach daemon logs"))
	return cmd
}

func (a *app) newDaemonStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show attach daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			probe, err := a.browserProbe(ctx)
			if err != nil {
				return commandError(
					"invalid_browser_url",
					"usage",
					err.Error(),
					ExitUsage,
					[]string{"cdp daemon status --browser-url <browser-url> --json"},
				)
			}
			status := a.daemonStatus(probe)
			data := map[string]any{
				"ok":     true,
				"daemon": status,
			}
			return a.render(ctx, status.Message, data)
		},
	}
}

func (a *app) newConnectionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connection",
		Short: "Manage disk-backed browser connection memory",
	}
	cmd.AddCommand(a.newConnectionAddCommand())
	cmd.AddCommand(a.newConnectionListCommand())
	cmd.AddCommand(a.newConnectionSelectCommand())
	cmd.AddCommand(a.newConnectionRemoveCommand())
	cmd.AddCommand(a.newConnectionCurrentCommand())
	cmd.AddCommand(a.newConnectionResolveCommand())
	return cmd
}

func (a *app) newConnectionAddCommand() *cobra.Command {
	var browserURL string
	var autoConnect bool
	var channel string
	var project string

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update a named browser connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			mode := "browser_url"
			if autoConnect {
				mode = "auto_connect"
			}
			if mode == "browser_url" && strings.TrimSpace(browserURL) == "" {
				return commandError(
					"missing_browser_url",
					"usage",
					"connection add requires --browser-url unless --auto-connect is set",
					ExitUsage,
					[]string{"cdp connection add local --browser-url <browser-url> --json"},
				)
			}
			projectPath, err := normalizeProjectPath(project)
			if err != nil {
				return commandError(
					"invalid_project",
					"usage",
					err.Error(),
					ExitUsage,
					[]string{"cdp connection add local --browser-url <browser-url> --project <path> --json"},
				)
			}

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			conn := state.Connection{
				Name:        args[0],
				Mode:        mode,
				BrowserURL:  browserURL,
				AutoConnect: autoConnect,
				Project:     project,
			}
			conn.Project = projectPath
			if autoConnect {
				conn.Channel = channel
			}
			file = state.UpsertConnection(file, conn)
			if file.Selected == "" {
				file.Selected = conn.Name
			}
			if err := store.Save(ctx, file); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("connection %s saved", conn.Name), map[string]any{
				"ok":         true,
				"connection": conn,
				"selected":   file.Selected,
				"state_path": store.Path(),
			})
		},
	}
	cmd.Flags().StringVar(&browserURL, "browser-url", "", "Chrome DevTools browser URL")
	cmd.Flags().StringVar(&browserURL, "browserUrl", "", "alias for --browser-url")
	cmd.Flags().BoolVar(&autoConnect, "auto-connect", false, "use Chrome's default-profile auto-connect flow")
	cmd.Flags().BoolVar(&autoConnect, "autoConnect", false, "alias for --auto-connect")
	cmd.Flags().StringVar(&channel, "channel", "stable", "Chrome channel for auto-connect")
	cmd.Flags().StringVar(&project, "project", "", "optional project selector")
	return cmd
}

func normalizeProjectPath(project string) (string, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return "", nil
	}
	abs, err := filepath.Abs(project)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	return filepath.Clean(abs), nil
}

func (a *app) newConnectionListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved browser connections",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			var lines []string
			for _, conn := range file.Connections {
				marker := " "
				if conn.Name == file.Selected {
					marker = "*"
				}
				lines = append(lines, fmt.Sprintf("%s %s %s", marker, conn.Name, conn.Mode))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":          true,
				"connections": file.Connections,
				"selected":    file.Selected,
				"state_path":  store.Path(),
			})
		},
	}
}

func (a *app) newConnectionSelectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "select <name>",
		Short: "Select the default browser connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			file, ok := state.SelectConnection(file, args[0])
			if !ok {
				return commandError(
					"unknown_connection",
					"usage",
					fmt.Sprintf("unknown connection %q", args[0]),
					ExitUsage,
					[]string{"cdp connection list --json", "cdp connection add <name> --browser-url <browser-url> --json"},
				)
			}
			if err := store.Save(ctx, file); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("connection %s selected", args[0]), map[string]any{
				"ok":         true,
				"selected":   file.Selected,
				"state_path": store.Path(),
			})
		},
	}
}

func (a *app) newConnectionRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a saved browser connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			file, ok := state.RemoveConnection(file, args[0])
			if !ok {
				return commandError(
					"unknown_connection",
					"usage",
					fmt.Sprintf("unknown connection %q", args[0]),
					ExitUsage,
					[]string{"cdp connection list --json"},
				)
			}
			if err := store.Save(ctx, file); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("connection %s removed", args[0]), map[string]any{
				"ok":          true,
				"removed":     args[0],
				"selected":    file.Selected,
				"connections": file.Connections,
				"state_path":  store.Path(),
			})
		},
	}
}

func (a *app) newConnectionCurrentCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the selected browser connection",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			conn, ok := state.CurrentConnection(file)
			if !ok {
				return commandError(
					"connection_not_configured",
					"connection",
					"no browser connection is selected",
					ExitConnection,
					[]string{"cdp connection add default --auto-connect --json", "cdp connection list --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("%s %s", conn.Name, conn.Mode), map[string]any{
				"ok":         true,
				"connection": conn,
				"state_path": store.Path(),
			})
		},
	}
}

func (a *app) newConnectionResolveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve",
		Short: "Show the effective browser connection for this command",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			conn, source, ok, err := a.resolveConnection(ctx)
			if err != nil {
				return err
			}
			if !ok {
				return commandError(
					"connection_not_configured",
					"connection",
					"no browser connection is configured",
					ExitConnection,
					[]string{"cdp connection add default --auto-connect --json", "cdp connection list --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("%s %s", source, conn.Name), map[string]any{
				"ok":         true,
				"source":     source,
				"connection": conn,
				"state_path": store.Path(),
			})
		},
	}
}

func (a *app) resolveConnection(ctx context.Context) (state.Connection, string, bool, error) {
	if a.opts.browserURL != "" || a.opts.autoConnect {
		conn := state.Connection{
			Name:        "flags",
			Mode:        a.connectionMode(),
			BrowserURL:  a.opts.browserURL,
			AutoConnect: a.opts.autoConnect,
		}
		if a.opts.autoConnect {
			conn.Channel = a.opts.channel
		}
		return conn, "flags", true, nil
	}
	store, err := a.stateStore()
	if err != nil {
		return state.Connection{}, "", false, err
	}
	file, err := store.Load(ctx)
	if err != nil {
		return state.Connection{}, "", false, err
	}
	if a.opts.connection != "" {
		conn, ok := state.ConnectionByName(file, a.opts.connection)
		if !ok {
			return state.Connection{}, "", false, commandError(
				"unknown_connection",
				"usage",
				fmt.Sprintf("unknown connection %q", a.opts.connection),
				ExitUsage,
				[]string{"cdp connection list --json", "cdp connection add <name> --browser-url <browser-url> --json"},
			)
		}
		return conn, "named", true, nil
	}
	cwd, cwdErr := filepath.Abs(".")
	if cwdErr == nil {
		if conn, ok := state.ProjectConnection(file, cwd); ok {
			return conn, "project", true, nil
		}
	}
	if file.Selected != "" {
		conn, ok := state.ConnectionByName(file, file.Selected)
		return conn, "selected", ok, nil
	}
	conn, ok := state.CurrentConnection(file)
	if ok {
		return conn, "single", true, nil
	}
	return state.Connection{}, "", false, nil
}

func (a *app) newTargetsCommand() *cobra.Command {
	var limit int
	var targetType string
	cmd := &cobra.Command{
		Use:   "targets",
		Short: "List browser targets",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			targets, err := a.listTargets(ctx)
			if err != nil {
				return err
			}
			targets = filterTargetsByType(targets, targetType)
			rows := targetRows(targets)
			rows = limitRows(rows, limit)
			var lines []string
			for _, target := range rows {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s", target["id"], target["type"], target["title"]))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{"ok": true, "targets": rows})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of targets to return; use 0 for no limit")
	cmd.Flags().StringVar(&targetType, "type", "", "only return targets of this CDP type, such as page or service_worker")
	return cmd
}

func (a *app) newPagesCommand() *cobra.Command {
	var limit int
	var urlContains string
	cmd := &cobra.Command{
		Use:   "pages",
		Short: "List open pages and tabs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			targets, err := a.listTargets(ctx)
			if err != nil {
				return err
			}
			pages := pageRows(targets)
			pages = filterRowsContains(pages, "url", urlContains)
			pages = limitRows(pages, limit)
			var lines []string
			for _, page := range pages {
				lines = append(lines, fmt.Sprintf("%s\t%s", page["id"], page["title"]))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{"ok": true, "pages": pages})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of pages to return; use 0 for no limit")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "only return pages whose URL contains this text")
	return cmd
}

func (a *app) listTargets(ctx context.Context) ([]cdp.TargetInfo, error) {
	endpoint, err := a.browserEndpoint(ctx)
	if err != nil {
		return nil, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			[]string{"cdp connection current --json", "cdp doctor --auto-connect --json"},
		)
	}
	targets, err := cdp.ListTargets(ctx, endpoint)
	if err != nil {
		return nil, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("list targets: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp daemon status --json"},
		)
	}
	return targets, nil
}

func targetRows(targets []cdp.TargetInfo) []map[string]any {
	rows := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		rows = append(rows, map[string]any{
			"id":       target.TargetID,
			"type":     target.Type,
			"title":    target.Title,
			"url":      target.URL,
			"attached": target.Attached,
		})
	}
	return rows
}

func filterTargetsByType(targets []cdp.TargetInfo, targetType string) []cdp.TargetInfo {
	targetType = strings.TrimSpace(targetType)
	if targetType == "" {
		return targets
	}
	filtered := make([]cdp.TargetInfo, 0, len(targets))
	for _, target := range targets {
		if target.Type == targetType {
			filtered = append(filtered, target)
		}
	}
	return filtered
}

func limitRows(rows []map[string]any, limit int) []map[string]any {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}

func filterRowsContains(rows []map[string]any, key, needle string) []map[string]any {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return rows
	}
	filtered := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		value, _ := row[key].(string)
		if strings.Contains(value, needle) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func pageRows(targets []cdp.TargetInfo) []map[string]any {
	pages := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		if target.Type != "page" {
			continue
		}
		pages = append(pages, map[string]any{
			"id":       target.TargetID,
			"type":     target.Type,
			"title":    target.Title,
			"url":      target.URL,
			"attached": target.Attached,
		})
	}
	return pages
}

func (a *app) newOpenCommand() *cobra.Command {
	return planned("open <url>", "Open or navigate to a URL")
}

func (a *app) newEvalCommand() *cobra.Command {
	return planned("eval <expression>", "Evaluate JavaScript in the selected page")
}

func (a *app) newSnapshotCommand() *cobra.Command {
	return planned("snapshot", "Print a compact accessibility snapshot")
}

func (a *app) newScreenshotCommand() *cobra.Command {
	return planned("screenshot", "Capture a page or element screenshot")
}

func (a *app) newConsoleCommand() *cobra.Command {
	return planned("console", "Read console messages")
}

func (a *app) newNetworkCommand() *cobra.Command {
	return planned("network", "Inspect network requests")
}

func (a *app) newCDPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "protocol",
		Aliases: []string{"cdp"},
		Short:   "Discover and execute raw CDP methods",
	}
	cmd.AddCommand(a.newProtocolMetadataCommand())
	cmd.AddCommand(a.newProtocolDomainsCommand())
	cmd.AddCommand(a.newProtocolSearchCommand())
	cmd.AddCommand(a.newProtocolDescribeCommand())
	cmd.AddCommand(a.newProtocolExecCommand())
	return cmd
}

func (a *app) newProtocolMetadataCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "metadata",
		Short: "Print the live protocol metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			domains := cdp.SummarizeDomains(protocol.Domains)
			data := map[string]any{
				"ok": true,
				"protocol": map[string]any{
					"version":      protocol.Version,
					"domain_count": len(domains),
					"domains":      domains,
				},
			}
			human := fmt.Sprintf("CDP %s.%s, %d domains", protocol.Version.Major, protocol.Version.Minor, len(domains))
			return a.render(ctx, human, data)
		},
	}
}

func (a *app) newProtocolDomainsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "domains",
		Short: "List CDP domains",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			domains := cdp.SummarizeDomains(protocol.Domains)
			var lines []string
			for _, domain := range domains {
				lines = append(lines, fmt.Sprintf("%s\tcommands=%d\tevents=%d", domain.Name, domain.CommandCount, domain.EventCount))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":           true,
				"domain_count": len(domains),
				"domains":      domains,
			})
		},
	}
}

func (a *app) newProtocolSearchCommand() *cobra.Command {
	var limit int
	var kind string
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search CDP domains, methods, events, and types",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			results := cdp.SearchProtocol(protocol, args[0], limit)
			results = cdp.FilterSearchResultsByKind(results, kind)
			var lines []string
			for _, result := range results {
				lines = append(lines, fmt.Sprintf("%s\t%s", result.Kind, result.Path))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":      true,
				"query":   args[0],
				"matches": results,
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 25, "maximum number of search results")
	cmd.Flags().StringVar(&kind, "kind", "", "only return matches of this kind: domain, command, event, or type")
	return cmd
}

func (a *app) newProtocolDescribeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <Domain.entity>",
		Short: "Describe a CDP domain, command, event, or type schema",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			desc, ok := cdp.DescribeEntity(protocol, args[0])
			if !ok {
				return commandError(
					"unknown_protocol_entity",
					"usage",
					fmt.Sprintf("unknown protocol entity %q", args[0]),
					ExitUsage,
					[]string{"cdp protocol search <query> --json", "cdp protocol domains --json"},
				)
			}
			human := fmt.Sprintf("%s\t%s", desc.Kind, desc.Path)
			return a.render(ctx, human, map[string]any{
				"ok":     true,
				"entity": desc,
			})
		},
	}
}

func (a *app) newProtocolExecCommand() *cobra.Command {
	var params string
	cmd := &cobra.Command{
		Use:   "exec <Domain.method>",
		Short: "Execute a raw CDP method",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			rawParams := json.RawMessage(params)
			if len(rawParams) == 0 {
				rawParams = json.RawMessage(`{}`)
			}
			if !json.Valid(rawParams) {
				return commandError(
					"invalid_json",
					"usage",
					"--params must be valid JSON",
					ExitUsage,
					[]string{"cdp protocol exec Browser.getVersion --params '{}' --json"},
				)
			}
			endpoint, err := a.browserEndpoint(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					[]string{"cdp connection current --json", "cdp doctor --active-browser-probe --json"},
				)
			}
			result, err := cdp.Exec(ctx, endpoint, args[0], rawParams)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("execute %s: %v", args[0], err),
					ExitConnection,
					[]string{"cdp doctor --json", "cdp protocol describe " + args[0] + " --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("%s ok", args[0]), map[string]any{
				"ok":     true,
				"method": args[0],
				"result": result,
			})
		},
	}
	cmd.Flags().StringVar(&params, "params", "{}", "JSON params object for the CDP method")
	return cmd
}

func (a *app) fetchProtocol(ctx context.Context) (cdp.Protocol, error) {
	protocolURL, err := a.browserProtocolURL(ctx)
	if err != nil {
		return cdp.Protocol{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			[]string{"cdp connection current --json", "cdp doctor --auto-connect --json"},
		)
	}
	protocol, err := cdp.FetchProtocol(ctx, protocolURL)
	if err != nil {
		return cdp.Protocol{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("fetch protocol metadata: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp daemon status --json"},
		)
	}
	return protocol, nil
}

func (a *app) newWorkflowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Run high-level browser debugging workflows",
	}
	cmd.AddCommand(planned("verify <url>", "Open a URL and collect basic verification evidence"))
	cmd.AddCommand(planned("console-errors", "Summarize console errors"))
	cmd.AddCommand(planned("network-failures", "Summarize failed network requests"))
	cmd.AddCommand(planned("perf <url>", "Capture and summarize performance evidence"))
	cmd.AddCommand(planned("a11y", "Run a focused accessibility workflow"))
	return cmd
}

func (a *app) newMCPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Inspect and generate MCP integration config",
	}
	claude := &cobra.Command{
		Use:   "claude",
		Short: "Inspect and generate Claude MCP config",
	}
	claude.AddCommand(planned("status", "Inspect Claude MCP configuration"))
	claude.AddCommand(planned("print-config", "Print suggested Claude MCP configuration"))
	claude.AddCommand(planned("install", "Install cdp-cli MCP integration"))
	claude.AddCommand(planned("restore-chrome-devtools", "Print restoration commands for official Chrome DevTools MCP"))
	cmd.AddCommand(claude)
	return cmd
}

func planned(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := cmd.CommandPath()
			return notImplemented(name)
		},
	}
}

func describeCommand(cmd *cobra.Command) commandInfo {
	info := commandInfo{
		Name:     cmd.Name(),
		Use:      cmd.UseLine(),
		Short:    cmd.Short,
		Aliases:  cmd.Aliases,
		Examples: commandExamples(cmd.CommandPath()),
	}

	for _, child := range cmd.Commands() {
		if child.Hidden {
			continue
		}
		info.Children = append(info.Children, describeCommand(child))
	}

	return info
}

func commandExamples(path string) []string {
	examples := map[string][]string{
		"cdp": {
			"cdp doctor --json",
			"cdp describe --json | jq '.commands.children | map(.name)'",
		},
		"cdp describe": {
			"cdp describe --json",
			"cdp describe --command 'daemon status' --json",
		},
		"cdp doctor": {
			"cdp doctor --json",
		},
		"cdp explain-error": {
			"cdp explain-error not_implemented --json",
		},
		"cdp exit-codes": {
			"cdp exit-codes --json",
		},
		"cdp schema": {
			"cdp schema --json",
			"cdp schema error-envelope --json",
		},
		"cdp daemon status": {
			"cdp daemon status --json",
		},
		"cdp connection remove": {
			"cdp connection remove stale --json",
		},
		"cdp connection resolve": {
			"cdp connection resolve --json",
			"cdp connection resolve --connection default --json",
		},
		"cdp targets": {
			"cdp targets --json",
			"cdp targets --limit 10 --json",
			"cdp targets --type service_worker --json",
			"cdp targets --browser-url <browser-url> --json",
		},
		"cdp pages": {
			"cdp pages --json",
			"cdp pages --limit 10 --json",
			"cdp pages --url-contains localhost --json",
			"cdp pages --browser-url <browser-url> --json",
		},
		"cdp protocol metadata": {
			"cdp protocol metadata --json",
			"cdp protocol metadata --browser-url <browser-url> --json",
		},
		"cdp protocol domains": {
			"cdp protocol domains --json",
			"cdp protocol domains --browser-url <browser-url> --json",
		},
		"cdp protocol search": {
			"cdp protocol search screenshot --json",
			"cdp protocol search console --kind event --json",
		},
		"cdp protocol describe": {
			"cdp protocol describe Page.captureScreenshot --json",
		},
		"cdp protocol exec": {
			"cdp protocol exec Browser.getVersion --params '{}' --json",
		},
		"cdp workflow verify": {
			"cdp workflow verify https://example.com --json",
		},
	}
	return examples[path]
}

func findCommand(root *cobra.Command, path string) (*cobra.Command, error) {
	parts := strings.Fields(path)
	if len(parts) > 0 && parts[0] == root.Name() {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return root, nil
	}

	found, _, err := root.Find(parts)
	if err != nil || found == nil {
		return nil, commandError(
			"unknown_command",
			"usage",
			fmt.Sprintf("unknown command path %q", path),
			ExitUsage,
			[]string{"cdp describe --json", "cdp --help"},
		)
	}
	return found, nil
}
