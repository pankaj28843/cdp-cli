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

func TestScreenshotSelectsPageByTargetIndex(t *testing.T) {
	for _, test := range []struct {
		name     string
		navigate string
	}{
		{name: "capture", navigate: ""},
		{name: "capture after navigate", navigate: "https://example.test/next"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")
			outPath := filepath.Join(t.TempDir(), "indexed.png")

			args := []string{"screenshot", "--out", outPath, "--target-index", "2"}
			if test.navigate != "" {
				args = append(args, "--navigate", test.navigate, "--wait", "0s")
			}
			args = append(args, "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("screenshot target-index exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			var result struct {
				OK     bool `json:"ok"`
				Target struct {
					ID string `json:"id"`
				} `json:"target"`
				TargetIndex int `json:"target_index"`
				Screenshot  struct {
					Path     string `json:"path"`
					Bytes    int    `json:"bytes"`
					Navigate struct {
						URL string `json:"url"`
					} `json:"navigate"`
				} `json:"screenshot"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode screenshot target-index output: %v; output=%s", err, out.String())
			}
			if !result.OK || result.Target.ID != "page-two" || result.TargetIndex != 2 || result.Screenshot.Path != outPath || result.Screenshot.Bytes != len("synthetic screenshot") {
				t.Fatalf("screenshot target-index result = %+v, want page-two, index 2, and artifact metadata", result)
			}
			if result.Screenshot.Navigate.URL != test.navigate {
				t.Fatalf("screenshot target-index navigate URL = %q, want %q", result.Screenshot.Navigate.URL, test.navigate)
			}
			data, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("read indexed screenshot artifact: %v", err)
			}
			if string(data) != "synthetic screenshot" {
				t.Fatalf("indexed screenshot artifact = %q, want synthetic screenshot", string(data))
			}
		})
	}
}

func TestScreenshotRejectsInvalidTargetIndex(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "invalid.png")
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"screenshot", "--out", outPath, "--target-index", value, "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("screenshot target-index %s exit=%d stdout=%s stderr=%s", value, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
		})
	}
}

func TestScreenshotRejectsTargetIndexSelectorConflicts(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "conflict.png")
	for _, selector := range [][]string{
		{"--target", "page-one"},
		{"--url-contains", "example.test"},
		{"--title-contains", "First"},
	} {
		name := strings.Join(selector, "-")
		t.Run(name, func(t *testing.T) {
			args := []string{"screenshot", "--out", outPath, "--target-index", "1"}
			args = append(args, selector...)
			args = append(args, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("screenshot conflict exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
		})
	}
}

func TestScreenshotReportsOutOfRangeTargetIndex(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{{
		"targetId": "only-page",
		"type":     "page",
		"title":    "Only page",
		"url":      "https://example.test/only",
	}})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")
	outPath := filepath.Join(t.TempDir(), "range.png")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"screenshot", "--out", outPath, "--target-index", "2", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("screenshot out-of-range exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	assertTargetIndexError(t, out.Bytes(), "target_not_found")
}
