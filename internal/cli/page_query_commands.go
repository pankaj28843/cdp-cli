package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"encoding/json"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type textItem struct {
	Index      int          `json:"index"`
	Tag        string       `json:"tag"`
	Text       string       `json:"text"`
	TextLength int          `json:"text_length"`
	Rect       snapshotRect `json:"rect"`
}

type htmlResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Items    []htmlItem `json:"items"`
	Error    *evalError `json:"error,omitempty"`
}

type htmlItem struct {
	Index      int    `json:"index"`
	Tag        string `json:"tag"`
	HTML       string `json:"html"`
	HTMLLength int    `json:"html_length"`
	Truncated  bool   `json:"truncated"`
}

type domQueryResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Nodes    []domNode  `json:"nodes"`
	Error    *evalError `json:"error,omitempty"`
}

type domNode struct {
	UID       string       `json:"uid"`
	Index     int          `json:"index"`
	Tag       string       `json:"tag"`
	ID        string       `json:"id_attr,omitempty"`
	Classes   []string     `json:"classes,omitempty"`
	Role      string       `json:"role,omitempty"`
	AriaLabel string       `json:"aria_label,omitempty"`
	Text      string       `json:"text,omitempty"`
	Href      string       `json:"href,omitempty"`
	Rect      snapshotRect `json:"rect"`
}

type cssInspectResult struct {
	URL      string            `json:"url"`
	Title    string            `json:"title"`
	Selector string            `json:"selector"`
	Found    bool              `json:"found"`
	Count    int               `json:"count"`
	Tag      string            `json:"tag,omitempty"`
	Styles   map[string]string `json:"styles,omitempty"`
	Rect     snapshotRect      `json:"rect"`
	Error    *evalError        `json:"error,omitempty"`
}

type layoutOverflowResult struct {
	URL      string               `json:"url"`
	Title    string               `json:"title"`
	Selector string               `json:"selector"`
	Count    int                  `json:"count"`
	Items    []layoutOverflowItem `json:"items"`
	Error    *evalError           `json:"error,omitempty"`
}

type layoutOverflowItem struct {
	UID          string       `json:"uid"`
	Index        int          `json:"index"`
	Tag          string       `json:"tag"`
	Text         string       `json:"text,omitempty"`
	Rect         snapshotRect `json:"rect"`
	ClientWidth  int          `json:"client_width"`
	ScrollWidth  int          `json:"scroll_width"`
	ClientHeight int          `json:"client_height"`
	ScrollHeight int          `json:"scroll_height"`
}

type waitResult struct {
	Kind         string     `json:"kind"`
	Needle       string     `json:"needle,omitempty"`
	Selector     string     `json:"selector,omitempty"`
	Matched      bool       `json:"matched"`
	Count        int        `json:"count,omitempty"`
	ElapsedMS    int64      `json:"elapsed_ms"`
	PollInterval string     `json:"poll_interval"`
	Error        *evalError `json:"error,omitempty"`
}

