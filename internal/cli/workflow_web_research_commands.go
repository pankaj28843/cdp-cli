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
	var fallbackSerp string
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
	var progress string
	var fastFailBlocked bool
	var blockedFailureThreshold int
	var parallelEngines bool
	cmd := &cobra.Command{
		Use:   "serp",
		Short: "Collect rendered SERP artifacts and deduped research candidates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait < 0 || maxCandidates < 0 || parallel < 0 || resultPages < 0 || minVisibleWords < 0 || minMarkdownWords < 0 || minHTMLChars < 0 || blockedFailureThreshold < 0 {
				return commandError("usage", "usage", "--wait, --max-candidates, --parallel, --result-pages, --blocked-failure-threshold, and quality thresholds must be non-negative", ExitUsage, []string{"cdp workflow web-research serp --query-file tmp/queries.txt --result-pages 3 --out-dir tmp/research --fast-fail-blocked --json"})
			}
			progress = strings.TrimSpace(strings.ToLower(progress))
			if progress == "" {
				progress = "none"
			}
			if progress != "none" && progress != "stderr" {
				return commandError("usage", "usage", "--progress must be none or stderr", ExitUsage, []string{"cdp workflow web-research serp --query-file tmp/queries.txt --progress stderr --json"})
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
			primarySerps, err := parseWebResearchSERPs(serp)
			if err != nil {
				return commandError("usage", "usage", "--serp must be one of, all, or a comma-separated list from: "+webResearchSERPList(), ExitUsage, []string{"cdp workflow web-research serp --serp google --json", "cdp workflow web-research serp --serp google,bing --json", "cdp workflow web-research serp --serp all --json"})
			}
			serp = strings.Join(primarySerps, ",")
			fallbackSerp = strings.TrimSpace(strings.ToLower(fallbackSerp))
			if fallbackSerp == "" {
				fallbackSerp = "auto"
			}
			resolvedFallbackSerp := fallbackSerp
			if fallbackSerp == "auto" {
				if len(primarySerps) != 1 || primarySerps[0] == "google" {
					resolvedFallbackSerp = "none"
				} else {
					resolvedFallbackSerp = "google"
				}
			}
			if resolvedFallbackSerp != "none" {
				if len(primarySerps) != 1 {
					return commandError("usage", "usage", "--fallback-serp other than none requires a single primary --serp engine", ExitUsage, []string{"cdp workflow web-research serp --serp duckduckgo --fallback-serp google --json", "cdp workflow web-research serp --serp google,bing --fallback-serp none --json"})
				}
				if !isWebResearchSupportedSERP(resolvedFallbackSerp) {
					return commandError("usage", "usage", "--fallback-serp must be auto, none, or one of: "+webResearchSERPList(), ExitUsage, []string{"cdp workflow web-research serp --serp duckduckgo --fallback-serp google --json"})
				}
				if resolvedFallbackSerp == primarySerps[0] {
					return commandError("usage", "usage", "--fallback-serp must differ from --serp", ExitUsage, []string{"cdp workflow web-research serp --serp duckduckgo --fallback-serp google --json"})
				}
			}

			ctx := cmd.Context()
			queriesPath := filepath.Join(outDir, "queries.json")
			effectiveParallelEngines := parallelEngines && len(primarySerps) > 1
			perEngineParallel := parallel
			if effectiveParallelEngines {
				perEngineParallel = 1
			}
			parallelEngineCount := 1
			if effectiveParallelEngines {
				parallelEngineCount = len(primarySerps)
			}
			queriesPayload, err := json.MarshalIndent(map[string]any{"queries": queries, "count": len(queries), "serp": serp, "serps": primarySerps, "fallback_serp": fallbackSerp, "resolved_fallback_serp": resolvedFallbackSerp, "result_pages": resultPages, "parallel_engines": effectiveParallelEngines, "per_engine_parallel": perEngineParallel}, "", "  ")
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
				Serp       string
				QueryIndex int
				SerpPage   int
				Query      webResearchQuery
				Result     renderedExtractResult
				Err        error
			}
			type serpRunStats struct {
				Scheduled int
				Completed int
				Blocked   int
				FastFail  bool
			}
			type serpEngineLane struct {
				Serp        string `json:"serp"`
				PageReused  bool   `json:"page_reused"`
				CreatedPage bool   `json:"created_page"`
				Closed      bool   `json:"closed"`
				CloseError  string `json:"close_error,omitempty"`
				JobCount    int    `json:"job_count"`
			}
			type serpBatchResult struct {
				Index   int
				Serp    string
				Results []serpResult
				Stats   serpRunStats
				Lane    serpEngineLane
			}
			runJob := func(activeSerp, artifactRoot string, job serpJob, reusePage *renderedExtractReusablePage) serpResult {
				query := queries[job.QueryIndex]
				queryURL := webResearchSearchURL(activeSerp, query.Text, query.TimeFilter, job.SerpPage)
				result, err := a.runRenderedExtractWorkflow(cmd, renderedExtractOptions{
					WorkflowName:       "web-research-serp",
					ArtifactTypePrefix: "web-research-serp",
					UsageCommand:       "cdp workflow web-research serp",
					RawURL:             queryURL,
					Selector:           "body",
					Wait:               wait,
					WaitUntil:          waitUntil,
					Formats:            "snapshot,text,html,markdown,links",
					OutDir:             filepath.Join(artifactRoot, webResearchSlug(query.Text), fmt.Sprintf("page-%d", job.SerpPage)),
					Serp:               activeSerp,
					Limit:              80,
					MinVisibleWords:    minVisibleWords,
					MinMarkdownWords:   minMarkdownWords,
					MinHTMLChars:       minHTMLChars,
					ReusePage:          reusePage,
				})
				return serpResult{Serp: activeSerp, QueryIndex: job.QueryIndex, SerpPage: job.SerpPage, Query: query, Result: result, Err: err}
			}
			blockedFailureLimit := blockedFailureThreshold
			if blockedFailureLimit == 0 {
				blockedFailureLimit = 3
			}
			runBatch := func(activeSerp, artifactRoot string, batchParallel int) ([]serpResult, serpRunStats, serpEngineLane) {
				jobs := make(chan serpJob)
				resultCount := len(queries) * resultPages
				results := make(chan serpResult, resultCount)
				var wg sync.WaitGroup
				if batchParallel <= 0 {
					batchParallel = 1
				}
				var reusePage *renderedExtractReusablePage
				lane := serpEngineLane{Serp: activeSerp}
				if batchParallel == 1 && resultCount > 0 {
					page, err := a.openRenderedExtractReusablePage(ctx, "about:blank", "web-research-serp-"+activeSerp)
					if err != nil {
						for queryIndex, query := range queries {
							for page := 1; page <= resultPages; page++ {
								results <- serpResult{Serp: activeSerp, QueryIndex: queryIndex, SerpPage: page, Query: query, Err: err}
							}
						}
						close(results)
						failed := make([]serpResult, 0, resultCount)
						for result := range results {
							failed = append(failed, result)
						}
						sort.SliceStable(failed, func(i, j int) bool {
							if failed[i].QueryIndex == failed[j].QueryIndex {
								return failed[i].SerpPage < failed[j].SerpPage
							}
							return failed[i].QueryIndex < failed[j].QueryIndex
						})
						return failed, serpRunStats{Scheduled: resultCount, Completed: resultCount}, lane
					}
					reusePage = page
					lane.PageReused = true
					lane.CreatedPage = true
				}
				closeLane := func(jobCount int) serpEngineLane {
					lane.JobCount = jobCount
					if reusePage != nil {
						lane.Closed, lane.CloseError = reusePage.Close(ctx)
					}
					return lane
				}
				for i := 0; i < batchParallel; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for job := range jobs {
							results <- runJob(activeSerp, artifactRoot, job, reusePage)
						}
					}()
				}
				scheduledJobs := 0
				completedJobs := 0
				activeJobs := 0
				blockedFailures := 0
				fastFailTriggered := false
				serpResults := make([]serpResult, 0, resultCount)
				nextQueryIndex := 0
				nextSerpPage := 1
				nextJob := func() (serpJob, bool) {
					if nextQueryIndex >= len(queries) {
						return serpJob{}, false
					}
					job := serpJob{QueryIndex: nextQueryIndex, SerpPage: nextSerpPage}
					nextSerpPage++
					if nextSerpPage > resultPages {
						nextSerpPage = 1
						nextQueryIndex++
					}
					return job, true
				}
				recordFastFailSignal := func(result serpResult) {
					if blocked, _ := detectSERPBlocked(result.Result); blocked {
						blockedFailures++
					} else {
						blockedFailures = 0
					}
					if blockedFailures >= blockedFailureLimit {
						fastFailTriggered = true
					}
				}
				emitProgress := func(result serpResult) {
					if progress != "stderr" {
						return
					}
					blocked, signals := detectSERPBlocked(result.Result)
					event := map[string]any{"event": "serp_page_complete", "serp": activeSerp, "query": result.Query.Text, "time_filter": result.Query.TimeFilter, "serp_page": result.SerpPage, "completed_result_pages": completedJobs, "scheduled_result_pages": scheduledJobs, "blocked": blocked, "signals": signals}
					if result.Err != nil {
						event["err_class"] = classifyWorkflowSERPFailure(result.Err, result.Result)
						event["error"] = result.Err.Error()
					} else if blocked {
						event["err_class"] = "serp_blocked"
					}
					if payload, err := json.Marshal(event); err == nil {
						fmt.Fprintln(a.err, string(payload))
					}
				}
				scheduleNext := func() bool {
					if fastFailTriggered {
						return false
					}
					job, ok := nextJob()
					if !ok {
						return false
					}
					jobs <- job
					scheduledJobs++
					activeJobs++
					return true
				}
				if fastFailBlocked && batchParallel == 1 {
					for {
						job, ok := nextJob()
						if !ok {
							break
						}
						result := runJob(activeSerp, artifactRoot, job, reusePage)
						serpResults = append(serpResults, result)
						scheduledJobs++
						completedJobs++
						recordFastFailSignal(result)
						emitProgress(result)
						if fastFailTriggered {
							break
						}
					}
				} else {
					for activeJobs < batchParallel && scheduleNext() {
					}
					for activeJobs > 0 {
						result := <-results
						serpResults = append(serpResults, result)
						completedJobs++
						activeJobs--
						if fastFailBlocked {
							recordFastFailSignal(result)
						}
						emitProgress(result)
						for activeJobs < batchParallel && scheduleNext() {
						}
					}
				}
				close(jobs)
				wg.Wait()
				close(results)
				sort.SliceStable(serpResults, func(i, j int) bool {
					if serpResults[i].QueryIndex == serpResults[j].QueryIndex {
						return serpResults[i].SerpPage < serpResults[j].SerpPage
					}
					return serpResults[i].QueryIndex < serpResults[j].QueryIndex
				})
				return serpResults, serpRunStats{Scheduled: scheduledJobs, Completed: completedJobs, Blocked: blockedFailures, FastFail: fastFailTriggered}, closeLane(completedJobs)
			}

			primaryArtifactRoot := func(activeSerp string) string {
				if len(primarySerps) == 1 {
					return filepath.Join(outDir, "serps")
				}
				return filepath.Join(outDir, "serps", activeSerp)
			}
			runPrimaryBatches := func() []serpBatchResult {
				batches := make([]serpBatchResult, len(primarySerps))
				if effectiveParallelEngines {
					var wg sync.WaitGroup
					for index, activeSerp := range primarySerps {
						index := index
						activeSerp := activeSerp
						wg.Add(1)
						go func() {
							defer wg.Done()
							results, batchStats, lane := runBatch(activeSerp, primaryArtifactRoot(activeSerp), perEngineParallel)
							batches[index] = serpBatchResult{Index: index, Serp: activeSerp, Results: results, Stats: batchStats, Lane: lane}
						}()
					}
					wg.Wait()
					return batches
				}
				for index, activeSerp := range primarySerps {
					results, batchStats, lane := runBatch(activeSerp, primaryArtifactRoot(activeSerp), perEngineParallel)
					batches[index] = serpBatchResult{Index: index, Serp: activeSerp, Results: results, Stats: batchStats, Lane: lane}
				}
				return batches
			}
			primaryBatches := runPrimaryBatches()
			stats := make([]serpRunStats, 0, len(primaryBatches)+1)
			engineLanes := make([]serpEngineLane, 0, len(primaryBatches)+1)
			primaryResults := make([]serpResult, 0, len(primarySerps)*len(queries)*resultPages)
			for _, batch := range primaryBatches {
				stats = append(stats, batch.Stats)
				engineLanes = append(engineLanes, batch.Lane)
				primaryResults = append(primaryResults, batch.Results...)
			}
			fallbackTriggered := false
			fallbackReason := ""

			processResults := func(results []serpResult, serpReports []map[string]any, failures []map[string]any, warnings []string, candidates []webResearchCandidate, seen map[string]bool) ([]map[string]any, []map[string]any, []string, []webResearchCandidate) {
				for _, result := range results {
					if result.Err != nil {
						failures = append(failures, map[string]any{"serp": result.Serp, "query": result.Query.Text, "time_filter": result.Query.TimeFilter, "serp_page": result.SerpPage, "err_class": classifyWorkflowSERPFailure(result.Err, result.Result), "error": result.Err.Error()})
						continue
					}
					serpReports = append(serpReports, map[string]any{"serp": result.Serp, "query": result.Query.Text, "time_filter": result.Query.TimeFilter, "serp_page": result.SerpPage, "report": result.Result.Report})
					if blocked, signals := detectSERPBlocked(result.Result); blocked {
						failures = append(failures, map[string]any{"serp": result.Serp, "query": result.Query.Text, "time_filter": result.Query.TimeFilter, "serp_page": result.SerpPage, "err_class": "serp_blocked", "message": result.Serp + " served a consent, CAPTCHA, auth, or bot-check page", "signals": signals})
						warnings = append(warnings, fmt.Sprintf("%s SERP page %d for query %q was blocked by a consent, CAPTCHA, auth, or bot-check page", result.Serp, result.SerpPage, result.Query.Text))
						continue
					}
					for _, link := range result.Result.Links.Results {
						key := normalizeResearchURL(link.URL)
						if key == "" || seen[key] {
							continue
						}
						seen[key] = true
						globalRank := (result.SerpPage-1)*10 + link.Rank
						candidates = append(candidates, webResearchCandidate{Serp: result.Serp, Query: result.Query.Text, TimeFilter: result.Query.TimeFilter, SerpPage: result.SerpPage, RankOnPage: link.Rank, GlobalRank: globalRank, Rank: globalRank, Title: link.Title, Source: link.DisplayURL, Preview: link.Snippet, URL: link.URL, Type: link.Type})
						if maxCandidates > 0 && len(candidates) >= maxCandidates {
							break
						}
					}
					if maxCandidates > 0 && len(candidates) >= maxCandidates {
						break
					}
				}
				return serpReports, failures, warnings, candidates
			}

			resultCount := len(queries) * resultPages
			serpReports := make([]map[string]any, 0, resultCount)
			failures := make([]map[string]any, 0)
			warnings := make([]string, 0)
			candidates := make([]webResearchCandidate, 0)
			seen := map[string]bool{}
			serpReports, failures, warnings, candidates = processResults(primaryResults, serpReports, failures, warnings, candidates, seen)
			primaryFailureCount := len(failures)
			primaryBlockedFailures := countSERPFailures(failures, "serp_blocked")
			primaryCandidateCount := len(candidates)
			for _, batch := range primaryBatches {
				if batch.Stats.FastFail {
					warnings = append(warnings, fmt.Sprintf("%s SERP sampling stopped early after %d consecutive blocked pages", batch.Serp, batch.Stats.Blocked))
				}
			}
			if resolvedFallbackSerp != "none" && primaryCandidateCount == 0 && primaryFailureCount > 0 && primaryBlockedFailures == primaryFailureCount {
				fallbackTriggered = true
				fallbackReason = fmt.Sprintf("%s produced zero candidates after %d blocked SERP pages", primarySerps[0], primaryBlockedFailures)
				warnings = append(warnings, fmt.Sprintf("%s; running fallback SERP %s", fallbackReason, resolvedFallbackSerp))
				fallbackResults, fallbackStats, fallbackLane := runBatch(resolvedFallbackSerp, filepath.Join(outDir, "fallback-serps", resolvedFallbackSerp), parallel)
				stats = append(stats, fallbackStats)
				engineLanes = append(engineLanes, fallbackLane)
				serpReports, failures, warnings, candidates = processResults(fallbackResults, serpReports, failures, warnings, candidates, seen)
				if fallbackStats.FastFail {
					warnings = append(warnings, fmt.Sprintf("%s fallback SERP sampling stopped early after %d consecutive blocked pages", resolvedFallbackSerp, fallbackStats.Blocked))
				}
			}
			if len(candidates) == 0 && len(failures) > 0 {
				warnings = append(warnings, "SERP sampling produced zero candidates because one or more pages were blocked or failed")
			}

			engineOrder := map[string]int{}
			for index, engine := range primarySerps {
				engineOrder[engine] = index
			}
			if fallbackTriggered {
				engineOrder[resolvedFallbackSerp] = len(engineOrder)
			}
			sort.SliceStable(candidates, func(i, j int) bool {
				if candidates[i].Query == candidates[j].Query {
					if engineOrder[candidates[i].Serp] != engineOrder[candidates[j].Serp] {
						return engineOrder[candidates[i].Serp] < engineOrder[candidates[j].Serp]
					}
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

			scheduledResultPages := 0
			completedResultPages := 0
			fastFailTriggered := false
			for _, stat := range stats {
				scheduledResultPages += stat.Scheduled
				completedResultPages += stat.Completed
				if stat.FastFail {
					fastFailTriggered = true
				}
			}
			report := map[string]any{
				"ok":         len(failures) == 0,
				"queries":    queries,
				"serps":      serpReports,
				"candidates": candidates,
				"warnings":   warnings,
				"failures":   failures,
				"artifacts": map[string]string{
					"queries_json":    queriesPath,
					"candidates_json": candidateOut,
					"candidates_tsv":  candidatesTSV,
				},
				"workflow": map[string]any{
					"name":                      "web-research-serp",
					"serp":                      serp,
					"serps":                     primarySerps,
					"engine_count":              len(primarySerps),
					"parallel_engines":          effectiveParallelEngines,
					"parallel_engine_count":     parallelEngineCount,
					"per_engine_parallel":       perEngineParallel,
					"engine_lanes":              engineLanes,
					"fallback_serp":             fallbackSerp,
					"resolved_fallback_serp":    resolvedFallbackSerp,
					"fallback_triggered":        fallbackTriggered,
					"fallback_reason":           fallbackReason,
					"query_count":               len(queries),
					"candidate_count":           len(candidates),
					"failure_count":             len(failures),
					"primary_candidate_count":   primaryCandidateCount,
					"primary_failure_count":     primaryFailureCount,
					"primary_blocked_failures":  primaryBlockedFailures,
					"warning_count":             len(warnings),
					"max_candidates":            maxCandidates,
					"result_pages":              resultPages,
					"scheduled_result_pages":    scheduledResultPages,
					"completed_result_pages":    completedResultPages,
					"fast_fail_blocked":         fastFailBlocked,
					"blocked_failure_threshold": blockedFailureLimit,
					"fast_fail_triggered":       fastFailTriggered,
					"progress":                  progress,
					"parallel":                  parallel,
					"out_dir":                   outDir,
					"next_commands":             []string{"jq -r '.[].url' " + candidateOut + " > " + filepath.Join(outDir, "visit-urls.txt"), "cdp workflow web-research extract --url-file " + filepath.Join(outDir, "visit-urls.txt") + " --out-dir " + filepath.Join(outDir, "pages") + " --json"},
				},
			}
			return a.render(ctx, fmt.Sprintf("web-research-serp\t%d queries\t%d candidates", len(queries), len(candidates)), report)
		},
	}
	cmd.Flags().StringVar(&queryFile, "query-file", "", "newline-delimited search queries to sample")
	cmd.Flags().StringVar(&serp, "serp", "google", "SERP extractor: google, bing, brave, duckduckgo, kagi, all, or a comma-separated engine list")
	cmd.Flags().StringVar(&fallbackSerp, "fallback-serp", "auto", "fallback SERP after blocked zero-candidate primary runs: auto, none, google, bing, brave, duckduckgo, or kagi")
	cmd.Flags().IntVar(&maxCandidates, "max-candidates", 100, "maximum deduped candidates to emit; use 0 for no limit")
	cmd.Flags().StringVar(&candidateOut, "candidate-out", "", "path for deduped candidates JSON")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "directory for SERP artifacts and candidate files")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "maximum time to wait for each rendered SERP")
	cmd.Flags().StringVar(&waitUntil, "wait-until", "useful-content", "readiness gate: useful-content, load, or dom-stable")
	cmd.Flags().IntVar(&parallel, "parallel", 3, "maximum parallel SERP tabs, capped at 3")
	cmd.Flags().IntVar(&resultPages, "result-pages", 1, "SERP result pages per query to sample, capped at 3")
	cmd.Flags().IntVar(&minVisibleWords, "min-visible-words", 5, "warning threshold for visible text word count")
	cmd.Flags().IntVar(&minMarkdownWords, "min-markdown-words", 5, "warning threshold for Markdown word count")
	cmd.Flags().IntVar(&minHTMLChars, "min-html-chars", 64, "warning threshold for extracted HTML character count")
	cmd.Flags().StringVar(&progress, "progress", "none", "progress event stream: none or stderr")
	cmd.Flags().BoolVar(&fastFailBlocked, "fast-fail-blocked", false, "stop SERP sampling early after repeated consent, CAPTCHA, auth, or bot-check pages")
	cmd.Flags().IntVar(&blockedFailureThreshold, "blocked-failure-threshold", 3, "consecutive blocked SERP pages required before --fast-fail-blocked stops scheduling")
	cmd.Flags().BoolVar(&parallelEngines, "parallel-engines", true, "run comma-separated or all SERP engines concurrently with one reusable page lane per engine")
	return cmd
}

