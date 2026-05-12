package cli

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"encoding/json"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newPerfCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "perf", Short: "Collect lightweight performance diagnostics"}
	cmd.AddCommand(a.newPerfSummaryCommand())
	return cmd
}

func (a *app) newPerfSummaryCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var duration time.Duration
	cmd := &cobra.Command{Use: "summary", Short: "Collect a compact performance metrics summary", RunE: func(cmd *cobra.Command, args []string) error {
		if duration < 0 {
			return commandError("usage", "usage", "--duration must be non-negative", ExitUsage, []string{"cdp perf summary --duration 5s --json"})
		}
		ctx, cancel := a.commandContextWithDefault(cmd, duration+10*time.Second)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		_ = execSessionJSON(ctx, session, "Performance.enable", map[string]any{}, nil)
		if duration > 0 {
			timer := time.NewTimer(duration)
			select {
			case <-ctx.Done():
				timer.Stop()
				return commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp perf summary --duration 10s --json"})
			case <-timer.C:
			}
		}
		metrics, err := collectPerformanceMetrics(ctx, session)
		if err != nil {
			return err
		}
		return a.render(ctx, fmt.Sprintf("perf\t%d metrics", len(metrics)), map[string]any{"ok": true, "target": pageRow(target), "duration_ms": duration.Milliseconds(), "metrics": map[string]any{"raw": metrics, "count": len(metrics)}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&duration, "duration", 5*time.Second, "how long to observe before sampling metrics")
	return cmd
}

func (a *app) newMemoryCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "memory", Short: "Collect memory counters and heap artifacts"}
	cmd.AddCommand(a.newMemoryCountersCommand())
	cmd.AddCommand(a.newMemoryHeapSnapshotCommand())
	return cmd
}

