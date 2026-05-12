package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type hackerNewsFrontpage struct {
	URL          string            `json:"url"`
	Title        string            `json:"title"`
	Count        int               `json:"count"`
	Stories      []hackerNewsStory `json:"stories"`
	Organization map[string]string `json:"organization"`
	Error        *snapshotError    `json:"error,omitempty"`
}

type hackerNewsStory struct {
	Rank        int    `json:"rank,omitempty"`
	ID          string `json:"id,omitempty"`
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	Site        string `json:"site,omitempty"`
	Score       int    `json:"score,omitempty"`
	User        string `json:"user,omitempty"`
	Age         string `json:"age,omitempty"`
	Comments    int    `json:"comments,omitempty"`
	CommentsURL string `json:"comments_url,omitempty"`
}

func (a *app) newWorkflowHackerNewsCommand() *cobra.Command {
	var limit int
	var wait time.Duration
	var keepOpen bool
	cmd := &cobra.Command{
		Use:   "hacker-news [url]",
		Short: "Open Hacker News and summarize visible stories",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()

			rawURL := "https://news.ycombinator.com/"
			if len(args) == 1 {
				rawURL = strings.TrimSpace(args[0])
			}
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
			}
			targetID, err := a.createWorkflowPageTarget(ctx, client, rawURL, "hacker-news")
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

			frontpage, err := waitForHackerNewsStories(ctx, session, limit, wait)
			if err != nil {
				return err
			}
			if len(frontpage.Stories) == 0 {
				return commandError("no_visible_posts", "check_failed", "no Hacker News story rows matched tr.athing", ExitCheckFailed, []string{"cdp workflow hacker-news --wait 30s --json", "cdp snapshot --selector '.titleline' --json"})
			}
			closed, closeErr := closeWorkflowPage()
			lines := hackerNewsStoryLines(frontpage.Stories)
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":           true,
				"url":          rawURL,
				"target":       pageRow(cdp.TargetInfo{TargetID: targetID, Type: "page", URL: rawURL}),
				"workflow":     map[string]any{"name": "hacker-news", "count": len(frontpage.Stories), "wait": durationString(wait), "limit": limit, "created_page": true, "closed": closed, "close_error": closeErr, "next_commands": []string{fmt.Sprintf("cdp page close --target %s --json", targetID)}},
				"organization": frontpage.Organization,
				"stories":      frontpage.Stories,
				"frontpage":    frontpage,
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 30, "maximum number of stories to return; use 0 for no limit")
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "how long to wait for Hacker News story rows")
	cmd.Flags().BoolVar(&keepOpen, "keep-open", false, "leave the workflow-created page open for debugging")
	return cmd
}

func waitForHackerNewsStories(ctx context.Context, session *cdp.PageSession, limit int, wait time.Duration) (hackerNewsFrontpage, error) {
	if limit < 0 {
		return hackerNewsFrontpage{}, commandError("usage", "usage", "--limit must be non-negative", ExitUsage, []string{"cdp workflow hacker-news --limit 30 --json"})
	}
	if wait < 0 {
		return hackerNewsFrontpage{}, commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp workflow hacker-news --wait 30s --json"})
	}
	deadline := time.Now().Add(wait)
	var last hackerNewsFrontpage
	for {
		frontpage, err := collectHackerNewsFrontpage(ctx, session, limit)
		if err != nil {
			return hackerNewsFrontpage{}, err
		}
		last = frontpage
		if len(frontpage.Stories) > 0 || wait == 0 || time.Now().After(deadline) {
			return last, nil
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return hackerNewsFrontpage{}, commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp workflow hacker-news --timeout 45s --json"})
		case <-timer.C:
		}
	}
}

