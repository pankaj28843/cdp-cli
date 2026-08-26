package m365

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AuthRefreshSchemaVersion  = "m365-auth-refresh/v1"
	DoctorSchemaVersion       = "m365-doctor/v1"
	defaultObservationTimeout = 20 * time.Second
	dictationControlTimeout   = 30 * time.Second
	dictationControlGrace     = 8 * time.Second
	dictationControlPoll      = 250 * time.Millisecond
)

type EventClient interface {
	cdp.CommandClient
	ReadEvent(context.Context) (cdp.Event, error)
}

type BrowserConfig struct {
	Client      EventClient
	Engine      *browserflow.Engine
	Journal     browserflow.Journal
	BuildCommit string
}

type AuthRefreshConfig struct {
	BrowserConfig
	Store              *Store
	ObservationTimeout time.Duration
	Dial               SocketDialer
}

type AuthRefreshData struct {
	SchemaVersion           string `json:"schema_version"`
	AuthState               string `json:"auth_state"`
	TemplatePath            string `json:"template_path"`
	DictationButtonObserved bool   `json:"dictation_button_observed"`
	TokenProviderObserved   bool   `json:"token_provider_observed"`
	DirectWebSocketObserved bool   `json:"direct_websocket_observed"`
	WebSocketObserved       bool   `json:"websocket_observed"`
	TokenProvisioned        bool   `json:"token_provisioned"`
	ClientMetadataObserved  bool   `json:"client_metadata_observed"`
	CapturedAt              string `json:"captured_at,omitempty"`
}

type CapabilitiesRefreshData struct {
	SchemaVersion string              `json:"schema_version"`
	Runtime       RuntimeCapabilities `json:"runtime"`
}

type DoctorData struct {
	SchemaVersion string        `json:"schema_version"`
	Auth          AuthStatus    `json:"auth"`
	Runtime       RuntimeStatus `json:"runtime"`
	BrowserSubmit string        `json:"browser_submit"`
	BrowserMode   string        `json:"browser_mode"`
	BrowserProbed bool          `json:"browser_probed"`
}

type websocketEvent struct {
	RequestID string `json:"requestId"`
	URL       string `json:"url"`
}

type websocketFrameEvent struct {
	RequestID string `json:"requestId"`
	Response  struct {
		Opcode      int    `json:"opcode"`
		PayloadData string `json:"payloadData"`
	} `json:"response"`
}

type clientMetadataWire struct {
	AppName              string `json:"appName"`
	AppPlatform          string `json:"appPlatform"`
	AppVersion           string `json:"appVersion"`
	ReleaseAudienceGroup string `json:"releaseAudienceGroup"`
	ReleaseChannel       string `json:"releaseChannel"`
	ReleaseFork          string `json:"releaseFork"`
	Flights              string `json:"flights"`
	UserSystemTimezone   string `json:"userSystemTimezone"`
	RuntimeVersion       string `json:"runtimeVersion"`
}

type observedProbe struct {
	Template                AuthTemplate
	Runtime                 RuntimeCapabilities
	DictationButtonObserved bool
	DictationObserved       bool
	TokenProviderObserved   bool
	WebSocketObserved       bool
	TokenProvisioned        bool
}

type dictationControlState struct {
	ComposerFound  bool `json:"composer_found"`
	ButtonFound    bool `json:"button_found"`
	CandidateCount int  `json:"candidate_count"`
	ButtonPressed  bool `json:"button_pressed"`
}

