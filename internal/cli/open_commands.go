package cli

import (
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
	cmd := &cobra.Command{
		Use:   "open <url>",
		Short: "Open a URL in a new tab or navigate a selected page",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
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
					_ = closeClient(ctx)
				}
			}()
			rawURL := strings.TrimSpace(args[0])
			pageAction := "created"
			frameID := ""
			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			if newTab || (targetID == "" && urlContains == "") {
				createdID, err := a.createPageTarget(ctx, client, rawURL)
				if err != nil {
					return err
				}
				target.TargetID = createdID
			} else {
				selected, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
				if err != nil {
					return err
				}
				closeOwned = false
				session, err := cdp.AttachToTargetWithClient(ctx, client, selected.TargetID, closeClient)
				if err != nil {
					closeOwned = true
					return commandError(
						"connection_failed",
						"connection",
						fmt.Sprintf("attach target %s: %v", selected.TargetID, err),
						ExitConnection,
						[]string{"cdp pages --json", "cdp doctor --json"},
					)
				}
				defer session.Close(ctx)
				frameID, err = session.Navigate(ctx, rawURL)
				if err != nil {
					return commandError(
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
			human := fmt.Sprintf("%s\t%s\t%s", pageAction, target.TargetID, rawURL)
			return a.render(ctx, human, map[string]any{
				"ok":     true,
				"action": pageAction,
				"page":   page,
			})
		},
	}
	cmd.Flags().BoolVar(&newTab, "new-tab", true, "open a new tab instead of navigating an existing page")
	cmd.Flags().StringVar(&targetID, "target", "", "navigate a page target by exact id or unique prefix when --new-tab=false")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "navigate the first page whose URL contains this text when --new-tab=false")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "navigate the first page whose title contains this text when --new-tab=false")
	return cmd
}
