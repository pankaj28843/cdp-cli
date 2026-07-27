package alex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

const (
	Origin                    = "https://bytebytego.com"
	MyCoursesURL              = Origin + "/my-courses"
	ChatURL                   = Origin + "/api/chat"
	CoursesRoot               = Origin + "/courses"
	DefaultCourseID           = "coding-patterns"
	DefaultChapterID          = "two-pointers/introduction-to-two-pointers"
	AuthTemplateSchemaVersion = "alex-auth-template/v1"
	CatalogSchemaVersion      = "alex-catalog/v1"
	ContentSchemaVersion      = "alex-content/v1"
	AskRecordSchemaVersion    = "alex-ask-record/v1"
	RelativeTemplatePath      = "webagent/alex/request-template.json"
	RelativeCatalogPath       = "webagent/alex/catalog.json"
	RelativeContentRoot       = "webagent/alex/content"
	RelativeAskRecordRoot     = "webagent/alex/operations"
	DefaultAuthTTL            = time.Hour
	DefaultCatalogTTL         = 24 * time.Hour
	MaxPromptCharacters       = 18_000
)

var (
	slugPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,511}$`)
	runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type RequestBody struct {
	Chapter   string    `json:"chapter"`
	Course    string    `json:"course"`
	ChapterID string    `json:"chapterId"`
	CourseID  string    `json:"courseId"`
	Messages  []Message `json:"messages"`
}

type RequestTemplate struct {
	SchemaVersion    string            `json:"schema_version"`
	Method           string            `json:"method"`
	URL              string            `json:"url"`
	Headers          map[string]string `json:"headers"`
	Cookies          map[string]string `json:"cookies"`
	BrowserUserAgent string            `json:"browser_user_agent"`
	Body             RequestBody       `json:"body"`
	CapturedAt       string            `json:"captured_at"`
	Source           string            `json:"source"`
}

type Course struct {
	Key            string `json:"key"`
	Title          string `json:"title"`
	RootPath       string `json:"root_path"`
	DefaultChapter string `json:"default_chapter"`
	Lessons        *int   `json:"lessons,omitempty"`
	Students       *int   `json:"students,omitempty"`
	Authors        string `json:"authors,omitempty"`
	LastModified   string `json:"last_modified,omitempty"`
}

type Chapter struct {
	CourseKey string `json:"course_key"`
	ChapterID string `json:"chapter_id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Section   string `json:"section,omitempty"`
}

type Catalog struct {
	SchemaVersion string               `json:"schema_version"`
	RefreshedAt   string               `json:"refreshed_at"`
	Courses       map[string]Course    `json:"courses"`
	Chapters      map[string][]Chapter `json:"chapters"`
}

type Content struct {
	SchemaVersion string   `json:"schema_version"`
	CourseKey     string   `json:"course_key"`
	ChapterID     string   `json:"chapter_id"`
	URL           string   `json:"url"`
	Headings      []string `json:"headings"`
	Text          string   `json:"text"`
	FetchedAt     string   `json:"fetched_at"`
}

type AskRecord struct {
	SchemaVersion     string `json:"schema_version"`
	RunID             string `json:"run_id"`
	PromptFingerprint string `json:"prompt_fingerprint"`
	CourseID          string `json:"course_id"`
	ChapterID         string `json:"chapter_id"`
	State             string `json:"state"`
	AttemptCount      int    `json:"attempt_count"`
	RawInputCount     int    `json:"raw_input_count"`
	PendingPersisted  bool   `json:"pending_persisted"`
	UpdatedAt         string `json:"updated_at"`
}

type AuthStatus struct {
	SchemaVersion string `json:"schema_version"`
	State         string `json:"state"`
	Ready         bool   `json:"ready"`
	Stale         bool   `json:"stale"`
	StatePath     string `json:"state_path"`
	CapturedAt    string `json:"captured_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type CatalogStatus struct {
	SchemaVersion string `json:"schema_version"`
	State         string `json:"state"`
	Ready         bool   `json:"ready"`
	Stale         bool   `json:"stale"`
	StatePath     string `json:"state_path"`
	RefreshedAt   string `json:"refreshed_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	CourseCount   int    `json:"course_count"`
	ChapterCount  int    `json:"chapter_count"`
	Reason        string `json:"reason,omitempty"`
}

type Store struct {
	templatePath  string
	catalogPath   string
	contentRoot   string
	askRecordRoot string
}

