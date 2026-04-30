package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"path/filepath"
)

func (a *app) newFocusCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	cmd := &cobra.Command{
		Use:   "focus <selector>",
		Short: "Focus the first matching element for a CSS selector",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			var result map[string]any
			if err := evaluateJSONValue(ctx, session, focusExpression(args[0]), "focus", &result); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("focus\t%s", args[0]), map[string]any{"ok": true, "target": pageRow(target), "focus": result})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newClearCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	cmd := &cobra.Command{
		Use:   "clear <selector>",
		Short: "Clear the value of the first matching form control",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			var result map[string]any
			if err := evaluateJSONValue(ctx, session, clearExpression(args[0]), "clear", &result); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("clear\t%s", args[0]), map[string]any{"ok": true, "target": pageRow(target), "clear": result})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newSelectCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	cmd := &cobra.Command{
		Use:   "select <selector> <value>",
		Short: "Select an option value in the first matching select control",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			var result map[string]any
			if err := evaluateJSONValue(ctx, session, selectExpression(args[0], args[1]), "select", &result); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("select\t%s", args[0]), map[string]any{"ok": true, "target": pageRow(target), "select": result})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newFileCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	cmd := &cobra.Command{
		Use:   "file <selector> <path>",
		Short: "Set a file input to a local file path without printing file contents",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(args[1]); err != nil {
				return commandError("usage", "usage", fmt.Sprintf("file path is not readable: %v", err), ExitUsage, []string{"cdp file input[type=file] tmp/upload.txt --json"})
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			var result map[string]any
			if err := evaluateJSONValue(ctx, session, fileInputExpression(args[0], filepath.Base(args[1])), "file", &result); err != nil {
				return err
			}
			result["path"] = args[1]
			result["content_omitted"] = true
			return a.render(ctx, fmt.Sprintf("file\t%s\t%s", args[0], args[1]), map[string]any{"ok": true, "target": pageRow(target), "file": result})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newDialogCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "dialog", Short: "Observe and handle JavaScript dialogs"}
	cmd.AddCommand(planned("wait", "Wait for the next JavaScript dialog"))
	cmd.AddCommand(a.newDialogHandleCommand("accept", true))
	cmd.AddCommand(a.newDialogHandleCommand("dismiss", false))
	return cmd
}

func (a *app) newDialogHandleCommand(name string, accept bool) *cobra.Command {
	var targetID, urlContains, titleContains, promptText string
	cmd := &cobra.Command{
		Use:   name,
		Short: name + " the currently open JavaScript dialog",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			params := map[string]any{"accept": accept}
			if promptText != "" {
				params["promptText"] = promptText
			}
			if err := execSessionJSON(ctx, session, "Page.handleJavaScriptDialog", params, nil); err != nil {
				return commandError("connection_failed", "connection", fmt.Sprintf("handle dialog: %v", err), ExitConnection, []string{"cdp dialog wait --json"})
			}
			return a.render(ctx, "dialog "+name, map[string]any{"ok": true, "target": pageRow(target), "dialog": map[string]any{"action": name, "accepted": accept, "prompt_text_supplied": promptText != ""}})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&promptText, "prompt-text", "", "prompt text to send when accepting a prompt dialog")
	return cmd
}

func (a *app) newEmulateCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "emulate", Short: "Apply or clear target emulation settings"}
	cmd.AddCommand(a.newEmulateViewportCommand())
	cmd.AddCommand(a.newEmulateClearCommand())
	cmd.AddCommand(a.newEmulateMediaCommand())
	cmd.AddCommand(planned("network", "Apply a named network emulation preset"))
	cmd.AddCommand(planned("cpu", "Apply CPU throttling emulation"))
	cmd.AddCommand(planned("geolocation", "Apply geolocation override"))
	return cmd
}

