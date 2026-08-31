package cli

import (
	"fmt"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/sourcecollect/arxiv"
	"github.com/spf13/cobra"
	"time"
)

func (a *app) newWorkflowArxivCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "arxiv", Short: "Collect arXiv source-native records", Long: "Collect a version-pinned arXiv paper and its bounded references. Plain output is detailed Markdown; use --json or --jq for structured processing."}
	cmd.AddCommand(&cobra.Command{Use: "collect <paper-url>", Short: "Collect a version-pinned paper and references", Long: "Collect a version-pinned arXiv paper. Plain stdout is detailed Markdown with paper metadata and references; --json and --jq preserve normalized fields.", Example: "  cdp workflow arxiv collect 'https://arxiv.org/abs/2604.12374v2' > paper.md\n  cdp workflow arxiv collect 'https://arxiv.org/abs/2604.12374v2' --jq '.references[] | .text'\n  cdp schema workflow-arxiv-collect --json", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		req, err := arxiv.Parse(args[0])
		if err != nil {
			return commandError("invalid_arxiv_url", "usage", err.Error(), ExitUsage, nil)
		}
		ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
		defer cancel()
		client, closeClient, err := a.browserCDPClient(ctx)
		if err != nil {
			return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
		}
		target, err := a.createWorkflowPageTarget(ctx, client, req.URL, "arxiv-collect")
		if err != nil {
			_ = closeClient(ctx)
			return err
		}
		closeWorkflowPage := a.workflowPageCloser(client, target, req.URL, false)
		session, err := cdp.AttachToTargetWithClient(ctx, client, target, closeClient)
		if err != nil {
			_, _ = closeWorkflowPage()
			_ = closeClient(ctx)
			return commandError("connection_failed", "connection", err.Error(), ExitConnection, nil)
		}
		defer session.Close(ctx)
		defer func() { _, _ = closeWorkflowPage() }()
		if _, err = session.Navigate(ctx, req.URL); err != nil {
			return commandError("arxiv_navigation_failed", "connection", err.Error(), ExitConnection, nil)
		}
		ready, err := waitForRenderedExtractReadiness(ctx, session, "body", 10*time.Second, 0, "load", 0, 0)
		if err != nil {
			return err
		}
		if !renderedExtractReadinessQualityPassed(ready) {
			return commandError("arxiv_navigation_not_ready", "timeout", "arXiv abstract page did not finish loading", ExitTimeout, nil)
		}
		final, err := arxiv.ValidateFinalURL(req, ready.URL)
		if err != nil {
			return commandError("arxiv_identity_changed", "check_failed", err.Error(), ExitCheckFailed, nil)
		}
		if _, err := session.Navigate(ctx, "https://arxiv.org/html/"+final.Identifier); err != nil {
			return commandError("arxiv_html_navigation_failed", "connection", err.Error(), ExitConnection, nil)
		}
		htmlReady, err := waitForRenderedExtractReadiness(ctx, session, "article.ltx_document", 10*time.Second, 0, "useful-content", 1, 1)
		if err != nil {
			return err
		}
		if !renderedExtractReadinessQualityPassed(htmlReady) {
			return commandError("arxiv_html_not_ready", "timeout", "arXiv HTML page did not reach useful content", ExitTimeout, nil)
		}
		if _, err := arxiv.ValidateFinalURL(final, htmlReady.URL); err != nil {
			return commandError("arxiv_identity_changed", "check_failed", err.Error(), ExitCheckFailed, nil)
		}
		result, err := session.Evaluate(ctx, arxiv.PaperExpression(final.Identifier, 500), true)
		if err != nil || result.Exception != nil {
			return commandError("arxiv_collection_failed", "runtime", "arXiv collection expression failed", ExitCheckFailed, nil)
		}
		page, err := arxiv.DecodePaperPage(final, result.Object.Value)
		if err != nil {
			return commandError("invalid_arxiv_collection", "check_failed", err.Error(), ExitCheckFailed, nil)
		}
		status, reason := "exhausted", ""
		if len(page.References) >= 500 {
			status, reason = "partial", "hard_limit_without_exhaustion_proof"
		}
		observed, missing := []string{"paper"}, []string(nil)
		if len(page.References) > 0 {
			observed = append(observed, "reference")
		}
		if status != "exhausted" {
			missing = []string{"reference"}
		}
		coverage := staticSourceCoverage(observed, missing, status, reason)
		closed, closeError := closeWorkflowPage()
		workflow := map[string]any{"name": "arxiv-collect", "count": len(page.References), "limit": 500, "status": status, "partial_reason": reason, "interactions": 0, "created_page": true, "closed": closed, "close_error": closeError}
		if closeError != "" {
			return commandError("arxiv_cleanup_failed", "cleanup", closeError, ExitCheckFailed, nil)
		}
		return a.render(ctx, arxivCollectionMarkdown(final.URL, page.Paper, page.References, workflow, coverage), map[string]any{"ok": true, "request": final, "kind": "paper", "paper": page.Paper, "references": page.References, "coverage": coverage, "workflow": workflow})
	}})
	return cmd
}

var _ = fmt.Sprintf
