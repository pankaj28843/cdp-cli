package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestWorkflowGoogleMapsDirectionsJSONClosesExactOwnedTarget(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "google-maps-directions", "Hvidegaard Møn", "Møn Is", "--wait", "0", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
		Trust  struct {
			Level string `json:"level"`
		} `json:"trust"`
		Routes []struct {
			DurationMinutes int     `json:"duration_minutes"`
			DistanceKM      float64 `json:"distance_km"`
		} `json:"routes"`
		Evidence struct {
			TargetID     string `json:"target_id"`
			AttemptCount int    `json:"attempt_count"`
			Bounded      bool   `json:"bounded"`
		} `json:"evidence"`
		Cleanup struct {
			Attempted bool   `json:"attempted"`
			Closed    bool   `json:"closed"`
			TargetID  string `json:"target_id"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !got.OK || got.Status != "success" || got.Trust.Level != "trusted" || len(got.Routes) != 2 {
		t.Fatalf("result=%+v", got)
	}
	if !got.Evidence.Bounded || got.Evidence.AttemptCount != 1 || got.Evidence.TargetID != "created-page" {
		t.Fatalf("evidence=%+v", got.Evidence)
	}
	if !got.Cleanup.Attempted || !got.Cleanup.Closed || got.Cleanup.TargetID != got.Evidence.TargetID {
		t.Fatalf("cleanup=%+v evidence=%+v", got.Cleanup, got.Evidence)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK || strings.Contains(out.String(), "created-page") {
		t.Fatalf("owned target leaked: exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

func TestWorkflowGoogleMapsDirectionsCleanupWaitsForTargetGone(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId":             "delayed-close-sentinel",
		"type":                 "page",
		"title":                "Sentinel",
		"url":                  "https://example.test/sentinel",
		"fakeCloseTargetDelay": true,
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "google-maps-directions", "Hvidegaard Møn", "Møn Is", "--wait", "0", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("delayed Google Maps cleanup exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		Cleanup struct {
			Closed       bool `json:"closed"`
			TargetGone   bool `json:"target_gone"`
			AttemptCount int  `json:"attempt_count"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode delayed Google Maps cleanup: %v", err)
	}
	if !got.Cleanup.Closed || !got.Cleanup.TargetGone || got.Cleanup.AttemptCount < 1 {
		t.Fatalf("delayed Google Maps cleanup=%+v", got.Cleanup)
	}
	if count := fakePagesCount(t); count != 1 {
		t.Fatalf("delayed Google Maps cleanup page count=%d, want baseline 1", count)
	}
}

func TestWorkflowGoogleMapsDirectionsCleanupFailureIsNotSuccess(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "sentinel-page", "type": "page", "title": "Sentinel", "url": "https://example.test/keep", "attached": false,
		"fakeCloseTargetError": true,
	}})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "google-maps-directions", "Hvidegaard Møn", "Møn Is", "--wait", "0", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitInternal {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Cleanup struct {
				TargetID string `json:"target_id"`
				Error    string `json:"error"`
			} `json:"cleanup"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.OK || got.Code != "google_maps_cleanup_failed" || got.Data.Cleanup.TargetID != "created-page" || got.Data.Cleanup.Error == "" {
		t.Fatalf("error result=%+v", got)
	}
}

func TestWorkflowGoogleMapsDirectionsRejectsNegativeWaitBeforeBrowser(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "google-maps-directions", "Origin", "Destination", "--wait", "-1s", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Code != "usage" {
		t.Fatalf("output=%s error=%v", out.String(), err)
	}
}
