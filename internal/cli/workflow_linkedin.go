package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/sourcecollect/linkedin"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowLinkedInCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "linkedin", Short: "Collect LinkedIn source-native records"}
	var limit int
	var wait time.Duration
	collect := &cobra.Command{Use: "collect <linkedin-url>", Short: "Collect normalized records from a discovered LinkedIn URL", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		request, err := linkedin.Parse(args[0])
		if err != nil {
			return commandError("invalid_linkedin_url", "usage", err.Error(), ExitUsage, nil)
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
		targetID, err := a.createWorkflowPageTarget(ctx, client, request.URL, "linkedin-collect")
		if err != nil {
			_ = closeClient(ctx)
			return err
		}
		closed := false
		closePage := func() string {
			if closed {
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
			_ = cdp.CloseTargetWithClient(ctx, client, targetID)
			_ = closeClient(ctx)
			return commandError("connection_failed", "connection", fmt.Sprintf("attach LinkedIn target: %v", err), ExitConnection, nil)
		}
		defer session.Close(ctx)
		if _, err := session.Navigate(ctx, request.URL); err != nil {
			return commandError("linkedin_navigation_failed", "connection", err.Error(), ExitConnection, nil)
		}
		readiness, err := waitForRenderedExtractReadiness(ctx, session, "article", wait, 0, "useful-content", 1, 1)
		if err != nil {
			return err
		}
		final, err := linkedin.ValidateFinalURL(request, readiness.URL)
		if err != nil {
			return commandError("linkedin_identity_changed", "check_failed", err.Error(), ExitCheckFailed, nil)
		}
		records, err := collectVisibleLinkedInRecords(ctx, session, final, limit)
		if err != nil {
			return err
		}
		status, reason := "partial", "currently_visible_only"
		interactions := 0
		if final.Kind == linkedin.KindActivityThread {
			expansion, expansionErr := expandRenderedExtractDiscussion(ctx, session, renderedExtractContentPlan{Profile: renderedExtractContentProfileLinkedIn})
			if expansionErr != nil {
				return expansionErr
			}
			interactions = expansion.Interactions
			if next, nextErr := collectVisibleLinkedInThreadComments(ctx, session, final, limit); nextErr == nil {
				records = mergeLinkedInRecords(records, next, limit)
			} else if expansion.Status == "exhausted" {
				return nextErr
			}
			if expansion.Status == "exhausted" {
				status, reason = "exhausted", ""
			} else {
				reason = "discussion_" + expansion.Status
			}
		}
		if final.Kind == linkedin.KindCompanyPosts && wait > 0 && len(records) < limit {
			var traversalErr error
			records, status, reason, traversalErr = collectLinkedInCompanyFeed(ctx, session, final, records, limit, wait)
			if traversalErr != nil {
				return traversalErr
			}
		}
		if len(records) >= limit {
			if status != "exhausted" {
				reason = "requested_limit_without_exhaustion_proof"
			}
		}
		closeError := closePage()
		if closeError != "" {
			return commandError("linkedin_cleanup_failed", "cleanup", closeError, ExitCheckFailed, nil)
		}
		observed, missing := linkedInRecordKinds(records, final.Kind)
		continuation := "company_feed"
		if final.Kind == linkedin.KindActivityThread {
			continuation = "discussion_" + status
		}
		coverage := dynamicSourceCoverage(observed, missing, status, reason, continuation, "", status != "exhausted" && status != "ceiling")
		return a.render(ctx, "", map[string]any{"ok": true, "request": final, "kind": final.Kind, "records": records, "coverage": coverage, "workflow": map[string]any{"name": "linkedin-collect", "count": len(records), "limit": limit, "status": status, "partial_reason": reason, "interactions": interactions, "discussion_interactions": interactions, "created_page": true, "closed": closeError == "", "close_error": closeError}})
	}}
	collect.Flags().IntVar(&limit, "limit", 100, "maximum source-native records to collect (1-500)")
	collect.Flags().DurationVar(&wait, "wait", 10*time.Second, "how long to wait for visible LinkedIn source records")
	cmd.AddCommand(collect)
	return cmd
}

