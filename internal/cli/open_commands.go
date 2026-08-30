package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newOpenCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var targetIndex int
	var newTab bool
	var reuse bool
	var budgetSummary bool
	var retryOpts commandRetryOptions
	var ownershipFlags targetOwnershipMetadata
	cmd := &cobra.Command{
		Use:   "open <url>",
		Short: "Open a URL in a new tab or navigate a selected page",
		Long:  "Open a URL in a new tab by default, or navigate one existing page with mutually exclusive target, URL, title, or page-index selection. Use --new-tab=false for strict existing-target navigation; --reuse may create a new tab only when its non-index filter finds no match.",
		Example: "  cdp open https://example.com --new-tab=false --target-index 2 --json\n" +
			"  cdp open https://example.com --reuse --target-index 2 --budget-summary --json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			rawURL := strings.TrimSpace(args[0])
			if targetIndex > 0 && newTab && !reuse {
				return commandError("invalid_target_selector", "usage", "--target-index requires --new-tab=false unless --reuse is enabled", ExitUsage, []string{"cdp open https://example.com --new-tab=false --target-index 2 --json", "cdp open https://example.com --reuse --target-index 2 --json"})
			}
			if reuse && !newTab {
				return commandError("usage", "usage", "--reuse cannot be combined with --new-tab=false; use --new-tab=false for strict existing-target navigation", ExitUsage, []string{"cdp open https://example.com --reuse --url-contains example.com --json", "cdp open https://example.com --new-tab=false --target <target-id> --json"})
			}
			if reuse && strings.TrimSpace(targetID) == "" && strings.TrimSpace(urlContains) == "" && strings.TrimSpace(titleContains) == "" && targetIndex == 0 {
				return commandError("usage", "usage", "--reuse requires --target, --url-contains, or --title-contains", ExitUsage, []string{"cdp open https://example.com --reuse --url-contains example.com --json"})
			}
			ownership, err := normalizeTargetOwnership(ownershipFlags, "cdp")
			if err != nil {
				return err
			}
			recordNavigatedOwnership := ownership.hasRunOrTask() || cmd.Flags().Changed("created-by")
			result, retryReport, err := runCommandWithRetry(ctx, retryOpts, func(attemptCtx context.Context) (commandRetryResult, error) {
				client, closeClient, err := a.browserCDPClient(attemptCtx)
				if err != nil {
					return commandRetryResult{}, commandError(
						"connection_not_configured",
						"connection",
						err.Error(),
						ExitConnection,
						a.connectionRemediationCommands(),
					)
				}
				defer closeClient(attemptCtx)

				var budgetBefore *cdp.BrowserResourceBudget
				if budgetSummary {
					budget, err := a.openBudgetSnapshot(attemptCtx, client)
					if err != nil {
						return commandRetryResult{}, err
					}
					budgetBefore = &budget
				}

				pageAction := "created"
				frameID := ""
				target := cdp.TargetInfo{Type: "page", URL: rawURL}
				created := true
				reused := false
				reuseReport := openReuseReport(reuse, targetID, urlContains, titleContains, targetIndex)
				if reuse {
					selected, err := resolveOpenTarget(attemptCtx, a, client, targetID, urlContains, titleContains, targetIndex)
					if err != nil && !(targetIndex == 0 && strings.TrimSpace(targetID) == "" && commandErrorHasCode(err, "target_not_found")) {
						return commandRetryResult{}, err
					}
					if err == nil {
						frameID, err = navigateExistingPageTarget(attemptCtx, client, selected, rawURL)
						if err != nil {
							return commandRetryResult{Target: &selected}, err
						}
						target = selected
						target.URL = rawURL
						pageAction = "reused"
						created = false
						reused = true
						reuseReport["matched"] = true
						reuseReport["target_id"] = selected.TargetID
						if recordNavigatedOwnership {
							if err := a.recordPageTargetOwnership(attemptCtx, target.TargetID, rawURL, ownership); err != nil {
								return commandRetryResult{Target: &target}, commandError(
									"page_record_failed",
									"internal",
									fmt.Sprintf("record task-owned page %s: %v", target.TargetID, err),
									ExitInternal,
									[]string{"cdp page cleanup --json", "cdp daemon status --json"},
								)
							}
						}
					} else {
						reuseReport["matched"] = false
						reuseReport["fallback_created"] = true
					}
				}
				if !reused {
					if reuse || newTab || (targetID == "" && urlContains == "" && titleContains == "" && targetIndex == 0) {
						createdID, err := a.createPageTargetWithOwnership(attemptCtx, client, rawURL, ownership)
						if err != nil {
							if createdID != "" {
								target.TargetID = createdID
								return commandRetryResult{Target: &target}, err
							}
							return commandRetryResult{}, err
						}
						target.TargetID = createdID
					} else {
						selected, err := resolveOpenTarget(attemptCtx, a, client, targetID, urlContains, titleContains, targetIndex)
						if err != nil {
							return commandRetryResult{}, err
						}
						frameID, err = navigateExistingPageTarget(attemptCtx, client, selected, rawURL)
						if err != nil {
							return commandRetryResult{Target: &selected}, err
						}
						target = selected
						target.URL = rawURL
						pageAction = "navigated"
						created = false
						reused = true
						if recordNavigatedOwnership {
							if err := a.recordPageTargetOwnership(attemptCtx, target.TargetID, rawURL, ownership); err != nil {
								return commandRetryResult{Target: &target}, commandError(
									"page_record_failed",
									"internal",
									fmt.Sprintf("record task-owned page %s: %v", target.TargetID, err),
									ExitInternal,
									[]string{"cdp page cleanup --json", "cdp daemon status --json"},
								)
							}
						}
					}
				}

				var budgetAfter *cdp.BrowserResourceBudget
				if budgetSummary {
					budget, err := a.openBudgetSnapshot(attemptCtx, client)
					if err != nil {
						return commandRetryResult{Target: &target}, err
					}
					budgetAfter = &budget
				}

				page := pageRow(target)
				page["action"] = pageAction
				page["frame_id"] = frameID
				data := map[string]any{
					"ok":      true,
					"action":  pageAction,
					"created": created,
					"reused":  reused,
					"page":    page,
				}
				if targetIndex > 0 {
					data["target_index"] = targetIndex
				}
				if reuse {
					data["reuse"] = reuseReport
				}
				if budgetSummary {
					data["tab_budget"] = openTabBudgetSummary(openTabBudgetSummaryOptions{
						Policy:        openPolicyName(reuse, newTab, targetID, urlContains, titleContains, targetIndex),
						ReuseTarget:   openReuseTargetLabel(targetID, urlContains, titleContains, targetIndex),
						TargetIndex:   targetIndex,
						TargetID:      target.TargetID,
						Created:       created,
						Reused:        reused,
						CleanupStatus: openCleanupStatus(created, reused),
						Before:        budgetBefore,
						After:         budgetAfter,
						Ownership:     ownership,
					})
				}
				ownership.addTo(data, target.TargetID)
				return commandRetryResult{
					Human:  fmt.Sprintf("%s\t%s\t%s", pageAction, target.TargetID, rawURL),
					Target: &target,
					Data:   data,
				}, nil
			})
			if err != nil {
				return err
			}
			attachCommandRetryReport(result.Data, retryReport)
			return a.render(ctx, result.Human, result.Data)
		},
	}
	cmd.Flags().BoolVar(&newTab, "new-tab", true, "open a new tab instead of navigating an existing page")
	cmd.Flags().BoolVar(&reuse, "reuse", false, "reuse a matching page target and navigate it; with URL/title filters, create a new tab when no match exists")
	cmd.Flags().BoolVar(&budgetSummary, "budget-summary", false, "include before/after tab budget and managed-tab cleanup status")
	cmd.Flags().StringVar(&targetID, "target", "", "navigate a page target by exact id or unique prefix when --new-tab=false")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "navigate the first page whose URL contains this text when --new-tab=false")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "navigate the first page whose title contains this text when --new-tab=false")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "navigate the 1-based existing page target index; workers do not consume indexes")
	addTargetOwnershipFlags(cmd, &ownershipFlags, true)
	addCommandRetryFlags(cmd, &retryOpts)
	return cmd
}

