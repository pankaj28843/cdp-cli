package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type clickResult struct {
	URL        string       `json:"url"`
	Title      string       `json:"title"`
	Selector   string       `json:"selector"`
	Count      int          `json:"count"`
	Clicked    bool         `json:"clicked"`
	Strategy   string       `json:"strategy,omitempty"`
	X          float64      `json:"x,omitempty"`
	Y          float64      `json:"y,omitempty"`
	Rect       snapshotRect `json:"rect,omitempty"`
	Verified   *bool        `json:"verified,omitempty"`
	TargetID   string       `json:"target_id,omitempty"`
	FinalURL   string       `json:"final_url,omitempty"`
	FinalTitle string       `json:"final_title,omitempty"`
	Error      *evalError   `json:"error,omitempty"`
}

type fillResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Filled   bool       `json:"filled"`
	Value    string     `json:"value"`
	Previous string     `json:"previous"`
	Error    *evalError `json:"error,omitempty"`
}

type locatorActionOptions struct {
	By            string
	Role          string
	TestIDAttr    string
	Exact         bool
	IncludeHidden bool
	Limit         int
}

type typeResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Typing   bool       `json:"typing"`
	Typed    string     `json:"typed"`
	Previous string     `json:"previous"`
	Value    string     `json:"value,omitempty"`
	Kind     string     `json:"kind,omitempty"`
	Strategy string     `json:"strategy,omitempty"`
	Error    *evalError `json:"error,omitempty"`
}

type pressResult struct {
	URL        string     `json:"url"`
	Title      string     `json:"title"`
	Selector   string     `json:"selector"`
	Key        string     `json:"key"`
	Count      int        `json:"count"`
	Dispatched bool       `json:"dispatched"`
	Error      *evalError `json:"error,omitempty"`
}

type hoverResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Hovered  bool       `json:"hovered"`
	X        float64    `json:"x"`
	Y        float64    `json:"y"`
	Error    *evalError `json:"error,omitempty"`
}

type dragResult struct {
	URL      string     `json:"url"`
	Title    string     `json:"title"`
	Selector string     `json:"selector"`
	Count    int        `json:"count"`
	Dragged  bool       `json:"dragged"`
	DeltaX   int        `json:"delta_x"`
	DeltaY   int        `json:"delta_y"`
	StartX   float64    `json:"start_x"`
	StartY   float64    `json:"start_y"`
	EndX     float64    `json:"end_x"`
	EndY     float64    `json:"end_y"`
	Error    *evalError `json:"error,omitempty"`
}

type frameTreeResponse struct {
	FrameTree *frameTreeNode `json:"frameTree"`
}

type frameTreeNode struct {
	Frame       *frameInfo      `json:"frame"`
	ChildFrames []frameTreeNode `json:"childFrames"`
}

type frameInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	SecurityOrigin string `json:"securityOrigin"`
	MimeType       string `json:"mimeType"`
}

