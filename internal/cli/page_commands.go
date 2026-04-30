package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"encoding/json"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/state"
	"github.com/spf13/cobra"
	"path/filepath"
)

func (a *app) newTargetsCommand() *cobra.Command {
	var limit int
	var targetType string
	cmd := &cobra.Command{
		Use:   "targets",
		Short: "List browser targets",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			targets, err := a.listTargets(ctx)
			if err != nil {
				return err
			}
			targets = filterTargetsByType(targets, targetType)
			rows := targetRows(targets)
			rows = limitRows(rows, limit)
			var lines []string
			for _, target := range rows {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s", target["id"], target["type"], target["title"]))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{"ok": true, "targets": rows})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of targets to return; use 0 for no limit")
	cmd.Flags().StringVar(&targetType, "type", "", "only return targets of this CDP type, such as page or service_worker")
	return cmd
}

func (a *app) newPagesCommand() *cobra.Command {
	var limit int
	var urlContains string
	var titleContains string
	var includeURL string
	var excludeURL string
	cmd := &cobra.Command{
		Use:   "pages",
		Short: "List open pages and tabs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			targets, err := a.listTargets(ctx)
			if err != nil {
				return err
			}
			pages := pageRows(targets)
			pages = filterRowsContains(pages, "url", firstNonEmpty(urlContains, includeURL))
			pages = filterRowsContains(pages, "title", titleContains)
			pages = filterRowsExcludes(pages, "url", excludeURL)
			pages = limitRows(pages, limit)
			var lines []string
			for _, page := range pages {
				lines = append(lines, fmt.Sprintf("%s\t%s", page["id"], page["title"]))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{"ok": true, "pages": pages})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of pages to return; use 0 for no limit")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "only return pages whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "only return pages whose title contains this text")
	cmd.Flags().StringVar(&includeURL, "include-url", "", "only return pages whose URL contains this text")
	cmd.Flags().StringVar(&excludeURL, "exclude-url", "", "exclude pages whose URL contains this text")
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
			[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
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
			"id":       target.TargetID,
			"type":     target.Type,
			"title":    target.Title,
			"url":      target.URL,
			"attached": target.Attached,
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
	pages := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		if target.Type != "page" {
			continue
		}
		pages = append(pages, pageRow(target))
	}
	return pages
}

func pageRow(target cdp.TargetInfo) map[string]any {
	return map[string]any{
		"id":       target.TargetID,
		"type":     target.Type,
		"title":    target.Title,
		"url":      target.URL,
		"attached": target.Attached,
	}
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
	cmd := &cobra.Command{
		Use:   "select [target-id]",
		Short: "Select the default page target for subsequent commands",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetID := ""
			if len(args) == 1 {
				targetID = args[0]
			}
			if strings.TrimSpace(targetID) == "" && strings.TrimSpace(urlContains) == "" && strings.TrimSpace(titleContains) == "" {
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
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			defer closeClient(ctx)

			target, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			selection := state.PageSelection{
				Connection: a.connectionStateName(ctx),
				TargetID:   target.TargetID,
				URL:        target.URL,
				Title:      target.Title,
				SelectedAt: time.Now().UTC().Format(time.RFC3339),
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
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "select the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "select the first page whose title contains this text")
	return cmd
}

func (a *app) newPageReloadCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var ignoreCache bool
	cmd := &cobra.Command{
		Use:   "reload",
		Short: "Reload a page target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
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
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().BoolVar(&ignoreCache, "ignore-cache", false, "reload while bypassing cache")
	return cmd
}

func (a *app) newPageHistoryCommand(name, short string, offset int) *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
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
			targetIndex := history.CurrentIndex + offset
			if targetIndex < 0 || targetIndex >= len(history.Entries) {
				return commandError(
					"navigation_unavailable",
					"usage",
					fmt.Sprintf("page has no %s history entry", name),
					ExitUsage,
					[]string{"cdp page reload --json", "cdp open <url> --new-tab=false --target <target-id> --json"},
				)
			}
			entry := history.Entries[targetIndex]
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
					"target_index":  targetIndex,
					"entry_id":      entry.ID,
					"entry":         entry,
				},
			})
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

func (a *app) newPageActivateCommand() *cobra.Command {
	return a.newPageTargetCommand("activate", "Bring a page target to the foreground", "activated", cdp.ActivateTargetWithClient)
}

func (a *app) newPageCloseCommand() *cobra.Command {
	return a.newPageTargetCommand("close", "Close a page target", "closed", cdp.CloseTargetWithClient)
}

type cleanupCandidate struct {
	Target          cdp.TargetInfo `json:"target"`
	VisibilityState string         `json:"visibility_state,omitempty"`
	Hidden          bool           `json:"hidden,omitempty"`
	Prerendering    bool           `json:"prerendering,omitempty"`
	FirstSeen       string         `json:"first_seen,omitempty"`
	LastSeen        string         `json:"last_seen,omitempty"`
	IdleFor         string         `json:"idle_for,omitempty"`
	EligibleAt      string         `json:"eligible_at,omitempty"`
	Ready           bool           `json:"ready"`
	KeepReason      string         `json:"keep_reason,omitempty"`
	CloseError      string         `json:"close_error,omitempty"`
}

type pageCleanupRecord struct {
	Connection string `json:"connection"`
	TargetID   string `json:"target_id"`
	URL        string `json:"url,omitempty"`
	Title      string `json:"title,omitempty"`
	FirstSeen  string `json:"first_seen"`
	LastSeen   string `json:"last_seen"`
}

type pageCleanupState struct {
	Pages []pageCleanupRecord `json:"pages"`
}

func (a *app) newPageCleanupCommand() *cobra.Command {
	var closePages bool
	var includeAttached bool
	var includeURL string
	var excludeURL string
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
--idle-for across cleanup runs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if max < 0 {
				return commandError("usage", "usage", "--max must be non-negative", ExitUsage, []string{"cdp page cleanup --max 10 --json"})
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
					[]string{"cdp daemon keepalive --auto-connect", "cdp connection current --json"},
				)
			}
			defer closeClient(ctx)

			targets, err := cdp.ListTargetsWithClient(ctx, client)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("list targets: %v", err),
					ExitConnection,
					[]string{"cdp doctor --json", "cdp daemon status --json"},
				)
			}
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			connectionName := a.connectionStateName(ctx)
			selectedID := a.selectedPageID(ctx)
			records, err := loadPageCleanupRecords(ctx, store.Dir)
			if err != nil {
				return commandError("internal", "internal", fmt.Sprintf("read page cleanup state: %v", err), ExitInternal, []string{"cdp page cleanup --json"})
			}
			now := time.Now().UTC()
			candidates := cleanupCandidates(ctx, client, targets, cleanupOptions{
				Connection:      connectionName,
				SelectedID:      selectedID,
				IncludeAttached: includeAttached,
				IncludeURL:      includeURL,
				ExcludeURL:      excludeURL,
				IdleFor:         idleFor,
				Max:             max,
				Now:             now,
				Records:         records,
			})
			closed := []cleanupCandidate{}
			if closePages {
				for i := range candidates {
					if !candidates[i].Ready {
						continue
					}
					if err := cdp.CloseTargetWithClient(ctx, client, candidates[i].Target.TargetID); err != nil {
						candidates[i].CloseError = err.Error()
						continue
					}
					delete(records, pageCleanupKey(connectionName, candidates[i].Target.TargetID))
					closed = append(closed, candidates[i])
				}
			}

			if err := savePageCleanupRecords(ctx, store.Dir, records); err != nil {
				return commandError("internal", "internal", fmt.Sprintf("write page cleanup state: %v", err), ExitInternal, []string{"cdp page cleanup --json"})
			}

			lines := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				status := "candidate"
				if candidate.KeepReason != "" {
					status = "kept:" + candidate.KeepReason
				} else if candidate.CloseError != "" {
					status = "error"
				} else if closePages {
					status = "closed"
				}
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s", candidate.Target.TargetID, status, candidate.Target.Title))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok": true,
				"cleanup": map[string]any{
					"dry_run":          !closePages,
					"close":            closePages,
					"candidate_count":  countClosableCandidates(candidates),
					"closed_count":     len(closed),
					"idle_for":         idleFor.String(),
					"state_path":       pageCleanupStatePath(store.Dir),
					"include_attached": includeAttached,
					"include_url":      strings.TrimSpace(includeURL),
					"exclude_url":      strings.TrimSpace(excludeURL),
					"selected_page":    selectedID,
					"next_commands": []string{
						"cdp page cleanup --json",
						"cdp page cleanup --close --max 10 --json",
						"crontab -l | grep cdp",
					},
				},
				"candidates": candidates,
				"closed":     closed,
			})
		},
	}
	cmd.Flags().BoolVar(&closePages, "close", false, "close matching inactive page targets; default is dry-run")
	cmd.Flags().BoolVar(&includeAttached, "include-attached", false, "also consider attached page targets")
	cmd.Flags().StringVar(&includeURL, "include-url", "", "only consider pages whose URL contains this text")
	cmd.Flags().StringVar(&excludeURL, "exclude-url", "", "exclude pages whose URL contains this text")
	cmd.Flags().DurationVar(&idleFor, "idle-for", 30*time.Minute, "minimum duration a page must remain inactive before --close can close it")
	cmd.Flags().IntVar(&max, "max", 10, "maximum ready candidate pages to close or report; use 0 for no limit")
	return cmd
}

