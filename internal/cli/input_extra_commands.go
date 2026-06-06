package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type selectResult struct {
	URL            string     `json:"url,omitempty"`
	Title          string     `json:"title,omitempty"`
	Selector       string     `json:"selector"`
	Count          int        `json:"count"`
	Selected       bool       `json:"selected"`
	Trial          bool       `json:"trial,omitempty"`
	Force          bool       `json:"force,omitempty"`
	Value          string     `json:"value"`
	Previous       string     `json:"previous,omitempty"`
	RequestedValue string     `json:"requested_value,omitempty"`
	MatchedBy      string     `json:"matched_by,omitempty"`
	SelectedValues []string   `json:"selected_values,omitempty"`
	Verified       *bool      `json:"verified,omitempty"`
	Error          *evalError `json:"error,omitempty"`
}

type checkResult struct {
	URL             string     `json:"url,omitempty"`
	Title           string     `json:"title,omitempty"`
	Selector        string     `json:"selector"`
	Count           int        `json:"count"`
	Checked         bool       `json:"checked"`
	DesiredChecked  bool       `json:"desired_checked"`
	PreviousChecked bool       `json:"previous_checked"`
	Changed         bool       `json:"changed"`
	Already         bool       `json:"already,omitempty"`
	Trial           bool       `json:"trial,omitempty"`
	Force           bool       `json:"force,omitempty"`
	Tag             string     `json:"tag,omitempty"`
	Type            string     `json:"type,omitempty"`
	Role            string     `json:"role,omitempty"`
	Name            string     `json:"name,omitempty"`
	Error           *evalError `json:"error,omitempty"`
}

type fileResult struct {
	URL            string     `json:"url,omitempty"`
	Title          string     `json:"title,omitempty"`
	Selector       string     `json:"selector"`
	Count          int        `json:"count"`
	Accepted       bool       `json:"accepted"`
	FileSet        bool       `json:"file_set"`
	Trial          bool       `json:"trial,omitempty"`
	Path           string     `json:"path,omitempty"`
	FileName       string     `json:"file_name,omitempty"`
	ContentOmitted bool       `json:"content_omitted"`
	Tag            string     `json:"tag,omitempty"`
	Type           string     `json:"type,omitempty"`
	Error          *evalError `json:"error,omitempty"`
}

type scrollViewportEvidence struct {
	Rect            snapshotRect `json:"rect"`
	InViewport      bool         `json:"in_viewport"`
	FullyInViewport bool         `json:"fully_in_viewport"`
	ViewportWidth   float64      `json:"viewport_width"`
	ViewportHeight  float64      `json:"viewport_height"`
	ScrollX         float64      `json:"scroll_x"`
	ScrollY         float64      `json:"scroll_y"`
}

type scrollResult struct {
	URL      string                 `json:"url,omitempty"`
	Title    string                 `json:"title,omitempty"`
	Selector string                 `json:"selector"`
	Count    int                    `json:"count"`
	Scrolled bool                   `json:"scrolled"`
	Changed  bool                   `json:"changed"`
	Trial    bool                   `json:"trial,omitempty"`
	Block    string                 `json:"block"`
	Inline   string                 `json:"inline"`
	Before   scrollViewportEvidence `json:"before"`
	After    scrollViewportEvidence `json:"after"`
	Error    *evalError             `json:"error,omitempty"`
}

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

func (a *app) newCheckCommand() *cobra.Command {
	return a.newCheckedCommand("check", true)
}

func (a *app) newUncheckCommand() *cobra.Command {
	return a.newCheckedCommand("uncheck", false)
}

