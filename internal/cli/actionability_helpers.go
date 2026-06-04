package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type actionabilityResult struct {
	URL            string                        `json:"url"`
	Title          string                        `json:"title"`
	Selector       string                        `json:"selector"`
	Action         string                        `json:"action"`
	Trial          bool                          `json:"trial"`
	Force          bool                          `json:"force"`
	Count          int                           `json:"count"`
	Actionable     bool                          `json:"actionable"`
	RequiredChecks []string                      `json:"required_checks"`
	SkippedChecks  []string                      `json:"skipped_checks,omitempty"`
	Checks         map[string]actionabilityCheck `json:"checks"`
	Target         actionabilityTarget           `json:"target"`
	Rect           snapshotRect                  `json:"rect"`
	Point          actionabilityPoint            `json:"point"`
	Error          *evalError                    `json:"error,omitempty"`
}

type actionabilityCheck struct {
	Required bool   `json:"required"`
	Passed   bool   `json:"passed"`
	Skipped  bool   `json:"skipped,omitempty"`
	Message  string `json:"message,omitempty"`
}

type actionabilityTarget struct {
	Tag             string `json:"tag,omitempty"`
	ID              string `json:"id,omitempty"`
	Type            string `json:"type,omitempty"`
	Role            string `json:"role,omitempty"`
	Name            string `json:"name,omitempty"`
	Enabled         bool   `json:"enabled"`
	Disabled        bool   `json:"disabled"`
	Editable        bool   `json:"editable"`
	ReadOnly        bool   `json:"read_only"`
	SupportsEditing bool   `json:"supports_editing"`
	ContentEditable bool   `json:"content_editable"`
}

type actionabilityPoint struct {
	X             float64 `json:"x"`
	Y             float64 `json:"y"`
	HitTag        string  `json:"hit_tag,omitempty"`
	HitID         string  `json:"hit_id,omitempty"`
	HitRole       string  `json:"hit_role,omitempty"`
	TargetMatches bool    `json:"target_matches"`
}

func evaluateActionability(ctx context.Context, session *cdp.PageSession, selector, action string) (actionabilityResult, error) {
	var result actionabilityResult
	if err := evaluateJSONValue(ctx, session, actionabilityExpression(selector, action), "actionability", &result); err != nil {
		return actionabilityResult{}, err
	}
	result.Action = action
	return result, nil
}

func prepareActionability(result *actionabilityResult, action string, trial, force bool) {
	result.Action = action
	result.Trial = trial
	result.Force = force
	if force {
		applyForceActionabilitySkips(result, action)
		return
	}
	result.Actionable = actionabilityRequiredChecksPass(*result)
}

func applyForceActionabilitySkips(result *actionabilityResult, action string) {
	if result.Checks == nil {
		result.Actionable = false
		return
	}
	skipSet := map[string]bool{}
	for _, name := range actionabilityForceSkippedChecks(action) {
		skipSet[name] = true
	}
	if len(skipSet) == 0 {
		result.Actionable = actionabilityRequiredChecksPass(*result)
		return
	}

	required := result.RequiredChecks[:0]
	result.SkippedChecks = result.SkippedChecks[:0]
	for _, name := range result.RequiredChecks {
		if !skipSet[name] {
			required = append(required, name)
			continue
		}
		check, ok := result.Checks[name]
		if !ok {
			continue
		}
		check.Required = false
		check.Skipped = true
		check.Message = forceSkippedMessage(check.Message)
		result.Checks[name] = check
		result.SkippedChecks = append(result.SkippedChecks, name)
	}
	result.RequiredChecks = required
	result.Actionable = actionabilityRequiredChecksPass(*result)
}

func actionabilityForceSkippedChecks(action string) []string {
	switch action {
	case "click":
		return []string{"receives_events"}
	case "fill":
		return []string{"visible"}
	default:
		return nil
	}
}

func forceSkippedMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "skipped by --force"
	}
	return message + "; skipped by --force"
}

func actionabilityRequiredChecksPass(result actionabilityResult) bool {
	for _, name := range result.RequiredChecks {
		check, ok := result.Checks[name]
		if !ok || !check.Passed {
			return false
		}
	}
	return len(result.RequiredChecks) > 0
}

func actionabilityFailureMessage(action, selector string, result actionabilityResult) string {
	failed := failedActionabilityChecks(result)
	if len(failed) == 0 {
		return fmt.Sprintf("%s actionability failed for %q", action, selector)
	}
	return fmt.Sprintf("%s actionability failed for %q: %s", action, selector, strings.Join(failed, ", "))
}

