package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/sourcecollect/reddit"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowRedditCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "reddit", Short: "Collect Reddit source-native records", Long: "Collect subreddit listings, threads, or user profiles from validated Reddit URLs. Plain output is detailed Markdown; use --json or --jq for structured processing."}
	cmd.AddCommand(a.newWorkflowRedditCollectionCommand("collect <reddit-url>", "Collect normalized records from a discovered Reddit URL", ""))
	cmd.AddCommand(a.newWorkflowRedditCollectionCommand("posts <subreddit-listing-url>", "Collect normalized threads from a Reddit subreddit listing", reddit.KindSubredditListing))
	return cmd
}

func (a *app) newWorkflowRedditPostsCommand() *cobra.Command {
	return a.newWorkflowRedditCollectionCommand("posts <subreddit-listing-url>", "Collect normalized threads from a Reddit subreddit listing", reddit.KindSubredditListing)
}

func (a *app) newWorkflowRedditCollectionCommand(use, short string, expected reddit.Kind) *cobra.Command {
	var limit int
	var wait time.Duration
	var keepOpen bool
	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    "Collect a validated Reddit source. Supported URL identity determines listing, thread, or profile behavior. Plain stdout is detailed Markdown; --json and --jq expose normalized records.",
		Example: "  cdp workflow reddit collect 'https://www.reddit.com/r/formula1/top/?t=week' > subreddit.md\n  cdp workflow reddit collect 'https://www.reddit.com/r/formula1/comments/example/' --jq '.records[] | {author, body}'\n  cdp schema workflow-reddit-collect --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			request, err := reddit.ParseExpected(args[0], expected)
			if err != nil {
				return commandError("invalid_reddit_url", "usage", err.Error(), ExitUsage, []string{"cdp workflow reddit collect https://www.reddit.com/r/formula1/top/?t=week --json"})
			}
			if limit < 1 || limit > 500 {
				return commandError("invalid_limit", "usage", "--limit must be between 1 and 500", ExitUsage, []string{"cdp workflow reddit posts https://www.reddit.com/r/formula1/ --limit 200 --json"})
			}
			commandTimeout := wait + 15*time.Second
			if commandTimeout < 30*time.Second {
				commandTimeout = 30 * time.Second
			}
			ctx, cancel := a.commandContextWithDefault(cmd, commandTimeout)
			defer cancel()
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
			}
			targetID, err := a.createWorkflowPageTargetWithKeepOpen(ctx, client, request.URL, "reddit-collect", keepOpen)
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
				return commandError("connection_failed", "connection", fmt.Sprintf("attach Reddit target: %v", err), ExitConnection, []string{"cdp pages --json"})
			}
			defer session.Close(ctx)
			if _, err := session.Navigate(ctx, request.URL); err != nil {
				return commandError("reddit_navigation_failed", "connection", err.Error(), ExitConnection, []string{"cdp workflow reddit collect " + request.URL + " --json"})
			}
			selector := redditReadinessSelector(request.Kind)
			readiness, err := waitForRenderedExtractReadiness(ctx, session, selector, wait, 0, "useful-content", 1, 1)
			if err != nil {
				return err
			}
			if err := reddit.ValidateFinalURL(request, readiness.URL); err != nil {
				return commandError("reddit_identity_changed", "check_failed", fmt.Sprintf("%s (final URL: %s)", err, readiness.URL), ExitCheckFailed, nil)
			}
			if request.Kind != reddit.KindSubredditListing {
				expansionStatus, interactions := "partial", 0
				profileStalled := false
				if request.Kind == reddit.KindThread {
					expansion, expansionErr := expandRenderedExtractDiscussion(ctx, session, renderedExtractContentPlan{Profile: renderedExtractContentProfileReddit})
					if expansionErr != nil {
						return expansionErr
					}
					expansionStatus, interactions = expansion.Status, expansion.Interactions
				}
				records, err := collectVisibleRedditRecords(ctx, session, request, limit)
				if err != nil {
					return err
				}
				if request.Kind == reddit.KindUserProfile && len(records) < limit && wait > 0 {
					seen := map[string]reddit.Record{}
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
						next, nextErr := collectVisibleRedditRecords(ctx, session, request, limit)
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
					records = make([]reddit.Record, 0, min(limit, len(order)))
					for _, id := range order[:min(limit, len(order))] {
						records = append(records, seen[id])
					}
					profileStalled = len(records) < limit
				}
				closeError := closePage()
				partialReason := "currently_visible_only"
				if profileStalled {
					partialReason = "pagination_stalled"
				}
				if len(records) >= limit {
					if limit >= 500 {
						expansionStatus, partialReason = "ceiling", "hard_limit"
					} else {
						expansionStatus, partialReason = "partial", "requested_limit"
					}
				}
				if partialReason == "currently_visible_only" && request.Kind == reddit.KindThread && expansionStatus != "exhausted" && expansionStatus != "ceiling" {
					partialReason = "discussion_" + expansionStatus
				}
				if request.Kind == reddit.KindThread && expansionStatus == "exhausted" {
					partialReason = ""
				}
				observed, missing := redditRecordKinds(records)
				continuation := "profile_scroll"
				if request.Kind == reddit.KindThread {
					continuation = "discussion_" + expansionStatus
				}
				coverage := dynamicSourceCoverage(observed, missing, expansionStatus, partialReason, continuation, "", expansionStatus != "exhausted" && expansionStatus != "ceiling")
				workflow := map[string]any{"name": "reddit-collect", "count": len(records), "limit": limit, "status": expansionStatus, "partial_reason": partialReason, "interactions": interactions, "discussion_interactions": interactions, "created_page": true, "closed": closeError == "", "close_error": closeError}
				return a.render(ctx, redditCollectionMarkdown(request.URL, request.Kind, records, nil, workflow, coverage), map[string]any{"ok": true, "request": request, "kind": request.Kind, "records": records, "coverage": coverage, "workflow": workflow})
			}
			deadline := time.Now().Add(wait)
			traversal := reddit.NewTraversal()
			lastCursor, status, partialReason := "", "partial", "currently_visible_only"
			for round := 0; round < 100 && traversal.Count() < limit; round++ {
				result, evalErr := session.Evaluate(ctx, reddit.CollectionExpression(request, limit), true)
				if evalErr != nil || result.Exception != nil {
					return commandError("reddit_collection_failed", "runtime", "Reddit collection expression failed", ExitCheckFailed, []string{"cdp workflow reddit posts " + request.URL + " --wait 15s --json"})
				}
				page, err := reddit.DecodePage(request.Subreddit, result.Object.Value)
				if err != nil {
					return commandError("invalid_reddit_collection", "check_failed", err.Error(), ExitCheckFailed, []string{"cdp snapshot --selector 'shreddit-feed shreddit-post' --json"})
				}
				observation := traversal.Observe(page, limit)
				lastCursor = page.NextCursor
				if observation.ReachedLimit {
					if limit >= 500 {
						status, partialReason = "ceiling", "hard_limit"
					} else {
						status, partialReason = "partial", "requested_limit"
					}
					break
				}
				if observation.Exhausted {
					status, partialReason = "exhausted", ""
					break
				}
				if observation.Stalled >= 3 || wait == 0 || time.Now().After(deadline) {
					status, partialReason = "partial", "pagination_stalled"
					break
				}
				if _, advanceErr := session.Evaluate(ctx, reddit.AdvanceExpression(), true); advanceErr != nil {
					status, partialReason = "partial", "continuation_failed"
					break
				}
				if _, scrollErr := session.Evaluate(ctx, "window.scrollTo(0, document.body.scrollHeight)", true); scrollErr != nil {
					status, partialReason = "partial", "scroll_failed"
					break
				}
				select {
				case <-ctx.Done():
					return commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, nil)
				case <-time.After(500 * time.Millisecond):
				}
			}
			collected := traversal.Threads()
			closeError := closePage()
			missing := []string(nil)
			if status != "exhausted" {
				missing = []string{"listing_thread"}
			}
			coverage := dynamicSourceCoverage([]string{"listing_thread"}, missing, status, partialReason, "listing_cursor", lastCursor, lastCursor != "")
			workflow := map[string]any{"name": "reddit-collect", "count": len(collected), "limit": limit, "status": status, "partial_reason": partialReason, "interactions": 0, "last_cursor": lastCursor, "created_page": true, "closed": closeError == "", "close_error": closeError}
			return a.render(ctx, redditCollectionMarkdown(request.URL, request.Kind, nil, collected, workflow, coverage), map[string]any{"ok": true, "request": request, "kind": request.Kind, "threads": collected, "next_cursor": lastCursor, "coverage": coverage, "workflow": workflow})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum source-native records to collect (1-500)")
	cmd.Flags().DurationVar(&wait, "wait", 10*time.Second, "how long to wait for visible Reddit source records")
	cmd.Flags().BoolVar(&keepOpen, "keep-open", false, "leave the workflow-created page open for debugging")
	return cmd
}

