package browser_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
)

func TestManagedProfilePaths(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	if got := browser.ManagedProfileDir(stateDir); got != filepath.Join(stateDir, "browser", "headless-profile") {
		t.Fatalf("ManagedProfileDir() = %q", got)
	}
	if got := browser.ManagedMetadataPath(stateDir); got != filepath.Join(stateDir, "browser", "managed-browser.json") {
		t.Fatalf("ManagedMetadataPath() = %q", got)
	}
}

func TestManagedLaunchArgsUseSafeHeadlessFlags(t *testing.T) {
	args := browser.ManagedLaunchArgs("/usr/bin/google-chrome", "/tmp/profile")
	joined := strings.Join(args, " ")
	for _, want := range []string{"/usr/bin/google-chrome", "--headless", "--remote-debugging-port=0", "--user-data-dir=/tmp/profile", "--no-first-run", "--no-default-browser-check"} {
		if !containsArg(args, want) {
			t.Fatalf("ManagedLaunchArgs() = %v, missing %q", args, want)
		}
	}
	for _, disallowed := range []string{"--remote-allow-origins=*", "--password-store=basic", "--use-mock-keychain", "--no-sandbox"} {
		if strings.Contains(joined, disallowed) {
			t.Fatalf("ManagedLaunchArgs() contains disallowed flag %q: %v", disallowed, args)
		}
	}
}

func TestPrepareManagedProfileWritesOwnerOnlyMetadata(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	metadata, err := browser.PrepareManagedProfile(stateDir, now)
	if err != nil {
		t.Fatalf("PrepareManagedProfile returned error: %v", err)
	}
	if metadata.BrowserMode != "headless" || metadata.ProfileSeedStrategy != "managed" || metadata.UserDataDir != browser.ManagedProfileDir(stateDir) {
		t.Fatalf("metadata = %+v, want managed headless profile", metadata)
	}
	profileInfo, err := os.Stat(browser.ManagedProfileDir(stateDir))
	if err != nil {
		t.Fatalf("stat profile: %v", err)
	}
	if got := profileInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("profile permissions = %o, want 700", got)
	}
	metadataInfo, err := os.Stat(browser.ManagedMetadataPath(stateDir))
	if err != nil {
		t.Fatalf("stat metadata: %v", err)
	}
	if got := metadataInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("metadata permissions = %o, want 600", got)
	}

	loaded, ok, err := browser.LoadManagedMetadata(stateDir)
	if err != nil {
		t.Fatalf("LoadManagedMetadata returned error: %v", err)
	}
	if !ok || loaded.UserDataDir != metadata.UserDataDir || loaded.LastSeededAt != now.Format(time.RFC3339) {
		t.Fatalf("LoadManagedMetadata() = %+v, %v, want saved metadata", loaded, ok)
	}
}

func TestManagedMetadataRoundTripAndStatusRedaction(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           123,
		StartedAt:           "2026-05-21T12:00:00Z",
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		DebuggingPort:       "9222",
		ProfileSeedStrategy: "managed",
		LastSeededAt:        "2026-05-21T11:00:00Z",
		OwnedMarker:         "secret-token",
		ProcessStartTime:    "2026-05-21T12:00:00Z",
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	loaded, ok, err := browser.LoadManagedMetadata(stateDir)
	if err != nil {
		t.Fatalf("LoadManagedMetadata returned error: %v", err)
	}
	if !ok || loaded.OwnedMarker != "secret-token" || loaded.ProcessStartTime != metadata.ProcessStartTime || loaded.DebuggingPort != "9222" {
		t.Fatalf("LoadManagedMetadata() = %+v, %v, want full metadata round trip", loaded, ok)
	}

	status := browser.ManagedMetadataStatus(loaded)
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if strings.Contains(string(encoded), "secret-token") || strings.Contains(string(encoded), "process_start_time") {
		t.Fatalf("ManagedMetadataStatus leaked internal ownership fields: %s", string(encoded))
	}
	if status.BrowserMode != "headless" || status.UserDataDir != metadata.UserDataDir || status.DebuggingPort != "9222" {
		t.Fatalf("ManagedMetadataStatus() = %+v, want safe status fields", status)
	}
}

