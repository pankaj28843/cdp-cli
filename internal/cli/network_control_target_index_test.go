package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestNetworkControlsExposeTargetIndex(t *testing.T) {
	for _, command := range []struct {
		name   string
		args   []string
		schema string
	}{
		{name: "block", args: []string{"network", "block", "--pattern", "*://*/analytics/*", "--duration", "10s"}, schema: "network-block"},
		{name: "mock", args: []string{"network", "mock", "--rule", `{"url_pattern":"*://*/api/config","status":200,"body":"ok"}`, "--duration", "10s"}, schema: "network-mock"},
	} {
		t.Run(command.name, func(t *testing.T) {
			var describeOut, describeErr bytes.Buffer
			code := cli.Execute(context.Background(), []string{"describe", "--command", "network", command.name, "--json"}, &describeOut, &describeErr, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("describe network %s exit=%d stdout=%s stderr=%s", command.name, code, describeOut.String(), describeErr.String())
			}
			var describe struct {
				OK       bool `json:"ok"`
				Commands struct {
					Flags []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"flags"`
					Examples []string `json:"examples"`
				} `json:"commands"`
			}
			if err := json.Unmarshal(describeOut.Bytes(), &describe); err != nil {
				t.Fatalf("describe network %s output is invalid JSON: %v; output=%s", command.name, err, describeOut.String())
			}
			foundFlag := false
			for _, flag := range describe.Commands.Flags {
				if flag.Name == "target-index" && flag.Type == "int" {
					foundFlag = true
					break
				}
			}
			if !describe.OK || !foundFlag || !examplesContainTargetIndex(describe.Commands.Examples) {
				t.Fatalf("describe network %s = %+v, want integer target-index flag and example", command.name, describe)
			}

			var schemaOut, schemaErr bytes.Buffer
			code = cli.Execute(context.Background(), []string{"schema", command.schema, "--json"}, &schemaOut, &schemaErr, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("schema %s exit=%d stdout=%s stderr=%s", command.schema, code, schemaOut.String(), schemaErr.String())
			}
			var schema struct {
				OK     bool `json:"ok"`
				Schema struct {
					Name   string `json:"name"`
					Fields []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"fields"`
				} `json:"schema"`
			}
			if err := json.Unmarshal(schemaOut.Bytes(), &schema); err != nil {
				t.Fatalf("schema %s output is invalid JSON: %v; output=%s", command.schema, err, schemaOut.String())
			}
			foundField := false
			for _, field := range schema.Schema.Fields {
				if field.Name == "target_index" && field.Type == "integer" {
					foundField = true
					break
				}
			}
			if !schema.OK || schema.Schema.Name != command.schema || !foundField {
				t.Fatalf("schema %s = %+v, want integer target_index field", command.schema, schema)
			}
		})
	}
}

func TestNetworkControlsRejectInvalidTargetIndexBeforeConnection(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{name: "block", args: []string{"network", "block", "--pattern", "*://*/analytics/*", "--duration", "10s"}},
		{name: "mock", args: []string{"network", "mock", "--rule", `{"url_pattern":"*://*/api/config","status":200,"body":"ok"}`, "--duration", "10s"}},
	}
	for _, command := range commands {
		for _, test := range []struct {
			name  string
			value string
			code  string
		}{
			{name: "zero", value: "0", code: "invalid_target_index"},
			{name: "negative", value: "-1", code: "invalid_target_index"},
			{name: "target conflict", value: "1", code: "invalid_target_selector"},
			{name: "url filter conflict", value: "1", code: "invalid_target_selector"},
			{name: "title filter conflict", value: "1", code: "invalid_target_selector"},
		} {
			t.Run(fmt.Sprintf("%s/%s", command.name, test.name), func(t *testing.T) {
				args := append([]string{}, command.args...)
				args = append(args, "--target-index", test.value)
				switch test.name {
				case "target conflict":
					args = append(args, "--target", "page-one")
				case "url filter conflict":
					args = append(args, "--url-contains", "example.test")
				case "title filter conflict":
					args = append(args, "--title-contains", "First")
				}
				args = append(args, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("network %s %s exit=%d stdout=%s stderr=%s", command.name, test.name, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), test.code)
			})
		}
	}
}

func TestNetworkControlsSelectExistingPageByTargetIndex(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{name: "block", args: []string{"network", "block", "--pattern", "*://*/analytics/*", "--duration", "50ms"}},
		{name: "mock", args: []string{"network", "mock", "--rule", `{"url_pattern":"*://*/api/config","method":"GET","status":200,"body":"{\"enabled\":true}","max_matches":1}`, "--duration", "250ms"}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{}, command.args...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("network %s target-index exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			var report struct {
				OK     bool `json:"ok"`
				Target struct {
					ID string `json:"id"`
				} `json:"target"`
				TargetIndex int `json:"target_index"`
				Matched     int `json:"matched_count"`
				Cleanup     struct {
					Complete       bool `json:"complete"`
					FetchDisabled  bool `json:"fetch_disabled"`
					NetworkDisable bool `json:"network_disabled"`
				} `json:"cleanup"`
				Actions map[string]int `json:"actions"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("network %s target-index output is invalid JSON: %v; output=%s", command.name, err, out.String())
			}
			if !report.OK || report.Target.ID != "page-two" || report.TargetIndex != 2 || !report.Cleanup.Complete {
				t.Fatalf("network %s target-index report = %+v, want page-two, index 2, and complete cleanup", command.name, report)
			}
			if command.name == "block" {
				if report.Matched != 1 || !report.Cleanup.NetworkDisable {
					t.Fatalf("network block target-index report = %+v, want one blocked request and Network disabled", report)
				}
			} else if report.Matched != 1 || report.Actions["fulfilled"] != 1 || report.Actions["continued"] != 1 || !report.Cleanup.FetchDisabled {
				t.Fatalf("network mock target-index report = %+v, want fulfilled/continued actions and Fetch disabled", report)
			}
			if strings.Contains(out.String(), `"enabled":true`) {
				t.Fatalf("network %s JSON leaked mock response body: %s", command.name, out.String())
			}
			if pages := fakePagesCount(t); pages != 2 {
				t.Fatalf("network %s target-index page count=%d, want existing pages preserved", command.name, pages)
			}
		})
	}
}

func TestNetworkControlsReportOutOfRangeTargetIndex(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{name: "block", args: []string{"network", "block", "--pattern", "*://*/analytics/*", "--duration", "50ms"}},
		{name: "mock", args: []string{"network", "mock", "--rule", `{"url_pattern":"*://*/api/config","status":200,"body":"ok"}`, "--duration", "50ms"}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "only-page", "type": "page", "title": "Only page", "url": "https://example.test/only",
			}})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")
			args := append([]string{}, command.args...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("network %s out-of-range exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}