func classifyWorkflowSERPFailure(err error, result renderedExtractResult) string {
	if blocked, _ := detectSERPBlocked(result); blocked {
		return "serp_blocked"
	}
	return classifyWorkflowExtractFailure(err)
}

func detectSERPBlocked(result renderedExtractResult) (bool, []string) {
	signals := make([]string, 0)
	finalURL := ""
	if workflow, ok := result.Report["workflow"].(map[string]any); ok {
		finalURL = fmt.Sprint(workflow["final_url"])
	}
	lowerURL := strings.ToLower(finalURL)
	if strings.Contains(lowerURL, "/sorry/") || strings.Contains(lowerURL, "captcha") || strings.Contains(lowerURL, "consent") || strings.Contains(lowerURL, "challenge") || strings.Contains(lowerURL, "signin") || strings.Contains(lowerURL, "login") {
		signals = append(signals, "blocked_final_url")
	}
	for _, warning := range result.Warnings {
		lower := strings.ToLower(warning)
		if strings.Contains(lower, "consent") || strings.Contains(lower, "captcha") || strings.Contains(lower, "bot-check") || strings.Contains(lower, "auth") {
			signals = append(signals, "bot_check_warning")
		}
		if strings.Contains(lower, "unusual traffic") ||
			strings.Contains(lower, "not a robot") ||
			strings.Contains(lower, "enable javascript") ||
			strings.Contains(lower, "unfortunately, bots use") ||
			strings.Contains(lower, "select all squares") ||
			strings.Contains(lower, "page text suggests") ||
			strings.Contains(lower, "sign in") ||
			strings.Contains(lower, "login") {
			signals = append(signals, "block_page_text")
		}
		if strings.Contains(lower, "serp extraction found no") && strings.Contains(lower, "external result links") {
			signals = append(signals, "no_external_result_links")
		}
	}
	return stringListContains(signals, "blocked_final_url") || stringListContains(signals, "block_page_text") || (stringListContains(signals, "bot_check_warning") && stringListContains(signals, "no_external_result_links")), signals
}

func stringListContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func countSERPFailures(failures []map[string]any, errClass string) int {
	count := 0
	for _, failure := range failures {
		if fmt.Sprint(failure["err_class"]) == errClass {
			count++
		}
	}
	return count
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
