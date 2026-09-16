package transcriptionapi

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/providerpolicy"
)

type authRefreshTestProvider struct {
	id              ProviderID
	calls           atomic.Int32
	capabilityCalls atomic.Int32
	err             error
	capabilityErr   error
	active          *atomic.Int32
	maxActive       *atomic.Int32
	delay           time.Duration
}

func (p *authRefreshTestProvider) ID() ProviderID { return p.id }

func (p *authRefreshTestProvider) Capabilities(context.Context) ProviderCapability {
	p.capabilityCalls.Add(1)
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
	if p.active != nil && p.maxActive != nil {
		active := p.active.Add(1)
		for current := p.maxActive.Load(); active > current && !p.maxActive.CompareAndSwap(current, active); current = p.maxActive.Load() {
		}
		defer p.active.Add(-1)
	}
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	return p.err
}

func (p *authRefreshTestProvider) EnsureCapabilitiesFresh(context.Context) error {
	p.capabilityCalls.Add(1)
	return p.capabilityErr
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

func TestAuthRefreshCoordinatorDoesNotHammerFailedProvider(t *testing.T) {
	provider := &authRefreshTestProvider{id: ProviderChatGPT, err: &ProviderError{Status: 503, Retryable: true}}
	coordinator := NewAuthRefreshCoordinator(NewRegistry(provider), 15*time.Minute)
	coordinator.RefreshAll(context.Background())
	coordinator.RefreshAll(context.Background())
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("back-to-back failed refresh calls = %d, want one bounded attempt", got)
	}
}

func TestAuthRefreshFailureRetriesAreBoundedAndSuccessResetsThem(t *testing.T) {
	provider := &authRefreshTestProvider{id: ProviderChatGPT, err: &ProviderError{Status: 503, Retryable: true}}
	c := NewAuthRefreshCoordinator(NewRegistry(provider), 15*time.Minute)
	for _, want := range []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		previous := c.retries[provider.id]
		previous.next = time.Now().Add(-time.Second)
		c.retries[provider.id] = previous
		before := time.Now()
		c.RefreshAll(context.Background())
		delay := c.retries[provider.id].next.Sub(before)
		if delay < want || delay > want+time.Second {
			t.Fatalf("retry delay = %s, want %s", delay, want)
		}
		if next := c.nextDelay(time.Now()); next > want+time.Second {
			t.Fatalf("scheduler sleeps %s past retry budget %s", next, want)
		}
	}
	provider.err = nil
	retry := c.retries[provider.id]
	retry.next = time.Now().Add(-time.Second)
	c.retries[provider.id] = retry
	c.RefreshAll(context.Background())
	if len(c.retries) != 0 {
		t.Fatal("successful repair did not resume the ordinary freshness cadence")
	}
}

func TestAuthRefreshDoesNotFastRetryAuthRejectionOrRateLimit(t *testing.T) {
	for _, status := range []int{401, 403, 429} {
		c := NewAuthRefreshCoordinator(nil, 15*time.Minute)
		now := time.Now()
		c.recordAttempt(ProviderChatGPT, &ProviderError{Status: status, Retryable: true}, now)
		if delay := c.retries[ProviderChatGPT].next.Sub(now); delay < 15*time.Minute {
			t.Fatalf("status %d got unsafe fast retry %s", status, delay)
		}
	}
}

func TestAuthRefreshCoordinatorSerializesProviders(t *testing.T) {
	var active atomic.Int32
	var maxActive atomic.Int32
	first := &authRefreshTestProvider{id: ProviderChatGPT, active: &active, maxActive: &maxActive, delay: 10 * time.Millisecond}
	second := &authRefreshTestProvider{id: ProviderClaude, active: &active, maxActive: &maxActive, delay: 10 * time.Millisecond}
	third := &authRefreshTestProvider{id: ProviderGemini, active: &active, maxActive: &maxActive, delay: 10 * time.Millisecond}

	NewAuthRefreshCoordinator(NewRegistry(third, first, second), 0).RefreshAll(context.Background())

	if maxActive.Load() != 1 {
		t.Fatalf("concurrent provider refreshes = %d, want 1", maxActive.Load())
	}
	for _, provider := range []*authRefreshTestProvider{first, second, third} {
		if provider.calls.Load() != 1 {
			t.Fatalf("provider %s calls = %d, want 1", provider.id, provider.calls.Load())
		}
	}
}

