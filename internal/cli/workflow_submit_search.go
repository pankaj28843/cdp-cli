package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowSubmitSearchCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var targetIndex int
	var locatorOpts locatorActionOptions
	var suggestionQuery string
	var suggestionBy string
	var suggestionRole string
	var suggestionTestIDAttr string
	var suggestionExact bool
	var suggestionIncludeHidden bool
	var suggestionLimit int
	var suggestionStrategy string
	var inputMode string
	var inputStrategy string
	var submit string
	var submitKey string
	var force bool
	var waitText string
	var waitSelector string
	var waitURL string
	var waitURLContains string
	var waitLoadState string
	var waitResponse bool
	var waitResponseURL string
	var waitResponseMatchURL string
	var waitResponseMethod string
	var waitResponseResourceType string
	var waitResponseStatus int
	var waitResponseStatusMin int
	var waitResponseStatusMax int
	var waitResponseRedact string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "submit-search <input-selector-or-locator> <query>",
		Short: "Fill or type into a search input, optionally submit with Enter, and verify the resulting page state",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			inputMode = strings.ToLower(strings.TrimSpace(inputMode))
			if inputMode == "" {
				inputMode = "fill"
			}
			if inputMode != "fill" && inputMode != "type" {
				return commandError("usage", "usage", "--input-mode must be fill or type", ExitUsage, []string{"cdp workflow submit-search 'Search' 'agentic engineering' --by label --wait-url-contains /search --json"})
			}
			inputStrategy = strings.ToLower(strings.TrimSpace(inputStrategy))
			if inputStrategy == "" {
				inputStrategy = "auto"
			}
			if inputStrategy != "auto" && inputStrategy != "dom" && inputStrategy != "insert-text" {
				return commandError("usage", "usage", "--input-strategy must be auto, dom, or insert-text", ExitUsage, []string{"cdp workflow submit-search 'Search' 'agentic engineering' --input-mode type --input-strategy auto --json"})
			}
			submit = strings.ToLower(strings.TrimSpace(submit))
			if submit == "" {
				submit = "enter"
			}
			if submit != "enter" && submit != "none" {
				return commandError("usage", "usage", "--submit must be enter or none", ExitUsage, []string{"cdp workflow submit-search 'Search' 'agentic engineering' --submit enter --json"})
			}
			submitKey = strings.TrimSpace(submitKey)
			if submit == "enter" && submitKey == "" {
				return commandError("usage", "usage", "--submit-key is required when --submit enter is used", ExitUsage, []string{"cdp workflow submit-search 'Search' 'agentic engineering' --submit-key Enter --json"})
			}
			if err := normalizeLocatorActionOptions(&locatorOpts); err != nil {
				return err
			}
			stateWaitCount := 0
			if strings.TrimSpace(waitText) != "" {
				stateWaitCount++
			}
			if strings.TrimSpace(waitSelector) != "" {
				stateWaitCount++
			}
			if strings.TrimSpace(waitURL) != "" {
				stateWaitCount++
			}
			if strings.TrimSpace(waitURLContains) != "" {
				stateWaitCount++
			}
			var loadState string
			waitResponse = waitResponse || cmd.Flags().Changed("wait-response-url") || cmd.Flags().Changed("wait-response-match-url") || cmd.Flags().Changed("wait-response-method") || cmd.Flags().Changed("wait-response-resource-type") || cmd.Flags().Changed("wait-response-status") || cmd.Flags().Changed("wait-response-status-min") || cmd.Flags().Changed("wait-response-status-max") || cmd.Flags().Changed("wait-response-redact")
			if strings.TrimSpace(waitLoadState) != "" {
				var err error
				loadState, err = normalizeLoadState(waitLoadState)
				if err != nil {
					return err
				}
				stateWaitCount++
			}
			waitModeCount := stateWaitCount
			if waitResponse {
				waitModeCount++
			}
			if waitModeCount > 1 {
				return commandError("usage", "usage", "use only one submit-search wait mode: --wait-text, --wait-selector, --wait-url, --wait-url-contains, --wait-load-state, or --wait-response", ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --wait-url-contains /results --json"})
			}
			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --wait-text Results --poll 250ms --json"})
			}
			suggestionOpts := submitSearchSuggestionOptions{
				Query:         strings.TrimSpace(suggestionQuery),
				By:            normalizeLocatorStrategy(suggestionBy),
				Role:          strings.TrimSpace(suggestionRole),
				TestIDAttr:    strings.TrimSpace(suggestionTestIDAttr),
				Exact:         suggestionExact,
				IncludeHidden: suggestionIncludeHidden,
				Limit:         suggestionLimit,
				Strategy:      strings.ToLower(strings.TrimSpace(suggestionStrategy)),
			}
			if suggestionOpts.Strategy == "" {
				suggestionOpts.Strategy = "auto"
			}
			if suggestionOpts.Requested() {
				if err := validateLocatorFindOptions(suggestionOpts.By, suggestionOpts.Role, suggestionOpts.TestIDAttr, suggestionOpts.Limit); err != nil {
					return err
				}
				if suggestionOpts.Strategy != "auto" && suggestionOpts.Strategy != "dom" && suggestionOpts.Strategy != "raw-input" {
					return commandError("usage", "usage", "--suggestion-strategy must be auto, dom, or raw-input", ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --suggestion 'Aarhus Denmark' --suggestion-by text --json"})
				}
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, session, selectedTarget, err := a.attachPageEventSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			beforeTarget := selectedTarget

			selector, locator, err := resolveActionSelector(ctx, session, args[0], locatorOpts, "submit-search")
			if err != nil {
				return err
			}

			actionName := inputMode
			actionability, err := evaluateActionability(ctx, session, selector, actionName)
			if err != nil {
				return err
			}
			if actionability.Error != nil {
				return invalidSelectorError(selector, actionability.Error, "cdp workflow submit-search 'Search' query --by label --json")
			}
			prepareActionability(&actionability, actionName, false, force)
			if !actionability.Actionable {
				report := submitSearchBlockedReport(selectedTarget, selector, args[1], inputMode, inputStrategy, submit, submitKey, force, actionability)
				addWorkflowTargetIndex(report, targetIndex)
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage(actionName, selector, actionability), ExitCheckFailed, actionabilityRemediations(actionName, args[0], selector, locatorOpts), report)
			}

			var networkCriteria networkWaitCriteria
			var responseReport map[string]any
			var responseErr error
			if waitResponse {
				networkCriteria = networkWaitCriteria{
					URL:          waitResponseURL,
					URLContains:  waitResponseMatchURL,
					Method:       waitResponseMethod,
					ResourceType: waitResponseResourceType,
					Status:       waitResponseStatus,
					StatusMin:    waitResponseStatusMin,
					StatusMax:    waitResponseStatusMax,
					StatusSet:    cmd.Flags().Changed("wait-response-status"),
					StatusMinSet: cmd.Flags().Changed("wait-response-status-min"),
					StatusMaxSet: cmd.Flags().Changed("wait-response-status-max"),
				}
				if err := normalizeNetworkWaitCriteria(&networkCriteria); err != nil {
					return err
				}
				if _, err := networkWaitRedactor(waitResponseRedact); err != nil {
					return err
				}
				if err := setupNetworkEventWait(ctx, client, session.SessionID); err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("submit-search response wait target %s: %v", beforeTarget.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
				}
				if _, err := client.DrainEvents(ctx); err != nil {
					return commandError("connection_failed", "connection", fmt.Sprintf("submit-search response wait target %s: %v", beforeTarget.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
				}
			}

			eventWaitStart := time.Now()
			var fill *fillResult
			var typed *typeResult
			if inputMode == "fill" {
				var result fillResult
				if err := evaluateJSONValue(ctx, session, fillExpression(selector, args[1]), "submit-search fill", &result); err != nil {
					return err
				}
				result.Force = force
				if result.Error != nil {
					return commandError("invalid_selector", "usage", fmt.Sprintf("submit-search fill %q: %s", selector, result.Error.Message), ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --json"})
				}
				if !result.Filled {
					return commandError("invalid_selector", "usage", fmt.Sprintf("no editable element found for selector %q", selector), ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --json"})
				}
				fill = &result
			} else {
				result, err := performTextInput(ctx, session, selector, args[1], inputStrategy)
				if err != nil {
					return err
				}
				result.Force = force
				if result.Error != nil {
					return commandError("invalid_selector", "usage", fmt.Sprintf("submit-search type %q: %s", selector, result.Error.Message), ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --input-mode type --json"})
				}
				if !result.Typing {
					return commandError("invalid_selector", "usage", fmt.Sprintf("no editable element found for selector %q", selector), ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --input-mode type --json"})
				}
				typed = &result
			}

			var suggestion *locatorFindResult
			var suggestionSelector string
			var suggestionActionability *actionabilityResult
			var suggestionClick *clickResult
			if suggestionOpts.Requested() {
				var result locatorFindResult
				if err := evaluateJSONValue(ctx, session, locatorFindExpression(suggestionOpts.By, suggestionOpts.Query, suggestionOpts.Role, suggestionOpts.Exact, suggestionOpts.IncludeHidden, suggestionOpts.TestIDAttr, suggestionOpts.Limit), "submit-search suggestion", &result); err != nil {
					return err
				}
				suggestion = &result
				if result.Error != nil {
					report := submitSearchSuggestionFailureReport(beforeTarget, selector, args[1], inputMode, inputStrategy, submit, submitKey, poll, actionability, fill, typed, locator, result, "suggestion_invalid")
					addWorkflowTargetIndex(report, targetIndex)
					return commandErrorWithData("invalid_suggestion_locator", "usage", fmt.Sprintf("submit-search suggestion locator %s %q: %s", suggestionOpts.By, suggestionOpts.Query, result.Error.Message), ExitUsage, submitSearchSuggestionRemediations(suggestionOpts), report)
				}
				if result.Count == 0 || len(result.Matches) == 0 {
					report := submitSearchSuggestionFailureReport(beforeTarget, selector, args[1], inputMode, inputStrategy, submit, submitKey, poll, actionability, fill, typed, locator, result, "suggestion_not_found")
					addWorkflowTargetIndex(report, targetIndex)
					return commandErrorWithData("suggestion_not_found", "check_failed", fmt.Sprintf("submit-search suggestion %s %q matched no elements", suggestionOpts.By, suggestionOpts.Query), ExitCheckFailed, submitSearchSuggestionRemediations(suggestionOpts), report)
				}
				if result.Count != 1 || len(result.Matches) != 1 {
					report := submitSearchSuggestionFailureReport(beforeTarget, selector, args[1], inputMode, inputStrategy, submit, submitKey, poll, actionability, fill, typed, locator, result, "suggestion_ambiguous")
					addWorkflowTargetIndex(report, targetIndex)
					return commandErrorWithData("ambiguous_suggestion", "check_failed", fmt.Sprintf("submit-search suggestion %s %q matched %d elements; refine the suggestion locator before acting", suggestionOpts.By, suggestionOpts.Query, result.Count), ExitCheckFailed, submitSearchSuggestionRemediations(suggestionOpts), report)
				}
				match := result.Matches[0]
				suggestionSelector = strings.TrimSpace(match.SelectorHint)
				if suggestionSelector == "" || match.SelectorAmbiguous {
					report := submitSearchSuggestionFailureReport(beforeTarget, selector, args[1], inputMode, inputStrategy, submit, submitKey, poll, actionability, fill, typed, locator, result, "suggestion_ambiguous")
					addWorkflowTargetIndex(report, targetIndex)
					return commandErrorWithData("ambiguous_suggestion", "check_failed", fmt.Sprintf("submit-search suggestion %s %q matched one element but did not produce a unique CSS selector hint", suggestionOpts.By, suggestionOpts.Query), ExitCheckFailed, submitSearchSuggestionRemediations(suggestionOpts), report)
				}
				checks, err := evaluateActionability(ctx, session, suggestionSelector, "click")
				if err != nil {
					return err
				}
				if checks.Error != nil {
					report := submitSearchSuggestionFailureReport(beforeTarget, selector, args[1], inputMode, inputStrategy, submit, submitKey, poll, actionability, fill, typed, locator, result, "suggestion_invalid")
					addWorkflowTargetIndex(report, targetIndex)
					return commandErrorWithData("invalid_suggestion_selector", "usage", fmt.Sprintf("submit-search suggestion %q: %s", suggestionSelector, checks.Error.Message), ExitUsage, submitSearchSuggestionRemediations(suggestionOpts), report)
				}
				prepareActionability(&checks, "click", false, false)
				suggestionActionability = &checks
				if !checks.Actionable {
					report := submitSearchSuggestionFailureReport(beforeTarget, selector, args[1], inputMode, inputStrategy, submit, submitKey, poll, actionability, fill, typed, locator, result, "suggestion_blocked")
					addWorkflowTargetIndex(report, targetIndex)
					report["suggestion_actionability"] = checks
					return commandErrorWithData("suggestion_not_actionable", "check_failed", actionabilityFailureMessage("click", suggestionSelector, checks), ExitCheckFailed, actionabilityRemediations("click", suggestionOpts.Query, suggestionSelector, locatorActionOptions{By: suggestionOpts.By, Role: suggestionOpts.Role, Exact: suggestionOpts.Exact, IncludeHidden: suggestionOpts.IncludeHidden, TestIDAttr: suggestionOpts.TestIDAttr, Limit: suggestionOpts.Limit}), report)
				}
				click, err := performClick(ctx, session, suggestionSelector, suggestionOpts.Strategy)
				if err != nil {
					return err
				}
				if click.Error != nil {
					return commandError("invalid_suggestion_selector", "usage", fmt.Sprintf("submit-search suggestion click %q: %s", suggestionSelector, click.Error.Message), ExitUsage, submitSearchSuggestionRemediations(suggestionOpts))
				}
				if !click.Clicked {
					return commandError("suggestion_not_clicked", "check_failed", fmt.Sprintf("no suggestion target clicked for selector %q", suggestionSelector), ExitCheckFailed, submitSearchSuggestionRemediations(suggestionOpts))
				}
				suggestionClick = &click
			}

			var press *pressResult
			if submit == "enter" {
				var result pressResult
				if err := evaluateJSONValue(ctx, session, pressExpression(submitKey, selector), "submit-search press", &result); err != nil {
					return err
				}
				if result.Error != nil {
					return commandError("invalid_selector", "usage", fmt.Sprintf("submit-search press %q: %s", submitKey, result.Error.Message), ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --submit enter --json"})
				}
				if !result.Dispatched {
					return commandError("invalid_selector", "usage", fmt.Sprintf("no target found for submit key %q", submitKey), ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --submit enter --json"})
				}
				press = &result
			}

			verified := true
			var verification *waitResult
			waitCriteria := actionWaitCriteria{
				Text:        waitText,
				Selector:    waitSelector,
				URL:         waitURL,
				URLContains: waitURLContains,
			}
			if waitCriteria.Has() {
				wait, err := waitForActionVerification(ctx, session, poll, waitCriteria)
				if err != nil {
					return err
				}
				verified = wait.Matched
				verification = &wait
				submitSearchSetVerified(fill, typed, press, suggestionClick, verified)
				submitSearchApplyWaitURL(fill, typed, press, suggestionClick, wait)
			} else if loadState != "" {
				start := time.Now()
				wait, err := waitForLoadStateCondition(ctx, session, poll, loadState)
				wait.ElapsedMS = time.Since(start).Milliseconds()
				wait.PollInterval = poll.String()
				if err != nil && ctx.Err() == nil {
					return err
				}
				verified = err == nil && wait.Matched && wait.Error == nil
				verification = &wait
				submitSearchSetVerified(fill, typed, press, suggestionClick, verified)
				submitSearchApplyWaitURL(fill, typed, press, suggestionClick, wait)
			}
			if waitResponse {
				redactor, err := networkWaitRedactor(waitResponseRedact)
				if err != nil {
					return err
				}
				observation, err := collectNetworkEvent(ctx, client, session.SessionID, networkWaitKindResponse, networkCriteria)
				responseReport = networkWaitReport(networkWaitKindResponse, networkCriteria, observation, time.Since(eventWaitStart), a.effectiveNetworkWaitTimeout(), waitResponseRedact, redactor)
				responseReport["target"] = pageRow(beforeTarget)
				verified = observation.Matched
				submitSearchSetVerified(fill, typed, press, suggestionClick, verified)
				if err != nil {
					responseErr = err
				}
			}

			finalTarget, refreshErr := refreshedClickTarget(ctx, client, beforeTarget)
			if verification != nil && (verification.Kind == "url" || verification.Kind == "load-state") && strings.TrimSpace(verification.URL) != "" {
				finalTarget.URL = verification.URL
				if strings.TrimSpace(verification.Title) != "" {
					finalTarget.Title = verification.Title
				}
			}

			report := map[string]any{
				"ok":            verified,
				"action":        "submit_search",
				"target":        pageRow(finalTarget),
				"before_target": pageRow(beforeTarget),
				"after_target":  pageRow(finalTarget),
				"final_target":  pageRow(finalTarget),
				"page_state":    clickPageState(beforeTarget, finalTarget),
				"input": map[string]any{
					"mode":     inputMode,
					"selector": selector,
					"query":    args[1],
				},
				"workflow": map[string]any{
					"name":                 "submit-search",
					"input_mode":           inputMode,
					"input_strategy":       inputStrategy,
					"submit":               submit,
					"submit_key":           submitKey,
					"suggestion_requested": suggestionOpts.Requested(),
					"suggestion_selected":  suggestionClick != nil && suggestionClick.Clicked,
					"suggestion_strategy":  suggestionOpts.Strategy,
					"wait_requested":       waitCriteria.Has() || loadState != "" || waitResponse,
					"verified":             verified,
					"poll_interval":        poll.String(),
				},
				"actionability": actionability,
				"next_commands": submitSearchNextCommands(finalTarget.TargetID, selector),
			}
			if fill != nil {
				report["fill"] = fill
			}
			if typed != nil {
				report["type"] = typed
			}
			if press != nil {
				report["press"] = press
			}
			if suggestion != nil {
				report["suggestion"] = suggestion
				report["suggestion_selector"] = suggestionSelector
			}
			if suggestionActionability != nil {
				report["suggestion_actionability"] = suggestionActionability
			}
			if suggestionClick != nil {
				report["suggestion_click"] = suggestionClick
			}
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			addWorkflowTargetIndex(report, targetIndex)
			if verification != nil {
				report["verification"] = verification
			}
			if responseReport != nil {
				addNetworkWaitToClickReport(report, networkWaitKindResponse, responseReport)
			}
			if refreshErr != nil {
				report["target_refresh"] = map[string]any{
					"ok":        false,
					"target_id": beforeTarget.TargetID,
					"error":     refreshErr.Error(),
				}
			}
			if responseErr != nil {
				return networkWaitError(ctx, beforeTarget.TargetID, networkWaitKindResponse, networkCriteria, report, responseErr)
			}

			human := fmt.Sprintf("submit-search\t%s\t%s", finalTarget.TargetID, selector)
			if waitResponse {
				human = fmt.Sprintf("submit-search-response\t%s\t%s", finalTarget.TargetID, selector)
			}
			if !verified {
				human = fmt.Sprintf("submit-search-unverified\t%s\t%s", finalTarget.TargetID, selector)
			}
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the unique page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the unique page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based page target index")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().StringVar(&suggestionQuery, "suggestion", "", "optional suggestion locator query to click after input and before submit")
	cmd.Flags().StringVar(&suggestionBy, "suggestion-by", "text", "suggestion locator strategy: css, role, text, label, placeholder, alt, title, or test-id")
	cmd.Flags().StringVar(&suggestionRole, "suggestion-role", "", "ARIA role to match when --suggestion-by role is used")
	cmd.Flags().StringVar(&suggestionTestIDAttr, "suggestion-test-id-attr", "data-testid", "attribute name for --suggestion-by test-id")
	cmd.Flags().BoolVar(&suggestionExact, "suggestion-exact", false, "require exact normalized suggestion text/name/attribute match")
	cmd.Flags().BoolVar(&suggestionIncludeHidden, "suggestion-include-hidden", false, "include hidden suggestion locator matches")
	cmd.Flags().IntVar(&suggestionLimit, "suggestion-limit", 20, "maximum suggestion locator matches to inspect")
	cmd.Flags().StringVar(&suggestionStrategy, "suggestion-strategy", "auto", "suggestion click strategy: auto, dom, or raw-input")
	cmd.Flags().StringVar(&inputMode, "input-mode", "fill", "input mode before submit: fill or type")
	cmd.Flags().StringVar(&inputStrategy, "input-strategy", "auto", "text input strategy used when --input-mode type: auto, dom, or insert-text")
	cmd.Flags().StringVar(&submit, "submit", "enter", "submit mode after input: enter or none")
	cmd.Flags().StringVar(&submitKey, "submit-key", "Enter", "key to dispatch when --submit enter is used")
	cmd.Flags().BoolVar(&force, "force", false, "skip non-essential input actionability checks and record skipped checks in JSON")
	cmd.Flags().StringVar(&waitText, "wait-text", "", "verify by waiting until visible page text contains this string")
	cmd.Flags().StringVar(&waitSelector, "wait-selector", "", "verify by waiting until this CSS selector matches")
	cmd.Flags().StringVar(&waitURL, "wait-url", "", "verify by waiting until the page URL exactly matches this value")
	cmd.Flags().StringVar(&waitURLContains, "wait-url-contains", "", "verify by waiting until the page URL contains this string")
	cmd.Flags().StringVar(&waitLoadState, "wait-load-state", "", "verify by waiting for document load state: load or domcontentloaded")
	cmd.Flags().BoolVar(&waitResponse, "wait-response", false, "verify by waiting for a matching network response triggered by this workflow")
	cmd.Flags().StringVar(&waitResponseURL, "wait-response-url", "", "exact response URL to match; implies --wait-response")
	cmd.Flags().StringVar(&waitResponseMatchURL, "wait-response-match-url", "", "substring that the response URL must contain; implies --wait-response")
	cmd.Flags().StringVar(&waitResponseMethod, "wait-response-method", "", "HTTP method of the request to match when it was observed; implies --wait-response")
	cmd.Flags().StringVar(&waitResponseResourceType, "wait-response-resource-type", "", "CDP resource type to match, such as Document, Fetch, XHR, or Script; implies --wait-response")
	cmd.Flags().IntVar(&waitResponseStatus, "wait-response-status", 0, "exact HTTP status to match; implies --wait-response")
	cmd.Flags().IntVar(&waitResponseStatusMin, "wait-response-status-min", 0, "minimum HTTP status to match; implies --wait-response")
	cmd.Flags().IntVar(&waitResponseStatusMax, "wait-response-status-max", 0, "maximum HTTP status to match; implies --wait-response")
	cmd.Flags().StringVar(&waitResponseRedact, "wait-response-redact", "safe", "redaction preset for returned response URL: safe or none; implies --wait-response")
	cmd.Flags().DurationVar(&poll, "poll", 250*time.Millisecond, "poll interval while waiting for verification")
	return cmd
}

func submitSearchBlockedReport(target cdp.TargetInfo, selector, query, inputMode, inputStrategy, submit, submitKey string, force bool, actionability actionabilityResult) map[string]any {
	report := map[string]any{
		"ok":            false,
		"action":        "blocked",
		"target":        pageRow(target),
		"before_target": pageRow(target),
		"after_target":  pageRow(target),
		"final_target":  pageRow(target),
		"page_state":    clickPageState(target, target),
		"input": map[string]any{
			"mode":     inputMode,
			"selector": selector,
			"query":    query,
		},
		"workflow": map[string]any{
			"name":           "submit-search",
			"input_mode":     inputMode,
			"input_strategy": inputStrategy,
			"submit":         submit,
			"submit_key":     submitKey,
			"wait_requested": false,
			"verified":       false,
		},
		"actionability": actionability,
		"next_commands": submitSearchNextCommands("", selector),
	}
	if inputMode == "fill" {
		report["fill"] = fillResult{
			URL:      actionability.URL,
			Title:    actionability.Title,
			Selector: selector,
			Count:    actionability.Count,
			Filled:   false,
			Force:    force,
			Value:    query,
		}
	} else {
		report["type"] = typeResult{
			URL:      actionability.URL,
			Title:    actionability.Title,
			Selector: selector,
			Count:    actionability.Count,
			Typing:   false,
			Force:    force,
			Typed:    query,
			Strategy: inputStrategy,
		}
	}
	return report
}

type submitSearchSuggestionOptions struct {
	Query         string
	By            string
	Role          string
	TestIDAttr    string
	Exact         bool
	IncludeHidden bool
	Limit         int
	Strategy      string
}

func (o submitSearchSuggestionOptions) Requested() bool {
	return strings.TrimSpace(o.Query) != ""
}

func submitSearchSuggestionFailureReport(target cdp.TargetInfo, selector, query, inputMode, inputStrategy, submit, submitKey string, poll time.Duration, actionability actionabilityResult, fill *fillResult, typed *typeResult, locator *locatorFindResult, suggestion locatorFindResult, action string) map[string]any {
	report := map[string]any{
		"ok":            false,
		"action":        action,
		"target":        pageRow(target),
		"before_target": pageRow(target),
		"after_target":  pageRow(target),
		"final_target":  pageRow(target),
		"page_state":    clickPageState(target, target),
		"input": map[string]any{
			"mode":     inputMode,
			"selector": selector,
			"query":    query,
		},
		"workflow": map[string]any{
			"name":                 "submit-search",
			"input_mode":           inputMode,
			"input_strategy":       inputStrategy,
			"submit":               submit,
			"submit_key":           submitKey,
			"suggestion_requested": true,
			"suggestion_selected":  false,
			"wait_requested":       false,
			"verified":             false,
			"poll_interval":        poll.String(),
		},
		"actionability": actionability,
		"suggestion":    suggestion,
		"next_commands": submitSearchNextCommands(target.TargetID, selector),
	}
	if fill != nil {
		report["fill"] = fill
	}
	if typed != nil {
		report["type"] = typed
	}
	if locator != nil {
		report["locator"] = locator
		report["resolved_selector"] = selector
	}
	return report
}

func submitSearchSuggestionRemediations(opts submitSearchSuggestionOptions) []string {
	command := "cdp locator find " + shellQuote(opts.Query) + " --by " + opts.By
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
	return []string{command + " --json", "cdp snapshot --selector body --json"}
}

func submitSearchSetVerified(fill *fillResult, typed *typeResult, press *pressResult, suggestionClick *clickResult, verified bool) {
	if fill != nil {
		fill.Verified = &verified
	}
	if typed != nil {
		typed.Verified = &verified
	}
	if press != nil {
		press.Verified = &verified
	}
	if suggestionClick != nil {
		suggestionClick.Verified = &verified
	}
}

func submitSearchApplyWaitURL(fill *fillResult, typed *typeResult, press *pressResult, suggestionClick *clickResult, wait waitResult) {
	if strings.TrimSpace(wait.URL) == "" || wait.Kind != "url" && wait.Kind != "load-state" {
		return
	}
	if fill != nil {
		fill.URL = wait.URL
		fill.Title = wait.Title
	}
	if typed != nil {
		typed.URL = wait.URL
		typed.Title = wait.Title
	}
	if press != nil {
		press.URL = wait.URL
		press.Title = wait.Title
	}
	if suggestionClick != nil {
		suggestionClick.URL = wait.URL
		suggestionClick.Title = wait.Title
		suggestionClick.FinalURL = wait.URL
		suggestionClick.FinalTitle = wait.Title
	}
}

func submitSearchNextCommands(targetID, selector string) []string {
	commands := []string{"cdp pages --json"}
	if strings.TrimSpace(targetID) != "" {
		commands = append(commands,
			"cdp snapshot --target "+shellQuote(targetID)+" --selector body --json",
			"cdp text main --target "+shellQuote(targetID)+" --json",
		)
	}
	if strings.TrimSpace(selector) != "" {
		commands = append(commands, "cdp assert focused "+shellQuote(selector)+" --json")
	}
	return commands
}
