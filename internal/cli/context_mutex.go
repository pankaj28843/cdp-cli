package cli

import (
	"context"
	"sync"
)

// contextMutex serializes provider lifecycle work without making a request
// wait forever behind another headed browser operation. Its zero value is
// ready for providers constructed directly in tests as well as the registry.
type contextMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *contextMutex) init() {
	m.once.Do(func() {
		m.token = make(chan struct{}, 1)
		m.token <- struct{}{}
	})
}

func (m *contextMutex) Lock(ctx context.Context) error {
	if m == nil {
		return context.Canceled
	}
	m.init()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-m.token:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *contextMutex) Unlock() {
	if m == nil {
		return
	}
	m.init()
	m.token <- struct{}{}
}