type evalError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func (a *app) newTextCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var limit int
	var minChars int
	cmd := &cobra.Command{
		Use:   "text <selector>",
		Short: "Extract compact visible text for a CSS selector",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if limit < 0 || minChars < 0 {
				return commandError("usage", "usage", "--limit and --min-chars must be non-negative", ExitUsage, []string{"cdp text main --limit 20 --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result textResult
			if err := evaluateJSONValue(ctx, session, textExpression(args[0], limit, minChars), "text", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp text body --json")
			}
			return a.render(ctx, result.Text, map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"text":   result,
				"items":  result.Items,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum number of text elements to return; use 0 for no limit")
	cmd.Flags().IntVar(&minChars, "min-chars", 1, "minimum normalized text length per item")
	return cmd
}

func (a *app) newHTMLCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var limit int
	var maxChars int
	var diagnoseEmpty bool
	var debugEmpty bool
	cmd := &cobra.Command{
		Use:   "html <selector>",
		Short: "Extract compact HTML for a CSS selector",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if limit < 0 || maxChars < 0 {
				return commandError("usage", "usage", "--limit and --max-chars must be non-negative", ExitUsage, []string{"cdp html main --max-chars 4000 --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result htmlResult
			if err := evaluateJSONValue(ctx, session, htmlExpression(args[0], limit, maxChars), "html", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp html body --json")
			}
			lines := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				lines = append(lines, fmt.Sprintf("%d\t%s", item.Index, item.HTML))
			}
			report := map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"html":   result,
			}
			if result.Count == 0 {
				report["warnings"] = []string{"selector produced zero HTML items; rerun with --diagnose-empty for page diagnostics"}
				if diagnoseEmpty || debugEmpty {
					report["diagnostics"] = collectExtractionDiagnostics(ctx, session, args[0])
				}
			}
			return a.render(ctx, strings.Join(lines, "\n"), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&limit, "limit", 5, "maximum number of elements to return; use 0 for no limit")
	cmd.Flags().IntVar(&maxChars, "max-chars", 4000, "maximum HTML characters per element; use 0 for no truncation")
	cmd.Flags().BoolVar(&diagnoseEmpty, "diagnose-empty", false, "include page diagnostics when extraction succeeds but returns zero items")
	cmd.Flags().BoolVar(&debugEmpty, "debug-empty", false, "alias for --diagnose-empty")
	return cmd
}

func (a *app) newDOMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dom",
		Short: "Inspect DOM nodes",
	}
	cmd.AddCommand(a.newDOMQueryCommand())
	return cmd
}

func (a *app) newDOMQueryCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var limit int
	cmd := &cobra.Command{
		Use:   "query <selector>",
		Short: "Return DOM node summaries for a CSS selector",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if limit < 0 {
				return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp dom query button --limit 20 --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result domQueryResult
			if err := evaluateJSONValue(ctx, session, domQueryExpression(args[0], limit), "dom query", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp dom query button --json")
			}
			lines := make([]string, 0, len(result.Nodes))
			for _, node := range result.Nodes {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s", node.UID, node.Tag, node.Text))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"query":  result,
				"nodes":  result.Nodes,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&limit, "limit", 25, "maximum number of nodes to return; use 0 for no limit")
	return cmd
}

func (a *app) newCSSCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "css",
		Short: "Inspect CSS and layout data",
	}
	cmd.AddCommand(a.newCSSInspectCommand())
	return cmd
}

func (a *app) newCSSInspectCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "inspect <selector>",
		Short: "Return computed style and box data for the first matching element",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result cssInspectResult
			if err := evaluateJSONValue(ctx, session, cssInspectExpression(args[0]), "css inspect", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp css inspect main --json")
			}
			human := "no matching element"
			if result.Found {
				human = fmt.Sprintf("%s\tdisplay=%s\tposition=%s", result.Tag, result.Styles["display"], result.Styles["position"])
			}
			return a.render(ctx, human, map[string]any{
				"ok":      true,
				"target":  pageRow(target),
				"inspect": result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newLayoutCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "layout",
		Short: "Run page layout diagnostics",
	}
	cmd.AddCommand(a.newLayoutOverflowCommand())
	return cmd
}

func (a *app) newLayoutOverflowCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var selector string
	var limit int
	cmd := &cobra.Command{
		Use:   "overflow",
		Short: "Detect elements whose scroll size exceeds their client box",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if limit < 0 {
				return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp layout overflow --limit 20 --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result layoutOverflowResult
			if err := evaluateJSONValue(ctx, session, layoutOverflowExpression(selector, limit), "layout overflow", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(selector, result.Error, "cdp layout overflow --selector 'body *' --json")
			}
			lines := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%d>%d", item.UID, item.Tag, item.ScrollWidth, item.ClientWidth))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"overflow": result,
				"items":    result.Items,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&selector, "selector", "body *", "CSS selector to scan for overflow")
	cmd.Flags().IntVar(&limit, "limit", 25, "maximum number of overflowing elements to return; use 0 for no limit")
	return cmd
}

func (a *app) newWaitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for page conditions",
	}
	cmd.AddCommand(a.newWaitTextCommand())
	cmd.AddCommand(a.newWaitSelectorCommand())
	cmd.AddCommand(a.newWaitLoadCommand())
	cmd.AddCommand(a.newWaitStableCommand())
	cmd.AddCommand(a.newWaitIdleCommand())
	return cmd
}

func (a *app) newWaitLoadCommand() *cobra.Command {
	return planned("load", "Wait for DOMContentLoaded or load lifecycle events")
}

func (a *app) newWaitStableCommand() *cobra.Command {
	return planned("stable", "Wait for a quiet DOM mutation window")
}

