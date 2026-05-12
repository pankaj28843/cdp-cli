package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowWebResearchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "web-research",
		Short: "Run batched browser-grounded web research workflows",
	}
	cmd.AddCommand(a.newWorkflowWebResearchSERPCommand())
	cmd.AddCommand(a.newWorkflowWebResearchExtractCommand())
	return cmd
}

func (a *app) newWorkflowWebResearchSERPCommand() *cobra.Command {
	var queryFile string
	var serp string
	var maxCandidates int
	var candidateOut string
	var outDir string
	var wait time.Duration
	var waitUntil string
	var parallel int
	var resultPages int
	var minVisibleWords int
	var minMarkdownWords int
	var minHTMLChars int
	cmd := &cobra.Command{
		Use:   "serp",
		Short: "Collect rendered SERP artifacts and deduped research candidates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || maxCandidates < 0 || parallel < 0 || resultPages < 0 || minVisibleWords < 0 || minMarkdownWords < 0 || minHTMLChars < 0 {
				return commandError("usage", "usage", "--wait, --max-candidates, --parallel, --result-pages, and quality thresholds must be non-negative", ExitUsage, []string{"cdp workflow web-research serp --query-file tmp/queries.txt --result-pages 3 --out-dir tmp/research --json"})
			}
			queries, err := readWebResearchQueries(queryFile)
			if err != nil {
				return err
			}
			if strings.TrimSpace(outDir) == "" {
				outDir = filepath.Join("tmp", "cdp-web-research")
			}
			if strings.TrimSpace(candidateOut) == "" {
				candidateOut = filepath.Join(outDir, "candidates.json")
			}
			if parallel == 0 || parallel > 3 {
				parallel = 3
			}
			if resultPages == 0 {
				resultPages = 1
			}
			if resultPages > 3 {
				resultPages = 3
			}
			serp = strings.TrimSpace(strings.ToLower(serp))
			if serp == "" {
				serp = "google"
			}
			if serp != "google" {
				return commandError("usage", "usage", "--serp must be google", ExitUsage, []string{"cdp workflow web-research serp --serp google --json"})
			}

			ctx := cmd.Context()
			queriesPath := filepath.Join(outDir, "queries.json")
			queriesPayload, err := json.MarshalIndent(map[string]any{"queries": queries, "count": len(queries), "serp": serp, "result_pages": resultPages}, "", "  ")
			if err != nil {
				return commandError("internal", "internal", fmt.Sprintf("marshal web research queries: %v", err), ExitInternal, []string{"cdp workflow web-research serp --json"})
			}
			queriesPath, err = writeArtifactFile(queriesPath, append(queriesPayload, '\n'))
			if err != nil {
				return err
			}

			type serpJob struct {
				QueryIndex int
				SerpPage   int
			}
			type serpResult struct {
				QueryIndex int
				SerpPage   int
				Query      webResearchQuery
				Result     renderedExtractResult
				Err        error
			}
			jobs := make(chan serpJob)
			resultCount := len(queries) * resultPages
			results := make(chan serpResult, resultCount)
			var wg sync.WaitGroup
			for i := 0; i < parallel; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for job := range jobs {
						query := queries[job.QueryIndex]
						queryURL := googleSearchURL(query.Text, query.TimeFilter, (job.SerpPage-1)*10)
						result, err := a.runRenderedExtractWorkflow(cmd, renderedExtractOptions{
							WorkflowName:       "web-research-serp",
							ArtifactTypePrefix: "web-research-serp",
							UsageCommand:       "cdp workflow web-research serp",
							RawURL:             queryURL,
							Selector:           "body",
							Wait:               wait,
							WaitUntil:          waitUntil,
							Formats:            "snapshot,text,html,markdown,links",
							OutDir:             filepath.Join(outDir, "serps", webResearchSlug(query.Text), fmt.Sprintf("page-%d", job.SerpPage)),
							Serp:               "google",
							Limit:              80,
							MinVisibleWords:    minVisibleWords,
							MinMarkdownWords:   minMarkdownWords,
							MinHTMLChars:       minHTMLChars,
						})
						results <- serpResult{QueryIndex: job.QueryIndex, SerpPage: job.SerpPage, Query: query, Result: result, Err: err}
					}
				}()
			}
			for i := range queries {
				for page := 1; page <= resultPages; page++ {
					jobs <- serpJob{QueryIndex: i, SerpPage: page}
				}
			}
			close(jobs)
			wg.Wait()
			close(results)

			serpResults := make([]serpResult, 0, resultCount)
			for result := range results {
				serpResults = append(serpResults, result)
			}
			sort.SliceStable(serpResults, func(i, j int) bool {
				if serpResults[i].QueryIndex == serpResults[j].QueryIndex {
					return serpResults[i].SerpPage < serpResults[j].SerpPage
				}
				return serpResults[i].QueryIndex < serpResults[j].QueryIndex
			})

			serpReports := make([]map[string]any, 0, resultCount)
			failures := make([]map[string]any, 0)
			candidates := make([]webResearchCandidate, 0)
			seen := map[string]bool{}
			for _, result := range serpResults {
				if result.Err != nil {
					failures = append(failures, map[string]any{"query": result.Query.Text, "serp_page": result.SerpPage, "error": result.Err.Error()})
					continue
				}
				serpReports = append(serpReports, map[string]any{"query": result.Query.Text, "time_filter": result.Query.TimeFilter, "serp_page": result.SerpPage, "report": result.Result.Report})
				for _, link := range result.Result.Links.Results {
					key := normalizeResearchURL(link.URL)
					if key == "" || seen[key] {
						continue
					}
					seen[key] = true
					globalRank := (result.SerpPage-1)*10 + link.Rank
					candidates = append(candidates, webResearchCandidate{Query: result.Query.Text, TimeFilter: result.Query.TimeFilter, SerpPage: result.SerpPage, RankOnPage: link.Rank, GlobalRank: globalRank, Rank: globalRank, Title: link.Title, Source: link.DisplayURL, Preview: link.Snippet, URL: link.URL, Type: link.Type})
					if maxCandidates > 0 && len(candidates) >= maxCandidates {
						break
					}
				}
				if maxCandidates > 0 && len(candidates) >= maxCandidates {
					break
				}
			}

			sort.SliceStable(candidates, func(i, j int) bool {
				if candidates[i].Query == candidates[j].Query {
					return candidates[i].Rank < candidates[j].Rank
				}
				return candidates[i].Query < candidates[j].Query
			})
			candidatePayload, err := json.MarshalIndent(candidates, "", "  ")
			if err != nil {
				return commandError("internal", "internal", fmt.Sprintf("marshal web research candidates: %v", err), ExitInternal, []string{"cdp workflow web-research serp --json"})
			}
			candidateOut, err = writeArtifactFile(candidateOut, append(candidatePayload, '\n'))
			if err != nil {
				return err
			}
			candidatesTSV := filepath.Join(outDir, "candidates.tsv")
			candidatesTSV, err = writeArtifactFile(candidatesTSV, []byte(webResearchCandidatesTSV(candidates)))
			if err != nil {
				return err
			}

			report := map[string]any{
				"ok":         len(failures) == 0,
				"queries":    queries,
				"serps":      serpReports,
				"candidates": candidates,
				"failures":   failures,
				"artifacts": map[string]string{
					"queries_json":    queriesPath,
					"candidates_json": candidateOut,
					"candidates_tsv":  candidatesTSV,
				},
				"workflow": map[string]any{
					"name":            "web-research-serp",
					"serp":            serp,
					"query_count":     len(queries),
					"candidate_count": len(candidates),
					"failure_count":   len(failures),
					"max_candidates":  maxCandidates,
					"result_pages":    resultPages,
					"parallel":        parallel,
					"out_dir":         outDir,
					"next_commands":   []string{"jq -r '.[].url' " + candidateOut + " > " + filepath.Join(outDir, "visit-urls.txt"), "cdp workflow web-research extract --url-file " + filepath.Join(outDir, "visit-urls.txt") + " --out-dir " + filepath.Join(outDir, "pages") + " --json"},
				},
			}
			return a.render(ctx, fmt.Sprintf("web-research-serp\t%d queries\t%d candidates", len(queries), len(candidates)), report)
		},
	}
	cmd.Flags().StringVar(&queryFile, "query-file", "", "newline-delimited Google queries to sample")
	cmd.Flags().StringVar(&serp, "serp", "google", "SERP extractor: google")
	cmd.Flags().IntVar(&maxCandidates, "max-candidates", 100, "maximum deduped candidates to emit; use 0 for no limit")
	cmd.Flags().StringVar(&candidateOut, "candidate-out", "", "path for deduped candidates JSON")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "directory for SERP artifacts and candidate files")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "maximum time to wait for each rendered SERP")
	cmd.Flags().StringVar(&waitUntil, "wait-until", "useful-content", "readiness gate: useful-content, load, or dom-stable")
	cmd.Flags().IntVar(&parallel, "parallel", 3, "maximum parallel SERP tabs, capped at 3")
	cmd.Flags().IntVar(&resultPages, "result-pages", 1, "Google result pages per query to sample, capped at 3")
	cmd.Flags().IntVar(&minVisibleWords, "min-visible-words", 5, "warning threshold for visible text word count")
	cmd.Flags().IntVar(&minMarkdownWords, "min-markdown-words", 5, "warning threshold for Markdown word count")
	cmd.Flags().IntVar(&minHTMLChars, "min-html-chars", 64, "warning threshold for extracted HTML character count")
	return cmd
}

