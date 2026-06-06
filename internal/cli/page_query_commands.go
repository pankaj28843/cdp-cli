package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

type observeResult struct {
	URL         string        `json:"url"`
	Title       string        `json:"title"`
	Selector    string        `json:"selector"`
	Count       int           `json:"count"`
	Interactive []observeNode `json:"interactive"`
	Warnings    []string      `json:"warnings,omitempty"`
	Error       *evalError    `json:"error,omitempty"`
}

type observeNode struct {
	Ref      string       `json:"ref"`
	Index    int          `json:"index"`
	Tag      string       `json:"tag"`
	Role     string       `json:"role,omitempty"`
	Name     string       `json:"name,omitempty"`
	Selector string       `json:"selector"`
	Text     string       `json:"text,omitempty"`
	Href     string       `json:"href,omitempty"`
	Disabled bool         `json:"disabled"`
	Visible  bool         `json:"visible"`
	Rect     snapshotRect `json:"rect"`
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
	Kind         string             `json:"kind"`
	Needle       string             `json:"needle,omitempty"`
	Selector     string             `json:"selector,omitempty"`
	Expression   string             `json:"expression,omitempty"`
	State        string             `json:"state,omitempty"`
	ReadyState   string             `json:"ready_state,omitempty"`
	URL          string             `json:"url,omitempty"`
	Title        string             `json:"title,omitempty"`
	By           string             `json:"by,omitempty"`
	Query        string             `json:"query,omitempty"`
	Role         string             `json:"role,omitempty"`
	Strict       bool               `json:"strict,omitempty"`
	Resolved     string             `json:"resolved_selector,omitempty"`
	Matched      bool               `json:"matched"`
	Count        int                `json:"count,omitempty"`
	Value        json.RawMessage    `json:"value,omitempty"`
	Condition    string             `json:"condition,omitempty"`
	Evidence     map[string]any     `json:"evidence,omitempty"`
	Locator      *locatorFindResult `json:"locator,omitempty"`
	ElapsedMS    int64              `json:"elapsed_ms"`
	PollInterval string             `json:"poll_interval"`
	Error        *evalError         `json:"error,omitempty"`
}

type locatorWaitOptions struct {
	By            string
	Role          string
	TestIDAttr    string
	Exact         bool
	IncludeHidden bool
	Strict        bool
	Limit         int
}

