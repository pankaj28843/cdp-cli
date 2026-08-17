package browser_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
)

func TestManagedProfileSeedPolicyDefaultsToCleanManagedProfile(t *testing.T) {
	if got := browser.NormalizeProfileSeedStrategy(""); got != browser.ProfileSeedStrategyManaged {
		t.Fatalf("NormalizeProfileSeedStrategy(\"\") = %q, want managed", got)
	}
	if !browser.SupportedProfileSeedStrategy(browser.ProfileSeedStrategyManaged) {
		t.Fatal("managed profile seed strategy is not supported")
	}
	if !browser.SupportedProfileSeedStrategy(browser.ProfileSeedStrategyCopyDefault) {
		t.Fatal("copy-default profile seed strategy is not supported")
	}
}

func TestManagedProfilePaths(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	if got := browser.ManagedProfileDir(stateDir); got != filepath.Join(stateDir, "browser", "headless-profile") {
		t.Fatalf("ManagedProfileDir() = %q", got)
	}
	if got := browser.ManagedMetadataPath(stateDir); got != filepath.Join(stateDir, "browser", "managed-browser.json") {
		t.Fatalf("ManagedMetadataPath() = %q", got)
	}
}

func TestReconcileManagedProcessesCompactsBoundedTerminalTail(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	metadata := browser.ManagedMetadata{
		BrowserMode:      "headless",
		ChromePID:        900,
		UserDataDir:      profileDir,
		DebuggingPort:    "9222",
		StartedAt:        now.Add(-time.Minute).Format(time.RFC3339),
		ProcessStartTime: now.Add(-time.Minute).Format(time.RFC3339),
		OwnedMarker:      "current-owned-marker",
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata: %v", err)
	}
	records := make([]browser.ManagedProcessRecord, 0, 14)
	for i := 0; i < 12; i++ {
		when := now.Add(-time.Duration(i+1) * time.Minute).Format(time.RFC3339)
		records = append(records, browser.ManagedProcessRecord{
			PID:              100 + i,
			BrowserMode:      "headless",
			UserDataDir:      profileDir,
			ProcessStartTime: when,
			OwnershipMarker:  "owned-" + when,
			State:            "stopped",
			ExitedAt:         when,
			CleanupAt:        when,
			CleanupReason:    fmt.Sprintf("historical stop %02d", i),
		})
	}
	records = append(records, browser.ManagedProcessRecord{PID: 777, BrowserMode: "headless", UserDataDir: profileDir, State: "unknown_fixture"})
	if err := browser.SaveManagedProcessRegistry(stateDir, browser.ManagedProcessRegistry{Version: 1, BrowserMode: "headless", Records: records}); err != nil {
		t.Fatalf("SaveManagedProcessRegistry: %v", err)
	}

	result, err := browser.ReconcileManagedProcesses(context.Background(), stateDir, browser.ManagedProcessReconcileOptions{
		ActivePID: 900,
		Now:       func() time.Time { return now },
		ProcessLister: func(context.Context, string) ([]int, error) {
			return []int{900}, nil
		},
	})
	if err != nil {
		t.Fatalf("ReconcileManagedProcesses: %v", err)
	}
	if result.CompactedCount != 4 || result.HistoricalProcesses.Retained != 8 || result.HistoricalProcesses.LiveProbesAttempted != 0 {
		t.Fatalf("history summary = %+v compacted=%d, want retained=8 compacted=4 and no historical live probes", result.HistoricalProcesses, result.CompactedCount)
	}
	if result.HistoricalProcesses.LastFailureSummary != "historical stop 00" {
		t.Fatalf("last failure summary = %q, want newest terminal failure", result.HistoricalProcesses.LastFailureSummary)
	}
	if result.HistoricalProcesses.SkipReasons["missing_ownership_identity"] != 1 {
		t.Fatalf("skip reasons = %+v, want malformed unknown record preserved", result.HistoricalProcesses.SkipReasons)
	}
	second, err := browser.ReconcileManagedProcesses(context.Background(), stateDir, browser.ManagedProcessReconcileOptions{
		ActivePID: 900,
		Now:       func() time.Time { return now },
		ProcessLister: func(context.Context, string) ([]int, error) {
			return []int{900}, nil
		},
	})
	if err != nil || second.CompactedCount != 0 {
		t.Fatalf("second reconcile = %+v err=%v, want idempotent compaction", second, err)
	}
}