func (a *app) newWorkflowWebResearchExtractCommand() *cobra.Command {
	var urlFile string
	var maxPages int
	var parallel int
	var outDir string
	var wait time.Duration
	var waitUntil string
	var selector string
	var minVisibleWords int
	var minMarkdownWords int
	var minHTMLChars int
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract selected research pages with bounded tab concurrency",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || maxPages < 0 || parallel < 0 || minVisibleWords < 0 || minMarkdownWords < 0 || minHTMLChars < 0 {
				return commandError("usage", "usage", "--wait, --max-pages, --parallel, and quality thresholds must be non-negative", ExitUsage, []string{"cdp workflow web-research extract --url-file tmp/urls.txt --json"})
			}
			urls, err := readWebResearchURLs(urlFile, maxPages)
			if err != nil {
				return err
			}
			if strings.TrimSpace(outDir) == "" {
				outDir = filepath.Join("tmp", "cdp-web-research", "pages")
			}
			requestedParallel := parallel
			if parallel == 0 {
				parallel = 4
			}
			if parallel > 10 {
				parallel = 10
			}

			ctx := cmd.Context()
			initialBudget := cdp.BrowserResourceBudget{}
			effectiveParallel := parallel
			backpressureApplied := false
			if len(urls) > 0 {
				client, closeClient, err := a.browserCDPClient(ctx)
				if err != nil {
					return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
				}
				budget, budgetErr := a.enforceBrowserBudgetForNewPage(ctx, client)
				_ = closeClient(ctx)
				if budgetErr != nil {
					return budgetErr
				}
				initialBudget = budget
				if remaining := budget.RemainingTabs(); remaining > 0 && effectiveParallel > remaining {
					effectiveParallel = remaining
					backpressureApplied = true
				}
			}
			if effectiveParallel <= 0 {
				effectiveParallel = 1
			}

			type pageResult struct {
				Index  int
				URL    string
				Result renderedExtractResult
				Err    error
			}
			results := make(chan pageResult, len(urls))
			launch := func(idx int) {
				rawURL := urls[idx]
				go func() {
					result, err := a.runRenderedExtractWorkflow(cmd, renderedExtractOptions{
						WorkflowName:       "web-research-extract",
						ArtifactTypePrefix: "web-research-extract",
						UsageCommand:       "cdp workflow web-research extract",
						RawURL:             rawURL,
						Selector:           selector,
						Wait:               wait,
						WaitUntil:          waitUntil,
						Formats:            "snapshot,text,html,markdown,links",
						OutDir:             filepath.Join(outDir, fmt.Sprintf("%03d-%s", idx+1, webResearchURLSlug(rawURL))),
						Serp:               "none",
						Limit:              80,
						MinVisibleWords:    minVisibleWords,
						MinMarkdownWords:   minMarkdownWords,
						MinHTMLChars:       minHTMLChars,
					})
					results <- pageResult{Index: idx, URL: rawURL, Result: result, Err: err}
				}()
			}
			collected := make([]pageResult, 0, len(urls))
			nextIndex := 0
			active := 0
			currentParallel := effectiveParallel
			pageFailureCount := 0
			infrastructureFailureCount := 0
			retriedAfterReconnect := false
			stopScheduling := false
			for len(collected) < len(urls) {
				for !stopScheduling && active < currentParallel && nextIndex < len(urls) {
					launch(nextIndex)
					nextIndex++
					active++
				}
				if active == 0 {
					break
				}
				result := <-results
				active--
				collected = append(collected, result)
				if result.Err == nil {
					continue
				}
				failureClass := classifyWorkflowExtractFailure(result.Err)
				if isInfrastructureFailureClass(failureClass) {
					infrastructureFailureCount++
					currentParallel = 1
					backpressureApplied = true
					if !retriedAfterReconnect {
						if err := a.repairDaemonForWorkflow(ctx); err == nil {
							retriedAfterReconnect = true
						} else {
							stopScheduling = true
						}
					} else {
						stopScheduling = true
					}
					continue
				}
				pageFailureCount++
				if pageFailureCount >= 3 {
					currentParallel = 1
					backpressureApplied = true
				}
			}
			remainingURLs := append([]string(nil), urls[nextIndex:]...)

			pages := make([]map[string]any, 0, len(urls))
			qualities := make([]map[string]any, 0, len(urls))
			failures := make([]map[string]any, 0)
			warnings := make([]string, 0)
			for _, result := range collected {
				if result.Err != nil {
					failures = append(failures, map[string]any{"url": result.URL, "error": result.Err.Error(), "err_class": classifyWorkflowExtractFailure(result.Err)})
					continue
				}
				pages = append(pages, map[string]any{"url": result.URL, "report": result.Result.Report})
				quality, _ := result.Result.Report["quality"].(map[string]any)
				artifacts, _ := result.Result.Report["artifacts"].(map[string]string)
				qualities = append(qualities, map[string]any{"url": result.URL, "quality": quality, "warnings": result.Result.Warnings, "artifacts": artifacts})
				for _, warning := range result.Result.Warnings {
					warnings = append(warnings, result.URL+": "+warning)
				}
			}
			sort.SliceStable(pages, func(i, j int) bool { return fmt.Sprint(pages[i]["url"]) < fmt.Sprint(pages[j]["url"]) })
			qualityPath := filepath.Join(outDir, "page-quality.json")
			qualityPayload, err := json.MarshalIndent(qualities, "", "  ")
			if err != nil {
				return commandError("internal", "internal", fmt.Sprintf("marshal page quality: %v", err), ExitInternal, []string{"cdp workflow web-research extract --json"})
			}
			qualityPath, err = writeArtifactFile(qualityPath, append(qualityPayload, '\n'))
			if err != nil {
				return err
			}
			failuresPath := filepath.Join(outDir, "failures.json")
			failuresPayload, err := json.MarshalIndent(failures, "", "  ")
			if err != nil {
				return commandError("internal", "internal", fmt.Sprintf("marshal extraction failures: %v", err), ExitInternal, []string{"cdp workflow web-research extract --json"})
			}
			failuresPath, err = writeArtifactFile(failuresPath, append(failuresPayload, '\n'))
			if err != nil {
				return err
			}
			failedURLs := make([]string, 0, len(failures))
			for _, failure := range failures {
				failedURLs = append(failedURLs, fmt.Sprint(failure["url"]))
			}
			failedURLsPath, err := writeArtifactFile(filepath.Join(outDir, "failed-urls.txt"), []byte(strings.Join(failedURLs, "\n")+newlineIfNotEmpty(failedURLs)))
			if err != nil {
				return err
			}
			remainingURLsPath, err := writeArtifactFile(filepath.Join(outDir, "remaining-urls.txt"), []byte(strings.Join(remainingURLs, "\n")+newlineIfNotEmpty(remainingURLs)))
			if err != nil {
				return err
			}
			retryParallel := saferWebResearchRetryParallel(effectiveParallel, backpressureApplied)
			retryCommandPath, err := writeArtifactFile(filepath.Join(outDir, "retry-command.sh"), []byte(webResearchRetryCommand(urlFile, outDir, wait, waitUntil, selector, maxPages, minVisibleWords, minMarkdownWords, minHTMLChars, retryParallel)))
			if err != nil {
				return err
			}

			report := map[string]any{
				"ok":        len(failures) == 0,
				"pages":     pages,
				"quality":   qualities,
				"warnings":  warnings,
				"failures":  failures,
				"artifacts": map[string]string{"page_quality_json": qualityPath, "failures_json": failuresPath, "failed_urls": failedURLsPath, "remaining_urls": remainingURLsPath, "retry_command": retryCommandPath},
				"workflow": map[string]any{
					"name":                    "web-research-extract",
					"url_count":               len(urls),
					"page_count":              len(pages),
					"failure_count":           len(failures),
					"page_failures":           pageFailureCount,
					"infrastructure_failures": infrastructureFailureCount,
					"warning_count":           len(warnings),
					"max_pages":               maxPages,
					"requested_parallel":      requestedParallel,
					"parallel":                effectiveParallel,
					"parallel_cap":            10,
					"backpressure_applied":    backpressureApplied,
					"retried_after_reconnect": retriedAfterReconnect,
					"remaining_url_count":     len(remainingURLs),
					"initial_resource_budget": initialBudget,
					"retry_parallel":          retryParallel,
					"retry_artifacts":         map[string]string{"failed_urls": failedURLsPath, "remaining_urls": remainingURLsPath, "retry_command": retryCommandPath},
					"out_dir":                 outDir,
					"next_commands":           []string{"jq '.[] | select((.warnings | length) > 0)' " + qualityPath, "jq -r '.[].url' " + failuresPath, "sh " + retryCommandPath},
				},
			}
			return a.render(ctx, fmt.Sprintf("web-research-extract\t%d pages\t%d failures", len(pages), len(failures)), report)
		},
	}
	cmd.Flags().StringVar(&urlFile, "url-file", "", "newline-delimited URLs to extract")
	cmd.Flags().IntVar(&maxPages, "max-pages", 100, "maximum URLs to extract; use 0 for no limit")
	cmd.Flags().IntVar(&parallel, "parallel", 4, "maximum parallel page tabs; default 4, hard-capped at 10 and bounded by remaining tab budget")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "directory for page artifacts")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "maximum time to wait for each rendered page")
	cmd.Flags().StringVar(&waitUntil, "wait-until", "useful-content", "readiness gate: useful-content, load, or dom-stable")
	cmd.Flags().StringVar(&selector, "selector", "body", "CSS selector to extract rendered research content from")
	cmd.Flags().IntVar(&minVisibleWords, "min-visible-words", 5, "warning threshold for visible text word count")
	cmd.Flags().IntVar(&minMarkdownWords, "min-markdown-words", 5, "warning threshold for Markdown word count")
	cmd.Flags().IntVar(&minHTMLChars, "min-html-chars", 64, "warning threshold for extracted HTML character count")
	return cmd
}