func NewStore(stateDir string) (*Store, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("Ask Alex state directory is required")
	}
	return &Store{
		templatePath: filepath.Join(
			stateDir,
			filepath.FromSlash(RelativeTemplatePath),
		),
		catalogPath: filepath.Join(
			stateDir,
			filepath.FromSlash(RelativeCatalogPath),
		),
		contentRoot: filepath.Join(
			stateDir,
			filepath.FromSlash(RelativeContentRoot),
		),
		askRecordRoot: filepath.Join(
			stateDir,
			filepath.FromSlash(RelativeAskRecordRoot),
		),
	}, nil
}

func (s *Store) SaveTemplate(
	ctx context.Context,
	template RequestTemplate,
) error {
	if s == nil || s.templatePath == "" {
		return fmt.Errorf("Ask Alex auth store is not configured")
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("validate Ask Alex request template: %w", err)
	}
	return saveOwnerJSON(ctx, s.templatePath, template)
}

func (s *Store) LoadTemplate(
	ctx context.Context,
) (RequestTemplate, error) {
	if s == nil || s.templatePath == "" {
		return RequestTemplate{}, fmt.Errorf(
			"Ask Alex auth store is not configured",
		)
	}
	var template RequestTemplate
	if err := loadOwnerJSON(ctx, s.templatePath, &template); err != nil {
		return RequestTemplate{}, err
	}
	if err := template.Validate(); err != nil {
		return RequestTemplate{}, fmt.Errorf(
			"validate Ask Alex request template: %w",
			err,
		)
	}
	return template, nil
}

func (s *Store) SaveCatalog(ctx context.Context, catalog Catalog) error {
	if s == nil || s.catalogPath == "" {
		return fmt.Errorf("Ask Alex catalog store is not configured")
	}
	if err := catalog.Validate(); err != nil {
		return fmt.Errorf("validate Ask Alex catalog: %w", err)
	}
	return saveOwnerJSON(ctx, s.catalogPath, catalog)
}

func (s *Store) LoadCatalog(ctx context.Context) (Catalog, error) {
	if s == nil || s.catalogPath == "" {
		return Catalog{}, fmt.Errorf("Ask Alex catalog store is not configured")
	}
	var catalog Catalog
	if err := loadOwnerJSON(ctx, s.catalogPath, &catalog); err != nil {
		return Catalog{}, err
	}
	if err := catalog.Validate(); err != nil {
		return Catalog{}, fmt.Errorf("validate Ask Alex catalog: %w", err)
	}
	return catalog, nil
}

func (s *Store) SaveContent(ctx context.Context, content Content) error {
	if s == nil || s.contentRoot == "" {
		return fmt.Errorf("Ask Alex content store is not configured")
	}
	if err := content.Validate(); err != nil {
		return fmt.Errorf("validate Ask Alex content: %w", err)
	}
	return saveOwnerJSON(
		ctx,
		filepath.Join(
			s.contentRoot,
			content.CourseKey,
			contentFileName(content.ChapterID),
		),
		content,
	)
}

func (s *Store) SaveAskRecord(
	ctx context.Context,
	record AskRecord,
) error {
	if s == nil || s.askRecordRoot == "" {
		return fmt.Errorf("Ask Alex operation store is not configured")
	}
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate Ask Alex operation record: %w", err)
	}
	return saveOwnerJSON(
		ctx,
		filepath.Join(s.askRecordRoot, record.RunID+".json"),
		record,
	)
}

func (s *Store) LoadAskRecord(
	ctx context.Context,
	runID string,
) (AskRecord, error) {
	if s == nil || s.askRecordRoot == "" {
		return AskRecord{}, fmt.Errorf("Ask Alex operation store is not configured")
	}
	if !runIDPattern.MatchString(runID) {
		return AskRecord{}, fmt.Errorf("Ask Alex operation run id is invalid")
	}
	var record AskRecord
	if err := loadOwnerJSON(
		ctx,
		filepath.Join(s.askRecordRoot, runID+".json"),
		&record,
	); err != nil {
		return AskRecord{}, err
	}
	if err := record.Validate(); err != nil {
		return AskRecord{}, fmt.Errorf("validate Ask Alex operation record: %w", err)
	}
	if record.RunID != runID {
		return AskRecord{}, fmt.Errorf("Ask Alex operation identity does not match the requested run")
	}
	return record, nil
}

