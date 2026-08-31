package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestSourceAttachStopNumericTargetSelectsPageIndex(t *testing.T) {
	t.Run("events stream", func(t *testing.T) {
		server := newFakeCDPServer(t, []map[string]any{
			{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
			{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
			{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
		})
		defer server.Close()
		startFakeDaemon(t, server, "browser_url")

		var out, errOut bytes.Buffer
		code := cli.ExecuteWithInput(context.Background(), []string{
			"events", "stream", "--target", "2", "--enable", "runtime",
			"--match", "Runtime.consoleAPICalled", "--max-events", "1", "--json",
		}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("numeric stream target exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		records := decodeJSONLines(t, out.String())
		target, ok := records[0]["target"].(map[string]any)
		if !ok || target["id"] != "page-two" {
			t.Fatalf("numeric stream target=%#v, want page-two", records[0]["target"])
		}
		stream, ok := records[0]["stream"].(map[string]any)
		if !ok || stream["target_index"] != float64(2) {
			t.Fatalf("numeric stream metadata=%#v, want target_index 2", records[0]["stream"])
		}
	})

	t.Run("events interactions", func(t *testing.T) {
		server := newFakeCDPServer(t, []map[string]any{
			{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
			{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
			{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
		})
		defer server.Close()
		startFakeDaemon(t, server, "browser_url")

		var out, errOut bytes.Buffer
		code := cli.ExecuteWithInput(context.Background(), []string{
			"events", "interactions", "--target", "2", "--match", "click",
			"--max-events", "1", "--json",
		}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("numeric interactions target exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		records := decodeJSONLines(t, out.String())
		target, ok := records[0]["target"].(map[string]any)
		if !ok || target["id"] != "page-two" {
			t.Fatalf("numeric interactions target=%#v, want page-two", records[0]["target"])
		}
		observer, ok := records[0]["observer"].(map[string]any)
		if !ok || observer["target_index"] != float64(2) {
			t.Fatalf("numeric interactions metadata=%#v, want target_index 2", records[0]["observer"])
		}
	})

	t.Run("page close", func(t *testing.T) {
		server := newFakeCDPServer(t, []map[string]any{
			{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
			{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
			{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
		})
		defer server.Close()
		stateDir := startFakeDaemon(t, server, "browser_url")

		var out, errOut bytes.Buffer
		code := cli.Execute(context.Background(), []string{
			"page", "close", "--target", "2", "--state-dir", stateDir, "--json",
		}, &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("numeric close target exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		var result struct {
			OK         bool `json:"ok"`
			TargetGone bool `json:"target_gone"`
			Target     struct {
				ID string `json:"id"`
			} `json:"target"`
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("numeric close output is invalid JSON: %v; output=%s", err, out.String())
		}
		if !result.OK || !result.TargetGone || result.Target.ID != "page-two" {
			t.Fatalf("numeric close result=%+v, want settled page-two close", result)
		}
	})
}

func TestSourceAttachStopNumericTargetKeepsExplicitIDAndRejectsIndexFallback(t *testing.T) {
	t.Run("explicit numeric target id", func(t *testing.T) {
		server := newFakeCDPServer(t, []map[string]any{
			{"targetId": "2AAA0000000000000000000000000000", "type": "page", "title": "Numeric ID", "url": "https://example.test/numeric-id"},
			{"targetId": "BBBB0000000000000000000000000000", "type": "page", "title": "Second", "url": "https://example.test/second"},
		})
		defer server.Close()
		stateDir := startFakeDaemon(t, server, "browser_url")

		var out, errOut bytes.Buffer
		code := cli.Execute(context.Background(), []string{
			"page", "close", "--target-id", "2", "--state-dir", stateDir, "--json",
		}, &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("explicit numeric ID exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		var result struct {
			Target struct {
				ID string `json:"id"`
			} `json:"target"`
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("explicit numeric ID output is invalid JSON: %v; output=%s", err, out.String())
		}
		if result.Target.ID != "2AAA0000000000000000000000000000" {
			t.Fatalf("explicit numeric ID selected %q, want numeric ID target", result.Target.ID)
		}
	})

	t.Run("out of range does not fall back to ID prefix", func(t *testing.T) {
		server := newFakeCDPServer(t, []map[string]any{
			{"targetId": "3AAA0000000000000000000000000000", "type": "page", "title": "Numeric ID", "url": "https://example.test/numeric-id"},
			{"targetId": "BBBB0000000000000000000000000000", "type": "page", "title": "Second", "url": "https://example.test/second"},
		})
		defer server.Close()
		stateDir := startFakeDaemon(t, server, "browser_url")

		var out, errOut bytes.Buffer
		code := cli.Execute(context.Background(), []string{
			"page", "close", "--target", "3", "--state-dir", stateDir, "--json",
		}, &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitUsage {
			t.Fatalf("out-of-range numeric target exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("out-of-range numeric target output is invalid JSON: %v; output=%s", err, out.String())
		}
		message, _ := result["message"].(string)
		if result["ok"] != false || result["code"] != "target_not_found" || !strings.Contains(message, "page target index 3") {
			t.Fatalf("out-of-range numeric target error=%#v, want target_not_found index evidence", result)
		}
	})
}