func resolveOpenTarget(ctx context.Context, a *app, client cdp.CommandClient, targetID, urlContains, titleContains string, targetIndex int) (cdp.TargetInfo, error) {
	if targetIndex > 0 {
		return a.resolvePageTargetWithClientIndex(ctx, client, targetID, urlContains, titleContains, targetIndex)
	}
	return a.resolvePageTargetWithClient(ctx, client, targetID, urlContains, titleContains)
}

func navigateExistingPageTarget(ctx context.Context, client cdp.CommandClient, target cdp.TargetInfo, rawURL string) (string, error) {
	session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, func(context.Context) error { return nil })
	if err != nil {
		return "", commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("attach target %s: %v", target.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	defer session.Close(ctx)
	frameID, err := session.Navigate(ctx, rawURL)
	if err != nil {
		return "", commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("navigate target %s: %v", target.TargetID, err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp doctor --json"},
		)
	}
	return frameID, nil
}

func (a *app) openBudgetSnapshot(ctx context.Context, client cdp.CommandClient) (cdp.BrowserResourceBudget, error) {
	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return cdp.BrowserResourceBudget{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("list targets for tab budget: %v", err),
			ExitConnection,
			[]string{"cdp pages --json", "cdp daemon status --json"},
		)
	}
	return cdp.BrowserBudgetForTargets(ctx, client, targets, a.browserResourceBudgetOptions()), nil
}

