package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestEventsStreamAcceptsSourceAttachSelectorAliases(t *testing.T) {
	for _, test := range []struct {
		name  string
		flag  string
		value string
	}{
		{name: "target id", flag: "--target-id", value: "stream-alias-page"},
		{name: "url", flag: "--url", value: "stream-alias"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "stream-alias-page",
				"type":     "page",
				"title":    "Stream alias",
				"url":      "https://example.test/stream-alias",
			}})
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.ExecuteWithInput(context.Background(), []string{
				"events", "stream", test.flag, test.value,
				"--enable", "runtime", "--match", "Runtime.consoleAPICalled",
				"--max-events", "1", "--json",
			}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("events stream %s exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			records := decodeJSONLines(t, out.String())
			if len(records) < 1 {
				t.Fatalf("events stream %s returned no records: %s", test.name, out.String())
			}
			target, ok := records[0]["target"].(map[string]any)
			if !ok || target["id"] != "stream-alias-page" {
				t.Fatalf("events stream %s target=%#v, want stream-alias-page", test.name, records[0]["target"])
			}
		})
	}
}

func TestEventsInteractionsAcceptsSourceAttachSelectorAliases(t *testing.T) {
	for _, test := range []struct {
		name  string
		flag  string
		value string
	}{
		{name: "target id", flag: "--target-id", value: "interaction-alias-page"},
		{name: "url", flag: "--url", value: "interaction-alias"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "interaction-alias-page",
				"type":     "page",
				"title":    "Interaction alias",
				"url":      "https://example.test/interaction-alias",
			}})
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.ExecuteWithInput(context.Background(), []string{
				"events", "interactions", test.flag, test.value,
				"--match", "click", "--max-events", "1", "--json",
			}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("events interactions %s exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			records := decodeJSONLines(t, out.String())
			if len(records) < 1 {
				t.Fatalf("events interactions %s returned no records: %s", test.name, out.String())
			}
			target, ok := records[0]["target"].(map[string]any)
			if !ok || target["id"] != "interaction-alias-page" {
				t.Fatalf("events interactions %s target=%#v, want interaction-alias-page", test.name, records[0]["target"])
			}
		})
	}
}

func TestPageCloseAcceptsSourceStopSelectorAliases(t *testing.T) {
	for _, test := range []struct {
		name  string
		flag  string
		value string
	}{
		{name: "target id", flag: "--target-id", value: "close-alias-page"},
		{name: "url", flag: "--url", value: "close-alias"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "close-alias-page",
				"type":     "page",
				"title":    "Close alias",
				"url":      "https://example.test/close-alias",
			}})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{
				"page", "close", test.flag, test.value, "--state-dir", stateDir, "--json",
			}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("page close %s exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			var result struct {
				OK         bool `json:"ok"`
				TargetGone bool `json:"target_gone"`
				Target     struct {
					ID string `json:"id"`
				} `json:"target"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode page close %s output: %v; output=%s", test.name, err, out.String())
			}
			if !result.OK || !result.TargetGone || result.Target.ID != "close-alias-page" {
				t.Fatalf("page close %s result=%+v, want settled close-alias-page", test.name, result)
			}
		})
	}
}

func TestSourceAttachStopSelectorAliasesRejectDuplicateSpellings(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		text string
	}{
		{name: "stream target", args: []string{"events", "stream", "--target", "one", "--target-id", "two", "--json"}, text: "--target and --target-id are aliases"},
		{name: "stream url", args: []string{"events", "stream", "--url-contains", "one", "--url", "two", "--json"}, text: "--url and --url-contains are aliases"},
		{name: "interactions target", args: []string{"events", "interactions", "--target", "one", "--target-id", "two", "--json"}, text: "--target and --target-id are aliases"},
		{name: "interactions url", args: []string{"events", "interactions", "--url-contains", "one", "--url", "two", "--json"}, text: "--url and --url-contains are aliases"},
		{name: "close target", args: []string{"page", "close", "--target", "one", "--target-id", "two", "--json"}, text: "--target and --target-id are aliases"},
		{name: "close url", args: []string{"page", "close", "--url-contains", "one", "--url", "two", "--json"}, text: "--url and --url-contains are aliases"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), test.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("duplicate selector exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
			}
			var failure struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(out.Bytes(), &failure); err != nil {
				t.Fatalf("duplicate selector output is invalid JSON: %v; output=%s", err, out.String())
			}
			if failure.Code != "invalid_target_selector" || !strings.Contains(failure.Message, test.text) {
				t.Fatalf("duplicate selector failure=%+v, want invalid_target_selector containing %q", failure, test.text)
			}
		})
	}
}