func collectVisibleLinkedInRecords(ctx context.Context, session *cdp.PageSession, request linkedin.Request, limit int) ([]linkedin.Record, error) {
	var expression string
	var decode func(linkedin.Request, json.RawMessage) (linkedin.RecordPage, error)
	switch request.Kind {
	case linkedin.KindActivityThread:
		expression, decode = linkedin.ThreadExpression(request, limit), linkedin.DecodeThreadPage
	case linkedin.KindCompanyPosts:
		expression, decode = linkedin.CompanyExpression(request, limit), linkedin.DecodeCompanyPage
	default:
		return nil, commandError("unsupported_linkedin_kind", "internal", "unsupported LinkedIn collector kind", ExitInternal, nil)
	}
	result, err := session.Evaluate(ctx, expression, true)
	if err != nil || result.Exception != nil {
		return nil, commandError("linkedin_collection_failed", "runtime", "LinkedIn collection expression failed", ExitCheckFailed, nil)
	}
	page, err := decode(request, result.Object.Value)
	if err != nil {
		return nil, commandError("invalid_linkedin_collection", "check_failed", err.Error(), ExitCheckFailed, nil)
	}
	return page.Records, nil
}

func collectVisibleLinkedInThreadComments(ctx context.Context, session *cdp.PageSession, request linkedin.Request, limit int) ([]linkedin.Record, error) {
	result, err := session.Evaluate(ctx, linkedin.ThreadExpression(request, limit), true)
	if err != nil || result.Exception != nil {
		return nil, commandError("linkedin_collection_failed", "runtime", "LinkedIn collection expression failed", ExitCheckFailed, nil)
	}
	if page, fullErr := linkedin.DecodeThreadPage(request, result.Object.Value); fullErr == nil {
		comments := make([]linkedin.Record, 0, len(page.Records))
		for _, record := range page.Records {
			if record.Kind == linkedin.RecordComment {
				comments = append(comments, record)
			}
		}
		return comments, nil
	}
	page, err := linkedin.DecodeThreadComments(request, result.Object.Value)
	if err != nil {
		return nil, commandError("invalid_linkedin_collection", "check_failed", err.Error(), ExitCheckFailed, nil)
	}
	return page.Records, nil
}

func mergeLinkedInRecords(first, later []linkedin.Record, limit int) []linkedin.Record {
	seen := make(map[string]struct{}, len(first)+len(later))
	merged := make([]linkedin.Record, 0, min(limit, len(first)+len(later)))
	for _, records := range [][]linkedin.Record{first, later} {
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

func collectLinkedInCompanyFeed(ctx context.Context, session *cdp.PageSession, request linkedin.Request, initial []linkedin.Record, limit int, wait time.Duration) ([]linkedin.Record, string, string, error) {
	traversal := linkedin.NewTraversal()
	records, deadline := initial, time.Now().Add(wait)
	for round := 0; round < 100; round++ {
		var progress struct {
			TerminalExtent int `json:"terminal_extent"`
		}
		if err := evaluateJSONValue(ctx, session, linkedin.ProgressExpression(), "LinkedIn company pagination", &progress); err != nil {
			return records, "partial", "pagination_evidence_failed", err
		}
		ids := make([]string, 0, len(records))
		for _, record := range records {
			ids = append(ids, record.ID)
		}
		observation := traversal.Observe(linkedin.Page{ActivityIDs: ids, TerminalExtent: progress.TerminalExtent}, false, true)
		if len(records) >= limit {
			return records, "partial", "requested_limit_without_exhaustion_proof", nil
		}
		if observation.Exhausted {
			return records, "exhausted", "", nil
		}
		if time.Now().After(deadline) {
			return records, "partial", "pagination_wait_expired", nil
		}
		if _, err := session.Evaluate(ctx, "window.scrollTo(0, document.body.scrollHeight)", true); err != nil {
			return records, "partial", "scroll_failed", nil
		}
		select {
		case <-ctx.Done():
			return records, "partial", "timeout", commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, nil)
		case <-time.After(500 * time.Millisecond):
		}
		next, err := collectVisibleLinkedInRecords(ctx, session, request, limit)
		if err != nil {
			return records, "partial", "collection_failed", err
		}
		records = mergeLinkedInRecords(records, next, limit)
	}
	return records, "partial", "pagination_round_limit", nil
}

func linkedInRecordKinds(records []linkedin.Record, kind linkedin.Kind) (observed, possiblyMissing []string) {
	seen := map[linkedin.RecordKind]bool{}
	for _, record := range records {
		seen[record.Kind] = true
	}
	possible := []linkedin.RecordKind{linkedin.RecordActivity}
	if kind == linkedin.KindActivityThread {
		possible = append(possible, linkedin.RecordComment)
	}
	for _, recordKind := range possible {
		if seen[recordKind] {
			observed = append(observed, string(recordKind))
		} else {
			possiblyMissing = append(possiblyMissing, string(recordKind))
		}
	}
	return observed, possiblyMissing
}
