//go:build darwin

package browser

import (
	"bytes"
	"encoding/json"
	"testing"
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