func (a *app) newEmulateViewportCommand() *cobra.Command {
	var targetID, urlContains, titleContains, preset string
	var width, height int
	var dpr float64
	var mobile bool
	cmd := &cobra.Command{
		Use:   "viewport",
		Short: "Apply device metrics emulation to a page target",
		RunE: func(cmd *cobra.Command, args []string) error {
			if preset != "" {
				width, height, dpr, mobile = viewportPreset(preset)
			}
			if width <= 0 || height <= 0 || dpr <= 0 {
				return commandError("usage", "usage", "--width, --height, and --dpr must be positive", ExitUsage, []string{"cdp emulate viewport --preset mobile --json"})
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			params := map[string]any{"width": width, "height": height, "deviceScaleFactor": dpr, "mobile": mobile}
			if err := execSessionJSON(ctx, session, "Emulation.setDeviceMetricsOverride", params, nil); err != nil {
				return commandError("connection_failed", "connection", fmt.Sprintf("emulate viewport: %v", err), ExitConnection, []string{"cdp protocol describe Emulation.setDeviceMetricsOverride --json"})
			}
			return a.render(ctx, fmt.Sprintf("viewport\t%dx%d", width, height), map[string]any{"ok": true, "target": pageRow(target), "emulation": map[string]any{"viewport": params, "preset": preset, "cleanup_command": fmt.Sprintf("cdp emulate clear --target %s --json", target.TargetID)}})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&preset, "preset", "", "viewport preset: desktop, laptop, tablet, mobile, iphone-12")
	cmd.Flags().IntVar(&width, "width", 390, "viewport width in CSS pixels")
	cmd.Flags().IntVar(&height, "height", 844, "viewport height in CSS pixels")
	cmd.Flags().Float64Var(&dpr, "dpr", 1, "device scale factor")
	cmd.Flags().BoolVar(&mobile, "mobile", false, "enable mobile viewport mode")
	return cmd
}

func (a *app) newEmulateClearCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	cmd := &cobra.Command{Use: "clear", Short: "Clear viewport, media, and geolocation emulation", RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		_ = execSessionJSON(ctx, session, "Emulation.clearDeviceMetricsOverride", map[string]any{}, nil)
		_ = execSessionJSON(ctx, session, "Emulation.clearGeolocationOverride", map[string]any{}, nil)
		_ = execSessionJSON(ctx, session, "Emulation.setEmulatedMedia", map[string]any{}, nil)
		return a.render(ctx, "emulation cleared", map[string]any{"ok": true, "target": pageRow(target), "emulation": map[string]any{"cleared": true}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newEmulateMediaCommand() *cobra.Command {
	var targetID, urlContains, titleContains, colorScheme string
	cmd := &cobra.Command{Use: "media", Short: "Apply media feature emulation", RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		features := []map[string]string{}
		if colorScheme != "" {
			features = append(features, map[string]string{"name": "prefers-color-scheme", "value": colorScheme})
		}
		if err := execSessionJSON(ctx, session, "Emulation.setEmulatedMedia", map[string]any{"features": features}, nil); err != nil {
			return commandError("connection_failed", "connection", fmt.Sprintf("emulate media: %v", err), ExitConnection, []string{"cdp protocol describe Emulation.setEmulatedMedia --json"})
		}
		return a.render(ctx, "media emulation", map[string]any{"ok": true, "target": pageRow(target), "emulation": map[string]any{"media_features": features}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&colorScheme, "prefers-color-scheme", "", "emulate prefers-color-scheme: light or dark")
	return cmd
}

func (a *app) newA11yCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "a11y", Short: "Inspect accessibility tree information"}
	cmd.AddCommand(a.newA11yTreeCommand())
	cmd.AddCommand(a.newA11yFindCommand())
	cmd.AddCommand(a.newA11yNodeCommand())
	return cmd
}

func (a *app) newA11yTreeCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var depth, limit int
	var ignored bool
	cmd := &cobra.Command{Use: "tree", Short: "Return a bounded accessibility tree", RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		nodes, truncated, err := collectA11yNodes(ctx, session, depth, limit, ignored)
		if err != nil {
			return err
		}
		return a.render(ctx, fmt.Sprintf("a11y\t%d nodes", len(nodes)), map[string]any{"ok": true, "target": pageRow(target), "nodes": nodes, "truncated": truncated})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&depth, "depth", 4, "maximum tree depth to return")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum nodes to return")
	cmd.Flags().BoolVar(&ignored, "include-ignored", false, "include ignored accessibility nodes")
	return cmd
}

func (a *app) newA11yFindCommand() *cobra.Command {
	var targetID, urlContains, titleContains, role, name string
	var limit int
	cmd := &cobra.Command{Use: "find", Short: "Find accessibility nodes by role and accessible name", RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		nodes, truncated, err := collectA11yNodes(ctx, session, 0, limit, false)
		if err != nil {
			return err
		}
		nodes = filterA11yNodes(nodes, role, name)
		return a.render(ctx, fmt.Sprintf("a11y-find\t%d nodes", len(nodes)), map[string]any{"ok": true, "target": pageRow(target), "nodes": nodes, "truncated": truncated})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&role, "role", "", "accessibility role to match")
	cmd.Flags().StringVar(&name, "name", "", "accessible name substring to match")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum nodes to inspect")
	return cmd
}

func (a *app) newA11yNodeCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	cmd := &cobra.Command{Use: "node <selector>", Short: "Inspect accessibility information for a CSS selector", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		var result map[string]any
		if err := evaluateJSONValue(ctx, session, a11yNodeExpression(args[0]), "a11y node", &result); err != nil {
			return err
		}
		return a.render(ctx, "a11y node", map[string]any{"ok": true, "target": pageRow(target), "node": result})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}