func (a *app) newWaitIdleCommand() *cobra.Command {
	return planned("idle", "Wait for network idle with bounded inflight requests")
}

func (a *app) newWaitTextCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "text <needle>",
		Short: "Wait until visible page text contains a string",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp wait text Ready --poll 250ms --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			result, err := waitForPageCondition(ctx, session, poll, func() (waitResult, error) {
				var result waitResult
				err := evaluateJSONValue(ctx, session, waitTextExpression(args[0]), "wait text", &result)
				return result, err
			})
			if err != nil {
				return err
			}
			if result.Error != nil {
				return commandError("javascript_exception", "runtime", result.Error.Message, ExitCheckFailed, []string{"cdp wait text Ready --json"})
			}
			result.ElapsedMS = time.Since(start).Milliseconds()
			result.PollInterval = poll.String()
			return a.render(ctx, fmt.Sprintf("matched text\t%s", args[0]), map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"wait":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while waiting")
	return cmd
}

func (a *app) newWaitSelectorCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "selector <css>",
		Short: "Wait until a CSS selector matches at least one element",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp wait selector main --poll 250ms --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			result, err := waitForPageCondition(ctx, session, poll, func() (waitResult, error) {
				var result waitResult
				err := evaluateJSONValue(ctx, session, waitSelectorExpression(args[0]), "wait selector", &result)
				return result, err
			})
			if err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(args[0], result.Error, "cdp wait selector main --json")
			}
			result.ElapsedMS = time.Since(start).Milliseconds()
			result.PollInterval = poll.String()
			return a.render(ctx, fmt.Sprintf("matched selector\t%s", args[0]), map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"wait":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while waiting")
	return cmd
}

func waitForPageCondition(ctx context.Context, session *cdp.PageSession, poll time.Duration, check func() (waitResult, error)) (waitResult, error) {
	for {
		result, err := check()
		if err != nil {
			return waitResult{}, err
		}
		if result.Matched || result.Error != nil {
			return result, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return waitResult{}, commandError(
				"timeout",
				"timeout",
				fmt.Sprintf("wait condition not met for target %s: %v", session.TargetID, ctx.Err()),
				ExitTimeout,
				[]string{"cdp wait text <needle> --timeout 15s --json", "cdp wait selector <css> --timeout 15s --json"},
			)
		case <-timer.C:
		}
	}
}

