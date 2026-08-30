package cli

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type eventStreamLivenessFakeClient struct {
	mu        sync.Mutex
	outcomes  []error
	callCount int
	lastCall  eventStreamLivenessCall
}

type eventStreamLivenessCall struct {
	sessionID string
	method    string
	params    map[string]any
}

func (f *eventStreamLivenessFakeClient) Call(context.Context, string, any, any) error {
	return nil
}

func (f *eventStreamLivenessFakeClient) CallSession(ctx context.Context, sessionID, method string, params any, _ any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	paramsMap, _ := params.(map[string]any)
	f.mu.Lock()
	f.callCount++
	f.lastCall = eventStreamLivenessCall{sessionID: sessionID, method: method, params: paramsMap}
	var err error
	if len(f.outcomes) > 0 {
		err = f.outcomes[0]
		f.outcomes = f.outcomes[1:]
	}
	f.mu.Unlock()
	return err
}

func (f *eventStreamLivenessFakeClient) DrainEvents(context.Context) ([]cdp.Event, error) {
	return nil, nil
}

func (f *eventStreamLivenessFakeClient) ReadEvent(ctx context.Context) (cdp.Event, error) {
	<-ctx.Done()
	return cdp.Event{}, ctx.Err()
}

func (f *eventStreamLivenessFakeClient) DrainSessionEvents(context.Context, string) ([]cdp.Event, error) {
	return nil, nil
}

func (f *eventStreamLivenessFakeClient) ReadSessionEvent(ctx context.Context, _ string) (cdp.Event, error) {
	<-ctx.Done()
	return cdp.Event{}, ctx.Err()
}

func (f *eventStreamLivenessFakeClient) snapshot() (int, eventStreamLivenessCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount, f.lastCall
}

func TestPumpEventStreamLivenessRetiresAfterConsecutiveFailuresAndResets(t *testing.T) {
	fake := &eventStreamLivenessFakeClient{outcomes: []error{
		errors.New("transient heartbeat failure"),
		nil,
		errors.New("first terminal heartbeat failure"),
		errors.New("second terminal heartbeat failure"),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := pumpEventStreamLiveness(ctx, fake, "session-1", time.Millisecond, 2)
	select {
	case result, ok := <-results:
		if !ok {
			t.Fatal("liveness result channel closed before retirement")
		}
		if !result.retired || result.consecutiveFailures != 2 || result.reason != "exact_session_unhealthy" {
			t.Fatalf("liveness result = %+v, want two-strike retirement", result)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("liveness pump did not retire after consecutive failures")
	}

	count, call := fake.snapshot()
	if count < 4 {
		t.Fatalf("heartbeat calls = %d, want transient failure, reset, and two strikes", count)
	}
	if call.sessionID != "session-1" || call.method != "Runtime.evaluate" {
		t.Fatalf("last heartbeat call = %+v, want exact-session Runtime.evaluate", call)
	}
	if got := call.params["expression"]; got != "void 0" {
		t.Fatalf("heartbeat expression = %#v, want read-only void 0", got)
	}
	if got := call.params["returnByValue"]; got != true {
		t.Fatalf("heartbeat returnByValue = %#v, want true", got)
	}
}

func TestPumpEventStreamLivenessCancellationStopsFurtherCalls(t *testing.T) {
	fake := &eventStreamLivenessFakeClient{}
	ctx, cancel := context.WithCancel(context.Background())
	results := pumpEventStreamLiveness(ctx, fake, "session-1", time.Millisecond, 2)

	deadline := time.After(100 * time.Millisecond)
	for {
		count, _ := fake.snapshot()
		if count > 0 {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("liveness pump did not issue an initial heartbeat")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()

	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("liveness pump emitted a retirement result after cancellation")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("liveness pump did not close after cancellation")
	}
	count, _ := fake.snapshot()
	time.Sleep(5 * time.Millisecond)
	if later, _ := fake.snapshot(); later != count {
		t.Fatalf("heartbeat calls after cancellation changed from %d to %d", count, later)
	}
}
