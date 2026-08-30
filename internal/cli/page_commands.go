package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
)

var pageCleanupRecordsMu sync.Mutex

const (
	defaultPageCloseMaxAttempts        = 3
	defaultPageClosePollInterval       = 100 * time.Millisecond
	defaultPageCloseRetryBackoff       = 200 * time.Millisecond
	defaultPageCleanupCloseConcurrency = 4
	pageIndexOrder                     = "target_id_ascending"
)

func (a *app) newTargetsCommand() *cobra.Command {
	var limit int
	var targetType string
	var retryOpts commandRetryOptions
	cmd := &cobra.Command{
		Use:   "targets",
		Short: "List browser targets",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			result, retryReport, err := runCommandWithRetry(ctx, retryOpts, func(attemptCtx context.Context) (commandRetryResult, error) {
				targets, err := a.listTargets(attemptCtx)
				if err != nil {
					return commandRetryResult{}, err
				}
				targets = filterTargetsByType(targets, targetType)
				rows := targetRows(targets)
				rows = limitRows(rows, limit)
				return commandRetryResult{
					Human: strings.Join(targetHumanLines(rows), "\n"),
					Data:  map[string]any{"ok": true, "targets": rows},
				}, nil
			})
			if err != nil {
				return err
			}
			attachCommandRetryReport(result.Data, retryReport)
			return a.render(ctx, result.Human, result.Data)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of targets to return; use 0 for no limit")
	cmd.Flags().StringVar(&targetType, "type", "", "only return targets of this CDP type, such as page or service_worker")
	addCommandRetryFlags(cmd, &retryOpts)
	return cmd
}

func (a *app) newPagesCommand() *cobra.Command {
	var limit int
	var urlContains string
	var titleContains string
	var includeURL string
	var excludeURL string
	var retryOpts commandRetryOptions
	cmd := &cobra.Command{
		Use:   "pages",
		Short: "List open pages and tabs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			result, retryReport, err := runCommandWithRetry(ctx, retryOpts, func(attemptCtx context.Context) (commandRetryResult, error) {
				targets, err := a.listTargets(attemptCtx)
				if err != nil {
					return commandRetryResult{}, err
				}
				client, closeClient, err := a.browserCDPClient(attemptCtx)
				if err != nil {
					return commandRetryResult{}, commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
				}
				defer closeClient(attemptCtx)
				budget := cdp.BrowserBudgetForTargets(attemptCtx, client, targets, a.browserResourceBudgetOptions())
				pages := pageRows(targets)
				pages = filterRowsContains(pages, "url", firstNonEmpty(urlContains, includeURL))
				pages = filterRowsContains(pages, "title", titleContains)
				pages = filterRowsExcludes(pages, "url", excludeURL)
				pages = limitRows(pages, limit)
				return commandRetryResult{
					Human: strings.Join(pageHumanLines(pages), "\n"),
					Data:  map[string]any{"ok": true, "pages": pages, "index_order": pageIndexOrder, "budget": budget},
				}, nil
			})
			if err != nil {
				return err
			}
			attachCommandRetryReport(result.Data, retryReport)
			return a.render(ctx, result.Human, result.Data)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of pages to return; use 0 for no limit")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "only return pages whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "only return pages whose title contains this text")
	cmd.Flags().StringVar(&includeURL, "include-url", "", "only return pages whose URL contains this text")
	cmd.Flags().StringVar(&excludeURL, "exclude-url", "", "exclude pages whose URL contains this text")
	addCommandRetryFlags(cmd, &retryOpts)
	return cmd
}

func (a *app) listTargets(ctx context.Context) ([]cdp.TargetInfo, error) {
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return nil, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			a.connectionRemediationCommands(),
		)
	}
	defer closeClient(ctx)

	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return nil, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("list targets: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp daemon status --json"},
		)
	}
	return targets, nil
}

func targetRows(targets []cdp.TargetInfo) []map[string]any {
	rows := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		rows = append(rows, map[string]any{
			"id":                 target.TargetID,
			"short_id":           shortTargetID(target.TargetID),
			"type":               target.Type,
			"title":              target.Title,
			"url":                target.URL,
			"attached":           target.Attached,
			"browser_context_id": target.BrowserContextID,
		})
	}
	return rows
}

func filterTargetsByType(targets []cdp.TargetInfo, targetType string) []cdp.TargetInfo {
	targetType = strings.TrimSpace(targetType)
	if targetType == "" {
		return targets
	}
	filtered := make([]cdp.TargetInfo, 0, len(targets))
	for _, target := range targets {
		if target.Type == targetType {
			filtered = append(filtered, target)
		}
	}
	return filtered
}

func limitRows(rows []map[string]any, limit int) []map[string]any {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}

func filterRowsContains(rows []map[string]any, key, needle string) []map[string]any {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return rows
	}
	filtered := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		value, _ := row[key].(string)
		if strings.Contains(strings.ToLower(value), strings.ToLower(needle)) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterRowsExcludes(rows []map[string]any, key, needle string) []map[string]any {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return rows
	}
	filtered := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		value, _ := row[key].(string)
		if !strings.Contains(value, needle) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func pageRows(targets []cdp.TargetInfo) []map[string]any {
	ordered := orderedPageTargets(targets)
	pages := make([]map[string]any, 0, len(ordered))
	for index, target := range ordered {
		row := pageRow(target)
		row["index"] = index + 1
		pages = append(pages, row)
	}
	return pages
}

func orderedPageTargets(targets []cdp.TargetInfo) []cdp.TargetInfo {
	pages := make([]cdp.TargetInfo, 0, len(targets))
	for _, target := range targets {
		if target.Type == "page" {
			pages = append(pages, target)
		}
	}
	sort.SliceStable(pages, func(i, j int) bool {
		return pages[i].TargetID < pages[j].TargetID
	})
	return pages
}

func targetHumanLines(rows []map[string]any) []string {
	lines := make([]string, 0, len(rows))
	for _, target := range rows {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s\t%q", target["short_id"], target["id"], target["type"], target["url"], target["title"]))
	}
	return lines
}

func pageHumanLines(rows []map[string]any) []string {
	lines := make([]string, 0, len(rows))
	for _, page := range rows {
		lines = append(lines, fmt.Sprintf("[%v]\t%s\t%s\t%s\t%q", page["index"], page["short_id"], page["id"], page["url"], page["title"]))
	}
	return lines
}

func pageRow(target cdp.TargetInfo) map[string]any {
	return map[string]any{
		"id":       target.TargetID,
		"short_id": shortTargetID(target.TargetID),
		"type":     target.Type,
		"title":    target.Title,
		"url":      target.URL,
		"attached": target.Attached,
	}
}

func shortTargetID(targetID string) string {
	const shortLength = 8
	targetID = strings.ToUpper(strings.TrimSpace(targetID))
	if len(targetID) > shortLength {
		return targetID[:shortLength]
	}
	return targetID
}

func targetIDMatchesPrefix(targetID, prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	return prefix != "" && strings.HasPrefix(strings.ToUpper(targetID), strings.ToUpper(prefix))
}

func ambiguousTargetEvidence(matches []cdp.TargetInfo) map[string]any {
	count, shortIDs, ids, truncated := boundedTargetIDs(matches)
	return map[string]any{
		"candidate_count":     count,
		"candidate_ids":       ids,
		"candidate_short_ids": shortIDs,
		"candidate_truncated": truncated,
	}
}

func ambiguousPageTargetEvidence(matches, pages []cdp.TargetInfo) map[string]any {
	data := ambiguousTargetEvidence(matches)
	data["candidate_indexes"] = boundedPageIndexes(matches, pages)
	return data
}

func availableTargetEvidence(targets []cdp.TargetInfo) map[string]any {
	count, shortIDs, ids, truncated := boundedTargetIDs(targets)
	return map[string]any{
		"available_count":     count,
		"available_ids":       ids,
		"available_short_ids": shortIDs,
		"available_truncated": truncated,
	}
}

func availablePageTargetEvidence(pages []cdp.TargetInfo) map[string]any {
	data := availableTargetEvidence(pages)
	data["available_indexes"] = boundedPageIndexes(pages, pages)
	return data
}

func boundedPageIndexes(targets, pages []cdp.TargetInfo) []int {
	const limit = 10
	if len(targets) > limit {
		targets = targets[:limit]
	}
	indexByID := make(map[string]int, len(pages))
	for i, page := range pages {
		indexByID[page.TargetID] = i + 1
	}
	indexes := make([]int, 0, len(targets))
	for _, target := range targets {
		if index, ok := indexByID[target.TargetID]; ok {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func boundedTargetIDs(targets []cdp.TargetInfo) (int, []string, []string, bool) {
	const limit = 10
	count := len(targets)
	if len(targets) > limit {
		targets = targets[:limit]
	}
	shortIDs := make([]string, 0, len(targets))
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		shortIDs = append(shortIDs, shortTargetID(target.TargetID))
		ids = append(ids, target.TargetID)
	}
	return count, shortIDs, ids, count > limit
}

func (a *app) newPageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "page",
		Short: "Control an open page target",
	}
	cmd.AddCommand(a.newPageSelectCommand())
	cmd.AddCommand(a.newPageReloadCommand())
	cmd.AddCommand(a.newPageHistoryCommand("back", "Navigate the selected page back in history", -1))
	cmd.AddCommand(a.newPageHistoryCommand("forward", "Navigate the selected page forward in history", 1))
	cmd.AddCommand(a.newPageActivateCommand())
	cmd.AddCommand(a.newPageCloseCommand())
	cmd.AddCommand(a.newPageCleanupCommand())
	return cmd
}

func (a *app) newPageSelectCommand() *cobra.Command {
	var urlContains string
	var titleContains string
	var targetIndex int
	cmd := &cobra.Command{
		Use:   "select [target-id]",
		Short: "Select the default page target for subsequent commands",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetID := ""
			if len(args) == 1 {
				targetID = args[0]
			}
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			if targetIndex == 0 && strings.TrimSpace(targetID) == "" && strings.TrimSpace(urlContains) == "" && strings.TrimSpace(titleContains) == "" {
				return commandError(
					"missing_page_selector",
					"usage",
					"page select requires a target id/prefix or --url-contains",
					ExitUsage,
					[]string{"cdp page select <target-id> --json", "cdp page select --url-contains localhost --json"},
				)
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					a.connectionRemediationCommands(),
				)
			}
			defer closeClient(ctx)

			var target cdp.TargetInfo
			if targetIndex > 0 {
				target, err = a.resolvePageTargetWithClientIndex(ctx, client, targetID, urlContains, titleContains, targetIndex)
			} else {
				target, err = a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
			}
			if err != nil {
				return err
			}
			selection := state.PageSelection{
				BrowserMode: a.browserModeName(),
				Connection:  a.connectionStateName(ctx),
				TargetID:    target.TargetID,
				URL:         target.URL,
				Title:       target.Title,
				SelectedAt:  time.Now().UTC().Format(time.RFC3339),
			}
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			file, err := store.Load(ctx)
			if err != nil {
				return err
			}
			file = state.UpsertPageSelection(file, selection)
			if err := store.Save(ctx, file); err != nil {
				return err
			}
			return a.render(ctx, fmt.Sprintf("selected\t%s", target.TargetID), map[string]any{
				"ok":            true,
				"selected_page": selection,
				"target":        pageRow(target),
				"state_path":    store.Path(),
			})
		},
	}
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "select the unique page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "select the unique page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based page target index")
	return cmd
}

