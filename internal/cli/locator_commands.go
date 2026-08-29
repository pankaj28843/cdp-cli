package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type locatorFindResult struct {
	URL           string         `json:"url"`
	Title         string         `json:"title"`
	By            string         `json:"by"`
	Query         string         `json:"query"`
	Role          string         `json:"role,omitempty"`
	Exact         bool           `json:"exact"`
	IncludeHidden bool           `json:"include_hidden"`
	TestIDAttr    string         `json:"test_id_attr,omitempty"`
	Count         int            `json:"count"`
	Returned      int            `json:"returned"`
	Strict        bool           `json:"strict"`
	Matches       []locatorMatch `json:"matches"`
	Error         *evalError     `json:"error,omitempty"`
}

type locatorMatch struct {
	Index                 int          `json:"index"`
	SelectorHint          string       `json:"selector_hint"`
	SelectorAmbiguous     bool         `json:"selector_ambiguous"`
	ResolvedNodeSelector  string       `json:"-"`
	ResolvedNodeID        string       `json:"-"`
	ResolvedBackendNodeID int          `json:"-"`
	Tag                   string       `json:"tag"`
	Type                  string       `json:"type,omitempty"`
	Role                  string       `json:"role,omitempty"`
	Name                  string       `json:"name,omitempty"`
	Text                  string       `json:"text,omitempty"`
	Placeholder           string       `json:"placeholder,omitempty"`
	Title                 string       `json:"title,omitempty"`
	Alt                   string       `json:"alt,omitempty"`
	TestID                string       `json:"test_id,omitempty"`
	Visible               bool         `json:"visible"`
	Disabled              bool         `json:"disabled,omitempty"`
	ReadOnly              bool         `json:"read_only,omitempty"`
	ContentEditable       bool         `json:"content_editable,omitempty"`
	Rect                  snapshotRect `json:"rect"`
}

func (match *locatorMatch) UnmarshalJSON(data []byte) error {
	type publicLocatorMatch locatorMatch
	decoded := struct {
		*publicLocatorMatch
		ResolvedNodeSelector string `json:"resolved_node_selector"`
		ResolvedNodeID       string `json:"resolved_node_id"`
	}{publicLocatorMatch: (*publicLocatorMatch)(match)}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	match.ResolvedNodeSelector = decoded.ResolvedNodeSelector
	match.ResolvedNodeID = decoded.ResolvedNodeID
	return nil
}

func (a *app) newLocatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "locator",
		Short: "Find elements by user-facing locators",
	}
	cmd.AddCommand(a.newLocatorFindCommand())
	return cmd
}

