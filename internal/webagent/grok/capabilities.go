package grok

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	CapabilityRefreshSchemaVersion = "grok-capabilities-refresh/v1"
	modesPath                      = "/rest/modes"
	maxModesResponseBytes          = 4 << 20
)

type CapabilityRefreshConfig struct {
	BrowserConfig
	Store               *Store
	ObservationTimeout  time.Duration
	ObservationAttempts int
	Now                 func() time.Time
}

type CapabilityRefreshData struct {
	SchemaVersion string `json:"schema_version"`
	RuntimeState  string `json:"runtime_state"`
	StatePath     string `json:"state_path"`
	DefaultModeID string `json:"default_mode_id,omitempty"`
	ModeCount     int    `json:"mode_count"`
	Available     int    `json:"available_mode_count"`
	CapturedAt    string `json:"captured_at,omitempty"`
}

type modesResponseRecord struct {
	RequestID string
	URL       string
	Status    int
	Finished  bool
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
	}
	if config.Store == nil {
		return capabilityFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"grok_state_unavailable", "internal",
			"Grok owner-only runtime capability state is unavailable", data,
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
					"grok_capabilities_prepare_failed", "connection",
					"Grok runtime observation could not prepare the exact headed target",
					data,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return capabilityFailure(
					runID, config, webagent.StageAttached, target, pending,
					"grok_capabilities_prepare_state_failed", "internal",
					"Grok runtime observation preparation could not be persisted",
					data,
				)
			}
			payload, found, err := observeModesResponse(
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
					"grok_capabilities_observation_failed", "connection",
					"Grok runtime modes response observation failed on the exact headed target",
					data,
				)
			}
			if !found {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"grok_modes_response_not_observed", "capability",
					"Grok runtime modes response was not observed",
					data,
				)
			}
			now := time.Now
			if config.Now != nil {
				now = config.Now
			}
			runtime, err := parseRuntimeCapabilities(
				payload,
				now().UTC().Format(time.RFC3339Nano),
			)
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"grok_modes_response_invalid", "capability",
					"Grok runtime modes response did not contain one available default mode",
					data,
				)
			}
			if err := config.Store.SaveRuntime(ctx, runtime); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"grok_capabilities_state_write_failed", "internal",
					"Grok runtime capability evidence could not be persisted",
					data,
				)
			}
			if err := lease.MarkTerminal(ctx); err != nil {
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"grok_capabilities_terminal_state_failed", "internal",
					"Grok runtime capability terminal state could not be persisted",
					data,
				)
			}
			data.RuntimeState = "ready"
			data.DefaultModeID = runtime.DefaultModeID
			data.ModeCount = len(runtime.Modes)
			for _, mode := range runtime.Modes {
				if mode.Available {
					data.Available++
				}
			}
			data.CapturedAt = runtime.CapturedAt
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationCapabilities,
				webagent.StateReady, webagent.StageObserveTerminal,
				"browser_observed_runtime", target, pending, nil, nil, data,
				[]string{
					"cdp workflow agent grok capabilities --json",
					"cdp workflow agent grok doctor --json",
				},
			)
		},
	)
}

func observeModesResponse(
	ctx context.Context,
	client EventClient,
	session *cdp.PageSession,
	attempts int,
	wait time.Duration,
) ([]byte, bool, error) {
	records := map[string]*modesResponseRecord{}
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
					!isModesURL(value.Response.URL) {
					continue
				}
				records[value.RequestID] = &modesResponseRecord{
					RequestID: value.RequestID,
					URL:       value.Response.URL,
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
				record.Finished = true
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
	var body []byte
	if payload.Base64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(payload.Body)
		if err != nil {
			return nil, fmt.Errorf("decode Grok modes response body")
		}
		body = decoded
	} else {
		body = []byte(payload.Body)
	}
	if len(body) == 0 || len(body) > maxModesResponseBytes {
		return nil, fmt.Errorf("Grok modes response body is empty or exceeds its bound")
	}
	return body, nil
}

func isModesURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	return err == nil &&
		parsed.Scheme == "https" &&
		parsed.Host == "grok.com" &&
		parsed.Path == modesPath &&
		parsed.User == nil &&
		parsed.Fragment == ""
}

func parseRuntimeCapabilities(
	body []byte,
	capturedAt string,
) (RuntimeCapabilities, error) {
	var payload struct {
		DefaultModeID string `json:"defaultModeId"`
		Modes         []struct {
			ID           string `json:"id"`
			Title        string `json:"title"`
			Description  string `json:"description"`
			Availability struct {
				Available       json.RawMessage `json:"available"`
				RequiresUpgrade *struct {
					MinimumSubscriptionTier string `json:"minimumSubscriptionTier"`
				} `json:"requiresUpgrade"`
			} `json:"availability"`
			Tags []string `json:"tags"`
		} `json:"modes"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return RuntimeCapabilities{}, err
	}
	defaultModeID := strings.TrimSpace(payload.DefaultModeID)
	runtime := RuntimeCapabilities{
		SchemaVersion: RuntimeCapabilitiesSchemaVersion,
		CapturedAt:    capturedAt,
		DefaultModeID: defaultModeID,
		Modes:         make([]Mode, 0, len(payload.Modes)),
		Source:        "headed-cdp-observed-modes-response",
	}
	for _, raw := range payload.Modes {
		id := strings.TrimSpace(raw.ID)
		title := strings.TrimSpace(raw.Title)
		if id == "" || title == "" {
			continue
		}
		var availableObject map[string]any
		available := len(raw.Availability.Available) > 0 &&
			string(raw.Availability.Available) != "null" &&
			json.Unmarshal(raw.Availability.Available, &availableObject) == nil &&
			availableObject != nil
		failureReason := ""
		if !available {
			tier := "unknown"
			if raw.Availability.RequiresUpgrade != nil &&
				strings.TrimSpace(raw.Availability.RequiresUpgrade.MinimumSubscriptionTier) != "" {
				tier = strings.TrimSpace(
					raw.Availability.RequiresUpgrade.MinimumSubscriptionTier,
				)
			}
			failureReason = "requires_upgrade:" + tier
		}
		tags := make([]string, 0, len(raw.Tags))
		for _, tag := range raw.Tags {
			if tag = strings.TrimSpace(tag); tag != "" && len(tag) <= 256 {
				tags = append(tags, tag)
			}
		}
		runtime.Modes = append(runtime.Modes, Mode{
			ID:            id,
			Title:         title,
			Description:   strings.TrimSpace(raw.Description),
			Available:     available,
			Selected:      id == defaultModeID,
			FailureReason: failureReason,
			Tags:          tags,
		})
	}
	if err := runtime.Validate(); err != nil {
		return RuntimeCapabilities{}, err
	}
	return runtime, nil
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
