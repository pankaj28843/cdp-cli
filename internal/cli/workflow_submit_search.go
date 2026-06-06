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
	var locatorOpts locatorActionOptions
	var inputMode string
	var inputStrategy string
	var submit string
	var submitKey string
	var force bool
	var waitText string
	var waitSelector string
	var waitURL string
	var waitURLContains string
	var poll time.Duration
	cmd := &cobra.Command{
		Use:   "submit-search <input-selector-or-locator> <query>",
		Short: "Fill or type into a search input, optionally submit with Enter, and verify the resulting page state",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if stateWaitCount > 1 {
				return commandError("usage", "usage", "use only one submit-search wait mode: --wait-text, --wait-selector, --wait-url, or --wait-url-contains", ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --wait-url-contains /results --json"})
			}
			if poll <= 0 {
				return commandError("usage", "usage", "--poll must be positive", ExitUsage, []string{"cdp workflow submit-search 'Search' query --by label --wait-text Results --poll 250ms --json"})
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, session, selectedTarget, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
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
				if locator != nil {
					report["locator"] = locator
					report["resolved_selector"] = selector
				}
				return commandErrorWithData("actionability_failed", "check_failed", actionabilityFailureMessage(actionName, selector, actionability), ExitCheckFailed, actionabilityRemediations(actionName, args[0], selector, locatorOpts), report)
			}

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
				submitSearchSetVerified(fill, typed, press, verified)
				submitSearchApplyWaitURL(fill, typed, press, wait)
			}

			finalTarget, refreshErr := refreshedClickTarget(ctx, client, beforeTarget)
			if verification != nil && verification.Kind == "url" && strings.TrimSpace(verification.URL) != "" {
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
					"name":           "submit-search",
					"input_mode":     inputMode,
					"input_strategy": inputStrategy,
					"submit":         submit,
					"submit_key":     submitKey,
					"wait_requested": waitCriteria.Has(),
					"verified":       verified,
					"poll_interval":  poll.String(),
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
			if locator != nil {
				report["locator"] = locator
				report["resolved_selector"] = selector
			}
			if verification != nil {
				report["verification"] = verification
			}
			if refreshErr != nil {
				report["target_refresh"] = map[string]any{
					"ok":        false,
					"target_id": beforeTarget.TargetID,
					"error":     refreshErr.Error(),
				}
			}

			human := fmt.Sprintf("submit-search\t%s\t%s", finalTarget.TargetID, selector)
			if !verified {
				human = fmt.Sprintf("submit-search-unverified\t%s\t%s", finalTarget.TargetID, selector)
			}
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	addLocatorActionFlags(cmd, &locatorOpts)
	cmd.Flags().StringVar(&inputMode, "input-mode", "fill", "input mode before submit: fill or type")
	cmd.Flags().StringVar(&inputStrategy, "input-strategy", "auto", "text input strategy used when --input-mode type: auto, dom, or insert-text")
	cmd.Flags().StringVar(&submit, "submit", "enter", "submit mode after input: enter or none")
	cmd.Flags().StringVar(&submitKey, "submit-key", "Enter", "key to dispatch when --submit enter is used")
	cmd.Flags().BoolVar(&force, "force", false, "skip non-essential input actionability checks and record skipped checks in JSON")
	cmd.Flags().StringVar(&waitText, "wait-text", "", "verify by waiting until visible page text contains this string")
	cmd.Flags().StringVar(&waitSelector, "wait-selector", "", "verify by waiting until this CSS selector matches")
	cmd.Flags().StringVar(&waitURL, "wait-url", "", "verify by waiting until the page URL exactly matches this value")
	cmd.Flags().StringVar(&waitURLContains, "wait-url-contains", "", "verify by waiting until the page URL contains this string")
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

func submitSearchSetVerified(fill *fillResult, typed *typeResult, press *pressResult, verified bool) {
	if fill != nil {
		fill.Verified = &verified
	}
	if typed != nil {
		typed.Verified = &verified
	}
	if press != nil {
		press.Verified = &verified
	}
}

func submitSearchApplyWaitURL(fill *fillResult, typed *typeResult, press *pressResult, wait waitResult) {
	if wait.Kind != "url" || strings.TrimSpace(wait.URL) == "" {
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
