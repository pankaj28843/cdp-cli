package cdp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDrainSessionEventsPreservesForeignOrder(t *testing.T) {
	client := &Client{eventNotify: make(chan struct{})}
	client.bufferEvent(Event{SessionID: "session-b", Method: "B.one"})
	client.bufferEvent(Event{SessionID: "session-a", Method: "A.one"})
	client.bufferEvent(Event{SessionID: "session-b", Method: "B.two"})
	client.bufferEvent(Event{SessionID: "session-a", Method: "A.two"})

	got := client.DrainSessionEvents("session-a")
	if methods := eventMethods(got); methods != "A.one,A.two" {
		t.Fatalf("session-a drain methods = %q, want A.one,A.two", methods)
	}
	if methods := eventMethods(client.DrainEvents()); methods != "B.one,B.two" {
		t.Fatalf("retained foreign methods = %q, want B.one,B.two", methods)
	}
}

func TestReadSessionEventLeavesForeignEventAvailable(t *testing.T) {
	client := &Client{eventNotify: make(chan struct{})}
	client.bufferEvent(Event{SessionID: "session-b", Method: "B.ready"})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.ReadSessionEvent(ctx, "session-a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("session-a read error = %v, want deadline while only session-b is buffered", err)
	}
	event, err := client.ReadSessionEvent(context.Background(), "session-b")
	if err != nil || event.Method != "B.ready" {
		t.Fatalf("session-b read = (%+v, %v), want retained B.ready", event, err)
	}
}

func TestReadSessionEventWakesConcurrentExactReaders(t *testing.T) {
	client := &Client{eventNotify: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	type result struct {
		event Event
		err   error
	}
	read := func(sessionID string) <-chan result {
		ch := make(chan result, 1)
		go func() {
			event, err := client.ReadSessionEvent(ctx, sessionID)
			ch <- result{event: event, err: err}
		}()
		return ch
	}
	aResult := read("session-a")
	bResult := read("session-b")
	client.bufferEvent(Event{SessionID: "session-b", Method: "B.event"})
	client.bufferEvent(Event{SessionID: "session-a", Method: "A.event"})

	if got := <-aResult; got.err != nil || got.event.Method != "A.event" {
		t.Fatalf("session-a read = (%+v, %v)", got.event, got.err)
	}
	if got := <-bResult; got.err != nil || got.event.Method != "B.event" {
		t.Fatalf("session-b read = (%+v, %v)", got.event, got.err)
	}
}

func eventMethods(events []Event) string {
	methods := ""
	for _, event := range events {
		if methods != "" {
			methods += ","
		}
		methods += event.Method
	}
	return methods
}
