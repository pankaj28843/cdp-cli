package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestHandleRPCDoesNotTouchLeaseAfterRequestCancellation(t *testing.T) {
	now := time.Date(2026, 8, 30, 17, 50, 0, 0, time.UTC)
	manager, err := newLeaseManager(context.Background(), t.TempDir(), "headless", func() time.Time { return now })
	if err != nil {
		t.Fatalf("newLeaseManager returned error: %v", err)
	}
	info, err := manager.Begin(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	before := manager.leases[info.LeaseID]
	now = now.Add(time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := callLeaseRPC(t, ctx, &rpcLeaseClient{}, manager, RPCRequest{
		Method:  "Browser.getVersion",
		OwnerID: info.LeaseID,
	})
	if response.OK || response.ErrorEnvelope == nil || response.ErrorEnvelope.Code != "canceled" {
		t.Fatalf("canceled lease touch response = %+v, want cancellation", response)
	}
	if got := manager.leases[info.LeaseID]; got.ExpiresAt != before.ExpiresAt {
		t.Fatalf("canceled lease touch expiry = %q, want unchanged %q", got.ExpiresAt, before.ExpiresAt)
	}
}

func TestHandleRPCDoesNotRegisterTargetAfterRequestCancellation(t *testing.T) {
	manager := newRPCLeaseManager(t)
	started := make(chan struct{})
	release := make(chan struct{})
	client := &rpcLeaseClient{
		callSession: func(_ context.Context, _ string, method string, _ any, result any) error {
			if method != "Target.createTarget" {
				return nil
			}
			close(started)
			<-release
			*result.(*json.RawMessage) = json.RawMessage(`{"targetId":"created-page"}`)
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, peer := net.Pipe()
	go func() {
		handleRPC(ctx, server, client, holdOptions{}, manager, nil)
	}()
	if err := json.NewEncoder(peer).Encode(RPCRequest{Method: "Target.createTarget", OwnerID: firstRPCLeaseID(manager)}); err != nil {
		t.Fatalf("write create request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Target.createTarget did not reach the fake browser")
	}
	cancel()
	close(release)
	var response RPCResponse
	if err := json.NewDecoder(peer).Decode(&response); err != nil {
		t.Fatalf("read create response: %v", err)
	}
	_ = peer.Close()
	if response.OK || response.ErrorEnvelope == nil || response.ErrorEnvelope.Code != "canceled" {
		t.Fatalf("canceled target registration response = %+v, want cancellation", response)
	}
	if managerHasLeaseTarget(manager, firstRPCLeaseID(manager), "created-page") {
		t.Fatal("canceled target registration persisted an owned target")
	}
	if client.closeCalls() != 1 {
		t.Fatalf("rollback close calls = %d, want one", client.closeCalls())
	}
}

func TestHandleRPCDoesNotUnregisterTargetAfterRequestCancellation(t *testing.T) {
	manager := newRPCLeaseManager(t)
	leaseID := firstRPCLeaseID(manager)
	if err := manager.RegisterTarget(context.Background(), leaseID, LeaseTarget{TargetID: "existing-page", Disposable: true}); err != nil {
		t.Fatalf("RegisterTarget returned error: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	client := &rpcLeaseClient{
		callSession: func(_ context.Context, _ string, method string, _ any, result any) error {
			if method != "Target.closeTarget" {
				return nil
			}
			close(started)
			<-release
			*result.(*json.RawMessage) = json.RawMessage(`{"success":true}`)
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, peer := net.Pipe()
	go func() {
		handleRPC(ctx, server, client, holdOptions{}, manager, nil)
	}()
	if err := json.NewEncoder(peer).Encode(RPCRequest{
		Method:  "Target.closeTarget",
		OwnerID: leaseID,
		Params:  json.RawMessage(`{"targetId":"existing-page"}`),
	}); err != nil {
		t.Fatalf("write close request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Target.closeTarget did not reach the fake browser")
	}
	cancel()
	close(release)
	var response RPCResponse
	if err := json.NewDecoder(peer).Decode(&response); err != nil {
		t.Fatalf("read close response: %v", err)
	}
	_ = peer.Close()
	if !response.OK {
		t.Fatalf("canceled target close response = %+v, want browser result preserved", response)
	}
	if !managerHasLeaseTarget(manager, leaseID, "existing-page") {
		t.Fatal("canceled target unregistration removed the owned target")
	}
}

func TestHandleRPCKeepsTargetOwnedUntilCleanupConfirmsItGone(t *testing.T) {
	manager := newRPCLeaseManager(t)
	leaseID := firstRPCLeaseID(manager)
	if err := manager.RegisterTarget(context.Background(), leaseID, LeaseTarget{TargetID: "existing-page", Disposable: true}); err != nil {
		t.Fatalf("RegisterTarget returned error: %v", err)
	}
	client := &rpcLeaseClient{
		call: func(_ context.Context, method string, _ any, result any) error {
			if method == "Target.getTargetInfo" {
				setNestedStringField(result, "TargetInfo", "TargetID", "existing-page")
			}
			return nil
		},
		callSession: func(_ context.Context, _ string, method string, _ any, result any) error {
			if method == "Target.closeTarget" {
				*result.(*json.RawMessage) = json.RawMessage(`{"success":true}`)
			}
			return nil
		},
	}
	response := callLeaseRPC(t, context.Background(), client, manager, RPCRequest{
		Method:  "Target.closeTarget",
		OwnerID: leaseID,
		Params:  json.RawMessage(`{"targetId":"existing-page"}`),
	})
	if !response.OK {
		t.Fatalf("target close response = %+v, want browser result preserved", response)
	}
	if !managerHasLeaseTarget(manager, leaseID, "existing-page") {
		t.Fatal("accepted close released ownership before target-gone confirmation")
	}
}

func TestHandleRPCExplicitEndRetainsBoundedCleanupAfterRequestCancellation(t *testing.T) {
	manager := newRPCLeaseManager(t)
	leaseID := firstRPCLeaseID(manager)
	if err := manager.RegisterTarget(context.Background(), leaseID, LeaseTarget{TargetID: "owned-page", Disposable: true}); err != nil {
		t.Fatalf("RegisterTarget returned error: %v", err)
	}
	client := &rpcLeaseClient{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := callLeaseRPC(t, ctx, client, manager, RPCRequest{
		Method:  RPCMethodEndInvocationLease,
		OwnerID: leaseID,
	})
	if !response.OK {
		t.Fatalf("canceled explicit end response = %+v, want bounded cleanup result", response)
	}
	if client.closeCalls() != 1 {
		t.Fatalf("explicit end close calls = %d, want one", client.closeCalls())
	}
	if _, ok := manager.leases[leaseID]; ok {
		t.Fatal("explicit end left a cleaned lease in memory")
	}
}

type rpcLeaseClient struct {
	mu          sync.Mutex
	call        func(context.Context, string, any, any) error
	callSession func(context.Context, string, string, any, any) error
	closeCount  int
}

func (c *rpcLeaseClient) Call(ctx context.Context, method string, params any, result any) error {
	if c.call != nil {
		return c.call(ctx, method, params, result)
	}
	if method == "Target.closeTarget" {
		c.mu.Lock()
		c.closeCount++
		c.mu.Unlock()
		setBoolField(result, "Success", true)
	}
	return nil
}

func (c *rpcLeaseClient) CallSession(ctx context.Context, sessionID, method string, params any, result any) error {
	if c.callSession != nil {
		return c.callSession(ctx, sessionID, method, params, result)
	}
	return nil
}

func (c *rpcLeaseClient) Endpoint() string { return "ws://synthetic.invalid/devtools/browser/test" }

func (c *rpcLeaseClient) DrainEvents() []cdp.Event { return nil }

func (c *rpcLeaseClient) ReadEvent(context.Context) (cdp.Event, error) {
	return cdp.Event{}, errors.New("synthetic event unavailable")
}

func (c *rpcLeaseClient) DrainSessionEvents(string) []cdp.Event { return nil }

func (c *rpcLeaseClient) ReadSessionEvent(context.Context, string) (cdp.Event, error) {
	return cdp.Event{}, errors.New("synthetic session event unavailable")
}

func (c *rpcLeaseClient) closeCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCount
}

func callLeaseRPC(t *testing.T, ctx context.Context, client *rpcLeaseClient, manager *LeaseManager, request RPCRequest) RPCResponse {
	t.Helper()
	server, peer := net.Pipe()
	defer peer.Close()
	go handleRPC(ctx, server, client, holdOptions{}, manager, nil)
	if err := json.NewEncoder(peer).Encode(request); err != nil {
		t.Fatalf("write RPC request: %v", err)
	}
	var response RPCResponse
	if err := json.NewDecoder(peer).Decode(&response); err != nil {
		t.Fatalf("read RPC response: %v", err)
	}
	return response
}

func newRPCLeaseManager(t *testing.T) *LeaseManager {
	t.Helper()
	manager, err := newLeaseManager(context.Background(), t.TempDir(), "headless", func() time.Time {
		return time.Date(2026, 8, 30, 17, 50, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("newLeaseManager returned error: %v", err)
	}
	if _, err := manager.Begin(context.Background(), time.Minute); err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	return manager
}

func firstRPCLeaseID(manager *LeaseManager) string {
	for leaseID := range manager.leases {
		return leaseID
	}
	return ""
}

func managerHasLeaseTarget(manager *LeaseManager, leaseID, targetID string) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, target := range manager.leases[leaseID].Targets {
		if target.TargetID == targetID {
			return true
		}
	}
	return false
}
