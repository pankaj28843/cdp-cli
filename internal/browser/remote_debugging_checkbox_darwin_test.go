//go:build darwin

package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnableRemoteDebuggingInLocalState(t *testing.T) {
	updated, changed, err := enableRemoteDebuggingInLocalState([]byte(`{"version":1,"devtools":{"remote_debugging":{"user-enabled":false},"keep":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("state was not changed")
	}
	var state map[string]any
	if err := json.Unmarshal(updated, &state); err != nil {
		t.Fatal(err)
	}
	remote := state["devtools"].(map[string]any)["remote_debugging"].(map[string]any)
	if enabled, ok := remote["user-enabled"].(bool); !ok || !enabled {
		t.Fatalf("user-enabled = %#v, want true", remote["user-enabled"])
	}
	if keep := state["devtools"].(map[string]any)["keep"]; keep != true {
		t.Fatalf("unrelated state changed: %#v", keep)
	}
}

func TestEnableRemoteDebuggingInLocalStateAlreadyEnabled(t *testing.T) {
	original := []byte(`{"devtools":{"remote_debugging":{"user-enabled":true}}}`)
	updated, changed, err := enableRemoteDebuggingInLocalState(original)
	if err != nil {
		t.Fatal(err)
	}
	if changed || !bytes.Equal(updated, original) {
		t.Fatalf("already enabled state changed: changed=%v bytes=%q", changed, updated)
	}
}

func TestChromeCommandUsesDefaultProfile(t *testing.T) {
	defaultProfile := "/Users/test/Library/Application Support/Google/Chrome"
	for _, test := range []struct {
		name    string
		command string
		want    bool
	}{
		{name: "headed main", command: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --profile-directory=Default", want: true},
		{name: "managed headless child", command: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome Helper --user-data-dir=/Users/test/.cdp-cli/browser/headless-profile", want: false},
		{name: "default headless", command: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --headless --user-data-dir=/Users/test/Library/Application Support/Google/Chrome", want: true},
		{name: "crashpad", command: "/Applications/Google Chrome.app/Contents/MacOS/chrome_crashpad_handler --database=/Users/test/Library/Application Support/Google/Chrome/Crashpad", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := chromeCommandUsesDefaultProfile(test.command, defaultProfile); got != test.want {
				t.Fatalf("default profile in use = %v, want %v", got, test.want)
			}
		})
	}
}

func TestChromeDefaultProfileInUseTerminatesProbeDescendantOnCancel(t *testing.T) {
	binDir := t.TempDir()
	pidPath := filepath.Join(t.TempDir(), "profile-probe-child.pid")
	writeManagedPSFixture(t, binDir, `#!/bin/sh
set -eu
(
  trap '' TERM INT
  exec /bin/sleep 30
) &
child=$!
printf '%s\n' "$child" > "$CDP_PROFILE_PROBE_CHILD_PID_FILE"
while :; do /bin/sleep 1; done
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CDP_PROFILE_PROBE_CHILD_PID_FILE", pidPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- chromeDefaultProfileInUse(ctx, "Google Chrome", "/synthetic/profile") }()
	childPID := waitForManagedPSChildPID(t, pidPath)
	cancel()
	t.Cleanup(func() {
		if process, err := os.FindProcess(childPID); err == nil {
			_ = process.Kill()
		}
	})

	select {
	case <-done:
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && managedPSProcessAlive(childPID) {
			time.Sleep(20 * time.Millisecond)
		}
		if managedPSProcessAlive(childPID) {
			t.Fatalf("profile-use probe returned while descendant %d remained alive", childPID)
		}
	case <-time.After(2 * time.Second):
		if process, err := os.FindProcess(childPID); err == nil {
			_ = process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("profile-use probe remained blocked after descendant cleanup")
		}
		t.Fatalf("profile-use probe left descendant %d alive after cancellation", childPID)
	}
}

func TestChromeDefaultProfileInUseFailsClosedOnProbeOverflow(t *testing.T) {
	binDir := t.TempDir()
	writeManagedPSFixture(t, binDir, `#!/bin/sh
/usr/bin/yes p | /usr/bin/head -c 4194305
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if !chromeDefaultProfileInUse(context.Background(), "Google Chrome", "/synthetic/profile") {
		t.Fatal("chromeDefaultProfileInUse returned false after process-table overflow; want fail-closed true")
	}
}

func TestNativeChromeProcessIDsUsesCompleteOwnedPGrepOutput(t *testing.T) {
	binDir := t.TempDir()
	pgrepPath := filepath.Join(binDir, "pgrep")
	if err := os.WriteFile(pgrepPath, []byte("#!/bin/sh\nprintf '%s\\n' 410 411\n"), 0o700); err != nil {
		t.Fatalf("write pgrep fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pids, err := nativeChromeProcessIDs(context.Background(), "Google Chrome")
	if err != nil {
		t.Fatalf("nativeChromeProcessIDs returned error: %v", err)
	}
	if len(pids) != 2 || pids[0] != 410 || pids[1] != 411 {
		t.Fatalf("nativeChromeProcessIDs = %v, want [410 411]", pids)
	}
}

func TestNativeChromeProcessIDsClassifiesNoProcesses(t *testing.T) {
	binDir := t.TempDir()
	pgrepPath := filepath.Join(binDir, "pgrep")
	if err := os.WriteFile(pgrepPath, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write pgrep fixture: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pids, err := nativeChromeProcessIDs(context.Background(), "Google Chrome")
	if err != nil {
		t.Fatalf("nativeChromeProcessIDs returned error for no processes: %v", err)
	}
	if len(pids) != 0 {
		t.Fatalf("nativeChromeProcessIDs = %v, want no processes", pids)
	}
}
