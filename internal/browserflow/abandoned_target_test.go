package browserflow

import (
	"context"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"testing"
)

type lifecycleBrowser struct {
	*fakeBrowserClient
	persistent, disposable []string
}

func (c *lifecycleBrowser) MarkTargetPersistent(_ context.Context, id string) error {
	c.persistent = append(c.persistent, id)
	return nil
}
func (c *lifecycleBrowser) MarkTargetDisposable(_ context.Context, id string) error {
	c.disposable = append(c.disposable, id)
	return nil
}

func TestPendingUnknownTargetPreservedUntilExactJournalRecovery(t *testing.T) {
	client := &lifecycleBrowser{fakeBrowserClient: newFakeBrowserClient("user-page")}
	journal, err := NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{Client: client, Journal: journal})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Acquire(context.Background(), AcquireRequest{RunID: "abandoned-unknown", Provider: "chatgpt", Operation: "ask", InitialURL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.persistent) != 1 || client.persistent[0] != lease.TargetID() || len(client.disposable) != 0 {
		t.Fatalf("pending target was disposable: persistent=%v disposable=%v", client.persistent, client.disposable)
	}
	if err := lease.MarkPrepared(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = lease.Dispatch(context.Background(), DispatchFunc(func(context.Context, *cdp.PageSession) (DispatchOutcome, error) {
		return DispatchOutcome{Dispatch: DispatchUnknown, RawInputAttempted: true}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	record, err := journal.Load(context.Background(), "abandoned-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if !record.PendingPersisted || record.RawInputCount != 1 || record.Dispatch != DispatchUnknown || !client.hasTarget(lease.TargetID()) {
		t.Fatalf("unknown action not preserved: %+v", record)
	}
	cleanup, err := engine.Recover(context.Background(), record.RunID)
	if err != nil || !cleanup.TargetGone {
		t.Fatalf("recovery=%+v err=%v", cleanup, err)
	}
	if !client.hasTarget("user-page") || client.hasTarget(lease.TargetID()) {
		t.Fatal("exact recovery touched the wrong target")
	}
}
