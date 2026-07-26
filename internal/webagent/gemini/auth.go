package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	SignedIn    bool `json:"signed_in"`
	PromptReady bool `json:"prompt_ready"`
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
			var observation authObservation
			_, err := pollUntil(ctx, config.Timeout, config.PollInterval, func() (bool, error) {
				if err := evaluateInto(ctx, session, `(() => {
				  const editor = document.querySelector('[role=textbox][contenteditable=true]');
				  return {signed_in: Boolean(editor), prompt_ready: Boolean(editor)};
				})()`, &observation); err != nil {
					return false, err
				}
				return observation.SignedIn && observation.PromptReady, nil
			})
			cookieCount, sessionCookie, cookieErr := observeSessionCookies(ctx, session)
			data.SignedIn = observation.SignedIn && observation.PromptReady
			data.SessionCookieObserved = sessionCookie
			data.CookieCount = cookieCount
			if err != nil || cookieErr != nil || !data.SignedIn || !sessionCookie {
				_ = lease.MarkIncomplete(context.Background())
				data.AuthState = "signed_out"
				return operationFailure(
					runID, config.BuildCommit, webagent.OperationAuthRefresh,
					webagent.StageObserveTerminal, "browser_observed_auth",
					target, pending, nil, nil,
					"gemini_signed_out", "auth",
					"Signed-in Gemini browser evidence was not observed", "",
					data,
					[]string{
						"Sign in to Gemini in headed Chrome.",
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
) (int, bool, error) {
	raw, err := session.Exec(
		ctx,
		"Network.getCookies",
		json.RawMessage(`{"urls":["https://gemini.google.com"]}`),
	)
	if err != nil {
		return 0, false, err
	}
	var payload struct {
		Cookies []struct {
			Name string `json:"name"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0, false, fmt.Errorf("decode Gemini cookie names")
	}
	for _, cookie := range payload.Cookies {
		name := strings.TrimSpace(cookie.Name)
		if strings.HasPrefix(name, "__Secure-1PSID") ||
			strings.HasPrefix(name, "__Secure-3PSID") ||
			name == "SID" ||
			strings.HasPrefix(name, "SID") {
			return len(payload.Cookies), true, nil
		}
	}
	return len(payload.Cookies), false, nil
}
