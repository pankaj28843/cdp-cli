package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func (a *app) newConsoleCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var wait time.Duration
	var limit int
	var errorsOnly bool
	var types string
	cmd := &cobra.Command{
		Use:   "console",
		Short: "Capture console and browser log messages from a page target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if wait < 0 {
				return commandError(
					"usage",
					"usage",
					"--wait must be non-negative",
					ExitUsage,
					[]string{"cdp console --wait 2s --json"},
				)
			}
			if limit < 0 {
				return commandError(
					"usage",
					"usage",
					"--limit must be non-negative",
					ExitUsage,
					[]string{"cdp console --limit 50 --json"},
				)
			}

			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			typeSet := parseCSVSet(types)
			messages, truncated, err := collectConsoleMessages(ctx, client, session.SessionID, wait, limit, errorsOnly, typeSet)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture console target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			lines := consoleMessageLines(messages)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"messages": messages,
				"console": map[string]any{
					"count":       len(messages),
					"wait":        durationString(wait),
					"limit":       limit,
					"truncated":   truncated,
					"errors_only": errorsOnly,
					"types":       setKeys(typeSet),
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&wait, "wait", time.Second, "how long to collect console/log events after attaching")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of messages to return; use 0 for no limit")
	cmd.Flags().BoolVar(&errorsOnly, "errors", false, "only return warnings, errors, assertions, and exceptions")
	cmd.Flags().StringVar(&types, "types", "", "comma-separated console types or log levels to keep, such as error,warning")
	return cmd
}
