package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestLocatorFindByLabelJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"locator", "find", "Search", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("locator find exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK      bool `json:"ok"`
		Locator struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Count   int    `json:"count"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Tag          string `json:"tag"`
				Role         string `json:"role"`
				Name         string `json:"name"`
				Visible      bool   `json:"visible"`
			} `json:"matches"`
		} `json:"locator"`
		Matches []struct {
			SelectorHint string `json:"selector_hint"`
		} `json:"matches"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("locator find output is invalid JSON: %v", err)
	}
	if !got.OK || got.Locator.By != "label" || got.Locator.Query != "Search" || got.Locator.Count != 1 || !got.Locator.Strict || len(got.Locator.Matches) != 1 {
		t.Fatalf("locator find = %+v, want one strict label match", got)
	}
	match := got.Locator.Matches[0]
	if match.SelectorHint != "input#q" || match.Tag != "input" || match.Role != "searchbox" || match.Name != "Search" || !match.Visible {
		t.Fatalf("locator match = %+v, want search input metadata", match)
	}
	if len(got.Matches) != 1 || got.Matches[0].SelectorHint != "input#q" {
		t.Fatalf("top-level matches = %+v, want jq-friendly duplicate", got.Matches)
	}
	if len(got.NextCommands) == 0 || !containsSubstring(got.NextCommands, "cdp fill 'input#q' <value> --json") {
		t.Fatalf("next commands = %+v, want fill suggestion", got.NextCommands)
	}
}

func TestLocatorFindRoleRequiresRoleFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"locator", "find", "Submit", "--by", "role", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("locator role exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "--role is required") {
		t.Fatalf("locator role error = %s, want --role guidance", out.String())
	}
}

func containsSubstring(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
