package cli

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"encoding/json"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
	"net/url"
	"path/filepath"
)

type renderedExtractReadiness struct {
	URL                     string `json:"url"`
	DocumentReadyState      string `json:"document_ready_state"`
	SelectorMatched         bool   `json:"selector_matched"`
	SelectorMatchCount      int    `json:"selector_match_count"`
	SelectedTextLength      int    `json:"selected_text_length"`
	SelectedHTMLLength      int    `json:"selected_html_length"`
	SelectedWordCount       int    `json:"selected_word_count"`
	BodyTextLength          int    `json:"body_text_length"`
	BodyHTMLLength          int    `json:"body_html_length"`
	ElementCount            int    `json:"element_count"`
	DOMSignature            string `json:"dom_signature,omitempty"`
	NavigatedFromAboutBlank bool   `json:"navigated_from_about_blank"`
	LoadSeen                bool   `json:"load_seen"`
	NetworkIdleSeen         bool   `json:"network_idle_seen"`
	DOMStableSeen           bool   `json:"dom_stable_seen"`
	TextStableSeen          bool   `json:"text_stable_seen"`
	HTMLStableSeen          bool   `json:"html_stable_seen"`
	ContentStableSeen       bool   `json:"content_stable_seen"`
	ContentGrewSeen         bool   `json:"content_grew_seen"`
	StablePolls             int    `json:"stable_polls"`
	PollCount               int    `json:"poll_count"`
	UsefulContentSeen       bool   `json:"useful_content_seen"`
	Error                   string `json:"error,omitempty"`
}

type renderedExtractLinks struct {
	Results    []renderedExtractLink `json:"results"`
	Query      string                `json:"query,omitempty"`
	TimeFilter string                `json:"time_filter,omitempty"`
	SourceURL  string                `json:"source_url"`
	Serp       string                `json:"serp,omitempty"`
	Count      int                   `json:"count"`
}

