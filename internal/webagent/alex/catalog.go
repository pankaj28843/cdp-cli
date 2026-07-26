package alex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	CatalogRefreshSchemaVersion = "alex-catalog-refresh/v1"
	CoursesListSchemaVersion    = "alex-courses-list/v1"
	ChaptersListSchemaVersion   = "alex-chapters-list/v1"
	maxCatalogScripts           = 64
	maxCatalogScriptBytes       = 4 << 20
	maxCatalogTotalBytes        = 32 << 20
)

var courseBlockPattern = regexp.MustCompile(
	`"([^"]+)":\{title:"([^"]+)",authors:"([^"]*)",` +
		`claimCodes:\[[^\]]*\],key:([A-Za-z_$][A-Za-z0-9_$]*),defaultChapter:` +
		"`" + "\\$\\{t\\}/\\$\\{([A-Za-z_$][A-Za-z0-9_$]*)\\}/([^`]+)" + "`" +
		`,` +
		`rootPath:` + "`" + `\$\{t\}/\$\{([A-Za-z_$][A-Za-z0-9_$]*)\}` + "`" +
		`,lessons:([0-9]+),students:([^,}]+),showChapter:[^,}]+,lastModified:"([^"]+)"`,
)

type CatalogRefreshConfig struct {
	BrowserConfig
	Store        *Store
	Timeout      time.Duration
	PollInterval time.Duration
	Now          func() time.Time
}

type CatalogRefreshData struct {
	SchemaVersion string   `json:"schema_version"`
	State         string   `json:"state"`
	StatePath     string   `json:"state_path"`
	CourseCount   int      `json:"course_count"`
	ChapterCount  int      `json:"chapter_count"`
	ScriptCount   int      `json:"script_count"`
	Warnings      []string `json:"warnings"`
	RefreshedAt   string   `json:"refreshed_at,omitempty"`
}

type CoursesListData struct {
	SchemaVersion string   `json:"schema_version"`
	Courses       []Course `json:"courses"`
}

type ChaptersListData struct {
	SchemaVersion string    `json:"schema_version"`
	Course        string    `json:"course"`
	Chapters      []Chapter `json:"chapters"`
}

type courseDiscovery struct {
	URL        string `json:"url"`
	BodyReady  bool   `json:"body_ready"`
	CardTitles []string
	ScriptURLs []string `json:"script_urls"`
}

