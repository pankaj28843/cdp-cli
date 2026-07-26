package alex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AuthRefreshSchemaVersion = "alex-auth-refresh/v1"
	DoctorSchemaVersion      = "alex-doctor/v1"
)

type AuthRefreshConfig struct {
	BrowserConfig
	Store        *Store
	Timeout      time.Duration
	PollInterval time.Duration
	DryRun       bool
	Now          func() time.Time
}

type AuthRefreshData struct {
	SchemaVersion       string `json:"schema_version"`
	AuthState           string `json:"auth_state"`
	StatePath           string `json:"state_path"`
	CookieCount         int    `json:"cookie_count"`
	ObservationAttempts int    `json:"observation_attempts"`
	DryRun              bool   `json:"dry_run"`
	WroteState          bool   `json:"wrote_state"`
	CapturedAt          string `json:"captured_at,omitempty"`
}

type DoctorData struct {
	SchemaVersion string        `json:"schema_version"`
	Auth          AuthStatus    `json:"auth"`
	Catalog       CatalogStatus `json:"catalog"`
	AskReplay     string        `json:"ask_replay"`
	ContentReads  string        `json:"content_reads"`
	BrowserMode   string        `json:"browser_mode"`
	BrowserProbed bool          `json:"browser_probed"`
}

type browserIdentity struct {
	URL       string `json:"url"`
	UserAgent string `json:"user_agent"`
	Language  string `json:"language"`
	BodyReady bool   `json:"body_ready"`
}

type networkCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type networkCookies struct {
	Cookies []networkCookie `json:"cookies"`
}