func failedActionabilityChecks(result actionabilityResult) []string {
	failed := make([]string, 0, len(result.RequiredChecks))
	for _, name := range result.RequiredChecks {
		check, ok := result.Checks[name]
		if !ok || !check.Passed {
			failed = append(failed, name)
		}
	}
	return failed
}

func actionabilityRemediations(action, query, selector string, opts locatorActionOptions) []string {
	commands := []string{locatorActionFindCommand(query, opts), "cdp dom query " + shellQuote(selector) + " --json"}
	if action == "fill" {
		commands = append(commands, "cdp assert editable "+shellQuote(selector)+" --json")
	} else {
		commands = append(commands, "cdp assert visible "+shellQuote(selector)+" --json", "cdp assert enabled "+shellQuote(selector)+" --json")
	}
	return commands
}

func actionabilityExpression(selector, action string) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_actionability__";
  const selector = %s;
  const action = %s;
  const nativeDisabledTags = new Set(["button", "select", "input", "textarea", "option", "optgroup"]);
  const nativeEditableTags = new Set(["input", "textarea", "select"]);
  const ariaReadonlyRoles = new Set(["checkbox", "combobox", "grid", "gridcell", "listbox", "radiogroup", "searchbox", "slider", "spinbutton", "switch", "textbox", "treegrid"]);
  const norm = (value) => String(value || "").replace(/\s+/g, " ").trim();
  const roleOf = (el) => {
    const explicit = norm(el.getAttribute("role")).split(" ")[0];
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    const type = String(el.getAttribute("type") || "").toLowerCase();
    if (tag === "button") return "button";
    if (tag === "a" && el.hasAttribute("href")) return "link";
    if (/^h[1-6]$/.test(tag)) return "heading";
    if (tag === "textarea") return "textbox";
    if (tag === "select") return el.multiple ? "listbox" : "combobox";
    if (tag === "input") {
      if (["button", "submit", "reset"].includes(type)) return "button";
      if (type === "checkbox") return "checkbox";
      if (type === "radio") return "radio";
      if (type === "range") return "slider";
      if (type === "search") return "searchbox";
      return "textbox";
    }
    return "";
  };
  const nameOf = (el) => norm(el.getAttribute("aria-label") || el.getAttribute("alt") || el.getAttribute("title") || el.getAttribute("placeholder") || el.getAttribute("value") || el.innerText || el.textContent || "");
  const disabledInfo = (el) => {
    const tag = el.tagName.toLowerCase();
    const nativeDisableable = nativeDisabledTags.has(tag);
    const nativeDisabled = nativeDisableable && el.hasAttribute("disabled");
    const fieldsetDisabled = nativeDisableable && Boolean(el.closest("fieldset[disabled]"));
    let ariaDisabled = false;
    for (let node = el; node && node.nodeType === Node.ELEMENT_NODE; node = node.parentElement) {
      if (String(node.getAttribute("aria-disabled") || "").toLowerCase() === "true") {
        ariaDisabled = true;
        break;
      }
    }
    return nativeDisabled || fieldsetDisabled || ariaDisabled;
  };
  const readOnlyInfo = (el, role) => {
    const tag = el.tagName.toLowerCase();
    const nativeEditable = nativeEditableTags.has(tag);
    const contentEditable = Boolean(el.isContentEditable);
    const supportsAriaReadonly = ariaReadonlyRoles.has(role);
    const supportsEditing = nativeEditable || contentEditable || supportsAriaReadonly;
    const nativeReadOnly = nativeEditable && el.hasAttribute("readonly");
    const ariaReadOnly = supportsAriaReadonly && String(el.getAttribute("aria-readonly") || "").toLowerCase() === "true";
    return { readOnly: nativeReadOnly || ariaReadOnly, supportsEditing, contentEditable };
  };
  const rectSnapshot = (el) => {
    const rect = el.getBoundingClientRect();
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  };
  const sameRect = (a, b) => Math.abs(a.x - b.x) < 0.5 && Math.abs(a.y - b.y) < 0.5 && Math.abs(a.width - b.width) < 0.5 && Math.abs(a.height - b.height) < 0.5;
  const check = (required, passed, message) => {
    const out = { required: Boolean(required), passed: Boolean(passed) };
    if (!required) out.skipped = true;
    if (message) out.message = message;
    return out;
  };
  const emptyChecks = () => {
    const requiredChecks = action === "fill" ? ["attached", "visible", "enabled", "editable"] : ["attached", "visible", "stable", "receives_events", "enabled"];
    const checks = {
      attached: check(true, false, "selector matched no elements"),
      visible: check(requiredChecks.includes("visible"), false, "no element to inspect"),
      stable: check(requiredChecks.includes("stable"), false, "no element to inspect"),
      receives_events: check(requiredChecks.includes("receives_events"), false, "no element to inspect"),
      enabled: check(requiredChecks.includes("enabled"), false, "no element to inspect"),
      editable: check(requiredChecks.includes("editable"), false, "no element to inspect"),
      in_viewport: check(false, false, "no element to inspect")
    };
    return { requiredChecks, checks };
  };
  let elements;
  try {
    elements = Array.from(document.querySelectorAll(selector));
  } catch (error) {
    const empty = emptyChecks();
    return { url: location.href, title: document.title, selector, action, trial: false, count: 0, actionable: false, required_checks: empty.requiredChecks, checks: empty.checks, target: {}, rect: { x: 0, y: 0, width: 0, height: 0 }, point: { x: 0, y: 0, target_matches: false }, error: { name: error.name, message: error.message }, marker };
  }
  if (elements.length === 0) {
    const empty = emptyChecks();
    return { url: location.href, title: document.title, selector, action, trial: false, count: 0, actionable: false, required_checks: empty.requiredChecks, checks: empty.checks, target: {}, rect: { x: 0, y: 0, width: 0, height: 0 }, point: { x: 0, y: 0, target_matches: false }, marker };
  }
  const el = elements[0];
  const role = roleOf(el);
  const disabled = disabledInfo(el);
  const readonly = readOnlyInfo(el, role);
  const first = rectSnapshot(el);
  return new Promise((resolve) => {
    requestAnimationFrame(() => {
      const mid = rectSnapshot(el);
      requestAnimationFrame(() => {
        const liveRect = el.getBoundingClientRect();
        const rect = { x: liveRect.x, y: liveRect.y, width: liveRect.width, height: liveRect.height };
        const style = getComputedStyle(el);
        const hidden = Boolean(el.hidden || el.closest("[hidden]") || style.display === "none" || style.visibility === "hidden");
        const visible = !hidden && rect.width > 0 && rect.height > 0;
        const stable = sameRect(first, mid) && sameRect(mid, rect);
        const inViewport = rect.width > 0 && rect.height > 0 && liveRect.bottom >= 0 && liveRect.right >= 0 && liveRect.top <= window.innerHeight && liveRect.left <= window.innerWidth;
        const x = rect.x + rect.width / 2;
        const y = rect.y + rect.height / 2;
        const hit = inViewport ? document.elementFromPoint(Math.min(Math.max(x, 0), Math.max(window.innerWidth - 1, 0)), Math.min(Math.max(y, 0), Math.max(window.innerHeight - 1, 0))) : null;
        const targetMatches = Boolean(hit && (hit === el || el.contains(hit)));
        const editable = readonly.supportsEditing && !disabled && !readonly.readOnly;
        const requiredChecks = action === "fill" ? ["attached", "visible", "enabled", "editable"] : ["attached", "visible", "stable", "receives_events", "enabled"];
        const checks = {
          attached: check(true, elements.length > 0, ""),
          visible: check(requiredChecks.includes("visible"), visible, visible ? "" : "element has empty box or hidden/display-none/visibility-hidden state"),
          stable: check(requiredChecks.includes("stable"), stable, stable ? "" : "bounding box changed across animation frames"),
          receives_events: check(requiredChecks.includes("receives_events"), targetMatches, targetMatches ? "" : "center point is not the hit target"),
          enabled: check(requiredChecks.includes("enabled"), !disabled, disabled ? "element is disabled" : ""),
          editable: check(requiredChecks.includes("editable"), editable, editable ? "" : "element is disabled, read-only, or does not support editing"),
          in_viewport: check(false, inViewport, inViewport ? "" : "element center is outside the viewport")
        };
        const actionable = requiredChecks.every((name) => checks[name] && checks[name].passed);
        resolve({
          url: location.href,
          title: document.title,
          selector,
          action,
          trial: false,
          count: elements.length,
          actionable,
          required_checks: requiredChecks,
          checks,
          target: {
            tag: el.tagName.toLowerCase(),
            id: el.id || "",
            type: el.getAttribute("type") || "",
            role,
            name: nameOf(el).slice(0, 240),
            enabled: !disabled,
            disabled,
            editable,
            read_only: readonly.readOnly,
            supports_editing: readonly.supportsEditing,
            content_editable: readonly.contentEditable
          },
          rect,
          point: {
            x,
            y,
            hit_tag: hit ? hit.tagName.toLowerCase() : "",
            hit_id: hit ? hit.id || "" : "",
            hit_role: hit ? roleOf(hit) : "",
            target_matches: targetMatches
          },
          marker
        });
      });
    });
  });
})()`, jsStringLiteral(selector), jsStringLiteral(action))
}
