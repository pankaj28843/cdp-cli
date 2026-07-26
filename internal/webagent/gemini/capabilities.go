package gemini

import (
	"context"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const CapabilityRefreshSchemaVersion = "gemini-capabilities-refresh/v1"

type CapabilityRefreshConfig struct {
	BrowserConfig
	Store        *Store
	Timeout      time.Duration
	PollInterval time.Duration
	Now          func() time.Time
}

type CapabilityRefreshData struct {
	SchemaVersion           string   `json:"schema_version"`
	RuntimeState            string   `json:"runtime_state"`
	StatePath               string   `json:"state_path"`
	CurrentMode             string   `json:"current_mode,omitempty"`
	ModeOptions             []string `json:"mode_options"`
	FileUploadControl       string   `json:"file_upload_control,omitempty"`
	FileUploadAction        string   `json:"file_upload_action,omitempty"`
	DeepResearchSelected    bool     `json:"deep_research_selected"`
	ExplicitModeSelection   string   `json:"explicit_mode_selection,omitempty"`
	PickerAttempts          int      `json:"picker_attempts"`
	MenuObservationAttempts int      `json:"menu_observation_attempts"`
	PickerClickDispatch     string   `json:"picker_click_dispatch,omitempty"`
	CapturedAt              string   `json:"captured_at,omitempty"`
}

type capabilityObservation struct {
	PickerCount               int      `json:"picker_count"`
	PickerName                string   `json:"picker_name"`
	CurrentMode               string   `json:"current_mode"`
	MenuOpen                  bool     `json:"menu_open"`
	ModeOptions               []string `json:"mode_options"`
	FileUploadControlObserved bool     `json:"file_upload_control_observed"`
	DeepResearchSelected      bool     `json:"deep_research_selected"`
	PickerReady               bool     `json:"picker_ready"`
	X                         float64  `json:"x"`
	Y                         float64  `json:"y"`
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
		ModeOptions:   []string{},
	}
	if config.Store == nil {
		return operationFailure(
			runID, config.BuildCommit, webagent.OperationCapabilities,
			webagent.StagePlanned, "headed_rendered_controls",
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil,
			"gemini_state_unavailable", "internal",
			"Gemini owner-only runtime capability state is unavailable", "",
			data, []string{"cdp doctor --json"},
		)
	}
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationCapabilities,
		"about:blank",
		"headed_rendered_controls",
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
					"gemini_capabilities_prepare_failed", "connection",
					"Gemini runtime controls could not be prepared on the exact headed target",
					data,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return capabilityFailure(
					runID, config, webagent.StageAttached, target, pending,
					"gemini_capabilities_prepare_state_failed", "internal",
					"Gemini capability preparation could not be persisted",
					data,
				)
			}
			var observation capabilityObservation
			attempts, err := pollUntil(
				ctx,
				config.Timeout,
				config.PollInterval,
				func() (bool, error) {
					if err := observeCapabilities(ctx, session, &observation); err != nil {
						return false, err
					}
					return observation.PickerCount == 1 &&
						observation.PickerReady &&
						strings.TrimSpace(observation.CurrentMode) != "", nil
				},
			)
			data.PickerAttempts = attempts
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"gemini_capability_controls_not_ready", "provider",
					"Gemini mode picker did not become uniquely observable",
					data,
				)
			}
			if !observation.MenuOpen || len(observation.ModeOptions) == 0 {
				outcome, clickErr := browserflow.ClickPoint(
					ctx,
					session,
					observation.X,
					observation.Y,
				)
				data.PickerClickDispatch = string(outcome.Dispatch)
				if clickErr != nil || outcome.Dispatch != browserflow.DispatchPerformed {
					_ = lease.MarkIncomplete(context.Background())
					return capabilityFailure(
						runID, config, webagent.StagePrepared, target, pending,
						"gemini_mode_picker_open_failed", "provider",
						"Gemini mode picker menu could not be opened once on the exact target",
						data,
					)
				}
				menuAttempts, menuErr := pollUntil(
					ctx,
					config.Timeout,
					config.PollInterval,
					func() (bool, error) {
						if err := observeCapabilities(ctx, session, &observation); err != nil {
							return false, err
						}
						return observation.MenuOpen && len(observation.ModeOptions) > 0, nil
					},
				)
				data.MenuObservationAttempts = menuAttempts
				if menuErr != nil {
					_ = lease.MarkIncomplete(context.Background())
					return capabilityFailure(
						runID, config, webagent.StagePrepared, target, pending,
						"gemini_mode_options_not_ready", "provider",
						"Gemini mode options did not become observable after the one picker action",
						data,
					)
				}
			}
			options := uniqueNonEmpty(observation.ModeOptions)
			if len(options) == 0 {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"gemini_mode_options_empty", "capability",
					"Gemini runtime mode options were empty",
					data,
				)
			}
			now := time.Now
			if config.Now != nil {
				now = config.Now
			}
			capturedAt := now().UTC().Format(time.RFC3339Nano)
			upload := "not_observed"
			if observation.FileUploadControlObserved {
				upload = "observed"
			}
			runtime := RuntimeCapabilities{
				SchemaVersion:         RuntimeCapabilitiesSchemaVersion,
				CapturedAt:            capturedAt,
				CurrentMode:           strings.TrimSpace(observation.CurrentMode),
				ModeOptions:           options,
				FileUploadControl:     upload,
				FileUploadAction:      "unsupported",
				DeepResearchSelected:  observation.DeepResearchSelected,
				ExplicitModeSelection: "request_shape_unobserved",
				Source:                "headed-cdp-rendered-controls",
			}
			if err := config.Store.SaveRuntime(ctx, runtime); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"gemini_capabilities_state_write_failed", "internal",
					"Gemini runtime capability evidence could not be persisted",
					data,
				)
			}
			if err := lease.MarkTerminal(ctx); err != nil {
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"gemini_capabilities_terminal_state_failed", "internal",
					"Gemini runtime capability terminal state could not be persisted",
					data,
				)
			}
			data.RuntimeState = "ready"
			data.CurrentMode = runtime.CurrentMode
			data.ModeOptions = runtime.ModeOptions
			data.FileUploadControl = runtime.FileUploadControl
			data.FileUploadAction = runtime.FileUploadAction
			data.DeepResearchSelected = runtime.DeepResearchSelected
			data.ExplicitModeSelection = runtime.ExplicitModeSelection
			data.CapturedAt = capturedAt
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationCapabilities,
				webagent.StateReady, webagent.StageObserveTerminal,
				"headed_rendered_controls", target, pending, nil, nil, data,
				[]string{
					"cdp workflow agent gemini capabilities --json",
					"cdp workflow agent gemini doctor --json",
				},
			)
		},
	)
}