func RefreshAuth(ctx context.Context, config AuthRefreshConfig) webagent.Result {
	runID := webagent.NewRunID()
	data := AuthRefreshData{
		SchemaVersion: AuthRefreshSchemaVersion,
		AuthState:     "blocked",
		StatePath:     RelativeTemplatePath,
		DryRun:        config.DryRun,
	}
	if config.Store == nil {
		return operationFailure(
			runID,
			config.BuildCommit,
			webagent.OperationAuthRefresh,
			webagent.StagePlanned,
			"browser_observed_request_template",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil,
			"alex_state_unavailable",
			"internal",
			"Ask Alex owner-only auth state is unavailable",
			"",
			data,
			[]string{"cdp doctor --json"},
		)
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
		webagent.OperationAuthRefresh,
		"about:blank",
		"browser_observed_request_template",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			chapterURL := Origin + "/courses/" + DefaultCourseID + "/" + DefaultChapterID
			if err := preparePage(ctx, config.Client, session, chapterURL); err != nil {
				return operationFailure(
					runID,
					config.BuildCommit,
					webagent.OperationAuthRefresh,
					webagent.StageAttached,
					"browser_observed_request_template",
					target,
					pending,
					nil,
					"alex_auth_prepare_failed",
					"connection",
					"Ask Alex auth observation could not prepare the exact headed target",
					"",
					data,
					cleanupCommands(runID, pending),
				)
			}
			var identity browserIdentity
			_, err := pollUntil(
				ctx,
				config.Timeout,
				config.PollInterval,
				func() (bool, error) {
					if err := observeBrowserIdentity(ctx, session, &identity); err != nil {
						return false, err
					}
					return strings.HasPrefix(
						identity.URL,
						Origin+"/courses/",
					) && identity.BodyReady &&
						strings.TrimSpace(identity.UserAgent) != "", nil
				},
			)
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return operationFailure(
					runID,
					config.BuildCommit,
					webagent.OperationAuthRefresh,
					webagent.StageAttached,
					"browser_observed_request_template",
					target,
					pending,
					nil,
					"alex_auth_page_not_ready",
					"auth",
					"Signed-in ByteByteGo page evidence did not become ready",
					"",
					data,
					[]string{
						"Sign in to ByteByteGo in headed Chrome.",
						"cdp workflow agent alex auth refresh --json",
					},
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return operationFailure(
					runID,
					config.BuildCommit,
					webagent.OperationAuthRefresh,
					webagent.StageAttached,
					"browser_observed_request_template",
					target,
					pending,
					nil,
					"alex_auth_prepare_state_failed",
					"internal",
					"Ask Alex auth observation preparation could not be persisted",
					"",
					data,
					cleanupCommands(runID, pending),
				)
			}

			var cookies map[string]string
			for attempt := 1; attempt <= 3; attempt++ {
				data.ObservationAttempts = attempt
				observed, observeErr := observeCookies(ctx, session)
				if observeErr == nil {
					data.CookieCount = len(observed)
					csrf := decodedCookieValue(observed["csrf-token"])
					token := observed["token"]
					if csrf != "" && token != "" && !jwtExpired(token, nowFor(config.Now)) {
						cookies = observed
						break
					}
				}
				if attempt < 3 {
					if reloadErr := session.Reload(ctx, true); reloadErr != nil {
						break
					}
					_, _ = pollUntil(
						ctx,
						10*time.Second,
						config.PollInterval,
						func() (bool, error) {
							return pageReady(ctx, session, chapterURL)
						},
					)
				}
			}
			if cookies == nil {
				_ = lease.MarkIncomplete(context.Background())
				data.AuthState = "missing_browser_auth"
				return operationFailure(
					runID,
					config.BuildCommit,
					webagent.OperationAuthRefresh,
					webagent.StageObserveTerminal,
					"browser_observed_request_template",
					target,
					pending,
					nil,
					"alex_signed_out",
					"auth",
					"ByteByteGo token and CSRF cookie evidence was not observed after three bounded attempts",
					"",
					data,
					[]string{
						"Sign in to ByteByteGo in headed Chrome.",
						"cdp workflow agent alex auth refresh --json",
					},
				)
			}

			now := nowFor(config.Now)
			capturedAt := now.UTC().Format(time.RFC3339Nano)
			template := RequestTemplate{
				SchemaVersion: AuthTemplateSchemaVersion,
				Method:        "POST",
				URL:           ChatURL,
				Headers: map[string]string{
					"accept":          "*/*",
					"accept-language": strings.TrimSpace(identity.Language),
					"content-type":    "application/json",
					"origin":          Origin,
					"referer":         MyCoursesURL,
					"user-agent":      strings.TrimSpace(identity.UserAgent),
					"x-csrf-token":    decodedCookieValue(cookies["csrf-token"]),
				},
				Cookies:          cookies,
				BrowserUserAgent: strings.TrimSpace(identity.UserAgent),
				Body: RequestBody{
					Messages: []Message{{Role: "user", Content: ""}},
				},
				CapturedAt: capturedAt,
				Source:     "headed-cdp-auth+established-api-chat-v1",
			}
			if strings.TrimSpace(template.Headers["accept-language"]) == "" {
				delete(template.Headers, "accept-language")
			}
			if err := template.Validate(); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return operationFailure(
					runID,
					config.BuildCommit,
					webagent.OperationAuthRefresh,
					webagent.StageObserveTerminal,
					"browser_observed_request_template",
					target,
					pending,
					nil,
					"alex_auth_template_invalid",
					"auth",
					"Observed ByteByteGo request-template evidence was incomplete",
					"",
					data,
					[]string{"cdp workflow agent alex auth refresh --json"},
				)
			}
			if !config.DryRun {
				if err := config.Store.SaveTemplate(ctx, template); err != nil {
					_ = lease.MarkIncomplete(context.Background())
					return operationFailure(
						runID,
						config.BuildCommit,
						webagent.OperationAuthRefresh,
						webagent.StageObserveTerminal,
						"browser_observed_request_template",
						target,
						pending,
						nil,
						"alex_auth_state_write_failed",
						"internal",
						"Ask Alex request template could not be persisted to owner-only state",
						"",
						data,
						cleanupCommands(runID, pending),
					)
				}
				data.WroteState = true
			}
			if err := lease.MarkTerminal(ctx); err != nil {
				return operationFailure(
					runID,
					config.BuildCommit,
					webagent.OperationAuthRefresh,
					webagent.StageObserveTerminal,
					"browser_observed_request_template",
					target,
					pending,
					nil,
					"alex_auth_terminal_state_failed",
					"internal",
					"Ask Alex auth terminal state could not be persisted",
					"",
					data,
					cleanupCommands(runID, pending),
				)
			}
			data.AuthState = "ready"
			data.CapturedAt = capturedAt
			return operationSuccess(
				runID,
				config.BuildCommit,
				webagent.OperationAuthRefresh,
				webagent.StateReady,
				webagent.StageObserveTerminal,
				"browser_observed_request_template",
				target,
				pending,
				nil,
				data,
				[]string{
					"cdp workflow agent alex doctor --json",
					"cdp workflow agent alex catalog refresh --json",
				},
			)
		},
	)
}