type evalError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func (a *app) newObserveCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var selector string
	var limit int
	cmd := &cobra.Command{
		Use:   "observe",
		Short: "Summarize visible interactive elements for agent planning",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if limit < 0 {
				return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp observe --limit 30 --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result observeResult
			if err := evaluateJSONValue(ctx, session, observeExpression(selector, limit), "observe", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(selector, result.Error, "cdp observe --selector 'button, a' --json")
			}
			lines := make([]string, 0, len(result.Interactive))
			for _, node := range result.Interactive {
				label := firstNonEmpty(node.Name, node.Text, node.Href, node.Selector)
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", node.Ref, node.Role, node.Selector, label))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":          true,
				"target":      pageRow(target),
				"observe":     result,
				"interactive": result.Interactive,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&selector, "selector", "a[href], button, input, textarea, select, [role=button], [role=link], [role=menuitem], [contenteditable=true]", "CSS selector for candidate interactive elements")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum interactive elements to return; use 0 for no limit")
	return cmd
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
		Short: "Wait for page or network conditions",
	}
	cmd.AddCommand(a.newWaitTextCommand())
	cmd.AddCommand(a.newWaitSelectorCommand())
	cmd.AddCommand(a.newWaitURLCommand())
	cmd.AddCommand(a.newWaitLocatorCommand())
	cmd.AddCommand(a.newWaitEvalCommand())
	cmd.AddCommand(a.newWaitLoadStateCommand())
	cmd.AddCommand(a.newWaitRequestCommand())
	cmd.AddCommand(a.newWaitResponseCommand())
	cmd.AddCommand(a.newWaitNetworkIdleCommand())
	cmd.AddCommand(a.newWaitDialogCommand())
	cmd.AddCommand(a.newWaitFileChooserCommand())
	cmd.AddCommand(a.newWaitPopupCommand())
	cmd.AddCommand(a.newWaitDownloadCommand())
	return cmd
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

func (a *app) newWaitURLCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var mode string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "url <expected>",
		Short: "Wait until the page URL matches",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			expected := strings.TrimSpace(args[0])
			if expected == "" {
				return commandError("usage", "usage", "expected URL must not be empty", ExitUsage, []string{"cdp wait url /results --mode contains --json"})
			}
			mode = strings.ToLower(strings.TrimSpace(mode))
			if mode != "exact" && mode != "contains" {
				return commandError("usage", "usage", "--mode must be exact or contains", ExitUsage, []string{"cdp wait url /results --mode contains --json", "cdp wait url https://example.com/checkout --mode exact --json"})
			}
			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp wait url /results --mode contains --poll 250ms --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			result, err := waitForPageCondition(ctx, session, poll, func() (waitResult, error) {
				var result waitResult
				err := evaluateJSONValue(ctx, session, waitURLExpression(expected, mode == "contains"), "wait url", &result)
				return result, err
			})
			if err != nil {
				return err
			}
			if result.Error != nil {
				return commandError("javascript_exception", "runtime", result.Error.Message, ExitCheckFailed, []string{"cdp wait url /results --mode contains --json"})
			}
			result.ElapsedMS = time.Since(start).Milliseconds()
			result.PollInterval = poll.String()
			if strings.TrimSpace(result.URL) != "" {
				target.URL = result.URL
			}
			if strings.TrimSpace(result.Title) != "" {
				target.Title = result.Title
			}
			return a.render(ctx, fmt.Sprintf("matched url\t%s", expected), map[string]any{
				"ok":     true,
				"target": pageRow(target),
				"wait":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "contains", "URL match mode: exact or contains")
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

func (a *app) newWaitLocatorCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var poll time.Duration
	var locatorOpts locatorWaitOptions
	cmd := &cobra.Command{
		Use:   "locator <query>",
		Short: "Wait until a user-facing locator matches",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp wait locator Ready --by text --poll 250ms --json"})
			}
			if err := normalizeLocatorWaitOptions(&locatorOpts); err != nil {
				return err
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			result, locator, err := waitForLocatorCondition(ctx, session, poll, args[0], locatorOpts)
			result.ElapsedMS = time.Since(start).Milliseconds()
			result.PollInterval = poll.String()
			report := map[string]any{
				"ok":     err == nil && result.Error == nil,
				"target": pageRow(target),
				"wait":   result,
			}
			if locator != nil {
				report["locator"] = locator
				report["matches"] = locator.Matches
			}
			if err != nil {
				if ctx.Err() == nil {
					return err
				}
				return commandErrorWithData("timeout", "timeout", fmt.Sprintf("wait locator %s %q not matched for target %s: %v", locatorOpts.By, args[0], session.TargetID, ctx.Err()), ExitTimeout, locatorWaitRemediations(args[0], locatorOpts), report)
			}
			if result.Error != nil {
				return commandErrorWithData("invalid_locator", "usage", fmt.Sprintf("wait locator %s %q: %s", locatorOpts.By, args[0], result.Error.Message), ExitUsage, locatorWaitRemediations(args[0], locatorOpts), report)
			}
			return a.render(ctx, fmt.Sprintf("matched locator\t%s\t%s", locatorOpts.By, args[0]), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while waiting")
	addLocatorWaitFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newWaitEvalCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "eval <expression>",
		Short: "Wait until a JavaScript expression evaluates truthy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp wait eval 'window.__rendered === true' --poll 250ms --json"})
			}
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			result, err := waitForPageCondition(ctx, session, poll, func() (waitResult, error) {
				var result waitResult
				err := evaluateJSONValue(ctx, session, waitEvalExpression(args[0]), "wait eval", &result)
				return result, err
			})
			if err != nil {
				return err
			}
			if result.Error != nil {
				return commandError("javascript_exception", "runtime", result.Error.Message, ExitCheckFailed, []string{"cdp wait eval 'window.__rendered === true' --json"})
			}
			result.ElapsedMS = time.Since(start).Milliseconds()
			result.PollInterval = poll.String()
			return a.render(ctx, fmt.Sprintf("matched eval\t%s", args[0]), map[string]any{
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

func (a *app) newWaitLoadStateCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "load-state <load|domcontentloaded>",
		Short: "Wait until the document reaches a browser load state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := normalizeLoadState(args[0])
			if err != nil {
				return err
			}
			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp wait load-state load --poll 250ms --json"})
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			result, err := waitForLoadStateCondition(ctx, session, poll, state)
			result.ElapsedMS = time.Since(start).Milliseconds()
			result.PollInterval = poll.String()
			report := map[string]any{
				"ok":     err == nil && result.Error == nil,
				"target": pageRow(target),
				"wait":   result,
			}
			if err != nil {
				if ctx.Err() == nil && exitCode(err) != ExitTimeout {
					return err
				}
				cause := err
				if ctx.Err() != nil {
					cause = ctx.Err()
				}
				return commandErrorWithData("timeout", "timeout", fmt.Sprintf("wait load-state %q not reached for target %s: %v", state, session.TargetID, cause), ExitTimeout, loadStateWaitRemediations(state), report)
			}
			if result.Error != nil {
				return commandErrorWithData("javascript_exception", "runtime", result.Error.Message, ExitCheckFailed, loadStateWaitRemediations(state), report)
			}
			return a.render(ctx, fmt.Sprintf("matched load-state\t%s\t%s", state, result.ReadyState), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while waiting")
	return cmd
}

func addLocatorWaitFlags(cmd *cobra.Command, opts *locatorWaitOptions) {
	cmd.Flags().StringVar(&opts.By, "by", "text", "locator strategy: role, text, label, placeholder, alt, title, test-id, or css")
	cmd.Flags().StringVar(&opts.Role, "role", "", "ARIA role to match when --by role is used")
	cmd.Flags().StringVar(&opts.TestIDAttr, "test-id-attr", "data-testid", "attribute name for --by test-id")
	cmd.Flags().BoolVar(&opts.Exact, "exact", false, "require exact normalized text/name/attribute match")
	cmd.Flags().BoolVar(&opts.IncludeHidden, "include-hidden", false, "include hidden locator matches")
	cmd.Flags().BoolVar(&opts.Strict, "strict", false, "require exactly one locator match before succeeding")
	cmd.Flags().IntVar(&opts.Limit, "limit", 20, "maximum locator matches to return")
}

func normalizeLocatorWaitOptions(opts *locatorWaitOptions) error {
	opts.By = normalizeLocatorStrategy(opts.By)
	opts.Role = strings.TrimSpace(opts.Role)
	opts.TestIDAttr = strings.TrimSpace(opts.TestIDAttr)
	return validateLocatorFindOptions(opts.By, opts.Role, opts.TestIDAttr, opts.Limit)
}

func waitForLocatorCondition(ctx context.Context, session *cdp.PageSession, poll time.Duration, query string, opts locatorWaitOptions) (waitResult, *locatorFindResult, error) {
	var last waitResult
	var lastLocator *locatorFindResult
	for {
		var locator locatorFindResult
		if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "wait locator", &locator); err != nil {
			return last, lastLocator, err
		}
		result := waitResultFromLocator(query, opts, locator)
		last = result
		lastLocator = &locator
		if result.Matched || result.Error != nil {
			result.addEvidence()
			return result, &locator, nil
		}

		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			last.Condition = locatorWaitCondition(query, opts)
			last.Evidence = locatorWaitEvidence(last, lastLocator)
			return last, lastLocator, ctx.Err()
		case <-timer.C:
		}
	}
}

func waitForLoadStateCondition(ctx context.Context, session *cdp.PageSession, poll time.Duration, state string) (waitResult, error) {
	var last waitResult
	for {
		var result waitResult
		if err := evaluateJSONValue(ctx, session, waitLoadStateExpression(state), "wait load-state", &result); err != nil {
			if ctx.Err() != nil || exitCode(err) == ExitTimeout {
				return decorateLoadStateTimeoutResult(last, state), err
			}
			return last, err
		}
		last = result
		if result.Matched || result.Error != nil {
			result.addEvidence()
			return result, nil
		}

		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return decorateLoadStateTimeoutResult(last, state), ctx.Err()
		case <-timer.C:
		}
	}
}

