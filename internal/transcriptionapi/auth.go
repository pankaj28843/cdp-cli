package transcriptionapi

import (
	"context"
	"sync"
	"time"
)

const (
	DefaultAuthRefreshInterval = 10 * time.Minute
	DefaultAuthRefreshTimeout  = 2 * time.Minute
)

// AuthRefresher is an optional provider capability. Browser-backed providers
// implement it; local providers do not need to. EnsureAuthFresh must be safe
// to call concurrently with a transcription request and must own its
// provider-specific freshness check and refresh operation.
type AuthRefresher interface {
	EnsureAuthFresh(context.Context) error
}

// AuthRefreshCoordinator owns the one service-level refresh schedule. It is
// deliberately provider-neutral: a stale or broken provider cannot prevent
// other providers from being refreshed, and adding a provider does not add a
// process-level cron job.
type AuthRefreshCoordinator struct {
	providers []AuthRefresher
	interval  time.Duration
	timeout   time.Duration
	startOnce sync.Once
	runMu     sync.Mutex
}

func NewAuthRefreshCoordinator(registry *Registry, interval time.Duration) *AuthRefreshCoordinator {
	if interval <= 0 {
		interval = DefaultAuthRefreshInterval
	}
	providers := []AuthRefresher(nil)
	if registry != nil {
		providers = registry.AuthRefreshers()
	}
	return &AuthRefreshCoordinator{
		providers: providers,
		interval:  interval,
		timeout:   DefaultAuthRefreshTimeout,
	}
}

// Start performs one proactive pass and then refreshes on the shared cadence.
// It never returns provider errors to the listener; request-time preparation
// remains the authoritative failure path and each provider is isolated from
// its siblings.
func (c *AuthRefreshCoordinator) Start(ctx context.Context) {
	if c == nil || len(c.providers) == 0 {
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

// RefreshAll refreshes every configured online provider independently. The
// method is exported for deterministic service tests and explicit maintenance
// commands; normal serving uses Start.
func (c *AuthRefreshCoordinator) RefreshAll(ctx context.Context) {
	if c == nil || len(c.providers) == 0 {
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
	for _, provider := range c.providers {
		provider := provider
		wait.Add(1)
		go func() {
			defer wait.Done()
			refreshContext := ctx
			cancel := func() {}
			if c.timeout > 0 {
				refreshContext, cancel = context.WithTimeout(ctx, c.timeout)
			}
			defer cancel()
			_ = provider.EnsureAuthFresh(refreshContext)
		}()
	}
	wait.Wait()
}
