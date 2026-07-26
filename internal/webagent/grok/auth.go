package grok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AuthRefreshSchemaVersion   = "grok-auth-refresh/v1"
	DoctorSchemaVersion        = "grok-doctor/v1"
	defaultObservationTimeout  = 10 * time.Second
	defaultObservationAttempts = 3
)

type AuthRefreshConfig struct {
	BrowserConfig
	Store               *Store
	ObservationTimeout  time.Duration
	ObservationAttempts int
	Now                 func() time.Time
}

type AuthRefreshData struct {
	SchemaVersion         string `json:"schema_version"`
	AuthState             string `json:"auth_state"`
	TemplatePath          string `json:"template_path"`
	SessionCookieObserved bool   `json:"session_cookie_observed"`
	CookieCount           int    `json:"cookie_count"`
	RequestShape          string `json:"request_shape"`
	CapturedAt            string `json:"captured_at,omitempty"`
}

type DoctorData struct {
	SchemaVersion string        `json:"schema_version"`
	Auth          AuthStatus    `json:"auth"`
	Runtime       RuntimeStatus `json:"runtime"`
	BrowserSubmit string        `json:"browser_submit"`
	StableReads   string        `json:"stable_reads"`
	BrowserMode   string        `json:"browser_mode"`
	BrowserProbed bool          `json:"browser_probed"`
}

type requestRecord struct {
	ID          string
	Method      string
	URL         string
	Headers     map[string]any
	Status      int
	ResponseURL string
}

type networkObserver struct {
	records map[string]*requestRecord
	order   []string
}