type cleanupOptions struct {
	Connection      string
	SelectedID      string
	IncludeAttached bool
	IncludeURL      string
	ExcludeURL      string
	IdleFor         time.Duration
	Max             int
	Now             time.Time
	Records         map[string]pageCleanupRecord
}

func cleanupCandidates(ctx context.Context, client cdp.CommandClient, targets []cdp.TargetInfo, opts cleanupOptions) []cleanupCandidate {
	candidates := []cleanupCandidate{}
	includeURL := strings.ToLower(strings.TrimSpace(opts.IncludeURL))
	excludeURL := strings.ToLower(strings.TrimSpace(opts.ExcludeURL))
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
		key := pageCleanupKey(opts.Connection, target.TargetID)
		seen[key] = true
		candidate := cleanupCandidate{Target: target}
		switch {
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
		candidates = append(candidates, candidate)
		if opts.Max > 0 && countReadyCandidates(candidates) >= opts.Max {
			break
		}
	}
	for key := range opts.Records {
		if strings.HasPrefix(key, opts.Connection+"|") && !seen[key] {
			delete(opts.Records, key)
		}
	}
	return candidates
}

func updateCleanupRecord(candidate *cleanupCandidate, opts cleanupOptions, key string) {
	record, ok := opts.Records[key]
	if !ok || candidate.KeepReason != "" {
		record = pageCleanupRecord{
			Connection: opts.Connection,
			TargetID:   candidate.Target.TargetID,
			URL:        candidate.Target.URL,
			Title:      candidate.Target.Title,
			FirstSeen:  opts.Now.Format(time.RFC3339),
		}
	}
	record.LastSeen = opts.Now.Format(time.RFC3339)
	record.URL = candidate.Target.URL
	record.Title = candidate.Target.Title
	opts.Records[key] = record
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

func countClosableCandidates(candidates []cleanupCandidate) int {
	count := 0
	for _, candidate := range candidates {
		if candidate.Ready {
			count++
		}
	}
	return count
}

func pageCleanupStatePath(stateDir string) string {
	return filepath.Join(stateDir, "page-cleanup.json")
}

func pageCleanupKey(connection, targetID string) string {
	return connection + "|" + targetID
}

func loadPageCleanupRecords(ctx context.Context, stateDir string) (map[string]pageCleanupRecord, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	path := pageCleanupStatePath(stateDir)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]pageCleanupRecord{}, nil
		}
		return nil, err
	}
	var file pageCleanupState
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, err
	}
	records := map[string]pageCleanupRecord{}
	for _, record := range file.Pages {
		records[pageCleanupKey(record.Connection, record.TargetID)] = record
	}
	return records, nil
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
		if pages[i].Connection == pages[j].Connection {
			return pages[i].TargetID < pages[j].TargetID
		}
		return pages[i].Connection < pages[j].Connection
	})
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(pageCleanupState{Pages: pages}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pageCleanupStatePath(stateDir), append(b, '\n'), 0o600)
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
	selection, ok := state.PageSelectionForConnection(file, connection)
	if !ok {
		return ""
	}
	return selection.TargetID
}

