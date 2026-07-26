package chatgpt

import (
	"context"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const CapabilityRefreshSchemaVersion = "chatgpt-capabilities-refresh/v1"

var (
	allowedProductModes = []string{"Chat", "Work"}
	allowedIntelligence = []string{
		"Instant", "Instant 5.5", "Medium", "High", "Extra High", "Pro", "GPT-5.6 Sol",
	}
	allowedTools = []string{
		"Add photos & files", "Create image", "Web search", "Deep research",
		"GitHub", "Visualize", "OpenAI Platform", "Atlassian Rovo",
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
			if _, err := pollUntil(
				ctx,
				config.Timeout,
				250*time.Millisecond,
				func() (bool, error) {
					var observed bool
					err := evaluateInto(
						ctx,
						session,
						`Boolean(document.querySelector('#prompt-textarea') || document.querySelector('[contenteditable="true"][role="textbox"]'))`,
						&observed,
					)
					return observed, err
				},
			); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageAttached, target, pending,
					"chatgpt_composer_not_observed", "auth",
					"Signed-in ChatGPT composer was not observed on the exact headed target",
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
			probe = sanitizeCapabilityProbe(probe)
			now := time.Now
			if config.Now != nil {
				now = config.Now
			}
			capturedAt := now().UTC().Format(time.RFC3339Nano)
			state := "unknown"
			message := "ChatGPT composer was observed, but paid Chat and Medium were not both proven."
			if probe.OK &&
				probe.ComposerObserved &&
				containsString(probe.ProductModes, "Chat") &&
				containsString(probe.IntelligenceOptions, "Medium") {
				state = "ready"
				message = "Paid Chat product and Medium intelligence were observed in the headed composer."
			}
			runtime := RuntimeCapabilities{
				SchemaVersion:        RuntimeCapabilitiesSchemaVersion,
				State:                state,
				CapturedAt:           capturedAt,
				ComposerObserved:     probe.ComposerObserved,
				ProductModes:         probe.ProductModes,
				SelectedProduct:      probe.SelectedProduct,
				IntelligenceOptions:  probe.IntelligenceOptions,
				SelectedIntelligence: probe.SelectedIntelligence,
				FileUploadObserved:   probe.FileUploadObserved,
				Tools:                probe.Tools,
				Source:               "headed-cdp-sanitized-composer-probe",
				Message:              message,
			}
			if err := config.Store.SaveRuntime(ctx, runtime); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return capabilityFailure(
					runID, config, webagent.StageObserveTerminal, target, pending,
					"chatgpt_capabilities_state_write_failed", "internal",
					"ChatGPT runtime capability evidence could not be persisted",
					data,
				)
			}
			if err := lease.MarkTerminal(ctx); err != nil {
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

func sanitizeCapabilityProbe(probe capabilityProbe) capabilityProbe {
	probe.ProductModes = orderedIntersection(probe.ProductModes, allowedProductModes)
	probe.IntelligenceOptions = orderedIntersection(
		probe.IntelligenceOptions,
		allowedIntelligence,
	)
	probe.Tools = orderedIntersection(probe.Tools, allowedTools)
	if !containsString(allowedProductModes, probe.SelectedProduct) {
		probe.SelectedProduct = ""
	}
	if !containsString(allowedIntelligence, probe.SelectedIntelligence) {
		probe.SelectedIntelligence = ""
	}
	return probe
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
  const escape = () => {
    const target = document.activeElement || document.body;
    if (target) {
      target.dispatchEvent(new KeyboardEvent('keydown', {
        key: 'Escape',
        code: 'Escape',
        bubbles: true,
        cancelable: true
      }));
    }
  };

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
    'Instant', 'Instant 5.5', 'Medium', 'High', 'Extra High', 'Pro', 'GPT-5.6 Sol'
  ];
  const pageButtons = Array.from(document.querySelectorAll('button,[role="button"]')).filter(visible);
  const intelligencePickers = pageButtons.filter((button) => {
    if (button.getAttribute('aria-haspopup') !== 'menu') return false;
    return intelligenceKnown.some((candidate) =>
      candidate.toLowerCase() === textOf(button).toLowerCase()
    );
  });
  const intelligencePicker = intelligencePickers.length === 1 ? intelligencePickers[0] : null;
  const selectedIntelligence = intelligencePicker ? textOf(intelligencePicker) : '';
  let intelligenceOptions = selectedIntelligence ? [selectedIntelligence] : [];
  if (intelligencePicker) {
    const wasExpanded = intelligencePicker.getAttribute('aria-expanded') === 'true';
    if (!wasExpanded) {
      intelligencePicker.click();
      await sleep(500);
    }
    const optionText = Array.from(document.querySelectorAll(
      '[role="menuitemradio"],[role="menuitem"],[role="option"]'
    )).filter(visible).map(textOf);
    intelligenceOptions = exactKnown(optionText.concat(intelligenceOptions), intelligenceKnown);
    if (!wasExpanded) {
      escape();
      await sleep(100);
    }
  }

  const toolsKnown = [
    'Add photos & files', 'Create image', 'Web search', 'Deep research',
    'GitHub', 'Visualize', 'OpenAI Platform', 'Atlassian Rovo'
  ];
  const plusCandidates = [
    document.querySelector('#composer-plus-btn'),
    document.querySelector('[data-testid="composer-plus-btn"]')
  ].filter((candidate) => visible(candidate));
  const plus = plusCandidates.length === 1 ? plusCandidates[0] : null;
  let tools = [];
  if (plus) {
    const wasExpanded = plus.getAttribute('aria-expanded') === 'true';
    if (!wasExpanded) {
      plus.click();
      await sleep(500);
    }
    const menuText = Array.from(document.querySelectorAll(
      '[role="menuitemradio"],[role="menuitem"],[role="option"]'
    )).filter(visible).map(textOf);
    tools = exactKnown(menuText, toolsKnown);
    if (!wasExpanded) {
      escape();
      await sleep(100);
    }
  }

  return {
    ok: intelligencePickers.length === 1,
    composer_observed: true,
    product_modes: productModes,
    selected_product: selectedProductButton ? textOf(selectedProductButton) : '',
    intelligence_options: intelligenceOptions,
    selected_intelligence: selectedIntelligence,
    file_upload_observed: Boolean(document.querySelector('input[type="file"]')) ||
      tools.some((tool) => tool === 'Add photos & files'),
    tools
  };
})()`