func classifyWorkflowExtractFailure(err error) string {
	if err == nil {
		return ""
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		switch cmdErr.Code {
		case "timeout":
			return "page_timeout"
		case "browser_resource_budget_exceeded":
			return "browser_resource_budget_exceeded"
		case "permission_pending":
			return "permission_pending"
		case "connection_not_configured", "connection_failed":
			if looksLikeDaemonDisconnect(cmdErr.Error()) {
				return "daemon_disconnected"
			}
			return "collector_error"
		default:
			if cmdErr.Class == "connection" && looksLikeDaemonDisconnect(cmdErr.Error()) {
				return "daemon_disconnected"
			}
		}
	}
	message := err.Error()
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(message), "context deadline exceeded") {
		return "page_timeout"
	}
	if looksLikeDaemonDisconnect(message) {
		return "daemon_disconnected"
	}
	return "collector_error"
}

func looksLikeDaemonDisconnect(message string) bool {
	message = strings.ToLower(message)
	needles := []string{"use of closed network connection", "daemon runtime socket", "daemon runtime state", "running cdp daemon", "connection is closed", "failed to get reader", "broken pipe"}
	for _, needle := range needles {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

func isInfrastructureFailureClass(class string) bool {
	switch class {
	case "daemon_disconnected", "permission_pending", "browser_resource_budget_exceeded":
		return true
	default:
		return false
	}
}

func (a *app) repairDaemonForWorkflow(ctx context.Context) error {
	if a.opts.autoConnect && !a.opts.activeProbe {
		return fmt.Errorf("auto-connect daemon repair requires human approval")
	}
	_, err := a.runDaemonStart(ctx, daemonStartConfig{connectionName: a.connectionStateName(ctx), remember: true})
	return err
}

func newlineIfNotEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return "\n"
}

func saferWebResearchRetryParallel(effectiveParallel int, backpressureApplied bool) int {
	parallel := effectiveParallel
	if parallel <= 0 {
		parallel = 1
	}
	if parallel > 4 {
		parallel = 4
	}
	if backpressureApplied && parallel > 2 {
		parallel = 2
	}
	return parallel
}

func webResearchRetryCommand(urlFile, outDir string, wait time.Duration, waitUntil, selector string, maxPages, minVisibleWords, minMarkdownWords, minHTMLChars, parallel int) string {
	failedURLFile := filepath.Join(outDir, "failed-urls.txt")
	parts := []string{
		"cdp", "workflow", "web-research", "extract",
		"--url-file", failedURLFile,
		"--out-dir", outDir,
		"--parallel", fmt.Sprint(parallel),
		"--wait", wait.String(),
		"--wait-until", waitUntil,
		"--selector", selector,
		"--min-visible-words", fmt.Sprint(minVisibleWords),
		"--min-markdown-words", fmt.Sprint(minMarkdownWords),
		"--min-html-chars", fmt.Sprint(minHTMLChars),
		"--json",
	}
	if maxPages > 0 {
		parts = append(parts[:8], append([]string{"--max-pages", fmt.Sprint(maxPages)}, parts[8:]...)...)
	}
	_ = urlFile
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		quoted = append(quoted, shellQuote(part))
	}
	return "#!/bin/sh\nset -eu\n" + strings.Join(quoted, " ") + "\n"
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '/' || r == '.' || r == ':' || r == '=' || r == ',' || r == '+' || r == '@' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
