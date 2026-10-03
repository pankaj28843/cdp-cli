package claude

import (
	"context"
	"fmt"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

// Controls records observed menu choices, not subscription entitlements.
type Controls struct {
	Selected   string   `json:"selected"`
	Models     []string `json:"models"`
	Efforts    []string `json:"efforts"`
	FileInputs int      `json:"file_inputs"`
	FileAccept []string `json:"file_accept"`
}

func discoverControls(ctx context.Context, session *cdp.PageSession) (*Controls, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var composer composerObservation
	result, err := authreadiness.WaitForHydration(ctx, session, authreadiness.MinimumAttempts, 30*time.Second, 100*time.Millisecond, func(ctx context.Context) (bool, error) {
		var err error
		composer, err = evaluateComposer(ctx, session)
		return composer.Ready, err
	})
	if err != nil || !result.Observed {
		return nil, fmt.Errorf("Claude controls were not ready")
	}
	original := composer.ModelLabel
	if err := setModelMenu(ctx, session, true, 100*time.Millisecond); err != nil {
		return nil, err
	}
	var controls Controls
	controls.Selected = original
	if err := evaluateInto(ctx, session, `(() => {
 const visible=e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return r.width>0&&r.height>0&&s.display!=='none'&&s.visibility!=='hidden'};
 const models=Array.from(document.querySelectorAll('[role="menuitemradio"]')).filter(visible).filter(e=>e.getAttribute('aria-disabled')!=='true').map(e=>(e.innerText||'').trim().split('\n')[0].trim()).filter(Boolean);
 const inputs=Array.from(document.querySelectorAll('input[type="file"]'));
 return {models,file_inputs:inputs.length,file_accept:inputs.map(e=>e.getAttribute('accept')||'')};
 })()`, &controls); err != nil {
		return nil, err
	}
	controls.Selected = original
	if len(controls.Models) == 0 {
		return nil, fmt.Errorf("Claude model rows were not observed")
	}
	if err := chooseSelectionRow(ctx, session, "menuitem", "Effort", 100*time.Millisecond); err != nil {
		return nil, err
	}
	var effort struct {
		Efforts []string `json:"efforts"`
	}
	if err := evaluateInto(ctx, session, `(() => {
 const visible=e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return r.width>0&&r.height>0&&s.display!=='none'&&s.visibility!=='hidden'};
 const menus=Array.from(document.querySelectorAll('[role="menu"]')).filter(visible);
 const menu=menus.at(-1);
 return {efforts:menu?Array.from(menu.querySelectorAll('[role="menuitemradio"]')).filter(visible).filter(e=>e.getAttribute('aria-disabled')!=='true').map(e=>(e.innerText||'').trim().split('\n')[0].trim()).filter(Boolean):[]};
 })()`, &effort); err != nil {
		return nil, err
	}
	if len(effort.Efforts) == 0 {
		return nil, fmt.Errorf("Claude effort rows were not observed")
	}
	controls.Efforts = effort.Efforts
	if err := setModelMenu(ctx, session, false, 100*time.Millisecond); err != nil {
		return nil, err
	}
	current, err := evaluateComposer(ctx, session)
	if err != nil || current.ModelLabel != original {
		return nil, fmt.Errorf("Claude selection changed during control discovery")
	}
	return &controls, nil
}
