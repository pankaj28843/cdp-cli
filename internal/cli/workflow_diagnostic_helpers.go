package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func diagnosticWorkflowURL(cmd *cobra.Command, args []string, targetIndex int, example string) (string, error) {
	if err := validatePageTargetIndexSelector(cmd, "", "", "", targetIndex); err != nil {
		return "", err
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		if targetIndex > 0 {
			return "", nil
		}
		return "", commandError("usage", "usage", "a URL is required unless --target-index selects an existing page", ExitUsage, []string{example})
	}
	return strings.TrimSpace(args[0]), nil
}

func (a *app) selectOrCreateDiagnosticPage(ctx context.Context, client cdp.CommandClient, rawURL, workflow string, targetIndex int) (cdp.TargetInfo, error) {
	if targetIndex > 0 {
		return a.resolvePageTargetWithClientIndex(ctx, client, "", "", "", targetIndex)
	}
	targetID, err := a.createWorkflowPageTarget(ctx, client, "about:blank", workflow)
	if err != nil {
		return cdp.TargetInfo{}, err
	}
	return cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}, nil
}

func diagnosticWorkflowTargetCommand(workflow, rawURL string, targetIndex int, targetID string) string {
	if rawURL != "" {
		return fmt.Sprintf("cdp workflow %s %s --json", workflow, rawURL)
	}
	if targetIndex > 0 {
		return fmt.Sprintf("cdp workflow %s --target-index %d --json", workflow, targetIndex)
	}
	return fmt.Sprintf("cdp workflow %s --target %s --json", workflow, targetID)
}