func evaluateJSONValue(ctx context.Context, session *cdp.PageSession, expression, label string, out any) error {
	result, err := session.Evaluate(ctx, expression, true)
	if err != nil {
		return commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("%s target %s: %v", label, session.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	if result.Exception != nil {
		return commandError(
			"javascript_exception",
			"runtime",
			fmt.Sprintf("%s javascript exception: %s", label, result.Exception.Text),
			ExitCheckFailed,
			[]string{"cdp eval 'document.title' --json", "cdp pages --json"},
		)
	}
	if err := json.Unmarshal(result.Object.Value, out); err != nil {
		return commandError(
			"invalid_runtime_result",
			"internal",
			fmt.Sprintf("decode %s result: %v", label, err),
			ExitInternal,
			[]string{"cdp doctor --json", "cdp eval 'document.title' --json"},
		)
	}
	return nil
}

func invalidSelectorError(selector string, evalErr *evalError, example string) error {
	return commandError(
		"invalid_selector",
		"usage",
		fmt.Sprintf("invalid selector %q: %s", selector, evalErr.Message),
		ExitUsage,
		[]string{example},
	)
}

type a11yNode struct {
	NodeID   string         `json:"node_id,omitempty"`
	Role     string         `json:"role,omitempty"`
	Name     string         `json:"name,omitempty"`
	Disabled bool           `json:"disabled,omitempty"`
	Ignored  bool           `json:"ignored"`
	Depth    int            `json:"depth"`
	Path     string         `json:"path,omitempty"`
	Raw      map[string]any `json:"raw,omitempty"`
}

func focusExpression(selector string) string {
	return fmt.Sprintf(`(() => { const selector = %s; const el = document.querySelector(selector); if (!el) return {selector, focused:false, error:{name:"NotFoundError", message:"selector matched no elements"}}; el.focus(); return {selector, focused: document.activeElement === el, tag: el.tagName.toLowerCase()}; })()`, jsStringLiteral(selector))
}

func clearExpression(selector string) string {
	return fmt.Sprintf(`(() => { const selector = %s; const el = document.querySelector(selector); if (!el) return {selector, cleared:false, error:{name:"NotFoundError", message:"selector matched no elements"}}; const previous = "value" in el ? String(el.value ?? "") : String(el.textContent ?? ""); if ("value" in el) { el.focus(); el.value = ""; el.dispatchEvent(new Event("input", {bubbles:true})); el.dispatchEvent(new Event("change", {bubbles:true})); return {selector, cleared:true, previous, value:String(el.value ?? "")}; } return {selector, cleared:false, previous, error:{name:"InvalidTargetError", message:"target element does not support direct value assignment"}}; })()`, jsStringLiteral(selector))
}

func selectExpression(selector, value string) string {
	return fmt.Sprintf(`(() => { const selector = %s; const value = String(%s); const el = document.querySelector(selector); if (!el) return {selector, selected:false, error:{name:"NotFoundError", message:"selector matched no elements"}}; if (el.tagName !== "SELECT") return {selector, selected:false, error:{name:"InvalidTargetError", message:"target element is not a select"}}; const previous = String(el.value ?? ""); el.value = value; el.dispatchEvent(new Event("input", {bubbles:true})); el.dispatchEvent(new Event("change", {bubbles:true})); return {selector, selected: el.value === value, previous, value: String(el.value ?? "")}; })()`, jsStringLiteral(selector), jsStringLiteral(value))
}

func fileInputExpression(selector, basename string) string {
	return fmt.Sprintf(`(() => { const selector = %s; const el = document.querySelector(selector); if (!el) return {selector, accepted:false, error:{name:"NotFoundError", message:"selector matched no elements"}}; return {selector, accepted: el.tagName === "INPUT" && el.type === "file", tag: el.tagName.toLowerCase(), type: el.type || "", file_name: %s}; })()`, jsStringLiteral(selector), jsStringLiteral(basename))
}

func a11yNodeExpression(selector string) string {
	return fmt.Sprintf(`(() => { const selector = %s; const el = document.querySelector(selector); if (!el) return {selector, found:false, error:{name:"NotFoundError", message:"selector matched no elements"}}; const label = el.getAttribute("aria-label") || el.getAttribute("alt") || el.innerText || el.textContent || el.value || ""; return {selector, found:true, role: el.getAttribute("role") || el.tagName.toLowerCase(), name: String(label).trim(), disabled: Boolean(el.disabled || el.getAttribute("aria-disabled") === "true"), ignored: false}; })()`, jsStringLiteral(selector))
}

func viewportPreset(name string) (int, int, float64, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "desktop":
		return 1440, 900, 1, false
	case "laptop":
		return 1366, 768, 1, false
	case "tablet":
		return 768, 1024, 1, true
	case "mobile", "iphone-12":
		return 390, 844, 3, true
	default:
		return 0, 0, 0, false
	}
}

func collectA11yNodes(ctx context.Context, session *cdp.PageSession, depth, limit int, includeIgnored bool) ([]a11yNode, bool, error) {
	var raw struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := execSessionJSON(ctx, session, "Accessibility.getFullAXTree", map[string]any{}, &raw); err != nil {
		return nil, false, commandError("connection_failed", "connection", fmt.Sprintf("collect accessibility tree: %v", err), ExitConnection, []string{"cdp protocol describe Accessibility.getFullAXTree --json"})
	}
	nodes := make([]a11yNode, 0, len(raw.Nodes))
	for _, item := range raw.Nodes {
		node := normalizeA11yNode(item)
		if !includeIgnored && node.Ignored {
			continue
		}
		if depth > 0 && node.Depth > depth {
			continue
		}
		nodes = append(nodes, node)
		if limit > 0 && len(nodes) >= limit {
			return nodes, true, nil
		}
	}
	return nodes, false, nil
}

func normalizeA11yNode(raw map[string]any) a11yNode {
	node := a11yNode{Ignored: boolValue(raw["ignored"]), Raw: raw}
	if v, ok := raw["nodeId"].(string); ok {
		node.NodeID = v
	}
	node.Role = axPropString(raw["role"])
	node.Name = axPropString(raw["name"])
	if props, ok := raw["properties"].([]any); ok {
		for _, prop := range props {
			m, ok := prop.(map[string]any)
			if !ok {
				continue
			}
			if m["name"] == "disabled" {
				node.Disabled = boolValue(propValue(m["value"]))
			}
		}
	}
	return node
}

func filterA11yNodes(nodes []a11yNode, role, name string) []a11yNode {
	role = strings.ToLower(strings.TrimSpace(role))
	name = strings.ToLower(strings.TrimSpace(name))
	out := nodes[:0]
	for _, node := range nodes {
		if role != "" && strings.ToLower(node.Role) != role {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(node.Name), name) {
			continue
		}
		out = append(out, node)
	}
	return out
}

func axPropString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if m, ok := v.(map[string]any); ok {
		if pv, ok := propValue(m).(string); ok {
			return pv
		}
	}
	return ""
}

