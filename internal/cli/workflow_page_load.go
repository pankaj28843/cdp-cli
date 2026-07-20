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

type pageLoadStorageKeys struct {
	URL                string   `json:"url"`
	Origin             string   `json:"origin"`
	CookieKeys         []string `json:"cookie_keys"`
	LocalStorageKeys   []string `json:"local_storage_keys"`
	SessionStorageKeys []string `json:"session_storage_keys"`
}

type pageLoadMetric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

type workflowA11ySignals struct {
	ImagesWithoutAlt        int `json:"images_without_alt"`
	FormControlsWithoutName int `json:"form_controls_without_name"`
	HeadingSkips            int `json:"heading_skips"`
	FocusableWithoutLabel   int `json:"focusable_without_label"`
}

type pageLoadContentState struct {
	Class           string   `json:"class"`
	Signals         []string `json:"signals,omitempty"`
	FinalURL        string   `json:"final_url,omitempty"`
	MainStatus      int      `json:"main_status,omitempty"`
	Actionable      bool     `json:"actionable"`
	Warning         string   `json:"warning,omitempty"`
	NextCommands    []string `json:"next_commands,omitempty"`
	TextSampleBytes int      `json:"text_sample_bytes,omitempty"`
}

func (a *app) newWorkflowPageLoadCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var reload bool
	var ignoreCache bool
	var wait time.Duration
	var include string
	var limit int
	var outPath string
	var readyFile string
	cmd := &cobra.Command{
		Use:   "page-load [url]",
		Short: "Capture console, network, storage, and performance signals around a page load",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || limit < 0 {
				return commandError("usage", "usage", "--wait and --limit must be non-negative", ExitUsage, []string{"cdp workflow page-load 'https://example.com' --wait 10s --json"})
			}
			fallback := wait + 10*time.Second
			if fallback < 30*time.Second {
				fallback = 30 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, fallback)
			defer cancel()

			rawURL := ""
			if len(args) == 1 {
				rawURL = strings.TrimSpace(args[0])
			}
			includeSet := pageLoadIncludeSet(include)
			client, closeClient, err := a.browserEventCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					a.connectionRemediationCommands(),
				)
			}
			closeOwned := true
			defer func() {
				if closeOwned {
					_ = closeClient(ctx)
				}
			}()

			target := cdp.TargetInfo{Type: "page", URL: rawURL}
			if rawURL != "" && strings.TrimSpace(targetID) == "" && strings.TrimSpace(urlContains) == "" && strings.TrimSpace(titleContains) == "" {
				createdID, err := a.createWorkflowPageTarget(ctx, client, "about:blank", "page-load")
				if err != nil {
					return err
				}
				target.TargetID = createdID
			} else {
				selected, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
				if err != nil {
					return err
				}
				target = selected
			}

			closeOwned = false
			session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
			if err != nil {
				closeOwned = true
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("attach target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			defer session.Close(ctx)

			collectorErrors := enablePageLoadCollectors(ctx, client, session.SessionID, includeSet)
			if strings.TrimSpace(readyFile) != "" && len(collectorErrors) > 0 {
				return commandErrorWithData("collector_enable_failed", "connection", "cannot publish readiness because one or more requested collectors failed to enable", ExitConnection, []string{"cdp pages --json", "cdp doctor --json"}, map[string]any{"collector_errors": collectorErrors})
			}
			removeReady, err := publishCollectorReadiness(readyFile, target.TargetID, session.SessionID, pageLoadEnabledDomains(includeSet))
			if err != nil {
				return collectorReadinessError(err)
			}
			defer removeReady()
			trigger := "observe"
			frameID := ""
			if rawURL != "" {
				frameID, err = session.Navigate(ctx, rawURL)
				if err != nil {
					collectorErrors = append(collectorErrors, collectorError("navigation", err))
				} else {
					target.URL = rawURL
					trigger = "navigate"
				}
			} else if reload {
				if err := session.Reload(ctx, ignoreCache); err != nil {
					collectorErrors = append(collectorErrors, collectorError("reload", err))
				} else {
					trigger = "reload"
				}
			}

			requests, requestsTruncated, messages, messagesTruncated, err := collectPageLoadEvents(ctx, client, session.SessionID, wait, limit, includeSet)
			if err != nil {
				collectorErrors = append(collectorErrors, collectorError("events", err))
			}
			var storage pageLoadStorageKeys
			if includeSet["storage"] {
				if storage, err = collectPageLoadStorageKeys(ctx, session); err != nil {
					collectorErrors = append(collectorErrors, collectorError("storage", err))
				}
			}
			var metrics []pageLoadMetric
			if includeSet["performance"] {
				if metrics, err = collectPerformanceMetrics(ctx, session); err != nil {
					collectorErrors = append(collectorErrors, collectorError("performance", err))
				}
			}
			var history cdp.NavigationHistory
			if includeSet["navigation"] {
				if history, err = session.NavigationHistory(ctx); err != nil {
					collectorErrors = append(collectorErrors, collectorError("navigation_history", err))
				}
			}

			contentState, contentErr := classifyPageLoadContentState(ctx, session, rawURL, target, requests, history)
			if contentErr != nil {
				collectorErrors = append(collectorErrors, collectorError("content_state", contentErr))
			}

			report := map[string]any{
				"ok":            true,
				"target":        pageRow(target),
				"requests":      requests,
				"messages":      messages,
				"content_state": contentState,
				"workflow": map[string]any{
					"name":               "page-load",
					"trigger":            trigger,
					"requested_url":      rawURL,
					"frame_id":           frameID,
					"wait":               durationString(wait),
					"include":            setKeys(includeSet),
					"limit":              limit,
					"request_count":      len(requests),
					"message_count":      len(messages),
					"requests_truncated": requestsTruncated,
					"messages_truncated": messagesTruncated,
					"collector_errors":   collectorErrors,
					"partial":            len(collectorErrors) > 0,
					"next_commands": []string{
						"cdp console --errors --wait 2s --json",
						"cdp network --failed --wait 2s --json",
						"cdp protocol exec Performance.getMetrics --target <target-id> --json",
					},
				},
			}
			if includeSet["storage"] {
				report["storage"] = storage
			}
			if includeSet["performance"] {
				report["performance"] = map[string]any{"metrics": metrics, "count": len(metrics)}
			}
			if includeSet["navigation"] {
				report["navigation"] = history
			}
			if strings.TrimSpace(outPath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal page-load report: %v", err), ExitInternal, []string{"cdp workflow page-load --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "page-load", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "page-load", "path": writtenPath}}
			}
			human := fmt.Sprintf("page-load\t%s\t%d requests\t%d messages", trigger, len(requests), len(messages))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().BoolVar(&reload, "reload", false, "reload the selected page after attaching collectors")
	cmd.Flags().BoolVar(&ignoreCache, "ignore-cache", false, "reload while bypassing cache")
	cmd.Flags().DurationVar(&wait, "wait", 5*time.Second, "how long to collect events after navigation or reload")
	cmd.Flags().StringVar(&include, "include", "console,network,storage,performance,navigation", "comma-separated collectors: console,network,storage,performance,navigation")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum console messages and requests per collector; use 0 for no limit")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the JSON page-load report artifact")
	cmd.Flags().StringVar(&readyFile, "ready-file", "", "exclusive owner-only readiness artifact written after exact-target attach and all requested collector enables")
	return cmd
}

