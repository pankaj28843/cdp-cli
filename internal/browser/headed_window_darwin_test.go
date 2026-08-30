//go:build darwin

package browser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeadedChromeProcessCountMatchesCompleteTable(t *testing.T) {
	userDataDir, err := defaultUserDataDir("stable")
	if err != nil {
		t.Fatalf("defaultUserDataDir: %v", err)
	}
	binDir := t.TempDir()
	writeManagedPSFixture(t, binDir, "#!/bin/sh\nprintf '%s\\n' '420 Google Chrome --user-data-dir="+userDataDir+"'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	count, err := headedChromeProcessCount(context.Background(), "Google Chrome", "stable")
	if err != nil || count != 1 {
		t.Fatalf("headedChromeProcessCount() = %d, %v; want one complete-table match for %q", count, err, strings.TrimSpace(userDataDir))
	}
}

func TestEnsureHeadedChromeWindowDoesNotLaunchAfterProcessProbeFailure(t *testing.T) {
	binDir := t.TempDir()
	writeManagedPSFixture(t, binDir, "#!/bin/sh\nexit 7\n")
	openMarker := filepath.Join(t.TempDir(), "open-marker")
	openPath := filepath.Join(binDir, "open")
	if err := os.WriteFile(openPath, []byte("#!/bin/sh\nprintf opened > \""+openMarker+"\"\n"), 0o700); err != nil {
		t.Fatalf("write open fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := ensureHeadedChromeWindow(context.Background(), "stable")
	if err != nil {
		t.Fatalf("ensureHeadedChromeWindow returned error: %v", err)
	}
	if result.Action != "failed" || result.WindowReady {
		t.Fatalf("ensureHeadedChromeWindow result = %+v, want explicit failed scan", result)
	}
	if _, statErr := os.Stat(openMarker); statErr == nil {
		t.Fatal("ensureHeadedChromeWindow invoked Launch Services after an unknown process scan")
	}
}