func (s *Store) AuthStatus(
	ctx context.Context,
	now time.Time,
	ttl time.Duration,
) AuthStatus {
	status := AuthStatus{
		SchemaVersion: AuthTemplateSchemaVersion,
		State:         "missing",
		StatePath:     RelativeTemplatePath,
		Reason:        "auth request template is not present",
	}
	if ttl <= 0 {
		ttl = DefaultAuthTTL
	}
	template, err := s.LoadTemplate(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return status
		}
		status.State = "invalid"
		status.Reason = "auth request template failed owner-only validation"
		return status
	}
	capturedAt, _ := time.Parse(time.RFC3339Nano, template.CapturedAt)
	expiresAt := capturedAt.Add(ttl)
	status.CapturedAt = capturedAt.Format(time.RFC3339Nano)
	status.ExpiresAt = expiresAt.Format(time.RFC3339Nano)
	status.Stale = !now.UTC().Before(expiresAt)
	switch {
	case capturedAt.After(now.UTC().Add(5 * time.Minute)):
		status.State = "invalid"
		status.Reason = "capture time is unexpectedly in the future"
	case status.Stale:
		status.State = "expired"
		status.Reason = "auth evidence exceeded its freshness window"
	default:
		status.State = "ready"
		status.Ready = true
		status.Reason = ""
	}
	return status
}

func (s *Store) CatalogStatus(
	ctx context.Context,
	now time.Time,
	ttl time.Duration,
) CatalogStatus {
	status := CatalogStatus{
		SchemaVersion: CatalogSchemaVersion,
		State:         "missing",
		StatePath:     RelativeCatalogPath,
		Reason:        "dynamic catalog is not present",
	}
	if ttl <= 0 {
		ttl = DefaultCatalogTTL
	}
	catalog, err := s.LoadCatalog(ctx)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return status
		}
		status.State = "invalid"
		status.Reason = "dynamic catalog failed owner-only validation"
		return status
	}
	refreshedAt, _ := time.Parse(time.RFC3339Nano, catalog.RefreshedAt)
	expiresAt := refreshedAt.Add(ttl)
	status.RefreshedAt = refreshedAt.Format(time.RFC3339Nano)
	status.ExpiresAt = expiresAt.Format(time.RFC3339Nano)
	status.CourseCount = len(catalog.Courses)
	status.ChapterCount = catalog.ChapterCount()
	status.Stale = !now.UTC().Before(expiresAt)
	switch {
	case refreshedAt.After(now.UTC().Add(5 * time.Minute)):
		status.State = "invalid"
		status.Reason = "catalog refresh time is unexpectedly in the future"
	case status.Stale:
		status.State = "expired"
		status.Reason = "dynamic catalog exceeded its freshness window"
	default:
		status.State = "ready"
		status.Ready = true
		status.Reason = ""
	}
	return status
}

func (t RequestTemplate) Validate() error {
	if t.SchemaVersion != AuthTemplateSchemaVersion {
		return fmt.Errorf(
			"schema_version must be %q",
			AuthTemplateSchemaVersion,
		)
	}
	if t.Method != "POST" || t.URL != ChatURL {
		return fmt.Errorf("method and url must preserve the Ask Alex chat shape")
	}
	if len(t.Headers) < 4 || len(t.Headers) > 64 {
		return fmt.Errorf("headers must contain between 4 and 64 entries")
	}
	requiredHeaders := map[string]string{
		"content-type": "application/json",
		"origin":       Origin,
	}
	for name, expected := range requiredHeaders {
		if t.Headers[name] != expected {
			return fmt.Errorf("required replay header %q is invalid", name)
		}
	}
	if strings.TrimSpace(t.Headers["x-csrf-token"]) == "" {
		return fmt.Errorf("x-csrf-token is required")
	}
	referer, err := url.Parse(t.Headers["referer"])
	if err != nil || referer.Scheme != "https" ||
		referer.Host != "bytebytego.com" {
		return fmt.Errorf("referer must be a ByteByteGo HTTPS URL")
	}
	if len(t.Cookies) == 0 || len(t.Cookies) > 256 {
		return fmt.Errorf("cookies must contain between 1 and 256 entries")
	}
	if strings.TrimSpace(t.Cookies["token"]) == "" ||
		strings.TrimSpace(t.Cookies["csrf-token"]) == "" {
		return fmt.Errorf("token and csrf-token cookies are required")
	}
	for name, value := range t.Headers {
		if err := validatePrivateValue("header name", name, 1024); err != nil {
			return err
		}
		if err := validatePrivateValue("header value", value, 64<<10); err != nil {
			return err
		}
	}
	for name, value := range t.Cookies {
		if err := validatePrivateValue("cookie name", name, 1024); err != nil {
			return err
		}
		if err := validatePrivateValue("cookie value", value, 64<<10); err != nil {
			return err
		}
	}
	if strings.TrimSpace(t.BrowserUserAgent) == "" ||
		len(t.BrowserUserAgent) > 4096 {
		return fmt.Errorf("browser_user_agent is required and bounded")
	}
	if _, err := time.Parse(time.RFC3339Nano, t.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339")
	}
	if t.Source != "headed-cdp-auth+established-api-chat-v1" {
		return fmt.Errorf("source is not an accepted Ask Alex observation")
	}
	if len(t.Body.Messages) != 1 ||
		t.Body.Messages[0].Role != "user" ||
		t.Body.Messages[0].Content != "" ||
		t.Body.Chapter != "" ||
		t.Body.Course != "" ||
		t.Body.ChapterID != "" ||
		t.Body.CourseID != "" {
		return fmt.Errorf(
			"stored request body must retain the empty variable template",
		)
	}
	return nil
}