type frameSummary struct {
	FrameID        string `json:"frame_id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	SecurityOrigin string `json:"security_origin"`
	MimeType       string `json:"mime_type"`
	ParentID       string `json:"parent_id"`
	ChildCount     int    `json:"child_count"`
}

func (a *app) newClickCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var strategy string
	var locatorOpts locatorActionOptions
	var activate bool
	var waitText string
	var waitSelector string
	var diagnosticsOut string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "click <selector-or-locator>",
		Short: "Click the first matching element by CSS selector or strict locator",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			strategy = strings.ToLower(strings.TrimSpace(strategy))
			if strategy == "" {
				strategy = "auto"
			}
			if strategy != "auto" && strategy != "dom" && strategy != "raw-input" {
				return commandError("usage", "usage", "--strategy must be auto, dom, or raw-input", ExitUsage, []string{"cdp click main --strategy raw-input --json"})
			}
			if strings.TrimSpace(waitText) != "" && strings.TrimSpace(waitSelector) != "" {
				return commandError("usage", "usage", "use only one of --wait-text or --wait-selector", ExitUsage, []string{"cdp click button --wait-text Done --json"})
			}
			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp click button --wait-text Done --poll 250ms --json"})
			}
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			if activate {
				if err := cdp.ActivateTargetWithClient(ctx, client, target.TargetID); err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("activate target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp page activate --json"})
				}
			}

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "click")
			if err != nil {
				return err
			}

			result, err := performClick(ctx, session, selector, strategy)
			if err != nil {
				return err
			}
			if result.Error != nil {
				return invalidSelectorError(selector, result.Error, "cdp click main --json")
			}
			if !result.Clicked {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no matching element found for selector %q", selector),
					ExitUsage,
					[]string{"cdp click main --json"},
				)
			}

			verified := true
			var verification *waitResult
			if strings.TrimSpace(waitText) != "" || strings.TrimSpace(waitSelector) != "" {
				wait, err := waitForClickVerification(ctx, session, poll, waitText, waitSelector)
				if err != nil {
					return err
				}
				verification = &wait
				verified = wait.Matched
				if !verified && strategy == "auto" {
					fallback, err := performClick(ctx, session, selector, "raw-input")
					if err != nil {
						return err
					}
					if fallback.Error != nil {
						return invalidSelectorError(selector, fallback.Error, "cdp click main --strategy raw-input --json")
					}
					result = fallback
					wait, err = waitForClickVerification(ctx, session, poll, waitText, waitSelector)
					if err != nil {
						return err
					}
					verification = &wait
					verified = wait.Matched
				}
				result.Verified = &verified
			}

			finalTarget, refreshErr := refreshedClickTarget(ctx, client, target)
			result.TargetID = finalTarget.TargetID
			result.FinalURL = finalTarget.URL
			result.FinalTitle = finalTarget.Title

			report := map[string]any{
				"ok":            verified,
				"action":        "clicked",
				"target":        pageRow(finalTarget),
				"before_target": pageRow(target),
				"after_target":  pageRow(finalTarget),
				"final_target":  pageRow(finalTarget),
				"page_state":    clickPageState(target, finalTarget),
				"click":         result,
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			if refreshErr != nil {
				report["target_refresh"] = map[string]any{
					"ok":        false,
					"target_id": target.TargetID,
					"error":     refreshErr.Error(),
				}
			}
			if verification != nil {
				report["verification"] = verification
			}
			if strings.TrimSpace(diagnosticsOut) != "" {
				diagnostics := clickDiagnostics(target, finalTarget, selector, strategy, activate, waitText, waitSelector, a.clickTimeout(), result, verification)
				report["diagnostics"] = diagnostics
				b, err := json.MarshalIndent(diagnostics, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal click diagnostics: %v", err), ExitInternal, []string{"cdp click button --diagnostics-out tmp/click.local.json --json"})
				}
				writtenPath, err := writeArtifactFile(diagnosticsOut, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "click-diagnostics", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "click-diagnostics", "path": writtenPath, "bytes": len(b) + 1}}
			}
			human := fmt.Sprintf("clicked\t%s\t%s", target.TargetID, result.Selector)
			if !verified {
				human = fmt.Sprintf("click-unverified\t%s\t%s", target.TargetID, result.Selector)
			}
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&strategy, "strategy", "auto", "click strategy: auto, dom, or raw-input")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().BoolVar(&activate, "activate", false, "bring the target page to the foreground before clicking")
	cmd.Flags().StringVar(&waitText, "wait-text", "", "verify by waiting until visible page text contains this string")
	cmd.Flags().StringVar(&waitSelector, "wait-selector", "", "verify by waiting until this CSS selector matches")
	cmd.Flags().StringVar(&diagnosticsOut, "diagnostics-out", "", "optional path for privacy-preserving click diagnostics JSON")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while waiting for verification")
	return cmd
}

func performClick(ctx context.Context, session *cdp.PageSession, selector, strategy string) (clickResult, error) {
	if strategy == "auto" || strategy == "dom" {
		var result clickResult
		if err := evaluateJSONValue(ctx, session, clickExpression(selector), "click", &result); err != nil {
			return clickResult{}, err
		}
		result.Strategy = "dom"
		return result, nil
	}
	var result clickResult
	if err := evaluateJSONValue(ctx, session, rawClickPointExpression(selector), "click point", &result); err != nil {
		return clickResult{}, err
	}
	if result.Error != nil || !result.Clicked {
		return result, nil
	}
	if err := dispatchRawMouseClick(ctx, session, result.X, result.Y); err != nil {
		return clickResult{}, err
	}
	result.Strategy = "raw-input"
	return result, nil
}

func dispatchRawMouseClick(ctx context.Context, session *cdp.PageSession, x, y float64) error {
	events := []map[string]any{
		{"type": "mouseMoved", "x": x, "y": y, "button": "none"},
		{"type": "mousePressed", "x": x, "y": y, "button": "left", "buttons": 1, "clickCount": 1},
		{"type": "mouseReleased", "x": x, "y": y, "button": "left", "buttons": 0, "clickCount": 1},
	}
	for _, event := range events {
		params, _ := json.Marshal(event)
		if _, err := session.Exec(ctx, "Input.dispatchMouseEvent", params); err != nil {
			return commandError("connection_failed", "connection", fmt.Sprintf("dispatch raw mouse event target %s: %v", session.TargetID, err), ExitConnection, []string{"cdp protocol exec Input.dispatchMouseEvent --params '{\"type\":\"mousePressed\",\"x\":100,\"y\":100,\"button\":\"left\",\"buttons\":1,\"clickCount\":1}' --json"})
		}
	}
	return nil
}

func addLocatorActionFlags(cmd *cobra.Command, opts *locatorActionOptions) {
	cmd.Flags().StringVar(&opts.By, "by", "css", "locator strategy before action: css, role, text, label, placeholder, alt, title, or test-id")
	cmd.Flags().StringVar(&opts.Role, "role", "", "ARIA role to match when --by role is used")
	cmd.Flags().StringVar(&opts.TestIDAttr, "test-id-attr", "data-testid", "attribute name for --by test-id")
	cmd.Flags().BoolVar(&opts.Exact, "exact", false, "require exact normalized text/name/attribute match for locator actions")
	cmd.Flags().BoolVar(&opts.IncludeHidden, "include-hidden", false, "include hidden locator matches before action")
	cmd.Flags().IntVar(&opts.Limit, "locator-limit", 20, "maximum locator matches to inspect before action")
}

func normalizeLocatorActionOptions(opts *locatorActionOptions) error {
	opts.By = normalizeLocatorStrategy(opts.By)
	opts.Role = strings.TrimSpace(opts.Role)
	opts.TestIDAttr = strings.TrimSpace(opts.TestIDAttr)
	return validateLocatorFindOptions(opts.By, opts.Role, opts.TestIDAttr, opts.Limit)
}

func resolveActionSelector(ctx context.Context, session *cdp.PageSession, query string, opts locatorActionOptions, action string) (string, *locatorFindResult, error) {
	if opts.By == "css" {
		return query, nil, nil
	}

	var result locatorFindResult
	if err := evaluateJSONValue(ctx, session, locatorFindExpression(opts.By, query, opts.Role, opts.Exact, opts.IncludeHidden, opts.TestIDAttr, opts.Limit), "locator action", &result); err != nil {
		return "", nil, err
	}
	if result.Error != nil {
		return "", &result, commandError(
			"invalid_locator",
			"usage",
			fmt.Sprintf("%s locator %s %q: %s", action, opts.By, query, result.Error.Message),
			ExitUsage,
			locatorActionRemediations(action, query, opts),
		)
	}
	if result.Count == 0 {
		return "", &result, commandError(
			"locator_not_found",
			"usage",
			fmt.Sprintf("%s locator %s %q matched no elements", action, opts.By, query),
			ExitUsage,
			locatorActionRemediations(action, query, opts),
		)
	}
	if result.Count != 1 || len(result.Matches) != 1 {
		return "", &result, commandError(
			"ambiguous_locator",
			"usage",
			fmt.Sprintf("%s locator %s %q matched %d elements; refine the locator before acting", action, opts.By, query, result.Count),
			ExitUsage,
			locatorActionRemediations(action, query, opts),
		)
	}

	match := result.Matches[0]
	selector := strings.TrimSpace(match.SelectorHint)
	if selector == "" || match.SelectorAmbiguous {
		return "", &result, commandError(
			"ambiguous_locator",
			"usage",
			fmt.Sprintf("%s locator %s %q matched one element but did not produce a unique CSS selector hint", action, opts.By, query),
			ExitUsage,
			[]string{locatorActionFindCommand(query, opts), "cdp snapshot --selector body --json"},
		)
	}
	return selector, &result, nil
}

func locatorActionRemediations(action, query string, opts locatorActionOptions) []string {
	example := "cdp " + action + " " + shellQuote(query)
	switch action {
	case "fill":
		example += " <value>"
	case "assert value", "assert text":
		example += " <expected>"
	}
	example += " --by " + opts.By
	if opts.By == "role" {
		example += " --role " + shellQuote(opts.Role)
	}
	if opts.Exact {
		example += " --exact"
	}
	if opts.IncludeHidden {
		example += " --include-hidden"
	}
	if opts.By == "test-id" && opts.TestIDAttr != "data-testid" {
		example += " --test-id-attr " + shellQuote(opts.TestIDAttr)
	}
	return []string{locatorActionFindCommand(query, opts), example + " --json"}
}

func locatorActionFindCommand(query string, opts locatorActionOptions) string {
	command := "cdp locator find " + shellQuote(query) + " --by " + opts.By
	if opts.By == "role" {
		command += " --role " + shellQuote(opts.Role)
	}
	if opts.Exact {
		command += " --exact"
	}
	if opts.IncludeHidden {
		command += " --include-hidden"
	}
	if opts.By == "test-id" && opts.TestIDAttr != "data-testid" {
		command += " --test-id-attr " + shellQuote(opts.TestIDAttr)
	}
	return command + " --json"
}

func waitForClickVerification(ctx context.Context, session *cdp.PageSession, poll time.Duration, waitText, waitSelector string) (waitResult, error) {
	start := time.Now()
	kind := "text"
	value := strings.TrimSpace(waitText)
	label := "wait text"
	expression := func() string { return waitTextExpression(value) }
	if strings.TrimSpace(waitSelector) != "" {
		kind = "selector"
		value = strings.TrimSpace(waitSelector)
		label = "wait selector"
		expression = func() string { return waitSelectorExpression(value) }
	}
	last := waitResult{Kind: kind, PollInterval: poll.String()}
	if kind == "text" {
		last.Needle = value
	} else {
		last.Selector = value
	}
	for {
		select {
		case <-ctx.Done():
			last.ElapsedMS = time.Since(start).Milliseconds()
			return last, nil
		default:
		}

		var result waitResult
		if err := evaluateJSONValue(ctx, session, expression(), label, &result); err != nil {
			if clickVerificationTimedOut(ctx, err) {
				last.ElapsedMS = time.Since(start).Milliseconds()
				last.PollInterval = poll.String()
				return last, nil
			}
			return waitResult{}, err
		}
		result.ElapsedMS = time.Since(start).Milliseconds()
		result.PollInterval = poll.String()
		last = result
		if result.Matched || result.Error != nil {
			return result, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			last.ElapsedMS = time.Since(start).Milliseconds()
			last.PollInterval = poll.String()
			return last, nil
		case <-timer.C:
		}
	}
}

func clickVerificationTimedOut(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	if commandErr.ExitCode == ExitTimeout {
		return true
	}
	return commandErr.Code == "connection_failed" && strings.Contains(strings.ToLower(commandErr.Message), "timeout")
}

func refreshedClickTarget(ctx context.Context, client cdp.CommandClient, before cdp.TargetInfo) (cdp.TargetInfo, error) {
	if strings.TrimSpace(before.TargetID) == "" {
		return before, nil
	}
	after, err := cdp.TargetInfoWithClient(ctx, client, before.TargetID)
	if err != nil {
		return before, err
	}
	if strings.TrimSpace(after.TargetID) == "" {
		return before, nil
	}
	return after, nil
}

func clickPageState(before, after cdp.TargetInfo) map[string]any {
	return map[string]any{
		"before":        pageRow(before),
		"after":         pageRow(after),
		"final":         pageRow(after),
		"same_target":   before.TargetID == after.TargetID,
		"url_changed":   before.URL != after.URL,
		"title_changed": before.Title != after.Title,
	}
}

func (a *app) clickTimeout() time.Duration {
	if a.opts.timeout > 0 {
		return a.opts.timeout
	}
	return 10 * time.Second
}

func clickDiagnostics(beforeTarget, afterTarget cdp.TargetInfo, selector, requestedStrategy string, activate bool, waitText, waitSelector string, timeout time.Duration, click clickResult, verification *waitResult) map[string]any {
	diagnostics := map[string]any{
		"selector":           selector,
		"requested_strategy": requestedStrategy,
		"strategy":           click.Strategy,
		"activated":          activate,
		"timeout":            timeout.String(),
		"target":             pageRow(afterTarget),
		"before_target":      pageRow(beforeTarget),
		"after_target":       pageRow(afterTarget),
		"page_state":         clickPageState(beforeTarget, afterTarget),
		"click": map[string]any{
			"clicked": click.Clicked,
			"count":   click.Count,
			"rect":    click.Rect,
			"x":       click.X,
			"y":       click.Y,
		},
	}
	if strings.TrimSpace(waitText) != "" {
		diagnostics["wait"] = map[string]any{"kind": "text", "needle": waitText}
	} else if strings.TrimSpace(waitSelector) != "" {
		diagnostics["wait"] = map[string]any{"kind": "selector", "selector": waitSelector}
	}
	if verification != nil {
		diagnostics["verification"] = verification
	}
	return diagnostics
}
func (a *app) newFillCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var locatorOpts locatorActionOptions
	cmd := &cobra.Command{
		Use:   "fill <selector-or-locator> <value>",
		Short: "Set the value of the first matching form control by CSS selector or strict locator",
		Args:  cobra.ExactArgs(2),
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

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "fill")
			if err != nil {
				return err
			}

			var result fillResult
			if err := evaluateJSONValue(ctx, session, fillExpression(selector, args[1]), "fill", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("fill %q: %s", selector, result.Error.Message),
					ExitUsage,
					[]string{"cdp fill input.email example@example.com --json"},
				)
			}
			if !result.Filled {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no editable element found for selector %q", selector),
					ExitUsage,
					[]string{"cdp fill #name Alice --json"},
				)
			}
			report := map[string]any{
				"ok":     true,
				"action": "filled",
				"target": pageRow(target),
				"fill":   result,
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			return a.render(ctx, fmt.Sprintf("filled\t%s\t%s", target.TargetID, result.Selector), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	return cmd
}

func (a *app) newTypeCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var strategy string
	cmd := &cobra.Command{
		Use:   "type <selector> <text>",
		Short: "Type text into the first matching editable element",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			strategy = strings.ToLower(strings.TrimSpace(strategy))
			if strategy == "" {
				strategy = "auto"
			}
			if strategy != "auto" && strategy != "dom" && strategy != "insert-text" {
				return commandError("usage", "usage", "--strategy must be auto, dom, or insert-text", ExitUsage, []string{"cdp type '[contenteditable=true]' hello --strategy auto --json"})
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			result, err := performTextInput(ctx, session, args[0], args[1], strategy)
			if err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("type %q: %s", args[0], result.Error.Message),
					ExitUsage,
					[]string{"cdp type input[name='email'] user@example.com --json"},
				)
			}
			if !result.Typing {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no editable element found for selector %q", args[0]),
					ExitUsage,
					[]string{"cdp type #name Alice --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("typed\t%s\t%s", target.TargetID, result.Selector), map[string]any{
				"ok":     true,
				"action": "typed",
				"target": pageRow(target),
				"type":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&strategy, "strategy", "auto", "text input strategy: auto, dom, or insert-text")
	return cmd
}

func (a *app) newInsertTextCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "insert-text <selector> <text>",
		Short: "Insert text through the browser input pipeline",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			result, err := performTextInput(ctx, session, args[0], args[1], "insert-text")
			if err != nil {
				return err
			}
			if result.Error != nil {
				return commandError("invalid_selector", "usage", fmt.Sprintf("insert-text %q: %s", args[0], result.Error.Message), ExitUsage, []string{"cdp insert-text '[contenteditable=true]' hello --json"})
			}
			if !result.Typing {
				return commandError("invalid_selector", "usage", fmt.Sprintf("no editable element found for selector %q", args[0]), ExitUsage, []string{"cdp insert-text '[contenteditable=true]' hello --json"})
			}
			return a.render(ctx, fmt.Sprintf("inserted-text\t%s\t%s", target.TargetID, result.Selector), map[string]any{
				"ok":          true,
				"action":      "inserted_text",
				"target":      pageRow(target),
				"insert_text": result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func performTextInput(ctx context.Context, session *cdp.PageSession, selector, text, strategy string) (typeResult, error) {
	var result typeResult
	if err := evaluateJSONValue(ctx, session, typeExpression(selector, text, strategy), "type", &result); err != nil {
		return typeResult{}, err
	}
	if result.Error != nil || !result.Typing || result.Strategy != "insert-text" {
		return result, nil
	}
	params, _ := json.Marshal(map[string]any{"text": text})
	if _, err := session.Exec(ctx, "Input.insertText", params); err != nil {
		return typeResult{}, commandError("connection_failed", "connection", fmt.Sprintf("insert text target %s: %v", session.TargetID, err), ExitConnection, []string{"cdp protocol exec Input.insertText --params '{\"text\":\"hello\"}' --json"})
	}
	if err := evaluateJSONValue(ctx, session, insertedTextResultExpression(selector, text, result.Previous, result.Kind, result.Count), "insert-text", &result); err != nil {
		return typeResult{}, err
	}
	return result, nil
}

func (a *app) newPressCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var selector string
	cmd := &cobra.Command{
		Use:   "press <key>",
		Short: "Press a key on the focused element or an optional selector",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result pressResult
			if err := evaluateJSONValue(ctx, session, pressExpression(args[0], selector), "press", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("press %q: %s", args[0], result.Error.Message),
					ExitUsage,
					[]string{"cdp press Enter --selector 'input[name=\"q\"]' --json"},
				)
			}
			if !result.Dispatched {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no target found for keypress %q", args[0]),
					ExitUsage,
					[]string{"cdp press Enter --selector 'body' --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("pressed\t%s\t%q", target.TargetID, result.Key), map[string]any{
				"ok":     true,
				"action": "pressed",
				"target": pageRow(target),
				"press":  result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&selector, "selector", "", "optional selector to focus before pressing the key")
	return cmd
}

func (a *app) newHoverCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "hover <selector>",
		Short: "Dispatch pointer hover events over the first matching element",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result hoverResult
			if err := evaluateJSONValue(ctx, session, hoverExpression(args[0]), "hover", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("hover %q: %s", args[0], result.Error.Message),
					ExitUsage,
					[]string{"cdp hover 'button.primary' --json"},
				)
			}
			if !result.Hovered {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no matching element found for selector %q", args[0]),
					ExitUsage,
					[]string{"cdp hover 'button.primary' --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("hovered\t%s\t%s", target.TargetID, result.Selector), map[string]any{
				"ok":     true,
				"action": "hovered",
				"target": pageRow(target),
				"hover":  result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newDragCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "drag <selector> <dx> <dy>",
		Short: "Drag the first matching element by a delta",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			dx, err := strconv.Atoi(strings.TrimSpace(args[1]))
			if err != nil {
				return commandError("invalid_argument", "usage", "dx must be an integer", ExitUsage, []string{"cdp drag '.node' 10 20 --json"})
			}
			dy, err := strconv.Atoi(strings.TrimSpace(args[2]))
			if err != nil {
				return commandError("invalid_argument", "usage", "dy must be an integer", ExitUsage, []string{"cdp drag '.node' 10 20 --json"})
			}

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result dragResult
			if err := evaluateJSONValue(ctx, session, dragExpression(args[0], dx, dy), "drag", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("drag %q: %s", args[0], result.Error.Message),
					ExitUsage,
					[]string{"cdp drag '#drag-me' 10 20 --json"},
				)
			}
			if !result.Dragged {
				return commandError(
					"invalid_selector",
					"usage",
					fmt.Sprintf("no matching element found for selector %q", args[0]),
					ExitUsage,
					[]string{"cdp drag '#drag-me' 10 20 --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("dragged\t%s\t%s", target.TargetID, result.Selector), map[string]any{
				"ok":     true,
				"action": "dragged",
				"target": pageRow(target),
				"drag":   result,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}