func collectHackerNewsFrontpage(ctx context.Context, session *cdp.PageSession, limit int) (hackerNewsFrontpage, error) {
	result, err := session.Evaluate(ctx, hackerNewsExpression(limit), true)
	if err != nil {
		return hackerNewsFrontpage{}, commandError("connection_failed", "connection", fmt.Sprintf("Hacker News workflow target %s: %v", session.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	if result.Exception != nil {
		return hackerNewsFrontpage{}, commandError("javascript_exception", "runtime", fmt.Sprintf("Hacker News workflow javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp workflow hacker-news --json", "cdp snapshot --selector body --json"})
	}
	var frontpage hackerNewsFrontpage
	if err := json.Unmarshal(result.Object.Value, &frontpage); err != nil {
		return hackerNewsFrontpage{}, commandError("invalid_workflow_result", "internal", fmt.Sprintf("decode Hacker News workflow result: %v", err), ExitInternal, []string{"cdp doctor --json", "cdp eval 'document.title' --json"})
	}
	if frontpage.Error != nil {
		return hackerNewsFrontpage{}, commandError("invalid_selector", "usage", fmt.Sprintf("Hacker News selector failed: %s", frontpage.Error.Message), ExitUsage, []string{"cdp workflow hacker-news --json", "cdp snapshot --selector '.athing' --json"})
	}
	return frontpage, nil
}

func hackerNewsExpression(limit int) string {
	return fmt.Sprintf(`(() => {
  "__cdp_cli_hn_frontpage__";
  const limit = %d;
  const normalize = (value) => (value || "").replace(/\s+/g, " ").trim();
  const parseNumber = (value) => {
    const match = normalize(value).match(/\d+/);
    return match ? Number(match[0]) : 0;
  };
  let rows;
  try {
    rows = Array.from(document.querySelectorAll("tr.athing"));
  } catch (error) {
    return { url: location.href, title: document.title, count: 0, stories: [], organization: {}, error: { name: error.name, message: error.message } };
  }
  const stories = [];
  for (const row of rows) {
    const titleLink = row.querySelector(".titleline > a") || row.querySelector(".storylink");
    if (!titleLink) continue;
    const metaRow = row.nextElementSibling;
    const subtext = metaRow && metaRow.querySelector(".subtext");
    const commentLink = Array.from(subtext ? subtext.querySelectorAll("a") : []).find((link) => /comment|discuss/i.test(link.textContent || ""));
    stories.push({
      rank: parseNumber(row.querySelector(".rank") && row.querySelector(".rank").textContent),
      id: row.getAttribute("id") || "",
      title: normalize(titleLink.textContent),
      url: titleLink.href || titleLink.getAttribute("href") || "",
      site: normalize(row.querySelector(".sitestr") && row.querySelector(".sitestr").textContent),
      score: parseNumber(subtext && subtext.querySelector(".score") && subtext.querySelector(".score").textContent),
      user: normalize(subtext && subtext.querySelector(".hnuser") && subtext.querySelector(".hnuser").textContent),
      age: normalize(subtext && subtext.querySelector(".age") && subtext.querySelector(".age").textContent),
      comments: parseNumber(commentLink && commentLink.textContent),
      comments_url: commentLink ? commentLink.href : ""
    });
    if (limit > 0 && stories.length >= limit) break;
  }
  return {
    url: location.href,
    title: document.title,
    count: stories.length,
    stories,
    organization: {
      page_kind: "table-based link aggregator front page",
      container_selector: "table.itemlist",
      story_row_selector: "tr.athing",
      metadata_row_selector: "tr.athing + tr .subtext",
      title_selector: ".titleline > a",
      rank_selector: ".rank",
      discussion_signal: "score, author, age, and comment links live in the metadata row after each story row"
    }
  };
})()`, limit)
}

func hackerNewsStoryLines(stories []hackerNewsStory) []string {
	lines := make([]string, 0, len(stories)+1)
	lines = append(lines, fmt.Sprintf("%-4s %7s %9s  %s", "rank", "points", "comments", "title"))
	for _, story := range stories {
		lines = append(lines, fmt.Sprintf(
			"#%-3d %7s %9s  %s",
			story.Rank,
			hackerNewsCountLabel(story.Score, "pt", "pts"),
			hackerNewsCountLabel(story.Comments, "comment", "comments"),
			story.Title,
		))
	}
	return lines
}

func hackerNewsCountLabel(count int, singular, plural string) string {
	if count == 0 {
		return "-"
	}
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}