func commandErrorHasCode(err error, code string) bool {
	got, _ := commandErrorCodeClass(err)
	return got == code
}

func openReuseReport(reuse bool, targetID, urlContains, titleContains string, targetIndex int) map[string]any {
	report := map[string]any{
		"requested":        reuse,
		"policy":           openPolicyName(reuse, true, targetID, urlContains, titleContains, targetIndex),
		"target":           strings.TrimSpace(targetID),
		"url_contains":     strings.TrimSpace(urlContains),
		"title_contains":   strings.TrimSpace(titleContains),
		"matched":          false,
		"fallback_created": false,
	}
	if targetIndex > 0 {
		report["target_index"] = targetIndex
	}
	return report
}

type openTabBudgetSummaryOptions struct {
	Policy        string
	ReuseTarget   string
	TargetIndex   int
	TargetID      string
	Created       bool
	Reused        bool
	CleanupStatus string
	Before        *cdp.BrowserResourceBudget
	After         *cdp.BrowserResourceBudget
	Ownership     targetOwnershipMetadata
}

func openTabBudgetSummary(opts openTabBudgetSummaryOptions) map[string]any {
	summary := map[string]any{
		"policy":              opts.Policy,
		"reuse_target":        opts.ReuseTarget,
		"managed_tab_id":      opts.TargetID,
		"managed_tab_created": opts.Created,
		"created":             opts.Created,
		"reused":              opts.Reused,
		"cleanup_status":      opts.CleanupStatus,
		"cleanup_commands":    openCleanupCommands(opts.TargetID, opts.Ownership),
	}
	if opts.TargetIndex > 0 {
		summary["target_index"] = opts.TargetIndex
	}
	if opts.Before != nil {
		summary["before"] = *opts.Before
		summary["max_tabs"] = opts.Before.MaxTabs
	}
	if opts.After != nil {
		summary["after"] = *opts.After
		summary["max_tabs"] = opts.After.MaxTabs
	}
	return summary
}

func openPolicyName(reuse, newTab bool, targetID, urlContains, titleContains string, targetIndex int) string {
	if reuse {
		switch {
		case targetIndex > 0:
			return "reuse_target_index"
		case strings.TrimSpace(targetID) != "":
			return "reuse_target"
		case strings.TrimSpace(urlContains) != "":
			return "reuse_url_contains"
		case strings.TrimSpace(titleContains) != "":
			return "reuse_title_contains"
		default:
			return "reuse"
		}
	}
	if targetIndex > 0 {
		return "strict_target_index"
	}
	if !newTab || strings.TrimSpace(targetID) != "" || strings.TrimSpace(urlContains) != "" || strings.TrimSpace(titleContains) != "" {
		return "strict_existing"
	}
	return "new"
}

func openReuseTargetLabel(targetID, urlContains, titleContains string, targetIndex int) string {
	switch {
	case targetIndex > 0:
		return fmt.Sprintf("index:%d", targetIndex)
	case strings.TrimSpace(targetID) != "":
		return strings.TrimSpace(targetID)
	case strings.TrimSpace(urlContains) != "":
		return "url:" + strings.TrimSpace(urlContains)
	case strings.TrimSpace(titleContains) != "":
		return "title:" + strings.TrimSpace(titleContains)
	default:
		return ""
	}
}

func openCleanupStatus(created, reused bool) string {
	if reused && !created {
		return "skipped_reused_tab"
	}
	return "not_run"
}

func openCleanupCommands(targetID string, ownership targetOwnershipMetadata) []string {
	commands := []string{}
	if strings.TrimSpace(targetID) != "" {
		commands = append(commands, fmt.Sprintf("cdp page cleanup --target %s --force --json", targetID))
	}
	if strings.TrimSpace(ownership.TaskID) != "" {
		commands = append(commands, fmt.Sprintf("cdp page cleanup --task-id %s --close --force --json", ownership.TaskID))
	}
	if strings.TrimSpace(ownership.RootTaskID) != "" && ownership.RootTaskID != ownership.TaskID {
		commands = append(commands, fmt.Sprintf("cdp page cleanup --root-task-id %s --close --force --json", ownership.RootTaskID))
	}
	return commands
}
