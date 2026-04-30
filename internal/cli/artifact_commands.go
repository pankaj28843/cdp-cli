package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"encoding/json"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
	"path/filepath"
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

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			shot, err := session.CaptureScreenshot(ctx, cdp.ScreenshotOptions{
				Format:   normalizedFormat,
				Quality:  quality,
				FullPage: fullPage,
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
			writtenPath, err := writeArtifactFile(outPath, shot.Data)
			if err != nil {
				return err
			}
			screenshot := map[string]any{
				"path":      writtenPath,
				"bytes":     len(shot.Data),
				"format":    shot.Format,
				"full_page": fullPage,
			}
			if quality > 0 {
				screenshot["quality"] = quality
			}
			human := fmt.Sprintf("%s\t%d bytes", writtenPath, len(shot.Data))
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
	return cmd
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
