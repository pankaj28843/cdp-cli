package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *app) newEvalCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var awaitPromise bool
	cmd := &cobra.Command{
		Use:   "eval <expression>",
		Short: "Evaluate JavaScript in a page target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			result, err := session.Evaluate(ctx, args[0], awaitPromise)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("evaluate target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			if result.Exception != nil {
				return commandError(
					"javascript_exception",
					"runtime",
					fmt.Sprintf("javascript exception: %s", result.Exception.Text),
					ExitCheckFailed,
					[]string{"cdp eval 'document.title' --json", "cdp pages --json"},
				)
			}
			human := string(result.Object.Value)
			if human == "" {
				human = result.Object.Description
			}
			return a.render(ctx, human, map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"result": result.Object,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().BoolVar(&awaitPromise, "await-promise", true, "wait for promise results before returning")
	return cmd
}