func propValue(v any) any {
	if m, ok := v.(map[string]any); ok {
		if val, ok := m["value"]; ok {
			return val
		}
	}
	return v
}

func boolValue(v any) bool { b, _ := v.(bool); return b }

func clickExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_click__";
  const selector = %s;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, clicked: false, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    return { url: location.href, title: document.title, selector, count: 0, clicked: false, error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  try {
    elements[0].click();
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, clicked: false, error: { name: error.name, message: error.message }, marker };
  }
  return { url: location.href, title: document.title, selector, count: elements.length, clicked: true, marker };
})()`, string(selectorJSON))
}

func rawClickPointExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_click_point__";
  const selector = %s;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, clicked: false, strategy: "raw-input", x: 0, y: 0, rect: { x: 0, y: 0, width: 0, height: 0 }, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    return { url: location.href, title: document.title, selector, count: 0, clicked: false, strategy: "raw-input", x: 0, y: 0, rect: { x: 0, y: 0, width: 0, height: 0 }, error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  const element = elements[0];
  if (typeof element.scrollIntoView === "function") {
    element.scrollIntoView({ block: "center", inline: "center", behavior: "instant" });
  }
  const rect = element.getBoundingClientRect();
  const box = { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  if (rect.width <= 0 || rect.height <= 0) {
    return { url: location.href, title: document.title, selector, count: elements.length, clicked: false, strategy: "raw-input", x: rect.x, y: rect.y, rect: box, error: { name: "InvalidTargetError", message: "target has zero width or height" }, marker };
  }
  const style = window.getComputedStyle(element);
  if (style.visibility === "hidden" || style.display === "none" || Number(style.opacity || "1") === 0) {
    return { url: location.href, title: document.title, selector, count: elements.length, clicked: false, strategy: "raw-input", x: rect.x + rect.width / 2, y: rect.y + rect.height / 2, rect: box, error: { name: "InvalidTargetError", message: "target is not visible" }, marker };
  }
  const x = rect.x + rect.width / 2;
  const y = rect.y + rect.height / 2;
  return { url: location.href, title: document.title, selector, count: elements.length, clicked: true, strategy: "raw-input", x, y, rect: box, marker };
})()`, string(selectorJSON))
}

func collectFrameSummaries(node *frameTreeNode, parent string) []frameSummary {
	if node == nil || node.Frame == nil {
		return nil
	}
	frame := frameSummary{
		FrameID:        node.Frame.ID,
		Name:           node.Frame.Name,
		URL:            node.Frame.URL,
		SecurityOrigin: node.Frame.SecurityOrigin,
		MimeType:       node.Frame.MimeType,
		ParentID:       parent,
		ChildCount:     len(node.ChildFrames),
	}
	out := []frameSummary{frame}
	for idx := range node.ChildFrames {
		out = append(out, collectFrameSummaries(&node.ChildFrames[idx], node.Frame.ID)...)
	}
	return out
}