func (d *courseDiscovery) UnmarshalJSON(data []byte) error {
	var wire struct {
		URL        string   `json:"url"`
		BodyReady  bool     `json:"body_ready"`
		CardTitles []string `json:"card_titles"`
		ScriptURLs []string `json:"script_urls"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	d.URL = wire.URL
	d.BodyReady = wire.BodyReady
	d.CardTitles = wire.CardTitles
	d.ScriptURLs = wire.ScriptURLs
	return nil
}

type scriptObservation struct {
	OK          bool   `json:"ok"`
	Status      int    `json:"status"`
	Text        string `json:"text"`
	TotalLength int    `json:"total_length"`
}

type chapterDiscovery struct {
	URL               string             `json:"url"`
	TOC               []chapterCandidate `json:"toc"`
	Items             []chapterCandidate `json:"items"`
	ArticleTextLength int                `json:"article_text_length"`
}

type chapterCandidate struct {
	Course    string `json:"course"`
	ChapterID string `json:"chapter_id"`
	Title     string `json:"title"`
	Section   string `json:"section"`
	Href      string `json:"href"`
}

func RefreshCatalog(
	ctx context.Context,
	config CatalogRefreshConfig,
) webagent.Result {
	runID := webagent.NewRunID()
	data := CatalogRefreshData{
		SchemaVersion: CatalogRefreshSchemaVersion,
		State:         "blocked",
		StatePath:     RelativeCatalogPath,
		Warnings:      []string{},
	}
	if config.Store == nil {
		return operationFailure(
			runID,
			config.BuildCommit,
			webagent.OperationCatalogRefresh,
			webagent.StagePlanned,
			"headed_dynamic_catalog",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil,
			"alex_state_unavailable",
			"internal",
			"Ask Alex owner-only catalog state is unavailable",
			"",
			data,
			[]string{"cdp doctor --json"},
		)
	}
	if config.Timeout <= 0 {
		config.Timeout = 2 * time.Minute
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationCatalogRefresh,
		"about:blank",
		"headed_dynamic_catalog",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			if err := preparePage(ctx, config.Client, session, MyCoursesURL); err != nil {
				return catalogFailure(
					runID, config, webagent.StageAttached, target, pending,
					"alex_catalog_prepare_failed", "connection",
					"Ask Alex catalog could not prepare the exact headed target",
					data, cleanupCommands(runID, pending),
				)
			}
			var discovery courseDiscovery
			_, err := pollUntil(
				ctx,
				30*time.Second,
				config.PollInterval,
				func() (bool, error) {
					if err := observeCoursePage(ctx, session, &discovery); err != nil {
						return false, err
					}
					return discovery.URL == MyCoursesURL &&
						discovery.BodyReady &&
						len(discovery.ScriptURLs) > 0, nil
				},
			)
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return catalogFailure(
					runID, config, webagent.StageAttached, target, pending,
					"alex_catalog_page_not_ready", "provider",
					"ByteByteGo my-courses did not expose bounded dynamic discovery evidence",
					data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return catalogFailure(
					runID, config, webagent.StageAttached, target, pending,
					"alex_catalog_prepare_state_failed", "internal",
					"Ask Alex catalog prepared state could not be persisted",
					data, cleanupCommands(runID, pending),
				)
			}

			scripts := validCatalogScriptURLs(discovery.ScriptURLs)
			data.ScriptCount = len(scripts)
			courses, err := discoverCoursesFromScripts(
				ctx,
				session,
				scripts,
				discovery.CardTitles,
			)
			if err != nil || len(courses) == 0 {
				_ = lease.MarkIncomplete(context.Background())
				return catalogFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"alex_catalog_courses_not_discovered", "provider",
					"ByteByteGo visible course cards could not be matched to current dynamic course metadata",
					data,
					[]string{
						"Review docs/cdp-headed-self-healing.md.",
						"cdp workflow agent alex catalog refresh --json",
					},
				)
			}

			catalog := Catalog{
				SchemaVersion: CatalogSchemaVersion,
				Courses:       courses,
				Chapters:      make(map[string][]Chapter, len(courses)),
			}
			for _, course := range catalog.SortedCourses() {
				chapters, chapterErr := discoverChapters(
					ctx,
					session,
					course,
					config.PollInterval,
				)
				if chapterErr != nil || len(chapters) == 0 {
					catalog.Chapters[course.Key] = []Chapter{}
					data.Warnings = append(
						data.Warnings,
						fmt.Sprintf("no chapters discovered for %s", course.Key),
					)
					continue
				}
				catalog.Chapters[course.Key] = chapters
			}
			now := nowFor(config.Now)
			catalog.RefreshedAt = now.Format(time.RFC3339Nano)
			if err := catalog.Validate(); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return catalogFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"alex_catalog_invalid", "provider",
					"Discovered ByteByteGo catalog violated the bounded dynamic catalog contract",
					data, cleanupCommands(runID, pending),
				)
			}
			if catalog.ChapterCount() == 0 {
				_ = lease.MarkIncomplete(context.Background())
				return catalogFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"alex_catalog_chapters_not_discovered", "provider",
					"ByteByteGo dynamic course discovery succeeded but no chapter TOC was proven",
					data,
					[]string{
						"Review docs/cdp-headed-self-healing.md.",
						"cdp workflow agent alex catalog refresh --json",
					},
				)
			}
			if err := config.Store.SaveCatalog(ctx, catalog); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return catalogFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"alex_catalog_state_write_failed", "internal",
					"Ask Alex dynamic catalog could not be persisted to owner-only state",
					data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkTerminal(ctx); err != nil {
				return catalogFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"alex_catalog_terminal_state_failed", "internal",
					"Ask Alex catalog terminal state could not be persisted",
					data, cleanupCommands(runID, pending),
				)
			}
			data.State = "ready"
			data.CourseCount = len(catalog.Courses)
			data.ChapterCount = catalog.ChapterCount()
			data.RefreshedAt = catalog.RefreshedAt
			return operationSuccess(
				runID,
				config.BuildCommit,
				webagent.OperationCatalogRefresh,
				webagent.StateReady,
				webagent.StageObserveTerminal,
				"headed_dynamic_catalog",
				target,
				pending,
				nil,
				data,
				[]string{
					"cdp workflow agent alex courses list --json",
					"cdp workflow agent alex chapters list --json",
				},
			)
		},
	)
}

func CatalogState(
	ctx context.Context,
	store *Store,
	now time.Time,
	buildCommit string,
) webagent.Result {
	if store == nil {
		return unavailableMetadata(
			webagent.OperationCatalogStatus,
			buildCommit,
			"alex_state_unavailable",
			"Ask Alex owner-only catalog state is unavailable",
			map[string]any{"schema_version": CatalogSchemaVersion},
		)
	}
	status := store.CatalogStatus(ctx, now, DefaultCatalogTTL)
	result := webagent.NewMetadataResult(
		webagent.ProviderAlex,
		webagent.OperationCatalogStatus,
		status,
		buildCommit,
		[]string{"cdp workflow agent alex catalog refresh --json"},
	)
	result.Evidence.ReadMode = "owner_only_local_state"
	return result
}

func ListCourses(
	ctx context.Context,
	store *Store,
	buildCommit string,
) webagent.Result {
	if store == nil {
		return unavailableMetadata(
			webagent.OperationCoursesList,
			buildCommit,
			"alex_state_unavailable",
			"Ask Alex owner-only catalog state is unavailable",
			CoursesListData{
				SchemaVersion: CoursesListSchemaVersion,
				Courses:       []Course{},
			},
		)
	}
	catalog, err := store.LoadCatalog(ctx)
	if err != nil {
		return unavailableMetadata(
			webagent.OperationCoursesList,
			buildCommit,
			"alex_catalog_unavailable",
			"Ask Alex dynamic catalog is unavailable",
			CoursesListData{
				SchemaVersion: CoursesListSchemaVersion,
				Courses:       []Course{},
			},
		)
	}
	result := webagent.NewMetadataResult(
		webagent.ProviderAlex,
		webagent.OperationCoursesList,
		CoursesListData{
			SchemaVersion: CoursesListSchemaVersion,
			Courses:       catalog.SortedCourses(),
		},
		buildCommit,
		[]string{"cdp workflow agent alex chapters list --json"},
	)
	result.Evidence.ReadMode = "owner_only_dynamic_catalog"
	return result
}

func ListChapters(
	ctx context.Context,
	store *Store,
	courseID string,
	buildCommit string,
) webagent.Result {
	data := ChaptersListData{
		SchemaVersion: ChaptersListSchemaVersion,
		Course:        courseID,
		Chapters:      []Chapter{},
	}
	if store == nil {
		return unavailableMetadata(
			webagent.OperationChaptersList,
			buildCommit,
			"alex_state_unavailable",
			"Ask Alex owner-only catalog state is unavailable",
			data,
		)
	}
	catalog, err := store.LoadCatalog(ctx)
	if err != nil {
		return unavailableMetadata(
			webagent.OperationChaptersList,
			buildCommit,
			"alex_catalog_unavailable",
			"Ask Alex dynamic catalog is unavailable",
			data,
		)
	}
	if _, found := catalog.Courses[courseID]; !found {
		return operationFailure(
			webagent.NewRunID(),
			buildCommit,
			webagent.OperationChaptersList,
			webagent.StageMetadata,
			"owner_only_dynamic_catalog",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil,
			"alex_unknown_course",
			"usage",
			"Ask Alex course key is absent from the dynamic catalog",
			"",
			data,
			[]string{"cdp workflow agent alex courses list --json"},
		)
	}
	data.Chapters = append([]Chapter(nil), catalog.Chapters[courseID]...)
	result := webagent.NewMetadataResult(
		webagent.ProviderAlex,
		webagent.OperationChaptersList,
		data,
		buildCommit,
		[]string{
			fmt.Sprintf(
				"cdp workflow agent alex content fetch --course %s --chapter-id <id> --json",
				courseID,
			),
		},
	)
	result.Evidence.ReadMode = "owner_only_dynamic_catalog"
	return result
}

func observeCoursePage(
	ctx context.Context,
	session *cdp.PageSession,
	target *courseDiscovery,
) error {
	return evaluateCourseDiscovery(ctx, session, target)
}

func evaluateCourseDiscovery(
	ctx context.Context,
	session *cdp.PageSession,
	target *courseDiscovery,
) error {
	evaluated, err := session.Evaluate(ctx, `(() => ({
	  url: location.href,
	  body_ready: Boolean(document.body),
	  card_titles: [...document.querySelectorAll('img[alt]')]
	    .map(img => ({
	      title: (img.alt || '').trim(),
	      src: img.currentSrc || img.src || ''
	    }))
	    .filter(item => item.title && item.src.includes('my-course'))
	    .map(item => item.title)
	    .slice(0, 256),
	  script_urls: [...document.scripts]
	    .map(script => {
	      try {
	        return new URL(script.src, location.href).href;
	      } catch {
	        return '';
	      }
	    })
	    .filter(Boolean)
	    .slice(0, 256)
	}))()`, true)
	if err != nil {
		return err
	}
	if evaluated.Exception != nil || len(evaluated.Object.Value) == 0 {
		return fmt.Errorf("course discovery evaluation failed")
	}
	if err := json.Unmarshal(evaluated.Object.Value, target); err != nil {
		return fmt.Errorf("decode course discovery")
	}
	return nil
}

func validCatalogScriptURLs(values []string) []string {
	seen := map[string]struct{}{}
	valid := make([]string, 0, len(values))
	for _, value := range values {
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil ||
			parsed.Scheme != "https" ||
			parsed.Host != "bytebytego.com" ||
			!strings.Contains(parsed.Path, "/_next/static/chunks/") ||
			parsed.Fragment != "" {
			continue
		}
		if parsed.RawQuery != "" {
			query := parsed.Query()
			if len(query) != 1 ||
				len(query["dpl"]) != 1 ||
				strings.TrimSpace(query.Get("dpl")) == "" {
				continue
			}
		}
		if _, found := seen[parsed.String()]; found {
			continue
		}
		seen[parsed.String()] = struct{}{}
		valid = append(valid, parsed.String())
		if len(valid) == maxCatalogScripts {
			break
		}
	}
	return valid
}

func discoverCoursesFromScripts(
	ctx context.Context,
	session *cdp.PageSession,
	scriptURLs []string,
	cardTitles []string,
) (map[string]Course, error) {
	courses := map[string]Course{}
	totalBytes := 0
	for _, scriptURL := range scriptURLs {
		literal, _ := json.Marshal(scriptURL)
		expression := fmt.Sprintf(`(async () => {
		  try {
		    const response = await fetch(%s, {credentials: 'same-origin'});
		    const text = await response.text();
		    return {
		      ok: response.ok,
		      status: response.status,
		      text: text.slice(0, %d),
		      total_length: text.length
		    };
		  } catch {
		    return {ok: false, status: 0, text: '', total_length: 0};
		  }
		})()`, literal, maxCatalogScriptBytes+1)
		evaluated, err := session.Evaluate(ctx, expression, true)
		if err != nil || evaluated.Exception != nil ||
			len(evaluated.Object.Value) == 0 {
			continue
		}
		var observation scriptObservation
		if json.Unmarshal(evaluated.Object.Value, &observation) != nil ||
			!observation.OK ||
			observation.Status != 200 ||
			observation.TotalLength > maxCatalogScriptBytes ||
			len(observation.Text) > maxCatalogScriptBytes {
			continue
		}
		totalBytes += len(observation.Text)
		if totalBytes > maxCatalogTotalBytes {
			return nil, fmt.Errorf("catalog script observation exceeded its total bound")
		}
		for key, course := range ParseCoursesFromScript(observation.Text) {
			courses[key] = course
		}
	}
	if len(cardTitles) == 0 {
		return courses, nil
	}
	visible := make(map[string]struct{}, len(cardTitles))
	for _, title := range cardTitles {
		if title = cleanText(title); title != "" {
			visible[title] = struct{}{}
		}
	}
	filtered := map[string]Course{}
	for key, course := range courses {
		if _, found := visible[course.Title]; found {
			filtered[key] = course
		}
	}
	return filtered, nil
}

func ParseCoursesFromScript(script string) map[string]Course {
	courses := map[string]Course{}
	if !strings.Contains(script, "defaultChapter") ||
		!strings.Contains(script, "rootPath") {
		return courses
	}
	for _, match := range courseBlockPattern.FindAllStringSubmatch(script, -1) {
		if len(match) != 11 ||
			match[4] != match[5] ||
			match[4] != match[7] {
			continue
		}
		key := match[1]
		lessons, err := strconv.Atoi(match[8])
		if err != nil {
			continue
		}
		students := parseJSNumber(match[9])
		course := Course{
			Key:            key,
			Title:          cleanText(match[2]),
			Authors:        cleanText(match[3]),
			RootPath:       "/courses/" + key,
			DefaultChapter: "/courses/" + key + "/" + strings.Trim(match[6], "/"),
			Lessons:        &lessons,
			Students:       students,
			LastModified:   cleanText(match[10]),
		}
		if course.Validate() == nil {
			courses[key] = course
		}
	}
	return courses
}

func discoverChapters(
	ctx context.Context,
	session *cdp.PageSession,
	course Course,
	pollInterval time.Duration,
) ([]Chapter, error) {
	rawURL := Origin + course.DefaultChapter
	if _, err := session.Navigate(ctx, rawURL); err != nil {
		return nil, err
	}
	var discovery chapterDiscovery
	_, err := pollUntil(
		ctx,
		30*time.Second,
		pollInterval,
		func() (bool, error) {
			if err := evaluateChapterDiscovery(ctx, session, &discovery); err != nil {
				return false, err
			}
			return discovery.URL == rawURL &&
				(len(discovery.TOC) > 0 ||
					len(discovery.Items) > 0 ||
					discovery.ArticleTextLength > 100), nil
		},
	)
	if err != nil {
		return nil, err
	}
	chapters := chaptersFromCandidates(course, discovery.TOC)
	if len(chapters) == 0 {
		chapters = chaptersFromCandidates(course, discovery.Items)
	}
	return chapters, nil
}

func evaluateChapterDiscovery(
	ctx context.Context,
	session *cdp.PageSession,
	target *chapterDiscovery,
) error {
	evaluated, err := session.Evaluate(ctx, `(() => {
	  let data = {};
	  try {
	    const text = document.querySelector('#__NEXT_DATA__')?.textContent || '';
	    data = text.trim() ? JSON.parse(text) : {};
	  } catch {
	    data = {};
	  }
	  const clean = value => String(value || '').trim().replace(/\s+/g, ' ');
	  const slug = value => Array.isArray(value)
	    ? value.map(part => String(part || '').replace(/^\/+|\/+$/g, '')).filter(Boolean).join('/')
	    : clean(value).replace(/^\/+|\/+$/g, '');
	  const toc = Array.isArray(data?.props?.pageProps?.toc)
	    ? data.props.pageProps.toc.slice(0, 5000).map(item => ({
	        course: clean(item?.course),
	        chapter_id: slug(item?.slug) || slug(item?.id),
	        title: clean(item?.tocLabel || item?.title),
	        section: clean(item?.subsection || item?.tocGroup),
	        href: ''
	      }))
	    : [];
	  const items = [...document.querySelectorAll('a[href]')]
	    .slice(0, 10000)
	    .map(link => ({
	      course: '',
	      chapter_id: '',
	      title: clean(link.innerText || link.textContent || link.title),
	      section: '',
	      href: link.href || ''
	    }))
	    .filter(item => item.title && item.href.includes('/courses/'));
	  return {
	    url: location.href,
	    toc,
	    items,
	    article_text_length: clean(document.querySelector('article')?.innerText).length
	  };
	})()`, true)
	if err != nil {
		return err
	}
	if evaluated.Exception != nil || len(evaluated.Object.Value) == 0 {
		return fmt.Errorf("chapter discovery evaluation failed")
	}
	if err := json.Unmarshal(evaluated.Object.Value, target); err != nil {
		return fmt.Errorf("decode chapter discovery")
	}
	return nil
}

func chaptersFromCandidates(
	course Course,
	candidates []chapterCandidate,
) []Chapter {
	seen := map[string]struct{}{}
	chapters := make([]Chapter, 0, len(candidates))
	prefix := course.RootPath + "/"
	for _, candidate := range candidates {
		if candidate.Course != "" && candidate.Course != course.Key {
			continue
		}
		chapterID := strings.Trim(candidate.ChapterID, "/")
		if chapterID == "" && candidate.Href != "" {
			parsed, err := url.Parse(candidate.Href)
			if err != nil ||
				parsed.Scheme != "https" ||
				parsed.Host != "bytebytego.com" ||
				!strings.HasPrefix(parsed.Path, prefix) ||
				parsed.RawQuery != "" ||
				parsed.Fragment != "" {
				continue
			}
			chapterID = strings.TrimPrefix(parsed.Path, prefix)
		}
		chapterID = strings.TrimPrefix(chapterID, "courses/"+course.Key+"/")
		chapterID = strings.TrimPrefix(chapterID, course.Key+"/")
		title := cleanText(candidate.Title)
		if validateSlug("chapter id", chapterID) != nil || title == "" {
			continue
		}
		if _, duplicate := seen[chapterID]; duplicate {
			continue
		}
		chapter := Chapter{
			CourseKey: course.Key,
			ChapterID: chapterID,
			Title:     title,
			URL:       Origin + prefix + chapterID,
			Section:   cleanText(candidate.Section),
		}
		if chapter.Validate(course) != nil {
			continue
		}
		seen[chapterID] = struct{}{}
		chapters = append(chapters, chapter)
	}
	return chapters
}

func parseJSNumber(value string) *int {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || number < 0 || number > 1_000_000_000 {
		return nil
	}
	parsed := int(number)
	return &parsed
}

func cleanText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func catalogFailure(
	runID string,
	config CatalogRefreshConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	code string,
	errClass string,
	message string,
	data CatalogRefreshData,
	next []string,
) webagent.Result {
	return operationFailure(
		runID,
		config.BuildCommit,
		webagent.OperationCatalogRefresh,
		stage,
		"headed_dynamic_catalog",
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

func unavailableMetadata(
	operation webagent.Operation,
	buildCommit string,
	code string,
	message string,
	data any,
) webagent.Result {
	result := operationFailure(
		webagent.NewRunID(),
		buildCommit,
		operation,
		webagent.StageMetadata,
		"owner_only_local_state",
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		nil,
		code,
		"internal",
		message,
		"",
		data,
		[]string{"cdp workflow agent alex catalog refresh --json"},
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func sortedCourseKeys(catalog Catalog) []string {
	keys := make([]string, 0, len(catalog.Courses))
	for key := range catalog.Courses {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