// M365's current web composer renders the voice control as a Fluent button
// with a tooltip and microphone SVG, not a stable aria-label. Keep the legacy
// labels for older deployments, but scope the icon fallback to the composer
// and require a visible button-like control so unrelated page controls cannot
// be clicked.
const m365DictationControlExpression = `(() => {
  const root = document.querySelector('#m365-chat-input-shared-container');
  const visible = element => {
    if (!element || element.disabled) return false;
    const style = window.getComputedStyle(element);
    return style.display !== 'none' && style.visibility !== 'hidden' && element.getClientRects().length > 0;
  };
  const composer = [...document.querySelectorAll('[contenteditable="true"], textarea')].find(visible);
  const label = button => [
    button.getAttribute('aria-label'),
    button.getAttribute('title'),
    button.getAttribute('data-testid'),
    button.innerText,
  ].filter(Boolean).join(' ').toLowerCase();
  const hasMicrophoneIcon = button => [...button.querySelectorAll('svg path')].some(path =>
    (path.getAttribute('d') || '').startsWith('M10 13a3 3 0 0 0 3-3V5')
  );
  const inComposer = button => !root || root.contains(button);
  const controls = root ? [...root.querySelectorAll('button,[role="button"]')] : [...document.querySelectorAll('button,[role="button"]')];
  const candidates = controls.filter(button =>
    visible(button) && inComposer(button) && (
      /microphone|dictation|voice input|start dictation|stop dictation/i.test(label(button)) ||
      hasMicrophoneIcon(button)
    )
  );
  const button = candidates[0];
  return {
    composer_found: visible(composer),
    button_found: Boolean(button),
    candidate_count: candidates.length,
    button_pressed: button?.getAttribute('aria-pressed') === 'true',
  };
})()`

const m365DictationClickExpression = `(() => {
  const root = document.querySelector('#m365-chat-input-shared-container');
  const visible = element => {
    if (!element || element.disabled) return false;
    const style = window.getComputedStyle(element);
    return style.display !== 'none' && style.visibility !== 'hidden' && element.getClientRects().length > 0;
  };
  const label = button => [
    button.getAttribute('aria-label'),
    button.getAttribute('title'),
    button.getAttribute('data-testid'),
    button.innerText,
  ].filter(Boolean).join(' ').toLowerCase();
  const hasMicrophoneIcon = button => [...button.querySelectorAll('svg path')].some(path =>
    (path.getAttribute('d') || '').startsWith('M10 13a3 3 0 0 0 3-3V5')
  );
  const inComposer = button => !root || root.contains(button);
  const controls = root ? [...root.querySelectorAll('button,[role="button"]')] : [...document.querySelectorAll('button,[role="button"]')];
  const candidates = controls.filter(button =>
    visible(button) && inComposer(button) && (
      /microphone|dictation|voice input|start dictation|stop dictation/i.test(label(button)) ||
      hasMicrophoneIcon(button)
    )
  );
  const button = candidates.find(candidate =>
    /start dictation/i.test(label(candidate)) ||
    !/stop dictation/i.test(label(candidate))
  );
  if (!button) return {clicked: false};
  button.click();
  return {clicked: true};
})()`

const m365DictationStopExpression = `(() => {
  const root = document.querySelector('#m365-chat-input-shared-container');
  const visible = element => {
    if (!element || element.disabled) return false;
    const style = window.getComputedStyle(element);
    return style.display !== 'none' && style.visibility !== 'hidden' && element.getClientRects().length > 0;
  };
  const label = button => [
    button.getAttribute('aria-label'),
    button.getAttribute('title'),
    button.getAttribute('data-testid'),
    button.innerText,
  ].filter(Boolean).join(' ').toLowerCase();
  const hasMicrophoneIcon = button => [...button.querySelectorAll('svg path')].some(path =>
    (path.getAttribute('d') || '').startsWith('M10 13a3 3 0 0 0 3-3V5')
  );
  const inComposer = button => !root || root.contains(button);
  const controls = root ? [...root.querySelectorAll('button,[role="button"]')] : [...document.querySelectorAll('button,[role="button"]')];
  const candidates = controls.filter(button =>
    visible(button) && inComposer(button) && (
      /microphone|dictation|voice input|start dictation|stop dictation/i.test(label(button)) ||
      hasMicrophoneIcon(button)
    )
  );
  const button = candidates.find(candidate =>
    /stop dictation/i.test(label(candidate)) || candidate.getAttribute('aria-pressed') === 'true'
  );
  if (!button) return {stopped: false};
  button.click();
  return {stopped: true};
})()`

