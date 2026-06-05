package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type formValuesResult struct {
	URL      string        `json:"url"`
	Title    string        `json:"title"`
	Count    int           `json:"count"`
	Controls []formControl `json:"controls"`
	Error    *evalError    `json:"error,omitempty"`
}

type formGetResult struct {
	URL      string       `json:"url"`
	Title    string       `json:"title"`
	Selector string       `json:"selector"`
	Count    int          `json:"count"`
	Control  *formControl `json:"control,omitempty"`
	Error    *evalError   `json:"error,omitempty"`
}

type formControl struct {
	SelectorHint      string   `json:"selector_hint,omitempty"`
	Tag               string   `json:"tag"`
	Type              string   `json:"type,omitempty"`
	Role              string   `json:"role,omitempty"`
	Name              string   `json:"name,omitempty"`
	Value             string   `json:"value"`
	Values            []string `json:"values,omitempty"`
	Checked           *bool    `json:"checked,omitempty"`
	Visible           bool     `json:"visible"`
	AriaHidden        bool     `json:"aria_hidden"`
	SelectorAmbiguous bool     `json:"selector_ambiguous,omitempty"`
	ReadOnly          bool     `json:"read_only"`
	Disabled          bool     `json:"disabled"`
	ContentEditable   bool     `json:"content_editable"`
}

type assertValueResult struct {
	Selector string       `json:"selector"`
	Expected string       `json:"expected"`
	Actual   string       `json:"actual"`
	Mode     string       `json:"mode"`
	Passed   bool         `json:"passed"`
	Count    int          `json:"count"`
	Control  *formControl `json:"control,omitempty"`
	Error    *evalError   `json:"error,omitempty"`
}

type assertTextResult struct {
	Selector string     `json:"selector,omitempty"`
	Expected string     `json:"expected"`
	Actual   string     `json:"actual"`
	Mode     string     `json:"mode"`
	Passed   bool       `json:"passed"`
	Count    int        `json:"count"`
	Error    *evalError `json:"error,omitempty"`
}

type assertVisibilityResult struct {
	Selector     string                 `json:"selector"`
	Expected     string                 `json:"expected"`
	Visible      bool                   `json:"visible"`
	Hidden       bool                   `json:"hidden"`
	Passed       bool                   `json:"passed"`
	Count        int                    `json:"count"`
	VisibleCount int                    `json:"visible_count"`
	HiddenCount  int                    `json:"hidden_count"`
	Items        []assertVisibilityItem `json:"items,omitempty"`
	Error        *evalError             `json:"error,omitempty"`
}

type assertVisibilityItem struct {
	Index      int          `json:"index"`
	Tag        string       `json:"tag"`
	ID         string       `json:"id,omitempty"`
	Role       string       `json:"role,omitempty"`
	Name       string       `json:"name,omitempty"`
	Visible    bool         `json:"visible"`
	Display    string       `json:"display,omitempty"`
	Visibility string       `json:"visibility,omitempty"`
	Hidden     bool         `json:"hidden"`
	Rect       snapshotRect `json:"rect"`
}

type assertEnabledResult struct {
	Selector      string              `json:"selector"`
	Expected      string              `json:"expected"`
	Enabled       bool                `json:"enabled"`
	Disabled      bool                `json:"disabled"`
	Passed        bool                `json:"passed"`
	Count         int                 `json:"count"`
	EnabledCount  int                 `json:"enabled_count"`
	DisabledCount int                 `json:"disabled_count"`
	Items         []assertEnabledItem `json:"items,omitempty"`
	Error         *evalError          `json:"error,omitempty"`
}

type assertEnabledItem struct {
	Index            int          `json:"index"`
	Tag              string       `json:"tag"`
	ID               string       `json:"id,omitempty"`
	Role             string       `json:"role,omitempty"`
	Name             string       `json:"name,omitempty"`
	Enabled          bool         `json:"enabled"`
	Disabled         bool         `json:"disabled"`
	DisabledReason   []string     `json:"disabled_reason,omitempty"`
	NativeDisabled   bool         `json:"native_disabled"`
	FieldsetDisabled bool         `json:"fieldset_disabled"`
	AriaDisabled     bool         `json:"aria_disabled"`
	ReadOnly         bool         `json:"read_only"`
	ContentEditable  bool         `json:"content_editable"`
	Visible          bool         `json:"visible"`
	Rect             snapshotRect `json:"rect"`
}

type assertEditableResult struct {
	Selector         string               `json:"selector"`
	Expected         string               `json:"expected"`
	Editable         bool                 `json:"editable"`
	ReadOnly         bool                 `json:"read_only"`
	Passed           bool                 `json:"passed"`
	Count            int                  `json:"count"`
	EditableCount    int                  `json:"editable_count"`
	ReadOnlyCount    int                  `json:"read_only_count"`
	DisabledCount    int                  `json:"disabled_count"`
	UnsupportedCount int                  `json:"unsupported_count"`
	Items            []assertEditableItem `json:"items,omitempty"`
	Error            *evalError           `json:"error,omitempty"`
}

type assertEditableItem struct {
	Index                int          `json:"index"`
	Tag                  string       `json:"tag"`
	ID                   string       `json:"id,omitempty"`
	Type                 string       `json:"type,omitempty"`
	Role                 string       `json:"role,omitempty"`
	Name                 string       `json:"name,omitempty"`
	Editable             bool         `json:"editable"`
	ReadOnly             bool         `json:"read_only"`
	ReadOnlyReason       []string     `json:"read_only_reason,omitempty"`
	SupportsEditable     bool         `json:"supports_editable"`
	SupportsAriaReadonly bool         `json:"supports_aria_readonly"`
	NativeReadOnly       bool         `json:"native_read_only"`
	AriaReadOnly         bool         `json:"aria_read_only"`
	Enabled              bool         `json:"enabled"`
	Disabled             bool         `json:"disabled"`
	DisabledReason       []string     `json:"disabled_reason,omitempty"`
	ContentEditable      bool         `json:"content_editable"`
	Visible              bool         `json:"visible"`
	Rect                 snapshotRect `json:"rect"`
}

