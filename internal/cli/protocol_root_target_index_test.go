package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestBareProtocolTargetUsesSourceNumericIndexInference(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "AAAA0000000000000000000000000000", "type": "page", "title": "First", "url": "https://example.test/first", "attached": false},
		{"targetId": "BBBB0000000000000000000000000000", "type": "page", "title": "Second", "url": "https://example.test/second", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"--target", "2", "Runtime.evaluate", `{"expression":"document.title","returnByValue":true}`, "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("bare numeric target exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("bare numeric target output is invalid JSON: %v; output=%s", err, out.String())
	}
	if got.Target.ID != "BBBB0000000000000000000000000000" {
		t.Fatalf("bare numeric target selected %q, want second page", got.Target.ID)
	}
}

func TestBareNumericTargetIDEscapeHatchAndDirectProtocolSemanticsRemainStable(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "2AAA0000000000000000000000000000", "type": "page", "title": "Numeric ID", "url": "https://example.test/numeric-id", "attached": false},
		{"targetId": "BBBB0000000000000000000000000000", "type": "page", "title": "Second", "url": "https://example.test/second", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "explicit numeric target id",
			args: []string{"--target-id", "2", "Runtime.evaluate", `{"expression":"document.title","returnByValue":true}`, "--json"},
		},
		{
			name: "direct protocol target remains id prefix",
			args: []string{"protocol", "exec", "Runtime.evaluate", "--target", "2", "--params", `{"expression":"document.title","returnByValue":true}`, "--json"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), test.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("numeric target escape hatch exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
			}
			var got struct {
				Target struct {
					ID string `json:"id"`
				} `json:"target"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("numeric target escape output is invalid JSON: %v; output=%s", err, out.String())
			}
			if got.Target.ID != "2AAA0000000000000000000000000000" {
				t.Fatalf("numeric target escape selected %q, want numeric ID target", got.Target.ID)
			}
		})
	}
}