func RefreshAuth(ctx context.Context, config AuthRefreshConfig) webagent.Result {
	data := AuthRefreshData{
		SchemaVersion: AuthRefreshSchemaVersion,
		AuthState:     "blocked",
		TemplatePath:  RelativeTemplatePath,
	}
	return refresh(ctx, config, webagent.OperationAuthRefresh, data)
}

func RefreshCapabilities(ctx context.Context, config AuthRefreshConfig) webagent.Result {
	data := CapabilitiesRefreshData{
		SchemaVersion: "m365-capabilities-refresh/v1",
	}
	return refresh(ctx, config, webagent.OperationCapabilities, data)
}

func refresh(
	ctx context.Context,
	config AuthRefreshConfig,
	operation webagent.Operation,
	data any,
) webagent.Result {
	runID := webagent.NewRunID()
	if config.Store == nil {
		return operationFailure(
			runID, config.BuildCommit, operation, webagent.StagePlanned,
			"browser_observed_auth", nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"m365_state_unavailable", "internal",
			"Microsoft 365 owner-only auth state is unavailable", data,
			[]string{"cdp doctor --json"},
		)
	}
	if config.ObservationTimeout <= 0 {
		config.ObservationTimeout = defaultObservationTimeout
	}
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		operation,
		"dictation-auth-probe",
		HomeURL,
		"browser_observed_auth",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			probe, err := observeProbe(ctx, config, lease.Session())
			if err != nil {
				probeCode := probeErrorCode(err)
				errClass := "auth"
				nextCommands := []string{
					"cdp workflow agent m365 auth refresh --json",
					"cdp workflow agent m365 doctor --json",
				}
				if operation == webagent.OperationCapabilities {
					errClass = "capability"
					nextCommands = []string{
						"cdp workflow agent m365 capabilities refresh --json",
						"cdp workflow agent m365 doctor --json",
					}
				}
				if probeCode == "m365_voice_input_unavailable" {
					nextCommands = []string{"cdp workflow agent m365 doctor --json"}
				}
				result := operationFailure(
					runID, config.BuildCommit, operation, webagent.StageObserveTerminal,
					"browser_observed_auth", target, pending,
					probeCode, errClass, probeErrorMessage(err), data, nextCommands,
				)
				if probeCode == "m365_voice_input_unavailable" && result.Error != nil {
					result.Error.RetrySafe = false
				}
				return result
			}
			if err := config.Store.SaveTemplate(ctx, probe.Template); err != nil {
				return operationFailure(
					runID, config.BuildCommit, operation, webagent.StageObserveTerminal,
					"browser_observed_auth", target, pending,
					"m365_auth_state_write_failed", "internal",
					"Microsoft 365 auth evidence could not be persisted", data,
					[]string{"cdp doctor --json"},
				)
			}
			if err := config.Store.SaveRuntime(ctx, probe.Runtime); err != nil {
				return operationFailure(
					runID, config.BuildCommit, operation, webagent.StageObserveTerminal,
					"browser_observed_auth", target, pending,
					"m365_capabilities_state_write_failed", "internal",
					"Microsoft 365 capability evidence could not be persisted", data,
					[]string{"cdp doctor --json"},
				)
			}
			if operation == webagent.OperationAuthRefresh {
				data = AuthRefreshData{
					SchemaVersion:           AuthRefreshSchemaVersion,
					AuthState:               "ready",
					TemplatePath:            RelativeTemplatePath,
					DictationButtonObserved: probe.DictationButtonObserved,
					TokenProviderObserved:   probe.TokenProviderObserved,
					DirectWebSocketObserved: probe.WebSocketObserved,
					WebSocketObserved:       probe.WebSocketObserved,
					TokenProvisioned:        probe.TokenProvisioned,
					ClientMetadataObserved:  probe.Template.ClientMetadata.AppName != "",
					CapturedAt:              probe.Template.CapturedAt,
				}
			} else {
				data = CapabilitiesRefreshData{
					SchemaVersion: "m365-capabilities-refresh/v1",
					Runtime:       probe.Runtime,
				}
			}
			return operationSuccess(
				runID, config.BuildCommit, operation, webagent.StageObserveTerminal,
				"browser_observed_auth", target, pending, data,
				[]string{
					"cdp workflow agent m365 doctor --json",
					"cdp workflow agent m365 capabilities --json",
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
		return UnavailableOperation(
			buildCommit,
			webagent.OperationDoctor,
			"m365_state_unavailable",
			"internal",
			"Microsoft 365 owner-only auth state is unavailable",
		)
	}
	auth := store.AuthStatus(ctx, now, DefaultAuthTTL)
	runtime := store.RuntimeStatus(ctx, now, DefaultCapabilitiesTTL)
	data := DoctorData{
		SchemaVersion: DoctorSchemaVersion,
		Auth:          auth,
		Runtime:       runtime,
		BrowserSubmit: "unavailable",
		BrowserMode:   "headed",
		BrowserProbed: false,
	}
	if auth.Ready && runtime.Ready {
		data.BrowserSubmit = "ready"
		result := webagent.NewMetadataResult(
			webagent.ProviderM365,
			webagent.OperationDoctor,
			data,
			buildCommit,
			[]string{
				"cdp workflow agent m365 capabilities --json",
				"cdp workflow agent m365 auth refresh --json",
			},
		)
		result.Evidence.ReadMode = "owner_only_local_state"
		return result
	}
	code := "m365_auth_" + auth.State
	errClass := "auth"
	message := "Microsoft 365 auth evidence is not ready"
	next := []string{"cdp workflow agent m365 auth refresh --json"}
	if auth.Ready && !runtime.Ready {
		code = "m365_runtime_capabilities_" + runtime.State
		errClass = "capability"
		message = "Microsoft 365 dictation capability evidence is not ready"
		next = []string{"cdp workflow agent m365 capabilities refresh --json"}
	}
	result := operationFailure(
		webagent.NewRunID(), buildCommit, webagent.OperationDoctor,
		webagent.StageMetadata, "owner_only_local_state", nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		code, errClass, message, data, next,
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func observeProbe(
	ctx context.Context,
	config AuthRefreshConfig,
	session *cdp.PageSession,
) (observedProbe, error) {
	if err := preparePage(ctx, config.Client, session, HomeURL); err != nil {
		return observedProbe{}, fmt.Errorf("prepare Microsoft 365 page: %w", err)
	}
	var userAgent struct {
		UserAgent string `json:"userAgent"`
	}
	if err := config.Client.Call(ctx, "Browser.getVersion", map[string]any{}, &userAgent); err != nil {
		return observedProbe{}, fmt.Errorf("observe browser user agent: %w", err)
	}
	if strings.TrimSpace(userAgent.UserAgent) == "" {
		return observedProbe{}, fmt.Errorf("browser user agent was empty")
	}
	directProbe, directErr := observeDirectProbe(ctx, config, session, userAgent.UserAgent)
	if directErr == nil {
		return directProbe, nil
	}
	if !isDirectProviderUnavailable(directErr) {
		return observedProbe{}, directErr
	}
	legacyProbe, legacyErr := observeLegacyProbe(ctx, config, session, userAgent.UserAgent)
	if legacyErr != nil {
		return observedProbe{}, fmt.Errorf("direct M365 auth provider unavailable: %v; legacy dictation probe failed: %w", directErr, legacyErr)
	}
	return legacyProbe, nil
}

func observeDirectProbe(
	ctx context.Context,
	config AuthRefreshConfig,
	session *cdp.PageSession,
	userAgent string,
) (observedProbe, error) {
	var observation directAuthObservation
	timeout := config.ObservationTimeout
	if timeout <= 0 {
		timeout = defaultObservationTimeout
	}
	if err := pollUntil(ctx, timeout, dictationControlPoll, func() (bool, error) {
		var current directAuthObservation
		if err := evaluateInto(ctx, session, m365DirectAuthExpression, &current); err != nil {
			return false, err
		}
		observation = current
		if !observation.TokenProviderFound {
			return false, nil
		}
		if observation.TokenProviderError {
			return false, nil
		}
		return strings.TrimSpace(observation.AuthToken) != "", nil
	}); err != nil {
		if !observation.TokenProviderFound {
			return observedProbe{}, fmt.Errorf("Microsoft 365 direct auth token provider was not found: %w", err)
		}
		return observedProbe{}, fmt.Errorf("Microsoft 365 direct auth token provider failed: %w", err)
	}
	metadata := observation.ClientMetadata
	if err := metadata.Validate(); err != nil {
		return observedProbe{}, fmt.Errorf("validate direct Microsoft 365 client metadata: %w", err)
	}
	now := time.Now().UTC()
	template := AuthTemplate{
		SchemaVersion:    AuthTemplateSchemaVersion,
		AuthToken:        strings.TrimSpace(observation.AuthToken),
		ClientMetadata:   metadata,
		BrowserUserAgent: userAgent,
		CapturedAt:       now.Format(time.RFC3339Nano),
		Source:           directAuthTemplateSource,
	}
	if err := template.Validate(); err != nil {
		return observedProbe{}, fmt.Errorf("validate direct Microsoft 365 auth: %w", err)
	}
	if err := probeDirectTransport(ctx, template, config.Dial); err != nil {
		return observedProbe{}, fmt.Errorf("Microsoft 365 direct AugLoop WebSocket probe failed: %w", err)
	}
	runtime := RuntimeCapabilities{
		SchemaVersion:     RuntimeCapabilitiesSchemaVersion,
		State:             "ready",
		CapturedAt:        now.Format(time.RFC3339Nano),
		ComposerObserved:  observation.ComposerFound,
		DictationObserved: true,
		WebSocketObserved: true,
		TokenProvisioned:  true,
		AudioProtocol:     "AugLoop_Voice_VoiceTile/v2",
		Source:            directRuntimeSource,
		Message:           "Observed the live M365 AugLoop token provider and direct VoiceTile WebSocket session.",
	}
	if err := runtime.Validate(); err != nil {
		return observedProbe{}, fmt.Errorf("validate direct Microsoft 365 capabilities: %w", err)
	}
	return observedProbe{
		Template:                template,
		Runtime:                 runtime,
		DictationButtonObserved: false,
		DictationObserved:       true,
		TokenProviderObserved:   true,
		WebSocketObserved:       true,
		TokenProvisioned:        true,
	}, nil
}

func probeDirectTransport(ctx context.Context, template AuthTemplate, dial SocketDialer) error {
	if dial == nil {
		dial = augloop.Dial
	}
	session, failure := openLiveSession(ctx, template, TranscribeConfig{Dial: dial})
	if failure != nil {
		return failure
	}
	session.close()
	return nil
}

func isDirectProviderUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "direct auth token provider was not found")
}

func observeLegacyProbe(
	ctx context.Context,
	config AuthRefreshConfig,
	session *cdp.PageSession,
	userAgent string,
) (observedProbe, error) {
	var control dictationControlState
	composerObservedAt := time.Time{}
	if err := pollUntil(ctx, dictationControlTimeout, dictationControlPoll, func() (bool, error) {
		if err := evaluateInto(ctx, session, m365DictationControlExpression, &control); err != nil {
			return false, err
		}
		if control.ButtonFound {
			return true, nil
		}
		if !control.ComposerFound {
			composerObservedAt = time.Time{}
			return false, nil
		}
		if composerObservedAt.IsZero() {
			composerObservedAt = time.Now()
			return false, nil
		}
		if time.Since(composerObservedAt) >= dictationControlGrace {
			return false, fmt.Errorf("Microsoft 365 voice input was unavailable on the exact headed target")
		}
		return false, nil
	}); err != nil {
		return observedProbe{}, fmt.Errorf("Microsoft 365 dictation control was not observed: %w", err)
	}
	var clicked struct {
		Clicked bool `json:"clicked"`
	}
	if err := evaluateInto(ctx, session, m365DictationClickExpression, &clicked); err != nil || !clicked.Clicked {
		return observedProbe{}, fmt.Errorf("Microsoft 365 dictation control could not be triggered")
	}

	metadata := ClientMetadata{}
	var authToken string
	websocketObserved := false
	websocketIDs := map[string]bool{}
	deadline, cancel := context.WithTimeout(ctx, config.ObservationTimeout)
	defer cancel()
	for authToken == "" {
		event, err := config.Client.ReadEvent(deadline)
		if err != nil {
			return observedProbe{}, fmt.Errorf("Microsoft 365 auth token was not observed: %w", err)
		}
		switch event.Method {
		case "Network.webSocketCreated", "Network.webSocketWillSendHandshakeRequest":
			var payload websocketEvent
			if json.Unmarshal(event.Params, &payload) == nil && strings.Contains(strings.ToLower(payload.URL), "augloop.svc.cloud.microsoft") {
				websocketObserved = true
				websocketIDs[payload.RequestID] = true
			}
		case "Network.webSocketFrameSent":
			var payload websocketFrameEvent
			if json.Unmarshal(event.Params, &payload) != nil || payload.Response.Opcode != 1 {
				continue
			}
			if len(websocketIDs) > 0 && !websocketIDs[payload.RequestID] {
				continue
			}
			var message map[string]any
			if json.Unmarshal([]byte(payload.Response.PayloadData), &message) != nil {
				continue
			}
			if typeName(message) == "AugLoop_Session_Protocol_SessionInitMessage" {
				metadata = metadataFromMessage(message)
				websocketObserved = true
			}
			if typeName(message) == "AugLoop_Session_Protocol_TokenProvisionMessage" {
				if value, ok := message["authToken"].(string); ok {
					authToken = strings.TrimSpace(value)
					websocketObserved = true
				}
			}
		}
	}
	_ = evaluateInto(ctx, session, m365DictationStopExpression, &struct {
		Stopped bool `json:"stopped"`
	}{})
	if metadata.AppName == "" {
		return observedProbe{}, fmt.Errorf("Microsoft 365 session metadata was not observed")
	}
	now := time.Now().UTC()
	template := AuthTemplate{
		SchemaVersion:    AuthTemplateSchemaVersion,
		AuthToken:        authToken,
		ClientMetadata:   metadata,
		BrowserUserAgent: userAgent,
		CapturedAt:       now.Format(time.RFC3339Nano),
		Source:           legacyAuthTemplateSource,
	}
	if err := template.Validate(); err != nil {
		return observedProbe{}, fmt.Errorf("validate observed Microsoft 365 auth: %w", err)
	}
	runtime := RuntimeCapabilities{
		SchemaVersion:     RuntimeCapabilitiesSchemaVersion,
		State:             "ready",
		CapturedAt:        now.Format(time.RFC3339Nano),
		ComposerObserved:  true,
		DictationObserved: true,
		WebSocketObserved: websocketObserved,
		TokenProvisioned:  true,
		AudioProtocol:     "AugLoop_Voice_VoiceTile/v2",
		Source:            legacyRuntimeSource,
		Message:           "Observed M365 Copilot dictation session initialization and auth token provision.",
	}
	if err := runtime.Validate(); err != nil {
		return observedProbe{}, fmt.Errorf("validate observed Microsoft 365 capabilities: %w", err)
	}
	return observedProbe{
		Template:                template,
		Runtime:                 runtime,
		DictationButtonObserved: true,
		DictationObserved:       true,
		TokenProviderObserved:   false,
		WebSocketObserved:       websocketObserved,
		TokenProvisioned:        true,
	}, nil
}

func typeName(message map[string]any) string {
	header, ok := message["H_"].(map[string]any)
	if !ok {
		return ""
	}
	value, _ := header["T_"].(string)
	return value
}

func metadataFromMessage(message map[string]any) ClientMetadata {
	raw, _ := message["clientMetadata"].(map[string]any)
	encoded, _ := json.Marshal(raw)
	var wire clientMetadataWire
	_ = json.Unmarshal(encoded, &wire)
	return ClientMetadata{
		AppName:              wire.AppName,
		AppPlatform:          wire.AppPlatform,
		AppVersion:           wire.AppVersion,
		ReleaseAudienceGroup: wire.ReleaseAudienceGroup,
		ReleaseChannel:       wire.ReleaseChannel,
		ReleaseFork:          wire.ReleaseFork,
		Flights:              wire.Flights,
		UserSystemTimezone:   wire.UserSystemTimezone,
		RuntimeVersion:       wire.RuntimeVersion,
	}
}

func preparePage(ctx context.Context, client cdp.CommandClient, session *cdp.PageSession, rawURL string) error {
	for _, method := range []string{"Runtime.enable", "Page.enable", "Network.enable"} {
		if err := client.CallSession(ctx, session.SessionID, method, map[string]any{}, nil); err != nil {
			return err
		}
	}
	if err := cdp.ActivateTargetWithClient(ctx, client, session.TargetID); err != nil {
		return err
	}
	_, err := session.Navigate(ctx, rawURL)
	return err
}

func evaluateInto(ctx context.Context, session *cdp.PageSession, expression string, target any) error {
	evaluated, err := session.Evaluate(ctx, expression, true)
	if err != nil {
		return err
	}
	if evaluated.Exception != nil || len(evaluated.Object.Value) == 0 {
		return fmt.Errorf("exact-target evaluation failed")
	}
	return json.Unmarshal(evaluated.Object.Value, target)
}

func pollUntil(ctx context.Context, timeout, interval time.Duration, observe func() (bool, error)) error {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		ready, err := observe()
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-deadline.Done():
			timer.Stop()
			return deadline.Err()
		case <-timer.C:
		}
	}
}

