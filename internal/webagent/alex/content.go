package alex

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const ContentFetchSchemaVersion = "alex-content-fetch/v1"

type ContentFetchConfig struct {
	BrowserConfig
	Store        *Store
	Timeout      time.Duration
	PollInterval time.Duration
	Now          func() time.Time
}

type ContentTarget struct {
	Course  Course
	Chapter Chapter
}

type ContentSummary struct {
	CourseKey    string `json:"course_key"`
	ChapterID    string `json:"chapter_id"`
	URL          string `json:"url"`
	HeadingCount int    `json:"heading_count"`
	TextLength   int    `json:"text_length"`
	FetchedAt    string `json:"fetched_at"`
}

type ContentFetchData struct {
	SchemaVersion string           `json:"schema_version"`
	Count         int              `json:"count"`
	Limited       bool             `json:"limited"`
	Content       *Content         `json:"content,omitempty"`
	Fetched       []ContentSummary `json:"fetched"`
}

type renderedContentObservation struct {
	URL          string   `json:"url"`
	Ready        bool     `json:"ready"`
	Text         string   `json:"text"`
	TextLength   int      `json:"text_length"`
	Headings     []string `json:"headings"`
	HeadingCount int      `json:"heading_count"`
}

func SelectContentTargets(
	catalog Catalog,
	courseID string,
	chapterID string,
	allChapters bool,
	allCourses bool,
	limit int,
) ([]ContentTarget, error) {
	courseID = strings.TrimSpace(courseID)
	chapterID = strings.TrimSpace(chapterID)
	if chapterID != "" && (allChapters || allCourses) {
		return nil, fmt.Errorf(
			"--chapter-id cannot be combined with bulk fetch flags",
		)
	}
	if allChapters && allCourses {
		return nil, fmt.Errorf(
			"--all-chapters and --all-courses are mutually exclusive",
		)
	}
	if (chapterID != "" || allChapters) && courseID == "" {
		return nil, fmt.Errorf(
			"--course is required unless --all-courses is used",
		)
	}
	if allCourses && courseID != "" {
		return nil, fmt.Errorf(
			"--course cannot be combined with --all-courses",
		)
	}
	if limit < 0 {
		return nil, fmt.Errorf("--limit must be greater than zero")
	}

	targets := []ContentTarget{}
	appendCourse := func(course Course) {
		for _, chapter := range catalog.Chapters[course.Key] {
			targets = append(targets, ContentTarget{
				Course:  course,
				Chapter: chapter,
			})
		}
	}
	if allCourses {
		for _, key := range sortedCourseKeys(catalog) {
			appendCourse(catalog.Courses[key])
		}
	} else {
		course, found := catalog.Courses[courseID]
		if !found {
			return nil, fmt.Errorf("unknown dynamic course key %q", courseID)
		}
		if chapterID != "" {
			_, chapter, err := catalog.Resolve(courseID, chapterID)
			if err != nil {
				return nil, err
			}
			targets = append(targets, ContentTarget{
				Course:  course,
				Chapter: chapter,
			})
		} else if allChapters {
			appendCourse(course)
		} else {
			return nil, fmt.Errorf(
				"provide --chapter-id, --all-chapters, or --all-courses",
			)
		}
	}
	if limit > 0 && len(targets) > limit {
		targets = targets[:limit]
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no dynamic content targets were selected")
	}
	return targets, nil
}

