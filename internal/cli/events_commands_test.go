package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestEventsTapIsSessionScopedAndDurationBounded(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "event-tap-page",
		"type":     "page",
		"title":    "Event Tap",
		"url":      "https://example.test/events",
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	readyDir := t.TempDir()
	if err := os.Chmod(readyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	readyFile := filepath.Join(readyDir, "tap.ready.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"events", "tap", "--target", "event-tap-page", "--enable", "network", "--match", "Network.requestWillBeSent", "--duration", "1s", "--max-events", "1", "--ready-file", readyFile, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("events tap exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Events []struct {
			SessionID string `json:"sessionId"`
		} `json:"events"`
		Tap struct {
			SessionBound         bool `json:"session_bound"`
			ForeignEventsDropped int  `json:"foreign_events_dropped"`
		} `json:"tap"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode events tap: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].SessionID != "session-event-tap-page" || !got.Tap.SessionBound || got.Tap.ForeignEventsDropped != 1 {
		t.Fatalf("events tap = %+v, want only attached session and one dropped foreign event", got)
	}

	out.Reset()
	errOut.Reset()
	started := time.Now()
	code = cli.Execute(context.Background(), []string{"events", "tap", "--target", "event-tap-page", "--enable", "page", "--match", "Never.emitted", "--duration", "75ms", "--max-events", "0", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("idle events tap exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("idle events tap elapsed %s, want duration-bound exit", elapsed)
	}
}
