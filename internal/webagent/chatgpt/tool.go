package chatgpt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const (
	ToolCreateImage      = "create-image"
	toolCreateImageLabel = "Create image"
)

// NormalizeTool accepts only tools whose full send-and-read lifecycle is
// proven by the headed ChatGPT surface. Runtime capability discovery may show
// more tools; that is evidence for documentation, not permission to guess a
// new automation contract.
func NormalizeTool(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.Join(strings.Fields(
		strings.NewReplacer("_", " ", "-", " ").Replace(normalized),
	), " ")
	if normalized == "" {
		return "", nil
	}
	if normalized == "create image" {
		return ToolCreateImage, nil
	}
	return "", fmt.Errorf(
		"unsupported ChatGPT tool %q; the verified direct-ask tool is create-image",
		value,
	)
}

func toolDisplayLabel(tool string) string {
	switch tool {
	case ToolCreateImage:
		return toolCreateImageLabel
	default:
		return ""
	}
}

type chatGPTToolSurface struct {
	MenuOpen bool     `json:"menu_open"`
	Options  []string `json:"options"`
	Selected string   `json:"selected"`
}

func observeChatGPTToolSurface(
	ctx context.Context,
	session *cdp.PageSession,
	surface *chatGPTToolSurface,
) error {
	return evaluateInto(ctx, session, `(() => {
  const normalize = value => String(value || '').replace(/\s+/g, ' ').trim();
  const visible = element => {
    if (!(element instanceof HTMLElement)) return false;
    const style = getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
  };
  const itemLabel = item => {
    const first = item.querySelector('span.max-w-full,span');
    return normalize(first && (first.innerText || first.textContent));
  };
  const items = Array.from(document.querySelectorAll(
    'div[tabindex="0"][data-fill]'
  )).filter(item => {
    const popover = item.closest('div[class*="popover"]');
    return visible(item) && Boolean(popover) && visible(popover);
  });
  const menu = items.length ? items[0].closest('div[class*="popover"]') : null;
  const options = Array.from(new Set(
    (menu ? items.filter(item => item.closest('div[class*="popover"]') === menu) : [])
      .map(itemLabel)
      .filter(Boolean)
  ));
  const editors = Array.from(document.querySelectorAll(
    '#prompt-textarea,[contenteditable="true"][role="textbox"]'
  )).filter(element => visible(element) && element.isContentEditable);
  const selected = Array.from(new Set(
    editors.flatMap(editor => Array.from(editor.querySelectorAll(
      '[data-inline-selection-pill][data-keyword]'
    )).filter(visible).map(pill => normalize(
      pill.getAttribute('data-keyword') || pill.innerText || pill.textContent
    )))
  )).filter(Boolean);
  return {
    menu_open: Boolean(menu),
    options,
    selected: selected.length === 1 ? selected[0] : ''
  };
})()`, surface)
}

func selectChatGPTTool(
	ctx context.Context,
	session *cdp.PageSession,
	tool string,
	timeout time.Duration,
	poll time.Duration,
) (string, error) {
	normalized, err := NormalizeTool(tool)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return "", nil
	}
	label := toolDisplayLabel(normalized)
	if label == "" {
		return "", fmt.Errorf("ChatGPT tool %q has no visible label", normalized)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	var surface chatGPTToolSurface
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = nil
		if err := observeChatGPTToolSurface(ctx, session, &surface); err != nil {
			lastErr = err
		} else if strings.EqualFold(surface.Selected, label) &&
			!surface.MenuOpen {
			return normalized, nil
		} else if surface.MenuOpen {
			found := false
			for _, option := range surface.Options {
				if strings.EqualFold(option, label) {
					found = true
					break
				}
			}
			if !found {
				return "", fmt.Errorf(
					"ChatGPT tool %q was not present in the visible plus menu",
					label,
				)
			}
			if err := activateSelectionControl(
				ctx, session, "tool", label,
			); err != nil {
				lastErr = err
			}
		} else if err := activateSelectionControl(
			ctx, session, "tool-menu", "",
		); err != nil {
			lastErr = err
		}
		if lastErr == nil {
			_, pollErr := pollUntil(
				ctx,
				minDuration(time.Until(deadline), 3*time.Second),
				poll,
				func() (bool, error) {
					if err := observeChatGPTToolSurface(ctx, session, &surface); err != nil {
						return false, err
					}
					return strings.EqualFold(surface.Selected, label) &&
						!surface.MenuOpen, nil
				},
			)
			if pollErr == nil {
				return normalized, nil
			}
			lastErr = pollErr
		}
		if !waitForObservation(ctx, poll, time.Until(deadline)) {
			break
		}
	}
	if lastErr == nil {
		lastErr = context.DeadlineExceeded
	}
	return "", fmt.Errorf(
		"ChatGPT tool %q was not selected on the exact composer: %w",
		label,
		lastErr,
	)
}

func inspectChatGPTToolOptions(
	ctx context.Context,
	session *cdp.PageSession,
	timeout time.Duration,
	poll time.Duration,
) ([]string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	var surface chatGPTToolSurface
	if err := observeChatGPTToolSurface(ctx, session, &surface); err != nil {
		return nil, err
	}
	if !surface.MenuOpen {
		if err := activateSelectionControl(ctx, session, "tool-menu", ""); err != nil {
			return nil, err
		}
	}
	if _, err := pollUntil(
		ctx,
		time.Until(deadline),
		poll,
		func() (bool, error) {
			if err := observeChatGPTToolSurface(ctx, session, &surface); err != nil {
				return false, err
			}
			return surface.MenuOpen && len(surface.Options) > 0, nil
		},
	); err != nil {
		return nil, fmt.Errorf("ChatGPT plus-menu tools were not observed: %w", err)
	}
	options := append([]string{}, surface.Options...)
	if err := activateSelectionControl(ctx, session, "tool-menu", ""); err != nil {
		return nil, fmt.Errorf("close ChatGPT plus menu: %w", err)
	}
	if _, err := pollUntil(
		ctx,
		time.Until(deadline),
		poll,
		func() (bool, error) {
			if err := observeChatGPTToolSurface(ctx, session, &surface); err != nil {
				return false, err
			}
			return !surface.MenuOpen, nil
		},
	); err != nil {
		return nil, fmt.Errorf("ChatGPT plus menu did not close: %w", err)
	}
	return options, nil
}