type assertCheckedResult struct {
	Selector         string              `json:"selector"`
	Expected         string              `json:"expected"`
	Checked          bool                `json:"checked"`
	Unchecked        bool                `json:"unchecked"`
	Passed           bool                `json:"passed"`
	Count            int                 `json:"count"`
	CheckedCount     int                 `json:"checked_count"`
	UncheckedCount   int                 `json:"unchecked_count"`
	UnsupportedCount int                 `json:"unsupported_count"`
	Items            []assertCheckedItem `json:"items,omitempty"`
	Attempts         int                 `json:"attempts,omitempty"`
	ElapsedMS        int64               `json:"elapsed_ms,omitempty"`
	PollInterval     string              `json:"poll_interval,omitempty"`
	Error            *evalError          `json:"error,omitempty"`
}

type assertCheckedItem struct {
	Index           int          `json:"index"`
	Tag             string       `json:"tag"`
	ID              string       `json:"id,omitempty"`
	Type            string       `json:"type,omitempty"`
	Role            string       `json:"role,omitempty"`
	Name            string       `json:"name,omitempty"`
	Checked         bool         `json:"checked"`
	SupportsChecked bool         `json:"supports_checked"`
	AriaChecked     string       `json:"aria_checked,omitempty"`
	Visible         bool         `json:"visible"`
	Rect            snapshotRect `json:"rect"`
}

func (a *app) newFormCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "form", Short: "Inspect live form control state"}
	cmd.AddCommand(a.newFormValuesCommand())
	cmd.AddCommand(a.newFormGetCommand())
	return cmd
}

func (a *app) newFormValuesCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var includeHidden bool
	cmd := &cobra.Command{Use: "values", Short: "List input, textarea, select, and contenteditable values", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		var result formValuesResult
		if err := evaluateJSONValue(ctx, session, formValuesExpression(includeHidden), "form values", &result); err != nil {
			return err
		}
		if result.Error != nil {
			return invalidSelectorError("form controls", result.Error, "cdp form values --json")
		}
		return a.render(ctx, fmt.Sprintf("form\t%d controls", result.Count), map[string]any{"ok": true, "target": pageRow(target), "form": result, "controls": result.Controls})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().BoolVar(&includeHidden, "include-hidden", false, "include hidden form controls such as UI-library measurement clones")
	return cmd
}

func (a *app) newFormGetCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	cmd := &cobra.Command{Use: "get <selector>", Short: "Return one form control value by CSS selector", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		var result formGetResult
		if err := evaluateJSONValue(ctx, session, formGetExpression(args[0]), "form get", &result); err != nil {
			return err
		}
		if result.Error != nil {
			return invalidSelectorError(args[0], result.Error, "cdp form get 'input[name=q]' --json")
		}
		if result.Count == 0 {
			return commandError("selector_not_found", "check_failed", fmt.Sprintf("selector %q matched no form controls", args[0]), ExitCheckFailed, []string{"cdp form values --json", "cdp dom query " + args[0] + " --json"})
		}
		return a.render(ctx, result.Control.Value, map[string]any{"ok": true, "target": pageRow(target), "form": result, "control": result.Control})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newAssertCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "assert", Short: "Assert browser state with JSON diagnostics"}
	cmd.AddCommand(a.newAssertValueCommand())
	cmd.AddCommand(a.newAssertTextCommand())
	cmd.AddCommand(a.newAssertVisibleCommand())
	cmd.AddCommand(a.newAssertHiddenCommand())
	cmd.AddCommand(a.newAssertEnabledCommand())
	cmd.AddCommand(a.newAssertDisabledCommand())
	cmd.AddCommand(a.newAssertEditableCommand())
	cmd.AddCommand(a.newAssertReadonlyCommand())
	cmd.AddCommand(a.newAssertCheckedCommand())
	cmd.AddCommand(a.newAssertUncheckedCommand())
	return cmd
}

