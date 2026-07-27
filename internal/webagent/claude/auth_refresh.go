package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AuthRefreshSchemaVersion   = "claude-auth-refresh/v1"
	defaultObservationTimeout  = 10 * time.Second
	defaultObservationAttempts = 3
)

type EventClient interface {
	cdp.CommandClient
	ReadEvent(context.Context) (cdp.Event, error)
}

type AuthRefreshConfig struct {
	Client              EventClient
	Engine              *browserflow.Engine
	Journal             browserflow.Journal
	Store               *Store
	BuildCommit         string
	ObservationTimeout  time.Duration
	ObservationAttempts int
	Now                 func() time.Time
}

type AuthRefreshData struct {
	SchemaVersion         string `json:"schema_version"`
	AuthState             string `json:"auth_state"`
	TemplatePath          string `json:"template_path"`
	OrganizationDerived   bool   `json:"organization_derived"`
	SessionCookieObserved bool   `json:"session_cookie_observed"`
	CookieCount           int    `json:"cookie_count"`
	RequestShape          string `json:"request_shape"`
	CapturedAt            string `json:"captured_at,omitempty"`
}

type requestRecord struct {
	ID          string
	Method      string
	URL         string
	Headers     map[string]any
	Status      int
	ResponseURL string
}

type authObservation struct {
	OrganizationID string
	ListURL        string
	Headers        map[string]string
	Source         string
	RequestShape   string
}

type networkObserver struct {
	records map[string]*requestRecord
	order   []string
}

func UnavailableAuthRefresh(
	buildCommit string,
	code string,
	errClass string,
	message string,
	nextCommands []string,
) webagent.Result {
	return authRefreshFailure(
		webagent.NewRunID(),
		buildCommit,
		webagent.StagePlanned,
		nil,
		webagent.CleanupEvidence{Required: false, State: webagent.CleanupNotRequired},
		code,
		errClass,
		message,
		"",
		AuthRefreshData{
			SchemaVersion: AuthRefreshSchemaVersion,
			AuthState:     "blocked",
			TemplatePath:  RelativeTemplatePath,
			RequestShape:  "not_observed",
		},
		nextCommands,
	)
}