func (c Catalog) Validate() error {
	if c.SchemaVersion != CatalogSchemaVersion {
		return fmt.Errorf("schema_version must be %q", CatalogSchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339Nano, c.RefreshedAt); err != nil {
		return fmt.Errorf("refreshed_at must be RFC3339")
	}
	if len(c.Courses) == 0 || len(c.Courses) > 128 {
		return fmt.Errorf("courses must contain between 1 and 128 entries")
	}
	if c.Chapters == nil {
		return fmt.Errorf("chapters map is required")
	}
	total := 0
	for key, course := range c.Courses {
		if key != course.Key {
			return fmt.Errorf("course map key does not match course key")
		}
		if err := course.Validate(); err != nil {
			return err
		}
		chapters, found := c.Chapters[key]
		if !found {
			return fmt.Errorf("course %q is missing its chapter list", key)
		}
		seen := map[string]struct{}{}
		for _, chapter := range chapters {
			if err := chapter.Validate(course); err != nil {
				return err
			}
			if _, duplicate := seen[chapter.ChapterID]; duplicate {
				return fmt.Errorf("chapter ids must be unique within a course")
			}
			seen[chapter.ChapterID] = struct{}{}
			total++
		}
	}
	if total > 5000 {
		return fmt.Errorf("catalog chapter count exceeds 5000")
	}
	for key := range c.Chapters {
		if _, found := c.Courses[key]; !found {
			return fmt.Errorf("chapter map contains an unknown course")
		}
	}
	return nil
}

func (c Course) Validate() error {
	if err := validateSlug("course key", c.Key); err != nil {
		return err
	}
	if err := validatePublicString("course title", c.Title, 512); err != nil {
		return err
	}
	if c.RootPath != "/courses/"+c.Key {
		return fmt.Errorf("course root path does not match its key")
	}
	if !strings.HasPrefix(
		c.DefaultChapter,
		c.RootPath+"/",
	) {
		return fmt.Errorf("default chapter must be below the course root")
	}
	if c.Lessons != nil && (*c.Lessons < 0 || *c.Lessons > 10000) {
		return fmt.Errorf("course lesson count is invalid")
	}
	if c.Students != nil && (*c.Students < 0 || *c.Students > 1_000_000_000) {
		return fmt.Errorf("course student count is invalid")
	}
	return nil
}

func (c Chapter) Validate(course Course) error {
	if c.CourseKey != course.Key {
		return fmt.Errorf("chapter course key does not match its course")
	}
	if err := validateSlug("chapter id", c.ChapterID); err != nil {
		return err
	}
	if err := validatePublicString("chapter title", c.Title, 1024); err != nil {
		return err
	}
	parsed, err := url.Parse(c.URL)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host != "bytebytego.com" ||
		parsed.Path != course.RootPath+"/"+c.ChapterID ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return fmt.Errorf("chapter URL does not match its exact catalog identity")
	}
	return nil
}

func (c Content) Validate() error {
	if c.SchemaVersion != ContentSchemaVersion {
		return fmt.Errorf("schema_version must be %q", ContentSchemaVersion)
	}
	if err := validateSlug("course key", c.CourseKey); err != nil {
		return err
	}
	if err := validateSlug("chapter id", c.ChapterID); err != nil {
		return err
	}
	if len(c.Text) == 0 || len(c.Text) > 2<<20 {
		return fmt.Errorf("content text must be non-empty and at most 2 MiB")
	}
	if len(c.Headings) > 512 {
		return fmt.Errorf("content headings exceed 512 entries")
	}
	if _, err := time.Parse(time.RFC3339Nano, c.FetchedAt); err != nil {
		return fmt.Errorf("fetched_at must be RFC3339")
	}
	return nil
}