func (a *app) newAssertValueCommand() *cobra.Command {
	var targetID, urlContains, titleContains, mode string
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "value <selector-or-locator> <expected>", Short: "Assert a form control value by CSS selector or strict locator", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
			return err
		}
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "assert value")
		if err != nil {
			return err
		}
		var got formGetResult
		if err := evaluateJSONValue(ctx, session, formGetExpression(selector), "assert value", &got); err != nil {
			return err
		}
		if got.Error != nil {
			return invalidSelectorError(selector, got.Error, "cdp assert value 'input[name=q]' expected --json")
		}
		actual := ""
		if got.Control != nil {
			actual = got.Control.Value
		}
		passed, err := assertionMatch(actual, args[1], mode)
		if err != nil {
			return err
		}
		result := assertValueResult{Selector: selector, Expected: args[1], Actual: actual, Mode: normalizeAssertMode(mode), Passed: passed, Count: got.Count, Control: got.Control, Error: got.Error}
		report := map[string]any{"ok": passed, "target": pageRow(target), "assertion": result}
		if locator != nil {
			report["locator"] = locator
			report["resolved_selector"] = selector
		}
		if !passed {
			return commandErrorWithData("assertion_failed", "check_failed", fmt.Sprintf("value assertion failed for %q: got %q", selector, actual), ExitCheckFailed, []string{"cdp form get " + shellQuote(selector) + " --json"}, report)
		}
		return a.render(ctx, "assertion passed", report)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "exact", "match mode: exact, contains, or regex")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertTextCommand() *cobra.Command {
	var targetID, urlContains, titleContains, mode string
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "text [selector-or-locator] <expected>", Short: "Assert visible text by body, CSS selector, or strict locator", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
			return err
		}
		selector := "body"
		expected := args[0]
		var locator *locatorFindResult
		if len(args) == 1 && locatorOptionsNeedQuery(locatorOpts) {
			return commandError("usage", "usage", "locator flags require both a locator query and expected text", ExitUsage, []string{"cdp assert text 'Saved successfully' --json", "cdp assert text 'Search' 'Search' --by role --role button --json"})
		}
		if len(args) == 2 {
			expected = args[1]
		}
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		if len(args) == 2 {
			selector, locator, err = resolveActionSelector(ctx, session, args[0], locatorOpts, "assert text")
			if err != nil {
				return err
			}
		}
		var got textResult
		if err := evaluateJSONValue(ctx, session, textExpression(selector, 0, 1), "assert text", &got); err != nil {
			return err
		}
		if got.Error != nil {
			return invalidSelectorError(selector, got.Error, "cdp assert text expected --json")
		}
		passed, err := assertionMatch(got.Text, expected, mode)
		if err != nil {
			return err
		}
		result := assertTextResult{Selector: selector, Expected: expected, Actual: got.Text, Mode: normalizeAssertMode(mode), Passed: passed, Count: got.Count, Error: got.Error}
		report := map[string]any{"ok": passed, "target": pageRow(target), "assertion": result}
		if locator != nil {
			report["locator"] = locator
			report["resolved_selector"] = selector
		}
		if !passed {
			return commandErrorWithData("assertion_failed", "check_failed", fmt.Sprintf("text assertion failed for %q: %q was not found", selector, expected), ExitCheckFailed, []string{"cdp text " + shellQuote(selector) + " --limit 0 --json"}, report)
		}
		return a.render(ctx, "assertion passed", report)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "contains", "match mode: exact, contains, or regex")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func locatorOptionsNeedQuery(opts locatorActionOptions) bool {
	return opts.By != "css" || opts.Role != "" || opts.Exact || opts.IncludeHidden || opts.TestIDAttr != "data-testid" || opts.Limit != 20
}

func (a *app) newAssertVisibleCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "visible <selector-or-locator>", Short: "Assert an element is visible by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertVisibilityCommand(cmd, args[0], "visible", locatorOpts, targetID, urlContains, titleContains)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertHiddenCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "hidden <selector-or-locator>", Short: "Assert an element is hidden or absent by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertVisibilityCommand(cmd, args[0], "hidden", locatorOpts, targetID, urlContains, titleContains)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertEnabledCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "enabled <selector-or-locator>", Short: "Assert an element is enabled by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertEnabledCommand(cmd, args[0], "enabled", locatorOpts, targetID, urlContains, titleContains)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertDisabledCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "disabled <selector-or-locator>", Short: "Assert an element is disabled by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertEnabledCommand(cmd, args[0], "disabled", locatorOpts, targetID, urlContains, titleContains)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertEditableCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "editable <selector-or-locator>", Short: "Assert an element is editable by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertEditableCommand(cmd, args[0], "editable", locatorOpts, targetID, urlContains, titleContains)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertReadonlyCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "readonly <selector-or-locator>", Aliases: []string{"read-only"}, Short: "Assert an element is read-only by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertEditableCommand(cmd, args[0], "readonly", locatorOpts, targetID, urlContains, titleContains)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertCheckedCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "checked <selector-or-locator>", Short: "Assert a checkbox, radio, or switch is checked by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertCheckedCommand(cmd, args[0], "checked", locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertUncheckedCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "unchecked <selector-or-locator>", Short: "Assert a checkbox, radio, or switch is unchecked by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertCheckedCommand(cmd, args[0], "unchecked", locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) runAssertCheckedCommand(cmd *cobra.Command, query, expected string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert checked 'Subscribe to newsletter' --by label --poll 250ms --json"})
	}
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel := a.commandContextWithDefault(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	start := time.Now()
	got, locator, selector, err := waitForCheckedAssertion(ctx, session, query, expected, locatorOpts, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if strings.TrimSpace(selector) != "" {
			report["resolved_selector"] = selector
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("%s assertion for %q did not pass before timeout: %v", expected, query, ctx.Err()), ExitTimeout, checkedAssertionRemediations(query, selector, locatorOpts), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func waitForCheckedAssertion(ctx context.Context, session *cdp.PageSession, query, expected string, opts locatorActionOptions, poll time.Duration, start time.Time) (assertCheckedResult, *locatorFindResult, string, error) {
	attempts := 0
	last := assertCheckedResult{Selector: query, Expected: expected, PollInterval: poll.String()}
	var lastLocator *locatorFindResult
	lastSelector := query
	for {
		attempts++
		selector := query
		var locator *locatorFindResult
		if opts.By != "css" {
			var result locatorFindResult
			if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "assert "+expected+" locator", &result); err != nil {
				return last, lastLocator, lastSelector, err
			}
			locator = &result
			lastLocator = locator
			if result.Error != nil {
				return last, locator, "", commandError("invalid_locator", "usage", fmt.Sprintf("assert %s locator %s %q: %s", expected, opts.By, query, result.Error.Message), ExitUsage, locatorActionRemediations("assert "+expected, query, opts))
			}
			if result.Count != 1 || len(result.Matches) != 1 || strings.TrimSpace(result.Matches[0].SelectorHint) == "" || result.Matches[0].SelectorAmbiguous {
				last = checkedAssertionPendingResult(query, expected, result.Count, attempts, start, poll)
				lastSelector = ""
				if done, err := waitForNextAssertionPoll(ctx, poll); done {
					return last, lastLocator, lastSelector, err
				}
				continue
			}
			selector = strings.TrimSpace(result.Matches[0].SelectorHint)
			lastSelector = selector
		}

		var got assertCheckedResult
		if err := evaluateJSONValue(ctx, session, assertCheckedExpression(selector, 20), "assert "+expected, &got); err != nil {
			return last, lastLocator, lastSelector, err
		}
		if got.Error != nil {
			return got, locator, selector, invalidSelectorError(selector, got.Error, "cdp assert "+expected+" 'input[type=checkbox]' --json")
		}
		finishCheckedAssertionResult(&got, expected, attempts, start, poll)
		last = got
		lastLocator = locator
		lastSelector = selector
		if got.Passed {
			return got, locator, selector, nil
		}
		if done, err := waitForNextAssertionPoll(ctx, poll); done {
			return last, lastLocator, lastSelector, err
		}
	}
}

func finishCheckedAssertionResult(got *assertCheckedResult, expected string, attempts int, start time.Time, poll time.Duration) {
	got.Expected = expected
	got.Passed = got.Checked
	if expected == "unchecked" {
		got.Passed = got.Unchecked
	}
	got.Attempts = attempts
	got.ElapsedMS = time.Since(start).Milliseconds()
	got.PollInterval = poll.String()
}

func checkedAssertionPendingResult(query, expected string, count, attempts int, start time.Time, poll time.Duration) assertCheckedResult {
	return assertCheckedResult{
		Selector:       query,
		Expected:       expected,
		Checked:        false,
		Unchecked:      false,
		Passed:         false,
		Count:          count,
		Attempts:       attempts,
		ElapsedMS:      time.Since(start).Milliseconds(),
		PollInterval:   poll.String(),
		CheckedCount:   0,
		UncheckedCount: 0,
	}
}

func waitForNextAssertionPoll(ctx context.Context, poll time.Duration) (bool, error) {
	timer := time.NewTimer(poll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true, ctx.Err()
	case <-timer.C:
		return false, nil
	}
}

func (a *app) runAssertEditableCommand(cmd *cobra.Command, query, expected string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string) error {
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel := a.browserCommandContext(cmd)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	selector, locator, err := resolveActionSelector(ctx, session, query, locatorOpts, "assert "+expected)
	if err != nil {
		return err
	}
	var got assertEditableResult
	if err := evaluateJSONValue(ctx, session, assertEditableExpression(selector, 20), "assert "+expected, &got); err != nil {
		return err
	}
	if got.Error != nil {
		return invalidSelectorError(selector, got.Error, "cdp assert "+expected+" 'input[name=q]' --json")
	}
	got.Expected = expected
	got.Passed = got.Editable
	if expected == "readonly" {
		got.Passed = got.ReadOnly
	}
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		report["resolved_selector"] = selector
	}
	if !got.Passed {
		return commandErrorWithData("assertion_failed", "check_failed", editableAssertionFailureMessage(expected, selector, got), ExitCheckFailed, []string{locatorActionFindCommand(query, locatorOpts), "cdp dom query " + shellQuote(selector) + " --json"}, report)
	}
	return a.render(ctx, "assertion passed", report)
}

func (a *app) runAssertEnabledCommand(cmd *cobra.Command, query, expected string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string) error {
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel := a.browserCommandContext(cmd)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	selector, locator, err := resolveActionSelector(ctx, session, query, locatorOpts, "assert "+expected)
	if err != nil {
		return err
	}
	var got assertEnabledResult
	if err := evaluateJSONValue(ctx, session, assertEnabledExpression(selector, 20), "assert "+expected, &got); err != nil {
		return err
	}
	if got.Error != nil {
		return invalidSelectorError(selector, got.Error, "cdp assert "+expected+" 'button[type=submit]' --json")
	}
	got.Expected = expected
	got.Passed = got.Enabled
	if expected == "disabled" {
		got.Passed = got.Disabled
	}
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		report["resolved_selector"] = selector
	}
	if !got.Passed {
		return commandErrorWithData("assertion_failed", "check_failed", enabledAssertionFailureMessage(expected, selector, got), ExitCheckFailed, []string{locatorActionFindCommand(query, locatorOpts), "cdp dom query " + shellQuote(selector) + " --json"}, report)
	}
	return a.render(ctx, "assertion passed", report)
}

