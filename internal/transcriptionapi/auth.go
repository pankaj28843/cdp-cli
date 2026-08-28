package transcriptionapi

import (
	"context"
	"sync"
	"time"
)

const (
	// Keep the shared schedule inside the shortest provider auth freshness
	// window, including the provider's proactive refresh safety margin.
	DefaultAuthRefreshInterval = 10 * time.Minute
	DefaultAuthRefreshTimeout  = 2 * time.Minute
	// Request-time auth repair must have a shorter bound than the listener's
	// write timeout. A stale headed session should become a recoverable API
	// error, not leave a mobile client in an endless finalizing state.
	DefaultRequestAuthTimeout = 45 * time.Second
)

// AuthRefresher is an optional provider capability. Browser-backed providers
// implement it; local providers do not need to. EnsureAuthFresh must be safe
// to call concurrently with a transcription request and must own its
// provider-specific freshness check and refresh operation.
type AuthRefresher interface {
	EnsureAuthFresh(context.Context) error
}

// AuthRefreshCoordinator owns the one service-level auth and capability
// freshness schedule. It is deliberately provider-neutral: a stale or broken
// provider cannot prevent other providers from being refreshed, and adding a
// provider does not add a process-level cron job.
type AuthRefreshCoordinator struct {
	targets       []providerRefreshTarget
	interval      time.Duration
	offset        time.Duration
	timeout       time.Duration
	startOnce     sync.Once
	initialDone   chan struct{}
	initialDoneMu sync.Once
	runMu         sync.Mutex
}

// SetScheduleOffset aligns recurring refreshes to a stable wall-clock phase.
// The startup refresh remains immediate; callers set the offset before Start.
func (c *AuthRefreshCoordinator) SetScheduleOffset(offset time.Duration) {
	if c == nil || c.interval <= 0 {
		return
	}
	if offset < 0 {
		offset = 0
	}
	c.offset = offset % c.interval
}

func NewAuthRefreshCoordinator(registry *Registry, interval time.Duration) *AuthRefreshCoordinator {
	if interval < 0 {
		interval = DefaultAuthRefreshInterval
	}
	targets := []providerRefreshTarget(nil)
	if registry != nil {
		targets = registry.refreshTargets()
	}
	initialDone := make(chan struct{})
	if interval <= 0 || len(targets) == 0 {
		close(initialDone)
	}
	return &AuthRefreshCoordinator{
		targets:     targets,
		interval:    interval,
		timeout:     DefaultAuthRefreshTimeout,
		initialDone: initialDone,
	}
}

// Start performs one proactive pass and then refreshes on the shared cadence.
// It never returns provider errors to the listener; request-time preparation
// remains the authoritative failure path and each provider is isolated from
// its siblings.
func (c *AuthRefreshCoordinator) Start(ctx context.Context) {
	if c == nil || c.interval <= 0 || len(c.targets) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.startOnce.Do(func() {
		go func() {
			c.RefreshAll(ctx)
			c.markInitialDone()
			for {
				timer := time.NewTimer(nextAlignedScheduleDelay(time.Now().UTC(), c.interval, c.offset))
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
					c.RefreshAll(ctx)
				}
			}
		}()
	})
}

func nextAlignedScheduleDelay(now time.Time, interval, offset time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	offset %= interval
	if offset < 0 {
		offset += interval
	}
	phase := time.Duration(now.UnixNano() % int64(interval))
	delay := offset - phase
	if delay <= 0 {
		delay += interval
	}
	return delay
}

// WaitInitial waits until the first proactive pass has finished. The
// transcription health probe uses this once at service startup so it cannot
// publish a stale provider_not_ready result while auth/capability repair is in
// flight. Provider errors remain isolated and are reflected by the provider's
// cached readiness state; only cancellation is returned here.
func (c *AuthRefreshCoordinator) WaitInitial(ctx context.Context) error {
	if c == nil || c.interval <= 0 || len(c.targets) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-c.initialDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *AuthRefreshCoordinator) markInitialDone() {
	if c == nil || c.initialDone == nil {
		return
	}
	c.initialDoneMu.Do(func() { close(c.initialDone) })
}

// RefreshAll refreshes auth and capability evidence for every configured online
// provider independently. Auth runs before capabilities for each provider so
// a capability observation never races a provider's own auth repair. The
// method is exported for deterministic service tests and explicit maintenance
// commands; normal serving uses Start.
func (c *AuthRefreshCoordinator) RefreshAll(ctx context.Context) {
	if c == nil || len(c.targets) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !c.runMu.TryLock() {
		return
	}
	defer c.runMu.Unlock()
	var wait sync.WaitGroup
	for _, target := range c.targets {
		target := target
		wait.Add(1)
		go func() {
			defer wait.Done()
			refreshContext := ctx
			cancel := func() {}
			if c.timeout > 0 {
				refreshContext, cancel = context.WithTimeout(ctx, c.timeout)
			}
			defer cancel()
			if target.auth != nil {
				// Capability evidence is meaningful only after the provider's
				// auth evidence is fresh. A failed provider stays isolated and
				// will be retried on the next bounded cadence.
				if err := target.auth.EnsureAuthFresh(refreshContext); err != nil {
					return
				}
			}
			if target.capabilities != nil {
				_ = target.capabilities.EnsureCapabilitiesFresh(refreshContext)
			}
		}()
	}
	wait.Wait()
}
