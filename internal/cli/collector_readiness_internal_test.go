package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishCollectorReadinessLifecycleAndSafety(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "collector.ready.json")
	remove, err := publishCollectorReadiness(path, "target-1", "session-1", []string{"Runtime", "Log"})
	if err != nil {
		t.Fatalf("publishCollectorReadiness: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat readiness: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("readiness mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record collectorReadinessRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("read readiness: %v", err)
	}
	if record.SchemaVersion != collectorReadinessSchemaVersion || record.State != "ready" || record.TargetID != "target-1" || !record.SessionBound || record.CollectorPID != os.Getpid() || record.ReadyMonotonicNS <= 0 {
		t.Fatalf("readiness record = %+v", record)
	}
	if len(record.EnabledDomains) != 2 || record.EnabledDomains[0] != "Log" || record.EnabledDomains[1] != "Runtime" {
		t.Fatalf("enabled domains = %+v, want sorted enum", record.EnabledDomains)
	}
	if _, err := publishCollectorReadiness(path, "target-1", "session-1", nil); err == nil {
		t.Fatal("exclusive readiness creation accepted an existing file")
	}
	remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("readiness remains after cleanup: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := publishCollectorReadiness(filepath.Join(root, "linked", "ready.json"), "target-1", "session-1", nil); err == nil {
		t.Fatal("readiness accepted a symlink parent")
	}
	unsafeRoot := t.TempDir()
	if err := os.Chmod(unsafeRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := publishCollectorReadiness(filepath.Join(unsafeRoot, "ready.json"), "target-1", "session-1", nil); err == nil {
		t.Fatal("readiness accepted a group/world-writable root")
	}
}
