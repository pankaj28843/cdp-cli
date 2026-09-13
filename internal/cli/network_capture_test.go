package cli

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type networkCaptureLateDrainFakeClient struct {
	mu       sync.Mutex
	drainRun int
	event    cdp.Event
}

func (f *networkCaptureLateDrainFakeClient) Call(context.Context, string, any, any) error {
	return nil
}

func (f *networkCaptureLateDrainFakeClient) CallSession(context.Context, string, string, any, any) error {
	return nil
}

func (f *networkCaptureLateDrainFakeClient) DrainEvents(context.Context) ([]cdp.Event, error) {
	return nil, nil
}

func (f *networkCaptureLateDrainFakeClient) ReadEvent(ctx context.Context) (cdp.Event, error) {
	<-ctx.Done()
	return cdp.Event{}, ctx.Err()
}

func (f *networkCaptureLateDrainFakeClient) DrainSessionEvents(_ context.Context, sessionID string) ([]cdp.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drainRun++
	if f.drainRun != 2 {
		return nil, nil
	}
	f.event.SessionID = sessionID
	return []cdp.Event{f.event}, nil
}

func (f *networkCaptureLateDrainFakeClient) ReadSessionEvent(ctx context.Context, _ string) (cdp.Event, error) {
	<-ctx.Done()
	return cdp.Event{}, ctx.Err()
}

func TestCollectNetworkCaptureDrainsExactSessionAfterWaitDeadline(t *testing.T) {
	fake := &networkCaptureLateDrainFakeClient{event: cdp.Event{
		Method: "Network.requestWillBeSent",
		Params: mustRawJSON(t, map[string]any{
			"requestId": "late-request",
			"type":      "Fetch",
			"request": map[string]any{
				"url":    "https://example.test/late",
				"method": "GET",
			},
		}),
	}}
	records, truncated, collectorErrors, err := collectNetworkCapture(context.Background(), fake, "session-1", networkCaptureOptions{
		Wait:  time.Nanosecond,
		Limit: 0,
	})
	if err != nil {
		t.Fatalf("collectNetworkCapture() error = %v", err)
	}
	if truncated || len(collectorErrors) != 0 || len(records) != 1 || records[0].ID != "late-request" {
		t.Fatalf("capture = records=%+v truncated=%v collector_errors=%v, want late event retained", records, truncated, collectorErrors)
	}
}
