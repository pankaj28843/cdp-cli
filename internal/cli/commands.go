package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

type commandInfo struct {
	Name     string        `json:"name"`
	Use      string        `json:"use"`
	Short    string        `json:"short,omitempty"`
	Aliases  []string      `json:"aliases,omitempty"`
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
	return &cobra.Command{
		Use:   "describe",
		Short: "Describe the command tree as JSON for agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			data := map[string]any{
				"ok":       true,
				"commands": describeCommand(a.root),
				"globals":  []string{"--json", "--jq", "--debug", "--timeout", "--profile", "--config"},
			}
			return a.render(ctx, "Use --json to print the command tree.", data)
		},
	}
}

func (a *app) newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run local readiness checks",
		Long:  "Run readiness checks for the CLI. Browser and daemon checks will be added with the attach daemon.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			data := map[string]any{
				"ok": true,
				"checks": []map[string]any{
					{"name": "cli", "status": "pass", "message": "command scaffold is installed"},
					{"name": "daemon", "status": "pending", "message": "attach daemon is not implemented yet"},
					{"name": "browser", "status": "pending", "message": "browser checks are not implemented yet"},
				},
			}
			return a.render(ctx, "cli: pass\ndaemon: pending\nbrowser: pending", data)
		},
	}
}

func (a *app) newDaemonCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the long-running Chrome attach daemon",
	}
	cmd.AddCommand(planned("start", "Start the attach daemon"))
	cmd.AddCommand(planned("status", "Show attach daemon status"))
	cmd.AddCommand(planned("stop", "Stop the attach daemon"))
	cmd.AddCommand(planned("logs", "Show attach daemon logs"))
	return cmd
}

func (a *app) newTargetsCommand() *cobra.Command {
	return planned("targets", "List browser targets")
}

func (a *app) newPagesCommand() *cobra.Command {
	return planned("pages", "List open pages and tabs")
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
	cmd.AddCommand(planned("metadata", "Print the live protocol metadata"))
	cmd.AddCommand(planned("domains", "List CDP domains"))
	cmd.AddCommand(planned("search <query>", "Search CDP domains, methods, events, and types"))
	cmd.AddCommand(planned("describe <Domain.method>", "Describe a CDP method schema"))
	cmd.AddCommand(planned("exec <Domain.method>", "Execute a raw CDP method"))
	return cmd
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
		Name:    cmd.Name(),
		Use:     cmd.UseLine(),
		Short:   cmd.Short,
		Aliases: cmd.Aliases,
	}

	for _, child := range cmd.Commands() {
		if child.Hidden {
			continue
		}
		info.Children = append(info.Children, describeCommand(child))
	}

	return info
}
