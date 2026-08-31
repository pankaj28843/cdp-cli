package cli

import (
	"context"
	"fmt"
	"sync"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func idempotentContextCloser(closeClient func(context.Context) error) func(context.Context) error {
	var once sync.Once
	var closeErr error
	return func(ctx context.Context) error {
		once.Do(func() {
			if closeClient != nil {
				closeErr = closeClient(ctx)
			}
		})
		return closeErr
	}
}

func workflowOwnedCleanupError(workflow string, primary error, cleanup renderedExtractCleanupResult, remediation []string) error {
	if cleanup.Error == "" {
		return primary
	}
	data := map[string]any{"cleanup": cleanup}
	message := fmt.Sprintf("%s workflow-owned page cleanup failed: %s", workflow, cleanup.Error)
	if primary != nil {
		data["primary_error"] = commandErrorSummary(primary)
		message = fmt.Sprintf("%s; workflow-owned page cleanup failed: %s", primary.Error(), cleanup.Error)
	}
	commands := make([]string, 0, len(remediation)+1)
	if cleanup.RecoveryCommand != "" {
		commands = append(commands, cleanup.RecoveryCommand)
	}
	commands = append(commands, remediation...)
	return commandErrorWithData(workflow+"_cleanup_failed", "cleanup", message, ExitInternal, uniqueCommands(commands), data)
}

// workflowPageCloser returns an idempotent cleanup operation for a page a
// workflow created. Cleanup deliberately owns its independent bounded context
// so a command timeout or an earlier error cannot strand the disposable page.
func (a *app) workflowPageCloser(client cdp.CommandClient, targetID, rawURL string, keepOpen bool) func() (bool, string) {
	closed := false
	return func() (bool, string) {
		if keepOpen || closed {
			return false, ""
		}
		closed = true

		closeCtx, cancel := context.WithTimeout(context.Background(), pageCloseDefaultTimeout(a.browserModeName(), defaultPageCloseMaxAttempts))
		defer cancel()
		report := closePageTargetSettled(closeCtx, client, cdp.TargetInfo{
			TargetID: targetID,
			Type:     "page",
			URL:      rawURL,
		}, pageCloseOptions{
			WaitGone:     true,
			MaxAttempts:  defaultPageCloseMaxAttempts,
			AttemptWait:  pageCloseAttemptTimeout(a.browserModeName()),
			PollInterval: defaultPageClosePollInterval,
			RetryBackoff: defaultPageCloseRetryBackoff,
		})
		if report.TargetGone {
			return true, ""
		}
		if report.LastError != "" {
			return false, report.LastError
		}
		return false, fmt.Sprintf("target %s close did not settle", targetID)
	}
}
