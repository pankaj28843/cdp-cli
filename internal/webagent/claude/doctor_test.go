package claude

import (
	"context"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestDoctorIsBrowserFreeAndReportsAuthReadiness(t *testing.T) {
	now := time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	missing := Doctor(context.Background(), store, now, time.Hour, "test-commit")
	if missing.OK ||
		missing.Error == nil ||
		missing.Error.Code != "claude_auth_missing" ||
		missing.Evidence.BrowserMode != "none" ||
		missing.Evidence.ReadMode != "owner_only_local_state" ||
		missing.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("missing doctor = %+v", missing)
	}
	if err := missing.Validate(); err != nil {
		t.Fatalf("missing doctor validation: %v", err)
	}

	if err := store.Save(context.Background(), validAuthTemplate(now)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ready := Doctor(context.Background(), store, now.Add(time.Minute), time.Hour, "test-commit")
	if !ready.OK ||
		ready.State != webagent.StateReady ||
		ready.Operation != webagent.OperationDoctor ||
		ready.Evidence.BrowserMode != "none" ||
		ready.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("ready doctor = %+v", ready)
	}
	data, ok := ready.Data.(DoctorData)
	if !ok || !data.Auth.Ready || data.Browser.Probed || data.Browser.Mode != "headed" {
		t.Fatalf("ready doctor data = %#v", ready.Data)
	}
	if err := ready.Validate(); err != nil {
		t.Fatalf("ready doctor validation: %v", err)
	}
}