func FetchContent(
	ctx context.Context,
	config ContentFetchConfig,
	targets []ContentTarget,
	limited bool,
) webagent.Result {
	runID := webagent.NewRunID()
	data := ContentFetchData{
		SchemaVersion: ContentFetchSchemaVersion,
		Limited:       limited,
		Fetched:       []ContentSummary{},
	}
	if config.Store == nil {
		return contentFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"alex_state_unavailable", "internal",
			"Ask Alex owner-only content state is unavailable",
			data, []string{"cdp doctor --json"},
		)
	}
	if len(targets) == 0 || len(targets) > 5000 {
		return contentFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"alex_content_targets_invalid", "usage",
			"Ask Alex content fetch requires between one and 5000 exact dynamic targets",
			data, []string{"cdp workflow agent alex chapters list --json"},
		)
	}
	for _, target := range targets {
		if err := target.Course.Validate(); err != nil {
			return contentFailure(
				runID, config, webagent.StagePlanned, nil,
				webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
				"alex_content_target_invalid", "usage",
				"Ask Alex content target did not match dynamic catalog evidence",
				data, []string{"cdp workflow agent alex catalog refresh --json"},
			)
		}
		if err := target.Chapter.Validate(target.Course); err != nil {
			return contentFailure(
				runID, config, webagent.StagePlanned, nil,
				webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
				"alex_content_target_invalid", "usage",
				"Ask Alex content target did not match dynamic catalog evidence",
				data, []string{"cdp workflow agent alex catalog refresh --json"},
			)
		}
	}
	if config.Timeout <= 0 {
		config.Timeout = 45 * time.Second
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 250 * time.Millisecond
	}

	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationContentFetch,
		"about:blank",
		"headed_rendered_content",
		data,
		func(
			lease *browserflow.Lease,
			targetEvidence *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			first := targets[0].Chapter.URL
			if err := preparePage(ctx, config.Client, session, first); err != nil {
				return contentFailure(
					runID, config, webagent.StageAttached,
					targetEvidence, pending,
					"alex_content_prepare_failed", "connection",
					"Ask Alex content fetch could not prepare the exact headed target",
					data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return contentFailure(
					runID, config, webagent.StageAttached,
					targetEvidence, pending,
					"alex_content_prepare_state_failed", "internal",
					"Ask Alex content prepared state could not be persisted",
					data, cleanupCommands(runID, pending),
				)
			}
			for index, contentTarget := range targets {
				if index > 0 {
					if _, err := session.Navigate(
						ctx,
						contentTarget.Chapter.URL,
					); err != nil {
						_ = lease.MarkIncomplete(context.Background())
						return contentFailure(
							runID, config, webagent.StagePrepared,
							targetEvidence, pending,
							"alex_content_navigation_failed", "connection",
							"Ask Alex content fetch could not navigate the exact owned target",
							data, cleanupCommands(runID, pending),
						)
					}
				}
				content, err := readRenderedContent(
					ctx,
					session,
					contentTarget,
					config.Timeout,
					config.PollInterval,
					nowFor(config.Now),
				)
				if err != nil {
					_ = lease.MarkIncomplete(context.Background())
					return contentFailure(
						runID, config, webagent.StagePrepared,
						targetEvidence, pending,
						"alex_content_not_ready", "provider",
						"ByteByteGo rendered chapter content did not satisfy the bounded exact-route contract",
						data, cleanupCommands(runID, pending),
					)
				}
				if err := config.Store.SaveContent(ctx, content); err != nil {
					_ = lease.MarkIncomplete(context.Background())
					return contentFailure(
						runID, config, webagent.StagePrepared,
						targetEvidence, pending,
						"alex_content_state_write_failed", "internal",
						"Ask Alex rendered content could not be persisted to owner-only cache",
						data, cleanupCommands(runID, pending),
					)
				}
				data.Fetched = append(data.Fetched, ContentSummary{
					CourseKey:    content.CourseKey,
					ChapterID:    content.ChapterID,
					URL:          content.URL,
					HeadingCount: len(content.Headings),
					TextLength:   len(content.Text),
					FetchedAt:    content.FetchedAt,
				})
				if len(targets) == 1 {
					copy := content
					data.Content = &copy
				}
			}
			if err := lease.MarkTerminal(ctx); err != nil {
				return contentFailure(
					runID, config, webagent.StageObserveTerminal,
					targetEvidence, pending,
					"alex_content_terminal_state_failed", "internal",
					"Ask Alex content terminal state could not be persisted",
					data, cleanupCommands(runID, pending),
				)
			}
			data.Count = len(data.Fetched)
			return operationSuccess(
				runID,
				config.BuildCommit,
				webagent.OperationContentFetch,
				webagent.StateTerminal,
				webagent.StageObserveTerminal,
				"headed_rendered_content",
				targetEvidence,
				pending,
				nil,
				data,
				[]string{"cdp workflow agent alex ask --stdin --json"},
			)
		},
	)
}

func readRenderedContent(
	ctx context.Context,
	session *cdp.PageSession,
	target ContentTarget,
	timeout time.Duration,
	pollInterval time.Duration,
	now time.Time,
) (Content, error) {
	var observation renderedContentObservation
	_, err := pollUntil(
		ctx,
		timeout,
		pollInterval,
		func() (bool, error) {
			if err := evaluateRenderedContent(
				ctx,
				session,
				&observation,
			); err != nil {
				return false, err
			}
			return observation.URL == target.Chapter.URL &&
				observation.Ready &&
				observation.TextLength > 100, nil
		},
	)
	if err != nil {
		return Content{}, err
	}
	if observation.TextLength > 2<<20 ||
		len(observation.Text) > 2<<20 ||
		observation.HeadingCount > 512 {
		return Content{}, fmt.Errorf("rendered content exceeds its bounded contract")
	}
	headings := make([]string, 0, len(observation.Headings))
	for _, heading := range observation.Headings {
		if heading = cleanText(heading); heading != "" {
			headings = append(headings, heading)
		}
	}
	content := Content{
		SchemaVersion: ContentSchemaVersion,
		CourseKey:     target.Course.Key,
		ChapterID:     target.Chapter.ChapterID,
		URL:           target.Chapter.URL,
		Headings:      headings,
		Text:          strings.TrimSpace(observation.Text),
		FetchedAt:     now.UTC().Format(time.RFC3339Nano),
	}
	if err := content.Validate(); err != nil {
		return Content{}, err
	}
	return content, nil
}

func evaluateRenderedContent(
	ctx context.Context,
	session *cdp.PageSession,
	target *renderedContentObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
	  const clean = value => String(value || '').trim();
	  const candidates = [
	    document.querySelector('article'),
	    document.querySelector('main'),
	    document.body
	  ].filter(Boolean);
	  const selected = candidates.find(node => clean(node.innerText).length > 100) || null;
	  const text = clean(selected?.innerText);
	  const headings = selected
	    ? [...selected.querySelectorAll('h1,h2,h3')]
	        .map(node => clean(node.innerText))
	        .filter(Boolean)
	    : [];
	  return {
	    url: location.href,
	    ready: Boolean(selected),
	    text: text.slice(0, 2097153),
	    text_length: text.length,
	    headings: headings.slice(0, 513),
	    heading_count: headings.length
	  };
	})()`, target)
}

func contentFailure(
	runID string,
	config ContentFetchConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	code string,
	errClass string,
	message string,
	data ContentFetchData,
	next []string,
) webagent.Result {
	return operationFailure(
		runID,
		config.BuildCommit,
		webagent.OperationContentFetch,
		stage,
		"headed_rendered_content",
		target,
		cleanup,
		nil,
		code,
		errClass,
		message,
		"",
		data,
		next,
	)
}

func sortContentTargets(targets []ContentTarget) {
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Course.Key == targets[j].Course.Key {
			return targets[i].Chapter.ChapterID <
				targets[j].Chapter.ChapterID
		}
		return targets[i].Course.Key < targets[j].Course.Key
	})
}
