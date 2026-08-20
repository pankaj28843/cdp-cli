package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/sourcecollect/x"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowXCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "x", Short: "Collect X source-native records", Long: "Collect a post thread or profile posts from a validated X URL. Plain output is detailed Markdown; use --json or --jq for structured processing."}
	cmd.AddCommand(a.newWorkflowXCollectionCommand("collect <x-url>", "Collect normalized records from a discovered X URL"))
	return cmd
}

func (a *app) newWorkflowXCollectionCommand(use, short string) *cobra.Command {
	var limit int
	var wait time.Duration
	var keepOpen bool
	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    "Collect a validated X post thread or profile. Plain stdout is a detailed Markdown export suitable for redirection; --json preserves normalized records and --jq selects from that JSON.",
		Example: "  cdp workflow x collect 'https://x.com/karpathy/status/2079610838143623371' > post.md\n  cdp workflow x collect 'https://x.com/karpathy/status/2079610838143623371' --jq '.records[] | {handle, body}'\n  cdp schema workflow-x-collect --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			request, err := x.Parse(args[0])
			if err != nil {
				return commandError("invalid_x_url", "usage", err.Error(), ExitUsage, []string{"cdp workflow x collect https://x.com/karpathy/status/2079610838143623371 --json"})
			}
			if limit < 1 || limit > 500 {
				return commandError("invalid_limit", "usage", "--limit must be between 1 and 500", ExitUsage, nil)
			}
			ctx, cancel := a.commandContextWithDefault(cmd, max(wait+15*time.Second, 30*time.Second))
			defer cancel()
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
			}
			targetID, err := a.createWorkflowPageTargetWithKeepOpen(ctx, client, request.URL, "x-collect", keepOpen)
			if err != nil {
				_ = closeClient(ctx)
				return err
			}
			closed := false
			closePage := func() string {
				if keepOpen || closed {
					return ""
				}
				closed = true
				if err := cdp.CloseTargetWithClient(ctx, client, targetID); err != nil {
					return err.Error()
				}
				return ""
			}
			defer closePage()
			session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
			if err != nil {
				return commandError("connection_failed", "connection", fmt.Sprintf("attach X target: %v", err), ExitConnection, nil)
			}
			defer session.Close(ctx)
			if _, err := session.Navigate(ctx, request.URL); err != nil {
				return commandError("x_navigation_failed", "connection", err.Error(), ExitConnection, nil)
			}
			readiness, err := waitForRenderedExtractReadiness(ctx, session, `article[data-testid="tweet"]`, wait, 0, "useful-content", 1, 1)
			if err != nil {
				return err
			}
			final, err := x.ValidateFinalURL(request, readiness.URL)
			if err != nil {
				return commandError("x_identity_changed", "check_failed", fmt.Sprintf("%s (final URL: %s)", err, readiness.URL), ExitCheckFailed, nil)
			}
			if final.HandleChanged {
				closeError := closePage()
				coverage := dynamicSourceCoverage(nil, xPossibleRecordKinds(final.Kind), "invalid", "profile_handle_changed_requires_stable_account_verification", "profile_identity", "", true)
				workflow := map[string]any{"name": "x-collect", "count": 0, "limit": limit, "status": "invalid", "partial_reason": "profile_handle_changed_requires_stable_account_verification", "interactions": 0, "created_page": true, "closed": closeError == "", "close_error": closeError}
				return a.render(ctx, xCollectionMarkdown(request.URL, final.Kind, nil, workflow, coverage), map[string]any{"ok": true, "request": request, "kind": final.Kind, "records": []x.Record{}, "coverage": coverage, "workflow": workflow})
			}
			status, interactions := "partial", 0
			var records []x.Record
			if final.Kind == x.KindPostThread {
				initial, initialErr := collectVisibleXRecords(ctx, session, final, limit)
				if initialErr != nil {
					return initialErr
				}
				expansion, err := expandRenderedExtractDiscussion(ctx, session, renderedExtractContentPlan{Profile: renderedExtractContentProfileX})
				if err != nil {
					return err
				}
				status, interactions = expansion.Status, expansion.Interactions
				replies, replyErr := collectVisibleXThreadReplies(ctx, session, final, limit)
				if replyErr != nil {
					return replyErr
				}
				records = mergeXRecords(initial, replies, limit)
			} else {
				records, err = collectVisibleXRecords(ctx, session, final, limit)
				if err != nil {
					return err
				}
			}
			profileStalled := false
			if final.Kind == x.KindProfilePosts && len(records) < limit && wait > 0 {
				seen := make(map[string]x.Record, len(records))
				order := make([]string, 0, limit)
				for _, record := range records {
					if _, exists := seen[record.ID]; !exists {
						seen[record.ID] = record
						order = append(order, record.ID)
					}
				}
				stalled := 0
				for round := 0; round < 100 && len(seen) < limit && stalled < 3; round++ {
					if _, scrollErr := session.Evaluate(ctx, "window.scrollTo(0, document.body.scrollHeight)", true); scrollErr != nil {
						break
					}
					select {
					case <-ctx.Done():
						return commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, nil)
					case <-time.After(500 * time.Millisecond):
					}
					next, nextErr := collectVisibleXRecords(ctx, session, final, limit)
					if nextErr != nil {
						return nextErr
					}
					added := 0
					for _, record := range next {
						if _, exists := seen[record.ID]; !exists {
							seen[record.ID] = record
							order = append(order, record.ID)
							added++
						}
					}
					if added == 0 {
						stalled++
					} else {
						stalled = 0
					}
				}
				records = make([]x.Record, 0, min(limit, len(order)))
				for _, id := range order[:min(limit, len(order))] {
					records = append(records, seen[id])
				}
				profileStalled = len(records) < limit
			}
			partialReason := "currently_visible_only"
			if profileStalled {
				partialReason = "pagination_stalled"
			}
			if final.Kind == x.KindPostThread && status == "exhausted" {
				partialReason = ""
			}
			if len(records) >= limit {
				if limit >= 500 {
					status, partialReason = "ceiling", "hard_limit"
				} else {
					status, partialReason = "partial", "requested_limit"
				}
			} else if final.Kind == x.KindPostThread && status != "exhausted" && status != "ceiling" {
				partialReason = "discussion_" + status
			}
			closeError := closePage()
			observed, missing := xRecordKinds(records, final.Kind)
			continuation := "profile_scroll"
			if final.Kind == x.KindPostThread {
				continuation = "discussion_" + status
			}
			coverage := dynamicSourceCoverage(observed, missing, status, partialReason, continuation, "", status != "exhausted" && status != "ceiling")
			workflow := map[string]any{"name": "x-collect", "count": len(records), "limit": limit, "status": status, "partial_reason": partialReason, "interactions": interactions, "discussion_interactions": interactions, "created_page": true, "closed": closeError == "", "close_error": closeError}
			return a.render(ctx, xCollectionMarkdown(final.URL, final.Kind, records, workflow, coverage), map[string]any{"ok": true, "request": final, "kind": final.Kind, "records": records, "coverage": coverage, "workflow": workflow})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum source-native records to collect (1-500)")
	cmd.Flags().DurationVar(&wait, "wait", 10*time.Second, "how long to wait for visible X source records")
	cmd.Flags().BoolVar(&keepOpen, "keep-open", false, "leave the workflow-created page open for debugging")
	return cmd
}