func fillExpression(selector, value string) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_fill__";
  const selector = %s;
  const value = String(%s);
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, filled: false, previous: "", value: "", error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    return { url: location.href, title: document.title, selector, count: 0, filled: false, previous: "", value: "", error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  const element = elements[0];
  if (!("value" in element)) {
    return { url: location.href, title: document.title, selector, count: 0, filled: false, previous: "", value: "", error: { name: "InvalidTargetError", message: "target element does not support direct value assignment" }, marker };
  }
  const previous = element.value ?? "";
  try {
    element.focus();
    element.value = value;
    element.dispatchEvent(new Event("input", { bubbles: true }));
    element.dispatchEvent(new Event("change", { bubbles: true }));
    return { url: location.href, title: document.title, selector, count: elements.length, filled: true, previous, value: String(element.value), marker };
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, filled: false, previous: String(previous), value: String(element.value ?? ""), error: { name: error.name, message: error.message }, marker };
  }
})()`, jsStringLiteral(selector), jsStringLiteral(value))
}

func typeExpression(selector, text, strategy string) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_type__";
  const selector = %s;
  const text = String(%s);
  const strategy = %s;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, typed: "", previous: "", value: "", kind: "", strategy, typing: false, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    return { url: location.href, title: document.title, selector, count: 0, typed: "", previous: "", value: "", kind: "", strategy, typing: false, error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  const element = elements[0];
  const editable = element.isContentEditable || element.getAttribute("contenteditable") === "true";
  const kind = ("value" in element) ? String(element.tagName || "input").toLowerCase() : (editable ? "contenteditable" : "");
  if (!("value" in element) && !editable) {
    return { url: location.href, title: document.title, selector, count: 0, typed: "", previous: "", value: "", kind, strategy, typing: false, error: { name: "InvalidTargetError", message: "target element is not editable" }, marker };
  }
  const previous = ("value" in element) ? String(element.value ?? "") : String(element.innerText || element.textContent || "");
  const chosen = strategy === "insert-text" || (strategy === "auto" && editable && !("value" in element)) ? "insert-text" : "dom";
  try {
    element.focus();
    if (chosen === "insert-text") {
      return { url: location.href, title: document.title, selector, count: elements.length, typed: text, previous, value: previous, kind, strategy: chosen, typing: true, marker };
    }
    if (!("value" in element)) {
      return { url: location.href, title: document.title, selector, count: 0, typed: "", previous, value: previous, kind, strategy: chosen, typing: false, error: { name: "InvalidTargetError", message: "target element requires insert-text strategy" }, marker };
    }
    let value = previous;
    for (const ch of text) {
      value += ch;
      element.value = value;
      const key = String(ch);
      const keyCode = key.length > 0 ? key.codePointAt(0) : 0;
      const init = { key, code: key.length === 1 ? "Key" + key.toUpperCase() : key, keyCode: keyCode || 0, charCode: keyCode || 0, bubbles: true, cancelable: true };
      element.dispatchEvent(new KeyboardEvent("keydown", init));
      element.dispatchEvent(new KeyboardEvent("keypress", init));
      element.dispatchEvent(new Event("input", { bubbles: true }));
      element.dispatchEvent(new KeyboardEvent("keyup", init));
    }
    return { url: location.href, title: document.title, selector, count: elements.length, typed: text, previous, value: String(element.value ?? ""), kind, strategy: chosen, typing: true, marker };
  } catch (error) {
    const value = ("value" in element) ? String(element.value ?? "") : String(element.innerText || element.textContent || "");
    return { url: location.href, title: document.title, selector, count: 0, typed: "", previous, value, kind, strategy: chosen, typing: false, error: { name: error.name, message: error.message }, marker };
  }
})()`, jsStringLiteral(selector), jsStringLiteral(text), jsStringLiteral(strategy))
}

func insertedTextResultExpression(selector, text, previous, kind string, count int) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_insert_text_result__";
  const selector = %s;
  const text = String(%s);
  const previous = String(%s);
  const kind = %s;
  const count = %d;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, typed: "", previous, value: "", kind, strategy: "insert-text", typing: false, error: { name: error.name, message: error.message }, marker };
  }
  const element = elements[0];
  if (!element) {
    return { url: location.href, title: document.title, selector, count: 0, typed: "", previous, value: "", kind, strategy: "insert-text", typing: false, error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  const value = ("value" in element) ? String(element.value ?? "") : String(element.innerText || element.textContent || "");
  return { url: location.href, title: document.title, selector, count, typed: text, previous, value, kind, strategy: "insert-text", typing: true, marker };
})()`, jsStringLiteral(selector), jsStringLiteral(text), jsStringLiteral(previous), jsStringLiteral(kind), count)
}

func pressExpression(key string, selector string) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_press__";
  const key = String(%s);
  const selector = %s;
  let target;
  if (selector) {
    let elements;
    try {
      elements = Array.from(document.querySelectorAll(selector));
    } catch (error) {
      return { url: location.href, title: document.title, selector, key, count: 0, dispatched: false, error: { name: error.name, message: error.message }, marker };
    }
    if (elements.length === 0) {
      return { url: location.href, title: document.title, selector, key, count: 0, dispatched: false, error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
    }
    target = elements[0];
  } else {
    target = document.activeElement || document.body;
  }
  if (!target) {
    return { url: location.href, title: document.title, selector, key, count: 0, dispatched: false, error: { name: "InvalidTargetError", message: "no focused element to dispatch key events" }, marker };
  }
  const safeKey = key || "Unidentified";
  const keyCode = safeKey.length > 0 ? safeKey.codePointAt(0) : 0;
  const init = {
    key: safeKey,
    code: safeKey.length === 1 ? "Key" + safeKey.toUpperCase() : safeKey,
    keyCode: keyCode || 0,
    charCode: keyCode || 0,
    bubbles: true,
    cancelable: true,
    view: window
  };
  target.focus();
  target.dispatchEvent(new KeyboardEvent("keydown", init));
  target.dispatchEvent(new KeyboardEvent("keypress", init));
  target.dispatchEvent(new KeyboardEvent("keyup", init));
  return { url: location.href, title: document.title, selector, key: safeKey, count: selector ? 1 : 0, dispatched: true, marker };
})()`, jsStringLiteral(key), jsStringLiteral(selector))
}