func (r AskRecord) Validate() error {
	if r.SchemaVersion != AskRecordSchemaVersion {
		return fmt.Errorf("schema_version must be %q", AskRecordSchemaVersion)
	}
	if !runIDPattern.MatchString(r.RunID) ||
		len(r.PromptFingerprint) != 64 {
		return fmt.Errorf("operation identity is invalid")
	}
	if _, err := hex.DecodeString(r.PromptFingerprint); err != nil {
		return fmt.Errorf("prompt fingerprint must be SHA-256 hex")
	}
	if err := validateSlug("course id", r.CourseID); err != nil {
		return err
	}
	if err := validateSlug("chapter id", r.ChapterID); err != nil {
		return err
	}
	switch r.State {
	case "action_pending":
		if r.AttemptCount != 1 ||
			r.RawInputCount != 0 ||
			!r.PendingPersisted {
			return fmt.Errorf("pending operation evidence is contradictory")
		}
	case "performed", "unknown":
		if r.AttemptCount != 1 ||
			r.RawInputCount != 1 ||
			!r.PendingPersisted {
			return fmt.Errorf("attempted operation evidence is contradictory")
		}
	default:
		return fmt.Errorf("operation state is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.UpdatedAt); err != nil {
		return fmt.Errorf("updated_at must be RFC3339")
	}
	return nil
}

func (c Catalog) ChapterCount() int {
	count := 0
	for _, chapters := range c.Chapters {
		count += len(chapters)
	}
	return count
}

func (c Catalog) SortedCourses() []Course {
	courses := make([]Course, 0, len(c.Courses))
	for _, course := range c.Courses {
		courses = append(courses, course)
	}
	sort.Slice(courses, func(i, j int) bool {
		if courses[i].Title == courses[j].Title {
			return courses[i].Key < courses[j].Key
		}
		return courses[i].Title < courses[j].Title
	})
	return courses
}

func (c Catalog) Resolve(
	courseID string,
	chapterID string,
) (Course, Chapter, error) {
	course, found := c.Courses[courseID]
	if !found {
		return Course{}, Chapter{}, fmt.Errorf(
			"unknown dynamic course key %q",
			courseID,
		)
	}
	for _, chapter := range c.Chapters[courseID] {
		if chapter.ChapterID == chapterID {
			return course, chapter, nil
		}
	}
	return Course{}, Chapter{}, fmt.Errorf(
		"unknown dynamic chapter id %q for course %q",
		chapterID,
		courseID,
	)
}

func (t RequestTemplate) WithInput(
	prompt string,
	course Course,
	chapter Chapter,
) RequestTemplate {
	updated := t
	updated.Headers = cloneStrings(t.Headers)
	updated.Cookies = cloneStrings(t.Cookies)
	updated.Body = RequestBody{
		Chapter:   chapter.Title,
		Course:    course.Title,
		ChapterID: chapter.ChapterID,
		CourseID:  course.Key,
		Messages: []Message{{
			Role:    "user",
			Content: prompt,
		}},
	}
	return updated
}

func contentFileName(chapterID string) string {
	sum := sha256.Sum256([]byte(chapterID))
	return hex.EncodeToString(sum[:]) + ".json"
}

func validateSlug(label string, value string) error {
	if !slugPattern.MatchString(value) ||
		path.Clean(value) != value ||
		strings.HasPrefix(value, "/") ||
		strings.Contains(value, "//") {
		return fmt.Errorf("%s is not a safe relative slug", label)
	}
	return nil
}

func validatePublicString(label string, value string, maximum int) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maximum ||
		strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s is required and bounded", label)
	}
	return nil
}

func validatePrivateValue(label, value string, maximum int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", label)
	}
	if len(value) > maximum {
		return fmt.Errorf("%s exceeds its bound", label)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s contains unsupported control characters", label)
	}
	return nil
}

func cloneStrings(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func saveOwnerJSON(ctx context.Context, filePath string, value any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal owner-only JSON: %w", err)
	}
	data = append(data, '\n')
	return artifacts.WithOwnerOnlyFileLock(
		ctx,
		filePath+".lock",
		func() error {
			return artifacts.WriteOwnerOnlyFileAtomic(filePath, data)
		},
	)
}

func loadOwnerJSON(ctx context.Context, filePath string, target any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	data, err := artifacts.ReadOwnerOnlyFile(filePath)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("parse owner-only JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return fmt.Errorf("parse trailing owner-only JSON: %w", err)
		}
		return fmt.Errorf("owner-only JSON contains trailing data")
	}
	return nil
}