type renderedExtractLink struct {
	Rank       int    `json:"rank"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	DisplayURL string `json:"display_url,omitempty"`
	Snippet    string `json:"snippet,omitempty"`
	DateText   string `json:"date_text,omitempty"`
	Type       string `json:"type,omitempty"`
}

type renderedExtractOptions struct {
	WorkflowName       string
	ArtifactTypePrefix string
	UsageCommand       string
	RawURL             string
	Selector           string
	Wait               time.Duration
	WaitUntil          string
	Formats            string
	OutDir             string
	Serp               string
	Limit              int
	MinVisibleWords    int
	MinMarkdownWords   int
	MinHTMLChars       int
	KeepOpen           bool
}

type renderedExtractResult struct {
	Report   map[string]any
	Human    string
	Links    renderedExtractLinks
	Warnings []string
}

func (a *app) newWorkflowRenderedExtractCommand() *cobra.Command {
	var selector string
	var wait time.Duration
	var waitUntil string
	var formats string
	var outDir string
	var serp string
	var limit int
	var minVisibleWords int
	var minMarkdownWords int
	var minHTMLChars int
	var keepOpen bool
	cmd := &cobra.Command{
		Use:   "rendered-extract <url>",
		Short: "Open a rendered page and write research extraction artifacts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			options := renderedExtractOptions{
				WorkflowName:       "rendered-extract",
				ArtifactTypePrefix: "rendered-extract",
				UsageCommand:       "cdp workflow rendered-extract",
				RawURL:             args[0],
				Selector:           selector,
				Wait:               wait,
				WaitUntil:          waitUntil,
				Formats:            formats,
				OutDir:             outDir,
				Serp:               serp,
				Limit:              limit,
				MinVisibleWords:    minVisibleWords,
				MinMarkdownWords:   minMarkdownWords,
				MinHTMLChars:       minHTMLChars,
				KeepOpen:           keepOpen,
			}
			if strings.TrimSpace(options.OutDir) == "" {
				options.OutDir = filepath.Join("tmp", "cdp-rendered-extract")
			}
			result, err := a.runRenderedExtractWorkflow(cmd, options)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			return a.render(ctx, result.Human, result.Report)
		},
	}
	cmd.Flags().StringVar(&selector, "selector", "body", "CSS selector to extract rendered research content from")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "maximum time to wait for useful rendered content and a quiet stability window")
	cmd.Flags().StringVar(&waitUntil, "wait-until", "useful-content", "readiness gate: useful-content, load, or dom-stable; useful-content waits for SPA-aware content stability")
	cmd.Flags().StringVar(&formats, "formats", "snapshot,text,html,markdown,links", "comma-separated artifacts: snapshot,text,html,markdown,links,all")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "directory for rendered extraction artifacts")
	cmd.Flags().StringVar(&serp, "serp", "auto", "SERP extractor: auto, google, or none")
	cmd.Flags().IntVar(&limit, "limit", 80, "maximum visible text snapshot items; use 0 for no limit")
	cmd.Flags().IntVar(&minVisibleWords, "min-visible-words", 5, "warning threshold for visible text word count")
	cmd.Flags().IntVar(&minMarkdownWords, "min-markdown-words", 5, "warning threshold for Markdown word count")
	cmd.Flags().IntVar(&minHTMLChars, "min-html-chars", 64, "warning threshold for extracted HTML character count")
	cmd.Flags().BoolVar(&keepOpen, "keep-open", false, "leave the workflow-created page open for debugging")
	return cmd
}

func (a *app) runRenderedExtractWorkflow(cmd *cobra.Command, options renderedExtractOptions) (renderedExtractResult, error) {
	if options.Wait < 0 || options.Limit < 0 || options.MinVisibleWords < 0 || options.MinMarkdownWords < 0 || options.MinHTMLChars < 0 {
		return renderedExtractResult{}, commandError("usage", "usage", "--wait, --limit, and quality thresholds must be non-negative", ExitUsage, []string{options.UsageCommand + " https://example.com --json"})
	}
	options.WaitUntil = strings.TrimSpace(options.WaitUntil)
	if options.WaitUntil == "" {
		options.WaitUntil = "useful-content"
	}
	if options.WaitUntil != "useful-content" && options.WaitUntil != "load" && options.WaitUntil != "dom-stable" {
		return renderedExtractResult{}, commandError("usage", "usage", "--wait-until must be useful-content, load, or dom-stable", ExitUsage, []string{options.UsageCommand + " https://example.com --wait-until useful-content --json"})
	}
	options.Serp = strings.TrimSpace(strings.ToLower(options.Serp))
	if options.Serp == "" {
		options.Serp = "auto"
	}
	if options.Serp != "auto" && options.Serp != "google" && options.Serp != "none" {
		return renderedExtractResult{}, commandError("usage", "usage", "--serp must be auto, google, or none", ExitUsage, []string{options.UsageCommand + " 'https://www.google.com/search?q=test' --serp google --json"})
	}

	fallback := options.Wait + 15*time.Second
	if fallback < 30*time.Second {
		fallback = 30 * time.Second
	}
	ctx, cancel := a.commandContextWithDefault(cmd, fallback)
	defer cancel()

	rawURL := strings.TrimSpace(options.RawURL)
	if rawURL == "" {
		return renderedExtractResult{}, commandError("usage", "usage", "url is required", ExitUsage, []string{options.UsageCommand + " https://example.com --json"})
	}
	if strings.TrimSpace(options.OutDir) == "" {
		options.OutDir = filepath.Join("tmp", "cdp-rendered-extract")
	}
	formatSet := renderedExtractFormatSet(options.Formats)
	serpMode := renderedExtractSERPMode(rawURL, options.Serp)

	client, closeClient, err := a.browserEventCDPClient(ctx)
	if err != nil {
		return renderedExtractResult{}, commandError("connection_not_configured", "connection", err.Error(), ExitConnection, []string{"cdp daemon start --auto-connect --json", "cdp connection current --json"})
	}
	createdID, err := a.createWorkflowPageTarget(ctx, client, "about:blank", "rendered-extract")
	if err != nil {
		_ = closeClient(ctx)
		return renderedExtractResult{}, err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, createdID, closeClient)
	if err != nil {
		_ = closeClient(ctx)
		return renderedExtractResult{}, commandError("connection_failed", "connection", fmt.Sprintf("attach target %s: %v", createdID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	defer session.Close(ctx)

	collectorErrors := enablePageLoadCollectors(ctx, client, session.SessionID, map[string]bool{"navigation": true, "network": true})
	frameID, err := session.Navigate(ctx, rawURL)
	if err != nil {
		collectorErrors = append(collectorErrors, collectorError("navigation", err))
	}

	readiness, err := waitForRenderedExtractReadiness(ctx, session, options.Selector, options.Wait, options.WaitUntil, options.MinVisibleWords, options.MinHTMLChars)
	if err != nil {
		return renderedExtractResult{}, err
	}
	finalURL := readiness.URL
	if strings.TrimSpace(finalURL) == "" {
		finalURL = rawURL
	}
	target := cdp.TargetInfo{TargetID: createdID, Type: "page", URL: finalURL}

	snapshot, err := collectPageSnapshot(ctx, session, options.Selector, options.Limit, 1)
	if err != nil {
		return renderedExtractResult{}, err
	}
	var htmlResult htmlResult
	if err := evaluateJSONValue(ctx, session, htmlExpression(options.Selector, 1, 0), options.WorkflowName+" html", &htmlResult); err != nil {
		return renderedExtractResult{}, err
	}
	if htmlResult.Error != nil {
		return renderedExtractResult{}, invalidSelectorError(options.Selector, htmlResult.Error, options.UsageCommand+" https://example.com --selector body --json")
	}
	pageHTML := ""
	htmlLength := 0
	if len(htmlResult.Items) > 0 {
		pageHTML = htmlResult.Items[0].HTML
		htmlLength = htmlResult.Items[0].HTMLLength
	}
	visibleText := strings.Join(snapshotTextValues(snapshot.Items), "\n")
	markdown := htmlToResearchMarkdown(pageHTML)
	links, err := collectRenderedExtractLinks(ctx, session, rawURL, finalURL, serpMode)
	if err != nil {
		return renderedExtractResult{}, err
	}

	visibleWordCount := wordCount(visibleText)
	markdownWordCount := wordCount(markdown)
	warnings := renderedExtractWarnings(readiness, snapshot.Count, visibleWordCount, htmlLength, markdownWordCount, len(links.Results), options.MinVisibleWords, options.MinHTMLChars, options.MinMarkdownWords, serpMode)
	artifactPaths := map[string]string{}
	artifactList := []map[string]any{}
	writeArtifact := func(key, artifactType, path string, payload []byte) error {
		writtenPath, err := writeArtifactFile(path, payload)
		if err != nil {
			return err
		}
		artifactPaths[key] = writtenPath
		artifactList = append(artifactList, map[string]any{"type": artifactType, "path": writtenPath, "bytes": len(payload)})
		return nil
	}
	visibleJSONPayload, err := json.MarshalIndent(map[string]any{"snapshot": snapshot, "items": snapshot.Items}, "", "  ")
	if err != nil {
		return renderedExtractResult{}, commandError("internal", "internal", fmt.Sprintf("marshal visible artifact: %v", err), ExitInternal, []string{options.UsageCommand + " <url> --json"})
	}
	htmlJSONPayload, err := json.MarshalIndent(map[string]any{"html": htmlResult}, "", "  ")
	if err != nil {
		return renderedExtractResult{}, commandError("internal", "internal", fmt.Sprintf("marshal html artifact: %v", err), ExitInternal, []string{options.UsageCommand + " <url> --json"})
	}
	linksPayload, err := json.MarshalIndent(links, "", "  ")
	if err != nil {
		return renderedExtractResult{}, commandError("internal", "internal", fmt.Sprintf("marshal links artifact: %v", err), ExitInternal, []string{options.UsageCommand + " <url> --json"})
	}
	diagnostics := map[string]any{
		"readiness":        readiness,
		"warnings":         warnings,
		"collector_errors": collectorErrors,
		"suggested_commands": []string{
			fmt.Sprintf("cdp snapshot --target %s --selector %s --diagnose-empty --json", createdID, options.Selector),
			fmt.Sprintf("cdp html %s --target %s --diagnose-empty --json", options.Selector, createdID),
			fmt.Sprintf("cdp workflow debug-bundle --target %s --out-dir %s --json", createdID, filepath.Join(options.OutDir, "debug-bundle")),
		},
	}
	diagnosticsPayload, err := json.MarshalIndent(diagnostics, "", "  ")
	if err != nil {
		return renderedExtractResult{}, commandError("internal", "internal", fmt.Sprintf("marshal diagnostics artifact: %v", err), ExitInternal, []string{options.UsageCommand + " <url> --json"})
	}
	artifactPrefix := strings.TrimSpace(options.ArtifactTypePrefix)
	if artifactPrefix == "" {
		artifactPrefix = options.WorkflowName
	}
	if formatSet["snapshot"] {
		if err := writeArtifact("visible_json", artifactPrefix+"-visible-json", filepath.Join(options.OutDir, "visible.json"), append(visibleJSONPayload, '\n')); err != nil {
			return renderedExtractResult{}, err
		}
	}
	if formatSet["text"] {
		if err := writeArtifact("visible_txt", artifactPrefix+"-visible-text", filepath.Join(options.OutDir, "visible.txt"), []byte(visibleText+"\n")); err != nil {
			return renderedExtractResult{}, err
		}
	}
	if formatSet["html"] {
		if err := writeArtifact("html_json", artifactPrefix+"-html-json", filepath.Join(options.OutDir, "html.json"), append(htmlJSONPayload, '\n')); err != nil {
			return renderedExtractResult{}, err
		}
	}
	if formatSet["markdown"] {
		if err := writeArtifact("markdown", artifactPrefix+"-markdown", filepath.Join(options.OutDir, "page.md"), []byte(markdown+"\n")); err != nil {
			return renderedExtractResult{}, err
		}
	}
	if formatSet["links"] {
		if err := writeArtifact("links_json", artifactPrefix+"-links-json", filepath.Join(options.OutDir, "links.json"), append(linksPayload, '\n')); err != nil {
			return renderedExtractResult{}, err
		}
	}
	if len(warnings) > 0 || len(collectorErrors) > 0 {
		if err := writeArtifact("diagnostics_json", artifactPrefix+"-diagnostics-json", filepath.Join(options.OutDir, "diagnostics.json"), append(diagnosticsPayload, '\n')); err != nil {
			return renderedExtractResult{}, err
		}
	}

	closed := false
	closeErr := ""
	if !options.KeepOpen {
		if err := cdp.CloseTargetWithClient(ctx, client, createdID); err != nil {
			closeErr = err.Error()
		} else {
			closed = true
		}
	}

	report := map[string]any{
		"ok":            true,
		"target":        pageRow(target),
		"readiness":     readiness,
		"artifacts":     artifactPaths,
		"artifact_list": artifactList,
		"quality": map[string]any{
			"snapshot_count":       snapshot.Count,
			"visible_word_count":   visibleWordCount,
			"html_length":          htmlLength,
			"markdown_word_count":  markdownWordCount,
			"external_link_count":  len(links.Results),
			"selector_match_count": readiness.SelectorMatchCount,
		},
		"links":    map[string]any{"count": len(links.Results), "query": links.Query, "time_filter": links.TimeFilter, "serp": links.Serp},
		"warnings": warnings,
		"workflow": map[string]any{
			"name":             options.WorkflowName,
			"requested_url":    rawURL,
			"final_url":        finalURL,
			"frame_id":         frameID,
			"selector":         options.Selector,
			"wait":             durationString(options.Wait),
			"wait_until":       options.WaitUntil,
			"formats":          setKeys(formatSet),
			"serp":             serpMode,
			"created_page":     true,
			"closed":           closed,
			"close_error":      closeErr,
			"collector_errors": collectorErrors,
			"partial":          len(collectorErrors) > 0,
		},
	}
	if len(warnings) > 0 || len(collectorErrors) > 0 {
		report["diagnostics"] = diagnostics
	}
	human := fmt.Sprintf("%s\t%s\t%d words\t%d links", options.WorkflowName, finalURL, visibleWordCount, len(links.Results))
	return renderedExtractResult{Report: report, Human: human, Links: links, Warnings: warnings}, nil
}

func renderedExtractFormatSet(formats string) map[string]bool {
	set := parseCSVSet(formats)
	if len(set) == 0 || set["all"] {
		return parseCSVSet("snapshot,text,html,markdown,links")
	}
	return set
}

func renderedExtractSERPMode(rawURL, mode string) string {
	if mode == "none" {
		return "none"
	}
	if mode == "google" {
		return "google"
	}
	parsed, err := url.Parse(rawURL)
	if err == nil && strings.Contains(strings.ToLower(parsed.Hostname()), "google.") && strings.HasPrefix(parsed.EscapedPath(), "/search") {
		return "google"
	}
	return "generic"
}

func waitForRenderedExtractReadiness(ctx context.Context, session *cdp.PageSession, selector string, wait time.Duration, waitUntil string, minWords, minHTMLChars int) (renderedExtractReadiness, error) {
	return waitForRenderedExtractReadinessFunc(ctx, func(ctx context.Context, selector string) (renderedExtractReadiness, error) {
		return collectRenderedExtractReadiness(ctx, session, selector)
	}, selector, wait, waitUntil, minWords, minHTMLChars, 500*time.Millisecond)
}

func waitForRenderedExtractReadinessFunc(ctx context.Context, collect func(context.Context, string) (renderedExtractReadiness, error), selector string, wait time.Duration, waitUntil string, minWords, minHTMLChars int, pollInterval time.Duration) (renderedExtractReadiness, error) {
	deadline := time.Now().Add(wait)
	var last renderedExtractReadiness
	stablePolls := 0
	pollCount := 0
	contentGrewSeen := false
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	for {
		readiness, err := collect(ctx, selector)
		if err != nil {
			return renderedExtractReadiness{}, err
		}
		pollCount++
		readiness.NavigatedFromAboutBlank = readiness.URL != "" && readiness.URL != "about:blank"
		readiness.LoadSeen = readiness.DocumentReadyState == "complete"
		readiness.DOMStableSeen = readiness.DOMSignature != "" && readiness.DOMSignature == last.DOMSignature
		readiness.TextStableSeen = pollCount > 1 && readiness.SelectedTextLength == last.SelectedTextLength && readiness.SelectedWordCount == last.SelectedWordCount && readiness.BodyTextLength == last.BodyTextLength
		readiness.HTMLStableSeen = pollCount > 1 && readiness.SelectedHTMLLength == last.SelectedHTMLLength && readiness.BodyHTMLLength == last.BodyHTMLLength && readiness.ElementCount == last.ElementCount
		readiness.ContentStableSeen = readiness.TextStableSeen && readiness.HTMLStableSeen
		if pollCount > 1 && (readiness.SelectedTextLength > last.SelectedTextLength || readiness.SelectedWordCount > last.SelectedWordCount || readiness.SelectedHTMLLength > last.SelectedHTMLLength || readiness.BodyTextLength > last.BodyTextLength || readiness.BodyHTMLLength > last.BodyHTMLLength || readiness.ElementCount > last.ElementCount) {
			contentGrewSeen = true
		}
		readiness.ContentGrewSeen = contentGrewSeen
		if readiness.ContentStableSeen {
			stablePolls++
		} else {
			stablePolls = 0
		}
		readiness.StablePolls = stablePolls
		readiness.PollCount = pollCount
		readiness.NetworkIdleSeen = readiness.ContentStableSeen
		readiness.UsefulContentSeen = readiness.NavigatedFromAboutBlank && readiness.SelectorMatched && (readiness.SelectedWordCount >= minWords || readiness.SelectedHTMLLength >= minHTMLChars)
		last = readiness
		switch waitUntil {
		case "load":
			if readiness.NavigatedFromAboutBlank && readiness.LoadSeen {
				return readiness, nil
			}
		case "dom-stable":
			if readiness.NavigatedFromAboutBlank && readiness.DOMStableSeen && readiness.StablePolls >= 2 {
				return readiness, nil
			}
		default:
			if readiness.UsefulContentSeen && readiness.StablePolls >= 2 {
				return readiness, nil
			}
		}
		if wait == 0 || time.Now().After(deadline) {
			return last, nil
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return renderedExtractReadiness{}, commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp workflow rendered-extract <url> --wait 30s --json"})
		case <-timer.C:
		}
	}
}

func collectRenderedExtractReadiness(ctx context.Context, session *cdp.PageSession, selector string) (renderedExtractReadiness, error) {
	result, err := session.Evaluate(ctx, renderedExtractReadinessExpression(selector), true)
	if err != nil {
		return renderedExtractReadiness{}, commandError("connection_failed", "connection", fmt.Sprintf("rendered-extract readiness target %s: %v", session.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	if result.Exception != nil {
		return renderedExtractReadiness{}, commandError("javascript_exception", "runtime", fmt.Sprintf("rendered-extract readiness javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp eval 'document.readyState' --json"})
	}
	var readiness renderedExtractReadiness
	if err := json.Unmarshal(result.Object.Value, &readiness); err != nil {
		return renderedExtractReadiness{}, commandError("invalid_workflow_result", "internal", fmt.Sprintf("decode rendered-extract readiness: %v", err), ExitInternal, []string{"cdp doctor --json"})
	}
	return readiness, nil
}

func renderedExtractReadinessExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  "__cdp_cli_rendered_extract_readiness__";
  const selector = %s;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const words = (value) => {
    const text = normalize(value);
    return text ? text.split(/\s+/).filter(Boolean).length : 0;
  };
  let elements = [];
  try {
    elements = Array.from(document.querySelectorAll(selector));
	} catch (error) {
	  return { url: location.href, document_ready_state: document.readyState || "", selector_matched: false, selector_match_count: 0, selected_text_length: 0, selected_html_length: 0, selected_word_count: 0, body_text_length: 0, body_html_length: 0, element_count: 0, dom_signature: "", error: error.name + ": " + error.message };
	}
  let text = "";
  let html = "";
  for (const element of elements) {
    text += " " + normalize(element.innerText || element.textContent);
    html += element.outerHTML || "";
  }
  const bodyText = normalize(document.body && (document.body.innerText || document.body.textContent));
	const bodyHTML = String(document.body && document.body.outerHTML || "");
	const elementCount = document.querySelectorAll("*").length;
  return {
    url: location.href,
    document_ready_state: document.readyState || "",
    selector_matched: elements.length > 0,
    selector_match_count: elements.length,
    selected_text_length: normalize(text).length,
    selected_html_length: html.length,
    selected_word_count: words(text),
    body_text_length: bodyText.length,
	  body_html_length: bodyHTML.length,
	  element_count: elementCount,
	  dom_signature: [location.href, document.readyState || "", elements.length, normalize(text).length, html.length, bodyText.length, bodyHTML.length, elementCount].join("|")
	};
})()`, string(selectorJSON))
}

