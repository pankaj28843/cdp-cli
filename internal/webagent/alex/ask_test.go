package alex

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func TestAskPerformsOneExactReplayAfterPendingPersistence(t *testing.T) {
	now := time.Date(2026, 7, 26, 7, 0, 0, 0, time.UTC)
	store := readyAskState(t, now)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost ||
			request.URL.String() != ChatURL ||
			request.Header.Get("X-Csrf-Token") != "csrf:value" ||
			!strings.Contains(request.Header.Get("Cookie"), "token=opaque") {
			t.Fatalf("unexpected replay request: %+v", request)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if !strings.Contains(string(body), `"content":"useful prompt"`) ||
			!strings.Contains(
				string(body),
				`"courseId":"system-design-interview"`,
			) {
			t.Fatalf("request body = %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
			Body: io.NopCloser(strings.NewReader(
				`{"answer":"decision-ready answer"}`,
			)),
		}, nil
	})}

	result := Ask(
		context.Background(),
		AskConfig{
			Store:       store,
			HTTPClient:  client,
			BuildCommit: "test",
			Now:         func() time.Time { return now },
		},
		"useful prompt",
		"system-design-interview",
		"design-a-rate-limiter",
	)

	if calls != 1 {
		t.Fatalf("replay calls = %d, want 1", calls)
	}
	if !result.OK ||
		result.State != webagent.StateTerminal ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchPerformed ||
		result.Action.RetrySafe ||
		!result.Action.PendingPersisted {
		t.Fatalf("result = %+v", result)
	}
	data, ok := result.Data.(AskData)
	if !ok || data.Text != "decision-ready answer" {
		t.Fatalf("data = %#v", result.Data)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
}

func TestAskNeverRetriesAcknowledgedRateLimit(t *testing.T) {
	now := time.Date(2026, 7, 26, 7, 0, 0, 0, time.UTC)
	store := readyAskState(t, now)
	calls := 0
	result := Ask(
		context.Background(),
		AskConfig{
			Store: store,
			HTTPClient: &http.Client{Transport: roundTripFunc(func(
				*http.Request,
			) (*http.Response, error) {
				calls++
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header: http.Header{
						"Retry-After": []string{"30"},
					},
					Body: io.NopCloser(strings.NewReader("rate limited")),
				}, nil
			})},
			BuildCommit: "test",
			Now:         func() time.Time { return now },
		},
		"useful prompt",
		"system-design-interview",
		"design-a-rate-limiter",
	)
	if calls != 1 {
		t.Fatalf("replay calls = %d, want 1", calls)
	}
	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "alex_rate_limited" ||
		result.Error.RetrySafe ||
		result.Error.RetryAt != now.Add(30*time.Second).Format(time.RFC3339Nano) ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchPerformed {
		t.Fatalf("result = %+v", result)
	}
}

func TestAskClassifiesTransportFailureAsUnknownWithoutRetry(t *testing.T) {
	now := time.Date(2026, 7, 26, 7, 0, 0, 0, time.UTC)
	store := readyAskState(t, now)
	calls := 0
	result := Ask(
		context.Background(),
		AskConfig{
			Store: store,
			HTTPClient: &http.Client{Transport: roundTripFunc(func(
				*http.Request,
			) (*http.Response, error) {
				calls++
				return nil, errors.New("ambiguous transport failure")
			})},
			BuildCommit: "test",
			Now:         func() time.Time { return now },
		},
		"useful prompt",
		"system-design-interview",
		"design-a-rate-limiter",
	)
	if calls != 1 {
		t.Fatalf("replay calls = %d, want 1", calls)
	}
	if result.OK ||
		result.Error == nil ||
		result.Error.RetrySafe ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchUnknown {
		t.Fatalf("result = %+v", result)
	}
	record, err := store.LoadAskRecord(
		context.Background(),
		result.Evidence.RunID,
	)
	if err != nil {
		t.Fatalf("load exact unknown action record: %v", err)
	}
	if record.RunID != result.Evidence.RunID ||
		record.State != "unknown" ||
		record.AttemptCount != 1 ||
		record.RawInputCount != 1 ||
		!record.PendingPersisted {
		t.Fatalf("unknown action record = %+v", record)
	}
	if _, err := store.LoadAskRecord(
		context.Background(),
		"../unsafe-run",
	); err == nil {
		t.Fatal("LoadAskRecord accepted a path-traversal run id")
	}
}

func readyAskState(
	t *testing.T,
	now time.Time,
) *Store {
	t.Helper()
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	template := RequestTemplate{
		SchemaVersion: AuthTemplateSchemaVersion,
		Method:        http.MethodPost,
		URL:           ChatURL,
		Headers: map[string]string{
			"accept":       "*/*",
			"content-type": "application/json",
			"origin":       Origin,
			"referer":      MyCoursesURL,
			"user-agent":   "Browser UA",
			"x-csrf-token": "csrf:value",
		},
		Cookies: map[string]string{
			"csrf-token": "csrf%3Avalue",
			"token":      "opaque",
		},
		BrowserUserAgent: "Browser UA",
		Body: RequestBody{
			Messages: []Message{{Role: "user", Content: ""}},
		},
		CapturedAt: now.Format(time.RFC3339Nano),
		Source:     "headed-cdp-auth+established-api-chat-v1",
	}
	if err := store.SaveTemplate(context.Background(), template); err != nil {
		t.Fatalf("save template: %v", err)
	}
	lessons := 31
	course := Course{
		Key:            "system-design-interview",
		Title:          "System Design Interview",
		RootPath:       "/courses/system-design-interview",
		DefaultChapter: "/courses/system-design-interview/design-a-rate-limiter",
		Lessons:        &lessons,
	}
	chapter := Chapter{
		CourseKey: course.Key,
		ChapterID: "design-a-rate-limiter",
		Title:     "Design A Rate Limiter",
		URL: Origin +
			"/courses/system-design-interview/design-a-rate-limiter",
	}
	if err := store.SaveCatalog(context.Background(), Catalog{
		SchemaVersion: CatalogSchemaVersion,
		RefreshedAt:   now.Format(time.RFC3339Nano),
		Courses:       map[string]Course{course.Key: course},
		Chapters:      map[string][]Chapter{course.Key: {chapter}},
	}); err != nil {
		t.Fatalf("save catalog: %v", err)
	}
	return store
}