func decorateLoadStateTimeoutResult(result waitResult, state string) waitResult {
	if result.Kind == "" {
		result.Kind = "load-state"
	}
	if result.State == "" {
		result.State = state
	}
	result.Condition = loadStateWaitCondition(state)
	result.Evidence = loadStateWaitEvidence(result)
	return result
}

func normalizeLoadState(raw string) (string, error) {
	state := strings.ToLower(strings.TrimSpace(raw))
	state = strings.ReplaceAll(state, "-", "")
	switch state {
	case "load":
		return "load", nil
	case "domcontentloaded":
		return "domcontentloaded", nil
	default:
		return "", commandError("usage", "usage", "load-state must be load or domcontentloaded", ExitUsage, []string{"cdp wait load-state load --json", "cdp wait load-state domcontentloaded --json"})
	}
}

func loadStateWaitCondition(state string) string {
	switch state {
	case "domcontentloaded":
		return "document readyState is interactive or complete"
	default:
		return "document readyState is complete"
	}
}

func loadStateWaitEvidence(result waitResult) map[string]any {
	return map[string]any{
		"state":       result.State,
		"ready_state": result.ReadyState,
		"matched":     result.Matched,
	}
}

func loadStateWaitRemediations(state string) []string {
	return []string{
		"cdp wait load-state " + state + " --timeout 15s --json",
		"cdp wait selector main --timeout 15s --json",
		"cdp wait eval 'document.readyState === \"complete\"' --timeout 15s --json",
	}
}

func waitResultFromLocator(query string, opts locatorWaitOptions, locator locatorFindResult) waitResult {
	matched := locator.Count > 0
	if opts.Strict {
		matched = locator.Count == 1
	}
	resolved := ""
	if len(locator.Matches) == 1 && strings.TrimSpace(locator.Matches[0].SelectorHint) != "" && !locator.Matches[0].SelectorAmbiguous {
		resolved = locator.Matches[0].SelectorHint
	}
	return waitResult{
		Kind:     "locator",
		By:       locator.By,
		Query:    locator.Query,
		Role:     locator.Role,
		Strict:   opts.Strict,
		Resolved: resolved,
		Matched:  matched,
		Count:    locator.Count,
		Locator:  &locator,
		Error:    locator.Error,
	}
}

func locatorWaitCondition(query string, opts locatorWaitOptions) string {
	if opts.Strict {
		return fmt.Sprintf("locator %s %q matched exactly one element", opts.By, query)
	}
	return fmt.Sprintf("locator %s %q matched at least one element", opts.By, query)
}

func locatorWaitEvidence(result waitResult, locator *locatorFindResult) map[string]any {
	evidence := map[string]any{
		"by":       result.By,
		"query":    result.Query,
		"matched":  result.Matched,
		"count":    result.Count,
		"strict":   result.Strict,
		"returned": 0,
	}
	if strings.TrimSpace(result.Role) != "" {
		evidence["role"] = result.Role
	}
	if strings.TrimSpace(result.Resolved) != "" {
		evidence["resolved_selector"] = result.Resolved
	}
	if locator != nil {
		evidence["returned"] = locator.Returned
	}
	return evidence
}

func locatorWaitRemediations(query string, opts locatorWaitOptions) []string {
	waitCommand := "cdp wait locator " + shellQuote(query) + " --by " + opts.By
	findCommand := "cdp locator find " + shellQuote(query) + " --by " + opts.By
	if opts.By == "role" {
		role := shellQuote(opts.Role)
		waitCommand += " --role " + role
		findCommand += " --role " + role
	}
	if opts.Exact {
		waitCommand += " --exact"
		findCommand += " --exact"
	}
	if opts.IncludeHidden {
		waitCommand += " --include-hidden"
		findCommand += " --include-hidden"
	}
	if opts.Strict {
		waitCommand += " --strict"
	}
	if opts.By == "test-id" && opts.TestIDAttr != "data-testid" {
		attr := shellQuote(opts.TestIDAttr)
		waitCommand += " --test-id-attr " + attr
		findCommand += " --test-id-attr " + attr
	}
	return []string{waitCommand + " --timeout 15s --json", findCommand + " --json"}
}

func (r *waitResult) addEvidence() {
	if !r.Matched {
		return
	}
	switch r.Kind {
	case "text":
		r.Condition = fmt.Sprintf("visible text contains %q", r.Needle)
		r.Evidence = map[string]any{"needle": r.Needle, "matched": true, "count": r.Count}
	case "selector":
		r.Condition = fmt.Sprintf("selector %q matched at least one element", r.Selector)
		r.Evidence = map[string]any{"selector": r.Selector, "matched": true, "count": r.Count}
	case "url":
		r.Evidence = map[string]any{"needle": r.Needle, "condition": r.Condition, "url": r.URL, "title": r.Title, "matched": true, "count": r.Count}
	case "eval":
		r.Condition = fmt.Sprintf("expression %q evaluated truthy", r.Expression)
		r.Evidence = map[string]any{"expression": r.Expression, "matched": true, "value": r.Value}
	case "locator":
		r.Condition = locatorWaitCondition(r.Query, locatorWaitOptions{By: r.By, Strict: r.Strict})
		r.Evidence = locatorWaitEvidence(*r, r.Locator)
	case "load-state":
		r.Condition = loadStateWaitCondition(r.State)
		r.Evidence = loadStateWaitEvidence(*r)
	}
}