func (a *app) newCheckedCommand(name string, desired bool) *cobra.Command {
	var targetID, urlContains, titleContains string
	var locatorOpts locatorActionOptions
	var trial bool
	var force bool
	actionName := "checked"
	short := "Check a checkbox or radio control by CSS selector or strict locator"
	if !desired {
		actionName = "unchecked"
		short = "Uncheck a checkbox or radio control by CSS selector or strict locator"
	}
	cmd := &cobra.Command{
		Use:   name + " <selector-or-locator>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, name)
			if err != nil {
				return err
			}
			actionability, err := evaluateActionability(ctx, session, selector, name)
			if err != nil {
				return err
			}
			if actionability.Error != nil {
				return invalidSelectorError(selector, actionability.Error, "cdp "+name+" 'Subscribe to newsletter' --by label --trial --json")
			}
			prepareActionability(&actionability, name, trial, force)
			var autoScroll *scrollResult
			if !trial && shouldAutoScrollBeforePointerAction(name, actionability) {
				scrolled, err := autoScrollPointerTarget(ctx, session, selector)
				if err != nil {
					return err
				}
				autoScroll = &scrolled
				if scrolled.Error == nil && scrolled.After.InViewport {
					actionability, err = evaluateActionability(ctx, session, selector, name)
					if err != nil {
						return err
					}
					if actionability.Error != nil {
						return invalidSelectorError(selector, actionability.Error, "cdp "+name+" 'Subscribe to newsletter' --by label --trial --json")
					}
					prepareActionability(&actionability, name, trial, force)
				}
			}

			result := checkResult{
				URL:            actionability.URL,
				Title:          actionability.Title,
				Selector:       selector,
				Count:          actionability.Count,
				DesiredChecked: desired,
				Trial:          trial,
				Force:          force,
			}
			if trial && actionability.Actionable {
				if err := evaluateJSONValue(ctx, session, checkExpression(selector, desired, false), name+" trial", &result); err != nil {
					return err
				}
				result.Trial = true
				result.Force = force
				if result.Error != nil {
					return checkCommandResultError(name, selector, result.Error)
				}
			}
			if trial {
				report := map[string]any{
					"ok":            actionability.Actionable,
					"action":        "trial",
					"target":        pageRow(target),
					name:            result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if !actionability.Actionable {
					return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage(name, selector, actionability), ExitCheckFailed, actionabilityRemediations(name, args[0], selector, locatorOpts), report)
				}
				return a.render(ctx, fmt.Sprintf("trial\t%s\t%s", target.TargetID, selector), report)
			}
			if !actionability.Actionable {
				report := map[string]any{
					"ok":            false,
					"action":        "blocked",
					"target":        pageRow(target),
					name:            result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if autoScroll != nil {
					report["auto_scroll"] = autoScroll
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage(name, selector, actionability), ExitCheckFailed, actionabilityRemediations(name, args[0], selector, locatorOpts), report)
			}

			if err := evaluateJSONValue(ctx, session, checkExpression(selector, desired, true), name, &result); err != nil {
				return err
			}
			result.Force = force
			if result.Error != nil {
				return checkCommandResultError(name, selector, result.Error)
			}
			if result.Checked != desired {
				return commandError(name+"_failed", "check_failed", fmt.Sprintf("%s %q did not reach checked state %t", name, selector, desired), ExitCheckFailed, []string{"cdp form get " + shellQuote(selector) + " --json"})
			}
			report := map[string]any{
				"ok":            true,
				"action":        actionName,
				"target":        pageRow(target),
				name:            result,
				"actionability": actionability,
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			if autoScroll != nil {
				report["auto_scroll"] = autoScroll
			}
			return a.render(ctx, fmt.Sprintf("%s\t%s\t%s", actionName, target.TargetID, result.Selector), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&trial, "trial", false, "run locator resolution and actionability checks without changing checked state")
	cmd.Flags().BoolVar(&force, "force", false, "skip non-essential checked-state actionability checks and record skipped checks in JSON")
	return cmd
}

func checkCommandResultError(action, selector string, err *evalError) error {
	if err == nil {
		return nil
	}
	switch err.Name {
	case "InvalidTargetError":
		return commandError("invalid_target", "usage", fmt.Sprintf("%s %q: %s", action, selector, err.Message), ExitUsage, []string{"cdp form get " + shellQuote(selector) + " --json"})
	case "StateMismatchError":
		return commandError(action+"_failed", "check_failed", fmt.Sprintf("%s %q: %s", action, selector, err.Message), ExitCheckFailed, []string{"cdp form get " + shellQuote(selector) + " --json"})
	default:
		return commandError("invalid_selector", "usage", fmt.Sprintf("%s %q: %s", action, selector, err.Message), ExitUsage, []string{"cdp " + action + " input[type=checkbox] --json"})
	}
}

func (a *app) newScrollCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var locatorOpts locatorActionOptions
	var trial bool
	var block, inline string
	cmd := &cobra.Command{
		Use:   "scroll <selector-or-locator>",
		Short: "Scroll a CSS selector or strict locator into the viewport and report before/after evidence",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}
			var err error
			block, err = normalizeScrollAlignment(block, "--block")
			if err != nil {
				return err
			}
			inline, err = normalizeScrollAlignment(inline, "--inline")
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

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "scroll")
			if err != nil {
				return err
			}
			actionability, err := evaluateActionability(ctx, session, selector, "scroll")
			if err != nil {
				return err
			}
			if actionability.Error != nil {
				return invalidSelectorError(selector, actionability.Error, "cdp scroll '#target' --trial --json")
			}
			prepareActionability(&actionability, "scroll", trial, false)
			if !actionability.Actionable {
				result := scrollResult{
					URL:      actionability.URL,
					Title:    actionability.Title,
					Selector: selector,
					Count:    actionability.Count,
					Trial:    trial,
					Block:    block,
					Inline:   inline,
				}
				action := "blocked"
				if trial {
					action = "trial"
				}
				report := map[string]any{
					"ok":            false,
					"action":        action,
					"target":        pageRow(target),
					"scroll":        result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("scroll", selector, actionability), ExitCheckFailed, actionabilityRemediations("scroll", args[0], selector, locatorOpts), report)
			}

			var result scrollResult
			if err := evaluateJSONValue(ctx, session, scrollExpression(selector, block, inline, !trial), "scroll", &result); err != nil {
				return err
			}
			result.Trial = trial
			result.Block = block
			result.Inline = inline
			if result.Error != nil {
				return invalidSelectorError(selector, result.Error, "cdp scroll '#target' --json")
			}
			report := map[string]any{
				"ok":            true,
				"action":        "scrolled",
				"target":        pageRow(target),
				"scroll":        result,
				"actionability": actionability,
			}
			if trial {
				report["action"] = "trial"
			} else if !result.After.InViewport {
				report["ok"] = false
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			if !trial && !result.After.InViewport {
				return commandErrorWithData("scroll_failed", "check_failed", fmt.Sprintf("scroll %q did not bring the element into the viewport", selector), ExitCheckFailed, []string{"cdp layout overflow --json", "cdp scroll " + shellQuote(selector) + " --block center --json"}, report)
			}
			return a.render(ctx, fmt.Sprintf("%s\t%s\t%s", report["action"], target.TargetID, selector), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&trial, "trial", false, "resolve the target and report scroll evidence without changing page scroll")
	cmd.Flags().StringVar(&block, "block", "center", "vertical scroll alignment: start, center, end, or nearest")
	cmd.Flags().StringVar(&inline, "inline", "nearest", "horizontal scroll alignment: start, center, end, or nearest")
	return cmd
}

func normalizeScrollAlignment(value, flag string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "start", "center", "end", "nearest":
		return value, nil
	default:
		return "", commandError("usage", "usage", flag+" must be start, center, end, or nearest", ExitUsage, []string{"cdp scroll '#target' --block center --inline nearest --json"})
	}
}

func (a *app) newSelectCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var locatorOpts locatorActionOptions
	var trial bool
	var force bool
	var waitText string
	var waitSelector string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "select <selector-or-locator> <value>",
		Short: "Select an option value in the first matching select control by CSS selector or strict locator",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}
			hasWaitText := strings.TrimSpace(waitText) != ""
			hasWaitSelector := strings.TrimSpace(waitSelector) != ""
			if hasWaitText && hasWaitSelector {
				return commandError("usage", "usage", "use only one select wait mode: --wait-text or --wait-selector", ExitUsage, []string{"cdp select Plan pro --by label --wait-text Pro --json"})
			}
			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp select Plan pro --by label --wait-text Pro --poll 250ms --json"})
			}
			if trial && (hasWaitText || hasWaitSelector) {
				return commandError("usage", "usage", "--trial does not change the page, so it cannot use select wait flags", ExitUsage, []string{"cdp select Plan pro --by label --wait-text Pro --json"})
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "select")
			if err != nil {
				return err
			}
			actionability, err := evaluateActionability(ctx, session, selector, "select")
			if err != nil {
				return err
			}
			if actionability.Error != nil {
				return invalidSelectorError(selector, actionability.Error, "cdp select 'select[name=plan]' pro --trial --json")
			}
			prepareActionability(&actionability, "select", trial, force)
			if trial {
				result := selectResult{URL: actionability.URL, Title: actionability.Title, Selector: selector, Count: actionability.Count, Selected: false, Trial: true, Force: force, Value: args[1], RequestedValue: args[1]}
				report := map[string]any{
					"ok":            actionability.Actionable,
					"action":        "trial",
					"target":        pageRow(target),
					"select":        result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				if !actionability.Actionable {
					return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("select", selector, actionability), ExitCheckFailed, actionabilityRemediations("select", args[0], selector, locatorOpts), report)
				}
				return a.render(ctx, fmt.Sprintf("trial\t%s\t%s", target.TargetID, selector), report)
			}
			if !actionability.Actionable {
				result := selectResult{URL: actionability.URL, Title: actionability.Title, Selector: selector, Count: actionability.Count, Selected: false, Force: force, Value: args[1], RequestedValue: args[1]}
				report := map[string]any{
					"ok":            false,
					"action":        "blocked",
					"target":        pageRow(target),
					"select":        result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("select", selector, actionability), ExitCheckFailed, actionabilityRemediations("select", args[0], selector, locatorOpts), report)
			}

			var result selectResult
			if err := evaluateJSONValue(ctx, session, selectExpression(selector, args[1]), "select", &result); err != nil {
				return err
			}
			result.Force = force
			if result.Error != nil {
				if result.Error.Name == "OptionNotFoundError" {
					return commandError("option_not_found", "check_failed", fmt.Sprintf("select %q: %s", selector, result.Error.Message), ExitCheckFailed, []string{"cdp form get " + shellQuote(selector) + " --json"})
				}
				return commandError("invalid_selector", "usage", fmt.Sprintf("select %q: %s", selector, result.Error.Message), ExitUsage, []string{"cdp select 'select[name=plan]' pro --json"})
			}
			if !result.Selected {
				return commandError("option_not_selected", "check_failed", fmt.Sprintf("select %q did not select value %q", selector, args[1]), ExitCheckFailed, []string{"cdp form get " + shellQuote(selector) + " --json"})
			}
			verified := true
			var verification *waitResult
			if hasWaitText || hasWaitSelector {
				wait, err := waitForClickVerification(ctx, session, poll, waitText, waitSelector)
				if err != nil {
					return err
				}
				verified = wait.Matched
				result.Verified = &verified
				verification = &wait
			}
			report := map[string]any{
				"ok":            verified,
				"action":        "selected",
				"target":        pageRow(target),
				"select":        result,
				"actionability": actionability,
			}
			if verification != nil {
				report["verification"] = verification
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			human := fmt.Sprintf("selected\t%s\t%s", target.TargetID, result.Selector)
			if !verified {
				human = fmt.Sprintf("select-unverified\t%s\t%s", target.TargetID, result.Selector)
			}
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&trial, "trial", false, "run locator resolution and actionability checks without changing the selected option")
	cmd.Flags().BoolVar(&force, "force", false, "skip non-essential select actionability checks and record skipped checks in JSON")
	cmd.Flags().StringVar(&waitText, "wait-text", "", "verify by waiting until visible page text contains this string")
	cmd.Flags().StringVar(&waitSelector, "wait-selector", "", "verify by waiting until this CSS selector matches")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while waiting for verification")
	return cmd
}