func observeCapabilities(
	ctx context.Context,
	session *cdp.PageSession,
	observation *capabilityObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const buttons = Array.from(document.querySelectorAll('button')).filter(visible);
	  const pickers = buttons.filter(button =>
	    (button.getAttribute('aria-label') || '').startsWith('Open mode picker, currently ')
	  );
	  const picker = pickers.length === 1 ? pickers[0] : null;
	  const pickerName = picker?.getAttribute('aria-label') || '';
	  const rect = picker?.getBoundingClientRect();
	  const x = rect ? rect.left + rect.width / 2 : 0;
	  const y = rect ? rect.top + rect.height / 2 : 0;
	  const top = rect ? document.elementFromPoint(x, y) : null;
	  const pickerReady = Boolean(
	    picker && top && (top === picker || picker.contains(top)) &&
	    !picker.hasAttribute('disabled') && picker.getAttribute('aria-disabled') !== 'true'
	  );
	  const options = Array.from(
	    document.querySelectorAll('[role=option],[role=menuitem]')
	  ).filter(visible).map(element =>
	    (element.innerText || element.textContent || '').trim()
	  ).filter(Boolean);
	  return {
	    picker_count: pickers.length,
	    picker_name: pickerName,
	    current_mode: pickerName.split('currently ')[1] || '',
	    menu_open: options.length > 0,
	    mode_options: [...new Set(options)],
	    file_upload_control_observed: buttons.some(button =>
	      (button.getAttribute('aria-label') || '') === 'Upload & tools'
	    ),
	    deep_research_selected: buttons.some(button =>
	      /Deselect Deep research/i.test(
	        button.innerText || button.getAttribute('aria-label') || ''
	      )
	    ),
	    picker_ready: pickerReady,
	    x,
	    y
	  };
	})()`, observation)
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
		stage, "headed_rendered_controls", target, cleanup,
		nil, nil, code, errClass, message, "", data,
		cleanupCommands(runID, cleanup),
	)
}

func uniqueNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
