package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/pankaj28843/cdp-cli/internal/state"
)

func TestDaemonStartErrorKeepsManagedHeadlessNoninteractive(t *testing.T) {
	t.Setenv("CDP_BROWSER_MODE", "headless")
	a := &app{opts: options{profile: config.DefaultProfile}}
	a.root = a.newRoot()

	err := a.daemonStartError("start_keepalive", "synthetic failure", map[string]any{"browser_endpoint_seen": true})
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("daemonStartError() = %T, want CommandError", err)
	}
	if commandErr.Code == "permission_pending" || commandErr.Class == "permission" || commandErr.ExitCode == ExitPermission {
		t.Fatalf("managed headless error = %+v, want non-permission classification", commandErr)
	}
	for _, command := range commandErr.RemediationCommands {
		if command == "open chrome://inspect/#remote-debugging" {
			t.Fatalf("managed headless remediation contains human approval command: %+v", commandErr.RemediationCommands)
		}
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok || data["human_required"] != false || data["agent_should_stop"] != false {
		t.Fatalf("managed headless error data = %+v, want explicit noninteractive booleans", commandErr.Data)
	}
}

func TestStartManagedChromeWithRetriesIsBounded(t *testing.T) {
	stateDir := t.TempDir()
	status := keepaliveChromeStatus{MaxAttempts: 3}
	calls := 0
	launch := func(context.Context, browser.ManagedOptions) (browser.ManagedLaunch, error) {
		calls++
		if calls < 3 {
			return browser.ManagedLaunch{}, errors.New("synthetic launch failure")
		}
		return browser.ManagedLaunch{Endpoint: "ws://managed-headless.test/devtools/browser/test"}, nil
	}

	got, err := startManagedChromeWithRetries(context.Background(), stateDir, browser.ManagedOptions{StateDir: stateDir}, &status, launch, 3, time.Second)
	if err != nil {
		t.Fatalf("startManagedChromeWithRetries returned error: %v", err)
	}
	if calls != 3 || status.Attempts != 3 || len(status.AttemptErrors) != 2 || got.Endpoint == "" {
		t.Fatalf("retry result = launch %+v status %+v calls=%d, want bounded third-attempt success", got, status, calls)
	}
}

func TestClearManagedHeadlessRecoveryStateIsModeScoped(t *testing.T) {
	stateDir := t.TempDir()
	store := state.Store{Dir: stateDir}
	file := state.File{
		Selected: "headless",
		Connections: []state.Connection{
			{Name: "headless", Mode: "browser_url", BrowserMode: "headless", BrowserURL: "http://stale-headless.test"},
			{Name: "project-headless", Mode: "browser_url", BrowserMode: "headless", BrowserURL: "http://project-headless.test", Project: t.TempDir()},
			{Name: "default", Mode: "auto_connect", BrowserMode: "headed", AutoConnect: true},
		},
	}
	if err := store.Save(context.Background(), file); err != nil {
		t.Fatalf("save connection state: %v", err)
	}
	lockDir := filepath.Join(stateDir, "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("create lock dir: %v", err)
	}
	staleLock := filepath.Join(lockDir, "daemon-keepalive-headless-browser_url-headless.lock")
	if err := os.WriteFile(staleLock, []byte(`{"name":"daemon-keepalive-headless-browser_url-headless","pid":999999,"started_at":"2020-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	a := &app{}
	cleanup, err := a.clearManagedHeadlessRecoveryState(context.Background(), stateDir, time.Minute)
	if err != nil {
		t.Fatalf("clearManagedHeadlessRecoveryState returned error: %v", err)
	}
	if len(cleanup.ConnectionsRemoved) != 1 || cleanup.ConnectionsRemoved[0] != "headless" || len(cleanup.StaleLocks.Removed) != 1 || !cleanup.RuntimeArtifactsCleared {
		t.Fatalf("recovery cleanup = %+v, want one mode-scoped connection and stale lock removed", cleanup)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load connection state: %v", err)
	}
	if _, ok := state.ConnectionByName(got, "headless"); ok {
		t.Fatalf("stale managed headless connection remains: %+v", got.Connections)
	}
	if _, ok := state.ConnectionByName(got, "project-headless"); !ok {
		t.Fatalf("project override was removed: %+v", got.Connections)
	}
	if _, ok := state.ConnectionByName(got, "default"); !ok {
		t.Fatalf("headed connection was removed: %+v", got.Connections)
	}
}

func TestRemoveStaleLocksKeepsLiveOwner(t *testing.T) {
	stateDir := t.TempDir()
	lockDir := filepath.Join(stateDir, "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("create lock dir: %v", err)
	}
	path := filepath.Join(lockDir, "daemon-health-check-headless.lock")
	body := []byte(`{"name":"daemon-health-check-headless","pid":` + fmt.Sprint(os.Getpid()) + `,"started_at":"2020-01-01T00:00:00Z"}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write active lock: %v", err)
	}
	cleanup, err := daemon.RemoveStaleLocks(context.Background(), stateDir, time.Nanosecond, "daemon-health-check-headless")
	if err != nil {
		t.Fatalf("RemoveStaleLocks returned error: %v", err)
	}
	if len(cleanup.Removed) != 0 {
		t.Fatalf("RemoveStaleLocks removed live owner: %+v", cleanup.Removed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("live-owner lock missing after cleanup: %v", err)
	}
}
