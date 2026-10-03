package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

// prepareMode runs only before prompt mutation, under the ask's owned input lease.
func prepareMode(ctx context.Context, session *cdp.PageSession, mode string, timeout, interval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var picker capabilityObservation
	if err := observeCapabilities(ctx, session, &picker); err != nil {
		return fmt.Errorf("observe mode picker: %w", err)
	}
	if picker.PickerCount != 1 || !picker.PickerReady {
		return fmt.Errorf("mode picker is not uniquely actionable")
	}
	if strings.EqualFold(strings.TrimSpace(picker.CurrentMode), mode) {
		return nil
	}
	if !picker.MenuOpen {
		outcome, err := browserflow.ClickPoint(ctx, session, picker.X, picker.Y)
		if err != nil {
			return fmt.Errorf("open mode picker: %w", err)
		}
		if outcome.Dispatch != browserflow.DispatchPerformed {
			return fmt.Errorf("mode picker click was not confirmed")
		}
	}
	encoded, _ := json.Marshal(mode)
	var option struct {
		MatchCount int     `json:"match_count"`
		Ready      bool    `json:"ready"`
		X          float64 `json:"x"`
		Y          float64 `json:"y"`
	}
	_, err := pollUntil(ctx, timeout, interval, func() (bool, error) {
		err := evaluateInto(ctx, session, `(() => {
   const geminiModeSelection = `+string(encoded)+`;
   const visible = e => {
    const r = e.getBoundingClientRect(), s = getComputedStyle(e);
    return r.width > 0 && r.height > 0 && s.display !== 'none' &&
      s.visibility !== 'hidden' && Number(s.opacity || '1') !== 0;
   };
   const matches = Array.from(document.querySelectorAll('[role=option],[role=menuitem]'))
    .filter(visible).filter(e => {
     const title = (e.innerText || e.textContent || '').trim().split('\n')[0]
      .replace(/^\d+(?:\.\d+)*\s+/, '').trim();
     return title.toLowerCase() === geminiModeSelection.toLowerCase();
    });
   const e = matches.length === 1 ? matches[0] : null;
   const r = e?.getBoundingClientRect();
   const x = r ? r.left + r.width / 2 : 0, y = r ? r.top + r.height / 2 : 0;
   const top = e ? document.elementFromPoint(x,y) : null;
   return {
    match_count: matches.length, x, y,
    ready: Boolean(e && top && (top === e || e.contains(top)) &&
     !e.hasAttribute('disabled') && e.getAttribute('aria-disabled') !== 'true')
   };
  })()`, &option)
		if err != nil {
			return false, err
		}
		if option.MatchCount > 1 {
			return false, fmt.Errorf("requested mode is ambiguous")
		}
		return option.MatchCount == 1 && option.Ready, nil
	})
	if err != nil {
		return fmt.Errorf("observe requested mode option: %w", err)
	}
	outcome, err := browserflow.ClickPoint(ctx, session, option.X, option.Y)
	if err != nil {
		return fmt.Errorf("select requested mode: %w", err)
	}
	if outcome.Dispatch != browserflow.DispatchPerformed {
		return fmt.Errorf("mode selection click was not confirmed")
	}
	_, err = pollUntil(ctx, timeout, interval, func() (bool, error) {
		if err := observeCapabilities(ctx, session, &picker); err != nil {
			return false, err
		}
		return picker.PickerCount == 1 && picker.PickerReady &&
			strings.EqualFold(strings.TrimSpace(picker.CurrentMode), mode), nil
	})
	if err != nil {
		return fmt.Errorf("verify selected mode: %w", err)
	}
	return nil
}
