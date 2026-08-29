package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type stopStateInput struct {
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
	Text  string `json:"-"`
}

type stopStateRule struct {
	State       string `json:"state"`
	Class       string `json:"class"`
	Source      string `json:"source"`
	MatchType   string `json:"match_type"`
	Pattern     string `json:"pattern"`
	BuiltIn     bool   `json:"built_in"`
	NextCommand string `json:"-"`
}

type stopStateEvidence struct {
	Source    string `json:"source"`
	MatchType string `json:"match_type"`
	Pattern   string `json:"pattern"`
	Snippet   string `json:"snippet,omitempty"`
	BuiltIn   bool   `json:"built_in"`
}

type stopStateResult struct {
	OK               bool               `json:"ok"`
	Status           string             `json:"status"`
	StopState        string             `json:"stop_state,omitempty"`
	StopStateClass   string             `json:"stop_state_class,omitempty"`
	AgentShouldStop  bool               `json:"agent_should_stop,omitempty"`
	HumanRequired    bool               `json:"human_required,omitempty"`
	HumanAction      string             `json:"human_action,omitempty"`
	Evidence         *stopStateEvidence `json:"stop_state_evidence,omitempty"`
	MatchedRule      *stopStateRule     `json:"matched_rule,omitempty"`
	ConfiguredRules  []stopStateRule    `json:"configured_rules,omitempty"`
	NextCommands     []string           `json:"next_commands"`
	Remediation      []string           `json:"remediation_commands,omitempty"`
	Input            map[string]any     `json:"input"`
	ClassificationOK bool               `json:"classification_ok"`
	Target           map[string]any     `json:"target,omitempty"`
	TargetIndex      int                `json:"target_index,omitempty"`
}

type stopStateRuleOptions struct {
	TextContains  []string
	TitleContains []string
	URLContains   []string
	TextRegex     []string
	TitleRegex    []string
	URLRegex      []string
}

func (a *app) newStopStateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop-state",
		Short: "Classify browser stop states without bypassing them",
	}
	cmd.AddCommand(a.newStopStateClassifyCommand())
	return cmd
}