func (a *app) runAssertVisibilityCommand(cmd *cobra.Command, query, expected string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string) error {
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel := a.browserCommandContext(cmd)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	resolveOpts := locatorOpts
	if resolveOpts.By != "css" {
		resolveOpts.IncludeHidden = true
	}
	selector := query
	var locator *locatorFindResult
	if resolveOpts.By != "css" {
		if expected == "hidden" {
			selector, locator, err = resolveOptionalHiddenAssertionSelector(ctx, session, query, resolveOpts)
		} else {
			selector, locator, err = resolveActionSelector(ctx, session, query, resolveOpts, "assert "+expected)
		}
		if err != nil {
			return err
		}
	}

	var got assertVisibilityResult
	if selector == "" && expected == "hidden" && locator != nil && locator.Count == 0 {
		got = assertVisibilityResult{Selector: query, Expected: "hidden", Visible: false, Hidden: true, Passed: true, Count: 0, VisibleCount: 0, HiddenCount: 0}
	} else {
		if err := evaluateJSONValue(ctx, session, assertVisibilityExpression(selector, 20), "assert "+expected, &got); err != nil {
			return err
		}
		if got.Error != nil {
			return invalidSelectorError(selector, got.Error, "cdp assert "+expected+" 'button[type=submit]' --json")
		}
		got.Expected = expected
		got.Hidden = got.VisibleCount == 0
		got.Passed = got.Visible
		if expected == "hidden" {
			got.Passed = got.Hidden
		}
	}

	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if selector != "" {
			report["resolved_selector"] = selector
		}
	}
	if !got.Passed {
		return commandErrorWithData("assertion_failed", "check_failed", visibilityAssertionFailureMessage(expected, selector, got), ExitCheckFailed, visibilityAssertionRemediations(query, selector, resolveOpts), report)
	}
	return a.render(ctx, "assertion passed", report)
}