type ownedCallback func(*browserflow.Lease, *webagent.TargetEvidence, webagent.CleanupEvidence) webagent.Result

func runOwned(
	ctx context.Context,
	config BrowserConfig,
	runID string,
	operation webagent.Operation,
	actionName string,
	initialURL string,
	readMode string,
	data any,
	callback ownedCallback,
) (result webagent.Result) {
	if config.Client == nil || config.Engine == nil || config.Journal == nil || callback == nil {
		return operationFailure(
			runID, config.BuildCommit, operation, webagent.StagePlanned, readMode, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"m365_browserflow_unavailable", "internal",
			"Microsoft 365 exact-target browser transaction is not configured", data,
			[]string{"cdp workflow agent m365 doctor --json"},
		)
	}
	lease, err := config.Engine.Acquire(ctx, browserflow.AcquireRequest{
		RunID: runID, Provider: string(webagent.ProviderM365), Operation: string(operation),
		ActionName: actionName, InitialURL: initialURL,
	})
	if err != nil {
		return operationFailure(
			runID, config.BuildCommit, operation, webagent.StagePlanned, readMode, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"m365_browser_start_failed", "connection",
			"Microsoft 365 workflow could not acquire one exact headed target", data,
			[]string{"cdp --browser-mode headed daemon status --json", "cdp workflow agent m365 doctor --json"},
		)
	}
	target := &webagent.TargetEvidence{TargetID: lease.TargetID(), SessionID: lease.Session().SessionID, Owned: true, Created: true}
	pending := webagent.CleanupEvidence{Required: true, State: webagent.CleanupPending, TargetID: lease.TargetID()}
	result = callback(lease, target, pending)
	cleanup, _ := lease.Close(context.Background())
	if cleanup.State != browserflow.CleanupClosed || !cleanup.TargetGone {
		target.Closed = false
		result = replaceCleanupFailure(result, target, cleanup, runID)
		return result
	}
	target.Closed = true
	result.Evidence.Target = target
	result.Cleanup = webagent.CleanupEvidence{
		Required: true, State: webagent.CleanupClosed, TargetID: lease.TargetID(),
		TargetClosed: true, CloseAttemptCount: cleanup.CloseAttemptCount,
		CloseSent: cleanup.CloseSent, TargetPollObserved: cleanup.TargetPollObserved,
		CloseProof: "exact_target_absent_after_close",
	}
	result.Stage = webagent.StageClosed
	return result
}

