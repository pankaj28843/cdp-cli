package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
	"path/filepath"
)

func (a *app) newConnectionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connection",
		Short: "Manage disk-backed browser connection memory",
	}
	cmd.AddCommand(a.newConnectionAddCommand())
	cmd.AddCommand(a.newConnectionListCommand())
	cmd.AddCommand(a.newConnectionSelectCommand())
	cmd.AddCommand(a.newConnectionRemoveCommand())
	cmd.AddCommand(a.newConnectionPruneCommand())
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
				UserDataDir: a.opts.userDataDir,
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
	var project string
	cmd := &cobra.Command{
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
			projectPath, err := normalizeProjectPath(project)
			if err != nil {
				return commandError(
					"invalid_project",
					"usage",
					err.Error(),
					ExitUsage,
					[]string{"cdp connection list --project <path> --json"},
				)
			}
			connections := filterConnectionsByProject(file.Connections, projectPath)
			var lines []string
			for _, conn := range connections {
				marker := " "
				if conn.Name == file.Selected {
					marker = "*"
				}
				lines = append(lines, fmt.Sprintf("%s %s %s", marker, conn.Name, conn.Mode))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":          true,
				"connections": connections,
				"selected":    file.Selected,
				"state_path":  store.Path(),
			})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "only list connections scoped to this project path")
	return cmd
}

func filterConnectionsByProject(connections []state.Connection, project string) []state.Connection {
	if project == "" {
		return connections
	}
	filtered := make([]state.Connection, 0, len(connections))
	for _, conn := range connections {
		if conn.Project == project {
			filtered = append(filtered, conn)
		}
	}
	return filtered
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

func (a *app) newConnectionPruneCommand() *cobra.Command {
	var missingProjects bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune stale saved browser connections",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			if !missingProjects {
				return commandError(
					"missing_prune_criteria",
					"usage",
					"connection prune requires --missing-projects",
					ExitUsage,
					[]string{"cdp connection prune --missing-projects --json"},
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
			prunedFile, removed := state.PruneMissingProjects(file, pathExists)
			if !dryRun {
				if err := store.Save(ctx, prunedFile); err != nil {
					return err
				}
			}
			return a.render(ctx, fmt.Sprintf("pruned %d connections", len(removed)), map[string]any{
				"ok":          true,
				"dry_run":     dryRun,
				"removed":     removed,
				"connections": prunedFile.Connections,
				"selected":    prunedFile.Selected,
				"state_path":  store.Path(),
			})
		},
	}
	cmd.Flags().BoolVar(&missingProjects, "missing-projects", false, "remove connections whose project path no longer exists")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report stale connections without writing state")
	return cmd
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
			UserDataDir: a.opts.userDataDir,
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
