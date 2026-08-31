package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestPerformanceMemoryCommandsSelectPageByTargetIndex(t *testing.T) {
	commands := performanceMemoryTargetIndexCommands(t)
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
				t.Fatalf("%s target-index exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s target-index output: %v; output=%s", command.name, err, out.String())
			}
			target, ok := result["target"].(map[string]any)
			if !ok || result["ok"] != true || target["id"] != "page-two" || result["target_index"] != float64(2) {
				t.Fatalf("%s target-index result = %#v, want page-two and index 2", command.name, result)
			}
			if _, ok := result[command.outputField]; !ok {
				t.Fatalf("%s target-index result = %#v, want command output field %q", command.name, result, command.outputField)
			}
			if command.artifactPath != "" {
				artifact, ok := result["artifact"].(map[string]any)
				if !ok || artifact["path"] != command.artifactPath || artifact["bytes"] == nil {
					t.Fatalf("%s target-index artifact = %#v, want path %q and bytes", command.name, result["artifact"], command.artifactPath)
				}
				if _, err := os.Stat(command.artifactPath); err != nil {
					t.Fatalf("%s target-index artifact was not written: %v", command.name, err)
				}
			}
		})
	}
}

func TestPerformanceMemoryCommandsRejectInvalidTargetIndex(t *testing.T) {
	for _, command := range performanceMemoryTargetIndexCommands(t) {
		for _, value := range []string{"0", "-1"} {
			t.Run(command.name+"/"+value, func(t *testing.T) {
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

func TestPerformanceMemoryCommandsRejectTargetIndexSelectorConflicts(t *testing.T) {
	selectors := [][]string{
		{"--target", "page-one"},
		{"--url-contains", "example.test"},
		{"--title-contains", "First"},
	}
	for _, command := range performanceMemoryTargetIndexCommands(t) {
		for _, selector := range selectors {
			name := command.name + "/" + strings.Join(selector, "-")
			t.Run(name, func(t *testing.T) {
				args := append([]string{}, command.args...)
				args = append(args, "--target-index", "1")
				args = append(args, selector...)
				args = append(args, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s conflict exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
			})
		}
	}
}

func TestPerformanceMemoryCommandsReportOutOfRangeTargetIndex(t *testing.T) {
	for _, command := range performanceMemoryTargetIndexCommands(t) {
		t.Run(command.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{{
				"targetId": "only-page",
				"type":     "page",
				"title":    "Only page",
				"url":      "https://example.test/only",
			}})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

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

type performanceMemoryTargetIndexCommand struct {
	name         string
	args         []string
	outputField  string
	artifactPath string
}

func performanceMemoryTargetIndexCommands(t *testing.T) []performanceMemoryTargetIndexCommand {
	t.Helper()
	heapPath := filepath.Join(t.TempDir(), "page.heapsnapshot")
	return []performanceMemoryTargetIndexCommand{
		{name: "perf-summary", args: []string{"perf", "summary", "--duration", "0s"}, outputField: "metrics"},
		{name: "memory-counters", args: []string{"memory", "counters"}, outputField: "memory"},
		{name: "memory-heap-snapshot", args: []string{"memory", "heap-snapshot", "--out", heapPath}, outputField: "artifact", artifactPath: heapPath},
	}
}