func resolveOptionalHiddenAssertionSelector(ctx context.Context, session *cdp.PageSession, query string, opts locatorActionOptions) (string, *locatorFindResult, error) {
	var result locatorFindResult
	if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "assert hidden locator", &result); err != nil {
		return "", nil, err
	}
	if result.Error != nil {
		return "", &result, commandError("invalid_locator", "usage", fmt.Sprintf("assert hidden locator %s %q: %s", opts.By, query, result.Error.Message), ExitUsage, []string{"cdp locator find Search --by label --json", "cdp locator find Submit --by role --role button --json"})
	}
	if result.Count == 0 {
		return "", &result, nil
	}
	if result.Count != 1 || len(result.Matches) != 1 {
		return "", &result, commandError("ambiguous_locator", "usage", fmt.Sprintf("assert hidden locator %s %q matched %d elements; refine the locator before asserting", opts.By, query, result.Count), ExitUsage, locatorActionRemediations("assert hidden", query, opts))
	}
	match := result.Matches[0]
	selector := strings.TrimSpace(match.SelectorHint)
	if selector == "" || match.SelectorAmbiguous {
		return "", &result, commandError("ambiguous_locator", "usage", fmt.Sprintf("assert hidden locator %s %q matched one element but did not produce a unique CSS selector hint", opts.By, query), ExitUsage, []string{locatorActionFindCommand(query, opts), "cdp snapshot --selector body --json"})
	}
	return selector, &result, nil
}

func visibilityAssertionFailureMessage(expected, selector string, got assertVisibilityResult) string {
	if expected == "hidden" {
		return fmt.Sprintf("hidden assertion failed for %q: %d visible of %d matched", selector, got.VisibleCount, got.Count)
	}
	return fmt.Sprintf("visible assertion failed for %q: %d visible of %d matched", selector, got.VisibleCount, got.Count)
}

func visibilityAssertionRemediations(query, selector string, opts locatorActionOptions) []string {
	if selector == "" {
		selector = query
	}
	return []string{locatorActionFindCommand(query, opts), "cdp dom query " + shellQuote(selector) + " --json"}
}

func enabledAssertionFailureMessage(expected, selector string, got assertEnabledResult) string {
	return fmt.Sprintf("%s assertion failed for %q: %d enabled and %d disabled of %d matched", expected, selector, got.EnabledCount, got.DisabledCount, got.Count)
}

func editableAssertionFailureMessage(expected, selector string, got assertEditableResult) string {
	return fmt.Sprintf("%s assertion failed for %q: %d editable, %d read-only, %d disabled, and %d unsupported of %d matched", expected, selector, got.EditableCount, got.ReadOnlyCount, got.DisabledCount, got.UnsupportedCount, got.Count)
}

func checkedAssertionFailureMessage(expected, selector string, got assertCheckedResult) string {
	return fmt.Sprintf("%s assertion failed for %q: %d checked, %d unchecked, and %d unsupported of %d matched", expected, selector, got.CheckedCount, got.UncheckedCount, got.UnsupportedCount, got.Count)
}

func checkedAssertionRemediations(query, selector string, opts locatorActionOptions) []string {
	commands := []string{locatorActionFindCommand(query, opts)}
	if strings.TrimSpace(selector) != "" {
		commands = append(commands, "cdp form get "+shellQuote(selector)+" --json")
	} else {
		commands = append(commands, "cdp form values --json")
	}
	return commands
}

func assertionMatch(actual, expected, mode string) (bool, error) {
	switch normalizeAssertMode(mode) {
	case "exact":
		return actual == expected, nil
	case "contains":
		return strings.Contains(actual, expected), nil
	case "regex":
		re, err := regexp.Compile(expected)
		if err != nil {
			return false, commandError("invalid_regex", "usage", err.Error(), ExitUsage, []string{"cdp assert text --mode regex 'Welcome|Hello' --json"})
		}
		return re.MatchString(actual), nil
	default:
		return false, commandError("invalid_assert_mode", "usage", "--mode must be exact, contains, or regex", ExitUsage, []string{"cdp assert value input expected --mode exact --json"})
	}
}

func normalizeAssertMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		return "exact"
	}
	return m
}

func formValuesExpression(includeHidden bool) string {
	return `(() => { const __cdp_cli_form_values__ = true; return (` + formCollectorJS("null", fmt.Sprintf("%t", includeHidden)) + `); })()`
}

func formGetExpression(selector string) string {
	return `(() => { const __cdp_cli_form_get__ = true; return (` + formCollectorJS(jsStringLiteral(selector), "true") + `); })()`
}