func pageLoadEnabledDomains(includeSet map[string]bool) []string {
	domains := []string{}
	if includeSet["navigation"] {
		domains = append(domains, "Page")
	}
	if includeSet["console"] {
		domains = append(domains, "Log", "Runtime")
	}
	if includeSet["network"] {
		domains = append(domains, "Network")
	}
	if includeSet["performance"] {
		domains = append(domains, "Performance")
	}
	return domains
}

func pageLoadIncludeSet(include string) map[string]bool {
	set := parseCSVSet(include)
	if len(set) == 0 {
		set = parseCSVSet("console,network,storage,performance,navigation")
	}
	if set["all"] {
		set = parseCSVSet("console,network,storage,performance,navigation")
	}
	return set
}

func enablePageLoadCollectors(ctx context.Context, client browserEventClient, sessionID string, includeSet map[string]bool) []map[string]string {
	collectorErrors, _ := enablePageLoadCollectorsWithTeardown(ctx, client, sessionID, includeSet)
	return collectorErrors
}

func enablePageLoadCollectorsWithTeardown(ctx context.Context, client browserEventClient, sessionID string, includeSet map[string]bool) ([]map[string]string, func(context.Context) []map[string]string) {
	var collectorErrors []map[string]string
	type enabledCollector struct {
		name    string
		disable string
	}
	enabled := make([]enabledCollector, 0)
	enable := func(name, method, disable string, params map[string]any) {
		if params == nil {
			params = map[string]any{}
		}
		if err := client.CallSession(ctx, sessionID, method, params, nil); err != nil {
			collectorErrors = append(collectorErrors, collectorError(name, err))
			return
		}
		enabled = append(enabled, enabledCollector{name: name, disable: disable})
	}
	if includeSet["navigation"] {
		enable("navigation", "Page.enable", "Page.disable", nil)
	}
	if includeSet["console"] {
		enable("runtime", "Runtime.enable", "Runtime.disable", nil)
		enable("log", "Log.enable", "Log.disable", nil)
	}
	if includeSet["network"] {
		enable("network", "Network.enable", "Network.disable", boundedNetworkEnableParams())
	}
	if includeSet["performance"] {
		enable("performance", "Performance.enable", "Performance.disable", nil)
	}
	teardown := func(teardownCtx context.Context) []map[string]string {
		var teardownErrors []map[string]string
		for i := len(enabled) - 1; i >= 0; i-- {
			collector := enabled[i]
			if err := client.CallSession(teardownCtx, sessionID, collector.disable, map[string]any{}, nil); err != nil {
				teardownErrors = append(teardownErrors, collectorError(collector.name+"_teardown", err))
			}
		}
		return teardownErrors
	}
	return collectorErrors, teardown
}