func collectVisibleXThreadReplies(ctx context.Context, session *cdp.PageSession, request x.Request, limit int) ([]x.Record, error) {
	result, err := session.Evaluate(ctx, x.ThreadExpression(request, limit), true)
	if err != nil || result.Exception != nil {
		return nil, commandError("x_collection_failed", "runtime", "X collection expression failed", ExitCheckFailed, nil)
	}
	page, fullErr := x.DecodeThreadPage(request, result.Object.Value)
	if fullErr == nil {
		return xThreadReplies(page.Records), nil
	}
	page, err = x.DecodeThreadReplies(request, result.Object.Value)
	if err != nil {
		return nil, commandError("invalid_x_collection", "check_failed", err.Error(), ExitCheckFailed, nil)
	}
	return page.Records, nil
}

func xThreadReplies(records []x.Record) []x.Record {
	replies := make([]x.Record, 0, len(records))
	for _, record := range records {
		if record.Kind == x.RecordReply {
			replies = append(replies, record)
		}
	}
	return replies
}

func mergeXRecords(first, later []x.Record, limit int) []x.Record {
	seen := make(map[string]struct{}, len(first)+len(later))
	merged := make([]x.Record, 0, min(limit, len(first)+len(later)))
	for _, records := range [][]x.Record{first, later} {
		for _, record := range records {
			if len(merged) >= limit {
				return merged
			}
			if _, exists := seen[record.ID]; exists {
				continue
			}
			seen[record.ID] = struct{}{}
			merged = append(merged, record)
		}
	}
	return merged
}

func collectVisibleXRecords(ctx context.Context, session *cdp.PageSession, request x.Request, limit int) ([]x.Record, error) {
	var expression string
	var decode func(x.Request, json.RawMessage) (x.RecordPage, error)
	switch request.Kind {
	case x.KindPostThread:
		expression, decode = x.ThreadExpression(request, limit), x.DecodeThreadPage
	case x.KindProfilePosts:
		expression, decode = x.ProfileExpression(request, limit), x.DecodeProfilePage
	default:
		return nil, commandError("unsupported_x_kind", "internal", "unsupported X collector kind", ExitInternal, nil)
	}
	result, err := session.Evaluate(ctx, expression, true)
	if err != nil || result.Exception != nil {
		return nil, commandError("x_collection_failed", "runtime", "X collection expression failed", ExitCheckFailed, nil)
	}
	page, err := decode(request, result.Object.Value)
	if err != nil {
		return nil, commandError("invalid_x_collection", "check_failed", err.Error(), ExitCheckFailed, nil)
	}
	return page.Records, nil
}

func xRecordKinds(records []x.Record, kind x.Kind) (observed, possiblyMissing []string) {
	seen := map[x.RecordKind]bool{}
	for _, record := range records {
		seen[record.Kind] = true
	}
	for _, recordKind := range xPossibleRecordKinds(kind) {
		if seen[x.RecordKind(recordKind)] {
			observed = append(observed, recordKind)
		} else {
			possiblyMissing = append(possiblyMissing, recordKind)
		}
	}
	return observed, possiblyMissing
}

func xPossibleRecordKinds(kind x.Kind) []string {
	if kind == x.KindProfilePosts {
		return []string{string(x.RecordPost)}
	}
	return []string{string(x.RecordPost), string(x.RecordReply)}
}
