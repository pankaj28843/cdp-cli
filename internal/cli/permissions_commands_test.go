package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestPermissionsGrantJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"permissions", "grant", "notifications", "geolocation", "--origin", "https://example.test/path?q=1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("permissions grant exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK          bool `json:"ok"`
		Permissions struct {
			Action        string   `json:"action"`
			Origin        string   `json:"origin"`
			Setting       string   `json:"setting"`
			BrowserScoped bool     `json:"browser_scoped"`
			ResetCommand  string   `json:"reset_command"`
			Warnings      []string `json:"warnings"`
			Permissions   []struct {
				Name    string `json:"name"`
				Setting string `json:"setting"`
				Origin  string `json:"origin"`
				Method  string `json:"method"`
			} `json:"permissions"`
		} `json:"permissions"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("permissions grant output is invalid JSON: %v", err)
	}
	if !got.OK || got.Permissions.Action != "grant" || got.Permissions.Origin != "https://example.test" || got.Permissions.Setting != "granted" || !got.Permissions.BrowserScoped || !strings.Contains(got.Permissions.ResetCommand, "permissions reset") || len(got.Permissions.Warnings) == 0 {
		t.Fatalf("permissions grant output = %+v, want scoped grant metadata and reset warning", got)
	}
	if len(got.Permissions.Permissions) != 2 || got.Permissions.Permissions[0].Name != "notifications" || got.Permissions.Permissions[0].Method != "Browser.setPermission" || got.Permissions.Permissions[1].Name != "geolocation" {
		t.Fatalf("permissions grant rows = %+v, want granted notification and geolocation rows", got.Permissions.Permissions)
	}
	if !containsString(got.NextCommands, "cdp permissions reset --json") {
		t.Fatalf("next_commands = %v, want reset command", got.NextCommands)
	}
}

func TestPermissionsResetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"permissions", "reset", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("permissions reset exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK          bool `json:"ok"`
		Permissions struct {
			Action          string   `json:"action"`
			Method          string   `json:"method"`
			BrowserScoped   bool     `json:"browser_scoped"`
			ResetAllOrigins bool     `json:"reset_all_origins"`
			Warnings        []string `json:"warnings"`
		} `json:"permissions"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("permissions reset output is invalid JSON: %v", err)
	}
	if !got.OK || got.Permissions.Action != "reset" || got.Permissions.Method != "Browser.resetPermissions" || !got.Permissions.BrowserScoped || !got.Permissions.ResetAllOrigins || len(got.Permissions.Warnings) == 0 {
		t.Fatalf("permissions reset output = %+v, want reset metadata and scope warning", got)
	}
	if len(got.NextCommands) == 0 || !strings.Contains(got.NextCommands[0], "permissions grant") {
		t.Fatalf("next_commands = %v, want grant example", got.NextCommands)
	}
}
