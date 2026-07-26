package perplexity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	CapabilityRefreshSchemaVersion = "perplexity-capabilities-refresh/v1"
	maxModelConfigResponseBytes    = 4 << 20
)

var capabilitySlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

type CapabilityRefreshConfig struct {
	BrowserConfig
	Store               *Store
	ObservationTimeout  time.Duration
	ObservationAttempts int
	Now                 func() time.Time
}

type CapabilityRefreshData struct {
	SchemaVersion string               `json:"schema_version"`
	RuntimeState  string               `json:"runtime_state"`
	StatePath     string               `json:"state_path"`
	Capabilities  []ComposerCapability `json:"capabilities"`
	CapturedAt    string               `json:"captured_at,omitempty"`
	Message       string               `json:"message,omitempty"`
}

type modelConfigResponseRecord struct {
	RequestID string
	Status    int
}

func RefreshCapabilities(
	ctx context.Context,
	config CapabilityRefreshConfig,
) webagent.Result {
	runID := webagent.NewRunID()
	data := CapabilityRefreshData{
		SchemaVersion: CapabilityRefreshSchemaVersion,
		RuntimeState:  "blocked",
		StatePath:     RelativeCapabilitiesPath,
		Capabilities:  []ComposerCapability{},
	}
	if config.Store == nil {
		return capabilityFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"perplexity_state_unavailable", "internal",
			"Perplexity owner-only runtime capability state is unavailable", data,
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
		webagent.OperationCapabilities,
		"",
		"about:blank",
		"browser_observed_runtime",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			if err := preparePage(ctx, config.Client, session, HomeURL); err != nil {
				return capabilityFailure(
					runID, config, webagent.StageAttached, target, pending,
					"perplexity_capabilities_prepare_failed", "connection",
					"Perplexity runtime observation could not prepare the exact headed target",
					data,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return capabilityFailure(
					runID, config, webagent.StageAttached, target, pending,
					"perplexity_capabilities_prepare_state_failed", "internal",
					"Perplexity runtime observation preparation could not be persisted",
					data,
				)
			}
			payload, found, err := observeModelConfigResponse(
				ctx,
				config.Client,
				session,
				config.ObservationAttempts,
				config.ObservationTimeout,
			)
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"perplexity_capabilities_observation_failed", "connection",
					"Perplexity model catalog observation failed on the exact headed target",
					data,
				)
			}
			now := time.Now
			if config.Now != nil {
				now = config.Now
			}
			capturedAt := now().UTC().Format(time.RFC3339Nano)
			runtime := RuntimeCapabilities{
				SchemaVersion: RuntimeCapabilitiesSchemaVersion,
				State:         "unknown",
				CapturedAt:    capturedAt,
				Capabilities:  []ComposerCapability{},
				Source:        "headed-cdp-model-config-not-observed",
				Message: "Perplexity default ask remains available; no optional model " +
					"catalog was observed during this refresh.",
			}
			if found {
				runtime, err = parseRuntimeCapabilities(payload, capturedAt)
				if err != nil {
					_ = lease.MarkIncomplete(context.Background())
					return capabilityFailure(
						runID, config, webagent.StageObserveTerminal, target, pending,
						"perplexity_model_config_invalid", "capability",
						"Perplexity model catalog response was invalid",
						data,
					)
				}
			}
			if err := config.Store.SaveRuntime(ctx, runtime); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"perplexity_capabilities_state_write_failed", "internal",
					"Perplexity runtime capability evidence could not be persisted",
					data,
				)
			}
			if err := lease.MarkTerminal(ctx); err != nil {
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"perplexity_capabilities_terminal_state_failed", "internal",
					"Perplexity runtime capability terminal state could not be persisted",
					data,
				)
			}
			data.RuntimeState = runtime.State
			data.Capabilities = append([]ComposerCapability(nil), runtime.Capabilities...)
			data.CapturedAt = runtime.CapturedAt
			data.Message = runtime.Message
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationCapabilities,
				webagent.StateReady, webagent.StageObserveTerminal,
				"browser_observed_runtime", target, pending, nil, nil, data,
				[]string{
					"cdp workflow agent perplexity capabilities --json",
					"cdp workflow agent perplexity doctor --json",
				},
			)
		},
	)
}