func hoverExpression(selector string) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_hover__";
  const selector = %s;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, hovered: false, x: 0, y: 0, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    return { url: location.href, title: document.title, selector, count: 0, hovered: false, x: 0, y: 0, error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  const element = elements[0];
  const rect = element.getBoundingClientRect();
  if (rect.width === 0 && rect.height === 0) {
    return { url: location.href, title: document.title, selector, count: elements.length, hovered: false, x: rect.x, y: rect.y, error: { name: "InvalidTargetError", message: "target has zero width and height" }, marker };
  }
  const x = rect.x + rect.width / 2;
  const y = rect.y + rect.height / 2;
  const eventInit = { clientX: x, clientY: y, bubbles: true, cancelable: true, view: window };
  element.dispatchEvent(new MouseEvent("mouseover", eventInit));
  element.dispatchEvent(new MouseEvent("mousemove", eventInit));
  element.dispatchEvent(new MouseEvent("mouseenter", eventInit));
  element.dispatchEvent(new MouseEvent("mouseover", eventInit));
  return { url: location.href, title: document.title, selector, count: elements.length, hovered: true, x, y, marker };
})()`, jsStringLiteral(selector))
}

func dragExpression(selector string, dx, dy int) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_drag__";
  const selector = %s;
  const deltaX = %d;
  const deltaY = %d;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, dragged: false, delta_x: deltaX, delta_y: deltaY, start_x: 0, start_y: 0, end_x: 0, end_y: 0, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    return { url: location.href, title: document.title, selector, count: 0, dragged: false, delta_x: deltaX, delta_y: deltaY, start_x: 0, start_y: 0, end_x: 0, end_y: 0, error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  const element = elements[0];
  const rect = element.getBoundingClientRect();
  const startX = rect.x + rect.width / 2;
  const startY = rect.y + rect.height / 2;
  const endX = startX + deltaX;
  const endY = startY + deltaY;
  element.dispatchEvent(new MouseEvent("mousedown", { clientX: startX, clientY: startY, bubbles: true, cancelable: true, buttons: 1, button: 0, view: window }));
  element.dispatchEvent(new MouseEvent("mousemove", { clientX: endX, clientY: endY, bubbles: true, cancelable: true, buttons: 1, button: 0, view: window }));
  element.dispatchEvent(new MouseEvent("mouseup", { clientX: endX, clientY: endY, bubbles: true, cancelable: true, button: 0, view: window }));
  return { url: location.href, title: document.title, selector, count: elements.length, dragged: true, delta_x: deltaX, delta_y: deltaY, start_x: startX, start_y: startY, end_x: endX, end_y: endY, marker };
})()`, jsStringLiteral(selector), dx, dy)
}