func waitForPageCondition(ctx context.Context, session *cdp.PageSession, poll time.Duration, check func() (waitResult, error)) (waitResult, error) {
	for {
		result, err := check()
		if err != nil {
			return waitResult{}, err
		}
		if result.Matched || result.Error != nil {
			result.addEvidence()
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
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return commandError(
				"timeout",
				"timeout",
				fmt.Sprintf("%s target %s: %v", label, session.TargetID, context.DeadlineExceeded),
				ExitTimeout,
				[]string{"cdp wait eval 'window.__rendered === true' --timeout 15s --json"},
			)
		}
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
	Value    string         `json:"value,omitempty"`
	Disabled bool           `json:"disabled,omitempty"`
	Ignored  bool           `json:"ignored"`
	Depth    int            `json:"depth"`
	Path     string         `json:"path,omitempty"`
	ChildIDs []string       `json:"child_ids,omitempty"`
	Raw      map[string]any `json:"raw,omitempty"`
}

type a11ySnapshotResult struct {
	URL            string   `json:"url,omitempty"`
	Title          string   `json:"title,omitempty"`
	Selector       string   `json:"selector"`
	LineCount      int      `json:"line_count"`
	Lines          []string `json:"lines"`
	Text           string   `json:"text"`
	Truncated      bool     `json:"truncated"`
	Depth          int      `json:"depth"`
	Limit          int      `json:"limit"`
	IncludeIgnored bool     `json:"include_ignored,omitempty"`
	Source         string   `json:"source"`
}

func focusExpression(selector string) string {
	return fmt.Sprintf(`(() => { const selector = %s; const el = document.querySelector(selector); if (!el) return {selector, focused:false, error:{name:"NotFoundError", message:"selector matched no elements"}}; el.focus(); return {selector, focused: document.activeElement === el, tag: el.tagName.toLowerCase()}; })()`, jsStringLiteral(selector))
}

func clearExpression(selector string) string {
	return fmt.Sprintf(`(() => { const selector = %s; const el = document.querySelector(selector); if (!el) return {selector, cleared:false, error:{name:"NotFoundError", message:"selector matched no elements"}}; const previous = "value" in el ? String(el.value ?? "") : String(el.textContent ?? ""); if ("value" in el) { el.focus(); el.value = ""; el.dispatchEvent(new Event("input", {bubbles:true})); el.dispatchEvent(new Event("change", {bubbles:true})); return {selector, cleared:true, previous, value:String(el.value ?? "")}; } return {selector, cleared:false, previous, error:{name:"InvalidTargetError", message:"target element does not support direct value assignment"}}; })()`, jsStringLiteral(selector))
}

func selectExpression(selector, value string) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_select__";
  const selector = %s;
  const requestedValue = String(%s);
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, selected: false, requested_value: requestedValue, value: "", error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    return { url: location.href, title: document.title, selector, count: 0, selected: false, requested_value: requestedValue, value: "", error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  const el = elements[0];
  if (el.tagName !== "SELECT") {
    return { url: location.href, title: document.title, selector, count: elements.length, selected: false, requested_value: requestedValue, value: "", error: { name: "InvalidTargetError", message: "target element is not a select" }, marker };
  }
  const previous = String(el.value ?? "");
  const options = Array.from(el.options || []);
  let matchedBy = "value";
  let option = options.find((candidate) => String(candidate.value) === requestedValue);
  if (!option) {
    matchedBy = "label";
    option = options.find((candidate) => String(candidate.label || candidate.textContent || "").trim() === requestedValue);
  }
  if (!option) {
    return { url: location.href, title: document.title, selector, count: elements.length, selected: false, previous, requested_value: requestedValue, value: String(el.value ?? ""), selected_values: Array.from(el.selectedOptions || []).map((selected) => String(selected.value)), error: { name: "OptionNotFoundError", message: "option value or label matched no options" }, marker };
  }
  el.value = String(option.value);
  el.dispatchEvent(new Event("input", { bubbles: true }));
  el.dispatchEvent(new Event("change", { bubbles: true }));
  const selectedValues = Array.from(el.selectedOptions || []).map((selected) => String(selected.value));
  return { url: location.href, title: document.title, selector, count: elements.length, selected: el.value === String(option.value), previous, requested_value: requestedValue, value: String(el.value ?? ""), selected_values: selectedValues, matched_by: matchedBy, option: { value: String(option.value), label: String(option.label || option.textContent || "").trim(), index: option.index }, marker };
})()`, jsStringLiteral(selector), jsStringLiteral(value))
}

func checkExpression(selector string, desired bool, mutate bool) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_check__";
  const selector = %s;
  const desiredChecked = %t;
  const mutate = %t;
  const norm = (value) => String(value || "").replace(/\s+/g, " ").trim();
  const roleOf = (el) => {
    const explicit = norm(el.getAttribute("role")).split(" ")[0];
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    const type = String(el.getAttribute("type") || "").toLowerCase();
    if (tag === "input" && type === "checkbox") return "checkbox";
    if (tag === "input" && type === "radio") return "radio";
    return "";
  };
  const nameOf = (el) => {
    const labelled = el.getAttribute("aria-label") || el.getAttribute("alt") || el.getAttribute("title") || el.getAttribute("placeholder") || "";
    if (labelled) return norm(labelled);
    if (el.id && window.CSS && CSS.escape) {
      const label = document.querySelector('label[for="' + CSS.escape(el.id) + '"]');
      if (label) return norm(label.innerText || label.textContent);
    }
    const parent = el.closest("label");
    return parent ? norm(parent.innerText || parent.textContent) : norm(el.innerText || el.textContent || el.value || "");
  };
  const stateOf = (el) => {
    const tag = el.tagName.toLowerCase();
    const type = String(el.getAttribute("type") || "").toLowerCase();
    const role = roleOf(el);
    const native = tag === "input" && (type === "checkbox" || type === "radio");
    const aria = !native && (role === "checkbox" || role === "switch" || role === "radio");
    if (!native && !aria) {
      return { valid: false, tag, type, role, checked: false };
    }
    const checked = native ? Boolean(el.checked) : String(el.getAttribute("aria-checked") || "").toLowerCase() === "true";
    return { valid: true, native, aria, tag, type, role, checked };
  };
  const setNativeChecked = (el, checked) => {
    const proto = Object.getPrototypeOf(el);
    const descriptor = proto && Object.getOwnPropertyDescriptor(proto, "checked");
    if (descriptor && typeof descriptor.set === "function") descriptor.set.call(el, checked);
    else el.checked = checked;
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, checked: false, desired_checked: desiredChecked, previous_checked: false, changed: false, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    return { url: location.href, title: document.title, selector, count: 0, checked: false, desired_checked: desiredChecked, previous_checked: false, changed: false, error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  const el = elements[0];
  const before = stateOf(el);
  if (!before.valid) {
    return { url: location.href, title: document.title, selector, count: elements.length, checked: false, desired_checked: desiredChecked, previous_checked: false, changed: false, tag: before.tag, type: before.type, role: before.role, error: { name: "InvalidTargetError", message: "target element is not a checkbox or radio control" }, marker };
  }
  const previous = before.checked;
  try {
    if (mutate && previous !== desiredChecked) {
      if (typeof el.focus === "function") el.focus();
      if (typeof el.click === "function") el.click();
      const afterClick = stateOf(el);
      if (afterClick.checked !== desiredChecked) {
        if (before.native) {
          setNativeChecked(el, desiredChecked);
        } else if (before.aria) {
          el.setAttribute("aria-checked", desiredChecked ? "true" : "false");
        }
        el.dispatchEvent(new Event("input", { bubbles: true }));
        el.dispatchEvent(new Event("change", { bubbles: true }));
      }
    }
  } catch (error) {
    const current = stateOf(el);
    return { url: location.href, title: document.title, selector, count: elements.length, checked: current.checked, desired_checked: desiredChecked, previous_checked: previous, changed: current.checked !== previous, tag: before.tag, type: before.type, role: before.role, name: nameOf(el), error: { name: error.name, message: error.message }, marker };
  }
  const after = stateOf(el);
  const changed = after.checked !== previous;
  const out = { url: location.href, title: document.title, selector, count: elements.length, checked: after.checked, desired_checked: desiredChecked, previous_checked: previous, changed, already: previous === desiredChecked, tag: before.tag, type: before.type, role: before.role, name: nameOf(el), marker };
  if (mutate && after.checked !== desiredChecked) {
    out.error = { name: "StateMismatchError", message: "checked state did not change to requested value" };
  }
  return out;
})()`, jsStringLiteral(selector), desired, mutate)
}

