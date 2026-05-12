package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestStorageListAndSnapshotJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"storage", "list", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("storage list exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK      bool `json:"ok"`
		Storage struct {
			LocalStorage struct {
				Count   int `json:"count"`
				Entries []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"entries"`
			} `json:"local_storage"`
			SessionStorage struct {
				Keys []string `json:"keys"`
			} `json:"session_storage"`
			Cookies []map[string]any `json:"cookies"`
			Quota   map[string]any   `json:"quota"`
		} `json:"storage"`
		CollectorErrors []map[string]string `json:"collector_errors"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("storage list output is invalid JSON: %v", err)
	}
	if !got.OK || got.Storage.LocalStorage.Count != 2 || got.Storage.LocalStorage.Entries[0].Key != "authToken" || got.Storage.LocalStorage.Entries[0].Value != "secret" || len(got.Storage.Cookies) != 1 || len(got.CollectorErrors) != 0 {
		t.Fatalf("storage list = %+v, want unredacted local forensic storage", got)
	}
	if got.Storage.Quota["usage"] == nil || !containsString(got.Storage.SessionStorage.Keys, "nonce") {
		t.Fatalf("storage list quota/session = %+v / %+v, want quota and session key", got.Storage.Quota, got.Storage.SessionStorage.Keys)
	}

	out.Reset()
	errOut.Reset()
	outPath := filepath.Join(t.TempDir(), "storage.local.json")
	code = cli.Execute(context.Background(), []string{"storage", "snapshot", "--redact", "safe", "--out", outPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("storage snapshot exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var snap struct {
		Snapshot struct {
			LocalStorage struct {
				Entries []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"entries"`
			} `json:"local_storage"`
			SessionStorage struct {
				Entries []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"entries"`
			} `json:"session_storage"`
			Cookies []map[string]any `json:"cookies"`
		} `json:"snapshot"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &snap); err != nil {
		t.Fatalf("storage snapshot output is invalid JSON: %v", err)
	}
	if snap.Artifact.Path != outPath {
		t.Fatalf("storage snapshot artifact = %+v, want %q", snap.Artifact, outPath)
	}
	for _, entry := range snap.Snapshot.LocalStorage.Entries {
		if entry.Value != "<redacted>" {
			t.Fatalf("localStorage entry %q value = %q, want redacted", entry.Key, entry.Value)
		}
	}
	for _, entry := range snap.Snapshot.SessionStorage.Entries {
		if entry.Value != "<redacted>" {
			t.Fatalf("sessionStorage entry %q value = %q, want redacted", entry.Key, entry.Value)
		}
	}
	if snap.Snapshot.Cookies[0]["value"] != "<redacted>" {
		t.Fatalf("storage snapshot cookies = %+v, want redacted values", snap.Snapshot.Cookies)
	}
}

func TestStorageWebStorageMutationJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "get", args: []string{"storage", "get", "localStorage", "feature", "--json"}},
		{name: "set", args: []string{"storage", "set", "localStorage", "feature", "disabled", "--json"}},
		{name: "delete", args: []string{"storage", "delete", "sessionStorage", "nonce", "--json"}},
		{name: "clear", args: []string{"storage", "clear", "sessionStorage", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), tc.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("%s exit code = %d, want %d; stdout=%s stderr=%s", tc.name, code, cli.ExitOK, out.String(), errOut.String())
			}
			var got struct {
				OK      bool `json:"ok"`
				Storage struct {
					Backend string `json:"backend"`
				} `json:"storage"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("%s output is invalid JSON: %v", tc.name, err)
			}
			if !got.OK || got.Storage.Backend == "" {
				t.Fatalf("%s output = %+v, want storage operation result", tc.name, got)
			}
		})
	}
}

func TestStorageCookiesAndDiffJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	for _, args := range [][]string{
		{"storage", "cookies", "list", "--json"},
		{"storage", "cookies", "set", "--name", "feature", "--value", "enabled", "--json"},
		{"storage", "cookies", "delete", "--name", "feature", "--json"},
	} {
		var out, errOut bytes.Buffer
		code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("%v exit code = %d, want %d; stdout=%s stderr=%s", args, code, cli.ExitOK, out.String(), errOut.String())
		}
		var got struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("%v output is invalid JSON: %v", args, err)
		}
		if !got.OK {
			t.Fatalf("%v output = %+v, want ok", args, got)
		}
	}

	dir := t.TempDir()
	left := filepath.Join(dir, "left.json")
	right := filepath.Join(dir, "right.json")
	if err := os.WriteFile(left, []byte(`{"snapshot":{"local_storage":{"entries":[{"key":"feature","value":"enabled"}]},"session_storage":{"entries":[]},"cookies":[]}}`), 0o600); err != nil {
		t.Fatalf("write left snapshot: %v", err)
	}
	if err := os.WriteFile(right, []byte(`{"snapshot":{"local_storage":{"entries":[{"key":"feature","value":"disabled"},{"key":"new","value":"yes"}]},"session_storage":{"entries":[]},"cookies":[]}}`), 0o600); err != nil {
		t.Fatalf("write right snapshot: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"storage", "diff", "--left", left, "--right", right, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("storage diff exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var diff struct {
		HasDiff bool `json:"has_diff"`
		Diff    struct {
			Summary map[string]int `json:"summary"`
		} `json:"diff"`
	}
	if err := json.Unmarshal(out.Bytes(), &diff); err != nil {
		t.Fatalf("storage diff output is invalid JSON: %v", err)
	}
	if !diff.HasDiff || diff.Diff.Summary["added"] != 1 || diff.Diff.Summary["changed"] != 1 {
		t.Fatalf("storage diff = %+v, want one added and one changed", diff)
	}
}

func TestStorageIndexedDBDumpJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "dump.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"storage", "indexeddb", "dump", "cdp-demo-db", "settings",
		"--page-size", "2",
		"--out", outPath,
		"--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("indexeddb dump exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK      bool `json:"ok"`
		Storage struct {
			Operation  string `json:"operation"`
			Database   string `json:"database"`
			Store      string `json:"store"`
			Count      int    `json:"count"`
			Limit      int    `json:"limit"`
			PageSize   int    `json:"page_size"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
			Records    []struct {
				Key   string         `json:"key"`
				Value map[string]any `json:"value"`
			} `json:"records"`
		} `json:"storage"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("indexeddb dump output is invalid JSON: %v", err)
	}
	if !got.OK || got.Storage.Operation != "dump" || got.Storage.Database != "cdp-demo-db" || got.Storage.Store != "settings" || got.Storage.Count != 2 || got.Storage.Limit != 2 || got.Storage.PageSize != 2 || !got.Storage.HasMore || got.Storage.NextCursor == "" || got.Artifact.Path != outPath {
		t.Fatalf("indexeddb dump = %+v, want paginated dump artifact", got)
	}
	if len(got.Storage.Records) != 2 || got.Storage.Records[0].Key != "feature" || got.Storage.Records[0].Value["enabled"] != true {
		t.Fatalf("indexeddb dump records = %+v, want keys and values", got.Storage.Records)
	}
}
