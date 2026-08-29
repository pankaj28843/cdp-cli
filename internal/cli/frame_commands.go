package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *app) newFramesCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var targetIndex int
	cmd := &cobra.Command{
		Use:   "frames",
		Short: "List the page frame tree for the selected target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
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
			report := map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"frames": frames,
			}
			if targetIndex > 0 {
				report["target_index"] = targetIndex
			}
			return a.render(ctx, fmt.Sprintf("frames\t%s\t%d", target.TargetID, len(frames)), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based page target index")
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
