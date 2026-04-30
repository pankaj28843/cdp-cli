package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type clickResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Clicked  bool       `json:"clicked"`
	Error    *evalError `json:"error,omitempty"`
}

type fillResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Filled   bool       `json:"filled"`
	Value    string     `json:"value"`
	Previous string     `json:"previous"`
	Error    *evalError `json:"error,omitempty"`
}

type typeResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Typing   bool       `json:"typing"`
	Typed    string     `json:"typed"`
	Previous string     `json:"previous"`
	Error    *evalError `json:"error,omitempty"`
}

type pressResult struct {
	URL        string     `json:"url"`
	Title      string     `json:"title"`
	Selector   string     `json:"selector"`
	Key        string     `json:"key"`
	Count      int        `json:"count"`
	Dispatched bool       `json:"dispatched"`
	Error      *evalError `json:"error,omitempty"`
}

type hoverResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Hovered  bool       `json:"hovered"`
	X        float64    `json:"x"`
	Y        float64    `json:"y"`
	Error    *evalError `json:"error,omitempty"`
}

type dragResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Dragged  bool       `json:"dragged"`
	DeltaX   int        `json:"delta_x"`
	DeltaY   int        `json:"delta_y"`
	StartX   float64    `json:"start_x"`
	StartY   float64    `json:"start_y"`
	EndX     float64    `json:"end_x"`
	EndY     float64    `json:"end_y"`
	Error    *evalError `json:"error,omitempty"`
}

type frameTreeResponse struct {
	FrameTree *frameTreeNode `json:"frameTree"`
}

type frameTreeNode struct {
	Frame       *frameInfo      `json:"frame"`
	ChildFrames []frameTreeNode `json:"childFrames"`
}

type frameInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	SecurityOrigin string `json:"securityOrigin"`
	MimeType       string `json:"mimeType"`
}

type frameSummary struct {
	FrameID        string `json:"frame_id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	SecurityOrigin string `json:"security_origin"`
	MimeType       string `json:"mime_type"`
	ParentID       string `json:"parent_id"`
	ChildCount     int    `json:"child_count"`
}

func (a *app) newClickCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "click <selector>",
		Short: "Click the first matching element for a CSS selector",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result clickResult
			if err := evaluateJSONValue(ctx, session, clickExpression(args[0]), "click", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp click main --json")
			}
			if !result.Clicked {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no matching element found for selector %q", args[0]),
					ExitUsage,
					[]string{"cdp click main --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("clicked\t%s\t%s", target.TargetID, result.Selector), map[string]any{
				"ok":     true,
				"action": "clicked",
				"target": pageRow(target),
				"click":  result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newFillCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "fill <selector> <value>",
		Short: "Set the value of the first matching form control",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result fillResult
			if err := evaluateJSONValue(ctx, session, fillExpression(args[0], args[1]), "fill", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("fill %q: %s", args[0], result.Error.Message),
					ExitUsage,
					[]string{"cdp fill input.email example@example.com --json"},
				)
			}
			if !result.Filled {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no editable element found for selector %q", args[0]),
					ExitUsage,
					[]string{"cdp fill #name Alice --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("filled\t%s\t%s", target.TargetID, result.Selector), map[string]any{
				"ok":     true,
				"action": "filled",
				"target": pageRow(target),
				"fill":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newTypeCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "type <selector> <text>",
		Short: "Type text into the first matching form control",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result typeResult
			if err := evaluateJSONValue(ctx, session, typeExpression(args[0], args[1]), "type", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("type %q: %s", args[0], result.Error.Message),
					ExitUsage,
					[]string{"cdp type input[name='email'] user@example.com --json"},
				)
			}
			if !result.Typing {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no editable element found for selector %q", args[0]),
					ExitUsage,
					[]string{"cdp type #name Alice --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("typed\t%s\t%s", target.TargetID, result.Selector), map[string]any{
				"ok":     true,
				"action": "typed",
				"target": pageRow(target),
				"type":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newPressCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var selector string
	cmd := &cobra.Command{
		Use:   "press <key>",
		Short: "Press a key on the focused element or an optional selector",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result pressResult
			if err := evaluateJSONValue(ctx, session, pressExpression(args[0], selector), "press", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("press %q: %s", args[0], result.Error.Message),
					ExitUsage,
					[]string{"cdp press Enter --selector 'input[name=\"q\"]' --json"},
				)
			}
			if !result.Dispatched {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no target found for keypress %q", args[0]),
					ExitUsage,
					[]string{"cdp press Enter --selector 'body' --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("pressed\t%s\t%q", target.TargetID, result.Key), map[string]any{
				"ok":     true,
				"action": "pressed",
				"target": pageRow(target),
				"press":  result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&selector, "selector", "", "optional selector to focus before pressing the key")
	return cmd
}

func (a *app) newHoverCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "hover <selector>",
		Short: "Dispatch pointer hover events over the first matching element",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result hoverResult
			if err := evaluateJSONValue(ctx, session, hoverExpression(args[0]), "hover", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("hover %q: %s", args[0], result.Error.Message),
					ExitUsage,
					[]string{"cdp hover 'button.primary' --json"},
				)
			}
			if !result.Hovered {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no matching element found for selector %q", args[0]),
					ExitUsage,
					[]string{"cdp hover 'button.primary' --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("hovered\t%s\t%s", target.TargetID, result.Selector), map[string]any{
				"ok":     true,
				"action": "hovered",
				"target": pageRow(target),
				"hover":  result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newDragCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "drag <selector> <dx> <dy>",
		Short: "Drag the first matching element by a delta",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			dx, err := strconv.Atoi(strings.TrimSpace(args[1]))
			if err != nil {
				return commandError("invalid_argument", "usage", "dx must be an integer", ExitUsage, []string{"cdp drag '.node' 10 20 --json"})
			}
			dy, err := strconv.Atoi(strings.TrimSpace(args[2]))
			if err != nil {
				return commandError("invalid_argument", "usage", "dy must be an integer", ExitUsage, []string{"cdp drag '.node' 10 20 --json"})
			}

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result dragResult
			if err := evaluateJSONValue(ctx, session, dragExpression(args[0], dx, dy), "drag", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("drag %q: %s", args[0], result.Error.Message),
					ExitUsage,
					[]string{"cdp drag '#drag-me' 10 20 --json"},
				)
			}
			if !result.Dragged {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no matching element found for selector %q", args[0]),
					ExitUsage,
					[]string{"cdp drag '#drag-me' 10 20 --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("dragged\t%s\t%s", target.TargetID, result.Selector), map[string]any{
				"ok":     true,
				"action": "dragged",
				"target": pageRow(target),
				"drag":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}
