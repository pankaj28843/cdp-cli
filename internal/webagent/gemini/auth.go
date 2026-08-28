package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AuthRefreshSchemaVersion = "gemini-auth-refresh/v1"
	DoctorSchemaVersion      = "gemini-doctor/v1"
)

type AuthRefreshConfig struct {
	BrowserConfig
	Store        *Store
	Timeout      time.Duration
	PollInterval time.Duration
	Now          func() time.Time
}

type AuthRefreshData struct {
	SchemaVersion         string `json:"schema_version"`
	AuthState             string `json:"auth_state"`
	StatePath             string `json:"state_path"`
	ComposerReady         bool   `json:"composer_ready"`
	SignedIn              bool   `json:"signed_in"`
	SessionCookieObserved bool   `json:"session_cookie_observed"`
	CookieCount           int    `json:"cookie_count"`
	CapturedAt            string `json:"captured_at,omitempty"`
}

type DoctorData struct {
	SchemaVersion string        `json:"schema_version"`
	Auth          AuthStatus    `json:"auth"`
	Runtime       RuntimeStatus `json:"runtime"`
	BrowserSubmit string        `json:"browser_submit"`
	BrowserReads  string        `json:"browser_reads"`
	BrowserMode   string        `json:"browser_mode"`
	BrowserProbed bool          `json:"browser_probed"`
}

type authObservation struct {
	ComposerReady bool `json:"composer_ready"`
	PromptReady   bool `json:"prompt_ready"`
}