func boundedNetworkEnableParams() map[string]any {
	return map[string]any{
		"maxTotalBufferSize":    1 << 20,
		"maxResourceBufferSize": 256 << 10,
		"maxPostDataSize":       64 << 10,
	}
}

func collectPageLoadEvents(ctx context.Context, client browserEventClient, sessionID string, wait time.Duration, limit int, includeSet map[string]bool) ([]networkRequest, bool, []consoleMessage, bool, error) {
	requestsByID := map[string]*networkRequest{}
	var requestOrder []string
	var messages []consoleMessage
	addEvent := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		if includeSet["network"] {
			req, ok := networkRequestFromEvent(event)
			if ok && req.ID != "" {
				existing, ok := requestsByID[req.ID]
				if !ok {
					copyReq := req
					requestsByID[req.ID] = &copyReq
					requestOrder = append(requestOrder, req.ID)
				} else {
					mergeNetworkRequest(existing, req)
				}
			}
		}
		if includeSet["console"] {
			msg, ok := consoleMessageFromEvent(event)
			if ok {
				msg.ID = len(messages)
				messages = append(messages, msg)
			}
		}
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return nil, false, nil, false, err
	}
	for _, event := range events {
		addEvent(event)
	}
	if wait > 0 {
		eventCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for {
			event, err := client.ReadEvent(eventCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded) {
					break
				}
				return nil, false, nil, false, err
			}
			addEvent(event)
		}
	}

	requests := make([]networkRequest, 0, len(requestOrder))
	for _, id := range requestOrder {
		requests = append(requests, *requestsByID[id])
	}
	requestsTruncated := false
	if limit > 0 && len(requests) > limit {
		requests = requests[:limit]
		requestsTruncated = true
	}
	messagesTruncated := false
	if limit > 0 && len(messages) > limit {
		messages = messages[:limit]
		messagesTruncated = true
		for i := range messages {
			messages[i].ID = i
		}
	}
	return requests, requestsTruncated, messages, messagesTruncated, nil
}

func collectorError(name string, err error) map[string]string {
	return map[string]string{"collector": name, "error": err.Error()}
}

