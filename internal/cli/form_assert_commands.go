package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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
	Selector     string       `json:"selector"`
	Expected     string       `json:"expected"`
	Actual       string       `json:"actual"`
	Mode         string       `json:"mode"`
	Passed       bool         `json:"passed"`
	Count        int          `json:"count"`
	Control      *formControl `json:"control,omitempty"`
	Attempts     int          `json:"attempts,omitempty"`
	ElapsedMS    int64        `json:"elapsed_ms,omitempty"`
	PollInterval string       `json:"poll_interval,omitempty"`
	Error        *evalError   `json:"error,omitempty"`
}

type assertTextResult struct {
	Selector     string     `json:"selector,omitempty"`
	Expected     string     `json:"expected"`
	Actual       string     `json:"actual"`
	Mode         string     `json:"mode"`
	Passed       bool       `json:"passed"`
	Count        int        `json:"count"`
	Attempts     int        `json:"attempts,omitempty"`
	ElapsedMS    int64      `json:"elapsed_ms,omitempty"`
	PollInterval string     `json:"poll_interval,omitempty"`
	Error        *evalError `json:"error,omitempty"`
}

type assertPageResult struct {
	Field        string `json:"field"`
	Expected     string `json:"expected"`
	Actual       string `json:"actual"`
	Mode         string `json:"mode"`
	Passed       bool   `json:"passed"`
	URL          string `json:"url"`
	Title        string `json:"title"`
	Attempts     int    `json:"attempts,omitempty"`
	ElapsedMS    int64  `json:"elapsed_ms,omitempty"`
	PollInterval string `json:"poll_interval,omitempty"`
}

type assertPageInfo struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type assertCountResult struct {
	Query        string            `json:"query,omitempty"`
	Selector     string            `json:"selector,omitempty"`
	Expected     int               `json:"expected"`
	Actual       int               `json:"actual"`
	Passed       bool              `json:"passed"`
	Count        int               `json:"count"`
	Items        []assertCountItem `json:"items,omitempty"`
	Attempts     int               `json:"attempts,omitempty"`
	ElapsedMS    int64             `json:"elapsed_ms,omitempty"`
	PollInterval string            `json:"poll_interval,omitempty"`
	Error        *evalError        `json:"error,omitempty"`
}

type assertCountItem struct {
	Index int    `json:"index"`
	Tag   string `json:"tag"`
	ID    string `json:"id,omitempty"`
	Role  string `json:"role,omitempty"`
	Name  string `json:"name,omitempty"`
}

type assertAttributeResult struct {
	Selector         string     `json:"selector"`
	Attribute        string     `json:"attribute"`
	AttributePresent bool       `json:"attribute_present"`
	Expected         string     `json:"expected"`
	Actual           string     `json:"actual"`
	Mode             string     `json:"mode"`
	Passed           bool       `json:"passed"`
	Count            int        `json:"count"`
	Attempts         int        `json:"attempts,omitempty"`
	ElapsedMS        int64      `json:"elapsed_ms,omitempty"`
	PollInterval     string     `json:"poll_interval,omitempty"`
	Error            *evalError `json:"error,omitempty"`
}

type assertFocusedResult struct {
	Selector       string              `json:"selector"`
	Expected       string              `json:"expected"`
	Focused        bool                `json:"focused"`
	Passed         bool                `json:"passed"`
	Count          int                 `json:"count"`
	FocusedCount   int                 `json:"focused_count"`
	ActiveSelector string              `json:"active_selector,omitempty"`
	ActiveTag      string              `json:"active_tag,omitempty"`
	ActiveID       string              `json:"active_id,omitempty"`
	ActiveRole     string              `json:"active_role,omitempty"`
	ActiveName     string              `json:"active_name,omitempty"`
	Items          []assertFocusedItem `json:"items,omitempty"`
	Attempts       int                 `json:"attempts,omitempty"`
	ElapsedMS      int64               `json:"elapsed_ms,omitempty"`
	PollInterval   string              `json:"poll_interval,omitempty"`
	Error          *evalError          `json:"error,omitempty"`
}

type assertFocusedItem struct {
	Index   int          `json:"index"`
	Tag     string       `json:"tag"`
	ID      string       `json:"id,omitempty"`
	Role    string       `json:"role,omitempty"`
	Name    string       `json:"name,omitempty"`
	Focused bool         `json:"focused"`
	Visible bool         `json:"visible"`
	Rect    snapshotRect `json:"rect"`
}

