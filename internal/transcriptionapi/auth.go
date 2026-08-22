package transcriptionapi

import (
	"context"
	"sync"
	"time"
)

const (
	DefaultAuthRefreshInterval = time.Hour
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
	targets   []providerRefreshTarget
	interval  time.Duration
	timeout   time.Duration
	startOnce sync.Once
	runMu     sync.Mutex
}

func NewAuthRefreshCoordinator(registry *Registry, interval time.Duration) *AuthRefreshCoordinator {
	if interval < 0 {
		interval = DefaultAuthRefreshInterval
	}
	targets := []providerRefreshTarget(nil)
	if registry != nil {
		targets = registry.refreshTargets()
	}
	return &AuthRefreshCoordinator{
		targets:  targets,
		interval: interval,
		timeout:  DefaultAuthRefreshTimeout,
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
			ticker := time.NewTicker(c.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					c.RefreshAll(ctx)
				}
			}
		}()
	})
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
				_ = target.auth.EnsureAuthFresh(refreshContext)
			}
			if target.capabilities != nil {
				_ = target.capabilities.EnsureCapabilitiesFresh(refreshContext)
			}
		}()
	}
	wait.Wait()
}