func replaceCleanupFailure(result webagent.Result, target *webagent.TargetEvidence, cleanup browserflow.CleanupResult, runID string) webagent.Result {
	result.OK = false
	result.State = webagent.StateFailed
	result.Error = &webagent.OperationError{
		Code: "m365_exact_target_cleanup_failed", ErrClass: "cleanup",
		Message: "Microsoft 365 workflow could not prove exact target cleanup", RetrySafe: true,
	}
	result.Evidence.Target = target
	result.Cleanup = webagent.CleanupEvidence{
		Required: true, State: webagent.CleanupFailed, TargetID: cleanup.TargetID,
		CloseAttemptCount: cleanup.CloseAttemptCount, CloseSent: cleanup.CloseSent,
		TargetPollObserved: cleanup.TargetPollObserved, FailurePhase: cleanup.FailurePhase,
	}
	result.Stage = webagent.StageCleanupPending
	result.NextCommands = cleanupCommands(runID, result.Operation, result.Cleanup)
	return result
}

func probeErrorCode(err error) string {
	if strings.Contains(err.Error(), "voice input was unavailable") {
		return "m365_voice_input_unavailable"
	}
	if strings.Contains(err.Error(), "direct AugLoop WebSocket probe failed") {
		return "m365_direct_websocket_probe_failed"
	}
	if strings.Contains(err.Error(), "token") {
		return "m365_auth_token_not_observed"
	}
	if strings.Contains(err.Error(), "dictation") {
		return "m365_dictation_probe_failed"
	}
	return "m365_auth_observation_failed"
}

func probeErrorMessage(err error) string {
	if strings.Contains(err.Error(), "voice input was unavailable") {
		return "Microsoft 365 Copilot did not expose its voice-input control or runtime token provider on the exact headed target; the current account or runtime may not be eligible for dictation"
	}
	if strings.Contains(err.Error(), "direct AugLoop WebSocket probe failed") {
		return "Microsoft 365 exposed its live auth provider, but the direct AugLoop VoiceTile WebSocket probe failed"
	}
	if strings.Contains(err.Error(), "token") {
		return "Microsoft 365 did not expose a fresh AugLoop auth token during the bounded dictation probe"
	}
	if strings.Contains(err.Error(), "dictation") {
		return "Microsoft 365 Copilot dictation was not observable on the exact headed target"
	}
	return "Microsoft 365 auth observation failed on the exact headed target"
}

func UnavailableDoctor(buildCommit string) webagent.Result {
	return UnavailableOperation(buildCommit, webagent.OperationDoctor, "m365_state_unavailable", "internal", "Microsoft 365 owner-only auth state is unavailable")
}
