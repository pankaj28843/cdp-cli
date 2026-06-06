package cli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func (a *app) newPermissionsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "permissions", Short: "Grant or reset browser permissions"}
	cmd.AddCommand(a.newPermissionsGrantCommand())
	cmd.AddCommand(a.newPermissionsResetCommand())
	return cmd
}

func (a *app) newPermissionsGrantCommand() *cobra.Command {
	var origin string
	cmd := &cobra.Command{
		Use:   "grant <permission>...",
		Short: "Grant browser permissions for an origin",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			normalizedOrigin, err := normalizePermissionOrigin(origin)
			if err != nil {
				return err
			}
			permissions, err := normalizePermissionNames(args)
			if err != nil {
				return err
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			client, err := a.daemonRuntimeClient(ctx)
			if err != nil {
				return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
			}

			rows := make([]map[string]any, 0, len(permissions))
			for _, permission := range permissions {
				params := map[string]any{
					"permission": map[string]any{"name": permission},
					"setting":    "granted",
					"origin":     normalizedOrigin,
				}
				if err := client.Call(ctx, "Browser.setPermission", params, nil); err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("grant permission %s for %s: %v", permission, normalizedOrigin, err), ExitConnection, []string{"cdp protocol describe Browser.setPermission --json", "cdp permissions reset --json"})
				}
				rows = append(rows, map[string]any{
					"name":    permission,
					"setting": "granted",
					"origin":  normalizedOrigin,
					"method":  "Browser.setPermission",
				})
			}

			report := map[string]any{
				"ok": true,
				"permissions": map[string]any{
					"action":         "grant",
					"origin":         normalizedOrigin,
					"setting":        "granted",
					"permissions":    rows,
					"browser_scoped": true,
					"reset_command":  "cdp permissions reset --json",
					"warnings":       permissionScopeWarnings(),
				},
				"next_commands": []string{"cdp permissions reset --json", "cdp protocol describe Browser.setPermission --json"},
			}
			return a.render(ctx, fmt.Sprintf("granted permissions\t%s", strings.Join(permissions, ",")), report)
		},
	}
	cmd.Flags().StringVar(&origin, "origin", "", "HTTP(S) origin to grant permissions for, for example https://example.com")
	return cmd
}

func (a *app) newPermissionsResetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset browser permission overrides",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			client, err := a.daemonRuntimeClient(ctx)
			if err != nil {
				return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
			}
			if err := client.Call(ctx, "Browser.resetPermissions", map[string]any{}, nil); err != nil {
				return commandError("connection_failed", "connection", fmt.Sprintf("reset permissions: %v", err), ExitConnection, []string{"cdp protocol describe Browser.resetPermissions --json", "cdp doctor --json"})
			}
			report := map[string]any{
				"ok": true,
				"permissions": map[string]any{
					"action":            "reset",
					"method":            "Browser.resetPermissions",
					"browser_scoped":    true,
					"reset_all_origins": true,
					"warnings":          permissionScopeWarnings(),
				},
				"next_commands": []string{"cdp permissions grant notifications --origin https://example.com --json"},
			}
			return a.render(ctx, "permissions reset", report)
		},
	}
	return cmd
}

func normalizePermissionOrigin(raw string) (string, error) {
	origin := strings.TrimSpace(raw)
	if origin == "" {
		return "", commandError("usage", "usage", "--origin is required", ExitUsage, []string{"cdp permissions grant notifications --origin https://example.com --json"})
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", commandError("usage", "usage", "--origin must be an absolute HTTP(S) origin", ExitUsage, []string{"cdp permissions grant geolocation --origin https://example.com --json"})
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", commandError("usage", "usage", "--origin must use http or https", ExitUsage, []string{"cdp permissions grant notifications --origin https://example.com --json"})
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func normalizePermissionNames(args []string) ([]string, error) {
	permissions := make([]string, 0, len(args))
	for _, arg := range args {
		permission := strings.TrimSpace(arg)
		if permission == "" {
			continue
		}
		permissions = append(permissions, permission)
	}
	if len(permissions) == 0 {
		return nil, commandError("usage", "usage", "at least one permission is required", ExitUsage, []string{"cdp permissions grant notifications --origin https://example.com --json"})
	}
	return permissions, nil
}

func permissionScopeWarnings() []string {
	return []string{
		"permissions are browser/profile scoped; prefer --browser-mode headless for disposable automation",
		"cdp permissions reset clears permission overrides for all origins in the selected browser runtime",
	}
}
