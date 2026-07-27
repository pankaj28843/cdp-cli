package alex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AskSchemaVersion       = "alex-ask/v1"
	maxAskResponseBytes    = 4 << 20
	defaultAskTimeout      = 45 * time.Second
	defaultRateLimitWindow = 5 * time.Minute
)

type AskConfig struct {
	Store       *Store
	HTTPClient  *http.Client
	Timeout     time.Duration
	IncludeRaw  bool
	BuildCommit string
	Now         func() time.Time
}

type AskData struct {
	SchemaVersion     string         `json:"schema_version"`
	CourseKey         string         `json:"course_key"`
	ChapterID         string         `json:"chapter_id"`
	StatusCode        int            `json:"status_code"`
	Text              string         `json:"text"`
	ElapsedSeconds    float64        `json:"elapsed_seconds"`
	CompletionState   string         `json:"completion_state"`
	ReadMode          string         `json:"read_mode"`
	PromptCharacters  int            `json:"prompt_characters"`
	PromptFingerprint string         `json:"prompt_fingerprint,omitempty"`
	Raw               any            `json:"raw,omitempty"`
	Metadata          map[string]any `json:"metadata"`
}

func Ask(
	ctx context.Context,
	config AskConfig,
	prompt string,
	courseID string,
	chapterID string,
) (result webagent.Result) {
	runID := webagent.NewRunID()
	prompt = strings.TrimSpace(prompt)
	courseID = strings.TrimSpace(courseID)
	chapterID = strings.TrimSpace(chapterID)
	data := AskData{
		SchemaVersion:    AskSchemaVersion,
		CourseKey:        courseID,
		ChapterID:        chapterID,
		CompletionState:  "not_submitted",
		ReadMode:         "direct_http_replay",
		PromptCharacters: utf8.RuneCountInString(prompt),
		Metadata:         map[string]any{},
	}
	notPerformed := notPerformedAction()
	if prompt == "" {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_prompt_required", "usage",
			"Ask Alex prompt must not be empty", "", data,
			[]string{"cdp workflow agent alex ask --stdin --json"},
		)
	}
	if data.PromptCharacters > MaxPromptCharacters {
		data.Metadata["max_prompt_characters"] = MaxPromptCharacters
		data.Metadata["excess_characters"] =
			data.PromptCharacters - MaxPromptCharacters
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_prompt_too_long", "usage",
			"Ask Alex prompt exceeds the safe character limit", "", data,
			[]string{"Split the request into self-contained prompts below the limit."},
		)
	}
	if config.Store == nil {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_state_unavailable", "internal",
			"Ask Alex owner-only replay state is unavailable", "", data,
			[]string{"cdp workflow agent alex doctor --json"},
		)
	}
	now := nowFor(config.Now)
	auth := config.Store.AuthStatus(ctx, now, DefaultAuthTTL)
	if !auth.Ready {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_auth_"+auth.State, "auth",
			"Ask Alex auth request-template evidence is not ready before replay",
			"", data,
			[]string{"cdp workflow agent alex auth refresh --json"},
		)
	}
	catalogStatus := config.Store.CatalogStatus(
		ctx,
		now,
		DefaultCatalogTTL,
	)
	if !catalogStatus.Ready {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_catalog_"+catalogStatus.State, "capability",
			"Ask Alex dynamic catalog is not ready before replay",
			"", data,
			[]string{"cdp workflow agent alex catalog refresh --json"},
		)
	}
	template, err := config.Store.LoadTemplate(ctx)
	if err != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_auth_invalid", "auth",
			"Ask Alex request template failed owner-only validation",
			"", data,
			[]string{"cdp workflow agent alex auth refresh --json"},
		)
	}
	catalog, err := config.Store.LoadCatalog(ctx)
	if err != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_catalog_invalid", "capability",
			"Ask Alex dynamic catalog failed owner-only validation",
			"", data,
			[]string{"cdp workflow agent alex catalog refresh --json"},
		)
	}
	course, chapter, err := catalog.Resolve(courseID, chapterID)
	if err != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_context_not_discovered", "usage",
			"Ask Alex course and chapter must exactly match the dynamic catalog",
			"", data,
			[]string{
				"cdp workflow agent alex courses list --json",
				fmt.Sprintf(
					"cdp workflow agent alex chapters list --course %s --json",
					courseID,
				),
			},
		)
	}
	data.CourseKey = course.Key
	data.ChapterID = chapter.ChapterID
	data.PromptFingerprint = promptFingerprint(prompt)
	requestTemplate := template.WithInput(prompt, course, chapter)
	body, err := json.Marshal(requestTemplate.Body)
	if err != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_request_encode_failed", "internal",
			"Ask Alex replay body could not be encoded", "", data,
			[]string{"cdp workflow agent alex doctor --json"},
		)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		ChatURL,
		bytes.NewReader(body),
	)
	if err != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_request_build_failed", "internal",
			"Ask Alex replay request could not be built", "", data,
			[]string{"cdp workflow agent alex doctor --json"},
		)
	}
	for name, value := range requestTemplate.Headers {
		request.Header.Set(name, value)
	}
	cookieHeader, err := serializedCookieHeader(requestTemplate.Cookies)
	if err != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_cookie_shape_invalid", "auth",
			"Ask Alex captured cookie shape is unsafe for exact replay",
			"", data,
			[]string{"cdp workflow agent alex auth refresh --json"},
		)
	}
	request.Header.Set("Cookie", cookieHeader)

	var cooldownUntil time.Time

	pendingRecord := AskRecord{
		SchemaVersion:     AskRecordSchemaVersion,
		RunID:             runID,
		PromptFingerprint: data.PromptFingerprint,
		CourseID:          course.Key,
		ChapterID:         chapter.ChapterID,
		State:             "action_pending",
		AttemptCount:      1,
		RawInputCount:     0,
		PendingPersisted:  true,
		UpdatedAt:         now.Format(time.RFC3339Nano),
	}
	if err := config.Store.SaveAskRecord(ctx, pendingRecord); err != nil {
		return askFailure(
			runID, config, webagent.StagePlanned, notPerformed,
			"alex_action_pending_write_failed", "internal",
			"Ask Alex action_pending evidence could not be durably persisted before replay",
			"", data, []string{"cdp workflow agent alex doctor --json"},
		)
	}
	action := &webagent.ActionEvidence{
		Dispatch:         webagent.DispatchUnknown,
		AttemptCount:     1,
		RawInputCount:    1,
		RetrySafe:        false,
		PendingPersisted: true,
	}
	started := time.Now()
	client := config.HTTPClient
	if client == nil {
		timeout := config.Timeout
		if timeout <= 0 {
			timeout = defaultAskTimeout
		}
		client = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(
				*http.Request,
				[]*http.Request,
			) error {
				return http.ErrUseLastResponse
			},
		}
	}
	response, requestErr := client.Do(request)
	data.ElapsedSeconds = time.Since(started).Seconds()
	if requestErr != nil {
		pendingRecord.State = "unknown"
		pendingRecord.RawInputCount = 1
		pendingRecord.UpdatedAt = nowFor(config.Now).Format(time.RFC3339Nano)
		_ = config.Store.SaveAskRecord(context.Background(), pendingRecord)
		data.CompletionState = "dispatch_unknown"
		return askFailure(
			runID, config, webagent.StageActionDispatched, action,
			"alex_replay_unknown", "connection",
			"Ask Alex replay transport failed after dispatch; do not resubmit",
			"", data,
			[]string{"cdp workflow agent alex doctor --json"},
		)
	}
	defer response.Body.Close()
	action.Dispatch = webagent.DispatchPerformed
	pendingRecord.State = "performed"
	pendingRecord.RawInputCount = 1
	pendingRecord.UpdatedAt = nowFor(config.Now).Format(time.RFC3339Nano)
	if err := config.Store.SaveAskRecord(
		context.Background(),
		pendingRecord,
	); err != nil {
		data.StatusCode = response.StatusCode
		data.CompletionState = "performed_state_unavailable"
		return askFailure(
			runID, config, webagent.StageActionDispatched, action,
			"alex_performed_state_write_failed", "internal",
			"Ask Alex replay returned but performed evidence could not be persisted; do not resubmit",
			"", data,
			[]string{"cdp workflow agent alex doctor --json"},
		)
	}

	rawBody, err := io.ReadAll(io.LimitReader(
		response.Body,
		maxAskResponseBytes+1,
	))
	data.StatusCode = response.StatusCode
	data.Metadata["content_type"] = response.Header.Get("Content-Type")
	if err != nil {
		data.CompletionState = "response_read_failed"
		return askFailure(
			runID, config, webagent.StageAcknowledged, action,
			"alex_response_read_failed", "completion",
			"Ask Alex response could not be read after acknowledged HTTP dispatch; do not resubmit",
			"", data,
			[]string{"cdp workflow agent alex doctor --json"},
		)
	}
	if len(rawBody) > maxAskResponseBytes {
		data.CompletionState = "response_too_large"
		return askFailure(
			runID, config, webagent.StageAcknowledged, action,
			"alex_response_too_large", "completion",
			"Ask Alex response exceeded the bounded read limit; do not resubmit",
			"", data,
			[]string{"cdp workflow agent alex doctor --json"},
		)
	}
	parsed := parseAskResponse(rawBody)
	data.Text = extractAskText(parsed, string(rawBody))
	if config.IncludeRaw {
		data.Raw = parsed
	}
	if response.StatusCode == http.StatusTooManyRequests {
		cooldownUntil = retryAtFromHeader(
			response.Header.Get("Retry-After"),
			nowFor(config.Now),
		)
		if cooldownUntil.IsZero() {
			cooldownUntil = nowFor(config.Now).Add(
				defaultRateLimitWindow,
			)
		}
		data.CompletionState = "rate_limited"
		return askFailure(
			runID, config, webagent.StageAcknowledged, action,
			"alex_rate_limited", "provider",
			"ByteByteGo returned HTTP 429 after one request; this invocation was not retried",
			cooldownUntil.Format(time.RFC3339Nano),
			data,
			nil,
		)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data.CompletionState = "http_failure"
		return askFailure(
			runID, config, webagent.StageAcknowledged, action,
			"alex_http_failure", "provider",
			fmt.Sprintf(
				"ByteByteGo returned HTTP %d after one replay; the request was not retried",
				response.StatusCode,
			),
			"", data,
			[]string{"cdp workflow agent alex doctor --json"},
		)
	}
	if strings.TrimSpace(data.Text) == "" {
		data.CompletionState = "empty_response"
		return askFailure(
			runID, config, webagent.StageAcknowledged, action,
			"alex_empty_response", "completion",
			"Ask Alex returned HTTP success without a usable answer; do not resubmit",
			"", data,
			[]string{"cdp workflow agent alex doctor --json"},
		)
	}
	data.CompletionState = "terminal"
	return operationSuccess(
		runID,
		config.BuildCommit,
		webagent.OperationAsk,
		webagent.StateTerminal,
		webagent.StageAcknowledged,
		"direct_http_replay",
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		action,
		data,
		[]string{
			fmt.Sprintf(
				"cdp workflow agent alex content fetch --course %s --chapter-id %s --json",
				course.Key,
				chapter.ChapterID,
			),
		},
	)
}