func collectPageLoadStorageKeys(ctx context.Context, session *cdp.PageSession) (pageLoadStorageKeys, error) {
	result, err := session.Evaluate(ctx, pageLoadStorageExpression(), true)
	if err != nil {
		return pageLoadStorageKeys{}, err
	}
	if result.Exception != nil {
		return pageLoadStorageKeys{}, fmt.Errorf("javascript exception: %s", result.Exception.Text)
	}
	var storage pageLoadStorageKeys
	if err := json.Unmarshal(result.Object.Value, &storage); err != nil {
		return pageLoadStorageKeys{}, fmt.Errorf("decode storage keys: %w", err)
	}
	return storage, nil
}

func workflowA11yExpression() string {
	return `(() => {
  "__cdp_cli_workflow_a11y__";
  const byTag = (selector, all = false) => {
    try {
      const elements = Array.from(document.querySelectorAll(selector));
      return all ? elements : elements.filter(Boolean);
    } catch (error) {
      return [];
    }
  };
  const hasAccessibleName = (element) => {
    return (
      (element.getAttribute("aria-label") || "").trim() !== "" ||
      (element.getAttribute("aria-labelledby") || "").trim() !== "" ||
      ((element.getAttribute("title") || "").trim() !== "") ||
      ((element.getAttribute("alt") || "").trim() !== "") ||
      element.textContent.trim() !== "" ||
      (element.getAttribute("value") || "").trim() !== ""
    );
  };
  const images = byTag("img");
  const controls = byTag("button, input, textarea, select, option");
  const focusables = byTag("a, button, input, textarea, select, [tabindex]");
  let previousHeadingLevel = 0;
  let headingSkips = 0;
  byTag("h1, h2, h3, h4, h5, h6").forEach((heading) => {
    const level = Number(heading.tagName.substring(1));
    if (previousHeadingLevel > 0 && level - previousHeadingLevel > 1) {
      headingSkips += 1;
    }
    previousHeadingLevel = level;
  });
  return {
    images_without_alt: images.filter((image) => (image.getAttribute("alt") || "").trim() === "").length,
    form_controls_without_name: controls.filter((control) => {
      const hasName = (control.getAttribute("name") || "").trim() !== "";
      const hasId = (control.getAttribute("id") || "").trim() !== "";
      return !hasAccessibleName(control) && !hasName && !hasId;
    }).length,
    heading_skips: headingSkips,
    focusable_without_label: focusables.filter((element) => !hasAccessibleName(element)).length,
  };
})();`
}

func pageLoadStorageExpression() string {
	return `(() => {
  "__cdp_cli_page_load_storage__";
  const keys = (store) => {
    try { return Object.keys(store || {}).sort(); } catch (error) { return []; }
  };
  const cookieKeys = (() => {
    try {
      return (document.cookie || "")
        .split(";")
        .map((part) => part.trim().split("=")[0])
        .filter(Boolean)
        .sort();
    } catch (error) {
      return [];
    }
  })();
  return {
    url: location.href,
    origin: location.origin,
    cookie_keys: cookieKeys,
    local_storage_keys: keys(window.localStorage),
    session_storage_keys: keys(window.sessionStorage)
  };
})()`
}

