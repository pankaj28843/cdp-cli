package daemon

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type leaseTestClient struct {
	mu       sync.Mutex
	targets  map[string]bool
	failures map[string]error
	calls    []string
}

func (c *leaseTestClient) Call(_ context.Context, method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, method)
	if method != "Target.closeTarget" && method != "Target.getTargetInfo" {
		return nil
	}
	targetID := ""
	if raw, ok := params.(map[string]any); ok {
		targetID, _ = raw["targetId"].(string)
	}
	if err := c.failures[targetID]; err != nil {
		return err
	}
	if !c.targets[targetID] {
		return errors.New("target not found")
	}
	if method == "Target.closeTarget" {
		delete(c.targets, targetID)
		setBoolField(result, "Success", true)
	}
	return nil
}

func (c *leaseTestClient) CallSession(ctx context.Context, _ string, method string, params any, result any) error {
	return c.Call(ctx, method, params, result)
}

func (c *leaseTestClient) hasTarget(targetID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.targets[targetID]
}

func setBoolField(result any, fieldName string, value bool) {
	if result == nil {
		return
	}
	reflected := reflect.ValueOf(result)
	if reflected.Kind() != reflect.Pointer || reflected.IsNil() {
		return
	}
	field := reflected.Elem().FieldByName(fieldName)
	if field.IsValid() && field.CanSet() && field.Kind() == reflect.Bool {
		field.SetBool(value)
	}
}

func TestLeaseManagerPersistsAndReapsExpiredTargets(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	stateDir := t.TempDir()
	manager, err := newLeaseManager(context.Background(), stateDir, "headless", func() time.Time { return now })
	if err != nil {
		t.Fatalf("newLeaseManager: %v", err)
	}
	info, err := manager.Begin(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := manager.RegisterTarget(context.Background(), info.LeaseID, LeaseTarget{TargetID: "owned-page", Disposable: true}); err != nil {
		t.Fatalf("RegisterTarget: %v", err)
	}
	client := &leaseTestClient{targets: map[string]bool{"owned-page": true, "user-page": true}, failures: map[string]error{}}
	now = now.Add(61 * time.Second)
	result, err := manager.ReconcileExpired(context.Background(), client)
	if err != nil {
		t.Fatalf("ReconcileExpired: %v", err)
	}
	if result.ExpiredLeaseCount != 1 || result.ClosedTargetCount != 1 || len(result.PendingTargetIDs) != 0 {
		t.Fatalf("reconcile result = %+v", result)
	}
	if client.hasTarget("owned-page") || !client.hasTarget("user-page") {
		t.Fatalf("targets after reconcile = owned=%v user=%v", client.hasTarget("owned-page"), client.hasTarget("user-page"))
	}
	reloaded, err := newLeaseManager(context.Background(), stateDir, "headless", func() time.Time { return now })
	if err != nil {
		t.Fatalf("reload manager: %v", err)
	}
	if len(reloaded.leases) != 0 {
		t.Fatalf("reloaded leases = %+v, want empty after successful cleanup", reloaded.leases)
	}
}

func TestLeaseManagerRetainsFailedCleanupForRetry(t *testing.T) {
	stateDir := t.TempDir()
	manager, err := newLeaseManager(context.Background(), stateDir, "headed", time.Now)
	if err != nil {
		t.Fatalf("newLeaseManager: %v", err)
	}
	info, err := manager.Begin(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := manager.RegisterTarget(context.Background(), info.LeaseID, LeaseTarget{TargetID: "owned-page", Disposable: true}); err != nil {
		t.Fatalf("RegisterTarget: %v", err)
	}
	client := &leaseTestClient{
		targets:  map[string]bool{"owned-page": true},
		failures: map[string]error{"owned-page": errors.New("synthetic close failure")},
	}
	if _, err := manager.End(context.Background(), client, info.LeaseID); err == nil {
		t.Fatal("End returned nil after synthetic cleanup failure")
	}
	if got := manager.leases[info.LeaseID].State; got != leaseStateCleanupPending {
		t.Fatalf("lease state = %q, want %q", got, leaseStateCleanupPending)
	}
	client.failures = map[string]error{}
	result, err := manager.ReconcileExpired(context.Background(), client)
	if err != nil {
		t.Fatalf("retry ReconcileExpired: %v", err)
	}
	if result.ClosedTargetCount != 1 || len(result.PendingTargetIDs) != 0 || client.hasTarget("owned-page") {
		t.Fatalf("retry result = %+v; target remains=%v", result, client.hasTarget("owned-page"))
	}
}

func TestLeaseManagerEndClosesOnlyRegisteredTargets(t *testing.T) {
	manager, err := newLeaseManager(context.Background(), t.TempDir(), "headed", time.Now)
	if err != nil {
		t.Fatalf("newLeaseManager: %v", err)
	}
	info, err := manager.Begin(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := manager.RegisterTarget(context.Background(), info.LeaseID, LeaseTarget{TargetID: "owned-page", Disposable: true}); err != nil {
		t.Fatalf("RegisterTarget: %v", err)
	}
	if err := manager.RegisterTarget(context.Background(), info.LeaseID, LeaseTarget{TargetID: "persistent-page"}); err != nil {
		t.Fatalf("RegisterTarget persistent: %v", err)
	}
	client := &leaseTestClient{targets: map[string]bool{"owned-page": true, "persistent-page": true, "user-page": true}, failures: map[string]error{}}
	result, err := manager.End(context.Background(), client, info.LeaseID)
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if result.ClosedTargetCount != 1 || client.hasTarget("owned-page") || !client.hasTarget("persistent-page") || !client.hasTarget("user-page") {
		t.Fatalf("end result = %+v; targets owned=%v persistent=%v user=%v", result, client.hasTarget("owned-page"), client.hasTarget("persistent-page"), client.hasTarget("user-page"))
	}
}