func redditReadinessSelector(kind reddit.Kind) string {
	switch kind {
	case reddit.KindThread:
		return "shreddit-post, shreddit-comment"
	case reddit.KindUserProfile:
		return "shreddit-feed shreddit-profile-comment, shreddit-feed shreddit-post"
	default:
		return "shreddit-feed shreddit-post"
	}
}

func collectVisibleRedditRecords(ctx context.Context, session *cdp.PageSession, request reddit.Request, limit int) ([]reddit.Record, error) {
	var expression string
	var decode func(reddit.Request, json.RawMessage) (reddit.RecordPage, error)
	switch request.Kind {
	case reddit.KindThread:
		expression, decode = reddit.ThreadExpression(request, limit), reddit.DecodeThreadPage
	case reddit.KindUserProfile:
		expression, decode = reddit.UserExpression(request, limit), reddit.DecodeUserPage
	default:
		return nil, commandError("unsupported_reddit_kind", "internal", "unsupported Reddit collector kind", ExitInternal, nil)
	}
	result, err := session.Evaluate(ctx, expression, true)
	if err != nil {
		return nil, commandError("reddit_collection_failed", "runtime", fmt.Sprintf("Reddit collection expression failed: %v", err), ExitCheckFailed, []string{"cdp snapshot --selector shreddit-post --json"})
	}
	if result.Exception != nil {
		return nil, commandError("reddit_collection_failed", "runtime", "Reddit collection expression failed: "+result.Exception.Text, ExitCheckFailed, []string{"cdp snapshot --selector shreddit-post --json"})
	}
	page, err := decode(request, result.Object.Value)
	if err != nil {
		return nil, commandError("invalid_reddit_collection", "check_failed", err.Error(), ExitCheckFailed, nil)
	}
	return page.Records, nil
}

func redditThreadLines(threads []reddit.Thread) []string {
	lines := make([]string, 0, len(threads))
	for _, thread := range threads {
		lines = append(lines, fmt.Sprintf("%s · %d points · %d comments · %s", thread.Title, thread.Score, thread.CommentCount, thread.Permalink))
	}
	return lines
}

func redditRecordKinds(records []reddit.Record) (observed, possiblyMissing []string) {
	seen := map[reddit.RecordKind]bool{}
	for _, record := range records {
		seen[record.Kind] = true
	}
	for _, kind := range []reddit.RecordKind{reddit.RecordSubmission, reddit.RecordComment} {
		if seen[kind] {
			observed = append(observed, string(kind))
		} else {
			possiblyMissing = append(possiblyMissing, string(kind))
		}
	}
	return observed, possiblyMissing
}