func collectRenderedExtractLinks(ctx context.Context, session *cdp.PageSession, requestedURL, sourceURL, serpMode string) (renderedExtractLinks, error) {
	result, err := session.Evaluate(ctx, renderedExtractLinksExpression(serpMode), true)
	if err != nil {
		return renderedExtractLinks{}, commandError("connection_failed", "connection", fmt.Sprintf("rendered-extract links target %s: %v", session.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	if result.Exception != nil {
		return renderedExtractLinks{}, commandError("javascript_exception", "runtime", fmt.Sprintf("rendered-extract links javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp eval 'Array.from(document.links).length' --json"})
	}
	var links renderedExtractLinks
	if err := json.Unmarshal(result.Object.Value, &links); err != nil {
		return renderedExtractLinks{}, commandError("invalid_workflow_result", "internal", fmt.Sprintf("decode rendered-extract links: %v", err), ExitInternal, []string{"cdp doctor --json"})
	}
	links.SourceURL = sourceURL
	links.Serp = serpMode
	if parsed, err := url.Parse(requestedURL); err == nil {
		values := parsed.Query()
		if links.Query == "" {
			links.Query = values.Get("q")
		}
		if links.TimeFilter == "" {
			links.TimeFilter = values.Get("tbs")
		}
	}
	links.Count = len(links.Results)
	return links, nil
}

func renderedExtractLinksExpression(serpMode string) string {
	modeJSON, _ := json.Marshal(serpMode)
	return fmt.Sprintf(`(() => {
  "__cdp_cli_rendered_extract_links__";
  const serp = %s;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const displayHost = (href) => {
    try { return new URL(href).hostname.replace(/^www\./, ""); } catch (error) { return ""; }
  };
  const decodeGoogleURL = (href) => {
    try {
      const parsed = new URL(href, document.baseURI);
      if (parsed.hostname.includes("google.") && parsed.pathname === "/url") {
        return parsed.searchParams.get("url") || parsed.searchParams.get("q") || href;
      }
      return parsed.href;
    } catch (error) {
      return href;
    }
  };
  const isGoogleInternal = (href) => {
    try {
      const parsed = new URL(href, document.baseURI);
      return parsed.hostname.includes("google.") && parsed.pathname !== "/url";
    } catch (error) {
      return true;
    }
  };
  const results = [];
  const seen = new Set();
  for (const anchor of Array.from(document.querySelectorAll("a[href]"))) {
    const raw = anchor.getAttribute("href") || "";
    if (!raw || raw.startsWith("#") || raw.startsWith("javascript:") || raw.startsWith("mailto:")) continue;
    let decoded = "";
    try {
      decoded = serp === "google" ? decodeGoogleURL(raw) : new URL(raw, document.baseURI).href;
    } catch (error) {
      continue;
    }
    if (!/^https?:\/\//i.test(decoded)) continue;
    if (serp === "google" && isGoogleInternal(decoded)) continue;
    if (seen.has(decoded)) continue;
    seen.add(decoded);
    const title = normalize(anchor.innerText || anchor.textContent || anchor.getAttribute("aria-label") || decoded);
    if (!title) continue;
    const container = anchor.closest("div, article, section, li") || anchor.parentElement;
    const snippet = normalize(container && container.innerText || "").slice(0, 500);
    const dateMatch = snippet.match(/\b(\d{1,2}\s+[A-Z][a-z]{2,8}\s+\d{4}|[A-Z][a-z]{2,8}\s+\d{1,2},\s+\d{4}|\d+\s+(?:hour|day|week|month|year)s?\s+ago)\b/);
    results.push({
      rank: results.length + 1,
      title,
      url: decoded,
      display_url: displayHost(decoded),
      snippet,
      date_text: dateMatch ? dateMatch[1] : "",
      type: "web"
    });
  }
  return { results, source_url: location.href, serp, count: results.length };
})()`, string(modeJSON))
}

func snapshotTextValues(items []snapshotItem) []string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Text) != "" {
			values = append(values, item.Text)
		}
	}
	return values
}