func (a *app) newFileCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var locatorOpts locatorActionOptions
	var trial bool
	cmd := &cobra.Command{
		Use:   "file <selector-or-locator> <path>",
		Short: "Set a file input by CSS selector or strict locator without printing file contents",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(args[1]); err != nil {
				return commandError("usage", "usage", fmt.Sprintf("file path is not readable: %v", err), ExitUsage, []string{"cdp file input[type=file] tmp/upload.txt --json"})
			}
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}
			absPath, err := filepath.Abs(args[1])
			if err != nil {
				return commandError("usage", "usage", fmt.Sprintf("resolve file path %s: %v", args[1], err), ExitUsage, []string{"cdp file input[type=file] tmp/upload.txt --json"})
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "file")
			if err != nil {
				return err
			}
			actionability, err := evaluateActionability(ctx, session, selector, "file")
			if err != nil {
				return err
			}
			if actionability.Error != nil {
				return invalidSelectorError(selector, actionability.Error, "cdp file input[type=file] tmp/upload.txt --trial --json")
			}
			prepareActionability(&actionability, "file", trial, false)
			result := fileResult{
				URL:            actionability.URL,
				Title:          actionability.Title,
				Selector:       selector,
				Count:          actionability.Count,
				Trial:          trial,
				Path:           args[1],
				FileName:       filepath.Base(args[1]),
				ContentOmitted: true,
			}
			if !actionability.Actionable {
				report := map[string]any{
					"ok":            false,
					"action":        map[bool]string{true: "trial", false: "blocked"}[trial],
					"target":        pageRow(target),
					"file":          result,
					"actionability": actionability,
				}
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage("file", selector, actionability), ExitCheckFailed, actionabilityRemediations("file", args[0], selector, locatorOpts), report)
			}
			if err := evaluateJSONValue(ctx, session, fileInputExpression(selector, filepath.Base(args[1])), "file", &result); err != nil {
				return err
			}
			result.Trial = trial
			result.Path = args[1]
			result.FileName = filepath.Base(args[1])
			result.ContentOmitted = true
			if result.Error != nil {
				return fileCommandResultError(selector, result.Error)
			}
			if !result.Accepted {
				return commandError("invalid_target", "usage", fmt.Sprintf("file %q did not resolve to an input[type=file]", selector), ExitUsage, []string{"cdp locator find 'Upload file' --by label --json", "cdp file input[type=file] tmp/upload.txt --json"})
			}
			report := map[string]any{
				"ok":            true,
				"action":        "file_set",
				"target":        pageRow(target),
				"file":          result,
				"actionability": actionability,
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			if trial {
				report["action"] = "trial"
				return a.render(ctx, fmt.Sprintf("trial\t%s\t%s", target.TargetID, selector), report)
			}
			if err := setFileInputFiles(ctx, session, selector, absPath); err != nil {
				return err
			}
			result.FileSet = true
			report["file"] = result
			return a.render(ctx, fmt.Sprintf("file\t%s\t%s", selector, filepath.Base(args[1])), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&trial, "trial", false, "resolve and validate the file input without assigning the local file")
	return cmd
}

