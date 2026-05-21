package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
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
				return commandError("connection_failed", "connection", fmt.Sprintf("handle dialog: %v", err), ExitConnection, []string{"cdp events tap --enable page --match Page.javascriptDialogOpening --json"})
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
	cmd.AddCommand(a.newEmulateUserAgentCommand())
	cmd.AddCommand(a.newEmulateGeolocationCommand())
	cmd.AddCommand(a.newEmulateCPUCommand())
	cmd.AddCommand(a.newEmulateNetworkCommand())
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
			normalizedPreset := strings.ToLower(strings.TrimSpace(preset))
			if preset != "" {
				selected, ok := knownViewportPreset(preset)
				if !ok {
					return commandError("usage", "usage", "unknown viewport preset", ExitUsage, []string{"cdp emulate viewport --preset mobile --json"})
				}
				width, height, dpr, mobile = selected.Width, selected.Height, selected.DeviceScaleFactor, selected.Mobile
				normalizedPreset = selected.Name
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
			return a.render(ctx, fmt.Sprintf("viewport\t%dx%d", width, height), map[string]any{"ok": true, "target": pageRow(target), "emulation": map[string]any{"viewport": params, "preset": normalizedPreset, "cleanup_command": fmt.Sprintf("cdp emulate clear --target %s --json", target.TargetID)}})
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
	cmd := &cobra.Command{Use: "clear", Short: "Clear viewport, media, user-agent, geolocation, CPU, and network emulation", RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		cleared := []string{}
		if err := execSessionJSON(ctx, session, "Emulation.clearDeviceMetricsOverride", map[string]any{}, nil); err == nil {
			cleared = append(cleared, "viewport")
		}
		if err := execSessionJSON(ctx, session, "Emulation.clearGeolocationOverride", map[string]any{}, nil); err == nil {
			cleared = append(cleared, "geolocation")
		}
		if err := execSessionJSON(ctx, session, "Emulation.setEmulatedMedia", map[string]any{}, nil); err == nil {
			cleared = append(cleared, "media")
		}
		if err := execSessionJSON(ctx, session, "Emulation.setUserAgentOverride", map[string]any{"userAgent": ""}, nil); err == nil {
			cleared = append(cleared, "user-agent")
		}
		if err := execSessionJSON(ctx, session, "Emulation.setCPUThrottlingRate", map[string]any{"rate": 1}, nil); err == nil {
			cleared = append(cleared, "cpu")
		}
		if err := execSessionJSON(ctx, session, "Network.emulateNetworkConditions", networkEmulationResetParams(), nil); err == nil {
			cleared = append(cleared, "network")
		}
		return a.render(ctx, "emulation cleared", map[string]any{"ok": true, "target": pageRow(target), "emulation": map[string]any{"cleared": true, "cleared_overrides": cleared}})
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

func (a *app) newEmulateUserAgentCommand() *cobra.Command {
	var targetID, urlContains, titleContains, userAgent, platform string
	cmd := &cobra.Command{Use: "user-agent", Short: "Apply user-agent emulation to a page target", RunE: func(cmd *cobra.Command, args []string) error {
		if userAgent == "" {
			return commandError("usage", "usage", "--user-agent is required", ExitUsage, []string{"cdp emulate user-agent --user-agent 'Mozilla/5.0 ...' --json"})
		}
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		params := map[string]any{"userAgent": userAgent}
		if platform != "" {
			params["platform"] = platform
		}
		if err := execSessionJSON(ctx, session, "Emulation.setUserAgentOverride", params, nil); err != nil {
			return commandError("connection_failed", "connection", fmt.Sprintf("emulate user-agent: %v", err), ExitConnection, []string{"cdp protocol describe Emulation.setUserAgentOverride --json"})
		}
		return a.render(ctx, "user-agent emulation", map[string]any{"ok": true, "target": pageRow(target), "emulation": map[string]any{"user_agent": userAgent, "platform": platform, "cleanup_command": fmt.Sprintf("cdp emulate clear --target %s --json", target.TargetID)}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&userAgent, "user-agent", "", "user-agent string to apply")
	cmd.Flags().StringVar(&platform, "platform", "", "optional navigator platform override")
	return cmd
}

func (a *app) newEmulateGeolocationCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var latitude, longitude, accuracy float64
	cmd := &cobra.Command{Use: "geolocation", Short: "Apply geolocation emulation to a page target", RunE: func(cmd *cobra.Command, args []string) error {
		if latitude < -90 || latitude > 90 {
			return commandError("usage", "usage", "--latitude must be between -90 and 90", ExitUsage, []string{"cdp emulate geolocation --latitude 55.6761 --longitude 12.5683 --json"})
		}
		if longitude < -180 || longitude > 180 {
			return commandError("usage", "usage", "--longitude must be between -180 and 180", ExitUsage, []string{"cdp emulate geolocation --latitude 55.6761 --longitude 12.5683 --json"})
		}
		if accuracy < 0 {
			return commandError("usage", "usage", "--accuracy must be non-negative", ExitUsage, []string{"cdp emulate geolocation --latitude 55.6761 --longitude 12.5683 --accuracy 100 --json"})
		}
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		params := map[string]any{"latitude": latitude, "longitude": longitude, "accuracy": accuracy}
		if err := execSessionJSON(ctx, session, "Emulation.setGeolocationOverride", params, nil); err != nil {
			return commandError("connection_failed", "connection", fmt.Sprintf("emulate geolocation: %v", err), ExitConnection, []string{"cdp protocol describe Emulation.setGeolocationOverride --json"})
		}
		return a.render(ctx, "geolocation emulation", map[string]any{"ok": true, "target": pageRow(target), "emulation": map[string]any{"geolocation": params, "cleanup_command": fmt.Sprintf("cdp emulate clear --target %s --json", target.TargetID)}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().Float64Var(&latitude, "latitude", 0, "latitude to emulate")
	cmd.Flags().Float64Var(&longitude, "longitude", 0, "longitude to emulate")
	cmd.Flags().Float64Var(&accuracy, "accuracy", 100, "geolocation accuracy in meters")
	return cmd
}

func (a *app) newEmulateCPUCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var rate float64
	cmd := &cobra.Command{Use: "cpu", Short: "Apply CPU throttling emulation to a page target", RunE: func(cmd *cobra.Command, args []string) error {
		if rate < 1 {
			return commandError("usage", "usage", "--rate must be >= 1; 1 disables CPU throttling", ExitUsage, []string{"cdp emulate cpu --rate 4 --json", "cdp emulate cpu --rate 1 --json"})
		}
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		params := map[string]any{"rate": rate}
		if err := execSessionJSON(ctx, session, "Emulation.setCPUThrottlingRate", params, nil); err != nil {
			return commandError("connection_failed", "connection", fmt.Sprintf("emulate cpu: %v", err), ExitConnection, []string{"cdp protocol describe Emulation.setCPUThrottlingRate --json"})
		}
		return a.render(ctx, fmt.Sprintf("cpu throttling	%.2fx", rate), map[string]any{"ok": true, "target": pageRow(target), "emulation": map[string]any{"cpu": params, "cleanup_command": fmt.Sprintf("cdp emulate cpu --rate 1 --target %s --json", target.TargetID)}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().Float64Var(&rate, "rate", 4, "CPU slowdown multiplier; use 1 to disable throttling")
	return cmd
}

func (a *app) newEmulateNetworkCommand() *cobra.Command {
	var targetID, urlContains, titleContains, preset string
	var latency int
	var downloadKbps, uploadKbps float64
	var offline bool
	cmd := &cobra.Command{Use: "network", Short: "Apply network throttling emulation to a page target", RunE: func(cmd *cobra.Command, args []string) error {
		params, label, err := networkEmulationParams(preset, offline, latency, downloadKbps, uploadKbps)
		if err != nil {
			return err
		}
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		if err := execSessionJSON(ctx, session, "Network.emulateNetworkConditions", params, nil); err != nil {
			return commandError("connection_failed", "connection", fmt.Sprintf("emulate network: %v", err), ExitConnection, []string{"cdp protocol describe Network.emulateNetworkConditions --json"})
		}
		return a.render(ctx, fmt.Sprintf("network throttling\t%s", label), map[string]any{"ok": true, "target": pageRow(target), "emulation": map[string]any{"network": params, "preset": label, "cleanup_command": fmt.Sprintf("cdp emulate network --preset none --target %s --json", target.TargetID)}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&preset, "preset", "", "network preset: none, offline, slow-3g, fast-3g, wifi")
	cmd.Flags().BoolVar(&offline, "offline", false, "emulate offline network state")
	cmd.Flags().IntVar(&latency, "latency", 0, "round-trip latency in milliseconds")
	cmd.Flags().Float64Var(&downloadKbps, "download-kbps", 0, "download throughput in kilobits per second; 0 disables throttling")
	cmd.Flags().Float64Var(&uploadKbps, "upload-kbps", 0, "upload throughput in kilobits per second; 0 disables throttling")
	return cmd
}

type networkPreset struct {
	Latency      int
	DownloadKbps float64
	UploadKbps   float64
	Offline      bool
}

func networkEmulationParams(preset string, offline bool, latency int, downloadKbps, uploadKbps float64) (map[string]any, string, error) {
	if latency < 0 || downloadKbps < 0 || uploadKbps < 0 {
		return nil, "", commandError("usage", "usage", "--latency, --download-kbps, and --upload-kbps must be non-negative", ExitUsage, []string{"cdp emulate network --preset slow-3g --json", "cdp emulate network --latency 100 --download-kbps 750 --upload-kbps 250 --json"})
	}
	label := strings.TrimSpace(strings.ToLower(preset))
	if label == "" {
		label = "custom"
	}
	presets := map[string]networkPreset{
		"none":    {},
		"offline": {Offline: true},
		"slow-3g": {Latency: 400, DownloadKbps: 400, UploadKbps: 400},
		"fast-3g": {Latency: 150, DownloadKbps: 1600, UploadKbps: 750},
		"wifi":    {Latency: 20, DownloadKbps: 30000, UploadKbps: 15000},
	}
	if presetValues, ok := presets[label]; ok {
		latency = presetValues.Latency
		downloadKbps = presetValues.DownloadKbps
		uploadKbps = presetValues.UploadKbps
		offline = presetValues.Offline
	} else if strings.TrimSpace(preset) != "" {
		return nil, "", commandError("usage", "usage", "unknown network preset", ExitUsage, []string{"cdp emulate network --preset slow-3g --json", "cdp emulate network --preset none --json"})
	}
	return map[string]any{
		"offline":            offline,
		"latency":            latency,
		"downloadThroughput": kbpsToBytesPerSecond(downloadKbps),
		"uploadThroughput":   kbpsToBytesPerSecond(uploadKbps),
	}, label, nil
}

func networkEmulationResetParams() map[string]any {
	return map[string]any{
		"offline":            false,
		"latency":            0,
		"downloadThroughput": 0.0,
		"uploadThroughput":   0.0,
	}
}

func kbpsToBytesPerSecond(kbps float64) float64 {
	return kbps * 1000 / 8
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
