package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type selectionControl struct {
	Count   int     `json:"count"`
	Ready   bool    `json:"ready"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Checked bool    `json:"checked"`
	Open    bool    `json:"open"`
}

// selectRequestedControls uses the current menu's radio rows. Upgrade entries
// are never model candidates. No prompt mutation or Send occurs here.
func selectRequestedControls(ctx context.Context, session *cdp.PageSession, model, effort string, timeout, poll time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	model, effort = strings.TrimSpace(model), strings.TrimSpace(effort)
	if model != "" {
		if err := setModelMenu(ctx, session, true, poll); err != nil {
			return err
		}
		if err := chooseSelectionRow(ctx, session, "menuitemradio", model, poll); err != nil {
			return err
		}
	}
	if effort != "" {
		if err := setModelMenu(ctx, session, true, poll); err != nil {
			return err
		}
		if err := chooseSelectionRow(ctx, session, "menuitem", "Effort", poll); err != nil {
			return err
		}
		if err := chooseSelectionRow(ctx, session, "menuitemradio", effort, poll); err != nil {
			return err
		}
	}
	if err := setModelMenu(ctx, session, false, poll); err != nil {
		return err
	}
	for {
		current, err := evaluateComposer(ctx, session)
		if err == nil && current.Ready && requestedSelectionMatches(current.ModelLabel, model, effort) {
			return nil
		}
		if err := waitSelectionPoll(ctx, poll); err != nil {
			return fmt.Errorf("requested Claude selection readback: %w", err)
		}
	}
}

func requestedSelectionMatches(label, model, effort string) bool {
	label, model, effort = strings.ToLower(strings.TrimSpace(label)), strings.ToLower(strings.TrimSpace(model)), strings.ToLower(strings.TrimSpace(effort))
	return label != "" && (model == "" || label == model || strings.HasPrefix(label, model+" ")) &&
		(effort == "" || strings.HasSuffix(label, " "+effort))
}

func setModelMenu(ctx context.Context, session *cdp.PageSession, open bool, poll time.Duration) error {
	const expression = `(() => {
 const claudeSelectionPicker = true;
 const visible = e => {const r=e.getBoundingClientRect(),s=getComputedStyle(e);return r.width>0&&r.height>0&&s.display!=='none'&&s.visibility!=='hidden'&&Number(s.opacity||'1')!==0};
 const matches = Array.from(document.querySelectorAll('button[data-testid="model-selector-dropdown"]')).filter(visible);
 const e=matches.length===1?matches[0]:null, r=e?.getBoundingClientRect();
 const x=r?r.left+r.width/2:0,y=r?r.top+r.height/2:0,top=e?document.elementFromPoint(x,y):null;
 return {count:matches.length,x,y,open:e?.getAttribute('aria-expanded')==='true',
 ready:Boolean(e&&top&&(top===e||e.contains(top))&&!e.disabled&&e.getAttribute('aria-disabled')!=='true')};
 })()`
	control, err := waitSelectionControl(ctx, session, expression, poll)
	if err != nil || control.Open == open {
		return err
	}
	if err := clickSelectionControl(ctx, session, control); err != nil {
		return err
	}
	for {
		control, err = waitSelectionControl(ctx, session, expression, poll)
		if err != nil {
			return err
		}
		if control.Open == open {
			return nil
		}
		if err := waitSelectionPoll(ctx, poll); err != nil {
			return err
		}
	}
}

func chooseSelectionRow(ctx context.Context, session *cdp.PageSession, role, title string, poll time.Duration) error {
	parameters, _ := json.Marshal(map[string]string{"role": role, "title": title})
	expression := `(() => {
 const claudeSelectionRow = ` + string(parameters) + `;
 const visible = e => {const r=e.getBoundingClientRect(),s=getComputedStyle(e);return r.width>0&&r.height>0&&s.display!=='none'&&s.visibility!=='hidden'&&Number(s.opacity||'1')!==0};
 const matches=Array.from(document.querySelectorAll('[role="'+claudeSelectionRow.role+'"]')).filter(visible).filter(e=>
 (e.innerText||e.textContent||'').trim().split('\n')[0].trim().toLowerCase()===claudeSelectionRow.title.toLowerCase());
 const e=matches.length===1?matches[0]:null,r=e?.getBoundingClientRect();
 const x=r?r.left+r.width/2:0,y=r?r.top+r.height/2:0,top=e?document.elementFromPoint(x,y):null;
 return {count:matches.length,x,y,checked:e?.getAttribute('aria-checked')==='true',
 ready:Boolean(e&&top&&(top===e||e.contains(top))&&!e.hasAttribute('disabled')&&e.getAttribute('aria-disabled')!=='true')};
 })()`
	control, err := waitSelectionControl(ctx, session, expression, poll)
	if err != nil || control.Checked {
		return err
	}
	return clickSelectionControl(ctx, session, control)
}

func waitSelectionControl(ctx context.Context, session *cdp.PageSession, expression string, poll time.Duration) (selectionControl, error) {
	for {
		var control selectionControl
		err := evaluateInto(ctx, session, expression, &control)
		if err == nil {
			if control.Count > 1 {
				return control, fmt.Errorf("Claude selection control is ambiguous")
			}
			if control.Count == 1 && control.Ready {
				return control, nil
			}
		}
		if err := waitSelectionPoll(ctx, poll); err != nil {
			return control, err
		}
	}
}

func clickSelectionControl(ctx context.Context, session *cdp.PageSession, control selectionControl) error {
	outcome, err := browserflow.ClickPoint(ctx, session, control.X, control.Y)
	if err != nil {
		return err
	}
	if outcome.Dispatch != browserflow.DispatchPerformed {
		return fmt.Errorf("Claude selection click was not confirmed")
	}
	return nil
}

func waitSelectionPoll(ctx context.Context, poll time.Duration) error {
	timer := time.NewTimer(poll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