func TestReconcileManagedProcessesDoesNotProtectStoppedMetadataGeneration(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	metadata := browser.ManagedMetadata{
		BrowserMode:      "headless",
		ChromePID:        509,
		UserDataDir:      profileDir,
		ProcessStartTime: now.Add(-time.Minute).Format(time.RFC3339),
		OwnedMarker:      "stopped-current-metadata",
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatal(err)
	}
	records := make([]browser.ManagedProcessRecord, 0, 10)
	for i := 0; i < 10; i++ {
		when := now.Add(-time.Duration(10-i) * time.Minute).Format(time.RFC3339)
		marker := fmt.Sprintf("stopped-%d", i)
		if i == 9 {
			marker = metadata.OwnedMarker
			when = metadata.ProcessStartTime
		}
		records = append(records, browser.ManagedProcessRecord{PID: 500 + i, BrowserMode: "headless", UserDataDir: profileDir, ProcessStartTime: when, OwnershipMarker: marker, State: "stopped", ExitedAt: when, CleanupAt: when})
	}
	if err := browser.SaveManagedProcessRegistry(stateDir, browser.ManagedProcessRegistry{Version: 1, BrowserMode: "headless", Records: records}); err != nil {
		t.Fatal(err)
	}
	result, err := browser.ReconcileManagedProcesses(context.Background(), stateDir, browser.ManagedProcessReconcileOptions{
		ActivePID: 509,
		Now:       func() time.Time { return now },
		ProcessLister: func(context.Context, string) ([]int, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CompactedCount != 2 || result.RegisteredCount != 8 || result.HistoricalProcesses.SkipReasons["active_generation"] != 0 {
		t.Fatalf("reconcile = %+v, want stopped metadata generation eligible for the bounded terminal tail", result)
	}
}

func TestManagedProcessRegistryConcurrentLaunchesAndSymlinkSafety(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	const launches = 12
	var wg sync.WaitGroup
	errCh := make(chan error, launches)
	for i := 0; i < launches; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errCh <- browser.RegisterManagedProcessLaunch(stateDir, browser.ManagedMetadata{
				BrowserMode:      "headless",
				ChromePID:        1000 + i,
				UserDataDir:      profileDir,
				ProcessStartTime: time.Date(2026, 7, 20, 12, i, 0, 0, time.UTC).Format(time.RFC3339),
				OwnedMarker:      "owned-concurrent-" + string(rune('a'+i)),
			})
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("RegisterManagedProcessLaunch: %v", err)
		}
	}
	registry, ok, err := browser.LoadManagedProcessRegistry(stateDir)
	if err != nil || !ok || len(registry.Records) != launches {
		t.Fatalf("concurrent registry ok=%v err=%v records=%d, want %d", ok, err, len(registry.Records), launches)
	}

	symlinkState := filepath.Join(t.TempDir(), "symlink-state")
	if err := os.MkdirAll(filepath.Dir(browser.ManagedProcessRegistryPath(symlinkState)), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, browser.ManagedProcessRegistryPath(symlinkState)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := browser.LoadManagedProcessRegistry(symlinkState); err == nil {
		t.Fatal("LoadManagedProcessRegistry accepted a symlink")
	}
	if err := browser.SaveManagedProcessRegistry(symlinkState, browser.ManagedProcessRegistry{}); err == nil {
		t.Fatal("SaveManagedProcessRegistry replaced a symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "outside" {
		t.Fatalf("outside symlink target changed: %q err=%v", data, err)
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

func TestPrepareManagedProfileCopyDefaultFullyReplacesProfile(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	srcRoot := filepath.Join(t.TempDir(), "chrome")

	stateFiles := map[string]string{
		"Local State":                       "local-state",
		filepath.Join("Default", "Cookies"): "cookie-db",
		filepath.Join("Default", "Local Storage", "leveldb", "token.log"):                        "local-storage-token",
		filepath.Join("Default", "Local Storage", "leveldb", "DevToolsActivePort"):               "nested-devtools-state",
		filepath.Join("Default", "IndexedDB", "https_example_0.indexeddb.leveldb", "000003.log"): "indexeddb-state",
		filepath.Join("Default", "Extensions", "abcdefghijklmnop", "1.0.0", "manifest.json"):     `{"name":"synthetic-extension"}`,
		filepath.Join("Default", "Preferences"):                                                  `{"profile":{"name":"Synthetic"}}`,
		filepath.Join("Default", "History"):                                                      "history-db",
		filepath.Join("Default", "Cache", "Cache_Data", "f_000001"):                              "cache-bytes",
		filepath.Join("Default", "Service Worker", "Database", "000003.log"):                     "service-worker-db",
	}
	for rel, contents := range stateFiles {
		path := filepath.Join(srcRoot, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create source parent for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write source profile file %s: %v", rel, err)
		}
	}
	for _, rel := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket", "DevToolsActivePort"} {
		if err := os.WriteFile(filepath.Join(srcRoot, rel), []byte("runtime-artifact"), 0o600); err != nil {
			t.Fatalf("write runtime artifact %s: %v", rel, err)
		}
	}

	profileDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(filepath.Join(profileDir, "Default"), 0o700); err != nil {
		t.Fatalf("create old profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "Default", "stale"), []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale profile file: %v", err)
	}

	copied, err := browser.ReplaceManagedProfileFromDefault(profileDir, srcRoot)
	if err != nil {
		t.Fatalf("ReplaceManagedProfileFromDefault returned error: %v", err)
	}
	if copied < len(stateFiles) {
		t.Fatalf("copied file count = %d, want at least %d state files copied", copied, len(stateFiles))
	}
	for rel, want := range stateFiles {
		got, err := os.ReadFile(filepath.Join(profileDir, rel))
		if err != nil {
			t.Fatalf("copied profile missing %s: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("copied profile %s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(profileDir, "Default", "stale")); !os.IsNotExist(err) {
		t.Fatalf("stale destination file still exists: %v", err)
	}
	for _, rel := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket", "DevToolsActivePort"} {
		if _, err := os.Stat(filepath.Join(profileDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("root runtime artifact %s copied into managed profile: %v", rel, err)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(profileDir, "Default", "Cookies"))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("copied Cookies mode = %o, want 600", got)
		}
	}
}

func TestManagedMetadataStatusIncludesMetadataOnlyCopyDefaultFields(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	metadata := browser.ManagedMetadata{
		BrowserMode:          "headless",
		UserDataDir:          browser.ManagedProfileDir(stateDir),
		ProfileSeedStrategy:  browser.ProfileSeedStrategyCopyDefault,
		LastSeededAt:         "2026-05-21T12:00:00Z",
		DefaultProfileCopied: true,
		CopiedFileCount:      3,
		OwnedMarker:          "secret-token",
		ProcessStartTime:     "2026-05-21T12:00:00Z",
	}
	status := browser.ManagedMetadataStatus(metadata)
	if status.ProfileSeedStrategy != browser.ProfileSeedStrategyCopyDefault || !status.DefaultProfileCopied || status.CopiedFileCount != 3 {
		t.Fatalf("ManagedMetadataStatus() = %+v, want metadata-only copy-default fields", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if strings.Contains(string(encoded), "secret-token") || strings.Contains(string(encoded), "process_start_time") {
		t.Fatalf("ManagedMetadataStatus leaked internal ownership fields: %s", string(encoded))
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

func TestVerifyManagedOwnershipRequiresMatchingAtomicMetadataAndRegistry(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           4242,
		StartedAt:           "2026-06-11T12:00:00Z",
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		DebuggingPort:       "9222",
		ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
		OwnedMarker:         "synthetic-owned-token",
		ProcessStartTime:    "2026-06-11T12:00:00Z",
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	if err := browser.RegisterManagedProcessLaunch(stateDir, metadata); err != nil {
		t.Fatalf("RegisterManagedProcessLaunch returned error: %v", err)
	}

	evidence := browser.VerifyManagedOwnership(stateDir, browser.ManagedMetadataStatus(metadata))
	if !evidence.Checked || !evidence.Owned || len(evidence.Reasons) != 0 || !containsString(evidence.SafetyChecks, "managed_registry_record_matches") {
		t.Fatalf("VerifyManagedOwnership = %+v, want verified ownership", evidence)
	}
	mismatched := browser.ManagedMetadataStatus(metadata)
	mismatched.DebuggingPort = "9333"
	evidence = browser.VerifyManagedOwnership(stateDir, mismatched)
	if evidence.Owned || !containsString(evidence.Reasons, "runtime_debugging_port_mismatch") {
		t.Fatalf("VerifyManagedOwnership mismatched port = %+v, want unowned port mismatch", evidence)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(browser.ManagedMetadataPath(stateDir)), ".managed-state-*"))
	if err != nil {
		t.Fatalf("Glob temporary managed state: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("atomic managed state left temporary files: %+v", matches)
	}
}

func TestManagedProcessRegistryRoundTripAndStatusRedaction(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	registry := browser.ManagedProcessRegistry{
		Version:     1,
		BrowserMode: "headless",
		Records: []browser.ManagedProcessRecord{{
			PID:                 123,
			BrowserMode:         "headless",
			UserDataDir:         browser.ManagedProfileDir(stateDir),
			DebuggingPort:       "9222",
			StartedAt:           "2026-05-21T12:00:00Z",
			LastSeenAt:          "2026-05-21T12:00:01Z",
			State:               "live",
			OwnershipMarker:     "secret-token",
			ProcessStartTime:    "2026-05-21T12:00:00Z",
			ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
		}},
	}
	if err := browser.SaveManagedProcessRegistry(stateDir, registry); err != nil {
		t.Fatalf("SaveManagedProcessRegistry returned error: %v", err)
	}
	info, err := os.Stat(browser.ManagedProcessRegistryPath(stateDir))
	if err != nil {
		t.Fatalf("stat registry: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("registry permissions = %o, want 600", got)
	}
	loaded, ok, err := browser.LoadManagedProcessRegistry(stateDir)
	if err != nil {
		t.Fatalf("LoadManagedProcessRegistry returned error: %v", err)
	}
	if !ok || len(loaded.Records) != 1 || loaded.Records[0].OwnershipMarker != "secret-token" || loaded.Records[0].ProcessStartTime == "" {
		t.Fatalf("LoadManagedProcessRegistry() = %+v, %v, want private fields preserved", loaded, ok)
	}
	status := browser.ManagedProcessStatuses(loaded.Records)
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal statuses: %v", err)
	}
	if strings.Contains(string(encoded), "secret-token") || strings.Contains(string(encoded), "process_start_time") {
		t.Fatalf("ManagedProcessStatuses leaked internal ownership fields: %s", string(encoded))
	}
}

func TestLoadManagedProcessRegistryMissingAndMalformed(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	registry, ok, err := browser.LoadManagedProcessRegistry(stateDir)
	if err != nil {
		t.Fatalf("LoadManagedProcessRegistry missing returned error: %v", err)
	}
	if ok || registry.Version != 0 {
		t.Fatalf("LoadManagedProcessRegistry missing = %+v, %v; want missing", registry, ok)
	}
	if err := os.MkdirAll(filepath.Dir(browser.ManagedProcessRegistryPath(stateDir)), 0o700); err != nil {
		t.Fatalf("create registry parent: %v", err)
	}
	if err := os.WriteFile(browser.ManagedProcessRegistryPath(stateDir), []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write malformed registry: %v", err)
	}
	if _, _, err := browser.LoadManagedProcessRegistry(stateDir); err == nil {
		t.Fatalf("LoadManagedProcessRegistry malformed returned nil error")
	}
	result, err := browser.ReconcileManagedProcesses(context.Background(), stateDir, browser.ManagedProcessReconcileOptions{ReadOnly: true})
	if err == nil || result.State != "error" {
		t.Fatalf("ReconcileManagedProcesses malformed result=%+v err=%v, want error state", result, err)
	}
}

func TestReconcileManagedProcessesReapsDuplicateAndRetainsActivePID(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	if err := browser.SaveManagedProcessRegistry(stateDir, browser.ManagedProcessRegistry{
		Version:     1,
		BrowserMode: "headless",
		Records: []browser.ManagedProcessRecord{
			{PID: 101, BrowserMode: "headless", UserDataDir: profileDir, StartedAt: "2026-05-21T11:00:00Z", OwnershipMarker: "old-token", ProcessStartTime: "2026-05-21T11:00:00Z"},
			{PID: 202, BrowserMode: "headless", UserDataDir: profileDir, StartedAt: "2026-05-21T12:00:00Z", OwnershipMarker: "new-token", ProcessStartTime: "2026-05-21T12:00:00Z"},
		},
	}); err != nil {
		t.Fatalf("SaveManagedProcessRegistry returned error: %v", err)
	}
	var signaled []int
	result, err := browser.ReconcileManagedProcesses(context.Background(), stateDir, browser.ManagedProcessReconcileOptions{
		ActivePID:  202,
		ReapExtras: true,
		Now:        func() time.Time { return time.Date(2026, 5, 21, 12, 5, 0, 0, time.UTC) },
		ProcessLister: func(context.Context, string) ([]int, error) {
			return []int{101, 202}, nil
		},
		Signal: func(pid int) error {
			signaled = append(signaled, pid)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ReconcileManagedProcesses returned error: %v", err)
	}
	if result.State != "reaped" || result.LiveCount != 1 || result.ReapedCount != 1 || !containsInt(result.ReapedPIDs, 101) || containsInt(signaled, 202) {
		t.Fatalf("ReconcileManagedProcesses = %+v signaled=%+v, want only duplicate 101 reaped", result, signaled)
	}
	if !containsString(result.SafetyChecks, "process_command_line_matches_managed_profile") {
		t.Fatalf("safety checks = %+v, want command-line ownership check", result.SafetyChecks)
	}
	loaded, ok, err := browser.LoadManagedProcessRegistry(stateDir)
	if err != nil || !ok {
		t.Fatalf("LoadManagedProcessRegistry ok=%v err=%v, want saved registry", ok, err)
	}
	if got := processRecordState(loaded.Records, 101); got != "reaped" {
		t.Fatalf("record 101 state = %q, want reaped", got)
	}
	if got := processRecordState(loaded.Records, 202); got != "live" {
		t.Fatalf("record 202 state = %q, want live", got)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(encoded), "old-token") || strings.Contains(string(encoded), "new-token") || strings.Contains(string(encoded), "process_start_time") {
		t.Fatalf("ReconcileManagedProcesses leaked internal ownership fields: %s", string(encoded))
	}
}

func TestReconcileManagedProcessesReportsSignalFailure(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	if err := browser.SaveManagedProcessRegistry(stateDir, browser.ManagedProcessRegistry{
		Version:     1,
		BrowserMode: "headless",
		Records: []browser.ManagedProcessRecord{
			{PID: 101, BrowserMode: "headless", UserDataDir: profileDir},
			{PID: 202, BrowserMode: "headless", UserDataDir: profileDir},
		},
	}); err != nil {
		t.Fatalf("SaveManagedProcessRegistry returned error: %v", err)
	}
	result, err := browser.ReconcileManagedProcesses(context.Background(), stateDir, browser.ManagedProcessReconcileOptions{
		ActivePID:  202,
		ReapExtras: true,
		ProcessLister: func(context.Context, string) ([]int, error) {
			return []int{101, 202}, nil
		},
		Signal: func(pid int) error {
			if pid == 101 {
				return errors.New("synthetic signal failure")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ReconcileManagedProcesses returned error: %v", err)
	}
	if result.State != "degraded" || result.LiveCount != 2 || len(result.SignalFailures) != 1 || result.SignalFailures[0].PID != 101 || !containsInt(result.SkippedPIDs, 101) {
		t.Fatalf("ReconcileManagedProcesses = %+v, want degraded signal failure for 101", result)
	}
	loaded, ok, err := browser.LoadManagedProcessRegistry(stateDir)
	if err != nil || !ok {
		t.Fatalf("LoadManagedProcessRegistry ok=%v err=%v, want saved registry", ok, err)
	}
	if got := processRecordState(loaded.Records, 101); got != "signal_failed" {
		t.Fatalf("record 101 state = %q, want signal_failed", got)
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

func TestStopManagedChromeForceRecoversIncompleteOwnershipFromCommandLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process command-line recovery test is unix-only")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		UserDataDir:         profileDir,
		ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	script := `#!/usr/bin/env sh
trap 'exit 0' INT TERM
while :; do sleep 1; done
`
	if err := os.WriteFile(chromePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake chrome: %v", err)
	}
	cmd := exec.Command(chromePath, "--headless", "--remote-debugging-port=0", "--user-data-dir="+profileDir)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake managed chrome: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	var signaled []int
	result, err := browser.StopManagedChrome(context.Background(), stateDir, browser.ManagedStopOptions{
		Force: true,
		Signal: func(pid int) error {
			signaled = append(signaled, pid)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("StopManagedChrome returned error: %v", err)
	}
	if !result.Checked || !result.Force || result.Skipped || !result.Stopped || len(signaled) == 0 {
		t.Fatalf("StopManagedChrome = %+v signaled=%+v, want forced recovered process", result, signaled)
	}
	if signaled[0] != cmd.Process.Pid || !containsInt(result.PIDs, cmd.Process.Pid) {
		t.Fatalf("StopManagedChrome pids = result %+v signaled=%+v, want fake chrome pid %d", result.PIDs, signaled, cmd.Process.Pid)
	}
	if !containsString(result.SafetyChecks, "managed_profile_path_matches_state_dir") || !containsString(result.SafetyChecks, "process_command_line_matches_managed_profile") {
		t.Fatalf("safety checks = %+v, want managed profile and command-line checks", result.SafetyChecks)
	}
	if !containsString(result.SafetyChecks, "process_tree_inspected") || len(result.ProcessEvidence) == 0 || result.ProcessEvidence[0].Role != "root" || !result.ProcessEvidence[0].DebuggingPortMatch {
		t.Fatalf("process evidence = %+v checks=%+v, want root port and process-tree evidence", result.ProcessEvidence, result.SafetyChecks)
	}
}

func TestStopManagedChromeForceRecoversWithoutMetadataFromCommandLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process command-line recovery test is unix-only")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	script := `#!/usr/bin/env sh
trap 'exit 0' INT TERM
while :; do sleep 1; done
`
	if err := os.WriteFile(chromePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake chrome: %v", err)
	}
	cmd := exec.Command(chromePath, "--headless", "--remote-debugging-port=0", "--user-data-dir="+profileDir)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake managed chrome: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	var signaled []int
	result, err := browser.StopManagedChrome(context.Background(), stateDir, browser.ManagedStopOptions{
		Force: true,
		Signal: func(pid int) error {
			signaled = append(signaled, pid)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("StopManagedChrome returned error: %v", err)
	}
	if !result.Checked || !result.Force || result.Skipped || !result.Stopped || len(signaled) == 0 {
		t.Fatalf("StopManagedChrome = %+v signaled=%+v, want forced recovered process without metadata", result, signaled)
	}
	if signaled[0] != cmd.Process.Pid || !containsInt(result.PIDs, cmd.Process.Pid) {
		t.Fatalf("StopManagedChrome pids = result %+v signaled=%+v, want fake chrome pid %d", result.PIDs, signaled, cmd.Process.Pid)
	}
	if !containsString(result.SafetyChecks, "managed_profile_path_matches_state_dir") || !containsString(result.SafetyChecks, "process_command_line_matches_managed_profile") {
		t.Fatalf("safety checks = %+v, want managed profile and command-line checks", result.SafetyChecks)
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

func TestStopManagedChromeDoesNotClaimSuccessWhileOwnedTreeRemains(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           123,
		StartedAt:           "2026-05-21T12:00:00Z",
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		DebuggingPort:       "9222",
		ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
		OwnedMarker:         "owned-token",
		ProcessStartTime:    "2026-05-21T12:00:00Z",
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	var signaled []int
	result, err := browser.StopManagedChrome(context.Background(), stateDir, browser.ManagedStopOptions{
		Signal: func(pid int) error {
			signaled = append(signaled, pid)
			return nil
		},
		ProcessLister: func(context.Context, string) ([]int, error) {
			return []int{123, 456}, nil
		},
		EndpointReachable:        func(context.Context, string) bool { return true },
		VerificationTimeout:      10 * time.Millisecond,
		VerificationPollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StopManagedChrome returned error: %v", err)
	}
	if result.Stopped || result.Reason == "" || len(result.RemainingPIDs) != 2 {
		t.Fatalf("StopManagedChrome = %+v, want stopped=false with remaining PIDs", result)
	}
	if len(signaled) != 2 || signaled[0] != 123 || signaled[1] != 456 {
		t.Fatalf("signaled PIDs = %+v, want root and remaining descendant", signaled)
	}
}

func TestStopManagedChromeVerifiesDescendantsWithoutRootChromeFlags(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll profile returned error: %v", err)
	}
	readyFile := filepath.Join(t.TempDir(), "child-ready")
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	script := "#!/usr/bin/env sh\n/bin/sleep 30 &\necho $! > \"$CDP_TEST_READY\"\nwait\n"
	if err := os.WriteFile(chromePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write synthetic managed process: %v", err)
	}
	cmd := exec.Command(chromePath, "--headless", "--remote-debugging-port=9222", "--user-data-dir="+profileDir)
	cmd.Env = append(os.Environ(), "CDP_TEST_READY="+readyFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start synthetic managed process tree: %v", err)
	}
	ready := false
	for range 100 {
		if _, err := os.Stat(readyFile); err == nil {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("synthetic managed process did not report its descendant ready")
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	if err := browser.SaveManagedMetadata(stateDir, browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           cmd.Process.Pid,
		StartedAt:           "2026-05-21T12:00:00Z",
		UserDataDir:         profileDir,
		DebuggingPort:       "9222",
		ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
		OwnedMarker:         "owned-token",
		ProcessStartTime:    "2026-05-21T12:00:00Z",
	}); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	var signaled []int
	result, err := browser.StopManagedChrome(context.Background(), stateDir, browser.ManagedStopOptions{
		Signal: func(pid int) error {
			signaled = append(signaled, pid)
			return nil
		},
		EndpointReachable:        func(context.Context, string) bool { return false },
		VerificationTimeout:      500 * time.Millisecond,
		VerificationPollInterval: time.Second,
	})
	if err != nil {
		t.Fatalf("StopManagedChrome returned error: %v", err)
	}
	if result.Stopped || len(result.RemainingPIDs) < 2 {
		t.Fatalf("StopManagedChrome = %+v, want an unproven shutdown with root and descendant remaining", result)
	}
	if len(signaled) < 2 || !containsInt(signaled, cmd.Process.Pid) || len(signaled) == 1 {
		t.Fatalf("signaled PIDs = %+v, want root and descendant", signaled)
	}
}

func TestStopManagedChromeFailsClosedWhenVerificationContextIsCancelled(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := browser.SaveManagedMetadata(stateDir, browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           123,
		StartedAt:           "2026-05-21T12:00:00Z",
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		DebuggingPort:       "9222",
		ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
		OwnedMarker:         "owned-token",
		ProcessStartTime:    "2026-05-21T12:00:00Z",
	}); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := browser.StopManagedChrome(ctx, stateDir, browser.ManagedStopOptions{
		Signal: func(int) error { return nil },
		ProcessLister: func(context.Context, string) ([]int, error) {
			return nil, nil
		},
		EndpointReachable: func(context.Context, string) bool { return false },
	})
	if err == nil || result.Stopped {
		t.Fatalf("StopManagedChrome = %+v, err=%v; want cancellation error without a success claim", result, err)
	}
}

func TestStopManagedChromeTracksDescendantAfterOwnedRootExits(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process tree verification is only implemented on Unix")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll profile returned error: %v", err)
	}
	readyFile := filepath.Join(t.TempDir(), "child-ready")
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	script := "#!/usr/bin/env sh\n/bin/sleep 30 &\necho $! > \"$CDP_TEST_READY\"\nwait\n"
	if err := os.WriteFile(chromePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write synthetic managed process: %v", err)
	}
	cmd := exec.Command(chromePath, "--headless", "--remote-debugging-port=9222", "--user-data-dir="+profileDir)
	cmd.Env = append(os.Environ(), "CDP_TEST_READY="+readyFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start synthetic managed process tree: %v", err)
	}
	childPID := 0
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if childPID > 0 {
			if child, err := os.FindProcess(childPID); err == nil {
				_ = child.Kill()
			}
		}
	})
	ready := false
	for range 100 {
		if _, err := os.Stat(readyFile); err == nil {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("synthetic managed process did not report its descendant ready")
	}
	contents, err := os.ReadFile(readyFile)
	if err != nil {
		t.Fatalf("read child PID: %v", err)
	}
	childPID, err = strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil || childPID <= 0 {
		t.Fatalf("child PID = %q, want a positive PID", strings.TrimSpace(string(contents)))
	}

	if err := browser.SaveManagedMetadata(stateDir, browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           cmd.Process.Pid,
		StartedAt:           "2026-05-21T12:00:00Z",
		UserDataDir:         profileDir,
		DebuggingPort:       "9222",
		ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
		OwnedMarker:         "owned-token",
		ProcessStartTime:    "2026-05-21T12:00:00Z",
	}); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}
	rootPID := cmd.Process.Pid
	var signaled []int
	result, err := browser.StopManagedChrome(context.Background(), stateDir, browser.ManagedStopOptions{
		Signal: func(pid int) error {
			signaled = append(signaled, pid)
			if pid == rootPID {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatalf("kill synthetic managed root: %v", err)
				}
				_ = cmd.Wait()
			}
			return nil
		},
		EndpointReachable:        func(context.Context, string) bool { return false },
		VerificationTimeout:      500 * time.Millisecond,
		VerificationPollInterval: 10 * time.Millisecond,
	})
	if err != nil && !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("StopManagedChrome returned unexpected error: %v", err)
	}
	if result.Stopped || !containsInt(result.RemainingPIDs, childPID) {
		t.Fatalf("StopManagedChrome = %+v, want the surviving orphaned descendant to block success", result)
	}
	if !containsInt(signaled, rootPID) || !containsInt(signaled, childPID) {
		t.Fatalf("signaled PIDs = %+v, want root %d and descendant %d", signaled, rootPID, childPID)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func TestStartManagedChromeOutlivesCallerContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell chrome test is unix-only")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "DevToolsActivePort"), []byte("1\n/devtools/browser/stale\n"), 0o600); err != nil {
		t.Fatalf("write stale active port: %v", err)
	}
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	script := `#!/usr/bin/env sh
set -eu
user_data_dir=
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) user_data_dir="${arg#--user-data-dir=}" ;;
  esac
done
mkdir -p "$user_data_dir"
printf '12345\n/devtools/browser/test\n' > "$user_data_dir/DevToolsActivePort"
sleep 30
`
	if err := os.WriteFile(chromePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake chrome: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	launch, err := browser.StartManagedChrome(ctx, browser.ManagedOptions{StateDir: stateDir, Chrome: chromePath})
	if err != nil {
		t.Fatalf("StartManagedChrome returned error: %v", err)
	}
	registry, ok, err := browser.LoadManagedProcessRegistry(stateDir)
	if err != nil {
		t.Fatalf("LoadManagedProcessRegistry returned error: %v", err)
	}
	if !ok || len(registry.Records) != 1 || registry.Records[0].PID != launch.Metadata.ChromePID || registry.Records[0].OwnershipMarker == "" {
		t.Fatalf("managed process registry = %+v ok=%v, want registered launch", registry, ok)
	}
	if launch.Metadata.DebuggingPort != "12345" {
		t.Fatalf("StartManagedChrome reused stale active port %q, want fake Chrome port", launch.Metadata.DebuggingPort)
	}
	t.Cleanup(func() {
		process, findErr := os.FindProcess(launch.Metadata.ChromePID)
		if findErr == nil {
			_ = process.Kill()
		}
	})
	cancel()
	time.Sleep(100 * time.Millisecond)

	process, err := os.FindProcess(launch.Metadata.ChromePID)
	if err != nil {
		t.Fatalf("FindProcess returned error: %v", err)
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("managed chrome died when caller context was canceled: %v", err)
	}
}

func TestStartManagedChromeBlocksWhenManagedProcessAlreadyLive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell chrome test is unix-only")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	profileDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	oldChromePath := filepath.Join(t.TempDir(), "old-fake-chrome")
	oldScript := `#!/usr/bin/env sh
trap 'exit 0' INT TERM
while :; do sleep 1; done
`
	if err := os.WriteFile(oldChromePath, []byte(oldScript), 0o755); err != nil {
		t.Fatalf("write old fake chrome: %v", err)
	}
	old := exec.Command(oldChromePath, "--headless", "--remote-debugging-port=0", "--user-data-dir="+profileDir)
	if err := old.Start(); err != nil {
		t.Fatalf("start old fake managed chrome: %v", err)
	}
	t.Cleanup(func() {
		_ = old.Process.Kill()
		_, _ = old.Process.Wait()
	})
	newChromePath := filepath.Join(t.TempDir(), "new-fake-chrome")
	newScript := `#!/usr/bin/env sh
exit 99
`
	if err := os.WriteFile(newChromePath, []byte(newScript), 0o755); err != nil {
		t.Fatalf("write new fake chrome: %v", err)
	}
	_, err := browser.StartManagedChrome(context.Background(), browser.ManagedOptions{StateDir: stateDir, Chrome: newChromePath})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("StartManagedChrome error = %v, want already-running guard", err)
	}
}

func TestStartManagedChromeReusesExistingCopyDefaultProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell chrome test is unix-only")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	homeDir := t.TempDir()
	sourceRoot := filepath.Join(homeDir, ".config", "google-chrome")
	if runtime.GOOS == "darwin" {
		sourceRoot = filepath.Join(homeDir, "Library", "Application Support", "Google", "Chrome")
	}
	if err := os.MkdirAll(filepath.Join(sourceRoot, "Default"), 0o700); err != nil {
		t.Fatalf("create source profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "Default", "Cookies"), []byte("source-cookie-db"), 0o600); err != nil {
		t.Fatalf("write source cookies: %v", err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))

	profileDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(filepath.Join(profileDir, "Default"), 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	cookiePath := filepath.Join(profileDir, "Default", "Cookies")
	if err := os.WriteFile(cookiePath, []byte("active-managed-cookie-db"), 0o600); err != nil {
		t.Fatalf("write managed cookies: %v", err)
	}
	metadata := browser.ManagedMetadata{
		BrowserMode:          "headless",
		UserDataDir:          profileDir,
		ProfileSeedStrategy:  browser.ProfileSeedStrategyCopyDefault,
		LastSeededAt:         time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		DefaultProfileCopied: true,
		CopiedFileCount:      1,
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("save managed metadata: %v", err)
	}

	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	script := `#!/usr/bin/env sh
set -eu
user_data_dir=
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) user_data_dir="${arg#--user-data-dir=}" ;;
  esac
done
printf '12345\n/devtools/browser/test\n' > "$user_data_dir/DevToolsActivePort"
sleep 30
`
	if err := os.WriteFile(chromePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake chrome: %v", err)
	}

	launch, err := browser.StartManagedChrome(context.Background(), browser.ManagedOptions{StateDir: stateDir, Chrome: chromePath})
	if err != nil {
		t.Fatalf("StartManagedChrome returned error: %v", err)
	}
	t.Cleanup(func() {
		process, findErr := os.FindProcess(launch.Metadata.ChromePID)
		if findErr == nil {
			_ = process.Kill()
		}
	})

	content, err := os.ReadFile(cookiePath)
	if err != nil {
		t.Fatalf("read managed cookies: %v", err)
	}
	if string(content) != "active-managed-cookie-db" {
		t.Fatalf("StartManagedChrome recopied default profile; cookie fixture = %q", content)
	}
}

func TestReuseManagedChromeUsesExistingActivePort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"Browser": "Chrome/144.0"})
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split server host: %v", err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	userDataDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(userDataDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte(port+"\n/devtools/browser/reused\n"), 0o600); err != nil {
		t.Fatalf("write active port: %v", err)
	}
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           os.Getpid(),
		UserDataDir:         userDataDir,
		DebuggingPort:       "12345",
		ProfileSeedStrategy: browser.ProfileSeedStrategyCopyDefault,
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}

	launch, ok, err := browser.ReuseManagedChrome(context.Background(), stateDir)
	if err != nil {
		t.Fatalf("ReuseManagedChrome returned error: %v", err)
	}
	if !ok {
		t.Fatalf("ReuseManagedChrome ok = false, want true")
	}
	wantEndpoint := "ws://" + net.JoinHostPort("127.0.0.1", port) + "/devtools/browser/reused"
	if launch.Endpoint != wantEndpoint || launch.Metadata.DebuggingPort != port {
		t.Fatalf("ReuseManagedChrome = %+v, want endpoint from active port", launch)
	}
}

func TestReuseManagedChromeRejectsUnreachableActivePortEvenWhenPIDLooksAlive(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	userDataDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(userDataDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte("1\n/devtools/browser/stale\n"), 0o600); err != nil {
		t.Fatalf("write active port: %v", err)
	}
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           os.Getpid(),
		UserDataDir:         userDataDir,
		DebuggingPort:       "12345",
		ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}

	launch, ok, err := browser.ReuseManagedChrome(context.Background(), stateDir)
	if err != nil {
		t.Fatalf("ReuseManagedChrome returned error: %v", err)
	}
	if ok {
		t.Fatalf("ReuseManagedChrome = %+v ok=true, want stale active port rejected", launch)
	}
}

func TestReuseManagedChromeUsesReachableEndpointWhenSavedPIDExited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"Browser": "Chrome/144.0"})
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split server host: %v", err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	userDataDir := browser.ManagedProfileDir(stateDir)
	if err := os.MkdirAll(userDataDir, 0o700); err != nil {
		t.Fatalf("create managed profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDataDir, "DevToolsActivePort"), []byte(port+"\n/devtools/browser/reused\n"), 0o600); err != nil {
		t.Fatalf("write active port: %v", err)
	}
	metadata := browser.ManagedMetadata{
		BrowserMode:         "headless",
		ChromePID:           exitedPID(t),
		UserDataDir:         userDataDir,
		DebuggingPort:       "12345",
		ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
	}
	if err := browser.SaveManagedMetadata(stateDir, metadata); err != nil {
		t.Fatalf("SaveManagedMetadata returned error: %v", err)
	}

	launch, ok, err := browser.ReuseManagedChrome(context.Background(), stateDir)
	if err != nil {
		t.Fatalf("ReuseManagedChrome returned error: %v", err)
	}
	if !ok {
		t.Fatalf("ReuseManagedChrome ok = false, want true from reachable endpoint")
	}
	wantEndpoint := "ws://" + net.JoinHostPort("127.0.0.1", port) + "/devtools/browser/reused"
	if launch.Endpoint != wantEndpoint || launch.Metadata.DebuggingPort != port {
		t.Fatalf("ReuseManagedChrome = %+v, want reachable endpoint despite stale PID", launch)
	}
}

func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait helper process: %v", err)
	}
	return pid
}

func processRecordState(records []browser.ManagedProcessRecord, pid int) string {
	for _, record := range records {
		if record.PID == pid {
			return record.State
		}
	}
	return ""
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
	if runtime.GOOS == "darwin" {
		t.Setenv("CDP_CHROME_CANDIDATES", filepath.Join(t.TempDir(), "missing-chrome"))
	}
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