type assertCSSResult struct {
	Selector     string     `json:"selector"`
	Property     string     `json:"property"`
	Expected     string     `json:"expected"`
	Actual       string     `json:"actual"`
	Mode         string     `json:"mode"`
	Passed       bool       `json:"passed"`
	Count        int        `json:"count"`
	Attempts     int        `json:"attempts,omitempty"`
	ElapsedMS    int64      `json:"elapsed_ms,omitempty"`
	PollInterval string     `json:"poll_interval,omitempty"`
	Error        *evalError `json:"error,omitempty"`
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
	Attempts     int                    `json:"attempts,omitempty"`
	ElapsedMS    int64                  `json:"elapsed_ms,omitempty"`
	PollInterval string                 `json:"poll_interval,omitempty"`
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
	Attempts      int                 `json:"attempts,omitempty"`
	ElapsedMS     int64               `json:"elapsed_ms,omitempty"`
	PollInterval  string              `json:"poll_interval,omitempty"`
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
	Attempts         int                  `json:"attempts,omitempty"`
	ElapsedMS        int64                `json:"elapsed_ms,omitempty"`
	PollInterval     string               `json:"poll_interval,omitempty"`
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
	cmd.AddCommand(a.newAssertURLCommand())
	cmd.AddCommand(a.newAssertTitleCommand())
	cmd.AddCommand(a.newAssertCountCommand())
	cmd.AddCommand(a.newAssertAttributeCommand())
	cmd.AddCommand(a.newAssertFocusedCommand())
	cmd.AddCommand(a.newAssertCSSCommand())
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
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "value <selector-or-locator> <expected>", Short: "Assert a form control value by CSS selector or strict locator", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertValueCommand(cmd, args[0], args[1], mode, locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "exact", "match mode: exact, contains, or regex")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertTextCommand() *cobra.Command {
	var targetID, urlContains, titleContains, mode string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "text [selector-or-locator] <expected>", Short: "Assert visible text by body, CSS selector, or strict locator", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 && locatorOptionsNeedQuery(locatorOpts) {
			return commandError("usage", "usage", "locator flags require both a locator query and expected text", ExitUsage, []string{"cdp assert text 'Saved successfully' --json", "cdp assert text 'Search' 'Search' --by role --role button --json"})
		}
		query := "body"
		expected := args[0]
		if len(args) == 2 {
			query = args[0]
			expected = args[1]
		}
		return a.runAssertTextCommand(cmd, query, expected, mode, locatorOpts, len(args) == 2, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "contains", "match mode: exact, contains, or regex")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func locatorOptionsNeedQuery(opts locatorActionOptions) bool {
	return opts.By != "css" || opts.Role != "" || opts.Exact || opts.IncludeHidden || opts.TestIDAttr != "data-testid" || opts.Limit != 20
}

func (a *app) runAssertValueCommand(cmd *cobra.Command, query, expected, mode string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert value 'Search' expected --by label --poll 250ms --json"})
	}
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, locator, selector, err := waitForValueAssertion(assertionCtx, session, query, expected, mode, locatorOpts, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if strings.TrimSpace(selector) != "" {
			report["resolved_selector"] = selector
		}
	}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("value assertion for %q did not pass before timeout: %v", query, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, valueAssertionRemediations(query, selector, locatorOpts), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func waitForValueAssertion(ctx context.Context, session *cdp.PageSession, query, expected, mode string, opts locatorActionOptions, poll time.Duration, start time.Time) (assertValueResult, *locatorFindResult, string, error) {
	attempts := 0
	last := assertValueResult{Selector: query, Expected: expected, Mode: normalizeAssertMode(mode), PollInterval: poll.String()}
	var lastLocator *locatorFindResult
	lastSelector := query
	for {
		attempts++
		selector := query
		var locator *locatorFindResult
		if opts.By != "css" {
			var result locatorFindResult
			if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "assert value locator", &result); err != nil {
				return last, lastLocator, lastSelector, err
			}
			locator = &result
			lastLocator = locator
			if result.Error != nil {
				return last, locator, "", commandError("invalid_locator", "usage", fmt.Sprintf("assert value locator %s %q: %s", opts.By, query, result.Error.Message), ExitUsage, locatorActionRemediations("assert value", query, opts))
			}
			if result.Count != 1 || len(result.Matches) != 1 || strings.TrimSpace(result.Matches[0].SelectorHint) == "" || result.Matches[0].SelectorAmbiguous {
				last = valueAssertionPendingResult(query, expected, mode, result.Count, attempts, start, poll)
				lastSelector = ""
				if done, err := waitForNextAssertionPoll(ctx, poll); done {
					return last, lastLocator, lastSelector, err
				}
				continue
			}
			selector = strings.TrimSpace(result.Matches[0].SelectorHint)
			lastSelector = selector
		}

		var got formGetResult
		if err := evaluateJSONValue(ctx, session, formGetExpression(selector), "assert value", &got); err != nil {
			return last, lastLocator, lastSelector, err
		}
		if got.Error != nil {
			return assertValueResultFromFormGet(selector, expected, "", mode, got, attempts, start, poll), locator, selector, invalidSelectorError(selector, got.Error, "cdp assert value 'input[name=q]' expected --json")
		}
		actual := ""
		if got.Control != nil {
			actual = got.Control.Value
		}
		passed, err := assertionMatch(actual, expected, mode)
		if err != nil {
			return last, lastLocator, lastSelector, err
		}
		if got.Count == 0 {
			passed = false
		}
		result := assertValueResultFromFormGet(selector, expected, actual, mode, got, attempts, start, poll)
		result.Passed = passed
		last = result
		lastLocator = locator
		lastSelector = selector
		if result.Passed {
			return result, locator, selector, nil
		}
		if done, err := waitForNextAssertionPoll(ctx, poll); done {
			return last, lastLocator, lastSelector, err
		}
	}
}

func assertValueResultFromFormGet(selector, expected, actual, mode string, got formGetResult, attempts int, start time.Time, poll time.Duration) assertValueResult {
	return assertValueResult{
		Selector:     selector,
		Expected:     expected,
		Actual:       actual,
		Mode:         normalizeAssertMode(mode),
		Passed:       false,
		Count:        got.Count,
		Control:      got.Control,
		Error:        got.Error,
		Attempts:     attempts,
		ElapsedMS:    time.Since(start).Milliseconds(),
		PollInterval: poll.String(),
	}
}

func valueAssertionPendingResult(query, expected, mode string, count, attempts int, start time.Time, poll time.Duration) assertValueResult {
	return assertValueResult{
		Selector:     query,
		Expected:     expected,
		Actual:       "",
		Mode:         normalizeAssertMode(mode),
		Passed:       false,
		Count:        count,
		Attempts:     attempts,
		ElapsedMS:    time.Since(start).Milliseconds(),
		PollInterval: poll.String(),
	}
}

func (a *app) runAssertTextCommand(cmd *cobra.Command, query, expected, mode string, locatorOpts locatorActionOptions, useLocatorQuery bool, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert text 'Search' 'Search' --by role --role button --poll 250ms --json"})
	}
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, locator, selector, err := waitForTextAssertion(assertionCtx, session, query, expected, mode, locatorOpts, useLocatorQuery, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if strings.TrimSpace(selector) != "" {
			report["resolved_selector"] = selector
		}
	}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("text assertion for %q did not pass before timeout: %v", query, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, textAssertionRemediations(query, selector, locatorOpts, useLocatorQuery), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func waitForTextAssertion(ctx context.Context, session *cdp.PageSession, query, expected, mode string, opts locatorActionOptions, useLocatorQuery bool, poll time.Duration, start time.Time) (assertTextResult, *locatorFindResult, string, error) {
	attempts := 0
	last := assertTextResult{Selector: query, Expected: expected, Mode: normalizeAssertMode(mode), PollInterval: poll.String()}
	var lastLocator *locatorFindResult
	lastSelector := query
	for {
		attempts++
		selector := query
		var locator *locatorFindResult
		if useLocatorQuery && opts.By != "css" {
			var result locatorFindResult
			if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "assert text locator", &result); err != nil {
				return last, lastLocator, lastSelector, err
			}
			locator = &result
			lastLocator = locator
			if result.Error != nil {
				return last, locator, "", commandError("invalid_locator", "usage", fmt.Sprintf("assert text locator %s %q: %s", opts.By, query, result.Error.Message), ExitUsage, locatorActionRemediations("assert text", query, opts))
			}
			if result.Count != 1 || len(result.Matches) != 1 || strings.TrimSpace(result.Matches[0].SelectorHint) == "" || result.Matches[0].SelectorAmbiguous {
				last = textAssertionPendingResult(query, expected, mode, result.Count, attempts, start, poll)
				lastSelector = ""
				if done, err := waitForNextAssertionPoll(ctx, poll); done {
					return last, lastLocator, lastSelector, err
				}
				continue
			}
			selector = strings.TrimSpace(result.Matches[0].SelectorHint)
			lastSelector = selector
		}

		var got textResult
		if err := evaluateJSONValue(ctx, session, textExpression(selector, 0, 1), "assert text", &got); err != nil {
			return last, lastLocator, lastSelector, err
		}
		if got.Error != nil {
			return assertTextResultFromText(selector, expected, mode, got, attempts, start, poll), locator, selector, invalidSelectorError(selector, got.Error, "cdp assert text expected --json")
		}
		passed, err := assertionMatch(got.Text, expected, mode)
		if err != nil {
			return last, lastLocator, lastSelector, err
		}
		if got.Count == 0 {
			passed = false
		}
		result := assertTextResultFromText(selector, expected, mode, got, attempts, start, poll)
		result.Passed = passed
		last = result
		lastLocator = locator
		lastSelector = selector
		if result.Passed {
			return result, locator, selector, nil
		}
		if done, err := waitForNextAssertionPoll(ctx, poll); done {
			return last, lastLocator, lastSelector, err
		}
	}
}

func assertTextResultFromText(selector, expected, mode string, got textResult, attempts int, start time.Time, poll time.Duration) assertTextResult {
	return assertTextResult{
		Selector:     selector,
		Expected:     expected,
		Actual:       got.Text,
		Mode:         normalizeAssertMode(mode),
		Passed:       false,
		Count:        got.Count,
		Error:        got.Error,
		Attempts:     attempts,
		ElapsedMS:    time.Since(start).Milliseconds(),
		PollInterval: poll.String(),
	}
}

func textAssertionPendingResult(query, expected, mode string, count, attempts int, start time.Time, poll time.Duration) assertTextResult {
	return assertTextResult{
		Selector:     query,
		Expected:     expected,
		Actual:       "",
		Mode:         normalizeAssertMode(mode),
		Passed:       false,
		Count:        count,
		Attempts:     attempts,
		ElapsedMS:    time.Since(start).Milliseconds(),
		PollInterval: poll.String(),
	}
}

func (a *app) newAssertURLCommand() *cobra.Command {
	var targetID, urlContains, titleContains, mode string
	var poll time.Duration
	cmd := &cobra.Command{Use: "url <expected>", Short: "Assert the current page URL with auto-retry", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertPageCommand(cmd, "url", args[0], mode, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "contains", "match mode: exact, contains, or regex")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	return cmd
}

func (a *app) newAssertTitleCommand() *cobra.Command {
	var targetID, urlContains, titleContains, mode string
	var poll time.Duration
	cmd := &cobra.Command{Use: "title <expected>", Short: "Assert the current page title with auto-retry", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertPageCommand(cmd, "title", args[0], mode, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "contains", "match mode: exact, contains, or regex")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	return cmd
}

func (a *app) runAssertPageCommand(cmd *cobra.Command, field, expected, mode, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert " + field + " expected --poll 250ms --json"})
	}
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, err := waitForPageAssertion(assertionCtx, session, field, expected, mode, poll, start)
	finalTarget := targetWithPageAssertion(target, got)
	report := map[string]any{"ok": got.Passed, "target": pageRow(finalTarget), "assertion": got}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("%s assertion for %q did not pass before timeout: %v", field, expected, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, pageAssertionRemediations(field, expected, mode), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func waitForPageAssertion(ctx context.Context, session *cdp.PageSession, field, expected, mode string, poll time.Duration, start time.Time) (assertPageResult, error) {
	attempts := 0
	normalizedMode := normalizeAssertMode(mode)
	last := assertPageResult{Field: field, Expected: expected, Mode: normalizedMode, PollInterval: poll.String()}
	for {
		attempts++
		var info assertPageInfo
		if err := evaluateJSONValue(ctx, session, pageInfoExpression(), "assert "+field, &info); err != nil {
			return last, err
		}
		actual := info.URL
		if field == "title" {
			actual = info.Title
		}
		passed, err := assertionMatch(actual, expected, normalizedMode)
		if err != nil {
			return last, err
		}
		last = assertPageResult{
			Field:        field,
			Expected:     expected,
			Actual:       actual,
			Mode:         normalizedMode,
			Passed:       passed,
			URL:          info.URL,
			Title:        info.Title,
			Attempts:     attempts,
			ElapsedMS:    time.Since(start).Milliseconds(),
			PollInterval: poll.String(),
		}
		if passed {
			return last, nil
		}
		if done, err := waitForNextAssertionPoll(ctx, poll); done {
			return last, err
		}
	}
}

func targetWithPageAssertion(target cdp.TargetInfo, result assertPageResult) cdp.TargetInfo {
	if result.URL != "" {
		target.URL = result.URL
	}
	if result.Attempts > 0 {
		target.Title = result.Title
	}
	return target
}

func pageAssertionRemediations(field, expected, mode string) []string {
	return []string{
		"cdp pages --json",
		"cdp assert " + field + " " + shellQuote(expected) + " --mode " + shellQuote(normalizeAssertMode(mode)) + " --json",
	}
}

func (a *app) newAssertCountCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "count <selector-or-locator> <expected-count>", Short: "Assert an exact element count by CSS selector or locator", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		expected, err := parseExpectedCount(args[1])
		if err != nil {
			return err
		}
		return a.runAssertCountCommand(cmd, args[0], expected, locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertAttributeCommand() *cobra.Command {
	var targetID, urlContains, titleContains, mode string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "attribute <selector-or-locator> <attribute> <expected>", Short: "Assert a DOM attribute by CSS selector or strict locator", Args: cobra.ExactArgs(3), RunE: func(cmd *cobra.Command, args []string) error {
		if !validLocatorAttributeName(args[1]) {
			return commandError("usage", "usage", "<attribute> must be a simple attribute name", ExitUsage, []string{"cdp assert attribute button data-state ready --json"})
		}
		return a.runAssertAttributeCommand(cmd, args[0], args[1], args[2], mode, locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "exact", "match mode: exact, contains, or regex")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertFocusedCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "focused <selector-or-locator>", Short: "Assert an element has focus by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertFocusedCommand(cmd, args[0], locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertCSSCommand() *cobra.Command {
	var targetID, urlContains, titleContains, mode string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "css <selector-or-locator> <property> <expected>", Short: "Assert a computed CSS property by CSS selector or strict locator", Args: cobra.ExactArgs(3), RunE: func(cmd *cobra.Command, args []string) error {
		property := strings.TrimSpace(args[1])
		if !validCSSPropertyName(property) {
			return commandError("usage", "usage", "<property> must be a simple CSS property name", ExitUsage, []string{"cdp assert css button background-color 'rgb(20, 92, 160)' --json"})
		}
		return a.runAssertCSSCommand(cmd, args[0], property, args[2], mode, locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "exact", "match mode: exact, contains, or regex")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func parseExpectedCount(value string) (int, error) {
	expected, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || expected < 0 {
		return 0, commandError("usage", "usage", "<expected-count> must be a non-negative integer", ExitUsage, []string{"cdp assert count '.result' 3 --json"})
	}
	return expected, nil
}

func validCSSPropertyName(value string) bool {
	return validLocatorAttributeName(value)
}

func (a *app) runAssertCountCommand(cmd *cobra.Command, query string, expected int, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert count '.result' 3 --poll 250ms --json"})
	}
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, locator, err := waitForCountAssertion(assertionCtx, session, query, expected, locatorOpts, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
	}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("count assertion for %q did not pass before timeout: %v", query, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, countAssertionRemediations(query, expected, locatorOpts), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func waitForCountAssertion(ctx context.Context, session *cdp.PageSession, query string, expected int, opts locatorActionOptions, poll time.Duration, start time.Time) (assertCountResult, *locatorFindResult, error) {
	attempts := 0
	last := assertCountResult{Query: query, Selector: query, Expected: expected, PollInterval: poll.String()}
	var lastLocator *locatorFindResult
	for {
		attempts++
		var result assertCountResult
		var locator *locatorFindResult
		if opts.By == "css" {
			var got assertCountProbeResult
			if err := evaluateJSONValue(ctx, session, assertCountExpression(query, 20), "assert count", &got); err != nil {
				return last, lastLocator, err
			}
			if got.Error != nil {
				result = assertCountResultFromProbe(query, expected, got, attempts, start, poll)
				return result, nil, invalidSelectorError(query, got.Error, "cdp assert count '.result' 3 --json")
			}
			result = assertCountResultFromProbe(query, expected, got, attempts, start, poll)
		} else {
			var got locatorFindResult
			if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "assert count locator", &got); err != nil {
				return last, lastLocator, err
			}
			locator = &got
			lastLocator = locator
			if got.Error != nil {
				return last, locator, commandError("invalid_locator", "usage", fmt.Sprintf("assert count locator %s %q: %s", opts.By, query, got.Error.Message), ExitUsage, countAssertionRemediations(query, expected, opts))
			}
			result = assertCountResultFromLocator(query, expected, got, attempts, start, poll)
		}
		result.Passed = result.Actual == expected
		last = result
		if result.Passed {
			return result, locator, nil
		}
		if done, err := waitForNextAssertionPoll(ctx, poll); done {
			return last, lastLocator, err
		}
	}
}

type assertCountProbeResult struct {
	URL      string            `json:"url"`
	Title    string            `json:"title"`
	Selector string            `json:"selector"`
	Count    int               `json:"count"`
	Items    []assertCountItem `json:"items,omitempty"`
	Error    *evalError        `json:"error,omitempty"`
}

func assertCountResultFromProbe(selector string, expected int, got assertCountProbeResult, attempts int, start time.Time, poll time.Duration) assertCountResult {
	return assertCountResult{
		Selector:     selector,
		Expected:     expected,
		Actual:       got.Count,
		Count:        got.Count,
		Items:        got.Items,
		Error:        got.Error,
		Attempts:     attempts,
		ElapsedMS:    time.Since(start).Milliseconds(),
		PollInterval: poll.String(),
	}
}

func assertCountResultFromLocator(query string, expected int, got locatorFindResult, attempts int, start time.Time, poll time.Duration) assertCountResult {
	return assertCountResult{
		Query:        query,
		Expected:     expected,
		Actual:       got.Count,
		Count:        got.Count,
		Items:        assertCountItemsFromLocator(got.Matches),
		Attempts:     attempts,
		ElapsedMS:    time.Since(start).Milliseconds(),
		PollInterval: poll.String(),
	}
}

func assertCountItemsFromLocator(matches []locatorMatch) []assertCountItem {
	if len(matches) == 0 {
		return nil
	}
	items := make([]assertCountItem, 0, len(matches))
	for _, match := range matches {
		items = append(items, assertCountItem{Index: match.Index, Tag: match.Tag, ID: locatorMatchID(match.SelectorHint), Role: match.Role, Name: match.Name})
	}
	return items
}

func locatorMatchID(selector string) string {
	parts := strings.Split(selector, "#")
	if len(parts) < 2 {
		return ""
	}
	id := parts[len(parts)-1]
	if idx := strings.IndexAny(id, " .[:>"); idx >= 0 {
		id = id[:idx]
	}
	return id
}

func (a *app) runAssertAttributeCommand(cmd *cobra.Command, query, attribute, expected, mode string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert attribute button data-state ready --poll 250ms --json"})
	}
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, locator, selector, err := waitForAttributeAssertion(assertionCtx, session, query, attribute, expected, mode, locatorOpts, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if strings.TrimSpace(selector) != "" {
			report["resolved_selector"] = selector
		}
	}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("attribute assertion for %q did not pass before timeout: %v", query, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, attributeAssertionRemediations(query, attribute, expected, mode, selector, locatorOpts), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func waitForAttributeAssertion(ctx context.Context, session *cdp.PageSession, query, attribute, expected, mode string, opts locatorActionOptions, poll time.Duration, start time.Time) (assertAttributeResult, *locatorFindResult, string, error) {
	attempts := 0
	normalizedMode := normalizeAssertMode(mode)
	last := assertAttributeResult{Selector: query, Attribute: attribute, Expected: expected, Mode: normalizedMode, PollInterval: poll.String()}
	var lastLocator *locatorFindResult
	lastSelector := query
	for {
		attempts++
		selector := query
		var locator *locatorFindResult
		if opts.By != "css" {
			var got locatorFindResult
			if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "assert attribute locator", &got); err != nil {
				return last, lastLocator, lastSelector, err
			}
			locator = &got
			lastLocator = locator
			if got.Error != nil {
				return last, locator, "", commandError("invalid_locator", "usage", fmt.Sprintf("assert attribute locator %s %q: %s", opts.By, query, got.Error.Message), ExitUsage, attributeAssertionRemediations(query, attribute, expected, normalizedMode, "", opts))
			}
			if got.Count != 1 || len(got.Matches) != 1 || strings.TrimSpace(got.Matches[0].SelectorHint) == "" || got.Matches[0].SelectorAmbiguous {
				last = attributeAssertionPendingResult(query, attribute, expected, normalizedMode, got.Count, attempts, start, poll)
				lastSelector = ""
				if done, err := waitForNextAssertionPoll(ctx, poll); done {
					return last, lastLocator, lastSelector, err
				}
				continue
			}
			selector = strings.TrimSpace(got.Matches[0].SelectorHint)
			lastSelector = selector
		}
		var got assertAttributeProbeResult
		if err := evaluateJSONValue(ctx, session, assertAttributeExpression(selector, attribute), "assert attribute", &got); err != nil {
			return last, lastLocator, lastSelector, err
		}
		result := assertAttributeResultFromProbe(selector, attribute, expected, normalizedMode, got, attempts, start, poll)
		if got.Error != nil {
			return result, locator, selector, invalidSelectorError(selector, got.Error, "cdp assert attribute button data-state ready --json")
		}
		passed, err := assertionMatch(got.Value, expected, normalizedMode)
		if err != nil {
			return last, lastLocator, lastSelector, err
		}
		result.Passed = got.Count == 1 && got.AttributePresent && passed
		last = result
		lastLocator = locator
		lastSelector = selector
		if result.Passed {
			return result, locator, selector, nil
		}
		if done, err := waitForNextAssertionPoll(ctx, poll); done {
			return last, lastLocator, lastSelector, err
		}
	}
}

type assertAttributeProbeResult struct {
	URL              string     `json:"url"`
	Title            string     `json:"title"`
	Selector         string     `json:"selector"`
	Attribute        string     `json:"attribute"`
	AttributePresent bool       `json:"attribute_present"`
	Value            string     `json:"value"`
	Count            int        `json:"count"`
	Error            *evalError `json:"error,omitempty"`
}

func assertAttributeResultFromProbe(selector, attribute, expected, mode string, got assertAttributeProbeResult, attempts int, start time.Time, poll time.Duration) assertAttributeResult {
	return assertAttributeResult{
		Selector:         selector,
		Attribute:        attribute,
		AttributePresent: got.AttributePresent,
		Expected:         expected,
		Actual:           got.Value,
		Mode:             normalizeAssertMode(mode),
		Count:            got.Count,
		Error:            got.Error,
		Attempts:         attempts,
		ElapsedMS:        time.Since(start).Milliseconds(),
		PollInterval:     poll.String(),
	}
}

func attributeAssertionPendingResult(query, attribute, expected, mode string, count, attempts int, start time.Time, poll time.Duration) assertAttributeResult {
	return assertAttributeResult{
		Selector:         query,
		Attribute:        attribute,
		AttributePresent: false,
		Expected:         expected,
		Actual:           "",
		Mode:             normalizeAssertMode(mode),
		Passed:           false,
		Count:            count,
		Attempts:         attempts,
		ElapsedMS:        time.Since(start).Milliseconds(),
		PollInterval:     poll.String(),
	}
}

func (a *app) runAssertFocusedCommand(cmd *cobra.Command, query string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert focused 'Search' --by label --poll 250ms --json"})
	}
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, locator, selector, err := waitForFocusedAssertion(assertionCtx, session, query, locatorOpts, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if strings.TrimSpace(selector) != "" {
			report["resolved_selector"] = selector
		}
	}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("focused assertion for %q did not pass before timeout: %v", query, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, focusedAssertionRemediations(query, selector, locatorOpts), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func waitForFocusedAssertion(ctx context.Context, session *cdp.PageSession, query string, opts locatorActionOptions, poll time.Duration, start time.Time) (assertFocusedResult, *locatorFindResult, string, error) {
	attempts := 0
	last := assertFocusedResult{Selector: query, Expected: "focused", PollInterval: poll.String()}
	var lastLocator *locatorFindResult
	lastSelector := query
	for {
		attempts++
		selector := query
		var locator *locatorFindResult
		if opts.By != "css" {
			var result locatorFindResult
			if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "assert focused locator", &result); err != nil {
				return last, lastLocator, lastSelector, err
			}
			locator = &result
			lastLocator = locator
			if result.Error != nil {
				return last, locator, "", commandError("invalid_locator", "usage", fmt.Sprintf("assert focused locator %s %q: %s", opts.By, query, result.Error.Message), ExitUsage, locatorActionRemediations("assert focused", query, opts))
			}
			if result.Count != 1 || len(result.Matches) != 1 || strings.TrimSpace(result.Matches[0].SelectorHint) == "" || result.Matches[0].SelectorAmbiguous {
				last = focusedAssertionPendingResult(query, result.Count, attempts, start, poll)
				lastSelector = ""
				if done, err := waitForNextAssertionPoll(ctx, poll); done {
					return last, lastLocator, lastSelector, err
				}
				continue
			}
			selector = strings.TrimSpace(result.Matches[0].SelectorHint)
			lastSelector = selector
		}

		var got assertFocusedResult
		if err := evaluateJSONValue(ctx, session, assertFocusedExpression(selector, 20), "assert focused", &got); err != nil {
			return last, lastLocator, lastSelector, err
		}
		if got.Error != nil {
			return got, locator, selector, invalidSelectorError(selector, got.Error, "cdp assert focused 'input[name=q]' --json")
		}
		finishFocusedAssertionResult(&got, attempts, start, poll)
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

func finishFocusedAssertionResult(got *assertFocusedResult, attempts int, start time.Time, poll time.Duration) {
	got.Expected = "focused"
	got.Passed = got.Focused
	got.Attempts = attempts
	got.ElapsedMS = time.Since(start).Milliseconds()
	got.PollInterval = poll.String()
}

func focusedAssertionPendingResult(query string, count, attempts int, start time.Time, poll time.Duration) assertFocusedResult {
	return assertFocusedResult{
		Selector:     query,
		Expected:     "focused",
		Focused:      false,
		Passed:       false,
		Count:        count,
		FocusedCount: 0,
		Attempts:     attempts,
		ElapsedMS:    time.Since(start).Milliseconds(),
		PollInterval: poll.String(),
	}
}

func (a *app) runAssertCSSCommand(cmd *cobra.Command, query, property, expected, mode string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert css button background-color 'rgb(20, 92, 160)' --poll 250ms --json"})
	}
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, locator, selector, err := waitForCSSAssertion(assertionCtx, session, query, property, expected, mode, locatorOpts, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if strings.TrimSpace(selector) != "" {
			report["resolved_selector"] = selector
		}
	}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("CSS assertion for %q did not pass before timeout: %v", query, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, cssAssertionRemediations(query, property, expected, mode, selector, locatorOpts), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func waitForCSSAssertion(ctx context.Context, session *cdp.PageSession, query, property, expected, mode string, opts locatorActionOptions, poll time.Duration, start time.Time) (assertCSSResult, *locatorFindResult, string, error) {
	attempts := 0
	normalizedMode := normalizeAssertMode(mode)
	last := assertCSSResult{Selector: query, Property: property, Expected: expected, Mode: normalizedMode, PollInterval: poll.String()}
	var lastLocator *locatorFindResult
	lastSelector := query
	for {
		attempts++
		selector := query
		var locator *locatorFindResult
		if opts.By != "css" {
			var result locatorFindResult
			if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "assert css locator", &result); err != nil {
				return last, lastLocator, lastSelector, err
			}
			locator = &result
			lastLocator = locator
			if result.Error != nil {
				return last, locator, "", commandError("invalid_locator", "usage", fmt.Sprintf("assert css locator %s %q: %s", opts.By, query, result.Error.Message), ExitUsage, cssAssertionRemediations(query, property, expected, normalizedMode, "", opts))
			}
			if result.Count != 1 || len(result.Matches) != 1 || strings.TrimSpace(result.Matches[0].SelectorHint) == "" || result.Matches[0].SelectorAmbiguous {
				last = cssAssertionPendingResult(query, property, expected, normalizedMode, result.Count, attempts, start, poll)
				lastSelector = ""
				if done, err := waitForNextAssertionPoll(ctx, poll); done {
					return last, lastLocator, lastSelector, err
				}
				continue
			}
			selector = strings.TrimSpace(result.Matches[0].SelectorHint)
			lastSelector = selector
		}
		var got assertCSSResult
		if err := evaluateJSONValue(ctx, session, assertCSSExpression(selector, property), "assert css", &got); err != nil {
			return last, lastLocator, lastSelector, err
		}
		result := assertCSSResultFromProbe(selector, property, expected, normalizedMode, got, attempts, start, poll)
		if got.Error != nil {
			return result, locator, selector, invalidSelectorError(selector, got.Error, "cdp assert css button color blue --json")
		}
		passed, err := assertionMatch(got.Actual, expected, normalizedMode)
		if err != nil {
			return last, lastLocator, lastSelector, err
		}
		result.Passed = got.Count == 1 && passed
		last = result
		lastLocator = locator
		lastSelector = selector
		if result.Passed {
			return result, locator, selector, nil
		}
		if done, err := waitForNextAssertionPoll(ctx, poll); done {
			return last, lastLocator, lastSelector, err
		}
	}
}

func assertCSSResultFromProbe(selector, property, expected, mode string, got assertCSSResult, attempts int, start time.Time, poll time.Duration) assertCSSResult {
	return assertCSSResult{
		Selector:     selector,
		Property:     property,
		Expected:     expected,
		Actual:       got.Actual,
		Mode:         normalizeAssertMode(mode),
		Passed:       false,
		Count:        got.Count,
		Error:        got.Error,
		Attempts:     attempts,
		ElapsedMS:    time.Since(start).Milliseconds(),
		PollInterval: poll.String(),
	}
}

func cssAssertionPendingResult(query, property, expected, mode string, count, attempts int, start time.Time, poll time.Duration) assertCSSResult {
	return assertCSSResult{
		Selector:     query,
		Property:     property,
		Expected:     expected,
		Actual:       "",
		Mode:         normalizeAssertMode(mode),
		Passed:       false,
		Count:        count,
		Attempts:     attempts,
		ElapsedMS:    time.Since(start).Milliseconds(),
		PollInterval: poll.String(),
	}
}

func (a *app) newAssertVisibleCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "visible <selector-or-locator>", Short: "Assert an element is visible by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertVisibilityCommand(cmd, args[0], "visible", locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertHiddenCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "hidden <selector-or-locator>", Short: "Assert an element is hidden or absent by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertVisibilityCommand(cmd, args[0], "hidden", locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertEnabledCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "enabled <selector-or-locator>", Short: "Assert an element is enabled by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertEnabledCommand(cmd, args[0], "enabled", locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertDisabledCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "disabled <selector-or-locator>", Short: "Assert an element is disabled by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertEnabledCommand(cmd, args[0], "disabled", locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertEditableCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "editable <selector-or-locator>", Short: "Assert an element is editable by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertEditableCommand(cmd, args[0], "editable", locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newAssertReadonlyCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var poll time.Duration
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{Use: "readonly <selector-or-locator>", Aliases: []string{"read-only"}, Short: "Assert an element is read-only by CSS selector or strict locator", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAssertEditableCommand(cmd, args[0], "readonly", locatorOpts, targetID, urlContains, titleContains, poll)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while retrying the assertion")
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
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, locator, selector, err := waitForCheckedAssertion(assertionCtx, session, query, expected, locatorOpts, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if strings.TrimSpace(selector) != "" {
			report["resolved_selector"] = selector
		}
	}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("%s assertion for %q did not pass before timeout: %v", expected, query, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, checkedAssertionRemediations(query, selector, locatorOpts), report)
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

func (a *app) runAssertEditableCommand(cmd *cobra.Command, query, expected string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert editable 'Search' --by label --poll 250ms --json"})
	}
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, locator, selector, err := waitForEditableAssertion(assertionCtx, session, query, expected, locatorOpts, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if strings.TrimSpace(selector) != "" {
			report["resolved_selector"] = selector
		}
	}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("%s assertion for %q did not pass before timeout: %v", expected, query, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, editableAssertionRemediations(query, selector, locatorOpts), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func waitForEditableAssertion(ctx context.Context, session *cdp.PageSession, query, expected string, opts locatorActionOptions, poll time.Duration, start time.Time) (assertEditableResult, *locatorFindResult, string, error) {
	attempts := 0
	last := assertEditableResult{Selector: query, Expected: expected, PollInterval: poll.String()}
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
				last = editableAssertionPendingResult(query, expected, result.Count, attempts, start, poll)
				lastSelector = ""
				if done, err := waitForNextAssertionPoll(ctx, poll); done {
					return last, lastLocator, lastSelector, err
				}
				continue
			}
			selector = strings.TrimSpace(result.Matches[0].SelectorHint)
			lastSelector = selector
		}

		var got assertEditableResult
		if err := evaluateJSONValue(ctx, session, assertEditableExpression(selector, 20), "assert "+expected, &got); err != nil {
			return last, lastLocator, lastSelector, err
		}
		if got.Error != nil {
			return got, locator, selector, invalidSelectorError(selector, got.Error, "cdp assert "+expected+" 'input[name=q]' --json")
		}
		finishEditableAssertionResult(&got, expected, attempts, start, poll)
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

func finishEditableAssertionResult(got *assertEditableResult, expected string, attempts int, start time.Time, poll time.Duration) {
	got.Expected = expected
	got.Passed = got.Editable
	if expected == "readonly" {
		got.Passed = got.ReadOnly
	}
	got.Attempts = attempts
	got.ElapsedMS = time.Since(start).Milliseconds()
	got.PollInterval = poll.String()
}

func editableAssertionPendingResult(query, expected string, count, attempts int, start time.Time, poll time.Duration) assertEditableResult {
	return assertEditableResult{
		Selector:         query,
		Expected:         expected,
		Editable:         false,
		ReadOnly:         false,
		Passed:           false,
		Count:            count,
		EditableCount:    0,
		ReadOnlyCount:    0,
		DisabledCount:    0,
		UnsupportedCount: 0,
		Attempts:         attempts,
		ElapsedMS:        time.Since(start).Milliseconds(),
		PollInterval:     poll.String(),
	}
}

func (a *app) runAssertEnabledCommand(cmd *cobra.Command, query, expected string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert enabled 'Search' --by role --role button --poll 250ms --json"})
	}
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
	defer cancel()
	session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, locator, selector, err := waitForEnabledAssertion(assertionCtx, session, query, expected, locatorOpts, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if strings.TrimSpace(selector) != "" {
			report["resolved_selector"] = selector
		}
	}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("%s assertion for %q did not pass before timeout: %v", expected, query, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, enabledAssertionRemediations(query, selector, locatorOpts), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func waitForEnabledAssertion(ctx context.Context, session *cdp.PageSession, query, expected string, opts locatorActionOptions, poll time.Duration, start time.Time) (assertEnabledResult, *locatorFindResult, string, error) {
	attempts := 0
	last := assertEnabledResult{Selector: query, Expected: expected, PollInterval: poll.String()}
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
				last = enabledAssertionPendingResult(query, expected, result.Count, attempts, start, poll)
				lastSelector = ""
				if done, err := waitForNextAssertionPoll(ctx, poll); done {
					return last, lastLocator, lastSelector, err
				}
				continue
			}
			selector = strings.TrimSpace(result.Matches[0].SelectorHint)
			lastSelector = selector
		}

		var got assertEnabledResult
		if err := evaluateJSONValue(ctx, session, assertEnabledExpression(selector, 20), "assert "+expected, &got); err != nil {
			return last, lastLocator, lastSelector, err
		}
		if got.Error != nil {
			return got, locator, selector, invalidSelectorError(selector, got.Error, "cdp assert "+expected+" 'button[type=submit]' --json")
		}
		finishEnabledAssertionResult(&got, expected, attempts, start, poll)
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

func finishEnabledAssertionResult(got *assertEnabledResult, expected string, attempts int, start time.Time, poll time.Duration) {
	got.Expected = expected
	got.Passed = got.Enabled
	if expected == "disabled" {
		got.Passed = got.Disabled
	}
	got.Attempts = attempts
	got.ElapsedMS = time.Since(start).Milliseconds()
	got.PollInterval = poll.String()
}

func enabledAssertionPendingResult(query, expected string, count, attempts int, start time.Time, poll time.Duration) assertEnabledResult {
	return assertEnabledResult{
		Selector:      query,
		Expected:      expected,
		Enabled:       false,
		Disabled:      false,
		Passed:        false,
		Count:         count,
		EnabledCount:  0,
		DisabledCount: 0,
		Attempts:      attempts,
		ElapsedMS:     time.Since(start).Milliseconds(),
		PollInterval:  poll.String(),
	}
}

func (a *app) runAssertVisibilityCommand(cmd *cobra.Command, query, expected string, locatorOpts locatorActionOptions, targetID, urlContains, titleContains string, poll time.Duration) error {
	if poll <= 0 {
		return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp assert visible 'Search' --by role --role button --poll 250ms --json"})
	}
	if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
		return err
	}
	ctx, cancel, assertionTimeout := a.retryingAssertionCommandContext(cmd, 5*time.Second)
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

	assertionCtx, assertionCancel := context.WithTimeout(ctx, assertionTimeout)
	defer assertionCancel()
	start := time.Now()
	got, locator, selector, err := waitForVisibilityAssertion(assertionCtx, session, query, expected, resolveOpts, poll, start)
	report := map[string]any{"ok": got.Passed, "target": pageRow(target), "assertion": got}
	if locator != nil {
		report["locator"] = locator
		if selector != "" {
			report["resolved_selector"] = selector
		}
	}
	if err != nil {
		if assertionCtx.Err() != nil || isTimeoutCommandError(err) {
			return commandErrorWithData("timeout", "timeout", fmt.Sprintf("%s assertion for %q did not pass before timeout: %v", expected, query, assertionTimeoutCause(assertionCtx, err)), ExitTimeout, visibilityAssertionRemediations(query, selector, resolveOpts), report)
		}
		return err
	}
	return a.render(ctx, "assertion passed", report)
}

func (a *app) retryingAssertionCommandContext(cmd *cobra.Command, fallback time.Duration) (context.Context, context.CancelFunc, time.Duration) {
	timeout := a.opts.timeout
	if timeout <= 0 {
		timeout = fallback
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout+10*time.Second)
	return ctx, cancel, timeout
}

func isTimeoutCommandError(err error) bool {
	var cmdErr *CommandError
	return errors.As(err, &cmdErr) && cmdErr.Code == "timeout"
}

func assertionTimeoutCause(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func waitForVisibilityAssertion(ctx context.Context, session *cdp.PageSession, query, expected string, opts locatorActionOptions, poll time.Duration, start time.Time) (assertVisibilityResult, *locatorFindResult, string, error) {
	attempts := 0
	last := assertVisibilityResult{Selector: query, Expected: expected, PollInterval: poll.String()}
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
			if expected == "hidden" && result.Count == 0 {
				got := assertVisibilityResult{Selector: query, Expected: "hidden", Visible: false, Hidden: true, Passed: true, Count: 0, VisibleCount: 0, HiddenCount: 0}
				finishVisibilityAssertionResult(&got, expected, attempts, start, poll)
				return got, locator, "", nil
			}
			if result.Count != 1 || len(result.Matches) != 1 || strings.TrimSpace(result.Matches[0].SelectorHint) == "" || result.Matches[0].SelectorAmbiguous {
				if result.Count > 1 || (result.Count == 1 && len(result.Matches) == 1 && result.Matches[0].SelectorAmbiguous) {
					return last, locator, "", commandError("ambiguous_locator", "usage", fmt.Sprintf("assert %s locator %s %q matched %d elements; refine the locator before asserting", expected, opts.By, query, result.Count), ExitUsage, locatorActionRemediations("assert "+expected, query, opts))
				}
				last = visibilityAssertionPendingResult(query, expected, result.Count, attempts, start, poll)
				lastSelector = ""
				if done, err := waitForNextAssertionPoll(ctx, poll); done {
					return last, lastLocator, lastSelector, err
				}
				continue
			}
			selector = strings.TrimSpace(result.Matches[0].SelectorHint)
			lastSelector = selector
		}

		var got assertVisibilityResult
		if err := evaluateJSONValue(ctx, session, assertVisibilityExpression(selector, 20), "assert "+expected, &got); err != nil {
			return last, lastLocator, lastSelector, err
		}
		if got.Error != nil {
			return got, locator, selector, invalidSelectorError(selector, got.Error, "cdp assert "+expected+" 'button[type=submit]' --json")
		}
		finishVisibilityAssertionResult(&got, expected, attempts, start, poll)
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

func finishVisibilityAssertionResult(got *assertVisibilityResult, expected string, attempts int, start time.Time, poll time.Duration) {
	got.Expected = expected
	got.Hidden = got.VisibleCount == 0
	got.Passed = got.Visible
	if expected == "hidden" {
		got.Passed = got.Hidden
	}
	got.Attempts = attempts
	got.ElapsedMS = time.Since(start).Milliseconds()
	got.PollInterval = poll.String()
}

func visibilityAssertionPendingResult(query, expected string, count, attempts int, start time.Time, poll time.Duration) assertVisibilityResult {
	return assertVisibilityResult{
		Selector:     query,
		Expected:     expected,
		Visible:      false,
		Hidden:       count == 0,
		Passed:       false,
		Count:        count,
		VisibleCount: 0,
		HiddenCount:  0,
		Attempts:     attempts,
		ElapsedMS:    time.Since(start).Milliseconds(),
		PollInterval: poll.String(),
	}
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

func valueAssertionRemediations(query, selector string, opts locatorActionOptions) []string {
	commands := []string{}
	if opts.By != "css" {
		commands = append(commands, locatorActionFindCommand(query, opts))
	}
	if strings.TrimSpace(selector) == "" {
		selector = query
	}
	commands = append(commands, "cdp form get "+shellQuote(selector)+" --json")
	return commands
}

func textAssertionRemediations(query, selector string, opts locatorActionOptions, useLocatorQuery bool) []string {
	commands := []string{}
	if useLocatorQuery && opts.By != "css" {
		commands = append(commands, locatorActionFindCommand(query, opts))
	}
	if strings.TrimSpace(selector) == "" {
		selector = query
	}
	commands = append(commands, "cdp text "+shellQuote(selector)+" --limit 0 --json")
	return commands
}

func enabledAssertionFailureMessage(expected, selector string, got assertEnabledResult) string {
	return fmt.Sprintf("%s assertion failed for %q: %d enabled and %d disabled of %d matched", expected, selector, got.EnabledCount, got.DisabledCount, got.Count)
}

func enabledAssertionRemediations(query, selector string, opts locatorActionOptions) []string {
	if selector == "" {
		selector = query
	}
	return []string{locatorActionFindCommand(query, opts), "cdp dom query " + shellQuote(selector) + " --json"}
}

func editableAssertionFailureMessage(expected, selector string, got assertEditableResult) string {
	return fmt.Sprintf("%s assertion failed for %q: %d editable, %d read-only, %d disabled, and %d unsupported of %d matched", expected, selector, got.EditableCount, got.ReadOnlyCount, got.DisabledCount, got.UnsupportedCount, got.Count)
}

func editableAssertionRemediations(query, selector string, opts locatorActionOptions) []string {
	if selector == "" {
		selector = query
	}
	return []string{locatorActionFindCommand(query, opts), "cdp dom query " + shellQuote(selector) + " --json"}
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

func countAssertionRemediations(query string, expected int, opts locatorActionOptions) []string {
	if opts.By != "css" {
		return []string{locatorActionFindCommand(query, opts), countAssertionCommand(query, expected, opts)}
	}
	return []string{"cdp dom query " + shellQuote(query) + " --json", countAssertionCommand(query, expected, opts)}
}

func countAssertionCommand(query string, expected int, opts locatorActionOptions) string {
	command := "cdp assert count " + shellQuote(query) + " " + strconv.Itoa(expected)
	return command + locatorAssertionFlagSuffix(opts) + " --json"
}

func attributeAssertionRemediations(query, attribute, expected, mode, selector string, opts locatorActionOptions) []string {
	commands := []string{}
	if opts.By != "css" {
		commands = append(commands, locatorActionFindCommand(query, opts))
	}
	if strings.TrimSpace(selector) == "" {
		selector = query
	}
	commands = append(commands, "cdp dom query "+shellQuote(selector)+" --json")
	commands = append(commands, attributeAssertionCommand(query, attribute, expected, mode, opts))
	return commands
}

func attributeAssertionCommand(query, attribute, expected, mode string, opts locatorActionOptions) string {
	command := "cdp assert attribute " + shellQuote(query) + " " + shellQuote(attribute) + " " + shellQuote(expected) + " --mode " + shellQuote(normalizeAssertMode(mode))
	return command + locatorAssertionFlagSuffix(opts) + " --json"
}

func focusedAssertionRemediations(query, selector string, opts locatorActionOptions) []string {
	commands := []string{}
	if opts.By != "css" {
		commands = append(commands, locatorActionFindCommand(query, opts))
	}
	if strings.TrimSpace(selector) == "" {
		selector = query
	}
	commands = append(commands, "cdp dom query "+shellQuote(selector)+" --json")
	commands = append(commands, focusedAssertionCommand(query, opts))
	return commands
}

func focusedAssertionCommand(query string, opts locatorActionOptions) string {
	command := "cdp assert focused " + shellQuote(query)
	return command + locatorAssertionFlagSuffix(opts) + " --json"
}

func cssAssertionRemediations(query, property, expected, mode, selector string, opts locatorActionOptions) []string {
	commands := []string{}
	if opts.By != "css" {
		commands = append(commands, locatorActionFindCommand(query, opts))
	}
	if strings.TrimSpace(selector) == "" {
		selector = query
	}
	commands = append(commands, "cdp css inspect "+shellQuote(selector)+" --json")
	commands = append(commands, cssAssertionCommand(query, property, expected, mode, opts))
	return commands
}

func cssAssertionCommand(query, property, expected, mode string, opts locatorActionOptions) string {
	command := "cdp assert css " + shellQuote(query) + " " + shellQuote(property) + " " + shellQuote(expected) + " --mode " + shellQuote(normalizeAssertMode(mode))
	return command + locatorAssertionFlagSuffix(opts) + " --json"
}

func locatorAssertionFlagSuffix(opts locatorActionOptions) string {
	if opts.By == "css" {
		return ""
	}
	suffix := " --by " + opts.By
	if opts.By == "role" {
		suffix += " --role " + shellQuote(opts.Role)
	}
	if opts.Exact {
		suffix += " --exact"
	}
	if opts.IncludeHidden {
		suffix += " --include-hidden"
	}
	if opts.By == "test-id" && opts.TestIDAttr != "data-testid" {
		suffix += " --test-id-attr " + shellQuote(opts.TestIDAttr)
	}
	return suffix
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

func pageInfoExpression() string {
	return `(() => {
  const marker = "__cdp_cli_page_assertion__";
  return { url: String(location.href || ""), title: String(document.title || ""), marker };
})()`
}

func assertCountExpression(selector string, limit int) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_assert_count__";
  const selector = %s;
  const limit = %d;
  const norm = (value) => String(value || "").replace(/\s+/g, " ").trim();
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, count: 0, items: [], error: { name: error.name, message: error.message }, marker };
  }
  const items = elements.slice(0, limit).map((el, index) => ({
    index,
    tag: el.tagName.toLowerCase(),
    id: el.id || "",
    role: norm(el.getAttribute("role") || ""),
    name: norm(el.getAttribute("aria-label") || el.getAttribute("title") || el.getAttribute("alt") || el.textContent || "").slice(0, 240)
  }));
  return { url: location.href, title: document.title, selector, count: elements.length, items, marker };
})()`, jsStringLiteral(selector), limit)
}

func assertAttributeExpression(selector, attribute string) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_assert_attribute__";
  const selector = %s;
  const attributeName = %s;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, attribute: attributeName, attribute_present: false, value: "", count: 0, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length !== 1) {
    return { url: location.href, title: document.title, selector, attribute: attributeName, attribute_present: false, value: "", count: elements.length, marker };
  }
  const element = elements[0];
  const present = element.hasAttribute(attributeName);
  return { url: location.href, title: document.title, selector, attribute: attributeName, attribute_present: present, value: present ? String(element.getAttribute(attributeName) || "") : "", count: elements.length, marker };
})()`, jsStringLiteral(selector), jsStringLiteral(attribute))
}

func assertFocusedExpression(selector string, limit int) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_assert_focused__";
  const selector = %s;
  const limit = %d;
  const norm = (value) => String(value || "").replace(/\s+/g, " ").trim();
  const cssEscape = (value) => {
    if (globalThis.CSS && typeof CSS.escape === "function") return CSS.escape(String(value));
    return String(value).replace(/[^a-zA-Z0-9_-]/g, (ch) => "\\" + ch);
  };
  const roleOf = (el) => {
    if (!el || !el.tagName) return "";
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
  const nameOf = (el) => {
    if (!el || !el.getAttribute) return "";
    return norm(el.getAttribute("aria-label") || el.getAttribute("alt") || el.getAttribute("title") || el.getAttribute("placeholder") || el.getAttribute("value") || el.innerText || el.textContent || "");
  };
  const selectorOf = (el) => {
    if (!el || !el.tagName) return "";
    const tag = el.tagName.toLowerCase();
    if (el.id) return tag + "#" + cssEscape(el.id);
    const attrs = ["name", "aria-label", "data-testid", "role"];
    for (const attr of attrs) {
      const value = el.getAttribute(attr);
      if (value) return tag + "[" + attr + "=" + JSON.stringify(value) + "]";
    }
    const sameTag = Array.from(document.querySelectorAll(tag));
    const index = sameTag.indexOf(el) + 1;
    return index > 0 ? tag + ":nth-of-type(" + index + ")" : tag;
  };
  const visibilityOf = (el) => {
    const style = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    const hidden = Boolean(el.hidden || el.closest("[hidden]") || style.display === "none" || style.visibility === "hidden");
    return { visible: !hidden && rect.width > 0 && rect.height > 0, rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, expected: "focused", focused: false, passed: false, count: 0, focused_count: 0, items: [], error: { name: error.name, message: error.message }, marker };
  }
  const active = document.activeElement;
  const allItems = elements.map((el, index) => {
    const visibility = visibilityOf(el);
    return {
      index,
      tag: el.tagName.toLowerCase(),
      id: el.id || "",
      role: roleOf(el),
      name: nameOf(el).slice(0, 240),
      focused: el === active,
      visible: visibility.visible,
      rect: visibility.rect
    };
  });
  const focusedCount = allItems.filter((item) => item.focused).length;
  return {
    url: location.href,
    title: document.title,
    selector,
    expected: "focused",
    focused: focusedCount > 0,
    passed: focusedCount > 0,
    count: allItems.length,
    focused_count: focusedCount,
    active_selector: selectorOf(active),
    active_tag: active && active.tagName ? active.tagName.toLowerCase() : "",
    active_id: active && active.id ? active.id : "",
    active_role: roleOf(active),
    active_name: nameOf(active).slice(0, 240),
    items: allItems.slice(0, limit),
    marker
  };
})()`, jsStringLiteral(selector), limit)
}

func assertCSSExpression(selector, property string) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_assert_css__";
  const selector = %s;
  const propertyName = %s;
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { url: location.href, title: document.title, selector, property: propertyName, value: "", actual: "", count: 0, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length !== 1) {
    return { url: location.href, title: document.title, selector, property: propertyName, value: "", actual: "", count: elements.length, marker };
  }
  const computed = getComputedStyle(elements[0]);
  let value = computed.getPropertyValue(propertyName);
  if (value === "" && propertyName in computed) {
    value = String(computed[propertyName] || "");
  }
  value = String(value || "").trim();
  return { url: location.href, title: document.title, selector, property: propertyName, value, actual: value, count: elements.length, marker };
})()`, jsStringLiteral(selector), jsStringLiteral(property))
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