func assertVisibilityExpression(selector string, limit int) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_assert_visible__";
  const selector = %s;
  const limit = %d;
  const norm = (value) => String(value || "").replace(/\s+/g, " ").trim();
  const roleOf = (el) => {
    const explicit = norm(el.getAttribute("role")).split(" ")[0];
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    const type = String(el.getAttribute("type") || "").toLowerCase();
    if (tag === "button") return "button";
    if (tag === "a" && el.hasAttribute("href")) return "link";
    if (/^h[1-6]$/.test(tag)) return "heading";
    if (tag === "textarea") return "textbox";
    if (tag === "select") return el.multiple ? "listbox" : "combobox";
    if (tag === "input") {
      if (["button", "submit", "reset"].includes(type)) return "button";
      if (type === "checkbox") return "checkbox";
      if (type === "radio") return "radio";
      if (type === "range") return "slider";
      if (type === "search") return "searchbox";
      return "textbox";
    }
    return "";
  };
  const nameOf = (el) => norm(el.getAttribute("aria-label") || el.getAttribute("alt") || el.getAttribute("title") || el.getAttribute("placeholder") || el.getAttribute("value") || el.innerText || el.textContent || "");
  const itemFor = (el, index) => {
    const style = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    const hidden = Boolean(el.hidden || el.closest("[hidden]") || style.display === "none" || style.visibility === "hidden");
    const visible = !hidden && rect.width > 0 && rect.height > 0;
    return {
      index,
      tag: el.tagName.toLowerCase(),
      id: el.id || "",
      role: roleOf(el),
      name: nameOf(el).slice(0, 240),
      visible,
      display: style.display || "",
      visibility: style.visibility || "",
      hidden,
      rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height }
    };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, expected: "visible", visible: false, hidden: false, passed: false, count: 0, visible_count: 0, hidden_count: 0, items: [], error: { name: error.name, message: error.message }, marker };
  }
  const allItems = elements.map(itemFor);
  const visibleCount = allItems.filter((item) => item.visible).length;
  const hiddenCount = allItems.length - visibleCount;
  return { url: location.href, title: document.title, selector, expected: "visible", visible: visibleCount > 0, hidden: visibleCount === 0, passed: visibleCount > 0, count: allItems.length, visible_count: visibleCount, hidden_count: hiddenCount, items: allItems.slice(0, limit), marker };
})()`, jsStringLiteral(selector), limit)
}

func assertEnabledExpression(selector string, limit int) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_assert_enabled__";
  const selector = %s;
  const limit = %d;
  const nativeDisabledTags = new Set(["button", "select", "input", "textarea", "option", "optgroup"]);
  const norm = (value) => String(value || "").replace(/\s+/g, " ").trim();
  const roleOf = (el) => {
    const explicit = norm(el.getAttribute("role")).split(" ")[0];
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    const type = String(el.getAttribute("type") || "").toLowerCase();
    if (tag === "button") return "button";
    if (tag === "a" && el.hasAttribute("href")) return "link";
    if (/^h[1-6]$/.test(tag)) return "heading";
    if (tag === "textarea") return "textbox";
    if (tag === "select") return el.multiple ? "listbox" : "combobox";
    if (tag === "input") {
      if (["button", "submit", "reset"].includes(type)) return "button";
      if (type === "checkbox") return "checkbox";
      if (type === "radio") return "radio";
      if (type === "range") return "slider";
      if (type === "search") return "searchbox";
      return "textbox";
    }
    return "";
  };
  const nameOf = (el) => norm(el.getAttribute("aria-label") || el.getAttribute("alt") || el.getAttribute("title") || el.getAttribute("placeholder") || el.getAttribute("value") || el.innerText || el.textContent || "");
  const visibilityOf = (el) => {
    const style = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    const hidden = Boolean(el.hidden || el.closest("[hidden]") || style.display === "none" || style.visibility === "hidden");
    return { visible: !hidden && rect.width > 0 && rect.height > 0, rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } };
  };
  const disabledInfo = (el) => {
    const tag = el.tagName.toLowerCase();
    const nativeDisableable = nativeDisabledTags.has(tag);
    const nativeDisabled = nativeDisableable && el.hasAttribute("disabled");
    const fieldsetDisabled = nativeDisableable && Boolean(el.closest("fieldset[disabled]"));
    let ariaDisabled = false;
    for (let node = el; node && node.nodeType === Node.ELEMENT_NODE; node = node.parentElement) {
      if (String(node.getAttribute("aria-disabled") || "").toLowerCase() === "true") {
        ariaDisabled = true;
        break;
      }
    }
    const reason = [];
    if (nativeDisabled) reason.push("native_disabled");
    if (fieldsetDisabled) reason.push("fieldset_disabled");
    if (ariaDisabled) reason.push("aria_disabled");
    return { disabled: nativeDisabled || fieldsetDisabled || ariaDisabled, nativeDisabled, fieldsetDisabled, ariaDisabled, reason };
  };
  const itemFor = (el, index) => {
    const disabled = disabledInfo(el);
    const visibility = visibilityOf(el);
    return {
      index,
      tag: el.tagName.toLowerCase(),
      id: el.id || "",
      role: roleOf(el),
      name: nameOf(el).slice(0, 240),
      enabled: !disabled.disabled,
      disabled: disabled.disabled,
      disabled_reason: disabled.reason,
      native_disabled: disabled.nativeDisabled,
      fieldset_disabled: disabled.fieldsetDisabled,
      aria_disabled: disabled.ariaDisabled,
      read_only: Boolean(el.readOnly) || el.getAttribute("aria-readonly") === "true",
      content_editable: Boolean(el.isContentEditable),
      visible: visibility.visible,
      rect: visibility.rect
    };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, expected: "enabled", enabled: false, disabled: false, passed: false, count: 0, enabled_count: 0, disabled_count: 0, items: [], error: { name: error.name, message: error.message }, marker };
  }
  const allItems = elements.map(itemFor);
  const enabledCount = allItems.filter((item) => item.enabled).length;
  const disabledCount = allItems.filter((item) => item.disabled).length;
  return { url: location.href, title: document.title, selector, expected: "enabled", enabled: enabledCount > 0, disabled: allItems.length > 0 && enabledCount === 0, passed: enabledCount > 0, count: allItems.length, enabled_count: enabledCount, disabled_count: disabledCount, items: allItems.slice(0, limit), marker };
})()`, jsStringLiteral(selector), limit)
}