func htmlToResearchMarkdown(rawHTML string) string {
	text := rawHTML
	replacements := []string{
		"</p>", "\n\n", "<br>", "\n", "<br/>", "\n", "<br />", "\n",
		"</div>", "\n", "</section>", "\n", "</article>", "\n", "</li>", "\n",
		"</h1>", "\n\n", "</h2>", "\n\n", "</h3>", "\n\n", "</h4>", "\n\n",
	}
	replacer := strings.NewReplacer(replacements...)
	text = replacer.Replace(text)
	var out strings.Builder
	inTag := false
	for _, r := range text {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}
	lines := strings.Split(html.UnescapeString(out.String()), "\n")
	compact := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if !blank && len(compact) > 0 {
				compact = append(compact, "")
			}
			blank = true
			continue
		}
		compact = append(compact, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(compact, "\n"))
}

func wordCount(text string) int {
	return len(strings.Fields(text))
}

func renderedExtractWarnings(readiness renderedExtractReadiness, snapshotCount, visibleWords, htmlLength, markdownWords, externalLinks, minVisibleWords, minHTMLChars, minMarkdownWords int, serpMode string) []string {
	var warnings []string
	if !readiness.NavigatedFromAboutBlank {
		warnings = append(warnings, "target remained about:blank or did not report a final URL")
	}
	if !readiness.SelectorMatched {
		warnings = append(warnings, "selector matched zero elements")
	}
	if snapshotCount == 0 {
		warnings = append(warnings, "snapshot produced zero visible text items")
	}
	if visibleWords < minVisibleWords {
		warnings = append(warnings, "visible text word count is below threshold")
	}
	if htmlLength < minHTMLChars {
		warnings = append(warnings, "extracted HTML length is below threshold")
	}
	if markdownWords < minMarkdownWords {
		warnings = append(warnings, "markdown word count is below threshold")
	}
	if serpMode == "google" && externalLinks == 0 {
		warnings = append(warnings, "google SERP extraction found no decoded external result links")
	}
	lowerSignal := strings.ToLower(readiness.URL)
	if strings.Contains(lowerSignal, "sorry") || strings.Contains(lowerSignal, "captcha") || strings.Contains(lowerSignal, "consent") {
		warnings = append(warnings, "final URL suggests consent, CAPTCHA, or bot-check handling")
	}
	return warnings
}
