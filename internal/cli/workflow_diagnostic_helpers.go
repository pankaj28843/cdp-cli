package cli

import (
	"context"
	"errors"
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

func (a *app) selectOrCreateDiagnosticPage(ctx context.Context, client cdp.CommandClient, rawURL, workflow string, targetIndex int) (cdp.TargetInfo, bool, error) {
	if targetIndex > 0 {
		target, err := a.resolvePageTargetWithClientIndex(ctx, client, "", "", "", targetIndex)
		return target, false, err
	}
	targetID, err := a.createWorkflowPageTarget(ctx, client, "about:blank", workflow)
	if err != nil {
		return cdp.TargetInfo{}, false, err
	}
	return cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}, true, nil
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

func diagnosticWorkflowErrorWithCleanup(workflow string, primary error, cleanup renderedExtractCleanupResult) error {
	code := "workflow_" + workflow + "_cleanup_failed"
	if primary == nil {
		if cleanup.Error == "" {
			return nil
		}
		return commandErrorWithData(
			code,
			"cleanup",
			fmt.Sprintf("workflow %s completed but its workflow-owned page cleanup did not settle", workflow),
			ExitInternal,
			diagnosticWorkflowCleanupRemediation(cleanup),
			map[string]any{"cleanup": cleanup},
		)
	}
	var rendered *renderedResultExit
	if errors.As(primary, &rendered) {
		return primary
	}
	if cleanup.Error == "" {
		var commandErr *CommandError
		if errors.As(primary, &commandErr) {
			attachDiagnosticCleanupData(commandErr, cleanup)
			return primary
		}
		return commandErrorWithData(
			"workflow_"+workflow+"_failed",
			"runtime",
			primary.Error(),
			ExitInternal,
			diagnosticWorkflowCleanupRemediation(cleanup),
			map[string]any{"primary_error": commandErrorSummary(primary), "cleanup": cleanup},
		)
	}
	var commandErr *CommandError
	if errors.As(primary, &commandErr) && commandErr.Code == code {
		return primary
	}
	return commandErrorWithData(
		code,
		"cleanup",
		fmt.Sprintf("workflow %s failed and its workflow-owned page cleanup did not settle: %s", workflow, cleanup.Error),
		ExitInternal,
		diagnosticWorkflowCleanupRemediation(cleanup),
		map[string]any{"primary_error": commandErrorSummary(primary), "cleanup": cleanup},
	)
}

func attachDiagnosticCleanupData(commandErr *CommandError, cleanup renderedExtractCleanupResult) {
	if commandErr == nil {
		return
	}
	if data, ok := commandErr.Data.(map[string]any); ok {
		copyData := make(map[string]any, len(data)+1)
		for key, value := range data {
			copyData[key] = value
		}
		copyData["cleanup"] = cleanup
		commandErr.Data = copyData
		return
	}
	if commandErr.Data == nil {
		commandErr.Data = map[string]any{"cleanup": cleanup}
		return
	}
	commandErr.Data = map[string]any{"primary_data": commandErr.Data, "cleanup": cleanup}
}

func diagnosticWorkflowCleanupRemediation(cleanup renderedExtractCleanupResult) []string {
	commands := []string{}
	if strings.TrimSpace(cleanup.RecoveryCommand) != "" {
		commands = append(commands, cleanup.RecoveryCommand)
	}
	return append(commands, "cdp pages --json")
}
