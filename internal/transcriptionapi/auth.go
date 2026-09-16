package transcriptionapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// AuthRefreshMode determines who is allowed to repair browser-backed provider
// state. External mode is for leaf services that receive owner-only state from
// a separate authority and must never open a provider tab themselves.
type AuthRefreshMode string

const (
	AuthRefreshModeLocal    AuthRefreshMode = "local"
	AuthRefreshModeExternal AuthRefreshMode = "external"
)

func ParseAuthRefreshMode(raw string) (AuthRefreshMode, error) {
	mode := AuthRefreshMode(strings.ToLower(strings.TrimSpace(raw)))
	if mode == "" {
		mode = AuthRefreshModeLocal
	}
	switch mode {
	case AuthRefreshModeLocal, AuthRefreshModeExternal:
		return mode, nil
	default:
		return "", fmt.Errorf("auth refresh mode must be local or external")
	}
}

func (m AuthRefreshMode) Validate(interval time.Duration) error {
	parsed, err := ParseAuthRefreshMode(string(m))
	if err != nil {
		return err
	}
	if parsed == AuthRefreshModeExternal && interval != 0 {
		return fmt.Errorf("external auth refresh mode requires a zero local refresh interval")
	}
	return nil
}

// AuthRefresher is an optional provider capability. Browser-backed providers
// implement it; local providers do not need to. EnsureAuthFresh must be safe
// to call concurrently with a transcription request and must own its
// provider-specific freshness check and refresh operation.
type AuthRefresher interface {
	EnsureAuthFresh(context.Context) error
}

// AuthorityAuthRefresher is implemented only by a service allowed to own
// browser-backed auth repair. AuthCapturedAt supplies the server-enforced
// cooldown boundary; RefreshAuthNow performs one provider-specific refresh.
type AuthorityAuthRefresher interface {
	AuthCapturedAt(context.Context) (time.Time, bool)
	RefreshAuthNow(context.Context, time.Time) (bool, error)
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
	retries       map[ProviderID]authRefreshRetry
	lastAttempt   map[ProviderID]time.Time
	wake          chan struct{}
}

type authRefreshRetry struct {
	failures int
	next     time.Time
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
		retries:     make(map[ProviderID]authRefreshRetry),
		lastAttempt: make(map[ProviderID]time.Time),
		wake:        make(chan struct{}, 1),
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
				timer := time.NewTimer(c.nextDelay(time.Now().UTC()))
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
					c.RefreshAll(ctx)
				case <-c.wake:
					timer.Stop()
				}
			}
		}()
	})
}

// lockRequest lets on-demand callers join lifecycle work within their own deadline.
func (c *AuthRefreshCoordinator) lockRequest(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.runMu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *AuthRefreshCoordinator) nextDelay(now time.Time) time.Duration {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	delay := nextAlignedScheduleDelay(now, c.interval, c.offset)
	for _, retry := range c.retries {
		if remaining := retry.next.Sub(now); remaining < delay {
			delay = remaining
			if delay < time.Millisecond {
				delay = time.Millisecond
			}
		}
	}
	return delay
}

// recordAttempt runs under runMu for both scheduled and authority API work.
// Success resumes the ordinary cadence; failures cannot multiply across callers.
func (c *AuthRefreshCoordinator) recordAttempt(id ProviderID, err error, now time.Time) {
	c.lastAttempt[id] = now
	if err == nil {
		delete(c.retries, id)
		return
	}
	retry := c.retries[id]
	retry.failures++
	delay := AuthRefreshRetryDelay(err, retry.failures)
	retry.next = now.Add(delay)
	c.retries[id] = retry
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// AuthRefreshRetryDelay bounds failed refreshes across scheduled and direct repair paths.
func AuthRefreshRetryDelay(err error, failures int) time.Duration {
	delay := time.Minute
	if failures == 2 {
		delay = 2 * time.Minute
	} else if failures > 2 {
		delay = 5 * time.Minute
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && (providerErr.Status == 401 || providerErr.Status == 403 || providerErr.Status == 429) {
		// Explicit auth rejection/rate limiting is not an infrastructure outage.
		delay = 15 * time.Minute
	}
	return delay
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
// provider sequentially. Auth runs before capabilities for each provider so a
// capability observation never races a provider's own auth repair, and two
// providers never compete for the single headed browser target budget. Errors
// remain isolated: one failed provider does not prevent later providers from
// being attempted. The method is exported for deterministic service tests and
// explicit maintenance commands; normal serving uses Start.
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
	for _, target := range c.targets {
		if ctx.Err() != nil {
			return
		}
		if time.Now().Before(c.retries[target.id].next) {
			continue
		}
		refreshContext := ctx
		cancel := func() {}
		if c.timeout > 0 {
			refreshContext, cancel = context.WithTimeout(ctx, c.timeout)
		}
		if target.auth != nil {
			// Capability evidence is meaningful only after the provider's
			// auth evidence is fresh. A failed provider stays isolated and
			// will be retried on the next bounded cadence.
			if err := target.auth.EnsureAuthFresh(refreshContext); err != nil {
				c.recordAttempt(target.id, err, time.Now())
				cancel()
				continue
			}
		}
		var err error
		if target.capabilities != nil {
			err = target.capabilities.EnsureCapabilitiesFresh(refreshContext)
		}
		c.recordAttempt(target.id, err, time.Now())
		cancel()
	}
}