func (a *app) newPageReloadCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var targetIndex int
	var ignoreCache bool
	cmd := &cobra.Command{
		Use:   "reload",
		Short: "Reload a page target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			if err := session.Reload(ctx, ignoreCache); err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("reload target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("reloaded\t%s", target.TargetID), map[string]any{
				"ok":           true,
				"action":       "reloaded",
				"target":       pageRow(target),
				"ignore_cache": ignoreCache,
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the unique page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the unique page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based page target index")
	cmd.Flags().BoolVar(&ignoreCache, "ignore-cache", false, "reload while bypassing cache")
	return cmd
}

func (a *app) newPageHistoryCommand(name, short string, offset int) *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var targetIndex int
	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			history, err := session.NavigationHistory(ctx)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("read navigation history for target %s: %v", target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			historyIndex := history.CurrentIndex + offset
			if historyIndex < 0 || historyIndex >= len(history.Entries) {
				return commandError(
					"navigation_unavailable",
					"usage",
					fmt.Sprintf("page has no %s history entry", name),
					ExitUsage,
					[]string{"cdp page reload --json", "cdp open <url> --new-tab=false --target <target-id> --json"},
				)
			}
			entry := history.Entries[historyIndex]
			if err := session.NavigateToHistoryEntry(ctx, entry.ID); err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("navigate %s for target %s: %v", name, target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("%s\t%s\t%d", name, target.TargetID, entry.ID), map[string]any{
				"ok":     true,
				"action": name,
				"target": pageRow(target),
				"history": map[string]any{
					"current_index": history.CurrentIndex,
					"target_index":  historyIndex,
					"entry_id":      entry.ID,
					"entry":         entry,
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the unique page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the unique page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based page target index")
	return cmd
}

func (a *app) newPageActivateCommand() *cobra.Command {
	return a.newPageTargetCommand("activate", "Bring a page target to the foreground", "activated", cdp.ActivateTargetWithClient)
}

func (a *app) newPageCloseCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var targetIndex int
	waitGone := true
	maxAttempts := defaultPageCloseMaxAttempts
	cmd := &cobra.Command{
		Use:   "close",
		Short: "Close a page target and wait until it is gone",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			if maxAttempts <= 0 {
				return commandError("invalid_argument", "usage", "--max-attempts must be positive", ExitUsage, []string{"cdp page close --target <target-id> --max-attempts 3 --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, pageCloseDefaultTimeout(a.browserModeName(), maxAttempts))
			defer cancel()

			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					a.connectionRemediationCommands(),
				)
			}
			defer closeClient(ctx)

			var target cdp.TargetInfo
			if targetIndex > 0 {
				target, err = a.resolvePageTargetWithClientIndex(ctx, client, targetID, urlContains, titleContains, targetIndex)
			} else {
				target, err = a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
			}
			if err != nil {
				return err
			}
			closeReport := closePageTargetSettled(ctx, client, target, pageCloseOptions{
				WaitGone:      waitGone,
				MaxAttempts:   maxAttempts,
				AttemptWait:   pageCloseAttemptTimeout(a.browserModeName()),
				PollInterval:  defaultPageClosePollInterval,
				RetryBackoff:  defaultPageCloseRetryBackoff,
				FinalPageList: true,
			})
			data := map[string]any{
				"ok":               closeReport.Closed && (!waitGone || closeReport.TargetGone),
				"action":           "closed",
				"target":           pageRow(target),
				"closed":           closeReport.Closed,
				"target_gone":      closeReport.TargetGone,
				"attempts":         closeReport.Attempts,
				"attempt_count":    closeReport.AttemptCount,
				"max_attempts":     closeReport.MaxAttempts,
				"elapsed_ms":       closeReport.ElapsedMS,
				"wait_gone":        waitGone,
				"final_page_count": closeReport.FinalPageCount,
				"last_observed":    closeReport.LastObservedTarget,
				"last_error":       closeReport.LastError,
				"retry_policy":     closeReport.RetryPolicy,
				"next_commands":    []string{"cdp pages --json", "cdp daemon health --json"},
			}
			if closeReport.Closed && (!waitGone || closeReport.TargetGone) {
				return a.render(ctx, fmt.Sprintf("closed\t%s", target.TargetID), data)
			}
			code := "page_close_failed"
			exit := ExitConnection
			if errorsIsTimeout(ctx.Err()) || closeReport.TimedOut {
				code = "timeout"
				exit = ExitTimeout
			}
			return commandErrorWithData(
				code,
				"connection",
				fmt.Sprintf("close target %s did not settle after %d attempt(s)", target.TargetID, closeReport.AttemptCount),
				exit,
				[]string{"cdp pages --json", "cdp page cleanup --close --force --json", "cdp daemon health --json"},
				data,
			)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the unique page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the unique page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based page target index")
	cmd.Flags().BoolVar(&waitGone, "wait-gone", true, "wait until target listing no longer contains the page")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", defaultPageCloseMaxAttempts, "maximum close attempts before reporting failure")
	return cmd
}

type cleanupCandidate struct {
	Target          cdp.TargetInfo   `json:"target"`
	VisibilityState string           `json:"visibility_state,omitempty"`
	Hidden          bool             `json:"hidden,omitempty"`
	Prerendering    bool             `json:"prerendering,omitempty"`
	RunID           string           `json:"run_id,omitempty"`
	TaskID          string           `json:"task_id,omitempty"`
	RootTaskID      string           `json:"root_task_id,omitempty"`
	ParentTaskID    string           `json:"parent_task_id,omitempty"`
	CreatedBy       string           `json:"created_by,omitempty"`
	Workflow        string           `json:"workflow,omitempty"`
	FirstSeen       string           `json:"first_seen,omitempty"`
	LastSeen        string           `json:"last_seen,omitempty"`
	IdleFor         string           `json:"idle_for,omitempty"`
	EligibleAt      string           `json:"eligible_at,omitempty"`
	Ready           bool             `json:"ready"`
	KeepReason      string           `json:"keep_reason,omitempty"`
	CloseError      string           `json:"close_error,omitempty"`
	TargetGone      bool             `json:"target_gone,omitempty"`
	Close           *pageCloseReport `json:"close,omitempty"`
}

type pageCleanupRecord struct {
	BrowserMode  string `json:"browser_mode,omitempty"`
	Connection   string `json:"connection"`
	TargetID     string `json:"target_id"`
	URL          string `json:"url,omitempty"`
	Title        string `json:"title,omitempty"`
	CreatedBy    string `json:"created_by,omitempty"`
	Workflow     string `json:"workflow,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	RootTaskID   string `json:"root_task_id,omitempty"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	FirstSeen    string `json:"first_seen"`
	LastSeen     string `json:"last_seen"`
}

type pageCleanupState struct {
	Pages []pageCleanupRecord `json:"pages"`
}

type pageCleanupRunOptions struct {
	Close            bool
	IncludeAttached  bool
	IncludeURL       string
	ExcludeURL       string
	CreatedBy        string
	WorkflowCreated  bool
	OwnershipFilter  targetOwnershipFilter
	Force            bool
	ForceTarget      string
	WaitGone         bool
	MaxAttempts      int
	CloseConcurrency int
	Since            time.Duration
	IdleFor          time.Duration
	Max              int
	MaxChanged       bool
}

func (a *app) newPageCleanupCommand() *cobra.Command {
	var closePages bool
	var includeAttached bool
	var includeURL string
	var excludeURL string
	var createdBy string
	var workflowCreated bool
	var ownershipFilter targetOwnershipFilter
	var force bool
	var forceTarget string
	waitGone := true
	maxAttempts := defaultPageCloseMaxAttempts
	closeConcurrency := defaultPageCleanupCloseConcurrency
	var since time.Duration
	var idleFor time.Duration
	var max int
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Close or list inactive page targets for cron cleanup",
		Long: `Close or list inactive page targets for cron cleanup.

Chrome DevTools Protocol does not expose a reliable last-used timestamp, so this
command uses conservative signals: it only considers page targets, skips the
currently selected page when known, skips attached pages unless --include-attached
is set, and checks document.visibilityState before closing. The default is a dry
run; pass --close to close candidates after they have remained inactive for
--idle-for across cleanup runs. Use --force with narrow filters for disposable
headless agent tabs that should be closed even when visible, selected, or
attached.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if max < 0 || since < 0 || maxAttempts <= 0 || closeConcurrency <= 0 {
				return commandError("usage", "usage", "--max and --since must be non-negative, and --max-attempts/--close-concurrency must be positive", ExitUsage, []string{"cdp page cleanup --max 10 --json"})
			}
			if strings.TrimSpace(forceTarget) != "" {
				force = true
				closePages = true
			}
			timeoutFallback := 10 * time.Second
			if closePages {
				timeoutFallback = pageCloseDefaultTimeout(a.browserModeName(), maxAttempts)
			}
			ctx, cancel := a.commandContextWithDefault(cmd, timeoutFallback)
			defer cancel()
			restoreHeadlessRepair := a.disableHeadlessRepair()
			defer restoreHeadlessRepair()

			human, data, err := a.runPageCleanup(ctx, pageCleanupRunOptions{
				Close:            closePages,
				IncludeAttached:  includeAttached,
				IncludeURL:       includeURL,
				ExcludeURL:       excludeURL,
				CreatedBy:        createdBy,
				WorkflowCreated:  workflowCreated,
				OwnershipFilter:  normalizeTargetOwnershipFilter(ownershipFilter),
				Force:            force,
				ForceTarget:      forceTarget,
				WaitGone:         waitGone,
				MaxAttempts:      maxAttempts,
				CloseConcurrency: closeConcurrency,
				Since:            since,
				IdleFor:          idleFor,
				Max:              max,
				MaxChanged:       cmd.Flags().Changed("max"),
			})
			if err != nil {
				return err
			}
			return a.render(ctx, human, data)
		},
	}
	cmd.Flags().BoolVar(&closePages, "close", false, "close matching inactive page targets; default is dry-run")
	cmd.Flags().BoolVar(&includeAttached, "include-attached", false, "also consider attached page targets")
	cmd.Flags().StringVar(&includeURL, "include-url", "", "only consider pages whose URL contains this text")
	cmd.Flags().StringVar(&excludeURL, "exclude-url", "", "exclude pages whose URL contains this text")
	cmd.Flags().StringVar(&createdBy, "created-by", "", "only consider pages tagged with this creator, such as cdp")
	cmd.Flags().BoolVar(&workflowCreated, "workflow-created", false, "close pages tagged as created by cdp workflows without waiting for --idle-for")
	addTargetOwnershipFilterFlags(cmd, &ownershipFilter)
	cmd.Flags().BoolVar(&force, "force", false, "allow cleanup to bypass selected, attached, and visible protections; with --target it also bypasses idle checks")
	cmd.Flags().StringVar(&forceTarget, "target", "", "force-close a specific page target id or unique prefix when used with --force")
	cmd.Flags().BoolVar(&waitGone, "wait-gone", true, "wait until each closed target disappears from target listing")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", defaultPageCloseMaxAttempts, "maximum close attempts per target before reporting failure")
	cmd.Flags().IntVar(&closeConcurrency, "close-concurrency", defaultPageCleanupCloseConcurrency, "maximum page targets to close concurrently")
	cmd.Flags().DurationVar(&since, "since", 0, "only consider cleanup records first seen within this duration; 0 disables the filter")
	cmd.Flags().DurationVar(&idleFor, "idle-for", 30*time.Minute, "minimum duration a page must remain inactive before --close can close it")
	cmd.Flags().IntVar(&max, "max", 0, "maximum ready candidate pages to close or report; use 0 for no limit; default is 10 headed or 25 headless")
	return cmd
}

func (a *app) runPageCleanup(ctx context.Context, opts pageCleanupRunOptions) (string, map[string]any, error) {
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return "", nil, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			a.connectionRemediationCommands(),
		)
	}
	defer closeClient(ctx)

	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return "", nil, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("list targets: %v", err),
			ExitConnection,
			a.connectionRemediationCommands(),
		)
	}
	if strings.TrimSpace(opts.ForceTarget) != "" {
		target, err := resolvePageTarget(targets, opts.ForceTarget, "", "")
		if err != nil {
			return "", nil, err
		}
		opts.ForceTarget = target.TargetID
	}
	store, err := a.stateStore()
	if err != nil {
		return "", nil, err
	}
	browserMode := a.browserModeName()
	effectiveMax, maxSource := pageCleanupEffectiveMax(browserMode, opts.Max, opts.MaxChanged)
	connectionName := a.connectionStateName(ctx)
	selectedID := a.selectedPageID(ctx)
	records, stateWarnings, err := loadPageCleanupRecords(ctx, store.Dir)
	if err != nil {
		return "", nil, commandError("internal", "internal", fmt.Sprintf("read page cleanup state: %v", err), ExitInternal, []string{"cdp page cleanup --json"})
	}
	pruneLegacyHeadlessCleanupRecords(records, browserMode, connectionName)
	recordCountBefore := len(records)
	now := time.Now().UTC()
	candidates := cleanupCandidates(ctx, client, targets, cleanupOptions{
		BrowserMode:     browserMode,
		Connection:      connectionName,
		SelectedID:      selectedID,
		IncludeAttached: opts.IncludeAttached,
		IncludeURL:      opts.IncludeURL,
		ExcludeURL:      opts.ExcludeURL,
		CreatedBy:       opts.CreatedBy,
		WorkflowCreated: opts.WorkflowCreated,
		OwnershipFilter: opts.OwnershipFilter,
		Force:           opts.Force,
		ForceTarget:     opts.ForceTarget,
		Since:           opts.Since,
		IdleFor:         opts.IdleFor,
		Max:             effectiveMax,
		Now:             now,
		Records:         records,
	})
	closed := []cleanupCandidate{}
	if opts.Close {
		closeReadyCleanupCandidates(ctx, client, candidates, pageCloseOptions{
			WaitGone:      opts.WaitGone,
			MaxAttempts:   opts.MaxAttempts,
			AttemptWait:   pageCloseAttemptTimeout(browserMode),
			PollInterval:  defaultPageClosePollInterval,
			RetryBackoff:  defaultPageCloseRetryBackoff,
			FinalPageList: false,
		}, opts.CloseConcurrency)
		for i := range candidates {
			if candidates[i].Close == nil {
				continue
			}
			if candidates[i].Close.Closed && (!opts.WaitGone || candidates[i].Close.TargetGone) {
				delete(records, pageCleanupKey(browserMode, connectionName, candidates[i].Target.TargetID))
				closed = append(closed, candidates[i])
				continue
			}
			if candidates[i].Close.LastError != "" {
				candidates[i].CloseError = candidates[i].Close.LastError
			} else {
				candidates[i].CloseError = "target close did not settle"
			}
		}
	}

	if err := savePageCleanupRecords(ctx, store.Dir, records); err != nil {
		return "", nil, commandError("internal", "internal", fmt.Sprintf("write page cleanup state: %v", err), ExitInternal, []string{"cdp page cleanup --json"})
	}

	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		status := "candidate"
		if candidate.KeepReason != "" {
			status = "kept:" + candidate.KeepReason
		} else if candidate.CloseError != "" {
			status = "error"
		} else if opts.Close {
			status = "closed"
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", candidate.Target.TargetID, status, candidate.Target.Title))
	}
	readyCount := countReadyCandidates(candidates)
	wouldCloseCount := 0
	if !opts.Close {
		wouldCloseCount = readyCount
	}
	return strings.Join(lines, "\n"), map[string]any{
		"ok": true,
		"cleanup": map[string]any{
			"browser_mode":        browserMode,
			"dry_run":             !opts.Close,
			"close":               opts.Close,
			"candidate_count":     readyCount,
			"ready_count":         readyCount,
			"would_close_count":   wouldCloseCount,
			"close_required":      !opts.Close && readyCount > 0,
			"closed_count":        len(closed),
			"idle_for":            opts.IdleFor.String(),
			"state_path":          pageCleanupStatePath(store.Dir),
			"include_attached":    opts.IncludeAttached,
			"include_url":         strings.TrimSpace(opts.IncludeURL),
			"exclude_url":         strings.TrimSpace(opts.ExcludeURL),
			"created_by":          strings.TrimSpace(opts.CreatedBy),
			"workflow_created":    opts.WorkflowCreated,
			"run_id":              strings.TrimSpace(opts.OwnershipFilter.RunID),
			"task_id":             strings.TrimSpace(opts.OwnershipFilter.TaskID),
			"root_task_id":        strings.TrimSpace(opts.OwnershipFilter.RootTaskID),
			"task_scope":          opts.OwnershipFilter.isSet(),
			"target_task_ids":     targetTaskIDsForCandidates(candidates),
			"record_count_before": recordCountBefore,
			"record_count_after":  len(records),
			"force":               opts.Force,
			"force_target":        strings.TrimSpace(opts.ForceTarget),
			"wait_gone":           opts.WaitGone,
			"max_attempts":        opts.MaxAttempts,
			"close_concurrency":   opts.CloseConcurrency,
			"since":               durationString(opts.Since),
			"max":                 effectiveMax,
			"max_source":          maxSource,
			"max_unlimited":       effectiveMax == 0,
			"selected_page":       selectedID,
			"state_warnings":      stateWarnings,
			"next_commands": []string{
				"cdp page cleanup --json",
				modeScopedCommand(browserMode, fmt.Sprintf("page cleanup --close --wait-gone --max %d --json", pageCleanupDefaultMaxForMode(browserMode))),
				"cdp cron status --json",
			},
		},
		"candidates": candidates,
		"closed":     closed,
	}, nil
}

func pageCleanupEffectiveMax(browserMode string, flagMax int, flagChanged bool) (int, string) {
	if flagChanged {
		return flagMax, "flag"
	}
	return pageCleanupDefaultMaxForMode(browserMode), "mode_default"
}

func pageCleanupDefaultMaxForMode(browserMode string) int {
	if cleanupBrowserMode(browserMode) == "headless" {
		return cdp.DefaultHeadlessMaxTabs
	}
	return 10
}

type cleanupOptions struct {
	BrowserMode     string
	Connection      string
	SelectedID      string
	IncludeAttached bool
	IncludeURL      string
	ExcludeURL      string
	CreatedBy       string
	WorkflowCreated bool
	OwnershipFilter targetOwnershipFilter
	Force           bool
	ForceTarget     string
	Since           time.Duration
	IdleFor         time.Duration
	Max             int
	Now             time.Time
	Records         map[string]pageCleanupRecord
}

func cleanupCandidates(ctx context.Context, client cdp.CommandClient, targets []cdp.TargetInfo, opts cleanupOptions) []cleanupCandidate {
	candidates := []cleanupCandidate{}
	includeURL := strings.ToLower(strings.TrimSpace(opts.IncludeURL))
	excludeURL := strings.ToLower(strings.TrimSpace(opts.ExcludeURL))
	createdBy := strings.ToLower(strings.TrimSpace(opts.CreatedBy))
	forceTarget := strings.TrimSpace(opts.ForceTarget)
	seen := map[string]bool{}
	for _, target := range targets {
		if target.Type != "page" {
			continue
		}
		urlText := strings.ToLower(target.URL)
		if includeURL != "" && !strings.Contains(urlText, includeURL) {
			continue
		}
		if excludeURL != "" && strings.Contains(urlText, excludeURL) {
			continue
		}
		key := pageCleanupKey(opts.BrowserMode, opts.Connection, target.TargetID)
		record, hasRecord := opts.Records[key]
		if forceTarget != "" && !targetIDMatchesPrefix(target.TargetID, forceTarget) {
			continue
		}
		if opts.OwnershipFilter.isSet() {
			if !hasRecord || !opts.OwnershipFilter.matches(record) {
				continue
			}
		}
		if createdBy != "" && strings.ToLower(record.CreatedBy) != createdBy {
			continue
		}
		if opts.WorkflowCreated && strings.ToLower(record.CreatedBy) != "cdp" {
			continue
		}
		if opts.Since > 0 && hasRecord {
			firstSeen, err := time.Parse(time.RFC3339, record.FirstSeen)
			if err == nil && opts.Now.Sub(firstSeen) > opts.Since {
				continue
			}
		}
		seen[key] = true
		candidate := cleanupCandidate{Target: target}
		switch {
		case opts.Force && forceTarget != "":
			candidate.Ready = true
		case opts.Force:
		case target.TargetID == strings.TrimSpace(opts.SelectedID):
			candidate.KeepReason = "selected_page"
		case target.Attached && !opts.IncludeAttached:
			candidate.KeepReason = "attached"
		default:
			candidate.VisibilityState, candidate.Hidden, candidate.Prerendering = pageVisibility(ctx, client, target.TargetID)
			if candidate.VisibilityState == "visible" && !candidate.Hidden {
				candidate.KeepReason = "visible"
			}
		}
		updateCleanupRecord(&candidate, opts, key)
		if opts.WorkflowCreated && candidate.KeepReason == "visible" {
			candidate.KeepReason = ""
			candidate.Ready = true
		}
		candidates = append(candidates, candidate)
		if opts.Max > 0 && countReadyCandidates(candidates) >= opts.Max {
			break
		}
	}
	for key := range opts.Records {
		if strings.HasPrefix(key, pageCleanupScopePrefix(opts.BrowserMode, opts.Connection)) && !seen[key] {
			if opts.OwnershipFilter.isSet() && !opts.OwnershipFilter.matches(opts.Records[key]) {
				continue
			}
			delete(opts.Records, key)
		}
	}
	return candidates
}

func updateCleanupRecord(candidate *cleanupCandidate, opts cleanupOptions, key string) {
	record, ok := opts.Records[key]
	if !ok {
		record = pageCleanupRecord{
			BrowserMode: cleanupBrowserMode(opts.BrowserMode),
			Connection:  opts.Connection,
			TargetID:    candidate.Target.TargetID,
			URL:         candidate.Target.URL,
			Title:       candidate.Target.Title,
			FirstSeen:   opts.Now.Format(time.RFC3339),
		}
	} else if candidate.KeepReason != "" {
		record.FirstSeen = opts.Now.Format(time.RFC3339)
	}
	record.BrowserMode = cleanupBrowserMode(opts.BrowserMode)
	record.LastSeen = opts.Now.Format(time.RFC3339)
	record.URL = candidate.Target.URL
	record.Title = candidate.Target.Title
	opts.Records[key] = record
	candidate.RunID = record.RunID
	candidate.TaskID = record.TaskID
	candidate.RootTaskID = record.RootTaskID
	candidate.ParentTaskID = record.ParentTaskID
	candidate.CreatedBy = record.CreatedBy
	candidate.Workflow = record.Workflow
	candidate.FirstSeen = record.FirstSeen
	candidate.LastSeen = record.LastSeen
	firstSeen, err := time.Parse(time.RFC3339, record.FirstSeen)
	if err != nil {
		return
	}
	idle := opts.Now.Sub(firstSeen)
	if idle < 0 {
		idle = 0
	}
	candidate.IdleFor = durationString(idle)
	candidate.EligibleAt = firstSeen.Add(opts.IdleFor).UTC().Format(time.RFC3339)
	if candidate.KeepReason == "" && idle >= opts.IdleFor {
		candidate.Ready = true
	}
}

func countReadyCandidates(candidates []cleanupCandidate) int {
	count := 0
	for _, candidate := range candidates {
		if candidate.Ready {
			count++
		}
	}
	return count
}

type pageCloseOptions struct {
	WaitGone      bool
	MaxAttempts   int
	AttemptWait   time.Duration
	PollInterval  time.Duration
	RetryBackoff  time.Duration
	FinalPageList bool
}

type pageCloseReport struct {
	Closed             bool               `json:"closed"`
	TargetGone         bool               `json:"target_gone"`
	AttemptCount       int                `json:"attempt_count"`
	MaxAttempts        int                `json:"max_attempts"`
	ElapsedMS          int64              `json:"elapsed_ms"`
	FinalPageCount     int                `json:"final_page_count,omitempty"`
	LastObservedTarget *cdp.TargetInfo    `json:"last_observed_target,omitempty"`
	LastError          string             `json:"last_error,omitempty"`
	TimedOut           bool               `json:"timed_out,omitempty"`
	RetryPolicy        string             `json:"retry_policy"`
	Attempts           []pageCloseAttempt `json:"attempts"`
}

type pageCloseAttempt struct {
	Attempt            int             `json:"attempt"`
	CloseSent          bool            `json:"close_sent"`
	Closed             bool            `json:"closed"`
	TargetGone         bool            `json:"target_gone"`
	ElapsedMS          int64           `json:"elapsed_ms"`
	PageCount          int             `json:"page_count,omitempty"`
	LastObservedTarget *cdp.TargetInfo `json:"last_observed_target,omitempty"`
	Error              string          `json:"error,omitempty"`
	Retryable          bool            `json:"retryable,omitempty"`
}

func closeReadyCleanupCandidates(ctx context.Context, client cdp.CommandClient, candidates []cleanupCandidate, opts pageCloseOptions, concurrency int) {
	if concurrency <= 0 {
		concurrency = defaultPageCleanupCloseConcurrency
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range candidates {
		if !candidates[i].Ready {
			continue
		}
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				candidates[i].CloseError = ctx.Err().Error()
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			report := closePageTargetSettled(ctx, client, candidates[i].Target, opts)
			candidates[i].Close = &report
			candidates[i].TargetGone = report.TargetGone
			if report.LastError != "" {
				candidates[i].CloseError = report.LastError
			}
		}()
	}
	wg.Wait()
}

func closePageTargetSettled(ctx context.Context, client cdp.CommandClient, target cdp.TargetInfo, opts pageCloseOptions) pageCloseReport {
	start := time.Now()
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 1
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPageClosePollInterval
	}
	if opts.AttemptWait <= 0 {
		opts.AttemptWait = pageCloseAttemptTimeout("")
	}
	report := pageCloseReport{
		MaxAttempts: opts.MaxAttempts,
		RetryPolicy: "target_gone",
		Attempts:    []pageCloseAttempt{},
	}
	for attempt := 1; attempt <= opts.MaxAttempts; attempt++ {
		attemptStart := time.Now()
		attemptReport := pageCloseAttempt{Attempt: attempt}
		err := cdp.CloseTargetWithClient(ctx, client, target.TargetID)
		if err == nil {
			attemptReport.CloseSent = true
			attemptReport.Closed = true
			report.Closed = true
		} else if targetGoneError(err) {
			attemptReport.Closed = true
			attemptReport.TargetGone = true
			attemptReport.Error = err.Error()
			report.Closed = true
			report.TargetGone = true
			report.LastError = ""
			attemptReport.ElapsedMS = time.Since(attemptStart).Milliseconds()
			report.Attempts = append(report.Attempts, attemptReport)
			report.AttemptCount = len(report.Attempts)
			report.ElapsedMS = time.Since(start).Milliseconds()
			return report
		} else {
			attemptReport.Error = err.Error()
			attemptReport.Retryable = true
			report.LastError = err.Error()
		}

		if opts.WaitGone {
			waitCtx, cancel := context.WithTimeout(ctx, opts.AttemptWait)
			gone, last, pageCount, waitErr := waitForTargetGone(waitCtx, client, target.TargetID, opts.PollInterval)
			cancel()
			attemptReport.TargetGone = gone
			attemptReport.PageCount = pageCount
			if last != nil {
				attemptReport.LastObservedTarget = last
				report.LastObservedTarget = last
			}
			if gone {
				report.TargetGone = true
				report.LastError = ""
			} else if waitErr != nil {
				attemptReport.Error = waitErr.Error()
				attemptReport.Retryable = retryablePageCloseError(waitErr)
				report.LastError = waitErr.Error()
				if errors.Is(waitErr, context.DeadlineExceeded) {
					report.TimedOut = true
				}
			}
		}
		attemptReport.ElapsedMS = time.Since(attemptStart).Milliseconds()
		report.Attempts = append(report.Attempts, attemptReport)
		report.AttemptCount = len(report.Attempts)
		report.ElapsedMS = time.Since(start).Milliseconds()
		if report.Closed && (!opts.WaitGone || report.TargetGone) {
			break
		}
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				report.TimedOut = true
			}
			break
		}
		if opts.RetryBackoff > 0 && attempt < opts.MaxAttempts {
			select {
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					report.TimedOut = true
				}
				return report
			case <-time.After(opts.RetryBackoff):
			}
		}
	}
	if opts.FinalPageList {
		if targets, err := cdp.ListTargetsWithClient(ctx, client); err == nil {
			report.FinalPageCount = pageTargetCount(targets)
		}
	}
	return report
}

func waitForTargetGone(ctx context.Context, client cdp.CommandClient, targetID string, poll time.Duration) (bool, *cdp.TargetInfo, int, error) {
	if poll <= 0 {
		poll = defaultPageClosePollInterval
	}
	for {
		targets, err := cdp.ListTargetsWithClient(ctx, client)
		if err != nil {
			return false, nil, 0, err
		}
		pageCount := pageTargetCount(targets)
		var last *cdp.TargetInfo
		for _, target := range targets {
			if target.TargetID == targetID {
				copy := target
				last = &copy
				break
			}
		}
		if last == nil {
			return true, nil, pageCount, nil
		}
		select {
		case <-ctx.Done():
			return false, last, pageCount, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func pageTargetCount(targets []cdp.TargetInfo) int {
	count := 0
	for _, target := range targets {
		if target.Type == "page" {
			count++
		}
	}
	return count
}

func pageCloseAttemptTimeout(browserMode string) time.Duration {
	if cleanupBrowserMode(browserMode) == "headless" {
		return 60 * time.Second
	}
	return 10 * time.Second
}

func pageCloseDefaultTimeout(browserMode string, maxAttempts int) time.Duration {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	return time.Duration(maxAttempts) * pageCloseAttemptTimeout(browserMode)
}

func targetGoneError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, needle := range []string{"target not found", "no target", "cannot find target", "target closed", "target id is not found"} {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

func retryablePageCloseError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, needle := range []string{"closed network connection", "failed to get reader", "i/o timeout", "context canceled", "target closed", "target not found"} {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return true
}

func errorsIsTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

func pageVisibility(ctx context.Context, client cdp.CommandClient, targetID string) (string, bool, bool) {
	session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, nil)
	if err != nil {
		return "unknown", false, false
	}
	defer session.Close(ctx)
	var result struct {
		VisibilityState string `json:"visibilityState"`
		Hidden          bool   `json:"hidden"`
		Prerendering    bool   `json:"prerendering"`
	}
	if err := evaluateJSONValue(ctx, session, `(() => ({visibilityState: document.visibilityState, hidden: document.hidden, prerendering: Boolean(document.prerendering)}))()`, "page cleanup visibility", &result); err != nil {
		return "unknown", false, false
	}
	return result.VisibilityState, result.Hidden, result.Prerendering
}

func pageCleanupStatePath(stateDir string) string {
	return filepath.Join(stateDir, "page-cleanup.json")
}

func pageCleanupKey(browserMode, connection, targetID string) string {
	return pageCleanupScopePrefix(browserMode, connection) + targetID
}

func pageCleanupScopePrefix(browserMode, connection string) string {
	return cleanupBrowserMode(browserMode) + "|" + connection + "|"
}

func cleanupBrowserMode(browserMode string) string {
	browserMode = strings.TrimSpace(browserMode)
	if browserMode == "" {
		return "headed"
	}
	return browserMode
}

func pruneLegacyHeadlessCleanupRecords(records map[string]pageCleanupRecord, browserMode, connection string) {
	if cleanupBrowserMode(browserMode) != "headless" || connection != "headless" {
		return
	}
	for key, record := range records {
		if cleanupBrowserMode(record.BrowserMode) == "headless" && record.Connection == "default" {
			delete(records, key)
		}
	}
}

func loadPageCleanupRecords(ctx context.Context, stateDir string) (map[string]pageCleanupRecord, []string, error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
	}
	path := pageCleanupStatePath(stateDir)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]pageCleanupRecord{}, nil, nil
		}
		return nil, nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]pageCleanupRecord{}, []string{"page cleanup state was empty and was recovered as empty state"}, nil
	}
	var file pageCleanupState
	if err := json.Unmarshal(b, &file); err != nil {
		return map[string]pageCleanupRecord{}, []string{"page cleanup state was invalid JSON and was recovered as empty state"}, nil
	}
	records := map[string]pageCleanupRecord{}
	for _, record := range file.Pages {
		record.BrowserMode = cleanupBrowserMode(record.BrowserMode)
		records[pageCleanupKey(record.BrowserMode, record.Connection, record.TargetID)] = record
	}
	return records, nil, nil
}

func savePageCleanupRecords(ctx context.Context, stateDir string, records map[string]pageCleanupRecord) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	pages := make([]pageCleanupRecord, 0, len(records))
	for _, record := range records {
		pages = append(pages, record)
	}
	sort.Slice(pages, func(i, j int) bool {
		if cleanupBrowserMode(pages[i].BrowserMode) == cleanupBrowserMode(pages[j].BrowserMode) {
			if pages[i].Connection == pages[j].Connection {
				return pages[i].TargetID < pages[j].TargetID
			}
			return pages[i].Connection < pages[j].Connection
		}
		return cleanupBrowserMode(pages[i].BrowserMode) < cleanupBrowserMode(pages[j].BrowserMode)
	})
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(pageCleanupState{Pages: pages}, "", "  ")
	if err != nil {
		return err
	}
	return writeLocalFileAtomic(pageCleanupStatePath(stateDir), append(b, '\n'), 0o600)
}

func writeLocalFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (a *app) selectedPageID(ctx context.Context) string {
	store, err := a.stateStore()
	if err != nil {
		return ""
	}
	file, err := store.Load(ctx)
	if err != nil {
		return ""
	}
	connection := a.connectionStateName(ctx)
	selection, ok := state.PageSelectionForMode(file, a.browserModeName(), connection)
	if !ok {
		return ""
	}
	return selection.TargetID
}

func (a *app) newPageTargetCommand(use, short, action string, run func(context.Context, cdp.CommandClient, string) error) *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var targetIndex int
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					a.connectionRemediationCommands(),
				)
			}
			defer closeClient(ctx)

			var target cdp.TargetInfo
			if targetIndex > 0 {
				target, err = a.resolvePageTargetWithClientIndex(ctx, client, targetID, urlContains, titleContains, targetIndex)
			} else {
				target, err = a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
			}
			if err != nil {
				return err
			}
			if err := run(ctx, client, target.TargetID); err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("%s target %s: %v", use, target.TargetID, err),
					ExitConnection,
					[]string{"cdp pages --json", "cdp doctor --json"},
				)
			}
			return a.render(ctx, fmt.Sprintf("%s\t%s", action, target.TargetID), map[string]any{
				"ok":     true,
				"action": action,
				"target": pageRow(target),
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the unique page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the unique page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based page target index")
	return cmd
}

type browserEventClient interface {
	cdp.CommandClient
	DrainEvents(context.Context) ([]cdp.Event, error)
	ReadEvent(context.Context) (cdp.Event, error)
}

func (a *app) browserCDPClient(ctx context.Context) (cdp.CommandClient, func(context.Context) error, error) {
	client, err := a.daemonRuntimeClient(ctx)
	if err != nil {
		return nil, nil, err
	}
	leased, _, err := client.BeginLease(ctx, daemon.DefaultInvocationLeaseTTL)
	if err == nil {
		return leased, leased.EndLease, nil
	}
	if daemon.IsInvocationLeaseUnsupported(err) {
		// Keep an older, still-running daemon usable while the scheduled
		// keepalive upgrades it. The unleased path deliberately does not claim
		// lifecycle attribution or target cleanup ownership.
		return client, func(context.Context) error { return nil }, nil
	}
	return nil, nil, err
}

func (a *app) browserEventCDPClient(ctx context.Context) (browserEventClient, func(context.Context) error, error) {
	client, err := a.daemonRuntimeClient(ctx)
	if err != nil {
		return nil, nil, err
	}
	leased, _, err := client.BeginLease(ctx, daemon.DefaultInvocationLeaseTTL)
	if err == nil {
		return leased, leased.EndLease, nil
	}
	if daemon.IsInvocationLeaseUnsupported(err) {
		return client, func(context.Context) error { return nil }, nil
	}
	return nil, nil, err
}

func (a *app) daemonRuntimeClient(ctx context.Context) (daemon.RuntimeClient, error) {
	runtime, err := a.requiredDaemonRuntime(ctx)
	if err != nil {
		return daemon.RuntimeClient{}, err
	}
	return daemon.RuntimeClient{Runtime: runtime}, nil
}

func (a *app) requiredDaemonRuntime(ctx context.Context) (daemon.Runtime, error) {
	if _, err := a.browserOptions(ctx); err != nil {
		return daemon.Runtime{}, err
	}
	store, err := a.stateStore()
	if err != nil {
		return daemon.Runtime{}, err
	}
	browserMode := a.browserModeName()
	runtime, err := a.loadRequiredDaemonRuntime(ctx, store.Dir, browserMode)
	if err == nil {
		return runtime, nil
	}
	if browserMode != string(config.BrowserModeHeadless) {
		return daemon.Runtime{}, err
	}
	if a.opts.noHeadlessRepair {
		return daemon.Runtime{}, err
	}
	if repairErr := a.repairHeadlessDaemonForBrowserCommand(ctx, store.Dir); repairErr != nil {
		return daemon.Runtime{}, fmt.Errorf("%v; automatic headless daemon repair failed: %v", err, repairErr)
	}
	runtime, retryErr := a.loadRequiredDaemonRuntime(ctx, store.Dir, browserMode)
	if retryErr != nil {
		return daemon.Runtime{}, fmt.Errorf("%v; automatic headless daemon repair completed but daemon runtime is still unavailable: %v", err, retryErr)
	}
	return runtime, nil
}

func (a *app) disableHeadlessRepair() func() {
	previous := a.opts.noHeadlessRepair
	a.opts.noHeadlessRepair = true
	return func() {
		a.opts.noHeadlessRepair = previous
	}
}

func (a *app) loadRequiredDaemonRuntime(ctx context.Context, storeDir, browserMode string) (daemon.Runtime, error) {
	statusCommand := modeScopedCommand(browserMode, "daemon status --json")
	doctorCommand := modeScopedCommand(browserMode, "doctor --check daemon --json")
	resolveCommand := modeScopedCommand(browserMode, "connection resolve --json")
	currentCommand := modeScopedCommand(browserMode, "connection current --json")
	repairCommand := modeScopedCommand(browserMode, "daemon keepalive --repair --json")
	runtime, ok, err := daemon.LoadRuntimeForMode(ctx, storeDir, browserMode)
	if err != nil {
		return daemon.Runtime{}, err
	}
	if !ok {
		if browserMode == string(config.BrowserModeHeadless) {
			return daemon.Runtime{}, fmt.Errorf("browser commands require a running %s cdp daemon; run `%s` or inspect `%s` before retrying", browserMode, repairCommand, statusCommand)
		}
		return daemon.Runtime{}, fmt.Errorf("browser commands require a running %s cdp daemon; inspect `%s` and `%s` before retrying", browserMode, statusCommand, doctorCommand)
	}
	if !a.runtimeMatchesConnection(runtime) {
		return daemon.Runtime{}, fmt.Errorf("running daemon does not match the effective %s browser-mode connection; inspect `%s`, `%s`, and `%s`, then run `%s` if the effective connection is correct and repair is appropriate for the current unattended context", browserMode, statusCommand, currentCommand, resolveCommand, repairCommand)
	}
	processCheck := daemon.CheckRuntimeProcess(ctx, runtime)
	if !processCheck.Running {
		if processCheck.State == daemon.RuntimeProcessStateIdentityMismatch || processCheck.State == daemon.RuntimeProcessStateIdentityUnavailable {
			reason := "does not match the recorded owner"
			if processCheck.State == daemon.RuntimeProcessStateIdentityUnavailable {
				reason = "could not be verified"
			}
			return daemon.Runtime{}, fmt.Errorf("%s daemon runtime process identity %s; inspect `%s` before retrying", browserMode, reason, statusCommand)
		}
		if browserMode == string(config.BrowserModeHeadless) {
			return daemon.Runtime{}, fmt.Errorf("%s daemon process is not running; run `%s` or inspect `%s`", browserMode, repairCommand, statusCommand)
		}
		return daemon.Runtime{}, fmt.Errorf("%s daemon runtime state exists but the process is not running; inspect `%s` or run `%s` when repair is appropriate for the current unattended context", browserMode, statusCommand, repairCommand)
	}
	if !daemon.RuntimeSocketReady(ctx, runtime) {
		if browserMode == string(config.BrowserModeHeadless) {
			return daemon.Runtime{}, fmt.Errorf("%s daemon socket is not ready; run `%s` or inspect `%s`", browserMode, repairCommand, statusCommand)
		}
		return daemon.Runtime{}, fmt.Errorf("%s daemon runtime socket is not ready; inspect `%s` or run `%s` when repair is appropriate for the current unattended context", browserMode, statusCommand, repairCommand)
	}
	return runtime, nil
}

func (a *app) repairHeadlessDaemonForBrowserCommand(ctx context.Context, storeDir string) error {
	connectionName := a.connectionStateName(ctx)
	if strings.TrimSpace(connectionName) == "" {
		connectionName = defaultConnectionNameForBrowserMode(string(config.BrowserModeHeadless))
	}
	mode := a.connectionMode()
	if strings.TrimSpace(mode) == "" {
		mode = "browser_url"
	}
	lockName := "daemon-keepalive-headless-" + mode + "-" + connectionName
	lock, acquired, existingLock, err := daemon.AcquireLock(ctx, storeDir, lockName, 5*time.Second, 10*time.Minute, daemon.LockMetadata{
		Name:  lockName,
		Phase: "browser_command_repair",
	})
	if err != nil {
		return fmt.Errorf("acquire headless keepalive lock: %w", err)
	}
	if !acquired {
		phase := strings.TrimSpace(existingLock.Phase)
		if phase == "" {
			phase = "unknown"
		}
		return fmt.Errorf("headless keepalive repair is locked by pid %d in phase %s", existingLock.PID, phase)
	}
	defer lock.Release()

	probe, err := a.browserProbe(ctx)
	if err != nil {
		return fmt.Errorf("probe selected headless connection: %w", err)
	}
	status := a.daemonStatus(ctx, probe)
	runtimeHealthy, runtimeCheck := keepaliveRuntimeCheck(ctx, status)
	if status.State == "running" && runtimeHealthy {
		return nil
	}
	probeResult := map[string]any{
		"mode":             "browser-command",
		"result":           probe.State,
		"repair_requested": true,
	}
	_, keepalive, err := a.runHeadlessKeepaliveStartOrRepair(ctx, storeDir, lock, connectionName, mode, 30*time.Second, defaultChromeCommand(), false, false, false, 10*time.Minute, status, probeResult, runtimeCheck)
	if err != nil {
		return err
	}
	state, _ := stringMapField(keepalive, "state")
	switch state {
	case "healthy", "started", "repaired":
		return nil
	case "locked":
		return fmt.Errorf("headless keepalive repair is locked")
	default:
		if state == "" {
			state = "unknown"
		}
		return fmt.Errorf("headless keepalive repair ended in state %s", state)
	}
}

func (a *app) attachPageSession(ctx context.Context, targetID, urlContains, titleContains string) (*cdp.PageSession, cdp.TargetInfo, error) {
	return a.attachPageSessionWithIndex(ctx, targetID, urlContains, titleContains, 0)
}

func (a *app) attachPageSessionWithIndex(ctx context.Context, targetID, urlContains, titleContains string, targetIndex int) (*cdp.PageSession, cdp.TargetInfo, error) {
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return nil, cdp.TargetInfo{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			a.connectionRemediationCommands(),
		)
	}
	if targetIndex > 0 {
		target, err := a.resolvePageTargetWithClientIndex(ctx, client, targetID, urlContains, titleContains, targetIndex)
		if err != nil {
			_ = closeClient(ctx)
			return nil, cdp.TargetInfo{}, err
		}
		session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
		if err != nil {
			_ = closeClient(ctx)
			return nil, target, commandError(
				"connection_failed",
				"connection",
				fmt.Sprintf("attach target %s: %v", target.TargetID, err),
				ExitConnection,
				[]string{"cdp pages --json", "cdp doctor --json"},
			)
		}
		return session, target, nil
	}
	if strings.TrimSpace(targetID) != "" && strings.TrimSpace(urlContains) == "" && strings.TrimSpace(titleContains) == "" {
		session, target, handled, err := a.attachExactPageSession(ctx, client, closeClient, targetID)
		if handled {
			return session, target, err
		}
	}
	target, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
	if err != nil {
		_ = closeClient(ctx)
		return nil, cdp.TargetInfo{}, err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
	if err != nil {
		_ = closeClient(ctx)
		return nil, target, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("attach target %s: %v", target.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	return session, target, nil
}

func (a *app) attachExactPageSession(ctx context.Context, client cdp.CommandClient, closeClient func(context.Context) error, targetID string) (*cdp.PageSession, cdp.TargetInfo, bool, error) {
	targetID = strings.TrimSpace(targetID)
	target, err := cdp.TargetInfoWithClient(ctx, client, targetID)
	if err != nil {
		return nil, cdp.TargetInfo{}, false, nil
	}
	if target.Type != "page" {
		_ = closeClient(ctx)
		return nil, cdp.TargetInfo{}, true, targetNotFound(fmt.Sprintf("target %q is %q, not page", targetID, target.Type))
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
	if err != nil {
		_ = closeClient(ctx)
		return nil, target, true, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("attach target %s: %v", target.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	return session, target, true, nil
}

func (a *app) attachPageEventSession(ctx context.Context, targetID, urlContains, titleContains string) (browserEventClient, *cdp.PageSession, cdp.TargetInfo, error) {
	return a.attachPageEventSessionWithIndex(ctx, targetID, urlContains, titleContains, 0)
}

func (a *app) attachPageEventSessionWithIndex(ctx context.Context, targetID, urlContains, titleContains string, targetIndex int) (browserEventClient, *cdp.PageSession, cdp.TargetInfo, error) {
	client, closeClient, err := a.browserEventCDPClient(ctx)
	if err != nil {
		return nil, nil, cdp.TargetInfo{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			a.connectionRemediationCommands(),
		)
	}
	if targetIndex > 0 {
		target, err := a.resolvePageTargetWithClientIndex(ctx, client, targetID, urlContains, titleContains, targetIndex)
		if err != nil {
			_ = closeClient(ctx)
			return nil, nil, cdp.TargetInfo{}, err
		}
		session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
		if err != nil {
			_ = closeClient(ctx)
			return nil, nil, cdp.TargetInfo{}, commandError(
				"connection_failed",
				"connection",
				fmt.Sprintf("attach target %s: %v", target.TargetID, err),
				ExitConnection,
				[]string{"cdp pages --json", "cdp doctor --json"},
			)
		}
		return client, session, target, nil
	}
	if strings.TrimSpace(targetID) != "" && strings.TrimSpace(urlContains) == "" && strings.TrimSpace(titleContains) == "" {
		session, target, handled, err := a.attachExactPageSession(ctx, client, closeClient, targetID)
		if handled {
			return client, session, target, err
		}
	}
	target, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
	if err != nil {
		_ = closeClient(ctx)
		return nil, nil, cdp.TargetInfo{}, err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
	if err != nil {
		_ = closeClient(ctx)
		return nil, nil, cdp.TargetInfo{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("attach target %s: %v", target.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	return client, session, target, nil
}

func (a *app) resolvePageTarget(ctx context.Context, targetID, urlContains string) (cdp.TargetInfo, error) {
	targets, err := a.listTargets(ctx)
	if err != nil {
		return cdp.TargetInfo{}, err
	}
	return resolvePageTarget(targets, targetID, urlContains, "")
}

func (a *app) resolvePageTargetWithClient(ctx context.Context, client cdp.CommandClient, targetID, urlContains, titleContains string) (cdp.TargetInfo, error) {
	if strings.TrimSpace(targetID) == "" && strings.TrimSpace(urlContains) == "" && strings.TrimSpace(titleContains) == "" {
		if target, ok := a.selectedPageTarget(ctx, client); ok {
			return target, nil
		}
	}
	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return cdp.TargetInfo{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("list targets: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp daemon status --json"},
		)
	}
	return resolvePageTarget(targets, targetID, urlContains, titleContains)
}

func (a *app) resolvePageTargetWithClientIndex(ctx context.Context, client cdp.CommandClient, targetID, urlContains, titleContains string, targetIndex int) (cdp.TargetInfo, error) {
	if targetIndex <= 0 {
		return cdp.TargetInfo{}, commandError("invalid_target_index", "usage", "--target-index must be greater than zero", ExitUsage, []string{"cdp pages --json"})
	}
	if strings.TrimSpace(targetID) != "" || strings.TrimSpace(urlContains) != "" || strings.TrimSpace(titleContains) != "" {
		return cdp.TargetInfo{}, commandError("invalid_target_selector", "usage", "--target-index cannot be combined with --target, --url-contains, or --title-contains", ExitUsage, []string{"cdp pages --json"})
	}
	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return cdp.TargetInfo{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("list targets: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp daemon status --json"},
		)
	}
	return resolvePageTargetByIndex(targets, targetIndex)
}

func validatePageTargetIndexSelector(cmd *cobra.Command, targetID, urlContains, titleContains string, targetIndex int) error {
	if targetIndex < 0 || (cmd.Flags().Changed("target-index") && targetIndex == 0) {
		return commandError("invalid_target_index", "usage", "--target-index must be greater than zero", ExitUsage, []string{"cdp pages --json"})
	}
	if targetIndex > 0 && (strings.TrimSpace(targetID) != "" || strings.TrimSpace(urlContains) != "" || strings.TrimSpace(titleContains) != "") {
		return commandError("invalid_target_selector", "usage", "--target-index cannot be combined with --target, --url-contains, or --title-contains", ExitUsage, []string{"cdp pages --json"})
	}
	return validatePageTargetSelectorValues(targetID, urlContains, titleContains)
}

func validatePageTargetSelectorValues(targetID, urlContains, titleContains string) error {
	selectors := 0
	for _, selector := range []string{targetID, urlContains, titleContains} {
		if strings.TrimSpace(selector) != "" {
			selectors++
		}
	}
	if selectors > 1 {
		return commandError("invalid_target_selector", "usage", "pass only one of --target, --url-contains, or --title-contains", ExitUsage, []string{"cdp pages --json"})
	}
	return nil
}

func (a *app) selectedPageTarget(ctx context.Context, client cdp.CommandClient) (cdp.TargetInfo, bool) {
	store, err := a.stateStore()
	if err != nil {
		return cdp.TargetInfo{}, false
	}
	file, err := store.Load(ctx)
	if err != nil {
		return cdp.TargetInfo{}, false
	}
	selection, ok := state.PageSelectionForMode(file, a.browserModeName(), a.connectionStateName(ctx))
	if !ok || strings.TrimSpace(selection.TargetID) == "" {
		return cdp.TargetInfo{}, false
	}
	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return cdp.TargetInfo{}, false
	}
	for _, target := range targets {
		if target.TargetID == selection.TargetID && target.Type == "page" {
			return target, true
		}
	}
	return cdp.TargetInfo{}, false
}

func (a *app) createPageTarget(ctx context.Context, client cdp.CommandClient, rawURL string) (string, error) {
	return a.createPageTargetWithOwnership(ctx, client, rawURL, targetOwnershipMetadata{CreatedBy: "cdp"})
}

func (a *app) createWorkflowPageTarget(ctx context.Context, client cdp.CommandClient, rawURL, workflow string) (string, error) {
	return a.createPageTargetWithOwnership(ctx, client, rawURL, targetOwnershipMetadata{CreatedBy: "cdp", Workflow: workflow})
}

func (a *app) createWorkflowPageTargetWithKeepOpen(ctx context.Context, client cdp.CommandClient, rawURL, workflow string, keepOpen bool) (string, error) {
	targetID, err := a.createWorkflowPageTarget(ctx, client, rawURL, workflow)
	if err != nil || !keepOpen {
		return targetID, err
	}
	if err := cdp.MarkTargetPersistent(ctx, client, targetID); err != nil {
		return targetID, a.keepOpenPromotionFailure(client, targetID, rawURL, workflow, err)
	}
	return targetID, nil
}

func (a *app) keepOpenPromotionFailure(client cdp.CommandClient, targetID, rawURL, workflow string, policyErr error) error {
	closeCtx, cancel := context.WithTimeout(context.Background(), pageCloseDefaultTimeout(a.browserModeName(), defaultPageCloseMaxAttempts))
	closeReport := closePageTargetSettled(closeCtx, client, cdp.TargetInfo{
		TargetID: targetID,
		Type:     "page",
		URL:      rawURL,
	}, pageCloseOptions{
		WaitGone:     true,
		MaxAttempts:  defaultPageCloseMaxAttempts,
		AttemptWait:  pageCloseAttemptTimeout(a.browserModeName()),
		PollInterval: defaultPageClosePollInterval,
		RetryBackoff: defaultPageCloseRetryBackoff,
	})
	cancel()

	recoveryCommand := "cdp page cleanup --target " + targetID + " --force --close --json"
	data := map[string]any{
		"target_id":        targetID,
		"policy_error":     policyErr.Error(),
		"primary_error":    commandErrorSummary(policyErr),
		"close":            closeReport,
		"recovery_command": recoveryCommand,
	}
	message := fmt.Sprintf("keep %s target %s open: %v", workflow, targetID, policyErr)
	if !closeReport.TargetGone {
		cleanupError := closeReport.LastError
		if cleanupError == "" {
			cleanupError = fmt.Sprintf("target %s close did not settle", targetID)
		}
		data["cleanup_error"] = cleanupError
		message += "; cleanup incomplete: " + cleanupError
	} else {
		message += "; created target was closed after policy failure"
	}
	return commandErrorWithData(
		"lease_target_policy_failed",
		"lifecycle",
		message,
		ExitConnection,
		uniqueCommands([]string{recoveryCommand, "cdp daemon status --json", "cdp pages --json"}),
		data,
	)
}

func (a *app) createPageTargetWithOwnership(ctx context.Context, client cdp.CommandClient, rawURL string, ownership targetOwnershipMetadata) (string, error) {
	if _, err := a.enforceBrowserBudgetForNewPage(ctx, client); err != nil {
		return "", err
	}
	targetID, err := cdp.CreateTargetWithClient(ctx, client, rawURL)
	if err != nil {
		return "", commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("open page: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp pages --json"},
		)
	}
	if strings.TrimSpace(ownership.CreatedBy) != "" || ownership.hasRunOrTask() {
		if err := a.recordPageTargetOwnership(ctx, targetID, rawURL, ownership); err != nil {
			closeCtx, cancel := context.WithTimeout(ctx, pageCloseAttemptTimeout(a.browserModeName()))
			closeReport := closePageTargetSettled(closeCtx, client, cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}, pageCloseOptions{
				WaitGone:     true,
				MaxAttempts:  defaultPageCloseMaxAttempts,
				AttemptWait:  pageCloseAttemptTimeout(a.browserModeName()),
				PollInterval: defaultPageClosePollInterval,
				RetryBackoff: defaultPageCloseRetryBackoff,
			})
			cancel()
			recoveredTarget := pageRow(cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL})
			return targetID, commandErrorWithData(
				"page_record_failed",
				"internal",
				fmt.Sprintf("record workflow-created page %s: %v", targetID, err),
				ExitInternal,
				[]string{"cdp page cleanup --json", "cdp daemon status --json"},
				map[string]any{
					"created":          true,
					"target_id":        targetID,
					"recovered_target": recoveredTarget,
					"record_error":     err.Error(),
					"close":            closeReport,
					"ownership":        ownership.summary(targetID),
				},
			)
		}
	}
	markDisposable := strings.TrimSpace(ownership.Workflow) != ""
	var policyErr error
	if markDisposable {
		policyErr = cdp.MarkTargetDisposable(ctx, client, targetID)
	} else {
		policyErr = cdp.MarkTargetPersistent(ctx, client, targetID)
	}
	if policyErr != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), pageCloseDefaultTimeout(a.browserModeName(), defaultPageCloseMaxAttempts))
		closeReport := closePageTargetSettled(closeCtx, client, cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}, pageCloseOptions{
			WaitGone:     true,
			MaxAttempts:  defaultPageCloseMaxAttempts,
			AttemptWait:  pageCloseAttemptTimeout(a.browserModeName()),
			PollInterval: defaultPageClosePollInterval,
			RetryBackoff: defaultPageCloseRetryBackoff,
		})
		cancel()
		return targetID, commandErrorWithData(
			"lease_target_policy_failed",
			"lifecycle",
			fmt.Sprintf("mark page %s %s: %v", targetID, map[bool]string{true: "disposable", false: "persistent"}[markDisposable], policyErr),
			ExitConnection,
			[]string{"cdp daemon status --json", "cdp page cleanup --target " + targetID + " --close --json"},
			map[string]any{"target_id": targetID, "close": closeReport},
		)
	}
	return targetID, nil
}

func (a *app) recordPageTargetOwnership(ctx context.Context, targetID, rawURL string, ownership targetOwnershipMetadata) error {
	store, err := a.stateStore()
	if err != nil {
		return err
	}
	pageCleanupRecordsMu.Lock()
	defer pageCleanupRecordsMu.Unlock()
	records, _, err := loadPageCleanupRecords(ctx, store.Dir)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	browserMode := a.browserModeName()
	connection := a.connectionStateName(ctx)
	key := pageCleanupKey(browserMode, connection, targetID)
	records[key] = pageCleanupRecord{
		BrowserMode:  cleanupBrowserMode(browserMode),
		Connection:   connection,
		TargetID:     targetID,
		URL:          rawURL,
		CreatedBy:    ownership.CreatedBy,
		Workflow:     ownership.Workflow,
		RunID:        ownership.RunID,
		TaskID:       ownership.TaskID,
		RootTaskID:   ownership.RootTaskID,
		ParentTaskID: ownership.ParentTaskID,
		FirstSeen:    now,
		LastSeen:     now,
	}
	return savePageCleanupRecords(ctx, store.Dir, records)
}

func resolvePageTarget(targets []cdp.TargetInfo, targetID, urlContains, titleContains string) (cdp.TargetInfo, error) {
	targetID = strings.TrimSpace(targetID)
	urlContains = strings.TrimSpace(urlContains)
	titleContains = strings.TrimSpace(titleContains)
	if err := validatePageTargetSelectorValues(targetID, urlContains, titleContains); err != nil {
		return cdp.TargetInfo{}, err
	}
	pages := orderedPageTargets(targets)
	if targetID != "" {
		var matches []cdp.TargetInfo
		for _, page := range pages {
			if targetIDMatchesPrefix(page.TargetID, targetID) {
				matches = append(matches, page)
			}
		}
		return onePageTarget(matches, pages, fmt.Sprintf("target %q", targetID))
	}
	if urlContains != "" {
		matches := make([]cdp.TargetInfo, 0)
		for _, page := range pages {
			if strings.Contains(strings.ToLower(page.URL), strings.ToLower(urlContains)) {
				matches = append(matches, page)
			}
		}
		if len(matches) == 0 {
			return cdp.TargetInfo{}, pageTargetNotFound(fmt.Sprintf("no page URL contains %q", urlContains), pages)
		}
		return onePageTarget(matches, pages, fmt.Sprintf("page URL containing %q", urlContains))
	}
	if titleContains != "" {
		matches := make([]cdp.TargetInfo, 0)
		for _, page := range pages {
			if strings.Contains(strings.ToLower(page.Title), strings.ToLower(titleContains)) {
				matches = append(matches, page)
			}
		}
		if len(matches) == 0 {
			return cdp.TargetInfo{}, pageTargetNotFound(fmt.Sprintf("no page title contains %q", titleContains), pages)
		}
		return onePageTarget(matches, pages, fmt.Sprintf("page title containing %q", titleContains))
	}
	return onePageTarget(pages, pages, "default page")
}

func resolvePageTargetByIndex(targets []cdp.TargetInfo, targetIndex int) (cdp.TargetInfo, error) {
	pages := orderedPageTargets(targets)
	if targetIndex <= 0 {
		return cdp.TargetInfo{}, commandError("invalid_target_index", "usage", "--target-index must be greater than zero", ExitUsage, []string{"cdp pages --json"})
	}
	if targetIndex > len(pages) {
		return cdp.TargetInfo{}, commandErrorWithData(
			"target_not_found",
			"usage",
			fmt.Sprintf("page target index %d is out of range; found %d page targets", targetIndex, len(pages)),
			ExitUsage,
			[]string{"cdp pages --json"},
			availablePageTargetEvidence(pages),
		)
	}
	return pages[targetIndex-1], nil
}

func onePageTarget(matches, available []cdp.TargetInfo, label string) (cdp.TargetInfo, error) {
	switch len(matches) {
	case 0:
		return cdp.TargetInfo{}, pageTargetNotFound(fmt.Sprintf("no %s matched", label), available)
	case 1:
		return matches[0], nil
	default:
		return cdp.TargetInfo{}, commandErrorWithData(
			"ambiguous_target",
			"usage",
			fmt.Sprintf("%s matched %d pages; pass a longer --target", label, len(matches)),
			ExitUsage,
			[]string{"cdp pages --json", "cdp snapshot --target <target-id> --json"},
			ambiguousPageTargetEvidence(matches, available),
		)
	}
}

func pageTargetNotFound(message string, available []cdp.TargetInfo) error {
	return commandErrorWithData(
		"target_not_found",
		"usage",
		message,
		ExitUsage,
		[]string{"cdp pages --json", "cdp open <url> --json"},
		availablePageTargetEvidence(available),
	)
}

func targetNotFound(message string) error {
	return commandError(
		"target_not_found",
		"usage",
		message,
		ExitUsage,
		[]string{"cdp pages --json", "cdp open <url> --json"},
	)
}

type pageSnapshot struct {
	URL      string         `json:"url"`
	Title    string         `json:"title"`
	Selector string         `json:"selector"`
	Count    int            `json:"count"`
	Items    []snapshotItem `json:"items"`
	Error    *snapshotError `json:"error,omitempty"`
}

type extractionDiagnostics struct {
	SelectorMatched        bool     `json:"selector_matched"`
	SelectorMatchCount     int      `json:"selector_match_count"`
	SelectedVisibleCount   int      `json:"selected_visible_count"`
	SelectedTextLength     int      `json:"selected_text_length"`
	SelectedHTMLLength     int      `json:"selected_html_length"`
	BodyTextLength         int      `json:"body_text_length"`
	BodyInnerTextLength    int      `json:"body_inner_text_length"`
	BodyTextContentLength  int      `json:"body_text_content_length"`
	DocumentReadyState     string   `json:"document_ready_state"`
	FrameCount             int      `json:"frame_count"`
	IFrameElementCount     int      `json:"iframe_element_count"`
	ShadowRootCount        int      `json:"shadow_root_count"`
	VisibleTextCandidates  int      `json:"visible_text_candidates"`
	PossibleCauses         []string `json:"possible_causes"`
	SuggestedCommands      []string `json:"suggested_commands"`
	RuntimeDiagnosticError string   `json:"runtime_diagnostic_error,omitempty"`
	FrameTreeError         string   `json:"frame_tree_error,omitempty"`
}

type snapshotItem struct {
	Index      int          `json:"index"`
	Tag        string       `json:"tag"`
	Role       string       `json:"role,omitempty"`
	AriaLabel  string       `json:"aria_label,omitempty"`
	Text       string       `json:"text"`
	TextLength int          `json:"text_length"`
	Href       string       `json:"href,omitempty"`
	Rect       snapshotRect `json:"rect"`
}

type snapshotRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type snapshotError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func collectPageSnapshot(ctx context.Context, session *cdp.PageSession, selector string, limit, minChars int) (pageSnapshot, error) {
	if limit < 0 {
		return pageSnapshot{}, commandError(
			"usage",
			"usage",
			"--limit must be non-negative",
			ExitUsage,
			[]string{"cdp snapshot --limit 20 --json"},
		)
	}
	if minChars < 0 {
		return pageSnapshot{}, commandError(
			"usage",
			"usage",
			"--min-chars must be non-negative",
			ExitUsage,
			[]string{"cdp snapshot --min-chars 1 --json"},
		)
	}
	result, err := session.Evaluate(ctx, snapshotExpression(selector, limit, minChars), true)
	if err != nil {
		return pageSnapshot{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("snapshot target %s: %v", session.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	if result.Exception != nil {
		return pageSnapshot{}, commandError(
			"javascript_exception",
			"runtime",
			fmt.Sprintf("snapshot javascript exception: %s", result.Exception.Text),
			ExitCheckFailed,
			[]string{"cdp snapshot --selector body --json", "cdp pages --json"},
		)
	}
	var snapshot pageSnapshot
	if err := json.Unmarshal(result.Object.Value, &snapshot); err != nil {
		return pageSnapshot{}, commandError(
			"invalid_snapshot_result",
			"internal",
			fmt.Sprintf("decode snapshot result: %v", err),
			ExitInternal,
			[]string{"cdp doctor --json", "cdp eval 'document.title' --json"},
		)
	}
	if snapshot.Error != nil {
		return pageSnapshot{}, commandError(
			"invalid_selector",
			"usage",
			fmt.Sprintf("invalid selector %q: %s", selector, snapshot.Error.Message),
			ExitUsage,
			[]string{"cdp snapshot --selector body --json", "cdp snapshot --selector article --json"},
		)
	}
	return snapshot, nil
}

func collectExtractionDiagnostics(ctx context.Context, session *cdp.PageSession, selector string) extractionDiagnostics {
	diagnostics := extractionDiagnostics{}
	result, err := session.Evaluate(ctx, extractionDiagnosticsExpression(selector), true)
	if err != nil {
		diagnostics.RuntimeDiagnosticError = err.Error()
	} else if result.Exception != nil {
		diagnostics.RuntimeDiagnosticError = result.Exception.Text
	} else if err := json.Unmarshal(result.Object.Value, &diagnostics); err != nil {
		diagnostics.RuntimeDiagnosticError = err.Error()
	}

	var frames frameTreeResponse
	if err := execSessionJSON(ctx, session, "Page.getFrameTree", map[string]any{}, &frames); err != nil {
		diagnostics.FrameTreeError = err.Error()
	} else {
		diagnostics.FrameCount = len(collectFrameSummaries(frames.FrameTree, ""))
	}

	if diagnostics.FrameCount == 0 && diagnostics.IFrameElementCount > 0 {
		diagnostics.FrameCount = diagnostics.IFrameElementCount + 1
	}
	diagnostics.PossibleCauses = emptyExtractionPossibleCauses(diagnostics)
	diagnostics.SuggestedCommands = emptyExtractionSuggestedCommands(session.TargetID)
	return diagnostics
}

func emptyExtractionPossibleCauses(diagnostics extractionDiagnostics) []string {
	causes := make([]string, 0, 6)
	if !diagnostics.SelectorMatched {
		causes = append(causes, "selector_matched_zero")
	}
	if diagnostics.SelectorMatched && diagnostics.SelectedVisibleCount == 0 {
		causes = append(causes, "selector_not_visible")
	}
	if diagnostics.DocumentReadyState != "" && diagnostics.DocumentReadyState != "complete" {
		causes = append(causes, "page_not_ready")
	}
	if diagnostics.FrameCount > 1 || diagnostics.IFrameElementCount > 0 {
		causes = append(causes, "iframe_content")
	}
	if diagnostics.ShadowRootCount > 0 {
		causes = append(causes, "shadow_dom")
	}
	if diagnostics.SelectorMatched && diagnostics.SelectedTextLength == 0 && diagnostics.SelectedHTMLLength > 0 {
		causes = append(causes, "non_text_dom")
	}
	if diagnostics.VisibleTextCandidates == 0 {
		causes = append(causes, "no_visible_text_candidates")
	}
	if diagnostics.SelectorMatched && diagnostics.BodyTextLength < 20 && diagnostics.SelectedHTMLLength > 0 {
		causes = append(causes, "bot_or_consent_page")
	}
	if len(causes) == 0 {
		causes = append(causes, "filtered_by_visibility_or_min_chars")
	}
	return causes
}

func emptyExtractionSuggestedCommands(targetID string) []string {
	target := "<target-id>"
	if strings.TrimSpace(targetID) != "" {
		target = targetID
	}
	return []string{
		fmt.Sprintf("cdp frames --target %s --json", target),
		fmt.Sprintf("cdp snapshot --target %s --selector main --diagnose-empty --json", target),
		fmt.Sprintf("cdp html body --target %s --diagnose-empty --json", target),
		fmt.Sprintf("cdp dom query body --target %s --json", target),
	}
}

func extractionDiagnosticsExpression(selector string) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_empty_diagnostics__";
  const selector = %s;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const textLength = (value) => normalize(value).length;
  const isVisible = (element) => {
    if (!element || !element.getBoundingClientRect) return false;
    const style = window.getComputedStyle(element);
    if (style.visibility === "hidden" || style.display === "none") return false;
    const rect = element.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  let elements = [];
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return { marker, selector_matched: false, selector_match_count: 0, selected_visible_count: 0, selected_text_length: 0, selected_html_length: 0, body_text_length: 0, body_inner_text_length: 0, body_text_content_length: 0, document_ready_state: document.readyState || "", frame_count: 0, iframe_element_count: 0, shadow_root_count: 0, visible_text_candidates: 0, runtime_diagnostic_error: error.name + ": " + error.message };
  }
  const body = document.body;
  const bodyInnerText = body ? String(body.innerText || "") : "";
  const bodyTextContent = body ? String(body.textContent || "") : "";
  let selectedVisibleCount = 0;
  let selectedTextLength = 0;
  let selectedHTMLLength = 0;
  for (const element of elements) {
    if (isVisible(element)) selectedVisibleCount++;
    selectedTextLength += textLength(element.innerText || element.textContent);
    selectedHTMLLength += String(element.outerHTML || "").length;
  }
  let shadowRootCount = 0;
  let visibleTextCandidates = 0;
  const visitRoot = (root, depth) => {
    if (!root || depth > 4) return;
    const all = Array.from(root.querySelectorAll ? root.querySelectorAll("*") : []);
    for (const element of all) {
      if (element.shadowRoot) {
        shadowRootCount++;
        visitRoot(element.shadowRoot, depth + 1);
      }
      if (visibleTextCandidates < 1000 && isVisible(element) && textLength(element.innerText || element.textContent) > 0) {
        visibleTextCandidates++;
      }
    }
  };
  visitRoot(document, 0);
  return {
    marker,
    selector_matched: elements.length > 0,
    selector_match_count: elements.length,
    selected_visible_count: selectedVisibleCount,
    selected_text_length: selectedTextLength,
    selected_html_length: selectedHTMLLength,
    body_text_length: textLength(bodyInnerText || bodyTextContent),
    body_inner_text_length: textLength(bodyInnerText),
    body_text_content_length: textLength(bodyTextContent),
    document_ready_state: document.readyState || "",
    frame_count: 0,
    iframe_element_count: document.querySelectorAll("iframe,frame").length,
    shadow_root_count: shadowRootCount,
    visible_text_candidates: visibleTextCandidates
  };
})()`, string(selectorJSON))
}

func snapshotExpression(selector string, limit, minChars int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => {
  const selector = %s;
  const limit = %d;
  const minChars = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const isVisible = (element) => {
    const style = window.getComputedStyle(element);
    if (style.visibility === "hidden" || style.display === "none") return false;
    const rect = element.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    return {
      url: location.href,
      title: document.title,
      selector,
      count: 0,
      items: [],
      error: { name: error.name, message: error.message }
    };
  }
  const items = [];
  for (let index = 0; index < elements.length; index++) {
    const element = elements[index];
    if (!isVisible(element)) continue;
    const text = normalize(element.innerText || element.textContent);
    if (text.length < minChars) continue;
    const rect = element.getBoundingClientRect();
    items.push({
      index,
      tag: element.tagName.toLowerCase(),
      role: element.getAttribute("role") || "",
      aria_label: element.getAttribute("aria-label") || "",
      text,
      text_length: text.length,
      href: element.href || "",
      rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height }
    });
    if (limit > 0 && items.length >= limit) break;
  }
  return { url: location.href, title: document.title, selector, count: items.length, items };
})()`, string(selectorJSON), limit, minChars)
}

func snapshotTextLines(items []snapshotItem) []string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		text := item.Text
		if len([]rune(text)) > 240 {
			text = string([]rune(text)[:240]) + "..."
		}
		lines = append(lines, fmt.Sprintf("%d\t%s", item.Index, text))
	}
	return lines
}