func RefreshAuth(ctx context.Context, config AuthRefreshConfig) webagent.Result {
	runID := webagent.NewRunID()
	data := AuthRefreshData{
		SchemaVersion: AuthRefreshSchemaVersion,
		AuthState:     "blocked",
		StatePath:     RelativeAuthStatePath,
	}
	if config.Store == nil {
		return operationFailure(
			runID, config.BuildCommit, webagent.OperationAuthRefresh,
			webagent.StagePlanned, "browser_observed_auth",
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil,
			"gemini_state_unavailable", "internal",
			"Gemini owner-only auth state is unavailable", "",
			data, []string{"cdp doctor --json"},
		)
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
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
		"browser_observed_auth",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			var observedCookies map[string]string
			if err := preparePage(ctx, config.Client, session, HomeURL); err != nil {
				return operationFailure(
					runID, config.BuildCommit, webagent.OperationAuthRefresh,
					webagent.StageAttached, "browser_observed_auth",
					target, pending, nil, nil,
					"gemini_auth_prepare_failed", "connection",
					"Gemini auth observation could not prepare the exact headed target", "",
					data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return operationFailure(
					runID, config.BuildCommit, webagent.OperationAuthRefresh,
					webagent.StageAttached, "browser_observed_auth",
					target, pending, nil, nil,
					"gemini_auth_prepare_state_failed", "internal",
					"Gemini auth observation preparation could not be persisted", "",
					data, cleanupCommands(runID, pending),
				)
			}
			const readinessAttempts = 3
			var observation authObservation
			readiness, readinessErr := authreadiness.WaitForEvidence(
				ctx,
				session,
				readinessAttempts,
				config.Timeout,
				config.PollInterval,
				func(observationCtx context.Context) (bool, error) {
					if err := evaluateInto(observationCtx, session, `(() => {
					  const editor = document.querySelector('[role=textbox][contenteditable=true]');
					  return {composer_ready: Boolean(editor), prompt_ready: Boolean(editor)};
					})()`, &observation); err != nil {
						return false, err
					}
					data.ComposerReady = observation.ComposerReady &&
						observation.PromptReady
					cookies, cookieCount, sessionCookie, cookieErr :=
						observeSessionCookies(observationCtx, session)
					if cookieErr != nil {
						return false, cookieErr
					}
					data.CookieCount = cookieCount
					observedCookies = cookies
					data.SessionCookieObserved = sessionCookie
					data.SignedIn = data.ComposerReady &&
						data.SessionCookieObserved
					return data.SignedIn, nil
				},
			)
			if readinessErr != nil {
				_ = lease.MarkIncomplete(context.Background())
				return operationFailure(
					runID, config.BuildCommit, webagent.OperationAuthRefresh,
					webagent.StagePrepared, "browser_observed_auth",
					target, pending, nil, nil,
					"gemini_auth_readiness_failed", "connection",
					"Gemini auth readiness could not complete its bounded load, reload, hard-reload, and grace-wait sequence", "",
					data, cleanupCommands(runID, pending),
				)
			}
			if !data.SignedIn || !data.SessionCookieObserved {
				_ = lease.MarkIncomplete(context.Background())
				if readiness.ObservationFailed() {
					return operationFailure(
						runID, config.BuildCommit, webagent.OperationAuthRefresh,
						webagent.StageObserveTerminal, "browser_observed_auth",
						target, pending, nil, nil,
						"gemini_cookie_observation_failed", "connection",
						"Gemini cookie evidence could not be read after the bounded readiness sequence", "",
						data, cleanupCommands(runID, pending),
					)
				}
				data.AuthState = "evidence_not_observed"
				return operationFailure(
					runID, config.BuildCommit, webagent.OperationAuthRefresh,
					webagent.StageObserveTerminal, "browser_observed_auth",
					target, pending, nil, nil,
					"gemini_auth_evidence_not_observed", "auth",
					"Gemini auth evidence was not observed after initial load, reload, cache-bypassing hard reload, and final grace wait; the browser session may still be active", "",
					data,
					[]string{
						"cdp workflow agent gemini auth refresh --json",
					},
				)
			}
			now := time.Now
			if config.Now != nil {
				now = config.Now
			}
			capturedAt := now().UTC().Format(time.RFC3339Nano)
			if err := config.Store.SaveAuth(ctx, AuthState{
				SchemaVersion:         AuthStateSchemaVersion,
				CapturedAt:            capturedAt,
				SignedIn:              true,
				SessionCookieObserved: true,
				Source:                "headed-cdp-safe-auth-evidence",
			}); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return operationFailure(
					runID, config.BuildCommit, webagent.OperationAuthRefresh,
					webagent.StageObserveTerminal, "browser_observed_auth",
					target, pending, nil, nil,
					"gemini_auth_state_write_failed", "internal",
					"Gemini auth evidence could not be persisted to owner-only state", "",
					data, cleanupCommands(runID, pending),
				)
			}
			var existing *RequestTemplate
			if loaded, err := config.Store.LoadTemplate(ctx); err == nil {
				existing = &loaded
			}
			var browser struct {
				UserAgent string `json:"user_agent"`
				APIKey    string `json:"api_key"`
			}
			if err := evaluateInto(ctx, session, `(() => {
			  const html = document.documentElement?.innerHTML || "";
			  const match = html.match(/\\?"VVlN6d\\?"\s*:\s*\\?"(AIza[A-Za-z0-9_-]{35})\\?"/);
			  return {user_agent: navigator.userAgent || "", api_key: match?.[1] || ""};
			})()`, &browser); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return operationFailure(
					runID, config.BuildCommit, webagent.OperationAuthRefresh,
					webagent.StageObserveTerminal, "browser_observed_auth",
					target, pending, nil, nil,
					"gemini_auth_template_observation_failed", "connection",
					"Gemini browser auth template could not be refreshed", "",
					data, cleanupCommands(runID, pending),
				)
			}
			refreshed, ok := retainedDictationTemplate(existing, observedCookies, browser.UserAgent, browser.APIKey, now())
			if !ok || config.Store.SaveTemplate(ctx, refreshed) != nil {
				_ = lease.MarkIncomplete(context.Background())
				return operationFailure(
					runID, config.BuildCommit, webagent.OperationAuthRefresh,
					webagent.StageObserveTerminal, "browser_observed_auth",
					target, pending, nil, nil,
					"gemini_dictation_template_refresh_failed", "auth",
					"Gemini dictation template credentials could not be refreshed from the signed-in browser", "",
					data, cleanupCommands(runID, pending),
				)
			}
			if err := lease.MarkTerminal(ctx); err != nil {
				return operationFailure(
					runID, config.BuildCommit, webagent.OperationAuthRefresh,
					webagent.StageObserveTerminal, "browser_observed_auth",
					target, pending, nil, nil,
					"gemini_auth_terminal_state_failed", "internal",
					"Gemini auth terminal state could not be persisted", "",
					data, cleanupCommands(runID, pending),
				)
			}
			data.AuthState = "ready"
			data.CapturedAt = capturedAt
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationAuthRefresh,
				webagent.StateReady, webagent.StageObserveTerminal,
				"browser_observed_auth", target, pending, nil, nil, data,
				[]string{
					"cdp workflow agent gemini doctor --json",
					"cdp workflow agent gemini capabilities refresh --json",
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
		BrowserReads:  "unavailable",
		BrowserMode:   "headed",
		BrowserProbed: false,
	}
	if auth.Ready && runtime.Ready {
		data.BrowserSubmit = "ready"
		data.BrowserReads = "ready"
		result := webagent.NewMetadataResult(
			webagent.ProviderGemini,
			webagent.OperationDoctor,
			data,
			buildCommit,
			[]string{
				"cdp workflow agent gemini capabilities --json",
				"cdp workflow agent gemini conversations list --json",
			},
		)
		result.Evidence.ReadMode = "owner_only_local_state"
		return result
	}
	code := "gemini_auth_" + auth.State
	errClass := "auth"
	message := "Gemini auth evidence is not ready"
	next := []string{"cdp workflow agent gemini auth refresh --json"}
	if auth.Ready && !runtime.Ready {
		code = "gemini_runtime_capabilities_" + runtime.State
		errClass = "capability"
		message = "Gemini runtime capability evidence is not ready"
		next = []string{"cdp workflow agent gemini capabilities refresh --json"}
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
		"gemini_state_unavailable", "internal",
		"Gemini owner-only state is unavailable", "",
		map[string]any{"schema_version": DoctorSchemaVersion},
		[]string{"cdp doctor --json"},
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func observeSessionCookies(
	ctx context.Context,
	session interface {
		Exec(context.Context, string, json.RawMessage) (json.RawMessage, error)
	},
) (map[string]string, int, bool, error) {
	raw, err := session.Exec(
		ctx,
		"Network.getCookies",
		json.RawMessage(`{"urls":["https://gemini.google.com"]}`),
	)
	if err != nil {
		return nil, 0, false, err
	}
	var payload struct {
		Cookies []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, 0, false, fmt.Errorf("decode Gemini cookies")
	}
	values := make(map[string]string, len(payload.Cookies))
	signedIn := false
	for _, cookie := range payload.Cookies {
		name := strings.TrimSpace(cookie.Name)
		values[name] = cookie.Value
		if strings.HasPrefix(name, "__Secure-1PSID") ||
			strings.HasPrefix(name, "__Secure-3PSID") ||
			name == "SID" ||
			strings.HasPrefix(name, "SID") {
			signedIn = true
		}
	}
	return values, len(payload.Cookies), signedIn, nil
}

func retainedDictationTemplate(existing *RequestTemplate, cookies map[string]string, userAgent, apiKey string, capturedAt time.Time) (RequestTemplate, bool) {
	refreshed := RequestTemplate{
		SchemaVersion: RequestTemplateSchemaVersion,
		AuthUser:      "0",
		Source:        "headed-cdp-observed-dictation-template",
	}
	if existing != nil {
		refreshed = *existing
		refreshed.Source = "headed-cdp-retained-dictation-template"
	}
	refreshed.APIKey = strings.TrimSpace(apiKey)
	refreshed.Cookies = make(map[string]string, len(cookies))
	for name, value := range cookies {
		if strings.TrimSpace(name) != "" {
			refreshed.Cookies[name] = value
		}
	}
	for _, name := range []string{"SAPISID", "__Secure-1PAPISID", "__Secure-3PAPISID"} {
		value := strings.TrimSpace(refreshed.Cookies[name])
		if value == "" {
			return RequestTemplate{}, false
		}
	}
	refreshed.BrowserUserAgent = strings.TrimSpace(userAgent)
	if refreshed.BrowserUserAgent == "" {
		return RequestTemplate{}, false
	}
	refreshed.CapturedAt = capturedAt.UTC().Format(time.RFC3339Nano)
	if err := refreshed.Validate(); err != nil {
		return RequestTemplate{}, false
	}
	return refreshed, true
}