func observeModelConfigResponse(
	ctx context.Context,
	client EventClient,
	session *cdp.PageSession,
	attempts int,
	wait time.Duration,
) ([]byte, bool, error) {
	records := map[string]*modelConfigResponseRecord{}
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := session.Reload(ctx, false); err != nil {
				return nil, false, err
			}
		}
		attemptCtx, cancel := context.WithTimeout(ctx, wait)
		for {
			event, err := client.ReadEvent(attemptCtx)
			if err != nil {
				cancel()
				if attemptCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
					if ctx.Err() != nil {
						return nil, false, ctx.Err()
					}
					break
				}
				return nil, false, err
			}
			if event.SessionID != session.SessionID {
				continue
			}
			switch event.Method {
			case "Network.responseReceived":
				var value struct {
					RequestID string `json:"requestId"`
					Response  struct {
						URL    string  `json:"url"`
						Status float64 `json:"status"`
					} `json:"response"`
				}
				if json.Unmarshal(event.Params, &value) != nil ||
					value.RequestID == "" ||
					!isModelConfigURL(value.Response.URL) {
					continue
				}
				records[value.RequestID] = &modelConfigResponseRecord{
					RequestID: value.RequestID,
					Status:    int(value.Response.Status),
				}
			case "Network.loadingFinished":
				var value struct {
					RequestID string `json:"requestId"`
				}
				if json.Unmarshal(event.Params, &value) != nil {
					continue
				}
				record := records[value.RequestID]
				if record == nil || record.Status != 200 {
					continue
				}
				body, bodyErr := readResponseBody(
					attemptCtx,
					client,
					session.SessionID,
					record.RequestID,
				)
				cancel()
				if bodyErr != nil {
					return nil, false, bodyErr
				}
				return body, true, nil
			case "Network.loadingFailed":
				var value struct {
					RequestID string `json:"requestId"`
				}
				if json.Unmarshal(event.Params, &value) == nil {
					delete(records, value.RequestID)
				}
			}
		}
	}
	return nil, false, nil
}

func readResponseBody(
	ctx context.Context,
	client cdp.CommandClient,
	sessionID string,
	requestID string,
) ([]byte, error) {
	var payload struct {
		Body          string `json:"body"`
		Base64Encoded bool   `json:"base64Encoded"`
	}
	if err := client.CallSession(
		ctx,
		sessionID,
		"Network.getResponseBody",
		map[string]any{"requestId": requestID},
		&payload,
	); err != nil {
		return nil, err
	}
	body := []byte(payload.Body)
	if payload.Base64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(payload.Body)
		if err != nil {
			return nil, fmt.Errorf("decode Perplexity model config response body")
		}
		body = decoded
	}
	if len(body) == 0 || len(body) > maxModelConfigResponseBytes {
		return nil, fmt.Errorf("Perplexity model config response is empty or exceeds its bound")
	}
	return body, nil
}

func isModelConfigURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	return err == nil &&
		parsed.Scheme == "https" &&
		parsed.Host == "www.perplexity.ai" &&
		(parsed.Path == ModelConfigPath ||
			parsed.Path == ModelConfigPath+"/v2") &&
		parsed.User == nil &&
		parsed.Fragment == ""
}

func parseRuntimeCapabilities(
	body []byte,
	capturedAt string,
) (RuntimeCapabilities, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return RuntimeCapabilities{}, err
	}
	runtime := RuntimeCapabilities{
		SchemaVersion: RuntimeCapabilitiesSchemaVersion,
		State:         "ready",
		CapturedAt:    capturedAt,
		Capabilities:  []ComposerCapability{},
		Source:        "headed-cdp-observed-model-config",
	}
	catalogs := map[string][]any{}
	for _, kind := range []string{"search", "computer"} {
		values, _ := payload[kind+"_config"].([]any)
		catalogs[kind] = values
	}
	if values, ok := payload["config"].([]any); ok {
		catalogs["search"] = values
	}
	for _, kind := range []string{"search", "computer"} {
		values := catalogs[kind]
		for _, value := range values {
			raw, _ := value.(map[string]any)
			label := strings.TrimSpace(stringValue(raw["label"]))
			if label == "" {
				continue
			}
			tier := strings.ToLower(strings.TrimSpace(stringValue(raw["subscription_tier"])))
			selected := boolValue(raw["is_default"])
			available := selected || tier == "" || tier == "all" || tier == "free"
			failureReason := ""
			if !available {
				failureReason = "entitlement_unverified:" + tier
			}
			runtime.Capabilities = append(runtime.Capabilities, ComposerCapability{
				ID:            slugCapability(label),
				Label:         label,
				Description:   strings.TrimSpace(stringValue(raw["description"])),
				Kind:          kind,
				Selected:      selected,
				Available:     available,
				FailureReason: failureReason,
				Metadata: map[string]any{
					"audience":          stringValue(raw["audience"]),
					"has_new_tag":       boolValue(raw["has_new_tag"]),
					"subscription_tier": tier,
				},
			})
		}
	}
	if err := runtime.Validate(); err != nil {
		return RuntimeCapabilities{}, err
	}
	return runtime, nil
}

func slugCapability(value string) string {
	return strings.Trim(capabilitySlugPattern.ReplaceAllString(strings.ToLower(value), "-"), "-")
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func boolValue(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func capabilityFailure(
	runID string,
	config CapabilityRefreshConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	code string,
	errClass string,
	message string,
	data CapabilityRefreshData,
) webagent.Result {
	return operationFailure(
		runID, config.BuildCommit, webagent.OperationCapabilities,
		stage, "browser_observed_runtime", target, cleanup,
		nil, nil, code, errClass, message, "", data,
		cleanupCommands(runID, cleanup),
	)
}