func fileInputExpression(selector, basename string) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_file_input__";
  const selector = %s;
  const fileName = %s;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, accepted: false, file_set: false, file_name: fileName, content_omitted: true, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    return { url: location.href, title: document.title, selector, count: 0, accepted: false, file_set: false, file_name: fileName, content_omitted: true, error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  const el = elements[0];
  const tag = el.tagName.toLowerCase();
  const type = String(el.getAttribute("type") || "").toLowerCase();
  const accepted = tag === "input" && type === "file";
  const out = { url: location.href, title: document.title, selector, count: elements.length, accepted, file_set: false, tag, type, file_name: fileName, content_omitted: true, marker };
  if (!accepted) out.error = { name: "InvalidTargetError", message: "target element is not input[type=file]" };
  return out;
})()`, jsStringLiteral(selector), jsStringLiteral(basename))
}

func a11yNodeExpression(selector string) string {
	return fmt.Sprintf(`(() => { const selector = %s; const el = document.querySelector(selector); if (!el) return {selector, found:false, error:{name:"NotFoundError", message:"selector matched no elements"}}; const label = el.getAttribute("aria-label") || el.getAttribute("alt") || el.innerText || el.textContent || el.value || ""; return {selector, found:true, role: el.getAttribute("role") || el.tagName.toLowerCase(), name: String(label).trim(), disabled: Boolean(el.disabled || el.getAttribute("aria-disabled") === "true"), ignored: false}; })()`, jsStringLiteral(selector))
}

func collectA11yNodes(ctx context.Context, session *cdp.PageSession, depth, limit int, includeIgnored bool) ([]a11yNode, bool, error) {
	var raw struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := execSessionJSON(ctx, session, "Accessibility.getFullAXTree", map[string]any{}, &raw); err != nil {
		return nil, false, commandError("connection_failed", "connection", fmt.Sprintf("collect accessibility tree: %v", err), ExitConnection, []string{"cdp protocol describe Accessibility.getFullAXTree --json"})
	}
	return normalizeA11yNodes(raw.Nodes, depth, limit, includeIgnored), a11yNodesTruncated(raw.Nodes, depth, limit, includeIgnored), nil
}

func normalizeA11yNode(raw map[string]any) a11yNode {
	node := a11yNode{Ignored: boolValue(raw["ignored"]), Raw: raw}
	if v, ok := raw["nodeId"].(string); ok {
		node.NodeID = v
	}
	node.Role = axPropString(raw["role"])
	node.Name = axPropString(raw["name"])
	node.Value = axPropString(raw["value"])
	node.ChildIDs = stringSliceValue(raw["childIds"])
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

func normalizeA11yNodes(rawNodes []map[string]any, maxDepth, limit int, includeIgnored bool) []a11yNode {
	nodesByID := map[string]a11yNode{}
	childrenByID := map[string][]string{}
	hasParent := map[string]bool{}
	order := make([]string, 0, len(rawNodes))
	for _, raw := range rawNodes {
		node := normalizeA11yNode(raw)
		if node.NodeID == "" {
			node.NodeID = fmt.Sprintf("index:%d", len(order))
		}
		nodesByID[node.NodeID] = node
		childrenByID[node.NodeID] = node.ChildIDs
		order = append(order, node.NodeID)
		for _, childID := range node.ChildIDs {
			hasParent[childID] = true
		}
	}
	roots := make([]string, 0, len(order))
	for _, id := range order {
		if !hasParent[id] {
			roots = append(roots, id)
		}
	}
	if len(roots) == 0 {
		roots = order
	}

	out := make([]a11yNode, 0, len(rawNodes))
	seen := map[string]bool{}
	var visit func(id string, depth int, path string)
	visit = func(id string, depth int, path string) {
		if seen[id] {
			return
		}
		seen[id] = true
		node, ok := nodesByID[id]
		if !ok {
			return
		}
		node.Depth = depth
		node.Path = path
		if (includeIgnored || !node.Ignored) && (maxDepth <= 0 || depth <= maxDepth) {
			out = append(out, node)
		}
		for idx, childID := range childrenByID[id] {
			childPath := fmt.Sprintf("%s/%d", path, idx)
			if path == "" {
				childPath = fmt.Sprint(idx)
			}
			visit(childID, depth+1, childPath)
		}
	}
	for idx, id := range roots {
		visit(id, 0, fmt.Sprint(idx))
	}
	for _, id := range order {
		if !seen[id] {
			visit(id, 0, fmt.Sprint(len(seen)))
		}
	}
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func a11yNodesTruncated(rawNodes []map[string]any, depth, limit int, includeIgnored bool) bool {
	if limit <= 0 {
		return false
	}
	nodes := normalizeA11yNodes(rawNodes, depth, 0, includeIgnored)
	return len(nodes) > limit
}

func collectA11ySnapshot(ctx context.Context, session *cdp.PageSession, selector string, depth, limit int, includeIgnored bool) (a11ySnapshotResult, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		selector = "body"
	}
	rawNodes, source, err := collectA11yRawNodesForSnapshot(ctx, session, selector)
	if err != nil {
		return a11ySnapshotResult{}, err
	}
	nodes := normalizeA11yNodes(rawNodes, depth, limit, includeIgnored)
	truncated := a11yNodesTruncated(rawNodes, depth, limit, includeIgnored)
	lines := ariaSnapshotLines(nodes)
	text := strings.Join(lines, "\n")
	if text != "" {
		text += "\n"
	}
	return a11ySnapshotResult{
		Selector:       selector,
		LineCount:      len(lines),
		Lines:          lines,
		Text:           text,
		Truncated:      truncated,
		Depth:          depth,
		Limit:          limit,
		IncludeIgnored: includeIgnored,
		Source:         source,
	}, nil
}

func collectA11yRawNodesForSnapshot(ctx context.Context, session *cdp.PageSession, selector string) ([]map[string]any, string, error) {
	if selector == "" || selector == "body" || selector == "html" {
		var raw struct {
			Nodes []map[string]any `json:"nodes"`
		}
		if err := execSessionJSON(ctx, session, "Accessibility.getFullAXTree", map[string]any{}, &raw); err != nil {
			return nil, "", commandError("connection_failed", "connection", fmt.Sprintf("collect accessibility snapshot: %v", err), ExitConnection, []string{"cdp protocol describe Accessibility.getFullAXTree --json"})
		}
		return raw.Nodes, "cdp-accessibility-tree", nil
	}
	nodeID, err := queryDOMNodeID(ctx, session, selector, "cdp a11y snapshot --selector "+shellQuote(selector)+" --json")
	if err != nil {
		return nil, "", err
	}
	var raw struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := execSessionJSON(ctx, session, "Accessibility.getPartialAXTree", map[string]any{"nodeId": nodeID, "fetchRelatives": true}, &raw); err != nil {
		return nil, "", commandError("connection_failed", "connection", fmt.Sprintf("collect accessibility snapshot for %q: %v", selector, err), ExitConnection, []string{"cdp protocol describe Accessibility.getPartialAXTree --json"})
	}
	return raw.Nodes, "cdp-accessibility-partial-tree", nil
}

func queryDOMNodeID(ctx context.Context, session *cdp.PageSession, selector, example string) (int, error) {
	var doc struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if err := execSessionJSON(ctx, session, "DOM.getDocument", map[string]any{"depth": 0, "pierce": true}, &doc); err != nil {
		return 0, commandError("connection_failed", "connection", fmt.Sprintf("inspect DOM for selector %q: %v", selector, err), ExitConnection, []string{"cdp protocol describe DOM.getDocument --json"})
	}
	if doc.Root.NodeID == 0 {
		return 0, commandError("dom_unavailable", "connection", "DOM.getDocument did not return a root node", ExitConnection, []string{"cdp daemon health --json"})
	}
	var query struct {
		NodeID int `json:"nodeId"`
	}
	if err := execSessionJSON(ctx, session, "DOM.querySelector", map[string]any{"nodeId": doc.Root.NodeID, "selector": selector}, &query); err != nil {
		return 0, commandError("invalid_selector", "usage", fmt.Sprintf("query selector %q: %v", selector, err), ExitUsage, []string{example})
	}
	if query.NodeID == 0 {
		return 0, commandError("invalid_selector", "usage", fmt.Sprintf("selector %q matched no DOM node", selector), ExitUsage, []string{example})
	}
	return query.NodeID, nil
}

func ariaSnapshotLines(nodes []a11yNode) []string {
	minDepth := -1
	for _, node := range nodes {
		if !ariaSnapshotIncludeNode(node) {
			continue
		}
		if minDepth == -1 || node.Depth < minDepth {
			minDepth = node.Depth
		}
	}
	if minDepth < 0 {
		return []string{}
	}
	lines := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if !ariaSnapshotIncludeNode(node) {
			continue
		}
		depth := node.Depth - minDepth
		if depth < 0 {
			depth = 0
		}
		lines = append(lines, strings.Repeat("  ", depth)+ariaSnapshotLine(node))
	}
	return lines
}

func ariaSnapshotIncludeNode(node a11yNode) bool {
	role := strings.TrimSpace(node.Role)
	if role == "" {
		return false
	}
	switch strings.ToLower(role) {
	case "rootwebarea":
		return false
	case "none", "generic":
		return strings.TrimSpace(node.Name) != ""
	default:
		return true
	}
}

func ariaSnapshotLine(node a11yNode) string {
	role := ariaSnapshotRole(node.Role)
	name := strings.TrimSpace(node.Name)
	if name == "" {
		name = strings.TrimSpace(node.Value)
	}
	attrs := ariaSnapshotAttrs(node)
	if name == "" {
		if attrs == "" {
			return "- " + role
		}
		return "- " + role + " " + attrs
	}
	if role == "text" || role == "paragraph" || role == "textbox" {
		if attrs == "" {
			return "- " + role + ": " + name
		}
		return "- " + role + ": " + name + " " + attrs
	}
	if attrs == "" {
		return "- " + role + " " + fmt.Sprintf("%q", name)
	}
	return "- " + role + " " + fmt.Sprintf("%q", name) + " " + attrs
}

func ariaSnapshotRole(role string) string {
	role = strings.TrimSpace(role)
	switch strings.ToLower(role) {
	case "statictext", "inlinetextbox":
		return "text"
	case "rootwebarea":
		return "document"
	default:
		return strings.ToLower(role)
	}
}

func ariaSnapshotAttrs(node a11yNode) string {
	attrs := []string{}
	if node.Disabled {
		attrs = append(attrs, "disabled")
	}
	if props, ok := node.Raw["properties"].([]any); ok {
		for _, prop := range props {
			m, ok := prop.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if name == "" || name == "disabled" && node.Disabled {
				continue
			}
			if !ariaSnapshotAttrName(name) {
				continue
			}
			value := propValue(m["value"])
			if b, ok := value.(bool); ok {
				if b {
					attrs = append(attrs, name)
				}
				continue
			}
			if s := strings.TrimSpace(fmt.Sprint(value)); s != "" {
				attrs = append(attrs, name+"="+s)
			}
		}
	}
	if len(attrs) == 0 {
		return ""
	}
	return "[" + strings.Join(attrs, " ") + "]"
}

func ariaSnapshotAttrName(name string) bool {
	switch name {
	case "checked", "disabled", "expanded", "level", "pressed", "selected":
		return true
	default:
		return false
	}
}

func stringSliceValue(v any) []string {
	values, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s, ok := value.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
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
  const setNativeValue = (el, next) => {
    const proto = Object.getPrototypeOf(el);
    const descriptor = proto && Object.getOwnPropertyDescriptor(proto, "value");
    if (descriptor && typeof descriptor.set === "function") descriptor.set.call(el, next);
    else el.value = next;
  };
  const previous = element.value ?? "";
  try {
    element.focus();
    setNativeValue(element, value);
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
  const setNativeValue = (el, next) => {
    const proto = Object.getPrototypeOf(el);
    const descriptor = proto && Object.getOwnPropertyDescriptor(proto, "value");
    if (descriptor && typeof descriptor.set === "function") descriptor.set.call(el, next);
    else el.value = next;
  };
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
      setNativeValue(element, value);
      const key = String(ch);
      const keyCode = key.length > 0 ? key.codePointAt(0) : 0;
      const init = { key, code: key.length === 1 ? "Key" + key.toUpperCase() : key, keyCode: keyCode || 0, charCode: keyCode || 0, bubbles: true, cancelable: true };
      element.dispatchEvent(new KeyboardEvent("keydown", init));
      element.dispatchEvent(new KeyboardEvent("keypress", init));
      element.dispatchEvent(new Event("input", { bubbles: true }));
      element.dispatchEvent(new KeyboardEvent("keyup", init));
    }
    element.dispatchEvent(new Event("change", { bubbles: true }));
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
  let count = 0;
  if (selector) {
    let elements;
    try {
      elements = Array.from(document.querySelectorAll(selector));
    } catch (error) {
      return { url: location.href, title: document.title, selector, key, count: 0, dispatched: false, error: { name: error.name, message: error.message }, marker };
    }
    count = elements.length;
    if (count === 0) {
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
  return { url: location.href, title: document.title, selector, key: safeKey, count: selector ? count : 0, dispatched: true, marker };
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

func scrollExpression(selector, block, inline string, mutate bool) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_scroll__";
  const selector = %s;
  const block = %s;
  const inline = %s;
  const mutate = %t;
  const zeroViewport = () => ({
    rect: { x: 0, y: 0, width: 0, height: 0 },
    in_viewport: false,
    fully_in_viewport: false,
    viewport_width: window.innerWidth || 0,
    viewport_height: window.innerHeight || 0,
    scroll_x: window.scrollX || 0,
    scroll_y: window.scrollY || 0
  });
  const evidence = (el) => {
    const rect = el.getBoundingClientRect();
    const inViewport = rect.width > 0 && rect.height > 0 && rect.bottom >= 0 && rect.right >= 0 && rect.top <= window.innerHeight && rect.left <= window.innerWidth;
    const fullyInViewport = rect.width > 0 && rect.height > 0 && rect.top >= 0 && rect.left >= 0 && rect.bottom <= window.innerHeight && rect.right <= window.innerWidth;
    return {
      rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
      in_viewport: inViewport,
      fully_in_viewport: fullyInViewport,
      viewport_width: window.innerWidth || 0,
      viewport_height: window.innerHeight || 0,
      scroll_x: window.scrollX || 0,
      scroll_y: window.scrollY || 0
    };
  };
  const changed = (before, after) => Math.abs(before.scroll_x - after.scroll_x) > 0.5 || Math.abs(before.scroll_y - after.scroll_y) > 0.5 || Math.abs(before.rect.x - after.rect.x) > 0.5 || Math.abs(before.rect.y - after.rect.y) > 0.5;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    const empty = zeroViewport();
    return { url: location.href, title: document.title, selector, count: 0, scrolled: false, changed: false, trial: !mutate, block, inline, before: empty, after: empty, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    const empty = zeroViewport();
    return { url: location.href, title: document.title, selector, count: 0, scrolled: false, changed: false, trial: !mutate, block, inline, before: empty, after: empty, error: { name: "NotFoundError", message: "selector matched no elements" }, marker };
  }
  const el = elements[0];
  const before = evidence(el);
  if (mutate) {
    el.scrollIntoView({ block, inline, behavior: "auto" });
  }
  return new Promise((resolve) => {
    requestAnimationFrame(() => {
      const after = evidence(el);
      resolve({ url: location.href, title: document.title, selector, count: elements.length, scrolled: mutate && after.in_viewport, changed: changed(before, after), trial: !mutate, block, inline, before, after, marker });
    });
  });
})()`, jsStringLiteral(selector), jsStringLiteral(block), jsStringLiteral(inline), mutate)
}

