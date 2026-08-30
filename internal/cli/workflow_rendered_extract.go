package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type renderedExtractReadiness struct {
	URL                       string `json:"url"`
	DocumentReadyState        string `json:"document_ready_state"`
	SelectorMatched           bool   `json:"selector_matched"`
	SelectorMatchCount        int    `json:"selector_match_count"`
	SelectedTextLength        int    `json:"selected_text_length"`
	SelectedHTMLLength        int    `json:"selected_html_length"`
	SelectedWordCount         int    `json:"selected_word_count"`
	BodyTextLength            int    `json:"body_text_length"`
	BodyHTMLLength            int    `json:"body_html_length"`
	ElementCount              int    `json:"element_count"`
	DOMSignature              string `json:"dom_signature,omitempty"`
	NavigatedFromAboutBlank   bool   `json:"navigated_from_about_blank"`
	LoadSeen                  bool   `json:"load_seen"`
	NetworkIdleSeen           bool   `json:"network_idle_seen"`
	DOMStableSeen             bool   `json:"dom_stable_seen"`
	TextStableSeen            bool   `json:"text_stable_seen"`
	HTMLStableSeen            bool   `json:"html_stable_seen"`
	ContentStableSeen         bool   `json:"content_stable_seen"`
	ContentGrewSeen           bool   `json:"content_grew_seen"`
	StablePolls               int    `json:"stable_polls"`
	PollCount                 int    `json:"poll_count"`
	UsefulContentSeen         bool   `json:"useful_content_seen"`
	ThresholdsMet             bool   `json:"thresholds_met"`
	ContentSettledSeen        bool   `json:"content_settled_seen"`
	Settle                    string `json:"settle"`
	SettledFor                string `json:"settled_for"`
	Outcome                   string `json:"outcome"`
	CaptureConsistencyChecked bool   `json:"capture_consistency_checked"`
	CaptureConsistent         bool   `json:"capture_consistent"`
	Error                     string `json:"error,omitempty"`
}

type renderedExtractReadinessPolicy struct {
	WaitUntil    string
	MinWords     int
	MinHTMLChars int
	Settle       time.Duration
}

type renderedExtractReadinessTracker struct {
	last            renderedExtractReadiness
	hasLast         bool
	stableSince     time.Time
	settling        bool
	stablePolls     int
	pollCount       int
	contentGrewSeen bool
}

func (t *renderedExtractReadinessTracker) Observe(sample renderedExtractReadiness, now time.Time, policy renderedExtractReadinessPolicy) (renderedExtractReadiness, bool) {
	t.pollCount++
	sample.NavigatedFromAboutBlank = sample.URL != "" && sample.URL != "about:blank"
	sample.LoadSeen = sample.DocumentReadyState == "complete"
	sample.DOMStableSeen = t.hasLast && sample.DOMSignature != "" && sample.DOMSignature == t.last.DOMSignature
	sample.TextStableSeen = t.hasLast && sample.SelectedTextLength == t.last.SelectedTextLength && sample.SelectedWordCount == t.last.SelectedWordCount && sample.BodyTextLength == t.last.BodyTextLength
	sample.HTMLStableSeen = t.hasLast && sample.SelectedHTMLLength == t.last.SelectedHTMLLength && sample.BodyHTMLLength == t.last.BodyHTMLLength && sample.ElementCount == t.last.ElementCount
	sample.ContentStableSeen = sample.DOMStableSeen
	if t.hasLast && (sample.SelectedTextLength > t.last.SelectedTextLength || sample.SelectedWordCount > t.last.SelectedWordCount || sample.SelectedHTMLLength > t.last.SelectedHTMLLength || sample.BodyTextLength > t.last.BodyTextLength || sample.BodyHTMLLength > t.last.BodyHTMLLength || sample.ElementCount > t.last.ElementCount) {
		t.contentGrewSeen = true
	}
	sample.ContentGrewSeen = t.contentGrewSeen
	if sample.ContentStableSeen {
		t.stablePolls++
	} else {
		t.stablePolls = 0
	}
	sample.StablePolls = t.stablePolls
	sample.PollCount = t.pollCount
	sample.NetworkIdleSeen = false
	wordsMet := policy.MinWords <= 0 || sample.SelectedWordCount >= policy.MinWords
	htmlMet := policy.MinHTMLChars <= 0 || sample.SelectedHTMLLength >= policy.MinHTMLChars
	sample.ThresholdsMet = wordsMet && htmlMet
	sample.UsefulContentSeen = sample.NavigatedFromAboutBlank && sample.SelectorMatched && sample.ThresholdsMet
	sample.Settle = policy.Settle.String()
	sample.SettledFor = time.Duration(0).String()

	eligible := false
	switch policy.WaitUntil {
	case "load":
		eligible = sample.NavigatedFromAboutBlank && sample.LoadSeen
		if eligible {
			sample.Outcome = "load"
			t.last = sample
			t.hasLast = true
			return sample, true
		}
	case "dom-stable":
		eligible = sample.UsefulContentSeen
	default:
		eligible = sample.UsefulContentSeen
	}

	if !eligible {
		t.settling = false
		t.stableSince = time.Time{}
	} else if policy.Settle <= 0 {
		sample.ContentSettledSeen = true
		sample.Outcome = "settled"
		t.last = sample
		t.hasLast = true
		return sample, true
	} else {
		if !t.settling || !sample.ContentStableSeen {
			t.stableSince = now
			t.settling = true
		}
		settledFor := now.Sub(t.stableSince)
		if settledFor < 0 {
			settledFor = 0
		}
		sample.SettledFor = settledFor.String()
		if sample.ContentStableSeen && settledFor >= policy.Settle {
			sample.ContentSettledSeen = true
			sample.Outcome = "settled"
			t.last = sample
			t.hasLast = true
			return sample, true
		}
	}

	t.last = sample
	t.hasLast = true
	return sample, false
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
	TargetID           string
	URLContains        string
	TitleContains      string
	TargetIndex        int
	Selector           string
	Wait               time.Duration
	Settle             time.Duration
	WaitUntil          string
	Formats            string
	OutDir             string
	Serp               string
	GoogleAI           string
	ContentExtractor   string
	Limit              int
	MinVisibleWords    int
	MinMarkdownWords   int
	MinHTMLChars       int
	KeepOpen           bool
	Reload             bool
	IgnoreCache        bool
	ReusePage          *renderedExtractReusablePage
	BeforeNavigate     func(context.Context) error
}