func (a *app) newLocatorFindCommand() *cobra.Command {
	var targetID, urlContains, titleContains string
	var targetIndex int
	var by, role, testIDAttr string
	var exact, includeHidden bool
	var limit int
	cmd := &cobra.Command{
		Use:   "find <query>",
		Short: "Find elements by role, text, label, placeholder, alt, title, test id, or CSS",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePageTargetIndexSelector(cmd, targetID, urlContains, titleContains, targetIndex); err != nil {
				return err
			}
			by = normalizeLocatorStrategy(by)
			if err := validateLocatorFindOptions(by, role, testIDAttr, limit); err != nil {
				return err
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			session, target, err := a.attachPageSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			var result locatorFindResult
			if err := evaluateJSONValue(ctx, session, locatorFindExpression(by, args[0], role, exact, includeHidden, testIDAttr, limit), "locator find", &result); err != nil {
				return err
			}
			if result.Error != nil {
				return commandError("invalid_locator", "usage", fmt.Sprintf("locator %s %q: %s", by, args[0], result.Error.Message), ExitUsage, []string{"cdp locator find Search --by label --json", "cdp locator find Submit --by role --role button --json"})
			}
			next := locatorNextCommands(result)
			report := map[string]any{
				"ok":            true,
				"target":        pageRow(target),
				"locator":       result,
				"matches":       result.Matches,
				"next_commands": next,
			}
			if targetIndex > 0 {
				report["target_index"] = targetIndex
			}
			return a.render(ctx, fmt.Sprintf("locator-find\t%s\t%d matches", by, result.Count), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based page target index")
	cmd.Flags().StringVar(&by, "by", "text", "locator strategy: role, text, label, placeholder, alt, title, test-id, or css")
	cmd.Flags().StringVar(&role, "role", "", "ARIA role to match when --by role is used")
	cmd.Flags().StringVar(&testIDAttr, "test-id-attr", "data-testid", "attribute name for --by test-id")
	cmd.Flags().BoolVar(&exact, "exact", false, "require exact normalized text/name/attribute match")
	cmd.Flags().BoolVar(&includeHidden, "include-hidden", false, "include hidden matches")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum matches to return")
	return cmd
}

func normalizeLocatorStrategy(by string) string {
	switch strings.ToLower(strings.TrimSpace(by)) {
	case "", "text":
		return "text"
	case "role":
		return "role"
	case "label":
		return "label"
	case "placeholder":
		return "placeholder"
	case "alt", "alt-text", "alt_text":
		return "alt"
	case "title":
		return "title"
	case "test-id", "testid", "test_id":
		return "test-id"
	case "css", "selector":
		return "css"
	default:
		return strings.ToLower(strings.TrimSpace(by))
	}
}

func validateLocatorFindOptions(by, role, testIDAttr string, limit int) error {
	switch by {
	case "role", "text", "label", "placeholder", "alt", "title", "test-id", "css":
	default:
		return commandError("usage", "usage", "--by must be role, text, label, placeholder, alt, title, test-id, or css", ExitUsage, []string{"cdp locator find Search --by label --json"})
	}
	if by == "role" && strings.TrimSpace(role) == "" {
		return commandError("usage", "usage", "--role is required with --by role", ExitUsage, []string{"cdp locator find Submit --by role --role button --json"})
	}
	if limit <= 0 {
		return commandError("usage", "usage", "--limit must be positive", ExitUsage, []string{"cdp locator find Search --limit 20 --json"})
	}
	if !validLocatorAttributeName(testIDAttr) {
		return commandError("usage", "usage", "--test-id-attr must be a simple attribute name", ExitUsage, []string{"cdp locator find cdp_demo --by test-id --test-id-attr data-testid --json"})
	}
	return nil
}

func validLocatorAttributeName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == ':' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func locatorNextCommands(result locatorFindResult) []string {
	if result.Count != 1 || len(result.Matches) != 1 {
		return []string{"cdp locator find <query> --by " + result.By + " --json"}
	}
	match := result.Matches[0]
	if strings.TrimSpace(match.SelectorHint) == "" || match.SelectorAmbiguous {
		options := locatorActionOptions{
			By:            result.By,
			Role:          result.Role,
			TestIDAttr:    result.TestIDAttr,
			Exact:         result.Exact,
			IncludeHidden: result.IncludeHidden,
		}
		commands := []string{semanticLocatorActionCommand("click", result.Query, options)}
		if locatorMatchIsEditable(match) {
			commands = append([]string{semanticLocatorActionCommand("fill", result.Query, options)}, commands...)
		}
		return commands
	}
	selector := shellQuote(match.SelectorHint)
	commands := []string{"cdp click " + selector + " --json", "cdp snapshot --selector " + selector + " --json"}
	if locatorMatchIsEditable(match) {
		commands = append([]string{"cdp fill " + selector + " <value> --json"}, commands...)
	}
	return commands
}

func semanticLocatorActionCommand(action, query string, opts locatorActionOptions) string {
	command := "cdp " + action + " " + shellQuote(query) + " --by " + opts.By
	if action == "fill" {
		command = "cdp fill " + shellQuote(query) + " <value> --by " + opts.By
	}
	if opts.By == "role" {
		command += " --role " + shellQuote(opts.Role)
	}
	if opts.Exact {
		command += " --exact"
	}
	if opts.IncludeHidden {
		command += " --include-hidden"
	}
	if opts.By == "test-id" && opts.TestIDAttr != "" && opts.TestIDAttr != "data-testid" {
		command += " --test-id-attr " + shellQuote(opts.TestIDAttr)
	}
	return command + " --json"
}

func locatorMatchIsEditable(match locatorMatch) bool {
	if match.ContentEditable {
		return true
	}
	switch match.Tag {
	case "input", "textarea", "select":
		return true
	default:
		return false
	}
}

func locatorFindExpression(by, query, role string, exact, includeHidden bool, testIDAttr string, limit int) string {
	return fmt.Sprintf(`(() => {
  const marker = "__cdp_cli_locator_find__";
  const by = %s;
  const query = %s;
  const roleQuery = %s;
  const exact = %t;
  const includeHidden = %t;
  const testIDAttr = %s || "data-testid";
  const limit = %d;
  const norm = (s) => String(s || "").replace(/\s+/g, " ").trim();
  const lower = (s) => norm(s).toLowerCase();
  const queryLower = lower(query);
  const cssEscape = (value) => {
    if (globalThis.CSS && typeof CSS.escape === "function") return CSS.escape(String(value));
    return String(value).replace(/[^a-zA-Z0-9_-]/g, (ch) => "\\" + ch);
  };
  const attrSelector = (tag, attr, value) => tag + "[" + attr + "=" + JSON.stringify(value) + "]";
  const nativeDisabledTags = new Set(["button", "select", "input", "textarea", "option", "optgroup"]);
  const visibleInfo = (el) => {
    const style = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    const hidden = el.hidden || el.closest("[hidden]") !== null || style.display === "none" || style.visibility === "hidden" || Number(style.opacity || "1") === 0 || el.closest('[aria-hidden="true"]') !== null || el.getAttribute("aria-hidden") === "true";
    return { visible: !hidden && rect.width > 0 && rect.height > 0, rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } };
  };
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
  const ariaReadonlyRoles = new Set(["checkbox", "combobox", "grid", "gridcell", "listbox", "radiogroup", "searchbox", "slider", "spinbutton", "switch", "textbox", "treegrid"]);
  const readOnlyInfo = (el, role) => {
    const tag = el.tagName.toLowerCase();
    const nativeEditable = ["input", "textarea", "select"].includes(tag);
    const nativeReadOnly = nativeEditable && el.hasAttribute("readonly");
    const supportsAriaReadonly = ariaReadonlyRoles.has(role);
    const ariaReadOnly = supportsAriaReadonly && String(el.getAttribute("aria-readonly") || "").toLowerCase() === "true";
    return nativeReadOnly || ariaReadOnly;
  };
  const ownText = (el) => norm(Array.from(el.childNodes || []).filter((node) => node.nodeType === Node.TEXT_NODE).map((node) => node.textContent || "").join(" "));
  const textOf = (el) => norm(ownText(el) || el.innerText || el.textContent || "");
  %s
  const implicitRole = (el) => {
    const explicit = norm(el.getAttribute("role")).split(" ")[0];
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    const type = String(el.getAttribute("type") || "").toLowerCase();
    if (tag === "button") return "button";
    if (tag === "a" && el.hasAttribute("href")) return "link";
    if (/^h[1-6]$/.test(tag)) return "heading";
    if (tag === "textarea") return "textbox";
    if (tag === "select") return el.multiple ? "listbox" : "combobox";
    if (tag === "img") return "img";
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
  const selectorHint = (el) => {
    const tag = el.tagName.toLowerCase();
    if (el.id) return tag + "#" + cssEscape(el.id);
    for (const attr of [testIDAttr, "data-testid", "data-test-id", "data-test", "name", "aria-label", "placeholder", "title", "alt", "role"]) {
      const value = el.getAttribute(attr);
      if (value) return attrSelector(tag, attr, value);
    }
    const parts = [];
    let node = el;
    while (node && node.nodeType === Node.ELEMENT_NODE && node !== document.documentElement) {
      const nodeTag = node.tagName.toLowerCase();
      if (node.id) {
        parts.unshift(nodeTag + "#" + cssEscape(node.id));
        break;
      }
      const parent = node.parentElement;
      if (!parent) {
        parts.unshift(nodeTag);
        break;
      }
      const siblings = Array.from(parent.children).filter((child) => child.tagName === node.tagName);
      const index = siblings.indexOf(node) + 1;
      parts.unshift(siblings.length > 1 ? nodeTag + ":nth-of-type(" + index + ")" : nodeTag);
      const candidate = parts.join(" > ");
      if (document.querySelectorAll(candidate).length === 1) return candidate;
      node = parent;
    }
    return parts.join(" > ") || tag;
  };
  const resolvedNodeSelector = (el) => {
    const parts = [];
    let node = el;
    while (node && node.nodeType === Node.ELEMENT_NODE) {
      const tag = node.tagName.toLowerCase();
      const parent = node.parentElement;
      if (!parent) {
        parts.unshift(tag);
      } else {
        const siblings = Array.from(parent.children).filter((child) => child.tagName === node.tagName);
        const index = siblings.indexOf(node) + 1;
        parts.unshift(siblings.length > 1 ? tag + ":nth-of-type(" + index + ")" : tag);
      }
      const candidate = parts.join(" > ");
      if (document.querySelectorAll(candidate).length === 1) return candidate;
      node = parent;
    }
    return parts.join(" > ");
  };
  const matchesText = (value) => exact ? lower(value) === queryLower : lower(value).includes(queryLower);
  const all = () => Array.from(document.querySelectorAll("body *"));
  const controls = () => Array.from(document.querySelectorAll("input, textarea, select, [contenteditable=''], [contenteditable='true']"));
  let nodes = [];
  try {
    if (by === "css") {
      nodes = Array.from(document.querySelectorAll(query));
    } else if (by === "role") {
      nodes = all().filter((el) => implicitRole(el) === roleQuery && matchesText(accessibleName(el)));
    } else if (by === "label") {
      nodes = controls().filter((el) => matchesText(labelText(el) || accessibleName(el)));
    } else if (by === "placeholder") {
      nodes = Array.from(document.querySelectorAll("[placeholder]")).filter((el) => matchesText(el.getAttribute("placeholder")));
    } else if (by === "alt") {
      nodes = Array.from(document.querySelectorAll("[alt]")).filter((el) => matchesText(el.getAttribute("alt")));
    } else if (by === "title") {
      nodes = Array.from(document.querySelectorAll("[title]")).filter((el) => matchesText(el.getAttribute("title")));
    } else if (by === "test-id") {
      nodes = Array.from(document.querySelectorAll("[" + testIDAttr + "], [data-testid], [data-test-id], [data-test]")).filter((el) => matchesText(el.getAttribute(testIDAttr) || el.getAttribute("data-testid") || el.getAttribute("data-test-id") || el.getAttribute("data-test")));
    } else {
      nodes = all().filter((el) => matchesText(textOf(el)));
    }
  } catch (error) {
    return { url: location.href, title: document.title, by, query, role: roleQuery, exact, include_hidden: includeHidden, test_id_attr: testIDAttr, count: 0, returned: 0, strict: false, matches: [], error: { name: error.name, message: error.message }, marker };
  }
  const visibleFiltered = includeHidden ? nodes : nodes.filter((el) => visibleInfo(el).visible);
  const matches = visibleFiltered.slice(0, limit).map((el, index) => {
    const tag = el.tagName.toLowerCase();
    const visibility = visibleInfo(el);
    const role = implicitRole(el);
    const hint = selectorHint(el);
    return {
      index,
      selector_hint: hint,
      selector_ambiguous: document.querySelectorAll(hint).length !== 1,
      resolved_node_selector: resolvedNodeSelector(el),
      resolved_node_id: el.id || "",
      tag,
      type: el.getAttribute("type") || "",
      role,
      name: accessibleName(el),
      text: textOf(el).slice(0, 240),
      placeholder: el.getAttribute("placeholder") || "",
      title: el.getAttribute("title") || "",
      alt: el.getAttribute("alt") || "",
      test_id: el.getAttribute(testIDAttr) || el.getAttribute("data-testid") || el.getAttribute("data-test-id") || el.getAttribute("data-test") || "",
      visible: visibility.visible,
      disabled: disabledInfo(el),
      read_only: readOnlyInfo(el, role),
      content_editable: Boolean(el.isContentEditable),
      rect: visibility.rect
    };
  });
  return { url: location.href, title: document.title, by, query, role: roleQuery, exact, include_hidden: includeHidden, test_id_attr: testIDAttr, count: visibleFiltered.length, returned: matches.length, strict: visibleFiltered.length === 1, matches, marker };
})()`, jsStringLiteral(by), jsStringLiteral(query), jsStringLiteral(role), exact, includeHidden, jsStringLiteral(testIDAttr), limit, accessibleNameHelpersJS())
}

func accessibleNameHelpersJS() string {
	return `const labelledBy = (el) => norm(norm(el.getAttribute("aria-labelledby")).split(" ").filter(Boolean).map((id) => {
    const node = document.getElementById(id);
    return node ? node.innerText || node.textContent || "" : "";
  }).join(" "));
  const labelText = (el) => norm(el.labels && el.labels.length ? Array.from(el.labels).map((label) => label.innerText || label.textContent || "").join(" ") : "");
  const accessibleName = (el) => norm(el.getAttribute("aria-label") || labelledBy(el) || el.getAttribute("alt") || el.getAttribute("title") || labelText(el) || el.getAttribute("placeholder") || el.getAttribute("value") || el.innerText || el.textContent || "");`
}
