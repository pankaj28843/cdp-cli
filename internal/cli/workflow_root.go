package cli

import "github.com/spf13/cobra"

func (a *app) newWorkflowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Run high-level browser debugging workflows",
	}
	cmd.AddCommand(a.newWorkflowVerifyCommand())
	cmd.AddCommand(a.newWorkflowPerfCommand())
	cmd.AddCommand(a.newWorkflowA11yCommand())
	cmd.AddCommand(a.newWorkflowDebugBundleCommand())
	cmd.AddCommand(a.newWorkflowActionCaptureCommand())
	cmd.AddCommand(a.newWorkflowVisiblePostsCommand())
	cmd.AddCommand(a.newWorkflowHackerNewsCommand())
	cmd.AddCommand(a.newWorkflowConsoleErrorsCommand())
	cmd.AddCommand(a.newWorkflowNetworkFailuresCommand())
	cmd.AddCommand(a.newWorkflowPageLoadCommand())
	cmd.AddCommand(a.newWorkflowFeedsCommand())
	cmd.AddCommand(a.newWorkflowRenderedExtractCommand())
	cmd.AddCommand(a.newWorkflowWebResearchCommand())
	cmd.AddCommand(a.newWorkflowResponsiveAuditCommand())
	cmd.AddCommand(a.newWorkflowLighthouseCommand())
	return cmd
}
