package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AuthRefreshSchemaVersion   = "chatgpt-auth-refresh/v1"
	DoctorSchemaVersion        = "chatgpt-doctor/v1"
	defaultObservationTimeout  = 12 * time.Second
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
	SignedInUIObserved    bool   `json:"signed_in_ui_observed"`
	SessionCookieObserved bool   `json:"session_cookie_observed"`
	CookieCount           int    `json:"cookie_count"`
	RequestShape          string `json:"request_shape"`
	EndpointPath          string `json:"endpoint_path,omitempty"`
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

type authRequestRecord struct {
	ID           string
	Method       string
	URL          string
	Headers      map[string]any
	ExtraHeaders map[string]any
	Status       int
	ResponseURL  string
}

type authNetworkObserver struct {
	records map[string]*authRequestRecord
	order   []string
}

type readObservation struct {
	URL          string
	Headers      map[string]string
	CookieHeader string
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
			"chatgpt_state_unavailable", "internal",
			"ChatGPT owner-only auth state is unavailable",
			data, []string{"cdp doctor --json"},
		)
	}
	if config.ObservationTimeout <= 0 {
		config.ObservationTimeout = defaultObservationTimeout
	}
	if config.ObservationAttempts <= 0 {
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
					"chatgpt_auth_prepare_failed", "connection",
					"ChatGPT auth observation could not prepare the exact headed target",
					data,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return authFailure(
					runID, config, webagent.StageAttached, target, pending,
					"chatgpt_auth_prepare_state_failed", "internal",
					"ChatGPT auth observation preparation could not be persisted",
					data,
				)
			}
			existing := loadExistingTemplate(ctx, config.Store)
			observation, found, err := observeReadRequest(
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
					"chatgpt_auth_observation_failed", "connection",
					"ChatGPT conversation-read request observation failed on the exact headed target",
					data,
				)
			}
			cookies, err := readCookies(ctx, session)
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return authFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"chatgpt_cookie_observation_failed", "connection",
					"ChatGPT cookie evidence could not be read from the exact headed target",
					data,
				)
			}
			data.CookieCount = len(cookies)
			data.SessionCookieObserved = hasSessionCookie(cookies)
			data.SignedInUIObserved = signedInUIObserved(ctx, session)
			if !data.SessionCookieObserved || !data.SignedInUIObserved {
				_ = lease.MarkIncomplete(context.Background())
				data.AuthState = "signed_out"
				return authFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"chatgpt_signed_out", "auth",
					"Signed-in ChatGPT UI and session-cookie evidence were not both observed",
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
				observation.Headers["user-agent"] = userAgent
				template = RequestTemplate{
					SchemaVersion:    AuthTemplateSchemaVersion,
					Method:           "GET",
					URL:              observation.URL,
					Headers:          observation.Headers,
					Cookies:          cookies,
					CookieHeader:     observation.CookieHeader,
					BrowserUserAgent: userAgent,
					CapturedAt:       capturedAt,
					Source:           "headed-cdp-observed-read-request",
				}
				data.RequestShape = "observed_read"
			} else if existing != nil {
				template = *existing
				template.Cookies = cookies
				template.BrowserUserAgent = userAgent
				template.Headers["user-agent"] = userAgent
				template.CapturedAt = capturedAt
				template.Source = "headed-cdp-retained-read-shape"
				data.RequestShape = "retained_observed_read"
			} else {
				_ = lease.MarkIncomplete(context.Background())
				data.AuthState = "request_not_observed"
				return authFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"chatgpt_read_request_not_observed", "auth",
					"Signed-in ChatGPT conversation-read request evidence was not observed",
					data,
				)
			}
			if parsed, parseErr := url.Parse(template.URL); parseErr == nil {
				data.EndpointPath = parsed.Path
			}
			if err := config.Store.SaveTemplate(ctx, template); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return authFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"chatgpt_auth_state_write_failed", "internal",
					"ChatGPT request template could not be persisted to owner-only state",
					data,
				)
			}
			if err := lease.MarkTerminal(ctx); err != nil {
				return authFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"chatgpt_auth_terminal_state_failed", "internal",
					"ChatGPT auth terminal state could not be persisted",
					data,
				)
			}
			data.AuthState = "ready"
			data.CapturedAt = capturedAt
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationAuthRefresh,
				webagent.StageObserveTerminal, "browser_observed_auth",
				target, pending, data,
				[]string{
					"cdp workflow agent chatgpt capabilities refresh --json",
					"cdp workflow agent chatgpt doctor --json",
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
		data.StableReads = "ready_browser_context_http"
		result := webagent.NewMetadataResult(
			webagent.ProviderChatGPT,
			webagent.OperationDoctor,
			data,
			buildCommit,
			[]string{
				"cdp workflow agent chatgpt capabilities --json",
				"cdp workflow agent chatgpt auth refresh --json",
			},
		)
		result.Evidence.ReadMode = "owner_only_local_state"
		return result
	}
	code := "chatgpt_auth_" + auth.State
	errClass := "auth"
	message := "ChatGPT auth evidence is not ready"
	next := []string{"cdp workflow agent chatgpt auth refresh --json"}
	if auth.Ready && !runtime.Ready {
		code = "chatgpt_runtime_capabilities_" + runtime.State
		errClass = "capability"
		message = "ChatGPT runtime capability evidence is not ready"
		next = []string{"cdp workflow agent chatgpt capabilities refresh --json"}
	}
	result := operationFailure(
		webagent.NewRunID(), buildCommit, webagent.OperationDoctor,
		webagent.StageMetadata, "owner_only_local_state",
		nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		code, errClass, message, data, next,
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func UnavailableDoctor(buildCommit string) webagent.Result {
	result := operationFailure(
		webagent.NewRunID(), buildCommit, webagent.OperationDoctor,
		webagent.StageMetadata, "owner_only_local_state",
		nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		"chatgpt_state_unavailable", "internal",
		"ChatGPT owner-only state is unavailable",
		map[string]any{"schema_version": DoctorSchemaVersion},
		[]string{"cdp doctor --json"},
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func prepareAuthObservation(
	ctx context.Context,
	client EventClient,
	session *cdp.PageSession,
) (string, error) {
	for _, method := range []string{"Network.enable", "Page.enable", "Runtime.enable"} {
		if err := client.CallSession(ctx, session.SessionID, method, map[string]any{}, nil); err != nil {
			return "", err
		}
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

func observeReadRequest(
	ctx context.Context,
	client EventClient,
	session *cdp.PageSession,
	attempts int,
	wait time.Duration,
) (readObservation, bool, error) {
	observer := authNetworkObserver{records: map[string]*authRequestRecord{}}
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := session.Reload(ctx, false); err != nil {
				return readObservation{}, false, err
			}
		}
		attemptCtx, cancel := context.WithTimeout(ctx, wait)
		for {
			event, err := client.ReadEvent(attemptCtx)
			if err != nil {
				if attemptCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
					cancel()
					if ctx.Err() != nil {
						return readObservation{}, false, ctx.Err()
					}
					break
				}
				cancel()
				return readObservation{}, false, err
			}
			if event.SessionID != session.SessionID {
				continue
			}
			observer.add(event)
			if observation, ok := observer.selectRead(); ok {
				cancel()
				return observation, true, nil
			}
		}
	}
	return readObservation{}, false, nil
}

func (o *authNetworkObserver) add(event cdp.Event) {
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
	case "Network.requestWillBeSentExtraInfo":
		var payload struct {
			RequestID string         `json:"requestId"`
			Headers   map[string]any `json:"headers"`
		}
		if json.Unmarshal(event.Params, &payload) != nil || payload.RequestID == "" {
			return
		}
		o.ensure(payload.RequestID).ExtraHeaders = payload.Headers
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

func (o *authNetworkObserver) ensure(id string) *authRequestRecord {
	if record, ok := o.records[id]; ok {
		return record
	}
	record := &authRequestRecord{ID: id}
	o.records[id] = record
	o.order = append(o.order, id)
	return record
}

func (o *authNetworkObserver) selectRead() (readObservation, bool) {
	for _, id := range o.order {
		record := o.records[id]
		if !isReadRecord(record) {
			continue
		}
		raw := map[string]any{}
		for name, value := range record.Headers {
			raw[name] = value
		}
		for name, value := range record.ExtraHeaders {
			raw[name] = value
		}
		headers := normalizeReplayHeaders(raw)
		if strings.TrimSpace(headers["authorization"]) == "" {
			continue
		}
		cookieHeader := rawHeaderString(raw, "cookie")
		if strings.TrimSpace(cookieHeader) == "" {
			continue
		}
		return readObservation{
			URL:          record.URL,
			Headers:      headers,
			CookieHeader: cookieHeader,
		}, true
	}
	return readObservation{}, false
}

func isReadRecord(record *authRequestRecord) bool {
	if record == nil ||
		record.Method != "GET" ||
		record.Status != 200 ||
		record.URL == "" ||
		record.ResponseURL == "" {
		return false
	}
	request, requestOK := parseReadURL(record.URL)
	response, responseOK := parseReadURL(record.ResponseURL)
	return requestOK && responseOK && request.Path == response.Path
}

func parseReadURL(rawURL string) (*url.URL, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host != "chatgpt.com" ||
		!isReadAuthPath(parsed.Path) ||
		parsed.User != nil ||
		parsed.Fragment != "" {
		return nil, false
	}
	return parsed, true
}

func readCookies(ctx context.Context, session *cdp.PageSession) (map[string]string, error) {
	raw, err := session.Exec(
		ctx,
		"Network.getCookies",
		json.RawMessage(`{"urls":["https://chatgpt.com"]}`),
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
		return nil, fmt.Errorf("decode ChatGPT cookies")
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
		if (strings.HasPrefix(name, "__Secure-next-auth.session-token") ||
			name == "oai-client-auth-info") &&
			value != "" {
			return true
		}
	}
	return false
}

func signedInUIObserved(ctx context.Context, session *cdp.PageSession) bool {
	var result struct {
		SignedIn  bool `json:"signed_in"`
		SignedOut bool `json:"signed_out"`
	}
	_, err := pollUntil(ctx, 15*time.Second, 250*time.Millisecond, func() (bool, error) {
		err := evaluateInto(ctx, session, `(() => {
		  const text = String(document.body && document.body.innerText || '');
		  const composer = document.querySelector('#prompt-textarea') ||
		    document.querySelector('[contenteditable="true"][role="textbox"]');
		  return {
		    signed_in: Boolean(composer) || /\b(New chat|Chat history|How can I help)\b/i.test(text),
		    signed_out: /\b(Log in|Sign up)\b/i.test(text)
		  };
		})()`, &result)
		return err == nil && result.SignedIn && !result.SignedOut, err
	})
	return err == nil && result.SignedIn && !result.SignedOut
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

func rawHeaderString(raw map[string]any, wanted string) string {
	for name, value := range raw {
		if !strings.EqualFold(strings.TrimSpace(name), wanted) {
			continue
		}
		text, ok := value.(string)
		if !ok || validatePrivateValue("header", text, 256<<10) != nil {
			return ""
		}
		return text
	}
	return ""
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
	case "proxy-authorization", "cookie", "set-cookie",
		"content-length", "host", "accept-encoding", "connection",
		"proxy-connection", "transfer-encoding", "upgrade":
		return true
	default:
		return strings.HasPrefix(name, ":") ||
			strings.HasPrefix(name, "openai-sentinel-")
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
		code, errClass, message, data,
		cleanupCommands(runID, cleanup),
	)
}