func assertEditableExpression(selector string, limit int) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_assert_editable__";
  const selector = %s;
  const limit = %d;
  const nativeDisabledTags = new Set(["button", "select", "input", "textarea", "option", "optgroup"]);
  const nativeEditableTags = new Set(["input", "textarea", "select"]);
  const ariaReadonlyRoles = new Set(["checkbox", "combobox", "grid", "gridcell", "listbox", "radiogroup", "searchbox", "slider", "spinbutton", "switch", "textbox", "treegrid"]);
  const norm = (value) => String(value || "").replace(/\s+/g, " ").trim();
  const roleOf = (el) => {
    const explicit = norm(el.getAttribute("role")).split(" ")[0];
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    const type = String(el.getAttribute("type") || "").toLowerCase();
    if (tag === "button") return "button";
    if (tag === "a" && el.hasAttribute("href")) return "link";
    if (/^h[1-6]$/.test(tag)) return "heading";
    if (tag === "textarea") return "textbox";
    if (tag === "select") return el.multiple ? "listbox" : "combobox";
    if (tag === "input") {
      if (["button", "submit", "reset"].includes(type)) return "button";
      if (type === "checkbox") return "checkbox";
      if (type === "radio") return "radio";
      if (type === "range") return "slider";
      if (type === "search") return "searchbox";
      return "textbox";
    }
    return "";
  };
  const nameOf = (el) => norm(el.getAttribute("aria-label") || el.getAttribute("alt") || el.getAttribute("title") || el.getAttribute("placeholder") || el.getAttribute("value") || el.innerText || el.textContent || "");
  const visibilityOf = (el) => {
    const style = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    const hidden = Boolean(el.hidden || el.closest("[hidden]") || style.display === "none" || style.visibility === "hidden");
    return { visible: !hidden && rect.width > 0 && rect.height > 0, rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } };
  };
  const disabledInfo = (el) => {
    const tag = el.tagName.toLowerCase();
    const nativeDisableable = nativeDisabledTags.has(tag);
    const nativeDisabled = nativeDisableable && el.hasAttribute("disabled");
    const fieldsetDisabled = nativeDisableable && Boolean(el.closest("fieldset[disabled]"));
    let ariaDisabled = false;
    for (let node = el; node && node.nodeType === Node.ELEMENT_NODE; node = node.parentElement) {
      if (String(node.getAttribute("aria-disabled") || "").toLowerCase() === "true") {
        ariaDisabled = true;
        break;
      }
    }
    const reason = [];
    if (nativeDisabled) reason.push("native_disabled");
    if (fieldsetDisabled) reason.push("fieldset_disabled");
    if (ariaDisabled) reason.push("aria_disabled");
    return { disabled: nativeDisabled || fieldsetDisabled || ariaDisabled, reason };
  };
  const readOnlyInfo = (el, role) => {
    const tag = el.tagName.toLowerCase();
    const nativeEditable = nativeEditableTags.has(tag);
    const contentEditable = Boolean(el.isContentEditable);
    const supportsAriaReadonly = ariaReadonlyRoles.has(role);
    const supportsEditable = nativeEditable || contentEditable || supportsAriaReadonly;
    const nativeReadOnly = nativeEditable && el.hasAttribute("readonly");
    const ariaReadOnly = supportsAriaReadonly && String(el.getAttribute("aria-readonly") || "").toLowerCase() === "true";
    const reason = [];
    if (nativeReadOnly) reason.push("native_readonly");
    if (ariaReadOnly) reason.push("aria_readonly");
    return { readOnly: nativeReadOnly || ariaReadOnly, nativeReadOnly, ariaReadOnly, supportsEditable, supportsAriaReadonly, contentEditable, reason };
  };
  const itemFor = (el, index) => {
    const role = roleOf(el);
    const disabled = disabledInfo(el);
    const readonly = readOnlyInfo(el, role);
    const visibility = visibilityOf(el);
    const editable = readonly.supportsEditable && !disabled.disabled && !readonly.readOnly;
    return {
      index,
      tag: el.tagName.toLowerCase(),
      id: el.id || "",
      type: el.getAttribute("type") || "",
      role,
      name: nameOf(el).slice(0, 240),
      editable,
      read_only: readonly.readOnly,
      read_only_reason: readonly.reason,
      supports_editable: readonly.supportsEditable,
      supports_aria_readonly: readonly.supportsAriaReadonly,
      native_read_only: readonly.nativeReadOnly,
      aria_read_only: readonly.ariaReadOnly,
      enabled: !disabled.disabled,
      disabled: disabled.disabled,
      disabled_reason: disabled.reason,
      content_editable: readonly.contentEditable,
      visible: visibility.visible,
      rect: visibility.rect
    };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, expected: "editable", editable: false, read_only: false, passed: false, count: 0, editable_count: 0, read_only_count: 0, disabled_count: 0, unsupported_count: 0, items: [], error: { name: error.name, message: error.message }, marker };
  }
  const allItems = elements.map(itemFor);
  const editableCount = allItems.filter((item) => item.editable).length;
  const readOnlyCount = allItems.filter((item) => item.read_only).length;
  const disabledCount = allItems.filter((item) => item.disabled).length;
  const unsupportedCount = allItems.filter((item) => !item.supports_editable).length;
  const readOnly = allItems.length > 0 && editableCount === 0 && readOnlyCount > 0;
  return { url: location.href, title: document.title, selector, expected: "editable", editable: editableCount > 0, read_only: readOnly, passed: editableCount > 0, count: allItems.length, editable_count: editableCount, read_only_count: readOnlyCount, disabled_count: disabledCount, unsupported_count: unsupportedCount, items: allItems.slice(0, limit), marker };
})()`, jsStringLiteral(selector), limit)
}

func assertCheckedExpression(selector string, limit int) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_assert_checked__";
  const selector = %s;
  const limit = %d;
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
    if (el.id) {
      const label = Array.from(document.querySelectorAll("label[for]")).find((candidate) => candidate.getAttribute("for") === el.id);
      if (label) return norm(label.innerText || label.textContent);
    }
    const parent = el.closest("label");
    return parent ? norm(parent.innerText || parent.textContent) : norm(el.innerText || el.textContent || el.value || "");
  };
  const visibilityOf = (el) => {
    const style = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    const hidden = Boolean(el.hidden || el.closest("[hidden]") || style.display === "none" || style.visibility === "hidden");
    return { visible: !hidden && rect.width > 0 && rect.height > 0, rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } };
  };
  const stateOf = (el) => {
    const tag = el.tagName.toLowerCase();
    const type = String(el.getAttribute("type") || "").toLowerCase();
    const role = roleOf(el);
    const native = tag === "input" && (type === "checkbox" || type === "radio");
    const aria = !native && (role === "checkbox" || role === "switch" || role === "radio");
    const ariaChecked = String(el.getAttribute("aria-checked") || "").toLowerCase();
    if (!native && !aria) {
      return { supportsChecked: false, tag, type, role, checked: false, ariaChecked: ariaChecked || "" };
    }
    const checked = native ? Boolean(el.checked) : ariaChecked === "true";
    return { supportsChecked: true, tag, type, role, checked, ariaChecked: aria ? ariaChecked : "" };
  };
  const itemFor = (el, index) => {
    const state = stateOf(el);
    const visibility = visibilityOf(el);
    return {
      index,
      tag: state.tag,
      id: el.id || "",
      type: state.type,
      role: state.role,
      name: nameOf(el).slice(0, 240),
      checked: state.checked,
      supports_checked: state.supportsChecked,
      aria_checked: state.ariaChecked,
      visible: visibility.visible,
      rect: visibility.rect
    };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, expected: "checked", checked: false, unchecked: false, passed: false, count: 0, checked_count: 0, unchecked_count: 0, unsupported_count: 0, items: [], error: { name: error.name, message: error.message }, marker };
  }
  const allItems = elements.map(itemFor);
  const supported = allItems.filter((item) => item.supports_checked);
  const checkedCount = supported.filter((item) => item.checked).length;
  const uncheckedCount = supported.length - checkedCount;
  const unsupportedCount = allItems.length - supported.length;
  return {
    url: location.href,
    title: document.title,
    selector,
    expected: "checked",
    checked: checkedCount > 0,
    unchecked: supported.length > 0 && checkedCount === 0,
    passed: checkedCount > 0,
    count: allItems.length,
    checked_count: checkedCount,
    unchecked_count: uncheckedCount,
    unsupported_count: unsupportedCount,
    items: allItems.slice(0, limit),
    marker
  };
})()`, jsStringLiteral(selector), limit)
}