func RefreshAuth(ctx context.Context, config AuthRefreshConfig) (result webagent.Result) {
	runID := webagent.NewRunID()
	now := time.Now
	if config.Now != nil {
		now = config.Now
	}
	if config.ObservationTimeout <= 0 {
		config.ObservationTimeout = defaultObservationTimeout
	}
	if config.ObservationAttempts < defaultObservationAttempts {
		config.ObservationAttempts = defaultObservationAttempts
	}
	baseData := AuthRefreshData{
		SchemaVersion: AuthRefreshSchemaVersion,
		AuthState:     "blocked",
		TemplatePath:  RelativeTemplatePath,
		RequestShape:  "not_observed",
	}
	result = authRefreshFailure(
		runID,
		config.BuildCommit,
		webagent.StagePlanned,
		nil,
		webagent.CleanupEvidence{Required: false, State: webagent.CleanupNotRequired},
		"claude_auth_refresh_unavailable",
		"internal",
		"Claude auth refresh is not configured",
		"",
		baseData,
		[]string{"cdp workflow agent claude doctor --json"},
	)
	if config.Client == nil ||
		config.Engine == nil ||
		config.Journal == nil ||
		config.Store == nil {
		return result
	}

	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID:      runID,
		Provider:   string(webagent.ProviderClaude),
		Operation:  string(webagent.OperationAuthRefresh),
		InitialURL: "about:blank",
	})
	if err != nil {
		target, cleanup, stage := reconcileAcquireFailure(config, runID)
		code, errClass, message := classifyAcquireFailure(err)
		if cleanup.State == webagent.CleanupFailed || cleanup.State == webagent.CleanupPending {
			code = "claude_exact_target_cleanup_failed"
			errClass = "cleanup"
			message = "Claude auth refresh could not prove exact target cleanup"
		}
		return authRefreshFailure(
			runID,
			config.BuildCommit,
			stage,
			target,
			cleanup,
			code,
			errClass,
			message,
			"",
			baseData,
			authRefreshNextCommands(runID, cleanup),
		)
	}

	target := &webagent.TargetEvidence{
		TargetID:  lease.TargetID(),
		SessionID: lease.Session().SessionID,
		Owned:     true,
		Created:   true,
		Closed:    false,
	}
	pendingCleanup := webagent.CleanupEvidence{
		Required: true,
		State:    webagent.CleanupPending,
		TargetID: lease.TargetID(),
	}
	defer func() {
		cleanup, closeErr := lease.Close(context.Background())
		if closeErr != nil || cleanup.State != browserflow.CleanupClosed || !cleanup.TargetGone {
			target.Closed = false
			result.Evidence.Target = target
			result.Cleanup = webagent.CleanupEvidence{
				Required: true,
				State:    webagent.CleanupFailed,
				TargetID: lease.TargetID(),
			}
			result.Stage = webagent.StageCleanupPending
			result = replaceAuthRefreshFailure(
				result,
				"claude_exact_target_cleanup_failed",
				"cleanup",
				"Claude auth refresh could not prove exact target cleanup",
				"",
				authRefreshNextCommands(runID, result.Cleanup),
			)
			return
		}
		target.Closed = true
		result.Evidence.Target = target
		result.Cleanup = webagent.CleanupEvidence{
			Required:     true,
			State:        webagent.CleanupClosed,
			TargetID:     lease.TargetID(),
			TargetClosed: true,
			CloseProof:   "exact_target_absent_after_close",
		}
		result.Stage = webagent.StageClosed
	}()

	session := lease.Session()
	browserUserAgent, err := prepareAuthObservation(ctx, config.Client, session)
	if err != nil {
		return authRefreshFailure(
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			"claude_auth_prepare_failed",
			"connection",
			"Claude auth observation could not prepare the exact headed target",
			"",
			baseData,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		return authRefreshFailure(
			runID,
			config.BuildCommit,
			webagent.StageAttached,
			target,
			pendingCleanup,
			"claude_auth_prepare_state_failed",
			"internal",
			"Claude auth observation preparation could not be persisted",
			"",
			baseData,
			authRefreshNextCommands(runID, pendingCleanup),
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
			observedCookies, cookieErr := readClaudeCookies(
				observationCtx,
				session,
			)
			if cookieErr != nil {
				return false, cookieErr
			}
			baseData.CookieCount = len(observedCookies)
			baseData.SessionCookieObserved =
				hasSessionCookie(observedCookies)
			if !baseData.SessionCookieObserved {
				return false, nil
			}
			cookies = observedCookies
			return true, nil
		},
	)
	if readinessErr != nil || readiness.ObservationFailed() {
		_ = lease.MarkIncomplete(context.Background())
		return authRefreshFailure(
			runID,
			config.BuildCommit,
			webagent.StagePrepared,
			target,
			pendingCleanup,
			"claude_auth_readiness_failed",
			"connection",
			"Claude auth readiness could not complete its bounded load, reload, hard-reload, and grace-wait sequence",
			"",
			baseData,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if !readiness.Observed {
		_ = lease.MarkIncomplete(context.Background())
		baseData.AuthState = "evidence_not_observed"
		return authRefreshFailure(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			target,
			pendingCleanup,
			"claude_auth_evidence_not_observed",
			"auth",
			"Claude auth evidence was not observed after initial load, reload, cache-bypassing hard reload, and final grace wait; the browser session may still be active",
			"",
			baseData,
			[]string{
				"cdp workflow agent claude auth refresh --json",
			},
		)
	}

	existing := loadExistingTemplate(ctx, config.Store)
	observation, found, err := observeAuthRequest(
		ctx,
		config.Client,
		session,
		existing,
		config.ObservationAttempts,
		config.ObservationTimeout,
	)
	if err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return authRefreshFailure(
			runID,
			config.BuildCommit,
			webagent.StagePrepared,
			target,
			pendingCleanup,
			"claude_auth_observation_failed",
			"connection",
			"Claude auth request observation failed on the exact headed target",
			"",
			baseData,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if !found {
		_ = lease.MarkIncomplete(context.Background())
		baseData.AuthState = "request_not_observed"
		return authRefreshFailure(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			target,
			pendingCleanup,
			"claude_list_request_not_observed",
			"auth",
			"Signed-in Claude list request evidence was not observed",
			"",
			baseData,
			[]string{
				"cdp workflow agent claude auth refresh --json",
			},
		)
	}

	capturedAt := now().UTC().Format(time.RFC3339Nano)
	template := AuthTemplate{
		SchemaVersion:    AuthTemplateSchemaVersion,
		Method:           "GET",
		Origin:           Origin,
		OrganizationID:   observation.OrganizationID,
		ListURL:          observation.ListURL,
		Headers:          observation.Headers,
		Cookies:          cookies,
		BrowserUserAgent: browserUserAgent,
		CapturedAt:       capturedAt,
		Source:           observation.Source,
	}
	if err := config.Store.Save(ctx, template); err != nil {
		_ = lease.MarkIncomplete(context.Background())
		return authRefreshFailure(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			target,
			pendingCleanup,
			"claude_auth_state_write_failed",
			"internal",
			"Claude auth evidence could not be persisted to owner-only state",
			"",
			baseData,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}
	if err := lease.MarkTerminal(ctx); err != nil {
		return authRefreshFailure(
			runID,
			config.BuildCommit,
			webagent.StageObserveTerminal,
			target,
			pendingCleanup,
			"claude_auth_terminal_state_failed",
			"internal",
			"Claude auth terminal state could not be persisted",
			"",
			baseData,
			authRefreshNextCommands(runID, pendingCleanup),
		)
	}

	data := AuthRefreshData{
		SchemaVersion:         AuthRefreshSchemaVersion,
		AuthState:             "ready",
		TemplatePath:          RelativeTemplatePath,
		OrganizationDerived:   true,
		SessionCookieObserved: true,
		CookieCount:           len(cookies),
		RequestShape:          observation.RequestShape,
		CapturedAt:            capturedAt,
	}
	return webagent.Result{
		OK:            true,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationAuthRefresh,
		State:         webagent.StateReady,
		Stage:         webagent.StageObserveTerminal,
		Data:          data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(config.BuildCommit),
			BrowserMode: "headed",
			ReadMode:    "browser_observed_auth",
			Target:      target,
		},
		Cleanup: pendingCleanup,
		NextCommands: []string{
			"cdp workflow agent claude doctor --json",
			"cdp workflow agent claude capabilities --json",
		},
	}
}

func prepareAuthObservation(ctx context.Context, client EventClient, session *cdp.PageSession) (string, error) {
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

func observeAuthRequest(
	ctx context.Context,
	client EventClient,
	session *cdp.PageSession,
	existing *AuthTemplate,
	attempts int,
	wait time.Duration,
) (authObservation, bool, error) {
	observer := networkObserver{records: map[string]*requestRecord{}}
	var selected authObservation
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
			observation, ok := observer.selectObservation(existing)
			if ok {
				selected = observation
			}
			return ok, nil
		},
	)
	if err != nil {
		return authObservation{}, false, err
	}
	if readiness.ObservationFailed() {
		return authObservation{}, false, readiness.LastObservationError
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

func (o *networkObserver) selectObservation(existing *AuthTemplate) (authObservation, bool) {
	for _, id := range o.order {
		record := o.records[id]
		organizationID, list := claudeOrganizationRequest(record)
		if !list {
			continue
		}
		return authObservation{
			OrganizationID: organizationID,
			ListURL:        record.URL,
			Headers:        NormalizeReplayHeaders(record.Headers),
			Source:         "headed-cdp-observed-list-request",
			RequestShape:   "observed_list",
		}, true
	}
	if existing == nil {
		return authObservation{}, false
	}
	for _, id := range o.order {
		record := o.records[id]
		organizationID, ok := claudeSuccessfulOrganizationRequest(record)
		if !ok || organizationID != existing.OrganizationID {
			continue
		}
		return authObservation{
			OrganizationID: existing.OrganizationID,
			ListURL:        existing.ListURL,
			Headers:        NormalizeReplayHeaders(record.Headers),
			Source:         "headed-cdp-retained-list-shape",
			RequestShape:   "retained_observed_organization",
		}, true
	}
	return authObservation{}, false
}

func claudeOrganizationRequest(record *requestRecord) (string, bool) {
	organizationID, ok := claudeSuccessfulOrganizationRequest(record)
	if !ok {
		return "", false
	}
	parsed, _ := url.Parse(record.URL)
	expectedPath := "/api/organizations/" + organizationID + "/chat_conversations_v2"
	if parsed.Path != expectedPath || parsed.Query().Get("starred") != "false" {
		return "", false
	}
	response, err := url.Parse(record.ResponseURL)
	if err != nil || response.Path != expectedPath {
		return "", false
	}
	return organizationID, true
}

func claudeSuccessfulOrganizationRequest(record *requestRecord) (string, bool) {
	if record == nil ||
		record.Method != "GET" ||
		record.Status != 200 ||
		record.URL == "" ||
		record.ResponseURL == "" {
		return "", false
	}
	requestOrganization, requestOK := claudeOrganizationFromURL(record.URL)
	responseOrganization, responseOK := claudeOrganizationFromURL(record.ResponseURL)
	if !requestOK || !responseOK || requestOrganization != responseOrganization {
		return "", false
	}
	return requestOrganization, true
}

func claudeOrganizationFromURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host != "claude.ai" ||
		parsed.User != nil ||
		parsed.Fragment != "" {
		return "", false
	}
	const prefix = "/api/organizations/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return "", false
	}
	remainder := strings.TrimPrefix(parsed.Path, prefix)
	organizationID, _, found := strings.Cut(remainder, "/")
	if !found || !organizationPattern.MatchString(organizationID) {
		return "", false
	}
	return organizationID, true
}

func readClaudeCookies(ctx context.Context, session *cdp.PageSession) (map[string]string, error) {
	raw, err := session.Exec(ctx, "Network.getCookies", json.RawMessage(`{"urls":["https://claude.ai"]}`))
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
		return nil, err
	}
	cookies := make(map[string]string)
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
	return cookies["sessionKey"] != "" || cookies["sessionKeyLC"] != ""
}

func loadExistingTemplate(ctx context.Context, store *Store) *AuthTemplate {
	template, err := store.Load(ctx)
	if err != nil {
		return nil
	}
	return &template
}

func reconcileAcquireFailure(config AuthRefreshConfig, runID string) (*webagent.TargetEvidence, webagent.CleanupEvidence, webagent.Stage) {
	recoveryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cleanup, recoverErr := config.Engine.Recover(recoveryCtx, runID)
	record, loadErr := config.Journal.Load(recoveryCtx, runID)
	if loadErr != nil || record.TargetID == "" {
		return nil, webagent.CleanupEvidence{
			Required: false,
			State:    webagent.CleanupNotRequired,
		}, webagent.StagePlanned
	}
	target := &webagent.TargetEvidence{
		TargetID:  record.TargetID,
		SessionID: record.SessionID,
		Owned:     true,
		Created:   true,
		Closed:    recoverErr == nil && cleanup.TargetGone,
	}
	if target.Closed {
		return target, webagent.CleanupEvidence{
			Required:     true,
			State:        webagent.CleanupClosed,
			TargetID:     record.TargetID,
			TargetClosed: true,
			CloseProof:   "exact_target_absent_after_recovery",
		}, webagent.StageClosed
	}
	return target, webagent.CleanupEvidence{
		Required: true,
		State:    webagent.CleanupFailed,
		TargetID: record.TargetID,
	}, webagent.StageCleanupPending
}

func classifyAcquireFailure(err error) (string, string, string) {
	var budget *browserflow.BudgetExceededError
	if errors.As(err, &budget) {
		return "claude_browser_resource_budget_exceeded", "resource", "Claude auth refresh was blocked by the headed browser resource budget"
	}
	return "claude_browser_start_failed", "connection", "Claude auth refresh could not acquire one exact headed target"
}

func authRefreshFailure(
	runID string,
	buildCommit string,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	code string,
	errClass string,
	message string,
	retryAt string,
	data AuthRefreshData,
	nextCommands []string,
) webagent.Result {
	if nextCommands == nil {
		nextCommands = []string{}
	}
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationAuthRefresh,
		State:         webagent.StateFailed,
		Stage:         stage,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  errClass,
			Message:   message,
			RetrySafe: true,
			RetryAt:   retryAt,
		},
		Data: data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "headed",
			ReadMode:    "browser_observed_auth",
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: webagent.CloneCommands(nextCommands),
	}
}

func replaceAuthRefreshFailure(
	result webagent.Result,
	code string,
	errClass string,
	message string,
	retryAt string,
	nextCommands []string,
) webagent.Result {
	result.OK = false
	result.State = webagent.StateFailed
	result.Error = &webagent.OperationError{
		Code:      code,
		ErrClass:  errClass,
		Message:   message,
		RetrySafe: true,
		RetryAt:   retryAt,
	}
	result.NextCommands = webagent.CloneCommands(nextCommands)
	return result
}

func authRefreshNextCommands(_ string, _ webagent.CleanupEvidence) []string {
	return nil
}