type renderedExtractResult struct {
	Report   map[string]any
	Human    string
	Links    renderedExtractLinks
	Warnings []string
	GoogleAI *webResearchGoogleAIResponse
}

type renderedExtractReusablePage struct {
	Client      browserEventClient
	CloseClient func(context.Context) error
	Session     *cdp.PageSession
	TargetID    string
	KeepOpen    bool
}

type renderedExtractNavigateFunc func(context.Context, string) (string, error)

func dispatchRenderedExtractNavigation(ctx context.Context, url string, beforeNavigate func(context.Context) error, navigate renderedExtractNavigateFunc) (string, error) {
	if beforeNavigate != nil {
		if err := beforeNavigate(ctx); err != nil {
			return "", err
		}
	}
	return navigate(ctx, url)
}

const renderedExtractCleanupTimeout = 5 * time.Second
const renderedExtractCleanupAttemptWait = time.Second

type renderedExtractCleanupResult struct {
	Attempted       bool     `json:"attempted"`
	Closed          bool     `json:"closed"`
	TargetGone      bool     `json:"target_gone"`
	AttemptCount    int      `json:"attempt_count,omitempty"`
	MaxAttempts     int      `json:"max_attempts,omitempty"`
	ElapsedMS       int64    `json:"elapsed_ms,omitempty"`
	TimedOut        bool     `json:"timed_out,omitempty"`
	RetryPolicy     string   `json:"retry_policy,omitempty"`
	Skipped         bool     `json:"skipped,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	TargetID        string   `json:"target_id,omitempty"`
	Timeout         string   `json:"timeout,omitempty"`
	Error           string   `json:"error,omitempty"`
	RecoveryCommand string   `json:"recovery_command,omitempty"`
	Errors          []string `json:"errors,omitempty"`
}

type renderedExtractCleanupGuard struct {
	client   cdp.CommandClient
	targetID string
	owned    bool
	keepOpen bool
	once     sync.Once
	result   renderedExtractCleanupResult
}

func (g *renderedExtractCleanupGuard) cleanup() renderedExtractCleanupResult {
	if g == nil {
		return renderedExtractCleanupResult{Skipped: true, Reason: "not_registered"}
	}
	g.once.Do(func() {
		g.result = renderedExtractCleanupResult{TargetID: strings.TrimSpace(g.targetID)}
		switch {
		case !g.owned:
			g.result.Skipped = true
			g.result.Reason = "caller_owned"
			return
		case g.keepOpen:
			g.result.Skipped = true
			g.result.Reason = "keep_open"
			return
		case g.client == nil || strings.TrimSpace(g.targetID) == "":
			g.result.Attempted = true
			g.result.Error = "created target cleanup is missing its client or target id"
			g.result.Errors = []string{g.result.Error}
			return
		}

		g.result.Attempted = true
		g.result.Timeout = durationString(renderedExtractCleanupTimeout)
		g.result.RecoveryCommand = fmt.Sprintf("cdp page cleanup --target %s --force --close --json", g.targetID)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), renderedExtractCleanupTimeout)
		defer cancel()
		report := closePageTargetSettled(cleanupCtx, g.client, cdp.TargetInfo{
			TargetID: g.targetID,
			Type:     "page",
		}, pageCloseOptions{
			WaitGone:     true,
			MaxAttempts:  defaultPageCloseMaxAttempts,
			AttemptWait:  renderedExtractCleanupAttemptWait,
			PollInterval: defaultPageClosePollInterval,
			RetryBackoff: defaultPageCloseRetryBackoff,
		})
		g.result.Closed = report.TargetGone
		g.result.TargetGone = report.TargetGone
		g.result.AttemptCount = report.AttemptCount
		g.result.MaxAttempts = report.MaxAttempts
		g.result.ElapsedMS = report.ElapsedMS
		g.result.TimedOut = report.TimedOut
		g.result.RetryPolicy = report.RetryPolicy
		if !report.TargetGone {
			g.result.Error = report.LastError
			if g.result.Error == "" {
				g.result.Error = fmt.Sprintf("target %s close did not settle", g.targetID)
			}
			g.result.Errors = []string{g.result.Error}
		}
	})
	return g.result
}

func renderedExtractErrorSummary(err error) map[string]any {
	return commandErrorSummary(err)
}

func closeRenderedExtractSession(session *cdp.PageSession, closeClient func(context.Context) error) {
	closeCtx, cancel := context.WithTimeout(context.Background(), renderedExtractCleanupTimeout)
	defer cancel()
	if session != nil {
		_ = session.Close(closeCtx)
		return
	}
	if closeClient != nil {
		_ = closeClient(closeCtx)
	}
}

func (a *app) openRenderedExtractReusablePage(ctx context.Context, rawURL, workflow string, keepOpen bool) (*renderedExtractReusablePage, error) {
	client, closeClient, err := a.browserEventCDPClient(ctx)
	if err != nil {
		return nil, commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
	}
	targetID, err := a.createWorkflowPageTargetWithKeepOpen(ctx, client, rawURL, workflow, keepOpen)
	if err != nil {
		_ = closeClient(ctx)
		return nil, err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
	if err != nil {
		cleanup := (&renderedExtractCleanupGuard{client: client, targetID: targetID, owned: true, keepOpen: keepOpen}).cleanup()
		closeRenderedExtractSession(nil, closeClient)
		if cleanup.Error != "" {
			return nil, commandErrorWithData(
				"rendered_extract_cleanup_failed",
				"internal",
				fmt.Sprintf("attach target %s: %v; close created target: %s", targetID, err, cleanup.Error),
				ExitInternal,
				[]string{cleanup.RecoveryCommand, "cdp pages --json", "cdp doctor --json"},
				map[string]any{
					"primary_error": renderedExtractErrorSummary(commandError("connection_failed", "connection", fmt.Sprintf("attach target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})),
					"cleanup":       cleanup,
				},
			)
		}
		return nil, commandError("connection_failed", "connection", fmt.Sprintf("attach target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	return &renderedExtractReusablePage{Client: client, CloseClient: closeClient, Session: session, TargetID: targetID, KeepOpen: keepOpen}, nil
}

func (p *renderedExtractReusablePage) Close(_ context.Context) (bool, string) {
	if p == nil {
		return false, ""
	}
	cleanup := (&renderedExtractCleanupGuard{client: p.Client, targetID: p.TargetID, owned: true, keepOpen: p.KeepOpen}).cleanup()
	closeRenderedExtractSession(p.Session, p.CloseClient)
	return cleanup.Closed, cleanup.Error
}

func (a *app) newWorkflowRenderedExtractCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var targetIndex int
	var selector string
	var wait time.Duration
	var settle time.Duration
	var waitUntil string
	var formats string
	var outDir string
	var serp string
	var contentExtractor string
	var limit int
	var minVisibleWords int
	var minMarkdownWords int
	var minHTMLChars int
	var keepOpen bool
	var reload bool
	var ignoreCache bool
	cmd := &cobra.Command{
		Use:   "rendered-extract [url]",
		Short: "Extract rendered content from a new URL or an existing page target",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			rawURL := ""
			if len(args) == 1 {
				rawURL = args[0]
			}
			options := renderedExtractOptions{
				WorkflowName:       "rendered-extract",
				ArtifactTypePrefix: "rendered-extract",
				UsageCommand:       "cdp workflow rendered-extract",
				RawURL:             rawURL,
				TargetID:           targetID,
				URLContains:        urlContains,
				TitleContains:      titleContains,
				TargetIndex:        targetIndex,
				Selector:           selector,
				Wait:               wait,
				Settle:             settle,
				WaitUntil:          waitUntil,
				Formats:            formats,
				OutDir:             outDir,
				Serp:               serp,
				ContentExtractor:   contentExtractor,
				Limit:              limit,
				MinVisibleWords:    minVisibleWords,
				MinMarkdownWords:   minMarkdownWords,
				MinHTMLChars:       minHTMLChars,
				KeepOpen:           keepOpen,
				Reload:             reload,
				IgnoreCache:        ignoreCache,
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
	cmd.Flags().StringVar(&targetID, "target", "", "existing page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the unique existing page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the unique existing page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "use the 1-based existing page index; workers do not consume indexes and this cannot be combined with --target, --url-contains, or --title-contains")
	cmd.Flags().BoolVar(&reload, "reload", false, "reload the selected existing page before extraction")
	cmd.Flags().BoolVar(&ignoreCache, "ignore-cache", false, "reload the selected existing page while bypassing cache")
	cmd.Flags().StringVar(&selector, "selector", "body", "CSS selector for readiness and generic capture/fallback; auto uses semantic roots for arXiv/Hacker News plus strict X, LinkedIn, and Reddit post, thread, and profile-feed routes")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "hard deadline for rendered readiness; use 0 for one immediate sample")
	cmd.Flags().DurationVar(&settle, "settle", 2*time.Second, "continuous content-fingerprint quiet time required after readiness thresholds pass; use 0 to disable")
	cmd.Flags().StringVar(&waitUntil, "wait-until", "useful-content", "readiness policy: useful-content or dom-stable require enabled thresholds plus settling; load completes on document load")
	cmd.Flags().StringVar(&formats, "formats", "snapshot,text,html,markdown,links", "comma-separated artifacts: snapshot,text,html,markdown,links,all")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "directory for rendered extraction artifacts")
	cmd.Flags().StringVar(&serp, "serp", "auto", "SERP extractor: auto, google, or none")
	cmd.Flags().StringVar(&contentExtractor, "content-extractor", "auto", "content extractor: auto selects native source profiles; generic preserves legacy HTML conversion")
	cmd.Flags().IntVar(&limit, "limit", 80, "maximum visible text snapshot items; use 0 for no limit")
	cmd.Flags().IntVar(&minVisibleWords, "min-visible-words", 5, "enabled readiness and post-capture quality minimum for visible words; use 0 to disable")
	cmd.Flags().IntVar(&minMarkdownWords, "min-markdown-words", 5, "enabled post-capture quality minimum for Markdown words; use 0 to disable")
	cmd.Flags().IntVar(&minHTMLChars, "min-html-chars", 64, "enabled readiness and post-capture quality minimum for extracted HTML characters; use 0 to disable")
	cmd.Flags().BoolVar(&keepOpen, "keep-open", false, "leave the workflow-created page open for debugging")
	return cmd
}

func (a *app) openExistingRenderedExtractPage(ctx context.Context, targetID, urlContains, titleContains string, targetIndex int) (*renderedExtractReusablePage, cdp.TargetInfo, error) {
	client, closeClient, err := a.browserEventCDPClient(ctx)
	if err != nil {
		return nil, cdp.TargetInfo{}, commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
	}
	target, err := a.resolveUniqueRenderedExtractTarget(ctx, client, targetID, urlContains, titleContains, targetIndex)
	if err != nil {
		closeRenderedExtractSession(nil, closeClient)
		return nil, cdp.TargetInfo{}, err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
	if err != nil {
		closeRenderedExtractSession(nil, closeClient)
		return nil, target, commandError("connection_failed", "connection", fmt.Sprintf("attach target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	return &renderedExtractReusablePage{Client: client, CloseClient: closeClient, Session: session, TargetID: target.TargetID}, target, nil
}

func (a *app) resolveUniqueRenderedExtractTarget(ctx context.Context, client cdp.CommandClient, targetID, urlContains, titleContains string, targetIndex int) (cdp.TargetInfo, error) {
	targetID = strings.TrimSpace(targetID)
	urlContains = strings.TrimSpace(urlContains)
	titleContains = strings.TrimSpace(titleContains)
	selectors := 0
	for _, value := range []string{targetID, urlContains, titleContains} {
		if value != "" {
			selectors++
		}
	}
	if selectors > 1 {
		return cdp.TargetInfo{}, commandError("conflicting_target_selectors", "usage", "pass only one of --target, --url-contains, or --title-contains", ExitUsage, []string{"cdp workflow rendered-extract --target <target-id> --json", "cdp pages --json"})
	}
	if targetIndex > 0 {
		return a.resolvePageTargetWithClientIndex(ctx, client, "", "", "", targetIndex)
	}
	if selectors == 0 {
		return a.resolvePageTargetWithClient(ctx, client, "", "", "")
	}
	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return cdp.TargetInfo{}, commandError("connection_failed", "connection", fmt.Sprintf("list targets: %v", err), ExitConnection, []string{"cdp doctor --json", "cdp daemon status --json"})
	}
	if targetID != "" {
		return resolvePageTarget(targets, targetID, "", "")
	}
	matches := make([]cdp.TargetInfo, 0)
	for _, target := range targets {
		if target.Type != "page" {
			continue
		}
		if urlContains != "" && strings.Contains(strings.ToLower(target.URL), strings.ToLower(urlContains)) {
			matches = append(matches, target)
		}
		if titleContains != "" && strings.Contains(strings.ToLower(target.Title), strings.ToLower(titleContains)) {
			matches = append(matches, target)
		}
	}
	label := fmt.Sprintf("page URL containing %q", urlContains)
	if titleContains != "" {
		label = fmt.Sprintf("page title containing %q", titleContains)
	}
	return onePageTarget(matches, label)
}

func (a *app) runRenderedExtractWorkflow(cmd *cobra.Command, options renderedExtractOptions) (result renderedExtractResult, retErr error) {
	if options.Wait < 0 || options.Settle < 0 || options.Limit < 0 || options.MinVisibleWords < 0 || options.MinMarkdownWords < 0 || options.MinHTMLChars < 0 {
		return renderedExtractResult{}, commandError("usage", "usage", "--wait, --settle, --limit, and quality thresholds must be non-negative", ExitUsage, []string{options.UsageCommand + " https://example.com --json"})
	}
	if options.Wait > 0 && options.Settle > options.Wait {
		return renderedExtractResult{}, commandError("usage", "usage", "--settle must not exceed positive --wait", ExitUsage, []string{options.UsageCommand + " https://example.com --wait 15s --settle 2s --json"})
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
	if options.Serp != "auto" && options.Serp != "none" && !isWebResearchSupportedSERP(options.Serp) {
		return renderedExtractResult{}, commandError("usage", "usage", "--serp must be auto, none, or one of: "+webResearchSERPList(), ExitUsage, []string{options.UsageCommand + " 'https://www.google.com/search?q=test' --serp google --json"})
	}
	if strings.TrimSpace(options.GoogleAI) != "" {
		googleAIPolicy, policyErr := parseWebResearchGoogleAIPolicy(options.GoogleAI)
		if policyErr != nil {
			return renderedExtractResult{}, commandError("usage", "usage", "--google-ai must be auto, mode, or off", ExitUsage, []string{options.UsageCommand + " --google-ai auto --json"})
		}
		options.GoogleAI = googleAIPolicy
	}
	options.ContentExtractor = strings.TrimSpace(strings.ToLower(options.ContentExtractor))
	if options.ContentExtractor == "" {
		options.ContentExtractor = "auto"
	}
	if options.ContentExtractor != "auto" && options.ContentExtractor != "generic" {
		return renderedExtractResult{}, commandError("usage", "usage", "--content-extractor must be auto or generic", ExitUsage, []string{options.UsageCommand + " https://example.com --content-extractor auto --json"})
	}

	fallback := options.Wait + 15*time.Second
	if fallback < 30*time.Second {
		fallback = 30 * time.Second
	}
	ctx, cancel := a.commandContextWithDefault(cmd, fallback)
	defer cancel()

	rawURL := strings.TrimSpace(options.RawURL)
	if options.IgnoreCache && !options.Reload {
		return renderedExtractResult{}, commandError("usage", "usage", "--ignore-cache requires --reload", ExitUsage, []string{options.UsageCommand + " --target <target-id> --reload --ignore-cache --json"})
	}
	if strings.TrimSpace(options.OutDir) == "" {
		options.OutDir = filepath.Join("tmp", "cdp-rendered-extract")
	}
	formatSet := renderedExtractFormatSet(options.Formats)

	var client browserEventClient
	var session *cdp.PageSession
	var createdID string
	var selectedTarget cdp.TargetInfo
	createdPage := false
	reusedPage := false
	if options.ReusePage != nil {
		if options.ReusePage.Client == nil || options.ReusePage.Session == nil || strings.TrimSpace(options.ReusePage.TargetID) == "" {
			return renderedExtractResult{}, commandError("invalid_reusable_page", "internal", "rendered-extract reusable page is incomplete", ExitInternal, []string{options.UsageCommand + " <url> --json"})
		}
		client = options.ReusePage.Client
		session = options.ReusePage.Session
		createdID = options.ReusePage.TargetID
		createdPage = false
		reusedPage = true
		selectedTarget = cdp.TargetInfo{TargetID: createdID, Type: "page", URL: rawURL}
	} else if rawURL == "" || options.TargetIndex > 0 || strings.TrimSpace(options.TargetID) != "" || strings.TrimSpace(options.URLContains) != "" || strings.TrimSpace(options.TitleContains) != "" {
		reusablePage, target, err := a.openExistingRenderedExtractPage(ctx, options.TargetID, options.URLContains, options.TitleContains, options.TargetIndex)
		if err != nil {
			return renderedExtractResult{}, err
		}
		client = reusablePage.Client
		session = reusablePage.Session
		createdID = reusablePage.TargetID
		selectedTarget = target
		reusedPage = true
		defer closeRenderedExtractSession(session, nil)
	} else {
		reusablePage, err := a.openRenderedExtractReusablePage(ctx, "about:blank", "rendered-extract", options.KeepOpen)
		if err != nil {
			return renderedExtractResult{}, err
		}
		client = reusablePage.Client
		session = reusablePage.Session
		createdID = reusablePage.TargetID
		selectedTarget = cdp.TargetInfo{TargetID: createdID, Type: "page", URL: rawURL}
		createdPage = true
		defer closeRenderedExtractSession(session, nil)
	}

	cleanupGuard := &renderedExtractCleanupGuard{client: client, targetID: createdID, owned: createdPage, keepOpen: options.KeepOpen}
	defer func() {
		cleanup := cleanupGuard.cleanup()
		if result.Report != nil {
			if workflow, ok := result.Report["workflow"].(map[string]any); ok {
				workflow["closed"] = cleanup.Closed
				workflow["close_error"] = cleanup.Error
				workflow["cleanup"] = cleanup
				workflow["cleanup_command"] = cleanup.RecoveryCommand
				workflow["partial"] = workflow["partial"] == true || cleanup.Error != ""
			}
		}
		if cleanup.Error == "" {
			return
		}
		data := map[string]any{"cleanup": cleanup}
		message := fmt.Sprintf("close rendered-extract target %s: %s", createdID, cleanup.Error)
		if retErr != nil {
			data["primary_error"] = renderedExtractErrorSummary(retErr)
			message = fmt.Sprintf("%s; cleanup failed: %s", retErr.Error(), cleanup.Error)
		} else if result.Report != nil {
			data["partial_result"] = result.Report
		}
		remediation := []string{cleanup.RecoveryCommand, "cdp pages --json"}
		retErr = commandErrorWithData("rendered_extract_cleanup_failed", "internal", message, ExitInternal, remediation, data)
	}()

	if rawURL == "" {
		rawURL = strings.TrimSpace(selectedTarget.URL)
	}
	if rawURL == "" {
		return renderedExtractResult{}, commandError("target_url_missing", "usage", "selected page has no URL to extract", ExitUsage, []string{"cdp pages --json", options.UsageCommand + " https://example.com --json"})
	}
	contentPlan, err := planRenderedExtractContent(rawURL, options.Selector, options.ContentExtractor)
	if err != nil {
		return renderedExtractResult{}, commandError("usage", "usage", err.Error(), ExitUsage, []string{options.UsageCommand + " https://example.com --content-extractor auto --json"})
	}
	if !createdPage && strings.TrimSpace(options.RawURL) == "" && contentPlan.NavigationURL != rawURL {
		contentPlan.NavigationURL = rawURL
		contentPlan.Rewritten = false
	}
	navigationURL := contentPlan.NavigationURL
	captureSelector := renderedExtractCaptureSelector(contentPlan)
	serpMode := renderedExtractSERPMode(rawURL, options.Serp)

	collectorErrors, teardownCollectors := enablePageLoadCollectorsWithTeardown(ctx, client, session.SessionID, map[string]bool{"navigation": true, "network": true})
	defer func() {
		if teardownCollectors != nil {
			_ = teardownCollectors(ctx)
		}
	}()
	trigger := "observe"
	reloaded := false
	frameID := ""
	pageStartedAt := time.Now()
	contentWarnings := make([]string, 0, 2)
	googleAIExpanded := false
	var readinessHook func(context.Context)
	if serpMode == "google" && options.GoogleAI != "" && options.GoogleAI != webResearchGoogleAIOff {
		readinessHook = func(hookCtx context.Context) {
			expanded, expansionErr := expandWebResearchGoogleAIResponse(hookCtx, session, options.GoogleAI)
			if expansionErr != nil {
				return
			}
			googleAIExpanded = googleAIExpanded || expanded
		}
	}
	if createdPage || (reusedPage && strings.TrimSpace(options.RawURL) != "") {
		trigger = "navigate"
		var navigateErr error
		frameID, navigateErr = dispatchRenderedExtractNavigation(ctx, navigationURL, options.BeforeNavigate, session.Navigate)
		if navigateErr != nil {
			collectorErrors = append(collectorErrors, collectorError("navigation", navigateErr))
		}
	} else if options.Reload {
		trigger = "reload"
		if reloadErr := session.Reload(ctx, options.IgnoreCache); reloadErr != nil {
			collectorErrors = append(collectorErrors, collectorError("reload", reloadErr))
		} else {
			reloaded = true
		}
	}

	readiness, err := waitForRenderedExtractReadiness(ctx, session, captureSelector, options.Wait, options.Settle, options.WaitUntil, options.MinVisibleWords, options.MinHTMLChars, readinessHook)
	if err != nil {
		return renderedExtractResult{}, err
	}
	finalURL := readiness.URL
	if strings.TrimSpace(finalURL) == "" {
		finalURL = navigationURL
	}
	nativeContentEligible := renderedExtractContentNativeEligible(contentPlan, finalURL)
	target := cdp.TargetInfo{TargetID: createdID, Type: "page", URL: finalURL}

	snapshot, err := collectPageSnapshot(ctx, session, captureSelector, options.Limit, 1)
	if err != nil {
		return renderedExtractResult{}, err
	}
	var htmlResult htmlResult
	if err := evaluateJSONValue(ctx, session, htmlExpression(captureSelector, 1, 0), options.WorkflowName+" html", &htmlResult); err != nil {
		return renderedExtractResult{}, err
	}
	if htmlResult.Error != nil {
		return renderedExtractResult{}, invalidSelectorError(captureSelector, htmlResult.Error, options.UsageCommand+" https://example.com --selector body --json")
	}
	pageHTML := ""
	htmlLength := 0
	if len(htmlResult.Items) > 0 {
		pageHTML = htmlResult.Items[0].HTML
		htmlLength = htmlResult.Items[0].HTMLLength
	}
	visibleText := strings.Join(snapshotTextValues(snapshot.Items), "\n")
	markdown := htmlToResearchMarkdown(pageHTML)
	contentProvenance := newRenderedExtractContentProvenance(contentPlan, finalURL)
	var discussionExpansion renderedExtractDiscussionExpansion
	var preExpansionReadiness *renderedExtractReadiness
	if contentPlan.Strategy != renderedExtractContentStrategyLegacyHTML {
		if !nativeContentEligible {
			applyRenderedExtractGenericFallback(&contentProvenance, contentPlan, "resolved final URL does not match the planned native content profile")
		} else {
			contentProvenance.NativeAttempted = true
			if renderedExtractContentHasExpandableDiscussion(contentPlan) {
				discussionExpansion.Status = "unknown"
				expansion, expansionErr := expandRenderedExtractDiscussion(ctx, session, contentPlan)
				if expansionErr != nil {
					contentWarnings = append(contentWarnings, "native "+string(contentPlan.Profile)+" discussion expansion was unavailable: "+expansionErr.Error())
				} else {
					discussionExpansion = expansion
					if discussionExpansion.Status == "" {
						discussionExpansion.Status = "unknown"
						contentWarnings = append(contentWarnings, "native "+string(contentPlan.Profile)+" discussion expansion returned no terminal status")
					}
				}
			}
			if renderedExtractContentHasExpandableDiscussion(contentPlan) && discussionExpansion.Interactions > 0 {
				before := readiness
				preExpansionReadiness = &before
				rebased, rebaselineErr := waitForRenderedExtractReadiness(ctx, session, captureSelector, options.Wait, options.Settle, options.WaitUntil, options.MinVisibleWords, options.MinHTMLChars)
				if rebaselineErr != nil {
					return renderedExtractResult{}, rebaselineErr
				}
				readiness = rebased
			}
			contentCapture, contentErr := collectRenderedExtractContent(ctx, session, contentPlan)
			switch {
			case contentErr != nil:
				applyRenderedExtractGenericFallback(&contentProvenance, contentPlan, contentErr.Error())
			case contentCapture.Error != nil:
				applyRenderedExtractGenericFallback(&contentProvenance, contentPlan, contentCapture.Error.Name+": "+contentCapture.Error.Message)
			case strings.TrimSpace(contentCapture.Markdown) == "":
				applyRenderedExtractGenericFallback(&contentProvenance, contentPlan, "native extractor returned empty Markdown")
			default:
				contentProvenance.NativeSucceeded = true
				contentProvenance.ItemCount = contentCapture.ItemCount
				contentProvenance.DiscussionCount = contentCapture.DiscussionCount
				if contentCapture.DiscussionCount > 0 || renderedExtractContentHasExpandableDiscussion(contentPlan) || contentPlan.Profile == renderedExtractContentProfileHackerNews {
					contentProvenance.DiscussionLimit = renderedExtractDiscussionLimit
					contentProvenance.DiscussionStatus = discussionExpansion.Status
					contentProvenance.DiscussionInteractions = discussionExpansion.Interactions
					if contentCapture.DiscussionCount >= renderedExtractDiscussionLimit {
						contentProvenance.DiscussionStatus = "ceiling"
					} else if contentProvenance.DiscussionStatus == "" && contentPlan.Profile == renderedExtractContentProfileHackerNews {
						contentProvenance.DiscussionStatus = "exhausted"
					}
				}
				if strings.TrimSpace(contentCapture.RootSelector) != "" {
					contentProvenance.RootSelector = contentCapture.RootSelector
				}
				markdown = strings.TrimSpace(contentCapture.Markdown)
				if contentPlan.Profile == renderedExtractContentProfileArxiv {
					markdown = prependArxivRepresentationLinks(markdown, contentPlan.Representations)
				}
			}
		}
		if contentProvenance.FallbackUsed {
			contentWarnings = append(contentWarnings, "native "+string(contentPlan.Profile)+" content extraction fell back to generic HTML conversion: "+contentProvenance.FallbackReason)
		}
	}
	links, err := collectRenderedExtractLinks(ctx, session, rawURL, finalURL, serpMode)
	if err != nil {
		return renderedExtractResult{}, err
	}
	var googleAIResponse *webResearchGoogleAIResponse
	if serpMode == "google" && options.GoogleAI != "" && options.GoogleAI != webResearchGoogleAIOff {
		googleAIWait := options.Wait
		if googleAIWait > 0 {
			if elapsed := time.Since(pageStartedAt); elapsed >= googleAIWait {
				googleAIWait = 0
			} else {
				googleAIWait -= elapsed
			}
		}
		googleAIResponse, err = waitForWebResearchGoogleAIResponse(ctx, session, rawURL, options.GoogleAI, googleAIWait, options.Settle)
		if err != nil {
			googleAIResponse = newUnavailableWebResearchGoogleAIResponse(rawURL, options.GoogleAI, err)
			contentWarnings = append(contentWarnings, googleAIResponse.Warnings...)
		} else {
			googleAIResponse.Expanded = googleAIResponse.Expanded || googleAIExpanded
		}
	}
	postCaptureReadiness, consistencyErr := collectRenderedExtractReadiness(ctx, session, captureSelector)
	if consistencyErr != nil {
		collectorErrors = append(collectorErrors, collectorError("capture_consistency", consistencyErr))
	} else {
		applyRenderedExtractCaptureConsistency(&readiness, postCaptureReadiness)
	}

	visibleWordCount := wordCount(visibleText)
	markdownWordCount := wordCount(markdown)
	thresholdsPassed := renderedExtractQualityPassed(visibleWordCount, markdownWordCount, htmlLength, options.MinVisibleWords, options.MinMarkdownWords, options.MinHTMLChars)
	readinessPassed := renderedExtractReadinessQualityPassed(readiness)
	qualityPassed := renderedExtractPostCaptureQualityPassed(thresholdsPassed, readiness)
	warnings := renderedExtractWarnings(readiness, visibleText, snapshot.Count, visibleWordCount, htmlLength, markdownWordCount, len(links.Results), options.MinVisibleWords, options.MinHTMLChars, options.MinMarkdownWords, serpMode)
	warnings = append(warnings, contentWarnings...)
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
	var googleAIJSONPayload []byte
	var googleAIMarkdown []byte
	if googleAIResponse != nil {
		googleAIJSONPath := filepath.Clean(filepath.Join(options.OutDir, "google-ai-response.json"))
		googleAIMarkdownPath := filepath.Clean(filepath.Join(options.OutDir, "google-ai-response.md"))
		googleAIResponse.Artifacts = map[string]string{
			"json":     googleAIJSONPath,
			"markdown": googleAIMarkdownPath,
		}
		googleAIJSONPayload, err = json.MarshalIndent(map[string]any{
			"google_ai": googleAIResponse,
			"full_text": googleAIResponse.fullText,
		}, "", "  ")
		if err != nil {
			return renderedExtractResult{}, commandError("internal", "internal", fmt.Sprintf("marshal Google AI response artifact: %v", err), ExitInternal, []string{options.UsageCommand + " --google-ai auto --json"})
		}
		googleAIMarkdown = []byte(webResearchGoogleAIResponseMarkdown(googleAIResponse))
	}
	diagnostics := map[string]any{
		"readiness":        readiness,
		"content":          contentProvenance,
		"warnings":         warnings,
		"collector_errors": collectorErrors,
		"suggested_commands": []string{
			fmt.Sprintf("cdp snapshot --target %s --selector %s --diagnose-empty --json", createdID, captureSelector),
			fmt.Sprintf("cdp html %s --target %s --diagnose-empty --json", captureSelector, createdID),
			fmt.Sprintf("cdp workflow debug-bundle --target %s --out-dir %s --json", createdID, filepath.Join(options.OutDir, "debug-bundle")),
		},
	}
	if preExpansionReadiness != nil {
		diagnostics["pre_expansion_readiness"] = *preExpansionReadiness
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
	if googleAIResponse != nil {
		if err := writeArtifact("google_ai_json", artifactPrefix+"-google-ai-json", googleAIResponse.Artifacts["json"], append(googleAIJSONPayload, '\n')); err != nil {
			return renderedExtractResult{}, err
		}
		if err := writeArtifact("google_ai_markdown", artifactPrefix+"-google-ai-markdown", googleAIResponse.Artifacts["markdown"], googleAIMarkdown); err != nil {
			return renderedExtractResult{}, err
		}
		googleAIResponse.Artifacts["json"] = artifactPaths["google_ai_json"]
		googleAIResponse.Artifacts["markdown"] = artifactPaths["google_ai_markdown"]
	}
	if len(warnings) > 0 || len(collectorErrors) > 0 {
		if err := writeArtifact("diagnostics_json", artifactPrefix+"-diagnostics-json", filepath.Join(options.OutDir, "diagnostics.json"), append(diagnosticsPayload, '\n')); err != nil {
			return renderedExtractResult{}, err
		}
	}

	teardownErrors := teardownCollectors(ctx)
	teardownCollectors = nil
	collectorErrors = append(collectorErrors, teardownErrors...)

	report := map[string]any{
		"ok":            true,
		"target":        pageRow(target),
		"readiness":     readiness,
		"content":       contentProvenance,
		"artifacts":     artifactPaths,
		"artifact_list": artifactList,
		"quality": map[string]any{
			"passed":                      qualityPassed,
			"thresholds_passed":           thresholdsPassed,
			"readiness_passed":            readinessPassed,
			"capture_consistency_checked": readiness.CaptureConsistencyChecked,
			"capture_consistent":          readiness.CaptureConsistent,
			"snapshot_count":              snapshot.Count,
			"visible_word_count":          visibleWordCount,
			"html_length":                 htmlLength,
			"markdown_word_count":         markdownWordCount,
			"external_link_count":         len(links.Results),
			"selector_match_count":        readiness.SelectorMatchCount,
			"thresholds": map[string]int{
				"min_visible_words":  options.MinVisibleWords,
				"min_markdown_words": options.MinMarkdownWords,
				"min_html_chars":     options.MinHTMLChars,
			},
		},
		"links":    map[string]any{"count": len(links.Results), "query": links.Query, "time_filter": links.TimeFilter, "serp": links.Serp},
		"warnings": warnings,
		"workflow": map[string]any{
			"name":              options.WorkflowName,
			"trigger":           trigger,
			"requested_url":     rawURL,
			"navigation_url":    navigationURL,
			"final_url":         finalURL,
			"frame_id":          frameID,
			"selector":          captureSelector,
			"wait":              durationString(options.Wait),
			"settle":            durationString(options.Settle),
			"wait_until":        options.WaitUntil,
			"formats":           setKeys(formatSet),
			"serp":              serpMode,
			"content_extractor": options.ContentExtractor,
			"created_page":      createdPage,
			"reused_page":       reusedPage,
			"reloaded":          reloaded,
			"ignore_cache":      options.IgnoreCache,
			"closed":            false,
			"close_error":       "",
			"cleanup":           renderedExtractCleanupResult{TargetID: createdID},
			"cleanup_command":   "",
			"collector_errors":  collectorErrors,
			"partial":           renderedExtractWorkflowPartial(qualityPassed, collectorErrors),
		},
	}
	addWorkflowTargetIndex(report, options.TargetIndex)
	if googleAIResponse != nil {
		report["google_ai"] = googleAIResponse
	}
	if len(warnings) > 0 || len(collectorErrors) > 0 {
		report["diagnostics"] = diagnostics
	}
	human := fmt.Sprintf("%s\t%s\t%d words\t%d links", options.WorkflowName, finalURL, visibleWordCount, len(links.Results))
	return renderedExtractResult{Report: report, Human: human, Links: links, Warnings: warnings, GoogleAI: googleAIResponse}, nil
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
	if isWebResearchSupportedSERP(mode) {
		return mode
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "generic"
	}
	host := strings.ToLower(parsed.Hostname())
	path := parsed.EscapedPath()
	switch {
	case strings.Contains(host, "google.") && strings.HasPrefix(path, "/search"):
		return "google"
	case strings.Contains(host, "bing.com") && strings.HasPrefix(path, "/search"):
		return "bing"
	case host == "search.brave.com" && strings.HasPrefix(path, "/search"):
		return "brave"
	case strings.Contains(host, "duckduckgo.com"):
		return "duckduckgo"
	case strings.Contains(host, "kagi.com") && strings.HasPrefix(path, "/search"):
		return "kagi"
	default:
		return "generic"
	}
}

func waitForRenderedExtractReadiness(ctx context.Context, session *cdp.PageSession, selector string, wait, settle time.Duration, waitUntil string, minWords, minHTMLChars int, hooks ...func(context.Context)) (renderedExtractReadiness, error) {
	var hook func(context.Context)
	if len(hooks) > 0 {
		hook = hooks[0]
	}
	return waitForRenderedExtractReadinessFunc(ctx, func(ctx context.Context, selector string) (renderedExtractReadiness, error) {
		return collectRenderedExtractReadiness(ctx, session, selector)
	}, selector, wait, settle, waitUntil, minWords, minHTMLChars, 500*time.Millisecond, hook)
}

func waitForRenderedExtractReadinessFunc(ctx context.Context, collect func(context.Context, string) (renderedExtractReadiness, error), selector string, wait, settle time.Duration, waitUntil string, minWords, minHTMLChars int, pollInterval time.Duration, hooks ...func(context.Context)) (renderedExtractReadiness, error) {
	var hook func(context.Context)
	if len(hooks) > 0 {
		hook = hooks[0]
	}
	deadline := time.Now().Add(wait)
	tracker := renderedExtractReadinessTracker{}
	policy := renderedExtractReadinessPolicy{WaitUntil: waitUntil, MinWords: minWords, MinHTMLChars: minHTMLChars, Settle: settle}
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	deadlineReadiness := func() renderedExtractReadiness {
		readiness := tracker.last
		readiness.Settle = policy.Settle.String()
		if readiness.SettledFor == "" {
			readiness.SettledFor = time.Duration(0).String()
		}
		readiness.Outcome = "wait_expired"
		return readiness
	}
	contextError := func() error {
		return commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp workflow rendered-extract <url> --wait 30s --json"})
	}
	if err := ctx.Err(); err != nil {
		return renderedExtractReadiness{}, contextError()
	}
	if wait == 0 {
		if hook != nil {
			hook(ctx)
		}
		readiness, err := collect(ctx, selector)
		if err != nil {
			return renderedExtractReadiness{}, err
		}
		readiness, _ = tracker.Observe(readiness, time.Now(), policy)
		readiness.Outcome = "immediate"
		return readiness, nil
	}

	waitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	type collectResult struct {
		readiness renderedExtractReadiness
		err       error
	}
	for {
		if ctx.Err() != nil {
			return renderedExtractReadiness{}, contextError()
		}
		if !time.Now().Before(deadline) {
			return deadlineReadiness(), nil
		}
		resultCh := make(chan collectResult, 1)
		go func() {
			if hook != nil {
				hook(waitCtx)
			}
			readiness, err := collect(waitCtx, selector)
			resultCh <- collectResult{readiness: readiness, err: err}
		}()

		var result collectResult
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return renderedExtractReadiness{}, contextError()
			}
			return deadlineReadiness(), nil
		case result = <-resultCh:
		}
		if ctx.Err() != nil {
			return renderedExtractReadiness{}, contextError()
		}
		now := time.Now()
		if !now.Before(deadline) {
			return deadlineReadiness(), nil
		}
		if result.err != nil {
			return renderedExtractReadiness{}, result.err
		}
		readiness, ready := tracker.Observe(result.readiness, now, policy)
		if ready {
			return readiness, nil
		}
		sleepFor := pollInterval
		if remaining := time.Until(deadline); remaining < sleepFor {
			sleepFor = remaining
		}
		timer := time.NewTimer(sleepFor)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return renderedExtractReadiness{}, contextError()
			}
			return deadlineReadiness(), nil
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
	const hash = (value) => {
	  let result = 2166136261;
	  for (let index = 0; index < value.length; index++) {
		result ^= value.charCodeAt(index);
		result = Math.imul(result, 16777619);
	  }
	  return (result >>> 0).toString(16).padStart(8, "0");
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
	  dom_signature: "v2|" + [location.href, document.readyState || "", elements.length, hash(normalize(text)), hash(html)].join("|")
	};
})()`, string(selectorJSON))
}