func textExpression(selector string, limit, minChars int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_text__";
  const selector = %s;
  const limit = %d;
  const minChars = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const rectFor = (element) => {
    const rect = element.getBoundingClientRect();
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, text: "", items: [], error: { name: error.name, message: error.message } };
  }
  const items = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    const text = normalize(element.innerText || element.textContent);
    if (text.length < minChars) continue;
    items.push({ index, tag: element.tagName.toLowerCase(), text, text_length: text.length, rect: rectFor(element) });
    if (limit > 0 && items.length >= limit) break;
  }
  return { url: location.href, title: document.title, selector, count: items.length, text: items.map((item) => item.text).join("\n"), items, marker };
})()`, string(selectorJSON), limit, minChars)
}

func htmlExpression(selector string, limit, maxChars int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_html__";
  const selector = %s;
  const limit = %d;
  const maxChars = %d;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, items: [], error: { name: error.name, message: error.message } };
  }
  const items = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    const full = element.outerHTML || "";
    const truncated = maxChars > 0 && full.length > maxChars;
    const html = truncated ? full.slice(0, maxChars) : full;
    items.push({ index, tag: element.tagName.toLowerCase(), html, html_length: full.length, truncated });
    if (limit > 0 && items.length >= limit) break;
  }
  return { url: location.href, title: document.title, selector, count: items.length, items, marker };
})()`, string(selectorJSON), limit, maxChars)
}

func domQueryExpression(selector string, limit int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_dom_query__";
  const selector = %s;
  const limit = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const rectFor = (element) => {
    const rect = element.getBoundingClientRect();
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, nodes: [], error: { name: error.name, message: error.message } };
  }
  const nodes = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    nodes.push({
      uid: "css:" + selector + ":" + index,
      index,
      tag: element.tagName.toLowerCase(),
      id_attr: element.id || "",
      classes: Array.from(element.classList || []),
      role: element.getAttribute("role") || "",
      aria_label: element.getAttribute("aria-label") || "",
      text: normalize(element.innerText || element.textContent).slice(0, 500),
      href: element.href || "",
      rect: rectFor(element)
    });
    if (limit > 0 && nodes.length >= limit) break;
  }
  return { url: location.href, title: document.title, selector, count: nodes.length, nodes, marker };
})()`, string(selectorJSON), limit)
}

func cssInspectExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_css_inspect__";
  const selector = %s;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, found: false, count: 0, error: { name: error.name, message: error.message } };
  }
  const element = elements[0];
  if (!element) return { url: location.href, title: document.title, selector, found: false, count: 0, marker };
  const style = window.getComputedStyle(element);
  const rect = element.getBoundingClientRect();
  const pick = ["display", "position", "overflow", "overflowX", "overflowY", "color", "backgroundColor", "fontSize", "fontWeight", "lineHeight", "zIndex"];
  const styles = {};
  for (const key of pick) styles[key] = style[key] || "";
  return {
    url: location.href,
    title: document.title,
    selector,
    found: true,
    count: elements.length,
    tag: element.tagName.toLowerCase(),
    styles,
    rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
    marker
  };
})()`, string(selectorJSON))
}

func layoutOverflowExpression(selector string, limit int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_layout_overflow__";
  const selector = %s;
  const limit = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const rectFor = (element) => {
    const rect = element.getBoundingClientRect();
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, items: [], error: { name: error.name, message: error.message } };
  }
  const items = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    if (element.scrollWidth <= element.clientWidth && element.scrollHeight <= element.clientHeight) continue;
    items.push({
      uid: "overflow:" + index,
      index,
      tag: element.tagName.toLowerCase(),
      text: normalize(element.innerText || element.textContent).slice(0, 240),
      rect: rectFor(element),
      client_width: element.clientWidth,
      scroll_width: element.scrollWidth,
      client_height: element.clientHeight,
      scroll_height: element.scrollHeight
    });
    if (limit > 0 && items.length >= limit) break;
  }
  return { url: location.href, title: document.title, selector, count: items.length, items, marker };
})()`, string(selectorJSON), limit)
}

func waitTextExpression(needle string) string {
	needleJSON, _ := json.Marshal(needle)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_wait_text__";
  const needle = %s;
  const text = (document.body && (document.body.innerText || document.body.textContent) || "");
  return { kind: "text", needle, matched: text.includes(needle), count: text.includes(needle) ? 1 : 0, marker };
})()`, string(needleJSON))
}

func waitSelectorExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_wait_selector__";
  const selector = %s;
  try {
    const count = document.querySelectorAll(selector).length;
    return { kind: "selector", selector, matched: count > 0, count, marker };
  } catch (error) {
    return { kind: "selector", selector, matched: false, count: 0, error: { name: error.name, message: error.message }, marker };
  }
})()`, string(selectorJSON))
}
