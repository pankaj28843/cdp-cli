package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newOpenCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var newTab bool
	var retryOpts commandRetryOptions
	cmd := &cobra.Command{
		Use:   "open <url>",
		Short: "Open a URL in a new tab or navigate a selected page",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			rawURL := strings.TrimSpace(args[0])
			result, retryReport, err := runCommandWithRetry(ctx, retryOpts, func(attemptCtx context.Context) (commandRetryResult, error) {
				client, closeClient, err := a.browserCDPClient(attemptCtx)
				if err != nil {
					return commandRetryResult{}, commandError(
						"connection_not_configured",
						"connection",
						err.Error(),
						ExitConnection,
						a.connectionRemediationCommands(),
					)
				}
				closeOwned := true
				defer func() {
					if closeOwned {
						_ = closeClient(attemptCtx)
					}
				}()

				pageAction := "created"
				frameID := ""
				target := cdp.TargetInfo{Type: "page", URL: rawURL}
				if newTab || (targetID == "" && urlContains == "") {
					createdID, err := a.createPageTarget(attemptCtx, client, rawURL)
					if err != nil {
						if createdID != "" {
							target.TargetID = createdID
							return commandRetryResult{Target: &target}, err
						}
						return commandRetryResult{}, err
					}
					target.TargetID = createdID
				} else {
					selected, err := a.resolvePageTargetWithClient(attemptCtx, client, targetID, urlContains, titleContains)
					if err != nil {
						return commandRetryResult{}, err
					}
					session, err := cdp.AttachToTargetWithClient(attemptCtx, client, selected.TargetID, closeClient)
					if err != nil {
						return commandRetryResult{Target: &selected}, commandError(
							"connection_failed",
							"connection",
							fmt.Sprintf("attach target %s: %v", selected.TargetID, err),
							ExitConnection,
							[]string{"cdp pages --json", "cdp doctor --json"},
						)
					}
					closeOwned = false
					defer session.Close(attemptCtx)
					frameID, err = session.Navigate(attemptCtx, rawURL)
					if err != nil {
						return commandRetryResult{Target: &selected}, commandError(
							"connection_failed",
							"connection",
							fmt.Sprintf("navigate target %s: %v", selected.TargetID, err),
							ExitConnection,
							[]string{"cdp pages --json", "cdp doctor --json"},
						)
					}
					target = selected
					target.URL = rawURL
					pageAction = "navigated"
				}

				page := pageRow(target)
				page["action"] = pageAction
				page["frame_id"] = frameID
				return commandRetryResult{
					Human:  fmt.Sprintf("%s\t%s\t%s", pageAction, target.TargetID, rawURL),
					Target: &target,
					Data: map[string]any{
						"ok":     true,
						"action": pageAction,
						"page":   page,
					},
				}, nil
			})
			if err != nil {
				return err
			}
			attachCommandRetryReport(result.Data, retryReport)
			return a.render(ctx, result.Human, result.Data)
		},
	}
	cmd.Flags().BoolVar(&newTab, "new-tab", true, "open a new tab instead of navigating an existing page")
	cmd.Flags().StringVar(&targetID, "target", "", "navigate a page target by exact id or unique prefix when --new-tab=false")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "navigate the first page whose URL contains this text when --new-tab=false")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "navigate the first page whose title contains this text when --new-tab=false")
	addCommandRetryFlags(cmd, &retryOpts)
	return cmd
}
