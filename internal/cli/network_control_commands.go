package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const (
	maxNetworkControlPatterns = 50
	maxNetworkMockRules       = 50
	maxNetworkMockBodyBytes   = 256 * 1024
)

type networkControlCleanup struct {
	Attempted          bool     `json:"attempted"`
	BlockedURLsCleared bool     `json:"blocked_urls_cleared,omitempty"`
	PendingReleased    int      `json:"pending_released"`
	FetchDisabled      bool     `json:"fetch_disabled,omitempty"`
	NetworkDisabled    bool     `json:"network_disabled,omitempty"`
	Complete           bool     `json:"complete"`
	Errors             []string `json:"errors,omitempty"`
}

type networkMockRule struct {
	ID           string            `json:"id,omitempty"`
	URLPattern   string            `json:"url_pattern"`
	Method       string            `json:"method,omitempty"`
	ResourceType string            `json:"resource_type,omitempty"`
	Status       int               `json:"status"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body,omitempty"`
	MaxMatches   int               `json:"max_matches,omitempty"`
	Matched      int               `json:"-"`
	matcher      *regexp.Regexp
}

func (a *app) newNetworkBlockCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var targetIndex int
	var patterns []string
	var duration time.Duration
	cmd := &cobra.Command{
		Use:   "block",
		Short: "Temporarily block explicit request URL patterns",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			patterns = normalizedNonEmptyStrings(patterns)
			if len(patterns) == 0 || len(patterns) > maxNetworkControlPatterns || duration <= 0 {
				return commandError("usage", "usage", fmt.Sprintf("--pattern is required (maximum %d) and --duration must be positive", maxNetworkControlPatterns), ExitUsage, []string{"cdp network block --pattern '*://*/analytics/*' --duration 10s --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, duration+10*time.Second)
			defer cancel()
			client, session, target, err := a.attachPageEventSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
			if err != nil {
				return err
			}
			defer session.Close(context.Background())

			cleanup := networkControlCleanup{}
			cleanupNeeded := false
			defer func() {
				if cleanupNeeded {
					cleanupNetworkBlock(client, session.SessionID, &cleanup)
				}
			}()
			if err := client.CallSession(ctx, session.SessionID, "Network.enable", map[string]any{}, nil); err != nil {
				return networkControlFailure("enable network blocking", err, target.TargetID, patterns, duration, cleanup)
			}
			cleanupNeeded = true
			if err := client.CallSession(ctx, session.SessionID, "Network.setBlockedURLs", map[string]any{"urls": patterns}, nil); err != nil {
				cleanupNetworkBlock(client, session.SessionID, &cleanup)
				cleanupNeeded = false
				return networkControlFailure("set blocked URL patterns", err, target.TargetID, patterns, duration, cleanup)
			}

			matched, collectErr := collectBlockedRequests(ctx, client, session.SessionID, duration)
			cleanupNetworkBlock(client, session.SessionID, &cleanup)
			cleanupNeeded = false
			report := map[string]any{
				"ok":            collectErr == nil && cleanup.Complete,
				"target":        pageRow(target),
				"matched_count": matched,
				"rules":         networkBlockRuleSummaries(patterns),
				"cleanup":       cleanup,
				"control":       map[string]any{"kind": "block", "duration": durationString(duration), "bounded": true},
				"next_commands": []string{"cdp network --failed --wait 2s --json", "cdp network block --help", "cdp doctor --json"},
			}
			if targetIndex > 0 {
				report["target_index"] = targetIndex
			}
			if collectErr != nil {
				return commandErrorWithData("network_control_failed", "connection", fmt.Sprintf("block requests for target %s: %v", target.TargetID, collectErr), ExitConnection, toStringSlice(report["next_commands"]), report)
			}
			if !cleanup.Complete {
				return commandErrorWithData("network_cleanup_failed", "connection", "network blocking ended but cleanup was incomplete", ExitConnection, toStringSlice(report["next_commands"]), report)
			}
			return a.render(ctx, fmt.Sprintf("network-block\t%d matched", matched), report)
		},
	}
	cmd.Flags().StringArrayVar(&patterns, "pattern", nil, "explicit CDP URL pattern to block; repeat for multiple patterns")
	cmd.Flags().DurationVar(&duration, "duration", 10*time.Second, "positive bounded blocking window")
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based existing page target index; workers do not consume indexes")
	return cmd
}

func (a *app) newNetworkMockCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var targetIndex int
	var rawRules []string
	var duration time.Duration
	cmd := &cobra.Command{
		Use:   "mock",
		Short: "Temporarily fulfill matching requests with bounded mock responses",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			rules, err := parseNetworkMockRules(rawRules)
			if err != nil || duration <= 0 {
				message := "--duration must be positive"
				if err != nil {
					message = err.Error()
				}
				return commandError("usage", "usage", message, ExitUsage, []string{`cdp network mock --rule '{"url_pattern":"*://*/api/config","status":200,"body":"{\"enabled\":true}","max_matches":1}' --duration 10s --json`})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, duration+10*time.Second)
			defer cancel()
			client, session, target, err := a.attachPageEventSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
			if err != nil {
				return err
			}
			defer session.Close(context.Background())

			pending := map[string]bool{}
			cleanup := networkControlCleanup{}
			cleanupNeeded := false
			defer func() {
				if cleanupNeeded {
					cleanupFetchInterception(client, session.SessionID, pending, &cleanup)
				}
			}()
			patterns := make([]map[string]any, 0, len(rules))
			for _, rule := range rules {
				pattern := map[string]any{"urlPattern": rule.URLPattern, "requestStage": "Request"}
				if rule.ResourceType != "" {
					pattern["resourceType"] = rule.ResourceType
				}
				patterns = append(patterns, pattern)
			}
			if err := client.CallSession(ctx, session.SessionID, "Fetch.enable", map[string]any{"patterns": patterns}, nil); err != nil {
				return networkControlFailure("enable Fetch mocking", err, target.TargetID, networkMockRuleSummaries(rules), duration, cleanup)
			}
			cleanupNeeded = true
			actions, collectErr := collectMockedRequests(ctx, client, session.SessionID, duration, rules, pending)
			cleanupFetchInterception(client, session.SessionID, pending, &cleanup)
			cleanupNeeded = false
			matched := 0
			for _, rule := range rules {
				matched += rule.Matched
			}
			report := map[string]any{
				"ok":            collectErr == nil && cleanup.Complete,
				"target":        pageRow(target),
				"matched_count": matched,
				"rules":         networkMockRuleSummaries(rules),
				"actions":       actions,
				"cleanup":       cleanup,
				"control":       map[string]any{"kind": "mock", "duration": durationString(duration), "bounded": true},
				"next_commands": []string{"cdp network capture --wait 2s --redact safe --json", "cdp network mock --help", "cdp doctor --json"},
			}
			if targetIndex > 0 {
				report["target_index"] = targetIndex
			}
			if collectErr != nil {
				return commandErrorWithData("network_control_failed", "connection", fmt.Sprintf("mock requests for target %s: %v", target.TargetID, collectErr), ExitConnection, toStringSlice(report["next_commands"]), report)
			}
			if !cleanup.Complete {
				return commandErrorWithData("network_cleanup_failed", "connection", "request mocking ended but cleanup was incomplete", ExitConnection, toStringSlice(report["next_commands"]), report)
			}
			return a.render(ctx, fmt.Sprintf("network-mock\t%d matched", matched), report)
		},
	}
	cmd.Flags().StringArrayVar(&rawRules, "rule", nil, "bounded JSON mock rule; repeat for multiple rules")
	cmd.Flags().DurationVar(&duration, "duration", 10*time.Second, "positive bounded mocking window")
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based existing page target index; workers do not consume indexes")
	return cmd
}