func fileCommandResultError(selector string, err *evalError) error {
	if err == nil {
		return nil
	}
	switch err.Name {
	case "InvalidTargetError":
		return commandError("invalid_target", "usage", fmt.Sprintf("file %q: %s", selector, err.Message), ExitUsage, []string{"cdp form values --json", "cdp locator find 'Upload file' --by label --json"})
	default:
		return commandError("invalid_selector", "usage", fmt.Sprintf("file %q: %s", selector, err.Message), ExitUsage, []string{"cdp file input[type=file] tmp/upload.txt --json"})
	}
}

func setFileInputFiles(ctx context.Context, session *cdp.PageSession, selector, path string) error {
	var doc struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if err := execSessionJSON(ctx, session, "DOM.getDocument", map[string]any{"depth": 0, "pierce": true}, &doc); err != nil {
		return commandError("connection_failed", "connection", fmt.Sprintf("inspect DOM for file input: %v", err), ExitConnection, []string{"cdp protocol describe DOM.getDocument --json"})
	}
	if doc.Root.NodeID == 0 {
		return commandError("dom_unavailable", "connection", "DOM.getDocument did not return a root node", ExitConnection, []string{"cdp daemon health --json"})
	}
	var query struct {
		NodeID int `json:"nodeId"`
	}
	if err := execSessionJSON(ctx, session, "DOM.querySelector", map[string]any{"nodeId": doc.Root.NodeID, "selector": selector}, &query); err != nil {
		return commandError("invalid_selector", "usage", fmt.Sprintf("query file input %q: %v", selector, err), ExitUsage, []string{"cdp dom query " + shellQuote(selector) + " --json"})
	}
	if query.NodeID == 0 {
		return commandError("invalid_selector", "usage", fmt.Sprintf("selector %q matched no DOM node for file assignment", selector), ExitUsage, []string{"cdp dom query " + shellQuote(selector) + " --json"})
	}
	if err := execSessionJSON(ctx, session, "DOM.setFileInputFiles", map[string]any{"nodeId": query.NodeID, "files": []string{path}}, nil); err != nil {
		return commandError("file_set_failed", "connection", fmt.Sprintf("set file input %q: %v", selector, err), ExitConnection, []string{"cdp protocol describe DOM.setFileInputFiles --json"})
	}
	return nil
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
	cmd.AddCommand(a.newA11ySnapshotCommand())
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

func (a *app) newA11ySnapshotCommand() *cobra.Command {
	var targetID, urlContains, titleContains, selector string
	var depth, limit int
	var includeIgnored bool
	cmd := &cobra.Command{Use: "snapshot", Short: "Generate a bounded ARIA snapshot from the accessibility tree", RunE: func(cmd *cobra.Command, args []string) error {
		if depth < 0 {
			return commandError("usage", "usage", "--depth must be non-negative", ExitUsage, []string{"cdp a11y snapshot --depth 4 --json"})
		}
		if limit < 0 {
			return commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp a11y snapshot --limit 100 --json"})
		}
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		snapshot, err := collectA11ySnapshot(ctx, session, selector, depth, limit, includeIgnored)
		if err != nil {
			return err
		}
		snapshot.URL = target.URL
		snapshot.Title = target.Title
		report := map[string]any{
			"ok":        true,
			"target":    pageRow(target),
			"snapshot":  snapshot,
			"lines":     snapshot.Lines,
			"text":      snapshot.Text,
			"truncated": snapshot.Truncated,
		}
		return a.render(ctx, fmt.Sprintf("a11y-snapshot\t%d lines", snapshot.LineCount), report)
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&selector, "selector", "body", "CSS selector that names the intended snapshot scope")
	cmd.Flags().IntVar(&depth, "depth", 4, "maximum accessibility tree depth to include")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum snapshot lines to return")
	cmd.Flags().BoolVar(&includeIgnored, "include-ignored", false, "include ignored accessibility nodes")
	return cmd
}
