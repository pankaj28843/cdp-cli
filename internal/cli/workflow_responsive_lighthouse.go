package cli

import (
	"time"

	"github.com/spf13/cobra"
)

func (a *app) newWorkflowResponsiveAuditCommand() *cobra.Command {
	var viewports, include, outDir string
	var wait time.Duration
	var limit int
	cmd := &cobra.Command{
		Use:   "responsive-audit <url>",
		Short: "Audit a URL across desktop, tablet, and mobile viewport presets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || limit < 0 {
				return commandError("usage", "usage", "--wait and --limit must be non-negative", ExitUsage, []string{"cdp workflow responsive-audit https://example.com --wait 5s --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, wait+30*time.Second)
			defer cancel()
			return runResponsiveAuditWorkflow(ctx, a, args[0], responsiveAuditOptions{Viewports: viewports, Include: include, OutDir: outDir, Wait: wait, Limit: limit})
		},
	}
	cmd.Flags().StringVar(&viewports, "viewports", "desktop,tablet,mobile", "comma-separated viewport presets: desktop, tablet, mobile")
	cmd.Flags().StringVar(&include, "include", "console,network,layout,screenshot,a11y", "signals to collect: console, network, layout, screenshot, a11y, all")
	cmd.Flags().StringVar(&outDir, "out-dir", "tmp/responsive-audit", "directory for screenshot artifacts")
	cmd.Flags().DurationVar(&wait, "wait", 3*time.Second, "how long to collect events after each viewport navigation")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum console, network, and layout items per viewport; use 0 for no limit")
	return cmd
}

func (a *app) newWorkflowLighthouseCommand() *cobra.Command {
	var categories, formFactor, throttling, outDir string
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "lighthouse <url>",
		Short: "Run Lighthouse CLI and summarize category scores",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 {
				return commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp workflow lighthouse https://example.com --wait 5s --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 2*time.Minute)
			defer cancel()
			return runLighthouseWorkflow(ctx, a, args[0], lighthouseWorkflowOptions{Categories: categories, FormFactor: formFactor, Throttling: throttling, OutDir: outDir, Wait: wait})
		},
	}
	cmd.Flags().StringVar(&categories, "categories", "accessibility,best-practices,performance,seo", "comma-separated Lighthouse categories")
	cmd.Flags().StringVar(&formFactor, "form-factor", "mobile", "Lighthouse form factor: mobile or desktop")
	cmd.Flags().StringVar(&throttling, "throttling", "simulate", "Lighthouse throttling method: simulate, devtools, or provided")
	cmd.Flags().StringVar(&outDir, "out-dir", "tmp/lighthouse", "directory for Lighthouse JSON and HTML reports")
	cmd.Flags().DurationVar(&wait, "wait", 0, "optional pre-audit wait hint included in output")
	return cmd
}