func observeExpression(selector string, limit int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_observe__";
  const selector = %s;
  const limit = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const cssEscape = (value) => {
    if (window.CSS && CSS.escape) return CSS.escape(value);
    return String(value).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
  };
  const roleFor = (element) => {
    const explicit = element.getAttribute("role") || "";
    if (explicit) return explicit;
    const tag = element.tagName.toLowerCase();
    if (tag === "a" && element.href) return "link";
    if (tag === "button") return "button";
    if (tag === "select") return "combobox";
    if (tag === "textarea") return "textbox";
    if (tag === "input") {
      const type = (element.getAttribute("type") || "text").toLowerCase();
      if (["button", "submit", "reset"].includes(type)) return "button";
      if (type === "checkbox") return "checkbox";
      if (type === "radio") return "radio";
      return "textbox";
    }
    return tag;
  };
  const nameFor = (element) => {
    const labelledBy = element.getAttribute("aria-labelledby") || "";
    if (labelledBy) {
      const labelled = labelledBy.split(/\s+/).map((id) => document.getElementById(id)).filter(Boolean).map((el) => normalize(el.innerText || el.textContent)).filter(Boolean).join(" ");
      if (labelled) return labelled;
    }
    const id = element.id || "";
    if (id) {
      const label = document.querySelector('label[for="' + cssEscape(id) + '"]');
      if (label) {
        const text = normalize(label.innerText || label.textContent);
        if (text) return text;
      }
    }
    return normalize(element.getAttribute("aria-label") || element.getAttribute("alt") || element.getAttribute("title") || element.innerText || element.textContent || element.value || "");
  };
  const selectorFor = (element) => {
    const tag = element.tagName.toLowerCase();
    if (element.id) return tag + "#" + cssEscape(element.id);
    const aria = element.getAttribute("aria-label");
    if (aria) return tag + '[aria-label="' + aria.replace(/"/g, '\\"') + '"]';
    const testid = element.getAttribute("data-testid") || element.getAttribute("data-test") || element.getAttribute("data-cy");
    if (testid) return tag + '[data-testid="' + testid.replace(/"/g, '\\"') + '"]';
    const classes = Array.from(element.classList || []).slice(0, 2).map(cssEscape).join(".");
    return classes ? tag + "." + classes : tag;
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, interactive: [], error: { name: error.name, message: error.message } };
  }
  const interactive = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    const rect = element.getBoundingClientRect();
    const style = window.getComputedStyle(element);
    const visible = rect.width > 0 && rect.height > 0 && style.visibility !== "hidden" && style.display !== "none";
    if (!visible) continue;
    const text = normalize(element.innerText || element.textContent || "").slice(0, 240);
    interactive.push({
      ref: "obs:" + interactive.length,
      index,
      tag: element.tagName.toLowerCase(),
      role: roleFor(element),
      name: nameFor(element).slice(0, 240),
      selector: selectorFor(element),
      text,
      href: element.href || "",
      disabled: Boolean(element.disabled || element.getAttribute("aria-disabled") === "true"),
      visible,
      rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height }
    });
    if (limit > 0 && interactive.length >= limit) break;
  }
  const warnings = [];
  if (elements.length > interactive.length && limit > 0 && interactive.length >= limit) warnings.push("result limited; rerun with --limit 0 for all visible candidates");
  return { url: location.href, title: document.title, selector, count: interactive.length, interactive, warnings, marker };
})()`, string(selectorJSON), limit)
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

func waitURLExpression(expected string, contains bool) string {
	expectedJSON, _ := json.Marshal(expected)
	mode := "exact"
	if contains {
		mode = "contains"
	}
	modeJSON, _ := json.Marshal(mode)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_wait_url__";
  const needle = %s;
  const condition = %s;
  const url = location.href;
  const matched = condition === "contains" ? url.includes(needle) : url === needle;
  return { kind: "url", needle, condition, url, title: document.title, matched, count: matched ? 1 : 0, marker };
})()`, string(expectedJSON), string(modeJSON))
}

func waitEvalExpression(expression string) string {
	expressionJSON, _ := json.Marshal(expression)
	return fmt.Sprintf(`(async () => {
  const marker = "__cdp_cli_wait_eval__";
  const expression = %s;
  try {
    const value = await (0, eval)(expression);
    return { kind: "eval", expression, matched: !!value, value, marker };
  } catch (error) {
    return { kind: "eval", expression, matched: false, error: { name: error.name, message: error.message }, marker };
  }
})()`, string(expressionJSON))
}

func waitLoadStateExpression(state string) string {
	stateJSON, _ := json.Marshal(state)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_wait_load_state__";
  const state = %s;
  const readyState = String(document.readyState || "");
  const matched = state === "domcontentloaded" ? readyState === "interactive" || readyState === "complete" : readyState === "complete";
  return { kind: "load-state", state, ready_state: readyState, matched, url: location.href, title: document.title, marker };
})()`, string(stateJSON))
}