func TestStopOwnedManagedChromeRequiresOwnershipMetadata(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           123,
		StartedAt:           "2026-05-21T12:00:00Z",
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		DebuggingPort:       "9222",
		ProfileSeedStrategy: "managed",
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	called := false
	result, err := browser.StopOwnedManagedChrome(context.Background(), stateDir, func(pid int) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("StopOwnedManagedChrome returned error: %v", err)
	}
	if called || !result.Checked || !result.Skipped || result.Stopped || result.Reason == "" {
		t.Fatalf("StopOwnedManagedChrome = %+v, called=%v; want skipped without ownership metadata", result, called)
	}
}

func TestStopOwnedManagedChromeSignalsOwnedProcess(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           123,
		StartedAt:           "2026-05-21T12:00:00Z",
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		DebuggingPort:       "9222",
		ProfileSeedStrategy: "managed",
		OwnedMarker:         "owned-token",
		ProcessStartTime:    "2026-05-21T12:00:00Z",
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	var gotPID int
	result, err := browser.StopOwnedManagedChrome(context.Background(), stateDir, func(pid int) error {
		gotPID = pid
		return nil
	})
	if err != nil {
		t.Fatalf("StopOwnedManagedChrome returned error: %v", err)
	}
	if gotPID != 123 || !result.Checked || result.Skipped || !result.Stopped || result.Browser.DebuggingPort != "9222" {
		t.Fatalf("StopOwnedManagedChrome = %+v, pid=%d; want owned process stopped", result, gotPID)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(encoded), "owned-token") || strings.Contains(string(encoded), "process_start_time") {
		t.Fatalf("ManagedStopResult leaked internal ownership fields: %s", string(encoded))
	}
}

func TestLoadManagedMetadataMissing(t *testing.T) {
	metadata, ok, err := browser.LoadManagedMetadata(t.TempDir())
	if err != nil {
		t.Fatalf("LoadManagedMetadata returned error: %v", err)
	}
	if ok || metadata.UserDataDir != "" {
		t.Fatalf("LoadManagedMetadata() = %+v, %v, want missing", metadata, ok)
	}
}

func TestWaitManagedActivePort(t *testing.T) {
	userDataDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte("12345\n/devtools/browser/test\n"), 0o600)
	}()

	port, path, err := browser.WaitManagedActivePort(ctx, userDataDir)
	if err != nil {
		t.Fatalf("WaitManagedActivePort returned error: %v", err)
	}
	if port != "12345" || path != "/devtools/browser/test" {
		t.Fatalf("WaitManagedActivePort() = %q, %q, want active port", port, path)
	}
}

func TestWaitManagedActivePortTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := browser.WaitManagedActivePort(ctx, t.TempDir()); err == nil {
		t.Fatalf("WaitManagedActivePort returned nil error, want timeout")
	}
}

func TestWaitManagedActivePortRejectsInvalidFile(t *testing.T) {
	userDataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte("bad\nrelative\n"), 0o600); err != nil {
		t.Fatalf("write active port: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := browser.WaitManagedActivePort(ctx, userDataDir); err == nil {
		t.Fatalf("WaitManagedActivePort returned nil error, want invalid active port failure")
	}
}

func TestValidateLoopbackEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"ipv4", "ws://localhost/devtools/browser/test", false},
		{"localhost", "ws://localhost:9222/devtools/browser/test", false},
		{"ipv6", "ws://[::1]:9222/devtools/browser/test", false},
		{"any", "ws://0.0.0.0:9222/devtools/browser/test", true},
		{"lan", "ws://192.168.1.10:9222/devtools/browser/test", true},
		{"missing host", "ws:///devtools/browser/test", true},
		{"bad scheme", "http://localhost/devtools/devtools/browser/test", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := browser.ValidateLoopbackEndpoint(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateLoopbackEndpoint(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestDiscoverChromeExplicit(t *testing.T) {
	got, err := browser.DiscoverChrome("/custom/chrome")
	if err != nil {
		t.Fatalf("DiscoverChrome explicit returned error: %v", err)
	}
	if got != "/custom/chrome" {
		t.Fatalf("DiscoverChrome explicit = %q, want /custom/chrome", got)
	}
}

func TestDiscoverChromeMissing(t *testing.T) {
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", "")
	t.Cleanup(func() { _ = os.Setenv("PATH", oldPath) })
	if _, err := browser.DiscoverChrome(""); err == nil {
		t.Fatalf("DiscoverChrome returned nil error, want missing chrome")
	}
}

func containsArg(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

var _ = net.IP{}.IsLoopback
