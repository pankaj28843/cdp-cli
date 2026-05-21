package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
)

func (a *app) newBrowserCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browser",
		Short: "Inspect and prepare browser runtime modes",
	}
	cmd.AddCommand(a.newBrowserModeCommand())
	cmd.AddCommand(a.newBrowserProfileCommand())
	return cmd
}

func (a *app) newBrowserModeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mode",
		Short: "Inspect the selected browser runtime mode",
	}
	cmd.AddCommand(a.newBrowserModeGetCommand())
	return cmd
}

func (a *app) newBrowserModeGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the effective browser runtime mode",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			mode, err := a.resolveBrowserMode(cmd)
			if err != nil {
				return err
			}

			selected, err := a.selectedConnectionSummary(ctx)
			if err != nil {
				return err
			}

			data := map[string]any{
				"ok":                  true,
				"browser_mode":        mode.Mode,
				"browser_mode_source": mode.Source,
				"config_path":         mode.ConfigPath,
				"next_commands":       mode.NextCommands,
			}
			if len(mode.Warnings) > 0 {
				data["warnings"] = mode.Warnings
			}
			if selected != nil {
				data["selected_connection"] = selected
			}

			return a.render(ctx, fmt.Sprintf("browser mode %s (%s)", mode.Mode, mode.Source), data)
		},
	}
}

func (a *app) newBrowserProfileCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Inspect and seed managed headless browser profiles",
		Long:  "Inspect and seed the cdp-owned managed profile used by --browser-mode headless. The managed strategy creates an empty owner-only profile and never copies the default Chrome profile.",
	}
	cmd.AddCommand(a.newBrowserProfileStatusCommand())
	cmd.AddCommand(a.newBrowserProfileSeedCommand())
	return cmd
}

func (a *app) newBrowserProfileStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show managed headless browser profile status",
		Long:  "Show privacy-safe status for the cdp-owned managed headless profile, including owner-only permission checks and safe next commands.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			status, err := a.browserProfileStatus(ctx)
			if err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("browser profile %s", status.State), status)
		},
	}
}

func (a *app) newBrowserProfileSeedCommand() *cobra.Command {
	var strategy string
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Create managed headless browser profile metadata",
		Long:  "Create the cdp-owned managed profile metadata for --browser-mode headless. The managed strategy does not copy cookies, passwords, history, autofill, or default-profile files.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			if strings.TrimSpace(strategy) != "managed" {
				return commandError(
					"invalid_profile_seed_strategy",
					"usage",
					"--strategy must be managed",
					ExitUsage,
					[]string{"cdp browser profile seed --strategy managed --json"},
				)
			}

			store, err := a.stateStore()
			if err != nil {
				return err
			}
			metadata, err := browser.PrepareManagedProfile(store.Dir, time.Now().UTC())
			if err != nil {
				return err
			}
			status, err := browserProfileStatusForStore(ctx, store.Dir)
			if err != nil {
				return err
			}
			status.Seeded = true
			status.ManagedBrowser = browser.ManagedMetadataStatus(metadata)
			return a.render(ctx, "browser profile seeded", status)
		},
	}
	cmd.Flags().StringVar(&strategy, "strategy", "managed", "profile seed strategy; only managed is supported")
	return cmd
}

type browserProfileStatus struct {
	OK             bool                  `json:"ok"`
	BrowserMode    string                `json:"browser_mode"`
	StateDir       string                `json:"state_dir"`
	ProfileDir     string                `json:"profile_dir"`
	MetadataPath   string                `json:"metadata_path"`
	State          string                `json:"state"`
	Exists         bool                  `json:"exists"`
	Seeded         bool                  `json:"seeded"`
	ProfilePerm    string                `json:"profile_perm,omitempty"`
	MetadataPerm   string                `json:"metadata_perm,omitempty"`
	SeedStrategy   string                `json:"seed_strategy,omitempty"`
	LastSeededAt   string                `json:"last_seeded_at,omitempty"`
	LastLaunchAt   string                `json:"last_launch_at,omitempty"`
	ManagedBrowser browser.ManagedStatus `json:"managed_browser,omitempty"`
	Warnings       []string              `json:"warnings,omitempty"`
	NextCommands   []string              `json:"next_commands"`
}

