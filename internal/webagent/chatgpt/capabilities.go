package chatgpt

import (
	"context"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const CapabilityRefreshSchemaVersion = "chatgpt-capabilities-refresh/v1"

var (
	allowedProductModes = []string{"Chat", "Work"}
	allowedIntelligence = []string{
		"Instant", "Instant 5.5", "Medium", "High", "Extra High", "Pro",
	}
	allowedTools = []string{
		"Add photos & files", "Create image", "Web search", "Deep research",
		"GitHub", "OpenAI Platform", "Visualize", "Gmail",
	}
)

type CapabilityRefreshConfig struct {
	BrowserConfig
	Store   *Store
	Timeout time.Duration
	Now     func() time.Time
}

type CapabilityRefreshData struct {
	SchemaVersion        string   `json:"schema_version"`
	RuntimeState         string   `json:"runtime_state"`
	StatePath            string   `json:"state_path"`
	ComposerObserved     bool     `json:"composer_observed"`
	ProductModes         []string `json:"product_modes"`
	SelectedProduct      string   `json:"selected_product,omitempty"`
	IntelligenceOptions  []string `json:"intelligence_options"`
	SelectedIntelligence string   `json:"selected_intelligence,omitempty"`
	ModelOptions         []string `json:"model_options"`
	SelectedModel        string   `json:"selected_model,omitempty"`
	ModelOptionsObserved bool     `json:"model_options_observed"`
	FileUploadObserved   bool     `json:"file_upload_observed"`
	Tools                []string `json:"tools"`
	CapturedAt           string   `json:"captured_at,omitempty"`
	Message              string   `json:"message,omitempty"`
}

type capabilityProbe struct {
	OK                   bool     `json:"ok"`
	ComposerObserved     bool     `json:"composer_observed"`
	ProductModes         []string `json:"product_modes"`
	SelectedProduct      string   `json:"selected_product"`
	IntelligenceOptions  []string `json:"intelligence_options"`
	SelectedIntelligence string   `json:"selected_intelligence"`
	ModelOptions         []string `json:"model_options"`
	SelectedModel        string   `json:"selected_model"`
	ModelOptionsObserved bool     `json:"model_options_observed"`
	FileUploadObserved   bool     `json:"file_upload_observed"`
	Tools                []string `json:"tools"`
}

func RefreshCapabilities(
	ctx context.Context,
	config CapabilityRefreshConfig,
) webagent.Result {
	runID := webagent.NewRunID()
	data := CapabilityRefreshData{
		SchemaVersion:       CapabilityRefreshSchemaVersion,
		RuntimeState:        "blocked",
		StatePath:           RelativeCapabilitiesPath,
		ProductModes:        []string{},
		IntelligenceOptions: []string{},
		ModelOptions:        []string{},
		Tools:               []string{},
	}
	if config.Store == nil {
		return capabilityFailure(
			runID, config, webagent.StagePlanned, nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"chatgpt_state_unavailable", "internal",
			"ChatGPT owner-only runtime capability state is unavailable", data,
		)
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
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
					"chatgpt_capabilities_prepare_failed", "connection",
					"ChatGPT runtime observation could not prepare the exact headed target",
					data,
				)
			}
			readiness, err := authreadiness.WaitForEvidence(
				ctx,
				session,
				authreadiness.MinimumAttempts,
				config.Timeout,
				250*time.Millisecond,
				func(observationCtx context.Context) (bool, error) {
					var observed bool
					err := evaluateInto(
						observationCtx,
						session,
						`Boolean(document.querySelector('#prompt-textarea') || document.querySelector('[contenteditable="true"][role="textbox"]'))`,
						&observed,
					)
					return observed, err
				},
			)
			if err != nil || readiness.ObservationFailed() {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageAttached, target, pending,
					"chatgpt_capabilities_observation_failed", "connection",
					"ChatGPT composer observation could not complete its bounded load, reload, hard-reload, and final-grace sequence",
					data,
				)
			}
			if !readiness.Observed {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageAttached, target, pending,
					"chatgpt_composer_not_observed", "auth",
					"ChatGPT composer was not observed after bounded load, reload, hard reload, and final grace; the browser session may still be active",
					data,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return capabilityFailure(
					runID, config, webagent.StageAttached, target, pending,
					"chatgpt_capabilities_prepare_state_failed", "internal",
					"ChatGPT runtime observation preparation could not be persisted",
					data,
				)
			}
			selection, selectionErr := inspectChatGPTSelectionOptions(
				ctx,
				session,
				config.Timeout,
				250*time.Millisecond,
			)
			if selectionErr != nil {
				data.Message = selectionErr.Error()
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"chatgpt_capability_options_not_ready", "provider",
					"ChatGPT thinking and model options did not become observable after exact-target raw input",
					data,
				)
			}
			var probe capabilityProbe
			if err := evaluateInto(ctx, session, capabilityProbeExpression, &probe); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StagePrepared, target, pending,
					"chatgpt_capabilities_probe_failed", "connection",
					"ChatGPT composer capability probe failed on the exact headed target",
					data,
				)
			}
			probe.IntelligenceOptions = optionLabels(
				logicalThinkingOptions(selection.ThinkingOptions),
				false,
			)
			probe.SelectedIntelligence = selection.SelectedThinking
			probe.ModelOptions = optionLabels(
				selection.ModelOptions,
				true,
			)
			probe.SelectedModel = selection.SelectedModel
			if toolOptions, toolErr := inspectChatGPTToolOptions(
				ctx,
				session,
				config.Timeout,
				250*time.Millisecond,
			); toolErr == nil {
				probe.Tools = toolOptions
			}
			probe = sanitizeCapabilityProbe(probe)
			probe.ModelOptionsObserved =
				len(probe.ModelOptions) > 0 &&
					strings.TrimSpace(probe.SelectedModel) != ""
			now := time.Now
			if config.Now != nil {
				now = config.Now
			}
			capturedAt := now().UTC().Format(time.RFC3339Nano)
			state, message := capabilityStateAndMessage(probe)
			runtime := RuntimeCapabilities{
				SchemaVersion:        RuntimeCapabilitiesSchemaVersion,
				State:                state,
				CapturedAt:           capturedAt,
				ComposerObserved:     probe.ComposerObserved,
				ProductModes:         probe.ProductModes,
				SelectedProduct:      probe.SelectedProduct,
				IntelligenceOptions:  probe.IntelligenceOptions,
				SelectedIntelligence: probe.SelectedIntelligence,
				ModelOptions:         probe.ModelOptions,
				SelectedModel:        probe.SelectedModel,
				ModelOptionsObserved: probe.ModelOptionsObserved,
				FileUploadObserved:   probe.FileUploadObserved,
				Tools:                probe.Tools,
				Source:               "headed-cdp-sanitized-composer-probe",
				Message:              message,
			}
			persistenceContext, cancelPersistence := webagent.DurablePersistenceContext(ctx)
			defer cancelPersistence()
			if err := config.Store.SaveRuntime(persistenceContext, runtime); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"chatgpt_capabilities_state_write_failed", "internal",
					"ChatGPT runtime capability evidence could not be persisted",
					data,
				)
			}
			if err := lease.MarkTerminal(persistenceContext); err != nil {
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"chatgpt_capabilities_terminal_state_failed", "internal",
					"ChatGPT runtime capability terminal state could not be persisted",
					data,
				)
			}
			data.RuntimeState = runtime.State
			data.ComposerObserved = runtime.ComposerObserved
			data.ProductModes = append([]string{}, runtime.ProductModes...)
			data.SelectedProduct = runtime.SelectedProduct
			data.IntelligenceOptions = append([]string{}, runtime.IntelligenceOptions...)
			data.SelectedIntelligence = runtime.SelectedIntelligence
			data.ModelOptions = append([]string{}, runtime.ModelOptions...)
			data.SelectedModel = runtime.SelectedModel
			data.ModelOptionsObserved = runtime.ModelOptionsObserved
			data.FileUploadObserved = runtime.FileUploadObserved
			data.Tools = append([]string{}, runtime.Tools...)
			data.CapturedAt = runtime.CapturedAt
			data.Message = runtime.Message
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationCapabilities,
				webagent.StageObserveTerminal, "browser_observed_runtime",
				target, pending, data,
				[]string{
					"cdp workflow agent chatgpt capabilities --json",
					"cdp workflow agent chatgpt doctor --json",
				},
			)
		},
	)
}