func (a *app) newPageTargetCommand(use, short, action string, run func(context.Context, cdp.CommandClient, string) error) *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
				)
			}
			defer closeClient(ctx)

			target, err := a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
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
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	return cmd
}

type browserEventClient interface {
	cdp.CommandClient
	DrainEvents(context.Context) ([]cdp.Event, error)
	ReadEvent(context.Context) (cdp.Event, error)
}

func (a *app) browserCDPClient(ctx context.Context) (cdp.CommandClient, func(context.Context) error, error) {
	runtime, err := a.requiredDaemonRuntime(ctx)
	if err != nil {
		return nil, nil, err
	}
	return daemon.RuntimeClient{Runtime: runtime}, func(context.Context) error { return nil }, nil
}

func (a *app) browserEventCDPClient(ctx context.Context) (browserEventClient, func(context.Context) error, error) {
	runtime, err := a.requiredDaemonRuntime(ctx)
	if err != nil {
		return nil, nil, err
	}
	return daemon.RuntimeClient{Runtime: runtime}, func(context.Context) error { return nil }, nil
}

func (a *app) requiredDaemonRuntime(ctx context.Context) (daemon.Runtime, error) {
	if _, err := a.browserOptions(ctx); err != nil {
		return daemon.Runtime{}, err
	}
	store, err := a.stateStore()
	if err != nil {
		return daemon.Runtime{}, err
	}
	runtime, ok, err := daemon.LoadRuntime(ctx, store.Dir)
	if err != nil {
		return daemon.Runtime{}, err
	}
	if !ok {
		return daemon.Runtime{}, fmt.Errorf("browser commands require a running cdp daemon; run `cdp daemon start --auto-connect --json` or `cdp daemon start --browser-url <browser-url> --json`")
	}
	if !a.runtimeMatchesConnection(runtime) {
		return daemon.Runtime{}, fmt.Errorf("running daemon does not match the selected browser connection; run `cdp daemon status --json` or restart it with `cdp daemon stop --json` then `cdp daemon start --json`")
	}
	if !daemon.RuntimeRunning(runtime) {
		return daemon.Runtime{}, fmt.Errorf("daemon runtime state exists but the process is not running; run `cdp daemon start --json`")
	}
	if !daemon.RuntimeSocketReady(ctx, runtime) {
		return daemon.Runtime{}, fmt.Errorf("daemon runtime socket is not ready; run `cdp daemon status --json` or restart it with `cdp daemon stop --json` then `cdp daemon start --json`")
	}
	return runtime, nil
}

