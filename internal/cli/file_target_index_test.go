package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestFileCommandsExposeTargetIndex(t *testing.T) {
	for _, name := range []string{"file", "file chooser"} {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{"describe", "--command", name, "--json"}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("describe %s exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			var result struct {
				Commands struct {
					Flags []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"flags"`
				} `json:"commands"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode describe %s: %v; output=%s", name, err, out.String())
			}
			for _, flag := range result.Commands.Flags {
				if flag.Name == "target-index" {
					if flag.Type != "int" {
						t.Fatalf("describe %s target-index type=%q, want int", name, flag.Type)
					}
					return
				}
			}
			t.Fatalf("describe %s did not expose target-index: %s", name, out.String())
		})
	}
}

func TestFileCommandsRejectInvalidTargetIndexBeforeAttachment(t *testing.T) {
	uploadPath := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(uploadPath, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatalf("WriteFile upload returned error: %v", err)
	}
	commands := [][]string{
		{"file", "input#upload", uploadPath},
		{"file", "chooser", "247", uploadPath},
	}
	for _, command := range commands {
		for _, value := range []string{"0", "-1"} {
			t.Run(fmt.Sprintf("%s/%s", strings.Join(command[:2], "-"), value), func(t *testing.T) {
				args := append([]string{}, command...)
				args = append(args, "--target-index", value, "--json")
				var out, errOut bytes.Buffer
				code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
				if code != cli.ExitUsage {
					t.Fatalf("%s target-index %s exit=%d stdout=%s stderr=%s", command[0], value, code, out.String(), errOut.String())
				}
				assertTargetIndexError(t, out.Bytes(), "invalid_target_index")
			})
		}
	}
}

func TestFileCommandsRejectTargetIndexSelectorConflicts(t *testing.T) {
	uploadPath := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(uploadPath, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatalf("WriteFile upload returned error: %v", err)
	}
	commands := [][]string{
		{"file", "input#upload", uploadPath},
		{"file", "chooser", "247", uploadPath},
	}
	for _, command := range commands {
		name := strings.Join(command[:2], "-")
		t.Run(name, func(t *testing.T) {
			args := append([]string{}, command...)
			args = append(args, "--target-index", "1", "--target", "page-one", "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s target conflict exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "invalid_target_selector")
		})
	}
}

func TestFileCommandSelectsIndexedPageAndPreservesTrial(t *testing.T) {
	uploadPath := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(uploadPath, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatalf("WriteFile upload returned error: %v", err)
	}
	for _, trial := range []bool{false, true} {
		t.Run(map[bool]string{false: "set", true: "trial"}[trial], func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := []string{"file", "input#upload", uploadPath, "--target-index", "2", "--state-dir", stateDir, "--json"}
			if trial {
				args = append(args, "--trial")
			}
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("file indexed trial=%t exit=%d stdout=%s stderr=%s", trial, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode file indexed trial=%t: %v; output=%s", trial, err, out.String())
			}
			target, targetOK := result["target"].(map[string]any)
			file, fileOK := result["file"].(map[string]any)
			fileSet, _ := file["file_set"].(bool)
			fileTrial, _ := file["trial"].(bool)
			if result["ok"] != true || !targetOK || target["id"] != "page-two" || result["target_index"] != float64(2) || !fileOK || file["accepted"] != true || file["content_omitted"] != true || fileSet != !trial || fileTrial != trial {
				t.Fatalf("file indexed trial=%t result=%#v, want page-two and preserved file state", trial, result)
			}
			if strings.Contains(out.String(), "synthetic upload") {
				t.Fatalf("file indexed output leaked synthetic file contents: %s", out.String())
			}
		})
	}
}

func TestFileChooserSelectsIndexedPageAndPreservesTrial(t *testing.T) {
	firstPath := filepath.Join(t.TempDir(), "first.epub")
	secondPath := filepath.Join(t.TempDir(), "second.epub")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("synthetic upload"), 0o600); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", path, err)
		}
	}
	for _, trial := range []bool{false, true} {
		t.Run(map[bool]string{false: "set", true: "trial"}[trial], func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
				{"targetId": "worker-between", "type": "worker", "title": "Worker", "url": "https://example.test/worker"},
				{"targetId": "page-two", "type": "page", "title": "Second", "url": "https://example.test/second"},
			})
			defer server.Close()
			stateDir := startFakeDaemon(t, server, "browser_url")

			args := []string{"file", "chooser", "247", firstPath, secondPath, "--target-index", "2", "--state-dir", stateDir, "--json"}
			if trial {
				args = append(args, "--trial")
			}
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("file chooser indexed trial=%t exit=%d stdout=%s stderr=%s", trial, code, out.String(), errOut.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode file chooser indexed trial=%t: %v; output=%s", trial, err, out.String())
			}
			target, targetOK := result["target"].(map[string]any)
			chooser, chooserOK := result["file_chooser"].(map[string]any)
			filesSet, _ := chooser["files_set"].(bool)
			chooserTrial, _ := chooser["trial"].(bool)
			if result["ok"] != true || !targetOK || target["id"] != "page-two" || result["target_index"] != float64(2) || !chooserOK || chooser["backend_node_id"] != float64(247) || chooser["file_count"] != float64(2) || filesSet != !trial || chooserTrial != trial || chooser["content_omitted"] != true {
				t.Fatalf("file chooser indexed trial=%t result=%#v, want page-two and preserved chooser state", trial, result)
			}
			if strings.Contains(out.String(), "synthetic upload") {
				t.Fatalf("file chooser indexed output leaked synthetic file contents: %s", out.String())
			}
		})
	}
}

func TestFileCommandsReportOutOfRangeTargetIndex(t *testing.T) {
	uploadPath := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(uploadPath, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatalf("WriteFile upload returned error: %v", err)
	}
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-one", "type": "page", "title": "First", "url": "https://example.test/first"},
	})
	defer server.Close()
	stateDir := startFakeDaemon(t, server, "browser_url")

	commands := [][]string{
		{"file", "input#upload", uploadPath},
		{"file", "chooser", "247", uploadPath},
	}
	for _, command := range commands {
		name := strings.Join(command[:2], "-")
		t.Run(name, func(t *testing.T) {
			args := append([]string{}, command...)
			args = append(args, "--target-index", "2", "--state-dir", stateDir, "--json")
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s out-of-range exit=%d stdout=%s stderr=%s", name, code, out.String(), errOut.String())
			}
			assertTargetIndexError(t, out.Bytes(), "target_not_found")
		})
	}
}
