package transcriptionapi

import (
	"context"
	"sync"
	"time"
)

const storageProbeInterval = 10 * time.Second
const storageProbeMaxAge = 30 * time.Second

// One disk probe serves all health requests. Even creating a disposable file
// can block on filesystem journal I/O; that must not block the HTTP listener's
// health response. Results expire, and real persistence failures invalidate them.
type storageHealthProbe struct {
	mu         sync.Mutex
	check      func() error
	now        func() time.Time
	checkedAt  time.Time
	ready      bool
	pending    chan struct{}
	generation uint64
}

type storageHealthSnapshot struct {
	Ready         bool   `json:"ready"`
	Pending       bool   `json:"pending"`
	CheckedAt     string `json:"checked_at,omitempty"`
	MaxAgeSeconds int    `json:"max_age_seconds"`
}

func (p *storageHealthProbe) snapshotLocked() storageHealthSnapshot {
	state := storageHealthSnapshot{
		Ready:   p.ready && p.now().Sub(p.checkedAt) < storageProbeMaxAge,
		Pending: p.pending != nil, MaxAgeSeconds: int(storageProbeMaxAge / time.Second),
	}
	if !p.checkedAt.IsZero() {
		state.CheckedAt = p.checkedAt.UTC().Format(time.RFC3339Nano)
	}
	return state
}

func (p *storageHealthProbe) snapshot(ctx context.Context) storageHealthSnapshot {
	p.mu.Lock()
	if p.pending == nil && (!p.ready || p.now().Sub(p.checkedAt) >= storageProbeInterval) {
		done, generation := make(chan struct{}), p.generation
		p.pending = done
		go func() {
			err := p.check()
			p.mu.Lock()
			if p.generation == generation {
				p.ready, p.checkedAt = err == nil, p.now()
			}
			p.pending = nil
			close(done)
			p.mu.Unlock()
		}()
	}
	state, done := p.snapshotLocked(), p.pending
	p.mu.Unlock()
	if state.Ready || done == nil {
		return state
	}
	// A first/failed/expired check may finish quickly, but a stuck syscall
	// cannot occupy every health handler or spawn additional probe workers.
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
	case <-timer.C:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

func (p *storageHealthProbe) invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ready = false
	p.generation++ // An older in-flight success cannot erase a request failure.
}