func capabilityStateAndMessage(probe capabilityProbe) (string, string) {
	state := "unknown"
	message := "ChatGPT composer was observed, but its Chat and thinking controls were not both proven."
	if !probe.OK ||
		!probe.ComposerObserved ||
		!containsString(probe.ProductModes, "Chat") ||
		len(probe.IntelligenceOptions) == 0 ||
		strings.TrimSpace(probe.SelectedIntelligence) == "" ||
		!containsString(
			probe.IntelligenceOptions,
			probe.SelectedIntelligence,
		) {
		return state, message
	}
	state = "ready"
	message = "Chat product and logically ordered thinking modes were observed in the headed composer; the model catalog was not observed."
	if probe.ModelOptionsObserved {
		message = "Chat product, logically ordered thinking modes, and visible model options were observed in the headed composer."
	}
	if len(probe.Tools) > 0 {
		message += " The visible plus-menu tools were also sampled; tool visibility is not direct-ask support."
	}
	return state, message
}

func sanitizeCapabilityProbe(probe capabilityProbe) capabilityProbe {
	probe.ProductModes = orderedIntersection(probe.ProductModes, allowedProductModes)
	probe.IntelligenceOptions = sanitizeDynamicLabels(
		probe.IntelligenceOptions,
		16,
	)
	probe.ModelOptions = sanitizeDynamicLabels(probe.ModelOptions, 32)
	probe.Tools = orderedIntersection(probe.Tools, allowedTools)
	if !containsString(allowedProductModes, probe.SelectedProduct) {
		probe.SelectedProduct = ""
	}
	if !containsString(probe.IntelligenceOptions, probe.SelectedIntelligence) {
		probe.SelectedIntelligence = ""
	}
	if !containsString(probe.ModelOptions, probe.SelectedModel) {
		probe.SelectedModel = ""
	}
	return probe
}

