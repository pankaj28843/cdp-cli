package cli

import (
	"context"
	"fmt"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

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