func askFailure(
	runID string,
	config AskConfig,
	stage webagent.Stage,
	action *webagent.ActionEvidence,
	code string,
	errClass string,
	message string,
	retryAt string,
	data AskData,
	next []string,
) webagent.Result {
	return operationFailure(
		runID,
		config.BuildCommit,
		webagent.OperationAsk,
		stage,
		"direct_http_replay",
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		action,
		code,
		errClass,
		message,
		retryAt,
		data,
		next,
	)
}

func promptFingerprint(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

func serializedCookieHeader(cookies map[string]string) (string, error) {
	names := make([]string, 0, len(cookies))
	for name := range cookies {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		value := cookies[name]
		if !httpToken(name) ||
			value == "" ||
			strings.ContainsAny(value, ";\x00\r\n") {
			return "", fmt.Errorf("invalid captured cookie")
		}
		parts = append(parts, name+"="+value)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("captured cookies are empty")
	}
	return strings.Join(parts, "; "), nil
}

func httpToken(value string) bool {
	if value == "" {
		return false
	}
	const separators = "()<>@,;:\\\"/[]?={} \t"
	for _, character := range value {
		if character < 0x21 ||
			character > 0x7e ||
			strings.ContainsRune(separators, character) {
			return false
		}
	}
	return true
}

func parseAskResponse(raw []byte) any {
	var parsed any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&parsed) == nil {
		return parsed
	}
	return string(raw)
}

func extractAskText(value any, fallback string) string {
	if text, ok := value.(string); ok {
		return text
	}
	object, ok := value.(map[string]any)
	if !ok {
		return fallback
	}
	for _, key := range []string{
		"message",
		"content",
		"answer",
		"text",
		"response",
	} {
		if text, ok := object[key].(string); ok {
			return text
		}
	}
	if choices, ok := object["choices"].([]any); ok && len(choices) > 0 {
		if first, ok := choices[0].(map[string]any); ok {
			if message, ok := first["message"].(map[string]any); ok {
				if text, ok := message["content"].(string); ok {
					return text
				}
			}
		}
	}
	return fallback
}

func retryAtFromHeader(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return now.UTC().Add(time.Duration(seconds) * time.Second)
	}
	if parsed, err := http.ParseTime(value); err == nil &&
		parsed.After(now.UTC()) {
		return parsed.UTC()
	}
	return time.Time{}
}
