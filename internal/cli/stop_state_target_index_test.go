package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestStopStateClassifyExposesTargetIndexContract(t *testing.T) {
	var describeOut, describeErr bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "stop-state classify", "--json"}, &describeOut, &describeErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe stop-state classify exit=%d stdout=%s stderr=%s", code, describeOut.String(), describeErr.String())
	}
	var described struct {
		Commands struct {
			Flags    []struct{ Name, Type string } `json:"flags"`
			Examples []string                      `json:"examples"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(describeOut.Bytes(), &described); err != nil {
		t.Fatalf("decode describe output: %v; output=%s", err, describeOut.String())
	}
	foundIndex := false
	for _, flag := range described.Commands.Flags {
		if flag.Name == "target-index" {
			foundIndex = true
			if flag.Type != "int" {
				t.Fatalf("target-index type=%q, want int", flag.Type)
			}
		}
	}
	foundExample := false
	for _, example := range described.Commands.Examples {
		if strings.Contains(example, "--target-index 2") {
			foundExample = true
			break
		}
	}
	if !foundIndex || !foundExample {
		t.Fatalf("describe stop-state classify missing indexed contract: %+v", described.Commands)
	}

	var schemaOut, schemaErr bytes.Buffer
	code = cli.Execute(context.Background(), []string{"schema", "stop-state-classify", "--json"}, &schemaOut, &schemaErr, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("schema stop-state-classify exit=%d stdout=%s stderr=%s", code, schemaOut.String(), schemaErr.String())
	}
	var schema struct {
		Schema struct {
			Description string `json:"description"`
			Fields      []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"fields"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(schemaOut.Bytes(), &schema); err != nil {
		t.Fatalf("decode schema output: %v; output=%s", err, schemaOut.String())
	}
	if !bytes.Contains([]byte(schema.Schema.Description), []byte("target-index")) {
		t.Fatalf("schema description=%q, want target-index contract", schema.Schema.Description)
	}
	for _, field := range schema.Schema.Fields {
		if field.Name == "target_index" {
			if field.Type != "integer" {
				t.Fatalf("target_index type=%q, want integer", field.Type)
			}
			return
		}
	}
	t.Fatalf("schema stop-state-classify missing target_index: %s", schemaOut.String())
}

func TestStopStateClassifyRejectsInvalidAndConflictingTargetIndex(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "zero", args: []string{"--target-index", "0"}, want: "invalid_target_index"},
		{name: "negative", args: []string{"--target-index", "-1"}, want: "invalid_target_index"},
		{name: "target conflict", args: []string{"--target-index", "1", "--target", "page-one"}, want: "invalid_target_selector"},
		{name: "url conflict", args: []string{"--target-index", "1", "--url-contains", "example.test"}, want: "invalid_target_selector"},
		{name: "title conflict", args: []string{"--target-index", "1", "--title-contains", "Example"}, want: "invalid_target_selector"},
		{name: "offline input", args: []string{"--target-index", "1", "--text", "Please sign in to continue."}, want: "invalid_target_selector"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"stop-state", "classify"}, tt.args...)
			args = append(args, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s exit=%d stdout=%s stderr=%s", tt.name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), tt.want)
		})
	}
}

func TestStopStateClassifySelectsIndexedPageAndPreservesSafeOutput(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"stop-state", "classify", "--target-index", "2", "--state-dir", stateDir, "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("indexed stop-state exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode indexed stop-state output: %v; output=%s", err, out.String())
	}
	target, ok := result["target"].(map[string]any)
	if !ok || result["ok"] != true || result["status"] != "blocked" || result["stop_state"] != "login_required" || target["id"] != "page-two" || result["target_index"] != float64(2) {
		t.Fatalf("indexed stop-state result=%#v, want page-two/index 2 login-required classification", result)
	}
	if _, leaked := target["text"]; leaked {
		t.Fatalf("target metadata unexpectedly contains page text: %#v", target)
	}
	if result["input"].(map[string]any)["text_bytes"] != float64(len("Please sign in to continue.")) {
		t.Fatalf("indexed stop-state input summary=%#v, want bounded byte count", result["input"])
	}
}

func TestStopStateClassifyReportsOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"stop-state", "classify", "--target-index", "2", "--state-dir", stateDir, "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("out-of-range stop-state exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	assertTargetIndexError(t, out.Bytes(), "target_not_found")
}
