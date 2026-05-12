package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowFeedsCommand() *cobra.Command {
	var wait time.Duration
	var keepOpen bool
	cmd := &cobra.Command{Use: "feeds <url>", Short: "Discover RSS, Atom, and JSON Feed links", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if wait < 0 {
			return commandError("usage", "usage", "--wait-load must be non-negative", ExitUsage, []string{"cdp workflow feeds https://example.com --wait-load 10s --json"})
		}
		ctx, cancel := a.commandContextWithDefault(cmd, wait+10*time.Second)
		defer cancel()
		client, closeClient, err := a.browserCDPClient(ctx)
		if err != nil {
			return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
		}
		rawURL := strings.TrimSpace(args[0])
		targetID, err := a.createWorkflowPageTarget(ctx, client, rawURL, "feeds")
		if err != nil {
			_ = closeClient(ctx)
			return err
		}
		closeWorkflowPage := func() (bool, string) {
			if keepOpen {
				return false, ""
			}
			if err := cdp.CloseTargetWithClient(ctx, client, targetID); err != nil {
				return false, err.Error()
			}
			return true, ""
		}
		session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
		if err != nil {
			_ = closeClient(ctx)
			return commandError("connection_failed", "connection", fmt.Sprintf("attach target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
		}
		defer session.Close(ctx)
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp workflow feeds <url> --wait-load 10s --json"})
			case <-timer.C:
			}
		}
		var feeds []map[string]string
		if err := evaluateJSONValue(ctx, session, feedDiscoveryExpression(), "feeds", &feeds); err != nil {
			return err
		}
		closed, closeErr := closeWorkflowPage()
		workflow := map[string]any{"name": "feeds", "url": rawURL, "created_page": true, "closed": closed, "close_error": closeErr}
		if len(feeds) == 0 {
			return commandError("feed_not_found", "check_failed", "No RSS, Atom, or JSON Feed links were advertised by the page", ExitCheckFailed, []string{"cdp workflow feeds <url> --keep-open --json", "cdp eval 'Array.from(document.querySelectorAll(\"link[rel~=alternate]\"))' --json"})
		}
		return a.render(ctx, fmt.Sprintf("feeds\t%d", len(feeds)), map[string]any{"ok": true, "workflow": workflow, "page": map[string]any{"target_id": targetID, "final_url": rawURL}, "feeds": feeds})
	}}
	cmd.Flags().DurationVar(&wait, "wait-load", 5*time.Second, "how long to wait before discovering feed links")
	cmd.Flags().BoolVar(&keepOpen, "keep-open", false, "leave the workflow-created page open for debugging")
	return cmd
}

func feedDiscoveryExpression() string {
	return `(() => Array.from(document.querySelectorAll('link[rel~="alternate"]')).map((link) => {
		const type = (link.getAttribute('type') || '').toLowerCase();
		const href = link.getAttribute('href') || '';
		const rel = link.getAttribute('rel') || '';
		const isFeed = type.includes('rss') || type.includes('atom') || type.includes('feed+json') || /rss|atom|feed/i.test(href);
		if (!isFeed) return null;
		return { type: type.includes('atom') ? 'atom' : (type.includes('json') ? 'json' : 'rss'), title: link.getAttribute('title') || '', href, url: new URL(href, document.baseURI).href, mime: type, source: 'link[rel~=alternate]', rel };
	}).filter(Boolean))()`
}