func renderedExtractQualityPassed(visibleWords, markdownWords, htmlChars, minVisibleWords, minMarkdownWords, minHTMLChars int) bool {
	return (minVisibleWords <= 0 || visibleWords >= minVisibleWords) &&
		(minMarkdownWords <= 0 || markdownWords >= minMarkdownWords) &&
		(minHTMLChars <= 0 || htmlChars >= minHTMLChars)
}

func renderedExtractPostCaptureQualityPassed(thresholdsPassed bool, readiness renderedExtractReadiness) bool {
	return thresholdsPassed && renderedExtractReadinessQualityPassed(readiness) && readiness.CaptureConsistencyChecked && readiness.CaptureConsistent
}

func renderedExtractWorkflowPartial(qualityPassed bool, collectorErrors []map[string]string) bool {
	return !qualityPassed || len(collectorErrors) > 0
}

func renderedExtractReadinessQualityPassed(readiness renderedExtractReadiness) bool {
	switch readiness.Outcome {
	case "immediate", "load", "settled":
		return true
	default:
		return false
	}
}

func applyRenderedExtractCaptureConsistency(readiness *renderedExtractReadiness, postCapture renderedExtractReadiness) {
	readiness.CaptureConsistencyChecked = true
	readiness.CaptureConsistent = readiness.DOMSignature != "" && postCapture.DOMSignature != "" && readiness.DOMSignature == postCapture.DOMSignature
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
  const decodeDuckDuckGoURL = (href) => {
    try {
      const parsed = new URL(href, document.baseURI);
      const host = parsed.hostname.replace(/^www\./, "");
      if ((host === "duckduckgo.com" || host.endsWith(".duckduckgo.com") || host === "duck.com" || host.endsWith(".duck.com")) && parsed.searchParams.has("uddg")) {
        return parsed.searchParams.get("uddg") || href;
      }
      return parsed.href;
    } catch (error) {
      return href;
    }
  };
  const internalHosts = {
    google: ["google."],
    bing: ["bing.com"],
    brave: ["search.brave.com", "brave.com"],
    duckduckgo: ["duckduckgo.com", "duck.com"],
    kagi: ["kagi.com"]
  };
  const isInternal = (href) => {
    try {
      const parsed = new URL(href, document.baseURI);
      if (serp === "google") return parsed.hostname.includes("google.") && parsed.pathname !== "/url";
      return (internalHosts[serp] || []).some((host) => host.endsWith(".") ? parsed.hostname.includes(host) : parsed.hostname === host || parsed.hostname.endsWith("." + host));
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
      decoded = serp === "google" ? decodeGoogleURL(raw) : (serp === "duckduckgo" ? decodeDuckDuckGoURL(raw) : new URL(raw, document.baseURI).href);
    } catch (error) {
      continue;
    }
    if (!/^https?:\/\//i.test(decoded)) continue;
    if (serp !== "generic" && isInternal(decoded)) continue;
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

func renderedExtractWarnings(readiness renderedExtractReadiness, visibleText string, snapshotCount, visibleWords, htmlLength, markdownWords, externalLinks, minVisibleWords, minHTMLChars, minMarkdownWords int, serpMode string) []string {
	var warnings []string
	if readiness.Outcome == "wait_expired" && !readiness.ThresholdsMet {
		warnings = append(warnings, "rendered-content deadline expired before every enabled readiness threshold passed")
	} else if readiness.Outcome == "wait_expired" && !readiness.ContentSettledSeen {
		warnings = append(warnings, "rendered-content deadline expired before the content fingerprint remained quiet for the configured settle duration")
	}
	if !readiness.CaptureConsistencyChecked {
		warnings = append(warnings, "post-capture content consistency could not be verified")
	} else if !readiness.CaptureConsistent {
		warnings = append(warnings, "selected content changed while capture artifacts were collected")
	}
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
	if isWebResearchSupportedSERP(serpMode) && externalLinks == 0 {
		warnings = append(warnings, serpMode+" SERP extraction found no external result links")
	}
	lowerSignal := strings.ToLower(readiness.URL)
	if strings.Contains(lowerSignal, "sorry") || strings.Contains(lowerSignal, "captcha") || strings.Contains(lowerSignal, "consent") || strings.Contains(lowerSignal, "challenge") || strings.Contains(lowerSignal, "signin") || strings.Contains(lowerSignal, "login") {
		warnings = append(warnings, "final URL suggests consent, CAPTCHA, auth, or bot-check handling")
	}
	lowerText := strings.ToLower(visibleText)
	if strings.Contains(lowerText, "unusual traffic") ||
		strings.Contains(lowerText, "not a robot") ||
		strings.Contains(lowerText, "enable javascript") ||
		strings.Contains(lowerText, "unfortunately, bots use") ||
		strings.Contains(lowerText, "select all squares") ||
		strings.Contains(lowerText, "captcha") {
		warnings = append(warnings, "page text suggests consent, CAPTCHA, auth, or bot-check handling")
	}
	return warnings
}