func Doctor(
	ctx context.Context,
	store *Store,
	now time.Time,
	buildCommit string,
) webagent.Result {
	if store == nil {
		return UnavailableDoctor(buildCommit)
	}
	auth := store.AuthStatus(ctx, now, DefaultAuthTTL)
	catalog := store.CatalogStatus(ctx, now, DefaultCatalogTTL)
	data := DoctorData{
		SchemaVersion: DoctorSchemaVersion,
		Auth:          auth,
		Catalog:       catalog,
		AskReplay:     "unavailable",
		ContentReads:  "unavailable",
		BrowserMode:   "headed",
		BrowserProbed: false,
	}
	if auth.Ready {
		data.AskReplay = "ready"
	}
	if catalog.Ready {
		data.ContentReads = "ready"
	}
	if auth.Ready && catalog.Ready {
		result := webagent.NewMetadataResult(
			webagent.ProviderAlex,
			webagent.OperationDoctor,
			data,
			buildCommit,
			[]string{
				"cdp workflow agent alex courses list --json",
				"cdp workflow agent alex chapters list --json",
			},
		)
		result.Evidence.ReadMode = "owner_only_local_state"
		return result
	}
	code := "alex_auth_" + auth.State
	errClass := "auth"
	message := "Ask Alex auth evidence is not ready"
	next := []string{"cdp workflow agent alex auth refresh --json"}
	if auth.Ready && !catalog.Ready {
		code = "alex_catalog_" + catalog.State
		errClass = "capability"
		message = "Ask Alex dynamic catalog is not ready"
		next = []string{"cdp workflow agent alex catalog refresh --json"}
	}
	result := operationFailure(
		webagent.NewRunID(),
		buildCommit,
		webagent.OperationDoctor,
		webagent.StageMetadata,
		"owner_only_local_state",
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		nil,
		code,
		errClass,
		message,
		"",
		data,
		next,
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func UnavailableDoctor(buildCommit string) webagent.Result {
	result := operationFailure(
		webagent.NewRunID(),
		buildCommit,
		webagent.OperationDoctor,
		webagent.StageMetadata,
		"owner_only_local_state",
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		nil,
		"alex_state_unavailable",
		"internal",
		"Ask Alex owner-only state is unavailable",
		"",
		map[string]any{"schema_version": DoctorSchemaVersion},
		[]string{"cdp doctor --json"},
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func observeBrowserIdentity(
	ctx context.Context,
	session *cdp.PageSession,
	target *browserIdentity,
) error {
	return evaluateInto(ctx, session, `(() => ({
	  url: location.href,
	  user_agent: navigator.userAgent || '',
	  language: navigator.language || '',
	  body_ready: Boolean(document.body)
	}))()`, target)
}

func pageReady(
	ctx context.Context,
	session *cdp.PageSession,
	expectedURL string,
) (bool, error) {
	var identity browserIdentity
	if err := observeBrowserIdentity(ctx, session, &identity); err != nil {
		return false, err
	}
	return identity.URL == expectedURL && identity.BodyReady, nil
}

func observeCookies(
	ctx context.Context,
	session *cdp.PageSession,
) (map[string]string, error) {
	raw, err := session.Exec(
		ctx,
		"Network.getCookies",
		json.RawMessage(`{"urls":["https://bytebytego.com"]}`),
	)
	if err != nil {
		return nil, err
	}
	var payload networkCookies
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode ByteByteGo cookie observation")
	}
	cookies := make(map[string]string, len(payload.Cookies))
	for _, cookie := range payload.Cookies {
		name := strings.TrimSpace(cookie.Name)
		if name != "" && cookie.Value != "" {
			cookies[name] = cookie.Value
		}
	}
	return cookies, nil
}

func decodedCookieValue(value string) string {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(decoded)
}

func jwtExpired(token string, now time.Time) bool {
	if strings.Count(token, ".") != 2 {
		return false
	}
	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		ExpiresAt json.Number `json:"exp"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if decoder.Decode(&claims) != nil || claims.ExpiresAt == "" {
		return false
	}
	expiresAt, err := claims.ExpiresAt.Int64()
	if err != nil {
		value, floatErr := claims.ExpiresAt.Float64()
		if floatErr != nil {
			return false
		}
		expiresAt = int64(value)
	}
	return expiresAt <= now.Unix()
}

func nowFor(candidate func() time.Time) time.Time {
	if candidate != nil {
		return candidate().UTC()
	}
	return time.Now().UTC()
}