func TestRegistryPolicyOmitsDisabledCapabilitiesAndRefreshers(t *testing.T) {
	first := &authRefreshTestProvider{id: ProviderChatGPT}
	second := &authRefreshTestProvider{id: ProviderM365}
	policy, err := providerpolicy.New([]string{"chatgpt"})
	if err != nil {
		t.Fatalf("provider policy: %v", err)
	}
	registry := NewRegistryWithPolicy(policy, second, first)

	capabilities := registry.Capabilities(context.Background())
	if len(capabilities) != 1 || capabilities[0].Provider != ProviderM365 {
		t.Fatalf("ordinary capabilities = %+v", capabilities)
	}
	diagnostic := registry.DiagnosticCapabilities(context.Background())
	if len(diagnostic) != 2 || diagnostic[0].Provider != ProviderChatGPT || diagnostic[0].Reason != "disabled_by_config" || diagnostic[0].Ready {
		t.Fatalf("diagnostic capabilities = %+v", diagnostic)
	}
	if first.capabilityCalls.Load() != 0 {
		t.Fatalf("disabled provider capability calls = %d, want 0", first.capabilityCalls.Load())
	}
	if second.capabilityCalls.Load() != 2 {
		t.Fatalf("enabled provider capability calls = %d, want ordinary plus diagnostic", second.capabilityCalls.Load())
	}
	refreshers := registry.AuthRefreshers()
	if len(refreshers) != 1 || refreshers[0] != second {
		t.Fatalf("policy refreshers = %#v", refreshers)
	}
}

func TestRegistryPolicyBlocksDisabledExplicitAndFallbackSelection(t *testing.T) {
	chatGPT := &authRefreshTestProvider{id: ProviderChatGPT}
	m365 := &authRefreshTestProvider{id: ProviderM365}
	policy, err := providerpolicy.New([]string{"chatgpt"})
	if err != nil {
		t.Fatalf("provider policy: %v", err)
	}
	registry := NewRegistryWithPolicy(policy, chatGPT, m365)
	for name, test := range map[string]struct {
		selected ProviderID
		fallback ProviderID
	}{
		"explicit": {selected: ProviderChatGPT, fallback: ProviderM365},
		"fallback": {selected: "", fallback: ProviderChatGPT},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := registry.Select(test.selected, test.fallback)
			if err == nil {
				t.Fatal("Select returned nil error")
			}
			providerErr, ok := err.(*ProviderError)
			if !ok || providerErr.APIError.Code != "provider_disabled" || providerErr.Status != 403 {
				t.Fatalf("Select error = %T %+v", err, err)
			}
		})
	}
}