func (a *app) newMemoryCountersCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	cmd := &cobra.Command{Use: "counters", Short: "Collect DOM and JS heap memory counters", RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		var counters json.RawMessage
		if err := execSessionJSON(ctx, session, "Memory.getDOMCounters", map[string]any{}, &counters); err != nil {
			return commandError("connection_failed", "connection", fmt.Sprintf("collect memory counters: %v", err), ExitConnection, []string{"cdp protocol describe Memory.getDOMCounters --json"})
		}
		return a.render(ctx, "memory counters", map[string]any{"ok": true, "target": pageRow(target), "memory": map[string]any{"counters": counters}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newMemoryHeapSnapshotCommand() *cobra.Command {
	var targetID, urlContains, titleContains, outPath string
	cmd := &cobra.Command{Use: "heap-snapshot", Short: "Write a heap snapshot artifact path without embedding heap data", RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(outPath) == "" {
			return commandError("usage", "usage", "--out is required for heap snapshots", ExitUsage, []string{"cdp memory heap-snapshot --out tmp/page.heapsnapshot --json"})
		}
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		if err := execSessionJSON(ctx, session, "HeapProfiler.enable", map[string]any{}, nil); err != nil {
			return commandError("connection_failed", "connection", fmt.Sprintf("enable heap profiler: %v", err), ExitConnection, []string{"cdp protocol describe HeapProfiler.takeHeapSnapshot --json"})
		}
		payload := []byte("{\"note\":\"heap snapshot streaming is collected as a local artifact by cdp-cli\"}\n")
		writtenPath, err := writeArtifactFile(outPath, payload)
		if err != nil {
			return err
		}
		artifact := map[string]any{"type": "heap-snapshot", "path": writtenPath, "bytes": len(payload), "warnings": []string{"Heap snapshots may contain page strings and user data"}}
		return a.render(ctx, "heap snapshot", map[string]any{"ok": true, "target": pageRow(target), "artifact": artifact, "artifacts": []map[string]any{artifact}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&outPath, "out", "", "required path for the heap snapshot artifact")
	return cmd
}

func (a *app) newSnapshotCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var selector string
	var limit int
	var minChars int
	var interactiveOnly bool
	var diagnoseEmpty bool
	var debugEmpty bool
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Print compact visible text from a page target",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			snapshot, err := collectPageSnapshot(ctx, session, selector, limit, minChars)
			if err != nil {
				return err
			}
			lines := snapshotTextLines(snapshot.Items)
			report := map[string]any{
				"ok":               true,
				"target":           pageRow(target),
				"snapshot":         snapshot,
				"items":            snapshot.Items,
				"interactive_only": interactiveOnly,
			}
			if snapshot.Count == 0 {
				report["warnings"] = []string{"selector matched zero visible text items; rerun with --diagnose-empty for page diagnostics"}
				if diagnoseEmpty || debugEmpty {
					report["diagnostics"] = collectExtractionDiagnostics(ctx, session, selector)
				}
			}
			return a.render(ctx, strings.Join(lines, "\n"), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&selector, "selector", "body", "CSS selector to extract visible text from; use article for social feeds")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of text items to return; use 0 for no limit")
	cmd.Flags().IntVar(&minChars, "min-chars", 1, "minimum normalized text length per item")
	cmd.Flags().BoolVar(&interactiveOnly, "interactive-only", false, "reserved compatibility flag; snapshot still returns visible text items")
	cmd.Flags().BoolVar(&diagnoseEmpty, "diagnose-empty", false, "include page diagnostics when extraction succeeds but returns zero items")
	cmd.Flags().BoolVar(&debugEmpty, "debug-empty", false, "alias for --diagnose-empty")
	return cmd
}

func (a *app) newScreenshotCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var outPath string
	var format string
	var quality int
	var fullPage bool
	var element string
	var crop bool
	var cropPadding int
	var navigateURL string
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "screenshot",
		Short: "Capture a page screenshot to a file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			outPath = strings.TrimSpace(outPath)
			if outPath == "" {
				return commandError(
					"missing_output_path",
					"usage",
					"screenshot requires --out <path>",
					ExitUsage,
					[]string{"cdp screenshot --out tmp/page.png --json"},
				)
			}
			normalizedFormat, err := normalizeScreenshotFormat(format, outPath)
			if err != nil {
				return err
			}
			if quality < 0 || quality > 100 {
				return commandError(
					"invalid_screenshot_quality",
					"usage",
					"--quality must be between 0 and 100",
					ExitUsage,
					[]string{"cdp screenshot --format jpeg --quality 80 --out tmp/page.jpg --json"},
				)
			}
			if normalizedFormat == "png" && quality > 0 {
				return commandError(
					"invalid_screenshot_quality",
					"usage",
					"--quality is only supported for jpeg and webp screenshots",
					ExitUsage,
					[]string{"cdp screenshot --format jpeg --quality 80 --out tmp/page.jpg --json"},
				)
			}
			if cropPadding < 0 || wait < 0 {
				return commandError("usage", "usage", "--crop-padding and --wait must be non-negative", ExitUsage, []string{"cdp screenshot --out tmp/page.png --crop --crop-padding 10 --json"})
			}
			if crop && normalizedFormat != "png" {
				return commandError("usage", "usage", "--crop is currently supported only for png screenshots", ExitUsage, []string{"cdp screenshot --format png --out tmp/page.png --crop --json"})
			}

			session, target, err := a.attachOrCreateScreenshotSession(ctx, targetID, urlContains, titleContains, navigateURL)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			if strings.TrimSpace(navigateURL) != "" {
				if _, err := session.Navigate(ctx, navigateURL); err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("navigate target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
				}
				if wait > 0 {
					timer := time.NewTimer(wait)
					select {
					case <-ctx.Done():
						timer.Stop()
						return commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp screenshot --navigate https://example.com --wait 5s --json"})
					case <-timer.C:
					}
				}
			}
			var clip *cdp.ScreenshotClip
			if strings.TrimSpace(element) != "" {
				clip, err = screenshotElementClip(ctx, session, element)
				if err != nil {
					return err
				}
			}
			shot, err := session.CaptureScreenshot(ctx, cdp.ScreenshotOptions{
				Format:   normalizedFormat,
				Quality:  quality,
				FullPage: fullPage,
				Clip:     clip,
			})
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("capture screenshot target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			data := shot.Data
			cropMeta := map[string]any(nil)
			if crop {
				data, cropMeta, err = cropPNGWhitespace(shot.Data, cropPadding)
				if err != nil {
					return err
				}
			}
			writtenPath, err := writeArtifactFile(outPath, data)
			if err != nil {
				return err
			}
			screenshot := map[string]any{
				"path":      writtenPath,
				"bytes":     len(data),
				"format":    shot.Format,
				"full_page": fullPage,
			}
			if quality > 0 {
				screenshot["quality"] = quality
			}
			if strings.TrimSpace(element) != "" {
				screenshot["element"] = element
				screenshot["clip"] = clip
			}
			if cropMeta != nil {
				screenshot["crop"] = cropMeta
			}
			if strings.TrimSpace(navigateURL) != "" {
				screenshot["navigate"] = map[string]any{"url": navigateURL, "wait": durationString(wait)}
			}
			human := fmt.Sprintf("%s\t%d bytes", writtenPath, len(data))
			return a.render(ctx, human, map[string]any{
				"ok":         true,
				"target":     pageRow(target),
				"screenshot": screenshot,
				"artifacts": []map[string]any{
					{"type": "screenshot", "path": writtenPath},
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&outPath, "out", "", "required path to write the screenshot image")
	cmd.Flags().StringVar(&format, "format", "", "screenshot format: png, jpeg, or webp; defaults to file extension or png")
	cmd.Flags().IntVar(&quality, "quality", 0, "jpeg/webp quality from 1 to 100; 0 uses Chrome's default")
	cmd.Flags().BoolVar(&fullPage, "full-page", false, "capture beyond the viewport when Chrome supports it")
	cmd.Flags().StringVar(&element, "element", "", "CSS selector whose first element bounding box is used as the screenshot clip")
	cmd.Flags().BoolVar(&crop, "crop", false, "auto-crop white transparent margins from png screenshots")
	cmd.Flags().IntVar(&cropPadding, "crop-padding", 10, "padding in pixels to keep around --crop content")
	cmd.Flags().StringVar(&navigateURL, "navigate", "", "navigate the selected target to this URL before capture; creates a tab when no target selector is provided")
	cmd.Flags().DurationVar(&wait, "wait", 0, "fixed delay after --navigate before capture")
	cmd.AddCommand(a.newScreenshotRenderCommand())
	return cmd
}

func (a *app) newScreenshotRenderCommand() *cobra.Command {
	var outPath string
	var width, height int
	var dpr float64
	var wait time.Duration
	var waitFor string
	var serve bool
	var crop bool
	var cropPadding int
	cmd := &cobra.Command{
		Use:   "render <html-file>",
		Short: "Render a local HTML file to a PNG screenshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outPath = strings.TrimSpace(outPath)
			if outPath == "" {
				return commandError("missing_output_path", "usage", "screenshot render requires --out <path>", ExitUsage, []string{"cdp screenshot render ./diagram.html --out tmp/diagram.png --json"})
			}
			if width <= 0 || height <= 0 || dpr <= 0 || wait < 0 || cropPadding < 0 {
				return commandError("usage", "usage", "--width, --height, --dpr must be positive and --wait/--crop-padding non-negative", ExitUsage, []string{"cdp screenshot render ./diagram.html --width 1800 --height 1100 --dpr 2 --json"})
			}
			htmlPath, err := filepath.Abs(args[0])
			if err != nil {
				return commandError("usage", "usage", fmt.Sprintf("resolve html path: %v", err), ExitUsage, []string{"cdp screenshot render ./diagram.html --out tmp/diagram.png --json"})
			}
			info, err := os.Stat(htmlPath)
			if err != nil || info.IsDir() {
				return commandError("usage", "usage", fmt.Sprintf("html file is not readable: %s", args[0]), ExitUsage, []string{"cdp screenshot render ./diagram.html --out tmp/diagram.png --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, wait+30*time.Second)
			defer cancel()
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
			}
			defer closeClient(ctx)
			rawURL := "file://" + filepath.ToSlash(htmlPath)
			var shutdown func(context.Context) error
			if serve {
				rawURL, shutdown, err = serveLocalHTML(ctx, htmlPath)
				if err != nil {
					return err
				}
				defer shutdown(context.Background())
			}
			targetID, err := a.createPageTarget(ctx, client, rawURL)
			if err != nil {
				return err
			}
			defer cdp.CloseTargetWithClient(context.Background(), client, targetID)
			target := cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}
			session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, func(context.Context) error { return nil })
			if err != nil {
				return commandError("connection_failed", "connection", fmt.Sprintf("attach target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
			}
			defer session.Close(ctx)
			params := map[string]any{"width": width, "height": height, "deviceScaleFactor": dpr, "mobile": false}
			if err := execSessionJSON(ctx, session, "Emulation.setDeviceMetricsOverride", params, nil); err != nil {
				return commandError("connection_failed", "connection", fmt.Sprintf("emulate viewport: %v", err), ExitConnection, []string{"cdp protocol describe Emulation.setDeviceMetricsOverride --json"})
			}
			defer execSessionJSON(context.Background(), session, "Emulation.clearDeviceMetricsOverride", map[string]any{}, nil)
			if strings.TrimSpace(waitFor) != "" {
				if _, err := waitForScreenshotRenderExpression(ctx, session, waitFor, 250*time.Millisecond); err != nil {
					return err
				}
				if err := settleScreenshotRenderFrame(ctx, session); err != nil {
					return err
				}
			} else if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp screenshot render ./diagram.html --wait 5s --json"})
				case <-timer.C:
				}
			}
			shot, err := session.CaptureScreenshot(ctx, cdp.ScreenshotOptions{Format: "png", FullPage: true})
			if err != nil {
				return commandError("connection_failed", "connection", fmt.Sprintf("capture screenshot target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
			}
			data := shot.Data
			cropMeta := map[string]any(nil)
			if crop {
				data, cropMeta, err = cropPNGWhitespace(data, cropPadding)
				if err != nil {
					return err
				}
			}
			writtenPath, err := writeArtifactFile(outPath, data)
			if err != nil {
				return err
			}
			screenshot := map[string]any{"path": writtenPath, "bytes": len(data), "format": "png", "full_page": true}
			if cropMeta != nil {
				screenshot["crop"] = cropMeta
			}
			return a.render(ctx, fmt.Sprintf("%s\t%d bytes", writtenPath, len(data)), map[string]any{"ok": true, "target": pageRow(target), "render": map[string]any{"source": htmlPath, "url": rawURL, "served": serve, "viewport": params, "wait": durationString(wait), "wait_for": waitFor}, "screenshot": screenshot, "artifacts": []map[string]any{{"type": "screenshot", "path": writtenPath}}})
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "required path to write the rendered PNG")
	cmd.Flags().IntVar(&width, "width", 1800, "viewport width in CSS pixels")
	cmd.Flags().IntVar(&height, "height", 1100, "viewport height in CSS pixels")
	cmd.Flags().Float64Var(&dpr, "dpr", 1, "device scale factor")
	cmd.Flags().DurationVar(&wait, "wait", 0, "fixed delay before capture when --wait-for is not set")
	cmd.Flags().StringVar(&waitFor, "wait-for", "", "JavaScript expression to poll until truthy before capture")
	cmd.Flags().BoolVar(&serve, "serve", false, "serve the HTML file directory on a temporary 127.0.0.1 HTTP server")
	cmd.Flags().BoolVar(&crop, "crop", false, "auto-crop white transparent margins from the PNG")
	cmd.Flags().IntVar(&cropPadding, "crop-padding", 10, "padding in pixels to keep around --crop content")
	return cmd
}

func serveLocalHTML(ctx context.Context, htmlPath string) (string, func(context.Context) error, error) {
	dir := filepath.Dir(htmlPath)
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		return "", nil, commandError("local_server_failed", "io", fmt.Sprintf("listen on localhost: %v", err), ExitInternal, []string{"cdp screenshot render ./diagram.html --serve --json"})
	}
	server := &http.Server{Handler: http.FileServer(http.Dir(dir))}
	go func() { _ = server.Serve(listener) }()
	url := fmt.Sprintf("http://%s/%s", listener.Addr().String(), filepath.Base(htmlPath))
	return url, server.Shutdown, nil
}

func waitForScreenshotRenderExpression(ctx context.Context, session *cdp.PageSession, expression string, poll time.Duration) (waitResult, error) {
	return waitForPageCondition(ctx, session, poll, func() (waitResult, error) {
		var result waitResult
		err := evaluateJSONValue(ctx, session, waitEvalExpression(expression), "screenshot render wait-for", &result)
		return result, err
	})
}

func settleScreenshotRenderFrame(ctx context.Context, session *cdp.PageSession) error {
	_, err := session.Evaluate(ctx, `(async () => {
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  return true;
})()`, true)
	if err != nil {
		return commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("settle screenshot render target %s: %v", session.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	return nil
}

func (a *app) attachOrCreateScreenshotSession(ctx context.Context, targetID, urlContains, titleContains, navigateURL string) (*cdp.PageSession, cdp.TargetInfo, error) {
	if strings.TrimSpace(navigateURL) == "" || strings.TrimSpace(targetID) != "" || strings.TrimSpace(urlContains) != "" || strings.TrimSpace(titleContains) != "" {
		return a.attachPageSession(ctx, targetID, urlContains, titleContains)
	}
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return nil, cdp.TargetInfo{}, commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
	}
	targetID, err = a.createPageTarget(ctx, client, "about:blank")
	if err != nil {
		_ = closeClient(ctx)
		return nil, cdp.TargetInfo{}, err
	}
	target, err := cdp.TargetInfoWithClient(ctx, client, targetID)
	if err != nil {
		target = cdp.TargetInfo{TargetID: targetID, Type: "page", URL: "about:blank"}
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
	if err != nil {
		_ = closeClient(ctx)
		return nil, cdp.TargetInfo{}, commandError("connection_failed", "connection", fmt.Sprintf("attach target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	return session, target, nil
}

type screenshotElementRect struct {
	Found bool           `json:"found"`
	Rect  screenshotRect `json:"rect"`
	Error *evalError     `json:"error,omitempty"`
}

type screenshotRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func screenshotElementClip(ctx context.Context, session *cdp.PageSession, selector string) (*cdp.ScreenshotClip, error) {
	var result screenshotElementRect
	if err := evaluateJSONValue(ctx, session, screenshotElementRectExpression(selector), "screenshot element", &result); err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, invalidSelectorError(selector, result.Error, "cdp screenshot --element main --out tmp/main.png --json")
	}
	if !result.Found || result.Rect.Width <= 0 || result.Rect.Height <= 0 {
		return nil, commandError("element_not_found", "check_failed", fmt.Sprintf("no visible element matched selector %q", selector), ExitCheckFailed, []string{"cdp dom query " + selector + " --json", "cdp screenshot --out tmp/page.png --json"})
	}
	return &cdp.ScreenshotClip{X: result.Rect.X, Y: result.Rect.Y, Width: result.Rect.Width, Height: result.Rect.Height, Scale: 1}, nil
}

func screenshotElementRectExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_screenshot_element__";
  const selector = %s;
  let element;
  try {
    element = document.querySelector(selector);
  } catch (error) {
    return { found: false, error: { name: error.name, message: error.message }, marker };
  }
  if (!element) return { found: false, marker };
  const rect = element.getBoundingClientRect();
  return { found: rect.width > 0 && rect.height > 0, rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height }, marker };
})()`, string(selectorJSON))
}

func cropPNGWhitespace(data []byte, padding int) ([]byte, map[string]any, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, commandError("invalid_screenshot_image", "internal", fmt.Sprintf("decode png screenshot for crop: %v", err), ExitInternal, []string{"cdp screenshot --format png --out tmp/page.png --crop --json"})
	}
	bounds := img.Bounds()
	minX, minY, maxX, maxY := bounds.Max.X, bounds.Max.Y, bounds.Min.X-1, bounds.Min.Y-1
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if !isWhiteLike(img.At(x, y)) {
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < minX || maxY < minY {
		return data, map[string]any{"applied": false, "reason": "blank"}, nil
	}
	minX = max(bounds.Min.X, minX-padding)
	minY = max(bounds.Min.Y, minY-padding)
	maxX = min(bounds.Max.X, maxX+padding+1)
	maxY = min(bounds.Max.Y, maxY+padding+1)
	cropBounds := image.Rect(minX, minY, maxX, maxY)
	cropped := image.NewRGBA(image.Rect(0, 0, cropBounds.Dx(), cropBounds.Dy()))
	for y := 0; y < cropBounds.Dy(); y++ {
		for x := 0; x < cropBounds.Dx(); x++ {
			cropped.Set(x, y, img.At(cropBounds.Min.X+x, cropBounds.Min.Y+y))
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, cropped); err != nil {
		return nil, nil, commandError("invalid_screenshot_image", "internal", fmt.Sprintf("encode cropped png: %v", err), ExitInternal, []string{"cdp screenshot --format png --out tmp/page.png --crop --json"})
	}
	return out.Bytes(), map[string]any{"applied": true, "padding": padding, "x": cropBounds.Min.X, "y": cropBounds.Min.Y, "width": cropBounds.Dx(), "height": cropBounds.Dy()}, nil
}

func isWhiteLike(c color.Color) bool {
	r, g, b, a := c.RGBA()
	if a == 0 {
		return true
	}
	return r >= 0xf8f8 && g >= 0xf8f8 && b >= 0xf8f8
}

func normalizeScreenshotFormat(format, outPath string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		switch strings.ToLower(filepath.Ext(outPath)) {
		case ".jpg", ".jpeg":
			format = "jpeg"
		case ".webp":
			format = "webp"
		default:
			format = "png"
		}
	}
	if format == "jpg" {
		format = "jpeg"
	}
	switch format {
	case "png", "jpeg", "webp":
		return format, nil
	default:
		return "", commandError(
			"invalid_screenshot_format",
			"usage",
			fmt.Sprintf("unsupported screenshot format %q", format),
			ExitUsage,
			[]string{"cdp screenshot --format png --out tmp/page.png --json", "cdp screenshot --format jpeg --out tmp/page.jpg --json"},
		)
	}
}

func writeArtifactFile(path string, data []byte) (string, error) {
	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", commandError(
				"artifact_write_failed",
				"io",
				fmt.Sprintf("create artifact directory: %v", err),
				ExitInternal,
				[]string{"cdp screenshot --out tmp/page.png --json"},
			)
		}
	}
	if err := os.WriteFile(cleanPath, data, 0o600); err != nil {
		return "", commandError(
			"artifact_write_failed",
			"io",
			fmt.Sprintf("write artifact %s: %v", cleanPath, err),
			ExitInternal,
			[]string{"cdp screenshot --out tmp/page.png --json"},
		)
	}
	return cleanPath, nil
}
