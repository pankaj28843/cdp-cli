package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestAssertCommandsExposeTargetIndex(t *testing.T) {
	commands := []string{
		"value", "text", "url", "title", "count", "attribute", "class",
		"focused", "css", "role", "name", "aria-snapshot", "attached",
		"detached", "visible", "hidden", "in-viewport", "enabled", "disabled",
		"editable", "readonly", "checked", "unchecked", "indeterminate",
	}
	for _, name := range commands {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"describe", "--command", "assert " + name, "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("describe assert %s exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			var report struct {
				OK       bool `json:"ok"`
				Commands struct {
					Name  string `json:"name"`
					Flags []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"flags"`
				} `json:"commands"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("describe assert %s output is invalid JSON: %v; output=%s", name, err, out.String())
			}
			if !report.OK || report.Commands.Name != name {
				t.Fatalf("describe assert %s = %+v, want command metadata", name, report)
			}
			found := false
			for _, flag := range report.Commands.Flags {
				if flag.Name == "target-index" && flag.Type == "int" {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("describe assert %s flags = %+v, want integer target-index", name, report.Commands.Flags)
			}
		})
	}
}

func TestAssertCommandsRejectInvalidTargetIndexBeforeAttachment(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{name: "value", args: []string{"assert", "value", "input#q", "hello"}},
		{name: "text", args: []string{"assert", "text", "Ready"}},
		{name: "url", args: []string{"assert", "url", "https://example.test/app"}},
		{name: "title", args: []string{"assert", "title", "Example App"}},
		{name: "count", args: []string{"assert", "count", "main", "1"}},
		{name: "attribute", args: []string{"assert", "attribute", "button", "data-state", "ready"}},
		{name: "class", args: []string{"assert", "class", "button", "ready"}},
		{name: "focused", args: []string{"assert", "focused", "input"}},
		{name: "css", args: []string{"assert", "css", "button", "color", "red"}},
		{name: "role", args: []string{"assert", "role", "button", "button"}},
		{name: "name", args: []string{"assert", "name", "button", "button"}},
		{name: "aria-snapshot", args: []string{"assert", "aria-snapshot", "--expected", "expected"}},
		{name: "attached", args: []string{"assert", "attached", "main"}},
		{name: "detached", args: []string{"assert", "detached", "main"}},
		{name: "visible", args: []string{"assert", "visible", "main"}},
		{name: "hidden", args: []string{"assert", "hidden", "main"}},
		{name: "in-viewport", args: []string{"assert", "in-viewport", "main"}},
		{name: "enabled", args: []string{"assert", "enabled", "main"}},
		{name: "disabled", args: []string{"assert", "disabled", "main"}},
		{name: "editable", args: []string{"assert", "editable", "main"}},
		{name: "readonly", args: []string{"assert", "readonly", "main"}},
		{name: "checked", args: []string{"assert", "checked", "main"}},
		{name: "unchecked", args: []string{"assert", "unchecked", "main"}},
		{name: "indeterminate", args: []string{"assert", "indeterminate", "main"}},
	}
	for _, test := range commands {
		t.Run(test.name, func(t *testing.T) {
			args := append(append([]string{}, test.args...), "--target-index", "0", "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s invalid target-index exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
		})
	}
}

func TestAssertRepresentativeCommandsSelectPageByTargetIndex(t *testing.T) {
	commands := []struct {
		name  string
		args  []string
		check func(t *testing.T, report map[string]any)
	}{
		{
			name: "text",
			args: []string{"assert", "text", "Synthetic main text"},
			check: func(t *testing.T, report map[string]any) {
				assertion := report["assertion"].(map[string]any)
				if assertion["selector"] != "body" || assertion["passed"] != true {
					t.Fatalf("text assertion = %#v, want body assertion", assertion)
				}
			},
		},
		{
			name: "count",
			args: []string{"assert", "count", "main", "1"},
			check: func(t *testing.T, report map[string]any) {
				assertion := report["assertion"].(map[string]any)
				if assertion["selector"] != "main" || assertion["passed"] != true {
					t.Fatalf("count assertion = %#v, want main count assertion", assertion)
				}
			},
		},
		{
			name: "aria-snapshot",
			args: []string{"assert", "aria-snapshot", "--expected", "- button \"Submit\"", "--selector", "body"},
			check: func(t *testing.T, report map[string]any) {
				assertion := report["assertion"].(map[string]any)
				if assertion["selector"] != "body" || assertion["passed"] != true {
					t.Fatalf("aria-snapshot assertion = %#v, want passing snapshot assertion", assertion)
				}
			},
		},
		{
			name: "checked",
			args: []string{"assert", "checked", "Subscribe to newsletter", "--by", "label"},
			check: func(t *testing.T, report map[string]any) {
				assertion := report["assertion"].(map[string]any)
				if assertion["expected"] != "checked" || assertion["passed"] != true {
					t.Fatalf("checked assertion = %#v, want passing checked assertion", assertion)
				}
			},
		},
	}
	for _, test := range commands {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := append(append([]string{}, test.args...), "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s indexed assertion exit=%d stdout=%s stderr=%s", test.name, code, out.String(), errOut.String())
			}
			var report map[string]any
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("%s indexed assertion output is invalid JSON: %v; output=%s", test.name, err, out.String())
			}
			if report["ok"] != true {
				t.Fatalf("%s report = %#v, want ok", test.name, report)
			}
			target := report["target"].(map[string]any)
			if target["id"] != "page-two" || report["target_index"] != float64(2) {
				t.Fatalf("%s target evidence = %#v index=%#v, want page-two/index 2", test.name, target, report["target_index"])
			}
			test.check(t, report)
		})
	}
}

func TestAssertTargetIndexRejectsSelectorConflict(t *testing.T) {
	args := []string{"assert", "text", "Ready", "--target-index", "1", "--url-contains", "example.test", "--json"}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("conflicting target selector exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
}

func TestAssertTargetIndexReportsOutOfRange(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "only-page", "type": "page", "title": "Only page", "url": "https://example.test/only",
	}})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")
	args := []string{"assert", "text", "Ready", "--target-index", "2", "--state-dir", stateDir, "--json"}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("out-of-range target selector exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	assertTargetIndexError(t, out.Bytes(), "target_not_found")
}
