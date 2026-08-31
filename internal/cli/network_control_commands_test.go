package cli

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type networkControlFakeClient struct {
	mu       sync.Mutex
	events   []cdp.Event
	methods  []string
	failOnce map[string]bool
}

func (f *networkControlFakeClient) Call(context.Context, string, any, any) error { return nil }

func (f *networkControlFakeClient) CallSession(_ context.Context, _ string, method string, _ any, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods = append(f.methods, method)
	if f.failOnce[method] {
		delete(f.failOnce, method)
		return errors.New("synthetic " + method + " failure")
	}
	return nil
}

func (f *networkControlFakeClient) DrainEvents(context.Context) ([]cdp.Event, error) { return nil, nil }

func (f *networkControlFakeClient) ReadEvent(ctx context.Context) (cdp.Event, error) {
	f.mu.Lock()
	if len(f.events) > 0 {
		event := f.events[0]
		f.events = f.events[1:]
		f.mu.Unlock()
		return event, nil
	}
	f.mu.Unlock()
	<-ctx.Done()
	return cdp.Event{}, ctx.Err()
}

func (f *networkControlFakeClient) DrainSessionEvents(context.Context, string) ([]cdp.Event, error) {
	return nil, nil
}

func (f *networkControlFakeClient) ReadSessionEvent(ctx context.Context, _ string) (cdp.Event, error) {
	return f.ReadEvent(ctx)
}

func (f *networkControlFakeClient) methodSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func TestCollectMockedRequestsResolvesMatchedAndUnmatched(t *testing.T) {
	rules, err := parseNetworkMockRules([]string{`{"url_pattern":"*://*/api/config","status":200,"body":"{\"enabled\":true}","max_matches":1}`})
	if err != nil {
		t.Fatalf("parseNetworkMockRules() error = %v", err)
	}
	fake := &networkControlFakeClient{events: []cdp.Event{
		{SessionID: "session-1", Method: "Fetch.requestPaused", Params: mustRawJSON(t, map[string]any{"requestId": "matched", "resourceType": "Fetch", "request": map[string]any{"url": "https://example.test/api/config", "method": "GET"}})},
		{SessionID: "session-1", Method: "Fetch.requestPaused", Params: mustRawJSON(t, map[string]any{"requestId": "unmatched", "resourceType": "Fetch", "request": map[string]any{"url": "https://example.test/api/other", "method": "GET"}})},
	}}
	pending := map[string]bool{}
	actions, err := collectMockedRequests(context.Background(), fake, "session-1", 10*time.Millisecond, rules, pending)
	if err != nil {
		t.Fatalf("collectMockedRequests() error = %v", err)
	}
	if actions["fulfilled"] != 1 || actions["continued"] != 1 || len(pending) != 0 || rules[0].Matched != 1 {
		t.Fatalf("mock actions=%+v pending=%+v rule=%+v", actions, pending, rules[0])
	}
	if want := []string{"Fetch.fulfillRequest", "Fetch.continueRequest"}; !reflect.DeepEqual(fake.methodSnapshot(), want) {
		t.Fatalf("methods = %v, want %v", fake.methodSnapshot(), want)
	}
}

func TestCleanupFetchInterceptionUsesIndependentContext(t *testing.T) {
	fake := &networkControlFakeClient{}
	pending := map[string]bool{"request-b": true, "request-a": true}
	cleanup := networkControlCleanup{}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collectMockedRequests(canceled, fake, "session-1", time.Second, nil, map[string]bool{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("collectMockedRequests() error = %v, want canceled", err)
	}
	cleanupFetchInterception(fake, "session-1", pending, &cleanup)
	if !cleanup.Complete || !cleanup.FetchDisabled || cleanup.PendingReleased != 2 || len(pending) != 0 {
		t.Fatalf("cleanup = %+v pending=%+v", cleanup, pending)
	}
	if want := []string{"Fetch.continueRequest", "Fetch.continueRequest", "Fetch.disable"}; !reflect.DeepEqual(fake.methodSnapshot(), want) {
		t.Fatalf("methods = %v, want %v", fake.methodSnapshot(), want)
	}
}

func TestCollectMockedRequestsFailOpenResolvesActionFailure(t *testing.T) {
	rules, err := parseNetworkMockRules([]string{`{"url_pattern":"*://*/api/config","status":200,"body":"ok","max_matches":1}`})
	if err != nil {
		t.Fatal(err)
	}
	fake := &networkControlFakeClient{
		failOnce: map[string]bool{"Fetch.fulfillRequest": true},
		events: []cdp.Event{{SessionID: "session-1", Method: "Fetch.requestPaused", Params: mustRawJSON(t, map[string]any{
			"requestId": "request-1", "resourceType": "Fetch", "request": map[string]any{"url": "https://example.test/api/config", "method": "GET"},
		})}},
	}
	pending := map[string]bool{}
	actions, err := collectMockedRequests(context.Background(), fake, "session-1", 10*time.Millisecond, rules, pending)
	if err != nil {
		t.Fatalf("collectMockedRequests() error = %v", err)
	}
	if actions["failed"] != 1 || actions["fallback_continued"] != 1 || actions["fulfilled"] != 0 || len(pending) != 0 {
		t.Fatalf("actions=%+v pending=%+v, want failed fulfillment resolved fail-open", actions, pending)
	}
	if want := []string{"Fetch.fulfillRequest", "Fetch.continueRequest"}; !reflect.DeepEqual(fake.methodSnapshot(), want) {
		t.Fatalf("methods = %v, want %v", fake.methodSnapshot(), want)
	}
}

func TestCleanupNetworkBlockClearsBeforeDisable(t *testing.T) {
	fake := &networkControlFakeClient{}
	cleanup := networkControlCleanup{}
	cleanupNetworkBlock(fake, "session-1", &cleanup)
	if !cleanup.Complete || !cleanup.BlockedURLsCleared || !cleanup.NetworkDisabled {
		t.Fatalf("cleanup = %+v", cleanup)
	}
	if want := []string{"Network.setBlockedURLs", "Network.disable"}; !reflect.DeepEqual(fake.methodSnapshot(), want) {
		t.Fatalf("methods = %v, want %v", fake.methodSnapshot(), want)
	}
}

func mustRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
