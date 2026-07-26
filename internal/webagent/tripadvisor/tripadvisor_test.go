package tripadvisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestSessionStoreIsOwnerOnlyAndHonestAboutAnonymousMode(
	t *testing.T,
) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	now := time.Date(2026, 7, 26, 7, 0, 0, 0, time.UTC)
	missing := store.Status(
		context.Background(),
		now,
		DefaultAuthTTL,
	)
	if missing.Ready || missing.State != "missing" {
		t.Fatalf("missing status = %+v", missing)
	}
	state := SessionState{
		SchemaVersion: SessionStateSchemaVersion,
		CapturedAt:    now.Format(time.RFC3339Nano),
		PanelReady:    true,
		ComposerReady: true,
		HistoryReady:  true,
		SessionMode:   "anonymous",
		Source:        "headed-cdp-rendered-session",
	}
	if err := store.SaveSession(context.Background(), state); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	status := store.Status(
		context.Background(),
		now.Add(30*time.Minute),
		DefaultAuthTTL,
	)
	if !status.Ready ||
		status.State != "ready" ||
		status.SessionMode != "anonymous" {
		t.Fatalf("ready status = %+v", status)
	}
	path := filepath.Join(
		root,
		filepath.FromSlash(RelativeSessionPath),
	)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat session state: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session mode = %o, want 0600", info.Mode().Perm())
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat session directory: %v", err)
	}
	if parent.Mode().Perm() != 0o700 {
		t.Fatalf(
			"session directory mode = %o, want 0700",
			parent.Mode().Perm(),
		)
	}
	expired := store.Status(
		context.Background(),
		now.Add(2*time.Hour),
		DefaultAuthTTL,
	)
	if expired.Ready ||
		expired.State != "expired" ||
		!expired.Stale {
		t.Fatalf("expired status = %+v", expired)
	}
}

func TestSessionStateRejectsFalseReadinessAndInventedMode(t *testing.T) {
	base := SessionState{
		SchemaVersion: SessionStateSchemaVersion,
		CapturedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		PanelReady:    true,
		ComposerReady: true,
		HistoryReady:  true,
		SessionMode:   "anonymous",
		Source:        "headed-cdp-rendered-session",
	}
	notReady := base
	notReady.ComposerReady = false
	if err := notReady.Validate(); err == nil {
		t.Fatal("session accepted missing composer readiness")
	}
	invented := base
	invented.SessionMode = "authenticated_maybe"
	if err := invented.Validate(); err == nil {
		t.Fatal("session accepted invented authentication mode")
	}
}

func TestEmptyNextCommandsRemainAJSONArray(t *testing.T) {
	result := operationSuccess(
		"wa-tripadvisor-test",
		"test",
		webagent.OperationConversationsList,
		webagent.StateReady,
		webagent.StageMetadata,
		"local_empty_limit",
		nil,
		webagent.CleanupEvidence{
			State: webagent.CleanupNotRequired,
		},
		nil,
		nil,
		map[string]any{"schema_version": ConversationListSchemaVersion},
		[]string{},
	)
	if result.NextCommands == nil {
		t.Fatal("next_commands is nil, want an empty JSON array")
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
}

func TestConversationIdentityAndPromptBudgetFailClosed(t *testing.T) {
	valid := "019f9d4b-2367-7db2-9bad-d0a63b67ac34"
	if !validConversationID(valid) {
		t.Fatalf("valid conversation id %q was rejected", valid)
	}
	for _, candidate := range []string{
		"",
		strings.ToUpper(valid),
		"019f9d4b-2367-7db2-9bad-d0a63b67ac34?canvas=1",
		"not-a-conversation",
	} {
		if validConversationID(candidate) {
			t.Fatalf("invalid conversation id %q was accepted", candidate)
		}
	}

	result := Ask(
		context.Background(),
		AskConfig{},
		strings.Repeat("x", MaxPromptCharacters+1),
	)
	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "tripadvisor_prompt_too_long" ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchNotPerformed ||
		!result.Action.RetrySafe {
		t.Fatalf("over-budget ask = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("over-budget result validation: %v", err)
	}
}

func TestConversationListAcceptsRenderedRowsWithoutLegacyMarkers(
	t *testing.T,
) {
	if conversationListReady(listObservation{}) {
		t.Fatal("empty observation was treated as rendered history")
	}
	if !conversationListReady(listObservation{DrawerReady: true}) {
		t.Fatal("legacy drawer markers were not accepted")
	}
	if !conversationListReady(listObservation{
		RenderedTitles: 6,
		OmittedNoID:    6,
	}) {
		t.Fatal("visible title-only rows were not accepted")
	}
}
