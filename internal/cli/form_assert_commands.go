package cli

import (
	"fmt"
	"regexp"
	"strings"

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
	Value             string   `json:"value,omitempty"`
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
	Expected string     `json:"expected"`
	Actual   string     `json:"actual"`
	Mode     string     `json:"mode"`
	Passed   bool       `json:"passed"`
	Count    int        `json:"count"`
	Error    *evalError `json:"error,omitempty"`
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
	return cmd
}

func (a *app) newAssertValueCommand() *cobra.Command {
	var targetID, urlContains, titleContains, mode string
	cmd := &cobra.Command{Use: "value <selector> <expected>", Short: "Assert a form control value", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		var got formGetResult
		if err := evaluateJSONValue(ctx, session, formGetExpression(args[0]), "assert value", &got); err != nil {
			return err
		}
		if got.Error != nil {
			return invalidSelectorError(args[0], got.Error, "cdp assert value 'input[name=q]' expected --json")
		}
		actual := ""
		if got.Control != nil {
			actual = got.Control.Value
		}
		passed, err := assertionMatch(actual, args[1], mode)
		if err != nil {
			return err
		}
		result := assertValueResult{Selector: args[0], Expected: args[1], Actual: actual, Mode: normalizeAssertMode(mode), Passed: passed, Count: got.Count, Control: got.Control, Error: got.Error}
		report := map[string]any{"ok": passed, "target": pageRow(target), "assertion": result}
		if !passed {
			return commandErrorWithData("assertion_failed", "check_failed", fmt.Sprintf("value assertion failed for %q: got %q", args[0], actual), ExitCheckFailed, []string{"cdp form get " + args[0] + " --json"}, report)
		}
		return a.render(ctx, "assertion passed", report)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "exact", "match mode: exact, contains, or regex")
	return cmd
}

func (a *app) newAssertTextCommand() *cobra.Command {
	var targetID, urlContains, titleContains, mode string
	cmd := &cobra.Command{Use: "text <expected>", Short: "Assert visible body text", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		var got textResult
		if err := evaluateJSONValue(ctx, session, textExpression("body", 0, 1), "assert text", &got); err != nil {
			return err
		}
		if got.Error != nil {
			return invalidSelectorError("body", got.Error, "cdp assert text expected --json")
		}
		passed, err := assertionMatch(got.Text, args[0], mode)
		if err != nil {
			return err
		}
		result := assertTextResult{Expected: args[0], Actual: got.Text, Mode: normalizeAssertMode(mode), Passed: passed, Count: got.Count, Error: got.Error}
		report := map[string]any{"ok": passed, "target": pageRow(target), "assertion": result}
		if !passed {
			return commandErrorWithData("assertion_failed", "check_failed", fmt.Sprintf("text assertion failed: %q was not found", args[0]), ExitCheckFailed, []string{"cdp text body --limit 0 --json"}, report)
		}
		return a.render(ctx, "assertion passed", report)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "contains", "match mode: exact, contains, or regex")
	return cmd
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
	return `(() => { const __cdp_cli_form_values__ = true; const includeHidden = ` + fmt.Sprintf("%t", includeHidden) + `; return (` + formCollectorJS("null", "includeHidden") + `); })()`
}

func formGetExpression(selector string) string {
	return `(() => { const __cdp_cli_form_get__ = true; return (` + formCollectorJS(jsStringLiteral(selector), "true") + `); })()`
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
      const value = tag === 'select' ? selected.join(',') : (el.isContentEditable ? norm(el.innerText || el.textContent) : String(el.value || ''));
      const visibility = visibleInfo(el);
      const hint = css(el);
      const out = { selector_hint: hint, tag, type: el.type || '', role: el.getAttribute('role') || '', name: label(el), value, values: selected, visible: visibility.visible, aria_hidden: visibility.ariaHidden, read_only: Boolean(el.readOnly), disabled: Boolean(el.disabled), content_editable: Boolean(el.isContentEditable) };
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