func collectBlockedRequests(ctx context.Context, client browserEventClient, sessionID string, duration time.Duration) (int, error) {
	requests := map[string]string{}
	matched := 0
	eventCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	for {
		event, err := client.ReadEvent(eventCtx)
		if err != nil {
			if ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded)) {
				return matched, nil
			}
			return matched, err
		}
		if event.SessionID != "" && event.SessionID != sessionID {
			continue
		}
		switch event.Method {
		case "Network.requestWillBeSent":
			var params struct {
				RequestID string `json:"requestId"`
				Request   struct {
					URL string `json:"url"`
				} `json:"request"`
			}
			if json.Unmarshal(event.Params, &params) == nil {
				requests[params.RequestID] = params.Request.URL
			}
		case "Network.loadingFailed":
			var params struct {
				RequestID     string `json:"requestId"`
				BlockedReason string `json:"blockedReason"`
			}
			if json.Unmarshal(event.Params, &params) == nil && params.BlockedReason != "" {
				matched++
				delete(requests, params.RequestID)
			}
		}
	}
}

func collectMockedRequests(ctx context.Context, client browserEventClient, sessionID string, duration time.Duration, rules []*networkMockRule, pending map[string]bool) (map[string]int, error) {
	actions := map[string]int{"fulfilled": 0, "continued": 0, "fallback_continued": 0, "failed": 0}
	eventCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	for {
		event, err := client.ReadEvent(eventCtx)
		if err != nil {
			if ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded)) {
				return actions, nil
			}
			return actions, err
		}
		if event.Method != "Fetch.requestPaused" || (event.SessionID != "" && event.SessionID != sessionID) {
			continue
		}
		var params struct {
			RequestID    string `json:"requestId"`
			ResourceType string `json:"resourceType"`
			Request      struct {
				URL    string `json:"url"`
				Method string `json:"method"`
			} `json:"request"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil || params.RequestID == "" {
			continue
		}
		pending[params.RequestID] = true
		rule := matchingNetworkMockRule(rules, params.Request.URL, params.Request.Method, params.ResourceType)
		method := "Fetch.continueRequest"
		callParams := map[string]any{"requestId": params.RequestID}
		action := "continued"
		if rule != nil {
			method = "Fetch.fulfillRequest"
			action = "fulfilled"
			callParams["responseCode"] = rule.Status
			callParams["responseHeaders"] = networkMockHeaders(rule.Headers)
			callParams["body"] = base64.StdEncoding.EncodeToString([]byte(rule.Body))
		}
		if err := client.CallSession(eventCtx, sessionID, method, callParams, nil); err != nil {
			actions["failed"]++
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
			fallbackErr := client.CallSession(cleanupCtx, sessionID, "Fetch.continueRequest", map[string]any{"requestId": params.RequestID}, nil)
			cleanupCancel()
			if fallbackErr != nil {
				return actions, fmt.Errorf("resolve paused request %s with %s: %v; fail-open continue: %v", params.RequestID, method, err, fallbackErr)
			}
			delete(pending, params.RequestID)
			actions["fallback_continued"]++
			continue
		}
		delete(pending, params.RequestID)
		actions[action]++
		if rule != nil {
			rule.Matched++
		}
	}
}

func cleanupNetworkBlock(client browserEventClient, sessionID string, cleanup *networkControlCleanup) {
	cleanup.Attempted = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.CallSession(ctx, sessionID, "Network.setBlockedURLs", map[string]any{"urls": []string{}}, nil); err != nil {
		cleanup.Errors = append(cleanup.Errors, "clear blocked URLs: "+err.Error())
	} else {
		cleanup.BlockedURLsCleared = true
	}
	if err := client.CallSession(ctx, sessionID, "Network.disable", map[string]any{}, nil); err != nil {
		cleanup.Errors = append(cleanup.Errors, "disable Network: "+err.Error())
	} else {
		cleanup.NetworkDisabled = true
	}
	cleanup.Complete = cleanup.BlockedURLsCleared && cleanup.NetworkDisabled && len(cleanup.Errors) == 0
}

func cleanupFetchInterception(client browserEventClient, sessionID string, pending map[string]bool, cleanup *networkControlCleanup) {
	cleanup.Attempted = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := client.CallSession(ctx, sessionID, "Fetch.continueRequest", map[string]any{"requestId": id}, nil); err != nil {
			cleanup.Errors = append(cleanup.Errors, "release paused request "+id+": "+err.Error())
			continue
		}
		delete(pending, id)
		cleanup.PendingReleased++
	}
	if err := client.CallSession(ctx, sessionID, "Fetch.disable", map[string]any{}, nil); err != nil {
		cleanup.Errors = append(cleanup.Errors, "disable Fetch: "+err.Error())
	} else {
		cleanup.FetchDisabled = true
	}
	cleanup.Complete = cleanup.FetchDisabled && len(pending) == 0 && len(cleanup.Errors) == 0
}

func parseNetworkMockRules(rawRules []string) ([]*networkMockRule, error) {
	if len(rawRules) == 0 || len(rawRules) > maxNetworkMockRules {
		return nil, fmt.Errorf("--rule is required and accepts at most %d rules", maxNetworkMockRules)
	}
	rules := make([]*networkMockRule, 0, len(rawRules))
	for i, raw := range rawRules {
		var rule networkMockRule
		if err := json.Unmarshal([]byte(raw), &rule); err != nil {
			return nil, fmt.Errorf("decode --rule %d: %w", i+1, err)
		}
		rule.ID = strings.TrimSpace(rule.ID)
		if rule.ID == "" {
			rule.ID = fmt.Sprintf("rule-%d", i+1)
		}
		rule.URLPattern = strings.TrimSpace(rule.URLPattern)
		rule.Method = strings.ToUpper(strings.TrimSpace(rule.Method))
		rule.ResourceType = strings.TrimSpace(rule.ResourceType)
		if rule.URLPattern == "" || rule.Status < 100 || rule.Status > 599 {
			return nil, fmt.Errorf("--rule %d requires url_pattern and status between 100 and 599", i+1)
		}
		if len([]byte(rule.Body)) > maxNetworkMockBodyBytes {
			return nil, fmt.Errorf("--rule %d body exceeds %d bytes", i+1, maxNetworkMockBodyBytes)
		}
		if rule.MaxMatches == 0 {
			rule.MaxMatches = 1
		}
		if rule.MaxMatches < 1 || rule.MaxMatches > 1000 {
			return nil, fmt.Errorf("--rule %d max_matches must be between 1 and 1000", i+1)
		}
		matcher, err := compileCDPURLPattern(rule.URLPattern)
		if err != nil {
			return nil, fmt.Errorf("--rule %d url_pattern: %w", i+1, err)
		}
		rule.matcher = matcher
		rules = append(rules, &rule)
	}
	return rules, nil
}

func compileCDPURLPattern(pattern string) (*regexp.Regexp, error) {
	quoted := regexp.QuoteMeta(pattern)
	quoted = strings.ReplaceAll(quoted, `\*`, `.*`)
	quoted = strings.ReplaceAll(quoted, `\?`, `.`)
	return regexp.Compile("^" + quoted + "$")
}

func matchingNetworkMockRule(rules []*networkMockRule, url, method, resourceType string) *networkMockRule {
	for _, rule := range rules {
		if rule.Matched >= rule.MaxMatches || !rule.matcher.MatchString(url) {
			continue
		}
		if rule.Method != "" && !strings.EqualFold(rule.Method, method) {
			continue
		}
		if rule.ResourceType != "" && !strings.EqualFold(rule.ResourceType, resourceType) {
			continue
		}
		return rule
	}
	return nil
}

func networkMockHeaders(headers map[string]string) []map[string]string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]string, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]string{"name": name, "value": headers[name]})
	}
	return out
}

func networkBlockRuleSummaries(patterns []string) []map[string]any {
	out := make([]map[string]any, 0, len(patterns))
	for i, pattern := range patterns {
		out = append(out, map[string]any{"id": fmt.Sprintf("rule-%d", i+1), "url_pattern": pattern})
	}
	return out
}

func networkMockRuleSummaries(rules []*networkMockRule) []map[string]any {
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		headerNames := make([]string, 0, len(rule.Headers))
		for name := range rule.Headers {
			headerNames = append(headerNames, name)
		}
		sort.Strings(headerNames)
		out = append(out, map[string]any{"id": rule.ID, "url_pattern": rule.URLPattern, "method": rule.Method, "resource_type": rule.ResourceType, "status": rule.Status, "header_names": headerNames, "body_bytes": len([]byte(rule.Body)), "max_matches": rule.MaxMatches, "matched_count": rule.Matched})
	}
	return out
}

func normalizedNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func networkControlFailure(action string, err error, targetID string, rules any, duration time.Duration, cleanup networkControlCleanup) error {
	report := map[string]any{
		"ok":      false,
		"target":  map[string]any{"target_id": targetID},
		"rules":   rules,
		"cleanup": cleanup,
		"control": map[string]any{"duration": durationString(duration), "bounded": true},
		"next_commands": []string{
			"cdp network --failed --wait 2s --json",
			"cdp pages --json",
			"cdp doctor --json",
		},
	}
	return commandErrorWithData("network_control_failed", "connection", fmt.Sprintf("%s: %v", action, err), ExitConnection, toStringSlice(report["next_commands"]), report)
}

var _ cdp.CommandClient = browserEventClient(nil)