func (a *app) browserProfileStatus(ctx context.Context) (browserProfileStatus, error) {
	store, err := a.stateStore()
	if err != nil {
		return browserProfileStatus{}, err
	}
	return browserProfileStatusForStore(ctx, store.Dir)
}

func browserProfileStatusForStore(ctx context.Context, stateDir string) (browserProfileStatus, error) {
	select {
	case <-ctx.Done():
		return browserProfileStatus{}, ctx.Err()
	default:
	}

	status := browserProfileStatus{
		OK:           true,
		BrowserMode:  "headless",
		StateDir:     stateDir,
		ProfileDir:   browser.ManagedProfileDir(stateDir),
		MetadataPath: browser.ManagedMetadataPath(stateDir),
		State:        "missing",
		SeedStrategy: "managed",
		NextCommands: browserProfileNextCommands(false),
	}

	profileInfo, profileErr := os.Stat(status.ProfileDir)
	if profileErr == nil {
		status.Exists = true
		status.ProfilePerm = fmt.Sprintf("%03o", profileInfo.Mode().Perm())
		if profileInfo.Mode().Perm() != 0o700 {
			status.Warnings = append(status.Warnings, "managed profile permissions should be 0700")
		}
	} else if !os.IsNotExist(profileErr) {
		return browserProfileStatus{}, fmt.Errorf("stat managed profile: %w", profileErr)
	}

	metadataInfo, metadataErr := os.Stat(status.MetadataPath)
	if metadataErr == nil {
		status.MetadataPerm = fmt.Sprintf("%03o", metadataInfo.Mode().Perm())
		if metadataInfo.Mode().Perm() != 0o600 {
			status.Warnings = append(status.Warnings, "managed metadata permissions should be 0600")
		}
	} else if !os.IsNotExist(metadataErr) {
		return browserProfileStatus{}, fmt.Errorf("stat managed metadata: %w", metadataErr)
	}

	metadata, ok, err := browser.LoadManagedMetadata(stateDir)
	if err != nil {
		return browserProfileStatus{}, err
	}
	if ok {
		status.Seeded = true
		status.SeedStrategy = metadata.ProfileSeedStrategy
		status.LastSeededAt = metadata.LastSeededAt
		status.LastLaunchAt = metadata.StartedAt
		status.ManagedBrowser = browser.ManagedMetadataStatus(metadata)
	}

	switch {
	case status.Exists && status.Seeded:
		status.State = "ready"
	case status.Exists:
		status.State = "profile_exists"
	case status.Seeded:
		status.State = "metadata_only"
	default:
		status.State = "missing"
	}
	status.NextCommands = browserProfileNextCommands(status.Seeded)
	return status, nil
}

func browserProfileNextCommands(seeded bool) []string {
	if seeded {
		return []string{
			"cdp --browser-mode headless daemon keepalive --repair --json",
			"cdp browser profile status --json",
		}
	}
	return []string{
		"cdp browser profile seed --strategy managed --json",
		"cdp browser profile status --json",
	}
}

type selectedConnectionSummary struct {
	Name           string `json:"name"`
	ConnectionMode string `json:"connection_mode"`
	Source         string `json:"source"`
	AutoConnect    bool   `json:"auto_connect"`
	Channel        string `json:"channel,omitempty"`
	Project        string `json:"project,omitempty"`
}

func (a *app) selectedConnectionSummary(ctx context.Context) (*selectedConnectionSummary, error) {
	conn, source, ok, err := a.resolveConnection(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return &selectedConnectionSummary{
		Name:           conn.Name,
		ConnectionMode: connectionModeForSummary(conn),
		Source:         source,
		AutoConnect:    conn.AutoConnect,
		Channel:        conn.Channel,
		Project:        conn.Project,
	}, nil
}

func connectionModeForSummary(conn state.Connection) string {
	if conn.Mode != "" {
		return conn.Mode
	}
	if conn.AutoConnect {
		return "auto_connect"
	}
	return "browser_url"
}
