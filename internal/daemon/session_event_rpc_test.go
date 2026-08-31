package daemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestHandleRPCRoutesSessionEventOperations(t *testing.T) {
	client := &sessionEventRPCClient{}
	drain := callSessionEventRPC(t, client, RPCRequest{Method: RPCMethodDrainSessionEvents, SessionID: "session-a"})
	if !drain.OK || client.drainedSession != "session-a" {
		t.Fatalf("session drain response=%+v session=%q", drain, client.drainedSession)
	}
	var events []cdp.Event
	if err := json.Unmarshal(drain.Result, &events); err != nil || len(events) != 1 || events[0].SessionID != "session-a" {
		t.Fatalf("session drain result=%s events=%+v err=%v", drain.Result, events, err)
	}

	read := callSessionEventRPC(t, client, RPCRequest{Method: RPCMethodReadSessionEvent, SessionID: "session-b"})
	if !read.OK || client.readSession != "session-b" {
		t.Fatalf("session read response=%+v session=%q", read, client.readSession)
	}
	var event cdp.Event
	if err := json.Unmarshal(read.Result, &event); err != nil || event.SessionID != "session-b" {
		t.Fatalf("session read result=%s event=%+v err=%v", read.Result, event, err)
	}
}

func TestHandleRPCRejectsEmptySessionEventRoute(t *testing.T) {
	response := callSessionEventRPC(t, &sessionEventRPCClient{}, RPCRequest{Method: RPCMethodReadSessionEvent})
	if response.OK || response.ErrorEnvelope == nil || response.ErrorEnvelope.Code != "session_id_required" {
		t.Fatalf("empty session response = %+v, want session_id_required", response)
	}
}

type sessionEventRPCClient struct {
	drainedSession string
	readSession    string
}

func (c *sessionEventRPCClient) Call(context.Context, string, any, any) error { return nil }
func (c *sessionEventRPCClient) CallSession(context.Context, string, string, any, any) error {
	return nil
}
func (c *sessionEventRPCClient) Endpoint() string         { return "ws://synthetic.invalid" }
func (c *sessionEventRPCClient) DrainEvents() []cdp.Event { return nil }
func (c *sessionEventRPCClient) ReadEvent(context.Context) (cdp.Event, error) {
	return cdp.Event{}, nil
}
func (c *sessionEventRPCClient) DrainSessionEvents(sessionID string) []cdp.Event {
	c.drainedSession = sessionID
	return []cdp.Event{{SessionID: sessionID, Method: "Runtime.ready"}}
}
func (c *sessionEventRPCClient) ReadSessionEvent(_ context.Context, sessionID string) (cdp.Event, error) {
	c.readSession = sessionID
	return cdp.Event{SessionID: sessionID, Method: "Runtime.event"}, nil
}

func callSessionEventRPC(t *testing.T, client *sessionEventRPCClient, request RPCRequest) RPCResponse {
	t.Helper()
	server, peer := net.Pipe()
	defer peer.Close()
	go handleRPC(context.Background(), server, client, holdOptions{}, nil, nil)
	if err := json.NewEncoder(peer).Encode(request); err != nil {
		t.Fatalf("encode session event request: %v", err)
	}
	var response RPCResponse
	if err := json.NewDecoder(peer).Decode(&response); err != nil {
		t.Fatalf("decode session event response: %v", err)
	}
	return response
}