func sanitizeDynamicLabels(values []string, maximum int) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || containsString(out, value) ||
			validatePublicLabel(value) != nil {
			continue
		}
		out = append(out, value)
		if len(out) >= maximum {
			break
		}
	}
	return out
}

func orderedIntersection(values, allowed []string) []string {
	out := make([]string, 0, len(allowed))
	for _, candidate := range allowed {
		if containsString(values, candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), candidate) {
			return true
		}
	}
	return false
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
		code, errClass, message, data,
		cleanupCommands(runID, cleanup),
	)
}

const capabilityProbeExpression = `(async () => {
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const normalize = (value) => String(value || '').replace(/\s+/g, ' ').trim();
  const visible = (element) => {
    if (!element || !(element instanceof Element)) return false;
    const rect = element.getBoundingClientRect();
    const style = window.getComputedStyle(element);
    return rect.width > 0 && rect.height > 0 &&
      style.visibility !== 'hidden' && style.display !== 'none';
  };
  const textOf = (element) => normalize(
    element && (
      element.innerText ||
      element.textContent ||
      element.getAttribute('aria-label') ||
      element.getAttribute('title') ||
      ''
    )
  );
  const exactKnown = (values, known) => known.filter((candidate) =>
    values.some((value) => value.toLowerCase() === candidate.toLowerCase())
  );
  const prompt = document.querySelector('#prompt-textarea') ||
    document.querySelector('[contenteditable="true"][role="textbox"]');
  const form = prompt && prompt.closest('form');
  if (!prompt || !form) {
    return {
      ok: false,
      composer_observed: false,
      product_modes: [],
      selected_product: '',
      intelligence_options: [],
      selected_intelligence: '',
      model_options: [],
      selected_model: '',
      file_upload_observed: false,
      tools: []
    };
  }

  const productKnown = ['Chat', 'Work'];
  const radios = Array.from(document.querySelectorAll('button[role="radio"]')).filter(visible);
  const productModes = exactKnown(radios.map(textOf), productKnown);
  const selectedProductButton = radios.find((button) =>
    button.getAttribute('aria-checked') === 'true' &&
    productKnown.some((candidate) => candidate.toLowerCase() === textOf(button).toLowerCase())
  );

  const intelligenceKnown = [
    'Instant', 'Instant 5.5', 'Medium', 'High', 'Extra High', 'Pro'
  ];
  const pageButtons = Array.from(document.querySelectorAll('button,[role="button"]')).filter(visible);
  const intelligencePickers = pageButtons.filter((button) => {
    if (button.getAttribute('aria-haspopup') !== 'menu') return false;
    return button.classList.contains('__composer-pill') ||
      intelligenceKnown.some((candidate) =>
        candidate.toLowerCase() === textOf(button).toLowerCase()
      );
  });
  const intelligencePicker = intelligencePickers.length === 1 ? intelligencePickers[0] : null;
  const selectedIntelligence = intelligencePicker ? textOf(intelligencePicker) : '';
  const intelligenceOptions = selectedIntelligence ? [selectedIntelligence] : [];
  const modelOptions = [];
  const selectedModel = '';

  const toolsKnown = [
    'Add photos & files', 'Create image', 'Web search', 'Deep research',
    'GitHub', 'OpenAI Platform', 'Visualize', 'Gmail'
  ];
  const menuText = Array.from(document.querySelectorAll(
      '[role="menuitemradio"],[role="menuitem"],[role="option"]'
  )).filter(visible).map(textOf);
  const tools = exactKnown(menuText, toolsKnown);

  return {
    ok: intelligencePickers.length === 1,
    composer_observed: true,
    product_modes: productModes,
    selected_product: selectedProductButton ? textOf(selectedProductButton) : '',
    intelligence_options: intelligenceOptions,
    selected_intelligence: selectedIntelligence,
    model_options: modelOptions,
    selected_model: selectedModel,
    file_upload_observed: Boolean(document.querySelector('input[type="file"]')) ||
      tools.some((tool) => tool === 'Add photos & files'),
    tools
  };
})()`
