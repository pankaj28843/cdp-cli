package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestCoreObservationCommandsSelectPageByTargetIndex(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "eval", args: []string{"eval", "document.title"}},
		{name: "observe", args: []string{"observe", "--selector", "article"}},
		{name: "text", args: []string{"text", "article"}},
		{name: "html", args: []string{"html", "article"}},
		{name: "snapshot", args: []string{"snapshot", "--selector", "article"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{}, test.args...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s target-index exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s target-index output: %v; output=%s", test.name, err, out.String())
			}
			target, ok := result["target"].(map[string]any)
			if !ok || result["ok"] != true || target["id"] != "page-two" {
				t.Fatalf("%s target-index result = %#v, want page-two target evidence", test.name, result)
			}
			if result["target_index"] != float64(2) {
				t.Fatalf("%s target-index evidence = %#v, want 2", test.name, result["target_index"])
			}
		})
	}
}

func TestCoreObservationTargetIndexRetriesSelection(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{name: "eval", args: []string{"eval", "document.title"}},
		{name: "text", args: []string{"text", "article"}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second", "fakeRuntimeEvaluateErrorOnce": true},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append([]string{}, command.args...)
			args = append(args, "--target-index", "2", "--retry", "transient", "--max-attempts", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s indexed retry exit=%d stdout=%s stderr=%s", command.name, code, out.String(), errOut.String())
			}

			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode %s indexed retry output: %v; output=%s", command.name, err, out.String())
			}
			target, ok := result["target"].(map[string]any)
			attempts, attemptsOK := result["attempts"].([]any)
			if !ok || !attemptsOK || len(attempts) != 2 || result["ok"] != true || target["id"] != "page-two" || result["target_index"] != float64(2) || result["attempt_count"] != float64(2) {
				t.Fatalf("%s indexed retry result = %#v, want page-two, index 2, and two attempts", command.name, result)
			}
			firstAttempt, ok := attempts[0].(map[string]any)
			if !ok || firstAttempt["retry"] != true || firstAttempt["code"] != "connection_failed" {
				t.Fatalf("%s indexed retry first attempt = %#v, want retryable connection failure", command.name, firstAttempt)
			}
		})
	}
}

func TestCoreObservationCommandsRejectInvalidTargetIndex(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{name: "eval", args: []string{"eval", "document.title"}},
		{name: "observe", args: []string{"observe"}},
		{name: "text", args: []string{"text", "article"}},
		{name: "html", args: []string{"html", "article"}},
		{name: "snapshot", args: []string{"snapshot"}},
	}
	for _, command := range commands {
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

func TestCoreObservationCommandsRejectTargetIndexSelectorConflicts(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{name: "eval", args: []string{"eval", "document.title"}},
		{name: "observe", args: []string{"observe"}},
		{name: "text", args: []string{"text", "article"}},
		{name: "html", args: []string{"html", "article"}},
		{name: "snapshot", args: []string{"snapshot"}},
	}
	selectors := [][]string{
		{"--target", "page-one"},
		{"--url-contains", "example.test"},
		{"--title-contains", "First"},
	}
	for _, command := range commands {
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

func TestCoreObservationCommandsReportOutOfRangeTargetIndex(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{name: "eval", args: []string{"eval", "document.title"}},
		{name: "observe", args: []string{"observe"}},
		{name: "text", args: []string{"text", "article"}},
		{name: "html", args: []string{"html", "article"}},
		{name: "snapshot", args: []string{"snapshot"}},
	}
	for _, command := range commands {
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

func assertTargetIndexError(t *testing.T, data []byte, wantCode string) {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode target-index error: %v; output=%s", err, data)
	}
	if result["ok"] != false || result["code"] != wantCode {
		t.Fatalf("target-index error = %#v, want %s", result, wantCode)
	}
}
