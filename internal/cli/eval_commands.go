package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func (a *app) newEvalCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var awaitPromise bool
	var retryOpts commandRetryOptions
	cmd := &cobra.Command{
		Use:   "eval <expression>",
		Short: "Evaluate JavaScript in a page target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			result, retryReport, err := runCommandWithRetry(ctx, retryOpts, func(attemptCtx context.Context) (commandRetryResult, error) {
				session, target, err := a.attachPageSession(attemptCtx, targetID, urlContains, titleContains)
				if err != nil {
					if target.TargetID != "" {
						return commandRetryResult{Target: &target}, err
					}
					return commandRetryResult{}, err
				}
				defer session.Close(attemptCtx)

				result, err := session.Evaluate(attemptCtx, args[0], awaitPromise)
				if err != nil {
					if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
						timeoutErr := attemptCtx.Err()
						if timeoutErr == nil {
							timeoutErr = err
						}
						return commandRetryResult{Target: &target}, commandError(
							"timeout",
							"timeout",
							fmt.Sprintf("evaluate target %s: %v", target.TargetID, timeoutErr),
							ExitTimeout,
							[]string{"cdp eval 'document.title' --timeout 15s --json", "cdp pages --json"},
						)
					}
					return commandRetryResult{Target: &target}, commandError(
						"connection_failed",
						"connection",
						fmt.Sprintf("evaluate target %s: %v", target.TargetID, err),
						ExitConnection,
						[]string{"cdp pages --json", "cdp doctor --json"},
					)
				}
				if result.Exception != nil {
					return commandRetryResult{Target: &target}, commandError(
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
				return commandRetryResult{
					Human:  human,
					Target: &target,
					Data: map[string]any{
						"ok":     true,
						"target": pageRow(target),
						"result": result.Object,
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
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().BoolVar(&awaitPromise, "await-promise", true, "wait for promise results before returning")
	addCommandRetryFlags(cmd, &retryOpts)
	return cmd
}