func TestAuthRefreshCoordinatorRefreshesAuthAndCapabilitiesPerProvider(t *testing.T) {
	first := &authRefreshTestProvider{
		id:            ProviderChatGPT,
		capabilityErr: context.Canceled,
	}
	second := &authRefreshTestProvider{
		id:  ProviderM365,
		err: context.Canceled,
	}
	coordinator := NewAuthRefreshCoordinator(NewRegistry(second, first), 0)

	coordinator.RefreshAll(context.Background())

	for _, provider := range []*authRefreshTestProvider{first, second} {
		if provider.calls.Load() != 1 {
			t.Fatalf("provider %s auth calls = %d, want one", provider.id, provider.calls.Load())
		}
	}
	if first.capabilityCalls.Load() != 1 {
		t.Fatalf("provider %s capability calls = %d, want one after auth success", first.id, first.capabilityCalls.Load())
	}
	if second.capabilityCalls.Load() != 0 {
		t.Fatalf("provider %s capability calls = %d, want zero after auth failure", second.id, second.capabilityCalls.Load())
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
	capabilityCalls := provider.capabilityCalls.Load()
	cancel()
	time.Sleep(20 * time.Millisecond)
	if provider.calls.Load() > calls+1 {
		t.Fatalf("coordinator started duplicate schedules: calls grew from %d to %d", calls, provider.calls.Load())
	}
	if capabilityCalls < 2 {
		t.Fatalf("capability calls = %d, want startup plus one scheduled refresh", capabilityCalls)
	}
	if provider.capabilityCalls.Load() > capabilityCalls+1 {
		t.Fatalf("coordinator started duplicate capability schedules: calls grew from %d to %d", capabilityCalls, provider.capabilityCalls.Load())
	}
}

func TestNextAlignedScheduleDelayUsesWallClockOffset(t *testing.T) {
	interval := 15 * time.Minute
	for _, test := range []struct {
		name   string
		now    time.Time
		offset time.Duration
		want   time.Duration
	}{
		{
			name:   "before slot",
			now:    time.Date(2026, 8, 28, 6, 7, 0, 0, time.UTC),
			offset: 10 * time.Minute,
			want:   3 * time.Minute,
		},
		{
			name:   "at slot schedules next interval",
			now:    time.Date(2026, 8, 28, 6, 10, 0, 0, time.UTC),
			offset: 10 * time.Minute,
			want:   15 * time.Minute,
		},
		{
			name:   "after slot wraps",
			now:    time.Date(2026, 8, 28, 6, 12, 0, 0, time.UTC),
			offset: 10 * time.Minute,
			want:   13 * time.Minute,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nextAlignedScheduleDelay(test.now, interval, test.offset); got != test.want {
				t.Fatalf("delay = %s, want %s", got, test.want)
			}
		})
	}
}

// A stale provider becomes ready only after its one request-owned repair.
type onDemandAuthProvider struct {
	fakeProvider
	ready atomic.Bool
	calls atomic.Int32
}

func (p *onDemandAuthProvider) Capabilities(context.Context) ProviderCapability {
	return ProviderCapability{Provider: p.id, File: true, Ready: p.ready.Load()}
}

func (p *onDemandAuthProvider) EnsureAuthFresh(ctx context.Context) error {
	p.calls.Add(1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Millisecond):
		p.ready.Store(true)
		return nil
	}
}

func TestStaleRequestAuthRefreshesImmediatelyAndConcurrentRequestsJoin(t *testing.T) {
	p := &onDemandAuthProvider{fakeProvider: fakeProvider{id: ProviderChatGPT}}
	s := &Server{config: ServerConfig{AuthTimeout: time.Second, AuthCoordinator: NewAuthRefreshCoordinator(NewRegistry(p), 15*time.Minute)}}
	results := make(chan error, 8)
	for range 8 {
		go func() { results <- s.ensureProviderAuth(context.Background(), p) }()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if p.calls.Load() != 1 || !p.ready.Load() {
		t.Fatalf("calls=%d ready=%v", p.calls.Load(), p.ready.Load())
	}
}

func TestRequestAuthWaitHonorsDeadline(t *testing.T) {
	p := &onDemandAuthProvider{fakeProvider: fakeProvider{id: ProviderChatGPT}}
	c := NewAuthRefreshCoordinator(NewRegistry(p), 0)
	c.runMu.Lock()
	defer c.runMu.Unlock()
	s := &Server{config: ServerConfig{AuthTimeout: 20 * time.Millisecond, AuthCoordinator: c}}
	start := time.Now()
	if err := s.ensureProviderAuth(context.Background(), p); err == nil {
		t.Fatal("expected bounded wait failure")
	}
	if time.Since(start) > time.Second || p.calls.Load() != 0 {
		t.Fatal("wait ignored deadline or started another refresh")
	}
}
