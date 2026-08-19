package transcriptionapi

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type authRefreshTestProvider struct {
	id    ProviderID
	calls atomic.Int32
	err   error
}

func (p *authRefreshTestProvider) ID() ProviderID { return p.id }

func (p *authRefreshTestProvider) Capabilities(context.Context) ProviderCapability {
	return ProviderCapability{Provider: p.id, Models: []string{DefaultModel}, File: true, Ready: true}
}

func (p *authRefreshTestProvider) Transcribe(context.Context, FileRequest) (Result, error) {
	return Result{}, nil
}

func (p *authRefreshTestProvider) NewRealtime(context.Context, RealtimeSessionConfig) (RealtimeSession, error) {
	return nil, ErrProviderUnsupported
}

func (p *authRefreshTestProvider) EnsureAuthFresh(context.Context) error {
	p.calls.Add(1)
	return p.err
}

func TestAuthRefreshCoordinatorRefreshesAllProvidersIndependently(t *testing.T) {
	first := &authRefreshTestProvider{id: ProviderChatGPT}
	second := &authRefreshTestProvider{id: ProviderM365, err: context.Canceled}
	registry := NewRegistry(second, first)
	refreshers := registry.AuthRefreshers()
	if len(refreshers) != 2 {
		t.Fatalf("refreshers = %d, want 2", len(refreshers))
	}
	if refreshers[0] != first || refreshers[1] != second {
		t.Fatalf("refreshers are not deterministic: %#v", refreshers)
	}

	coordinator := NewAuthRefreshCoordinator(registry, 0)
	coordinator.RefreshAll(context.Background())
	if first.calls.Load() != 1 || second.calls.Load() != 1 {
		t.Fatalf("calls = %d/%d, want one attempt per provider", first.calls.Load(), second.calls.Load())
	}
}

func TestAuthRefreshCoordinatorRunsOneSharedRecurringSchedule(t *testing.T) {
	provider := &authRefreshTestProvider{id: ProviderM365}
	coordinator := NewAuthRefreshCoordinator(NewRegistry(provider), 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	coordinator.Start(ctx)
	coordinator.Start(ctx)

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for provider.calls.Load() < 2 {
		select {
		case <-deadline.C:
			t.Fatalf("recurring calls = %d, want startup plus one scheduled refresh", provider.calls.Load())
		case <-time.After(time.Millisecond):
		}
	}
	calls := provider.calls.Load()
	cancel()
	time.Sleep(20 * time.Millisecond)
	if provider.calls.Load() > calls+1 {
		t.Fatalf("coordinator started duplicate schedules: calls grew from %d to %d", calls, provider.calls.Load())
	}
}
