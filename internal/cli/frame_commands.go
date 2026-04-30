package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *app) newFramesCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "frames",
		Short: "List the page frame tree for the selected target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result frameTreeResponse
			if err := execSessionJSON(ctx, session, "Page.getFrameTree", map[string]any{}, &result); err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("frames target %s: %v", session.TargetID, err),
					ExitConnection,
					[]string{"cdp frames --json"},
				)
			}
			frames := collectFrameSummaries(result.FrameTree, "")
			return a.render(ctx, fmt.Sprintf("frames\t%s\t%d", target.TargetID, len(frames)), map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"frames": frames,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

type textResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Text     string     `json:"text"`
	Items    []textItem `json:"items"`
	Error    *evalError `json:"error,omitempty"`
}