func formCollectorJS(selectorExpr, includeHiddenExpr string) string {
	return `(() => {
  try {
    const norm = (s) => String(s || '').replace(/\s+/g, ' ').trim();
    const selector = ` + selectorExpr + `;
    const includeHidden = Boolean(` + includeHiddenExpr + `);
    const isControl = (el) => el && (el.matches('input, textarea, select') || el.isContentEditable);
    const label = (el) => {
      const labelled = el.getAttribute('aria-label') || el.getAttribute('placeholder') || el.getAttribute('title') || '';
      if (labelled) return norm(labelled);
      if (el.id) {
        const l = document.querySelector('label[for="' + CSS.escape(el.id) + '"]');
        if (l) return norm(l.innerText || l.textContent);
      }
      const parent = el.closest('label');
      return parent ? norm(parent.innerText || parent.textContent) : '';
    };
    const visibleInfo = (el) => {
      const style = getComputedStyle(el);
      const rect = el.getBoundingClientRect();
      const ariaHidden = el.closest('[aria-hidden="true"]') !== null || el.getAttribute('aria-hidden') === 'true';
      const hidden = el.hidden || el.closest('[hidden]') !== null || style.display === 'none' || style.visibility === 'hidden' || Number(style.opacity) === 0 || ariaHidden;
      const hasBox = rect.width > 0 && rect.height > 0;
      const offscreenMeasure = Math.abs(rect.left) > 10000 || Math.abs(rect.top) > 10000;
      return { visible: !hidden && hasBox && !offscreenMeasure, ariaHidden, width: rect.width, height: rect.height };
    };
    const css = (el) => {
      const tag = el.tagName.toLowerCase();
      if (el.id) return tag + '#' + CSS.escape(el.id);
      const attrs = ['name', 'aria-label', 'placeholder', 'role'];
      for (const attr of attrs) {
        const value = el.getAttribute(attr);
        if (value) return tag + '[' + attr + '=' + JSON.stringify(value) + ']';
      }
      const sameTag = Array.from(document.querySelectorAll(tag));
      const index = sameTag.indexOf(el) + 1;
      return index > 0 ? tag + ':nth-of-type(' + index + ')' : tag;
    };
    const one = (el) => {
      const tag = el.tagName.toLowerCase();
      const selected = tag === 'select' ? Array.from(el.selectedOptions || []).map(o => o.value) : [];
      const checked = (tag === 'input' && /checkbox|radio/i.test(el.type)) ? Boolean(el.checked) : undefined;
      const value = tag === 'select' ? selected.join(',') : (el.isContentEditable ? norm(el.innerText || el.textContent) : String(el.value ?? el.getAttribute('value') ?? el.textContent ?? ''));
      const visibility = visibleInfo(el);
      const hint = css(el);
      const out = { selector_hint: hint, tag, type: el.type || '', role: el.getAttribute('role') || '', name: label(el), value: String(value), values: selected, visible: visibility.visible, aria_hidden: visibility.ariaHidden, read_only: Boolean(el.readOnly), disabled: Boolean(el.disabled), content_editable: Boolean(el.isContentEditable) };
      if (checked !== undefined) out.checked = checked;
      out.selector_ambiguous = document.querySelectorAll(hint).length !== 1;
      return out;
    };
    let nodes = [];
    if (selector) {
      const selected = Array.from(document.querySelectorAll(selector));
      nodes = selected.filter(isControl);
      if (nodes.length === 0) nodes = selected.flatMap(el => Array.from(el.querySelectorAll('input, textarea, select, [contenteditable=""], [contenteditable="true"]')));
    } else {
      nodes = Array.from(document.querySelectorAll('input, textarea, select, [contenteditable=""], [contenteditable="true"]'));
    }
    let controls = nodes.map(one);
    if (!includeHidden) controls = controls.filter(control => control.visible);
    return { url: location.href, title: document.title, selector: selector || '', count: controls.length, controls, control: controls[0] || null };
  } catch (e) {
    return { url: location.href, title: document.title, count: 0, controls: [], error: { name: e.name, message: e.message } };
  }
})()`
}