func RefreshAuth(ctx context.Context, config AuthRefreshConfig) webagent.Result {
	runID := webagent.NewRunID()
	data := AuthRefreshData{
		SchemaVersion: AuthRefreshSchemaVersion,
		AuthState:     "blocked",
		TemplatePath:  RelativeTemplatePath,
		RequestShape:  "not_observed",
	}
	if config.Store == nil {
		return operationFailure(
			runID, config.BuildCommit, webagent.OperationAuthRefresh,
			webagent.StagePlanned, "browser_observed_auth",
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil,
			"grok_state_unavailable", "internal",
			"Grok owner-only auth state is unavailable", "",
			data, []string{"cdp doctor --json"},
		)
	}
	if config.ObservationTimeout <= 0 {
		config.ObservationTimeout = defaultObservationTimeout
	}
	if config.ObservationAttempts < defaultObservationAttempts {
		config.ObservationAttempts = defaultObservationAttempts
	}
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationAuthRefresh,
		"",
		"about:blank",
		"browser_observed_auth",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			userAgent, err := prepareAuthObservation(ctx, config.Client, session)
			if err != nil {
				return authFailure(
					runID, config, webagent.StageAttached, target, pending,
					"grok_auth_prepare_failed", "connection",
					"Grok auth observation could not prepare the exact headed target",
					data,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return authFailure(
					runID, config, webagent.StageAttached, target, pending,
					"grok_auth_prepare_state_failed", "internal",
					"Grok auth observation preparation could not be persisted",
					data,
				)
			}
			var cookies map[string]string
			readiness, readinessErr := authreadiness.WaitForEvidence(
				ctx,
				session,
				config.ObservationAttempts,
				config.ObservationTimeout,
				250*time.Millisecond,
				func(observationCtx context.Context) (bool, error) {
					observedCookies, cookieErr := readCookies(
						observationCtx,
						session,
					)
					if cookieErr != nil {
						return false, cookieErr
					}
					data.CookieCount = len(observedCookies)
					data.SessionCookieObserved =
						hasSessionCookie(observedCookies)
					if !data.SessionCookieObserved {
						return false, nil
					}
					cookies = observedCookies
					return true, nil
				},
			)
			if readinessErr != nil || readiness.ObservationFailed() {
				_ = lease.MarkIncomplete(context.Background())
				return authFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"grok_auth_readiness_failed", "connection",
					"Grok auth readiness could not complete its bounded load, reload, hard-reload, and grace-wait sequence",
					data,
				)
			}
			if !readiness.Observed {
				_ = lease.MarkIncomplete(context.Background())
				data.AuthState = "evidence_not_observed"
				return authFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"grok_auth_evidence_not_observed", "auth",
					"Grok auth evidence was not observed after initial load, reload, cache-bypassing hard reload, and final grace wait; the browser session may still be active",
					data,
				)
			}

			existing := loadExistingTemplate(ctx, config.Store)
			observation, found, err := observeListRequest(
				ctx,
				config.Client,
				session,
				config.ObservationAttempts,
				config.ObservationTimeout,
			)
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return authFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"grok_auth_observation_failed", "connection",
					"Grok auth request observation failed on the exact headed target",
					data,
				)
			}
			now := time.Now
			if config.Now != nil {
				now = config.Now
			}
			capturedAt := now().UTC().Format(time.RFC3339Nano)
			template := RequestTemplate{}
			if found {
				template = RequestTemplate{
					SchemaVersion:    AuthTemplateSchemaVersion,
					Method:           "GET",
					URL:              observation.URL,
					Headers:          observation.Headers,
					Cookies:          cookies,
					BrowserUserAgent: userAgent,
					CapturedAt:       capturedAt,
					Source:           "headed-cdp-observed-list-request",
				}
				data.RequestShape = "observed_list"
			} else if existing != nil {
				template = *existing
				template.Cookies = cookies
				template.BrowserUserAgent = userAgent
				template.CapturedAt = capturedAt
				template.Source = "headed-cdp-retained-list-shape"
				data.RequestShape = "retained_observed_list"
			} else {
				_ = lease.MarkIncomplete(context.Background())
				data.AuthState = "request_not_observed"
				return authFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"grok_list_request_not_observed", "auth",
					"Signed-in Grok conversation-list request evidence was not observed",
					data,
				)
			}
			if err := config.Store.SaveTemplate(ctx, template); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return authFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"grok_auth_state_write_failed", "internal",
					"Grok request template could not be persisted to owner-only state",
					data,
				)
			}
			if err := lease.MarkTerminal(ctx); err != nil {
				return authFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"grok_auth_terminal_state_failed", "internal",
					"Grok auth terminal state could not be persisted",
					data,
				)
			}
			data.AuthState = "ready"
			data.CapturedAt = capturedAt
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationAuthRefresh,
				webagent.StateReady, webagent.StageObserveTerminal,
				"browser_observed_auth", target, pending, nil, nil, data,
				[]string{
					"cdp workflow agent grok capabilities refresh --json",
					"cdp workflow agent grok doctor --json",
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
	runtime := store.RuntimeStatus(ctx, now, DefaultCapabilitiesTTL)
	data := DoctorData{
		SchemaVersion: DoctorSchemaVersion,
		Auth:          auth,
		Runtime:       runtime,
		BrowserSubmit: "unavailable",
		StableReads:   "unavailable",
		BrowserMode:   "headed",
		BrowserProbed: false,
	}
	if auth.Ready && runtime.Ready {
		data.BrowserSubmit = "ready"
		data.StableReads = "ready"
		result := webagent.NewMetadataResult(
			webagent.ProviderGrok,
			webagent.OperationDoctor,
			data,
			buildCommit,
			[]string{
				"cdp workflow agent grok capabilities --json",
				"cdp workflow agent grok conversations list --json",
			},
		)
		result.Evidence.ReadMode = "owner_only_local_state"
		return result
	}
	code := "grok_auth_" + auth.State
	errClass := "auth"
	message := "Grok auth evidence is not ready"
	next := []string{"cdp workflow agent grok auth refresh --json"}
	if auth.Ready && !runtime.Ready {
		code = "grok_runtime_capabilities_" + runtime.State
		errClass = "capability"
		message = "Grok runtime capability evidence is not ready"
		next = []string{"cdp workflow agent grok capabilities refresh --json"}
	}
	result := operationFailure(
		webagent.NewRunID(), buildCommit, webagent.OperationDoctor,
		webagent.StageMetadata, "owner_only_local_state",
		nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		nil, nil, code, errClass, message, "", data, next,
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func UnavailableDoctor(buildCommit string) webagent.Result {
	result := operationFailure(
		webagent.NewRunID(), buildCommit, webagent.OperationDoctor,
		webagent.StageMetadata, "owner_only_local_state",
		nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		nil, nil,
		"grok_state_unavailable", "internal",
		"Grok owner-only state is unavailable", "",
		map[string]any{"schema_version": DoctorSchemaVersion},
		[]string{"cdp doctor --json"},
	)
	result.Evidence.BrowserMode = "none"
	return result
}

type listObservation struct {
	URL     string
	Headers map[string]string
}

func prepareAuthObservation(
	ctx context.Context,
	client EventClient,
	session *cdp.PageSession,
) (string, error) {
	if err := client.CallSession(ctx, session.SessionID, "Network.enable", map[string]any{}, nil); err != nil {
		return "", err
	}
	if err := client.CallSession(ctx, session.SessionID, "Page.enable", map[string]any{}, nil); err != nil {
		return "", err
	}
	var version struct {
		UserAgent string `json:"userAgent"`
	}
	if err := client.Call(ctx, "Browser.getVersion", map[string]any{}, &version); err != nil {
		return "", err
	}
	if strings.TrimSpace(version.UserAgent) == "" {
		return "", fmt.Errorf("Browser.getVersion returned an empty user agent")
	}
	if _, err := session.Navigate(ctx, HomeURL); err != nil {
		return "", err
	}
	return version.UserAgent, nil
}

func observeListRequest(
	ctx context.Context,
	client EventClient,
	session *cdp.PageSession,
	attempts int,
	wait time.Duration,
) (listObservation, bool, error) {
	observer := networkObserver{records: map[string]*requestRecord{}}
	var selected listObservation
	readiness, err := authreadiness.WaitForEvidence(
		ctx,
		session,
		attempts,
		wait,
		time.Millisecond,
		func(observationCtx context.Context) (bool, error) {
			readCtx, cancelRead, sliceErr :=
				authreadiness.SubObservationContext(
					observationCtx,
					250*time.Millisecond,
				)
			if sliceErr != nil {
				return false, sliceErr
			}
			event, readErr := client.ReadEvent(readCtx)
			readExpired := readCtx.Err() != nil
			stageExpired := observationCtx.Err() != nil
			cancelRead()
			if readErr != nil {
				if readExpired && !stageExpired {
					return false, nil
				}
				if stageExpired ||
					errors.Is(readErr, context.DeadlineExceeded) {
					return false, nil
				}
				return false, readErr
			}
			if event.SessionID != session.SessionID {
				return false, nil
			}
			observer.add(event)
			observation, ok := observer.selectList()
			if ok {
				selected = observation
			}
			return ok, nil
		},
	)
	if err != nil {
		return listObservation{}, false, err
	}
	if readiness.ObservationFailed() {
		return listObservation{}, false, readiness.LastObservationError
	}
	return selected, readiness.Observed, nil
}

func (o *networkObserver) add(event cdp.Event) {
	switch event.Method {
	case "Network.requestWillBeSent":
		var payload struct {
			RequestID string `json:"requestId"`
			Request   struct {
				URL     string         `json:"url"`
				Method  string         `json:"method"`
				Headers map[string]any `json:"headers"`
			} `json:"request"`
		}
		if json.Unmarshal(event.Params, &payload) != nil || payload.RequestID == "" {
			return
		}
		record := o.ensure(payload.RequestID)
		record.Method = payload.Request.Method
		record.URL = payload.Request.URL
		record.Headers = payload.Request.Headers
	case "Network.responseReceived":
		var payload struct {
			RequestID string `json:"requestId"`
			Response  struct {
				URL    string  `json:"url"`
				Status float64 `json:"status"`
			} `json:"response"`
		}
		if json.Unmarshal(event.Params, &payload) != nil || payload.RequestID == "" {
			return
		}
		record := o.ensure(payload.RequestID)
		record.Status = int(payload.Response.Status)
		record.ResponseURL = payload.Response.URL
	}
}

func (o *networkObserver) ensure(id string) *requestRecord {
	if record, ok := o.records[id]; ok {
		return record
	}
	record := &requestRecord{ID: id}
	o.records[id] = record
	o.order = append(o.order, id)
	return record
}

func (o *networkObserver) selectList() (listObservation, bool) {
	for _, id := range o.order {
		record := o.records[id]
		if !isConversationListRecord(record) {
			continue
		}
		return listObservation{
			URL:     record.URL,
			Headers: normalizeReplayHeaders(record.Headers),
		}, true
	}
	return listObservation{}, false
}

func isConversationListRecord(record *requestRecord) bool {
	if record == nil ||
		record.Method != "GET" ||
		record.Status != 200 ||
		record.URL == "" ||
		record.ResponseURL == "" {
		return false
	}
	request, requestOK := parseConversationListURL(record.URL)
	response, responseOK := parseConversationListURL(record.ResponseURL)
	return requestOK && responseOK && request.Path == response.Path
}

func parseConversationListURL(rawURL string) (*url.URL, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host != "grok.com" ||
		parsed.Path != ConversationListPath ||
		parsed.User != nil ||
		parsed.Fragment != "" ||
		parsed.Query().Get("pageSize") == "" ||
		parsed.Query().Get("filterIsStarred") != "" {
		return nil, false
	}
	return parsed, true
}

func readCookies(ctx context.Context, session *cdp.PageSession) (map[string]string, error) {
	raw, err := session.Exec(
		ctx,
		"Network.getCookies",
		json.RawMessage(`{"urls":["https://grok.com"]}`),
	)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Cookies []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode Grok cookies")
	}
	cookies := map[string]string{}
	for _, cookie := range payload.Cookies {
		if validatePrivateValue("cookie name", cookie.Name, 1024) != nil ||
			validatePrivateValue("cookie value", cookie.Value, 64<<10) != nil {
			continue
		}
		cookies[cookie.Name] = cookie.Value
	}
	return cookies, nil
}

func hasSessionCookie(cookies map[string]string) bool {
	for name, value := range cookies {
		if (strings.HasPrefix(name, "sso") || strings.HasPrefix(name, "sso-rw")) &&
			value != "" {
			return true
		}
	}
	return false
}

func normalizeReplayHeaders(raw map[string]any) map[string]string {
	headers := map[string]string{}
	for rawName, rawValue := range raw {
		value, ok := rawValue.(string)
		if !ok {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(rawName))
		if !validHeaderName(name) || forbiddenReplayHeader(name) ||
			validatePrivateValue("header", value, 16<<10) != nil {
			continue
		}
		headers[name] = value
	}
	return headers
}

func validHeaderName(name string) bool {
	if name == "" || name != strings.ToLower(strings.TrimSpace(name)) {
		return false
	}
	for _, r := range name {
		if unicode.IsLetter(r) ||
			unicode.IsDigit(r) ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", r) {
			continue
		}
		return false
	}
	return true
}

func forbiddenReplayHeader(name string) bool {
	switch name {
	case "authorization", "proxy-authorization", "cookie", "set-cookie",
		"content-length", "host", "accept-encoding", "connection",
		"proxy-connection", "transfer-encoding", "upgrade":
		return true
	default:
		return strings.HasPrefix(name, ":")
	}
}

func loadExistingTemplate(ctx context.Context, store *Store) *RequestTemplate {
	template, err := store.LoadTemplate(ctx)
	if err != nil {
		return nil
	}
	return &template
}

func authFailure(
	runID string,
	config AuthRefreshConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	code string,
	errClass string,
	message string,
	data AuthRefreshData,
) webagent.Result {
	return operationFailure(
		runID, config.BuildCommit, webagent.OperationAuthRefresh,
		stage, "browser_observed_auth", target, cleanup,
		nil, nil, code, errClass, message, "", data,
		cleanupCommands(runID, cleanup),
	)
}