func (a *app) newStopStateClassifyCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var textInput string
	var titleInput string
	var urlInput string
	var ruleOpts stopStateRuleOptions
	cmd := &cobra.Command{
		Use:   "classify",
		Short: "Classify page text, title, or URL as a safe stop state",
		Long: `Classify page text, title, or URL as a safe stop state.

Built-in rules cover conservative browser boundaries such as login required,
access denied, unusual traffic, permission required, payment/booking boundary,
and personal-data prompts. Custom app-specific rules are explicit inputs using
state=pattern values, for example:

  cdp stop-state classify --text "$TEXT" --rule-text-contains google_page_error="Something went wrong" --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targetIndex, err := inputTargetIndex(cmd, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			rules, err := parseConfiguredStopStateRules(ruleOpts)
			if err != nil {
				return err
			}
			input := stopStateInput{
				URL:   strings.TrimSpace(urlInput),
				Title: strings.TrimSpace(titleInput),
				Text:  strings.TrimSpace(textInput),
			}
			pageBacked := input.URL == "" && input.Title == "" && input.Text == ""
			if !pageBacked && cmd.Flags().Changed("target-index") {
				return commandError("invalid_target_selector", "usage", "--target-index requires browser-backed classification without --text, --title, or --url", ExitUsage, []string{"cdp stop-state classify --target-index 2 --json"})
			}
			var target cdp.TargetInfo
			if input.URL == "" && input.Title == "" && input.Text == "" {
				pageInput, pageTarget, err := a.stopStateInputFromPage(ctx, targetID, urlContains, titleContains, targetIndex)
				if err != nil {
					return err
				}
				input = pageInput
				target = pageTarget
			}
			result, human := classifyStopState(input, rules)
			if pageBacked {
				result.Target = pageRow(target)
				if targetIndex > 0 {
					result.TargetIndex = targetIndex
				}
			}
			return a.render(ctx, human, result)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix to inspect when text/title/url are not supplied")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "inspect the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "inspect the first page whose title contains this text")
	cmd.Flags().StringVar(&textInput, "text", "", "page text to classify without attaching to a browser")
	cmd.Flags().StringVar(&titleInput, "title", "", "page title to classify without attaching to a browser")
	cmd.Flags().StringVar(&urlInput, "url", "", "page URL to classify without attaching to a browser")
	addInputTargetIndexFlag(cmd)
	addStopStateRuleFlags(cmd, &ruleOpts)
	return cmd
}

func (a *app) stopStateInputFromPage(ctx context.Context, targetID, urlContains, titleContains string, targetIndex int) (stopStateInput, cdp.TargetInfo, error) {
	session, target, err := a.attachPageSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
	if err != nil {
		return stopStateInput{}, cdp.TargetInfo{}, err
	}
	defer session.Close(ctx)
	input, err := stopStateInputFromSession(ctx, session)
	if err != nil {
		return stopStateInput{}, cdp.TargetInfo{}, err
	}
	if strings.TrimSpace(input.URL) == "" {
		input.URL = target.URL
	}
	if strings.TrimSpace(input.Title) == "" {
		input.Title = target.Title
	}
	return input, target, nil
}

func stopStateInputFromSession(ctx context.Context, session *cdp.PageSession) (stopStateInput, error) {
	var result struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	expression := `(() => ({url: location.href, title: document.title || "", text: (document.body && document.body.innerText || "").slice(0, 20000)}))()`
	if err := evaluateJSONValue(ctx, session, expression, "stop-state classify", &result); err != nil {
		return stopStateInput{}, err
	}
	return stopStateInput{URL: result.URL, Title: result.Title, Text: result.Text}, nil
}

func addStopStateRuleFlags(cmd *cobra.Command, opts *stopStateRuleOptions) {
	cmd.Flags().StringArrayVar(&opts.TextContains, "rule-text-contains", nil, "custom text contains rule as state=needle")
	cmd.Flags().StringArrayVar(&opts.TitleContains, "rule-title-contains", nil, "custom title contains rule as state=needle")
	cmd.Flags().StringArrayVar(&opts.URLContains, "rule-url-contains", nil, "custom URL contains rule as state=needle")
	cmd.Flags().StringArrayVar(&opts.TextRegex, "rule-text-regex", nil, "custom text regex rule as state=regex")
	cmd.Flags().StringArrayVar(&opts.TitleRegex, "rule-title-regex", nil, "custom title regex rule as state=regex")
	cmd.Flags().StringArrayVar(&opts.URLRegex, "rule-url-regex", nil, "custom URL regex rule as state=regex")
}

func classifyStopStateForSession(ctx context.Context, session *cdp.PageSession, rules []stopStateRule) (*stopStateResult, error) {
	input, err := stopStateInputFromSession(ctx, session)
	if err != nil {
		return nil, err
	}
	result, _ := classifyStopState(input, rules)
	return &result, nil
}

func attachStopStateResultToReport(report map[string]any, result *stopStateResult) {
	if result == nil || !result.AgentShouldStop {
		return
	}
	report["stop_state"] = result.StopState
	report["stop_state_class"] = result.StopStateClass
	report["agent_should_stop"] = result.AgentShouldStop
	report["human_required"] = result.HumanRequired
	report["human_action"] = result.HumanAction
	report["stop_state_result"] = result
	report["stop_state_evidence"] = result.Evidence
	report["matched_stop_state_rule"] = result.MatchedRule
	report["next_commands"] = result.NextCommands
	report["remediation_commands"] = result.Remediation
}

func classifyStopState(input stopStateInput, configured []stopStateRule) (stopStateResult, string) {
	rules := append([]stopStateRule{}, configured...)
	rules = append(rules, builtInStopStateRules()...)
	result := stopStateResult{
		OK:               true,
		Status:           "ok",
		ConfiguredRules:  configured,
		NextCommands:     []string{"cdp pages --json", "cdp stop-state classify --target <target-id> --json"},
		Input:            stopStateInputSummary(input),
		ClassificationOK: true,
	}
	for _, rule := range rules {
		if evidence, ok := matchStopStateRule(input, rule); ok {
			ruleCopy := rule
			result.Status = "blocked"
			result.StopState = rule.State
			result.StopStateClass = rule.Class
			result.AgentShouldStop = true
			result.Evidence = &evidence
			result.MatchedRule = &ruleCopy
			result.Remediation = stopStateRemediationCommands(rule)
			result.NextCommands = result.Remediation
			if stopStateNeedsHuman(rule) {
				result.HumanRequired = true
				result.HumanAction = stopStateHumanAction(rule)
			}
			return result, fmt.Sprintf("blocked\t%s\t%s", rule.State, rule.Class)
		}
	}
	return result, "ok\tno stop state"
}

func builtInStopStateRules() []stopStateRule {
	return []stopStateRule{
		{State: "unusual_traffic", Class: "bot_check", Source: "text", MatchType: "contains", Pattern: "unusual traffic", BuiltIn: true},
		{State: "unusual_traffic", Class: "bot_check", Source: "text", MatchType: "contains", Pattern: "captcha", BuiltIn: true},
		{State: "unusual_traffic", Class: "bot_check", Source: "text", MatchType: "contains", Pattern: "verify you are human", BuiltIn: true},
		{State: "unusual_traffic", Class: "bot_check", Source: "text", MatchType: "contains", Pattern: "automated queries", BuiltIn: true},
		{State: "human_required", Class: "human_required", Source: "text", MatchType: "contains", Pattern: "manual action required", BuiltIn: true},
		{State: "human_required", Class: "human_required", Source: "text", MatchType: "contains", Pattern: "human review required", BuiltIn: true},
		{State: "access_denied", Class: "access_denied", Source: "text", MatchType: "contains", Pattern: "access denied", BuiltIn: true},
		{State: "access_denied", Class: "access_denied", Source: "text", MatchType: "contains", Pattern: "403 forbidden", BuiltIn: true},
		{State: "access_denied", Class: "access_denied", Source: "text", MatchType: "contains", Pattern: "not authorized", BuiltIn: true},
		{State: "login_required", Class: "auth", Source: "text", MatchType: "contains", Pattern: "sign in to continue", BuiltIn: true},
		{State: "login_required", Class: "auth", Source: "text", MatchType: "contains", Pattern: "login required", BuiltIn: true},
		{State: "login_required", Class: "auth", Source: "text", MatchType: "contains", Pattern: "log in to continue", BuiltIn: true},
		{State: "permission_required", Class: "permission", Source: "text", MatchType: "contains", Pattern: "permission required", BuiltIn: true},
		{State: "permission_required", Class: "permission", Source: "text", MatchType: "contains", Pattern: "browser permission", BuiltIn: true},
		{State: "payment_or_booking_boundary", Class: "payment", Source: "text", MatchType: "contains", Pattern: "payment details", BuiltIn: true},
		{State: "payment_or_booking_boundary", Class: "payment", Source: "text", MatchType: "contains", Pattern: "continue to payment", BuiltIn: true},
		{State: "payment_or_booking_boundary", Class: "payment", Source: "text", MatchType: "contains", Pattern: "complete booking", BuiltIn: true},
		{State: "payment_or_booking_boundary", Class: "payment", Source: "text", MatchType: "contains", Pattern: "provider checkout", BuiltIn: true},
		{State: "personal_data_required", Class: "personal_data", Source: "text", MatchType: "contains", Pattern: "passenger details", BuiltIn: true},
		{State: "personal_data_required", Class: "personal_data", Source: "text", MatchType: "contains", Pattern: "passport number", BuiltIn: true},
		{State: "personal_data_required", Class: "personal_data", Source: "text", MatchType: "contains", Pattern: "date of birth", BuiltIn: true},
		{State: "personal_data_required", Class: "personal_data", Source: "text", MatchType: "contains", Pattern: "personal information", BuiltIn: true},
		{State: "personal_data_required", Class: "personal_data", Source: "text", MatchType: "contains", Pattern: "contact details required", BuiltIn: true},
		{State: "access_denied", Class: "access_denied", Source: "title", MatchType: "contains", Pattern: "access denied", BuiltIn: true},
		{State: "login_required", Class: "auth", Source: "title", MatchType: "contains", Pattern: "sign in", BuiltIn: true},
		{State: "unusual_traffic", Class: "bot_check", Source: "url", MatchType: "contains", Pattern: "/sorry/", BuiltIn: true},
	}
}

func parseConfiguredStopStateRules(opts stopStateRuleOptions) ([]stopStateRule, error) {
	var rules []stopStateRule
	addContains := func(source string, values []string) error {
		for _, value := range values {
			state, pattern, err := parseStopStateRuleValue(value)
			if err != nil {
				return err
			}
			rules = append(rules, stopStateRule{State: state, Class: "custom", Source: source, MatchType: "contains", Pattern: pattern})
		}
		return nil
	}
	addRegex := func(source string, values []string) error {
		for _, value := range values {
			state, pattern, err := parseStopStateRuleValue(value)
			if err != nil {
				return err
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return commandError("invalid_stop_state_rule", "usage", fmt.Sprintf("invalid %s regex for stop state %s: %v", source, state, err), ExitUsage, []string{"cdp stop-state classify --rule-text-regex app_block='blocked|unavailable' --json"})
			}
			rules = append(rules, stopStateRule{State: state, Class: "custom", Source: source, MatchType: "regex", Pattern: pattern})
		}
		return nil
	}
	for _, item := range []struct {
		source string
		values []string
	}{
		{"text", opts.TextContains},
		{"title", opts.TitleContains},
		{"url", opts.URLContains},
	} {
		if err := addContains(item.source, item.values); err != nil {
			return nil, err
		}
	}
	for _, item := range []struct {
		source string
		values []string
	}{
		{"text", opts.TextRegex},
		{"title", opts.TitleRegex},
		{"url", opts.URLRegex},
	} {
		if err := addRegex(item.source, item.values); err != nil {
			return nil, err
		}
	}
	return rules, nil
}

func parseStopStateRuleValue(value string) (string, string, error) {
	state, pattern, ok := strings.Cut(value, "=")
	state = strings.TrimSpace(state)
	pattern = strings.TrimSpace(pattern)
	if !ok || state == "" || pattern == "" {
		return "", "", commandError("invalid_stop_state_rule", "usage", "stop-state rules must use state=pattern", ExitUsage, []string{"cdp stop-state classify --rule-text-contains app_block='Something went wrong' --json"})
	}
	return state, pattern, nil
}

func matchStopStateRule(input stopStateInput, rule stopStateRule) (stopStateEvidence, bool) {
	value := stopStateSourceValue(input, rule.Source)
	if strings.TrimSpace(value) == "" {
		return stopStateEvidence{}, false
	}
	matched := false
	switch rule.MatchType {
	case "contains":
		matched = strings.Contains(strings.ToLower(value), strings.ToLower(rule.Pattern))
	case "regex":
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return stopStateEvidence{}, false
		}
		matched = re.MatchString(value)
	}
	if !matched {
		return stopStateEvidence{}, false
	}
	return stopStateEvidence{
		Source:    rule.Source,
		MatchType: rule.MatchType,
		Pattern:   rule.Pattern,
		Snippet:   stopStateSnippet(value, rule.Pattern),
		BuiltIn:   rule.BuiltIn,
	}, true
}

func stopStateSourceValue(input stopStateInput, source string) string {
	switch source {
	case "url":
		return input.URL
	case "title":
		return input.Title
	default:
		return input.Text
	}
}

func stopStateInputSummary(input stopStateInput) map[string]any {
	return map[string]any{
		"url":          input.URL,
		"title":        input.Title,
		"text_present": strings.TrimSpace(input.Text) != "",
		"text_bytes":   len(input.Text),
	}
}

func stopStateSnippet(value, pattern string) string {
	lowerValue := strings.ToLower(value)
	lowerPattern := strings.ToLower(pattern)
	idx := strings.Index(lowerValue, lowerPattern)
	if idx < 0 {
		if len(value) > 160 {
			return value[:160]
		}
		return value
	}
	start := idx - 60
	if start < 0 {
		start = 0
	}
	end := idx + len(pattern) + 60
	if end > len(value) {
		end = len(value)
	}
	return strings.TrimSpace(value[start:end])
}

func stopStateRemediationCommands(rule stopStateRule) []string {
	switch rule.Class {
	case "bot_check":
		return []string{"cdp --browser-mode headed browser preflight --json", "cdp pages --json"}
	case "permission":
		return []string{"cdp daemon status --json", "cdp browser preflight --json"}
	case "auth":
		return []string{"cdp --browser-mode headed daemon status --json", "cdp pages --json"}
	case "access_denied":
		return []string{"cdp pages --json", "cdp workflow debug-bundle --out-dir tmp/debug-bundle --json"}
	case "payment", "personal_data":
		return []string{"cdp pages --json"}
	default:
		return []string{"cdp pages --json", "cdp stop-state classify --target <target-id> --json"}
	}
}

func stopStateNeedsHuman(rule stopStateRule) bool {
	switch rule.Class {
	case "auth", "bot_check", "human_required", "payment", "permission", "personal_data":
		return true
	default:
		return false
	}
}

func stopStateHumanAction(rule stopStateRule) string {
	switch rule.Class {
	case "auth":
		return "A human must decide whether to sign in before continuing."
	case "bot_check":
		return "A human must decide whether and how to proceed past the bot-check boundary."
	case "human_required":
		return "A human decision is required before continuing."
	case "payment":
		return "A human must decide whether to proceed past the payment or booking boundary."
	case "personal_data":
		return "A human must decide whether to enter personal data before continuing."
	case "permission":
		return "A human must approve the browser permission before continuing."
	default:
		return "A human decision is required before continuing."
	}
}

func stopStateForCommandError(err *CommandError) *stopStateResult {
	if err == nil {
		return nil
	}
	var result *stopStateResult
	switch {
	case err.Code == "permission_pending" || err.Class == "permission":
		result = &stopStateResult{
			OK:              true,
			Status:          "blocked",
			StopState:       "permission_pending",
			StopStateClass:  "permission",
			AgentShouldStop: true,
			HumanRequired:   true,
			HumanAction:     autoConnectHumanAction,
			NextCommands:    permissionRemediationCommands(),
			Remediation:     permissionRemediationCommands(),
		}
	case err.Code == "browser_resource_budget_exceeded" || err.Class == "resource_budget":
		result = &stopStateResult{
			OK:              true,
			Status:          "blocked",
			StopState:       "browser_resource_budget_exceeded",
			StopStateClass:  "resource_budget",
			AgentShouldStop: true,
			NextCommands:    []string{"cdp pages --json", "cdp page cleanup --workflow-created --close --json", "cdp doctor --check browser-budget --json"},
			Remediation:     []string{"cdp pages --json", "cdp page cleanup --workflow-created --close --json", "cdp doctor --check browser-budget --json"},
		}
	}
	return result
}