func (a *app) attachPageSession(ctx context.Context, targetID, urlContains, titleContains string) (*cdp.PageSession, cdp.TargetInfo, error) {
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return nil, cdp.TargetInfo{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
		)
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
		return nil, cdp.TargetInfo{}, commandError(
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
		return nil, cdp.TargetInfo{}, true, commandError(
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
	client, closeClient, err := a.browserEventCDPClient(ctx)
	if err != nil {
		return nil, nil, cdp.TargetInfo{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			[]string{"cdp daemon start --auto-connect --json", "cdp connection current --json"},
		)
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

func (a *app) selectedPageTarget(ctx context.Context, client cdp.CommandClient) (cdp.TargetInfo, bool) {
	store, err := a.stateStore()
	if err != nil {
		return cdp.TargetInfo{}, false
	}
	file, err := store.Load(ctx)
	if err != nil {
		return cdp.TargetInfo{}, false
	}
	selection, ok := state.PageSelectionForConnection(file, a.connectionStateName(ctx))
	if !ok || strings.TrimSpace(selection.TargetID) == "" {
		return cdp.TargetInfo{}, false
	}
	target, err := cdp.TargetInfoWithClient(ctx, client, selection.TargetID)
	if err != nil || target.Type != "page" {
		return cdp.TargetInfo{}, false
	}
	return target, true
}

func (a *app) createPageTarget(ctx context.Context, client cdp.CommandClient, rawURL string) (string, error) {
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
	return targetID, nil
}

func resolvePageTarget(targets []cdp.TargetInfo, targetID, urlContains, titleContains string) (cdp.TargetInfo, error) {
	targetID = strings.TrimSpace(targetID)
	urlContains = strings.TrimSpace(urlContains)
	titleContains = strings.TrimSpace(titleContains)
	var pages []cdp.TargetInfo
	for _, target := range targets {
		if target.Type == "page" {
			pages = append(pages, target)
		}
	}
	if targetID != "" {
		var matches []cdp.TargetInfo
		for _, page := range pages {
			if page.TargetID == targetID || strings.HasPrefix(page.TargetID, targetID) {
				matches = append(matches, page)
			}
		}
		return onePageTarget(matches, fmt.Sprintf("target %q", targetID))
	}
	if urlContains != "" {
		for _, page := range pages {
			if strings.Contains(strings.ToLower(page.URL), strings.ToLower(urlContains)) {
				return page, nil
			}
		}
		return cdp.TargetInfo{}, targetNotFound(fmt.Sprintf("no page URL contains %q", urlContains))
	}
	if titleContains != "" {
		for _, page := range pages {
			if strings.Contains(strings.ToLower(page.Title), strings.ToLower(titleContains)) {
				return page, nil
			}
		}
		return cdp.TargetInfo{}, targetNotFound(fmt.Sprintf("no page title contains %q", titleContains))
	}
	return onePageTarget(pages, "default page")
}

func onePageTarget(matches []cdp.TargetInfo, label string) (cdp.TargetInfo, error) {
	switch len(matches) {
	case 0:
		return cdp.TargetInfo{}, targetNotFound(fmt.Sprintf("no %s matched", label))
	case 1:
		return matches[0], nil
	default:
		return cdp.TargetInfo{}, commandError(
			"ambiguous_target",
			"usage",
			fmt.Sprintf("%s matched %d pages; pass a longer --target", label, len(matches)),
			ExitUsage,
			[]string{"cdp pages --json", "cdp snapshot --target <target-id> --json"},
		)
	}
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