func collectPerformanceMetrics(ctx context.Context, session *cdp.PageSession) ([]pageLoadMetric, error) {
	raw, err := session.Exec(ctx, "Performance.getMetrics", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var result struct {
		Metrics []pageLoadMetric `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode performance metrics: %w", err)
	}
	return result.Metrics, nil
}

func classifyPageLoadContentState(ctx context.Context, session *cdp.PageSession, requestedURL string, target cdp.TargetInfo, requests []networkRequest, history cdp.NavigationHistory) (pageLoadContentState, error) {
	finalURL := pageLoadFinalURL(target, history, requestedURL)
	mainStatus := pageLoadMainDocumentStatus(requests, finalURL, requestedURL)
	textSample, err := pageLoadBodyTextSample(ctx, session)
	if err != nil {
		return pageLoadContentState{Class: "content", FinalURL: finalURL, MainStatus: mainStatus, Actionable: true}, err
	}
	return classifyPageLoadContentEvidence(finalURL, target.TargetID, mainStatus, textSample), nil
}

func classifyPageLoadContentEvidence(finalURL, targetID string, mainStatus int, textSample string) pageLoadContentState {
	state := pageLoadContentState{Class: "content", FinalURL: finalURL, MainStatus: mainStatus, Actionable: true, TextSampleBytes: len(textSample)}
	lowerURL := strings.ToLower(finalURL)
	lowerText := strings.ToLower(textSample)
	addSignal := func(signal string) {
		for _, existing := range state.Signals {
			if existing == signal {
				return
			}
		}
		state.Signals = append(state.Signals, signal)
	}
	if state.MainStatus == 401 || state.MainStatus == 403 || state.MainStatus == 429 {
		addSignal(fmt.Sprintf("main_document_http_%d", state.MainStatus))
	}
	if strings.Contains(lowerText, "you've been blocked by network security") || strings.Contains(lowerText, "you have been blocked") || strings.Contains(lowerText, "temporarily blocked") {
		state.Class = "blocked"
		addSignal("block_text")
	}
	if strings.Contains(lowerURL, "/sorry/") || strings.Contains(lowerURL, "captcha") || strings.Contains(lowerURL, "challenge") {
		state.Class = "bot_check"
		addSignal("challenge_url")
	}
	if strings.Contains(lowerURL, "/login") || strings.Contains(lowerURL, "/i/flow/login") || strings.Contains(lowerURL, "signin") || strings.Contains(lowerText, "sign in to x") || strings.Contains(lowerText, "log in to your reddit account") {
		if state.Class == "content" {
			state.Class = "login_wall"
		}
		addSignal("login_wall")
	}
	if strings.Contains(lowerText, "accept all cookies") || strings.Contains(lowerText, "refuse non-essential cookies") || strings.Contains(lowerText, "cookie use") {
		if state.Class == "content" {
			state.Class = "cookie_wall"
		}
		addSignal("cookie_wall")
	}
	if state.Class != "content" {
		state.Actionable = false
		state.Warning = "loaded page is not the requested content; it appears to be a " + strings.ReplaceAll(state.Class, "_", " ")
		state.NextCommands = []string{
			"cdp workflow debug-bundle --target " + targetID + " --out-dir tmp/page-load-debug --json",
			"cdp storage snapshot --target " + targetID + " --include all --redact safe --json",
		}
	}
	return state
}

func pageLoadFinalURL(target cdp.TargetInfo, history cdp.NavigationHistory, requestedURL string) string {
	if history.CurrentIndex >= 0 && history.CurrentIndex < len(history.Entries) {
		if url := strings.TrimSpace(history.Entries[history.CurrentIndex].URL); url != "" && url != "about:blank" {
			return url
		}
	}
	if url := strings.TrimSpace(target.URL); url != "" {
		return url
	}
	return strings.TrimSpace(requestedURL)
}

func pageLoadMainDocumentStatus(requests []networkRequest, finalURL, requestedURL string) int {
	for _, req := range requests {
		if req.ResourceType == "Document" && pageLoadURLMatches(req.URL, finalURL, requestedURL) && req.Status != 0 {
			return req.Status
		}
	}
	for _, req := range requests {
		if req.ResourceType == "Document" && req.Status != 0 {
			return req.Status
		}
	}
	for _, req := range requests {
		if pageLoadURLMatches(req.URL, finalURL, requestedURL) && req.Status != 0 {
			return req.Status
		}
	}
	return 0
}

func pageLoadURLMatches(raw, finalURL, requestedURL string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	for _, candidate := range []string{finalURL, requestedURL} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if raw == candidate || strings.TrimRight(raw, "/") == strings.TrimRight(candidate, "/") {
			return true
		}
	}
	return false
}

func pageLoadBodyTextSample(ctx context.Context, session *cdp.PageSession) (string, error) {
	result, err := session.Evaluate(ctx, `(() => {
  const text = document.body ? (document.body.innerText || document.body.textContent || '') : '';
  return text.replace(/\s+/g, ' ').trim().slice(0, 4096);
})()`, true)
	if err != nil {
		return "", err
	}
	if result.Exception != nil {
		return "", fmt.Errorf("evaluate body text sample: %s", result.Exception.Text)
	}
	if len(result.Object.Value) == 0 || string(result.Object.Value) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(result.Object.Value, &text); err != nil {
		return "", fmt.Errorf("decode body text sample: %w", err)
	}
	return text, nil
}
