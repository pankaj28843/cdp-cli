package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

type storageTargetIndexCommand struct {
	name string
	args []string
}

func storageTargetIndexCommands() []storageTargetIndexCommand {
	return []storageTargetIndexCommand{
		{name: "storage list", args: []string{"storage", "list", "--include", "localStorage"}},
		{name: "storage get", args: []string{"storage", "get", "localStorage", "feature"}},
		{name: "storage set", args: []string{"storage", "set", "localStorage", "feature", "disabled"}},
		{name: "storage delete", args: []string{"storage", "delete", "sessionStorage", "nonce"}},
		{name: "storage clear", args: []string{"storage", "clear", "sessionStorage"}},
		{name: "storage snapshot", args: []string{"storage", "snapshot", "--include", "localStorage", "--redact", "safe"}},
		{name: "storage cookies list", args: []string{"storage", "cookies", "list", "--url", "https://example.test/second"}},
		{name: "storage cookies set", args: []string{"storage", "cookies", "set", "--url", "https://example.test/second", "--name", "feature", "--value", "enabled"}},
		{name: "storage cookies delete", args: []string{"storage", "cookies", "delete", "--url", "https://example.test/second", "--name", "feature"}},
		{name: "storage indexeddb list", args: []string{"storage", "indexeddb", "list"}},
		{name: "storage indexeddb get", args: []string{"storage", "indexeddb", "get", "cdp-demo-db", "settings", "feature"}},
		{name: "storage indexeddb put", args: []string{"storage", "indexeddb", "put", "cdp-demo-db", "settings", "feature", `{"enabled":true}`}},
		{name: "storage indexeddb dump", args: []string{"storage", "indexeddb", "dump", "cdp-demo-db", "settings", "--page-size", "2"}},
		{name: "storage indexeddb delete", args: []string{"storage", "indexeddb", "delete", "cdp-demo-db", "settings", "feature"}},
		{name: "storage indexeddb clear", args: []string{"storage", "indexeddb", "clear", "cdp-demo-db", "settings"}},
		{name: "storage cache list", args: []string{"storage", "cache", "list"}},
		{name: "storage cache get", args: []string{"storage", "cache", "get", "app-cache", "https://example.test/api"}},
		{name: "storage cache put", args: []string{"storage", "cache", "put", "app-cache", "https://example.test/api", `{"ok":true}`}},
		{name: "storage cache delete", args: []string{"storage", "cache", "delete", "app-cache", "https://example.test/api"}},
		{name: "storage cache clear", args: []string{"storage", "cache", "clear", "app-cache"}},
		{name: "storage service-workers list", args: []string{"storage", "service-workers", "list"}},
		{name: "storage service-workers unregister", args: []string{"storage", "service-workers", "unregister", "--all"}},
	}
}

func TestStorageCommandsExposeTargetIndex(t *testing.T) {
	for _, command := range storageTargetIndexCommands() {
		t.Run(command.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"describe", "--command", command.name, "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("describe %s exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			var result struct {
				Commands struct {
					Flags []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"flags"`
				} `json:"commands"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode describe %s: %v; output=%s", command.name, err, out.String())
			}
			for _, flag := range result.Commands.Flags {
				if flag.Name == "target-index" {
					if flag.Type != "int" {
						t.Fatalf("describe %s target-index type=%q, want int", command.name, flag.Type)
					}
					return
				}
			}
			t.Fatalf("describe %s did not expose target-index: %s", command.name, out.String())
		})
	}
}

func TestStorageSchemasExposeTargetIndex(t *testing.T) {
	for _, schemaName := range []string{"storage", "storage-cache", "storage-indexeddb", "storage-service-workers", "storage-snapshot"} {
		t.Run(schemaName, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"schema", schemaName, "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("schema %s exit=%d stdout=%s stderr=%s", schemaName, code, out.String(), errOut.String())
			}
			var result struct {
				Schema struct {
					Name   string `json:"name"`
					Fields []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"fields"`
				} `json:"schema"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode schema %s: %v; output=%s", schemaName, err, out.String())
			}
			if result.Schema.Name != schemaName {
				t.Fatalf("schema name=%q, want %q", result.Schema.Name, schemaName)
			}
			for _, field := range result.Schema.Fields {
				if field.Name == "target_index" {
					if field.Type != "integer" {
						t.Fatalf("schema %s target_index type=%q, want integer", schemaName, field.Type)
					}
					return
				}
			}
			t.Fatalf("schema %s did not expose target_index: %s", schemaName, out.String())
		})
	}
}

func TestStorageCommandsRejectInvalidTargetIndexBeforeAttachment(t *testing.T) {
	for _, command := range storageTargetIndexCommands() {
		for _, value := range []string{"0", "-1"} {
			t.Run(fmt.Sprintf("%s/%s", command.name, value), func(t *testing.T) {
				args := append([]string{}, command.args...)
				args = append(args, "--target-index", value, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s target-index %s exit=%d stdout=%s stderr=%s", command.name, value, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
			})
		}
	}
}

func TestStorageCommandsRejectTargetIndexSelectorConflicts(t *testing.T) {
	for _, command := range storageTargetIndexCommands() {
		for _, selector := range []string{"--target", "--url-contains", "--title-contains"} {
			t.Run(fmt.Sprintf("%s/%s", command.name, selector), func(t *testing.T) {
				args := append([]string{}, command.args...)
				args = append(args, "--target-index", "1", selector, "page-one", "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s selector conflict exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
			})
		}
	}
}

func TestStorageCommandsSelectIndexedPageAndPreserveOutput(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	for _, command := range storageTargetIndexCommands() {
		t.Run(command.name, func(t *testing.T) {
			args := append([]string{}, command.args...)
			if command.name == "storage snapshot" {
				args = append(args, "--out", filepath.Join(t.TempDir(), "snapshot.json"))
			}
			if command.name == "storage indexeddb dump" {
				args = append(args, "--out", filepath.Join(t.TempDir(), "dump.json"))
			}
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s target-index exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s target-index: %v; output=%s", command.name, err, out.String())
			}
			target, ok := result["target"].(map[string]any)
			if !ok || result["ok"] != true || target["id"] != "page-two" || result["target_index"] != float64(2) {
				t.Fatalf("%s target-index result=%#v, want page-two/index 2", command.name, result)
			}
		})
	}
}

func TestStorageCommandsReportOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	for _, command := range storageTargetIndexCommands() {
		t.Run(command.name, func(t *testing.T) {
			args := append([]string{}, command.args...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s out-of-range exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}

func TestStorageDiffDoesNotExposeBrowserTargetIndex(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "storage diff", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe storage diff exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if bytes.Contains(out.Bytes(), []byte(`"name":"target-index"`)) {
		t.Fatalf("storage diff unexpectedly exposes browser target selector: %s", out.String())
	}
}
