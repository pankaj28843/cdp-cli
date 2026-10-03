package cli

import (
	"context"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/claude"
	"time"
)

type claudeCapabilitiesContract struct {
	webagent.Capabilities
	Runtime struct {
		State      string           `json:"state"`
		CapturedAt string           `json:"captured_at,omitempty"`
		Controls   *claude.Controls `json:"controls,omitempty"`
	} `json:"runtime"`
}

func (a *app) claudeCapabilitiesData(ctx context.Context, capabilities webagent.Capabilities) any {
	result := claudeCapabilitiesContract{Capabilities: capabilities}
	result.Runtime.State = "unavailable"
	state, err := a.stateStore()
	if err != nil {
		return result
	}
	store, err := claude.NewStore(state.Dir)
	if err != nil {
		return result
	}
	status := store.Status(ctx, time.Now(), claude.DefaultAuthTTL)
	result.Runtime.State = status.State
	template, err := store.Load(ctx)
	if err != nil {
		return result
	}
	result.Runtime.CapturedAt = template.CapturedAt
	result.Runtime.Controls = template.Controls
	if status.Ready && template.Controls == nil {
		result.Runtime.State = "not_observed"
	}
	return result
}
