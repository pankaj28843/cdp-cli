package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestEventsTapSelectsPageByTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
		{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
		{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"events", "tap", "--target-index", "2", "--enable", "network",
		"--match", "Network.requestWillBeSent", "--duration", "1s", "--max-events", "1", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("events tap target-index exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result struct {
		OK     bool `json:"ok"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Events []struct {
			SessionID string `json:"sessionId"`
		} `json:"events"`
		Tap struct {
			TargetIndex          int  `json:"target_index"`
			SessionBound         bool `json:"session_bound"`
			ForeignEventsDropped int  `json:"foreign_events_dropped"`
		} `json:"tap"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode events tap target-index output: %v; output=%s", err, out.String())
	}
	if !result.OK || result.Target.ID != "page-two" || result.Tap.TargetIndex != 2 || !result.Tap.SessionBound || len(result.Events) != 1 || result.Events[0].SessionID != "session-page-two" {
		t.Fatalf("events tap target-index result = %+v, want page-two/session-page-two evidence", result)
	}
}

func TestEventsTapRejectsInvalidTargetIndex(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{
				"events", "tap", "--target-index", value, "--json",
			}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("events tap target-index %s exit=%d stdout=%s stderr=%s", value, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode events tap target-index %s error: %v; output=%s", value, err, out.String())
			}
			if result["ok"] != false || result["code"] != "invalid_target_index" {
				t.Fatalf("events tap target-index %s error = %#v, want invalid_target_index", value, result)
			}
		})
	}
}

func TestEventsTapRejectsTargetIndexSelectorConflicts(t *testing.T) {
	for _, extra := range [][]string{
		{"--target", "page-one"},
		{"--url-contains", "example.test"},
		{"--title-contains", "First"},
	} {
		name := strings.Join(extra, "-")
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			args := []string{"events", "tap", "--target-index", "1"}
			args = append(args, extra...)
			args = append(args, "--json")
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("events tap target-index conflict exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode events tap target-index conflict error: %v; output=%s", err, out.String())
			}
			message, _ := result["message"].(string)
			if result["ok"] != false || result["code"] != "invalid_target_selector" || !strings.Contains(message, "--target-index") {
				t.Fatalf("events tap target-index conflict error = %#v, want invalid_target_selector", result)
			}
		})
	}
}

func TestEventsTapReportsOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "only-page",
		"type":     "page",
		"title":    "Only page",
		"url":      "https://example.test/only",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"events", "tap", "--target-index", "2", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("events tap out-of-range target-index exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode events tap out-of-range error: %v; output=%s", err, out.String())
	}
	message, _ := result["message"].(string)
	if result["ok"] != false || result["code"] != "target_not_found" || !strings.Contains(message, "page target index 2") {
		t.Fatalf("events tap out-of-range error = %#v, want target_not_found", result)
	}
}
