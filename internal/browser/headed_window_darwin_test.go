//go:build darwin

package browser

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestHeadedWindowLeavesRunningBrowserAndHeadlessAlone(t *testing.T) {
	binDir := t.TempDir()
	writeManagedPSFixture(t, binDir, "#!/bin/sh\nprintf '%s\\n' '420 Google Chrome' '421 Google Chrome --headless' '422 Google Chrome Helper --type=renderer'\n")
	marker := filepath.Join(t.TempDir(), "unexpected-action")
	for _, command := range []string{"open", "osascript"} {
		if err := os.WriteFile(filepath.Join(binDir, command), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	result, err := ensureHeadedChromeWindow(context.Background(), "stable")
	if err != nil || !result.WindowReady || result.WindowsBefore != 1 || result.Action != "already_ready" {
		t.Fatalf("ensure = %+v, %v", result, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("routine ensure activated or reopened Chrome: %v", err)
	}
}

func TestHeadedActionTargetsOnlyDefaultProfileBrowser(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table string
		want  string
	}{
		{"headed and headless", "420 Google Chrome\n421 Google Chrome --headless --user-data-dir=/tmp/managed\n422 Google Chrome Helper --type=renderer", "420"},
		{"headless alone", "421 Google Chrome --headless", ""},
		{"helpers alone", "422 Google Chrome Helper --type=renderer", ""},
		{"other profile", "423 Google Chrome --user-data-dir=/tmp/other", ""},
		{"split other profile", "423 Google Chrome --user-data-dir /tmp/other", ""},
		{"wrapper", "425 /bin/sh -c /Applications/Google Chrome.app/Contents/MacOS/Google Chrome", ""},
		{"ambiguous", "420 Google Chrome\n424 Google Chrome", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			writeManagedPSFixture(t, binDir, "#!/bin/sh\nprintf '%s\\n' '"+tc.table+"'\n")
			marker := filepath.Join(t.TempDir(), "action-args")
			if err := os.WriteFile(filepath.Join(binDir, "osascript"), []byte("#!/bin/sh\nprintf '%s\\n' \"$5\" \"$6\" \"$7\" > '"+marker+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			err := runHeadedChromeAction(context.Background(), "Google Chrome", RemoteDebuggingApprovalURL)
			data, readErr := os.ReadFile(marker)
			if tc.want == "" {
				if err == nil || !os.IsNotExist(readErr) {
					t.Fatalf("unresolved browser dispatched action: err=%v read=%v", err, readErr)
				}
			} else if err != nil || readErr != nil || string(data) != tc.want+"\nGoogle Chrome\n"+RemoteDebuggingApprovalURL+"\n" {
				t.Fatalf("action arguments = %q, err=%v read=%v", data, err, readErr)
			}
		})
	}
}

func TestHeadedActionScriptRejectsNonApplicationProcess(t *testing.T) {
	// Execute the native bridge against this test process, which is not a
	// foreground application. This checks the real script without browser input.
	command := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", headedChromeActionScript, strconv.Itoa(os.Getpid()), "Google Chrome", RemoteDebuggingApprovalURL)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "headed Chrome process is no longer available") {
		t.Fatalf("native guard = %v, %s", err, output)
	}
}
