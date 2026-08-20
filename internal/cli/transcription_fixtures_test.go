package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type transcriptionFixtureManifest struct {
	SchemaVersion string                      `json:"schema_version"`
	Count         int                         `json:"count"`
	Entries       []transcriptionFixtureEntry `json:"entries"`
}

type transcriptionFixtureEntry struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	WebM       string `json:"webm"`
	WebMBytes  int64  `json:"webm_bytes"`
	WebMSHA256 string `json:"webm_sha256"`
}

func TestCheckedInTranscriptionFixtureCorpus(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..", "testdata", "transcription-fixtures")
	manifestBytes, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest transcriptionFixtureManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != "cdp-cli-transcription-fixtures/v2" {
		t.Fatalf("schema version = %q", manifest.SchemaVersion)
	}
	if manifest.Count != 100 || len(manifest.Entries) != 100 {
		t.Fatalf("fixture count = %d/%d, want 100/100", manifest.Count, len(manifest.Entries))
	}

	seenIDs := make(map[string]struct{}, len(manifest.Entries))
	seenTexts := make(map[string]struct{}, len(manifest.Entries))
	for index, entry := range manifest.Entries {
		wantID := fmt.Sprintf("fixture-%03d", index+1)
		if entry.ID != wantID {
			t.Errorf("entry %d id = %q, want %q", index, entry.ID, wantID)
		}
		if entry.Text == "" {
			t.Errorf("entry %s has empty source text", entry.ID)
		}
		if _, ok := seenIDs[entry.ID]; ok {
			t.Errorf("duplicate fixture id %q", entry.ID)
		}
		if _, ok := seenTexts[entry.Text]; ok {
			t.Errorf("duplicate fixture text for %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		seenTexts[entry.Text] = struct{}{}

		checkFixtureFile(t, root, entry.WebM, ".webm", entry.WebMBytes, entry.WebMSHA256, []byte{0x1a, 0x45, 0xdf, 0xa3})
	}
}

func checkFixtureFile(t *testing.T, root, name, extension string, expectedBytes int64, expectedHash string, prefix []byte) {
	t.Helper()
	if name != filepath.Base(name) || filepath.Ext(name) != extension {
		t.Fatalf("fixture path %q is not a safe %s filename", name, extension)
	}
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}
	if int64(len(data)) != expectedBytes || len(data) == 0 {
		t.Fatalf("fixture %q bytes = %d, manifest = %d", name, len(data), expectedBytes)
	}
	if !bytes.HasPrefix(data, prefix) {
		t.Fatalf("fixture %q does not have the expected container header", name)
	}
	hash := sha256.Sum256(data)
	if got := hex.EncodeToString(hash[:]); !strings.EqualFold(got, expectedHash) {
		t.Fatalf("fixture %q hash changed: got %s, manifest %s", name, got, expectedHash)
	}
}
